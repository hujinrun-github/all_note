package storage

import "testing"

func TestLoadStorageConfigDefaultsToPostgresDriver(t *testing.T) {
	t.Setenv("FLOWSPACE_DATABASE_DRIVER", "")
	t.Setenv("FLOWSPACE_DATABASE_URL", "")
	t.Setenv("FLOWSPACE_SQLITE_PATH", "")

	cfg := LoadStorageConfig("test")

	if cfg.Driver != DriverPostgres {
		t.Fatalf("expected postgres driver, got %q", cfg.Driver)
	}
	if cfg.URL != "" {
		t.Fatalf("expected empty postgres URL, got %q", cfg.URL)
	}
}

func TestLoadStorageConfigReadsPostgresURL(t *testing.T) {
	t.Setenv("FLOWSPACE_DATABASE_DRIVER", "postgres")
	t.Setenv("FLOWSPACE_DATABASE_URL", "postgres://flowspace:flowspace@127.0.0.1:15432/flowspace_test?sslmode=disable")
	cfg := LoadStorageConfig("test")

	if cfg.Driver != DriverPostgres {
		t.Fatalf("expected postgres driver, got %q", cfg.Driver)
	}
	if cfg.URL != "postgres://flowspace:flowspace@127.0.0.1:15432/flowspace_test?sslmode=disable" {
		t.Fatalf("unexpected URL: %q", cfg.URL)
	}
	if cfg.Name != "flowspace_test" {
		t.Fatalf("expected database name flowspace_test, got %q", cfg.Name)
	}
}

func TestLoadStorageConfigReadsSQLitePath(t *testing.T) {
	t.Setenv("FLOWSPACE_DATABASE_DRIVER", "sqlite")
	t.Setenv("FLOWSPACE_SQLITE_PATH", "backend/flowspace.test.db")

	cfg := LoadStorageConfig("test")

	if cfg.Driver != DriverSQLite {
		t.Fatalf("expected sqlite driver, got %q", cfg.Driver)
	}
	if cfg.SQLitePath != "backend/flowspace.test.db" {
		t.Fatalf("unexpected sqlite path: %q", cfg.SQLitePath)
	}
}

func TestValidateStorageConfigRejectsUnknownDriver(t *testing.T) {
	cfg := Config{Env: "test", Driver: Driver("mysql")}

	if err := ValidateStorageConfig(cfg); err == nil {
		t.Fatal("expected unknown driver to fail")
	}
}

func TestValidateStorageConfigRejectsMissingProviderConfig(t *testing.T) {
	if err := ValidateStorageConfig(Config{Env: "test", Driver: DriverPostgres}); err == nil {
		t.Fatal("expected postgres without URL to fail")
	}
	if err := ValidateStorageConfig(Config{Env: "test", Driver: DriverSQLite}); err == nil {
		t.Fatal("expected sqlite without path to fail")
	}
}

func TestValidateStorageConfigRejectsMalformedPostgresURL(t *testing.T) {
	cfg := Config{Env: "test", Driver: DriverPostgres, URL: "postgres://flowspace:%zz@127.0.0.1:15432/flowspace_test"}

	if err := ValidateStorageConfig(cfg); err == nil {
		t.Fatal("expected malformed postgres URL to fail")
	}
}

func TestValidateStorageConfigRejectsPostgresURLWithoutDatabaseName(t *testing.T) {
	cfg := Config{Env: "test", Driver: DriverPostgres, URL: "postgres://flowspace:flowspace@127.0.0.1:15432?sslmode=disable"}

	if err := ValidateStorageConfig(cfg); err == nil {
		t.Fatal("expected postgres URL without database name to fail")
	}
}

func TestValidateStorageConfigRejectsTestEnvPointingAtProdDatabase(t *testing.T) {
	cfg := Config{Env: "test", Driver: DriverPostgres, URL: "postgres://flowspace:flowspace@127.0.0.1:15432/flowspace_prod?sslmode=disable"}

	if err := ValidateStorageConfig(cfg); err == nil {
		t.Fatal("expected test environment to reject flowspace_prod URL")
	}
}

func TestValidateStorageConfigRejectsProdEnvPointingAtTestDatabase(t *testing.T) {
	cfg := Config{Env: "prod", Driver: DriverPostgres, URL: "postgres://flowspace:flowspace@127.0.0.1:15432/flowspace_test?sslmode=disable"}

	if err := ValidateStorageConfig(cfg); err == nil {
		t.Fatal("expected prod environment to reject flowspace_test URL")
	}
}

func TestValidateStorageConfigRejectsSQLiteEnvMismatch(t *testing.T) {
	if err := ValidateStorageConfig(Config{Env: "test", Driver: DriverSQLite, SQLitePath: "backend/flowspace.db"}); err == nil {
		t.Fatal("expected test environment to reject prod sqlite file")
	}
	if err := ValidateStorageConfig(Config{Env: "prod", Driver: DriverSQLite, SQLitePath: "backend/flowspace.test.db"}); err == nil {
		t.Fatal("expected prod environment to reject test sqlite file")
	}
}
