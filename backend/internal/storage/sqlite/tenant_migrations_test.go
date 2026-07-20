package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/hujinrun/flowspace/internal/storage"
)

func TestSQLiteTenantBaselineKeepsInstallationIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.db")
	cfg := storage.Config{Env: "test", Driver: storage.DriverSQLite, SQLitePath: path}
	p := Provider{}
	if err := p.MigrateTenant(context.Background(), cfg); err != nil {
		t.Fatalf("first tenant migration: %v", err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var firstID string
	if err := db.QueryRow(`SELECT installation_id FROM tenant_installations WHERE singleton_key=1`).Scan(&firstID); err != nil {
		t.Fatal(err)
	}
	if firstID == "" {
		t.Fatal("installation id must not be empty")
	}
	if err := p.MigrateTenant(context.Background(), cfg); err != nil {
		t.Fatalf("second tenant migration: %v", err)
	}
	var secondID string
	if err := db.QueryRow(`SELECT installation_id FROM tenant_installations WHERE singleton_key=1`).Scan(&secondID); err != nil {
		t.Fatal(err)
	}
	if firstID != secondID {
		t.Fatalf("installation id changed: %q -> %q", firstID, secondID)
	}
	for _, forbidden := range []string{"users", "sessions", "workspace_service_bindings", "audit_events"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, forbidden).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Errorf("tenant baseline contains control table %s", forbidden)
		}
	}
	store, err := p.OpenTenant(context.Background(), cfg, "0001_tenant_baseline.sql")
	if err != nil {
		t.Fatalf("open migrated tenant: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close migrated tenant: %v", err)
	}
}

func TestSQLiteOpenTenantRejectsChecksumMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant-checksum.db")
	cfg := storage.Config{Env: "test", Driver: storage.DriverSQLite, SQLitePath: path}
	p := Provider{}
	if err := p.MigrateTenant(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE tenant_schema_migrations SET checksum='tampered' WHERE version='0001_tenant_baseline.sql'`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	store, err := p.OpenTenant(context.Background(), cfg, "0001_tenant_baseline.sql")
	if store != nil {
		_ = store.Close()
		t.Fatal("checksum mismatch must not return a store")
	}
	if !errors.Is(err, storage.ErrTenantSchemaNotReady) {
		t.Fatalf("expected tenant schema not ready, got %v", err)
	}
}
