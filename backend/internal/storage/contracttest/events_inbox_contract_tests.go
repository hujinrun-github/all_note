package contracttest

import (
	"context"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/model"
)

func RunEventInboxSuite(t *testing.T, factory StoreFactory) {
	t.Helper()

	t.Run("EventsUseOverlapBoundariesAndSearchLocationLifecycle", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctx := context.Background()
		localDay := time.Date(2026, 6, 16, 0, 0, 0, 0, time.Local)
		dayStart := localDay.Unix()
		dayEnd := localDay.Add(24 * time.Hour).Unix()
		location := "Old Room"
		events := []model.Event{
			{Title: "event at local 00:30", StartTime: localDay.Add(30 * time.Minute).Unix(), EndTime: localDay.Add(time.Hour).Unix(), Location: &location, Kind: "work"},
			{Title: "event at local 23:30", StartTime: localDay.Add(23*time.Hour + 30*time.Minute).Unix(), EndTime: localDay.Add(24*time.Hour - time.Minute).Unix(), Location: &location, Kind: "work"},
			{Title: "cross day event", StartTime: localDay.Add(-30 * time.Minute).Unix(), EndTime: localDay.Add(30 * time.Minute).Unix(), Location: &location, Kind: "work"},
		}
		for i := range events {
			if err := store.Events().Create(ctx, &events[i]); err != nil {
				t.Fatalf("create event %q: %v", events[i].Title, err)
			}
		}

		listed, total, err := store.Events().List(ctx, dayStart, dayEnd, 1, 20)
		if err != nil {
			t.Fatalf("list events: %v", err)
		}
		if total != 3 || len(listed) != 3 {
			t.Fatalf("expected 3 overlapping events, total=%d events=%+v", total, listed)
		}

		today, err := store.Events().Today(ctx, dayStart, dayEnd)
		if err != nil {
			t.Fatalf("today events: %v", err)
		}
		found := map[string]bool{}
		for _, event := range today {
			found[event.Title] = true
		}
		for _, title := range []string{"event at local 00:30", "event at local 23:30", "cross day event"} {
			if !found[title] {
				t.Fatalf("expected %q in today events, got %+v", title, today)
			}
		}

		newTitle := "new searchable event"
		newLocation := "New Room"
		updated, err := store.Events().Update(ctx, events[0].ID, &model.UpdateEventRequest{Title: &newTitle, Location: &newLocation})
		if err != nil {
			t.Fatalf("update event: %v", err)
		}
		if updated.Title != newTitle || updated.Location == nil || *updated.Location != newLocation {
			t.Fatalf("unexpected updated event: %+v", updated)
		}

		oldResults, oldTotal, err := searchStore(ctx, store, "Old Room", 1, 10)
		if err != nil {
			t.Fatalf("search old event location: %v", err)
		}
		if oldTotal != 2 || hasSearchResult(oldResults, "event", events[0].ID) {
			t.Fatalf("expected updated event to leave old location while other events remain, total=%d results=%+v", oldTotal, oldResults)
		}

		newResults, newTotal, err := searchStore(ctx, store, "New Room", 1, 10)
		if err != nil {
			t.Fatalf("search new event location: %v", err)
		}
		if newTotal != 1 || !hasSearchResult(newResults, "event", events[0].ID) {
			t.Fatalf("expected new event location result, total=%d results=%+v", newTotal, newResults)
		}

		if err := store.Events().Delete(ctx, events[0].ID); err != nil {
			t.Fatalf("delete event: %v", err)
		}
		deletedResults, deletedTotal, err := searchStore(ctx, store, "New Room", 1, 10)
		if err != nil {
			t.Fatalf("search deleted event: %v", err)
		}
		if deletedTotal != 0 || len(deletedResults) != 0 {
			t.Fatalf("expected deleted event to disappear, total=%d results=%+v", deletedTotal, deletedResults)
		}
	})

	t.Run("InboxBatchArchiveAndDeleteUseDynamicIDs", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctx := context.Background()
		first := &model.InboxItem{Kind: "note", Title: "first"}
		second := &model.InboxItem{Kind: "task", Title: "second"}
		third := &model.InboxItem{Kind: "note", Title: "third"}
		for _, item := range []*model.InboxItem{first, second, third} {
			if err := store.Inbox().Create(ctx, item); err != nil {
				t.Fatalf("create inbox item: %v", err)
			}
		}

		archived, err := store.Inbox().BatchArchive(ctx, []string{first.ID, second.ID})
		if err != nil {
			t.Fatalf("batch archive: %v", err)
		}
		if archived != 2 {
			t.Fatalf("expected 2 archived rows, got %d", archived)
		}

		deleted, err := store.Inbox().BatchDelete(ctx, []string{first.ID, third.ID})
		if err != nil {
			t.Fatalf("batch delete: %v", err)
		}
		if deleted != 2 {
			t.Fatalf("expected 2 deleted rows, got %d", deleted)
		}
	})
}
