package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

type transcriptionJobWorkerRepository struct {
	db *sql.DB
}

func (r transcriptionJobWorkerRepository) ClaimNext(ctx context.Context, claim model.ClaimTranscriptionJob) (*model.TranscriptionJobLease, error) {
	if claim.WorkerID == "" || claim.LeaseToken == "" || claim.LeaseExpiresAt <= claim.Now {
		return nil, errors.New("invalid transcription job claim")
	}
	lease, err := scanPostgresTranscriptionLease(r.db.QueryRowContext(ctx, `
		WITH candidate AS (
			SELECT job_id
			FROM transcription_jobs
			WHERE state = 'queued'
				OR (state = 'retry_waiting' AND (next_attempt_at IS NULL OR next_attempt_at <= $1))
				OR (state = 'processing' AND lease_expires_at IS NOT NULL AND lease_expires_at <= $1)
			ORDER BY created_at, job_id
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE transcription_jobs AS job
		SET state = 'processing',
			attempt = job.attempt + 1,
			revision = job.revision + 1,
			lease_owner = $2,
			lease_token = $3,
			lease_expires_at = $4,
			heartbeat_at = $1,
			updated_at = $1
		FROM candidate
		WHERE job.job_id = candidate.job_id
		RETURNING job.job_id, job.voice_note_id, job.generation, job.state, job.revision,
			job.error_code, job.next_attempt_at, job.language, job.attempt, job.max_attempts,
			job.created_at, job.updated_at, job.workspace_id, job.lease_token, job.lease_expires_at
	`, claim.Now, claim.WorkerID, claim.LeaseToken, claim.LeaseExpiresAt))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storage.ErrNoTranscriptionJob
	}
	return lease, err
}

func (r transcriptionJobWorkerRepository) Heartbeat(ctx context.Context, heartbeat model.HeartbeatTranscriptionJob) (*model.TranscriptionJobLease, error) {
	if heartbeat.JobID == "" || heartbeat.LeaseToken == "" || heartbeat.LeaseExpiresAt <= heartbeat.Now {
		return nil, errors.New("invalid transcription job heartbeat")
	}
	lease, err := scanPostgresTranscriptionLease(r.db.QueryRowContext(ctx, `
		UPDATE transcription_jobs
		SET lease_expires_at = $1, heartbeat_at = $2, updated_at = $2, revision = revision + 1
		WHERE job_id = $3 AND state = 'processing' AND lease_token = $4
			AND lease_expires_at IS NOT NULL AND lease_expires_at >= $2
		RETURNING job_id, voice_note_id, generation, state, revision, error_code, next_attempt_at,
			language, attempt, max_attempts, created_at, updated_at, workspace_id, lease_token, lease_expires_at
	`, heartbeat.LeaseExpiresAt, heartbeat.Now, heartbeat.JobID, heartbeat.LeaseToken))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storage.ErrTranscriptionLeaseLost
	}
	return lease, err
}

func (r transcriptionJobWorkerRepository) Fail(ctx context.Context, failure model.FailTranscriptionJob) (*model.TranscriptionJob, error) {
	if failure.JobID == "" || failure.LeaseToken == "" || failure.ErrorCode == "" {
		return nil, errors.New("invalid transcription job failure")
	}
	var job *model.TranscriptionJob
	err := withPostgresTranscriptionWorkerTx(ctx, r.db, func(tx *sql.Tx) error {
		var err error
		job, err = scanPostgresTranscriptionJob(tx.QueryRowContext(ctx, `
			UPDATE transcription_jobs
			SET state = CASE WHEN attempt >= max_attempts THEN 'failed' ELSE 'retry_waiting' END,
				error_code = $1,
				next_attempt_at = CASE WHEN attempt >= max_attempts THEN NULL ELSE $2::bigint END,
				lease_owner = '', lease_token = '', lease_expires_at = NULL, heartbeat_at = NULL,
				updated_at = $3, revision = revision + 1
			WHERE job_id = $4 AND state = 'processing' AND lease_token = $5
				AND lease_expires_at IS NOT NULL AND lease_expires_at >= $3
			RETURNING job_id, voice_note_id, generation, state, revision, error_code, next_attempt_at,
				language, attempt, max_attempts, created_at, updated_at
		`, failure.ErrorCode, failure.NextAttemptAt, failure.Now, failure.JobID, failure.LeaseToken))
		if errors.Is(err, sql.ErrNoRows) {
			return storage.ErrTranscriptionLeaseLost
		}
		if err != nil {
			return err
		}
		if job.State == model.TranscriptionJobFailed {
			_, err = tx.ExecContext(ctx, `
				UPDATE voice_notes
				SET transcription_state = $1, transcription_error = $2, updated_at = $3
				WHERE client_id = $4
			`, model.TranscriptionFailed, failure.ErrorCode, failure.Now, job.VoiceNoteID)
		}
		return err
	})
	return job, err
}

