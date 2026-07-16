package postgres

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

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
	if err := validatePostgresMobileEntityMutation(mutation); err != nil {
		return nil, err
	}
	var result *model.MobileMutationResult
	err = r.withTx(ctx, func(tx *sql.Tx) error {
		var applyErr error
		result, applyErr = applyPostgresEntityMutation(ctx, tx, workspaceID, mutation)
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
		return getPostgresMobileNote(ctx, r.db, workspaceID, clientID)
	}
	return getPostgresMobileEntity(ctx, r.db, workspaceID, entityType, clientID)
}

func applyPostgresEntityMutation(ctx context.Context, tx *sql.Tx, workspaceID string, mutation model.MobileEntityMutation) (*model.MobileMutationResult, error) {
	lockKey := fmt.Sprintf("%d:%s%d:%s%d:%s", len(workspaceID), workspaceID, len(mutation.DeviceClientID), mutation.DeviceClientID, len(mutation.MutationID), mutation.MutationID)
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1::text))`, lockKey); err != nil {
		return nil, fmt.Errorf("lock mobile mutation receipt: %w", err)
	}
	if receipt, found, err := getPostgresEntityMutationReceipt(ctx, tx, workspaceID, mutation); err != nil {
		return nil, err
	} else if found {
		return receipt, nil
	}
	if mutation.EntityType == "voice_note" {
		entity, err := applyPostgresVoiceMutation(ctx, tx, workspaceID, mutation)
		if err != nil {
			return nil, err
		}
		result := &model.MobileMutationResult{MutationID: mutation.MutationID, Status: model.MobileMutationApplied, Entity: entity}
		if err := persistPostgresEntityMutationResult(ctx, tx, workspaceID, mutation, result); err != nil {
			return nil, err
		}
		return result, nil
	}
	if mutation.EntityType == "task_occurrence" {
		entity, err := applyPostgresOccurrenceMutation(ctx, tx, workspaceID, mutation)
		if err != nil {
			return nil, err
		}
		result := &model.MobileMutationResult{MutationID: mutation.MutationID, Status: model.MobileMutationApplied, Entity: entity}
		if err := persistPostgresEntityMutationResult(ctx, tx, workspaceID, mutation, result); err != nil {
			return nil, err
		}
		return result, nil
	}

	var entity *model.MobileEntityEnvelope
	var err error
	switch {
	case strings.HasSuffix(mutation.Operation, ".create"):
		entity, err = createPostgresMobileEntity(ctx, tx, workspaceID, mutation)
	case strings.HasSuffix(mutation.Operation, ".update"):
		entity, err = updatePostgresMobileEntity(ctx, tx, workspaceID, mutation)
	case strings.HasSuffix(mutation.Operation, ".delete"):
		entity, err = deletePostgresMobileEntity(ctx, tx, workspaceID, mutation)
	default:
		err = fmt.Errorf("unsupported mobile operation %q", mutation.Operation)
	}
	if err != nil {
		return nil, err
	}
	result := &model.MobileMutationResult{MutationID: mutation.MutationID, Status: model.MobileMutationApplied, Entity: entity}
	if err := persistPostgresEntityMutationResult(ctx, tx, workspaceID, mutation, result); err != nil {
		return nil, err
	}
	return result, nil
}

func createPostgresMobileEntity(ctx context.Context, tx *sql.Tx, workspaceID string, mutation model.MobileEntityMutation) (*model.MobileEntityEnvelope, error) {
	var retired int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM mobile_retired_ids
		WHERE workspace_id = $1 AND entity_type = $2 AND client_id = $3
	`, workspaceID, mutation.EntityType, mutation.EntityClientID).Scan(&retired); err != nil {
		return nil, err
	}
	if retired > 0 {
		return nil, storage.ErrMobileEntityGone
	}

	now := time.Now().UTC()
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
		if err := decodePostgresEntityPayload(mutation.Payload, &payload); err != nil || strings.TrimSpace(payload.Title) == "" {
			return nil, errors.New("task.create requires a title")
		}
		projectID := "personal"
		if payload.ProjectID != nil && strings.TrimSpace(*payload.ProjectID) != "" {
			projectID = *payload.ProjectID
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO tasks (
				id, client_id, revision, title, content, project_id, due_at, planned_date, priority,
				done, status, horizon, scope, sort_order, execution_type, created_at, updated_at, workspace_id
			) VALUES ($1, $2, 1, $3, $4, $5, $6, $7::date, $8, false, 'open', 'week', 'daily', 0, 'single', $9, $9, $10)
		`, entityID, mutation.EntityClientID, strings.TrimSpace(payload.Title), payload.Content, projectID,
			postgresMobileUnixPtr(payload.Due), payload.PlannedDate, payload.Priority, now, workspaceID); err != nil {
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
		if err := decodePostgresEntityPayload(mutation.Payload, &payload); err != nil || strings.TrimSpace(payload.Title) == "" || payload.EndTime <= payload.StartTime {
			return nil, errors.New("event.create requires title and a valid time range")
		}
		if payload.Kind == "" {
			payload.Kind = "work"
		}
		startAt := time.Unix(payload.StartTime, 0).UTC()
		endAt := time.Unix(payload.EndTime, 0).UTC()
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO events (
				id, client_id, revision, title, start_at, end_at, time_range, location, kind, created_at, updated_at, workspace_id
			) VALUES ($1, $2, 1, $3, $4, $5, tstzrange($4, $5, '[)'), $6, $7, $8, $8, $9)
		`, entityID, mutation.EntityClientID, strings.TrimSpace(payload.Title), startAt, endAt,
			payload.Location, payload.Kind, now, workspaceID); err != nil {
			return nil, err
		}
	case "inbox":
		var payload struct {
			Kind  string  `json:"kind"`
			Title string  `json:"title"`
			Body  *string `json:"body"`
		}
		if err := decodePostgresEntityPayload(mutation.Payload, &payload); err != nil || strings.TrimSpace(payload.Kind) == "" || strings.TrimSpace(payload.Title) == "" {
			return nil, errors.New("inbox.create requires kind and title")
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO inbox (
				id, client_id, revision, kind, title, body, source, archived, created_at, updated_at, workspace_id
			) VALUES ($1, $2, 1, $3, $4, $5, 'quick-capture', false, $6, $6, $7)
		`, entityID, mutation.EntityClientID, payload.Kind, strings.TrimSpace(payload.Title), payload.Body, now, workspaceID); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported mobile entity type %q", mutation.EntityType)
	}
	if err := upsertPostgresMobileEntitySearch(ctx, tx, workspaceID, mutation.EntityType, mutation.EntityClientID); err != nil {
		return nil, err
	}
	return getPostgresMobileEntity(ctx, tx, workspaceID, mutation.EntityType, mutation.EntityClientID)
}

func updatePostgresMobileEntity(ctx context.Context, tx *sql.Tx, workspaceID string, mutation model.MobileEntityMutation) (*model.MobileEntityEnvelope, error) {
	current, err := loadPostgresMobileEntityIdentity(ctx, tx, workspaceID, mutation.EntityType, mutation.EntityClientID)
	if err != nil {
		return nil, err
	}
	if current.deletedAt.Valid {
		return nil, storage.ErrMobileEntityGone
	}
	if mutation.BaseRevision == nil || current.revision != *mutation.BaseRevision {
		return nil, storage.ErrRevisionConflict
	}
	now := time.Now().UTC()
	var result sql.Result
	switch mutation.EntityType {
	case "task":
		var payload struct {
			Title    *string `json:"title"`
			Content  *string `json:"content"`
			Priority *int    `json:"priority"`
			Done     *int    `json:"done"`
			Status   *string `json:"status"`
		}
		if err := decodePostgresEntityPayload(mutation.Payload, &payload); err != nil {
			return nil, err
		}
		var done *bool
		if payload.Done != nil {
			value := *payload.Done != 0
			done = &value
		}
		result, err = tx.ExecContext(ctx, `
			UPDATE tasks SET title = COALESCE($1, title), content = COALESCE($2, content),
				priority = COALESCE($3, priority), done = COALESCE($4, done), status = COALESCE($5, status),
				revision = revision + 1, updated_at = $6
			WHERE workspace_id = $7 AND client_id = $8 AND revision = $9 AND deleted_at IS NULL
		`, payload.Title, payload.Content, payload.Priority, done, payload.Status, now, workspaceID, mutation.EntityClientID, current.revision)
	case "event":
		var payload struct {
			Title     *string `json:"title"`
			StartTime *int64  `json:"start_time"`
			EndTime   *int64  `json:"end_time"`
			Location  *string `json:"location"`
			Kind      *string `json:"kind"`
		}
		if err := decodePostgresEntityPayload(mutation.Payload, &payload); err != nil {
			return nil, err
		}
		var currentStart, currentEnd time.Time
		if err := tx.QueryRowContext(ctx, `SELECT start_at, end_at FROM events WHERE workspace_id = $1 AND client_id = $2`, workspaceID, mutation.EntityClientID).Scan(&currentStart, &currentEnd); err != nil {
			return nil, err
		}
		startAt := currentStart.UTC()
		endAt := currentEnd.UTC()
		if payload.StartTime != nil {
			startAt = time.Unix(*payload.StartTime, 0).UTC()
		}
		if payload.EndTime != nil {
			endAt = time.Unix(*payload.EndTime, 0).UTC()
		}
		if !endAt.After(startAt) {
			return nil, errors.New("event update requires a valid time range")
		}
		result, err = tx.ExecContext(ctx, `
			UPDATE events SET title = COALESCE($1, title), start_at = $2, end_at = $3,
				time_range = tstzrange($2, $3, '[)'), location = COALESCE($4, location), kind = COALESCE($5, kind),
				revision = revision + 1, updated_at = $6
			WHERE workspace_id = $7 AND client_id = $8 AND revision = $9 AND deleted_at IS NULL
		`, payload.Title, startAt, endAt, payload.Location, payload.Kind, now, workspaceID, mutation.EntityClientID, current.revision)
	case "inbox":
		var payload struct {
			Title    *string `json:"title"`
			Body     *string `json:"body"`
			Archived *int    `json:"archived"`
		}
		if err := decodePostgresEntityPayload(mutation.Payload, &payload); err != nil {
			return nil, err
		}
		var archived *bool
		if payload.Archived != nil {
			value := *payload.Archived != 0
			archived = &value
		}
		result, err = tx.ExecContext(ctx, `
			UPDATE inbox SET title = COALESCE($1, title), body = COALESCE($2, body), archived = COALESCE($3, archived),
				revision = revision + 1, updated_at = $4
			WHERE workspace_id = $5 AND client_id = $6 AND revision = $7 AND deleted_at IS NULL
		`, payload.Title, payload.Body, archived, now, workspaceID, mutation.EntityClientID, current.revision)
	}
	if err != nil {
		return nil, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if rows != 1 {
		return nil, storage.ErrRevisionConflict
	}
	if err := upsertPostgresMobileEntitySearch(ctx, tx, workspaceID, mutation.EntityType, mutation.EntityClientID); err != nil {
		return nil, err
	}
	return getPostgresMobileEntity(ctx, tx, workspaceID, mutation.EntityType, mutation.EntityClientID)
}

func deletePostgresMobileEntity(ctx context.Context, tx *sql.Tx, workspaceID string, mutation model.MobileEntityMutation) (*model.MobileEntityEnvelope, error) {
	current, err := loadPostgresMobileEntityIdentity(ctx, tx, workspaceID, mutation.EntityType, mutation.EntityClientID)
	if err != nil {
		return nil, err
	}
	if current.deletedAt.Valid {
		return nil, storage.ErrMobileEntityGone
	}
	if mutation.BaseRevision == nil || current.revision != *mutation.BaseRevision {
		return nil, storage.ErrRevisionConflict
	}
	table, err := postgresMobileEntityTable(mutation.EntityType)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	if mutation.EntityType == "task" {
		if err := tombstonePostgresTaskOccurrences(ctx, tx, workspaceID, current.id, now); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM task_recurrence_rules WHERE workspace_id = $1 AND task_id = $2`, workspaceID, current.id); err != nil {
			return nil, err
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE `+table+`
		SET deleted_at = $1, revision = revision + 1, updated_at = $1
		WHERE workspace_id = $2 AND client_id = $3 AND revision = $4 AND deleted_at IS NULL`,
		now, workspaceID, mutation.EntityClientID, current.revision)
	if err != nil {
		return nil, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if rows != 1 {
		return nil, storage.ErrRevisionConflict
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO mobile_retired_ids (workspace_id, entity_type, client_id, retired_at)
		VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING
	`, workspaceID, mutation.EntityType, mutation.EntityClientID, now); err != nil {
		return nil, err
	}
	if mutation.EntityType == "task" || mutation.EntityType == "event" {
		if _, err := tx.ExecContext(ctx, `DELETE FROM search_index WHERE workspace_id = $1 AND entity_type = $2 AND entity_id = $3`, workspaceID, mutation.EntityType, current.id); err != nil {
			return nil, err
		}
	}
	return getPostgresMobileEntity(ctx, tx, workspaceID, mutation.EntityType, mutation.EntityClientID)
}

