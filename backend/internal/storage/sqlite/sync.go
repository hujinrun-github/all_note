package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/model"
)

type syncRepository struct {
	db sqliteRunner
}

func (r syncRepository) withTx(ctx context.Context, fn func(sqliteRunner) error) error {
	if tx, ok := r.db.(*sql.Tx); ok {
		return fn(tx)
	}
	db, ok := r.db.(*sql.DB)
	if !ok {
		return fmt.Errorf("unsupported sqlite runner %T", r.db)
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

func (r syncRepository) SaveTarget(ctx context.Context, target *model.SyncTarget) error {
	if target == nil {
		return fmt.Errorf("sync target is nil")
	}
	if err := validateSyncTargetType(target.Type); err != nil {
		return err
	}
	config, err := normalizeSyncConfigJSON(target.ConfigJSON)
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
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	return r.withTx(ctx, func(tx sqliteRunner) error {
		if target.IsDefault {
			if _, err := tx.ExecContext(ctx, `
				UPDATE sync_targets
				SET is_default = 0, updated_at = ?
				WHERE workspace_id = ? AND type = ? AND id <> ? AND is_default = 1
			`, target.UpdatedAt, workspaceID, target.Type, target.ID); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO sync_targets (id, type, name, vault_path, base_folder, config_json, enabled, auto_sync, is_default, created_at, updated_at, workspace_id)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				type = excluded.type,
				name = excluded.name,
				vault_path = excluded.vault_path,
				base_folder = excluded.base_folder,
				config_json = excluded.config_json,
				enabled = excluded.enabled,
				auto_sync = excluded.auto_sync,
				is_default = excluded.is_default,
				updated_at = excluded.updated_at,
				workspace_id = excluded.workspace_id
		`, target.ID, target.Type, target.Name, target.VaultPath, target.BaseFolder, target.ConfigJSON, boolToSQLiteInt(target.Enabled), boolToSQLiteInt(target.AutoSync), boolToSQLiteInt(target.IsDefault), target.CreatedAt, target.UpdatedAt, workspaceID)
		return err
	})
}

func (r syncRepository) GetTarget(ctx context.Context, targetID string) (*model.SyncTarget, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return scanSQLiteSyncTarget(r.db.QueryRowContext(ctx, `
		SELECT id, type, name, vault_path, base_folder, config_json, enabled, auto_sync, is_default, created_at, updated_at
		FROM sync_targets
		WHERE workspace_id = ? AND id = ?
	`, workspaceID, targetID))
}

func (r syncRepository) LockTarget(ctx context.Context, targetID string) (*model.SyncTarget, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE sync_targets
		SET updated_at = updated_at
		WHERE workspace_id = ? AND id = ?
	`, workspaceID, targetID)
	if err != nil {
		return nil, err
	}
	if rows, err := result.RowsAffected(); err == nil && rows == 0 {
		return nil, sql.ErrNoRows
	}
	return r.GetTarget(ctx, targetID)
}

func (r syncRepository) GetDefaultTarget(ctx context.Context, syncType string) (*model.SyncTarget, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return scanSQLiteSyncTarget(r.db.QueryRowContext(ctx, `
		SELECT id, type, name, vault_path, base_folder, config_json, enabled, auto_sync, is_default, created_at, updated_at
		FROM sync_targets
		WHERE workspace_id = ? AND type = ? AND enabled = 1 AND is_default = 1
		LIMIT 1
	`, workspaceID, syncType))
}

func (r syncRepository) ListTargets(ctx context.Context) ([]model.SyncTarget, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, type, name, vault_path, base_folder, config_json, enabled, auto_sync, is_default, created_at, updated_at
		FROM sync_targets
		WHERE workspace_id = ?
		ORDER BY updated_at DESC
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	targets := make([]model.SyncTarget, 0)
	for rows.Next() {
		target, err := scanSQLiteSyncTarget(rows)
		if err != nil {
			return nil, err
		}
		targets = append(targets, *target)
	}
	return targets, rows.Err()
}

func (r syncRepository) DeleteTarget(ctx context.Context, targetID string) error {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
		DELETE FROM sync_targets WHERE workspace_id = ? AND id = ?
	`, workspaceID, targetID)
	return err
}

func (r syncRepository) CountBindingsByTarget(ctx context.Context, targetID string) (int, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return 0, err
	}
	return scanSQLiteCount(r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM note_sync_bindings WHERE workspace_id = ? AND target_id = ?
	`, workspaceID, targetID))
}

func (r syncRepository) CountClaimsByTarget(ctx context.Context, targetID string) (int, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return 0, err
	}
	return scanSQLiteCount(r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM sync_external_claims WHERE workspace_id = ? AND target_id = ?
	`, workspaceID, targetID))
}

func (r syncRepository) CountStatesByTarget(ctx context.Context, targetID string) (int, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return 0, err
	}
	return scanSQLiteCount(r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM note_sync_state WHERE workspace_id = ? AND target_id = ?
	`, workspaceID, targetID))
}

