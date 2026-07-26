package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/storage/adopt"
)

func TestPostgresAdoptExistingTenantIsIdempotentAndAllowsV2Migration(t *testing.T) {
	rawURL := createPostgresTestSchema(t, fmt.Sprintf("fs_test_tenant_adopt_%d", time.Now().UnixNano()))
	db, err := sql.Open("pgx", rawURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		`CREATE TABLE workspaces(id TEXT PRIMARY KEY)`,
		`CREATE TABLE folders(id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL)`,
		`CREATE TABLE notes(id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL)`,
		`CREATE UNIQUE INDEX notes_workspace_id_id_idx ON notes(workspace_id, id)`,
		`CREATE TABLE task_projects(id TEXT NOT NULL, workspace_id TEXT NOT NULL, PRIMARY KEY(workspace_id, id))`,
		`CREATE TABLE tasks(id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL)`,
		`INSERT INTO workspaces(id) VALUES ('workspace-adopt')`,
		`INSERT INTO folders(id, workspace_id) VALUES ('folder-adopt', 'workspace-adopt')`,
		`INSERT INTO notes(id, workspace_id) VALUES ('note-adopt', 'workspace-adopt')`,
		`INSERT INTO task_projects(id, workspace_id) VALUES ('project-adopt', 'workspace-adopt')`,
		`INSERT INTO tasks(id, workspace_id) VALUES ('task-adopt', 'workspace-adopt')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}

	manifestPath, err := findPostgresTenantAdoptManifest()
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := adopt.LoadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	request := storage.AdoptManifest{ID: manifest.ID, Checksum: manifest.Checksum}
	cfg := storage.Config{Env: "test", Driver: storage.DriverPostgres, URL: rawURL}
	provider := Provider{}
	if err := provider.AdoptExistingTenant(context.Background(), cfg, request); err != nil {
		t.Fatalf("first adoption: %v", err)
	}
	if err := provider.AdoptExistingTenant(context.Background(), cfg, request); err != nil {
		t.Fatalf("second adoption: %v", err)
	}
	assertRowCount(t, db, `SELECT COUNT(*) FROM tenant_workspaces WHERE workspace_id='workspace-adopt'`, 1)
	assertRowCount(t, db, `SELECT COUNT(*) FROM tenant_schema_migrations WHERE version='0001_tenant_baseline.sql'`, 1)

	if err := provider.MigrateTenant(context.Background(), cfg); err != nil {
		t.Fatalf("migrate adopted tenant: %v", err)
	}
	assertRowCount(t, db, `SELECT COUNT(*) FROM tenant_schema_migrations`, 5)
	assertRowCount(t, db, `
		SELECT COUNT(*) FROM workspace_task_domain_state
		WHERE workspace_id='workspace-adopt'
		  AND model_version='legacy'
		  AND migration_state='idle'
		  AND accept_legacy_writes
	`, 1)
}
