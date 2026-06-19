package service

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/repository"
	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/storage/sqlite"
)

func TestSyncNoteRequiresBinding(t *testing.T) {
	openServiceSyncStoreTestDB(t)
	note := createServiceStoreNote(t, "Unbound", "Body\n", "[]")

	_, err := SyncNote(note.ID)

	if !errors.Is(err, ErrSyncBindingRequired) {
		t.Fatalf("error = %v, want ErrSyncBindingRequired", err)
	}
}

func TestSyncNoteDispatchesToBoundObsidianTarget(t *testing.T) {
	store := openServiceSyncStoreTestDB(t)
	vault := t.TempDir()
	target := saveServiceStoreObsidianTarget(t, vault)
	note := createServiceStoreNote(t, "Bound Obsidian", "Body\n", "[]")
	putServiceStoreBinding(t, store, note.ID, target.ID)

	item, err := SyncNote(note.ID)

	if err != nil {
		t.Fatalf("sync note: %v", err)
	}
	if item.Status != "synced" || item.ExternalPath == "" {
		t.Fatalf("item = %+v", item)
	}
	if _, err := os.Stat(filepath.Join(vault, target.BaseFolder, "Bound Obsidian.md")); err != nil {
		t.Fatalf("expected obsidian file: %v", err)
	}
}

func TestSyncNoteDispatchesToBoundNotionTarget(t *testing.T) {
	store := openServiceSyncStoreTestDB(t)
	fake := &fakeNotionGateway{}
	withServiceNotionGateway(t, fake)
	target := saveServiceStoreNotionTarget(t, `{"data_source_id":"ds-123"}`)
	note := createServiceStoreNote(t, "Bound Notion", "Body\n", "[]")
	putServiceStoreBinding(t, store, note.ID, target.ID)

	item, err := SyncNote(note.ID)

	if err != nil {
		t.Fatalf("sync note: %v", err)
	}
	if item.Status != "pushed" || item.ExternalID != "created-"+note.ID {
		t.Fatalf("item = %+v", item)
	}
	if len(fake.created) != 1 || fake.created[0] != note.ID {
		t.Fatalf("created = %#v, want %s", fake.created, note.ID)
	}
}

func TestSyncNoteIgnoresRequiredTagsForExplicitBoundNote(t *testing.T) {
	store := openServiceSyncStoreTestDB(t)
	fake := &fakeNotionGateway{}
	withServiceNotionGateway(t, fake)
	target := saveServiceStoreNotionTarget(t, `{"data_source_id":"ds-123","required_tags":["sync"]}`)
	note := createServiceStoreNote(t, "No Sync Tag", "Body\n", "[]")
	putServiceStoreBinding(t, store, note.ID, target.ID)

	item, err := SyncNote(note.ID)

	if err != nil {
		t.Fatalf("sync note: %v", err)
	}
	if item.Status != "pushed" || len(fake.created) != 1 {
		t.Fatalf("item = %+v created = %#v", item, fake.created)
	}
}

func TestTargetPushOnlyProcessesBoundNotesForTarget(t *testing.T) {
	store := openServiceSyncStoreTestDB(t)
	fake := &fakeNotionGateway{}
	withServiceNotionGateway(t, fake)
	target := saveServiceStoreNotionTarget(t, `{"data_source_id":"ds-123","required_tags":["sync"]}`)
	bound := createServiceStoreNote(t, "Bound Push", "Body\n", "[]")
	unboundTagged := createServiceStoreNote(t, "Unbound Tagged", "Body\n", `["sync"]`)
	putServiceStoreBinding(t, store, bound.ID, target.ID)

	result, err := SyncTargetPush(target.ID)

	if err != nil {
		t.Fatalf("target push: %v", err)
	}
	if result.Synced != 1 || result.Failed != 0 {
		t.Fatalf("result = %+v", result)
	}
	if len(fake.created) != 1 || fake.created[0] != bound.ID {
		t.Fatalf("created = %#v, want only %s; unbound=%s", fake.created, bound.ID, unboundTagged.ID)
	}
}

func TestTargetPushRejectsDisabledTarget(t *testing.T) {
	openServiceSyncStoreTestDB(t)
	target := saveServiceStoreNotionTarget(t, `{"data_source_id":"ds-123"}`)
	target.Enabled = false
	if err := repository.SaveSyncTarget(&target); err != nil {
		t.Fatalf("disable target: %v", err)
	}

	_, err := SyncTargetPush(target.ID)

	if !errors.Is(err, ErrSyncTargetNotFound) {
		t.Fatalf("error = %v, want ErrSyncTargetNotFound", err)
	}
}

