package service

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestListLocalDirectoriesReturnsOnlyDirectoriesSorted(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"Zulu", "Alpha"} {
		if err := os.Mkdir(filepath.Join(root, name), 0755); err != nil {
			t.Fatalf("create directory %q: %v", name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "note.md"), []byte("body"), 0644); err != nil {
		t.Fatalf("create file: %v", err)
	}

	result, err := ListLocalDirectories(root)
	if err != nil {
		t.Fatalf("ListLocalDirectories(): %v", err)
	}

	wantCurrent, err := filepath.Abs(root)
	if err != nil {
		t.Fatalf("resolve temp root: %v", err)
	}
	if result.CurrentPath != wantCurrent {
		t.Fatalf("CurrentPath = %q, want %q", result.CurrentPath, wantCurrent)
	}
	if result.ParentPath == "" {
		t.Fatal("ParentPath should be available for a nested temp directory")
	}
	if len(result.Entries) != 2 {
		t.Fatalf("expected 2 directory entries, got %+v", result.Entries)
	}
	if result.Entries[0].Name != "Alpha" || result.Entries[1].Name != "Zulu" {
		t.Fatalf("entries are not sorted by name: %+v", result.Entries)
	}
	if result.Entries[0].Path != filepath.Join(wantCurrent, "Alpha") {
		t.Fatalf("first entry path = %q", result.Entries[0].Path)
	}
	if result.Entries[0].ModifiedAt == 0 {
		t.Fatal("directory entries should include a modified timestamp")
	}
}

func TestListLocalDirectoriesRejectsFilePath(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "note.md")
	if err := os.WriteFile(filePath, []byte("body"), 0644); err != nil {
		t.Fatalf("create file: %v", err)
	}

	_, err := ListLocalDirectories(filePath)
	if !errors.Is(err, ErrLocalDirectoryNotDirectory) {
		t.Fatalf("expected ErrLocalDirectoryNotDirectory, got %v", err)
	}
}
