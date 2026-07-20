package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/storage"
)

func TestSQLiteFencedWriteChecksEpochRollsBackAndClosesTx(t *testing.T) {
	cfg, db := createSQLiteTenantWriterFixture(t)
	writer := NewTenantWriter(cfg)
	event := storage.TenantOutboxEvent{ID: "e1", Topic: "note.saved", AggregateID: "n1", AggregateRevision: 1, PayloadJSON: `{}`}
	var captured storage.TenantWriteTx
	errSentinel := errors.New("rollback")
	err := writer.BeginFencedWrite(context.Background(), "w1", 1, func(tx storage.TenantWriteTx) error {
		captured = tx
		if err := tx.EnqueueOutbox(context.Background(), event); err != nil {
			return err
		}
		return errSentinel
	})
	if !errors.Is(err, errSentinel) {
		t.Fatalf("callback error = %v", err)
	}
	assertSQLiteOutboxCount(t, db, 0)
	if err := captured.EnqueueOutbox(context.Background(), event); !errors.Is(err, storage.ErrTenantWriteTxClosed) {
		t.Fatalf("closed tx error = %v", err)
	}
	if err := writer.BeginFencedWrite(context.Background(), "w1", 2, func(storage.TenantWriteTx) error { return nil }); !errors.Is(err, storage.ErrTenantEpochMismatch) {
		t.Fatalf("stale/future epoch error = %v", err)
	}
	if err := writer.BeginFencedWrite(context.Background(), "missing", 1, func(storage.TenantWriteTx) error { return nil }); !errors.Is(err, storage.ErrTenantWorkspaceMissing) {
		t.Fatalf("missing anchor error = %v", err)
	}
}

func TestSQLiteFenceWaitsForWritesAndInvalidatesOldEpoch(t *testing.T) {
	cfg, db := createSQLiteTenantWriterFixture(t)
	writer := NewTenantWriter(cfg)
	writeStarted := make(chan struct{})
	releaseWrite := make(chan struct{})
	writeDone := make(chan error, 1)
	go func() {
		writeDone <- writer.BeginFencedWrite(context.Background(), "w1", 1, func(tx storage.TenantWriteTx) error {
			close(writeStarted)
			<-releaseWrite
			return tx.EnqueueOutbox(context.Background(), storage.TenantOutboxEvent{ID: "e1", Topic: "note.saved", AggregateID: "n1", AggregateRevision: 1, PayloadJSON: `{}`})
		})
	}()
	<-writeStarted
	fenceDone := make(chan error, 1)
	go func() {
		_, err := writer.FenceWorkspace(context.Background(), "w1", 1, "m1")
		fenceDone <- err
	}()
	select {
	case err := <-fenceDone:
		t.Fatalf("fence did not drain active write: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseWrite)
	if err := <-writeDone; err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := <-fenceDone; err != nil {
		t.Fatalf("fence: %v", err)
	}
	assertSQLiteOutboxCount(t, db, 1)
	if err := writer.BeginFencedWrite(context.Background(), "w1", 1, func(storage.TenantWriteTx) error { return nil }); !errors.Is(err, storage.ErrTenantEpochMismatch) {
		t.Fatalf("old epoch after fence = %v", err)
	}
	if err := writer.BeginFencedWrite(context.Background(), "w1", 2, func(storage.TenantWriteTx) error { return nil }); !errors.Is(err, storage.ErrTenantWorkspaceFenced) {
		t.Fatalf("fenced state write = %v", err)
	}
	if err := writer.ActivateWorkspace(context.Background(), "w1", 2, "m1"); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if err := writer.BeginFencedWrite(context.Background(), "w1", 2, func(storage.TenantWriteTx) error { return nil }); err != nil {
		t.Fatalf("new epoch write after activation: %v", err)
	}
}

func createSQLiteTenantWriterFixture(t *testing.T) (storage.Config, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tenant-writer.db")
	cfg := storage.Config{Env: "test", Driver: storage.DriverSQLite, SQLitePath: path}
	if err := (Provider{}).MigrateTenant(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`INSERT INTO tenant_workspaces(workspace_id) VALUES('w1')`); err != nil {
		t.Fatal(err)
	}
	return cfg, db
}

func assertSQLiteOutboxCount(t *testing.T, db *sql.DB, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(`SELECT COUNT(*) FROM tenant_job_outbox`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("outbox count=%d want=%d", got, want)
	}
}
