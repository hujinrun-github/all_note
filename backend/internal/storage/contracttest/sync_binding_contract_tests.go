package contracttest

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

func RunSyncBindingSuite(t *testing.T, factory StoreFactory) {
	t.Helper()

	suite := syncBindingContractSuite{factory: factory}
	t.Run("TestSyncBindingContractAllowsOneBindingPerNote", suite.TestSyncBindingContractAllowsOneBindingPerNote)
	t.Run("TestSyncBindingContractClaimRequiresCurrentBinding", suite.TestSyncBindingContractClaimRequiresCurrentBinding)
	t.Run("TestSyncBindingContractBindingDeleteCascadesClaim", suite.TestSyncBindingContractBindingDeleteCascadesClaim)
	t.Run("TestSyncBindingContractTombstoneSurvivesNoteDelete", suite.TestSyncBindingContractTombstoneSurvivesNoteDelete)
	t.Run("TestSyncBindingContractFindsTombstoneAfterExternalRename", suite.TestSyncBindingContractFindsTombstoneAfterExternalRename)
	t.Run("TestSyncBindingContractOneDefaultTargetPerType", suite.TestSyncBindingContractOneDefaultTargetPerType)
	t.Run("TestSyncBindingContractDefaultTargetDoesNotUseUpdatedAtFallback", suite.TestSyncBindingContractDefaultTargetDoesNotUseUpdatedAtFallback)
	t.Run("TestSyncBindingContractTargetIdentityLockCounts", suite.TestSyncBindingContractTargetIdentityLockCounts)
	t.Run("TestSyncBindingContractLockTargetReturnsTarget", suite.TestSyncBindingContractLockTargetReturnsTarget)
	t.Run("TestSyncBindingContractTargetDeleteRestrictedByBinding", suite.TestSyncBindingContractTargetDeleteRestrictedByBinding)
	t.Run("TestSyncBindingContractSuppressionReasonDefaults", suite.TestSyncBindingContractSuppressionReasonDefaults)
	t.Run("TestSyncBindingContractTombstoneReasonDefaults", suite.TestSyncBindingContractTombstoneReasonDefaults)
}

type syncBindingContractSuite struct {
	factory StoreFactory
}

func (s syncBindingContractSuite) TestSyncBindingContractAllowsOneBindingPerNote(t *testing.T) {
	store := s.factory(t)
	defer store.Close()

	ctx := scopedContractContext(t, store)
	note := createContractNote(t, ctx, store, "One Binding")
	targetA := createContractTarget(t, ctx, store, "binding-target-a", "notion", "Binding Target A", true)
	targetB := createContractTarget(t, ctx, store, "binding-target-b", "obsidian", "Binding Target B", true)

	if err := store.Sync().PutBinding(ctx, model.NoteSyncBinding{NoteID: note.ID, TargetID: targetA.ID}); err != nil {
		t.Fatalf("put initial binding: %v", err)
	}
	if err := store.Sync().PutBinding(ctx, model.NoteSyncBinding{NoteID: note.ID, TargetID: targetB.ID}); err != nil {
		t.Fatalf("replace binding: %v", err)
	}

	binding, err := store.Sync().GetBinding(ctx, note.ID)
	if err != nil {
		t.Fatalf("get binding: %v", err)
	}
	if binding.TargetID != targetB.ID {
		t.Fatalf("expected replacement binding to target %q, got %+v", targetB.ID, binding)
	}
	bindingsA, err := store.Sync().ListBindingsByTarget(ctx, targetA.ID)
	if err != nil {
		t.Fatalf("list old target bindings: %v", err)
	}
	if len(bindingsA) != 0 {
		t.Fatalf("expected old target to have no bindings, got %+v", bindingsA)
	}
	countB, err := store.Sync().CountBindingsByTarget(ctx, targetB.ID)
	if err != nil {
		t.Fatalf("count new target bindings: %v", err)
	}
	if countB != 1 {
		t.Fatalf("expected new target binding count 1, got %d", countB)
	}
}

