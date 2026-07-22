package taskmigration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

var ErrInvalidMigrationStatus = errors.New("invalid task-domain migration status")

// MigrationStatus is an operator-facing durable snapshot. ReplayLag is based
// on the workspace's highest global outbox sequence, not on row count: global
// sequence gaps caused by other workspaces are expected and harmless.
type MigrationStatus struct {
	WorkspaceID        string
	ModelVersion       ModelVersion
	MigrationState     MigrationState
	MigrationID        string
	Revision           uint64
	WriteEpoch         uint64
	SourceWatermark    uint64
	CutoverRevision    *uint64
	OutboxHead         uint64
	ReplayLag          uint64
	AcceptLegacyWrites bool
	LastError          string
}

type MigrationStatusStore struct {
	db      *sql.DB
	dialect Dialect
}

func NewMigrationStatusStore(db *sql.DB, dialect Dialect) (*MigrationStatusStore, error) {
	if db == nil || (dialect != DialectSQLite && dialect != DialectPostgres) {
		return nil, ErrInvalidMigrationStatus
	}
	return &MigrationStatusStore{db: db, dialect: dialect}, nil
}

// Load reads the state row and workspace-local outbox head in one transaction
// so dashboards and recovery tooling do not combine checkpoints from
// different commits.
func (s *MigrationStatusStore) Load(ctx context.Context, workspaceID string) (MigrationStatus, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if s == nil || s.db == nil || ctx == nil || workspaceID == "" {
		return MigrationStatus{}, ErrInvalidMigrationStatus
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return MigrationStatus{}, fmt.Errorf("begin task-domain migration status: %w", err)
	}
	defer tx.Rollback()

	stateQuery := `SELECT workspace_id,model_version,migration_state,source_watermark,cutover_revision,
		write_epoch,accept_legacy_writes,migration_timezone,v2_first_write_at,migration_id,last_error,revision
		FROM workspace_task_domain_state WHERE workspace_id=?`
	headQuery := `SELECT COALESCE(MAX(sequence),0) FROM task_domain_legacy_outbox WHERE workspace_id=?`
	if s.dialect == DialectPostgres {
		stateQuery = strings.Replace(stateQuery, "?", "$1", 1)
		headQuery = strings.Replace(headQuery, "?", "$1", 1)
	}
	state, err := scanWorkspaceTaskDomainState(tx.QueryRowContext(ctx, stateQuery, workspaceID))
	if err != nil {
		return MigrationStatus{}, fmt.Errorf("load task-domain migration status state: %w", err)
	}
	if err := state.Validate(); err != nil {
		return MigrationStatus{}, fmt.Errorf("%w: %v", ErrInvalidMigrationStatus, err)
	}
	var head int64
	if err := tx.QueryRowContext(ctx, headQuery, workspaceID).Scan(&head); err != nil {
		return MigrationStatus{}, fmt.Errorf("load task-domain migration outbox head: %w", err)
	}
	if head < 0 || uint64(head) < state.SourceWatermark {
		return MigrationStatus{}, fmt.Errorf("%w: outbox head %d is behind source watermark %d", ErrInvalidMigrationStatus, head, state.SourceWatermark)
	}
	if err := tx.Commit(); err != nil {
		return MigrationStatus{}, fmt.Errorf("commit task-domain migration status: %w", err)
	}

	outboxHead := uint64(head)
	return MigrationStatus{
		WorkspaceID: state.WorkspaceID, ModelVersion: state.ModelVersion, MigrationState: state.MigrationState,
		MigrationID: state.MigrationID, Revision: state.Revision, WriteEpoch: state.WriteEpoch,
		SourceWatermark: state.SourceWatermark, CutoverRevision: cloneRecoveryUint64(state.CutoverRevision),
		OutboxHead: outboxHead, ReplayLag: outboxHead - state.SourceWatermark,
		AcceptLegacyWrites: state.AcceptLegacyWrites, LastError: state.LastError,
	}, nil
}
