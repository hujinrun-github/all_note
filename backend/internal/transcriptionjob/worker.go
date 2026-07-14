package transcriptionjob

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/objectstore"
	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/transcription"
)

type Worker struct {
	Store             storage.Store
	Objects           objectstore.Store
	Transcriber       transcription.Transcriber
	WorkerID          string
	Now               func() time.Time
	NewLeaseToken     func() string
	LeaseDuration     time.Duration
	HeartbeatInterval time.Duration
	RetryDelay        func(attempt int64) time.Duration
}

func NewWorker(store storage.Store, objects objectstore.Store, transcriber transcription.Transcriber, workerID string) Worker {
	return Worker{
		Store: store, Objects: objects, Transcriber: transcriber, WorkerID: workerID,
		Now: time.Now, NewLeaseToken: uuid.NewString, LeaseDuration: 120 * time.Second, HeartbeatInterval: 30 * time.Second,
		RetryDelay: func(attempt int64) time.Duration {
			if attempt <= 1 {
				return 30 * time.Second
			}
			return time.Duration(attempt) * time.Minute
		},
	}
}

func (w Worker) RunOne(ctx context.Context) (bool, error) {
	if w.Store == nil || w.Objects == nil || w.Transcriber == nil || w.WorkerID == "" || w.Now == nil || w.NewLeaseToken == nil || w.LeaseDuration <= 0 || w.HeartbeatInterval <= 0 || w.RetryDelay == nil {
		return false, errors.New("transcription worker dependencies are not configured")
	}
	workerRepository, err := storage.TranscriptionJobWorkerRepositoryFrom(w.Store)
	if err != nil {
		return false, err
	}
	now := w.Now().UTC()
	lease, err := workerRepository.ClaimNext(ctx, model.ClaimTranscriptionJob{
		WorkerID: w.WorkerID, LeaseToken: w.NewLeaseToken(), Now: now.Unix(),
		LeaseExpiresAt: now.Add(w.LeaseDuration).Unix(),
	})
	if errors.Is(err, storage.ErrNoTranscriptionJob) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	jobContext := auth.ContextWithWorkspaceScope(ctx, lease.WorkspaceID)
	nativeStore, err := storage.NativeStoreFrom(w.Store)
	if err != nil {
		return true, w.fail(ctx, workerRepository, lease, "native_storage_unavailable")
	}
	voice, err := nativeStore.VoiceNotes().GetByClientID(jobContext, lease.Job.VoiceNoteID)
	if err != nil {
		return true, w.fail(ctx, workerRepository, lease, "voice_note_unavailable")
	}
	if voice.UploadState != model.VoiceUploadUploaded || voice.ObjectKey == "" {
		return true, w.fail(ctx, workerRepository, lease, "audio_not_ready")
	}
	object, err := w.Objects.Get(jobContext, voice.ObjectKey)
	if err != nil {
		return true, w.fail(ctx, workerRepository, lease, "audio_read_failed")
	}
	text, transcribeErr, heartbeatErr := w.transcribeWithHeartbeat(jobContext, workerRepository, lease, transcription.Input{
		Audio: object.Body, Filename: voice.ClientID + ".m4a", ContentType: voice.MimeType, Language: lease.Job.Language,
	})
	closeErr := object.Body.Close()
	if heartbeatErr != nil {
		return true, fmt.Errorf("heartbeat transcription job: %w", heartbeatErr)
	}
	if transcribeErr != nil {
		if failErr := w.fail(ctx, workerRepository, lease, "provider_failed"); failErr != nil {
			return true, errors.Join(transcribeErr, failErr)
		}
		return true, nil
	}
	if closeErr != nil {
		return true, w.fail(ctx, workerRepository, lease, "audio_close_failed")
	}
	completedAt := w.Now().UTC().Unix()
	if _, err := workerRepository.Complete(ctx, model.CompleteTranscriptionJob{
		JobID: lease.Job.JobID, LeaseToken: lease.LeaseToken, Text: text, Now: completedAt,
	}); err != nil {
		return true, fmt.Errorf("complete transcription job: %w", err)
	}
	return true, nil
}

func (w Worker) Run(ctx context.Context, idleDelay time.Duration, onError func(error)) {
	if idleDelay <= 0 {
		idleDelay = time.Second
	}
	for {
		claimed, err := w.RunOne(ctx)
		if err != nil && onError != nil {
			onError(err)
		}
		if ctx.Err() != nil {
			return
		}
		if claimed && err == nil {
			continue
		}
		timer := time.NewTimer(idleDelay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
		}
	}
}

func (w Worker) transcribeWithHeartbeat(
	ctx context.Context,
	repository storage.TranscriptionJobWorkerRepository,
	lease *model.TranscriptionJobLease,
	input transcription.Input,
) (string, error, error) {
	providerCtx, cancelProvider := context.WithCancel(ctx)
	defer cancelProvider()
	heartbeatDone := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(w.HeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-providerCtx.Done():
				heartbeatDone <- nil
				return
			case <-ticker.C:
				now := w.Now().UTC()
				_, err := repository.Heartbeat(providerCtx, model.HeartbeatTranscriptionJob{
					JobID: lease.Job.JobID, LeaseToken: lease.LeaseToken, Now: now.Unix(),
					LeaseExpiresAt: now.Add(w.LeaseDuration).Unix(),
				})
				if err != nil {
					cancelProvider()
					heartbeatDone <- err
					return
				}
			}
		}
	}()
	text, err := w.Transcriber.Transcribe(providerCtx, input)
	cancelProvider()
	heartbeatErr := <-heartbeatDone
	return text, err, heartbeatErr
}

func (w Worker) fail(ctx context.Context, repository storage.TranscriptionJobWorkerRepository, lease *model.TranscriptionJobLease, code string) error {
	failedAt := w.Now().UTC()
	_, err := repository.Fail(ctx, model.FailTranscriptionJob{
		JobID: lease.Job.JobID, LeaseToken: lease.LeaseToken, ErrorCode: code,
		NextAttemptAt: failedAt.Add(w.RetryDelay(lease.Job.Attempt)).Unix(), Now: failedAt.Unix(),
	})
	if err != nil {
		return fmt.Errorf("record transcription job failure: %w", err)
	}
	return nil
}
