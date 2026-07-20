CREATE TABLE codex_oauth_device_flows (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  device_ciphertext BYTEA NOT NULL,
  device_nonce BYTEA NOT NULL,
  encryption_key_id TEXT NOT NULL,
  user_code TEXT NOT NULL,
  verification_url TEXT NOT NULL,
  poll_interval_seconds INTEGER NOT NULL CHECK (poll_interval_seconds BETWEEN 3 AND 60),
  expires_at_unix BIGINT NOT NULL,
  state TEXT NOT NULL CHECK (state IN ('pending','authorized','expired','failed')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX codex_oauth_device_flows_scope_idx ON codex_oauth_device_flows(workspace_id,user_id,state);
