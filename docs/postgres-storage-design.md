# FlowSpace 可插拔存储与 PostgreSQL 升级设计

## 背景

FlowSpace 当前后端使用 Go + Gin + `database/sql` + SQLite，启动时读取 `backend/db/schema.sql` 并通过 `FLOWSPACE_ENV` 区分正式库 `flowspace.db` 和测试库 `flowspace.test.db`。当前 SQLite schema 已覆盖笔记、任务、日历、收件箱、同步配置、同步状态和学习 roadmap。

本次升级目标不是把 SQLite SQL 逐行翻译成 PostgreSQL，而是先把数据库访问边界抽象成可插拔 Storage Provider，再把 PostgreSQL 做成正式推荐 provider。这样上层 handler/service 面向稳定的业务 repository 接口，底层 provider 可以分别使用 PostgreSQL 或 SQLite 的合适数据结构。

## 目标

- 后端运行时通过 `FLOWSPACE_DATABASE_DRIVER` 选择 storage provider。
- PostgreSQL 作为正式推荐 provider；SQLite 保留为轻量本地、迁移源和兼容 provider。
- 正式和测试继续隔离：PostgreSQL 使用不同 database 或 schema，SQLite 使用不同文件。
- 保留现有 HTTP API 和现有字符串 ID，降低前端改动和迁移风险。
- 保留 FlowSpace 作为主数据源的同步语义：Notion/Obsidian token 不入库。
- 将搜索、标签、JSON 配置、时间范围等能力封装在 provider 内部，业务层不感知具体数据库实现。
- PostgreSQL provider 按数据特点使用原生结构：关系表、`TEXT[]`、`JSONB`、`TSTZRANGE`、GIN/GiST 索引、`tsvector` + `pg_trgm` 搜索。
- 测试环境使用现有 PostgreSQL 10.6 服务；DDL 必须兼容 PostgreSQL 10.6，不能依赖 PG12+ generated columns。需要派生值时由 provider repository 在同一事务内写入普通列。

## 非目标

- 不在第一阶段引入多用户账户、权限模型或租户隔离。
- 不把笔记正文改成 ProseMirror JSON 存储；先保持 API 兼容，后续可单独升级。
- 不改变前端路由、Tailscale 入口或 Notion/Obsidian 同步按钮行为。
- 不在数据库里保存任何 Notion integration token。
- 不做 Go `.so` 或运行时动态插件加载；本阶段的“可插拔”是编译期注册 provider，运行时配置选择。
- 不承诺支持任意 SQL 数据库；第一阶段只要求 `postgres` 和 `sqlite` 两个 provider 行为一致。

## 环境与连接

| 环境 | provider | 存储目标 | 启动变量 |
| --- | --- | --- | --- |
| 测试 | `postgres` | `flowspace_test` | `FLOWSPACE_ENV=test` + `FLOWSPACE_DATABASE_DRIVER=postgres` + `FLOWSPACE_DATABASE_URL` |
| 正式 | `postgres` | `flowspace_prod` | `FLOWSPACE_ENV=prod` + `FLOWSPACE_DATABASE_DRIVER=postgres` + `FLOWSPACE_DATABASE_URL` |
| 测试 | `sqlite` | `backend/flowspace.test.db` | `FLOWSPACE_ENV=test` + `FLOWSPACE_DATABASE_DRIVER=sqlite` + `FLOWSPACE_SQLITE_PATH` |
| 正式 | `sqlite` | `backend/flowspace.db` | `FLOWSPACE_ENV=prod` + `FLOWSPACE_DATABASE_DRIVER=sqlite` + `FLOWSPACE_SQLITE_PATH` |

PostgreSQL 推荐连接变量：

```text
FLOWSPACE_DATABASE_DRIVER=postgres
FLOWSPACE_DATABASE_URL=postgres://flowspace:flowspace@127.0.0.1:15432/flowspace_test?sslmode=disable&application_name=flowspace-test&options=-c%20statement_timeout=15000
```

SQLite 兼容连接变量：

```text
FLOWSPACE_DATABASE_DRIVER=sqlite
FLOWSPACE_SQLITE_PATH=backend/flowspace.test.db
```

启动安全校验：

- `FLOWSPACE_DATABASE_DRIVER` 只能是 `postgres` 或 `sqlite`；空值在第一阶段默认 `postgres`，但启动日志必须明确打印实际 driver。
- `postgres` provider 必须提供 `FLOWSPACE_DATABASE_URL`。
- `sqlite` provider 必须提供 `FLOWSPACE_SQLITE_PATH`。
- `FLOWSPACE_ENV=test` 时拒绝连接 URL path 为 `flowspace_prod`。
- `FLOWSPACE_ENV=prod` 时拒绝连接 URL path 为 `flowspace_test`。
- `FLOWSPACE_ENV=test` 时拒绝 SQLite path 指向 `flowspace.db`。
- `FLOWSPACE_ENV=prod` 时拒绝 SQLite path 指向 `flowspace.test.db`。
- `ValidateStorageConfig` 负责校验 driver 与环境匹配；具体 provider 的 `Validate` 负责校验 URL/path 是否存在和格式是否正确。
- 后端启动日志必须打印 environment、driver、database name 或 sqlite path，便于确认当前服务连的是测试库还是正式库。

连接池建议：