func (s syncBindingContractSuite) TestSyncBindingContractClaimRequiresCurrentBinding(t *testing.T) {
	store := s.factory(t)
	defer store.Close()

	ctx := scopedContractContext(t, store)
	note := createContractNote(t, ctx, store, "Claim Binding")
	targetA := createContractTarget(t, ctx, store, "claim-target-a", "notion", "Claim Target A", true)
	targetB := createContractTarget(t, ctx, store, "claim-target-b", "obsidian", "Claim Target B", true)

	if err := store.Sync().PutExternalClaim(ctx, model.SyncExternalClaim{
		ExternalKey:  "notion:unbound",
		NoteID:       note.ID,
		TargetID:     targetA.ID,
		ExternalType: "notion_page",
		ExternalID:   "unbound",
	}); err == nil {
		t.Fatal("expected claim without binding to fail")
	}
	if err := store.Sync().PutBinding(ctx, model.NoteSyncBinding{NoteID: note.ID, TargetID: targetA.ID}); err != nil {
		t.Fatalf("put binding: %v", err)
	}
	if err := store.Sync().PutExternalClaim(ctx, model.SyncExternalClaim{
		ExternalKey:  "obsidian:wrong-target.md",
		NoteID:       note.ID,
		TargetID:     targetB.ID,
		ExternalType: "obsidian_file",
		ExternalPath: "wrong-target.md",
	}); err == nil {
		t.Fatal("expected claim for a different target than current binding to fail")
	}
	if err := store.Sync().PutExternalClaim(ctx, model.SyncExternalClaim{
		ExternalKey:  "notion:page-1",
		NoteID:       note.ID,
		TargetID:     targetA.ID,
		ExternalType: "notion_page",
		ExternalID:   "page-1",
	}); err != nil {
		t.Fatalf("put current binding claim: %v", err)
	}
	claim, err := store.Sync().GetExternalClaimByNote(ctx, note.ID)
	if err != nil {
		t.Fatalf("get claim by note: %v", err)
	}
	if claim.ExternalKey != "notion:page-1" || claim.TargetID != targetA.ID {
		t.Fatalf("unexpected claim: %+v", claim)
	}
}

func (s syncBindingContractSuite) TestSyncBindingContractBindingDeleteCascadesClaim(t *testing.T) {
	store := s.factory(t)
	defer store.Close()

	ctx := scopedContractContext(t, store)
	note := createContractNote(t, ctx, store, "Cascade Claim")
	target := createContractTarget(t, ctx, store, "cascade-target", "notion", "Cascade Target", true)
	if err := store.Sync().PutBinding(ctx, model.NoteSyncBinding{NoteID: note.ID, TargetID: target.ID}); err != nil {
		t.Fatalf("put binding: %v", err)
	}
	if err := store.Sync().PutExternalClaim(ctx, model.SyncExternalClaim{
		ExternalKey:  "notion:cascade",
		NoteID:       note.ID,
		TargetID:     target.ID,
		ExternalType: "notion_page",
		ExternalID:   "cascade",
	}); err != nil {
		t.Fatalf("put claim: %v", err)
	}
	if err := store.Sync().DeleteBinding(ctx, note.ID); err != nil {
		t.Fatalf("delete binding: %v", err)
	}
	if _, err := store.Sync().GetExternalClaim(ctx, "notion:cascade"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected claim to be cascade-deleted, got %v", err)
	}
}

