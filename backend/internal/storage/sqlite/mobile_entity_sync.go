package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

func (r mobileSyncRepository) ApplyEntityMutation(ctx context.Context, mutation model.MobileEntityMutation) (*model.MobileMutationResult, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateMobileEntityMutation(mutation); err != nil {
		return nil, err
	}
	var result *model.MobileMutationResult
	err = r.withTx(ctx, func(tx *sql.Tx) error {
		var applyErr error
		result, applyErr = applySQLiteEntityMutation(ctx, tx, workspaceID, mutation)
		return applyErr
	})
	return result, err
}

func (r mobileSyncRepository) GetEntityByClientID(ctx context.Context, entityType, clientID string) (*model.MobileEntityEnvelope, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if entityType == "note" {
		return getSQLiteMobileNote(ctx, r.db, workspaceID, clientID)
	}
	return getSQLiteMobileEntity(ctx, r.db, workspaceID, entityType, clientID)
}

func applySQLiteEntityMutation(ctx context.Context, tx *sql.Tx, workspaceID string, mutation model.MobileEntityMutation) (*model.MobileMutationResult, error) {
	if receipt, found, err := getSQLiteEntityMutationReceipt(ctx, tx, workspaceID, mutation); err != nil {
		return nil, err
	} else if found {
		return receipt, nil
	}
	if mutation.EntityType == "voice_note" {
		entity, err := applySQLiteVoiceMutation(ctx, tx, workspaceID, mutation)
		if err != nil {
			return nil, err
		}
		result := &model.MobileMutationResult{MutationID: mutation.MutationID, Status: model.MobileMutationApplied, Entity: entity}
		if err := persistSQLiteEntityMutationResult(ctx, tx, workspaceID, mutation, result); err != nil {
			return nil, err
		}
		return result, nil
	}
	if mutation.EntityType == "task_occurrence" {
		entity, err := applySQLiteOccurrenceMutation(ctx, tx, workspaceID, mutation)
		if err != nil {
			return nil, err
		}
		result := &model.MobileMutationResult{MutationID: mutation.MutationID, Status: model.MobileMutationApplied, Entity: entity}
		if err := persistSQLiteEntityMutationResult(ctx, tx, workspaceID, mutation, result); err != nil {
			return nil, err
		}
		return result, nil
	}
	var entity *model.MobileEntityEnvelope
	var err error
	switch {
	case strings.HasSuffix(mutation.Operation, ".create"):
		entity, err = createSQLiteMobileEntity(ctx, tx, workspaceID, mutation)
	case strings.HasSuffix(mutation.Operation, ".update"):
		entity, err = updateSQLiteMobileEntity(ctx, tx, workspaceID, mutation)
	case strings.HasSuffix(mutation.Operation, ".delete"):
		entity, err = deleteSQLiteMobileEntity(ctx, tx, workspaceID, mutation)
	default:
		err = fmt.Errorf("unsupported mobile operation %q", mutation.Operation)
	}
	if err != nil {
		return nil, err
	}
	result := &model.MobileMutationResult{MutationID: mutation.MutationID, Status: model.MobileMutationApplied, Entity: entity}
	if err := persistSQLiteEntityMutationResult(ctx, tx, workspaceID, mutation, result); err != nil {
		return nil, err
	}
	return result, nil
}

