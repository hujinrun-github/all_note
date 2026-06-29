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

func TestMultiUserAuthMigrationEnforcesDefaultOwnedWorkspace(t *testing.T) {
	schema := fmt.Sprintf("fs_test_auth_default_ws_%d", time.Now().UnixNano())
	db := openPostgresTestDB(t, schema)
	defer db.Close()

	ctx := context.Background()
	if err := RunPostgresMigrationsContext(ctx, db); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO users (id, email, display_name, password_hash, role, status)
		VALUES ('user_owner', 'owner@example.com', 'Owner', 'hash', 'admin', 'active');
		INSERT INTO workspaces (id, name, owner_user_id)
		VALUES ('workspace_owner', 'Owner Workspace', 'user_owner');
		UPDATE users SET default_workspace_id = 'workspace_owner' WHERE id = 'user_owner';
		UPDATE folders SET workspace_id = 'workspace_owner';
		UPDATE task_projects SET workspace_id = 'workspace_owner';
	`); err != nil {
		t.Fatalf("seed valid owner workspace: %v", err)
	}
	if err := runMultiUserAuthFinalizer(ctx, db); err != nil {
		t.Fatalf("finalizer: %v", err)
	}

	_, err := db.ExecContext(ctx, `
		INSERT INTO users (id, email, display_name, password_hash, must_change_password, default_workspace_id, role, status)
		VALUES ('user_a', 'a@example.com', 'A', 'hash', false, 'workspace_b', 'user', 'active')
	`)
	if err == nil {
		t.Fatal("expected default workspace ownership constraint to reject invalid row")
	}
}

func TestMultiUserAuthMigrationCreatesPlannedAuthSchema(t *testing.T) {
	schema := fmt.Sprintf("fs_test_auth_schema_%d", time.Now().UnixNano())
	db := openPostgresTestDB(t, schema)
	defer db.Close()

	if err := RunPostgresMigrationsContext(context.Background(), db); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	for _, column := range []struct {
		table string
		name  string
	}{
		{"users", "last_login_at"},
		{"users", "password_changed_at"},
		{"sessions", "workspace_id"},
		{"sessions", "user_agent"},
		{"sessions", "ip_address"},
		{"sessions", "last_seen_at"},
		{"audit_events", "target_user_id"},
	} {
		assertPostgresColumnExists(t, db, schema, column.table, column.name)
	}
	for _, column := range []struct {
		table string
		name  string
	}{
		{"sessions", "updated_at"},
		{"workspace_members", "updated_at"},
		{"audit_events", "entity_type"},
		{"audit_events", "entity_id"},
	} {
		assertPostgresColumnMissing(t, db, schema, column.table, column.name)
	}
	assertPostgresColumnDefault(t, db, schema, "workspace_members", "role", "'owner'::text")
	assertPostgresColumnDefault(t, db, schema, "users", "must_change_password", "false")
	assertPostgresColumnDefault(t, db, schema, "sessions", "last_seen_at", "now()")
	assertPostgresColumnNullable(t, db, schema, "sessions", "last_seen_at", "NO")
}

func TestMultiUserAuthFinalizerCreatesDeferrableDefaultWorkspaceFK(t *testing.T) {
	schema := fmt.Sprintf("fs_test_auth_deferrable_ws_%d", time.Now().UnixNano())
	db := openPostgresTestDB(t, schema)
	defer db.Close()

	ctx := context.Background()
	if err := RunPostgresMigrationsContext(ctx, db); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO users (id, email, display_name, password_hash, role, status)
		VALUES ('user_existing', 'existing@example.com', 'Existing', 'hash', 'admin', 'active');
		INSERT INTO workspaces (id, name, owner_user_id)
		VALUES ('workspace_existing', 'Existing Workspace', 'user_existing');
		UPDATE users SET default_workspace_id = 'workspace_existing' WHERE id = 'user_existing';
		UPDATE folders SET workspace_id = 'workspace_existing';
		UPDATE task_projects SET workspace_id = 'workspace_existing';
	`); err != nil {
		t.Fatalf("seed finalizer data: %v", err)
	}
	if err := runMultiUserAuthFinalizer(ctx, db); err != nil {
		t.Fatalf("finalizer: %v", err)
	}
	if err := runMultiUserAuthFinalizer(ctx, db); err != nil {
		t.Fatalf("second finalizer should be idempotent: %v", err)
	}

	var deferrable, deferred bool
	if err := db.QueryRowContext(ctx, `
		SELECT condeferrable, condeferred
		FROM pg_constraint
		WHERE conname = 'users_default_owned_workspace_fk'
	`).Scan(&deferrable, &deferred); err != nil {
		t.Fatalf("read default workspace constraint: %v", err)
	}
	if !deferrable || !deferred {
		t.Fatalf("constraint deferrable=%v deferred=%v, want true/true", deferrable, deferred)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO users (id, email, display_name, password_hash, must_change_password, default_workspace_id, role, status)
		VALUES ('user_later_workspace', 'later@example.com', 'Later', 'hash', true, 'workspace_later', 'user', 'active')
	`); err != nil {
		t.Fatalf("insert user before workspace should be deferred: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO workspaces (id, name, owner_user_id)
		VALUES ('workspace_later', 'Later Workspace', 'user_later_workspace')
	`); err != nil {
		t.Fatalf("insert later workspace: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit deferred default workspace FK: %v", err)
	}
}

