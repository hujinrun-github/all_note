-- A schedule edited again on its first effective date still needs a durable
-- superseded version for occurrence lineage. Such a version has an empty
-- half-open effective range [date, date), which does not overlap either the
-- preceding or the replacement version.
PRAGMA writable_schema = ON;

UPDATE sqlite_schema
SET sql = replace(
  sql,
  'CHECK (effective_to IS NULL OR (effective_from IS NOT NULL AND effective_to > effective_from))',
  'CHECK (effective_to IS NULL OR (effective_from IS NOT NULL AND effective_to >= effective_from))'
)
WHERE type = 'table'
  AND name = 'domain_task_schedule_versions_v2';

PRAGMA writable_schema = OFF;
