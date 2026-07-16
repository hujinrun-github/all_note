CREATE TABLE IF NOT EXISTS mobile_sync_conflicts (
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  conflict_id TEXT NOT NULL,
  mutation_id TEXT NOT NULL,
  device_client_id TEXT NOT NULL,
  request_sha256 TEXT NOT NULL,
  entity_type TEXT NOT NULL,
  entity_client_id TEXT NOT NULL,
  operation TEXT NOT NULL,
  base_revision BIGINT NOT NULL,
  remote_revision BIGINT NOT NULL,
  local_payload JSONB NOT NULL,
  remote_payload JSONB NOT NULL,
  revision BIGINT NOT NULL DEFAULT 1,
  resolution TEXT,
  resolved_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, conflict_id),
  UNIQUE (workspace_id, device_client_id, mutation_id)
);

CREATE INDEX IF NOT EXISTS mobile_sync_conflicts_unresolved_idx
  ON mobile_sync_conflicts (workspace_id, created_at, conflict_id)
  WHERE resolved_at IS NULL;
