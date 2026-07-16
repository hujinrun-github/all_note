package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/watchprojection"
)

const maxMobileReadPageSize = 1000

func (r mobileSyncRepository) PublishPendingChanges(ctx context.Context, limit int, now int64) (int, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return 0, err
	}
	limit = normalizeMobileReadLimit(limit)
	published := 0
	err = r.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO mobile_sync_change_heads (workspace_id, latest_position, min_position)
			VALUES ($1, 0, 0)
			ON CONFLICT (workspace_id) DO NOTHING
		`, workspaceID); err != nil {
			return err
		}
		var latest int64
		if err := tx.QueryRowContext(ctx, `
			SELECT latest_position FROM mobile_sync_change_heads WHERE workspace_id = $1 FOR UPDATE
		`, workspaceID).Scan(&latest); err != nil {
			return err
		}
		type pendingChange struct {
			sequence  int64
			operation string
			entity    []byte
		}
		rows, err := tx.QueryContext(ctx, `
			SELECT sequence, operation, entity_json
			FROM mobile_sync_outbox
			WHERE workspace_id = $1 AND published_at IS NULL
			ORDER BY sequence
			FOR UPDATE
			LIMIT $2
		`, workspaceID, limit)
		if err != nil {
			return err
		}
		pending := make([]pendingChange, 0, limit)
		for rows.Next() {
			var change pendingChange
			if err := rows.Scan(&change.sequence, &change.operation, &change.entity); err != nil {
				_ = rows.Close()
				return err
			}
			pending = append(pending, change)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if err := rows.Err(); err != nil {
			return err
		}
		committedAt := time.Unix(now, 0).UTC()
		for _, change := range pending {
			latest++
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO mobile_sync_changes (workspace_id, position, operation, entity_json, committed_at)
				VALUES ($1, $2, $3, $4::jsonb, $5)
			`, workspaceID, latest, change.operation, change.entity, committedAt); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE mobile_sync_outbox SET published_at = $1
				WHERE workspace_id = $2 AND sequence = $3 AND published_at IS NULL
			`, committedAt, workspaceID, change.sequence); err != nil {
				return err
			}
		}
		if len(pending) > 0 {
			if _, err := tx.ExecContext(ctx, `
				UPDATE mobile_sync_change_heads SET latest_position = $1 WHERE workspace_id = $2
			`, latest, workspaceID); err != nil {
				return err
			}
		}
		published = len(pending)
		return nil
	})
	return published, err
}

func (r mobileSyncRepository) ReadCommittedChanges(ctx context.Context, after int64, limit int) (*model.MobileCommittedChangePage, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if after < 0 {
		return nil, errors.New("mobile cursor position must not be negative")
	}
	limit = normalizeMobileReadLimit(limit)
	var latest, minimum int64
	err = r.db.QueryRowContext(ctx, `
		SELECT latest_position, min_position FROM mobile_sync_change_heads WHERE workspace_id = $1
	`, workspaceID).Scan(&latest, &minimum)
	if errors.Is(err, sql.ErrNoRows) {
		return &model.MobileCommittedChangePage{Changes: []model.MobileCommittedChange{}, NextPosition: after}, nil
	}
	if err != nil {
		return nil, err
	}
	if after < minimum || after > latest {
		return nil, storage.ErrMobileCursorExpired
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT position, operation, entity_json
		FROM mobile_sync_changes
		WHERE workspace_id = $1 AND position > $2
		ORDER BY position
		LIMIT $3
	`, workspaceID, after, limit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	changes := make([]model.MobileCommittedChange, 0, limit+1)
	for rows.Next() {
		var change model.MobileCommittedChange
		var entityJSON []byte
		if err := rows.Scan(&change.Position, &change.Operation, &entityJSON); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(entityJSON, &change.Entity); err != nil {
			return nil, fmt.Errorf("decode committed mobile change: %w", err)
		}
		changes = append(changes, change)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	hasMore := len(changes) > limit
	if hasMore {
		changes = changes[:limit]
	}
	next := after
	if len(changes) > 0 {
		next = changes[len(changes)-1].Position
	}
	return &model.MobileCommittedChangePage{Changes: changes, NextPosition: next, HasMore: hasMore}, nil
}

