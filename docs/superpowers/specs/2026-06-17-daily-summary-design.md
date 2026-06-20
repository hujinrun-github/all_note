# 每日总结页面设计（v9）

**日期**: 2026-06-17 | **分支**: feat/note-project-links  
**审查**: R1–R8 累计 85 项 — 已修正

---

## 需求摘要

侧边栏「每日总结」入口。默认本周已完成任务，按日期分组，预设按钮 + 手动日期选择。任务可展开查看来源笔记和项目笔记（可跳转），项目 badge 可跳转。统计后端返回。URL 持久化日期/页码。

---

## 后端设计

### 1. 数据库迁移（不变）

PG: `0002_add_completed_at.sql` | SQLite: `db.go` `RunLegacySQLiteMigrations`

### 2. Model 层

**`model/task.go`** — 新增 `CompletedAt *int64 \`json:"completed_at,omitempty"\``

**`model/summary.go`**（新建）：
```go
package model

type SummaryData struct {
    Groups       []DateGroup `json:"groups"`
    ActiveDays   int         `json:"active_days"`
    ProjectCount int         `json:"project_count"`
    total        int
}
func NewSummaryData(groups []DateGroup, activeDays, projectCount, total int) *SummaryData {
    return &SummaryData{Groups: groups, ActiveDays: activeDays, ProjectCount: projectCount, total: total}
}
func (s *SummaryData) PaginationTotal() int { return s.total }

type DateGroup struct {
    Date  string        `json:"date"`
    Tasks []TaskSummary `json:"tasks"`
    Count int           `json:"count"`
}

type TaskSummary struct {
    ID          string       `json:"id"`
    Title       string       `json:"title"`
    Done        int          `json:"done"`
    PlannedDate *string      `json:"planned_date,omitempty"`
    Due         *int64       `json:"due,omitempty"`
    CompletedAt *int64       `json:"completed_at,omitempty"`
    NoteID      *string      `json:"note_id,omitempty"`
    Project     *TaskProject `json:"project,omitempty"`
    LinkedNotes []NoteRef    `json:"linked_notes,omitempty"`
}

type NoteRef struct {
    ID    string `json:"id"`
    Title string `json:"title"`
}

type SummaryParams struct {
    From, To int64
    Page, PageSize int
}
```

> `NoteRef` 放在 `model/summary.go`。虽然 `NoteRepository` 也引用它，但两者同属 `model` 包，编译器无碍。`NoteRef` 的语义是 summary 页面的轻量引用，与 `Note` 模型分离清晰。

### 3. SELECT/Scan — 注意 GetCompletedTasksByRange 不复用共享管道

现有 `List`/`Today`/`GetByID` 复用 `*TaskSelectSQL()` + `scan*TaskRow()` → `*model.Task`。但 `GetCompletedTasksByRange` 返回类型是 `[]model.TaskSummary`（9 列，非 17 列），**无法复用**上述管道。

两个 storage 后端 + repository fallback 各自实现独立的 SQL + scan：

