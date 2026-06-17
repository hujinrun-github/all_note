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
