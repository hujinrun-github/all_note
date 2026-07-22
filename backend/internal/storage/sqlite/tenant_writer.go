package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"

	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/taskdomain"
)

var sqliteWorkspaceGates sync.Map

type TenantWriter struct {
	provider Provider
	cfg      storage.Config
}

func NewTenantWriter(cfg storage.Config) *TenantWriter {
	return &TenantWriter{provider: Provider{}, cfg: cfg}
}

func (w *TenantWriter) gate(workspaceID string) *sync.RWMutex {
	key := w.cfg.SQLitePath + "\x00" + workspaceID
	gate, _ := sqliteWorkspaceGates.LoadOrStore(key, &sync.RWMutex{})
	return gate.(*sync.RWMutex)
}

func (w *TenantWriter) BeginFencedWrite(ctx context.Context, workspaceID string, expectedEpoch int64, fn func(storage.TenantWriteTx) error) error {
	if fn == nil {
		return errors.New("tenant write callback is nil")
	}
	// TenantWriteTx exposes only task-domain command repositories (plus their
	// transactional outbox), so this is deliberately a task-domain write
	// boundary. Non-task tenant writes must use a different writer rather than
	// calling this method and accidentally setting v2_first_write_at.
	return w.beginTaskDomainFencedWrite(ctx, workspaceID, expectedEpoch, fn)
}