func (s syncBindingContractSuite) TestSyncBindingContractTombstoneSurvivesNoteDelete(t *testing.T) {
	store := s.factory(t)
	defer store.Close()

	ctx := scopedContractContext(t, store)
	note := createContractNote(t, ctx, store, "Tombstone Survives")
	target := createContractTarget(t, ctx, store, "tombstone-target", "obsidian", "Tombstone Target", true)
	tombstone := model.SyncImportTombstone{
		ExternalKey:  "obsidian:/vault/old.md",
		TargetID:     target.ID,
		FormerNoteID: note.ID,
		ExternalType: "obsidian_file",
		ExternalPath: "/vault/old.md",
		Reason:       "note_deleted",
	}
	if err := store.Sync().PutImportTombstone(ctx, tombstone); err != nil {
		t.Fatalf("put tombstone: %v", err)
	}
	if err := store.Notes().Delete(ctx, note.ID); err != nil {
		t.Fatalf("delete note: %v", err)
	}
	found, err := store.Sync().FindImportTombstone(ctx, target.ID, tombstone.ExternalKey, "", tombstone.ExternalType)
	if err != nil {
		t.Fatalf("find tombstone after note delete: %v", err)
	}
	if found.FormerNoteID != note.ID {
		t.Fatalf("unexpected tombstone: %+v", found)
	}
}

func (s syncBindingContractSuite) TestSyncBindingContractFindsTombstoneAfterExternalRename(t *testing.T) {
	store := s.factory(t)
	defer store.Close()

	ctx := scopedContractContext(t, store)
	note := createContractNote(t, ctx, store, "Renamed Tombstone")
	target := createContractTarget(t, ctx, store, "rename-target", "obsidian", "Rename Target", true)
	if err := store.Sync().PutImportTombstone(ctx, model.SyncImportTombstone{
		ExternalKey:  "obsidian:/vault/original.md",
		TargetID:     target.ID,
		FormerNoteID: note.ID,
		ExternalType: "obsidian_file",
		ExternalPath: "/vault/original.md",
		Reason:       "user_unbound",
	}); err != nil {
		t.Fatalf("put tombstone: %v", err)
	}
	found, err := store.Sync().FindImportTombstone(ctx, target.ID, "obsidian:/vault/renamed.md", note.ID, "obsidian_file")
	if err != nil {
		t.Fatalf("find renamed tombstone: %v", err)
	}
	if found.ExternalKey != "obsidian:/vault/original.md" {
		t.Fatalf("expected fallback tombstone, got %+v", found)
	}
}

func (s syncBindingContractSuite) TestSyncBindingContractOneDefaultTargetPerType(t *testing.T) {
	store := s.factory(t)
	defer store.Close()

	ctx := scopedContractContext(t, store)
	targetA := createContractTarget(t, ctx, store, "default-notion-a", "notion", "Default Notion A", true)
	targetB := createContractTarget(t, ctx, store, "default-notion-b", "notion", "Default Notion B", true)
	targetA.IsDefault = true
	if err := store.Sync().SaveTarget(ctx, targetA); err != nil {
		t.Fatalf("set first default: %v", err)
	}
	targetB.IsDefault = true
	if err := store.Sync().SaveTarget(ctx, targetB); err != nil {
		t.Fatalf("set second default: %v", err)
	}
	defaultTarget, err := store.Sync().GetDefaultTarget(ctx, "notion")
	if err != nil {
		t.Fatalf("get default target: %v", err)
	}
	if defaultTarget.ID != targetB.ID {
		t.Fatalf("expected second default to replace first, got %+v", defaultTarget)
	}
	targets, err := store.Sync().ListTargets(ctx)
	if err != nil {
		t.Fatalf("list targets: %v", err)
	}
	defaults := 0
	for _, target := range targets {
		if target.Type == "notion" && target.IsDefault {
			defaults++
		}
	}
	if defaults != 1 {
		t.Fatalf("expected one default notion target, got %d in %+v", defaults, targets)
	}
}

