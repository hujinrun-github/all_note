package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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
	var lease *model.TranscriptionJobLease
	err := withSQLiteTranscriptionWorkerTx(ctx, r.db, func(tx *sql.Tx) error {
		var jobID string
		err := tx.QueryRowContext(ctx, `
			SELECT job_id
			FROM transcription_jobs
			WHERE state = 'queued'
				OR (state = 'retry_waiting' AND (next_attempt_at IS NULL OR next_attempt_at <= ?))
				OR (state = 'processing' AND lease_expires_at IS NOT NULL AND lease_expires_at <= ?)
			ORDER BY created_at, job_id
			LIMIT 1
		`, claim.Now, claim.Now).Scan(&jobID)
		if errors.Is(err, sql.ErrNoRows) {
			return storage.ErrNoTranscriptionJob
		}
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE transcription_jobs
			SET state = 'processing',
				attempt = attempt + 1,
				revision = revision + 1,
				lease_owner = ?,
				lease_token = ?,
				lease_expires_at = ?,
				heartbeat_at = ?,
				updated_at = ?
			WHERE job_id = ?
				AND (
					state = 'queued'
					OR (state = 'retry_waiting' AND (next_attempt_at IS NULL OR next_attempt_at <= ?))
					OR (state = 'processing' AND lease_expires_at IS NOT NULL AND lease_expires_at <= ?)
				)
		`, claim.WorkerID, claim.LeaseToken, claim.LeaseExpiresAt, claim.Now, claim.Now, jobID, claim.Now, claim.Now)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return storage.ErrNoTranscriptionJob
		}
		lease, err = scanSQLiteTranscriptionLease(tx.QueryRowContext(ctx, sqliteTranscriptionLeaseSelect+` WHERE job_id = ?`, jobID))
		return err
	})
	return lease, err
}

func (r transcriptionJobWorkerRepository) Heartbeat(ctx context.Context, heartbeat model.HeartbeatTranscriptionJob) (*model.TranscriptionJobLease, error) {
	if heartbeat.JobID == "" || heartbeat.LeaseToken == "" || heartbeat.LeaseExpiresAt <= heartbeat.Now {
		return nil, errors.New("invalid transcription job heartbeat")
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE transcription_jobs
		SET lease_expires_at = ?, heartbeat_at = ?, updated_at = ?, revision = revision + 1
		WHERE job_id = ? AND state = 'processing' AND lease_token = ?
			AND lease_expires_at IS NOT NULL AND lease_expires_at >= ?
	`, heartbeat.LeaseExpiresAt, heartbeat.Now, heartbeat.Now, heartbeat.JobID, heartbeat.LeaseToken, heartbeat.Now)
	if err != nil {
		return nil, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if affected != 1 {
		return nil, storage.ErrTranscriptionLeaseLost
	}
	return scanSQLiteTranscriptionLease(r.db.QueryRowContext(ctx, sqliteTranscriptionLeaseSelect+` WHERE job_id = ?`, heartbeat.JobID))
}

func (r transcriptionJobWorkerRepository) Fail(ctx context.Context, failure model.FailTranscriptionJob) (*model.TranscriptionJob, error) {
	if failure.JobID == "" || failure.LeaseToken == "" || failure.ErrorCode == "" {
		return nil, errors.New("invalid transcription job failure")
	}
	var job *model.TranscriptionJob
	err := withSQLiteTranscriptionWorkerTx(ctx, r.db, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			UPDATE transcription_jobs
			SET state = CASE WHEN attempt >= max_attempts THEN 'failed' ELSE 'retry_waiting' END,
				error_code = ?,
				next_attempt_at = CASE WHEN attempt >= max_attempts THEN NULL ELSE ? END,
				lease_owner = '', lease_token = '', lease_expires_at = NULL, heartbeat_at = NULL,
				updated_at = ?, revision = revision + 1
			WHERE job_id = ? AND state = 'processing' AND lease_token = ?
				AND lease_expires_at IS NOT NULL AND lease_expires_at >= ?
		`, failure.ErrorCode, failure.NextAttemptAt, failure.Now, failure.JobID, failure.LeaseToken, failure.Now)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return storage.ErrTranscriptionLeaseLost
		}
		job, err = scanSQLiteTranscriptionJob(tx.QueryRowContext(ctx, sqliteTranscriptionJobSelect+` WHERE job_id = ?`, failure.JobID))
		if err != nil {
			return err
		}
		if job.State == model.TranscriptionJobFailed {
			var workspaceID string
			if err := tx.QueryRowContext(ctx, `SELECT workspace_id FROM transcription_jobs WHERE job_id = ?`, failure.JobID).Scan(&workspaceID); err != nil {
				return err
			}
			result, err := tx.ExecContext(ctx, `
				UPDATE voice_notes
				SET transcription_state = ?, transcription_error = ?, updated_at = ?, revision = revision + 1
				WHERE workspace_id = ? AND client_id = ? AND deleted_at IS NULL
			`, model.TranscriptionFailed, failure.ErrorCode, failure.Now, workspaceID, job.VoiceNoteID)
			if err != nil {
				return err
			}
			if affected, err := result.RowsAffected(); err != nil || affected != 1 {
				if err != nil {
					return err
				}
				return storage.ErrMobileEntityNotFound
			}
			if err := persistSQLiteTranscriptionJobChange(ctx, tx, workspaceID, job, "transcription_job.failed", failure.Now); err != nil {
				return err
			}
			return persistSQLiteServerEntityChange(ctx, tx, workspaceID, job.JobID, "voice_note", "voice.server_updated", job.VoiceNoteID, failure.Now)
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
	err := withSQLiteTranscriptionWorkerTx(ctx, r.db, func(tx *sql.Tx) error {
		var workspaceID, voiceNoteID string
		err := tx.QueryRowContext(ctx, `
			SELECT workspace_id, voice_note_id
			FROM transcription_jobs
			WHERE job_id = ? AND state = 'processing' AND lease_token = ?
				AND lease_expires_at IS NOT NULL AND lease_expires_at >= ?
		`, completion.JobID, completion.LeaseToken, completion.Now).Scan(&workspaceID, &voiceNoteID)
		if errors.Is(err, sql.ErrNoRows) {
			return storage.ErrTranscriptionLeaseLost
		}
		if err != nil {
			return err
		}
		var noteID string
		if err := tx.QueryRowContext(ctx, `
			SELECT note_id FROM voice_notes WHERE workspace_id = ? AND client_id = ?
		`, workspaceID, voiceNoteID).Scan(&noteID); err != nil {
			return err
		}
		var body string
		if err := tx.QueryRowContext(ctx, `
			SELECT body FROM notes WHERE workspace_id = ? AND id = ? AND deleted_at IS NULL
		`, workspaceID, noteID).Scan(&body); err != nil {
			return err
		}
		applied := body == ""
		state := model.TranscriptionJobNeedsReview
		if applied {
			state = model.TranscriptionJobCompleted
			if _, err := tx.ExecContext(ctx, `
				UPDATE notes SET body = ?, updated_at = ?, revision = revision + 1
				WHERE workspace_id = ? AND id = ? AND body = '' AND deleted_at IS NULL
			`, completion.Text, completion.Now, workspaceID, noteID); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO transcription_results (job_id, workspace_id, voice_note_id, text, applied, created_at)
			VALUES (?, ?, ?, ?, ?, ?)
		`, completion.JobID, workspaceID, voiceNoteID, completion.Text, applied, completion.Now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE voice_notes
			SET transcription_state = ?, transcription_error = '', updated_at = ?, revision = revision + 1
			WHERE workspace_id = ? AND client_id = ? AND deleted_at IS NULL
		`, model.TranscriptionCompleted, completion.Now, workspaceID, voiceNoteID); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE transcription_jobs
			SET state = ?, error_code = '', next_attempt_at = NULL,
				lease_owner = '', lease_token = '', lease_expires_at = NULL, heartbeat_at = NULL,
				updated_at = ?, revision = revision + 1
			WHERE job_id = ? AND state = 'processing' AND lease_token = ?
		`, state, completion.Now, completion.JobID, completion.LeaseToken)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return storage.ErrTranscriptionLeaseLost
		}
		job, err = scanSQLiteTranscriptionJob(tx.QueryRowContext(ctx, sqliteTranscriptionJobSelect+` WHERE job_id = ?`, completion.JobID))
		if err != nil {
			return err
		}
		return persistSQLiteTranscriptionCompletionChanges(ctx, tx, workspaceID, noteID, job, applied, completion.Now)
	})
	return job, err
}