**storage/sqlite/tasks.go**：
```go
func (s *SQLiteTaskStore) GetCompletedTasksByRange(ctx context.Context, from, to int64, page, pageSize int) ([]model.TaskSummary, int, error) {
    var total int
    if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks WHERE completed_at >= ? AND completed_at < ?`, from, to).Scan(&total); err != nil {
        return nil, 0, err
    }
    offset := (page - 1) * pageSize
    rows, err := s.db.QueryContext(ctx,
        `SELECT t.id, t.title, t.done, t.planned_date, t.due, t.completed_at, t.note_id, p.id, p.name, p.type
         FROM tasks t LEFT JOIN task_projects p ON t.project_id = p.id
         WHERE t.completed_at >= ? AND t.completed_at < ?
         ORDER BY t.completed_at DESC LIMIT ? OFFSET ?`,
        from, to, pageSize, offset)
    if err != nil { return nil, 0, err }
    defer rows.Close()
    // scan → []model.TaskSummary（自定义 scan，非 scanSQLiteTaskRow）
    return summaries, total, nil
}
```

**storage/postgres/tasks.go** — 同逻辑，`$1` 占位符。注意：添加 `completed_at` 后 `scanPostgresTaskRow` 的 Scan 参数从 18 变为 19，列序必须与 `postgresTaskSelectSQL()` 精确一致。

**repository/tasks.go fallback** — 同逻辑，用 `DB.Query(...)` 包级变量。

### 4. UpdateTask — 两个分支都需处理 completed_at

done 可以通过两条路径变更：

| 分支 | 触发条件 | 需处理 |
|------|----------|--------|
| A: `req.Done != nil` | 直接设置 `done` 字段 | `0→1`: 设 `completed_at`；`1→0`: 清空 |
| B: `req.Status != nil && req.Done == nil` | 通过 `status` 字段间接变更 | `status="done" && currentDone==0`: 设；`status≠"done" && currentDone==1`: 清空 |

**分支 B 的伪代码**（三个 Update 路径均需加入）：

```go
// req.Status != nil && req.Done == nil 时
// 注意：status 值需要规范化比较 — repository/tasks.go:42 使用 COALESCE(t.status, CASE WHEN t.done = 1 THEN 'done' ELSE 'open' END)
// 同理此处：判断“变为 done”和“从 done 变为非 done”
if req.Status != nil {
    newStatus := strings.ToLower(*req.Status)
    isCurrentlyDone := (currentDone == 1) // 或 current status 包含 "done"
    isBecomingDone := (newStatus == "done" && !isCurrentlyDone)
    isBecomingUndone := (newStatus != "done" && isCurrentlyDone)
    if isBecomingDone {
        sets = append(sets, "completed_at = ?")
        args = append(args, nowUnix())
    } else if isBecomingUndone {
        sets = append(sets, "completed_at = NULL")
    }
}
```

`currentDone` 的获取方式：
- SQLite storage: `tx.QueryRowContext(...)`（§1 事务内）
- PG storage: `getByIDInTx(ctx, tx, id)`（`withTx` 内）
- Repository fallback: `tx.QueryRow(...)`（`DB.Begin()` 事务内）

使用 `nowUnix()`（`repository/db.go:50`）。使用 `[]interface{}`。

### 5. Interface（`storage/store.go`）

```go
type TaskRepository interface {
    // ... 现有 ...
    GetCompletedTasksByRange(ctx context.Context, from, to int64, page, pageSize int) ([]model.TaskSummary, int, error)
    GetSummaryStats(ctx context.Context, from, to int64) (activeDays, projectCount int, err error)
}
type NoteRepository interface {
    // ... 现有 ...
    GetNotesByProjectIDs(ctx context.Context, projectIDs []string) (map[string][]model.NoteRef, error)
}
```

### 6. GetNotesByProjectIDs — 三层，显式列名（不变）

### 7. Handler

```go
func GetSummary(c *gin.Context) {
    // 使用 time.Local 与 GetToday（service/today.go）和 completed_at 设置（time.Now()）保持一致
    fromTime, err := time.ParseInLocation("2006-01-02", c.DefaultQuery("from", ""), time.Local)
    if err != nil { badRequest(c, "日期格式无效，需要 YYYY-MM-DD"); return }
    toTime, err := time.ParseInLocation("2006-01-02", c.DefaultQuery("to", ""), time.Local)
    if err != nil { badRequest(c, "日期格式无效，需要 YYYY-MM-DD"); return }
    if !fromTime.Before(toTime) { badRequest(c, "起始日期必须早于结束日期"); return }
    toTime = toTime.Add(24 * time.Hour)

    page, pageSize := getPagination(c)
    params := model.SummaryParams{From: fromTime.Unix(), To: toTime.Unix(), Page: page, PageSize: pageSize}
    data, err := service.GetSummary(params)
    if err != nil { internalError(c, "获取总结失败"); return }
    successWithPagination(c, data, page, pageSize, data.PaginationTotal())
}
```

### 8. Service

```go
func GetSummary(params model.SummaryParams) (*model.SummaryData, error) {
    tasks, total, _ := repository.GetCompletedTasksByRange(params.From, params.To, params.Page, params.PageSize)
    noteMap, _ := repository.GetNotesByProjectIDs(uniqueProjectIDs(tasks))
    activeDays, projectCount, _ := repository.GetSummaryStats(params.From, params.To)
    return model.NewSummaryData(groupByDate(tasks, noteMap), activeDays, projectCount, total), nil
}
```

### 9. GetSummaryStats — repository fallback 完整实现

```go
func GetSummaryStats(from, to int64) (activeDays int, projectCount int, err error) {
    if store := CurrentStore(); store != nil {
        return store.Tasks().GetSummaryStats(context.Background(), from, to)
    }
    // fallback: SQLite（DATE 需要 'unixepoch' 修饰符）— 单次查询取两个聚合值
    err = DB.QueryRow(
        `SELECT COUNT(DISTINCT DATE(completed_at, 'unixepoch')),
                COUNT(DISTINCT project_id)
         FROM tasks WHERE completed_at >= ? AND completed_at < ?`,
        from, to,
    ).Scan(&activeDays, &projectCount)
    return
}
```

> DATE() 兼容：PG `DATE(completed_at)`；SQLite（storage + fallback）`DATE(completed_at, 'unixepoch')`

### 10. Router 注册

```go
// router/router.go，在 api 路由组内
api.GET("/summary", handler.GetSummary)
```

### 11. 排序：组间/组内均 `completed_at DESC`

---

## 前端设计

### Router 注册

```tsx
const DailySummary = lazy(() => import('./routes/DailySummary'))
// route:
{ path: 'summary', element: <DailySummary /> }  // App.tsx 的 <Suspense> 提供 fallback
```

> ErrorBoundary 包裹属于防御性改进，本次不强制加入（项目尚无 ErrorBoundary 组件）。如 chunk 加载失败，Suspense fallback 持续显示 "Loading..."，用户刷新即可恢复。

### API 模块（`frontend/src/api/summary.ts`，新建）

遵循项目惯例（参照 `api/notes.ts:36-38` — getNotes 返回 `{ notes, pagination }`）：

```ts
import { api } from './client'
import type { TaskProject } from './tasks'  // TaskProject 定义于此