func TestMultiUserAuthFinalizerAllowsWorkspaceScopedDefaultFolderAndProjectIDs(t *testing.T) {
	schema := fmt.Sprintf("fs_test_auth_scoped_defaults_%d", time.Now().UnixNano())
	db := openPostgresTestDB(t, schema)
	defer db.Close()

	ctx := context.Background()
	seedFinalizedWorkspace(t, ctx, db, "user_workspace_a", "workspace_a")
	if _, err := db.ExecContext(ctx, `
		INSERT INTO folders (id, name, sort_order, workspace_id)
		VALUES ('workspace_a_only_folder', 'Workspace A Only Folder', 99, 'workspace_a');
		INSERT INTO task_projects (id, name, type, description, workspace_id)
		VALUES ('workspace_a_only_project', 'Workspace A Only Project', 'regular', '', 'workspace_a');
	`); err != nil {
		t.Fatalf("seed workspace A scoped rows: %v", err)
	}
	if err := runMultiUserAuthFinalizer(ctx, db); err != nil {
		t.Fatalf("finalizer: %v", err)
	}

	insertPostgresWorkspace(t, ctx, db, "user_workspace_b", "workspace_b")
	if _, err := db.ExecContext(ctx, `
		INSERT INTO folders (id, name, sort_order, workspace_id)
		VALUES
			('__uncategorized', 'Uncategorized', 0, 'workspace_b'),
			('__work', 'Work', 1, 'workspace_b'),
			('__personal', 'Personal', 2, 'workspace_b');
		INSERT INTO task_projects (id, name, type, description, workspace_id)
		VALUES ('personal', 'Personal', 'personal', 'Default personal task project', 'workspace_b');
	`); err != nil {
		t.Fatalf("insert workspace B default rows with reused IDs: %v", err)
	}

	assertRowCount(t, db, `SELECT COUNT(*) FROM folders WHERE id IN ('__uncategorized', '__work', '__personal')`, 6)
	assertRowCount(t, db, `SELECT COUNT(*) FROM task_projects WHERE id = 'personal'`, 2)

	if _, err := db.ExecContext(ctx, `
		INSERT INTO notes (id, title, body, folder_id, tags, workspace_id)
		VALUES ('note_workspace_b_default', 'Workspace B Default Note', '', '__work', '{}'::text[], 'workspace_b');
		INSERT INTO tasks (id, title, content, project_id, workspace_id)
		VALUES ('task_workspace_b_default', 'Workspace B Default Task', '', 'personal', 'workspace_b');
		INSERT INTO note_project_links (note_id, project_id, workspace_id)
		VALUES ('note_workspace_b_default', 'personal', 'workspace_b');
		INSERT INTO learning_roadmaps (id, project_id, title, goal, workspace_id)
		VALUES ('roadmap_workspace_b_default', 'personal', 'Workspace B Default Roadmap', '', 'workspace_b');
	`); err != nil {
		t.Fatalf("insert workspace B child rows for reused defaults: %v", err)
	}

	for _, tc := range []struct {
		name string
		sql  string
	}{
		{
			name: "notes folder",
			sql: `INSERT INTO notes (id, title, body, folder_id, tags, workspace_id)
				VALUES ('note_cross_workspace_folder', 'Cross Workspace Folder', '', 'workspace_a_only_folder', '{}'::text[], 'workspace_b')`,
		},
		{
			name: "tasks project",
			sql: `INSERT INTO tasks (id, title, content, project_id, workspace_id)
				VALUES ('task_cross_workspace_project', 'Cross Workspace Project', '', 'workspace_a_only_project', 'workspace_b')`,
		},
		{
			name: "note project links project",
			sql: `INSERT INTO note_project_links (note_id, project_id, workspace_id)
				VALUES ('note_workspace_b_default', 'workspace_a_only_project', 'workspace_b')`,
		},
		{
			name: "learning roadmaps project",
			sql: `INSERT INTO learning_roadmaps (id, project_id, title, goal, workspace_id)
				VALUES ('roadmap_cross_workspace_project', 'workspace_a_only_project', 'Cross Workspace Roadmap', '', 'workspace_b')`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := db.ExecContext(ctx, tc.sql); err == nil {
				t.Fatalf("expected %s to reject cross-workspace reference", tc.name)
			}
		})
	}
}