func tombstonePostgresTaskOccurrences(ctx context.Context, tx *sql.Tx, workspaceID, taskID string, now time.Time) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT occurrence_id FROM task_occurrences
		WHERE workspace_id = $1 AND task_id = $2 AND occurrence_id IS NOT NULL AND deleted_at IS NULL
		ORDER BY occurrence_id
		FOR UPDATE
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
			UPDATE task_occurrences SET deleted_at = $1, updated_at = $1, revision = revision + 1
			WHERE workspace_id = $2 AND occurrence_id = $3 AND deleted_at IS NULL
		`, now, workspaceID, occurrenceID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO mobile_retired_ids (workspace_id, entity_type, client_id, retired_at)
			VALUES ($1, 'task_occurrence', $2, $3) ON CONFLICT DO NOTHING
		`, workspaceID, occurrenceID, now); err != nil {
			return err
		}
		if err := persistPostgresServerEntityChange(ctx, tx, workspaceID, uuid.NewString(), "task_occurrence", "task_occurrence.server_deleted", occurrenceID, now); err != nil {
			return err
		}
	}
	return nil
}

type postgresMobileEntityIdentity struct {
	id        string
	revision  int64
	deletedAt sql.NullTime
}

func loadPostgresMobileEntityIdentity(ctx context.Context, runner postgresRunner, workspaceID, entityType, clientID string) (*postgresMobileEntityIdentity, error) {
	table, err := postgresMobileEntityTable(entityType)
	if err != nil {
		return nil, err
	}
	var identity postgresMobileEntityIdentity
	err = runner.QueryRowContext(ctx, `SELECT id, revision, deleted_at FROM `+table+` WHERE workspace_id = $1 AND client_id = $2`, workspaceID, clientID).
		Scan(&identity.id, &identity.revision, &identity.deletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storage.ErrMobileEntityNotFound
	}
	return &identity, err
}

