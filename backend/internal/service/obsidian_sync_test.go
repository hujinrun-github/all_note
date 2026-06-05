package service

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/repository"
	_ "modernc.org/sqlite"
)

func TestSanitizeMarkdownFileName(t *testing.T) {
	got := sanitizeMarkdownFileName(`A/B:C*D?`)
	want := "A-B-C-D.md"
	if got != want {
		t.Fatalf("sanitizeMarkdownFileName() = %q, want %q", got, want)
	}
}

func TestSanitizeMarkdownFileNameHandlesWindowsUnsafeNames(t *testing.T) {
	tests := []struct {
		name  string
		title string
		want  string
	}{
		{name: "reserved device", title: "CON", want: "CON-note.md"},
		{name: "reserved with extension", title: "COM1.md", want: "COM1-note.md"},
		{name: "control character", title: "bad\x01name", want: "bad-name.md"},
		{name: "trailing punctuation", title: "Report. ", want: "Report.md"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizeMarkdownFileName(tt.title); got != tt.want {
				t.Fatalf("sanitizeMarkdownFileName(%q) = %q, want %q", tt.title, got, tt.want)
			}
		})
	}
}

func TestRenderObsidianMarkdownIncludesFrontmatter(t *testing.T) {
	note := &model.Note{
		ID:        "n01",
		Title:     "\u4ea7\u54c1\u89c4\u5212",
		Body:      "\u6b63\u6587",
		FolderID:  "__work",
		Tags:      `["\u4ea7\u54c1","\u89c4\u5212"]`,
		CreatedAt: 1717200000,
		UpdatedAt: 1717286400,
	}

	markdown := renderObsidianMarkdown(note)

	for _, want := range []string{
		"---",
		"id: n01",
		"source: flowspace",
		`folder: "__work"`,
		"created:",
		"updated:",
		"  - \"\u4ea7\u54c1\"",
		"# \u4ea7\u54c1\u89c4\u5212",
		"\u6b63\u6587",
	} {
		if !strings.Contains(markdown, want) {
			t.Fatalf("renderObsidianMarkdown() missing %q in:\n%s", want, markdown)
		}
	}
}

func TestRenderObsidianMarkdownEscapesYamlStrings(t *testing.T) {
	note := &model.Note{
		ID:       "n01",
		Title:    "YAML Edge Cases",
		Body:     "body",
		FolderID: `folder: "quoted"`,
		Tags:     "[\"tag: value\",\"#comment\",\"quote\\\"tag\",\"multi\\nline\"]",
	}

	markdown := renderObsidianMarkdown(note)

	for _, want := range []string{
		`folder: "folder: \"quoted\""`,
		`  - "tag: value"`,
		`  - "#comment"`,
		`  - "quote\"tag"`,
		`  - "multi\nline"`,
	} {
		if !strings.Contains(markdown, want) {
			t.Fatalf("renderObsidianMarkdown() missing %q in:\n%s", want, markdown)
		}
	}
}

func TestResolveOutputPathRejectsEscape(t *testing.T) {
	target := &model.SyncTarget{
		VaultPath:  t.TempDir(),
		BaseFolder: "FlowSpace Notes",
	}

	if _, err := resolveOutputPath(target, `..\escape.md`); err == nil {
		t.Fatal("resolveOutputPath() expected error for escaped path")
	}
}

func TestResolveOutputPathAllowsBaseFolder(t *testing.T) {
	vault := t.TempDir()
	target := &model.SyncTarget{
		VaultPath:  vault,
		BaseFolder: "FlowSpace Notes",
	}

	got, err := resolveOutputPath(target, "Hello.md")
	if err != nil {
		t.Fatalf("resolveOutputPath(): %v", err)
	}

	want := filepath.Join(vault, "FlowSpace Notes", "Hello.md")
	if got != want {
		t.Fatalf("resolveOutputPath() = %q, want %q", got, want)
	}
}

func TestWriteNoteToTargetUsesUniqueFileNameForTitleCollision(t *testing.T) {
	openServiceTestDB(t)
	vault := t.TempDir()
	baseDir := filepath.Join(vault, "FlowSpace Notes")
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		t.Fatalf("create base dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(baseDir, "Same.md"), []byte("existing note"), 0644); err != nil {
		t.Fatalf("create existing note file: %v", err)
	}

	target := &model.SyncTarget{
		ID:         "sync-target-1",
		Type:       "obsidian",
		Name:       "Local Vault",
		VaultPath:  vault,
		BaseFolder: "FlowSpace Notes",
		Enabled:    true,
	}
	note := &model.Note{
		ID:        "note-1",
		Title:     "Same",
		Body:      "body",
		FolderID:  "__work",
		Tags:      "[]",
		CreatedAt: 1717200000,
		UpdatedAt: 1717286400,
	}
	insertServiceSyncFixtures(t, target, note)

	item, err := writeNoteToTarget(note, target)
	if err != nil {
		t.Fatalf("writeNoteToTarget(): %v", err)
	}

	wantPath := filepath.Join(baseDir, "Same-note-1.md")
	if item.ExternalPath != wantPath {
		t.Fatalf("ExternalPath = %q, want %q", item.ExternalPath, wantPath)
	}
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf("expected unique note file: %v", err)
	}
}

