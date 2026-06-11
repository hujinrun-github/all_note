package service

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
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

type obsidianMarkdownScanFailure struct {
	Path string
	Err  error
}

func scanObsidianMarkdownFiles(target *model.SyncTarget) ([]obsidianMarkdownFile, error) {
	files, failures, err := scanObsidianMarkdownFilesWithFailures(target)
	if err != nil {
		return nil, err
	}
	if len(failures) > 0 {
		return nil, failures[0].Err
	}
	return files, nil
}

func scanObsidianMarkdownFilesWithFailures(target *model.SyncTarget) ([]obsidianMarkdownFile, []obsidianMarkdownScanFailure, error) {
	baseDir, err := targetBaseDir(target)
	if err != nil {
		return nil, nil, err
	}
	if err := verifyRealBaseDir(target); err != nil {
		return nil, nil, err
	}

	files := make([]obsidianMarkdownFile, 0)
	failures := make([]obsidianMarkdownScanFailure, 0)
	err = filepath.WalkDir(baseDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			failures = append(failures, obsidianMarkdownScanFailure{
				Path: path,
				Err:  fmt.Errorf("walk obsidian markdown file: %w", walkErr),
			})
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
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
			failures = append(failures, obsidianMarkdownScanFailure{
				Path: path,
				Err:  fmt.Errorf("resolve obsidian markdown file path: %w", err),
			})
			return nil
		}
		if !isPathWithin(absPath, baseDir) {
			failures = append(failures, obsidianMarkdownScanFailure{
				Path: absPath,
				Err:  fmt.Errorf("obsidian markdown file escapes base folder: %s", path),
			})
			return nil
		}

		info, err := entry.Info()
		if err != nil {
			failures = append(failures, obsidianMarkdownScanFailure{
				Path: absPath,
				Err:  fmt.Errorf("stat obsidian markdown file: %w", err),
			})
			return nil
		}
		raw, err := os.ReadFile(absPath)
		if err != nil {
			failures = append(failures, obsidianMarkdownScanFailure{
				Path: absPath,
				Err:  fmt.Errorf("read obsidian markdown file: %w", err),
			})
			return nil
		}
		note, err := parseObsidianMarkdown(raw, entry.Name())
		if err != nil {
			failures = append(failures, obsidianMarkdownScanFailure{
				Path: absPath,
				Err:  fmt.Errorf("parse obsidian markdown file: %w", err),
			})
			return nil
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
		return nil, nil, err
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].Path < files[j].Path
	})
	sort.Slice(failures, func(i, j int) bool {
		return failures[i].Path < failures[j].Path
	})
	return files, failures, nil
}