func getPostgresMobileEntity(ctx context.Context, runner postgresRunner, workspaceID, entityType, clientID string) (*model.MobileEntityEnvelope, error) {
	if entityType == "voice_note" {
		return getPostgresMobileVoiceNote(ctx, runner, workspaceID, clientID)
	}
	if entityType == "task_occurrence" {
		return getPostgresMobileOccurrence(ctx, runner, workspaceID, clientID)
	}
	identity, err := loadPostgresMobileEntityIdentity(ctx, runner, workspaceID, entityType, clientID)
	if err != nil {
		return nil, err
	}
	var payload []byte
	switch entityType {
	case "task":
		var title, content, status, executionType string
		var priority int
		var done bool
		var dueAt sql.NullTime
		var plannedDate sql.NullTime
		if err := runner.QueryRowContext(ctx, `
			SELECT title, content, priority, done, status, due_at, planned_date, execution_type
			FROM tasks WHERE workspace_id = $1 AND client_id = $2
		`, workspaceID, clientID).Scan(&title, &content, &priority, &done, &status, &dueAt, &plannedDate, &executionType); err != nil {
			return nil, err
		}
		doneValue := 0
		if done {
			doneValue = 1
		}
		payload, err = json.Marshal(map[string]any{
			"title": title, "content": content, "priority": priority, "done": doneValue, "status": status,
			"due": nullablePostgresMobileEntityUnix(dueAt), "planned_date": nullablePostgresMobileEntityDate(plannedDate), "execution_type": executionType,
		})
	case "event":
		var title, kind string
		var startAt, endAt time.Time
		var location sql.NullString
		if err := runner.QueryRowContext(ctx, `SELECT title, start_at, end_at, location, kind FROM events WHERE workspace_id = $1 AND client_id = $2`, workspaceID, clientID).
			Scan(&title, &startAt, &endAt, &location, &kind); err != nil {
			return nil, err
		}
		payload, err = json.Marshal(map[string]any{"title": title, "start_time": startAt.UTC().Unix(), "end_time": endAt.UTC().Unix(), "location": nullablePostgresMobileEntityString(location), "kind": kind})
	case "inbox":
		var kind, title string
		var body sql.NullString
		var archived bool
		if err := runner.QueryRowContext(ctx, `SELECT kind, title, body, archived FROM inbox WHERE workspace_id = $1 AND client_id = $2`, workspaceID, clientID).
			Scan(&kind, &title, &body, &archived); err != nil {
			return nil, err
		}
		archivedValue := 0
		if archived {
			archivedValue = 1
		}
		payload, err = json.Marshal(map[string]any{"kind": kind, "title": title, "body": nullablePostgresMobileEntityString(body), "archived": archivedValue})
	default:
		return nil, fmt.Errorf("unsupported mobile entity type %q", entityType)
	}
	if err != nil {
		return nil, err
	}
	entity := &model.MobileEntityEnvelope{EntityType: entityType, ID: identity.id, ClientID: clientID, Revision: identity.revision, Payload: payload}
	if identity.deletedAt.Valid {
		deletedAt := identity.deletedAt.Time.UTC().Unix()
		entity.DeletedAt = &deletedAt
	}
	return entity, nil
}

