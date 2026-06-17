# 每日总结页面设计（v4）

**日期**: 2026-06-17 | **分支**: feat/note-project-links  
**审查**: R1:12 / R2:12 / R3:14 — 已修正

---

## 需求摘要

- 侧边栏新增「每日总结」入口
- 默认本周已完成任务，按日期分组，预设按钮 + 手动日期选择
- 任务可展开：来源笔记、项目笔记（可跳转），项目 badge 可跳转
- 统计摘要由后端返回（不受分页影响）

---

## 架构总览

```
前端（6 文件）
  router.tsx, Sidebar.tsx, TopBar.tsx, hooks/useSummary.ts,
  routes/DailySummary.tsx（新建）, styles/index.css

后端模型（2 文件）
  model/task.go（CompletedAt）, model/summary.go（新建 — SummaryData 等结构体）

后端控制流（2 文件）
  handler/summary.go（新建 — 复用 getPagination + successWithPagination）
  service/summary.go（新建 — 两步查询 + 分组 + 全局统计）

后端存储层（6 文件）
  store.go（TaskRepository + NoteRepository 接口新增方法）
  sqlite/tasks.go（SELECT/scan + Update 事务内 TOCTOU + GetCompletedTasksByRange）
  sqlite/notes.go（GetNotesByProjectIDs）
  postgres/tasks.go（SELECT/scan + Update 事务内 TOCTOU + GetCompletedTasksByRange）
  postgres/notes.go（GetNotesByProjectIDs）
  repository/tasks.go + repository/notes.go（3 层 fallback）

后端其他（3 文件）
  router.go, db.go（SQLite 迁移）, 0002_*.sql（PG 迁移）

测试（1 文件）
  e2e/daily-summary.spec.ts（仅 Playwright）
```

---

## 后端设计

### 1. 数据库迁移（不变）

**PostgreSQL** — `db/migrations/postgres/0002_add_completed_at.sql`：
```sql
ALTER TABLE tasks ADD COLUMN completed_at TIMESTAMPTZ;
UPDATE tasks SET completed_at = updated_at WHERE done = true AND completed_at IS NULL;
```

**SQLite** — `repository/db.go` `RunLegacySQLiteMigrations` 追加：
```go
_, err = db.Exec(`ALTER TABLE tasks ADD COLUMN completed_at INTEGER`)
// ... error handling ...
_, err = db.Exec(`UPDATE tasks SET completed_at = updated_at WHERE done = 1 AND completed_at IS NULL`)
```

---

### 2. Model 层

**`model/task.go`** — 新增字段：
```go
CompletedAt *int64 `json:"completed_at,omitempty"`
```

**`model/summary.go`**（新建）— 所有 summary 结构体：
```go
package model

type SummaryData struct {
    Groups       []DateGroup `json:"groups"`
    Total        int         `json:"total"`
    ActiveDays   int         `json:"active_days"`
    ProjectCount int         `json:"project_count"`
}

type DateGroup struct {
    Date  string       `json:"date"`
    Tasks []TaskSummary `json:"tasks"`
    Count int          `json:"count"`
}

type TaskSummary struct {
    Task
    Project      *TaskProject `json:"project,omitempty"`
    LinkedNotes  []Note       `json:"linked_notes,omitempty"`
}

type SummaryParams struct {
    From     int64
    To       int64
    Page     int
    PageSize int
}
```

---

### 3. SELECT 列和 Scan 函数更新

#### storage/sqlite/tasks.go

- `sqliteTaskSelectSQL()` → 加 `t.completed_at`
- `scanSQLiteTaskRow()` → `rows.Scan(..., &task.CompletedAt)`（`*int64`，NULL 自动为 nil）
- `List()` / `Today()` → 复用上述函数，自动生效

#### storage/postgres/tasks.go

- `postgresTaskSelectSQL()` → 加 `t.completed_at`
- `scanPostgresTaskRow()` → `scan(&completedAt *time.Time)` → `timeToUnix()` → `*int64`

#### repository/tasks.go

