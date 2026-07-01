package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/hujinrun/flowspace/internal/model"
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

func SyncNote(ctx context.Context, store storage.Store, noteID string) (*model.SyncResultItem, error) {
	noteID = strings.TrimSpace(noteID)
	if noteID == "" {
		return nil, errors.New("note id is required")
	}
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
	return syncNoteToExplicitTarget(ctx, store, noteID, target)
}

func SyncTargetPush(ctx context.Context, store storage.Store, targetID string) (model.SyncBatchResult, error) {
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		return model.SyncBatchResult{}, ErrSyncTargetNotFound
	}
	target, err := loadSyncTargetForDispatch(ctx, store, targetID)
	if err != nil {
		return model.SyncBatchResult{}, err
	}
	bindings, err := store.Sync().ListBindingsByTarget(ctx, target.ID)
	if err != nil {
		return model.SyncBatchResult{}, fmt.Errorf("list sync bindings: %w", err)
	}
	result := model.SyncBatchResult{Items: make([]model.SyncResultItem, 0, len(bindings))}
	for _, binding := range bindings {
		item, err := syncNoteToExplicitTarget(ctx, store, binding.NoteID, target)
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

func SyncTargetPull(ctx context.Context, store storage.Store, targetID string) (TargetSyncResult, error) {
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		return TargetSyncResult{}, ErrSyncTargetNotFound
	}
	target, err := loadSyncTargetForDispatch(ctx, store, targetID)
	if err != nil {
		return TargetSyncResult{}, err
	}
	if err := ensureTargetSyncHasNoForeignBindings(ctx, store, target.ID); err != nil {
		return TargetSyncResult{}, err
	}

	switch target.Type {
	case "obsidian":
		return targetSyncResultFromObsidian(syncObsidianPullTargetScoped(ctx, store, target)), nil
	case "notion":
		config, err := parseNotionTargetConfig(target)
		if err != nil {
			return TargetSyncResult{}, err
		}
		gateway, err := notionGatewayForConfig(config)
		if err != nil {
			return TargetSyncResult{}, err
		}
		return targetSyncResultFromNotion(pullNotionRemoteScoped(ctx, store, NewNotionSyncService(gateway), *target)), nil
	default:
		return TargetSyncResult{}, fmt.Errorf("unsupported sync target type %q", target.Type)
	}
}

func SyncTargetBidirectional(ctx context.Context, store storage.Store, targetID string) (TargetSyncResult, error) {
	pullResult, err := SyncTargetPull(ctx, store, targetID)
	if err != nil {
		return TargetSyncResult{}, err
	}
	pushResult, err := SyncTargetPush(ctx, store, targetID)
	if err != nil {
		return TargetSyncResult{}, err
	}
	pullResult.Pushed += pushResult.Synced
	pullResult.Failed += pushResult.Failed
	pullResult.Items = append(pullResult.Items, pushResult.Items...)
	return pullResult, nil
}

func loadSyncTargetForDispatch(ctx context.Context, store storage.Store, targetID string) (*model.SyncTarget, error) {
	target, err := store.Sync().GetTarget(ctx, targetID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSyncTargetNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load sync target: %w", err)
	}
	if target == nil || !target.Enabled {
		return nil, ErrSyncTargetNotFound
	}
	return target, nil
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

func syncNoteToExplicitTarget(ctx context.Context, store storage.Store, noteID string, target *model.SyncTarget) (*model.SyncResultItem, error) {
	if target == nil {
		return nil, ErrSyncTargetNotFound
	}
	if !target.Enabled {
		return nil, fmt.Errorf("sync target is disabled")
	}
	note, err := store.Notes().GetByID(ctx, noteID)
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
		return syncNoteToExplicitNotionTarget(ctx, store, note, *target)
	default:
		return nil, fmt.Errorf("unsupported sync target type %q", target.Type)
	}
}

func syncNoteToExplicitNotionTarget(ctx context.Context, store storage.Store, note *model.Note, target model.SyncTarget) (*model.SyncResultItem, error) {
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
	existing, err := store.Sync().GetState(ctx, note.ID, target.ID)
	if err == nil {
		state = *existing
		hasState = true
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("load notion sync state: %w", err)
	}
	if !hasState {
		claim, err := store.Sync().GetExternalClaimByNote(ctx, note.ID)
		if err == nil && claim.TargetID == target.ID && claim.ExternalType == "notion_page" && strings.TrimSpace(claim.ExternalID) != "" {
			state = model.SyncState{
				NoteID:       note.ID,
				TargetID:     target.ID,
				ExternalPath: notionExternalPath(claim.ExternalID),
				ExternalID:   claim.ExternalID,
				ExternalURL:  "",
				Status:       "synced",
			}
			hasState = true
		} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("load notion external claim: %w", err)
		}
	}
	var item model.SyncResultItem
	withScopedRepositoryStore(ctx, store, func() {
		item = NewNotionSyncService(gateway).pushNotionLocalNote(config, target, *note, state, hasState)
	})
	if item.Status == "failed" {
		return &item, errors.New(item.ErrorMessage)
	}
	return &item, nil
}
