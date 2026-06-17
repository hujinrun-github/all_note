# Obsidian Bidirectional Sync Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add manual bidirectional sync between FlowSpace notes and the configured Obsidian base folder, with Obsidian winning conflicts and Obsidian deletions requiring confirmation before FlowSpace notes are deleted.

**Architecture:** Extend the existing one-way Obsidian sync rather than replacing it. The backend keeps SQLite as the source for sync mappings, scans only `vault_path/base_folder`, parses Markdown frontmatter, compares FlowSpace and Obsidian hashes, then performs push, pull, import, or delete-detection actions. The frontend adds a single bidirectional sync action, sync result summaries, and a deletion confirmation list on top of the existing sync panel and editor sync card.

**Tech Stack:** Go 1.26.x, Gin, SQLite, modernc.org/sqlite, React 19, TanStack Query, Vite, TypeScript.

---

## File Structure

Backend files:

- Modify `backend/db/schema.sql`: add new nullable columns to `note_sync_state` for external hash, external mtime, and last direction on new databases.
- Modify `backend/internal/repository/db.go`: run lightweight schema migrations after the existing schema is applied.
- Modify `backend/internal/model/sync.go`: extend `SyncState`; add bidirectional result and deletion item types.
- Modify `backend/internal/model/note.go`: add an internal note creation shape that can preserve a known ID when importing from Obsidian frontmatter.
- Modify `backend/internal/repository/notes.go`: add `CreateNoteWithID` and `ListAllNotes`.
- Modify `backend/internal/repository/sync.go`: persist new sync-state fields; add list/delete helpers for sync state and external-deleted notes.
- Modify `backend/internal/repository/sync_test.go`: cover migrations, extended sync state, and deletion list queries.
- Create `backend/internal/service/obsidian_markdown.go`: parse frontmatter, normalize imported Markdown, compute hashes, and render Markdown using the existing export format.
- Create `backend/internal/service/obsidian_paths.go`: scan configured base folder and return safe Markdown file descriptors.
- Create `backend/internal/service/obsidian_bidirectional.go`: implement the bidirectional decision engine, deletion confirmation, and restore actions.
- Modify `backend/internal/service/obsidian_sync.go`: reuse shared render/path helpers and record external hash/mtime on one-way push.
- Modify `backend/internal/service/obsidian_sync_test.go`: add bidirectional import, pull, conflict, deletion, restore, and path-safety tests.
- Modify `backend/internal/handler/sync.go`: add bidirectional sync and deletion handlers.
- Modify `backend/internal/router/router.go`: register the new routes.

Frontend files:

- Modify `frontend/src/api/sync.ts`: add bidirectional result types and API calls.
- Modify `frontend/src/hooks/useSync.ts`: add mutations and deletion query hooks.
- Modify `frontend/src/components/sync/ObsidianSyncPanel.tsx`: add bidirectional sync button, result summary, and deletion confirmation list.
- Modify `frontend/src/components/sync/NoteSyncCard.tsx`: show `external_deleted` and expose restore action for the current note.
- Modify `frontend/src/styles/index.css`: add compact summary and deletion-list styles.

Docs and verification:

- Modify `README.md`: document the bidirectional rules and safety boundaries.
- Run backend tests, frontend build, and manual API verification against the test service and a temporary Vault.

---

## Task 1: Schema Migration, Models, And Repository Contracts

**Files:**

- Modify: `backend/db/schema.sql`
- Modify: `backend/internal/repository/db.go`
- Modify: `backend/internal/model/sync.go`
- Modify: `backend/internal/model/note.go`
- Modify: `backend/internal/repository/notes.go`
- Modify: `backend/internal/repository/sync.go`
- Test: `backend/internal/repository/sync_test.go`

- [ ] **Step 1: Add failing repository tests for extended sync state**

Add these tests to `backend/internal/repository/sync_test.go`:

```go
func TestInitDBAddsBidirectionalSyncColumns(t *testing.T) {
	openSyncTestDB(t)

	rows, err := DB.Query(`PRAGMA table_info(note_sync_state)`)
	if err != nil {
		t.Fatalf("table info: %v", err)
	}
	defer rows.Close()

	columns := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			t.Fatalf("scan column: %v", err)
		}
		columns[name] = true
	}

	for _, name := range []string{"external_hash", "external_mtime", "last_direction"} {
		if !columns[name] {
			t.Fatalf("expected note_sync_state.%s to exist", name)
		}
	}
}

func TestSyncStateRoundTripIncludesExternalMetadata(t *testing.T) {
	openSyncTestDB(t)
	target := insertSyncTargetForTest(t)
	note := insertNoteForTest(t, "Round Trip", "Body")
	now := nowUnix()
	state := &model.SyncState{
		NoteID:        note.ID,
		TargetID:      target.ID,
		ExternalPath:  "D:\\Vault\\FlowSpace Notes\\Round Trip.md",
		ContentHash:   "flow-hash",
		ExternalHash:  "obsidian-hash",
		ExternalMTime: &now,
		LastSyncedAt:  &now,
		LastDirection: "pull",
		Status:        "synced",
	}

	if err := UpsertSyncState(state); err != nil {
		t.Fatalf("upsert state: %v", err)
	}
	got, err := GetSyncState(note.ID, target.ID)
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if got.ExternalHash != "obsidian-hash" || got.ExternalMTime == nil || got.LastDirection != "pull" {
		t.Fatalf("metadata was not persisted: %+v", got)
	}
}

func TestListExternalDeletedSyncStates(t *testing.T) {
	openSyncTestDB(t)
	target := insertSyncTargetForTest(t)
	note := insertNoteForTest(t, "Deleted In Obsidian", "Body")
	now := nowUnix()
	if err := UpsertSyncState(&model.SyncState{
		NoteID:        note.ID,
		TargetID:      target.ID,
		ExternalPath:  "D:\\Vault\\FlowSpace Notes\\Deleted.md",
		ContentHash:   "flow-hash",
		ExternalHash:  "obsidian-hash",
		ExternalMTime: &now,
		LastSyncedAt:  &now,
		LastDirection: "delete_detected",
		Status:        "external_deleted",
	}); err != nil {
		t.Fatalf("upsert state: %v", err)
	}

	items, err := ListExternalDeletedSyncStates(target.ID)
	if err != nil {
		t.Fatalf("list external deleted: %v", err)
	}
	if len(items) != 1 || items[0].NoteID != note.ID || items[0].Title != "Deleted In Obsidian" {
		t.Fatalf("unexpected items: %+v", items)
	}
}
```

Also add these helpers in the same test file:

```go
func insertSyncTargetForTest(t *testing.T) model.SyncTarget {
	t.Helper()
	target := &model.SyncTarget{
		Type:       "obsidian",
		Name:       "Test Vault",
		VaultPath:  "D:\\Vault",
		BaseFolder: "FlowSpace Notes",
		Enabled:    true,
		AutoSync:   false,
	}
	if err := SaveSyncTarget(target); err != nil {
		t.Fatalf("save target: %v", err)
	}
	return *target
}

func insertNoteForTest(t *testing.T, title string, body string) model.Note {
	t.Helper()
	note := &model.Note{
		Title:    title,
		Body:     body,
		FolderID: "__uncategorized",
		Tags:     "[]",
	}
	if err := CreateNote(note); err != nil {
		t.Fatalf("create note: %v", err)
	}
	return *note
}
```

- [ ] **Step 2: Run repository tests and confirm they fail**

Run:

```powershell
cd backend
go test ./internal/repository -run "TestInitDBAddsBidirectionalSyncColumns|TestSyncStateRoundTripIncludesExternalMetadata|TestListExternalDeletedSyncStates" -v
```

Expected: FAIL because the model fields, schema columns, and repository helper functions do not exist yet.

- [ ] **Step 3: Extend schema and run migrations**

Modify `backend/db/schema.sql` so new databases create the expanded table:

