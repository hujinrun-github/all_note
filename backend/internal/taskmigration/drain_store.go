package taskmigration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
)

var (
	// ErrDrainConflict is the stable result for a stale fence, a conflicting
	// retry, an inactive tenant anchor, or a workspace which is not ready to
	// enter the drain phase.
	ErrDrainConflict = errors.New("workspace task domain drain conflict")
	// ErrInvalidDrainStoreInput reports construction and command arguments
	// which cannot identify a safe drain operation.
	ErrInvalidDrainStoreInput = errors.New("invalid workspace task domain drain store input")
)

// DrainStore closes legacy task-domain writes and advances the tenant write
// epoch in one database transaction. It is intentionally an explicit
// migration primitive and is not wired into application startup or routing.
type DrainStore struct {
	db      *sql.DB
	dialect Dialect
}

func NewDrainStore(db *sql.DB, dialect Dialect) (*DrainStore, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: nil database", ErrInvalidDrainStoreInput)
	}
	if dialect != DialectSQLite && dialect != DialectPostgres {
		return nil, fmt.Errorf("%w: unsupported dialect %q", ErrInvalidDrainStoreInput, dialect)
	}
	return &DrainStore{db: db, dialect: dialect}, nil
}

// BeginDrain locks the workspace's durable migration fence, captures the
// workspace-local outbox high-water mark, closes legacy writes, and advances
// both copies of the write epoch. A retry with the original arguments returns
// the already-applied state without incrementing the epoch or revision again.
func (s *DrainStore) BeginDrain(
	ctx context.Context,
	workspaceID string,
	expectedRevision uint64,
	expectedWriteEpoch uint64,
	migrationID string,
) (WorkspaceTaskDomainState, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	migrationID = strings.TrimSpace(migrationID)
	if s == nil || s.db == nil || ctx == nil || workspaceID == "" || migrationID == "" ||
		expectedRevision == 0 || expectedWriteEpoch == 0 ||
		expectedRevision >= math.MaxInt64 || expectedWriteEpoch >= math.MaxInt64 {
		return WorkspaceTaskDomainState{}, fmt.Errorf("%w: context, workspace, migration, revision, and epoch are required", ErrInvalidDrainStoreInput)
	}
	if s.dialect == DialectSQLite {
		return s.beginDrainSQLite(ctx, workspaceID, expectedRevision, expectedWriteEpoch, migrationID)
	}
	return s.beginDrainPostgres(ctx, workspaceID, expectedRevision, expectedWriteEpoch, migrationID)
}

