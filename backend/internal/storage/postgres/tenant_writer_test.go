package postgres

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/storage"
)

func TestPostgresFenceWaitsForWritesAndInvalidatesOldEpoch(t *testing.T) {
	rawURL := createPostgresTestSchema(t, "fs_test_tenant_fence")
	cfg := storage.Config{Env: "test", Driver: storage.DriverPostgres, URL: rawURL}
	p := Provider{}
	if err := p.MigrateTenant(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", rawURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO tenant_workspaces(workspace_id) VALUES('w1')`); err != nil {
		t.Fatal(err)
	}
	writer := NewTenantWriter(cfg)
	started := make(chan struct{})
	release := make(chan struct{})
	writeDone := make(chan error, 1)
	go func() {
		writeDone <- writer.BeginFencedWrite(context.Background(), "w1", 1, func(tx storage.TenantWriteTx) error {
			close(started)
			<-release
			return tx.EnqueueOutbox(context.Background(), storage.TenantOutboxEvent{ID: "e1", Topic: "note.saved", AggregateID: "n1", AggregateRevision: 1, PayloadJSON: `{}`})
		})
	}()
	<-started
	fenceDone := make(chan error, 1)
	go func() {
		_, err := writer.FenceWorkspace(context.Background(), "w1", 1, "m1")
		fenceDone <- err
	}()
	select {
	case err := <-fenceDone:
		t.Fatalf("fence did not wait: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
	if err := <-fenceDone; err != nil {
		t.Fatal(err)
	}
	if err := writer.BeginFencedWrite(context.Background(), "w1", 1, func(storage.TenantWriteTx) error { return nil }); !errors.Is(err, storage.ErrTenantEpochMismatch) {
		t.Fatalf("old epoch error=%v", err)
	}
}
