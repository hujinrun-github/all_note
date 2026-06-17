# 每日总结页面设计（v2 — 已通过审查修正）

**日期**: 2026-06-17  
**分支**: feat/note-project-links  
**审查**: 12 项问题已修正（3 严重 / 3 高 / 3 中 / 3 低）

---

## 需求摘要

- 侧边栏新增「每日总结」入口
- 默认展示本周已完成的全部任务，按日期分组
- 用户通过预设按钮（本周/本月）+ 日期选择器切换时间范围
- 点击任务展开项目信息和关联笔记，可跳转
- 顶部展示统计摘要（随日期范围联动）：完成数、活跃天数、参与项目数

---

## 架构总览

```
新增页面路由
  /summary → DailySummary.tsx

前端改动（5 个文件）
  ├── router.tsx            — 新增 lazy route（复用 App.tsx 已有的 Suspense 边界）
  ├── Sidebar.tsx           — 新增导航项 + SummaryIcon SVG
  ├── TopBar.tsx            — 新增 pageMeta['/summary']
  ├── DailySummary.tsx      — 核心页面组件（新建）
  └── index.css             — 新增 .summary-grid 等样式

后端改动（10 个文件）
  ├── model/task.go         — 新增 CompletedAt *int64 字段
  ├── handler/tasks.go      — UpdateTask 在 done 切换时设/清 completed_at，响应中透传
  ├── handler/summary.go    — 新建，GET /api/summary（含日期→Unix 转换）
  ├── service/summary.go    — 新建，两步查询 + 分组 + 去重逻辑
  ├── repository/tasks.go   — 新增 GetCompletedTasksByRange 方法
  ├── repository/db.go      — SQLite 迁移：ALTER TABLE + 回填
  ├── storage/sqlite/tasks.go   — SQLite 层实现
  ├── storage/postgres/tasks.go — PostgreSQL 层实现
  ├── storage/postgres/migrations.go — 注册 migration .sql 文件
  └── router/router.go      — 注册 /api/summary 路由

新增文件
  ├── db/migrations/postgres/0002_add_completed_at.sql  — PostgreSQL 迁移+回填

测试（仅 Playwright e2e，遵循 CLAUDE.md 要求）
  └── e2e/daily-summary.spec.ts
```

---

## 后端设计

### 数据库迁移

**PostgreSQL** — 新建 `db/migrations/postgres/0002_add_completed_at.sql`：

```sql
ALTER TABLE tasks ADD COLUMN completed_at TIMESTAMPTZ;
-- 回填：将已有 done=1 的任务的 completed_at 设为 updated_at
UPDATE tasks SET completed_at = updated_at WHERE done = true AND completed_at IS NULL;
```

并在 `backend/internal/storage/postgres/migrations.go` 中注册该文件。

**SQLite** — 在 `backend/internal/repository/db.go` 的 `RunLegacySQLiteMigrations` 函数中追加：

```go
// 0002: completed_at for daily summary
_, err = db.Exec(`ALTER TABLE tasks ADD COLUMN completed_at INTEGER`)
if err != nil { return fmt.Errorf("add completed_at column: %w", err) }
_, err = db.Exec(`UPDATE tasks SET completed_at = updated_at WHERE done = 1 AND completed_at IS NULL`)
if err != nil { return fmt.Errorf("backfill completed_at: %w", err) }
```

### Task Model 变更 (`backend/internal/model/task.go`)

```go
type Task struct {
    // ... 现有字段不变 ...
    CompletedAt *int64 `json:"completed_at,omitempty"` // 新增
}

type UpdateTaskRequest struct {
    // ... 现有字段不变 ...
    // Done 字段已存在，无需新增字段 — handler 层根据 Done 的变更自动处理 CompletedAt
}
```

### UpdateTask 逻辑变更 (`backend/internal/handler/tasks.go`)

处理 `done` 字段变更时：

```go
if body.Done != nil {
    if *body.Done == 1 {
        now := time.Now().Unix()
        updates["completed_at"] = &now
    } else {
        // 重新打开：清零完成时间
        var zero int64 = 0
        updates["completed_at"] = &zero // repository 层识别 0 值写入 NULL
    }
}
```

**响应透传**：UpdateTask 的返回 JSON 需包含新的 `completed_at` 字段。

