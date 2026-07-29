CREATE TABLE mobile_v2_snapshot_sessions (
  snapshot_id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES tenant_workspaces(workspace_id) ON DELETE CASCADE,
  scope TEXT NOT NULL CHECK (
    scope IN ('iphone-content', 'iphone-task-core', 'iphone-occurrence-window', 'watch-occurrence-window')
  ),
  as_of_sequence BIGINT NOT NULL CHECK (as_of_sequence >= 0),
  contract_epoch BIGINT NOT NULL CHECK (contract_epoch > 0),
  runtime_epoch BIGINT NOT NULL CHECK (runtime_epoch > 0),
  task_model_version INTEGER NOT NULL CHECK (task_model_version >= 2),
  projection_as_of TIMESTAMPTZ NOT NULL,
  projection_time_zone TEXT,
  scope_generation TEXT NOT NULL CHECK (length(trim(scope_generation)) > 0),
  scope_valid_until TIMESTAMPTZ,
  snapshot_cursor TEXT NOT NULL CHECK (length(trim(snapshot_cursor)) > 0),
  manifest_checksum TEXT NOT NULL CHECK (manifest_checksum ~ '^sha256:[a-f0-9]{64}$'),
  page_count INTEGER NOT NULL CHECK (page_count > 0),
  expires_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (
    (scope IN ('iphone-content', 'iphone-task-core') AND projection_time_zone IS NULL AND scope_valid_until IS NULL)
    OR
    (scope IN ('iphone-occurrence-window', 'watch-occurrence-window')
      AND length(trim(COALESCE(projection_time_zone, ''))) > 0
      AND scope_valid_until IS NOT NULL)
  ),
  CHECK (expires_at > created_at)
);

CREATE INDEX mobile_v2_snapshot_sessions_expiry_idx
  ON mobile_v2_snapshot_sessions(expires_at);

CREATE TABLE mobile_v2_snapshot_pages (
  snapshot_id TEXT NOT NULL REFERENCES mobile_v2_snapshot_sessions(snapshot_id) ON DELETE CASCADE,
  page_index INTEGER NOT NULL CHECK (page_index >= 0),
  page_checksum TEXT NOT NULL CHECK (page_checksum ~ '^sha256:[a-f0-9]{64}$'),
  entities_json JSONB NOT NULL CHECK (jsonb_typeof(entities_json) = 'array'),
  PRIMARY KEY (snapshot_id, page_index)
);

CREATE TABLE mobile_v2_scope_change_batches (
  workspace_id TEXT NOT NULL REFERENCES mobile_v2_commit_heads(workspace_id) ON DELETE RESTRICT,
  scope TEXT NOT NULL CHECK (
    scope IN ('iphone-content', 'iphone-task-core', 'iphone-occurrence-window', 'watch-occurrence-window')
  ),
  sequence BIGINT NOT NULL CHECK (sequence > 0),
  caused_by_command_id TEXT,
  origin_device_client_id TEXT,
  receipt_json JSONB CHECK (receipt_json IS NULL OR jsonb_typeof(receipt_json) = 'object'),
  entities_json JSONB NOT NULL CHECK (jsonb_typeof(entities_json) = 'array'),
  committed_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (workspace_id, scope, sequence),
  FOREIGN KEY (workspace_id, origin_device_client_id, caused_by_command_id)
    REFERENCES mobile_v2_command_receipts(workspace_id, origin_device_client_id, command_id)
    ON DELETE RESTRICT,
  CHECK (
    (caused_by_command_id IS NULL AND origin_device_client_id IS NULL AND receipt_json IS NULL)
    OR
    (caused_by_command_id IS NOT NULL AND origin_device_client_id IS NOT NULL AND receipt_json IS NOT NULL)
  )
);

CREATE INDEX mobile_v2_scope_change_batches_read_idx
  ON mobile_v2_scope_change_batches(workspace_id, scope, sequence);
