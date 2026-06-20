# FlowSpace Web App Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a runnable FlowSpace web application with Go+Gin+SQLite backend and Vite+React 19+TypeScript+Tailwind frontend.

**Architecture:** Single-page React app communicates with Go REST API via TanStack Query. Backend uses handler→service→repository layering with SQLite+FTS5 for storage and full-text search. All data requests go through TanStack Query hooks in route components.

**Tech Stack:** Go, Gin, modernc.org/sqlite, React 19, TypeScript, Vite, Tailwind CSS, React Router v7, TanStack Query, Zustand, Tiptap

**Spec:** `docs/superpowers/specs/2026-05-27-flowspace-web-design.md`

---

## Phase 1: Backend Foundation

### Task 1: Initialize Go project

**Files:**
- Create: `backend/go.mod`
- Create: `backend/Makefile`
- Create: `backend/.gitignore`

- [ ] **Step 1: Create backend directory and init Go module**

```bash
mkdir -p backend
cd backend
go mod init github.com/hujinrun/flowspace
```

- [ ] **Step 2: Write Makefile**

```makefile
# backend/Makefile
.PHONY: dev build test lint seed

dev:
	go run ./cmd/server/main.go

build:
	CGO_ENABLED=0 go build -o bin/server ./cmd/server/main.go

test:
	go test ./internal/... -cover -count=1

lint:
	go vet ./...
	staticcheck ./...

seed:
	go run ./cmd/seed/main.go
```

- [ ] **Step 3: Write .gitignore**

```
# backend/.gitignore
bin/
*.db
*.db-wal
*.db-shm
```

- [ ] **Step 4: Commit**

```bash
git add backend/go.mod backend/Makefile backend/.gitignore
git commit -m "feat: init Go project with Makefile and .gitignore"
```

### Task 2: Database schema and seed data

**Files:**
- Create: `backend/db/schema.sql`
- Create: `backend/db/seed.sql`

- [ ] **Step 1: Write schema.sql with all DDL**

```sql
-- backend/db/schema.sql
PRAGMA journal_mode=WAL;
PRAGMA busy_timeout=5000;
PRAGMA foreign_keys=ON;

CREATE TABLE IF NOT EXISTS folders (
  id TEXT PRIMARY KEY,
  name TEXT UNIQUE NOT NULL,
  sort_order REAL NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);

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

CREATE TABLE IF NOT EXISTS tasks (
  rowid INTEGER PRIMARY KEY AUTOINCREMENT,
  id TEXT UNIQUE NOT NULL,
  title TEXT NOT NULL,
  project TEXT,
  due INTEGER,
  priority INTEGER NOT NULL DEFAULT 0,
  done INTEGER NOT NULL DEFAULT 0,
  scope TEXT NOT NULL DEFAULT 'daily',
  sort_order REAL NOT NULL DEFAULT 0,
  note_id TEXT REFERENCES notes(id) ON DELETE SET NULL,
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
```

- [ ] **Step 2: Write seed.sql**

```sql
-- backend/db/seed.sql
INSERT OR IGNORE INTO folders (id, name, sort_order, created_at) VALUES
  ('__uncategorized', '未分类', 0, unixepoch()),
  ('__work', '工作', 1, unixepoch()),
  ('__personal', '个人', 2, unixepoch());

-- 10 notes
INSERT OR IGNORE INTO notes (id, title, body, folder_id, tags, created_at, updated_at) VALUES
  ('n01', '桌面端架构设计', 'Tiptap 自定义节点的能力是关联引擎的 UI 基础，需要优先验证…', '__work', '["技术","架构"]', unixepoch(), unixepoch()),
  ('n02', '产品规划文档', 'MVP 阶段聚焦快速捕获、Markdown 编辑、智能关联三个核心…', '__work', '["产品","规划"]', unixepoch()-86400, unixepoch()-86400),
  ('n03', '周报 5月第三周', '本周完成了设计规范合并和组件库初版，下周启动原型开发…', '__work', '["周报"]', unixepoch()-172800, unixepoch()-172800),
  ('n04', '读书笔记：深度工作', 'Cal Newport 提出的深度工作概念与 FlowSpace 的产品理念高度契合…', '__personal', '["阅读","笔记"]', unixepoch()-259200, unixepoch()-259200),
  ('n05', 'CalDAV 集成调研', '评估 iCloud、Google Calendar 的 CalDAV 接入方案…', '__work', '["技术","调研"]', unixepoch()-345600, unixepoch()-345600),
  ('n06', '用户访谈记录 #3', '与 3 位自由职业者进行了 45 分钟的一对一访谈…', '__work', '["用户研究","访谈"]', unixepoch()-432000, unixepoch()-432000),
  ('n07', 'React 19 迁移笔记', 'React 19 的 use() hook 和 actions API 可以简化数据获取流程…', '__work', '["技术","前端"]', unixepoch()-518400, unixepoch()-518400),
  ('n08', '个人年度目标', '2026 年目标：完成 FlowSpace v1、读 24 本书、跑一次马拉松…', '__personal', '["规划","个人"]', unixepoch()-604800, unixepoch()-604800),
  ('n09', 'SQLite FTS5 使用笔记', 'FTS5 的 content= 模式要求源表必须有 INTEGER rowid…', '__work', '["技术","数据库"]', unixepoch()-691200, unixepoch()-691200),
  ('n10', 'Go Gin 中间件最佳实践', 'Logger→Recovery→CORS 是 Gin 推荐的中间件链…', '__work', '["技术","后端"]', unixepoch()-777600, unixepoch()-777600);

-- 10 tasks
INSERT OR IGNORE INTO tasks (id, title, project, due, priority, done, scope, sort_order, created_at, updated_at) VALUES
  ('t01', '完成 Tiptap 原型验证', '项目A', unixepoch('2026-05-30'), 1, 0, 'daily', 0, unixepoch(), unixepoch()),
  ('t02', '审查桌面端架构文档', '技术', unixepoch('2026-05-30'), 0, 0, 'daily', 1, unixepoch(), unixepoch()),
  ('t03', '整理本周笔记', '个人', unixepoch('2026-05-29'), 0, 1, 'daily', 2, unixepoch(), unixepoch()),
  ('t04', '准备周五评审材料', '工作', unixepoch('2026-06-15'), 0, 0, 'monthly', 3, unixepoch(), unixepoch()),
  ('t05', '更新产品规划文档', '项目A', unixepoch('2026-06-20'), 0, 1, 'monthly', 4, unixepoch(), unixepoch()),
  ('t06', '完成本地优先数据层设计', '技术', unixepoch('2026-09-30'), 1, 0, 'yearly', 5, unixepoch(), unixepoch()),
  ('t07', '实现 Quick Capture 接口', '技术', unixepoch('2026-05-31'), 1, 0, 'daily', 6, unixepoch(), unixepoch()),
  ('t08', '编写 API 文档', '项目A', unixepoch('2026-05-22'), 0, 0, 'daily', 7, unixepoch(), unixepoch()),
  ('t09', '数据库 schema 评审', '技术', unixepoch('2026-05-23'), 0, 0, 'daily', 8, unixepoch(), unixepoch()),
  ('t10', '前端路由设计', '项目A', unixepoch('2026-05-24'), 1, 0, 'daily', 9, unixepoch(), unixepoch());

-- 5 events
INSERT OR IGNORE INTO events (id, title, start_time, end_time, location, kind, created_at, updated_at) VALUES
  ('e01', '产品评审会', unixepoch('2026-05-30 10:00:00'), unixepoch('2026-05-30 11:00:00'), '会议室 3F', 'work', unixepoch(), unixepoch()),
  ('e02', '团队周会', unixepoch('2026-05-30 14:00:00'), unixepoch('2026-05-30 14:30:00'), NULL, 'reminder', unixepoch(), unixepoch()),
  ('e03', '夜跑', unixepoch('2026-05-30 19:00:00'), unixepoch('2026-05-30 20:00:00'), NULL, 'personal', unixepoch(), unixepoch()),
  ('e04', '技术分享会', unixepoch('2026-05-20 15:00:00'), unixepoch('2026-06-05 16:00:00'), '线上', 'work', unixepoch(), unixepoch()),
  ('e05', '周末 hiking', unixepoch('2026-06-01 08:00:00'), unixepoch('2026-06-01 17:00:00'), '森林公园', 'personal', unixepoch(), unixepoch());

-- 3 inbox items
INSERT OR IGNORE INTO inbox (id, kind, title, body, source, created_at, updated_at) VALUES
  ('i01', 'task', '调研 CalDAV 集成方案', '评估 iCloud/Google Calendar 的 CalDAV 接入复杂度', 'quick-capture', unixepoch()-600, unixepoch()-600),
  ('i02', 'note', '会议记录 — 产品方向讨论', '讨论了 v1.1 日历视图的交互方案和拖拽调整时间的实现…', '编辑器', unixepoch()-3600, unixepoch()-3600),
  ('i03', 'event', '周五团队午餐', NULL, 'quick-capture', unixepoch()-86400, unixepoch()-86400);
```

- [ ] **Step 3: Commit**

```bash
git add backend/db/schema.sql backend/db/seed.sql
git commit -m "feat: add database schema with FTS5 and seed data"
```

### Task 3: Model definitions

**Files:**
- Create: `backend/internal/model/folder.go`
- Create: `backend/internal/model/note.go`
- Create: `backend/internal/model/task.go`
- Create: `backend/internal/model/event.go`
- Create: `backend/internal/model/inbox.go`
- Create: `backend/internal/model/search.go`
- Create: `backend/internal/model/common.go`

- [ ] **Step 1: Write common response types**

```go
// backend/internal/model/common.go
package model

type Pagination struct {
	Page     int `json:"page"`
	PageSize int `json:"page_size"`
	Total    int `json:"total"`
}

type APIResponse struct {
	Data       interface{} `json:"data,omitempty"`
	Pagination *Pagination `json:"pagination,omitempty"`
	Error      *APIError   `json:"error,omitempty"`
}

type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
```

- [ ] **Step 2: Write folder model**

```go
// backend/internal/model/folder.go
package model

type Folder struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	SortOrder float64 `json:"sort_order"`
	NoteCount int    `json:"note_count"`
	CreatedAt int64  `json:"created_at"`
}
```

- [ ] **Step 3: Write note model**

```go
// backend/internal/model/note.go
package model

type Note struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	FolderID  string `json:"folder_id"`
	Tags      string `json:"tags"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

type CreateNoteRequest struct {
	Title    string `json:"title" binding:"required"`
	Body     string `json:"body"`
	FolderID string `json:"folder_id"`
	Tags     string `json:"tags"`
}

type UpdateNoteRequest struct {
	Title    *string `json:"title"`
	Body     *string `json:"body"`
	FolderID *string `json:"folder_id"`
	Tags     *string `json:"tags"`
}
```

- [ ] **Step 4: Write task model**

```go
// backend/internal/model/task.go
package model

type Task struct {
	ID        string  `json:"id"`
	Title     string  `json:"title"`
	Project   *string `json:"project"`
	Due       *int64  `json:"due"`
	Priority  int     `json:"priority"`
	Done      int     `json:"done"`
	Scope     string  `json:"scope"`
	SortOrder float64 `json:"sort_order"`
	NoteID    *string `json:"note_id"`
	CreatedAt int64   `json:"created_at"`
	UpdatedAt int64   `json:"updated_at"`
}

type CreateTaskRequest struct {
	Title    string  `json:"title" binding:"required"`
	Project  *string `json:"project"`
	Due      *int64  `json:"due"`
	Priority int     `json:"priority"`
	Scope    string  `json:"scope"`
}

type UpdateTaskRequest struct {
	Title     *string  `json:"title"`
	Project   *string  `json:"project"`
	Due       *int64   `json:"due"`
	Priority  *int     `json:"priority"`
	Done      *int     `json:"done"`
	Scope     *string  `json:"scope"`
	SortOrder *float64 `json:"sort_order"`
}
```

- [ ] **Step 5: Write event model**

```go
// backend/internal/model/event.go
package model

