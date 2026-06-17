# 每日总结页面设计（v3 — 第二轮审查修正）

**日期**: 2026-06-17  
**分支**: feat/note-project-links  
**审查轮次**: 第 1 轮 12 项 / 第 2 轮 12 项 — 均已修正

---

## 需求摘要

- 侧边栏新增「每日总结」入口
- 默认展示本周已完成的全部任务，按日期分组
- 用户通过预设按钮（本周/本月）+ 日期选择器切换时间范围
- 点击任务展开项目信息和关联笔记，可跳转
- 顶部展示统计摘要（由后端返回，不受分页影响）：完成数、活跃天数、参与项目数

---

## 架构总览

```
新增页面路由
  /summary → DailySummary.tsx

前端改动（6 个文件）
  ├── router.tsx            — 新增 lazy route
  ├── Sidebar.tsx           — 新增 navItem + SummaryIcon（不需要 end 属性）
  ├── TopBar.tsx            — 新增 pageMeta['/summary']
  ├── hooks/useSummary.ts   — TanStack Query hook（查询键: ['summary', from, to, page]）
  ├── DailySummary.tsx      — 核心页面组件（新建）
  └── index.css             — 新增 .summary-* 样式

后端改动（13 个文件）
  ├── model/task.go         — CompletedAt *int64（JSON omitempty）
  ├── handler/summary.go    — GET /api/summary（日期解析 + 400 错误处理）
  ├── handler/tasks.go      — UpdateTask 响应中透传 completed_at
  ├── service/summary.go    — 两步查询 + 分组 + 聚合统计
  ├── service/tasks.go      — 查询当前任务状态
  ├── storage/store.go      — TaskRepository interface 新增 GetCompletedTasksByRange
  ├── storage/sqlite/tasks.go   — (1) SELECT/scan 加列 (2) Update 中 completed_at 设/清 (3) 新查询实现
  ├── storage/postgres/tasks.go — (1) SELECT/scan 加列 (2) Update 中 completed_at 设/清 (3) 新查询实现
  ├── storage/sqlite/notes.go   — 新查询: GetNotesByProjectIDs
  ├── storage/postgres/notes.go — 新查询: GetNotesByProjectIDs
  ├── repository/tasks.go   — (1) 全部内联 SELECT 加列 (2) GetCompletedTasksByRange (store + fallback)
  ├── repository/notes.go   — GetNotesByProjectIDs 封装
  ├── repository/db.go      — SQLite ALTER TABLE + 回填迁移
  └── router/router.go      — 注册 /api/summary

新增文件
  ├── db/migrations/postgres/0002_add_completed_at.sql

测试（仅 Playwright e2e，遵循 CLAUDE.md）
  └── e2e/daily-summary.spec.ts
```

---

## 后端设计

### 1. 数据库迁移

**PostgreSQL** — `db/migrations/postgres/0002_add_completed_at.sql`：

```sql
ALTER TABLE tasks ADD COLUMN completed_at TIMESTAMPTZ;
UPDATE tasks SET completed_at = updated_at WHERE done = true AND completed_at IS NULL;
```

> 注：`loadPostgresMigrationsFromDir` 自动按文件名排序扫描 `.sql` 文件，无需手动注册。

**SQLite** — 在 `repository/db.go` 的 `RunLegacySQLiteMigrations` 中追加：

```go
// 0002: completed_at for daily summary
if _, err := db.Exec(`ALTER TABLE tasks ADD COLUMN completed_at INTEGER`); err != nil {
    return fmt.Errorf("add completed_at column: %w", err)
}
if _, err := db.Exec(`UPDATE tasks SET completed_at = updated_at WHERE done = 1 AND completed_at IS NULL`); err != nil {
    return fmt.Errorf("backfill completed_at: %w", err)
}
```

---

### 2. Task Model 变更 (`model/task.go`)

```go
type Task struct {
    // ... 现有字段不变 ...
    CompletedAt *int64 `json:"completed_at,omitempty"` // nil = 未完成, Unix 秒 = 完成时间
}
```

---

### 3. SELECT 列和 Scan 函数更新（变更量最大）

**所有**读取 tasks 的 SQL 和 scan 函数必须加入 `completed_at`：

#### SQLite