func (r mobileSyncRepository) PruneCommittedChanges(ctx context.Context, through int64) error {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	if through < 0 {
		return errors.New("mobile prune position must not be negative")
	}
	return r.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO mobile_sync_change_heads (workspace_id, latest_position, min_position)
			VALUES ($1, 0, 0)
			ON CONFLICT (workspace_id) DO NOTHING
		`, workspaceID); err != nil {
			return err
		}
		var latest, minimum int64
		if err := tx.QueryRowContext(ctx, `
			SELECT latest_position, min_position FROM mobile_sync_change_heads WHERE workspace_id = $1 FOR UPDATE
		`, workspaceID).Scan(&latest, &minimum); err != nil {
			return err
		}
		if through > latest {
			through = latest
		}
		if through <= minimum {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM mobile_sync_changes WHERE workspace_id = $1 AND position <= $2
		`, workspaceID, through); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `
			UPDATE mobile_sync_change_heads SET min_position = $1 WHERE workspace_id = $2
		`, through, workspaceID)
		return err
	})
}

func (r mobileSyncRepository) BeginSnapshot(ctx context.Context, request model.BeginMobileSnapshot) (*model.MobileSnapshot, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if request.SessionID == "" || (request.Scope != "iphone" && request.Scope != "watch") || request.ExpiresAt <= request.Now {
		return nil, errors.New("invalid mobile snapshot request")
	}
	snapshot := &model.MobileSnapshot{SessionID: request.SessionID, Scope: request.Scope, ExpiresAt: request.ExpiresAt, TimeZone: request.TimeZone}
	if snapshot.TimeZone == "" {
		snapshot.TimeZone = "UTC"
	}
	snapshot.ScopeValidUntil = snapshot.ExpiresAt
	err = r.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO mobile_sync_change_heads (workspace_id, latest_position, min_position)
			VALUES ($1, 0, 0)
			ON CONFLICT (workspace_id) DO NOTHING
		`, workspaceID); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `
			SELECT latest_position FROM mobile_sync_change_heads WHERE workspace_id = $1 FOR UPDATE
		`, workspaceID).Scan(&snapshot.BoundaryPosition); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO mobile_sync_snapshot_sessions (
				session_id, workspace_id, scope, boundary_position, projection_time_zone, scope_valid_until, expires_at, created_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		`, request.SessionID, workspaceID, request.Scope, snapshot.BoundaryPosition,
			snapshot.TimeZone, time.Unix(snapshot.ScopeValidUntil, 0).UTC(),
			time.Unix(request.ExpiresAt, 0).UTC(), time.Unix(request.Now, 0).UTC()); err != nil {
			return err
		}
		entities, err := collectPostgresSnapshotEntities(ctx, tx, workspaceID, request.Scope)
		if err != nil {
			return err
		}
		if request.Scope == "watch" {
			entities, snapshot.ScopeValidUntil, err = watchprojection.ProjectSnapshot(entities, request.Now, snapshot.TimeZone)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE mobile_sync_snapshot_sessions SET scope_valid_until = $1 WHERE session_id = $2`, time.Unix(snapshot.ScopeValidUntil, 0).UTC(), request.SessionID); err != nil {
				return err
			}
		}
		for index, entity := range entities {
			encoded, err := json.Marshal(entity)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO mobile_sync_snapshot_items (session_id, ordinal, entity_json)
				VALUES ($1, $2, $3::jsonb)
			`, request.SessionID, index, encoded); err != nil {
				return err
			}
		}
		snapshot.TotalEntities = int64(len(entities))
		return nil
	})
	return snapshot, err
}

