package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

type syncRepository struct {
	db postgresRunner
}

func (r syncRepository) SaveTarget(ctx context.Context, target *model.SyncTarget) error {
	if target == nil {
		return fmt.Errorf("sync target is nil")
	}
	config, err := normalizeJSONObjectString(target.ConfigJSON)
	if err != nil {
		return err
	}
	target.ConfigJSON = config
	now := nowUnix()
	if strings.TrimSpace(target.ID) == "" {
		target.ID = newID()
	}
	if target.CreatedAt == 0 {
		target.CreatedAt = now
	}
	target.UpdatedAt = now
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO sync_targets (id, type, name, vault_path, base_folder, config, enabled, auto_sync, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8, $9, $10)
		ON CONFLICT (id) DO UPDATE SET
			name = excluded.name,
			vault_path = excluded.vault_path,
			base_folder = excluded.base_folder,
			config = excluded.config,
			enabled = excluded.enabled,
			auto_sync = excluded.auto_sync,
			updated_at = excluded.updated_at
	`, target.ID, target.Type, target.Name, target.VaultPath, target.BaseFolder, target.ConfigJSON, target.Enabled, target.AutoSync, unixToTime(target.CreatedAt), unixToTime(target.UpdatedAt))
	return err
}

func (r syncRepository) GetTarget(ctx context.Context, targetID string) (*model.SyncTarget, error) {
	return nil, storage.ErrNotImplemented
}

func (r syncRepository) GetDefaultTarget(ctx context.Context, syncType string) (*model.SyncTarget, error) {
	return scanPostgresSyncTarget(r.db.QueryRowContext(ctx, `
		SELECT id, type, name, vault_path, base_folder, config::text, enabled, auto_sync, created_at, updated_at
		FROM sync_targets
		WHERE type = $1 AND enabled = true
		ORDER BY updated_at DESC
		LIMIT 1
	`, syncType))
}

func (r syncRepository) ListTargets(ctx context.Context) ([]model.SyncTarget, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, type, name, vault_path, base_folder, config::text, enabled, auto_sync, created_at, updated_at
		FROM sync_targets
		ORDER BY updated_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	targets := make([]model.SyncTarget, 0)
	for rows.Next() {
		target, err := scanPostgresSyncTarget(rows)
		if err != nil {
			return nil, err
		}
		targets = append(targets, *target)
	}
	return targets, rows.Err()
}

func (r syncRepository) DeleteTarget(ctx context.Context, targetID string) error {
	return storage.ErrNotImplemented
}

func (r syncRepository) CountBindingsByTarget(ctx context.Context, targetID string) (int, error) {
	return 0, storage.ErrNotImplemented
}

func (r syncRepository) CountClaimsByTarget(ctx context.Context, targetID string) (int, error) {
	return 0, storage.ErrNotImplemented
}

func (r syncRepository) CountStatesByTarget(ctx context.Context, targetID string) (int, error) {
	return 0, storage.ErrNotImplemented
}

func (r syncRepository) UpsertState(ctx context.Context, state *model.SyncState) error {
	if state == nil {
		return fmt.Errorf("sync state is nil")
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO note_sync_state (
			note_id, target_id, external_path, external_id, external_url, content_hash, external_hash, external_mtime,
			last_direction, last_synced_at, status, error_message
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (note_id, target_id) DO UPDATE SET
			external_path = excluded.external_path,
			external_id = excluded.external_id,
			external_url = excluded.external_url,
			content_hash = excluded.content_hash,
			external_hash = excluded.external_hash,
			external_mtime = excluded.external_mtime,
			last_direction = excluded.last_direction,
			last_synced_at = excluded.last_synced_at,
			status = excluded.status,
			error_message = excluded.error_message
	`, state.NoteID, state.TargetID, state.ExternalPath, nullableString(state.ExternalID), nullableString(state.ExternalURL), state.ContentHash, nullableString(state.ExternalHash), unixPtrToTime(state.ExternalMTime), nullableString(state.LastDirection), unixPtrToTime(state.LastSyncedAt), state.Status, state.ErrorMessage)
	return err
}

func (r syncRepository) GetState(ctx context.Context, noteID, targetID string) (*model.SyncState, error) {
	return scanPostgresSyncState(r.db.QueryRowContext(ctx, postgresSyncStateSelectSQL()+`
		WHERE note_id = $1 AND target_id = $2
	`, noteID, targetID))
}

