package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/repository"
	"github.com/hujinrun/flowspace/internal/storage"
)

type notionRemoteNote struct {
	PageID           string
	URL              string
	Title            string
	Markdown         string
	Hash             string
	LastEditedAt     int64
	FlowSpaceID      string
	Tags             []string
	UnsupportedTypes []string
}

type notionSyncStateSnapshot struct {
	NoteID        string
	TargetID      string
	ExternalPath  string
	ExternalID    string
	ExternalURL   string
	ContentHash   string
	ExternalHash  string
	ExternalMTime *int64
}

type notionSyncGateway interface {
	TestDataSource(config notionTargetConfig) error
	QueryRemoteNotes(config notionTargetConfig) ([]notionRemoteNote, error)
	CreateRemoteNote(config notionTargetConfig, note *model.Note) (notionRemoteNote, error)
	UpdateRemoteNote(config notionTargetConfig, pageID string, note *model.Note) (notionRemoteNote, error)
	RestoreRemoteNote(config notionTargetConfig, note *model.Note, previous notionSyncStateSnapshot) (notionRemoteNote, error)
}

type NotionSyncService struct {
	gateway notionSyncGateway
}

var (
	ErrNotionDeletionNotFound     = errors.New("notion deletion candidate not found")
	ErrNotionDeletionConflict     = errors.New("notion deletion conflict")
	ErrNotionDeletionInvalidState = errors.New("note is not marked as deleted in notion")
	notionGatewayFactory          = notionGatewayFromEnv
)

func NewNotionSyncService(gateway notionSyncGateway) *NotionSyncService {
	return &NotionSyncService{gateway: gateway}
}

func TestNotionTarget(target *model.SyncTarget) error {
	config, err := parseNotionTargetConfig(target)
	if err != nil {
		return err
	}
	gateway, err := notionGatewayForConfig(config)
	if err != nil {
		return err
	}
	return gateway.TestDataSource(config)
}

func SyncNoteToNotion(noteID string) (*model.SyncResultItem, error) {
	if strings.TrimSpace(noteID) == "" {
		return nil, errors.New("note id is required")
	}

	target, err := repository.GetDefaultSyncTarget("notion")
	if err != nil {
		return nil, fmt.Errorf("load notion sync target: %w", err)
	}
	config, err := parseNotionTargetConfig(target)
	if err != nil {
		return nil, err
	}
	note, err := repository.GetNoteByID(noteID)
	if err != nil {
		return nil, fmt.Errorf("load note: %w", err)
	}
	if !noteMatchesRequiredSyncTags(*note, config.RequiredTags) {
		return &model.SyncResultItem{
			NoteID: note.ID,
			Status: "skipped",
		}, nil
	}
	gateway, err := notionGatewayForConfig(config)
	if err != nil {
		return nil, err
	}

	var state model.SyncState
	hasState := false
	existing, err := repository.GetSyncState(noteID, target.ID)
	if err == nil {
		state = *existing
		hasState = true
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("load notion sync state: %w", err)
	}

	item := NewNotionSyncService(gateway).pushNotionLocalNote(config, *target, *note, state, hasState)
	if item.Status == "failed" {
		return &item, errors.New(item.ErrorMessage)
	}
	return &item, nil
}

func SyncNotionBidirectional() model.NotionBidirectionalResult {
	target, err := repository.GetDefaultSyncTarget("notion")
	if err != nil {
		return failedNotionBidirectionalResult(fmt.Errorf("load notion sync target: %w", err))
	}
	config, err := parseNotionTargetConfig(target)
	if err != nil {
		return failedNotionBidirectionalResult(err)
	}
	gateway, err := notionGatewayForConfig(config)
	if err != nil {
		return failedNotionBidirectionalResult(err)
	}
	return NewNotionSyncService(gateway).SyncBidirectional(*target)
}

func SyncNotionAll() model.SyncBatchResult {
	target, err := repository.GetDefaultSyncTarget("notion")
	if err != nil {
		return failedNotionBatchResult(fmt.Errorf("load notion sync target: %w", err))
	}
	config, err := parseNotionTargetConfig(target)
	if err != nil {
		return failedNotionBatchResult(err)
	}
	if len(config.RequiredTags) == 0 {
		return model.SyncBatchResult{Items: []model.SyncResultItem{}}
	}
	gateway, err := notionGatewayForConfig(config)
	if err != nil {
		return failedNotionBatchResult(err)
	}
	return NewNotionSyncService(gateway).PushAll(*target)
}

