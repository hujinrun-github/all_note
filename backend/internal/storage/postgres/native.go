package postgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
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
	return (mobileSyncRepository{db: r.db}).withTx(ctx, func(tx *sql.Tx) error {
		audioState := voice.AudioState
		if audioState == "" {
			audioState = model.VoiceAudioAbsent
		}
		audioRevision := voice.AudioRevision
		if audioRevision < 1 {
			audioRevision = 1
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO voice_notes (
				id, workspace_id, client_id, revision, audio_revision, audio_state, note_id, duration_ms, recorded_at, language,
				object_key, mime_type, audio_size, audio_sha256, upload_state,
				transcription_state, transcription_error, created_at, updated_at
			) VALUES ($1, $2, $3, 1, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
		`, voice.ID, workspaceID, voice.ClientID, audioRevision, audioState, voice.NoteID, voice.DurationMS, voice.RecordedAt, voice.Language,
			voice.ObjectKey, voice.MimeType, voice.AudioSize, voice.AudioSHA256, voice.UploadState,
			voice.TranscriptionState, voice.TranscriptionError, voice.CreatedAt, voice.UpdatedAt)
		if isPostgresUniqueViolation(err) {
			return storage.ErrAlreadyExists
		}
		if err != nil {
			return err
		}
		return persistPostgresServerEntityChange(ctx, tx, workspaceID, uuid.NewString(), "voice_note", "voice.server_created", voice.ClientID, time.Unix(voice.UpdatedAt, 0).UTC())
	})
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
	var voice *model.VoiceNote
	err = (mobileSyncRepository{db: r.db}).withTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			UPDATE voice_notes
			SET object_key = $1, mime_type = $2, audio_size = $3, audio_sha256 = $4, upload_state = 'uploading',
				audio_state = 'uploading', audio_revision = audio_revision + 1, updated_at = $5, revision = revision + 1
			WHERE workspace_id = $6 AND client_id = $7 AND deleted_at IS NULL AND upload_state <> 'uploaded'
				AND audio_state NOT IN ('delete_requested', 'deleted')
				AND (audio_sha256 = '' OR audio_sha256 = $4)
		`, claim.ObjectKey, claim.MimeType, claim.Size, claim.SHA256, now, workspaceID, clientID)
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if rows == 0 {
			existing, getErr := (voiceNoteRepository{db: tx}).GetByClientID(ctx, clientID)
			if getErr != nil {
				return getErr
			}
			if existing.DeletedAt != nil || existing.AudioState == model.VoiceAudioDeleteRequested || existing.AudioState == model.VoiceAudioDeleted {
				return storage.ErrVoiceAudioGone
			}
			if existing.AudioSHA256 != "" && existing.AudioSHA256 != claim.SHA256 {
				return storage.ErrUploadConflict
			}
			voice = existing
			return nil
		}
		if err := persistPostgresServerEntityChange(ctx, tx, workspaceID, uuid.NewString(), "voice_note", "voice.server_updated", clientID, time.Unix(now, 0).UTC()); err != nil {
			return err
		}
		voice, err = (voiceNoteRepository{db: tx}).GetByClientID(ctx, clientID)
		return err
	})
	return voice, err
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
		UPDATE voice_notes SET upload_state = 'uploaded', audio_state = 'uploaded', audio_revision = audio_revision + 1,
			updated_at = $1, revision = revision + 1
		WHERE workspace_id = $2 AND client_id = $3 AND audio_sha256 = $4 AND deleted_at IS NULL AND upload_state <> 'uploaded'
			AND audio_state NOT IN ('delete_requested', 'deleted')
	`, now, workspaceID, clientID, sha256)
	if err != nil {
		return nil, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if rows == 0 {
		existing, getErr := r.GetByClientID(ctx, clientID)
		if getErr == nil && (existing.DeletedAt != nil || existing.AudioState == model.VoiceAudioDeleteRequested || existing.AudioState == model.VoiceAudioDeleted) {
			return nil, storage.ErrVoiceAudioGone
		}
		if getErr == nil && existing.AudioSHA256 == sha256 && existing.UploadState == model.VoiceUploadUploaded {
			return existing, nil
		}
		return nil, storage.ErrUploadConflict
	}
	if _, err := r.db.ExecContext(ctx, `
		UPDATE transcription_jobs
		SET state = 'queued', revision = revision + 1, updated_at = $1
		WHERE workspace_id = $2 AND voice_note_id = $3 AND state = 'waiting_for_audio'
	`, now, workspaceID, clientID); err != nil {
		return nil, err
	}
	tx, ok := r.db.(*sql.Tx)
	if !ok {
		return nil, errors.New("voice upload completion requires a transaction")
	}
	if err := persistPostgresServerEntityChange(ctx, tx, workspaceID, uuid.NewString(), "voice_note", "voice.server_updated", clientID, time.Unix(now, 0).UTC()); err != nil {
		return nil, err
	}
	return r.GetByClientID(ctx, clientID)
}

func (r voiceNoteRepository) MarkUploadFailed(ctx context.Context, clientID, sha256 string) error {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	return (mobileSyncRepository{db: r.db}).withTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			UPDATE voice_notes SET upload_state = 'failed', updated_at = $1, revision = revision + 1
			WHERE workspace_id = $2 AND client_id = $3 AND audio_sha256 = $4 AND upload_state NOT IN ('uploaded', 'failed') AND deleted_at IS NULL
		`, now, workspaceID, clientID, sha256)
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if rows == 0 {
			existing, getErr := (voiceNoteRepository{db: tx}).GetByClientID(ctx, clientID)
			if getErr == nil && (existing.DeletedAt != nil || existing.AudioState == model.VoiceAudioDeleteRequested || existing.AudioState == model.VoiceAudioDeleted) {
				return storage.ErrVoiceAudioGone
			}
			return getErr
		}
		return persistPostgresServerEntityChange(ctx, tx, workspaceID, uuid.NewString(), "voice_note", "voice.server_updated", clientID, time.Unix(now, 0).UTC())
	})
}

