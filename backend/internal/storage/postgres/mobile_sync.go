package postgres

import (
	"context"
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
	"github.com/lib/pq"
)

type mobileSyncRepository struct {
	db postgresRunner
}

func (r mobileSyncRepository) ApplyNoteMutation(ctx context.Context, mutation model.MobileNoteMutation) (*model.MobileMutationResult, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if err := validatePostgresMobileNoteMutation(mutation); err != nil {
		return nil, err
	}
	var result *model.MobileMutationResult
	err = r.withTx(ctx, func(tx *sql.Tx) error {
		var applyErr error
		result, applyErr = applyPostgresNoteMutation(ctx, tx, workspaceID, mutation)
		return applyErr
	})
	return result, err
}

func (r mobileSyncRepository) GetNoteByClientID(ctx context.Context, clientID string) (*model.MobileEntityEnvelope, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return getPostgresMobileNote(ctx, r.db, workspaceID, clientID)
}

func (r mobileSyncRepository) ListPendingChanges(ctx context.Context) ([]model.MobileChange, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT sequence, mutation_id, operation, entity_json
		FROM mobile_sync_outbox
		WHERE workspace_id = $1 AND published_at IS NULL
		ORDER BY sequence ASC
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	changes := make([]model.MobileChange, 0)
	for rows.Next() {
		var change model.MobileChange
		var entityJSON []byte
		if err := rows.Scan(&change.Sequence, &change.MutationID, &change.Operation, &entityJSON); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(entityJSON, &change.Entity); err != nil {
			return nil, fmt.Errorf("decode mobile outbox entity: %w", err)
		}
		changes = append(changes, change)
	}
	return changes, rows.Err()
}

func (r mobileSyncRepository) withTx(ctx context.Context, fn func(*sql.Tx) error) error {
	if tx, ok := r.db.(*sql.Tx); ok {
		return fn(tx)
	}
	db, ok := r.db.(*sql.DB)
	if !ok {
		return fmt.Errorf("unsupported postgres mobile sync runner %T", r.db)
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

func applyPostgresNoteMutation(ctx context.Context, tx *sql.Tx, workspaceID string, mutation model.MobileNoteMutation) (*model.MobileMutationResult, error) {
	lockKey := fmt.Sprintf(
		"%d:%s%d:%s%d:%s",
		len(workspaceID), workspaceID,
		len(mutation.DeviceClientID), mutation.DeviceClientID,
		len(mutation.MutationID), mutation.MutationID,
	)
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1::text))`, lockKey); err != nil {
		return nil, fmt.Errorf("lock mobile mutation receipt: %w", err)
	}
	if receipt, found, err := getPostgresMutationReceipt(ctx, tx, workspaceID, mutation); err != nil {
		return nil, err
	} else if found {
		return receipt, nil
	}

	var entity *model.MobileEntityEnvelope
	var err error
	switch mutation.Operation {
	case model.MobileOperationNoteCreate:
		entity, err = createPostgresMobileNote(ctx, tx, workspaceID, mutation)
	case model.MobileOperationNoteUpdate:
		entity, err = updatePostgresMobileNote(ctx, tx, workspaceID, mutation)
	case model.MobileOperationNoteDelete:
		entity, err = deletePostgresMobileNote(ctx, tx, workspaceID, mutation)
	default:
		err = fmt.Errorf("unsupported mobile note operation %q", mutation.Operation)
	}
	if err != nil {
		return nil, err
	}
	result := &model.MobileMutationResult{MutationID: mutation.MutationID, Status: model.MobileMutationApplied, Entity: entity}
	if err := persistPostgresMutationResult(ctx, tx, workspaceID, mutation, result); err != nil {
		return nil, err
	}
	return result, nil
}

func getPostgresMutationReceipt(ctx context.Context, tx *sql.Tx, workspaceID string, mutation model.MobileNoteMutation) (*model.MobileMutationResult, bool, error) {
	var requestHash string
	var responseJSON []byte
	err := tx.QueryRowContext(ctx, `
		SELECT request_sha256, response_json
		FROM mobile_mutation_receipts
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
		return nil, false, fmt.Errorf("decode mutation receipt: %w", err)
	}
	return &result, true, nil
}

