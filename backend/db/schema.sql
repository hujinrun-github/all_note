PRAGMA journal_mode=WAL;
PRAGMA busy_timeout=5000;
PRAGMA foreign_keys=ON;

CREATE TABLE IF NOT EXISTS folders (
  id TEXT PRIMARY KEY,
  name TEXT UNIQUE NOT NULL,
  sort_order REAL NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);

INSERT OR IGNORE INTO folders (id, name, sort_order, created_at) VALUES
  ('__uncategorized', '未分类', 0, unixepoch()),
  ('__work', '工作', 1, unixepoch()),
  ('__personal', '个人', 2, unixepoch());

CREATE TABLE IF NOT EXISTS notes (
  rowid INTEGER PRIMARY KEY AUTOINCREMENT,
  id TEXT UNIQUE NOT NULL,
  title TEXT NOT NULL,
  body TEXT NOT NULL DEFAULT '',
  folder_id TEXT NOT NULL DEFAULT '__uncategorized' REFERENCES folders(id),
  tags TEXT NOT NULL DEFAULT '[]',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE VIRTUAL TABLE IF NOT EXISTS notes_fts USING fts5(
  title, body, tags, content='notes', content_rowid='rowid'
);

CREATE TRIGGER IF NOT EXISTS notes_ai AFTER INSERT ON notes BEGIN
  INSERT INTO notes_fts(rowid, title, body, tags) VALUES (new.rowid, new.title, new.body, new.tags);
END;

CREATE TRIGGER IF NOT EXISTS notes_ad AFTER DELETE ON notes BEGIN
  INSERT INTO notes_fts(notes_fts, rowid, title, body, tags) VALUES ('delete', old.rowid, old.title, old.body, old.tags);
END;

CREATE TRIGGER IF NOT EXISTS notes_au AFTER UPDATE ON notes BEGIN
  INSERT INTO notes_fts(notes_fts, rowid, title, body, tags) VALUES ('delete', old.rowid, old.title, old.body, old.tags);
  INSERT INTO notes_fts(rowid, title, body, tags) VALUES (new.rowid, new.title, new.body, new.tags);
END;

CREATE TABLE IF NOT EXISTS task_projects (
  id TEXT PRIMARY KEY,
  name TEXT UNIQUE NOT NULL,
  type TEXT NOT NULL DEFAULT 'regular',
  description TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

INSERT OR IGNORE INTO task_projects (id, name, type, description, created_at, updated_at) VALUES
  ('personal', '个人', 'personal', '默认个人任务项目', unixepoch(), unixepoch());

CREATE TABLE IF NOT EXISTS tasks (
  rowid INTEGER PRIMARY KEY AUTOINCREMENT,
  id TEXT UNIQUE NOT NULL,
  title TEXT NOT NULL,
  project TEXT,
  project_id TEXT NOT NULL DEFAULT 'personal' REFERENCES task_projects(id) ON DELETE SET DEFAULT,
  due INTEGER,
  planned_date TEXT,
  priority INTEGER NOT NULL DEFAULT 0,
  done INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL DEFAULT 'open',
  horizon TEXT NOT NULL DEFAULT 'week',
  scope TEXT NOT NULL DEFAULT 'daily',
  sort_order REAL NOT NULL DEFAULT 0,
  note_id TEXT REFERENCES notes(id) ON DELETE SET NULL,
  roadmap_node_id TEXT REFERENCES roadmap_nodes(id) ON DELETE SET NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE VIRTUAL TABLE IF NOT EXISTS tasks_fts USING fts5(
  title, content='tasks', content_rowid='rowid'
);

CREATE TRIGGER IF NOT EXISTS tasks_ai AFTER INSERT ON tasks BEGIN
  INSERT INTO tasks_fts(rowid, title) VALUES (new.rowid, new.title);
END;
CREATE TRIGGER IF NOT EXISTS tasks_ad AFTER DELETE ON tasks BEGIN
  INSERT INTO tasks_fts(tasks_fts, rowid, title) VALUES ('delete', old.rowid, old.title);
END;
CREATE TRIGGER IF NOT EXISTS tasks_au AFTER UPDATE ON tasks BEGIN
  INSERT INTO tasks_fts(tasks_fts, rowid, title) VALUES ('delete', old.rowid, old.title);
  INSERT INTO tasks_fts(rowid, title) VALUES (new.rowid, new.title);
END;

CREATE TABLE IF NOT EXISTS learning_roadmaps (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL UNIQUE REFERENCES task_projects(id) ON DELETE CASCADE,
  title TEXT NOT NULL,
  goal TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'draft',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS roadmap_nodes (
  id TEXT PRIMARY KEY,
  roadmap_id TEXT NOT NULL REFERENCES learning_roadmaps(id) ON DELETE CASCADE,
  parent_id TEXT REFERENCES roadmap_nodes(id) ON DELETE CASCADE,
  type TEXT NOT NULL DEFAULT 'task',
  title TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  path_type TEXT NOT NULL DEFAULT 'required',
  status TEXT NOT NULL DEFAULT 'todo',
  deliverable TEXT NOT NULL DEFAULT '',
  acceptance_criteria TEXT NOT NULL DEFAULT '',
  x REAL NOT NULL DEFAULT 0,
  y REAL NOT NULL DEFAULT 0,
  order_index INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS roadmap_edges (
  id TEXT PRIMARY KEY,
  roadmap_id TEXT NOT NULL REFERENCES learning_roadmaps(id) ON DELETE CASCADE,
  source_node_id TEXT NOT NULL REFERENCES roadmap_nodes(id) ON DELETE CASCADE,
  target_node_id TEXT NOT NULL REFERENCES roadmap_nodes(id) ON DELETE CASCADE,
  style TEXT NOT NULL DEFAULT 'solid',
  created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS roadmap_resources (
  id TEXT PRIMARY KEY,
  node_id TEXT NOT NULL REFERENCES roadmap_nodes(id) ON DELETE CASCADE,
  title TEXT NOT NULL,
  url TEXT NOT NULL,
  summary TEXT NOT NULL DEFAULT '',
  source_type TEXT NOT NULL DEFAULT 'article',
  added_by TEXT NOT NULL DEFAULT 'user',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS events (
  rowid INTEGER PRIMARY KEY AUTOINCREMENT,
  id TEXT UNIQUE NOT NULL,
  title TEXT NOT NULL,
  start_time INTEGER NOT NULL,
  end_time INTEGER NOT NULL,
  location TEXT,
  kind TEXT NOT NULL DEFAULT 'work',
  note_id TEXT REFERENCES notes(id) ON DELETE SET NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE VIRTUAL TABLE IF NOT EXISTS events_fts USING fts5(
  title, location, content='events', content_rowid='rowid'
);

CREATE TRIGGER IF NOT EXISTS events_ai AFTER INSERT ON events BEGIN
  INSERT INTO events_fts(rowid, title, location) VALUES (new.rowid, new.title, new.location);
END;
CREATE TRIGGER IF NOT EXISTS events_ad AFTER DELETE ON events BEGIN
  INSERT INTO events_fts(events_fts, rowid, title, location) VALUES ('delete', old.rowid, old.title, old.location);
END;
CREATE TRIGGER IF NOT EXISTS events_au AFTER UPDATE ON events BEGIN
  INSERT INTO events_fts(events_fts, rowid, title, location) VALUES ('delete', old.rowid, old.title, old.location);
  INSERT INTO events_fts(rowid, title, location) VALUES (new.rowid, new.title, new.location);
END;

CREATE TABLE IF NOT EXISTS inbox (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL,
  title TEXT NOT NULL,
  body TEXT,
  source TEXT NOT NULL DEFAULT 'quick-capture',
  archived INTEGER NOT NULL DEFAULT 0,
  converted_to TEXT,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS sync_targets (
  id TEXT PRIMARY KEY,
  type TEXT NOT NULL,
  name TEXT NOT NULL,
  vault_path TEXT NOT NULL,
  base_folder TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  auto_sync INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS note_sync_state (
  note_id TEXT NOT NULL,
  target_id TEXT NOT NULL,
  external_path TEXT NOT NULL,
  content_hash TEXT NOT NULL,
  external_hash TEXT,
  external_mtime INTEGER,
  last_direction TEXT,
  last_synced_at INTEGER,
  status TEXT NOT NULL,
  error_message TEXT,
  PRIMARY KEY (note_id, target_id),
  FOREIGN KEY (note_id) REFERENCES notes(id) ON DELETE CASCADE,
  FOREIGN KEY (target_id) REFERENCES sync_targets(id) ON DELETE CASCADE
);
