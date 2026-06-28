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
	if err := ensureSQLiteWorkspaceColumns(ctx, db); err != nil {
		return err
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
			must_change_password INTEGER NOT NULL DEFAULT 1,
			default_workspace_id TEXT,
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
			role TEXT NOT NULL DEFAULT 'member' CHECK (role IN ('owner', 'member')),
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			PRIMARY KEY (workspace_id, user_id)
		)`,
		`CREATE INDEX IF NOT EXISTS workspace_members_user_idx
			ON workspace_members (user_id)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			token_hash TEXT NOT NULL UNIQUE,
			expires_at INTEGER NOT NULL,
			revoked_at INTEGER,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS sessions_active_idx
			ON sessions (user_id, expires_at DESC)
			WHERE revoked_at IS NULL`,
		`CREATE TABLE IF NOT EXISTS audit_events (
			id TEXT PRIMARY KEY,
			actor_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
			workspace_id TEXT REFERENCES workspaces(id) ON DELETE SET NULL,
			action TEXT NOT NULL,
			entity_type TEXT NOT NULL,
			entity_id TEXT NOT NULL DEFAULT '',
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

func ensureSQLiteWorkspaceColumns(ctx context.Context, db *sql.DB) error {
	for _, table := range sqliteWorkspaceScopedTables {
		exists, err := sqliteTableExists(db, table)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		if err := sqliteAddColumnIfMissing(db, table, "workspace_id", fmt.Sprintf(`ALTER TABLE %s ADD COLUMN workspace_id TEXT`, table)); err != nil {
			return fmt.Errorf("ensure SQLite %s.workspace_id: %w", table, err)
		}
	}
	return nil
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
