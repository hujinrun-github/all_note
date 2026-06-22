# Recurring Tasks Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement recurring task templates with daily/weekly/monthly expansion, per-occurrence completion, today/calendar integration, and summary statistics — for both SQLite and PostgreSQL providers.

**Architecture:** Extend `tasks` table with `execution_type`, add `task_recurrence_rules` + `task_occurrences` tables. Recurrence expansion + state merge happens in a shared `RecurrenceService`. Today and summary services are refactored from old `repository.*` package-level calls to Store dependency injection. All recurring writes go through `Store.Transact` for atomic task+rule insertion.

**Tech Stack:** Go, Gin, database/sql, PostgreSQL (INTEGER[], TIMESTAMPTZ), SQLite (JSON text, INTEGER Unix timestamps)

## Global Constraints

- `execution_type` values: `'single'` (default), `'recurring'`
- `task_occurrences.status`: `'open'`, `'done'`, `'skipped'`
- Maximum date range for occurrence expansion: 370 days
- Summary merge hard limit: 10,000 items with `X-Truncated: true` header
- Recurring templates must NEVER have `planned_date` set (NULL only)
- Recurring templates must NEVER be marked complete via PATCH (return 409)
- `recurring → single` conversion prohibited in v1 (return 409)
- `single → recurring`: blocked if `done=1` (409); allowed if `done=0` (clear `planned_date`)
- Weekly interval anchor: ISO 8601 weeks, `start_date`'s ISO week = week 0
- SQLite JSON arrays: `[1,3,5]` format (no spaces, standard JSON number array)
- Timezone: default from `FLOWSPACE_DEFAULT_TIMEZONE` env, fallback `Asia/Shanghai`
- All queries must filter `execution_type` to prevent recurring templates leaking into single-task views
- Summary date boundaries use server timezone; cross-timezone may have ±1 day drift (documented limitation)
- Overdue tasks do NOT include recurring occurrences in v1

---

## File Map

| File | Responsibility | Change |
|------|---------------|--------|
| `model/task.go` | Task, CreateTaskRequest, UpdateTaskRequest structs | Add `ExecutionType`, occurrence fields, recurrence embed |
| `model/recurrence.go` | RecurrenceRule, TaskOccurrence, RecurrenceConfig | **Create** |
| `model/summary.go` | TaskSummary | Add `ExecutionType`, `OccurrenceDate` |
| `storage/store.go` | Store, TaskFilter, TaskRepository, RecurrenceRepository interfaces | Add `Recurrence()`, `ExecutionType` filter, new interface |
| `storage/contracttest/recurrence_contract_tests.go` | Shared contract tests for both providers | **Create** |
| `storage/postgres/migrations.go` | Migration loader | Register 0003 |
| `db/migrations/postgres/0003_recurring_tasks.sql` | PG DDL | **Create** |
| `storage/postgres/tasks.go` | PG TaskRepository | Add `execution_type` to WHERE, modify `normalizeTaskDefaults` |
| `storage/postgres/recurrence.go` | PG RecurrenceRepository | **Create** |
| `storage/postgres/builder.go` | PG query builder | Add `execution_type` helper |
| `storage/sqlite/tasks.go` | SQLite TaskRepository | Add `withTx`, add `execution_type` WHERE, modify `normalizeTaskDefaults` |
| `storage/sqlite/recurrence.go` | SQLite RecurrenceRepository | **Create** |
| `service/recurrence.go` | RecurrenceService: expand, label, merge | **Create** |
| `service/today.go` | Today data service | Refactor: repository→Store, add occurrence merge |
| `service/summary.go` | Summary data service | Refactor: repository→Store, add occurrence merge+sort |
| `service/tasks.go` | Task CRUD service | Refactor: repository→Store, add Transact for recurring |
| `handler/tasks.go` | Task HTTP handlers | Add CreateTask/UpdateTask recurring logic, occurrence routes |
| `handler/today.go` | Today HTTP handler | Pass Store to service |
| `handler/summary.go` | Summary HTTP handler | Pass Store to service |
| `router/router.go` | Route registration | Add occurrence routes |
| `cmd/server/main.go` | App entry point | Inject Store into services |

---

## Phase 1: Data Model & Backend Foundation

### Task 1: Add migration SQL files

**Files:**
- Create: `backend/db/migrations/postgres/0003_recurring_tasks.sql`
- Modify: `backend/internal/storage/postgres/migrations.go` (auto-discovers, may not need change)
- Create: SQLite DDL (embedded in `backend/internal/storage/sqlite/recurrence.go`)

**Interfaces:**
- Produces: `tasks.execution_type TEXT NOT NULL DEFAULT 'single'`, `task_recurrence_rules` table, `task_occurrences` table, indexes

- [ ] **Step 1: Create PostgreSQL migration file**

```sql
-- backend/db/migrations/postgres/0003_recurring_tasks.sql
ALTER TABLE tasks ADD COLUMN execution_type TEXT NOT NULL DEFAULT 'single'
  CHECK (execution_type IN ('single', 'recurring'));

UPDATE tasks SET execution_type = 'single' WHERE execution_type IS NULL OR execution_type = '';

CREATE TABLE task_recurrence_rules (
  task_id TEXT PRIMARY KEY REFERENCES tasks(id) ON DELETE CASCADE,
  start_date DATE NOT NULL,
  end_date DATE,
  frequency TEXT NOT NULL CHECK (frequency IN ('daily', 'weekly', 'monthly')),
  interval INTEGER NOT NULL DEFAULT 1 CHECK (interval >= 1),
  weekdays INTEGER[] NOT NULL DEFAULT '{}',
  month_days INTEGER[] NOT NULL DEFAULT '{}',
  timezone TEXT NOT NULL,
  enabled BOOLEAN NOT NULL DEFAULT true,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (end_date IS NULL OR end_date >= start_date)
);

CREATE INDEX task_recurrence_rules_enabled_idx
  ON task_recurrence_rules (enabled, start_date, end_date);

CREATE TABLE task_occurrences (
  task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  occurrence_date DATE NOT NULL,
  status TEXT NOT NULL DEFAULT 'open'
    CHECK (status IN ('open', 'done', 'skipped')),
  completed_at TIMESTAMPTZ,
  note TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (task_id, occurrence_date),
  CHECK (
    (status = 'done' AND completed_at IS NOT NULL)
    OR (status <> 'done')
  )
);

CREATE INDEX task_occurrences_date_idx
  ON task_occurrences (occurrence_date, status);

CREATE INDEX task_occurrences_task_date_idx
  ON task_occurrences (task_id, occurrence_date);

CREATE INDEX task_occurrences_completed_at_idx
  ON task_occurrences (completed_at) WHERE completed_at IS NOT NULL;
```

- [ ] **Step 2: Add `completed_at` column to tasks (PG) if not exists**

Check migration `0002_add_completed_at.sql` — if this ran, tasks already has `completed_at`. If not:

```sql
ALTER TABLE tasks ADD COLUMN completed_at TIMESTAMPTZ;
CREATE INDEX tasks_completed_at_idx ON tasks (completed_at) WHERE completed_at IS NOT NULL;
```

- [ ] **Step 3: Verify migration runs**

```bash
cd backend && go test ./internal/storage/postgres/ -run TestMigrations -v -count=1
```

Expected: PASS (0003 applies cleanly after 0001, 0002)

- [ ] **Step 4: Commit**

```bash
git add backend/db/migrations/postgres/0003_recurring_tasks.sql
git commit -m "feat: add recurring tasks migration (PG)"
```

---

### Task 2: Add model types

**Files:**
- Create: `backend/internal/model/recurrence.go`
- Modify: `backend/internal/model/task.go:1-23`
- Modify: `backend/internal/model/summary.go:22-32`

**Interfaces:**
- Produces: `RecurrenceRule`, `TaskOccurrence`, `RecurrenceConfig`; extended `Task` with `ExecutionType`/`OccurrenceDate`/`OccurrenceStatus`/`RecurrenceLabel`; extended `CreateTaskRequest` with `ExecutionType`/`Recurrence`; extended `UpdateTaskRequest` with `Recurrence`/`Enabled`/`EndDate`; extended `TaskSummary` with `ExecutionType`/`OccurrenceDate`

- [ ] **Step 1: Create model/recurrence.go**

