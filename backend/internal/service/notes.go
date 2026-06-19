package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/repository"
	"github.com/hujinrun/flowspace/internal/storage"
)

func GetNotes(folderID, projectID, sort string, unassigned bool, page, pageSize int) ([]model.Note, int, error) {
	return repository.GetNotes(folderID, projectID, sort, unassigned, page, pageSize)
}

func GetNote(id string) (*model.Note, error) {
	return repository.GetNoteByID(id)
}

func CreateNote(req *model.CreateNoteRequest) (*model.Note, error) {
	if req.Tags == "" {
		req.Tags = "[]"
	}
	return repository.CreateNoteWithProjectIDs(req)
}

func UpdateNote(id string, req *model.UpdateNoteRequest) (*model.Note, error) {
	existing, err := repository.GetNoteByID(id)
	if err != nil {
		return nil, errors.New("note not found")
	}
	_ = existing
	return repository.UpdateNote(id, req)
}

func DeleteNote(id string) error {
	if store := repository.CurrentStore(); store != nil {
		return deleteNoteWithStore(store, id)
	}
	return repository.DeleteNote(id)
}

func deleteNoteWithStore(store storage.Store, id string) error {
	ctx := context.Background()
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
