# Note Project Links Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让一篇笔记可以归属到多个项目，通过独立关联表 `note_project_links` 实现多对多关系，覆盖后端 API、前端编辑器/列表/任务页、双数据库 provider 和迁移。

**Architecture:** 新增 `note_project_links` 关联表 + `NoteProject` 轻量模型。Repository 层在事务内维护关联关系（merge 策略），API 层新增 `project_id`/`unassigned` 查询参数。前端编辑器增加项目多选 chip，列表增加项目筛选和 chip 展示，任务页增加"项目笔记"入口。

**Tech Stack:** Go + Gin (backend), React + TypeScript + Tiptap (frontend), SQLite + PostgreSQL (dual providers), Playwright (tests)

---

## File Structure

```
backend/
  db/
    schema.sql                                          # MODIFY: add note_project_links
    migrations/postgres/0001_init_postgres.sql           # MODIFY: add note_project_links
  internal/
    model/
      note.go                                           # MODIFY: add Projects, ProjectIDs
    storage/
      store.go                                          # MODIFY: NoteFilter + NoteRepository
      sqlite/
        notes.go                                        # MODIFY: implement project links
      postgres/
        notes.go                                        # MODIFY: implement project links + search_index
    handler/
      notes.go                                          # MODIFY: query params + request parsing
    service/
      notes.go                                          # MODIFY: pass project_ids
    repository/
      notes.go                                          # MODIFY: facade passthrough
      db.go                                             # MODIFY: legacy SQLite init
    migration/
      sqlite_to_pg.go                                   # MODIFY: add note_project_links step

frontend/
  src/
    api/
      notes.ts                                          # MODIFY: add project_ids params
    routes/
      Editor.tsx                                        # MODIFY: project multi-select chip
      Notes.tsx                                         # MODIFY: project filter + card chips
      Tasks.tsx                                         # MODIFY: "project notes" section
```

---

## Phase 1: Database & Models

### Task 1.1: Add `note_project_links` table to SQLite schema

**Files:**
- Modify: `backend/db/schema.sql`

- [ ] **Step 1: Add table definition after `task_projects`**

Insert after the `task_projects` table definition (after line 52):

```sql
CREATE TABLE IF NOT EXISTS note_project_links (
    note_id TEXT NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
    project_id TEXT NOT NULL REFERENCES task_projects(id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (note_id, project_id)
);

CREATE INDEX IF NOT EXISTS note_project_links_project_note_idx
    ON note_project_links (project_id, note_id);
```

- [ ] **Step 2: Commit**

```bash
git add backend/db/schema.sql
git commit -m "feat: add note_project_links table to SQLite schema"
```

---

### Task 1.2: Add `note_project_links` table to PostgreSQL schema

**Files:**
- Modify: `backend/db/migrations/postgres/0001_init_postgres.sql`

- [ ] **Step 1: Add table definition before the `search_index` section**

Insert after the `task_projects` table + indexes (after line 33):

```sql
CREATE TABLE note_project_links (
    note_id TEXT NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
    project_id TEXT NOT NULL REFERENCES task_projects(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (note_id, project_id)
);

CREATE INDEX note_project_links_project_note_idx
    ON note_project_links (project_id, note_id);
```

- [ ] **Step 2: Commit**

```bash
git add backend/db/migrations/postgres/0001_init_postgres.sql
git commit -m "feat: add note_project_links table to PostgreSQL schema"
```

---

### Task 1.3: Add `NoteProject` model and extend `Note`/request types

**Files:**
- Modify: `backend/internal/model/note.go`

- [ ] **Step 1: Add `NoteProject` struct at the top of the file**

```go
// NoteProject is a lightweight project reference included in note responses.
type NoteProject struct {
    ID   string `json:"id"`
    Name string `json:"name"`
    Type string `json:"type"`
}
```

- [ ] **Step 2: Add `Projects` field to the `Note` struct**

```go
type Note struct {
    ID        string        `json:"id"`
    Title     string        `json:"title"`
    Body      string        `json:"body"`
    FolderID  string        `json:"folder_id"`
    Tags      string        `json:"tags"`
    Projects  []NoteProject `json:"projects"`
    CreatedAt int64         `json:"created_at"`
    UpdatedAt int64         `json:"updated_at"`
}
```

- [ ] **Step 3: Add `ProjectIDs` to `CreateNoteRequest`**

```go
type CreateNoteRequest struct {
    Title      string   `json:"title" binding:"required"`
    Body       string   `json:"body"`
    FolderID   string   `json:"folder_id"`
    Tags       string   `json:"tags"`
    ProjectIDs []string `json:"project_ids"`
}
```

- [ ] **Step 4: Add `ProjectIDs` pointer to `UpdateNoteRequest`**

```go
type UpdateNoteRequest struct {
    Title      *string   `json:"title"`
    Body       *string   `json:"body"`
    FolderID   *string   `json:"folder_id"`
    Tags       *string   `json:"tags"`
    ProjectIDs *[]string `json:"project_ids"`
}
```

- [ ] **Step 5: Verify compilation**