func createPostgresMobileNote(ctx context.Context, tx *sql.Tx, workspaceID string, mutation model.MobileNoteMutation) (*model.MobileEntityEnvelope, error) {
	if mutation.Payload.Title == nil || strings.TrimSpace(*mutation.Payload.Title) == "" {
		return nil, errors.New("note.create requires a non-empty title")
	}
	var retired int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM mobile_retired_ids
		WHERE workspace_id = $1 AND entity_type = 'note' AND client_id = $2
	`, workspaceID, mutation.EntityClientID).Scan(&retired); err != nil {
		return nil, err
	}
	if retired > 0 {
		return nil, storage.ErrMobileEntityGone
	}
	body := postgresValueOrDefault(mutation.Payload.Body, "")
	folderID := postgresValueOrDefault(mutation.Payload.FolderID, "__uncategorized")
	tags, err := tagsJSONStringToArray(postgresValueOrDefault(mutation.Payload.Tags, "[]"))
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	noteID := uuid.NewString()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO notes (
			id, client_id, revision, title, body, folder_id, tags, created_at, updated_at, workspace_id
		) VALUES ($1, $2, 1, $3, $4, $5, $6::text[], $7, $7, $8)
	`, noteID, mutation.EntityClientID, strings.TrimSpace(*mutation.Payload.Title), body, folderID, pq.Array(tags), now, workspaceID)
	if err != nil {
		return nil, err
	}
	note := &model.Note{ID: noteID, Title: strings.TrimSpace(*mutation.Payload.Title), Body: body, FolderID: folderID, Tags: tagsArrayToJSONString(tags), UpdatedAt: now.Unix()}
	if err := upsertNoteSearchIndex(ctx, tx, workspaceID, note, tags); err != nil {
		return nil, err
	}
	return getPostgresMobileNote(ctx, tx, workspaceID, mutation.EntityClientID)
}

func updatePostgresMobileNote(ctx context.Context, tx *sql.Tx, workspaceID string, mutation model.MobileNoteMutation) (*model.MobileEntityEnvelope, error) {
	current, err := loadPostgresMobileNoteRow(ctx, tx, workspaceID, mutation.EntityClientID)
	if err != nil {
		return nil, err
	}
	if current.DeletedAt.Valid {
		return nil, storage.ErrMobileEntityGone
	}
	if mutation.BaseRevision == nil || current.Revision != *mutation.BaseRevision {
		return nil, storage.ErrRevisionConflict
	}
	title := postgresValueOrDefault(mutation.Payload.Title, current.Title)
	if strings.TrimSpace(title) == "" {
		return nil, errors.New("note title must not be empty")
	}
	body := postgresValueOrDefault(mutation.Payload.Body, current.Body)
	folderID := postgresValueOrDefault(mutation.Payload.FolderID, current.FolderID)
	tags := current.Tags
	if mutation.Payload.Tags != nil {
		tags, err = tagsJSONStringToArray(*mutation.Payload.Tags)
		if err != nil {
			return nil, err
		}
	}
	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, `
		UPDATE notes
		SET title = $1, body = $2, folder_id = $3, tags = $4::text[], revision = revision + 1, updated_at = $5
		WHERE workspace_id = $6 AND client_id = $7 AND revision = $8 AND deleted_at IS NULL
	`, strings.TrimSpace(title), body, folderID, pq.Array(tags), now, workspaceID, mutation.EntityClientID, current.Revision)
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
	note := &model.Note{ID: current.ID, Title: strings.TrimSpace(title), Body: body, FolderID: folderID, Tags: tagsArrayToJSONString(tags), UpdatedAt: now.Unix()}
	if err := upsertNoteSearchIndex(ctx, tx, workspaceID, note, tags); err != nil {
		return nil, err
	}
	return getPostgresMobileNote(ctx, tx, workspaceID, mutation.EntityClientID)
}