```sql
CREATE TABLE IF NOT EXISTS note_sync_state (
  note_id TEXT NOT NULL,
  target_id TEXT NOT NULL,
  external_path TEXT NOT NULL,
  content_hash TEXT NOT NULL,
  external_hash TEXT,
  external_mtime INTEGER,
  last_direction TEXT,
  last_synced_at INTEGER,
  status TEXT NOT NULL,
  error_message TEXT,
  PRIMARY KEY (note_id, target_id),
  FOREIGN KEY (note_id) REFERENCES notes(id) ON DELETE CASCADE,
  FOREIGN KEY (target_id) REFERENCES sync_targets(id) ON DELETE CASCADE
);
```

Modify `backend/internal/repository/db.go` to run additive migrations after applying `schema.sql`:

```go
func InitDB(dbPath string) error {
	var err error
	DB, err = sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=ON")
	if err != nil {
		return err
	}
	DB.SetMaxOpenConns(1)

	schema, err := os.ReadFile("db/schema.sql")
	if err != nil {
		return err
	}
	if _, err = DB.Exec(string(schema)); err != nil {
		return err
	}
	return migrateDB()
}

func migrateDB() error {
	statements := []string{
		`ALTER TABLE note_sync_state ADD COLUMN external_hash TEXT`,
		`ALTER TABLE note_sync_state ADD COLUMN external_mtime INTEGER`,
		`ALTER TABLE note_sync_state ADD COLUMN last_direction TEXT`,
	}
	for _, stmt := range statements {
		if _, err := DB.Exec(stmt); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
			return err
		}
	}
	return nil
}
```

Add `strings` to the imports in `db.go`.

- [ ] **Step 4: Extend sync models**

Modify `backend/internal/model/sync.go`:

```go
type SyncState struct {
	NoteID        string  `json:"note_id"`
	TargetID      string  `json:"target_id"`
	ExternalPath  string  `json:"external_path"`
	ContentHash   string  `json:"content_hash"`
	ExternalHash  string  `json:"external_hash"`
	ExternalMTime *int64  `json:"external_mtime"`
	LastDirection string  `json:"last_direction"`
	LastSyncedAt  *int64  `json:"last_synced_at"`
	Status        string  `json:"status"`
	ErrorMessage  *string `json:"error_message"`
}

type ObsidianBidirectionalResult struct {
	Pushed          int              `json:"pushed"`
	Pulled          int              `json:"pulled"`
	Imported        int              `json:"imported"`
	ExternalDeleted int              `json:"external_deleted"`
	Failed          int              `json:"failed"`
	Items           []SyncResultItem `json:"items"`
}

type ExternalDeletedNote struct {
	NoteID       string `json:"note_id"`
	Title        string `json:"title"`
	ExternalPath string `json:"external_path"`
	LastSyncedAt *int64 `json:"last_synced_at"`
}
```

- [ ] **Step 5: Add note repository helpers**

Modify `backend/internal/model/note.go`:

```go
type CreateNoteWithIDRequest struct {
	ID       string
	Title    string
	Body     string
	FolderID string
	Tags     string
}
```

Modify `backend/internal/repository/notes.go`:

```go
func CreateNoteWithID(req *model.CreateNoteWithIDRequest) (*model.Note, error) {
	id := strings.TrimSpace(req.ID)
	if id == "" {
		id = newUUID()
	}
	now := nowUnix()
	folderID := req.FolderID
	if folderID == "" {
		folderID = "__uncategorized"
	}
	tags := req.Tags
	if tags == "" {
		tags = "[]"
	}
	_, err := DB.Exec(`
		INSERT INTO notes (id, title, body, folder_id, tags, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, id, req.Title, req.Body, folderID, tags, now, now)
	if err != nil {
		return nil, err
	}
	return GetNoteByID(id)
}

