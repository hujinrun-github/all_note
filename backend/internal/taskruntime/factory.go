package taskruntime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/hujinrun/flowspace/internal/storage"
	storagepostgres "github.com/hujinrun/flowspace/internal/storage/postgres"
	storagesqlite "github.com/hujinrun/flowspace/internal/storage/sqlite"
	"github.com/hujinrun/flowspace/internal/taskapp"
	"github.com/hujinrun/flowspace/internal/taskdomain"
	"github.com/hujinrun/flowspace/internal/tenantruntime"
)

const ExpectedTenantSchemaVersion = "0003_task_domain_legacy_migration.sql"

var (
	ErrTaskDomainNotServingV2  = errors.New("workspace task domain is not serving v2")
	ErrTaskRuntimeType         = errors.New("tenant resource does not expose the task runtime contract")
	ErrTaskRuntimeClosed       = errors.New("task runtime resource is closed")
	ErrTaskRuntimeEpochChanged = errors.New("task runtime epoch changed")
	ErrTaskRuntimeModelChanged = errors.New("task runtime model changed")
)

type tenantStoreRegistry interface {
	OpenTenant(context.Context, storage.Config, string) (storage.Store, error)
}

type Factory struct {
	registry              tenantStoreRegistry
	endpoints             DatabaseEndpointConfigSource
	expectedSchemaVersion string
	postgresProvider      storagepostgres.Provider
	generationNudger      GenerationNudger
}

type FactoryOption func(*Factory) error

func WithGenerationNudger(nudger GenerationNudger) FactoryOption {
	return func(factory *Factory) error {
		if nudger == nil {
			return ErrTaskRuntimeType
		}
		factory.generationNudger = nudger
		return nil
	}
}

// NewFactory assembles the production tenant registry itself so PostgreSQL
// endpoint connections cannot be opened through the zero-value (trusted
// deployment database) provider. SQLite remains a deployment-local provider.
func NewFactory(endpoints DatabaseEndpointConfigSource, expectedSchemaVersion string, postgresDialContext storagepostgres.DialContextFunc, options ...FactoryOption) (*Factory, error) {
	postgresProvider, err := storagepostgres.NewProviderWithDialContext(postgresDialContext)
	if err != nil {
		return nil, err
	}
	registry := storage.NewRegistry()
	if err := registry.Register(postgresProvider); err != nil {
		return nil, err
	}
	if err := registry.Register(storagesqlite.Provider{}); err != nil {
		return nil, err
	}
	factory, err := newFactory(registry, endpoints, expectedSchemaVersion, options...)
	if err != nil {
		return nil, err
	}
	factory.postgresProvider = postgresProvider
	return factory, nil
}

func newFactory(registry tenantStoreRegistry, endpoints DatabaseEndpointConfigSource, expectedSchemaVersion string, options ...FactoryOption) (*Factory, error) {
	if registry == nil || endpoints == nil || strings.TrimSpace(expectedSchemaVersion) == "" {
		return nil, errors.New("tenant registry, database endpoint source, and schema version are required")
	}
	factory := &Factory{registry: registry, endpoints: endpoints, expectedSchemaVersion: expectedSchemaVersion}
	for _, option := range options {
		if option == nil {
			return nil, ErrTaskRuntimeType
		}
		if err := option(factory); err != nil {
			return nil, err
		}
	}
	return factory, nil
}

