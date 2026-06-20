package postgres

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/storage/contracttest"
)

func TestPostgresStoreContract(t *testing.T) {
	contracttest.RunStoreSuite(t, func(t *testing.T) storage.Store {
		t.Helper()

		schema := fmt.Sprintf("fs_test_contract_%d", time.Now().UnixNano())
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		t.Cleanup(cancel)
		store, err := (Provider{}).Open(ctx, storage.Config{
			Env:    "test",
			Driver: storage.DriverPostgres,
			URL:    createPostgresTestSchema(t, schema),
		})
		if err != nil {
			t.Fatalf("open postgres store: %v", err)
		}
		return store
	})
}

func TestPostgresNoteSearchContract(t *testing.T) {
	contracttest.RunNoteSearchSuite(t, func(t *testing.T) storage.Store {
		t.Helper()

		schema := fmt.Sprintf("fs_test_notes_contract_%d", time.Now().UnixNano())
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		t.Cleanup(cancel)
		store, err := (Provider{}).Open(ctx, storage.Config{
			Env:    "test",
			Driver: storage.DriverPostgres,
			URL:    createPostgresTestSchema(t, schema),
		})
		if err != nil {
			t.Fatalf("open postgres store: %v", err)
		}
		return store
	})
}

func TestPostgresNoteProjectLinksContract(t *testing.T) {
	contracttest.RunNoteProjectLinksSuite(t, func(t *testing.T) storage.Store {
		t.Helper()

		schema := fmt.Sprintf("fs_test_note_project_links_contract_%d", time.Now().UnixNano())
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		t.Cleanup(cancel)
		store, err := (Provider{}).Open(ctx, storage.Config{
			Env:    "test",
			Driver: storage.DriverPostgres,
			URL:    createPostgresTestSchema(t, schema),
		})
		if err != nil {
			t.Fatalf("open postgres store: %v", err)
		}
		return store
	})
}

func TestPostgresTaskContract(t *testing.T) {
	contracttest.RunTaskSuite(t, func(t *testing.T) storage.Store {
		t.Helper()

		schema := fmt.Sprintf("fs_test_tasks_contract_%d", time.Now().UnixNano())
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		t.Cleanup(cancel)
		store, err := (Provider{}).Open(ctx, storage.Config{
			Env:    "test",
			Driver: storage.DriverPostgres,
			URL:    createPostgresTestSchema(t, schema),
		})
		if err != nil {
			t.Fatalf("open postgres store: %v", err)
		}
		return store
	})
}

func TestPostgresEventInboxContract(t *testing.T) {
	contracttest.RunEventInboxSuite(t, func(t *testing.T) storage.Store {
		t.Helper()

		schema := fmt.Sprintf("fs_test_events_inbox_contract_%d", time.Now().UnixNano())
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		t.Cleanup(cancel)
		store, err := (Provider{}).Open(ctx, storage.Config{
			Env:    "test",
			Driver: storage.DriverPostgres,
			URL:    createPostgresTestSchema(t, schema),
		})
		if err != nil {
			t.Fatalf("open postgres store: %v", err)
		}
		return store
	})
}

func TestPostgresRoadmapContract(t *testing.T) {
	contracttest.RunRoadmapSuite(t, func(t *testing.T) storage.Store {
		t.Helper()

		schema := fmt.Sprintf("fs_test_roadmaps_contract_%d", time.Now().UnixNano())
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		t.Cleanup(cancel)
		store, err := (Provider{}).Open(ctx, storage.Config{
			Env:    "test",
			Driver: storage.DriverPostgres,
			URL:    createPostgresTestSchema(t, schema),
		})
		if err != nil {
			t.Fatalf("open postgres store: %v", err)
		}
		return store
	})
}

func TestPostgresSyncContract(t *testing.T) {
	contracttest.RunSyncSuite(t, func(t *testing.T) storage.Store {
		t.Helper()

		schema := fmt.Sprintf("fs_test_sync_contract_%d", time.Now().UnixNano())
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		t.Cleanup(cancel)
		store, err := (Provider{}).Open(ctx, storage.Config{
			Env:    "test",
			Driver: storage.DriverPostgres,
			URL:    createPostgresTestSchema(t, schema),
		})
		if err != nil {
			t.Fatalf("open postgres store: %v", err)
		}
		return store
	})
}

func TestPostgresSyncBindingContract(t *testing.T) {
	contracttest.RunSyncBindingSuite(t, func(t *testing.T) storage.Store {
		t.Helper()

		schema := fmt.Sprintf("fs_test_sync_binding_contract_%d", time.Now().UnixNano())
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		t.Cleanup(cancel)
		store, err := (Provider{}).Open(ctx, storage.Config{
			Env:    "test",
			Driver: storage.DriverPostgres,
			URL:    createPostgresTestSchema(t, schema),
		})
		if err != nil {
			t.Fatalf("open postgres store: %v", err)
		}
		return store
	})
}
