CREATE TABLE workspace_task_domain_state (
  workspace_id TEXT PRIMARY KEY REFERENCES tenant_workspaces(workspace_id) ON DELETE CASCADE,
  model_version TEXT NOT NULL DEFAULT 'v2' CHECK (model_version IN ('legacy', 'v2')),
  migration_state TEXT NOT NULL DEFAULT 'idle' CHECK (
    migration_state IN ('idle', 'backfilling', 'catching_up', 'draining', 'ready', 'cutover', 'failed')
  ),
  source_watermark INTEGER NOT NULL DEFAULT 0 CHECK (source_watermark >= 0),
  cutover_revision INTEGER CHECK (cutover_revision IS NULL OR cutover_revision >= 0),
  write_epoch INTEGER NOT NULL DEFAULT 1 CHECK (write_epoch > 0),
  accept_legacy_writes INTEGER NOT NULL DEFAULT 0 CHECK (accept_legacy_writes IN (0, 1)),
  migration_timezone TEXT NOT NULL DEFAULT 'UTC' CHECK (length(trim(migration_timezone)) > 0),
  v2_first_write_at TEXT,
  migration_id TEXT,
  last_error TEXT,
  revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  CHECK (v2_first_write_at IS NULL OR (model_version = 'v2' AND migration_state IN ('idle', 'cutover'))),
  CHECK (
    (migration_state = 'idle' AND cutover_revision IS NULL AND (
      (model_version = 'legacy' AND accept_legacy_writes = 1 AND v2_first_write_at IS NULL) OR
      (model_version = 'v2' AND accept_legacy_writes = 0 AND migration_id IS NULL AND source_watermark = 0)
    )) OR
    (migration_state IN ('backfilling', 'catching_up') AND model_version = 'legacy' AND accept_legacy_writes = 1 AND cutover_revision IS NULL AND length(trim(COALESCE(migration_id, ''))) > 0) OR
    (migration_state = 'draining' AND model_version = 'legacy' AND accept_legacy_writes = 0 AND cutover_revision IS NOT NULL AND source_watermark <= cutover_revision AND length(trim(COALESCE(migration_id, ''))) > 0) OR
    (migration_state = 'ready' AND model_version = 'legacy' AND accept_legacy_writes = 0 AND cutover_revision IS NOT NULL AND length(trim(COALESCE(migration_id, ''))) > 0 AND source_watermark >= cutover_revision) OR
    (migration_state = 'cutover' AND model_version = 'v2' AND accept_legacy_writes = 0 AND cutover_revision IS NOT NULL AND length(trim(COALESCE(migration_id, ''))) > 0 AND source_watermark >= cutover_revision) OR
    (migration_state = 'failed' AND model_version = 'legacy' AND accept_legacy_writes = 1 AND length(trim(COALESCE(migration_id, ''))) > 0 AND length(trim(COALESCE(last_error, ''))) > 0)
  )
);

-- Anchors which predate this migration belong to an adopted/legacy tenant.
INSERT INTO workspace_task_domain_state(workspace_id, model_version, accept_legacy_writes)
SELECT workspace_id, 'legacy', 1 FROM tenant_workspaces;

-- Workspaces provisioned after the v2 schema is installed start directly on v2.
CREATE TRIGGER workspace_task_domain_state_provision_v2
AFTER INSERT ON tenant_workspaces
BEGIN
  INSERT OR IGNORE INTO workspace_task_domain_state(workspace_id, model_version, accept_legacy_writes)
  VALUES (NEW.workspace_id, 'v2', 0);
END;

CREATE TABLE domain_projects_v2 (
  workspace_id TEXT NOT NULL REFERENCES tenant_workspaces(workspace_id) ON DELETE CASCADE,
  id TEXT NOT NULL,
  name TEXT NOT NULL CHECK (length(trim(name)) > 0),
  description TEXT NOT NULL DEFAULT '',
  kind TEXT NOT NULL CHECK (kind IN ('standard', 'learning')),
  horizon TEXT NOT NULL CHECK (horizon IN ('short', 'long')),
  status TEXT NOT NULL CHECK (status IN ('planning', 'active', 'paused', 'completed', 'archived')),
  system_role TEXT CHECK (system_role IN ('inbox', 'personal')),
  target_at TEXT,
  revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  archived_at TEXT,
  PRIMARY KEY (workspace_id, id),
  UNIQUE (workspace_id, name)
);

