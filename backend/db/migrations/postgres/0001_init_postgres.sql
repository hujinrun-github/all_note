CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public;

CREATE TABLE folders (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  sort_order DOUBLE PRECISION NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE notes (
  id TEXT PRIMARY KEY,
  title TEXT NOT NULL,
  body TEXT NOT NULL DEFAULT '',
  folder_id TEXT NOT NULL DEFAULT '__uncategorized' REFERENCES folders(id),
  tags TEXT[] NOT NULL DEFAULT '{}',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX notes_folder_updated_idx ON notes (folder_id, updated_at DESC);
CREATE INDEX notes_updated_idx ON notes (updated_at DESC);
CREATE INDEX notes_tags_idx ON notes USING GIN (tags);
CREATE INDEX notes_title_trgm_idx ON notes USING GIN (title public.gin_trgm_ops);

CREATE TABLE task_projects (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  type TEXT NOT NULL DEFAULT 'regular'
    CHECK (type IN ('personal', 'regular', 'learning')),
  description TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE tasks (
  id TEXT PRIMARY KEY,
  title TEXT NOT NULL,
  content TEXT NOT NULL DEFAULT '',
  project TEXT,
  project_id TEXT NOT NULL DEFAULT 'personal'
    REFERENCES task_projects(id) ON DELETE SET DEFAULT,
  due_at TIMESTAMPTZ,
  planned_date DATE,
  priority INTEGER NOT NULL DEFAULT 0 CHECK (priority >= 0),
  done BOOLEAN NOT NULL DEFAULT false,
  status TEXT NOT NULL DEFAULT 'open'
    CHECK (status IN ('open', 'active', 'blocked', 'done', 'archived', 'migrated', 'cancelled')),
  horizon TEXT NOT NULL DEFAULT 'week'
    CHECK (horizon IN ('day', 'week', 'long')),
  scope TEXT NOT NULL DEFAULT 'daily'
    CHECK (scope IN ('daily', 'weekly', 'monthly', 'yearly')),
  sort_order DOUBLE PRECISION NOT NULL DEFAULT 0,
  note_id TEXT REFERENCES notes(id) ON DELETE SET NULL,
  roadmap_node_id TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX tasks_project_status_idx ON tasks (project_id, status, planned_date);
CREATE INDEX tasks_today_idx ON tasks (planned_date, status, sort_order);
CREATE INDEX tasks_due_open_idx ON tasks (due_at) WHERE done = false AND due_at IS NOT NULL;
CREATE INDEX tasks_planned_open_idx ON tasks (planned_date, sort_order) WHERE done = false AND planned_date IS NOT NULL AND horizon <> 'long';
CREATE INDEX tasks_long_active_idx ON tasks (updated_at DESC) WHERE done = false AND horizon = 'long' AND status = 'active';
CREATE INDEX tasks_note_idx ON tasks (note_id) WHERE note_id IS NOT NULL;
CREATE INDEX tasks_roadmap_node_idx ON tasks (roadmap_node_id);
CREATE INDEX tasks_title_trgm_idx ON tasks USING GIN (title public.gin_trgm_ops);

CREATE TABLE learning_roadmaps (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL UNIQUE REFERENCES task_projects(id) ON DELETE CASCADE,
  title TEXT NOT NULL,
  goal TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'draft'
    CHECK (status IN ('draft', 'ready', 'active', 'done', 'archived', 'failed')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE roadmap_nodes (
  id TEXT PRIMARY KEY,
  roadmap_id TEXT NOT NULL REFERENCES learning_roadmaps(id) ON DELETE CASCADE,
  parent_id TEXT,
  type TEXT NOT NULL DEFAULT 'task'
    CHECK (type IN ('phase', 'module', 'choice', 'task')),
  title TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  path_type TEXT NOT NULL DEFAULT 'required'
    CHECK (path_type IN ('required', 'recommended', 'optional', 'alternative')),
  status TEXT NOT NULL DEFAULT 'todo'
    CHECK (status IN ('todo', 'active', 'done', 'skipped')),
  deliverable TEXT NOT NULL DEFAULT '',
  acceptance_criteria TEXT NOT NULL DEFAULT '',
  position JSONB NOT NULL DEFAULT '{"x":0,"y":0}',
  order_index INTEGER NOT NULL DEFAULT 0,
  article_search_queries TEXT[] NOT NULL DEFAULT '{}',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (roadmap_id, id),
  FOREIGN KEY (roadmap_id, parent_id)
    REFERENCES roadmap_nodes(roadmap_id, id) ON DELETE CASCADE
    DEFERRABLE INITIALLY DEFERRED
);

CREATE TABLE roadmap_edges (
  id TEXT PRIMARY KEY,
  roadmap_id TEXT NOT NULL REFERENCES learning_roadmaps(id) ON DELETE CASCADE,
  source_node_id TEXT NOT NULL,
  target_node_id TEXT NOT NULL,
  style TEXT NOT NULL DEFAULT 'solid',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (roadmap_id, source_node_id, target_node_id),
  FOREIGN KEY (roadmap_id, source_node_id)
    REFERENCES roadmap_nodes(roadmap_id, id) ON DELETE CASCADE,
  FOREIGN KEY (roadmap_id, target_node_id)
    REFERENCES roadmap_nodes(roadmap_id, id) ON DELETE CASCADE
);

CREATE TABLE roadmap_resources (
  id TEXT PRIMARY KEY,
  node_id TEXT NOT NULL REFERENCES roadmap_nodes(id) ON DELETE CASCADE,
  title TEXT NOT NULL,
  url TEXT NOT NULL,
  summary TEXT NOT NULL DEFAULT '',
  source_type TEXT NOT NULL DEFAULT 'article',
  added_by TEXT NOT NULL DEFAULT 'user',
  metadata JSONB NOT NULL DEFAULT '{}',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX roadmap_nodes_roadmap_parent_idx ON roadmap_nodes (roadmap_id, parent_id, order_index);
CREATE INDEX roadmap_nodes_parent_idx ON roadmap_nodes (parent_id) WHERE parent_id IS NOT NULL;
CREATE INDEX roadmap_edges_source_idx ON roadmap_edges (roadmap_id, source_node_id);
CREATE INDEX roadmap_edges_target_idx ON roadmap_edges (roadmap_id, target_node_id);
CREATE INDEX roadmap_resources_node_idx ON roadmap_resources (node_id);

ALTER TABLE tasks
  ADD CONSTRAINT tasks_roadmap_node_fk
  FOREIGN KEY (roadmap_node_id) REFERENCES roadmap_nodes(id) ON DELETE SET NULL;

CREATE TABLE events (
  id TEXT PRIMARY KEY,
  title TEXT NOT NULL,
  start_at TIMESTAMPTZ NOT NULL,
  end_at TIMESTAMPTZ NOT NULL,
  time_range TSTZRANGE NOT NULL,
  location TEXT,
  kind TEXT NOT NULL DEFAULT 'work',
  note_id TEXT REFERENCES notes(id) ON DELETE SET NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (end_at > start_at)
);

CREATE INDEX events_time_range_idx ON events USING GIST (time_range);
CREATE INDEX events_kind_start_idx ON events (kind, start_at);
CREATE INDEX events_note_idx ON events (note_id) WHERE note_id IS NOT NULL;
CREATE INDEX events_title_trgm_idx ON events USING GIN (title public.gin_trgm_ops);

CREATE TABLE inbox (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL,
  title TEXT NOT NULL,
  body TEXT,
  source TEXT NOT NULL DEFAULT 'quick-capture',
  archived BOOLEAN NOT NULL DEFAULT false,
  converted_to TEXT,
  payload JSONB NOT NULL DEFAULT '{}',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX inbox_archived_created_idx ON inbox (archived, created_at DESC);
CREATE INDEX inbox_open_created_idx ON inbox (created_at DESC) WHERE archived = false AND converted_to IS NULL;
CREATE INDEX inbox_payload_idx ON inbox USING GIN (payload);

CREATE TABLE sync_targets (
  id TEXT PRIMARY KEY,
  type TEXT NOT NULL CHECK (type IN ('obsidian', 'notion')),
  name TEXT NOT NULL,
  vault_path TEXT NOT NULL DEFAULT '',
  base_folder TEXT NOT NULL DEFAULT '',
  config JSONB NOT NULL DEFAULT '{}',
  enabled BOOLEAN NOT NULL DEFAULT true,
  auto_sync BOOLEAN NOT NULL DEFAULT false,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (type, name)
);

CREATE TABLE note_sync_state (
  note_id TEXT NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
  target_id TEXT NOT NULL REFERENCES sync_targets(id) ON DELETE CASCADE,
  external_path TEXT NOT NULL,
  external_id TEXT,
  external_url TEXT,
  content_hash TEXT NOT NULL,
  external_hash TEXT,
  external_mtime TIMESTAMPTZ,
  last_direction TEXT CHECK (last_direction IN ('push', 'pull', 'import', 'restore', 'delete') OR last_direction IS NULL),
  last_synced_at TIMESTAMPTZ,
  status TEXT NOT NULL CHECK (status IN ('synced', 'pending', 'failed', 'external_deleted')),
  error_message TEXT,
  external_metadata JSONB NOT NULL DEFAULT '{}',
  PRIMARY KEY (note_id, target_id)
);

CREATE INDEX sync_targets_type_enabled_idx ON sync_targets (type, enabled, updated_at DESC);
CREATE INDEX note_sync_target_status_idx ON note_sync_state (target_id, status, last_synced_at DESC);
CREATE INDEX note_sync_target_note_idx ON note_sync_state (target_id, note_id);
CREATE INDEX note_sync_external_id_idx ON note_sync_state (target_id, external_id);
CREATE INDEX note_sync_metadata_idx ON note_sync_state USING GIN (external_metadata);

CREATE TABLE note_project_links (
  note_id TEXT NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
  project_id TEXT NOT NULL REFERENCES task_projects(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (note_id, project_id)
);

CREATE INDEX note_project_links_project_note_idx
  ON note_project_links (project_id, note_id);

CREATE TABLE search_index (
  entity_type TEXT NOT NULL CHECK (entity_type IN ('note', 'task', 'event')),
  entity_id TEXT NOT NULL,
  title TEXT NOT NULL,
  content TEXT NOT NULL DEFAULT '',
  tags TEXT[] NOT NULL DEFAULT '{}',
  updated_at TIMESTAMPTZ NOT NULL,
  search_vector TSVECTOR NOT NULL,
  PRIMARY KEY (entity_type, entity_id)
);

CREATE INDEX search_index_vector_idx ON search_index USING GIN (search_vector);
CREATE INDEX search_index_title_trgm_idx ON search_index USING GIN (title public.gin_trgm_ops);
CREATE INDEX search_index_content_trgm_idx ON search_index USING GIN (content public.gin_trgm_ops);
CREATE INDEX search_index_updated_idx ON search_index (updated_at DESC);

INSERT INTO folders (id, name, sort_order, created_at)
VALUES
  ('__uncategorized', 'Uncategorized', 0, now()),
  ('__work', 'Work', 1, now()),
  ('__personal', 'Personal', 2, now())
ON CONFLICT (id) DO NOTHING;

INSERT INTO task_projects (id, name, type, description, created_at, updated_at)
VALUES ('personal', 'Personal', 'personal', 'Default personal task project', now(), now())
ON CONFLICT (id) DO NOTHING;
