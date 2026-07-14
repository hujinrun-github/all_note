package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/storage/contracttest"
)

func TestSQLiteStoreContract(t *testing.T) {
	contracttest.RunStoreSuite(t, func(t *testing.T) storage.Store {
		t.Helper()

		store, err := (Provider{}).Open(context.Background(), storage.Config{
			Env:        "test",
			Driver:     storage.DriverSQLite,
			SQLitePath: filepath.Join(t.TempDir(), "flowspace.test.db"),
		})
		if err != nil {
			t.Fatalf("open sqlite store: %v", err)
		}
		return store
	})
}

func TestSQLiteAuthContract(t *testing.T) {
	contracttest.RunAuthContractTests(t, func(t *testing.T) storage.Store {
		t.Helper()

		store, err := (Provider{}).Open(context.Background(), storage.Config{
			Env:        "test",
			Driver:     storage.DriverSQLite,
			SQLitePath: filepath.Join(t.TempDir(), "flowspace.test.db"),
		})
		if err != nil {
			t.Fatalf("open sqlite store: %v", err)
		}
		return store
	})
}

func TestSQLiteNoteSearchContract(t *testing.T) {
	contracttest.RunNoteSearchSuite(t, func(t *testing.T) storage.Store {
		t.Helper()

		store, err := (Provider{}).Open(context.Background(), storage.Config{
			Env:        "test",
			Driver:     storage.DriverSQLite,
			SQLitePath: filepath.Join(t.TempDir(), "flowspace.test.db"),
		})
		if err != nil {
			t.Fatalf("open sqlite store: %v", err)
		}
		return store
	})
}

func TestSQLiteNoteProjectLinksContract(t *testing.T) {
	contracttest.RunNoteProjectLinksSuite(t, func(t *testing.T) storage.Store {
		t.Helper()

		store, err := (Provider{}).Open(context.Background(), storage.Config{
			Env:        "test",
			Driver:     storage.DriverSQLite,
			SQLitePath: filepath.Join(t.TempDir(), "flowspace.test.db"),
		})
		if err != nil {
			t.Fatalf("open sqlite store: %v", err)
		}
		return store
	})
}

func TestSQLiteTaskContract(t *testing.T) {
	contracttest.RunTaskSuite(t, func(t *testing.T) storage.Store {
		t.Helper()

		store, err := (Provider{}).Open(context.Background(), storage.Config{
			Env:        "test",
			Driver:     storage.DriverSQLite,
			SQLitePath: filepath.Join(t.TempDir(), "flowspace.test.db"),
		})
		if err != nil {
			t.Fatalf("open sqlite store: %v", err)
		}
		return store
	})
}

func TestSQLiteEventInboxContract(t *testing.T) {
	contracttest.RunEventInboxSuite(t, func(t *testing.T) storage.Store {
		t.Helper()

		store, err := (Provider{}).Open(context.Background(), storage.Config{
			Env:        "test",
			Driver:     storage.DriverSQLite,
			SQLitePath: filepath.Join(t.TempDir(), "flowspace.test.db"),
		})
		if err != nil {
			t.Fatalf("open sqlite store: %v", err)
		}
		return store
	})
}

func TestSQLiteRoadmapContract(t *testing.T) {
	contracttest.RunRoadmapSuite(t, func(t *testing.T) storage.Store {
		t.Helper()

		store, err := (Provider{}).Open(context.Background(), storage.Config{
			Env:        "test",
			Driver:     storage.DriverSQLite,
			SQLitePath: filepath.Join(t.TempDir(), "flowspace.test.db"),
		})
		if err != nil {
			t.Fatalf("open sqlite store: %v", err)
		}
		return store
	})
}

func TestSQLiteSyncContract(t *testing.T) {
	contracttest.RunSyncSuite(t, func(t *testing.T) storage.Store {
		t.Helper()

		store, err := (Provider{}).Open(context.Background(), storage.Config{
			Env:        "test",
			Driver:     storage.DriverSQLite,
			SQLitePath: filepath.Join(t.TempDir(), "flowspace.test.db"),
		})
		if err != nil {
			t.Fatalf("open sqlite store: %v", err)
		}
		return store
	})
}

func TestSQLiteRecurrenceContract(t *testing.T) {
	contracttest.RunRecurrenceSuite(t, func(t *testing.T) storage.Store {
		t.Helper()

		store, err := (Provider{}).Open(context.Background(), storage.Config{
			Env:        "test",
			Driver:     storage.DriverSQLite,
			SQLitePath: filepath.Join(t.TempDir(), "flowspace.test.db"),
		})
		if err != nil {
			t.Fatalf("open sqlite store: %v", err)
		}
		return store
	})
}

func TestSQLiteSyncBindingContract(t *testing.T) {
	contracttest.RunSyncBindingSuite(t, func(t *testing.T) storage.Store {
		t.Helper()

		store, err := (Provider{}).Open(context.Background(), storage.Config{
			Env:        "test",
			Driver:     storage.DriverSQLite,
			SQLitePath: filepath.Join(t.TempDir(), "flowspace.test.db"),
		})
		if err != nil {
			t.Fatalf("open sqlite store: %v", err)
		}
		return store
	})
}

func TestSQLiteWorkspaceIsolationContract(t *testing.T) {
	contracttest.RunWorkspaceIsolationSuite(t, func(t *testing.T) storage.Store {
		t.Helper()

		store, err := (Provider{}).Open(context.Background(), storage.Config{
			Env:        "test",
			Driver:     storage.DriverSQLite,
			SQLitePath: filepath.Join(t.TempDir(), "flowspace.test.db"),
		})
		if err != nil {
			t.Fatalf("open sqlite store: %v", err)
		}
		return store
	})
}

func TestSQLiteCalendarProjectSourcesContract(t *testing.T) {
	contracttest.RunCalendarProjectSourcesSuite(t, func(t *testing.T) storage.Store {
		t.Helper()

		store, err := (Provider{}).Open(context.Background(), storage.Config{
			Env:        "test",
			Driver:     storage.DriverSQLite,
			SQLitePath: filepath.Join(t.TempDir(), "flowspace.test.db"),
		})
		if err != nil {
			t.Fatalf("open sqlite store: %v", err)
		}
		return store
	})
}