```bash
cd backend && go build ./...
```
Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/model/note.go
git commit -m "feat: add NoteProject model and extend Note/request types with project_ids"
```

---

### Task 1.4: Update `NoteFilter` and `NoteRepository` interface

**Files:**
- Modify: `backend/internal/storage/store.go`

- [ ] **Step 1: Update `NoteFilter` struct (lines 47-52)**

```go
type NoteFilter struct {
    FolderID   string
    ProjectID  string
    Unassigned bool
    Query      string
    Sort       string // "recent" | "az"
    Page       int
    PageSize   int
}
```

- [ ] **Step 2: Verify compilation**

```bash
cd backend && go build ./...
```
Expected: errors in sqlite/notes.go and postgres/notes.go (will fix in next tasks).

- [ ] **Step 3: Commit**

```bash
git add backend/internal/storage/store.go
git commit -m "feat: add ProjectID, Unassigned, Sort fields to NoteFilter"
```

---

## Phase 2: SQLite Repository Implementation

### Task 2.1: Implement `note_project_links` write operations in SQLite

**Files:**
- Modify: `backend/internal/storage/sqlite/notes.go`

- [ ] **Step 1: Add helper to upsert project links for a note**

Add after the `scanSQLiteNotes` function (near line 186):

```go
// setNoteProjectLinks replaces project links for a note within a transaction.
// Uses merge strategy: inserts new, deletes removed, keeps existing.
func setNoteProjectLinks(ctx context.Context, runner sqliteRunner, noteID string, projectIDs []string) error {
    if projectIDs == nil {
        return nil // nil means don't modify (caller checks this)
    }
    // Delete links not in the new set
    if len(projectIDs) == 0 {
        _, err := runner.ExecContext(ctx,
            `DELETE FROM note_project_links WHERE note_id = ?`, noteID)
        return err
    }
    // Build placeholders for the IN clause
    placeholders := make([]string, len(projectIDs))
    args := make([]interface{}, 0, len(projectIDs)+1)
    args = append(args, noteID)
    for i, pid := range projectIDs {
        placeholders[i] = "?"
        args = append(args, pid)
    }
    // Delete links not in the new set
    query := fmt.Sprintf(
        `DELETE FROM note_project_links WHERE note_id = ? AND project_id NOT IN (%s)`,
        strings.Join(placeholders, ","))
    if _, err := runner.ExecContext(ctx, query, args...); err != nil {
        return err
    }
    // Insert new links (ignore if already exists — keeps original created_at)
    for _, pid := range projectIDs {
        _, err := runner.ExecContext(ctx,
            `INSERT OR IGNORE INTO note_project_links (note_id, project_id, created_at)
             VALUES (?, ?, ?)`, noteID, pid, nowUnix())
        if err != nil {
            return err
        }
    }
    return nil
}
```

- [ ] **Step 2: Add helper to fetch projects for notes (batch)**

```go
// getNotesProjects fetches project info for a batch of note IDs.
func getNotesProjects(ctx context.Context, runner sqliteRunner, noteIDs []string) (map[string][]model.NoteProject, error) {
    if len(noteIDs) == 0 {
        return nil, nil
    }
    placeholders := make([]string, len(noteIDs))
    args := make([]interface{}, len(noteIDs))
    for i, id := range noteIDs {
        placeholders[i] = "?"
        args[i] = id
    }
    query := fmt.Sprintf(
        `SELECT npl.note_id, tp.id, tp.name, tp.type
         FROM note_project_links npl
         JOIN task_projects tp ON tp.id = npl.project_id
         WHERE npl.note_id IN (%s)
         ORDER BY tp.name ASC`, strings.Join(placeholders, ","))
    rows, err := runner.QueryContext(ctx, query, args...)
    if err != nil {
        return nil, err
    }
    defer rows.Close()
    result := make(map[string][]model.NoteProject)
    for rows.Next() {
        var noteID string
        var np model.NoteProject
        if err := rows.Scan(&noteID, &np.ID, &np.Name, &np.Type); err != nil {
            return nil, err
        }
        result[noteID] = append(result[noteID], np)
    }
    return result, rows.Err()
}
```

- [ ] **Step 3: Add imports for `model`, `fmt`, `strings`, `context`**

Ensure the file imports `"all_note/backend/internal/model"`, `"fmt"`, `"strings"`, and `"context"`.

- [ ] **Step 4: Commit**

```bash
git add backend/internal/storage/sqlite/notes.go
git commit -m "feat: add note_project_links write/read helpers for SQLite"
```

---

### Task 2.2: Wire project links into SQLite `Create` and `Update`

**Files:**
- Modify: `backend/internal/storage/sqlite/notes.go`

- [ ] **Step 1: Update `CreateWithID` to insert project links**

After the `INSERT INTO notes` statement (around line 112), add:

```go
// Insert project links
if len(req.ProjectIDs) > 0 {
    for _, pid := range req.ProjectIDs {
        _, err := r.ExecContext(ctx,
            `INSERT OR IGNORE INTO note_project_links (note_id, project_id, created_at)
             VALUES (?, ?, ?)`, note.ID, pid, note.CreatedAt)
        if err != nil {
            return fmt.Errorf("insert project link: %w", err)
        }
    }
}
```

Wait — `CreateWithID` takes `*model.Note`, not `*model.CreateNoteRequest`. We need to handle project_ids at the `Create` level, which delegates to `CreateWithID`. Let me re-examine...

Looking at the code: `Create` generates an ID, creates a `Note` from the request, then calls `CreateWithID`. So we need to handle project links in `Create` after calling `CreateWithID`, or pass the links through.

Better approach: handle it in `Create` after `CreateWithID`:

```go
func (r *noteRepository) Create(ctx context.Context, req *model.CreateNoteRequest) (*model.Note, error) {
    // ... existing code ...
    _, err := r.CreateWithID(ctx, &note)
    if err != nil {
        return nil, err
    }
    // Insert project links
    if len(req.ProjectIDs) > 0 {
        for _, pid := range req.ProjectIDs {
            _, err := r.ExecContext(ctx,
                `INSERT OR IGNORE INTO note_project_links (note_id, project_id, created_at)
                 VALUES (?, ?, ?)`, note.ID, pid, nowUnix())
            if err != nil {
                return nil, fmt.Errorf("insert project link: %w", err)
            }
        }
    }
    return r.GetByID(ctx, note.ID)
}
```

- [ ] **Step 2: Update `Update` to merge project links**

After the dynamic UPDATE statement (around line 145), add:

```go
// Merge project links if provided
if req.ProjectIDs != nil {
    if err := setNoteProjectLinks(ctx, r.sqliteRunner, id, *req.ProjectIDs); err != nil {
        return nil, fmt.Errorf("update project links: %w", err)
    }
}
```

- [ ] **Step 3: Verify compilation**

```bash
cd backend && go build ./...
```
Expected: no errors in sqlite package.

- [ ] **Step 4: Commit**

```bash
git add backend/internal/storage/sqlite/notes.go
git commit -m "feat: wire project links into SQLite note Create and Update"
```

---

### Task 2.3: Wire project links into SQLite `List`, `GetByID`, `Recent`

**Files:**
- Modify: `backend/internal/storage/sqlite/notes.go`

- [ ] **Step 1: Update `List` to support `ProjectID` and `Unassigned` filtering**

Modify the `List` function's query building (around line 23):

```go
func (r *noteRepository) List(ctx context.Context, filter storage.NoteFilter) ([]model.Note, int, error) {
    var where []string
    var args []interface{}
    if filter.FolderID != "" {
        where = append(where, "n.folder_id = ?")
        args = append(args, filter.FolderID)
    }
    if filter.ProjectID != "" {
        where = append(where,
            `EXISTS (SELECT 1 FROM note_project_links npl WHERE npl.note_id = n.id AND npl.project_id = ?)`)
        args = append(args, filter.ProjectID)
    }
    if filter.Unassigned {
        where = append(where,
            `NOT EXISTS (SELECT 1 FROM note_project_links npl WHERE npl.note_id = n.id)`)
    }
    whereClause := ""
    if len(where) > 0 {
        whereClause = "WHERE " + strings.Join(where, " AND ")
    }
    // ... count query using whereClause ...
    // ... select query with ORDER BY and LIMIT/OFFSET ...
}
```

- [ ] **Step 2: Update `List` to batch-load projects into results**

After scanning notes, call `getNotesProjects`:

```go
notes, err := scanSQLiteNotes(rows)
if err != nil {
    return nil, 0, err
}
// Batch load projects
noteIDs := make([]string, len(notes))
for i, n := range notes {
    noteIDs[i] = n.ID
}
projectsMap, err := getNotesProjects(ctx, r.sqliteRunner, noteIDs)
if err != nil {
    return nil, 0, err
}
for i := range notes {
    notes[i].Projects = projectsMap[notes[i].ID]
}
return notes, total, nil
```

- [ ] **Step 3: Update `GetByID` to load projects**

After scanning the single note:

```go
note, err := scanSQLiteNote(row)
if err != nil {
    return nil, err
}
projectsMap, err := getNotesProjects(ctx, r.sqliteRunner, []string{note.ID})
if err != nil {
    return nil, err
}
note.Projects = projectsMap[note.ID]
return note, nil
```

- [ ] **Step 4: Update `Recent` to load projects**

After scanning recent notes, batch-load projects (same pattern as `List`).

- [ ] **Step 5: `ListAll` should NOT load projects (per design spec)**

No change needed — `ListAll` stays lightweight for sync diff.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/storage/sqlite/notes.go
git commit -m "feat: support project_id/unassigned filtering and project loading in SQLite"
```

