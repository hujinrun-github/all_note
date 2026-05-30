package repository

import "github.com/hujinrun/flowspace/internal/model"

func GetFolders() ([]model.Folder, error) {
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

	var folders []model.Folder
	for rows.Next() {
		var f model.Folder
		if err := rows.Scan(&f.ID, &f.Name, &f.SortOrder, &f.NoteCount, &f.CreatedAt); err != nil {
			return nil, err
		}
		folders = append(folders, f)
	}
	return folders, nil
}
