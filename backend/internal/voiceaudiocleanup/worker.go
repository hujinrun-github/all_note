package voiceaudiocleanup

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
)

type Worker struct {
	Store         storage.Store
	Objects       objectstore.Store
	WorkerID      string
	Now           func() time.Time
	NewLeaseToken func() string
	LeaseDuration time.Duration
	RetryDelay    func(attempt int64) time.Duration
}

func NewWorker(store storage.Store, objects objectstore.Store, workerID string) Worker {
	return Worker{
		Store: store, Objects: objects, WorkerID: workerID,
		Now: time.Now, NewLeaseToken: uuid.NewString, LeaseDuration: 120 * time.Second,
		RetryDelay: func(attempt int64) time.Duration {
			if attempt <= 1 {
				return 30 * time.Second
			}
			return time.Duration(attempt) * time.Minute
		},
	}
}

func (w Worker) RunOne(ctx context.Context) (bool, error) {
	if w.Store == nil || w.Objects == nil || w.WorkerID == "" || w.Now == nil || w.NewLeaseToken == nil || w.LeaseDuration <= 0 || w.RetryDelay == nil {
		return false, errors.New("voice audio cleanup worker dependencies are not configured")
	}
	repository, err := storage.VoiceAudioCleanupRepositoryFrom(w.Store)
	if err != nil {
		return false, err
	}
	now := w.Now().UTC()
	lease, err := repository.ClaimNext(ctx, model.ClaimVoiceAudioCleanupJob{
		WorkerID: w.WorkerID, LeaseToken: w.NewLeaseToken(), Now: now.Unix(), LeaseExpiresAt: now.Add(w.LeaseDuration).Unix(),
	})
	if errors.Is(err, storage.ErrNoVoiceAudioCleanupJob) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	jobCtx := auth.ContextWithWorkspaceScope(ctx, lease.WorkspaceID)
	removeErr := w.Objects.Remove(jobCtx, lease.Job.ObjectKey)
	if removeErr != nil && !errors.Is(removeErr, objectstore.ErrNotFound) {
		failedAt := w.Now().UTC()
		if _, err := repository.Fail(ctx, model.FailVoiceAudioCleanupJob{
			JobID: lease.Job.JobID, LeaseToken: lease.LeaseToken, ErrorCode: "object_remove_failed",
			NextAttemptAt: failedAt.Add(w.RetryDelay(lease.Job.Attempt)).Unix(), Now: failedAt.Unix(),
		}); err != nil {
			return true, fmt.Errorf("record voice audio cleanup failure: %w", err)
		}
		return true, nil
	}
	completedAt := w.Now().UTC().Unix()
	if _, err := repository.Complete(ctx, model.CompleteVoiceAudioCleanupJob{
		JobID: lease.Job.JobID, LeaseToken: lease.LeaseToken, Now: completedAt,
	}); err != nil {
		return true, fmt.Errorf("complete voice audio cleanup: %w", err)
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