func ListAllNotes() ([]model.Note, error) {
	notes, _, err := GetNotes("", "recent", 1, 100000)
	return notes, err
}
```

- [ ] **Step 6: Update sync repository read/write functions**

Modify `backend/internal/repository/sync.go` so `UpsertSyncState` writes the new fields:

```go
func UpsertSyncState(state *model.SyncState) error {
	_, err := DB.Exec(`
		INSERT INTO note_sync_state (
			note_id, target_id, external_path, content_hash, external_hash, external_mtime,
			last_direction, last_synced_at, status, error_message
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(note_id, target_id) DO UPDATE SET
			external_path = excluded.external_path,
			content_hash = excluded.content_hash,
			external_hash = excluded.external_hash,
			external_mtime = excluded.external_mtime,
			last_direction = excluded.last_direction,
			last_synced_at = excluded.last_synced_at,
			status = excluded.status,
			error_message = excluded.error_message
	`, state.NoteID, state.TargetID, state.ExternalPath, state.ContentHash, state.ExternalHash, state.ExternalMTime, state.LastDirection, state.LastSyncedAt, state.Status, state.ErrorMessage)
	return err
}
```

Modify `GetSyncState` to select and scan the new fields in the same order.

Add repository helpers:

```go
func ListSyncStatesByTarget(targetID string) ([]model.SyncState, error) {
	rows, err := DB.Query(`
		SELECT note_id, target_id, external_path, content_hash, external_hash, external_mtime,
		       last_direction, last_synced_at, status, error_message
		FROM note_sync_state
		WHERE target_id = ?
	`, targetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	states := make([]model.SyncState, 0)
	for rows.Next() {
		var state model.SyncState
		if err := rows.Scan(&state.NoteID, &state.TargetID, &state.ExternalPath, &state.ContentHash, &state.ExternalHash, &state.ExternalMTime, &state.LastDirection, &state.LastSyncedAt, &state.Status, &state.ErrorMessage); err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return states, rows.Err()
}

func DeleteSyncState(noteID, targetID string) error {
	_, err := DB.Exec(`DELETE FROM note_sync_state WHERE note_id = ? AND target_id = ?`, noteID, targetID)
	return err
}

func ListExternalDeletedSyncStates(targetID string) ([]model.ExternalDeletedNote, error) {
	rows, err := DB.Query(`
		SELECT n.id, n.title, s.external_path, s.last_synced_at
		FROM note_sync_state s
		JOIN notes n ON n.id = s.note_id
		WHERE s.target_id = ? AND s.status = 'external_deleted'
		ORDER BY s.last_synced_at DESC
	`, targetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]model.ExternalDeletedNote, 0)
	for rows.Next() {
		var item model.ExternalDeletedNote
		if err := rows.Scan(&item.NoteID, &item.Title, &item.ExternalPath, &item.LastSyncedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
```

- [ ] **Step 7: Run repository tests**

Run:

```powershell
cd backend
go test ./internal/repository -run "TestInitDBAddsBidirectionalSyncColumns|TestSyncStateRoundTripIncludesExternalMetadata|TestListExternalDeletedSyncStates" -v
```

Expected: PASS.

- [ ] **Step 8: Commit**

Run:

```powershell
git add backend/db/schema.sql backend/internal/repository/db.go backend/internal/model/sync.go backend/internal/model/note.go backend/internal/repository/notes.go backend/internal/repository/sync.go backend/internal/repository/sync_test.go
git commit -m "feat: extend sync state for obsidian pull"
```

---

## Task 2: Markdown Parsing And Safe Vault Scanning

**Files:**

- Create: `backend/internal/service/obsidian_markdown.go`
- Create: `backend/internal/service/obsidian_paths.go`
- Modify: `backend/internal/service/obsidian_sync.go`
- Test: `backend/internal/service/obsidian_sync_test.go`
- Modify: `backend/go.mod`
- Modify: `backend/go.sum`

- [ ] **Step 1: Add failing tests for Markdown import parsing**

Add these tests to `backend/internal/service/obsidian_sync_test.go`:

```go
func TestParseObsidianMarkdownWithFlowSpaceFrontmatter(t *testing.T) {
	raw := "---\nid: n01\nsource: flowspace\nfolder: \"__work\"\ntags:\n  - product\n---\n\n# Product Plan\n\nBody text\n"
	parsed, err := parseObsidianMarkdown([]byte(raw), "Product Plan.md")
	if err != nil {
		t.Fatalf("parse markdown: %v", err)
	}
	if parsed.ID != "n01" || parsed.Title != "Product Plan" || parsed.FolderID != "__work" {
		t.Fatalf("unexpected parsed metadata: %+v", parsed)
	}
	if parsed.Body != "Body text\n" || parsed.TagsJSON != `["product"]` {
		t.Fatalf("unexpected body or tags: body=%q tags=%s", parsed.Body, parsed.TagsJSON)
	}
}

func TestParseObsidianMarkdownWithoutFrontmatterUsesFileName(t *testing.T) {
	raw := "Loose note body\nSecond line\n"
	parsed, err := parseObsidianMarkdown([]byte(raw), "Loose Note.md")
	if err != nil {
		t.Fatalf("parse markdown: %v", err)
	}
	if parsed.ID != "" || parsed.Title != "Loose Note" || parsed.Body != raw {
		t.Fatalf("unexpected parsed note: %+v", parsed)
	}
	if parsed.FolderID != "__uncategorized" || parsed.TagsJSON != "[]" {
		t.Fatalf("unexpected defaults: %+v", parsed)
	}
}

func TestParseObsidianMarkdownRemovesDuplicateHeading(t *testing.T) {
	raw := "# Meeting\n\nNotes\n"
	parsed, err := parseObsidianMarkdown([]byte(raw), "Meeting.md")
	if err != nil {
		t.Fatalf("parse markdown: %v", err)
	}
	if parsed.Title != "Meeting" || parsed.Body != "Notes\n" {
		t.Fatalf("unexpected parsed markdown: %+v", parsed)
	}
}
```

- [ ] **Step 2: Add failing tests for base-folder scanning**

Add this test to `backend/internal/service/obsidian_sync_test.go`:

```go
func TestScanObsidianMarkdownFilesOnlyReturnsBaseFolderMarkdown(t *testing.T) {
	vault := t.TempDir()
	base := filepath.Join(vault, "FlowSpace Notes")
	if err := os.MkdirAll(filepath.Join(base, "Nested"), 0755); err != nil {
		t.Fatalf("create nested: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(base, ".obsidian"), 0755); err != nil {
		t.Fatalf("create hidden: %v", err)
	}
	if err := os.WriteFile(filepath.Join(base, "One.md"), []byte("# One\n"), 0644); err != nil {
		t.Fatalf("write one: %v", err)
	}
	if err := os.WriteFile(filepath.Join(base, "Nested", "Two.md"), []byte("# Two\n"), 0644); err != nil {
		t.Fatalf("write two: %v", err)
	}
	if err := os.WriteFile(filepath.Join(base, "ignore.txt"), []byte("ignore"), 0644); err != nil {
		t.Fatalf("write ignore: %v", err)
	}
	if err := os.WriteFile(filepath.Join(base, ".obsidian", "Plugin.md"), []byte("# Plugin\n"), 0644); err != nil {
		t.Fatalf("write plugin: %v", err)
	}

	files, err := scanObsidianMarkdownFiles(&model.SyncTarget{VaultPath: vault, BaseFolder: "FlowSpace Notes"})
	if err != nil {
		t.Fatalf("scan files: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 markdown files, got %+v", files)
	}
}
```

- [ ] **Step 3: Run tests and confirm they fail**

Run:

```powershell
cd backend
go test ./internal/service -run "TestParseObsidianMarkdown|TestScanObsidianMarkdownFilesOnlyReturnsBaseFolderMarkdown" -v
```

Expected: FAIL because parsing and scanning helpers do not exist.

- [ ] **Step 4: Implement Markdown parsing helper**

Create `backend/internal/service/obsidian_markdown.go`:

```go
package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/goccy/go-yaml"
)

type obsidianParsedMarkdown struct {
	ID       string
	Title    string
	Body     string
	FolderID string
	TagsJSON string
	Hash     string
}

type obsidianFrontmatter struct {
	ID       string   `yaml:"id"`
	Source   string   `yaml:"source"`
	Folder   string   `yaml:"folder"`
	Tags     []string `yaml:"tags"`
	Created  string   `yaml:"created"`
	Updated  string   `yaml:"updated"`
}

var firstH1Pattern = regexp.MustCompile(`(?m)^#\s+(.+?)\s*$`)

func parseObsidianMarkdown(raw []byte, fileName string) (*obsidianParsedMarkdown, error) {
	body := string(raw)
	meta := obsidianFrontmatter{Folder: "__uncategorized"}
	if bytes.HasPrefix(raw, []byte("---\n")) {
		parts := strings.SplitN(body, "\n---\n", 2)
		if len(parts) == 2 {
			if err := yaml.Unmarshal([]byte(strings.TrimPrefix(parts[0], "---\n")), &meta); err != nil {
				return nil, err
			}
			body = parts[1]
		}
	}

	title := titleFromMarkdownBody(body)
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(fileName), filepath.Ext(fileName))
	}
	body = stripDuplicateHeading(body, title)

	tagsJSON, err := json.Marshal(cleanTags(meta.Tags))
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	folderID := strings.TrimSpace(meta.Folder)
	if folderID == "" {
		folderID = "__uncategorized"
	}
	return &obsidianParsedMarkdown{
		ID:       strings.TrimSpace(meta.ID),
		Title:    strings.TrimSpace(title),
		Body:     ensureTrailingNewline(strings.TrimLeft(body, "\r\n")),
		FolderID: folderID,
		TagsJSON: string(tagsJSON),
		Hash:     hex.EncodeToString(sum[:]),
	}, nil
}

func titleFromMarkdownBody(body string) string {
	match := firstH1Pattern.FindStringSubmatch(body)
	if len(match) < 2 {
		return ""
	}
	return strings.TrimSpace(match[1])
}

func stripDuplicateHeading(body, title string) string {
	lines := strings.Split(body, "\n")
	if len(lines) == 0 {
		return body
	}
	if strings.TrimSpace(lines[0]) != "# "+strings.TrimSpace(title) {
		return body
	}
	rest := strings.Join(lines[1:], "\n")
	return strings.TrimLeft(rest, "\r\n")
}

func ensureTrailingNewline(value string) string {
	if value == "" || strings.HasSuffix(value, "\n") {
		return value
	}
	return value + "\n"
}

func cleanTags(tags []string) []string {
	cleaned := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag != "" {
			cleaned = append(cleaned, tag)
		}
	}
	return cleaned
}
```

- [ ] **Step 5: Implement safe scanning helper**

Create `backend/internal/service/obsidian_paths.go`:

```go
package service

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/hujinrun/flowspace/internal/model"
)

type obsidianMarkdownFile struct {
	Path  string
	Raw   []byte
	Hash  string
	MTime int64
	Note  *obsidianParsedMarkdown
}