func (r syncRepository) UpsertState(ctx context.Context, state *model.SyncState) error {
	if state == nil {
		return fmt.Errorf("sync state is nil")
	}
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO note_sync_state (
			note_id, target_id, external_path, external_id, external_url, content_hash, external_hash, external_mtime,
			last_direction, last_synced_at, status, error_message, workspace_id
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(workspace_id, note_id, target_id) DO UPDATE SET
			external_path = excluded.external_path,
			external_id = excluded.external_id,
			external_url = excluded.external_url,
			content_hash = excluded.content_hash,
			external_hash = excluded.external_hash,
			external_mtime = excluded.external_mtime,
			last_direction = excluded.last_direction,
			last_synced_at = excluded.last_synced_at,
			status = excluded.status,
			error_message = excluded.error_message,
			workspace_id = excluded.workspace_id
	`, state.NoteID, state.TargetID, state.ExternalPath, state.ExternalID, state.ExternalURL, state.ContentHash, state.ExternalHash, state.ExternalMTime, state.LastDirection, state.LastSyncedAt, state.Status, state.ErrorMessage, workspaceID)
	return err
}

func (r syncRepository) GetState(ctx context.Context, noteID, targetID string) (*model.SyncState, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return scanSQLiteSyncState(r.db.QueryRowContext(ctx, sqliteSyncStateSelectSQL()+`
		WHERE workspace_id = ? AND note_id = ? AND target_id = ?
	`, workspaceID, noteID, targetID))
}

func (r syncRepository) ListStatesByTarget(ctx context.Context, targetID string) ([]model.SyncState, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, sqliteSyncStateSelectSQL()+`
		WHERE workspace_id = ? AND target_id = ?
		ORDER BY note_id
	`, workspaceID, targetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	states := make([]model.SyncState, 0)
	for rows.Next() {
		state, err := scanSQLiteSyncState(rows)
		if err != nil {
			return nil, err
		}
		states = append(states, *state)
	}
	return states, rows.Err()
}

func (r syncRepository) DeleteState(ctx context.Context, noteID, targetID string) error {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
		DELETE FROM note_sync_state WHERE workspace_id = ? AND note_id = ? AND target_id = ?
	`, workspaceID, noteID, targetID)
	return err
}

func (r syncRepository) ListExternalDeletedStates(ctx context.Context, targetID string) ([]model.ExternalDeletedNote, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT s.note_id, n.title, s.external_path, s.last_synced_at
		FROM note_sync_state s
		JOIN notes n ON n.workspace_id = s.workspace_id AND n.id = s.note_id
		WHERE s.workspace_id = ? AND s.target_id = ? AND s.status = 'external_deleted'
		ORDER BY s.last_synced_at DESC, n.title ASC
	`, workspaceID, targetID)
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
	return items, rows.Err()
}

func (r syncRepository) LockBindingSlot(ctx context.Context, noteID string) error {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
		UPDATE note_sync_bindings
		SET updated_at = updated_at
		WHERE workspace_id = ? AND note_id = ?
	`, workspaceID, noteID)
	return err
}

func (r syncRepository) GetBinding(ctx context.Context, noteID string) (*model.NoteSyncBinding, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return scanSQLiteNoteSyncBinding(r.db.QueryRowContext(ctx, sqliteBindingSelectSQL()+`
		WHERE workspace_id = ? AND note_id = ?
	`, workspaceID, noteID))
}

