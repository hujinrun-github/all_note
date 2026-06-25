# 多用户登录与账号管理平台设计

## 背景

FlowSpace 现在已经有一个前端登录页，但它只是本地表单与视觉预览，没有真实认证流程。后端 `router.Setup()` 直接注册所有 `/api/*` 业务接口，没有认证中间件；存储层虽然已经抽象为 `storage.Store` 和各类 repository，但当前业务表是全局数据模型，没有用户或租户维度。

这意味着只做前端路由隐藏不能满足多用户要求。真正的隔离必须从认证身份进入请求上下文开始，贯穿 handler、service、repository、数据库唯一约束、搜索索引、同步状态和账号管理权限。

## 目标

1. 增加真实登录、退出、当前用户会话查询能力。
2. 支持多个用户使用同一套部署，每个用户默认拥有独立 workspace。
3. 所有笔记、任务、日历、收件箱、搜索、同步目标、roadmap、重复任务数据按 workspace 隔离。
4. 增加账号管理平台，让管理员可以创建、禁用、重置用户账号。
5. 迁移现有单用户数据到首个管理员的默认 workspace。
6. 保持当前 React/Vite 前端和 Go/Gin 后端技术栈，不引入外部身份服务作为 v1 依赖。

## 非目标

v1 不做以下能力：

- OAuth / GitHub 登录。现有 GitHub 登录按钮改为隐藏或禁用说明，不接入第三方 OAuth。
- 注册开放入口。用户由管理员创建，降低私有部署的滥用风险。
- 团队协作编辑、共享笔记、跨 workspace 权限继承。
- 细粒度资源 ACL，例如单篇笔记分享给另一个用户。
- 多因素认证、邮箱验证、找回密码邮件流。
- 管理员直接浏览普通用户业务数据。
- PostgreSQL Row Level Security 强制策略。v1 采用应用层强制过滤加数据库唯一约束，RLS 作为后续增强。

## 当前架构观察

### 前端

- 路由在 `frontend/src/router.tsx` 中集中注册。
- `/login` 已经存在，工作台页面挂在 `App` 下。
- API client 在 `frontend/src/api/client.ts` 统一封装 `fetch`，当前没有携带 Cookie 或处理 401。
- 布局组件 `Sidebar` 和 `TopBar` 是账号入口、退出按钮、账号管理入口的自然位置。

### 后端

- Gin router 在 `backend/internal/router/router.go` 中集中注册，目前 `/api/health` 与所有业务接口同级。
- CORS 中间件已经允许 `Authorization`，但 v1 采用 Cookie session 后还需要允许 credentials。
- `repository.SetStore()` 保存全局 active store。部分 handler 已经直接使用 `repository.ActiveStore()`，部分 service 仍通过旧 facade 函数访问 repository。
- Postgres 是默认部署存储；SQLite provider 仍用于本地、测试和兼容场景。

### 数据模型

当前核心表包括：

- `folders`
- `notes`
- `task_projects`
- `note_project_links`
- `tasks`
- `task_recurrence_rules`
- `task_occurrences`
- `learning_roadmaps`
- `roadmap_nodes`
- `roadmap_edges`
- `roadmap_resources`
- `events`
- `inbox`
- `sync_targets`
- `note_sync_bindings`
- `sync_external_claims`
- `note_sync_suppressions`
- `sync_import_tombstones`
- `note_sync_state`
- `search_index`

其中 `search_index`、`sync_targets`、`sync_external_claims` 是隔离风险最高的表。若这些表仍是全局维度，即使普通列表接口隔离正确，也会通过搜索或同步流程泄露或串写数据。

## 方案选型

### 方案 A：只给业务表加 `user_id`

每条 note/task/event 等数据直接归属于一个用户。

优点：

- 表结构直观。
- v1 改动量略小。

缺点：

- 未来很难支持团队、共享空间、家庭空间或导入多个空间。
- `task_projects`、`sync_targets` 这类配置资源会被绑死到个人，后续迁移成本高。
- 管理员视角和账号生命周期与业务空间耦合过紧。

### 方案 B：用户 + workspace 隔离

引入 `users`、`workspaces`、`workspace_members`。业务数据归属于 workspace，用户通过 membership 访问 workspace。v1 每个用户创建一个默认 workspace。

优点：

- 隔离边界明确，所有业务数据统一以 `workspace_id` 过滤。
- 未来可自然扩展为团队协作或多空间切换。
- 管理员账号管理与业务数据所有权解耦。
- 与同步目标、项目、文件夹、roadmap 等“空间级配置”更匹配。

缺点：

- 迁移和唯一约束比 `user_id` 稍复杂。
- 请求上下文需要同时携带 `user_id` 和 `workspace_id`。

### 方案 C：每用户独立数据库或 schema

每个用户使用独立数据库、Postgres schema 或 SQLite 文件。

优点：

- 数据物理隔离强。
- 单用户导出和清理简单。

缺点：

- 当前 `storage.Store` 以单连接池、单 active store 为核心，改造成本很高。
- 迁移、搜索、账号管理、同步任务调度复杂度明显上升。
- 小规模私有部署没有必要。

## 推荐决策

采用方案 B：用户 + workspace 隔离。

v1 规则：

- 每个用户有且仅自动创建一个默认 workspace。
- 用户登录后默认进入自己的默认 workspace。
- 默认 workspace 由 `users.default_workspace_id` 指向；v1 同时用 `UNIQUE(workspaces.owner_user_id)` 保证一个用户只有一个 owned workspace。
- 管理员也是普通用户，只是额外拥有账号管理权限。
- 所有业务 repository 查询必须按 `workspace_id` 过滤。
- URL 暂不暴露 workspace 切换入口；`workspace_id` 来自服务端会话，不信任客户端传入。

## 认证与会话

### Session 模型

v1 使用服务端 session + HttpOnly Cookie。

原因：

- 前端不需要保存 access token，降低 XSS 后账号被直接盗用的风险。
- Go 后端可直接通过 session 表撤销、过期、轮换会话。
- 私有部署和同源 nginx 反向代理场景下 Cookie 模式最简单。

Cookie 设置：

```text
Name: fs_session
HttpOnly: true
Secure: true in prod, false only for local http dev
SameSite: Lax
Path: /
Max-Age: follows remember_me
```

登录时如果 `remember_me=true`，session 有效期为 30 天；否则为 12 小时。每次 `GET /api/auth/me` 不自动延长过期时间，避免会话无限续期。后续可增加滑动过期策略。

### 密码存储

密码只保存哈希，不保存明文。推荐 `bcrypt`，成本参数 12。

Go 依赖使用：

```text
golang.org/x/crypto/bcrypt
```

`users.password_hash` 保存 bcrypt hash。管理员设置临时密码或用户修改密码时生成新 hash；管理员设置临时密码会撤销该用户所有现有 session，用户主动改密只撤销除当前 session 以外的其他 session。

### 错误策略

登录失败统一返回：

```json
{
  "error": {
    "code": "INVALID_CREDENTIALS",
    "message": "邮箱或密码不正确"
  }
}
```

不区分邮箱不存在、密码错误、账号被禁用的内部细节。账号被禁用在认证成功后返回：

```json
{
  "error": {
    "code": "ACCOUNT_DISABLED",
    "message": "账号已停用，请联系管理员"
  }
}
```

### CSRF

v1 同源部署下 `SameSite=Lax` 已能覆盖常规表单跨站请求风险。因为 API 会修改用户数据，仍增加一层轻量防护：

- 所有非 GET/HEAD/OPTIONS 请求检查 `Origin` 或 `Referer` 是否为允许来源。
- CORS 仅允许配置中的前端 origin，并在跨端口本地开发时返回 `Access-Control-Allow-Credentials: true`。
- 不接受跨站 credentials。

后续如果需要跨域部署，再增加双提交 CSRF token。

## 数据模型

### 新增表

Postgres schema：

