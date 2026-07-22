# FlowSpace / all_note

Deployment of the split control/data stores, credential keyring, and private
endpoint allowlist is documented in [Runtime settings deployment](docs/runtime-settings-deployment.md).

轻量 All-in-one 效率工具 — 笔记 + 任务 + 日历自然打通的日常效率中枢。

用户使用与服务配置说明见[用户设置指南](docs/user-settings.md)，其中包含 FunASR / SenseVoice 语音转写配置示例。

## 技术栈

| 层 | 技术 |
|---|------|
| 前端 | React 19 + TypeScript + Tailwind CSS v4 + Vite |
| 编辑器 | Tiptap v3 (ProseMirror) |
| 状态管理 | Zustand + TanStack React Query |
| 后端 | Go + Gin |
| 数据库 | PostgreSQL（默认）/ SQLite（FTS5 全文搜索）|

## 项目结构

```text
.
├── frontend/                  # React 前端
│   └── src/
│       ├── routes/            # 页面组件 (Dashboard, Notes, Editor, Tasks, Calendar, Inbox, Search)
│       ├── components/        # UI 组件 + 布局组件
│       ├── hooks/             # React Query hooks
│       ├── api/               # API 调用层
│       ├── stores/            # Zustand stores
│       └── styles/            # 全局 CSS
├── backend/                   # Go 后端
│   └── internal/
│       ├── handler/           # HTTP 处理器
│       ├── repository/        # 数据层
│       ├── storage/           # 存储抽象层（PostgreSQL / SQLite）
│       ├── service/           # 业务逻辑层
│       ├── model/             # 数据模型
│       ├── router/            # 路由注册
│       └── middleware/        # 中间件 (CORS)
└── README.md
```

## 存储后端

FlowSpace 支持两种存储后端：**PostgreSQL**（默认）和 **SQLite**。按部署场景选择。

### 快速对比

| | PostgreSQL（默认） | SQLite |
|---|---|---|
| **启动方式** | `make dev` / `make dev-prod`（已配好 PG） | 需显式切换：见下方 SQLite 启动 |
| **部署依赖** | 需要运行中的 PostgreSQL 实例 | 零依赖，文件即数据库 |
| **全文搜索** | `tsvector` + `tsquery` | FTS5 |
| **高级搜索** | ✅ 三元组模糊搜索（`pg_trgm`） | ❌ |
| **JSON 列** | ✅ JSONB | ❌ |
| **数组列** | ✅ PostgreSQL 原生数组 | ❌ |
| **时间范围** | ✅ `tsrange` | ❌ |
| **并发** | 多用户，连接池（最大 10 / 空闲 5） | 单写入者 + WAL 读取 |
| **适用场景** | 团队部署、生产环境 | 本地开发、离线使用、单用户 |

### PostgreSQL（默认）

后端默认使用 PostgreSQL。设置方式：

```bash
# 必须的环境变量
FLOWSPACE_DATABASE_DRIVER=postgres          # "postgres" 是默认值，可省略
FLOWSPACE_DATABASE_URL="postgres://user:password@host:port/dbname?sslmode=disable"
```

**示例 — 日常开发（测试库）：**

```bash
FLOWSPACE_DATABASE_URL="postgres://postgres:12345@192.168.1.70:19588/flowspace_test?sslmode=disable" make dev
```

**示例 — 正式库：**

```bash
FLOWSPACE_DATABASE_URL="postgres://postgres:12345@192.168.1.70:19588/flowspace?sslmode=disable" make dev-prod
```

> **项目默认 PostgreSQL 地址**：`192.168.1.70:19588`，用户 `postgres`，密码 `12345`。
> 测试库名 `flowspace_test`，正式库名 `flowspace`。

### SQLite

如需使用 SQLite（无外部数据库依赖），显式设置 driver 和文件路径：

```bash
FLOWSPACE_DATABASE_DRIVER=sqlite
FLOWSPACE_SQLITE_PATH="backend/flowspace.db"
```

**示例 — 测试环境：**

```bash
FLOWSPACE_DATABASE_DRIVER=sqlite \
FLOWSPACE_SQLITE_PATH="backend/flowspace.test.db" \
make dev
```

PowerShell：

```powershell
$env:FLOWSPACE_DATABASE_DRIVER = "sqlite"
$env:FLOWSPACE_SQLITE_PATH = "backend/flowspace.test.db"
make dev
```

