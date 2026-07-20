package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"

	"github.com/hujinrun/flowspace/internal/storage"
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
	tx.closed = true
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return err
	}
	committed = true
	return nil
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
