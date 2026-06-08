package service

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/repository"
)

const bidirectionalPendingPushStatus = "pending_push"

var (
	ErrObsidianDeletionNotFound     = errors.New("obsidian deletion candidate not found")
	ErrObsidianDeletionConflict     = errors.New("obsidian deletion conflict")
	ErrObsidianDeletionInvalidState = errors.New("note is not marked as deleted in obsidian")
)

func SyncObsidianBidirectional() model.ObsidianBidirectionalResult {
	result := model.ObsidianBidirectionalResult{
		Items: make([]model.SyncResultItem, 0),
	}

	target, err := repository.GetDefaultSyncTarget("obsidian")
	if err != nil {
		return failedObsidianBidirectionalResult(fmt.Errorf("load obsidian sync target: %w", err))
	}
	if err := TestObsidianTarget(target); err != nil {
		return failedObsidianBidirectionalResult(err)
	}

	files, scanFailures, err := scanObsidianMarkdownFilesWithFailures(target)
	if err != nil {
		return failedObsidianBidirectionalResult(fmt.Errorf("scan obsidian markdown files: %w", err))
	}
	statesList, err := repository.ListSyncStatesByTarget(target.ID)
	if err != nil {
		return failedObsidianBidirectionalResult(fmt.Errorf("load obsidian sync states: %w", err))
	}
	notesList, err := repository.ListAllNotes()
	if err != nil {
		return failedObsidianBidirectionalResult(fmt.Errorf("load notes: %w", err))
	}

	notes := notesByID(notesList)
	states := statesByNoteID(statesList)
	statesByPath := statesByExternalPath(statesList)
	scannedPaths := make(map[string]struct{}, len(files))
	handledNoteIDs := make(map[string]struct{})

	for _, failure := range scanFailures {
		scannedPaths[normalizedPath(failure.Path)] = struct{}{}
		noteID := ""
		if state, ok := statesByPath[normalizedPath(failure.Path)]; ok {
			noteID = state.NoteID
			if noteID != "" {
				handledNoteIDs[noteID] = struct{}{}
			}
		}
		result.Failed++
		result.Items = append(result.Items, model.SyncResultItem{
			NoteID:       noteID,
			Status:       "failed",
			ExternalPath: failure.Path,
			ErrorMessage: failure.Err.Error(),
		})
	}

	for _, file := range files {
		scannedPaths[normalizedPath(file.Path)] = struct{}{}
		item := syncObsidianFile(file, target, notes, states)
		switch item.Status {
		case "imported":
			result.Imported++
			result.Items = append(result.Items, item)
			handledNoteIDs[item.NoteID] = struct{}{}
			refreshBidirectionalMaps(item.NoteID, target.ID, notes, states)
		case "pulled":
			result.Pulled++
			result.Items = append(result.Items, item)
			handledNoteIDs[item.NoteID] = struct{}{}
			refreshBidirectionalMaps(item.NoteID, target.ID, notes, states)
		case "failed":
			result.Failed++
			result.Items = append(result.Items, item)
			if item.NoteID != "" {
				handledNoteIDs[item.NoteID] = struct{}{}
			}
		case "synced":
			if item.NoteID != "" {
				handledNoteIDs[item.NoteID] = struct{}{}
			}
		}
	}

	for _, state := range statesList {
		if state.Status != "synced" {
			continue
		}
		if _, ok := handledNoteIDs[state.NoteID]; ok {
			continue
		}
		if _, ok := notes[state.NoteID]; !ok {
			continue
		}
		mappedPath, validMapping := validSyncStateExternalPath(state, target)
		if !validMapping {
			continue
		}
		if _, ok := scannedPaths[normalizedPath(mappedPath)]; ok {
			continue
		}

		item := markExternalDeleted(state, target)
		if item.Status == "failed" {
			result.Failed++
		} else {
			result.ExternalDeleted++
		}
		result.Items = append(result.Items, item)
		if item.NoteID != "" {
			handledNoteIDs[item.NoteID] = struct{}{}
		}
	}

	currentStatesList, err := repository.ListSyncStatesByTarget(target.ID)
	if err == nil {
		states = statesByNoteID(currentStatesList)
	} else {
		result.Failed++
		result.Items = append(result.Items, model.SyncResultItem{
			Status:       "failed",
			ErrorMessage: fmt.Errorf("reload obsidian sync states: %w", err).Error(),
		})
	}

	sort.Slice(notesList, func(i, j int) bool {
		return notesList[i].ID < notesList[j].ID
	})
	for i := range notesList {
		note := notesList[i]
		if _, ok := handledNoteIDs[note.ID]; ok {
			continue
		}

		state, hasState := states[note.ID]
		if hasState && state.Status == "external_deleted" {
			continue
		}
		mappedPath, validMapping := validSyncStateExternalPath(state, target)
		currentHash := markdownHash(renderObsidianMarkdown(&note))
		if hasState && state.Status == "synced" && validMapping && currentHash == state.ContentHash {
			continue
		}

		var item *model.SyncResultItem
		var err error
		if hasState && validMapping {
			item, err = writeNoteToOutputPath(&note, target, mappedPath)
		} else {
			item, err = writeNoteToTarget(&note, target)
		}
		if err != nil {
			result.Failed++
			result.Items = append(result.Items, model.SyncResultItem{
				NoteID:       note.ID,
				Status:       "failed",
				ErrorMessage: err.Error(),
			})
			continue
		}
		result.Pushed++
		result.Items = append(result.Items, model.SyncResultItem{
			NoteID:       item.NoteID,
			Status:       "pushed",
			ExternalPath: item.ExternalPath,
		})
	}

	return result
}

