package taskgeneration

import (
	"context"
	"errors"
	"time"

	"github.com/hujinrun/flowspace/internal/taskdomain"
)

var ErrInvalidWorkerLoop = errors.New("invalid task generation worker loop")

type BatchRunner interface {
	RunBatch(context.Context, taskdomain.GenerationBatchRequest) ([]taskdomain.GenerationWorkspaceResult, error)
}

type Waiter interface {
	Wait(context.Context, time.Duration) error
}

type Option func(*loopRuntime) error

func WithWaiter(waiter Waiter) Option {
	return func(runtime *loopRuntime) error {
		if waiter == nil {
			return ErrInvalidWorkerLoop
		}
		runtime.waiter = waiter
		return nil
	}
}

func WithClock(now func() time.Time) Option {
	return func(runtime *loopRuntime) error {
		if now == nil {
			return ErrInvalidWorkerLoop
		}
		runtime.now = now
		return nil
	}
}

func WithErrorSink(onError func(error)) Option {
	return func(runtime *loopRuntime) error {
		if onError == nil {
			return ErrInvalidWorkerLoop
		}
		runtime.onError = onError
		return nil
	}
}

type loopRuntime struct {
	waiter  Waiter
	now     func() time.Time
	onError func(error)
}

func newLoopRuntime(options []Option) (loopRuntime, error) {
	runtime := loopRuntime{waiter: timerWaiter{}, now: time.Now}
	for _, option := range options {
		if option == nil {
			return loopRuntime{}, ErrInvalidWorkerLoop
		}
		if err := option(&runtime); err != nil {
			return loopRuntime{}, err
		}
	}
	return runtime, nil
}

type WorkerLoop struct {
	runner       BatchRunner
	batchSize    int
	pollInterval time.Duration
	runtime      loopRuntime
}

func NewWorkerLoop(runner BatchRunner, batchSize int, pollInterval time.Duration, options ...Option) (*WorkerLoop, error) {
	if runner == nil || batchSize < 1 || pollInterval <= 0 {
		return nil, ErrInvalidWorkerLoop
	}
	runtime, err := newLoopRuntime(options)
	if err != nil {
		return nil, err
	}
	return &WorkerLoop{runner: runner, batchSize: batchSize, pollInterval: pollInterval, runtime: runtime}, nil
}

// Run isolates failures at the batch boundary and waits after every attempt.
// Per-workspace isolation and normalized durable error codes are provided by
// taskdomain.GenerationWorker; this loop never persists raw errors itself.
func (l *WorkerLoop) Run(ctx context.Context) error {
	if l == nil || l.runner == nil || l.batchSize < 1 || l.pollInterval <= 0 || l.runtime.waiter == nil || l.runtime.now == nil {
		return ErrInvalidWorkerLoop
	}
	for {
		if ctx.Err() != nil {
			return nil
		}
		_, err := l.runner.RunBatch(ctx, taskdomain.GenerationBatchRequest{Limit: l.batchSize, Now: l.runtime.now()})
		if err != nil && l.runtime.onError != nil {
			l.runtime.onError(err)
		}
		if err := l.runtime.waiter.Wait(ctx, l.pollInterval); err != nil {
			if ctx.Err() != nil || errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}
	}
}

type timerWaiter struct{}

func (timerWaiter) Wait(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
