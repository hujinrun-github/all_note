package mobilesyncpublisher

import (
	"context"
	"errors"
	"time"

	"github.com/hujinrun/flowspace/internal/storage"
)

type Worker struct {
	Store     storage.Store
	Now       func() time.Time
	BatchSize int
	Retention time.Duration
}

func NewWorker(store storage.Store) Worker {
	return Worker{Store: store, Now: time.Now, BatchSize: 1000, Retention: 30 * 24 * time.Hour}
}

func (w Worker) RunOne(ctx context.Context) (bool, error) {
	if w.Store == nil || w.Now == nil || w.BatchSize <= 0 || w.Retention < 30*24*time.Hour {
		return false, errors.New("mobile sync publisher worker dependencies are not configured")
	}
	repository, err := storage.MobileSyncPublisherRepositoryFrom(w.Store)
	if err != nil {
		return false, err
	}
	now := w.Now().UTC()
	published, err := repository.PublishNextWorkspace(ctx, w.BatchSize, now.Unix())
	if err != nil {
		return false, err
	}
	if _, err := repository.PruneExpired(ctx, now.Add(-w.Retention).Unix()); err != nil {
		return published > 0, err
	}
	return published > 0, nil
}

func (w Worker) Run(ctx context.Context, idleDelay time.Duration, onError func(error)) {
	if idleDelay <= 0 {
		idleDelay = 250 * time.Millisecond
	}
	for {
		published, err := w.RunOne(ctx)
		if err != nil && onError != nil {
			onError(err)
		}
		if ctx.Err() != nil {
			return
		}
		if published && err == nil {
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