type Event struct {
	ID        string  `json:"id"`
	Title     string  `json:"title"`
	StartTime int64   `json:"start_time"`
	EndTime   int64   `json:"end_time"`
	Location  *string `json:"location"`
	Kind      string  `json:"kind"`
	NoteID    *string `json:"note_id"`
	CreatedAt int64   `json:"created_at"`
	UpdatedAt int64   `json:"updated_at"`
}

type CreateEventRequest struct {
	Title     string  `json:"title" binding:"required"`
	StartTime int64   `json:"start_time" binding:"required"`
	EndTime   int64   `json:"end_time" binding:"required"`
	Location  *string `json:"location"`
	Kind      string  `json:"kind"`
}

type UpdateEventRequest struct {
	Title     *string `json:"title"`
	StartTime *int64  `json:"start_time"`
	EndTime   *int64  `json:"end_time"`
	Location  *string `json:"location"`
	Kind      *string `json:"kind"`
}
```

- [ ] **Step 6: Write inbox model**

```go
// backend/internal/model/inbox.go
package model

type InboxItem struct {
	ID          string  `json:"id"`
	Kind        string  `json:"kind"`
	Title       string  `json:"title"`
	Body        *string `json:"body"`
	Source      string  `json:"source"`
	Archived    int     `json:"archived"`
	ConvertedTo *string `json:"converted_to"`
	CreatedAt   int64   `json:"created_at"`
	UpdatedAt   int64   `json:"updated_at"`
}

type CreateInboxRequest struct {
	Kind  string  `json:"kind" binding:"required"`
	Title string  `json:"title" binding:"required"`
	Body  *string `json:"body"`
}

type ConvertInboxRequest struct {
	Kind string `json:"kind" binding:"required"`
}

type BatchInboxRequest struct {
	IDs    []string `json:"ids" binding:"required"`
	Action string   `json:"action" binding:"required"`
}
```

- [ ] **Step 7: Write search model**

```go
// backend/internal/model/search.go
package model

type SearchResult struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Title     string `json:"title"`
	Highlight string `json:"highlight"`
	// note-only fields
	FolderID *string `json:"folder_id,omitempty"`
	// task-only fields
	Done *int `json:"done,omitempty"`
	// event-only fields
	Kind *string `json:"kind,omitempty"`
	// common
	UpdatedAt int64 `json:"updated_at"`
}
```

- [ ] **Step 8: Commit**

```bash
git add backend/internal/model/
git commit -m "feat: add model definitions with JSON tags and validation binding"
```

### Task 4: Database connection and repository layer

**Files:**
- Create: `backend/internal/repository/db.go`
- Create: `backend/internal/repository/folders.go`
- Create: `backend/internal/repository/notes.go`
- Create: `backend/internal/repository/tasks.go`
- Create: `backend/internal/repository/events.go`
- Create: `backend/internal/repository/inbox.go`
- Create: `backend/internal/repository/search.go`

- [ ] **Step 1: Write DB connection with schema initialization**

```go
// backend/internal/repository/db.go
package repository

import (
	"database/sql"
	"os"

	_ "modernc.org/sqlite"
)

var DB *sql.DB

func InitDB(dbPath string) error {
	var err error
	DB, err = sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=ON")
	if err != nil {
		return err
	}
	DB.SetMaxOpenConns(1) // SQLite single-writer

	schema, err := os.ReadFile("db/schema.sql")
	if err != nil {
		return err
	}
	_, err = DB.Exec(string(schema))
	return err
}

func SeedDB() error {
	seed, err := os.ReadFile("db/seed.sql")
	if err != nil {
		return err
	}
	_, err = DB.Exec(string(seed))
	return err
}
```

- [ ] **Step 2: Write folders repository**

```go
// backend/internal/repository/folders.go
package repository

import "github.com/hujinrun/flowspace/internal/model"

