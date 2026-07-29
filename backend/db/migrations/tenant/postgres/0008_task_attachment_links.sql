ALTER TABLE domain_tasks_v2
  ADD COLUMN attachment_links JSONB NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE domain_tasks_v2
  ADD CONSTRAINT domain_tasks_v2_attachment_links_array
  CHECK (jsonb_typeof(attachment_links) = 'array');