func getPostgresEntityMutationReceipt(ctx context.Context, tx *sql.Tx, workspaceID string, mutation model.MobileEntityMutation) (*model.MobileMutationResult, bool, error) {
	var requestHash string
	var responseJSON []byte
	err := tx.QueryRowContext(ctx, `
		SELECT request_sha256, response_json FROM mobile_mutation_receipts
		WHERE workspace_id = $1 AND device_client_id = $2 AND mutation_id = $3
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
	if err := json.Unmarshal(responseJSON, &result); err != nil {
		return nil, false, err
	}
	return &result, true, nil
}

func persistPostgresEntityMutationResult(ctx context.Context, tx *sql.Tx, workspaceID string, mutation model.MobileEntityMutation, result *model.MobileMutationResult) error {
	responseJSON, err := json.Marshal(result)
	if err != nil {
		return err
	}
	entityJSON, err := json.Marshal(result.Entity)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO mobile_sync_outbox (
			workspace_id, mutation_id, entity_type, entity_client_id, operation, revision, entity_json, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8)
	`, workspaceID, mutation.MutationID, mutation.EntityType, mutation.EntityClientID, mutation.Operation, result.Entity.Revision, entityJSON, now); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO mobile_mutation_receipts (
			workspace_id, device_client_id, mutation_id, request_sha256, response_json, created_at
		) VALUES ($1, $2, $3, $4, $5::json, $6)
	`, workspaceID, mutation.DeviceClientID, mutation.MutationID, mutation.RequestSHA256, responseJSON, now)
	return err
}

func persistPostgresServerEntityChange(ctx context.Context, tx *sql.Tx, workspaceID, mutationID, entityType, operation, clientID string, now time.Time) error {
	entity, err := getPostgresMobileEntity(ctx, tx, workspaceID, entityType, clientID)
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
		) VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8)
	`, workspaceID, mutationID, entityType, clientID, operation, entity.Revision, entityJSON, now)
	return err
}

