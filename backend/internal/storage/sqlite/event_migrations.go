package sqlite

import (
	"context"
	"database/sql"
	"fmt"
)

func ensureSQLiteEventProjectSchema(ctx context.Context, db *sql.DB) error {
	exists, err := sqliteTableExists(db, "events")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if err := sqliteAddColumnIfMissing(db, "events", "project_id", `ALTER TABLE events ADD COLUMN project_id TEXT`); err != nil {
		return fmt.Errorf("ensure SQLite events.project_id: %w", err)
	}
	hasWorkspaceID, err := sqliteColumnExists(db, "events", "workspace_id")
	if err != nil {
		return err
	}
	hasProjectID, err := sqliteColumnExists(db, "events", "project_id")
	if err != nil {
		return err
	}
	if !hasWorkspaceID || !hasProjectID {
		return nil
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE events
		SET project_id = 'personal'
		WHERE (project_id IS NULL OR project_id = '')
			AND kind = 'personal'
			AND EXISTS (
				SELECT 1
				FROM task_projects p
				WHERE p.workspace_id = events.workspace_id
					AND p.id = 'personal'
			)
	`); err != nil {
		return fmt.Errorf("backfill SQLite personal events project_id: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS events_workspace_project_start_idx
			ON events (workspace_id, project_id, start_time)
	`); err != nil {
		return fmt.Errorf("ensure SQLite events project index: %w", err)
	}
	return nil
}
