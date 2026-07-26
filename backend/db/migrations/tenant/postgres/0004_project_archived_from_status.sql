ALTER TABLE domain_projects_v2
ADD COLUMN archived_from_status TEXT;

ALTER TABLE domain_projects_v2
ADD CONSTRAINT domain_projects_v2_archived_from_status_check
CHECK (
  (status = 'archived' AND (
    archived_from_status IS NULL OR
    archived_from_status IN ('planning', 'active', 'paused', 'completed')
  )) OR
  (status <> 'archived' AND archived_from_status IS NULL)
);
