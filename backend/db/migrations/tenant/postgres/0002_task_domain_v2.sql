CREATE TABLE workspace_task_domain_state (
  workspace_id TEXT PRIMARY KEY REFERENCES tenant_workspaces(workspace_id) ON DELETE CASCADE,
  model_version TEXT NOT NULL DEFAULT 'v2' CHECK (model_version IN ('legacy', 'v2')),
  migration_state TEXT NOT NULL DEFAULT 'idle' CHECK (
    migration_state IN ('idle', 'backfilling', 'catching_up', 'draining', 'ready', 'cutover', 'failed')
  ),
  source_watermark BIGINT NOT NULL DEFAULT 0 CHECK (source_watermark >= 0),
  cutover_revision BIGINT CHECK (cutover_revision IS NULL OR cutover_revision >= 0),
  write_epoch BIGINT NOT NULL DEFAULT 1 CHECK (write_epoch > 0),
  accept_legacy_writes BOOLEAN NOT NULL DEFAULT FALSE,
  migration_timezone TEXT NOT NULL DEFAULT 'UTC' CHECK (length(trim(migration_timezone)) > 0),
  v2_first_write_at TIMESTAMPTZ,
  migration_id TEXT,
  last_error TEXT,
  revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (v2_first_write_at IS NULL OR (model_version = 'v2' AND migration_state IN ('idle', 'cutover'))),
  CHECK (
    (migration_state = 'idle' AND cutover_revision IS NULL AND (
      (model_version = 'legacy' AND accept_legacy_writes AND v2_first_write_at IS NULL) OR
      (model_version = 'v2' AND NOT accept_legacy_writes AND migration_id IS NULL AND source_watermark = 0)
    )) OR
    (migration_state IN ('backfilling', 'catching_up') AND model_version = 'legacy' AND accept_legacy_writes AND cutover_revision IS NULL AND length(trim(COALESCE(migration_id, ''))) > 0) OR
    (migration_state = 'draining' AND model_version = 'legacy' AND NOT accept_legacy_writes AND cutover_revision IS NOT NULL AND source_watermark <= cutover_revision AND length(trim(COALESCE(migration_id, ''))) > 0) OR
    (migration_state = 'ready' AND model_version = 'legacy' AND NOT accept_legacy_writes AND cutover_revision IS NOT NULL AND length(trim(COALESCE(migration_id, ''))) > 0 AND source_watermark >= cutover_revision) OR
    (migration_state = 'cutover' AND model_version = 'v2' AND NOT accept_legacy_writes AND cutover_revision IS NOT NULL AND length(trim(COALESCE(migration_id, ''))) > 0 AND source_watermark >= cutover_revision) OR
    (migration_state = 'failed' AND model_version = 'legacy' AND accept_legacy_writes AND length(trim(COALESCE(migration_id, ''))) > 0 AND length(trim(COALESCE(last_error, ''))) > 0)
  )
);

-- Anchors which predate this migration belong to an adopted/legacy tenant.
INSERT INTO workspace_task_domain_state(workspace_id, model_version, accept_legacy_writes)
SELECT workspace_id, 'legacy', TRUE FROM tenant_workspaces;

-- Workspaces provisioned after the v2 schema is installed start directly on v2.
CREATE FUNCTION workspace_task_domain_state_provision_v2()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
  INSERT INTO workspace_task_domain_state(workspace_id, model_version, accept_legacy_writes)
  VALUES (NEW.workspace_id, 'v2', FALSE)
  ON CONFLICT (workspace_id) DO NOTHING;
  RETURN NEW;
END;
$$;

CREATE TRIGGER workspace_task_domain_state_provision_v2
AFTER INSERT ON tenant_workspaces
FOR EACH ROW EXECUTE PROCEDURE workspace_task_domain_state_provision_v2();

