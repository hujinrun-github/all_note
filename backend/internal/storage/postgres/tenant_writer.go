package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/taskdomain"
)

type TenantWriter struct {
	provider Provider
	cfg      storage.Config
}

func NewTenantWriter(cfg storage.Config) *TenantWriter {
	return (Provider{}).NewTenantWriter(cfg)
}

// NewTenantWriter derives the fenced writer from the same provider used to
// open the tenant read store. This is required for protected providers because
// each fenced command opens its own short-lived physical connection.
func (p Provider) NewTenantWriter(cfg storage.Config) *TenantWriter {
	return &TenantWriter{provider: p, cfg: cfg}
}

func (w *TenantWriter) BeginFencedWrite(ctx context.Context, workspaceID string, expectedEpoch int64, fn func(storage.TenantWriteTx) error) error {
	if fn == nil {
		return errors.New("tenant write callback is nil")
	}
	// TenantWriteTx exposes only task-domain command repositories (plus their
	// transactional outbox), so this is deliberately a task-domain write
	// boundary. Non-task tenant writes must use a different writer rather than
	// calling beginTaskDomainFencedWrite and accidentally setting v2_first_write_at.
	return w.beginTaskDomainFencedWrite(ctx, workspaceID, expectedEpoch, `FOR SHARE`, func(tx *postgresTenantWriteTx) error {
		return fn(tx)
	})
}

