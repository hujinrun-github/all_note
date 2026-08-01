package mobilev2service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hujinrun/flowspace/internal/objectstore"
	"github.com/hujinrun/flowspace/internal/storage"
)

type ActiveWorkspaceLister interface {
	ListActiveWorkspaceIDs(context.Context) ([]string, error)
}

type ContentRetentionWorkerConfig struct {
	Workspaces              ActiveWorkspaceLister
	Runtime                 RuntimeResolver
	Objects                 objectstore.Store
	DeletedContentRetention time.Duration
	Now                     func() time.Time
}

type ContentRetentionWorker struct {
	workspaces ActiveWorkspaceLister
	runtime    RuntimeResolver
	objects    objectstore.Store
	retention  time.Duration
	now        func() time.Time
}

func NewContentRetentionWorker(config ContentRetentionWorkerConfig) (*ContentRetentionWorker, error) {
	if config.Workspaces == nil || config.Runtime == nil {
		return nil, errors.New("mobile-v2 content retention workspace source and runtime are required")
	}
	if config.DeletedContentRetention <= 0 {
		config.DeletedContentRetention = defaultDeletedContentRetention
	}
	if config.Now == nil {
		config.Now = func() time.Time { return time.Now().UTC() }
	}
	return &ContentRetentionWorker{
		workspaces: config.Workspaces,
		runtime:    config.Runtime,
		objects:    config.Objects,
		retention:  config.DeletedContentRetention,
		now:        config.Now,
	}, nil
}

func (worker *ContentRetentionWorker) RunOnce(ctx context.Context) error {
	workspaceIDs, err := worker.workspaces.ListActiveWorkspaceIDs(ctx)
	if err != nil {
		return err
	}
	var joined error
	for _, workspaceID := range workspaceIDs {
		if err := worker.maintainWorkspace(ctx, workspaceID); err != nil {
			joined = errors.Join(joined, fmt.Errorf("workspace %s: %w", workspaceID, err))
		}
	}
	return joined
}

func (worker *ContentRetentionWorker) Run(ctx context.Context, interval time.Duration, onError func(error)) {
	if interval <= 0 {
		interval = time.Hour
	}
	run := func() {
		if err := worker.RunOnce(ctx); err != nil && ctx.Err() == nil && onError != nil {
			onError(err)
		}
	}
	run()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

func (worker *ContentRetentionWorker) maintainWorkspace(ctx context.Context, workspaceID string) error {
	runtime, err := worker.runtime.ResolveMobileRuntime(ctx, workspaceID)
	if err != nil {
		return err
	}
	if runtime.WorkspaceID != workspaceID || runtime.Epoch < 1 || runtime.DB == nil || runtime.Writer == nil {
		return errors.New("incomplete mobile-v2 retention runtime")
	}
	if err := drainTenantObjectCleanup(ctx, runtime, worker.objects, worker.now,
		"mobile-v2-retention-cleanup", 100); err != nil {
		return err
	}
	_, dialect, err := commandLedger(runtime)
	if err != nil {
		return err
	}
	return runtime.Writer.BeginFencedWrite(ctx, workspaceID, runtime.Epoch, func(tx storage.TenantWriteTx) error {
		mobileTx, ok := tx.(storage.MobileV2TenantWriteTx)
		if !ok || mobileTx.MobileV2SQLRunner() == nil {
			return errors.New("tenant transaction does not expose mobile-v2 retention capabilities")
		}
		return maintainContentRetention(ctx, mobileTx.MobileV2SQLRunner(), dialect, workspaceID,
			worker.now().UTC(), worker.retention)
	})
}

var _ interface {
	RunOnce(context.Context) error
} = (*ContentRetentionWorker)(nil)
