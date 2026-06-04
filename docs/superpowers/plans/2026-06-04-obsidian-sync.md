# Obsidian Local Note Sync Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build one-way FlowSpace-to-Obsidian local Markdown sync with vault configuration, path testing, single-note sync, batch sync, sync status, and editor/list UI entry points.

**Architecture:** FlowSpace remains the source of truth. The Go backend stores sync target configuration and note-to-file sync state in SQLite, renders notes to Markdown with frontmatter, writes files only inside the configured vault folder, and exposes sync APIs. The React frontend provides Obsidian settings, manual sync actions, status display, and optional auto-sync after a successful note save.

**Tech Stack:** Go 1.26.x, Gin, SQLite, React 19, TanStack Query, Vite, TypeScript.

---

## File Structure

Backend files:

- Create `backend/internal/model/sync.go`: sync target, sync state, request and response types.
- Create `backend/internal/repository/sync.go`: SQLite CRUD for `sync_targets` and `note_sync_state`.
- Create `backend/internal/repository/sync_test.go`: repository tests using a temporary SQLite database.
- Create `backend/internal/service/obsidian_sync.go`: filename sanitization, path safety, Markdown rendering, file writing, single and batch sync.
- Create `backend/internal/service/obsidian_sync_test.go`: path safety and Markdown rendering tests.
- Create `backend/internal/handler/sync.go`: HTTP handlers for target config, path test, single sync, folder sync, all sync, and sync state.
- Modify `backend/internal/router/router.go`: register `/api/sync/*` and `/api/notes/:id/sync-state`.
- Modify `backend/db/schema.sql`: add sync tables.

Frontend files:

- Create `frontend/src/api/sync.ts`: typed API calls.
- Create `frontend/src/hooks/useSync.ts`: TanStack Query hooks for sync targets, sync actions, and note sync state.
- Create `frontend/src/components/sync/ObsidianSyncPanel.tsx`: settings and batch sync panel.
- Create `frontend/src/components/sync/NoteSyncCard.tsx`: editor-side note sync status and actions.
- Modify `frontend/src/routes/Notes.tsx`: add sync panel entry.
- Modify `frontend/src/routes/Editor.tsx`: render sync card and auto-sync after save.
- Modify `frontend/src/styles/index.css`: sync panel/card/button/status styles.

Verification files:

- Create `test-obsidian-sync.mjs`: Playwright smoke test for settings panel and editor sync card.

---

## Task 1: Backend Sync Schema And Models

**Files:**

- Modify: `backend/db/schema.sql`
- Create: `backend/internal/model/sync.go`
- Test: `backend/internal/repository/sync_test.go`

- [ ] **Step 1: Add failing repository test for sync table access**

Create `backend/internal/repository/sync_test.go`:

```go
package repository

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "flowspace-test.db")+"?_foreign_keys=ON")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	DB = db
	schema, err := os.ReadFile(filepath.Join("..", "..", "db", "schema.sql"))
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	if _, err := DB.Exec(string(schema)); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
}

func TestSyncTargetRoundTrip(t *testing.T) {
	openTestDB(t)
	target := &model.SyncTarget{
		Type:       "obsidian",
		Name:       "Local Vault",
		VaultPath:  "C:\\Vault",
		BaseFolder: "FlowSpace Notes",
		Enabled:    true,
		AutoSync:   true,
	}
	if err := SaveSyncTarget(target); err != nil {
		t.Fatalf("save target: %v", err)
	}
	got, err := GetDefaultSyncTarget("obsidian")
	if err != nil {
		t.Fatalf("get target: %v", err)
	}
	if got.ID == "" || got.VaultPath != "C:\\Vault" || !got.AutoSync {
		t.Fatalf("unexpected target: %+v", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```powershell
cd backend
go test ./internal/repository -run TestSyncTargetRoundTrip -v
```

Expected: FAIL because `model.SyncTarget`, `SaveSyncTarget`, and `GetDefaultSyncTarget` do not exist.

- [ ] **Step 3: Add sync tables to schema**

Append to `backend/db/schema.sql`:

```sql
CREATE TABLE IF NOT EXISTS sync_targets (
  id TEXT PRIMARY KEY,
  type TEXT NOT NULL,
  name TEXT NOT NULL,
  vault_path TEXT NOT NULL,
  base_folder TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  auto_sync INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS note_sync_state (
  note_id TEXT NOT NULL,
  target_id TEXT NOT NULL,
  external_path TEXT NOT NULL,
  content_hash TEXT NOT NULL,
  last_synced_at INTEGER,
  status TEXT NOT NULL,
  error_message TEXT,
  PRIMARY KEY (note_id, target_id),
  FOREIGN KEY (note_id) REFERENCES notes(id) ON DELETE CASCADE,
  FOREIGN KEY (target_id) REFERENCES sync_targets(id) ON DELETE CASCADE
);
```

- [ ] **Step 4: Add sync model types**

Create `backend/internal/model/sync.go`:

```go
package model

type SyncTarget struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Name       string `json:"name"`
	VaultPath  string `json:"vault_path"`
	BaseFolder string `json:"base_folder"`
	Enabled    bool   `json:"enabled"`
	AutoSync   bool   `json:"auto_sync"`
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
}