func (s syncBindingContractSuite) TestSyncBindingContractDefaultTargetDoesNotUseUpdatedAtFallback(t *testing.T) {
	store := s.factory(t)
	defer store.Close()

	ctx := scopedContractContext(t, store)
	defaultTarget := createContractTarget(t, ctx, store, "stable-default", "obsidian", "Stable Default", true)
	latestTarget := createContractTarget(t, ctx, store, "latest-non-default", "obsidian", "Latest Non Default", true)
	defaultTarget.IsDefault = true
	if err := store.Sync().SaveTarget(ctx, defaultTarget); err != nil {
		t.Fatalf("set default target: %v", err)
	}
	latestTarget.Name = "Latest Non Default Updated"
	if err := store.Sync().SaveTarget(ctx, latestTarget); err != nil {
		t.Fatalf("update non-default target: %v", err)
	}
	got, err := store.Sync().GetDefaultTarget(ctx, "obsidian")
	if err != nil {
		t.Fatalf("get default target: %v", err)
	}
	if got.ID != defaultTarget.ID {
		t.Fatalf("expected explicit default %q, got %+v", defaultTarget.ID, got)
	}

	defaultTarget.IsDefault = false
	if err := store.Sync().SaveTarget(ctx, defaultTarget); err != nil {
		t.Fatalf("clear default target: %v", err)
	}
	if _, err := store.Sync().GetDefaultTarget(ctx, "obsidian"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected no default target without is_default, got %v", err)
	}
}

func (s syncBindingContractSuite) TestSyncBindingContractTargetIdentityLockCounts(t *testing.T) {
	store := s.factory(t)
	defer store.Close()

	ctx := scopedContractContext(t, store)
	note := createContractNote(t, ctx, store, "Lock Counts")
	target := createContractTarget(t, ctx, store, "lock-count-target", "notion", "Lock Count Target", true)
	if err := store.Sync().PutBinding(ctx, model.NoteSyncBinding{NoteID: note.ID, TargetID: target.ID}); err != nil {
		t.Fatalf("put binding: %v", err)
	}
	if err := store.Sync().PutExternalClaim(ctx, model.SyncExternalClaim{
		ExternalKey:  "notion:lock-count",
		NoteID:       note.ID,
		TargetID:     target.ID,
		ExternalType: "notion_page",
		ExternalID:   "lock-count",
	}); err != nil {
		t.Fatalf("put claim: %v", err)
	}
	if err := store.Sync().UpsertState(ctx, &model.SyncState{
		NoteID:        note.ID,
		TargetID:      target.ID,
		ExternalID:    "lock-count",
		ContentHash:   "hash",
		LastDirection: "push",
		Status:        "synced",
	}); err != nil {
		t.Fatalf("upsert state: %v", err)
	}
	bindings, err := store.Sync().CountBindingsByTarget(ctx, target.ID)
	if err != nil {
		t.Fatalf("count bindings: %v", err)
	}
	claims, err := store.Sync().CountClaimsByTarget(ctx, target.ID)
	if err != nil {
		t.Fatalf("count claims: %v", err)
	}
	states, err := store.Sync().CountStatesByTarget(ctx, target.ID)
	if err != nil {
		t.Fatalf("count states: %v", err)
	}
	if bindings != 1 || claims != 1 || states != 1 {
		t.Fatalf("unexpected counts: bindings=%d claims=%d states=%d", bindings, claims, states)
	}
}

