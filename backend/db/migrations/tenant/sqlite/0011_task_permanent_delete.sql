CREATE TABLE domain_task_delete_context_v2 (
  workspace_id TEXT NOT NULL,
  task_id TEXT NOT NULL,
  PRIMARY KEY (workspace_id, task_id)
);

DROP TRIGGER domain_task_execution_logs_v2_no_delete;

CREATE TRIGGER domain_task_execution_logs_v2_no_delete
BEFORE DELETE ON domain_task_execution_logs_v2
WHEN NOT EXISTS (
  SELECT 1
  FROM domain_task_occurrences_v2 occurrence
  JOIN domain_task_delete_context_v2 deletion
    ON deletion.workspace_id = occurrence.workspace_id
   AND deletion.task_id = occurrence.task_id
  WHERE occurrence.workspace_id = OLD.workspace_id
    AND occurrence.id = OLD.occurrence_id
)
BEGIN
  SELECT RAISE(ABORT, 'task execution logs are immutable');
END;