func collectPostgresSnapshotEntities(ctx context.Context, tx *sql.Tx, workspaceID, scope string) ([]model.MobileEntityEnvelope, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT client_id FROM notes
		WHERE workspace_id = $1 AND client_id IS NOT NULL
		ORDER BY client_id
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	clientIDs := make([]string, 0)
	for rows.Next() {
		var clientID string
		if err := rows.Scan(&clientID); err != nil {
			_ = rows.Close()
			return nil, err
		}
		clientIDs = append(clientIDs, clientID)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	entities := make([]model.MobileEntityEnvelope, 0, len(clientIDs))
	if scope == "iphone" {
		for _, clientID := range clientIDs {
			entity, err := getPostgresMobileNote(ctx, tx, workspaceID, clientID)
			if err != nil {
				return nil, err
			}
			entities = append(entities, *entity)
		}
	}
	entityTypes := []string{"task", "event"}
	if scope == "iphone" {
		entityTypes = append(entityTypes, "inbox")
	}
	for _, entityType := range entityTypes {
		entities, err = appendPostgresSnapshotEntityType(ctx, tx, workspaceID, entityType, entities)
		if err != nil {
			return nil, err
		}
	}
	occurrenceRows, err := tx.QueryContext(ctx, `
		SELECT occurrence_id FROM task_occurrences
		WHERE workspace_id = $1 AND occurrence_id IS NOT NULL ORDER BY occurrence_id
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	occurrenceIDs := make([]string, 0)
	for occurrenceRows.Next() {
		var occurrenceID string
		if err := occurrenceRows.Scan(&occurrenceID); err != nil {
			_ = occurrenceRows.Close()
			return nil, err
		}
		occurrenceIDs = append(occurrenceIDs, occurrenceID)
	}
	if err := occurrenceRows.Close(); err != nil {
		return nil, err
	}
	if err := occurrenceRows.Err(); err != nil {
		return nil, err
	}
	for _, occurrenceID := range occurrenceIDs {
		entity, err := getPostgresMobileOccurrence(ctx, tx, workspaceID, occurrenceID)
		if err != nil {
			return nil, err
		}
		entities = append(entities, *entity)
	}
	if scope == "iphone" {
		conflictRows, err := tx.QueryContext(ctx, `
			SELECT conflict_id FROM mobile_sync_conflicts
			WHERE workspace_id = $1 AND resolved_at IS NULL ORDER BY conflict_id
		`, workspaceID)
		if err != nil {
			return nil, err
		}
		conflictIDs := make([]string, 0)
		for conflictRows.Next() {
			var conflictID string
			if err := conflictRows.Scan(&conflictID); err != nil {
				_ = conflictRows.Close()
				return nil, err
			}
			conflictIDs = append(conflictIDs, conflictID)
		}
		if err := conflictRows.Close(); err != nil {
			return nil, err
		}
		if err := conflictRows.Err(); err != nil {
			return nil, err
		}
		for _, conflictID := range conflictIDs {
			conflict, err := getPostgresConflict(ctx, tx, workspaceID, conflictID)
			if err != nil {
				return nil, err
			}
			entity, err := postgresConflictEntity(*conflict)
			if err != nil {
				return nil, err
			}
			entities = append(entities, *entity)
		}
	}
	voiceLimit := maxMobileReadPageSize
	if scope == "watch" {
		voiceLimit = 20
	}
	voiceRows, err := tx.QueryContext(ctx, `
		SELECT client_id FROM voice_notes
		WHERE workspace_id = $1 ORDER BY updated_at DESC, client_id LIMIT $2
	`, workspaceID, voiceLimit)
	if err != nil {
		return nil, err
	}
	voiceClientIDs := make([]string, 0)
	for voiceRows.Next() {
		var clientID string
		if err := voiceRows.Scan(&clientID); err != nil {
			_ = voiceRows.Close()
			return nil, err
		}
		voiceClientIDs = append(voiceClientIDs, clientID)
	}
	if err := voiceRows.Close(); err != nil {
		return nil, err
	}
	if err := voiceRows.Err(); err != nil {
		return nil, err
	}
	for _, clientID := range voiceClientIDs {
		entity, err := getPostgresMobileVoiceNote(ctx, tx, workspaceID, clientID)
		if err != nil {
			return nil, err
		}
		entities = append(entities, *entity)
	}
	jobRows, err := tx.QueryContext(ctx, postgresTranscriptionJobSelect+`
		WHERE workspace_id = $1 ORDER BY job_id
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	for jobRows.Next() {
		var job model.TranscriptionJob
		var nextAttempt sql.NullInt64
		if err := jobRows.Scan(
			&job.JobID, &job.VoiceNoteID, &job.Generation, &job.State, &job.Revision, &job.ErrorCode,
			&nextAttempt, &job.Language, &job.Attempt, &job.MaxAttempts, &job.CreatedAt, &job.UpdatedAt,
		); err != nil {
			_ = jobRows.Close()
			return nil, err
		}
		payload, err := json.Marshal(map[string]any{
			"voice_note_id": job.VoiceNoteID, "generation": job.Generation, "state": job.State, "error_code": job.ErrorCode,
		})
		if err != nil {
			_ = jobRows.Close()
			return nil, err
		}
		entities = append(entities, model.MobileEntityEnvelope{
			EntityType: "transcription_job", ID: job.JobID, ClientID: job.JobID, Revision: job.Revision, Payload: payload,
		})
	}
	if err := jobRows.Close(); err != nil {
		return nil, err
	}
	return entities, jobRows.Err()
}

func appendPostgresSnapshotEntityType(ctx context.Context, tx *sql.Tx, workspaceID, entityType string, entities []model.MobileEntityEnvelope) ([]model.MobileEntityEnvelope, error) {
	table, err := postgresMobileEntityTable(entityType)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT client_id FROM `+table+`
		WHERE workspace_id = $1 AND client_id IS NOT NULL ORDER BY client_id`, workspaceID)
	if err != nil {
		return nil, err
	}
	clientIDs := make([]string, 0)
	for rows.Next() {
		var clientID string
		if err := rows.Scan(&clientID); err != nil {
			_ = rows.Close()
			return nil, err
		}
		clientIDs = append(clientIDs, clientID)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, clientID := range clientIDs {
		entity, err := getPostgresMobileEntity(ctx, tx, workspaceID, entityType, clientID)
		if err != nil {
			return nil, err
		}
		entities = append(entities, *entity)
	}
	return entities, nil
}

func (r mobileSyncRepository) ReadSnapshot(ctx context.Context, request model.ReadMobileSnapshot) (*model.MobileSnapshotPage, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if request.SessionID == "" || request.Offset < 0 {
		return nil, errors.New("invalid mobile snapshot page request")
	}
	request.Limit = normalizeMobileReadLimit(request.Limit)
	var boundary int64
	var expiresAt, scopeValidUntil time.Time
	var projectionTimeZone string
	err = r.db.QueryRowContext(ctx, `
		SELECT boundary_position, expires_at, scope_valid_until, projection_time_zone
		FROM mobile_sync_snapshot_sessions
		WHERE workspace_id = $1 AND session_id = $2
	`, workspaceID, request.SessionID).Scan(&boundary, &expiresAt, &scopeValidUntil, &projectionTimeZone)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && request.Now > expiresAt.UTC().Unix()) {
		return nil, storage.ErrMobileSnapshotExpired
	}
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT entity_json FROM mobile_sync_snapshot_items
		WHERE session_id = $1 AND ordinal >= $2
		ORDER BY ordinal
		LIMIT $3
	`, request.SessionID, request.Offset, request.Limit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entities := make([]model.MobileEntityEnvelope, 0, request.Limit+1)
	for rows.Next() {
		var encoded []byte
		if err := rows.Scan(&encoded); err != nil {
			return nil, err
		}
		var entity model.MobileEntityEnvelope
		if err := json.Unmarshal(encoded, &entity); err != nil {
			return nil, err
		}
		entities = append(entities, entity)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	hasMore := len(entities) > request.Limit
	if hasMore {
		entities = entities[:request.Limit]
	}
	return &model.MobileSnapshotPage{
		Entities: entities, NextOffset: request.Offset + int64(len(entities)), HasMore: hasMore,
		BoundaryPosition: boundary, ExpiresAt: expiresAt.UTC().Unix(), ScopeValidUntil: scopeValidUntil.UTC().Unix(), TimeZone: projectionTimeZone,
	}, nil
}

func normalizeMobileReadLimit(limit int) int {
	if limit <= 0 {
		return 100
	}
	if limit > maxMobileReadPageSize {
		return maxMobileReadPageSize
	}
	return limit
}
