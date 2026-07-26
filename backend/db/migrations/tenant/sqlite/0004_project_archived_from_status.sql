ALTER TABLE domain_projects_v2
ADD COLUMN archived_from_status TEXT
CHECK (
  (status = 'archived' AND (
    archived_from_status IS NULL OR
    archived_from_status IN ('planning', 'active', 'paused', 'completed')
  )) OR
  (status <> 'archived' AND archived_from_status IS NULL)
);
