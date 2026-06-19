package migration

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/storage/postgres"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/lib/pq"
	_ "modernc.org/sqlite"
)

func TestSQLiteToPostgresMigratesCoreDataAndSearchIndex(t *testing.T) {
	basePGURL := os.Getenv("FLOWSPACE_TEST_DATABASE_URL")
	if basePGURL == "" {
		t.Skip("FLOWSPACE_TEST_DATABASE_URL is required")
	}
	pgURL := createMigrationPostgresSchema(t, basePGURL, fmt.Sprintf("fs_test_migration_%d", time.Now().UnixNano()))
	sqlitePath := seedMigrationSQLite(t)

	if err := MigrateSQLiteToPostgres(sqlitePath, pgURL); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	pgDB, err := sql.Open("pgx", pgURL)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	defer pgDB.Close()

	assertTableCount(t, pgDB, "folders", 4)
	assertTableCount(t, pgDB, "notes", 1)
	assertTableCount(t, pgDB, "task_projects", 2)
	assertTableCount(t, pgDB, "tasks", 1)
	assertTableCount(t, pgDB, "learning_roadmaps", 1)
	assertTableCount(t, pgDB, "roadmap_nodes", 2)
	assertTableCount(t, pgDB, "roadmap_edges", 1)
	assertTableCount(t, pgDB, "roadmap_resources", 1)
	assertTableCount(t, pgDB, "events", 1)
	assertTableCount(t, pgDB, "inbox", 1)
	assertTableCount(t, pgDB, "sync_targets", 1)
	assertTableCount(t, pgDB, "note_sync_bindings", 1)
	assertTableCount(t, pgDB, "sync_external_claims", 1)
	assertTableCount(t, pgDB, "note_sync_suppressions", 1)
	assertTableCount(t, pgDB, "sync_import_tombstones", 1)
	assertTableCount(t, pgDB, "note_sync_state", 1)
	assertTableCount(t, pgDB, "search_index", 3)

	var folderName string
	var folderSort float64
	if err := pgDB.QueryRow(`SELECT name, sort_order FROM folders WHERE id = '__personal'`).Scan(&folderName, &folderSort); err != nil {
		t.Fatalf("query personal folder: %v", err)
	}
	if folderName != "Custom Personal Folder" || folderSort != 42 {
		t.Fatalf("expected SQLite default folder to override seed, got %q %v", folderName, folderSort)
	}

	var tags []string
	if err := pgDB.QueryRow(`SELECT tags FROM notes WHERE id = 'note-1'`).Scan(pq.Array(&tags)); err != nil {
		t.Fatalf("query tags: %v", err)
	}
	if len(tags) != 2 || tags[0] != "sync" || tags[1] != "publish" {
		t.Fatalf("unexpected tags: %#v", tags)
	}

	var tokenEnv string
	if err := pgDB.QueryRow(`SELECT config->>'token_env' FROM sync_targets WHERE id = 'target-1'`).Scan(&tokenEnv); err != nil {
		t.Fatalf("query config: %v", err)
	}
	if tokenEnv != "FLOWSPACE_NOTION_TOKEN" {
		t.Fatalf("unexpected token env: %q", tokenEnv)
	}

	var isDefault bool
	if err := pgDB.QueryRow(`SELECT is_default FROM sync_targets WHERE id = 'target-1'`).Scan(&isDefault); err != nil {
		t.Fatalf("query sync target default flag: %v", err)
	}
	if !isDefault {
		t.Fatal("expected sync target default flag to migrate")
	}

	var bindingTarget string
	if err := pgDB.QueryRow(`SELECT target_id FROM note_sync_bindings WHERE note_id = 'note-1'`).Scan(&bindingTarget); err != nil {
		t.Fatalf("query migrated binding: %v", err)
	}
	if bindingTarget != "target-1" {
		t.Fatalf("unexpected migrated binding target: %q", bindingTarget)
	}

	var claimNoteID string
	var claimType string
	if err := pgDB.QueryRow(`SELECT note_id, external_type FROM sync_external_claims WHERE external_key = 'notion:external-1'`).Scan(&claimNoteID, &claimType); err != nil {
		t.Fatalf("query migrated claim: %v", err)
	}
	if claimNoteID != "note-1" || claimType != "notion_page" {
		t.Fatalf("unexpected migrated claim: note_id=%q type=%q", claimNoteID, claimType)
	}

	var suppressionReason string
	if err := pgDB.QueryRow(`SELECT reason FROM note_sync_suppressions WHERE note_id = 'note-1' AND target_id = 'target-1'`).Scan(&suppressionReason); err != nil {
		t.Fatalf("query migrated suppression: %v", err)
	}
	if suppressionReason != "target_changed" {
		t.Fatalf("unexpected migrated suppression reason: %q", suppressionReason)
	}

	var tombstoneReason string
	if err := pgDB.QueryRow(`SELECT reason FROM sync_import_tombstones WHERE external_key = 'notion:deleted-page'`).Scan(&tombstoneReason); err != nil {
		t.Fatalf("query migrated tombstone: %v", err)
	}
	if tombstoneReason != "note_deleted" {
		t.Fatalf("unexpected migrated tombstone reason: %q", tombstoneReason)
	}

	var eventOverlaps bool
	if err := pgDB.QueryRow(`
		SELECT time_range && tstzrange(to_timestamp(1800000100), to_timestamp(1800000200), '[)')
		FROM events WHERE id = 'event-1'
	`).Scan(&eventOverlaps); err != nil {
		t.Fatalf("query event range: %v", err)
	}
	if !eventOverlaps {
		t.Fatal("expected migrated event range to overlap")
	}
}

