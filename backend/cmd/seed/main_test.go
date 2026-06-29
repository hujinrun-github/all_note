package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/storage/sqlite"
)

func TestRunLegacySQLiteSeedRefusesAuthEnabledStoreBeforeWriting(t *testing.T) {
	store := openSeedSQLiteStore(t)
	seedSeedCommandUser(t, store)

	initCalled := false
	seedCalled := false
	err := runLegacySQLiteSeed(context.Background(), store, "ignored.db",
		func(string) error {
			initCalled = true
			return nil
		},
		func() error {
			seedCalled = true
			return nil
		},
	)
	if !errors.Is(err, ErrLegacySQLiteSeedAuthEnabled) {
		t.Fatalf("run legacy sqlite seed error = %v, want ErrLegacySQLiteSeedAuthEnabled", err)
	}
	if initCalled || seedCalled {
		t.Fatalf("legacy seed callbacks called after auth users exist: init=%v seed=%v", initCalled, seedCalled)
	}
}

func openSeedSQLiteStore(t *testing.T) storage.Store {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "flowspace.seed.db")
	store, err := (sqlite.Provider{}).Open(context.Background(), storage.Config{
		Env:        "test",
		Driver:     storage.DriverSQLite,
		SQLitePath: dbPath,
	})
	if err != nil {
		t.Fatalf("open sqlite provider: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("close sqlite store: %v", err)
		}
	})
	return store
}

func seedSeedCommandUser(t *testing.T, store storage.Store) {
	t.Helper()

	ctx := context.Background()
	user := &model.User{
		ID:           "user_seed_existing",
		Email:        "seed-existing@example.com",
		DisplayName:  "Seed Existing",
		PasswordHash: "hash",
		Role:         "admin",
		Status:       "active",
	}
	if err := store.Auth().CreateUser(ctx, user); err != nil {
		t.Fatalf("seed existing user: %v", err)
	}
}
