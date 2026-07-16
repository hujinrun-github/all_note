package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

type voiceAudioCleanupRepository struct {
	db sqliteRunner
}

func (r voiceAudioCleanupRepository) ClaimNext(ctx context.Context, claim model.ClaimVoiceAudioCleanupJob) (*model.VoiceAudioCleanupLease, error) {
	if claim.WorkerID == "" || claim.LeaseToken == "" || claim.LeaseExpiresAt <= claim.Now {
		return nil, storage.ErrVoiceAudioCleanupLeaseLost
	}
	var lease *model.VoiceAudioCleanupLease
	err := (mobileSyncRepository{db: r.db}).withTx(ctx, func(tx *sql.Tx) error {
		var jobID string
		var revision int64
		err := tx.QueryRowContext(ctx, `
			SELECT job_id, revision FROM voice_audio_cleanup_jobs
			WHERE (
				state = 'queued' OR
				(state = 'retry_waiting' AND COALESCE(next_attempt_at, 0) <= ?) OR
				(state = 'processing' AND COALESCE(lease_expires_at, 0) <= ?)
			)
			ORDER BY created_at, job_id LIMIT 1
		`, claim.Now, claim.Now).Scan(&jobID, &revision)
		if errors.Is(err, sql.ErrNoRows) {
			return storage.ErrNoVoiceAudioCleanupJob
		}
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE voice_audio_cleanup_jobs SET state = 'processing', revision = revision + 1,
				attempt = attempt + 1, error_code = '', next_attempt_at = NULL,
				lease_owner = ?, lease_token = ?, lease_expires_at = ?, updated_at = ?
			WHERE job_id = ? AND revision = ?
		`, claim.WorkerID, claim.LeaseToken, claim.LeaseExpiresAt, claim.Now, jobID, revision)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			if err != nil {
				return err
			}
			return storage.ErrNoVoiceAudioCleanupJob
		}
		job, err := scanSQLiteVoiceAudioCleanupJob(tx.QueryRowContext(ctx, sqliteVoiceAudioCleanupSelect+` WHERE job_id = ?`, jobID))
		if err != nil {
			return err
		}
		lease = &model.VoiceAudioCleanupLease{Job: *job, WorkspaceID: job.WorkspaceID, LeaseToken: claim.LeaseToken, LeaseExpiresAt: claim.LeaseExpiresAt}
		return nil
	})
	return lease, err
}

func (r voiceAudioCleanupRepository) Complete(ctx context.Context, completion model.CompleteVoiceAudioCleanupJob) (*model.VoiceAudioCleanupJob, error) {
	var job *model.VoiceAudioCleanupJob
	err := (mobileSyncRepository{db: r.db}).withTx(ctx, func(tx *sql.Tx) error {
		current, err := scanSQLiteVoiceAudioCleanupJob(tx.QueryRowContext(ctx, sqliteVoiceAudioCleanupSelect+` WHERE job_id = ?`, completion.JobID))
		if errors.Is(err, sql.ErrNoRows) {
			return storage.ErrVoiceAudioCleanupLeaseLost
		}
		if err != nil {
			return err
		}
		workspaceID := current.WorkspaceID
		result, err := tx.ExecContext(ctx, `
			UPDATE voice_audio_cleanup_jobs SET state = 'completed', revision = revision + 1,
				error_code = '', next_attempt_at = NULL, lease_owner = '', lease_token = '', lease_expires_at = NULL, updated_at = ?
			WHERE workspace_id = ? AND job_id = ? AND state = 'processing' AND lease_token = ?
		`, completion.Now, workspaceID, completion.JobID, completion.LeaseToken)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			if err != nil {
				return err
			}
			return storage.ErrVoiceAudioCleanupLeaseLost
		}
		voiceResult, err := tx.ExecContext(ctx, `
			UPDATE voice_notes SET audio_state = 'deleted', audio_revision = audio_revision + 1,
				object_key = '', mime_type = '', audio_size = 0, audio_sha256 = '', upload_state = 'failed',
				revision = revision + 1, updated_at = ?
			WHERE workspace_id = ? AND client_id = ? AND object_key = ? AND audio_state = 'delete_requested'
		`, completion.Now, workspaceID, current.VoiceNoteID, current.ObjectKey)
		if err != nil {
			return err
		}
		if affected, err := voiceResult.RowsAffected(); err != nil || affected != 1 {
			if err != nil {
				return err
			}
			return storage.ErrVoiceAudioCleanupLeaseLost
		}
		if err := persistSQLiteServerEntityChange(ctx, tx, workspaceID, uuid.NewString(), "voice_note", "voice.audio_deleted", current.VoiceNoteID, completion.Now); err != nil {
			return err
		}
		job, err = scanSQLiteVoiceAudioCleanupJob(tx.QueryRowContext(ctx, sqliteVoiceAudioCleanupSelect+` WHERE workspace_id = ? AND job_id = ?`, workspaceID, completion.JobID))
		return err
	})
	return job, err
}

func (r voiceAudioCleanupRepository) Fail(ctx context.Context, failure model.FailVoiceAudioCleanupJob) (*model.VoiceAudioCleanupJob, error) {
	var job *model.VoiceAudioCleanupJob
	err := (mobileSyncRepository{db: r.db}).withTx(ctx, func(tx *sql.Tx) error {
		current, err := scanSQLiteVoiceAudioCleanupJob(tx.QueryRowContext(ctx, sqliteVoiceAudioCleanupSelect+` WHERE job_id = ?`, failure.JobID))
		if err != nil {
			return err
		}
		workspaceID := current.WorkspaceID
		state := model.VoiceAudioCleanupRetryWaiting
		var nextAttemptAt any = failure.NextAttemptAt
		if current.Attempt >= current.MaxAttempts {
			state = model.VoiceAudioCleanupFailed
			nextAttemptAt = nil
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE voice_audio_cleanup_jobs SET state = ?, revision = revision + 1, error_code = ?, next_attempt_at = ?,
				lease_owner = '', lease_token = '', lease_expires_at = NULL, updated_at = ?
			WHERE workspace_id = ? AND job_id = ? AND state = 'processing' AND lease_token = ?
		`, state, failure.ErrorCode, nextAttemptAt, failure.Now, workspaceID, failure.JobID, failure.LeaseToken)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			if err != nil {
				return err
			}
			return storage.ErrVoiceAudioCleanupLeaseLost
		}
		job, err = scanSQLiteVoiceAudioCleanupJob(tx.QueryRowContext(ctx, sqliteVoiceAudioCleanupSelect+` WHERE workspace_id = ? AND job_id = ?`, workspaceID, failure.JobID))
		return err
	})
	return job, err
}

