package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

type sqliteRunner interface {
	ExecContext(context.Context, string, ...interface{}) (sql.Result, error)
	QueryContext(context.Context, string, ...interface{}) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...interface{}) *sql.Row
}

type noteRepository struct {
	db sqliteRunner
}

func (r noteRepository) List(ctx context.Context, filter storage.NoteFilter) ([]model.Note, int, error) {
	where := "1=1"
	args := []interface{}{}
	if strings.TrimSpace(filter.FolderID) != "" {
		where = "n.folder_id = ?"
		args = append(args, filter.FolderID)
	}

	var total int
	if err := r.db.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM notes n WHERE %s", where), args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	order := "n.created_at DESC"
	if filter.Query == "az" {
		order = "n.title ASC"
	}

	page := filter.Page
	if page <= 0 {
		page = 1
	}
	pageSize := filter.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize
	query := fmt.Sprintf(`
		SELECT n.id, n.title, n.body, n.folder_id, n.tags, n.created_at, n.updated_at
		FROM notes n WHERE %s ORDER BY %s LIMIT ? OFFSET ?
	`, where, order)
	args = append(args, pageSize, offset)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	notes, err := scanSQLiteNotes(rows)
	if err != nil {
		return nil, 0, err
	}
	return notes, total, nil
}

func (r noteRepository) GetByID(ctx context.Context, id string) (*model.Note, error) {
	var note model.Note
	err := r.db.QueryRowContext(ctx, `
		SELECT id, title, body, folder_id, tags, created_at, updated_at
		FROM notes WHERE id = ?
	`, id).Scan(&note.ID, &note.Title, &note.Body, &note.FolderID, &note.Tags, &note.CreatedAt, &note.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &note, nil
}

func (r noteRepository) Create(ctx context.Context, req *model.CreateNoteRequest) (*model.Note, error) {
	note := &model.Note{
		ID:       newID(),
		Title:    req.Title,
		Body:     req.Body,
		FolderID: req.FolderID,
		Tags:     req.Tags,
	}
	if err := r.CreateWithID(ctx, note); err != nil {
		return nil, err
	}
	// Insert project links if provided.
	if len(req.ProjectIDs) > 0 {
		for _, pid := range req.ProjectIDs {
			if _, err := r.db.ExecContext(ctx,
				`INSERT OR IGNORE INTO note_project_links (note_id, project_id, created_at)
				 VALUES (?, ?, ?)`, note.ID, pid, nowUnix()); err != nil {
				return nil, fmt.Errorf("insert project link: %w", err)
			}
		}
	}
	return r.GetByID(ctx, note.ID)
}

func (r noteRepository) CreateWithID(ctx context.Context, note *model.Note) error {
	if note == nil {
		return fmt.Errorf("note is nil")
	}
	if strings.TrimSpace(note.ID) == "" {
		note.ID = newID()
	}
	if strings.TrimSpace(note.FolderID) == "" {
		note.FolderID = "__uncategorized"
	}
	tags, err := normalizeTagsJSON(note.Tags)
	if err != nil {
		return err
	}
	now := nowUnix()
	if note.CreatedAt == 0 {
		note.CreatedAt = now
	}
	if note.UpdatedAt == 0 {
		note.UpdatedAt = now
	}
	note.Tags = tags

	_, err = r.db.ExecContext(ctx, `
		INSERT INTO notes (id, title, body, folder_id, tags, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, note.ID, note.Title, note.Body, note.FolderID, note.Tags, note.CreatedAt, note.UpdatedAt)
	return err
}

func (r noteRepository) Update(ctx context.Context, id string, req *model.UpdateNoteRequest) (*model.Note, error) {
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
		tags, err := normalizeTagsJSON(*req.Tags)
		if err != nil {
			return nil, err
		}
		sets = append(sets, "tags = ?")
		args = append(args, tags)
	}

	args = append(args, id)
	if _, err := r.db.ExecContext(ctx, fmt.Sprintf("UPDATE notes SET %s WHERE id = ?", strings.Join(sets, ", ")), args...); err != nil {
		return nil, err
	}
	// Merge project links if provided.
	if req.ProjectIDs != nil {
		if err := setNoteProjectLinks(ctx, r.db, id, *req.ProjectIDs); err != nil {
			return nil, fmt.Errorf("update project links: %w", err)
		}
	}
	return r.GetByID(ctx, id)
}

func (r noteRepository) Delete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, "DELETE FROM notes WHERE id = ?", id)
	return err
}

func (r noteRepository) ListAll(ctx context.Context) ([]model.Note, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, title, body, folder_id, tags, created_at, updated_at
		FROM notes ORDER BY updated_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSQLiteNotes(rows)
}

func (r noteRepository) Recent(ctx context.Context, limit int) ([]model.Note, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, title, body, folder_id, tags, created_at, updated_at
		FROM notes ORDER BY updated_at DESC LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSQLiteNotes(rows)
}

func scanSQLiteNotes(rows *sql.Rows) ([]model.Note, error) {
	notes := make([]model.Note, 0)
	for rows.Next() {
		var note model.Note
		if err := rows.Scan(&note.ID, &note.Title, &note.Body, &note.FolderID, &note.Tags, &note.CreatedAt, &note.UpdatedAt); err != nil {
			return nil, err
		}
		notes = append(notes, note)
	}
	return notes, rows.Err()
}

// setNoteProjectLinks merges project links for a note using merge strategy:
// inserts new links, deletes removed ones, keeps existing (preserving created_at).
func setNoteProjectLinks(ctx context.Context, runner sqliteRunner, noteID string, projectIDs []string) error {
	if projectIDs == nil {
		return nil // nil means don't modify
	}
	if len(projectIDs) == 0 {
		_, err := runner.ExecContext(ctx,
			`DELETE FROM note_project_links WHERE note_id = ?`, noteID)
		return err
	}
	// Build placeholders for the NOT IN clause
	placeholders := make([]string, len(projectIDs))
	args := make([]interface{}, 0, len(projectIDs)+1)
	args = append(args, noteID)
	for i, pid := range projectIDs {
		placeholders[i] = "?"
		args = append(args, pid)
	}
	// Delete links not in the new set
	query := fmt.Sprintf(
		`DELETE FROM note_project_links WHERE note_id = ? AND project_id NOT IN (%s)`,
		strings.Join(placeholders, ","))
	if _, err := runner.ExecContext(ctx, query, args...); err != nil {
		return err
	}
	// Insert new links (INSERT OR IGNORE keeps original created_at for existing)
	for _, pid := range projectIDs {
		_, err := runner.ExecContext(ctx,
			`INSERT OR IGNORE INTO note_project_links (note_id, project_id, created_at)
			 VALUES (?, ?, ?)`, noteID, pid, nowUnix())
		if err != nil {
			return err
		}
	}
	return nil
}

// getNotesProjects fetches project info for a batch of note IDs.
func getNotesProjects(ctx context.Context, runner sqliteRunner, noteIDs []string) (map[string][]model.NoteProject, error) {
	if len(noteIDs) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(noteIDs))
	args := make([]interface{}, len(noteIDs))
	for i, id := range noteIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	query := fmt.Sprintf(
		`SELECT npl.note_id, tp.id, tp.name, tp.type
		 FROM note_project_links npl
		 JOIN task_projects tp ON tp.id = npl.project_id
		 WHERE npl.note_id IN (%s)
		 ORDER BY tp.name ASC`, strings.Join(placeholders, ","))
	rows, err := runner.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string][]model.NoteProject)
	for rows.Next() {
		var noteID string
		var np model.NoteProject
		if err := rows.Scan(&noteID, &np.ID, &np.Name, &np.Type); err != nil {
			return nil, err
		}
		result[noteID] = append(result[noteID], np)
	}
	return result, rows.Err()
}