**示例 — 后端单独启动（SQLite）：**

```bash
cd backend
FLOWSPACE_DATABASE_DRIVER=sqlite \
FLOWSPACE_SQLITE_PATH="flowspace.test.db" \
FLOWSPACE_ENV=test \
go run ./cmd/server
```

### 环境与数据库名校验

后端会校验环境与数据库名的对应关系，防止误操作：

| FLOWSPACE_ENV | PostgreSQL 库名要求 | SQLite 文件名要求 |
|---|---|---|
| `test` | 必须包含 `test`（如 `flowspace_test`） | 不能是 `flowspace.db` |
| `prod` | 不能是 `flowspace_test` | 不能是 `flowspace.test.db` |

### 从 SQLite 迁移到 PostgreSQL

项目内置迁移工具：

```bash
cd backend
FLOWSPACE_SQLITE_PATH="flowspace.db" \
FLOWSPACE_DATABASE_URL="postgres://postgres:12345@192.168.1.70:19588/flowspace?sslmode=disable" \
go run ./cmd/migrate_sqlite_to_pg
```

> 注意：两个后端之间的数据不共享。切换驱动意味着使用独立的数据库。迁移工具仅支持 SQLite → PostgreSQL 方向。

---

## 启动方式

### 日常开发：测试服务（推荐）

```bash
make dev
```

`make dev` 默认启动测试服务，使用 PostgreSQL（需配置 `FLOWSPACE_DATABASE_URL`）：

- 前端：`http://127.0.0.1:4100`
- 后端：`http://127.0.0.1:4101/api`
- PostgreSQL：`flowspace_test`
- SQLite（如已切换驱动）：`backend/flowspace.test.db`

自定义测试端口：

```bash
TEST_PORT=19080 TEST_FRONTEND_PORT=4100 make dev-test
```

Makefile 会调用通用启动脚本 `scripts/start-flowspace.mjs`，自动释放配置端口并启动后端与前端。
端口和存储隔离规则见 [docs/service-ports.md](docs/service-ports.md)。

### 正式/测试两套并行

```bash
# 测试服务：test DB + 独立入口，日常修改只用这一套
make dev-test
# http://127.0.0.1:4100
# http://127.0.0.1:4101/api
# PostgreSQL: flowspace_test  /  SQLite: backend/flowspace.test.db

# 正式服务：prod DB + 正式入口，需要明确使用 prod 命令
make dev-prod
# http://127.0.0.1:4200
# http://127.0.0.1:4201/api
# PostgreSQL: flowspace  /  SQLite: backend/flowspace.db

# 单独停止测试服务
make kill-test

# 单独停止正式服务
make kill-prod
```

### Tailscale 入口

当前 Tailscale Funnel 域名：

| 环境 | 外网入口 | 本地公开前端 | 后端 | 数据库 |
|------|----------|--------------|------|--------|
| 正式 | `https://tylerhu-1.king-shiner.ts.net/all-note/` | `http://127.0.0.1:4200/all-note/` | `4201` | PostgreSQL `flowspace` |
| 测试 | `https://tylerhu-1.king-shiner.ts.net/all-note-test/` | `http://127.0.0.1:4100/all-note-test/` | `4101` | PostgreSQL `flowspace_test` |

重启单个公开前端或重新写入 Tailscale 映射：

```bash
# 正式域名
make start-tailscale-frontend
make serve-tailscale

# 测试域名
make start-test-tailscale-frontend
make serve-test-tailscale

# 两个 Funnel path 一起写入
make serve-all-tailscale
```

Windows 下也可以一次启动两个公开前端并写入两个 Funnel path：

```powershell
.\.tailscale\start-flowspace-public.ps1
```

### 通用启动脚本

```bash
# 正式库（默认 PostgreSQL），4201/4200
node scripts/start-flowspace.mjs --env prod

# 测试库（默认 PostgreSQL），4101/4100
node scripts/start-flowspace.mjs --env test

# 自定义 SQLite 文件（需同时设置 FLOWSPACE_DATABASE_DRIVER=sqlite）
FLOWSPACE_DATABASE_DRIVER=sqlite node scripts/start-flowspace.mjs --db tmp/local-sandbox.db

# 自定义启动命令
node scripts/start-flowspace.mjs \
  --backend-cmd "go run ./cmd/server" \
  --frontend-cmd "npm run dev -- --host 127.0.0.1 --port 4200"
```

