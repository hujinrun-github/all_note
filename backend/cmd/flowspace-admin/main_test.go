package main

import (
	"context"
	"testing"

	"github.com/hujinrun/flowspace/internal/config"
	"github.com/hujinrun/flowspace/internal/storage"
)

type fakeMaintenanceRegistry struct {
	migrateControl int
	migrateTenant  int
	adoptTenant    int
	lastConfig     storage.Config
	lastManifest   storage.AdoptManifest
}

func (f *fakeMaintenanceRegistry) MigrateControl(_ context.Context, cfg storage.Config) error {
	f.migrateControl++
	f.lastConfig = cfg
	return nil
}

func (f *fakeMaintenanceRegistry) MigrateTenant(_ context.Context, cfg storage.Config) error {
	f.migrateTenant++
	f.lastConfig = cfg
	return nil
}

func (f *fakeMaintenanceRegistry) AdoptExistingTenant(_ context.Context, cfg storage.Config, manifest storage.AdoptManifest) error {
	f.adoptTenant++
	f.lastConfig = cfg
	f.lastManifest = manifest
	return nil
}

func TestRunAdminCommandRoutesMigrateControl(t *testing.T) {
	registry := &fakeMaintenanceRegistry{}
	cfg := adminTestRuntimeConfig()

	if err := runAdminCommand(context.Background(), []string{"migrate-control"}, cfg, registry); err != nil {
		t.Fatalf("run migrate-control: %v", err)
	}
	if registry.migrateControl != 1 || registry.migrateTenant != 0 || registry.adoptTenant != 0 {
		t.Fatalf("unexpected calls: %+v", registry)
	}
	if registry.lastConfig.URL != cfg.Control.URL {
		t.Fatalf("migrate-control used %q, want control URL", registry.lastConfig.URL)
	}
}

func TestRunAdminCommandRoutesMigrateTenant(t *testing.T) {
	registry := &fakeMaintenanceRegistry{}
	cfg := adminTestRuntimeConfig()

	if err := runAdminCommand(context.Background(), []string{"migrate-tenant"}, cfg, registry); err != nil {
		t.Fatalf("run migrate-tenant: %v", err)
	}
	if registry.migrateTenant != 1 || registry.migrateControl != 0 || registry.adoptTenant != 0 {
		t.Fatalf("unexpected calls: %+v", registry)
	}
	if registry.lastConfig.URL != cfg.PlatformData.URL {
		t.Fatalf("migrate-tenant used %q, want platform data URL", registry.lastConfig.URL)
	}
}

func TestRunAdminCommandRoutesAdoptTenantWithManifest(t *testing.T) {
	registry := &fakeMaintenanceRegistry{}
	cfg := adminTestRuntimeConfig()

	err := runAdminCommand(context.Background(), []string{
		"adopt-tenant", "--manifest-id", "legacy-v1", "--manifest-checksum", "abc123",
	}, cfg, registry)
	if err != nil {
		t.Fatalf("run adopt-tenant: %v", err)
	}
	if registry.adoptTenant != 1 || registry.migrateControl != 0 || registry.migrateTenant != 0 {
		t.Fatalf("unexpected calls: %+v", registry)
	}
	if registry.lastManifest.ID != "legacy-v1" || registry.lastManifest.Checksum != "abc123" {
		t.Fatalf("unexpected manifest: %+v", registry.lastManifest)
	}
}

func TestRunAdminCommandRejectsUnknownCommand(t *testing.T) {
	err := runAdminCommand(context.Background(), []string{"serve"}, adminTestRuntimeConfig(), &fakeMaintenanceRegistry{})
	if err == nil {
		t.Fatal("expected unsupported admin command to fail")
	}
}

func adminTestRuntimeConfig() config.RuntimeStorageConfig {
	return config.RuntimeStorageConfig{
		Environment:  "test",
		InstanceMode: config.InstanceModeSingle,
		Control: config.DatabaseConfig{
			Role:   config.DatabaseRoleControl,
			Driver: config.DatabaseDriverPostgres,
			URL:    "postgres://control:secret@db.test:5432/flowspace_control_test?sslmode=disable",
		},
		PlatformData: config.DatabaseConfig{
			Role:   config.DatabaseRolePlatformData,
			Driver: config.DatabaseDriverPostgres,
			URL:    "postgres://tenant:secret@db.test:5432/flowspace_tenant_test?sslmode=disable",
		},
	}
}