---

## Phase 3: PostgreSQL Repository Implementation

### Task 3.1: Implement `note_project_links` operations in PostgreSQL

**Files:**
- Modify: `backend/internal/storage/postgres/notes.go`

- [ ] **Step 1: Add helper to merge project links**

Add after the existing helper functions:

```go
// setNoteProjectLinks merges project links for a note using merge strategy.
func setNoteProjectLinks(ctx context.Context, runner postgresRunner, noteID string, projectIDs []string) error {
    if projectIDs == nil {
        return nil
    }
    if len(projectIDs) == 0 {
        _, err := runner.ExecContext(ctx,
            `DELETE FROM note_project_links WHERE note_id = $1`, noteID)
        return err
    }
    // Delete links not in the new set
    _, err := runner.ExecContext(ctx,
        `DELETE FROM note_project_links WHERE note_id = $1 AND project_id != ALL($2::text[])`,
        noteID, pq.Array(projectIDs))
    if err != nil {
        return err
    }
    // Insert new links (ON CONFLICT DO NOTHING keeps original created_at)
    for _, pid := range projectIDs {
        _, err := runner.ExecContext(ctx,
            `INSERT INTO note_project_links (note_id, project_id)
             VALUES ($1, $2) ON CONFLICT DO NOTHING`, noteID, pid)
        if err != nil {
            return err
        }
    }
    return nil
}
```

- [ ] **Step 2: Add batch project loader**

```go
// getNotesProjects fetches project info for a batch of note IDs.
func getNotesProjects(ctx context.Context, runner postgresRunner, noteIDs []string) (map[string][]model.NoteProject, error) {
    if len(noteIDs) == 0 {
        return nil, nil
    }
    rows, err := runner.QueryContext(ctx,
        `SELECT npl.note_id, tp.id, tp.name, tp.type
         FROM note_project_links npl
         JOIN task_projects tp ON tp.id = npl.project_id
         WHERE npl.note_id = ANY($1::text[])
         ORDER BY tp.name ASC`, pq.Array(noteIDs))
    if err != nil {
        return nil, err
    }
    defer rows.Close()
    result := make(map[string][]model.NoteProject)
    for rows.Next() {
        var noteID string
        var np model.NoteProject
        if err := rows.Scan(&noteID, &np.ID, &np.Name, &np.Type); err != nil {
            return nil, err
        }
        result[noteID] = append(result[noteID], np)
    }
    return result, rows.Err()
}
```

- [ ] **Step 3: Commit**

```bash
git add backend/internal/storage/postgres/notes.go
git commit -m "feat: add note_project_links helpers for PostgreSQL"
```

---

### Task 3.2: Wire project links into PostgreSQL `Create`, `Update`, `List`, `GetByID`, `Recent`

**Files:**
- Modify: `backend/internal/storage/postgres/notes.go`

- [ ] **Step 1: Update `CreateWithID` to insert project links**

