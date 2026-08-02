-- A schedule edited again on its first effective date still needs a durable
-- superseded version for occurrence lineage. Such a version has an empty
-- half-open effective range [date, date), which does not overlap either the
-- preceding or the replacement version.
DO $$
DECLARE
  effective_range_constraint TEXT;
BEGIN
  SELECT constraint_row.conname
  INTO effective_range_constraint
  FROM pg_constraint constraint_row
  JOIN pg_class table_row ON table_row.oid = constraint_row.conrelid
  JOIN pg_namespace namespace_row ON namespace_row.oid = table_row.relnamespace
  WHERE namespace_row.nspname = current_schema()
    AND table_row.relname = 'domain_task_schedule_versions_v2'
    AND constraint_row.contype = 'c'
    AND pg_get_constraintdef(constraint_row.oid) ILIKE '%effective_to%effective_from%'
    AND pg_get_constraintdef(constraint_row.oid) LIKE '%effective_to > effective_from%'
  LIMIT 1;

  IF effective_range_constraint IS NULL THEN
    RAISE EXCEPTION 'task schedule effective-range constraint was not found';
  END IF;

  EXECUTE format(
    'ALTER TABLE domain_task_schedule_versions_v2 DROP CONSTRAINT %I',
    effective_range_constraint
  );
END;
$$;

ALTER TABLE domain_task_schedule_versions_v2
  ADD CONSTRAINT domain_task_schedule_versions_v2_effective_range_check
  CHECK (effective_to IS NULL OR (effective_from IS NOT NULL AND effective_to >= effective_from));
