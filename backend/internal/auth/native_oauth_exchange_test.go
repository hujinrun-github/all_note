package auth

import (
	"errors"
	"testing"
	"time"
)

func TestMemoryNativeOAuthExchangeStoreConsumesGrantOnce(t *testing.T) {
	store := NewMemoryNativeOAuthExchangeStore()
	grant := NativeOAuthGrant{
		SessionToken:     "session-token",
		SessionExpiresAt: time.Now().UTC().Add(time.Hour),
		CodeChallenge:    "challenge",
	}
	if err := store.Save(t.Context(), "exchange-code", grant, time.Minute); err != nil {
		t.Fatalf("save grant: %v", err)
	}

	got, err := store.Consume(t.Context(), "exchange-code")
	if err != nil {
		t.Fatalf("consume grant: %v", err)
	}
	if got.SessionToken != grant.SessionToken || got.CodeChallenge != grant.CodeChallenge {
		t.Fatalf("grant = %+v, want %+v", got, grant)
	}
	if _, err := store.Consume(t.Context(), "exchange-code"); !errors.Is(err, ErrNativeOAuthCodeInvalid) {
		t.Fatalf("second consume error = %v, want ErrNativeOAuthCodeInvalid", err)
	}
}

func TestMemoryNativeOAuthExchangeStoreRejectsExpiredGrant(t *testing.T) {
	store := NewMemoryNativeOAuthExchangeStore()
	grant := NativeOAuthGrant{
		SessionToken:     "session-token",
		SessionExpiresAt: time.Now().UTC().Add(-time.Second),
		CodeChallenge:    "challenge",
	}
	if err := store.Save(t.Context(), "exchange-code", grant, time.Minute); !errors.Is(err, ErrNativeOAuthCodeInvalid) {
		t.Fatalf("save error = %v, want ErrNativeOAuthCodeInvalid", err)
	}
}

func TestMemoryNativeOAuthExchangeStoreCleanupBatch(t *testing.T) {
	store := NewMemoryNativeOAuthExchangeStore()
	now := time.Now().UTC()
	store.entries["expired-1"] = nativeOAuthExchangeEntry{expiresAt: now.Add(-time.Minute)}
	store.entries["expired-2"] = nativeOAuthExchangeEntry{expiresAt: now.Add(-time.Minute)}
	store.entries["active"] = nativeOAuthExchangeEntry{expiresAt: now.Add(time.Minute)}

	if got := store.CleanupExpired(now, 1); got != 1 {
		t.Fatalf("deleted = %d, want 1", got)
	}
	if len(store.entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(store.entries))
	}
}
