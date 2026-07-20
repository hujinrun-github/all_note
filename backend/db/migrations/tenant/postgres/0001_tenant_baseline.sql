CREATE TABLE tenant_installations (
  singleton_key SMALLINT PRIMARY KEY CHECK (singleton_key = 1),
  installation_id UUID NOT NULL UNIQUE,
  schema_identity TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO tenant_installations(singleton_key, installation_id, schema_identity)
VALUES (1, md5(random()::text || clock_timestamp()::text)::uuid, current_schema());

CREATE TABLE tenant_capabilities (
  capability TEXT PRIMARY KEY,
  enabled BOOLEAN NOT NULL,
  detail TEXT NOT NULL DEFAULT ''
);

INSERT INTO tenant_capabilities(capability, enabled, detail)
SELECT 'trigram_search', EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_trgm'),
       CASE WHEN EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_trgm') THEN 'pg_trgm installed' ELSE 'portable search' END;

CREATE TABLE tenant_workspaces (
  workspace_id TEXT PRIMARY KEY,
  epoch BIGINT NOT NULL DEFAULT 1 CHECK (epoch > 0),
  state TEXT NOT NULL DEFAULT 'active' CHECK (state IN ('active', 'fenced', 'retired')),
  migration_id TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK ((state = 'fenced' AND migration_id IS NOT NULL) OR (state <> 'fenced' AND migration_id IS NULL))
);

CREATE TABLE folders (
  id TEXT NOT NULL,
  workspace_id TEXT NOT NULL REFERENCES tenant_workspaces(workspace_id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  position INTEGER NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, id)
);

CREATE TABLE notes (
  id TEXT NOT NULL,
  workspace_id TEXT NOT NULL REFERENCES tenant_workspaces(workspace_id) ON DELETE CASCADE,
  folder_id TEXT,
  title TEXT NOT NULL DEFAULT '',
  content JSONB NOT NULL DEFAULT '{}'::jsonb,
  content_text TEXT NOT NULL DEFAULT '',
  content_format TEXT NOT NULL DEFAULT 'tiptap_json',
  revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
  pinned BOOLEAN NOT NULL DEFAULT false,
  deleted_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, id),
  FOREIGN KEY (workspace_id, folder_id) REFERENCES folders(workspace_id, id)
);

CREATE INDEX notes_workspace_updated_idx ON notes(workspace_id, updated_at DESC);

CREATE TABLE task_projects (
  id TEXT NOT NULL,
  workspace_id TEXT NOT NULL REFERENCES tenant_workspaces(workspace_id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  color TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, id)
);

CREATE TABLE tasks (
  id TEXT NOT NULL,
  workspace_id TEXT NOT NULL REFERENCES tenant_workspaces(workspace_id) ON DELETE CASCADE,
  project_id TEXT,
  note_id TEXT,
  title TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'todo',
  priority INTEGER NOT NULL DEFAULT 0,
  due_at TIMESTAMPTZ,
  completed_at TIMESTAMPTZ,
  deleted_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, id),
  FOREIGN KEY (workspace_id, project_id) REFERENCES task_projects(workspace_id, id),
  FOREIGN KEY (workspace_id, note_id) REFERENCES notes(workspace_id, id)
);

CREATE INDEX tasks_workspace_updated_idx ON tasks(workspace_id, updated_at DESC);

CREATE TABLE tenant_job_outbox (
  id TEXT NOT NULL,
  workspace_id TEXT NOT NULL REFERENCES tenant_workspaces(workspace_id) ON DELETE CASCADE,
  topic TEXT NOT NULL,
  aggregate_id TEXT NOT NULL,
  aggregate_revision BIGINT NOT NULL CHECK (aggregate_revision > 0),
  payload_json JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  published_at TIMESTAMPTZ,
  PRIMARY KEY (workspace_id, id),
  UNIQUE (workspace_id, topic, aggregate_id, aggregate_revision)
);