func ListObsidianDeletionCandidates() ([]model.ExternalDeletedNote, error) {
	target, err := repository.GetDefaultSyncTarget("obsidian")
	if err != nil {
		return nil, err
	}
	return repository.ListExternalDeletedSyncStates(target.ID)
}

func ConfirmObsidianDeletion(noteID string) error {
	target, err := loadObsidianDeletionTarget()
	if err != nil {
		return err
	}
	state, err := loadObsidianExternalDeletedState(noteID, target.ID)
	if err != nil {
		return err
	}
	if _, err := validateObsidianDeletionActionPath(state, target); err != nil {
		return err
	}
	if _, err := loadObsidianDeletionNote(noteID); err != nil {
		return err
	}
	if err := repository.DeleteNote(noteID); err != nil {
		return fmt.Errorf("delete note: %w", err)
	}
	if err := repository.DeleteSyncState(noteID, target.ID); err != nil {
		return fmt.Errorf("delete obsidian sync state: %w", err)
	}
	return nil
}

func RestoreObsidianDeletion(noteID string) (*model.SyncResultItem, error) {
	target, err := loadObsidianDeletionTarget()
	if err != nil {
		return nil, err
	}
	state, err := loadObsidianExternalDeletedState(noteID, target.ID)
	if err != nil {
		return nil, err
	}
	mappedPath, err := validateObsidianDeletionActionPath(state, target)
	if err != nil {
		return nil, err
	}
	note, err := loadObsidianDeletionNote(noteID)
	if err != nil {
		return nil, err
	}
	item, err := writeNoteToOutputPath(note, target, mappedPath)
	if err != nil {
		return nil, err
	}
	if err := markObsidianRestoreSynced(note.ID, target.ID); err != nil {
		return nil, err
	}
	item.Status = "synced"
	return item, nil
}

func loadObsidianDeletionTarget() (*model.SyncTarget, error) {
	target, err := repository.GetDefaultSyncTarget("obsidian")
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: obsidian sync target not found", ErrObsidianDeletionNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("load obsidian sync target: %w", err)
	}
	return target, nil
}

func loadObsidianExternalDeletedState(noteID, targetID string) (*model.SyncState, error) {
	state, err := repository.GetSyncState(noteID, targetID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: sync state not found", ErrObsidianDeletionNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("load obsidian sync state: %w", err)
	}
	if state.Status != "external_deleted" {
		return nil, ErrObsidianDeletionInvalidState
	}
	return state, nil
}

func loadObsidianDeletionNote(noteID string) (*model.Note, error) {
	note, err := repository.GetNoteByID(noteID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: note not found", ErrObsidianDeletionNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("load note: %w", err)
	}
	return note, nil
}

