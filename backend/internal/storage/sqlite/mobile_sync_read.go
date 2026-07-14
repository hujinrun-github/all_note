package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
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
			INSERT OR IGNORE INTO mobile_sync_change_heads (workspace_id, latest_position, min_position)
			VALUES (?, 0, 0)
		`, workspaceID); err != nil {
			return err
		}
		var latest int64
		if err := tx.QueryRowContext(ctx, `
			SELECT latest_position FROM mobile_sync_change_heads WHERE workspace_id = ?
		`, workspaceID).Scan(&latest); err != nil {
			return err
		}
		type pendingChange struct {
			sequence  int64
			operation string
			entity    string
		}
		rows, err := tx.QueryContext(ctx, `
			SELECT sequence, operation, entity_json
			FROM mobile_sync_outbox
			WHERE workspace_id = ? AND published_at IS NULL
			ORDER BY sequence
			LIMIT ?
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
		for _, change := range pending {
			latest++
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO mobile_sync_changes (workspace_id, position, operation, entity_json, committed_at)
				VALUES (?, ?, ?, ?, ?)
			`, workspaceID, latest, change.operation, change.entity, now); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE mobile_sync_outbox SET published_at = ?
				WHERE workspace_id = ? AND sequence = ? AND published_at IS NULL
			`, now, workspaceID, change.sequence); err != nil {
				return err
			}
		}
		if len(pending) > 0 {
			if _, err := tx.ExecContext(ctx, `
				UPDATE mobile_sync_change_heads SET latest_position = ? WHERE workspace_id = ?
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
		SELECT latest_position, min_position FROM mobile_sync_change_heads WHERE workspace_id = ?
	`, workspaceID).Scan(&latest, &minimum)
	if errors.Is(err, sql.ErrNoRows) {
		return &model.MobileCommittedChangePage{Changes: []model.MobileCommittedChange{}, NextPosition: after}, nil
	}
	if err != nil {
		return nil, err
	}
	if after < minimum {
		return nil, storage.ErrMobileCursorExpired
	}
	if after > latest {
		return nil, storage.ErrMobileCursorExpired
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT position, operation, entity_json
		FROM mobile_sync_changes
		WHERE workspace_id = ? AND position > ?
		ORDER BY position
		LIMIT ?
	`, workspaceID, after, limit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	changes := make([]model.MobileCommittedChange, 0, limit+1)
	for rows.Next() {
		var change model.MobileCommittedChange
		var entityJSON string
		if err := rows.Scan(&change.Position, &change.Operation, &entityJSON); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(entityJSON), &change.Entity); err != nil {
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
			INSERT OR IGNORE INTO mobile_sync_change_heads (workspace_id, latest_position, min_position)
			VALUES (?, 0, 0)
		`, workspaceID); err != nil {
			return err
		}
		var latest, minimum int64
		if err := tx.QueryRowContext(ctx, `
			SELECT latest_position, min_position FROM mobile_sync_change_heads WHERE workspace_id = ?
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
			DELETE FROM mobile_sync_changes WHERE workspace_id = ? AND position <= ?
		`, workspaceID, through); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `
			UPDATE mobile_sync_change_heads SET min_position = ? WHERE workspace_id = ?
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
	snapshot := &model.MobileSnapshot{
		SessionID: request.SessionID, Scope: request.Scope, ExpiresAt: request.ExpiresAt,
	}
	err = r.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO mobile_sync_change_heads (workspace_id, latest_position, min_position)
			VALUES (?, 0, 0)
		`, workspaceID); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `
			SELECT latest_position FROM mobile_sync_change_heads WHERE workspace_id = ?
		`, workspaceID).Scan(&snapshot.BoundaryPosition); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO mobile_sync_snapshot_sessions (
				session_id, workspace_id, scope, boundary_position, expires_at, created_at
			) VALUES (?, ?, ?, ?, ?, ?)
		`, request.SessionID, workspaceID, request.Scope, snapshot.BoundaryPosition, request.ExpiresAt, request.Now); err != nil {
			return err
		}
		entities, err := collectSQLiteSnapshotEntities(ctx, tx, workspaceID)
		if err != nil {
			return err
		}
		for index, entity := range entities {
			encoded, err := json.Marshal(entity)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO mobile_sync_snapshot_items (session_id, ordinal, entity_json)
				VALUES (?, ?, ?)
			`, request.SessionID, index, string(encoded)); err != nil {
				return err
			}
		}
		snapshot.TotalEntities = int64(len(entities))
		return nil
	})
	return snapshot, err
}

func collectSQLiteSnapshotEntities(ctx context.Context, tx *sql.Tx, workspaceID string) ([]model.MobileEntityEnvelope, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT client_id FROM notes
		WHERE workspace_id = ? AND client_id IS NOT NULL
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
	for _, clientID := range clientIDs {
		entity, err := getSQLiteMobileNote(ctx, tx, workspaceID, clientID)
		if err != nil {
			return nil, err
		}
		entities = append(entities, *entity)
	}
	jobRows, err := tx.QueryContext(ctx, sqliteTranscriptionJobSelect+`
		WHERE workspace_id = ? ORDER BY job_id
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

func (r mobileSyncRepository) ReadSnapshot(ctx context.Context, request model.ReadMobileSnapshot) (*model.MobileSnapshotPage, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if request.SessionID == "" || request.Offset < 0 {
		return nil, errors.New("invalid mobile snapshot page request")
	}
	request.Limit = normalizeMobileReadLimit(request.Limit)
	var boundary, expiresAt int64
	err = r.db.QueryRowContext(ctx, `
		SELECT boundary_position, expires_at
		FROM mobile_sync_snapshot_sessions
		WHERE workspace_id = ? AND session_id = ?
	`, workspaceID, request.SessionID).Scan(&boundary, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && request.Now > expiresAt) {
		return nil, storage.ErrMobileSnapshotExpired
	}
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT entity_json FROM mobile_sync_snapshot_items
		WHERE session_id = ? AND ordinal >= ?
		ORDER BY ordinal
		LIMIT ?
	`, request.SessionID, request.Offset, request.Limit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entities := make([]model.MobileEntityEnvelope, 0, request.Limit+1)
	for rows.Next() {
		var encoded string
		if err := rows.Scan(&encoded); err != nil {
			return nil, err
		}
		var entity model.MobileEntityEnvelope
		if err := json.Unmarshal([]byte(encoded), &entity); err != nil {
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
		BoundaryPosition: boundary, ExpiresAt: expiresAt,
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