func (f *Factory) Build(ctx context.Context, snapshot tenantruntime.Snapshot) (tenantruntime.Resource, error) {
	if f == nil || f.registry == nil || f.endpoints == nil || strings.TrimSpace(snapshot.WorkspaceID) == "" ||
		snapshot.Epoch < 1 || snapshot.BindingRevision < 1 || strings.TrimSpace(snapshot.DatabaseEndpointID) == "" {
		return nil, ErrTaskRuntimeType
	}
	endpoint, err := f.endpoints.LoadDatabaseEndpointConfig(ctx, snapshot.WorkspaceID, snapshot.DatabaseEndpointID)
	if err != nil {
		return nil, err
	}
	if endpoint.WorkspaceID != snapshot.WorkspaceID || endpoint.EndpointID != snapshot.DatabaseEndpointID ||
		strings.TrimSpace(endpoint.ProfileVersionID) == "" {
		return nil, ErrDatabaseEndpointUnavailable
	}
	store, err := f.registry.OpenTenant(ctx, endpoint.Storage, f.expectedSchemaVersion)
	if err != nil {
		return nil, fmt.Errorf("open task tenant store: %w", err)
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = store.Close()
		}
	}()

	runtimeStore, ok := store.(storage.TaskDomainRuntimeStore)
	if !ok {
		return nil, ErrTaskRuntimeType
	}
	state, err := runtimeStore.LoadTaskDomainRuntimeState(ctx, snapshot.WorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("load task-domain runtime state: %w", err)
	}
	model, err := stableTaskModel(snapshot.WorkspaceID, state)
	if err != nil {
		return nil, err
	}
	// The control-plane runtime version and the tenant anchor are one fence.
	// Never combine a stale endpoint snapshot with a newer data-plane epoch.
	if state.Epoch != snapshot.Epoch {
		return nil, fmt.Errorf("%w: control=%d tenant=%d", ErrTaskRuntimeEpochChanged, snapshot.Epoch, state.Epoch)
	}
	if model == taskapp.ModelLegacy {
		resource := &legacyResource{
			store: store, runtimeStore: runtimeStore, workspaceID: snapshot.WorkspaceID, epoch: state.Epoch,
		}
		closeOnError = false
		return resource, nil
	}
	reader := runtimeStore.TaskDomainReader(snapshot.WorkspaceID)
	roadmapReader, ok := reader.(taskdomain.RoadmapReader)
	if !ok || roadmapReader == nil {
		return nil, ErrTaskRuntimeType
	}
	stateReader, ok := reader.(taskdomain.TaskAggregateStateReader)
	if !ok || stateReader == nil {
		return nil, ErrTaskRuntimeType
	}
	scheduleReader, ok := reader.(taskdomain.ScheduleCommandStateReader)
	if !ok || scheduleReader == nil {
		return nil, ErrTaskRuntimeType
	}

	writer, err := f.writerForEndpoint(endpoint.Storage)
	if err != nil {
		return nil, err
	}
	taskFencer := taskCommandFencer{writer: writer}
	projectFencer, ok := writer.(taskdomain.ProjectCommandFencer)
	if !ok {
		return nil, ErrTaskRuntimeType
	}
	roadmapFencer, ok := writer.(taskdomain.RoadmapCommandFencer)
	if !ok {
		return nil, ErrTaskRuntimeType
	}
	scheduleFencer, ok := writer.(taskdomain.ScheduleCommandFencer)
	if !ok {
		return nil, ErrTaskRuntimeType
	}
	generationFencer, ok := writer.(taskdomain.GenerationFencer)
	if !ok {
		return nil, ErrTaskRuntimeType
	}

	tasks := taskdomain.NewTaskService(taskFencer, stateReader)
	occurrences := taskdomain.NewOccurrenceService(taskFencer, stateReader)
	projects := taskdomain.NewProjectService(projectFencer)
	roadmaps := taskdomain.NewRoadmapService(roadmapFencer)
	schedules := taskdomain.NewScheduleService(scheduleFencer, scheduleReader)
	resource := &Resource{
		store:        store,
		runtimeStore: runtimeStore,
		workspaceID:  snapshot.WorkspaceID,
		epoch:        state.Epoch,
		generation:   generationFencer,
		application: taskapp.RuntimeSnapshot{
			WorkspaceID:   snapshot.WorkspaceID,
			Epoch:         state.Epoch,
			Factory:       taskdomain.TaskFactory{},
			Tasks:         taskServiceAdapter{delegate: tasks, generation: f.generationNudger},
			Occurrences:   occurrenceServiceAdapter{delegate: occurrences},
			Projects:      projectServiceAdapter{delegate: projects},
			Roadmaps:      roadmaps,
			RoadmapReader: roadmapReader,
			Schedules:     scheduleServiceAdapter{delegate: schedules, generation: f.generationNudger},
			Reader:        reader,
		},
	}
	closeOnError = false
	return resource, nil
}

