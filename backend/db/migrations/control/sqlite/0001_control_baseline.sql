CREATE TABLE users (
  id TEXT PRIMARY KEY,
  email TEXT NOT NULL,
  display_name TEXT NOT NULL DEFAULT '',
  password_hash TEXT NOT NULL,
  password_set INTEGER NOT NULL DEFAULT 1 CHECK (password_set IN (0, 1)),
  must_change_password INTEGER NOT NULL DEFAULT 0 CHECK (must_change_password IN (0, 1)),
  default_workspace_id TEXT,
  last_login_at TEXT,
  password_changed_at TEXT,
  role TEXT NOT NULL DEFAULT 'user' CHECK (role IN ('admin', 'user')),
  status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX users_email_lower_idx ON users(lower(email));

CREATE TABLE workspaces (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  owner_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE (owner_user_id),
  UNIQUE (owner_user_id, id)
);

CREATE TABLE workspace_members (
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role TEXT NOT NULL DEFAULT 'owner' CHECK (role IN ('owner', 'member')),
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (workspace_id, user_id)
);

CREATE INDEX workspace_members_user_idx ON workspace_members(user_id);

CREATE TABLE sessions (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  token_hash TEXT NOT NULL UNIQUE,
  user_agent TEXT NOT NULL DEFAULT '',
  ip_address TEXT NOT NULL DEFAULT '',
  expires_at TEXT NOT NULL,
  last_seen_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  revoked_at TEXT,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX sessions_active_idx ON sessions(user_id, expires_at DESC) WHERE revoked_at IS NULL;

CREATE TABLE auth_identities (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  provider TEXT NOT NULL,
  provider_user_id TEXT NOT NULL,
  provider_login TEXT NOT NULL,
  email TEXT NOT NULL,
  avatar_url TEXT,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  last_login_at TEXT,
  UNIQUE (provider, provider_user_id)
);

CREATE INDEX auth_identities_user_idx ON auth_identities(user_id);

CREATE TABLE audit_events (
  id TEXT PRIMARY KEY,
  actor_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
  target_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
  workspace_id TEXT REFERENCES workspaces(id) ON DELETE SET NULL,
  action TEXT NOT NULL,
  metadata TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE user_profiles (
  user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  locale TEXT NOT NULL DEFAULT 'zh-CN',
  time_zone TEXT NOT NULL DEFAULT 'Asia/Shanghai',
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE user_avatar_blobs (
  user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  mime_type TEXT NOT NULL CHECK (mime_type IN ('image/jpeg', 'image/png', 'image/webp')),
  size_bytes INTEGER NOT NULL CHECK (size_bytes BETWEEN 1 AND 2097152),
  sha256 TEXT NOT NULL,
  width INTEGER NOT NULL CHECK (width > 0),
  height INTEGER NOT NULL CHECK (height > 0),
  content BLOB NOT NULL,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE system_profile_families (
  id TEXT NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('data_store', 'object_s3', 'llm_chat', 'llm_transcription')),
  name TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (kind, id)
);

CREATE TABLE system_profile_versions (
  id TEXT NOT NULL,
  family_id TEXT NOT NULL,
  kind TEXT NOT NULL,
  version INTEGER NOT NULL CHECK (version > 0),
  provider TEXT NOT NULL,
  state TEXT NOT NULL CHECK (state IN ('draft', 'verified', 'retired')),
  config_json TEXT NOT NULL DEFAULT '{}',
  secret_ciphertext BLOB,
  secret_nonce BLOB,
  encryption_key_id TEXT,
  config_fingerprint TEXT,
  verified_at TEXT,
  last_check_status TEXT,
  last_check_message TEXT,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (kind, id),
  UNIQUE (kind, family_id, version),
  FOREIGN KEY (kind, family_id) REFERENCES system_profile_families(kind, id)
);

CREATE TABLE workspace_profile_families (
  id TEXT NOT NULL,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  kind TEXT NOT NULL CHECK (kind IN ('data_store', 'object_s3', 'llm_chat', 'llm_transcription')),
  name TEXT NOT NULL,
  created_by TEXT NOT NULL REFERENCES users(id),
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (workspace_id, kind, id)
);

CREATE TABLE workspace_profile_versions (
  id TEXT NOT NULL,
  family_id TEXT NOT NULL,
  workspace_id TEXT NOT NULL,
  kind TEXT NOT NULL,
  version INTEGER NOT NULL CHECK (version > 0),
  provider TEXT NOT NULL,
  state TEXT NOT NULL CHECK (state IN ('draft', 'verified', 'retired')),
  config_json TEXT NOT NULL DEFAULT '{}',
  secret_ciphertext BLOB,
  secret_nonce BLOB,
  encryption_key_id TEXT,
  verified_at TEXT,
  last_check_status TEXT,
  last_check_message TEXT,
  created_by TEXT NOT NULL REFERENCES users(id),
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (workspace_id, kind, id),
  UNIQUE (workspace_id, kind, family_id, version),
  FOREIGN KEY (workspace_id, kind, family_id)
    REFERENCES workspace_profile_families(workspace_id, kind, id)
);

CREATE TABLE workspace_service_endpoints (
  id TEXT NOT NULL,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  kind TEXT NOT NULL CHECK (kind IN ('data_store', 'object_s3', 'llm_chat', 'llm_transcription')),
  source_type TEXT NOT NULL CHECK (source_type IN ('system', 'custom')),
  system_profile_version_id TEXT,
  workspace_profile_version_id TEXT,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (workspace_id, kind, source_type, id),
  CHECK (
    (source_type = 'system' AND system_profile_version_id IS NOT NULL AND workspace_profile_version_id IS NULL)
    OR
    (source_type = 'custom' AND system_profile_version_id IS NULL AND workspace_profile_version_id IS NOT NULL)
  ),
  FOREIGN KEY (kind, system_profile_version_id) REFERENCES system_profile_versions(kind, id),
  FOREIGN KEY (workspace_id, kind, workspace_profile_version_id)
    REFERENCES workspace_profile_versions(workspace_id, kind, id)
);

CREATE TABLE workspace_service_bindings (
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  kind TEXT NOT NULL CHECK (kind IN ('data_store', 'object_s3', 'llm_chat', 'llm_transcription')),
  mode TEXT NOT NULL,
  endpoint_source_type TEXT,
  endpoint_id TEXT,
  settings_json TEXT NOT NULL DEFAULT '{}',
  revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
  updated_by TEXT NOT NULL REFERENCES users(id),
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (workspace_id, kind),
  FOREIGN KEY (workspace_id, kind, endpoint_source_type, endpoint_id)
    REFERENCES workspace_service_endpoints(workspace_id, kind, source_type, id),
  CHECK (
    (kind IN ('data_store', 'object_s3') AND mode IN ('default', 'custom')
      AND endpoint_source_type IS NOT NULL AND endpoint_id IS NOT NULL)
    OR
    (kind = 'llm_chat' AND (
      (mode IN ('default', 'custom') AND endpoint_source_type IS NOT NULL AND endpoint_id IS NOT NULL)
      OR (mode = 'disabled' AND endpoint_source_type IS NULL AND endpoint_id IS NULL)
    ))
    OR
    (kind = 'llm_transcription' AND (
      (mode IN ('default', 'custom') AND endpoint_source_type IS NOT NULL AND endpoint_id IS NOT NULL)
      OR (mode IN ('reuse_chat', 'disabled') AND endpoint_source_type IS NULL AND endpoint_id IS NULL)
    ))
  ),
  CHECK (
    endpoint_source_type IS NULL
    OR (mode = 'default' AND endpoint_source_type = 'system')
    OR (mode = 'custom' AND endpoint_source_type = 'custom')
  )
);

CREATE TABLE workspace_ai_feature_settings (
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  feature TEXT NOT NULL CHECK (feature IN ('roadmap_generation', 'japanese_furigana')),
  enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
  fallback_mode TEXT NOT NULL,
  updated_by TEXT NOT NULL REFERENCES users(id),
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (workspace_id, feature),
  CHECK (
    (feature = 'roadmap_generation' AND fallback_mode IN ('error', 'template'))
    OR (feature = 'japanese_furigana' AND fallback_mode IN ('error', 'local'))
  )
);

CREATE TABLE storage_transition_jobs (
  id TEXT NOT NULL,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  operation_kind TEXT NOT NULL CHECK (operation_kind IN ('migration', 'rebind')),
  source_kind TEXT NOT NULL DEFAULT 'data_store' CHECK (source_kind = 'data_store'),
  source_endpoint_type TEXT NOT NULL CHECK (source_endpoint_type IN ('system', 'custom')),
  source_endpoint_id TEXT NOT NULL,
  source_provider TEXT NOT NULL CHECK (source_provider IN ('postgres', 'sqlite')),
  target_kind TEXT NOT NULL DEFAULT 'data_store' CHECK (target_kind = 'data_store'),
  target_endpoint_type TEXT NOT NULL CHECK (target_endpoint_type IN ('system', 'custom')),
  target_endpoint_id TEXT NOT NULL,
  target_provider TEXT NOT NULL CHECK (target_provider IN ('postgres', 'sqlite')),
  source_installation_id TEXT NOT NULL,
  source_database_identity TEXT NOT NULL,
  source_schema_identity TEXT NOT NULL,
  target_installation_id TEXT NOT NULL,
  target_database_identity TEXT NOT NULL,
  target_schema_identity TEXT NOT NULL,
  target_existing_policy TEXT NOT NULL DEFAULT 'reject' CHECK (target_existing_policy IN ('reject', 'replace_retired')),
  caused_by_migration_id TEXT,
  source_binding_revision INTEGER NOT NULL,
  source_runtime_revision INTEGER NOT NULL,
  migration_epoch INTEGER NOT NULL,
  state TEXT NOT NULL CHECK (state IN ('pending', 'preflight', 'draining', 'copying', 'verifying', 'activating', 'completed', 'failed', 'cancelled')),
  progress_json TEXT NOT NULL DEFAULT '{}',
  verification_json TEXT NOT NULL DEFAULT '{}',
  error_code TEXT,
  error_message TEXT,
  started_at TEXT,
  completed_at TEXT,
  created_by TEXT NOT NULL REFERENCES users(id),
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (workspace_id, id),
  FOREIGN KEY (workspace_id, source_kind, source_endpoint_type, source_endpoint_id)
    REFERENCES workspace_service_endpoints(workspace_id, kind, source_type, id),
  FOREIGN KEY (workspace_id, target_kind, target_endpoint_type, target_endpoint_id)
    REFERENCES workspace_service_endpoints(workspace_id, kind, source_type, id),
  FOREIGN KEY (workspace_id, caused_by_migration_id)
    REFERENCES storage_transition_jobs(workspace_id, id),
  CHECK (source_endpoint_type <> target_endpoint_type OR source_endpoint_id <> target_endpoint_id),
  CHECK (
    (operation_kind = 'migration' AND (
      source_provider <> target_provider
      OR source_installation_id <> target_installation_id
      OR source_schema_identity <> target_schema_identity
    ))
    OR
    (operation_kind = 'rebind'
      AND source_provider = target_provider
      AND source_installation_id = target_installation_id
      AND source_schema_identity = target_schema_identity
      AND target_existing_policy = 'reject')
  )
);

CREATE UNIQUE INDEX storage_transition_one_active_workspace_idx
  ON storage_transition_jobs(workspace_id)
  WHERE state IN ('pending', 'preflight', 'draining', 'copying', 'verifying', 'activating');

CREATE TABLE workspace_runtime_state (
  workspace_id TEXT PRIMARY KEY REFERENCES workspaces(id) ON DELETE CASCADE,
  mode TEXT NOT NULL CHECK (mode IN ('active', 'draining', 'migrating', 'activating', 'blocked')),
  epoch INTEGER NOT NULL DEFAULT 1 CHECK (epoch > 0),
  binding_revision INTEGER NOT NULL DEFAULT 1 CHECK (binding_revision > 0),
  storage_operation_kind TEXT CHECK (storage_operation_kind IN ('migration', 'rebind')),
  storage_operation_id TEXT,
  updated_by TEXT NOT NULL REFERENCES users(id),
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  CHECK ((storage_operation_kind IS NULL) = (storage_operation_id IS NULL)),
  CHECK (
    (mode = 'active' AND storage_operation_id IS NULL)
    OR (mode IN ('draining', 'migrating', 'activating') AND storage_operation_id IS NOT NULL)
    OR mode = 'blocked'
  ),
  FOREIGN KEY (workspace_id, storage_operation_id)
    REFERENCES storage_transition_jobs(workspace_id, id)
);