```go
// backend/internal/model/recurrence.go
package model

type RecurrenceConfig struct {
	StartDate string `json:"start_date" binding:"required"`
	EndDate   *string `json:"end_date"`
	Frequency  string `json:"frequency" binding:"required"`
	Interval   int    `json:"interval"`
	Weekdays   []int  `json:"weekdays"`
	MonthDays  []int  `json:"month_days"`
	Timezone   string `json:"timezone"`
	Enabled    *bool  `json:"enabled"`
}

type RecurrenceRule struct {
	TaskID    string  `json:"task_id"`
	StartDate string  `json:"start_date"`
	EndDate   *string `json:"end_date"`
	Frequency  string  `json:"frequency"`
	Interval   int     `json:"interval"`
	Weekdays   []int   `json:"weekdays"`
	MonthDays  []int   `json:"month_days"`
	Timezone   string  `json:"timezone"`
	Enabled    bool    `json:"enabled"`
	CreatedAt  int64   `json:"created_at"`
	UpdatedAt  int64   `json:"updated_at"`
}

type TaskOccurrence struct {
	TaskID          string `json:"task_id"`
	OccurrenceDate  string `json:"occurrence_date"`
	Status          string `json:"status"`
	CompletedAt     *int64 `json:"completed_at,omitempty"`
	Note            string `json:"note"`
	Title           string `json:"title,omitempty"`
	Content         string `json:"content,omitempty"`
	ProjectID       *string `json:"project_id,omitempty"`
	Project         string `json:"project,omitempty"`
	RoadmapNodeID   *string `json:"roadmap_node_id,omitempty"`
	RecurrenceLabel string `json:"recurrence_label,omitempty"`
	SortOrder       float64 `json:"sort_order"`
	CreatedAt       int64   `json:"created_at"`
}
```

- [ ] **Step 2: Extend Task struct in model/task.go**

Add fields to `Task` struct (after `CompletedAt` line):

```go
// Add to Task struct:
ExecutionType    string  `json:"execution_type,omitempty"`
OccurrenceDate   *string `json:"occurrence_date,omitempty"`
OccurrenceStatus *string `json:"occurrence_status,omitempty"`
RecurrenceLabel  *string `json:"recurrence_label,omitempty"`
```

Add fields to `CreateTaskRequest` struct:

```go
// Add to CreateTaskRequest:
ExecutionType string            `json:"execution_type"`
Recurrence    *RecurrenceConfig `json:"recurrence"`
```

Add fields to `UpdateTaskRequest` struct:

```go
// Add to UpdateTaskRequest:
ExecutionType *string           `json:"execution_type"`
Recurrence    *RecurrenceConfig `json:"recurrence"`
Enabled       *bool             `json:"enabled"`
EndDate       *string           `json:"end_date"`
```

- [ ] **Step 3: Extend TaskSummary in model/summary.go**

```go
// Add to TaskSummary struct:
ExecutionType  string `json:"execution_type,omitempty"`
OccurrenceDate string `json:"occurrence_date,omitempty"`
```

- [ ] **Step 4: Verify compilation**

```bash
cd backend && go build ./...
```

Expected: compiles (some "unused" warnings OK)

- [ ] **Step 5: Commit**

```bash
git add backend/internal/model/recurrence.go backend/internal/model/task.go backend/internal/model/summary.go
git commit -m "feat: add recurrence model types"
```

---

### Task 3: Add Recurrence() to Store interface + RecurrenceRepository + TaskFilter.ExecutionType

**Files:**
- Modify: `backend/internal/storage/store.go:29-43` (Store interface)
- Modify: `backend/internal/storage/store.go:72-82` (TaskFilter)

**Interfaces:**
- Produces: `Store.Recurrence() RecurrenceRepository`, `TaskFilter.ExecutionType string`, `RecurrenceRepository` interface with all methods

- [ ] **Step 1: Add ExecutionType to TaskFilter**

```go
// In storage/store.go, add to TaskFilter:
type TaskFilter struct {
	Project       string
	Status        string
	Scope         string
	Horizon       string
	ProjectID     string
	PlannedDate   string
	RoadmapNodeID string
	ExecutionType string // "" (default=single), "single", "recurring", "all"
	Page          int
	PageSize      int
}
```

- [ ] **Step 2: Add Recurrence() to Store interface**

```go
// In Store interface, add after Tasks():
Recurrence() RecurrenceRepository
```

- [ ] **Step 3: Add RecurrenceRepository interface after TaskRepository**

```go
type RecurrenceRepository interface {
	UpsertRule(ctx context.Context, rule *model.RecurrenceRule) error
	GetRule(ctx context.Context, taskID string) (*model.RecurrenceRule, error)
	DeleteRule(ctx context.Context, taskID string) error
	ListActiveRules(ctx context.Context, from, to string) ([]model.RecurrenceRule, error)
	ListOccurrences(ctx context.Context, from, to string) ([]model.TaskOccurrence, error)
	GetCompletedOccurrencesByRange(ctx context.Context, from, to int64) ([]model.TaskSummary, error)
	CompleteOccurrence(ctx context.Context, taskID, date string, completedAt int64) (*model.TaskOccurrence, error)
	ReopenOccurrence(ctx context.Context, taskID, date string) (*model.TaskOccurrence, error)
	SkipOccurrence(ctx context.Context, taskID, date string) (*model.TaskOccurrence, error)
	CountOccurrencesByTask(ctx context.Context, taskID string) (int, error)
}
```

- [ ] **Step 4: Verify compilation — will fail (expected)**

```bash
cd backend && go build ./...
```

