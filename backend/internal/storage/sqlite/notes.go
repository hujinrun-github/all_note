package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/mobilev2change"
	"github.com/hujinrun/flowspace/internal/mobilev2projection"
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
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, 0, err
	}
	where := []string{"n.workspace_id = ?", "n.deleted_at IS NULL"}
	args := []interface{}{workspaceID}

	if strings.TrimSpace(filter.FolderID) != "" {
		where = append(where, "n.folder_id = ?")
		args = append(args, filter.FolderID)
	}
	if filter.ProjectID != "" {
		where = append(where,
			`EXISTS (SELECT 1 FROM note_project_links npl WHERE npl.workspace_id = n.workspace_id AND npl.note_id = n.id AND npl.project_id = ?)`)
		args = append(args, filter.ProjectID)
	}
	if filter.Unassigned {
		where = append(where,
			`NOT EXISTS (SELECT 1 FROM note_project_links npl WHERE npl.workspace_id = n.workspace_id AND npl.note_id = n.id)`)
	}

	whereClause := strings.Join(where, " AND ")

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
	query := fmt.Sprintf(`
		SELECT n.id, n.title, n.body, n.folder_id, n.tags, n.created_at, n.updated_at
		FROM notes n WHERE %s ORDER BY %s LIMIT ? OFFSET ?
	`, whereClause, order)

	selectArgs := make([]interface{}, len(args))
	copy(selectArgs, args)
	selectArgs = append(selectArgs, pageSize, offset)

	rows, err := r.db.QueryContext(ctx, query, selectArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	notes, err := scanSQLiteNotes(rows)
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
	var note model.Note
	err = r.db.QueryRowContext(ctx, `
		SELECT id, title, body, folder_id, tags, created_at, updated_at
		FROM notes WHERE workspace_id = ? AND id = ? AND deleted_at IS NULL
	`, workspaceID, id).Scan(&note.ID, &note.Title, &note.Body, &note.FolderID, &note.Tags, &note.CreatedAt, &note.UpdatedAt)
	if err != nil {
		return nil, err
	}

	// Load projects for this note.
	projectsMap, err := getNotesProjects(ctx, r.db, workspaceID, []string{note.ID})
	if err != nil {
		return nil, err
	}
	note.Projects = projectsMap[note.ID]

	return &note, nil
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
	if err := r.CreateWithID(ctx, note); err != nil {
		return nil, err
	}
	// Insert project links if provided.
	if len(req.ProjectIDs) > 0 {
		for _, pid := range req.ProjectIDs {
			if _, err := r.db.ExecContext(ctx,
				`INSERT OR IGNORE INTO note_project_links (workspace_id, note_id, project_id, created_at)
				 VALUES (?, ?, ?, ?)`, workspaceID, note.ID, pid, nowUnix()); err != nil {
				return nil, fmt.Errorf("insert project link: %w", err)
			}
		}
	}
	return r.GetByID(ctx, note.ID)
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

	clientID := deterministicSQLiteMobileNoteClientID(workspaceID, note.ID)
	return r.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO notes (id, client_id, revision, title, body, folder_id, tags, created_at, updated_at, workspace_id)
			VALUES (?, ?, 1, ?, ?, ?, ?, ?, ?, ?)
		`, note.ID, clientID, note.Title, note.Body, note.FolderID, note.Tags, note.CreatedAt, note.UpdatedAt, workspaceID); err != nil {
			return err
		}
		return persistSQLiteServerNoteChange(
			ctx, tx, workspaceID, uuid.NewString(), model.MobileOperationNoteServerCreated, clientID, note.UpdatedAt,
		)
	})
}

func (r noteRepository) Update(ctx context.Context, id string, req *model.UpdateNoteRequest) (*model.Note, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	now := nowUnix()
	sets := []string{"updated_at = ?", "revision = revision + 1"}
	args := []interface{}{now}

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

	mutationID := uuid.NewString()
	err = r.withTx(ctx, func(tx *sql.Tx) error {
		updateArgs := append(append([]interface{}{}, args...), id, workspaceID)
		result, err := tx.ExecContext(ctx, fmt.Sprintf(
			"UPDATE notes SET %s WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL",
			strings.Join(sets, ", "),
		), updateArgs...)
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
		if req.ProjectIDs != nil {
			if err := setNoteProjectLinks(ctx, tx, workspaceID, id, *req.ProjectIDs); err != nil {
				return fmt.Errorf("update project links: %w", err)
			}
		}
		var clientID string
		if err := tx.QueryRowContext(ctx, `
			SELECT client_id FROM notes WHERE workspace_id = ? AND id = ?
		`, workspaceID, id).Scan(&clientID); err != nil {
			return err
		}
		return persistSQLiteServerNoteChange(
			ctx, tx, workspaceID, mutationID, model.MobileOperationNoteServerUpdated, clientID, now,
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
	now := nowUnix()
	return r.withTx(ctx, func(tx *sql.Tx) error {
		var clientID string
		var revision int64
		err := tx.QueryRowContext(ctx, `
			SELECT client_id, revision FROM notes WHERE workspace_id = ? AND id = ? AND deleted_at IS NULL
		`, workspaceID, id).Scan(&clientID, &revision)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := enqueueSQLiteNoteAttachmentCleanup(ctx, tx, workspaceID, id, now); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE notes SET deleted_at = ?, updated_at = ?, revision = revision + 1,
				title = '', body = '', tags = '[]'
			WHERE workspace_id = ? AND id = ? AND deleted_at IS NULL
		`, now, now, workspaceID, id)
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
		if err := redactSQLiteExtendedNoteContent(ctx, tx, workspaceID, id); err != nil {
			return err
		}
		if err := upsertSQLiteContentTombstone(ctx, tx, workspaceID, "note", id, clientID, revision+1, now); err != nil {
			return err
		}
		taskChanges, err := detachSQLiteNoteTaskReferences(ctx, tx, workspaceID, id, now)
		if err != nil {
			return err
		}
		voiceChanges, err := deleteSQLiteVoiceNotesForDeletedNote(ctx, tx, workspaceID, id, now)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO mobile_retired_ids (workspace_id, entity_type, client_id, retired_at)
			VALUES (?, 'note', ?, ?)
		`, workspaceID, clientID, now); err != nil {
			return err
		}
		if err := persistSQLiteServerNoteChange(
			ctx, tx, workspaceID, uuid.NewString(), model.MobileOperationNoteServerDeleted, clientID, now,
		); err != nil {
			return err
		}
		if err := mobilev2change.AppendTaskChangesAtCurrentSequence(
			ctx, tx, mobilev2projection.DialectSQLite, workspaceID, taskChanges, time.Unix(now, 0).UTC(),
		); err != nil {
			return err
		}
		return persistSQLiteDeletedVoiceNoteChanges(ctx, tx, workspaceID, voiceChanges, now)
	})
}

type deletedSQLiteVoiceNoteChange struct {
	ClientID string
}

func deleteSQLiteVoiceNotesForDeletedNote(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID string,
	noteID string,
	now int64,
) ([]deletedSQLiteVoiceNoteChange, error) {
	ready, err := sqliteNoteTableAvailable(ctx, tx, "voice_notes")
	if err != nil || !ready {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT id, client_id, revision, object_key
		FROM voice_notes
		WHERE workspace_id = ? AND note_id = ? AND deleted_at IS NULL
	`, workspaceID, noteID)
	if err != nil {
		return nil, err
	}
	type voiceRow struct {
		id        string
		clientID  string
		revision  int64
		objectKey string
	}
	voices := make([]voiceRow, 0)
	for rows.Next() {
		var voice voiceRow
		if err := rows.Scan(&voice.id, &voice.clientID, &voice.revision, &voice.objectKey); err != nil {
			rows.Close()
			return nil, err
		}
		voices = append(voices, voice)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	changes := make([]deletedSQLiteVoiceNoteChange, 0, len(voices))
	for _, voice := range voices {
		audioState := model.VoiceAudioDeleted
		if voice.objectKey != "" {
			audioState = model.VoiceAudioDeleteRequested
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE voice_notes SET deleted_at = ?, audio_state = ?, audio_revision = audio_revision + 1,
				object_key = CASE WHEN ? = '' THEN '' ELSE object_key END,
				mime_type = CASE WHEN ? = '' THEN '' ELSE mime_type END,
				audio_size = CASE WHEN ? = '' THEN 0 ELSE audio_size END,
				audio_sha256 = CASE WHEN ? = '' THEN '' ELSE audio_sha256 END,
				upload_state = CASE WHEN ? = '' THEN 'failed' ELSE upload_state END,
				duration_ms = 0, recorded_at = 0, language = '', transcription_error = '',
				revision = revision + 1, updated_at = ?
			WHERE workspace_id = ? AND id = ? AND revision = ? AND deleted_at IS NULL
		`, now, audioState, voice.objectKey, voice.objectKey, voice.objectKey, voice.objectKey, voice.objectKey, now, workspaceID, voice.id, voice.revision)
		if err != nil {
			return nil, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return nil, err
		}
		if affected != 1 {
			continue
		}
		if err := cancelSQLiteVoiceTranscriptionJobs(ctx, tx, workspaceID, voice.clientID, now); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM transcription_results WHERE workspace_id = ? AND voice_note_id = ?
		`, workspaceID, voice.clientID); err != nil {
			return nil, err
		}
		if err := upsertSQLiteContentTombstone(ctx, tx, workspaceID, "voice_note", voice.id, voice.clientID, voice.revision+1, now); err != nil {
			return nil, err
		}
		if voice.objectKey != "" {
			if err := enqueueSQLiteVoiceAudioCleanup(ctx, tx, workspaceID, voice.clientID, voice.objectKey, now, now); err != nil {
				return nil, err
			}
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO mobile_retired_ids (workspace_id, entity_type, client_id, retired_at)
			VALUES (?, 'voice_note', ?, ?)
		`, workspaceID, voice.clientID, now); err != nil {
			return nil, err
		}
		changes = append(changes, deletedSQLiteVoiceNoteChange{ClientID: voice.clientID})
	}
	return changes, nil
}

func persistSQLiteDeletedVoiceNoteChanges(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID string,
	changes []deletedSQLiteVoiceNoteChange,
	now int64,
) error {
	for _, change := range changes {
		if err := persistSQLiteServerEntityChange(ctx, tx, workspaceID, uuid.NewString(), "voice_note", "voice_note.server_deleted", change.ClientID, now); err != nil {
			return err
		}
	}
	return nil
}

func detachSQLiteNoteTaskReferences(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID string,
	noteID string,
	now int64,
) (storage.MobileV2TaskChangeSnapshot, error) {
	changes := storage.MobileV2TaskChangeSnapshot{
		TaskIDs: make(map[string]struct{}), OccurrenceIDs: make(map[string]struct{}),
	}
	timestamp := time.Unix(now, 0).UTC().Format(time.RFC3339Nano)
	tasksReady, err := sqliteNoteTableAvailable(ctx, tx, "domain_tasks_v2")
	if err != nil {
		return changes, err
	}
	if tasksReady {
		if err := collectSQLiteDetachedNoteReferenceIDs(ctx, tx, `UPDATE domain_tasks_v2
			SET note_id=NULL,revision=revision+1,updated_at=?
			WHERE workspace_id=? AND note_id=?
			RETURNING id`, changes.TaskIDs, timestamp, workspaceID, noteID); err != nil {
			return changes, err
		}
	}
	occurrencesReady, err := sqliteNoteTableAvailable(ctx, tx, "domain_task_occurrences_v2")
	if err != nil {
		return changes, err
	}
	if occurrencesReady {
		if err := collectSQLiteDetachedNoteReferenceIDs(ctx, tx, `UPDATE domain_task_occurrences_v2
			SET note_id=NULL,revision=revision+1,updated_at=?
			WHERE workspace_id=? AND note_id=?
			RETURNING id`, changes.OccurrenceIDs, timestamp, workspaceID, noteID); err != nil {
			return changes, err
		}
	}
	legacyReady, err := sqliteNoteTableAvailable(ctx, tx, "tasks")
	if err != nil {
		return changes, err
	}
	if legacyReady {
		if _, err := tx.ExecContext(ctx, `UPDATE tasks SET note_id=NULL,updated_at=?
			WHERE workspace_id=? AND note_id=?`, timestamp, workspaceID, noteID); err != nil {
			return changes, err
		}
	}
	return changes, nil
}

func collectSQLiteDetachedNoteReferenceIDs(
	ctx context.Context,
	tx *sql.Tx,
	query string,
	ids map[string]struct{},
	args ...any,
) error {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids[id] = struct{}{}
	}
	return rows.Err()
}

func sqliteNoteTableAvailable(ctx context.Context, tx *sql.Tx, table string) (bool, error) {
	var count int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master
		WHERE type='table' AND name=?`, table).Scan(&count)
	return count == 1, err
}

func upsertSQLiteContentTombstone(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID, entityType, entityID, clientID string,
	revision, deletedAt int64,
) error {
	available, err := sqliteContentRetentionAvailable(ctx, tx)
	if err != nil || !available {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO mobile_v2_content_tombstones
		(workspace_id,entity_type,entity_id,client_id,revision,deleted_at)
		VALUES (?,?,?,?,?,?)
		ON CONFLICT(workspace_id,entity_type,entity_id) DO UPDATE SET
			client_id=excluded.client_id,
			revision=MAX(mobile_v2_content_tombstones.revision,excluded.revision),
			deleted_at=MIN(mobile_v2_content_tombstones.deleted_at,excluded.deleted_at)`,
		workspaceID, entityType, entityID, clientID, revision, deletedAt)
	return err
}

func sqliteContentRetentionAvailable(ctx context.Context, tx *sql.Tx) (bool, error) {
	var count int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master
		WHERE type='table' AND name='mobile_v2_content_tombstones'`).Scan(&count)
	return count == 1, err
}

func redactSQLiteExtendedNoteContent(ctx context.Context, tx *sql.Tx, workspaceID, noteID string) error {
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('notes')
		WHERE name IN ('content','content_text')`).Scan(&count); err != nil {
		return err
	}
	if count != 2 {
		return nil
	}
	_, err := tx.ExecContext(ctx, `UPDATE notes SET content='',content_text=''
		WHERE workspace_id=? AND id=?`, workspaceID, noteID)
	return err
}

func enqueueSQLiteNoteAttachmentCleanup(ctx context.Context, tx *sql.Tx, workspaceID, noteID string, now int64) error {
	attachmentsReady, err := sqliteNoteTableAvailable(ctx, tx, "note_attachments")
	if err != nil || !attachmentsReady {
		return err
	}
	cleanupReady, err := sqliteNoteTableAvailable(ctx, tx, "voice_audio_cleanup_jobs")
	if err != nil || !cleanupReady {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,object_key FROM note_attachments
		WHERE workspace_id=? AND note_id=? AND object_key<>''`, workspaceID, noteID)
	if err != nil {
		return err
	}
	type attachmentObject struct{ id, key string }
	objects := make([]attachmentObject, 0)
	for rows.Next() {
		var item attachmentObject
		if err := rows.Scan(&item.id, &item.key); err != nil {
			rows.Close()
			return err
		}
		objects = append(objects, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, item := range objects {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO voice_audio_cleanup_jobs
			(job_id,workspace_id,voice_note_id,object_key,state,revision,attempt,max_attempts,error_code,next_attempt_at,lease_owner,lease_token,created_at,updated_at)
			VALUES(?,?,?,?,'queued',1,0,6,'',?,'','',?,?)`, uuid.NewString(), workspaceID,
			storage.NoteAttachmentCleanupSubject(item.id), item.key, now, now, now); err != nil {
			return err
		}
	}
	return nil
}

func (r noteRepository) withTx(ctx context.Context, fn func(*sql.Tx) error) error {
	if tx, ok := r.db.(*sql.Tx); ok {
		return fn(tx)
	}
	db, ok := r.db.(*sql.DB)
	if !ok {
		return fmt.Errorf("unsupported sqlite note runner %T", r.db)
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

func (r noteRepository) ListAll(ctx context.Context) ([]model.Note, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, title, body, folder_id, tags, created_at, updated_at
		FROM notes WHERE workspace_id = ? AND deleted_at IS NULL ORDER BY updated_at DESC
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSQLiteNotes(rows)
}

func (r noteRepository) Recent(ctx context.Context, limit int) ([]model.Note, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, title, body, folder_id, tags, created_at, updated_at
		FROM notes WHERE workspace_id = ? AND deleted_at IS NULL ORDER BY updated_at DESC LIMIT ?
	`, workspaceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	notes, err := scanSQLiteNotes(rows)
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
	placeholders := make([]string, len(projectIDs))
	args := make([]interface{}, 0, len(projectIDs)+1)
	args = append(args, workspaceID)
	for i, id := range projectIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}
	query := fmt.Sprintf(
		`SELECT n.id, n.title, npl.project_id
		 FROM notes n
		 JOIN note_project_links npl ON n.workspace_id = npl.workspace_id AND n.id = npl.note_id
		 WHERE n.workspace_id = ? AND n.deleted_at IS NULL AND npl.project_id IN (%s)
		 ORDER BY n.updated_at DESC`, strings.Join(placeholders, ","))
	rows, err := r.db.QueryContext(ctx, query, args...)
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
func setNoteProjectLinks(ctx context.Context, runner sqliteRunner, workspaceID string, noteID string, projectIDs []string) error {
	if projectIDs == nil {
		return nil // nil means don't modify
	}
	if len(projectIDs) == 0 {
		_, err := runner.ExecContext(ctx,
			`DELETE FROM note_project_links WHERE workspace_id = ? AND note_id = ?`, workspaceID, noteID)
		return err
	}
	// Build placeholders for the NOT IN clause
	placeholders := make([]string, len(projectIDs))
	args := make([]interface{}, 0, len(projectIDs)+2)
	args = append(args, workspaceID, noteID)
	for i, pid := range projectIDs {
		placeholders[i] = "?"
		args = append(args, pid)
	}
	// Delete links not in the new set
	query := fmt.Sprintf(
		`DELETE FROM note_project_links WHERE workspace_id = ? AND note_id = ? AND project_id NOT IN (%s)`,
		strings.Join(placeholders, ","))
	if _, err := runner.ExecContext(ctx, query, args...); err != nil {
		return err
	}
	// Insert new links (INSERT OR IGNORE keeps original created_at for existing)
	for _, pid := range projectIDs {
		_, err := runner.ExecContext(ctx,
			`INSERT OR IGNORE INTO note_project_links (workspace_id, note_id, project_id, created_at)
			 VALUES (?, ?, ?, ?)`, workspaceID, noteID, pid, nowUnix())
		if err != nil {
			return err
		}
	}
	return nil
}

// getNotesProjects fetches project info for a batch of note IDs.
func getNotesProjects(ctx context.Context, runner sqliteRunner, workspaceID string, noteIDs []string) (map[string][]model.NoteProject, error) {
	if len(noteIDs) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(noteIDs))
	args := make([]interface{}, 0, len(noteIDs)+1)
	args = append(args, workspaceID)
	for i, id := range noteIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}
	query := fmt.Sprintf(
		`SELECT npl.note_id, tp.id, tp.name, tp.type
		 FROM note_project_links npl
		 JOIN task_projects tp ON tp.workspace_id = npl.workspace_id AND tp.id = npl.project_id
		 WHERE npl.workspace_id = ? AND npl.note_id IN (%s)
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
