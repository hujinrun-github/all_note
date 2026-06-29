package bootstrap

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/storage/sqlite"
)

func TestBootstrapEmptyDBWithoutConfigNoop(t *testing.T) {
	fixture := openSQLiteBootstrapFixture(t)

	if err := EnsureAuthReady(context.Background(), fixture.store, Config{}); err != nil {
		t.Fatalf("bootstrap empty db without config: %v", err)
	}

	assertRowCount(t, fixture.db, `SELECT COUNT(*) FROM users`, 0)
	assertRowCount(t, fixture.db, `SELECT COUNT(*) FROM workspaces`, 0)
}

func TestBootstrapEmptyDBWithWeakConfiguredPasswordReturnsPasswordPolicyError(t *testing.T) {
	fixture := openSQLiteBootstrapFixture(t)
	cfg := weakBootstrapConfig()

	err := EnsureAuthReady(context.Background(), fixture.store, cfg)
	if !errors.Is(err, auth.ErrWeakPassword) {
		t.Fatalf("bootstrap error = %v, want ErrWeakPassword", err)
	}

	assertRowCount(t, fixture.db, `SELECT COUNT(*) FROM users`, 0)
	assertRowCount(t, fixture.db, `SELECT COUNT(*) FROM workspaces`, 0)
}

func TestBootstrapPartialConfigReturnsIncompleteConfigError(t *testing.T) {
	fixture := openSQLiteBootstrapFixture(t)
	cfg := Config{AdminEmail: "admin@example.com"}

	err := EnsureAuthReady(context.Background(), fixture.store, cfg)
	if !errors.Is(err, ErrBootstrapAdminIncomplete) {
		t.Fatalf("bootstrap error = %v, want ErrBootstrapAdminIncomplete", err)
	}

	assertRowCount(t, fixture.db, `SELECT COUNT(*) FROM users`, 0)
	assertRowCount(t, fixture.db, `SELECT COUNT(*) FROM workspaces`, 0)
}

func TestBootstrapExistingUserNoop(t *testing.T) {
	fixture := openSQLiteBootstrapFixture(t)
	seedExistingUserWorkspace(t, fixture.store)

	cfg := Config{
		AdminEmail:    "admin@example.com",
		AdminPassword: "abc12345",
		AdminName:     "Admin",
	}
	if err := EnsureAuthReady(context.Background(), fixture.store, cfg); err != nil {
		t.Fatalf("bootstrap with existing user: %v", err)
	}

	assertRowCount(t, fixture.db, `SELECT COUNT(*) FROM users`, 1)
	assertRowCount(t, fixture.db, `SELECT COUNT(*) FROM users WHERE lower(email) = lower(?)`, 0, cfg.AdminEmail)
}

func TestBootstrapLegacyDataWithWeakConfiguredPasswordReturnsPasswordPolicyError(t *testing.T) {
	fixture := openSQLiteBootstrapFixture(t)
	seedLegacyNote(t, fixture.store)
	cfg := weakBootstrapConfig()

	err := EnsureAuthReady(context.Background(), fixture.store, cfg)
	if !errors.Is(err, auth.ErrWeakPassword) {
		t.Fatalf("bootstrap error = %v, want ErrWeakPassword", err)
	}
	if errors.Is(err, ErrBootstrapAdminRequired) {
		t.Fatalf("bootstrap error = %v, did not want ErrBootstrapAdminRequired", err)
	}

	assertRowCount(t, fixture.db, `SELECT COUNT(*) FROM users`, 0)
	assertRowCount(t, fixture.db, `SELECT COUNT(*) FROM workspaces`, 0)
}

func TestBootstrapRequiresAdminConfigForLegacyData(t *testing.T) {
	fixture := openSQLiteBootstrapFixture(t)
	seedLegacyNote(t, fixture.store)

	err := EnsureAuthReady(context.Background(), fixture.store, Config{})
	if !errors.Is(err, ErrBootstrapAdminRequired) {
		t.Fatalf("bootstrap error = %v, want ErrBootstrapAdminRequired", err)
	}
}

func TestBootstrapAssignsLegacyDataBeforeDefaultRows(t *testing.T) {
	fixture := openSQLiteBootstrapFixture(t)
	seedLegacyWorkspaceScopedRows(t, fixture.db)
	cfg := validBootstrapConfig()

	if err := EnsureAuthReady(context.Background(), fixture.store, cfg); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	assertNoDuplicateDefaultFolderIDs(t, fixture.db)
	assertAllWorkspaceScopedRowsHaveWorkspace(t, fixture.db)
	assertDefaultWorkspaceOwnedByAdmin(t, fixture.db, cfg)
}

