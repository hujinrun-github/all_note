package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

func (r mobileSyncRepository) CreateConflict(ctx context.Context, request model.CreateMobileSyncConflict) (*model.MobileMutationResult, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if request.ConflictID == "" || request.MutationID == "" || request.DeviceClientID == "" || request.RequestSHA256 == "" || request.EntityClientID == "" || request.Operation == "" || request.BaseRevision < 0 {
		return nil, errors.New("invalid mobile sync conflict")
	}
	var result *model.MobileMutationResult
	err = r.withTx(ctx, func(tx *sql.Tx) error {
		if err := lockPostgresConflictMutation(ctx, tx, workspaceID, request.DeviceClientID, request.MutationID); err != nil {
			return err
		}
		replayed, found, err := getPostgresConflictReceipt(ctx, tx, workspaceID, request.DeviceClientID, request.MutationID, request.RequestSHA256)
		if err != nil {
			return err
		}
		if found {
			result = replayed
			return nil
		}
		remote, err := getPostgresConflictTarget(ctx, tx, workspaceID, request.EntityType, request.EntityClientID)
		if err != nil {
			return err
		}
		if remote.Revision <= request.BaseRevision {
			return storage.ErrRevisionConflict
		}
		conflict := model.MobileSyncConflict{
			ConflictID: request.ConflictID, EntityType: request.EntityType, EntityClientID: request.EntityClientID,
			Operation:    request.Operation,
			BaseRevision: request.BaseRevision, RemoteRevision: remote.Revision,
			LocalPayload: request.LocalPayload, RemotePayload: remote.Payload, Revision: 1,
		}
		now := time.Now().UTC()
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO mobile_sync_conflicts (
				workspace_id, conflict_id, mutation_id, device_client_id, request_sha256,
				entity_type, entity_client_id, operation, base_revision, remote_revision,
				local_payload, remote_payload, revision, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11::jsonb, $12::jsonb, 1, $13, $13)
		`, workspaceID, request.ConflictID, request.MutationID, request.DeviceClientID, request.RequestSHA256,
			request.EntityType, request.EntityClientID, request.Operation, request.BaseRevision, remote.Revision,
			request.LocalPayload, remote.Payload, now); err != nil {
			return err
		}
		entity, err := postgresConflictEntity(conflict)
		if err != nil {
			return err
		}
		result = &model.MobileMutationResult{
			MutationID: request.MutationID, Status: model.MobileMutationConflict,
			ErrorCode: "revision_conflict", Entity: entity,
		}
		return persistPostgresConflictResult(ctx, tx, workspaceID, request.DeviceClientID, request.MutationID, request.RequestSHA256, "sync_conflict.created", result, now)
	})
	return result, err
}

func (r mobileSyncRepository) ListUnresolvedConflicts(ctx context.Context) ([]model.MobileSyncConflict, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT conflict_id, entity_type, entity_client_id, operation, base_revision, remote_revision,
			local_payload, remote_payload, revision, resolved_at
		FROM mobile_sync_conflicts
		WHERE workspace_id = $1 AND resolved_at IS NULL
		ORDER BY created_at, conflict_id
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	conflicts := make([]model.MobileSyncConflict, 0)
	for rows.Next() {
		conflict, err := scanPostgresConflict(rows)
		if err != nil {
			return nil, err
		}
		conflicts = append(conflicts, *conflict)
	}
	return conflicts, rows.Err()
}

func (r mobileSyncRepository) ResolveConflict(ctx context.Context, request model.ResolveMobileSyncConflict) (*model.MobileEntityEnvelope, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if request.ConflictID == "" || request.MutationID == "" || request.RequestSHA256 == "" || request.ConflictRevision < 1 || request.TargetRevision < 1 {
		return nil, errors.New("invalid mobile conflict resolution")
	}
	if request.Resolution != "keep_local" && request.Resolution != "keep_remote" && request.Resolution != "merge" {
		return nil, errors.New("unsupported mobile conflict resolution")
	}
	var resolved *model.MobileEntityEnvelope
	var outcomeErr error
	err = r.withTx(ctx, func(tx *sql.Tx) error {
		deviceID := "sync-conflict/" + request.ConflictID
		if err := lockPostgresConflictMutation(ctx, tx, workspaceID, deviceID, request.MutationID); err != nil {
			return err
		}
		replayed, found, err := getPostgresConflictReceipt(ctx, tx, workspaceID, deviceID, request.MutationID, request.RequestSHA256)
		if err != nil {
			return err
		}
		if found {
			resolved = replayed.Entity
			return nil
		}
		conflict, err := getPostgresConflictForUpdate(ctx, tx, workspaceID, request.ConflictID)
		if err != nil {
			return err
		}
		if conflict.ResolvedAt != nil || conflict.Revision != request.ConflictRevision {
			return storage.ErrMobileConflictAdvanced
		}
		current, err := getPostgresConflictTarget(ctx, tx, workspaceID, conflict.EntityType, conflict.EntityClientID)
		if err != nil {
			return err
		}
		if current.Revision != request.TargetRevision {
			if err := advancePostgresConflictTarget(ctx, tx, workspaceID, conflict, request.MutationID, current, time.Now().UTC()); err != nil {
				return err
			}
			outcomeErr = storage.ErrMobileTargetAdvanced
			return nil
		}
		resolved = current
		if request.Resolution != "keep_remote" {
			payload := conflict.LocalPayload
			if request.Resolution == "merge" {
				payload = request.MergedPayload
				if len(payload) == 0 {
					return errors.New("merge resolution requires merged payload")
				}
			}
			resolved, err = applyPostgresConflictPayload(ctx, tx, workspaceID, conflict, request, payload)
			if err != nil {
				if errors.Is(err, storage.ErrRevisionConflict) {
					latest, loadErr := getPostgresConflictTarget(ctx, tx, workspaceID, conflict.EntityType, conflict.EntityClientID)
					if loadErr != nil {
						return loadErr
					}
					if advanceErr := advancePostgresConflictTarget(ctx, tx, workspaceID, conflict, request.MutationID, latest, time.Now().UTC()); advanceErr != nil {
						return advanceErr
					}
					outcomeErr = storage.ErrMobileTargetAdvanced
					return nil
				}
				return err
			}
			if err := persistPostgresConflictEntityChange(ctx, tx, workspaceID, request.MutationID, "conflict.resolved_target", resolved, time.Now().UTC()); err != nil {
				return err
			}
		}
		now := time.Now().UTC()
		result, err := tx.ExecContext(ctx, `
			UPDATE mobile_sync_conflicts
			SET revision = revision + 1, resolution = $1, resolved_at = $2, updated_at = $2
			WHERE workspace_id = $3 AND conflict_id = $4 AND revision = $5 AND resolved_at IS NULL
		`, request.Resolution, now, workspaceID, request.ConflictID, request.ConflictRevision)
		if err != nil {
			return err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return storage.ErrMobileConflictAdvanced
		}
		resolvedConflict, err := getPostgresConflict(ctx, tx, workspaceID, request.ConflictID)
		if err != nil {
			return err
		}
		conflictEntity, err := postgresConflictEntity(*resolvedConflict)
		if err != nil {
			return err
		}
		if err := persistPostgresConflictEntityChange(ctx, tx, workspaceID, request.MutationID, "sync_conflict.resolved", conflictEntity, now); err != nil {
			return err
		}
		receipt := &model.MobileMutationResult{MutationID: request.MutationID, Status: model.MobileMutationApplied, Entity: resolved}
		responseJSON, err := json.Marshal(receipt)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO mobile_mutation_receipts (
				workspace_id, device_client_id, mutation_id, request_sha256, response_json, created_at
			) VALUES ($1, $2, $3, $4, $5::json, $6)
		`, workspaceID, deviceID, request.MutationID, request.RequestSHA256, responseJSON, now)
		return err
	})
	if err == nil && outcomeErr != nil {
		return nil, outcomeErr
	}
	return resolved, err
}

