# FlowSpace Web App Design

> **日期:** 2026-05-27
> **状态:** 已确认
> **目标:** 将 FlowSpace 前端设计原型升级为可运行的 Web 应用（前端 Vite+React，后端 Go+SQLite）

## 技术选型

| 层 | 技术 | 理由 |
|----|------|------|
| 前端框架 | React 19 + TypeScript | React 19 已稳定 1.5 年，use()/actions/useOptimistic 可用。Tiptap 兼容 React 19 |
| 构建工具 | Vite | 快速 HMR，零配置启动 |
| CSS | Tailwind CSS + 自定义 tokens | 快速开发，设计规范映射到 config |
| 路由 | React Router v7 (createBrowserRouter) | URL 路由，浏览器前进后退 |
| UI 状态 | Zustand | 轻量，管理 sidebar/modal/theme |
| 服务端状态 | TanStack Query | API 缓存、乐观更新、重试。所有数据请求统一走 TanStack Query，RR 不承担数据加载职责 |
| 编辑器 | Tiptap | 基于 ProseMirror，支持 Markdown 序列化/反序列化，自定义 Node 扩展（TaskNode、EventNode），插件体系成熟 |
| 后端框架 | Go + Gin | 高性能 HTTP 框架，中间件生态成熟 |
| 数据库 | SQLite (modernc.org/sqlite) | 纯 Go 实现，无 CGO 依赖，跨平台编译友好，FTS5 全文搜索 |
| 笔记存储 | SQLite 为主存储 | 数据直接写入 SQLite，后续同步到 Notion/Obsidian |

> **数据流决策:** React Router 只做路由匹配和渲染，不参与数据加载（不使用 loader/action）。所有数据请求在组件层通过 TanStack Query hooks 发起。这意味着每个路由页面挂载时触发其 hooks，卸载时缓存保留，切换回来无需重新请求。

> **body 存储格式:** `notes.body` 存储 Markdown 明文。Tiptap 编辑器负责 Markdown ↔ ProseMirror JSON 的双向序列化。选择 Markdown 的理由：FTS5 索引到的是自然文本而非 JSON 结构字段，搜索质量更高；可迁移性强（任何编辑器都能打开 Markdown）。
>
> **tags 存储决策:** tags 使用 JSON 字符串 `TEXT NOT NULL DEFAULT '[]'`，不建关联表。v1 中 tags 仅用于：① 在 NoteCard 中展示标签列表，② 纳入 FTS5 索引供搜索。v1 不做"按 tag 筛选笔记列表"或"tag 聚合统计"。v2 如需侧边栏 tag 筛选，可迁移至 `note_tags` 关联表，JSON 数据作为迁移源。
>
> **folders 决策:** 建立独立的 `folders` 表，notes.folder 通过 folder_id 外键关联。理由：避免 SELECT DISTINCT 扫全表、folder 重命名只需改一行、删除笔记不留幽灵目录、后续可扩展 folder 排序/颜色。v1 仅支持一级目录（无嵌套），name 唯一约束。
>
> **word_count 决策:** 移除。v1 不计算字数。复杂的中英混合计数规则对核心价值（捕获→写作→回顾）无贡献，编辑器自带字数统计，需要时 v2 再加。
>
> **QuickCapture 自然语言解析:** v1 不做 NLP 解析。用户手动切换类型 tab（笔记/任务/日程），在文本框中输入标题后点击创建，直接入 inbox。

## 路由设计

| 路由 | 视图 | API |
|------|------|-----|
| `/` | Dashboard 今日视图 | `GET /api/today` |
| `/notes` | 笔记列表 | `GET/POST /api/notes` |
| `/editor/:id` | Markdown 编辑器 | `GET/PATCH /api/notes/:id` |
| `/tasks` | 任务管理 | `GET/POST/PATCH /api/tasks` |
| `/calendar` | 月历视图 | `GET /api/events?month=` |
| `/inbox` | 收件箱 | `GET/POST/DELETE /api/inbox` |
| `/search` | 全局搜索 | `GET /api/search?q=` |

所有路由共享 App Shell（Sidebar + TopBar + QuickBar + QuickCapture modal）。

## 组件定义

