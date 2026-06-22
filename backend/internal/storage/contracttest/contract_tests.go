package contracttest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/storage"
)

type StoreFactory func(t *testing.T) storage.Store

func RunStoreSuite(t *testing.T, factory StoreFactory) {
	t.Helper()

	t.Run("Health", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := store.Health(ctx); err != nil {
			t.Fatalf("health: %v", err)
		}
	})

	t.Run("TransactCommitCallsCallback", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		called := false
		err := store.Transact(context.Background(), func(txStore storage.Store) error {
			called = true
			if txStore == nil {
				t.Fatal("transaction store is nil")
			}
			return nil
		})
		if err != nil {
			t.Fatalf("commit transaction: %v", err)
		}
		if !called {
			t.Fatal("expected transaction callback to be called")
		}
	})

	t.Run("TransactRollbackPropagatesError", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		errSentinel := errors.New("contract rollback")
		err := store.Transact(context.Background(), func(txStore storage.Store) error {
			if txStore == nil {
				t.Fatal("transaction store is nil")
			}
			return errSentinel
		})
		if !errors.Is(err, errSentinel) {
			t.Fatalf("expected rollback sentinel, got %v", err)
		}
	})

	t.Run("TransactRejectsNilCallback", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		if err := store.Transact(context.Background(), nil); err == nil {
			t.Fatal("expected nil transaction callback to fail")
		}
	})

	t.Run("TransactReraisesPanic", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		panicValue := "contract panic"
		recovered := recoverContractPanic(func() {
			_ = store.Transact(context.Background(), func(txStore storage.Store) error {
				if txStore == nil {
					t.Fatal("transaction store is nil")
				}
				panic(panicValue)
			})
		})
		if recovered != panicValue {
			t.Fatalf("expected panic %q, got %#v", panicValue, recovered)
		}
	})
}

func recoverContractPanic(fn func()) (recovered any) {
	defer func() {
		recovered = recover()
	}()
	fn()
	return nil
}
