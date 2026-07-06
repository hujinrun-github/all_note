package auth

import (
	"context"
	"errors"
	"sync"
	"time"
)

var ErrOAuthStateInvalid = errors.New("oauth state invalid")

type OAuthStateStore interface {
	Save(ctx context.Context, state, next string, ttl time.Duration) error
	Consume(ctx context.Context, state string) (string, error)
}

type oauthStateEntry struct {
	next      string
	expiresAt time.Time
}

type MemoryOAuthStateStore struct {
	mu      sync.Mutex
	entries map[string]oauthStateEntry
}

func NewMemoryOAuthStateStore() *MemoryOAuthStateStore {
	return &MemoryOAuthStateStore{entries: map[string]oauthStateEntry{}}
}

func (s *MemoryOAuthStateStore) Save(ctx context.Context, state, next string, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[state] = oauthStateEntry{next: next, expiresAt: time.Now().UTC().Add(ttl)}
	return nil
}

func (s *MemoryOAuthStateStore) Consume(ctx context.Context, state string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[state]
	if !ok || time.Now().UTC().After(entry.expiresAt) {
		delete(s.entries, state)
		return "", ErrOAuthStateInvalid
	}
	delete(s.entries, state)
	return entry.next, nil
}

func (s *MemoryOAuthStateStore) CleanupExpired(now time.Time, limit int) int {
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
		if now.After(entry.expiresAt) {
			delete(s.entries, key)
			deleted++
		}
	}
	return deleted
}

func (s *MemoryOAuthStateStore) RunCleanup(ctx context.Context, interval time.Duration, batchSize int) {
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