func scanObsidianMarkdownFiles(target *model.SyncTarget) ([]obsidianMarkdownFile, error) {
	baseDir, err := targetBaseDir(target)
	if err != nil {
		return nil, err
	}
	if err := verifyRealBaseDir(target); err != nil {
		return nil, err
	}

	files := make([]obsidianMarkdownFile, 0)
	err = filepath.WalkDir(baseDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		name := entry.Name()
		if entry.IsDir() {
			if name == ".obsidian" || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.ToLower(filepath.Ext(name)) != ".md" {
			return nil
		}
		absPath, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		if !isPathWithin(absPath, baseDir) {
			return nil
		}
		raw, err := os.ReadFile(absPath)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		parsed, err := parseObsidianMarkdown(raw, name)
		if err != nil {
			return err
		}
		files = append(files, obsidianMarkdownFile{
			Path:  absPath,
			Raw:   raw,
			Hash:  parsed.Hash,
			MTime: info.ModTime().Unix(),
			Note:  parsed,
		})
		return nil
	})
	return files, err
}
```

- [ ] **Step 6: Reuse shared Markdown helper in existing push path**

Modify `backend/internal/service/obsidian_sync.go` so `renderObsidianMarkdown`, `parseTags`, and path helpers continue to be the single export format. If `renderObsidianMarkdown` remains in this file, do not duplicate it in `obsidian_markdown.go`.

Update `writeNoteToTarget` after `os.WriteFile`:

```go
info, statErr := os.Stat(outputPath)
if statErr != nil {
	return nil, statErr
}
mtime := info.ModTime().Unix()
now := time.Now().Unix()
if err := repository.UpsertSyncState(&model.SyncState{
	NoteID:        note.ID,
	TargetID:      target.ID,
	ExternalPath:  outputPath,
	ContentHash:   contentHash,
	ExternalHash:  contentHash,
	ExternalMTime: &mtime,
	LastDirection: "push",
	LastSyncedAt:  &now,
	Status:        "synced",
	ErrorMessage:  nil,
}); err != nil {
	return nil, fmt.Errorf("record sync state: %w", err)
}
```

- [ ] **Step 7: Run parsing and scanning tests**

Run:

```powershell
cd backend
go test ./internal/service -run "TestParseObsidianMarkdown|TestScanObsidianMarkdownFilesOnlyReturnsBaseFolderMarkdown" -v
go mod tidy
```

Expected: tests PASS; `go.mod` lists `github.com/goccy/go-yaml` as a direct dependency if needed.

- [ ] **Step 8: Commit**

Run:

```powershell
git add backend/internal/service/obsidian_markdown.go backend/internal/service/obsidian_paths.go backend/internal/service/obsidian_sync.go backend/internal/service/obsidian_sync_test.go backend/go.mod backend/go.sum
git commit -m "feat: parse obsidian markdown for import"
```

---

## Task 3: Bidirectional Sync Engine

**Files:**

- Create: `backend/internal/service/obsidian_bidirectional.go`
- Modify: `backend/internal/service/obsidian_sync_test.go`
- Modify: `backend/internal/repository/notes.go`
- Modify: `backend/internal/repository/sync.go`

- [ ] **Step 1: Add failing test for Obsidian-only import**

Add to `backend/internal/service/obsidian_sync_test.go`:

```go
func TestSyncObsidianBidirectionalImportsNewMarkdownFile(t *testing.T) {
	openObsidianSyncTestDB(t)
	vault := t.TempDir()
	target := saveObsidianTargetForTest(t, vault)
	base := filepath.Join(vault, target.BaseFolder)
	if err := os.MkdirAll(base, 0755); err != nil {
		t.Fatalf("create base: %v", err)
	}
	if err := os.WriteFile(filepath.Join(base, "Imported.md"), []byte("# Imported\n\nFrom Obsidian\n"), 0644); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	result := SyncObsidianBidirectional()
	if result.Imported != 1 || result.Failed != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
	notes, err := repository.ListAllNotes()
	if err != nil {
		t.Fatalf("list notes: %v", err)
	}
	if len(notes) != 1 || notes[0].Title != "Imported" || notes[0].Body != "From Obsidian\n" {
		t.Fatalf("unexpected imported notes: %+v", notes)
	}
}
```

- [ ] **Step 2: Add failing test for Obsidian pull winning a conflict**

Add:

```go
func TestSyncObsidianBidirectionalPullsWhenBothSidesChanged(t *testing.T) {
	openObsidianSyncTestDB(t)
	vault := t.TempDir()
	target := saveObsidianTargetForTest(t, vault)
	note := createNoteForSyncTest(t, "Conflict", "FlowSpace original\n")
	item, err := writeNoteToTarget(&note, &target)
	if err != nil {
		t.Fatalf("initial push: %v", err)
	}

	if _, err := repository.UpdateNote(note.ID, &model.UpdateNoteRequest{Body: ptrString("FlowSpace changed\n")}); err != nil {
		t.Fatalf("update local note: %v", err)
	}
	obsidianBody := "---\nid: " + note.ID + "\nsource: flowspace\nfolder: \"__uncategorized\"\ntags: []\n---\n\n# Conflict\n\nObsidian changed\n"
	if err := os.WriteFile(item.ExternalPath, []byte(obsidianBody), 0644); err != nil {
		t.Fatalf("write obsidian change: %v", err)
	}

	result := SyncObsidianBidirectional()
	if result.Pulled != 1 || result.Failed != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
	got, err := repository.GetNoteByID(note.ID)
	if err != nil {
		t.Fatalf("get note: %v", err)
	}
	if got.Body != "Obsidian changed\n" {
		t.Fatalf("expected Obsidian to win conflict, got %q", got.Body)
	}
}
```

- [ ] **Step 3: Add failing tests for FlowSpace push and external deletion detection**

Add:

```go
func TestSyncObsidianBidirectionalPushesFlowSpaceOnlyChange(t *testing.T) {
	openObsidianSyncTestDB(t)
	vault := t.TempDir()
	target := saveObsidianTargetForTest(t, vault)
	note := createNoteForSyncTest(t, "Push Me", "Initial\n")
	item, err := writeNoteToTarget(&note, &target)
	if err != nil {
		t.Fatalf("initial push: %v", err)
	}
	if _, err := repository.UpdateNote(note.ID, &model.UpdateNoteRequest{Body: ptrString("Changed in FlowSpace\n")}); err != nil {
		t.Fatalf("update note: %v", err)
	}

	result := SyncObsidianBidirectional()
	if result.Pushed != 1 || result.Failed != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
	raw, err := os.ReadFile(item.ExternalPath)
	if err != nil {
		t.Fatalf("read external: %v", err)
	}
	if !strings.Contains(string(raw), "Changed in FlowSpace") {
		t.Fatalf("expected pushed content, got %s", string(raw))
	}
}

func TestSyncObsidianBidirectionalMarksExternalDeletion(t *testing.T) {
	openObsidianSyncTestDB(t)
	vault := t.TempDir()
	target := saveObsidianTargetForTest(t, vault)
	note := createNoteForSyncTest(t, "Deleted Outside", "Body\n")
	item, err := writeNoteToTarget(&note, &target)
	if err != nil {
		t.Fatalf("initial push: %v", err)
	}
	if err := os.Remove(item.ExternalPath); err != nil {
		t.Fatalf("remove external: %v", err)
	}

	result := SyncObsidianBidirectional()
	if result.ExternalDeleted != 1 || result.Failed != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
	state, err := repository.GetSyncState(note.ID, target.ID)
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if state.Status != "external_deleted" || state.LastDirection != "delete_detected" {
		t.Fatalf("unexpected state: %+v", state)
	}
	if _, err := repository.GetNoteByID(note.ID); err != nil {
		t.Fatalf("FlowSpace note should still exist before confirmation: %v", err)
	}
}
```

- [ ] **Step 4: Run tests and confirm they fail**

Run:

```powershell
cd backend
go test ./internal/service -run "TestSyncObsidianBidirectionalImportsNewMarkdownFile|TestSyncObsidianBidirectionalPullsWhenBothSidesChanged|TestSyncObsidianBidirectionalPushesFlowSpaceOnlyChange|TestSyncObsidianBidirectionalMarksExternalDeletion" -v
```

Expected: FAIL because `SyncObsidianBidirectional` is not implemented.

- [ ] **Step 5: Implement bidirectional sync entry point**

Create `backend/internal/service/obsidian_bidirectional.go`:

```go
package service

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/repository"
)