CREATE UNIQUE INDEX domain_projects_v2_system_role_uidx
  ON domain_projects_v2(workspace_id, system_role)
  WHERE system_role IS NOT NULL;

CREATE TRIGGER domain_projects_v2_system_role_immutable
BEFORE UPDATE OF system_role ON domain_projects_v2
WHEN NOT (OLD.system_role IS NEW.system_role)
BEGIN
  SELECT RAISE(ABORT, 'system project role is immutable');
END;

CREATE TRIGGER domain_projects_v2_system_project_no_delete
BEFORE DELETE ON domain_projects_v2
WHEN OLD.system_role IS NOT NULL
BEGIN
  SELECT RAISE(ABORT, 'system project cannot be deleted');
END;

CREATE TABLE domain_learning_roadmaps_v2 (
  workspace_id TEXT NOT NULL,
  id TEXT NOT NULL,
  project_id TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('draft', 'active', 'completed', 'failed', 'archived')),
  title TEXT NOT NULL DEFAULT '',
  description TEXT NOT NULL DEFAULT '',
  revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
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
  position REAL NOT NULL DEFAULT 0,
  legacy_metadata TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(legacy_metadata) AND json_type(legacy_metadata) = 'object'),
  revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
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
  revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
  created_at TEXT NOT NULL,
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
  priority INTEGER NOT NULL DEFAULT 0 CHECK (priority BETWEEN 0 AND 3),
  sort_order REAL NOT NULL DEFAULT 0,
  revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  archived_at TEXT,
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
  created_at TEXT NOT NULL,
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
  revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
  current_schedule_revision INTEGER NOT NULL CHECK (current_schedule_revision > 0),
  current_schedule_open_marker INTEGER NOT NULL DEFAULT 1 CHECK (current_schedule_open_marker = 1),
  generation_watermark TEXT,
  generation_status TEXT NOT NULL DEFAULT 'idle' CHECK (
    generation_status IN ('idle', 'running', 'retry_pending', 'failed')
  ),
  generation_error TEXT,
  generation_retry_at TEXT,
  generation_retry_pending_jobs INTEGER NOT NULL DEFAULT 0 CHECK (generation_retry_pending_jobs >= 0),
  generation_failed_jobs INTEGER NOT NULL DEFAULT 0 CHECK (generation_failed_jobs >= 0),
  updated_at TEXT NOT NULL,
  PRIMARY KEY (workspace_id, task_id),
  FOREIGN KEY (workspace_id, task_id)
    REFERENCES domain_tasks_v2(workspace_id, id) ON DELETE CASCADE,
  FOREIGN KEY (workspace_id, task_id, current_schedule_revision, current_schedule_open_marker)
    REFERENCES domain_task_schedule_versions_v2(workspace_id, task_id, schedule_revision, open_marker)
    DEFERRABLE INITIALLY DEFERRED,
  CHECK (
    (generation_status IN ('idle', 'running') AND generation_error IS NULL AND generation_retry_at IS NULL)
    OR (generation_status = 'retry_pending' AND length(trim(generation_error)) > 0 AND generation_retry_at IS NOT NULL)
    OR (generation_status = 'failed' AND length(trim(generation_error)) > 0)
  )
);

