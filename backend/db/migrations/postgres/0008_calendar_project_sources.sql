CREATE UNIQUE INDEX IF NOT EXISTS task_projects_workspace_id_id_idx
  ON task_projects (workspace_id, id);

CREATE TABLE IF NOT EXISTS calendar_project_sources (
  workspace_id TEXT NOT NULL,
  user_id TEXT NOT NULL,
  project_id TEXT NOT NULL,
  enabled BOOLEAN NOT NULL DEFAULT true,
  color TEXT NOT NULL DEFAULT '',
  order_index INTEGER NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, user_id, project_id),
  FOREIGN KEY (workspace_id, user_id)
    REFERENCES workspace_members(workspace_id, user_id)
    ON DELETE CASCADE
    DEFERRABLE INITIALLY DEFERRED,
  FOREIGN KEY (workspace_id, project_id)
    REFERENCES task_projects(workspace_id, id)
    ON DELETE CASCADE
    DEFERRABLE INITIALLY DEFERRED
);

CREATE INDEX IF NOT EXISTS calendar_project_sources_user_enabled_idx
  ON calendar_project_sources (workspace_id, user_id, enabled, order_index);

CREATE INDEX IF NOT EXISTS calendar_project_sources_project_idx
  ON calendar_project_sources (workspace_id, project_id);
