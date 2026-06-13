# Notion Bidirectional Sync Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a TDD-driven Notion bidirectional sync feature where Notion content wins conflicts and FlowSpace can import, pull, push, and mark Notion deletions safely.

**Architecture:** Reuse the existing sync target and sync state tables, but generalize them for Notion metadata. Add a Notion gateway layer, a block/Markdown converter, a Notion-first sync decision service, backend routes, and a frontend sync UI with component and Playwright tests.

**Tech Stack:** Go 1.26, Gin, SQLite, React 19, TanStack Query, Vite, Playwright, Vitest, Testing Library.

---

## TDD Rules For This Work

- No production code before a failing test.
- Every task starts with one or more RED tests and an explicit command that must fail for the expected reason.
- Production code is minimal until the listed tests pass.
- After GREEN, run the focused package tests, then the broader package tests listed in each task.
- Commit after each task so regressions are isolated.

## Scope Check

This is one feature with backend and frontend slices. The backend is the source of truth for sync decisions, while frontend work is limited to configuration, status display, and action wiring. The plan keeps each task independently testable and avoids real Notion API calls in automated tests by using fake HTTP servers and a mock Notion provider.

## File Structure

### Backend

- Modify `backend/db/schema.sql`: add Notion-compatible generic columns.
- Modify `backend/internal/repository/db.go`: migrate old databases to the new columns.
- Modify `backend/internal/model/sync.go`: extend sync target/state models and add Notion request/result types.
- Modify `backend/internal/repository/sync.go`: persist `config_json`, `external_id`, and `external_url`.
- Modify `backend/internal/handler/sync.go`: support generic sync target type, Notion routes, and target-specific sync-state queries.
- Modify `backend/internal/router/router.go`: register Notion sync endpoints.
- Create `backend/internal/service/notion_config.go`: parse and validate Notion target configuration.
- Create `backend/internal/service/notion_blocks.go`: convert Notion blocks to Markdown and Markdown to Notion blocks.
- Create `backend/internal/service/notion_client.go`: call Notion HTTP API with pagination, auth headers, rate-limit retry, and typed errors.
- Create `backend/internal/service/notion_gateway.go`: define a gateway interface plus real/mock gateway factories.
- Create `backend/internal/service/notion_bidirectional.go`: implement Notion-first sync decisions.
- Create `backend/internal/service/notion_mock.go`: deterministic mock Notion provider for local and Playwright tests.
- Add tests in `backend/internal/repository/sync_test.go`.
- Add tests in `backend/internal/service/notion_config_test.go`.
- Add tests in `backend/internal/service/notion_blocks_test.go`.
- Add tests in `backend/internal/service/notion_client_test.go`.
- Add tests in `backend/internal/service/notion_bidirectional_test.go`.
- Add tests in `backend/internal/handler/notion_sync_test.go`.

### Frontend

- Modify `frontend/package.json`: add `test`, `test:unit`, and unit test dependencies.
- Create `frontend/vitest.config.ts`: jsdom Vitest config.
- Create `frontend/src/test/setup.ts`: Testing Library matchers.
- Modify `frontend/src/api/sync.ts`: add Notion types and endpoint functions.
- Modify `frontend/src/hooks/useSync.ts`: add Notion query/mutation hooks and target-specific sync-state hooks.
- Rename or replace `frontend/src/components/sync/ObsidianSyncPanel.tsx` with `frontend/src/components/sync/SyncSettingsPanel.tsx`: keep Obsidian, add Notion.
- Create `frontend/src/components/sync/NotionSyncPanel.tsx`: Notion configuration and actions.
- Modify `frontend/src/components/sync/NoteSyncCard.tsx`: show Obsidian and Notion cards or a multi-target state list.
- Modify `frontend/src/routes/Notes.tsx`: open `SyncSettingsPanel`.
- Modify `frontend/src/routes/Editor.tsx`: use target-specific auto-sync and status hooks.
- Add `frontend/src/api/sync.test.ts`.
- Add `frontend/src/components/sync/NotionSyncPanel.test.tsx`.
- Add `frontend/src/components/sync/NoteSyncCard.test.tsx`.
- Add `frontend/tests/e2e/notion-sync.spec.ts`.
- Modify `frontend/playwright.config.ts`: enable backend mock Notion env for E2E.

---

## Task 1: Generalize Sync Persistence For Notion

**Files:**
- Modify: `backend/db/schema.sql`
- Modify: `backend/internal/repository/db.go`
- Modify: `backend/internal/model/sync.go`
- Modify: `backend/internal/repository/sync.go`
- Test: `backend/internal/repository/sync_test.go`

- [ ] **Step 1: Write failing repository tests**

Append these tests to `backend/internal/repository/sync_test.go`:

```go
func TestInitDBAddsNotionSyncColumns(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "flowspace.db")
	createOldSyncStateDB(t, dbPath)
	chdirBackendRoot(t)
	t.Cleanup(func() {
		if DB != nil {
			DB.Close()
			DB = nil
		}
	})

	if err := InitDB(dbPath); err != nil {
		t.Fatalf("init db: %v", err)
	}

	assertTableColumns(t, "sync_targets", []string{"config_json"})
	assertTableColumns(t, "note_sync_state", []string{"external_id", "external_url"})
}

func TestSyncTargetRoundTripIncludesConfigJSON(t *testing.T) {
	openSyncTestDB(t)

	target := &model.SyncTarget{
		Type:       "notion",
		Name:       "Personal Notion",
		VaultPath:  "",
		BaseFolder: "",
		ConfigJSON: `{"data_source_id":"ds-123","title_property":"Name"}`,
		Enabled:    true,
		AutoSync:   false,
	}

	if err := SaveSyncTarget(target); err != nil {
		t.Fatalf("save sync target: %v", err)
	}

	got, err := GetDefaultSyncTarget("notion")
	if err != nil {
		t.Fatalf("get default notion target: %v", err)
	}
	if got.Type != "notion" {
		t.Fatalf("type = %q, want notion", got.Type)
	}
	if got.ConfigJSON != `{"data_source_id":"ds-123","title_property":"Name"}` {
		t.Fatalf("config_json = %q", got.ConfigJSON)
	}
}

func TestSyncStateRoundTripIncludesExternalIDAndURL(t *testing.T) {
	openSyncTestDB(t)
	target := &model.SyncTarget{
		Type:       "notion",
		Name:       "Personal Notion",
		ConfigJSON: `{"data_source_id":"ds-123"}`,
		Enabled:    true,
	}
	if err := SaveSyncTarget(target); err != nil {
		t.Fatalf("save target: %v", err)
	}
	note := insertNoteForTest(t, "Notion State", "Body")
	now := nowUnix()

	state := &model.SyncState{
		NoteID:        note.ID,
		TargetID:      target.ID,
		ExternalPath:  "notion:page-123",
		ExternalID:    "page-123",
		ExternalURL:   "https://www.notion.so/page-123",
		ContentHash:   "flow-hash",
		ExternalHash:  "notion-hash",
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
	if got.ExternalID != "page-123" || got.ExternalURL != "https://www.notion.so/page-123" {
		t.Fatalf("notion metadata was not persisted: %+v", got)
	}
}

func assertTableColumns(t *testing.T, table string, names []string) {
	t.Helper()
	rows, err := DB.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		t.Fatalf("table info %s: %v", table, err)
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
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate columns: %v", err)
	}
	for _, name := range names {
		if !columns[name] {
			t.Fatalf("expected %s.%s to exist", table, name)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify RED**

Run:

```powershell
cd backend
go test ./internal/repository -run "TestInitDBAddsNotionSyncColumns|TestSyncTargetRoundTripIncludesConfigJSON|TestSyncStateRoundTripIncludesExternalIDAndURL" -v
```

Expected: FAIL because `model.SyncTarget.ConfigJSON`, `model.SyncState.ExternalID`, and `model.SyncState.ExternalURL` do not exist.

- [ ] **Step 3: Implement minimal schema/model/repository changes**

Make these changes:

```sql
-- backend/db/schema.sql
ALTER TABLE equivalent change inside CREATE TABLE sync_targets:
  config_json TEXT NOT NULL DEFAULT '{}'

ALTER TABLE equivalent change inside CREATE TABLE note_sync_state:
  external_id TEXT,
  external_url TEXT
```

```go
// backend/internal/model/sync.go
type SyncTarget struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Name       string `json:"name"`
	VaultPath  string `json:"vault_path"`
	BaseFolder string `json:"base_folder"`
	ConfigJSON string `json:"config_json"`
	Enabled    bool   `json:"enabled"`
	AutoSync   bool   `json:"auto_sync"`
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
}