| 组件 | 类型 | 定义 |
|------|------|------|
| **Sidebar** | layout | 左侧 220px 导航栏：品牌 logo、导航项（今日/任务/笔记/日历/收件箱/搜索）、底部设置区 |
| **TopBar** | layout | 顶栏：当前视图标题 + 副标题 + 右侧操作按钮（快速捕获入口） |
| **QuickBar** | layout | 底部固定浮动栏（非导航，与 Sidebar 分工不同——Sidebar 管"去哪"，QuickBar 管"做什么"）：note/task/event 快速创建快捷按钮 + 快速捕获主入口。桌面端始终可见（编辑器底部 padding 防遮挡），移动端 compact 模式仅显示捕获按钮。z-index 低于 QuickCapture modal |
| **QuickCapture** | modal | Cmd+Shift+K 唤起（避免与浏览器 Cmd+K / Spotlight 冲突）、520px 居中 modal。上半区：类型切换（笔记/任务/日程）+ 文本输入 + 创建按钮；下半区：最近 3 条未整理的 inbox 条目列表（可快速跳转 Inbox 页或直接 convert）。v1 不做自然语言解析 |

## 数据模型

所有时间字段使用 Unix 时间戳（秒），ID 使用 UUID v4。

### SQLite 连接配置

```sql
PRAGMA journal_mode=WAL;       -- 读写并发不互斥
PRAGMA busy_timeout=5000;      -- 并发写冲突时等待 5 秒而非立即报 SQLITE_BUSY
PRAGMA foreign_keys=ON;        -- 启用外键约束
```

### folders
```sql
CREATE TABLE folders (
  id TEXT PRIMARY KEY,
  name TEXT UNIQUE NOT NULL,
  sort_order REAL NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);

-- 默认分类，seed.sql 中预插入
INSERT INTO folders (id, name, sort_order, created_at) VALUES
  ('__uncategorized', '未分类', 0, unixepoch()),
  ('__work', '工作', 1, unixepoch()),
  ('__personal', '个人', 2, unixepoch());
```

### notes
```sql
-- rowid 为 INTEGER PRIMARY KEY，FTS5 通过它关联
CREATE TABLE notes (
  rowid INTEGER PRIMARY KEY AUTOINCREMENT,
  id TEXT UNIQUE NOT NULL,
  title TEXT NOT NULL,
  body TEXT NOT NULL DEFAULT '',
  folder_id TEXT NOT NULL DEFAULT '__uncategorized' REFERENCES folders(id),
  tags TEXT NOT NULL DEFAULT '[]',  -- JSON array
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

-- content='notes' 表示 FTS5 不独立存储，直接读取 notes 表当前数据
-- 前提是 notes 必须有 INTEGER rowid（上面已满足）
CREATE VIRTUAL TABLE notes_fts USING fts5(title, body, tags, content='notes', content_rowid='rowid');

-- FTS5 同步触发器
CREATE TRIGGER notes_ai AFTER INSERT ON notes BEGIN
  INSERT INTO notes_fts(rowid, title, body, tags) VALUES (new.rowid, new.title, new.body, new.tags);
END;

CREATE TRIGGER notes_ad AFTER DELETE ON notes BEGIN
  INSERT INTO notes_fts(notes_fts, rowid, title, body, tags) VALUES ('delete', old.rowid, old.title, old.body, old.tags);
END;

CREATE TRIGGER notes_au AFTER UPDATE ON notes BEGIN
  INSERT INTO notes_fts(notes_fts, rowid, title, body, tags) VALUES ('delete', old.rowid, old.title, old.body, old.tags);
  INSERT INTO notes_fts(rowid, title, body, tags) VALUES (new.rowid, new.title, new.body, new.tags);
END;
```

