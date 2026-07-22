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

var (
	// ErrStateCASConflict is returned when durable state or its tenant runtime
	// anchor no longer matches the caller's expected fence. It is deliberately
	// stable so coordinators can reload and retry without inspecting SQL errors.
	ErrStateCASConflict       = errors.New("workspace task domain state CAS conflict")
	ErrInvalidStateStoreInput = errors.New("invalid workspace task domain state store input")
)

// StateStore persists the task-domain migration coordinator state. It is not
// wired into startup: callers must opt in as part of an explicit migration.
type StateStore struct {
	db      *sql.DB
	dialect Dialect
}

func NewStateStore(db *sql.DB, dialect Dialect) (*StateStore, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: nil database", ErrInvalidStateStoreInput)
	}
	if dialect != DialectSQLite && dialect != DialectPostgres {
		return nil, fmt.Errorf("%w: unsupported dialect %q", ErrInvalidStateStoreInput, dialect)
	}
	return &StateStore{db: db, dialect: dialect}, nil
}

// Load reads every durable state field. Invalid persisted combinations are
// rejected instead of being allowed to drive a coordinator transition.
func (s *StateStore) Load(ctx context.Context, workspaceID string) (WorkspaceTaskDomainState, error) {
	if ctx == nil || strings.TrimSpace(workspaceID) == "" {
		return WorkspaceTaskDomainState{}, fmt.Errorf("%w: context and workspace id are required", ErrInvalidStateStoreInput)
	}
	query := `SELECT workspace_id,model_version,migration_state,source_watermark,cutover_revision,
		write_epoch,accept_legacy_writes,migration_timezone,v2_first_write_at,migration_id,last_error,revision
		FROM workspace_task_domain_state WHERE workspace_id=` + s.placeholder(1)
	state, err := scanWorkspaceTaskDomainState(s.db.QueryRowContext(ctx, query, workspaceID))
	if err != nil {
		return WorkspaceTaskDomainState{}, err
	}
	if err := state.Validate(); err != nil {
		return WorkspaceTaskDomainState{}, fmt.Errorf("load workspace task domain state: %w", err)
	}
	return state, nil
}

// CompareAndSwap atomically advances state and, when the write fence changes,
// the tenant runtime epoch. A retry of an already-applied expected->next pair
// succeeds without another write. An expected==next pair is also a validated
// no-op, which is useful for idempotent coordinator checkpoints.
func (s *StateStore) CompareAndSwap(ctx context.Context, expected, next WorkspaceTaskDomainState) error {
	if ctx == nil {
		return fmt.Errorf("%w: nil context", ErrInvalidStateStoreInput)
	}
	if err := expected.Validate(); err != nil {
		return fmt.Errorf("%w: expected state: %v", ErrInvalidStateStoreInput, err)
	}
	if err := next.Validate(); err != nil {
		return fmt.Errorf("%w: next state: %v", ErrInvalidStateStoreInput, err)
	}
	if expected.WorkspaceID != next.WorkspaceID {
		return fmt.Errorf("%w: workspace ids differ", ErrInvalidStateStoreInput)
	}
	if err := validateStateStoreIntegerRange(expected); err != nil {
		return err
	}
	if err := validateStateStoreIntegerRange(next); err != nil {
		return err
	}
	noOp := stateStoreStatesEqual(expected, next)
	if !noOp && (expected.Revision == math.MaxUint64 || next.Revision != expected.Revision+1) {
		return fmt.Errorf("%w: next revision must equal expected revision plus one", ErrInvalidStateStoreInput)
	}
	if !noOp {
		if err := validateStateStoreTransition(expected, next); err != nil {
			return err
		}
	}

	if s.dialect == DialectSQLite {
		return s.compareAndSwapSQLite(ctx, expected, next, noOp)
	}
	return s.compareAndSwapPostgres(ctx, expected, next, noOp)
}