func deletePostgresMobileNote(ctx context.Context, tx *sql.Tx, workspaceID string, mutation model.MobileNoteMutation) (*model.MobileEntityEnvelope, error) {
	current, err := loadPostgresMobileNoteRow(ctx, tx, workspaceID, mutation.EntityClientID)
	if err != nil {
		return nil, err
	}
	if current.DeletedAt.Valid {
		return nil, storage.ErrMobileEntityGone
	}
	if mutation.BaseRevision == nil || current.Revision != *mutation.BaseRevision {
		return nil, storage.ErrRevisionConflict
	}
	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, `
		UPDATE notes
		SET deleted_at = $1, revision = revision + 1, updated_at = $1
		WHERE workspace_id = $2 AND client_id = $3 AND revision = $4 AND deleted_at IS NULL
	`, now, workspaceID, mutation.EntityClientID, current.Revision)
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
		VALUES ($1, 'note', $2, $3)
		ON CONFLICT DO NOTHING
	`, workspaceID, mutation.EntityClientID, now); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM search_index WHERE workspace_id = $1 AND entity_type = 'note' AND entity_id = $2`, workspaceID, current.ID); err != nil {
		return nil, err
	}
	return getPostgresMobileNote(ctx, tx, workspaceID, mutation.EntityClientID)
}

func persistPostgresMutationResult(ctx context.Context, tx *sql.Tx, workspaceID string, mutation model.MobileNoteMutation, result *model.MobileMutationResult) error {
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
		) VALUES ($1, $2, 'note', $3, $4, $5, $6::jsonb, $7)
	`, workspaceID, mutation.MutationID, mutation.EntityClientID, mutation.Operation, result.Entity.Revision, entityJSON, now); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO mobile_mutation_receipts (
			workspace_id, device_client_id, mutation_id, request_sha256, response_json, created_at
		) VALUES ($1, $2, $3, $4, $5::json, $6)
	`, workspaceID, mutation.DeviceClientID, mutation.MutationID, mutation.RequestSHA256, responseJSON, now)
	return err
}

func persistPostgresServerNoteChange(ctx context.Context, tx *sql.Tx, workspaceID, mutationID, operation, clientID string, now time.Time) error {
	entity, err := getPostgresMobileNote(ctx, tx, workspaceID, clientID)
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
		) VALUES ($1, $2, 'note', $3, $4, $5, $6::jsonb, $7)
	`, workspaceID, mutationID, clientID, operation, entity.Revision, entityJSON, now)
	return err
}

type postgresMobileNoteRow struct {
	ID        string
	ClientID  string
	Revision  int64
	DeletedAt sql.NullTime
	Title     string
	Body      string
	FolderID  string
	Tags      []string
}

func loadPostgresMobileNoteRow(ctx context.Context, runner postgresRunner, workspaceID, clientID string) (*postgresMobileNoteRow, error) {
	var row postgresMobileNoteRow
	err := runner.QueryRowContext(ctx, `
		SELECT id, client_id, revision, deleted_at, title, body, folder_id, tags
		FROM notes WHERE workspace_id = $1 AND client_id = $2
	`, workspaceID, clientID).Scan(&row.ID, &row.ClientID, &row.Revision, &row.DeletedAt, &row.Title, &row.Body, &row.FolderID, pq.Array(&row.Tags))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storage.ErrMobileEntityNotFound
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func getPostgresMobileNote(ctx context.Context, runner postgresRunner, workspaceID, clientID string) (*model.MobileEntityEnvelope, error) {
	row, err := loadPostgresMobileNoteRow(ctx, runner, workspaceID, clientID)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(map[string]string{
		"title":     row.Title,
		"body":      row.Body,
		"folder_id": row.FolderID,
		"tags":      tagsArrayToJSONString(row.Tags),
	})
	if err != nil {
		return nil, err
	}
	entity := &model.MobileEntityEnvelope{EntityType: "note", ID: row.ID, ClientID: row.ClientID, Revision: row.Revision, Payload: payload}
	if row.DeletedAt.Valid {
		deletedAt := row.DeletedAt.Time.UTC().Unix()
		entity.DeletedAt = &deletedAt
	}
	return entity, nil
}

func validatePostgresMobileNoteMutation(mutation model.MobileNoteMutation) error {
	for name, value := range map[string]string{
		"mutation_id":      mutation.MutationID,
		"device_client_id": mutation.DeviceClientID,
		"entity_client_id": mutation.EntityClientID,
		"operation":        mutation.Operation,
		"request_sha256":   mutation.RequestSHA256,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	return nil
}

func postgresValueOrDefault(value *string, fallback string) string {
	if value == nil {
		return fallback
	}
	return *value
}
