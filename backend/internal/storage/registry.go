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
	provider, err := r.provider(cfg.Driver)
	if err != nil {
		return nil, err
	}
	if err := provider.Validate(cfg); err != nil {
		return nil, err
	}
	return provider.Open(ctx, cfg)
}

func (r *Registry) OpenControl(ctx context.Context, cfg Config) (Store, error) {
	provider, err := r.controlProvider(cfg)
	if err != nil {
		return nil, err
	}
	return provider.OpenControl(ctx, cfg)
}

func (r *Registry) MigrateControl(ctx context.Context, cfg Config) error {
	provider, err := r.controlProvider(cfg)
	if err != nil {
		return err
	}
	return provider.MigrateControl(ctx, cfg)
}

func (r *Registry) OpenTenant(ctx context.Context, cfg Config, expectedSchemaVersion string) (Store, error) {
	provider, err := r.tenantProvider(cfg)
	if err != nil {
		return nil, err
	}
	return provider.OpenTenant(ctx, cfg, expectedSchemaVersion)
}

func (r *Registry) MigrateTenant(ctx context.Context, cfg Config) error {
	provider, err := r.tenantMaintenanceProvider(cfg)
	if err != nil {
		return err
	}
	return provider.MigrateTenant(ctx, cfg)
}

func (r *Registry) AdoptExistingTenant(ctx context.Context, cfg Config, manifest AdoptManifest) error {
	provider, err := r.tenantMaintenanceProvider(cfg)
	if err != nil {
		return err
	}
	return provider.AdoptExistingTenant(ctx, cfg, manifest)
}

func (r *Registry) provider(driver Driver) (Provider, error) {
	r.mu.RLock()
	provider, ok := r.providers[driver]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("storage provider %q is not registered", driver)
	}
	return provider, nil
}

func (r *Registry) controlProvider(cfg Config) (ControlProvider, error) {
	provider, err := r.provider(cfg.Driver)
	if err != nil {
		return nil, err
	}
	if err := provider.Validate(cfg); err != nil {
		return nil, err
	}
	roleProvider, ok := provider.(ControlProvider)
	if !ok {
		return nil, fmt.Errorf("storage provider %q does not support control operations", cfg.Driver)
	}
	return roleProvider, nil
}

func (r *Registry) tenantProvider(cfg Config) (TenantProvider, error) {
	provider, err := r.provider(cfg.Driver)
	if err != nil {
		return nil, err
	}
	if err := provider.Validate(cfg); err != nil {
		return nil, err
	}
	roleProvider, ok := provider.(TenantProvider)
	if !ok {
		return nil, fmt.Errorf("storage provider %q does not support tenant open", cfg.Driver)
	}
	return roleProvider, nil
}

func (r *Registry) tenantMaintenanceProvider(cfg Config) (TenantMaintenanceProvider, error) {
	provider, err := r.provider(cfg.Driver)
	if err != nil {
		return nil, err
	}
	if err := provider.Validate(cfg); err != nil {
		return nil, err
	}
	roleProvider, ok := provider.(TenantMaintenanceProvider)
	if !ok {
		return nil, fmt.Errorf("storage provider %q does not support tenant maintenance", cfg.Driver)
	}
	return roleProvider, nil
}