func validateObsidianDeletionActionPath(state *model.SyncState, target *model.SyncTarget) (string, error) {
	if state == nil {
		return "", fmt.Errorf("%w: sync state not found", ErrObsidianDeletionNotFound)
	}
	outputPath, err := filepath.Abs(state.ExternalPath)
	if err != nil {
		return "", fmt.Errorf("resolve obsidian deletion path: %w", err)
	}
	baseDir, err := targetBaseDir(target)
	if err != nil {
		return "", fmt.Errorf("resolve obsidian base folder: %w", err)
	}
	if strings.TrimSpace(state.ExternalPath) == "" || !isPathWithin(outputPath, baseDir) {
		return "", fmt.Errorf("%w: external path is outside obsidian base folder", ErrObsidianDeletionConflict)
	}
	if err := verifyRealBaseDir(target); err != nil {
		return "", fmt.Errorf("validate obsidian base folder: %w", err)
	}

	realBase, err := filepath.EvalSymlinks(baseDir)
	if err != nil {
		return "", fmt.Errorf("resolve real obsidian base folder: %w", err)
	}
	realParent, err := filepath.EvalSymlinks(filepath.Dir(outputPath))
	if err != nil {
		return "", fmt.Errorf("resolve real obsidian deletion parent: %w", err)
	}
	if !isPathWithin(realParent, realBase) {
		return "", fmt.Errorf("%w: external path parent resolves outside obsidian base folder", ErrObsidianDeletionConflict)
	}

	info, err := os.Lstat(outputPath)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("%w: external path is a symlink; run bidirectional sync first", ErrObsidianDeletionConflict)
		}
		return "", fmt.Errorf("%w: external path exists; run bidirectional sync first", ErrObsidianDeletionConflict)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect obsidian deletion path: %w", err)
	}
	return outputPath, nil
}

func markObsidianRestoreSynced(noteID, targetID string) error {
	state, err := repository.GetSyncState(noteID, targetID)
	if err != nil {
		return fmt.Errorf("load restored obsidian sync state: %w", err)
	}
	now := time.Now().Unix()
	state.LastDirection = "restore"
	state.LastSyncedAt = &now
	state.Status = "synced"
	state.ErrorMessage = nil
	if err := repository.UpsertSyncState(state); err != nil {
		return fmt.Errorf("record restored obsidian sync state: %w", err)
	}
	return nil
}

func syncObsidianFile(file obsidianMarkdownFile, target *model.SyncTarget, notes map[string]model.Note, states map[string]model.SyncState) model.SyncResultItem {
	if file.Note == nil {
		return model.SyncResultItem{
			Status:       "failed",
			ExternalPath: file.Path,
			ErrorMessage: "parsed obsidian note is required",
		}
	}

	filePath := normalizedPath(file.Path)
	if importedID := validImportedID(file.Note.ID); importedID != "" {
		if note, ok := notes[importedID]; ok {
			state, hasState := states[note.ID]
			if hasState && normalizedPath(state.ExternalPath) == filePath {
				if shouldRetryFailedPush(state, file.Hash) {
					return model.SyncResultItem{
						NoteID:       note.ID,
						Status:       bidirectionalPendingPushStatus,
						ExternalPath: file.Path,
					}
				}
				if state.ExternalHash != file.Hash || state.Status == "external_deleted" {
					return pullObsidianIntoNote(note, file, target)
				}
				if markdownHash(renderObsidianMarkdown(&note)) == state.ContentHash && state.Status == "synced" {
					return model.SyncResultItem{
						NoteID:       note.ID,
						Status:       "synced",
						ExternalPath: file.Path,
					}
				}
				return model.SyncResultItem{
					NoteID:       note.ID,
					Status:       bidirectionalPendingPushStatus,
					ExternalPath: file.Path,
				}
			}
			return pullObsidianIntoNote(note, file, target)
		}
	}

	stateNoteIDs := make([]string, 0, len(states))
	for noteID := range states {
		stateNoteIDs = append(stateNoteIDs, noteID)
	}
	sort.Strings(stateNoteIDs)
	for _, noteID := range stateNoteIDs {
		state := states[noteID]
		if strings.TrimSpace(state.ExternalPath) == "" || normalizedPath(state.ExternalPath) != filePath {
			continue
		}

		note, ok := notes[state.NoteID]
		if !ok {
			return model.SyncResultItem{
				NoteID:       state.NoteID,
				Status:       "failed",
				ExternalPath: file.Path,
				ErrorMessage: "mapped FlowSpace note was not found",
			}
		}
		if shouldRetryFailedPush(state, file.Hash) {
			return model.SyncResultItem{
				NoteID:       note.ID,
				Status:       bidirectionalPendingPushStatus,
				ExternalPath: file.Path,
			}
		}
		if state.ExternalHash != file.Hash || state.Status == "external_deleted" {
			return pullObsidianIntoNote(note, file, target)
		}
		if markdownHash(renderObsidianMarkdown(&note)) == state.ContentHash && state.Status == "synced" {
			return model.SyncResultItem{
				NoteID:       note.ID,
				Status:       "synced",
				ExternalPath: file.Path,
			}
		}
		return model.SyncResultItem{
			NoteID:       note.ID,
			Status:       bidirectionalPendingPushStatus,
			ExternalPath: file.Path,
		}
	}

	return importObsidianFile(file, target)
}

