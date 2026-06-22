# Daily Summary Page Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a daily summary page (`/summary`) showing completed tasks grouped by date with statistics, linked notes, and project navigation.

**Architecture:** New `GET /api/summary` endpoint queries tasks by `completed_at` range. Backend adds `completed_at` column to `tasks` table with migration+backfill, updates all SELECT/scan paths, and adds TOCTOU-safe Update logic. Frontend uses lazy-loaded route with URL-persisted date range via `useSearchParams`.

**Tech Stack:** Go/Gin backend, React+TypeScript+Tailwind frontend, TanStack Query, Playwright e2e

## Global Constraints

- All frontend page tests must use Playwright e2e (per `.claude/CLAUDE.md`) — no Vitest/testing-library
- Backend uses 4-layer architecture: handler → service → repository → storage
- Follow existing Go patterns: `[]interface{}` not `[]any`, `nowUnix()` from `repository/db.go:50`
- Follow existing TS patterns: `api/*.ts` modules returning `{ data, pagination }`, `hooks/use*.ts` TanStack Query wrappers
- Timezone: use `time.Local` (not UTC) for date parsing to align with `GetToday` and `time.Now()`
- SQLite DATE needs `'unixepoch'` modifier; Postgres uses native `DATE()`

---

### Task 1: Backend — Model + Migration

**Files:**
- Modify: `backend/internal/model/task.go:7` (add `CompletedAt` field)
- Create: `backend/internal/model/summary.go`
- Modify: `backend/internal/repository/db.go:58-155` (append SQLite migration)
- Create: `db/migrations/postgres/0002_add_completed_at.sql`

**Interfaces:**
- Produces: `model.Task.CompletedAt *int64`, `model.SummaryData`, `model.TaskSummary`, `model.NoteRef`, `model.SummaryParams`

- [ ] **Step 1: Run existing tests to confirm baseline**

```bash
cd backend && go test ./internal/... 2>&1 | tail -5
```

Expected: all tests pass (or pre-existing failures unrelated to our changes).

- [ ] **Step 2: Add `CompletedAt` to Task model**

In `backend/internal/model/task.go`, after `UpdatedAt` field (~line 21):

```go
CompletedAt *int64 `json:"completed_at,omitempty"`
```

- [ ] **Step 3: Create `backend/internal/model/summary.go`**

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

- [ ] **Step 4: Create PostgreSQL migration**

Create `db/migrations/postgres/0002_add_completed_at.sql`:

```sql
ALTER TABLE tasks ADD COLUMN completed_at TIMESTAMPTZ;
UPDATE tasks SET completed_at = updated_at WHERE done = true AND completed_at IS NULL;
```

- [ ] **Step 5: Add SQLite migration to `RunLegacySQLiteMigrations`**

In `backend/internal/repository/db.go`, inside `RunLegacySQLiteMigrations`, after the last existing statement (~line 140):

```go
_, err = db.Exec(`ALTER TABLE tasks ADD COLUMN completed_at INTEGER`)
if err != nil && !isDuplicateColumnError(err) {
    return fmt.Errorf("add completed_at column: %w", err)
}
if err == nil {
    _, err = db.Exec(`UPDATE tasks SET completed_at = updated_at WHERE done = 1 AND completed_at IS NULL`)
    if err != nil {
        return fmt.Errorf("backfill completed_at: %w", err)
    }
}
```

- [ ] **Step 6: Verify compilation**

```bash
cd backend && go build ./...
```

Expected: builds without errors.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/model/task.go backend/internal/model/summary.go \
        backend/internal/repository/db.go db/migrations/postgres/0002_add_completed_at.sql