func createSQLiteMobileEntity(ctx context.Context, tx *sql.Tx, workspaceID string, mutation model.MobileEntityMutation) (*model.MobileEntityEnvelope, error) {
	var retired int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM mobile_retired_ids
		WHERE workspace_id = ? AND entity_type = ? AND client_id = ?
	`, workspaceID, mutation.EntityType, mutation.EntityClientID).Scan(&retired); err != nil {
		return nil, err
	}
	if retired > 0 {
		return nil, storage.ErrMobileEntityGone
	}
	now := nowUnix()
	entityID := uuid.NewString()
	switch mutation.EntityType {
	case "task":
		var payload struct {
			Title       string  `json:"title"`
			Content     string  `json:"content"`
			ProjectID   *string `json:"project_id"`
			Due         *int64  `json:"due"`
			PlannedDate *string `json:"planned_date"`
			Priority    int     `json:"priority"`
		}
		if err := decodeEntityPayload(mutation.Payload, &payload); err != nil || strings.TrimSpace(payload.Title) == "" {
			return nil, errors.New("task.create requires a title")
		}
		projectID := "personal"
		if payload.ProjectID != nil && *payload.ProjectID != "" {
			projectID = *payload.ProjectID
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO tasks (
				id, client_id, revision, title, content, project_id, due, planned_date, priority,
				done, status, horizon, scope, sort_order, execution_type, created_at, updated_at, workspace_id
			) VALUES (?, ?, 1, ?, ?, ?, ?, ?, ?, 0, 'open', 'week', 'daily', 0, 'single', ?, ?, ?)
		`, entityID, mutation.EntityClientID, strings.TrimSpace(payload.Title), payload.Content, projectID,
			payload.Due, payload.PlannedDate, payload.Priority, now, now, workspaceID); err != nil {
			return nil, err
		}
	case "event":
		var payload struct {
			Title     string  `json:"title"`
			StartTime int64   `json:"start_time"`
			EndTime   int64   `json:"end_time"`
			Location  *string `json:"location"`
			Kind      string  `json:"kind"`
		}
		if err := decodeEntityPayload(mutation.Payload, &payload); err != nil || strings.TrimSpace(payload.Title) == "" || payload.EndTime <= payload.StartTime {
			return nil, errors.New("event.create requires title and a valid time range")
		}
		if payload.Kind == "" {
			payload.Kind = "work"
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO events (
				id, client_id, revision, title, start_time, end_time, location, kind, created_at, updated_at, workspace_id
			) VALUES (?, ?, 1, ?, ?, ?, ?, ?, ?, ?, ?)
		`, entityID, mutation.EntityClientID, strings.TrimSpace(payload.Title), payload.StartTime, payload.EndTime,
			payload.Location, payload.Kind, now, now, workspaceID); err != nil {
			return nil, err
		}
	case "inbox":
		var payload struct {
			Kind  string  `json:"kind"`
			Title string  `json:"title"`
			Body  *string `json:"body"`
		}
		if err := decodeEntityPayload(mutation.Payload, &payload); err != nil || strings.TrimSpace(payload.Kind) == "" || strings.TrimSpace(payload.Title) == "" {
			return nil, errors.New("inbox.create requires kind and title")
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO inbox (
				id, client_id, revision, kind, title, body, source, archived, created_at, updated_at, workspace_id
			) VALUES (?, ?, 1, ?, ?, ?, 'quick-capture', 0, ?, ?, ?)
		`, entityID, mutation.EntityClientID, payload.Kind, strings.TrimSpace(payload.Title), payload.Body, now, now, workspaceID); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported mobile entity type %q", mutation.EntityType)
	}
	return getSQLiteMobileEntity(ctx, tx, workspaceID, mutation.EntityType, mutation.EntityClientID)
}