| 位置 | 改动 |
|------|------|
| `storage/sqlite/tasks.go` `sqliteTaskSelectSQL()` (L362-373) | `SELECT` 列表加 `t.completed_at` |
| `storage/sqlite/tasks.go` `scanSQLiteTaskRow()` (~L498) | `rows.Scan(..., &task.CompletedAt)` — 直接用 `*int64` |
| `storage/sqlite/tasks.go` `List()` (L18-71) | 复用上述 select/scan，自动生效 |
| `storage/sqlite/tasks.go` `Today()` (L323-360) | 复用上述 select/scan，自动生效 |
| `repository/tasks.go` 全部 6 处内联 SELECT | 每处的列列表加 `t.completed_at`，scan 加对应变量 |
| `repository/tasks.go` `GetTasks()` (L63) | 内联 SQL 加列 |
| `repository/tasks.go` `GetTodayTasks()` (L457, L487) | 两处内联 SQL 加列 |
| `repository/tasks.go` `GetTasksByRoadmapNode()` (~L289) | 内联 SQL 加列 |
| `repository/tasks.go` `GetTaskByID()` (~L423) | 内联 SQL 加列 |

#### PostgreSQL

| 位置 | 改动 |
|------|------|
| `storage/postgres/tasks.go` `postgresTaskSelectSQL()` (L366-377) | `SELECT` 列表加 `t.completed_at` |
| `storage/postgres/tasks.go` `scanPostgresTaskRow()` (~L561) | `Scan(&completedAt)` → `timeToUnix(completedAt)` → `*int64` |
| `storage/postgres/tasks.go` `List()` (L18-66) | 复用上述 select/scan |
| `storage/postgres/tasks.go` `Today()` (L327-363) | 复用上述 select/scan |

> `completed_at` 是 `TIMESTAMPTZ`，可为 NULL。Scan 时使用 `*time.Time`，然后 `timeToUnix()` 转为 `*int64`。NULL 列直接产出 `nil`。

---

### 4. UpdateTask 中 completed_at 的设置/清空（storage 层实现）

**核心原则**：逻辑放在 storage 层，因为在 SQL 构造之前才能决定 SET 子句。

**SQLite** — `storage/sqlite/tasks.go` `Update()` 方法（当前 L222-296）：

在 SET 子句动态构造阶段，`req.Done` 变更时：

```go
if req.Done != nil {
    // 先查当前任务状态以判断 done 是否转换
    current, _ := s.GetByID(ctx, id) // 在 UPDATE 之前读取
    if *req.Done == 1 && (current == nil || current.Done == 0) {
        // 0→1 转换：设置 completed_at
        now := time.Now().Unix()
        setParts = append(setParts, "completed_at = ?")
        args = append(args, now)
    } else if *req.Done == 0 && current != nil && current.Done == 1 {
        // 1→0 转换：清空 completed_at
        setParts = append(setParts, "completed_at = NULL")
        // 不追加 args（NULL 不需要参数占位）
    }
    // done 不变时不操作 completed_at
}
```

**PostgreSQL** — `storage/postgres/tasks.go` `Update()` 方法（当前 L224-310）：

```go
if req.Done != nil {
    current, _ := s.GetByID(ctx, id)
    if *req.Done && (current == nil || !current.Done) {
        pgSet.Add("completed_at", time.Now())
    } else if !*req.Done && current != nil && current.Done {
        pgSet.Add("completed_at", nil) // pgSetBuilder 的 Add(nil) 产生 SQL NULL
    }
}
```

> 关键：要求 Update 方法在执行 UPDATE 之前调用 `GetByID` 读取当前状态。这引入了额外的一次 DB 查询，但对于 done 状态变更的正确性是必需的。

---

### 5. TaskRepository Interface 更新 (`storage/store.go`)

```go
type TaskRepository interface {
    // ... 现有方法不变 ...
    
    // GetCompletedTasksByRange 查询 completed_at 在 [from, to) 范围内的已完成任务
    // 返回 (tasks, totalCount, error)
    GetCompletedTasksByRange(ctx context.Context, from, to int64, page, pageSize int) ([]model.Task, int, error)
}
```

---

### 6. `GET /api/summary`

```
GET /api/summary?from=2026-06-10&to=2026-06-17&page=1&page_size=50
```

#### 日期→时间戳转换（handler 层，含 400 错误处理）

```go
func GetSummary(c *gin.Context) {
    from := c.DefaultQuery("from", "")
    to := c.DefaultQuery("to", "")
    
    fromTime, err := time.ParseInLocation("2006-01-02", from, time.UTC)
    if err != nil {
        c.JSON(400, gin.H{"error": gin.H{"code": "INVALID_DATE", "message": "日期格式无效，需要 YYYY-MM-DD"}})
        return
    }
    toTime, err := time.ParseInLocation("2006-01-02", to, time.UTC)
    if err != nil {
        c.JSON(400, gin.H{"error": gin.H{"code": "INVALID_DATE", "message": "日期格式无效，需要 YYYY-MM-DD"}})
        return
    }
    toTime = toTime.Add(24 * time.Hour) // [from, to+1day) 左闭右开
    
    page := c.DefaultQuery("page", "1")
    pageSize := c.DefaultQuery("page_size", "50")
    // ... 调用 service.GetSummary(fromUnix, toUnix, page, pageSize)
}
```