type SyncState struct {
	NoteID        string  `json:"note_id"`
	TargetID      string  `json:"target_id"`
	ExternalPath  string  `json:"external_path"`
	ExternalID    string  `json:"external_id"`
	ExternalURL   string  `json:"external_url"`
	ContentHash   string  `json:"content_hash"`
	ExternalHash  string  `json:"external_hash"`
	ExternalMTime *int64  `json:"external_mtime"`
	LastDirection string  `json:"last_direction"`
	LastSyncedAt  *int64  `json:"last_synced_at"`
	Status        string  `json:"status"`
	ErrorMessage  *string `json:"error_message"`
}
```

Update `backend/internal/repository/db.go` migration statements:

```go
`ALTER TABLE sync_targets ADD COLUMN config_json TEXT NOT NULL DEFAULT '{}'`,
`ALTER TABLE note_sync_state ADD COLUMN external_id TEXT`,
`ALTER TABLE note_sync_state ADD COLUMN external_url TEXT`,
```

Update all `sync_targets` SELECT/INSERT statements in `backend/internal/repository/sync.go` to include `config_json`. Update all `note_sync_state` SELECT/INSERT statements to include `external_id` and `external_url`, using `COALESCE(external_id, '')` and `COALESCE(external_url, '')` when selecting.

- [ ] **Step 4: Run tests to verify GREEN**

Run:

```powershell
cd backend
go test ./internal/repository -run "TestInitDBAddsNotionSyncColumns|TestSyncTargetRoundTripIncludesConfigJSON|TestSyncStateRoundTripIncludesExternalIDAndURL|TestSyncTargetRoundTrip|TestSyncStateRoundTripIncludesExternalMetadata" -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add backend/db/schema.sql backend/internal/repository/db.go backend/internal/model/sync.go backend/internal/repository/sync.go backend/internal/repository/sync_test.go
git commit -m "feat: generalize sync persistence for notion"
```

---

## Task 2: Make Sync Target Requests Generic And Preserve Obsidian Compatibility

**Files:**
- Modify: `backend/internal/model/sync.go`
- Modify: `backend/internal/handler/sync.go`
- Modify: `backend/internal/router/router.go`
- Test: `backend/internal/handler/notion_sync_test.go`

- [ ] **Step 1: Write failing handler tests**

Create `backend/internal/handler/notion_sync_test.go`:

```go
package handler

import (
	"testing"

	"github.com/hujinrun/flowspace/internal/model"
)

func TestSyncTargetFromRequestPreservesNotionTypeAndConfig(t *testing.T) {
	req := &model.SaveSyncTargetRequest{
		Type:       "notion",
		Name:       "Personal Notion",
		ConfigJSON: `{"data_source_id":"ds-123","title_property":"Name"}`,
		Enabled:    true,
		AutoSync:   false,
	}

	target := syncTargetFromRequest(req)

	if target.Type != "notion" {
		t.Fatalf("type = %q, want notion", target.Type)
	}
	if target.ConfigJSON != `{"data_source_id":"ds-123","title_property":"Name"}` {
		t.Fatalf("config_json = %q", target.ConfigJSON)
	}
	if target.VaultPath != "" || target.BaseFolder != "" {
		t.Fatalf("notion target should not require local path fields: %+v", target)
	}
}

func TestSyncTargetFromRequestDefaultsTypeToObsidian(t *testing.T) {
	req := &model.SaveSyncTargetRequest{
		Name:       "Local Vault",
		VaultPath:  "D:\\Vault",
		BaseFolder: "FlowSpace Notes",
		Enabled:    true,
	}

	target := syncTargetFromRequest(req)

	if target.Type != "obsidian" {
		t.Fatalf("type = %q, want obsidian", target.Type)
	}
	if target.ConfigJSON != "{}" {
		t.Fatalf("config_json = %q, want {}", target.ConfigJSON)
	}
}
```

- [ ] **Step 2: Run tests to verify RED**

Run:

```powershell
cd backend
go test ./internal/handler -run "TestSyncTargetFromRequestPreservesNotionTypeAndConfig|TestSyncTargetFromRequestDefaultsTypeToObsidian" -v
```

Expected: FAIL because `SaveSyncTargetRequest.Type` and `SaveSyncTargetRequest.ConfigJSON` do not exist, and `syncTargetFromRequest` hard-codes `obsidian`.

- [ ] **Step 3: Implement minimal request handling**

Modify `backend/internal/model/sync.go`:

```go
type SaveSyncTargetRequest struct {
	Type       string `json:"type"`
	Name       string `json:"name" binding:"required"`
	VaultPath  string `json:"vault_path"`
	BaseFolder string `json:"base_folder"`
	ConfigJSON string `json:"config_json"`
	Enabled    bool   `json:"enabled"`
	AutoSync   bool   `json:"auto_sync"`
}
```

Modify `syncTargetFromRequest` in `backend/internal/handler/sync.go`:

```go
func syncTargetFromRequest(req *model.SaveSyncTargetRequest) *model.SyncTarget {
	syncType := strings.TrimSpace(req.Type)
	if syncType == "" {
		syncType = "obsidian"
	}
	configJSON := strings.TrimSpace(req.ConfigJSON)
	if configJSON == "" {
		configJSON = "{}"
	}
	return &model.SyncTarget{
		Type:       syncType,
		Name:       req.Name,
		VaultPath:  req.VaultPath,
		BaseFolder: req.BaseFolder,
		ConfigJSON: configJSON,
		Enabled:    req.Enabled,
		AutoSync:   req.AutoSync,
	}
}
```

Add the `strings` import to `backend/internal/handler/sync.go`.

- [ ] **Step 4: Run tests to verify GREEN**

Run:

```powershell
cd backend
go test ./internal/handler -run "TestSyncTargetFromRequestPreservesNotionTypeAndConfig|TestSyncTargetFromRequestDefaultsTypeToObsidian" -v
go test ./internal/repository ./internal/handler -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add backend/internal/model/sync.go backend/internal/handler/sync.go backend/internal/handler/notion_sync_test.go
git commit -m "feat: accept generic sync target config"
```

---

## Task 3: Parse And Validate Notion Target Configuration

**Files:**
- Create: `backend/internal/service/notion_config.go`
- Test: `backend/internal/service/notion_config_test.go`

- [ ] **Step 1: Write failing config tests**

Create `backend/internal/service/notion_config_test.go`:

```go
package service

import (
	"strings"
	"testing"

	"github.com/hujinrun/flowspace/internal/model"
)

func TestParseNotionTargetConfigUsesDefaults(t *testing.T) {
	target := &model.SyncTarget{
		Type:       "notion",
		Name:       "Personal Notion",
		ConfigJSON: `{"data_source_id":"ds-123"}`,
		Enabled:    true,
	}

	config, err := parseNotionTargetConfig(target)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if config.DataSourceID != "ds-123" {
		t.Fatalf("data source id = %q", config.DataSourceID)
	}
	if config.TokenEnv != "FLOWSPACE_NOTION_TOKEN" {
		t.Fatalf("token env = %q", config.TokenEnv)
	}
	if config.TitleProperty != "Name" {
		t.Fatalf("title property = %q", config.TitleProperty)
	}
	if config.FlowSpaceIDProperty != "FlowSpace ID" {
		t.Fatalf("flowspace id property = %q", config.FlowSpaceIDProperty)
	}
	if config.TagsProperty != "Tags" {
		t.Fatalf("tags property = %q", config.TagsProperty)
	}
}

func TestParseNotionTargetConfigRejectsMissingDataSource(t *testing.T) {
	target := &model.SyncTarget{
		Type:       "notion",
		Name:       "Personal Notion",
		ConfigJSON: `{}`,
		Enabled:    true,
	}

	_, err := parseNotionTargetConfig(target)
	if err == nil || !strings.Contains(err.Error(), "notion data source id is required") {
		t.Fatalf("expected missing data source error, got %v", err)
	}
}

func TestParseNotionTargetConfigRejectsNonNotionTarget(t *testing.T) {
	target := &model.SyncTarget{
		Type:       "obsidian",
		Name:       "Local Vault",
		ConfigJSON: `{"data_source_id":"ds-123"}`,
		Enabled:    true,
	}

	_, err := parseNotionTargetConfig(target)
	if err == nil || !strings.Contains(err.Error(), "expected notion sync target") {
		t.Fatalf("expected wrong target error, got %v", err)
	}
}

func TestLoadNotionTokenFromConfiguredEnv(t *testing.T) {
	t.Setenv("FLOWSPACE_TEST_NOTION_TOKEN", "secret-token")

	config := notionTargetConfig{TokenEnv: "FLOWSPACE_TEST_NOTION_TOKEN"}
	token, err := notionToken(config)
	if err != nil {
		t.Fatalf("load token: %v", err)
	}
	if token != "secret-token" {
		t.Fatalf("token = %q", token)
	}
}