- `scanTaskRow()`（~L632）— 全部 19 个 scan 参数追加 `&task.CompletedAt`
- 全部 6 处内联 SELECT（L63, L91, L289, L423, L457, L487）加 `t.completed_at`

---

### 4. UpdateTask — 事务内 TOCTOU 防护

#### SQLite（`storage/sqlite/tasks.go` `Update()`）

```go
func (s *SQLiteTaskStore) Update(ctx context.Context, id string, req model.UpdateTaskRequest) (*model.Task, error) {
    // GetByID 必须在事务内，与 UPDATE 原子化
    tx, _ := s.db.BeginTx(ctx, nil)
    defer tx.Rollback()

    current := &model.Task{}
    err := tx.QueryRowContext(ctx, `SELECT done FROM tasks WHERE id = ?`, id).Scan(&current.Done)
    // ...

    // 构造 SET 子句...
    if req.Done != nil {
        if *req.Done == 1 && current.Done == 0 {
            now := time.Now().Unix()
            sets = append(sets, "completed_at = ?")
            args = append(args, now)
        } else if *req.Done == 0 && current.Done == 1 {
            sets = append(sets, "completed_at = NULL")
        }
    }

    _, err = tx.ExecContext(ctx, `UPDATE tasks SET ...`+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
    tx.Commit()
    // ...
}
```

#### PostgreSQL（`storage/postgres/tasks.go` `Update()`）

```go
func (s *PostgresTaskStore) Update(ctx context.Context, id string, req model.UpdateTaskRequest) (*model.Task, error) {
    return withTx(s.db, ctx, func(tx *sql.Tx) (*model.Task, error) {
        // GetByID 在事务内
        current, err := s.getByIDInTx(ctx, tx, id)
        // ...

        if req.Done != nil {
            if *req.Done && (current == nil || !current.Done) {
                pgSet.Add("completed_at", time.Now())
            } else if !*req.Done && current != nil && current.Done {
                pgSet.Add("completed_at", nil)
            }
        }
        // UPDATE ...
    })
}
```

---

### 5. Interface 更新（`storage/store.go`）

```go
type TaskRepository interface {
    // ... 现有方法 ...
    GetCompletedTasksByRange(ctx context.Context, from, to int64, page, pageSize int) ([]model.Task, int, error)
}

type NoteRepository interface {
    // ... 现有方法 ...
    GetNotesByProjectIDs(ctx context.Context, projectIDs []string) (map[string][]model.Note, error)
}
```

---

### 6. GetNotesByProjectIDs — 三层实现

#### storage/sqlite/notes.go

```go
func (s *SQLiteNoteStore) GetNotesByProjectIDs(ctx context.Context, projectIDs []string) (map[string][]model.Note, error) {
    if len(projectIDs) == 0 { return map[string][]model.Note{}, nil }
    query := `SELECT n.*, npl.project_id FROM notes n
              JOIN note_project_links npl ON n.id = npl.note_id
              WHERE npl.project_id IN (?` + strings.Repeat(`, ?`, len(projectIDs)-1) + `)
              ORDER BY n.updated_at DESC`
    // ... scan rows, group by project_id
}
```

#### storage/postgres/notes.go

```go
func (s *PostgresNoteStore) GetNotesByProjectIDs(ctx context.Context, projectIDs []string) (map[string][]model.Note, error) {
    if len(projectIDs) == 0 { return map[string][]model.Note{}, nil }
    query := `SELECT n.*, npl.project_id FROM notes n
              JOIN note_project_links npl ON n.id = npl.note_id
              WHERE npl.project_id = ANY($1)
              ORDER BY n.updated_at DESC`
    // ...
}
```

#### repository/notes.go（fallback — 直接 SQLite SQL）

```go
func GetNotesByProjectIDs(projectIDs []string) (map[string][]model.Note, error) {
    if store := CurrentStore(); store != nil {
        return store.Notes().GetNotesByProjectIDs(context.Background(), projectIDs)
    }
    // fallback: 直接 SQLite（与 sqlite/notes.go 逻辑相同，使用 db.Query）
    if len(projectIDs) == 0 { return map[string][]model.Note{}, nil }
    db := sqliteDB()
    query := `SELECT n.*, npl.project_id FROM notes n
              JOIN note_project_links npl ON n.id = npl.note_id
              WHERE npl.project_id IN (?` + placeholders(len(projectIDs)-1) + `)
              ORDER BY n.updated_at DESC`
    // ...
}
```

