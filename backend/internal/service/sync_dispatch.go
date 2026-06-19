package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/repository"
	"github.com/hujinrun/flowspace/internal/storage"
)

var (
	ErrSyncBindingRequired = errors.New("note sync binding is required")
	ErrSyncBindingConflict = errors.New("note is bound to another sync target")
	ErrSyncClaimConflict   = errors.New("external resource is already claimed by another sync target")
	ErrSyncTargetNotFound  = errors.New("sync target not found")
)

type TargetSyncResult struct {
	Pushed          int                    `json:"pushed"`
	Pulled          int                    `json:"pulled"`
	Imported        int                    `json:"imported"`
	ExternalDeleted int                    `json:"external_deleted"`
	ConflictPulled  int                    `json:"conflict_pulled,omitempty"`
	Unsupported     int                    `json:"unsupported,omitempty"`
	Failed          int                    `json:"failed"`
	Items           []model.SyncResultItem `json:"items"`
}

func SyncNote(noteID string) (*model.SyncResultItem, error) {
	noteID = strings.TrimSpace(noteID)
	if noteID == "" {
		return nil, errors.New("note id is required")
	}
	store, err := currentSyncStore()
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	binding, err := store.Sync().GetBinding(ctx, noteID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSyncBindingRequired
	}
	if err != nil {
		return nil, fmt.Errorf("load note sync binding: %w", err)
	}
	target, err := store.Sync().GetTarget(ctx, binding.TargetID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSyncTargetNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load sync target: %w", err)
	}
	return syncNoteToExplicitTarget(noteID, target)
}

func SyncTargetPush(targetID string) (model.SyncBatchResult, error) {
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		return model.SyncBatchResult{}, ErrSyncTargetNotFound
	}
	store, target, err := loadSyncTargetForDispatch(targetID)
	if err != nil {
		return model.SyncBatchResult{}, err
	}
	ctx := context.Background()
	bindings, err := store.Sync().ListBindingsByTarget(ctx, target.ID)
	if err != nil {
		return model.SyncBatchResult{}, fmt.Errorf("list sync bindings: %w", err)
	}
	result := model.SyncBatchResult{Items: make([]model.SyncResultItem, 0, len(bindings))}
	for _, binding := range bindings {
		item, err := syncNoteToExplicitTarget(binding.NoteID, target)
		if err != nil {
			result.Failed++
			result.Items = append(result.Items, model.SyncResultItem{
				NoteID:       binding.NoteID,
				Status:       "failed",
				ErrorMessage: err.Error(),
			})
			continue
		}
		if item.Status == "failed" {
			result.Failed++
		} else {
			result.Synced++
		}
		result.Items = append(result.Items, *item)
	}
	return result, nil
}

func SyncTargetPull(targetID string) (TargetSyncResult, error) {
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		return TargetSyncResult{}, ErrSyncTargetNotFound
	}
	store, target, err := loadSyncTargetForDispatch(targetID)
	if err != nil {
		return TargetSyncResult{}, err
	}
	ctx := context.Background()
	if err := ensureTargetSyncHasNoForeignBindings(ctx, store, target.ID); err != nil {
		return TargetSyncResult{}, err
	}

	switch target.Type {
	case "obsidian":
		return targetSyncResultFromObsidian(syncObsidianPullTarget(target)), nil
	case "notion":
		config, err := parseNotionTargetConfig(target)
		if err != nil {
			return TargetSyncResult{}, err
		}
		gateway, err := notionGatewayForConfig(config)
		if err != nil {
			return TargetSyncResult{}, err
		}
		return targetSyncResultFromNotion(NewNotionSyncService(gateway).PullRemote(*target)), nil
	default:
		return TargetSyncResult{}, fmt.Errorf("unsupported sync target type %q", target.Type)
	}
}

func SyncTargetBidirectional(targetID string) (TargetSyncResult, error) {
	pullResult, err := SyncTargetPull(targetID)
	if err != nil {
		return TargetSyncResult{}, err
	}
	pushResult, err := SyncTargetPush(targetID)
	if err != nil {
		return TargetSyncResult{}, err
	}
	pullResult.Pushed += pushResult.Synced
	pullResult.Failed += pushResult.Failed
	pullResult.Items = append(pullResult.Items, pushResult.Items...)
	return pullResult, nil
}

func loadSyncTargetForDispatch(targetID string) (storage.Store, *model.SyncTarget, error) {
	store, err := currentSyncStore()
	if err != nil {
		return nil, nil, err
	}
	target, err := store.Sync().GetTarget(context.Background(), targetID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrSyncTargetNotFound
	}
	if err != nil {
		return nil, nil, fmt.Errorf("load sync target: %w", err)
	}
	if target == nil || !target.Enabled {
		return nil, nil, ErrSyncTargetNotFound
	}
	return store, target, nil
}

