# Calendar Project Sources Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make calendar sources project-driven: personal short-term projects appear by default, regular and learning projects are user-configurable backend-saved calendar sources, and events belong to projects through `project_id`.

**Architecture:** Add a small calendar-source domain beside events instead of folding user configuration into event storage. Backend owns workspace/user validation and source persistence; frontend consumes typed source APIs and filters events by `project_id`. Keep `kind` as compatibility data, but stop using it as the calendar source model.

**Tech Stack:** Go 1.26, Gin, SQLite, PostgreSQL, storage contract tests, React 19, TanStack Query, Vitest, Testing Library.

---

## TDD Rules For This Plan

- No production code before the matching failing test is written and observed failing.
- Each task starts with the smallest behavior test that proves the desired behavior.
- A test that passes immediately is not accepted as a RED step; rewrite it until it fails for the missing behavior.
- Keep implementation minimal until the task is green, then refactor only with tests still green.
- Do not commit during execution unless the user explicitly approves commits in that session.

## Source Design Reference

- Design spec: `docs/superpowers/specs/2026-07-09-calendar-project-sources-design.md`

## File Structure

Backend model and contracts:

- Modify: `backend/internal/model/event.go`
- Create: `backend/internal/model/calendar.go`
- Modify: `backend/internal/storage/store.go`
- Create: `backend/internal/storage/contracttest/calendar_project_sources_contract_tests.go`
- Modify: `backend/internal/storage/contracttest/events_inbox_contract_tests.go`
- Modify: `backend/internal/storage/sqlite/contract_test.go`
- Modify: `backend/internal/storage/postgres/contract_test.go`

Backend storage:

- Modify: `backend/db/schema.sql`
- Modify: `backend/db/migrations/postgres/0001_init_postgres.sql`
- Modify: `backend/internal/storage/sqlite/provider.go`
- Modify: `backend/internal/storage/postgres/provider.go`
- Modify: `backend/internal/storage/sqlite/events.go`
- Modify: `backend/internal/storage/postgres/events.go`
- Create: `backend/internal/storage/sqlite/calendar_project_sources.go`
- Create: `backend/internal/storage/postgres/calendar_project_sources.go`
- Modify: `backend/internal/storage/sqlite/auth_migrations.go`
- Modify: `backend/internal/storage/postgres/auth_migrations.go`

Backend service, handler, router:

- Create: `backend/internal/service/calendar_sources.go`
- Create: `backend/internal/service/calendar_sources_test.go`
- Create: `backend/internal/handler/calendar_sources.go`
- Create: `backend/internal/handler/calendar_sources_test.go`
- Modify: `backend/internal/handler/events.go`
- Modify: `backend/internal/router/router.go`

Frontend API and hooks:

- Modify: `frontend/src/api/events.ts`
- Create: `frontend/src/api/calendar.ts`
- Create: `frontend/src/api/calendar.test.ts`
- Create: `frontend/src/hooks/useCalendarSources.ts`
- Modify: `frontend/src/hooks/useEvents.ts`

Frontend UI:

- Modify: `frontend/src/routes/Calendar.tsx`
- Modify: `frontend/src/routes/Calendar.test.tsx`
- Modify: `frontend/src/components/QuickCapture.tsx`
- Modify: `frontend/src/components/QuickCapture.test.tsx`

---

### Task 1: Event Model Carries Project Identity

**Files:**
- Modify: `backend/internal/model/event.go`
- Modify: `backend/internal/storage/contracttest/events_inbox_contract_tests.go`
- Modify: `backend/internal/storage/sqlite/events.go`
- Modify: `backend/internal/storage/postgres/events.go`
- Modify: `frontend/src/api/events.ts`

- [ ] **Step 1: Write the failing backend storage contract test**

Add this subtest inside `RunEventInboxSuite` in `backend/internal/storage/contracttest/events_inbox_contract_tests.go`:

```go
t.Run("EventsPersistProjectIdentityAndClearWithEmptyString", func(t *testing.T) {
	store := factory(t)
	defer store.Close()

	ctx := scopedContractContext(t, store)
	project, err := store.Tasks().CreateProject(ctx, &model.CreateTaskProjectRequest{
		Name:        "Calendar Learning",
		Type:        "learning",
		Description: "calendar source test",
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	event := model.Event{
		Title:     "project event",
		StartTime: time.Date(2026, 7, 9, 9, 0, 0, 0, time.Local).Unix(),
		EndTime:   time.Date(2026, 7, 9, 10, 0, 0, 0, time.Local).Unix(),
		Kind:      "work",
		ProjectID: &project.ID,
	}
	if err := store.Events().Create(ctx, &event); err != nil {
		t.Fatalf("create event: %v", err)
	}

	loaded, err := store.Events().GetByID(ctx, event.ID)
	if err != nil {
		t.Fatalf("load event: %v", err)
	}
	if loaded.ProjectID == nil || *loaded.ProjectID != project.ID {
		t.Fatalf("project_id = %v, want %q", loaded.ProjectID, project.ID)
	}
	if loaded.Project == nil || *loaded.Project != project.Name {
		t.Fatalf("project = %v, want %q", loaded.Project, project.Name)
	}
	if loaded.ProjectType == nil || *loaded.ProjectType != project.Type {
		t.Fatalf("project_type = %v, want %q", loaded.ProjectType, project.Type)
	}

	clear := ""
	updated, err := store.Events().Update(ctx, event.ID, &model.UpdateEventRequest{ProjectID: &clear})
	if err != nil {
		t.Fatalf("clear event project: %v", err)
	}
	if updated.ProjectID != nil || updated.Project != nil || updated.ProjectType != nil {
		t.Fatalf("expected project fields cleared, got %+v", updated)
	}
})
```