- `SetMaxOpenConns(10)`、`SetMaxIdleConns(5)`。
- `SetConnMaxLifetime(30 * time.Minute)`、`SetConnMaxIdleTime(5 * time.Minute)`。
- 本地和部署连接串包含 `application_name=flowspace-test|flowspace-prod`。
- 通过 URL `options=-c statement_timeout=15000` 设置默认查询超时，避免长查询卡死服务。

本地开发建议通过 Docker Compose 提供 PostgreSQL：

```yaml
services:
  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: flowspace
      POSTGRES_PASSWORD: flowspace
      POSTGRES_DB: postgres
    ports:
      - "15432:5432"
    volumes:
      - ./docker/postgres/init-flowspace.sql:/docker-entrypoint-initdb.d/010-flowspace.sql:ro
      - flowspace_pg_data:/var/lib/postgresql/data

volumes:
  flowspace_pg_data:
```

`docker/postgres/init-flowspace.sql` 必须显式创建测试库和正式库：

```sql
SELECT 'CREATE DATABASE flowspace_test OWNER flowspace'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'flowspace_test')\gexec

SELECT 'CREATE DATABASE flowspace_prod OWNER flowspace'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'flowspace_prod')\gexec
```

`POSTGRES_DB` 固定为 `postgres`，由初始化脚本统一创建两个业务库，避免 `flowspace_test` 被容器默认创建后初始化脚本再次创建同名数据库。

注意：Docker entrypoint 只会在空数据目录首次启动时执行 `docker-entrypoint-initdb.d`。如果本机已经存在 `flowspace_pg_data` volume，新增 init SQL 不会自动补建 `flowspace_prod`。这种情况下需要二选一：

```powershell
docker compose -f docker-compose.postgres.yml down -v
docker compose -f docker-compose.postgres.yml up -d
```

或手动执行：

```powershell
docker exec -i flowspace-postgres psql -U flowspace -d postgres -f /docker-entrypoint-initdb.d/010-flowspace.sql
```

## Storage Provider 架构

### 分层目标

数据库可插拔发生在 storage 边界，不发生在 handler/service 或零散 SQL helper 中。上层业务只依赖领域 repository 接口，底层 provider 自己决定连接方式、migration、SQL 方言、搜索实现和数据结构。

```text
handler/service
  ↓
repository facade 兼容层
  ↓
storage.Store 领域接口
  ↓
storage/postgres 或 storage/sqlite provider
  ↓
具体数据库
```

第一阶段保留当前 `repository.CreateNote(...)`、`repository.GetTodayTasks(...)` 这类包级函数，降低 handler/service 改动。包级函数内部只做一件事：把调用转发给当前 active `storage.Store`。后续再逐步把 service 改成显式依赖 `storage.Store`。

### 核心接口

新增 `backend/internal/storage` 包：

```go
package storage

import "context"

type Driver string

const (
	DriverPostgres Driver = "postgres"
	DriverSQLite   Driver = "sqlite"
)

type Config struct {
	Env        string
	Driver     Driver
	URL        string
	SQLitePath string
}

type Capabilities struct {
	FullTextSearch bool
	PrefixSearch   bool
	TrigramSearch  bool
	JSONObjects    bool
	ArrayColumns    bool
	TimeRanges      bool
	AdvisoryLocks   bool
}

type Provider interface {
	Driver() Driver
	Validate(Config) error
	Open(context.Context, Config) (Store, error)
	Migrate(context.Context, Config) error
}

type Store interface {
	Close() error
	Health(context.Context) error
	Capabilities() Capabilities
	Transact(context.Context, func(Store) error) error

	Folders() FolderRepository
	Notes() NoteRepository
	Tasks() TaskRepository
	Events() EventRepository
	Inbox() InboxRepository
	Roadmaps() RoadmapRepository
	Sync() SyncRepository
	Search() SearchRepository
}
```

领域 repository 接口只表达业务行为，不暴露 `*sql.DB`、`pgxpool.Pool`、SQLite FTS rowid、PostgreSQL `tsvector` 等实现细节。例如：

```go
type NoteRepository interface {
	List(ctx context.Context, filter NoteFilter) ([]model.Note, int, error)
	GetByID(ctx context.Context, id string) (*model.Note, error)
	Create(ctx context.Context, req *model.CreateNoteRequest) (*model.Note, error)
	CreateWithID(ctx context.Context, note *model.Note) error
	Update(ctx context.Context, id string, req *model.UpdateNoteRequest) (*model.Note, error)
	Delete(ctx context.Context, id string) error
	ListAll(ctx context.Context) ([]model.Note, error)
	Recent(ctx context.Context, limit int) ([]model.Note, error)
}
```

其他接口按当前 repository 函数边界拆分：`FolderRepository`、`TaskRepository`、`EventRepository`、`InboxRepository`、`RoadmapRepository`、`SyncRepository`、`SearchRepository`。接口方法签名应优先沿用现有 model 类型和返回 shape，避免前端/API 在同一阶段被迫改动。

### Provider 注册与启动

新增 provider registry：

```go
type Registry struct {
	providers map[Driver]Provider
}

func (r *Registry) Register(provider Provider) error
func (r *Registry) Open(ctx context.Context, cfg Config) (Store, error)
```

启动路径统一为：