func TestBootstrapCreatesMissingDefaultWorkspaceData(t *testing.T) {
	fixture := openSQLiteBootstrapFixture(t)
	mustExec(t, fixture.db, `DELETE FROM folders WHERE id = '__work'`)
	seedLegacyNote(t, fixture.store)
	cfg := validBootstrapConfig()

	if err := EnsureAuthReady(context.Background(), fixture.store, cfg); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	assertRowCount(t, fixture.db, `SELECT COUNT(*) FROM folders WHERE id = '__work'`, 1)
	assertRowCount(t, fixture.db, `SELECT COUNT(*) FROM task_projects WHERE id = 'personal'`, 1)
	assertAllWorkspaceScopedRowsHaveWorkspace(t, fixture.db)
}

type sqliteBootstrapFixture struct {
	store storage.Store
	db    *sql.DB
}

func openSQLiteBootstrapFixture(t *testing.T) sqliteBootstrapFixture {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "flowspace.bootstrap.db")
	store, err := (sqlite.Provider{}).Open(context.Background(), storage.Config{
		Env:        "test",
		Driver:     storage.DriverSQLite,
		SQLitePath: dbPath,
	})
	if err != nil {
		t.Fatalf("open sqlite provider: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("close sqlite store: %v", err)
		}
	})

	db, err := sql.Open("sqlite", dbPath+"?_foreign_keys=ON")
	if err != nil {
		t.Fatalf("open sqlite assertions connection: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close sqlite assertions connection: %v", err)
		}
	})
	return sqliteBootstrapFixture{store: store, db: db}
}

func validBootstrapConfig() Config {
	return Config{
		AdminEmail:    "admin@example.com",
		AdminPassword: "abc12345",
		AdminName:     "Admin",
	}
}

func weakBootstrapConfig() Config {
	return Config{
		AdminEmail:    "admin@example.com",
		AdminPassword: "weak",
		AdminName:     "Admin",
	}
}

func seedExistingUserWorkspace(t *testing.T, store storage.Store) {
	t.Helper()

	ctx := context.Background()
	user := &model.User{
		ID:                 "user_existing",
		Email:              "existing@example.com",
		DisplayName:        "Existing",
		PasswordHash:       "existing-hash",
		MustChangePassword: false,
		Role:               "admin",
		Status:             "active",
	}
	if err := store.Auth().CreateUser(ctx, user); err != nil {
		t.Fatalf("seed existing user: %v", err)
	}
	workspace := &model.Workspace{
		ID:          "workspace_existing",
		Name:        "Existing Workspace",
		OwnerUserID: user.ID,
	}
	if err := store.Auth().CreateWorkspace(ctx, workspace); err != nil {
		t.Fatalf("seed existing workspace: %v", err)
	}
	if err := store.Auth().SetDefaultWorkspace(ctx, user.ID, workspace.ID); err != nil {
		t.Fatalf("seed existing default workspace: %v", err)
	}
	if err := store.Auth().AddWorkspaceMember(ctx, workspace.ID, user.ID, "owner"); err != nil {
		t.Fatalf("seed existing membership: %v", err)
	}
}

func seedLegacyNote(t *testing.T, store storage.Store) {
	t.Helper()

	if _, err := store.Notes().Create(context.Background(), &model.CreateNoteRequest{
		Title:    "Legacy note",
		Body:     "legacy body",
		FolderID: "__uncategorized",
		Tags:     `[]`,
	}); err != nil {
		t.Fatalf("seed legacy note: %v", err)
	}
}

