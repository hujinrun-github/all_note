CREATE TABLE mobile_v2_commit_heads (
  workspace_id TEXT PRIMARY KEY REFERENCES tenant_workspaces(workspace_id) ON DELETE CASCADE,
  latest_sequence BIGINT NOT NULL DEFAULT 0 CHECK (latest_sequence >= 0),
  receipt_history_complete BOOLEAN NOT NULL DEFAULT TRUE,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE mobile_v2_command_receipts (
  workspace_id TEXT NOT NULL REFERENCES mobile_v2_commit_heads(workspace_id) ON DELETE RESTRICT,
  origin_device_client_id TEXT NOT NULL,
  command_id TEXT NOT NULL,
  request_digest TEXT NOT NULL,
  command_type TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('applied', 'no_op', 'conflict', 'rejected')),
  commit_sequence BIGINT NOT NULL CHECK (commit_sequence > 0),
  receipt_json JSONB NOT NULL CHECK (jsonb_typeof(receipt_json) = 'object'),
  completed_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (workspace_id, origin_device_client_id, command_id),
  UNIQUE (workspace_id, commit_sequence)
);

CREATE TABLE mobile_v2_change_batches (
  workspace_id TEXT NOT NULL,
  sequence BIGINT NOT NULL CHECK (sequence > 0),
  caused_by_command_id TEXT NOT NULL,
  origin_device_client_id TEXT NOT NULL,
  receipt_json JSONB NOT NULL CHECK (jsonb_typeof(receipt_json) = 'object'),
  after_images_json JSONB NOT NULL CHECK (jsonb_typeof(after_images_json) = 'array'),
  committed_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (workspace_id, sequence),
  FOREIGN KEY (workspace_id, origin_device_client_id, caused_by_command_id)
    REFERENCES mobile_v2_command_receipts(workspace_id, origin_device_client_id, command_id)
    ON DELETE RESTRICT
);

CREATE FUNCTION reject_mobile_v2_terminal_receipt_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  RAISE EXCEPTION 'mobile-v2 terminal receipts are immutable';
END;
$$;

CREATE TRIGGER mobile_v2_command_receipts_immutable
BEFORE UPDATE OR DELETE ON mobile_v2_command_receipts
FOR EACH ROW
EXECUTE PROCEDURE reject_mobile_v2_terminal_receipt_mutation();
