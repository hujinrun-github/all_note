# 每日总结页面设计（v5）

**日期**: 2026-06-17 | **分支**: feat/note-project-links  
**审查**: R1:12 / R2:12 / R3:14 / R4:14 — 已修正

---

## 需求摘要

侧边栏新入口「每日总结」。默认本周已完成任务，按日期分组，预设按钮 + 手动日期选择。任务可展开查看来源笔记和项目笔记（可跳转），项目 badge 可跳转。统计摘要由后端返回（不受分页影响）。

---

## 架构总览

```
前端（6 文件） router.tsx, Sidebar.tsx, TopBar.tsx, useSummary.ts, DailySummary.tsx, index.css
后端模型（2 文件） model/task.go, model/summary.go
后端控制流（2 文件） handler/summary.go, service/summary.go
后端存储（6 文件） store.go, sqlite/tasks.go, sqlite/notes.go, postgres/tasks.go, postgres/notes.go（均含相应更新）, repository/tasks.go, repository/notes.go
后端其他（3 文件） router.go, db.go, 0002_*.sql
测试（1 文件） e2e/daily-summary.spec.ts
```

---

## 后端设计

### 1. 数据库迁移

**PostgreSQL** — `db/migrations/postgres/0002_add_completed_at.sql`：
```sql
ALTER TABLE tasks ADD COLUMN completed_at TIMESTAMPTZ;
UPDATE tasks SET completed_at = updated_at WHERE done = true AND completed_at IS NULL;
```

**SQLite** — `repository/db.go` `RunLegacySQLiteMigrations` 追加：
```go
_, err = db.Exec(`ALTER TABLE tasks ADD COLUMN completed_at INTEGER`)
_, err = db.Exec(`UPDATE tasks SET completed_at = updated_at WHERE done = 1 AND completed_at IS NULL`)
```

---

### 2. Model 层

**`model/task.go`** — 新增字段：
```go
CompletedAt *int64 `json:"completed_at,omitempty"`
```

**`model/summary.go`**（新建）：
```go
type SummaryData struct {
    Groups       []DateGroup `json:"groups"`
    ActiveDays   int         `json:"active_days"`
    ProjectCount int         `json:"project_count"`
    // Total 由 successWithPagination 的 pagination.total 提供
}

type DateGroup struct {
    Date  string        `json:"date"`
    Tasks []TaskSummary `json:"tasks"`
    Count int           `json:"count"`
}

type TaskSummary struct {
    Task                     // 嵌入完整 Task（含 completed_at）
    Project      *TaskProject `json:"project,omitempty"`
    LinkedNotes  []NoteRef    `json:"linked_notes,omitempty"`
}

// NoteRef 轻量引用，避免拉全量 Note（不含 body/tags/projects 等大字段）
type NoteRef struct {
    ID    string `json:"id"`
    Title string `json:"title"`
}

type SummaryParams struct {
    From     int64
    To       int64
    Page     int
    PageSize int
}
```

> `NoteRef` 只含 `id` + `title`，不需要 `Note` 的 `body`/`tags`/`projects` 等字段。且不与 `Note.Projects` 的语义产生混淆。

---

### 3. SELECT 列和 Scan 函数更新

#### storage/sqlite/tasks.go
- `sqliteTaskSelectSQL()` → `SELECT` 列表加 `t.completed_at`
- `scanSQLiteTaskRow()` → `rows.Scan(..., &task.CompletedAt)`（`*int64` 直接接收 NULL）

#### storage/postgres/tasks.go
- `postgresTaskSelectSQL()` → 加 `t.completed_at`
- `scanPostgresTaskRow()` → `Scan(&completedAt *time.Time)` → `timeToUnix()` → `*int64`

#### repository/tasks.go
- `scanTaskRow()`（~L632）— 在 `&task.UpdatedAt` 之后追加 `&task.CompletedAt`
- 全部 6 处内联 SELECT（L63, L91, L289, L423, L457, L487）加 `t.completed_at`

---

### 4. UpdateTask — 三个路径均需 TOCTOU + completed_at