// validateStateStoreTransition prevents callers from treating CompareAndSwap
// as a generic row editor. Both values can satisfy the row-level invariants
// while still representing an impossible domain transition (for example an
// epoch jump, a replaced migration identity, or a rewritten cutover fence).
// Rebuilding the requested transition through the pure state commands keeps
// persistence and coordinator semantics on the same allow-list.
func validateStateStoreTransition(expected, next WorkspaceTaskDomainState) error {
	if next.WriteEpoch < expected.WriteEpoch {
		return invalidStateStoreTransition(expected, next, "write epoch must not regress")
	}
	if next.WriteEpoch > expected.WriteEpoch+1 {
		return invalidStateStoreTransition(expected, next, "write epoch must advance by at most one")
	}

	var (
		generated WorkspaceTaskDomainState
		err       error
	)
	switch {
	case expected.ModelVersion == ModelVersionV2 &&
		(expected.MigrationState == MigrationStateIdle || expected.MigrationState == MigrationStateCutover) &&
		next.ModelVersion == expected.ModelVersion && next.MigrationState == expected.MigrationState &&
		next.V2FirstWriteAt != nil:
		generated, err = MarkV2FirstWrite(expected, MarkV2FirstWriteCommand{
			ExpectedRevision: expected.Revision, ExpectedWriteEpoch: expected.WriteEpoch, WrittenAt: *next.V2FirstWriteAt,
		})
	case expected.ModelVersion == ModelVersionLegacy && expected.MigrationState == MigrationStateIdle &&
		next.ModelVersion == ModelVersionLegacy && next.MigrationState == MigrationStateBackfilling:
		generated, err = StartBackfill(expected, StartBackfillCommand{
			ExpectedRevision: expected.Revision, ExpectedWriteEpoch: expected.WriteEpoch,
			MigrationID: next.MigrationID, MigrationTimezone: next.MigrationTimezone,
		})
	case (expected.MigrationState == MigrationStateBackfilling || expected.MigrationState == MigrationStateCatchingUp) &&
		next.MigrationState == MigrationStateCatchingUp:
		generated, err = BeginCatchingUp(expected, BeginCatchingUpCommand{
			ExpectedRevision: expected.Revision, ExpectedWriteEpoch: expected.WriteEpoch, SourceWatermark: next.SourceWatermark,
		})
	case expected.MigrationState == MigrationStateCatchingUp && next.MigrationState == MigrationStateDraining &&
		next.CutoverRevision != nil:
		generated, err = BeginDrain(expected, BeginDrainCommand{
			ExpectedRevision: expected.Revision, ExpectedWriteEpoch: expected.WriteEpoch, CutoverRevision: *next.CutoverRevision,
		})
	case expected.MigrationState == MigrationStateDraining && next.MigrationState == MigrationStateReady:
		generated, err = MarkReady(expected, MarkReadyCommand{
			ExpectedRevision: expected.Revision, ExpectedWriteEpoch: expected.WriteEpoch, SourceWatermark: next.SourceWatermark,
		})
	case expected.MigrationState == MigrationStateReady && next.MigrationState == MigrationStateCutover:
		cutoverRevision := uint64(0)
		if next.CutoverRevision != nil {
			cutoverRevision = *next.CutoverRevision
		}
		generated, err = Cutover(expected, CutoverCommand{
			ExpectedRevision: expected.Revision, ExpectedWriteEpoch: expected.WriteEpoch,
			MigrationID: next.MigrationID, CutoverRevision: cutoverRevision,
		})
	case expected.ModelVersion == ModelVersionLegacy &&
		expected.MigrationState != MigrationStateIdle && expected.MigrationState != MigrationStateFailed &&
		next.MigrationState == MigrationStateFailed:
		generated, err = Fail(expected, FailCommand{
			ExpectedRevision: expected.Revision, ExpectedWriteEpoch: expected.WriteEpoch, Cause: next.LastError,
		})
	case ((expected.ModelVersion == ModelVersionLegacy && expected.MigrationState == MigrationStateFailed) ||
		(expected.ModelVersion == ModelVersionV2 && expected.MigrationState == MigrationStateCutover)) &&
		next.ModelVersion == ModelVersionLegacy && next.MigrationState == MigrationStateIdle:
		generated, err = Recover(expected, RecoverCommand{
			ExpectedRevision: expected.Revision, ExpectedWriteEpoch: expected.WriteEpoch,
		})
	default:
		return invalidStateStoreTransition(expected, next, "transition is not produced by a task-domain state command")
	}
	if err != nil {
		return invalidStateStoreTransition(expected, next, err.Error())
	}
	if !stateStoreStatesEqual(generated, next) {
		return invalidStateStoreTransition(expected, next, "transition rewrites command-owned state")
	}
	return nil
}

