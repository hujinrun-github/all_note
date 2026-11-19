CREATE TABLE workspace_ai_feature_settings_next (
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  feature TEXT NOT NULL CHECK (feature IN ('roadmap_generation', 'japanese_furigana', 'plan_generation')),
  enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
  fallback_mode TEXT NOT NULL,
  updated_by TEXT NOT NULL REFERENCES users(id),
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (workspace_id, feature),
  CHECK (
    (feature = 'roadmap_generation' AND fallback_mode IN ('error', 'template'))
    OR (feature = 'japanese_furigana' AND fallback_mode IN ('error', 'local'))
    OR (feature = 'plan_generation' AND fallback_mode = 'error')
  )
);
INSERT INTO workspace_ai_feature_settings_next
  SELECT * FROM workspace_ai_feature_settings;
DROP TABLE workspace_ai_feature_settings;
ALTER TABLE workspace_ai_feature_settings_next RENAME TO workspace_ai_feature_settings;
INSERT INTO workspace_ai_feature_settings(workspace_id,feature,enabled,fallback_mode,updated_by)
  SELECT id,'plan_generation',1,'error',owner_user_id FROM workspaces;