func seedLegacyWorkspaceScopedRows(t *testing.T, db *sql.DB) {
	t.Helper()

	mustExec(t, db, `INSERT INTO folders (id, name, sort_order, created_at) VALUES ('legacy_folder', 'Legacy Folder', 10, unixepoch())`)
	mustExec(t, db, `INSERT INTO task_projects (id, name, type, description, created_at, updated_at) VALUES ('legacy_project', 'Legacy Project', 'regular', '', unixepoch(), unixepoch())`)
	mustExec(t, db, `INSERT INTO task_projects (id, name, type, description, created_at, updated_at) VALUES ('legacy_learning_project', 'Legacy Learning Project', 'learning', '', unixepoch(), unixepoch())`)
	mustExec(t, db, `INSERT INTO notes (id, title, body, folder_id, tags, created_at, updated_at) VALUES ('legacy_note', 'Legacy note', 'body', 'legacy_folder', '[]', unixepoch(), unixepoch())`)
	mustExec(t, db, `INSERT INTO note_project_links (note_id, project_id, created_at) VALUES ('legacy_note', 'legacy_project', unixepoch())`)
	mustExec(t, db, `INSERT INTO tasks (id, title, content, project_id, created_at, updated_at) VALUES ('legacy_task', 'Legacy task', '', 'legacy_project', unixepoch(), unixepoch())`)
	mustExec(t, db, `INSERT INTO task_recurrence_rules (task_id, start_date, frequency, interval, weekdays, month_days, timezone, created_at, updated_at) VALUES ('legacy_task', '2026-06-29', 'weekly', 1, '[]', '[]', 'Asia/Shanghai', unixepoch(), unixepoch())`)
	mustExec(t, db, `INSERT INTO task_occurrences (task_id, occurrence_date, created_at, updated_at) VALUES ('legacy_task', '2026-06-29', unixepoch(), unixepoch())`)
	mustExec(t, db, `INSERT INTO learning_roadmaps (id, project_id, title, goal, created_at, updated_at) VALUES ('legacy_roadmap', 'legacy_learning_project', 'Legacy Roadmap', 'learn', unixepoch(), unixepoch())`)
	mustExec(t, db, `INSERT INTO roadmap_nodes (id, roadmap_id, title, created_at, updated_at) VALUES ('legacy_node_1', 'legacy_roadmap', 'Node 1', unixepoch(), unixepoch())`)
	mustExec(t, db, `INSERT INTO roadmap_nodes (id, roadmap_id, title, created_at, updated_at) VALUES ('legacy_node_2', 'legacy_roadmap', 'Node 2', unixepoch(), unixepoch())`)
	mustExec(t, db, `INSERT INTO roadmap_edges (id, roadmap_id, source_node_id, target_node_id, created_at) VALUES ('legacy_edge', 'legacy_roadmap', 'legacy_node_1', 'legacy_node_2', unixepoch())`)
	mustExec(t, db, `INSERT INTO roadmap_resources (id, node_id, title, url, created_at, updated_at) VALUES ('legacy_resource', 'legacy_node_1', 'Resource', 'https://example.test', unixepoch(), unixepoch())`)
	mustExec(t, db, `INSERT INTO events (id, title, start_time, end_time, kind, note_id, created_at, updated_at) VALUES ('legacy_event', 'Legacy event', unixepoch(), unixepoch() + 3600, 'work', 'legacy_note', unixepoch(), unixepoch())`)
	mustExec(t, db, `INSERT INTO inbox (id, kind, title, body, created_at, updated_at) VALUES ('legacy_inbox', 'note', 'Legacy inbox', 'body', unixepoch(), unixepoch())`)
	mustExec(t, db, `INSERT INTO sync_targets (id, type, name, vault_path, base_folder, created_at, updated_at) VALUES ('legacy_target', 'obsidian', 'Legacy Target', '', '', unixepoch(), unixepoch())`)
	mustExec(t, db, `INSERT INTO note_sync_bindings (note_id, target_id, created_at, updated_at) VALUES ('legacy_note', 'legacy_target', unixepoch(), unixepoch())`)
	mustExec(t, db, `INSERT INTO sync_external_claims (external_key, note_id, target_id, external_type, created_at, updated_at) VALUES ('legacy_external', 'legacy_note', 'legacy_target', 'obsidian_file', unixepoch(), unixepoch())`)
	mustExec(t, db, `INSERT INTO note_sync_suppressions (note_id, target_id, created_at, updated_at) VALUES ('legacy_note', 'legacy_target', unixepoch(), unixepoch())`)
	mustExec(t, db, `INSERT INTO sync_import_tombstones (external_key, target_id, former_note_id, external_type, created_at, updated_at) VALUES ('legacy_tombstone', 'legacy_target', 'legacy_note', 'obsidian_file', unixepoch(), unixepoch())`)
	mustExec(t, db, `INSERT INTO note_sync_state (note_id, target_id, external_path, content_hash, status) VALUES ('legacy_note', 'legacy_target', 'legacy.md', 'hash', 'pending')`)
	mustExec(t, db, `CREATE TABLE search_index (
		entity_type TEXT NOT NULL,
		entity_id TEXT NOT NULL,
		title TEXT NOT NULL,
		content TEXT NOT NULL DEFAULT '',
		updated_at INTEGER NOT NULL,
		workspace_id TEXT,
		PRIMARY KEY (entity_type, entity_id)
	)`)
	mustExec(t, db, `INSERT INTO search_index (entity_type, entity_id, title, content, updated_at) VALUES ('note', 'legacy_note', 'Legacy note', 'body', unixepoch())`)
}