func advancePostgresConflictTarget(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID string,
	conflict *model.MobileSyncConflict,
	mutationID string,
	current *model.MobileEntityEnvelope,
	now time.Time,
) error {
	result, err := tx.ExecContext(ctx, `
		UPDATE mobile_sync_conflicts
		SET remote_revision = $1, remote_payload = $2::jsonb, revision = revision + 1, updated_at = $3
		WHERE workspace_id = $4 AND conflict_id = $5 AND revision = $6 AND resolved_at IS NULL
	`, current.Revision, current.Payload, now, workspaceID, conflict.ConflictID, conflict.Revision)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return storage.ErrMobileConflictAdvanced
	}
	advanced, err := getPostgresConflict(ctx, tx, workspaceID, conflict.ConflictID)
	if err != nil {
		return err
	}
	entity, err := postgresConflictEntity(*advanced)
	if err != nil {
		return err
	}
	return persistPostgresConflictEntityChange(ctx, tx, workspaceID, mutationID, "sync_conflict.target_advanced", entity, now)
}

func applyPostgresConflictPayload(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID string,
	conflict *model.MobileSyncConflict,
	request model.ResolveMobileSyncConflict,
	payload json.RawMessage,
) (*model.MobileEntityEnvelope, error) {
	baseRevision := request.TargetRevision
	if conflict.EntityType == "note" {
		notePayload, err := decodePostgresConflictNotePayload(payload)
		if err != nil && conflict.Operation != model.MobileOperationNoteDelete {
			return nil, err
		}
		if conflict.Operation == model.MobileOperationNoteDelete {
			return deletePostgresMobileNote(ctx, tx, workspaceID, model.MobileNoteMutation{
				MutationID: request.MutationID, DeviceClientID: "sync-conflict/" + request.ConflictID,
				EntityClientID: conflict.EntityClientID, Operation: model.MobileOperationNoteDelete,
				BaseRevision: &baseRevision, RequestSHA256: request.RequestSHA256,
			})
		}
		return updatePostgresMobileNote(ctx, tx, workspaceID, model.MobileNoteMutation{
			MutationID: request.MutationID, DeviceClientID: "sync-conflict/" + request.ConflictID,
			EntityClientID: conflict.EntityClientID, Operation: model.MobileOperationNoteUpdate,
			BaseRevision: &baseRevision, RequestSHA256: request.RequestSHA256, Payload: notePayload,
		})
	}
	entityMutation := model.MobileEntityMutation{
		MutationID: request.MutationID, DeviceClientID: "sync-conflict/" + request.ConflictID,
		EntityType: conflict.EntityType, EntityClientID: conflict.EntityClientID,
		Operation: conflict.Operation, BaseRevision: &baseRevision,
		RequestSHA256: request.RequestSHA256, Payload: payload,
	}
	if strings.HasSuffix(conflict.Operation, ".delete") {
		return deletePostgresMobileEntity(ctx, tx, workspaceID, entityMutation)
	}
	return updatePostgresMobileEntity(ctx, tx, workspaceID, entityMutation)
}

