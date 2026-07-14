CREATE TABLE IF NOT EXISTS watch_devices (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  user_id TEXT NOT NULL,
  name TEXT NOT NULL DEFAULT 'Apple Watch',
  token_hash TEXT NOT NULL UNIQUE,
  status TEXT NOT NULL DEFAULT 'active'
    CHECK (status IN ('active', 'revoked')),
  expires_at BIGINT NOT NULL,
  last_seen_at BIGINT NOT NULL,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  revoked_at BIGINT,
  FOREIGN KEY (workspace_id, user_id)
    REFERENCES workspace_members(workspace_id, user_id)
    ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS watch_devices_workspace_active_idx
  ON watch_devices (workspace_id, user_id, expires_at DESC)
  WHERE revoked_at IS NULL AND status = 'active';

CREATE INDEX IF NOT EXISTS watch_devices_user_idx
  ON watch_devices (user_id);

CREATE TABLE IF NOT EXISTS voice_notes (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  client_id TEXT NOT NULL,
  note_id TEXT NOT NULL,
  duration_ms BIGINT NOT NULL DEFAULT 0 CHECK (duration_ms >= 0),
  recorded_at BIGINT NOT NULL,
  language TEXT NOT NULL DEFAULT '',
  object_key TEXT NOT NULL DEFAULT '',
  mime_type TEXT NOT NULL DEFAULT '',
  audio_size BIGINT NOT NULL DEFAULT 0 CHECK (audio_size >= 0),
  audio_sha256 TEXT NOT NULL DEFAULT '',
  upload_state TEXT NOT NULL DEFAULT 'pending'
    CHECK (upload_state IN ('pending', 'uploading', 'uploaded', 'failed')),
  transcription_state TEXT NOT NULL DEFAULT 'not_started'
    CHECK (transcription_state IN ('not_started', 'processing', 'completed', 'failed')),
  transcription_error TEXT NOT NULL DEFAULT '',
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  UNIQUE (workspace_id, client_id),
  UNIQUE (workspace_id, note_id),
  FOREIGN KEY (workspace_id, note_id)
    REFERENCES notes(workspace_id, id)
    ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS voice_notes_workspace_updated_idx
  ON voice_notes (workspace_id, updated_at DESC);

CREATE INDEX IF NOT EXISTS voice_notes_pending_upload_idx
  ON voice_notes (workspace_id, created_at)
  WHERE upload_state IN ('pending', 'uploading', 'failed');

CREATE INDEX IF NOT EXISTS voice_notes_transcription_idx
  ON voice_notes (workspace_id, transcription_state, updated_at DESC);