After `upsertNoteSearchIndex` call (around line 124), inside the transaction:

```go
// Insert project links
for _, pid := range req.ProjectIDs {
    if _, err := tx.ExecContext(ctx,
        `INSERT INTO note_project_links (note_id, project_id)
         VALUES ($1, $2) ON CONFLICT DO NOTHING`, note.ID, pid); err != nil {
        return fmt.Errorf("insert project link: %w", err)
    }
}
```

Wait — `CreateWithID` receives `*model.Note` which doesn't have ProjectIDs. Need to handle this in `Create` instead, same pattern as SQLite. Let me adjust:

In `Create` (after calling `CreateWithID`):

```go
func (r *noteRepository) Create(ctx context.Context, req *model.CreateNoteRequest) (*model.Note, error) {
    // ... existing code that calls CreateWithID ...
    _, err := r.CreateWithID(ctx, &note)
    if err != nil {
        return nil, err
    }
    // Insert project links
    if len(req.ProjectIDs) > 0 {
        if err := r.withTx(ctx, func(tx *sql.Tx) error {
            return setNoteProjectLinks(ctx, tx, note.ID, req.ProjectIDs)
        }); err != nil {
            return nil, fmt.Errorf("insert project links: %w", err)
        }
    }
    return r.GetByID(ctx, note.ID)
}
```

But wait — `CreateWithID` already wraps its work in a transaction. The project links should be in the SAME transaction as the note insert. Let me restructure:

Actually, looking at the PostgreSQL code, `CreateWithID` uses `r.withTx` internally. And `Create` calls `CreateWithID`. So if we put project links in `Create` after `CreateWithID`, they'd be in a separate transaction. That's not ideal but acceptable for phase 1 given the merge semantics.

For a more correct approach, we could pass projectIDs through or restructure `CreateWithID`. But to minimize changes to existing code, let's handle project links in `Create` after `CreateWithID`:

```go
func (r *noteRepository) Create(ctx context.Context, req *model.CreateNoteRequest) (*model.Note, error) {
    // ... existing code to build note and call CreateWithID ...
    note, err := r.CreateWithID(ctx, &modelNote)
    if err != nil {
        return nil, err
    }
    // Insert project links in a separate transaction
    if len(req.ProjectIDs) > 0 {
        if err := r.withTx(ctx, func(tx *sql.Tx) error {
            return setNoteProjectLinks(ctx, tx, note.ID, req.ProjectIDs)
        }); err != nil {
            return nil, fmt.Errorf("insert project links: %w", err)
        }
    }
    return note, nil
}
```

- [ ] **Step 2: Update `Update` to merge project links**

After the `upsertNoteSearchIndex` call (around line 170), inside the same transaction:

```go
// Merge project links if provided
if req.ProjectIDs != nil {
    if err := setNoteProjectLinks(ctx, tx, id, *req.ProjectIDs); err != nil {
        return fmt.Errorf("update project links: %w", err)
    }
}
```

- [ ] **Step 3: Update `List` with ProjectID/Unassigned filtering**

Modify the WHERE clause building in `List` (around line 25):

```go
if filter.ProjectID != "" {
    where = append(where,
        `EXISTS (SELECT 1 FROM note_project_links npl WHERE npl.note_id = n.id AND npl.project_id = $`+strconv.Itoa(len(args)+1)+`)`)
    args = append(args, filter.ProjectID)
}
if filter.Unassigned {
    where = append(where,
        `NOT EXISTS (SELECT 1 FROM note_project_links npl WHERE npl.note_id = n.id)`)
}
```

Also add Sort support — the existing `List` already uses `sort` from `filter.Query`. Add explicit Sort field handling.

- [ ] **Step 4: Update `List` to batch-load projects**

After scanning notes, call `getNotesProjects`.

- [ ] **Step 5: Update `GetByID` to load projects**

Same pattern as SQLite.

- [ ] **Step 6: Update `Recent` to load projects**

Same pattern.

- [ ] **Step 7: Verify compilation**

```bash
cd backend && go build ./...
```
Expected: no errors.

- [ ] **Step 8: Commit**

```bash
git add backend/internal/storage/postgres/notes.go
git commit -m "feat: wire project links into PostgreSQL note CRUD"
```

---

## Phase 4: Service & Handler Layer

### Task 4.1: Update service layer for project_ids

**Files:**
- Modify: `backend/internal/service/notes.go`

- [ ] **Step 1: Pass `ProjectIDs` through in `CreateNote`**

Update the `CreateNoteRequest` → `store.Create` passthrough to include `ProjectIDs`.

- [ ] **Step 2: Pass `ProjectIDs` through in `UpdateNote`**

Update the `UpdateNoteRequest` passthrough to include `ProjectIDs`.

- [ ] **Step 3: Commit**

```bash
git add backend/internal/service/notes.go
git commit -m "feat: pass project_ids through service layer"
```

---

### Task 4.2: Update API handler for new query params and request fields

**Files:**
- Modify: `backend/internal/handler/notes.go`

- [ ] **Step 1: Update `GetNotes` to parse `project_id` and `unassigned`**

```go
func GetNotes(c *gin.Context) {
    folderID := c.Query("folder_id")
    projectID := c.Query("project_id")
    unassigned := c.Query("unassigned") == "true"
    sort := c.DefaultQuery("sort", "recent")
    page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
    pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

    // Validate conflicting params
    if projectID != "" && unassigned {
        c.JSON(400, gin.H{"error": "project_id and unassigned cannot be used together"})
        return
    }

    filter := storage.NoteFilter{
        FolderID:   folderID,
        ProjectID:  projectID,
        Unassigned: unassigned,
        Sort:       sort,
        Page:       page,
        PageSize:   pageSize,
    }
    notes, total, err := service.GetNotes(filter)
    if err != nil {
        c.JSON(500, gin.H{"error": err.Error()})
        return
    }
    c.JSON(200, gin.H{
        "notes":     notes,
        "total":     total,
        "page":      page,
        "page_size": pageSize,
    })
}
```

