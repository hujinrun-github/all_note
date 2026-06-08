package service

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sort"
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

func TestParseObsidianMarkdownWithFlowSpaceFrontmatter(t *testing.T) {
	raw := "---\nid: n01\nsource: flowspace\nfolder: \"__work\"\ntags:\n  - product\n  - \" \"\n  - design\n---\n\n# Product Plan\n\nBody text\n"
	parsed, err := parseObsidianMarkdown([]byte(raw), "Product Plan.md")
	if err != nil {
		t.Fatalf("parse markdown: %v", err)
	}
	if parsed.ID != "n01" || parsed.Title != "Product Plan" || parsed.FolderID != "__work" {
		t.Fatalf("unexpected parsed metadata: %+v", parsed)
	}
	if parsed.Body != "Body text\n" || parsed.TagsJSON != `["product","design"]` {
		t.Fatalf("unexpected body or tags: body=%q tags=%s", parsed.Body, parsed.TagsJSON)
	}
	sum := sha256.Sum256([]byte(raw))
	if parsed.Hash != hex.EncodeToString(sum[:]) {
		t.Fatalf("unexpected hash: got %s", parsed.Hash)
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

func TestParseObsidianMarkdownPreservesTrailingHashInHeading(t *testing.T) {
	raw := "# C#\n\nBody\n"
	parsed, err := parseObsidianMarkdown([]byte(raw), "Language.md")
	if err != nil {
		t.Fatalf("parse markdown: %v", err)
	}
	if parsed.Title != "C#" || parsed.Body != "Body\n" {
		t.Fatalf("unexpected parsed markdown: %+v", parsed)
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
	got := scanRelativePaths(t, base, files)
	want := []string{"Nested/Two.md", "One.md"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("markdown files = %+v, want %+v", got, want)
	}
}

func TestScanObsidianMarkdownFilesSkipsSymlinkedMarkdown(t *testing.T) {
	vault := t.TempDir()
	base := filepath.Join(vault, "FlowSpace Notes")
	if err := os.MkdirAll(base, 0755); err != nil {
		t.Fatalf("create base: %v", err)
	}
	if err := os.WriteFile(filepath.Join(base, "One.md"), []byte("# One\n"), 0644); err != nil {
		t.Fatalf("write one: %v", err)
	}
	outside := filepath.Join(vault, "Outside.md")
	if err := os.WriteFile(outside, []byte("# Outside\n"), 0644); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	link := filepath.Join(base, "Linked.md")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("create symlink: %v", err)
	}

	files, err := scanObsidianMarkdownFiles(&model.SyncTarget{VaultPath: vault, BaseFolder: "FlowSpace Notes"})
	if err != nil {
		t.Fatalf("scan files: %v", err)
	}
	got := scanRelativePaths(t, base, files)
	want := []string{"One.md"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("markdown files = %+v, want %+v", got, want)
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

func TestWriteNoteToTargetRecordsPushExternalMetadata(t *testing.T) {
	openServiceTestDB(t)
	vault := t.TempDir()
	baseDir := filepath.Join(vault, "FlowSpace Notes")
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		t.Fatalf("create base dir: %v", err)
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
		Title:     "Push Metadata",
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
	info, err := os.Stat(item.ExternalPath)
	if err != nil {
		t.Fatalf("stat output file: %v", err)
	}
	state, err := repository.GetSyncState(note.ID, target.ID)
	if err != nil {
		t.Fatalf("get sync state: %v", err)
	}
	if state.ExternalHash != state.ContentHash || state.ExternalHash == "" {
		t.Fatalf("unexpected external hash/content hash: %+v", state)
	}
	if state.ExternalMTime == nil || *state.ExternalMTime != info.ModTime().Unix() {
		t.Fatalf("unexpected external mtime: state=%+v file=%d", state, info.ModTime().Unix())
	}
	if state.LastDirection != "push" || state.Status != "synced" {
		t.Fatalf("unexpected sync status metadata: %+v", state)
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

func TestSyncObsidianBidirectionalHandlesRenamedMarkdownFileWithSameID(t *testing.T) {
	openObsidianSyncTestDB(t)
	vault := t.TempDir()
	target := saveObsidianTargetForTest(t, vault)
	note := createNoteForSyncTest(t, "Rename Me", "Body\n")
	item, err := writeNoteToTarget(&note, &target)
	if err != nil {
		t.Fatalf("initial push: %v", err)
	}

	newDir := filepath.Join(vault, target.BaseFolder, "Moved")
	if err := os.MkdirAll(newDir, 0755); err != nil {
		t.Fatalf("create moved dir: %v", err)
	}
	newPath := filepath.Join(newDir, "Renamed.md")
	if err := os.Rename(item.ExternalPath, newPath); err != nil {
		t.Fatalf("rename external file: %v", err)
	}

	result := SyncObsidianBidirectional()
	if result.Failed != 0 || result.ExternalDeleted != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.Pulled != 1 && !hasSyncResultStatus(result.Items, note.ID, "synced") {
		t.Fatalf("expected renamed file to pull or remain synced, got %+v", result)
	}
	state, err := repository.GetSyncState(note.ID, target.ID)
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if state.Status != "synced" || normalizedPath(state.ExternalPath) != normalizedPath(newPath) {
		t.Fatalf("unexpected state after rename: %+v, want path %q", state, newPath)
	}
	if _, err := repository.GetNoteByID(note.ID); err != nil {
		t.Fatalf("FlowSpace note should still exist after rename: %v", err)
	}
}

func TestSyncObsidianBidirectionalPushesTitleChangeToExistingMappedPath(t *testing.T) {
	openObsidianSyncTestDB(t)
	vault := t.TempDir()
	target := saveObsidianTargetForTest(t, vault)
	note := createNoteForSyncTest(t, "Old Title", "Old content\n")
	item, err := writeNoteToTarget(&note, &target)
	if err != nil {
		t.Fatalf("initial push: %v", err)
	}
	oldPath := item.ExternalPath

	if _, err := repository.UpdateNote(note.ID, &model.UpdateNoteRequest{
		Title: ptrString("New Title"),
		Body:  ptrString("New content\n"),
	}); err != nil {
		t.Fatalf("update note: %v", err)
	}

	result := SyncObsidianBidirectional()
	if result.Pushed != 1 || result.Failed != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
	state, err := repository.GetSyncState(note.ID, target.ID)
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if normalizedPath(state.ExternalPath) != normalizedPath(oldPath) {
		t.Fatalf("expected mapped path to remain %q, got %+v", oldPath, state)
	}
	raw, err := os.ReadFile(oldPath)
	if err != nil {
		t.Fatalf("read old path: %v", err)
	}
	if !strings.Contains(string(raw), "# New Title") || !strings.Contains(string(raw), "New content") {
		t.Fatalf("expected updated content at old path, got %s", string(raw))
	}
	duplicatePath := filepath.Join(vault, target.BaseFolder, "New Title.md")
	if _, err := os.Stat(duplicatePath); !os.IsNotExist(err) {
		t.Fatalf("expected no duplicate title-derived file at %q, stat err=%v", duplicatePath, err)
	}

	second := SyncObsidianBidirectional()
	if second.Pulled != 0 || second.Failed != 0 {
		t.Fatalf("expected second sync not to pull stale content, got %+v", second)
	}
	got, err := repository.GetNoteByID(note.ID)
	if err != nil {
		t.Fatalf("get note after second sync: %v", err)
	}
	if got.Title != "New Title" || got.Body != "New content\n" {
		t.Fatalf("expected FlowSpace note to keep new content, got %+v", got)
	}
}

func TestSyncObsidianBidirectionalRetriesFailedStateWithMatchingContentHash(t *testing.T) {
	openObsidianSyncTestDB(t)
	vault := t.TempDir()
	target := saveObsidianTargetForTest(t, vault)
	note := createNoteForSyncTest(t, "Retry Failed", "Retry body\n")
	base := filepath.Join(vault, target.BaseFolder)
	if err := os.MkdirAll(base, 0755); err != nil {
		t.Fatalf("create base: %v", err)
	}
	mappedPath := filepath.Join(base, "Retry Failed.md")
	contentHash := markdownHash(renderObsidianMarkdown(&note))
	if err := repository.UpsertSyncState(&model.SyncState{
		NoteID:        note.ID,
		TargetID:      target.ID,
		ExternalPath:  mappedPath,
		ContentHash:   contentHash,
		ExternalHash:  "",
		LastDirection: "push",
		Status:        "failed",
	}); err != nil {
		t.Fatalf("create failed state: %v", err)
	}

	result := SyncObsidianBidirectional()
	if result.Pushed != 1 || result.Failed != 0 || result.ExternalDeleted != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
	raw, err := os.ReadFile(mappedPath)
	if err != nil {
		t.Fatalf("read retried file: %v", err)
	}
	if !strings.Contains(string(raw), "Retry body") {
		t.Fatalf("expected retry to write note content, got %s", string(raw))
	}
	state, err := repository.GetSyncState(note.ID, target.ID)
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if state.Status != "synced" || normalizedPath(state.ExternalPath) != normalizedPath(mappedPath) {
		t.Fatalf("unexpected retried state: %+v", state)
	}
}

func TestSyncObsidianBidirectionalRetriesFailedPushWithoutPullingStaleExternal(t *testing.T) {
	openObsidianSyncTestDB(t)
	vault := t.TempDir()
	target := saveObsidianTargetForTest(t, vault)
	note := createNoteForSyncTest(t, "Failed Push", "Original body\n")
	item, err := writeNoteToTarget(&note, &target)
	if err != nil {
		t.Fatalf("initial push: %v", err)
	}
	oldState, err := repository.GetSyncState(note.ID, target.ID)
	if err != nil {
		t.Fatalf("get initial state: %v", err)
	}

	if _, err := repository.UpdateNote(note.ID, &model.UpdateNoteRequest{Body: ptrString("Fresh local body\n")}); err != nil {
		t.Fatalf("update note: %v", err)
	}
	updated, err := repository.GetNoteByID(note.ID)
	if err != nil {
		t.Fatalf("get updated note: %v", err)
	}
	if err := recordSyncFailure(updated, &target, item.ExternalPath, markdownHash(renderObsidianMarkdown(updated)), errors.New("simulated push failure")); err != nil {
		t.Fatalf("record failed push: %v", err)
	}
	failedState, err := repository.GetSyncState(note.ID, target.ID)
	if err != nil {
		t.Fatalf("get failed state: %v", err)
	}
	if failedState.ExternalHash != oldState.ExternalHash || failedState.ExternalMTime == nil || oldState.ExternalMTime == nil || *failedState.ExternalMTime != *oldState.ExternalMTime {
		t.Fatalf("expected failed push to preserve external metadata: failed=%+v old=%+v", failedState, oldState)
	}

	result := SyncObsidianBidirectional()
	if result.Pushed != 1 || result.Pulled != 0 || result.Failed != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
	got, err := repository.GetNoteByID(note.ID)
	if err != nil {
		t.Fatalf("get note after retry: %v", err)
	}
	if got.Body != "Fresh local body\n" {
		t.Fatalf("expected local body to survive retry, got %q", got.Body)
	}
	raw, err := os.ReadFile(item.ExternalPath)
	if err != nil {
		t.Fatalf("read retried external file: %v", err)
	}
	if !strings.Contains(string(raw), "Fresh local body") {
		t.Fatalf("expected retry to push fresh body, got %s", string(raw))
	}
}

func TestSyncObsidianBidirectionalContinuesAfterMalformedMarkdownFile(t *testing.T) {
	openObsidianSyncTestDB(t)
	vault := t.TempDir()
	target := saveObsidianTargetForTest(t, vault)
	base := filepath.Join(vault, target.BaseFolder)
	if err := os.MkdirAll(base, 0755); err != nil {
		t.Fatalf("create base: %v", err)
	}
	if err := os.WriteFile(filepath.Join(base, "Bad.md"), []byte("---\nid: [\n---\n\n# Bad\n\nBroken\n"), 0644); err != nil {
		t.Fatalf("write malformed markdown: %v", err)
	}
	if err := os.WriteFile(filepath.Join(base, "Valid.md"), []byte("# Valid\n\nImported anyway\n"), 0644); err != nil {
		t.Fatalf("write valid markdown: %v", err)
	}

	result := SyncObsidianBidirectional()
	if result.Failed < 1 || result.Imported != 1 {
		t.Fatalf("expected partial failure and valid import, got %+v", result)
	}
	notes, err := repository.ListAllNotes()
	if err != nil {
		t.Fatalf("list notes: %v", err)
	}
	if len(notes) != 1 || notes[0].Title != "Valid" || notes[0].Body != "Imported anyway\n" {
		t.Fatalf("unexpected imported notes: %+v", notes)
	}
}

func TestSyncObsidianBidirectionalDoesNotOverwriteMalformedMappedFile(t *testing.T) {
	openObsidianSyncTestDB(t)
	vault := t.TempDir()
	target := saveObsidianTargetForTest(t, vault)
	note := createNoteForSyncTest(t, "Malformed Mapped", "Original\n")
	item, err := writeNoteToTarget(&note, &target)
	if err != nil {
		t.Fatalf("initial push: %v", err)
	}

	malformed := []byte("---\nid: [\n---\n\n# Malformed Mapped\n\nDo not overwrite\n")
	if err := os.WriteFile(item.ExternalPath, malformed, 0644); err != nil {
		t.Fatalf("write malformed mapped file: %v", err)
	}
	if _, err := repository.UpdateNote(note.ID, &model.UpdateNoteRequest{Body: ptrString("Local change\n")}); err != nil {
		t.Fatalf("update local note: %v", err)
	}

	result := SyncObsidianBidirectional()
	if result.Failed < 1 || result.Pushed != 0 {
		t.Fatalf("expected malformed mapped file to fail without push, got %+v", result)
	}
	if !hasSyncResultStatus(result.Items, note.ID, "failed") {
		t.Fatalf("expected failure item to include mapped note id, got %+v", result.Items)
	}
	raw, err := os.ReadFile(item.ExternalPath)
	if err != nil {
		t.Fatalf("read malformed mapped file: %v", err)
	}
	if string(raw) != string(malformed) {
		t.Fatalf("expected malformed file to remain untouched, got %s", string(raw))
	}
}

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

func TestRestoreObsidianDeletionReexportsMappedPath(t *testing.T) {
	openObsidianSyncTestDB(t)
	vault := t.TempDir()
	target := saveObsidianTargetForTest(t, vault)
	note := createNoteForSyncTest(t, "Restore Mapped", "Body\n")
	item, err := writeNoteToTarget(&note, &target)
	if err != nil {
		t.Fatalf("initial push: %v", err)
	}
	movedDir := filepath.Join(vault, target.BaseFolder, "Moved")
	if err := os.MkdirAll(movedDir, 0755); err != nil {
		t.Fatalf("create moved dir: %v", err)
	}
	mappedPath := filepath.Join(movedDir, "Renamed Restore.md")
	if err := os.Rename(item.ExternalPath, mappedPath); err != nil {
		t.Fatalf("rename external file: %v", err)
	}
	renamed := SyncObsidianBidirectional()
	if renamed.Failed != 0 {
		t.Fatalf("unexpected rename sync result: %+v", renamed)
	}
	state, err := repository.GetSyncState(note.ID, target.ID)
	if err != nil {
		t.Fatalf("get renamed state: %v", err)
	}
	if normalizedPath(state.ExternalPath) != normalizedPath(mappedPath) {
		t.Fatalf("expected renamed state path %q, got %+v", mappedPath, state)
	}

	if err := os.Remove(mappedPath); err != nil {
		t.Fatalf("remove external: %v", err)
	}
	deleted := SyncObsidianBidirectional()
	if deleted.ExternalDeleted != 1 {
		t.Fatalf("expected external deletion: %+v", deleted)
	}

	restored, err := RestoreObsidianDeletion(note.ID)
	if err != nil {
		t.Fatalf("restore deletion: %v", err)
	}
	if restored.Status != "synced" || normalizedPath(restored.ExternalPath) != normalizedPath(mappedPath) {
		t.Fatalf("unexpected restore result: %+v, want path %q", restored, mappedPath)
	}
	if _, err := os.Stat(mappedPath); err != nil {
		t.Fatalf("expected mapped external file to exist: %v", err)
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

func openObsidianSyncTestDB(t *testing.T) {
	t.Helper()
	openServiceTestDB(t)
}

func saveObsidianTargetForTest(t *testing.T, vault string) model.SyncTarget {
	t.Helper()
	target := model.SyncTarget{
		ID:         "sync-target-1",
		Type:       "obsidian",
		Name:       "Local Vault",
		VaultPath:  vault,
		BaseFolder: "FlowSpace Notes",
		Enabled:    true,
	}
	if err := repository.SaveSyncTarget(&target); err != nil {
		t.Fatalf("save sync target: %v", err)
	}
	return target
}

func createNoteForSyncTest(t *testing.T, title, body string) model.Note {
	t.Helper()
	note, err := CreateNote(&model.CreateNoteRequest{
		Title:    title,
		Body:     body,
		FolderID: "__uncategorized",
		Tags:     "[]",
	})
	if err != nil {
		t.Fatalf("create note: %v", err)
	}
	return *note
}

func ptrString(value string) *string {
	return &value
}

func hasSyncResultStatus(items []model.SyncResultItem, noteID, status string) bool {
	for _, item := range items {
		if item.NoteID == noteID && item.Status == status {
			return true
		}
	}
	return false
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

func scanRelativePaths(t *testing.T, base string, files []obsidianMarkdownFile) []string {
	t.Helper()
	paths := make([]string, 0, len(files))
	for _, file := range files {
		rel, err := filepath.Rel(base, file.Path)
		if err != nil {
			t.Fatalf("relative path for %q: %v", file.Path, err)
		}
		paths = append(paths, filepath.ToSlash(rel))
	}
	sort.Strings(paths)
	return paths
}
