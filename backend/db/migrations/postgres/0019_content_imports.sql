CREATE TABLE IF NOT EXISTS content_imports (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  idempotency_key TEXT NOT NULL,
  request_sha256 TEXT NOT NULL,
  source_url TEXT NOT NULL,
  source_type TEXT NOT NULL DEFAULT '',
  canonical_url TEXT NOT NULL DEFAULT '',
  external_id TEXT NOT NULL DEFAULT '',
  feed_url TEXT NOT NULL DEFAULT '',
  title TEXT NOT NULL DEFAULT '',
  podcast_title TEXT NOT NULL DEFAULT '',
  cover_url TEXT NOT NULL DEFAULT '',
  description TEXT NOT NULL DEFAULT '',
  duration_seconds BIGINT NOT NULL DEFAULT 0,
  transcript_url TEXT NOT NULL DEFAULT '',
  audio_url TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL,
  stage TEXT NOT NULL,
  progress INTEGER NOT NULL DEFAULT 0 CHECK(progress BETWEEN 0 AND 100),
  summarize_with_ai BOOLEAN NOT NULL DEFAULT TRUE,
  include_transcript BOOLEAN NOT NULL DEFAULT FALSE,
  language TEXT NOT NULL DEFAULT 'auto',
  folder_id TEXT NOT NULL DEFAULT '',
  project_ids TEXT NOT NULL DEFAULT '[]',
  tags TEXT NOT NULL DEFAULT '[]',
  result_note_id TEXT NOT NULL DEFAULT '',
  error_code TEXT NOT NULL DEFAULT '',
  error_message TEXT NOT NULL DEFAULT '',
  attempt BIGINT NOT NULL DEFAULT 0,
  max_attempts BIGINT NOT NULL DEFAULT 4,
  next_attempt_at BIGINT,
  lease_owner TEXT NOT NULL DEFAULT '',
  lease_token TEXT NOT NULL DEFAULT '',
  lease_expires_at BIGINT,
  revision BIGINT NOT NULL DEFAULT 1,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  UNIQUE(workspace_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS content_imports_workspace_created_idx
  ON content_imports(workspace_id, created_at DESC);
CREATE INDEX IF NOT EXISTS content_imports_worker_idx
  ON content_imports(status, stage, lease_expires_at, created_at);

CREATE TABLE IF NOT EXISTS content_import_artifacts (
  workspace_id TEXT NOT NULL,
  import_id TEXT NOT NULL REFERENCES content_imports(id) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  inline_text TEXT NOT NULL DEFAULT '',
  sha256 TEXT NOT NULL,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  PRIMARY KEY(workspace_id, import_id, kind)
);