func invalidStateStoreTransition(expected, next WorkspaceTaskDomainState, detail string) error {
	return fmt.Errorf("%w: invalid transition %s/%s(epoch=%d) -> %s/%s(epoch=%d): %s",
		ErrInvalidStateStoreInput,
		expected.ModelVersion, expected.MigrationState, expected.WriteEpoch,
		next.ModelVersion, next.MigrationState, next.WriteEpoch,
		detail,
	)
}

type stateStoreQueryExecer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func (s *StateStore) compareAndSwapPostgres(ctx context.Context, expected, next WorkspaceTaskDomainState, noOp bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin state CAS: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := s.compareAndSwapLocked(ctx, tx, expected, next, noOp, true); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit state CAS: %w", err)
	}
	return nil
}

func (s *StateStore) compareAndSwapSQLite(ctx context.Context, expected, next WorkspaceTaskDomainState, noOp bool) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire SQLite state CAS connection: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return fmt.Errorf("begin SQLite immediate state CAS: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()

	if err := s.compareAndSwapLocked(ctx, conn, expected, next, noOp, false); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return fmt.Errorf("commit SQLite state CAS: %w", err)
	}
	committed = true
	return nil
}

func (s *StateStore) compareAndSwapLocked(
	ctx context.Context,
	qx stateStoreQueryExecer,
	expected, next WorkspaceTaskDomainState,
	noOp, postgres bool,
) error {
	// Keep the lock order identical to task-domain business writes:
	// tenant_workspaces anchor first, workspace_task_domain_state second. A
	// business write holds the anchor while atomically recording its first v2
	// write on the domain row; reversing these locks would deadlock cutover or
	// drain against that path.
	anchorQuery := `SELECT epoch,state FROM tenant_workspaces WHERE workspace_id=` + s.placeholder(1)
	if postgres {
		anchorQuery += ` FOR UPDATE`
	}
	var anchorEpoch int64
	var anchorState string
	if err := qx.QueryRowContext(ctx, anchorQuery, expected.WorkspaceID).Scan(&anchorEpoch, &anchorState); errors.Is(err, sql.ErrNoRows) {
		return stateCASConflict("tenant workspace anchor is missing")
	} else if err != nil {
		return fmt.Errorf("lock tenant workspace anchor: %w", err)
	}

	stateQuery := `SELECT workspace_id,model_version,migration_state,source_watermark,cutover_revision,
		write_epoch,accept_legacy_writes,migration_timezone,v2_first_write_at,migration_id,last_error,revision
		FROM workspace_task_domain_state WHERE workspace_id=` + s.placeholder(1)
	if postgres {
		stateQuery += ` FOR UPDATE`
	}
	current, err := scanWorkspaceTaskDomainState(qx.QueryRowContext(ctx, stateQuery, expected.WorkspaceID))
	if errors.Is(err, sql.ErrNoRows) {
		return stateCASConflict("state row is missing")
	}
	if err != nil {
		return fmt.Errorf("lock workspace task domain state: %w", err)
	}
	if anchorEpoch < 0 || uint64(anchorEpoch) != current.WriteEpoch || anchorState != "active" {
		return stateCASConflict("tenant runtime epoch or state changed")
	}

	// A retry after a successful commit observes next as current. Ensure the
	// anchor agrees, then return without incrementing either revision or epoch.
	if stateStoreStatesEqual(current, next) {
		return nil
	}
	if !stateStoreCASIdentityEqual(current, expected) {
		return stateCASConflict("durable CAS identity changed")
	}
	if noOp {
		return nil
	}

	if next.WriteEpoch != expected.WriteEpoch {
		result, err := qx.ExecContext(ctx,
			`UPDATE tenant_workspaces SET epoch=`+s.placeholder(1)+
				` WHERE workspace_id=`+s.placeholder(2)+` AND epoch=`+s.placeholder(3)+` AND state='active'`,
			next.WriteEpoch, expected.WorkspaceID, expected.WriteEpoch)
		if err != nil {
			return fmt.Errorf("advance tenant workspace epoch: %w", err)
		}
		if !exactlyOneStateStoreRow(result) {
			return stateCASConflict("tenant runtime epoch advance lost CAS")
		}
	}

	args := []any{
		next.ModelVersion,
		next.MigrationState,
		next.SourceWatermark,
		nullableStateStoreUint64(next.CutoverRevision),
		next.WriteEpoch,
		next.AcceptLegacyWrites,
		next.MigrationTimezone,
		s.stateStoreTimeArgument(next.V2FirstWriteAt),
		nullableStateStoreText(next.MigrationID),
		nullableStateStoreText(next.LastError),
		next.Revision,
		next.WorkspaceID,
	}
	update := `UPDATE workspace_task_domain_state SET model_version=` + s.placeholder(1) +
		`,migration_state=` + s.placeholder(2) +
		`,source_watermark=` + s.placeholder(3) +
		`,cutover_revision=` + s.placeholder(4) +
		`,write_epoch=` + s.placeholder(5) +
		`,accept_legacy_writes=` + s.placeholder(6) +
		`,migration_timezone=` + s.placeholder(7) +
		`,v2_first_write_at=` + s.placeholder(8) +
		`,migration_id=` + s.placeholder(9) +
		`,last_error=` + s.placeholder(10) +
		`,revision=` + s.placeholder(11) +
		`,updated_at=CURRENT_TIMESTAMP WHERE workspace_id=` + s.placeholder(12)
	result, err := qx.ExecContext(ctx, update, args...)
	if err != nil {
		return fmt.Errorf("update workspace task domain state: %w", err)
	}
	if !exactlyOneStateStoreRow(result) {
		return stateCASConflict("state update affected no row")
	}
	return nil
}

