package sqlite

import (
	"context"
	"database/sql"
	"fmt"
)

func ensureSQLiteVoiceAudioCleanupSchema(ctx context.Context, db *sql.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS voice_audio_cleanup_jobs (
			job_id TEXT PRIMARY KEY,
			workspace_id TEXT NOT NULL,
			voice_note_id TEXT NOT NULL,
			object_key TEXT NOT NULL,
			state TEXT NOT NULL,
			revision INTEGER NOT NULL DEFAULT 1,
			attempt INTEGER NOT NULL DEFAULT 0,
			max_attempts INTEGER NOT NULL DEFAULT 6,
			error_code TEXT NOT NULL DEFAULT '',
			next_attempt_at INTEGER,
			lease_owner TEXT NOT NULL DEFAULT '',
			lease_token TEXT NOT NULL DEFAULT '',
			lease_expires_at INTEGER,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			UNIQUE (workspace_id, voice_note_id, object_key)
		)`,
		`CREATE INDEX IF NOT EXISTS voice_audio_cleanup_eligible_idx
			ON voice_audio_cleanup_jobs (workspace_id, state, next_attempt_at, created_at)`,
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("ensure voice audio cleanup schema: %w", err)
		}
	}
	return nil
}
