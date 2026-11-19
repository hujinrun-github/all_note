CREATE TABLE japanese_integration_pairings (
 code_hash TEXT PRIMARY KEY,
 workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
 actor_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 expires_unix BIGINT NOT NULL,
 connection_id TEXT NOT NULL DEFAULT ''
);
CREATE TABLE japanese_integration_connections (
 id TEXT PRIMARY KEY,
 workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
 actor_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 source_instance_id TEXT NOT NULL,
 source_user_id TEXT NOT NULL,
 source_user_name TEXT NOT NULL DEFAULT '',
 key_id TEXT NOT NULL,
 encrypted_secret TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('pending','active','revoked')),
 created_at TEXT NOT NULL
);
CREATE INDEX japanese_integration_workspace_idx ON japanese_integration_connections(workspace_id,state);
CREATE TABLE japanese_integration_nonces (
 connection_id TEXT NOT NULL REFERENCES japanese_integration_connections(id) ON DELETE CASCADE,
 nonce TEXT NOT NULL,
 expires_unix BIGINT NOT NULL,
 PRIMARY KEY(connection_id,nonce)
);
CREATE INDEX japanese_integration_nonce_expiry_idx ON japanese_integration_nonces(expires_unix);