func TestSQLiteToPostgresBackfillsInitialBindingBeforeServiceSwitch(t *testing.T) {
	basePGURL := os.Getenv("FLOWSPACE_TEST_DATABASE_URL")
	if basePGURL == "" {
		t.Skip("FLOWSPACE_TEST_DATABASE_URL is required")
	}
	pgURL := createMigrationPostgresSchema(t, basePGURL, fmt.Sprintf("fs_test_migration_binding_backfill_%d", time.Now().UnixNano()))
	sqlitePath := seedMigrationSQLiteForBindingBackfill(t)

	if err := MigrateSQLiteToPostgres(sqlitePath, pgURL); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	pgDB, err := sql.Open("pgx", pgURL)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	defer pgDB.Close()

	var defaultTarget bool
	if err := pgDB.QueryRow(`SELECT is_default FROM sync_targets WHERE id = 'target-new'`).Scan(&defaultTarget); err != nil {
		t.Fatalf("query default target: %v", err)
	}
	if !defaultTarget {
		t.Fatal("expected single enabled obsidian target to be backfilled as default")
	}

	var bindingTarget string
	if err := pgDB.QueryRow(`SELECT target_id FROM note_sync_bindings WHERE note_id = 'note-legacy'`).Scan(&bindingTarget); err != nil {
		t.Fatalf("query backfilled binding: %v", err)
	}
	if bindingTarget != "target-new" {
		t.Fatalf("binding target = %q, want target-new", bindingTarget)
	}

	var claimNoteID string
	if err := pgDB.QueryRow(`SELECT note_id FROM sync_external_claims WHERE external_key = $1`, `obsidian:D:\Vault\FlowSpace Notes\New.md`).Scan(&claimNoteID); err != nil {
		t.Fatalf("query backfilled claim: %v", err)
	}
	if claimNoteID != "note-legacy" {
		t.Fatalf("claim note = %q, want note-legacy", claimNoteID)
	}

	var tombstoneReason string
	if err := pgDB.QueryRow(`SELECT reason FROM sync_import_tombstones WHERE external_key = 'notion:old-page'`).Scan(&tombstoneReason); err != nil {
		if err := pgDB.QueryRow(`
			SELECT reason
			FROM sync_import_tombstones
			WHERE target_id = 'target-old'
				AND former_note_id = 'note-legacy'
				AND external_type = 'notion_page'
		`).Scan(&tombstoneReason); err != nil {
			t.Fatalf("query losing target tombstone: %v", err)
		}
	}
	if tombstoneReason != "target_changed" {
		t.Fatalf("tombstone reason = %q, want target_changed", tombstoneReason)
	}

	var lastDirection string
	if err := pgDB.QueryRow(`SELECT last_direction FROM note_sync_state WHERE note_id = 'note-deleted'`).Scan(&lastDirection); err != nil {
		t.Fatalf("query delete direction: %v", err)
	}
	if lastDirection != "delete_detected" {
		t.Fatalf("last_direction = %q, want delete_detected", lastDirection)
	}
}