CREATE TABLE domain_task_schedule_versions_v2 (
  workspace_id TEXT NOT NULL,
  task_id TEXT NOT NULL,
  schedule_revision INTEGER NOT NULL CHECK (schedule_revision > 0),
  effective_from TEXT,
  effective_to TEXT,
  recurrence_type TEXT NOT NULL CHECK (recurrence_type IN ('none', 'daily', 'weekly', 'monthly')),
  timing_type TEXT NOT NULL CHECK (timing_type IN ('unscheduled', 'date', 'time_block')),
  timezone TEXT NOT NULL CHECK (length(trim(timezone)) > 0),
  starts_on TEXT,
  ends_on TEXT,
  recurrence_rule TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(recurrence_rule) AND json_type(recurrence_rule) = 'object'),
  local_start_time TEXT,
  duration_minutes INTEGER,
  created_at TEXT NOT NULL,
  open_marker INTEGER GENERATED ALWAYS AS (CASE WHEN effective_to IS NULL THEN 1 ELSE NULL END) STORED,
  PRIMARY KEY (workspace_id, task_id, schedule_revision),
  UNIQUE (workspace_id, task_id, schedule_revision, open_marker),
  FOREIGN KEY (workspace_id, task_id)
    REFERENCES domain_task_schedules_v2(workspace_id, task_id) ON DELETE CASCADE
    DEFERRABLE INITIALLY DEFERRED,
  CHECK (effective_to IS NULL OR (effective_from IS NOT NULL AND effective_to > effective_from)),
  CHECK (ends_on IS NULL OR (starts_on IS NOT NULL AND ends_on >= starts_on)),
  CHECK (
    (recurrence_type = 'none' AND ends_on IS NULL)
    OR (recurrence_type <> 'none' AND starts_on IS NOT NULL AND effective_from IS NOT NULL)
  ),
  CHECK (
    (timing_type = 'unscheduled' AND recurrence_type = 'none' AND starts_on IS NULL AND local_start_time IS NULL AND duration_minutes IS NULL)
    OR (timing_type = 'date' AND starts_on IS NOT NULL AND local_start_time IS NULL AND duration_minutes IS NULL)
    OR (timing_type = 'time_block' AND starts_on IS NOT NULL AND local_start_time IS NOT NULL AND duration_minutes > 0)
  )
);

CREATE UNIQUE INDEX domain_task_schedule_versions_v2_one_open_idx
  ON domain_task_schedule_versions_v2(workspace_id, task_id)
  WHERE effective_to IS NULL;

CREATE TRIGGER domain_task_schedule_versions_v2_rule_insert
BEFORE INSERT ON domain_task_schedule_versions_v2
WHEN CASE NEW.recurrence_type
  WHEN 'none' THEN (SELECT COUNT(*) FROM json_each(NEW.recurrence_rule)) <> 0
  WHEN 'daily' THEN NOT (
    json_type(NEW.recurrence_rule, '$.interval') IS 'integer'
    AND COALESCE(json_extract(NEW.recurrence_rule, '$.interval'), 0) > 0
    AND (SELECT COUNT(*) FROM json_each(NEW.recurrence_rule)) = 1
  )
  WHEN 'weekly' THEN NOT (
    json_type(NEW.recurrence_rule, '$.interval') IS 'integer'
    AND COALESCE(json_extract(NEW.recurrence_rule, '$.interval'), 0) > 0
    AND json_type(NEW.recurrence_rule, '$.weekdays') IS 'array'
    AND COALESCE(json_array_length(NEW.recurrence_rule, '$.weekdays'), 0) > 0
    AND (SELECT COUNT(*) FROM json_each(NEW.recurrence_rule)) = 2
    AND NOT EXISTS (
      SELECT 1 FROM json_each(NEW.recurrence_rule, '$.weekdays') item
      WHERE item.type <> 'integer' OR item.value < 0 OR item.value > 6
    )
    AND (
      SELECT COUNT(DISTINCT item.value) FROM json_each(NEW.recurrence_rule, '$.weekdays') item
    ) = json_array_length(NEW.recurrence_rule, '$.weekdays')
  )
  WHEN 'monthly' THEN NOT (
    json_type(NEW.recurrence_rule, '$.interval') IS 'integer'
    AND COALESCE(json_extract(NEW.recurrence_rule, '$.interval'), 0) > 0
    AND json_type(NEW.recurrence_rule, '$.month_days') IS 'array'
    AND COALESCE(json_array_length(NEW.recurrence_rule, '$.month_days'), 0) > 0
    AND (SELECT COUNT(*) FROM json_each(NEW.recurrence_rule)) = 2
    AND NOT EXISTS (
      SELECT 1 FROM json_each(NEW.recurrence_rule, '$.month_days') item
      WHERE item.type <> 'integer' OR item.value < 1 OR item.value > 31
    )
    AND (
      SELECT COUNT(DISTINCT item.value) FROM json_each(NEW.recurrence_rule, '$.month_days') item
    ) = json_array_length(NEW.recurrence_rule, '$.month_days')
  )
  ELSE 1