---

### 7. `GET /api/summary` Handler（使用项目现有 helper）

```go
func GetSummary(c *gin.Context) {
    from := c.DefaultQuery("from", "")
    to := c.DefaultQuery("to", "")
    
    fromTime, err := time.ParseInLocation("2006-01-02", from, time.UTC)
    if err != nil {
        validationError(c, "日期格式无效，需要 YYYY-MM-DD")  // handler/helpers.go 已有
        return
    }
    toTime, err := time.ParseInLocation("2006-01-02", to, time.UTC)
    if err != nil {
        validationError(c, "日期格式无效，需要 YYYY-MM-DD")
        return
    }
    if !fromTime.Before(toTime) {
        validationError(c, "起始日期必须早于结束日期")
        return
    }
    toTime = toTime.Add(24 * time.Hour) // [from, to+1day)

    page, pageSize := getPagination(c) // handler/helpers.go:52 — 复用已有函数

    data, err := service.GetSummary(fromTime.Unix(), toTime.Unix(), page, pageSize)
    if err != nil {
        internalError(c, "获取总结失败")  // handler/helpers.go:39 — 复用已有函数
        return
    }
    successWithPagination(c, data, page, pageSize, data.Total)  // handler/helpers.go — 复用已有函数
}
```

**响应格式**（符合项目 `APIResponse` 规范）：

```json
{
  "data": {
    "groups": [
      {
        "date": "2026-06-17",
        "tasks": [{ "id": "...", "title": "...", "project": {...}, "note_id": "n0", "linked_notes": [...], "completed_at": 1718572800, "planned_date": "2026-06-16", "done": 1 }],
        "count": 3
      }
    ],
    "active_days": 5,
    "project_count": 3
  },
  "pagination": { "page": 1, "page_size": 50, "total": 80 }
}
```

---

### 8. Service 层 — 两步查询 + 统计

```go
func GetSummary(from, to int64, page, pageSize int) (*model.SummaryData, error) {
    // 第一步：已完成任务 + 项目
    tasks, total, err := repository.GetCompletedTasksByRange(from, to, page, pageSize)
    
    // 第二步：按 project_id 批量查笔记
    projectIDs := uniqueProjectIDs(tasks)
    noteMap, _ := repository.GetNotesByProjectIDs(projectIDs)
    
    // 组装
    groups := groupByDate(tasks, noteMap)
    
    // 全局统计（不加分页 — 另查 COUNT(DISTINCT ...)）
    activeDays, projectCount := repository.GetSummaryStats(from, to)
    
    return &model.SummaryData{
        Groups: groups, Total: total,
        ActiveDays: activeDays, ProjectCount: projectCount,
    }, nil
}
```

#### 统计查询的 DATE() 兼容

| 后端 | SQL |
|------|-----|
| PostgreSQL | `COUNT(DISTINCT DATE(completed_at))` |
| SQLite (storage) | `COUNT(DISTINCT DATE(completed_at, 'unixepoch'))` |
| SQLite (repository fallback) | `COUNT(DISTINCT DATE(completed_at, 'unixepoch'))` |

---

### 9. 排序语义

- **组间排序**：`completed_at` 日期降序（最近的在前）
- **组内排序**：`completed_at` 时间戳降序（同一天最近完成的在前）

---

## 前端设计

### 布局

```
┌──────────────────────────────────────────────────────────┐
│  日期栏:  [本周] [本月]  │  从 [date] 到 [date]          │
├──────────────────────────────────────────────────────────┤
│  统计卡片 (1/3)         │  已完成任务列表 (2/3)           │
│  ┌──────────────────┐   │  📅 6/17 · 3项                 │
│  │ 80 项已完成       │   │  ├─ ✓ 标题 [项目badge]        │
│  │ 5 天有产出        │   │  │   📄 来源笔记 → /editor/:id│
│  │ 3 个项目参与      │   │  │   📎 项目笔记 → /editor/:id│
│  └──────────────────┘   │  └─ ...                       │
│                          │  📅 6/16 · 2项                │
│                          │                               │
│                          │  ← 上一页  第1/2页  下一页 →  │
└──────────────────────────────────────────────────────────┘
```