func (r voiceAudioCleanupRepository) Get(ctx context.Context, jobID string) (*model.VoiceAudioCleanupJob, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return scanSQLiteVoiceAudioCleanupJob(r.db.QueryRowContext(ctx, sqliteVoiceAudioCleanupSelect+` WHERE workspace_id = ? AND job_id = ?`, workspaceID, jobID))
}

const sqliteVoiceAudioCleanupSelect = `
	SELECT job_id, voice_note_id, object_key, state, revision, attempt, max_attempts,
		error_code, next_attempt_at, created_at, updated_at, workspace_id
	FROM voice_audio_cleanup_jobs
`

func scanSQLiteVoiceAudioCleanupJob(row *sql.Row) (*model.VoiceAudioCleanupJob, error) {
	var job model.VoiceAudioCleanupJob
	var nextAttemptAt sql.NullInt64
	if err := row.Scan(&job.JobID, &job.VoiceNoteID, &job.ObjectKey, &job.State, &job.Revision,
		&job.Attempt, &job.MaxAttempts, &job.ErrorCode, &nextAttemptAt, &job.CreatedAt, &job.UpdatedAt, &job.WorkspaceID); err != nil {
		return nil, err
	}
	if nextAttemptAt.Valid {
		value := nextAttemptAt.Int64
		job.NextAttemptAt = &value
	}
	return &job, nil
}