// NoteRef — 在 api/summary.ts 本地定义（前端没有 ./types 模块）
export interface NoteRef {
  id: string
  title: string
}

export interface TaskSummaryItem {
  id: string; title: string; done: number
  planned_date?: string; due?: number; completed_at?: number
  note_id?: string
  project?: TaskProject
  linked_notes?: NoteRef[]
}

export interface DateGroup {
  date: string; tasks: TaskSummaryItem[]; count: number
}

export interface SummaryResponse {
  groups: DateGroup[]
  active_days: number
  project_count: number
}

export interface Pagination {
  page: number; page_size: number; total: number
}

export interface SummaryResult {
  summary: SummaryResponse
  pagination: Pagination
}

// 返回 { summary, pagination }，与 getNotes 的 { notes, pagination } 模式一致
export async function getSummary(from: string, to: string, page: number, pageSize = 50): Promise<SummaryResult> {
  const res = await api.get<SummaryResponse>('/api/summary', {
    from, to, page: String(page), page_size: String(pageSize),
  })
  return { summary: res.data, pagination: res.pagination! }
}
```

### useSummary Hook

```ts
import { useQuery } from '@tanstack/react-query'
import { getSummary } from '../api/summary'

export function useSummary(from: string, to: string, page: number) {
  return useQuery({
    queryKey: ['summary', from, to, page],
    queryFn: () => getSummary(from, to, page),
    enabled: !!from && !!to,
  })
}
```

> 调用方：`const { data } = useSummary(...)` — `data.summary.groups` 和 `data.pagination.total` 均可访问。

### URL 状态

`useSearchParams` 同步 `from`/`to`/`page`。改范围重置 `page=1`。刷新/分享保持视图。

### 布局

```
桌面端（≥760px）:                     移动端（<760px）:
┌──────────────────────────────┐      ┌──────────────────────┐
│ [本周][本月]  from [ ] to [ ]│      │ [本周][本月]          │
├──────────────┬───────────────┤      │ from [ ] to [ ]      │
│ 统计卡片(1/3)│ 任务列表(2/3) │      ├──────────────────────┤
└──────────────┴───────────────┘      │ 统计卡片（横排三列）  │
                                      │ 任务列表              │
                                      └──────────────────────┘
