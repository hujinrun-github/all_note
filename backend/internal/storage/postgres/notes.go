package postgres

import (
	"context"
	"crypto/md5"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hujinrun/flowspace/internal/auth"
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
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, 0, err
	}
	where := []string{"n.workspace_id = $1", "n.deleted_at IS NULL"}
	args := []interface{}{workspaceID}

	if strings.TrimSpace(filter.FolderID) != "" {
		where = append(where, fmt.Sprintf("n.folder_id = %s", pgPlaceholder(len(args)+1)))
		args = append(args, filter.FolderID)
	}
	if filter.ProjectID != "" {
		where = append(where,
			`EXISTS (SELECT 1 FROM note_project_links npl WHERE npl.workspace_id = n.workspace_id AND npl.note_id = n.id AND npl.project_id = $`+strconv.Itoa(len(args)+1)+`)`)
		args = append(args, filter.ProjectID)
	}
	if filter.Unassigned {
		where = append(where,
			`NOT EXISTS (SELECT 1 FROM note_project_links npl WHERE npl.workspace_id = n.workspace_id AND npl.note_id = n.id)`)
	}

	whereClause := "1=1"
	if len(where) > 0 {
		whereClause = strings.Join(where, " AND ")
	}

	var total int
	if err := r.db.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM notes n WHERE %s", whereClause), args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	order := "n.created_at DESC"
	if filter.Sort == "az" {
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

	selectArgs := make([]interface{}, len(args))
	copy(selectArgs, args)
	limitPlaceholder := pgPlaceholder(len(selectArgs) + 1)
	offsetPlaceholder := pgPlaceholder(len(selectArgs) + 2)
	selectArgs = append(selectArgs, pageSize, offset)

	query := fmt.Sprintf(`
		SELECT n.id, n.title, n.body, n.folder_id, n.tags, n.created_at, n.updated_at
		FROM notes n WHERE %s ORDER BY %s LIMIT %s OFFSET %s
	`, whereClause, order, limitPlaceholder, offsetPlaceholder)

	rows, err := r.db.QueryContext(ctx, query, selectArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	notes, err := scanPostgresNotes(rows)
	if err != nil {
		return nil, 0, err
	}

	// Batch load projects for the notes on this page.
	noteIDs := make([]string, len(notes))
	for i, n := range notes {
		noteIDs[i] = n.ID
	}
	projectsMap, err := getNotesProjects(ctx, r.db, workspaceID, noteIDs)
	if err != nil {
		return nil, 0, err
	}
	for i := range notes {
		notes[i].Projects = projectsMap[notes[i].ID]
	}

	return notes, total, nil
}

func (r noteRepository) GetByID(ctx context.Context, id string) (*model.Note, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	row := r.db.QueryRowContext(ctx, `
		SELECT id, title, body, folder_id, tags, created_at, updated_at
		FROM notes WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL
	`, workspaceID, id)
	note, err := scanPostgresNote(row)
	if err != nil {
		return nil, err
	}

	// Load projects for this note.
	projectsMap, err := getNotesProjects(ctx, r.db, workspaceID, []string{note.ID})
	if err != nil {
		return nil, err
	}
	note.Projects = projectsMap[note.ID]

	return note, nil
}

func (r noteRepository) Create(ctx context.Context, req *model.CreateNoteRequest) (*model.Note, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	note := &model.Note{
		ID:       newID(),
		Title:    req.Title,
		Body:     req.Body,
		FolderID: req.FolderID,
		Tags:     req.Tags,
	}
	// Use a single transaction for note insert + search index + project links
	if err := r.withTx(ctx, func(tx *sql.Tx) error {
		if err := createNoteInTx(ctx, tx, workspaceID, note); err != nil {
			return err
		}
		if len(req.ProjectIDs) > 0 {
			if err := setNoteProjectLinks(ctx, tx, workspaceID, note.ID, req.ProjectIDs); err != nil {
				return fmt.Errorf("insert project links: %w", err)
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return r.GetByID(ctx, note.ID)
}

// createNoteInTx inserts a note and its search_index entry within a transaction.
func createNoteInTx(ctx context.Context, tx *sql.Tx, workspaceID string, note *model.Note) error {
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
		return fmt.Errorf("parse tags: %w", err)
	}
	if note.CreatedAt == 0 {
		note.CreatedAt = nowUnix()
	}
	if note.UpdatedAt == 0 {
		note.UpdatedAt = nowUnix()
	}
	note.Tags = tagsArrayToJSONString(tags)
	clientID := deterministicPostgresMobileNoteClientID(workspaceID, note.ID)
	_, err = tx.ExecContext(ctx,
		`INSERT INTO notes (id, client_id, revision, title, body, folder_id, tags, created_at, updated_at, workspace_id)
		 VALUES ($1, $2, 1, $3, $4, $5, $6::text[], $7, $8, $9)`,
		note.ID, clientID, note.Title, note.Body, note.FolderID,
		pq.Array(tags), unixToTime(note.CreatedAt), unixToTime(note.UpdatedAt), workspaceID)
	if err != nil {
		return fmt.Errorf("insert note: %w", err)
	}
	if err := upsertNoteSearchIndex(ctx, tx, workspaceID, note, tags); err != nil {
		return err
	}
	return persistPostgresServerNoteChange(
		ctx, tx, workspaceID, uuid.NewString(), model.MobileOperationNoteServerCreated, clientID, unixToTime(note.UpdatedAt),
	)
}

func deterministicPostgresMobileNoteClientID(workspaceID, noteID string) string {
	digest := md5.Sum([]byte("flowspace:note:" + workspaceID + ":" + noteID))
	digest[6] = (digest[6] & 0x0f) | 0x30
	digest[8] = (digest[8] & 0x3f) | 0x80
	return uuid.UUID(digest).String()
}

func (r noteRepository) CreateWithID(ctx context.Context, note *model.Note) error {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	if note == nil {
		return fmt.Errorf("note is nil")
	}
	if strings.TrimSpace(note.ID) == "" {
		note.ID = newID()
	}
	if strings.TrimSpace(note.FolderID) == "" {
		note.FolderID = "__uncategorized"
	}
	if note.CreatedAt == 0 {
		note.CreatedAt = nowUnix()
	}
	if note.UpdatedAt == 0 {
		note.UpdatedAt = nowUnix()
	}
	return r.withTx(ctx, func(tx *sql.Tx) error {
		return createNoteInTx(ctx, tx, workspaceID, note)
	})
}

func (r noteRepository) Update(ctx context.Context, id string, req *model.UpdateNoteRequest) (*model.Note, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
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
	args = append(args, id, workspaceID)
	idPlaceholder := pgPlaceholder(len(args) - 1)
	workspacePlaceholder := pgPlaceholder(len(args))

	err = r.withTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, fmt.Sprintf(
			"UPDATE notes SET %s, revision = revision + 1 WHERE id = %s AND workspace_id = %s AND deleted_at IS NULL",
			clause, idPlaceholder, workspacePlaceholder,
		), args...)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return sql.ErrNoRows
		}
		note, err := scanPostgresNote(tx.QueryRowContext(ctx, `
			SELECT id, title, body, folder_id, tags, created_at, updated_at
			FROM notes WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL
		`, workspaceID, id))
		if err != nil {
			return err
		}
		tags, err := tagsJSONStringToArray(note.Tags)
		if err != nil {
			return err
		}
		if err := upsertNoteSearchIndex(ctx, tx, workspaceID, note, tags); err != nil {
			return err
		}
		// Merge project links if provided.
		if req.ProjectIDs != nil {
			if err := setNoteProjectLinks(ctx, tx, workspaceID, id, *req.ProjectIDs); err != nil {
				return fmt.Errorf("update project links: %w", err)
			}
		}
		var clientID string
		if err := tx.QueryRowContext(ctx, `
			SELECT client_id FROM notes WHERE workspace_id = $1 AND id = $2
		`, workspaceID, id).Scan(&clientID); err != nil {
			return err
		}
		return persistPostgresServerNoteChange(
			ctx, tx, workspaceID, uuid.NewString(), model.MobileOperationNoteServerUpdated, clientID, updatedAt,
		)
	})
	if err != nil {
		return nil, err
	}
	return r.GetByID(ctx, id)
}

