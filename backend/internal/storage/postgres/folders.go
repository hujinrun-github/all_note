package postgres

import (
	"context"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/model"
)

type folderRepository struct {
	db postgresRunner
}

func (r folderRepository) List(ctx context.Context) ([]model.Folder, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT f.id, f.name, f.sort_order, COUNT(n.id) as note_count, f.created_at
		FROM folders f
		LEFT JOIN notes n ON n.workspace_id = f.workspace_id AND n.folder_id = f.id
		WHERE f.workspace_id = $1
		GROUP BY f.workspace_id, f.id
		ORDER BY f.sort_order ASC
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	folders := make([]model.Folder, 0)
	for rows.Next() {
		var folder model.Folder
		var createdAt time.Time
		if err := rows.Scan(&folder.ID, &folder.Name, &folder.SortOrder, &folder.NoteCount, &createdAt); err != nil {
			return nil, err
		}
		folder.CreatedAt = timeToUnix(createdAt)
		folders = append(folders, folder)
	}
	return folders, rows.Err()
}

func (r folderRepository) Exists(ctx context.Context, id string) (bool, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return false, err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return false, nil
	}

	var exists bool
	if err := r.db.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM folders WHERE workspace_id = $1 AND id = $2)
	`, workspaceID, id).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}