func GetFolders() ([]model.Folder, error) {
	rows, err := DB.Query(`
		SELECT f.id, f.name, f.sort_order, COUNT(n.rowid) as note_count, f.created_at
		FROM folders f
		LEFT JOIN notes n ON n.folder_id = f.id
		GROUP BY f.id
		ORDER BY f.sort_order ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var folders []model.Folder
	for rows.Next() {
		var f model.Folder
		if err := rows.Scan(&f.ID, &f.Name, &f.SortOrder, &f.NoteCount, &f.CreatedAt); err != nil {
			return nil, err
		}
		folders = append(folders, f)
	}
	return folders, nil
}
```

- [ ] **Step 3: Write notes repository**

```go
// backend/internal/repository/notes.go
package repository

import (
	"fmt"
	"strings"

	"github.com/hujinrun/flowspace/internal/model"
)

func GetNotes(folderID, sort string, page, pageSize int) ([]model.Note, int, error) {
	where := "1=1"
	args := []interface{}{}
	if folderID != "" {
		where = "n.folder_id = ?"
		args = append(args, folderID)
	}

	var total int
	DB.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM notes n WHERE %s", where), args...).Scan(&total)

	order := "n.created_at DESC"
	if sort == "az" {
		order = "n.title ASC"
	}

	offset := (page - 1) * pageSize
	query := fmt.Sprintf(`
		SELECT n.id, n.title, n.body, n.folder_id, n.tags, n.created_at, n.updated_at
		FROM notes n WHERE %s ORDER BY %s LIMIT ? OFFSET ?
	`, where, order)
	args = append(args, pageSize, offset)

	rows, err := DB.Query(query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var notes []model.Note
	for rows.Next() {
		var n model.Note
		if err := rows.Scan(&n.ID, &n.Title, &n.Body, &n.FolderID, &n.Tags, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, 0, err
		}
		notes = append(notes, n)
	}
	return notes, total, nil
}

func GetNoteByID(id string) (*model.Note, error) {
	var n model.Note
	err := DB.QueryRow(`
		SELECT id, title, body, folder_id, tags, created_at, updated_at
		FROM notes WHERE id = ?
	`, id).Scan(&n.ID, &n.Title, &n.Body, &n.FolderID, &n.Tags, &n.CreatedAt, &n.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &n, nil
}

func CreateNote(n *model.Note) error {
	n.ID = newUUID()
	now := nowUnix()
	n.CreatedAt = now
	n.UpdatedAt = now
	if n.FolderID == "" {
		n.FolderID = "__uncategorized"
	}
	if n.Tags == "" {
		n.Tags = "[]"
	}
	_, err := DB.Exec(`
		INSERT INTO notes (id, title, body, folder_id, tags, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, n.ID, n.Title, n.Body, n.FolderID, n.Tags, n.CreatedAt, n.UpdatedAt)
	return err
}

func UpdateNote(id string, req *model.UpdateNoteRequest) (*model.Note, error) {
	sets := []string{"updated_at = ?"}
	args := []interface{}{nowUnix()}

	if req.Title != nil {
		sets = append(sets, "title = ?")
		args = append(args, *req.Title)
	}
	if req.Body != nil {
		sets = append(sets, "body = ?")
		args = append(args, *req.Body)
	}
	if req.FolderID != nil {
		sets = append(sets, "folder_id = ?")
		args = append(args, *req.FolderID)
	}
	if req.Tags != nil {
		sets = append(sets, "tags = ?")
		args = append(args, *req.Tags)
	}

	args = append(args, id)
	_, err := DB.Exec(fmt.Sprintf("UPDATE notes SET %s WHERE id = ?", strings.Join(sets, ", ")), args...)
	if err != nil {
		return nil, err
	}
	return GetNoteByID(id)
}

func DeleteNote(id string) error {
	_, err := DB.Exec("DELETE FROM notes WHERE id = ?", id)
	return err
}

func GetRecentNotes(limit int) ([]model.Note, error) {
	rows, err := DB.Query(`
		SELECT id, title, body, folder_id, tags, created_at, updated_at
		FROM notes ORDER BY updated_at DESC LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var notes []model.Note
	for rows.Next() {
		var n model.Note
		if err := rows.Scan(&n.ID, &n.Title, &n.Body, &n.FolderID, &n.Tags, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, err
		}
		notes = append(notes, n)
	}
	return notes, nil
}
```

- [ ] **Step 4: Write tasks repository**

```go
// backend/internal/repository/tasks.go
package repository

import (
	"fmt"
	"strings"

	"github.com/hujinrun/flowspace/internal/model"
)

func GetTasks(project, status, scope string, page, pageSize int) ([]model.Task, int, error) {
	where := []string{"1=1"}
	args := []interface{}{}

	if project != "" {
		where = append(where, "t.project = ?")
		args = append(args, project)
	}
	if status == "active" {
		where = append(where, "t.done = 0")
	} else if status == "done" {
		where = append(where, "t.done = 1")
	}
	if scope != "" {
		where = append(where, "t.scope = ?")
		args = append(args, scope)
	}

	whereClause := strings.Join(where, " AND ")

	var total int
	DB.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM tasks t WHERE %s", whereClause), args...).Scan(&total)

	offset := (page - 1) * pageSize
	query := fmt.Sprintf(`
		SELECT t.id, t.title, t.project, t.due, t.priority, t.done, t.scope, t.sort_order, t.note_id, t.created_at, t.updated_at
		FROM tasks t WHERE %s ORDER BY t.sort_order ASC, t.created_at DESC LIMIT ? OFFSET ?
	`, whereClause)
	args = append(args, pageSize, offset)

	rows, err := DB.Query(query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var tasks []model.Task
	for rows.Next() {
		var t model.Task
		if err := rows.Scan(&t.ID, &t.Title, &t.Project, &t.Due, &t.Priority, &t.Done, &t.Scope, &t.SortOrder, &t.NoteID, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, 0, err
		}
		tasks = append(tasks, t)
	}
	return tasks, total, nil
}

func CreateTask(t *model.Task) error {
	t.ID = newUUID()
	now := nowUnix()
	t.CreatedAt = now
	t.UpdatedAt = now
	if t.Scope == "" {
		t.Scope = "daily"
	}
	_, err := DB.Exec(`
		INSERT INTO tasks (id, title, project, due, priority, done, scope, sort_order, note_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, t.ID, t.Title, t.Project, t.Due, t.Priority, t.Done, t.Scope, t.SortOrder, t.NoteID, t.CreatedAt, t.UpdatedAt)
	return err
}

func UpdateTask(id string, req *model.UpdateTaskRequest) (*model.Task, error) {
	sets := []string{"updated_at = ?"}
	args := []interface{}{nowUnix()}

	if req.Title != nil {
		sets = append(sets, "title = ?")
		args = append(args, *req.Title)
	}
	if req.Project != nil {
		sets = append(sets, "project = ?")
		args = append(args, *req.Project)
	}
	if req.Due != nil {
		sets = append(sets, "due = ?")
		args = append(args, *req.Due)
	}
	if req.Priority != nil {
		sets = append(sets, "priority = ?")
		args = append(args, *req.Priority)
	}
	if req.Done != nil {
		sets = append(sets, "done = ?")
		args = append(args, *req.Done)
	}
	if req.Scope != nil {
		sets = append(sets, "scope = ?")
		args = append(args, *req.Scope)
	}
	if req.SortOrder != nil {
		sets = append(sets, "sort_order = ?")
		args = append(args, *req.SortOrder)
	}

	args = append(args, id)
	_, err := DB.Exec(fmt.Sprintf("UPDATE tasks SET %s WHERE id = ?", strings.Join(sets, ", ")), args...)
	if err != nil {
		return nil, err
	}
	return GetTaskByID(id)
}

func GetTaskByID(id string) (*model.Task, error) {
	var t model.Task
	err := DB.QueryRow(`
		SELECT id, title, project, due, priority, done, scope, sort_order, note_id, created_at, updated_at
		FROM tasks WHERE id = ?
	`, id).Scan(&t.ID, &t.Title, &t.Project, &t.Due, &t.Priority, &t.Done, &t.Scope, &t.SortOrder, &t.NoteID, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func DeleteTask(id string) error {
	_, err := DB.Exec("DELETE FROM tasks WHERE id = ?", id)
	return err
}

func GetTodayTasks(todayStart, todayEnd, overdueCutoff int64) ([]model.Task, []model.Task, error) {
	// Today tasks: due within today
	rows, err := DB.Query(`
		SELECT id, title, project, due, priority, done, scope, sort_order, note_id, created_at, updated_at
		FROM tasks WHERE done = 0 AND due >= ? AND due < ? ORDER BY sort_order ASC, created_at DESC
	`, todayStart, todayEnd)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	todayTasks := scanTasks(rows)

	// Overdue tasks: due < today AND due >= cutoff (OVERDUE_WINDOW_DAYS ago)
	rows2, err := DB.Query(`
		SELECT id, title, project, due, priority, done, scope, sort_order, note_id, created_at, updated_at
		FROM tasks WHERE done = 0 AND due < ? AND due >= ? ORDER BY due ASC LIMIT 10
	`, todayStart, overdueCutoff)
	if err != nil {
		return nil, nil, err
	}
	defer rows2.Close()
	overdueTasks := scanTasks(rows2)

	return todayTasks, overdueTasks, nil
}

func scanTasks(rows *sql.Rows) []model.Task {
	var tasks []model.Task
	for rows.Next() {
		var t model.Task
		rows.Scan(&t.ID, &t.Title, &t.Project, &t.Due, &t.Priority, &t.Done, &t.Scope, &t.SortOrder, &t.NoteID, &t.CreatedAt, &t.UpdatedAt)
		tasks = append(tasks, t)
	}
	return tasks
}
```

- [ ] **Step 5: Write events repository**

```go
// backend/internal/repository/events.go
package repository

import (
	"fmt"
	"strings"

	"github.com/hujinrun/flowspace/internal/model"
)

func GetEvents(monthStart, monthEnd int64, page, pageSize int) ([]model.Event, int, error) {
	var total int
	DB.QueryRow(`
		SELECT COUNT(*) FROM events
		WHERE start_time < ? AND end_time > ?
	`, monthEnd, monthStart).Scan(&total)

	offset := (page - 1) * pageSize
	rows, err := DB.Query(`
		SELECT id, title, start_time, end_time, location, kind, note_id, created_at, updated_at
		FROM events
		WHERE start_time < ? AND end_time > ?
		ORDER BY start_time ASC LIMIT ? OFFSET ?
	`, monthEnd, monthStart, pageSize, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var events []model.Event
	for rows.Next() {
		var e model.Event
		if err := rows.Scan(&e.ID, &e.Title, &e.StartTime, &e.EndTime, &e.Location, &e.Kind, &e.NoteID, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, 0, err
		}
		events = append(events, e)
	}
	return events, total, nil
}

func CreateEvent(e *model.Event) error {
	e.ID = newUUID()
	now := nowUnix()
	e.CreatedAt = now
	e.UpdatedAt = now
	if e.Kind == "" {
		e.Kind = "work"
	}
	_, err := DB.Exec(`
		INSERT INTO events (id, title, start_time, end_time, location, kind, note_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, e.ID, e.Title, e.StartTime, e.EndTime, e.Location, e.Kind, e.NoteID, e.CreatedAt, e.UpdatedAt)
	return err
}

func UpdateEvent(id string, req *model.UpdateEventRequest) (*model.Event, error) {
	sets := []string{"updated_at = ?"}
	args := []interface{}{nowUnix()}

	if req.Title != nil {
		sets = append(sets, "title = ?")
		args = append(args, *req.Title)
	}
	if req.StartTime != nil {
		sets = append(sets, "start_time = ?")
		args = append(args, *req.StartTime)
	}
	if req.EndTime != nil {
		sets = append(sets, "end_time = ?")
		args = append(args, *req.EndTime)
	}
	if req.Location != nil {
		sets = append(sets, "location = ?")
		args = append(args, *req.Location)
	}
	if req.Kind != nil {
		sets = append(sets, "kind = ?")
		args = append(args, *req.Kind)
	}

	args = append(args, id)
	_, err := DB.Exec(fmt.Sprintf("UPDATE events SET %s WHERE id = ?", strings.Join(sets, ", ")), args...)
	if err != nil {
		return nil, err
	}
	return GetEventByID(id)
}

func GetEventByID(id string) (*model.Event, error) {
	var e model.Event
	err := DB.QueryRow(`
		SELECT id, title, start_time, end_time, location, kind, note_id, created_at, updated_at
		FROM events WHERE id = ?
	`, id).Scan(&e.ID, &e.Title, &e.StartTime, &e.EndTime, &e.Location, &e.Kind, &e.NoteID, &e.CreatedAt, &e.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func DeleteEvent(id string) error {
	_, err := DB.Exec("DELETE FROM events WHERE id = ?", id)
	return err
}

func GetTodayEvents(todayStart, todayEnd int64) ([]model.Event, error) {
	rows, err := DB.Query(`
		SELECT id, title, start_time, end_time, location, kind, note_id, created_at, updated_at
		FROM events WHERE start_time < ? AND end_time > ? ORDER BY start_time ASC
	`, todayEnd, todayStart)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []model.Event
	for rows.Next() {
		var e model.Event
		rows.Scan(&e.ID, &e.Title, &e.StartTime, &e.EndTime, &e.Location, &e.Kind, &e.NoteID, &e.CreatedAt, &e.UpdatedAt)
		events = append(events, e)
	}
	return events, nil
}
```

- [ ] **Step 6: Write inbox repository**

```go
// backend/internal/repository/inbox.go
package repository

import (
	"fmt"
	"strings"

	"github.com/hujinrun/flowspace/internal/model"
)

func GetInboxItems(kind string, page, pageSize int) ([]model.InboxItem, int, error) {
	where := "archived = 0 AND converted_to IS NULL"
	args := []interface{}{}
	if kind != "" && kind != "all" {
		where += " AND kind = ?"
		args = append(args, kind)
	}

	var total int
	DB.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM inbox WHERE %s", where), args...).Scan(&total)

	offset := (page - 1) * pageSize
	rows, err := DB.Query(fmt.Sprintf(`
		SELECT id, kind, title, body, source, archived, converted_to, created_at, updated_at
		FROM inbox WHERE %s ORDER BY created_at DESC LIMIT ? OFFSET ?
	`, where), append(args, pageSize, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var items []model.InboxItem
	for rows.Next() {
		var it model.InboxItem
		if err := rows.Scan(&it.ID, &it.Kind, &it.Title, &it.Body, &it.Source, &it.Archived, &it.ConvertedTo, &it.CreatedAt, &it.UpdatedAt); err != nil {
			return nil, 0, err
		}
		items = append(items, it)
	}
	return items, total, nil
}

func CreateInboxItem(it *model.InboxItem) error {
	it.ID = newUUID()
	now := nowUnix()
	it.CreatedAt = now
	it.UpdatedAt = now
	if it.Source == "" {
		it.Source = "quick-capture"
	}
	_, err := DB.Exec(`
		INSERT INTO inbox (id, kind, title, body, source, archived, converted_to, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, it.ID, it.Kind, it.Title, it.Body, it.Source, it.Archived, it.ConvertedTo, it.CreatedAt, it.UpdatedAt)
	return err
}

func GetInboxItemByID(id string) (*model.InboxItem, error) {
	var it model.InboxItem
	err := DB.QueryRow(`
		SELECT id, kind, title, body, source, archived, converted_to, created_at, updated_at
		FROM inbox WHERE id = ?
	`, id).Scan(&it.ID, &it.Kind, &it.Title, &it.Body, &it.Source, &it.Archived, &it.ConvertedTo, &it.CreatedAt, &it.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &it, nil
}

func MarkInboxConverted(id, convertedTo string) error {
	_, err := DB.Exec("UPDATE inbox SET converted_to = ?, updated_at = ? WHERE id = ?", convertedTo, nowUnix(), id)
	return err
}

func DeleteInboxItem(id string) error {
	_, err := DB.Exec("DELETE FROM inbox WHERE id = ?", id)
	return err
}

func BatchArchiveInbox(ids []string) (int64, error) {
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]interface{}, len(ids)+1)
	args[0] = nowUnix()
	for i, id := range ids {
		args[i+1] = id
	}
	result, err := DB.Exec(fmt.Sprintf("UPDATE inbox SET archived = 1, updated_at = ? WHERE id IN (%s)", placeholders), args...)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func BatchDeleteInbox(ids []string) (int64, error) {
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	result, err := DB.Exec(fmt.Sprintf("DELETE FROM inbox WHERE id IN (%s)", placeholders), args...)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
```

- [ ] **Step 7: Write search repository**

```go
// backend/internal/repository/search.go
package repository

import (
	"sort"
	"strings"

	"github.com/hujinrun/flowspace/internal/model"
)

const searchPageMultiplier = 3

func Search(q string, page, pageSize int) ([]model.SearchResult, int, error) {
	if strings.TrimSpace(q) == "" {
		return []model.SearchResult{}, 0, nil
	}

	ftsQuery := buildFTS5Query(q)
	limit := pageSize * searchPageMultiplier
	var allResults []model.SearchResult
	totalCount := 0

	// Notes search
	noteResults, noteCount := searchNotes(ftsQuery, limit)
	totalCount += noteCount
	allResults = append(allResults, noteResults...)

	// Tasks search
	taskResults, taskCount := searchTasks(ftsQuery, limit)
	totalCount += taskCount
	allResults = append(allResults, taskResults...)

	// Events search
	eventResults, eventCount := searchEvents(ftsQuery, limit)
	totalCount += eventCount
	allResults = append(allResults, eventResults...)

	// Merge sort by updated_at DESC
	sort.Slice(allResults, func(i, j int) bool {
		return allResults[i].UpdatedAt > allResults[j].UpdatedAt
	})

	// Slice for requested page
	start := (page - 1) * pageSize
	if start > len(allResults) {
		return []model.SearchResult{}, totalCount, nil
	}
	end := start + pageSize
	if end > len(allResults) {
		end = len(allResults)
	}

	return allResults[start:end], totalCount, nil
}

func buildFTS5Query(q string) string {
	q = strings.TrimSpace(q)
	if strings.Contains(q, "\"") {
		return q
	}
	words := strings.Fields(q)
	for i, w := range words {
		words[i] = "\"" + w + "\""
	}
	return strings.Join(words, " AND ")
}

func searchNotes(q string, limit int) ([]model.SearchResult, int) {
	var total int
	DB.QueryRow("SELECT COUNT(*) FROM notes_fts WHERE notes_fts MATCH ?", q).Scan(&total)

	rows, err := DB.Query(`
		SELECT n.id, n.title, snippet(notes_fts, 1, '<mark>', '</mark>', '...', 40) as highlight,
		       n.folder_id, n.updated_at
		FROM notes_fts
		JOIN notes n ON n.rowid = notes_fts.rowid
		WHERE notes_fts MATCH ?
		ORDER BY n.updated_at DESC LIMIT ?
	`, q, limit)
	if err != nil {
		return nil, 0
	}
	defer rows.Close()

	var results []model.SearchResult
	for rows.Next() {
		var r model.SearchResult
		r.Type = "note"
		rows.Scan(&r.ID, &r.Title, &r.Highlight, &r.FolderID, &r.UpdatedAt)
		results = append(results, r)
	}
	return results, total
}

func searchTasks(q string, limit int) ([]model.SearchResult, int) {
	var total int
	DB.QueryRow("SELECT COUNT(*) FROM tasks_fts WHERE tasks_fts MATCH ?", q).Scan(&total)

	rows, err := DB.Query(`
		SELECT t.id, t.title, snippet(tasks_fts, 0, '<mark>', '</mark>', '...', 40) as highlight,
		       t.done, t.updated_at
		FROM tasks_fts
		JOIN tasks t ON t.rowid = tasks_fts.rowid
		WHERE tasks_fts MATCH ?
		ORDER BY t.updated_at DESC LIMIT ?
	`, q, limit)
	if err != nil {
		return nil, 0
	}
	defer rows.Close()

	var results []model.SearchResult
	for rows.Next() {
		var r model.SearchResult
		r.Type = "task"
		rows.Scan(&r.ID, &r.Title, &r.Highlight, &r.Done, &r.UpdatedAt)
		results = append(results, r)
	}
	return results, total
}

func searchEvents(q string, limit int) ([]model.SearchResult, int) {
	var total int
	DB.QueryRow("SELECT COUNT(*) FROM events_fts WHERE events_fts MATCH ?", q).Scan(&total)

	rows, err := DB.Query(`
		SELECT e.id, e.title, snippet(events_fts, 0, '<mark>', '</mark>', '...', 40) as highlight,
		       e.kind, e.updated_at
		FROM events_fts
		JOIN events e ON e.rowid = events_fts.rowid
		WHERE events_fts MATCH ?
		ORDER BY e.updated_at DESC LIMIT ?
	`, q, limit)
	if err != nil {
		return nil, 0
	}
	defer rows.Close()

	var results []model.SearchResult
	for rows.Next() {
		var r model.SearchResult
		r.Type = "event"
		rows.Scan(&r.ID, &r.Title, &r.Highlight, &r.Kind, &r.UpdatedAt)
		results = append(results, r)
	}
	return results, total
}
```

- [ ] **Step 8: Write utility functions (add to db.go)**

```go
// Append to backend/internal/repository/db.go
import (
	"fmt"
	"math/rand"
	"time"
)

func newUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func nowUnix() int64 {
	return time.Now().Unix()
}
```

- [ ] **Step 9: Import database/sql in repository files**

```bash
# Ensure all repository files have the right imports by running:
cd backend && go mod tidy
```

- [ ] **Step 10: Commit**

```bash
git add backend/internal/repository/ backend/go.mod backend/go.sum
git commit -m "feat: add repository layer with CRUD, FTS5 search, and WAL config"
```

### Task 5: Service layer

**Files:**
- Create: `backend/internal/service/folders.go`
- Create: `backend/internal/service/notes.go`
- Create: `backend/internal/service/tasks.go`
- Create: `backend/internal/service/events.go`
- Create: `backend/internal/service/inbox.go`
- Create: `backend/internal/service/search.go`
- Create: `backend/internal/service/today.go`

- [ ] **Step 1: Write folders service**

```go
// backend/internal/service/folders.go
package service

import (
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/repository"
)

func GetFolders() ([]model.Folder, error) {
	return repository.GetFolders()
}
```

- [ ] **Step 2: Write notes service**

```go
// backend/internal/service/notes.go
package service

import (
	"errors"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/repository"
)

func GetNotes(folderID, sort string, page, pageSize int) ([]model.Note, int, error) {
	return repository.GetNotes(folderID, sort, page, pageSize)
}

func GetNote(id string) (*model.Note, error) {
	return repository.GetNoteByID(id)
}

func CreateNote(req *model.CreateNoteRequest) (*model.Note, error) {
	if req.Tags == "" {
		req.Tags = "[]"
	}
	note := &model.Note{
		Title:    req.Title,
		Body:     req.Body,
		FolderID: req.FolderID,
		Tags:     req.Tags,
	}
	if err := repository.CreateNote(note); err != nil {
		return nil, err
	}
	return note, nil
}

func UpdateNote(id string, req *model.UpdateNoteRequest) (*model.Note, error) {
	existing, err := repository.GetNoteByID(id)
	if err != nil {
		return nil, errors.New("note not found")
	}
	_ = existing
	return repository.UpdateNote(id, req)
}

func DeleteNote(id string) error {
	return repository.DeleteNote(id)
}
```

- [ ] **Step 3: Write tasks service**

```go
// backend/internal/service/tasks.go
package service

import (
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/repository"
)

func GetTasks(project, status, scope string, page, pageSize int) ([]model.Task, int, error) {
	return repository.GetTasks(project, status, scope, page, pageSize)
}

func CreateTask(req *model.CreateTaskRequest) (*model.Task, error) {
	task := &model.Task{
		Title:    req.Title,
		Project:  req.Project,
		Due:      req.Due,
		Priority: req.Priority,
		Scope:    req.Scope,
	}
	if err := repository.CreateTask(task); err != nil {
		return nil, err
	}
	return task, nil
}

func UpdateTask(id string, req *model.UpdateTaskRequest) (*model.Task, error) {
	return repository.UpdateTask(id, req)
}

func DeleteTask(id string) error {
	return repository.DeleteTask(id)
}
```

- [ ] **Step 4: Write events service**

```go
// backend/internal/service/events.go
package service

import (
	"time"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/repository"
)

func GetEvents(month string, page, pageSize int) ([]model.Event, int, error) {
	t, err := time.Parse("2006-01", month)
	if err != nil {
		return nil, 0, err
	}
	monthStart := t.Unix()
	monthEnd := t.AddDate(0, 1, 0).Unix()
	return repository.GetEvents(monthStart, monthEnd, page, pageSize)
}

func CreateEvent(req *model.CreateEventRequest) (*model.Event, error) {
	event := &model.Event{
		Title:     req.Title,
		StartTime: req.StartTime,
		EndTime:   req.EndTime,
		Location:  req.Location,
		Kind:      req.Kind,
	}
	if err := repository.CreateEvent(event); err != nil {
		return nil, err
	}
	return event, nil
}

func UpdateEvent(id string, req *model.UpdateEventRequest) (*model.Event, error) {
	return repository.UpdateEvent(id, req)
}

func DeleteEvent(id string) error {
	return repository.DeleteEvent(id)
}
```

- [ ] **Step 5: Write inbox service (includes convert logic)**

```go
// backend/internal/service/inbox.go
package service

import (
	"errors"
	"time"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/repository"
)

func GetInboxItems(kind string, page, pageSize int) ([]model.InboxItem, int, error) {
	return repository.GetInboxItems(kind, page, pageSize)
}

func CreateInboxItem(req *model.CreateInboxRequest) (*model.InboxItem, error) {
	item := &model.InboxItem{
		Kind:  req.Kind,
		Title: req.Title,
		Body:  req.Body,
	}
	if err := repository.CreateInboxItem(item); err != nil {
		return nil, err
	}
	return item, nil
}

func ConvertInboxItem(id string, req *model.ConvertInboxRequest) (interface{}, error) {
	inboxItem, err := repository.GetInboxItemByID(id)
	if err != nil {
		return nil, errors.New("inbox item not found")
	}
	if inboxItem.ConvertedTo != nil {
		return nil, errors.New("already converted")
	}

	now := time.Now()
	tomorrow9am := time.Date(now.Year(), now.Month(), now.Day()+1, 9, 0, 0, 0, now.Location()).Unix()
	tomorrow10am := time.Date(now.Year(), now.Month(), now.Day()+1, 10, 0, 0, 0, now.Location()).Unix()

	var convertedID string

	switch req.Kind {
	case "note":
		body := ""
		if inboxItem.Body != nil {
			body = *inboxItem.Body
		}
		note := &model.Note{
			Title: inboxItem.Title,
			Body:  body,
		}
		if err := repository.CreateNote(note); err != nil {
			return nil, err
		}
		convertedID = note.ID

	case "task":
		task := &model.Task{
			Title: inboxItem.Title,
		}
		if err := repository.CreateTask(task); err != nil {
			return nil, err
		}
		convertedID = task.ID

	case "event":
		event := &model.Event{
			Title:     inboxItem.Title,
			StartTime: tomorrow9am,
			EndTime:   tomorrow10am,
		}
		if err := repository.CreateEvent(event); err != nil {
			return nil, err
		}
		convertedID = event.ID

	default:
		return nil, errors.New("invalid target kind")
	}

	if err := repository.MarkInboxConverted(id, convertedID); err != nil {
		return nil, err
	}

	// Return the created entity
	switch req.Kind {
	case "note":
		return repository.GetNoteByID(convertedID)
	case "task":
		return repository.GetTaskByID(convertedID)
	case "event":
		return repository.GetEventByID(convertedID)
	}
	return nil, errors.New("unexpected kind")
}

func DeleteInboxItem(id string) error {
	return repository.DeleteInboxItem(id)
}

func BatchInbox(req *model.BatchInboxRequest) (int64, error) {
	switch req.Action {
	case "archive":
		return repository.BatchArchiveInbox(req.IDs)
	case "delete":
		return repository.BatchDeleteInbox(req.IDs)
	default:
		return 0, errors.New("invalid action: must be 'archive' or 'delete'")
	}
}
```

- [ ] **Step 6: Write search service**

```go
// backend/internal/service/search.go
package service

import (
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/repository"
)

func Search(q string, page, pageSize int) ([]model.SearchResult, int, error) {
	return repository.Search(q, page, pageSize)
}
```

- [ ] **Step 7: Write today service**

```go
// backend/internal/service/today.go
package service

import (
	"time"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/repository"
)

const OverdueWindowDays = 7

type TodayData struct {
	TodayTasks   []model.Task  `json:"todayTasks"`
	OverdueTasks []model.Task  `json:"overdueTasks"`
	Events       []model.Event `json:"events"`
	RecentNotes  []model.Note  `json:"recentNotes"`
}

func GetToday() (*TodayData, error) {
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()
	todayEnd := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location()).Unix()
	overdueCutoff := todayStart - int64(OverdueWindowDays*86400)

	todayTasks, overdueTasks, err := repository.GetTodayTasks(todayStart, todayEnd, overdueCutoff)
	if err != nil {
		return nil, err
	}

	events, err := repository.GetTodayEvents(todayStart, todayEnd)
	if err != nil {
		return nil, err
	}

	recentNotes, err := repository.GetRecentNotes(5)
	if err != nil {
		return nil, err
	}

	return &TodayData{
		TodayTasks:   todayTasks,
		OverdueTasks: overdueTasks,
		Events:       events,
		RecentNotes:  recentNotes,
	}, nil
}
```

- [ ] **Step 8: Commit**

```bash
git add backend/internal/service/
git commit -m "feat: add service layer with convert logic, overdue window, and month parsing"
```

### Task 6: Handler layer

**Files:**
- Create: `backend/internal/handler/folders.go`
- Create: `backend/internal/handler/notes.go`
- Create: `backend/internal/handler/tasks.go`
- Create: `backend/internal/handler/events.go`
- Create: `backend/internal/handler/inbox.go`
- Create: `backend/internal/handler/search.go`
- Create: `backend/internal/handler/today.go`
- Create: `backend/internal/handler/helpers.go`

- [ ] **Step 1: Write handler helpers**

```go
// backend/internal/handler/helpers.go
package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/model"
)

func success(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, model.APIResponse{Data: data})
}

func successWithPagination(c *gin.Context, data interface{}, page, pageSize, total int) {
	c.JSON(http.StatusOK, model.APIResponse{
		Data: data,
		Pagination: &model.Pagination{
			Page:     page,
			PageSize: pageSize,
			Total:    total,
		},
	})
}

func created(c *gin.Context, data interface{}) {
	c.JSON(http.StatusCreated, model.APIResponse{Data: data})
}

func noContent(c *gin.Context) {
	c.Status(http.StatusNoContent)
}

func errorResponse(c *gin.Context, status int, code, message string) {
	c.JSON(status, model.APIResponse{
		Error: &model.APIError{Code: code, Message: message},
	})
}

func badRequest(c *gin.Context, msg string) {
	errorResponse(c, http.StatusBadRequest, "BAD_REQUEST", msg)
}

func notFound(c *gin.Context, msg string) {
	errorResponse(c, http.StatusNotFound, "NOT_FOUND", msg)
}

func internalError(c *gin.Context, msg string) {
	errorResponse(c, http.StatusInternalServerError, "INTERNAL_ERROR", msg)
}

func getPagination(c *gin.Context) (int, int) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	return page, pageSize
}
```

- [ ] **Step 2: Write folders handler**

```go
// backend/internal/handler/folders.go
package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/service"
)

func GetFolders(c *gin.Context) {
	folders, err := service.GetFolders()
	if err != nil {
		internalError(c, "failed to get folders")
		return
	}
	success(c, gin.H{"folders": folders})
}
```

- [ ] **Step 3: Write notes handler**

```go
// backend/internal/handler/notes.go
package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/service"
)

func GetNotes(c *gin.Context) {
	page, pageSize := getPagination(c)
	folderID := c.Query("folder_id")
	sort := c.DefaultQuery("sort", "recent")

	notes, total, err := service.GetNotes(folderID, sort, page, pageSize)
	if err != nil {
		internalError(c, "failed to get notes")
		return
	}
	successWithPagination(c, gin.H{"notes": notes}, page, pageSize, total)
}

func GetNote(c *gin.Context) {
	note, err := service.GetNote(c.Param("id"))
	if err != nil {
		notFound(c, "note not found")
		return
	}
	success(c, gin.H{"note": note})
}

func CreateNote(c *gin.Context) {
	var req model.CreateNoteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "title is required")
		return
	}
	note, err := service.CreateNote(&req)
	if err != nil {
		internalError(c, "failed to create note")
		return
	}
	created(c, gin.H{"note": note})
}

