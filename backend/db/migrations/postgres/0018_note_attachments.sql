CREATE TABLE IF NOT EXISTS note_attachments (
  id TEXT NOT NULL,
  workspace_id TEXT NOT NULL,
  note_id TEXT NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('audio', 'video', 'image', 'file')),
  original_name TEXT NOT NULL,
  mime_type TEXT NOT NULL,
  size_bytes BIGINT NOT NULL CHECK (size_bytes > 0),
  sha256 TEXT NOT NULL,
  object_key TEXT NOT NULL,
  created_at BIGINT NOT NULL,
  PRIMARY KEY (workspace_id, id),
  UNIQUE (workspace_id, object_key),
  FOREIGN KEY (workspace_id, note_id)
    REFERENCES notes(workspace_id, id)
    ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS note_attachments_note_created_idx
  ON note_attachments (workspace_id, note_id, created_at);