func persistSQLiteTranscriptionCompletionChanges(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID, noteID string,
	job *model.TranscriptionJob,
	applied bool,
	now int64,
) error {
	if err := persistSQLiteTranscriptionJobChange(ctx, tx, workspaceID, job, "transcription_job.completed", now); err != nil {
		return err
	}
	if err := persistSQLiteServerEntityChange(ctx, tx, workspaceID, job.JobID, "voice_note", "voice.server_updated", job.VoiceNoteID, now); err != nil {
		return err
	}
	if !applied {
		return nil
	}
	clientID := deterministicSQLiteMobileNoteClientID(workspaceID, noteID)
	if _, err := tx.ExecContext(ctx, `
		UPDATE notes SET client_id = COALESCE(client_id, ?)
		WHERE workspace_id = ? AND id = ?
	`, clientID, workspaceID, noteID); err != nil {
		return err
	}
	var revision int64
	var title, body, folderID, tags string
	if err := tx.QueryRowContext(ctx, `
		SELECT client_id, revision, title, body, folder_id, tags
		FROM notes WHERE workspace_id = ? AND id = ? AND deleted_at IS NULL
	`, workspaceID, noteID).Scan(&clientID, &revision, &title, &body, &folderID, &tags); err != nil {
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
		) VALUES (?, ?, 'note', ?, 'note.transcription_applied', ?, ?, ?)
	`, workspaceID, job.JobID, clientID, revision, string(noteJSON), now)
	return err
}

func persistSQLiteTranscriptionJobChange(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID string,
	job *model.TranscriptionJob,
	operation string,
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
	_, err = tx.ExecContext(ctx, `
		INSERT INTO mobile_sync_outbox (
			workspace_id, mutation_id, entity_type, entity_client_id, operation, revision, entity_json, created_at
		) VALUES (?, ?, 'transcription_job', ?, ?, ?, ?, ?)
	`, workspaceID, job.JobID, job.JobID, operation, job.Revision, string(jobJSON), now)
	return err
}

func withSQLiteTranscriptionWorkerTx(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) error {
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

const sqliteTranscriptionLeaseSelect = `
	SELECT job_id, voice_note_id, generation, state, revision, error_code, next_attempt_at,
		language, attempt, max_attempts, created_at, updated_at,
		workspace_id, lease_token, lease_expires_at
	FROM transcription_jobs
`

func scanSQLiteTranscriptionLease(row *sql.Row) (*model.TranscriptionJobLease, error) {
	var lease model.TranscriptionJobLease
	var nextAttempt sql.NullInt64
	var leaseExpires sql.NullInt64
	if err := row.Scan(
		&lease.Job.JobID, &lease.Job.VoiceNoteID, &lease.Job.Generation, &lease.Job.State,
		&lease.Job.Revision, &lease.Job.ErrorCode, &nextAttempt, &lease.Job.Language,
		&lease.Job.Attempt, &lease.Job.MaxAttempts, &lease.Job.CreatedAt, &lease.Job.UpdatedAt,
		&lease.WorkspaceID, &lease.LeaseToken, &leaseExpires,
	); err != nil {
		return nil, fmt.Errorf("scan transcription job lease: %w", err)
	}
	if nextAttempt.Valid {
		lease.Job.NextAttemptAt = &nextAttempt.Int64
	}
	if leaseExpires.Valid {
		lease.LeaseExpiresAt = leaseExpires.Int64
	}
	return &lease, nil
}