func UpdateNote(c *gin.Context) {
	var req model.UpdateNoteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid request body")
		return
	}
	note, err := service.UpdateNote(c.Param("id"), &req)
	if err != nil {
		notFound(c, "note not found")
		return
	}
	success(c, gin.H{"note": note})
}

func DeleteNote(c *gin.Context) {
	if err := service.DeleteNote(c.Param("id")); err != nil {
		internalError(c, "failed to delete note")
		return
	}
	noContent(c)
}
```

- [ ] **Step 4: Write tasks handler**

```go
// backend/internal/handler/tasks.go
package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/service"
)

func GetTasks(c *gin.Context) {
	page, pageSize := getPagination(c)
	project := c.Query("project")
	status := c.DefaultQuery("status", "all")
	scope := c.Query("scope")

	tasks, total, err := service.GetTasks(project, status, scope, page, pageSize)
	if err != nil {
		internalError(c, "failed to get tasks")
		return
	}
	successWithPagination(c, gin.H{"tasks": tasks}, page, pageSize, total)
}

func CreateTask(c *gin.Context) {
	var req model.CreateTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "title is required")
		return
	}
	task, err := service.CreateTask(&req)
	if err != nil {
		internalError(c, "failed to create task")
		return
	}
	created(c, gin.H{"task": task})
}

