ALTER TABLE notes
  ADD COLUMN IF NOT EXISTS client_id TEXT,
  ADD COLUMN IF NOT EXISTS revision BIGINT NOT NULL DEFAULT 1,
  ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;

WITH pending AS (
  SELECT
    id,
    workspace_id,
    md5('flowspace:note:' || workspace_id || ':' || id) AS digest
  FROM notes
  WHERE client_id IS NULL
)
UPDATE notes AS note
SET client_id =
  substr(pending.digest, 1, 8) || '-' ||
  substr(pending.digest, 9, 4) || '-' ||
  '3' || substr(pending.digest, 14, 3) || '-' ||
  '8' || substr(pending.digest, 18, 3) || '-' ||
  substr(pending.digest, 21, 12)
FROM pending
WHERE note.id = pending.id
  AND note.workspace_id = pending.workspace_id
  AND note.client_id IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS notes_workspace_client_id_idx
  ON notes (workspace_id, client_id)
  WHERE client_id IS NOT NULL;

ALTER TABLE tasks
  ADD COLUMN IF NOT EXISTS client_id TEXT,
  ADD COLUMN IF NOT EXISTS revision BIGINT NOT NULL DEFAULT 1,
  ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;

WITH pending AS (
  SELECT id, workspace_id, md5('flowspace:task:' || workspace_id || ':' || id) AS digest
  FROM tasks WHERE client_id IS NULL
)
UPDATE tasks AS entity
SET client_id =
  substr(pending.digest, 1, 8) || '-' || substr(pending.digest, 9, 4) || '-' ||
  '3' || substr(pending.digest, 14, 3) || '-' || '8' || substr(pending.digest, 18, 3) || '-' ||
  substr(pending.digest, 21, 12)
FROM pending
WHERE entity.id = pending.id AND entity.workspace_id = pending.workspace_id AND entity.client_id IS NULL;

ALTER TABLE events
  ADD COLUMN IF NOT EXISTS client_id TEXT,
  ADD COLUMN IF NOT EXISTS revision BIGINT NOT NULL DEFAULT 1,
  ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;

WITH pending AS (
  SELECT id, workspace_id, md5('flowspace:event:' || workspace_id || ':' || id) AS digest
  FROM events WHERE client_id IS NULL
)
UPDATE events AS entity
SET client_id =
  substr(pending.digest, 1, 8) || '-' || substr(pending.digest, 9, 4) || '-' ||
  '3' || substr(pending.digest, 14, 3) || '-' || '8' || substr(pending.digest, 18, 3) || '-' ||
  substr(pending.digest, 21, 12)
FROM pending
WHERE entity.id = pending.id AND entity.workspace_id = pending.workspace_id AND entity.client_id IS NULL;

ALTER TABLE inbox
  ADD COLUMN IF NOT EXISTS client_id TEXT,
  ADD COLUMN IF NOT EXISTS revision BIGINT NOT NULL DEFAULT 1,
  ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;

WITH pending AS (
  SELECT id, workspace_id, md5('flowspace:inbox:' || workspace_id || ':' || id) AS digest
  FROM inbox WHERE client_id IS NULL
)
UPDATE inbox AS entity
SET client_id =
  substr(pending.digest, 1, 8) || '-' || substr(pending.digest, 9, 4) || '-' ||
  '3' || substr(pending.digest, 14, 3) || '-' || '8' || substr(pending.digest, 18, 3) || '-' ||
  substr(pending.digest, 21, 12)
FROM pending
WHERE entity.id = pending.id AND entity.workspace_id = pending.workspace_id AND entity.client_id IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS tasks_workspace_client_id_idx
  ON tasks (workspace_id, client_id) WHERE client_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS events_workspace_client_id_idx
  ON events (workspace_id, client_id) WHERE client_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS inbox_workspace_client_id_idx
  ON inbox (workspace_id, client_id) WHERE client_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS mobile_mutation_receipts (
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  device_client_id TEXT NOT NULL,
  mutation_id TEXT NOT NULL,
  request_sha256 TEXT NOT NULL,
  response_json JSON NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, device_client_id, mutation_id)
);