func TestWriteNoteToTargetUsesNumberedFileNameForRepeatedCollision(t *testing.T) {
	openServiceTestDB(t)
	vault := t.TempDir()
	baseDir := filepath.Join(vault, "FlowSpace Notes")
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		t.Fatalf("create base dir: %v", err)
	}
	for _, name := range []string{"Same.md", "Same-note-1.md"} {
		if err := os.WriteFile(filepath.Join(baseDir, name), []byte("existing note"), 0644); err != nil {
			t.Fatalf("create existing note file %q: %v", name, err)
		}
	}

	target := &model.SyncTarget{
		ID:         "sync-target-1",
		Type:       "obsidian",
		Name:       "Local Vault",
		VaultPath:  vault,
		BaseFolder: "FlowSpace Notes",
		Enabled:    true,
	}
	note := &model.Note{
		ID:        "note-1",
		Title:     "Same",
		Body:      "body",
		FolderID:  "__work",
		Tags:      "[]",
		CreatedAt: 1717200000,
		UpdatedAt: 1717286400,
	}
	insertServiceSyncFixtures(t, target, note)

	item, err := writeNoteToTarget(note, target)
	if err != nil {
		t.Fatalf("writeNoteToTarget(): %v", err)
	}

	wantPath := filepath.Join(baseDir, "Same-note-1-2.md")
	if item.ExternalPath != wantPath {
		t.Fatalf("ExternalPath = %q, want %q", item.ExternalPath, wantPath)
	}
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf("expected numbered unique note file: %v", err)
	}
}

func TestWriteNoteToTargetRecordsFailedState(t *testing.T) {
	openServiceTestDB(t)
	vault := t.TempDir()
	if err := os.WriteFile(filepath.Join(vault, "FlowSpace Notes"), []byte("not a directory"), 0644); err != nil {
		t.Fatalf("create blocking file: %v", err)
	}

	target := &model.SyncTarget{
		ID:         "sync-target-1",
		Type:       "obsidian",
		Name:       "Local Vault",
		VaultPath:  vault,
		BaseFolder: "FlowSpace Notes",
		Enabled:    true,
	}
	note := &model.Note{
		ID:        "note-1",
		Title:     "Blocked",
		Body:      "body",
		FolderID:  "__work",
		Tags:      "[]",
		CreatedAt: 1717200000,
		UpdatedAt: 1717286400,
	}
	insertServiceSyncFixtures(t, target, note)

	if _, err := writeNoteToTarget(note, target); err == nil {
		t.Fatal("expected writeNoteToTarget to fail")
	}

	state, err := repository.GetSyncState(note.ID, target.ID)
	if err != nil {
		t.Fatalf("get sync state: %v", err)
	}
	if state.Status != "failed" {
		t.Fatalf("expected failed state, got %q", state.Status)
	}
	if state.ErrorMessage == nil || !strings.Contains(*state.ErrorMessage, "create obsidian note folder") {
		t.Fatalf("expected write failure message, got %#v", state.ErrorMessage)
	}
}

func openServiceTestDB(t *testing.T) {
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
		db.Close()
	})
}

func insertServiceSyncFixtures(t *testing.T, target *model.SyncTarget, note *model.Note) {
	t.Helper()
	_, err := repository.DB.Exec(`
		INSERT OR IGNORE INTO folders (id, name, sort_order, created_at)
		VALUES ('__work', 'Work', 0, 1717200000)
	`)
	if err != nil {
		t.Fatalf("insert folder: %v", err)
	}
	_, err = repository.DB.Exec(`
		INSERT INTO notes (id, title, body, folder_id, tags, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, note.ID, note.Title, note.Body, note.FolderID, note.Tags, note.CreatedAt, note.UpdatedAt)
	if err != nil {
		t.Fatalf("insert note: %v", err)
	}
	_, err = repository.DB.Exec(`
		INSERT INTO sync_targets (id, type, name, vault_path, base_folder, enabled, auto_sync, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 1, 0, 1717200000, 1717200000)
	`, target.ID, target.Type, target.Name, target.VaultPath, target.BaseFolder)
	if err != nil {
		t.Fatalf("insert sync target: %v", err)
	}
}
