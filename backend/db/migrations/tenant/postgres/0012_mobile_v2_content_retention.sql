CREATE TABLE mobile_v2_content_tombstones (
  workspace_id TEXT NOT NULL REFERENCES tenant_workspaces(workspace_id) ON DELETE CASCADE,
  entity_type TEXT NOT NULL CHECK (entity_type IN ('note', 'voice_note', 'inbox')),
  entity_id TEXT NOT NULL,
  client_id TEXT,
  revision BIGINT NOT NULL CHECK (revision > 0),
  deleted_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (workspace_id, entity_type, entity_id)
);

CREATE UNIQUE INDEX mobile_v2_content_tombstones_client_idx
  ON mobile_v2_content_tombstones(workspace_id,entity_type,client_id)
  WHERE client_id IS NOT NULL;

CREATE INDEX mobile_v2_content_tombstones_deleted_idx
  ON mobile_v2_content_tombstones(workspace_id, deleted_at);

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
    REFERENCES notes(workspace_id, id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS note_attachments_note_created_idx
  ON note_attachments(workspace_id,note_id,created_at);

CREATE TABLE mobile_v2_scope_retention (
  workspace_id TEXT NOT NULL REFERENCES tenant_workspaces(workspace_id) ON DELETE CASCADE,
  scope TEXT NOT NULL CHECK (
    scope IN ('iphone-content', 'iphone-task-core', 'iphone-occurrence-window', 'watch-occurrence-window')
  ),
  compacted_through_sequence BIGINT NOT NULL DEFAULT 0 CHECK (compacted_through_sequence >= 0),
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (workspace_id, scope)
);

INSERT INTO voice_audio_cleanup_jobs
  (job_id,workspace_id,voice_note_id,object_key,state,revision,attempt,max_attempts,error_code,
   next_attempt_at,lease_owner,lease_token,created_at,updated_at)
SELECT 'retention-attachment-'||a.id,a.workspace_id,'note_attachment:'||a.id,a.object_key,
       'queued',1,0,6,'',EXTRACT(EPOCH FROM now())::bigint,'','',
       EXTRACT(EPOCH FROM now())::bigint,EXTRACT(EPOCH FROM now())::bigint
FROM note_attachments a
JOIN notes n ON n.workspace_id=a.workspace_id AND n.id=a.note_id
WHERE n.deleted_at IS NOT NULL AND a.object_key<>''
ON CONFLICT (workspace_id,voice_note_id,object_key) DO NOTHING;

INSERT INTO voice_audio_cleanup_jobs
  (job_id,workspace_id,voice_note_id,object_key,state,revision,attempt,max_attempts,error_code,
   next_attempt_at,lease_owner,lease_token,created_at,updated_at)
SELECT 'retention-voice-'||v.id,v.workspace_id,v.client_id,v.object_key,
       'queued',1,0,6,'',EXTRACT(EPOCH FROM now())::bigint,'','',
       EXTRACT(EPOCH FROM now())::bigint,EXTRACT(EPOCH FROM now())::bigint
FROM voice_notes v
WHERE v.deleted_at IS NOT NULL AND v.object_key<>''
ON CONFLICT (workspace_id,voice_note_id,object_key) DO NOTHING;

INSERT INTO mobile_v2_content_tombstones
  (workspace_id,entity_type,entity_id,client_id,revision,deleted_at)
SELECT workspace_id,'note',id,client_id,revision,deleted_at
FROM notes
WHERE deleted_at IS NOT NULL
ON CONFLICT (workspace_id,entity_type,entity_id) DO UPDATE SET
  client_id=EXCLUDED.client_id,
  revision=GREATEST(mobile_v2_content_tombstones.revision,EXCLUDED.revision),
  deleted_at=LEAST(mobile_v2_content_tombstones.deleted_at,EXCLUDED.deleted_at);

INSERT INTO mobile_v2_content_tombstones
  (workspace_id,entity_type,entity_id,client_id,revision,deleted_at)
SELECT workspace_id,'voice_note',id,client_id,revision,deleted_at
FROM voice_notes
WHERE deleted_at IS NOT NULL
ON CONFLICT (workspace_id,entity_type,entity_id) DO UPDATE SET
  client_id=EXCLUDED.client_id,
  revision=GREATEST(mobile_v2_content_tombstones.revision,EXCLUDED.revision),
  deleted_at=LEAST(mobile_v2_content_tombstones.deleted_at,EXCLUDED.deleted_at);

INSERT INTO mobile_v2_content_tombstones
  (workspace_id,entity_type,entity_id,client_id,revision,deleted_at)
SELECT workspace_id,'inbox',id,client_id,revision,deleted_at
FROM inbox
WHERE deleted_at IS NOT NULL
ON CONFLICT (workspace_id,entity_type,entity_id) DO UPDATE SET
  client_id=EXCLUDED.client_id,
  revision=GREATEST(mobile_v2_content_tombstones.revision,EXCLUDED.revision),
  deleted_at=LEAST(mobile_v2_content_tombstones.deleted_at,EXCLUDED.deleted_at);

UPDATE domain_tasks_v2 t
SET note_id=NULL,revision=t.revision+1,updated_at=now()
FROM notes n
WHERE n.workspace_id=t.workspace_id
  AND n.id=t.note_id
  AND n.deleted_at IS NOT NULL;

UPDATE domain_task_occurrences_v2 o
SET note_id=NULL,revision=o.revision+1,updated_at=now()
FROM notes n
WHERE n.workspace_id=o.workspace_id
  AND n.id=o.note_id
  AND n.deleted_at IS NOT NULL;

-- Adopted legacy tenants are allowed to have a narrower tasks table than the
-- native tenant baseline. Detach legacy note references only when that column
-- exists, and do not require updated_at on the narrowest adopted schemas.
DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema=current_schema()
      AND table_name='tasks'
      AND column_name='note_id'
  ) THEN
    IF EXISTS (
      SELECT 1 FROM information_schema.columns
      WHERE table_schema=current_schema()
        AND table_name='tasks'
        AND column_name='updated_at'
    ) THEN
      EXECUTE $sql$
        UPDATE tasks t
        SET note_id=NULL,updated_at=now()
        FROM notes n
        WHERE n.workspace_id=t.workspace_id
          AND n.id=t.note_id
          AND n.deleted_at IS NOT NULL
      $sql$;
    ELSE
      EXECUTE $sql$
        UPDATE tasks t
        SET note_id=NULL
        FROM notes n
        WHERE n.workspace_id=t.workspace_id
          AND n.id=t.note_id
          AND n.deleted_at IS NOT NULL
      $sql$;
    END IF;
  END IF;