type SyncState struct {
	NoteID       string  `json:"note_id"`
	TargetID     string  `json:"target_id"`
	ExternalPath string  `json:"external_path"`
	ContentHash  string  `json:"content_hash"`
	LastSyncedAt *int64  `json:"last_synced_at"`
	Status       string  `json:"status"`
	ErrorMessage *string `json:"error_message"`
}

type SaveSyncTargetRequest struct {
	Name       string `json:"name" binding:"required"`
	VaultPath  string `json:"vault_path" binding:"required"`
	BaseFolder string `json:"base_folder" binding:"required"`
	Enabled    bool   `json:"enabled"`
	AutoSync   bool   `json:"auto_sync"`
}

type SyncResultItem struct {
	NoteID       string `json:"note_id"`
	Status       string `json:"status"`
	ExternalPath string `json:"external_path,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
}

type SyncBatchResult struct {
	Synced int              `json:"synced"`
	Failed int              `json:"failed"`
	Items  []SyncResultItem `json:"items"`
}
```

- [ ] **Step 5: Commit**

```powershell
git add backend/db/schema.sql backend/internal/model/sync.go backend/internal/repository/sync_test.go
git commit -m "test: define obsidian sync persistence contract"
```

---

## Task 2: Sync Repository

**Files:**

- Create: `backend/internal/repository/sync.go`
- Modify: `backend/internal/repository/sync_test.go`

- [ ] **Step 1: Implement repository functions**

Create `backend/internal/repository/sync.go`:

```go
package repository

import (
	"database/sql"

	"github.com/hujinrun/flowspace/internal/model"
)

func SaveSyncTarget(target *model.SyncTarget) error {
	now := nowUnix()
	if target.ID == "" {
		target.ID = newUUID()
		target.CreatedAt = now
	}
	target.UpdatedAt = now
	_, err := DB.Exec(`
		INSERT INTO sync_targets (id, type, name, vault_path, base_folder, enabled, auto_sync, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
		  name = excluded.name,
		  vault_path = excluded.vault_path,
		  base_folder = excluded.base_folder,
		  enabled = excluded.enabled,
		  auto_sync = excluded.auto_sync,
		  updated_at = excluded.updated_at
	`, target.ID, target.Type, target.Name, target.VaultPath, target.BaseFolder, boolToInt(target.Enabled), boolToInt(target.AutoSync), target.CreatedAt, target.UpdatedAt)
	return err
}

func GetDefaultSyncTarget(syncType string) (*model.SyncTarget, error) {
	row := DB.QueryRow(`
		SELECT id, type, name, vault_path, base_folder, enabled, auto_sync, created_at, updated_at
		FROM sync_targets
		WHERE type = ? AND enabled = 1
		ORDER BY updated_at DESC
		LIMIT 1
	`, syncType)
	return scanSyncTarget(row)
}

func ListSyncTargets() ([]model.SyncTarget, error) {
	rows, err := DB.Query(`
		SELECT id, type, name, vault_path, base_folder, enabled, auto_sync, created_at, updated_at
		FROM sync_targets
		ORDER BY updated_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	targets := []model.SyncTarget{}
	for rows.Next() {
		var target model.SyncTarget
		var enabled, autoSync int
		if err := rows.Scan(&target.ID, &target.Type, &target.Name, &target.VaultPath, &target.BaseFolder, &enabled, &autoSync, &target.CreatedAt, &target.UpdatedAt); err != nil {
			return nil, err
		}
		target.Enabled = enabled == 1
		target.AutoSync = autoSync == 1
		targets = append(targets, target)
	}
	return targets, nil
}

func UpsertSyncState(state *model.SyncState) error {
	_, err := DB.Exec(`
		INSERT INTO note_sync_state (note_id, target_id, external_path, content_hash, last_synced_at, status, error_message)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(note_id, target_id) DO UPDATE SET
		  external_path = excluded.external_path,
		  content_hash = excluded.content_hash,
		  last_synced_at = excluded.last_synced_at,
		  status = excluded.status,
		  error_message = excluded.error_message
	`, state.NoteID, state.TargetID, state.ExternalPath, state.ContentHash, state.LastSyncedAt, state.Status, state.ErrorMessage)
	return err
}

func GetSyncState(noteID, targetID string) (*model.SyncState, error) {
	row := DB.QueryRow(`
		SELECT note_id, target_id, external_path, content_hash, last_synced_at, status, error_message
		FROM note_sync_state
		WHERE note_id = ? AND target_id = ?
	`, noteID, targetID)
	var state model.SyncState
	if err := row.Scan(&state.NoteID, &state.TargetID, &state.ExternalPath, &state.ContentHash, &state.LastSyncedAt, &state.Status, &state.ErrorMessage); err != nil {
		return nil, err
	}
	return &state, nil
}

func scanSyncTarget(row *sql.Row) (*model.SyncTarget, error) {
	var target model.SyncTarget
	var enabled, autoSync int
	if err := row.Scan(&target.ID, &target.Type, &target.Name, &target.VaultPath, &target.BaseFolder, &enabled, &autoSync, &target.CreatedAt, &target.UpdatedAt); err != nil {
		return nil, err
	}
	target.Enabled = enabled == 1
	target.AutoSync = autoSync == 1
	return &target, nil
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
```

- [ ] **Step 2: Fix test import**

Modify `backend/internal/repository/sync_test.go` imports to include:

```go
import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/hujinrun/flowspace/internal/model"
	_ "modernc.org/sqlite"
)
```

- [ ] **Step 3: Run repository tests**

```powershell
cd backend
go test ./internal/repository -run TestSyncTargetRoundTrip -v
```

Expected: PASS.

- [ ] **Step 4: Commit**

```powershell
git add backend/internal/repository/sync.go backend/internal/repository/sync_test.go
git commit -m "feat: persist obsidian sync targets"
```

---

## Task 3: Obsidian Sync Service

**Files:**

- Create: `backend/internal/service/obsidian_sync.go`
- Create: `backend/internal/service/obsidian_sync_test.go`

- [ ] **Step 1: Add failing service tests**

Create `backend/internal/service/obsidian_sync_test.go`:

```go
package service

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/hujinrun/flowspace/internal/model"
)

