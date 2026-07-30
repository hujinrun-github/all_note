CREATE OR REPLACE FUNCTION domain_task_occurrences_v2_validate_timing()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
  schedule_timing_type TEXT;
  valid_actual_timing BOOLEAN;
BEGIN
  valid_actual_timing :=
    (NEW.planned_date IS NULL AND NEW.planned_start_at IS NULL AND NEW.planned_end_at IS NULL AND NEW.all_day_end_date IS NULL)
    OR (NEW.planned_date IS NOT NULL AND NEW.planned_start_at IS NULL AND NEW.planned_end_at IS NULL)
    OR (NEW.planned_date IS NOT NULL AND NEW.planned_start_at IS NOT NULL AND NEW.planned_end_at IS NOT NULL AND NEW.all_day_end_date IS NULL);

  IF NEW.manually_overridden THEN
    IF NOT valid_actual_timing THEN
      RAISE EXCEPTION 'manually overridden occurrence has invalid timing' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
  END IF;

  SELECT timing_type INTO schedule_timing_type
  FROM domain_task_schedule_versions_v2
  WHERE workspace_id = NEW.workspace_id
    AND task_id = NEW.task_id
    AND schedule_revision = NEW.generated_schedule_revision;

  IF schedule_timing_type IS NULL OR NOT (
    (schedule_timing_type = 'unscheduled' AND NEW.planned_date IS NULL AND NEW.planned_start_at IS NULL AND NEW.planned_end_at IS NULL AND NEW.all_day_end_date IS NULL)
    OR (schedule_timing_type = 'date' AND NEW.planned_date IS NOT NULL AND NEW.planned_start_at IS NULL AND NEW.planned_end_at IS NULL)
    OR (schedule_timing_type = 'time_block' AND NEW.planned_date IS NOT NULL AND NEW.planned_start_at IS NOT NULL AND NEW.planned_end_at IS NOT NULL AND NEW.all_day_end_date IS NULL)
  ) THEN
    RAISE EXCEPTION 'occurrence timing does not match schedule version' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END;
$$;

DROP TRIGGER domain_task_occurrences_v2_timing ON domain_task_occurrences_v2;

CREATE TRIGGER domain_task_occurrences_v2_timing
BEFORE INSERT OR UPDATE OF planned_date, planned_start_at, planned_end_at, all_day_end_date, generated_schedule_revision, manually_overridden
ON domain_task_occurrences_v2
FOR EACH ROW EXECUTE PROCEDURE domain_task_occurrences_v2_validate_timing();
