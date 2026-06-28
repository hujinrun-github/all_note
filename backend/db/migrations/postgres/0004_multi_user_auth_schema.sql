CREATE TABLE IF NOT EXISTS users (
  id TEXT PRIMARY KEY,
  email TEXT NOT NULL,
  display_name TEXT NOT NULL DEFAULT '',
  password_hash TEXT NOT NULL,
  must_change_password BOOLEAN NOT NULL DEFAULT false,
  default_workspace_id TEXT,
  last_login_at TIMESTAMPTZ,
  password_changed_at TIMESTAMPTZ,
  role TEXT NOT NULL DEFAULT 'user'
    CHECK (role IN ('admin', 'user')),
  status TEXT NOT NULL DEFAULT 'active'
    CHECK (status IN ('active', 'disabled')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS users_email_lower_idx
  ON users (lower(email));

CREATE TABLE IF NOT EXISTS workspaces (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  owner_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (owner_user_id),
  UNIQUE (owner_user_id, id)
);

CREATE INDEX IF NOT EXISTS workspaces_owner_idx
  ON workspaces (owner_user_id);

CREATE TABLE IF NOT EXISTS workspace_members (
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role TEXT NOT NULL DEFAULT 'owner'
    CHECK (role IN ('owner', 'member')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, user_id)
);

CREATE INDEX IF NOT EXISTS workspace_members_user_idx
  ON workspace_members (user_id);

CREATE TABLE IF NOT EXISTS sessions (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  token_hash TEXT NOT NULL UNIQUE,
  user_agent TEXT NOT NULL DEFAULT '',
  ip_address TEXT NOT NULL DEFAULT '',
  expires_at TIMESTAMPTZ NOT NULL,
  last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  revoked_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS sessions_active_idx
  ON sessions (user_id, expires_at DESC)
  WHERE revoked_at IS NULL;

CREATE TABLE IF NOT EXISTS audit_events (
  id TEXT PRIMARY KEY,
  actor_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
  target_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
  workspace_id TEXT REFERENCES workspaces(id) ON DELETE SET NULL,
  action TEXT NOT NULL,
  metadata JSONB NOT NULL DEFAULT '{}',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS audit_events_actor_created_idx
  ON audit_events (actor_user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS audit_events_created_idx
  ON audit_events (created_at DESC);

ALTER TABLE folders ADD COLUMN IF NOT EXISTS workspace_id TEXT;
ALTER TABLE notes ADD COLUMN IF NOT EXISTS workspace_id TEXT;
ALTER TABLE task_projects ADD COLUMN IF NOT EXISTS workspace_id TEXT;
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS workspace_id TEXT;
ALTER TABLE learning_roadmaps ADD COLUMN IF NOT EXISTS workspace_id TEXT;
ALTER TABLE roadmap_nodes ADD COLUMN IF NOT EXISTS workspace_id TEXT;
ALTER TABLE roadmap_edges ADD COLUMN IF NOT EXISTS workspace_id TEXT;
ALTER TABLE roadmap_resources ADD COLUMN IF NOT EXISTS workspace_id TEXT;
ALTER TABLE events ADD COLUMN IF NOT EXISTS workspace_id TEXT;
ALTER TABLE inbox ADD COLUMN IF NOT EXISTS workspace_id TEXT;
ALTER TABLE sync_targets ADD COLUMN IF NOT EXISTS workspace_id TEXT;
ALTER TABLE note_sync_state ADD COLUMN IF NOT EXISTS workspace_id TEXT;
ALTER TABLE note_project_links ADD COLUMN IF NOT EXISTS workspace_id TEXT;
ALTER TABLE task_recurrence_rules ADD COLUMN IF NOT EXISTS workspace_id TEXT;
ALTER TABLE task_occurrences ADD COLUMN IF NOT EXISTS workspace_id TEXT;
ALTER TABLE note_sync_bindings ADD COLUMN IF NOT EXISTS workspace_id TEXT;
ALTER TABLE sync_external_claims ADD COLUMN IF NOT EXISTS workspace_id TEXT;
ALTER TABLE note_sync_suppressions ADD COLUMN IF NOT EXISTS workspace_id TEXT;
ALTER TABLE sync_import_tombstones ADD COLUMN IF NOT EXISTS workspace_id TEXT;
ALTER TABLE search_index ADD COLUMN IF NOT EXISTS workspace_id TEXT;
