package service

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/hujinrun/flowspace/internal/model"
)

var ErrLocalDirectoryNotDirectory = errors.New("path is not a directory")

func ListLocalDirectories(rawPath string) (*model.LocalDirectoryList, error) {
	path := strings.TrimSpace(rawPath)
	if path == "" {
		return &model.LocalDirectoryList{
			Entries: defaultLocalDirectoryEntries(),
		}, nil
	}

	currentPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve directory path: %w", err)
	}
	info, err := os.Stat(currentPath)
	if err != nil {
		return nil, fmt.Errorf("inspect directory: %w", err)
	}
	if !info.IsDir() {
		return nil, ErrLocalDirectoryNotDirectory
	}

	entries, err := os.ReadDir(currentPath)
	if err != nil {
		return nil, fmt.Errorf("read directory: %w", err)
	}

	directories := make([]model.LocalDirectoryEntry, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		directories = append(directories, model.LocalDirectoryEntry{
			Name:       entry.Name(),
			Path:       filepath.Join(currentPath, entry.Name()),
			ModifiedAt: info.ModTime().Unix(),
		})
	}
	sortDirectoryEntries(directories)

	parentPath := filepath.Dir(currentPath)
	if parentPath == currentPath {
		parentPath = ""
	}

	return &model.LocalDirectoryList{
		CurrentPath: currentPath,
		ParentPath:  parentPath,
		Entries:     directories,
	}, nil
}

func defaultLocalDirectoryEntries() []model.LocalDirectoryEntry {
	entries := make([]model.LocalDirectoryEntry, 0, 8)
	seen := make(map[string]struct{})
	addIfDirectory := func(name, path string) {
		if strings.TrimSpace(path) == "" {
			return
		}
		absPath, err := filepath.Abs(path)
		if err != nil {
			return
		}
		info, err := os.Stat(absPath)
		if err != nil || !info.IsDir() {
			return
		}
		key := strings.ToLower(filepath.Clean(absPath))
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		entries = append(entries, model.LocalDirectoryEntry{
			Name:       name,
			Path:       absPath,
			ModifiedAt: info.ModTime().Unix(),
		})
	}

	if home, err := os.UserHomeDir(); err == nil {
		addIfDirectory("用户目录", home)
		addIfDirectory("桌面", filepath.Join(home, "Desktop"))
		addIfDirectory("文档", filepath.Join(home, "Documents"))
	}
	if cwd, err := os.Getwd(); err == nil {
		addIfDirectory("当前项目", cwd)
	}

	if runtime.GOOS == "windows" {
		for drive := 'A'; drive <= 'Z'; drive++ {
			root := fmt.Sprintf("%c:\\", drive)
			addIfDirectory(root, root)
		}
	} else {
		addIfDirectory("/", "/")
	}

	return entries
}

func sortDirectoryEntries(entries []model.LocalDirectoryEntry) {
	sort.Slice(entries, func(i, j int) bool {
		left := strings.ToLower(entries[i].Name)
		right := strings.ToLower(entries[j].Name)
		if left == right {
			return entries[i].Name < entries[j].Name
		}
		return left < right
	})
}
