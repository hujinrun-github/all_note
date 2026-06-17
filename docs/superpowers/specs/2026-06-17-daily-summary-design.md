# 每日总结页面设计（v6）

**日期**: 2026-06-17 | **分支**: feat/note-project-links  
**审查**: R1:12 / R2:12 / R3:14 / R4:14 / R5:13 — 已修正

---

## 需求摘要

侧边栏「每日总结」入口。默认本周已完成任务，按日期分组，预设按钮 + 手动日期选择。任务可展开查看来源笔记和项目笔记（可跳转），项目 badge 可跳转。统计后端返回。URL 持久化日期/页码。

---

## 架构总览（不变）

前端 6 + 后端 13 + 迁移 1 + e2e 1

---

## 后端设计

### 1. 数据库迁移（不变）

PG: `0002_add_completed_at.sql` | SQLite: `db.go` `RunLegacySQLiteMigrations`

### 2. Model 层

**`model/task.go`** — 新增：
```go
CompletedAt *int64 `json:"completed_at,omitempty"`
```

**`model/summary.go`**（新建）：
```go
type SummaryData struct {
    Groups       []DateGroup `json:"groups"`
    ActiveDays   int         `json:"active_days"`
    ProjectCount int         `json:"project_count"`
    total        int         // 私有，由 NewSummaryData 设置
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

// TaskSummary — 显式列出字段，不嵌入 Task（避免 Task.Project *string 与 TaskSummary.Project *TaskProject 的 JSON key 冲突）
type TaskSummary struct {
    ID           string       `json:"id"`
    Title        string       `json:"title"`
    Done         int          `json:"done"`
    PlannedDate  *string      `json:"planned_date,omitempty"`
    Due          *int64       `json:"due,omitempty"`
    CompletedAt  *int64       `json:"completed_at,omitempty"`
    NoteID       *string      `json:"note_id,omitempty"`
    Project      *TaskProject `json:"project,omitempty"`
    LinkedNotes  []NoteRef    `json:"linked_notes,omitempty"`
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

> `TaskSummary` 只含列表展示所需字段，不含 `Content`、`Body` 等大字段，避免 50 条/页 × 10KB+/条 的臃肿传输。

### 3. SELECT/Scan 更新

- `sqliteTaskSelectSQL()` / `scanSQLiteTaskRow()` → 加 `completed_at`
- `postgresTaskSelectSQL()` / `scanPostgresTaskRow()` → 加 `completed_at`（`*time.Time` → `timeToUnix`）
- `repository/tasks.go` `scanTaskRow()` ~L632 → 追加 `&task.CompletedAt`
- `repository/tasks.go` 6 处内联 SELECT → 加 `t.completed_at`

### 4. UpdateTask — 三路径 TOCTOU + completed_at

**通用约定**：用 `nowUnix()`（`repository/db.go:50` 已定义）而非 `time.Now().Unix()`。用 `[]interface{}` 而非 `[]any`。

#### SQLite storage — 事务内

```go
tx, err := s.db.BeginTx(ctx, nil)
if err != nil { return nil, err }
defer tx.Rollback() // 显式忽略返回值（与项目风格一致）
// ... GetByID + 判断 0→1/1→0 ...
tx.Commit() // 检查 err
```

#### Postgres storage — withTx 内

```go
return withTx(s.db, ctx, func(tx *sql.Tx) (*model.Task, error) {
    current, err := s.getByIDInTx(ctx, tx, id)
    // ... 判断 ...
})
```

#### Repository fallback（`repository/tasks.go` ~L296-395）

```go
func UpdateTask(id string, req model.UpdateTaskRequest) (*model.Task, error) {
    if store := CurrentStore(); store != nil {
        return store.Tasks().Update(context.Background(), id, req)
    }
    tx, err := DB.Begin()  // ← DB 是 repository 包级变量（db.go:14）
    if err != nil { return nil, err }
    defer tx.Rollback()

    var currentDone int
    if err := tx.QueryRow(`SELECT done FROM tasks WHERE id = ?`, id).Scan(&currentDone); err != nil {
        return nil, err
    }

    var sets []string
    var args []interface{}
    // ... 其他字段 ...

    if req.Done != nil {
        sets = append(sets, "done = ?", "status = ?")
        status := "open"
        if *req.Done == 1 { status = "done" }
        args = append(args, *req.Done, status)
        if *req.Done == 1 && currentDone == 0 {
            sets = append(sets, "completed_at = ?")
            args = append(args, nowUnix())
        } else if *req.Done == 0 && currentDone == 1 {
            sets = append(sets, "completed_at = NULL")
        }
    }

    _, err = tx.Exec(`UPDATE tasks SET `+strings.Join(sets, ", ")+` WHERE id = ?`, append(args, id)...)
    if err != nil { return nil, err }
    if err := tx.Commit(); err != nil { return nil, err }
    return GetTaskByID(id)
}
```

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

### 6. GetNotesByProjectIDs — 三层实现

SQL（storage/sqlite + postgres + repository fallback 三层均用显式列名）：
```sql
SELECT n.id, n.title, npl.project_id
FROM notes n JOIN note_project_links npl ON n.id = npl.note_id
WHERE npl.project_id IN (...)
ORDER BY n.updated_at DESC
```

Repository fallback 使用 `DB.Query(...)`（包级 `DB *sql.DB`，`db.go:14`）。

### 7. Handler（复用 helpers.go 全部函数）

```go
func GetSummary(c *gin.Context) {
    fromTime, err := time.ParseInLocation("2006-01-02", c.DefaultQuery("from", ""), time.UTC)
    if err != nil { badRequest(c, "日期格式无效，需要 YYYY-MM-DD"); return }
    toTime, err := time.ParseInLocation("2006-01-02", c.DefaultQuery("to", ""), time.UTC)
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

响应格式：
```json
{
  "data": {
    "groups": [{"date": "2026-06-17", "tasks": [...], "count": 3}],
    "active_days": 5,
    "project_count": 3
  },
  "pagination": {"page": 1, "page_size": 50, "total": 80}
}
```

### 8. Service 层

```go
func GetSummary(params model.SummaryParams) (*model.SummaryData, error) {
    tasks, total, _ := repository.GetCompletedTasksByRange(params.From, params.To, params.Page, params.PageSize)
    noteMap, _ := repository.GetNotesByProjectIDs(uniqueProjectIDs(tasks))
    activeDays, projectCount, _ := repository.GetSummaryStats(params.From, params.To)
    return model.NewSummaryData(groupByDate(tasks, noteMap), activeDays, projectCount, total), nil
}
```

#### DATE() 兼容

| 后端 | SQL |
|------|-----|
| PG | `DATE(completed_at)` |
| SQLite（storage + fallback） | `DATE(completed_at, 'unixepoch')` |

### 9. GetCompletedTasksByRange — repository fallback

```go
func GetCompletedTasksByRange(from, to int64, page, pageSize int) ([]model.TaskSummary, int, error) {
    if store := CurrentStore(); store != nil {
        return store.Tasks().GetCompletedTasksByRange(context.Background(), from, to, page, pageSize)
    }
    var total int
    DB.QueryRow(`SELECT COUNT(*) FROM tasks WHERE completed_at >= ? AND completed_at < ?`, from, to).Scan(&total)
    offset := (page - 1) * pageSize
    rows, _ := DB.Query(`SELECT t.id, t.title, t.done, t.planned_date, t.due, t.completed_at, t.note_id, p.id, p.name, p.type FROM tasks t LEFT JOIN task_projects p ON t.project_id = p.id WHERE t.completed_at >= ? AND t.completed_at < ? ORDER BY t.completed_at DESC LIMIT ? OFFSET ?`, from, to, pageSize, offset)
    // scan → []model.TaskSummary（显式列，不含大字段）
    return taskSummaries, total, nil
}
```

### 10. 排序：组间 `completed_at` 日期降序，组内 `completed_at` 时间戳降序

---

## 前端设计

### ErrorBoundary

项目中无 ErrorBoundary 组件。在 `router.tsx` 中为 lazy routes 包裹一个轻量 ErrorBoundary：

```tsx
// 新建 components/ErrorBoundary.tsx
class ChunkErrorBoundary extends React.Component<{children: ReactNode}, {hasError: boolean}> {
  state = { hasError: false }
  static getDerivedStateFromError() { return { hasError: true } }
  render() {
    if (this.state.hasError) return <div className="empty-copy">页面加载失败，请刷新重试</div>
    return this.props.children
  }
}

// router.tsx 用法
{ path: 'summary', element: <ChunkErrorBoundary><DailySummary /></ChunkErrorBoundary> }
```

> 其他 lazy routes 也应包裹，属于本次改动中顺手修的范围。

### URL 状态持久化（`useSearchParams`）

```tsx
// DailySummary.tsx
import { useSearchParams } from 'react-router-dom'

export default function DailySummary() {
  const [searchParams, setSearchParams] = useSearchParams()
  const from = searchParams.get('from') || getMonday() // 默认本周一
  const to = searchParams.get('to') || todayDateInputValue()
  const page = parseInt(searchParams.get('page') || '1', 10)

  function setRange(newFrom: string, newTo: string) {
    setSearchParams({ from: newFrom, to: newTo, page: '1' }) // 改范围重置页码
  }
  function setPage(newPage: number) {
    setSearchParams({ from, to, page: String(newPage) })
  }

  const { data, isLoading, error } = useSummary(from, to, page)
  // ...
}
```

### 布局

```
桌面端（≥760px）:                     移动端（<760px）:
┌──────────────────────────────┐      ┌──────────────────────┐
│ [本周][本月]  from [ ] to [ ]│      │ [本周][本月]          │
├──────────────┬───────────────┤      │ from [ ] to [ ]      │
│ 统计卡片     │  任务列表      │      ├──────────────────────┤
│  (1/3)      │  按日期分组    │      │ 统计卡片（横排）      │
│              │               │      ├──────────────────────┤
│              │               │      │ 任务列表              │
└──────────────┴───────────────┘      └──────────────────────┘

移动端统计卡片横排三列（flex-row），日期栏换行，preset 和 inputs 各占一行。
```

### 三态处理

| 状态 | 展示 |
|------|------|
| Loading | 骨架卡片 |
| Error（API） | "加载失败" + 重试按钮 |
| ChunkLoadError | "页面加载失败，请刷新重试"（ErrorBoundary 捕获） |
| Empty | "这个时间段还没有完成的任务，试试调整日期范围" |
| Data | 统计卡片 + 分组列表 + 分页 |
| 任务展开为空 | "无关联笔记" 提示文字（`note_id == null && linked_notes == []` 时） |

### 任务卡片交互

| 交互 | 行为 | 键盘 |
|------|------|------|
| 展开/折叠 | 显示来源笔记 + 项目笔记 | Enter / Space（`<details>` 元素原生支持） |
| 笔记链接 | `navigate(/editor/:id)` | Enter |
| 项目 badge | `navigate(/tasks)` | Enter |

### useSummary Hook

```ts
export function useSummary(from: string, to: string, page: number) {
  return useQuery({
    queryKey: ['summary', from, to, page],
    queryFn: () => api.get(`/api/summary?from=${from}&to=${to}&page=${page}&page_size=50`),
    enabled: !!from && !!to,
  })
}
```

### Sidebar navItem + SummaryIcon SVG（同 v5）

---

## 文件清单（v6）

### 新建

| 文件 |
|------|
| `frontend/src/routes/DailySummary.tsx` |
| `frontend/src/hooks/useSummary.ts` |
| `frontend/src/components/ErrorBoundary.tsx` |
| `backend/internal/model/summary.go` |
| `backend/internal/handler/summary.go` |
| `backend/internal/service/summary.go` |
| `db/migrations/postgres/0002_add_completed_at.sql` |
| `e2e/daily-summary.spec.ts` |

### 修改

| 文件 | 改动 |
|------|------|
| `frontend/src/router.tsx` | lazy + route + ErrorBoundary 包裹 |
| `frontend/src/components/layout/Sidebar.tsx` | navItem + SummaryIcon |
| `frontend/src/components/layout/TopBar.tsx` | pageMeta |
| `frontend/src/styles/index.css` | .summary-* + 移动端媒体查询 |
| `backend/internal/model/task.go` | CompletedAt |
| `backend/internal/storage/store.go` | TaskRepository +2, NoteRepository +1 |
| `backend/internal/handler/tasks.go` | 响应透传 completed_at |
| `backend/internal/storage/sqlite/tasks.go` | SELECT/scan + Update 事务 + GetCompletedTasksByRange + GetSummaryStats |
| `backend/internal/storage/postgres/tasks.go` | 同上 |
| `backend/internal/storage/sqlite/notes.go` | GetNotesByProjectIDs |
| `backend/internal/storage/postgres/notes.go` | 同上 |
| `backend/internal/repository/tasks.go` | scanTaskRow + 6 SELECT + UpdateTask TOCTOU + GetCompletedTasksByRange + GetSummaryStats |
| `backend/internal/repository/notes.go` | GetNotesByProjectIDs fallback |
| `backend/internal/repository/db.go` | SQLite 迁移 |
| `backend/internal/router/router.go` | GET /api/summary |

---

## 验证

1. `go test ./internal/...`
2. Playwright e2e：本周加载 / 预设切换 / 回填可见 / 统计跨页一致 / 笔记跳转 / 项目跳转 / 分页 / 键盘展开 / URL 刷新保持状态 / 移动端堆叠布局