1. `storage.LoadStorageConfig()` 读取 `FLOWSPACE_ENV`、`FLOWSPACE_DATABASE_DRIVER`、`FLOWSPACE_DATABASE_URL`、`FLOWSPACE_SQLITE_PATH`。
2. `storage.Registry.Open(ctx, cfg)` 根据 driver 找 provider。
3. provider 执行 `Validate`、`Open`、必要的 `Migrate`。
4. `repository.SetStore(store)` 设置 active store。
5. 启动日志打印 `env`、`driver`、`database/path`、capabilities。

`repository.DB *sql.DB` 只作为过渡兼容变量保留给尚未迁完的测试或迁移命令；运行时新代码不得直接访问它。完成 provider 迁移后删除 package-level `DB`。

Context 过渡规则：

- 领域接口统一接收 `context.Context`。
- 第一阶段 repository facade 可以用 `context.Background()` 兼容旧 service 调用，但必须集中在 facade 中，不能散落到 provider。
- handler/service 新增或重构代码必须传入 `c.Request.Context()` 或上游 context。
- 第二阶段把高频路径 service 改成显式接收 context：today、search、notes、inbox conversion、sync import/export。

### Provider 职责

`postgres` provider：

- 使用 `pgx` 或 `database/sql` + `pgx` stdlib 连接 PostgreSQL。
- 执行 `backend/db/migrations/postgres/*.sql`。
- 使用 PostgreSQL 原生结构：`TEXT[]`、`JSONB`、`TSTZRANGE`、`tsvector`、`pg_trgm`。
- 在 notes/tasks/events 写事务内维护 `search_index`。
- 提供完整 `Capabilities`：全文、前缀、trigram、JSON object、array、time range、advisory lock。

`sqlite` provider：

- 使用 `modernc.org/sqlite` 打开 SQLite 文件，保留 WAL、busy timeout、foreign keys。
- 执行 `backend/db/migrations/sqlite` 或现有 legacy SQLite migration helper。
- 使用当前 SQLite schema、FTS5、JSON text、Unix 秒字段。
- 对外通过同一 `storage.Store` 接口返回兼容 model。
- 作为本地轻量运行、迁移源、provider contract test 的兼容实现。

### 能力差异原则

业务 API 不能因为 provider 不同改变返回 shape；但 provider 可以在内部用不同实现达到同一语义。例如：

- `SearchRepository.Search` 在 PostgreSQL 走 `tsvector + pg_trgm`，在 SQLite 走 FTS5 + LIKE fallback。
- `EventRepository` 在 PostgreSQL 用 `TSTZRANGE`，在 SQLite 用 `start_time/end_time INTEGER`。
- `SyncRepository` 在 PostgreSQL 用 `JSONB`，在 SQLite 用 JSON text。

如果某个未来功能强依赖 PostgreSQL 能力，必须先在 service 层检查 `Capabilities()`，并返回明确错误，而不是让 SQLite provider 运行到 SQL 错误。

### 事务边界

`Store.Transact` 是跨领域写入的唯一事务入口。Provider 必须保证传入回调的 `Store` 绑定到同一个 transaction；回调返回错误时 rollback，返回 nil 时 commit。

必须纳入事务的流程：

- note/task/event create/update/delete 与 `search_index` 维护。
- inbox conversion：创建 note/task/event 和 `MarkInboxConverted` 必须同事务完成，避免创建目标实体成功但收件箱仍未转换。
- sync import：`CreateNoteWithID` 或 `UpdateNote` 与 `UpsertSyncState` 必须同事务完成，避免笔记导入成功但同步状态缺失。
- roadmap 节点删除：删除节点、相关资源、关联任务解绑或删除策略必须同事务完成。
- SQLite -> PostgreSQL 数据搬运：所有业务表搬运和 search index rebuild 在单个 PostgreSQL transaction 内完成。

Provider contract tests 必须覆盖至少一个跨领域事务 rollback：例如 inbox conversion 中故意让 `MarkInboxConverted` 失败，断言目标 note/task/event 没有留下半成品。

### 非数据库对象存储边界

本设计只抽象关系型业务数据库，不把附件、导出文件、图片、音频等二进制对象塞进 `storage.Store`。如果后续要接入 MinIO/local/S3，应新增独立 `BlobStore`：

```go
type BlobStore interface {
	Put(ctx context.Context, key string, body io.Reader, contentType string) error
	Get(ctx context.Context, key string) (io.ReadCloser, BlobInfo, error)
	Delete(ctx context.Context, key string) error
	SignedURL(ctx context.Context, key string, ttl time.Duration) (string, error)
}
```

数据库只保存 blob metadata 和 key；对象内容由 `BlobStore` provider 管理。

## PostgreSQL Provider 数据结构分层

| 业务数据 | PostgreSQL 结构 | 理由 |
| --- | --- | --- |
| 文件夹、任务项目、任务、roadmap 主表 | 关系表 | 强关系、强约束、常规排序过滤 |
| 笔记标签 | `TEXT[]` | 标签过滤高频，适合 GIN 索引 |
| 同步目标配置 | `JSONB` | Notion/Obsidian 配置字段不同，结构会演进 |
| 同步状态平台扩展字段 | `JSONB` | 公共字段结构化，平台私有字段灵活 |
| 全局搜索 | `search_index` + `TSVECTOR` | 替代 SQLite FTS5，统一 notes/tasks/events |
| 日历事件时间 | `TIMESTAMPTZ` + `TSTZRANGE` | 时间段查询、冲突检测和今日过滤更自然 |
| roadmap 节点图 | 关系表 + edge 表 | 节点是实体，依赖关系是图 |
| 收件箱扩展信息 | `JSONB` | quick capture 来源未来会扩展 |