func TestSanitizeMarkdownFileName(t *testing.T) {
	got := sanitizeMarkdownFileName(`A/B:C*D?`)
	if got != "A-B-C-D.md" {
		t.Fatalf("got %q", got)
	}
}

func TestRenderObsidianMarkdownIncludesFrontmatter(t *testing.T) {
	note := &model.Note{
		ID:        "n01",
		Title:     "产品规划",
		Body:      "正文",
		FolderID:  "__work",
		Tags:      `["产品","规划"]`,
		CreatedAt: 1780000000,
		UpdatedAt: 1780000300,
	}
	md := renderObsidianMarkdown(note)
	for _, part := range []string{"---", "id: n01", "source: flowspace", "- 产品", "# 产品规划", "正文"} {
		if !strings.Contains(md, part) {
			t.Fatalf("markdown missing %q:\n%s", part, md)
		}
	}
}

func TestResolveOutputPathRejectsEscape(t *testing.T) {
	vault := t.TempDir()
	target := &model.SyncTarget{VaultPath: vault, BaseFolder: "FlowSpace Notes"}
	_, err := resolveOutputPath(target, "..\\escape.md")
	if err == nil {
		t.Fatal("expected escape path to be rejected")
	}
}

func TestResolveOutputPathAllowsBaseFolder(t *testing.T) {
	vault := t.TempDir()
	target := &model.SyncTarget{VaultPath: vault, BaseFolder: "FlowSpace Notes"}
	got, err := resolveOutputPath(target, "Hello.md")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	wantPrefix := filepath.Join(vault, "FlowSpace Notes")
	if !strings.HasPrefix(got, wantPrefix) {
		t.Fatalf("expected %q to start with %q", got, wantPrefix)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```powershell
cd backend
go test ./internal/service -run "TestSanitizeMarkdownFileName|TestRenderObsidianMarkdownIncludesFrontmatter|TestResolveOutputPath" -v
```

Expected: FAIL because service functions do not exist.

- [ ] **Step 3: Implement service helpers and single-note sync**

Create `backend/internal/service/obsidian_sync.go`:

```go
package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/repository"
)

var invalidFileNameChars = regexp.MustCompile(`[<>:"/\\|?*]+`)

func TestObsidianTarget(target *model.SyncTarget) error {
	if target == nil {
		return errors.New("sync target is required")
	}
	info, err := os.Stat(target.VaultPath)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("vault path is not a directory")
	}
	baseDir := filepath.Join(target.VaultPath, target.BaseFolder)
	return os.MkdirAll(baseDir, 0755)
}

func SyncNoteToObsidian(noteID string) (*model.SyncResultItem, error) {
	target, err := repository.GetDefaultSyncTarget("obsidian")
	if err != nil {
		return nil, err
	}
	if err := TestObsidianTarget(target); err != nil {
		return nil, err
	}
	note, err := repository.GetNoteByID(noteID)
	if err != nil {
		return nil, err
	}
	return writeNoteToTarget(note, target)
}

func SyncNotesToObsidian(notes []model.Note) model.SyncBatchResult {
	result := model.SyncBatchResult{Items: []model.SyncResultItem{}}
	target, err := repository.GetDefaultSyncTarget("obsidian")
	if err != nil {
		return failedBatch(notes, err)
	}
	if err := TestObsidianTarget(target); err != nil {
		return failedBatch(notes, err)
	}
	for i := range notes {
		item, err := writeNoteToTarget(&notes[i], target)
		if err != nil {
			result.Failed++
			result.Items = append(result.Items, model.SyncResultItem{NoteID: notes[i].ID, Status: "failed", ErrorMessage: err.Error()})
			continue
		}
		result.Synced++
		result.Items = append(result.Items, *item)
	}
	return result
}

func writeNoteToTarget(note *model.Note, target *model.SyncTarget) (*model.SyncResultItem, error) {
	md := renderObsidianMarkdown(note)
	hash := sha256.Sum256([]byte(md))
	fileName := sanitizeMarkdownFileName(note.Title)
	if strings.TrimSuffix(fileName, ".md") == "" {
		fileName = fmt.Sprintf("Untitled-%s.md", note.ID)
	}
	path, err := resolveOutputPath(target, fileName)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(md), 0644); err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	state := &model.SyncState{
		NoteID:       note.ID,
		TargetID:     target.ID,
		ExternalPath: path,
		ContentHash:  hex.EncodeToString(hash[:]),
		LastSyncedAt: &now,
		Status:       "synced",
	}
	if err := repository.UpsertSyncState(state); err != nil {
		return nil, err
	}
	return &model.SyncResultItem{NoteID: note.ID, Status: "synced", ExternalPath: path}, nil
}

func renderObsidianMarkdown(note *model.Note) string {
	tags := parseTags(note.Tags)
	lines := []string{
		"---",
		fmt.Sprintf("id: %s", note.ID),
		"source: flowspace",
		fmt.Sprintf("folder: %s", note.FolderID),
		"tags:",
	}
	for _, tag := range tags {
		lines = append(lines, fmt.Sprintf("  - %s", tag))
	}
	lines = append(lines,
		fmt.Sprintf("created: %s", time.Unix(note.CreatedAt, 0).Format(time.RFC3339)),
		fmt.Sprintf("updated: %s", time.Unix(note.UpdatedAt, 0).Format(time.RFC3339)),
		"---",
		"",
	)
	body := strings.TrimSpace(note.Body)
	titleLine := "# " + note.Title
	if !strings.HasPrefix(body, titleLine) {
		lines = append(lines, titleLine, "")
	}
	lines = append(lines, body, "")
	return strings.Join(lines, "\n")
}

func sanitizeMarkdownFileName(title string) string {
	cleaned := strings.TrimSpace(invalidFileNameChars.ReplaceAllString(title, "-"))
	cleaned = strings.Trim(cleaned, ". ")
	if cleaned == "" {
		return ".md"
	}
	return cleaned + ".md"
}

func resolveOutputPath(target *model.SyncTarget, fileName string) (string, error) {
	baseDir, err := filepath.Abs(filepath.Join(target.VaultPath, target.BaseFolder))
	if err != nil {
		return "", err
	}
	out, err := filepath.Abs(filepath.Join(baseDir, fileName))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(baseDir, out)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", errors.New("output path escapes vault folder")
	}
	return out, nil
}