func UpdateTask(c *gin.Context) {
	var req model.UpdateTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid request body")
		return
	}
	task, err := service.UpdateTask(c.Param("id"), &req)
	if err != nil {
		notFound(c, "task not found")
		return
	}
	success(c, gin.H{"task": task})
}

func DeleteTask(c *gin.Context) {
	if err := service.DeleteTask(c.Param("id")); err != nil {
		internalError(c, "failed to delete task")
		return
	}
	noContent(c)
}
```

- [ ] **Step 5: Write events handler**

```go
// backend/internal/handler/events.go
package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/service"
)

func GetEvents(c *gin.Context) {
	page, pageSize := getPagination(c)
	month := c.DefaultQuery("month", "")

	events, total, err := service.GetEvents(month, page, pageSize)
	if err != nil {
		badRequest(c, "invalid month format, expected YYYY-MM")
		return
	}
	successWithPagination(c, gin.H{"events": events}, page, pageSize, total)
}

func CreateEvent(c *gin.Context) {
	var req model.CreateEventRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "title, start_time, and end_time are required")
		return
	}
	event, err := service.CreateEvent(&req)
	if err != nil {
		internalError(c, "failed to create event")
		return
	}
	created(c, gin.H{"event": event})
}

func UpdateEvent(c *gin.Context) {
	var req model.UpdateEventRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid request body")
		return
	}
	event, err := service.UpdateEvent(c.Param("id"), &req)
	if err != nil {
		notFound(c, "event not found")
		return
	}
	success(c, gin.H{"event": event})
}

func DeleteEvent(c *gin.Context) {
	if err := service.DeleteEvent(c.Param("id")); err != nil {
		internalError(c, "failed to delete event")
		return
	}
	noContent(c)
}
```

- [ ] **Step 6: Write inbox handler**

```go
// backend/internal/handler/inbox.go
package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/service"
)

func GetInbox(c *gin.Context) {
	page, pageSize := getPagination(c)
	kind := c.DefaultQuery("kind", "all")

	items, total, err := service.GetInboxItems(kind, page, pageSize)
	if err != nil {
		internalError(c, "failed to get inbox items")
		return
	}
	successWithPagination(c, gin.H{"items": items}, page, pageSize, total)
}

func CreateInboxItem(c *gin.Context) {
	var req model.CreateInboxRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "kind and title are required")
		return
	}
	item, err := service.CreateInboxItem(&req)
	if err != nil {
		internalError(c, "failed to create inbox item")
		return
	}
	created(c, gin.H{"item": item})
}

func ConvertInboxItem(c *gin.Context) {
	var req model.ConvertInboxRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "kind is required")
		return
	}
	result, err := service.ConvertInboxItem(c.Param("id"), &req)
	if err != nil {
		badRequest(c, err.Error())
		return
	}
	created(c, gin.H{"item": result})
}

func DeleteInboxItem(c *gin.Context) {
	if err := service.DeleteInboxItem(c.Param("id")); err != nil {
		internalError(c, "failed to delete inbox item")
		return
	}
	noContent(c)
}

func BatchInbox(c *gin.Context) {
	var req model.BatchInboxRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "ids and action are required")
		return
	}
	affected, err := service.BatchInbox(&req)
	if err != nil {
		badRequest(c, err.Error())
		return
	}
	success(c, gin.H{"affected": affected})
}
```

- [ ] **Step 7: Write search handler**

```go
// backend/internal/handler/search.go
package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/service"
)

func Search(c *gin.Context) {
	page, pageSize := getPagination(c)
	q := c.Query("q")

	results, total, err := service.Search(q, page, pageSize)
	if err != nil {
		internalError(c, "search failed")
		return
	}
	successWithPagination(c, gin.H{"items": results}, page, pageSize, total)
}
```

- [ ] **Step 8: Write today handler**

```go
// backend/internal/handler/today.go
package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/service"
)

func GetToday(c *gin.Context) {
	data, err := service.GetToday()
	if err != nil {
		internalError(c, "failed to get today data")
		return
	}
	success(c, data)
}
```

- [ ] **Step 9: Commit**

```bash
git add backend/internal/handler/
git commit -m "feat: add handler layer with validation and response formatting"
```

### Task 7: Router, middleware, and main entry point

**Files:**
- Create: `backend/internal/middleware/cors.go`
- Create: `backend/internal/router/router.go`
- Create: `backend/cmd/server/main.go`
- Create: `backend/cmd/seed/main.go`

- [ ] **Step 1: Write CORS middleware**

```go
// backend/internal/middleware/cors.go
package middleware

import (
	"github.com/gin-gonic/gin"
)

func CORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "http://localhost:5173")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization")
		c.Header("Access-Control-Max-Age", "86400")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	}
}
```

- [ ] **Step 2: Write router**

```go
// backend/internal/router/router.go
package router

import (
	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/handler"
	"github.com/hujinrun/flowspace/internal/middleware"
)

func Setup() *gin.Engine {
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery(), middleware.CORS())

	api := r.Group("/api")
	{
		api.GET("/folders", handler.GetFolders)

		api.GET("/notes", handler.GetNotes)
		api.GET("/notes/:id", handler.GetNote)
		api.POST("/notes", handler.CreateNote)
		api.PATCH("/notes/:id", handler.UpdateNote)
		api.DELETE("/notes/:id", handler.DeleteNote)

		api.GET("/tasks", handler.GetTasks)
		api.POST("/tasks", handler.CreateTask)
		api.PATCH("/tasks/:id", handler.UpdateTask)
		api.DELETE("/tasks/:id", handler.DeleteTask)

		api.GET("/events", handler.GetEvents)
		api.POST("/events", handler.CreateEvent)
		api.PATCH("/events/:id", handler.UpdateEvent)
		api.DELETE("/events/:id", handler.DeleteEvent)

		api.GET("/inbox", handler.GetInbox)
		api.POST("/inbox", handler.CreateInboxItem)
		api.POST("/inbox/:id/convert", handler.ConvertInboxItem)
		api.POST("/inbox/batch", handler.BatchInbox)
		api.DELETE("/inbox/:id", handler.DeleteInboxItem)

		api.GET("/search", handler.Search)
		api.GET("/today", handler.GetToday)
	}

	return r
}
```

- [ ] **Step 3: Write main.go**

```go
// backend/cmd/server/main.go
package main

import (
	"log"

	"github.com/hujinrun/flowspace/internal/repository"
	"github.com/hujinrun/flowspace/internal/router"
)

func main() {
	if err := repository.InitDB("flowspace.db"); err != nil {
		log.Fatalf("failed to init database: %v", err)
	}
	log.Println("database initialized")

	r := router.Setup()
	log.Println("server starting on :4201")
	if err := r.Run(":4201"); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
```

- [ ] **Step 4: Write seed command**

```go
// backend/cmd/seed/main.go
package main

import (
	"log"

	"github.com/hujinrun/flowspace/internal/repository"
)

func main() {
	if err := repository.InitDB("flowspace.db"); err != nil {
		log.Fatalf("failed to init database: %v", err)
	}
	if err := repository.SeedDB(); err != nil {
		log.Fatalf("failed to seed database: %v", err)
	}
	log.Println("database seeded successfully")
}
```

- [ ] **Step 5: Resolve dependencies**

```bash
cd backend && go mod tidy
```

- [ ] **Step 6: Test that the server starts**

```bash
cd backend && make dev &
sleep 2
curl http://localhost:4201/api/folders
# Expected: {"data":{"folders":[{"id":"__uncategorized",...}]}}
kill %1
```

- [ ] **Step 7: Commit**

```bash
git add backend/cmd/ backend/internal/router/ backend/internal/middleware/ backend/go.mod backend/go.sum
git commit -m "feat: add Gin router, CORS middleware, and server entry point"
```

---

## Phase 2: Frontend Foundation

### Task 8: Initialize Vite + React + TypeScript project

**Files:**
- Create: `frontend/package.json`
- Create: `frontend/tsconfig.json`
- Create: `frontend/tsconfig.node.json`
- Create: `frontend/vite.config.ts`
- Create: `frontend/index.html`
- Create: `frontend/.prettierrc`
- Create: `frontend/.eslintrc.cjs`
- Create: `frontend/src/main.tsx`
- Create: `frontend/src/vite-env.d.ts`

- [ ] **Step 1: Create frontend directory and init with pnpm**

```bash
mkdir -p frontend/src
cd frontend
pnpm init
pnpm add react@19 react-dom@19 react-router-dom@7 @tanstack/react-query zustand
pnpm add -D typescript @types/react @types/react-dom vite @vitejs/plugin-react tailwindcss @tailwindcss/vite prettier eslint @eslint/js typescript-eslint
```

- [ ] **Step 2: Write package.json scripts**

```json
{
  "name": "flowspace-frontend",
  "private": true,
  "version": "0.0.1",
  "type": "module",
  "scripts": {
    "dev": "vite",
    "build": "tsc -b && vite build",
    "preview": "vite preview",
    "lint": "eslint src/",
    "format": "prettier --write src/"
  }
}
```

- [ ] **Step 3: Write tsconfig.json**

```json
{
  "compilerOptions": {
    "target": "ES2022",
    "lib": ["ES2022", "DOM", "DOM.Iterable"],
    "module": "ESNext",
    "moduleResolution": "bundler",
    "jsx": "react-jsx",
    "strict": true,
    "noUnusedLocals": true,
    "noUnusedParameters": true,
    "paths": { "@/*": ["./src/*"] },
    "baseUrl": "."
  },
  "include": ["src"]
}
```

- [ ] **Step 4: Write tsconfig.node.json**

```json
{
  "compilerOptions": {
    "target": "ES2022",
    "lib": ["ES2022"],
    "module": "ESNext",
    "moduleResolution": "bundler",
    "strict": true
  },
  "include": ["vite.config.ts"]
}
```

- [ ] **Step 5: Write vite.config.ts**

```ts
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import path from 'node:path'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { '@': path.resolve(__dirname, 'src') },
  },
  server: {
    proxy: { '/api': 'http://localhost:4201' },
  },
})
```

- [ ] **Step 6: Write index.html**

```html
<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>FlowSpace</title>
</head>
<body>
  <div id="root"></div>
  <script type="module" src="/src/main.tsx"></script>
</body>
</html>
```

- [ ] **Step 7: Write main.tsx**

```tsx
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { RouterProvider } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { router } from './router'
import './styles/index.css'