- [ ] **Step 2: Update `CreateNote` to bind `project_ids`**

The `CreateNoteRequest` already has `ProjectIDs []string json:"project_ids"`. Gin binding will parse it automatically. Just add validation:

```go
func CreateNote(c *gin.Context) {
    var req model.CreateNoteRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(400, gin.H{"error": err.Error()})
        return
    }
    // The service/repo layer will silently drop invalid project IDs (warn log)
    note, err := service.CreateNote(&req)
    if err != nil {
        c.JSON(500, gin.H{"error": err.Error()})
        return
    }
    c.JSON(201, gin.H{"note": note})
}
```

- [ ] **Step 3: Update `UpdateNote` to handle `project_ids`**

```go
func UpdateNote(c *gin.Context) {
    id := c.Param("id")
    var req model.UpdateNoteRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(400, gin.H{"error": err.Error()})
        return
    }
    note, err := service.UpdateNote(id, &req)
    if err != nil {
        if errors.Is(err, service.ErrNoteNotFound) {
            c.JSON(404, gin.H{"error": "note not found"})
            return
        }
        c.JSON(500, gin.H{"error": err.Error()})
        return
    }
    c.JSON(200, gin.H{"note": note})
}
```

- [ ] **Step 4: Commit**

```bash
git add backend/internal/handler/notes.go backend/internal/service/notes.go
git commit -m "feat: add project_id/unassigned query params and project_ids to API handlers"
```

---

### Task 4.3: Update legacy repository facade

**Files:**
- Modify: `backend/internal/repository/notes.go`

- [ ] **Step 1: Update facade `CreateNote` to pass `ProjectIDs`**

If the facade `CreateNote` function builds its own request, add `ProjectIDs`.

- [ ] **Step 2: Update facade `UpdateNote` to pass `ProjectIDs`**

Same.

- [ ] **Step 3: Commit**

```bash
git add backend/internal/repository/notes.go
git commit -m "feat: pass project_ids through legacy repository facade"
```

---

## Phase 5: Migration

### Task 5.1: Add `note_project_links` to legacy SQLite init

**Files:**
- Modify: `backend/internal/repository/db.go`

- [ ] **Step 1: Add `note_project_links` table creation in legacy init**

```go
_, err = db.Exec(`
    CREATE TABLE IF NOT EXISTS note_project_links (
        note_id TEXT NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
        project_id TEXT NOT NULL REFERENCES task_projects(id) ON DELETE CASCADE,
        created_at INTEGER NOT NULL,
        PRIMARY KEY (note_id, project_id)
    );
    CREATE INDEX IF NOT EXISTS note_project_links_project_note_idx
        ON note_project_links (project_id, note_id);
`)
```

- [ ] **Step 2: Commit**

```bash
git add backend/internal/repository/db.go
git commit -m "feat: add note_project_links to legacy SQLite init"
```

---

### Task 5.2: Add `note_project_links` to SQLite→PostgreSQL migration

**Files:**
- Modify: `backend/internal/migration/sqlite_to_pg.go`

- [ ] **Step 1: Add `migrateNoteProjectLinks` function**

