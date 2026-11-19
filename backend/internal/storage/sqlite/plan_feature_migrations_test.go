package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/hujinrun/flowspace/internal/storage"
)

func TestPlanFeatureMigrationPreservesExistingSettingsAndEnablesExistingWorkspaces(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "plan-feature.test.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`PRAGMA foreign_keys=ON; CREATE TABLE control_schema_migrations(version TEXT PRIMARY KEY,checksum TEXT NOT NULL,applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	dir, err := findSQLiteControlMigrationsDir()
	if err != nil {
		t.Fatal(err)
	}
	migrations, err := loadSQLiteControlMigrations(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, migration := range migrations {
		if migration.version >= "0005" {
			break
		}
		if err := applySQLiteControlMigration(ctx, db, migration); err != nil {
			t.Fatal(err)
		}
	}
	for _, query := range []string{
		`INSERT INTO users(id,email,password_hash) VALUES('u1','u1@test.invalid','hash')`,
		`INSERT INTO workspaces(id,name,owner_user_id) VALUES('w1','existing','u1')`,
		`INSERT INTO workspace_ai_feature_settings(workspace_id,feature,enabled,fallback_mode,updated_by) VALUES('w1','roadmap_generation',0,'error','u1')`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	cfg := storage.Config{Env: "test", Driver: storage.DriverSQLite, SQLitePath: path}
	for i := 0; i < 2; i++ {
		if err := (Provider{}).MigrateControl(ctx, cfg); err != nil {
			t.Fatal(err)
		}
	}
	var enabled int
	var fallback string
	if err := db.QueryRow(`SELECT enabled,fallback_mode FROM workspace_ai_feature_settings WHERE workspace_id='w1' AND feature='roadmap_generation'`).Scan(&enabled, &fallback); err != nil || enabled != 0 || fallback != "error" {
		t.Fatalf("existing setting lost: %d %s %v", enabled, fallback, err)
	}
	if err := db.QueryRow(`SELECT enabled,fallback_mode FROM workspace_ai_feature_settings WHERE workspace_id='w1' AND feature='plan_generation'`).Scan(&enabled, &fallback); err != nil || enabled != 1 || fallback != "error" {
		t.Fatalf("plan setting: %d %s %v", enabled, fallback, err)
	}
	if _, err := db.Exec(`UPDATE workspace_ai_feature_settings SET fallback_mode='template' WHERE feature='plan_generation'`); err == nil {
		t.Fatal("plan generation must not silently use template fallback")
	}
	var violation string
	if err := db.QueryRow(`PRAGMA foreign_key_check`).Scan(&violation); err != sql.ErrNoRows {
		t.Fatalf("foreign key check: %v", err)
	}
}