func updateSQLiteMobileEntity(ctx context.Context, tx *sql.Tx, workspaceID string, mutation model.MobileEntityMutation) (*model.MobileEntityEnvelope, error) {
	current, err := loadSQLiteMobileEntityIdentity(ctx, tx, workspaceID, mutation.EntityType, mutation.EntityClientID)
	if err != nil {
		return nil, err
	}
	if current.deletedAt.Valid {
		return nil, storage.ErrMobileEntityGone
	}
	if mutation.BaseRevision == nil || current.revision != *mutation.BaseRevision {
		return nil, storage.ErrRevisionConflict
	}
	now := nowUnix()
	switch mutation.EntityType {
	case "task":
		var payload struct {
			Title    *string `json:"title"`
			Content  *string `json:"content"`
			Priority *int    `json:"priority"`
			Done     *int    `json:"done"`
			Status   *string `json:"status"`
		}
		if err := decodeEntityPayload(mutation.Payload, &payload); err != nil {
			return nil, err
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE tasks SET
				title = COALESCE(?, title), content = COALESCE(?, content), priority = COALESCE(?, priority),
				done = COALESCE(?, done), status = COALESCE(?, status), revision = revision + 1, updated_at = ?
			WHERE workspace_id = ? AND client_id = ? AND revision = ? AND deleted_at IS NULL
		`, payload.Title, payload.Content, payload.Priority, payload.Done, payload.Status, now,
			workspaceID, mutation.EntityClientID, current.revision)
		if err != nil {
			return nil, err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return nil, storage.ErrRevisionConflict
		}
	case "event":
		var payload struct {
			Title     *string `json:"title"`
			StartTime *int64  `json:"start_time"`
			EndTime   *int64  `json:"end_time"`
			Location  *string `json:"location"`
			Kind      *string `json:"kind"`
		}
		if err := decodeEntityPayload(mutation.Payload, &payload); err != nil {
			return nil, err
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE events SET
				title = COALESCE(?, title), start_time = COALESCE(?, start_time), end_time = COALESCE(?, end_time),
				location = COALESCE(?, location), kind = COALESCE(?, kind), revision = revision + 1, updated_at = ?
			WHERE workspace_id = ? AND client_id = ? AND revision = ? AND deleted_at IS NULL
		`, payload.Title, payload.StartTime, payload.EndTime, payload.Location, payload.Kind, now,
			workspaceID, mutation.EntityClientID, current.revision)
		if err != nil {
			return nil, err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return nil, storage.ErrRevisionConflict
		}
	case "inbox":
		var payload struct {
			Title    *string `json:"title"`
			Body     *string `json:"body"`
			Archived *int    `json:"archived"`
		}
		if err := decodeEntityPayload(mutation.Payload, &payload); err != nil {
			return nil, err
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE inbox SET title = COALESCE(?, title), body = COALESCE(?, body), archived = COALESCE(?, archived),
				revision = revision + 1, updated_at = ?
			WHERE workspace_id = ? AND client_id = ? AND revision = ? AND deleted_at IS NULL
		`, payload.Title, payload.Body, payload.Archived, now, workspaceID, mutation.EntityClientID, current.revision)
		if err != nil {
			return nil, err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return nil, storage.ErrRevisionConflict
		}
	}
	return getSQLiteMobileEntity(ctx, tx, workspaceID, mutation.EntityType, mutation.EntityClientID)
}

func deleteSQLiteMobileEntity(ctx context.Context, tx *sql.Tx, workspaceID string, mutation model.MobileEntityMutation) (*model.MobileEntityEnvelope, error) {
	current, err := loadSQLiteMobileEntityIdentity(ctx, tx, workspaceID, mutation.EntityType, mutation.EntityClientID)
	if err != nil {
		return nil, err
	}
	if current.deletedAt.Valid {
		return nil, storage.ErrMobileEntityGone
	}
	if mutation.BaseRevision == nil || current.revision != *mutation.BaseRevision {
		return nil, storage.ErrRevisionConflict
	}
	table, err := sqliteMobileEntityTable(mutation.EntityType)
	if err != nil {
		return nil, err
	}
	now := nowUnix()
	if mutation.EntityType == "task" {
		if err := tombstoneSQLiteTaskOccurrences(ctx, tx, workspaceID, current.id, now); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM task_recurrence_rules WHERE workspace_id = ? AND task_id = ?`, workspaceID, current.id); err != nil {
			return nil, err
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE `+table+`
		SET deleted_at = ?, revision = revision + 1, updated_at = ?
		WHERE workspace_id = ? AND client_id = ? AND revision = ? AND deleted_at IS NULL`,
		now, now, workspaceID, mutation.EntityClientID, current.revision)
	if err != nil {
		return nil, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return nil, storage.ErrRevisionConflict
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO mobile_retired_ids (workspace_id, entity_type, client_id, retired_at)
		VALUES (?, ?, ?, ?)
	`, workspaceID, mutation.EntityType, mutation.EntityClientID, now); err != nil {
		return nil, err
	}
	return getSQLiteMobileEntity(ctx, tx, workspaceID, mutation.EntityType, mutation.EntityClientID)
}

func tombstoneSQLiteTaskOccurrences(ctx context.Context, tx *sql.Tx, workspaceID, taskID string, now int64) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT occurrence_id FROM task_occurrences
		WHERE workspace_id = ? AND task_id = ? AND occurrence_id IS NOT NULL AND deleted_at IS NULL
		ORDER BY occurrence_id
	`, workspaceID, taskID)
	if err != nil {
		return err
	}
	occurrenceIDs := make([]string, 0)
	for rows.Next() {
		var occurrenceID string
		if err := rows.Scan(&occurrenceID); err != nil {
			_ = rows.Close()
			return err
		}
		occurrenceIDs = append(occurrenceIDs, occurrenceID)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, occurrenceID := range occurrenceIDs {
		if _, err := tx.ExecContext(ctx, `
			UPDATE task_occurrences SET deleted_at = ?, updated_at = ?, revision = revision + 1
			WHERE workspace_id = ? AND occurrence_id = ? AND deleted_at IS NULL
		`, now, now, workspaceID, occurrenceID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO mobile_retired_ids (workspace_id, entity_type, client_id, retired_at)
			VALUES (?, 'task_occurrence', ?, ?)
		`, workspaceID, occurrenceID, now); err != nil {
			return err
		}
		if err := persistSQLiteServerEntityChange(ctx, tx, workspaceID, uuid.NewString(), "task_occurrence", "task_occurrence.server_deleted", occurrenceID, now); err != nil {
			return err
		}
	}
	return nil
}

type sqliteMobileEntityIdentity struct {
	id        string
	revision  int64
	deletedAt sql.NullInt64
}

func loadSQLiteMobileEntityIdentity(ctx context.Context, runner sqliteRunner, workspaceID, entityType, clientID string) (*sqliteMobileEntityIdentity, error) {
	table, err := sqliteMobileEntityTable(entityType)
	if err != nil {
		return nil, err
	}
	var identity sqliteMobileEntityIdentity
	err = runner.QueryRowContext(ctx, `SELECT id, revision, deleted_at FROM `+table+` WHERE workspace_id = ? AND client_id = ?`,
		workspaceID, clientID).Scan(&identity.id, &identity.revision, &identity.deletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storage.ErrMobileEntityNotFound
	}
	return &identity, err
}

