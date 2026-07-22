package taskgeneration

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/generationclaims"
	"github.com/hujinrun/flowspace/internal/taskdomain"
)

func TestProductionDaemonRunsDurableSchedulerAndWorkerSQLite(t *testing.T) {
	repository, db := schedulerSQLiteRepository(t, time.Minute)
	seedSchedulerWorkspace(t, db, "daemon-v2")
	if _, err := db.Exec(`INSERT INTO workspace_runtime_state(workspace_id,mode,epoch,binding_revision,updated_by)
		VALUES('daemon-v2','active',4,1,'user-daemon-v2')`); err != nil {
		t.Fatal(err)
	}
	runtimes := productionRuntimeStub{fencer: &countingGenerationFencer{}}
	daemon, err := NewProductionDaemon(db, ControlDialectSQLite, repository, runtimes, ProductionConfig{
		SchedulerInterval: time.Hour, WorkerPollInterval: 5 * time.Millisecond, BatchSize: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := daemon.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = daemon.Close() })

	deadline := time.Now().Add(3 * time.Second)
	for {
		job, getErr := repository.Get(t.Context(), "daemon-v2")
		if getErr == nil && job.Status == generationclaims.StatusCompleted {
			if job.CreatedEpoch != 4 || job.RuntimeEpoch != 4 || runtimes.fencer.calls != 1 {
				t.Fatalf("completed job=%#v writes=%d", job, runtimes.fencer.calls)
			}
			break
		}
		if getErr != nil && !errors.Is(getErr, generationclaims.ErrJobNotFound) {
			t.Fatal(getErr)
		}
		if time.Now().After(deadline) {
			t.Fatalf("daemon did not complete generation job: %#v err=%v", job, getErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := daemon.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestDaemonStartAndCloseWaitsForBothLoops(t *testing.T) {
	scheduler := &runnableStub{started: make(chan struct{}), stopped: make(chan struct{})}
	worker := &runnableStub{started: make(chan struct{}), stopped: make(chan struct{})}
	daemon, err := newDaemon(scheduler, worker, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := daemon.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	<-scheduler.started
	<-worker.started
	if err := daemon.Close(); err != nil {
		t.Fatal(err)
	}
	<-scheduler.stopped
	<-worker.stopped
	if scheduler.calls != 1 || worker.calls != 1 {
		t.Fatalf("scheduler calls=%d worker calls=%d", scheduler.calls, worker.calls)
	}
	if err := daemon.Start(t.Context()); !errors.Is(err, ErrDaemonStarted) {
		t.Fatalf("daemon restarted: %v", err)
	}
}

func TestDaemonCancelsSiblingAndReturnsTerminalLoopError(t *testing.T) {
	terminal := errors.New("terminal loop failure")
	scheduler := &runnableStub{started: make(chan struct{}), stopped: make(chan struct{}), err: terminal}
	worker := &runnableStub{started: make(chan struct{}), stopped: make(chan struct{})}
	daemon, _ := newDaemon(scheduler, worker, nil)
	if err := daemon.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	<-scheduler.started
	<-worker.started
	if err := daemon.Close(); !errors.Is(err, terminal) {
		t.Fatalf("close error=%v", err)
	}
	<-worker.stopped
}

func TestNudgeRelayBindsExactlyOnce(t *testing.T) {
	relay := NewNudgeRelay()
	if err := relay.Nudge(t.Context(), "workspace-1", 1, time.Now()); !errors.Is(err, ErrNudgeTargetUnbound) {
		t.Fatalf("unbound nudge=%v", err)
	}
	first := &nudgeTargetStub{}
	if err := relay.Bind(first); err != nil {
		t.Fatal(err)
	}
	if err := relay.Bind(&nudgeTargetStub{}); !errors.Is(err, ErrNudgeTargetBound) {
		t.Fatalf("relay rebound: %v", err)
	}
	at := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	if err := relay.Nudge(t.Context(), "workspace-1", 8, at); err != nil {
		t.Fatal(err)
	}
	if len(first.calls) != 1 || first.calls[0].workspaceID != "workspace-1" || first.calls[0].epoch != 8 {
		t.Fatalf("nudge calls=%#v", first.calls)
	}
}

type runnableStub struct {
	mu      sync.Mutex
	calls   int
	started chan struct{}
	stopped chan struct{}
	err     error
}

func (r *runnableStub) Run(ctx context.Context) error {
	r.mu.Lock()
	r.calls++
	close(r.started)
	err := r.err
	r.mu.Unlock()
	if err != nil {
		return err
	}
	<-ctx.Done()
	close(r.stopped)
	return nil
}

type nudgeTargetStub struct {
	calls []generationNudgeCallForDaemon
}

type generationNudgeCallForDaemon struct {
	workspaceID string
	epoch       int64
	at          time.Time
}

func (s *nudgeTargetStub) Nudge(_ context.Context, workspaceID string, epoch int64, at time.Time) error {
	s.calls = append(s.calls, generationNudgeCallForDaemon{workspaceID: workspaceID, epoch: epoch, at: at})
	return nil
}

type productionRuntimeStub struct{ fencer *countingGenerationFencer }

func (s productionRuntimeStub) IsStableV2Workspace(_ context.Context, workspaceID string, epoch int64) (bool, error) {
	return workspaceID == "daemon-v2" && epoch == 4, nil
}

func (s productionRuntimeStub) ResolveGenerationRuntime(_ context.Context, workspaceID string) (taskdomain.GenerationRuntimeSnapshot, error) {
	return taskdomain.GenerationRuntimeSnapshot{WorkspaceID: workspaceID, Epoch: 4, Fencer: s.fencer}, nil
}