const queryClient = new QueryClient({
  defaultOptions: {
    queries: { staleTime: 30_000, retry: 1 },
  },
})

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </StrictMode>,
)
```

- [ ] **Step 8: Write .prettierrc**

```json
{
  "singleQuote": true,
  "semi": false,
  "tabWidth": 2,
  "trailingComma": "es5"
}
```

- [ ] **Step 9: Commit**

```bash
git add frontend/
git commit -m "feat: init Vite + React 19 + TypeScript + Tailwind frontend project"
```

### Task 9: Tailwind CSS design tokens and styles

**Files:**
- Create: `frontend/src/styles/index.css`
- Create: `frontend/tailwind.config.ts` (if needed)

- [ ] **Step 1: Write index.css with design tokens**

```css
/* frontend/src/styles/index.css */
@import "tailwindcss";

@theme {
  --color-fs-bg: #fafafa;
  --color-fs-surface: #ffffff;
  --color-fs-hover: #f4f4f5;
  --color-fs-border: #e4e4e7;
  --color-fs-border-hover: #d4d4d8;
  --color-fs-text: #18181b;
  --color-fs-text-secondary: #52525b;
  --color-fs-text-muted: #a1a1aa;
  --color-fs-text-disabled: #d4d4d8;
  --color-fs-accent: #2563eb;
  --color-fs-accent-hover: #1d4ed8;
  --color-fs-warning: #f59e0b;
  --color-fs-success: #10b981;

  --font-family-sans: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Noto Sans SC", sans-serif;
  --font-family-mono: "SF Mono", "Cascadia Code", "Fira Code", monospace;

  --font-size-hero: 28px;
  --font-size-h1: 22px;
  --font-size-h2: 18px;
  --font-size-body: 15px;
  --font-size-caption: 13px;
  --font-size-small: 12px;

  --radius-sm: 4px;
  --radius-md: 6px;
  --radius-lg: 10px;
  --radius-pill: 9999px;
  --radius-capture: 14px;

  --shadow-hover: 0 1px 3px rgba(0,0,0,0.08);
  --shadow-popover: 0 4px 16px rgba(0,0,0,0.12);

  --ease-standard: cubic-bezier(0.2, 0, 0, 1);
}

body {
  font-family: var(--font-family-sans);
  color: var(--color-fs-text);
  background: var(--color-fs-bg);
  -webkit-font-smoothing: antialiased;
}
```

- [ ] **Step 2: Commit**

```bash
git add frontend/src/styles/index.css
git commit -m "feat: add Tailwind design tokens from THEME.md"
```

### Task 10: Router and App shell

**Files:**
- Create: `frontend/src/router.tsx`
- Create: `frontend/src/App.tsx`
- Create: `frontend/src/stores/ui.ts`
- Create: `frontend/src/routes/Dashboard.tsx` (placeholder)
- Create: `frontend/src/routes/Notes.tsx` (placeholder)
- Create: `frontend/src/routes/Editor.tsx` (placeholder)
- Create: `frontend/src/routes/Tasks.tsx` (placeholder)
- Create: `frontend/src/routes/Calendar.tsx` (placeholder)
- Create: `frontend/src/routes/Inbox.tsx` (placeholder)
- Create: `frontend/src/routes/Search.tsx` (placeholder)

- [ ] **Step 1: Write Zustand UI store**

```ts
// frontend/src/stores/ui.ts
import { create } from 'zustand'

interface UIState {
  captureOpen: boolean
  setCaptureOpen: (open: boolean) => void
}

export const useUIStore = create<UIState>((set) => ({
  captureOpen: false,
  setCaptureOpen: (open) => set({ captureOpen: open }),
}))
```

- [ ] **Step 2: Write router with lazy-loaded routes**

```tsx
// frontend/src/router.tsx
import { createBrowserRouter } from 'react-router-dom'
import { App } from './App'
import { lazy } from 'react'

const Dashboard = lazy(() => import('./routes/Dashboard'))
const Notes = lazy(() => import('./routes/Notes'))
const Editor = lazy(() => import('./routes/Editor'))
const Tasks = lazy(() => import('./routes/Tasks'))
const Calendar = lazy(() => import('./routes/Calendar'))
const Inbox = lazy(() => import('./routes/Inbox'))
const Search = lazy(() => import('./routes/Search'))

export const router = createBrowserRouter([
  {
    path: '/',
    element: <App />,
    children: [
      { index: true, element: <Dashboard /> },
      { path: 'notes', element: <Notes /> },
      { path: 'editor/:id', element: <Editor /> },
      { path: 'tasks', element: <Tasks /> },
      { path: 'calendar', element: <Calendar /> },
      { path: 'inbox', element: <Inbox /> },
      { path: 'search', element: <Search /> },
    ],
  },
])
```

- [ ] **Step 3: Write App shell**

```tsx
// frontend/src/App.tsx
import { Suspense } from 'react'
import { Outlet } from 'react-router-dom'
import { useUIStore } from './stores/ui'
import { Sidebar } from './components/layout/Sidebar'
import { TopBar } from './components/layout/TopBar'
import { QuickBar } from './components/layout/QuickBar'
import { QuickCapture } from './components/QuickCapture'

export function App() {
  const captureOpen = useUIStore((s) => s.captureOpen)

  return (
    <div className="h-screen grid grid-cols-[220px_minmax(0,1fr)] bg-fs-bg overflow-hidden max-[760px]:grid-cols-1">
      <Sidebar />
      <main className="min-w-0 min-h-0 pb-[110px] px-8 pt-7 grid gap-5 content-start relative max-[760px]:px-4 max-[760px]:pt-5 max-[760px]:pb-[120px]">
        <TopBar />
        <Suspense fallback={<div className="text-fs-text-muted">Loading...</div>}>
          <Outlet />
        </Suspense>
      </main>
      <QuickBar />
      {captureOpen && <QuickCapture />}
    </div>
  )
}
```

- [ ] **Step 4: Write placeholder route components**

```tsx
// frontend/src/routes/Dashboard.tsx
export default function Dashboard() {
  return <div>Dashboard - 今日视图</div>
}
```

(Create similar placeholder files for Notes, Editor, Tasks, Calendar, Inbox, Search — each exporting a default component with a single `<div>` showing the route name.)

- [ ] **Step 5: Commit**

```bash
git add frontend/src/router.tsx frontend/src/App.tsx frontend/src/stores/ frontend/src/routes/
git commit -m "feat: add React Router v7 shell with Zustand store and route placeholders"
```

### Task 11: Sidebar component

**Files:**
- Create: `frontend/src/components/layout/Sidebar.tsx`

- [ ] **Step 1: Write Sidebar**

```tsx
// frontend/src/components/layout/Sidebar.tsx
import { NavLink } from 'react-router-dom'

const navItems = [
  { to: '/', label: '今日', icon: CalendarIcon },
  { to: '/tasks', label: '任务', icon: CheckIcon },
  { to: '/notes', label: '笔记', icon: FileIcon },
  { to: '/calendar', label: '日历', icon: CalendarDaysIcon },
  { to: '/inbox', label: '收件箱', icon: InboxIcon },
  { to: '/search', label: '搜索', icon: SearchIcon },
]

export function Sidebar() {
  return (
    <aside className="h-screen border-r border-fs-border bg-fs-surface px-3.5 py-[18px] flex flex-col gap-[22px] max-[760px]:hidden">
      <div className="flex gap-2.5 items-center px-1 pb-2">
        <div className="w-8 h-8 rounded-lg bg-fs-accent grid place-items-center text-white font-bold text-sm">F</div>
        <div>
          <strong className="block text-sm leading-tight">FlowSpace</strong>
          <span className="text-fs-text-muted text-xs">轻量效率中枢</span>
        </div>
      </div>

      <nav className="grid gap-1.5">
        {navItems.map(({ to, label, icon: Icon }) => (
          <NavLink
            key={to}
            to={to}
            end={to === '/'}
            className={({ isActive }) =>
              `flex items-center justify-between min-h-[34px] px-2.5 rounded-md text-[13px] font-medium transition-colors ${
                isActive ? 'bg-fs-hover text-fs-accent font-semibold' : 'text-fs-text hover:bg-fs-hover'
              }`
            }
          >
            <span className="inline-flex items-center gap-2 min-w-0">
              <Icon />
              {label}
            </span>
          </NavLink>
        ))}
      </nav>

      <div className="mt-auto grid gap-1.5">
        <div className="text-fs-text-muted text-[11px] font-semibold uppercase tracking-wider px-1">FlowSpace v0.1</div>
      </div>
    </aside>
  )
}

// SVG icon components
function CalendarIcon() {
  return <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round"><rect x="3" y="4" width="18" height="18" rx="2"/><line x1="16" y1="2" x2="16" y2="6"/><line x1="8" y1="2" x2="8" y2="6"/><line x1="3" y1="10" x2="21" y2="10"/></svg>
}
function CheckIcon() {
  return <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round"><polyline points="9 11 12 14 22 4"/><path d="M21 12v7a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11"/></svg>
}
function FileIcon() {
  return <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round"><path d="M14.5 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7.5L14.5 2z"/><polyline points="14 2 14 8 20 8"/></svg>
}
function CalendarDaysIcon() {
  return <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round"><rect x="3" y="4" width="18" height="18" rx="2"/><line x1="8" y1="2" x2="8" y2="6"/><line x1="16" y1="2" x2="16" y2="6"/><line x1="3" y1="10" x2="21" y2="10"/><line x1="8" y1="14" x2="8" y2="14.01"/><line x1="12" y1="14" x2="12" y2="14.01"/><line x1="16" y1="14" x2="16" y2="14.01"/></svg>
}
function InboxIcon() {
  return <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round"><polyline points="22 12 16 12 14 15 10 15 8 12 2 12"/><path d="M5.45 5.11L2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.45-6.89A2 2 0 0 0 16.76 4H7.24a2 2 0 0 0-1.79 1.11z"/></svg>
}
function SearchIcon() {
  return <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg>
}
```

- [ ] **Step 2: Commit**

```bash
git add frontend/src/components/layout/Sidebar.tsx
git commit -m "feat: add Sidebar component with nav links"
```

### Task 12: TopBar, QuickBar, and QuickCapture components

**Files:**
- Create: `frontend/src/components/layout/TopBar.tsx`
- Create: `frontend/src/components/layout/QuickBar.tsx`
- Create: `frontend/src/components/QuickCapture.tsx`

- [ ] **Step 1: Write TopBar**

```tsx
// frontend/src/components/layout/TopBar.tsx
import { useLocation } from 'react-router-dom'
import { useUIStore } from '../../stores/ui'

const titles: Record<string, string> = {
  '/': '今日',
  '/notes': '笔记',
  '/tasks': '任务',
  '/calendar': '日历',
  '/inbox': '收件箱',
  '/search': '搜索',
}

export function TopBar() {
  const { pathname } = useLocation()
  const setCaptureOpen = useUIStore((s) => s.setCaptureOpen)

  const title = pathname.startsWith('/editor/') ? '编辑器' : (titles[pathname] ?? '今日')

  return (
    <div className="flex justify-between items-start gap-5">
      <h1 className="text-[28px] leading-tight font-bold mt-1">{title}</h1>
      <button
        onClick={() => setCaptureOpen(true)}
        className="text-[13px] px-4 py-1.5 rounded-md bg-fs-accent text-white border-0 cursor-pointer font-sans hover:bg-fs-accent-hover transition-colors"
      >
        + 快速捕获 (⌘⇧K)
      </button>
    </div>
  )
}
```

- [ ] **Step 2: Write QuickBar**

```tsx
// frontend/src/components/layout/QuickBar.tsx
import { useUIStore } from '../../stores/ui'