func assertNoDuplicateDefaultFolderIDs(t *testing.T, db *sql.DB) {
	t.Helper()

	for _, id := range []string{"__uncategorized", "__work", "__personal"} {
		assertRowCount(t, db, `SELECT COUNT(*) FROM folders WHERE id = ?`, 1, id)
	}
}

func assertAllWorkspaceScopedRowsHaveWorkspace(t *testing.T, db *sql.DB) {
	t.Helper()

	for _, table := range workspaceScopedTableAssertions() {
		if !sqliteTableHasColumn(t, db, table, "workspace_id") {
			continue
		}
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table + ` WHERE workspace_id IS NULL OR workspace_id = ''`).Scan(&count); err != nil {
			t.Fatalf("count missing workspace_id in %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s missing workspace_id count = %d, want 0", table, count)
		}
	}
}

func assertDefaultWorkspaceOwnedByAdmin(t *testing.T, db *sql.DB, cfg Config) {
	t.Helper()

	var userID, defaultWorkspaceID, passwordHash string
	if err := db.QueryRow(`
		SELECT id, default_workspace_id, password_hash
		FROM users
		WHERE lower(email) = lower(?)
	`, cfg.AdminEmail).Scan(&userID, &defaultWorkspaceID, &passwordHash); err != nil {
		t.Fatalf("load bootstrap admin: %v", err)
	}
	if defaultWorkspaceID == "" {
		t.Fatal("bootstrap admin default workspace is empty")
	}
	if passwordHash == cfg.AdminPassword || !strings.HasPrefix(passwordHash, "$2") {
		t.Fatalf("bootstrap admin password was not stored as bcrypt hash: %q", passwordHash)
	}
	if err := auth.VerifyPassword(passwordHash, cfg.AdminPassword); err != nil {
		t.Fatalf("bootstrap admin password hash does not verify: %v", err)
	}

	var ownerUserID string
	if err := db.QueryRow(`SELECT owner_user_id FROM workspaces WHERE id = ?`, defaultWorkspaceID).Scan(&ownerUserID); err != nil {
		t.Fatalf("load default workspace: %v", err)
	}
	if ownerUserID != userID {
		t.Fatalf("default workspace owner = %q, want %q", ownerUserID, userID)
	}

	var role string
	if err := db.QueryRow(`
		SELECT role
		FROM workspace_members
		WHERE workspace_id = ? AND user_id = ?
	`, defaultWorkspaceID, userID).Scan(&role); err != nil {
		t.Fatalf("load admin membership: %v", err)
	}
	if role != "owner" {
		t.Fatalf("bootstrap admin membership role = %q, want owner", role)
	}
}

func workspaceScopedTableAssertions() []string {
	return []string{
		"folders",
		"notes",
		"task_projects",
		"tasks",
		"events",
		"inbox",
		"learning_roadmaps",
		"roadmap_nodes",
		"roadmap_edges",
		"roadmap_resources",
		"sync_targets",
		"note_sync_state",
		"note_project_links",
		"task_recurrence_rules",
		"task_occurrences",
		"note_sync_bindings",
		"sync_external_claims",
		"note_sync_suppressions",
		"sync_import_tombstones",
		"search_index",
	}
}

func sqliteTableHasColumn(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()

	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		t.Fatalf("inspect %s columns: %v", table, err)
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name string
		var columnType string
		var notNull int
		var defaultValue any
		var primaryKey int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatalf("scan %s columns: %v", table, err)
		}
		if name == column {
			return true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate %s columns: %v", table, err)
	}
	return false
}

func assertRowCount(t *testing.T, db *sql.DB, query string, want int, args ...any) {
	t.Helper()

	var got int
	if err := db.QueryRow(query, args...).Scan(&got); err != nil {
		t.Fatalf("count rows with %q: %v", query, err)
	}
	if got != want {
		t.Fatalf("row count for %q = %d, want %d", query, got, want)
	}
}

func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()

	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}
