package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadCredentialsConfigRequiresBothValuesAndRedactsPath(t *testing.T) {
	t.Setenv("FLOWSPACE_CREDENTIALS_ACTIVE_KEY_ID", "")
	t.Setenv("FLOWSPACE_CREDENTIALS_KEYRING_FILE", "")
	if _, err := LoadCredentialsConfig(); err == nil {
		t.Fatal("empty credential config accepted")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials-keyring.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLOWSPACE_CREDENTIALS_ACTIVE_KEY_ID", "active-2026")
	t.Setenv("FLOWSPACE_CREDENTIALS_KEYRING_FILE", path)
	cfg, err := LoadCredentialsConfig()
	if err != nil {
		t.Fatal(err)
	}
	summary := cfg.SafeSummary()
	if strings.Contains(summary, dir) || !strings.Contains(summary, "credentials-keyring.json") {
		t.Fatalf("unsafe credential config summary: %s", summary)
	}
}