func TestTargetPullRejectsBindingConflict(t *testing.T) {
	store := openServiceSyncStoreTestDB(t)
	withServiceNotionGateway(t, &fakeNotionGateway{})
	targetA := saveServiceStoreNotionTargetNamed(t, "Target A", `{"data_source_id":"ds-a","required_tags":["sync"]}`)
	targetB := saveServiceStoreNotionTargetNamed(t, "Target B", `{"data_source_id":"ds-b","required_tags":["sync"]}`)
	note := createServiceStoreNote(t, "Foreign Binding", "Body\n", `["sync"]`)
	putServiceStoreBinding(t, store, note.ID, targetA.ID)
	if err := repository.UpsertSyncState(&model.SyncState{
		NoteID:        note.ID,
		TargetID:      targetB.ID,
		ExternalPath:  "notion:page-b",
		ExternalID:    "page-b",
		ContentHash:   "old-local",
		ExternalHash:  "old-remote",
		LastDirection: "pull",
		Status:        "synced",
	}); err != nil {
		t.Fatalf("upsert sync state: %v", err)
	}

	_, err := SyncTargetPull(targetB.ID)

	if !errors.Is(err, ErrSyncBindingConflict) {
		t.Fatalf("error = %v, want ErrSyncBindingConflict", err)
	}
}

func TestTargetPullRejectsObsidianFlowSpaceIDBoundToAnotherTarget(t *testing.T) {
	store := openServiceSyncStoreTestDB(t)
	vaultA := t.TempDir()
	vaultB := t.TempDir()
	targetA := saveServiceStoreObsidianTargetNamed(t, "Vault A", vaultA)
	targetB := saveServiceStoreObsidianTargetNamed(t, "Vault B", vaultB)
	targetB.ConfigJSON = `{"required_tags":["sync"]}`
	if err := repository.SaveSyncTarget(&targetB); err != nil {
		t.Fatalf("save target b tags: %v", err)
	}
	note := createServiceStoreNote(t, "Foreign Obsidian", "Original\n", "[]")
	putServiceStoreBinding(t, store, note.ID, targetA.ID)
	baseB := filepath.Join(vaultB, targetB.BaseFolder)
	if err := os.MkdirAll(baseB, 0755); err != nil {
		t.Fatalf("create target base: %v", err)
	}
	markdown := "---\nid: " + note.ID + "\nsource: flowspace\nfolder: \"__uncategorized\"\ntags:\n  - sync\n---\n\n# Foreign Obsidian\n\nChanged by other vault\n"
	if err := os.WriteFile(filepath.Join(baseB, "Foreign Obsidian.md"), []byte(markdown), 0644); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	result, err := SyncTargetPull(targetB.ID)

	if err != nil {
		t.Fatalf("target pull: %v", err)
	}
	if result.Failed != 1 || len(result.Items) != 1 || result.Items[0].Status != "failed" {
		t.Fatalf("result = %+v", result)
	}
	if result.Items[0].ErrorMessage != ErrSyncBindingConflict.Error() {
		t.Fatalf("error message = %q, want %q", result.Items[0].ErrorMessage, ErrSyncBindingConflict.Error())
	}
	got, err := repository.GetNoteByID(note.ID)
	if err != nil {
		t.Fatalf("get note: %v", err)
	}
	if got.Body != "Original\n" {
		t.Fatalf("body = %q, want unchanged", got.Body)
	}
}

func TestTargetPullImportsNewObsidianNoteWithBinding(t *testing.T) {
	store := openServiceSyncStoreTestDB(t)
	vault := t.TempDir()
	target := saveServiceStoreObsidianTarget(t, vault)
	target.ConfigJSON = `{"required_tags":["sync"]}`
	if err := repository.SaveSyncTarget(&target); err != nil {
		t.Fatalf("save target tags: %v", err)
	}
	base := filepath.Join(vault, target.BaseFolder)
	if err := os.MkdirAll(base, 0755); err != nil {
		t.Fatalf("create target base: %v", err)
	}
	markdown := "---\nfolder: \"__uncategorized\"\ntags:\n  - sync\n---\n\n# Imported Obsidian\n\nImported body\n"
	if err := os.WriteFile(filepath.Join(base, "Imported Obsidian.md"), []byte(markdown), 0644); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	result, err := SyncTargetPull(target.ID)

	if err != nil {
		t.Fatalf("target pull: %v", err)
	}
	if result.Imported != 1 || len(result.Items) != 1 {
		t.Fatalf("result = %+v", result)
	}
	binding, err := store.Sync().GetBinding(t.Context(), result.Items[0].NoteID)
	if err != nil {
		t.Fatalf("get imported binding: %v", err)
	}
	if binding.TargetID != target.ID {
		t.Fatalf("target_id = %q, want %q", binding.TargetID, target.ID)
	}
}