CREATE TABLE IF NOT EXISTS mobile_sync_outbox (
  sequence BIGSERIAL PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  mutation_id TEXT NOT NULL,
  entity_type TEXT NOT NULL,
  entity_client_id TEXT NOT NULL,
  operation TEXT NOT NULL,
  revision BIGINT NOT NULL,
  entity_json JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  published_at TIMESTAMPTZ,
  UNIQUE (workspace_id, mutation_id, entity_type, entity_client_id)
);

CREATE INDEX IF NOT EXISTS mobile_sync_outbox_pending_idx
  ON mobile_sync_outbox (workspace_id, sequence)
  WHERE published_at IS NULL;

CREATE TABLE IF NOT EXISTS mobile_sync_change_heads (
  workspace_id TEXT PRIMARY KEY REFERENCES workspaces(id) ON DELETE CASCADE,
  latest_position BIGINT NOT NULL DEFAULT 0,
  min_position BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS mobile_sync_changes (
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  position BIGINT NOT NULL,
  operation TEXT NOT NULL,
  entity_json JSONB NOT NULL,
  committed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, position)
);

CREATE TABLE IF NOT EXISTS mobile_sync_snapshot_sessions (
  session_id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  scope TEXT NOT NULL,
  boundary_position BIGINT NOT NULL,
  expires_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS mobile_sync_snapshot_items (
  session_id TEXT NOT NULL REFERENCES mobile_sync_snapshot_sessions(session_id) ON DELETE CASCADE,
  ordinal BIGINT NOT NULL,
  entity_json JSONB NOT NULL,
  PRIMARY KEY (session_id, ordinal)
);

CREATE TABLE IF NOT EXISTS mobile_retired_ids (
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  entity_type TEXT NOT NULL,
  client_id TEXT NOT NULL,
  retired_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, entity_type, client_id)
);

CREATE TABLE IF NOT EXISTS transcription_jobs (
  job_id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  voice_note_id TEXT NOT NULL,
  generation BIGINT NOT NULL,
  state TEXT NOT NULL CHECK (state IN (
    'waiting_for_audio', 'queued', 'processing', 'retry_waiting', 'completed', 'needs_review', 'failed', 'canceled'
  )),
  revision BIGINT NOT NULL DEFAULT 1,
  language TEXT NOT NULL DEFAULT '',
  attempt BIGINT NOT NULL DEFAULT 0,
  max_attempts BIGINT NOT NULL DEFAULT 6,
  error_code TEXT NOT NULL DEFAULT '',
  next_attempt_at BIGINT,
  lease_owner TEXT NOT NULL DEFAULT '',
  lease_token TEXT NOT NULL DEFAULT '',
  lease_expires_at BIGINT,
  heartbeat_at BIGINT,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  UNIQUE (workspace_id, voice_note_id, generation),
  FOREIGN KEY (workspace_id, voice_note_id)
    REFERENCES voice_notes(workspace_id, client_id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS transcription_jobs_one_active_idx
  ON transcription_jobs (workspace_id, voice_note_id)
  WHERE state IN ('waiting_for_audio', 'queued', 'processing', 'retry_waiting');

CREATE TABLE IF NOT EXISTS transcription_job_requests (
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  mutation_id TEXT NOT NULL,
  request_sha256 TEXT NOT NULL,
  job_id TEXT NOT NULL REFERENCES transcription_jobs(job_id) ON DELETE CASCADE,
  response_json JSON NOT NULL,
  created_at BIGINT NOT NULL,
  PRIMARY KEY (workspace_id, mutation_id)
);

CREATE TABLE IF NOT EXISTS transcription_results (
  job_id TEXT PRIMARY KEY REFERENCES transcription_jobs(job_id) ON DELETE CASCADE,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  voice_note_id TEXT NOT NULL,
  text TEXT NOT NULL,
  applied BOOLEAN NOT NULL,
  created_at BIGINT NOT NULL,
  FOREIGN KEY (workspace_id, voice_note_id)
    REFERENCES voice_notes(workspace_id, client_id) ON DELETE CASCADE
);