func SyncNotionPull() model.NotionBidirectionalResult {
	target, err := repository.GetDefaultSyncTarget("notion")
	if err != nil {
		return failedNotionBidirectionalResult(fmt.Errorf("load notion sync target: %w", err))
	}
	config, err := parseNotionTargetConfig(target)
	if err != nil {
		return failedNotionBidirectionalResult(err)
	}
	if len(config.RequiredTags) == 0 {
		return model.NotionBidirectionalResult{Items: []model.SyncResultItem{}}
	}
	gateway, err := notionGatewayForConfig(config)
	if err != nil {
		return failedNotionBidirectionalResult(err)
	}
	return NewNotionSyncService(gateway).PullRemote(*target)
}

func ListNotionDeletionCandidates() ([]model.ExternalDeletedNote, error) {
	target, err := loadNotionDeletionTarget()
	if err != nil {
		return nil, err
	}
	return repository.ListExternalDeletedSyncStates(target.ID)
}

func ConfirmNotionDeletion(noteID string) error {
	target, err := loadNotionDeletionTarget()
	if err != nil {
		return err
	}
	if _, err := loadNotionExternalDeletedState(noteID, target.ID); err != nil {
		return err
	}
	if _, err := loadNotionDeletionNote(noteID); err != nil {
		return err
	}
	if err := repository.DeleteNote(noteID); err != nil {
		return fmt.Errorf("delete note: %w", err)
	}
	if err := repository.DeleteSyncState(noteID, target.ID); err != nil {
		return fmt.Errorf("delete notion sync state: %w", err)
	}
	return nil
}

func RestoreNotionDeletion(noteID string) (*model.SyncResultItem, error) {
	target, err := loadNotionDeletionTarget()
	if err != nil {
		return nil, err
	}
	state, err := loadNotionExternalDeletedState(noteID, target.ID)
	if err != nil {
		return nil, err
	}
	note, err := loadNotionDeletionNote(noteID)
	if err != nil {
		return nil, err
	}
	config, err := parseNotionTargetConfig(target)
	if err != nil {
		return nil, err
	}
	gateway, err := notionGatewayForConfig(config)
	if err != nil {
		return nil, err
	}

	remote, err := gateway.RestoreRemoteNote(config, note, notionStateSnapshot(*state))
	if err != nil {
		return nil, fmt.Errorf("restore notion page: %w", err)
	}
	remote = withNotionRemoteDefaults(remote)
	if remote.PageID == "" {
		remote.PageID = notionStateExternalID(*state)
	}
	if remote.URL == "" {
		remote.URL = state.ExternalURL
	}
	if err := recordSyncedNotionRemote(note, *target, remote, "restore"); err != nil {
		return nil, fmt.Errorf("record restored notion sync state: %w", err)
	}
	return &model.SyncResultItem{
		NoteID:       note.ID,
		Status:       "synced",
		ExternalPath: notionExternalPath(remote.PageID),
		ExternalID:   remote.PageID,
		ExternalURL:  remote.URL,
	}, nil
}

func notionGatewayForConfig(config notionTargetConfig) (notionSyncGateway, error) {
	token := ""
	if !notionMockProviderEnabled() {
		loaded, err := notionToken(config)
		if err != nil {
			return nil, err
		}
		token = loaded
	}
	return notionGatewayFactory(token), nil
}

func loadNotionDeletionTarget() (*model.SyncTarget, error) {
	target, err := repository.GetDefaultSyncTarget("notion")
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: notion sync target not found", ErrNotionDeletionNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("load notion sync target: %w", err)
	}
	return target, nil
}

func loadNotionExternalDeletedState(noteID, targetID string) (*model.SyncState, error) {
	state, err := repository.GetSyncState(noteID, targetID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: sync state not found", ErrNotionDeletionNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("load notion sync state: %w", err)
	}
	if state.Status != "external_deleted" {
		return nil, ErrNotionDeletionInvalidState
	}
	return state, nil
}

func loadNotionDeletionNote(noteID string) (*model.Note, error) {
	note, err := repository.GetNoteByID(noteID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: note not found", ErrNotionDeletionNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("load note: %w", err)
	}
	return note, nil
}