func TestMultiUserAuthFinalizerRejectsBusinessRowsWithoutWorkspace(t *testing.T) {
	schema := fmt.Sprintf("fs_test_auth_business_ws_%d", time.Now().UnixNano())
	db := openPostgresTestDB(t, schema)
	defer db.Close()

	ctx := context.Background()
	if err := RunPostgresMigrationsContext(ctx, db); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO users (id, email, display_name, password_hash, role, status)
		VALUES ('user_owner', 'strict-owner@example.com', 'Owner', 'hash', 'admin', 'active');
		INSERT INTO workspaces (id, name, owner_user_id)
		VALUES ('workspace_owner', 'Owner Workspace', 'user_owner');
		UPDATE users SET default_workspace_id = 'workspace_owner' WHERE id = 'user_owner';
		UPDATE folders SET workspace_id = 'workspace_owner';
		UPDATE task_projects SET workspace_id = 'workspace_owner';
	`); err != nil {
		t.Fatalf("seed workspace backfill: %v", err)
	}
	if err := runMultiUserAuthFinalizer(ctx, db); err != nil {
		t.Fatalf("finalizer: %v", err)
	}
	if err := runMultiUserAuthFinalizer(ctx, db); err != nil {
		t.Fatalf("second finalizer should be idempotent: %v", err)
	}
	for _, column := range []struct {
		table string
		name  string
	}{
		{"users", "default_workspace_id"},
		{"notes", "workspace_id"},
		{"tasks", "workspace_id"},
		{"events", "workspace_id"},
	} {
		assertPostgresColumnNullable(t, db, schema, column.table, column.name, "NO")
	}
	assertPostgresNoUnvalidatedWorkspaceConstraints(t, db)

	for _, tc := range []struct {
		name string
		sql  string
	}{
		{
			name: "notes",
			sql: `INSERT INTO notes (id, title, body, folder_id, tags)
				VALUES ('note_without_workspace', 'No Workspace', '', '__uncategorized', '{}'::text[])`,
		},
		{
			name: "tasks",
			sql: `INSERT INTO tasks (id, title, content, project_id)
				VALUES ('task_without_workspace', 'No Workspace', '', 'personal')`,
		},
		{
			name: "events",
			sql: `INSERT INTO events (id, title, start_at, end_at, time_range)
				VALUES ('event_without_workspace', 'No Workspace', now(), now() + interval '1 hour', tstzrange(now(), now() + interval '1 hour'))`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := db.ExecContext(ctx, tc.sql); err == nil {
				t.Fatalf("expected %s insert without workspace_id to fail", tc.name)
			}
		})
	}
}

func TestMultiUserAuthFinalizerScopesConstraintLookupToCurrentSchema(t *testing.T) {
	ctx := context.Background()
	schemaA := fmt.Sprintf("fs_test_auth_constraint_a_%d", time.Now().UnixNano())
	dbA := openPostgresTestDB(t, schemaA)
	defer dbA.Close()
	seedFinalizedWorkspace(t, ctx, dbA, "user_owner_a", "workspace_owner_a")
	if err := runMultiUserAuthFinalizer(ctx, dbA); err != nil {
		t.Fatalf("finalize schema A: %v", err)
	}

	schemaB := fmt.Sprintf("fs_test_auth_constraint_b_%d", time.Now().UnixNano())
	dbB := openPostgresTestDB(t, schemaB)
	defer dbB.Close()
	seedFinalizedWorkspace(t, ctx, dbB, "user_owner_b", "workspace_owner_b")
	if err := runMultiUserAuthFinalizer(ctx, dbB); err != nil {
		t.Fatalf("finalize schema B: %v", err)
	}

	assertRowCount(t, dbB, `
		SELECT COUNT(*)
		FROM pg_constraint c
		JOIN pg_class rel ON rel.oid = c.conrelid
		JOIN pg_namespace n ON n.oid = rel.relnamespace
		WHERE n.nspname = current_schema()
			AND rel.relname = 'users'
			AND c.conname = 'users_default_owned_workspace_fk'
	`, 1)

	if _, err := dbB.ExecContext(ctx, `
		INSERT INTO users (id, email, display_name, password_hash, must_change_password, default_workspace_id, role, status)
		VALUES ('user_invalid_b', 'invalid-b@example.com', 'Invalid', 'hash', false, 'workspace_owner_b', 'user', 'active')
	`); err == nil {
		t.Fatal("expected schema B ownership FK to reject another user's workspace")
	}
}

func TestSetPostgresColumnNotNullChecksCurrentNullability(t *testing.T) {
	schema := fmt.Sprintf("fs_test_auth_not_null_fast_path_%d", time.Now().UnixNano())
	db := openPostgresTestDB(t, schema)
	defer db.Close()

	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE not_null_probe (
			id TEXT PRIMARY KEY,
			already_required TEXT NOT NULL,
			later_required TEXT
		)
	`); err != nil {
		t.Fatalf("create not null probe: %v", err)
	}

	nullable, err := postgresColumnIsNullable(ctx, db, "not_null_probe", "already_required")
	if err != nil {
		t.Fatalf("check already_required nullability: %v", err)
	}
	if nullable {
		t.Fatal("already_required should start NOT NULL")
	}
	if err := setPostgresColumnNotNull(ctx, db, "not_null_probe", "already_required"); err != nil {
		t.Fatalf("set already_required not null should be a no-op: %v", err)
	}

	nullable, err = postgresColumnIsNullable(ctx, db, "not_null_probe", "later_required")
	if err != nil {
		t.Fatalf("check later_required nullability: %v", err)
	}
	if !nullable {
		t.Fatal("later_required should start nullable")
	}
	if err := setPostgresColumnNotNull(ctx, db, "not_null_probe", "later_required"); err != nil {
		t.Fatalf("set later_required not null: %v", err)
	}
	nullable, err = postgresColumnIsNullable(ctx, db, "not_null_probe", "later_required")
	if err != nil {
		t.Fatalf("recheck later_required nullability: %v", err)
	}
	if nullable {
		t.Fatal("later_required should be NOT NULL after helper")
	}
	if err := setPostgresColumnNotNull(ctx, db, "not_null_probe", "later_required"); err != nil {
		t.Fatalf("second set later_required not null should be a no-op: %v", err)
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

func seedFinalizedWorkspace(t *testing.T, ctx context.Context, db *sql.DB, userID, workspaceID string) {
	t.Helper()

	if err := RunPostgresMigrationsContext(ctx, db); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO users (id, email, display_name, password_hash, role, status)
		VALUES ($1, $2, 'Owner', 'hash', 'admin', 'active')
	`, userID, userID+"@example.com"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO workspaces (id, name, owner_user_id)
		VALUES ($1, 'Owner Workspace', $2)
	`, workspaceID, userID); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE users SET default_workspace_id = $1 WHERE id = $2
	`, workspaceID, userID); err != nil {
		t.Fatalf("backfill user default workspace: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE folders SET workspace_id = $1`, workspaceID); err != nil {
		t.Fatalf("backfill folders: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE task_projects SET workspace_id = $1`, workspaceID); err != nil {
		t.Fatalf("backfill task projects: %v", err)
	}
}