#### 两步查询（service/summary.go）

**第一步**：通过 `repository.GetCompletedTasksByRange` 查任务 + 项目信息
**第二步**：收集 projectIDs，调用 `repository.GetNotesByProjectIDs` 查关联笔记

```go
func GetSummary(from, to int64, page, pageSize int) (*SummaryData, error) {
    // 第一步：查已完成任务
    tasks, total, err := repository.GetCompletedTasksByRange(from, to, page, pageSize)
    
    // 第二步：收集 project_id，批量查笔记
    projectIDs := uniqueProjectIDs(tasks)
    noteMap, _ := repository.GetNotesByProjectIDs(projectIDs) // map[string][]model.Note
    
    // 组装：将笔记挂到对应项目的任务下
    // 按 completed_at 日期分组
    // 聚合统计
    return &SummaryData{...}, nil
}
```

#### GetNotesByProjectIDs（全新查询，两个后端均需实现）

**方向**：`project_id → notes`（通过 `note_project_links` 表）

```sql
SELECT n.*, npl.project_id
FROM notes n
JOIN note_project_links npl ON n.id = npl.note_id
WHERE npl.project_id IN (?, ?, ...)
ORDER BY n.updated_at DESC
```

> 注意：与现有的 `getNotesProjects`（note_id → projects）**方向相反**。这是一个全新的查询函数，不在现有代码中。

**涉及文件**：
- `storage/sqlite/notes.go` — 新增 `GetNotesByProjectIDs` 方法
- `storage/postgres/notes.go` — 新增 `GetNotesByProjectIDs` 方法
- `repository/notes.go` — 封装函数，返回 `map[projectID][]Note`

#### 统计由后端返回（不受分页影响）

```go
type SummaryData struct {
    Groups    []DateGroup `json:"groups"`
    Total     int         `json:"total"`     // 总完成数（跨所有页）
    ActiveDays int        `json:"active_days"` // 有完成记录的总天数
    ProjectCount int      `json:"project_count"` // 涉及的去重项目总数
    Page      int         `json:"page"`
    PageSize  int         `json:"page_size"`
    Range     DateRange   `json:"range"`
}
```

