CREATE TABLE mobile_v2_commit_heads (
  workspace_id TEXT PRIMARY KEY,
  latest_sequence INTEGER NOT NULL DEFAULT 0 CHECK (latest_sequence >= 0),
  receipt_history_complete INTEGER NOT NULL DEFAULT 1 CHECK (receipt_history_complete IN (0, 1)),
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (workspace_id) REFERENCES tenant_workspaces(workspace_id) ON DELETE CASCADE
);

CREATE TABLE mobile_v2_command_receipts (
  workspace_id TEXT NOT NULL,
  origin_device_client_id TEXT NOT NULL,
  command_id TEXT NOT NULL,
  request_digest TEXT NOT NULL,
  command_type TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('applied', 'no_op', 'conflict', 'rejected')),
  commit_sequence INTEGER NOT NULL CHECK (commit_sequence > 0),
  receipt_json TEXT NOT NULL CHECK (json_valid(receipt_json) AND json_type(receipt_json) = 'object'),
  completed_at TEXT NOT NULL,
  PRIMARY KEY (workspace_id, origin_device_client_id, command_id),
  UNIQUE (workspace_id, commit_sequence),
  FOREIGN KEY (workspace_id) REFERENCES mobile_v2_commit_heads(workspace_id) ON DELETE RESTRICT
);

CREATE TABLE mobile_v2_change_batches (
  workspace_id TEXT NOT NULL,
  sequence INTEGER NOT NULL CHECK (sequence > 0),
  caused_by_command_id TEXT NOT NULL,
  origin_device_client_id TEXT NOT NULL,
  receipt_json TEXT NOT NULL CHECK (json_valid(receipt_json) AND json_type(receipt_json) = 'object'),
  after_images_json TEXT NOT NULL CHECK (json_valid(after_images_json) AND json_type(after_images_json) = 'array'),
  committed_at TEXT NOT NULL,
  PRIMARY KEY (workspace_id, sequence),
  FOREIGN KEY (workspace_id, origin_device_client_id, caused_by_command_id)
    REFERENCES mobile_v2_command_receipts(workspace_id, origin_device_client_id, command_id)
    ON DELETE RESTRICT
);

CREATE TRIGGER mobile_v2_command_receipts_immutable_update
BEFORE UPDATE ON mobile_v2_command_receipts
BEGIN
  SELECT RAISE(ABORT, 'mobile-v2 terminal receipts are immutable');
END;

CREATE TRIGGER mobile_v2_command_receipts_immutable_delete
BEFORE DELETE ON mobile_v2_command_receipts
BEGIN
  SELECT RAISE(ABORT, 'mobile-v2 terminal receipts are immutable');
END;