func parseTags(raw string) []string {
	var tags []string
	if err := json.Unmarshal([]byte(raw), &tags); err != nil {
		return []string{}
	}
	return tags
}

func failedBatch(notes []model.Note, err error) model.SyncBatchResult {
	result := model.SyncBatchResult{Failed: len(notes), Items: []model.SyncResultItem{}}
	for _, note := range notes {
		result.Items = append(result.Items, model.SyncResultItem{NoteID: note.ID, Status: "failed", ErrorMessage: err.Error()})
	}
	return result
}
```

- [ ] **Step 4: Run service tests**

```powershell
cd backend
go test ./internal/service -run "TestSanitizeMarkdownFileName|TestRenderObsidianMarkdownIncludesFrontmatter|TestResolveOutputPath" -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add backend/internal/service/obsidian_sync.go backend/internal/service/obsidian_sync_test.go
git commit -m "feat: render and write obsidian markdown"
```

---

## Task 4: Sync HTTP API

**Files:**

- Create: `backend/internal/handler/sync.go`
- Modify: `backend/internal/router/router.go`
- Modify: `backend/internal/service/obsidian_sync.go`

- [ ] **Step 1: Add handler implementation**

Create `backend/internal/handler/sync.go`:

```go
package handler

import (
	"database/sql"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/repository"
	"github.com/hujinrun/flowspace/internal/service"
)

func ListSyncTargets(c *gin.Context) {
	targets, err := repository.ListSyncTargets()
	if err != nil {
		internalError(c, "failed to list sync targets")
		return
	}
	success(c, gin.H{"targets": targets})
}

func SaveSyncTarget(c *gin.Context) {
	var req model.SaveSyncTargetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid sync target")
		return
	}
	target := &model.SyncTarget{
		Type:       "obsidian",
		Name:       req.Name,
		VaultPath:  req.VaultPath,
		BaseFolder: req.BaseFolder,
		Enabled:    req.Enabled,
		AutoSync:   req.AutoSync,
	}
	if err := repository.SaveSyncTarget(target); err != nil {
		internalError(c, "failed to save sync target")
		return
	}
	success(c, gin.H{"target": target})
}

func TestObsidianTarget(c *gin.Context) {
	var req model.SaveSyncTargetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid sync target")
		return
	}
	target := &model.SyncTarget{Type: "obsidian", Name: req.Name, VaultPath: req.VaultPath, BaseFolder: req.BaseFolder, Enabled: true}
	if err := service.TestObsidianTarget(target); err != nil {
		badRequest(c, err.Error())
		return
	}
	success(c, gin.H{"ok": true})
}

func SyncObsidianNote(c *gin.Context) {
	item, err := service.SyncNoteToObsidian(c.Param("id"))
	if err != nil {
		internalError(c, err.Error())
		return
	}
	success(c, gin.H{"item": item})
}

func SyncObsidianFolder(c *gin.Context) {
	notes, _, err := repository.GetNotes(c.Param("folder_id"), "recent", 1, 10000)
	if err != nil {
		internalError(c, "failed to load notes")
		return
	}
	result := service.SyncNotesToObsidian(notes)
	success(c, gin.H{"result": result})
}

func SyncObsidianAll(c *gin.Context) {
	notes, _, err := repository.GetNotes("", "recent", 1, 10000)
	if err != nil {
		internalError(c, "failed to load notes")
		return
	}
	result := service.SyncNotesToObsidian(notes)
	success(c, gin.H{"result": result})
}