func validateServingState(workspaceID string, state storage.TaskDomainRuntimeState) error {
	model, err := stableTaskModel(workspaceID, state)
	if err != nil {
		return err
	}
	if model != taskapp.ModelV2 {
		return fmt.Errorf("%w: model=%s migration=%s anchor=%s", ErrTaskDomainNotServingV2,
			state.ModelVersion, state.MigrationState, state.AnchorState)
	}
	return nil
}

func stableTaskModel(workspaceID string, state storage.TaskDomainRuntimeState) (taskapp.ModelVersion, error) {
	if state.WorkspaceID != workspaceID || state.Epoch < 1 || state.AnchorState != "active" {
		return "", fmt.Errorf("%w: model=%s migration=%s anchor=%s", ErrTaskDomainNotServingV2,
			state.ModelVersion, state.MigrationState, state.AnchorState)
	}
	switch {
	case state.ModelVersion == string(taskapp.ModelLegacy) && state.MigrationState == "idle":
		return taskapp.ModelLegacy, nil
	case state.ModelVersion == string(taskapp.ModelV2) && (state.MigrationState == "idle" || state.MigrationState == "cutover"):
		return taskapp.ModelV2, nil
	default:
		return "", fmt.Errorf("%w: model=%s migration=%s anchor=%s", ErrTaskDomainNotServingV2,
			state.ModelVersion, state.MigrationState, state.AnchorState)
	}
}

func (f *Factory) writerForEndpoint(cfg storage.Config) (storage.TenantFencedWriter, error) {
	switch cfg.Driver {
	case storage.DriverPostgres:
		return f.postgresProvider.NewTenantWriter(cfg), nil
	case storage.DriverSQLite:
		return storagesqlite.NewTenantWriter(cfg), nil
	default:
		return nil, ErrDatabaseEndpointUnavailable
	}
}

type taskCommandFencer struct{ writer storage.TenantFencedWriter }

func (f taskCommandFencer) BeginFencedWrite(ctx context.Context, workspaceID string, epoch int64, fn func(taskdomain.TaskDomainFencedTx) error) error {
	if f.writer == nil || fn == nil {
		return taskdomain.ErrInvalidTaskCommand
	}
	return f.writer.BeginFencedWrite(ctx, workspaceID, epoch, func(tx storage.TenantWriteTx) error {
		return fn(tx)
	})
}

type Resource struct {
	mu           sync.RWMutex
	store        storage.Store
	runtimeStore storage.TaskDomainRuntimeStore
	workspaceID  string
	epoch        int64
	application  taskapp.RuntimeSnapshot
	generation   taskdomain.GenerationFencer
	closeOnce    sync.Once
	closeErr     error
	closed       bool
}

func (r *Resource) ApplicationSnapshot(ctx context.Context) (taskapp.RuntimeSnapshot, error) {
	if r == nil {
		return taskapp.RuntimeSnapshot{}, ErrTaskRuntimeClosed
	}
	r.mu.RLock()
	if r.closed || r.runtimeStore == nil {
		r.mu.RUnlock()
		return taskapp.RuntimeSnapshot{}, ErrTaskRuntimeClosed
	}
	runtimeStore := r.runtimeStore
	workspaceID := r.workspaceID
	epoch := r.epoch
	application := r.application
	r.mu.RUnlock()

	model, err := loadVerifiedDurableModel(ctx, runtimeStore, workspaceID, epoch, taskapp.ModelV2)
	if err != nil {
		return taskapp.RuntimeSnapshot{}, err
	}
	if model != taskapp.ModelV2 {
		return taskapp.RuntimeSnapshot{}, ErrTaskRuntimeModelChanged
	}
	return application, nil
}