export function QuickBar() {
  const setCaptureOpen = useUIStore((s) => s.setCaptureOpen)

  return (
    <div className="fixed left-1/2 bottom-6 -translate-x-1/2 inline-flex items-center gap-1 bg-fs-surface border border-fs-border rounded-full shadow-hover px-1.5 py-1.5 z-50 max-[760px]:gap-0">
      <button className="border-0 bg-transparent rounded-full min-h-[34px] px-3 text-sm cursor-pointer hover:bg-fs-hover transition-colors">
        📝 笔记
      </button>
      <button className="border-0 bg-transparent rounded-full min-h-[34px] px-3 text-sm cursor-pointer hover:bg-fs-hover transition-colors">
        ✅ 任务
      </button>
      <button className="border-0 bg-transparent rounded-full min-h-[34px] px-3 text-sm cursor-pointer hover:bg-fs-hover transition-colors">
        📅 日程
      </button>
      <div className="w-px h-5 bg-fs-border mx-1" />
      <button
        onClick={() => setCaptureOpen(true)}
        className="border-0 bg-fs-accent text-white rounded-full min-h-[34px] px-4 text-sm cursor-pointer hover:bg-fs-accent-hover transition-colors font-medium"
      >
        快速捕获
      </button>
    </div>
  )
}
```

- [ ] **Step 3: Write QuickCapture (with keyboard shortcut and recent inbox)**

```tsx
// frontend/src/components/QuickCapture.tsx
import { useEffect, useState } from 'react'
import { useUIStore } from '../stores/ui'

type Kind = 'note' | 'task' | 'event'

export function QuickCapture() {
  const setCaptureOpen = useUIStore((s) => s.setCaptureOpen)
  const [kind, setKind] = useState<Kind>('note')
  const [title, setTitle] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    function handleKeyDown(e: KeyboardEvent) {
      if (e.key === 'Escape') setCaptureOpen(false)
      if (e.metaKey && e.shiftKey && e.key === 'K') {
        e.preventDefault()
        setCaptureOpen(true)
      }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [setCaptureOpen])

  async function handleSubmit() {
    if (!title.trim()) return
    setSubmitting(true)
    setError(null)
    try {
      const res = await fetch('/api/inbox', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ kind, title: title.trim() }),
      })
      if (!res.ok) throw new Error('创建失败')
      setTitle('')
      setCaptureOpen(false)
    } catch {
      setError('创建失败，请重试')
    } finally {
      setSubmitting(false)
    }
  }

  const kinds: { value: Kind; label: string }[] = [
    { value: 'note', label: '笔记' },
    { value: 'task', label: '任务' },
    { value: 'event', label: '日程' },
  ]

  return (
    <>
      <div className="fixed inset-0 bg-black/20 z-[100] grid place-items-start pt-[60px]" onClick={() => setCaptureOpen(false)}>
        <div
          className="w-[520px] bg-fs-surface rounded-[14px] shadow-popover p-6 grid gap-[18px] animate-[slideDown_200ms_var(--ease-standard)] max-[760px]:w-[calc(100vw-32px)] max-[760px]:mx-4"
          onClick={(e) => e.stopPropagation()}
        >
          <div className="flex justify-between items-center">
            <strong className="text-[15px]">快速捕获</strong>
            <button onClick={() => setCaptureOpen(false)} className="border-0 bg-transparent text-fs-text-muted cursor-pointer text-lg leading-none">&times;</button>
          </div>

          <div className="flex gap-1.5">
            {kinds.map(({ value, label }) => (
              <button
                key={value}
                onClick={() => setKind(value)}
                className={`border-0 rounded-md px-3 py-1.5 text-xs cursor-pointer transition-colors ${
                  kind === value ? 'bg-fs-accent text-white' : 'bg-fs-hover text-fs-text-secondary hover:bg-fs-border'
                }`}
              >
                {label}
              </button>
            ))}
          </div>

          <textarea
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            placeholder={kind === 'note' ? '输入笔记标题...' : kind === 'task' ? '输入任务名称...' : '输入日程标题...'}
            className="w-full border border-fs-border rounded-md p-3 text-[15px] leading-relaxed resize-none outline-none focus:border-fs-accent transition-colors font-sans"
            rows={3}
            autoFocus
            onKeyDown={(e) => {
              if (e.metaKey && e.key === 'Enter') handleSubmit()
            }}
          />

          {error && <div className="text-red-500 text-xs">{error}</div>}

          <div className="flex justify-between items-center">
            <span className="text-[13px] text-fs-text-muted">⌘+Enter 创建</span>
            <div className="flex gap-2">
              <button onClick={() => setCaptureOpen(false)} className="border border-fs-border rounded-md px-4 py-1.5 text-sm bg-transparent cursor-pointer hover:bg-fs-hover transition-colors">取消</button>
              <button onClick={handleSubmit} disabled={submitting || !title.trim()} className="border-0 rounded-md px-4 py-1.5 text-sm bg-fs-accent text-white cursor-pointer hover:bg-fs-accent-hover transition-colors disabled:opacity-50 disabled:cursor-not-allowed">
                {submitting ? '创建中...' : '创建'}
              </button>
            </div>
          </div>
        </div>
      </div>

      <style>{`
        @keyframes slideDown {
          from { opacity: 0; transform: translateY(-12px); }
          to { opacity: 1; transform: translateY(0); }
        }
        @media (prefers-reduced-motion: reduce) {
          .animate-[slideDown_200ms] { animation: none; }
        }
      `}</style>
    </>
  )
}
```

- [ ] **Step 4: Commit**

```bash
git add frontend/src/components/layout/TopBar.tsx frontend/src/components/layout/QuickBar.tsx frontend/src/components/QuickCapture.tsx
git commit -m "feat: add TopBar, QuickBar, and QuickCapture with Cmd+Shift+K shortcut"
```

### Task 13: API client and TanStack Query hooks

**Files:**
- Create: `frontend/src/api/client.ts`
- Create: `frontend/src/api/notes.ts`
- Create: `frontend/src/api/tasks.ts`
- Create: `frontend/src/api/events.ts`
- Create: `frontend/src/api/inbox.ts`
- Create: `frontend/src/api/search.ts`
- Create: `frontend/src/api/folders.ts`
- Create: `frontend/src/hooks/useNotes.ts`
- Create: `frontend/src/hooks/useTasks.ts`
- Create: `frontend/src/hooks/useEvents.ts`
- Create: `frontend/src/hooks/useSearch.ts`

- [ ] **Step 1: Write API client**

```ts
// frontend/src/api/client.ts
export interface APIResponse<T> {
  data: T
  pagination?: { page: number; page_size: number; total: number }
  error?: { code: string; message: string }
}

class APIClient {
  private basePath = ''

  async get<T>(path: string, params?: Record<string, string>): Promise<APIResponse<T>> {
    const url = new URL(`${this.basePath}${path}`, window.location.origin)
    if (params) {
      Object.entries(params).forEach(([k, v]) => { if (v) url.searchParams.set(k, v) })
    }
    const res = await fetch(url.toString())
    if (!res.ok) {
      const body = await res.json().catch(() => ({}))
      throw new APIError(res.status, body?.error?.code ?? 'UNKNOWN', body?.error?.message ?? 'Request failed')
    }
    if (res.status === 204) return { data: undefined as T }
    return res.json()
  }

  async post<T>(path: string, body?: unknown): Promise<APIResponse<T>> {
    const res = await fetch(`${this.basePath}${path}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: body ? JSON.stringify(body) : undefined,
    })
    if (!res.ok) {
      const errBody = await res.json().catch(() => ({}))
      throw new APIError(res.status, errBody?.error?.code ?? 'UNKNOWN', errBody?.error?.message ?? 'Request failed')
    }
    return res.json()
  }

  async patch<T>(path: string, body?: unknown): Promise<APIResponse<T>> {
    const res = await fetch(`${this.basePath}${path}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: body ? JSON.stringify(body) : undefined,
    })
    if (!res.ok) {
      const errBody = await res.json().catch(() => ({}))
      throw new APIError(res.status, errBody?.error?.code ?? 'UNKNOWN', errBody?.error?.message ?? 'Request failed')
    }
    return res.json()
  }

  async del(path: string): Promise<void> {
    const res = await fetch(`${this.basePath}${path}`, { method: 'DELETE' })
    if (!res.ok) throw new APIError(res.status, 'UNKNOWN', 'Delete failed')
  }
}

export class APIError extends Error {
  constructor(public status: number, public code: string, message: string) {
    super(message)
  }
}

export const api = new APIClient()
```

- [ ] **Step 2: Write API modules**

```ts
// frontend/src/api/folders.ts
import { api } from './client'

export interface Folder {
  id: string; name: string; sort_order: number; note_count: number; created_at: number
}

export async function getFolders() {
  const res = await api.get<{ folders: Folder[] }>('/api/folders')
  return res.data.folders
}
```

```ts
// frontend/src/api/notes.ts
import { api } from './client'

export interface Note {
  id: string; title: string; body: string; folder_id: string; tags: string; created_at: number; updated_at: number
}

export async function getNotes(params: { folder_id?: string; sort?: string; page?: number; page_size?: number }) {
  const res = await api.get<{ notes: Note[] }>('/api/notes', {
    folder_id: params.folder_id ?? '',
    sort: params.sort ?? 'recent',
    page: String(params.page ?? 1),
    page_size: String(params.page_size ?? 20),
  })
  return { notes: res.data.notes, pagination: res.pagination! }
}

export async function getNote(id: string) {
  const res = await api.get<{ note: Note }>(`/api/notes/${id}`)
  return res.data.note
}

export async function createNote(body: { title: string; body?: string; folder_id?: string; tags?: string }) {
  const res = await api.post<{ note: Note }>('/api/notes', body)
  return res.data.note
}

export async function updateNote(id: string, body: Partial<Note>) {
  const res = await api.patch<{ note: Note }>(`/api/notes/${id}`, body)
  return res.data.note
}

export async function deleteNote(id: string) {
  await api.del(`/api/notes/${id}`)
}
```

(Create similar files for tasks.ts, events.ts, inbox.ts, search.ts — following the same pattern with types matching the Go models.)

- [ ] **Step 3: Write TanStack Query hooks**

```ts
// frontend/src/hooks/useNotes.ts
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import * as notesApi from '../api/notes'
import type { Note } from '../api/notes'

export function useNotesList(params: { folder_id?: string; sort?: string; page?: number }) {
  return useQuery({
    queryKey: ['notes', params],
    queryFn: () => notesApi.getNotes(params),
  })
}

export function useNote(id: string) {
  return useQuery({
    queryKey: ['notes', id],
    queryFn: () => notesApi.getNote(id),
    enabled: !!id,
  })
}

export function useCreateNote() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: notesApi.createNote,
    onSuccess: () => qc.invalidateQueries({ queryKey: ['notes'] }),
  })
}

export function useUpdateNote() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...body }: { id: string } & Partial<Note>) => notesApi.updateNote(id, body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['notes'] }),
  })
}

export function useDeleteNote() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: notesApi.deleteNote,
    onSuccess: () => qc.invalidateQueries({ queryKey: ['notes'] }),
  })
}
```

(Create similar hook files for useTasks.ts, useEvents.ts, useSearch.ts following the same pattern.)

- [ ] **Step 4: Commit**

```bash
git add frontend/src/api/ frontend/src/hooks/
git commit -m "feat: add API client and TanStack Query hooks"
```

---

## Phase 3: Frontend Routes

### Task 14: Dashboard (今日视图)

**Files:**
- Modify: `frontend/src/routes/Dashboard.tsx`
- Create: `frontend/src/components/ui/TaskRow.tsx`
- Create: `frontend/src/components/ui/EventChip.tsx`
- Create: `frontend/src/components/ui/NoteCard.tsx`
- Create: `frontend/src/components/ui/MiniCalendar.tsx`

- [ ] **Step 1: Write TaskRow component**

```tsx
// frontend/src/components/ui/TaskRow.tsx
export interface TaskData {
  id: string; title: string; project?: string; due?: number; priority: number; done: number; scope: string
}

