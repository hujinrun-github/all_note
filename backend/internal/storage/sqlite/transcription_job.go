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

type transcriptionJobRepository struct {
	db sqliteRunner
}

func (r transcriptionJobRepository) CreateOrGet(ctx context.Context, request model.CreateTranscriptionJob) (*model.TranscriptionJob, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	var job *model.TranscriptionJob
	err = r.withTx(ctx, func(tx *sql.Tx) error {
		var createErr error
		job, createErr = createOrGetSQLiteTranscriptionJob(ctx, tx, workspaceID, request)
		return createErr
	})
	return job, err
}

func (r transcriptionJobRepository) Retry(ctx context.Context, request model.RetryTranscriptionJob) (*model.TranscriptionJob, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	var job *model.TranscriptionJob
	err = r.withTx(ctx, func(tx *sql.Tx) error {
		var retryErr error
		job, retryErr = retrySQLiteTranscriptionJob(ctx, tx, workspaceID, request)
		return retryErr
	})
	return job, err
}

func (r transcriptionJobRepository) Get(ctx context.Context, jobID string) (*model.TranscriptionJob, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	job, err := scanSQLiteTranscriptionJob(r.db.QueryRowContext(ctx, sqliteTranscriptionJobSelect+`
		WHERE workspace_id = ? AND job_id = ?
	`, workspaceID, jobID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storage.ErrMobileEntityNotFound
	}
	return job, err
}

func (r transcriptionJobRepository) withTx(ctx context.Context, fn func(*sql.Tx) error) error {
	if tx, ok := r.db.(*sql.Tx); ok {
		return fn(tx)
	}
	db, ok := r.db.(*sql.DB)
	if !ok {
		return fmt.Errorf("unsupported sqlite transcription job runner %T", r.db)
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

func createOrGetSQLiteTranscriptionJob(ctx context.Context, tx *sql.Tx, workspaceID string, request model.CreateTranscriptionJob) (*model.TranscriptionJob, error) {
	if job, found, err := getSQLiteTranscriptionJobReceipt(ctx, tx, workspaceID, request); err != nil {
		return nil, err
	} else if found {
		return job, nil
	}

	uploadState, err := sqliteVoiceUploadStateForTranscription(ctx, tx, workspaceID, request.VoiceNoteID)
	if err != nil {
		return nil, err
	}

	active, err := scanSQLiteTranscriptionJob(tx.QueryRowContext(ctx, sqliteTranscriptionJobSelect+`
		WHERE workspace_id = ? AND voice_note_id = ?
			AND state IN ('waiting_for_audio', 'queued', 'processing', 'retry_waiting')
	`, workspaceID, request.VoiceNoteID))
	if err == nil {
		if err := persistSQLiteTranscriptionJobReceipt(ctx, tx, workspaceID, request, active); err != nil {
			return nil, err
		}
		return active, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	var generation int64
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(generation), 0) + 1
		FROM transcription_jobs WHERE workspace_id = ? AND voice_note_id = ?
	`, workspaceID, request.VoiceNoteID).Scan(&generation); err != nil {
		return nil, err
	}
	state := model.TranscriptionJobWaitingForAudio
	if uploadState == model.VoiceUploadUploaded {
		state = model.TranscriptionJobQueued
	}
	job := &model.TranscriptionJob{
		JobID: request.JobID, VoiceNoteID: request.VoiceNoteID, Generation: generation,
		State: state, Revision: 1, Language: request.Language, CreatedAt: request.Now, UpdatedAt: request.Now,
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO transcription_jobs (
			job_id, workspace_id, voice_note_id, generation, state, revision, language, attempt,
			error_code, next_attempt_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, 1, ?, 0, '', NULL, ?, ?)
	`, job.JobID, workspaceID, job.VoiceNoteID, job.Generation, job.State, job.Language, job.CreatedAt, job.UpdatedAt); err != nil {
		return nil, err
	}
	if err := persistSQLiteTranscriptionJobReceipt(ctx, tx, workspaceID, request, job); err != nil {
		return nil, err
	}
	return job, nil
}

func retrySQLiteTranscriptionJob(ctx context.Context, tx *sql.Tx, workspaceID string, request model.RetryTranscriptionJob) (*model.TranscriptionJob, error) {
	receiptRequest := model.CreateTranscriptionJob{
		JobID: request.JobID, MutationID: request.MutationID, RequestSHA256: request.RequestSHA256, Now: request.Now,
	}
	if job, found, err := getSQLiteTranscriptionJobReceipt(ctx, tx, workspaceID, receiptRequest); err != nil {
		return nil, err
	} else if found {
		return job, nil
	}
	failed, err := scanSQLiteTranscriptionJob(tx.QueryRowContext(ctx, sqliteTranscriptionJobSelect+`
		WHERE workspace_id = ? AND job_id = ?
	`, workspaceID, request.FailedJobID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storage.ErrMobileEntityNotFound
	}
	if err != nil {
		return nil, err
	}
	if failed.State != model.TranscriptionJobFailed {
		return nil, storage.ErrTranscriptionJobNotRetryable
	}
	active, err := scanSQLiteTranscriptionJob(tx.QueryRowContext(ctx, sqliteTranscriptionJobSelect+`
		WHERE workspace_id = ? AND voice_note_id = ?
			AND state IN ('waiting_for_audio', 'queued', 'processing', 'retry_waiting')
	`, workspaceID, failed.VoiceNoteID))
	if err == nil {
		if err := persistSQLiteTranscriptionJobReceipt(ctx, tx, workspaceID, receiptRequest, active); err != nil {
			return nil, err
		}
		return active, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	var latestGeneration int64
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(generation), 0)
		FROM transcription_jobs WHERE workspace_id = ? AND voice_note_id = ?
	`, workspaceID, failed.VoiceNoteID).Scan(&latestGeneration); err != nil {
		return nil, err
	}
	if latestGeneration != failed.Generation {
		return nil, storage.ErrStaleTranscriptionJob
	}
	uploadState, err := sqliteVoiceUploadStateForTranscription(ctx, tx, workspaceID, failed.VoiceNoteID)
	if err != nil {
		return nil, err
	}
	generation := latestGeneration + 1
	state := model.TranscriptionJobWaitingForAudio
	if uploadState == model.VoiceUploadUploaded {
		state = model.TranscriptionJobQueued
	}
	job := &model.TranscriptionJob{
		JobID: request.JobID, VoiceNoteID: failed.VoiceNoteID, Generation: generation,
		State: state, Revision: 1, Language: failed.Language, CreatedAt: request.Now, UpdatedAt: request.Now,
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO transcription_jobs (
			job_id, workspace_id, voice_note_id, generation, state, revision, language, attempt,
			error_code, next_attempt_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, 1, ?, 0, '', NULL, ?, ?)
	`, job.JobID, workspaceID, job.VoiceNoteID, job.Generation, job.State, job.Language, job.CreatedAt, job.UpdatedAt); err != nil {
		return nil, err
	}
	if err := persistSQLiteTranscriptionJobReceipt(ctx, tx, workspaceID, receiptRequest, job); err != nil {
		return nil, err
	}
	return job, nil
}

func sqliteVoiceUploadStateForTranscription(ctx context.Context, tx *sql.Tx, workspaceID, clientID string) (string, error) {
	var uploadState, audioState string
	var deletedAt sql.NullInt64
	err := tx.QueryRowContext(ctx, `
		SELECT upload_state, audio_state, deleted_at FROM voice_notes WHERE workspace_id = ? AND client_id = ?
	`, workspaceID, clientID).Scan(&uploadState, &audioState, &deletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", storage.ErrMobileEntityNotFound
	}
	if err != nil {
		return "", err
	}
	if deletedAt.Valid || audioState == model.VoiceAudioDeleteRequested || audioState == model.VoiceAudioDeleted {
		return "", storage.ErrVoiceAudioGone
	}
	return uploadState, nil
}

func getSQLiteTranscriptionJobReceipt(ctx context.Context, tx *sql.Tx, workspaceID string, request model.CreateTranscriptionJob) (*model.TranscriptionJob, bool, error) {
	var requestHash string
	var responseJSON string
	err := tx.QueryRowContext(ctx, `
		SELECT request_sha256, response_json
		FROM transcription_job_requests
		WHERE workspace_id = ? AND mutation_id = ?
	`, workspaceID, request.MutationID).Scan(&requestHash, &responseJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if requestHash != request.RequestSHA256 {
		return nil, false, storage.ErrMutationIDReused
	}
	var job model.TranscriptionJob
	if err := json.Unmarshal([]byte(responseJSON), &job); err != nil {
		return nil, false, fmt.Errorf("decode transcription job receipt: %w", err)
	}
	return &job, true, nil
}

func persistSQLiteTranscriptionJobReceipt(ctx context.Context, tx *sql.Tx, workspaceID string, request model.CreateTranscriptionJob, job *model.TranscriptionJob) error {
	responseJSON, err := json.Marshal(job)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO transcription_job_requests (
			workspace_id, mutation_id, request_sha256, job_id, response_json, created_at
		) VALUES (?, ?, ?, ?, ?, ?)
	`, workspaceID, request.MutationID, request.RequestSHA256, job.JobID, string(responseJSON), request.Now)
	return err
}

const sqliteTranscriptionJobSelect = `
	SELECT job_id, voice_note_id, generation, state, revision, error_code, next_attempt_at,
		language, attempt, max_attempts, created_at, updated_at
	FROM transcription_jobs
`

func scanSQLiteTranscriptionJob(row *sql.Row) (*model.TranscriptionJob, error) {
	var job model.TranscriptionJob
	var nextAttempt sql.NullInt64
	if err := row.Scan(
		&job.JobID, &job.VoiceNoteID, &job.Generation, &job.State, &job.Revision, &job.ErrorCode,
		&nextAttempt, &job.Language, &job.Attempt, &job.MaxAttempts, &job.CreatedAt, &job.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if nextAttempt.Valid {
		job.NextAttemptAt = &nextAttempt.Int64
	}
	return &job, nil
}