func TestLoadNotionTokenRejectsEmptyEnv(t *testing.T) {
	config := notionTargetConfig{TokenEnv: "FLOWSPACE_EMPTY_NOTION_TOKEN"}
	_, err := notionToken(config)
	if err == nil || !strings.Contains(err.Error(), "FLOWSPACE_EMPTY_NOTION_TOKEN is required") {
		t.Fatalf("expected missing token error, got %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify RED**

Run:

```powershell
cd backend
go test ./internal/service -run "TestParseNotionTargetConfig|TestLoadNotionToken" -v
```

Expected: FAIL because `parseNotionTargetConfig`, `notionTargetConfig`, and `notionToken` do not exist.

- [ ] **Step 3: Implement minimal config parser**

Create `backend/internal/service/notion_config.go`:

```go
package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/hujinrun/flowspace/internal/model"
)

type notionTargetConfig struct {
	DataSourceID              string `json:"data_source_id"`
	TokenEnv                  string `json:"token_env"`
	TitleProperty             string `json:"title_property"`
	FlowSpaceIDProperty       string `json:"flowspace_id_property"`
	FolderProperty            string `json:"folder_property"`
	TagsProperty              string `json:"tags_property"`
	FlowSpaceUpdatedProperty  string `json:"flowspace_updated_property"`
}

func parseNotionTargetConfig(target *model.SyncTarget) (notionTargetConfig, error) {
	if target == nil {
		return notionTargetConfig{}, errors.New("sync target is required")
	}
	if target.Type != "notion" {
		return notionTargetConfig{}, fmt.Errorf("expected notion sync target, got %q", target.Type)
	}
	if !target.Enabled {
		return notionTargetConfig{}, errors.New("notion sync target is disabled")
	}

	var config notionTargetConfig
	raw := strings.TrimSpace(target.ConfigJSON)
	if raw == "" {
		raw = "{}"
	}
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		return notionTargetConfig{}, fmt.Errorf("parse notion sync target config: %w", err)
	}

	config.DataSourceID = strings.TrimSpace(config.DataSourceID)
	config.TokenEnv = defaultString(config.TokenEnv, "FLOWSPACE_NOTION_TOKEN")
	config.TitleProperty = defaultString(config.TitleProperty, "Name")
	config.FlowSpaceIDProperty = defaultString(config.FlowSpaceIDProperty, "FlowSpace ID")
	config.FolderProperty = defaultString(config.FolderProperty, "Folder")
	config.TagsProperty = defaultString(config.TagsProperty, "Tags")
	config.FlowSpaceUpdatedProperty = defaultString(config.FlowSpaceUpdatedProperty, "FlowSpace Updated At")

	if config.DataSourceID == "" {
		return notionTargetConfig{}, errors.New("notion data source id is required")
	}
	return config, nil
}

func notionToken(config notionTargetConfig) (string, error) {
	envName := strings.TrimSpace(config.TokenEnv)
	if envName == "" {
		envName = "FLOWSPACE_NOTION_TOKEN"
	}
	token := strings.TrimSpace(os.Getenv(envName))
	if token == "" {
		return "", fmt.Errorf("%s is required", envName)
	}
	return token, nil
}

func defaultString(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}
```

- [ ] **Step 4: Run tests to verify GREEN**

Run:

```powershell
cd backend
go test ./internal/service -run "TestParseNotionTargetConfig|TestLoadNotionToken" -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add backend/internal/service/notion_config.go backend/internal/service/notion_config_test.go
git commit -m "feat: parse notion sync target config"
```

---

## Task 4: Convert Notion Blocks And Markdown

**Files:**
- Create: `backend/internal/service/notion_blocks.go`
- Test: `backend/internal/service/notion_blocks_test.go`

- [ ] **Step 1: Write failing block conversion tests**

Create `backend/internal/service/notion_blocks_test.go`:

```go
package service

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNotionBlocksToMarkdownCoversSupportedBlocks(t *testing.T) {
	raw := []byte(`[
		{"id":"h1","type":"heading_1","heading_1":{"rich_text":[{"plain_text":"Plan","annotations":{},"href":null}]},"has_children":false},
		{"id":"p1","type":"paragraph","paragraph":{"rich_text":[{"plain_text":"Read ","annotations":{},"href":null},{"plain_text":"docs","annotations":{"bold":true},"href":"https://example.com"}]},"has_children":false},
		{"id":"b1","type":"bulleted_list_item","bulleted_list_item":{"rich_text":[{"plain_text":"bullet","annotations":{},"href":null}]},"has_children":false},
		{"id":"n1","type":"numbered_list_item","numbered_list_item":{"rich_text":[{"plain_text":"numbered","annotations":{},"href":null}]},"has_children":false},
		{"id":"t1","type":"to_do","to_do":{"checked":true,"rich_text":[{"plain_text":"done","annotations":{},"href":null}]},"has_children":false},
		{"id":"q1","type":"quote","quote":{"rich_text":[{"plain_text":"quote","annotations":{},"href":null}]},"has_children":false},
		{"id":"c1","type":"code","code":{"language":"go","rich_text":[{"plain_text":"fmt.Println(\"hi\")","annotations":{},"href":null}]},"has_children":false},
		{"id":"d1","type":"divider","divider":{},"has_children":false}
	]`)
	var blocks []notionBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		t.Fatalf("decode blocks: %v", err)
	}

	converted := notionBlocksToMarkdown(blocks)

	want := strings.Join([]string{
		"# Plan",
		"",
		"Read [**docs**](https://example.com)",
		"",
		"- bullet",
		"1. numbered",
		"- [x] done",
		"> quote",
		"",
		"```go",
		"fmt.Println(\"hi\")",
		"```",
		"",
		"---",
		"",
	}, "\n")
	if converted.Markdown != want {
		t.Fatalf("markdown mismatch\nwant:\n%q\ngot:\n%q", want, converted.Markdown)
	}
	if len(converted.UnsupportedTypes) != 0 {
		t.Fatalf("unexpected unsupported types: %#v", converted.UnsupportedTypes)
	}
}

func TestNotionBlocksToMarkdownMarksUnsupportedBlocks(t *testing.T) {
	blocks := []notionBlock{
		{ID: "table-1", Type: "table"},
	}

	converted := notionBlocksToMarkdown(blocks)

	if !strings.Contains(converted.Markdown, "[Unsupported Notion block: table]") {
		t.Fatalf("unsupported marker missing from markdown: %q", converted.Markdown)
	}
	if len(converted.UnsupportedTypes) != 1 || converted.UnsupportedTypes[0] != "table" {
		t.Fatalf("unsupported types = %#v", converted.UnsupportedTypes)
	}
}

func TestMarkdownToNotionBlocksCoversSupportedMarkdown(t *testing.T) {
	markdown := strings.Join([]string{
		"# Title",
		"",
		"Paragraph",
		"",
		"- bullet",
		"1. first",
		"- [ ] open",
		"- [x] done",
		"> quote",
		"",
		"```go",
		"fmt.Println(\"hi\")",
		"```",
		"",
		"---",
	}, "\n")

	blocks := markdownToNotionBlocks(markdown)

	types := make([]string, 0, len(blocks))
	for _, block := range blocks {
		types = append(types, block.Type)
	}
	want := []string{"heading_1", "paragraph", "bulleted_list_item", "numbered_list_item", "to_do", "to_do", "quote", "code", "divider"}
	if strings.Join(types, ",") != strings.Join(want, ",") {
		t.Fatalf("types = %#v, want %#v", types, want)
	}
}

func TestNotionMarkdownHashIsStable(t *testing.T) {
	left := notionMarkdownHash("Paragraph\n\n")
	right := notionMarkdownHash("Paragraph\n\n")
	if left == "" || left != right {
		t.Fatalf("hash not stable: %q %q", left, right)
	}
}
```

- [ ] **Step 2: Run tests to verify RED**

Run:

```powershell
cd backend
go test ./internal/service -run "TestNotionBlocksToMarkdown|TestMarkdownToNotionBlocks|TestNotionMarkdownHash" -v
```

Expected: FAIL because `notionBlock`, `notionBlocksToMarkdown`, `markdownToNotionBlocks`, and `notionMarkdownHash` do not exist.

- [ ] **Step 3: Implement minimal converter**

Create `backend/internal/service/notion_blocks.go` with these public internal contracts:

```go
type notionBlock struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	HasChildren bool `json:"has_children,omitempty"`
	Paragraph notionTextBlock `json:"paragraph,omitempty"`
	Heading1 notionTextBlock `json:"heading_1,omitempty"`
	Heading2 notionTextBlock `json:"heading_2,omitempty"`
	Heading3 notionTextBlock `json:"heading_3,omitempty"`
	BulletedListItem notionTextBlock `json:"bulleted_list_item,omitempty"`
	NumberedListItem notionTextBlock `json:"numbered_list_item,omitempty"`
	ToDo notionToDoBlock `json:"to_do,omitempty"`
	Quote notionTextBlock `json:"quote,omitempty"`
	Code notionCodeBlock `json:"code,omitempty"`
	Divider map[string]any `json:"divider,omitempty"`
}

type notionRichText struct {
	PlainText string `json:"plain_text"`
	Href *string `json:"href"`
	Annotations notionAnnotations `json:"annotations"`
}

type notionAnnotations struct {
	Bold bool `json:"bold"`
	Italic bool `json:"italic"`
	Strikethrough bool `json:"strikethrough"`
	Code bool `json:"code"`
}

type notionTextBlock struct {
	RichText []notionRichText `json:"rich_text"`
}

