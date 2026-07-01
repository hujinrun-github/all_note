package config

import (
	"strings"
	"testing"
	"time"
)

var authEnvVars = []string{
	"FLOWSPACE_BOOTSTRAP_ADMIN_EMAIL",
	"FLOWSPACE_BOOTSTRAP_ADMIN_PASSWORD",
	"FLOWSPACE_BOOTSTRAP_ADMIN_NAME",
	"FLOWSPACE_SESSION_SECRET",
	"FLOWSPACE_ALLOWED_ORIGINS",
	"FLOWSPACE_COOKIE_SECURE",
	"FLOWSPACE_SESSION_TTL",
	"FLOWSPACE_REMEMBER_SESSION_TTL",
	"FLOWSPACE_LOGIN_MAX_FAILURES",
	"FLOWSPACE_LOGIN_WINDOW",
	"FLOWSPACE_SESSION_CLEANUP_INTERVAL",
	"FLOWSPACE_ENABLE_LOCAL_DIRECTORY_BROWSER",
	"FLOWSPACE_ALLOWED_LOCAL_ROOTS",
}

func clearAuthEnv(t *testing.T) {
	t.Helper()
	for _, name := range authEnvVars {
		t.Setenv(name, "")
	}
}

func TestLoadAuthConfigRequiresSessionSecretInProd(t *testing.T) {
	clearAuthEnv(t)
	t.Setenv("FLOWSPACE_SESSION_SECRET", "")
	t.Setenv("FLOWSPACE_ALLOWED_ORIGINS", "https://flowspace.example.com")

	_, err := LoadAuthConfig(EnvironmentProduction)
	if err == nil {
		t.Fatal("expected FLOWSPACE_SESSION_SECRET to be required in prod")
	}
	if !strings.Contains(err.Error(), "FLOWSPACE_SESSION_SECRET") {
		t.Fatalf("error = %q, want env var name", err.Error())
	}
}

func TestLoadAuthConfigNormalizesProductionLikeEnvironments(t *testing.T) {
	tests := []struct {
		name        string
		environment string
	}{
		{name: "empty", environment: ""},
		{name: "prod mixed case", environment: "Prod"},
		{name: "production", environment: "production"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearAuthEnv(t)
			t.Setenv("FLOWSPACE_SESSION_SECRET", "")
			t.Setenv("FLOWSPACE_ALLOWED_ORIGINS", "https://flowspace.example.com")

			_, err := LoadAuthConfig(tt.environment)
			if err == nil {
				t.Fatal("expected FLOWSPACE_SESSION_SECRET to be required")
			}
			if !strings.Contains(err.Error(), "FLOWSPACE_SESSION_SECRET") {
				t.Fatalf("error = %q, want env var name", err.Error())
			}
		})
	}
}

func TestLoadAuthConfigRejectsShortSessionSecretInProd(t *testing.T) {
	clearAuthEnv(t)
	t.Setenv("FLOWSPACE_SESSION_SECRET", "too-short")
	t.Setenv("FLOWSPACE_ALLOWED_ORIGINS", "https://flowspace.example.com")

	_, err := LoadAuthConfig(EnvironmentProduction)
	if err == nil {
		t.Fatal("expected short FLOWSPACE_SESSION_SECRET to fail in prod")
	}
	if !strings.Contains(err.Error(), "FLOWSPACE_SESSION_SECRET") {
		t.Fatalf("error = %q, want env var name", err.Error())
	}
}

func TestLoadAuthConfigRequiresAllowedOriginsInProd(t *testing.T) {
	clearAuthEnv(t)
	t.Setenv("FLOWSPACE_SESSION_SECRET", "prod-session-secret-with-at-least-32-bytes")
	t.Setenv("FLOWSPACE_ALLOWED_ORIGINS", "")

	_, err := LoadAuthConfig(EnvironmentProduction)
	if err == nil {
		t.Fatal("expected FLOWSPACE_ALLOWED_ORIGINS to be required in prod")
	}
	if !strings.Contains(err.Error(), "FLOWSPACE_ALLOWED_ORIGINS") {
		t.Fatalf("error = %q, want env var name", err.Error())
	}
}