func GetNoteSyncState(c *gin.Context) {
	target, err := repository.GetDefaultSyncTarget("obsidian")
	if err == sql.ErrNoRows {
		success(c, gin.H{"state": nil})
		return
	}
	if err != nil {
		internalError(c, "failed to get sync target")
		return
	}
	state, err := repository.GetSyncState(c.Param("id"), target.ID)
	if err == sql.ErrNoRows {
		success(c, gin.H{"state": nil})
		return
	}
	if err != nil {
		internalError(c, "failed to get sync state")
		return
	}
	success(c, gin.H{"state": state})
}
```

- [ ] **Step 2: Register routes**

Modify `backend/internal/router/router.go` inside the `/api` group:

```go
api.GET("/sync/targets", handler.ListSyncTargets)
api.POST("/sync/targets", handler.SaveSyncTarget)
api.POST("/sync/obsidian/test", handler.TestObsidianTarget)
api.POST("/sync/obsidian/notes/:id", handler.SyncObsidianNote)
api.POST("/sync/obsidian/folders/:folder_id", handler.SyncObsidianFolder)
api.POST("/sync/obsidian/all", handler.SyncObsidianAll)
api.GET("/notes/:id/sync-state", handler.GetNoteSyncState)
```

- [ ] **Step 3: Run backend tests**

```powershell
cd backend
go test ./...
```

Expected: PASS.

- [ ] **Step 4: Manually verify API with temporary vault**

Run backend and use a temporary vault path:

```powershell
cd backend
$env:PORT='8080'
.\server-flowspace.exe
```

In another shell:

```powershell
$vault = Join-Path $env:TEMP 'flowspace-obsidian-vault'
New-Item -ItemType Directory -Force -Path $vault
Invoke-RestMethod -Method Post -Uri http://127.0.0.1:8080/api/sync/targets -ContentType 'application/json' -Body (@{
  name='Temp Vault'
  vault_path=$vault
  base_folder='FlowSpace Notes'
  enabled=$true
  auto_sync=$false
} | ConvertTo-Json)
Invoke-RestMethod -Method Post -Uri http://127.0.0.1:8080/api/sync/obsidian/all
Get-ChildItem "$vault\FlowSpace Notes" -Filter *.md
```

Expected: at least one `.md` file appears if the database has notes.

- [ ] **Step 5: Commit**

```powershell
git add backend/internal/handler/sync.go backend/internal/router/router.go backend/internal/service/obsidian_sync.go
git commit -m "feat: expose obsidian sync api"
```

---

## Task 5: Frontend Sync API And Hooks

**Files:**

- Create: `frontend/src/api/sync.ts`
- Create: `frontend/src/hooks/useSync.ts`

- [ ] **Step 1: Add sync API client**

Create `frontend/src/api/sync.ts`:

```ts
import { api } from './client'

export interface SyncTarget {
  id: string
  type: 'obsidian'
  name: string
  vault_path: string
  base_folder: string
  enabled: boolean
  auto_sync: boolean
  created_at: number
  updated_at: number
}

export interface SaveSyncTargetInput {
  name: string
  vault_path: string
  base_folder: string
  enabled: boolean
  auto_sync: boolean
}

export interface SyncState {
  note_id: string
  target_id: string
  external_path: string
  content_hash: string
  last_synced_at: number | null
  status: 'synced' | 'pending' | 'failed'
  error_message: string | null
}

export interface SyncResultItem {
  note_id: string
  status: string
  external_path?: string
  error_message?: string
}

export interface SyncBatchResult {
  synced: number
  failed: number
  items: SyncResultItem[]
}

export async function getSyncTargets(): Promise<SyncTarget[]> {
  const res = await api.get<{ targets: SyncTarget[] }>('/api/sync/targets')
  return res.data.targets
}

export async function saveSyncTarget(input: SaveSyncTargetInput): Promise<SyncTarget> {
  const res = await api.post<{ target: SyncTarget }>('/api/sync/targets', input)
  return res.data.target
}

export async function testObsidianTarget(input: SaveSyncTargetInput): Promise<void> {
  await api.post<{ ok: boolean }>('/api/sync/obsidian/test', input)
}

export async function syncObsidianNote(id: string): Promise<SyncResultItem> {
  const res = await api.post<{ item: SyncResultItem }>(`/api/sync/obsidian/notes/${id}`)
  return res.data.item
}

export async function syncObsidianFolder(folderID: string): Promise<SyncBatchResult> {
  const res = await api.post<{ result: SyncBatchResult }>(`/api/sync/obsidian/folders/${folderID}`)
  return res.data.result
}

export async function syncObsidianAll(): Promise<SyncBatchResult> {
  const res = await api.post<{ result: SyncBatchResult }>('/api/sync/obsidian/all')
  return res.data.result
}

export async function getNoteSyncState(id: string): Promise<SyncState | null> {
  const res = await api.get<{ state: SyncState | null }>(`/api/notes/${id}/sync-state`)
  return res.data.state
}
```

- [ ] **Step 2: Add hooks**

Create `frontend/src/hooks/useSync.ts`:

```ts
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import * as syncApi from '../api/sync'

