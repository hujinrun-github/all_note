package taskmigration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

var (
	ErrInvalidMigrationCoordinator = errors.New("invalid task-domain migration coordinator")
	ErrMigrationCoordinatorStopped = errors.New("task-domain migration coordinator stopped")
)

// MigrationCoordinatorFaultPoint names only committed phase boundaries. A
// fault injected at one of these points simulates a process which disappeared
// after the durable work completed but before the caller observed success.
type MigrationCoordinatorFaultPoint string

const (
	MigrationCoordinatorFaultAfterSnapshot MigrationCoordinatorFaultPoint = "after_snapshot"
	MigrationCoordinatorFaultAfterReplay   MigrationCoordinatorFaultPoint = "after_replay_page"
	MigrationCoordinatorFaultAfterDrain    MigrationCoordinatorFaultPoint = "after_drain"
	MigrationCoordinatorFaultBeforeReady   MigrationCoordinatorFaultPoint = "before_ready"
)

type MigrationCoordinatorFaultInjector interface {
	Inject(context.Context, MigrationCoordinatorFaultPoint) error
}

type MigrationCoordinatorConfig struct {
	DB                 *sql.DB
	Dialect            Dialect
	WorkspaceID        string
	MigrationID        string
	MigrationTimezone  string
	OwnerTimezone      string
	DeploymentTimezone string
	ReplayPageSize     int
	MaximumSteps       int
	Now                func() time.Time
	Faults             MigrationCoordinatorFaultInjector
}

// MigrationCoordinator is an explicit operator-owned data-plane object. It is
// intentionally absent from application startup and route registration. The
// concrete constructor wires the durable stores used by every phase; callers
// cannot acknowledge a phase through an in-memory callback.
type MigrationCoordinator struct {
	db          *sql.DB
	dialect     Dialect
	workspaceID string
	migrationID string
	timezone    string
	pageSize    int
	maxSteps    int
	now         func() time.Time
	faults      MigrationCoordinatorFaultInjector

	state     *StateStore
	backfill  *BackfillStore
	replay    *ReplayStore
	projector *ReplayProjectionApplier
	drain     *DrainStore
	reconcile *ReconcileStore
	writer    *V2ProjectionWriter
	loader    *LegacySnapshotLoader
}

func NewMigrationCoordinator(config MigrationCoordinatorConfig) (*MigrationCoordinator, error) {
	config.WorkspaceID = strings.TrimSpace(config.WorkspaceID)
	config.MigrationID = strings.TrimSpace(config.MigrationID)
	config.MigrationTimezone = strings.TrimSpace(config.MigrationTimezone)
	if config.DB == nil || (config.Dialect != DialectSQLite && config.Dialect != DialectPostgres) ||
		config.WorkspaceID == "" || config.MigrationID == "" || config.MigrationTimezone == "" {
		return nil, ErrInvalidMigrationCoordinator
	}
	if _, err := time.LoadLocation(config.MigrationTimezone); err != nil {
		return nil, fmt.Errorf("%w: invalid migration timezone", ErrInvalidMigrationCoordinator)
	}
	if config.ReplayPageSize == 0 {
		config.ReplayPageSize = 100
	}
	if config.MaximumSteps == 0 {
		config.MaximumSteps = 100000
	}
	if config.ReplayPageSize < 1 || config.MaximumSteps < 1 {
		return nil, ErrInvalidMigrationCoordinator
	}
	if config.Now == nil {
		config.Now = time.Now
	}

	state, err := NewStateStore(config.DB, config.Dialect)
	if err != nil {
		return nil, err
	}
	backfill, err := NewBackfillStore(config.DB, config.Dialect)
	if err != nil {
		return nil, err
	}
	replay, err := NewReplayStore(config.DB, config.Dialect)
	if err != nil {
		return nil, err
	}
	projector, err := NewReplayProjectionApplier(config.WorkspaceID, config.Dialect)
	if err != nil {
		return nil, err
	}
	drain, err := NewDrainStore(config.DB, config.Dialect)
	if err != nil {
		return nil, err
	}
	reconcile, err := NewReconcileStore(config.DB, config.Dialect)
	if err != nil {
		return nil, err
	}
	writer, err := NewV2ProjectionWriter(config.Dialect)
	if err != nil {
		return nil, err
	}
	loader, err := NewLegacySnapshotLoader(LegacySnapshotLoaderConfig{
		Dialect: config.Dialect, WorkspaceID: config.WorkspaceID,
		WorkspaceTimezone: config.MigrationTimezone,
		OwnerTimezone:     config.OwnerTimezone, DeploymentTimezone: config.DeploymentTimezone,
	})
	if err != nil {
		return nil, err
	}

	return &MigrationCoordinator{
		db: config.DB, dialect: config.Dialect, workspaceID: config.WorkspaceID,
		migrationID: config.MigrationID, timezone: config.MigrationTimezone,
		pageSize: config.ReplayPageSize, maxSteps: config.MaximumSteps,
		now: config.Now, faults: config.Faults,
		state: state, backfill: backfill, replay: replay, projector: projector,
		drain: drain, reconcile: reconcile, writer: writer, loader: loader,
	}, nil
}

