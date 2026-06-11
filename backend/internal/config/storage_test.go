package config

import "testing"

func TestStorageConfigDefaultsToProductionDB(t *testing.T) {
	t.Setenv("FLOWSPACE_ENV", "")
	t.Setenv("FLOWSPACE_DB_PATH", "")

	cfg := LoadStorageConfig()

	if cfg.Environment != "prod" {
		t.Fatalf("expected prod environment, got %q", cfg.Environment)
	}
	if cfg.DBPath != "flowspace.db" {
		t.Fatalf("expected production db path, got %q", cfg.DBPath)
	}
}

func TestStorageConfigUsesTestDBWhenEnvironmentIsTest(t *testing.T) {
	t.Setenv("FLOWSPACE_ENV", "test")
	t.Setenv("FLOWSPACE_DB_PATH", "")

	cfg := LoadStorageConfig()

	if cfg.Environment != "test" {
		t.Fatalf("expected test environment, got %q", cfg.Environment)
	}
	if cfg.DBPath != "flowspace.test.db" {
		t.Fatalf("expected test db path, got %q", cfg.DBPath)
	}
}

func TestStorageConfigAllowsExplicitDBPathOverride(t *testing.T) {
	t.Setenv("FLOWSPACE_ENV", "test")
	t.Setenv("FLOWSPACE_DB_PATH", "tmp/custom-flowspace.db")

	cfg := LoadStorageConfig()

	if cfg.Environment != "test" {
		t.Fatalf("expected test environment, got %q", cfg.Environment)
	}
	if cfg.DBPath != "tmp/custom-flowspace.db" {
		t.Fatalf("expected explicit db path, got %q", cfg.DBPath)
	}
}