func SyncObsidianBidirectional() model.ObsidianBidirectionalResult {
	result := model.ObsidianBidirectionalResult{Items: []model.SyncResultItem{}}
	target, err := repository.GetDefaultSyncTarget("obsidian")
	if err != nil {
		result.Failed++
		result.Items = append(result.Items, model.SyncResultItem{Status: "failed", ErrorMessage: fmt.Errorf("load obsidian sync target: %w", err).Error()})
		return result
	}
	if err := TestObsidianTarget(target); err != nil {
		result.Failed++
		result.Items = append(result.Items, model.SyncResultItem{Status: "failed", ErrorMessage: err.Error()})
		return result
	}

	files, err := scanObsidianMarkdownFiles(target)
	if err != nil {
		result.Failed++
		result.Items = append(result.Items, model.SyncResultItem{Status: "failed", ErrorMessage: err.Error()})
		return result
	}
	states, err := repository.ListSyncStatesByTarget(target.ID)
	if err != nil {
		result.Failed++
		result.Items = append(result.Items, model.SyncResultItem{Status: "failed", ErrorMessage: err.Error()})
		return result
	}
	notes, err := repository.ListAllNotes()
	if err != nil {
		result.Failed++
		result.Items = append(result.Items, model.SyncResultItem{Status: "failed", ErrorMessage: err.Error()})
		return result
	}

	notesByID := notesByID(notes)
	statesByNoteID := statesByNoteID(states)
	externalPaths := map[string]bool{}
	handledNoteIDs := map[string]bool{}

	for _, file := range files {
		externalPaths[normalizedPath(file.Path)] = true
		item := syncObsidianFile(file, target, notesByID, statesByNoteID)
		result.Items = append(result.Items, item)
		switch item.Status {
		case "imported":
			result.Imported++
		case "pulled":
			result.Pulled++
		case "synced":
		case "failed":
			result.Failed++
		}
		if item.NoteID != "" {
			handledNoteIDs[item.NoteID] = true
		}
	}

	for _, state := range states {
		if state.ExternalPath == "" || state.Status == "external_deleted" {
			continue
		}
		if !externalPaths[normalizedPath(state.ExternalPath)] {
			if _, exists := notesByID[state.NoteID]; exists {
				item := markExternalDeleted(state, target)
				result.Items = append(result.Items, item)
				if item.Status == "external_deleted" {
					result.ExternalDeleted++
				} else {
					result.Failed++
				}
				handledNoteIDs[state.NoteID] = true
			}
		}
	}

	for _, note := range notes {
		if handledNoteIDs[note.ID] {
			continue
		}
		state, hasState := statesByNoteID[note.ID]
		if hasState && state.Status == "external_deleted" {
			continue
		}
		rendered := renderObsidianMarkdown(&note)
		contentHash := markdownHash(rendered)
		if hasState && state.ContentHash == contentHash {
			continue
		}
		item, err := writeNoteToTarget(&note, target)
		if err != nil {
			result.Failed++
			result.Items = append(result.Items, model.SyncResultItem{NoteID: note.ID, Status: "failed", ErrorMessage: err.Error()})
			continue
		}
		item.Status = "pushed"
		result.Pushed++
		result.Items = append(result.Items, *item)
	}

	return result
}
```

- [ ] **Step 6: Implement per-file decisions**

Add these functions to `obsidian_bidirectional.go`:

```go
func syncObsidianFile(file obsidianMarkdownFile, target *model.SyncTarget, notes map[string]model.Note, states map[string]model.SyncState) model.SyncResultItem {
	parsed := file.Note
	if parsed.ID != "" {
		if note, ok := notes[parsed.ID]; ok {
			return pullObsidianIntoNote(note, file, target)
		}
	}

	for _, state := range states {
		if normalizedPath(state.ExternalPath) == normalizedPath(file.Path) {
			if note, ok := notes[state.NoteID]; ok {
				if state.ExternalHash == file.Hash {
					return model.SyncResultItem{NoteID: note.ID, Status: "synced", ExternalPath: file.Path}
				}
				return pullObsidianIntoNote(note, file, target)
			}
		}
	}

	return importObsidianFile(file, target)
}

func importObsidianFile(file obsidianMarkdownFile, target *model.SyncTarget) model.SyncResultItem {
	req := &model.CreateNoteWithIDRequest{
		ID:       validImportedID(file.Note.ID),
		Title:    file.Note.Title,
		Body:     file.Note.Body,
		FolderID: file.Note.FolderID,
		Tags:     file.Note.TagsJSON,
	}
	note, err := repository.CreateNoteWithID(req)
	if err != nil {
		return model.SyncResultItem{Status: "failed", ExternalPath: file.Path, ErrorMessage: err.Error()}
	}
	if err := recordSyncedExternal(note.ID, target.ID, file, "import"); err != nil {
		return model.SyncResultItem{NoteID: note.ID, Status: "failed", ExternalPath: file.Path, ErrorMessage: err.Error()}
	}
	return model.SyncResultItem{NoteID: note.ID, Status: "imported", ExternalPath: file.Path}
}

func pullObsidianIntoNote(note model.Note, file obsidianMarkdownFile, target *model.SyncTarget) model.SyncResultItem {
	updated, err := repository.UpdateNote(note.ID, &model.UpdateNoteRequest{
		Title:    &file.Note.Title,
		Body:     &file.Note.Body,
		FolderID: &file.Note.FolderID,
		Tags:     &file.Note.TagsJSON,
	})
	if err != nil {
		return model.SyncResultItem{NoteID: note.ID, Status: "failed", ExternalPath: file.Path, ErrorMessage: err.Error()}
	}
	if err := recordSyncedExternal(updated.ID, target.ID, file, "pull"); err != nil {
		return model.SyncResultItem{NoteID: note.ID, Status: "failed", ExternalPath: file.Path, ErrorMessage: err.Error()}
	}
	return model.SyncResultItem{NoteID: note.ID, Status: "pulled", ExternalPath: file.Path}
}
```

Add these helpers:

```go
func recordSyncedExternal(noteID, targetID string, file obsidianMarkdownFile, direction string) error {
	now := time.Now().Unix()
	mtime := file.MTime
	return repository.UpsertSyncState(&model.SyncState{
		NoteID:        noteID,
		TargetID:      targetID,
		ExternalPath:  file.Path,
		ContentHash:   file.Hash,
		ExternalHash:  file.Hash,
		ExternalMTime: &mtime,
		LastDirection: direction,
		LastSyncedAt:  &now,
		Status:        "synced",
		ErrorMessage:  nil,
	})
}

func markExternalDeleted(state model.SyncState, target *model.SyncTarget) model.SyncResultItem {
	now := time.Now().Unix()
	err := repository.UpsertSyncState(&model.SyncState{
		NoteID:        state.NoteID,
		TargetID:      target.ID,
		ExternalPath:  state.ExternalPath,
		ContentHash:   state.ContentHash,
		ExternalHash:  state.ExternalHash,
		ExternalMTime: state.ExternalMTime,
		LastDirection: "delete_detected",
		LastSyncedAt:  &now,
		Status:        "external_deleted",
		ErrorMessage:  nil,
	})
	if err != nil {
		return model.SyncResultItem{NoteID: state.NoteID, Status: "failed", ExternalPath: state.ExternalPath, ErrorMessage: err.Error()}
	}
	return model.SyncResultItem{NoteID: state.NoteID, Status: "external_deleted", ExternalPath: state.ExternalPath}
}

func markdownHash(markdown string) string {
	sum := sha256.Sum256([]byte(markdown))
	return hex.EncodeToString(sum[:])
}

func normalizedPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return strings.ToLower(path)
	}
	return strings.ToLower(abs)
}

func notesByID(notes []model.Note) map[string]model.Note {
	result := make(map[string]model.Note, len(notes))
	for _, note := range notes {
		result[note.ID] = note
	}
	return result
}

func statesByNoteID(states []model.SyncState) map[string]model.SyncState {
	result := make(map[string]model.SyncState, len(states))
	for _, state := range states {
		result[state.NoteID] = state
	}
	return result
}

