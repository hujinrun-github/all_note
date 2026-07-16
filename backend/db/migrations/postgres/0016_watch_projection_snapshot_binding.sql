ALTER TABLE mobile_sync_snapshot_sessions
  ADD COLUMN IF NOT EXISTS projection_time_zone TEXT NOT NULL DEFAULT 'UTC',
  ADD COLUMN IF NOT EXISTS scope_valid_until TIMESTAMPTZ NOT NULL DEFAULT to_timestamp(0);