CREATE TABLE domain_projects_v2 (
  workspace_id TEXT NOT NULL REFERENCES tenant_workspaces(workspace_id) ON DELETE CASCADE,
  id TEXT NOT NULL,
  name TEXT NOT NULL CHECK (length(trim(name)) > 0),
  description TEXT NOT NULL DEFAULT '',
  kind TEXT NOT NULL CHECK (kind IN ('standard', 'learning')),
  horizon TEXT NOT NULL CHECK (horizon IN ('short', 'long')),
  status TEXT NOT NULL CHECK (status IN ('planning', 'active', 'paused', 'completed', 'archived')),
  system_role TEXT CHECK (system_role IN ('inbox', 'personal')),
  target_at TIMESTAMPTZ,
  revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  archived_at TIMESTAMPTZ,
  PRIMARY KEY (workspace_id, id),
  UNIQUE (workspace_id, name)
);

CREATE UNIQUE INDEX domain_projects_v2_system_role_uidx
  ON domain_projects_v2(workspace_id, system_role)
  WHERE system_role IS NOT NULL;

CREATE FUNCTION domain_projects_v2_protect_system_project()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
  IF TG_OP = 'DELETE' AND OLD.system_role IS NOT NULL THEN
    RAISE EXCEPTION 'system project cannot be deleted' USING ERRCODE = '55000';
  END IF;
  IF TG_OP = 'UPDATE' AND OLD.system_role IS DISTINCT FROM NEW.system_role THEN
    RAISE EXCEPTION 'system project role is immutable' USING ERRCODE = '55000';
  END IF;
  IF TG_OP = 'DELETE' THEN
    RETURN OLD;
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER domain_projects_v2_system_role_immutable
BEFORE UPDATE OF system_role ON domain_projects_v2
FOR EACH ROW EXECUTE PROCEDURE domain_projects_v2_protect_system_project();

CREATE TRIGGER domain_projects_v2_system_project_no_delete
BEFORE DELETE ON domain_projects_v2
FOR EACH ROW EXECUTE PROCEDURE domain_projects_v2_protect_system_project();

CREATE TABLE domain_learning_roadmaps_v2 (
  workspace_id TEXT NOT NULL,
  id TEXT NOT NULL,
  project_id TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('draft', 'active', 'completed', 'failed', 'archived')),
  title TEXT NOT NULL DEFAULT '',
  description TEXT NOT NULL DEFAULT '',
  revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (workspace_id, id),
  UNIQUE (workspace_id, project_id),
  UNIQUE (workspace_id, project_id, id),
  FOREIGN KEY (workspace_id, project_id)
    REFERENCES domain_projects_v2(workspace_id, id)
);

CREATE TABLE domain_roadmap_nodes_v2 (
  workspace_id TEXT NOT NULL,
  id TEXT NOT NULL,
  project_id TEXT NOT NULL,
  roadmap_id TEXT NOT NULL,
  parent_id TEXT,
  title TEXT NOT NULL CHECK (length(trim(title)) > 0),
  description TEXT NOT NULL DEFAULT '',
  node_type TEXT NOT NULL CHECK (node_type IN ('stage', 'topic', 'milestone')),
  status TEXT NOT NULL CHECK (status IN ('locked', 'available', 'in_progress', 'mastered', 'skipped')),
  position DOUBLE PRECISION NOT NULL DEFAULT 0,
  legacy_metadata JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(legacy_metadata) = 'object'),
  revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (workspace_id, id),
  UNIQUE (workspace_id, project_id, id),
  UNIQUE (workspace_id, project_id, roadmap_id, id),
  FOREIGN KEY (workspace_id, project_id, roadmap_id)
    REFERENCES domain_learning_roadmaps_v2(workspace_id, project_id, id),
  FOREIGN KEY (workspace_id, project_id, roadmap_id, parent_id)
    REFERENCES domain_roadmap_nodes_v2(workspace_id, project_id, roadmap_id, id)
);

CREATE TABLE domain_roadmap_edges_v2 (
  workspace_id TEXT NOT NULL,
  id TEXT NOT NULL,
  project_id TEXT NOT NULL,
  roadmap_id TEXT NOT NULL,
  from_node_id TEXT NOT NULL,
  to_node_id TEXT NOT NULL,
  edge_type TEXT NOT NULL CHECK (edge_type IN ('prerequisite', 'related', 'suggested_order')),
  revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
  created_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (workspace_id, id),
  UNIQUE (workspace_id, roadmap_id, from_node_id, to_node_id, edge_type),
  CHECK (from_node_id <> to_node_id),
  FOREIGN KEY (workspace_id, project_id, roadmap_id)
    REFERENCES domain_learning_roadmaps_v2(workspace_id, project_id, id),
  FOREIGN KEY (workspace_id, project_id, roadmap_id, from_node_id)
    REFERENCES domain_roadmap_nodes_v2(workspace_id, project_id, roadmap_id, id),
  FOREIGN KEY (workspace_id, project_id, roadmap_id, to_node_id)
    REFERENCES domain_roadmap_nodes_v2(workspace_id, project_id, roadmap_id, id)
);