```sql
CREATE TABLE users (
  id TEXT PRIMARY KEY,
  email TEXT NOT NULL,
  display_name TEXT NOT NULL DEFAULT '',
  password_hash TEXT NOT NULL,
  must_change_password BOOLEAN NOT NULL DEFAULT false,
  default_workspace_id TEXT NOT NULL,
  role TEXT NOT NULL DEFAULT 'user'
    CHECK (role IN ('admin', 'user')),
  status TEXT NOT NULL DEFAULT 'active'
    CHECK (status IN ('active', 'disabled')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_login_at TIMESTAMPTZ,
  password_changed_at TIMESTAMPTZ
);

CREATE UNIQUE INDEX users_email_lower_idx ON users (lower(email));

CREATE TABLE workspaces (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  owner_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX workspaces_single_owner_v1_idx
  ON workspaces (owner_user_id);

CREATE UNIQUE INDEX workspaces_owner_workspace_idx
  ON workspaces (owner_user_id, id);

ALTER TABLE users
  ADD CONSTRAINT users_default_owned_workspace_fk
  FOREIGN KEY (id, default_workspace_id) REFERENCES workspaces(owner_user_id, id)
  DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE workspace_members (
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role TEXT NOT NULL DEFAULT 'owner'
    CHECK (role IN ('owner', 'member')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, user_id)
);

CREATE TABLE sessions (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  token_hash TEXT NOT NULL UNIQUE,
  user_agent TEXT NOT NULL DEFAULT '',
  ip_address TEXT NOT NULL DEFAULT '',
  expires_at TIMESTAMPTZ NOT NULL,
  revoked_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX sessions_user_active_idx
  ON sessions (user_id, expires_at)
  WHERE revoked_at IS NULL;

CREATE TABLE audit_events (
  id TEXT PRIMARY KEY,
  actor_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
  target_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
  workspace_id TEXT REFERENCES workspaces(id) ON DELETE SET NULL,
  action TEXT NOT NULL,
  metadata JSONB NOT NULL DEFAULT '{}',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX audit_events_created_idx ON audit_events (created_at DESC);
CREATE INDEX audit_events_actor_idx ON audit_events (actor_user_id, created_at DESC);
```

SQLite 使用同名表，时间字段按现有 SQLite 习惯使用 Unix 秒整数，JSON 使用 TEXT 保存。repository 层继续对外暴露 Unix 秒或 Go struct，不让 handler 感知 provider 差异。

### 业务表新增 `workspace_id`

以下表新增 `workspace_id TEXT NOT NULL REFERENCES workspaces(id)`：

- `folders`
- `notes`
- `task_projects`
- `tasks`
- `learning_roadmaps`
- `events`
- `inbox`
- `sync_targets`
- `search_index`

以下表通过父表可推导 workspace，但为了查询性能和防串写，也直接增加或调整复合外键：

- `note_project_links(workspace_id, note_id, project_id)`
- `task_recurrence_rules(workspace_id, task_id)`
- `task_occurrences(workspace_id, task_id, occurrence_date)`
- `roadmap_nodes(workspace_id, roadmap_id, id)`
- `roadmap_edges(workspace_id, roadmap_id, source_node_id, target_node_id)`
- `roadmap_resources(workspace_id, node_id)`
- `note_sync_bindings(workspace_id, note_id, target_id)`
- `sync_external_claims(workspace_id, external_key, note_id, target_id)`
- `note_sync_suppressions(workspace_id, note_id, target_id)`
- `sync_import_tombstones(workspace_id, external_key, target_id, former_note_id)`
- `note_sync_state(workspace_id, note_id, target_id)`

关键原则：

- 所有主查询条件必须包含 workspace。
- 所有唯一约束从全局唯一改为 workspace 内唯一。
- 所有跨表引用尽量使用复合外键，避免 A workspace 的 note 绑定到 B workspace 的 sync target。

### 主键与复合外键策略

默认 folder id 和默认 task project id 需要在每个 workspace 内复用，因此 `folders.id` 和 `task_projects.id` 不能继续作为全局主键。

迁移后的主键策略：

```sql
-- folders 允许每个 workspace 都有 __uncategorized / __work / __personal
PRIMARY KEY (workspace_id, id)
UNIQUE (workspace_id, name)

-- task_projects 允许每个 workspace 都有 personal
PRIMARY KEY (workspace_id, id)
UNIQUE (workspace_id, name)
```

引用这两个表的业务表必须使用复合外键：

```sql
FOREIGN KEY (workspace_id, folder_id)
  REFERENCES folders(workspace_id, id)

FOREIGN KEY (workspace_id, project_id)
  REFERENCES task_projects(workspace_id, id)
```

`notes.id`、`tasks.id`、`events.id`、`sync_targets.id`、`learning_roadmaps.id` 等随机 entity id 可以继续全局唯一，以降低迁移规模。但只要其他表要用 `(workspace_id, entity_id)` 做数据库级防串写，就必须在父表上补显式唯一约束：

```sql
-- notes.id 可以仍是 PRIMARY KEY，但复合 FK 需要这个唯一约束
UNIQUE (workspace_id, id)

-- tasks.id 可以仍是 PRIMARY KEY，但 recurrence / roadmap / sync 关联需要这个唯一约束
UNIQUE (workspace_id, id)

-- events.id 可以仍是 PRIMARY KEY；保持和 note/task/event 搜索实体一致
UNIQUE (workspace_id, id)

-- sync_targets.id 可以仍是 PRIMARY KEY，但 binding/state/claim 需要这个唯一约束
UNIQUE (workspace_id, id)

-- learning_roadmaps.id 可以仍是 PRIMARY KEY，但 roadmap_nodes/edges 需要这个唯一约束
UNIQUE (workspace_id, id)

-- roadmap_nodes.id 可以仍是 PRIMARY KEY，但 tasks/resources 按 node id 关联需要这个唯一约束
UNIQUE (workspace_id, id)

-- roadmap_nodes parent/edge 需要同时约束同一 roadmap 内的 node
UNIQUE (workspace_id, roadmap_id, id)

-- note_sync_bindings 供 sync_external_claims 使用复合 FK
UNIQUE (workspace_id, note_id, target_id)
```

迁移时必须先删除旧的单列外键，再按 `workspace_id` 重建复合外键。不能只给子表加 `workspace_id`，否则数据库不会阻止跨 workspace 的 note-project、note-target 或 roadmap-node 关联。

### 旧外键到复合外键迁移清单

finalizer 必须覆盖现有 schema 里的所有跨业务表引用，不能只处理 folder/project 示例。下表中的“新 FK”省略索引名，但实现时需要显式命名，便于幂等迁移和回滚诊断。

