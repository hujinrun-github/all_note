package sqlite

import (
	"context"
	"database/sql"
	"fmt"
)

var sqliteWorkspaceScopedTables = []string{
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
	"note_project_links",
	"task_recurrence_rules",
	"task_occurrences",
	"note_sync_bindings",
	"sync_external_claims",
	"note_sync_suppressions",
	"sync_import_tombstones",
	"search_index",
}

func ensureSQLiteAuthSchema(ctx context.Context, db *sql.DB) error {
	if err := createSQLiteAuthTables(ctx, db); err != nil {
		return err
	}
	workspaceColumnsChanged, err := ensureSQLiteWorkspaceColumns(ctx, db)
	if err != nil {
		return err
	}
	defaultKeysChanged, err := ensureSQLiteWorkspaceScopedDefaultKeys(ctx, db)
	if err != nil {
		return err
	}
	if !workspaceColumnsChanged && !defaultKeysChanged {
		return nil
	}
	return rebuildSQLiteFTSAfterWorkspaceMigration(ctx, db)
}

func createSQLiteAuthTables(ctx context.Context, db *sql.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			email TEXT NOT NULL,
			display_name TEXT NOT NULL DEFAULT '',
			password_hash TEXT NOT NULL,
			must_change_password INTEGER NOT NULL DEFAULT 0,
			default_workspace_id TEXT,
			last_login_at INTEGER,
			password_changed_at INTEGER,
			role TEXT NOT NULL DEFAULT 'user' CHECK (role IN ('admin', 'user')),
			status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			FOREIGN KEY (id, default_workspace_id)
			  REFERENCES workspaces(owner_user_id, id)
			  DEFERRABLE INITIALLY DEFERRED
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS users_email_lower_idx
			ON users (lower(email))`,
		`CREATE TABLE IF NOT EXISTS workspaces (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			owner_user_id TEXT NOT NULL UNIQUE,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			UNIQUE (owner_user_id, id),
			FOREIGN KEY (owner_user_id) REFERENCES users(id) ON DELETE RESTRICT
		)`,
		`CREATE INDEX IF NOT EXISTS workspaces_owner_idx
			ON workspaces (owner_user_id)`,
		`CREATE TABLE IF NOT EXISTS workspace_members (
			workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
			user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			role TEXT NOT NULL DEFAULT 'owner' CHECK (role IN ('owner', 'member')),
			created_at INTEGER NOT NULL,
			PRIMARY KEY (workspace_id, user_id)
		)`,
		`CREATE INDEX IF NOT EXISTS workspace_members_user_idx
			ON workspace_members (user_id)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
			token_hash TEXT NOT NULL UNIQUE,
			user_agent TEXT NOT NULL DEFAULT '',
			ip_address TEXT NOT NULL DEFAULT '',
			expires_at INTEGER NOT NULL,
			last_seen_at INTEGER NOT NULL DEFAULT (unixepoch()),
			revoked_at INTEGER,
			created_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS sessions_active_idx
			ON sessions (user_id, expires_at DESC)
			WHERE revoked_at IS NULL`,
		`CREATE TABLE IF NOT EXISTS audit_events (
			id TEXT PRIMARY KEY,
			actor_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
			target_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
			workspace_id TEXT REFERENCES workspaces(id) ON DELETE SET NULL,
			action TEXT NOT NULL,
			metadata TEXT NOT NULL DEFAULT '{}',
			created_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS audit_events_actor_created_idx
			ON audit_events (actor_user_id, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS audit_events_created_idx
			ON audit_events (created_at DESC)`,
	}
	for _, stmt := range statements {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("ensure SQLite auth schema: %w", err)
		}
	}
	return nil
}

func ensureSQLiteWorkspaceColumns(ctx context.Context, db *sql.DB) (bool, error) {
	changed := false
	for _, table := range sqliteWorkspaceScopedTables {
		exists, err := sqliteTableExists(db, table)
		if err != nil {
			return false, err
		}
		if !exists {
			continue
		}
		hasColumn, err := sqliteColumnExists(db, table, "workspace_id")
		if err != nil {
			return false, err
		}
		if hasColumn {
			continue
		}
		if err := sqliteAddColumnIfMissing(db, table, "workspace_id", fmt.Sprintf(`ALTER TABLE %s ADD COLUMN workspace_id TEXT`, table)); err != nil {
			return false, fmt.Errorf("ensure SQLite %s.workspace_id: %w", table, err)
		}
		changed = true
	}
	return changed, nil
}

func rebuildSQLiteFTSAfterWorkspaceMigration(ctx context.Context, db *sql.DB) error {
	for _, table := range []string{"notes_fts", "tasks_fts", "events_fts"} {
		exists, err := sqliteTableExists(db, table)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		stmt := fmt.Sprintf("INSERT INTO %s(%s) VALUES('rebuild')", table, table)
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("rebuild SQLite FTS table %s: %w", table, err)
		}
	}
	return nil
}

func ensureSQLiteWorkspaceScopedDefaultKeys(ctx context.Context, db *sql.DB) (bool, error) {
	foldersReady, err := sqlitePrimaryKeyMatches(db, "folders", []string{"workspace_id", "id"})
	if err != nil {
		return false, err
	}
	projectsReady, err := sqlitePrimaryKeyMatches(db, "task_projects", []string{"workspace_id", "id"})
	if err != nil {
		return false, err
	}
	roadmapProjectReady, err := sqliteUniqueKeyMatches(db, "learning_roadmaps", []string{"workspace_id", "project_id"})
	if err != nil {
		return false, err
	}
	if foldersReady && projectsReady && roadmapProjectReady {
		if err := ensureSQLiteWorkspaceScopedDefaultAuxiliary(ctx, db); err != nil {
			return false, err
		}
		return false, nil
	}
	if err := rebuildSQLiteWorkspaceScopedDefaultTables(ctx, db); err != nil {
		return false, err
	}
	return true, nil
}

func rebuildSQLiteWorkspaceScopedDefaultTables(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		return fmt.Errorf("disable SQLite foreign keys for workspace default table rebuild: %w", err)
	}
	foreignKeysDisabled := true
	defer func() {
		if foreignKeysDisabled {
			_, _ = db.ExecContext(context.Background(), `PRAGMA foreign_keys=ON`)
		}
	}()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin SQLite workspace default table rebuild: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	statements := []string{
		`PRAGMA legacy_alter_table=ON`,
		`DROP TRIGGER IF EXISTS notes_ai`,
		`DROP TRIGGER IF EXISTS notes_ad`,
		`DROP TRIGGER IF EXISTS notes_au`,
		`DROP TRIGGER IF EXISTS tasks_ai`,
		`DROP TRIGGER IF EXISTS tasks_ad`,
		`DROP TRIGGER IF EXISTS tasks_au`,
		`DROP TRIGGER IF EXISTS tasks_project_delete_reassign`,
		`DROP INDEX IF EXISTS note_project_links_project_note_idx`,
		`DROP INDEX IF EXISTS folders_unscoped_id_idx`,
		`DROP INDEX IF EXISTS folders_unscoped_name_idx`,
		`DROP INDEX IF EXISTS task_projects_unscoped_id_idx`,
		`DROP INDEX IF EXISTS task_projects_unscoped_name_idx`,
		`DROP TABLE IF EXISTS folders_workspace_scope_old`,
		`DROP TABLE IF EXISTS task_projects_workspace_scope_old`,
		`DROP TABLE IF EXISTS notes_workspace_scope_old`,
		`DROP TABLE IF EXISTS tasks_workspace_scope_old`,
		`DROP TABLE IF EXISTS note_project_links_workspace_scope_old`,
		`DROP TABLE IF EXISTS learning_roadmaps_workspace_scope_old`,
		`ALTER TABLE folders RENAME TO folders_workspace_scope_old`,
		`ALTER TABLE task_projects RENAME TO task_projects_workspace_scope_old`,
		`ALTER TABLE notes RENAME TO notes_workspace_scope_old`,
		`ALTER TABLE tasks RENAME TO tasks_workspace_scope_old`,
		`ALTER TABLE note_project_links RENAME TO note_project_links_workspace_scope_old`,
		`ALTER TABLE learning_roadmaps RENAME TO learning_roadmaps_workspace_scope_old`,
		sqliteFoldersWorkspaceScopedDDL,
		sqliteTaskProjectsWorkspaceScopedDDL,
		sqliteNotesWorkspaceScopedDDL,
		sqliteTasksWorkspaceScopedDDL,
		sqliteNoteProjectLinksWorkspaceScopedDDL,
		sqliteLearningRoadmapsWorkspaceScopedDDL,
		`INSERT INTO folders (id, workspace_id, name, sort_order, created_at)
			SELECT id, COALESCE(workspace_id, ''), name, sort_order, created_at
			FROM folders_workspace_scope_old`,
		`INSERT INTO task_projects (id, workspace_id, name, type, description, created_at, updated_at)
			SELECT id, COALESCE(workspace_id, ''), name, type, description, created_at, updated_at
			FROM task_projects_workspace_scope_old`,
		`INSERT INTO notes (rowid, id, workspace_id, title, body, folder_id, tags, created_at, updated_at)
			SELECT rowid, id, COALESCE(workspace_id, ''), title, body, folder_id, tags, created_at, updated_at
			FROM notes_workspace_scope_old`,
		`INSERT INTO tasks (
				rowid, id, workspace_id, title, content, project, project_id, due, planned_date,
				priority, done, status, horizon, scope, sort_order, note_id, roadmap_node_id,
				execution_type, created_at, updated_at, completed_at
			)
			SELECT
				rowid, id, COALESCE(workspace_id, ''), title, content, project, project_id, due, planned_date,
				priority, done, status, horizon, scope, sort_order, note_id, roadmap_node_id,
				execution_type, created_at, updated_at, completed_at
			FROM tasks_workspace_scope_old`,
		`INSERT INTO note_project_links (note_id, project_id, workspace_id, created_at)
			SELECT note_id, project_id, COALESCE(workspace_id, ''), created_at
			FROM note_project_links_workspace_scope_old`,
		`INSERT INTO learning_roadmaps (id, project_id, workspace_id, title, goal, status, created_at, updated_at)
			SELECT id, project_id, COALESCE(workspace_id, ''), title, goal, status, created_at, updated_at
			FROM learning_roadmaps_workspace_scope_old`,
		`DROP TABLE folders_workspace_scope_old`,
		`DROP TABLE task_projects_workspace_scope_old`,
		`DROP TABLE notes_workspace_scope_old`,
		`DROP TABLE tasks_workspace_scope_old`,
		`DROP TABLE note_project_links_workspace_scope_old`,
		`DROP TABLE learning_roadmaps_workspace_scope_old`,
		`PRAGMA legacy_alter_table=OFF`,
	}
	for _, stmt := range statements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("rebuild SQLite workspace default tables with %q: %w", stmt, err)
		}
	}
	if err := createSQLiteWorkspaceScopedDefaultAuxiliary(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit SQLite workspace default table rebuild: %w", err)
	}
	committed = true

	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys=ON`); err != nil {
		return fmt.Errorf("reenable SQLite foreign keys after workspace default table rebuild: %w", err)
	}
	foreignKeysDisabled = false
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_key_check`); err != nil {
		return fmt.Errorf("check SQLite foreign keys after workspace default table rebuild: %w", err)
	}
	return nil
}

const sqliteFoldersWorkspaceScopedDDL = `
CREATE TABLE folders (
	id TEXT NOT NULL,
	workspace_id TEXT NOT NULL DEFAULT '',
	name TEXT NOT NULL,
	sort_order REAL NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL,
	PRIMARY KEY (workspace_id, id),
	UNIQUE (workspace_id, name)
)`

const sqliteTaskProjectsWorkspaceScopedDDL = `
CREATE TABLE task_projects (
	id TEXT NOT NULL,
	workspace_id TEXT NOT NULL DEFAULT '',
	name TEXT NOT NULL,
	type TEXT NOT NULL DEFAULT 'regular',
	description TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	PRIMARY KEY (workspace_id, id),
	UNIQUE (workspace_id, name)
)`

const sqliteNotesWorkspaceScopedDDL = `
CREATE TABLE notes (
	rowid INTEGER PRIMARY KEY AUTOINCREMENT,
	id TEXT UNIQUE NOT NULL,
	workspace_id TEXT NOT NULL DEFAULT '',
	title TEXT NOT NULL,
	body TEXT NOT NULL DEFAULT '',
	folder_id TEXT NOT NULL DEFAULT '__uncategorized',
	tags TEXT NOT NULL DEFAULT '[]',
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	FOREIGN KEY (workspace_id, folder_id)
		REFERENCES folders(workspace_id, id)
		DEFERRABLE INITIALLY DEFERRED
)`

const sqliteTasksWorkspaceScopedDDL = `
CREATE TABLE tasks (
	rowid INTEGER PRIMARY KEY AUTOINCREMENT,
	id TEXT UNIQUE NOT NULL,
	workspace_id TEXT NOT NULL DEFAULT '',
	title TEXT NOT NULL,
	content TEXT NOT NULL DEFAULT '',
	project TEXT,
	project_id TEXT NOT NULL DEFAULT 'personal',
	due INTEGER,
	planned_date TEXT,
	priority INTEGER NOT NULL DEFAULT 0,
	done INTEGER NOT NULL DEFAULT 0,
	status TEXT NOT NULL DEFAULT 'open',
	horizon TEXT NOT NULL DEFAULT 'week',
	scope TEXT NOT NULL DEFAULT 'daily',
	sort_order REAL NOT NULL DEFAULT 0,
	note_id TEXT REFERENCES notes(id) ON DELETE SET NULL,
	roadmap_node_id TEXT REFERENCES roadmap_nodes(id) ON DELETE SET NULL,
	execution_type TEXT NOT NULL DEFAULT 'single',
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	completed_at INTEGER,
	FOREIGN KEY (workspace_id, project_id)
		REFERENCES task_projects(workspace_id, id)
		ON DELETE RESTRICT
		DEFERRABLE INITIALLY DEFERRED
)`

const sqliteNoteProjectLinksWorkspaceScopedDDL = `
CREATE TABLE note_project_links (
	note_id TEXT NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
	project_id TEXT NOT NULL,
	workspace_id TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	PRIMARY KEY (note_id, project_id),
	FOREIGN KEY (workspace_id, project_id)
		REFERENCES task_projects(workspace_id, id)
		ON DELETE CASCADE
		DEFERRABLE INITIALLY DEFERRED
)`

const sqliteLearningRoadmapsWorkspaceScopedDDL = `
CREATE TABLE learning_roadmaps (
	id TEXT PRIMARY KEY,
	project_id TEXT NOT NULL,
	workspace_id TEXT NOT NULL DEFAULT '',
	title TEXT NOT NULL,
	goal TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'draft',
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	UNIQUE (workspace_id, project_id),
	FOREIGN KEY (workspace_id, project_id)
		REFERENCES task_projects(workspace_id, id)
		ON DELETE CASCADE
		DEFERRABLE INITIALLY DEFERRED
)`

func ensureSQLiteWorkspaceScopedDefaultAuxiliary(ctx context.Context, db *sql.DB) error {
	return createSQLiteWorkspaceScopedDefaultAuxiliary(ctx, db)
}

func createSQLiteWorkspaceScopedDefaultAuxiliary(ctx context.Context, runner interface {
	ExecContext(context.Context, string, ...interface{}) (sql.Result, error)
}) error {
	statements := []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS folders_unscoped_id_idx
			ON folders (id)
			WHERE workspace_id IS NULL OR workspace_id = ''`,
		`CREATE UNIQUE INDEX IF NOT EXISTS folders_unscoped_name_idx
			ON folders (name)
			WHERE workspace_id IS NULL OR workspace_id = ''`,
		`CREATE UNIQUE INDEX IF NOT EXISTS task_projects_unscoped_id_idx
			ON task_projects (id)
			WHERE workspace_id IS NULL OR workspace_id = ''`,
		`CREATE UNIQUE INDEX IF NOT EXISTS task_projects_unscoped_name_idx
			ON task_projects (name)
			WHERE workspace_id IS NULL OR workspace_id = ''`,
		`CREATE INDEX IF NOT EXISTS note_project_links_project_note_idx
			ON note_project_links (project_id, note_id)`,
		`CREATE TRIGGER IF NOT EXISTS notes_ai AFTER INSERT ON notes BEGIN
			INSERT INTO notes_fts(rowid, title, body, tags) VALUES (new.rowid, new.title, new.body, new.tags);
		END`,
		`CREATE TRIGGER IF NOT EXISTS notes_ad AFTER DELETE ON notes BEGIN
			INSERT INTO notes_fts(notes_fts, rowid, title, body, tags) VALUES ('delete', old.rowid, old.title, old.body, old.tags);
		END`,
		`CREATE TRIGGER IF NOT EXISTS notes_au AFTER UPDATE ON notes BEGIN
			INSERT INTO notes_fts(notes_fts, rowid, title, body, tags) VALUES ('delete', old.rowid, old.title, old.body, old.tags);
			INSERT INTO notes_fts(rowid, title, body, tags) VALUES (new.rowid, new.title, new.body, new.tags);
		END`,
		`CREATE TRIGGER IF NOT EXISTS tasks_ai AFTER INSERT ON tasks BEGIN
			INSERT INTO tasks_fts(rowid, title) VALUES (new.rowid, new.title);
		END`,
		`CREATE TRIGGER IF NOT EXISTS tasks_ad AFTER DELETE ON tasks BEGIN
			INSERT INTO tasks_fts(tasks_fts, rowid, title) VALUES ('delete', old.rowid, old.title);
		END`,
		`CREATE TRIGGER IF NOT EXISTS tasks_au AFTER UPDATE ON tasks BEGIN
			INSERT INTO tasks_fts(tasks_fts, rowid, title) VALUES ('delete', old.rowid, old.title);
			INSERT INTO tasks_fts(rowid, title) VALUES (new.rowid, new.title);
		END`,
		`CREATE TRIGGER IF NOT EXISTS tasks_project_delete_reassign
			BEFORE DELETE ON task_projects
			WHEN old.id <> 'personal'
			BEGIN
				UPDATE tasks
				SET project_id = 'personal'
				WHERE project_id = old.id
					AND workspace_id IS old.workspace_id;
			END`,
		`DELETE FROM folders
			WHERE (workspace_id IS NULL OR workspace_id = '')
				AND id IN ('__uncategorized', '__work', '__personal')
				AND EXISTS (
					SELECT 1
					FROM folders scoped
					WHERE scoped.id = folders.id
						AND scoped.workspace_id IS NOT NULL
						AND scoped.workspace_id <> ''
				)`,
		`DELETE FROM task_projects
			WHERE (workspace_id IS NULL OR workspace_id = '')
				AND id = 'personal'
				AND EXISTS (
					SELECT 1
					FROM task_projects scoped
					WHERE scoped.id = task_projects.id
						AND scoped.workspace_id IS NOT NULL
						AND scoped.workspace_id <> ''
				)`,
	}
	for _, stmt := range statements {
		if _, err := runner.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("ensure SQLite workspace scoped default auxiliary with %q: %w", stmt, err)
		}
	}
	return nil
}