func getSQLiteMobileEntity(ctx context.Context, runner sqliteRunner, workspaceID, entityType, clientID string) (*model.MobileEntityEnvelope, error) {
	if entityType == "voice_note" {
		return getSQLiteMobileVoiceNote(ctx, runner, workspaceID, clientID)
	}
	if entityType == "task_occurrence" {
		return getSQLiteMobileOccurrence(ctx, runner, workspaceID, clientID)
	}
	identity, err := loadSQLiteMobileEntityIdentity(ctx, runner, workspaceID, entityType, clientID)
	if err != nil {
		return nil, err
	}
	var payload []byte
	switch entityType {
	case "task":
		var title, content, status, executionType string
		var priority, done int
		var due sql.NullInt64
		var plannedDate sql.NullString
		if err := runner.QueryRowContext(ctx, `
			SELECT title, content, priority, done, status, due, planned_date, execution_type
			FROM tasks WHERE workspace_id = ? AND client_id = ?
		`, workspaceID, clientID).Scan(&title, &content, &priority, &done, &status, &due, &plannedDate, &executionType); err != nil {
			return nil, err
		}
		payload, err = json.Marshal(map[string]any{
			"title": title, "content": content, "priority": priority, "done": done, "status": status,
			"due": nullableSQLiteMobileEntityInt64(due), "planned_date": nullableMobileEntityString(plannedDate), "execution_type": executionType,
		})
	case "event":
		var title, kind string
		var startTime, endTime int64
		var location sql.NullString
		if err := runner.QueryRowContext(ctx, `
			SELECT title, start_time, end_time, location, kind FROM events WHERE workspace_id = ? AND client_id = ?
		`, workspaceID, clientID).Scan(&title, &startTime, &endTime, &location, &kind); err != nil {
			return nil, err
		}
		payload, err = json.Marshal(map[string]any{"title": title, "start_time": startTime, "end_time": endTime, "location": nullableMobileEntityString(location), "kind": kind})
	case "inbox":
		var kind, title string
		var body sql.NullString
		var archived int
		if err := runner.QueryRowContext(ctx, `
			SELECT kind, title, body, archived FROM inbox WHERE workspace_id = ? AND client_id = ?
		`, workspaceID, clientID).Scan(&kind, &title, &body, &archived); err != nil {
			return nil, err
		}
		payload, err = json.Marshal(map[string]any{"kind": kind, "title": title, "body": nullableMobileEntityString(body), "archived": archived})
	default:
		return nil, fmt.Errorf("unsupported mobile entity type %q", entityType)
	}
	if err != nil {
		return nil, err
	}
	entity := &model.MobileEntityEnvelope{
		EntityType: entityType, ID: identity.id, ClientID: clientID, Revision: identity.revision, Payload: payload,
	}
	if identity.deletedAt.Valid {
		deletedAt := identity.deletedAt.Int64
		entity.DeletedAt = &deletedAt
	}
	return entity, nil
}