## PostgreSQL Provider 推荐 Schema

### 扩展

```sql
CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public;
```

第一阶段不强制启用 `uuid-ossp`，继续由 Go 生成现有字符串 ID。

测试使用独立 schema 时连接 `search_path` 必须包含 `public`，例如 `options=-c search_path=fs_test_xxx,public`。这样业务表落在测试 schema，`pg_trgm` 的 operator class 和函数仍可从 `public` 扩展 schema 解析到。

### 文件夹与笔记

```sql
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
CREATE INDEX notes_title_trgm_idx ON notes USING GIN (title gin_trgm_ops);

INSERT INTO folders (id, name, sort_order, created_at)
VALUES
  ('__uncategorized', '未分类', 0, now()),
  ('__work', '工作', 1, now()),
  ('__personal', '个人', 2, now())
ON CONFLICT (id) DO NOTHING;
```

迁移规则：

- SQLite `notes.tags` 当前是 JSON 字符串，例如 `["技术","数据库"]`。
- PostgreSQL 中转换为 `TEXT[]`。
- API 第一阶段可继续返回 JSON 字符串，repository 层负责 `TEXT[] <-> JSON string` 兼容；第二阶段再让前端模型升级为数组。

### 任务与项目

```sql
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
CREATE INDEX tasks_title_trgm_idx ON tasks USING GIN (title gin_trgm_ops);

INSERT INTO task_projects (id, name, type, description, created_at, updated_at)
VALUES ('personal', '个人', 'personal', '默认个人任务项目', now(), now())
ON CONFLICT (id) DO NOTHING;
```

迁移规则：

- SQLite `due INTEGER` 转 `due_at TIMESTAMPTZ`。
- SQLite `planned_date TEXT` 转 `DATE`。
- SQLite `done INTEGER` 转 `BOOLEAN`。
- 第一阶段保留 `project TEXT` 兼容旧 API 返回；后续可删除。

### Roadmap

```sql
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
```

说明：

- `position JSONB` 比独立 `x/y` 更便于后续保存布局算法元数据，但 repository 可继续映射为 `X`、`Y`。
- `article_search_queries TEXT[]` 适合 roadmap 节点自动资源搜索。

### 日历事件

```sql
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
CREATE INDEX events_title_trgm_idx ON events USING GIN (title gin_trgm_ops);
```

迁移规则：

- SQLite `start_time/end_time INTEGER` 转 `TIMESTAMPTZ`。
- 今日视图可以用 `time_range && tstzrange(day_start, day_end, '[)')` 查询。

### Inbox

```sql
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
```

### 同步目标与同步状态

```sql
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
```

`sync_targets.config` 示例：

```json
{
  "data_source_id": "xxx",
  "token_env": "FLOWSPACE_NOTION_TOKEN",
  "title_property": "Name",
  "required_tags": ["sync", "publish"]
}
```

`note_sync_state.external_metadata` 示例：

```json
{
  "notion_parent_page_id": "xxx",
  "notion_last_edited_by": "user-id",
  "obsidian_frontmatter": {
    "tags": ["sync"]
  }
}
```

输入校验规则：

- `sync_targets.type` 只允许 `obsidian`、`notion`，handler 必须在入库前返回 400，不能依赖 PostgreSQL CHECK 变成 500。
- `sync_targets(type,name)` 唯一冲突必须映射为 409 或明确的冲突提示。
- `config` 必须在 handler/repository 入库前校验为 JSON object；空配置规范化为 `{}`，不能接受 `null`、数组、字符串或数字。Notion token 仍然只保存环境变量名，不保存 token 值。

### 统一搜索

```sql
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
CREATE INDEX search_index_title_trgm_idx ON search_index USING GIN (title gin_trgm_ops);
CREATE INDEX search_index_content_trgm_idx ON search_index USING GIN (content gin_trgm_ops);
CREATE INDEX search_index_updated_idx ON search_index (updated_at DESC);
```

同步策略：

- 第一阶段通过 repository 写入时同步 upsert `search_index`，避免 trigger 复杂度。
- PostgreSQL 10.6 不支持 stored generated columns；`search_vector` 必须由 repository/provider 用 `setweight(to_tsvector(...))` 写入。
- 源表写入和 `search_index` 维护必须在同一个数据库事务中完成；notes/tasks/events 的 create/update/delete 不能先提交源表再单独更新索引。
- `CreateNote` 和同步导入使用的 `CreateNoteWithID` 都必须 upsert note 的 `search_index` 行；`UpdateNote` 更新索引，`DeleteNote` 在同一事务内删除索引行。
- task/event 的 create/update/delete 同样在同一事务内维护索引；event 的 `search_index.content` 至少包含 `location` 和 `kind`，保留 SQLite FTS 时代按地点搜索事件的能力。
- 后续如需要更强一致性，可改成数据库 trigger。
- 中文/CJK 搜索优先走 `pg_trgm` fallback，英文/分词走 `tsvector`。

查询策略：