Expected: FAIL — PG and SQLite Store implementations don't satisfy `Recurrence()` yet. This is correct at this stage.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/storage/store.go
git commit -m "feat: add RecurrenceRepository interface and TaskFilter.ExecutionType"
```

---

### Task 4: Add SQLite withTx helper + fix Create/Delete transactions

**Files:**
- Modify: `backend/internal/storage/sqlite/tasks.go:211-226` (Create)
- Modify: `backend/internal/storage/sqlite/tasks.go` (add Delete if missing, add withTx)

**Interfaces:**
- Produces: SQLite `taskRepository.Create` wrapped in `withTx()`, `taskRepository.Delete` in tx

- [ ] **Step 1: Add withTx helper to sqlite/tasks.go**

```go
// Add at top of sqlite/tasks.go, after imports:
func (r taskRepository) withTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	db, ok := r.db.(*sql.DB)
	if !ok {
		// Already in a transaction, run directly
		return fn(r.db.(*sql.Tx))
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}
```

- [ ] **Step 2: Wrap Create in withTx**

```go
// Replace sqlite/tasks.go:211-226:
func (r taskRepository) Create(ctx context.Context, task *model.Task) error {
	task.ID = newID()
	now := nowUnix()
	task.CreatedAt = now
	task.UpdatedAt = now
	if err := r.normalizeTaskDefaults(ctx, task); err != nil {
		return err
	}
	return r.withTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO tasks (
				id, title, content, project, project_id, due, planned_date, priority, done, status, horizon, scope, sort_order, note_id, roadmap_node_id, execution_type, created_at, updated_at
			)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, task.ID, task.Title, task.Content, task.Project, task.ProjectID, task.Due, task.PlannedDate, task.Priority, task.Done, task.Status, task.Horizon, task.Scope, task.SortOrder, task.NoteID, task.RoadmapNodeID, task.ExecutionType, task.CreatedAt, task.UpdatedAt)
		if err != nil {
			return err
		}
		return deleteTaskSearchIndex(ctx, tx, task.ID)
	})
}
```

- [ ] **Step 3: Verify Update already uses tx (line 233: db.BeginTx) — ensure search index delete is inside tx**

Check `sqlite/tasks.go:228-310` — the Update method already opens `db.BeginTx`. Verify `deleteTaskSearchIndex`/`upsertTaskSearchIndex` are called inside the tx. If not, move them inside.

- [ ] **Step 4: Run SQLite tests**

```bash
cd backend && go test ./internal/storage/sqlite/ -run TestTask -v -count=1
```

Expected: PASS (Create now runs in tx, behavior unchanged)

- [ ] **Step 5: Commit**

```bash
git add backend/internal/storage/sqlite/tasks.go
git commit -m "fix: add withTx to SQLite task Create for atomic recurring writes"
```

---

### Task 5: Modify three normalizeTaskDefaults for execution_type

**Files:**
- Modify: `backend/internal/repository/tasks.go:684-710`
- Modify: `backend/internal/storage/postgres/tasks.go:466-500`
- Modify: `backend/internal/storage/sqlite/tasks.go:471-505`

**Interfaces:**
- Consumes: `task.ExecutionType` (added to Task struct in Task 2)
- Produces: recurring templates get `planned_date = NULL` (skip default assignment)

- [ ] **Step 1: Modify repository/tasks.go normalizeTaskDefaults**

In the `if t.PlannedDate == nil {` block (line 703), add guard at top:

```go
func normalizeTaskDefaults(t *model.Task) {
	if strings.TrimSpace(t.Title) != "" {
		t.Title = strings.TrimSpace(t.Title)
	}
	if t.Scope == "" {
		t.Scope = "daily"
	}
	t.Scope = normalizeScope(t.Scope)
	t.Horizon = normalizeHorizon(t.Horizon)
	if t.Horizon == "long" && t.Scope == "daily" {
		t.Scope = "yearly"
	}
	t.Status = normalizeTaskStatus(t.Status)
	if t.Status == "done" {
		t.Done = 1
	}
	if t.Done == 1 {
		t.Status = "done"
	}
	// Recurring templates never get planned_date
	if t.ExecutionType == "recurring" {
		return
	}
	if t.PlannedDate == nil {
		planned := time.Now().Format("2006-01-02")
		if t.Due != nil {
			planned = time.Unix(*t.Due, 0).Format("2006-01-02")
		}
		t.PlannedDate = &planned
	}
	// ... rest unchanged ...
}
```

- [ ] **Step 2: Apply same change to postgres/tasks.go:466**

Insert before `if task.PlannedDate == nil {` (line 481):

```go
	// Recurring templates never get planned_date
	if task.ExecutionType == "recurring" {
		return nil
	}
```

- [ ] **Step 3: Apply same change to sqlite/tasks.go:471**

Insert before `if task.PlannedDate == nil {` (line 486):

```go
	// Recurring templates never get planned_date
	if task.ExecutionType == "recurring" {
		return nil
	}
```

- [ ] **Step 4: Run all task tests**

```bash
cd backend && go test ./internal/storage/... -run TestTask -v -count=1
```

Expected: PASS — existing tests pass, recurring template won't get planned_date

- [ ] **Step 5: Commit**

```bash
git add backend/internal/repository/tasks.go backend/internal/storage/postgres/tasks.go backend/internal/storage/sqlite/tasks.go
git commit -m "fix: skip planned_date default for recurring templates in all three normalizeTaskDefaults"
```

---

### Task 6: Add execution_type WHERE filter to all task queries

**Files:**
- Modify: `backend/internal/storage/postgres/builder.go` (postgresTaskWhere)
- Modify: `backend/internal/storage/sqlite/tasks.go` (sqliteTaskWhere)
- Modify: `backend/internal/repository/tasks.go` (buildTaskQuery)

**Interfaces:**
- Consumes: `TaskFilter.ExecutionType`

- [ ] **Step 1: Add helper function in storage/store.go**

```go
// ExecutionTypeFilter returns the WHERE clause and args for execution_type filtering.
// emptyStr means "single" (default for backward compat).
func ExecutionTypeFilter(execType string) (string, []any) {
	switch execType {
	case "recurring":
		return "t.execution_type = 'recurring'", nil
	case "all":
		return "", nil // no filter
	default: // "" or "single"
		return "(t.execution_type IS NULL OR t.execution_type = 'single')", nil
	}
}
```

- [ ] **Step 2: Add execution_type to postgresTaskWhere**

Find `backend/internal/storage/postgres/builder.go`, locate `postgresTaskWhere` function. Add after the existing filter building:

```go
if filter.ExecutionType != "all" {
	if filter.ExecutionType == "recurring" {
		where = append(where, "t.execution_type = "+pgPlaceholder(len(args)+1))
		args = append(args, "recurring")
	} else {
		// default: single only
		where = append(where, "(t.execution_type IS NULL OR t.execution_type = "+pgPlaceholder(len(args)+1)+")")
		args = append(args, "single")
	}
}
```

- [ ] **Step 3: Add execution_type to sqliteTaskWhere**

Find `sqliteTaskWhere` in `backend/internal/storage/sqlite/tasks.go`. Add same logic with `?` placeholders:

```go
if filter.ExecutionType != "all" {
	if filter.ExecutionType == "recurring" {
		where = append(where, "t.execution_type = ?")
		args = append(args, "recurring")
	} else {
		where = append(where, "(t.execution_type IS NULL OR t.execution_type = ?)")
		args = append(args, "single")
	}
}
```

- [ ] **Step 4: Add execution_type to old repository buildTaskQuery**

Find `buildTaskQuery` in `backend/internal/repository/tasks.go`. Add similar logic.

- [ ] **Step 5: Run task list tests**

```bash
cd backend && go test ./internal/storage/... -run TestTask -v -count=1
```

Expected: PASS — existing tasks still returned (all have `execution_type = 'single'` or NULL)

- [ ] **Step 6: Commit**

```bash
git add backend/internal/storage/store.go backend/internal/storage/postgres/builder.go backend/internal/storage/sqlite/tasks.go backend/internal/repository/tasks.go
git commit -m "feat: add execution_type filter to all task queries"
```

---

### Task 7: Implement recurrence expansion engine

**Files:**
- Create: `backend/internal/service/recurrence.go`

**Interfaces:**
- Produces: `RecurrenceService` with `ExpandRuleOccurrences()`, `GenerateRecurrenceLabel()`, `ExpandWithStatus()`, `ValidateRecurrenceConfig()`

- [ ] **Step 1: Write the test file**

```go
// backend/internal/service/recurrence_test.go
package service

import (
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/model"
)

func TestExpandDailyRuleIncludesStartAndEnd(t *testing.T) {
	rule := &model.RecurrenceRule{
		StartDate: "2026-06-21",
		EndDate:   strPtr("2026-06-25"),
		Frequency:  "daily",
		Interval:   1,
		Timezone:   "Asia/Shanghai",
		Enabled:    true,
	}
	dates := ExpandRuleOccurrences(rule, "2026-06-20", "2026-06-26")
	if len(dates) != 5 {
		t.Fatalf("expected 5 dates, got %d: %v", len(dates), dates)
	}
	if dates[0] != "2026-06-21" || dates[4] != "2026-06-25" {
		t.Errorf("unexpected range: %v", dates)
	}
}

func TestExpandEveryTwoDaysUsesStartDateAsAnchor(t *testing.T) {
	rule := &model.RecurrenceRule{
		StartDate: "2026-06-21",
		Frequency:  "daily",
		Interval:   2,
		Timezone:   "Asia/Shanghai",
		Enabled:    true,
	}
	dates := ExpandRuleOccurrences(rule, "2026-06-20", "2026-06-30")
	expected := []string{"2026-06-21", "2026-06-23", "2026-06-25", "2026-06-27", "2026-06-29"}
	if len(dates) != len(expected) {
		t.Fatalf("expected %d dates, got %d: %v", len(expected), len(dates), dates)
	}
	for i, d := range expected {
		if dates[i] != d {
			t.Errorf("date[%d]: expected %s, got %s", i, d, dates[i])
		}
	}
}

func TestExpandWeeklyRuleUsesISOWeekdays(t *testing.T) {
	rule := &model.RecurrenceRule{
		StartDate: "2026-06-22", // Monday
		Frequency:  "weekly",
		Interval:   1,
		Weekdays:   []int{1, 3, 5}, // Mon, Wed, Fri
		Timezone:   "Asia/Shanghai",
		Enabled:    true,
	}
	dates := ExpandRuleOccurrences(rule, "2026-06-22", "2026-06-28")
	expected := []string{"2026-06-22", "2026-06-24", "2026-06-26"}
	if len(dates) != len(expected) {
		t.Fatalf("expected %d dates, got %d: %v", len(expected), len(dates), dates)
	}
}

func TestExpandWeeklyInterval2AnchorsAtStartDateISOWeek(t *testing.T) {
	// start_date=2026-06-20 (Saturday), weekdays=[1] (Monday), interval=2
	rule := &model.RecurrenceRule{
		StartDate: "2026-06-20",
		Frequency:  "weekly",
		Interval:   2,
		Weekdays:   []int{1},
		Timezone:   "Asia/Shanghai",
		Enabled:    true,
	}
	dates := ExpandRuleOccurrences(rule, "2026-06-15", "2026-07-15")
	// 2026-06-20 is in ISO W25 (Mon=06-15). Week 0: Mon 06-15 (before start, skip).
	// Week 2 (W27): Mon 06-29. Week 4 (W29): Mon 07-13.
	if len(dates) < 2 {
		t.Fatalf("expected at least 2 dates, got %d: %v", len(dates), dates)
	}
	if dates[0] != "2026-06-29" {
		t.Errorf("first Mon after start in week 2 should be 2026-06-29, got %s", dates[0])
	}
	if dates[1] != "2026-07-13" {
		t.Errorf("second Mon should be 2026-07-13, got %s", dates[1])
	}
}

func TestExpandMonthlyRuleSkipsMissingMonthDay(t *testing.T) {
	rule := &model.RecurrenceRule{
		StartDate: "2026-01-01",
		Frequency:  "monthly",
		Interval:   1,
		MonthDays:  []int{31},
		Timezone:   "Asia/Shanghai",
		Enabled:    true,
	}
	dates := ExpandRuleOccurrences(rule, "2026-01-01", "2026-06-30")
	// Jan 31, Mar 31, May 31 — Feb/Apr/Jun have no 31st
	expected := []string{"2026-01-31", "2026-03-31", "2026-05-31"}
	if len(dates) != len(expected) {
		t.Fatalf("expected %d dates, got %d: %v", len(expected), len(dates), dates)
	}
}

func TestExpandMonthlyLeapYearFeb29(t *testing.T) {
	rule := &model.RecurrenceRule{
		StartDate: "2028-01-01", // 2028 is leap year
		Frequency:  "monthly",
		Interval:   1,
		MonthDays:  []int{29},
		Timezone:   "Asia/Shanghai",
		Enabled:    true,
	}
	dates := ExpandRuleOccurrences(rule, "2028-02-01", "2028-02-29")
	if len(dates) != 1 || dates[0] != "2028-02-29" {
		t.Errorf("leap year Feb 29 should appear, got %v", dates)
	}
}

func TestExpandRuleRespectsEndDate(t *testing.T) {
	rule := &model.RecurrenceRule{
		StartDate: "2026-06-01",
		EndDate:   strPtr("2026-06-10"),
		Frequency:  "daily",
		Interval:   1,
		Timezone:   "Asia/Shanghai",
		Enabled:    true,
	}
	dates := ExpandRuleOccurrences(rule, "2026-06-01", "2026-06-30")
	if len(dates) != 10 {
		t.Fatalf("expected 10 dates, got %d", len(dates))
	}
}

func TestExpandRuleReturnsEmptyWhenDisabled(t *testing.T) {
	rule := &model.RecurrenceRule{
		StartDate: "2026-06-01",
		Frequency:  "daily",
		Interval:   1,
		Timezone:   "Asia/Shanghai",
		Enabled:    false,
	}
	dates := ExpandRuleOccurrences(rule, "2026-06-01", "2026-06-10")
	if len(dates) != 0 {
		t.Errorf("disabled rule should return empty, got %v", dates)
	}
}

func TestGenerateRecurrenceLabel(t *testing.T) {
	tests := []struct {
		rule     model.RecurrenceRule
		expected string
	}{
		{model.RecurrenceRule{Frequency: "daily", Interval: 1, EndDate: strPtr("2026-08-21")}, "每天"},
		{model.RecurrenceRule{Frequency: "daily", Interval: 1}, "每天（长期）"},
		{model.RecurrenceRule{Frequency: "daily", Interval: 2}, "每 2 天"},
		{model.RecurrenceRule{Frequency: "weekly", Interval: 1, Weekdays: []int{1, 3, 5}}, "每周一三五"},
		{model.RecurrenceRule{Frequency: "weekly", Interval: 1, Weekdays: []int{1, 2, 3, 4, 5}}, "每周一至五"},
		{model.RecurrenceRule{Frequency: "weekly", Interval: 2, Weekdays: []int{1}}, "隔周周一"},
		{model.RecurrenceRule{Frequency: "monthly", Interval: 1, MonthDays: []int{1, 15}}, "每月 1/15 号"},
		{model.RecurrenceRule{Frequency: "monthly", Interval: 2, MonthDays: []int{1}}, "每 2 个月 1 号"},
	}
	for _, tc := range tests {
		got := GenerateRecurrenceLabel(&tc.rule)
		if got != tc.expected {
			t.Errorf("GenerateRecurrenceLabel(%+v): expected %q, got %q", tc.rule, tc.expected, got)
		}
	}
}

func strPtr(s string) *string { return &s }
```

- [ ] **Step 2: Run tests to see them fail**

```bash
cd backend && go test ./internal/service/ -run TestExpand -v -count=1
```

Expected: FAIL — `ExpandRuleOccurrences` not defined

- [ ] **Step 3: Implement recurrence.go**

```go
// backend/internal/service/recurrence.go
package service

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/model"
)

type RecurrenceService struct {
	store RecurrenceStore
}

// RecurrenceStore is the subset of Store needed by RecurrenceService.
type RecurrenceStore interface {
	// ... defined via storage.Store in production ...
}

var weekdayNames = map[int]string{1: "一", 2: "二", 3: "三", 4: "四", 5: "五", 6: "六", 7: "日"}

// ExpandRuleOccurrences returns all occurrence dates in [from, to] for a given rule.
// Uses rule.timezone for date calculations. Returns empty if rule is disabled.
func ExpandRuleOccurrences(rule *model.RecurrenceRule, from, to string) []string {
	if !rule.Enabled {
		return nil
	}
	loc, err := time.LoadLocation(rule.Timezone)
	if err != nil {
		loc = time.Local
	}
	start, _ := time.ParseInLocation("2006-01-02", rule.StartDate, loc)
	fromDate, _ := time.ParseInLocation("2006-01-02", from, loc)
	toDate, _ := time.ParseInLocation("2006-01-02", to, loc)
	var endDate time.Time
	if rule.EndDate != nil {
		endDate, _ = time.ParseInLocation("2006-01-02", *rule.EndDate, loc)
	} else {
		endDate = toDate
	}
	if start.After(endDate) {
		return nil
	}
	if fromDate.Before(start) {
		fromDate = start
	}
	if toDate.After(endDate) {
		toDate = endDate
	}
	if fromDate.After(toDate) {
		return nil
	}

	var dates []string
	switch rule.Frequency {
	case "daily":
		dates = expandDaily(start, fromDate, toDate, rule.Interval)
	case "weekly":
		dates = expandWeekly(start, fromDate, toDate, rule.Interval, rule.Weekdays, loc)
	case "monthly":
		dates = expandMonthly(start, fromDate, toDate, rule.Interval, rule.MonthDays)
	}
	return dates
}

func expandDaily(start, from, to time.Time, interval int) []string {
	startDay := start
	// Walk forward from start by interval until >= from
	current := startDay
	for current.Before(from) {
		current = current.AddDate(0, 0, interval)
	}
	var dates []string
	for !current.After(to) {
		dates = append(dates, current.Format("2006-01-02"))
		current = current.AddDate(0, 0, interval)
	}
	return dates
}

func expandWeekly(start, from, to time.Time, interval int, weekdays []int, loc *time.Location) []string {
	if len(weekdays) == 0 {
		return nil
	}
	// Find the Monday of start's ISO week (week 0)
	startISOYear, startISOWeek := start.ISOWeek()
	week0Monday := isoWeekMonday(startISOYear, startISOWeek, loc)

	weekdaySet := make(map[time.Weekday]bool)
	for _, d := range weekdays {
		weekdaySet[time.Weekday(d%7)] = true // Sunday=0 in Go, ISO Sunday=7→0
	}

	var dates []string
	for w := 0; ; w += interval {
		weekStart := week0Monday.AddDate(0, 0, w*7)
		if weekStart.After(to) {
			break
		}
		for _, d := range weekdays {
			goWd := time.Weekday(d % 7)
			date := weekStart.AddDate(0, 0, int(goWd)-int(time.Monday))
			if date.Before(from) || date.Before(start) || date.After(to) {
				continue
			}
			dates = append(dates, date.Format("2006-01-02"))
		}
	}
	return dates
}

func isoWeekMonday(year, week int, loc *time.Location) time.Time {
	// Jan 4 is always in ISO week 1
	jan4 := time.Date(year, 1, 4, 0, 0, 0, 0, loc)
	_, jan4Week := jan4.ISOWeek()
	daysToMonday := int(time.Monday - jan4.Weekday())
	if jan4.Weekday() == time.Sunday {
		daysToMonday = -6
	}
	week1Monday := jan4.AddDate(0, 0, daysToMonday)
	return week1Monday.AddDate(0, 0, (week-1)*7)
}

func expandMonthly(start, from, to time.Time, interval int, monthDays []int) []string {
	if len(monthDays) == 0 {
		return nil
	}
	var dates []string
	current := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, start.Location())
	for !current.After(to) {
		for _, day := range monthDays {
			lastDay := daysInMonth(current.Year(), int(current.Month()))
			if day > lastDay {
				continue
			}
			date := time.Date(current.Year(), current.Month(), day, 0, 0, 0, 0, current.Location())
			if date.Before(from) || date.Before(start) || date.After(to) {
				continue
			}
			dates = append(dates, date.Format("2006-01-02"))
		}
		current = current.AddDate(0, interval, 0)
	}
	return dates
}

func daysInMonth(year, month int) int {
	return time.Date(year, time.Month(month)+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// GenerateRecurrenceLabel produces Chinese label like "每天", "每周一三五", "每月 1/15 号".
func GenerateRecurrenceLabel(rule *model.RecurrenceRule) string {
	var parts []string
	if rule.Interval > 1 {
		parts = append(parts, "每 "+strconv.Itoa(rule.Interval))
		switch rule.Frequency {
		case "daily":
			parts = append(parts, " 天")
		case "weekly":
			parts = append(parts, " 周")
		case "monthly":
			parts = append(parts, " 个月")
		}
	} else {
		switch rule.Frequency {
		case "daily":
			parts = append(parts, "每天")
		case "weekly":
			parts = append(parts, "每周")
		case "monthly":
			parts = append(parts, "每月")
		}
	}
	switch rule.Frequency {
	case "weekly":
		parts = append(parts, formatWeekdays(rule.Weekdays))
	case "monthly":
		parts = append(parts, formatMonthDays(rule.MonthDays))
	}
	if rule.EndDate == nil {
		parts = append(parts, "（长期）")
	}
	return strings.Join(parts, "")
}

func formatWeekdays(days []int) string {
	if len(days) == 0 {
		return ""
	}
	if len(days) == 1 {
		return "周" + weekdayNames[days[0]]
	}
	// Check if consecutive
	sort.Ints(days)
	isConsecutive := true
	for i := 1; i < len(days); i++ {
		if days[i]-days[i-1] != 1 {
			isConsecutive = false
			break
		}
	}
	if isConsecutive && len(days) >= 3 {
		return weekdayNames[days[0]] + "至" + weekdayNames[days[len(days)-1]]
	}
	names := make([]string, len(days))
	for i, d := range days {
		names[i] = weekdayNames[d]
	}
	return strings.Join(names, "")
}

func formatMonthDays(days []int) string {
	if len(days) == 0 {
		return ""
	}
	sort.Ints(days)
	if len(days) == 1 {
		return strconv.Itoa(days[0]) + " 号"
	}
	parts := make([]string, len(days))
	for i, d := range days {
		parts[i] = strconv.Itoa(d)
	}
	return strings.Join(parts, "/") + " 号"
}

// ValidateRecurrenceConfig validates a RecurrenceConfig from API input.
func ValidateRecurrenceConfig(rc *model.RecurrenceConfig) error {
	if rc == nil {
		return fmt.Errorf("recurrence config is required")
	}
	if rc.StartDate == "" {
		return fmt.Errorf("请选择开始日期")
	}
	if rc.Frequency != "daily" && rc.Frequency != "weekly" && rc.Frequency != "monthly" {
		return fmt.Errorf("invalid frequency: %s", rc.Frequency)
	}
	if rc.Interval < 1 {
		rc.Interval = 1
	}
	if rc.Frequency == "weekly" && len(rc.Weekdays) == 0 {
		return fmt.Errorf("请选择每周执行的日期")
	}
	if rc.Frequency == "monthly" && len(rc.MonthDays) == 0 {
		return fmt.Errorf("请选择每月执行的日期")
	}
	for _, d := range rc.Weekdays {
		if d < 1 || d > 7 {
			return fmt.Errorf("invalid weekday: %d", d)
		}
	}
	for _, d := range rc.MonthDays {
		if d < 1 || d > 31 {
			return fmt.Errorf("invalid month day: %d", d)
		}
	}
	if rc.EndDate != nil && *rc.EndDate != "" && *rc.EndDate < rc.StartDate {
		return fmt.Errorf("结束日期不能早于开始日期")
	}
	if rc.Timezone == "" {
		rc.Timezone = defaultTimezone()
	}
	return nil
}

func defaultTimezone() string {
	// Read from FLOWSPACE_DEFAULT_TIMEZONE env, fallback Asia/Shanghai
	if tz := ""; tz != "" { // TODO: os.Getenv
		return tz
	}
	return "Asia/Shanghai"
}
```

- [ ] **Step 4: Run tests**

```bash
cd backend && go test ./internal/service/ -run "TestExpand|TestGenerate" -v -count=1
```

Expected: PASS — all expansion and label tests pass

- [ ] **Step 5: Commit**

```bash
git add backend/internal/service/recurrence.go backend/internal/service/recurrence_test.go
git commit -m "feat: implement recurrence expansion engine with label generation"
```

---

### Task 8: Implement PostgreSQL RecurrenceRepository

**Files:**
- Create: `backend/internal/storage/postgres/recurrence.go`

**Interfaces:**
- Consumes: `postgresRunner` interface (db/tx), `storage.RecurrenceRepository`
- Produces: Full PG RecurrenceRepository implementation

- [ ] **Step 1: Create recurrence.go with all methods**

```go
// backend/internal/storage/postgres/recurrence.go
package postgres

import (
	"context"
	"database/sql"
	"time"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/lib/pq"
)

type recurrenceRepository struct {
	db postgresRunner
}

func (r recurrenceRepository) UpsertRule(ctx context.Context, rule *model.RecurrenceRule) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO task_recurrence_rules (task_id, start_date, end_date, frequency, interval, weekdays, month_days, timezone, enabled)
		VALUES ($1, $2::date, $3::date, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (task_id) DO UPDATE SET
			start_date = EXCLUDED.start_date,
			end_date = EXCLUDED.end_date,
			frequency = EXCLUDED.frequency,
			interval = EXCLUDED.interval,
			weekdays = EXCLUDED.weekdays,
			month_days = EXCLUDED.month_days,
			timezone = EXCLUDED.timezone,
			enabled = EXCLUDED.enabled,
			updated_at = now()
	`, rule.TaskID, rule.StartDate, rule.EndDate, rule.Frequency, rule.Interval,
		pq.Array(rule.Weekdays), pq.Array(rule.MonthDays), rule.Timezone, rule.Enabled)
	return err
}

func (r recurrenceRepository) GetRule(ctx context.Context, taskID string) (*model.RecurrenceRule, error) {
	rule := &model.RecurrenceRule{}
	var endDate sql.NullString
	var weekdays, monthDays []int64
	err := r.db.QueryRowContext(ctx, `
		SELECT task_id, start_date, end_date, frequency, interval, weekdays, month_days, timezone, enabled,
			EXTRACT(EPOCH FROM created_at)::bigint, EXTRACT(EPOCH FROM updated_at)::bigint
		FROM task_recurrence_rules WHERE task_id = $1
	`, taskID).Scan(&rule.TaskID, &rule.StartDate, &endDate, &rule.Frequency, &rule.Interval,
		pq.Array(&weekdays), pq.Array(&monthDays), &rule.Timezone, &rule.Enabled,
		&rule.CreatedAt, &rule.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if endDate.Valid {
		rule.EndDate = &endDate.String
	}
	rule.Weekdays = make([]int, len(weekdays))
	for i, v := range weekdays {
		rule.Weekdays[i] = int(v)
	}
	rule.MonthDays = make([]int, len(monthDays))
	for i, v := range monthDays {
		rule.MonthDays[i] = int(v)
	}
	return rule, nil
}

func (r recurrenceRepository) DeleteRule(ctx context.Context, taskID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM task_recurrence_rules WHERE task_id = $1`, taskID)
	return err
}

func (r recurrenceRepository) ListActiveRules(ctx context.Context, from, to string) ([]model.RecurrenceRule, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT task_id, start_date, end_date, frequency, interval, weekdays, month_days, timezone, enabled,
			EXTRACT(EPOCH FROM created_at)::bigint, EXTRACT(EPOCH FROM updated_at)::bigint
		FROM task_recurrence_rules
		WHERE enabled = true
			AND start_date <= $2::date
			AND (end_date IS NULL OR end_date >= $1::date)
	`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRecurrenceRules(rows)
}

func (r recurrenceRepository) ListOccurrences(ctx context.Context, from, to string) ([]model.TaskOccurrence, error) {
	// This method leaves expansion to the Service layer.
	// Repository just returns raw occurrence rows + task metadata.
	rows, err := r.db.QueryContext(ctx, `
		SELECT o.task_id, o.occurrence_date, o.status, o.completed_at, o.note,
			t.title, COALESCE(t.content, ''), t.project_id, COALESCE(p.name, t.project),
			t.roadmap_node_id, t.sort_order,
			EXTRACT(EPOCH FROM o.created_at)::bigint
		FROM task_occurrences o
		JOIN tasks t ON t.id = o.task_id
		LEFT JOIN task_projects p ON p.id = t.project_id
		WHERE o.occurrence_date >= $1::date AND o.occurrence_date <= $2::date
		ORDER BY o.occurrence_date, t.sort_order
	`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanOccurrences(rows)
}

func (r recurrenceRepository) GetCompletedOccurrencesByRange(ctx context.Context, from, to int64) ([]model.TaskSummary, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT o.task_id AS id, t.title, o.completed_at,
			t.project_id, p.name AS project_name, p.type AS project_type,
			t.due, o.occurrence_date
		FROM task_occurrences o
		JOIN tasks t ON t.id = o.task_id
		LEFT JOIN task_projects p ON p.id = t.project_id
		WHERE o.completed_at IS NOT NULL
			AND o.completed_at >= to_timestamp($1)
			AND o.completed_at < to_timestamp($2)
		ORDER BY o.completed_at DESC
	`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var summaries []model.TaskSummary
	for rows.Next() {
		var s model.TaskSummary
		var projectID, projectName, projectType sql.NullString
		var due sql.NullInt64
		var completedAt int64
		var occurrenceDate string
		if err := rows.Scan(&s.ID, &s.Title, &completedAt, &projectID, &projectName, &projectType, &due, &occurrenceDate); err != nil {
			return nil, err
		}
		s.CompletedAt = &completedAt
		s.ExecutionType = "recurring"
		s.OccurrenceDate = occurrenceDate
		if projectID.Valid {
			s.Project = &model.TaskProject{ID: projectID.String, Name: projectName.String, Type: projectType.String}
		}
		if due.Valid {
			s.Due = &due.Int64
		}
		s.Done = 1
		summaries = append(summaries, s)
	}
	return summaries, nil
}

func (r recurrenceRepository) CompleteOccurrence(ctx context.Context, taskID, date string, completedAt int64) (*model.TaskOccurrence, error) {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO task_occurrences (task_id, occurrence_date, status, completed_at)
		VALUES ($1, $2::date, 'done', to_timestamp($3))
		ON CONFLICT (task_id, occurrence_date) DO UPDATE SET
			status = 'done', completed_at = to_timestamp($3), updated_at = now()
	`, taskID, date, completedAt)
	if err != nil {
		return nil, err
	}
	return r.getOccurrence(ctx, taskID, date)
}

func (r recurrenceRepository) ReopenOccurrence(ctx context.Context, taskID, date string) (*model.TaskOccurrence, error) {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO task_occurrences (task_id, occurrence_date, status, completed_at)
		VALUES ($1, $2::date, 'open', NULL)
		ON CONFLICT (task_id, occurrence_date) DO UPDATE SET
			status = 'open', completed_at = NULL, updated_at = now()
	`, taskID, date)
	if err != nil {
		return nil, err
	}
	return r.getOccurrence(ctx, taskID, date)
}

func (r recurrenceRepository) SkipOccurrence(ctx context.Context, taskID, date string) (*model.TaskOccurrence, error) {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO task_occurrences (task_id, occurrence_date, status, completed_at)
		VALUES ($1, $2::date, 'skipped', NULL)
		ON CONFLICT (task_id, occurrence_date) DO UPDATE SET
			status = 'skipped', completed_at = NULL, updated_at = now()
	`, taskID, date)
	if err != nil {
		return nil, err
	}
	return r.getOccurrence(ctx, taskID, date)
}

func (r recurrenceRepository) CountOccurrencesByTask(ctx context.Context, taskID string) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_occurrences WHERE task_id = $1`, taskID).Scan(&count)
	return count, err
}