- [ ] **Step 2: Run the RED check**

Run:

```powershell
cd backend
go test ./internal/storage/sqlite -run TestSQLiteEventInboxContract/EventsPersistProjectIdentityAndClearWithEmptyString -count=1
```

Expected: FAIL at compile time because `model.Event` and `model.UpdateEventRequest` do not have `ProjectID`, `Project`, or `ProjectType`.

- [ ] **Step 3: Add minimal model fields**

In `backend/internal/model/event.go`, extend the structs:

```go
type Event struct {
	ID          string  `json:"id"`
	Title       string  `json:"title"`
	StartTime   int64   `json:"start_time"`
	EndTime     int64   `json:"end_time"`
	Location    *string `json:"location"`
	Kind        string  `json:"kind"`
	NoteID      *string `json:"note_id"`
	ProjectID   *string `json:"project_id"`
	Project     *string `json:"project,omitempty"`
	ProjectType *string `json:"project_type,omitempty"`
	CreatedAt   int64   `json:"created_at"`
	UpdatedAt   int64   `json:"updated_at"`
}

type CreateEventRequest struct {
	Title     string  `json:"title" binding:"required"`
	StartTime int64   `json:"start_time" binding:"required"`
	EndTime   int64   `json:"end_time" binding:"required"`
	Location  *string `json:"location"`
	Kind      string  `json:"kind"`
	ProjectID *string `json:"project_id"`
}

type UpdateEventRequest struct {
	Title     *string `json:"title"`
	StartTime *int64  `json:"start_time"`
	EndTime   *int64  `json:"end_time"`
	Location  *string `json:"location"`
	Kind      *string `json:"kind"`
	ProjectID *string `json:"project_id,omitempty"`
}
```

- [ ] **Step 4: Run RED again**

Run:

```powershell
cd backend
go test ./internal/storage/sqlite -run TestSQLiteEventInboxContract/EventsPersistProjectIdentityAndClearWithEmptyString -count=1
```

Expected: FAIL because storage does not persist or scan `project_id`.

- [ ] **Step 5: Implement minimal SQLite event persistence**

In `backend/db/schema.sql`, add `project_id TEXT` to `events` and add the workspace/project index after the table exists:

```sql
project_id TEXT,
```

```sql
CREATE INDEX IF NOT EXISTS events_workspace_project_start_idx
  ON events (workspace_id, project_id, start_time);
```

In `backend/internal/storage/sqlite/events.go`:

- Include `project_id` in inserts.
- Join `task_projects` on `(workspace_id, project_id)` in every select.
- Scan `project_id`, project name, and project type into `model.Event`.
- On update, if `req.ProjectID != nil` and `strings.TrimSpace(*req.ProjectID) == ""`, set `project_id = NULL`; otherwise set `project_id = ?`.

The selected column list should be:

```sql
SELECT e.id, e.title, e.start_time, e.end_time, e.location, e.kind, e.note_id,
       e.project_id, p.name, p.type, e.created_at, e.updated_at
FROM events e
LEFT JOIN task_projects p ON p.workspace_id = e.workspace_id AND p.id = e.project_id
```

- [ ] **Step 6: Verify SQLite GREEN**

Run:

```powershell
cd backend
go test ./internal/storage/sqlite -run TestSQLiteEventInboxContract/EventsPersistProjectIdentityAndClearWithEmptyString -count=1
```

Expected: PASS.

- [ ] **Step 7: Repeat RED/GREEN for PostgreSQL**

Before implementation, run:

```powershell
cd backend
go test ./internal/storage/postgres -run TestPostgresEventInboxContract/EventsPersistProjectIdentityAndClearWithEmptyString -count=1
```

Expected: FAIL because PostgreSQL events do not include `project_id`.

Then update `backend/db/migrations/postgres/0001_init_postgres.sql` and `backend/internal/storage/postgres/events.go` with the same behavior, using `start_at` and `tstzrange`. Event selects should join:

