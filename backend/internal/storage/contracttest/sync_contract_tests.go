package contracttest

import (
	"context"
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

		ctx := context.Background()
		target := &model.SyncTarget{
			ID:         "sync-target-contract",
			Type:       "notion",
			Name:       "Notion Workspace",
			ConfigJSON: `{"data_source_id":"abc","token_env":"NOTION_TOKEN","required_tags":["sync"]}`,
			Enabled:    true,
			AutoSync:   false,
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

	t.Run("SyncTargetRejectsNonObjectConfig", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctx := context.Background()
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
}
