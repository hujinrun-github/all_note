package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/config"
	"github.com/hujinrun/flowspace/internal/runtimecontrol"
	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/taskmigration"
)

const backendHealthURL = "http://backend:4201/api/health"

var errBackendStillReachable = errors.New("backend is still reachable")

type taskCutoverStoreOpener interface {
	tenantStoreOpener
	OpenControl(context.Context, storage.Config) (storage.Store, error)
}

type taskCutoverOfflineGate interface {
	taskmigration.MobileCutoverPreflight
	taskmigration.OldWriterHeartbeatCounter
}

type taskMigrationCutoverOptions struct {
	WorkspaceID        string
	MigrationID        string
	OwnerTimezone      string
	DeploymentTimezone string
	RoutingEnabled     bool
	OfflineGate        taskCutoverOfflineGate
}

type taskDomainV2Capability bool

func (capability taskDomainV2Capability) SupportsTaskDomainV2Schema() bool {
	return bool(capability)
}

type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// singleInstanceOfflineGate turns the deployment's stopped backend into a
// concrete proof for both the mobile-v1 shutdown and old-writer gates. It is
// intentionally limited to the single-instance cutover workflow.
type singleInstanceOfflineGate struct {
	healthURL string
	client    httpDoer
}

func newSingleInstanceOfflineGate() *singleInstanceOfflineGate {
	return &singleInstanceOfflineGate{
		healthURL: backendHealthURL,
		client:    &http.Client{Timeout: 2 * time.Second},
	}
}

