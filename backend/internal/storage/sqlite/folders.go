package sqlite

import (
	"context"
	"strings"

	"github.com/hujinrun/flowspace/internal/model"
)

type folderRepository struct {
	db sqliteRunner
}

func (r folderRepository) List(ctx context.Context) ([]model.Folder, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT f.id, f.name, f.sort_order, COUNT(n.rowid) as note_count, f.created_at
		FROM folders f
		LEFT JOIN notes n ON n.folder_id = f.id
		GROUP BY f.id
		ORDER BY f.sort_order ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	folders := make([]model.Folder, 0)
	for rows.Next() {
		var folder model.Folder
		if err := rows.Scan(&folder.ID, &folder.Name, &folder.SortOrder, &folder.NoteCount, &folder.CreatedAt); err != nil {
			return nil, err
		}
		folders = append(folders, folder)
	}
	return folders, rows.Err()
}

func (r folderRepository) Exists(ctx context.Context, id string) (bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return false, nil
	}

	var exists int
	if err := r.db.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM folders WHERE id = ?)
	`, id).Scan(&exists); err != nil {
		return false, err
	}
	return exists == 1, nil
}