| 表 | 旧 FK / 约束 | 新 FK / 约束 |
| --- | --- | --- |
| `notes` | `folder_id REFERENCES folders(id)` | `(workspace_id, folder_id) REFERENCES folders(workspace_id, id)` |
| `tasks` | `project_id REFERENCES task_projects(id)` | `(workspace_id, project_id) REFERENCES task_projects(workspace_id, id)`；删除 project 时先在同 workspace 内迁移到 `personal`，再删除 |
| `tasks` | `note_id REFERENCES notes(id)` | `(workspace_id, note_id) REFERENCES notes(workspace_id, id) ON DELETE SET NULL` |
| `tasks` | `roadmap_node_id REFERENCES roadmap_nodes(id)` | `(workspace_id, roadmap_node_id) REFERENCES roadmap_nodes(workspace_id, id) ON DELETE SET NULL` |
| `learning_roadmaps` | `project_id UNIQUE REFERENCES task_projects(id)` | `UNIQUE(workspace_id, project_id)`，并用 `(workspace_id, project_id) REFERENCES task_projects(workspace_id, id)` |
| `roadmap_nodes` | `roadmap_id REFERENCES learning_roadmaps(id)` | `(workspace_id, roadmap_id) REFERENCES learning_roadmaps(workspace_id, id)` |
| `roadmap_nodes` | Postgres: `(roadmap_id, parent_id) REFERENCES roadmap_nodes(roadmap_id, id)`；SQLite: `parent_id REFERENCES roadmap_nodes(id)` | `(workspace_id, roadmap_id, parent_id) REFERENCES roadmap_nodes(workspace_id, roadmap_id, id)` |
| `roadmap_edges` | `roadmap_id REFERENCES learning_roadmaps(id)` | `(workspace_id, roadmap_id) REFERENCES learning_roadmaps(workspace_id, id)` |
| `roadmap_edges` | Postgres: `(roadmap_id, source_node_id) REFERENCES roadmap_nodes(roadmap_id, id)`；SQLite: `source_node_id REFERENCES roadmap_nodes(id)` | `(workspace_id, roadmap_id, source_node_id) REFERENCES roadmap_nodes(workspace_id, roadmap_id, id)` |
| `roadmap_edges` | Postgres: `(roadmap_id, target_node_id) REFERENCES roadmap_nodes(roadmap_id, id)`；SQLite: `target_node_id REFERENCES roadmap_nodes(id)` | `(workspace_id, roadmap_id, target_node_id) REFERENCES roadmap_nodes(workspace_id, roadmap_id, id)` |
| `roadmap_resources` | `node_id REFERENCES roadmap_nodes(id)` | `(workspace_id, node_id) REFERENCES roadmap_nodes(workspace_id, id)` |
| `events` | `note_id REFERENCES notes(id)` | `(workspace_id, note_id) REFERENCES notes(workspace_id, id) ON DELETE SET NULL` |
| `note_project_links` | `note_id REFERENCES notes(id)` | `(workspace_id, note_id) REFERENCES notes(workspace_id, id)` |
| `note_project_links` | `project_id REFERENCES task_projects(id)` | `(workspace_id, project_id) REFERENCES task_projects(workspace_id, id)` |
| `task_recurrence_rules` | `task_id PRIMARY KEY REFERENCES tasks(id)` | `PRIMARY KEY(workspace_id, task_id)`，并用 `(workspace_id, task_id) REFERENCES tasks(workspace_id, id)` |
| `task_occurrences` | `task_id REFERENCES tasks(id)` | `PRIMARY KEY(workspace_id, task_id, occurrence_date)`，并用 `(workspace_id, task_id) REFERENCES tasks(workspace_id, id)` |
| `note_sync_state` | `note_id REFERENCES notes(id)` | `(workspace_id, note_id) REFERENCES notes(workspace_id, id)` |
| `note_sync_state` | `target_id REFERENCES sync_targets(id)` | `(workspace_id, target_id) REFERENCES sync_targets(workspace_id, id)` |
| `note_sync_bindings` | `note_id PRIMARY KEY REFERENCES notes(id)` | `PRIMARY KEY(workspace_id, note_id)`，并用 `(workspace_id, note_id) REFERENCES notes(workspace_id, id)` |
| `note_sync_bindings` | `target_id REFERENCES sync_targets(id)` | `(workspace_id, target_id) REFERENCES sync_targets(workspace_id, id)` |
| `sync_external_claims` | `external_key PRIMARY KEY` | `PRIMARY KEY(workspace_id, external_key)` |
| `sync_external_claims` | `note_id UNIQUE` | `UNIQUE(workspace_id, note_id)` |
| `sync_external_claims` | `(note_id, target_id) REFERENCES note_sync_bindings(note_id, target_id)` | `(workspace_id, note_id, target_id) REFERENCES note_sync_bindings(workspace_id, note_id, target_id)` |
| `note_sync_suppressions` | `note_id REFERENCES notes(id)` | `(workspace_id, note_id) REFERENCES notes(workspace_id, id)` |
| `note_sync_suppressions` | `target_id REFERENCES sync_targets(id)` | `(workspace_id, target_id) REFERENCES sync_targets(workspace_id, id)` |
| `sync_import_tombstones` | `external_key PRIMARY KEY` | `PRIMARY KEY(workspace_id, external_key)` |
| `sync_import_tombstones` | `target_id REFERENCES sync_targets(id)` | `(workspace_id, target_id) REFERENCES sync_targets(workspace_id, id)` |
| `sync_import_tombstones` | `UNIQUE(target_id, former_note_id, external_type)` | `UNIQUE(workspace_id, target_id, former_note_id, external_type)` |

Postgres 对 `ON DELETE SET NULL` 或 `SET DEFAULT` 的复合 FK 需要避免把 `workspace_id` 一起置空或置默认值。实现时优先使用列列表形式只作用于 nullable 子列；如果 provider 不支持列列表动作，则在 service transaction 中先清理或迁移子引用，再执行父表删除。

### 唯一约束调整

现有全局唯一约束需要改为 workspace 维度：

```sql
-- folders
PRIMARY KEY (workspace_id, id)
UNIQUE (workspace_id, name)

-- task_projects
PRIMARY KEY (workspace_id, id)
UNIQUE (workspace_id, name)

-- sync_targets
UNIQUE (workspace_id, type, name)

-- default sync target
CREATE UNIQUE INDEX sync_targets_one_default_per_workspace_type_idx
  ON sync_targets (workspace_id, type)
  WHERE is_default = true;

-- search index
PRIMARY KEY (workspace_id, entity_type, entity_id)

-- note project links
PRIMARY KEY (workspace_id, note_id, project_id)

-- note sync state
PRIMARY KEY (workspace_id, note_id, target_id)
```

`notes.id`、`tasks.id` 等 entity id 可以保持全局唯一，因为现有 `newID()` 生成随机 id，碰撞概率足够低。但查询仍必须带 `workspace_id`，不能因为 id 全局唯一就跳过隔离条件。

### 默认数据

每个 workspace 创建时写入：

```text
folders:
- __uncategorized / Uncategorized
- __work / Work
- __personal / Personal

task_projects:
- personal / Personal / personal
```

这些 id 在 workspace 内复用。因为表有 `workspace_id`，`personal` 和 `__uncategorized` 不再需要全局唯一。

## 迁移策略

迁移分成 SQL schema migration 和 Go bootstrap/backfill 两部分。当前 Postgres migration runner 只负责执行 SQL 文件，不能安全读取环境变量，也不能计算 bcrypt hash，因此 SQL migration 不创建 bootstrap admin。

SQL migration 职责：

1. `0004_multi_user_auth_schema.sql` 创建 `users`、`workspaces`、`workspace_members`、`sessions`、`audit_events`。
2. 给业务表新增 nullable `workspace_id`。
3. 新增迁移期间需要的非唯一索引，降低回填成本。
4. 删除或暂缓旧外键中会阻塞回填的部分，但不写入任何用户密码 hash。

Go bootstrap/backfill 职责：

1. 启动时或 `cmd/bootstrap_admin` 命令中读取：
   - `FLOWSPACE_BOOTSTRAP_ADMIN_EMAIL`
   - `FLOWSPACE_BOOTSTRAP_ADMIN_PASSWORD`
   - `FLOWSPACE_BOOTSTRAP_ADMIN_NAME`
2. 使用 bcrypt 生成 `password_hash`。
3. 在没有任何 user 时创建首个 admin 和默认 workspace，并设置 `users.default_workspace_id`。
4. 区分空库和 legacy 升级：
   - 空库：直接写入默认 folders 和默认 personal task project。
   - legacy 升级：先把现有 `folders`、`task_projects` 和业务数据回填到 bootstrap workspace；只在缺少 `__uncategorized`、`__work`、`__personal` 或 `personal` 时补默认项。
5. legacy 回填完成后再校验所有业务表 `workspace_id IS NOT NULL`，并校验跨表 workspace 一致性。
6. 调用 provider-specific finalizer 应用 NOT NULL、复合主键、复合唯一索引和复合外键。

bootstrap/backfill 写默认数据时没有 HTTP 请求身份，必须显式构造 bootstrap workspace 的 `WorkspaceScope`。不能调用依赖 session identity 的 handler 逻辑，也不能让 repository 在缺少 scope 时回退到全局写入。

`users.default_workspace_id` 的迁移顺序：

1. SQL schema migration 可以先添加 nullable `default_workspace_id`，因为 legacy 数据需要先创建 bootstrap user/workspace 后回填。
2. Go bootstrap/backfill 必须为每个 user 设置默认 workspace，并写入对应 `workspace_members` owner 记录。
3. finalizer 校验 `users.default_workspace_id IS NOT NULL`，且 `(users.id, users.default_workspace_id)` 能匹配 `workspaces(owner_user_id, id)`。
4. 校验通过后再设置 `users.default_workspace_id NOT NULL`，并创建 `users_default_owned_workspace_fk` 复合外键。

约束 finalizer 可以实现为 Go 代码中的幂等 DDL 步骤，也可以实现为单独 SQL 文件，但它必须在 bootstrap/backfill 成功后运行。不能让普通 migration runner 在 bootstrap 之前直接执行最终 NOT NULL 和复合外键，否则空库以外的升级路径会失败。

启动保护：

- 如果数据库已有业务数据但没有任何用户，且未配置 bootstrap admin，后端启动失败并打印明确错误。
- 如果数据库是空库且未配置 bootstrap admin，可以启动，但只有 health 可访问；创建首个管理员需要命令行 seed 或配置环境变量后重启。
- bootstrap/backfill 未完成时，不注册受保护业务路由和 admin 用户创建路由，避免“已登录但仍访问全局业务数据”的半迁移状态。