也可以通过 Makefile 传参：

```bash
DB_ENV=prod make dev-prod
DB_PATH=tmp/local-prod-sandbox.db make dev-prod                     # SQLite 模式
TEST_DB_PATH=tmp/local-test-sandbox.db make dev-test                 # SQLite 模式
BACKEND_CMD="go run ./cmd/server" FRONTEND_CMD="npm run dev -- --host 127.0.0.1 --port 4100" make dev-test
```

Windows PowerShell 直接运行：

```powershell
node .\scripts\start-flowspace.mjs --env test
node .\scripts\start-flowspace.mjs --db tmp/local-sandbox.db
```

Linux/macOS 使用 GNU Make 即可，Makefile 不依赖平台专属 shell 语法；端口释放会优先使用 `lsof`，没有时退到 `fuser`。如果两者都未安装，脚本会继续启动，但不会自动清理已占用端口。

### 端口配置

| 环境 | 前端端口 | 后端端口 | 后端存储环境 | 数据库 |
|------|----------|----------|--------------|--------|
| 测试 | `4100` | `4101` | `FLOWSPACE_ENV=test` | PostgreSQL `flowspace_test` / SQLite `backend/flowspace.test.db` |
| 正式 | `4200` | `4201` | `FLOWSPACE_ENV=prod` | PostgreSQL `flowspace` / SQLite `backend/flowspace.db` |

`frontend/.env.example` 和本地 `frontend/.env` 默认使用 `VITE_BACKEND_PORT=4101`，用于日常开发测试服务；`frontend/.env.production` 固定为 `VITE_BACKEND_PORT=4201`，用于正式构建或正式服务。

### 数据库存储隔离

后端按环境隔离数据库。支持 PostgreSQL 和 SQLite 两种驱动。

#### PostgreSQL 模式（默认）

日常开发使用测试存储，启动后端时设置：

```bash
# 测试库
FLOWSPACE_ENV=test \
FLOWSPACE_DATABASE_URL="postgres://postgres:12345@192.168.1.70:19588/flowspace_test?sslmode=disable" \
go run ./cmd/server

# 正式库
FLOWSPACE_ENV=prod \
FLOWSPACE_DATABASE_URL="postgres://postgres:12345@192.168.1.70:19588/flowspace?sslmode=disable" \
go run ./cmd/server
```

PowerShell 写法：

```powershell
$env:FLOWSPACE_ENV = "test"
$env:FLOWSPACE_DATABASE_URL = "postgres://postgres:12345@192.168.1.70:19588/flowspace_test?sslmode=disable"
go run ./cmd/server
```

#### SQLite 模式

```bash
# 测试库
FLOWSPACE_DATABASE_DRIVER=sqlite \
FLOWSPACE_SQLITE_PATH="flowspace.test.db" \
FLOWSPACE_ENV=test \
go run ./cmd/server

# 正式库
FLOWSPACE_DATABASE_DRIVER=sqlite \
FLOWSPACE_SQLITE_PATH="flowspace.db" \
FLOWSPACE_ENV=prod \
go run ./cmd/server
```

PowerShell 写法：

```powershell
$env:FLOWSPACE_ENV = "test"
$env:FLOWSPACE_DATABASE_DRIVER = "sqlite"
$env:FLOWSPACE_SQLITE_PATH = "flowspace.test.db"
go run ./cmd/server
```

> 启动日志会打印当前驱动、数据库名和存储能力（capabilities），方便确认实际使用的后端。

### 环境变量速查

| 变量 | 必填 | 默认值 | 说明 |
|---|---|---|---|
| `FLOWSPACE_ENV` | 否 | `prod` | 环境：`prod` 或 `test` |
| `FLOWSPACE_DATABASE_DRIVER` | 否 | `postgres` | 存储驱动：`postgres` 或 `sqlite` |
| `FLOWSPACE_DATABASE_URL` | PG 必填 | — | PostgreSQL 连接 URL |
| `FLOWSPACE_SQLITE_PATH` | SQLite 必填 | — | SQLite 数据库文件路径 |
| `PORT` | 否 | `4201` (prod) / `4101` (test) | 后端端口 |
| `FRONTEND_PORT` | 否 | `4200` (prod) / `4100` (test) | 前端开发服务器端口 |

### 笔记同步目标与绑定