CREATE TABLE domain_tasks_v2 (
  workspace_id TEXT NOT NULL,
  id TEXT NOT NULL,
  project_id TEXT NOT NULL,
  roadmap_node_id TEXT,
  note_id TEXT,
  title TEXT NOT NULL CHECK (length(trim(title)) > 0),
  description TEXT NOT NULL DEFAULT '',
  lifecycle_status TEXT NOT NULL CHECK (
    lifecycle_status IN ('draft', 'active', 'paused', 'completed', 'cancelled', 'archived')
  ),
  priority SMALLINT NOT NULL DEFAULT 0 CHECK (priority BETWEEN 0 AND 3),
  sort_order DOUBLE PRECISION NOT NULL DEFAULT 0,
  revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  archived_at TIMESTAMPTZ,
  PRIMARY KEY (workspace_id, id),
  FOREIGN KEY (workspace_id, project_id)
    REFERENCES domain_projects_v2(workspace_id, id),
  FOREIGN KEY (workspace_id, project_id, roadmap_node_id)
    REFERENCES domain_roadmap_nodes_v2(workspace_id, project_id, id),
  FOREIGN KEY (workspace_id, note_id)
    REFERENCES notes(workspace_id, id)
);

CREATE INDEX domain_tasks_v2_project_idx ON domain_tasks_v2(workspace_id, project_id);

CREATE TABLE domain_task_dependencies_v2 (
  workspace_id TEXT NOT NULL,
  predecessor_task_id TEXT NOT NULL,
  successor_task_id TEXT NOT NULL,
  dependency_type TEXT NOT NULL CHECK (dependency_type IN ('finish_to_start', 'related', 'suggested_order')),
  created_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (workspace_id, predecessor_task_id, successor_task_id, dependency_type),
  CHECK (predecessor_task_id <> successor_task_id),
  FOREIGN KEY (workspace_id, predecessor_task_id)
    REFERENCES domain_tasks_v2(workspace_id, id) ON DELETE CASCADE,
  FOREIGN KEY (workspace_id, successor_task_id)
    REFERENCES domain_tasks_v2(workspace_id, id) ON DELETE CASCADE
);

CREATE TABLE domain_task_schedules_v2 (
  workspace_id TEXT NOT NULL,
  task_id TEXT NOT NULL,
  revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
  current_schedule_revision BIGINT NOT NULL CHECK (current_schedule_revision > 0),
  current_schedule_open_marker BOOLEAN NOT NULL DEFAULT TRUE CHECK (current_schedule_open_marker),
  generation_watermark DATE,
  generation_status TEXT NOT NULL DEFAULT 'idle' CHECK (
    generation_status IN ('idle', 'running', 'retry_pending', 'failed')
  ),
  generation_error TEXT,
  generation_retry_at TIMESTAMPTZ,
  generation_retry_pending_jobs INTEGER NOT NULL DEFAULT 0 CHECK (generation_retry_pending_jobs >= 0),
  generation_failed_jobs INTEGER NOT NULL DEFAULT 0 CHECK (generation_failed_jobs >= 0),
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (workspace_id, task_id),
  FOREIGN KEY (workspace_id, task_id)
    REFERENCES domain_tasks_v2(workspace_id, id) ON DELETE CASCADE,
  CHECK (
    (generation_status IN ('idle', 'running') AND generation_error IS NULL AND generation_retry_at IS NULL)
    OR (generation_status = 'retry_pending' AND NULLIF(trim(generation_error), '') IS NOT NULL AND generation_retry_at IS NOT NULL)
    OR (generation_status = 'failed' AND NULLIF(trim(generation_error), '') IS NOT NULL)
  )
);