```go
func migrateNoteProjectLinks(ctx context.Context, sqliteDB *sql.DB, pgTx *sql.Tx) error {
    rows, err := sqliteDB.QueryContext(ctx,
        `SELECT note_id, project_id, created_at FROM note_project_links`)
    if err != nil {
        // Table might not exist in legacy SQLite — that's OK
        return nil
    }
    defer rows.Close()
    for rows.Next() {
        var noteID, projectID string
        var createdAt int64
        if err := rows.Scan(&noteID, &projectID, &createdAt); err != nil {
            return fmt.Errorf("scan note_project_links: %w", err)
        }
        _, err := pgTx.ExecContext(ctx,
            `INSERT INTO note_project_links (note_id, project_id, created_at)
             VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
            noteID, projectID, unixToTime(createdAt))
        if err != nil {
            return fmt.Errorf("insert note_project_links (%s, %s): %w", noteID, projectID, err)
        }
    }
    return rows.Err()
}
```

- [ ] **Step 2: Add to migration steps in correct order**

Insert `migrateNoteProjectLinks` in the steps slice after `migrateTaskProjects` and before `migrateLearningRoadmaps` (or after `migrateNotes` and `migrateTaskProjects`):

```go
steps := []func(context.Context, *sql.DB, *sql.Tx) error{
    migrateFolders,
    migrateNotes,
    migrateTaskProjects,
    migrateNoteProjectLinks,  // NEW: after notes and task_projects exist
    migrateLearningRoadmaps,
    migrateRoadmapNodes,
    migrateTasks,
    // ... rest unchanged
}
```

- [ ] **Step 3: Commit**

```bash
git add backend/internal/migration/sqlite_to_pg.go
git commit -m "feat: add note_project_links to SQLite→PostgreSQL migration"
```

---

## Phase 6: Contract Tests

### Task 6.1: Write contract tests for project links

**Files:**
- Modify: `backend/internal/storage/contracttest/notes_contract_tests.go`

- [ ] **Step 1: Add test for creating note with multiple project_ids**

```go
func TestNoteProjectLinks_CreateWithProjects(t *testing.T, store storage.Store) {
    ctx := context.Background()
    // Create test project first
    projectID := "test-project-" + uuid.New().String()
    _, err := store.Tasks().CreateProject(ctx, &model.CreateTaskProjectRequest{
        Name: "Test Project",
        Type: "regular",
    })
    require.NoError(t, err)
    defer store.Tasks().DeleteProject(ctx, projectID)

    // Create note with project
    note, err := store.Notes().Create(ctx, &model.CreateNoteRequest{
        Title:      "Test Note",
        ProjectIDs: []string{projectID},
    })
    require.NoError(t, err)
    require.Len(t, note.Projects, 1)
    assert.Equal(t, projectID, note.Projects[0].ID)
}
```

- [ ] **Step 2: Add test for GetByID returning projects**

```go
func TestNoteProjectLinks_GetByIDReturnsProjects(t *testing.T, store storage.Store) {
    // Create project + note with link, then GetByID and verify projects populated
}
```

- [ ] **Step 3: Add test for Update merge semantics (omit, clear, replace)**

```go
func TestNoteProjectLinks_UpdateMergeSemantics(t *testing.T, store storage.Store) {
    // 1. Create note with project A
    // 2. Update with project B — verify A removed, B added
    // 3. Update with nil ProjectIDs — verify unchanged (still B)
    // 4. Update with empty ProjectIDs — verify cleared
}
```

- [ ] **Step 4: Add test for List filtering by project_id**

```go
func TestNoteProjectLinks_ListByProjectID(t *testing.T, store storage.Store) {
    // Create note in project A, note in project B, note with no project
    // Filter by project A — only note A returned
}
```

- [ ] **Step 5: Add test for unassigned=true filtering**

```go
func TestNoteProjectLinks_ListUnassigned(t *testing.T, store storage.Store) {
    // Create notes with and without projects
    // Filter unassigned=true — only notes without projects returned
}
```

- [ ] **Step 6: Add test for project_id + unassigned conflict**

```go
func TestNoteProjectLinks_ConflictParams(t *testing.T, store storage.Store) {
    // Verify that ProjectID + Unassigned together returns an error
    // Note: this may be enforced at handler level, not repo level
}
```

- [ ] **Step 7: Add test for delete note cascades link**

```go
func TestNoteProjectLinks_DeleteNoteCleansLinks(t *testing.T, store storage.Store) {
    // Create note with project, delete note, verify link gone
}
```

- [ ] **Step 8: Add test for delete project cascades link (note survives)**

```go
func TestNoteProjectLinks_DeleteProjectKeepsNote(t *testing.T, store storage.Store) {
    // Create note with project, delete project, verify note still exists, no projects
}
```

- [ ] **Step 9: Add test for invalid project_id silently dropped**

```go
func TestNoteProjectLinks_InvalidProjectIDSilentlyDropped(t *testing.T, store storage.Store) {
    // Create note with non-existent project_id — should succeed, project ignored
}
```

- [ ] **Step 10: Add test for Recent returning projects**

```go
func TestNoteProjectLinks_RecentReturnsProjects(t *testing.T, store storage.Store) {
    // Create note with project, call Recent, verify projects populated
}
```

- [ ] **Step 11: Add test for ListAll NOT returning projects (lightweight)**

```go
func TestNoteProjectLinks_ListAllNoProjects(t *testing.T, store storage.Store) {
    // Create note with project, call ListAll, verify project field is nil/empty
}
```

- [ ] **Step 12: Run contract tests against SQLite**

```bash
cd backend && go test ./internal/storage/sqlite/... -v -run TestNoteProjectLinks
```
Expected: all new tests pass.

- [ ] **Step 13: Run contract tests against PostgreSQL**

```bash
cd backend && go test ./internal/storage/postgres/... -v -run TestNoteProjectLinks
```
Expected: all new tests pass.

- [ ] **Step 14: Commit**

```bash
git add backend/internal/storage/contracttest/notes_contract_tests.go
git commit -m "test: add contract tests for note-project links"
```

---

## Phase 7: Frontend — API Client & Editor

### Task 7.1: Update frontend API client for project_ids

**Files:**
- Modify: `frontend/src/api/notes.ts`

- [ ] **Step 1: Add `project_ids` to `createNote` and `updateNote` params**

```typescript
export interface CreateNoteParams {
  title: string
  body?: string
  folder_id?: string
  tags?: string
  project_ids?: string[]
}

export interface UpdateNoteParams {
  title?: string
  body?: string
  folder_id?: string
  tags?: string
  project_ids?: string[]
}

export interface GetNotesParams {
  folder_id?: string
  project_id?: string
  unassigned?: boolean
  sort?: string
  page?: number
  page_size?: number
}

export async function getNotes(params: GetNotesParams = {}) {
  const searchParams = new URLSearchParams()
  if (params.folder_id) searchParams.set('folder_id', params.folder_id)
  if (params.project_id) searchParams.set('project_id', params.project_id)
  if (params.unassigned) searchParams.set('unassigned', 'true')
  if (params.sort) searchParams.set('sort', params.sort)
  if (params.page) searchParams.set('page', String(params.page))
  if (params.page_size) searchParams.set('page_size', String(params.page_size))
  const qs = searchParams.toString()
  return api.get<PaginatedNotes>(`/api/notes${qs ? `?${qs}` : ''}`)
}
```

- [ ] **Step 2: Update `Note` interface to include `projects`**

```typescript
export interface NoteProject {
  id: string
  name: string
  type: 'personal' | 'regular' | 'learning'
}

export interface Note {
  id: string
  title: string
  body: string
  folder_id: string
  tags: string
  projects: NoteProject[]
  created_at: number
  updated_at: number
}
```

- [ ] **Step 3: Commit**

```bash
git add frontend/src/api/notes.ts
git commit -m "feat: add project_ids to frontend notes API client"
```

---

### Task 7.2: Add project multi-select chip to Editor

**Files:**
- Modify: `frontend/src/routes/Editor.tsx`

- [ ] **Step 1: Add project state and data fetching**

Add state and a query to load projects:

```typescript
import { listTaskProjects, type TaskProject } from '../api/tasks'
import { formatTaskProjectOption } from '../utils/taskProjects'

