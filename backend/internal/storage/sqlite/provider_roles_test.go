package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/hujinrun/flowspace/internal/storage"
	_ "modernc.org/sqlite"
)

func TestOpenControlDoesNotCreateSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.test.db")
	cfg := storage.Config{Env: "test", Driver: storage.DriverSQLite, SQLitePath: path}

	store, err := (Provider{}).OpenControl(context.Background(), cfg)
	if store != nil {
		_ = store.Close()
		t.Fatal("schema-not-ready control open must not return a store")
	}
	if !errors.Is(err, storage.ErrControlSchemaNotReady) {
		t.Fatalf("expected ErrControlSchemaNotReady, got %v", err)
	}
	assertSQLiteHasNoTables(t, path)
}

func TestOpenTenantDoesNotCreateSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.test.db")
	cfg := storage.Config{Env: "test", Driver: storage.DriverSQLite, SQLitePath: path}

	store, err := (Provider{}).OpenTenant(context.Background(), cfg, "0001")
	if store != nil {
		_ = store.Close()
		t.Fatal("schema-not-ready tenant open must not return a store")
	}
	if !errors.Is(err, storage.ErrTenantSchemaNotReady) {
		t.Fatalf("expected ErrTenantSchemaNotReady, got %v", err)
	}
	assertSQLiteHasNoTables(t, path)
}

func TestMigrateControlCreatesVersionedControlSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.test.db")
	cfg := storage.Config{Env: "test", Driver: storage.DriverSQLite, SQLitePath: path}
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

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite for inspection: %v", err)
	}
	defer db.Close()
	for _, table := range []string{
		"control_schema_migrations", "users", "workspaces", "workspace_members", "sessions",
		"workspace_profile_families", "workspace_profile_versions", "workspace_service_endpoints",
		"workspace_service_bindings", "workspace_runtime_state", "storage_transition_jobs",
	} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil {
			t.Fatalf("inspect table %s: %v", table, err)
		}
		if count != 1 {
			t.Errorf("expected table %s", table)
		}
	}
	var migrationCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM control_schema_migrations`).Scan(&migrationCount); err != nil {
		t.Fatalf("count control migrations: %v", err)
	}
	if migrationCount != 4 {
		t.Fatalf("control migration count = %d, want 4", migrationCount)
	}
}

func TestOpenControlListsUsersWithSQLiteTimestampDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control-users.test.db")
	cfg := storage.Config{Env: "test", Driver: storage.DriverSQLite, SQLitePath: path}
	provider := Provider{}
	if err := provider.MigrateControl(context.Background(), cfg); err != nil {
		t.Fatalf("migrate control: %v", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO users(id,email,display_name,password_hash,role,status)
		VALUES ('u1','owner@example.test','Owner','hash','admin','active')
	`); err != nil {
		_ = db.Close()
		t.Fatalf("seed control user: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := provider.OpenControl(context.Background(), cfg)
	if err != nil {
		t.Fatalf("open control: %v", err)
	}
	defer store.Close()
	users, total, err := store.Auth().ListUsers(context.Background(), storage.UserListFilter{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("list control users: %v", err)
	}
	if total != 1 || len(users) != 1 {
		t.Fatalf("users total=%d len=%d, want 1", total, len(users))
	}
	if users[0].CreatedAt <= 0 || users[0].UpdatedAt <= 0 {
		t.Fatalf("control user timestamps were not normalized: created=%d updated=%d", users[0].CreatedAt, users[0].UpdatedAt)
	}
}

func TestOpenControlRejectsIncompleteOrTamperedMigrationHistory(t *testing.T) {
	for _, tc := range []struct {
		name      string
		statement string
	}{
		{name: "missing latest", statement: `DELETE FROM control_schema_migrations WHERE version = '0003_codex_oauth_device_flows.sql'`},
		{name: "tampered checksum", statement: `UPDATE control_schema_migrations SET checksum = 'tampered' WHERE version = '0001_control_baseline.sql'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "control.test.db")
			cfg := storage.Config{Env: "test", Driver: storage.DriverSQLite, SQLitePath: path}
			provider := Provider{}
			if err := provider.MigrateControl(context.Background(), cfg); err != nil {
				t.Fatalf("migrate control: %v", err)
			}
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(tc.statement); err != nil {
				_ = db.Close()
				t.Fatal(err)
			}
			_ = db.Close()

			store, err := provider.OpenControl(context.Background(), cfg)
			if store != nil {
				_ = store.Close()
				t.Fatal("invalid control migration history must not return a store")
			}
			if !errors.Is(err, storage.ErrControlSchemaNotReady) {
				t.Fatalf("expected ErrControlSchemaNotReady, got %v", err)
			}
		})
	}
}

func TestControlSchemaEnforcesWorkspaceKindAndRuntimeConstraints(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.constraints.db")
	cfg := storage.Config{Env: "test", Driver: storage.DriverSQLite, SQLitePath: path}
	if err := (Provider{}).MigrateControl(context.Background(), cfg); err != nil {
		t.Fatalf("migrate control: %v", err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	seed := []string{
		`INSERT INTO users(id,email,password_hash) VALUES ('u1','u1@example.test','x'),('u2','u2@example.test','x')`,
		`INSERT INTO workspaces(id,name,owner_user_id) VALUES ('w1','one','u1'),('w2','two','u2')`,
		`INSERT INTO system_profile_families(id,kind,name) VALUES ('sf-db','data_store','default db'),('sf-ai','llm_chat','default ai')`,
		`INSERT INTO system_profile_versions(id,family_id,kind,version,provider,state) VALUES ('sv-db','sf-db','data_store',1,'postgres','verified'),('sv-ai','sf-ai','llm_chat',1,'openai','verified')`,
		`INSERT INTO workspace_service_endpoints(id,workspace_id,kind,source_type,system_profile_version_id) VALUES ('db','w1','data_store','system','sv-db'),('ai','w1','llm_chat','system','sv-ai'),('db','w2','data_store','system','sv-db')`,
	}
	for _, statement := range seed {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("seed control constraint fixture: %v", err)
		}
	}

	assertSQLiteConstraintFails(t, db, `INSERT INTO workspace_service_bindings(workspace_id,kind,mode,endpoint_source_type,endpoint_id,updated_by) VALUES ('w2','data_store','default','system','ai','u2')`)
	assertSQLiteConstraintFails(t, db, `INSERT INTO workspace_service_bindings(workspace_id,kind,mode,endpoint_source_type,endpoint_id,updated_by) VALUES ('w1','llm_chat','disabled','system','ai','u1')`)
	assertSQLiteConstraintFails(t, db, `INSERT INTO workspace_service_bindings(workspace_id,kind,mode,endpoint_source_type,endpoint_id,updated_by) VALUES ('w1','llm_transcription','reuse_chat','system','ai','u1')`)

	if _, err := db.Exec(`INSERT INTO workspace_service_bindings(workspace_id,kind,mode,endpoint_source_type,endpoint_id,updated_by) VALUES ('w1','data_store','default','system','db','u1'),('w1','llm_chat','disabled',NULL,NULL,'u1'),('w1','llm_transcription','reuse_chat',NULL,NULL,'u1')`); err != nil {
		t.Fatalf("valid binding modes rejected: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO workspace_runtime_state(workspace_id,mode,epoch,binding_revision,updated_by) VALUES ('w1','active',1,1,'u1')`); err != nil {
		t.Fatalf("valid active runtime state rejected: %v", err)
	}
	assertSQLiteConstraintFails(t, db, `INSERT INTO workspace_runtime_state(workspace_id,mode,epoch,binding_revision,storage_operation_kind,storage_operation_id,updated_by) VALUES ('w2','active',1,1,'migration','missing','u2')`)
}

func assertSQLiteConstraintFails(t *testing.T, db *sql.DB, statement string) {
	t.Helper()
	if _, err := db.Exec(statement); err == nil {
		t.Fatalf("expected database constraint failure: %s", statement)
	}
}

func assertSQLiteHasNoTables(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite for inspection: %v", err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`).Scan(&count); err != nil {
		t.Fatalf("count sqlite tables: %v", err)
	}
	if count != 0 {
		t.Fatalf("Open created %d tables; Open must never run DDL", count)
	}
}
