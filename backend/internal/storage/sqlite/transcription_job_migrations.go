package sqlite

import (
	"context"
	"database/sql"
	"fmt"
)

func ensureSQLiteTranscriptionJobSchema(ctx context.Context, db *sql.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS transcription_jobs (
			job_id TEXT PRIMARY KEY,
			workspace_id TEXT NOT NULL,
			voice_note_id TEXT NOT NULL,
			generation INTEGER NOT NULL,
			state TEXT NOT NULL,
			revision INTEGER NOT NULL DEFAULT 1,
			language TEXT NOT NULL DEFAULT '',
			attempt INTEGER NOT NULL DEFAULT 0,
			max_attempts INTEGER NOT NULL DEFAULT 6,
			error_code TEXT NOT NULL DEFAULT '',
			next_attempt_at INTEGER,
			lease_owner TEXT NOT NULL DEFAULT '',
			lease_token TEXT NOT NULL DEFAULT '',
			lease_expires_at INTEGER,
			heartbeat_at INTEGER,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			UNIQUE (workspace_id, voice_note_id, generation)
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS transcription_jobs_one_active_idx
			ON transcription_jobs (workspace_id, voice_note_id)
			WHERE state IN ('waiting_for_audio', 'queued', 'processing', 'retry_waiting')`,
		`CREATE TABLE IF NOT EXISTS transcription_job_requests (
			workspace_id TEXT NOT NULL,
			mutation_id TEXT NOT NULL,
			request_sha256 TEXT NOT NULL,
			job_id TEXT NOT NULL,
			response_json TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			PRIMARY KEY (workspace_id, mutation_id),
			FOREIGN KEY (job_id) REFERENCES transcription_jobs(job_id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS transcription_results (
			job_id TEXT PRIMARY KEY,
			workspace_id TEXT NOT NULL,
			voice_note_id TEXT NOT NULL,
			text TEXT NOT NULL,
			applied INTEGER NOT NULL,
			created_at INTEGER NOT NULL,
			FOREIGN KEY (job_id) REFERENCES transcription_jobs(job_id) ON DELETE CASCADE
		)`,
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("ensure transcription job schema: %w", err)
		}
	}
	return nil
}