export function useSyncTargets() {
  return useQuery({ queryKey: ['sync-targets'], queryFn: syncApi.getSyncTargets })
}

export function useSaveSyncTarget() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: syncApi.saveSyncTarget,
    onSuccess: () => qc.invalidateQueries({ queryKey: ['sync-targets'] }),
  })
}

export function useTestObsidianTarget() {
  return useMutation({ mutationFn: syncApi.testObsidianTarget })
}

export function useNoteSyncState(noteID: string | undefined) {
  return useQuery({
    queryKey: ['note-sync-state', noteID],
    queryFn: () => syncApi.getNoteSyncState(noteID!),
    enabled: Boolean(noteID),
  })
}

export function useSyncObsidianNote(noteID: string | undefined) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => syncApi.syncObsidianNote(noteID!),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['note-sync-state', noteID] })
      qc.invalidateQueries({ queryKey: ['sync-targets'] })
    },
  })
}

export function useSyncObsidianAll() {
  return useMutation({ mutationFn: syncApi.syncObsidianAll })
}
```

- [ ] **Step 3: Run frontend build**

```powershell
cd frontend
npm run build
```

Expected: PASS.

- [ ] **Step 4: Commit**

```powershell
git add frontend/src/api/sync.ts frontend/src/hooks/useSync.ts
git commit -m "feat: add obsidian sync frontend api"
```

---

## Task 6: Notes Sync Panel UI

**Files:**

- Create: `frontend/src/components/sync/ObsidianSyncPanel.tsx`
- Modify: `frontend/src/routes/Notes.tsx`
- Modify: `frontend/src/styles/index.css`

- [ ] **Step 1: Create sync panel component**

Create `frontend/src/components/sync/ObsidianSyncPanel.tsx`:

```tsx
import { useEffect, useMemo, useState } from 'react'
import { useSaveSyncTarget, useSyncObsidianAll, useSyncTargets, useTestObsidianTarget } from '../../hooks/useSync'

export function ObsidianSyncPanel({ onClose }: { onClose: () => void }) {
  const targetsQ = useSyncTargets()
  const saveTarget = useSaveSyncTarget()
  const testTarget = useTestObsidianTarget()
  const syncAll = useSyncObsidianAll()
  const target = useMemo(() => targetsQ.data?.find((t) => t.type === 'obsidian'), [targetsQ.data])

  const [name, setName] = useState('Obsidian Vault')
  const [vaultPath, setVaultPath] = useState('')
  const [baseFolder, setBaseFolder] = useState('FlowSpace Notes')
  const [autoSync, setAutoSync] = useState(false)
  const [message, setMessage] = useState('')

  useEffect(() => {
    if (!target) return
    setName(target.name)
    setVaultPath(target.vault_path)
    setBaseFolder(target.base_folder)
    setAutoSync(target.auto_sync)
  }, [target])

  const payload = { name, vault_path: vaultPath, base_folder: baseFolder, enabled: true, auto_sync: autoSync }

  async function handleSave() {
    setMessage('')
    try {
      await saveTarget.mutateAsync(payload)
      setMessage('同步设置已保存')
    } catch {
      setMessage('保存失败，请检查路径和后端服务')
    }
  }

  async function handleTest() {
    setMessage('')
    try {
      await testTarget.mutateAsync(payload)
      setMessage('路径可用')
    } catch {
      setMessage('路径不可用或没有写入权限')
    }
  }

  async function handleSyncAll() {
    setMessage('')
    try {
      const result = await syncAll.mutateAsync()
      setMessage(`同步完成：成功 ${result.synced}，失败 ${result.failed}`)
    } catch {
      setMessage('同步失败，请先保存并测试路径')
    }
  }

  return (
    <div className="sync-overlay" onClick={onClose}>
      <section className="sync-panel" onClick={(e) => e.stopPropagation()}>
        <header className="sync-panel-header">
          <div>
            <span>Obsidian</span>
            <h2>本地 Vault 同步</h2>
          </div>
          <button type="button" onClick={onClose}>×</button>
        </header>

        <label className="sync-field">
          <span>目标名称</span>
          <input value={name} onChange={(e) => setName(e.target.value)} />
        </label>
        <label className="sync-field">
          <span>Vault 路径</span>
          <input value={vaultPath} onChange={(e) => setVaultPath(e.target.value)} placeholder="D:\\Obsidian\\MyVault" />
        </label>
        <label className="sync-field">
          <span>同步目录</span>
          <input value={baseFolder} onChange={(e) => setBaseFolder(e.target.value)} />
        </label>
        <label className="sync-toggle">
          <input type="checkbox" checked={autoSync} onChange={(e) => setAutoSync(e.target.checked)} />
          <span>保存笔记后自动同步</span>
        </label>

        {message && <p className="sync-message">{message}</p>}

        <footer className="sync-actions">
          <button type="button" className="secondary-action" onClick={handleTest}>测试路径</button>
          <button type="button" className="secondary-action" onClick={handleSave}>保存设置</button>
          <button type="button" className="primary-action" onClick={handleSyncAll}>同步全部</button>
        </footer>
      </section>
    </div>
  )
}
```

- [ ] **Step 2: Add panel entry to notes page**

Modify `frontend/src/routes/Notes.tsx`:

```tsx
import { ObsidianSyncPanel } from '../components/sync/ObsidianSyncPanel'
```

Add state near existing `useState` calls:

```tsx
const [syncOpen, setSyncOpen] = useState(false)
```

In toolbar actions, add:

```tsx
<button onClick={() => setSyncOpen(true)} className="secondary-action">同步</button>
```

Before the closing root `</div>`, add:

```tsx
{syncOpen && <ObsidianSyncPanel onClose={() => setSyncOpen(false)} />}
```

- [ ] **Step 3: Add sync styles**

Append to `frontend/src/styles/index.css`:

```css
.sync-overlay {
  position: fixed;
  inset: 0;
  z-index: 90;
  display: grid;
  place-items: center;
  background: rgba(44, 36, 22, 0.24);
  backdrop-filter: blur(3px);
  padding: 1rem;
}