type MigrationCoordinatorAction string

const (
	MigrationCoordinatorPrepared          MigrationCoordinatorAction = "prepared"
	MigrationCoordinatorSnapshotCommitted MigrationCoordinatorAction = "snapshot_committed"
	MigrationCoordinatorReplayCommitted   MigrationCoordinatorAction = "replay_page_committed"
	MigrationCoordinatorDrainCommitted    MigrationCoordinatorAction = "drain_committed"
	MigrationCoordinatorRepaired          MigrationCoordinatorAction = "reconcile_repaired"
	MigrationCoordinatorReady             MigrationCoordinatorAction = "ready"
	MigrationCoordinatorNoop              MigrationCoordinatorAction = "noop"
)

type MigrationCoordinatorStepResult struct {
	Action MigrationCoordinatorAction
	State  WorkspaceTaskDomainState
}

// MigrationCoordinatorExecutionError retains the last durable phase. It does
// not convert the workspace to failed or reopen a fence; an operator can retry
// the exact same phase after addressing the cause.
type MigrationCoordinatorExecutionError struct {
	Phase     MigrationState
	Operation string
	Cause     error
}

func (e *MigrationCoordinatorExecutionError) Error() string {
	return fmt.Sprintf("task-domain migration phase %s operation %s: %v", e.Phase, e.Operation, e.Cause)
}

func (e *MigrationCoordinatorExecutionError) Unwrap() error { return e.Cause }

// Step executes at most one durable boundary. It never performs cutover.
func (c *MigrationCoordinator) Step(ctx context.Context) (MigrationCoordinatorStepResult, error) {
	if c == nil || ctx == nil {
		return MigrationCoordinatorStepResult{}, ErrInvalidMigrationCoordinator
	}
	state, err := c.state.Load(ctx, c.workspaceID)
	if err != nil {
		return MigrationCoordinatorStepResult{}, c.executionError(MigrationStateIdle, "load_state", err)
	}
	if err := c.validateMigrationIdentity(state); err != nil {
		return MigrationCoordinatorStepResult{State: state}, c.executionError(state.MigrationState, "validate_identity", err)
	}

	switch state.MigrationState {
	case MigrationStateIdle:
		if state.ModelVersion == ModelVersionV2 {
			return MigrationCoordinatorStepResult{Action: MigrationCoordinatorNoop, State: state}, nil
		}
		return c.prepare(ctx, state)
	case MigrationStateBackfilling:
		return c.snapshot(ctx, state)
	case MigrationStateCatchingUp:
		return c.replayOrDrain(ctx, state)
	case MigrationStateDraining:
		return c.replayOrReconcile(ctx, state)
	case MigrationStateReady:
		return MigrationCoordinatorStepResult{Action: MigrationCoordinatorReady, State: state}, nil
	case MigrationStateCutover:
		return MigrationCoordinatorStepResult{Action: MigrationCoordinatorNoop, State: state}, nil
	case MigrationStateFailed:
		return MigrationCoordinatorStepResult{State: state}, c.executionError(state.MigrationState, "manual_recovery", ErrManualMigrationRecoveryRequired)
	default:
		return MigrationCoordinatorStepResult{State: state}, c.executionError(state.MigrationState, "dispatch", ErrInvalidMigrationCoordinator)
	}
}

// RunToReady repeatedly uses Step, which is also the crash-recovery path. It
// deliberately stops at legacy/ready. Domain version CAS remains an explicit
// CutoverService operation with its own mobile, heartbeat and application
// capability gates.
func (c *MigrationCoordinator) RunToReady(ctx context.Context) (WorkspaceTaskDomainState, error) {
	if c == nil || ctx == nil {
		return WorkspaceTaskDomainState{}, ErrInvalidMigrationCoordinator
	}
	for step := 0; step < c.maxSteps; step++ {
		result, err := c.Step(ctx)
		if err != nil {
			return result.State, err
		}
		if result.State.ModelVersion == ModelVersionV2 || result.State.MigrationState == MigrationStateReady {
			return result.State, nil
		}
		if err := ctx.Err(); err != nil {
			return result.State, err
		}
	}
	state, _ := c.state.Load(ctx, c.workspaceID)
	return state, c.executionError(state.MigrationState, "step_limit", ErrMigrationCoordinatorStopped)
}

