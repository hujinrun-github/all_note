package storage

import (
	"context"
	"testing"
)

type roleProviderStub struct{}

func (roleProviderStub) OpenControl(context.Context, Config) (Store, error) { return nil, nil }
func (roleProviderStub) MigrateControl(context.Context, Config) error       { return nil }
func (roleProviderStub) OpenTenant(context.Context, Config, string) (Store, error) {
	return nil, nil
}
func (roleProviderStub) MigrateTenant(context.Context, Config) error { return nil }
func (roleProviderStub) AdoptExistingTenant(context.Context, Config, AdoptManifest) error {
	return nil
}

func TestProviderRoleInterfacesRemainSeparate(t *testing.T) {
	var provider roleProviderStub
	var _ ControlProvider = provider
	var _ TenantProvider = provider
	var _ TenantMaintenanceProvider = provider
}