func insertPostgresWorkspace(t *testing.T, ctx context.Context, db *sql.DB, userID, workspaceID string) {
	t.Helper()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin workspace insert: %v", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO users (id, email, display_name, password_hash, must_change_password, default_workspace_id, role, status)
		VALUES ($1, $2, 'Workspace User', 'hash', false, $3, 'user', 'active')
	`, userID, userID+"@example.com", workspaceID); err != nil {
		t.Fatalf("insert user %s: %v", userID, err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO workspaces (id, name, owner_user_id)
		VALUES ($1, 'Workspace', $2)
	`, workspaceID, userID); err != nil {
		t.Fatalf("insert workspace %s: %v", workspaceID, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit workspace insert: %v", err)
	}
}

func assertPostgresColumnExists(t *testing.T, db *sql.DB, schema, table, column string) {
	t.Helper()

	var exists bool
	if err := db.QueryRow(`
		SELECT EXISTS (
			SELECT 1
			FROM information_schema.columns
			WHERE table_schema = $1 AND table_name = $2 AND column_name = $3
		)
	`, schema, table, column).Scan(&exists); err != nil {
		t.Fatalf("check column %s.%s: %v", table, column, err)
	}
	if !exists {
		t.Fatalf("expected column %s.%s to exist", table, column)
	}
}