func (r syncRepository) PutBinding(ctx context.Context, binding model.NoteSyncBinding) error {
	now := nowUnix()
	if binding.CreatedAt == 0 {
		binding.CreatedAt = now
	}
	binding.UpdatedAt = now
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO note_sync_bindings (note_id, target_id, created_at, updated_at, workspace_id)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(workspace_id, note_id) DO UPDATE SET
			target_id = excluded.target_id,
			updated_at = excluded.updated_at,
			workspace_id = excluded.workspace_id
	`, binding.NoteID, binding.TargetID, binding.CreatedAt, binding.UpdatedAt, workspaceID)
	return err
}

func (r syncRepository) DeleteBinding(ctx context.Context, noteID string) error {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
		DELETE FROM note_sync_bindings WHERE workspace_id = ? AND note_id = ?
	`, workspaceID, noteID)
	return err
}

func (r syncRepository) ListBindingsByTarget(ctx context.Context, targetID string) ([]model.NoteSyncBinding, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, sqliteBindingSelectSQL()+`
		WHERE workspace_id = ? AND target_id = ?
		ORDER BY updated_at DESC, note_id
	`, workspaceID, targetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	bindings := make([]model.NoteSyncBinding, 0)
	for rows.Next() {
		binding, err := scanSQLiteNoteSyncBinding(rows)
		if err != nil {
			return nil, err
		}
		bindings = append(bindings, *binding)
	}
	return bindings, rows.Err()
}

func (r syncRepository) GetExternalClaim(ctx context.Context, externalKey string) (*model.SyncExternalClaim, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return scanSQLiteExternalClaim(r.db.QueryRowContext(ctx, sqliteExternalClaimSelectSQL()+`
		WHERE workspace_id = ? AND external_key = ?
	`, workspaceID, externalKey))
}

func (r syncRepository) GetExternalClaimByNote(ctx context.Context, noteID string) (*model.SyncExternalClaim, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return scanSQLiteExternalClaim(r.db.QueryRowContext(ctx, sqliteExternalClaimSelectSQL()+`
		WHERE workspace_id = ? AND note_id = ?
	`, workspaceID, noteID))
}

func (r syncRepository) PutExternalClaim(ctx context.Context, claim model.SyncExternalClaim) error {
	now := nowUnix()
	if claim.CreatedAt == 0 {
		claim.CreatedAt = now
	}
	claim.UpdatedAt = now
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	return r.withTx(ctx, func(tx sqliteRunner) error {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM sync_external_claims
			WHERE workspace_id = ? AND note_id = ? AND external_key <> ?
		`, workspaceID, claim.NoteID, claim.ExternalKey); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO sync_external_claims (
				external_key, note_id, target_id, external_type, external_id, external_path, created_at, updated_at, workspace_id
			)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(workspace_id, external_key) DO UPDATE SET
				note_id = excluded.note_id,
				target_id = excluded.target_id,
				external_type = excluded.external_type,
				external_id = excluded.external_id,
				external_path = excluded.external_path,
				updated_at = excluded.updated_at,
				workspace_id = excluded.workspace_id
		`, claim.ExternalKey, claim.NoteID, claim.TargetID, claim.ExternalType, claim.ExternalID, claim.ExternalPath, claim.CreatedAt, claim.UpdatedAt, workspaceID)
		return err
	})
}

func (r syncRepository) ReleaseExternalClaim(ctx context.Context, noteID string) error {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
		DELETE FROM sync_external_claims WHERE workspace_id = ? AND note_id = ?
	`, workspaceID, noteID)
	return err
}

func (r syncRepository) PutSuppression(ctx context.Context, suppression model.NoteSyncSuppression) error {
	now := nowUnix()
	if suppression.CreatedAt == 0 {
		suppression.CreatedAt = now
	}
	if strings.TrimSpace(suppression.Reason) == "" {
		suppression.Reason = "user_unbound"
	}
	suppression.UpdatedAt = now
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO note_sync_suppressions (note_id, target_id, reason, created_at, updated_at, workspace_id)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(workspace_id, note_id, target_id) DO UPDATE SET
			reason = excluded.reason,
			updated_at = excluded.updated_at,
			workspace_id = excluded.workspace_id
	`, suppression.NoteID, suppression.TargetID, suppression.Reason, suppression.CreatedAt, suppression.UpdatedAt, workspaceID)
	return err
}

