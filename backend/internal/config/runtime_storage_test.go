package config

import (
	"strings"
	"testing"
)

func TestLoadRuntimeStorageConfigSeparatesControlAndPlatformData(t *testing.T) {
	clearRuntimeStorageEnv(t)
	t.Setenv("FLOWSPACE_CONTROL_DATABASE_DRIVER", "postgres")
	t.Setenv("FLOWSPACE_CONTROL_DATABASE_URL", "postgres://control:control-secret@db.test:5432/flowspace_control_test?sslmode=disable")
	t.Setenv("FLOWSPACE_PLATFORM_DATA_DATABASE_DRIVER", "postgres")
	t.Setenv("FLOWSPACE_PLATFORM_DATA_DATABASE_URL", "postgres://tenant:tenant-secret@tenant.test:5432/flowspace_tenant_test?sslmode=disable")

	cfg, err := LoadRuntimeStorageConfig("test", RuntimeStorageLoadOptions{})
	if err != nil {
		t.Fatalf("load runtime storage config: %v", err)
	}
	if cfg.Control.URL == cfg.PlatformData.URL {
		t.Fatal("control and platform data URLs must remain independently scoped")
	}
	if cfg.Control.URL != "postgres://control:control-secret@db.test:5432/flowspace_control_test?sslmode=disable" {
		t.Fatalf("unexpected control URL: %q", cfg.Control.URL)
	}
	if cfg.PlatformData.URL != "postgres://tenant:tenant-secret@tenant.test:5432/flowspace_tenant_test?sslmode=disable" {
		t.Fatalf("unexpected platform data URL: %q", cfg.PlatformData.URL)
	}
}

func TestLoadRuntimeStorageConfigAllowsSamePhysicalURLWithSeparateRoles(t *testing.T) {
	clearRuntimeStorageEnv(t)
	const sharedURL = "postgres://flowspace:secret@db.test:5432/flowspace_test?sslmode=disable"
	t.Setenv("FLOWSPACE_CONTROL_DATABASE_URL", sharedURL)
	t.Setenv("FLOWSPACE_PLATFORM_DATA_DATABASE_URL", sharedURL)

	cfg, err := LoadRuntimeStorageConfig("test", RuntimeStorageLoadOptions{})
	if err != nil {
		t.Fatalf("load runtime storage config: %v", err)
	}
	if cfg.Control.Role != DatabaseRoleControl {
		t.Fatalf("control role = %q", cfg.Control.Role)
	}
	if cfg.PlatformData.Role != DatabaseRolePlatformData {
		t.Fatalf("platform data role = %q", cfg.PlatformData.Role)
	}
}

func TestLoadRuntimeStorageConfigRejectsLegacyDatabaseURLForServer(t *testing.T) {
	clearRuntimeStorageEnv(t)
	t.Setenv("FLOWSPACE_DATABASE_URL", "postgres://legacy:secret@db.test:5432/flowspace_test?sslmode=disable")

	_, err := LoadRuntimeStorageConfig("test", RuntimeStorageLoadOptions{})
	if err == nil || !strings.Contains(err.Error(), "FLOWSPACE_CONTROL_DATABASE_URL") {
		t.Fatalf("expected legacy-only config to require explicit upgrade, got %v", err)
	}
}

func TestLoadRuntimeStorageConfigAllowsLegacyURLOnlyForUpgrade(t *testing.T) {
	clearRuntimeStorageEnv(t)
	const legacyURL = "postgres://legacy:secret@db.test:5432/flowspace_test?sslmode=disable"
	t.Setenv("FLOWSPACE_DATABASE_URL", legacyURL)

	cfg, err := LoadRuntimeStorageConfig("test", RuntimeStorageLoadOptions{AllowLegacyUpgrade: true})
	if err != nil {
		t.Fatalf("load upgrade config: %v", err)
	}
	if cfg.Control.URL != legacyURL || cfg.PlatformData.URL != legacyURL {
		t.Fatalf("legacy upgrade must seed both roles, got control=%q data=%q", cfg.Control.URL, cfg.PlatformData.URL)
	}
	if !cfg.UsedLegacyUpgradeConfig {
		t.Fatal("expected legacy upgrade usage to be explicit")
	}
}