| 路径 | 位置 | 修正 |
|------|------|------|
| SQLite storage | `storage/sqlite/tasks.go` `Update()` | 事务内 `GetByID` → 判断 0→1/1→0 → 设/清 `completed_at` |
| Postgres storage | `storage/postgres/tasks.go` `Update()` | `withTx` 内 `getByIDInTx` → 判断 → `pgSet.Add` |
| **Repository fallback** | `repository/tasks.go` `UpdateTask()`（~L296-395） | **新增**：事务内 `db.QueryRow("SELECT done FROM tasks WHERE id=?")` → 判断 → SET 加 `completed_at` |

Repository fallback 具体实现（`repository/tasks.go`）：

```go
func UpdateTask(id string, req model.UpdateTaskRequest) (*model.Task, error) {
    if store := CurrentStore(); store != nil {
        return store.Tasks().Update(context.Background(), id, req)
    }
    // 直接 SQLite fallback
    db := sqliteDB() // 已有 helper（repository/tasks.go:~L430）
    tx, _ := db.Begin()
    defer tx.Rollback()

    // TOCTOU 防护：事务内读当前 done
    var currentDone int
    tx.QueryRow(`SELECT done FROM tasks WHERE id = ?`, id).Scan(&currentDone)

    var sets []string
    var args []any
    // ... 其他字段的 SET 构造 ...

    if req.Done != nil {
        sets = append(sets, "done = ?", "status = ?")
        status := "open"
        if *req.Done == 1 { status = "done" }
        args = append(args, *req.Done, status)

        // completed_at 设/清
        if *req.Done == 1 && currentDone == 0 {
            sets = append(sets, "completed_at = ?")
            args = append(args, time.Now().Unix())
        } else if *req.Done == 0 && currentDone == 1 {
            sets = append(sets, "completed_at = NULL")
        }
    }

    tx.Exec(`UPDATE tasks SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
    tx.Commit()
    // 然后 GetByID 读回并返回
}
```

---

### 5. Interface 更新（`storage/store.go`）

```go
type TaskRepository interface {
    // ... 现有方法 ...
    GetCompletedTasksByRange(ctx context.Context, from, to int64, page, pageSize int) ([]model.Task, int, error)
    GetSummaryStats(ctx context.Context, from, to int64) (activeDays int, projectCount int, err error)
}

