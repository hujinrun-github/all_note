package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/hujinrun/flowspace/internal/storage"
)

type TenantWriter struct {
	provider Provider
	cfg      storage.Config
}

func NewTenantWriter(cfg storage.Config) *TenantWriter {
	return &TenantWriter{provider: Provider{}, cfg: cfg}
}

func (w *TenantWriter) BeginFencedWrite(ctx context.Context, workspaceID string, expectedEpoch int64, fn func(storage.TenantWriteTx) error) error {
	if fn == nil {
		return errors.New("tenant write callback is nil")
	}
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
	if err := checkPostgresTenantFence(ctx, tx, workspaceID, expectedEpoch, "active", `FOR SHARE`); err != nil {
		return err
	}
	if err := fn(writeTx); err != nil {
		return err
	}
	writeTx.closed = true
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
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