git commit -m "feat: add completed_at column and summary model types"
```

---

### Task 2: Backend — SQLite Storage SELECT/Scan + Update TOCTOU

**Files:**
- Modify: `backend/internal/storage/sqlite/tasks.go:362-373` (sqliteTaskSelectSQL)
- Modify: `backend/internal/storage/sqlite/tasks.go:522-532` (scanSQLiteTaskRow)
- Modify: `backend/internal/storage/sqlite/tasks.go:222-312` (Update method)

**Interfaces:**
- Consumes: `model.Task.CompletedAt *int64` (from Task 1)

- [ ] **Step 1: Add `t.completed_at` to `sqliteTaskSelectSQL()`**

At `sqlite/tasks.go:362-373`, add `t.completed_at` as the last column in the SELECT list (before `FROM`):

```go
func sqliteTaskSelectSQL() string {
    return `
        SELECT
            t.id, t.title, COALESCE(t.content, ''), COALESCE(p.name, t.project),
            t.project_id, p.type, t.due, t.planned_date, t.priority, t.done,
            COALESCE(t.status, CASE WHEN t.done = 1 THEN 'done' ELSE 'open' END),
            COALESCE(t.horizon, CASE WHEN t.scope IN ('monthly', 'yearly') THEN 'long' ELSE 'week' END),
            t.scope, t.sort_order, t.note_id, t.roadmap_node_id, t.created_at, t.updated_at,
            t.completed_at
        FROM tasks t
        LEFT JOIN task_projects p ON p.id = t.project_id
    `
}
```

- [ ] **Step 2: Add `&task.CompletedAt` to `scanSQLiteTaskRow()`**

At `sqlite/tasks.go:522-532`, add `&task.CompletedAt` as the 19th Scan parameter (after `&task.UpdatedAt`):

```go
func scanSQLiteTaskRow(row sqliteRowScanner) (*model.Task, error) {
    var task model.Task
    if err := row.Scan(
        &task.ID, &task.Title, &task.Content, &task.Project, &task.ProjectID, &task.ProjectType,
        &task.Due, &task.PlannedDate, &task.Priority, &task.Done, &task.Status, &task.Horizon,
        &task.Scope, &task.SortOrder, &task.NoteID, &task.RoadmapNodeID, &task.CreatedAt, &task.UpdatedAt,
        &task.CompletedAt,
    ); err != nil {
        return nil, err
    }
    return &task, nil
}
```

- [ ] **Step 3: Wrap SQLite Update in transaction + add TOCTOU-safe completed_at logic**

Replace the existing `Update` method body in `sqlite/tasks.go:222-312`. The key change: wrap the entire update in a transaction, read current done state first, then set/clear completed_at based on transition:

```go
func (r taskRepository) Update(ctx context.Context, id string, req *model.UpdateTaskRequest) (*model.Task, error) {
    tx, err := r.db.BeginTx(ctx, nil)
    if err != nil {
        return nil, err
    }
    defer tx.Rollback()

    // TOCTOU: read current state inside transaction
    var currentDone int
    if err := tx.QueryRowContext(ctx, `SELECT done FROM tasks WHERE id = ?`, id).Scan(&currentDone); err != nil {
        if err == sql.ErrNoRows {
            return nil, err
        }
        return nil, err
    }

    sets := []string{"updated_at = ?"}
    args := []interface{}{nowUnix()}
    if req.Title != nil {
        sets = append(sets, "title = ?")
        args = append(args, strings.TrimSpace(*req.Title))
    }
    if req.Content != nil {
        sets = append(sets, "content = ?")
        args = append(args, strings.TrimSpace(*req.Content))
    }
    if req.ProjectID != nil {
        project, err := r.getProjectByIDInTx(ctx, tx, *req.ProjectID)
        if err != nil {
            return nil, err
        }
        sets = append(sets, "project_id = ?", "project = ?")
        args = append(args, project.ID, project.Name)
    } else if req.Project != nil {
        projectID, err := r.ensureTaskProjectByNameInTx(ctx, tx, *req.Project, "regular")
        if err != nil {
            return nil, err
        }
        name := strings.TrimSpace(*req.Project)
        sets = append(sets, "project_id = ?", "project = ?")
        args = append(args, projectID, name)
    }
    if req.Due != nil {
        sets = append(sets, "due = ?")
        args = append(args, *req.Due)
    }
    if req.PlannedDate != nil {
        sets = append(sets, "planned_date = ?")
        args = append(args, strings.TrimSpace(*req.PlannedDate))
    }
    if req.Priority != nil {
        sets = append(sets, "priority = ?")
        args = append(args, *req.Priority)
    }
    // Branch A: req.Done directly set
    if req.Done != nil && req.Status == nil {
        sets = append(sets, "done = ?")
        args = append(args, *req.Done)
        status := "open"
        if *req.Done == 1 {
            status = "done"
        }
        sets = append(sets, "status = ?")
        args = append(args, status)
        // TOCTOU: completed_at set/clear
        if *req.Done == 1 && currentDone == 0 {
            sets = append(sets, "completed_at = ?")
            args = append(args, nowUnix())
        } else if *req.Done == 0 && currentDone == 1 {
            sets = append(sets, "completed_at = NULL")
        }
    }
    // Branch B: req.Status indirectly changes done
    if req.Status != nil {
        newStatus := strings.ToLower(normalizeTaskStatus(*req.Status))
        isCurrentlyDone := (currentDone == 1)
        isBecomingDone := (newStatus == "done" && !isCurrentlyDone)
        isBecomingUndone := (newStatus != "done" && isCurrentlyDone)
        if isBecomingDone {
            sets = append(sets, "completed_at = ?")
            args = append(args, nowUnix())
        } else if isBecomingUndone {
            sets = append(sets, "completed_at = NULL")
        }
        status := normalizeTaskStatus(*req.Status)
        done := 0
        if status == "done" {
            done = 1
        }
        sets = append(sets, "status = ?", "done = ?")
        args = append(args, status, done)
    }
    if req.Scope != nil {
        sets = append(sets, "scope = ?")
        args = append(args, normalizeScope(*req.Scope))
    }
    if req.Horizon != nil {
        sets = append(sets, "horizon = ?")
        args = append(args, normalizeHorizon(*req.Horizon))
    }
    if req.SortOrder != nil {
        sets = append(sets, "sort_order = ?")
        args = append(args, *req.SortOrder)
    }
    if req.RoadmapNodeID != nil {
        sets = append(sets, "roadmap_node_id = ?")
        args = append(args, *req.RoadmapNodeID)
    }
    args = append(args, id)
    result, err := tx.ExecContext(ctx, fmt.Sprintf("UPDATE tasks SET %s WHERE id = ?", strings.Join(sets, ", ")), args...)
    if err != nil {
        return nil, err
    }
    if affected, err := result.RowsAffected(); err == nil && affected == 0 {
        return nil, sql.ErrNoRows
    }
    if err := tx.Commit(); err != nil {
        return nil, err
    }
    task, err := r.GetByID(ctx, id)
    if err != nil {
        return nil, err
    }
    if err := r.syncRoadmapNodeStatus(ctx, task); err != nil {
        return nil, err
    }
    return task, nil
}
```

Note: This introduces `getProjectByIDInTx` and `ensureTaskProjectByNameInTx` — transactional variants of existing helpers. Add these as inline helper methods on `taskRepository`:

```go
func (r taskRepository) getProjectByIDInTx(ctx context.Context, tx *sql.Tx, id string) (*model.TaskProject, error) {
    return scanSQLiteTaskProjectRow(tx.QueryRowContext(ctx, `SELECT id, name, type, description, created_at, updated_at FROM task_projects WHERE id = ?`, id))
}

