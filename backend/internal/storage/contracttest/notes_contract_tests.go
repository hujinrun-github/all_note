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

		ctx := context.Background()
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

		ctx := context.Background()
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

		ctx := context.Background()
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

		ctx := context.Background()
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

		ctx := context.Background()
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