func TestValidateRuntimeStorageConfigRejectsSQLiteInMultiInstanceMode(t *testing.T) {
	cfg := RuntimeStorageConfig{
		Environment:  "test",
		InstanceMode: InstanceModeMulti,
		Control: DatabaseConfig{
			Role:       DatabaseRoleControl,
			Driver:     DatabaseDriverSQLite,
			SQLitePath: "control.test.db",
		},
		PlatformData: DatabaseConfig{
			Role:   DatabaseRolePlatformData,
			Driver: DatabaseDriverPostgres,
			URL:    "postgres://tenant:secret@db.test:5432/flowspace_tenant_test?sslmode=disable",
		},
	}

	if err := ValidateRuntimeStorageConfig(cfg); err == nil {
		t.Fatal("expected multi-instance SQLite control to be rejected")
	}
}

func TestValidateRuntimeStorageConfigRejectsDriverConfigMismatch(t *testing.T) {
	cfg := RuntimeStorageConfig{
		Environment:  "test",
		InstanceMode: InstanceModeSingle,
		Control: DatabaseConfig{
			Role:       DatabaseRoleControl,
			Driver:     DatabaseDriverSQLite,
			URL:        "postgres://control:secret@db.test:5432/flowspace_control_test?sslmode=disable",
			SQLitePath: "",
		},
		PlatformData: DatabaseConfig{
			Role:   DatabaseRolePlatformData,
			Driver: DatabaseDriverPostgres,
			URL:    "postgres://tenant:secret@db.test:5432/flowspace_tenant_test?sslmode=disable",
		},
	}

	if err := ValidateRuntimeStorageConfig(cfg); err == nil {
		t.Fatal("expected sqlite driver with postgres URL to fail")
	}
}

func TestRuntimeStorageSafeSummaryRedactsCredentials(t *testing.T) {
	cfg := RuntimeStorageConfig{
		Environment:  "test",
		InstanceMode: InstanceModeSingle,
		Control: DatabaseConfig{
			Role:   DatabaseRoleControl,
			Driver: DatabaseDriverPostgres,
			URL:    "postgres://control:control-secret@db.test:5432/flowspace_control_test?sslmode=disable",
		},
		PlatformData: DatabaseConfig{
			Role:   DatabaseRolePlatformData,
			Driver: DatabaseDriverPostgres,
			URL:    "postgres://tenant:tenant-secret@tenant.test:5432/flowspace_tenant_test?sslmode=disable",
		},
	}

	summary := cfg.SafeSummary()
	for _, secret := range []string{"control-secret", "tenant-secret", "control@", "tenant@"} {
		if strings.Contains(summary, secret) {
			t.Fatalf("safe summary leaked %q: %s", secret, summary)
		}
	}
	if !strings.Contains(summary, "flowspace_control_test") || !strings.Contains(summary, "flowspace_tenant_test") {
		t.Fatalf("safe summary should retain database names: %s", summary)
	}
}

func clearRuntimeStorageEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"FLOWSPACE_CONTROL_DATABASE_DRIVER",
		"FLOWSPACE_CONTROL_DATABASE_URL",
		"FLOWSPACE_CONTROL_SQLITE_PATH",
		"FLOWSPACE_PLATFORM_DATA_DATABASE_DRIVER",
		"FLOWSPACE_PLATFORM_DATA_DATABASE_URL",
		"FLOWSPACE_PLATFORM_DATA_SQLITE_PATH",
		"FLOWSPACE_INSTANCE_MODE",
		"FLOWSPACE_DATABASE_DRIVER",
		"FLOWSPACE_DATABASE_URL",
		"FLOWSPACE_SQLITE_PATH",
	} {
		t.Setenv(key, "")
	}
}
