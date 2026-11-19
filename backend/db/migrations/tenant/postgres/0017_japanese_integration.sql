CREATE TABLE japanese_integration_catalogs (
 workspace_id TEXT NOT NULL REFERENCES tenant_workspaces(workspace_id) ON DELETE CASCADE,
 connection_id TEXT NOT NULL,
 catalog_revision BIGINT NOT NULL DEFAULT 0,
 catalog_hash TEXT NOT NULL DEFAULT '',
 catalog_json TEXT NOT NULL DEFAULT '',
 PRIMARY KEY(workspace_id,connection_id)
);
CREATE TABLE japanese_integration_bindings (
 workspace_id TEXT NOT NULL,
 id TEXT NOT NULL,
 connection_id TEXT NOT NULL,
 plan_id TEXT NOT NULL,
 slot_id TEXT NOT NULL,
 source_task_id TEXT NOT NULL,
 target_task_id TEXT NOT NULL,
 target_title TEXT NOT NULL,
 binding_version BIGINT NOT NULL CHECK(binding_version>0),
 effective_from TEXT NOT NULL,
 effective_to TEXT NOT NULL DEFAULT '',
 schedule_revision BIGINT NOT NULL,
 source_fingerprint TEXT NOT NULL,
 enabled INTEGER NOT NULL CHECK(enabled IN (0,1)),
 PRIMARY KEY(workspace_id,id),
 FOREIGN KEY(workspace_id,connection_id) REFERENCES japanese_integration_catalogs(workspace_id,connection_id) ON DELETE CASCADE
);
CREATE INDEX japanese_integration_binding_source_idx ON japanese_integration_bindings(workspace_id,connection_id,plan_id,slot_id,source_task_id,effective_from);
CREATE UNIQUE INDEX japanese_integration_binding_target_idx ON japanese_integration_bindings(workspace_id,target_task_id) WHERE effective_to='' AND enabled=1;
CREATE UNIQUE INDEX japanese_integration_binding_current_idx ON japanese_integration_bindings(workspace_id,connection_id,plan_id,slot_id,source_task_id) WHERE effective_to='' AND enabled=1;
CREATE TABLE japanese_integration_inbox (
 workspace_id TEXT NOT NULL,
 connection_id TEXT NOT NULL,
 event_id TEXT NOT NULL,
 payload_hash TEXT NOT NULL,
 event_json TEXT NOT NULL,
 source_key TEXT NOT NULL,
 state_revision BIGINT NOT NULL,
 binding_id TEXT NOT NULL DEFAULT '',
 receipt_json TEXT NOT NULL,
 status TEXT NOT NULL,
 retry_unix BIGINT NOT NULL DEFAULT 0,
 received_at TEXT NOT NULL,
 PRIMARY KEY(workspace_id,connection_id,event_id),
 FOREIGN KEY(workspace_id,connection_id) REFERENCES japanese_integration_catalogs(workspace_id,connection_id) ON DELETE CASCADE
);
CREATE INDEX japanese_integration_pending_idx ON japanese_integration_inbox(workspace_id,connection_id,status,retry_unix);
CREATE INDEX japanese_integration_source_idx ON japanese_integration_inbox(workspace_id,connection_id,source_key,state_revision);
CREATE TABLE japanese_integration_source_states (
 workspace_id TEXT NOT NULL,
 connection_id TEXT NOT NULL,
 source_key TEXT NOT NULL,
 state_revision BIGINT NOT NULL,
 completed INTEGER NOT NULL CHECK(completed IN (0,1)),
 occurrence_id TEXT NOT NULL DEFAULT '',
 target_task_id TEXT NOT NULL DEFAULT '',
 PRIMARY KEY(workspace_id,connection_id,source_key),
 FOREIGN KEY(workspace_id,connection_id) REFERENCES japanese_integration_catalogs(workspace_id,connection_id) ON DELETE CASCADE
);