type notionToDoBlock struct {
	RichText []notionRichText `json:"rich_text"`
	Checked bool `json:"checked"`
}

type notionCodeBlock struct {
	RichText []notionRichText `json:"rich_text"`
	Language string `json:"language"`
}

type notionMarkdownConversion struct {
	Markdown string
	UnsupportedTypes []string
}
```

Implement:

- `notionBlocksToMarkdown(blocks []notionBlock) notionMarkdownConversion`
- `markdownToNotionBlocks(markdown string) []notionBlock`
- `notionMarkdownHash(markdown string) string`

Keep parsing intentionally narrow: headings, paragraph, bullet, numbered, to-do, quote, code fences, divider. Use deterministic formatting so hash comparisons remain stable.

- [ ] **Step 4: Run tests to verify GREEN**

Run:

```powershell
cd backend
go test ./internal/service -run "TestNotionBlocksToMarkdown|TestMarkdownToNotionBlocks|TestNotionMarkdownHash" -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add backend/internal/service/notion_blocks.go backend/internal/service/notion_blocks_test.go
git commit -m "feat: convert notion blocks to markdown"
```

---

## Task 5: Add Notion HTTP Client With Pagination And Retry

**Files:**
- Create: `backend/internal/service/notion_client.go`
- Create: `backend/internal/service/notion_gateway.go`
- Test: `backend/internal/service/notion_client_test.go`

- [ ] **Step 1: Write failing HTTP client tests**

Create `backend/internal/service/notion_client_test.go`:

```go
package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNotionClientQueryDataSourceSendsHeadersAndPaginates(t *testing.T) {
	var authHeader string
	var notionVersion string
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		authHeader = r.Header.Get("Authorization")
		notionVersion = r.Header.Get("Notion-Version")
		if r.URL.Path != "/v1/data_sources/ds-123/query" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		if requests == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"results": []map[string]any{{"id": "page-1", "url": "https://notion.so/page-1", "last_edited_time": "2026-06-13T01:00:00.000Z"}},
				"has_more": true,
				"next_cursor": "cursor-2",
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{{"id": "page-2", "url": "https://notion.so/page-2", "last_edited_time": "2026-06-13T02:00:00.000Z"}},
			"has_more": false,
		})
	}))
	defer server.Close()

	client := newNotionHTTPClient("secret-token", server.URL)
	pages, err := client.QueryDataSource("ds-123")
	if err != nil {
		t.Fatalf("query data source: %v", err)
	}
	if authHeader != "Bearer secret-token" {
		t.Fatalf("authorization header = %q", authHeader)
	}
	if notionVersion == "" {
		t.Fatal("expected Notion-Version header")
	}
	if len(pages) != 2 || pages[0].ID != "page-1" || pages[1].ID != "page-2" {
		t.Fatalf("pages = %#v", pages)
	}
}