数据安全：

- 迁移前先检查是否存在孤儿数据，例如 note 引用不存在的 folder、task 引用不存在的 project。
- 检查失败时停止迁移，不进行半迁移。
- Postgres 迁移在事务中执行；SQLite 迁移尽量在单事务中执行，遇到不支持的 DDL 操作时拆分并记录迁移版本。

## 后端设计

### 新增包与职责

```text
backend/internal/auth
```

职责：

- 生成 session token。
- hash token。
- 校验密码 hash。
- 从 Gin context 读取当前用户和 workspace。

```text
backend/internal/model/auth.go
```

定义：

- `User`
- `Workspace`
- `Session`
- `CurrentUser`
- `LoginRequest`
- `LoginResponse`
- `CreateUserRequest`
- `UpdateUserRequest`
- `ResetPasswordRequest`

```text
backend/internal/handler/auth.go
backend/internal/handler/admin_users.go
```

职责：

- auth endpoints。
- admin user management endpoints。

```text
backend/internal/middleware/auth.go
```

职责：

- 解析 `fs_session` Cookie。
- 查找未过期、未撤销 session。
- 校验 user status。
- 校验 `sessions.workspace_id` 仍存在，且 `sessions.user_id` 仍是该 workspace 的 `workspace_members` 成员。
- 把 `AuthContext{UserID, WorkspaceID, Role, MustChangePassword}` 放入 Gin context 和 request context。
- 提供 `RequirePasswordSettled()` middleware：当 `MustChangePassword=true` 时阻止 protected/admin/system 业务路由，返回 403 `PASSWORD_CHANGE_REQUIRED`。

```text
backend/internal/storage/store.go
```

扩展：

```go
// 在现有 Store 接口上新增 Auth()，保留 Folders/Notes/Tasks/Recurrence/Events/
// Inbox/Roadmaps/Sync/Search 等现有 repository 方法。
type Store interface {
    Close() error
    Health(context.Context) error
    Capabilities() Capabilities
    Transact(context.Context, func(Store) error) error

    Folders() FolderRepository
    Notes() NoteRepository
    Tasks() TaskRepository
    Recurrence() RecurrenceRepository
    Events() EventRepository
    Inbox() InboxRepository
    Roadmaps() RoadmapRepository
    Sync() SyncRepository
    Search() SearchRepository
    Auth() AuthRepository
}

// backend/internal/model/auth.go
type UserListFilter struct {
    Page     int
    PageSize int
    Query    string
}

type AuthRepository interface {
    CreateUser(ctx context.Context, user *model.User) error
    GetUserByEmail(ctx context.Context, email string) (*model.User, error)
    GetUserByID(ctx context.Context, id string) (*model.User, error)
    ListUsers(ctx context.Context, filter model.UserListFilter) ([]model.User, int, error)
    UpdateUser(ctx context.Context, id string, req *model.UpdateUserRequest) (*model.User, error)
    UpdateUserPassword(ctx context.Context, userID, passwordHash string, mustChangePassword bool) error
    CreateWorkspace(ctx context.Context, workspace *model.Workspace) error
    AddWorkspaceMember(ctx context.Context, workspaceID, userID, role string) error
    CreateSession(ctx context.Context, session *model.Session) error
    GetSessionByTokenHash(ctx context.Context, tokenHash string) (*model.Session, error)
    GetWorkspaceMembership(ctx context.Context, workspaceID, userID string) (*model.WorkspaceMember, error)
    RevokeSession(ctx context.Context, sessionID string) error
    RevokeUserSessions(ctx context.Context, userID string) error
    RevokeUserSessionsExcept(ctx context.Context, userID, keepSessionID string) error
    RecordAuditEvent(ctx context.Context, event *model.AuditEvent) error
}
```

### Request context

新增 context helper：

```go
type RequestIdentity struct {
    UserID             string
    WorkspaceID        string
    Role               string
    MustChangePassword bool
}

type WorkspaceScope struct {
    WorkspaceID string
}
```

handler 获取方式：

```go
identity, ok := auth.IdentityFromContext(c.Request.Context())
```

`RequestIdentity` 表示操作者，`WorkspaceScope` 表示本次业务数据读写的目标 workspace。普通登录请求中两者来自同一个 session；账号开通、默认数据 provisioning、legacy bootstrap 这类流程中，actor 和 target workspace 必须分开。

repository 不直接依赖 Gin。业务 workspace 通过 `WorkspaceScope` 或显式 filter 字段传入。推荐使用 context helper，减少接口签名大面积膨胀：

```go
ctx = auth.ContextWithIdentity(ctx, identity)
ctx = auth.ContextWithWorkspaceScope(ctx, identity.WorkspaceID)
workspaceID, err := auth.WorkspaceIDFromContext(ctx)
```

所有业务 repository 写 SQL 前必须从 `WorkspaceScope` 取 workspaceID，而不是从 actor identity 推导。缺少 workspace scope 时返回 `auth.ErrMissingWorkspace`，handler 映射为 500，因为受保护路由缺少 workspace 是服务端接线错误。

provisioning 规则：

- 管理员创建用户时，audit actor 使用管理员 `RequestIdentity`，默认 folders/project 写入使用新用户 workspace 的 `WorkspaceScope`。
- legacy bootstrap 没有请求身份，使用 system actor 或空 actor 写 audit，但仍必须先构造 bootstrap workspace 的 `WorkspaceScope` 再写默认数据。
- 禁止直接复用 admin 请求 ctx 写目标用户默认数据；否则默认 folder/project 会落入 admin workspace。

### Membership 校验

登录发 session 和每次 session 恢复都必须校验 workspace membership。

登录流程：

1. 校验 email/password。
2. 校验 user status 为 `active`。
3. 读取 `users.default_workspace_id` 指向的 workspace。
4. 校验 `workspace_members(workspace_id, user_id)` 存在。
5. 创建 session。

session 恢复流程：

1. 通过 Cookie token hash 找到未过期、未撤销 session。
2. 加载 user，校验 user status 为 `active`。
3. 加载 workspace，确认 workspace 未被删除。
4. 校验 `workspace_members` 中仍存在该 user 对该 workspace 的 membership。
5. 如果 membership 缺失，撤销当前 session、清除 Cookie，并返回 401 `WORKSPACE_ACCESS_REVOKED`。

这样即使后续移除某用户的 workspace membership，旧 session 也不能继续访问该 workspace。

### 路由分组

`router.Setup()` 调整为：

```go
api := r.Group("/api")
api.GET("/health", handler.Health)

authRoutes := api.Group("/auth")
authRoutes.POST("/login", handler.Login)
authRoutes.POST("/logout", authMiddleware.Optional(), handler.Logout)
authRoutes.GET("/me", authMiddleware.Required(), handler.Me)
authRoutes.POST("/change-password", authMiddleware.Required(), handler.ChangePassword)

protected := api.Group("")
protected.Use(authMiddleware.Required(), authMiddleware.RequirePasswordSettled())
{
    protected.GET("/folders", handler.GetFolders)
    protected.GET("/notes", handler.GetNotes)
    protected.POST("/notes", handler.CreateNote)
    protected.GET("/tasks", handler.GetTasks)
    protected.GET("/search", handler.Search)
    // 其他现有业务路由继续注册在 protected 分组内。
}

admin := protected.Group("/admin")
admin.Use(authMiddleware.RequireAdmin())
{
    admin.GET("/users", handler.ListUsers)
    admin.POST("/users", handler.CreateUser)
    admin.PATCH("/users/:id", handler.UpdateUser)
    admin.POST("/users/:id/reset-password", handler.ResetUserPassword)
    admin.POST("/users/:id/disable", handler.DisableUser)
    admin.POST("/users/:id/enable", handler.EnableUser)
}

if cfg.EnableLocalDirectoryBrowser {
    systemAdmin := protected.Group("/system")
    systemAdmin.Use(authMiddleware.RequireAdmin())
    systemAdmin.GET("/directories", handler.ListLocalDirectories)
}
```

`/api/health` 保持公开，便于 Docker healthcheck。

`/api/system/directories` 不能作为普通 protected API。该端点会浏览服务器本地目录，v1 规则：

