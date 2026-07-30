DROP TRIGGER domain_task_occurrences_v2_timing_insert;
DROP TRIGGER domain_task_occurrences_v2_timing_update;

CREATE TRIGGER domain_task_occurrences_v2_timing_insert
BEFORE INSERT ON domain_task_occurrences_v2
WHEN NOT (
  (
    NEW.manually_overridden = 1
    AND (
      (NEW.planned_date IS NULL AND NEW.planned_start_at IS NULL AND NEW.planned_end_at IS NULL AND NEW.all_day_end_date IS NULL)
      OR (NEW.planned_date IS NOT NULL AND NEW.planned_start_at IS NULL AND NEW.planned_end_at IS NULL)
      OR (NEW.planned_date IS NOT NULL AND NEW.planned_start_at IS NOT NULL AND NEW.planned_end_at IS NOT NULL AND NEW.all_day_end_date IS NULL)
    )
  )
  OR EXISTS (
    SELECT 1
    FROM domain_task_schedule_versions_v2 version
    WHERE version.workspace_id = NEW.workspace_id
      AND version.task_id = NEW.task_id
      AND version.schedule_revision = NEW.generated_schedule_revision
      AND (
        (version.timing_type = 'unscheduled' AND NEW.planned_date IS NULL AND NEW.planned_start_at IS NULL AND NEW.planned_end_at IS NULL AND NEW.all_day_end_date IS NULL)
        OR (version.timing_type = 'date' AND NEW.planned_date IS NOT NULL AND NEW.planned_start_at IS NULL AND NEW.planned_end_at IS NULL)
        OR (version.timing_type = 'time_block' AND NEW.planned_date IS NOT NULL AND NEW.planned_start_at IS NOT NULL AND NEW.planned_end_at IS NOT NULL AND NEW.all_day_end_date IS NULL)
      )
  )
)
BEGIN
  SELECT RAISE(ABORT, 'occurrence timing does not match schedule version');
END;

CREATE TRIGGER domain_task_occurrences_v2_timing_update
BEFORE UPDATE OF planned_date, planned_start_at, planned_end_at, all_day_end_date, generated_schedule_revision, manually_overridden
ON domain_task_occurrences_v2
WHEN NOT (
  (
    NEW.manually_overridden = 1
    AND (
      (NEW.planned_date IS NULL AND NEW.planned_start_at IS NULL AND NEW.planned_end_at IS NULL AND NEW.all_day_end_date IS NULL)
      OR (NEW.planned_date IS NOT NULL AND NEW.planned_start_at IS NULL AND NEW.planned_end_at IS NULL)
      OR (NEW.planned_date IS NOT NULL AND NEW.planned_start_at IS NOT NULL AND NEW.planned_end_at IS NOT NULL AND NEW.all_day_end_date IS NULL)
    )
  )
  OR EXISTS (
    SELECT 1
    FROM domain_task_schedule_versions_v2 version
    WHERE version.workspace_id = NEW.workspace_id
      AND version.task_id = NEW.task_id
      AND version.schedule_revision = NEW.generated_schedule_revision
      AND (
        (version.timing_type = 'unscheduled' AND NEW.planned_date IS NULL AND NEW.planned_start_at IS NULL AND NEW.planned_end_at IS NULL AND NEW.all_day_end_date IS NULL)
        OR (version.timing_type = 'date' AND NEW.planned_date IS NOT NULL AND NEW.planned_start_at IS NULL AND NEW.planned_end_at IS NULL)
        OR (version.timing_type = 'time_block' AND NEW.planned_date IS NOT NULL AND NEW.planned_start_at IS NOT NULL AND NEW.planned_end_at IS NOT NULL AND NEW.all_day_end_date IS NULL)
      )
  )
)
BEGIN
  SELECT RAISE(ABORT, 'occurrence timing does not match schedule version');
END;