func TestNotionClientRetriesRateLimitedRequests(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		if requests == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]any{"message": "rate limited"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{}, "has_more": false})
	}))
	defer server.Close()

	client := newNotionHTTPClient("secret-token", server.URL)
	client.retrySleep = func(time.Duration) {}

	if _, err := client.QueryDataSource("ds-123"); err != nil {
		t.Fatalf("query after retry: %v", err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}

func TestNotionClientReturnsUsefulHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{"message": "integration lacks access"})
	}))
	defer server.Close()

	client := newNotionHTTPClient("secret-token", server.URL)
	_, err := client.QueryDataSource("ds-123")
	if err == nil || !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "integration lacks access") {
		t.Fatalf("expected useful 403 error, got %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify RED**

Run:

```powershell
cd backend
go test ./internal/service -run "TestNotionClient" -v
```

Expected: FAIL because `newNotionHTTPClient` and client methods do not exist.

- [ ] **Step 3: Implement minimal HTTP client and gateway contract**

Create `backend/internal/service/notion_gateway.go`:

```go
type notionGateway interface {
	TestDataSource(config notionTargetConfig) error
	QueryDataSource(dataSourceID string) ([]notionPage, error)
	RetrievePageBlocks(pageID string) ([]notionBlock, error)
	CreatePage(config notionTargetConfig, note *model.Note, blocks []notionBlock) (notionPage, error)
	UpdatePage(config notionTargetConfig, pageID string, note *model.Note, blocks []notionBlock) (notionPage, error)
	RestorePage(pageID string) error
}
```

Create `backend/internal/service/notion_client.go` implementing:

- `newNotionHTTPClient(token string, baseURL string) *notionHTTPClient`
- `QueryDataSource(dataSourceID string) ([]notionPage, error)`
- `doJSON(method, path string, body any, out any) error`
- retry on 429 and 529 with `Retry-After` support

Set the Notion version header to a fixed value such as `2022-06-28` until the codebase intentionally upgrades API versions.

- [ ] **Step 4: Run tests to verify GREEN**

Run:

```powershell
cd backend
go test ./internal/service -run "TestNotionClient" -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add backend/internal/service/notion_client.go backend/internal/service/notion_gateway.go backend/internal/service/notion_client_test.go
git commit -m "feat: add notion http client"
```

---

## Task 6: Implement Notion-First Sync Decisions With A Fake Gateway

**Files:**
- Create: `backend/internal/service/notion_bidirectional.go`
- Create: `backend/internal/service/notion_mock.go`
- Modify: `backend/internal/model/sync.go`
- Test: `backend/internal/service/notion_bidirectional_test.go`

- [ ] **Step 1: Write failing sync service tests**

Create `backend/internal/service/notion_bidirectional_test.go`:

```go
package service

import (
	"database/sql"
	"os"
	"testing"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/repository"
	_ "modernc.org/sqlite"
)

type fakeNotionGateway struct {
	pages []notionRemoteNote
	created []string
	updated []string
	restored []string
}

func (fake *fakeNotionGateway) TestDataSource(config notionTargetConfig) error { return nil }
func (fake *fakeNotionGateway) QueryRemoteNotes(config notionTargetConfig) ([]notionRemoteNote, error) {
	return fake.pages, nil
}
func (fake *fakeNotionGateway) CreateRemoteNote(config notionTargetConfig, note *model.Note) (notionRemoteNote, error) {
	fake.created = append(fake.created, note.ID)
	return notionRemoteNote{
		PageID: "created-" + note.ID,
		URL: "https://www.notion.so/created-" + note.ID,
		Title: note.Title,
		Markdown: note.Body,
		Hash: notionMarkdownHash(note.Body),
		LastEditedAt: 1900000000,
		FlowSpaceID: note.ID,
	}, nil
}
func (fake *fakeNotionGateway) UpdateRemoteNote(config notionTargetConfig, pageID string, note *model.Note) (notionRemoteNote, error) {
	fake.updated = append(fake.updated, pageID)
	return notionRemoteNote{
		PageID: pageID,
		URL: "https://www.notion.so/" + pageID,
		Title: note.Title,
		Markdown: note.Body,
		Hash: notionMarkdownHash(note.Body),
		LastEditedAt: 1900000001,
		FlowSpaceID: note.ID,
	}, nil
}
func (fake *fakeNotionGateway) RestoreRemoteNote(config notionTargetConfig, note *model.Note, previous notionSyncStateSnapshot) (notionRemoteNote, error) {
	fake.restored = append(fake.restored, note.ID)
	return fake.CreateRemoteNote(config, note)
}

func TestSyncNotionImportsNewRemotePage(t *testing.T) {
	openServiceSyncTestDB(t)
	target := saveNotionTargetForTest(t)
	gateway := &fakeNotionGateway{
		pages: []notionRemoteNote{{
			PageID: "page-1",
			URL: "https://www.notion.so/page-1",
			Title: "Remote Only",
			Markdown: "Remote body\n",
			Hash: notionMarkdownHash("Remote body\n"),
			LastEditedAt: 1800000000,
		}},
	}

	result := NewNotionSyncService(gateway).SyncBidirectional(target)

	if result.Imported != 1 || result.Failed != 0 {
		t.Fatalf("result = %+v", result)
	}
	notes, err := repository.ListAllNotes()
	if err != nil {
		t.Fatalf("list notes: %v", err)
	}
	if len(notes) != 1 || notes[0].Title != "Remote Only" || notes[0].Body != "Remote body\n" {
		t.Fatalf("notes = %+v", notes)
	}
}

func TestSyncNotionPullsRemoteChangeOverLocalChange(t *testing.T) {
	openServiceSyncTestDB(t)
	target := saveNotionTargetForTest(t)
	note := insertServiceNoteForTest(t, "Local Title", "Local changed\n")
	lastSync := int64(1700000000)
	if err := repository.UpsertSyncState(&model.SyncState{
		NoteID: note.ID,
		TargetID: target.ID,
		ExternalPath: "notion:page-1",
		ExternalID: "page-1",
		ExternalURL: "https://www.notion.so/page-1",
		ContentHash: notionMarkdownHash("Old local\n"),
		ExternalHash: notionMarkdownHash("Old remote\n"),
		ExternalMTime: &lastSync,
		LastSyncedAt: &lastSync,
		LastDirection: "push",
		Status: "synced",
	}); err != nil {
		t.Fatalf("upsert state: %v", err)
	}
	gateway := &fakeNotionGateway{
		pages: []notionRemoteNote{{
			PageID: "page-1",
			URL: "https://www.notion.so/page-1",
			Title: "Remote Wins",
			Markdown: "Remote changed\n",
			Hash: notionMarkdownHash("Remote changed\n"),
			LastEditedAt: 1800000000,
			FlowSpaceID: note.ID,
		}},
	}

	result := NewNotionSyncService(gateway).SyncBidirectional(target)

	if result.ConflictPulled != 1 {
		t.Fatalf("result = %+v", result)
	}
	got, err := repository.GetNoteByID(note.ID)
	if err != nil {
		t.Fatalf("get note: %v", err)
	}
	if got.Title != "Remote Wins" || got.Body != "Remote changed\n" {
		t.Fatalf("note = %+v", got)
	}
}

func TestSyncNotionPushesLocalChangeWhenRemoteUnchanged(t *testing.T) {
	openServiceSyncTestDB(t)
	target := saveNotionTargetForTest(t)
	note := insertServiceNoteForTest(t, "Local Push", "Local changed\n")
	lastSync := int64(1700000000)
	remoteHash := notionMarkdownHash("Old body\n")
	if err := repository.UpsertSyncState(&model.SyncState{
		NoteID: note.ID,
		TargetID: target.ID,
		ExternalPath: "notion:page-1",
		ExternalID: "page-1",
		ExternalURL: "https://www.notion.so/page-1",
		ContentHash: notionMarkdownHash("Old body\n"),
		ExternalHash: remoteHash,
		ExternalMTime: &lastSync,
		LastSyncedAt: &lastSync,
		LastDirection: "pull",
		Status: "synced",
	}); err != nil {
		t.Fatalf("upsert state: %v", err)
	}
	gateway := &fakeNotionGateway{
		pages: []notionRemoteNote{{
			PageID: "page-1",
			URL: "https://www.notion.so/page-1",
			Title: "Local Push",
			Markdown: "Old body\n",
			Hash: remoteHash,
			LastEditedAt: lastSync,
			FlowSpaceID: note.ID,
		}},
	}

	result := NewNotionSyncService(gateway).SyncBidirectional(target)

	if result.Pushed != 1 || len(gateway.updated) != 1 || gateway.updated[0] != "page-1" {
		t.Fatalf("result = %+v updated = %#v", result, gateway.updated)
	}
}

func TestSyncNotionMarksMissingRemoteAsExternalDeleted(t *testing.T) {
	openServiceSyncTestDB(t)
	target := saveNotionTargetForTest(t)
	note := insertServiceNoteForTest(t, "Deleted Remote", "Body\n")
	lastSync := int64(1700000000)
	if err := repository.UpsertSyncState(&model.SyncState{
		NoteID: note.ID,
		TargetID: target.ID,
		ExternalPath: "notion:page-deleted",
		ExternalID: "page-deleted",
		ExternalURL: "https://www.notion.so/page-deleted",
		ContentHash: notionMarkdownHash("Body\n"),
		ExternalHash: notionMarkdownHash("Body\n"),
		ExternalMTime: &lastSync,
		LastSyncedAt: &lastSync,
		LastDirection: "push",
		Status: "synced",
	}); err != nil {
		t.Fatalf("upsert state: %v", err)
	}

	result := NewNotionSyncService(&fakeNotionGateway{}).SyncBidirectional(target)

	if result.ExternalDeleted != 1 {
		t.Fatalf("result = %+v", result)
	}
	state, err := repository.GetSyncState(note.ID, target.ID)
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if state.Status != "external_deleted" || state.LastDirection != "delete_detected" {
		t.Fatalf("state = %+v", state)
	}
}
```

Add helper functions in the same file:

```go
func openServiceSyncTestDB(t *testing.T) {
	t.Helper()
	repositoryTestDB(t)
}

func repositoryTestDB(t *testing.T) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)

	schema, err := os.ReadFile("../../db/schema.sql")
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	if _, err := db.Exec(string(schema)); err != nil {
		t.Fatalf("exec schema: %v", err)
	}

	repository.DB = db
	t.Cleanup(func() {
		repository.DB = nil
		if err := db.Close(); err != nil {
			t.Fatalf("close db: %v", err)
		}
	})
}

func saveNotionTargetForTest(t *testing.T) model.SyncTarget {
	t.Helper()
	target := &model.SyncTarget{
		Type: "notion",
		Name: "Personal Notion",
		ConfigJSON: `{"data_source_id":"ds-123"}`,
		Enabled: true,
	}
	if err := repository.SaveSyncTarget(target); err != nil {
		t.Fatalf("save target: %v", err)
	}
	return *target
}

func insertServiceNoteForTest(t *testing.T, title, body string) model.Note {
	t.Helper()
	note := &model.Note{Title: title, Body: body, FolderID: "__uncategorized", Tags: "[]"}
	if err := repository.CreateNote(note); err != nil {
		t.Fatalf("create note: %v", err)
	}
	return *note
}
```

- [ ] **Step 2: Run tests to verify RED**

Run:

```powershell
cd backend
go test ./internal/service -run "TestSyncNotion" -v
```

Expected: FAIL because `NewNotionSyncService`, `notionRemoteNote`, result fields, and service logic do not exist.

- [ ] **Step 3: Implement minimal service contracts and sync logic**

In `backend/internal/model/sync.go`, add:

```go
type NotionBidirectionalResult struct {
	Pushed         int              `json:"pushed"`
	Pulled         int              `json:"pulled"`
	ConflictPulled int             `json:"conflict_pulled"`
	Imported       int             `json:"imported"`
	ExternalDeleted int            `json:"external_deleted"`
	Unsupported    int             `json:"unsupported"`
	Failed         int             `json:"failed"`
	Items          []SyncResultItem `json:"items"`
}
```

Extend `SyncResultItem` with optional Notion fields:

```go
ExternalID  string `json:"external_id,omitempty"`
ExternalURL string `json:"external_url,omitempty"`
```

Create `backend/internal/service/notion_bidirectional.go` with:

- `type notionRemoteNote`
- `type notionSyncGateway`
- `type NotionSyncService struct`
- `func NewNotionSyncService(gateway notionSyncGateway) *NotionSyncService`
- `func SyncNotionBidirectional() model.NotionBidirectionalResult`
- `func (svc *NotionSyncService) SyncBidirectional(target model.SyncTarget) model.NotionBidirectionalResult`

Implement ordering:

1. Index local notes by id.
2. Index states by note id and external id.
3. Process remote notes first.
4. Pull remote if remote hash differs from stored external hash.
5. Count `ConflictPulled` when local hash also differs from stored content hash.
6. Import unmatched remote notes.
7. Mark synced states missing from remote list as `external_deleted`.
8. Push local notes only if not already handled, not external deleted, and local hash differs.

Create `backend/internal/service/notion_mock.go`:

```go
func notionGatewayFromEnv(token string) notionSyncGateway {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("NOTION_PROVIDER")), "mock") {
		return newMockNotionGateway()
	}
	return newRealNotionGateway(token)
}
```

The mock gateway returns one deterministic page:

```go
Title: "Mock Notion Note"
Markdown: "Imported from mock Notion.\n"
PageID: "mock-page-1"
URL: "https://www.notion.so/mock-page-1"
```

- [ ] **Step 4: Run tests to verify GREEN**

Run:

```powershell
cd backend
go test ./internal/service -run "TestSyncNotion" -v
go test ./internal/service -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add backend/internal/model/sync.go backend/internal/service/notion_bidirectional.go backend/internal/service/notion_mock.go backend/internal/service/notion_bidirectional_test.go
git commit -m "feat: sync notion notes with notion priority"
```

---

## Task 7: Add Notion HTTP Handlers And Routes

**Files:**
- Modify: `backend/internal/handler/sync.go`
- Modify: `backend/internal/router/router.go`
- Test: `backend/internal/handler/notion_sync_test.go`

- [ ] **Step 1: Write failing handler tests**

Append to `backend/internal/handler/notion_sync_test.go`:

```go
func TestNotionRoutesAreRegistered(t *testing.T) {
	routes := []string{
		"POST /api/sync/notion/test",
		"POST /api/sync/notion/bidirectional",
		"POST /api/sync/notion/notes/:id",
		"GET /api/sync/notion/deletions",
		"POST /api/sync/notion/deletions/:note_id/confirm",
		"POST /api/sync/notion/deletions/:note_id/restore",
	}

	router := router.Setup()
	registered := map[string]bool{}
	for _, route := range router.Routes() {
		registered[route.Method+" "+route.Path] = true
	}
	for _, route := range routes {
		if !registered[route] {
			t.Fatalf("route %s is not registered", route)
		}
	}
}
```

Add the router import:

```go
import "github.com/hujinrun/flowspace/internal/router"
```

- [ ] **Step 2: Run tests to verify RED**

Run:

```powershell
cd backend
go test ./internal/handler -run "TestNotionRoutesAreRegistered" -v
```

Expected: FAIL because Notion routes do not exist.

- [ ] **Step 3: Implement handlers and routes**

Add handler functions in `backend/internal/handler/sync.go`:

```go
func TestNotionTarget(c *gin.Context) {
	var req model.SaveSyncTargetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid sync target")
		return
	}
	target := syncTargetFromRequest(&req)
	target.Type = "notion"
	target.Enabled = true
	if err := service.TestNotionTarget(target); err != nil {
		badRequest(c, err.Error())
		return
	}
	success(c, gin.H{"ok": true})
}

func SyncNotionBidirectional(c *gin.Context) {
	result := service.SyncNotionBidirectional()
	success(c, gin.H{"result": result})
}
```

Add note sync, deletion list, confirm, and restore handlers following the existing Obsidian deletion pattern. Reuse `repository.ListExternalDeletedSyncStates(target.ID)` through service wrappers.

Register routes in `backend/internal/router/router.go`:

```go
api.POST("/sync/notion/test", handler.TestNotionTarget)
api.POST("/sync/notion/bidirectional", handler.SyncNotionBidirectional)
api.POST("/sync/notion/notes/:id", handler.SyncNotionNote)
api.GET("/sync/notion/deletions", handler.ListNotionDeletions)
api.POST("/sync/notion/deletions/:note_id/confirm", handler.ConfirmNotionDeletion)
api.POST("/sync/notion/deletions/:note_id/restore", handler.RestoreNotionDeletion)
```

- [ ] **Step 4: Run tests to verify GREEN**

Run:

```powershell
cd backend
go test ./internal/handler -run "TestNotionRoutesAreRegistered|TestSyncTargetFromRequest" -v
go test ./internal/handler ./internal/router -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add backend/internal/handler/sync.go backend/internal/router/router.go backend/internal/handler/notion_sync_test.go
git commit -m "feat: expose notion sync endpoints"
```

---

## Task 8: Add Frontend Unit Test Infrastructure And Sync API Tests

**Files:**
- Modify: `frontend/package.json`
- Create: `frontend/vitest.config.ts`
- Create: `frontend/src/test/setup.ts`
- Modify: `frontend/src/api/sync.ts`
- Test: `frontend/src/api/sync.test.ts`

- [ ] **Step 1: Add test dependencies**

Run:

```powershell
cd frontend
npm install -D vitest @testing-library/react @testing-library/jest-dom @testing-library/user-event jsdom
```

Then add scripts to `frontend/package.json`:

```json
{
  "scripts": {
    "test": "vitest run",
    "test:unit": "vitest run",
    "test:e2e": "playwright test"
  }
}
```

- [ ] **Step 2: Create Vitest config and setup**

Create `frontend/vitest.config.ts`:

```ts
import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  test: {
    environment: 'jsdom',
    setupFiles: './src/test/setup.ts',
    globals: true,
  },
})
```

Create `frontend/src/test/setup.ts`:

```ts
import '@testing-library/jest-dom/vitest'
```

- [ ] **Step 3: Write failing API tests**

Create `frontend/src/api/sync.test.ts`:

```ts
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  confirmNotionDeletion,
  getNoteSyncState,
  getNotionDeletions,
  restoreNotionDeletion,
  saveSyncTarget,
  syncNotionBidirectional,
  testNotionTarget,
} from './sync'

describe('notion sync api', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => {
      return new Response(JSON.stringify({ data: { target: { id: 'target-1' }, result: { imported: 1 }, items: [], state: null } }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    }))
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('saves notion target without sending a token', async () => {
    await saveSyncTarget({
      type: 'notion',
      name: 'Personal Notion',
      vault_path: '',
      base_folder: '',
      config_json: JSON.stringify({ data_source_id: 'ds-123', token_env: 'FLOWSPACE_NOTION_TOKEN' }),
      enabled: true,
      auto_sync: false,
    })

    const fetchMock = vi.mocked(fetch)
    const [, init] = fetchMock.mock.calls[0]
    expect(String(fetchMock.mock.calls[0][0])).toContain('/api/sync/targets')
    expect(init?.method).toBe('POST')
    expect(JSON.parse(String(init?.body))).toEqual({
      type: 'notion',
      name: 'Personal Notion',
      vault_path: '',
      base_folder: '',
      config_json: JSON.stringify({ data_source_id: 'ds-123', token_env: 'FLOWSPACE_NOTION_TOKEN' }),
      enabled: true,
      auto_sync: false,
    })
    expect(String(init?.body)).not.toContain('secret')
  })

  it('calls notion endpoints with encoded ids', async () => {
    await testNotionTarget({
      type: 'notion',
      name: 'Personal Notion',
      vault_path: '',
      base_folder: '',
      config_json: JSON.stringify({ data_source_id: 'ds-123' }),
      enabled: true,
      auto_sync: false,
    })
    await syncNotionBidirectional()
    await getNotionDeletions()
    await confirmNotionDeletion('note/1')
    await restoreNotionDeletion('note/1')
    await getNoteSyncState('note/1', 'notion')

    const paths = vi.mocked(fetch).mock.calls.map(([input]) => String(input))
    expect(paths.some((path) => path.includes('/api/sync/notion/test'))).toBe(true)
    expect(paths.some((path) => path.includes('/api/sync/notion/bidirectional'))).toBe(true)
    expect(paths.some((path) => path.includes('/api/sync/notion/deletions'))).toBe(true)
    expect(paths.some((path) => path.includes('/api/sync/notion/deletions/note%2F1/confirm'))).toBe(true)
    expect(paths.some((path) => path.includes('/api/sync/notion/deletions/note%2F1/restore'))).toBe(true)
    expect(paths.some((path) => path.includes('/api/notes/note%2F1/sync-state?target=notion'))).toBe(true)
  })
})
```

- [ ] **Step 4: Run tests to verify RED**

Run:

```powershell
cd frontend
npm run test:unit -- src/api/sync.test.ts
```

Expected: FAIL because Notion API functions and `type: 'notion'` are not defined.

- [ ] **Step 5: Implement minimal frontend API types and functions**

Modify `frontend/src/api/sync.ts`:

```ts
export type SyncTargetType = 'obsidian' | 'notion'

export interface SyncTarget {
  id: string
  type: SyncTargetType
  name: string
  vault_path: string
  base_folder: string
  config_json: string
  enabled: boolean
  auto_sync: boolean
  created_at: number
  updated_at: number
}

export interface SaveSyncTargetInput {
  id?: string
  type?: SyncTargetType
  name: string
  vault_path: string
  base_folder: string
  config_json?: string
  enabled: boolean
  auto_sync: boolean
}
```

Add:

```ts
export interface NotionBidirectionalResult {
  pushed: number
  pulled: number
  conflict_pulled: number
  imported: number
  external_deleted: number
  unsupported: number
  failed: number
  items: SyncResultItem[]
}

export async function testNotionTarget(input: SaveSyncTargetInput): Promise<void> {
  await api.post<{ ok: boolean }>('/api/sync/notion/test', input)
}

export async function syncNotionBidirectional(): Promise<NotionBidirectionalResult> {
  const res = await api.post<{ result: NotionBidirectionalResult }>('/api/sync/notion/bidirectional')
  return res.data.result
}

export async function getNotionDeletions(): Promise<ExternalDeletedNote[]> {
  const res = await api.get<{ items: ExternalDeletedNote[] }>('/api/sync/notion/deletions')
  return res.data.items
}

export async function confirmNotionDeletion(noteID: string): Promise<void> {
  await api.post(`/api/sync/notion/deletions/${encodeURIComponent(noteID)}/confirm`)
}

export async function restoreNotionDeletion(noteID: string): Promise<SyncResultItem> {
  const res = await api.post<{ item: SyncResultItem }>(`/api/sync/notion/deletions/${encodeURIComponent(noteID)}/restore`)
  return res.data.item
}

export async function getNoteSyncState(id: string, target?: SyncTargetType): Promise<SyncState | null> {
  const res = await api.get<{ state: SyncState | null }>(
    `/api/notes/${encodeURIComponent(id)}/sync-state`,
    target ? { target } : undefined,
  )
  return res.data.state
}
```

- [ ] **Step 6: Run tests to verify GREEN**

Run:

```powershell
cd frontend
npm run test:unit -- src/api/sync.test.ts
npm run build
```

Expected: PASS.

- [ ] **Step 7: Commit**

```powershell
git add frontend/package.json frontend/package-lock.json frontend/vitest.config.ts frontend/src/test/setup.ts frontend/src/api/sync.ts frontend/src/api/sync.test.ts
git commit -m "test: add frontend notion sync api coverage"
```

---

## Task 9: Add Frontend Notion Hooks And Component Tests

**Files:**
- Modify: `frontend/src/hooks/useSync.ts`
- Create: `frontend/src/components/sync/NotionSyncPanel.tsx`
- Modify: `frontend/src/components/sync/ObsidianSyncPanel.tsx`
- Create: `frontend/src/components/sync/SyncSettingsPanel.tsx`
- Modify: `frontend/src/routes/Notes.tsx`
- Test: `frontend/src/components/sync/NotionSyncPanel.test.tsx`

- [ ] **Step 1: Write failing Notion panel tests**

Create `frontend/src/components/sync/NotionSyncPanel.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { NotionSyncPanel } from './NotionSyncPanel'
import * as syncApi from '../../api/sync'

vi.mock('../../api/sync')

function renderPanel() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <QueryClientProvider client={queryClient}>
      <NotionSyncPanel />
    </QueryClientProvider>,
  )
}

describe('NotionSyncPanel', () => {
  beforeEach(() => {
    vi.mocked(syncApi.getSyncTargets).mockResolvedValue([])
    vi.mocked(syncApi.getNotionDeletions).mockResolvedValue([])
    vi.mocked(syncApi.saveSyncTarget).mockResolvedValue({
      id: 'target-1',
      type: 'notion',
      name: 'Personal Notion',
      vault_path: '',
      base_folder: '',
      config_json: JSON.stringify({ data_source_id: 'ds-123' }),
      enabled: true,
      auto_sync: false,
      created_at: 1,
      updated_at: 1,
    })
    vi.mocked(syncApi.testNotionTarget).mockResolvedValue(undefined)
    vi.mocked(syncApi.syncNotionBidirectional).mockResolvedValue({
      pushed: 1,
      pulled: 2,
      conflict_pulled: 1,
      imported: 3,
      external_deleted: 1,
      unsupported: 1,
      failed: 0,
      items: [],
    })
  })

  it('saves notion config with data source id and no token value', async () => {
    renderPanel()
    const user = userEvent.setup()

    await user.type(await screen.findByLabelText('Data Source ID'), 'ds-123')
    await user.click(screen.getByRole('button', { name: '保存 Notion 设置' }))

    await waitFor(() => expect(syncApi.saveSyncTarget).toHaveBeenCalled())
    expect(syncApi.saveSyncTarget).toHaveBeenCalledWith(expect.objectContaining({
      type: 'notion',
      name: 'Personal Notion',
      vault_path: '',
      base_folder: '',
      enabled: true,
      auto_sync: false,
    }))
    const payload = vi.mocked(syncApi.saveSyncTarget).mock.calls[0][0]
    expect(payload.config_json).toContain('"data_source_id":"ds-123"')
    expect(payload.config_json).toContain('"token_env":"FLOWSPACE_NOTION_TOKEN"')
    expect(payload.config_json).not.toContain('secret')
  })

  it('shows sync summary counts', async () => {
    renderPanel()
    const user = userEvent.setup()

    await user.click(await screen.findByRole('button', { name: 'Notion 双向同步' }))

    expect(await screen.findByText('导入 3')).toBeVisible()
    expect(screen.getByText('Notion 更新 2')).toBeVisible()
    expect(screen.getByText('冲突拉取 1')).toBeVisible()
    expect(screen.getByText('写入 1')).toBeVisible()
    expect(screen.getByText('复杂内容跳过 1')).toBeVisible()
  })
})
```

- [ ] **Step 2: Run tests to verify RED**

Run:

```powershell
cd frontend
npm run test:unit -- src/components/sync/NotionSyncPanel.test.tsx
```

Expected: FAIL because `NotionSyncPanel` and Notion hooks do not exist.

- [ ] **Step 3: Implement hooks and panel**

Add hooks in `frontend/src/hooks/useSync.ts`:

```ts
export function useTestNotionTarget() {
  return useMutation({ mutationFn: syncApi.testNotionTarget })
}

export function useSyncNotionBidirectional() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: syncApi.syncNotionBidirectional,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['notes'] })
      qc.invalidateQueries({ queryKey: ['note'] })
      qc.invalidateQueries({ queryKey: ['note-sync-state'] })
      qc.invalidateQueries({ queryKey: ['notion-deletions'] })
    },
  })
}

export function useNotionDeletions() {
  return useQuery({
    queryKey: ['notion-deletions'],
    queryFn: syncApi.getNotionDeletions,
  })
}
```

Create `frontend/src/components/sync/NotionSyncPanel.tsx` with labels matching the tests:

- `Data Source ID`
- `保存 Notion 设置`
- `测试 Notion 连接`
- `Notion 双向同步`

The panel should:

- Default name to `Personal Notion`.
- Store Notion config in `config_json`.
- Show token source text `FLOWSPACE_NOTION_TOKEN`.
- Render summary chips after sync.
- Render deletion candidates from `useNotionDeletions`.

Create `frontend/src/components/sync/SyncSettingsPanel.tsx` that contains a segmented control with `Obsidian` and `Notion`, renders existing `ObsidianSyncPanel` content for Obsidian and `NotionSyncPanel` for Notion. Keep the close overlay behavior in this wrapper so `Notes.tsx` only opens one settings panel.

- [ ] **Step 4: Run tests to verify GREEN**

Run:

```powershell
cd frontend
npm run test:unit -- src/components/sync/NotionSyncPanel.test.tsx
npm run build
```

Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add frontend/src/hooks/useSync.ts frontend/src/components/sync/NotionSyncPanel.tsx frontend/src/components/sync/SyncSettingsPanel.tsx frontend/src/components/sync/ObsidianSyncPanel.tsx frontend/src/routes/Notes.tsx frontend/src/components/sync/NotionSyncPanel.test.tsx
git commit -m "feat: add notion sync settings panel"
```

---

## Task 10: Add Multi-Target Editor Sync Card Tests

**Files:**
- Modify: `frontend/src/components/sync/NoteSyncCard.tsx`
- Modify: `frontend/src/hooks/useSync.ts`
- Test: `frontend/src/components/sync/NoteSyncCard.test.tsx`

- [ ] **Step 1: Write failing sync card tests**

Create `frontend/src/components/sync/NoteSyncCard.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { NoteSyncCard } from './NoteSyncCard'
import * as syncApi from '../../api/sync'

vi.mock('../../api/sync')

function renderCard() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <QueryClientProvider client={queryClient}>
      <NoteSyncCard noteID="note-1" />
    </QueryClientProvider>,
  )
}

