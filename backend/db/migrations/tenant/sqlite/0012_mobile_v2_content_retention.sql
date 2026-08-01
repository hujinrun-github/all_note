CREATE TABLE mobile_v2_content_tombstones (
  workspace_id TEXT NOT NULL,
  entity_type TEXT NOT NULL CHECK (entity_type IN ('note', 'voice_note', 'inbox')),
  entity_id TEXT NOT NULL,
  client_id TEXT,
  revision INTEGER NOT NULL CHECK (revision > 0),
  deleted_at INTEGER NOT NULL,
  PRIMARY KEY (workspace_id, entity_type, entity_id),
  FOREIGN KEY (workspace_id) REFERENCES tenant_workspaces(workspace_id) ON DELETE CASCADE
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
  size_bytes INTEGER NOT NULL CHECK (size_bytes > 0),
  sha256 TEXT NOT NULL,
  object_key TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (workspace_id, id),
  UNIQUE (workspace_id, object_key),
  FOREIGN KEY (workspace_id, note_id)
    REFERENCES notes(workspace_id, id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS note_attachments_note_created_idx
  ON note_attachments(workspace_id,note_id,created_at);

CREATE TABLE mobile_v2_scope_retention (
  workspace_id TEXT NOT NULL,
  scope TEXT NOT NULL CHECK (
    scope IN ('iphone-content', 'iphone-task-core', 'iphone-occurrence-window', 'watch-occurrence-window')
  ),
  compacted_through_sequence INTEGER NOT NULL DEFAULT 0 CHECK (compacted_through_sequence >= 0),
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (workspace_id, scope),
  FOREIGN KEY (workspace_id) REFERENCES tenant_workspaces(workspace_id) ON DELETE CASCADE
);

INSERT OR IGNORE INTO voice_audio_cleanup_jobs
  (job_id,workspace_id,voice_note_id,object_key,state,revision,attempt,max_attempts,error_code,
   next_attempt_at,lease_owner,lease_token,created_at,updated_at)
SELECT 'retention-attachment-'||a.id,a.workspace_id,'note_attachment:'||a.id,a.object_key,
       'queued',1,0,6,'',CAST(strftime('%s','now') AS INTEGER),'','',
       CAST(strftime('%s','now') AS INTEGER),CAST(strftime('%s','now') AS INTEGER)
FROM note_attachments a
JOIN notes n ON n.workspace_id=a.workspace_id AND n.id=a.note_id
WHERE n.deleted_at IS NOT NULL AND a.object_key<>'';

INSERT OR IGNORE INTO voice_audio_cleanup_jobs
  (job_id,workspace_id,voice_note_id,object_key,state,revision,attempt,max_attempts,error_code,
   next_attempt_at,lease_owner,lease_token,created_at,updated_at)
SELECT 'retention-voice-'||v.id,v.workspace_id,v.client_id,v.object_key,
       'queued',1,0,6,'',CAST(strftime('%s','now') AS INTEGER),'','',
       CAST(strftime('%s','now') AS INTEGER),CAST(strftime('%s','now') AS INTEGER)
FROM voice_notes v
WHERE v.deleted_at IS NOT NULL AND v.object_key<>'';

INSERT INTO mobile_v2_content_tombstones
  (workspace_id,entity_type,entity_id,client_id,revision,deleted_at)
SELECT workspace_id,'note',id,client_id,revision,deleted_at
FROM notes
WHERE deleted_at IS NOT NULL
ON CONFLICT(workspace_id,entity_type,entity_id) DO UPDATE SET
  client_id=excluded.client_id,
  revision=MAX(mobile_v2_content_tombstones.revision,excluded.revision),
  deleted_at=MIN(mobile_v2_content_tombstones.deleted_at,excluded.deleted_at);

INSERT INTO mobile_v2_content_tombstones
  (workspace_id,entity_type,entity_id,client_id,revision,deleted_at)
SELECT workspace_id,'voice_note',id,client_id,revision,deleted_at
FROM voice_notes
WHERE deleted_at IS NOT NULL
ON CONFLICT(workspace_id,entity_type,entity_id) DO UPDATE SET
  client_id=excluded.client_id,
  revision=MAX(mobile_v2_content_tombstones.revision,excluded.revision),
  deleted_at=MIN(mobile_v2_content_tombstones.deleted_at,excluded.deleted_at);

INSERT INTO mobile_v2_content_tombstones
  (workspace_id,entity_type,entity_id,client_id,revision,deleted_at)
SELECT workspace_id,'inbox',id,client_id,revision,deleted_at
FROM inbox
WHERE deleted_at IS NOT NULL
ON CONFLICT(workspace_id,entity_type,entity_id) DO UPDATE SET
  client_id=excluded.client_id,
  revision=MAX(mobile_v2_content_tombstones.revision,excluded.revision),
  deleted_at=MIN(mobile_v2_content_tombstones.deleted_at,excluded.deleted_at);

UPDATE domain_tasks_v2
SET note_id=NULL,revision=revision+1,updated_at=CURRENT_TIMESTAMP
WHERE note_id IS NOT NULL AND EXISTS (
  SELECT 1 FROM notes n
  WHERE n.workspace_id=domain_tasks_v2.workspace_id
    AND n.id=domain_tasks_v2.note_id
    AND n.deleted_at IS NOT NULL
);

UPDATE domain_task_occurrences_v2
SET note_id=NULL,revision=revision+1,updated_at=CURRENT_TIMESTAMP
WHERE note_id IS NOT NULL AND EXISTS (
  SELECT 1 FROM notes n
  WHERE n.workspace_id=domain_task_occurrences_v2.workspace_id
    AND n.id=domain_task_occurrences_v2.note_id
    AND n.deleted_at IS NOT NULL
);

UPDATE tasks
SET note_id=NULL,updated_at=CURRENT_TIMESTAMP
WHERE note_id IS NOT NULL AND EXISTS (
  SELECT 1 FROM notes n
  WHERE n.workspace_id=tasks.workspace_id
    AND n.id=tasks.note_id
    AND n.deleted_at IS NOT NULL
);

-- The updates above predate command receipts, so advance the head and compact
-- task scopes to an unreachable boundary. Existing cursors must snapshot once;
-- new snapshots contain the detached references and their incremented revisions.
UPDATE mobile_v2_commit_heads
SET latest_sequence=latest_sequence+1,updated_at=CURRENT_TIMESTAMP;

INSERT INTO mobile_v2_scope_retention
  (workspace_id,scope,compacted_through_sequence,updated_at)
SELECT h.workspace_id,scopes.scope,h.latest_sequence,CAST(strftime('%s','now') AS INTEGER)
FROM mobile_v2_commit_heads h
CROSS JOIN (
  SELECT 'iphone-task-core' AS scope
  UNION ALL SELECT 'iphone-occurrence-window'
  UNION ALL SELECT 'watch-occurrence-window'
) AS scopes
WHERE 1
ON CONFLICT(workspace_id,scope) DO UPDATE SET
  compacted_through_sequence=MAX(
    mobile_v2_scope_retention.compacted_through_sequence,
    excluded.compacted_through_sequence
  ),
  updated_at=excluded.updated_at;

DELETE FROM mobile_v2_scope_change_batches
WHERE scope IN ('iphone-task-core','iphone-occurrence-window','watch-occurrence-window')
  AND sequence<=(
    SELECT r.compacted_through_sequence
    FROM mobile_v2_scope_retention r
    WHERE r.workspace_id=mobile_v2_scope_change_batches.workspace_id
      AND r.scope=mobile_v2_scope_change_batches.scope
  );

UPDATE notes
SET title='',body='',tags='[]',content='',content_text=''
WHERE deleted_at IS NOT NULL;

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

UPDATE mobile_v2_change_batches
SET after_images_json=(
  SELECT COALESCE(json_group_array(json(
    CASE WHEN json_type(item.value,'$.deleted_at') IS NOT NULL
                AND json_type(item.value,'$.deleted_at')<>'null'
      THEN json_set(item.value,'$.payload',json('null'))
      ELSE item.value
    END
  )),'[]')
  FROM json_each(mobile_v2_change_batches.after_images_json) AS item
);

UPDATE mobile_v2_scope_change_batches
SET entities_json=(
  SELECT COALESCE(json_group_array(json(
    CASE WHEN json_type(item.value,'$.deleted_at') IS NOT NULL
                AND json_type(item.value,'$.deleted_at')<>'null'
      THEN json_set(item.value,'$.payload',json('null'))
      ELSE item.value
    END
  )),'[]')
  FROM json_each(mobile_v2_scope_change_batches.entities_json) AS item
);

-- Snapshot page and manifest checksums bind the original entities_json bytes.
-- Invalidating the session also removes its pages through ON DELETE CASCADE, so
-- clients obtain a newly projected and consistently checksummed snapshot.
DELETE FROM mobile_v2_snapshot_sessions;
