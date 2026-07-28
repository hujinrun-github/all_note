package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/config"
	"github.com/hujinrun/flowspace/internal/storage"
	storagesqlite "github.com/hujinrun/flowspace/internal/storage/sqlite"
	"github.com/hujinrun/flowspace/internal/taskruntime"
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

func TestAdminCommandTimeoutAllowsLongRunningTaskMigration(t *testing.T) {
	if got := adminCommandTimeout([]string{"task-migration-run-to-ready"}); got != 60*time.Minute {
		t.Fatalf("task migration timeout = %s", got)
	}
	if got := adminCommandTimeout([]string{"migrate-tenant"}); got != 10*time.Minute {
		t.Fatalf("maintenance timeout = %s", got)
	}
}

func TestRunAdminCommandInspectsAndNoopsFreshV2Workspace(t *testing.T) {
	ctx := context.Background()
	tenantPath := filepath.Join(t.TempDir(), "flowspace-admin-tenant-test.db")
	cfg := config.RuntimeStorageConfig{
		Environment:  "test",
		InstanceMode: config.InstanceModeSingle,
		Control: config.DatabaseConfig{
			Role:       config.DatabaseRoleControl,
			Driver:     config.DatabaseDriverSQLite,
			SQLitePath: filepath.Join(t.TempDir(), "flowspace-admin-control-test.db"),
		},
		PlatformData: config.DatabaseConfig{
			Role:       config.DatabaseRolePlatformData,
			Driver:     config.DatabaseDriverSQLite,
			SQLitePath: tenantPath,
		},
	}
	registry := storage.NewRegistry()
	if err := registry.Register(storagesqlite.Provider{}); err != nil {
		t.Fatal(err)
	}
	tenantConfig := toStorageConfig(cfg.Environment, cfg.PlatformData)
	if err := registry.MigrateTenant(ctx, tenantConfig); err != nil {
		t.Fatal(err)
	}
	store, err := registry.OpenTenant(ctx, tenantConfig, taskruntime.ExpectedTenantSchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	sqlStore := store.(storage.SQLStore)
	if _, err := sqlStore.SQLDB().ExecContext(ctx, `INSERT INTO tenant_workspaces(workspace_id) VALUES(?)`, "fresh-v2-workspace"); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	if err := runAdminCommand(ctx, []string{
		"task-migration-status", "--workspace-id", "fresh-v2-workspace",
	}, cfg, registry); err != nil {
		t.Fatalf("status fresh v2 workspace: %v", err)
	}
	if err := runAdminCommand(ctx, []string{
		"task-migration-run-to-ready",
		"--workspace-id", "fresh-v2-workspace",
		"--migration-id", "unused-for-fresh-v2",
		"--migration-timezone", "UTC",
		"--confirm-fence-legacy-writes",
	}, cfg, registry); err != nil {
		t.Fatalf("run-to-ready fresh v2 workspace: %v", err)
	}
}

func TestRunAdminCommandRequiresLegacyWriteFenceConfirmation(t *testing.T) {
	err := runAdminCommand(context.Background(), []string{
		"task-migration-run-to-ready",
		"--workspace-id", "workspace-1",
		"--migration-id", "migration-1",
		"--migration-timezone", "UTC",
	}, adminTestRuntimeConfig(), &fakeMaintenanceRegistry{})
	if err == nil || err.Error() != "task-migration-run-to-ready requires --confirm-fence-legacy-writes" {
		t.Fatalf("error = %v", err)
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