CREATE TABLE domain_task_schedule_versions_v2 (
  workspace_id TEXT NOT NULL,
  task_id TEXT NOT NULL,
  schedule_revision BIGINT NOT NULL CHECK (schedule_revision > 0),
  effective_from DATE,
  effective_to DATE,
  recurrence_type TEXT NOT NULL CHECK (recurrence_type IN ('none', 'daily', 'weekly', 'monthly')),
  timing_type TEXT NOT NULL CHECK (timing_type IN ('unscheduled', 'date', 'time_block')),
  timezone TEXT NOT NULL CHECK (length(trim(timezone)) > 0),
  starts_on DATE,
  ends_on DATE,
  recurrence_rule JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(recurrence_rule) = 'object'),
  local_start_time TIME,
  duration_minutes INTEGER,
  created_at TIMESTAMPTZ NOT NULL,
  open_marker BOOLEAN DEFAULT TRUE,
  PRIMARY KEY (workspace_id, task_id, schedule_revision),
  UNIQUE (workspace_id, task_id, schedule_revision, open_marker),
  FOREIGN KEY (workspace_id, task_id)
    REFERENCES domain_task_schedules_v2(workspace_id, task_id) ON DELETE CASCADE
    DEFERRABLE INITIALLY DEFERRED,
  CHECK (effective_to IS NULL OR (effective_from IS NOT NULL AND effective_to > effective_from)),
  CHECK (
    (effective_to IS NULL AND open_marker IS TRUE)
    OR (effective_to IS NOT NULL AND open_marker IS NULL)
  ),
  CHECK (ends_on IS NULL OR (starts_on IS NOT NULL AND ends_on >= starts_on)),
  CHECK (
    (recurrence_type = 'none' AND recurrence_rule = '{}'::jsonb AND ends_on IS NULL)
    OR (recurrence_type <> 'none' AND starts_on IS NOT NULL AND effective_from IS NOT NULL)
  ),
  CHECK (
    (timing_type = 'unscheduled' AND recurrence_type = 'none' AND starts_on IS NULL AND local_start_time IS NULL AND duration_minutes IS NULL)
    OR (timing_type = 'date' AND starts_on IS NOT NULL AND local_start_time IS NULL AND duration_minutes IS NULL)
    OR (timing_type = 'time_block' AND starts_on IS NOT NULL AND local_start_time IS NOT NULL AND duration_minutes > 0)
  )
);

CREATE FUNCTION domain_task_schedule_versions_v2_set_open_marker()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
  NEW.open_marker := CASE WHEN NEW.effective_to IS NULL THEN TRUE ELSE NULL END;
  RETURN NEW;
END;
$$;

CREATE TRIGGER domain_task_schedule_versions_v2_open_marker
BEFORE INSERT OR UPDATE OF effective_to
ON domain_task_schedule_versions_v2
FOR EACH ROW EXECUTE PROCEDURE domain_task_schedule_versions_v2_set_open_marker();

ALTER TABLE domain_task_schedules_v2
  ADD CONSTRAINT domain_task_schedule_current_open_version_fk
  FOREIGN KEY (workspace_id, task_id, current_schedule_revision, current_schedule_open_marker)
  REFERENCES domain_task_schedule_versions_v2(workspace_id, task_id, schedule_revision, open_marker)
  DEFERRABLE INITIALLY DEFERRED;

CREATE UNIQUE INDEX domain_task_schedule_versions_v2_one_open_idx
  ON domain_task_schedule_versions_v2(workspace_id, task_id)
  WHERE effective_to IS NULL;

CREATE FUNCTION domain_task_schedule_versions_v2_validate_rule()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
  object_key_count INTEGER := 0;
  value_count INTEGER := 0;
  distinct_value_count INTEGER := 0;
  interval_valid BOOLEAN := FALSE;
  values_valid BOOLEAN := FALSE;
  valid_rule BOOLEAN := FALSE;