func (r transcriptionJobWorkerRepository) Complete(ctx context.Context, completion model.CompleteTranscriptionJob) (*model.TranscriptionJob, error) {
	if completion.JobID == "" || completion.LeaseToken == "" || strings.TrimSpace(completion.Text) == "" {
		return nil, errors.New("invalid transcription job completion")
	}
	var job *model.TranscriptionJob
	err := withPostgresTranscriptionWorkerTx(ctx, r.db, func(tx *sql.Tx) error {
		var workspaceID, voiceNoteID string
		err := tx.QueryRowContext(ctx, `
			SELECT workspace_id, voice_note_id
			FROM transcription_jobs
			WHERE job_id = $1 AND state = 'processing' AND lease_token = $2
				AND lease_expires_at IS NOT NULL AND lease_expires_at >= $3
			FOR UPDATE
		`, completion.JobID, completion.LeaseToken, completion.Now).Scan(&workspaceID, &voiceNoteID)
		if errors.Is(err, sql.ErrNoRows) {
			return storage.ErrTranscriptionLeaseLost
		}
		if err != nil {
			return err
		}
		var noteID string
		if err := tx.QueryRowContext(ctx, `
			SELECT note_id FROM voice_notes WHERE workspace_id = $1 AND client_id = $2
		`, workspaceID, voiceNoteID).Scan(&noteID); err != nil {
			return err
		}
		var body string
		if err := tx.QueryRowContext(ctx, `
			SELECT body FROM notes
			WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL
			FOR UPDATE
		`, workspaceID, noteID).Scan(&body); err != nil {
			return err
		}
		applied := body == ""
		state := model.TranscriptionJobNeedsReview
		if applied {
			state = model.TranscriptionJobCompleted
			if _, err := tx.ExecContext(ctx, `
				UPDATE notes SET body = $1, updated_at = to_timestamp($2), revision = revision + 1
				WHERE workspace_id = $3 AND id = $4 AND body = '' AND deleted_at IS NULL
			`, completion.Text, completion.Now, workspaceID, noteID); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO transcription_results (job_id, workspace_id, voice_note_id, text, applied, created_at)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, completion.JobID, workspaceID, voiceNoteID, completion.Text, applied, completion.Now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE voice_notes
			SET transcription_state = $1, transcription_error = '', updated_at = $2
			WHERE workspace_id = $3 AND client_id = $4
		`, model.TranscriptionCompleted, completion.Now, workspaceID, voiceNoteID); err != nil {
			return err
		}
		job, err = scanPostgresTranscriptionJob(tx.QueryRowContext(ctx, `
			UPDATE transcription_jobs
			SET state = $1, error_code = '', next_attempt_at = NULL,
				lease_owner = '', lease_token = '', lease_expires_at = NULL, heartbeat_at = NULL,
				updated_at = $2, revision = revision + 1
			WHERE job_id = $3 AND state = 'processing' AND lease_token = $4
			RETURNING job_id, voice_note_id, generation, state, revision, error_code, next_attempt_at,
				language, attempt, max_attempts, created_at, updated_at
		`, state, completion.Now, completion.JobID, completion.LeaseToken))
		if errors.Is(err, sql.ErrNoRows) {
			return storage.ErrTranscriptionLeaseLost
		}
		if err != nil {
			return err
		}
		return persistPostgresTranscriptionCompletionChanges(ctx, tx, workspaceID, noteID, job, applied, completion.Now)
	})
	return job, err
}

func persistPostgresTranscriptionCompletionChanges(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID, noteID string,
	job *model.TranscriptionJob,
	applied bool,
	now int64,
) error {
	jobPayload, err := json.Marshal(map[string]any{
		"voice_note_id": job.VoiceNoteID,
		"generation":    job.Generation,
		"state":         job.State,
		"error_code":    job.ErrorCode,
	})
	if err != nil {
		return err
	}
	jobEntity := model.MobileEntityEnvelope{
		EntityType: "transcription_job", ID: job.JobID, ClientID: job.JobID, Revision: job.Revision, Payload: jobPayload,
	}
	jobJSON, err := json.Marshal(jobEntity)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO mobile_sync_outbox (
			workspace_id, mutation_id, entity_type, entity_client_id, operation, revision, entity_json, created_at
		) VALUES ($1, $2, 'transcription_job', $2, 'transcription_job.completed', $3, $4::jsonb, to_timestamp($5))
	`, workspaceID, job.JobID, job.Revision, jobJSON, now); err != nil {
		return err
	}
	if !applied {
		return nil
	}
	clientID := deterministicPostgresMobileNoteClientID(workspaceID, noteID)
	if _, err := tx.ExecContext(ctx, `
		UPDATE notes SET client_id = COALESCE(client_id, $1)
		WHERE workspace_id = $2 AND id = $3
	`, clientID, workspaceID, noteID); err != nil {
		return err
	}
	var revision int64
	var title, body, folderID, tags string
	if err := tx.QueryRowContext(ctx, `
		SELECT client_id, revision, title, body, folder_id, COALESCE(array_to_json(tags)::text, '[]')
		FROM notes WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL
	`, workspaceID, noteID).Scan(&clientID, &revision, &title, &body, &folderID, &tags); err != nil {
		return err
	}
	parsedTags, err := tagsJSONStringToArray(tags)
	if err != nil {
		return err
	}
	if err := upsertNoteSearchIndex(ctx, tx, workspaceID, &model.Note{
		ID: noteID, Title: title, Body: body, FolderID: folderID, Tags: tags, UpdatedAt: now,
	}, parsedTags); err != nil {
		return err
	}
	notePayload, err := json.Marshal(map[string]string{
		"title": title, "body": body, "folder_id": folderID, "tags": tags,
	})
	if err != nil {
		return err
	}
	noteEntity := model.MobileEntityEnvelope{
		EntityType: "note", ID: noteID, ClientID: clientID, Revision: revision, Payload: notePayload,
	}
	noteJSON, err := json.Marshal(noteEntity)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO mobile_sync_outbox (
			workspace_id, mutation_id, entity_type, entity_client_id, operation, revision, entity_json, created_at
		) VALUES ($1, $2, 'note', $3, 'note.transcription_applied', $4, $5::jsonb, to_timestamp($6))
	`, workspaceID, job.JobID, clientID, revision, noteJSON, now)
	return err
}

func withPostgresTranscriptionWorkerTx(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) error {
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

func scanPostgresTranscriptionLease(row *sql.Row) (*model.TranscriptionJobLease, error) {
	var lease model.TranscriptionJobLease
	var nextAttempt sql.NullInt64
	var leaseExpires sql.NullInt64
	if err := row.Scan(
		&lease.Job.JobID, &lease.Job.VoiceNoteID, &lease.Job.Generation, &lease.Job.State,
		&lease.Job.Revision, &lease.Job.ErrorCode, &nextAttempt, &lease.Job.Language,
		&lease.Job.Attempt, &lease.Job.MaxAttempts, &lease.Job.CreatedAt, &lease.Job.UpdatedAt,
		&lease.WorkspaceID, &lease.LeaseToken, &leaseExpires,
	); err != nil {
		return nil, err
	}
	if nextAttempt.Valid {
		lease.Job.NextAttemptAt = &nextAttempt.Int64
	}
	if leaseExpires.Valid {
		lease.LeaseExpiresAt = leaseExpires.Int64
	}
	return &lease, nil
}