```sql
LEFT JOIN task_projects p ON p.workspace_id = e.workspace_id AND p.id = e.project_id
```

Run again:

```powershell
cd backend
go test ./internal/storage/postgres -run TestPostgresEventInboxContract/EventsPersistProjectIdentityAndClearWithEmptyString -count=1
```

Expected: PASS.

---

### Task 2: Calendar Project Sources Repository

**Files:**
- Create: `backend/internal/model/calendar.go`
- Modify: `backend/internal/storage/store.go`
- Create: `backend/internal/storage/contracttest/calendar_project_sources_contract_tests.go`
- Modify: `backend/internal/storage/sqlite/contract_test.go`
- Modify: `backend/internal/storage/postgres/contract_test.go`
- Create: `backend/internal/storage/sqlite/calendar_project_sources.go`
- Create: `backend/internal/storage/postgres/calendar_project_sources.go`
- Modify: `backend/internal/storage/sqlite/provider.go`
- Modify: `backend/internal/storage/postgres/provider.go`
- Modify: `backend/db/schema.sql`
- Modify: `backend/db/migrations/postgres/0001_init_postgres.sql`

- [ ] **Step 1: Write the model and interface test first**

Create `backend/internal/storage/contracttest/calendar_project_sources_contract_tests.go`:

```go
package contracttest

import (
	"testing"

	"github.com/hujinrun/flowspace/internal/model"
)

func RunCalendarProjectSourcesSuite(t *testing.T, factory StoreFactory) {
	t.Helper()

	t.Run("PersonalProjectsAreDefaultAndConfiguredProjectsAreUserScoped", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctx := scopedContractContext(t, store)
		regular, err := store.Tasks().CreateProject(ctx, &model.CreateTaskProjectRequest{
			Name: "Knowledge Base",
			Type: "regular",
		})
		if err != nil {
			t.Fatalf("create regular project: %v", err)
		}
		learning, err := store.Tasks().CreateProject(ctx, &model.CreateTaskProjectRequest{
			Name: "N2 Learning",
			Type: "learning",
		})
		if err != nil {
			t.Fatalf("create learning project: %v", err)
		}

		before, err := store.Calendar().ListProjectSources(ctx)
		if err != nil {
			t.Fatalf("list project sources before save: %v", err)
		}
		assertSourceState(t, before.Sources, "personal", true, true)
		assertSourceState(t, before.AvailableProjects, regular.ID, false, false)
		assertSourceState(t, before.AvailableProjects, learning.ID, false, false)

		after, err := store.Calendar().SaveProjectSources(ctx, []model.CalendarProjectSourceInput{
			{ProjectID: regular.ID, Enabled: true, Color: "#2e90fa", OrderIndex: 10},
			{ProjectID: learning.ID, Enabled: false, Color: "", OrderIndex: 20},
		})
		if err != nil {
			t.Fatalf("save project sources: %v", err)
		}
		assertSourceState(t, after.Sources, "personal", true, true)
		assertSourceState(t, after.Sources, regular.ID, true, false)
		assertSourceState(t, after.AvailableProjects, learning.ID, false, false)
	})
}

func assertSourceState(t *testing.T, sources []model.CalendarProjectSource, projectID string, enabled bool, defaultSource bool) {
	t.Helper()
	for _, source := range sources {
		if source.ProjectID == projectID {
			if source.Enabled != enabled || source.Default != defaultSource {
				t.Fatalf("source %q state = enabled:%v default:%v, want enabled:%v default:%v", projectID, source.Enabled, source.Default, enabled, defaultSource)
			}
			return
		}
	}
	t.Fatalf("source %q not found in %+v", projectID, sources)
}
```

Add suite registration to `backend/internal/storage/sqlite/contract_test.go` and `backend/internal/storage/postgres/contract_test.go` with the same factory pattern as `RunEventInboxSuite`.

- [ ] **Step 2: Run RED**

Run:

```powershell
cd backend
go test ./internal/storage/sqlite -run TestSQLiteCalendarProjectSourcesContract -count=1
```

Expected: FAIL because `Store.Calendar`, `model.CalendarProjectSource`, and related types do not exist.

- [ ] **Step 3: Add minimal models and storage interface**

Create `backend/internal/model/calendar.go`:

```go
package model

type CalendarProjectSource struct {
	ProjectID  string `json:"project_id"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	Enabled    bool   `json:"enabled"`
	Default    bool   `json:"default"`
	Color      string `json:"color"`
	OrderIndex int    `json:"order_index"`
}

type CalendarProjectSourcesResponse struct {
	Sources           []CalendarProjectSource `json:"sources"`
	AvailableProjects []CalendarProjectSource `json:"available_projects"`
}