func (r taskRepository) ensureTaskProjectByNameInTx(ctx context.Context, tx *sql.Tx, name string, typ string) (string, error) {
    name = strings.TrimSpace(name)
    var id string
    err := tx.QueryRowContext(ctx, `SELECT id FROM task_projects WHERE name = ? AND type = ?`, name, typ).Scan(&id)
    if err == sql.ErrNoRows {
        id = uuid.New().String()
        now := nowUnix()
        _, err = tx.ExecContext(ctx,
            `INSERT INTO task_projects (id, name, type, description, created_at, updated_at) VALUES (?, ?, ?, '', ?, ?)`,
            id, name, typ, now, now)
        if err != nil {
            return "", err
        }
        return id, nil
    }
    return id, err
}
```

- [ ] **Step 4: Verify compilation and run SQLite tests**

```bash
cd backend && go build ./...
go test ./internal/storage/sqlite/... -v 2>&1 | tail -20
```

Expected: builds, tests pass.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/storage/sqlite/tasks.go
git commit -m "feat: add completed_at to sqlite SELECT/scan and TOCTOU-safe Update"
```

---

### Task 3: Backend — Postgres Storage SELECT/Scan + Update TOCTOU

**Files:**
- Modify: `backend/internal/storage/postgres/tasks.go:366-377` (postgresTaskSelectSQL)
- Modify: `backend/internal/storage/postgres/tasks.go:561-609` (scanPostgresTaskRow)
- Modify: `backend/internal/storage/postgres/tasks.go:224-311` (Update method)

**Interfaces:**
- Consumes: `model.Task.CompletedAt *int64` (from Task 1)

- [ ] **Step 1: Add `t.completed_at` to `postgresTaskSelectSQL()`**

At `postgres/tasks.go:366-377`, add `t.completed_at` as the last column:

```go
func postgresTaskSelectSQL() string {
    return `
        SELECT
            t.id, t.title, COALESCE(t.content, ''), COALESCE(p.name, t.project),
            t.project_id, p.type, t.due_at, t.planned_date, t.priority, t.done,
            COALESCE(t.status, CASE WHEN t.done THEN 'done' ELSE 'open' END),
            COALESCE(t.horizon, CASE WHEN t.scope IN ('monthly', 'yearly') THEN 'long' ELSE 'week' END),
            t.scope, t.sort_order, t.note_id, t.roadmap_node_id, t.created_at, t.updated_at,
            t.completed_at
        FROM tasks t
        LEFT JOIN task_projects p ON p.id = t.project_id
    `
}
```

- [ ] **Step 2: Add completed_at scan to `scanPostgresTaskRow()`**

At `postgres/tasks.go:561-609`, add `var completedAt sql.NullTime` and scan it as the 19th parameter. Then convert:

```go
func scanPostgresTaskRow(row rowScanner) (*model.Task, error) {
    var task model.Task
    var project sql.NullString
    var projectID sql.NullString
    var projectType sql.NullString
    var dueAt sql.NullTime
    var plannedDate sql.NullTime
    var done bool
    var noteID sql.NullString
    var roadmapNodeID sql.NullString
    var createdAt time.Time
    var updatedAt time.Time
    var completedAt sql.NullTime
    if err := row.Scan(
        &task.ID, &task.Title, &task.Content, &project, &projectID, &projectType,
        &dueAt, &plannedDate, &task.Priority, &done, &task.Status, &task.Horizon,
        &task.Scope, &task.SortOrder, &noteID, &roadmapNodeID, &createdAt, &updatedAt,
        &completedAt,
    ); err != nil {
        return nil, err
    }
    // ... existing null field conversions ...
    task.CreatedAt = timeToUnix(createdAt)
    task.UpdatedAt = timeToUnix(updatedAt)
    if completedAt.Valid {
        u := timeToUnix(completedAt.Time)
        task.CompletedAt = &u
    }
    // ... existing return ...
    return &task, nil
}
```

- [ ] **Step 3: Add TOCTOU-safe completed_at logic to Postgres Update**

In `postgres/tasks.go:224-311` Update method, inside the `withTx` callback, add current state reading before the SET clause construction. Add after opening `withTx`:

```go
var updated *model.Task
err := r.withTx(ctx, func(tx *sql.Tx) error {
    // TOCTOU: read current done state inside transaction
    var currentDone bool
    if err := tx.QueryRowContext(ctx, `SELECT done FROM tasks WHERE id = $1`, id).Scan(&currentDone); err != nil {
        return err
    }

    builder := newPgSetBuilder(1)
    builder.Add("updated_at", time.Now().UTC())
    // ... existing req.Title, req.Content, req.ProjectID, etc. ...

    if req.Done != nil && req.Status == nil {
        builder.Add("done", *req.Done == 1)
        status := "open"
        if *req.Done == 1 { status = "done" }
        builder.Add("status", status)
        // Branch A: completed_at set/clear
        if *req.Done && !currentDone {
            builder.Add("completed_at", time.Now())
        } else if !*req.Done && currentDone {
            builder.Add("completed_at", nil)
        }
    }
    if req.Status != nil {
        status := normalizeTaskStatus(*req.Status)
        builder.Add("status", status)
        newDone := status == "done"
        builder.Add("done", newDone)
        // Branch B: completed_at set/clear via status change
        if newDone && !currentDone {
            builder.Add("completed_at", time.Now())
        } else if !newDone && currentDone {
            builder.Add("completed_at", nil)
        }
    }
    // ... rest of existing Update logic ...
})
```

- [ ] **Step 4: Verify compilation and run Postgres tests**

```bash
cd backend && go build ./...
go test ./internal/storage/postgres/... -v 2>&1 | tail -20
```

