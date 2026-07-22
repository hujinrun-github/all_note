package taskgeneration

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	"github.com/hujinrun/flowspace/internal/generationclaims"
	"github.com/hujinrun/flowspace/internal/taskdomain"
)

var (
	ErrInvalidDaemon      = errors.New("invalid task generation daemon")
	ErrDaemonStarted      = errors.New("task generation daemon already started")
	ErrNudgeTargetUnbound = errors.New("task generation nudge target is not bound")
	ErrNudgeTargetBound   = errors.New("task generation nudge target is already bound")
)

type runnable interface {
	Run(context.Context) error
}

type NudgeTarget interface {
	Nudge(context.Context, string, int64, time.Time) error
}

type Daemon struct {
	mu        sync.Mutex
	scheduler runnable
	worker    runnable
	nudger    NudgeTarget
	cancel    context.CancelFunc
	started   bool
	closed    bool
	wait      sync.WaitGroup
	errors    []error
	onError   func(error)
}

func newDaemon(scheduler runnable, worker runnable, onError func(error)) (*Daemon, error) {
	if scheduler == nil || worker == nil {
		return nil, ErrInvalidDaemon
	}
	nudger, ok := scheduler.(NudgeTarget)
	if !ok {
		// Unit tests may supply lifecycle-only runnables. Production construction
		// always supplies Scheduler and therefore a nudge target.
		nudger = nil
	}
	return &Daemon{scheduler: scheduler, worker: worker, nudger: nudger, onError: onError}, nil
}

func (d *Daemon) Start(parent context.Context) error {
	if d == nil || parent == nil {
		return ErrInvalidDaemon
	}
	d.mu.Lock()
	if d.started || d.closed {
		d.mu.Unlock()
		return ErrDaemonStarted
	}
	ctx, cancel := context.WithCancel(parent)
	d.cancel = cancel
	d.started = true
	d.wait.Add(2)
	d.mu.Unlock()

	d.startLoop(ctx, d.scheduler)
	d.startLoop(ctx, d.worker)
	return nil
}

func (d *Daemon) startLoop(ctx context.Context, loop runnable) {
	go func() {
		defer d.wait.Done()
		err := loop.Run(ctx)
		if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return
		}
		d.mu.Lock()
		d.errors = append(d.errors, err)
		cancel := d.cancel
		onError := d.onError
		d.mu.Unlock()
		if onError != nil {
			onError(err)
		}
		if cancel != nil {
			cancel()
		}
	}()
}

// Close first cancels both loops, waits until no claim or scheduling code is
// still using control/tenant resources, and only then returns to main so it can
// close the tenant resolver and databases.
func (d *Daemon) Close() error {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	if d.closed {
		errs := append([]error(nil), d.errors...)
		d.mu.Unlock()
		return errors.Join(errs...)
	}
	d.closed = true
	cancel := d.cancel
	started := d.started
	d.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if started {
		d.wait.Wait()
	}
	d.mu.Lock()
	errs := append([]error(nil), d.errors...)
	d.mu.Unlock()
	return errors.Join(errs...)
}

func (d *Daemon) Nudge(ctx context.Context, workspaceID string, epoch int64, at time.Time) error {
	if d == nil || d.nudger == nil {
		return ErrNudgeTargetUnbound
	}
	return d.nudger.Nudge(ctx, workspaceID, epoch, at)
}

type ProductionRuntimeResolver interface {
	StableV2Classifier
	taskdomain.GenerationRuntimeResolver
}

type ProductionConfig struct {
	SchedulerInterval  time.Duration
	WorkerPollInterval time.Duration
	BatchSize          int
	OnError            func(error)
}

// NewProductionDaemon is the main-process assembly boundary. Callers retain
// ownership of controlDB, claims, and runtimes; Close only drains goroutines.
func NewProductionDaemon(
	controlDB *sql.DB,
	dialect ControlDialect,
	claims *generationclaims.Repository,
	runtimes ProductionRuntimeResolver,
	config ProductionConfig,
) (*Daemon, error) {
	if controlDB == nil || claims == nil || runtimes == nil || config.SchedulerInterval <= 0 ||
		config.WorkerPollInterval <= 0 || config.BatchSize < 1 {
		return nil, ErrInvalidDaemon
	}
	active, err := NewSQLActiveWorkspaceSource(controlDB, dialect)
	if err != nil {
		return nil, err
	}
	stable, err := NewDurableStableV2Source(active, runtimes)
	if err != nil {
		return nil, err
	}
	options := make([]Option, 0, 1)
	if config.OnError != nil {
		options = append(options, WithErrorSink(config.OnError))
	}
	scheduler, err := NewScheduler(stable, claims, config.SchedulerInterval, options...)
	if err != nil {
		return nil, err
	}
	worker := taskdomain.NewGenerationWorker(claims, runtimes)
	workerLoop, err := NewWorkerLoop(worker, config.BatchSize, config.WorkerPollInterval, options...)
	if err != nil {
		return nil, err
	}
	return newDaemon(scheduler, workerLoop, config.OnError)
}

type NudgeRelay struct {
	mu     sync.RWMutex
	target NudgeTarget
}

func NewNudgeRelay() *NudgeRelay { return &NudgeRelay{} }

func (r *NudgeRelay) Bind(target NudgeTarget) error {
	if r == nil || target == nil {
		return ErrNudgeTargetUnbound
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.target != nil {
		return ErrNudgeTargetBound
	}
	r.target = target
	return nil
}

func (r *NudgeRelay) Nudge(ctx context.Context, workspaceID string, epoch int64, at time.Time) error {
	if r == nil {
		return ErrNudgeTargetUnbound
	}
	r.mu.RLock()
	target := r.target
	r.mu.RUnlock()
	if target == nil {
		return ErrNudgeTargetUnbound
	}
	return target.Nudge(ctx, workspaceID, epoch, at)
}
