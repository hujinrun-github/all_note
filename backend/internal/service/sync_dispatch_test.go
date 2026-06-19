package service

import (
	"database/sql"
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

func TestObsidianPushReservesClaimBeforeWrite(t *testing.T) {
	store := openServiceSyncStoreTestDB(t)
	vault := t.TempDir()
	target := saveServiceStoreObsidianTarget(t, vault)
	note := createServiceStoreNote(t, "Claimed Obsidian", "Body\n", "[]")
	putServiceStoreBinding(t, store, note.ID, target.ID)

	item, err := SyncNote(note.ID)

	if err != nil {
		t.Fatalf("sync note: %v", err)
	}
	claim, err := store.Sync().GetExternalClaimByNote(t.Context(), note.ID)
	if err != nil {
		t.Fatalf("get claim: %v", err)
	}
	if claim.TargetID != target.ID || claim.ExternalType != "obsidian_file" || claim.ExternalPath != item.ExternalPath {
		t.Fatalf("claim = %+v item = %+v", claim, item)
	}
	expectedKey, err := obsidianExternalKey(item.ExternalPath)
	if err != nil {
		t.Fatalf("external key: %v", err)
	}
	if claim.ExternalKey != expectedKey {
		t.Fatalf("external_key = %q, want %q", claim.ExternalKey, expectedKey)
	}
}

func TestObsidianPushClaimFailureDoesNotWriteFile(t *testing.T) {
	store := openServiceSyncStoreTestDB(t)
	vault := t.TempDir()
	target := saveServiceStoreObsidianTarget(t, vault)
	blockedPath := filepath.Join(vault, target.BaseFolder, "Blocked.md")
	blockedKey, err := obsidianExternalKey(blockedPath)
	if err != nil {
		t.Fatalf("external key: %v", err)
	}
	owner := createServiceStoreNote(t, "Owner", "Owner body\n", "[]")
	blocked := createServiceStoreNote(t, "Blocked", "Blocked body\n", "[]")
	putServiceStoreBinding(t, store, owner.ID, target.ID)
	putServiceStoreBinding(t, store, blocked.ID, target.ID)
	if err := store.Sync().PutExternalClaim(t.Context(), model.SyncExternalClaim{
		ExternalKey:  blockedKey,
		NoteID:       owner.ID,
		TargetID:     target.ID,
		ExternalType: "obsidian_file",
		ExternalPath: blockedPath,
	}); err != nil {
		t.Fatalf("put owner claim: %v", err)
	}

	_, err = SyncNote(blocked.ID)

	if err == nil {
		t.Fatal("expected claim conflict")
	}
	if _, statErr := os.Stat(blockedPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("blocked file stat error = %v, want not exist", statErr)
	}
	claim, err := store.Sync().GetExternalClaim(t.Context(), blockedKey)
	if err != nil {
		t.Fatalf("get claim: %v", err)
	}
	if claim.NoteID != owner.ID {
		t.Fatalf("claim owner = %q, want %q", claim.NoteID, owner.ID)
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

func TestObsidianPullDoesNotAutoBindSuppressedNote(t *testing.T) {
	store := openServiceSyncStoreTestDB(t)
	vault := t.TempDir()
	target := saveServiceStoreObsidianTarget(t, vault)
	target.ConfigJSON = `{"required_tags":["sync"]}`
	if err := repository.SaveSyncTarget(&target); err != nil {
		t.Fatalf("save target tags: %v", err)
	}
	note := createServiceStoreNote(t, "Suppressed Obsidian", "Original\n", "[]")
	if err := store.Sync().PutSuppression(t.Context(), model.NoteSyncSuppression{
		NoteID:   note.ID,
		TargetID: target.ID,
		Reason:   "user_unbound",
	}); err != nil {
		t.Fatalf("put suppression: %v", err)
	}
	base := filepath.Join(vault, target.BaseFolder)
	if err := os.MkdirAll(base, 0755); err != nil {
		t.Fatalf("create target base: %v", err)
	}
	markdown := "---\nid: " + note.ID + "\nsource: flowspace\nfolder: \"__uncategorized\"\ntags:\n  - sync\n---\n\n# Suppressed Obsidian\n\nPulled anyway\n"
	if err := os.WriteFile(filepath.Join(base, "Suppressed Obsidian.md"), []byte(markdown), 0644); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	result, err := SyncTargetPull(target.ID)

	if err != nil {
		t.Fatalf("target pull: %v", err)
	}
	if result.Failed != 1 || len(result.Items) != 1 || result.Items[0].ErrorMessage != ErrSyncBindingRequired.Error() {
		t.Fatalf("result = %+v", result)
	}
	if _, err := store.Sync().GetBinding(t.Context(), note.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("binding error = %v, want sql.ErrNoRows", err)
	}
	got, err := repository.GetNoteByID(note.ID)
	if err != nil {
		t.Fatalf("get note: %v", err)
	}
	if got.Body != "Original\n" {
		t.Fatalf("body = %q, want unchanged", got.Body)
	}
}

func TestObsidianPullAutoBindsUnsuppressedNote(t *testing.T) {
	store := openServiceSyncStoreTestDB(t)
	vault := t.TempDir()
	target := saveServiceStoreObsidianTarget(t, vault)
	target.ConfigJSON = `{"required_tags":["sync"]}`
	if err := repository.SaveSyncTarget(&target); err != nil {
		t.Fatalf("save target tags: %v", err)
	}
	note := createServiceStoreNote(t, "Unsuppressed Obsidian", "Original\n", "[]")
	base := filepath.Join(vault, target.BaseFolder)
	if err := os.MkdirAll(base, 0755); err != nil {
		t.Fatalf("create target base: %v", err)
	}
	markdown := "---\nid: " + note.ID + "\nsource: flowspace\nfolder: \"__uncategorized\"\ntags:\n  - sync\n---\n\n# Unsuppressed Obsidian\n\nPulled body\n"
	if err := os.WriteFile(filepath.Join(base, "Unsuppressed Obsidian.md"), []byte(markdown), 0644); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	result, err := SyncTargetPull(target.ID)

	if err != nil {
		t.Fatalf("target pull: %v", err)
	}
	if result.Pulled != 1 || result.Failed != 0 {
		t.Fatalf("result = %+v", result)
	}
	binding, err := store.Sync().GetBinding(t.Context(), note.ID)
	if err != nil {
		t.Fatalf("get binding: %v", err)
	}
	if binding.TargetID != target.ID {
		t.Fatalf("target_id = %q, want %q", binding.TargetID, target.ID)
	}
	got, err := repository.GetNoteByID(note.ID)
	if err != nil {
		t.Fatalf("get note: %v", err)
	}
	if got.Body != "Pulled body\n" {
		t.Fatalf("body = %q", got.Body)
	}
}

func TestObsidianPullChecksTombstoneAfterPathRename(t *testing.T) {
	store := openServiceSyncStoreTestDB(t)
	vault := t.TempDir()
	target := saveServiceStoreObsidianTarget(t, vault)
	target.ConfigJSON = `{"required_tags":["sync"]}`
	if err := repository.SaveSyncTarget(&target); err != nil {
		t.Fatalf("save target tags: %v", err)
	}
	formerNoteID := "12345678-1234-1234-1234-123456789abc"
	if err := store.Sync().PutImportTombstone(t.Context(), model.SyncImportTombstone{
		ExternalKey:  "obsidian:" + filepath.ToSlash(filepath.Join(vault, target.BaseFolder, "Old Name.md")),
		TargetID:     target.ID,
		FormerNoteID: formerNoteID,
		ExternalType: "obsidian_file",
		ExternalPath: filepath.Join(vault, target.BaseFolder, "Old Name.md"),
		Reason:       "note_deleted",
	}); err != nil {
		t.Fatalf("put tombstone: %v", err)
	}
	base := filepath.Join(vault, target.BaseFolder)
	if err := os.MkdirAll(base, 0755); err != nil {
		t.Fatalf("create target base: %v", err)
	}
	markdown := "---\nid: " + formerNoteID + "\nsource: flowspace\nfolder: \"__uncategorized\"\ntags:\n  - sync\n---\n\n# Renamed Tombstone\n\nShould not import\n"
	if err := os.WriteFile(filepath.Join(base, "Renamed Tombstone.md"), []byte(markdown), 0644); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	result, err := SyncTargetPull(target.ID)

	if err != nil {
		t.Fatalf("target pull: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].Status != "import_tombstoned" {
		t.Fatalf("result = %+v", result)
	}
	if _, err := repository.GetNoteByID(formerNoteID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("note lookup error = %v, want sql.ErrNoRows", err)
	}
}

func TestObsidianDeleteCandidatesAreTargetScoped(t *testing.T) {
	openServiceSyncStoreTestDB(t)
	targetA := saveServiceStoreObsidianTargetNamed(t, "Delete Vault A", t.TempDir())
	targetB := saveServiceStoreObsidianTargetNamed(t, "Delete Vault B", t.TempDir())
	noteA := createServiceStoreNote(t, "Deleted A", "Body\n", "[]")
	noteB := createServiceStoreNote(t, "Deleted B", "Body\n", "[]")
	putExternalDeletedStateForServiceTest(t, noteA.ID, targetA.ID, filepath.Join(targetA.VaultPath, targetA.BaseFolder, "Deleted A.md"))
	putExternalDeletedStateForServiceTest(t, noteB.ID, targetB.ID, filepath.Join(targetB.VaultPath, targetB.BaseFolder, "Deleted B.md"))

	items, err := ListObsidianDeletionCandidatesForTarget(targetB.ID)

	if err != nil {
		t.Fatalf("list deletions: %v", err)
	}
	if len(items) != 1 || items[0].NoteID != noteB.ID {
		t.Fatalf("items = %+v, want only %s", items, noteB.ID)
	}
}

func TestObsidianConfirmDeletionUsesTargetID(t *testing.T) {
	openServiceSyncStoreTestDB(t)
	targetA := saveServiceStoreObsidianTargetNamed(t, "Confirm Vault A", t.TempDir())
	targetB := saveServiceStoreObsidianTargetNamed(t, "Confirm Vault B", t.TempDir())
	note := createServiceStoreNote(t, "Confirm Target", "Body\n", "[]")
	pathB := filepath.Join(targetB.VaultPath, targetB.BaseFolder, "Confirm Target.md")
	if err := os.MkdirAll(filepath.Dir(pathB), 0755); err != nil {
		t.Fatalf("create base: %v", err)
	}
	putExternalDeletedStateForServiceTest(t, note.ID, targetB.ID, pathB)
	putServiceStoreBinding(t, repository.CurrentStore(), note.ID, targetB.ID)
	externalKey, err := obsidianExternalKey(pathB)
	if err != nil {
		t.Fatalf("external key: %v", err)
	}
	if err := repository.CurrentStore().Sync().PutExternalClaim(t.Context(), model.SyncExternalClaim{
		ExternalKey:  externalKey,
		NoteID:       note.ID,
		TargetID:     targetB.ID,
		ExternalType: "obsidian_file",
		ExternalPath: pathB,
	}); err != nil {
		t.Fatalf("put external claim: %v", err)
	}
	_ = targetA

	if err := ConfirmObsidianDeletionForTarget(note.ID, targetB.ID); err != nil {
		t.Fatalf("confirm deletion: %v", err)
	}
	if _, err := repository.GetNoteByID(note.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("note lookup error = %v, want sql.ErrNoRows", err)
	}
	tombstone, err := repository.CurrentStore().Sync().FindImportTombstone(t.Context(), targetB.ID, externalKey, note.ID, "obsidian_file")
	if err != nil {
		t.Fatalf("find tombstone: %v", err)
	}
	if tombstone.Reason != "note_deleted" || tombstone.ExternalPath != pathB {
		t.Fatalf("tombstone = %+v", tombstone)
	}
}

func TestObsidianRestoreDeletionUsesTargetID(t *testing.T) {
	openServiceSyncStoreTestDB(t)
	targetA := saveServiceStoreObsidianTargetNamed(t, "Restore Vault A", t.TempDir())
	targetB := saveServiceStoreObsidianTargetNamed(t, "Restore Vault B", t.TempDir())
	note := createServiceStoreNote(t, "Restore Target", "Body\n", "[]")
	pathB := filepath.Join(targetB.VaultPath, targetB.BaseFolder, "Restore Target.md")
	if err := os.MkdirAll(filepath.Dir(pathB), 0755); err != nil {
		t.Fatalf("create base: %v", err)
	}
	putExternalDeletedStateForServiceTest(t, note.ID, targetB.ID, pathB)
	putServiceStoreBinding(t, repository.CurrentStore(), note.ID, targetB.ID)
	_ = targetA

	item, err := RestoreObsidianDeletionForTarget(note.ID, targetB.ID)

	if err != nil {
		t.Fatalf("restore deletion: %v", err)
	}
	if item.ExternalPath != pathB {
		t.Fatalf("external path = %q, want %q", item.ExternalPath, pathB)
	}
	if _, err := os.Stat(pathB); err != nil {
		t.Fatalf("restored file: %v", err)
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

func putExternalDeletedStateForServiceTest(t *testing.T, noteID string, targetID string, externalPath string) {
	t.Helper()
	now := int64(1700000000)
	if err := repository.UpsertSyncState(&model.SyncState{
		NoteID:        noteID,
		TargetID:      targetID,
		ExternalPath:  externalPath,
		ContentHash:   "content",
		ExternalHash:  "external",
		LastDirection: "delete_detected",
		LastSyncedAt:  &now,
		Status:        "external_deleted",
	}); err != nil {
		t.Fatalf("upsert external deleted state: %v", err)
	}
}