FlowSpace 可以配置多个 Notion 或 Obsidian 同步目标，但一篇笔记最多只能选择一个同步目标。新建笔记默认不同步；只有在编辑器右侧的 `笔记同步` 中选择了目标后，这篇笔记才会参与单篇同步或目标批量 push。

使用规则：

- 在 `笔记` 页面点击 `同步` 管理同步目标。目标名称用于编辑器下拉框展示，建议用能区分外部空间的名字，例如 `个人 Notion 知识库`、`写作 Vault`。
- 在编辑器右侧 `笔记同步` 下拉框选择目标名；选择 `不同步` 表示该笔记不参与自动或批量 push。
- `同步此笔记` 固定把 FlowSpace 当前内容推送到该笔记绑定的目标。Notion 或 Obsidian 中的内容进入 FlowSpace 需要在同步面板里对指定目标手动拉取。
- 解除绑定后，旧 Obsidian 文件或 Notion page 即使仍带有 FlowSpace ID，也不会在下一次 pull/import 时自动把笔记重新绑定；需要用户在界面中手动确认重新绑定或重新导入。
- 一个外部资源同一时间只能被一个同步配置管理。Obsidian 按规范化文件路径识别，Notion 按 page id 识别。
- 目标已经被笔记绑定或已有外部资源声明后，不能再把 Obsidian `Vault path/base folder` 或 Notion `Data Source ID` 改成另一个外部空间；名称、启用状态、自动同步开关、`Sync tags`、Notion 标题属性和 token 环境变量名等非身份配置仍可编辑。要接入新的外部空间，请新建同步目标。

### Obsidian 同步

Obsidian 同步目标支持配置一个 Vault 路径和一个同步目录。同步只扫描 `vault_path/base_folder` 中的 Markdown 文件，不会扫描整个 Vault。

同步规则：

- 未绑定到该 Obsidian 目标的 FlowSpace 笔记不会被写入 Obsidian。
- 点击目标的 `同步到 Obsidian` 时，只把已绑定到该目标的 FlowSpace 新增或修改笔记写入 Obsidian。
- Obsidian 新增或修改的 Markdown 不会自动进入 FlowSpace；需要点击该目标的 `从 Obsidian 手动拉取`。
- `Sync tags` 用于手动拉取时的过滤：填写后，只导入或更新 frontmatter `tags` 命中的 Markdown；留空时不主动扩大导入范围。
- 手动拉取遇到两边都修改时，Obsidian 内容优先。
- 手动拉取发现 Obsidian 删除已同步 Markdown 后，FlowSpace 只标记为“Obsidian 已删除”，需要在同步面板确认后才会删除 FlowSpace 笔记。
- 选择“保留并重新导出”会重新生成 Obsidian Markdown 文件。

### Notion 同步

FlowSpace 支持把一个或多个个人 Notion Data Source 配置为同步目标。默认同步方向是 FlowSpace 写入绑定目标；Notion 内容进入 FlowSpace 需要对指定目标手动拉取。手动拉取发生冲突时以 Notion 内容为准。

用户关联自己的 Notion：

1. 在 Notion 创建一个 internal integration，并复制 integration token。
2. 创建或选择一个 Data Source，例如 `FlowSpace Notes`。推荐至少包含标题属性，默认属性名为 `Name`；如需从 Notion 手动拉取，请增加一个 `Tags` multi-select 属性用于同步过滤。
3. 在 Notion 中打开该 Data Source 所在页面，通过 `Add connections` / `Connections` 把刚创建的 integration 加进去。没有这一步，Notion API 会返回无权限。
4. 从 Notion URL 中复制 Data Source ID。FlowSpace 前端只需要填写这个 ID，不需要填写 token。
5. 在启动 FlowSpace 后端前设置 Notion token 环境变量：

```powershell
$env:FLOWSPACE_NOTION_TOKEN = "secret_xxx"
```

6. 启动或重启后端，让环境变量生效。
7. 在 FlowSpace 前端进入 `Notes`，点击 `同步`，切到 `Notion`：
   - `Data Source ID`：填写第 4 步复制的 Data Source ID。
   - `Token environment variable`：默认保持 `FLOWSPACE_NOTION_TOKEN`。
   - `Title property`：默认保持 `Name`，如果你的 Notion 标题属性叫别的名字，在这里改成对应名称。
   - `Sync tags`：手动拉取过滤条件。填写后只导入或更新 `Tags` 属性命中的 Notion 页面。
