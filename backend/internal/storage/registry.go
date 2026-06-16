package storage

import (
	"context"
	"fmt"
	"sync"
)

type Registry struct {
	mu        sync.RWMutex
	providers map[Driver]Provider
}

func NewRegistry() *Registry {
	return &Registry{providers: map[Driver]Provider{}}
}

func (r *Registry) Register(provider Provider) error {
	if provider == nil {
		return fmt.Errorf("storage provider is nil")
	}
	driver := provider.Driver()
	if driver == "" {
		return fmt.Errorf("storage provider driver is empty")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.providers[driver]; exists {
		return fmt.Errorf("storage provider %q already registered", driver)
	}
	r.providers[driver] = provider
	return nil
}

func (r *Registry) Open(ctx context.Context, cfg Config) (Store, error) {
	r.mu.RLock()
	provider, ok := r.providers[cfg.Driver]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("storage provider %q is not registered", cfg.Driver)
	}
	if err := provider.Validate(cfg); err != nil {
		return nil, err
	}
	return provider.Open(ctx, cfg)
}
