package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

func GetNotes(ctx context.Context, store storage.Store, folderID, projectID, sort string, unassigned bool, page, pageSize int) ([]model.Note, int, error) {
	return store.Notes().List(ctx, storage.NoteFilter{
		FolderID:   folderID,
		ProjectID:  projectID,
		Sort:       sort,
		Unassigned: unassigned,
		Page:       page,
		PageSize:   pageSize,
	})
}

func GetNote(ctx context.Context, store storage.Store, id string) (*model.Note, error) {
	return store.Notes().GetByID(ctx, id)
}

func CreateNote(ctx context.Context, store storage.Store, req *model.CreateNoteRequest) (*model.Note, error) {
	if req.Tags == "" {
		req.Tags = "[]"
	}
	return store.Notes().Create(ctx, req)
}

func UpdateNote(ctx context.Context, store storage.Store, id string, req *model.UpdateNoteRequest) (*model.Note, error) {
	existing, err := store.Notes().GetByID(ctx, id)
	if err != nil {
		return nil, errors.New("note not found")
	}
	_ = existing
	return store.Notes().Update(ctx, id, req)
}

func DeleteNote(ctx context.Context, store storage.Store, id string) error {
	return deleteNoteWithStore(ctx, store, id)
}

func deleteNoteWithStore(ctx context.Context, store storage.Store, id string) error {
	return store.Transact(ctx, func(txStore storage.Store) error {
		if err := txStore.Sync().LockBindingSlot(ctx, id); err != nil {
			return err
		}
		claim, err := txStore.Sync().GetExternalClaimByNote(ctx, id)
		if err == nil {
			if err := txStore.Sync().PutImportTombstone(ctx, model.SyncImportTombstone{
				ExternalKey:  claim.ExternalKey,
				TargetID:     claim.TargetID,
				FormerNoteID: id,
				ExternalType: claim.ExternalType,
				ExternalID:   claim.ExternalID,
				ExternalPath: claim.ExternalPath,
				Reason:       "note_deleted",
			}); err != nil {
				return fmt.Errorf("write note deletion tombstone: %w", err)
			}
		} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("load external claim: %w", err)
		}
		if err := txStore.Notes().Delete(ctx, id); err != nil {
			return fmt.Errorf("delete note: %w", err)
		}
		return nil
	})
}
