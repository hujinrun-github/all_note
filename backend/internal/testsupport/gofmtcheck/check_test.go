package gofmtcheck

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckFindsOnlyUnformattedGoFiles(t *testing.T) {
	dir := t.TempDir()
	formatted := filepath.Join(dir, "formatted.go")
	unformatted := filepath.Join(dir, "unformatted.go")
	nonGo := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(formatted, []byte("package sample\n\nconst Value = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unformatted, []byte("package sample\nconst Other=2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nonGo, []byte("ignored"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Check([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.ToSlash(unformatted)
	if len(got) != 1 || got[0] != want {
		t.Fatalf("Check() = %v, want [%s]", got, want)
	}
}