- `SearchNotes`、`SearchTasks`、`SearchEvents` 继续返回现有 `Highlight` 字段。
- PostgreSQL 实现不能只用 `plainto_tsquery`；现有 SQLite FTS5 会把每个词变成 `word*` 前缀匹配。PostgreSQL 需要生成 prefix tsquery，例如把 `Post migr` 转成 `to_tsquery('simple', 'Post:* & migr:*')`，并用同一个 tsquery 调 `ts_headline`。
- prefix tsquery builder 必须先清洗 token，只保留可安全进入 `to_tsquery` 的词；构造失败、纯 CJK 或 token 过短时不能调用空 `to_tsquery`，必须走 trigram/substring fallback。
- CJK 和短词 fallback 使用 `title ILIKE '%' || $1 || '%'`、`content ILIKE '%' || $1 || '%'`、内容相似度、标签精确匹配和 `#tag` 归一化；排序不能只看 title similarity，否则 content-only/tag-only 结果会被压到后面。
- 空白搜索必须在 Go 层直接返回空结果；SQL fallback 也要保留 `length(trim($1)) > 0` 防线，避免 `ILIKE '%%'` 扫全表。
- `search_index` 只保存统一可搜索文本；返回 `folder_id`、`done`、`kind` 等 API 字段时必须按 `entity_type/entity_id` join 回 `notes`、`tasks`、`events` 原表。
- 搜索查询必须过滤掉源表已不存在的 stale index 行：join 回源表后，只返回 `note` 且 `notes.id IS NOT NULL`、`task` 且 `tasks.id IS NOT NULL`、`event` 且 `events.id IS NOT NULL` 的结果。
- 搜索结果必须按 `(entity_type, entity_id)` 去重，避免同一实体同时命中 `tsvector` 和 trigram fallback 时重复返回。
- 搜索总数必须用去重后的结果计算，例如 `COUNT(*) FROM deduped`，不能用分页 SQL 的行数。
- `SearchPostgres` 测试必须覆盖：英文命中、英文前缀命中（例如 `Post migr` 命中 `PostgreSQL migration`）、中文命中、标签命中、event location 命中、highlight 包含 `<mark>`、返回 note folder/task done/event kind、同一实体不重复、过滤 stale index 行、`total` 与去重实体数一致。

## PostgreSQL Provider Migration Runner

迁移执行必须幂等，不能在每次启动时重复执行所有 SQL 文件。

推荐规则：

- `runPostgresMigrations` 启动时先创建 `schema_migrations`。
- 每个 migration 文件使用文件名作为 version，例如 `0001_init_postgres.sql`。
- 执行前计算 SQL 文件 SHA-256 checksum。
- 如果 `schema_migrations.version` 已存在且 checksum 一致，直接跳过。
- 如果 version 已存在但 checksum 不一致，返回错误，禁止静默覆盖已执行 migration。
- 每个 migration 在独立事务中执行，DDL 和 `schema_migrations` 写入同成同败。
- 每个 migration 的事务内必须先执行 `pg_advisory_xact_lock(hashtext('flowspace_schema_migrations'))`，并在拿到锁之后重新检查 `schema_migrations`，避免两个后端进程同时启动时都判断同一个 migration 尚未执行。
- `0001_init_postgres.sql` 可以使用普通 `CREATE INDEX`；幂等性由 migration runner 保证，不依赖所有 DDL 都写 `IF NOT EXISTS`。
- 初始化默认数据必须放在 migration 文件中，例如 `folders.__uncategorized`、`folders.__work`、`folders.__personal` 和 `task_projects.personal` 使用 `INSERT ... ON CONFLICT DO NOTHING`。
- 第一阶段不创建 search refresh trigger；`search_index` 由 repository 写入时维护。

## 代码架构调整

### 当前问题

当前 repository 直接使用全局 `repository.DB *sql.DB`，SQL 中使用 SQLite 占位符 `?`，测试大量使用 `sql.Open("sqlite", ":memory:")`。如果直接把这些文件改成 PostgreSQL SQL，会得到一个难以切换的单数据库实现。可插拔设计需要同时解决：

- repository 包级函数与具体数据库解耦。
- provider 内部允许使用不同连接驱动和 SQL 方言。
- 事务要能跨领域 repository 保持一致，例如 note 写入和 search index upsert。
- 同一套业务 contract tests 能同时验证 SQLite 和 PostgreSQL provider。
- 迁移命令可以把 SQLite provider 作为 source，把 PostgreSQL provider 作为 target。

### 推荐代码分层

```text
backend/internal/storage/
  store.go             # Store、Provider、Capabilities、领域 repository 接口
  config.go            # FLOWSPACE_DATABASE_DRIVER/URL/path 配置解析
  registry.go          # provider 注册、选择、打开
  contract_tests.go    # provider 共享行为测试 helper

backend/internal/storage/postgres/
  provider.go          # postgres Provider.Open/Validate/Migrate
  migrations.go        # schema_migrations runner
  tx.go                # Transact 实现
  types.go             # unix/time、TEXT[]、JSONB 兼容转换
  builder.go           # PostgreSQL placeholder、IN、动态 SET helper
  notes.go
  tasks.go
  events.go
  search.go
  inbox.go
  roadmaps.go
  sync.go

backend/internal/storage/sqlite/
  provider.go          # sqlite Provider.Open/Validate/Migrate
  legacy_migrations.go # 当前 migrateDB() 逻辑抽出复用
  tx.go
  notes.go
  tasks.go
  events.go
  search.go
  inbox.go
  roadmaps.go
  sync.go

backend/internal/repository/
  facade.go            # 兼容现有包级函数，转发给 active storage.Store
  legacy_db.go         # 过渡期保留 DB/InitDB，完成迁移后删除
```

