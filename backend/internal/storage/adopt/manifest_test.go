package adopt

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManifestChecksumAndScopeFailClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	contents := []byte(`{"id":"legacy-v1","provider":"sqlite","role":"tenant","required_tables":["notes"]}`)
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := manifest.Verify("legacy-v1", manifest.Checksum, "sqlite", "tenant"); err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}
	for _, input := range []struct{ id, checksum, provider, role string }{
		{"other", manifest.Checksum, "sqlite", "tenant"},
		{"legacy-v1", "bad", "sqlite", "tenant"},
		{"legacy-v1", manifest.Checksum, "postgres", "tenant"},
		{"legacy-v1", manifest.Checksum, "sqlite", "control"},
	} {
		if err := manifest.Verify(input.id, input.checksum, input.provider, input.role); err == nil {
			t.Fatalf("expected mismatch for %+v", input)
		}
	}
}
