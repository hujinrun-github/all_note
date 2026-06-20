package config

import "testing"

func TestServerConfigDefaultsToProductionPort(t *testing.T) {
	t.Setenv("PORT", "")

	cfg := LoadServerConfig(EnvironmentProduction)

	if cfg.Port != "4201" {
		t.Fatalf("expected production port 4201, got %q", cfg.Port)
	}
}

func TestServerConfigDefaultsToTestPort(t *testing.T) {
	t.Setenv("PORT", "")

	cfg := LoadServerConfig(EnvironmentTest)

	if cfg.Port != "4101" {
		t.Fatalf("expected test port 4101, got %q", cfg.Port)
	}
}

func TestServerConfigAllowsExplicitPortOverride(t *testing.T) {
	t.Setenv("PORT", "19090")

	cfg := LoadServerConfig(EnvironmentTest)

	if cfg.Port != "19090" {
		t.Fatalf("expected explicit port override, got %q", cfg.Port)
	}
}
