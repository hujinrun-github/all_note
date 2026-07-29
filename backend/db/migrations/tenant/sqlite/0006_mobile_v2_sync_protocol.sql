CREATE TABLE mobile_v2_snapshot_sessions (
  snapshot_id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  scope TEXT NOT NULL CHECK (
    scope IN ('iphone-content', 'iphone-task-core', 'iphone-occurrence-window', 'watch-occurrence-window')
  ),
  as_of_sequence INTEGER NOT NULL CHECK (as_of_sequence >= 0),
  contract_epoch INTEGER NOT NULL CHECK (contract_epoch > 0),
  runtime_epoch INTEGER NOT NULL CHECK (runtime_epoch > 0),
  task_model_version INTEGER NOT NULL CHECK (task_model_version >= 2),
  projection_as_of TEXT NOT NULL,
  projection_time_zone TEXT,
  scope_generation TEXT NOT NULL CHECK (length(trim(scope_generation)) > 0),
  scope_valid_until TEXT,
  snapshot_cursor TEXT NOT NULL CHECK (length(trim(snapshot_cursor)) > 0),
  manifest_checksum TEXT NOT NULL CHECK (
    length(manifest_checksum) = 71 AND substr(manifest_checksum, 1, 7) = 'sha256:'
  ),
  page_count INTEGER NOT NULL CHECK (page_count > 0),
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (workspace_id) REFERENCES tenant_workspaces(workspace_id) ON DELETE CASCADE,
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
  snapshot_id TEXT NOT NULL,
  page_index INTEGER NOT NULL CHECK (page_index >= 0),
  page_checksum TEXT NOT NULL CHECK (
    length(page_checksum) = 71 AND substr(page_checksum, 1, 7) = 'sha256:'
  ),
  entities_json TEXT NOT NULL CHECK (json_valid(entities_json) AND json_type(entities_json) = 'array'),
  PRIMARY KEY (snapshot_id, page_index),
  FOREIGN KEY (snapshot_id) REFERENCES mobile_v2_snapshot_sessions(snapshot_id) ON DELETE CASCADE
);

CREATE TABLE mobile_v2_scope_change_batches (
  workspace_id TEXT NOT NULL,
  scope TEXT NOT NULL CHECK (
    scope IN ('iphone-content', 'iphone-task-core', 'iphone-occurrence-window', 'watch-occurrence-window')
  ),
  sequence INTEGER NOT NULL CHECK (sequence > 0),
  caused_by_command_id TEXT,
  origin_device_client_id TEXT,
  receipt_json TEXT CHECK (
    receipt_json IS NULL OR (json_valid(receipt_json) AND json_type(receipt_json) = 'object')
  ),
  entities_json TEXT NOT NULL CHECK (json_valid(entities_json) AND json_type(entities_json) = 'array'),
  committed_at TEXT NOT NULL,
  PRIMARY KEY (workspace_id, scope, sequence),
  FOREIGN KEY (workspace_id) REFERENCES mobile_v2_commit_heads(workspace_id) ON DELETE RESTRICT,
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
