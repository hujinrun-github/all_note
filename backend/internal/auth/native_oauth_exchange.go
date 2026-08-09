package auth

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

var ErrNativeOAuthCodeInvalid = errors.New("native oauth code invalid")

type NativeOAuthGrant struct {
	SessionToken     string
	SessionExpiresAt time.Time
	CodeChallenge    string
}

type NativeOAuthExchangeStore interface {
	Save(ctx context.Context, code string, grant NativeOAuthGrant, ttl time.Duration) error
	Consume(ctx context.Context, code string) (NativeOAuthGrant, error)
}

type nativeOAuthExchangeEntry struct {
	grant     NativeOAuthGrant
	expiresAt time.Time
}

type MemoryNativeOAuthExchangeStore struct {
	mu      sync.Mutex
	entries map[string]nativeOAuthExchangeEntry
}

func NewMemoryNativeOAuthExchangeStore() *MemoryNativeOAuthExchangeStore {
	return &MemoryNativeOAuthExchangeStore{entries: map[string]nativeOAuthExchangeEntry{}}
}

func (s *MemoryNativeOAuthExchangeStore) Save(ctx context.Context, code string, grant NativeOAuthGrant, ttl time.Duration) error {
	if strings.TrimSpace(code) == "" || strings.TrimSpace(grant.SessionToken) == "" ||
		strings.TrimSpace(grant.CodeChallenge) == "" || grant.SessionExpiresAt.IsZero() || ttl <= 0 {
		return ErrNativeOAuthCodeInvalid
	}
	now := time.Now().UTC()
	expiresAt := now.Add(ttl)
	if grant.SessionExpiresAt.Before(expiresAt) {
		expiresAt = grant.SessionExpiresAt
	}
	if !expiresAt.After(now) {
		return ErrNativeOAuthCodeInvalid
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[code] = nativeOAuthExchangeEntry{grant: grant, expiresAt: expiresAt}
	return nil
}

func (s *MemoryNativeOAuthExchangeStore) Consume(ctx context.Context, code string) (NativeOAuthGrant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[code]
	delete(s.entries, code)
	if !ok || !entry.expiresAt.After(time.Now().UTC()) {
		return NativeOAuthGrant{}, ErrNativeOAuthCodeInvalid
	}
	return entry.grant, nil
}

func (s *MemoryNativeOAuthExchangeStore) CleanupExpired(now time.Time, limit int) int {
	if limit <= 0 {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	deleted := 0
	for key, entry := range s.entries {
		if deleted >= limit {
			break
		}
		if !entry.expiresAt.After(now) {
			delete(s.entries, key)
			deleted++
		}
	}
	return deleted
}

func (s *MemoryNativeOAuthExchangeStore) RunCleanup(ctx context.Context, interval time.Duration, batchSize int) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			s.CleanupExpired(now.UTC(), batchSize)
		}
	}
}