func validImportedID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	if strings.ContainsAny(id, " \t\r\n/\\") {
		return ""
	}
	return id
}
```

Add required imports: `crypto/sha256`, `encoding/hex`.

- [ ] **Step 7: Run bidirectional engine tests**

Run:

```powershell
cd backend
go test ./internal/service -run "TestSyncObsidianBidirectionalImportsNewMarkdownFile|TestSyncObsidianBidirectionalPullsWhenBothSidesChanged|TestSyncObsidianBidirectionalPushesFlowSpaceOnlyChange|TestSyncObsidianBidirectionalMarksExternalDeletion" -v
```

Expected: PASS.

- [ ] **Step 8: Commit**

Run:

```powershell
git add backend/internal/service/obsidian_bidirectional.go backend/internal/service/obsidian_sync_test.go backend/internal/repository/notes.go backend/internal/repository/sync.go
git commit -m "feat: sync obsidian changes back to notes"
```

---

## Task 4: Deletion Confirmation And Restore APIs

**Files:**

- Modify: `backend/internal/service/obsidian_bidirectional.go`
- Modify: `backend/internal/service/obsidian_sync_test.go`
- Modify: `backend/internal/handler/sync.go`
- Modify: `backend/internal/router/router.go`
- Modify: `backend/internal/model/sync.go`

- [ ] **Step 1: Add failing service tests for confirm and restore**

Add:

```go
func TestConfirmObsidianDeletionDeletesFlowSpaceNote(t *testing.T) {
	openObsidianSyncTestDB(t)
	vault := t.TempDir()
	target := saveObsidianTargetForTest(t, vault)
	note := createNoteForSyncTest(t, "Confirm Delete", "Body\n")
	item, err := writeNoteToTarget(&note, &target)
	if err != nil {
		t.Fatalf("initial push: %v", err)
	}
	if err := os.Remove(item.ExternalPath); err != nil {
		t.Fatalf("remove external: %v", err)
	}
	result := SyncObsidianBidirectional()
	if result.ExternalDeleted != 1 {
		t.Fatalf("expected external deletion: %+v", result)
	}

	if err := ConfirmObsidianDeletion(note.ID); err != nil {
		t.Fatalf("confirm deletion: %v", err)
	}
	if _, err := repository.GetNoteByID(note.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected note to be deleted, got err=%v", err)
	}
	if _, err := repository.GetSyncState(note.ID, target.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected sync state to be deleted, got err=%v", err)
	}
}

func TestRestoreObsidianDeletionReexportsFile(t *testing.T) {
	openObsidianSyncTestDB(t)
	vault := t.TempDir()
	target := saveObsidianTargetForTest(t, vault)
	note := createNoteForSyncTest(t, "Restore Delete", "Body\n")
	item, err := writeNoteToTarget(&note, &target)
	if err != nil {
		t.Fatalf("initial push: %v", err)
	}
	if err := os.Remove(item.ExternalPath); err != nil {
		t.Fatalf("remove external: %v", err)
	}
	result := SyncObsidianBidirectional()
	if result.ExternalDeleted != 1 {
		t.Fatalf("expected external deletion: %+v", result)
	}

	restored, err := RestoreObsidianDeletion(note.ID)
	if err != nil {
		t.Fatalf("restore deletion: %v", err)
	}
	if restored.Status != "synced" {
		t.Fatalf("unexpected restore result: %+v", restored)
	}
	if _, err := os.Stat(restored.ExternalPath); err != nil {
		t.Fatalf("expected external file to exist: %v", err)
	}
}
```

- [ ] **Step 2: Run tests and confirm they fail**

Run:

```powershell
cd backend
go test ./internal/service -run "TestConfirmObsidianDeletionDeletesFlowSpaceNote|TestRestoreObsidianDeletionReexportsFile" -v
```

Expected: FAIL because confirm and restore services do not exist.

- [ ] **Step 3: Implement deletion list, confirm, and restore services**

Add to `backend/internal/service/obsidian_bidirectional.go`:

```go
func ListObsidianDeletionCandidates() ([]model.ExternalDeletedNote, error) {
	target, err := repository.GetDefaultSyncTarget("obsidian")
	if err != nil {
		return nil, err
	}
	return repository.ListExternalDeletedSyncStates(target.ID)
}

func ConfirmObsidianDeletion(noteID string) error {
	target, err := repository.GetDefaultSyncTarget("obsidian")
	if err != nil {
		return err
	}
	state, err := repository.GetSyncState(noteID, target.ID)
	if err != nil {
		return err
	}
	if state.Status != "external_deleted" {
		return errors.New("note is not marked as deleted in obsidian")
	}
	if err := repository.DeleteNote(noteID); err != nil {
		return err
	}
	return repository.DeleteSyncState(noteID, target.ID)
}

func RestoreObsidianDeletion(noteID string) (*model.SyncResultItem, error) {
	target, err := repository.GetDefaultSyncTarget("obsidian")
	if err != nil {
		return nil, err
	}
	state, err := repository.GetSyncState(noteID, target.ID)
	if err != nil {
		return nil, err
	}
	if state.Status != "external_deleted" {
		return nil, errors.New("note is not marked as deleted in obsidian")
	}
	note, err := repository.GetNoteByID(noteID)
	if err != nil {
		return nil, err
	}
	item, err := writeNoteToTarget(note, target)
	if err != nil {
		return nil, err
	}
	item.Status = "synced"
	return item, nil
}
```

- [ ] **Step 4: Add handlers**

Add to `backend/internal/handler/sync.go`:

```go
func SyncObsidianBidirectional(c *gin.Context) {
	result := service.SyncObsidianBidirectional()
	success(c, gin.H{"result": result})
}

func ListObsidianDeletions(c *gin.Context) {
	items, err := service.ListObsidianDeletionCandidates()
	if err != nil {
		internalError(c, err.Error())
		return
	}
	success(c, gin.H{"items": items})
}

func ConfirmObsidianDeletion(c *gin.Context) {
	if err := service.ConfirmObsidianDeletion(c.Param("note_id")); err != nil {
		badRequest(c, err.Error())
		return
	}
	noContent(c)
}

func RestoreObsidianDeletion(c *gin.Context) {
	item, err := service.RestoreObsidianDeletion(c.Param("note_id"))
	if err != nil {
		badRequest(c, err.Error())
		return
	}
	success(c, gin.H{"item": item})
}
```

- [ ] **Step 5: Register routes**

Modify `backend/internal/router/router.go`:

```go
api.POST("/sync/obsidian/bidirectional", handler.SyncObsidianBidirectional)
api.GET("/sync/obsidian/deletions", handler.ListObsidianDeletions)
api.POST("/sync/obsidian/deletions/:note_id/confirm", handler.ConfirmObsidianDeletion)
api.POST("/sync/obsidian/deletions/:note_id/restore", handler.RestoreObsidianDeletion)
```

- [ ] **Step 6: Run backend tests**

Run:

```powershell
cd backend
go test ./...
```

Expected: PASS.

- [ ] **Step 7: Commit**

Run:

```powershell
git add backend/internal/service/obsidian_bidirectional.go backend/internal/service/obsidian_sync_test.go backend/internal/handler/sync.go backend/internal/router/router.go backend/internal/model/sync.go
git commit -m "feat: confirm obsidian deletion sync"
```

---

## Task 5: Frontend API And Hooks

**Files:**

- Modify: `frontend/src/api/sync.ts`
- Modify: `frontend/src/hooks/useSync.ts`

- [ ] **Step 1: Extend frontend sync API types**

Modify `frontend/src/api/sync.ts`:

```ts
export interface ObsidianBidirectionalResult {
  pushed: number
  pulled: number
  imported: number
  external_deleted: number
  failed: number
  items: SyncResultItem[]
}

export interface ExternalDeletedNote {
  note_id: string
  title: string
  external_path: string
  last_synced_at: number | null
}

