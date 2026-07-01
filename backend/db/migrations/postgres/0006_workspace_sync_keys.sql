CREATE UNIQUE INDEX IF NOT EXISTS notes_workspace_id_id_idx
  ON notes (workspace_id, id);

CREATE UNIQUE INDEX IF NOT EXISTS sync_targets_workspace_id_id_idx
  ON sync_targets (workspace_id, id);

CREATE UNIQUE INDEX IF NOT EXISTS note_sync_state_workspace_note_target_idx
  ON note_sync_state (workspace_id, note_id, target_id);

CREATE UNIQUE INDEX IF NOT EXISTS note_sync_bindings_workspace_note_idx
  ON note_sync_bindings (workspace_id, note_id);

CREATE UNIQUE INDEX IF NOT EXISTS note_sync_bindings_workspace_note_target_idx
  ON note_sync_bindings (workspace_id, note_id, target_id);

CREATE UNIQUE INDEX IF NOT EXISTS sync_external_claims_workspace_external_key_idx
  ON sync_external_claims (workspace_id, external_key);

CREATE UNIQUE INDEX IF NOT EXISTS sync_external_claims_workspace_note_idx
  ON sync_external_claims (workspace_id, note_id);

CREATE UNIQUE INDEX IF NOT EXISTS note_sync_suppressions_workspace_note_target_idx
  ON note_sync_suppressions (workspace_id, note_id, target_id);

CREATE UNIQUE INDEX IF NOT EXISTS sync_import_tombstones_workspace_external_key_idx
  ON sync_import_tombstones (workspace_id, external_key);

CREATE UNIQUE INDEX IF NOT EXISTS sync_import_tombstones_workspace_target_note_type_idx
  ON sync_import_tombstones (workspace_id, target_id, former_note_id, external_type);
