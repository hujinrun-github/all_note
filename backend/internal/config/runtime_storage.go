package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type DatabaseRole string

const (
	DatabaseRoleControl      DatabaseRole = "control"
	DatabaseRolePlatformData DatabaseRole = "platform_data"
)

type DatabaseDriver string

const (
	DatabaseDriverPostgres DatabaseDriver = "postgres"
	DatabaseDriverSQLite   DatabaseDriver = "sqlite"
)

type InstanceMode string

const (
	InstanceModeSingle InstanceMode = "single"
	InstanceModeMulti  InstanceMode = "multi"
)

type DatabaseConfig struct {
	Role       DatabaseRole
	Driver     DatabaseDriver
	URL        string
	SQLitePath string
}

type RuntimeStorageConfig struct {
	Environment             string
	InstanceMode            InstanceMode
	Control                 DatabaseConfig
	PlatformData            DatabaseConfig
	UsedLegacyUpgradeConfig bool
}

type RuntimeStorageLoadOptions struct {
	AllowLegacyUpgrade bool
}

func LoadRuntimeStorageConfig(environment string, options RuntimeStorageLoadOptions) (RuntimeStorageConfig, error) {
	environment = normalizeEnvironment(environment)
	instanceMode := InstanceMode(strings.ToLower(strings.TrimSpace(os.Getenv("FLOWSPACE_INSTANCE_MODE"))))
	if instanceMode == "" {
		instanceMode = InstanceModeSingle
	}

	control := loadRoleDatabaseConfig(
		DatabaseRoleControl,
		"FLOWSPACE_CONTROL_DATABASE_DRIVER",
		"FLOWSPACE_CONTROL_DATABASE_URL",
		"FLOWSPACE_CONTROL_SQLITE_PATH",
	)
	platformData := loadRoleDatabaseConfig(
		DatabaseRolePlatformData,
		"FLOWSPACE_PLATFORM_DATA_DATABASE_DRIVER",
		"FLOWSPACE_PLATFORM_DATA_DATABASE_URL",
		"FLOWSPACE_PLATFORM_DATA_SQLITE_PATH",
	)

	usedLegacy := false
	if databaseConfigEmpty(control) && databaseConfigEmpty(platformData) {
		legacy := loadLegacyUpgradeDatabaseConfig()
		if !databaseConfigEmpty(legacy) {
			if !options.AllowLegacyUpgrade {
				return RuntimeStorageConfig{}, fmt.Errorf("legacy database configuration is upgrade-only; set FLOWSPACE_CONTROL_DATABASE_URL and FLOWSPACE_PLATFORM_DATA_DATABASE_URL explicitly")
			}
			control = legacy
			control.Role = DatabaseRoleControl
			platformData = legacy
			platformData.Role = DatabaseRolePlatformData
			usedLegacy = true
		}
	}

	cfg := RuntimeStorageConfig{
		Environment:             environment,
		InstanceMode:            instanceMode,
		Control:                 control,
		PlatformData:            platformData,
		UsedLegacyUpgradeConfig: usedLegacy,
	}
	if err := ValidateRuntimeStorageConfig(cfg); err != nil {
		return RuntimeStorageConfig{}, err
	}
	return cfg, nil
}

func ValidateRuntimeStorageConfig(cfg RuntimeStorageConfig) error {
	if cfg.InstanceMode != InstanceModeSingle && cfg.InstanceMode != InstanceModeMulti {
		return fmt.Errorf("unsupported FLOWSPACE_INSTANCE_MODE %q", cfg.InstanceMode)
	}
	if err := validateRoleDatabaseConfig(cfg.Environment, DatabaseRoleControl, cfg.Control); err != nil {
		return err
	}
	if err := validateRoleDatabaseConfig(cfg.Environment, DatabaseRolePlatformData, cfg.PlatformData); err != nil {
		return err
	}
	if cfg.InstanceMode == InstanceModeMulti {
		if cfg.Control.Driver == DatabaseDriverSQLite || cfg.PlatformData.Driver == DatabaseDriverSQLite {
			return fmt.Errorf("multi-instance deployments require PostgreSQL control and platform data databases")
		}
	}
	return nil
}

func (cfg RuntimeStorageConfig) SafeSummary() string {
	return fmt.Sprintf(
		"env=%s instance_mode=%s control=%s platform_data=%s",
		cfg.Environment,
		cfg.InstanceMode,
		safeDatabaseSummary(cfg.Control),
		safeDatabaseSummary(cfg.PlatformData),
	)
}

