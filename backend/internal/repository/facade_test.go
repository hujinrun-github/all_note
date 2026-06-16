package repository

import (
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

type fakeRepositoryStore struct{ storage.Store }