### tasks
```sql
CREATE TABLE tasks (
  rowid INTEGER PRIMARY KEY AUTOINCREMENT,
  id TEXT UNIQUE NOT NULL,
  title TEXT NOT NULL,
  project TEXT,
  due INTEGER,                     -- 截止日当天 00:00:00 的时间戳（日期级别精度）
  priority INTEGER NOT NULL DEFAULT 0,
  done INTEGER NOT NULL DEFAULT 0,
  scope TEXT NOT NULL DEFAULT 'daily',  -- daily/monthly/yearly
  sort_order REAL NOT NULL DEFAULT 0,   -- 拖拽排序用，新任务默认 0（最后），插入到指定位置用 fractional indexing
  note_id TEXT REFERENCES notes(id) ON DELETE SET NULL,  -- 删除笔记不清除任务，仅解除关联
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE VIRTUAL TABLE tasks_fts USING fts5(title, content='tasks', content_rowid='rowid');

CREATE TRIGGER tasks_ai AFTER INSERT ON tasks BEGIN
  INSERT INTO tasks_fts(rowid, title) VALUES (new.rowid, new.title);
END;
CREATE TRIGGER tasks_ad AFTER DELETE ON tasks BEGIN
  INSERT INTO tasks_fts(tasks_fts, rowid, title) VALUES ('delete', old.rowid, old.title);
END;
CREATE TRIGGER tasks_au AFTER UPDATE ON tasks BEGIN
  INSERT INTO tasks_fts(tasks_fts, rowid, title) VALUES ('delete', old.rowid, old.title);
  INSERT INTO tasks_fts(rowid, title) VALUES (new.rowid, new.title);
END;
```

> **due vs events 时间精度:** `tasks.due` 存储当天 00:00:00（日期级别），语义为"截止到这一天"；`events.start_time/end_time` 存储精确到秒的时间戳。前端传入 due 时只传日期字符串，后端解析为当天 00:00:00 UTC 时间戳。查询 overdue 用 `due < today_00:00:00`，查询 today 用 `due >= today_00:00:00 AND due < tomorrow_00:00:00`。

### events
```sql
CREATE TABLE events (
  rowid INTEGER PRIMARY KEY AUTOINCREMENT,
  id TEXT UNIQUE NOT NULL,
  title TEXT NOT NULL,
  start_time INTEGER NOT NULL,
  end_time INTEGER NOT NULL,
  location TEXT,
  kind TEXT NOT NULL DEFAULT 'work',  -- work/personal/reminder
  note_id TEXT REFERENCES notes(id) ON DELETE SET NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE VIRTUAL TABLE events_fts USING fts5(title, location, content='events', content_rowid='rowid');

CREATE TRIGGER events_ai AFTER INSERT ON events BEGIN
  INSERT INTO events_fts(rowid, title, location) VALUES (new.rowid, new.title, new.location);
END;
CREATE TRIGGER events_ad AFTER DELETE ON events BEGIN
  INSERT INTO events_fts(events_fts, rowid, title, location) VALUES ('delete', old.rowid, old.title, old.location);
END;
CREATE TRIGGER events_au AFTER UPDATE ON events BEGIN
  INSERT INTO events_fts(events_fts, rowid, title, location) VALUES ('delete', old.rowid, old.title, old.location);
  INSERT INTO events_fts(rowid, title, location) VALUES (new.rowid, new.title, new.location);
END;
```

> **实现注意:** `GET /api/events?month=2026-05` 查询使用时间区间重叠逻辑：`start_time < end_of_month AND end_time > start_of_month`。这样跨月事件（如 4月28日→5月3日）在 5 月的月历视图中依然可见。`month` 是 YYYY-MM 字符串，后端解析为当月范围的 Unix 时间戳。

### inbox
```sql
CREATE TABLE inbox (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL,  -- note/task/event
  title TEXT NOT NULL,
  body TEXT,
  source TEXT NOT NULL DEFAULT 'quick-capture',
  archived INTEGER NOT NULL DEFAULT 0,
  converted_to TEXT,   -- 转成 note/task/event 后的目标 ID；NULL 表示未处理
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
```

`archived` 和 `converted_to` 的区别：
- `converted_to` 非空 → 已转化（转成了笔记/任务/日程），不再显示在 inbox 中
- `archived=1` 且 `converted_to` 为空 → 用户手动归档（暂不处理，但保留记录）

## API 契约

### 成功响应格式
```json
{
  "data": { ... },
  "pagination": { "page": 1, "page_size": 20, "total": 150 }
}
```
单条数据接口（GET/:id、POST、PUT、PATCH、DELETE）不返回 pagination。

### 错误响应格式
```json
{
  "error": { "code": "NOT_FOUND", "message": "笔记不存在" }
}
```

错误码: `NOT_FOUND` / `BAD_REQUEST` / `VALIDATION_ERROR` / `INTERNAL_ERROR`

### 分页