describe('NoteSyncCard', () => {
  beforeEach(() => {
    vi.mocked(syncApi.getSyncTargets).mockResolvedValue([
      { id: 'obs-1', type: 'obsidian', name: 'Vault', vault_path: 'D:\\Vault', base_folder: 'FlowSpace Notes', config_json: '{}', enabled: true, auto_sync: false, created_at: 1, updated_at: 1 },
      { id: 'notion-1', type: 'notion', name: 'Personal Notion', vault_path: '', base_folder: '', config_json: '{"data_source_id":"ds-123"}', enabled: true, auto_sync: false, created_at: 1, updated_at: 1 },
    ])
  })

  it('shows notion synced state with page link', async () => {
    vi.mocked(syncApi.getNoteSyncState).mockImplementation(async (_id, target) => {
      if (target === 'notion') {
        return {
          note_id: 'note-1',
          target_id: 'notion-1',
          external_path: 'notion:page-1',
          external_id: 'page-1',
          external_url: 'https://www.notion.so/page-1',
          content_hash: 'flow',
          external_hash: 'notion',
          external_mtime: 1800000000,
          last_direction: 'pull',
          last_synced_at: 1800000000,
          status: 'synced',
          error_message: null,
        }
      }
      return null
    })

    renderCard()

    expect(await screen.findByText('Notion')).toBeVisible()
    expect(screen.getByRole('link', { name: '打开 Notion 页面' })).toHaveAttribute('href', 'https://www.notion.so/page-1')
    expect(screen.getByText('已同步')).toBeVisible()
  })

  it('shows notion deletion state without hiding obsidian card', async () => {
    vi.mocked(syncApi.getNoteSyncState).mockImplementation(async (_id, target) => {
      if (target === 'notion') {
        return {
          note_id: 'note-1',
          target_id: 'notion-1',
          external_path: 'notion:page-1',
          external_id: 'page-1',
          external_url: 'https://www.notion.so/page-1',
          content_hash: 'flow',
          external_hash: 'notion',
          external_mtime: 1800000000,
          last_direction: 'delete_detected',
          last_synced_at: 1800000000,
          status: 'external_deleted',
          error_message: null,
        }
      }
      return null
    })

    renderCard()

    expect(await screen.findByText('Notion 已删除')).toBeVisible()
    expect(screen.getByText('Obsidian')).toBeVisible()
    expect(screen.getByRole('button', { name: '保留并重新导出到 Notion' })).toBeVisible()
  })
})
```

- [ ] **Step 2: Run tests to verify RED**

Run:

```powershell
cd frontend
npm run test:unit -- src/components/sync/NoteSyncCard.test.tsx
```

Expected: FAIL because `getNoteSyncState` does not accept a target argument in hooks/card usage and the card is Obsidian-only.

- [ ] **Step 3: Implement minimal multi-target card**

Modify `frontend/src/hooks/useSync.ts`:

```ts
export function useNoteSyncState(noteID: string | undefined, target?: syncApi.SyncTargetType) {
  return useQuery({
    queryKey: ['note-sync-state', noteID, target ?? 'obsidian'],
    queryFn: () => syncApi.getNoteSyncState(noteID!, target),
    enabled: Boolean(noteID),
  })
}
```

Refactor `NoteSyncCard.tsx`:

- Query targets once.
- Render an Obsidian card when an Obsidian target exists, preserving current actions.
- Render a Notion card when a Notion target exists.
- Notion card uses `useNoteSyncState(noteID, 'notion')`.
- Notion card link uses `state.external_url`.
- External deleted status label for Notion is `Notion 已删除`.

- [ ] **Step 4: Run tests to verify GREEN**

Run:

```powershell
cd frontend
npm run test:unit -- src/components/sync/NoteSyncCard.test.tsx
npm run test:unit -- src/components/sync/NotionSyncPanel.test.tsx
npm run build
```

Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add frontend/src/hooks/useSync.ts frontend/src/components/sync/NoteSyncCard.tsx frontend/src/components/sync/NoteSyncCard.test.tsx
git commit -m "feat: show notion sync state in editor"
```

