package storage

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type Driver string

const (
	DriverPostgres Driver = "postgres"
	DriverSQLite   Driver = "sqlite"
)

type Config struct {
	Env        string
	Driver     Driver
	URL        string
	SQLitePath string
	Name       string
}

func LoadStorageConfig(environment string) Config {
	driver := Driver(strings.ToLower(strings.TrimSpace(os.Getenv("FLOWSPACE_DATABASE_DRIVER"))))
	if driver == "" {
		driver = DriverPostgres
	}
	rawURL := strings.TrimSpace(os.Getenv("FLOWSPACE_DATABASE_URL"))
	sqlitePath := strings.TrimSpace(os.Getenv("FLOWSPACE_SQLITE_PATH"))
	return Config{
		Env:        strings.ToLower(strings.TrimSpace(environment)),
		Driver:     driver,
		URL:        rawURL,
		SQLitePath: sqlitePath,
		Name:       databaseNameFromURL(rawURL),
	}
}

func ValidateStorageConfig(cfg Config) error {
	switch cfg.Driver {
	case DriverPostgres:
		if strings.TrimSpace(cfg.URL) == "" {
			return fmt.Errorf("FLOWSPACE_DATABASE_URL is required when FLOWSPACE_DATABASE_DRIVER=postgres")
		}
		dbName, err := parseDatabaseNameFromURL(cfg.URL)
		if err != nil {
			return err
		}
		if err := validateEnvironmentDatabase(cfg.Env, dbName); err != nil {
			return err
		}
	case DriverSQLite:
		if strings.TrimSpace(cfg.SQLitePath) == "" {
			return fmt.Errorf("FLOWSPACE_SQLITE_PATH is required when FLOWSPACE_DATABASE_DRIVER=sqlite")
		}
		if err := validateEnvironmentSQLitePath(cfg.Env, cfg.SQLitePath); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported FLOWSPACE_DATABASE_DRIVER %q", cfg.Driver)
	}
	return nil
}

func databaseNameFromURL(rawURL string) string {
	name, err := parseDatabaseNameFromURL(rawURL)
	if err != nil {
		return ""
	}
	return name
}

func parseDatabaseNameFromURL(rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("invalid FLOWSPACE_DATABASE_URL: %w", err)
	}
	databaseName := strings.TrimPrefix(parsed.Path, "/")
	if strings.TrimSpace(databaseName) == "" {
		return "", fmt.Errorf("FLOWSPACE_DATABASE_URL must include a database name")
	}
	return databaseName, nil
}

func validateEnvironmentDatabase(environment, databaseName string) error {
	switch normalizeEnvironmentName(environment) {
	case "test":
		if databaseName == "flowspace_prod" {
			return fmt.Errorf("FLOWSPACE_ENV=test cannot use prod database %q", databaseName)
		}
	case "prod":
		if databaseName == "flowspace_test" {
			return fmt.Errorf("FLOWSPACE_ENV=prod cannot use test database %q", databaseName)
		}
	}
	return nil
}

func validateEnvironmentSQLitePath(environment, sqlitePath string) error {
	name := filepath.Base(filepath.Clean(sqlitePath))
	switch normalizeEnvironmentName(environment) {
	case "test":
		if name == "flowspace.db" {
			return fmt.Errorf("FLOWSPACE_ENV=test cannot use prod sqlite file %q", sqlitePath)
		}
	case "prod":
		if name == "flowspace.test.db" {
			return fmt.Errorf("FLOWSPACE_ENV=prod cannot use test sqlite file %q", sqlitePath)
		}
	}
	return nil
}

func normalizeEnvironmentName(environment string) string {
	switch strings.ToLower(strings.TrimSpace(environment)) {
	case "test", "testing":
		return "test"
	default:
		return "prod"
	}
}