type drainQueryExecer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func (s *DrainStore) beginDrainSQLite(
	ctx context.Context,
	workspaceID string,
	expectedRevision uint64,
	expectedWriteEpoch uint64,
	migrationID string,
) (state WorkspaceTaskDomainState, err error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return WorkspaceTaskDomainState{}, fmt.Errorf("acquire SQLite drain connection: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return WorkspaceTaskDomainState{}, fmt.Errorf("begin SQLite immediate drain: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()

	state, err = s.beginDrainLocked(ctx, conn, workspaceID, expectedRevision, expectedWriteEpoch, migrationID, false)
	if err != nil {
		return WorkspaceTaskDomainState{}, err
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return WorkspaceTaskDomainState{}, fmt.Errorf("commit SQLite drain: %w", err)
	}
	committed = true
	return state, nil
}

func (s *DrainStore) beginDrainPostgres(
	ctx context.Context,
	workspaceID string,
	expectedRevision uint64,
	expectedWriteEpoch uint64,
	migrationID string,
) (WorkspaceTaskDomainState, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return WorkspaceTaskDomainState{}, fmt.Errorf("begin PostgreSQL drain: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	state, err := s.beginDrainLocked(ctx, tx, workspaceID, expectedRevision, expectedWriteEpoch, migrationID, true)
	if err != nil {
		return WorkspaceTaskDomainState{}, err
	}
	if err := tx.Commit(); err != nil {
		return WorkspaceTaskDomainState{}, fmt.Errorf("commit PostgreSQL drain: %w", err)
	}
	return state, nil
}

func (s *DrainStore) beginDrainLocked(
	ctx context.Context,
	qx drainQueryExecer,
	workspaceID string,
	expectedRevision uint64,
	expectedWriteEpoch uint64,
	migrationID string,
	postgres bool,
) (WorkspaceTaskDomainState, error) {
	// Tenant writers lock this anchor before doing business work. Taking it
	// first preserves their lock order and drains all already-admitted writes.
	anchorQuery := `SELECT epoch,state FROM tenant_workspaces WHERE workspace_id=` + s.placeholder(1)
	if postgres {
		anchorQuery += ` FOR UPDATE`
	}
	var anchorEpoch int64
	var anchorState string
	if err := qx.QueryRowContext(ctx, anchorQuery, workspaceID).Scan(&anchorEpoch, &anchorState); errors.Is(err, sql.ErrNoRows) {
		return WorkspaceTaskDomainState{}, drainConflict("tenant workspace anchor is missing")
	} else if err != nil {
		return WorkspaceTaskDomainState{}, fmt.Errorf("lock tenant workspace anchor: %w", err)
	}

	// PostgreSQL outbox triggers hold FOR SHARE on this row. FOR UPDATE waits
	// for them to commit, after which no new legacy trigger can pass the fence.
	stateQuery := `SELECT workspace_id,model_version,migration_state,source_watermark,cutover_revision,
		write_epoch,accept_legacy_writes,migration_timezone,v2_first_write_at,migration_id,last_error,revision
		FROM workspace_task_domain_state WHERE workspace_id=` + s.placeholder(1)
	if postgres {
		stateQuery += ` FOR UPDATE`
	}
	current, err := scanWorkspaceTaskDomainState(qx.QueryRowContext(ctx, stateQuery, workspaceID))
	if errors.Is(err, sql.ErrNoRows) {
		return WorkspaceTaskDomainState{}, drainConflict("workspace task-domain state is missing")
	}
	if err != nil {
		return WorkspaceTaskDomainState{}, fmt.Errorf("lock workspace task-domain state: %w", err)
	}
	if err := current.Validate(); err != nil {
		return WorkspaceTaskDomainState{}, drainConflict("persisted task-domain state is invalid")
	}
	if anchorEpoch <= 0 || uint64(anchorEpoch) != current.WriteEpoch || anchorState != "active" {
		return WorkspaceTaskDomainState{}, drainConflict("tenant runtime epoch or state changed")
	}

	if drainCommandAlreadyApplied(current, expectedRevision, expectedWriteEpoch, migrationID) {
		return current, nil
	}
	if current.Revision != expectedRevision || current.WriteEpoch != expectedWriteEpoch ||
		current.ModelVersion != ModelVersionLegacy || current.MigrationState != MigrationStateCatchingUp ||
		!current.AcceptLegacyWrites || current.MigrationID != migrationID || current.CutoverRevision != nil {
		return WorkspaceTaskDomainState{}, drainConflict("durable drain CAS identity changed")
	}

	maxQuery := `SELECT COALESCE(MAX(sequence),0) FROM task_domain_legacy_outbox WHERE workspace_id=` + s.placeholder(1)
	var cutoverRevision int64
	if err := qx.QueryRowContext(ctx, maxQuery, workspaceID).Scan(&cutoverRevision); err != nil {
		return WorkspaceTaskDomainState{}, fmt.Errorf("read workspace outbox high-water mark: %w", err)
	}
	if cutoverRevision < 0 || uint64(cutoverRevision) < current.SourceWatermark {
		return WorkspaceTaskDomainState{}, drainConflict("outbox high-water mark is behind source watermark")
	}

	next, err := BeginDrain(current, BeginDrainCommand{
		ExpectedRevision:   expectedRevision,
		ExpectedWriteEpoch: expectedWriteEpoch,
		CutoverRevision:    uint64(cutoverRevision),
	})
	if err != nil {
		return WorkspaceTaskDomainState{}, drainConflict("domain transition rejected")
	}

	anchorUpdate := `UPDATE tenant_workspaces SET epoch=` + s.placeholder(1) +
		` WHERE workspace_id=` + s.placeholder(2) + ` AND epoch=` + s.placeholder(3) + ` AND state='active'`
	result, err := qx.ExecContext(ctx, anchorUpdate, next.WriteEpoch, workspaceID, expectedWriteEpoch)
	if err != nil {
		return WorkspaceTaskDomainState{}, fmt.Errorf("advance tenant workspace drain epoch: %w", err)
	}
	if !exactlyOneStateStoreRow(result) {
		return WorkspaceTaskDomainState{}, drainConflict("tenant workspace epoch advance lost CAS")
	}

	stateUpdate := `UPDATE workspace_task_domain_state SET migration_state=` + s.placeholder(1) +
		`,cutover_revision=` + s.placeholder(2) +
		`,write_epoch=` + s.placeholder(3) +
		`,accept_legacy_writes=` + s.placeholder(4) +
		`,revision=` + s.placeholder(5) +
		`,updated_at=CURRENT_TIMESTAMP WHERE workspace_id=` + s.placeholder(6) +
		` AND revision=` + s.placeholder(7) +
		` AND write_epoch=` + s.placeholder(8) +
		` AND model_version='legacy' AND migration_state='catching_up'` +
		` AND accept_legacy_writes=` + s.acceptLegacyPredicate() +
		` AND migration_id=` + s.placeholder(9)
	result, err = qx.ExecContext(ctx, stateUpdate,
		next.MigrationState, cutoverRevision, next.WriteEpoch, next.AcceptLegacyWrites, next.Revision,
		workspaceID, expectedRevision, expectedWriteEpoch, migrationID,
	)
	if err != nil {
		return WorkspaceTaskDomainState{}, fmt.Errorf("close workspace legacy drain fence: %w", err)
	}
	if !exactlyOneStateStoreRow(result) {
		return WorkspaceTaskDomainState{}, drainConflict("workspace task-domain state update lost CAS")
	}
	return next, nil
}

func drainCommandAlreadyApplied(
	state WorkspaceTaskDomainState,
	expectedRevision uint64,
	expectedWriteEpoch uint64,
	migrationID string,
) bool {
	if state.MigrationID != migrationID || state.AcceptLegacyWrites || state.CutoverRevision == nil ||
		state.WriteEpoch != expectedWriteEpoch+1 || state.Revision < expectedRevision+1 {
		return false
	}
	switch state.MigrationState {
	case MigrationStateDraining, MigrationStateReady:
		return state.ModelVersion == ModelVersionLegacy
	case MigrationStateCutover:
		return state.ModelVersion == ModelVersionV2
	default:
		return false
	}
}

func (s *DrainStore) placeholder(position int) string {
	if s.dialect == DialectPostgres {
		return fmt.Sprintf("$%d", position)
	}
	return "?"
}

func (s *DrainStore) acceptLegacyPredicate() string {
	if s.dialect == DialectPostgres {
		return "TRUE"
	}
	return "1"
}

func drainConflict(detail string) error {
	return fmt.Errorf("%w: %s", ErrDrainConflict, detail)
}