func sqlitePrimaryKeyMatches(db *sql.DB, table string, want []string) (bool, error) {
	exists, err := sqliteTableExists(db, table)
	if err != nil {
		return false, err
	}
	if !exists {
		return true, nil
	}
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false, fmt.Errorf("inspect SQLite primary key for %s: %w", table, err)
	}
	defer rows.Close()

	columnsByOrdinal := map[int]string{}
	for rows.Next() {
		var cid int
		var name string
		var columnType string
		var notNull int
		var defaultValue any
		var primaryKey int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, err
		}
		if primaryKey > 0 {
			columnsByOrdinal[primaryKey] = name
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	if len(columnsByOrdinal) != len(want) {
		return false, nil
	}
	for i, column := range want {
		if columnsByOrdinal[i+1] != column {
			return false, nil
		}
	}
	return true, nil
}

func sqliteUniqueKeyMatches(db *sql.DB, table string, want []string) (bool, error) {
	exists, err := sqliteTableExists(db, table)
	if err != nil {
		return false, err
	}
	if !exists {
		return true, nil
	}
	rows, err := db.Query(`PRAGMA index_list(` + table + `)`)
	if err != nil {
		return false, fmt.Errorf("inspect SQLite indexes for %s: %w", table, err)
	}

	var uniqueIndexes []string
	for rows.Next() {
		var seq int
		var name string
		var unique int
		var origin string
		var partial int
		if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			_ = rows.Close()
			return false, err
		}
		if unique == 0 {
			continue
		}
		uniqueIndexes = append(uniqueIndexes, name)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return false, err
	}
	if err := rows.Close(); err != nil {
		return false, err
	}

	for _, name := range uniqueIndexes {
		matches, err := sqliteIndexColumnsMatch(db, name, want)
		if err != nil {
			return false, err
		}
		if matches {
			return true, nil
		}
	}
	return false, nil
}

func sqliteIndexColumnsMatch(db *sql.DB, indexName string, want []string) (bool, error) {
	rows, err := db.Query(`PRAGMA index_info(` + indexName + `)`)
	if err != nil {
		return false, fmt.Errorf("inspect SQLite index columns for %s: %w", indexName, err)
	}
	defer rows.Close()

	var columns []string
	for rows.Next() {
		var seqno int
		var cid int
		var name string
		if err := rows.Scan(&seqno, &cid, &name); err != nil {
			return false, err
		}
		columns = append(columns, name)
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	if len(columns) != len(want) {
		return false, nil
	}
	for i := range want {
		if columns[i] != want[i] {
			return false, nil
		}
	}
	return true, nil
}