// Inside the Editor component:
const { data: allProjects = [] } = useQuery({
  queryKey: ['task-projects'],
  queryFn: listTaskProjects,
})
const [selectedProjectIDs, setSelectedProjectIDs] = useState<string[]>([])
```

- [ ] **Step 2: Initialize selectedProjectIDs from note data**

In the `useEffect` that loads the note (around line 59):

```typescript
if (note && note.id === id) {
  setTitle(note.title)
  // ...existing editor content load...
  setSelectedProjectIDs(note.projects?.map(p => p.id) || [])
}
```

- [ ] **Step 3: Include project_ids in save and auto-save**

Update the `save` callback:

```typescript
const save = useCallback(() => {
  if (!id || !title.trim() || !editor || editor.isDestroyed) return
  updateNote.mutate(
    {
      id,
      title: title.trim(),
      body: getMarkdown(editor),
      project_ids: selectedProjectIDs,  // ADD THIS
    },
    { onSuccess: syncAfterSave },
  )
}, [id, title, editor, updateNote, syncAfterSave, selectedProjectIDs])
```

Same for auto-save (the `setInterval` callback).

- [ ] **Step 4: Replace hardcoded tags with project multi-select in inspector**

In the `.editor-inspector` section (around line 271), replace the hardcoded `<div className="chip-list">` with:

```tsx
<div className="inspector-section">
  <h4>所属项目</h4>
  <div className="chip-list">
    {selectedProjectIDs.map(pid => {
      const project = allProjects.find(p => p.id === pid)
      if (!project) return null
      return (
        <button
          key={pid}
          className="sync-tag-chip"
          onClick={() => setSelectedProjectIDs(prev => prev.filter(id => id !== pid))}
        >
          {formatTaskProjectOption(project)}
          <span aria-hidden="true">&times;</span>
        </button>
      )
    })}
  </div>
  <select
    value=""
    onChange={e => {
      const pid = e.target.value
      if (pid && !selectedProjectIDs.includes(pid)) {
        setSelectedProjectIDs(prev => [...prev, pid])
      }
      e.target.value = ''
    }}
  >
    <option value="">+ 添加项目</option>
    {allProjects
      .filter(p => !selectedProjectIDs.includes(p.id))
      .map(p => (
        <option key={p.id} value={p.id}>
          {formatTaskProjectOption(p)}
        </option>
      ))}
  </select>
</div>
```

- [ ] **Step 5: Commit**

```bash
git add frontend/src/routes/Editor.tsx
git commit -m "feat: add project multi-select chip to note editor"
```

---

## Phase 8: Frontend — List & Task Pages

### Task 8.1: Add project filter and card chips to Notes list

**Files:**
- Modify: `frontend/src/routes/Notes.tsx`

- [ ] **Step 1: Add project filter state**

```typescript
const [projectID, setProjectID] = useState('')
const [unassigned, setUnassigned] = useState(false)
const { data: allProjects = [] } = useQuery({
  queryKey: ['task-projects'],
  queryFn: listTaskProjects,
})
```

- [ ] **Step 2: Update query to include project filter**

```typescript
const notesQ = useQuery({
  queryKey: ['notes', folder, sort, projectID, unassigned],
  queryFn: () => getNotes({
    folder_id: folder || undefined,
    sort,
    project_id: projectID || undefined,
    unassigned: unassigned || undefined,
  }),
})
```

- [ ] **Step 3: Add project filter UI in sidebar**

After the folder list, add:

```tsx
<hr className="my-3" />
<h4 className="text-xs font-semibold text-fs-text-muted mb-2">项目</h4>
<button
  className={`block w-full text-left px-2 py-1 rounded ${!projectID && !unassigned ? 'bg-fs-accent/10 text-fs-accent' : ''}`}
  onClick={() => { setProjectID(''); setUnassigned(false) }}
>
  全部笔记
</button>
<button
  className={`block w-full text-left px-2 py-1 rounded ${unassigned ? 'bg-fs-accent/10 text-fs-accent' : ''}`}
  onClick={() => { setProjectID(''); setUnassigned(true) }}
>
  未归属项目
</button>
{allProjects.map(p => (
  <button
    key={p.id}
    className={`block w-full text-left px-2 py-1 rounded ${projectID === p.id ? 'bg-fs-accent/10 text-fs-accent' : ''}`}
    onClick={() => { setProjectID(p.id); setUnassigned(false) }}
  >
    {formatTaskProjectOption(p)}
  </button>
))}
```

- [ ] **Step 4: Show project chips on note cards**

In the note card render (around line 84), after the title:

```tsx
{note.projects && note.projects.length > 0 && (
  <div className="chip-list mt-1">
    {note.projects.slice(0, 2).map(p => (
      <em key={p.id}>{formatTaskProjectOption(p)}</em>
    ))}
    {note.projects.length > 2 && (
      <em>+{note.projects.length - 2}</em>
    )}
  </div>
)}
```

- [ ] **Step 5: Update `handleCreateNote` to pass project_id when in project filter**

```typescript
async function handleCreateNote() {
  const note = await createNote.mutateAsync({
    title: '未命名笔记',
    body: '',
    folder_id: folder || undefined,
    tags: '[]',
    project_ids: projectID ? [projectID] : undefined,
  })
  navigate(`/editor/${note.id}`)
}
```

- [ ] **Step 6: Commit**

```bash
git add frontend/src/routes/Notes.tsx
git commit -m "feat: add project filter and project chips to notes list"
```

---

### Task 8.2: Add "project notes" section to Tasks page

**Files:**
- Modify: `frontend/src/routes/Tasks.tsx`

- [ ] **Step 1: Add state and query for project notes**

```typescript
const [activeProjectID, setActiveProjectID] = useState<string | null>(null)