.sync-panel {
  width: min(520px, 100%);
  display: grid;
  gap: 1rem;
  border: 1px solid var(--color-fs-border);
  border-radius: 12px;
  background: var(--color-fs-surface);
  box-shadow: var(--shadow-popover);
  padding: 1.25rem;
}

.sync-panel-header {
  display: flex;
  justify-content: space-between;
  gap: 1rem;
  align-items: flex-start;
}

.sync-panel-header span,
.sync-field span {
  color: var(--color-fs-text-muted);
  font-size: 0.75rem;
  font-weight: 800;
}

.sync-panel-header h2 {
  margin-top: 0.2rem;
  font-family: var(--font-family-heading);
  font-size: 1.25rem;
}

.sync-panel-header button {
  border: 0;
  background: transparent;
  color: var(--color-fs-text-muted);
  font-size: 1.3rem;
  cursor: pointer;
}

.sync-field {
  display: grid;
  gap: 0.45rem;
}

.sync-field input {
  min-height: 40px;
  border: 1px solid var(--color-fs-border);
  border-radius: 8px;
  background: #fffdfa;
  color: var(--color-fs-text);
  padding: 0 0.8rem;
  outline: none;
}

.sync-toggle {
  display: inline-flex;
  align-items: center;
  gap: 0.5rem;
  color: var(--color-fs-text-secondary);
  font-size: 0.86rem;
}

.sync-message {
  color: var(--color-fs-text-secondary);
  font-size: 0.84rem;
}

.sync-actions {
  display: flex;
  justify-content: flex-end;
  gap: 0.6rem;
  flex-wrap: wrap;
}
```

- [ ] **Step 4: Run frontend build**

```powershell
cd frontend
npm run build
```

Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add frontend/src/components/sync/ObsidianSyncPanel.tsx frontend/src/routes/Notes.tsx frontend/src/styles/index.css
git commit -m "feat: add obsidian sync settings panel"
```

---

## Task 7: Editor Sync Card And Auto-Sync

**Files:**

- Create: `frontend/src/components/sync/NoteSyncCard.tsx`
- Modify: `frontend/src/routes/Editor.tsx`
- Modify: `frontend/src/styles/index.css`

- [ ] **Step 1: Create note sync card**

Create `frontend/src/components/sync/NoteSyncCard.tsx`:

```tsx
import { useNoteSyncState, useSyncObsidianNote, useSyncTargets } from '../../hooks/useSync'

export function NoteSyncCard({ noteID }: { noteID: string }) {
  const targetsQ = useSyncTargets()
  const stateQ = useNoteSyncState(noteID)
  const syncNote = useSyncObsidianNote(noteID)
  const target = targetsQ.data?.find((t) => t.type === 'obsidian')
  const state = stateQ.data

  async function handleSync() {
    await syncNote.mutateAsync()
  }

  return (
    <div className="sync-card">
      <div className="sync-card-header">
        <span>Obsidian</span>
        <strong>{state?.status === 'synced' ? '已同步' : state?.status === 'failed' ? '同步失败' : '未同步'}</strong>
      </div>
      {target ? (
        <>
          <p>{target.vault_path}</p>
          {state?.external_path && <code>{state.external_path}</code>}
          {state?.error_message && <em>{state.error_message}</em>}
          <button type="button" className="secondary-action" onClick={handleSync} disabled={syncNote.isPending}>
            {syncNote.isPending ? '同步中...' : '同步当前笔记'}
          </button>
        </>
      ) : (
        <p>还没有配置 Obsidian Vault</p>
      )}
    </div>
  )
}
```

- [ ] **Step 2: Render sync card in editor inspector**

Modify `frontend/src/routes/Editor.tsx` imports:

```tsx
import { NoteSyncCard } from '../components/sync/NoteSyncCard'
import { useSyncObsidianNote, useSyncTargets } from '../hooks/useSync'
```

Inside `EditorPage`, after `const updateNote = useUpdateNote()`:

```tsx
const syncTargetsQ = useSyncTargets()
const syncNote = useSyncObsidianNote(id)
const autoSyncEnabled = syncTargetsQ.data?.some((t) => t.type === 'obsidian' && t.enabled && t.auto_sync)
```

In the interval auto-save block, replace the save mutation call with:

```tsx
updateNote.mutate(
  { id: id!, title: title.trim(), body: md },
  {
    onSuccess: () => {
      if (autoSyncEnabled) syncNote.mutate()
    },
  },
)
```

In the manual `save` callback, replace the mutation call with the same `onSuccess` pattern.

In the inspector, add this section near the top:

