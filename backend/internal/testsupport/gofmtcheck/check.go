package gofmtcheck

import (
	"bytes"
	"fmt"
	"go/format"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Check returns Go files whose contents differ from gofmt output.
func Check(paths []string) ([]string, error) {
	var unformatted []string
	seen := make(map[string]struct{})
	for _, root := range paths {
		info, err := os.Stat(root)
		if err != nil {
			return nil, fmt.Errorf("stat %s: %w", root, err)
		}
		if !info.IsDir() {
			if err := checkFile(root, seen, &unformatted); err != nil {
				return nil, err
			}
			continue
		}
		err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
				return nil
			}
			return checkFile(path, seen, &unformatted)
		})
		if err != nil {
			return nil, fmt.Errorf("walk %s: %w", root, err)
		}
	}
	sort.Strings(unformatted)
	return unformatted, nil
}

func checkFile(path string, seen map[string]struct{}, unformatted *[]string) error {
	clean := filepath.Clean(path)
	if _, ok := seen[clean]; ok {
		return nil
	}
	seen[clean] = struct{}{}
	source, err := os.ReadFile(clean)
	if err != nil {
		return fmt.Errorf("read %s: %w", clean, err)
	}
	formatted, err := format.Source(source)
	if err != nil {
		return fmt.Errorf("format %s: %w", clean, err)
	}
	if !bytes.Equal(source, formatted) {
		*unformatted = append(*unformatted, filepath.ToSlash(clean))
	}
	return nil
}