func (r syncRepository) ListStatesByTarget(ctx context.Context, targetID string) ([]model.SyncState, error) {
	rows, err := r.db.QueryContext(ctx, postgresSyncStateSelectSQL()+`
		WHERE target_id = $1
		ORDER BY note_id
	`, targetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	states := make([]model.SyncState, 0)
	for rows.Next() {
		state, err := scanPostgresSyncState(rows)
		if err != nil {
			return nil, err
		}
		states = append(states, *state)
	}
	return states, rows.Err()
}

func (r syncRepository) DeleteState(ctx context.Context, noteID, targetID string) error {
	_, err := r.db.ExecContext(ctx, `
		DELETE FROM note_sync_state WHERE note_id = $1 AND target_id = $2
	`, noteID, targetID)
	return err
}

func (r syncRepository) ListExternalDeletedStates(ctx context.Context, targetID string) ([]model.ExternalDeletedNote, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT s.note_id, n.title, s.external_path, s.last_synced_at
		FROM note_sync_state s
		JOIN notes n ON n.id = s.note_id
		WHERE s.target_id = $1 AND s.status = 'external_deleted'
		ORDER BY s.last_synced_at DESC, n.title ASC
	`, targetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]model.ExternalDeletedNote, 0)
	for rows.Next() {
		var item model.ExternalDeletedNote
		var lastSyncedAt sql.NullTime
		if err := rows.Scan(&item.NoteID, &item.Title, &item.ExternalPath, &lastSyncedAt); err != nil {
			return nil, err
		}
		item.LastSyncedAt = nullTimeToUnixPtr(lastSyncedAt)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r syncRepository) GetBinding(ctx context.Context, noteID string) (*model.NoteSyncBinding, error) {
	return nil, storage.ErrNotImplemented
}

func (r syncRepository) PutBinding(ctx context.Context, binding model.NoteSyncBinding) error {
	return storage.ErrNotImplemented
}

func (r syncRepository) DeleteBinding(ctx context.Context, noteID string) error {
	return storage.ErrNotImplemented
}

func (r syncRepository) ListBindingsByTarget(ctx context.Context, targetID string) ([]model.NoteSyncBinding, error) {
	return nil, storage.ErrNotImplemented
}

func (r syncRepository) GetExternalClaim(ctx context.Context, externalKey string) (*model.SyncExternalClaim, error) {
	return nil, storage.ErrNotImplemented
}

func (r syncRepository) GetExternalClaimByNote(ctx context.Context, noteID string) (*model.SyncExternalClaim, error) {
	return nil, storage.ErrNotImplemented
}

func (r syncRepository) PutExternalClaim(ctx context.Context, claim model.SyncExternalClaim) error {
	return storage.ErrNotImplemented
}

func (r syncRepository) ReleaseExternalClaim(ctx context.Context, noteID string) error {
	return storage.ErrNotImplemented
}

func (r syncRepository) PutSuppression(ctx context.Context, suppression model.NoteSyncSuppression) error {
	return storage.ErrNotImplemented
}

func (r syncRepository) DeleteSuppression(ctx context.Context, noteID string, targetID string) error {
	return storage.ErrNotImplemented
}

func (r syncRepository) GetSuppression(ctx context.Context, noteID string, targetID string) (*model.NoteSyncSuppression, error) {
	return nil, storage.ErrNotImplemented
}

func (r syncRepository) PutImportTombstone(ctx context.Context, tombstone model.SyncImportTombstone) error {
	return storage.ErrNotImplemented
}

func (r syncRepository) DeleteImportTombstone(ctx context.Context, externalKey string) error {
	return storage.ErrNotImplemented
}

func (r syncRepository) DeleteImportTombstonesForNoteTarget(ctx context.Context, noteID string, targetID string) error {
	return storage.ErrNotImplemented
}

func (r syncRepository) FindImportTombstone(ctx context.Context, targetID string, externalKey string, formerNoteID string, externalType string) (*model.SyncImportTombstone, error) {
	return nil, storage.ErrNotImplemented
}

func scanPostgresSyncTarget(row rowScanner) (*model.SyncTarget, error) {
	var target model.SyncTarget
	var createdAt time.Time
	var updatedAt time.Time
	if err := row.Scan(&target.ID, &target.Type, &target.Name, &target.VaultPath, &target.BaseFolder, &target.ConfigJSON, &target.Enabled, &target.AutoSync, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	target.CreatedAt = timeToUnix(createdAt)
	target.UpdatedAt = timeToUnix(updatedAt)
	return &target, nil
}

func postgresSyncStateSelectSQL() string {
	return `
		SELECT note_id, target_id, external_path, COALESCE(external_id, ''), COALESCE(external_url, ''), content_hash, COALESCE(external_hash, ''), external_mtime,
			COALESCE(last_direction, ''), last_synced_at, status, error_message
		FROM note_sync_state
	`
}

func scanPostgresSyncState(row rowScanner) (*model.SyncState, error) {
	var state model.SyncState
	var externalMTime sql.NullTime
	var lastSyncedAt sql.NullTime
	if err := row.Scan(
		&state.NoteID,
		&state.TargetID,
		&state.ExternalPath,
		&state.ExternalID,
		&state.ExternalURL,
		&state.ContentHash,
		&state.ExternalHash,
		&externalMTime,
		&state.LastDirection,
		&lastSyncedAt,
		&state.Status,
		&state.ErrorMessage,
	); err != nil {
		return nil, err
	}
	state.ExternalMTime = nullTimeToUnixPtr(externalMTime)
	state.LastSyncedAt = nullTimeToUnixPtr(lastSyncedAt)
	return &state, nil
}

func unixPtrToTime(value *int64) interface{} {
	if value == nil {
		return nil
	}
	return unixToTime(*value)
}

func nullTimeToUnixPtr(value sql.NullTime) *int64 {
	if !value.Valid {
		return nil
	}
	unix := timeToUnix(value.Time)
	return &unix
}

func nullableString(value string) interface{} {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}