Expected: builds, tests pass.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/storage/postgres/tasks.go
git commit -m "feat: add completed_at to postgres SELECT/scan and TOCTOU-safe Update"
```

---

### Task 4: Backend — Repository Layer SELECT/Scan + UpdateTask Fallback

**Files:**
- Modify: `backend/internal/repository/tasks.go:632-658` (scanTaskRow)
- Modify: `backend/internal/repository/tasks.go` — all 6 inline SELECT queries (add `t.completed_at` column)
- Modify: `backend/internal/repository/tasks.go:296-395` (UpdateTask fallback)

**Interfaces:**
- Consumes: `model.Task.CompletedAt *int64` (from Task 1), `DB *sql.DB` (package-level, db.go:14)

- [ ] **Step 1: Update `scanTaskRow()` to scan `completed_at`**

At `repository/tasks.go:632-658`, add `&task.CompletedAt` after `&task.UpdatedAt`:

```go
func scanTaskRow(row rowScanner) (*model.Task, error) {
    var task model.Task
    err := row.Scan(
        &task.ID, &task.Title, &task.Content, &task.Project, &task.ProjectID,
        &task.ProjectType, &task.Due, &task.PlannedDate, &task.Priority, &task.Done,
        &task.Status, &task.Horizon, &task.Scope, &task.SortOrder, &task.NoteID,
        &task.RoadmapNodeID, &task.CreatedAt, &task.UpdatedAt,
        &task.CompletedAt,
    )
    if err != nil { return nil, err }
    return &task, nil
}
```

- [ ] **Step 2: Add `t.completed_at` to all 6 inline SELECT queries**

In `repository/tasks.go`, find each inline SELECT (search for `SELECT.*FROM tasks t LEFT JOIN task_projects`) and add `t.completed_at` as the last column before `FROM`. The 6 locations:

| Line ~ | Function |
|--------|----------|
| ~63 | `GetTasks` data query |
| ~91 | `GetTasks` count query (no change needed — COUNT doesn't use column list) |
| ~289 | `GetTasksByRoadmapNode` |
| ~423 | `GetTaskByID` |
| ~457 | `GetTodayTasks` (today) |
| ~487 | `GetTodayTasks` (overdue) |

For each, add `, t.completed_at` after `t.updated_at` in the SELECT column list.

- [ ] **Step 3: Add TOCTOU + completed_at to `UpdateTask` fallback**

In `repository/tasks.go:296-395`, in the `else` branch (legacy SQLite), wrap in transaction + read current done + set/clear completed_at:

```go
func UpdateTask(id string, req *model.UpdateTaskRequest) (*model.Task, error) {
    if store := CurrentStore(); store != nil {
        return store.Tasks().Update(context.Background(), id, req)
    }
    tx, err := DB.Begin()
    if err != nil { return nil, err }
    defer tx.Rollback()

    var currentDone int
    if err := tx.QueryRow(`SELECT done FROM tasks WHERE id = ?`, id).Scan(&currentDone); err != nil {
        return nil, err
    }

    sets := []string{"updated_at = ?"}
    args := []interface{}{nowUnix()}
    // ... (same field handling as sqlite/tasks.go Update) ...

    // Branch A + B: completed_at set/clear (same logic as Task 2 Step 3)

    result, err := tx.Exec(`UPDATE tasks SET `+strings.Join(sets, ", ")+` WHERE id = ?`, append(args, id)...)
    if err != nil { return nil, err }
    if affected, _ := result.RowsAffected(); affected == 0 { return nil, sql.ErrNoRows }
    if err := tx.Commit(); err != nil { return nil, err }
    return GetTaskByID(id)
}
```

- [ ] **Step 4: Verify compilation**

```bash
cd backend && go build ./...
```

Expected: builds without errors.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/repository/tasks.go
git commit -m "feat: add completed_at to repository scanTaskRow, inline SELECTs, and UpdateTask fallback TOCTOU"
```

---

### Task 5: Backend — Store Interface + New Query Methods + Notes Query

**Files:**
- Modify: `backend/internal/storage/store.go:79-92` (TaskRepository interface)
- Modify: `backend/internal/storage/store.go:57-66` (NoteRepository interface)
- Modify: `backend/internal/storage/sqlite/tasks.go` (add GetCompletedTasksByRange, GetSummaryStats)
- Modify: `backend/internal/storage/postgres/tasks.go` (same)
- Modify: `backend/internal/storage/sqlite/notes.go` (add GetNotesByProjectIDs)
- Modify: `backend/internal/storage/postgres/notes.go` (same)
- Modify: `backend/internal/repository/tasks.go` (add fallback GetCompletedTasksByRange, GetSummaryStats)
- Modify: `backend/internal/repository/notes.go` (add fallback GetNotesByProjectIDs)

**Interfaces:**
- Consumes: `model.TaskSummary`, `model.NoteRef`, `storage.TaskRepository` interface, `storage.NoteRepository` interface
- Produces: `GetCompletedTasksByRange`, `GetSummaryStats`, `GetNotesByProjectIDs` methods

- [ ] **Step 1: Add methods to `TaskRepository` interface in `store.go`**

At `storage/store.go:92` (after `Today`), add:

```go
GetCompletedTasksByRange(ctx context.Context, from, to int64, page, pageSize int) ([]model.TaskSummary, int, error)
GetSummaryStats(ctx context.Context, from, to int64) (activeDays, projectCount int, err error)
```

- [ ] **Step 2: Add method to `NoteRepository` interface in `store.go`**

At `storage/store.go:66` (after `Recent`), add:

```go
GetNotesByProjectIDs(ctx context.Context, projectIDs []string) (map[string][]model.NoteRef, error)
```

- [ ] **Step 3: Implement `GetCompletedTasksByRange` and `GetSummaryStats` in SQLite**

In `sqlite/tasks.go`, add:

