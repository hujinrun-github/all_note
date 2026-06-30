package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/model"
)

type syncRepository struct {
	db postgresRunner
}

func (r syncRepository) withTx(ctx context.Context, fn func(postgresRunner) error) error {
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

func (r syncRepository) SaveTarget(ctx context.Context, target *model.SyncTarget) error {
	if target == nil {
		return fmt.Errorf("sync target is nil")
	}
	if err := validateSyncTargetType(target.Type); err != nil {
		return err
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
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	return r.withTx(ctx, func(tx postgresRunner) error {
		if target.IsDefault {
			if _, err := tx.ExecContext(ctx, `
				UPDATE sync_targets
				SET is_default = false, updated_at = $1
				WHERE workspace_id = $2 AND type = $3 AND id <> $4 AND is_default = true
			`, unixToTime(target.UpdatedAt), workspaceID, target.Type, target.ID); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO sync_targets (id, type, name, vault_path, base_folder, config, enabled, auto_sync, is_default, created_at, updated_at, workspace_id)
			VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8, $9, $10, $11, $12)
			ON CONFLICT (id) DO UPDATE SET
				type = excluded.type,
				name = excluded.name,
				vault_path = excluded.vault_path,
				base_folder = excluded.base_folder,
				config = excluded.config,
				enabled = excluded.enabled,
				auto_sync = excluded.auto_sync,
				is_default = excluded.is_default,
				updated_at = excluded.updated_at,
				workspace_id = excluded.workspace_id
		`, target.ID, target.Type, target.Name, target.VaultPath, target.BaseFolder, target.ConfigJSON, target.Enabled, target.AutoSync, target.IsDefault, unixToTime(target.CreatedAt), unixToTime(target.UpdatedAt), workspaceID)
		return err
	})
}

func (r syncRepository) GetTarget(ctx context.Context, targetID string) (*model.SyncTarget, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return scanPostgresSyncTarget(r.db.QueryRowContext(ctx, `
		SELECT id, type, name, vault_path, base_folder, config::text, enabled, auto_sync, is_default, created_at, updated_at
		FROM sync_targets
		WHERE workspace_id = $1 AND id = $2
	`, workspaceID, targetID))
}

func (r syncRepository) LockTarget(ctx context.Context, targetID string) (*model.SyncTarget, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return scanPostgresSyncTarget(r.db.QueryRowContext(ctx, `
		SELECT id, type, name, vault_path, base_folder, config::text, enabled, auto_sync, is_default, created_at, updated_at
		FROM sync_targets
		WHERE workspace_id = $1 AND id = $2
		FOR UPDATE
	`, workspaceID, targetID))
}

func (r syncRepository) GetDefaultTarget(ctx context.Context, syncType string) (*model.SyncTarget, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return scanPostgresSyncTarget(r.db.QueryRowContext(ctx, `
		SELECT id, type, name, vault_path, base_folder, config::text, enabled, auto_sync, is_default, created_at, updated_at
		FROM sync_targets
		WHERE workspace_id = $1 AND type = $2 AND enabled = true AND is_default = true
		LIMIT 1
	`, workspaceID, syncType))
}

func (r syncRepository) ListTargets(ctx context.Context) ([]model.SyncTarget, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, type, name, vault_path, base_folder, config::text, enabled, auto_sync, is_default, created_at, updated_at
		FROM sync_targets
		WHERE workspace_id = $1
		ORDER BY updated_at DESC
	`, workspaceID)
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
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
		DELETE FROM sync_targets WHERE workspace_id = $1 AND id = $2
	`, workspaceID, targetID)
	return err
}

func (r syncRepository) CountBindingsByTarget(ctx context.Context, targetID string) (int, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return 0, err
	}
	return scanPostgresCount(r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM note_sync_bindings WHERE workspace_id = $1 AND target_id = $2
	`, workspaceID, targetID))
}

func (r syncRepository) CountClaimsByTarget(ctx context.Context, targetID string) (int, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return 0, err
	}
	return scanPostgresCount(r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM sync_external_claims WHERE workspace_id = $1 AND target_id = $2
	`, workspaceID, targetID))
}

func (r syncRepository) CountStatesByTarget(ctx context.Context, targetID string) (int, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return 0, err
	}
	return scanPostgresCount(r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM note_sync_state WHERE workspace_id = $1 AND target_id = $2
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
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (workspace_id, note_id, target_id) DO UPDATE SET
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
	`, state.NoteID, state.TargetID, state.ExternalPath, nullableString(state.ExternalID), nullableString(state.ExternalURL), state.ContentHash, nullableString(state.ExternalHash), unixPtrToTime(state.ExternalMTime), nullableString(state.LastDirection), unixPtrToTime(state.LastSyncedAt), state.Status, state.ErrorMessage, workspaceID)
	return err
}