第一阶段迁移原则：

- 不在 handler/service 层直接引入 `postgres` 或 `sqlite` 包。
- 新增或修改业务查询时，先改 `storage` 领域接口和 provider contract test。
- SQLite provider 可以先复用当前 SQL，PostgreSQL provider 使用新 schema 和 SQL。
- repository facade 只允许做参数转发、`context.Background()` 兼容和错误透传，不写 SQL。
- 跨表一致性由 `Store.Transact` 提供，同一事务内的 store 必须返回绑定到同一 transaction 的领域 repository。

### SQLite 专属 SQL 替换清单

这些替换只发生在 `storage/postgres` provider 内部，不能泄漏到 facade 或 service。迁移 PostgreSQL provider 时必须逐项处理这些 SQLite 专属语法，不能只机械替换占位符：

| 当前 SQLite 写法 | PostgreSQL 写法 |
| --- | --- |
| `?` placeholder | `$1`、`$2`，通过 helper 按参数顺序生成 |
| 动态 `IN (?, ?, ?)` | helper 生成 `$n` 列表，例如 `id IN ($2,$3)` |
| 动态 `SET column = ?` | helper 返回 `column = $n` 并追加 args |
| `COUNT(n.rowid)` | `COUNT(n.id)` |
| `JOIN ... ON n.rowid = fts.rowid` | 改查 `search_index.entity_id` 并 join 源表 ID |
| `COLLATE NOCASE` | `LOWER(name) ASC` 或显式 PostgreSQL collation |
| `done = 1` / `archived = 0` | `done = true` / `archived = false` |
| `date(value, 'unixepoch', 'localtime')` | repository 传入 UTC date 或 `to_timestamp(value)::date` |
| `LIKE` 默认大小写行为 | `ILIKE` 或 `LOWER(column) LIKE LOWER($n)` |
| FTS5 `MATCH` / `snippet()` | prefix `to_tsquery` / `ts_headline`，短词和 CJK 走 trigram fallback |

建议新增 `backend/internal/storage/postgres/builder.go`，集中提供：

- `pgPlaceholder(index int) string`
- `pgPlaceholders(start, count int) string`
- `pgInClause(column string, start, count int) (string, error)`
- `pgSetBuilder` 用于动态 `UPDATE ... SET`

`BatchArchiveInbox`、`BatchDeleteInbox`、`UpdateNote`、`UpdateTask`、`UpdateTaskProject`、`UpdateEvent`、`UpdateRoadmapNode` 的 PostgreSQL 实现都必须通过这些 helper 生成动态 SQL。

## 迁移策略

### 阶段 0：备份

- 停止正式服务。
- 复制 `backend/flowspace.db`、`backend/flowspace.db-wal`、`backend/flowspace.db-shm` 到 `backend/db-backups/`。
- 测试库也做一次备份，用于演练。
- 迁移命令生成工作副本时必须 WAL-safe。推荐在服务停止后用 SQLite `VACUUM INTO` 从源库生成临时迁移副本；不要只复制主 `.db` 文件，因为 WAL 模式下已提交数据可能仍在 `.db-wal`。

### 阶段 1：搭 PostgreSQL 测试库

- 增加本地 PostgreSQL 启动方式。
- 创建 `flowspace_test`。
- 执行 migration。
- 只切测试后端，不碰正式服务。

### 阶段 2：迁移 SQLite 数据

新增命令：

```text
backend/cmd/migrate_sqlite_to_pg
```

输入：

```powershell
$env:SQLITE_DB_PATH = "backend/flowspace.test.db"
$env:FLOWSPACE_DATABASE_URL = "postgres://flowspace:flowspace@127.0.0.1:15432/flowspace_test?sslmode=disable"
go run ./cmd/migrate_sqlite_to_pg
```

转换规则：

| SQLite 字段 | PostgreSQL 字段 |
| --- | --- |
| `created_at INTEGER` | `to_timestamp(created_at)` |
| `updated_at INTEGER` | `to_timestamp(updated_at)` |
| `due INTEGER` | `to_timestamp(due)` or `NULL` |
| `planned_date TEXT` | `DATE` |
| `done INTEGER` | `BOOLEAN` |
| `tags TEXT JSON` | `TEXT[]` |
| `config_json TEXT` | `JSONB` |
| `external_mtime INTEGER` | `TIMESTAMPTZ` |

迁移顺序：

1. `folders`
2. `notes`
3. `task_projects`
4. `learning_roadmaps`
5. `roadmap_nodes`
6. `tasks`
7. `roadmap_edges`
8. `roadmap_resources`
9. `events`
10. `inbox`
11. `sync_targets`
12. `note_sync_state`
13. rebuild `search_index`

数据迁移事务规则：

