package contracttest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/provisioning"
	"github.com/hujinrun/flowspace/internal/service"
	"github.com/hujinrun/flowspace/internal/storage"
)

func RunWorkspaceIsolationSuite(t *testing.T, factory StoreFactory) {
	t.Helper()

	t.Run("WorkspaceIsolationNotesAndTasksAreScoped", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctxA := seedWorkspaceDefaults(t, store, "workspace_iso_a")
		finalizeAuthSchemaIfSupported(t, store, ctxA)
		ctxB := seedWorkspaceDefaults(t, store, "workspace_iso_b")

		note, err := store.Notes().Create(ctxA, &model.CreateNoteRequest{
			Title:    "workspace A note",
			Body:     "only A can see this",
			FolderID: "__uncategorized",
		})
		if err != nil {
			t.Fatalf("create note in workspace A: %v", err)
		}
		task := &model.Task{Title: "workspace A task", Content: "only A can see this", Status: "open", Horizon: "week", Scope: "daily"}
		if err := store.Tasks().Create(ctxA, task); err != nil {
			t.Fatalf("create task in workspace A: %v", err)
		}

		if _, err := store.Notes().GetByID(ctxB, note.ID); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("workspace B note lookup got %v, want sql.ErrNoRows", err)
		}
		bNotes, bNoteTotal, err := store.Notes().List(ctxB, storage.NoteFilter{Page: 1, PageSize: 20})
		if err != nil {
			t.Fatalf("list notes in workspace B: %v", err)
		}
		if bNoteTotal != 0 || len(bNotes) != 0 {
			t.Fatalf("workspace B saw workspace A notes: total=%d notes=%+v", bNoteTotal, bNotes)
		}
		if _, err := store.Tasks().GetByID(ctxB, task.ID); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("workspace B task lookup got %v, want sql.ErrNoRows", err)
		}
		bTasks, bTaskTotal, err := store.Tasks().List(ctxB, storage.TaskFilter{Page: 1, PageSize: 20})
		if err != nil {
			t.Fatalf("list tasks in workspace B: %v", err)
		}
		if bTaskTotal != 0 || len(bTasks) != 0 {
			t.Fatalf("workspace B saw workspace A tasks: total=%d tasks=%+v", bTaskTotal, bTasks)
		}
	})

	t.Run("WorkspaceIsolationMissingWorkspaceScopeFails", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		_, _, err := store.Notes().List(context.Background(), storage.NoteFilter{Page: 1, PageSize: 10})
		if !errors.Is(err, auth.ErrMissingWorkspace) {
			t.Fatalf("notes list missing workspace err=%v, want %v", err, auth.ErrMissingWorkspace)
		}
	})

	t.Run("WorkspaceIsolationInboxConversionWritesTypedConvertedTo", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctx := seedWorkspaceDefaults(t, store, "workspace_iso_conversion")
		item := &model.InboxItem{Kind: "note", Title: "Convert me"}
		if err := store.Inbox().Create(ctx, item); err != nil {
			t.Fatalf("create inbox item: %v", err)
		}
		converted, err := service.ConvertInboxItem(ctx, store, item.ID, &model.ConvertInboxRequest{Kind: "note"})
		if err != nil {
			t.Fatalf("convert inbox item: %v", err)
		}
		note, ok := converted.(*model.Note)
		if !ok {
			t.Fatalf("converted item type = %T, want *model.Note", converted)
		}
		got, err := store.Inbox().GetByID(ctx, item.ID)
		if err != nil {
			t.Fatalf("get converted inbox item: %v", err)
		}
		if got.ConvertedTo == nil || *got.ConvertedTo != "note:"+note.ID {
			t.Fatalf("converted_to=%v, want note:%s", got.ConvertedTo, note.ID)
		}
	})
}

func seedWorkspaceDefaults(t *testing.T, store storage.Store, workspaceID string) context.Context {
	t.Helper()

	now := time.Now().Unix()
	userID := workspaceID + "_owner"
	ctx := auth.ContextWithWorkspaceScope(context.Background(), workspaceID)
	if err := store.Transact(ctx, func(tx storage.Store) error {
		if err := tx.Auth().CreateUser(context.Background(), &model.User{
			ID:                 userID,
			Email:              fmt.Sprintf("%s@example.com", workspaceID),
			DisplayName:        workspaceID,
			PasswordHash:       "hash",
			MustChangePassword: false,
			DefaultWorkspaceID: workspaceID,
			Role:               "admin",
			Status:             "active",
			CreatedAt:          now,
			UpdatedAt:          now,
		}); err != nil {
			return fmt.Errorf("create workspace user %s: %w", userID, err)
		}
		if err := tx.Auth().CreateWorkspace(context.Background(), &model.Workspace{
			ID:          workspaceID,
			Name:        workspaceID,
			OwnerUserID: userID,
			CreatedAt:   now,
			UpdatedAt:   now,
		}); err != nil {
			return fmt.Errorf("create workspace %s: %w", workspaceID, err)
		}
		if err := tx.Auth().AddWorkspaceMember(context.Background(), workspaceID, userID, "owner"); err != nil {
			return fmt.Errorf("add workspace member %s: %w", workspaceID, err)
		}
		return provisioning.EnsureDefaultWorkspaceData(ctx, tx)
	}); err != nil {
		t.Fatalf("seed workspace %s: %v", workspaceID, err)
	}
	return ctx
}

type authSchemaFinalizer interface {
	FinalizeAuthSchema(context.Context) error
}

func finalizeAuthSchemaIfSupported(t *testing.T, store storage.Store, ctx context.Context) {
	t.Helper()

	finalizer, ok := store.(authSchemaFinalizer)
	if !ok {
		return
	}
	if err := finalizer.FinalizeAuthSchema(ctx); err != nil {
		t.Fatalf("finalize auth schema: %v", err)
	}
}

func scopedContractContext(t *testing.T, store storage.Store) context.Context {
	t.Helper()
	return seedWorkspaceDefaults(t, store, contractWorkspaceID(t))
}

func contractWorkspaceID(t *testing.T) string {
	t.Helper()
	workspaceID := "contract_" + regexp.MustCompile(`[^a-zA-Z0-9_]+`).ReplaceAllString(t.Name(), "_")
	if len(workspaceID) > 120 {
		workspaceID = workspaceID[:120]
	}
	return workspaceID
}