// Cutover is intentionally separate from RunToReady. The supplied service is
// the only component allowed to perform the model-version CAS because it owns
// the final reconcile, mobile shutdown, old-writer heartbeat and application
// capability gates.
func (c *MigrationCoordinator) Cutover(ctx context.Context, service *CutoverService) (CutoverExecutionResult, error) {
	if c == nil || ctx == nil || service == nil {
		return CutoverExecutionResult{}, ErrInvalidMigrationCoordinator
	}
	state, err := c.state.Load(ctx, c.workspaceID)
	if err != nil {
		return CutoverExecutionResult{}, c.executionError(MigrationStateIdle, "load_cutover_state", err)
	}
	if err := c.validateMigrationIdentity(state); err != nil {
		return CutoverExecutionResult{}, c.executionError(state.MigrationState, "validate_cutover_identity", err)
	}
	result, err := service.Execute(ctx, c.workspaceID, state.Revision, state.WriteEpoch, c.migrationID)
	if err != nil {
		return result, c.executionError(state.MigrationState, "cutover", err)
	}
	return result, nil
}

func (c *MigrationCoordinator) prepare(ctx context.Context, state WorkspaceTaskDomainState) (MigrationCoordinatorStepResult, error) {
	// Trigger installation and baseline seeding come first. A crash here leaves
	// the durable coordinator idle and a harmless idempotent trigger set; the
	// reverse order would permit uncaptured writes in backfilling state.
	if err := InstallLegacyOutboxTriggers(ctx, c.db, c.dialect, TaskDomainSourceLegacyWorkspace); err != nil {
		return MigrationCoordinatorStepResult{State: state}, c.executionError(state.MigrationState, "install_outbox", err)
	}
	next, err := StartBackfill(state, StartBackfillCommand{
		ExpectedRevision: state.Revision, ExpectedWriteEpoch: state.WriteEpoch,
		MigrationID: c.migrationID, MigrationTimezone: c.timezone,
	})
	if err != nil {
		return MigrationCoordinatorStepResult{State: state}, c.executionError(state.MigrationState, "start_backfill", err)
	}
	if err := c.state.CompareAndSwap(ctx, state, next); err != nil {
		return MigrationCoordinatorStepResult{State: state}, c.executionError(state.MigrationState, "persist_backfill", err)
	}
	return MigrationCoordinatorStepResult{Action: MigrationCoordinatorPrepared, State: next}, nil
}

func (c *MigrationCoordinator) snapshot(ctx context.Context, state WorkspaceTaskDomainState) (MigrationCoordinatorStepResult, error) {
	result, err := c.backfill.RunSnapshotBackfill(
		ctx, c.workspaceID, state.Revision, state.WriteEpoch, c.now().UTC(), c.loader.Load, c.writer,
	)
	if err != nil {
		return MigrationCoordinatorStepResult{State: state}, c.executionError(state.MigrationState, "snapshot", err)
	}
	if err := c.inject(ctx, MigrationCoordinatorFaultAfterSnapshot); err != nil {
		return MigrationCoordinatorStepResult{Action: MigrationCoordinatorSnapshotCommitted, State: result.State}, c.executionError(result.State.MigrationState, "after_snapshot", err)
	}
	return MigrationCoordinatorStepResult{Action: MigrationCoordinatorSnapshotCommitted, State: result.State}, nil
}

func (c *MigrationCoordinator) replayOrDrain(ctx context.Context, state WorkspaceTaskDomainState) (MigrationCoordinatorStepResult, error) {
	page, err := c.replay.FetchPage(ctx, c.workspaceID, c.pageSize)
	if err != nil {
		return MigrationCoordinatorStepResult{State: state}, c.executionError(state.MigrationState, "fetch_replay", err)
	}
	if len(page.Events) != 0 {
		if err := c.replay.CommitPage(ctx, page, c.projector.Projector()); err != nil {
			return MigrationCoordinatorStepResult{State: state}, c.executionError(state.MigrationState, "commit_replay", err)
		}
		current, loadErr := c.state.Load(ctx, c.workspaceID)
		if loadErr != nil {
			return MigrationCoordinatorStepResult{State: state}, c.executionError(state.MigrationState, "reload_replay", loadErr)
		}
		if err := c.inject(ctx, MigrationCoordinatorFaultAfterReplay); err != nil {
			return MigrationCoordinatorStepResult{Action: MigrationCoordinatorReplayCommitted, State: current}, c.executionError(current.MigrationState, "after_replay", err)
		}
		return MigrationCoordinatorStepResult{Action: MigrationCoordinatorReplayCommitted, State: current}, nil
	}

	next, err := c.drain.BeginDrain(ctx, c.workspaceID, state.Revision, state.WriteEpoch, c.migrationID)
	if err != nil {
		return MigrationCoordinatorStepResult{State: state}, c.executionError(state.MigrationState, "begin_drain", err)
	}
	if err := c.inject(ctx, MigrationCoordinatorFaultAfterDrain); err != nil {
		return MigrationCoordinatorStepResult{Action: MigrationCoordinatorDrainCommitted, State: next}, c.executionError(next.MigrationState, "after_drain", err)
	}
	return MigrationCoordinatorStepResult{Action: MigrationCoordinatorDrainCommitted, State: next}, nil
}