func (s syncBindingContractSuite) TestSyncBindingContractLockTargetReturnsTarget(t *testing.T) {
	store := s.factory(t)
	defer store.Close()

	ctx := scopedContractContext(t, store)
	target := createContractTarget(t, ctx, store, "lock-target", "obsidian", "Lock Target", true)
	err := store.Transact(ctx, func(txStore storage.Store) error {
		locked, err := txStore.Sync().LockTarget(ctx, target.ID)
		if err != nil {
			return err
		}
		if locked.ID != target.ID {
			t.Fatalf("locked target id = %q, want %q", locked.ID, target.ID)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("lock target: %v", err)
	}

	err = store.Transact(ctx, func(txStore storage.Store) error {
		_, err := txStore.Sync().LockTarget(ctx, "missing-target")
		return err
	})
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected missing lock to return sql.ErrNoRows, got %v", err)
	}
}

func (s syncBindingContractSuite) TestSyncBindingContractTargetDeleteRestrictedByBinding(t *testing.T) {
	store := s.factory(t)
	defer store.Close()

	ctx := scopedContractContext(t, store)
	note := createContractNote(t, ctx, store, "Target Restrict")
	target := createContractTarget(t, ctx, store, "restrict-target", "notion", "Restrict Target", true)
	if err := store.Sync().PutBinding(ctx, model.NoteSyncBinding{NoteID: note.ID, TargetID: target.ID}); err != nil {
		t.Fatalf("put binding: %v", err)
	}

	if err := store.Sync().DeleteTarget(ctx, target.ID); err == nil {
		t.Fatal("expected target delete to be restricted while binding exists")
	}
	if _, err := store.Sync().GetTarget(ctx, target.ID); err != nil {
		t.Fatalf("target should remain after restricted delete: %v", err)
	}
	if binding, err := store.Sync().GetBinding(ctx, note.ID); err != nil || binding.TargetID != target.ID {
		t.Fatalf("binding should remain after restricted delete, binding=%+v err=%v", binding, err)
	}
}

func (s syncBindingContractSuite) TestSyncBindingContractSuppressionReasonDefaults(t *testing.T) {
	store := s.factory(t)
	defer store.Close()

	ctx := scopedContractContext(t, store)
	note := createContractNote(t, ctx, store, "Suppression Reason")
	target := createContractTarget(t, ctx, store, "suppression-reason-target", "obsidian", "Suppression Reason Target", true)
	if err := store.Sync().PutSuppression(ctx, model.NoteSyncSuppression{NoteID: note.ID, TargetID: target.ID}); err != nil {
		t.Fatalf("put suppression: %v", err)
	}
	suppression, err := store.Sync().GetSuppression(ctx, note.ID, target.ID)
	if err != nil {
		t.Fatalf("get suppression: %v", err)
	}
	if suppression.Reason != "user_unbound" {
		t.Fatalf("expected default suppression reason user_unbound, got %+v", suppression)
	}
}

func (s syncBindingContractSuite) TestSyncBindingContractTombstoneReasonDefaults(t *testing.T) {
	store := s.factory(t)
	defer store.Close()

	ctx := scopedContractContext(t, store)
	note := createContractNote(t, ctx, store, "Tombstone Reason")
	target := createContractTarget(t, ctx, store, "tombstone-reason-target", "obsidian", "Tombstone Reason Target", true)
	if err := store.Sync().PutImportTombstone(ctx, model.SyncImportTombstone{
		ExternalKey:  "obsidian:/vault/default-reason.md",
		TargetID:     target.ID,
		FormerNoteID: note.ID,
		ExternalType: "obsidian_file",
		ExternalPath: "/vault/default-reason.md",
	}); err != nil {
		t.Fatalf("put tombstone: %v", err)
	}
	tombstone, err := store.Sync().FindImportTombstone(ctx, target.ID, "obsidian:/vault/default-reason.md", "", "obsidian_file")
	if err != nil {
		t.Fatalf("find tombstone: %v", err)
	}
	if tombstone.Reason != "user_unbound" {
		t.Fatalf("expected default tombstone reason user_unbound, got %+v", tombstone)
	}
}

func createContractNote(t *testing.T, ctx context.Context, store storage.Store, title string) *model.Note {
	t.Helper()

	note, err := store.Notes().Create(ctx, &model.CreateNoteRequest{
		Title:    title,
		Body:     "Body",
		FolderID: "__uncategorized",
		Tags:     `[]`,
	})
	if err != nil {
		t.Fatalf("create note: %v", err)
	}
	return note
}

func createContractTarget(t *testing.T, ctx context.Context, store storage.Store, id, syncType, name string, enabled bool) *model.SyncTarget {
	t.Helper()

	target := &model.SyncTarget{
		ID:         id,
		Type:       syncType,
		Name:       name,
		VaultPath:  "/vault",
		BaseFolder: "notes",
		ConfigJSON: `{}`,
		Enabled:    enabled,
	}
	if err := store.Sync().SaveTarget(ctx, target); err != nil {
		t.Fatalf("save target: %v", err)
	}
	return target
}