func (r syncRepository) GetState(ctx context.Context, noteID, targetID string) (*model.SyncState, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return scanPostgresSyncState(r.db.QueryRowContext(ctx, postgresSyncStateSelectSQL()+`
		WHERE workspace_id = $1 AND note_id = $2 AND target_id = $3
	`, workspaceID, noteID, targetID))
}

func (r syncRepository) ListStatesByTarget(ctx context.Context, targetID string) ([]model.SyncState, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, postgresSyncStateSelectSQL()+`
		WHERE workspace_id = $1 AND target_id = $2
		ORDER BY note_id
	`, workspaceID, targetID)
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
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
		DELETE FROM note_sync_state WHERE workspace_id = $1 AND note_id = $2 AND target_id = $3
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
		WHERE s.workspace_id = $1 AND s.target_id = $2 AND s.status = 'external_deleted'
		ORDER BY s.last_synced_at DESC, n.title ASC
	`, workspaceID, targetID)
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

func (r syncRepository) LockBindingSlot(ctx context.Context, noteID string) error {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1)::bigint)`, "note_sync_binding:"+workspaceID+":"+noteID)
	return err
}

func (r syncRepository) GetBinding(ctx context.Context, noteID string) (*model.NoteSyncBinding, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return scanPostgresNoteSyncBinding(r.db.QueryRowContext(ctx, postgresBindingSelectSQL()+`
		WHERE workspace_id = $1 AND note_id = $2
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
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (workspace_id, note_id) DO UPDATE SET
			target_id = excluded.target_id,
			updated_at = excluded.updated_at,
			workspace_id = excluded.workspace_id
	`, binding.NoteID, binding.TargetID, unixToTime(binding.CreatedAt), unixToTime(binding.UpdatedAt), workspaceID)
	return err
}

func (r syncRepository) DeleteBinding(ctx context.Context, noteID string) error {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
		DELETE FROM note_sync_bindings WHERE workspace_id = $1 AND note_id = $2
	`, workspaceID, noteID)
	return err
}

func (r syncRepository) ListBindingsByTarget(ctx context.Context, targetID string) ([]model.NoteSyncBinding, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, postgresBindingSelectSQL()+`
		WHERE workspace_id = $1 AND target_id = $2
		ORDER BY updated_at DESC, note_id
	`, workspaceID, targetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	bindings := make([]model.NoteSyncBinding, 0)
	for rows.Next() {
		binding, err := scanPostgresNoteSyncBinding(rows)
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
	return scanPostgresExternalClaim(r.db.QueryRowContext(ctx, postgresExternalClaimSelectSQL()+`
		WHERE workspace_id = $1 AND external_key = $2
	`, workspaceID, externalKey))
}

func (r syncRepository) GetExternalClaimByNote(ctx context.Context, noteID string) (*model.SyncExternalClaim, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return scanPostgresExternalClaim(r.db.QueryRowContext(ctx, postgresExternalClaimSelectSQL()+`
		WHERE workspace_id = $1 AND note_id = $2
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
	return r.withTx(ctx, func(tx postgresRunner) error {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM sync_external_claims
			WHERE workspace_id = $1 AND note_id = $2 AND external_key <> $3
		`, workspaceID, claim.NoteID, claim.ExternalKey); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO sync_external_claims (
				external_key, note_id, target_id, external_type, external_id, external_path, created_at, updated_at, workspace_id
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			ON CONFLICT (workspace_id, external_key) DO UPDATE SET
				note_id = excluded.note_id,
				target_id = excluded.target_id,
				external_type = excluded.external_type,
				external_id = excluded.external_id,
				external_path = excluded.external_path,
				updated_at = excluded.updated_at,
				workspace_id = excluded.workspace_id
		`, claim.ExternalKey, claim.NoteID, claim.TargetID, claim.ExternalType, claim.ExternalID, claim.ExternalPath, unixToTime(claim.CreatedAt), unixToTime(claim.UpdatedAt), workspaceID)
		return err
	})
}