### useSummary Hook（`hooks/useSummary.ts`）

```ts
export function useSummary(from: string, to: string, page: number) {
  return useQuery({
    queryKey: ['summary', from, to, page],
    queryFn: () => api.get<SummaryResponse>(`/api/summary?from=${from}&to=${to}&page=${page}&page_size=50`),
    enabled: !!from && !!to,
  })
}
```

> 缓存失效：Sidebar 点击任何导航项时已执行 `queryClient.invalidateQueries()`，从 Tasks 页面标记完成再导航到 Summary 会自动刷新。

### 日期选择器

- 预设按钮 `[本周]` `[本月]`（segmented-tabs 风格），手动改日期后预设取消选中
- 两个 `<input type="date">`，变化时重置 page=1 并 refetch

### 统计卡片

值全部来自 API `data.active_days`、`data.project_count` 和 `pagination.total`，跨页一致。

### 三态：Loading / Error / Empty / Data（同 v3）

### 懒加载：复用 `App.tsx` `<Suspense>`

### Sidebar：`{ to: '/summary', label: '每日总结', icon: SummaryIcon }`（无 `end`）

### SummaryIcon SVG：

```tsx
function SummaryIcon() {
  return (
    <svg width="18" height="18" viewBox="0 0 18 18" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
      <path d="M3 5h12M3 9h12M3 13h8" />
      <circle cx="14" cy="13" r="2" />
      <path d="M15.5 14.5L17 16" />
    </svg>
  )
}
```

---

## 文件清单（v4 完整版）

### 新建

| 文件 |
|------|
| `frontend/src/routes/DailySummary.tsx` |
| `frontend/src/hooks/useSummary.ts` |
| `backend/internal/model/summary.go` |
| `backend/internal/handler/summary.go` |
| `backend/internal/service/summary.go` |
| `db/migrations/postgres/0002_add_completed_at.sql` |
| `e2e/daily-summary.spec.ts` |

### 修改

| 文件 | 改动 |
|------|------|
| `frontend/src/router.tsx` | lazy + route |
| `frontend/src/components/layout/Sidebar.tsx` | navItem + SummaryIcon SVG |
| `frontend/src/components/layout/TopBar.tsx` | pageMeta |
| `frontend/src/styles/index.css` | .summary-* |
| `backend/internal/model/task.go` | CompletedAt *int64 |
| `backend/internal/storage/store.go` | TaskRepository + NoteRepository 接口新增方法 |
| `backend/internal/handler/tasks.go` | 响应透传 completed_at |
| `backend/internal/storage/sqlite/tasks.go` | SELECT/scan 加列 + Update 事务内 TOCTOU + GetCompletedTasksByRange + GetSummaryStats |
| `backend/internal/storage/postgres/tasks.go` | SELECT/scan 加列 + Update 事务内 TOCTOU + GetCompletedTasksByRange + GetSummaryStats |
| `backend/internal/storage/sqlite/notes.go` | GetNotesByProjectIDs |
| `backend/internal/storage/postgres/notes.go` | GetNotesByProjectIDs |
| `backend/internal/repository/tasks.go` | scanTaskRow + 6 内联 SELECT + GetCompletedTasksByRange fallback + GetSummaryStats fallback |
| `backend/internal/repository/notes.go` | GetNotesByProjectIDs fallback |
| `backend/internal/repository/db.go` | SQLite 迁移 |
| `backend/internal/router/router.go` | GET /api/summary |

---

## 验证

1. `go test ./internal/...` — 后端全量
2. `npx playwright test daily-summary.spec.ts` — E2E：本周加载 / 预设切换 / 回填可见 / 统计跨页一致 / 笔记跳转 / 项目跳转 / 分页
