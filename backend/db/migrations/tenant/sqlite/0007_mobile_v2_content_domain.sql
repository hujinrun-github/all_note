CREATE UNIQUE INDEX IF NOT EXISTS notes_workspace_client_id_idx
  ON notes(workspace_id, client_id)
  WHERE client_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS inbox (
  id TEXT NOT NULL,
  workspace_id TEXT NOT NULL REFERENCES tenant_workspaces(workspace_id) ON DELETE CASCADE,
  client_id TEXT,
  revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
  kind TEXT NOT NULL DEFAULT 'note',
  title TEXT NOT NULL DEFAULT '',
  body TEXT,
  source TEXT NOT NULL DEFAULT 'quick-capture',
  archived INTEGER NOT NULL DEFAULT 0 CHECK (archived IN (0, 1)),
  converted_to TEXT,
  deleted_at INTEGER,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (workspace_id, id)
);

CREATE UNIQUE INDEX IF NOT EXISTS inbox_workspace_client_id_idx
  ON inbox(workspace_id, client_id)
  WHERE client_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS inbox_workspace_updated_idx
  ON inbox(workspace_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS voice_notes (
  id TEXT NOT NULL,
  workspace_id TEXT NOT NULL REFERENCES tenant_workspaces(workspace_id) ON DELETE CASCADE,
  client_id TEXT NOT NULL,
  revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
  deleted_at INTEGER,
  audio_revision INTEGER NOT NULL DEFAULT 1 CHECK (audio_revision > 0),
  audio_state TEXT NOT NULL DEFAULT 'absent',
  note_id TEXT NOT NULL,
  duration_ms INTEGER NOT NULL DEFAULT 0 CHECK (duration_ms >= 0),
  recorded_at INTEGER NOT NULL,
  language TEXT NOT NULL DEFAULT '',
  object_key TEXT NOT NULL DEFAULT '',
  mime_type TEXT NOT NULL DEFAULT '',
  audio_size INTEGER NOT NULL DEFAULT 0 CHECK (audio_size >= 0),
  audio_sha256 TEXT NOT NULL DEFAULT '',
  upload_state TEXT NOT NULL DEFAULT 'pending',
  transcription_state TEXT NOT NULL DEFAULT 'not_started',
  transcription_error TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (workspace_id, id),
  UNIQUE (workspace_id, client_id),
  UNIQUE (workspace_id, note_id),
  FOREIGN KEY (workspace_id, note_id)
    REFERENCES notes(workspace_id, id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS voice_notes_workspace_updated_idx
  ON voice_notes(workspace_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS transcription_jobs (
  job_id TEXT NOT NULL,
  workspace_id TEXT NOT NULL REFERENCES tenant_workspaces(workspace_id) ON DELETE CASCADE,
  voice_note_id TEXT NOT NULL,
  generation INTEGER NOT NULL CHECK (generation > 0),
  state TEXT NOT NULL,
  revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
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
  PRIMARY KEY (workspace_id, job_id),
  UNIQUE (workspace_id, voice_note_id, generation),
  FOREIGN KEY (workspace_id, voice_note_id)
    REFERENCES voice_notes(workspace_id, client_id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS transcription_jobs_one_active_idx
  ON transcription_jobs(workspace_id, voice_note_id)
  WHERE state IN ('waiting_for_audio', 'queued', 'processing', 'retry_waiting');

CREATE TABLE IF NOT EXISTS transcription_job_requests (
  workspace_id TEXT NOT NULL REFERENCES tenant_workspaces(workspace_id) ON DELETE CASCADE,
  mutation_id TEXT NOT NULL,
  request_sha256 TEXT NOT NULL,
  job_id TEXT NOT NULL,
  response_json TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (workspace_id, mutation_id)
);

CREATE TABLE IF NOT EXISTS transcription_results (
  workspace_id TEXT NOT NULL REFERENCES tenant_workspaces(workspace_id) ON DELETE CASCADE,
  job_id TEXT NOT NULL,
  voice_note_id TEXT NOT NULL,
  text TEXT NOT NULL,
  applied INTEGER NOT NULL CHECK (applied IN (0, 1)),
  created_at INTEGER NOT NULL,
  PRIMARY KEY (workspace_id, job_id)
);

CREATE TABLE IF NOT EXISTS voice_audio_cleanup_jobs (
  job_id TEXT NOT NULL,
  workspace_id TEXT NOT NULL REFERENCES tenant_workspaces(workspace_id) ON DELETE CASCADE,
  voice_note_id TEXT NOT NULL,
  object_key TEXT NOT NULL,
  state TEXT NOT NULL,
  revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
  attempt INTEGER NOT NULL DEFAULT 0,
  max_attempts INTEGER NOT NULL DEFAULT 6,
  error_code TEXT NOT NULL DEFAULT '',
  next_attempt_at INTEGER,
  lease_owner TEXT NOT NULL DEFAULT '',
  lease_token TEXT NOT NULL DEFAULT '',
  lease_expires_at INTEGER,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (workspace_id, job_id),
  UNIQUE (workspace_id, voice_note_id, object_key)
);

CREATE INDEX IF NOT EXISTS voice_audio_cleanup_eligible_idx
  ON voice_audio_cleanup_jobs(workspace_id, state, next_attempt_at, created_at);
