package repository

import (
	"errors"
	"testing"

	"github.com/hujinrun/flowspace/internal/storage"
)

func TestSetStoreAndActiveStore(t *testing.T) {
	store := &fakeRepositoryStore{}
	SetStore(store)
	t.Cleanup(func() { SetStore(nil) })

	if ActiveStore() != store {
		t.Fatal("expected active store to be returned")
	}
}

func TestActiveStorePanicsWhenMissing(t *testing.T) {
	SetStore(nil)
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic when active store is missing")
		}
	}()
	_ = ActiveStore()
}

func TestWithScopedStoreIsGoroutineLocal(t *testing.T) {
	base := &fakeRepositoryStore{}
	scoped := &fakeRepositoryStore{}
	SetStore(base)
	t.Cleanup(func() { SetStore(nil) })

	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		WithScopedStore(scoped, func() {
			if CurrentStore() != scoped {
				done <- errUnexpectedStore
				return
			}
			close(entered)
			<-release
			if CurrentStore() != scoped {
				done <- errUnexpectedStore
				return
			}
		})
		done <- nil
	}()

	<-entered
	if CurrentStore() != base {
		t.Fatal("other goroutine saw scoped store")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("scoped goroutine error: %v", err)
	}
	if CurrentStore() != base {
		t.Fatal("scoped store leaked after callback")
	}
}

func TestWithScopedStoreRestoresOuterScopedStore(t *testing.T) {
	base := &fakeRepositoryStore{}
	outer := &fakeRepositoryStore{}
	inner := &fakeRepositoryStore{}
	SetStore(base)
	t.Cleanup(func() { SetStore(nil) })

	WithScopedStore(outer, func() {
		if CurrentStore() != outer {
			t.Fatal("outer scoped store not active")
		}
		WithScopedStore(inner, func() {
			if CurrentStore() != inner {
				t.Fatal("inner scoped store not active")
			}
		})
		if CurrentStore() != outer {
			t.Fatal("outer scoped store not restored")
		}
	})
	if CurrentStore() != base {
		t.Fatal("base store not restored")
	}
}

var errUnexpectedStore = errors.New("unexpected scoped store")

type fakeRepositoryStore struct{ storage.Store }
