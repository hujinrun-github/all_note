package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/repository"
	"github.com/hujinrun/flowspace/internal/storage"
)

const bidirectionalPendingPushStatus = "pending_push"

var flowspaceImportedIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

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
		case "import_tombstoned", "external_claim_conflict", "binding_required":
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

func SyncObsidianPull() model.ObsidianBidirectionalResult {
	target, err := repository.GetDefaultSyncTarget("obsidian")
	if err != nil {
		return failedObsidianBidirectionalResult(fmt.Errorf("load obsidian sync target: %w", err))
	}
	return syncObsidianPullTarget(target)
}

func syncObsidianPullTarget(target *model.SyncTarget) model.ObsidianBidirectionalResult {
	result := model.ObsidianBidirectionalResult{
		Items: make([]model.SyncResultItem, 0),
	}
	if target == nil {
		return failedObsidianBidirectionalResult(errors.New("obsidian sync target is required"))
	}
	if err := TestObsidianTarget(target); err != nil {
		return failedObsidianBidirectionalResult(err)
	}
	requiredTags := requiredSyncTagsFromTarget(target)
	if len(requiredTags) == 0 {
		return result
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
		if file.Note != nil && !tagsMatchRequiredSyncTags(parseTags(file.Note.TagsJSON), requiredTags) {
			continue
		}
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
		case "import_tombstoned", "external_claim_conflict", "binding_required":
			result.Failed++
			result.Items = append(result.Items, item)
			if item.NoteID != "" {
				handledNoteIDs[item.NoteID] = struct{}{}
			}
		case "synced", bidirectionalPendingPushStatus:
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

	return result
}

func ListObsidianDeletionCandidates() ([]model.ExternalDeletedNote, error) {
	target, err := repository.GetDefaultSyncTarget("obsidian")
	if err != nil {
		return nil, err
	}
	return ListObsidianDeletionCandidatesForTarget(target.ID)
}

func ListObsidianDeletionCandidatesForTarget(targetID string) ([]model.ExternalDeletedNote, error) {
	target, err := loadObsidianTargetByID(targetID)
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
	return ConfirmObsidianDeletionForTarget(noteID, target.ID)
}

func ConfirmObsidianDeletionForTarget(noteID string, targetID string) error {
	target, err := loadObsidianTargetByID(targetID)
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
	if store := repository.CurrentStore(); store != nil {
		return confirmObsidianDeletionWithStore(store, noteID, target, state)
	}
	if err := repository.DeleteNote(noteID); err != nil {
		return fmt.Errorf("delete note: %w", err)
	}
	if err := repository.DeleteSyncState(noteID, target.ID); err != nil {
		return fmt.Errorf("delete obsidian sync state: %w", err)
	}
	return nil
}

func confirmObsidianDeletionWithStore(store storage.Store, noteID string, target *model.SyncTarget, state *model.SyncState) error {
	ctx := context.Background()
	return store.Transact(ctx, func(txStore storage.Store) error {
		if err := txStore.Sync().LockBindingSlot(ctx, noteID); err != nil {
			return err
		}
		tombstone, err := obsidianDeletionTombstone(ctx, txStore, noteID, target, state)
		if err != nil {
			return err
		}
		if err := txStore.Sync().PutImportTombstone(ctx, tombstone); err != nil {
			return fmt.Errorf("write obsidian deletion tombstone: %w", err)
		}
		if err := txStore.Notes().Delete(ctx, noteID); err != nil {
			return fmt.Errorf("delete note: %w", err)
		}
		if err := txStore.Sync().DeleteState(ctx, noteID, target.ID); err != nil {
			return fmt.Errorf("delete obsidian sync state: %w", err)
		}
		return nil
	})
}

func obsidianDeletionTombstone(ctx context.Context, store storage.Store, noteID string, target *model.SyncTarget, state *model.SyncState) (model.SyncImportTombstone, error) {
	claim, err := store.Sync().GetExternalClaimByNote(ctx, noteID)
	if err == nil {
		return model.SyncImportTombstone{
			ExternalKey:  claim.ExternalKey,
			TargetID:     claim.TargetID,
			FormerNoteID: noteID,
			ExternalType: claim.ExternalType,
			ExternalID:   claim.ExternalID,
			ExternalPath: claim.ExternalPath,
			Reason:       "note_deleted",
		}, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return model.SyncImportTombstone{}, fmt.Errorf("load external claim: %w", err)
	}
	externalKey, err := obsidianExternalKey(state.ExternalPath)
	if err != nil {
		return model.SyncImportTombstone{}, fmt.Errorf("build obsidian external key: %w", err)
	}
	return model.SyncImportTombstone{
		ExternalKey:  externalKey,
		TargetID:     target.ID,
		FormerNoteID: noteID,
		ExternalType: "obsidian_file",
		ExternalPath: state.ExternalPath,
		Reason:       "note_deleted",
	}, nil
}

func RestoreObsidianDeletion(noteID string) (*model.SyncResultItem, error) {
	target, err := loadObsidianDeletionTarget()
	if err != nil {
		return nil, err
	}
	return RestoreObsidianDeletionForTarget(noteID, target.ID)
}

func RestoreObsidianDeletionForTarget(noteID string, targetID string) (*model.SyncResultItem, error) {
	target, err := loadObsidianTargetByID(targetID)
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

func loadObsidianTargetByID(targetID string) (*model.SyncTarget, error) {
	if strings.TrimSpace(targetID) == "" {
		return nil, fmt.Errorf("%w: obsidian sync target not found", ErrObsidianDeletionNotFound)
	}
	target, err := repository.GetSyncTarget(targetID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: obsidian sync target not found", ErrObsidianDeletionNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("load obsidian sync target: %w", err)
	}
	if target.Type != "obsidian" {
		return nil, fmt.Errorf("%w: sync target is not obsidian", ErrObsidianDeletionNotFound)
	}
	return target, nil
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
	if err := validateObsidianDeletionPathComponents(outputPath, baseDir, realBase); err != nil {
		return "", err
	}
	return outputPath, nil
}

func validateObsidianDeletionPathComponents(outputPath, baseDir, realBase string) error {
	rel, err := filepath.Rel(baseDir, outputPath)
	if err != nil {
		return fmt.Errorf("resolve obsidian deletion relative path: %w", err)
	}
	components := splitRelativePath(rel)
	if len(components) == 0 {
		return fmt.Errorf("%w: external path is not a note file", ErrObsidianDeletionConflict)
	}

	current := baseDir
	for i, component := range components {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect obsidian deletion path component: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: external path component is a symlink; run bidirectional sync first", ErrObsidianDeletionConflict)
		}

		isFinalComponent := i == len(components)-1
		if isFinalComponent {
			return fmt.Errorf("%w: external path exists; run bidirectional sync first", ErrObsidianDeletionConflict)
		}
		if !info.IsDir() {
			return fmt.Errorf("%w: external path component is not a directory", ErrObsidianDeletionConflict)
		}
		realCurrent, err := filepath.EvalSymlinks(current)
		if err != nil {
			return fmt.Errorf("resolve real obsidian deletion path component: %w", err)
		}
		if !isPathWithin(realCurrent, realBase) {
			return fmt.Errorf("%w: external path component resolves outside obsidian base folder", ErrObsidianDeletionConflict)
		}
	}
	return nil
}

func splitRelativePath(path string) []string {
	cleaned := filepath.Clean(path)
	if cleaned == "." || cleaned == string(os.PathSeparator) {
		return nil
	}
	return strings.Split(cleaned, string(os.PathSeparator))
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
	if item := checkObsidianExternalBlockers(file, target, states); item != nil {
		return *item
	}
	if importedID := validImportedID(file.Note); importedID != "" {
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
				if effectiveExternalHash(state) != file.Hash || state.Status == "external_deleted" {
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
		if effectiveExternalHash(state) != file.Hash || state.Status == "external_deleted" {
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

func checkObsidianExternalBlockers(file obsidianMarkdownFile, target *model.SyncTarget, states map[string]model.SyncState) *model.SyncResultItem {
	store := repository.CurrentStore()
	if store == nil {
		return nil
	}
	externalKey, err := obsidianExternalKey(file.Path)
	if err != nil {
		return &model.SyncResultItem{
			Status:       "failed",
			ExternalPath: file.Path,
			ErrorMessage: fmt.Errorf("build obsidian external key: %w", err).Error(),
		}
	}
	importedID := validImportedID(file.Note)
	ctx := context.Background()
	tombstone, err := store.Sync().FindImportTombstone(ctx, target.ID, externalKey, importedID, "obsidian_file")
	if err == nil {
		return &model.SyncResultItem{
			NoteID:       tombstone.FormerNoteID,
			Status:       "import_tombstoned",
			ExternalPath: file.Path,
			ErrorMessage: "external resource was previously removed from sync",
		}
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return &model.SyncResultItem{
			NoteID:       importedID,
			Status:       "failed",
			ExternalPath: file.Path,
			ErrorMessage: fmt.Errorf("check import tombstone: %w", err).Error(),
		}
	}

	claim, err := store.Sync().GetExternalClaim(ctx, externalKey)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return &model.SyncResultItem{
			NoteID:       importedID,
			Status:       "failed",
			ExternalPath: file.Path,
			ErrorMessage: fmt.Errorf("check external claim: %w", err).Error(),
		}
	}
	if claim.TargetID == target.ID && claim.NoteID == importedID && importedID != "" {
		return nil
	}
	for _, state := range states {
		if normalizedPath(state.ExternalPath) == normalizedPath(file.Path) && claim.TargetID == target.ID && claim.NoteID == state.NoteID {
			return nil
		}
	}
	return &model.SyncResultItem{
		NoteID:       claim.NoteID,
		Status:       "external_claim_conflict",
		ExternalPath: file.Path,
		ErrorMessage: ErrSyncClaimConflict.Error(),
	}
}

func importObsidianFile(file obsidianMarkdownFile, target *model.SyncTarget) model.SyncResultItem {
	if file.Note == nil {
		return model.SyncResultItem{
			Status:       "failed",
			ExternalPath: file.Path,
			ErrorMessage: "parsed obsidian note is required",
		}
	}

	folderID, err := existingObsidianFolderID(file.Note.FolderID)
	if err != nil {
		return model.SyncResultItem{
			Status:       "failed",
			ExternalPath: file.Path,
			ErrorMessage: err.Error(),
		}
	}
	if store := repository.CurrentStore(); store != nil {
		return importObsidianFileWithStore(store, file, target, folderID)
	}

	note, err := repository.CreateNoteWithID(&model.CreateNoteWithIDRequest{
		ID:       validImportedID(file.Note),
		Title:    file.Note.Title,
		Body:     file.Note.Body,
		FolderID: folderID,
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

func importObsidianFileWithStore(store storage.Store, file obsidianMarkdownFile, target *model.SyncTarget, folderID string) model.SyncResultItem {
	note := &model.Note{
		ID:       validImportedID(file.Note),
		Title:    file.Note.Title,
		Body:     file.Note.Body,
		FolderID: folderID,
		Tags:     file.Note.TagsJSON,
	}
	ctx := context.Background()
	err := store.Transact(ctx, func(txStore storage.Store) error {
		if strings.TrimSpace(note.ID) != "" {
			if err := txStore.Sync().LockBindingSlot(ctx, note.ID); err != nil {
				return err
			}
		}
		if err := txStore.Notes().CreateWithID(ctx, note); err != nil {
			return fmt.Errorf("import obsidian note: %w", err)
		}
		if err := txStore.Sync().LockBindingSlot(ctx, note.ID); err != nil {
			return err
		}
		if err := bindImportedNoteToSyncTargetInStore(ctx, txStore, note.ID, target.ID); err != nil {
			return err
		}
		state, err := obsidianSyncedState(note, target.ID, file, "import")
		if err != nil {
			return err
		}
		if err := txStore.Sync().UpsertState(ctx, state); err != nil {
			return fmt.Errorf("record imported obsidian sync state: %w", err)
		}
		return nil
	})
	if err != nil {
		return model.SyncResultItem{
			NoteID:       note.ID,
			Status:       "failed",
			ExternalPath: file.Path,
			ErrorMessage: err.Error(),
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

	folderID, err := existingObsidianFolderID(file.Note.FolderID)
	if err != nil {
		return model.SyncResultItem{
			NoteID:       note.ID,
			Status:       "failed",
			ExternalPath: file.Path,
			ErrorMessage: err.Error(),
		}
	}
	if store := repository.CurrentStore(); store != nil {
		return pullObsidianIntoNoteWithStore(store, note, file, target, folderID)
	}

	updated, err := repository.UpdateNote(note.ID, &model.UpdateNoteRequest{
		Title:    &file.Note.Title,
		Body:     &file.Note.Body,
		FolderID: &folderID,
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

func pullObsidianIntoNoteWithStore(store storage.Store, note model.Note, file obsidianMarkdownFile, target *model.SyncTarget, folderID string) model.SyncResultItem {
	ctx := context.Background()
	var updated *model.Note
	err := store.Transact(ctx, func(txStore storage.Store) error {
		if err := txStore.Sync().LockBindingSlot(ctx, note.ID); err != nil {
			return err
		}
		if err := ensureOrCreatePullBindingInStore(ctx, txStore, note.ID, target.ID); err != nil {
			return err
		}
		var err error
		updated, err = txStore.Notes().Update(ctx, note.ID, &model.UpdateNoteRequest{
			Title:    &file.Note.Title,
			Body:     &file.Note.Body,
			FolderID: &folderID,
			Tags:     &file.Note.TagsJSON,
		})
		if err != nil {
			return fmt.Errorf("pull obsidian note: %w", err)
		}
		state, err := obsidianSyncedState(updated, target.ID, file, "pull")
		if err != nil {
			return err
		}
		if err := txStore.Sync().UpsertState(ctx, state); err != nil {
			return fmt.Errorf("record pulled obsidian sync state: %w", err)
		}
		return nil
	})
	if err != nil {
		return model.SyncResultItem{
			NoteID:       note.ID,
			Status:       "failed",
			ExternalPath: file.Path,
			ErrorMessage: err.Error(),
		}
	}
	return model.SyncResultItem{
		NoteID:       updated.ID,
		Status:       "pulled",
		ExternalPath: file.Path,
	}
}

func recordSyncedExternal(note *model.Note, targetID string, file obsidianMarkdownFile, direction string) error {
	state, err := obsidianSyncedState(note, targetID, file, direction)
	if err != nil {
		return err
	}
	return repository.UpsertSyncState(state)
}

func obsidianSyncedState(note *model.Note, targetID string, file obsidianMarkdownFile, direction string) (*model.SyncState, error) {
	if note == nil {
		return nil, errors.New("note is required")
	}
	if strings.TrimSpace(targetID) == "" {
		return nil, errors.New("target id is required")
	}

	now := time.Now().Unix()
	externalMTime := file.MTime
	return &model.SyncState{
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
	}, nil
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

func effectiveExternalHash(state model.SyncState) string {
	if strings.TrimSpace(state.ExternalHash) != "" {
		return state.ExternalHash
	}
	if state.Status == "synced" {
		return state.ContentHash
	}
	return ""
}

func validImportedID(note *obsidianParsedMarkdown) string {
	if note == nil || !strings.EqualFold(strings.TrimSpace(note.Source), "flowspace") {
		return ""
	}
	id := strings.TrimSpace(note.ID)
	if !flowspaceImportedIDPattern.MatchString(id) {
		return ""
	}
	return id
}

func existingObsidianFolderID(folderID string) (string, error) {
	folderID = strings.TrimSpace(folderID)
	if folderID == "" {
		return "__uncategorized", nil
	}
	exists, err := repository.FolderExists(folderID)
	if err != nil {
		return "", fmt.Errorf("validate obsidian folder: %w", err)
	}
	if !exists {
		return "__uncategorized", nil
	}
	return folderID, nil
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
