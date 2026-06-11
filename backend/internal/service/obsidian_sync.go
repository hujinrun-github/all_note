package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/repository"
)

var invalidMarkdownFileNameChars = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]+`)

var reservedWindowsFileNames = map[string]struct{}{
	"CON": {}, "PRN": {}, "AUX": {}, "NUL": {},
	"COM1": {}, "COM2": {}, "COM3": {}, "COM4": {}, "COM5": {}, "COM6": {}, "COM7": {}, "COM8": {}, "COM9": {},
	"LPT1": {}, "LPT2": {}, "LPT3": {}, "LPT4": {}, "LPT5": {}, "LPT6": {}, "LPT7": {}, "LPT8": {}, "LPT9": {},
}

func TestObsidianTarget(target *model.SyncTarget) error {
	if target == nil {
		return errors.New("sync target is required")
	}
	if target.Type != "obsidian" {
		return fmt.Errorf("expected obsidian sync target, got %q", target.Type)
	}
	if !target.Enabled {
		return errors.New("obsidian sync target is disabled")
	}
	if strings.TrimSpace(target.VaultPath) == "" {
		return errors.New("obsidian vault path is required")
	}
	if strings.TrimSpace(target.BaseFolder) == "" {
		return errors.New("obsidian base folder is required")
	}

	info, err := os.Stat(target.VaultPath)
	if err != nil {
		return fmt.Errorf("obsidian vault path is unavailable: %w", err)
	}
	if !info.IsDir() {
		return errors.New("obsidian vault path must be a directory")
	}

	baseDir, err := targetBaseDir(target)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return fmt.Errorf("create obsidian base folder: %w", err)
	}
	if err := verifyRealBaseDir(target); err != nil {
		return err
	}
	return nil
}

func SyncNoteToObsidian(noteID string) (*model.SyncResultItem, error) {
	if strings.TrimSpace(noteID) == "" {
		return nil, errors.New("note id is required")
	}

	target, err := repository.GetDefaultSyncTarget("obsidian")
	if err != nil {
		return nil, fmt.Errorf("load obsidian sync target: %w", err)
	}
	if err := TestObsidianTarget(target); err != nil {
		return nil, err
	}

	note, err := repository.GetNoteByID(noteID)
	if err != nil {
		return nil, fmt.Errorf("load note: %w", err)
	}
	return writeNoteToTarget(note, target)
}

func SyncNotesToObsidian(notes []model.Note) model.SyncBatchResult {
	target, err := repository.GetDefaultSyncTarget("obsidian")
	if err != nil {
		return failedBatch(notes, fmt.Errorf("load obsidian sync target: %w", err))
	}
	if err := TestObsidianTarget(target); err != nil {
		return failedBatch(notes, err)
	}

	result := model.SyncBatchResult{
		Items: make([]model.SyncResultItem, 0, len(notes)),
	}
	for i := range notes {
		item, err := writeNoteToTarget(&notes[i], target)
		if err != nil {
			result.Failed++
			result.Items = append(result.Items, model.SyncResultItem{
				NoteID:       notes[i].ID,
				Status:       "failed",
				ErrorMessage: err.Error(),
			})
			continue
		}
		result.Synced++
		result.Items = append(result.Items, *item)
	}
	return result
}

func writeNoteToTarget(note *model.Note, target *model.SyncTarget) (*model.SyncResultItem, error) {
	if note == nil {
		return nil, errors.New("note is required")
	}
	if target == nil {
		return nil, errors.New("sync target is required")
	}

	markdown := renderObsidianMarkdown(note)
	sum := sha256.Sum256([]byte(markdown))
	contentHash := hex.EncodeToString(sum[:])

	fileName := sanitizeMarkdownFileName(note.Title)
	if fileName == ".md" {
		fileName = fmt.Sprintf("Untitled-%s.md", note.ID)
	}

	outputPath, err := resolveUniqueOutputPath(note, target, fileName)
	if err != nil {
		if recordErr := recordSyncFailure(note, target, "", contentHash, err); recordErr != nil {
			return nil, fmt.Errorf("%w; failed to record sync state: %v", err, recordErr)
		}
		return nil, err
	}

	return writeNoteToOutputPath(note, target, outputPath)
}

func writeNoteToOutputPath(note *model.Note, target *model.SyncTarget, outputPath string) (*model.SyncResultItem, error) {
	if note == nil {
		return nil, errors.New("note is required")
	}
	if target == nil {
		return nil, errors.New("sync target is required")
	}

	markdown := renderObsidianMarkdown(note)
	sum := sha256.Sum256([]byte(markdown))
	contentHash := hex.EncodeToString(sum[:])

	validatedOutputPath, err := validateObsidianWritePath(outputPath, target)
	if err != nil {
		wrapped := fmt.Errorf("validate obsidian note path: %w", err)
		if recordErr := recordSyncFailure(note, target, outputPath, contentHash, wrapped); recordErr != nil {
			return nil, fmt.Errorf("%w; failed to record sync state: %v", wrapped, recordErr)
		}
		return nil, wrapped
	}
	outputPath = validatedOutputPath

	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		wrapped := fmt.Errorf("create obsidian note folder: %w", err)
		if recordErr := recordSyncFailure(note, target, outputPath, contentHash, wrapped); recordErr != nil {
			return nil, fmt.Errorf("%w; failed to record sync state: %v", wrapped, recordErr)
		}
		return nil, wrapped
	}
	if err := os.WriteFile(outputPath, []byte(markdown), 0644); err != nil {
		wrapped := fmt.Errorf("write obsidian note: %w", err)
		if recordErr := recordSyncFailure(note, target, outputPath, contentHash, wrapped); recordErr != nil {
			return nil, fmt.Errorf("%w; failed to record sync state: %v", wrapped, recordErr)
		}
		return nil, wrapped
	}
	info, err := os.Stat(outputPath)
	if err != nil {
		wrapped := fmt.Errorf("stat obsidian note: %w", err)
		if recordErr := recordSyncFailure(note, target, outputPath, contentHash, wrapped); recordErr != nil {
			return nil, fmt.Errorf("%w; failed to record sync state: %v", wrapped, recordErr)
		}
		return nil, wrapped
	}

	now := time.Now().Unix()
	externalMTime := info.ModTime().Unix()
	if err := repository.UpsertSyncState(&model.SyncState{
		NoteID:        note.ID,
		TargetID:      target.ID,
		ExternalPath:  outputPath,
		ContentHash:   contentHash,
		ExternalHash:  contentHash,
		ExternalMTime: &externalMTime,
		LastDirection: "push",
		LastSyncedAt:  &now,
		Status:        "synced",
		ErrorMessage:  nil,
	}); err != nil {
		return nil, fmt.Errorf("record sync state: %w", err)
	}

	return &model.SyncResultItem{
		NoteID:       note.ID,
		Status:       "synced",
		ExternalPath: outputPath,
	}, nil
}

func recordSyncFailure(note *model.Note, target *model.SyncTarget, outputPath, contentHash string, cause error) error {
	if note == nil || target == nil || note.ID == "" || target.ID == "" || cause == nil {
		return nil
	}
	message := cause.Error()
	state := model.SyncState{
		NoteID:        note.ID,
		TargetID:      target.ID,
		ExternalPath:  outputPath,
		ContentHash:   contentHash,
		LastDirection: "push",
		LastSyncedAt:  nil,
		Status:        "failed",
		ErrorMessage:  &message,
	}
	if prior, err := repository.GetSyncState(note.ID, target.ID); err == nil && prior != nil {
		if state.ExternalPath == "" {
			state.ExternalPath = prior.ExternalPath
		}
		state.ExternalHash = prior.ExternalHash
		state.ExternalMTime = prior.ExternalMTime
		state.LastSyncedAt = prior.LastSyncedAt
	}
	return repository.UpsertSyncState(&state)
}

func renderObsidianMarkdown(note *model.Note) string {
	var b strings.Builder
	tags := parseTags(note.Tags)

	b.WriteString("---\n")
	b.WriteString(fmt.Sprintf("id: %s\n", note.ID))
	b.WriteString("source: flowspace\n")
	b.WriteString(fmt.Sprintf("folder: %s\n", yamlString(note.FolderID)))
	b.WriteString(fmt.Sprintf("created: %s\n", unixTime(note.CreatedAt)))
	b.WriteString(fmt.Sprintf("updated: %s\n", unixTime(note.UpdatedAt)))
	if len(tags) == 0 {
		b.WriteString("tags: []\n")
	} else {
		b.WriteString("tags:\n")
		for _, tag := range tags {
			b.WriteString(fmt.Sprintf("  - %s\n", yamlString(tag)))
		}
	}
	b.WriteString("---\n\n")
	b.WriteString(fmt.Sprintf("# %s\n\n", note.Title))
	b.WriteString(note.Body)
	if !strings.HasSuffix(note.Body, "\n") {
		b.WriteString("\n")
	}

	return b.String()
}

func sanitizeMarkdownFileName(title string) string {
	fileName := invalidMarkdownFileNameChars.ReplaceAllString(title, "-")
	fileName = strings.Trim(fileName, " .-")
	if fileName == "" {
		return ".md"
	}
	if !strings.HasSuffix(strings.ToLower(fileName), ".md") {
		fileName += ".md"
	}
	stem := strings.TrimSuffix(fileName, filepath.Ext(fileName))
	if _, reserved := reservedWindowsFileNames[strings.ToUpper(stem)]; reserved {
		fileName = stem + "-note" + filepath.Ext(fileName)
	}
	return fileName
}

func resolveUniqueOutputPath(note *model.Note, target *model.SyncTarget, fileName string) (string, error) {
	outputPath, err := resolveOutputPath(target, fileName)
	if err != nil {
		return "", err
	}
	if canUseOutputPath(note, target, outputPath) {
		return outputPath, nil
	}

	uniqueName := appendNoteIDToFileName(fileName, note.ID)
	outputPath, err = resolveOutputPath(target, uniqueName)
	if err != nil {
		return "", err
	}
	if canUseOutputPath(note, target, outputPath) {
		return outputPath, nil
	}

	for i := 2; i <= 100; i++ {
		candidateName := appendFileNameSuffix(uniqueName, fmt.Sprint(i))
		outputPath, err = resolveOutputPath(target, candidateName)
		if err != nil {
			return "", err
		}
		if canUseOutputPath(note, target, outputPath) {
			return outputPath, nil
		}
	}
	return "", fmt.Errorf("no available obsidian note file name for %q", fileName)
}

func canUseOutputPath(note *model.Note, target *model.SyncTarget, outputPath string) bool {
	if _, err := os.Stat(outputPath); errors.Is(err, os.ErrNotExist) {
		return true
	}
	if note == nil || target == nil || note.ID == "" || target.ID == "" {
		return false
	}
	state, err := repository.GetSyncState(note.ID, target.ID)
	if err != nil || state == nil || state.ExternalPath == "" {
		return false
	}
	statePath, err := filepath.Abs(state.ExternalPath)
	if err != nil {
		return false
	}
	outputAbs, err := filepath.Abs(outputPath)
	if err != nil {
		return false
	}
	return strings.EqualFold(statePath, outputAbs)
}

func validateObsidianWritePath(outputPath string, target *model.SyncTarget) (string, error) {
	if strings.TrimSpace(outputPath) == "" {
		return "", errors.New("output path is required")
	}
	outputAbs, err := filepath.Abs(outputPath)
	if err != nil {
		return "", fmt.Errorf("resolve output path: %w", err)
	}
	baseDir, err := targetBaseDir(target)
	if err != nil {
		return "", err
	}
	if !isPathWithin(outputAbs, baseDir) {
		return "", fmt.Errorf("output path escapes base folder: %s", outputPath)
	}
	if err := verifyRealBaseDir(target); err != nil {
		return "", err
	}

	realBase, err := filepath.EvalSymlinks(baseDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return outputAbs, nil
		}
		return "", fmt.Errorf("resolve real obsidian base folder: %w", err)
	}
	baseInfo, err := os.Stat(realBase)
	if err != nil {
		return "", fmt.Errorf("stat real obsidian base folder: %w", err)
	}
	if !baseInfo.IsDir() {
		return "", errors.New("obsidian base folder must be a directory")
	}
	if err := validateObsidianWritePathComponents(outputAbs, baseDir, realBase); err != nil {
		return "", err
	}
	return outputAbs, nil
}

func validateObsidianWritePathComponents(outputPath, baseDir, realBase string) error {
	rel, err := filepath.Rel(baseDir, outputPath)
	if err != nil {
		return fmt.Errorf("resolve output relative path: %w", err)
	}
	components := splitRelativePath(rel)
	if len(components) == 0 {
		return errors.New("output path is not a note file")
	}

	current := baseDir
	for i, component := range components {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect output path component: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("output path component is a symlink")
		}

		isFinalComponent := i == len(components)-1
		if isFinalComponent {
			if info.IsDir() {
				return errors.New("output path is a directory")
			}
			if !info.Mode().IsRegular() {
				return errors.New("output path is not a regular file")
			}
			return nil
		}
		if !info.IsDir() {
			return errors.New("output path component is not a directory")
		}
		realCurrent, err := filepath.EvalSymlinks(current)
		if err != nil {
			return fmt.Errorf("resolve real output path component: %w", err)
		}
		if !isPathWithin(realCurrent, realBase) {
			return errors.New("output path component resolves outside obsidian base folder")
		}
	}
	return nil
}

func appendNoteIDToFileName(fileName, noteID string) string {
	ext := filepath.Ext(fileName)
	stem := strings.TrimSuffix(fileName, ext)
	if ext == "" {
		ext = ".md"
	}
	suffix := noteID
	if len(suffix) > 8 {
		suffix = suffix[:8]
	}
	if strings.TrimSpace(suffix) == "" {
		suffix = "note"
	}
	return fmt.Sprintf("%s-%s%s", stem, suffix, ext)
}

func appendFileNameSuffix(fileName, suffix string) string {
	ext := filepath.Ext(fileName)
	stem := strings.TrimSuffix(fileName, ext)
	if ext == "" {
		ext = ".md"
	}
	return fmt.Sprintf("%s-%s%s", stem, suffix, ext)
}

func resolveOutputPath(target *model.SyncTarget, fileName string) (string, error) {
	if strings.TrimSpace(fileName) == "" {
		return "", errors.New("file name is required")
	}
	if filepath.IsAbs(fileName) {
		return "", errors.New("file name must be relative")
	}

	baseDir, err := targetBaseDir(target)
	if err != nil {
		return "", err
	}

	outputPath, err := filepath.Abs(filepath.Join(baseDir, fileName))
	if err != nil {
		return "", fmt.Errorf("resolve output path: %w", err)
	}
	if !isPathWithin(outputPath, baseDir) {
		return "", fmt.Errorf("output path escapes base folder: %s", fileName)
	}
	if err := verifyRealBaseDir(target); err != nil {
		return "", err
	}
	return outputPath, nil
}

func parseTags(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}

	var tags []string
	if err := json.Unmarshal([]byte(raw), &tags); err != nil {
		return nil
	}

	cleaned := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag != "" {
			cleaned = append(cleaned, tag)
		}
	}
	return cleaned
}

func failedBatch(notes []model.Note, err error) model.SyncBatchResult {
	result := model.SyncBatchResult{
		Failed: len(notes),
		Items:  make([]model.SyncResultItem, 0, len(notes)),
	}
	for _, note := range notes {
		result.Items = append(result.Items, model.SyncResultItem{
			NoteID:       note.ID,
			Status:       "failed",
			ErrorMessage: err.Error(),
		})
	}
	return result
}

func targetBaseDir(target *model.SyncTarget) (string, error) {
	if target == nil {
		return "", errors.New("sync target is required")
	}
	if strings.TrimSpace(target.VaultPath) == "" {
		return "", errors.New("obsidian vault path is required")
	}
	if filepath.IsAbs(target.BaseFolder) {
		return "", errors.New("obsidian base folder must be relative")
	}

	vaultDir, err := filepath.Abs(target.VaultPath)
	if err != nil {
		return "", fmt.Errorf("resolve obsidian vault path: %w", err)
	}

	baseDir, err := filepath.Abs(filepath.Join(vaultDir, target.BaseFolder))
	if err != nil {
		return "", fmt.Errorf("resolve obsidian base folder: %w", err)
	}
	if !isPathWithin(baseDir, vaultDir) {
		return "", fmt.Errorf("obsidian base folder escapes vault path: %s", target.BaseFolder)
	}
	return baseDir, nil
}

func verifyRealBaseDir(target *model.SyncTarget) error {
	vaultDir, err := filepath.Abs(target.VaultPath)
	if err != nil {
		return fmt.Errorf("resolve obsidian vault path: %w", err)
	}
	baseDir, err := targetBaseDir(target)
	if err != nil {
		return err
	}
	realVault, err := filepath.EvalSymlinks(vaultDir)
	if err != nil {
		return fmt.Errorf("resolve real obsidian vault path: %w", err)
	}
	realBase, err := filepath.EvalSymlinks(baseDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("resolve real obsidian base folder: %w", err)
	}
	if !isPathWithin(realBase, realVault) {
		return fmt.Errorf("obsidian base folder resolves outside vault path: %s", target.BaseFolder)
	}
	return nil
}

func isPathWithin(path, baseDir string) bool {
	rel, err := filepath.Rel(baseDir, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel))
}

func unixTime(value int64) string {
	if value <= 0 {
		return ""
	}
	return time.Unix(value, 0).UTC().Format(time.RFC3339)
}

func yamlString(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return `""`
	}
	return string(encoded)
}
