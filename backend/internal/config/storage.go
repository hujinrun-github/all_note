package config

import (
	"os"
	"strings"
)

const (
	EnvironmentProduction = "prod"
	EnvironmentTest       = "test"

	ProductionDBPath = "flowspace.db"
	TestDBPath       = "flowspace.test.db"
)

type StorageConfig struct {
	Environment string
	DBPath      string
}

func LoadStorageConfig() StorageConfig {
	environment := normalizeEnvironment(os.Getenv("FLOWSPACE_ENV"))
	dbPath := strings.TrimSpace(os.Getenv("FLOWSPACE_DB_PATH"))
	if dbPath == "" {
		dbPath = defaultDBPath(environment)
	}

	return StorageConfig{
		Environment: environment,
		DBPath:      dbPath,
	}
}

func normalizeEnvironment(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "test", "testing":
		return EnvironmentTest
	default:
		return EnvironmentProduction
	}
}

func defaultDBPath(environment string) string {
	if environment == EnvironmentTest {
		return TestDBPath
	}
	return ProductionDBPath
}