// GenerationSnapshot exposes only the transaction-scoped generation fencer.
// The complete tenant store and ordinary writable repositories remain hidden.
// Durable epoch/model state is revalidated for every claimed cycle.
func (r *Resource) GenerationSnapshot(ctx context.Context) (taskdomain.GenerationRuntimeSnapshot, error) {
	if r == nil {
		return taskdomain.GenerationRuntimeSnapshot{}, ErrTaskRuntimeClosed
	}
	r.mu.RLock()
	if r.closed || r.runtimeStore == nil || r.generation == nil {
		r.mu.RUnlock()
		return taskdomain.GenerationRuntimeSnapshot{}, ErrTaskRuntimeClosed
	}
	runtimeStore := r.runtimeStore
	workspaceID := r.workspaceID
	epoch := r.epoch
	generation := r.generation
	r.mu.RUnlock()

	model, err := loadVerifiedDurableModel(ctx, runtimeStore, workspaceID, epoch, taskapp.ModelV2)
	if err != nil {
		return taskdomain.GenerationRuntimeSnapshot{}, err
	}
	if model != taskapp.ModelV2 {
		return taskdomain.GenerationRuntimeSnapshot{}, ErrTaskRuntimeModelChanged
	}
	return taskdomain.GenerationRuntimeSnapshot{WorkspaceID: workspaceID, Epoch: epoch, Fencer: generation}, nil
}

func (r *Resource) TaskDomainModel(ctx context.Context) (taskapp.ModelVersion, error) {
	if r == nil {
		return "", ErrTaskRuntimeClosed
	}
	r.mu.RLock()
	if r.closed || r.runtimeStore == nil {
		r.mu.RUnlock()
		return "", ErrTaskRuntimeClosed
	}
	runtimeStore := r.runtimeStore
	workspaceID := r.workspaceID
	epoch := r.epoch
	r.mu.RUnlock()
	return loadVerifiedDurableModel(ctx, runtimeStore, workspaceID, epoch, taskapp.ModelV2)
}

func (r *Resource) Close() error {
	if r == nil {
		return nil
	}
	r.closeOnce.Do(func() {
		r.mu.Lock()
		r.closed = true
		store := r.store
		r.store = nil
		r.runtimeStore = nil
		r.generation = nil
		r.mu.Unlock()
		if store != nil {
			r.closeErr = store.Close()
		}
	})
	return r.closeErr
}

type legacyResource struct {
	mu           sync.RWMutex
	store        storage.Store
	runtimeStore storage.TaskDomainRuntimeStore
	workspaceID  string
	epoch        int64
	closeOnce    sync.Once
	closeErr     error
	closed       bool
}

func (r *legacyResource) TaskDomainModel(ctx context.Context) (taskapp.ModelVersion, error) {
	if r == nil {
		return "", ErrTaskRuntimeClosed
	}
	r.mu.RLock()
	if r.closed || r.runtimeStore == nil {
		r.mu.RUnlock()
		return "", ErrTaskRuntimeClosed
	}
	runtimeStore := r.runtimeStore
	workspaceID := r.workspaceID
	epoch := r.epoch
	r.mu.RUnlock()
	return loadVerifiedDurableModel(ctx, runtimeStore, workspaceID, epoch, taskapp.ModelLegacy)
}

func (r *legacyResource) Close() error {
	if r == nil {
		return nil
	}
	r.closeOnce.Do(func() {
		r.mu.Lock()
		r.closed = true
		store := r.store
		r.store = nil
		r.runtimeStore = nil
		r.mu.Unlock()
		if store != nil {
			r.closeErr = store.Close()
		}
	})
	return r.closeErr
}

func loadVerifiedDurableModel(ctx context.Context, runtimeStore storage.TaskDomainRuntimeStore, workspaceID string, cachedEpoch int64, cachedModel taskapp.ModelVersion) (taskapp.ModelVersion, error) {
	state, err := runtimeStore.LoadTaskDomainRuntimeState(ctx, workspaceID)
	if err != nil {
		return "", err
	}
	model, err := stableTaskModel(workspaceID, state)
	if err != nil {
		return "", err
	}
	if state.Epoch != cachedEpoch {
		return "", fmt.Errorf("%w: cached=%d durable=%d", ErrTaskRuntimeEpochChanged, cachedEpoch, state.Epoch)
	}
	if model != cachedModel {
		return "", fmt.Errorf("%w: cached=%s durable=%s", ErrTaskRuntimeModelChanged, cachedModel, model)
	}
	return model, nil
}

var _ tenantruntime.Factory = (*Factory)(nil)
var _ tenantruntime.Resource = (*Resource)(nil)
var _ tenantruntime.Resource = (*legacyResource)(nil)
var _ taskdomain.TaskDomainCommandFencer = taskCommandFencer{}
