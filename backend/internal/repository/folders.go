package repository

import (
	"context"
	"strings"

	"github.com/hujinrun/flowspace/internal/model"
)

func GetFolders() ([]model.Folder, error) {
	if store := CurrentStore(); store != nil {
		return store.Folders().List(context.Background())
	}

	rows, err := DB.Query(`
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
		var f model.Folder
		if err := rows.Scan(&f.ID, &f.Name, &f.SortOrder, &f.NoteCount, &f.CreatedAt); err != nil {
			return nil, err
		}
		folders = append(folders, f)
	}
	return folders, nil
}

func FolderExists(id string) (bool, error) {
	if store := CurrentStore(); store != nil {
		return store.Folders().Exists(context.Background(), id)
	}

	id = strings.TrimSpace(id)
	if id == "" {
		return false, nil
	}

	var exists int
	if err := DB.QueryRow(`
		SELECT EXISTS(SELECT 1 FROM folders WHERE id = ?)
	`, id).Scan(&exists); err != nil {
		return false, err
	}
	return exists == 1, nil
}
