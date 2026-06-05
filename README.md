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

Makefile 会自动：
1. 杀掉占用端口的旧进程
2. 编译并启动后端
3. 启动前端开发服务器

### 端口配置

| 服务 | 环境变量 | 默认值 | 说明 |
|------|----------|--------|------|
| 后端 | `PORT` | `8080` | Go 服务监听端口 |
| 前端代理 | `VITE_BACKEND_PORT` | `8080` | Vite 将 `/api` 代理到后端端口 |

如需本地固定后端端口，可在 `frontend/.env` 中写入 `VITE_BACKEND_PORT=8080`。

### 1. 启动后端

```bash
cd backend
go build -o server ./cmd/server

# 默认 8080
./server

# 自定义端口
PORT=9090 ./server
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
