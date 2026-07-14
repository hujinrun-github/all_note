package sqlite

import (
	"context"
	"database/sql"
	"fmt"
)

func ensureSQLiteCalendarProjectSourcesSchema(ctx context.Context, db *sql.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS calendar_project_sources (
			workspace_id TEXT NOT NULL,
			user_id TEXT NOT NULL,
			project_id TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			color TEXT NOT NULL DEFAULT '',
			order_index INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			PRIMARY KEY (workspace_id, user_id, project_id),
			FOREIGN KEY (workspace_id, user_id)
				REFERENCES workspace_members(workspace_id, user_id)
				ON DELETE CASCADE
				DEFERRABLE INITIALLY DEFERRED,
			FOREIGN KEY (workspace_id, project_id)
				REFERENCES task_projects(workspace_id, id)
				ON DELETE CASCADE
				DEFERRABLE INITIALLY DEFERRED
		)`,
		`CREATE INDEX IF NOT EXISTS calendar_project_sources_user_enabled_idx
			ON calendar_project_sources (workspace_id, user_id, enabled, order_index)`,
		`CREATE INDEX IF NOT EXISTS calendar_project_sources_project_idx
			ON calendar_project_sources (workspace_id, project_id)`,
	}
	for _, stmt := range statements {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("ensure SQLite calendar project sources schema with %q: %w", stmt, err)
		}
	}
	return nil
}