func (w *TenantWriter) beginTaskDomainFencedWrite(ctx context.Context, workspaceID string, expectedEpoch int64, lockClause string, fn func(*postgresTenantWriteTx) error) error {
	db, err := w.provider.openWithoutMigrations(ctx, w.cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	writeTx := &postgresTenantWriteTx{tx: tx, workspaceID: workspaceID}
	defer func() {
		writeTx.closed = true
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := checkPostgresTenantFence(ctx, tx, workspaceID, expectedEpoch, "active", lockClause); err != nil {
		return err
	}
	if err := fn(writeTx); err != nil {
		return err
	}
	if err := markPostgresTaskDomainV2FirstWrite(ctx, tx, workspaceID); err != nil {
		return err
	}
	writeTx.closed = true
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

// markPostgresTaskDomainV2FirstWrite runs after the business callback and
// before commit on the same transaction. The predicate makes legacy shadow
// writes a no-op and makes repeated v2 commands preserve both timestamp and
// state revision.
func markPostgresTaskDomainV2FirstWrite(ctx context.Context, tx *sql.Tx, workspaceID string) error {
	_, err := tx.ExecContext(ctx, `UPDATE workspace_task_domain_state
		SET v2_first_write_at=now(), revision=revision+1, updated_at=now()
		WHERE workspace_id=$1 AND model_version='v2' AND v2_first_write_at IS NULL`, workspaceID)
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
	// Project completion must serialize its count-and-save decision with every
	// normal tenant write. Normal writes hold FOR SHARE on this anchor; taking
	// FOR UPDATE here closes the count/occurrence-create race across instances.
	return w.beginTaskDomainFencedWrite(ctx, workspaceID, expectedEpoch, `FOR UPDATE`, func(tx *postgresTenantWriteTx) error {
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
	// The natural-completion proof and its task CAS must observe one stable
	// workspace write boundary, including occurrence reopen and generation.
	return w.beginTaskDomainFencedWrite(ctx, workspaceID, expectedEpoch, `FOR UPDATE`, func(tx *postgresTenantWriteTx) error {
		return fn(tx)
	})
}

func (w *TenantWriter) BeginGenerationWrite(ctx context.Context, workspaceID string, expectedEpoch int64, fn func(taskdomain.GenerationStateReader, taskdomain.GenerationWriter) error) error {
	if fn == nil {
		return errors.New("generation write callback is nil")
	}
	return w.beginTaskDomainFencedWrite(ctx, workspaceID, expectedEpoch, `FOR UPDATE`, func(tx *postgresTenantWriteTx) error {
		return fn(tx, tx)
	})
}

func (w *TenantWriter) FenceWorkspace(ctx context.Context, workspaceID string, expectedEpoch int64, migrationID string) (int64, error) {
	if migrationID == "" {
		return 0, errors.New("migration id is required")
	}
	db, err := w.provider.openWithoutMigrations(ctx, w.cfg)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err := checkPostgresTenantFence(ctx, tx, workspaceID, expectedEpoch, "active", `FOR UPDATE`); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE tenant_workspaces SET state='fenced',epoch=epoch+1,migration_id=$1,updated_at=now() WHERE workspace_id=$2`, migrationID, workspaceID); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return expectedEpoch + 1, nil
}

func (w *TenantWriter) ActivateWorkspace(ctx context.Context, workspaceID string, expectedEpoch int64, migrationID string) error {
	db, err := w.provider.openWithoutMigrations(ctx, w.cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := checkPostgresTenantFence(ctx, tx, workspaceID, expectedEpoch, "fenced", `FOR UPDATE`); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE tenant_workspaces SET state='active',migration_id=NULL,updated_at=now() WHERE workspace_id=$1 AND migration_id=$2`, workspaceID, migrationID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return storage.ErrTenantWorkspaceFenced
	}
	return tx.Commit()
}

type postgresTenantWriteTx struct {
	tx          *sql.Tx
	workspaceID string
	closed      bool
}

func (tx *postgresTenantWriteTx) TaskDomainWriter() taskdomain.TaskDomainWriter {
	return &postgresTaskDomainV2ProjectWriter{
		queryer:     tx.tx,
		workspaceID: tx.workspaceID,
		isClosed:    func() bool { return tx.closed },
	}
}

func (tx *postgresTenantWriteTx) ScheduleCommandWriter() taskdomain.ScheduleCommandWriter {
	return &postgresTaskDomainV2ProjectWriter{
		queryer:     tx.tx,
		workspaceID: tx.workspaceID,
		isClosed:    func() bool { return tx.closed },
	}
}

func (tx *postgresTenantWriteTx) ProjectWriter() taskdomain.ProjectWriter {
	return &postgresProjectCommandWriter{delegate: tx.taskDomainWriter()}
}

func (tx *postgresTenantWriteTx) RoadmapWriter() taskdomain.RoadmapWriter {
	return tx.taskDomainWriter()
}
func (tx *postgresTenantWriteTx) GetRoadmapByProject(ctx context.Context, id string) (taskdomain.RoadmapSnapshot, error) {
	return tx.taskDomainWriter().GetRoadmapByProject(ctx, id)
}
func (tx *postgresTenantWriteTx) GetRoadmapByID(ctx context.Context, id string) (taskdomain.RoadmapSnapshot, error) {
	return tx.taskDomainWriter().GetRoadmapByID(ctx, id)
}
func (tx *postgresTenantWriteTx) GetRoadmapNode(ctx context.Context, id string) (taskdomain.RoadmapNodeSnapshot, error) {
	return tx.taskDomainWriter().GetRoadmapNode(ctx, id)
}
func (tx *postgresTenantWriteTx) CountRoadmapNodeTasks(ctx context.Context, id string) (int, error) {
	return tx.taskDomainWriter().CountRoadmapNodeTasks(ctx, id)
}

func (tx *postgresTenantWriteTx) GetProject(ctx context.Context, projectID string) (taskdomain.ProjectSnapshot, error) {
	return tx.taskDomainWriter().GetProject(ctx, projectID)
}

func (tx *postgresTenantWriteTx) CountNonTerminalProjectOccurrences(ctx context.Context, projectID string) (int, error) {
	return tx.taskDomainWriter().CountNonTerminalProjectOccurrences(ctx, projectID)
}

func (tx *postgresTenantWriteTx) LoadRecurringCompletionState(ctx context.Context, taskID string) (taskdomain.RecurringCompletionCommandState, error) {
	return tx.taskDomainWriter().LoadRecurringCompletionState(ctx, taskID)
}

func (tx *postgresTenantWriteTx) ListGenerationTargets(ctx context.Context) ([]taskdomain.GenerationTargetState, error) {
	return tx.taskDomainWriter().ListGenerationTargets(ctx)
}

func (tx *postgresTenantWriteTx) InsertMissingOccurrences(ctx context.Context, insert taskdomain.GenerationInsert) error {
	return tx.taskDomainWriter().InsertMissingOccurrences(ctx, insert)
}

func (tx *postgresTenantWriteTx) CompleteGeneration(ctx context.Context, completion taskdomain.GenerationCompletion) error {
	return tx.taskDomainWriter().CompleteGeneration(ctx, completion)
}

func (tx *postgresTenantWriteTx) taskDomainWriter() *postgresTaskDomainV2ProjectWriter {
	return &postgresTaskDomainV2ProjectWriter{
		queryer: tx.tx, workspaceID: tx.workspaceID, isClosed: func() bool { return tx.closed },
	}
}

func (tx *postgresTenantWriteTx) EnqueueOutbox(ctx context.Context, event storage.TenantOutboxEvent) error {
	if tx.closed {
		return storage.ErrTenantWriteTxClosed
	}
	if event.ID == "" || event.Topic == "" || event.AggregateID == "" || event.AggregateRevision < 1 {
		return errors.New("invalid tenant outbox event")
	}
	_, err := tx.tx.ExecContext(ctx, `INSERT INTO tenant_job_outbox(id,workspace_id,topic,aggregate_id,aggregate_revision,payload_json) VALUES($1,$2,$3,$4,$5,$6::jsonb)`, event.ID, tx.workspaceID, event.Topic, event.AggregateID, event.AggregateRevision, event.PayloadJSON)
	return err
}

type postgresFenceQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func checkPostgresTenantFence(ctx context.Context, queryer postgresFenceQueryer, workspaceID string, expectedEpoch int64, requiredState, lockClause string) error {
	var epoch int64
	var state string
	err := queryer.QueryRowContext(ctx, `SELECT epoch,state FROM tenant_workspaces WHERE workspace_id=$1 `+lockClause, workspaceID).Scan(&epoch, &state)
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

var _ storage.TenantFencedWriter = (*TenantWriter)(nil)
var _ storage.TenantMigrationFencer = (*TenantWriter)(nil)
var _ taskdomain.ScheduleCommandFencer = (*TenantWriter)(nil)
var _ taskdomain.ProjectCommandFencer = (*TenantWriter)(nil)
var _ taskdomain.CompletionCommandFencer = (*TenantWriter)(nil)
var _ taskdomain.GenerationFencer = (*TenantWriter)(nil)