type CalendarProjectSourceInput struct {
	ProjectID  string `json:"project_id"`
	Enabled    bool   `json:"enabled"`
	Color      string `json:"color"`
	OrderIndex int    `json:"order_index"`
}

type SaveCalendarProjectSourcesRequest struct {
	Sources []CalendarProjectSourceInput `json:"sources"`
}
```

Modify `backend/internal/storage/store.go`:

```go
type Store interface {
	Close() error
	Health(context.Context) error
	Capabilities() Capabilities
	Transact(context.Context, func(Store) error) error

	Folders() FolderRepository
	Notes() NoteRepository
	Tasks() TaskRepository
	Recurrence() RecurrenceRepository
	Events() EventRepository
	Calendar() CalendarRepository
	Inbox() InboxRepository
	Roadmaps() RoadmapRepository
	Sync() SyncRepository
	Search() SearchRepository
	Auth() AuthRepository
}

type CalendarRepository interface {
	ListProjectSources(context.Context) (*model.CalendarProjectSourcesResponse, error)
	SaveProjectSources(context.Context, []model.CalendarProjectSourceInput) (*model.CalendarProjectSourcesResponse, error)
}
```

- [ ] **Step 4: Run RED again**

Run:

```powershell
cd backend
go test ./internal/storage/sqlite -run TestSQLiteCalendarProjectSourcesContract -count=1
```

Expected: FAIL because providers do not implement `Calendar()` and storage queries are missing.

- [ ] **Step 5: Implement SQLite storage minimally**

In `backend/db/schema.sql`, create `calendar_project_sources` using the design spec's SQLite schema, including:

```sql
FOREIGN KEY (workspace_id, user_id)
  REFERENCES workspace_members(workspace_id, user_id)
  ON DELETE CASCADE,
FOREIGN KEY (workspace_id, project_id)
  REFERENCES task_projects(workspace_id, id)
  ON UPDATE CASCADE
  ON DELETE CASCADE
```

Create `backend/internal/storage/sqlite/calendar_project_sources.go` with:

```go
package sqlite

import (
	"context"

	"github.com/hujinrun/flowspace/internal/model"
)

type calendarRepository struct {
	db sqliteRunner
}
```

Implement `ListProjectSources` and `SaveProjectSources` using current `auth.UserIDFromContext(ctx)` and `auth.WorkspaceIDFromContext(ctx)`. Rules:

- `ListProjectSources` returns personal projects as default sources even without config rows.
- Enabled regular/learning rows appear in `Sources`.
- Disabled or unconfigured regular/learning projects appear in `AvailableProjects`.
- Personal projects are ignored by `SaveProjectSources`.
- Save upserts all inputs by `(workspace_id, user_id, project_id)`.

Modify `backend/internal/storage/sqlite/provider.go` to return `calendarRepository{db: s.db}` and `calendarRepository{db: s.tx}` from `Calendar()`.

- [ ] **Step 6: Verify SQLite GREEN**

Run:

```powershell
cd backend
go test ./internal/storage/sqlite -run TestSQLiteCalendarProjectSourcesContract -count=1
```

Expected: PASS.

- [ ] **Step 7: Repeat RED/GREEN for PostgreSQL**

Run RED:

```powershell
cd backend
go test ./internal/storage/postgres -run TestPostgresCalendarProjectSourcesContract -count=1
```

Expected: FAIL because PostgreSQL storage does not implement calendar sources.

Then add `calendar_project_sources` to `backend/db/migrations/postgres/0001_init_postgres.sql`, create `backend/internal/storage/postgres/calendar_project_sources.go`, and wire `Calendar()` in `backend/internal/storage/postgres/provider.go`.

Run GREEN:

```powershell
cd backend
go test ./internal/storage/postgres -run TestPostgresCalendarProjectSourcesContract -count=1
```

Expected: PASS.

---

### Task 3: Calendar Project Sources HTTP API

**Files:**
- Create: `backend/internal/service/calendar_sources.go`
- Create: `backend/internal/service/calendar_sources_test.go`
- Create: `backend/internal/handler/calendar_sources.go`
- Create: `backend/internal/handler/calendar_sources_test.go`
- Modify: `backend/internal/router/router.go`

- [ ] **Step 1: Write failing handler test**

Create `backend/internal/handler/calendar_sources_test.go` with a Gin test that authenticates like existing handler tests and asserts:

- `GET /api/calendar/project-sources` returns `sources` and `available_projects`.
- `PUT /api/calendar/project-sources` accepts a full `sources` body and returns the refreshed shape.
- A request without auth is rejected by router middleware when route is registered.

Use this JSON assertion body for the PUT behavior:

```json
{
  "sources": [
    { "project_id": "regular-1", "enabled": true, "color": "#2e90fa", "order_index": 10 },
    { "project_id": "learning-1", "enabled": false, "color": "", "order_index": 20 }
  ]
}
```

- [ ] **Step 2: Run RED**

Run:

```powershell
cd backend
go test ./internal/handler -run TestCalendarProjectSources -count=1
```

Expected: FAIL because handlers and routes do not exist.

- [ ] **Step 3: Implement minimal service and handlers**

Create `backend/internal/service/calendar_sources.go`:

```go
package service

