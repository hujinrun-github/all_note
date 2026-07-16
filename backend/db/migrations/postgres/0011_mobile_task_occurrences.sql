ALTER TABLE task_occurrences
  ADD COLUMN IF NOT EXISTS occurrence_id TEXT,
  ADD COLUMN IF NOT EXISTS revision BIGINT NOT NULL DEFAULT 1,
  ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;

WITH pending AS (
  SELECT
    task_id,
    occurrence_date,
    workspace_id,
    md5('flowspace:task_occurrence:' || workspace_id || ':' || task_id || ':' || occurrence_date::text) AS digest
  FROM task_occurrences
  WHERE occurrence_id IS NULL
)
UPDATE task_occurrences AS occurrence
SET occurrence_id =
  substr(pending.digest, 1, 8) || '-' ||
  substr(pending.digest, 9, 4) || '-' ||
  '3' || substr(pending.digest, 14, 3) || '-' ||
  '8' || substr(pending.digest, 18, 3) || '-' ||
  substr(pending.digest, 21, 12)
FROM pending
WHERE occurrence.task_id = pending.task_id
  AND occurrence.occurrence_date = pending.occurrence_date
  AND occurrence.workspace_id = pending.workspace_id
  AND occurrence.occurrence_id IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS task_occurrences_workspace_occurrence_id_idx
  ON task_occurrences (workspace_id, occurrence_id)
  WHERE occurrence_id IS NOT NULL;