---

## Task 11: Add Playwright E2E Tests For Notion Sync UI

**Files:**
- Modify: `frontend/playwright.config.ts`
- Create: `frontend/tests/e2e/notion-sync.spec.ts`
- Modify: backend mock provider if E2E exposes a missing behavior

- [ ] **Step 1: Write failing Playwright tests**

Create `frontend/tests/e2e/notion-sync.spec.ts`:

```ts
import { expect, test } from '@playwright/test'

test('configures notion target and imports a mock notion note', async ({ page }) => {
  await page.goto('/notes')

  await page.getByRole('button', { name: '同步' }).click()
  await page.getByRole('tab', { name: 'Notion' }).click()
  await page.getByLabel('Data Source ID').fill('mock-data-source')
  await page.getByRole('button', { name: '保存 Notion 设置' }).click()
  await expect(page.getByText('Notion 设置已保存')).toBeVisible()

  await page.getByRole('button', { name: '测试 Notion 连接' }).click()
  await expect(page.getByText('Notion 连接可用')).toBeVisible()

  await page.getByRole('button', { name: 'Notion 双向同步' }).click()
  await expect(page.getByText('导入 1')).toBeVisible()
  await page.getByRole('button', { name: '关闭同步面板' }).click()

  await expect(page.getByText('Mock Notion Note')).toBeVisible()
})

test('shows notion sync card for imported mock note', async ({ page }) => {
  await page.goto('/notes')

  await page.getByRole('button', { name: '同步' }).click()
  await page.getByRole('tab', { name: 'Notion' }).click()
  await page.getByLabel('Data Source ID').fill('mock-data-source')
  await page.getByRole('button', { name: '保存 Notion 设置' }).click()
  await page.getByRole('button', { name: 'Notion 双向同步' }).click()
  await page.getByRole('button', { name: '关闭同步面板' }).click()

  await page.getByText('Mock Notion Note').click()
  await expect(page.getByText('Notion')).toBeVisible()
  await expect(page.getByRole('link', { name: '打开 Notion 页面' })).toHaveAttribute(/href/, /notion\.so\/mock-page-1/)
  await expect(page.getByText('已同步')).toBeVisible()
})
```