func notionStateSnapshot(state model.SyncState) notionSyncStateSnapshot {
	return notionSyncStateSnapshot{
		NoteID:        state.NoteID,
		TargetID:      state.TargetID,
		ExternalPath:  state.ExternalPath,
		ExternalID:    state.ExternalID,
		ExternalURL:   state.ExternalURL,
		ContentHash:   state.ContentHash,
		ExternalHash:  state.ExternalHash,
		ExternalMTime: state.ExternalMTime,
	}
}

func (svc *NotionSyncService) SyncBidirectional(target model.SyncTarget) model.NotionBidirectionalResult {
	result := model.NotionBidirectionalResult{
		Items: make([]model.SyncResultItem, 0),
	}
	if svc == nil || svc.gateway == nil {
		return failedNotionBidirectionalResult(errors.New("notion sync gateway is required"))
	}

	config, err := parseNotionTargetConfig(&target)
	if err != nil {
		return failedNotionBidirectionalResult(err)
	}
	if err := svc.gateway.TestDataSource(config); err != nil {
		return failedNotionBidirectionalResult(fmt.Errorf("test notion data source: %w", err))
	}

	remoteNotes, err := svc.gateway.QueryRemoteNotes(config)
	if err != nil {
		return failedNotionBidirectionalResult(fmt.Errorf("query notion notes: %w", err))
	}
	statesList, err := repository.ListSyncStatesByTarget(target.ID)
	if err != nil {
		return failedNotionBidirectionalResult(fmt.Errorf("load notion sync states: %w", err))
	}
	notesList, err := repository.ListAllNotes()
	if err != nil {
		return failedNotionBidirectionalResult(fmt.Errorf("load notes: %w", err))
	}

	notes := notesByID(notesList)
	statesByNote := statesByNoteID(statesList)
	statesByExternal := notionStatesByExternalID(statesList)
	remoteExternalIDs := make(map[string]struct{}, len(remoteNotes))
	handledNoteIDs := make(map[string]struct{})

	sort.Slice(remoteNotes, func(i, j int) bool {
		return remoteNotes[i].PageID < remoteNotes[j].PageID
	})
	for _, remote := range remoteNotes {
		remote = withNotionRemoteDefaults(remote)
		if strings.TrimSpace(remote.PageID) == "" {
			addNotionFailure(&result, model.SyncResultItem{
				Status:       "failed",
				ErrorMessage: "notion page id is required",
			})
			continue
		}

		remoteExternalIDs[remote.PageID] = struct{}{}
		state, hasState := matchNotionSyncState(remote, statesByNote, statesByExternal)
		note, hasNote := matchNotionLocalNote(remote, state, hasState, notes)

		if len(remote.UnsupportedTypes) > 0 {
			if hasNote {
				handledNoteIDs[note.ID] = struct{}{}
			}
			result.Unsupported++
			result.Items = append(result.Items, model.SyncResultItem{
				NoteID:       notionMatchedNoteID(remote, state, hasState),
				Status:       "unsupported",
				ExternalPath: notionExternalPath(remote.PageID),
				ExternalID:   remote.PageID,
				ExternalURL:  remote.URL,
				ErrorMessage: "unsupported Notion block types: " + strings.Join(remote.UnsupportedTypes, ", "),
			})
			continue
		}

		if !hasNote {
			item := importNotionRemoteNote(remote, target)
			if item.Status == "failed" {
				result.Failed++
			} else {
				result.Imported++
				handledNoteIDs[item.NoteID] = struct{}{}
			}
			result.Items = append(result.Items, item)
			continue
		}

		if !hasState || notionRemoteHash(remote) != state.ExternalHash || state.Status == "external_deleted" {
			conflict := hasState && notionLocalContentHash(note) != state.ContentHash
			item := pullNotionRemoteIntoNote(note, remote, target)
			if item.Status == "failed" {
				result.Failed++
			} else {
				result.Pulled++
				if conflict {
					result.ConflictPulled++
				}
				handledNoteIDs[note.ID] = struct{}{}
			}
			result.Items = append(result.Items, item)
		}
	}

	for _, state := range statesList {
		if state.Status != "synced" {
			continue
		}
		externalID := notionStateExternalID(state)
		if externalID == "" {
			continue
		}
		if _, ok := remoteExternalIDs[externalID]; ok {
			continue
		}
		if _, ok := notes[state.NoteID]; !ok {
			continue
		}
		item := markNotionExternalDeleted(state, target)
		if item.Status == "failed" {
			result.Failed++
		} else {
			result.ExternalDeleted++
			handledNoteIDs[state.NoteID] = struct{}{}
		}
		result.Items = append(result.Items, item)
	}

	currentStatesList, err := repository.ListSyncStatesByTarget(target.ID)
	if err != nil {
		addNotionFailure(&result, model.SyncResultItem{
			Status:       "failed",
			ErrorMessage: fmt.Errorf("reload notion sync states: %w", err).Error(),
		})
		return result
	}
	statesByNote = statesByNoteID(currentStatesList)

	sort.Slice(notesList, func(i, j int) bool {
		return notesList[i].ID < notesList[j].ID
	})
	for i := range notesList {
		note := notesList[i]
		if _, ok := handledNoteIDs[note.ID]; ok {
			continue
		}

		state, hasState := statesByNote[note.ID]
		if hasState && state.Status == "external_deleted" {
			continue
		}
		if hasState && notionLocalContentHash(note) == state.ContentHash {
			continue
		}

		item := svc.pushNotionLocalNote(config, target, note, state, hasState)
		if item.Status == "failed" {
			result.Failed++
		} else {
			result.Pushed++
		}
		result.Items = append(result.Items, item)
	}

	return result
}