func getSQLiteEntityMutationReceipt(ctx context.Context, tx *sql.Tx, workspaceID string, mutation model.MobileEntityMutation) (*model.MobileMutationResult, bool, error) {
	var requestHash, responseJSON string
	err := tx.QueryRowContext(ctx, `
		SELECT request_sha256, response_json FROM mobile_mutation_receipts
		WHERE workspace_id = ? AND device_client_id = ? AND mutation_id = ?
	`, workspaceID, mutation.DeviceClientID, mutation.MutationID).Scan(&requestHash, &responseJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if requestHash != mutation.RequestSHA256 {
		return nil, false, storage.ErrMutationIDReused
	}
	var result model.MobileMutationResult
	if err := json.Unmarshal([]byte(responseJSON), &result); err != nil {
		return nil, false, err
	}
	return &result, true, nil
}

func persistSQLiteEntityMutationResult(ctx context.Context, tx *sql.Tx, workspaceID string, mutation model.MobileEntityMutation, result *model.MobileMutationResult) error {
	responseJSON, err := json.Marshal(result)
	if err != nil {
		return err
	}
	entityJSON, err := json.Marshal(result.Entity)
	if err != nil {
		return err
	}
	now := nowUnix()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO mobile_sync_outbox (
			workspace_id, mutation_id, entity_type, entity_client_id, operation, revision, entity_json, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, workspaceID, mutation.MutationID, mutation.EntityType, mutation.EntityClientID, mutation.Operation,
		result.Entity.Revision, string(entityJSON), now); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO mobile_mutation_receipts (
			workspace_id, device_client_id, mutation_id, request_sha256, response_json, created_at
		) VALUES (?, ?, ?, ?, ?, ?)
	`, workspaceID, mutation.DeviceClientID, mutation.MutationID, mutation.RequestSHA256, string(responseJSON), now)
	return err
}

func persistSQLiteServerEntityChange(ctx context.Context, tx *sql.Tx, workspaceID, mutationID, entityType, operation, clientID string, now int64) error {
	entity, err := getSQLiteMobileEntity(ctx, tx, workspaceID, entityType, clientID)
	if err != nil {
		return err
	}
	entityJSON, err := json.Marshal(entity)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO mobile_sync_outbox (
			workspace_id, mutation_id, entity_type, entity_client_id, operation, revision, entity_json, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, workspaceID, mutationID, entityType, clientID, operation, entity.Revision, string(entityJSON), now)
	return err
}

func validateMobileEntityMutation(mutation model.MobileEntityMutation) error {
	if mutation.EntityType != "task" && mutation.EntityType != "event" && mutation.EntityType != "inbox" && mutation.EntityType != "task_occurrence" && mutation.EntityType != "voice_note" {
		return fmt.Errorf("unsupported mobile entity type %q", mutation.EntityType)
	}
	if mutation.MutationID == "" || mutation.DeviceClientID == "" || mutation.EntityClientID == "" || mutation.RequestSHA256 == "" {
		return errors.New("mobile entity mutation identity is incomplete")
	}
	expectedPrefix := mutation.EntityType + "."
	if mutation.EntityType == "voice_note" {
		if mutation.Operation != "voice.create" && mutation.Operation != "voice_audio.delete" && mutation.Operation != "voice_note.delete" {
			return errors.New("mobile mutation operation does not match entity type")
		}
		return nil
	}
	if !strings.HasPrefix(mutation.Operation, expectedPrefix) {
		return errors.New("mobile mutation operation does not match entity type")
	}
	return nil
}

func applySQLiteVoiceMutation(ctx context.Context, tx *sql.Tx, workspaceID string, mutation model.MobileEntityMutation) (*model.MobileEntityEnvelope, error) {
	switch mutation.Operation {
	case "voice.create":
		return createSQLiteVoiceNote(ctx, tx, workspaceID, mutation)
	case "voice_audio.delete":
		return deleteSQLiteVoiceAudio(ctx, tx, workspaceID, mutation)
	case "voice_note.delete":
		return deleteSQLiteVoiceNote(ctx, tx, workspaceID, mutation)
	default:
		return nil, fmt.Errorf("unsupported mobile operation %q", mutation.Operation)
	}
}

func createSQLiteVoiceNote(ctx context.Context, tx *sql.Tx, workspaceID string, mutation model.MobileEntityMutation) (*model.MobileEntityEnvelope, error) {
	var payload struct {
		Title      string `json:"title"`
		DurationMS int64  `json:"duration_ms"`
		RecordedAt int64  `json:"recorded_at"`
		Language   string `json:"language"`
	}
	if err := decodeEntityPayload(mutation.Payload, &payload); err != nil || payload.DurationMS < 0 {
		return nil, errors.New("voice.create contains invalid metadata")
	}
	var retired int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM mobile_retired_ids WHERE workspace_id = ? AND entity_type = 'voice_note' AND client_id = ?`, workspaceID, mutation.EntityClientID).Scan(&retired); err != nil {
		return nil, err
	}
	if retired > 0 {
		return nil, storage.ErrMobileEntityGone
	}
	var existing int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM voice_notes WHERE workspace_id = ? AND client_id = ?`, workspaceID, mutation.EntityClientID).Scan(&existing); err != nil {
		return nil, err
	}
	if existing > 0 {
		return nil, storage.ErrAlreadyExists
	}
	now := nowUnix()
	if payload.RecordedAt <= 0 {
		payload.RecordedAt = now
	}
	payload.Title = strings.TrimSpace(payload.Title)
	if payload.Title == "" {
		payload.Title = "Voice note"
	}
	noteID := uuid.NewString()
	noteClientID := deterministicSQLiteMobileNoteClientID(workspaceID, noteID)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO notes (id, client_id, revision, title, body, folder_id, tags, created_at, updated_at, workspace_id)
		VALUES (?, ?, 1, ?, '', '__uncategorized', '["voice"]', ?, ?, ?)
	`, noteID, noteClientID, payload.Title, payload.RecordedAt, now, workspaceID); err != nil {
		return nil, err
	}
	if err := persistSQLiteServerNoteChange(ctx, tx, workspaceID, uuid.NewString(), model.MobileOperationNoteServerCreated, noteClientID, now); err != nil {
		return nil, err
	}
	voiceID := uuid.NewString()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO voice_notes (
			id, workspace_id, client_id, revision, audio_revision, audio_state, note_id, duration_ms, recorded_at, language,
			object_key, mime_type, audio_size, audio_sha256, upload_state,
			transcription_state, transcription_error, created_at, updated_at
		) VALUES (?, ?, ?, 1, 1, 'absent', ?, ?, ?, ?, '', '', 0, '', 'pending', 'not_started', '', ?, ?)
	`, voiceID, workspaceID, mutation.EntityClientID, noteID, payload.DurationMS, payload.RecordedAt, strings.TrimSpace(payload.Language), now, now); err != nil {
		return nil, err
	}
	return getSQLiteMobileVoiceNote(ctx, tx, workspaceID, mutation.EntityClientID)
}

func deleteSQLiteVoiceAudio(ctx context.Context, tx *sql.Tx, workspaceID string, mutation model.MobileEntityMutation) (*model.MobileEntityEnvelope, error) {
	var revision int64
	var objectKey string
	var deletedAt sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT revision, object_key, deleted_at FROM voice_notes WHERE workspace_id = ? AND client_id = ?`, workspaceID, mutation.EntityClientID).
		Scan(&revision, &objectKey, &deletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storage.ErrMobileEntityNotFound
	}
	if err != nil {
		return nil, err
	}
	if deletedAt.Valid {
		return nil, storage.ErrMobileEntityGone
	}
	if mutation.BaseRevision == nil || *mutation.BaseRevision != revision {
		return nil, storage.ErrRevisionConflict
	}
	now := nowUnix()
	audioState := model.VoiceAudioDeleted
	if objectKey != "" {
		audioState = model.VoiceAudioDeleteRequested
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE voice_notes SET audio_state = ?, audio_revision = audio_revision + 1,
			object_key = CASE WHEN ? = '' THEN '' ELSE object_key END,
			mime_type = CASE WHEN ? = '' THEN '' ELSE mime_type END,
			audio_size = CASE WHEN ? = '' THEN 0 ELSE audio_size END,
			audio_sha256 = CASE WHEN ? = '' THEN '' ELSE audio_sha256 END,
			upload_state = CASE WHEN ? = '' THEN 'failed' ELSE upload_state END,
			revision = revision + 1, updated_at = ?
		WHERE workspace_id = ? AND client_id = ? AND revision = ? AND deleted_at IS NULL
	`, audioState, objectKey, objectKey, objectKey, objectKey, objectKey, now, workspaceID, mutation.EntityClientID, revision)
	if err != nil {
		return nil, err
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		if err != nil {
			return nil, err
		}
		return nil, storage.ErrRevisionConflict
	}
	if err := cancelSQLiteVoiceTranscriptionJobs(ctx, tx, workspaceID, mutation.EntityClientID, now); err != nil {
		return nil, err
	}
	if objectKey != "" {
		if err := enqueueSQLiteVoiceAudioCleanup(ctx, tx, workspaceID, mutation.EntityClientID, objectKey, now); err != nil {
			return nil, err
		}
	}
	return getSQLiteMobileVoiceNote(ctx, tx, workspaceID, mutation.EntityClientID)
}

func deleteSQLiteVoiceNote(ctx context.Context, tx *sql.Tx, workspaceID string, mutation model.MobileEntityMutation) (*model.MobileEntityEnvelope, error) {
	var revision int64
	var noteID string
	var objectKey string
	var deletedAt sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT revision, note_id, object_key, deleted_at FROM voice_notes WHERE workspace_id = ? AND client_id = ?`, workspaceID, mutation.EntityClientID).
		Scan(&revision, &noteID, &objectKey, &deletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		if mutation.BaseRevision == nil || *mutation.BaseRevision != 0 {
			return nil, storage.ErrMobileEntityNotFound
		}
		return pretombstoneSQLiteVoiceNote(ctx, tx, workspaceID, mutation.EntityClientID)
	}
	if err != nil {
		return nil, err
	}
	if deletedAt.Valid {
		return nil, storage.ErrMobileEntityGone
	}
	if mutation.BaseRevision == nil || *mutation.BaseRevision != revision {
		return nil, storage.ErrRevisionConflict
	}
	now := nowUnix()
	audioState := model.VoiceAudioDeleted
	if objectKey != "" {
		audioState = model.VoiceAudioDeleteRequested
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE voice_notes SET deleted_at = ?, audio_state = ?, audio_revision = audio_revision + 1,
			object_key = CASE WHEN ? = '' THEN '' ELSE object_key END,
			mime_type = CASE WHEN ? = '' THEN '' ELSE mime_type END,
			audio_size = CASE WHEN ? = '' THEN 0 ELSE audio_size END,
			audio_sha256 = CASE WHEN ? = '' THEN '' ELSE audio_sha256 END,
			upload_state = CASE WHEN ? = '' THEN 'failed' ELSE upload_state END,
			revision = revision + 1, updated_at = ?
		WHERE workspace_id = ? AND client_id = ? AND revision = ? AND deleted_at IS NULL
	`, now, audioState, objectKey, objectKey, objectKey, objectKey, objectKey, now, workspaceID, mutation.EntityClientID, revision)
	if err != nil {
		return nil, err
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		if err != nil {
			return nil, err
		}
		return nil, storage.ErrRevisionConflict
	}
	if err := cancelSQLiteVoiceTranscriptionJobs(ctx, tx, workspaceID, mutation.EntityClientID, now); err != nil {
		return nil, err
	}
	if objectKey != "" {
		if err := enqueueSQLiteVoiceAudioCleanup(ctx, tx, workspaceID, mutation.EntityClientID, objectKey, now); err != nil {
			return nil, err
		}
	}
	if err := (noteRepository{db: tx}).Delete(ctx, noteID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO mobile_retired_ids (workspace_id, entity_type, client_id, retired_at) VALUES (?, 'voice_note', ?, ?)`, workspaceID, mutation.EntityClientID, now); err != nil {
		return nil, err
	}
	return getSQLiteMobileVoiceNote(ctx, tx, workspaceID, mutation.EntityClientID)
}