- [ ] **Step 2: Configure Playwright backend env and run RED**

Modify `frontend/playwright.config.ts` backend webServer env:

```ts
NOTION_PROVIDER: 'mock',
FLOWSPACE_NOTION_TOKEN: 'mock-token',
```

Run:

```powershell
cd frontend
npm run test:e2e -- notion-sync.spec.ts --project=chromium
```

Expected before the UI/backend work is complete: FAIL because the Notion tab, labels, routes, or mock provider behavior is missing.

- [ ] **Step 3: Implement minimal E2E support**

Make the already planned backend mock provider return:

```go
notionRemoteNote{
	PageID: "mock-page-1",
	URL: "https://www.notion.so/mock-page-1",
	Title: "Mock Notion Note",
	Markdown: "Imported from mock Notion.\n",
	Hash: notionMarkdownHash("Imported from mock Notion.\n"),
	LastEditedAt: 1900000000,
}
```

Make the frontend Notion tab use these stable accessible labels:

- tab: `Notion`
- input label: `Data Source ID`
- button: `保存 Notion 设置`
- button: `测试 Notion 连接`
- button: `Notion 双向同步`
- close button: `关闭同步面板`
- page link: `打开 Notion 页面`

- [ ] **Step 4: Run Playwright test to verify GREEN**

Run:

```powershell
cd frontend
npm run test:e2e -- notion-sync.spec.ts --project=chromium
```

Expected: PASS.

- [ ] **Step 5: Run frontend regression tests**

Run:

```powershell
cd frontend
npm run test:unit
npm run build
npm run test:e2e -- tasks-roadmap.spec.ts --project=chromium
```

Expected: PASS. The existing task/roadmap flow still works.

- [ ] **Step 6: Commit**

```powershell
git add frontend/playwright.config.ts frontend/tests/e2e/notion-sync.spec.ts backend/internal/service/notion_mock.go
git commit -m "test: cover notion sync frontend flows"
```

---

## Task 12: Documentation And Final Verification

**Files:**
- Modify: `README.md`
- Optional Modify: `docs/test-cases.md`

- [ ] **Step 1: Write documentation before final verification**

Add a `Notion 双向同步` section to `README.md`:

````md
### Notion 双向同步

FlowSpace 支持把一个个人 Notion Data Source 与本地笔记双向同步。冲突时 Notion 内容优先。

准备步骤：

1. 在 Notion 创建一个 integration。
2. 创建或选择一个 Data Source，例如 `FlowSpace Notes`。
3. 把 Data Source 分享给该 integration。
4. 在启动后端前设置：

```powershell
$env:FLOWSPACE_NOTION_TOKEN = "secret_xxx"
```

5. 在 FlowSpace 的笔记同步设置中打开 Notion，填写 Data Source ID。

同步规则：

- Notion 新页面会导入 FlowSpace。
- FlowSpace 新笔记会创建 Notion 页面。
- 两边同时修改时，Notion 覆盖 FlowSpace。
- Notion 页面删除、归档或进回收站后，FlowSpace 先标记为待确认删除。
- 包含复杂 Notion block 的页面不会被 FlowSpace 覆盖，以避免丢失复杂内容。
````

- [ ] **Step 2: Run full backend verification**

Run:

```powershell
cd backend
go test ./...
```

Expected: PASS.

- [ ] **Step 3: Run full frontend verification**

Run:

```powershell
cd frontend
npm run test:unit
npm run build
npm run test:e2e -- notion-sync.spec.ts --project=chromium
```

Expected: PASS.

- [ ] **Step 4: Manual smoke with mock provider**

Run:

```powershell
$env:NOTION_PROVIDER = "mock"
$env:FLOWSPACE_NOTION_TOKEN = "mock-token"
node .\scripts\start-flowspace.mjs --env test
```

Open the frontend test URL from the script output and verify:

- Notes page opens.
- Sync settings panel opens.
- Notion tab saves `mock-data-source`.
- Notion sync imports `Mock Notion Note`.
- Editor shows a Notion sync card and page link.

- [ ] **Step 5: Commit docs**

```powershell
git add README.md docs/test-cases.md
git commit -m "docs: document notion sync setup"
```

---

## Required Frontend Test Cases

The implementation must include these concrete frontend tests:

- `frontend/src/api/sync.test.ts`: verifies Notion target save payload does not include token secrets.
- `frontend/src/api/sync.test.ts`: verifies Notion endpoint paths and encoded ids.
- `frontend/src/components/sync/NotionSyncPanel.test.tsx`: verifies Data Source ID save payload and `FLOWSPACE_NOTION_TOKEN` hint.
- `frontend/src/components/sync/NotionSyncPanel.test.tsx`: verifies Notion bidirectional sync summary counts.
- `frontend/src/components/sync/NoteSyncCard.test.tsx`: verifies Notion synced state with external page link.
- `frontend/src/components/sync/NoteSyncCard.test.tsx`: verifies Notion external deletion state while Obsidian status remains visible.
- `frontend/tests/e2e/notion-sync.spec.ts`: verifies settings, connection test, bidirectional sync, and mock Notion import.
- `frontend/tests/e2e/notion-sync.spec.ts`: verifies editor Notion sync card for an imported mock page.

## Final Verification Checklist

- [ ] Backend unit tests pass with `go test ./...`.
- [ ] Frontend unit tests pass with `npm run test:unit`.
- [ ] Frontend build passes with `npm run build`.
- [ ] Notion Playwright spec passes in Chromium.
- [ ] Existing task/roadmap Playwright spec passes in Chromium.
- [ ] Notion token is never stored in SQLite, returned to frontend, or written to logs.
- [ ] Obsidian sync routes and editor card still work.
- [ ] Conflict behavior is Notion-first in automated tests.
- [ ] Notion deletion marks FlowSpace state as `external_deleted` before user confirmation.