func assertPostgresColumnMissing(t *testing.T, db *sql.DB, schema, table, column string) {
	t.Helper()

	var exists bool
	if err := db.QueryRow(`
		SELECT EXISTS (
			SELECT 1
			FROM information_schema.columns
			WHERE table_schema = $1 AND table_name = $2 AND column_name = $3
		)
	`, schema, table, column).Scan(&exists); err != nil {
		t.Fatalf("check column %s.%s: %v", table, column, err)
	}
	if exists {
		t.Fatalf("expected column %s.%s to be absent", table, column)
	}
}

func assertPostgresColumnDefault(t *testing.T, db *sql.DB, schema, table, column, want string) {
	t.Helper()

	var got sql.NullString
	if err := db.QueryRow(`
		SELECT column_default
		FROM information_schema.columns
		WHERE table_schema = $1 AND table_name = $2 AND column_name = $3
	`, schema, table, column).Scan(&got); err != nil {
		t.Fatalf("check default %s.%s: %v", table, column, err)
	}
	if !got.Valid || got.String != want {
		t.Fatalf("default %s.%s = %q, want %q", table, column, got.String, want)
	}
}

func assertPostgresColumnNullable(t *testing.T, db *sql.DB, schema, table, column, want string) {
	t.Helper()

	var got string
	if err := db.QueryRow(`
		SELECT is_nullable
		FROM information_schema.columns
		WHERE table_schema = $1 AND table_name = $2 AND column_name = $3
	`, schema, table, column).Scan(&got); err != nil {
		t.Fatalf("check nullability %s.%s: %v", table, column, err)
	}
	if got != want {
		t.Fatalf("nullability %s.%s = %q, want %q", table, column, got, want)
	}
}

func assertPostgresNoUnvalidatedWorkspaceConstraints(t *testing.T, db *sql.DB) {
	t.Helper()

	var count int
	if err := db.QueryRow(`
		SELECT COUNT(*)
		FROM pg_constraint
		WHERE convalidated = false
			AND (
				conname = 'users_default_workspace_id_not_null'
				OR conname LIKE '%_workspace_id_not_null'
			)
	`).Scan(&count); err != nil {
		t.Fatalf("check unvalidated workspace constraints: %v", err)
	}
	if count != 0 {
		t.Fatalf("found %d unvalidated workspace constraints, want 0", count)
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
