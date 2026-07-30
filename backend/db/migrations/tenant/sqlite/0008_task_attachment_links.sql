ALTER TABLE domain_tasks_v2
  ADD COLUMN attachment_links TEXT NOT NULL DEFAULT '[]'
  CHECK (json_valid(attachment_links) AND json_type(attachment_links) = 'array');
