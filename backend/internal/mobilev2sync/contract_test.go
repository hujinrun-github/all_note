package mobilev2sync

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestMTDV2Contract006SlidingScopeExpiryDoesNotAdvanceCursor(t *testing.T) {
	timeZone := "Asia/Shanghai"
	validUntil := time.Date(2026, 7, 24, 0, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	sliding := ScopeState{
		Name: ScopeIPhoneOccurrenceWindow, Cursor: "cursor-window", Generation: "generation-1",
		ProjectionTimeZone: &timeZone, ValidUntil: &validUntil,
	}
	if err := sliding.ValidateChanges(validUntil.Add(-time.Second), &timeZone, "generation-1"); err != nil {
		t.Fatalf("valid sliding scope rejected: %v", err)
	}
	for _, test := range []struct {
		name       string
		now        time.Time
		timeZone   *string
		generation string
	}{
		{name: "expired", now: validUntil, timeZone: &timeZone, generation: "generation-1"},
		{name: "timezone changed", now: validUntil.Add(-time.Hour), timeZone: stringPointer("UTC"), generation: "generation-1"},
		{name: "generation changed", now: validUntil.Add(-time.Hour), timeZone: &timeZone, generation: "generation-2"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := sliding.ValidateChanges(test.now, test.timeZone, test.generation); !errors.Is(err, ErrProjectionRefreshRequired) {
				t.Fatalf("error = %v, want projection refresh", err)
			}
			if sliding.Cursor != "cursor-window" {
				t.Fatalf("refresh check advanced cursor to %q", sliding.Cursor)
			}
		})
	}
	stable := ScopeState{Name: ScopeIPhoneTaskCore, Cursor: "cursor-core", Generation: "stable-1"}
	if err := stable.ValidateChanges(validUntil.Add(48*time.Hour), nil, "stable-1"); err != nil {
		t.Fatalf("stable scope must not expire at midnight: %v", err)
	}
}

func TestMTDV2Contract007SnapshotPagesUseOneFixedAsOfBoundary(t *testing.T) {
	store := NewReferenceStore([]EntityImage{
		{EntityType: "task", EntityID: "task-a", Revision: "1"},
		{EntityType: "task", EntityID: "task-b", Revision: "1"},
		{EntityType: "task", EntityID: "task-c", Revision: "1"},
	})
	session, err := store.BeginSnapshot("snapshot-1", ScopeIPhoneTaskCore, "generation-1", nil, time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if session.AsOfSequence != 3 || session.SnapshotCursor != "sequence:3" {
		t.Fatalf("snapshot boundary = (%d,%q), want (3,sequence:3)", session.AsOfSequence, session.SnapshotCursor)
	}
	store.Commit(EntityImage{EntityType: "task", EntityID: "task-d", Revision: "1"})
	store.Commit(EntityImage{EntityType: "task", EntityID: "task-a", Revision: "2"})
	store.Commit(EntityImage{EntityType: "task", EntityID: "task-b", Revision: "2", Deleted: true})

	first, more, err := session.Page(0, 2)
	if err != nil || !more {
		t.Fatalf("first page more=%v err=%v", more, err)
	}
	second, more, err := session.Page(1, 2)
	if err != nil || more {
		t.Fatalf("second page more=%v err=%v", more, err)
	}
	gotSnapshot := append(first, second...)
	wantSnapshot := []EntityImage{
		{EntityType: "task", EntityID: "task-a", Revision: "1"},
		{EntityType: "task", EntityID: "task-b", Revision: "1"},
		{EntityType: "task", EntityID: "task-c", Revision: "1"},
	}
	if !reflect.DeepEqual(gotSnapshot, wantSnapshot) {
		t.Fatalf("fixed snapshot = %#v, want %#v", gotSnapshot, wantSnapshot)
	}
	changes := store.ChangesAfter(session.AsOfSequence)
	if len(changes) != 3 || changes[0].Sequence != 4 || changes[2].Sequence != 6 {
		t.Fatalf("changes after boundary = %#v", changes)
	}
}

func TestMTDV2Contract016ScopeCursorsAreIndependent(t *testing.T) {
	validUntil := time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC)
	state := WorkspaceCursors{Scopes: map[ScopeName]ScopeState{
		ScopeIPhoneContent:          {Name: ScopeIPhoneContent, Cursor: "content-8", Generation: "content-g"},
		ScopeIPhoneTaskCore:         {Name: ScopeIPhoneTaskCore, Cursor: "core-9", Generation: "core-g"},
		ScopeIPhoneOccurrenceWindow: {Name: ScopeIPhoneOccurrenceWindow, Cursor: "window-10", Generation: "window-g", ValidUntil: &validUntil},
		ScopeWatchOccurrenceWindow:  {Name: ScopeWatchOccurrenceWindow, Cursor: "watch-11", Generation: "watch-g", ValidUntil: &validUntil},
	}}
	refreshed := state.RequiringRefresh(ScopeIPhoneOccurrenceWindow, "window-g2", validUntil.Add(24*time.Hour))
	if refreshed.Scopes[ScopeIPhoneContent].Cursor != "content-8" || refreshed.Scopes[ScopeIPhoneTaskCore].Cursor != "core-9" || refreshed.Scopes[ScopeWatchOccurrenceWindow].Cursor != "watch-11" {
		t.Fatalf("window refresh changed independent scopes: %#v", refreshed.Scopes)
	}
	window := refreshed.Scopes[ScopeIPhoneOccurrenceWindow]
	if window.Cursor != "" || window.Generation != "window-g2" || window.ValidUntil == nil || !window.ValidUntil.Equal(validUntil.Add(24*time.Hour)) {
		t.Fatalf("window refresh state = %#v", window)
	}
}

func stringPointer(value string) *string { return &value }