- 仅 admin 可访问。
- 生产环境默认关闭，必须设置 `FLOWSPACE_ENABLE_LOCAL_DIRECTORY_BROWSER=true` 才注册。
- 只能浏览 `FLOWSPACE_ALLOWED_LOCAL_ROOTS` 白名单内路径。
- 不返回白名单外路径，也不跟随白名单外符号链接。
- 每次访问写入 audit event `system.directories.list`。

### Auth API

#### `POST /api/auth/login`

请求：

```json
{
  "email": "admin@example.com",
  "password": "secret123",
  "remember_me": true
}
```

响应：

```json
{
  "data": {
    "user": {
      "id": "user_admin_123",
      "email": "admin@example.com",
      "display_name": "Admin",
      "role": "admin",
      "status": "active"
    },
    "workspace": {
      "id": "workspace_admin_123",
      "name": "Admin 的工作区"
    }
  }
}
```

副作用：

- 写入 `sessions`。
- 设置 `fs_session` Cookie。
- 更新 `users.last_login_at`。
- 写入 `audit_events`，action 为 `auth.login`。

#### `GET /api/auth/me`

用于前端启动时恢复登录态。返回当前用户、当前 workspace、权限和 `must_change_password`。前端看到 `must_change_password=true` 时只展示改密流程和退出入口，不加载业务查询。

#### `POST /api/auth/logout`

撤销当前 session，清除 Cookie。即使 session 已失效也返回 204。

#### `POST /api/auth/change-password`

普通用户修改自己的密码。

请求：

```json
{
  "current_password": "old-secret",
  "new_password": "new-secret"
}
```

规则：

- 普通改密必须校验 `current_password`。
- 当 `must_change_password=true` 时，用户已经通过临时密码登录；此时仍校验当前临时密码，但只允许访问 `/api/auth/me`、`/api/auth/change-password`、`/api/auth/logout`。
- 成功后设置 `must_change_password=false`，更新 `password_changed_at`。
- 成功后调用 `RevokeUserSessionsExcept(ctx, userID, currentSessionID)`，撤销除当前 session 以外的其他 session。
- 写入 audit event `auth.change_password`。

## 账号管理平台

### 权限模型

`users.role` v1 只有两个值：

- `admin`：可进入账号管理，可创建、禁用、重置用户密码。
- `user`：只能访问自己的默认 workspace。

管理员不能禁用最后一个 active admin。后端在禁用或降级管理员时检查：

```text
active admin count must remain >= 1
```

并发保护：

- 禁用、启用、降级、升为管理员都在 `Store.Transact(ctx, fn)` 内完成。
- 禁用或降级 admin 前，Postgres 使用 `SELECT id FROM users WHERE role = 'admin' AND status = 'active' FOR UPDATE` 锁定 active admin 集合，再按本次变更后的结果判断是否仍有至少一个 active admin。
- SQLite 使用 `BEGIN IMMEDIATE` 或 provider 等价写锁包住检查与更新，避免两个写事务同时通过计数检查。
- 如果并发变更导致操作后 active admin 数量会变成 0，返回 `LAST_ADMIN_REQUIRED`，不写入状态变更或 audit event。

### 用户列表

`GET /api/admin/users?page=1&page_size=20&q=alice`

`q` 通过 `model.UserListFilter.Query` 传入 repository，匹配 `email` 和 `display_name`，按 provider 使用 `ILIKE` 或大小写归一化 LIKE。

返回字段：

```json
{
  "data": {
    "users": [
      {
        "id": "user_alice_123",
        "email": "user@example.com",
        "display_name": "User",
        "role": "user",
        "status": "active",
        "created_at": 1782220000,
        "last_login_at": 1782223600
      }
    ]
  },
  "pagination": {
    "page": 1,
    "page_size": 20,
    "total": 1
  }
}
```

不返回 `password_hash`、session token、业务数据统计明细。

### 更新用户资料

`PATCH /api/admin/users/:id`

请求只允许以下字段：

```json
{
  "email": "new-email@example.com",
  "display_name": "New Name",
  "role": "user"
}
```

规则：

- `email` 必须通过 `users_email_lower_idx` 唯一校验。
- `role` 只允许 `admin` 和 `user`，且 role 变更必须在 `Store.Transact(ctx, fn)` 内完成。
- `admin -> user` 降级必须使用和禁用 admin 相同的最后管理员并发保护。
- role 变化后撤销目标用户所有 sessions，确保旧 session 不能继续使用旧权限。
- `PATCH` 不接受 `status`、`password`、`temporary_password`、`must_change_password`、`default_workspace_id` 或 workspace membership 字段；禁用/启用必须走专用 endpoint，密码必须走 reset-password 或 change-password。
- 成功写入 audit event `admin.user.update`，metadata 只记录变更字段名和非敏感摘要。

### 创建用户

`POST /api/admin/users`

请求：

```json
{
  "email": "new@example.com",
  "display_name": "New User",
  "temporary_password": "initial-secret",
  "role": "user"
}
```

副作用：

- 创建 user，写入 `must_change_password=true`。
- 创建默认 workspace。
- 设置 `users.default_workspace_id`。
- 写入默认 folders 和 personal task project。
- 写入 workspace_members owner 记录。
- 写入 audit event `admin.user.create`。

事务要求：

- 创建用户必须在一个 `Store.Transact(ctx, fn)` 中完成 user、workspace、membership、默认 folders、personal project 和 audit event 写入。
- 默认 folders 和 personal project 必须使用 `auth.ContextWithWorkspaceScope(txCtx, newWorkspaceID)` 写入；audit event 的 actor 仍使用管理员 identity。
- 任一步失败都回滚整个事务，不能留下没有 workspace 的 user、没有 owner 的 workspace 或半写入默认数据。
- 集成测试需要模拟默认 project 写入失败，验证 user/workspace/membership 均未落库。

信任模型：

- 创建用户和重置密码一样使用临时密码。
- 管理员知道临时密码，但用户首次登录只能访问改密流程。
- 用户完成改密前不能访问业务 API。

### 重置密码

`POST /api/admin/users/:id/reset-password`

请求：

```json
{
  "temporary_password": "new-secret"
}
```

副作用：

- 更新 password hash。
- 设置 `users.must_change_password=true`。
- 撤销该用户所有 sessions。
- 写入 audit event `admin.user.reset_password`。

信任模型：

- v1 管理员可以设置临时密码，因此管理员在技术上具备登录成该用户的能力。
- UI 必须在设置临时密码弹窗中明确提示这一点。
- 所有重置操作写入 audit event，包含 actor、target、时间和请求来源。
- audit metadata 禁止记录 `temporary_password`、明文密码、password hash、Cookie、session token、session token hash、Authorization header、Notion token、MinIO secret key 或任何外部服务密钥。
- 被重置用户下次登录后只能访问 `/api/auth/me`、`/api/auth/change-password`、`/api/auth/logout`，业务 API 返回 403 `PASSWORD_CHANGE_REQUIRED`。
- 用户完成改密后，后端设置 `must_change_password=false`、更新 `password_changed_at`，并撤销除当前 session 以外的其他 session。
- 后续如果接入邮箱，可替换为邀请/找回密码链接，管理员不再看到或设置临时密码。

### 审计 metadata 红线

`audit_events.metadata` 只记录可审计的上下文，例如请求 IP、user agent、目标用户 id、目标 workspace id、操作结果和错误码。

禁止写入：

- `temporary_password` 或任何明文密码。
- password hash。
- Cookie 原文。
- session token 或 session token hash。
- `Authorization` header。
- Notion token、MinIO secret key、AI API key 或任何外部服务密钥。
- 用户笔记正文、任务正文等业务私密内容。

审计写入前必须通过 sanitizer 过滤 metadata。测试需要覆盖 reset password 和 system directories 两类事件，确认敏感字段不会落库。

### 禁用/启用用户

禁用：

- `POST /api/admin/users/:id/disable`
- 设置 `status='disabled'`。
- 撤销该用户所有 sessions。

启用：

- `POST /api/admin/users/:id/enable`
- 设置 `status='active'`。

## 数据隔离细节

### Repository 过滤原则

所有业务 repository 方法必须满足：

1. List 查询 `WHERE workspace_id = $n`。
2. GetByID 查询 `WHERE id = $n AND workspace_id = $m`。
3. Update/Delete 查询 `WHERE id = $n AND workspace_id = $m`。
4. Create 写入当前 workspace_id。
5. 跨表 JOIN 同时校验两边 workspace_id。
6. Search 查询先按 `search_index.workspace_id` 过滤，再 JOIN 业务表。