func (gate *singleInstanceOfflineGate) Preflight() error {
	if gate == nil || strings.TrimSpace(gate.healthURL) == "" || gate.client == nil {
		return errors.New("backend offline probe is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, gate.healthURL, nil)
	if err != nil {
		return fmt.Errorf("construct backend offline probe: %w", err)
	}
	response, err := gate.client.Do(request)
	if err != nil {
		// DNS failure, connection refusal, and timeout all prove that the known
		// single backend endpoint is not accepting requests.
		return nil
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	return fmt.Errorf("%w at %s", errBackendStillReachable, gate.healthURL)
}

func (gate *singleInstanceOfflineGate) CountOldWriterHeartbeats(context.Context, string) (int, error) {
	if err := gate.Preflight(); err != nil {
		if errors.Is(err, errBackendStillReachable) {
			return 1, nil
		}
		return 0, err
	}
	return 0, nil
}

func cutoverTaskMigration(
	ctx context.Context,
	cfg config.RuntimeStorageConfig,
	opener taskCutoverStoreOpener,
	options taskMigrationCutoverOptions,
) error {
	if cfg.InstanceMode != config.InstanceModeSingle {
		return fmt.Errorf("task-domain cutover requires FLOWSPACE_INSTANCE_MODE=single")
	}
	if !options.RoutingEnabled {
		return fmt.Errorf("task-domain cutover requires FLOWSPACE_ENABLE_TASK_DOMAIN_V2_ROUTING=true")
	}
	if options.OfflineGate == nil {
		return fmt.Errorf("task-domain cutover requires a backend offline gate")
	}
	if err := options.OfflineGate.Preflight(); err != nil {
		return fmt.Errorf("task-domain cutover requires the backend service to be stopped: %w", err)
	}

	tenant, closeTenant, tenantDialect, err := openTaskMigrationStore(ctx, cfg, opener)
	if err != nil {
		return err
	}
	defer closeTenant()

	stateStore, err := taskmigration.NewStateStore(tenant.SQLDB(), tenantDialect)
	if err != nil {
		return err
	}
	state, err := stateStore.Load(ctx, options.WorkspaceID)
	if err != nil {
		return fmt.Errorf("load task-domain cutover state: %w", err)
	}
	observer, err := taskmigration.NewDBFinalCutoverObserver(taskmigration.DBFinalCutoverObserverConfig{
		DB:                 tenant.SQLDB(),
		Dialect:            tenantDialect,
		OwnerTimezone:      options.OwnerTimezone,
		DeploymentTimezone: options.DeploymentTimezone,
	})
	if err != nil {
		return err
	}
	service, err := taskmigration.NewCutoverService(taskmigration.CutoverServiceDependencies{
		StateStore:  stateStore,
		Observer:    observer,
		Mobile:      options.OfflineGate,
		Heartbeats:  options.OfflineGate,
		Application: taskDomainV2Capability(options.RoutingEnabled),
	})
	if err != nil {
		return err
	}
	result, err := service.Execute(
		ctx,
		options.WorkspaceID,
		state.Revision,
		state.WriteEpoch,
		options.MigrationID,
	)
	if err != nil {
		return fmt.Errorf("execute task-domain v2 cutover: %w", err)
	}

	control, err := opener.OpenControl(ctx, toStorageConfig(cfg.Environment, cfg.Control))
	if err != nil {
		return fmt.Errorf("open control store for epoch synchronization: %w", err)
	}
	defer control.Close()
	controlSQL, ok := control.(storage.SQLStore)
	if !ok || controlSQL.SQLDB() == nil {
		return fmt.Errorf("control store does not expose SQL access")
	}
	controlState, advanced, err := synchronizeControlEpoch(
		ctx,
		controlSQL.SQLDB(),
		cfg.Control.Driver,
		options.WorkspaceID,
		result.State.WriteEpoch,
	)
	if err != nil {
		return err
	}

	fmt.Printf(
		"workspace=%s model=%s state=%s migration=%s tenant_revision=%d tenant_epoch=%d control_epoch=%d cutover_applied=%t cutover_already_applied=%t control_epoch_advanced=%t\n",
		result.State.WorkspaceID,
		result.State.ModelVersion,
		result.State.MigrationState,
		result.State.MigrationID,
		result.State.Revision,
		result.State.WriteEpoch,
		controlState.Epoch,
		result.Applied,
		result.AlreadyApplied,
		advanced,
	)
	return nil
}

func synchronizeControlEpoch(
	ctx context.Context,
	db *sql.DB,
	driver config.DatabaseDriver,
	workspaceID string,
	tenantEpoch uint64,
) (runtimecontrol.State, bool, error) {
	if ctx == nil || db == nil || strings.TrimSpace(workspaceID) == "" || tenantEpoch == 0 || tenantEpoch > math.MaxInt64 {
		return runtimecontrol.State{}, false, fmt.Errorf("invalid control epoch synchronization input")
	}
	dialect, placeholder, err := controlRuntimeDialect(driver)
	if err != nil {
		return runtimecontrol.State{}, false, err
	}
	repository, err := runtimecontrol.New(db, dialect)
	if err != nil {
		return runtimecontrol.State{}, false, err
	}
	state, err := repository.Get(ctx, workspaceID)
	if err != nil {
		return runtimecontrol.State{}, false, fmt.Errorf("load control runtime state: %w", err)
	}
	if state.Mode != "active" || state.OperationID != "" {
		return runtimecontrol.State{}, false, fmt.Errorf(
			"control runtime state must be active without a storage operation; mode=%s operation=%q",
			state.Mode,
			state.OperationID,
		)
	}
	targetEpoch := int64(tenantEpoch)
	if state.Epoch == targetEpoch {
		return state, false, nil
	}
	if state.Epoch <= 0 || state.Epoch+1 != targetEpoch {
		return runtimecontrol.State{}, false, fmt.Errorf(
			"control epoch %d cannot be synchronized to tenant epoch %d",
			state.Epoch,
			targetEpoch,
		)
	}

	var actorUserID string
	if err := db.QueryRowContext(
		ctx,
		`SELECT owner_user_id FROM workspaces WHERE id=`+placeholder,
		workspaceID,
	).Scan(&actorUserID); err != nil {
		return runtimecontrol.State{}, false, fmt.Errorf("load workspace owner for control epoch audit: %w", err)
	}
	actorUserID = strings.TrimSpace(actorUserID)
	if actorUserID == "" {
		return runtimecontrol.State{}, false, fmt.Errorf("workspace owner for control epoch audit is empty")
	}
	advanced, err := repository.AdvanceEpoch(
		ctx,
		workspaceID,
		state.Epoch,
		state.BindingRevision,
		actorUserID,
	)
	if err != nil {
		return runtimecontrol.State{}, false, fmt.Errorf("advance control runtime epoch: %w", err)
	}
	if advanced.Epoch != targetEpoch || advanced.Mode != "active" {
		return runtimecontrol.State{}, false, fmt.Errorf(
			"control epoch synchronization returned mode=%s epoch=%d, want active/%d",
			advanced.Mode,
			advanced.Epoch,
			targetEpoch,
		)
	}
	return advanced, true, nil
}

func controlRuntimeDialect(driver config.DatabaseDriver) (runtimecontrol.Dialect, string, error) {
	switch driver {
	case config.DatabaseDriverPostgres:
		return runtimecontrol.DialectPostgres, "$1", nil
	case config.DatabaseDriverSQLite:
		return runtimecontrol.DialectSQLite, "?", nil
	default:
		return "", "", fmt.Errorf("unsupported control database driver %q", driver)
	}
}
