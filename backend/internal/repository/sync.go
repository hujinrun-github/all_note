package repository

import "github.com/hujinrun/flowspace/internal/model"

func SaveSyncTarget(target *model.SyncTarget) error {
	now := nowUnix()
	if target.ID == "" {
		target.ID = newUUID()
	}
	if target.CreatedAt == 0 {
		target.CreatedAt = now
	}
	target.UpdatedAt = now

	_, err := DB.Exec(`
		INSERT INTO sync_targets (id, type, name, vault_path, base_folder, config_json, enabled, auto_sync, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			vault_path = excluded.vault_path,
			base_folder = excluded.base_folder,
			config_json = excluded.config_json,
			enabled = excluded.enabled,
			auto_sync = excluded.auto_sync,
			updated_at = excluded.updated_at
	`, target.ID, target.Type, target.Name, target.VaultPath, target.BaseFolder, target.ConfigJSON, boolToInt(target.Enabled), boolToInt(target.AutoSync), target.CreatedAt, target.UpdatedAt)
	return err
}

func GetDefaultSyncTarget(syncType string) (*model.SyncTarget, error) {
	var target model.SyncTarget
	var enabled int
	var autoSync int
	err := DB.QueryRow(`
		SELECT id, type, name, vault_path, base_folder, config_json, enabled, auto_sync, created_at, updated_at
		FROM sync_targets
		WHERE type = ? AND enabled = 1
		ORDER BY updated_at DESC
		LIMIT 1
	`, syncType).Scan(&target.ID, &target.Type, &target.Name, &target.VaultPath, &target.BaseFolder, &target.ConfigJSON, &enabled, &autoSync, &target.CreatedAt, &target.UpdatedAt)
	if err != nil {
		return nil, err
	}
	target.Enabled = enabled == 1
	target.AutoSync = autoSync == 1
	return &target, nil
}

func ListSyncTargets() ([]model.SyncTarget, error) {
	rows, err := DB.Query(`
		SELECT id, type, name, vault_path, base_folder, config_json, enabled, auto_sync, created_at, updated_at
		FROM sync_targets
		ORDER BY updated_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	targets := make([]model.SyncTarget, 0)
	for rows.Next() {
		var target model.SyncTarget
		var enabled int
		var autoSync int
		if err := rows.Scan(&target.ID, &target.Type, &target.Name, &target.VaultPath, &target.BaseFolder, &target.ConfigJSON, &enabled, &autoSync, &target.CreatedAt, &target.UpdatedAt); err != nil {
			return nil, err
		}
		target.Enabled = enabled == 1
		target.AutoSync = autoSync == 1
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return targets, nil
}

func UpsertSyncState(state *model.SyncState) error {
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
	_, err := DB.Exec(`
		DELETE FROM note_sync_state
		WHERE note_id = ? AND target_id = ?
	`, noteID, targetID)
	return err
}

func ListExternalDeletedSyncStates(targetID string) ([]model.ExternalDeletedNote, error) {
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

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