8. 点击 `保存 Notion 设置` 保存配置。
9. 点击 `测试 Notion 连接` 验证后端能访问该 Data Source。
10. 打开一篇 FlowSpace 笔记，在编辑器右侧 `笔记同步` 中选择这个 Notion 目标。
11. 点击 `同步此笔记` 或目标面板里的 `同步到 Notion`，把已绑定的 FlowSpace 笔记写入 Notion。
12. 如需把 Notion 中新增或修改的页面同步回 FlowSpace，点击该目标的 `从 Notion 手动拉取`。

Notion 字段映射：

| FlowSpace 笔记字段 | Notion 数据库位置 | Notion 属性类型 | 说明 |
|---|---|---|---|
| `title` | `Name`，或 Notion 设置中的 `Title property` | `title` | 笔记标题。默认属性名是 `Name`，如果 Notion 数据库标题属性叫别的名字，需要在配置里改成对应名称。 |
| `tags` | `Tags`，或 Notion 设置中的 `Tags property` | `multi_select` | 笔记标签。手动拉取时，`Sync tags` 也是通过这个属性过滤页面。 |
| `body` | Notion 页面正文 blocks | blocks | 笔记正文不会写入数据库的普通字段，而是写成页面内容。Markdown 会转换为段落、标题、列表、todo、引用、代码块和分割线等 Notion blocks。 |
| `id` | 可选的 `FlowSpace ID` 字段 | `rich_text` | 只有配置了 `flowspace_id_property` 时才会写入，用于在本地同步状态丢失后尝试找回对应页面；默认不写入。 |

目前不会自动写入 `Content`、`Notes`、`Source URL` 等自定义字段；这些字段可以保留给 Notion 内部使用。同步关系、Notion page id、内容哈希和上次同步状态保存在 FlowSpace 本地数据库的 `note_sync_state` / `sync_external_claims` 中，不作为 Notion 数据库字段保存。

安全边界：

- FlowSpace 不在前端收集 Notion token，也不会把 token 写入 SQLite 或 PostgreSQL。
- 数据库里只保存 `token_env` 这类环境变量名和 Data Source 配置。
- 当前实现是单后端实例使用环境变量授权，不是 Notion OAuth。多人部署时，需要由部署者为该后端实例配置对应的 token 环境变量。

同步规则：

- 未绑定到该 Notion 目标的 FlowSpace 笔记不会被写入 Notion。
- 点击目标的 `同步到 Notion` 时，FlowSpace 新笔记会创建 Notion 页面，FlowSpace 修改会更新已有 Notion 页面。
- Notion 新页面不会自动进入 FlowSpace；需要点击 `从 Notion 手动拉取`。
- 手动拉取时，Notion 新页面会导入 FlowSpace，Notion 修改会更新 FlowSpace。
- `Sync tags` 用于手动拉取过滤：填写后只导入或更新 `Tags` 属性命中的页面。
- 手动拉取遇到两边都修改时，Notion 覆盖 FlowSpace。
- 手动拉取发现 Notion 页面删除、归档或进入回收站后，FlowSpace 先标记为待确认删除。
- 包含暂不支持 Notion block 的页面会跳过写回，避免覆盖复杂内容。
- 自动化测试和本地 smoke test 可使用 mock provider：

```powershell
$env:NOTION_PROVIDER = "mock"
$env:FLOWSPACE_NOTION_TOKEN = "mock-token"
node .\scripts\start-flowspace.mjs --env test
```

### 1. 单独启动后端

```bash
cd backend
go build -o server ./cmd/server

# === PostgreSQL 模式（默认） ===

# 测试后端：4101 + flowspace_test
FLOWSPACE_ENV=test \
FLOWSPACE_DATABASE_URL="postgres://postgres:12345@192.168.1.70:19588/flowspace_test?sslmode=disable" \
./server

# 正式后端：4201 + flowspace
FLOWSPACE_ENV=prod \
FLOWSPACE_DATABASE_URL="postgres://postgres:12345@192.168.1.70:19588/flowspace?sslmode=disable" \
./server

# === SQLite 模式 ===

# 测试后端：4101 + flowspace.test.db
FLOWSPACE_DATABASE_DRIVER=sqlite \
FLOWSPACE_SQLITE_PATH="flowspace.test.db" \
FLOWSPACE_ENV=test \
./server

# 正式后端：4201 + flowspace.db
FLOWSPACE_DATABASE_DRIVER=sqlite \
FLOWSPACE_SQLITE_PATH="flowspace.db" \
FLOWSPACE_ENV=prod \
./server

# 临时覆盖端口
FLOWSPACE_ENV=test PORT=19080 ./server
```