func TestSQLiteToPostgresValidatesSourceBeforeTouchingPostgres(t *testing.T) {
	basePGURL := os.Getenv("FLOWSPACE_TEST_DATABASE_URL")
	if basePGURL == "" {
		t.Skip("FLOWSPACE_TEST_DATABASE_URL is required")
	}
	pgURL := createMigrationPostgresSchema(t, basePGURL, fmt.Sprintf("fs_test_migration_preflight_%d", time.Now().UnixNano()))
	sqlitePath := createSQLiteWithSchema(t)
	sqliteDB, err := sql.Open("sqlite", sqlitePath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqliteDB.Close()
	if _, err := sqliteDB.Exec(`
		INSERT INTO tasks (id, title, content, priority, done, status, horizon, scope, created_at, updated_at)
		VALUES ('bad-priority', 'bad priority task', 'invalid', -1, 0, 'open', 'week', 'daily', 1800000000, 1800000000)
	`); err != nil {
		t.Fatalf("seed invalid task: %v", err)
	}

	err = MigrateSQLiteToPostgres(sqlitePath, pgURL)
	if err == nil {
		t.Fatal("expected migration to fail")
	}
	if !strings.Contains(err.Error(), "tasks.priority") {
		t.Fatalf("expected tasks.priority validation error, got %v", err)
	}

	pgDB, err := sql.Open("pgx", pgURL)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	defer pgDB.Close()
	var exists bool
	if err := pgDB.QueryRow(`
		SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = current_schema() AND table_name = 'notes'
		)
	`).Scan(&exists); err != nil {
		t.Fatalf("check postgres touched: %v", err)
	}
	if exists {
		t.Fatal("expected source validation to fail before PostgreSQL migrations")
	}
}

func TestSQLiteToPostgresRejectsNonEmptySeedTables(t *testing.T) {
	basePGURL := os.Getenv("FLOWSPACE_TEST_DATABASE_URL")
	if basePGURL == "" {
		t.Skip("FLOWSPACE_TEST_DATABASE_URL is required")
	}
	pgURL := createMigrationPostgresSchema(t, basePGURL, fmt.Sprintf("fs_test_migration_nonempty_%d", time.Now().UnixNano()))
	pgDB, err := sql.Open("pgx", pgURL)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	defer pgDB.Close()
	if err := postgres.RunPostgresMigrationsContext(context.Background(), pgDB); err != nil {
		t.Fatalf("run postgres migrations: %v", err)
	}
	if _, err := pgDB.Exec(`INSERT INTO folders (id, name, sort_order, created_at) VALUES ('existing-folder', 'Existing', 99, now())`); err != nil {
		t.Fatalf("insert existing folder: %v", err)
	}

	err = MigrateSQLiteToPostgres(seedMigrationSQLite(t), pgURL)
	if err == nil {
		t.Fatal("expected migration to reject non-empty target seed table")
	}
	if !strings.Contains(err.Error(), "folders") {
		t.Fatalf("expected folders safety error, got %v", err)
	}
}

func seedMigrationSQLiteForBindingBackfill(t *testing.T) string {
	t.Helper()
	sqlitePath := createSQLiteWithSchema(t)
	sqliteDB, err := sql.Open("sqlite", sqlitePath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqliteDB.Close()
	if _, err := sqliteDB.Exec(`
		INSERT INTO folders (id, name, sort_order, created_at)
		VALUES ('folder-legacy', 'Legacy', 1, 1800000000);

		INSERT INTO notes (id, title, body, folder_id, tags, created_at, updated_at)
		VALUES
			('note-legacy', 'Legacy Synced', 'Body', 'folder-legacy', '[]', 1800000000, 1800000100),
			('note-deleted', 'Deleted Externally', 'Body', 'folder-legacy', '[]', 1800000000, 1800000100);

		INSERT INTO sync_targets (id, type, name, vault_path, base_folder, config_json, enabled, auto_sync, is_default, created_at, updated_at)
		VALUES
			('target-old', 'notion', 'Old Notion', '', '', '{}', 1, 0, 0, 1800000000, 1800000100),
			('target-new', 'obsidian', 'New Vault', 'D:\Vault', 'FlowSpace Notes', '{}', 1, 0, 0, 1800000000, 1800000200);

		INSERT INTO note_sync_state (
			note_id, target_id, external_path, external_id, external_url, content_hash,
			external_hash, external_mtime, last_direction, last_synced_at, status, error_message
		)
		VALUES
			('note-legacy', 'target-old', 'notion/old-page', 'old-page', '', 'old-local', 'old-external', 1800000200, 'push', 1800000200, 'synced', NULL),
			('note-legacy', 'target-new', 'D:\Vault\FlowSpace Notes\New.md', '', '', 'new-local', 'new-external', 1800000300, 'pull', 1800000300, 'synced', NULL),
			('note-deleted', 'target-old', 'notion/deleted-page', 'deleted-page', '', 'deleted-local', 'deleted-external', 1800000400, 'delete_detected', 1800000400, 'external_deleted', NULL);
	`); err != nil {
		t.Fatalf("seed binding backfill sqlite: %v", err)
	}
	if _, err := sqliteDB.Exec(`
		DELETE FROM note_sync_bindings;
		DELETE FROM sync_external_claims;
		DELETE FROM note_sync_suppressions;
		DELETE FROM sync_import_tombstones;
	`); err != nil {
		t.Fatalf("clear new sync tables: %v", err)
	}
	if _, err := sqliteDB.Exec(`
		INSERT INTO sync_import_tombstones (
			external_key, target_id, former_note_id, external_type, external_id, external_path, reason, created_at, updated_at
		)
		VALUES (
			'notion:renamed-old-page', 'target-old', 'note-legacy', 'notion_page', 'renamed-old-page', '', 'target_changed', 1800000100, 1800000100
		);
	`); err != nil {
		t.Fatalf("seed preexisting tombstone: %v", err)
	}
	return sqlitePath
}

func seedMigrationSQLite(t *testing.T) string {
	t.Helper()
	sqlitePath := createSQLiteWithSchema(t)
	sqliteDB, err := sql.Open("sqlite", sqlitePath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqliteDB.Close()
	if _, err := sqliteDB.Exec(`
		UPDATE folders SET name = 'Custom Personal Folder', sort_order = 42 WHERE id = '__personal';
		UPDATE task_projects SET name = 'Custom Personal Project', description = 'user renamed default project' WHERE id = 'personal';

		INSERT INTO folders (id, name, sort_order, created_at)
		VALUES ('folder-1', 'Migration Folder', 3, 1800000000);

		INSERT INTO notes (id, title, body, folder_id, tags, created_at, updated_at)
		VALUES ('note-1', 'Migration Note', 'Body 中文', 'folder-1', '["sync","publish"]', 1800000000, 1800000100);

		INSERT INTO task_projects (id, name, type, description, created_at, updated_at)
		VALUES ('project-1', 'Migration Project', 'learning', 'Learning project', 1800000000, 1800000100);

		INSERT INTO learning_roadmaps (id, project_id, title, goal, status, created_at, updated_at)
		VALUES ('roadmap-1', 'project-1', 'Migration Roadmap', 'Complete migration', 'active', 1800000000, 1800000100);

		INSERT INTO roadmap_nodes (
			id, roadmap_id, parent_id, type, title, description, path_type, status,
			deliverable, acceptance_criteria, x, y, order_index, article_search_queries, created_at, updated_at
		)
		VALUES
			('node-1', 'roadmap-1', NULL, 'phase', 'Understand schema', 'Read design', 'required', 'active', 'DDL', 'Explain schema', 12.5, 20.5, 1, '["postgres docs"]', 1800000000, 1800000100),
			('node-2', 'roadmap-1', 'node-1', 'task', 'Implement migration', 'Write code', 'required', 'todo', 'Command', 'Tests pass', 30.0, 40.0, 2, '["migration docs"]', 1800000000, 1800000100);

		INSERT INTO tasks (
			id, title, content, project, project_id, due, planned_date, priority,
			done, status, horizon, scope, sort_order, note_id, roadmap_node_id,
			created_at, updated_at
		)
		VALUES (
			'task-1', 'Migration Task', 'Task content 中文', 'Migration Project', 'project-1', 1800003600,
			'2026-06-16', 2, 1, 'active', 'long', 'monthly', 1.5,
			'note-1', 'node-1', 1800000000, 1800000100
		);

		INSERT INTO roadmap_edges (id, roadmap_id, source_node_id, target_node_id, style, created_at)
		VALUES ('edge-1', 'roadmap-1', 'node-1', 'node-2', 'solid', 1800000000);

		INSERT INTO roadmap_resources (id, node_id, title, url, summary, source_type, added_by, created_at, updated_at)
		VALUES ('resource-1', 'node-1', 'PostgreSQL Docs', 'https://www.postgresql.org/docs/', 'Official docs', 'article', 'user', 1800000000, 1800000100);

		INSERT INTO events (id, title, start_time, end_time, location, kind, note_id, created_at, updated_at)
		VALUES ('event-1', 'Migration Meeting', 1800000000, 1800007200, 'Online', 'work', 'note-1', 1800000000, 1800000100);

		INSERT INTO inbox (id, kind, title, body, source, archived, converted_to, created_at, updated_at)
		VALUES ('inbox-1', 'note', 'Inbox Item', 'Needs triage', 'quick-capture', 0, NULL, 1800000000, 1800000100);

		INSERT INTO sync_targets (id, type, name, vault_path, base_folder, config_json, enabled, auto_sync, is_default, created_at, updated_at)
		VALUES ('target-1', 'notion', 'Notion', '', '', '{"token_env":"FLOWSPACE_NOTION_TOKEN","required_tags":["sync"]}', 1, 0, 1, 1800000000, 1800000100);

		INSERT INTO note_sync_bindings (note_id, target_id, created_at, updated_at)
		VALUES ('note-1', 'target-1', 1800000000, 1800000100);

		INSERT INTO sync_external_claims (
			external_key, note_id, target_id, external_type, external_id, external_path, created_at, updated_at
		)
		VALUES (
			'notion:external-1', 'note-1', 'target-1', 'notion_page', 'external-1', '', 1800000000, 1800000100
		);

		INSERT INTO note_sync_suppressions (note_id, target_id, reason, created_at, updated_at)
		VALUES ('note-1', 'target-1', 'target_changed', 1800000000, 1800000100);

		INSERT INTO sync_import_tombstones (
			external_key, target_id, former_note_id, external_type, external_id, external_path, reason, created_at, updated_at
		)
		VALUES (
			'notion:deleted-page', 'target-1', 'note-1', 'notion_page', 'deleted-page', '', 'note_deleted', 1800000000, 1800000100
		);

		INSERT INTO note_sync_state (
			note_id, target_id, external_path, external_id, external_url, content_hash,
			external_hash, external_mtime, last_direction, last_synced_at, status, error_message
		)
		VALUES (
			'note-1', 'target-1', 'notion/note-1', 'external-1', 'https://notion.so/external-1',
			'hash-local', 'hash-external', 1800000200, 'push', 1800000300, 'synced', NULL
		);
	`); err != nil {
		t.Fatalf("seed sqlite: %v", err)
	}
	return sqlitePath
}

func createSQLiteWithSchema(t *testing.T) string {
	t.Helper()
	sqlitePath := filepath.Join(t.TempDir(), "flowspace.db")
	sqliteDB, err := sql.Open("sqlite", sqlitePath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqliteDB.Close()
	schemaSQL, err := os.ReadFile(filepath.Join("..", "..", "db", "schema.sql"))
	if err != nil {
		t.Fatalf("read sqlite schema: %v", err)
	}
	if _, err := sqliteDB.Exec(string(schemaSQL)); err != nil {
		t.Fatalf("create sqlite schema: %v", err)
	}
	return sqlitePath
}

func createMigrationPostgresSchema(t *testing.T, baseURL, schema string) string {
	t.Helper()
	root, err := sql.Open("pgx", baseURL)
	if err != nil {
		t.Fatalf("open postgres root: %v", err)
	}
	defer root.Close()
	if _, err := root.ExecContext(context.Background(), `CREATE SCHEMA `+pq.QuoteIdentifier(schema)); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = root.ExecContext(context.Background(), `DROP SCHEMA IF EXISTS `+pq.QuoteIdentifier(schema)+` CASCADE`)
	})
	u, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("parse postgres url: %v", err)
	}
	values := u.Query()
	values.Set("search_path", schema+",public")
	u.RawQuery = values.Encode()
	return u.String()
}

func assertTableCount(t *testing.T, db *sql.DB, table string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + pq.QuoteIdentifier(table)).Scan(&got); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if got != want {
		t.Fatalf("expected %s count %d, got %d", table, want, got)
	}
}

var _ = postgres.RunPostgresMigrationsContext