func (c *MigrationCoordinator) replayOrReconcile(ctx context.Context, state WorkspaceTaskDomainState) (MigrationCoordinatorStepResult, error) {
	page, err := c.replay.FetchPage(ctx, c.workspaceID, c.pageSize)
	if err != nil {
		return MigrationCoordinatorStepResult{State: state}, c.executionError(state.MigrationState, "fetch_drain_replay", err)
	}
	if len(page.Events) != 0 {
		if err := c.replay.CommitPage(ctx, page, c.projector.Projector()); err != nil {
			return MigrationCoordinatorStepResult{State: state}, c.executionError(state.MigrationState, "commit_drain_replay", err)
		}
		current, loadErr := c.state.Load(ctx, c.workspaceID)
		if loadErr != nil {
			return MigrationCoordinatorStepResult{State: state}, c.executionError(state.MigrationState, "reload_drain_replay", loadErr)
		}
		if err := c.inject(ctx, MigrationCoordinatorFaultAfterReplay); err != nil {
			return MigrationCoordinatorStepResult{Action: MigrationCoordinatorReplayCommitted, State: current}, c.executionError(current.MigrationState, "after_drain_replay", err)
		}
		return MigrationCoordinatorStepResult{Action: MigrationCoordinatorReplayCommitted, State: current}, nil
	}

	projection, versions, err := c.captureFrozenProjection(ctx)
	if err != nil {
		return MigrationCoordinatorStepResult{State: state}, c.executionError(state.MigrationState, "capture_reconcile_source", err)
	}
	observation, err := c.reconcile.Observe(ctx, c.workspaceID, projection, versions)
	if err != nil {
		return MigrationCoordinatorStepResult{State: state}, c.executionError(state.MigrationState, "observe_reconcile", err)
	}
	if !observation.Plan.Ready {
		if _, err := c.reconcile.ApplyPlan(ctx, observation, c.now().UTC()); err != nil {
			return MigrationCoordinatorStepResult{State: state}, c.executionError(state.MigrationState, "repair_reconcile", err)
		}
		current, loadErr := c.state.Load(ctx, c.workspaceID)
		if loadErr != nil {
			return MigrationCoordinatorStepResult{State: state}, c.executionError(state.MigrationState, "reload_reconcile", loadErr)
		}
		return MigrationCoordinatorStepResult{Action: MigrationCoordinatorRepaired, State: current}, nil
	}

	current, err := c.state.Load(ctx, c.workspaceID)
	if err != nil {
		return MigrationCoordinatorStepResult{State: state}, c.executionError(state.MigrationState, "reload_before_ready", err)
	}
	if current.MigrationState != MigrationStateDraining || current.MigrationID != c.migrationID ||
		current.CutoverRevision == nil || current.SourceWatermark < *current.CutoverRevision {
		return MigrationCoordinatorStepResult{State: current}, c.executionError(current.MigrationState, "ready_fence", ErrStateCASConflict)
	}
	if err := c.inject(ctx, MigrationCoordinatorFaultBeforeReady); err != nil {
		return MigrationCoordinatorStepResult{State: current}, c.executionError(current.MigrationState, "before_ready", err)
	}
	next, err := MarkReady(current, MarkReadyCommand{
		ExpectedRevision: current.Revision, ExpectedWriteEpoch: current.WriteEpoch,
		SourceWatermark: current.SourceWatermark,
	})
	if err != nil {
		return MigrationCoordinatorStepResult{State: current}, c.executionError(current.MigrationState, "mark_ready", err)
	}
	if err := c.state.CompareAndSwap(ctx, current, next); err != nil {
		return MigrationCoordinatorStepResult{State: current}, c.executionError(current.MigrationState, "persist_ready", err)
	}
	return MigrationCoordinatorStepResult{Action: MigrationCoordinatorReady, State: next}, nil
}