func (svc *NotionSyncService) PullRemote(target model.SyncTarget) model.NotionBidirectionalResult {
	result := model.NotionBidirectionalResult{
		Items: make([]model.SyncResultItem, 0),
	}
	if svc == nil || svc.gateway == nil {
		return failedNotionBidirectionalResult(errors.New("notion sync gateway is required"))
	}

	config, err := parseNotionTargetConfig(&target)
	if err != nil {
		return failedNotionBidirectionalResult(err)
	}
	if len(config.RequiredTags) == 0 {
		return result
	}
	if err := svc.gateway.TestDataSource(config); err != nil {
		return failedNotionBidirectionalResult(fmt.Errorf("test notion data source: %w", err))
	}

	remoteNotes, err := svc.gateway.QueryRemoteNotes(config)
	if err != nil {
		return failedNotionBidirectionalResult(fmt.Errorf("query notion notes: %w", err))
	}
	statesList, err := repository.ListSyncStatesByTarget(target.ID)
	if err != nil {
		return failedNotionBidirectionalResult(fmt.Errorf("load notion sync states: %w", err))
	}
	notesList, err := repository.ListAllNotes()
	if err != nil {
		return failedNotionBidirectionalResult(fmt.Errorf("load notes: %w", err))
	}

	notes := notesByID(notesList)
	statesByNote := statesByNoteID(statesList)
	statesByExternal := notionStatesByExternalID(statesList)
	remoteExternalIDs := make(map[string]struct{}, len(remoteNotes))
	handledNoteIDs := make(map[string]struct{})

	sort.Slice(remoteNotes, func(i, j int) bool {
		return remoteNotes[i].PageID < remoteNotes[j].PageID
	})
	for _, remote := range remoteNotes {
		remote = withNotionRemoteDefaults(remote)
		if strings.TrimSpace(remote.PageID) == "" {
			addNotionFailure(&result, model.SyncResultItem{
				Status:       "failed",
				ErrorMessage: "notion page id is required",
			})
			continue
		}

		remoteExternalIDs[remote.PageID] = struct{}{}
		if !tagsMatchRequiredSyncTags(remote.Tags, config.RequiredTags) {
			continue
		}
		state, hasState := matchNotionSyncState(remote, statesByNote, statesByExternal)
		note, hasNote := matchNotionLocalNote(remote, state, hasState, notes)

		if len(remote.UnsupportedTypes) > 0 {
			if hasNote {
				handledNoteIDs[note.ID] = struct{}{}
			}
			result.Unsupported++
			result.Items = append(result.Items, model.SyncResultItem{
				NoteID:       notionMatchedNoteID(remote, state, hasState),
				Status:       "unsupported",
				ExternalPath: notionExternalPath(remote.PageID),
				ExternalID:   remote.PageID,
				ExternalURL:  remote.URL,
				ErrorMessage: "unsupported Notion block types: " + strings.Join(remote.UnsupportedTypes, ", "),
			})
			continue
		}

		if !hasNote {
			item := importNotionRemoteNote(remote, target)
			if item.Status == "failed" {
				result.Failed++
			} else {
				result.Imported++
				handledNoteIDs[item.NoteID] = struct{}{}
			}
			result.Items = append(result.Items, item)
			continue
		}

		if !hasState || notionRemoteHash(remote) != state.ExternalHash || state.Status == "external_deleted" {
			conflict := hasState && notionLocalContentHash(note) != state.ContentHash
			item := pullNotionRemoteIntoNote(note, remote, target)
			if item.Status == "failed" {
				result.Failed++
			} else {
				result.Pulled++
				if conflict {
					result.ConflictPulled++
				}
				handledNoteIDs[note.ID] = struct{}{}
			}
			result.Items = append(result.Items, item)
		}
	}

	for _, state := range statesList {
		if state.Status != "synced" {
			continue
		}
		externalID := notionStateExternalID(state)
		if externalID == "" {
			continue
		}
		if _, ok := remoteExternalIDs[externalID]; ok {
			continue
		}
		if _, ok := notes[state.NoteID]; !ok {
			continue
		}
		if _, ok := handledNoteIDs[state.NoteID]; ok {
			continue
		}
		item := markNotionExternalDeleted(state, target)
		if item.Status == "failed" {
			result.Failed++
		} else {
			result.ExternalDeleted++
			handledNoteIDs[state.NoteID] = struct{}{}
		}
		result.Items = append(result.Items, item)
	}

	return result
}