- 迁移命令不得直接修改用户传入的 SQLite 文件；必须先用 WAL-safe 方式生成临时副本（优先 `VACUUM INTO`），在副本上执行现有 SQLite schema upgrade（等价于当前 `migrateDB()` 补列逻辑），再对副本做预检和读取。
- `RunPostgresMigrations` 可以先执行，用于创建 schema、索引和默认 seed。
- 正式搬运 SQLite 数据前必须先跑 `validateSQLiteSource`，对旧 SQLite 数据做预检：`PRAGMA foreign_key_check`、负数 `tasks.priority`、非法 task/project/roadmap/sync 状态或类型、非法 `tasks.horizon/scope`、bool 字段不是 `0/1`、非法 `planned_date` 日期格式、`events.end_time <= start_time`、重复 `sync_targets(type,name)`、非法 JSON（例如 `notes.tags`、`sync_targets.config_json`）、`sync_targets.config_json` 非空但不是 JSON object、`notes.tags` 不是 `[]string`、Unix 秒字段为负数或疑似毫秒时间戳都要在触碰 PostgreSQL 业务数据前报清楚。
- 正式搬运 SQLite 数据前，迁移命令必须检查目标业务表为空；允许存在 migration 默认 seed：`folders.__uncategorized`、`folders.__work`、`folders.__personal` 和 `task_projects.personal`。
- SQLite 数据搬运必须在单个 PostgreSQL transaction 中完成。
- `migrateFolders`、`migrateNotes`、`migrateTasks` 等 helper 接收 transaction/executor，而不是各自直接使用 `*sql.DB`。
- `migrateFolders` 和 `migrateTaskProjects` 遇到 migration 默认 seed 主键时必须使用源库数据覆盖 seed，例如 `ON CONFLICT (id) DO UPDATE SET name = excluded.name, sort_order = excluded.sort_order`，保留用户在 SQLite 中改过的默认名称、排序和项目描述。
- 任意后段 helper 失败时必须 rollback，不能留下部分 notes/tasks/events/roadmaps/sync state。
- 回滚测试至少制造一个 late-table 失败，例如 SQLite event `end_time <= start_time` 触发 PostgreSQL `CHECK (end_at > start_at)`，然后断言目标 `notes/tasks/events/search_index` 仍为空。

### 阶段 3：切测试服务

- `FLOWSPACE_ENV=test`
- `FLOWSPACE_DATABASE_DRIVER=postgres`
- `FLOWSPACE_DATABASE_URL` 指向 `flowspace_test`
- 后端启动时通过 provider registry 打开 PostgreSQL provider，不再走 legacy `repository.DB` 初始化。
- 如需回归 SQLite 兼容 provider，显式设置 `FLOWSPACE_DATABASE_DRIVER=sqlite` 和 `FLOWSPACE_SQLITE_PATH=backend/flowspace.test.db`。
- 前端仍用 `4100/4100` 测试入口。

### 阶段 4：正式迁移

- 测试库稳定后，先做 dry-run：用最近的 SQLite 备份迁移到临时 PostgreSQL schema/database，跑完整后端测试、前端 smoke test、逐表 count 和关键表抽样校验。
- 正式切换前冻结写入：停止正式后端、同步任务和任何会写 SQLite 的脚本，确认没有残留进程持有 `flowspace.db`。
- 备份 SQLite：保留 `flowspace.db`、`flowspace.db-wal`、`flowspace.db-shm`，并记录文件大小、修改时间和 SHA-256。
- 迁移到 `flowspace_prod` 空库；迁移完成后执行逐表 count，对 `notes/tasks/events/inbox/sync_targets/note_sync_state/learning_roadmaps/roadmap_nodes` 做抽样内容校验。
- 对关键表做轻量 checksum：例如按 `id` 排序聚合 `id || updated_at` 的 hash，用于发现漏行或明显时间字段错误。
- 验证 `search_index`：抽样搜索 note 正文、task 标题、event location、tag，并确认 stale index 行不会返回。
- 成功后立即执行 `pg_dump`，保存为切换后 PostgreSQL 基线备份。
- 正式后端设置 `FLOWSPACE_ENV=prod`、`FLOWSPACE_DATABASE_DRIVER=postgres` 和 `FLOWSPACE_DATABASE_URL`，启动后检查启动日志中的 environment、driver、database name。
- 失败回滚：停止 PostgreSQL 后端，恢复原 `FLOWSPACE_ENV=prod` SQLite 配置和备份文件；如果 PostgreSQL 已写入新数据，先 `pg_dump` 留证，再丢弃失败库或恢复到迁移前 dump。
- 保留 SQLite 文件备份和切换后 `pg_dump` 至少一个发布周期。

## Repository SQL 调整要点

### 占位符

SQLite：

```sql
WHERE id = ?
```

PostgreSQL：

```sql
WHERE id = $1
```

### Upsert

当前 SQLite upsert 基本可迁移，但注意 `excluded` 语义和 bool/json 参数类型：

```sql
INSERT INTO sync_targets (
  id,
  type,
  name,
  vault_path,
  base_folder,
  config,
  enabled,
  auto_sync,
  created_at,
  updated_at
)
VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8, now(), now())
ON CONFLICT (id) DO UPDATE SET
  type = excluded.type,
  name = excluded.name,
  vault_path = excluded.vault_path,
  base_folder = excluded.base_folder,
  config = excluded.config,
  enabled = excluded.enabled,
  auto_sync = excluded.auto_sync,
  updated_at = now()
```

### 时间

对外 API 第一阶段仍返回 Unix 秒，repository 内部统一使用 `time.Time` 与 `TIMESTAMPTZ`：

```go
func unixToTime(value int64) time.Time {
    return time.Unix(value, 0).UTC()
}

func timeToUnix(value time.Time) int64 {
    return value.Unix()
}
```

