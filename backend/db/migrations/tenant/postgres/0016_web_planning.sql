CREATE TABLE plan_drafts (
  workspace_id TEXT NOT NULL REFERENCES tenant_workspaces(workspace_id) ON DELETE CASCADE,
  owner_id TEXT NOT NULL,
  id TEXT NOT NULL,
  request_id TEXT NOT NULL,
  request_hash TEXT NOT NULL,
  revision BIGINT NOT NULL CHECK(revision > 0),
  status TEXT NOT NULL,
  cleanup_needed INTEGER NOT NULL DEFAULT 0 CHECK(cleanup_needed IN (0,1)),
  audio_expires_at TEXT NOT NULL DEFAULT '',
  record_json TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  PRIMARY KEY(workspace_id, id),
  UNIQUE(workspace_id, owner_id, id),
  UNIQUE(workspace_id, owner_id, request_id)
);
CREATE INDEX plan_drafts_owner_updated_idx ON plan_drafts(workspace_id, owner_id, updated_at DESC);
CREATE INDEX plan_drafts_cleanup_idx ON plan_drafts(workspace_id, cleanup_needed, audio_expires_at);

CREATE TABLE plan_jobs (
  workspace_id TEXT NOT NULL,
  owner_id TEXT NOT NULL,
  id TEXT NOT NULL,
  draft_id TEXT NOT NULL,
  request_id TEXT NOT NULL,
  request_hash TEXT NOT NULL,
  revision BIGINT NOT NULL CHECK(revision > 0),
  state TEXT NOT NULL CHECK(state IN ('queued','processing','completed','failed','cancelled','stale')),
  lease_expires_at TEXT NOT NULL,
  record_json TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  PRIMARY KEY(workspace_id, id),
  UNIQUE(workspace_id, owner_id, request_id),
  FOREIGN KEY(workspace_id, owner_id, draft_id) REFERENCES plan_drafts(workspace_id, owner_id, id) ON DELETE CASCADE
);
CREATE INDEX plan_jobs_worker_idx ON plan_jobs(workspace_id, state, lease_expires_at, created_at);
CREATE INDEX plan_jobs_draft_idx ON plan_jobs(workspace_id, owner_id, draft_id);

CREATE TABLE plan_commits (
  workspace_id TEXT NOT NULL,
  owner_id TEXT NOT NULL,
  draft_id TEXT NOT NULL,
  request_id TEXT NOT NULL,
  request_hash TEXT NOT NULL,
  receipt_json TEXT NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY(workspace_id, draft_id),
  UNIQUE(workspace_id, owner_id, request_id),
  FOREIGN KEY(workspace_id, owner_id, draft_id) REFERENCES plan_drafts(workspace_id, owner_id, id)
);