示例：

```sql
SELECT n.id, n.title, n.body
FROM notes n
WHERE n.workspace_id = $1
  AND n.id = $2
```

反例：

```sql
SELECT n.id, n.title, n.body
FROM notes n
WHERE n.id = $1
```

### Inbox 转换隔离

`inbox` 表新增 `workspace_id` 后，所有 list/create/delete/batch archive 都按当前 `WorkspaceScope` 过滤。`converted_to` 是多态字符串，数据库无法直接用单个 FK 约束到 note/task/event，因此转换流程必须用服务层事务保证一致性。

`ConvertInboxItem` 规则：

- 在 `Store.Transact(ctx, fn)` 内完成读取 inbox、创建目标实体、标记 inbox converted 三步。
- 读取 inbox 时使用 `WHERE workspace_id = $1 AND id = $2 AND converted_to IS NULL`，避免重复转换和跨 workspace 转换。
- 创建 note/task/event 时使用同一个 `WorkspaceScope`，不能从 request body 接收 workspace id。
- `converted_to` 写入带类型前缀的稳定格式，例如 `note:<note_id>`、`task:<task_id>`、`event:<event_id>`；legacy id-only 值迁移时保留但新写入必须用 typed format。
- 标记 converted 时使用条件更新：`WHERE workspace_id = $1 AND id = $2 AND converted_to IS NULL`，并要求 affected rows 等于 1。
- 如果标记 converted 失败、affected rows 不为 1，或创建目标实体后任一步失败，整个事务回滚，不能留下孤儿 note/task/event。
- 转换完成后返回同 workspace 的目标实体；最终 `GetByID` 仍必须带 workspace filter。

### Search 隔离

`search_index` 主键改为：

```sql
PRIMARY KEY (workspace_id, entity_type, entity_id)
```

`upsertNoteSearchIndex`、`upsertTaskSearchIndex` 和 event search index 写入都必须带 workspace_id。搜索 SQL 的 `matched` CTE 第一层就加：

```sql
FROM search_index s
WHERE s.workspace_id = $1
  AND (s.title ILIKE $2 OR s.content ILIKE $2)
```

这样可以避免搜索 rank、fallback、dedup 过程中混入其他 workspace 数据。

### SQLite 搜索隔离

SQLite provider 不能只按 `search_index.workspace_id` 设计，因为当前 SQLite 搜索走 `notes_fts`、`tasks_fts`、`events_fts` 以及 LIKE fallback 查询。

SQLite 搜索迁移规则：

- `notes`、`tasks`、`events` 表新增 `workspace_id` 后，FTS 查询必须 JOIN 回业务表并过滤业务表 `workspace_id`。
- COUNT 查询和数据查询都必须使用同一套 workspace filter。
- FTS trigger 仍只负责同步 title/body/tags 等搜索内容，不把 workspace_id 写进 FTS 虚表；workspace 过滤由 JOIN 的业务表提供。
- LIKE fallback 直接在业务表 WHERE 中加 `workspace_id = ?`。
- 删除业务数据时，FTS 删除 trigger 继续按 rowid 删除；rowid 来源必须来自当前 workspace 的业务表查询。
- 如果 SQLite finalizer 通过重建表迁移 `notes`、`tasks` 或 `events`，表 swap 后必须重建对应 FTS 虚表和 triggers。优先执行 `INSERT INTO notes_fts(notes_fts) VALUES('rebuild')`、`tasks_fts` rebuild、`events_fts` rebuild；如果外部内容 FTS rebuild 不可用，则清空虚表并从业务表全量回灌。
- FTS rebuild 后校验 `notes_fts`、`tasks_fts`、`events_fts` 可按业务表 rowid JOIN，并跑搜索 contract tests；不能只验证业务表行数。

示例：

```sql
SELECT n.id, n.title, n.body
FROM notes_fts f
JOIN notes n ON n.rowid = f.rowid
WHERE n.workspace_id = ?
  AND notes_fts MATCH ?
```

### Sync 隔离

同步相关表必须以 workspace 为边界：

- 一个 workspace 的默认 Notion target 不影响另一个 workspace。
- `sync_external_claims.external_key` 不能全局唯一，否则两个用户同步同一个外部路径会冲突。
- `note_sync_bindings` 必须防止跨 workspace note-target 绑定。

调整：

```sql
PRIMARY KEY (workspace_id, external_key)
UNIQUE (workspace_id, note_id)
UNIQUE (workspace_id, note_id, target_id)
```

`LockBindingSlot` 的 advisory lock key 加 workspace：

```text
note_sync_binding:{workspace_id}:{note_id}
```

### Roadmap 隔离

`learning_roadmaps.project_id` 当前唯一，改为：

```sql
UNIQUE (workspace_id, project_id)
```

`roadmap_nodes` 保留 `id` 全局随机主键，同时补两个 workspace 维度唯一约束，供不同关联路径建立复合外键：

```sql
UNIQUE (workspace_id, id)
UNIQUE (workspace_id, roadmap_id, id)
```

`roadmap_nodes`、`roadmap_edges`、`roadmap_resources` 查询全部通过 workspace 限定。`tasks.roadmap_node_id` 更新 roadmap node 状态时，必须更新同 workspace 的 node：

```sql
UPDATE roadmap_nodes
SET status = $1, updated_at = $2
WHERE workspace_id = $3 AND id = $4
```

### Recurrence 隔离

`task_recurrence_rules` 和 `task_occurrences` 以 workspace + task 为复合键。完成 occurrence 时，先确认 task 属于当前 workspace 且是 recurring task，再 upsert occurrence。

### MinIO / BlobStore

当前代码只在部署配置中暴露 MinIO 环境变量，业务存储设计文档也说明二进制对象应由未来独立 `BlobStore` 处理。v1 不新增文件上传能力。

如果后续接入附件，object key 必须以 workspace 分区：

```text
workspaces/{workspace_id}/attachments/{object_id}
```

数据库中保存 object metadata 时也必须带 workspace_id。

## 前端设计

### Auth client

新增：

```text
frontend/src/api/auth.ts
frontend/src/hooks/useAuth.tsx
frontend/src/components/auth/ProtectedRoute.tsx
frontend/src/components/auth/AdminRoute.tsx
```

`api.client.ts` 统一加：

```ts
credentials: 'include'
```

当响应为 401：

- 清空 auth query cache。
- 如果当前路径不是 `/login`，跳转 `/login?next=<current path>`。

### 路由

`router.tsx` 调整：

```tsx
{ path: '/login', element: <Login /> },
{
  path: '/',
  element: (
    <ProtectedRoute>
      <App />
    </ProtectedRoute>
  ),
  children: [
    { index: true, element: <Dashboard /> },
    { path: 'notes', element: <Notes /> },
    { path: 'tasks', element: <Tasks /> },
    {
      path: 'admin/users',
      element: (
        <AdminRoute>
          <AccountAdmin />
        </AdminRoute>
      ),
    },
  ],
}
```

`ProtectedRoute` 首次加载时调用 `/api/auth/me`。加载中显示轻量全屏 loading。未登录跳转 login。已登录渲染 children。

### 登录页

现有 `Login.tsx` 保留视觉结构，但改为真实提交：

- 邮箱字段。
- 密码字段。
- 记住我。
- 错误提示。
- 登录成功后跳转 `next`，没有 next 则跳转 `/`。

GitHub 登录按钮 v1 隐藏。若保留，必须显示 disabled 状态并且不会误导用户以为可用。

### 顶部账号菜单

`TopBar` 增加用户菜单：

- 当前 display name / email。
- 修改密码。
- 退出登录。

管理员额外显示：

- 账号管理。

退出登录调用 `/api/auth/logout` 后：

- 清空 React Query cache。
- 跳转 `/login`。

### 账号管理页面

新增 route：

```text
frontend/src/routes/AccountAdmin.tsx
```

页面结构：

- 顶部标题与创建用户按钮。
- 用户表格：邮箱、名称、角色、状态、最后登录、创建时间、操作。
- 创建用户 modal，字段为邮箱、显示名、角色、临时密码。
- 设置临时密码 modal，并提示用户下次登录必须改密。
- 禁用/启用确认。

v1 不做复杂仪表盘，不显示业务数据量排行，避免把管理页面变成数据窥探入口。

## 配置

新增环境变量：

