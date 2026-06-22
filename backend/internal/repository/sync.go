package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

var (
	ErrSyncTargetInUse          = errors.New("sync target is in use")
	ErrSyncTargetIdentityLocked = errors.New("sync target identity is locked")
)

type SyncTargetIdentityChangedFunc func(existing, proposed *model.SyncTarget) (bool, error)

func SaveSyncTarget(target *model.SyncTarget) error {
	if store := CurrentStore(); store != nil {
		return store.Sync().SaveTarget(context.Background(), target)
	}

	tx, err := DB.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := saveSyncTargetWithRunner(tx, target); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func UpdateSyncTargetGuarded(target *model.SyncTarget, preserveIsDefault bool, identityChanged SyncTargetIdentityChangedFunc) error {
	if target == nil {
		return errors.New("sync target is nil")
	}
	ctx := context.Background()
	if store := CurrentStore(); store != nil {
		return store.Transact(ctx, func(txStore storage.Store) error {
			existing, err := txStore.Sync().LockTarget(ctx, target.ID)
			if err != nil {
				return err
			}
			target.CreatedAt = existing.CreatedAt
			if preserveIsDefault {
				target.IsDefault = existing.IsDefault
			}
			inUse, err := syncTargetInUseWithRepository(ctx, txStore.Sync(), target.ID)
			if err != nil {
				return err
			}
			if inUse {
				changed, err := identityChanged(existing, target)
				if err != nil {
					return err
				}
				if changed {
					return ErrSyncTargetIdentityLocked
				}
			}
			return txStore.Sync().SaveTarget(ctx, target)
		})
	}

	tx, err := DB.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	existing, err := lockSyncTargetWithRunner(tx, target.ID)
	if err != nil {
		return err
	}
	target.CreatedAt = existing.CreatedAt
	if preserveIsDefault {
		target.IsDefault = existing.IsDefault
	}
	inUse, err := syncTargetInUseWithRunner(tx, target.ID)
	if err != nil {
		return err
	}
	if inUse {
		changed, err := identityChanged(existing, target)
		if err != nil {
			return err
		}
		if changed {
			return ErrSyncTargetIdentityLocked
		}
	}
	if err := saveSyncTargetWithRunner(tx, target); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func GetSyncTarget(targetID string) (*model.SyncTarget, error) {
	if store := CurrentStore(); store != nil {
		return store.Sync().GetTarget(context.Background(), targetID)
	}

	return getSyncTargetWithRunner(DB, targetID)
}

func DeleteSyncTarget(targetID string) error {
	if store := CurrentStore(); store != nil {
		return store.Sync().DeleteTarget(context.Background(), targetID)
	}

	_, err := DB.Exec(`
		DELETE FROM sync_targets
		WHERE id = ?
	`, targetID)
	return err
}

func DeleteSyncTargetGuarded(targetID string) error {
	ctx := context.Background()
	if store := CurrentStore(); store != nil {
		return store.Transact(ctx, func(txStore storage.Store) error {
			if _, err := txStore.Sync().LockTarget(ctx, targetID); err != nil {
				return err
			}
			inUse, err := syncTargetInUseWithRepository(ctx, txStore.Sync(), targetID)
			if err != nil {
				return err
			}
			if inUse {
				return ErrSyncTargetInUse
			}
			return txStore.Sync().DeleteTarget(ctx, targetID)
		})
	}

	tx, err := DB.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if _, err := lockSyncTargetWithRunner(tx, targetID); err != nil {
		return err
	}
	inUse, err := syncTargetInUseWithRunner(tx, targetID)
	if err != nil {
		return err
	}
	if inUse {
		return ErrSyncTargetInUse
	}
	if _, err := tx.Exec(`
		DELETE FROM sync_targets
		WHERE id = ?
	`, targetID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func CountSyncBindingsByTarget(targetID string) (int, error) {
	if store := CurrentStore(); store != nil {
		return store.Sync().CountBindingsByTarget(context.Background(), targetID)
	}
	return scanCount(DB.QueryRow(`SELECT COUNT(*) FROM note_sync_bindings WHERE target_id = ?`, targetID))
}

func CountSyncClaimsByTarget(targetID string) (int, error) {
	if store := CurrentStore(); store != nil {
		return store.Sync().CountClaimsByTarget(context.Background(), targetID)
	}
	return scanCount(DB.QueryRow(`SELECT COUNT(*) FROM sync_external_claims WHERE target_id = ?`, targetID))
}

func CountSyncStatesByTarget(targetID string) (int, error) {
	if store := CurrentStore(); store != nil {
		return store.Sync().CountStatesByTarget(context.Background(), targetID)
	}
	return scanCount(DB.QueryRow(`SELECT COUNT(*) FROM note_sync_state WHERE target_id = ?`, targetID))
}

func GetDefaultSyncTarget(syncType string) (*model.SyncTarget, error) {
	if store := CurrentStore(); store != nil {
		return store.Sync().GetDefaultTarget(context.Background(), syncType)
	}

	var target model.SyncTarget
	var enabled int
	var autoSync int
	var isDefault int
	err := DB.QueryRow(`
		SELECT id, type, name, vault_path, base_folder, config_json, enabled, auto_sync, is_default, created_at, updated_at
		FROM sync_targets
		WHERE type = ? AND enabled = 1 AND is_default = 1
		LIMIT 1
	`, syncType).Scan(&target.ID, &target.Type, &target.Name, &target.VaultPath, &target.BaseFolder, &target.ConfigJSON, &enabled, &autoSync, &isDefault, &target.CreatedAt, &target.UpdatedAt)
	if err != nil {
		return nil, err
	}
	target.Enabled = enabled == 1
	target.AutoSync = autoSync == 1
	target.IsDefault = isDefault == 1
	return &target, nil
}

func ListSyncTargets() ([]model.SyncTarget, error) {
	if store := CurrentStore(); store != nil {
		return store.Sync().ListTargets(context.Background())
	}

	rows, err := DB.Query(`
		SELECT id, type, name, vault_path, base_folder, config_json, enabled, auto_sync, is_default, created_at, updated_at
		FROM sync_targets
		ORDER BY updated_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	targets := make([]model.SyncTarget, 0)
	for rows.Next() {
		target, err := scanSyncTarget(rows)
		if err != nil {
			return nil, err
		}
		targets = append(targets, *target)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return targets, nil
}

func UpsertSyncState(state *model.SyncState) error {
	if store := CurrentStore(); store != nil {
		return store.Sync().UpsertState(context.Background(), state)
	}

	_, err := DB.Exec(`
		INSERT INTO note_sync_state (
			note_id, target_id, external_path, external_id, external_url, content_hash, external_hash, external_mtime,
			last_direction, last_synced_at, status, error_message
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(note_id, target_id) DO UPDATE SET
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
	`, state.NoteID, state.TargetID, state.ExternalPath, state.ExternalID, state.ExternalURL, state.ContentHash, state.ExternalHash, state.ExternalMTime, state.LastDirection, state.LastSyncedAt, state.Status, state.ErrorMessage)
	return err
}

func GetSyncState(noteID, targetID string) (*model.SyncState, error) {
	if store := CurrentStore(); store != nil {
		return store.Sync().GetState(context.Background(), noteID, targetID)
	}

	var state model.SyncState
	err := DB.QueryRow(`
		SELECT note_id, target_id, external_path, COALESCE(external_id, ''), COALESCE(external_url, ''), content_hash, COALESCE(external_hash, ''), external_mtime,
			COALESCE(last_direction, ''), last_synced_at, status, error_message
		FROM note_sync_state
		WHERE note_id = ? AND target_id = ?
	`, noteID, targetID).Scan(
		&state.NoteID,
		&state.TargetID,
		&state.ExternalPath,
		&state.ExternalID,
		&state.ExternalURL,
		&state.ContentHash,
		&state.ExternalHash,
		&state.ExternalMTime,
		&state.LastDirection,
		&state.LastSyncedAt,
		&state.Status,
		&state.ErrorMessage,
	)
	if err != nil {
		return nil, err
	}
	return &state, nil
}

func ListSyncStatesByTarget(targetID string) ([]model.SyncState, error) {
	if store := CurrentStore(); store != nil {
		return store.Sync().ListStatesByTarget(context.Background(), targetID)
	}

	rows, err := DB.Query(`
		SELECT note_id, target_id, external_path, COALESCE(external_id, ''), COALESCE(external_url, ''), content_hash, COALESCE(external_hash, ''), external_mtime,
			COALESCE(last_direction, ''), last_synced_at, status, error_message
		FROM note_sync_state
		WHERE target_id = ?
		ORDER BY note_id
	`, targetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	states := make([]model.SyncState, 0)
	for rows.Next() {
		var state model.SyncState
		if err := rows.Scan(
			&state.NoteID,
			&state.TargetID,
			&state.ExternalPath,
			&state.ExternalID,
			&state.ExternalURL,
			&state.ContentHash,
			&state.ExternalHash,
			&state.ExternalMTime,
			&state.LastDirection,
			&state.LastSyncedAt,
			&state.Status,
			&state.ErrorMessage,
		); err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return states, nil
}

func DeleteSyncState(noteID, targetID string) error {
	if store := CurrentStore(); store != nil {
		return store.Sync().DeleteState(context.Background(), noteID, targetID)
	}

	_, err := DB.Exec(`
		DELETE FROM note_sync_state
		WHERE note_id = ? AND target_id = ?
	`, noteID, targetID)
	return err
}

func ListExternalDeletedSyncStates(targetID string) ([]model.ExternalDeletedNote, error) {
	if store := CurrentStore(); store != nil {
		return store.Sync().ListExternalDeletedStates(context.Background(), targetID)
	}

	rows, err := DB.Query(`
		SELECT s.note_id, n.title, s.external_path, s.last_synced_at
		FROM note_sync_state s
		JOIN notes n ON n.id = s.note_id
		WHERE s.target_id = ? AND s.status = 'external_deleted'
		ORDER BY s.last_synced_at DESC, n.title ASC
	`, targetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]model.ExternalDeletedNote, 0)
	for rows.Next() {
		var item model.ExternalDeletedNote
		if err := rows.Scan(&item.NoteID, &item.Title, &item.ExternalPath, &item.LastSyncedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

type syncTargetScanner interface {
	Scan(dest ...interface{}) error
}

type legacySyncTargetRunner interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
	QueryRow(query string, args ...interface{}) *sql.Row
}

func saveSyncTargetWithRunner(r legacySyncTargetRunner, target *model.SyncTarget) error {
	now := nowUnix()
	if target.ID == "" {
		target.ID = newUUID()
	}
	if target.CreatedAt == 0 {
		target.CreatedAt = now
	}
	target.UpdatedAt = now
	if target.IsDefault {
		if _, err := r.Exec(`
			UPDATE sync_targets
			SET is_default = 0, updated_at = ?
			WHERE type = ? AND id <> ? AND is_default = 1
		`, target.UpdatedAt, target.Type, target.ID); err != nil {
			return err
		}
	}
	_, err := r.Exec(`
		INSERT INTO sync_targets (id, type, name, vault_path, base_folder, config_json, enabled, auto_sync, is_default, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			type = excluded.type,
			name = excluded.name,
			vault_path = excluded.vault_path,
			base_folder = excluded.base_folder,
			config_json = excluded.config_json,
			enabled = excluded.enabled,
			auto_sync = excluded.auto_sync,
			is_default = excluded.is_default,
			updated_at = excluded.updated_at
	`, target.ID, target.Type, target.Name, target.VaultPath, target.BaseFolder, target.ConfigJSON, boolToInt(target.Enabled), boolToInt(target.AutoSync), boolToInt(target.IsDefault), target.CreatedAt, target.UpdatedAt)
	return err
}

func getSyncTargetWithRunner(r legacySyncTargetRunner, targetID string) (*model.SyncTarget, error) {
	return scanSyncTarget(r.QueryRow(`
		SELECT id, type, name, vault_path, base_folder, config_json, enabled, auto_sync, is_default, created_at, updated_at
		FROM sync_targets
		WHERE id = ?
	`, targetID))
}

func lockSyncTargetWithRunner(r legacySyncTargetRunner, targetID string) (*model.SyncTarget, error) {
	result, err := r.Exec(`
		UPDATE sync_targets
		SET updated_at = updated_at
		WHERE id = ?
	`, targetID)
	if err != nil {
		return nil, err
	}
	if rows, err := result.RowsAffected(); err == nil && rows == 0 {
		return nil, sql.ErrNoRows
	}
	return getSyncTargetWithRunner(r, targetID)
}

func syncTargetInUseWithRepository(ctx context.Context, repo storage.SyncRepository, targetID string) (bool, error) {
	bindings, err := repo.CountBindingsByTarget(ctx, targetID)
	if err != nil {
		return false, err
	}
	if bindings > 0 {
		return true, nil
	}
	claims, err := repo.CountClaimsByTarget(ctx, targetID)
	if err != nil {
		return false, err
	}
	if claims > 0 {
		return true, nil
	}
	states, err := repo.CountStatesByTarget(ctx, targetID)
	if err != nil {
		return false, err
	}
	return states > 0, nil
}

func syncTargetInUseWithRunner(r legacySyncTargetRunner, targetID string) (bool, error) {
	bindings, err := scanCount(r.QueryRow(`SELECT COUNT(*) FROM note_sync_bindings WHERE target_id = ?`, targetID))
	if err != nil {
		return false, err
	}
	if bindings > 0 {
		return true, nil
	}
	claims, err := scanCount(r.QueryRow(`SELECT COUNT(*) FROM sync_external_claims WHERE target_id = ?`, targetID))
	if err != nil {
		return false, err
	}
	if claims > 0 {
		return true, nil
	}
	states, err := scanCount(r.QueryRow(`SELECT COUNT(*) FROM note_sync_state WHERE target_id = ?`, targetID))
	if err != nil {
		return false, err
	}
	return states > 0, nil
}

func scanSyncTarget(row syncTargetScanner) (*model.SyncTarget, error) {
	var target model.SyncTarget
	var enabled int
	var autoSync int
	var isDefault int
	if err := row.Scan(&target.ID, &target.Type, &target.Name, &target.VaultPath, &target.BaseFolder, &target.ConfigJSON, &enabled, &autoSync, &isDefault, &target.CreatedAt, &target.UpdatedAt); err != nil {
		return nil, err
	}
	target.Enabled = enabled == 1
	target.AutoSync = autoSync == 1
	target.IsDefault = isDefault == 1
	return &target, nil
}

func scanCount(row *sql.Row) (int, error) {
	var count int
	err := row.Scan(&count)
	return count, err
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