所有 list 接口统一分页参数 `?page=1&page_size=20`（默认 page_size=20，最大 100）。

### CORS 与代理

开发环境 Vite dev server (:5173) 和 Gin (:8080) 不同端口，使用 Vite proxy 让 `/api` 请求同源：

```ts
// vite.config.ts
export default defineConfig({
  server: {
    proxy: { '/api': 'http://localhost:8080' }
  }
});
```

生产环境 Gin 托管前端构建产物，天然同源无需 CORS。开发环境作为后备，Gin 配置 CORS 中间件允许 `http://localhost:5173`。

### 通用说明

- **PATCH** — 部分更新。只更新请求体中出现的字段，未传的字段保持不变。v1 只用 PATCH，不做 PUT（全量替换）——前端统一发 PATCH 请求，降低实现和测试负担。
- 列表接口默认排序：notes/events/inbox 按 `created_at DESC`，tasks 按 `sort_order ASC, created_at DESC`（支持手动拖拽排序）。

### Folders
- `GET /api/folders` → `{ data: { folders: Folder[] } }`
  - 返回 `[{ id, name, sort_order, note_count }, ...]`，按 sort_order 排序。note_count 为 COUNT JOIN 结果。
  - POST/PUT/DELETE 暂不提供（v1 中 folder 通过 notes PATCH 的 folder_id 自动使用，无需独立管理）

### Notes
- `GET /api/notes?folder_id=&sort=recent|az&page=1&page_size=20` → `{ data: { notes: Note[] }, pagination }`
- `GET /api/notes/:id` → `{ data: { note: Note } }`
- `POST /api/notes` → `{ data: { note: Note } }`  body: `{ title, body?, folder_id?, tags? }`
- `PATCH /api/notes/:id` → `{ data: { note: Note } }`  body: 部分字段（`folder_id` 可更换到已有 folder）
- `DELETE /api/notes/:id` → `204`

### Tasks
- `GET /api/tasks?project=&status=all|active|done&scope=&page=1&page_size=20` → `{ data: { tasks: Task[] }, pagination }`
- `POST /api/tasks` → `{ data: { task: Task } }`
- `PATCH /api/tasks/:id` → `{ data: { task: Task } }`  body: `{ done?, title?, due?, priority?, scope?, project?, sort_order? }`
- `DELETE /api/tasks/:id` → `204`

### Events
- `GET /api/events?month=2026-05&page=1&page_size=20` → `{ data: { events: Event[] }, pagination }`
  - 返回与当月有时间重叠的所有 events（`start_time < end_of_month AND end_time > start_of_month`），跨月事件在月初/月末均可见
- `POST /api/events` → `{ data: { event: Event } }`
- `PATCH /api/events/:id` → `{ data: { event: Event } }`
- `DELETE /api/events/:id` → `204`

### Inbox
- `GET /api/inbox?kind=all|note|task|event&page=1&page_size=20` → `{ data: { items: InboxItem[] }, pagination }`
  - 只返回 `archived=0 AND converted_to IS NULL` 的条目
- `POST /api/inbox`  body: `{ kind: "note"|"task"|"event", title, body? }` → `{ data: { item: InboxItem } }`
- `POST /api/inbox/:id/convert`  body: `{ kind: "note"|"task"|"event" }` → `{ data: { item: Note|Task|Event } }`
  - 根据 kind 创建对应的 note/task/event 记录，回写 `inbox.converted_to` 为新记录的 ID
  - 创建 note 时 title 来自 inbox.title，body 来自 inbox.body
  - 创建 task 时 title 来自 inbox.title，due/priority/project 取默认值，后续在任务管理页编辑
  - 创建 event 时 title 来自 inbox.title，start_time 默认设为次日 9:00，end_time 默认设为次日 10:00
- `POST /api/inbox/batch`  body: `{ ids: string[], action: "archive"|"delete" }` → `{ data: { affected: number } }`
  - 批量归档或删除 inbox 条目。QuickCapture modal 中用户可勾选多条一键处理
- `DELETE /api/inbox/:id` → `204`

