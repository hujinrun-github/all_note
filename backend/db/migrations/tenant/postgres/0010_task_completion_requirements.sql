ALTER TABLE domain_tasks_v2
  ADD COLUMN completion_requirements JSONB NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE domain_tasks_v2
  ADD CONSTRAINT domain_tasks_v2_completion_requirements_array
  CHECK (jsonb_typeof(completion_requirements) = 'array');
