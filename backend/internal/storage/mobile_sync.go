package storage

import (
	"context"
	"errors"

	"github.com/hujinrun/flowspace/internal/model"
)

var (
	ErrMutationIDReused      = errors.New("mutation ID was reused with a different request")
	ErrRevisionConflict      = errors.New("base revision does not match current revision")
	ErrMobileEntityNotFound  = errors.New("mobile entity was not found")
	ErrMobileEntityGone      = errors.New("mobile entity is tombstoned")
	ErrMobileSyncStorage     = errors.New("mobile sync storage is unavailable")
	ErrMobileCursorExpired   = errors.New("mobile sync cursor is no longer retained")
	ErrMobileSnapshotExpired = errors.New("mobile sync snapshot session expired")
)

type MobileSyncStore interface {
	MobileSync() MobileSyncRepository
}

func MobileSyncRepositoryFrom(store Store) (MobileSyncRepository, error) {
	mobileStore, ok := store.(MobileSyncStore)
	if !ok || mobileStore.MobileSync() == nil {
		return nil, ErrMobileSyncStorage
	}
	return mobileStore.MobileSync(), nil
}

type MobileSyncRepository interface {
	ApplyNoteMutation(context.Context, model.MobileNoteMutation) (*model.MobileMutationResult, error)
	ApplyEntityMutation(context.Context, model.MobileEntityMutation) (*model.MobileMutationResult, error)
	GetNoteByClientID(context.Context, string) (*model.MobileEntityEnvelope, error)
	GetEntityByClientID(context.Context, string, string) (*model.MobileEntityEnvelope, error)
	ListPendingChanges(context.Context) ([]model.MobileChange, error)
	PublishPendingChanges(context.Context, int, int64) (int, error)
	ReadCommittedChanges(context.Context, int64, int) (*model.MobileCommittedChangePage, error)
	PruneCommittedChanges(context.Context, int64) error
	BeginSnapshot(context.Context, model.BeginMobileSnapshot) (*model.MobileSnapshot, error)
	ReadSnapshot(context.Context, model.ReadMobileSnapshot) (*model.MobileSnapshotPage, error)
}