func upsertPostgresMobileEntitySearch(ctx context.Context, tx *sql.Tx, workspaceID, entityType, clientID string) error {
	switch entityType {
	case "task":
		var task model.Task
		var updatedAt time.Time
		if err := tx.QueryRowContext(ctx, `SELECT id, title, content, updated_at FROM tasks WHERE workspace_id = $1 AND client_id = $2`, workspaceID, clientID).
			Scan(&task.ID, &task.Title, &task.Content, &updatedAt); err != nil {
			return err
		}
		task.UpdatedAt = updatedAt.UTC().Unix()
		return upsertTaskSearchIndex(ctx, tx, workspaceID, &task)
	case "event":
		var event model.Event
		var location sql.NullString
		var updatedAt time.Time
		if err := tx.QueryRowContext(ctx, `SELECT id, title, location, kind, updated_at FROM events WHERE workspace_id = $1 AND client_id = $2`, workspaceID, clientID).
			Scan(&event.ID, &event.Title, &location, &event.Kind, &updatedAt); err != nil {
			return err
		}
		if location.Valid {
			event.Location = &location.String
		}
		event.UpdatedAt = updatedAt.UTC().Unix()
		return upsertEventSearchIndex(ctx, tx, workspaceID, &event)
	case "inbox":
		return nil
	default:
		return fmt.Errorf("unsupported mobile entity type %q", entityType)
	}
}

func validatePostgresMobileEntityMutation(mutation model.MobileEntityMutation) error {
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

func applyPostgresVoiceMutation(ctx context.Context, tx *sql.Tx, workspaceID string, mutation model.MobileEntityMutation) (*model.MobileEntityEnvelope, error) {
	switch mutation.Operation {
	case "voice.create":
		return createPostgresVoiceNote(ctx, tx, workspaceID, mutation)
	case "voice_audio.delete":
		return deletePostgresVoiceAudio(ctx, tx, workspaceID, mutation)
	case "voice_note.delete":
		return deletePostgresVoiceNote(ctx, tx, workspaceID, mutation)
	default:
		return nil, fmt.Errorf("unsupported mobile operation %q", mutation.Operation)
	}
}

func createPostgresVoiceNote(ctx context.Context, tx *sql.Tx, workspaceID string, mutation model.MobileEntityMutation) (*model.MobileEntityEnvelope, error) {
	var payload struct {
		Title      string `json:"title"`
		DurationMS int64  `json:"duration_ms"`
		RecordedAt int64  `json:"recorded_at"`
		Language   string `json:"language"`
	}
	if err := decodePostgresEntityPayload(mutation.Payload, &payload); err != nil || payload.DurationMS < 0 {
		return nil, errors.New("voice.create contains invalid metadata")
	}
	var retired int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM mobile_retired_ids WHERE workspace_id = $1 AND entity_type = 'voice_note' AND client_id = $2`, workspaceID, mutation.EntityClientID).Scan(&retired); err != nil {
		return nil, err
	}
	if retired > 0 {
		return nil, storage.ErrMobileEntityGone
	}
	var existing int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM voice_notes WHERE workspace_id = $1 AND client_id = $2`, workspaceID, mutation.EntityClientID).Scan(&existing); err != nil {
		return nil, err
	}
	if existing > 0 {
		return nil, storage.ErrAlreadyExists
	}
	now := time.Now().UTC()
	if payload.RecordedAt <= 0 {
		payload.RecordedAt = now.Unix()
	}
	payload.Title = strings.TrimSpace(payload.Title)
	if payload.Title == "" {
		payload.Title = "Voice note"
	}
	noteID := uuid.NewString()
	noteClientID := deterministicPostgresMobileNoteClientID(workspaceID, noteID)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO notes (id, client_id, revision, title, body, folder_id, tags, created_at, updated_at, workspace_id)
		VALUES ($1, $2, 1, $3, '', '__uncategorized', ARRAY['voice']::text[], $4, $5, $6)
	`, noteID, noteClientID, payload.Title, time.Unix(payload.RecordedAt, 0).UTC(), now, workspaceID); err != nil {
		return nil, err
	}
	if err := upsertNoteSearchIndex(ctx, tx, workspaceID, &model.Note{
		ID: noteID, Title: payload.Title, FolderID: "__uncategorized", Tags: `["voice"]`, UpdatedAt: now.Unix(),
	}, []string{"voice"}); err != nil {
		return nil, err
	}
	if err := persistPostgresServerNoteChange(ctx, tx, workspaceID, uuid.NewString(), model.MobileOperationNoteServerCreated, noteClientID, now); err != nil {
		return nil, err
	}
	voiceID := uuid.NewString()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO voice_notes (
			id, workspace_id, client_id, revision, audio_revision, audio_state, note_id, duration_ms, recorded_at, language,
			object_key, mime_type, audio_size, audio_sha256, upload_state,
			transcription_state, transcription_error, created_at, updated_at
		) VALUES ($1, $2, $3, 1, 1, 'absent', $4, $5, $6, $7, '', '', 0, '', 'pending', 'not_started', '', $8, $8)
	`, voiceID, workspaceID, mutation.EntityClientID, noteID, payload.DurationMS, payload.RecordedAt, strings.TrimSpace(payload.Language), now.Unix()); err != nil {
		return nil, err
	}
	return getPostgresMobileVoiceNote(ctx, tx, workspaceID, mutation.EntityClientID)
}

