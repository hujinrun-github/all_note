# 桌面端与 Web 端架构设计与技术选型

> 项目代号：TBD  
> 文档版本：v1.1  
> 最后更新：2026-05-19

---

## 目录

1. [整体架构](#一整体架构)
2. [进程模型 & IPC](#二进程模型--ipc)
3. [Rust 后端设计](#三rust-后端设计)
4. [React 前端设计](#四react-前端设计)
5. [Tiptap 编辑器方案](#五tiptap-编辑器方案)
6. [数据模型 & 存储](#六数据模型--存储)
7. [窗口管理 & 多窗口架构](#七窗口管理--多窗口架构)
8. [性能策略](#八性能策略)
9. [Web 应用架构方案](#九web-应用架构方案)
10. [技术栈清单](#十技术栈清单)
11. [关键决策记录](#十一关键决策记录)

---

## 一、整体架构

### 1.1 分层架构

```
┌──────────────────────────────────────────────────┐
│                   UI Layer                        │
│      React 18 + TypeScript + Tailwind CSS        │
│      Tiptap · Zustand · React Router · Radix     │
├──────────────────────────────────────────────────┤
│                  Bridge Layer                      │
│   Tauri IPC (invoke / events / commands)          │
│   serde JSON serialization                       │
├──────────────────────────────────────────────────┤
│               Rust Core Layer                      │
│   ┌────────┬──────────┬─────────┬──────────┐    │
│   │  Note  │   Task   │Calendar │  Search  │    │
│   │ Engine │  Engine  │ Engine  │  Engine  │    │
│   ├────────┴──────────┴─────────┴──────────┤    │
│   │          Link Engine (关联引擎)          │    │
│   ├────────────────────────────────────────┤    │
│   │       Storage Layer (SQLite + FS)       │    │
│   └────────────────────────────────────────┘    │
├──────────────────────────────────────────────────┤
│                  Platform Layer                    │
│    macOS (Cocoa) / Windows (Win32) / iOS (UIKit)  │
│    Tauri Runtime + WKWebView / WebView2           │
└──────────────────────────────────────────────────┘
```

### 1.2 设计原则

| 原则 | 含义 | 实现方式 |
|------|------|---------|
| **Rust 做重活** | 所有计算、IO、搜索放 Rust 侧 | Tauri commands 暴露 API |
| **React 做展示** | 前端只管渲染和用户交互 | 通过 invoke() 调用 Rust |
| **数据在 Rust** | 前端不直接操作文件系统或数据库 | 所有数据读写经 IPC |
| **Markdown 是真实源** | SQLite 是索引，.md 文件是真相 | 导出 = 原样复制 .md 文件 |
| **懒加载优先** | 大列表/搜索按需加载 | 虚拟滚动 + 分页查询 |

### 1.3 数据流方向

```
用户操作 → React event → Zustand action
  → invoke("command_name", payload)  ← Tauri IPC
    → Rust command handler
      → SQLite 写入 / 文件写入
      → 返回结果
    ← serde JSON
  ← invoke() 返回
→ Zustand store 更新 → React re-render
```

---

## 二、进程模型 & IPC

### 2.1 进程架构

```
┌──────────────────────────────────────┐
│         Main Process (Rust)          │
│  ┌────────────────────────────────┐  │
│  │  Tauri Core                     │  │
│  │  - Window management            │  │
│  │  - Menu / Tray / Shortcuts      │  │
│  │  - File system access           │  │
│  │  - SQLite connection pool       │  │
│  │  - Command handlers             │  │
│  │  - Search indexer               │  │
│  └────────────────────────────────┘  │
│              ↕ IPC                    │
│  ┌────────────────────────────────┐  │
│  │  WebView (per window)           │  │
│  │  - React app bundle             │  │
│  │  - Tiptap editor instance       │  │
│  │  - Zustand stores               │  │
│  └────────────────────────────────┘  │
└──────────────────────────────────────┘
```

### 2.2 IPC 协议设计

四种交互模式：

| 模式 | 方向 | 适用场景 | 示例 |
|------|------|---------|------|
| **invoke** | Renderer → Main | 请求-响应 | 保存笔记、查询任务列表 |
| **event emit** | Renderer → Main | 单向通知 | 窗口焦点变化、快捷键按下 |
| **event listen** | Main → Renderer | 推送通知 | 文件变更通知、同步状态变化 |
| **state sync** | Main ↔ Renderer | 状态同步 | 应用设置、主题切换 |

```rust
// 示例：笔记保存的 IPC 契约
// Rust 侧 command 定义
#[tauri::command]
async fn note_save(id: String, content: String, title: String) -> Result<Note, Error> {
    // 写入 .md 文件 + 更新 SQLite 索引
}
```

```typescript
// React 侧调用
const note = await invoke<Note>("note_save", {
  id: "note-123",
  content: "# Hello World",
  title: "My Note"
});
```

### 2.3 安全模型

| 规则 | 配置 |
|------|------|
| **CSP** | 禁止内联脚本，只允许加载打包资源 |
| **IPC scope** | 白名单模式，只暴露明确定义的 commands |
| **FS access** | 通过 Tauri FS plugin，限制在 App 数据目录 |
| **网络** | v1 不发网络请求（本地优先） |

---

## 三、Rust 后端设计

### 3.1 模块结构

```
src-tauri/src/
├── main.rs              # 入口，Tauri Builder
├── commands/            # Tauri commands（IPC 接口）
│   ├── mod.rs
│   ├── note.rs          # note_create, note_get, note_update, note_delete, note_list, note_search
│   ├── task.rs          # task_create, task_update, task_complete, task_list_today, task_list_project
│   ├── calendar.rs      # event_create, event_list_day, event_list_week, event_move
│   ├── quick_capture.rs # capture_create (智能识别 + 自动创建)
│   ├── link.rs          # link_suggest, link_create, link_list
│   ├── learning.rs      # learning_map_create, learning_node_update, learning_today
│   └── dashboard.rs     # dashboard_today (聚合查询)
├── engine/              # 业务引擎
│   ├── mod.rs
│   ├── note_engine.rs   # 笔记 CRUD + Markdown 解析
│   ├── task_engine.rs   # 任务状态机 + 循环任务
│   ├── calendar_engine.rs # 日历计算 + 重复事件
│   ├── link_engine.rs   # 关联建议 + 双向链接
│   ├── learning_engine.rs # 知识地图、前置依赖、掌握度计算
│   ├── capture_engine.rs # 文本智能解析（NLP-lite）
│   └── search_engine.rs # Tantivy 索引管理 + 查询
├── storage/             # 存储层
│   ├── mod.rs
│   ├── db.rs            # SQLite 连接池 + 迁移
│   ├── files.rs         # Markdown 文件读写
│   ├── models.rs        # 数据模型 (serde)
│   └── migrations/      # SQL 迁移脚本
├── parsing/             # 解析器
│   ├── mod.rs
│   ├── markdown.rs      # Markdown 解析（提取标题、链接、标签）
│   └── natural.rs       # 自然语言解析（"明天下午3点开会"）
├── sync/                # 同步（v2）
│   └── mod.rs
└── utils/               # 工具函数
    ├── mod.rs
    ├── id.rs            # ID 生成 (ULID)
    └── time.rs          # 时间处理
```

### 3.2 Command 清单

#### 笔记

```
note_create(title: str, content: str, folder_id?: str) → Note
note_get(id: str) → Note
note_update(id: str, content: str, title: str) → Note
note_delete(id: str) → void
note_list(folder_id?: str, limit: u32, offset: u32) → Vec<Note>
note_search(query: str, limit: u32) → Vec<Note>
note_get_linked_tasks(id: str) → Vec<Task>
note_get_linked_events(id: str) → Vec<Event>
```

#### 任务

```
task_create(title: str, due_date?: str, priority: u8, project?: str) → Task
task_update(id: str, ...) → Task
task_complete(id: str) → Task
task_list_today() → Vec<Task>
task_list_inbox() → Vec<Task>
task_list_project(project: str) → Vec<Task>
task_get_linked_notes(id: str) → Vec<Note>
```

#### 日历

```
event_create(title: str, start: str, end: str, all_day: bool) → Event
event_update(id: str, ...) → Event
event_delete(id: str) → void
event_list_day(date: str) → Vec<Event>
event_list_week(start_date: str) → Vec<Event>
event_list_month(year: u32, month: u32) → Vec<Event>
event_move(id: str, new_start: str, new_end: str) → Event
event_get_linked_notes(id: str) → Vec<Note>
event_get_linked_tasks(id: str) → Vec<Task>
```

#### 快速捕获

```
capture_parse(text: str) → CaptureResult  # 解析文本，返回建议的创建类型
capture_create(text: str) → CaptureOutcome  # 智能创建笔记/任务/事件
```

#### 关联

```
link_suggest(source_id: str, source_type: str) → Vec<LinkSuggestion>
link_create(source_id: str, source_type: str, target_id: str, target_type: str) → Link
link_remove(id: str) → void
```

#### 学习地图

```
learning_map_create(title: str, description?: str) → LearningMap
learning_map_get(id: str) → LearningMapDetail
learning_map_list() → Vec<LearningMapSummary>
learning_node_create(map_id: str, title: str, parent_id?: str) → LearningNode
learning_node_update(id: str, ...) → LearningNode
learning_node_move(id: str, parent_id?: str, sort_order: i32) → LearningNode
learning_node_link_entity(node_id: str, entity_id: str, entity_type: str) → Link
learning_node_add_prerequisite(node_id: str, prerequisite_id: str) → void
learning_today() → Vec<LearningNodeRecommendation>
learning_update_mastery(node_id: str, mastery: u8, reason: str) → LearningNode
```

#### 仪表盘

```
dashboard_today() → DashboardData {
  events: Vec<Event>,
  tasks: Vec<Task>,
  recent_notes: Vec<Note>,
  learning_nodes: Vec<LearningNodeRecommendation>,
  streak: u32
}
```

### 3.3 快速捕获：智能解析引擎

这是产品最关键的差异化能力，需要重点设计。

```
输入文本 → 分词 → 模式匹配 → 输出建议

示例输入: "明天下午3点 #项目A 开会讨论登录方案"

解析结果:
{
  "suggested_type": "event",      // 检测到时间 → 建议创建日历事件
  "title": "开会讨论登录方案",
  "start_time": "2026-05-15T15:00:00",
  "tags": ["项目A"],
  "also_create_note": false       // 是否同时创建关联笔记
}
```

解析优先级（从高到低）：
1. **有时间 + 有动作** → 日历事件
2. **有截止日期 + 动作词** → 任务
3. **有 # 标签** → 归类到对应项目
4. **其他** → 笔记、收件箱项

解析器是一个轻量级 Rust 模块（不依赖 LLM），基于规则 + 正则表达式，响应 < 1ms。

### 3.4 关联引擎

```
LinkEngine 启动时:
  1. 从 SQLite 加载所有实体 (notes, tasks, events)
  2. 构建倒排索引: 标签 → [实体ID]
  3. 计算内容相似度 (TF-IDF, 基于 Tantivy)

当用户打开一个笔记:
  1. 提取该笔记的标签 + 关键词
  2. 查询同标签的 tasks/events
  3. 查询内容相似的其他 notes
  4. 返回 TOP 5 建议

性能目标: < 50ms 返回建议
```

### 3.5 学习地图引擎

LearningEngine 负责把学习路线变成可执行的知识点网络。

```
创建学习地图:
  1. 创建 learning_map
  2. 创建第一层知识点节点
  3. 为每个节点生成默认学习任务（可选）

打开学习地图:
  1. 加载 learning_nodes 构建 Tree
  2. 加载 learning_prerequisites 构建 Map 边
  3. 通过 links 加载关联笔记/任务/事件
  4. 根据 status/mastery/review_at 计算今日推荐节点

推荐下一步:
  1. 优先选择前置节点已掌握的 not_started 节点
  2. 其次选择 review_at 到期的待复习节点
  3. 如果当前节点卡住，提示未掌握的 prerequisite
  4. 返回 1-3 个建议，避免给用户过多压力
```

节点状态流转：

```
not_started → learning → review_due → mastered
                    │          ↑
                    └── blocked┘
```

掌握度计算 v1 先用规则：

| 行为 | 掌握度变化 |
|------|------------|
| 完成关联学习任务 | +20 |
| 写入/更新关联笔记 | +10 |
| 完成练习任务 | +30 |
| 复习卡片答对 | +10 |
| 手动标记已掌握 | 设置为 100 |

---

## 四、React 前端设计

### 4.1 路由设计

```
/                      → Dashboard (今日仪表盘)
/notes                 → NoteList (笔记列表)
/notes/:id             → NoteDetail (笔记详情/编辑)
/tasks                 → TaskList (任务列表)
/tasks/today           → TaskToday (今日任务)
/tasks/project/:name   → TaskProject (项目任务)
/calendar              → Calendar (日历视图)
/calendar/day/:date    → CalendarDay
/calendar/week/:date   → CalendarWeek
/learning              → LearningMapList
/learning/:id          → LearningMapDetail (Tree/Map)
/learning/:id/node/:nodeId → LearningNodeDetail
/search                → SearchResults
/settings              → Settings
```

### 4.2 组件树

```
App
├── GlobalShortcutListener          # 监听 Cmd+Shift+N
├── QuickCaptureWindow               # 快速捕获弹窗（独立窗口）
│   ├── CaptureInput                 # 输入框 + 智能识别反馈
│   └── CaptureSuggestions           # 解析后的创建建议
├── MainLayout
│   ├── Sidebar                      # 左侧导航
│   │   ├── NavItem (Dashboard)
│   │   ├── NavItem (Notes)
│   │   ├── NavItem (Tasks)
│   │   ├── NavItem (Calendar)
│   │   ├── NavItem (Learning)
│   │   ├── NavItem (Search)
│   │   ├── FolderTree
│   │   └── ProjectList
│   ├── ContentArea
│   │   ├── Dashboard
│   │   │   ├── CalendarMini         # 小日历
│   │   │   ├── TaskListCompact      # 今日任务紧凑视图
│   │   │   └── RecentNotesList      # 最近笔记
│   │   ├── NoteEditor
│   │   │   ├── EditorToolbar
│   │   │   ├── TiptapEditor         # 核心编辑器
│   │   │   ├── LinkedItemsPanel     # 关联的任务/事件/笔记
│   │   │   └── BacklinksPanel       # 反向链接
│   │   ├── TaskBoard
│   │   │   ├── TaskList
│   │   │   │   └── TaskItem
│   │   │   └── TaskDetailSheet
│   │   ├── CalendarView
│   │   │   ├── CalendarHeader
│   │   │   ├── CalendarGrid (Day/Week/Month)
│   │   │   └── EventCard
│   │   ├── LearningMapView
│   │   │   ├── LearningTree         # 按层级学习路线
│   │   │   ├── KnowledgeGraph       # 节点依赖/关联图谱
│   │   │   ├── LearningNodeDetail   # 节点详情、前置、掌握度
│   │   │   └── TodayLearningPanel   # 今日推荐节点
│   │   └── SearchView
│   │       ├── SearchInput
│   │       └── SearchResultList
│   └── StatusBar                     # 底部状态栏（字数、同步状态）
└── SettingsWindow                    # 设置页（独立窗口）
```

### 4.3 状态管理 (Zustand)

```typescript
// stores 设计

// noteStore — 笔记状态
interface NoteStore {
  notes: Note[];
  currentNote: Note | null;
  isLoading: boolean;
  fetchNotes: (folderId?: string) => Promise<void>;
  saveNote: (id: string, content: string) => Promise<void>;
  createNote: () => Promise<string>;  // 返回新笔记 ID
  deleteNote: (id: string) => Promise<void>;
}

// taskStore — 任务状态
interface TaskStore {
  todayTasks: Task[];
  inboxTasks: Task[];
  projectTasks: Record<string, Task[]>;
  completeTask: (id: string) => Promise<void>;
  createTask: (task: CreateTaskInput) => Promise<void>;
}

// calendarStore — 日历状态
interface CalendarStore {
  events: Event[];
  currentDate: Date;
  viewMode: 'day' | 'week' | 'month';
  fetchEvents: (date: Date, mode: ViewMode) => Promise<void>;
  moveEvent: (id: string, start: string, end: string) => Promise<void>;
}

// dashboardStore — 仪表盘聚合（组合上面三个 store 的数据）
interface DashboardStore {
  todayData: DashboardData | null;
  refresh: () => Promise<void>;
}

// learningStore — 学习地图状态
interface LearningStore {
  maps: LearningMapSummary[];
  currentMap: LearningMapDetail | null;
  selectedNode: LearningNode | null;
  todayNodes: LearningNodeRecommendation[];
  viewMode: 'tree' | 'map';
  fetchMaps: () => Promise<void>;
  fetchMap: (id: string) => Promise<void>;
  createNode: (mapId: string, title: string, parentId?: string) => Promise<void>;
  updateNodeStatus: (id: string, status: LearningNodeStatus) => Promise<void>;
  linkNodeEntity: (nodeId: string, entityId: string, entityType: EntityType) => Promise<void>;
  refreshTodayNodes: () => Promise<void>;
}

// captureStore — 快速捕获
interface CaptureStore {
  isOpen: boolean;
  inputText: string;
  parseResult: CaptureResult | null;
  open: () => void;
  close: () => void;
  submit: () => Promise<void>;
}

// uiStore — UI 状态
interface UIStore {
  theme: 'light' | 'dark' | 'system';
  sidebarCollapsed: boolean;
  fontSize: number;
  toggleTheme: () => void;
}
```

### 4.4 快捷键设计

| 快捷键 | 功能 |
|--------|------|
| `Cmd/Ctrl + Shift + N` | 打开快速捕获 |
| `Cmd/Ctrl + K` | 全局搜索 / 命令面板 |
| `Cmd/Ctrl + Shift + [` | 切换侧边栏 |
| `Cmd/Ctrl + 1/2/3/4` | 切换到 仪表盘/笔记/任务/日历 |
| `Cmd/Ctrl + S` | 保存当前笔记 |
| `Cmd/Ctrl + Enter` | 在笔记中快速插入任务项 |
| `Cmd/Ctrl + D` | 在笔记中快速插入日期 |

---

## 五、Tiptap 编辑器方案

### 5.1 为什么是 Tiptap (ProseMirror)

| 对比项 | Tiptap | Slate.js | Lexical | Monaco |
|--------|--------|----------|---------|--------|
| **Markdown 支持** | ✅ 原生（Markdown shortcuts） | 需插件 | ✅ | 代码编辑器 |
| **自定义节点** | ✅ 一流 | ✅ | ✅ | ❌ |
| **协作编辑** | ✅ CRDT 内置 | ❌ 需自己实现 | ✅ | ❌ |
| **包体积** | ~150KB gzip | ~180KB | ~200KB | ~500KB |
| **成熟度** | ProseMirror（10 年） | 中等 | Facebook 维护 | VS Code 内核 |
| **TypeScript** | ✅ 完整 | ✅ | ✅ | ✅ |
| **移动端** | ✅ | ✅ | ✅ | ❌ |

选 Tiptap 的核心原因：它可以自定义「任务节点」「日历事件节点」「笔记链接节点」——这正是我们需要的一体化编辑体验。

### 5.2 自定义节点扩展

```typescript
// 需要自定义的 Node/Extension

// 1. TaskNode — 在笔记中嵌入任务卡片
TaskNode {
  // 显示为紧凑的 checkbox + 标题卡片
  // 点击跳转到任务详情
  // 勾选可直接完成任务
}

// 2. EventNode — 在笔记中嵌入日历事件
EventNode {
  // 显示时间 + 标题
  // 点击查看日历中的事件
}

// 3. NoteLinkNode — 笔记之间的双向链接
NoteLinkNode {
  // [[笔记标题]] → 自动补全 → 可点击跳转
  // hover 预览笔记摘要
}

// 4. TagNode — 标签
TagNode {
  // #标签名 → 自动变色 → 可点击筛选
}

// 5. DateInline — 自然日期
DateInline {
  // "明天" "下周三" → 自动转换为日期 chip
  // 点击可快速创建日历事件
}
```

### 5.3 编辑器性能策略

| 策略 | 实现 |
|------|------|
| **虚拟滚动** | 超长笔记（> 10K 字）不渲染全部 DOM，只渲染视口内 |
| **懒加载关联面板** | 右边的关联面板在用户 hover/点击时才查询 |
| **Debounce 保存** | 用户停止输入 500ms 后自动保存 |
| **Content hash** | 对比保存前后的内容，避免无变化写入 |

---

## 六、数据模型 & 存储

### 6.1 SQLite Schema

```sql
-- 笔记
CREATE TABLE notes (
    id          TEXT PRIMARY KEY,          -- ULID
    title       TEXT NOT NULL DEFAULT '',
    content     TEXT NOT NULL DEFAULT '',
    folder_id   TEXT,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL,
    is_deleted  INTEGER DEFAULT 0,
    word_count  INTEGER DEFAULT 0,
    file_path   TEXT                       -- 对应的 .md 文件路径
);

CREATE INDEX idx_notes_folder ON notes(folder_id);
CREATE INDEX idx_notes_updated ON notes(updated_at);
CREATE INDEX idx_notes_title ON notes(title);

-- 任务
CREATE TABLE tasks (
    id          TEXT PRIMARY KEY,
    title       TEXT NOT NULL,
    description TEXT DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'todo',  -- 'todo', 'done', 'cancelled'
    priority    INTEGER DEFAULT 0,              -- 0=none, 1=low, 2=medium, 3=high
    due_date    TEXT,                            -- ISO 8601
    project     TEXT DEFAULT 'inbox',
    parent_id   TEXT,                            -- 子任务父 ID
    sort_order  INTEGER DEFAULT 0,
    created_at  TEXT NOT NULL,
    completed_at TEXT,
    updated_at  TEXT NOT NULL,
    is_deleted  INTEGER DEFAULT 0,
    FOREIGN KEY (parent_id) REFERENCES tasks(id)
);

CREATE INDEX idx_tasks_status ON tasks(status);
CREATE INDEX idx_tasks_due ON tasks(due_date);
CREATE INDEX idx_tasks_project ON tasks(project);

-- 日历事件
CREATE TABLE events (
    id          TEXT PRIMARY KEY,
    title       TEXT NOT NULL,
    description TEXT DEFAULT '',
    start_time  TEXT NOT NULL,                   -- ISO 8601
    end_time    TEXT NOT NULL,
    all_day     INTEGER DEFAULT 0,
    location    TEXT DEFAULT '',
    recurrence  TEXT,                             -- RRULE 格式
    color       TEXT DEFAULT '#3B82F6',
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL,
    is_deleted  INTEGER DEFAULT 0
);

CREATE INDEX idx_events_start ON events(start_time);
CREATE INDEX idx_events_range ON events(start_time, end_time);

-- 文件夹
CREATE TABLE folders (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    parent_id   TEXT,
    sort_order  INTEGER DEFAULT 0,
    created_at  TEXT NOT NULL,
    FOREIGN KEY (parent_id) REFERENCES folders(id)
);

-- 关联（核心！）
CREATE TABLE links (
    id           TEXT PRIMARY KEY,
    source_id    TEXT NOT NULL,
    source_type  TEXT NOT NULL,   -- 'note', 'task', 'event', 'learning_node'
    target_id    TEXT NOT NULL,
    target_type  TEXT NOT NULL,
    created_at   TEXT NOT NULL,
    UNIQUE(source_id, source_type, target_id, target_type)
);

CREATE INDEX idx_links_source ON links(source_id, source_type);
CREATE INDEX idx_links_target ON links(target_id, target_type);

-- 学习地图
CREATE TABLE learning_maps (
    id          TEXT PRIMARY KEY,
    title       TEXT NOT NULL,
    description TEXT DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'active', -- 'active', 'archived'
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

CREATE INDEX idx_learning_maps_updated ON learning_maps(updated_at);

-- 知识点节点
CREATE TABLE learning_nodes (
    id            TEXT PRIMARY KEY,
    map_id        TEXT NOT NULL,
    parent_id     TEXT,
    title         TEXT NOT NULL,
    description   TEXT DEFAULT '',
    status        TEXT NOT NULL DEFAULT 'not_started',
    mastery       INTEGER DEFAULT 0,             -- 0-100
    sort_order    INTEGER DEFAULT 0,
    due_date      TEXT,
    review_at     TEXT,
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL,
    is_deleted    INTEGER DEFAULT 0,
    FOREIGN KEY (map_id) REFERENCES learning_maps(id),
    FOREIGN KEY (parent_id) REFERENCES learning_nodes(id)
);

CREATE INDEX idx_learning_nodes_map ON learning_nodes(map_id);
CREATE INDEX idx_learning_nodes_parent ON learning_nodes(parent_id);
CREATE INDEX idx_learning_nodes_status ON learning_nodes(status);
CREATE INDEX idx_learning_nodes_review ON learning_nodes(review_at);

-- 知识点前置依赖（Map 视图边）
CREATE TABLE learning_prerequisites (
    node_id          TEXT NOT NULL,
    prerequisite_id  TEXT NOT NULL,
    created_at       TEXT NOT NULL,
    PRIMARY KEY (node_id, prerequisite_id),
    FOREIGN KEY (node_id) REFERENCES learning_nodes(id),
    FOREIGN KEY (prerequisite_id) REFERENCES learning_nodes(id)
);

-- 标签
CREATE TABLE tags (
    id      TEXT PRIMARY KEY,
    name    TEXT NOT NULL UNIQUE
);

CREATE TABLE entity_tags (
    entity_id    TEXT NOT NULL,
    entity_type  TEXT NOT NULL,
    tag_id       TEXT NOT NULL,
    PRIMARY KEY (entity_id, entity_type, tag_id)
);

-- 设置
CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
```

### 6.2 文件系统布局

```
~/Documents/AppName/              # 应用数据根目录（可自定义）
├── workspace.db                   # SQLite 数据库
├── search_index/                  # Tantivy 索引文件
├── notes/                         # Markdown 笔记文件
│   ├── inbox/
│   │   └── 01JEXAMPLE0001.md
│   ├── work/
│   │   └── 01JEXAMPLE0002.md
│   └── personal/
│       └── 01JEXAMPLE0003.md
├── attachments/                   # 附件（图片、PDF）
│   └── 2026/
│       └── 05/
│           └── img_xxx.png
└── backups/                       # 自动备份（SQLite dump）
    └── 2026-05-14_auto.db
```

### 6.3 Markdown 文件格式

```markdown
---
id: "01JEXAMPLE0001"
title: "会议笔记 2026-05-14"
created: "2026-05-14T10:30:00+08:00"
updated: "2026-05-14T11:45:00+08:00"
tags: [项目A, 会议]
---

# 会议笔记 2026-05-14

讨论内容...

## 待办事项

- [ ] 整理 API 文档 [[task-xxx]]
- [ ] 同步给前端团队

## 相关

- [[note-yyy]] 上次会议记录
- 📅 [[event-zzz]] 下次评审会议
```

> .md 文件是 source of truth，SQLite 是性能加速的索引层。用户随时可以把 notes/ 文件夹拖进 Obsidian 直接使用。**零锁定**。

---

## 七、窗口管理 & 多窗口架构

### 7.1 窗口清单

| 窗口 | 类型 | 大小 | 说明 |
|------|------|------|------|
| **Main Window** | 标准窗口 | 1200×800 | 主界面，含侧边栏+内容区 |
| **Quick Capture** | 浮动面板 | 500×120 | 全局快捷键触发，类似 Spotlight |
| **Settings** | 独立窗口 | 600×500 | 设置页面 |
| **Note Pop-out** | 独立窗口 | 800×600 | 笔记弹出为独立窗口编辑 |

### 7.2 Quick Capture 窗口设计

这是产品体验的关键入口，需要特别设计：

```
┌────────────────────────────────────────────────┐
│  ⚡ 快速捕获                           ⌘W 关闭  │
├────────────────────────────────────────────────┤
│                                                │
│  ┌──────────────────────────────────────────┐  │
│  │  明天下午3点 #项目A 开会讨论登录方案  │  │  │
│  └──────────────────────────────────────────┘  │
│                                                │
│  解析结果：                                     │
│  ┌─ 类型：[日历事件 ▼] ──────────────────────┐  │
│  │  标题：开会讨论登录方案                     │  │
│  │  时间：2026-05-15 15:00                    │  │
│  │  标签：#项目A                               │  │
│  │                                            │  │
│  │  □ 同时创建关联笔记                         │  │
│  │                                            │  │
│  │              [ 取消 ]   [ ✓ 创建 ]          │  │
│  └────────────────────────────────────────────┘  │
└────────────────────────────────────────────────┘
```

窗口行为：
- **显示：** 按 `Cmd/Ctrl+Shift+N`，从屏幕顶部滑入
- **隐藏：** 按 `Esc` 或点击外部区域
- **动画：** 200ms ease-out 滑入/滑出
- **始终最前：** `always_on_top = true`
- **无边框：** 简洁的浮动面板

### 7.3 Tauri 窗口配置

```json
// tauri.conf.json (窗口相关部分)
{
  "app": {
    "windows": [
      {
        "label": "main",
        "title": "AppName",
        "width": 1200,
        "height": 800,
        "resizable": true,
        "minWidth": 800,
        "minHeight": 600
      },
      {
        "label": "quick-capture",
        "title": "Quick Capture",
        "width": 500,
        "height": 150,
        "resizable": false,
        "decorations": false,
        "alwaysOnTop": true,
        "visible": false,          // 默认隐藏
        "center": true,
        "skipTaskbar": true
      }
    ]
  }
}
```

---

## 八、性能策略

### 8.1 启动性能

```
冷启动流程:
  Tauri Runtime init    →  < 100ms
  SQLite 连接池创建      →  < 50ms
  WebView 加载           →  < 200ms
  React app mount        →  < 200ms
  Dashboard 数据查询     →  < 100ms
  Dashboard 渲染         →  < 100ms
═══════════════════════════════════
  总启动时间              →  < 750ms   目标 < 1s ✅
```

优化手段：
- **Rust 侧预加载** — 应用启动时异步预热 SQLite 连接池 + Tantivy 索引
- **React 代码分割** — 日历、编辑器等大组件 lazy load
- **WebView 预热** — 第一时间加载 shell HTML，React 异步 mount
- **骨架屏** — 在数据到达前显示占位 UI，感知更快

### 8.2 编辑器性能

| 场景 | 策略 |
|------|------|
| **输入延迟** | Tiptap 原生 < 16ms per keystroke，不做额外处理 |
| **大文档渲染** | 分页渲染，只渲染当前段落 + 前后各 2 段 |
| **搜索高亮** | SQLite FTS5 → 返回匹配位置 → 前端批量 highlight |
| **保存抖动** | 500ms debounce + content hash 去重 |

### 8.3 搜索性能

```
搜索链路:
  用户输入 → 300ms debounce
    → Tauri invoke("note_search", { query, limit: 20 })
      → Rust: Tantivy 索引查询 (< 10ms for 10000 docs)
        → 如果需要精准匹配: SQLite FTS5 二次查询 (< 5ms)
    ← 返回 TOP 20 结果
  → 渲染结果列表 (< 50ms)
═══════════════════════════════════
  总搜索延迟                     < 350ms
```

### 8.4 内存管理

| 组件 | 内存预算 | 策略 |
|------|---------|------|
| Tauri Runtime | ~15 MB | Rust 天然低占用 |
| WebView (主窗口) | ~20-30 MB | 单页应用，无 iframe |
| 编辑器 DOM | ~10 MB | 虚拟滚动 |
| Zustand stores | ~5 MB | 限制 store 大小，分页加载 |
| SQLite 缓存 | ~5 MB | WAL mode，定期 checkpoint |
| **总计（空闲）** | **~40 MB** | 目标 < 50 MB ✅ |
| **总计（使用中）** | **~80 MB** | 目标 < 150 MB ✅ |

---

## 九、Web 应用架构方案

Web 端目标不是削弱版，而是提供和桌面端一致的核心能力：今日仪表盘、笔记、任务、日历、快速捕获、智能关联和搜索。差异在于 Web 端更强调跨设备访问、轻量部署和可选同步；桌面端继续承担本地文件系统、全局快捷键、离线体验和原生性能优势。

### 9.1 产品定位

| 形态 | 核心价值 | 适用场景 |
|------|----------|----------|
| 桌面端 | 本地优先、最快启动、系统级快捷键、Markdown 文件真实落盘 | 主力工作设备、深度写作、离线使用 |
| Web 端 | 免安装、跨设备、快速访问、团队/共享入口 | 临时设备、轻量录入、查看今日计划、协作分享 |
| 移动端 | 秒级捕获、通知提醒、随身查看 | 灵感收集、日程提醒、简单勾选任务 |

Web v1 的原则：功能同构、体验近似、能力分层。核心页面和业务模型复用桌面端设计，但不强求 Web 端直接访问用户本地 Markdown 文件。

### 9.2 整体架构

```
┌──────────────────────────────────────────────────┐
│                  Web Client                       │
│  React 18 + TypeScript + Vite + Tailwind + Tiptap │
│  Zustand · React Router · TanStack Query          │
├──────────────────────────────────────────────────┤
│                  API Layer                         │
│  REST / tRPC / GraphQL (v1 推荐 REST + OpenAPI)    │
│  Auth · Rate Limit · Request Validation            │
├──────────────────────────────────────────────────┤
│              Application Service                   │
│  Note Service · Task Service · Calendar Service    │
│  Capture Service · Link Service · Search Service   │
├──────────────────────────────────────────────────┤
│                  Data Layer                         │
│  PostgreSQL · Object Storage · Search Index         │
│  Redis Cache / Queue (v1 可选)                      │
└──────────────────────────────────────────────────┘
```

### 9.3 前端实现方案

Web 前端尽量复用桌面端 React 代码，把平台差异封装在 adapter 层。

```
apps/
├── desktop/              # Tauri 壳 + 桌面入口
├── web/                  # Web 入口，部署到 Vercel/Cloudflare/自托管
packages/
├── ui/                   # 共享 UI 组件
├── editor/               # Tiptap 扩展、节点、Markdown 转换
├── domain/               # Note/Task/Event/Link 类型与业务规则
├── api-client/           # Web API client + 桌面 IPC client 统一接口
└── config/               # eslint/tsconfig/tailwind 共享配置
```

关键接口抽象：

```typescript
interface AppDataClient {
  notes: NoteRepository;
  tasks: TaskRepository;
  events: EventRepository;
  learning: LearningRepository;
  links: LinkRepository;
  search: SearchRepository;
  capture: CaptureRepository;
}

// desktop: 通过 Tauri invoke 调 Rust command
// web: 通过 fetch 调后端 API
```

这样 `Dashboard`、`NoteEditor`、`TaskBoard`、`CalendarView` 不关心运行环境，只依赖 `AppDataClient`。

### 9.4 后端实现方案

Web 后端推荐从 TypeScript 服务开始，降低前后端协作和部署成本；搜索、解析等性能敏感能力后续可复用 Rust 核心。

| 模块 | v1 实现 | 后续演进 |
|------|---------|----------|
| API Server | Node.js + Fastify/NestJS | 拆分 Rust service 或 edge function |
| 数据库 | PostgreSQL | 本地 SQLite 与云端 Postgres 双向同步 |
| ORM | Drizzle / Prisma | 保持 schema 迁移可审计 |
| Auth | Auth.js / Lucia / Clerk | 企业 SSO、Passkey |
| 搜索 | PostgreSQL FTS + trigram | Meilisearch / Tantivy service |
| 文件存储 | Markdown 内容先存在 DB | 导出为 .md，后续对象存储版本历史 |
| 队列 | v1 可不用 | BullMQ / Cloudflare Queues 做索引和同步任务 |

推荐 v1 采用单体后端：一个 API 服务覆盖笔记、任务、日历、快速捕获、搜索和关联建议。等团队协作、同步和全文搜索压力上来后，再拆分服务。

### 9.5 数据存储与同步策略

Web 端没有桌面端的“本地文件就是唯一真相”，因此采用“云端工作区 + 标准 Markdown 导出”的模型。

| 数据 | Web v1 存储 | 说明 |
|------|-------------|------|
| notes | PostgreSQL `notes.content_md` | Markdown 仍是主格式，Tiptap JSON 可作为缓存 |
| tasks | PostgreSQL `tasks` | 支持 due date、priority、status、project |
| events | PostgreSQL `events` | 支持 day/week/month 查询 |
| learning_maps | PostgreSQL `learning_maps` | 学习路线容器 |
| learning_nodes | PostgreSQL `learning_nodes` | 知识点 Tree 节点和掌握状态 |
| learning_prerequisites | PostgreSQL `learning_prerequisites` | Map 视图依赖边 |
| links | PostgreSQL `links` | 记录 note-task-event 三类实体关系 |
| search_index | PostgreSQL FTS | v1 够用，数据增长后独立搜索服务 |
| attachments | Object Storage | v1 可先禁用或限制图片上传 |

同步分三阶段：

| 阶段 | 能力 | 策略 |
|------|------|------|
| v1 | Web 独立使用 | Web 数据在云端，支持 Markdown 导入/导出 |
| v1.5 | 桌面登录云账号 | 桌面端把本地数据增量上传到云工作区 |
| v2 | 多端双向同步 | 每条实体维护 `updated_at`、`deleted_at`、`version`，冲突保留双版本 |

冲突处理原则：

1. 笔记冲突不自动覆盖，生成两个版本让用户合并。
2. 任务和日历事件以字段级 `updated_at` 做最后写入优先。
3. 删除使用软删除，保留 30 天回收站。
4. 所有导出都生成标准 `.md` 和 `.json` 元数据，避免平台锁定。

### 9.6 Web 快速捕获

Web 端无法提供桌面级全局快捷键，但可以提供三个入口：

| 入口 | 实现 | 场景 |
|------|------|------|
| 应用内快捷键 | `Ctrl/Cmd + Shift + N` | 当前 Web App 已打开 |
| PWA Share Target | Web App Manifest `share_target` | 从浏览器/手机分享文本到应用 |
| 浏览器扩展 | Chrome Extension + Web API | 任意网页上一键捕获选中文本 |

Web 快速捕获仍使用同一套 `CaptureService`：

```
输入文本 → natural parser → 候选类型
  → note / task / event / mixed
  → 创建实体 → 自动建立 links
```

### 9.7 安全与权限

| 领域 | 方案 |
|------|------|
| 认证 | Email magic link + OAuth，v1 避免密码系统复杂度 |
| 授权 | `workspace_id + user_id + role`，所有查询强制 workspace scope |
| API 安全 | Zod 校验输入，速率限制，CSRF 防护，安全 cookie |
| 数据隔离 | 每个表包含 `workspace_id`，后续可迁移到 RLS |
| 加密 | HTTPS 传输；v2 支持端到端加密工作区 |
| 备份 | 每日数据库备份，用户可手动导出 Markdown |

### 9.8 离线与 PWA

Web v1 推荐支持“弱离线”：已经打开的页面可继续查看和编辑最近内容，网络恢复后自动提交。

| 能力 | v1 | v2 |
|------|----|----|
| PWA 安装 | 支持 | 支持 |
| 最近笔记缓存 | IndexedDB | IndexedDB + 后台同步 |
| 离线创建任务 | 支持，入本地 outbox | 支持 |
| 离线编辑笔记 | 仅最近打开笔记 | 全工作区可选缓存 |
| 冲突合并 | 简单版本冲突提示 | CRDT / 操作日志 |

本地缓存结构：

```
IndexedDB
├── entities_cache      # 最近 notes/tasks/events
├── pending_mutations   # 离线期间的写操作
└── user_settings       # 主题、侧边栏、最近 workspace
```

### 9.9 API 草案

```
GET    /api/dashboard/today

GET    /api/notes
POST   /api/notes
GET    /api/notes/:id
PATCH  /api/notes/:id
DELETE /api/notes/:id

GET    /api/tasks
POST   /api/tasks
PATCH  /api/tasks/:id
POST   /api/tasks/:id/complete

GET    /api/events?from=&to=
POST   /api/events
PATCH  /api/events/:id
DELETE /api/events/:id

POST   /api/capture/parse
POST   /api/capture/commit

GET    /api/learning/maps
POST   /api/learning/maps
GET    /api/learning/maps/:id
POST   /api/learning/maps/:id/nodes
PATCH  /api/learning/nodes/:id
POST   /api/learning/nodes/:id/prerequisites
POST   /api/learning/nodes/:id/link
GET    /api/learning/today

GET    /api/search?q=
GET    /api/links/suggest?entity_id=&entity_type=
POST   /api/links
```

### 9.10 部署方案

| 方案 | 推荐阶段 | 说明 |
|------|----------|------|
| Vercel + Neon/Supabase | 原型/MVP | 最快上线，前后端开发效率高 |
| Cloudflare Pages + Workers + D1 | 轻量全球访问 | 适合读多写少，成本低 |
| Docker Compose 自托管 | 面向 selfhosted 用户 | Postgres + API + Web，一条命令启动 |
| Kubernetes | v2+ | 团队版和高可用需求出现后再考虑 |

MVP 推荐：`apps/web` 部署到 Vercel，API 使用同仓库 server routes 或独立 `apps/api`，数据库使用 Neon/Supabase Postgres。

### 9.11 与桌面端的关系

| 能力 | 桌面端 | Web 端 | 共享策略 |
|------|--------|--------|----------|
| UI 页面 | React | React | 共享组件和页面 |
| 编辑器 | Tiptap | Tiptap | 共享 editor package |
| 业务模型 | Rust models + TS types | TS domain types | 从 schema 生成类型 |
| 数据访问 | Tauri IPC | HTTP API | `AppDataClient` 统一接口 |
| 存储 | Markdown + SQLite | Postgres + Markdown 字段 | 导入/导出保持兼容 |
| 搜索 | Tantivy | Postgres FTS | 搜索结果类型统一 |
| 快速捕获 | 全局快捷键 | PWA/扩展/应用内快捷键 | 共享解析规则 |

---

## 十、技术栈清单

### 桌面前端

| 包 | 版本 | 用途 | 大小 |
|---|------|------|------|
| react | ^18.3 | UI 框架 | — |
| react-dom | ^18.3 | DOM 渲染 | — |
| react-router-dom | ^7 | 客户端路由 | ~16KB |
| zustand | ^5 | 状态管理 | ~2KB |
| @tiptap/react | ^2 | 编辑器核心 | ~150KB gzip |
| @tiptap/starter-kit | ^2 | 编辑器基础扩展 | — |
| @tiptap/extension-link | ^2 | 链接扩展 | — |
| @tiptap/extension-task-list | ^2 | 任务列表 | — |
| @tiptap/extension-task-item | ^2 | 任务项 | — |
| @tiptap/extension-mention | ^2 | @提及（用作笔记链接） | — |
| @tauri-apps/api | ^2 | Tauri IPC | ~10KB |
| tailwindcss | ^4 | 原子化 CSS | — |
| @radix-ui/react-dialog | ^1 | 弹窗组件 | ~5KB |
| @radix-ui/react-dropdown-menu | ^1 | 下拉菜单 | ~8KB |
| @radix-ui/react-popover | ^1 | 气泡面板 | ~5KB |
| @radix-ui/react-tooltip | ^1 | 工具提示 | ~2KB |
| @dnd-kit/core | ^6 | 学习树拖拽排序 | — |
| @xyflow/react | ^12 | 知识地图节点关系图 | — |
| date-fns | ^3 | 日期处理 | tree-shakeable |
| lucide-react | ^0.400 | 图标库 | tree-shakeable |
| clsx | ^2 | class 拼接 | ~300B |

### Web 前端

| 包 | 版本 | 用途 | 说明 |
|---|------|------|------|
| react | ^18.3 | UI 框架 | 与桌面端共享 |
| react-dom | ^18.3 | DOM 渲染 | 与桌面端共享 |
| vite | ^6 | 构建工具 | Web/desktop 前端统一 |
| typescript | ^5.5 | 类型系统 | 与桌面端共享 |
| react-router-dom | ^7 | 客户端路由 | 与桌面端共享 |
| zustand | ^5 | 本地状态 | 与桌面端共享 |
| @tanstack/react-query | ^5 | Server state / cache | Web API 请求缓存 |
| idb | ^8 | IndexedDB 封装 | PWA 离线缓存 |
| @dnd-kit/core | ^6 | 学习树拖拽排序 | 与桌面端共享 |
| @xyflow/react | ^12 | 知识地图节点关系图 | Tree/Map 视图 |
| @tiptap/react | ^2 | 编辑器核心 | 与桌面端共享 editor package |
| tailwindcss | ^4 | 样式系统 | 与桌面端共享主题 |
| lucide-react | ^0.400 | 图标库 | 与桌面端共享 |

### Web 后端

| 包/服务 | 用途 |
|---------|------|
| Node.js 20+ | Web API 运行时 |
| Fastify / NestJS | API 服务框架 |
| PostgreSQL | 云端主数据库 |
| Drizzle / Prisma | ORM 与 schema migration |
| Zod | API 输入校验 |
| Auth.js / Lucia / Clerk | 用户认证 |
| PostgreSQL FTS + pg_trgm | v1 全文搜索 |
| Redis / BullMQ | 后台任务队列（v1 可选） |
| S3 / R2 / Supabase Storage | 附件对象存储 |
| Docker Compose | 自托管部署 |

### Rust / Tauri 后端

| Crate | 用途 |
|-------|------|
| tauri ^2 | 桌面框架 |
| tauri-plugin-shell ^2 | Shell 操作 |
| tauri-plugin-fs ^2 | 文件系统 |
| tauri-plugin-global-shortcut ^2 | 全局快捷键 |
| tauri-plugin-notification ^2 | 系统通知 |
| rusqlite ^0.32 | SQLite 绑定 |
| tantivy ^0.22 | 全文搜索引擎 |
| serde ^1 + serde_json ^1 | 序列化 |
| chrono ^0.4 | 时间处理 |
| ulid ^1 | ID 生成 |
| tokio ^1 | 异步运行时 |
| regex ^1 | 正则（自然语言解析） |
| uuid ^1 | UUID（备选 ID） |

### 开发工具

| 工具 | 用途 |
|------|------|
| Vite ^6 | 前端构建 |
| TypeScript ^5.5 | 类型检查 |
| ESLint + Prettier | 代码规范 |
| Rust Analyzer | Rust IDE 支持 |
| Tauri CLI ^2 | 开发/构建/打包 |

---

## 十一、关键决策记录

| # | 决策 | 理由 | 日期 |
|---|------|------|------|
| 1 | 用 Tauri 不用 Electron | 性能差 10x，用户感知的「快」是核心卖点 | 2026-05-14 |
| 2 | 用 Tiptap 不用其他编辑器 | 自定义节点能力是关联引擎的 UI 基础 | 2026-05-14 |
| 3 | Rust 做全部后端，React 只做展示 | 避免前后端逻辑重复，保证搜索/解析性能 | 2026-05-14 |
| 4 | .md 文件是 Source of Truth | 数据不锁定，用户随时迁移 | 2026-05-14 |
| 5 | v1 不做云同步 | 本地优先验证核心价值，避免同步 bug 毁口碑 | 2026-05-14 |
| 6 | Quick Capture 独立窗口 | 全局快捷键 + 浮动面板 = 类 Spotlight 体验 | 2026-05-14 |
| 7 | 项目方向选定：All-in-one 轻量效率工具 | 基于 Reddit 调研，笔记+任务+日历一体化 | 2026-05-14 |
| 8 | 用 ULID 不用 UUID v4 | 可排序、URL-safe、时间戳内嵌 | 2026-05-14 |
| 9 | Tantivy 做主搜索，SQLite FTS5 做辅助 | Tantivy 中文分词好，SQLite 做精确回退 | 2026-05-14 |
| 10 | 增加 Web 应用形态 | 免安装跨设备访问，作为桌面端之外的轻量入口和未来协作基础 | 2026-05-19 |
| 11 | Web v1 采用 Postgres 云端工作区 | 浏览器不能可靠管理本地 Markdown 文件，云端工作区更适合跨设备和协作 | 2026-05-19 |
| 12 | 桌面/Web 通过 AppDataClient 共享页面层 | 保持功能一致，同时隔离 Tauri IPC 和 HTTP API 的平台差异 | 2026-05-19 |
| 13 | 学习路线采用 Tree + Map 双视图 | Tree 负责按部就班推进，Map 负责解释知识依赖和关联 | 2026-05-20 |

---

## 附录 A：技术预研待验证项

| # | 验证项 | 方法 |
|---|--------|------|
| 1 | Tauri v2 冷启动实际耗时 | 创建空项目，打 release build 测 10 次取中位数 |
| 2 | Tiptap 自定义节点能否渲染 React 组件 | 写一个 `TaskNode` 渲染实验 |
| 3 | Tantivy 中文分词效果 | 索引 1000 篇中文 Markdown，测搜索准确率 |
| 4 | WKWebView vs WebView2 渲染差异 | macOS 和 Windows 各测一次完全相同的 UI |
| 5 | Quick Capture 窗口动画流畅性 | 验证 Tauri 窗口 API 的动画帧率 |

## 附录 B：.gitignore

```gitignore
# Dependencies
node_modules/
target/

# Build output
dist/
src-tauri/target/

# IDE
.vscode/
.idea/
*.swp
*.swo

# OS
.DS_Store
Thumbs.db

# Env
.env
.env.local

# Data (开发时的本地数据)
data/
*.db
search_index/
```