func loadRoleDatabaseConfig(role DatabaseRole, driverKey, urlKey, sqlitePathKey string) DatabaseConfig {
	driver := DatabaseDriver(strings.ToLower(strings.TrimSpace(os.Getenv(driverKey))))
	rawURL := strings.TrimSpace(os.Getenv(urlKey))
	sqlitePath := strings.TrimSpace(os.Getenv(sqlitePathKey))
	if driver == "" {
		driver = DatabaseDriverPostgres
	}
	return DatabaseConfig{Role: role, Driver: driver, URL: rawURL, SQLitePath: sqlitePath}
}

func loadLegacyUpgradeDatabaseConfig() DatabaseConfig {
	driver := DatabaseDriver(strings.ToLower(strings.TrimSpace(os.Getenv("FLOWSPACE_DATABASE_DRIVER"))))
	rawURL := strings.TrimSpace(os.Getenv("FLOWSPACE_DATABASE_URL"))
	sqlitePath := strings.TrimSpace(os.Getenv("FLOWSPACE_SQLITE_PATH"))
	if sqlitePath == "" {
		sqlitePath = strings.TrimSpace(os.Getenv("FLOWSPACE_DB_PATH"))
	}
	if driver == "" {
		if rawURL != "" {
			driver = DatabaseDriverPostgres
		} else if sqlitePath != "" {
			driver = DatabaseDriverSQLite
		}
	}
	return DatabaseConfig{Driver: driver, URL: rawURL, SQLitePath: sqlitePath}
}

func databaseConfigEmpty(cfg DatabaseConfig) bool {
	return strings.TrimSpace(cfg.URL) == "" && strings.TrimSpace(cfg.SQLitePath) == ""
}

func validateRoleDatabaseConfig(environment string, expectedRole DatabaseRole, cfg DatabaseConfig) error {
	if cfg.Role != expectedRole {
		return fmt.Errorf("database role is %q, want %q", cfg.Role, expectedRole)
	}
	switch cfg.Driver {
	case DatabaseDriverPostgres:
		if strings.TrimSpace(cfg.URL) == "" {
			return fmt.Errorf("%s PostgreSQL URL is required", expectedRole)
		}
		if strings.TrimSpace(cfg.SQLitePath) != "" {
			return fmt.Errorf("%s PostgreSQL config cannot include a SQLite path", expectedRole)
		}
		parsed, err := url.Parse(cfg.URL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return fmt.Errorf("invalid %s PostgreSQL URL", expectedRole)
		}
		databaseName := strings.TrimPrefix(parsed.Path, "/")
		if databaseName == "" {
			return fmt.Errorf("%s PostgreSQL URL must include a database name", expectedRole)
		}
		if normalizeEnvironment(environment) == EnvironmentTest && !strings.Contains(strings.ToLower(databaseName), "test") {
			return fmt.Errorf("test environment cannot use non-test %s database %q", expectedRole, databaseName)
		}
	case DatabaseDriverSQLite:
		if strings.TrimSpace(cfg.SQLitePath) == "" {
			return fmt.Errorf("%s SQLite path is required", expectedRole)
		}
		if strings.TrimSpace(cfg.URL) != "" {
			return fmt.Errorf("%s SQLite config cannot include a PostgreSQL URL", expectedRole)
		}
		if normalizeEnvironment(environment) == EnvironmentTest && !strings.Contains(strings.ToLower(filepath.Base(cfg.SQLitePath)), "test") {
			return fmt.Errorf("test environment cannot use non-test %s SQLite file %q", expectedRole, cfg.SQLitePath)
		}
	default:
		return fmt.Errorf("unsupported %s database driver %q", expectedRole, cfg.Driver)
	}
	return nil
}

func safeDatabaseSummary(cfg DatabaseConfig) string {
	if cfg.Driver == DatabaseDriverSQLite {
		return fmt.Sprintf("%s:sqlite:%s", cfg.Role, filepath.Base(cfg.SQLitePath))
	}
	parsed, err := url.Parse(cfg.URL)
	if err != nil {
		return fmt.Sprintf("%s:%s:invalid", cfg.Role, cfg.Driver)
	}
	databaseName := strings.TrimPrefix(parsed.Path, "/")
	return fmt.Sprintf("%s:%s:%s/%s", cfg.Role, cfg.Driver, parsed.Host, databaseName)
}