func deletePostgresVoiceAudio(ctx context.Context, tx *sql.Tx, workspaceID string, mutation model.MobileEntityMutation) (*model.MobileEntityEnvelope, error) {
	var revision int64
	var objectKey string
	var deletedAt sql.NullTime
	err := tx.QueryRowContext(ctx, `SELECT revision, object_key, deleted_at FROM voice_notes WHERE workspace_id = $1 AND client_id = $2 FOR UPDATE`, workspaceID, mutation.EntityClientID).
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
	now := time.Now().UTC()
	audioState := model.VoiceAudioDeleted
	if objectKey != "" {
		audioState = model.VoiceAudioDeleteRequested
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE voice_notes SET audio_state = $1, audio_revision = audio_revision + 1,
			object_key = CASE WHEN $2 = '' THEN '' ELSE object_key END,
			mime_type = CASE WHEN $2 = '' THEN '' ELSE mime_type END,
			audio_size = CASE WHEN $2 = '' THEN 0 ELSE audio_size END,
			audio_sha256 = CASE WHEN $2 = '' THEN '' ELSE audio_sha256 END,
			upload_state = CASE WHEN $2 = '' THEN 'failed' ELSE upload_state END,
			revision = revision + 1, updated_at = $3
		WHERE workspace_id = $4 AND client_id = $5 AND revision = $6 AND deleted_at IS NULL
	`, audioState, objectKey, now.Unix(), workspaceID, mutation.EntityClientID, revision)
	if err != nil {
		return nil, err
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		if err != nil {
			return nil, err
		}
		return nil, storage.ErrRevisionConflict
	}
	if err := cancelPostgresVoiceTranscriptionJobs(ctx, tx, workspaceID, mutation.EntityClientID, now.Unix()); err != nil {
		return nil, err
	}
	if objectKey != "" {
		if err := enqueuePostgresVoiceAudioCleanup(ctx, tx, workspaceID, mutation.EntityClientID, objectKey, now.Unix()); err != nil {
			return nil, err
		}
	}
	return getPostgresMobileVoiceNote(ctx, tx, workspaceID, mutation.EntityClientID)
}

func deletePostgresVoiceNote(ctx context.Context, tx *sql.Tx, workspaceID string, mutation model.MobileEntityMutation) (*model.MobileEntityEnvelope, error) {
	var revision int64
	var noteID string
	var objectKey string
	var deletedAt sql.NullTime
	err := tx.QueryRowContext(ctx, `SELECT revision, note_id, object_key, deleted_at FROM voice_notes WHERE workspace_id = $1 AND client_id = $2 FOR UPDATE`, workspaceID, mutation.EntityClientID).
		Scan(&revision, &noteID, &objectKey, &deletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		if mutation.BaseRevision == nil || *mutation.BaseRevision != 0 {
			return nil, storage.ErrMobileEntityNotFound
		}
		return pretombstonePostgresVoiceNote(ctx, tx, workspaceID, mutation.EntityClientID)
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
	now := time.Now().UTC()
	audioState := model.VoiceAudioDeleted
	if objectKey != "" {
		audioState = model.VoiceAudioDeleteRequested
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE voice_notes SET deleted_at = $1, audio_state = $2, audio_revision = audio_revision + 1,
			object_key = CASE WHEN $3 = '' THEN '' ELSE object_key END,
			mime_type = CASE WHEN $3 = '' THEN '' ELSE mime_type END,
			audio_size = CASE WHEN $3 = '' THEN 0 ELSE audio_size END,
			audio_sha256 = CASE WHEN $3 = '' THEN '' ELSE audio_sha256 END,
			upload_state = CASE WHEN $3 = '' THEN 'failed' ELSE upload_state END,
			revision = revision + 1, updated_at = $4
		WHERE workspace_id = $5 AND client_id = $6 AND revision = $7 AND deleted_at IS NULL
	`, now, audioState, objectKey, now.Unix(), workspaceID, mutation.EntityClientID, revision)
	if err != nil {
		return nil, err
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		if err != nil {
			return nil, err
		}
		return nil, storage.ErrRevisionConflict
	}
	if err := cancelPostgresVoiceTranscriptionJobs(ctx, tx, workspaceID, mutation.EntityClientID, now.Unix()); err != nil {
		return nil, err
	}
	if objectKey != "" {
		if err := enqueuePostgresVoiceAudioCleanup(ctx, tx, workspaceID, mutation.EntityClientID, objectKey, now.Unix()); err != nil {
			return nil, err
		}
	}
	if err := (noteRepository{db: tx}).Delete(ctx, noteID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO mobile_retired_ids (workspace_id, entity_type, client_id, retired_at)
		VALUES ($1, 'voice_note', $2, $3) ON CONFLICT DO NOTHING
	`, workspaceID, mutation.EntityClientID, now); err != nil {
		return nil, err
	}
	return getPostgresMobileVoiceNote(ctx, tx, workspaceID, mutation.EntityClientID)
}

func pretombstonePostgresVoiceNote(ctx context.Context, tx *sql.Tx, workspaceID, clientID string) (*model.MobileEntityEnvelope, error) {
	now := time.Now().UTC()
	noteID := uuid.NewString()
	noteClientID := deterministicPostgresMobileNoteClientID(workspaceID, noteID)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO notes (id, client_id, revision, deleted_at, title, body, folder_id, tags, created_at, updated_at, workspace_id)
		VALUES ($1, $2, 1, $3, '[deleted voice]', '', '__uncategorized', ARRAY['voice']::text[], $3, $3, $4)
	`, noteID, noteClientID, now, workspaceID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO voice_notes (
			id, workspace_id, client_id, revision, deleted_at, audio_revision, audio_state, note_id,
			duration_ms, recorded_at, language, object_key, mime_type, audio_size, audio_sha256,
			upload_state, transcription_state, transcription_error, created_at, updated_at
		) VALUES ($1, $2, $3, 1, $4, 1, 'deleted', $5, 0, $6, '', '', '', 0, '', 'failed', 'not_started', '', $6, $6)
	`, uuid.NewString(), workspaceID, clientID, now, noteID, now.Unix()); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO mobile_retired_ids (workspace_id, entity_type, client_id, retired_at)
		VALUES ($1, 'voice_note', $2, $3) ON CONFLICT DO NOTHING
	`, workspaceID, clientID, now); err != nil {
		return nil, err
	}
	return getPostgresMobileVoiceNote(ctx, tx, workspaceID, clientID)
}

