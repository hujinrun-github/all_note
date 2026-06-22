ALTER TABLE tasks ADD COLUMN execution_type TEXT NOT NULL DEFAULT 'single'
  CHECK (execution_type IN ('single', 'recurring'));

UPDATE tasks SET execution_type = 'single' WHERE execution_type IS NULL OR execution_type = '';

CREATE TABLE task_recurrence_rules (
  task_id TEXT PRIMARY KEY REFERENCES tasks(id) ON DELETE CASCADE,
  start_date DATE NOT NULL,
  end_date DATE,
  frequency TEXT NOT NULL CHECK (frequency IN ('daily', 'weekly', 'monthly')),
  interval INTEGER NOT NULL DEFAULT 1 CHECK (interval >= 1),
  weekdays INTEGER[] NOT NULL DEFAULT '{}',
  month_days INTEGER[] NOT NULL DEFAULT '{}',
  timezone TEXT NOT NULL,
  enabled BOOLEAN NOT NULL DEFAULT true,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (end_date IS NULL OR end_date >= start_date)
);

CREATE INDEX task_recurrence_rules_enabled_idx
  ON task_recurrence_rules (enabled, start_date, end_date);

CREATE TABLE task_occurrences (
  task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  occurrence_date DATE NOT NULL,
  status TEXT NOT NULL DEFAULT 'open'
    CHECK (status IN ('open', 'done', 'skipped')),
  completed_at TIMESTAMPTZ,
  note TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (task_id, occurrence_date),
  CHECK (
    (status = 'done' AND completed_at IS NOT NULL)
    OR (status <> 'done')
  )
);

CREATE INDEX task_occurrences_date_idx
  ON task_occurrences (occurrence_date, status);

CREATE INDEX task_occurrences_task_date_idx
  ON task_occurrences (task_id, occurrence_date);

CREATE INDEX task_occurrences_completed_at_idx
  ON task_occurrences (completed_at) WHERE completed_at IS NOT NULL;