END
BEGIN
  SELECT RAISE(ABORT, 'invalid task recurrence rule');
END;

CREATE TRIGGER domain_task_schedule_versions_v2_rule_update
BEFORE UPDATE OF recurrence_type, recurrence_rule ON domain_task_schedule_versions_v2
WHEN CASE NEW.recurrence_type
  WHEN 'none' THEN (SELECT COUNT(*) FROM json_each(NEW.recurrence_rule)) <> 0
  WHEN 'daily' THEN NOT (
    json_type(NEW.recurrence_rule, '$.interval') IS 'integer'
    AND COALESCE(json_extract(NEW.recurrence_rule, '$.interval'), 0) > 0
    AND (SELECT COUNT(*) FROM json_each(NEW.recurrence_rule)) = 1
  )
  WHEN 'weekly' THEN NOT (
    json_type(NEW.recurrence_rule, '$.interval') IS 'integer'
    AND COALESCE(json_extract(NEW.recurrence_rule, '$.interval'), 0) > 0
    AND json_type(NEW.recurrence_rule, '$.weekdays') IS 'array'
    AND COALESCE(json_array_length(NEW.recurrence_rule, '$.weekdays'), 0) > 0
    AND (SELECT COUNT(*) FROM json_each(NEW.recurrence_rule)) = 2
    AND NOT EXISTS (
      SELECT 1 FROM json_each(NEW.recurrence_rule, '$.weekdays') item
      WHERE item.type <> 'integer' OR item.value < 0 OR item.value > 6
    )
    AND (
      SELECT COUNT(DISTINCT item.value) FROM json_each(NEW.recurrence_rule, '$.weekdays') item
    ) = json_array_length(NEW.recurrence_rule, '$.weekdays')
  )
  WHEN 'monthly' THEN NOT (
    json_type(NEW.recurrence_rule, '$.interval') IS 'integer'
    AND COALESCE(json_extract(NEW.recurrence_rule, '$.interval'), 0) > 0
    AND json_type(NEW.recurrence_rule, '$.month_days') IS 'array'
    AND COALESCE(json_array_length(NEW.recurrence_rule, '$.month_days'), 0) > 0
    AND (SELECT COUNT(*) FROM json_each(NEW.recurrence_rule)) = 2
    AND NOT EXISTS (
      SELECT 1 FROM json_each(NEW.recurrence_rule, '$.month_days') item
      WHERE item.type <> 'integer' OR item.value < 1 OR item.value > 31
    )
    AND (
      SELECT COUNT(DISTINCT item.value) FROM json_each(NEW.recurrence_rule, '$.month_days') item
    ) = json_array_length(NEW.recurrence_rule, '$.month_days')
  )
  ELSE 1
END
BEGIN
  SELECT RAISE(ABORT, 'invalid task recurrence rule');
END;

CREATE TRIGGER domain_task_schedule_versions_v2_no_overlap_insert
BEFORE INSERT ON domain_task_schedule_versions_v2
WHEN EXISTS (
  SELECT 1 FROM domain_task_schedule_versions_v2 existing
  WHERE existing.workspace_id = NEW.workspace_id
    AND existing.task_id = NEW.task_id
    AND (existing.effective_to IS NULL OR NEW.effective_from IS NULL OR existing.effective_to > NEW.effective_from)
    AND (NEW.effective_to IS NULL OR existing.effective_from IS NULL OR NEW.effective_to > existing.effective_from)
)
BEGIN
  SELECT RAISE(ABORT, 'task schedule effective ranges overlap');
END;