export function TaskRow({ task, onToggle }: { task: TaskData; onToggle: (id: string) => void }) {
  return (
    <button
      onClick={() => onToggle(task.id)}
      className={`w-full border rounded-md px-2.5 py-2 grid grid-cols-[18px_1fr_auto] gap-2.5 items-start text-left cursor-pointer transition-colors hover:bg-fs-surface hover:border-fs-border ${
        task.done ? 'border-transparent bg-transparent' : 'border-transparent bg-transparent'
      }`}
    >
      <span className={`w-[18px] h-[18px] rounded border-2 grid place-items-center text-[10px] font-bold mt-0.5 ${
        task.done ? 'bg-fs-success border-fs-success text-white' : 'border-fs-border-hover text-transparent'
      }`}>
        {task.done ? '✓' : ''}
      </span>
      <div className="grid gap-[3px]">
        <strong className={`text-[13px] leading-snug font-medium ${task.done ? 'text-fs-text-disabled line-through' : ''}`}>
          {task.title}
        </strong>
        {task.due && (
          <small className="text-fs-text-muted text-xs flex gap-1.5 items-center">
            {new Date(task.due * 1000).toLocaleDateString('zh-CN', { month: 'short', day: 'numeric' })}
            {task.project && <span className="text-fs-text-muted">· {task.project}</span>}
          </small>
        )}
      </div>
      {task.priority === 1 && <span className="text-fs-warning text-[11px] font-semibold self-start">!!</span>}
    </button>
  )
}
```

- [ ] **Step 2: Write EventChip component**

```tsx
// frontend/src/components/ui/EventChip.tsx
export interface EventData {
  id: string; title: string; start_time: number; end_time: number; location?: string; kind: string
}

const kindColors: Record<string, string> = {
  work: 'bg-fs-accent',
  personal: 'bg-fs-success',
  reminder: 'bg-fs-warning',
}

export function EventChip({ event }: { event: EventData }) {
  const start = new Date(event.start_time * 1000)
  const end = new Date(event.end_time * 1000)
  const timeStr = `${start.getHours().toString().padStart(2, '0')}:${start.getMinutes().toString().padStart(2, '0')} - ${end.getHours().toString().padStart(2, '0')}:${end.getMinutes().toString().padStart(2, '0')}`

  return (
    <div className="flex gap-2.5 items-start py-0.5">
      <div className={`w-2 h-2 rounded-full mt-1.5 shrink-0 ${kindColors[event.kind] ?? 'bg-fs-border'}`} />
      <div className="grid gap-0.5 min-w-0">
        <strong className="text-[13px] leading-snug font-medium">{event.title}</strong>
        <small className="text-fs-text-muted text-xs tabular-nums">
          {timeStr}
          {event.location && ` · ${event.location}`}
        </small>
      </div>
    </div>
  )
}
```

- [ ] **Step 3: Write NoteCard component**

```tsx
// frontend/src/components/ui/NoteCard.tsx
export interface NoteData {
  id: string; title: string; folder_id: string; tags: string; updated_at: number
}

export function NoteCard({ note }: { note: NoteData }) {
  const tags: string[] = JSON.parse(note.tags || '[]')

  return (
    <div className="grid gap-1 py-2">
      <strong className="text-[13px] leading-snug font-medium">{note.title}</strong>
      <div className="flex gap-1 flex-wrap mt-1">
        {tags.map((tag: string) => (
          <span key={tag} className="text-fs-accent bg-fs-accent/10 rounded-sm px-1.5 py-0.5 text-[11px] font-medium">{tag}</span>
        ))}
      </div>
    </div>
  )
}
```

- [ ] **Step 4: Write MiniCalendar component**

```tsx
// frontend/src/components/ui/MiniCalendar.tsx
import { useState } from 'react'

export function MiniCalendar() {
  const [currentDate] = useState(new Date())
  const year = currentDate.getFullYear()
  const month = currentDate.getMonth()

  const firstDay = new Date(year, month, 1).getDay()
  const daysInMonth = new Date(year, month + 1, 0).getDate()
  const today = currentDate.getDate()

  const days = ['日', '一', '二', '三', '四', '五', '六']

  return (
    <div>
      <div className="mb-2">
        <strong className="text-[13px] font-semibold">{currentDate.toLocaleDateString('zh-CN', { month: 'long', year: 'numeric' })}</strong>
      </div>
      <div className="grid grid-cols-7 gap-[3px] text-center text-xs">
        {days.map((d) => <div key={d} className="text-fs-text-muted text-[11px] pb-1">{d}</div>)}
        {Array.from({ length: firstDay }).map((_, i) => <div key={`empty-${i}`} className="min-h-[30px]" />)}
        {Array.from({ length: daysInMonth }).map((_, i) => {
          const day = i + 1
          const isToday = day === today
          return (
            <div
              key={day}
              className={`min-h-[30px] grid place-items-center rounded-md text-xs tabular-nums ${
                isToday ? 'bg-fs-accent text-white font-semibold' : 'hover:bg-fs-hover'
              }`}
            >
              {day}
            </div>
          )
        })}
      </div>
    </div>
  )
}
```

- [ ] **Step 5: Write Dashboard with 3-column layout**

```tsx
// frontend/src/routes/Dashboard.tsx (replace placeholder)
import { useQuery } from '@tanstack/react-query'
import { api } from '../api/client'
import { TaskRow, type TaskData } from '../components/ui/TaskRow'
import { EventChip, type EventData } from '../components/ui/EventChip'
import { NoteCard, type NoteData } from '../components/ui/NoteCard'
import { MiniCalendar } from '../components/ui/MiniCalendar'

interface TodayData {
  todayTasks: TaskData[]
  overdueTasks: TaskData[]
  events: EventData[]
  recentNotes: NoteData[]
}

export default function Dashboard() {
  const { data, isLoading, error } = useQuery({
    queryKey: ['today'],
    queryFn: async () => {
      const res = await api.get<TodayData>('/api/today')
      return res.data
    },
  })

  if (isLoading) {
    return (
      <div className="grid grid-cols-[5fr_4fr_3fr] gap-4 max-[1120px]:grid-cols-2 max-[760px]:grid-cols-1">
        <div className="grid gap-4"><SkeletonBlock rows={4} /></div>
        <div className="grid gap-4"><SkeletonBlock rows={3} /><div className="h-[200px] bg-fs-hover rounded-lg animate-pulse" /></div>
        <div className="grid gap-4"><SkeletonBlock rows={3} /></div>
      </div>
    )
  }

  if (error) {
    return <div className="text-red-500 text-sm">加载失败 <button onClick={() => window.location.reload()} className="underline ml-2">重试</button></div>
  }

  if (!data) return null

  return (
    <div className="grid grid-cols-[5fr_4fr_3fr] gap-4 max-[1120px]:grid-cols-2 max-[760px]:grid-cols-1">
      {/* Column 1: Tasks */}
      <div className="grid gap-4 content-start">
        <div className="flex justify-between items-center mb-3">
          <h2 className="text-sm font-semibold uppercase tracking-wider text-fs-text-muted">今日任务</h2>
          <span className="text-xs text-fs-text-muted tabular-nums font-mono">{data.todayTasks.length}</span>
        </div>
        {data.todayTasks.length === 0 && data.overdueTasks.length === 0 ? (
          <p className="text-fs-text-muted text-sm text-center py-4">今天还没有任务，点击右上角"快速捕获"开始</p>
        ) : (
          <>
            {data.overdueTasks.length > 0 && (
              <div className="mb-2">
                <span className="text-fs-warning text-[11px] font-semibold uppercase tracking-wider">逾期</span>
                <div className="grid gap-1.5 mt-1">
                  {data.overdueTasks.map((t) => <TaskRow key={t.id} task={t} onToggle={() => {}} />)}
                </div>
              </div>
            )}
            <div className="grid gap-1.5">
              {data.todayTasks.map((t) => <TaskRow key={t.id} task={t} onToggle={() => {}} />)}
            </div>
          </>
        )}
      </div>

      {/* Column 2: Calendar + Schedule */}
      <div className="grid gap-4 content-start">
        <div className="flex justify-between items-center mb-3">
          <h2 className="text-sm font-semibold uppercase tracking-wider text-fs-text-muted">日程</h2>
          <span className="text-xs text-fs-text-muted tabular-nums font-mono">{data.events.length}</span>
        </div>
        <MiniCalendar />
        {data.events.length > 0 && (
          <div className="grid gap-2 mt-2">
            {data.events.map((e) => <EventChip key={e.id} event={e} />)}
          </div>
        )}
      </div>

      {/* Column 3: Recent Notes */}
      <div className="grid gap-4 content-start">
        <div className="flex justify-between items-center mb-3">
          <h2 className="text-sm font-semibold uppercase tracking-wider text-fs-text-muted">最近笔记</h2>
          <span className="text-xs text-fs-text-muted tabular-nums font-mono">{data.recentNotes.length}</span>
        </div>
        <div className="grid gap-2">
          {data.recentNotes.map((n) => <NoteCard key={n.id} note={n} />)}
        </div>
      </div>
    </div>
  )
}

function SkeletonBlock({ rows }: { rows: number }) {
  return (
    <div className="grid gap-2">
      {Array.from({ length: rows }).map((_, i) => (
        <div key={i} className="h-10 bg-fs-hover rounded-md animate-pulse" />
      ))}
    </div>
  )
}
```

- [ ] **Step 6: Commit**

```bash
git add frontend/src/routes/Dashboard.tsx frontend/src/components/ui/
git commit -m "feat: add Dashboard with 3-column layout, TaskRow, EventChip, NoteCard, MiniCalendar"
```

### Tasks 15-21: Remaining routes

Due to length, the remaining routes (Tasks, Notes, Editor, Calendar, Inbox, Search) follow the same pattern as Dashboard:
- Use TanStack Query hooks to fetch data
- Render skeleton/loading/error/empty states per spec
- Compose UI components (TaskRow, EventChip, NoteCard, Tag, MiniCalendar)

**Key implementation notes per route:**

- **Tasks** (`/tasks`): Filter pills (全部/项目A/技术/工作/个人), status tabs (全部/未完成/已完成), grouped by scope sections, inline add-task form. Uses `useTasksList` hook.
- **Notes** (`/notes`): Left folder list (from `useFolders` hook), right note list with sort toggle, click to navigate to `/editor/:id`. Uses `useNotesList` hook.
- **Editor** (`/editor/:id`): Install `@tiptap/react @tiptap/starter-kit @tiptap/extension-markdown`. Render Tiptap editor with Markdown extension. Load note via `useNote(id)`, save via debounced `useUpdateNote`. 740px max-w writing column.
- **Calendar** (`/calendar`): Month navigation arrows, today button, 7-column day grid with event dots. Uses `useEventsList` with month param.
- **Inbox** (`/inbox`): Kind filter tabs, item list with archive/delete/convert buttons, batch select via checkboxes. Uses `useInboxList`, `useConvertInbox`, `useBatchInbox` hooks.
- **Search** (`/search`): Auto-focused input, query param `?q=`, results grouped by type using `type` field. Uses `useSearch` hook with debounced input.

These will be implemented in Tasks 15-21 of the plan (one task per route), each committing independently.

---

## Phase 4: Integration

### Task 22: Wire up TanStack Query and replace mock data

- [ ] **Step 1: Seed the database**

```bash
cd backend && make seed
```

- [ ] **Step 2: Start backend and verify all endpoints**

```bash
cd backend && make dev &
# Test each endpoint:
curl http://localhost:4201/api/today | jq
curl http://localhost:4201/api/notes | jq
curl http://localhost:4201/api/tasks | jq
curl http://localhost:4201/api/events?month=2026-05 | jq
curl http://localhost:4201/api/inbox | jq
curl "http://localhost:4201/api/search?q=架构" | jq
curl http://localhost:4201/api/folders | jq
kill %1
```

- [ ] **Step 3: Start frontend and verify all pages render with real data**

```bash
cd frontend && pnpm dev
# Open http://localhost:5173 in browser
# Navigate through all 7 routes
# Verify data loads, check skeleton→content transition
# Test QuickCapture Cmd+Shift+K
# Test Inbox convert flow
```

- [ ] **Step 4: Commit any fixes**

```bash
git add -A
git commit -m "fix: wire up frontend to backend API, fix data integration issues"
```

---

## Implementation Sequence Summary

| Phase | Tasks | Est. Time |
|-------|-------|-----------|
| 1. Backend Foundation | 1-7 | ~4 hrs |
| 2. Frontend Foundation | 8-13 | ~3 hrs |
| 3. Frontend Routes | 14-21 | ~6 hrs |
| 4. Integration | 22 | ~1 hr |
| **Total** | **22 tasks** | **~14 hrs** |

Each task produces a commit. Run backend tests (`make test`) after Task 7, and verify the full stack end-to-end after Task 22.