func (r syncRepository) DeleteSuppression(ctx context.Context, noteID string, targetID string) error {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
		DELETE FROM note_sync_suppressions WHERE workspace_id = ? AND note_id = ? AND target_id = ?
	`, workspaceID, noteID, targetID)
	return err
}

func (r syncRepository) GetSuppression(ctx context.Context, noteID string, targetID string) (*model.NoteSyncSuppression, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return scanSQLiteSuppression(r.db.QueryRowContext(ctx, sqliteSuppressionSelectSQL()+`
		WHERE workspace_id = ? AND note_id = ? AND target_id = ?
	`, workspaceID, noteID, targetID))
}

func (r syncRepository) PutImportTombstone(ctx context.Context, tombstone model.SyncImportTombstone) error {
	now := nowUnix()
	if tombstone.CreatedAt == 0 {
		tombstone.CreatedAt = now
	}
	if strings.TrimSpace(tombstone.Reason) == "" {
		tombstone.Reason = "user_unbound"
	}
	tombstone.UpdatedAt = now
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	return r.withTx(ctx, func(tx sqliteRunner) error {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM sync_import_tombstones
			WHERE workspace_id = ?
				AND (external_key = ?
					OR (target_id = ? AND former_note_id = ? AND external_type = ?))
		`, workspaceID, tombstone.ExternalKey, tombstone.TargetID, tombstone.FormerNoteID, tombstone.ExternalType); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO sync_import_tombstones (
				external_key, target_id, former_note_id, external_type, external_id, external_path, reason, created_at, updated_at, workspace_id
			)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, tombstone.ExternalKey, tombstone.TargetID, tombstone.FormerNoteID, tombstone.ExternalType, tombstone.ExternalID, tombstone.ExternalPath, tombstone.Reason, tombstone.CreatedAt, tombstone.UpdatedAt, workspaceID)
		return err
	})
}

func (r syncRepository) DeleteImportTombstone(ctx context.Context, externalKey string) error {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
		DELETE FROM sync_import_tombstones WHERE workspace_id = ? AND external_key = ?
	`, workspaceID, externalKey)
	return err
}

func (r syncRepository) DeleteImportTombstonesForNoteTarget(ctx context.Context, noteID string, targetID string) error {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
		DELETE FROM sync_import_tombstones WHERE workspace_id = ? AND former_note_id = ? AND target_id = ?
	`, workspaceID, noteID, targetID)
	return err
}

func (r syncRepository) FindImportTombstone(ctx context.Context, targetID string, externalKey string, formerNoteID string, externalType string) (*model.SyncImportTombstone, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(targetID) != "" && strings.TrimSpace(externalKey) != "" {
		tombstone, err := scanSQLiteImportTombstone(r.db.QueryRowContext(ctx, sqliteImportTombstoneSelectSQL()+`
			WHERE workspace_id = ? AND target_id = ? AND external_key = ?
			ORDER BY updated_at DESC, created_at DESC
			LIMIT 1
		`, workspaceID, targetID, externalKey))
		if err == nil {
			return tombstone, nil
		}
		if err != sql.ErrNoRows {
			return nil, err
		}
	}
	if strings.TrimSpace(targetID) != "" && strings.TrimSpace(formerNoteID) != "" && strings.TrimSpace(externalType) != "" {
		tombstone, err := scanSQLiteImportTombstone(r.db.QueryRowContext(ctx, sqliteImportTombstoneSelectSQL()+`
			WHERE workspace_id = ? AND target_id = ? AND former_note_id = ? AND external_type = ?
			ORDER BY updated_at DESC, created_at DESC
			LIMIT 1
		`, workspaceID, targetID, formerNoteID, externalType))
		if err == nil {
			return tombstone, nil
		}
		if err != sql.ErrNoRows {
			return nil, err
		}
	}
	if strings.TrimSpace(formerNoteID) != "" && strings.TrimSpace(externalType) != "" {
		return scanSQLiteImportTombstone(r.db.QueryRowContext(ctx, sqliteImportTombstoneSelectSQL()+`
			WHERE workspace_id = ? AND former_note_id = ? AND external_type = ?
			ORDER BY updated_at DESC, created_at DESC
			LIMIT 1
		`, workspaceID, formerNoteID, externalType))
	}
	return nil, sql.ErrNoRows
}

