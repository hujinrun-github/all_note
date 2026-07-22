package taskgeneration

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/taskdomain"
)

func TestWorkerLoopIsCancellableAndWaitsAfterEveryBatch(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	runner := &batchRunnerStub{errors: []error{errors.New("temporary claim outage"), nil, nil}}
	waiter := &recordingWaiter{cancelAfter: 3, cancel: cancel}
	loop, err := NewWorkerLoop(runner, 8, 250*time.Millisecond, WithWaiter(waiter), WithClock(func() time.Time {
		return time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := loop.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if runner.calls != 3 || len(waiter.durations) != 3 {
		t.Fatalf("calls=%d waits=%#v", runner.calls, waiter.durations)
	}
	for _, duration := range waiter.durations {
		if duration != 250*time.Millisecond {
			t.Fatalf("unexpected wait duration %s", duration)
		}
	}
}

func TestWorkerLoopRejectsBusyLoopConfiguration(t *testing.T) {
	if _, err := NewWorkerLoop(&batchRunnerStub{}, 1, 0); !errors.Is(err, ErrInvalidWorkerLoop) {
		t.Fatalf("zero poll interval accepted: %v", err)
	}
	if _, err := NewWorkerLoop(&batchRunnerStub{}, 0, time.Second); !errors.Is(err, ErrInvalidWorkerLoop) {
		t.Fatalf("zero batch accepted: %v", err)
	}
}

type batchRunnerStub struct {
	mu     sync.Mutex
	calls  int
	errors []error
}

func (s *batchRunnerStub) RunBatch(context.Context, taskdomain.GenerationBatchRequest) ([]taskdomain.GenerationWorkspaceResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	index := s.calls
	s.calls++
	if index < len(s.errors) {
		return nil, s.errors[index]
	}
	return nil, nil
}

type recordingWaiter struct {
	mu          sync.Mutex
	durations   []time.Duration
	cancelAfter int
	cancel      context.CancelFunc
}

func (w *recordingWaiter) Wait(ctx context.Context, duration time.Duration) error {
	w.mu.Lock()
	w.durations = append(w.durations, duration)
	count := len(w.durations)
	w.mu.Unlock()
	if count >= w.cancelAfter {
		w.cancel()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}