func (r voiceNoteRepository) SetTranscriptionState(ctx context.Context, clientID, state, message string) (*model.VoiceNote, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	var voice *model.VoiceNote
	err = (mobileSyncRepository{db: r.db}).withTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			UPDATE voice_notes
			SET transcription_state = $1, transcription_error = $2, updated_at = $3, revision = revision + 1
			WHERE workspace_id = $4 AND client_id = $5 AND deleted_at IS NULL
		`, state, message, now, workspaceID, clientID)
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if rows == 0 {
			existing, getErr := (voiceNoteRepository{db: tx}).GetByClientID(ctx, clientID)
			if getErr == nil && (existing.DeletedAt != nil || existing.AudioState == model.VoiceAudioDeleteRequested || existing.AudioState == model.VoiceAudioDeleted) {
				return storage.ErrVoiceAudioGone
			}
			return sql.ErrNoRows
		}
		if err := persistPostgresServerEntityChange(ctx, tx, workspaceID, uuid.NewString(), "voice_note", "voice.server_updated", clientID, time.Unix(now, 0).UTC()); err != nil {
			return err
		}
		voice, err = (voiceNoteRepository{db: tx}).GetByClientID(ctx, clientID)
		return err
	})
	return voice, err
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
		WHERE v.workspace_id = $1 AND v.deleted_at IS NULL ORDER BY v.updated_at DESC LIMIT $2
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
		v.revision, v.deleted_at, v.audio_revision, v.audio_state,
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
	var deletedAt sql.NullTime
	err := scanner.Scan(
		&voice.ID, &voice.WorkspaceID, &voice.ClientID, &voice.NoteID, &voice.Title, &voice.Body,
		&voice.Revision, &deletedAt, &voice.AudioRevision, &voice.AudioState,
		&voice.DurationMS, &voice.RecordedAt, &voice.Language, &voice.ObjectKey, &voice.MimeType,
		&voice.AudioSize, &voice.AudioSHA256, &voice.UploadState, &voice.TranscriptionState,
		&voice.TranscriptionError, &voice.CreatedAt, &voice.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if deletedAt.Valid {
		value := deletedAt.Time.UTC().Unix()
		voice.DeletedAt = &value
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
