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

type mobileSyncRepository struct {
	db sqliteRunner
}

func (r mobileSyncRepository) ApplyNoteMutation(ctx context.Context, mutation model.MobileNoteMutation) (*model.MobileMutationResult, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateMobileNoteMutation(mutation); err != nil {
		return nil, err
	}

	var result *model.MobileMutationResult
	err = r.withTx(ctx, func(tx *sql.Tx) error {
		var applyErr error
		result, applyErr = applySQLiteNoteMutation(ctx, tx, workspaceID, mutation)
		return applyErr
	})
	return result, err
}

func (r mobileSyncRepository) GetNoteByClientID(ctx context.Context, clientID string) (*model.MobileEntityEnvelope, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return getSQLiteMobileNote(ctx, r.db, workspaceID, clientID)
}

func (r mobileSyncRepository) ListPendingChanges(ctx context.Context) ([]model.MobileChange, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT sequence, mutation_id, operation, entity_json
		FROM mobile_sync_outbox
		WHERE workspace_id = ? AND published_at IS NULL
		ORDER BY sequence ASC
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	changes := make([]model.MobileChange, 0)
	for rows.Next() {
		var change model.MobileChange
		var entityJSON string
		if err := rows.Scan(&change.Sequence, &change.MutationID, &change.Operation, &entityJSON); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(entityJSON), &change.Entity); err != nil {
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
		return fmt.Errorf("unsupported sqlite mobile sync runner %T", r.db)
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

func applySQLiteNoteMutation(ctx context.Context, tx *sql.Tx, workspaceID string, mutation model.MobileNoteMutation) (*model.MobileMutationResult, error) {
	if receipt, found, err := getSQLiteMutationReceipt(ctx, tx, workspaceID, mutation); err != nil {
		return nil, err
	} else if found {
		return receipt, nil
	}

	var entity *model.MobileEntityEnvelope
	var err error
	switch mutation.Operation {
	case model.MobileOperationNoteCreate:
		entity, err = createSQLiteMobileNote(ctx, tx, workspaceID, mutation)
	case model.MobileOperationNoteUpdate:
		entity, err = updateSQLiteMobileNote(ctx, tx, workspaceID, mutation)
	case model.MobileOperationNoteDelete:
		entity, err = deleteSQLiteMobileNote(ctx, tx, workspaceID, mutation)
	default:
		err = fmt.Errorf("unsupported mobile note operation %q", mutation.Operation)
	}
	if err != nil {
		return nil, err
	}
	result := &model.MobileMutationResult{MutationID: mutation.MutationID, Status: model.MobileMutationApplied, Entity: entity}
	if err := persistSQLiteMutationResult(ctx, tx, workspaceID, mutation, result); err != nil {
		return nil, err
	}
	return result, nil
}

func getSQLiteMutationReceipt(ctx context.Context, tx *sql.Tx, workspaceID string, mutation model.MobileNoteMutation) (*model.MobileMutationResult, bool, error) {
	var requestHash string
	var responseJSON string
	err := tx.QueryRowContext(ctx, `
		SELECT request_sha256, response_json
		FROM mobile_mutation_receipts
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
		return nil, false, fmt.Errorf("decode mutation receipt: %w", err)
	}
	return &result, true, nil
}

func createSQLiteMobileNote(ctx context.Context, tx *sql.Tx, workspaceID string, mutation model.MobileNoteMutation) (*model.MobileEntityEnvelope, error) {
	if mutation.Payload.Title == nil || strings.TrimSpace(*mutation.Payload.Title) == "" {
		return nil, errors.New("note.create requires a non-empty title")
	}
	var retired int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM mobile_retired_ids
		WHERE workspace_id = ? AND entity_type = 'note' AND client_id = ?
	`, workspaceID, mutation.EntityClientID).Scan(&retired); err != nil {
		return nil, err
	}
	if retired > 0 {
		return nil, storage.ErrMobileEntityGone
	}
	body := valueOrDefault(mutation.Payload.Body, "")
	folderID := valueOrDefault(mutation.Payload.FolderID, "__uncategorized")
	tags, err := normalizeTagsJSON(valueOrDefault(mutation.Payload.Tags, "[]"))
	if err != nil {
		return nil, err
	}
	now := nowUnix()
	noteID := uuid.NewString()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO notes (id, client_id, revision, title, body, folder_id, tags, created_at, updated_at, workspace_id)
		VALUES (?, ?, 1, ?, ?, ?, ?, ?, ?, ?)
	`, noteID, mutation.EntityClientID, strings.TrimSpace(*mutation.Payload.Title), body, folderID, tags, now, now, workspaceID)
	if err != nil {
		return nil, err
	}
	return getSQLiteMobileNote(ctx, tx, workspaceID, mutation.EntityClientID)
}

func updateSQLiteMobileNote(ctx context.Context, tx *sql.Tx, workspaceID string, mutation model.MobileNoteMutation) (*model.MobileEntityEnvelope, error) {
	current, err := loadSQLiteMobileNoteRow(ctx, tx, workspaceID, mutation.EntityClientID)
	if err != nil {
		return nil, err
	}
	if current.DeletedAt.Valid {
		return nil, storage.ErrMobileEntityGone
	}
	if mutation.BaseRevision == nil || current.Revision != *mutation.BaseRevision {
		return nil, storage.ErrRevisionConflict
	}
	title := valueOrDefault(mutation.Payload.Title, current.Title)
	if strings.TrimSpace(title) == "" {
		return nil, errors.New("note title must not be empty")
	}
	body := valueOrDefault(mutation.Payload.Body, current.Body)
	folderID := valueOrDefault(mutation.Payload.FolderID, current.FolderID)
	tags := current.Tags
	if mutation.Payload.Tags != nil {
		tags, err = normalizeTagsJSON(*mutation.Payload.Tags)
		if err != nil {
			return nil, err
		}
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE notes
		SET title = ?, body = ?, folder_id = ?, tags = ?, revision = revision + 1, updated_at = ?
		WHERE workspace_id = ? AND client_id = ? AND revision = ? AND deleted_at IS NULL
	`, strings.TrimSpace(title), body, folderID, tags, nowUnix(), workspaceID, mutation.EntityClientID, current.Revision)
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
	return getSQLiteMobileNote(ctx, tx, workspaceID, mutation.EntityClientID)
}

func deleteSQLiteMobileNote(ctx context.Context, tx *sql.Tx, workspaceID string, mutation model.MobileNoteMutation) (*model.MobileEntityEnvelope, error) {
	current, err := loadSQLiteMobileNoteRow(ctx, tx, workspaceID, mutation.EntityClientID)
	if err != nil {
		return nil, err
	}
	if current.DeletedAt.Valid {
		return nil, storage.ErrMobileEntityGone
	}
	if mutation.BaseRevision == nil || current.Revision != *mutation.BaseRevision {
		return nil, storage.ErrRevisionConflict
	}
	now := nowUnix()
	result, err := tx.ExecContext(ctx, `
		UPDATE notes
		SET deleted_at = ?, revision = revision + 1, updated_at = ?
		WHERE workspace_id = ? AND client_id = ? AND revision = ? AND deleted_at IS NULL
	`, now, now, workspaceID, mutation.EntityClientID, current.Revision)
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
		INSERT OR IGNORE INTO mobile_retired_ids (workspace_id, entity_type, client_id, retired_at)
		VALUES (?, 'note', ?, ?)
	`, workspaceID, mutation.EntityClientID, now); err != nil {
		return nil, err
	}
	return getSQLiteMobileNote(ctx, tx, workspaceID, mutation.EntityClientID)
}

func persistSQLiteMutationResult(ctx context.Context, tx *sql.Tx, workspaceID string, mutation model.MobileNoteMutation, result *model.MobileMutationResult) error {
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
		) VALUES (?, ?, 'note', ?, ?, ?, ?, ?)
	`, workspaceID, mutation.MutationID, mutation.EntityClientID, mutation.Operation, result.Entity.Revision, string(entityJSON), now); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO mobile_mutation_receipts (
			workspace_id, device_client_id, mutation_id, request_sha256, response_json, created_at
		) VALUES (?, ?, ?, ?, ?, ?)
	`, workspaceID, mutation.DeviceClientID, mutation.MutationID, mutation.RequestSHA256, string(responseJSON), now)
	return err
}

func persistSQLiteServerNoteChange(ctx context.Context, tx *sql.Tx, workspaceID, mutationID, operation, clientID string, now int64) error {
	entity, err := getSQLiteMobileNote(ctx, tx, workspaceID, clientID)
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
		) VALUES (?, ?, 'note', ?, ?, ?, ?, ?)
	`, workspaceID, mutationID, clientID, operation, entity.Revision, string(entityJSON), now)
	return err
}