func (svc *NotionSyncService) PushAll(target model.SyncTarget) model.SyncBatchResult {
	result := model.SyncBatchResult{
		Items: make([]model.SyncResultItem, 0),
	}
	if svc == nil || svc.gateway == nil {
		return failedNotionBatchResult(errors.New("notion sync gateway is required"))
	}

	config, err := parseNotionTargetConfig(&target)
	if err != nil {
		return failedNotionBatchResult(err)
	}
	if len(config.RequiredTags) == 0 {
		return result
	}
	if err := svc.gateway.TestDataSource(config); err != nil {
		return failedNotionBatchResult(fmt.Errorf("test notion data source: %w", err))
	}
	notesList, err := repository.ListAllNotes()
	if err != nil {
		return failedNotionBatchResult(fmt.Errorf("load notes: %w", err))
	}
	statesList, err := repository.ListSyncStatesByTarget(target.ID)
	if err != nil {
		return failedNotionBatchResult(fmt.Errorf("load notion sync states: %w", err))
	}
	statesByNote := statesByNoteID(statesList)

	sort.Slice(notesList, func(i, j int) bool {
		return notesList[i].ID < notesList[j].ID
	})
	for i := range notesList {
		note := notesList[i]
		if !noteMatchesRequiredSyncTags(note, config.RequiredTags) {
			continue
		}
		state, hasState := statesByNote[note.ID]
		if hasState && state.Status == "external_deleted" {
			continue
		}
		if hasState && notionLocalContentHash(note) == state.ContentHash {
			continue
		}

		item := svc.pushNotionLocalNote(config, target, note, state, hasState)
		if item.Status == "failed" {
			result.Failed++
		} else {
			result.Synced++
		}
		result.Items = append(result.Items, item)
	}

	return result
}

func (svc *NotionSyncService) pushNotionLocalNote(config notionTargetConfig, target model.SyncTarget, note model.Note, state model.SyncState, hasState bool) model.SyncResultItem {
	var remote notionRemoteNote
	var err error
	if hasState && notionStateExternalID(state) != "" {
		remote, err = svc.gateway.UpdateRemoteNote(config, notionStateExternalID(state), &note)
	} else {
		remote, err = svc.gateway.CreateRemoteNote(config, &note)
	}
	if err != nil {
		return model.SyncResultItem{
			NoteID:       note.ID,
			Status:       "failed",
			ExternalPath: state.ExternalPath,
			ExternalID:   state.ExternalID,
			ExternalURL:  state.ExternalURL,
			ErrorMessage: fmt.Errorf("push notion note: %w", err).Error(),
		}
	}
	remote = withNotionRemoteDefaults(remote)
	if remote.PageID == "" && hasState {
		remote.PageID = notionStateExternalID(state)
	}
	if remote.URL == "" && hasState {
		remote.URL = state.ExternalURL
	}
	if err := recordSyncedNotionRemote(&note, target, remote, "push"); err != nil {
		return model.SyncResultItem{
			NoteID:       note.ID,
			Status:       "failed",
			ExternalPath: notionExternalPath(remote.PageID),
			ExternalID:   remote.PageID,
			ExternalURL:  remote.URL,
			ErrorMessage: fmt.Errorf("record pushed notion sync state: %w", err).Error(),
		}
	}
	return model.SyncResultItem{
		NoteID:       note.ID,
		Status:       "pushed",
		ExternalPath: notionExternalPath(remote.PageID),
		ExternalID:   remote.PageID,
		ExternalURL:  remote.URL,
	}
}