END
$$;

-- The updates above predate command receipts, so advance the head and compact
-- task scopes to an unreachable boundary. Existing cursors must snapshot once;
-- new snapshots contain the detached references and their incremented revisions.
UPDATE mobile_v2_commit_heads
SET latest_sequence=latest_sequence+1,updated_at=now();

INSERT INTO mobile_v2_scope_retention
  (workspace_id,scope,compacted_through_sequence,updated_at)
SELECT h.workspace_id,scopes.scope,h.latest_sequence,now()
FROM mobile_v2_commit_heads h
CROSS JOIN (VALUES
  ('iphone-task-core'),
  ('iphone-occurrence-window'),
  ('watch-occurrence-window')
) AS scopes(scope)
ON CONFLICT (workspace_id,scope) DO UPDATE SET
  compacted_through_sequence=GREATEST(
    mobile_v2_scope_retention.compacted_through_sequence,
    EXCLUDED.compacted_through_sequence
  ),
  updated_at=EXCLUDED.updated_at;

DELETE FROM mobile_v2_scope_change_batches b
USING mobile_v2_scope_retention r
WHERE r.workspace_id=b.workspace_id
  AND r.scope=b.scope
  AND b.scope IN ('iphone-task-core','iphone-occurrence-window','watch-occurrence-window')
  AND b.sequence<=r.compacted_through_sequence;

UPDATE notes
SET body='',tags='{}'::text[]
WHERE deleted_at IS NOT NULL;

-- Native tenant notes include title/content/content_text, while adopted
-- legacy schemas may omit one or more of them. Redact every available field
-- without making optional legacy columns a migration prerequisite.
DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema=current_schema()
      AND table_name='notes'
      AND column_name='title'
  ) THEN
    EXECUTE 'UPDATE notes SET title='''' WHERE deleted_at IS NOT NULL';
  END IF;
  IF EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema=current_schema()
      AND table_name='notes'
      AND column_name='content'
  ) THEN
    EXECUTE 'UPDATE notes SET content=''{}''::jsonb WHERE deleted_at IS NOT NULL';
  END IF;
  IF EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema=current_schema()
      AND table_name='notes'
      AND column_name='content_text'
  ) THEN
    EXECUTE 'UPDATE notes SET content_text='''' WHERE deleted_at IS NOT NULL';
  END IF;
END
$$;

UPDATE voice_notes
SET duration_ms=0,recorded_at=0,language='',transcription_error=''
WHERE deleted_at IS NOT NULL;

UPDATE inbox
SET kind='note',title='',body=NULL,source='',converted_to=NULL
WHERE deleted_at IS NOT NULL;

UPDATE transcription_jobs
SET language='',error_code=''
WHERE EXISTS (
  SELECT 1 FROM voice_notes v
  WHERE v.workspace_id=transcription_jobs.workspace_id
    AND v.client_id=transcription_jobs.voice_note_id
    AND v.deleted_at IS NOT NULL
);

DELETE FROM transcription_results
WHERE EXISTS (
  SELECT 1 FROM voice_notes v
  WHERE v.workspace_id=transcription_results.workspace_id
    AND v.client_id=transcription_results.voice_note_id
    AND v.deleted_at IS NOT NULL
);

UPDATE mobile_v2_change_batches b
SET after_images_json=COALESCE((
  SELECT jsonb_agg(
    CASE WHEN item.value->'deleted_at' IS NOT NULL AND item.value->'deleted_at'<>'null'::jsonb
      THEN jsonb_set(item.value,'{payload}','null'::jsonb,true)
      ELSE item.value
    END ORDER BY item.ordinality
  )
  FROM jsonb_array_elements(b.after_images_json) WITH ORDINALITY AS item(value,ordinality)
),'[]'::jsonb);

UPDATE mobile_v2_scope_change_batches b
SET entities_json=COALESCE((
  SELECT jsonb_agg(
    CASE WHEN item.value->'deleted_at' IS NOT NULL AND item.value->'deleted_at'<>'null'::jsonb
      THEN jsonb_set(item.value,'{payload}','null'::jsonb,true)
      ELSE item.value
    END ORDER BY item.ordinality
  )
  FROM jsonb_array_elements(b.entities_json) WITH ORDINALITY AS item(value,ordinality)
),'[]'::jsonb);

-- Snapshot page and manifest checksums bind the original entities_json bytes.
-- Invalidating the session also removes its pages through ON DELETE CASCADE, so
-- clients obtain a newly projected and consistently checksummed snapshot.
DELETE FROM mobile_v2_snapshot_sessions;