```tsx
{id && <NoteSyncCard noteID={id} />}
```

- [ ] **Step 3: Add sync card styles**

Append to `frontend/src/styles/index.css`:

```css
.sync-card {
  display: grid;
  gap: 0.55rem;
  border: 1px solid var(--color-fs-border);
  border-radius: 10px;
  background: rgba(255, 254, 250, 0.72);
  padding: 0.85rem;
}

.sync-card-header {
  display: flex;
  justify-content: space-between;
  gap: 0.75rem;
}

.sync-card-header span {
  color: var(--color-fs-text-muted);
  font-size: 0.72rem;
  font-weight: 800;
}

.sync-card-header strong {
  color: var(--color-fs-accent);
  font-size: 0.78rem;
}

.sync-card p,
.sync-card em,
.sync-card code {
  color: var(--color-fs-text-muted);
  font-size: 0.76rem;
}

.sync-card code {
  display: block;
  overflow-wrap: anywhere;
  font-family: var(--font-family-mono);
}

.sync-card em {
  color: #9a382c;
  font-style: normal;
}
```

- [ ] **Step 4: Run frontend build**

```powershell
cd frontend
npm run build
```

Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add frontend/src/components/sync/NoteSyncCard.tsx frontend/src/routes/Editor.tsx frontend/src/styles/index.css
git commit -m "feat: show obsidian sync state in editor"
```

---

## Task 8: End-To-End Verification

**Files:**

- Create: `test-obsidian-sync.mjs`

- [ ] **Step 1: Create smoke test**

Create `test-obsidian-sync.mjs`:

```js
import { chromium } from 'playwright'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'

const baseURL = process.env.FLOWSPACE_URL ?? 'http://127.0.0.1:5176/all-note/'
const vault = fs.mkdtempSync(path.join(os.tmpdir(), 'flowspace-vault-'))

const browser = await chromium.launch({ headless: true })
const page = await browser.newPage({ viewport: { width: 1280, height: 900 } })
const errors = []
page.on('console', (msg) => { if (msg.type() === 'error') errors.push(msg.text()) })

await page.goto(baseURL + 'notes', { waitUntil: 'networkidle' })
await page.getByRole('button', { name: '同步' }).click()
await page.getByPlaceholder('D:\\Obsidian\\MyVault').fill(vault)
await page.getByRole('button', { name: '测试路径' }).click()
await page.getByText('路径可用').waitFor({ state: 'visible', timeout: 10000 })
await page.getByRole('button', { name: '保存设置' }).click()
await page.getByText('同步设置已保存').waitFor({ state: 'visible', timeout: 10000 })
await page.getByRole('button', { name: '同步全部' }).click()
await page.getByText('同步完成', { exact: false }).waitFor({ state: 'visible', timeout: 10000 })

const files = fs.readdirSync(path.join(vault, 'FlowSpace Notes')).filter((name) => name.endsWith('.md'))
console.log(JSON.stringify({ files: files.length, errors, vault }, null, 2))
await browser.close()

if (files.length === 0) {
  throw new Error('Expected at least one synced Markdown file')
}
if (errors.length) {
  throw new Error(`Console errors: ${errors.join('\\n')}`)
}
```

- [ ] **Step 2: Run full backend tests**

```powershell
cd backend
go test ./...
```

Expected: PASS.

- [ ] **Step 3: Build frontend**

```powershell
cd frontend
$env:VITE_APP_BASE='/all-note/'
npm run build
```

Expected: PASS.

- [ ] **Step 4: Start services and run smoke test**

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .tailscale\start-flowspace-public.ps1
node test-obsidian-sync.mjs
```

Expected:

```json
{
  "files": 1,
  "errors": [],
  "vault": "..."
}
```

`files` can be greater than 1 if the local database has multiple notes.

- [ ] **Step 5: Verify public route still works**

```powershell
curl.exe -L --connect-timeout 10 --max-time 20 -I https://tylerhu-1.tail5cec87.ts.net/all-note/
curl.exe -L --connect-timeout 10 --max-time 20 https://tylerhu-1.tail5cec87.ts.net/all-note/api/today
```

Expected: first command returns `HTTP/1.1 200 OK`; second command returns JSON with `data`.

- [ ] **Step 6: Commit**

```powershell
git add test-obsidian-sync.mjs
git commit -m "test: verify obsidian sync flow"
```

---

## Self-Review

Spec coverage:

- Obsidian-only first version: covered by all tasks.
- No Notion implementation: preserved.
- One-way FlowSpace-to-Obsidian sync: covered by service and API tasks.
- Vault config and path test: covered by Tasks 1, 4, 6, and 8.
- Single note, folder, and all notes sync: covered by Tasks 4 and 8.
- Sync state display: covered by Tasks 2, 4, 5, and 7.
- Path safety: covered by Task 3.
- Auto-sync after save: covered by Task 7.
- Testing: covered by Tasks 1, 3, and 8.

Plan completeness scan:

- No incomplete follow-up markers or undefined implementation steps are present.
- Each code-changing task names exact files and commands.

Type consistency:

- Backend type names are `SyncTarget`, `SyncState`, `SaveSyncTargetRequest`, `SyncResultItem`, and `SyncBatchResult`.
- Frontend type names mirror backend JSON fields.
- API paths match the approved design.