func TestTargetBidirectionalUsesTargetScope(t *testing.T) {
	store := openServiceSyncStoreTestDB(t)
	fake := &fakeNotionGateway{}
	withServiceNotionGateway(t, fake)
	target := saveServiceStoreNotionTarget(t, `{"data_source_id":"ds-123","required_tags":["sync"]}`)
	bound := createServiceStoreNote(t, "Bound Bidirectional", "Body\n", `["sync"]`)
	unboundTagged := createServiceStoreNote(t, "Unbound Bidirectional", "Body\n", `["sync"]`)
	putServiceStoreBinding(t, store, bound.ID, target.ID)

	result, err := SyncTargetBidirectional(target.ID)

	if err != nil {
		t.Fatalf("target bidirectional: %v", err)
	}
	if result.Pushed != 1 || result.Failed != 0 {
		t.Fatalf("result = %+v", result)
	}
	if len(fake.created) != 1 || fake.created[0] != bound.ID {
		t.Fatalf("created = %#v, want only %s; unbound=%s", fake.created, bound.ID, unboundTagged.ID)
	}
}

func openServiceSyncStoreTestDB(t *testing.T) storage.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "service.flowspace.test.db")
	store, err := sqlite.Provider{}.Open(t.Context(), storage.Config{
		Env:        "test",
		Driver:     storage.DriverSQLite,
		SQLitePath: dbPath,
	})
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	repository.SetStore(store)
	t.Cleanup(func() {
		repository.SetStore(nil)
		if err := store.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
	})
	return store
}

func saveServiceStoreObsidianTarget(t *testing.T, vault string) model.SyncTarget {
	t.Helper()
	return saveServiceStoreObsidianTargetNamed(t, "Local Vault", vault)
}

func saveServiceStoreObsidianTargetNamed(t *testing.T, name string, vault string) model.SyncTarget {
	t.Helper()
	target := model.SyncTarget{
		Type:       "obsidian",
		Name:       name,
		VaultPath:  vault,
		BaseFolder: "FlowSpace Notes",
		ConfigJSON: "{}",
		Enabled:    true,
		IsDefault:  true,
	}
	if err := repository.SaveSyncTarget(&target); err != nil {
		t.Fatalf("save obsidian target: %v", err)
	}
	return target
}

func saveServiceStoreNotionTarget(t *testing.T, configJSON string) model.SyncTarget {
	t.Helper()
	return saveServiceStoreNotionTargetNamed(t, "Personal Notion", configJSON)
}

func saveServiceStoreNotionTargetNamed(t *testing.T, name string, configJSON string) model.SyncTarget {
	t.Helper()
	target := model.SyncTarget{
		Type:       "notion",
		Name:       name,
		ConfigJSON: configJSON,
		Enabled:    true,
		IsDefault:  true,
	}
	if err := repository.SaveSyncTarget(&target); err != nil {
		t.Fatalf("save notion target: %v", err)
	}
	return target
}

func createServiceStoreNote(t *testing.T, title string, body string, tags string) model.Note {
	t.Helper()
	note := &model.Note{Title: title, Body: body, FolderID: "__uncategorized", Tags: tags}
	if err := repository.CreateNote(note); err != nil {
		t.Fatalf("create note: %v", err)
	}
	return *note
}

func putServiceStoreBinding(t *testing.T, store storage.Store, noteID string, targetID string) {
	t.Helper()
	if err := store.Sync().PutBinding(t.Context(), model.NoteSyncBinding{NoteID: noteID, TargetID: targetID}); err != nil {
		t.Fatalf("put binding: %v", err)
	}
}

func withServiceNotionGateway(t *testing.T, gateway notionSyncGateway) {
	t.Helper()
	t.Setenv("NOTION_PROVIDER", "mock")
	original := notionGatewayFactory
	notionGatewayFactory = func(token string) notionSyncGateway {
		return gateway
	}
	t.Cleanup(func() {
		notionGatewayFactory = original
	})
}
