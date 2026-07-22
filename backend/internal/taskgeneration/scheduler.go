package taskgeneration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/generationclaims"
)

var (
	ErrInvalidScheduler       = errors.New("invalid task generation scheduler")
	ErrInvalidStableWorkspace = errors.New("invalid stable v2 workspace snapshot")
)

// StableV2Workspace is a durable eligibility fact, not an inference made from
// a cached request runtime. Implementations must list only workspaces whose
// control runtime is active and whose tenant task-domain state is stably v2
// (idle or cutover) at the same epoch.
type StableV2Workspace struct {
	WorkspaceID string
	Epoch       int64
}

type StableV2WorkspaceSource interface {
	ListStableV2Workspaces(context.Context) ([]StableV2Workspace, error)
}

// DurableJobScheduler is deliberately narrower than the claim repository.
// EnsureScheduled must preserve live leases and retry backoff while atomically
// creating or reusing the unique workspace job.
type DurableJobScheduler interface {
	EnsureScheduled(context.Context, generationclaims.EnqueueRequest) (bool, error)
}

type ReconcileResult struct {
	Eligible  int
	Scheduled int
	Active    int
}

type Scheduler struct {
	workspaces StableV2WorkspaceSource
	jobs       DurableJobScheduler
	interval   time.Duration
	runtime    loopRuntime
}

func NewScheduler(workspaces StableV2WorkspaceSource, jobs DurableJobScheduler, interval time.Duration, options ...Option) (*Scheduler, error) {
	if workspaces == nil || jobs == nil || interval <= 0 {
		return nil, ErrInvalidScheduler
	}
	runtime, err := newLoopRuntime(options)
	if err != nil {
		return nil, err
	}
	return &Scheduler{workspaces: workspaces, jobs: jobs, interval: interval, runtime: runtime}, nil
}

func (s *Scheduler) Reconcile(ctx context.Context, at time.Time) (ReconcileResult, error) {
	if s == nil || s.workspaces == nil || s.jobs == nil || at.IsZero() {
		return ReconcileResult{}, ErrInvalidScheduler
	}
	workspaces, err := s.workspaces.ListStableV2Workspaces(ctx)
	sourceErr := err
	if validationErr := validateStableWorkspaces(workspaces); validationErr != nil {
		return ReconcileResult{}, errors.Join(sourceErr, validationErr)
	}
	sort.Slice(workspaces, func(left, right int) bool { return workspaces[left].WorkspaceID < workspaces[right].WorkspaceID })
	result := ReconcileResult{Eligible: len(workspaces)}
	var reconcileErr error
	for _, workspace := range workspaces {
		scheduled, scheduleErr := s.jobs.EnsureScheduled(ctx, generationclaims.EnqueueRequest{
			JobID: generationCycleID(workspace, at), WorkspaceID: workspace.WorkspaceID,
			CreatedEpoch: workspace.Epoch, AvailableAt: at,
		})
		if scheduleErr != nil {
			reconcileErr = errors.Join(reconcileErr, fmt.Errorf("schedule generation for workspace %s: %w", workspace.WorkspaceID, scheduleErr))
			continue
		}
		if scheduled {
			result.Scheduled++
		} else {
			result.Active++
		}
	}
	return result, errors.Join(sourceErr, reconcileErr)
}

// Nudge is a best-effort post-commit fast path used after recurring task or
// schedule commands. It is never called from a tenant write transaction.
// Periodic Reconcile remains the correctness path if this call races a lease
// or the control store is temporarily unavailable.
func (s *Scheduler) Nudge(ctx context.Context, workspaceID string, createdEpoch int64, at time.Time) error {
	workspaceID = strings.TrimSpace(workspaceID)
	if s == nil || s.jobs == nil || workspaceID == "" || createdEpoch < 1 || at.IsZero() {
		return ErrInvalidScheduler
	}
	_, err := s.jobs.EnsureScheduled(ctx, generationclaims.EnqueueRequest{
		JobID:       generationCycleID(StableV2Workspace{WorkspaceID: workspaceID, Epoch: createdEpoch}, at),
		WorkspaceID: workspaceID, CreatedEpoch: createdEpoch, AvailableAt: at,
	})
	return err
}

// Run continuously repairs the durable claim set. Every pass waits for the
// configured interval, including empty and failed passes, so dependency
// failures cannot create a busy loop.
func (s *Scheduler) Run(ctx context.Context) error {
	if s == nil || s.interval <= 0 || s.runtime.waiter == nil || s.runtime.now == nil {
		return ErrInvalidScheduler
	}
	for {
		if ctx.Err() != nil {
			return nil
		}
		_, err := s.Reconcile(ctx, s.runtime.now())
		if err != nil && s.runtime.onError != nil {
			s.runtime.onError(err)
		}
		if err := s.runtime.waiter.Wait(ctx, s.interval); err != nil {
			if ctx.Err() != nil || errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}
	}
}

func validateStableWorkspaces(workspaces []StableV2Workspace) error {
	seen := make(map[string]struct{}, len(workspaces))
	for _, workspace := range workspaces {
		workspaceID := strings.TrimSpace(workspace.WorkspaceID)
		if workspaceID == "" || workspace.Epoch < 1 {
			return ErrInvalidStableWorkspace
		}
		if _, exists := seen[workspaceID]; exists {
			return ErrInvalidStableWorkspace
		}
		seen[workspaceID] = struct{}{}
	}
	return nil
}

func generationCycleID(workspace StableV2Workspace, at time.Time) string {
	sum := sha256.Sum256([]byte(workspace.WorkspaceID + "\x00" + fmt.Sprint(workspace.Epoch) + "\x00" + at.UTC().Format(time.RFC3339Nano)))
	return "generation_" + hex.EncodeToString(sum[:16])
}