### Search
- `GET /api/search?q=&page=1&page_size=20` → `{ data: { items: SearchResult[] }, pagination }`

    `SearchResult` 类型:
    ```json
    { "type": "note", "id": "uuid", "title": "...", "highlight": "...", "folder_id": "...", "updated_at": 1234567890 }
    { "type": "task", "id": "uuid", "title": "...", "highlight": "...", "done": false, "updated_at": 1234567890 }
    { "type": "event", "id": "uuid", "title": "...", "highlight": "...", "kind": "work", "updated_at": 1234567890 }
    ```

    三种类型混合后按 `updated_at DESC` 统一排序，`pagination.total` 是匹配总条数。单一次查询返回统一流，前端按 `type` 分组渲染。

    > **FTS5 查询语法:** 空格分隔的词 → 隐式 AND（所有词都匹配）。双引号包裹 → phrase 匹配（连续完整出现）。前端搜索输入直接传 raw string，后端将其转为 FTS5 查询语法。`highlight` 字段返回匹配片段（包含 `<mark>` 标签），最多 200 字符。
    >
    > **搜索实现与分页策略:**
    > 1. 对三张 FTS5 表分别执行查询，每张表取 `page_size * 3` 条结果（为合并排序留足余量）
    > 2. 每张表记录各自的 match count，三表 sum 得到 `pagination.total`
    > 3. Service 层将所有结果按 `updated_at DESC` 合并排序
    > 4. 排序后按 `(page-1)*page_size` 到 `page*page_size` 切片返回当前页
    >
    > 这个策略在 v1 数据量下准确可靠。page_size * 3 的余量避免了"某张表前 N 条全排在后面导致缺页"的边界情况。v2 数据量大时可改为每表取 `page * page_size` 条然后合并，仍能保证准确。

### Today (Dashboard)
- `GET /api/today` → `{ data: { todayTasks: Task[], overdueTasks: Task[], events: Event[], recentNotes: Note[] } }`
  - `todayTasks`: 当天任务（due 在今天范围内，且 done=0）
  - `overdueTasks`: 逾期任务（due < 今天 00:00:00，且 done=0），最多返回 10 条，按 due ASC（最早逾期排最前）。仅取逾期不超过 OVERDUE_WINDOW_DAYS 天的任务
  - `events`: 当天日程（start_time 与今天有时间重叠）
  - `recentNotes`: 最近 5 条笔记

> **逾期任务截止窗口 `OVERDUE_WINDOW_DAYS`:** 后端常量，默认值 7。Dashboard 只展示"未来 7 天内"和"逾期不超过 7 天"的任务。超过窗口的任务不在 Dashboard 显示，但仍出现在 `/tasks` 管理视图中。这个窗口避免 Dashboard 被堆积任务淹没，同时引导用户定期在 Tasks 页做清理评审。后续可通过配置调整。

## 前端项目结构

React Router v7 使用 `createBrowserRouter`，路由只做匹配和渲染。所有数据请求在组件内通过 TanStack Query hooks 发起。

```
frontend/src/
├── main.tsx                    # createRoot, RouterProvider
├── router.tsx                  # createBrowserRouter 路由配置
├── App.tsx                     # Sidebar + TopBar + <Outlet> + QuickBar + QuickCapture
├── api/
│   ├── client.ts               # fetch 封装: baseURL/basePath（Vite dev 时 /api，生产同源）, error handling, request/response interceptors
│   ├── notes.ts                # notes CRUD 请求函数
│   ├── tasks.ts
│   ├── events.ts
│   ├── inbox.ts
│   └── search.ts
├── routes/
│   ├── Dashboard.tsx           # 3-column 今日视图
│   ├── Notes.tsx              # folder sidebar + note list
│   ├── Editor.tsx             # Markdown 编辑，Tiptap
│   ├── Tasks.tsx              # 任务管理，filter/section/checkbox
│   ├── Calendar.tsx           # 月历视图 + event list
│   ├── Inbox.tsx              # 收件箱 + kind filter + convert action
│   └── Search.tsx             # 搜索结果 grouped by type
├── components/
│   ├── ui/
│   │   ├── NoteCard.tsx
│   │   ├── TaskRow.tsx
│   │   ├── EventChip.tsx
│   │   ├── MiniCalendar.tsx
│   │   └── Tag.tsx
│   ├── layout/
│   │   ├── Sidebar.tsx
│   │   ├── TopBar.tsx
│   │   └── QuickBar.tsx
│   └── QuickCapture.tsx
├── stores/
│   └── ui.ts                   # Zustand store
├── hooks/
│   ├── useNotes.ts             # TanStack Query hooks: useNotesList, useNote, useCreateNote, useUpdateNote, useDeleteNote
│   ├── useTasks.ts
│   ├── useEvents.ts
│   └── useSearch.ts
└── styles/
    └── tokens.css              # CSS custom properties
```