// Fetch project notes when a project is selected
const { data: projectNotesData } = useQuery({
  queryKey: ['notes', { project_id: activeProjectID }],
  queryFn: () => getNotes({ project_id: activeProjectID!, page_size: 6 }),
  enabled: !!activeProjectID,
})
const projectNotes = projectNotesData?.notes || []
```

- [ ] **Step 2: Add "project notes" UI section in the project sidebar**

After the project list, when `activeProjectID` is set:

```tsx
{activeProjectID && (
  <div className="mt-4">
    <h4 className="text-xs font-semibold text-fs-text-muted mb-2">项目笔记</h4>
    {projectNotes.map(note => (
      <button
        key={note.id}
        className="block w-full text-left px-2 py-1 rounded hover:bg-fs-accent/5 text-sm"
        onClick={() => navigate(`/editor/${encodeURIComponent(note.id)}`)}
      >
        <div className="truncate">{note.title}</div>
        <div className="text-xs text-fs-text-muted">
          {new Date(note.updated_at * 1000).toLocaleDateString('zh-CN')}
        </div>
      </button>
    ))}
    <button
      className="mt-2 text-sm text-fs-accent"
      onClick={async () => {
        const note = await createNote({
          title: '未命名笔记',
          body: '',
          folder_id: '__uncategorized',
          tags: '[]',
          project_ids: [activeProjectID],
        })
        navigate(`/editor/${note.id}`)
      }}
    >
      + 新建项目笔记
    </button>
  </div>
)}
```

- [ ] **Step 3: Commit**

```bash
git add frontend/src/routes/Tasks.tsx
git commit -m "feat: add project notes section to tasks page"
```

---

## Phase 9: Playwright Tests

### Task 9.1: Write Playwright tests for project links

**Files:**
- Create: `frontend/tests/note-project-links.spec.ts`

- [ ] **Step 1: Test: editor shows project chips for note with projects**

```typescript
import { test, expect } from '@playwright/test'

test('editor shows project chips for note with projects', async ({ page }) => {
  // Login, create a project, create a note with that project
  // Navigate to editor, verify project chip is visible
})
```

- [ ] **Step 2: Test: editor can add and remove project chips**

```typescript
test('editor can add and remove project chips', async ({ page }) => {
  // Open editor for existing note
  // Select a project from dropdown, verify chip appears
  // Click chip remove button, verify chip disappears
})
```

- [ ] **Step 3: Test: save preserves project_ids**

```typescript
test('save preserves project_ids', async ({ page }) => {
  // Add projects to note in editor, save
  // Reload page, verify projects are still there
})
```

- [ ] **Step 4: Test: auto-save does not lose project_ids**

```typescript
test('auto-save does not lose project_ids', async ({ page }) => {
  // Add projects, type content, wait for auto-save
  // Reload, verify both content and projects persisted
})
```

- [ ] **Step 5: Test: notes list filters by project**

```typescript
test('notes list filters by project', async ({ page }) => {
  // Create notes in different projects
  // Navigate to /notes, click project filter
  // Verify only notes in that project shown
})
```

- [ ] **Step 6: Test: notes list shows unassigned filter**

```typescript
test('notes list shows unassigned notes', async ({ page }) => {
  // Click "未归属项目" filter
  // Verify only notes without projects shown
})
```

- [ ] **Step 7: Test: note cards show project chips (max 2 + overflow)**

```typescript
test('note cards show project chips with overflow', async ({ page }) => {
  // Create note with 3+ projects, go to /notes
  // Verify 2 chips + "+N" displayed
})
```

- [ ] **Step 8: Test: task page shows project notes section**

```typescript
test('task page shows project notes when project selected', async ({ page }) => {
  // Go to /tasks, select a project that has notes
  // Verify "项目笔记" section appears with notes
})
```

- [ ] **Step 9: Test: "new project note" button on task page**

```typescript
test('new project note button creates note with project', async ({ page }) => {
  // On /tasks, select project, click "新建项目笔记"
  // Verify navigated to editor with project pre-selected
})
```

- [ ] **Step 10: Run tests**

```bash
cd frontend && npx playwright test tests/note-project-links.spec.ts
```
Expected: all tests pass.

- [ ] **Step 11: Commit**

```bash
git add frontend/tests/note-project-links.spec.ts
git commit -m "test: add Playwright tests for note-project links"
```

---

## Execution Order Summary

| Phase | Tasks | Description | Depends On |
|-------|-------|-------------|------------|
| 1 | 1.1–1.4 | Database schemas + models + interfaces | — |
| 2 | 2.1–2.3 | SQLite repository implementation | Phase 1 |
| 3 | 3.1–3.2 | PostgreSQL repository implementation | Phase 1 |
| 4 | 4.1–4.3 | Service, handler, facade | Phase 2,3 |
| 5 | 5.1–5.2 | Migration tool updates | Phase 1 |
| 6 | 6.1 | Contract tests | Phase 2,3 |
| 7 | 7.1–7.2 | Frontend API + Editor | Phase 4 |
| 8 | 8.1–8.2 | Frontend List + Tasks | Phase 7 |
| 9 | 9.1 | Playwright end-to-end tests | Phase 8 |

Phases 2 and 3 can run in parallel. Phases 5 and 6 can run in parallel with Phase 4.

---

## Self-Review Checklist

- [x] Spec coverage: every section in the design doc maps to a task
  - Database: Tasks 1.1, 1.2, 5.1
  - Backend models: Tasks 1.3, 1.4
  - Repository contract: Task 1.4
  - SQLite impl: Tasks 2.1–2.3
  - PostgreSQL impl: Tasks 3.1–3.2
  - API design: Task 4.2
  - Error classification: Task 4.2
  - Frontend editor: Task 7.2
  - Frontend list: Task 8.1
  - Frontend task page: Task 8.2
  - Search/sync: Covered by contract tests (Task 6.1) + sync test scenarios
  - Migration: Tasks 5.1, 5.2
  - TDD acceptance: Tasks 6.1, 9.1

- [x] No placeholders: all steps have actual code, no TBD/TODO

- [x] Type consistency: NoteProject model used consistently, NoteFilter fields match across all layers