type sqliteMobileNoteRow struct {
	ID        string
	ClientID  string
	Revision  int64
	DeletedAt sql.NullInt64
	Title     string
	Body      string
	FolderID  string
	Tags      string
}

func loadSQLiteMobileNoteRow(ctx context.Context, runner sqliteRunner, workspaceID, clientID string) (*sqliteMobileNoteRow, error) {
	var row sqliteMobileNoteRow
	err := runner.QueryRowContext(ctx, `
		SELECT id, client_id, revision, deleted_at, title, body, folder_id, tags
		FROM notes WHERE workspace_id = ? AND client_id = ?
	`, workspaceID, clientID).Scan(&row.ID, &row.ClientID, &row.Revision, &row.DeletedAt, &row.Title, &row.Body, &row.FolderID, &row.Tags)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storage.ErrMobileEntityNotFound
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func getSQLiteMobileNote(ctx context.Context, runner sqliteRunner, workspaceID, clientID string) (*model.MobileEntityEnvelope, error) {
	row, err := loadSQLiteMobileNoteRow(ctx, runner, workspaceID, clientID)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(map[string]string{
		"title":     row.Title,
		"body":      row.Body,
		"folder_id": row.FolderID,
		"tags":      row.Tags,
	})
	if err != nil {
		return nil, err
	}
	entity := &model.MobileEntityEnvelope{
		EntityType: "note",
		ID:         row.ID,
		ClientID:   row.ClientID,
		Revision:   row.Revision,
		Payload:    payload,
	}
	if row.DeletedAt.Valid {
		deletedAt := row.DeletedAt.Int64
		entity.DeletedAt = &deletedAt
	}
	return entity, nil
}

func validateMobileNoteMutation(mutation model.MobileNoteMutation) error {
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

func valueOrDefault(value *string, fallback string) string {
	if value == nil {
		return fallback
	}
	return *value
}