```go
func (r taskRepository) GetCompletedTasksByRange(ctx context.Context, from, to int64, page, pageSize int) ([]model.TaskSummary, int, error) {
    var total int
    if err := r.db.QueryRowContext(ctx,
        `SELECT COUNT(*) FROM tasks WHERE completed_at >= ? AND completed_at < ?`,
        from, to,
    ).Scan(&total); err != nil {
        return nil, 0, err
    }
    offset := (page - 1) * pageSize
    rows, err := r.db.QueryContext(ctx,
        `SELECT t.id, t.title, t.done, t.planned_date, t.due, t.completed_at, t.note_id,
                p.id, p.name, p.type
         FROM tasks t LEFT JOIN task_projects p ON t.project_id = p.id
         WHERE t.completed_at >= ? AND t.completed_at < ?
         ORDER BY t.completed_at DESC LIMIT ? OFFSET ?`,
        from, to, pageSize, offset,
    )
    if err != nil { return nil, 0, err }
    defer rows.Close()
    var summaries []model.TaskSummary
    for rows.Next() {
        var s model.TaskSummary
        var projectID, projectName, projectType sql.NullString
        if err := rows.Scan(&s.ID, &s.Title, &s.Done, &s.PlannedDate, &s.Due, &s.CompletedAt,
            &s.NoteID, &projectID, &projectName, &projectType); err != nil {
            return nil, 0, err
        }
        if projectID.Valid {
            s.Project = &model.TaskProject{ID: projectID.String, Name: projectName.String, Type: projectType.String}
        }
        summaries = append(summaries, s)
    }
    return summaries, total, rows.Err()
}

func (r taskRepository) GetSummaryStats(ctx context.Context, from, to int64) (int, int, error) {
    var activeDays, projectCount int
    err := r.db.QueryRowContext(ctx,
        `SELECT COUNT(DISTINCT DATE(completed_at, 'unixepoch')),
                COUNT(DISTINCT project_id)
         FROM tasks WHERE completed_at >= ? AND completed_at < ?`,
        from, to,
    ).Scan(&activeDays, &projectCount)
    return activeDays, projectCount, err
}
```

- [ ] **Step 4: Implement `GetCompletedTasksByRange` and `GetSummaryStats` in Postgres**

In `postgres/tasks.go`, add analogous methods using `$1`/`$2`/`$3`/`$4` placeholders and `DATE(completed_at)` (no `'unixepoch'`).

- [ ] **Step 5: Implement `GetNotesByProjectIDs` in SQLite**

In `sqlite/notes.go`, add:

```go
func (r noteRepository) GetNotesByProjectIDs(ctx context.Context, projectIDs []string) (map[string][]model.NoteRef, error) {
    if len(projectIDs) == 0 {
        return map[string][]model.NoteRef{}, nil
    }
    placeholders := make([]string, len(projectIDs))
    args := make([]interface{}, len(projectIDs))
    for i, id := range projectIDs {
        placeholders[i] = "?"
        args[i] = id
    }
    query := fmt.Sprintf(
        `SELECT n.id, n.title, npl.project_id
         FROM notes n
         JOIN note_project_links npl ON n.id = npl.note_id
         WHERE npl.project_id IN (%s)
         ORDER BY n.updated_at DESC`, strings.Join(placeholders, ","))
    rows, err := r.db.QueryContext(ctx, query, args...)
    if err != nil { return nil, err }
    defer rows.Close()
    result := make(map[string][]model.NoteRef)
    for rows.Next() {
        var noteID, projectID string
        var ref model.NoteRef
        if err := rows.Scan(&ref.ID, &ref.Title, &projectID); err != nil { return nil, err }
        result[projectID] = append(result[projectID], ref)
    }
    return result, rows.Err()
}
```

- [ ] **Step 6: Implement `GetNotesByProjectIDs` in Postgres**

In `postgres/notes.go`, add analogous method using `ANY($1::text[])` with `pq.Array(projectIDs)`.

- [ ] **Step 7: Add repository fallback implementations**

In `repository/tasks.go`, add `GetCompletedTasksByRange` and `GetSummaryStats` fallback functions (check `CurrentStore()` first, else use `DB`).

In `repository/notes.go`, add `GetNotesByProjectIDs` fallback (same pattern).

- [ ] **Step 8: Verify compilation and run all tests**

```bash
cd backend && go build ./...
go test ./internal/... 2>&1 | tail -10
```

Expected: builds, tests pass.

- [ ] **Step 9: Commit**

```bash
git add backend/internal/storage/store.go \
        backend/internal/storage/sqlite/tasks.go backend/internal/storage/postgres/tasks.go \
        backend/internal/storage/sqlite/notes.go backend/internal/storage/postgres/notes.go \
        backend/internal/repository/tasks.go backend/internal/repository/notes.go
git commit -m "feat: add GetCompletedTasksByRange, GetSummaryStats, GetNotesByProjectIDs (3-layer)"
```

---

### Task 6: Backend — Handler, Service, Router

**Files:**
- Create: `backend/internal/handler/summary.go`
- Create: `backend/internal/service/summary.go`
- Modify: `backend/internal/router/router.go:78` (add route)

**Interfaces:**
- Consumes: `model.SummaryParams`, `model.SummaryData`, `repository.GetCompletedTasksByRange`, `repository.GetNotesByProjectIDs`, `repository.GetSummaryStats`
- Produces: `GET /api/summary`

- [ ] **Step 1: Create `backend/internal/service/summary.go`**

```go
package service

import (
    "sort"
    "time"
    "github.com/hujinrun/flowspace/internal/model"
    "github.com/hujinrun/flowspace/internal/repository"
)

func GetSummary(params model.SummaryParams) (*model.SummaryData, error) {
    tasks, total, err := repository.GetCompletedTasksByRange(params.From, params.To, params.Page, params.PageSize)
    if err != nil {
        return nil, err
    }

    projectIDs := make(map[string]bool)
    for _, t := range tasks {
        if t.Project != nil {
            projectIDs[t.Project.ID] = true
        }
    }
    ids := make([]string, 0, len(projectIDs))
    for id := range projectIDs {
        ids = append(ids, id)
    }
    noteMap, err := repository.GetNotesByProjectIDs(ids)
    if err != nil {
        noteMap = map[string][]model.NoteRef{}
    }

    // Attach notes and group by date
    for i := range tasks {
        if tasks[i].Project != nil {
            tasks[i].LinkedNotes = noteMap[tasks[i].Project.ID]
        }
    }

    groups := groupByDate(tasks)
    activeDays, projectCount, err := repository.GetSummaryStats(params.From, params.To)
    if err != nil {
        activeDays, projectCount = 0, 0
    }

    return model.NewSummaryData(groups, activeDays, projectCount, total), nil
}

func groupByDate(tasks []model.TaskSummary) []model.DateGroup {
    groups := make(map[string][]model.TaskSummary)
    var dates []string
    for _, t := range tasks {
        var date string
        if t.CompletedAt != nil {
            date = time.Unix(*t.CompletedAt, 0).Format("2006-01-02")
        }
        if date == "" {
            date = "未知日期"
        }
        if _, ok := groups[date]; !ok {
            dates = append(dates, date)
        }
        groups[date] = append(groups[date], t)
    }
    sort.Slice(dates, func(i, j int) bool { return dates[i] > dates[j] })
    var result []model.DateGroup
    for _, d := range dates {
        result = append(result, model.DateGroup{Date: d, Tasks: groups[d], Count: len(groups[d])})
    }
    return result
}
```