func TestLoadAuthConfigRejectsInsecureCookieInProd(t *testing.T) {
	clearAuthEnv(t)
	t.Setenv("FLOWSPACE_SESSION_SECRET", "prod-session-secret-with-at-least-32-bytes")
	t.Setenv("FLOWSPACE_ALLOWED_ORIGINS", "https://flowspace.example.com")
	t.Setenv("FLOWSPACE_COOKIE_SECURE", "false")

	_, err := LoadAuthConfig(EnvironmentProduction)
	if err == nil {
		t.Fatal("expected FLOWSPACE_COOKIE_SECURE=false to fail in prod")
	}
	if !strings.Contains(err.Error(), "FLOWSPACE_COOKIE_SECURE") {
		t.Fatalf("error = %q, want env var name", err.Error())
	}
}

func TestLoadAuthConfigParsesRequiredSecuritySettings(t *testing.T) {
	clearAuthEnv(t)
	t.Setenv("FLOWSPACE_BOOTSTRAP_ADMIN_EMAIL", "admin@example.com")
	t.Setenv("FLOWSPACE_BOOTSTRAP_ADMIN_PASSWORD", "abc12345")
	t.Setenv("FLOWSPACE_BOOTSTRAP_ADMIN_NAME", "Admin")
	t.Setenv("FLOWSPACE_SESSION_SECRET", "prod-session-secret-with-at-least-32-bytes")
	t.Setenv("FLOWSPACE_ALLOWED_ORIGINS", "https://flowspace.example.com,http://localhost:5173")
	t.Setenv("FLOWSPACE_COOKIE_SECURE", "true")
	t.Setenv("FLOWSPACE_ENABLE_LOCAL_DIRECTORY_BROWSER", "false")

	cfg, err := LoadAuthConfig(EnvironmentProduction)
	if err != nil {
		t.Fatalf("load auth config: %v", err)
	}
	if cfg.SessionSecret != "prod-session-secret-with-at-least-32-bytes" {
		t.Fatalf("session secret not loaded")
	}
	if len(cfg.AllowedOrigins) != 2 || cfg.AllowedOrigins[0] != "https://flowspace.example.com" {
		t.Fatalf("allowed origins = %#v", cfg.AllowedOrigins)
	}
	if !cfg.Cookie.Secure {
		t.Fatal("cookie secure should be true")
	}
	if cfg.Session.ShortTTL != 12*time.Hour {
		t.Fatalf("short session ttl = %s, want 12h", cfg.Session.ShortTTL)
	}
	if cfg.Session.RememberTTL != 30*24*time.Hour {
		t.Fatalf("remember session ttl = %s, want 720h", cfg.Session.RememberTTL)
	}
}

func TestLoadAuthConfigRejectsMalformedConfiguredValues(t *testing.T) {
	tests := []struct {
		name   string
		envVar string
		value  string
	}{
		{name: "invalid bool", envVar: "FLOWSPACE_COOKIE_SECURE", value: "maybe"},
		{name: "invalid int", envVar: "FLOWSPACE_LOGIN_MAX_FAILURES", value: "abc"},
		{name: "invalid duration", envVar: "FLOWSPACE_SESSION_TTL", value: "30d"},
		{name: "non-positive int", envVar: "FLOWSPACE_LOGIN_MAX_FAILURES", value: "0"},
		{name: "non-positive duration", envVar: "FLOWSPACE_SESSION_TTL", value: "0s"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearAuthEnv(t)
			t.Setenv(tt.envVar, tt.value)

			_, err := LoadAuthConfig(EnvironmentTest)
			if err == nil {
				t.Fatal("expected malformed env value error")
			}
			if !strings.Contains(err.Error(), tt.envVar) {
				t.Fatalf("error = %q, want env var name %s", err.Error(), tt.envVar)
			}
		})
	}
}