func pretombstoneSQLiteVoiceNote(ctx context.Context, tx *sql.Tx, workspaceID, clientID string) (*model.MobileEntityEnvelope, error) {
	now := nowUnix()
	noteID := uuid.NewString()
	noteClientID := deterministicSQLiteMobileNoteClientID(workspaceID, noteID)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO notes (id, client_id, revision, deleted_at, title, body, folder_id, tags, created_at, updated_at, workspace_id)
		VALUES (?, ?, 1, ?, '[deleted voice]', '', '__uncategorized', '["voice"]', ?, ?, ?)
	`, noteID, noteClientID, now, now, now, workspaceID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO voice_notes (
			id, workspace_id, client_id, revision, deleted_at, audio_revision, audio_state, note_id,
			duration_ms, recorded_at, language, object_key, mime_type, audio_size, audio_sha256,
			upload_state, transcription_state, transcription_error, created_at, updated_at
		) VALUES (?, ?, ?, 1, ?, 1, 'deleted', ?, 0, ?, '', '', '', 0, '', 'failed', 'not_started', '', ?, ?)
	`, uuid.NewString(), workspaceID, clientID, now, noteID, now, now, now); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO mobile_retired_ids (workspace_id, entity_type, client_id, retired_at) VALUES (?, 'voice_note', ?, ?)`, workspaceID, clientID, now); err != nil {
		return nil, err
	}
	return getSQLiteMobileVoiceNote(ctx, tx, workspaceID, clientID)
}

func cancelSQLiteVoiceTranscriptionJobs(ctx context.Context, tx *sql.Tx, workspaceID, clientID string, now int64) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE transcription_jobs SET state = 'canceled', revision = revision + 1, error_code = 'voice_audio_deleted',
			next_attempt_at = NULL, lease_owner = '', lease_token = '', lease_expires_at = NULL, heartbeat_at = NULL, updated_at = ?
		WHERE workspace_id = ? AND voice_note_id = ? AND state IN ('waiting_for_audio', 'queued', 'processing', 'retry_waiting')
	`, now, workspaceID, clientID)
	return err
}

