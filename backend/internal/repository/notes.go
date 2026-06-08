package repository

import (
	"fmt"
	"strings"

	"github.com/hujinrun/flowspace/internal/model"
)

func GetNotes(folderID, sort string, page, pageSize int) ([]model.Note, int, error) {
	where := "1=1"
	args := []interface{}{}
	if folderID != "" {
		where = "n.folder_id = ?"
		args = append(args, folderID)
	}

	var total int
	DB.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM notes n WHERE %s", where), args...).Scan(&total)

	order := "n.created_at DESC"
	if sort == "az" {
		order = "n.title ASC"
	}

	offset := (page - 1) * pageSize
	query := fmt.Sprintf(`
		SELECT n.id, n.title, n.body, n.folder_id, n.tags, n.created_at, n.updated_at
		FROM notes n WHERE %s ORDER BY %s LIMIT ? OFFSET ?
	`, where, order)
	args = append(args, pageSize, offset)

	rows, err := DB.Query(query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	notes := make([]model.Note, 0)
	for rows.Next() {
		var n model.Note
		if err := rows.Scan(&n.ID, &n.Title, &n.Body, &n.FolderID, &n.Tags, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, 0, err
		}
		notes = append(notes, n)
	}
	return notes, total, nil
}

func GetNoteByID(id string) (*model.Note, error) {
	var n model.Note
	err := DB.QueryRow(`
		SELECT id, title, body, folder_id, tags, created_at, updated_at
		FROM notes WHERE id = ?
	`, id).Scan(&n.ID, &n.Title, &n.Body, &n.FolderID, &n.Tags, &n.CreatedAt, &n.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &n, nil
}

func CreateNote(n *model.Note) error {
	n.ID = newUUID()
	now := nowUnix()
	n.CreatedAt = now
	n.UpdatedAt = now
	if n.FolderID == "" {
		n.FolderID = "__uncategorized"
	}
	if n.Tags == "" {
		n.Tags = "[]"
	}
	_, err := DB.Exec(`
		INSERT INTO notes (id, title, body, folder_id, tags, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, n.ID, n.Title, n.Body, n.FolderID, n.Tags, n.CreatedAt, n.UpdatedAt)
	return err
}

func CreateNoteWithID(req *model.CreateNoteWithIDRequest) (*model.Note, error) {
	id := strings.TrimSpace(req.ID)
	if id == "" {
		id = newUUID()
	}
	now := nowUnix()
	folderID := req.FolderID
	if folderID == "" {
		folderID = "__uncategorized"
	}
	tags := req.Tags
	if tags == "" {
		tags = "[]"
	}
	_, err := DB.Exec(`
		INSERT INTO notes (id, title, body, folder_id, tags, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, id, req.Title, req.Body, folderID, tags, now, now)
	if err != nil {
		return nil, err
	}
	return GetNoteByID(id)
}

func ListAllNotes() ([]model.Note, error) {
	notes, _, err := GetNotes("", "recent", 1, 100000)
	return notes, err
}

func UpdateNote(id string, req *model.UpdateNoteRequest) (*model.Note, error) {
	sets := []string{"updated_at = ?"}
	args := []interface{}{nowUnix()}

	if req.Title != nil {
		sets = append(sets, "title = ?")
		args = append(args, *req.Title)
	}
	if req.Body != nil {
		sets = append(sets, "body = ?")
		args = append(args, *req.Body)
	}
	if req.FolderID != nil {
		sets = append(sets, "folder_id = ?")
		args = append(args, *req.FolderID)
	}
	if req.Tags != nil {
		sets = append(sets, "tags = ?")
		args = append(args, *req.Tags)
	}

	args = append(args, id)
	_, err := DB.Exec(fmt.Sprintf("UPDATE notes SET %s WHERE id = ?", strings.Join(sets, ", ")), args...)
	if err != nil {
		return nil, err
	}
	return GetNoteByID(id)
}

func DeleteNote(id string) error {
	_, err := DB.Exec("DELETE FROM notes WHERE id = ?", id)
	return err
}

func GetRecentNotes(limit int) ([]model.Note, error) {
	rows, err := DB.Query(`
		SELECT id, title, body, folder_id, tags, created_at, updated_at
		FROM notes ORDER BY updated_at DESC LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	notes := make([]model.Note, 0)
	for rows.Next() {
		var n model.Note
		if err := rows.Scan(&n.ID, &n.Title, &n.Body, &n.FolderID, &n.Tags, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, err
		}
		notes = append(notes, n)
	}
	return notes, nil
}
