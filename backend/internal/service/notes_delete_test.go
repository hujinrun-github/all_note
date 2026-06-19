package service

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/repository"
	"github.com/hujinrun/flowspace/internal/storage"
)

func TestDeleteNoteWritesTombstoneBeforeDeletingBoundNote(t *testing.T) {
	store := openServiceSyncStoreTestDB(t)
	target := saveServiceStoreNotionTarget(t, `{"data_source_id":"ds-delete"}`)
	note := createServiceStoreNote(t, "Delete Tombstone", "Body\n", "[]")
	putServiceStoreBinding(t, store, note.ID, target.ID)
	if err := store.Sync().PutExternalClaim(t.Context(), model.SyncExternalClaim{
		ExternalKey:  "notion:page-delete",
		NoteID:       note.ID,
		TargetID:     target.ID,
		ExternalType: "notion_page",
		ExternalID:   "page-delete",
		ExternalPath: "notion:page-delete",
	}); err != nil {
		t.Fatalf("put claim: %v", err)
	}

	if err := DeleteNote(note.ID); err != nil {
		t.Fatalf("delete note: %v", err)
	}

	if _, err := repository.GetNoteByID(note.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("note lookup error = %v, want sql.ErrNoRows", err)
	}
	tombstone, err := store.Sync().FindImportTombstone(t.Context(), target.ID, "notion:page-delete", note.ID, "notion_page")
	if err != nil {
		t.Fatalf("find tombstone: %v", err)
	}
	if tombstone.Reason != "note_deleted" || tombstone.ExternalID != "page-delete" {
		t.Fatalf("tombstone = %+v", tombstone)
	}
}

func TestDeleteNoteWithoutClaimDeletesNormally(t *testing.T) {
	openServiceSyncStoreTestDB(t)
	note := createServiceStoreNote(t, "Plain Delete", "Body\n", "[]")

	if err := DeleteNote(note.ID); err != nil {
		t.Fatalf("delete note: %v", err)
	}

	if _, err := repository.GetNoteByID(note.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("note lookup error = %v, want sql.ErrNoRows", err)
	}
}

func TestDeleteNoteRollsBackWhenTombstoneWriteFails(t *testing.T) {
	store := openServiceSyncStoreTestDB(t)
	target := saveServiceStoreNotionTarget(t, `{"data_source_id":"ds-delete"}`)
	note := createServiceStoreNote(t, "Rollback Tombstone", "Body\n", "[]")
	putServiceStoreBinding(t, store, note.ID, target.ID)
	if err := store.Sync().PutExternalClaim(t.Context(), model.SyncExternalClaim{
		ExternalKey:  "notion:page-rollback",
		NoteID:       note.ID,
		TargetID:     target.ID,
		ExternalType: "notion_page",
		ExternalID:   "page-rollback",
		ExternalPath: "notion:page-rollback",
	}); err != nil {
		t.Fatalf("put claim: %v", err)
	}
	remainingFailures := 1
	repository.SetStore(&putTombstoneFailOnceStore{
		Store:     store,
		err:       errors.New("tombstone database unavailable"),
		remaining: &remainingFailures,
	})

	err := DeleteNote(note.ID)

	if err == nil {
		t.Fatal("expected tombstone write failure")
	}
	if _, err := repository.GetNoteByID(note.ID); err != nil {
		t.Fatalf("note should remain after rollback: %v", err)
	}
	claim, err := store.Sync().GetExternalClaimByNote(t.Context(), note.ID)
	if err != nil {
		t.Fatalf("claim should remain after rollback: %v", err)
	}
	if claim.ExternalID != "page-rollback" {
		t.Fatalf("claim = %+v", claim)
	}
}

type putTombstoneFailOnceStore struct {
	storage.Store
	err       error
	remaining *int
}

func (store *putTombstoneFailOnceStore) Transact(ctx context.Context, fn func(storage.Store) error) error {
	return store.Store.Transact(ctx, func(txStore storage.Store) error {
		return fn(&putTombstoneFailOnceStore{
			Store:     txStore,
			err:       store.err,
			remaining: store.remaining,
		})
	})
}

func (store *putTombstoneFailOnceStore) Sync() storage.SyncRepository {
	return &putTombstoneFailOnceSyncRepository{
		SyncRepository: store.Store.Sync(),
		err:            store.err,
		remaining:      store.remaining,
	}
}

type putTombstoneFailOnceSyncRepository struct {
	storage.SyncRepository
	err       error
	remaining *int
}

func (repo *putTombstoneFailOnceSyncRepository) PutImportTombstone(ctx context.Context, tombstone model.SyncImportTombstone) error {
	if repo.remaining != nil && *repo.remaining > 0 {
		*repo.remaining--
		return repo.err
	}
	return repo.SyncRepository.PutImportTombstone(ctx, tombstone)
}
