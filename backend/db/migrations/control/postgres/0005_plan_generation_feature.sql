ALTER TABLE workspace_ai_feature_settings
  DROP CONSTRAINT workspace_ai_feature_settings_feature_check,
  DROP CONSTRAINT workspace_ai_feature_settings_check;
ALTER TABLE workspace_ai_feature_settings
  ADD CONSTRAINT workspace_ai_feature_settings_feature_check
    CHECK (feature IN ('roadmap_generation', 'japanese_furigana', 'plan_generation')),
  ADD CONSTRAINT workspace_ai_feature_settings_check CHECK (
    (feature = 'roadmap_generation' AND fallback_mode IN ('error', 'template'))
    OR (feature = 'japanese_furigana' AND fallback_mode IN ('error', 'local'))
    OR (feature = 'plan_generation' AND fallback_mode = 'error')
  );
INSERT INTO workspace_ai_feature_settings(workspace_id,feature,enabled,fallback_mode,updated_by)
  SELECT id,'plan_generation',TRUE,'error',owner_user_id FROM workspaces;