- [ ] **Step 2: Create `backend/internal/handler/summary.go`**

```go
package handler

import (
    "time"
    "github.com/gin-gonic/gin"
    "github.com/hujinrun/flowspace/internal/model"
    "github.com/hujinrun/flowspace/internal/service"
)

func GetSummary(c *gin.Context) {
    fromTime, err := time.ParseInLocation("2006-01-02", c.DefaultQuery("from", ""), time.Local)
    if err != nil {
        badRequest(c, "日期格式无效，需要 YYYY-MM-DD")
        return
    }
    toTime, err := time.ParseInLocation("2006-01-02", c.DefaultQuery("to", ""), time.Local)
    if err != nil {
        badRequest(c, "日期格式无效，需要 YYYY-MM-DD")
        return
    }
    if !fromTime.Before(toTime) {
        badRequest(c, "起始日期必须早于结束日期")
        return
    }
    toTime = toTime.Add(24 * time.Hour)

    page, pageSize := getPagination(c)
    params := model.SummaryParams{
        From: fromTime.Unix(), To: toTime.Unix(),
        Page: page, PageSize: pageSize,
    }
    data, err := service.GetSummary(params)
    if err != nil {
        internalError(c, "获取总结失败")
        return
    }
    successWithPagination(c, data, page, pageSize, data.PaginationTotal())
}
```

- [ ] **Step 3: Register route in `router.go`**

At `backend/internal/router/router.go`, after the `today` route (~line 78):

```go
api.GET("/summary", handler.GetSummary)
```

- [ ] **Step 4: Verify compilation and run tests**

```bash
cd backend && go build ./...
go test ./internal/handler/... ./internal/service/... 2>&1 | tail -10
```

Expected: builds, tests pass.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/handler/summary.go backend/internal/service/summary.go \
        backend/internal/router/router.go
git commit -m "feat: add GET /api/summary endpoint with handler, service, and router"
```

---

### Task 7: Frontend — API Module + Hook

**Files:**
- Create: `frontend/src/api/summary.ts`
- Create: `frontend/src/hooks/useSummary.ts`

**Interfaces:**
- Consumes: `api` client from `api/client.ts`, `useQuery` from `@tanstack/react-query`
- Produces: `getSummary`, `useSummary`, `SummaryResult`, `TaskSummaryItem`, `NoteRef` types

- [ ] **Step 1: Create `frontend/src/api/summary.ts`**

```ts
import { api } from './client'
import type { TaskProject } from './tasks'

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

export async function getSummary(from: string, to: string, page: number, pageSize = 50): Promise<SummaryResult> {
  const res = await api.get<SummaryResponse>('/api/summary', {
    from, to, page: String(page), page_size: String(pageSize),
  })
  return { summary: res.data, pagination: res.pagination! }
}
```

- [ ] **Step 2: Create `frontend/src/hooks/useSummary.ts`**

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

- [ ] **Step 3: Verify TypeScript compilation**

```bash
cd frontend && npx tsc --noEmit 2>&1 | tail -10
```

Expected: no new type errors.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/api/summary.ts frontend/src/hooks/useSummary.ts
git commit -m "feat: add frontend api/summary.ts and useSummary hook"
```

---

### Task 8: Frontend — DailySummary Page Component

**Files:**
- Create: `frontend/src/routes/DailySummary.tsx`

**Interfaces:**
- Consumes: `useSummary`, `useSearchParams`, `useNavigate`

- [ ] **Step 1: Create `frontend/src/routes/DailySummary.tsx`**

```tsx
import { useMemo } from 'react'
import { useSearchParams, useNavigate, Link } from 'react-router-dom'
import { useSummary } from '../hooks/useSummary'
import type { DateGroup } from '../api/summary'

function getMonday(d: Date = new Date()): string {
  const day = d.getDay()
  const diff = d.getDate() - day + (day === 0 ? -6 : 1)
  const monday = new Date(d.getFullYear(), d.getMonth(), diff)
  return monday.toISOString().slice(0, 10)
}

function todayDateInputValue(): string {
  return new Date().toISOString().slice(0, 10)
}

function getMonthStart(): string {
  const d = new Date()
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-01`
}

