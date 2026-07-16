package storage

import (
	"context"
	"errors"

	"github.com/hujinrun/flowspace/internal/model"
)

var (
	ErrAlreadyExists  = errors.New("already exists")
	ErrUploadConflict = errors.New("audio upload conflicts with existing content")
	ErrVoiceAudioGone = errors.New("voice audio was deleted")
	ErrNativeStorage  = errors.New("native app storage is unavailable")
)

type NativeStore interface {
	WatchDevices() WatchDeviceRepository
	VoiceNotes() VoiceNoteRepository
}

func NativeStoreFrom(store Store) (NativeStore, error) {
	nativeStore, ok := store.(NativeStore)
	if !ok || nativeStore.WatchDevices() == nil || nativeStore.VoiceNotes() == nil {
		return nil, ErrNativeStorage
	}
	return nativeStore, nil
}

type WatchDeviceRepository interface {
	Create(context.Context, *model.WatchDevice) error
	GetActiveByTokenHash(context.Context, string) (*model.WatchDevice, error)
	Revoke(context.Context, string, string) error
	TouchLastSeen(context.Context, string, int64) error
}

type VoiceNoteRepository interface {
	Create(context.Context, *model.VoiceNote) error
	GetByClientID(context.Context, string) (*model.VoiceNote, error)
	ClaimUpload(context.Context, string, model.VoiceUploadClaim) (*model.VoiceNote, error)
	MarkUploaded(context.Context, string, string) (*model.VoiceNote, error)
	MarkUploadFailed(context.Context, string, string) error
	SetTranscriptionState(context.Context, string, string, string) (*model.VoiceNote, error)
	ListRecent(context.Context, int) ([]model.VoiceNote, error)
}