type NoteRepository interface {
    // ... 现有方法 ...
    GetNotesByProjectIDs(ctx context.Context, projectIDs []string) (map[string][]model.NoteRef, error)
}
```

---

### 6. GetNotesByProjectIDs — 三层实现

#### storage/sqlite/notes.go

```go
func (s *SQLiteNoteStore) GetNotesByProjectIDs(ctx context.Context, projectIDs []string) (map[string][]model.NoteRef, error) {
    if len(projectIDs) == 0 { return map[string][]model.NoteRef{}, nil }
    query := `SELECT n.id, n.title, npl.project_id FROM notes n
              JOIN note_project_links npl ON n.id = npl.note_id
              WHERE npl.project_id IN (?` + strings.Repeat(`, ?`, len(projectIDs)-1) + `)
              ORDER BY n.updated_at DESC`
    // rows.Scan(&id, &title, &projectID) → map[projectID] = append(..., NoteRef{id, title})
}
```

#### storage/postgres/notes.go

```go
func (s *PostgresNoteStore) GetNotesByProjectIDs(ctx context.Context, projectIDs []string) (map[string][]model.NoteRef, error) {
    if len(projectIDs) == 0 { return map[string][]model.NoteRef{}, nil }
    query := `SELECT n.id, n.title, npl.project_id FROM notes n
              JOIN note_project_links npl ON n.id = npl.note_id
              WHERE npl.project_id = ANY($1)
              ORDER BY n.updated_at DESC`
    // ...
}
```

#### repository/notes.go（fallback）

```go
func GetNotesByProjectIDs(projectIDs []string) (map[string][]model.NoteRef, error) {
    if store := CurrentStore(); store != nil {
        return store.Notes().GetNotesByProjectIDs(context.Background(), projectIDs)
    }
    if len(projectIDs) == 0 { return map[string][]model.NoteRef{}, nil }
    db := sqliteDB() // repository 内部已有
    // 与 sqlite/notes.go 相同的 SQL + scan 逻辑
}
```

> 使用显式列名 `n.id, n.title, npl.project_id` 而非 `n.*`，避免 scan 列序歧义。

---

### 7. `GET /api/summary` Handler

```go
func GetSummary(c *gin.Context) {
    from := c.DefaultQuery("from", "")
    to := c.DefaultQuery("to", "")

    fromTime, err := time.ParseInLocation("2006-01-02", from, time.UTC)
    if err != nil {
        badRequest(c, "日期格式无效，需要 YYYY-MM-DD")  // handler/helpers.go 已有
        return
    }
    toTime, err := time.ParseInLocation("2006-01-02", to, time.UTC)
    if err != nil {
        badRequest(c, "日期格式无效，需要 YYYY-MM-DD")
        return
    }
    if !fromTime.Before(toTime) {
        badRequest(c, "起始日期必须早于结束日期")
        return
    }
    toTime = toTime.Add(24 * time.Hour)

    page, pageSize := getPagination(c) // handler/helpers.go:52 已有（默认 pageSize=20）

    params := model.SummaryParams{
        From: fromTime.Unix(), To: toTime.Unix(),
        Page: page, PageSize: pageSize,
    }
    data, err := service.GetSummary(params)
    if err != nil {
        internalError(c, "获取总结失败")
        return
    }
    successWithPagination(c, data, page, pageSize, data.Total()) // handler/helpers.go 已有
}
```

> `getPagination` 默认 pageSize=20。handler 显式接收即可；前端 `useSummary` 始终传 `page_size=50`。`SummaryData.Total()` 是一个方法，从最后一次统计查询中获取（或在 service 层通过 `GetCompletedTasksByRange` 返回的 total 暂存）。

#### 响应格式（对齐 `APIResponse` 规范）

```json
{
  "data": {
    "groups": [{
      "date": "2026-06-17",
      "tasks": [{ "id": "...", "title": "...", "project": {...}, "note_id": "n0", "linked_notes": [{"id":"n1","title":"review 笔记"}], "completed_at": 1718572800, "planned_date": "2026-06-16", "done": 1 }],
      "count": 3
    }],
    "active_days": 5,
    "project_count": 3
  },
  "pagination": { "page": 1, "page_size": 50, "total": 80 }
}
```

---

### 8. Service 层

```go
func GetSummary(params model.SummaryParams) (*model.SummaryData, error) {
    // 第一步：已完成任务（分页）
    tasks, total, err := repository.GetCompletedTasksByRange(params.From, params.To, params.Page, params.PageSize)

    // 第二步：按 project_id 批量查笔记
    projectIDs := uniqueProjectIDs(tasks)
    noteMap, _ := repository.GetNotesByProjectIDs(projectIDs)

    // 第三步：全局统计（不分页）
    activeDays, projectCount, _ := repository.GetSummaryStats(params.From, params.To)

    // 组装
    groups := groupByDate(tasks, noteMap)
    return &model.SummaryData{
        Groups: groups, ActiveDays: activeDays, ProjectCount: projectCount,
        total: total, // 私有字段，供 Total() 方法
    }, nil
}
```

#### 统计查询的 DATE() 兼容

| 后端 | SQL |
|------|-----|
| PostgreSQL（storage + repository） | `COUNT(DISTINCT DATE(completed_at))` |
| SQLite（storage + repository fallback） | `COUNT(DISTINCT DATE(completed_at, 'unixepoch'))` |

---

### 9. GetCompletedTasksByRange — repository fallback

```go
func GetCompletedTasksByRange(from, to int64, page, pageSize int) ([]model.Task, int, error) {
    if store := CurrentStore(); store != nil {
        return store.Tasks().GetCompletedTasksByRange(context.Background(), from, to, page, pageSize)
    }
    // 直接 SQLite fallback
    db := sqliteDB()
    // COUNT 查询
    var total int
    db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE completed_at >= ? AND completed_at < ?`, from, to).Scan(&total)
    // 数据查询
    offset := (page - 1) * pageSize
    rows, _ := db.Query(`SELECT `+taskSelectColumns+` FROM tasks t LEFT JOIN task_projects p ON t.project_id = p.id WHERE t.completed_at >= ? AND t.completed_at < ? ORDER BY t.completed_at DESC LIMIT ? OFFSET ?`, from, to, pageSize, offset)
    defer rows.Close()
    tasks := []model.Task{}
    for rows.Next() { task := scanTaskRow(rows); tasks = append(tasks, task) }
    // scanTaskRow 已在 §3 更新完毕
    return tasks, total, nil
}
```

> 复用已有的 `sqliteDB()` helper（repository/tasks.go:~L430）获取 DB 句柄。

---

### 10. 排序语义

- 组间：`completed_at` 日期降序
- 组内：`completed_at` 时间戳降序

---

## 前端设计

### 布局（同 v4）

日期栏（预设按钮 + 双 date input）→ 统计卡片（1/3）+ 任务列表（2/3）→ 分页

### useSummary Hook（`hooks/useSummary.ts`）

```ts
export function useSummary(from: string, to: string, page: number) {
  return useQuery({
    queryKey: ['summary', from, to, page],
    queryFn: () => api.get(`/api/summary?from=${from}&to=${to}&page=${page}&page_size=50`),
    enabled: !!from && !!to,
  })
}
```

> 缓存失效：Sidebar 已全局 `queryClient.invalidateQueries()`。

### 日期选择器：预设按钮（本周/本月）+ 双 `<input type="date">`，双向联动，改日期重置 page=1

### 统计卡片：来自 `data.active_days`、`data.project_count`、`pagination.total`

### 任务卡片交互

| 交互 | 行为 | 键盘 |
|------|------|------|
| 展开/折叠 | 显示来源笔记 + 项目笔记 | Enter / Space（用 `<details>` 或 `tabIndex=0` + `onKeyDown`） |
| 笔记链接 | 跳转 `/editor/:id` | Enter（原生 `<a>`） |
| 项目 badge | 跳转任务页面 | Enter |

### 三态：Loading / Error（重试按钮）/ Empty（提示调整日期范围）/ Data

### 懒加载：复用 `App.tsx` `<Suspense>`

### Sidebar：`{ to: '/summary', label: '每日总结', icon: SummaryIcon }`

### SummaryIcon SVG：
```tsx
<svg width="18" height="18" viewBox="0 0 18 18" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
  <path d="M3 5h12M3 9h12M3 13h8" />
  <circle cx="14" cy="13" r="2" />
  <path d="M15.5 14.5L17 16" />
</svg>
```

---

## 文件清单（v5）

### 新建

`frontend/src/routes/DailySummary.tsx` | `frontend/src/hooks/useSummary.ts` | `backend/internal/model/summary.go` | `backend/internal/handler/summary.go` | `backend/internal/service/summary.go` | `db/migrations/postgres/0002_add_completed_at.sql` | `e2e/daily-summary.spec.ts`

### 修改

| 文件 | 改动 |
|------|------|
| `frontend/src/router.tsx` | lazy + route |
| `frontend/src/components/layout/Sidebar.tsx` | navItem + SummaryIcon |
| `frontend/src/components/layout/TopBar.tsx` | pageMeta |
| `frontend/src/styles/index.css` | .summary-* |
| `backend/internal/model/task.go` | CompletedAt |
| `backend/internal/storage/store.go` | TaskRepository +2 方法，NoteRepository +1 方法 |
| `backend/internal/handler/tasks.go` | 响应透传 completed_at |
| `backend/internal/storage/sqlite/tasks.go` | SELECT/scan + Update 事务内 TOCTOU + GetCompletedTasksByRange + GetSummaryStats |
| `backend/internal/storage/postgres/tasks.go` | SELECT/scan + Update 事务内 TOCTOU + GetCompletedTasksByRange + GetSummaryStats |
| `backend/internal/storage/sqlite/notes.go` | GetNotesByProjectIDs |
| `backend/internal/storage/postgres/notes.go` | GetNotesByProjectIDs |
| `backend/internal/repository/tasks.go` | scanTaskRow + 6 内联 SELECT + UpdateTask 事务内 TOCTOU 修复 + GetCompletedTasksByRange fallback + GetSummaryStats fallback |
| `backend/internal/repository/notes.go` | GetNotesByProjectIDs fallback |
| `backend/internal/repository/db.go` | SQLite 迁移 |
| `backend/internal/router/router.go` | api.GET("/summary", handler.GetSummary) |

---

## 验证

1. `go test ./internal/...` — 后端全量
2. `npx playwright test daily-summary.spec.ts` — 本周加载 / 预设切换 / 回填可见 / 统计跨页一致 / 笔记跳转 / 项目跳转 / 分页 / 键盘展开任务
