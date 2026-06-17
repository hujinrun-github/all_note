# FlowSpace / all_note

轻量 All-in-one 效率工具 — 笔记 + 任务 + 日历自然打通的日常效率中枢。

## 技术栈

| 层 | 技术 |
|---|------|
| 前端 | React 19 + TypeScript + Tailwind CSS v4 + Vite |
| 编辑器 | Tiptap v3 (ProseMirror) |
| 状态管理 | Zustand + TanStack React Query |
| 后端 | Go + Gin |
| 数据库 | SQLite (FTS5 全文搜索) |

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
│       ├── repository/        # 数据层 (SQLite + FTS5)
│       ├── service/           # 业务逻辑层
│       ├── model/             # 数据模型
│       ├── router/            # 路由注册
│       └── middleware/        # 中间件 (CORS)
└── README.md
```

## 启动方式

### 日常开发：测试服务（推荐）

```bash
make dev
```

`make dev` 默认启动测试服务，不会访问正式 SQLite：

- 前端：`http://127.0.0.1:15199`
- 后端：`http://127.0.0.1:18080/api`
- SQLite：`backend/flowspace.test.db`

自定义测试端口：

```bash
TEST_PORT=19080 TEST_FRONTEND_PORT=15199 make dev-test
```

Makefile 会调用通用启动脚本 `scripts/start-flowspace.mjs`，自动释放配置端口并启动后端与前端。
端口和存储隔离规则见 [docs/service-ports.md](docs/service-ports.md)。

### 正式/测试两套并行

```bash
# 测试服务：test DB + 独立入口，日常修改只用这一套
make dev-test
# http://127.0.0.1:15199
# http://127.0.0.1:18080/api
# backend/flowspace.test.db

# 正式服务：prod DB + 正式入口，需要明确使用 prod 命令
make dev-prod
# http://127.0.0.1:5199
# http://127.0.0.1:8080/api
# backend/flowspace.db

# 单独停止测试服务
make kill-test

# 单独停止正式服务
make kill-prod
```

### Tailscale 入口

当前 Tailscale Funnel 域名：

| 环境 | 外网入口 | 本地公开前端 | 后端 | SQLite |
|------|----------|--------------|------|--------|
| 正式 | `https://tylerhu-1.king-shiner.ts.net/all-note/` | `http://127.0.0.1:5198/all-note/` | `8080` | `backend/flowspace.db` |
| 测试 | `https://tylerhu-1.king-shiner.ts.net/all-note-test/` | `http://127.0.0.1:15198/all-note-test/` | `18080` | `backend/flowspace.test.db` |

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
# 正式库：backend/flowspace.db，8080/5199
node scripts/start-flowspace.mjs --env prod

# 测试库：backend/flowspace.test.db，18080/15199
node scripts/start-flowspace.mjs --env test

# 自定义 SQLite 文件
node scripts/start-flowspace.mjs --db tmp/local-sandbox.db

# 自定义启动命令
node scripts/start-flowspace.mjs \
  --backend-cmd "go run ./cmd/server" \
  --frontend-cmd "npm run dev -- --host 127.0.0.1 --port 5199"
