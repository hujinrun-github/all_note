ALTER TABLE sync_targets
  ADD COLUMN IF NOT EXISTS is_default BOOLEAN NOT NULL DEFAULT false;

CREATE UNIQUE INDEX IF NOT EXISTS sync_targets_one_default_per_type_idx
  ON sync_targets (type) WHERE is_default = true;

CREATE TABLE IF NOT EXISTS note_sync_bindings (
  note_id TEXT PRIMARY KEY REFERENCES notes(id) ON DELETE CASCADE,
  target_id TEXT NOT NULL REFERENCES sync_targets(id) ON DELETE RESTRICT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (note_id, target_id)
);

CREATE INDEX IF NOT EXISTS note_sync_bindings_target_idx
  ON note_sync_bindings (target_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS sync_external_claims (
  external_key TEXT PRIMARY KEY,
  note_id TEXT NOT NULL UNIQUE,
  target_id TEXT NOT NULL,
  external_type TEXT NOT NULL CHECK (external_type IN ('obsidian_file', 'notion_page')),
  external_id TEXT NOT NULL DEFAULT '',
  external_path TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  FOREIGN KEY (note_id, target_id)
    REFERENCES note_sync_bindings(note_id, target_id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS sync_external_claims_target_idx
  ON sync_external_claims (target_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS note_sync_suppressions (
  note_id TEXT NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
  target_id TEXT NOT NULL REFERENCES sync_targets(id) ON DELETE CASCADE,
  reason TEXT NOT NULL DEFAULT 'user_unbound'
    CHECK (reason IN ('user_unbound', 'target_changed')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (note_id, target_id)
);

CREATE INDEX IF NOT EXISTS note_sync_suppressions_target_updated_idx
  ON note_sync_suppressions (target_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS sync_import_tombstones (
  external_key TEXT PRIMARY KEY,
  target_id TEXT NOT NULL REFERENCES sync_targets(id) ON DELETE CASCADE,
  former_note_id TEXT NOT NULL,
  external_type TEXT NOT NULL CHECK (external_type IN ('obsidian_file', 'notion_page')),
  external_id TEXT NOT NULL DEFAULT '',
  external_path TEXT NOT NULL DEFAULT '',
  reason TEXT NOT NULL DEFAULT 'user_unbound'
    CHECK (reason IN ('user_unbound', 'target_changed', 'note_deleted')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (target_id, former_note_id, external_type)
);

CREATE INDEX IF NOT EXISTS sync_import_tombstones_target_updated_idx
  ON sync_import_tombstones (target_id, updated_at DESC);

CREATE INDEX IF NOT EXISTS sync_import_tombstones_note_type_idx
  ON sync_import_tombstones (former_note_id, external_type, updated_at DESC, created_at DESC);

DO $$
DECLARE
  constraint_record RECORD;
BEGIN
  FOR constraint_record IN
    SELECT conname
    FROM pg_constraint
    WHERE conrelid = 'note_sync_state'::regclass
      AND contype = 'c'
      AND pg_get_constraintdef(oid) LIKE '%last_direction%'
  LOOP
    EXECUTE format('ALTER TABLE note_sync_state DROP CONSTRAINT %I', constraint_record.conname);
  END LOOP;

  IF NOT EXISTS (
    SELECT 1
    FROM pg_constraint
    WHERE conrelid = 'note_sync_state'::regclass
      AND conname = 'note_sync_state_last_direction_check'
  ) THEN
    ALTER TABLE note_sync_state
      ADD CONSTRAINT note_sync_state_last_direction_check
      CHECK (last_direction IN ('push', 'pull', 'import', 'restore', 'delete', 'delete_detected') OR last_direction IS NULL);
  END IF;
END $$;