func cancelPostgresVoiceTranscriptionJobs(ctx context.Context, tx *sql.Tx, workspaceID, clientID string, now int64) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE transcription_jobs SET state = 'canceled', revision = revision + 1, error_code = 'voice_audio_deleted',
			next_attempt_at = NULL, lease_owner = '', lease_token = '', lease_expires_at = NULL, heartbeat_at = NULL, updated_at = $1
		WHERE workspace_id = $2 AND voice_note_id = $3 AND state IN ('waiting_for_audio', 'queued', 'processing', 'retry_waiting')
	`, now, workspaceID, clientID)
	return err
}

func enqueuePostgresVoiceAudioCleanup(ctx context.Context, tx *sql.Tx, workspaceID, clientID, objectKey string, now int64) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO voice_audio_cleanup_jobs (
			job_id, workspace_id, voice_note_id, object_key, state, revision, attempt, max_attempts,
			error_code, next_attempt_at, lease_owner, lease_token, created_at, updated_at
		) VALUES ($1, $2, $3, $4, 'retry_waiting', 1, 0, 6, '', $5 + 600, '', '', $5, $5)
		ON CONFLICT (workspace_id, voice_note_id, object_key) DO NOTHING
	`, uuid.NewString(), workspaceID, clientID, objectKey, now)
	return err
}