### `GET /api/summary`

```
GET /api/summary?from=2026-06-10&to=2026-06-17&page=1&page_size=50
```

#### 日期→时间戳转换（Handler 层）

使用 **UTC** 时区，消除本地时区歧义：

```go
fromTime, _ := time.ParseInLocation("2006-01-02", from, time.UTC)
toTime, _ := time.ParseInLocation("2006-01-02", to, time.UTC)
toTime = toTime.Add(24 * time.Hour) // [from, to+1day) 左闭右开
```

#### 避免笛卡尔积：两步查询

**第一步**：查询已完成任务 + 项目信息（不 JOIN 笔记）

```sql
SELECT t.*, p.name AS project_name, p.type AS project_type
FROM tasks t
LEFT JOIN task_projects p ON t.project_id = p.id
WHERE t.completed_at >= ? AND t.completed_at < ?
ORDER BY t.completed_at DESC
LIMIT ? OFFSET ?
```

**第二步**：收集所有 `project_id`，批量查询关联笔记

```go
projectIDs := uniqueProjectIDs(tasks)
notes, _ := repo.GetNotesByProjectIDs(projectIDs) // 返回 map[projectID][]Note
```

Service 层负责将笔记挂到对应项目的任务下。

#### 字段语义说明

| 字段 | 含义 |
|------|------|
| `note_id` | 任务直接关联的笔记（任务从该笔记创建），单个 |
| `linked_notes` | 通过 `note_project_links` 关联到任务所在项目的笔记，数组 |

API 响应同时返回两者，前端分别展示。

#### 分页参数

| 参数 | 默认值 | 上限 |
|------|--------|------|
| `page` | 1 | — |
| `page_size` | 50 | 100 |

#### 返回 JSON

```json
{
  "groups": [
    {
      "date": "2026-06-17",
      "tasks": [
        {
          "id": "xxx",
          "title": "完成 PR 审查",
          "project": { "id": "p1", "name": "FlowSpace", "type": "regular" },
          "note_id": "n0",
          "linked_notes": [{ "id": "n1", "title": "review 笔记" }],
          "completed_at": 1718572800,
          "planned_date": "2026-06-16",
          "done": 1
        }
      ],
      "count": 3
    }
  ],
  "total": 42,
  "page": 1,
  "page_size": 50,
  "range": { "from": "2026-06-10", "to": "2026-06-17" }
}
```

---

## 前端设计

### 布局

桌面端两栏（`1fr 2fr`），移动端堆叠：

```
┌──────────────────────────────────────────────────────────┐
│  日期栏:  [本周] [本月]  │  从 [date] 到 [date]          │
├──────────────────────────────────────────────────────────┤
│  统计卡片 (1/3)         │  已完成任务列表 (2/3)           │
│                          │  按 completed_at 日期分组       │
│  ┌──────────────────┐   │                               │
│  │ 12 项已完成       │   │  📅 6/17 · 3项                │
│  │ 5 天有产出        │   │  ├─ ✓ 任务标题 [项目badge]    │
│  │ 3 个项目参与      │   │  │   📄 来源笔记: xxx → 跳转  │
│  └──────────────────┘   │  │   📎 项目笔记: yyy → 跳转   │
│                          │  └─ ...                       │
│                          │  📅 6/16 · 2项                │
│                          │  └─ ...                       │
│                          │                               │
│                          │  ← 上一页  第1/3页  下一页 →  │
└──────────────────────────────────────────────────────────┘
```

### 日期选择器 UX

- **预设按钮**：`本周`（默认选中）、`本月` — 点击后自动填充下方日期输入框
- **日期输入**：两个 `<input type="date">` — `起始日期` 和 `结束日期`
- 预设按钮和手动输入双向联动：手动修改日期后，预设按钮取消选中状态
- 变更时触发 `useQuery` 重新请求 `/api/summary`（重置到第 1 页）

### 统计卡片（随日期范围联动）

| 卡片 | 逻辑 |
|------|------|
| 已完成数 | `total`（当前分页范围的总数） |
| 活跃天数 | `groups.length`（有完成记录的天数） |
| 项目参与数 | `unique(project_id)` 计数 |

三个统计值均反映当前日期选择器的 `from`–`to` 范围，切换日期后同步更新。

### 交互