```

### 三态 + 子态

| 状态 | 展示 |
|------|------|
| Loading | 骨架卡片 |
| Error | "加载失败" + 重试 |
| Empty | "这个时间段还没有完成的任务，试试调整日期范围" |
| Data | 统计卡片 + 分组列表 + 分页 |
| 任务展开为空 | "无关联笔记"（note_id==null && linked_notes==[]） |

### 交互：展开/折叠用 `<details>`（键盘 Enter/Space 原生支持）；笔记跳转 `/editor/:id`；项目 badge 跳转 `/tasks`

### 预设按钮 `[本周]` `[本月]`；手动改日期取消选中；改日期重置 page=1

### 统计卡片：来自 `data.summary.active_days`、`data.summary.project_count`、`data.pagination.total`

### Sidebar navItem + SummaryIcon SVG（同 v6）

### `getPagination` 默认 pageSize=20（handler/helpers.go）— 前端始终传 `page_size=50`；API 直接调用时用默认值 20

---

## 文件清单（v9）

### 新建

`frontend/src/routes/DailySummary.tsx` | `frontend/src/hooks/useSummary.ts` | `frontend/src/api/summary.ts` | `backend/internal/model/summary.go` | `backend/internal/handler/summary.go` | `backend/internal/service/summary.go` | `db/migrations/postgres/0002_add_completed_at.sql` | `e2e/daily-summary.spec.ts`

### 修改

| 文件 | 改动 |
|------|------|
| `frontend/src/router.tsx` | lazy + route |
| `frontend/src/components/layout/Sidebar.tsx` | navItem + SummaryIcon |
| `frontend/src/components/layout/TopBar.tsx` | pageMeta |
| `frontend/src/styles/index.css` | .summary-* + 移动端 @media |
| `backend/internal/model/task.go` | CompletedAt |
| `backend/internal/storage/store.go` | TaskRepository +2, NoteRepository +1 |
| `backend/internal/handler/tasks.go` | 响应透传 completed_at |
| `backend/internal/storage/sqlite/tasks.go` | SELECT/scan + Update（A/B 双分支 TOCTOU）+ GetCompletedTasksByRange（自定义 SQL）+ GetSummaryStats |
| `backend/internal/storage/postgres/tasks.go` | 同上 |
| `backend/internal/storage/sqlite/notes.go` | GetNotesByProjectIDs |
| `backend/internal/storage/postgres/notes.go` | 同上 |
| `backend/internal/repository/tasks.go` | scanTaskRow + 6 SELECT + UpdateTask（A/B 双分支 TOCTOU）+ GetCompletedTasksByRange fallback + GetSummaryStats fallback |
| `backend/internal/repository/notes.go` | GetNotesByProjectIDs fallback |
| `backend/internal/repository/db.go` | SQLite 迁移 |
| `backend/internal/router/router.go` | `api.GET("/summary", handler.GetSummary)` |

---

## 验证

1. `go test ./internal/...`
2. Playwright e2e：本周加载 / 预设切换 / 回填可见 / 统计跨页一致 / 笔记+项目跳转 / 分页 / 键盘展开 / URL 刷新保持 / 移动端 / Status 字段标记完成 → completed_at 正确记录