func (r noteRepository) Delete(ctx context.Context, id string) error {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	return r.withTx(ctx, func(tx *sql.Tx) error {
		var clientID string
		err := tx.QueryRowContext(ctx, `
			SELECT client_id FROM notes WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL
			FOR UPDATE
		`, workspaceID, id).Scan(&clientID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE notes SET deleted_at = $1, updated_at = $1, revision = revision + 1
			WHERE workspace_id = $2 AND id = $3 AND deleted_at IS NULL
		`, now, workspaceID, id)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO mobile_retired_ids (workspace_id, entity_type, client_id, retired_at)
			VALUES ($1, 'note', $2, $3)
			ON CONFLICT (workspace_id, entity_type, client_id) DO NOTHING
		`, workspaceID, clientID, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM search_index WHERE workspace_id = $1 AND entity_type = 'note' AND entity_id = $2`, workspaceID, id); err != nil {
			return err
		}
		return persistPostgresServerNoteChange(
			ctx, tx, workspaceID, uuid.NewString(), model.MobileOperationNoteServerDeleted, clientID, now,
		)
	})
}

func (r noteRepository) ListAll(ctx context.Context) ([]model.Note, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, title, body, folder_id, tags, created_at, updated_at
		FROM notes WHERE workspace_id = $1 AND deleted_at IS NULL ORDER BY updated_at DESC
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPostgresNotes(rows)
}

func (r noteRepository) Recent(ctx context.Context, limit int) ([]model.Note, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, title, body, folder_id, tags, created_at, updated_at
		FROM notes WHERE workspace_id = $1 AND deleted_at IS NULL ORDER BY updated_at DESC LIMIT $2
	`, workspaceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	notes, err := scanPostgresNotes(rows)
	if err != nil {
		return nil, err
	}

	// Batch load projects for recent notes.
	noteIDs := make([]string, len(notes))
	for i, n := range notes {
		noteIDs[i] = n.ID
	}
	projectsMap, err := getNotesProjects(ctx, r.db, workspaceID, noteIDs)
	if err != nil {
		return nil, err
	}
	for i := range notes {
		notes[i].Projects = projectsMap[notes[i].ID]
	}

	return notes, nil
}

func (r noteRepository) GetNotesByProjectIDs(ctx context.Context, projectIDs []string) (map[string][]model.NoteRef, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if len(projectIDs) == 0 {
		return map[string][]model.NoteRef{}, nil
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT n.id, n.title, npl.project_id
		 FROM notes n
		 JOIN note_project_links npl ON n.workspace_id = npl.workspace_id AND n.id = npl.note_id
		 WHERE n.workspace_id = $1 AND n.deleted_at IS NULL AND npl.project_id = ANY($2::text[])
		 ORDER BY n.updated_at DESC`, workspaceID, pq.Array(projectIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string][]model.NoteRef)
	for rows.Next() {
		var ref model.NoteRef
		var projectID string
		if err := rows.Scan(&ref.ID, &ref.Title, &projectID); err != nil {
			return nil, err
		}
		result[projectID] = append(result[projectID], ref)
	}
	return result, rows.Err()
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

func upsertNoteSearchIndex(ctx context.Context, tx *sql.Tx, workspaceID string, note *model.Note, tags []string) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO search_index (workspace_id, entity_type, entity_id, title, content, tags, updated_at, search_vector)
		VALUES (
			$1,
			'note',
			$2,
			$3,
			$4,
			$5::text[],
			$6,
			to_tsvector('simple', coalesce($3, '') || ' ' || coalesce($4, '') || ' ' || coalesce(array_to_string($5::text[], ' '), ''))
		)
		ON CONFLICT (entity_type, entity_id) DO UPDATE SET
			workspace_id = excluded.workspace_id,
			title = excluded.title,
			content = excluded.content,
			tags = excluded.tags,
			updated_at = excluded.updated_at,
			search_vector = excluded.search_vector
	`, workspaceID, note.ID, note.Title, note.Body, pq.Array(tags), unixToTime(note.UpdatedAt))
	return err
}

// setNoteProjectLinks merges project links for a note using merge strategy.
// nil projectIDs = no-op, empty = delete all, non-empty = merge.
func setNoteProjectLinks(ctx context.Context, runner postgresRunner, workspaceID string, noteID string, projectIDs []string) error {
	if projectIDs == nil {
		return nil
	}
	if len(projectIDs) == 0 {
		_, err := runner.ExecContext(ctx,
			`DELETE FROM note_project_links WHERE workspace_id = $1 AND note_id = $2`, workspaceID, noteID)
		return err
	}
	// Delete links not in the new set
	_, err := runner.ExecContext(ctx,
		`DELETE FROM note_project_links WHERE workspace_id = $1 AND note_id = $2 AND project_id != ALL($3::text[])`,
		workspaceID, noteID, pq.Array(projectIDs))
	if err != nil {
		return err
	}
	// Insert new links (ON CONFLICT DO NOTHING keeps original created_at)
	for _, pid := range projectIDs {
		_, err := runner.ExecContext(ctx,
			`INSERT INTO note_project_links (workspace_id, note_id, project_id)
			 VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, workspaceID, noteID, pid)
		if err != nil {
			return err
		}
	}
	return nil
}

// getNotesProjects fetches project info for a batch of note IDs.
func getNotesProjects(ctx context.Context, runner postgresRunner, workspaceID string, noteIDs []string) (map[string][]model.NoteProject, error) {
	if len(noteIDs) == 0 {
		return nil, nil
	}
	rows, err := runner.QueryContext(ctx,
		`SELECT npl.note_id, tp.id, tp.name, tp.type
		 FROM note_project_links npl
		 JOIN task_projects tp ON tp.workspace_id = npl.workspace_id AND tp.id = npl.project_id
		 WHERE npl.workspace_id = $1 AND npl.note_id = ANY($2::text[])
		 ORDER BY tp.name ASC`, workspaceID, pq.Array(noteIDs))
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
