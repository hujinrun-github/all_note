package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProtectedServicesDoNotUseGlobalRepositoryFacade(t *testing.T) {
	root := protectedServiceTestRoot(t)
	blocked := []string{"repository.", "context.Background()"}
	allowlist := map[string]string{
		"internal/service/notion_bidirectional.go":  "Task 9: background sync engine storage remains temporarily global while sync SQL is scoped.",
		"internal/service/obsidian_bidirectional.go": "Task 9: background sync engine storage remains temporarily global while sync SQL is scoped.",
		"internal/service/obsidian_sync.go":          "Task 9: background sync engine storage remains temporarily global while sync SQL is scoped.",
	}

	for _, dir := range []string{"internal/service", "internal/handler"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") || entry.Name() == "health.go" {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if reason, ok := allowlist[rel]; ok {
				t.Logf("allowing %s for now: %s", rel, reason)
				return nil
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			text := string(content)
			for _, needle := range blocked {
				if strings.Contains(text, needle) {
					t.Errorf("%s contains %q; pass ctx and storage.Store instead", rel, needle)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
}

func protectedServiceTestRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find backend go.mod")
		}
		dir = parent
	}
}
