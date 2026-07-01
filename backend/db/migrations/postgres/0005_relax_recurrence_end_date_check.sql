DO $$
DECLARE
  constraint_name TEXT;
BEGIN
  SELECT c.conname INTO constraint_name
  FROM pg_constraint c
  JOIN pg_class t ON t.oid = c.conrelid
  JOIN pg_namespace n ON n.oid = t.relnamespace
  WHERE t.relname = 'task_recurrence_rules'
    AND n.nspname = current_schema()
    AND c.contype = 'c'
    AND pg_get_constraintdef(c.oid) LIKE '%end_date%start_date%';

  IF constraint_name IS NOT NULL THEN
    EXECUTE format('ALTER TABLE task_recurrence_rules DROP CONSTRAINT %I', constraint_name);
  END IF;
END $$;
