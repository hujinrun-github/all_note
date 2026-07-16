CREATE TABLE IF NOT EXISTS voice_audio_cleanup_jobs (
  job_id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  voice_note_id TEXT NOT NULL,
  object_key TEXT NOT NULL,
  state TEXT NOT NULL CHECK (state IN ('queued', 'processing', 'retry_waiting', 'completed', 'failed')),
  revision BIGINT NOT NULL DEFAULT 1,
  attempt BIGINT NOT NULL DEFAULT 0,
  max_attempts BIGINT NOT NULL DEFAULT 6,
  error_code TEXT NOT NULL DEFAULT '',
  next_attempt_at BIGINT,
  lease_owner TEXT NOT NULL DEFAULT '',
  lease_token TEXT NOT NULL DEFAULT '',
  lease_expires_at BIGINT,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  UNIQUE (workspace_id, voice_note_id, object_key)
);

CREATE INDEX IF NOT EXISTS voice_audio_cleanup_eligible_idx
  ON voice_audio_cleanup_jobs (workspace_id, state, next_attempt_at, created_at);