CREATE TRIGGER domain_task_schedule_versions_v2_no_overlap_update
BEFORE UPDATE OF effective_from, effective_to ON domain_task_schedule_versions_v2
WHEN EXISTS (
  SELECT 1 FROM domain_task_schedule_versions_v2 existing
  WHERE existing.workspace_id = NEW.workspace_id
    AND existing.task_id = NEW.task_id
    AND existing.schedule_revision <> OLD.schedule_revision
    AND (existing.effective_to IS NULL OR NEW.effective_from IS NULL OR existing.effective_to > NEW.effective_from)
    AND (NEW.effective_to IS NULL OR existing.effective_from IS NULL OR NEW.effective_to > existing.effective_from)
)
BEGIN
  SELECT RAISE(ABORT, 'task schedule effective ranges overlap');
END;

CREATE TABLE domain_task_occurrences_v2 (
  workspace_id TEXT NOT NULL,
  id TEXT NOT NULL,
  task_id TEXT NOT NULL,
  occurrence_key TEXT NOT NULL,
  planned_date TEXT,
  planned_start_at TEXT,
  planned_end_at TEXT,
  due_at TEXT,
  execution_status TEXT NOT NULL CHECK (
    execution_status IN ('open', 'active', 'blocked', 'done', 'skipped', 'cancelled')
  ),
  actual_start_at TEXT,
  completed_at TEXT,
  override_title TEXT,
  override_description TEXT,
  location TEXT,
  calendar_kind TEXT,
  calendar_notes TEXT,
  note_id TEXT,
  all_day_end_date TEXT,
  blocked_reason TEXT,
  next_action TEXT,
  revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
  generated_schedule_revision INTEGER NOT NULL,
  manually_overridden INTEGER NOT NULL DEFAULT 0 CHECK (manually_overridden IN (0, 1)),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
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

CREATE TRIGGER domain_task_occurrences_v2_timing_insert
BEFORE INSERT ON domain_task_occurrences_v2
WHEN NOT EXISTS (
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
BEGIN
  SELECT RAISE(ABORT, 'occurrence timing does not match schedule version');
END;

CREATE TRIGGER domain_task_occurrences_v2_timing_update
BEFORE UPDATE OF planned_date, planned_start_at, planned_end_at, all_day_end_date, generated_schedule_revision
ON domain_task_occurrences_v2
WHEN NOT EXISTS (
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
BEGIN
  SELECT RAISE(ABORT, 'occurrence timing does not match schedule version');
END;

CREATE TABLE domain_task_execution_logs_v2 (
  workspace_id TEXT NOT NULL,
  id TEXT NOT NULL,
  occurrence_id TEXT NOT NULL,
  from_status TEXT CHECK (from_status IS NULL OR from_status IN ('open', 'active', 'blocked', 'done', 'skipped', 'cancelled')),
  to_status TEXT NOT NULL CHECK (to_status IN ('open', 'active', 'blocked', 'done', 'skipped', 'cancelled')),
  blocked_reason TEXT,
  next_action TEXT,
  actor_id TEXT NOT NULL,
  metadata TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(metadata) AND json_type(metadata) = 'object'),
  created_at TEXT NOT NULL,
  PRIMARY KEY (workspace_id, id),
  FOREIGN KEY (workspace_id, occurrence_id)
    REFERENCES domain_task_occurrences_v2(workspace_id, id),
  CHECK (
    (to_status = 'blocked' AND NULLIF(trim(blocked_reason), '') IS NOT NULL AND NULLIF(trim(next_action), '') IS NOT NULL)
    OR (to_status <> 'blocked' AND blocked_reason IS NULL AND next_action IS NULL)
  )
);

CREATE TRIGGER domain_task_execution_logs_v2_no_update
BEFORE UPDATE ON domain_task_execution_logs_v2
BEGIN
  SELECT RAISE(ABORT, 'task execution logs are immutable');
END;

CREATE TRIGGER domain_task_execution_logs_v2_no_delete
BEFORE DELETE ON domain_task_execution_logs_v2
BEGIN
  SELECT RAISE(ABORT, 'task execution logs are immutable');
END;
