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

`users.password_hash` 保存 bcrypt hash。重置密码时生成新 hash，并撤销该用户所有现有 session。

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
  role TEXT NOT NULL DEFAULT 'user'
    CHECK (role IN ('admin', 'user')),
  status TEXT NOT NULL DEFAULT 'active'
    CHECK (status IN ('active', 'disabled')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_login_at TIMESTAMPTZ
);

CREATE UNIQUE INDEX users_email_lower_idx ON users (lower(email));

CREATE TABLE workspaces (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  owner_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

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

### 唯一约束调整

现有全局唯一约束需要改为 workspace 维度：

```sql
-- folders
UNIQUE (workspace_id, name)

-- task_projects
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

迁移脚本 `0004_multi_user_auth.sql` 分阶段执行：

1. 创建 `users`、`workspaces`、`workspace_members`、`sessions`、`audit_events`。
2. 根据环境变量创建 bootstrap admin：
   - `FLOWSPACE_BOOTSTRAP_ADMIN_EMAIL`
   - `FLOWSPACE_BOOTSTRAP_ADMIN_PASSWORD`
   - `FLOWSPACE_BOOTSTRAP_ADMIN_NAME`
3. 为 bootstrap admin 创建默认 workspace。
4. 给业务表新增 nullable `workspace_id`。
5. 将现有所有业务数据回填到 bootstrap workspace。
6. 为 `workspace_id` 建立索引和复合唯一约束。
7. 将 `workspace_id` 改为 NOT NULL。
8. 删除或替换旧的全局唯一约束。

启动保护：

- 如果数据库已有业务数据但没有任何用户，且未配置 bootstrap admin，后端启动失败并打印明确错误。
- 如果数据库是空库且未配置 bootstrap admin，可以启动，但只有 health 可访问；创建首个管理员需要命令行 seed 或配置环境变量后重启。

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
- 把 `AuthContext{UserID, WorkspaceID, Role}` 放入 Gin context 和 request context。

```text
backend/internal/storage/store.go
```

扩展：

```go
type Store interface {
    Close() error
    Health(context.Context) error
    Capabilities() Capabilities
    Transact(context.Context, func(Store) error) error
    Auth() AuthRepository
}

type AuthRepository interface {
    CreateUser(ctx context.Context, user *model.User) error
    GetUserByEmail(ctx context.Context, email string) (*model.User, error)
    GetUserByID(ctx context.Context, id string) (*model.User, error)
    ListUsers(ctx context.Context, page, pageSize int) ([]model.User, int, error)
    UpdateUser(ctx context.Context, id string, req *model.UpdateUserRequest) (*model.User, error)
    CreateWorkspace(ctx context.Context, workspace *model.Workspace) error
    AddWorkspaceMember(ctx context.Context, workspaceID, userID, role string) error
    CreateSession(ctx context.Context, session *model.Session) error
    GetSessionByTokenHash(ctx context.Context, tokenHash string) (*model.Session, error)
    RevokeSession(ctx context.Context, sessionID string) error
    RevokeUserSessions(ctx context.Context, userID string) error
    RecordAuditEvent(ctx context.Context, event *model.AuditEvent) error
}
```

### Request context

新增 context helper：

```go
type RequestIdentity struct {
    UserID      string
    WorkspaceID string
    Role        string
}
```

handler 获取方式：

```go
identity, ok := auth.IdentityFromContext(c.Request.Context())
```

repository 不直接依赖 Gin。workspace 通过 `context.Context` 或显式 filter 字段传入。推荐使用 context helper，减少接口签名大面积膨胀：

```go
ctx = auth.ContextWithIdentity(ctx, identity)
workspaceID, err := auth.WorkspaceIDFromContext(ctx)
```

所有 repository 写 SQL 前必须从 ctx 取 workspaceID。缺少 workspaceID 时返回 `auth.ErrMissingWorkspace`，handler 映射为 500，因为受保护路由缺少 workspace 是服务端接线错误。

### 路由分组

`router.Setup()` 调整为：

```go
api := r.Group("/api")
api.GET("/health", handler.Health)

authRoutes := api.Group("/auth")
authRoutes.POST("/login", handler.Login)
authRoutes.POST("/logout", authMiddleware.Optional(), handler.Logout)
authRoutes.GET("/me", authMiddleware.Required(), handler.Me)

protected := api.Group("")
protected.Use(authMiddleware.Required())
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
```

`/api/health` 保持公开，便于 Docker healthcheck。

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

用于前端启动时恢复登录态。返回当前用户、当前 workspace 和权限。

#### `POST /api/auth/logout`

撤销当前 session，清除 Cookie。即使 session 已失效也返回 204。

#### `POST /api/auth/change-password`

普通用户修改自己的密码。成功后撤销除当前 session 以外的其他 session。

## 账号管理平台

### 权限模型

`users.role` v1 只有两个值：

- `admin`：可进入账号管理，可创建、禁用、重置用户密码。
- `user`：只能访问自己的默认 workspace。

管理员不能禁用最后一个 active admin。后端在禁用或降级管理员时检查：

```text
active admin count must remain >= 1
```

### 用户列表

`GET /api/admin/users?page=1&page_size=20&q=alice`

返回字段：

```json
{
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
}
```

不返回 `password_hash`、session token、业务数据统计明细。

### 创建用户

`POST /api/admin/users`

请求：

```json
{
  "email": "new@example.com",
  "display_name": "New User",
  "password": "initial-secret",
  "role": "user"
}
```

副作用：

- 创建 user。
- 创建默认 workspace。
- 写入默认 folders 和 personal task project。
- 写入 workspace_members owner 记录。
- 写入 audit event `admin.user.create`。

### 重置密码

`POST /api/admin/users/:id/reset-password`

请求：

```json
{
  "password": "new-secret"
}
```

副作用：

- 更新 password hash。
- 撤销该用户所有 sessions。
- 写入 audit event `admin.user.reset_password`。

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
- 创建用户 modal。
- 重置密码 modal。
- 禁用/启用确认。

v1 不做复杂仪表盘，不显示业务数据量排行，避免把管理页面变成数据窥探入口。

## 配置

新增环境变量：

```text
FLOWSPACE_SESSION_SECRET
FLOWSPACE_COOKIE_SECURE
FLOWSPACE_ALLOWED_ORIGINS
FLOWSPACE_BOOTSTRAP_ADMIN_EMAIL
FLOWSPACE_BOOTSTRAP_ADMIN_PASSWORD
FLOWSPACE_BOOTSTRAP_ADMIN_NAME
```

规则：

- `FLOWSPACE_SESSION_SECRET` 用于 token hash pepper 或后续签名用途。生产环境必填。
- `FLOWSPACE_COOKIE_SECURE` 默认为 `true`，本地开发可设为 `false`。
- `FLOWSPACE_ALLOWED_ORIGINS` 替代当前硬编码 `http://localhost:5173`。
- bootstrap password 只在首次创建管理员时使用，不写入日志。

`docker-compose.yml` 增加这些 env 的透传。

## 错误码

认证相关：

- `UNAUTHENTICATED`：未登录或 session 失效，HTTP 401。
- `FORBIDDEN`：非管理员访问 admin API，HTTP 403。
- `INVALID_CREDENTIALS`：登录失败，HTTP 401。
- `ACCOUNT_DISABLED`：账号停用，HTTP 403。
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

### Storage contract tests

Postgres 和 SQLite 都要覆盖：

1. 用户 A 和用户 B 都有 `personal` task project，但互不冲突。
2. 用户 A 和用户 B 都有 `__uncategorized` folder，但互不冲突。
3. 同名 sync target 在不同 workspace 可创建。
4. `ListNotes` 只返回当前 workspace notes。
5. `GetNoteByID` 不能读取其他 workspace 的 note，即使知道 id。
6. `UpdateTask` 不能更新其他 workspace 的 task。
7. `Search` 只返回当前 workspace 的 note/task/event。
8. `ListSyncTargets` 只返回当前 workspace targets。
9. `GetDefaultTarget` 按 workspace + type 查找。
10. `task_occurrences` 完成记录按 workspace 隔离。

### API 集成测试

1. 未登录访问 `/api/notes` 返回 401。
2. 登录用户 A 创建 note。
3. 登录用户 B 访问用户 A note id 返回 404。
4. 登录用户 B 搜索用户 A note 标题返回空结果。
5. 管理员创建用户 C 后，用户 C 首次登录能看到空 workspace 默认数据。
6. 用户 C 退出后旧 Cookie 不再可访问 protected API。

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

验收：

- 未登录不能访问业务 API。
- 登录后能访问当前工作台。
- 退出后不能继续访问。

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
- 完成创建、禁用、启用、重置密码 UI。
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
9. 默认 folder/project id 在 workspace 内复用。
10. Postgres 和 SQLite provider 都要对齐接口，避免测试和本地模式产生第二套行为。
11. v1 不启用 PostgreSQL RLS，但保留后续添加 RLS 的空间。
12. 迁移时已有数据全部归入 bootstrap admin 默认 workspace。
13. 如果已有数据但没有 bootstrap admin 配置，后端启动失败，避免产生不可登录系统。
14. 禁用用户会撤销所有 session。
15. 不能禁用或降级最后一个 active admin。

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

### 风险：迁移破坏唯一约束

`folders.name`、`task_projects.name` 当前全局唯一。回填 workspace 后需要替换为复合唯一。

缓解：

- 先添加 workspace_id 并回填。
- 建新复合唯一索引。
- 再删除旧唯一约束。
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