### 页面状态设计

每个路由页面注入 TanStack Query 的三个核心状态。统一设计原则：Loading 用骨架屏不闪屏，Empty 给引导文案和 CTA，Error 给错误信息和重试按钮。

| 路由 | Loading | Empty | Error |
|------|---------|-------|-------|
| **Dashboard** `/` | 3 列骨架屏（任务列 4 行、日历格子、笔记列 3 行） | "今天还没有任务" + 快速捕获入口按钮 | 顶栏下方内联红色提示 "加载失败" + 重试按钮 |
| **Notes** `/notes` | 左侧 folder 列表骨架 + 右侧 6 行笔记骨架 | 当前分类下 "暂无笔记，Cmd+K 开始写第一条" | 同上 |
| **Editor** `/editor/:id` | 编辑器区域骨架（标题栏 + 正文 4 行） | 不适用（从 Notes 路由跳转，note 一定存在） | "笔记加载失败" + 返回按钮（路由回退到 /notes） |
| **Tasks** `/tasks` | 筛选栏骨架 + 任务列表 5 行骨架 | 当前筛选下 "没有任务"（文案按筛选条件变化："所有任务已完成" / "项目A 下暂无任务"） | 同 Dashboard |
| **Calendar** `/calendar` | 月历格子骨架（7×5 灰色方块） | 不适用（格子始终渲染，空白天不视为 empty） | 同 Dashboard |
| **Inbox** `/inbox` | 类型筛选栏 + 5 行条目骨架 | "收件箱为空，Cmd+K 捕获第一条" | 同 Dashboard |
| **Search** `/search` | 不适用（搜索框始终可见，按需触发） | 搜索无结果时 "没有找到匹配 'xxx' 的结果" | "搜索失败" + 重试按钮 |
| **QuickCapture** modal | 不适用（modal 内容是本地状态，不发网络请求） | 不适用 | 创建失败时在 modal 底部显示红色提示，不关闭 modal |

> **骨架屏实现:** 复用对应 UI 组件的尺寸但填充灰色脉冲动画。每个组件目录下加 `ComponentName.skeleton.tsx`。

## 开发工具链

### 前端
```
frontend/
├── .eslintrc.cjs               # @antfu/eslint-config 或 eslint-plugin-react-hooks + @typescript-eslint
├── .prettierrc                 # singleQuote, semi, tabWidth: 2, trailingComma: es5
├── tsconfig.json               # strict: true, paths: { @/*: ["./src/*"] }
├── tsconfig.node.json          # vite config 用
└── vite.config.ts              # proxy /api → :8080
```

**Lint/Format 命令:**
- `pnpm dev` — Vite dev server
- `pnpm build` — tsc + vite build
- `pnpm lint` — eslint src/
- `pnpm format` — prettier --write src/

### 后端
```
backend/
├── Makefile                    # 常用命令
├── go.mod
└── go.sum
```

**Makefile targets:**
```makefile
.PHONY: dev build test lint seed

dev:     # go run cmd/server/main.go
build:   # CGO_ENABLED=0 go build -o bin/server cmd/server/main.go
test:    # go test ./internal/... -cover -count=1
lint:    # go vet ./... && staticcheck ./...
seed:    # 初始化 DB + 插入测试数据（独立 cmd/seed/main.go 或 flag）
```

Go 项目使用 Go modules（`go.mod`），代码格式化依赖 `gofmt`（Go 内置），不额外引入 prettier。静态检查用 `go vet` + `staticcheck`。

`seed` 命令插入 10 条笔记、10 条任务、5 条日程、3 条 inbox 条目，供前端开发调试。

## 后端项目结构

采用分层架构: handler (HTTP层) → service (业务逻辑) → repository (数据访问)。

