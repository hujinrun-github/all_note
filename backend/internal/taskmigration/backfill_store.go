package taskmigration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

var ErrInvalidBackfillInput = errors.New("invalid task domain backfill input")

type BackfillConflictCode string

const (
	BackfillConflictStateMissing    BackfillConflictCode = "state_missing"
	BackfillConflictInvalidState    BackfillConflictCode = "invalid_state"
	BackfillConflictStaleRevision   BackfillConflictCode = "stale_revision"
	BackfillConflictStaleEpoch      BackfillConflictCode = "stale_epoch"
	BackfillConflictSnapshotChanged BackfillConflictCode = "snapshot_changed"
	BackfillConflictTimezoneChanged BackfillConflictCode = "timezone_changed"
	BackfillConflictLostCAS         BackfillConflictCode = "lost_cas"
)

// BackfillConflictError is a stable coordinator-facing conflict. Callers can
// reload the workspace state and decide whether to retry without parsing a
// provider-specific SQL error.
type BackfillConflictError struct {
	Code        BackfillConflictCode
	WorkspaceID string
	Detail      string
}

func (e *BackfillConflictError) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("task domain backfill conflict: workspace=%s code=%s", e.WorkspaceID, e.Code)
	}
	return fmt.Sprintf("task domain backfill conflict: workspace=%s code=%s: %s", e.WorkspaceID, e.Code, e.Detail)
}

func (e *BackfillConflictError) Is(target error) bool {
	other, ok := target.(*BackfillConflictError)
	return ok && e.Code == other.Code
}

type BackfillLoader func(context.Context, *sql.Tx) (LegacyTaskDomainInventory, LegacyTaskDomainRows, error)
type BackfillProjector func(context.Context, *sql.Tx, V2Projection) error

type BackfillResult struct {
	SnapshotSequence uint64
	State            WorkspaceTaskDomainState
	Idempotent       bool
}

// BackfillStore owns the snapshot transaction only. It is intentionally not
// registered in application startup; a migration coordinator must invoke it
// explicitly after StartBackfill has durably moved the workspace state.
type BackfillStore struct {
	db      *sql.DB
	dialect Dialect
}

// RunSnapshotBackfill is the concrete backfill path used by migration
// coordinators. It binds the pure legacy mapper to V2ProjectionWriter so the
// source snapshot, durable source versions, v2 projection, ID map, and state
// watermark commit in one transaction.
func (s *BackfillStore) RunSnapshotBackfill(
	ctx context.Context,
	workspaceID string,
	expectedRevision uint64,
	expectedEpoch uint64,
	writtenAt time.Time,
	loader BackfillLoader,
	writer *V2ProjectionWriter,
) (BackfillResult, error) {
	if writer == nil || writtenAt.IsZero() {
		return BackfillResult{}, fmt.Errorf("%w: projection writer and written_at are required", ErrInvalidBackfillInput)
	}
	return s.RunBackfill(ctx, workspaceID, expectedRevision, expectedEpoch, loader,
		func(ctx context.Context, tx *sql.Tx, projection V2Projection) error {
			return writer.WriteSnapshot(ctx, tx, workspaceID, projection, writtenAt)
		},
	)
}

func NewBackfillStore(db *sql.DB, dialect Dialect) (*BackfillStore, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: nil database", ErrInvalidBackfillInput)
	}
	if dialect != DialectSQLite && dialect != DialectPostgres {
		return nil, fmt.Errorf("%w: unsupported dialect %q", ErrInvalidBackfillInput, dialect)
	}
	return &BackfillStore{db: db, dialect: dialect}, nil
}