func importObsidianFile(file obsidianMarkdownFile, target *model.SyncTarget) model.SyncResultItem {
	if file.Note == nil {
		return model.SyncResultItem{
			Status:       "failed",
			ExternalPath: file.Path,
			ErrorMessage: "parsed obsidian note is required",
		}
	}

	note, err := repository.CreateNoteWithID(&model.CreateNoteWithIDRequest{
		ID:       validImportedID(file.Note.ID),
		Title:    file.Note.Title,
		Body:     file.Note.Body,
		FolderID: file.Note.FolderID,
		Tags:     file.Note.TagsJSON,
	})
	if err != nil {
		return model.SyncResultItem{
			Status:       "failed",
			ExternalPath: file.Path,
			ErrorMessage: fmt.Errorf("import obsidian note: %w", err).Error(),
		}
	}
	if err := recordSyncedExternal(note, target.ID, file, "import"); err != nil {
		return model.SyncResultItem{
			NoteID:       note.ID,
			Status:       "failed",
			ExternalPath: file.Path,
			ErrorMessage: fmt.Errorf("record imported obsidian sync state: %w", err).Error(),
		}
	}

	return model.SyncResultItem{
		NoteID:       note.ID,
		Status:       "imported",
		ExternalPath: file.Path,
	}
}

func pullObsidianIntoNote(note model.Note, file obsidianMarkdownFile, target *model.SyncTarget) model.SyncResultItem {
	if file.Note == nil {
		return model.SyncResultItem{
			NoteID:       note.ID,
			Status:       "failed",
			ExternalPath: file.Path,
			ErrorMessage: "parsed obsidian note is required",
		}
	}

	updated, err := repository.UpdateNote(note.ID, &model.UpdateNoteRequest{
		Title:    &file.Note.Title,
		Body:     &file.Note.Body,
		FolderID: &file.Note.FolderID,
		Tags:     &file.Note.TagsJSON,
	})
	if err != nil {
		return model.SyncResultItem{
			NoteID:       note.ID,
			Status:       "failed",
			ExternalPath: file.Path,
			ErrorMessage: fmt.Errorf("pull obsidian note: %w", err).Error(),
		}
	}
	if err := recordSyncedExternal(updated, target.ID, file, "pull"); err != nil {
		return model.SyncResultItem{
			NoteID:       note.ID,
			Status:       "failed",
			ExternalPath: file.Path,
			ErrorMessage: fmt.Errorf("record pulled obsidian sync state: %w", err).Error(),
		}
	}

	return model.SyncResultItem{
		NoteID:       note.ID,
		Status:       "pulled",
		ExternalPath: file.Path,
	}
}

