package contracttest

import (
	"context"
	"database/sql"
	"testing"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

func RunNoteSearchSuite(t *testing.T, factory StoreFactory) {
	t.Helper()

	t.Run("NotesPreserveTagsAndSearchImportedNote", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctx := scopedContractContext(t, store)
		note := &model.Note{
			ID:       "contract-imported-note",
			Title:    "Imported Notion Note",
			Body:     "This note came from Notion sync.",
			FolderID: "__uncategorized",
			Tags:     `["sync","publish"]`,
		}
		if err := store.Notes().CreateWithID(ctx, note); err != nil {
			t.Fatalf("create note with id: %v", err)
		}

		got, err := store.Notes().GetByID(ctx, note.ID)
		if err != nil {
			t.Fatalf("get note by id: %v", err)
		}
		if got.ID != "contract-imported-note" {
			t.Fatalf("expected caller-provided id, got %+v", got)
		}
		if got.Tags != `["sync","publish"]` {
			t.Fatalf("expected tags to round trip, got %s", got.Tags)
		}

		results, total, err := searchStore(ctx, store, "Notion", 1, 10)
		if err != nil {
			t.Fatalf("search imported note: %v", err)
		}
		if total != 1 || !hasSearchResult(results, "note", note.ID) {
			t.Fatalf("expected imported note search result, total=%d results=%+v", total, results)
		}
	})

	t.Run("NoteSearchIndexTracksUpdateAndDelete", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctx := scopedContractContext(t, store)
		note := &model.Note{
			ID:       "contract-search-lifecycle-note",
			Title:    "old searchable note",
			Body:     "stable body",
			FolderID: "__uncategorized",
			Tags:     `[]`,
		}
		if err := store.Notes().CreateWithID(ctx, note); err != nil {
			t.Fatalf("create note: %v", err)
		}

		title := "new searchable note"
		updated, err := store.Notes().Update(ctx, note.ID, &model.UpdateNoteRequest{Title: &title})
		if err != nil {
			t.Fatalf("update note: %v", err)
		}
		if updated.Title != title || updated.Body != "stable body" {
			t.Fatalf("unexpected updated note: %+v", updated)
		}

		oldResults, oldTotal, err := searchStore(ctx, store, "old searchable", 1, 10)
		if err != nil {
			t.Fatalf("search old note title: %v", err)
		}
		if oldTotal != 0 || len(oldResults) != 0 {
			t.Fatalf("expected old note title to disappear, total=%d results=%+v", oldTotal, oldResults)
		}

		newResults, newTotal, err := searchStore(ctx, store, "new searchable", 1, 10)
		if err != nil {
			t.Fatalf("search new note title: %v", err)
		}
		if newTotal != 1 || !hasSearchResult(newResults, "note", note.ID) {
			t.Fatalf("expected new note title to appear, total=%d results=%+v", newTotal, newResults)
		}

		if err := store.Notes().Delete(ctx, note.ID); err != nil {
			t.Fatalf("delete note: %v", err)
		}
		deletedResults, deletedTotal, err := searchStore(ctx, store, "new searchable", 1, 10)
		if err != nil {
			t.Fatalf("search deleted note: %v", err)
		}
		if deletedTotal != 0 || len(deletedResults) != 0 {
			t.Fatalf("expected deleted note to disappear, total=%d results=%+v", deletedTotal, deletedResults)
		}
		if _, err := store.Notes().GetByID(ctx, note.ID); err == nil {
			t.Fatal("expected deleted note lookup to fail")
		} else if err != sql.ErrNoRows {
			t.Fatalf("expected sql.ErrNoRows after delete, got %v", err)
		}
	})

	t.Run("SearchMatchesWordPrefixes", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctx := scopedContractContext(t, store)
		note := &model.Note{
			ID:       "contract-prefix-note",
			Title:    "PostgreSQL migration plan",
			Body:     "Storage provider migration details",
			FolderID: "__uncategorized",
			Tags:     `["database"]`,
		}
		if err := store.Notes().CreateWithID(ctx, note); err != nil {
			t.Fatalf("create note: %v", err)
		}

		results, total, err := searchStore(ctx, store, "Post migr", 1, 10)
		if err != nil {
			t.Fatalf("search prefix query: %v", err)
		}
		if total != 1 || !hasSearchResult(results, "note", note.ID) {
			t.Fatalf("expected prefix query to find note, total=%d results=%+v", total, results)
		}
	})

	t.Run("SearchKeepsTotalWhenPageIsPastResults", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctx := scopedContractContext(t, store)
		note := &model.Note{
			ID:       "contract-search-total-note",
			Title:    "unique pagination query",
			Body:     "stable body",
			FolderID: "__uncategorized",
			Tags:     `[]`,
		}
		if err := store.Notes().CreateWithID(ctx, note); err != nil {
			t.Fatalf("create note: %v", err)
		}

		results, total, err := searchStore(ctx, store, "unique pagination", 2, 1)
		if err != nil {
			t.Fatalf("search second page: %v", err)
		}
		if total != 1 || len(results) != 0 {
			t.Fatalf("expected total to survive empty page, total=%d results=%+v", total, results)
		}
	})

	t.Run("SearchMatchesTagsCaseInsensitively", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctx := scopedContractContext(t, store)
		note := &model.Note{
			ID:       "contract-tag-search-note",
			Title:    "Tag search note",
			Body:     "stable body",
			FolderID: "__uncategorized",
			Tags:     `["Database"]`,
		}
		if err := store.Notes().CreateWithID(ctx, note); err != nil {
			t.Fatalf("create note: %v", err)
		}

		results, total, err := searchStore(ctx, store, "#database", 1, 10)
		if err != nil {
			t.Fatalf("search tag: %v", err)
		}
		if total != 1 || !hasSearchResult(results, "note", note.ID) {
			t.Fatalf("expected tag query to find note, total=%d results=%+v", total, results)
		}
	})

	t.Run("SearchDoesNotReturnOtherWorkspaceResults", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctxA := seedWorkspaceDefaults(t, store, "workspace_search_a")
		finalizeAuthSchemaIfSupported(t, store, ctxA)
		ctxB := seedWorkspaceDefaults(t, store, "workspace_search_b")

		if _, err := store.Notes().Create(ctxA, &model.CreateNoteRequest{
			Title:    "private phrase alpha",
			Body:     "workspace A only",
			FolderID: "__uncategorized",
		}); err != nil {
			t.Fatalf("create note: %v", err)
		}

		results, total, err := searchStore(ctxB, store, "private phrase alpha", 1, 20)
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		if total != 0 || len(results) != 0 {
			t.Fatalf("workspace B saw workspace A search results: total=%d results=%+v", total, results)
		}
	})
}