BEGIN
  IF jsonb_typeof(NEW.recurrence_rule) <> 'object' THEN
    RAISE EXCEPTION 'invalid task recurrence rule' USING ERRCODE = '23514';
  END IF;

  SELECT COUNT(*) INTO object_key_count
  FROM jsonb_object_keys(NEW.recurrence_rule);

  interval_valid := COALESCE(
    jsonb_typeof(NEW.recurrence_rule -> 'interval') = 'number'
    AND (NEW.recurrence_rule ->> 'interval') ~ '^[1-9][0-9]*$',
    FALSE
  );

  IF NEW.recurrence_type = 'none' THEN
    valid_rule := object_key_count = 0;
  ELSIF NEW.recurrence_type = 'daily' THEN
    valid_rule := interval_valid AND object_key_count = 1;
  ELSIF NEW.recurrence_type = 'weekly' THEN
    IF jsonb_typeof(NEW.recurrence_rule -> 'weekdays') = 'array' THEN
      SELECT
        COUNT(*),
        COUNT(DISTINCT item::text),
        COALESCE(BOOL_AND(jsonb_typeof(item) = 'number' AND item::text ~ '^[0-6]$'), FALSE)
      INTO value_count, distinct_value_count, values_valid
      FROM jsonb_array_elements(NEW.recurrence_rule -> 'weekdays') item;
    END IF;
    valid_rule := interval_valid
      AND object_key_count = 2
      AND value_count > 0
      AND distinct_value_count = value_count
      AND values_valid;
  ELSIF NEW.recurrence_type = 'monthly' THEN
    IF jsonb_typeof(NEW.recurrence_rule -> 'month_days') = 'array' THEN
      SELECT
        COUNT(*),
        COUNT(DISTINCT item::text),
        COALESCE(BOOL_AND(
          jsonb_typeof(item) = 'number'
          AND item::text ~ '^([1-9]|[12][0-9]|3[01])$'
        ), FALSE)
      INTO value_count, distinct_value_count, values_valid
      FROM jsonb_array_elements(NEW.recurrence_rule -> 'month_days') item;
    END IF;
    valid_rule := interval_valid
      AND object_key_count = 2
      AND value_count > 0
      AND distinct_value_count = value_count
      AND values_valid;
  END IF;

  IF NOT valid_rule THEN
    RAISE EXCEPTION 'invalid task recurrence rule' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER domain_task_schedule_versions_v2_rule
BEFORE INSERT OR UPDATE OF recurrence_type, recurrence_rule
ON domain_task_schedule_versions_v2
FOR EACH ROW EXECUTE PROCEDURE domain_task_schedule_versions_v2_validate_rule();

CREATE FUNCTION domain_task_schedule_versions_v2_reject_overlap()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
  IF EXISTS (
    SELECT 1
    FROM domain_task_schedule_versions_v2 existing
    WHERE existing.workspace_id = NEW.workspace_id
      AND existing.task_id = NEW.task_id
      AND (TG_OP = 'INSERT' OR existing.schedule_revision <> OLD.schedule_revision)
      AND (existing.effective_to IS NULL OR NEW.effective_from IS NULL OR existing.effective_to > NEW.effective_from)
      AND (NEW.effective_to IS NULL OR existing.effective_from IS NULL OR NEW.effective_to > existing.effective_from)
  ) THEN
    RAISE EXCEPTION 'task schedule effective ranges overlap' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER domain_task_schedule_versions_v2_no_overlap
BEFORE INSERT OR UPDATE OF effective_from, effective_to
ON domain_task_schedule_versions_v2
FOR EACH ROW EXECUTE PROCEDURE domain_task_schedule_versions_v2_reject_overlap();

