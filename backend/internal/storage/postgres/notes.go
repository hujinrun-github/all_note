package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/lib/pq"
)

type postgresRunner interface {
	ExecContext(context.Context, string, ...interface{}) (sql.Result, error)
	QueryContext(context.Context, string, ...interface{}) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...interface{}) *sql.Row
}

type noteRepository struct {
	db postgresRunner
}

func (r noteRepository) List(ctx context.Context, filter storage.NoteFilter) ([]model.Note, int, error) {
	where := "1=1"
	args := []interface{}{}
	if strings.TrimSpace(filter.FolderID) != "" {
		where = "n.folder_id = $1"
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
	limitPlaceholder := pgPlaceholder(len(args) + 1)
	offsetPlaceholder := pgPlaceholder(len(args) + 2)
	query := fmt.Sprintf(`
		SELECT n.id, n.title, n.body, n.folder_id, n.tags, n.created_at, n.updated_at
		FROM notes n WHERE %s ORDER BY %s LIMIT %s OFFSET %s
	`, where, order, limitPlaceholder, offsetPlaceholder)
	args = append(args, pageSize, offset)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	notes, err := scanPostgresNotes(rows)
	if err != nil {
		return nil, 0, err
	}
	return notes, total, nil
}

func (r noteRepository) GetByID(ctx context.Context, id string) (*model.Note, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, title, body, folder_id, tags, created_at, updated_at
		FROM notes WHERE id = $1
	`, id)
	return scanPostgresNote(row)
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
	tags, err := tagsJSONStringToArray(note.Tags)
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
	note.Tags = tagsArrayToJSONString(tags)

	return r.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO notes (id, title, body, folder_id, tags, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5::text[], $6, $7)
		`, note.ID, note.Title, note.Body, note.FolderID, pq.Array(tags), unixToTime(note.CreatedAt), unixToTime(note.UpdatedAt)); err != nil {
			return err
		}
		return upsertNoteSearchIndex(ctx, tx, note, tags)
	})
}

func (r noteRepository) Update(ctx context.Context, id string, req *model.UpdateNoteRequest) (*model.Note, error) {
	updatedAt := time.Now().UTC()
	builder := newPgSetBuilder(1)
	builder.Add("updated_at", updatedAt)

	if req.Title != nil {
		builder.Add("title", *req.Title)
	}
	if req.Body != nil {
		builder.Add("body", *req.Body)
	}
	if req.FolderID != nil {
		builder.Add("folder_id", *req.FolderID)
	}
	if req.Tags != nil {
		tags, err := tagsJSONStringToArray(*req.Tags)
		if err != nil {
			return nil, err
		}
		builder.Add("tags", pq.Array(tags))
	}

	clause, args := builder.ClauseAndArgs()
	args = append(args, id)
	idPlaceholder := pgPlaceholder(len(args))

	var updated *model.Note
	err := r.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("UPDATE notes SET %s WHERE id = %s", clause, idPlaceholder), args...); err != nil {
			return err
		}
		note, err := scanPostgresNote(tx.QueryRowContext(ctx, `
			SELECT id, title, body, folder_id, tags, created_at, updated_at
			FROM notes WHERE id = $1
		`, id))
		if err != nil {
			return err
		}
		tags, err := tagsJSONStringToArray(note.Tags)
		if err != nil {
			return err
		}
		if err := upsertNoteSearchIndex(ctx, tx, note, tags); err != nil {
			return err
		}
		updated = note
		return nil
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

func (r noteRepository) Delete(ctx context.Context, id string) error {
	return r.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM notes WHERE id = $1`, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM search_index WHERE entity_type = 'note' AND entity_id = $1`, id)
		return err
	})
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
	return scanPostgresNotes(rows)
}

func (r noteRepository) Recent(ctx context.Context, limit int) ([]model.Note, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, title, body, folder_id, tags, created_at, updated_at
		FROM notes ORDER BY updated_at DESC LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPostgresNotes(rows)
}

func (r noteRepository) withTx(ctx context.Context, fn func(*sql.Tx) error) error {
	if tx, ok := r.db.(*sql.Tx); ok {
		return fn(tx)
	}
	db, ok := r.db.(*sql.DB)
	if !ok {
		return fmt.Errorf("unsupported postgres runner %T", r.db)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

type rowScanner interface {
	Scan(...interface{}) error
}

func scanPostgresNote(row rowScanner) (*model.Note, error) {
	var note model.Note
	var tags []string
	var createdAt time.Time
	var updatedAt time.Time
	if err := row.Scan(&note.ID, &note.Title, &note.Body, &note.FolderID, pq.Array(&tags), &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	note.Tags = tagsArrayToJSONString(tags)
	note.CreatedAt = timeToUnix(createdAt)
	note.UpdatedAt = timeToUnix(updatedAt)
	return &note, nil
}

func scanPostgresNotes(rows *sql.Rows) ([]model.Note, error) {
	notes := make([]model.Note, 0)
	for rows.Next() {
		note, err := scanPostgresNote(rows)
		if err != nil {
			return nil, err
		}
		notes = append(notes, *note)
	}
	return notes, rows.Err()
}

func upsertNoteSearchIndex(ctx context.Context, tx *sql.Tx, note *model.Note, tags []string) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO search_index (entity_type, entity_id, title, content, tags, updated_at, search_vector)
		VALUES (
			'note',
			$1,
			$2,
			$3,
			$4::text[],
			$5,
			to_tsvector('simple', coalesce($2, '') || ' ' || coalesce($3, '') || ' ' || coalesce(array_to_string($4::text[], ' '), ''))
		)
		ON CONFLICT (entity_type, entity_id) DO UPDATE SET
			title = excluded.title,
			content = excluded.content,
			tags = excluded.tags,
			updated_at = excluded.updated_at,
			search_vector = excluded.search_vector
	`, note.ID, note.Title, note.Body, pq.Array(tags), unixToTime(note.UpdatedAt))
	return err
}