import (
	"context"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

func ListCalendarProjectSources(ctx context.Context, store storage.Store) (*model.CalendarProjectSourcesResponse, error) {
	return store.Calendar().ListProjectSources(ctx)
}

func SaveCalendarProjectSources(ctx context.Context, store storage.Store, req *model.SaveCalendarProjectSourcesRequest) (*model.CalendarProjectSourcesResponse, error) {
	return store.Calendar().SaveProjectSources(ctx, req.Sources)
}
```

Create `backend/internal/handler/calendar_sources.go`:

```go
package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/service"
	"github.com/hujinrun/flowspace/internal/storage"
)

func GetCalendarProjectSources(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		response, err := service.ListCalendarProjectSources(c.Request.Context(), store)
		if err != nil {
			internalError(c, "failed to load calendar project sources")
			return
		}
		success(c, response)
	}
}

func PutCalendarProjectSources(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req model.SaveCalendarProjectSourcesRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			badRequest(c, "invalid calendar project sources request")
			return
		}
		response, err := service.SaveCalendarProjectSources(c.Request.Context(), store, &req)
		if err != nil {
			internalError(c, "failed to save calendar project sources")
			return
		}
		success(c, response)
	}
}
```

Modify `backend/internal/router/router.go`:

```go
protected.GET("/calendar/project-sources", handler.GetCalendarProjectSources(cfg.Store))
protected.PUT("/calendar/project-sources", handler.PutCalendarProjectSources(cfg.Store))
```

- [ ] **Step 4: Verify handler GREEN**

Run:

```powershell
cd backend
go test ./internal/handler -run TestCalendarProjectSources -count=1
go test ./internal/router -run TestProtectedRoutesRequireAuth -count=1
```

Expected: PASS. The second command may run the whole router package when no matching test exists; success still proves the new route compiles and protected routes remain valid.

---

### Task 4: Event Create/Update Validation Through Service

**Files:**
- Modify: `backend/internal/service/events.go`
- Create: `backend/internal/service/events_test.go`
- Modify: `backend/internal/handler/events.go`

- [ ] **Step 1: Write failing service tests**

In `backend/internal/service/events_test.go`, add tests for:

- Create event with valid `project_id` stores that ID.
- Create event with invalid `project_id` returns an error.
- Update event with `project_id` omitted keeps the existing project.
- Update event with `project_id: ""` clears the project.

The invalid project assertion should check that the error is returned before storage silently creates an unassigned event.

- [ ] **Step 2: Run RED**

Run:

```powershell
cd backend
go test ./internal/service -run TestEventProjectID -count=1
```

Expected: FAIL because service does not validate or pass `ProjectID`.

- [ ] **Step 3: Implement minimal service validation**

In `backend/internal/service/events.go`:

- When `req.ProjectID != nil` and trimmed value is not empty, call `store.Tasks().GetProjectByID(ctx, *req.ProjectID)` before creating/updating.
- For create, assign `ProjectID: req.ProjectID` into `model.Event`.
- For update, pass `ProjectID` through to storage.
- Return a non-nil error for invalid project IDs.

- [ ] **Step 4: Verify GREEN**

Run:

```powershell
cd backend
go test ./internal/service -run TestEventProjectID -count=1
go test ./internal/storage/sqlite -run TestSQLiteEventInboxContract/EventsPersistProjectIdentityAndClearWithEmptyString -count=1
go test ./internal/storage/postgres -run TestPostgresEventInboxContract/EventsPersistProjectIdentityAndClearWithEmptyString -count=1
```

Expected: PASS.

---

### Task 5: Project Deletion Clears Event Project Without Losing Events

**Files:**
- Modify: `backend/internal/storage/contracttest/events_inbox_contract_tests.go`
- Modify: `backend/internal/storage/sqlite/tasks.go`
- Modify: `backend/internal/storage/postgres/tasks.go`

- [ ] **Step 1: Write failing contract test**

Add a subtest to `RunEventInboxSuite`:

```go
t.Run("DeletingProjectClearsEventProjectOnly", func(t *testing.T) {
	store := factory(t)
	defer store.Close()

	ctx := scopedContractContext(t, store)
	project, err := store.Tasks().CreateProject(ctx, &model.CreateTaskProjectRequest{Name: "Temporary Calendar Project", Type: "regular"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	event := model.Event{
		Title:     "event survives project delete",
		StartTime: time.Date(2026, 7, 9, 9, 0, 0, 0, time.Local).Unix(),
		EndTime:   time.Date(2026, 7, 9, 10, 0, 0, 0, time.Local).Unix(),
		Kind:      "work",
		ProjectID: &project.ID,
	}
	if err := store.Events().Create(ctx, &event); err != nil {
		t.Fatalf("create event: %v", err)
	}
	if err := store.Tasks().DeleteProject(ctx, project.ID); err != nil {
		t.Fatalf("delete project: %v", err)
	}
	loaded, err := store.Events().GetByID(ctx, event.ID)
	if err != nil {
		t.Fatalf("load event after project delete: %v", err)
	}
	if loaded.ProjectID != nil {
		t.Fatalf("project_id after project delete = %v, want nil", loaded.ProjectID)
	}
})
```

- [ ] **Step 2: Run RED**

Run:

```powershell
cd backend
go test ./internal/storage/sqlite -run TestSQLiteEventInboxContract/DeletingProjectClearsEventProjectOnly -count=1
```

Expected: FAIL because deleting a project does not clear event project references.

- [ ] **Step 3: Implement minimal delete behavior**

In both `backend/internal/storage/sqlite/tasks.go` and `backend/internal/storage/postgres/tasks.go`, wrap project deletion in a transaction:

1. `UPDATE events SET project_id = NULL WHERE workspace_id = ? AND project_id = ?`
2. Delete or migrate dependent task/project rows according to the current task project deletion behavior.
3. Keep `events.workspace_id` unchanged.

- [ ] **Step 4: Verify GREEN**

Run:

```powershell
cd backend
go test ./internal/storage/sqlite -run TestSQLiteEventInboxContract/DeletingProjectClearsEventProjectOnly -count=1
go test ./internal/storage/postgres -run TestPostgresEventInboxContract/DeletingProjectClearsEventProjectOnly -count=1
```

Expected: PASS.

---

### Task 6: Frontend API And Hooks

**Files:**
- Modify: `frontend/src/api/events.ts`
- Create: `frontend/src/api/calendar.ts`
- Create: `frontend/src/api/calendar.test.ts`
- Create: `frontend/src/hooks/useCalendarSources.ts`
- Modify: `frontend/src/hooks/useEvents.ts`

- [ ] **Step 1: Write failing frontend API tests**

Create `frontend/src/api/calendar.test.ts`:

```ts
import { describe, expect, it, vi } from 'vitest'
import { api } from './client'
import { getCalendarProjectSources, saveCalendarProjectSources } from './calendar'

vi.mock('./client', () => ({
  api: {
    get: vi.fn(),
    put: vi.fn(),
  },
}))

describe('calendar project source api', () => {
  it('loads project sources from the backend calendar endpoint', async () => {
    vi.mocked(api.get).mockResolvedValue({
      data: {
        sources: [{ project_id: 'personal', name: 'Personal', type: 'personal', enabled: true, default: true, color: '#c4742f', order_index: 0 }],
        available_projects: [],
      },
    })

    const result = await getCalendarProjectSources()

    expect(api.get).toHaveBeenCalledWith('/api/calendar/project-sources')
    expect(result.sources[0]?.project_id).toBe('personal')
  })

  it('saves all configurable source states', async () => {
    vi.mocked(api.put).mockResolvedValue({
      data: {
        sources: [],
        available_projects: [{ project_id: 'learning-1', name: 'N2', type: 'learning', enabled: false, default: false, color: '', order_index: 10 }],
      },
    })

    await saveCalendarProjectSources({
      sources: [{ project_id: 'learning-1', enabled: false, color: '', order_index: 10 }],
    })

    expect(api.put).toHaveBeenCalledWith('/api/calendar/project-sources', {
      sources: [{ project_id: 'learning-1', enabled: false, color: '', order_index: 10 }],
    })
  })
})
```

- [ ] **Step 2: Run RED**

Run:

```powershell
cd frontend
npm test -- src/api/calendar.test.ts
```

Expected: FAIL because `frontend/src/api/calendar.ts` does not exist.

- [ ] **Step 3: Implement frontend API and event types**

Create `frontend/src/api/calendar.ts` with typed `CalendarProjectSource`, `CalendarProjectSourcesResponse`, `SaveCalendarProjectSourcesRequest`, `getCalendarProjectSources`, and `saveCalendarProjectSources`.

Modify `frontend/src/api/events.ts`:

```ts
export interface Event {
  id: string
  title: string
  start_time: number
  end_time: number
  location?: string
  kind: string
  note_id?: string
  project_id?: string
  project?: string
  project_type?: string
  created_at: number
  updated_at: number
}

export async function createEvent(body: {
  title: string
  start_time: number
  end_time: number
  location?: string
  kind?: string
  project_id?: string
}) {
  const res = await api.post<{ event: Event }>('/api/events', body)
  return res.data.event
}
```

Create `frontend/src/hooks/useCalendarSources.ts` with a query key `['calendar-project-sources']` and mutation invalidation for that key and `['events']`.

- [ ] **Step 4: Verify GREEN**

Run:

```powershell
cd frontend
npm test -- src/api/calendar.test.ts
npm test -- src/api/client.test.ts src/api/sync.test.ts
```

Expected: PASS.

---

### Task 7: Calendar Page Uses Project Sources

**Files:**
- Modify: `frontend/src/routes/Calendar.test.tsx`
- Modify: `frontend/src/routes/Calendar.tsx`
- Modify: `frontend/src/hooks/useCalendarSources.ts`

- [ ] **Step 1: Write failing Calendar UI tests**

Update `frontend/src/routes/Calendar.test.tsx` so the mocked event has `project_id: 'personal'` and the mocked calendar sources hook returns:

```ts
{
  sources: [
    { project_id: 'personal', name: 'Personal', type: 'personal', enabled: true, default: true, color: '#c4742f', order_index: 0 },
    { project_id: 'learning-1', name: 'N2', type: 'learning', enabled: true, default: false, color: '#2e90fa', order_index: 10 },
  ],
  available_projects: [
    { project_id: 'regular-1', name: 'Knowledge Base', type: 'regular', enabled: false, default: false, color: '', order_index: 20 },
  ],
}
```

Add these tests:

```ts
it('renders project-driven calendar sources and filters by selected project', async () => {
  const user = userEvent.setup()
  renderCalendar()

  expect(await screen.findByText('个人短期项目')).toBeVisible()
  expect(screen.getByRole('button', { name: /Personal/ })).toBeVisible()
  expect(screen.getByText('已加入日历')).toBeVisible()
  expect(screen.getByRole('button', { name: /N2/ })).toBeVisible()
  expect(screen.getByLabelText('日程：设计评审，09:30')).toBeVisible()

  await user.click(screen.getByRole('button', { name: /N2/ }))

  expect(screen.queryByLabelText('日程：设计评审，09:30')).not.toBeInTheDocument()
})

it('opens project source configuration and saves disabled project states', async () => {
  const user = userEvent.setup()
  const saveSources = vi.fn().mockResolvedValue({
    sources: [
      { project_id: 'personal', name: 'Personal', type: 'personal', enabled: true, default: true, color: '#c4742f', order_index: 0 },
    ],
    available_projects: [
      { project_id: 'regular-1', name: 'Knowledge Base', type: 'regular', enabled: false, default: false, color: '', order_index: 20 },
    ],
  })
  vi.mocked(useSaveCalendarProjectSources).mockReturnValue({
    mutateAsync: saveSources,
    isPending: false,
  } as unknown as ReturnType<typeof useSaveCalendarProjectSources>)

  renderCalendar()

  await user.click(await screen.findByRole('button', { name: '配置项目' }))
  expect(screen.getByRole('checkbox', { name: /Knowledge Base/ })).toBeVisible()
  await user.click(screen.getByRole('button', { name: '保存配置' }))

  expect(saveSources).toHaveBeenCalledWith({
    sources: expect.arrayContaining([
      { project_id: 'regular-1', enabled: false, color: '', order_index: 20 },
    ]),
  })
})
```

- [ ] **Step 2: Run RED**

Run:

```powershell
cd frontend
npm test -- src/routes/Calendar.test.tsx
```

Expected: FAIL because Calendar still uses hard-coded `工作 / 个人 / 提醒` sources and local-only custom calendars.

- [ ] **Step 3: Implement minimal Calendar UI**

In `frontend/src/routes/Calendar.tsx`:

- Replace hard-coded `calendarSources` with `useCalendarProjectSources`.
- Use `selectedSourceProjectID` instead of `selectedSource`.
- Filter events by `event.project_id === selectedSourceProjectID`.
- Add fallback source `未归属日程` only when visible events contain `project_id` missing.
- Replace `添加日历` local form with a configuration panel that toggles regular/learning project source states and calls `saveCalendarProjectSources`.
- When creating a calendar event from the side panel, send `project_id: selectedSourceProjectID` and keep compatible `kind`.

- [ ] **Step 4: Verify GREEN**

Run:

```powershell
cd frontend
npm test -- src/routes/Calendar.test.tsx
```

Expected: PASS.

---

### Task 8: Quick Capture Sends Event project_id

**Files:**
- Modify: `frontend/src/components/QuickCapture.test.tsx`
- Modify: `frontend/src/components/QuickCapture.tsx`

- [ ] **Step 1: Update the failing test expectation**

In `frontend/src/components/QuickCapture.test.tsx`, change the direct event creation expectation to include `project_id`:

```ts
expect(vi.mocked(eventsApi.createEvent).mock.calls[0]?.[0]).toEqual({
  title: '明天晚上8点复习N2语法',
  start_time: startTime,
  end_time: startTime + 60 * 60,
  location: '学习写小说 · 学习项目',
  kind: 'work',
  project_id: 'learning-1',
})
```

- [ ] **Step 2: Run RED**

Run:

```powershell
cd frontend
npm test -- src/components/QuickCapture.test.tsx
```

Expected: FAIL because `QuickCapture` does not send `project_id` when creating an event.

- [ ] **Step 3: Implement minimal QuickCapture change**

In `frontend/src/components/QuickCapture.tsx`, in the event creation payload, add:

```ts
project_id: selectedProjectID,
```

Keep `kind` for compatibility:

```ts
kind: selectedProject?.type === 'personal' ? 'personal' : 'work',
```

- [ ] **Step 4: Verify GREEN**

Run:

```powershell
cd frontend
npm test -- src/components/QuickCapture.test.tsx
```

Expected: PASS.

---

### Task 9: Historical Migration And Workspace Isolation

**Files:**
- Modify: `backend/internal/storage/sqlite/auth_migrations.go`
- Modify: `backend/internal/storage/postgres/auth_migrations.go`
- Modify: `backend/internal/storage/sqlite/provider.go`
- Modify: `backend/internal/storage/postgres/provider.go`
- Modify: `backend/internal/storage/contracttest/workspace_isolation_contract_tests.go`

- [ ] **Step 1: Write failing isolation test**

Add a contract test that creates two workspaces, creates a regular project in workspace A, and verifies a user scoped to workspace B cannot enable or read that project through `store.Calendar()`.

Expected assertions:

- Workspace A `SaveProjectSources` succeeds for workspace A project.
- Workspace B `SaveProjectSources` with workspace A project ID returns a response without that source.
- Workspace B `ListProjectSources` never includes workspace A project.

- [ ] **Step 2: Run RED**

Run:

```powershell
cd backend
go test ./internal/storage/sqlite -run TestSQLiteWorkspaceIsolationContract/CalendarProjectSourcesCannotCrossWorkspaces -count=1
```

Expected: FAIL until calendar source repository scopes all queries by `workspace_id` and current user.

- [ ] **Step 3: Implement migration and isolation details**

Ensure migrations:

- Add `events.project_id`.
- Create `calendar_project_sources`.
- Add composite indexes.
- Backfill only events where `kind = 'personal'` and the same workspace contains `task_projects.id = 'personal'`.
- Do not backfill `kind = 'work'` or `kind = 'reminder'`.

Ensure repositories:

- Always read current `workspace_id` and `user_id` from context.
- Never accept workspace/user from request body.
- Use `(workspace_id, project_id)` in every project lookup.

- [ ] **Step 4: Verify GREEN**

Run:

```powershell
cd backend
go test ./internal/storage/sqlite -run TestSQLiteWorkspaceIsolationContract/CalendarProjectSourcesCannotCrossWorkspaces -count=1
go test ./internal/storage/postgres -run TestPostgresWorkspaceIsolationContract/CalendarProjectSourcesCannotCrossWorkspaces -count=1
```

Expected: PASS.

---

### Task 10: Full Verification

**Files:**
- No new files.

- [ ] **Step 1: Run backend storage and service suites**

Run:

```powershell
cd backend
go test ./internal/storage/sqlite ./internal/storage/postgres ./internal/service ./internal/handler ./internal/router -count=1
```

Expected: PASS with no unexpected warnings.

- [ ] **Step 2: Run frontend unit tests**

Run:

```powershell
cd frontend
npm test -- src/api/calendar.test.ts src/routes/Calendar.test.tsx src/components/QuickCapture.test.tsx
```

Expected: PASS.

- [ ] **Step 3: Run frontend build**

Run:

```powershell
cd frontend
npm run build
```

Expected: PASS.

- [ ] **Step 4: Manual browser smoke check**

Run the app, then verify:

- Calendar left panel shows project-driven sources, not `工作 / 个人 / 提醒`.
- `Personal` source shows personal project events.
- A regular or learning project appears only after being enabled in the configuration panel.
- Quick Capture creates an event with the selected project and it appears under that project source after the source is enabled.
- Removing a source from the configuration panel hides it from the calendar source list.

- [ ] **Step 5: Final diff review**

Run:

```powershell
git diff --check
git status --short
```

Expected: `git diff --check` exits 0. `git status --short` contains only intentional files for this feature plus any pre-existing unrelated dirty files.

---

## Implementation Notes

- `kind` remains in event create/update payloads and database rows for compatibility.
- `project_id` becomes the calendar source identity.
- `PUT /api/calendar/project-sources` is full-save from the configuration panel. A disabled project must be sent as `enabled: false`.
- JSON null for `project_id` update is treated the same as omitted because Go `*string` cannot distinguish both cases without a custom patch type.
- Empty string clears `project_id`.
- Project deletion must preserve events and only clear `project_id`.