export default function DailySummary() {
  const navigate = useNavigate()
  const [searchParams, setSearchParams] = useSearchParams()
  const from = searchParams.get('from') || getMonday()
  const to = searchParams.get('to') || todayDateInputValue()
  const page = parseInt(searchParams.get('page') || '1', 10)

  const { data, isLoading, error } = useSummary(from, to, page)

  const activePreset = useMemo(() => {
    if (from === getMonday() && to === todayDateInputValue()) return 'week'
    if (from === getMonthStart() && to === todayDateInputValue()) return 'month'
    return null
  }, [from, to])

  function setRange(newFrom: string, newTo: string) {
    setSearchParams({ from: newFrom, to: newTo, page: '1' })
  }

  function setPage(newPage: number) {
    setSearchParams({ from, to, page: String(newPage) })
  }

  if (isLoading) {
    return (
      <div className="summary-grid">
        <div className="surface-panel animate-pulse h-48" />
        <div className="surface-panel animate-pulse h-96" />
      </div>
    )
  }

  if (error) {
    return (
      <div className="text-center py-12">
        <p className="text-fs-text-muted text-sm mb-3">加载失败</p>
        <button onClick={() => window.location.reload()} className="filter-pill is-active">重试</button>
      </div>
    )
  }

  if (!data) return null

  const { summary, pagination } = data
  const totalPages = Math.ceil(pagination.total / pagination.page_size)

  return (
    <div className="summary-page">
      {/* Date bar */}
      <div className="summary-date-bar">
        <div className="segmented-tabs">
          <button
            className={activePreset === 'week' ? 'is-active' : ''}
            onClick={() => setRange(getMonday(), todayDateInputValue())}
          >本周</button>
          <button
            className={activePreset === 'month' ? 'is-active' : ''}
            onClick={() => setRange(getMonthStart(), todayDateInputValue())}
          >本月</button>
        </div>
        <div className="summary-date-inputs">
          <label>
            <span>从</span>
            <input type="date" value={from}
              onChange={e => setRange(e.target.value, to)} />
          </label>
          <label>
            <span>到</span>
            <input type="date" value={to}
              onChange={e => setRange(from, e.target.value)} />
          </label>
        </div>
      </div>

      <div className="summary-grid">
        {/* Stats cards */}
        <div className="summary-stats">
          <div className="metric-tile">
            <span>已完成</span>
            <strong>{pagination.total}</strong>
            <p>项任务</p>
          </div>
          <div className="metric-tile">
            <span>活跃</span>
            <strong>{summary.active_days}</strong>
            <p>天有产出</p>
          </div>
          <div className="metric-tile">
            <span>参与</span>
            <strong>{summary.project_count}</strong>
            <p>个项目</p>
          </div>
        </div>

        {/* Task list */}
        <div className="summary-task-list">
          {summary.groups.length === 0 ? (
            <p className="empty-copy">这个时间段还没有完成的任务，试试调整日期范围</p>
          ) : (
            summary.groups.map((group: DateGroup) => (
              <div key={group.date} className="task-section">
                <span className="summary-date-heading">
                  📅 {group.date} · {group.count}项
                </span>
                <div className="row-stack">
                  {group.tasks.map(task => (
                    <details key={task.id} className="summary-task-card">
                      <summary className="summary-task-header">
                        <span className="summary-task-check">✓</span>
                        <strong className={task.done ? 'is-done' : ''}>{task.title}</strong>
                        {task.project && (
                          <button type="button" className="task-project-tag"
                            onClick={e => { e.preventDefault(); navigate('/tasks') }}>
                            {task.project.name}
                          </button>
                        )}
                      </summary>
                      <div className="summary-task-detail">
                        {task.note_id && (
                          <p>📄 来源笔记：<Link to={`/editor/${encodeURIComponent(task.note_id)}`} className="text-fs-accent hover:underline">查看</Link></p>
                        )}
                        {(!task.note_id && (!task.linked_notes || task.linked_notes.length === 0)) && (
                          <p className="text-fs-text-muted">无关联笔记</p>
                        )}
                        {task.linked_notes && task.linked_notes.length > 0 && (
                          <div>
                            <span>📎 项目笔记：</span>
                            {task.linked_notes.map(note => (
                              <Link key={note.id} to={`/editor/${encodeURIComponent(note.id)}`}
                                className="text-fs-accent hover:underline ml-1">{note.title}</Link>
                            ))}
                          </div>
                        )}
                      </div>
                    </details>
                  ))}
                </div>
              </div>
            ))
          )}
        </div>
      </div>

      {/* Pagination */}
      {totalPages > 1 && (
        <div className="summary-pagination">
          <button disabled={page <= 1} onClick={() => setPage(page - 1)} className="filter-pill">上一页</button>
          <span className="text-fs-text-muted">第 {page}/{totalPages} 页</span>
          <button disabled={page >= totalPages} onClick={() => setPage(page + 1)} className="filter-pill">下一页</button>
        </div>
      )}
    </div>
  )
}
```

- [ ] **Step 2: Verify TypeScript compilation**

```bash
cd frontend && npx tsc --noEmit 2>&1 | tail -10
```

Expected: no new type errors.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/routes/DailySummary.tsx
git commit -m "feat: add DailySummary page with date bar, stats, task groups, and pagination"
```

---

### Task 9: Frontend — Router + Sidebar + TopBar + CSS

**Files:**
- Modify: `frontend/src/router.tsx` (lazy import + route)
- Modify: `frontend/src/components/layout/Sidebar.tsx` (nav item + icon)
- Modify: `frontend/src/components/layout/TopBar.tsx` (pageMeta)
- Modify: `frontend/src/styles/index.css` (summary styles)

- [ ] **Step 1: Add route to `router.tsx`**

```tsx
const DailySummary = lazy(() => import('./routes/DailySummary'))
// In children array:
{ path: 'summary', element: <DailySummary /> },
```

- [ ] **Step 2: Add nav item to `Sidebar.tsx`**

Add to `navItems` array:
```tsx
{ to: '/summary', label: '每日总结', icon: SummaryIcon },
```

Add SVG icon component at bottom of file:
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

- [ ] **Step 3: Add pageMeta to `TopBar.tsx`**

```tsx
'/summary': { title: '每日总结', subtitle: '回顾你已经完成的事项和产出' },
```

- [ ] **Step 4: Add CSS styles to `index.css`**