func importNotionRemoteNote(remote notionRemoteNote, target model.SyncTarget) model.SyncResultItem {
	if store := repository.CurrentStore(); store != nil {
		return importNotionRemoteNoteWithStore(store, remote, target)
	}
	note, err := repository.CreateNoteWithID(&model.CreateNoteWithIDRequest{
		ID:       strings.TrimSpace(remote.FlowSpaceID),
		Title:    notionRemoteTitle(remote),
		Body:     remote.Markdown,
		FolderID: "__uncategorized",
		Tags:     syncTagsJSON(remote.Tags),
	})
	if err != nil {
		return model.SyncResultItem{
			Status:       "failed",
			ExternalPath: notionExternalPath(remote.PageID),
			ExternalID:   remote.PageID,
			ExternalURL:  remote.URL,
			ErrorMessage: fmt.Errorf("import notion note: %w", err).Error(),
		}
	}
	if err := recordSyncedNotionRemote(note, target, remote, "import"); err != nil {
		return model.SyncResultItem{
			NoteID:       note.ID,
			Status:       "failed",
			ExternalPath: notionExternalPath(remote.PageID),
			ExternalID:   remote.PageID,
			ExternalURL:  remote.URL,
			ErrorMessage: fmt.Errorf("record imported notion sync state: %w", err).Error(),
		}
	}
	return model.SyncResultItem{
		NoteID:       note.ID,
		Status:       "imported",
		ExternalPath: notionExternalPath(remote.PageID),
		ExternalID:   remote.PageID,
		ExternalURL:  remote.URL,
	}
}

func importNotionRemoteNoteWithStore(store storage.Store, remote notionRemoteNote, target model.SyncTarget) model.SyncResultItem {
	note := &model.Note{
		ID:       strings.TrimSpace(remote.FlowSpaceID),
		Title:    notionRemoteTitle(remote),
		Body:     remote.Markdown,
		FolderID: "__uncategorized",
		Tags:     syncTagsJSON(remote.Tags),
	}
	ctx := context.Background()
	err := store.Transact(ctx, func(txStore storage.Store) error {
		if strings.TrimSpace(note.ID) != "" {
			if err := txStore.Sync().LockBindingSlot(ctx, note.ID); err != nil {
				return err
			}
		}
		if err := txStore.Notes().CreateWithID(ctx, note); err != nil {
			return fmt.Errorf("import notion note: %w", err)
		}
		if err := txStore.Sync().LockBindingSlot(ctx, note.ID); err != nil {
			return err
		}
		if err := bindImportedNoteToSyncTargetInStore(ctx, txStore, note.ID, target.ID); err != nil {
			return err
		}
		state, err := notionSyncedState(note, target, remote, "import")
		if err != nil {
			return err
		}
		if err := txStore.Sync().UpsertState(ctx, state); err != nil {
			return fmt.Errorf("record imported notion sync state: %w", err)
		}
		return nil
	})
	if err != nil {
		return model.SyncResultItem{
			NoteID:       note.ID,
			Status:       "failed",
			ExternalPath: notionExternalPath(remote.PageID),
			ExternalID:   remote.PageID,
			ExternalURL:  remote.URL,
			ErrorMessage: err.Error(),
		}
	}
	return model.SyncResultItem{
		NoteID:       note.ID,
		Status:       "imported",
		ExternalPath: notionExternalPath(remote.PageID),
		ExternalID:   remote.PageID,
		ExternalURL:  remote.URL,
	}
}