### 2. 单独启动前端

```bash
cd frontend
pnpm install    # 首次运行需要

# 测试前端：4100，代理测试后端 4101
VITE_BACKEND_PORT=4101 npx vite --port 4100 --host 127.0.0.1

# 正式前端：4200，代理正式后端 4201
VITE_BACKEND_PORT=4201 npx vite --port 4200 --host 127.0.0.1
```

日常开发访问测试入口 **http://127.0.0.1:4100**。正式入口 **http://127.0.0.1:4200** 只用于真实数据验证。

### 3. 一行启动全部（WSL / Linux）

```bash
# PostgreSQL 模式 — 测试后端 + 测试前端
cd /mnt/d/MyGitProject/all_note/backend && go build -o server ./cmd/server && \
FLOWSPACE_ENV=test \
FLOWSPACE_DATABASE_URL="postgres://postgres:12345@192.168.1.70:19588/flowspace_test?sslmode=disable" \
./server &
cd /mnt/d/MyGitProject/all_note/frontend && VITE_BACKEND_PORT=4101 npx vite --port 4100 --host 127.0.0.1 &

# PostgreSQL 模式 — 正式后端 + 正式前端
cd ./backend && go build -o server ./cmd/server && \
FLOWSPACE_ENV=prod \
FLOWSPACE_DATABASE_URL="postgres://postgres:12345@119.91.114.203:19588/flowspace?sslmode=disable" \
./server &
cd ./frontend && VITE_BACKEND_PORT=4201 npx vite --port 4200 --host 127.0.0.1 &

# SQLite 模式 — 测试后端 + 测试前端
cd /mnt/d/MyGitProject/all_note/backend && go build -o server ./cmd/server && \
FLOWSPACE_DATABASE_DRIVER=sqlite FLOWSPACE_SQLITE_PATH="flowspace.test.db" FLOWSPACE_ENV=test ./server &
cd /mnt/d/MyGitProject/all_note/frontend && VITE_BACKEND_PORT=4101 npx vite --port 4100 --host 127.0.0.1 &
```

## 页面路由

| 路径 | 页面 | 说明 |
|------|------|------|
| `/` | 今日 | 三栏：今日任务 · 日程 · 最近笔记 |
| `/tasks` | 任务 | 按项目/状态筛选，行内添加 |
| `/notes` | 笔记 | 按文件夹浏览，点击进入编辑 |
| `/editor/:id` | 编辑器 | Markdown 编辑，工具栏 + 自动保存 |
| `/calendar` | 日历 | 月视图，点击日期查看日程 |
| `/inbox` | 收件箱 | 快速捕获项管理，批量归档/删除 |
| `/search` | 搜索 | FTS5 全文搜索，300ms 防抖 |

## API 端点

```
GET    /api/notes          # 笔记列表
GET    /api/notes/:id      # 笔记详情
POST   /api/notes          # 创建笔记
PATCH  /api/notes/:id      # 更新笔记
DELETE /api/notes/:id      # 删除笔记

GET    /api/tasks          # 任务列表
POST   /api/tasks          # 创建任务
PATCH  /api/tasks/:id      # 更新任务
DELETE /api/tasks/:id      # 删除任务

GET    /api/events         # 日程列表
POST   /api/events         # 创建日程
PATCH  /api/events/:id     # 更新日程
DELETE /api/events/:id     # 删除日程

GET    /api/inbox          # 收件箱列表
POST   /api/inbox          # 添加捕获项
POST   /api/inbox/:id/convert  # 转换捕获项
POST   /api/inbox/batch    # 批量操作
DELETE /api/inbox/:id      # 删除捕获项

GET    /api/folders        # 文件夹列表
GET    /api/search?q=      # 全文搜索 (FTS5)
GET    /api/today          # 今日视图聚合数据
```

## 设计系统

暖纸编辑风 (Warm Paper Editorial)：

- **背景**: `#f7f4ee` 暖奶油色
- **表面**: `#fefdf8` 暖纸白，带柔和阴影
- **强调色**: `#b87333` 铜色
- **标题字体**: Noto Serif SC (宋体)
- **正文字体**: PingFang SC / Noto Sans SC
