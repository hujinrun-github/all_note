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

### 一键启动（推荐）

```bash
make dev
```

默认后端端口 `8080`，前端端口 `5199`。自定义端口：

```bash
PORT=9090 FRONTEND_PORT=5199 make dev
```

Makefile 会调用通用启动脚本 `scripts/start-flowspace.mjs`，自动释放配置端口并启动后端与前端。

### 正式/测试两套并行

```bash
# 正式服务：prod DB + 旧入口
make dev
# http://127.0.0.1:5199
# http://127.0.0.1:8080/api

# 测试服务：test DB + 独立入口
make dev-test
# http://127.0.0.1:15199
# http://127.0.0.1:18080/api

# 单独停止测试服务
make kill-test
```

### Tailscale 入口

当前 Tailscale Funnel 域名：

```text
https://tylerhu-1.tail5cec87.ts.net/all-note/
https://tylerhu-1.tail5cec87.ts.net/all-note-test/
```

`/all-note/` 使用正式后端 `8080`；`/all-note-test/` 使用测试后端 `18080`。

如需重启测试域名前端或重新写入 Tailscale 映射：

```bash
make start-test-tailscale-frontend
make serve-test-tailscale
```

### 通用启动脚本

```bash
# 正式库：backend/flowspace.db
node scripts/start-flowspace.mjs --env prod

# 测试库：backend/flowspace.test.db
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
DB_ENV=prod make dev
DB_PATH=tmp/local-prod-sandbox.db make dev
TEST_DB_PATH=tmp/local-test-sandbox.db make dev-test
BACKEND_CMD="go run ./cmd/server" FRONTEND_CMD="npm run dev -- --host 127.0.0.1 --port 5199" make dev
```

Windows PowerShell 直接运行：

```powershell
node .\scripts\start-flowspace.mjs --env test
node .\scripts\start-flowspace.mjs --db tmp/local-sandbox.db
```

Linux/macOS 使用 GNU Make 即可，Makefile 不依赖平台专属 shell 语法；端口释放会优先使用 `lsof`，没有时退到 `fuser`。如果两者都未安装，脚本会继续启动，但不会自动清理已占用端口。

### 端口配置

| 服务 | 环境变量 | 默认值 | 说明 |
|------|----------|--------|------|
| 后端 | `PORT` | `8080` | Go 服务监听端口 |
| 后端存储环境 | `FLOWSPACE_ENV` | `prod` | `prod` 使用正式库，`test` 使用测试库 |
| 后端数据库路径 | `FLOWSPACE_DB_PATH` | 按环境决定 | 显式指定 SQLite 文件路径，优先级高于 `FLOWSPACE_ENV` |
| 前端代理 | `VITE_BACKEND_PORT` | `8080` | Vite 将 `/api` 代理到后端端口 |

如需本地固定后端端口，可在 `frontend/.env` 中写入 `VITE_BACKEND_PORT=8080`。

### 数据库存储隔离

后端默认使用正式数据库 `backend/flowspace.db`。如需使用测试存储，启动后端或 seed 时设置：

```bash
FLOWSPACE_ENV=test go run ./cmd/server
```

这会切换到 `backend/flowspace.test.db`。如需完全自定义 SQLite 文件路径：

```bash
FLOWSPACE_DB_PATH=tmp/local-sandbox.db go run ./cmd/server
```

PowerShell 写法：

```powershell
$env:FLOWSPACE_ENV = "test"; go run ./cmd/server
$env:FLOWSPACE_DB_PATH = "tmp/local-sandbox.db"; go run ./cmd/server
```

`FLOWSPACE_DB_PATH` 优先级最高，适合临时验证或 CI；服务启动日志会打印当前环境和数据库路径。

### Obsidian 双向同步

Obsidian 同步支持配置一个 Vault 路径和一个同步目录。双向同步只扫描 `vault_path/base_folder` 中的 Markdown 文件，不会扫描整个 Vault。

同步规则：

- FlowSpace 新增或修改的笔记会写入 Obsidian。
- Obsidian 新增的 Markdown 会导入 FlowSpace。
- Obsidian 修改的 Markdown 会更新 FlowSpace。
- 两边同时修改时，Obsidian 优先。
- Obsidian 删除已同步 Markdown 后，FlowSpace 只标记为“Obsidian 已删除”，需要在同步面板确认后才会删除 FlowSpace 笔记。
- 选择“保留并重新导出”会重新生成 Obsidian Markdown 文件。

### Notion 双向同步

FlowSpace 支持把一个个人 Notion Data Source 与本地笔记双向同步。冲突时以 Notion 内容为准。

准备步骤：

1. 在 Notion 创建一个 integration。
2. 创建或选择一个 Data Source，例如 `FlowSpace Notes`。
3. 把 Data Source 分享给该 integration。
4. 在启动后端前设置 Notion token：

```powershell
$env:FLOWSPACE_NOTION_TOKEN = "secret_xxx"
```

5. 在 FlowSpace 的笔记同步设置中打开 `Notion`，填写 Data Source ID。

同步规则：

- Notion 新页面会导入 FlowSpace。
- FlowSpace 新笔记会创建 Notion 页面。
- 两边同时修改时，Notion 覆盖 FlowSpace。
- Notion 页面删除、归档或进入回收站后，FlowSpace 先标记为待确认删除。
- 包含暂不支持 Notion block 的页面会跳过写回，避免覆盖复杂内容。
- 自动化测试和本地 smoke test 可使用 mock provider：

```powershell
$env:NOTION_PROVIDER = "mock"
$env:FLOWSPACE_NOTION_TOKEN = "mock-token"
node .\scripts\start-flowspace.mjs --env test
```

### 1. 启动后端

```bash
cd backend
go build -o server ./cmd/server

# 默认 8080
./server

# 自定义端口
PORT=9090 ./server

# 测试库
FLOWSPACE_ENV=test ./server
```

### 2. 启动前端

```bash
cd frontend
pnpm install    # 首次运行需要

# 默认 5173
pnpm dev

# 自定义端口 + 自定义后端
npx vite --port 5199 --host 127.0.0.1
```

如果后端端口不是默认 8080，启动前端时设置：
```bash
VITE_BACKEND_PORT=9090 npx vite --port 5199 --host 127.0.0.1
```

启动后访问 **http://127.0.0.1:5199**。

### 3. 一行启动全部（WSL）

```bash
# 后端 (默认 8080)
cd /mnt/d/MyGitProject/all_note/backend && go build -o server ./cmd/server && ./server &

# 前端 (5199)
cd /mnt/d/MyGitProject/all_note/frontend && npx vite --port 5199 --host 127.0.0.1 &
```

自定义后端端口：
```bash
PORT=9090 go run ./cmd/server &
VITE_BACKEND_PORT=9090 npx vite --port 5199 --host 127.0.0.1 &
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
