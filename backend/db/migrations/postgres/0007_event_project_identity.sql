ALTER TABLE events
  ADD COLUMN IF NOT EXISTS project_id TEXT;

UPDATE events e
SET project_id = 'personal'
WHERE (e.project_id IS NULL OR e.project_id = '')
  AND e.kind = 'personal'
  AND EXISTS (
    SELECT 1
    FROM task_projects p
    WHERE p.workspace_id = e.workspace_id
      AND p.id = 'personal'
  );

CREATE INDEX IF NOT EXISTS events_workspace_project_start_idx
  ON events (workspace_id, project_id, start_at);