export type SyncStateStatus = 'synced' | 'pending' | 'failed' | 'external_deleted'
```

Update `SyncState.status`:

```ts
status: SyncStateStatus
```

Add API calls:

```ts
export async function syncObsidianBidirectional(): Promise<ObsidianBidirectionalResult> {
  const res = await api.post<{ result: ObsidianBidirectionalResult }>('/api/sync/obsidian/bidirectional')
  return res.data.result
}

export async function getObsidianDeletions(): Promise<ExternalDeletedNote[]> {
  const res = await api.get<{ items: ExternalDeletedNote[] }>('/api/sync/obsidian/deletions')
  return res.data.items
}

export async function confirmObsidianDeletion(noteID: string): Promise<void> {
  await api.post(`/api/sync/obsidian/deletions/${noteID}/confirm`)
}

export async function restoreObsidianDeletion(noteID: string): Promise<SyncResultItem> {
  const res = await api.post<{ item: SyncResultItem }>(`/api/sync/obsidian/deletions/${noteID}/restore`)
  return res.data.item
}
```

- [ ] **Step 2: Add hooks**

Modify `frontend/src/hooks/useSync.ts`:

```ts
export function useObsidianDeletions() {
  return useQuery({
    queryKey: ['obsidian-deletions'],
    queryFn: syncApi.getObsidianDeletions,
  })
}

export function useSyncObsidianBidirectional() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: syncApi.syncObsidianBidirectional,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['notes'] })
      qc.invalidateQueries({ queryKey: ['note'] })
      qc.invalidateQueries({ queryKey: ['note-sync-state'] })
      qc.invalidateQueries({ queryKey: ['obsidian-deletions'] })
    },
  })
}

export function useConfirmObsidianDeletion() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: syncApi.confirmObsidianDeletion,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['notes'] })
      qc.invalidateQueries({ queryKey: ['obsidian-deletions'] })
      qc.invalidateQueries({ queryKey: ['note-sync-state'] })
    },
  })
}

export function useRestoreObsidianDeletion() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: syncApi.restoreObsidianDeletion,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['obsidian-deletions'] })
      qc.invalidateQueries({ queryKey: ['note-sync-state'] })
    },
  })
}
```

- [ ] **Step 3: Run frontend type check through build**

Run:

```powershell
cd frontend
npm run build
```

Expected: PASS.

- [ ] **Step 4: Commit**

Run:

```powershell
git add frontend/src/api/sync.ts frontend/src/hooks/useSync.ts
git commit -m "feat: add obsidian pull frontend api"
```

---

## Task 6: Sync Panel Bidirectional UI

**Files:**

- Modify: `frontend/src/components/sync/ObsidianSyncPanel.tsx`
- Modify: `frontend/src/styles/index.css`

- [ ] **Step 1: Wire bidirectional hooks into the panel**

Modify imports in `frontend/src/components/sync/ObsidianSyncPanel.tsx`:

```ts
import {
  useConfirmObsidianDeletion,
  useObsidianDeletions,
  useRestoreObsidianDeletion,
  useSaveSyncTarget,
  useSyncObsidianAll,
  useSyncObsidianBidirectional,
  useSyncTargets,
  useTestObsidianTarget,
} from '../../hooks/useSync'
import type { ObsidianBidirectionalResult } from '../../api/sync'
```

Add hook state in the component:

```ts
const syncBoth = useSyncObsidianBidirectional()
const deletionsQ = useObsidianDeletions()
const confirmDeletion = useConfirmObsidianDeletion()
const restoreDeletion = useRestoreObsidianDeletion()
const [lastBidirectionalResult, setLastBidirectionalResult] = useState<ObsidianBidirectionalResult | null>(null)
```

Update busy state:

```ts
const isBusy =
  saveTarget.isPending ||
  testTarget.isPending ||
  syncAll.isPending ||
  syncBoth.isPending ||
  confirmDeletion.isPending ||
  restoreDeletion.isPending
```

- [ ] **Step 2: Add bidirectional sync handler**

Add inside the component:

```ts
async function handleSyncBidirectional() {
  setMessage(null)
  setLastBidirectionalResult(null)
  try {
    const result = await syncBoth.mutateAsync()
    setLastBidirectionalResult(result)
    setMessage({
      tone: 'success',
      text: `双向同步完成：导入 ${result.imported}，从 Obsidian 更新 ${result.pulled}，写入 Obsidian ${result.pushed}，待确认删除 ${result.external_deleted}，失败 ${result.failed}`,
    })
  } catch {
    setMessage({ tone: 'error', text: '双向同步失败，请先保存并测试 Obsidian 路径' })
  }
}
```

Add deletion actions:

```ts
async function handleConfirmDeletion(noteID: string) {
  await confirmDeletion.mutateAsync(noteID)
}

async function handleRestoreDeletion(noteID: string) {
  await restoreDeletion.mutateAsync(noteID)
}
```

- [ ] **Step 3: Render button, summary, and deletion list**

Add a new primary button before the existing one-way sync button:

```tsx
<button type="button" className="primary-action" onClick={handleSyncBidirectional} disabled={isBusy}>
  {syncBoth.isPending ? '双向同步中' : '双向同步'}
</button>
```

Render summary before `<footer className="sync-actions">`:

```tsx
{lastBidirectionalResult && (
  <div className="sync-summary" aria-label="双向同步结果">
    <span>导入 {lastBidirectionalResult.imported}</span>
    <span>Obsidian 更新 {lastBidirectionalResult.pulled}</span>
    <span>写入 {lastBidirectionalResult.pushed}</span>
    <span>待确认删除 {lastBidirectionalResult.external_deleted}</span>
    <span>失败 {lastBidirectionalResult.failed}</span>
  </div>
)}
```

Render deletion list after summary:

```tsx
{Boolean(deletionsQ.data?.length) && (
  <div className="sync-deletions">
    <strong>Obsidian 已删除，等待确认</strong>
    {deletionsQ.data!.map((item) => (
      <div className="sync-deletion-item" key={item.note_id}>
        <div>
          <span>{item.title}</span>
          <code>{item.external_path}</code>
        </div>
        <div className="sync-deletion-actions">
          <button type="button" className="secondary-action" onClick={() => handleRestoreDeletion(item.note_id)} disabled={isBusy}>
            保留并重新导出
          </button>
          <button type="button" className="danger-action" onClick={() => handleConfirmDeletion(item.note_id)} disabled={isBusy}>
            确认删除
          </button>
        </div>
      </div>
    ))}
  </div>
)}
```

- [ ] **Step 4: Add styles**

Append to `frontend/src/styles/index.css`:

```css
.sync-summary {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(96px, 1fr));
  gap: 0.45rem;
}

.sync-summary span {
  border: 1px solid rgba(184, 115, 51, 0.18);
  border-radius: 7px;
  background: rgba(255, 255, 255, 0.62);
  color: var(--color-fs-text-secondary);
  font-size: 0.78rem;
  font-weight: 700;
  padding: 0.45rem 0.55rem;
}

.sync-deletions {
  display: grid;
  gap: 0.6rem;
  border-top: 1px solid var(--color-fs-border);
  padding-top: 0.85rem;
}

.sync-deletions > strong {
  color: var(--color-fs-text);
  font-size: 0.86rem;
}

.sync-deletion-item {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  gap: 0.75rem;
  border: 1px solid rgba(184, 115, 51, 0.16);
  border-radius: 8px;
  background: rgba(255, 255, 255, 0.7);
  padding: 0.65rem;
}

.sync-deletion-item span {
  display: block;
  color: var(--color-fs-text);
  font-weight: 800;
  font-size: 0.84rem;
}

.sync-deletion-item code {
  display: block;
  overflow-wrap: anywhere;
  color: var(--color-fs-text-muted);
  font-family: var(--font-family-mono);
  font-size: 0.72rem;
  margin-top: 0.2rem;
}