func ensureTargetSyncHasNoForeignBindings(ctx context.Context, store storage.Store, targetID string) error {
	states, err := store.Sync().ListStatesByTarget(ctx, targetID)
	if err != nil {
		return fmt.Errorf("list sync states: %w", err)
	}
	for _, state := range states {
		binding, err := store.Sync().GetBinding(ctx, state.NoteID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return fmt.Errorf("load sync binding: %w", err)
		}
		if binding.TargetID != targetID {
			return ErrSyncBindingConflict
		}
	}
	return nil
}

func ensureNoteBoundToSyncTargetInStore(ctx context.Context, store storage.Store, noteID string, targetID string) error {
	binding, err := store.Sync().GetBinding(ctx, noteID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrSyncBindingRequired
	}
	if err != nil {
		return fmt.Errorf("load sync binding: %w", err)
	}
	if binding.TargetID != targetID {
		return ErrSyncBindingConflict
	}
	return nil
}

func ensureOrCreatePullBindingInStore(ctx context.Context, store storage.Store, noteID string, targetID string) error {
	binding, err := store.Sync().GetBinding(ctx, noteID)
	if err == nil {
		if binding.TargetID != targetID {
			return ErrSyncBindingConflict
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("load sync binding: %w", err)
	}
	if _, err := store.Sync().GetSuppression(ctx, noteID, targetID); err == nil {
		return ErrSyncBindingRequired
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("load sync suppression: %w", err)
	}
	return store.Sync().PutBinding(ctx, model.NoteSyncBinding{
		NoteID:   noteID,
		TargetID: targetID,
	})
}

func bindImportedNoteToSyncTargetInStore(ctx context.Context, store storage.Store, noteID string, targetID string) error {
	binding, err := store.Sync().GetBinding(ctx, noteID)
	if err == nil {
		if binding.TargetID != targetID {
			return ErrSyncBindingConflict
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("load sync binding: %w", err)
	}
	return store.Sync().PutBinding(ctx, model.NoteSyncBinding{
		NoteID:   noteID,
		TargetID: targetID,
	})
}

func targetSyncResultFromObsidian(result model.ObsidianBidirectionalResult) TargetSyncResult {
	return TargetSyncResult{
		Pushed:          result.Pushed,
		Pulled:          result.Pulled,
		Imported:        result.Imported,
		ExternalDeleted: result.ExternalDeleted,
		Failed:          result.Failed,
		Items:           result.Items,
	}
}

func targetSyncResultFromNotion(result model.NotionBidirectionalResult) TargetSyncResult {
	return TargetSyncResult{
		Pushed:          result.Pushed,
		Pulled:          result.Pulled,
		Imported:        result.Imported,
		ExternalDeleted: result.ExternalDeleted,
		ConflictPulled:  result.ConflictPulled,
		Unsupported:     result.Unsupported,
		Failed:          result.Failed,
		Items:           result.Items,
	}
}

func syncNoteToExplicitTarget(noteID string, target *model.SyncTarget) (*model.SyncResultItem, error) {
	if target == nil {
		return nil, ErrSyncTargetNotFound
	}
	if !target.Enabled {
		return nil, fmt.Errorf("sync target is disabled")
	}
	note, err := repository.GetNoteByID(noteID)
	if err != nil {
		return nil, fmt.Errorf("load note: %w", err)
	}
	switch target.Type {
	case "obsidian":
		if err := TestObsidianTarget(target); err != nil {
			return nil, err
		}
		return writeNoteToTarget(note, target)
	case "notion":
		return syncNoteToExplicitNotionTarget(note, *target)
	default:
		return nil, fmt.Errorf("unsupported sync target type %q", target.Type)
	}
}

func syncNoteToExplicitNotionTarget(note *model.Note, target model.SyncTarget) (*model.SyncResultItem, error) {
	config, err := parseNotionTargetConfig(&target)
	if err != nil {
		return nil, err
	}
	gateway, err := notionGatewayForConfig(config)
	if err != nil {
		return nil, err
	}
	var state model.SyncState
	hasState := false
	existing, err := repository.GetSyncState(note.ID, target.ID)
	if err == nil {
		state = *existing
		hasState = true
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("load notion sync state: %w", err)
	}
	item := NewNotionSyncService(gateway).pushNotionLocalNote(config, target, *note, state, hasState)
	if item.Status == "failed" {
		return &item, errors.New(item.ErrorMessage)
	}
	return &item, nil
}

func currentSyncStore() (storage.Store, error) {
	store := repository.CurrentStore()
	if store == nil {
		return nil, errors.New("storage provider is not initialized")
	}
	return store, nil
}