func scanSQLiteSyncTarget(row sqliteRowScanner) (*model.SyncTarget, error) {
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

func scanSQLiteCount(row sqliteRowScanner) (int, error) {
	var count int
	err := row.Scan(&count)
	return count, err
}

func sqliteSyncStateSelectSQL() string {
	return `
		SELECT note_id, target_id, external_path, COALESCE(external_id, ''), COALESCE(external_url, ''), content_hash, COALESCE(external_hash, ''), external_mtime,
			COALESCE(last_direction, ''), last_synced_at, status, error_message
		FROM note_sync_state
	`
}

func scanSQLiteSyncState(row sqliteRowScanner) (*model.SyncState, error) {
	var state model.SyncState
	if err := row.Scan(
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
	return &state, nil
}

func sqliteBindingSelectSQL() string {
	return `
		SELECT note_id, target_id, created_at, updated_at
		FROM note_sync_bindings
	`
}

func scanSQLiteNoteSyncBinding(row sqliteRowScanner) (*model.NoteSyncBinding, error) {
	var binding model.NoteSyncBinding
	if err := row.Scan(&binding.NoteID, &binding.TargetID, &binding.CreatedAt, &binding.UpdatedAt); err != nil {
		return nil, err
	}
	return &binding, nil
}

func sqliteExternalClaimSelectSQL() string {
	return `
		SELECT external_key, note_id, target_id, external_type, external_id, external_path, created_at, updated_at
		FROM sync_external_claims
	`
}

func scanSQLiteExternalClaim(row sqliteRowScanner) (*model.SyncExternalClaim, error) {
	var claim model.SyncExternalClaim
	if err := row.Scan(
		&claim.ExternalKey,
		&claim.NoteID,
		&claim.TargetID,
		&claim.ExternalType,
		&claim.ExternalID,
		&claim.ExternalPath,
		&claim.CreatedAt,
		&claim.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &claim, nil
}

func sqliteSuppressionSelectSQL() string {
	return `
		SELECT note_id, target_id, reason, created_at, updated_at
		FROM note_sync_suppressions
	`
}

func scanSQLiteSuppression(row sqliteRowScanner) (*model.NoteSyncSuppression, error) {
	var suppression model.NoteSyncSuppression
	if err := row.Scan(&suppression.NoteID, &suppression.TargetID, &suppression.Reason, &suppression.CreatedAt, &suppression.UpdatedAt); err != nil {
		return nil, err
	}
	return &suppression, nil
}

func sqliteImportTombstoneSelectSQL() string {
	return `
		SELECT external_key, target_id, former_note_id, external_type, external_id, external_path, reason, created_at, updated_at
		FROM sync_import_tombstones
	`
}

func scanSQLiteImportTombstone(row sqliteRowScanner) (*model.SyncImportTombstone, error) {
	var tombstone model.SyncImportTombstone
	if err := row.Scan(
		&tombstone.ExternalKey,
		&tombstone.TargetID,
		&tombstone.FormerNoteID,
		&tombstone.ExternalType,
		&tombstone.ExternalID,
		&tombstone.ExternalPath,
		&tombstone.Reason,
		&tombstone.CreatedAt,
		&tombstone.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &tombstone, nil
}

func normalizeSyncConfigJSON(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "{}", nil
	}
	var value map[string]any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return "", err
	}
	if value == nil {
		return "", fmt.Errorf("sync target config_json must be a JSON object")
	}
	var compacted bytes.Buffer
	if err := json.Compact(&compacted, []byte(raw)); err != nil {
		return "", err
	}
	return compacted.String(), nil
}

func boolToSQLiteInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func validateSyncTargetType(targetType string) error {
	switch targetType {
	case "obsidian", "notion":
		return nil
	default:
		return fmt.Errorf("unsupported sync target type %q", targetType)
	}
}