func (c *MigrationCoordinator) captureFrozenProjection(ctx context.Context) (V2Projection, []ProjectionSourceVersion, error) {
	tx, err := c.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable, ReadOnly: true})
	if err != nil {
		return V2Projection{}, nil, fmt.Errorf("begin frozen projection snapshot: %w", err)
	}
	defer tx.Rollback()
	inventory, rows, err := c.loader.Load(ctx, tx)
	if err != nil {
		return V2Projection{}, nil, err
	}
	preflight, err := PreflightLegacyTaskDomain(inventory)
	if err != nil {
		return V2Projection{}, nil, err
	}
	if preflight.MigrationTimezone != c.timezone {
		return V2Projection{}, nil, fmt.Errorf("migration timezone changed: expected=%s actual=%s", c.timezone, preflight.MigrationTimezone)
	}
	projection, err := MapLegacyTaskDomain(preflight, rows)
	if err != nil {
		return V2Projection{}, nil, err
	}
	versions, err := c.loadProjectionSourceVersions(ctx, tx, projection)
	if err != nil {
		return V2Projection{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return V2Projection{}, nil, fmt.Errorf("commit frozen projection snapshot: %w", err)
	}
	return projection, versions, nil
}

func (c *MigrationCoordinator) loadProjectionSourceVersions(ctx context.Context, tx *sql.Tx, projection V2Projection) ([]ProjectionSourceVersion, error) {
	type sourceKey struct {
		kind LegacyEntityKind
		id   string
	}
	keys := make(map[sourceKey]struct{}, len(projection.IDMap))
	for _, entry := range projection.IDMap {
		key := sourceKey{kind: entry.LegacyKind, id: strings.TrimSpace(entry.LegacyID)}
		if !validLegacyProjectionKind(key.kind) || key.id == "" {
			return nil, fmt.Errorf("invalid frozen projection source %s/%q", key.kind, key.id)
		}
		keys[key] = struct{}{}
	}
	ordered := make([]sourceKey, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].kind != ordered[j].kind {
			return ordered[i].kind < ordered[j].kind
		}
		return ordered[i].id < ordered[j].id
	})

	query := `SELECT logical_version,deleted FROM legacy_task_domain_entity_versions WHERE workspace_id=? AND entity_kind=? AND entity_id=?`
	if c.dialect == DialectPostgres {
		query = `SELECT logical_version,deleted FROM legacy_task_domain_entity_versions WHERE workspace_id=$1 AND entity_kind=$2 AND entity_id=$3`
	}
	versions := make([]ProjectionSourceVersion, 0, len(ordered))
	for _, key := range ordered {
		var version int64
		var deleted bool
		if err := tx.QueryRowContext(ctx, query, c.workspaceID, key.kind, key.id).Scan(&version, &deleted); err != nil {
			return nil, fmt.Errorf("read frozen projection source %s/%s: %w", key.kind, key.id, err)
		}
		if deleted || version <= 0 {
			return nil, fmt.Errorf("invalid frozen projection source %s/%s version=%d deleted=%t", key.kind, key.id, version, deleted)
		}
		versions = append(versions, ProjectionSourceVersion{EntityKind: key.kind, LegacyID: key.id, LogicalVersion: version})
	}
	return versions, nil
}

func (c *MigrationCoordinator) validateMigrationIdentity(state WorkspaceTaskDomainState) error {
	if state.WorkspaceID != c.workspaceID {
		return fmt.Errorf("%w: workspace mismatch", ErrInvalidMigrationCoordinator)
	}
	if state.MigrationState == MigrationStateIdle && state.ModelVersion == ModelVersionLegacy {
		return nil
	}
	if state.ModelVersion == ModelVersionV2 && state.MigrationState == MigrationStateIdle {
		return nil
	}
	if state.MigrationID != c.migrationID || state.MigrationTimezone != c.timezone {
		return fmt.Errorf("%w: durable migration identity changed", ErrInvalidMigrationCoordinator)
	}
	return nil
}

func (c *MigrationCoordinator) inject(ctx context.Context, point MigrationCoordinatorFaultPoint) error {
	if c.faults == nil {
		return nil
	}
	return c.faults.Inject(ctx, point)
}

func (c *MigrationCoordinator) executionError(phase MigrationState, operation string, cause error) error {
	return &MigrationCoordinatorExecutionError{Phase: phase, Operation: operation, Cause: cause}
}
