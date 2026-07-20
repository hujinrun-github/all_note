package postgres

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/hujinrun/flowspace/internal/storage"
)

var (
	_ storage.ControlProvider           = Provider{}
	_ storage.TenantProvider            = Provider{}
	_ storage.TenantMaintenanceProvider = Provider{}
)

func TestOpenControlDoesNotCreateSchema(t *testing.T) {
	rawURL := createPostgresTestSchema(t, "fs_test_open_control_no_ddl")
	cfg := storage.Config{Env: "test", Driver: storage.DriverPostgres, URL: rawURL}

	store, err := (Provider{}).OpenControl(context.Background(), cfg)
	if store != nil {
		_ = store.Close()
		t.Fatal("schema-not-ready control open must not return a store")
	}
	if !errors.Is(err, storage.ErrControlSchemaNotReady) {
		t.Fatalf("expected ErrControlSchemaNotReady, got %v", err)
	}

	db, err := sql.Open("pgx", rawURL)
	if err != nil {
		t.Fatalf("open postgres for inspection: %v", err)
	}
	defer db.Close()
	assertRowCount(t, db, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = current_schema()`, 0)
}

func TestOpenTenantDoesNotCreateSchema(t *testing.T) {
	rawURL := createPostgresTestSchema(t, "fs_test_open_tenant_no_ddl")
	cfg := storage.Config{Env: "test", Driver: storage.DriverPostgres, URL: rawURL}

	store, err := (Provider{}).OpenTenant(context.Background(), cfg, "0001")
	if store != nil {
		_ = store.Close()
		t.Fatal("schema-not-ready tenant open must not return a store")
	}
	if !errors.Is(err, storage.ErrTenantSchemaNotReady) {
		t.Fatalf("expected ErrTenantSchemaNotReady, got %v", err)
	}

	db, err := sql.Open("pgx", rawURL)
	if err != nil {
		t.Fatalf("open postgres for inspection: %v", err)
	}
	defer db.Close()
	assertRowCount(t, db, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = current_schema()`, 0)
}

func TestMigrateControlCreatesVersionedControlSchema(t *testing.T) {
	rawURL := createPostgresTestSchema(t, "fs_test_migrate_control")
	cfg := storage.Config{Env: "test", Driver: storage.DriverPostgres, URL: rawURL}
	provider := Provider{}

	if err := provider.MigrateControl(context.Background(), cfg); err != nil {
		t.Fatalf("migrate control first run: %v", err)
	}
	if err := provider.MigrateControl(context.Background(), cfg); err != nil {
		t.Fatalf("migrate control second run: %v", err)
	}
	store, err := provider.OpenControl(context.Background(), cfg)
	if err != nil {
		t.Fatalf("open migrated control: %v", err)
	}
	defer store.Close()

	db, err := sql.Open("pgx", rawURL)
	if err != nil {
		t.Fatalf("open postgres for inspection: %v", err)
	}
	defer db.Close()
	for _, table := range []string{
		"control_schema_migrations", "users", "workspaces", "workspace_members", "sessions",
		"workspace_profile_families", "workspace_profile_versions", "workspace_service_endpoints",
		"workspace_service_bindings", "workspace_runtime_state", "storage_transition_jobs",
	} {
		var exists bool
		if err := db.QueryRow(`SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists); err != nil {
			t.Fatalf("inspect table %s: %v", table, err)
		}
		if !exists {
			t.Errorf("expected table %s", table)
		}
	}
	assertRowCount(t, db, `SELECT COUNT(*) FROM control_schema_migrations`, 2)
}

func TestPostgresTenantBaselineKeepsInstallationIdentity(t *testing.T) {
	rawURL := createPostgresTestSchema(t, "fs_test_migrate_tenant")
	cfg := storage.Config{Env: "test", Driver: storage.DriverPostgres, URL: rawURL}
	provider := Provider{}
	if err := provider.MigrateTenant(context.Background(), cfg); err != nil {
		t.Fatalf("migrate tenant first run: %v", err)
	}
	db, err := sql.Open("pgx", rawURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var firstID string
	if err := db.QueryRow(`SELECT installation_id::text FROM tenant_installations WHERE singleton_key=1`).Scan(&firstID); err != nil {
		t.Fatal(err)
	}
	if err := provider.MigrateTenant(context.Background(), cfg); err != nil {
		t.Fatalf("migrate tenant second run: %v", err)
	}
	var secondID string
	if err := db.QueryRow(`SELECT installation_id::text FROM tenant_installations WHERE singleton_key=1`).Scan(&secondID); err != nil {
		t.Fatal(err)
	}
	if firstID == "" || firstID != secondID {
		t.Fatalf("unstable installation id: %q -> %q", firstID, secondID)
	}
	for _, forbidden := range []string{"users", "sessions", "workspace_service_bindings", "audit_events"} {
		var exists bool
		if err := db.QueryRow(`SELECT to_regclass($1) IS NOT NULL`, forbidden).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Errorf("tenant baseline contains control table %s", forbidden)
		}
	}
	store, err := provider.OpenTenant(context.Background(), cfg, "0001_tenant_baseline.sql")
	if err != nil {
		t.Fatalf("open migrated tenant: %v", err)
	}
	_ = store.Close()
}
