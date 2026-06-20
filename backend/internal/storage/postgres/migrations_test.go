package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRunPostgresMigrationsCreatesCoreTablesSeedDataAndIsIdempotent(t *testing.T) {
	schema := fmt.Sprintf("fs_test_migrations_%d", time.Now().UnixNano())
	db := openPostgresTestDB(t, schema)

	if err := runPostgresMigrations(db); err != nil {
		t.Fatalf("first run migrations: %v", err)
	}
	if err := runPostgresMigrations(db); err != nil {
		t.Fatalf("second run migrations must be idempotent: %v", err)
	}

	for _, table := range []string{
		"schema_migrations",
		"folders",
		"notes",
		"task_projects",
		"tasks",
		"learning_roadmaps",
		"roadmap_nodes",
		"roadmap_edges",
		"roadmap_resources",
		"events",
		"inbox",
		"sync_targets",
		"note_sync_state",
		"search_index",
	} {
		var exists bool
		if err := db.QueryRow(`SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = $1 AND table_name = $2
		)`, schema, table).Scan(&exists); err != nil {
			t.Fatalf("check table %s: %v", table, err)
		}
		if !exists {
			t.Fatalf("expected table %s to exist", table)
		}
	}

	assertRowCount(t, db, `SELECT COUNT(*) FROM folders WHERE id IN ('__uncategorized', '__work', '__personal')`, 3)
	assertRowCount(t, db, `SELECT COUNT(*) FROM task_projects WHERE id = 'personal'`, 1)
	assertRowCount(t, db, `SELECT COUNT(*) FROM schema_migrations WHERE version = '0001_init_postgres.sql'`, 1)
	assertRowCount(t, db, `
		SELECT COUNT(*)
		FROM pg_extension e
		JOIN pg_namespace n ON n.oid = e.extnamespace
		WHERE e.extname = 'pg_trgm' AND n.nspname = 'public'
	`, 1)
	assertColumnType(t, db, schema, "events", "time_range", "tstzrange")
	assertColumnType(t, db, schema, "search_index", "search_vector", "tsvector")
}

