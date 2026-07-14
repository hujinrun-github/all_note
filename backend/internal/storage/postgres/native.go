package postgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

type watchDeviceRepository struct {
	db postgresRunner
}

func (r watchDeviceRepository) Create(ctx context.Context, device *model.WatchDevice) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO watch_devices (
			id, workspace_id, user_id, name, token_hash, status,
			expires_at, last_seen_at, created_at, updated_at, revoked_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`, device.ID, device.WorkspaceID, device.UserID, device.Name, device.TokenHash, device.Status,
		device.ExpiresAt, device.LastSeenAt, device.CreatedAt, device.UpdatedAt, device.RevokedAt)
	if isPostgresUniqueViolation(err) {
		return storage.ErrAlreadyExists
	}
	return err
}

func (r watchDeviceRepository) GetActiveByTokenHash(ctx context.Context, tokenHash string) (*model.WatchDevice, error) {
	var device model.WatchDevice
	err := r.db.QueryRowContext(ctx, `
		SELECT id, workspace_id, user_id, name, token_hash, status,
			expires_at, last_seen_at, created_at, updated_at, revoked_at
		FROM watch_devices
		WHERE token_hash = $1 AND status = 'active' AND revoked_at IS NULL AND expires_at > $2
	`, tokenHash, time.Now().Unix()).Scan(
		&device.ID, &device.WorkspaceID, &device.UserID, &device.Name, &device.TokenHash, &device.Status,
		&device.ExpiresAt, &device.LastSeenAt, &device.CreatedAt, &device.UpdatedAt, &device.RevokedAt,
	)
	if err != nil {
		return nil, err
	}
	return &device, nil
}

func (r watchDeviceRepository) Revoke(ctx context.Context, deviceID, userID string) error {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	result, err := r.db.ExecContext(ctx, `
		UPDATE watch_devices
		SET status = 'revoked', revoked_at = $1, updated_at = $1
		WHERE id = $2 AND workspace_id = $3 AND user_id = $4 AND revoked_at IS NULL
	`, now, deviceID, workspaceID, userID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (r watchDeviceRepository) TouchLastSeen(ctx context.Context, deviceID string, seenAt int64) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE watch_devices SET last_seen_at = $1, updated_at = $1
		WHERE id = $2 AND status = 'active' AND revoked_at IS NULL AND last_seen_at <= $3
	`, seenAt, deviceID, seenAt-300)
	return err
}

type voiceNoteRepository struct {
	db postgresRunner
}