```css
/* Summary page */
.summary-page { display: grid; gap: 1rem; }

.summary-date-bar {
  display: flex; align-items: center; justify-content: space-between;
  gap: 1rem; flex-wrap: wrap;
}

.summary-date-inputs {
  display: flex; align-items: center; gap: 0.5rem;
}
.summary-date-inputs label {
  display: flex; align-items: center; gap: 0.35rem;
  font-size: 0.8rem; color: var(--color-fs-text-muted);
}
.summary-date-inputs input[type="date"] {
  border: 1px solid var(--color-fs-border);
  border-radius: var(--radius-sm);
  padding: 0.35rem 0.5rem;
  font-size: 0.8rem;
  background: var(--color-fs-surface);
}

.summary-grid {
  display: grid;
  grid-template-columns: 1fr 2fr;
  gap: 1rem;
  align-items: start;
}

.summary-stats {
  display: grid; gap: 0.85rem; align-content: start;
}

.summary-date-heading {
  font-size: 0.8rem; font-weight: 700; color: var(--color-fs-text-muted);
  text-transform: none; letter-spacing: 0.02em;
}

.summary-task-card {
  background: var(--color-fs-surface);
  border: 1px solid var(--color-fs-border);
  border-radius: var(--radius-md);
  padding: 0.75rem 1rem;
  transition: border-color 0.2s var(--ease-standard);
}
.summary-task-card[open] { border-color: var(--color-fs-accent); }

.summary-task-header {
  display: flex; align-items: center; gap: 0.65rem;
  cursor: pointer; list-style: none;
  font-size: 0.88rem;
}
.summary-task-header::-webkit-details-marker { display: none; }

.summary-task-check {
  width: 22px; height: 22px;
  display: grid; place-items: center;
  border-radius: 6px;
  background: var(--color-fs-success); color: #fff;
  font-size: 11px; font-weight: 800;
  flex-shrink: 0;
}
.summary-task-header strong.is-done {
  color: var(--color-fs-text-disabled); text-decoration: line-through;
}

.summary-task-detail {
  margin-top: 0.65rem; padding-top: 0.65rem;
  border-top: 1px solid var(--color-fs-border);
  font-size: 0.82rem; display: grid; gap: 0.35rem;
}

.summary-pagination {
  display: flex; align-items: center; justify-content: center;
  gap: 0.85rem; padding-top: 0.5rem;
}

@media (max-width: 760px) {
  .summary-grid { grid-template-columns: 1fr; }
  .summary-stats { grid-template-columns: repeat(3, 1fr); }
  .summary-date-bar { flex-direction: column; align-items: flex-start; }
}
```

- [ ] **Step 5: Verify build**

```bash
cd frontend && npx tsc --noEmit 2>&1 | tail -10
```

Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/router.tsx \
        frontend/src/components/layout/Sidebar.tsx \
        frontend/src/components/layout/TopBar.tsx \
        frontend/src/styles/index.css
git commit -m "feat: wire up DailySummary route, sidebar nav, topbar meta, and styles"
```

---

### Task 10: E2E Test — Playwright

**Files:**
- Create: `e2e/daily-summary.spec.ts`

**Interfaces:**
- Consumes: Running backend + frontend, Playwright browser

- [ ] **Step 1: Create `e2e/daily-summary.spec.ts`**

```ts
import { test, expect } from '@playwright/test'

test.describe('Daily Summary page', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/summary')
    await page.waitForLoadState('networkidle')
  })

  test('loads with default week range', async ({ page }) => {
    await expect(page.locator('.segmented-tabs button.is-active')).toHaveText('本周')
    await expect(page.locator('.summary-date-inputs input').first()).toHaveValue(/.+/) // date populated
  })

  test('switches preset to month and updates data', async ({ page }) => {
    await page.click('button:has-text("本月")')
    await expect(page.locator('.segmented-tabs button.is-active')).toHaveText('本月')
    await page.waitForResponse(r => r.url().includes('/api/summary') && r.status() === 200)
  })

  test('shows stats cards', async ({ page }) => {
    await expect(page.locator('.metric-tile').first()).toBeVisible()
  })

  test('expands task to show linked notes', async ({ page }) => {
    const firstTask = page.locator('.summary-task-card summary').first()
    if (await firstTask.isVisible()) {
      await firstTask.click()
      await expect(page.locator('.summary-task-detail').first()).toBeVisible()
    }
  })

  test('navigates to editor from note link', async ({ page }) => {
    const noteLink = page.locator('.summary-task-detail a').first()
    if (await noteLink.isVisible()) {
      await noteLink.click()
      await expect(page).toHaveURL(/\/editor\//)
    }
  })

  test('shows empty state for no completed tasks', async ({ page }) => {
    // Navigate to a far future range
    await page.fill('.summary-date-inputs input').first(), '2099-01-01')
    await page.fill('.summary-date-inputs input').last(), '2099-01-07')
    await page.waitForResponse(r => r.url().includes('/api/summary'))
    await expect(page.locator('.empty-copy')).toBeVisible()
  })

  test('pagination works', async ({ page }) => {
    const nextBtn = page.locator('button:has-text("下一页")')
    if (await nextBtn.isVisible() && await nextBtn.isEnabled()) {
      await nextBtn.click()
      const url = new URL(page.url())
      expect(url.searchParams.get('page')).toBe('2')
    }
  })

  test('refresh preserves URL state', async ({ page }) => {
    await page.fill('.summary-date-inputs input').first(), '2026-06-01')
    await page.fill('.summary-date-inputs input').last(), '2026-06-10')
    await page.waitForResponse(r => r.url().includes('/api/summary'))
    const beforeUrl = page.url()
    await page.reload()
    expect(page.url()).toBe(beforeUrl)
  })

  test('can navigate from sidebar', async ({ page }) => {
    await page.click('.sidebar-link:has-text("每日总结")')
    await expect(page.locator('.page-title')).toHaveText('每日总结')
  })
})
```

- [ ] **Step 2: Run e2e tests**

```bash
npx playwright test daily-summary.spec.ts
```

Expected: tests pass (may need seed data for completed tasks).

- [ ] **Step 3: Commit**

```bash
git add e2e/daily-summary.spec.ts
git commit -m "test: add Playwright e2e tests for daily summary page"
```

---

## Verification Summary

```bash
# Backend
cd backend && go build ./... && go test ./internal/...

# Frontend
cd frontend && npx tsc --noEmit

# E2E (start backend + frontend dev servers first)
npx playwright test daily-summary.spec.ts
```
