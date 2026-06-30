package contracttest

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/hujinrun/flowspace/internal/model"
)

func RunSyncSuite(t *testing.T, factory StoreFactory) {
	t.Helper()

	t.Run("SyncTargetAndStateLifecycle", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctx := scopedContractContext(t, store)
		target := &model.SyncTarget{
			ID:         "sync-target-contract",
			Type:       "notion",
			Name:       "Notion Workspace",
			ConfigJSON: `{"data_source_id":"abc","token_env":"NOTION_TOKEN","required_tags":["sync"]}`,
			Enabled:    true,
			AutoSync:   false,
			IsDefault:  true,
		}
		if err := store.Sync().SaveTarget(ctx, target); err != nil {
			t.Fatalf("save target: %v", err)
		}
		if target.CreatedAt == 0 || target.UpdatedAt == 0 {
			t.Fatalf("expected target timestamps, got %+v", target)
		}

		defaultTarget, err := store.Sync().GetDefaultTarget(ctx, "notion")
		if err != nil {
			t.Fatalf("get default target: %v", err)
		}
		if defaultTarget.ID != target.ID || defaultTarget.ConfigJSON == "" || !defaultTarget.Enabled {
			t.Fatalf("unexpected default target: %+v", defaultTarget)
		}

		updatedName := "Notion Workspace Updated"
		target.Name = updatedName
		target.AutoSync = true
		if err := store.Sync().SaveTarget(ctx, target); err != nil {
			t.Fatalf("update target: %v", err)
		}
		targets, err := store.Sync().ListTargets(ctx)
		if err != nil {
			t.Fatalf("list targets: %v", err)
		}
		if len(targets) != 1 || targets[0].Name != updatedName || !targets[0].AutoSync {
			t.Fatalf("unexpected targets: %+v", targets)
		}

		note, err := store.Notes().Create(ctx, &model.CreateNoteRequest{
			Title:    "Sync State Note",
			Body:     "Body",
			FolderID: "__uncategorized",
			Tags:     `["sync"]`,
		})
		if err != nil {
			t.Fatalf("create note: %v", err)
		}
		syncedAt := int64(1800000000)
		externalMTime := int64(1800000100)
		errorMessage := "previous error"
		state := &model.SyncState{
			NoteID:        note.ID,
			TargetID:      target.ID,
			ExternalPath:  "FlowSpace Notes/Sync State Note.md",
			ExternalID:    "page-1",
			ExternalURL:   "https://notion.so/page-1",
			ContentHash:   "local-hash",
			ExternalHash:  "remote-hash",
			ExternalMTime: &externalMTime,
			LastDirection: "push",
			LastSyncedAt:  &syncedAt,
			Status:        "failed",
			ErrorMessage:  &errorMessage,
		}
		if err := store.Sync().UpsertState(ctx, state); err != nil {
			t.Fatalf("upsert state: %v", err)
		}

		loaded, err := store.Sync().GetState(ctx, note.ID, target.ID)
		if err != nil {
			t.Fatalf("get state: %v", err)
		}
		if loaded.ExternalID != "page-1" || loaded.ExternalURL != "https://notion.so/page-1" || loaded.ExternalHash != "remote-hash" || loaded.LastDirection != "push" {
			t.Fatalf("unexpected loaded state: %+v", loaded)
		}
		if loaded.ExternalMTime == nil || *loaded.ExternalMTime != externalMTime || loaded.LastSyncedAt == nil || *loaded.LastSyncedAt != syncedAt {
			t.Fatalf("unexpected time fields: %+v", loaded)
		}
		if loaded.ErrorMessage == nil || *loaded.ErrorMessage != errorMessage {
			t.Fatalf("unexpected error message: %+v", loaded)
		}

		state.Status = "external_deleted"
		state.ErrorMessage = nil
		state.LastDirection = "delete"
		if err := store.Sync().UpsertState(ctx, state); err != nil {
			t.Fatalf("update state: %v", err)
		}
		states, err := store.Sync().ListStatesByTarget(ctx, target.ID)
		if err != nil {
			t.Fatalf("list states: %v", err)
		}
		if len(states) != 1 || states[0].Status != "external_deleted" || states[0].LastDirection != "delete" || states[0].ErrorMessage != nil {
			t.Fatalf("unexpected states: %+v", states)
		}

		deleted, err := store.Sync().ListExternalDeletedStates(ctx, target.ID)
		if err != nil {
			t.Fatalf("list external deleted: %v", err)
		}
		if len(deleted) != 1 || deleted[0].NoteID != note.ID || deleted[0].Title != "Sync State Note" || deleted[0].LastSyncedAt == nil {
			t.Fatalf("unexpected external deleted list: %+v", deleted)
		}

		if err := store.Sync().DeleteState(ctx, note.ID, target.ID); err != nil {
			t.Fatalf("delete state: %v", err)
		}
		if _, err := store.Sync().GetState(ctx, note.ID, target.ID); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("expected deleted state to be missing, got %v", err)
		}
	})

	t.Run("SyncDataDoesNotCrossWorkspaces", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctxA := seedWorkspaceDefaults(t, store, "workspace_sync_a")
		finalizeAuthSchemaIfSupported(t, store, ctxA)
		ctxB := seedWorkspaceDefaults(t, store, "workspace_sync_b")

		targetA := &model.SyncTarget{
			ID:         "sync-target-workspace-a",
			Type:       "notion",
			Name:       "Shared Notion",
			ConfigJSON: `{"data_source_id":"a"}`,
			Enabled:    true,
			IsDefault:  true,
		}
		if err := store.Sync().SaveTarget(ctxA, targetA); err != nil {
			t.Fatalf("save target A: %v", err)
		}
		targetB := &model.SyncTarget{
			ID:         "sync-target-workspace-b",
			Type:       "notion",
			Name:       "Shared Notion",
			ConfigJSON: `{"data_source_id":"b"}`,
			Enabled:    true,
			IsDefault:  true,
		}
		if err := store.Sync().SaveTarget(ctxB, targetB); err != nil {
			t.Fatalf("save target B: %v", err)
		}

		defaultA, err := store.Sync().GetDefaultTarget(ctxA, "notion")
		if err != nil {
			t.Fatalf("get workspace A default target: %v", err)
		}
		if defaultA.ID != targetA.ID {
			t.Fatalf("workspace A default target = %+v, want %s", defaultA, targetA.ID)
		}
		defaultB, err := store.Sync().GetDefaultTarget(ctxB, "notion")
		if err != nil {
			t.Fatalf("get workspace B default target: %v", err)
		}
		if defaultB.ID != targetB.ID {
			t.Fatalf("workspace B default target = %+v, want %s", defaultB, targetB.ID)
		}
		targetsB, err := store.Sync().ListTargets(ctxB)
		if err != nil {
			t.Fatalf("list workspace B targets: %v", err)
		}
		if len(targetsB) != 1 || targetsB[0].ID != targetB.ID {
			t.Fatalf("workspace B targets = %+v, want only %s", targetsB, targetB.ID)
		}
		if _, err := store.Sync().GetTarget(ctxB, targetA.ID); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("workspace B target A lookup err=%v, want sql.ErrNoRows", err)
		}

		noteB, err := store.Notes().Create(ctxB, &model.CreateNoteRequest{
			Title:    "Workspace B Sync Note",
			Body:     "Body",
			FolderID: "__uncategorized",
		})
		if err != nil {
			t.Fatalf("create workspace B note: %v", err)
		}
		if err := store.Sync().PutBinding(ctxB, model.NoteSyncBinding{NoteID: noteB.ID, TargetID: targetB.ID}); err != nil {
			t.Fatalf("put workspace B binding: %v", err)
		}

		noteA, err := store.Notes().Create(ctxA, &model.CreateNoteRequest{
			Title:    "Workspace A Sync Note",
			Body:     "Body",
			FolderID: "__uncategorized",
		})
		if err != nil {
			t.Fatalf("create workspace A note: %v", err)
		}
		if err := store.Sync().PutBinding(ctxA, model.NoteSyncBinding{NoteID: noteA.ID, TargetID: targetA.ID}); err != nil {
			t.Fatalf("put workspace A binding: %v", err)
		}
		if err := store.Sync().PutExternalClaim(ctxA, model.SyncExternalClaim{
			ExternalKey:  "notion:shared-page",
			NoteID:       noteA.ID,
			TargetID:     targetA.ID,
			ExternalType: "notion_page",
			ExternalID:   "shared-page",
		}); err != nil {
			t.Fatalf("put workspace A claim: %v", err)
		}
		if err := store.Sync().PutExternalClaim(ctxB, model.SyncExternalClaim{
			ExternalKey:  "notion:shared-page",
			NoteID:       noteB.ID,
			TargetID:     targetB.ID,
			ExternalType: "notion_page",
			ExternalID:   "shared-page",
		}); err != nil {
			t.Fatalf("put workspace B claim with same external key: %v", err)
		}
		if err := store.Sync().UpsertState(ctxA, &model.SyncState{
			NoteID:        noteA.ID,
			TargetID:      targetA.ID,
			ExternalID:    "workspace-a-page",
			ContentHash:   "hash",
			LastDirection: "delete_detected",
			Status:        "external_deleted",
		}); err != nil {
			t.Fatalf("upsert workspace A state: %v", err)
		}
		if err := store.Sync().PutSuppression(ctxA, model.NoteSyncSuppression{NoteID: noteA.ID, TargetID: targetA.ID}); err != nil {
			t.Fatalf("put workspace A suppression: %v", err)
		}
		if err := store.Sync().PutImportTombstone(ctxA, model.SyncImportTombstone{
			ExternalKey:  "notion:shared-tombstone",
			TargetID:     targetA.ID,
			FormerNoteID: noteA.ID,
			ExternalType: "notion_page",
			ExternalID:   "shared-tombstone",
		}); err != nil {
			t.Fatalf("put workspace A tombstone: %v", err)
		}
		if err := store.Sync().PutImportTombstone(ctxB, model.SyncImportTombstone{
			ExternalKey:  "notion:shared-tombstone",
			TargetID:     targetB.ID,
			FormerNoteID: noteB.ID,
			ExternalType: "notion_page",
			ExternalID:   "shared-tombstone",
		}); err != nil {
			t.Fatalf("put workspace B tombstone with same external key: %v", err)
		}

		if _, err := store.Sync().GetBinding(ctxB, noteA.ID); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("workspace B binding lookup err=%v, want sql.ErrNoRows", err)
		}
		claimA, err := store.Sync().GetExternalClaim(ctxA, "notion:shared-page")
		if err != nil {
			t.Fatalf("get workspace A shared claim: %v", err)
		}
		if claimA.NoteID != noteA.ID || claimA.TargetID != targetA.ID {
			t.Fatalf("workspace A shared claim = %+v, want note %s target %s", claimA, noteA.ID, targetA.ID)
		}
		claimB, err := store.Sync().GetExternalClaim(ctxB, "notion:shared-page")
		if err != nil {
			t.Fatalf("get workspace B shared claim: %v", err)
		}
		if claimB.NoteID != noteB.ID || claimB.TargetID != targetB.ID {
			t.Fatalf("workspace B shared claim = %+v, want note %s target %s", claimB, noteB.ID, targetB.ID)
		}
		if _, err := store.Sync().GetState(ctxB, noteA.ID, targetA.ID); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("workspace B state lookup err=%v, want sql.ErrNoRows", err)
		}
		if states, err := store.Sync().ListStatesByTarget(ctxB, targetA.ID); err != nil || len(states) != 0 {
			t.Fatalf("workspace B states for target A = %+v err=%v, want none", states, err)
		}
		if deleted, err := store.Sync().ListExternalDeletedStates(ctxB, targetA.ID); err != nil || len(deleted) != 0 {
			t.Fatalf("workspace B external deleted for target A = %+v err=%v, want none", deleted, err)
		}
		if _, err := store.Sync().GetSuppression(ctxB, noteA.ID, targetA.ID); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("workspace B suppression lookup err=%v, want sql.ErrNoRows", err)
		}
		tombstoneA, err := store.Sync().FindImportTombstone(ctxA, targetA.ID, "notion:shared-tombstone", noteA.ID, "notion_page")
		if err != nil {
			t.Fatalf("find workspace A shared tombstone: %v", err)
		}
		if tombstoneA.FormerNoteID != noteA.ID || tombstoneA.TargetID != targetA.ID {
			t.Fatalf("workspace A shared tombstone = %+v, want note %s target %s", tombstoneA, noteA.ID, targetA.ID)
		}
		tombstoneB, err := store.Sync().FindImportTombstone(ctxB, targetB.ID, "notion:shared-tombstone", noteB.ID, "notion_page")
		if err != nil {
			t.Fatalf("find workspace B shared tombstone: %v", err)
		}
		if tombstoneB.FormerNoteID != noteB.ID || tombstoneB.TargetID != targetB.ID {
			t.Fatalf("workspace B shared tombstone = %+v, want note %s target %s", tombstoneB, noteB.ID, targetB.ID)
		}
		if _, err := store.Sync().FindImportTombstone(ctxB, targetA.ID, "notion:shared-tombstone", noteA.ID, "notion_page"); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("workspace B tombstone lookup err=%v, want sql.ErrNoRows", err)
		}
	})

	t.Run("SyncTargetRejectsNonObjectConfig", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctx := scopedContractContext(t, store)
		target := &model.SyncTarget{
			Type:       "notion",
			Name:       "Invalid Config",
			ConfigJSON: `["not-an-object"]`,
			Enabled:    true,
		}
		if err := store.Sync().SaveTarget(ctx, target); err == nil {
			t.Fatal("expected non-object config to be rejected")
		}
	})

	t.Run("SyncTargetRejectsUnsupportedType", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctx := scopedContractContext(t, store)
		target := &model.SyncTarget{
			Type:       "unsupported",
			Name:       "Unsupported Target",
			ConfigJSON: `{}`,
			Enabled:    true,
		}
		if err := store.Sync().SaveTarget(ctx, target); err == nil {
			t.Fatal("expected unsupported target type to be rejected")
		}
	})
}
