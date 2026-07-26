package mobilev2sync

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

var ErrProjectionRefreshRequired = errors.New("mobile-v2 projection refresh required")

type ScopeName string

const (
	ScopeIPhoneContent          ScopeName = "iphone-content"
	ScopeIPhoneTaskCore         ScopeName = "iphone-task-core"
	ScopeIPhoneOccurrenceWindow ScopeName = "iphone-occurrence-window"
	ScopeWatchOccurrenceWindow  ScopeName = "watch-occurrence-window"
)

type ScopeState struct {
	Name               ScopeName
	Cursor             string
	Generation         string
	ProjectionTimeZone *string
	ValidUntil         *time.Time
}

func (state ScopeState) ValidateChanges(now time.Time, requestedTimeZone *string, generation string) error {
	if state.Generation == "" || generation != state.Generation {
		return ErrProjectionRefreshRequired
	}
	if !state.sliding() {
		if state.ProjectionTimeZone != nil || state.ValidUntil != nil || requestedTimeZone != nil {
			return ErrProjectionRefreshRequired
		}
		return nil
	}
	if state.ProjectionTimeZone == nil || state.ValidUntil == nil || requestedTimeZone == nil {
		return ErrProjectionRefreshRequired
	}
	if *state.ProjectionTimeZone != *requestedTimeZone || !now.Before(*state.ValidUntil) {
		return ErrProjectionRefreshRequired
	}
	return nil
}

func (state ScopeState) sliding() bool {
	return state.Name == ScopeIPhoneOccurrenceWindow || state.Name == ScopeWatchOccurrenceWindow
}

type WorkspaceCursors struct {
	Scopes map[ScopeName]ScopeState
}

func (state WorkspaceCursors) RequiringRefresh(scope ScopeName, generation string, validUntil time.Time) WorkspaceCursors {
	result := WorkspaceCursors{Scopes: make(map[ScopeName]ScopeState, len(state.Scopes))}
	for name, existing := range state.Scopes {
		result.Scopes[name] = existing
	}
	selected := result.Scopes[scope]
	selected.Cursor = ""
	selected.Generation = generation
	selected.ValidUntil = &validUntil
	result.Scopes[scope] = selected
	return result
}

type EntityImage struct {
	EntityType string
	EntityID   string
	Revision   string
	Deleted    bool
}

type CommittedChange struct {
	Sequence uint64
	Image    EntityImage
}

type ReferenceStore struct {
	mu       sync.Mutex
	sequence uint64
	current  map[string]EntityImage
	changes  []CommittedChange
}

func NewReferenceStore(seed []EntityImage) *ReferenceStore {
	store := &ReferenceStore{current: make(map[string]EntityImage)}
	for _, image := range seed {
		store.Commit(image)
	}
	return store
}

func (store *ReferenceStore) Commit(image EntityImage) uint64 {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.sequence++
	if image.Deleted {
		delete(store.current, entityKey(image))
	} else {
		store.current[entityKey(image)] = image
	}
	store.changes = append(store.changes, CommittedChange{Sequence: store.sequence, Image: image})
	return store.sequence
}

func (store *ReferenceStore) BeginSnapshot(snapshotID string, scope ScopeName, generation string, timeZone *string, asOf time.Time) (*SnapshotSession, error) {
	if snapshotID == "" || generation == "" {
		return nil, fmt.Errorf("snapshot id and scope generation are required")
	}
	if scope == ScopeIPhoneOccurrenceWindow || scope == ScopeWatchOccurrenceWindow {
		if timeZone == nil || *timeZone == "" {
			return nil, fmt.Errorf("sliding scope requires projection time zone")
		}
	} else if timeZone != nil {
		return nil, fmt.Errorf("stable scope must not bind a projection time zone")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	images := make([]EntityImage, 0, len(store.current))
	for _, image := range store.current {
		images = append(images, image)
	}
	sort.Slice(images, func(left, right int) bool {
		if images[left].EntityType != images[right].EntityType {
			return images[left].EntityType < images[right].EntityType
		}
		return images[left].EntityID < images[right].EntityID
	})
	return &SnapshotSession{
		SnapshotID: snapshotID, AsOfSequence: store.sequence, SnapshotCursor: fmt.Sprintf("sequence:%d", store.sequence),
		Scope: scope, ScopeGeneration: generation, ProjectionTimeZone: cloneString(timeZone), ProjectionAsOf: asOf,
		images: images,
	}, nil
}

func (store *ReferenceStore) ChangesAfter(sequence uint64) []CommittedChange {
	store.mu.Lock()
	defer store.mu.Unlock()
	result := make([]CommittedChange, 0)
	for _, change := range store.changes {
		if change.Sequence > sequence {
			result = append(result, change)
		}
	}
	return result
}

type SnapshotSession struct {
	SnapshotID         string
	AsOfSequence       uint64
	SnapshotCursor     string
	Scope              ScopeName
	ScopeGeneration    string
	ProjectionTimeZone *string
	ProjectionAsOf     time.Time
	images             []EntityImage
}

func (session *SnapshotSession) Page(pageIndex, pageSize int) ([]EntityImage, bool, error) {
	if pageIndex < 0 || pageSize <= 0 {
		return nil, false, fmt.Errorf("invalid page request")
	}
	start := pageIndex * pageSize
	if start > len(session.images) {
		return nil, false, fmt.Errorf("page index is beyond snapshot manifest")
	}
	end := start + pageSize
	if end > len(session.images) {
		end = len(session.images)
	}
	result := append([]EntityImage(nil), session.images[start:end]...)
	return result, end < len(session.images), nil
}

func entityKey(image EntityImage) string { return image.EntityType + "\x00" + image.EntityID }

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