CREATE TABLE domain_task_occurrences_v2 (
  workspace_id TEXT NOT NULL,
  id TEXT NOT NULL,
  task_id TEXT NOT NULL,
  occurrence_key TEXT NOT NULL,
  planned_date DATE,
  planned_start_at TIMESTAMPTZ,
  planned_end_at TIMESTAMPTZ,
  due_at TIMESTAMPTZ,
  execution_status TEXT NOT NULL CHECK (
    execution_status IN ('open', 'active', 'blocked', 'done', 'skipped', 'cancelled')
  ),
  actual_start_at TIMESTAMPTZ,
  completed_at TIMESTAMPTZ,
  override_title TEXT,
  override_description TEXT,
  location TEXT,
  calendar_kind TEXT,
  calendar_notes TEXT,
  note_id TEXT,
  all_day_end_date DATE,
  blocked_reason TEXT,
  next_action TEXT,
  revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
  generated_schedule_revision BIGINT NOT NULL,
  manually_overridden BOOLEAN NOT NULL DEFAULT FALSE,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (workspace_id, id),
  UNIQUE (workspace_id, task_id, occurrence_key),
  FOREIGN KEY (workspace_id, task_id)
    REFERENCES domain_tasks_v2(workspace_id, id) ON DELETE CASCADE,
  FOREIGN KEY (workspace_id, task_id, generated_schedule_revision)
    REFERENCES domain_task_schedule_versions_v2(workspace_id, task_id, schedule_revision),
  FOREIGN KEY (workspace_id, note_id)
    REFERENCES notes(workspace_id, id),
  CHECK ((planned_start_at IS NULL) = (planned_end_at IS NULL)),
  CHECK (planned_end_at IS NULL OR planned_end_at > planned_start_at),
  CHECK (all_day_end_date IS NULL OR (planned_date IS NOT NULL AND all_day_end_date > planned_date)),
  CHECK ((execution_status = 'done') = (completed_at IS NOT NULL)),
  CHECK (
    (execution_status = 'blocked' AND NULLIF(trim(blocked_reason), '') IS NOT NULL AND NULLIF(trim(next_action), '') IS NOT NULL)
    OR (execution_status <> 'blocked' AND blocked_reason IS NULL AND next_action IS NULL)
  )
);

CREATE INDEX domain_task_occurrences_v2_status_date_idx
  ON domain_task_occurrences_v2(workspace_id, execution_status, planned_date);
CREATE INDEX domain_task_occurrences_v2_start_idx
  ON domain_task_occurrences_v2(workspace_id, planned_start_at);
CREATE INDEX domain_task_occurrences_v2_date_idx
  ON domain_task_occurrences_v2(workspace_id, planned_date);
CREATE INDEX domain_task_occurrences_v2_due_open_idx
  ON domain_task_occurrences_v2(workspace_id, due_at)
  WHERE execution_status NOT IN ('done', 'skipped', 'cancelled');
CREATE INDEX domain_task_occurrences_v2_completed_idx
  ON domain_task_occurrences_v2(workspace_id, completed_at)
  WHERE execution_status = 'done';

CREATE FUNCTION domain_task_occurrences_v2_validate_timing()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
  schedule_timing_type TEXT;
BEGIN
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

CREATE TRIGGER domain_task_occurrences_v2_timing
BEFORE INSERT OR UPDATE OF planned_date, planned_start_at, planned_end_at, all_day_end_date, generated_schedule_revision
ON domain_task_occurrences_v2
FOR EACH ROW EXECUTE PROCEDURE domain_task_occurrences_v2_validate_timing();

CREATE TABLE domain_task_execution_logs_v2 (
  workspace_id TEXT NOT NULL,
  id TEXT NOT NULL,
  occurrence_id TEXT NOT NULL,
  from_status TEXT CHECK (from_status IS NULL OR from_status IN ('open', 'active', 'blocked', 'done', 'skipped', 'cancelled')),
  to_status TEXT NOT NULL CHECK (to_status IN ('open', 'active', 'blocked', 'done', 'skipped', 'cancelled')),
  blocked_reason TEXT,
  next_action TEXT,
  actor_id TEXT NOT NULL,
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata) = 'object'),
  created_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (workspace_id, id),
  FOREIGN KEY (workspace_id, occurrence_id)
    REFERENCES domain_task_occurrences_v2(workspace_id, id),
  CHECK (
    (to_status = 'blocked' AND NULLIF(trim(blocked_reason), '') IS NOT NULL AND NULLIF(trim(next_action), '') IS NOT NULL)
    OR (to_status <> 'blocked' AND blocked_reason IS NULL AND next_action IS NULL)
  )
);

CREATE FUNCTION domain_task_execution_logs_v2_reject_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
  RAISE EXCEPTION 'task execution logs are immutable' USING ERRCODE = '55000';
END;
$$;

CREATE TRIGGER domain_task_execution_logs_v2_no_update_or_delete
BEFORE UPDATE OR DELETE ON domain_task_execution_logs_v2
FOR EACH ROW EXECUTE PROCEDURE domain_task_execution_logs_v2_reject_mutation();
