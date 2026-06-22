# Task 13 Report: Refactor service/tasks.go for recurring create/update + occurrence routes

## Status: COMPLETED

## Changes Made

### 1. Fixed execution_type in SELECT queries (prerequisite)

Both SQLite and Postgres task SELECT queries were missing `t.execution_type` in their column lists, which is required by `UpdateTask` to check the current task's execution type.

- **`backend/internal/storage/sqlite/tasks.go`**:
  - Added `t.execution_type` to `sqliteTaskSelectSQL()` 
  - Added `&task.ExecutionType` to `scanSQLiteTaskRow()`

- **`backend/internal/storage/postgres/tasks.go`**:
  - Added `t.execution_type` to `postgresTaskSelectSQL()`
  - Added `&task.ExecutionType` to `scanPostgresTaskRow()`

### 2. Rewrote service/tasks.go

All service functions now accept `store storage.Store` and `context.Context` parameters:

- **`CreateTask`**: 
  - For `execution_type=recurring`: validates recurrence config, creates task + recurrence rule atomically via `store.Transact`
  - For single tasks: unchanged path, sets `ExecutionType: "single"`
  
- **`UpdateTask`**: Validation guards:
  - Cannot complete recurring template (returns `RecurringTaskError{Code: "CANNOT_COMPLETE_RECURRING_TEMPLATE"}`)
  - Cannot switch recurring→single (returns `RecurringTaskError{Code: "CANNOT_SWITCH_RECURRING_TO_SINGLE"}`)
  - Single→recurring switch: validates recurrence, clears planned_date
  - Recurrence config upserted within Transact when applicable
  - Enabled/EndDate changes for existing rules handled within Transact

- **`RecurringTaskError`**: New typed error with Code and Message fields for structured 409 responses

- **`recurrenceConfigToRule`**: Helper to convert API `RecurrenceConfig` to domain `RecurrenceRule` with defaults

### 3. Updated handler/tasks.go

- All existing handler functions updated to get `Store` from `repository.ActiveStore()` and pass it to service
- `UpdateTask` handler detects `*service.RecurringTaskError` and returns 409 Conflict
- Added `conflict()` helper to `handler/helpers.go`

**New occurrence handlers:**

- **`CompleteOccurrence`** (POST /api/tasks/:id/occurrences/:date/complete): Validates task is recurring, date is expected occurrence, marks as completed
- **`ReopenOccurrence`** (POST /api/tasks/:id/occurrences/:date/reopen): Reopens completed/skipped occurrence
- **`SkipOccurrence`** (POST /api/tasks/:id/occurrences/:date/skip): Marks occurrence as skipped
- **`GetTaskOccurrences`** (GET /api/task-occurrences?from=...&to=...): Lists occurrences in date range

- **`validateOccurrenceAction`**: Shared validation: checks task exists, is recurring, rule exists, date is valid occurrence via `ExpandRuleOccurrences`

### 4. Updated router/router.go

4 new routes registered after existing task routes:

```go
api.POST("/tasks/:id/occurrences/:date/complete", handler.CompleteOccurrence)
api.POST("/tasks/:id/occurrences/:date/reopen", handler.ReopenOccurrence)
api.POST("/tasks/:id/occurrences/:date/skip", handler.SkipOccurrence)
api.GET("/task-occurrences", handler.GetTaskOccurrences)
```

### 5. Added handler/helpers.go

Added `conflict(c *gin.Context, code, msg string)` helper for 409 Conflict HTTP responses.

## Build Verification

```
cd backend && go build ./...
```
Compiles successfully with no errors.

## Files Modified

- `backend/internal/storage/sqlite/tasks.go` — execution_type in SELECT + scan
- `backend/internal/storage/postgres/tasks.go` — execution_type in SELECT + scan
- `backend/internal/service/tasks.go` — complete rewrite with Store param + recurring support
- `backend/internal/handler/tasks.go` — Store passing + 4 occurrence handlers
- `backend/internal/handler/helpers.go` — conflict() helper
- `backend/internal/router/router.go` — 4 new occurrence routes

## Critical Bug Fixes (2026-06-21)

### Bug 1: SQLite Update panics inside Transact

`sqlite/tasks.go` `Update` was doing a hard `r.db.(*sql.DB)` assertion and manually calling `db.BeginTx`. When called inside `Store.Transact`, `r.db` is `*sql.Tx`, so the assertion failed.

**Fix**: Refactored `Update` to use `r.withTx()` (same pattern as `Create`), which handles both `*sql.DB` and `*sql.Tx`. The read-back of the updated task now happens inside the transaction via `tx.QueryRowContext` instead of calling `GetByID` (which uses `r.db`).

### Bug 2: execution_type not persisted on Update

Both `postgres/tasks.go` and `sqlite/tasks.go` `Update` methods were missing `execution_type` in their dynamic SET builders. When `service.UpdateTask` handled a single-to-recurring conversion, it called `txStore.Tasks().Update()` with `req.ExecutionType` set, but the value was never written to the database.

**Fix**: Added `req.ExecutionType` to the dynamic SET clause in both backends. Column name: `execution_type`.

## Verification

```
cd backend && go build ./...     # compiles
cd backend && go test ./internal/storage/sqlite/ -run TestTask -v -count=1  # PASS
```
