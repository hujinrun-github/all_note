package storage

import (
	"context"
	"errors"

	"github.com/hujinrun/flowspace/internal/model"
)

var (
	ErrTranscriptionJobStorage      = errors.New("transcription job storage is unavailable")
	ErrNoTranscriptionJob           = errors.New("no transcription job is eligible")
	ErrTranscriptionLeaseLost       = errors.New("transcription job lease was lost")
	ErrTranscriptionJobNotRetryable = errors.New("transcription job is not retryable")
	ErrStaleTranscriptionJob        = errors.New("transcription job is not the latest generation")
)

type TranscriptionJobStore interface {
	TranscriptionJobs() TranscriptionJobRepository
}

func TranscriptionJobRepositoryFrom(store Store) (TranscriptionJobRepository, error) {
	jobStore, ok := store.(TranscriptionJobStore)
	if !ok || jobStore.TranscriptionJobs() == nil {
		return nil, ErrTranscriptionJobStorage
	}
	return jobStore.TranscriptionJobs(), nil
}

type TranscriptionJobRepository interface {
	CreateOrGet(context.Context, model.CreateTranscriptionJob) (*model.TranscriptionJob, error)
	Retry(context.Context, model.RetryTranscriptionJob) (*model.TranscriptionJob, error)
	Get(context.Context, string) (*model.TranscriptionJob, error)
}

type TranscriptionJobWorkerStore interface {
	TranscriptionJobWorker() TranscriptionJobWorkerRepository
}

func TranscriptionJobWorkerRepositoryFrom(store Store) (TranscriptionJobWorkerRepository, error) {
	workerStore, ok := store.(TranscriptionJobWorkerStore)
	if !ok || workerStore.TranscriptionJobWorker() == nil {
		return nil, ErrTranscriptionJobStorage
	}
	return workerStore.TranscriptionJobWorker(), nil
}

type TranscriptionJobWorkerRepository interface {
	ClaimNext(context.Context, model.ClaimTranscriptionJob) (*model.TranscriptionJobLease, error)
	Heartbeat(context.Context, model.HeartbeatTranscriptionJob) (*model.TranscriptionJobLease, error)
	Fail(context.Context, model.FailTranscriptionJob) (*model.TranscriptionJob, error)
	Complete(context.Context, model.CompleteTranscriptionJob) (*model.TranscriptionJob, error)
}