func getPostgresConflictTarget(ctx context.Context, runner postgresRunner, workspaceID, entityType, clientID string) (*model.MobileEntityEnvelope, error) {
	if entityType == "note" {
		return getPostgresMobileNote(ctx, runner, workspaceID, clientID)
	}
	return getPostgresMobileEntity(ctx, runner, workspaceID, entityType, clientID)
}

func decodePostgresConflictNotePayload(payload json.RawMessage) (model.MobileNotePayload, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return model.MobileNotePayload{}, err
	}
	result := model.MobileNotePayload{}
	for field, raw := range fields {
		switch field {
		case "title":
			if err := json.Unmarshal(raw, &result.Title); err != nil {
				return result, err
			}
		case "body":
			if err := json.Unmarshal(raw, &result.Body); err != nil {
				return result, err
			}
		case "folder_id":
			if err := json.Unmarshal(raw, &result.FolderID); err != nil {
				return result, err
			}
		case "tags":
			var tags []string
			if err := json.Unmarshal(raw, &tags); err != nil {
				return result, err
			}
			encoded, _ := json.Marshal(tags)
			value := string(encoded)
			result.Tags = &value
		default:
			return result, fmt.Errorf("unsupported note conflict field %q", field)
		}
	}
	return result, nil
}

func getPostgresConflict(ctx context.Context, runner postgresRunner, workspaceID, conflictID string) (*model.MobileSyncConflict, error) {
	conflict, err := scanPostgresConflict(runner.QueryRowContext(ctx, `
		SELECT conflict_id, entity_type, entity_client_id, operation, base_revision, remote_revision,
			local_payload, remote_payload, revision, resolved_at
		FROM mobile_sync_conflicts WHERE workspace_id = $1 AND conflict_id = $2
	`, workspaceID, conflictID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storage.ErrMobileConflictNotFound
	}
	return conflict, err
}

func getPostgresConflictForUpdate(ctx context.Context, tx *sql.Tx, workspaceID, conflictID string) (*model.MobileSyncConflict, error) {
	conflict, err := scanPostgresConflict(tx.QueryRowContext(ctx, `
		SELECT conflict_id, entity_type, entity_client_id, operation, base_revision, remote_revision,
			local_payload, remote_payload, revision, resolved_at
		FROM mobile_sync_conflicts WHERE workspace_id = $1 AND conflict_id = $2 FOR UPDATE
	`, workspaceID, conflictID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storage.ErrMobileConflictNotFound
	}
	return conflict, err
}

type postgresConflictScanner interface {
	Scan(...any) error
}

func scanPostgresConflict(scanner postgresConflictScanner) (*model.MobileSyncConflict, error) {
	var conflict model.MobileSyncConflict
	var localPayload, remotePayload []byte
	var resolvedAt sql.NullTime
	if err := scanner.Scan(
		&conflict.ConflictID, &conflict.EntityType, &conflict.EntityClientID, &conflict.Operation,
		&conflict.BaseRevision, &conflict.RemoteRevision, &localPayload, &remotePayload,
		&conflict.Revision, &resolvedAt,
	); err != nil {
		return nil, err
	}
	conflict.LocalPayload = json.RawMessage(localPayload)
	conflict.RemotePayload = json.RawMessage(remotePayload)
	if resolvedAt.Valid {
		value := resolvedAt.Time.UTC().Unix()
		conflict.ResolvedAt = &value
	}
	return &conflict, nil
}

func postgresConflictEntity(conflict model.MobileSyncConflict) (*model.MobileEntityEnvelope, error) {
	payload, err := json.Marshal(conflict)
	if err != nil {
		return nil, err
	}
	return &model.MobileEntityEnvelope{
		EntityType: "sync_conflict", ID: conflict.ConflictID, ClientID: conflict.ConflictID,
		Revision: conflict.Revision, Payload: payload,
	}, nil
}

func getPostgresConflictReceipt(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID, deviceID, mutationID, requestHash string,
) (*model.MobileMutationResult, bool, error) {
	var storedHash string
	var responseJSON []byte
	err := tx.QueryRowContext(ctx, `
		SELECT request_sha256, response_json FROM mobile_mutation_receipts
		WHERE workspace_id = $1 AND device_client_id = $2 AND mutation_id = $3
	`, workspaceID, deviceID, mutationID).Scan(&storedHash, &responseJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if storedHash != requestHash {
		return nil, false, storage.ErrMutationIDReused
	}
	var result model.MobileMutationResult
	if err := json.Unmarshal(responseJSON, &result); err != nil {
		return nil, false, err
	}
	return &result, true, nil
}

func persistPostgresConflictResult(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID, deviceID, mutationID, requestHash, operation string,
	result *model.MobileMutationResult,
	now time.Time,
) error {
	if result.Entity == nil {
		return errors.New("conflict result has no entity")
	}
	if err := persistPostgresConflictEntityChange(ctx, tx, workspaceID, mutationID, operation, result.Entity, now); err != nil {
		return err
	}
	responseJSON, err := json.Marshal(result)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO mobile_mutation_receipts (
			workspace_id, device_client_id, mutation_id, request_sha256, response_json, created_at
		) VALUES ($1, $2, $3, $4, $5::json, $6)
	`, workspaceID, deviceID, mutationID, requestHash, responseJSON, now)
	return err
}

func persistPostgresConflictEntityChange(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID, mutationID, operation string,
	entity *model.MobileEntityEnvelope,
	now time.Time,
) error {
	entityJSON, err := json.Marshal(entity)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO mobile_sync_outbox (
			workspace_id, mutation_id, entity_type, entity_client_id, operation, revision, entity_json, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8)
	`, workspaceID, mutationID, entity.EntityType, entity.ClientID, operation, entity.Revision, entityJSON, now)
	return err
}

func lockPostgresConflictMutation(ctx context.Context, tx *sql.Tx, workspaceID, deviceID, mutationID string) error {
	key := fmt.Sprintf("%d:%s%d:%s%d:%s", len(workspaceID), workspaceID, len(deviceID), deviceID, len(mutationID), mutationID)
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1::text))`, key)
	return err
}
