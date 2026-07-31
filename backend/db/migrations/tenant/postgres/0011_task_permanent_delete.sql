CREATE TABLE domain_task_delete_context_v2 (
  workspace_id TEXT NOT NULL,
  task_id TEXT NOT NULL,
  PRIMARY KEY (workspace_id, task_id)
);

DROP TRIGGER domain_task_execution_logs_v2_no_update_or_delete ON domain_task_execution_logs_v2;
DROP FUNCTION domain_task_execution_logs_v2_reject_mutation();

CREATE FUNCTION domain_task_execution_logs_v2_reject_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
  IF TG_OP = 'DELETE' AND EXISTS (
    SELECT 1
    FROM domain_task_occurrences_v2 occurrence
    JOIN domain_task_delete_context_v2 deletion
      ON deletion.workspace_id = occurrence.workspace_id
     AND deletion.task_id = occurrence.task_id
    WHERE occurrence.workspace_id = OLD.workspace_id
      AND occurrence.id = OLD.occurrence_id
  ) THEN
    RETURN OLD;
  END IF;
  RAISE EXCEPTION 'task execution logs are immutable' USING ERRCODE = '55000';
END;
$$;

CREATE TRIGGER domain_task_execution_logs_v2_no_update_or_delete
BEFORE UPDATE OR DELETE ON domain_task_execution_logs_v2
FOR EACH ROW EXECUTE PROCEDURE domain_task_execution_logs_v2_reject_mutation();