func (r recurrenceRepository) getOccurrence(ctx context.Context, taskID, date string) (*model.TaskOccurrence, error) {
	o := &model.TaskOccurrence{}
	var completedAt sql.NullInt64
	err := r.db.QueryRowContext(ctx, `
		SELECT task_id, occurrence_date, status,
			EXTRACT(EPOCH FROM completed_at)::bigint, note,
			EXTRACT(EPOCH FROM created_at)::bigint
		FROM task_occurrences WHERE task_id = $1 AND occurrence_date = $2::date
	`, taskID, date).Scan(&o.TaskID, &o.OccurrenceDate, &o.Status, &completedAt, &o.Note, &o.CreatedAt)
	if err != nil {
		return nil, err
	}
	if completedAt.Valid {
		o.CompletedAt = &completedAt.Int64
	}
	return o, nil
}

func scanRecurrenceRules(rows *sql.Rows) ([]model.RecurrenceRule, error) {
	var rules []model.RecurrenceRule
	for rows.Next() {
		var r model.RecurrenceRule
		var endDate sql.NullString
		var weekdays, monthDays []int64
		if err := rows.Scan(&r.TaskID, &r.StartDate, &endDate, &r.Frequency, &r.Interval,
			pq.Array(&weekdays), pq.Array(&monthDays), &r.Timezone, &r.Enabled,
			&r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		if endDate.Valid {
			r.EndDate = &endDate.String
		}
		r.Weekdays = make([]int, len(weekdays))
		for i, v := range weekdays {
			r.Weekdays[i] = int(v)
		}
		r.MonthDays = make([]int, len(monthDays))
		for i, v := range monthDays {
			r.MonthDays[i] = int(v)
		}
		rules = append(rules, r)
	}
	return rules, nil
}

func scanOccurrences(rows *sql.Rows) ([]model.TaskOccurrence, error) {
	var occurrences []model.TaskOccurrence
	for rows.Next() {
		var o model.TaskOccurrence
		var completedAt sql.NullInt64
		var projectID, project sql.NullString
		var roadmapNodeID sql.NullString
		if err := rows.Scan(&o.TaskID, &o.OccurrenceDate, &o.Status, &completedAt, &o.Note,
			&o.Title, &o.Content, &projectID, &project,
			&roadmapNodeID, &o.SortOrder, &o.CreatedAt); err != nil {
			return nil, err
		}
		if completedAt.Valid {
			o.CompletedAt = &completedAt.Int64
		}
		if projectID.Valid {
			o.ProjectID = &projectID.String
		}
		if project.Valid {
			o.Project = project.String
		}
		if roadmapNodeID.Valid {
			o.RoadmapNodeID = &roadmapNodeID.String
		}
		occurrences = append(occurrences, o)
	}
	return occurrences, nil
}

// Ensure interface compliance
var _ storage.RecurrenceRepository = (*recurrenceRepository)(nil)
```

- [ ] **Step 2: Wire Recurrence() into PG Store**

In the PG store struct (find in postgres package), add recurrence field and method:

```go
// In the store struct, add:
recurrence recurrenceRepository

// Add method:
func (s *store) Recurrence() storage.RecurrenceRepository {
	return &s.recurrence
}
```

And in the store constructor, initialize `recurrence: recurrenceRepository{db: s.db}`.

- [ ] **Step 3: Run contract tests stub**

```bash
cd backend && go build ./internal/storage/postgres/...
```

Expected: compiles

- [ ] **Step 4: Commit**

```bash
git add backend/internal/storage/postgres/recurrence.go backend/internal/storage/postgres/store.go
git commit -m "feat: implement PostgreSQL RecurrenceRepository"
```

---

### Task 9: Implement SQLite RecurrenceRepository

**Files:**
- Create: `backend/internal/storage/sqlite/recurrence.go`

**Interfaces:**
- Consumes: `sqliteRunner` interface
- Produces: Full SQLite RecurrenceRepository implementation

- [ ] **Step 1: Create recurrence.go with SQL DDL**

```go
// backend/internal/storage/sqlite/recurrence.go
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

type recurrenceRepository struct {
	db sqliteRunner
}

const recurrenceSchemaSQL = `
CREATE TABLE IF NOT EXISTS task_recurrence_rules (
	task_id TEXT PRIMARY KEY REFERENCES tasks(id) ON DELETE CASCADE,
	start_date TEXT NOT NULL,
	end_date TEXT,
	frequency TEXT NOT NULL CHECK (frequency IN ('daily','weekly','monthly')),
	interval INTEGER NOT NULL DEFAULT 1 CHECK (interval >= 1),
	weekdays TEXT NOT NULL DEFAULT '[]',
	month_days TEXT NOT NULL DEFAULT '[]',
	timezone TEXT NOT NULL,
	enabled INTEGER NOT NULL DEFAULT 1,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS task_occurrences (
	task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
	occurrence_date TEXT NOT NULL,
	status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open','done','skipped')),
	completed_at INTEGER,
	note TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	PRIMARY KEY (task_id, occurrence_date)
);

CREATE INDEX IF NOT EXISTS idx_occurrences_date ON task_occurrences (occurrence_date, status);
CREATE INDEX IF NOT EXISTS idx_occurrences_task_date ON task_occurrences (task_id, occurrence_date);
CREATE INDEX IF NOT EXISTS idx_occurrences_completed ON task_occurrences (completed_at) WHERE completed_at IS NOT NULL;
`

func ensureRecurrenceSchema(db sqliteRunner) error {
	_, err := db.ExecContext(context.Background(), recurrenceSchemaSQL)
	return err
}

// ... implement all methods matching storage.RecurrenceRepository ...
// (CompleteOccurrence, ReopenOccurrence, SkipOccurrence, GetRule, DeleteRule,
//  ListActiveRules, ListOccurrences, GetCompletedOccurrencesByRange, CountOccurrencesByTask)
// All methods use ? placeholders. weekdays/month_days stored as JSON [1,3,5].
// completed_at stored as INTEGER (Unix timestamp). timezone stored as TEXT.
// See postgres/recurrence.go for method signatures and logic — adapt SQL accordingly.
```

The full SQLite implementation mirrors PG but:
- Uses `?` placeholders instead of `$1, $2`
- `weekdays`/`month_days`: `json.Marshal`/`json.Unmarshal` for `[]int` ↔ `TEXT`
- `completed_at`: direct `int64` (no `to_timestamp()`)
- `enabled`: `INTEGER` (0/1) instead of `BOOLEAN`
- Date strings: stored as `TEXT 'YYYY-MM-DD'`, compared lexicographically

- [ ] **Step 2: Wire Recurrence() into SQLite Store**

Same pattern as PG: add `recurrence recurrenceRepository` to store struct, add `Recurrence()` method, initialize in constructor.

- [ ] **Step 3: Verify compilation**

```bash
cd backend && go build ./internal/storage/sqlite/...
```

Expected: compiles

- [ ] **Step 4: Commit**

```bash
git add backend/internal/storage/sqlite/recurrence.go
git commit -m "feat: implement SQLite RecurrenceRepository"
```

---

### Task 10: Write storage contract tests for recurrence

**Files:**
- Create: `backend/internal/storage/contracttest/recurrence_contract_tests.go`

**Interfaces:**
- Consumes: `storage.Store` (via contract test helper), `RecurrenceRepository`
- Produces: Shared test suite run against both PG and SQLite

- [ ] **Step 1: Write contract tests**

Implement these test cases as a `RecurrenceContractTests` function that takes a `Store` factory:
1. Create recurring task + rule in transaction, round-trip rule
2. Complete/reopen/skip occurrence upsert behavior
3. `ListOccurrences(from, to)` merges task/project metadata
4. Delete task cascades to rule and occurrences
5. `GetCompletedOccurrencesByRange` returns correct summaries
6. `CountOccurrencesByTask` returns correct count (used for delete confirmation)

- [ ] **Step 2: Wire into PG and SQLite contract test suites**

Modify `backend/internal/storage/postgres/contract_test.go` and the SQLite equivalent to also run `RecurrenceContractTests`.

- [ ] **Step 3: Run contract tests**

```bash
cd backend && go test ./internal/storage/postgres/ -run TestContract -v -count=1
cd backend && go test ./internal/storage/sqlite/ -run TestContract -v -count=1
```

Expected: PASS on both providers

- [ ] **Step 4: Commit**

```bash
git add backend/internal/storage/contracttest/recurrence_contract_tests.go
git commit -m "test: add storage contract tests for recurrence"
```

---

## Phase 2: Today Flow Integration

### Task 11: Refactor service/today.go to use Store

**Files:**
- Modify: `backend/internal/service/today.go`
- Modify: `backend/internal/handler/today.go`
- Modify: `backend/cmd/server/main.go`

**Interfaces:**
- Consumes: `storage.Store`, `RecurrenceService`
- Produces: `GetToday(ctx, store, recurrenceService)` with mixed single+recurring results

- [ ] **Step 1: Change service/today.go function signature**

```go
// backend/internal/service/today.go
func GetToday(ctx context.Context, store storage.Store, recurrenceSvc *RecurrenceService) (*TodayData, error) {
	now := time.Now()
	todayStr := now.Format("2006-01-02")
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()
	todayEnd := todayStart + 86400
	overdueCutoff := todayStart - int64(OverdueWindowDays*86400)

	// 1. Get single tasks (filtered to execution_type=single)
	todayTasks, overdueTasks, err := store.Tasks().Today(ctx, todayStart, todayEnd, overdueCutoff)
	if err != nil {
		return nil, err
	}

	// 2. Get recurring occurrences for today
	activeRules, err := store.Recurrence().ListActiveRules(ctx, todayStr, todayStr)
	if err != nil {
		return nil, err
	}
	for _, rule := range activeRules {
		dates := ExpandRuleOccurrences(&rule, todayStr, todayStr)
		if len(dates) == 0 {
			continue
		}
		// LEFT JOIN existing occurrence status
		existingOccurrences, _ := store.Recurrence().ListOccurrences(ctx, todayStr, todayStr)
		occMap := make(map[string]*model.TaskOccurrence)
		for i := range existingOccurrences {
			occMap[existingOccurrences[i].TaskID] = &existingOccurrences[i]
		}
		for _, date := range dates {
			task, err := store.Tasks().GetByID(ctx, rule.TaskID)
			if err != nil {
				continue
			}
			task.ExecutionType = "recurring"
			task.OccurrenceDate = &date
			status := "open"
			if occ, ok := occMap[rule.TaskID]; ok && occ.OccurrenceDate == date {
				status = occ.Status
			}
			task.OccurrenceStatus = &status
			label := GenerateRecurrenceLabel(&rule)
			task.RecurrenceLabel = &label
			task.PlannedDate = nil // template has no planned_date
			todayTasks = append(todayTasks, *task)
		}
	}

	// 3. Sort merged list: sort_order ASC, created_at DESC
	sort.SliceStable(todayTasks, func(i, j int) bool {
		if todayTasks[i].SortOrder != todayTasks[j].SortOrder {
			return todayTasks[i].SortOrder < todayTasks[j].SortOrder
		}
		return todayTasks[i].CreatedAt > todayTasks[j].CreatedAt
	})

	// 4. Events and notes (unchanged)
	events, err := store.Events().Today(ctx, todayStart, todayEnd)
	if err != nil {
		return nil, err
	}
	recentNotes, err := store.Notes().Recent(ctx, 5)
	if err != nil {
		return nil, err
	}

	return &TodayData{
		TodayTasks:   todayTasks,
		OverdueTasks: overdueTasks,
		Events:       events,
		RecentNotes:  recentNotes,
	}, nil
}
```

- [ ] **Step 2: Update handler/today.go**

```go
func GetToday(c *gin.Context) {
	store := repository.ActiveStore()
	recurrenceSvc := service.NewRecurrenceService()
	data, err := service.GetToday(c.Request.Context(), store, recurrenceSvc)
	if err != nil {
		internalError(c, "failed to get today data")
		return
	}
	success(c, data)
}
```

- [ ] **Step 3: Test manually**

Start server, call `GET /api/today`. Verify single tasks still appear. (Recurring tasks won't appear yet until we have creation working.)

- [ ] **Step 4: Commit**

```bash
git add backend/internal/service/today.go backend/internal/handler/today.go
git commit -m "refactor: migrate today service to Store with recurring occurrence merge"
```

---

### Task 12: Refactor service/summary.go to use Store

**Files:**
- Modify: `backend/internal/service/summary.go`
- Modify: `backend/internal/handler/summary.go`

**Interfaces:**
- Consumes: `storage.Store`
- Produces: `GetSummary(ctx, store, params)` with occurrence merge + sort + pagination

- [ ] **Step 1: Rewrite service/summary.go**

```go
func GetSummary(ctx context.Context, store storage.Store, params model.SummaryParams) (*model.SummaryData, error) {
	const maxItems = 10000

	// 1. Single tasks
	singleTasks, singleTotal, err := store.Tasks().GetCompletedTasksByRange(ctx, params.From, params.To, 1, maxItems)
	if err != nil {
		return nil, err
	}

	// 2. Recurring occurrences
	recurringSummaries, err := store.Recurrence().GetCompletedOccurrencesByRange(ctx, params.From, params.To)
	if err != nil {
		recurringSummaries = nil // graceful degradation
	}

	// 3. Merge and sort
	all := append(singleTasks, recurringSummaries...)
	truncated := false
	if len(all) > maxItems {
		sort.Slice(all, func(i, j int) bool {
			a, b := all[i].CompletedAt, all[j].CompletedAt
			if a == nil && b == nil { return false }
			if a == nil { return false }
			if b == nil { return true }
			return *a > *b
		})
		all = all[:maxItems]
		truncated = true
	} else {
		sort.Slice(all, func(i, j int) bool {
			a, b := all[i].CompletedAt, all[j].CompletedAt
			if a == nil && b == nil { return false }
			if a == nil { return false }
			if b == nil { return true }
			return *a > *b
		})
	}

	// 4. Paginate
	total := singleTotal + len(recurringSummaries)
	start := (params.Page - 1) * params.PageSize
	if start > len(all) {
		all = nil
	} else {
		end := start + params.PageSize
		if end > len(all) {
			end = len(all)
		}
		all = all[start:end]
	}

	// 5. Attach notes (unchanged logic)
	// ...

	// 6. active_days + project_count from unified source
	activeDays, projectCount, _ := store.Tasks().GetSummaryStats(ctx, params.From, params.To)
	// Note: for full accuracy, GetSummaryStats should also consider occurrences.
	// See design doc for UNION query approach.
	// For v1, reuse existing stats + add occurrence stats (simplified).

	result := model.NewSummaryData(groups, activeDays, projectCount, total)
	if truncated {
		// Set X-Truncated header in handler
	}
	return result, nil
}
```

- [ ] **Step 2: Update handler/summary.go to pass Store**

```go
func GetSummary(c *gin.Context) {
	// ... parse params ...
	store := repository.ActiveStore()
	data, err := service.GetSummary(c.Request.Context(), store, params)
	// ...
}
```

- [ ] **Step 3: Commit**

```bash
git add backend/internal/service/summary.go backend/internal/handler/summary.go
git commit -m "refactor: migrate summary service to Store with occurrence merge"
```

---

### Task 13: Refactor service/tasks.go for recurring create/update

**Files:**
- Modify: `backend/internal/service/tasks.go`
- Modify: `backend/internal/handler/tasks.go`
- Modify: `backend/internal/router/router.go`

**Interfaces:**
- Produces: `CreateTask` supports `execution_type=recurring` with `Store.Transact`, `UpdateTask` validates recurring template constraints

- [ ] **Step 1: Rewrite CreateTask for both types**

```go
func CreateTask(ctx context.Context, store storage.Store, req *model.CreateTaskRequest) (*model.Task, error) {
	if req.ExecutionType == "recurring" {
		if req.Recurrence == nil {
			return nil, fmt.Errorf("recurrence config is required for recurring task")
		}
		if err := ValidateRecurrenceConfig(req.Recurrence); err != nil {
			return nil, err
		}
		var task *model.Task
		err := store.Transact(ctx, func(txStore storage.Store) error {
			t := &model.Task{
				Title:         req.Title,
				Content:       req.Content,
				Project:       req.Project,
				ProjectID:     req.ProjectID,
				Due:           req.Due,
				Priority:      req.Priority,
				Scope:         req.Scope,
				Horizon:       req.Horizon,
				RoadmapNodeID: req.RoadmapNodeID,
				ExecutionType: "recurring",
			}
			if err := txStore.Tasks().Create(ctx, t); err != nil {
				return err
			}
			rule := &model.RecurrenceRule{
				TaskID:    t.ID,
				StartDate: req.Recurrence.StartDate,
				EndDate:   req.Recurrence.EndDate,
				Frequency:  req.Recurrence.Frequency,
				Interval:   req.Recurrence.Interval,
				Weekdays:   req.Recurrence.Weekdays,
				MonthDays:  req.Recurrence.MonthDays,
				Timezone:   req.Recurrence.Timezone,
				Enabled:    true,
			}
			if req.Recurrence.Interval == 0 {
				rule.Interval = 1
			}
			if rule.Timezone == "" {
				rule.Timezone = defaultTimezone()
			}
			if err := txStore.Recurrence().UpsertRule(ctx, rule); err != nil {
				return err
			}
			task = t
			return nil
		})
		if err != nil {
			return nil, err
		}
		return store.Tasks().GetByID(ctx, task.ID)
	}

	// Single task (unchanged path)
	task := &model.Task{
		Title: req.Title, Content: req.Content, Project: req.Project,
		ProjectID: req.ProjectID, Due: req.Due, PlannedDate: req.PlannedDate,
		Priority: req.Priority, Scope: req.Scope, Horizon: req.Horizon,
		RoadmapNodeID: req.RoadmapNodeID, ExecutionType: "single",
	}
	if err := store.Tasks().Create(ctx, task); err != nil {
		return nil, err
	}
	return store.Tasks().GetByID(ctx, task.ID)
}
```

- [ ] **Step 2: Rewrite UpdateTask for recurring validation**

Add guards:
- If task is recurring and request has `done=1` or `status=done` → return 409 `CANNOT_COMPLETE_RECURRING_TEMPLATE`
- If task is recurring and request has `execution_type=single` → return 409 `CANNOT_SWITCH_RECURRING_TO_SINGLE`
- If task is single and request has `execution_type=recurring` → validate recurrence, clear planned_date
- If request has `recurrence` → validate, upsert rule in Transact

- [ ] **Step 3: Update handler/tasks.go CreateTask and UpdateTask**

```go
func CreateTask(c *gin.Context) {
	var req model.CreateTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "title is required")
		return
	}
	store := repository.ActiveStore()
	task, err := service.CreateTask(c.Request.Context(), store, &req)
	if err != nil {
		internalError(c, err.Error())
		return
	}
	created(c, gin.H{"task": task})
}
```

- [ ] **Step 4: Add occurrence routes to router**

```go
// In router.go, after existing task routes:
api.POST("/tasks/:id/occurrences/:date/complete", handler.CompleteOccurrence)
api.POST("/tasks/:id/occurrences/:date/reopen", handler.ReopenOccurrence)
api.POST("/tasks/:id/occurrences/:date/skip", handler.SkipOccurrence)
api.GET("/task-occurrences", handler.GetTaskOccurrences)
```

- [ ] **Step 5: Add occurrence handlers**

Create handler methods that:
1. Get Store from `repository.ActiveStore()`
2. Validate task exists and is recurring
3. Validate date is an expected occurrence via `ExpandRuleOccurrences(rule, date, date)`
4. Call appropriate `RecurrenceRepository` method
5. Return occurrence status

- [ ] **Step 6: Commit**

```bash
git add backend/internal/service/tasks.go backend/internal/handler/tasks.go backend/internal/router/router.go
git commit -m "feat: add recurring task create/update with Transact + occurrence endpoints"
```

---

## Phase 3-5: Remaining Tasks (outline)

### Task 14: Add `GET /api/task-occurrences` handler
- Query param `from`, `to` (max 370 days)
- Call `ListActiveRules` + expand + merge status
- Return `[]TaskOccurrence` with task metadata and `recurrence_label`

### Task 15: Add `GET /api/tasks/:id/occurrences/count` (for delete UX)
- Return `{ "count": N }` for frontend delete confirmation dialog

### Task 16: Summary stats unification
- Update `GetSummaryStats` to use UNION query for active_days and project_count
- Includes both `tasks.completed_at` and `task_occurrences.completed_at`

### Task 17: E2E test with Playwright
- Create daily recurring task
- Verify today page shows it
- Complete occurrence
- Verify refresh preserves state
- Verify calendar shows dot
- Verify summary includes completion record

### Task 18: Search index integration
- On recurring task create/update, upsert search index with `recurrence_label`
- Add `execution_type` to search result metadata

---

## Test Summary

| Layer | Tests | Expected |
|-------|-------|----------|
| Unit: expansion | `TestExpand*` (8 tests) | All dates match expected |
| Unit: labels | `TestGenerateRecurrenceLabel` (8 cases) | All labels match |
| Unit: today sort | `TestTodaySortOrder` | Mixed sort correct |
| Contract: PG | RecurrenceContractTests (6 tests) | Round-trip, cascade, upsert |
| Contract: SQLite | RecurrenceContractTests (6 tests) | Round-trip, cascade, upsert |
| E2E: Playwright | Create + complete + summary (5 steps) | Full flow works |