func scanWorkspaceTaskDomainState(row *sql.Row) (WorkspaceTaskDomainState, error) {
	var (
		state           WorkspaceTaskDomainState
		modelVersion    string
		migrationState  string
		sourceWatermark int64
		cutoverRevision sql.NullInt64
		writeEpoch      int64
		v2FirstWriteAt  any
		migrationID     sql.NullString
		lastError       sql.NullString
		revision        int64
	)
	if err := row.Scan(
		&state.WorkspaceID, &modelVersion, &migrationState, &sourceWatermark, &cutoverRevision,
		&writeEpoch, &state.AcceptLegacyWrites, &state.MigrationTimezone, &v2FirstWriteAt,
		&migrationID, &lastError, &revision,
	); err != nil {
		return WorkspaceTaskDomainState{}, err
	}
	if sourceWatermark < 0 || writeEpoch <= 0 || revision <= 0 || (cutoverRevision.Valid && cutoverRevision.Int64 < 0) {
		return WorkspaceTaskDomainState{}, fmt.Errorf("persisted workspace task domain state has invalid integer range")
	}
	state.ModelVersion = ModelVersion(modelVersion)
	state.MigrationState = MigrationState(migrationState)
	state.SourceWatermark = uint64(sourceWatermark)
	state.WriteEpoch = uint64(writeEpoch)
	state.Revision = uint64(revision)
	if cutoverRevision.Valid {
		value := uint64(cutoverRevision.Int64)
		state.CutoverRevision = &value
	}
	if migrationID.Valid {
		state.MigrationID = migrationID.String
	}
	if lastError.Valid {
		state.LastError = lastError.String
	}
	parsedTime, err := parseStateStoreTime(v2FirstWriteAt)
	if err != nil {
		return WorkspaceTaskDomainState{}, fmt.Errorf("parse v2 first write timestamp: %w", err)
	}
	state.V2FirstWriteAt = parsedTime
	return state, nil
}