func (r syncRepository) ReleaseExternalClaim(ctx context.Context, noteID string) error {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
		DELETE FROM sync_external_claims WHERE workspace_id = $1 AND note_id = $2
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
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (workspace_id, note_id, target_id) DO UPDATE SET
			reason = excluded.reason,
			updated_at = excluded.updated_at,
			workspace_id = excluded.workspace_id
	`, suppression.NoteID, suppression.TargetID, suppression.Reason, unixToTime(suppression.CreatedAt), unixToTime(suppression.UpdatedAt), workspaceID)
	return err
}

func (r syncRepository) DeleteSuppression(ctx context.Context, noteID string, targetID string) error {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
		DELETE FROM note_sync_suppressions WHERE workspace_id = $1 AND note_id = $2 AND target_id = $3
	`, workspaceID, noteID, targetID)
	return err
}

func (r syncRepository) GetSuppression(ctx context.Context, noteID string, targetID string) (*model.NoteSyncSuppression, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return scanPostgresSuppression(r.db.QueryRowContext(ctx, postgresSuppressionSelectSQL()+`
		WHERE workspace_id = $1 AND note_id = $2 AND target_id = $3
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
	return r.withTx(ctx, func(tx postgresRunner) error {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM sync_import_tombstones
			WHERE workspace_id = $1
				AND (external_key = $2
					OR (target_id = $3 AND former_note_id = $4 AND external_type = $5))
		`, workspaceID, tombstone.ExternalKey, tombstone.TargetID, tombstone.FormerNoteID, tombstone.ExternalType); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO sync_import_tombstones (
				external_key, target_id, former_note_id, external_type, external_id, external_path, reason, created_at, updated_at, workspace_id
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		`, tombstone.ExternalKey, tombstone.TargetID, tombstone.FormerNoteID, tombstone.ExternalType, tombstone.ExternalID, tombstone.ExternalPath, tombstone.Reason, unixToTime(tombstone.CreatedAt), unixToTime(tombstone.UpdatedAt), workspaceID)
		return err
	})
}

func (r syncRepository) DeleteImportTombstone(ctx context.Context, externalKey string) error {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
		DELETE FROM sync_import_tombstones WHERE workspace_id = $1 AND external_key = $2
	`, workspaceID, externalKey)
	return err
}

func (r syncRepository) DeleteImportTombstonesForNoteTarget(ctx context.Context, noteID string, targetID string) error {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
		DELETE FROM sync_import_tombstones WHERE workspace_id = $1 AND former_note_id = $2 AND target_id = $3
	`, workspaceID, noteID, targetID)
	return err
}

func (r syncRepository) FindImportTombstone(ctx context.Context, targetID string, externalKey string, formerNoteID string, externalType string) (*model.SyncImportTombstone, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(targetID) != "" && strings.TrimSpace(externalKey) != "" {
		tombstone, err := scanPostgresImportTombstone(r.db.QueryRowContext(ctx, postgresImportTombstoneSelectSQL()+`
			WHERE workspace_id = $1 AND target_id = $2 AND external_key = $3
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
		tombstone, err := scanPostgresImportTombstone(r.db.QueryRowContext(ctx, postgresImportTombstoneSelectSQL()+`
			WHERE workspace_id = $1 AND target_id = $2 AND former_note_id = $3 AND external_type = $4
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
		return scanPostgresImportTombstone(r.db.QueryRowContext(ctx, postgresImportTombstoneSelectSQL()+`
			WHERE workspace_id = $1 AND former_note_id = $2 AND external_type = $3
			ORDER BY updated_at DESC, created_at DESC
			LIMIT 1
		`, workspaceID, formerNoteID, externalType))
	}
	return nil, sql.ErrNoRows
}

func scanPostgresSyncTarget(row rowScanner) (*model.SyncTarget, error) {
	var target model.SyncTarget
	var createdAt time.Time
	var updatedAt time.Time
	if err := row.Scan(&target.ID, &target.Type, &target.Name, &target.VaultPath, &target.BaseFolder, &target.ConfigJSON, &target.Enabled, &target.AutoSync, &target.IsDefault, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	target.CreatedAt = timeToUnix(createdAt)
	target.UpdatedAt = timeToUnix(updatedAt)
	return &target, nil
}

func scanPostgresCount(row rowScanner) (int, error) {
	var count int
	err := row.Scan(&count)
	return count, err
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

func postgresBindingSelectSQL() string {
	return `
		SELECT note_id, target_id, created_at, updated_at
		FROM note_sync_bindings
	`
}

func scanPostgresNoteSyncBinding(row rowScanner) (*model.NoteSyncBinding, error) {
	var binding model.NoteSyncBinding
	var createdAt time.Time
	var updatedAt time.Time
	if err := row.Scan(&binding.NoteID, &binding.TargetID, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	binding.CreatedAt = timeToUnix(createdAt)
	binding.UpdatedAt = timeToUnix(updatedAt)
	return &binding, nil
}

func postgresExternalClaimSelectSQL() string {
	return `
		SELECT external_key, note_id, target_id, external_type, external_id, external_path, created_at, updated_at
		FROM sync_external_claims
	`
}

func scanPostgresExternalClaim(row rowScanner) (*model.SyncExternalClaim, error) {
	var claim model.SyncExternalClaim
	var createdAt time.Time
	var updatedAt time.Time
	if err := row.Scan(
		&claim.ExternalKey,
		&claim.NoteID,
		&claim.TargetID,
		&claim.ExternalType,
		&claim.ExternalID,
		&claim.ExternalPath,
		&createdAt,
		&updatedAt,
	); err != nil {
		return nil, err
	}
	claim.CreatedAt = timeToUnix(createdAt)
	claim.UpdatedAt = timeToUnix(updatedAt)
	return &claim, nil
}

func postgresSuppressionSelectSQL() string {
	return `
		SELECT note_id, target_id, reason, created_at, updated_at
		FROM note_sync_suppressions
	`
}

func scanPostgresSuppression(row rowScanner) (*model.NoteSyncSuppression, error) {
	var suppression model.NoteSyncSuppression
	var createdAt time.Time
	var updatedAt time.Time
	if err := row.Scan(&suppression.NoteID, &suppression.TargetID, &suppression.Reason, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	suppression.CreatedAt = timeToUnix(createdAt)
	suppression.UpdatedAt = timeToUnix(updatedAt)
	return &suppression, nil
}

func postgresImportTombstoneSelectSQL() string {
	return `
		SELECT external_key, target_id, former_note_id, external_type, external_id, external_path, reason, created_at, updated_at
		FROM sync_import_tombstones
	`
}

func scanPostgresImportTombstone(row rowScanner) (*model.SyncImportTombstone, error) {
	var tombstone model.SyncImportTombstone
	var createdAt time.Time
	var updatedAt time.Time
	if err := row.Scan(
		&tombstone.ExternalKey,
		&tombstone.TargetID,
		&tombstone.FormerNoteID,
		&tombstone.ExternalType,
		&tombstone.ExternalID,
		&tombstone.ExternalPath,
		&tombstone.Reason,
		&createdAt,
		&updatedAt,
	); err != nil {
		return nil, err
	}
	tombstone.CreatedAt = timeToUnix(createdAt)
	tombstone.UpdatedAt = timeToUnix(updatedAt)
	return &tombstone, nil
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

func validateSyncTargetType(targetType string) error {
	switch targetType {
	case "obsidian", "notion":
		return nil
	default:
		return fmt.Errorf("unsupported sync target type %q", targetType)
	}
}