后端通过额外的 `COUNT(DISTINCT DATE(completed_at))` 和 `COUNT(DISTINCT project_id)` 查询（不加分页限制）来计算全局统计值。

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
  "total": 80,
  "active_days": 5,
  "project_count": 3,
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
│  │ 80 项已完成       │   │  📅 6/17 · 3项                │
│  │ 5 天有产出        │   │  ├─ ✓ 任务标题 [项目badge]    │
│  │ 3 个项目参与      │   │  │   📄 来源笔记: xxx → 跳转  │
│  └──────────────────┘   │  │   📎 项目笔记: yyy → 跳转   │
│                          │  └─ ...                       │
│                          │  📅 6/16 · 2项                │
│                          │  └─ ...                       │
│                          │                               │
│                          │  ← 上一页  第1/2页  下一页 →  │
└──────────────────────────────────────────────────────────┘
```

### 数据获取 — useSummary Hook (`hooks/useSummary.ts`)

遵循项目惯例（参照 `useTasks.ts`、`useNotes.ts`）：

```ts
export function useSummary(from: string, to: string, page: number) {
  return useQuery({
    queryKey: ['summary', from, to, page],
    queryFn: () => api.get<SummaryData>(`/api/summary?from=${from}&to=${to}&page=${page}&page_size=50`),
    enabled: !!from && !!to,
  })
}
```

### 日期选择器 UX

- **预设按钮**：`本周`（默认选中）、`本月` — 点击后自动填充下方日期输入框
- **日期输入**：两个 `<input type="date">` — `起始日期` 和 `结束日期`
- 预设按钮和手动输入双向联动：手动修改日期后，预设按钮取消选中状态
- 变更时重置到第 1 页并触发 refetch

### 统计卡片

三个值全部来自 API 响应的 `total`、`active_days`、`project_count` 字段，确保跨页一致性。

### 交互

| 交互 | 行为 |
|------|------|
| 预设按钮 | 点击「本周/本月」快速设置日期范围 |
| 日期输入 | 手动选择起止日期，预设按钮取消选中 |
| 任务卡片 | 点击展开/折叠，显示 `note_id` 来源笔记和 `linked_notes` 项目笔记 |
| 来源笔记链接 | 跳转到 `/editor/:note_id` |
| 项目笔记链接 | 跳转到 `/editor/:note_id` |
| 项目 badge | 跳转到任务页面 |
| 分页 | 「上一页/下一页」按钮，不影响日期范围 |
| 统计卡片 | 纯展示 |

### 三态处理

| 状态 | 展示 |
|------|------|
| Loading | 骨架卡片 |
| Error | "加载失败" + 重试按钮 |
| Empty | "这个时间段还没有完成的任务" |
| Data | 统计卡片 + 分组任务列表 + 分页 |

### 懒加载

`router.tsx` 添加 `const DailySummary = lazy(() => import('./routes/DailySummary'))`，复用 `App.tsx` 中已有的 `<Suspense>` 边界。

### Sidebar 导航项

```tsx
{ to: '/summary', label: '每日总结', icon: SummaryIcon }
// 不需要 end 属性 — 默认行为就是 prefix match，且 /summary 无子路由
```

---

## 文件清单（v3 完整版）

### 新建文件

| 文件 | 说明 |
|------|------|
| `frontend/src/routes/DailySummary.tsx` | 核心页面组件 |
| `frontend/src/hooks/useSummary.ts` | TanStack Query hook |
| `backend/internal/handler/summary.go` | `GET /api/summary` handler（含 400 错误处理） |
| `backend/internal/service/summary.go` | 两步查询 + 分组 + 全局统计 |
| `db/migrations/postgres/0002_add_completed_at.sql` | PostgreSQL 迁移 + 回填 |
| `e2e/daily-summary.spec.ts` | Playwright e2e 测试（唯一前端测试） |

### 修改文件

| 文件 | 改动 |
|------|------|
| `frontend/src/router.tsx` | lazy import + `{ path: 'summary', element: <DailySummary /> }` |
| `frontend/src/components/layout/Sidebar.tsx` | `navItems` 新条目 + `SummaryIcon` SVG |
| `frontend/src/components/layout/TopBar.tsx` | `pageMeta['/summary']` |
| `frontend/src/styles/index.css` | `.summary-grid` 等新样式 |
| `backend/internal/model/task.go` | `CompletedAt *int64` |
| `backend/internal/storage/store.go` | `TaskRepository` interface 新增 `GetCompletedTasksByRange` |
| `backend/internal/handler/tasks.go` | UpdateTask 响应透传 `completed_at` |
| `backend/internal/storage/sqlite/tasks.go` | (a) SELECT 加列 (b) scan 加字段 (c) Update 加 completed_at 设/清 (d) `GetCompletedTasksByRange` 实现 |
| `backend/internal/storage/postgres/tasks.go` | (a) SELECT 加列 (b) scan 加字段并 timeToUnix (c) Update 加 completed_at 设/清 (d) `GetCompletedTasksByRange` 实现 |
| `backend/internal/storage/sqlite/notes.go` | 新方法 `GetNotesByProjectIDs` |
| `backend/internal/storage/postgres/notes.go` | 新方法 `GetNotesByProjectIDs` |
| `backend/internal/repository/tasks.go` | (a) 6 处内联 SELECT 加列 (b) `GetCompletedTasksByRange`（store 路径 + fallback 路径） |
| `backend/internal/repository/notes.go` | `GetNotesByProjectIDs` 封装 |
| `backend/internal/repository/db.go` | SQLite ALTER TABLE + 回填 |
| `backend/internal/router/router.go` | `api.GET("/summary", handler.GetSummary)` |

---

## 样式设计要点

- 复用现有设计系统变量：`--color-fs-surface`, `--color-fs-accent`, `--shadow-card`, `--radius-lg`
- 统计卡片复用 `.metric-tile` 模式，数字用等宽字体
- 日期栏：左侧预设按钮组（segmented-tabs 风格），右侧双 `<input type="date">`
- 任务列表按日期分组，每组有日期分隔线
- 已完成任务左侧绿色 checkmark（`--color-fs-success`）
- 项目 badge 复用 `.task-project-tag`
- 来源笔记链接：accent 色 + `📄` 前缀
- 项目笔记链接：accent 色 + `📎` 前缀

---

## 验证计划

1. **后端 go test**：`go test ./internal/handler/... ./internal/service/... ./internal/storage/...`
2. **E2E（仅 Playwright，遵循 CLAUDE.md）**：
   - 导航到 `/summary`，验证本周数据默认加载
   - 验证回填后迁移前已完成任务可见
   - 点击「本月」预设按钮 → 日期范围更新 → 数据更新
   - 统计值跨分页一致（翻页后 total/active_days/project_count 不变）
   - 任务展开：区分来源笔记和项目笔记
   - 笔记链接跳转到 `/editor/:id`
   - 项目 badge 跳转到任务页面
   - 分页正常