```

也可以通过 Makefile 传参：

```bash
DB_ENV=prod make dev-prod
DB_PATH=tmp/local-prod-sandbox.db make dev-prod
TEST_DB_PATH=tmp/local-test-sandbox.db make dev-test
BACKEND_CMD="go run ./cmd/server" FRONTEND_CMD="npm run dev -- --host 127.0.0.1 --port 15199" make dev-test
```

Windows PowerShell 直接运行：

```powershell
node .\scripts\start-flowspace.mjs --env test
node .\scripts\start-flowspace.mjs --db tmp/local-sandbox.db
```

Linux/macOS 使用 GNU Make 即可，Makefile 不依赖平台专属 shell 语法；端口释放会优先使用 `lsof`，没有时退到 `fuser`。如果两者都未安装，脚本会继续启动，但不会自动清理已占用端口。

### 端口配置

| 环境 | 前端端口 | 后端端口 | 后端存储环境 | SQLite 文件 |
|------|----------|----------|--------------|-------------|
| 测试 | `15199` | `18080` | `FLOWSPACE_ENV=test` | `backend/flowspace.test.db` |
| 正式 | `5199` | `8080` | `FLOWSPACE_ENV=prod` | `backend/flowspace.db` |

`frontend/.env.example` 和本地 `frontend/.env` 默认使用 `VITE_BACKEND_PORT=18080`，用于日常开发测试服务；`frontend/.env.production` 固定为 `VITE_BACKEND_PORT=8080`，用于正式构建或正式服务。

### 数据库存储隔离

后端按环境隔离 SQLite。日常开发使用测试存储，启动后端或 seed 时设置：

```bash
FLOWSPACE_ENV=test go run ./cmd/server
```

这会监听测试端口 `18080` 并切换到 `backend/flowspace.test.db`。正式环境使用：

```bash
FLOWSPACE_ENV=prod go run ./cmd/server
```

这会监听正式端口 `8080` 并使用 `backend/flowspace.db`。如需完全自定义 SQLite 文件路径：

```bash
FLOWSPACE_DB_PATH=tmp/local-sandbox.db go run ./cmd/server
```

PowerShell 写法：

```powershell
$env:FLOWSPACE_ENV = "test"; go run ./cmd/server
$env:FLOWSPACE_DB_PATH = "tmp/local-sandbox.db"; go run ./cmd/server
```

`FLOWSPACE_DB_PATH` 优先级最高，适合临时验证或 CI；服务启动日志会打印当前环境和数据库路径。

### Obsidian 同步

Obsidian 同步支持配置一个 Vault 路径和一个同步目录。同步只扫描 `vault_path/base_folder` 中的 Markdown 文件，不会扫描整个 Vault。

同步规则：

- `Sync tags` 为空时，默认不同步任何笔记。
- 填写 `Sync tags` 后，只同步带有这些标签的 FlowSpace 笔记；从 Obsidian 手动拉取时，只导入或更新 frontmatter `tags` 命中的 Markdown。
- 点击 `同步到 Obsidian` 时，只把 FlowSpace 新增或修改的笔记写入 Obsidian。
- Obsidian 新增或修改的 Markdown 不会自动进入 FlowSpace；需要点击 `从 Obsidian 手动拉取`。
- 手动拉取时，Obsidian 新增的 Markdown 会导入 FlowSpace，Obsidian 修改的 Markdown 会更新 FlowSpace。
- 手动拉取遇到两边都修改时，Obsidian 内容优先。
- 手动拉取发现 Obsidian 删除已同步 Markdown 后，FlowSpace 只标记为“Obsidian 已删除”，需要在同步面板确认后才会删除 FlowSpace 笔记。
- 选择“保留并重新导出”会重新生成 Obsidian Markdown 文件。

### Notion 同步

FlowSpace 支持把一个个人 Notion Data Source 与本地笔记关联同步。默认同步方向是 FlowSpace 写入 Notion；Notion 内容进入 FlowSpace 需要手动拉取。手动拉取发生冲突时以 Notion 内容为准。

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
   - `Sync tags`：同步白名单。留空时默认不同步任何笔记；填写后只同步命中标签的 FlowSpace 笔记或 Notion 页面。
8. 点击 `Save Notion settings` 保存配置。
9. 点击 `Test Notion connection` 验证后端能访问该 Data Source。
10. 点击 `同步到 Notion`，把 FlowSpace 笔记写入 Notion。
11. 如需把 Notion 中新增或修改的页面同步回 FlowSpace，点击 `从 Notion 手动拉取`。

安全边界：

- FlowSpace 不在前端收集 Notion token，也不会把 token 写入 SQLite。
- SQLite 里只保存 `token_env` 这类环境变量名和 Data Source 配置。
- 当前实现是单后端实例使用环境变量授权，不是 Notion OAuth。多人部署时，需要由部署者为该后端实例配置对应的 token 环境变量。

同步规则：

- `Sync tags` 为空时，默认不同步任何笔记。
- 填写 `Sync tags` 后，只同步带有这些标签的 FlowSpace 笔记；从 Notion 手动拉取时，只导入或更新 `Tags` 属性命中的页面。
- 点击 `同步到 Notion` 时，FlowSpace 新笔记会创建 Notion 页面，FlowSpace 修改会更新已有 Notion 页面。
- Notion 新页面不会自动进入 FlowSpace；需要点击 `从 Notion 手动拉取`。
- 手动拉取时，Notion 新页面会导入 FlowSpace，Notion 修改会更新 FlowSpace。
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

# 测试后端：18080 + flowspace.test.db
FLOWSPACE_ENV=test ./server

# 正式后端：8080 + flowspace.db
FLOWSPACE_ENV=prod ./server

# 临时覆盖端口或 SQLite
FLOWSPACE_ENV=test PORT=19080 FLOWSPACE_DB_PATH=tmp/local-test.db ./server
```

### 2. 单独启动前端

```bash
cd frontend
pnpm install    # 首次运行需要

# 测试前端：15199，代理测试后端 18080
VITE_BACKEND_PORT=18080 npx vite --port 15199 --host 127.0.0.1

# 正式前端：5199，代理正式后端 8080
VITE_BACKEND_PORT=8080 npx vite --port 5199 --host 127.0.0.1
```

日常开发访问测试入口 **http://127.0.0.1:15199**。正式入口 **http://127.0.0.1:5199** 只用于真实数据验证。

### 3. 一行启动全部（WSL）

```bash
# 测试后端 + 测试前端
cd /mnt/d/MyGitProject/all_note/backend && go build -o server ./cmd/server && FLOWSPACE_ENV=test ./server &
cd /mnt/d/MyGitProject/all_note/frontend && VITE_BACKEND_PORT=18080 npx vite --port 15199 --host 127.0.0.1 &

# 正式后端 + 正式前端
cd /mnt/d/MyGitProject/all_note/backend && go build -o server ./cmd/server && FLOWSPACE_ENV=prod ./server &
cd /mnt/d/MyGitProject/all_note/frontend && VITE_BACKEND_PORT=8080 npx vite --port 5199 --host 127.0.0.1 &
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