func getPostgresMobileVoiceNote(ctx context.Context, runner postgresRunner, workspaceID, clientID string) (*model.MobileEntityEnvelope, error) {
	var id, noteID, title, body, language, uploadState, audioState, transcriptionState, transcriptionError string
	var revision, audioRevision, durationMS, recordedAt, audioSize int64
	var deletedAt sql.NullTime
	err := runner.QueryRowContext(ctx, `
		SELECT v.id, v.note_id, n.title, n.body, v.revision, v.deleted_at,
			v.duration_ms, v.recorded_at, v.language, v.audio_size, v.upload_state, v.audio_state, v.audio_revision,
			v.transcription_state, v.transcription_error
		FROM voice_notes v
		JOIN notes n ON n.workspace_id = v.workspace_id AND n.id = v.note_id
		WHERE v.workspace_id = $1 AND v.client_id = $2
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
		value := deletedAt.Time.UTC().Unix()
		entity.DeletedAt = &value
	}
	return entity, nil
}

func applyPostgresOccurrenceMutation(ctx context.Context, tx *sql.Tx, workspaceID string, mutation model.MobileEntityMutation) (*model.MobileEntityEnvelope, error) {
	switch mutation.Operation {
	case "task_occurrence.complete":
		var payload struct {
			TaskID         string `json:"task_id"`
			OccurrenceDate string `json:"occurrence_date"`
			CompletedAt    int64  `json:"completed_at"`
		}
		if err := decodePostgresEntityPayload(mutation.Payload, &payload); err != nil || payload.TaskID == "" || payload.OccurrenceDate == "" || payload.CompletedAt <= 0 {
			return nil, errors.New("task_occurrence.complete requires task_id, occurrence_date, and completed_at")
		}
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_occurrences WHERE workspace_id = $1 AND occurrence_id = $2`, workspaceID, mutation.EntityClientID).Scan(&exists); err != nil {
			return nil, err
		}
		if exists == 0 {
			var taskExists bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM tasks WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL)`, workspaceID, payload.TaskID).Scan(&taskExists); err != nil {
				return nil, err
			}
			if !taskExists {
				return nil, storage.ErrMobileEntityNotFound
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO task_occurrences (
					task_id, occurrence_date, occurrence_id, revision, status, completed_at, workspace_id
				) VALUES ($1, $2::date, $3, 1, 'done', to_timestamp($4), $5)
			`, payload.TaskID, payload.OccurrenceDate, mutation.EntityClientID, payload.CompletedAt, workspaceID); err != nil {
				return nil, err
			}
		} else {
			if mutation.BaseRevision == nil {
				return nil, storage.ErrRevisionConflict
			}
			result, err := tx.ExecContext(ctx, `
				UPDATE task_occurrences SET status = 'done', completed_at = to_timestamp($1), revision = revision + 1, updated_at = now()
				WHERE workspace_id = $2 AND occurrence_id = $3 AND revision = $4 AND deleted_at IS NULL
			`, payload.CompletedAt, workspaceID, mutation.EntityClientID, *mutation.BaseRevision)
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
			UPDATE task_occurrences SET status = 'open', completed_at = NULL, revision = revision + 1, updated_at = now()
			WHERE workspace_id = $1 AND occurrence_id = $2 AND revision = $3 AND deleted_at IS NULL
		`, workspaceID, mutation.EntityClientID, *mutation.BaseRevision)
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
	return getPostgresMobileOccurrence(ctx, tx, workspaceID, mutation.EntityClientID)
}

func getPostgresMobileOccurrence(ctx context.Context, runner postgresRunner, workspaceID, occurrenceID string) (*model.MobileEntityEnvelope, error) {
	var taskID, occurrenceDate, status, note string
	var revision int64
	var completedAt sql.NullInt64
	var deletedAt sql.NullTime
	err := runner.QueryRowContext(ctx, `
		SELECT task_id, occurrence_date::text, status, EXTRACT(EPOCH FROM completed_at)::bigint, note, revision, deleted_at
		FROM task_occurrences WHERE workspace_id = $1 AND occurrence_id = $2
	`, workspaceID, occurrenceID).Scan(&taskID, &occurrenceDate, &status, &completedAt, &note, &revision, &deletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storage.ErrMobileEntityNotFound
	}
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(map[string]any{
		"task_id": taskID, "occurrence_date": occurrenceDate, "status": status,
		"completed_at": nullablePostgresMobileEntityInt64(completedAt), "note": note,
	})
	if err != nil {
		return nil, err
	}
	entity := &model.MobileEntityEnvelope{EntityType: "task_occurrence", ID: occurrenceID, ClientID: occurrenceID, Revision: revision, Payload: payload}
	if deletedAt.Valid {
		value := deletedAt.Time.UTC().Unix()
		entity.DeletedAt = &value
	}
	return entity, nil
}

func nullablePostgresMobileEntityInt64(value sql.NullInt64) any {
	if !value.Valid {
		return nil
	}
	return value.Int64
}

func nullablePostgresMobileEntityUnix(value sql.NullTime) any {
	if !value.Valid {
		return nil
	}
	return value.Time.UTC().Unix()
}

func nullablePostgresMobileEntityDate(value sql.NullTime) any {
	if !value.Valid {
		return nil
	}
	return value.Time.Format("2006-01-02")
}

func postgresMobileEntityTable(entityType string) (string, error) {
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

func decodePostgresEntityPayload(raw json.RawMessage, destination any) error {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	return json.Unmarshal(raw, destination)
}

func postgresMobileUnixPtr(value *int64) any {
	if value == nil {
		return nil
	}
	return time.Unix(*value, 0).UTC()
}

func nullablePostgresMobileEntityString(value sql.NullString) any {
	if !value.Valid {
		return nil
	}
	return value.String
}

func deterministicPostgresMobileEntityClientID(entityType, workspaceID, entityID string) string {
	digest := md5.Sum([]byte("flowspace:" + entityType + ":" + workspaceID + ":" + entityID))
	digest[6] = (digest[6] & 0x0f) | 0x30
	digest[8] = (digest[8] & 0x3f) | 0x80
	return uuid.UUID(digest).String()
}