// RunBackfill reads the legacy inventory and rows, creates the complete v2
// projection, writes it, and advances the durable migration state in one
// consistent transaction.
//
// PostgreSQL uses REPEATABLE READ and locks the domain-state row FOR UPDATE.
// For SQLite, database/sql cannot expose a *sql.Tx after a literal manual
// BEGIN IMMEDIATE. The first statement in the serializable, connection-pinned
// transaction is therefore a harmless write to the state row. SQLite acquires
// its RESERVED single-writer lock before any snapshot read, which provides the
// same transaction boundary while retaining *sql.Tx for the callbacks.
func (s *BackfillStore) RunBackfill(
	ctx context.Context,
	workspaceID string,
	expectedRevision uint64,
	expectedEpoch uint64,
	loader BackfillLoader,
	projector BackfillProjector,
) (result BackfillResult, err error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if s == nil || s.db == nil || ctx == nil || workspaceID == "" || loader == nil || projector == nil ||
		expectedRevision == 0 || expectedEpoch == 0 || expectedRevision > math.MaxInt64 || expectedEpoch > math.MaxInt64 {
		return BackfillResult{}, fmt.Errorf("%w: store, context, workspace, fences, loader, and projector are required", ErrInvalidBackfillInput)
	}

	isolation := sql.LevelRepeatableRead
	if s.dialect == DialectSQLite {
		isolation = sql.LevelSerializable
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: isolation})
	if err != nil {
		return BackfillResult{}, fmt.Errorf("begin task domain backfill: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	current, err := s.lockBackfillState(ctx, tx, workspaceID)
	if err != nil {
		return BackfillResult{}, err
	}
	snapshotSequence, err := s.loadSnapshotSequence(ctx, tx, workspaceID)
	if err != nil {
		return BackfillResult{}, err
	}

	if current.MigrationState == MigrationStateCatchingUp {
		result, err = idempotentBackfillResult(current, snapshotSequence, expectedRevision, expectedEpoch)
		if err != nil {
			return BackfillResult{}, err
		}
		if err := tx.Commit(); err != nil {
			return BackfillResult{}, fmt.Errorf("commit idempotent task domain backfill: %w", err)
		}
		return result, nil
	}
	if current.MigrationState != MigrationStateBackfilling || current.ModelVersion != ModelVersionLegacy || !current.AcceptLegacyWrites {
		return BackfillResult{}, backfillConflict(BackfillConflictInvalidState, workspaceID,
			fmt.Sprintf("model=%s migration_state=%s accept_legacy_writes=%t", current.ModelVersion, current.MigrationState, current.AcceptLegacyWrites))
	}
	if current.Revision != expectedRevision {
		return BackfillResult{}, backfillConflict(BackfillConflictStaleRevision, workspaceID,
			fmt.Sprintf("expected=%d actual=%d", expectedRevision, current.Revision))
	}
	if current.WriteEpoch != expectedEpoch {
		return BackfillResult{}, backfillConflict(BackfillConflictStaleEpoch, workspaceID,
			fmt.Sprintf("expected=%d actual=%d", expectedEpoch, current.WriteEpoch))
	}

	inventory, rows, err := loader(ctx, tx)
	if err != nil {
		return BackfillResult{}, fmt.Errorf("load legacy task domain snapshot for workspace %q: %w", workspaceID, err)
	}
	if strings.TrimSpace(inventory.WorkspaceID) != workspaceID {
		return BackfillResult{}, fmt.Errorf("%w: loader inventory workspace=%q, expected=%q", ErrInvalidBackfillInput, inventory.WorkspaceID, workspaceID)
	}
	preflight, err := PreflightLegacyTaskDomain(inventory)
	if err != nil {
		return BackfillResult{}, fmt.Errorf("preflight legacy task domain snapshot for workspace %q: %w", workspaceID, err)
	}
	if preflight.MigrationTimezone != current.MigrationTimezone {
		return BackfillResult{}, backfillConflict(BackfillConflictTimezoneChanged, workspaceID,
			fmt.Sprintf("state=%s snapshot=%s", current.MigrationTimezone, preflight.MigrationTimezone))
	}
	projection, err := MapLegacyTaskDomain(preflight, rows)
	if err != nil {
		return BackfillResult{}, fmt.Errorf("map legacy task domain snapshot for workspace %q: %w", workspaceID, err)
	}
	if err := projector(ctx, tx, projection); err != nil {
		return BackfillResult{}, fmt.Errorf("project legacy task domain snapshot for workspace %q: %w", workspaceID, err)
	}

	next, err := BeginCatchingUp(current, BeginCatchingUpCommand{
		ExpectedRevision:   expectedRevision,
		ExpectedWriteEpoch: expectedEpoch,
		SourceWatermark:    snapshotSequence,
	})
	if err != nil {
		return BackfillResult{}, fmt.Errorf("advance backfill state for workspace %q: %w", workspaceID, err)
	}
	if err := s.persistCatchingUp(ctx, tx, current, next); err != nil {
		return BackfillResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return BackfillResult{}, fmt.Errorf("commit task domain backfill for workspace %q: %w", workspaceID, err)
	}
	return BackfillResult{SnapshotSequence: snapshotSequence, State: next}, nil
}

func (s *BackfillStore) lockBackfillState(ctx context.Context, tx *sql.Tx, workspaceID string) (WorkspaceTaskDomainState, error) {
	if s.dialect == DialectSQLite {
		result, err := tx.ExecContext(ctx, `UPDATE workspace_task_domain_state
SET revision=revision WHERE workspace_id=?`, workspaceID)
		if err != nil {
			return WorkspaceTaskDomainState{}, fmt.Errorf("lock SQLite task domain backfill state: %w", err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return WorkspaceTaskDomainState{}, fmt.Errorf("read SQLite task domain backfill lock result: %w", err)
		}
		if rows != 1 {
			return WorkspaceTaskDomainState{}, backfillConflict(BackfillConflictStateMissing, workspaceID, "state row is missing")
		}
	}

	query := `SELECT workspace_id,model_version,migration_state,source_watermark,cutover_revision,
		write_epoch,accept_legacy_writes,migration_timezone,v2_first_write_at,migration_id,last_error,revision
		FROM workspace_task_domain_state WHERE workspace_id=?`
	if s.dialect == DialectPostgres {
		query = `SELECT workspace_id,model_version,migration_state,source_watermark,cutover_revision,
			write_epoch,accept_legacy_writes,migration_timezone,v2_first_write_at,migration_id,last_error,revision
			FROM workspace_task_domain_state WHERE workspace_id=$1 FOR UPDATE`
	}
	state, err := scanWorkspaceTaskDomainState(tx.QueryRowContext(ctx, query, workspaceID))
	if errors.Is(err, sql.ErrNoRows) {
		return WorkspaceTaskDomainState{}, backfillConflict(BackfillConflictStateMissing, workspaceID, "state row is missing")
	}
	if err != nil {
		return WorkspaceTaskDomainState{}, fmt.Errorf("load locked task domain backfill state: %w", err)
	}
	if err := state.Validate(); err != nil {
		return WorkspaceTaskDomainState{}, backfillConflict(BackfillConflictInvalidState, workspaceID, err.Error())
	}
	return state, nil
}

func (s *BackfillStore) loadSnapshotSequence(ctx context.Context, tx *sql.Tx, workspaceID string) (uint64, error) {
	query := `SELECT COALESCE(MAX(sequence),0) FROM task_domain_legacy_outbox WHERE workspace_id=?`
	if s.dialect == DialectPostgres {
		query = `SELECT COALESCE(MAX(sequence),0) FROM task_domain_legacy_outbox WHERE workspace_id=$1`
	}
	var sequence int64
	if err := tx.QueryRowContext(ctx, query, workspaceID).Scan(&sequence); err != nil {
		return 0, fmt.Errorf("read legacy outbox snapshot sequence for workspace %q: %w", workspaceID, err)
	}
	if sequence < 0 {
		return 0, fmt.Errorf("%w: negative outbox snapshot sequence", ErrInvalidBackfillInput)
	}
	return uint64(sequence), nil
}

func (s *BackfillStore) persistCatchingUp(
	ctx context.Context,
	tx *sql.Tx,
	current WorkspaceTaskDomainState,
	next WorkspaceTaskDomainState,
) error {
	query := `UPDATE workspace_task_domain_state
		SET migration_state=?,source_watermark=?,revision=?,updated_at=CURRENT_TIMESTAMP
		WHERE workspace_id=? AND model_version='legacy' AND migration_state='backfilling' AND revision=? AND write_epoch=?`
	if s.dialect == DialectPostgres {
		query = `UPDATE workspace_task_domain_state
			SET migration_state=$1,source_watermark=$2,revision=$3,updated_at=CURRENT_TIMESTAMP
			WHERE workspace_id=$4 AND model_version='legacy' AND migration_state='backfilling' AND revision=$5 AND write_epoch=$6`
	}
	result, err := tx.ExecContext(ctx, query, next.MigrationState, int64(next.SourceWatermark), int64(next.Revision),
		next.WorkspaceID, int64(current.Revision), int64(current.WriteEpoch))
	if err != nil {
		return fmt.Errorf("persist catching-up task domain state for workspace %q: %w", current.WorkspaceID, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read catching-up task domain state result: %w", err)
	}
	if rows != 1 {
		return backfillConflict(BackfillConflictLostCAS, current.WorkspaceID, "state changed while snapshot was projected")
	}
	return nil
}

func idempotentBackfillResult(
	state WorkspaceTaskDomainState,
	snapshotSequence uint64,
	expectedRevision uint64,
	expectedEpoch uint64,
) (BackfillResult, error) {
	if state.WriteEpoch != expectedEpoch {
		return BackfillResult{}, backfillConflict(BackfillConflictStaleEpoch, state.WorkspaceID,
			fmt.Sprintf("expected=%d actual=%d", expectedEpoch, state.WriteEpoch))
	}
	if expectedRevision == math.MaxUint64 || state.Revision != expectedRevision+1 {
		return BackfillResult{}, backfillConflict(BackfillConflictStaleRevision, state.WorkspaceID,
			fmt.Sprintf("expected committed revision=%d actual=%d", expectedRevision+1, state.Revision))
	}
	if state.SourceWatermark != snapshotSequence {
		return BackfillResult{}, backfillConflict(BackfillConflictSnapshotChanged, state.WorkspaceID,
			fmt.Sprintf("committed=%d current_snapshot=%d", state.SourceWatermark, snapshotSequence))
	}
	return BackfillResult{SnapshotSequence: snapshotSequence, State: state, Idempotent: true}, nil
}

func backfillConflict(code BackfillConflictCode, workspaceID, detail string) error {
	return &BackfillConflictError{Code: code, WorkspaceID: workspaceID, Detail: detail}
}