.sync-deletion-actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 0.45rem;
}

.danger-action {
  min-height: 34px;
  border: 1px solid rgba(180, 64, 48, 0.32);
  border-radius: 7px;
  background: rgba(180, 64, 48, 0.08);
  color: #9a382c;
  font-weight: 800;
  padding: 0 0.75rem;
}

@media (max-width: 640px) {
  .sync-deletion-item {
    grid-template-columns: 1fr;
  }

  .sync-deletion-actions {
    justify-content: flex-start;
  }
}
```

- [ ] **Step 5: Build frontend**

Run:

```powershell
cd frontend
npm run build
```

Expected: PASS.

- [ ] **Step 6: Commit**

Run:

```powershell
git add frontend/src/components/sync/ObsidianSyncPanel.tsx frontend/src/styles/index.css
git commit -m "feat: add obsidian bidirectional sync panel"
```

---

## Task 7: Editor Sync Card Status And Restore

**Files:**

- Modify: `frontend/src/components/sync/NoteSyncCard.tsx`
- Modify: `frontend/src/styles/index.css`

- [ ] **Step 1: Update status label and restore hook**

Modify `frontend/src/components/sync/NoteSyncCard.tsx`:

```tsx
import { useNoteSyncState, useRestoreObsidianDeletion, useSyncObsidianNote, useSyncTargets } from '../../hooks/useSync'

function syncStatusLabel(status: string | undefined) {
  if (status === 'synced') return '已同步'
  if (status === 'failed') return '同步失败'
  if (status === 'external_deleted') return 'Obsidian 已删除'
  return '未同步'
}
```

Inside the component:

```tsx
const restoreDeletion = useRestoreObsidianDeletion()
const isExternalDeleted = state?.status === 'external_deleted'

async function handleRestore() {
  await restoreDeletion.mutateAsync(noteID)
}
```

- [ ] **Step 2: Render external-deleted action**

Replace the existing action button area with:

```tsx
{isExternalDeleted ? (
  <>
    <p>这篇笔记在 Obsidian 中已删除，FlowSpace 正在等待确认。</p>
    <button type="button" className="secondary-action" onClick={handleRestore} disabled={restoreDeletion.isPending}>
      {restoreDeletion.isPending ? '重新导出中' : '保留并重新导出'}
    </button>
  </>
) : (
  <button type="button" className="secondary-action" onClick={handleSync} disabled={syncNote.isPending}>
    {syncNote.isPending ? '同步中' : '同步当前笔记'}
  </button>
)}
```

Keep the existing target path, external path, and error message display.

- [ ] **Step 3: Add status color style**

Append to `frontend/src/styles/index.css`:

```css
.sync-card-status-external_deleted {
  color: #9a382c;
}
```

- [ ] **Step 4: Build frontend**

Run:

```powershell
cd frontend
npm run build
```

Expected: PASS.

- [ ] **Step 5: Commit**

Run:

```powershell
git add frontend/src/components/sync/NoteSyncCard.tsx frontend/src/styles/index.css
git commit -m "feat: show obsidian deletion state in editor"
```

---

## Task 8: Documentation And End-To-End Verification

**Files:**

- Modify: `README.md`

- [ ] **Step 1: Update README rules**

Add an Obsidian bidirectional sync section to `README.md`:

```md
### Obsidian 双向同步

Obsidian 同步支持配置一个 Vault 路径和一个同步目录。双向同步只扫描 `vault_path/base_folder` 中的 Markdown 文件，不会扫描整个 Vault。

同步规则：

- FlowSpace 新增或修改的笔记会写入 Obsidian。
- Obsidian 新增的 Markdown 会导入 FlowSpace。
- Obsidian 修改的 Markdown 会更新 FlowSpace。
- 两边同时修改时，Obsidian 优先。
- Obsidian 删除已同步 Markdown 后，FlowSpace 只标记为“Obsidian 已删除”，需要在同步面板确认后才会删除 FlowSpace 笔记。
- 选择“保留并重新导出”会重新生成 Obsidian Markdown 文件。
```

- [ ] **Step 2: Run backend tests**

Run:

```powershell
cd backend
go test ./...
```

Expected: PASS.

- [ ] **Step 3: Build frontend**

Run:

```powershell
cd frontend
npm run build
```

Expected: PASS.

- [ ] **Step 4: Start test service**

Run:

```powershell
node scripts/start-flowspace.mjs --env test --backend-port 4101 --frontend-port 4100 --frontend-cmd "npm run dev -- --host 127.0.0.1"
```

Expected: backend listens on `127.0.0.1:4101`; frontend opens at `http://127.0.0.1:4100/`.

- [ ] **Step 5: Verify bidirectional sync manually with a temporary Vault**

Use PowerShell:

```powershell
$vault = Join-Path $env:TEMP "flowspace-bidirectional-vault"
$base = Join-Path $vault "FlowSpace Notes"
New-Item -ItemType Directory -Force -Path $base | Out-Null
Set-Content -LiteralPath (Join-Path $base "Imported From Obsidian.md") -Encoding UTF8 -Value "# Imported From Obsidian`n`nCreated in Obsidian."
```

In the browser at `http://127.0.0.1:4100/notes`:

- Open Obsidian sync settings.
- Set Vault path to the temporary `$vault`.
- Set base folder to `FlowSpace Notes`.
- Save and test the path.
- Click `双向同步`.

Expected:

- Summary shows `导入 1`.
- A note named `Imported From Obsidian` appears in FlowSpace.

- [ ] **Step 6: Verify Obsidian wins conflict**

Use the imported note:

- Edit the same note in FlowSpace and save.
- Edit the corresponding Markdown file in `$base` to contain `Changed in Obsidian`.
- Click `双向同步`.

Expected:

- The FlowSpace note body contains `Changed in Obsidian`.
- Summary shows at least one Obsidian update.

- [ ] **Step 7: Verify deletion confirmation**

Use PowerShell:

```powershell
Remove-Item -LiteralPath (Join-Path $base "Imported From Obsidian.md")
```

Click `双向同步`.

Expected:

- The note still exists in FlowSpace.
- Sync panel shows the note in `Obsidian 已删除，等待确认`.
- Clicking `保留并重新导出` recreates the Markdown file.
- Repeating the deletion and clicking `确认删除` removes the FlowSpace note.

- [ ] **Step 8: Commit docs**

Run:

```powershell
git add README.md
git commit -m "docs: document obsidian bidirectional sync"
```

---

## Self-Review

Spec coverage:

- Only configured base folder is scanned: Task 2 scanning helper and Task 8 manual verification.
- Obsidian import: Task 3 import test and service path.
- Obsidian modifies FlowSpace: Task 3 pull/conflict test.
- Conflict uses Obsidian priority: Task 3 conflict test.
- FlowSpace pushes to Obsidian: Task 3 push test and existing one-way sync preservation.
- Obsidian deletion does not automatically delete FlowSpace: Task 3 deletion detection and Task 4 confirmation.
- Deletion confirm and restore: Task 4 backend, Task 6 panel, Task 7 editor card.
- Existing one-way sync remains: Task 2 updates `writeNoteToTarget` without removing existing routes.
- Frontend summary and deletion UI: Task 6.
- README update: Task 8.

Red-flag scan:

- The plan avoids undefined filler markers.
- Every task has concrete files, tests, commands, and expected outcomes.

Type consistency:

- Backend result type is `ObsidianBidirectionalResult`; frontend mirrors it as `ObsidianBidirectionalResult`.
- Deletion status is `external_deleted` in backend model, repository state, frontend API type, panel, and editor card.
- New API paths match the design: `/api/sync/obsidian/bidirectional`, `/api/sync/obsidian/deletions`, `/api/sync/obsidian/deletions/:note_id/confirm`, and `/api/sync/obsidian/deletions/:note_id/restore`.
