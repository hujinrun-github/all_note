package contracttest

import (
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/model"
)

func RunEventInboxSuite(t *testing.T, factory StoreFactory) {
	t.Helper()

	t.Run("EventsUseOverlapBoundariesAndSearchLocationLifecycle", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctx := scopedContractContext(t, store)
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

		ctx := scopedContractContext(t, store)
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

	t.Run("EventsPersistProjectIdentityAndClearWithEmptyString", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctx := scopedContractContext(t, store)
		project, err := store.Tasks().CreateProject(ctx, &model.CreateTaskProjectRequest{
			Name:        "Calendar Learning",
			Type:        "learning",
			Description: "calendar source test",
		})
		if err != nil {
			t.Fatalf("create project: %v", err)
		}

		event := model.Event{
			Title:     "project event",
			StartTime: time.Date(2026, 7, 9, 9, 0, 0, 0, time.Local).Unix(),
			EndTime:   time.Date(2026, 7, 9, 10, 0, 0, 0, time.Local).Unix(),
			Kind:      "work",
			ProjectID: &project.ID,
		}
		if err := store.Events().Create(ctx, &event); err != nil {
			t.Fatalf("create event: %v", err)
		}

		loaded, err := store.Events().GetByID(ctx, event.ID)
		if err != nil {
			t.Fatalf("load event: %v", err)
		}
		if loaded.ProjectID == nil || *loaded.ProjectID != project.ID {
			t.Fatalf("project_id = %v, want %q", loaded.ProjectID, project.ID)
		}
		if loaded.Project == nil || *loaded.Project != project.Name {
			t.Fatalf("project = %v, want %q", loaded.Project, project.Name)
		}
		if loaded.ProjectType == nil || *loaded.ProjectType != project.Type {
			t.Fatalf("project_type = %v, want %q", loaded.ProjectType, project.Type)
		}

		clear := ""
		updated, err := store.Events().Update(ctx, event.ID, &model.UpdateEventRequest{ProjectID: &clear})
		if err != nil {
			t.Fatalf("clear event project: %v", err)
		}
		if updated.ProjectID != nil || updated.Project != nil || updated.ProjectType != nil {
			t.Fatalf("expected project fields cleared, got %+v", updated)
		}
	})

	t.Run("DeleteProjectClearsEventProjectIDAndKeepsEventVisible", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctx := scopedContractContext(t, store)
		project, err := store.Tasks().CreateProject(ctx, &model.CreateTaskProjectRequest{
			Name:        "Calendar Project Delete",
			Type:        "regular",
			Description: "event project delete test",
		})
		if err != nil {
			t.Fatalf("create project: %v", err)
		}

		start := time.Date(2026, 7, 10, 9, 0, 0, 0, time.Local).Unix()
		event := model.Event{
			Title:     "project survives delete",
			StartTime: start,
			EndTime:   start + int64(time.Hour.Seconds()),
			Kind:      "work",
			ProjectID: &project.ID,
		}
		if err := store.Events().Create(ctx, &event); err != nil {
			t.Fatalf("create event: %v", err)
		}

		if err := store.Tasks().DeleteProject(ctx, project.ID); err != nil {
			t.Fatalf("delete project: %v", err)
		}

		loaded, err := store.Events().GetByID(ctx, event.ID)
		if err != nil {
			t.Fatalf("load event after project delete: %v", err)
		}
		if loaded.ProjectID != nil || loaded.Project != nil || loaded.ProjectType != nil {
			t.Fatalf("expected deleted project fields cleared, got %+v", loaded)
		}

		listed, total, err := store.Events().List(ctx, start-1, start+int64(2*time.Hour.Seconds()), 1, 10)
		if err != nil {
			t.Fatalf("list events after project delete: %v", err)
		}
		if total != 1 || len(listed) != 1 || listed[0].ID != event.ID {
			t.Fatalf("expected event to remain visible in workspace, total=%d events=%+v", total, listed)
		}
	})
}