type searchRepository interface {
	Search(context.Context, string, int, int) ([]model.SearchResult, int, error)
}

func searchStore(ctx context.Context, store storage.Store, query string, page, pageSize int) ([]model.SearchResult, int, error) {
	return store.Search().Search(ctx, query, page, pageSize)
}

func hasSearchResult(results []model.SearchResult, resultType, id string) bool {
	for _, result := range results {
		if result.Type == resultType && result.ID == id {
			return true
		}
	}
	return false
}

// RunNoteProjectLinksSuite runs contract tests for note-project many-to-many links.
func RunNoteProjectLinksSuite(t *testing.T, factory StoreFactory) {
	t.Helper()

	t.Run("CreateNoteWithProjects", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		ctx := scopedContractContext(t, store)

		// Create a test project first
		proj, err := store.Tasks().CreateProject(ctx, &model.CreateTaskProjectRequest{
			Name: "Contract Test Project",
			Type: "regular",
		})
		if err != nil {
			t.Fatalf("create project: %v", err)
		}

		// Create note with project
		note, err := store.Notes().Create(ctx, &model.CreateNoteRequest{
			Title:      "Note With Project",
			Body:       "test body",
			ProjectIDs: []string{proj.ID},
		})
		if err != nil {
			t.Fatalf("create note with project: %v", err)
		}
		if len(note.Projects) != 1 {
			t.Fatalf("expected 1 project, got %d", len(note.Projects))
		}
		if note.Projects[0].ID != proj.ID {
			t.Fatalf("expected project ID %s, got %s", proj.ID, note.Projects[0].ID)
		}
	})

	t.Run("GetByIDReturnsProjects", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		ctx := scopedContractContext(t, store)

		proj, _ := store.Tasks().CreateProject(ctx, &model.CreateTaskProjectRequest{
			Name: "GetByID Project", Type: "regular",
		})
		note, _ := store.Notes().Create(ctx, &model.CreateNoteRequest{
			Title: "GetByID Note", ProjectIDs: []string{proj.ID},
		})

		got, err := store.Notes().GetByID(ctx, note.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if len(got.Projects) == 0 {
			t.Fatal("expected projects in GetByID response")
		}
		if got.Projects[0].ID != proj.ID {
			t.Fatalf("expected project %s, got %s", proj.ID, got.Projects[0].ID)
		}
	})

	t.Run("UpdatePreservesProjectsWhenNil", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		ctx := scopedContractContext(t, store)

		proj, _ := store.Tasks().CreateProject(ctx, &model.CreateTaskProjectRequest{
			Name: "Preserve Project", Type: "regular",
		})
		note, _ := store.Notes().Create(ctx, &model.CreateNoteRequest{
			Title: "Preserve Note", ProjectIDs: []string{proj.ID},
		})

		// Update only title, omit project_ids (nil)
		newTitle := "Updated Title"
		updated, err := store.Notes().Update(ctx, note.ID, &model.UpdateNoteRequest{
			Title: &newTitle,
		})
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if len(updated.Projects) != 1 {
			t.Fatalf("expected projects preserved, got %d", len(updated.Projects))
		}
	})

	t.Run("UpdateClearsProjectsWithEmptySlice", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		ctx := scopedContractContext(t, store)

		proj, _ := store.Tasks().CreateProject(ctx, &model.CreateTaskProjectRequest{
			Name: "Clear Project", Type: "regular",
		})
		note, _ := store.Notes().Create(ctx, &model.CreateNoteRequest{
			Title: "Clear Note", ProjectIDs: []string{proj.ID},
		})

		empty := []string{}
		updated, err := store.Notes().Update(ctx, note.ID, &model.UpdateNoteRequest{
			ProjectIDs: &empty,
		})
		if err != nil {
			t.Fatalf("update clear: %v", err)
		}
		if len(updated.Projects) != 0 {
			t.Fatalf("expected 0 projects after clear, got %d", len(updated.Projects))
		}
	})

	t.Run("UpdateReplacesProjects", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		ctx := scopedContractContext(t, store)

		projA, _ := store.Tasks().CreateProject(ctx, &model.CreateTaskProjectRequest{
			Name: "Replace Project A", Type: "regular",
		})
		projB, _ := store.Tasks().CreateProject(ctx, &model.CreateTaskProjectRequest{
			Name: "Replace Project B", Type: "regular",
		})
		note, _ := store.Notes().Create(ctx, &model.CreateNoteRequest{
			Title: "Replace Note", ProjectIDs: []string{projA.ID},
		})

		// Replace A with B
		updated, err := store.Notes().Update(ctx, note.ID, &model.UpdateNoteRequest{
			ProjectIDs: &[]string{projB.ID},
		})
		if err != nil {
			t.Fatalf("update replace: %v", err)
		}
		if len(updated.Projects) != 1 || updated.Projects[0].ID != projB.ID {
			t.Fatalf("expected project B, got %+v", updated.Projects)
		}
	})

	t.Run("ListByProjectID", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		ctx := scopedContractContext(t, store)

		proj, _ := store.Tasks().CreateProject(ctx, &model.CreateTaskProjectRequest{
			Name: "List Project", Type: "regular",
		})
		note, _ := store.Notes().Create(ctx, &model.CreateNoteRequest{
			Title: "In Project", ProjectIDs: []string{proj.ID},
		})
		// Create another note NOT in the project
		store.Notes().Create(ctx, &model.CreateNoteRequest{
			Title: "Not In Project",
		})

		notes, total, err := store.Notes().List(ctx, storage.NoteFilter{
			ProjectID: proj.ID,
			Sort:      "recent",
			Page:      1,
			PageSize:  20,
		})
		if err != nil {
			t.Fatalf("list by project: %v", err)
		}
		if total != 1 {
			t.Fatalf("expected 1 note in project, got total=%d", total)
		}
		if notes[0].ID != note.ID {
			t.Fatalf("expected note %s, got %s", note.ID, notes[0].ID)
		}
	})

	t.Run("ListUnassigned", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		ctx := scopedContractContext(t, store)

		// Create note WITHOUT project
		note, _ := store.Notes().Create(ctx, &model.CreateNoteRequest{
			Title: "Unassigned Note",
		})
		// Create note WITH project
		proj, _ := store.Tasks().CreateProject(ctx, &model.CreateTaskProjectRequest{
			Name: "Assigned Project", Type: "regular",
		})
		store.Notes().Create(ctx, &model.CreateNoteRequest{
			Title: "Assigned Note", ProjectIDs: []string{proj.ID},
		})

		notes, total, err := store.Notes().List(ctx, storage.NoteFilter{
			Unassigned: true,
			Sort:       "recent",
			Page:       1,
			PageSize:   20,
		})
		if err != nil {
			t.Fatalf("list unassigned: %v", err)
		}
		// Should find at least the unassigned note, and NOT the assigned note
		found := false
		for _, n := range notes {
			if n.ID == note.ID {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected unassigned note in results, got total=%d notes=%+v", total, notes)
		}
		if total < 1 {
			t.Fatalf("expected at least 1 unassigned note, got %d", total)
		}
	})

	t.Run("DeleteNoteCleansLinks", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		ctx := scopedContractContext(t, store)

		proj, _ := store.Tasks().CreateProject(ctx, &model.CreateTaskProjectRequest{
			Name: "Delete Project", Type: "regular",
		})
		note, _ := store.Notes().Create(ctx, &model.CreateNoteRequest{
			Title: "To Delete", ProjectIDs: []string{proj.ID},
		})

		// Delete the note
		if err := store.Notes().Delete(ctx, note.ID); err != nil {
			t.Fatalf("delete note: %v", err)
		}
		// Verify note is gone
		_, err := store.Notes().GetByID(ctx, note.ID)
		if err == nil {
			t.Fatal("expected note to be deleted")
		}
	})

	t.Run("DeleteProjectPreservesNote", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		ctx := scopedContractContext(t, store)

		proj, _ := store.Tasks().CreateProject(ctx, &model.CreateTaskProjectRequest{
			Name: "To Delete Project", Type: "regular",
		})
		note, _ := store.Notes().Create(ctx, &model.CreateNoteRequest{
			Title: "Surviving Note", ProjectIDs: []string{proj.ID},
		})

		// Delete the project
		if err := store.Tasks().DeleteProject(ctx, proj.ID); err != nil {
			t.Fatalf("delete project: %v", err)
		}
		// Note should still exist
		got, err := store.Notes().GetByID(ctx, note.ID)
		if err != nil {
			t.Fatalf("note should survive project deletion: %v", err)
		}
		if len(got.Projects) != 0 {
			t.Fatalf("expected 0 projects after project deletion, got %d", len(got.Projects))
		}
	})

	t.Run("InvalidProjectIDRejected", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		ctx := scopedContractContext(t, store)

		// Create note with non-existent project ID 閳?should fail with constraint error
		_, err := store.Notes().Create(ctx, &model.CreateNoteRequest{
			Title:      "Invalid Project Note",
			ProjectIDs: []string{"nonexistent-project-id"},
		})
		if err == nil {
			t.Fatal("expected error when creating note with non-existent project ID")
		}
	})

	t.Run("DuplicateProjectIDsHandled", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		ctx := scopedContractContext(t, store)

		proj, _ := store.Tasks().CreateProject(ctx, &model.CreateTaskProjectRequest{
			Name: "Dedup Project", Type: "regular",
		})
		note, err := store.Notes().Create(ctx, &model.CreateNoteRequest{
			Title:      "Dedup Note",
			ProjectIDs: []string{proj.ID, proj.ID}, // duplicate
		})
		if err != nil {
			t.Fatalf("create with duplicate project IDs: %v", err)
		}
		if len(note.Projects) != 1 {
			t.Fatalf("expected 1 project after dedup, got %d", len(note.Projects))
		}
	})

	t.Run("RecentReturnsProjects", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		ctx := scopedContractContext(t, store)

		proj, _ := store.Tasks().CreateProject(ctx, &model.CreateTaskProjectRequest{
			Name: "Recent Project", Type: "regular",
		})
		store.Notes().Create(ctx, &model.CreateNoteRequest{
			Title: "Recent Note", ProjectIDs: []string{proj.ID},
		})

		notes, err := store.Notes().Recent(ctx, 10)
		if err != nil {
			t.Fatalf("recent: %v", err)
		}
		found := false
		for _, n := range notes {
			if len(n.Projects) > 0 {
				found = true
				break
			}
		}
		if !found {
			t.Fatal("expected at least one note with projects in Recent")
		}
	})

	t.Run("ListAllDoesNotRequireProjects", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		ctx := scopedContractContext(t, store)

		proj, _ := store.Tasks().CreateProject(ctx, &model.CreateTaskProjectRequest{
			Name: "ListAll Project", Type: "regular",
		})
		store.Notes().Create(ctx, &model.CreateNoteRequest{
			Title: "ListAll Note", ProjectIDs: []string{proj.ID},
		})

		notes, err := store.Notes().ListAll(ctx)
		if err != nil {
			t.Fatalf("ListAll: %v", err)
		}
		if len(notes) == 0 {
			t.Fatal("expected at least one note from ListAll")
		}
		// Projects may or may not be populated 閳?that's fine for ListAll
	})
}