func TestApplyPostgresMigrationSerializesConcurrentStartup(t *testing.T) {
	schema := fmt.Sprintf("fs_test_migration_lock_%d", time.Now().UnixNano())
	db := openPostgresTestDB(t, schema)

	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY,
			checksum TEXT NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}

	sqlBytes := []byte(`
		SELECT pg_sleep(0.2);
		CREATE TABLE concurrent_migration_guard (id INTEGER PRIMARY KEY);
	`)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- applyPostgresMigration(db, "9999_concurrent_guard.sql", sqlBytes)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent migration should serialize and succeed: %v", err)
		}
	}

	assertRowCount(t, db, `SELECT COUNT(*) FROM schema_migrations WHERE version = '9999_concurrent_guard.sql'`, 1)
	assertRowCount(t, db, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = 'concurrent_migration_guard'`, 1)
}

func TestRunPostgresMigrationsSerializesConcurrentEmptySchemaStartup(t *testing.T) {
	schema := fmt.Sprintf("fs_test_run_lock_%d", time.Now().UnixNano())
	db := openPostgresTestDB(t, schema)

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- RunPostgresMigrations(db)
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent RunPostgresMigrations should serialize and succeed: %v", err)
		}
	}
	assertRowCount(t, db, `SELECT COUNT(*) FROM schema_migrations WHERE version = '0001_init_postgres.sql'`, 1)
	assertRowCount(t, db, `SELECT COUNT(*) FROM folders WHERE id IN ('__uncategorized', '__work', '__personal')`, 3)
}

func TestRunPostgresMigrationsWaitsForAdvisoryLockBeforeBootstrap(t *testing.T) {
	schema := fmt.Sprintf("fs_test_bootstrap_lock_%d", time.Now().UnixNano())
	db := openPostgresTestDB(t, schema)

	lockConn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatalf("open lock connection: %v", err)
	}
	defer lockConn.Close()
	if _, err := lockConn.ExecContext(context.Background(), `SELECT pg_advisory_lock(hashtext('flowspace_schema_migrations'))`); err != nil {
		t.Fatalf("hold advisory lock: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- RunPostgresMigrations(db)
	}()

	time.Sleep(250 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("RunPostgresMigrations returned before advisory lock was released: %v", err)
	default:
	}
	assertRowCount(t, db, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = 'schema_migrations'`, 0)

	if _, err := lockConn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(hashtext('flowspace_schema_migrations'))`); err != nil {
		t.Fatalf("release advisory lock: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("run migrations after releasing advisory lock: %v", err)
	}
	assertRowCount(t, db, `SELECT COUNT(*) FROM schema_migrations WHERE version = '0001_init_postgres.sql'`, 1)
}

func TestRunPostgresMigrationsContextTimesOutWaitingForAdvisoryLock(t *testing.T) {
	schema := fmt.Sprintf("fs_test_context_lock_%d", time.Now().UnixNano())
	db := openPostgresTestDB(t, schema)

	lockConn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatalf("open lock connection: %v", err)
	}
	defer lockConn.Close()
	if _, err := lockConn.ExecContext(context.Background(), `SELECT pg_advisory_lock(hashtext('flowspace_schema_migrations'))`); err != nil {
		t.Fatalf("hold advisory lock: %v", err)
	}
	defer func() {
		_, _ = lockConn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(hashtext('flowspace_schema_migrations'))`)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	startedAt := time.Now()
	err = RunPostgresMigrationsContext(ctx, db)
	elapsed := time.Since(startedAt)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context deadline exceeded, got %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("expected migration lock wait to honor context promptly, took %s", elapsed)
	}
	assertRowCount(t, db, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = 'schema_migrations'`, 0)
}

func TestRunPostgresMigrationsFromDirUsesSQLFiles(t *testing.T) {
	schema := fmt.Sprintf("fs_test_explicit_dir_%d", time.Now().UnixNano())
	db := openPostgresTestDB(t, schema)
	dir := t.TempDir()
	migrationPath := filepath.Join(dir, "4242_explicit_dir.sql")
	if err := os.WriteFile(migrationPath, []byte(`
		CREATE TABLE explicit_dir_probe (
			id TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);
		INSERT INTO explicit_dir_probe (id, value) VALUES ('from-file', 'read from explicit migration dir');
	`), 0o600); err != nil {
		t.Fatalf("write explicit migration file: %v", err)
	}

	if err := RunPostgresMigrationsFromDir(db, dir); err != nil {
		t.Fatalf("run migrations from explicit dir: %v", err)
	}

	assertRowCount(t, db, `SELECT COUNT(*) FROM explicit_dir_probe WHERE id = 'from-file'`, 1)
	assertRowCount(t, db, `SELECT COUNT(*) FROM schema_migrations WHERE version = '4242_explicit_dir.sql'`, 1)
}

func TestRunPostgresMigrationsReplacesCustomLastDirectionCheck(t *testing.T) {
	schema := fmt.Sprintf("fs_test_custom_last_direction_%d", time.Now().UnixNano())
	db := openPostgresTestDB(t, schema)

	if err := RunPostgresMigrations(db); err != nil {
		t.Fatalf("initial migrations: %v", err)
	}
	if _, err := db.Exec(`
		ALTER TABLE note_sync_state DROP CONSTRAINT IF EXISTS note_sync_state_last_direction_check;
		ALTER TABLE note_sync_state
			ADD CONSTRAINT custom_legacy_last_direction_only_delete
			CHECK (last_direction IN ('push', 'pull', 'import', 'restore', 'delete') OR last_direction IS NULL);
		DELETE FROM schema_migrations WHERE version = '0002_single_note_sync_target.sql';
	`); err != nil {
		t.Fatalf("install custom legacy last_direction check: %v", err)
	}

	if err := RunPostgresMigrations(db); err != nil {
		t.Fatalf("rerun migrations: %v", err)
	}

	assertRowCount(t, db, `
		SELECT COUNT(*)
		FROM pg_constraint
		WHERE conrelid = 'note_sync_state'::regclass
			AND contype = 'c'
			AND conname = 'custom_legacy_last_direction_only_delete'
	`, 0)
	assertRowCount(t, db, `
		SELECT COUNT(*)
		FROM pg_constraint
		WHERE conrelid = 'note_sync_state'::regclass
			AND contype = 'c'
			AND conname = 'note_sync_state_last_direction_check'
			AND pg_get_constraintdef(oid) LIKE '%delete_detected%'
	`, 1)

	if _, err := db.Exec(`
		INSERT INTO sync_targets (id, type, name, config, enabled)
		VALUES ('target-delete-detected', 'notion', 'Delete Detected Target', '{}'::jsonb, true);
		INSERT INTO notes (id, title, body, folder_id, tags)
		VALUES ('note-delete-detected', 'Delete Detected Note', '', '__uncategorized', '{}'::text[]);
		INSERT INTO note_sync_state (
			note_id, target_id, external_path, content_hash, last_direction, status
		)
		VALUES (
			'note-delete-detected', 'target-delete-detected', 'remote/path', 'hash', 'delete_detected', 'synced'
		);
	`); err != nil {
		t.Fatalf("insert delete_detected state after migration: %v", err)
	}
}

func TestWrapPostgresMigrationErrorMentionsPgTrgmBootstrap(t *testing.T) {
	err := wrapPostgresMigrationError("0001_init_postgres.sql", []byte(`CREATE EXTENSION IF NOT EXISTS pg_trgm`), fmt.Errorf(`permission denied to create extension "pg_trgm"`))
	if err == nil {
		t.Fatal("expected wrapped error")
	}
	if !strings.Contains(err.Error(), "pg_trgm extension privilege/bootstrap failed") {
		t.Fatalf("expected pg_trgm bootstrap guidance, got %v", err)
	}
}

func assertColumnType(t *testing.T, db *sql.DB, schema, table, column, want string) {
	t.Helper()

	var got string
	if err := db.QueryRow(`
		SELECT udt_name
		FROM information_schema.columns
		WHERE table_schema = $1 AND table_name = $2 AND column_name = $3
	`, schema, table, column).Scan(&got); err != nil {
		t.Fatalf("check %s.%s type: %v", table, column, err)
	}
	if got != want {
		t.Fatalf("expected %s.%s type %s, got %s", table, column, want, got)
	}
}