func parseStateStoreTime(value any) (*time.Time, error) {
	if value == nil {
		return nil, nil
	}
	if timestamp, ok := value.(time.Time); ok {
		utc := timestamp.UTC()
		return &utc, nil
	}
	var text string
	switch typed := value.(type) {
	case string:
		text = typed
	case []byte:
		text = string(typed)
	default:
		return nil, fmt.Errorf("unsupported timestamp type %T", value)
	}
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	formats := []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	}
	for _, format := range formats {
		if timestamp, err := time.Parse(format, text); err == nil {
			utc := timestamp.UTC()
			return &utc, nil
		}
	}
	return nil, fmt.Errorf("invalid timestamp %q", text)
}

func (s *StateStore) placeholder(position int) string {
	if s.dialect == DialectPostgres {
		return fmt.Sprintf("$%d", position)
	}
	return "?"
}

func (s *StateStore) stateStoreTimeArgument(value *time.Time) any {
	if value == nil {
		return nil
	}
	utc := value.UTC()
	if s.dialect == DialectSQLite {
		return utc.Format(time.RFC3339Nano)
	}
	return utc
}

func nullableStateStoreUint64(value *uint64) any {
	if value == nil {
		return nil
	}
	return int64(*value)
}

func nullableStateStoreText(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func validateStateStoreIntegerRange(state WorkspaceTaskDomainState) error {
	if state.SourceWatermark > math.MaxInt64 || state.WriteEpoch > math.MaxInt64 || state.Revision > math.MaxInt64 ||
		(state.CutoverRevision != nil && *state.CutoverRevision > math.MaxInt64) {
		return fmt.Errorf("%w: state integer exceeds SQL BIGINT range", ErrInvalidStateStoreInput)
	}
	return nil
}

func exactlyOneStateStoreRow(result sql.Result) bool {
	rows, err := result.RowsAffected()
	return err == nil && rows == 1
}

func stateCASConflict(detail string) error {
	return fmt.Errorf("%w: %s", ErrStateCASConflict, detail)
}

func stateStoreCASIdentityEqual(left, right WorkspaceTaskDomainState) bool {
	return left.WorkspaceID == right.WorkspaceID &&
		left.Revision == right.Revision &&
		left.WriteEpoch == right.WriteEpoch &&
		left.ModelVersion == right.ModelVersion &&
		left.MigrationID == right.MigrationID &&
		equalStateStoreUint64(left.CutoverRevision, right.CutoverRevision)
}

func stateStoreStatesEqual(left, right WorkspaceTaskDomainState) bool {
	return stateStoreCASIdentityEqual(left, right) &&
		left.MigrationState == right.MigrationState &&
		left.SourceWatermark == right.SourceWatermark &&
		left.AcceptLegacyWrites == right.AcceptLegacyWrites &&
		left.MigrationTimezone == right.MigrationTimezone &&
		left.LastError == right.LastError &&
		equalStateStoreTimePointer(left.V2FirstWriteAt, right.V2FirstWriteAt)
}

func equalStateStoreUint64(left, right *uint64) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func equalStateStoreTimePointer(left, right *time.Time) bool {
	// PostgreSQL timestamps are stored at microsecond precision. Treat the
	// sub-microsecond portion as non-durable so a committed CAS can be retried
	// with the original in-memory command value.
	return left == nil && right == nil || left != nil && right != nil && left.UnixMicro() == right.UnixMicro()
}
