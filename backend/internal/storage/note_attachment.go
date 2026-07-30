package storage

import (
	"context"
	"errors"

	"github.com/hujinrun/flowspace/internal/model"
)

var ErrNoteAttachmentStoreUnavailable = errors.New("note attachment storage is unavailable")

type NoteAttachmentStore interface {
	NoteAttachments() NoteAttachmentRepository
}

type NoteAttachmentRepository interface {
	ListByNote(context.Context, string) ([]model.NoteAttachment, error)
	GetByNoteAndID(context.Context, string, string) (*model.NoteAttachment, error)
	Create(context.Context, *model.NoteAttachment) error
	Delete(context.Context, string, string) error
}

func NoteAttachmentStoreFrom(store Store) (NoteAttachmentStore, error) {
	attachmentStore, ok := store.(NoteAttachmentStore)
	if !ok || attachmentStore.NoteAttachments() == nil {
		return nil, ErrNoteAttachmentStoreUnavailable
	}
	return attachmentStore, nil
}
