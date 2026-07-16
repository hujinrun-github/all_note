package storage

import (
	"context"
	"errors"

	"github.com/hujinrun/flowspace/internal/model"
)

var (
	ErrVoiceAudioCleanupStorage   = errors.New("voice audio cleanup storage is unavailable")
	ErrNoVoiceAudioCleanupJob     = errors.New("no voice audio cleanup job is eligible")
	ErrVoiceAudioCleanupLeaseLost = errors.New("voice audio cleanup lease was lost")
)

type VoiceAudioCleanupStore interface {
	VoiceAudioCleanup() VoiceAudioCleanupRepository
}

func VoiceAudioCleanupRepositoryFrom(store Store) (VoiceAudioCleanupRepository, error) {
	cleanupStore, ok := store.(VoiceAudioCleanupStore)
	if !ok || cleanupStore.VoiceAudioCleanup() == nil {
		return nil, ErrVoiceAudioCleanupStorage
	}
	return cleanupStore.VoiceAudioCleanup(), nil
}

type VoiceAudioCleanupRepository interface {
	ClaimNext(context.Context, model.ClaimVoiceAudioCleanupJob) (*model.VoiceAudioCleanupLease, error)
	Complete(context.Context, model.CompleteVoiceAudioCleanupJob) (*model.VoiceAudioCleanupJob, error)
	Fail(context.Context, model.FailVoiceAudioCleanupJob) (*model.VoiceAudioCleanupJob, error)
	Get(context.Context, string) (*model.VoiceAudioCleanupJob, error)
}
