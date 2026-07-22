CREATE TABLE task_domain_generation_jobs (
  job_id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL UNIQUE REFERENCES workspaces(id) ON DELETE CASCADE,
  claim_id TEXT UNIQUE,
  created_epoch INTEGER NOT NULL CHECK (created_epoch > 0),
  status TEXT NOT NULL CHECK (status IN ('queued', 'claimed', 'retry_pending', 'completed', 'failed')),
  attempt INTEGER NOT NULL DEFAULT 0 CHECK (attempt >= 0),
  available_at TEXT NOT NULL,
  lease_until TEXT,
  runtime_epoch INTEGER CHECK (runtime_epoch IS NULL OR runtime_epoch > 0),
  inserted INTEGER NOT NULL DEFAULT 0 CHECK (inserted >= 0),
  generation_watermark TEXT,
  error_code TEXT CHECK (error_code IN ('invalid_runtime', 'runtime_resolve_failed', 'fenced_write_failed')),
  revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  CHECK (
    (status = 'queued' AND claim_id IS NULL AND lease_until IS NULL AND runtime_epoch IS NULL
      AND attempt = 0 AND inserted = 0 AND generation_watermark IS NULL AND error_code IS NULL)
    OR
    (status = 'claimed' AND claim_id IS NOT NULL AND lease_until IS NOT NULL
      AND runtime_epoch IS NULL AND inserted = 0 AND generation_watermark IS NULL AND error_code IS NULL)
    OR
    (status = 'retry_pending' AND claim_id IS NOT NULL AND lease_until IS NULL AND error_code IS NOT NULL)
    OR
    (status = 'completed' AND claim_id IS NOT NULL AND lease_until IS NULL
      AND runtime_epoch IS NOT NULL AND error_code IS NULL)
    OR
    (status = 'failed' AND claim_id IS NOT NULL AND lease_until IS NULL AND error_code IS NOT NULL)
  )
);

CREATE INDEX task_domain_generation_jobs_claimable_idx
  ON task_domain_generation_jobs(status, available_at, lease_until, workspace_id);
