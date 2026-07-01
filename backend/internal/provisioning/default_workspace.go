package provisioning

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/storage"
)

// SQLRunner is the narrow storage capability needed to provision scoped defaults
// inside the caller's transaction.
type SQLRunner interface {
	ExecContext(context.Context, string, ...interface{}) (sql.Result, error)
}

type defaultFolder struct {
	ID        string
	Name      string
	SortOrder float64
}

var defaultFolders = []defaultFolder{
	{ID: "__uncategorized", Name: "Uncategorized", SortOrder: 0},
	{ID: "__work", Name: "Work", SortOrder: 1},
	{ID: "__personal", Name: "Personal", SortOrder: 2},
}

// EnsureDefaultWorkspaceData inserts the default folders and personal task
// project for the workspace scope in ctx. It uses workspace-scoped
// (workspace_id, id) conflicts and must be called inside the provisioning
// transaction for the target workspace.
func EnsureDefaultWorkspaceData(ctx context.Context, store storage.Store) error {
	runner, ok := store.(SQLRunner)
	if !ok {
		return fmt.Errorf("storage store %T does not expose SQL runner", store)
	}
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	if store.Capabilities().TimeRanges {
		return ensurePostgresDefaults(ctx, runner, workspaceID)
	}
	return ensureSQLiteDefaults(ctx, runner, workspaceID)
}

func ensurePostgresDefaults(ctx context.Context, runner SQLRunner, workspaceID string) error {
	for _, folder := range defaultFolders {
		if _, err := runner.ExecContext(ctx, `
			UPDATE folders
			SET workspace_id = $1
			WHERE id = $2 AND (workspace_id IS NULL OR workspace_id = '')
		`, workspaceID, folder.ID); err != nil {
			return fmt.Errorf("scope legacy default folder %s: %w", folder.ID, err)
		}
		if _, err := runner.ExecContext(ctx, `
			INSERT INTO folders (id, name, sort_order, created_at, workspace_id)
			VALUES ($1, $2, $3, now(), $4)
			ON CONFLICT DO NOTHING
		`, folder.ID, folder.Name, folder.SortOrder, workspaceID); err != nil {
			return fmt.Errorf("provision default folder %s: %w", folder.ID, err)
		}
	}
	if _, err := runner.ExecContext(ctx, `
		UPDATE task_projects
		SET workspace_id = $1
		WHERE id = 'personal' AND (workspace_id IS NULL OR workspace_id = '')
	`, workspaceID); err != nil {
		return fmt.Errorf("scope legacy default task project: %w", err)
	}
	if _, err := runner.ExecContext(ctx, `
		INSERT INTO task_projects (id, name, type, description, created_at, updated_at, workspace_id)
		VALUES ($1, $2, $3, $4, now(), now(), $5)
		ON CONFLICT DO NOTHING
	`, "personal", "Personal", "personal", "Default personal task project", workspaceID); err != nil {
		return fmt.Errorf("provision default task project: %w", err)
	}
	return nil
}

func ensureSQLiteDefaults(ctx context.Context, runner SQLRunner, workspaceID string) error {
	for _, folder := range defaultFolders {
		if _, err := runner.ExecContext(ctx, `
			INSERT INTO folders (id, name, sort_order, created_at, workspace_id)
			VALUES (?, ?, ?, unixepoch(), ?)
			ON CONFLICT (workspace_id, id) DO NOTHING
		`, folder.ID, folder.Name, folder.SortOrder, workspaceID); err != nil {
			return fmt.Errorf("provision default folder %s: %w", folder.ID, err)
		}
	}
	if _, err := runner.ExecContext(ctx, `
		INSERT INTO task_projects (id, name, type, description, created_at, updated_at, workspace_id)
		VALUES (?, ?, ?, ?, unixepoch(), unixepoch(), ?)
		ON CONFLICT (workspace_id, id) DO NOTHING
	`, "personal", "Personal", "personal", "Default personal task project", workspaceID); err != nil {
		return fmt.Errorf("provision default task project: %w", err)
	}
	return nil
}