| 交互 | 行为 |
|------|------|
| 预设按钮 | 点击「本周/本月」快速设置日期范围 |
| 日期输入 | 手动选择起止日期，预设按钮取消选中 |
| 任务卡片 | 点击展开/折叠，显示 `note_id` 来源笔记和 `linked_notes` 项目笔记 |
| 来源笔记链接 | 跳转到 `/editor/:note_id` |
| 项目笔记链接 | 跳转到 `/editor/:note_id` |
| 项目 badge | 跳转到任务页面（锚定到对应项目） |
| 分页 | 「上一页/下一页」按钮，切换时保持日期范围不变 |
| 统计卡片 | 纯展示，无交互 |

### 三态处理

| 状态 | 展示 |
|------|------|
| Loading | 骨架卡片 |
| Error | "加载失败" + 重试按钮 |
| Empty | "这个时间段还没有完成的任务" + 提示调整日期范围 |
| Data | 统计卡片 + 分组任务列表 + 分页 |

### 懒加载

复用 `App.tsx` 中已有的 `<Suspense fallback={...}>` —— `router.tsx` 仅需添加 `lazy(() => import('./routes/DailySummary'))`。

---

## 文件清单（修正后）

### 新建文件

| 文件 | 说明 |
|------|------|
| `frontend/src/routes/DailySummary.tsx` | 核心页面组件 |
| `backend/internal/handler/summary.go` | `GET /api/summary` handler |
| `backend/internal/service/summary.go` | 两步查询 + 分组去重逻辑 |
| `db/migrations/postgres/0002_add_completed_at.sql` | PostgreSQL 迁移 + 回填 |
| `e2e/daily-summary.spec.ts` | Playwright e2e 测试（唯一前端测试） |

### 修改文件

| 文件 | 改动 |
|------|------|
| `frontend/src/router.tsx` | lazy import + `/summary` route |
| `frontend/src/components/layout/Sidebar.tsx` | 新增 navItem + SummaryIcon SVG |
| `frontend/src/components/layout/TopBar.tsx` | 新增 `'/summary'` pageMeta |
| `frontend/src/styles/index.css` | `.summary-grid`, `.summary-stat-card`, `.summary-task-group` |
| `backend/internal/model/task.go` | `CompletedAt *int64` 字段 + JSON tag |
| `backend/internal/handler/tasks.go` | UpdateTask 中处理 completed_at 设/清/透传 |
| `backend/internal/router/router.go` | 注册 `/api/summary` |
| `backend/internal/repository/tasks.go` | 新增 `GetCompletedTasksByRange` 方法 |
| `backend/internal/storage/sqlite/tasks.go` | SQLite 层实现 |
| `backend/internal/storage/postgres/tasks.go` | PostgreSQL 层实现 |
| `backend/internal/repository/db.go` | SQLite ALTER TABLE + 回填迁移 |
| `backend/internal/storage/postgres/migrations.go` | 注册 0002 migration |

---

## 样式设计要点

- 复用现有设计系统变量：`--color-fs-surface`, `--color-fs-accent`, `--shadow-card`, `--radius-lg`
- 统计卡片使用 `.metric-tile` 模式，数字用等宽字体
- 日期栏 flex 布局：左侧预设按钮组（segmented-tabs 风格），右侧双日期输入
- 任务列表按日期分组，每组有日期分隔线
- 已完成任务左侧绿色 checkmark（`--color-fs-success`）
- 项目 badge 复用 `.task-project-tag`
- 来源笔记链接用 accent 色文字 + hover 下划线，标注 `📄 来源笔记`
- 项目笔记链接标注 `📎 项目笔记`

---

## 验证计划

1. **后端单元测试**：`go test ./internal/handler/... ./internal/service/...` 确认 summary 查询和分组正确
2. **E2E（仅 Playwright，遵循 CLAUDE.md）**：`npx playwright test daily-summary.spec.ts`
   - 导航到 `/summary`，验证默认本周数据显示
   - 验证 completed_at 回填后，迁移前的已完成任务不丢失
   - 点击「本月」预设按钮，验证日期范围更新
   - 点击任务展开笔记列表，区分来源笔记和项目笔记
   - 点击笔记链接跳转到编辑器
   - 点击项目 badge 跳转到任务页面
   - 分页按钮：验证翻页不改变日期范围