func (r voiceNoteRepository) Create(ctx context.Context, voice *model.VoiceNote) error {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO voice_notes (
			id, workspace_id, client_id, note_id, duration_ms, recorded_at, language,
			object_key, mime_type, audio_size, audio_sha256, upload_state,
			transcription_state, transcription_error, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
	`, voice.ID, workspaceID, voice.ClientID, voice.NoteID, voice.DurationMS, voice.RecordedAt, voice.Language,
		voice.ObjectKey, voice.MimeType, voice.AudioSize, voice.AudioSHA256, voice.UploadState,
		voice.TranscriptionState, voice.TranscriptionError, voice.CreatedAt, voice.UpdatedAt)
	if isPostgresUniqueViolation(err) {
		return storage.ErrAlreadyExists
	}
	return err
}

func (r voiceNoteRepository) GetByClientID(ctx context.Context, clientID string) (*model.VoiceNote, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return scanPostgresVoiceNote(r.db.QueryRowContext(ctx, postgresVoiceNoteSelect+`
		WHERE v.workspace_id = $1 AND v.client_id = $2
	`, workspaceID, clientID))
}

func (r voiceNoteRepository) ClaimUpload(ctx context.Context, clientID string, claim model.VoiceUploadClaim) (*model.VoiceNote, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	result, err := r.db.ExecContext(ctx, `
		UPDATE voice_notes
		SET object_key = CASE WHEN upload_state = 'uploaded' THEN object_key ELSE $1 END,
			mime_type = CASE WHEN upload_state = 'uploaded' THEN mime_type ELSE $2 END,
			audio_size = CASE WHEN upload_state = 'uploaded' THEN audio_size ELSE $3 END,
			audio_sha256 = CASE WHEN upload_state = 'uploaded' THEN audio_sha256 ELSE $4 END,
			upload_state = CASE WHEN upload_state = 'uploaded' THEN upload_state ELSE 'uploading' END,
			updated_at = $5
		WHERE workspace_id = $6 AND client_id = $7
			AND (audio_sha256 = '' OR audio_sha256 = $4)
	`, claim.ObjectKey, claim.MimeType, claim.Size, claim.SHA256, now, workspaceID, clientID)
	if err != nil {
		return nil, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if rows == 0 {
		existing, getErr := r.GetByClientID(ctx, clientID)
		if getErr != nil {
			return nil, getErr
		}
		if existing.AudioSHA256 != "" && existing.AudioSHA256 != claim.SHA256 {
			return nil, storage.ErrUploadConflict
		}
		return existing, nil
	}
	return r.GetByClientID(ctx, clientID)
}

func (r voiceNoteRepository) MarkUploaded(ctx context.Context, clientID, sha256 string) (*model.VoiceNote, error) {
	if db, ok := r.db.(*sql.DB); ok {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		committed := false
		defer func() {
			if !committed {
				_ = tx.Rollback()
			}
		}()
		voice, err := (voiceNoteRepository{db: tx}).markUploaded(ctx, clientID, sha256)
		if err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		committed = true
		return voice, nil
	}
	return r.markUploaded(ctx, clientID, sha256)
}

func (r voiceNoteRepository) markUploaded(ctx context.Context, clientID, sha256 string) (*model.VoiceNote, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	result, err := r.db.ExecContext(ctx, `
		UPDATE voice_notes SET upload_state = 'uploaded', updated_at = $1
		WHERE workspace_id = $2 AND client_id = $3 AND audio_sha256 = $4
	`, now, workspaceID, clientID, sha256)
	if err != nil {
		return nil, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if rows == 0 {
		return nil, storage.ErrUploadConflict
	}
	if _, err := r.db.ExecContext(ctx, `
		UPDATE transcription_jobs
		SET state = 'queued', revision = revision + 1, updated_at = $1
		WHERE workspace_id = $2 AND voice_note_id = $3 AND state = 'waiting_for_audio'
	`, now, workspaceID, clientID); err != nil {
		return nil, err
	}
	return r.GetByClientID(ctx, clientID)
}

func (r voiceNoteRepository) MarkUploadFailed(ctx context.Context, clientID, sha256 string) error {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
		UPDATE voice_notes SET upload_state = 'failed', updated_at = $1
		WHERE workspace_id = $2 AND client_id = $3 AND audio_sha256 = $4 AND upload_state <> 'uploaded'
	`, time.Now().Unix(), workspaceID, clientID, sha256)
	return err
}

func (r voiceNoteRepository) SetTranscriptionState(ctx context.Context, clientID, state, message string) (*model.VoiceNote, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE voice_notes
		SET transcription_state = $1, transcription_error = $2, updated_at = $3
		WHERE workspace_id = $4 AND client_id = $5
	`, state, message, time.Now().Unix(), workspaceID, clientID)
	if err != nil {
		return nil, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if rows == 0 {
		return nil, sql.ErrNoRows
	}
	return r.GetByClientID(ctx, clientID)
}

func (r voiceNoteRepository) ListRecent(ctx context.Context, limit int) ([]model.VoiceNote, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	rows, err := r.db.QueryContext(ctx, postgresVoiceNoteSelect+`
		WHERE v.workspace_id = $1 ORDER BY v.updated_at DESC LIMIT $2
	`, workspaceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]model.VoiceNote, 0)
	for rows.Next() {
		voice, err := scanPostgresVoiceNote(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *voice)
	}
	return result, rows.Err()
}

const postgresVoiceNoteSelect = `
	SELECT v.id, v.workspace_id, v.client_id, v.note_id, n.title, n.body,
		v.duration_ms, v.recorded_at, v.language, v.object_key, v.mime_type,
		v.audio_size, v.audio_sha256, v.upload_state, v.transcription_state,
		v.transcription_error, v.created_at, v.updated_at
	FROM voice_notes v
	JOIN notes n ON n.workspace_id = v.workspace_id AND n.id = v.note_id
`

type postgresScanner interface {
	Scan(...interface{}) error
}

func scanPostgresVoiceNote(scanner postgresScanner) (*model.VoiceNote, error) {
	var voice model.VoiceNote
	err := scanner.Scan(
		&voice.ID, &voice.WorkspaceID, &voice.ClientID, &voice.NoteID, &voice.Title, &voice.Body,
		&voice.DurationMS, &voice.RecordedAt, &voice.Language, &voice.ObjectKey, &voice.MimeType,
		&voice.AudioSize, &voice.AudioSHA256, &voice.UploadState, &voice.TranscriptionState,
		&voice.TranscriptionError, &voice.CreatedAt, &voice.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &voice, nil
}

func isPostgresUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "duplicate key value violates unique constraint") ||
		strings.Contains(message, "sqlstate 23505") ||
		errors.Is(err, storage.ErrAlreadyExists)
}
