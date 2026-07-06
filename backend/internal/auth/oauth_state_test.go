package auth

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestMemoryOAuthStateStoreConsumesStateOnce(t *testing.T) {
	store := NewMemoryOAuthStateStore()
	state := "state-1"
	if err := store.Save(t.Context(), state, "/tasks", time.Minute); err != nil {
		t.Fatalf("save state: %v", err)
	}
	next, err := store.Consume(t.Context(), state)
	if err != nil {
		t.Fatalf("consume state: %v", err)
	}
	if next != "/tasks" {
		t.Fatalf("next = %q, want /tasks", next)
	}
	_, err = store.Consume(t.Context(), state)
	if !errors.Is(err, ErrOAuthStateInvalid) {
		t.Fatalf("second consume error = %v, want ErrOAuthStateInvalid", err)
	}
}

func TestMemoryOAuthStateStoreRejectsExpiredState(t *testing.T) {
	store := NewMemoryOAuthStateStore()
	if err := store.Save(t.Context(), "expired", "/notes", time.Nanosecond); err != nil {
		t.Fatalf("save state: %v", err)
	}
	time.Sleep(time.Millisecond)
	_, err := store.Consume(t.Context(), "expired")
	if !errors.Is(err, ErrOAuthStateInvalid) {
		t.Fatalf("expired state error = %v, want ErrOAuthStateInvalid", err)
	}
}

func TestMemoryOAuthStateStoreCleanupBatch(t *testing.T) {
	store := NewMemoryOAuthStateStore()
	for i := 0; i < 5; i++ {
		if err := store.Save(t.Context(), fmt.Sprintf("state-%d", i), "/", time.Nanosecond); err != nil {
			t.Fatalf("save state %d: %v", i, err)
		}
	}
	time.Sleep(time.Millisecond)
	deleted := store.CleanupExpired(time.Now().UTC(), 2)
	if deleted != 2 {
		t.Fatalf("deleted = %d, want 2", deleted)
	}
}
