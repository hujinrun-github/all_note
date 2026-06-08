package service

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/repository"
)

const bidirectionalPendingPushStatus = "pending_push"

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

	files, err := scanObsidianMarkdownFiles(target)
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
	scannedPaths := make(map[string]struct{}, len(files))
	handledNoteIDs := make(map[string]struct{})

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
		if strings.TrimSpace(state.ExternalPath) == "" || state.Status == "external_deleted" {
			continue
		}
		if _, ok := notes[state.NoteID]; !ok {
			continue
		}
		if _, ok := scannedPaths[normalizedPath(state.ExternalPath)]; ok {
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
		if hasState && markdownHash(renderObsidianMarkdown(&note)) == state.ContentHash {
			continue
		}

		item, err := writeNoteToTarget(&note, target)
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
			if hasState && normalizedPath(state.ExternalPath) == filePath && state.ExternalHash == file.Hash {
				if state.Status == "external_deleted" {
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
