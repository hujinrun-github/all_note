package sqlite

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

func TestLockActiveAdminsBlocksConcurrentGuard(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "flowspace.auth-lock.db")
	first := openSQLiteAuthLockStore(t, dbPath)
	second := openSQLiteAuthLockStore(t, dbPath)

	ctx := context.Background()
	seedSQLiteAuthLockAdmin(t, ctx, first)

	firstLocked := make(chan struct{})
	releaseFirst := make(chan struct{})
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() {
			close(releaseFirst)
		})
	}
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- first.Transact(ctx, func(txStore storage.Store) error {
			if _, err := txStore.Auth().LockActiveAdmins(ctx); err != nil {
				return err
			}
			close(firstLocked)
			<-releaseFirst
			return nil
		})
	}()

	select {
	case <-firstLocked:
	case err := <-firstDone:
		t.Fatalf("first transaction ended before holding lock: %v", err)
	case <-time.After(2 * time.Second):
		release()
		t.Fatal("timed out waiting for first transaction to hold admin lock")
	}

	secondStarted := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- second.Transact(ctx, func(txStore storage.Store) error {
			close(secondStarted)
			_, err := txStore.Auth().LockActiveAdmins(ctx)
			return err
		})
	}()

	select {
	case <-secondStarted:
	case err := <-secondDone:
		release()
		waitForSQLiteAuthLockTransaction(t, firstDone, "first transaction")
		t.Fatalf("second transaction ended before attempting lock: %v", err)
	case <-time.After(2 * time.Second):
		release()
		waitForSQLiteAuthLockTransaction(t, firstDone, "first transaction")
		t.Fatal("timed out waiting for second transaction to attempt admin lock")
	}

	select {
	case err := <-secondDone:
		release()
		waitForSQLiteAuthLockTransaction(t, firstDone, "first transaction")
		if err == nil {
			t.Fatal("second transaction passed LockActiveAdmins while first transaction held the guard lock")
		}
		t.Fatalf("second transaction failed before first released guard lock: %v", err)
	case <-time.After(150 * time.Millisecond):
	}

	release()

	waitForSQLiteAuthLockTransaction(t, firstDone, "first transaction")

	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatalf("second transaction after first released lock: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for second transaction after first released lock")
	}
}

func waitForSQLiteAuthLockTransaction(t *testing.T, done <-chan error, name string) {
	t.Helper()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s to finish", name)
	}
}

func openSQLiteAuthLockStore(t *testing.T, dbPath string) *store {
	t.Helper()

	opened, err := (Provider{}).Open(context.Background(), storage.Config{
		Env:        "test",
		Driver:     storage.DriverSQLite,
		SQLitePath: dbPath,
	})
	if err != nil {
		t.Fatalf("open sqlite auth lock store: %v", err)
	}
	t.Cleanup(func() {
		if err := opened.Close(); err != nil {
			t.Fatalf("close sqlite auth lock store: %v", err)
		}
	})
	sqliteStore, ok := opened.(*store)
	if !ok {
		t.Fatalf("expected sqlite store, got %T", opened)
	}
	return sqliteStore
}

func seedSQLiteAuthLockAdmin(t *testing.T, ctx context.Context, store *store) {
	t.Helper()

	user := &model.User{
		ID:                 "sqlite_auth_lock_admin",
		Email:              "sqlite-auth-lock-admin@example.com",
		DisplayName:        "SQLite Auth Lock Admin",
		PasswordHash:       "hash",
		MustChangePassword: true,
		DefaultWorkspaceID: "sqlite_auth_lock_workspace",
		Role:               "admin",
		Status:             "active",
	}
	workspace := &model.Workspace{
		ID:          "sqlite_auth_lock_workspace",
		Name:        "SQLite Auth Lock Workspace",
		OwnerUserID: user.ID,
	}

	if err := store.Transact(ctx, func(txStore storage.Store) error {
		if err := txStore.Auth().CreateUser(ctx, user); err != nil {
			return err
		}
		if err := txStore.Auth().CreateWorkspace(ctx, workspace); err != nil {
			return err
		}
		return txStore.Auth().AddWorkspaceMember(ctx, workspace.ID, user.ID, "owner")
	}); err != nil {
		t.Fatalf("seed sqlite auth lock admin: %v", err)
	}
}
