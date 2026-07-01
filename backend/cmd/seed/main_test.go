package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/bootstrap"
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
		bootstrap.Config{},
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

func TestRunLegacySQLiteSeedRequiresValidBootstrapConfigBeforeWriting(t *testing.T) {
	tests := []struct {
		name string
		cfg  bootstrap.Config
		want error
	}{
		{
			name: "missing config",
			cfg:  bootstrap.Config{},
			want: bootstrap.ErrBootstrapAdminRequired,
		},
		{
			name: "weak password",
			cfg: bootstrap.Config{
				AdminEmail:    "admin@example.com",
				AdminPassword: "weak",
				AdminName:     "Admin",
			},
			want: auth.ErrWeakPassword,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := openSeedSQLiteStore(t)
			initCalled := false
			seedCalled := false

			err := runLegacySQLiteSeed(context.Background(), store, "ignored.db",
				tt.cfg,
				func(string) error {
					initCalled = true
					return nil
				},
				func() error {
					seedCalled = true
					return nil
				},
			)
			if !errors.Is(err, tt.want) {
				t.Fatalf("run legacy sqlite seed error = %v, want %v", err, tt.want)
			}
			if initCalled || seedCalled {
				t.Fatalf("legacy seed callbacks called before valid bootstrap config: init=%v seed=%v", initCalled, seedCalled)
			}
		})
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