```
backend/
├── cmd/server/main.go          # 入口: 初始化 DB、注册路由、启动 Gin
├── internal/
│   ├── router/
│   │   └── router.go           # Gin 路由注册，关联 handler 与路由路径
│   ├── handler/
│   │   ├── notes.go            # 参数绑定 + 校验 + 调用 service + 响应格式化
│   │   ├── tasks.go
│   │   ├── events.go
│   │   ├── inbox.go
│   │   ├── search.go
│   │   ├── folders.go
│   │   └── today.go
│   ├── service/
│   │   ├── folders.go
│   │   ├── notes.go            # 业务逻辑: 标签处理、FTS 索引维护
│   │   ├── tasks.go
│   │   ├── events.go
│   │   ├── inbox.go            # 包含 convert 逻辑: 创建目标记录 + 回写 converted_to
│   │   ├── search.go           # FTS5 查询语法转换 + 三类型合并排序
│   │   └── today.go
│   ├── repository/
│   │   ├── notes.go            # SQL 查询封装
│   │   ├── tasks.go
│   │   ├── events.go
│   │   ├── inbox.go
│   │   └── folders.go
│   ├── model/
│   │   ├── note.go             # struct + JSON tags
│   │   ├── task.go
│   │   ├── event.go
│   │   ├── inbox.go
│   │   └── folder.go
│   └── middleware/
│       ├── cors.go             # 开发环境 CORS
│       └── logger.go           # 请求日志（复用 Gin 内置 Logger/Recovery）
├── db/
│   ├── schema.sql              # 完整 DDL（包含 FTS5 触发器）
│   └── seed.sql                # 开发测试数据
├── go.mod
└── go.sum
```

**中间件链** (按顺序): `gin.Logger()` → `gin.Recovery()` → `CORS` → 业务 handler。

**数据库迁移策略:** v1 简单方案——`db/schema.sql` 作为建表脚本，`cmd/server/main.go` 启动时读取并执行。表已存在时跳过（`IF NOT EXISTS`）。后续版本升级时在 `db/migrations/` 目录下按序号管理 SQL 文件，使用 golang-migrate 执行。

**测试策略:**
- `handler` → 使用 `net/http/httptest` + Gin `TestMode` 做 API 级测试。Mock service 层，验证请求绑定、响应格式、错误码
- `service` → 纯单元测试，mock repository 接口。覆盖 FTS5 查询转换、分页合并逻辑、inbox convert 转化流程、逾期窗口过滤
- `repository` → 集成测试，使用 `:memory:` SQLite 实例。`setup_test.go` 中执行 schema.sql + seed.sql，每次测试前清理数据。覆盖 CRUD、FTS5 搜索、WAL 并发读写
- `_test.go` 与源文件同目录（Go 惯例），测试覆盖率目标 ≥ 70%

## 设计规范

- 色彩、字体、间距、圆角、阴影从 `sketches/THEME.md` 和 `App.jsx` CSS 提取到 Tailwind config
- 已有的 1750 行 CSS 转为 Tailwind utilities + tokens.css（不逐行迁移，按组件重写）
- 响应式断点: 360/390/430/600/820/1024/1366/1440/1920
- Quick Capture: 520px 居中 modal，slide-down 动画，Cmd+Shift+K 快捷键唤起（避免与浏览器内置 Cmd+K 冲突）。modal 状态由 Zustand 管理（非组件本地 state），切换路由时内容不丢失
- Editor: 740px 最大宽度写作列，左侧悬浮工具栏

## 实现顺序

1. 后端: Go 项目初始化 → SQLite schema + WAL 配置 + FTS5 触发器 → CRUD API + 分页 + 错误处理 → inbox convert → 搜索 FTS5 → today 聚合（含逾期窗口）
2. 前端: Vite 项目初始化（含 proxy 配置）→ Tailwind + tokens → Router + App Shell → Sidebar + TopBar + QuickBar
3. 前端: Dashboard（今日视图含逾期分组）→ Quick Capture → Tasks → Notes → Editor（Tiptap）→ Calendar → Inbox（含 convert）→ Search
4. 串联: TanStack Query 接入后端 API → 真实数据替换 mock
5. 同步: SQLite → Notion/Obsidian 单向导出同步模块（格式映射 + 增量导出，不做实时云同步）

## 不做的事

- 用户认证/多用户
- 多设备实时同步
- WebSocket/实时协作
- 文件上传/附件
- 插件系统
- 导出/导入（不含 Notion/Obsidian 单向导出）