func enqueueSQLiteVoiceAudioCleanup(ctx context.Context, tx *sql.Tx, workspaceID, clientID, objectKey string, now int64) error {
	_, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO voice_audio_cleanup_jobs (
			job_id, workspace_id, voice_note_id, object_key, state, revision, attempt, max_attempts,
			error_code, next_attempt_at, lease_owner, lease_token, created_at, updated_at
		) VALUES (?, ?, ?, ?, 'retry_waiting', 1, 0, 6, '', ?, '', '', ?, ?)
	`, uuid.NewString(), workspaceID, clientID, objectKey, now+600, now, now)
	return err
}

func getSQLiteMobileVoiceNote(ctx context.Context, runner sqliteRunner, workspaceID, clientID string) (*model.MobileEntityEnvelope, error) {
	var id, noteID, title, body, language, uploadState, audioState, transcriptionState, transcriptionError string
	var revision, audioRevision, durationMS, recordedAt, audioSize int64
	var deletedAt sql.NullInt64
	err := runner.QueryRowContext(ctx, `
		SELECT v.id, v.note_id, n.title, n.body, v.revision, v.deleted_at,
			v.duration_ms, v.recorded_at, v.language, v.audio_size, v.upload_state, v.audio_state, v.audio_revision,
			v.transcription_state, v.transcription_error
		FROM voice_notes v
		JOIN notes n ON n.workspace_id = v.workspace_id AND n.id = v.note_id
		WHERE v.workspace_id = ? AND v.client_id = ?
	`, workspaceID, clientID).Scan(
		&id, &noteID, &title, &body, &revision, &deletedAt, &durationMS, &recordedAt, &language,
		&audioSize, &uploadState, &audioState, &audioRevision, &transcriptionState, &transcriptionError,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storage.ErrMobileEntityNotFound
	}
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(map[string]any{
		"note_id": noteID, "title": title, "body": body, "duration_ms": durationMS,
		"recorded_at": recordedAt, "language": language, "audio_size": audioSize,
		"upload_state": uploadState, "transcription_state": transcriptionState,
		"audio_state": audioState, "audio_revision": audioRevision,
		"transcription_error": transcriptionError,
	})
	if err != nil {
		return nil, err
	}
	entity := &model.MobileEntityEnvelope{EntityType: "voice_note", ID: id, ClientID: clientID, Revision: revision, Payload: payload}
	if deletedAt.Valid {
		value := deletedAt.Int64
		entity.DeletedAt = &value
	}
	return entity, nil
}

func applySQLiteOccurrenceMutation(ctx context.Context, tx *sql.Tx, workspaceID string, mutation model.MobileEntityMutation) (*model.MobileEntityEnvelope, error) {
	switch mutation.Operation {
	case "task_occurrence.complete":
		var payload struct {
			TaskID         string `json:"task_id"`
			OccurrenceDate string `json:"occurrence_date"`
			CompletedAt    int64  `json:"completed_at"`
		}
		if err := decodeEntityPayload(mutation.Payload, &payload); err != nil || payload.TaskID == "" || payload.OccurrenceDate == "" || payload.CompletedAt <= 0 {
			return nil, errors.New("task_occurrence.complete requires task_id, occurrence_date, and completed_at")
		}
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_occurrences WHERE workspace_id = ? AND occurrence_id = ?`, workspaceID, mutation.EntityClientID).Scan(&exists); err != nil {
			return nil, err
		}
		now := nowUnix()
		if exists == 0 {
			var taskExists bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM tasks WHERE workspace_id = ? AND id = ? AND deleted_at IS NULL)`, workspaceID, payload.TaskID).Scan(&taskExists); err != nil {
				return nil, err
			}
			if !taskExists {
				return nil, storage.ErrMobileEntityNotFound
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO task_occurrences (
					task_id, occurrence_date, occurrence_id, revision, status, completed_at, created_at, updated_at, workspace_id
				) VALUES (?, ?, ?, 1, 'done', ?, ?, ?, ?)
			`, payload.TaskID, payload.OccurrenceDate, mutation.EntityClientID, payload.CompletedAt, now, now, workspaceID); err != nil {
				return nil, err
			}
		} else {
			if mutation.BaseRevision == nil {
				return nil, storage.ErrRevisionConflict
			}
			result, err := tx.ExecContext(ctx, `
				UPDATE task_occurrences SET status = 'done', completed_at = ?, revision = revision + 1, updated_at = ?
				WHERE workspace_id = ? AND occurrence_id = ? AND revision = ? AND deleted_at IS NULL
			`, payload.CompletedAt, now, workspaceID, mutation.EntityClientID, *mutation.BaseRevision)
			if err != nil {
				return nil, err
			}
			if affected, err := result.RowsAffected(); err != nil || affected != 1 {
				if err != nil {
					return nil, err
				}
				return nil, storage.ErrRevisionConflict
			}
		}
	case "task_occurrence.reopen":
		if mutation.BaseRevision == nil {
			return nil, storage.ErrRevisionConflict
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE task_occurrences SET status = 'open', completed_at = NULL, revision = revision + 1, updated_at = ?
			WHERE workspace_id = ? AND occurrence_id = ? AND revision = ? AND deleted_at IS NULL
		`, nowUnix(), workspaceID, mutation.EntityClientID, *mutation.BaseRevision)
		if err != nil {
			return nil, err
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			if err != nil {
				return nil, err
			}
			return nil, storage.ErrRevisionConflict
		}
	default:
		return nil, fmt.Errorf("unsupported mobile operation %q", mutation.Operation)
	}
	return getSQLiteMobileOccurrence(ctx, tx, workspaceID, mutation.EntityClientID)
}

func getSQLiteMobileOccurrence(ctx context.Context, runner sqliteRunner, workspaceID, occurrenceID string) (*model.MobileEntityEnvelope, error) {
	var taskID, occurrenceDate, status, note string
	var revision int64
	var completedAt, deletedAt sql.NullInt64
	err := runner.QueryRowContext(ctx, `
		SELECT task_id, occurrence_date, status, completed_at, note, revision, deleted_at
		FROM task_occurrences WHERE workspace_id = ? AND occurrence_id = ?
	`, workspaceID, occurrenceID).Scan(&taskID, &occurrenceDate, &status, &completedAt, &note, &revision, &deletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storage.ErrMobileEntityNotFound
	}
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(map[string]any{
		"task_id": taskID, "occurrence_date": occurrenceDate, "status": status,
		"completed_at": nullableSQLiteMobileEntityInt64(completedAt), "note": note,
	})
	if err != nil {
		return nil, err
	}
	entity := &model.MobileEntityEnvelope{EntityType: "task_occurrence", ID: occurrenceID, ClientID: occurrenceID, Revision: revision, Payload: payload}
	if deletedAt.Valid {
		value := deletedAt.Int64
		entity.DeletedAt = &value
	}
	return entity, nil
}

func nullableSQLiteMobileEntityInt64(value sql.NullInt64) any {
	if !value.Valid {
		return nil
	}
	return value.Int64
}

func sqliteMobileEntityTable(entityType string) (string, error) {
	switch entityType {
	case "task":
		return "tasks", nil
	case "event":
		return "events", nil
	case "inbox":
		return "inbox", nil
	default:
		return "", fmt.Errorf("unsupported mobile entity type %q", entityType)
	}
}

func decodeEntityPayload(raw json.RawMessage, destination any) error {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	return json.Unmarshal(raw, destination)
}

func nullableMobileEntityString(value sql.NullString) any {
	if !value.Valid {
		return nil
	}
	return value.String
}