### 标签

第一阶段模型继续保持：

```go
Tags string `json:"tags"`
```

repository 查询时将 `TEXT[]` 编码成 JSON 字符串，避免前端立刻改：

```go
func tagsArrayToJSONString(tags []string) string {
    if tags == nil {
        return "[]"
    }
    data, _ := json.Marshal(tags)
    return string(data)
}
```

第二阶段可把模型升级为：

```go
Tags []string `json:"tags"`
```

## 测试策略

### 单元测试

- config 层测试 `FLOWSPACE_DATABASE_DRIVER`、`FLOWSPACE_DATABASE_URL`、`FLOWSPACE_SQLITE_PATH` 的 provider 选择和环境安全校验。
- storage registry 测试未知 driver、重复 provider 注册、provider validate/open 调用顺序。
- repository facade 测试包级函数转发到 active `storage.Store`，且没有直接触碰 legacy `DB`。
- SQL helper 测试时间、标签、JSONB 转换。
- migration parser 测试 SQLite tags/config 转换。

### Provider Contract Tests

同一套 contract tests 必须能跑在 SQLite provider 和 PostgreSQL provider 上。contract tests 只断言业务语义，不断言底层表结构。

最低覆盖：

- notes/folders：创建、指定 ID 创建、更新、删除、tags round-trip、recent notes。
- tasks/task projects：项目选择、今日任务、长期任务进行中进入今日、roadmap node 关联任务。
- events：当天、跨天、本地日期边界、location 搜索。
- inbox：创建、批量归档/删除、转换为 note/task/event 时事务 rollback。
- search：note/task/event 命中、标签命中、前缀命中、删除后不可搜。
- sync：sync target config object 校验、sync state upsert、按 target 列表排序、导入笔记与 sync state 同事务。
- roadmaps：nodes/edges/resources round-trip、`article_search_queries`、跨 roadmap parent/edge 不允许。

PostgreSQL provider 可以有额外专属测试，例如 `JSONB`、`TEXT[]`、`TSTZRANGE`、`pg_trgm`、advisory lock。SQLite provider 可以有额外专属测试，例如 WAL、FTS5、legacy migration。

### PostgreSQL 集成测试

使用 PostgreSQL 测试库，不能用 SQLite in-memory 替代。

建议测试环境变量：

```text
FLOWSPACE_TEST_DATABASE_URL=postgres://flowspace:flowspace@127.0.0.1:15432/flowspace_test?sslmode=disable
```

测试原则：

- 不在测试中执行 `DROP SCHEMA public CASCADE`。
- 每个 package 的 PostgreSQL 集成测试使用独立 schema，例如 `fs_test_repository_<random>`、`fs_test_migration_<random>`。
- 测试连接通过 PostgreSQL `options=-c search_path=<schema>,public` 指向独立 schema 并保留 public 扩展可见性；migration SQL 不写死业务表的 `public.`。
- repository 测试可在自己的 schema 内 truncate 业务表并重新 seed 默认数据。
- migration 测试使用独立 database 或独立 schema，并验证 migration runner 可连续执行两次。
- repository 测试必须覆盖真实 PostgreSQL SQL。
- today/calendar 测试必须覆盖真实入口：`GetEvents`、`GetTodayEvents`、`GetTodayTasks`，并用本地日期边界验证 00:30、23:30、跨天事件、只有 `planned_date` 没有 `due_at` 的任务不会偏一天。

### 迁移验收

迁移测试必须断言：

- 迁移后的业务表行数与 SQLite 源数据一致；允许 PostgreSQL migration seed 行和源库默认行合并后的差异，但必须明确断言 seed 是否被源库覆盖。
- `notes.tags` 从 JSON 字符串正确转为 `TEXT[]`。
- `sync_targets.config_json` 正确转为 `JSONB`。
- `events.time_range` 可以查询当天事件。
- `search_index` 能搜到 notes/tasks/events。
- roadmap node/edge/resource 关系完整。
- Notion token 未出现在 PostgreSQL 任意表。

## 风险与规避

| 风险 | 规避 |
| --- | --- |
| PostgreSQL SQL 改动面大 | 分任务迁移 repository，每个业务域独立红绿测试 |
| 时间字段时区偏移 | 全部入库 UTC，API 出参继续 Unix 秒 |
| 标签 API 类型变化影响前端 | 第一阶段 repository 保持 JSON 字符串兼容 |
| 搜索中文效果不如 SQLite fallback | `tsvector` + `pg_trgm` 双路径 |
| 正式库误写 | 先只切测试库，正式必须显式 `FLOWSPACE_ENV=prod` 和 prod URL |
| 数据迁移不可逆 | SQLite 文件只读备份，迁移脚本可重复写入空 PostgreSQL 库 |

## 推荐实施顺序

1. 增加 PostgreSQL 连接配置和本地 Docker Compose。
2. 增加 migration 框架和 PostgreSQL schema。
3. 增加迁移命令和迁移测试。
4. 先迁移 notes/folders/search。
5. 迁移 tasks/task_projects/today。
6. 迁移 events/inbox。
7. 迁移 roadmaps。
8. 迁移 sync targets/state 与 Notion/Obsidian 同步测试。
9. 切测试服务。
10. 更新 README、service ports、启动脚本。
11. 备份并切正式服务。