```text
FLOWSPACE_SESSION_SECRET
FLOWSPACE_COOKIE_SECURE
FLOWSPACE_ALLOWED_ORIGINS
FLOWSPACE_ENABLE_LOCAL_DIRECTORY_BROWSER
FLOWSPACE_ALLOWED_LOCAL_ROOTS
FLOWSPACE_BOOTSTRAP_ADMIN_EMAIL
FLOWSPACE_BOOTSTRAP_ADMIN_PASSWORD
FLOWSPACE_BOOTSTRAP_ADMIN_NAME
```

规则：

- `FLOWSPACE_SESSION_SECRET` 用于 token hash pepper 或后续签名用途。生产环境必填。
- `FLOWSPACE_COOKIE_SECURE` 默认为 `true`，本地开发可设为 `false`。
- `FLOWSPACE_ALLOWED_ORIGINS` 替代当前硬编码 `http://localhost:5173`。
- `FLOWSPACE_ENABLE_LOCAL_DIRECTORY_BROWSER` 默认为 `false`，只有 admin 需要配置 Obsidian 本地路径时才打开。
- `FLOWSPACE_ALLOWED_LOCAL_ROOTS` 是逗号分隔路径白名单，例如 `D:\Notes,D:\MyGitProject`。
- bootstrap password 只在首次创建管理员时使用，不写入日志。

`docker-compose.yml` 增加这些 env 的透传。

## 错误码

认证相关：

- `UNAUTHENTICATED`：未登录或 session 失效，HTTP 401。
- `FORBIDDEN`：非管理员访问 admin API，HTTP 403。
- `INVALID_CREDENTIALS`：登录失败，HTTP 401。
- `ACCOUNT_DISABLED`：账号停用，HTTP 403。
- `PASSWORD_CHANGE_REQUIRED`：账号必须先修改临时密码，HTTP 403。
- `WORKSPACE_ACCESS_REVOKED`：session 指向的 workspace membership 已失效，HTTP 401，并撤销当前 session。
- `EMAIL_ALREADY_EXISTS`：创建用户邮箱重复，HTTP 409。
- `LAST_ADMIN_REQUIRED`：不能禁用或降级最后一个管理员，HTTP 409。
- `WEAK_PASSWORD`：密码不符合规则，HTTP 400。

密码规则：

- 最少 8 个字符。
- 至少包含字母和数字。
- 前端做提示，后端强制校验。

## 测试计划

### 后端单元测试

1. 密码 hash 校验成功与失败。
2. 登录成功创建 session 并设置 Cookie。
3. 错误密码返回 `INVALID_CREDENTIALS`。
4. disabled 用户不能登录。
5. logout 撤销当前 session。
6. `GET /api/auth/me` 对有效 session 返回当前用户和 workspace。
7. 非管理员访问 `/api/admin/users` 返回 403。
8. 管理员创建用户时自动创建 workspace、默认 folders、personal project。
9. 禁用用户撤销其所有 session。
10. 禁用最后一个管理员返回 `LAST_ADMIN_REQUIRED`。
11. session 恢复时 membership 缺失会撤销 session 并返回 401。
12. `must_change_password=true` 用户访问业务 API 返回 `PASSWORD_CHANGE_REQUIRED`。
13. `/api/system/directories` 对普通用户返回 403，对未开启配置的 admin 返回 404 或 403。
14. `/api/auth/me` 返回 `must_change_password`，前端可据此进入改密流程。
15. `RequirePasswordSettled()` 允许 `/api/auth/me`、`/api/auth/change-password`、`/api/auth/logout`，阻止 protected/admin/system 路由。
16. 创建用户任一步写入失败会回滚 user、workspace、membership、默认数据和 audit event。
17. audit metadata 不包含临时密码、Cookie、session token 或任何 secret。
18. 管理员创建用户时默认 folders/project 写入新用户 workspace，而不是管理员 workspace。
19. 两个 admin 并发禁用或降级时，不能把最后一个 active admin 消除。
20. `/api/admin/users?q=alice` 会把 query 传入 repository filter，并只匹配 email/display_name。
21. `PATCH /api/admin/users/:id` 拒绝 status/password/default_workspace 字段。
22. admin 降级为 user 时套用最后管理员并发保护并撤销目标用户 sessions。
23. `ConvertInboxItem` 标记 converted 失败时回滚已创建 note/task/event。

### Storage contract tests

Postgres 和 SQLite 都要覆盖：

1. 用户 A 和用户 B 都有 `personal` task project，但互不冲突。
2. 用户 A 和用户 B 都有 `__uncategorized` folder，但互不冲突。
3. `folders` 和 `task_projects` 使用 `(workspace_id, id)` 主键，子表复合外键能阻止跨 workspace 引用。
4. 同名 sync target 在不同 workspace 可创建。
5. `ListNotes` 只返回当前 workspace notes。
6. `GetNoteByID` 不能读取其他 workspace 的 note，即使知道 id。
7. `UpdateTask` 不能更新其他 workspace 的 task。
8. Postgres `Search` 只返回当前 workspace 的 note/task/event。
9. SQLite FTS 和 LIKE fallback 的 `Search` 只返回当前 workspace 的 note/task/event，COUNT 同样隔离。
10. `ListSyncTargets` 只返回当前 workspace targets。
11. `GetDefaultTarget` 按 workspace + type 查找。
12. `task_occurrences` 完成记录按 workspace 隔离。
13. `users.default_workspace_id` 指向当前唯一 owned workspace。
14. v1 `workspaces.owner_user_id` 唯一约束阻止同一用户创建第二个 owned workspace。
15. roadmap node 相关复合外键能阻止 task/resource/edge 指向其他 workspace 的 node。
16. sync binding、claim、suppression、tombstone 相关复合外键能阻止跨 workspace note-target 关联。
17. SQLite 表重建后 FTS rebuild 可用，搜索通过 rowid JOIN 仍返回正确结果。
18. `users.default_workspace_id` 为 NOT NULL，且复合 FK 阻止它指向其他用户 owned workspace。
19. inbox `converted_to` 新写入使用 typed format，转换查询和条件更新都按 workspace 过滤。

### API 集成测试

1. 未登录访问 `/api/notes` 返回 401。
2. 登录用户 A 创建 note。
3. 登录用户 B 访问用户 A note id 返回 404。
4. 登录用户 B 搜索用户 A note 标题返回空结果。
5. 管理员创建用户 C 后，用户 C 首次登录能看到空 workspace 默认数据。
6. 用户 C 退出后旧 Cookie 不再可访问 protected API。
7. 管理员重置用户 C 密码后，用户 C 必须先改密才能访问业务 API。
8. `/api/system/directories` 只能在 admin、开关开启、路径命中白名单时返回目录。
9. legacy 数据库升级时先回填已有 folders/projects，再补缺失默认项，不因 `__uncategorized` 或 `personal` 已存在而失败。
10. 创建用户提交的是临时密码，用户首次登录必须先改密。
11. legacy bootstrap 在没有请求身份时仍用 bootstrap workspace scope 写默认数据。
12. 禁用或降级最后一个 active admin 的并发竞争只允许一个事务成功，另一个返回 `LAST_ADMIN_REQUIRED`。
13. `/api/system/directories` 在未启用 `FLOWSPACE_ENABLE_LOCAL_DIRECTORY_BROWSER` 时路由不存在。
14. `PATCH /api/admin/users/:id` role 降级后旧 session 不能继续访问 admin API。
15. inbox 转换创建目标实体和标记 converted 在单事务内完成，模拟 mark 失败时目标实体不落库。

### 前端测试

1. `/login` 表单校验邮箱和密码。
2. 登录成功跳转到 `next`。
3. 未登录访问 `/notes` 跳转 `/login?next=/notes`。
4. 已登录用户刷新页面时 `/api/auth/me` 恢复会话。
5. 401 响应触发退出态和跳转。
6. 普通用户不显示账号管理入口。
7. 管理员能看到账号管理入口。
8. 创建用户 modal 成功后列表刷新。

### E2E 验证

1. 管理员登录。
2. 创建用户 A 与用户 B。
3. 用户 A 创建笔记、任务、同步目标。
4. 用户 B 登录后看不到用户 A 的任何数据。
5. 用户 B 使用搜索查用户 A 的标题，结果为空。
6. 管理员禁用用户 A。
7. 用户 A 已有会话刷新后被踢回登录页。

## 分阶段落地

### Phase 1：认证基础

