package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hujinrun/flowspace/internal/model"
)

type obsidianMarkdownFile struct {
	Path  string
	Raw   []byte
	Hash  string
	MTime int64
	Note  *obsidianParsedMarkdown
}

func scanObsidianMarkdownFiles(target *model.SyncTarget) ([]obsidianMarkdownFile, error) {
	baseDir, err := targetBaseDir(target)
	if err != nil {
		return nil, err
	}
	if err := verifyRealBaseDir(target); err != nil {
		return nil, err
	}

	files := make([]obsidianMarkdownFile, 0)
	err = filepath.WalkDir(baseDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != baseDir && strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			return nil
		}

		absPath, err := filepath.Abs(path)
		if err != nil {
			return fmt.Errorf("resolve obsidian markdown file path: %w", err)
		}
		if !isPathWithin(absPath, baseDir) {
			return fmt.Errorf("obsidian markdown file escapes base folder: %s", path)
		}

		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("stat obsidian markdown file: %w", err)
		}
		raw, err := os.ReadFile(absPath)
		if err != nil {
			return fmt.Errorf("read obsidian markdown file: %w", err)
		}
		note, err := parseObsidianMarkdown(raw, entry.Name())
		if err != nil {
			return fmt.Errorf("parse obsidian markdown file: %w", err)
		}

		files = append(files, obsidianMarkdownFile{
			Path:  absPath,
			Raw:   raw,
			Hash:  note.Hash,
			MTime: info.ModTime().Unix(),
			Note:  note,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}