func TestLoadAuthConfigDefaultsAllowedOriginsOutsideProd(t *testing.T) {
	tests := []struct {
		name        string
		environment string
	}{
		{name: "test", environment: "test"},
		{name: "testing", environment: "testing"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearAuthEnv(t)

			cfg, err := LoadAuthConfig(tt.environment)
			if err != nil {
				t.Fatalf("load auth config: %v", err)
			}
			if len(cfg.AllowedOrigins) != 1 || cfg.AllowedOrigins[0] != "http://localhost:5173" {
				t.Fatalf("allowed origins = %#v, want dev default", cfg.AllowedOrigins)
			}
		})
	}
}

func TestLoadAuthConfigRejectsInvalidAllowedOrigins(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{name: "malformed", value: "not-a-url"},
		{name: "unsupported scheme", value: "ftp://flowspace.example.com"},
		{name: "wildcard", value: "*"},
		{name: "wildcard host", value: "https://*.flowspace.example.com"},
		{name: "comma only", value: " , , "},
		{name: "empty segment", value: "https://a.com,,https://b.com"},
		{name: "mixed invalid", value: "https://flowspace.example.com,not-a-url"},
		{name: "missing hostname with port", value: "http://:5173"},
		{name: "missing hostname", value: "http://"},
		{name: "hostname with underscore", value: "https://exa_mple.com"},
		{name: "hostname label starts with hyphen", value: "https://-bad.com"},
		{name: "hostname label ends with hyphen", value: "https://bad-.com"},
		{name: "hostname with empty label", value: "https://bad..com"},
		{name: "hostname with leading dot", value: "https://.bad.com"},
		{name: "invalid ipv4-looking host", value: "http://999.999.999.999"},
		{name: "non numeric port", value: "https://flowspace.example.com:notaport"},
		{name: "empty port", value: "https://flowspace.example.com:"},
		{name: "port out of range", value: "https://flowspace.example.com:65536"},
		{name: "path", value: "https://flowspace.example.com/path"},
		{name: "trailing slash", value: "https://flowspace.example.com/"},
		{name: "query", value: "https://flowspace.example.com?x=1"},
		{name: "fragment", value: "https://flowspace.example.com#frag"},
		{name: "userinfo", value: "https://user@flowspace.example.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearAuthEnv(t)
			t.Setenv("FLOWSPACE_ALLOWED_ORIGINS", tt.value)

			_, err := LoadAuthConfig(EnvironmentTest)
			if err == nil {
				t.Fatal("expected invalid allowed origin error")
			}
			if !strings.Contains(err.Error(), "FLOWSPACE_ALLOWED_ORIGINS") {
				t.Fatalf("error = %q, want env var name", err.Error())
			}
		})
	}
}

func TestLoadAuthConfigAcceptsLiteralAllowedOrigins(t *testing.T) {
	clearAuthEnv(t)
	t.Setenv("FLOWSPACE_ALLOWED_ORIGINS", "http://localhost:5173,https://flowspace.example.com,https://flowspace.example.com:443,http://127.0.0.1:5173,http://[::1]:5173")

	cfg, err := LoadAuthConfig(EnvironmentTest)
	if err != nil {
		t.Fatalf("load auth config: %v", err)
	}
	want := []string{
		"http://localhost:5173",
		"https://flowspace.example.com",
		"https://flowspace.example.com:443",
		"http://127.0.0.1:5173",
		"http://[::1]:5173",
	}
	if len(cfg.AllowedOrigins) != len(want) {
		t.Fatalf("allowed origins = %#v, want %#v", cfg.AllowedOrigins, want)
	}
	for i := range want {
		if cfg.AllowedOrigins[i] != want[i] {
			t.Fatalf("allowed origins = %#v, want %#v", cfg.AllowedOrigins, want)
		}
	}
}