func (w *TenantWriter) beginTaskDomainFencedWrite(ctx context.Context, workspaceID string, expectedEpoch int64, fn func(storage.TenantWriteTx) error) error {
	gate := w.gate(workspaceID)
	gate.RLock()
	defer gate.RUnlock()
	db, err := w.provider.openWithoutMigrations(ctx, w.cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return err
	}
	committed := false
	tx := &sqliteTenantWriteTx{conn: conn, workspaceID: workspaceID}
	defer func() {
		tx.closed = true
		if !committed {
			_, _ = conn.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()
	if err := checkSQLiteTenantFence(ctx, conn, workspaceID, expectedEpoch, "active"); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	if err := markSQLiteTaskDomainV2FirstWrite(ctx, conn, workspaceID); err != nil {
		return err
	}
	tx.closed = true
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return err
	}
	committed = true
	return nil
}

// markSQLiteTaskDomainV2FirstWrite runs after the business callback and
// before COMMIT on the same connection/transaction. The predicate makes
// legacy shadow writes a no-op and repeated v2 commands idempotent.
func markSQLiteTaskDomainV2FirstWrite(ctx context.Context, conn *sql.Conn, workspaceID string) error {
	_, err := conn.ExecContext(ctx, `UPDATE workspace_task_domain_state
		SET v2_first_write_at=CURRENT_TIMESTAMP, revision=revision+1, updated_at=CURRENT_TIMESTAMP
		WHERE workspace_id=? AND model_version='v2' AND v2_first_write_at IS NULL`, workspaceID)
	return err
}

func (w *TenantWriter) BeginFencedScheduleWrite(ctx context.Context, workspaceID string, expectedEpoch int64, fn func(taskdomain.ScheduleCommandFencedTx) error) error {
	if fn == nil {
		return errors.New("schedule write callback is nil")
	}
	return w.BeginFencedWrite(ctx, workspaceID, expectedEpoch, func(tx storage.TenantWriteTx) error {
		return fn(tx)
	})
}

func (w *TenantWriter) BeginFencedProjectWrite(ctx context.Context, workspaceID string, expectedEpoch int64, fn func(taskdomain.ProjectCommandTx) error) error {
	if fn == nil {
		return errors.New("project write callback is nil")
	}
	return w.BeginFencedWrite(ctx, workspaceID, expectedEpoch, func(tx storage.TenantWriteTx) error {
		return fn(tx)
	})
}

func (w *TenantWriter) BeginFencedRoadmapWrite(ctx context.Context, workspaceID string, expectedEpoch int64, fn func(taskdomain.RoadmapCommandTx) error) error {
	if fn == nil {
		return errors.New("roadmap write callback is nil")
	}
	return w.BeginFencedWrite(ctx, workspaceID, expectedEpoch, func(tx storage.TenantWriteTx) error { return fn(tx) })
}

func (w *TenantWriter) BeginFencedCompletionWrite(ctx context.Context, workspaceID string, expectedEpoch int64, fn func(taskdomain.CompletionCommandTx) error) error {
	if fn == nil {
		return errors.New("completion write callback is nil")
	}
	return w.BeginFencedWrite(ctx, workspaceID, expectedEpoch, func(tx storage.TenantWriteTx) error {
		return fn(tx)
	})
}

func (w *TenantWriter) BeginGenerationWrite(ctx context.Context, workspaceID string, expectedEpoch int64, fn func(taskdomain.GenerationStateReader, taskdomain.GenerationWriter) error) error {
	if fn == nil {
		return errors.New("generation write callback is nil")
	}
	return w.BeginFencedWrite(ctx, workspaceID, expectedEpoch, func(tx storage.TenantWriteTx) error {
		return fn(tx, tx)
	})
}

func (w *TenantWriter) FenceWorkspace(ctx context.Context, workspaceID string, expectedEpoch int64, migrationID string) (int64, error) {
	if migrationID == "" {
		return 0, errors.New("migration id is required")
	}
	gate := w.gate(workspaceID)
	gate.Lock()
	defer gate.Unlock()
	db, err := w.provider.openWithoutMigrations(ctx, w.cfg)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	result, err := db.ExecContext(ctx, `UPDATE tenant_workspaces SET state='fenced',epoch=epoch+1,migration_id=?,updated_at=CURRENT_TIMESTAMP WHERE workspace_id=? AND state='active' AND epoch=?`, migrationID, workspaceID, expectedEpoch)
	if err != nil {
		return 0, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if changed != 1 {
		if err := classifySQLiteTenantFenceFailure(ctx, db, workspaceID, expectedEpoch); err != nil {
			return 0, err
		}
		return 0, storage.ErrTenantWorkspaceFenced
	}
	return expectedEpoch + 1, nil
}

func (w *TenantWriter) ActivateWorkspace(ctx context.Context, workspaceID string, expectedEpoch int64, migrationID string) error {
	gate := w.gate(workspaceID)
	gate.Lock()
	defer gate.Unlock()
	db, err := w.provider.openWithoutMigrations(ctx, w.cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	result, err := db.ExecContext(ctx, `UPDATE tenant_workspaces SET state='active',migration_id=NULL,updated_at=CURRENT_TIMESTAMP WHERE workspace_id=? AND state='fenced' AND epoch=? AND migration_id=?`, workspaceID, expectedEpoch, migrationID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return classifySQLiteTenantFenceFailure(ctx, db, workspaceID, expectedEpoch)
	}
	return nil
}

type sqliteTenantWriteTx struct {
	conn        *sql.Conn
	workspaceID string
	closed      bool
}

func (tx *sqliteTenantWriteTx) TaskDomainWriter() taskdomain.TaskDomainWriter {
	return &sqliteTaskDomainV2ProjectWriter{
		queryer:     tx.conn,
		workspaceID: tx.workspaceID,
		isClosed:    func() bool { return tx.closed },
	}
}

func (tx *sqliteTenantWriteTx) ScheduleCommandWriter() taskdomain.ScheduleCommandWriter {
	return &sqliteTaskDomainV2ProjectWriter{
		queryer:     tx.conn,
		workspaceID: tx.workspaceID,
		isClosed:    func() bool { return tx.closed },
	}
}

func (tx *sqliteTenantWriteTx) ProjectWriter() taskdomain.ProjectWriter {
	return &sqliteProjectCommandWriter{delegate: tx.taskDomainWriter()}
}

func (tx *sqliteTenantWriteTx) RoadmapWriter() taskdomain.RoadmapWriter { return tx.taskDomainWriter() }

func (tx *sqliteTenantWriteTx) GetRoadmapByProject(ctx context.Context, id string) (taskdomain.RoadmapSnapshot, error) {
	return tx.taskDomainWriter().GetRoadmapByProject(ctx, id)
}
func (tx *sqliteTenantWriteTx) GetRoadmapByID(ctx context.Context, id string) (taskdomain.RoadmapSnapshot, error) {
	return tx.taskDomainWriter().GetRoadmapByID(ctx, id)
}
func (tx *sqliteTenantWriteTx) GetRoadmapNode(ctx context.Context, id string) (taskdomain.RoadmapNodeSnapshot, error) {
	return tx.taskDomainWriter().GetRoadmapNode(ctx, id)
}
func (tx *sqliteTenantWriteTx) CountRoadmapNodeTasks(ctx context.Context, id string) (int, error) {
	return tx.taskDomainWriter().CountRoadmapNodeTasks(ctx, id)
}

func (tx *sqliteTenantWriteTx) GetProject(ctx context.Context, projectID string) (taskdomain.ProjectSnapshot, error) {
	return tx.taskDomainWriter().GetProject(ctx, projectID)
}

func (tx *sqliteTenantWriteTx) CountNonTerminalProjectOccurrences(ctx context.Context, projectID string) (int, error) {
	return tx.taskDomainWriter().CountNonTerminalProjectOccurrences(ctx, projectID)
}

func (tx *sqliteTenantWriteTx) LoadRecurringCompletionState(ctx context.Context, taskID string) (taskdomain.RecurringCompletionCommandState, error) {
	return tx.taskDomainWriter().LoadRecurringCompletionState(ctx, taskID)
}

func (tx *sqliteTenantWriteTx) ListGenerationTargets(ctx context.Context) ([]taskdomain.GenerationTargetState, error) {
	return tx.taskDomainWriter().ListGenerationTargets(ctx)
}

func (tx *sqliteTenantWriteTx) InsertMissingOccurrences(ctx context.Context, insert taskdomain.GenerationInsert) error {
	return tx.taskDomainWriter().InsertMissingOccurrences(ctx, insert)
}

func (tx *sqliteTenantWriteTx) CompleteGeneration(ctx context.Context, completion taskdomain.GenerationCompletion) error {
	return tx.taskDomainWriter().CompleteGeneration(ctx, completion)
}

func (tx *sqliteTenantWriteTx) taskDomainWriter() *sqliteTaskDomainV2ProjectWriter {
	return &sqliteTaskDomainV2ProjectWriter{
		queryer: tx.conn, workspaceID: tx.workspaceID, isClosed: func() bool { return tx.closed },
	}
}

func (tx *sqliteTenantWriteTx) EnqueueOutbox(ctx context.Context, event storage.TenantOutboxEvent) error {
	if tx.closed {
		return storage.ErrTenantWriteTxClosed
	}
	if event.ID == "" || event.Topic == "" || event.AggregateID == "" || event.AggregateRevision < 1 {
		return errors.New("invalid tenant outbox event")
	}
	_, err := tx.conn.ExecContext(ctx, `INSERT INTO tenant_job_outbox(id,workspace_id,topic,aggregate_id,aggregate_revision,payload_json) VALUES(?,?,?,?,?,?)`, event.ID, tx.workspaceID, event.Topic, event.AggregateID, event.AggregateRevision, event.PayloadJSON)
	return err
}

func checkSQLiteTenantFence(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, workspaceID string, expectedEpoch int64, requiredState string) error {
	var epoch int64
	var state string
	err := queryer.QueryRowContext(ctx, `SELECT epoch,state FROM tenant_workspaces WHERE workspace_id=?`, workspaceID).Scan(&epoch, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return storage.ErrTenantWorkspaceMissing
	}
	if err != nil {
		return err
	}
	if epoch != expectedEpoch {
		return fmt.Errorf("%w: expected=%d actual=%d", storage.ErrTenantEpochMismatch, expectedEpoch, epoch)
	}
	if state != requiredState {
		return fmt.Errorf("%w: state=%s", storage.ErrTenantWorkspaceFenced, state)
	}
	return nil
}

func classifySQLiteTenantFenceFailure(ctx context.Context, db *sql.DB, workspaceID string, expectedEpoch int64) error {
	var epoch int64
	var state string
	err := db.QueryRowContext(ctx, `SELECT epoch,state FROM tenant_workspaces WHERE workspace_id=?`, workspaceID).Scan(&epoch, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return storage.ErrTenantWorkspaceMissing
	}
	if err != nil {
		return err
	}
	if epoch != expectedEpoch {
		return fmt.Errorf("%w: expected=%d actual=%d", storage.ErrTenantEpochMismatch, expectedEpoch, epoch)
	}
	return fmt.Errorf("%w: state=%s", storage.ErrTenantWorkspaceFenced, state)
}

var _ storage.TenantFencedWriter = (*TenantWriter)(nil)
var _ storage.TenantMigrationFencer = (*TenantWriter)(nil)
var _ taskdomain.ScheduleCommandFencer = (*TenantWriter)(nil)
var _ taskdomain.ProjectCommandFencer = (*TenantWriter)(nil)
var _ taskdomain.CompletionCommandFencer = (*TenantWriter)(nil)
var _ taskdomain.GenerationFencer = (*TenantWriter)(nil)