func recordSyncedExternal(note *model.Note, targetID string, file obsidianMarkdownFile, direction string) error {
	if note == nil {
		return errors.New("note is required")
	}
	if strings.TrimSpace(targetID) == "" {
		return errors.New("target id is required")
	}

	now := time.Now().Unix()
	externalMTime := file.MTime
	return repository.UpsertSyncState(&model.SyncState{
		NoteID:        note.ID,
		TargetID:      targetID,
		ExternalPath:  file.Path,
		ContentHash:   markdownHash(renderObsidianMarkdown(note)),
		ExternalHash:  file.Hash,
		ExternalMTime: &externalMTime,
		LastDirection: direction,
		LastSyncedAt:  &now,
		Status:        "synced",
		ErrorMessage:  nil,
	})
}

func markExternalDeleted(state model.SyncState, target *model.SyncTarget) model.SyncResultItem {
	if target == nil {
		return model.SyncResultItem{
			NoteID:       state.NoteID,
			Status:       "failed",
			ExternalPath: state.ExternalPath,
			ErrorMessage: "sync target is required",
		}
	}

	now := time.Now().Unix()
	state.TargetID = target.ID
	state.LastDirection = "delete_detected"
	state.LastSyncedAt = &now
	state.Status = "external_deleted"
	state.ErrorMessage = nil
	if err := repository.UpsertSyncState(&state); err != nil {
		return model.SyncResultItem{
			NoteID:       state.NoteID,
			Status:       "failed",
			ExternalPath: state.ExternalPath,
			ErrorMessage: fmt.Errorf("mark external deletion: %w", err).Error(),
		}
	}

	return model.SyncResultItem{
		NoteID:       state.NoteID,
		Status:       "external_deleted",
		ExternalPath: state.ExternalPath,
	}
}

func markdownHash(markdown string) string {
	sum := sha256.Sum256([]byte(markdown))
	return hex.EncodeToString(sum[:])
}

func normalizedPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = path
	}
	return strings.ToLower(filepath.Clean(absPath))
}

func validSyncStateExternalPath(state model.SyncState, target *model.SyncTarget) (string, bool) {
	if strings.TrimSpace(state.ExternalPath) == "" {
		return "", false
	}
	outputPath, err := filepath.Abs(state.ExternalPath)
	if err != nil {
		return "", false
	}
	baseDir, err := targetBaseDir(target)
	if err != nil {
		return "", false
	}
	if !isPathWithin(outputPath, baseDir) {
		return "", false
	}
	if err := verifyRealBaseDir(target); err != nil {
		return "", false
	}
	return outputPath, true
}

func notesByID(notes []model.Note) map[string]model.Note {
	byID := make(map[string]model.Note, len(notes))
	for _, note := range notes {
		if note.ID != "" {
			byID[note.ID] = note
		}
	}
	return byID
}

func statesByNoteID(states []model.SyncState) map[string]model.SyncState {
	byNoteID := make(map[string]model.SyncState, len(states))
	for _, state := range states {
		if state.NoteID != "" {
			byNoteID[state.NoteID] = state
		}
	}
	return byNoteID
}

func statesByExternalPath(states []model.SyncState) map[string]model.SyncState {
	byPath := make(map[string]model.SyncState, len(states))
	for _, state := range states {
		path := normalizedPath(state.ExternalPath)
		if path != "" {
			byPath[path] = state
		}
	}
	return byPath
}

func shouldRetryFailedPush(state model.SyncState, externalHash string) bool {
	if state.Status != "failed" {
		return false
	}
	return state.ExternalHash == "" || state.ExternalHash == externalHash
}

func validImportedID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" || strings.ContainsRune(id, 0) || strings.ContainsAny(id, "\r\n") {
		return ""
	}
	return id
}

func refreshBidirectionalMaps(noteID, targetID string, notes map[string]model.Note, states map[string]model.SyncState) {
	if noteID == "" {
		return
	}
	if note, err := repository.GetNoteByID(noteID); err == nil {
		notes[noteID] = *note
	}
	if state, err := repository.GetSyncState(noteID, targetID); err == nil {
		states[noteID] = *state
	}
}

func failedObsidianBidirectionalResult(err error) model.ObsidianBidirectionalResult {
	message := ""
	if err != nil {
		message = err.Error()
	}
	return model.ObsidianBidirectionalResult{
		Failed: 1,
		Items: []model.SyncResultItem{
			{
				Status:       "failed",
				ErrorMessage: message,
			},
		},
	}
}