- 新增 auth schema、model、repository、middleware。
- 新增 login/logout/me/change-password API。
- 前端 Login 接真实 API。
- ProtectedRoute 接入 `/api/auth/me`。
- Phase 1 只允许 bootstrap admin 登录，不提供创建第二个用户的 UI 或 API。
- Phase 1 不能作为独立生产发布版本；如果要上线真实多用户，Phase 1 和 Phase 2 必须同批发布并通过隔离验收。

验收：

- 未登录不能访问业务 API。
- 登录后能访问当前工作台。
- 退出后不能继续访问。
- 没有任何路径可以在 workspace 隔离完成前创建非 bootstrap 用户。

### Phase 2：workspace 数据隔离

- 迁移业务表增加 workspace_id。
- repository 查询和写入全部带 workspace。
- search_index、sync_targets、sync_external_claims 完成隔离改造。
- 所有 Store provider 对齐。

验收：

- 两个用户的默认 folder/project id 可以相同。
- 用户间无法通过 list/get/search/sync API 看到或修改彼此数据。

### Phase 3：账号管理平台

- 新增 `/admin/users` route。
- 新增 admin user APIs。
- 完成创建、禁用、启用、设置临时密码并强制改密 UI。
- 增加 audit events。

验收：

- admin 可管理用户。
- 普通用户不能访问 admin route 或 API。
- 禁用用户立即失效。

### Phase 4：清理旧 facade 与安全加固

- 将仍依赖旧 `repository.*` 包级函数的 service 迁移到显式 Store + ctx。
- 补齐 CSRF origin check。
- 增加登录失败节流。
- 增加 session 清理任务，删除过期 session。

验收：

- 新业务路径不再绕过 AuthContext。
- 过期 session 不会长期堆积。

## 决策记录

1. 使用 workspace 隔离，而不是直接 `user_id` 隔离。
2. v1 每个用户只有一个默认 workspace，不做 UI 切换。
3. 登录使用 HttpOnly Cookie session，不使用 localStorage token。
4. v1 不接 OAuth；现有 GitHub 登录按钮不提供真实能力。
5. 管理员管理账号，不默认读取用户业务数据。
6. `search_index` 必须带 workspace_id，搜索隔离不能只靠 JOIN 后过滤。
7. 同步相关表必须带 workspace_id，尤其是 `sync_external_claims.external_key` 不能全局唯一。
8. 业务 entity id 可继续全局随机唯一，但所有查询仍必须带 workspace_id。
9. 默认 folder/project id 在 workspace 内复用，因此 `folders` 和 `task_projects` 改为 `(workspace_id, id)` 复合主键。
10. SQL migration 不创建 bootstrap admin；Go bootstrap/seed 负责读取 env、计算 bcrypt、创建首个 admin/default workspace，并完成 legacy backfill。
11. Phase 1 不能独立开放多用户创建；认证和 workspace 隔离必须同批满足生产多用户验收。
12. session 恢复时必须校验 workspace membership 仍然存在。
13. 管理员重置密码采用临时密码 + 强制改密 + 审计，承认 v1 管理员具备潜在 impersonation 能力。
14. `/api/system/directories` 是 admin-only、默认关闭、白名单路径限制的敏感端点。
15. SQLite 搜索隔离通过 FTS JOIN 业务表 workspace_id 和 LIKE workspace filter 完成，不依赖 `search_index`。
16. legacy bootstrap 先创建 user/workspace 并回填已有 folders/projects，只补缺失默认项；空库才直接插默认项。
17. v1 默认 workspace 用 `users.default_workspace_id` 明确记录；finalizer 后设为 NOT NULL，并用复合 FK 保证它指向该用户 owned workspace。
18. `must_change_password` 进入 `RequestIdentity`，由 `RequirePasswordSettled()` gate 阻止业务路由。
19. 创建用户也使用临时密码 + 强制首次改密，不允许管理员设置用户长期密码。
20. 创建用户的 user/workspace/membership/default data/audit 写入必须在单事务内完成。
21. audit metadata 禁止记录密码、Cookie、session token、token hash 和外部服务密钥。
22. Postgres 和 SQLite provider 都要对齐接口，避免测试和本地模式产生第二套行为。
23. v1 不启用 PostgreSQL RLS，但保留后续添加 RLS 的空间。
24. 迁移时已有数据全部归入 bootstrap admin 默认 workspace。
25. 如果已有数据但没有 bootstrap admin 配置，后端启动失败，避免产生不可登录系统。
26. 禁用用户会撤销所有 session。
27. 不能禁用或降级最后一个 active admin。
28. `RequestIdentity` 只表示 actor，业务 repository 使用单独的 `WorkspaceScope` 选择目标 workspace。
29. 创建用户和 bootstrap 默认数据写入必须显式使用 target workspace scope，不能复用 admin 请求 workspace。
30. 旧单列外键必须按完整清单迁移为复合外键，roadmap node 父表必须补 workspace 维度唯一约束。
31. 禁用或降级 admin 的最后管理员保护必须有事务级并发保护。
32. 管理员用户列表保留 `q` 搜索参数，并在 repository filter 中实现。
33. SQLite 表重建迁移后必须 rebuild FTS 虚表和 triggers，并通过搜索 contract tests 验证。
34. `PATCH /api/admin/users/:id` 不负责 status/password/default workspace，只允许资料和 role；role 变化撤销目标用户 sessions。
35. inbox `converted_to` 保持 TEXT，但新写入必须用 typed format，并通过单事务转换流程保证和目标实体同 workspace。
36. `/api/system/directories` 必须配置开启才注册路由。

## 风险与缓解

### 风险：漏掉旧 repository facade 路径

当前代码中仍有不少 service 通过 `repository.GetNotes`、`repository.Search`、`repository.ListSyncTargets` 等包级函数访问数据。若这些路径没有拿到 request context，会绕过 workspace。

缓解：

- Phase 2 把所有受保护 handler 迁移为 `ctx + store` 显式传递。
- 旧 facade 在缺少 AuthContext 时返回错误，而不是回退全局查询。
- 用 `rg "context.Background()" backend/internal/repository backend/internal/service` 做实现前扫描，并为每条业务路径补测试。

### 风险：同步逻辑跨 workspace 串写

Obsidian/Notion 同步代码有批量读取 `ListAllNotes`、`ListSyncStatesByTarget` 的流程。若只改普通 note API，批量同步仍可能读到全局数据。

缓解：

- `ListAllNotes` 必须按 workspace 过滤。
- 所有 sync service 入口必须要求 AuthContext。
- target 查询、state 查询、external claim 查询统一按 workspace 过滤。

### 风险：迁移破坏主键、唯一约束和外键

`folders.id`、`task_projects.id` 当前是全局主键，而设计要求默认 id 在 workspace 内复用。回填 workspace 后不仅要替换唯一约束，还要替换主键和引用它们的外键。

缓解：

- 先添加 workspace_id 并回填。
- 先按“旧外键到复合外键迁移清单”删除所有旧单列外键，包括 `tasks.note_id`、`events.note_id`、roadmap 节点/资源、recurrence 和 sync 相关引用。
- 将 `folders`、`task_projects` 重建或迁移为 `PRIMARY KEY (workspace_id, id)`。
- 为清单中的每条路径重建复合外键。
- 为仍保留全局 id 主键的父表补 `UNIQUE(workspace_id, id)`，roadmap 节点额外补 `UNIQUE(workspace_id, roadmap_id, id)`，供复合外键引用。
- 再删除旧的全局唯一约束。
- 对 SQLite 使用重建表策略时，迁移前后跑 contract tests。

### 风险：Cookie 本地开发与生产配置不一致

生产必须 `Secure=true`，本地 HTTP 需要 `Secure=false`。

缓解：

- `FLOWSPACE_COOKIE_SECURE` 显式配置。
- `FLOWSPACE_ENV=prod` 时默认 true。
- dev 文档说明本地端口和 credentials 要求。

## 审查清单

请重点审查以下问题：

1. v1 是否接受“管理员创建用户，不开放注册”。
2. v1 是否接受“每个用户一个默认 workspace，暂不做多 workspace UI”。
3. 是否需要保留 GitHub 登录按钮为禁用状态，还是直接从登录页移除。
4. bootstrap admin 环境变量方式是否符合部署习惯。
5. 管理员不能查看用户业务数据这一边界是否符合预期。
6. 是否希望 v1 就启用 PostgreSQL RLS；当前设计建议后续增强。
