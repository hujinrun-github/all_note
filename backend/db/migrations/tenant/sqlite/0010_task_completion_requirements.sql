ALTER TABLE domain_tasks_v2
  ADD COLUMN completion_requirements TEXT NOT NULL DEFAULT '[]'
  CHECK (json_valid(completion_requirements) AND json_type(completion_requirements) = 'array');