func pullNotionRemoteIntoNote(note model.Note, remote notionRemoteNote, target model.SyncTarget) model.SyncResultItem {
	if store := repository.CurrentStore(); store != nil {
		return pullNotionRemoteIntoNoteWithStore(store, note, remote, target)
	}
	title := notionRemoteTitle(remote)
	body := remote.Markdown
	tags := syncTagsJSON(remote.Tags)
	updated, err := repository.UpdateNote(note.ID, &model.UpdateNoteRequest{
		Title: &title,
		Body:  &body,
		Tags:  &tags,
	})
	if err != nil {
		return model.SyncResultItem{
			NoteID:       note.ID,
			Status:       "failed",
			ExternalPath: notionExternalPath(remote.PageID),
			ExternalID:   remote.PageID,
			ExternalURL:  remote.URL,
			ErrorMessage: fmt.Errorf("pull notion note: %w", err).Error(),
		}
	}
	if err := recordSyncedNotionRemote(updated, target, remote, "pull"); err != nil {
		return model.SyncResultItem{
			NoteID:       note.ID,
			Status:       "failed",
			ExternalPath: notionExternalPath(remote.PageID),
			ExternalID:   remote.PageID,
			ExternalURL:  remote.URL,
			ErrorMessage: fmt.Errorf("record pulled notion sync state: %w", err).Error(),
		}
	}
	return model.SyncResultItem{
		NoteID:       note.ID,
		Status:       "pulled",
		ExternalPath: notionExternalPath(remote.PageID),
		ExternalID:   remote.PageID,
		ExternalURL:  remote.URL,
	}
}

func pullNotionRemoteIntoNoteWithStore(store storage.Store, note model.Note, remote notionRemoteNote, target model.SyncTarget) model.SyncResultItem {
	ctx := context.Background()
	title := notionRemoteTitle(remote)
	body := remote.Markdown
	tags := syncTagsJSON(remote.Tags)
	var updated *model.Note
	err := store.Transact(ctx, func(txStore storage.Store) error {
		if err := txStore.Sync().LockBindingSlot(ctx, note.ID); err != nil {
			return err
		}
		if err := ensureNoteBoundToSyncTargetInStore(ctx, txStore, note.ID, target.ID); err != nil {
			return err
		}
		var err error
		updated, err = txStore.Notes().Update(ctx, note.ID, &model.UpdateNoteRequest{
			Title: &title,
			Body:  &body,
			Tags:  &tags,
		})
		if err != nil {
			return fmt.Errorf("pull notion note: %w", err)
		}
		state, err := notionSyncedState(updated, target, remote, "pull")
		if err != nil {
			return err
		}
		if err := txStore.Sync().UpsertState(ctx, state); err != nil {
			return fmt.Errorf("record pulled notion sync state: %w", err)
		}
		return nil
	})
	if err != nil {
		return model.SyncResultItem{
			NoteID:       note.ID,
			Status:       "failed",
			ExternalPath: notionExternalPath(remote.PageID),
			ExternalID:   remote.PageID,
			ExternalURL:  remote.URL,
			ErrorMessage: err.Error(),
		}
	}
	return model.SyncResultItem{
		NoteID:       updated.ID,
		Status:       "pulled",
		ExternalPath: notionExternalPath(remote.PageID),
		ExternalID:   remote.PageID,
		ExternalURL:  remote.URL,
	}
}

func recordSyncedNotionRemote(note *model.Note, target model.SyncTarget, remote notionRemoteNote, direction string) error {
	state, err := notionSyncedState(note, target, remote, direction)
	if err != nil {
		return err
	}
	return repository.UpsertSyncState(state)
}

func notionSyncedState(note *model.Note, target model.SyncTarget, remote notionRemoteNote, direction string) (*model.SyncState, error) {
	if note == nil {
		return nil, errors.New("note is required")
	}
	if target.ID == "" {
		return nil, errors.New("target id is required")
	}
	if strings.TrimSpace(remote.PageID) == "" {
		return nil, errors.New("notion page id is required")
	}

	now := time.Now().Unix()
	externalMTime := remote.LastEditedAt
	return &model.SyncState{
		NoteID:        note.ID,
		TargetID:      target.ID,
		ExternalPath:  notionExternalPath(remote.PageID),
		ExternalID:    remote.PageID,
		ExternalURL:   remote.URL,
		ContentHash:   notionLocalContentHash(*note),
		ExternalHash:  notionRemoteHash(remote),
		ExternalMTime: &externalMTime,
		LastDirection: direction,
		LastSyncedAt:  &now,
		Status:        "synced",
		ErrorMessage:  nil,
	}, nil
}

func markNotionExternalDeleted(state model.SyncState, target model.SyncTarget) model.SyncResultItem {
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
			ExternalID:   state.ExternalID,
			ExternalURL:  state.ExternalURL,
			ErrorMessage: fmt.Errorf("mark notion external deletion: %w", err).Error(),
		}
	}
	return model.SyncResultItem{
		NoteID:       state.NoteID,
		Status:       "external_deleted",
		ExternalPath: state.ExternalPath,
		ExternalID:   state.ExternalID,
		ExternalURL:  state.ExternalURL,
	}
}

func matchNotionSyncState(remote notionRemoteNote, statesByNote map[string]model.SyncState, statesByExternal map[string]model.SyncState) (model.SyncState, bool) {
	if noteID := strings.TrimSpace(remote.FlowSpaceID); noteID != "" {
		if state, ok := statesByNote[noteID]; ok {
			return state, true
		}
	}
	if state, ok := statesByExternal[remote.PageID]; ok {
		return state, true
	}
	return model.SyncState{}, false
}

func matchNotionLocalNote(remote notionRemoteNote, state model.SyncState, hasState bool, notes map[string]model.Note) (model.Note, bool) {
	if noteID := strings.TrimSpace(remote.FlowSpaceID); noteID != "" {
		if note, ok := notes[noteID]; ok {
			return note, true
		}
	}
	if hasState {
		if note, ok := notes[state.NoteID]; ok {
			return note, true
		}
	}
	return model.Note{}, false
}

func notionMatchedNoteID(remote notionRemoteNote, state model.SyncState, hasState bool) string {
	if noteID := strings.TrimSpace(remote.FlowSpaceID); noteID != "" {
		return noteID
	}
	if hasState {
		return state.NoteID
	}
	return ""
}

func notionStatesByExternalID(states []model.SyncState) map[string]model.SyncState {
	byExternalID := make(map[string]model.SyncState, len(states))
	for _, state := range states {
		if externalID := notionStateExternalID(state); externalID != "" {
			byExternalID[externalID] = state
		}
	}
	return byExternalID
}

func notionStateExternalID(state model.SyncState) string {
	if externalID := strings.TrimSpace(state.ExternalID); externalID != "" {
		return externalID
	}
	return strings.TrimPrefix(strings.TrimSpace(state.ExternalPath), "notion:")
}

func notionLocalContentHash(note model.Note) string {
	return notionTitleBodyHash(note.Title, note.Body)
}

func notionRemoteHash(remote notionRemoteNote) string {
	return notionTitleBodyHash(notionRemoteTitle(remote), remote.Markdown)
}

func withNotionRemoteDefaults(remote notionRemoteNote) notionRemoteNote {
	remote.PageID = strings.TrimSpace(remote.PageID)
	remote.URL = strings.TrimSpace(remote.URL)
	remote.FlowSpaceID = strings.TrimSpace(remote.FlowSpaceID)
	remote.Tags = cleanSyncTags(remote.Tags)
	if strings.TrimSpace(remote.Hash) == "" {
		remote.Hash = notionRemoteHash(remote)
	}
	return remote
}

func notionTitleBodyHash(title, markdown string) string {
	title = strings.TrimSpace(title)
	body := canonicalNotionMarkdown(markdown)
	return notionMarkdownHash(fmt.Sprintf("title:%d:%s\nbody:%d:%s", len(title), title, len(body), body))
}

func notionRemoteTitle(remote notionRemoteNote) string {
	title := strings.TrimSpace(remote.Title)
	if title == "" {
		return "Untitled"
	}
	return title
}

func notionExternalPath(pageID string) string {
	pageID = strings.TrimSpace(pageID)
	if pageID == "" {
		return ""
	}
	return "notion:" + pageID
}

func addNotionFailure(result *model.NotionBidirectionalResult, item model.SyncResultItem) {
	result.Failed++
	item.Status = "failed"
	result.Items = append(result.Items, item)
}

func failedNotionBidirectionalResult(err error) model.NotionBidirectionalResult {
	message := ""
	if err != nil {
		message = err.Error()
	}
	return model.NotionBidirectionalResult{
		Failed: 1,
		Items: []model.SyncResultItem{
			{
				Status:       "failed",
				ErrorMessage: message,
			},
		},
	}
}

func failedNotionBatchResult(err error) model.SyncBatchResult {
	message := ""
	if err != nil {
		message = err.Error()
	}
	return model.SyncBatchResult{
		Failed: 1,
		Items: []model.SyncResultItem{
			{
				Status:       "failed",
				ErrorMessage: message,
			},
		},
	}
}
