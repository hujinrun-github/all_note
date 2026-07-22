package taskmigration

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrInvalidMigrationRecoveryExecutor = errors.New("invalid task-domain migration recovery executor")
	ErrManualMigrationRecoveryRequired  = errors.New("manual task-domain migration recovery required")
)

// MigrationRecoveryStateLoader is intentionally the read-only subset of
// StateStore. Recovery chooses a phase from durable state before invoking any
// idempotent migration primitive.
type MigrationRecoveryStateLoader interface {
	Load(context.Context, string) (WorkspaceTaskDomainState, error)
}

// MigrationRecoveryStep resumes one durable phase. Implementations must bind
// the plan's revision, epoch, migration id, and watermarks to the underlying
// store call; they must not reload a different workspace implicitly.
type MigrationRecoveryStep func(context.Context, MigrationRecoveryPlan) error

type MigrationRecoveryExecutorDependencies struct {
	StateLoader    MigrationRecoveryStateLoader
	ResumeSnapshot MigrationRecoveryStep
	ResumeReplay   MigrationRecoveryStep
	ResumeDrain    MigrationRecoveryStep
	RetryCutover   MigrationRecoveryStep
}

type MigrationRecoveryExecutor struct {
	stateLoader    MigrationRecoveryStateLoader
	resumeSnapshot MigrationRecoveryStep
	resumeReplay   MigrationRecoveryStep
	resumeDrain    MigrationRecoveryStep
	retryCutover   MigrationRecoveryStep
}

func NewMigrationRecoveryExecutor(dependencies MigrationRecoveryExecutorDependencies) (*MigrationRecoveryExecutor, error) {
	if dependencies.StateLoader == nil || dependencies.ResumeSnapshot == nil || dependencies.ResumeReplay == nil ||
		dependencies.ResumeDrain == nil || dependencies.RetryCutover == nil {
		return nil, ErrInvalidMigrationRecoveryExecutor
	}
	return &MigrationRecoveryExecutor{
		stateLoader: dependencies.StateLoader, resumeSnapshot: dependencies.ResumeSnapshot,
		resumeReplay: dependencies.ResumeReplay, resumeDrain: dependencies.ResumeDrain,
		retryCutover: dependencies.RetryCutover,
	}, nil
}

type MigrationRecoveryExecutionResult struct {
	Plan      MigrationRecoveryPlan
	Attempted bool
}

// MigrationRecoveryExecutionError retains the durable action that failed so
// operators can distinguish a replay retry from a cutover retry without
// parsing provider error text.
type MigrationRecoveryExecutionError struct {
	Action MigrationRecoveryAction
	Cause  error
}

func (e *MigrationRecoveryExecutionError) Error() string {
	return fmt.Sprintf("execute task-domain migration recovery action %s: %v", e.Action, e.Cause)
}

func (e *MigrationRecoveryExecutionError) Unwrap() error { return e.Cause }

// Execute dispatches at most one recovery phase. Phase implementations may
// drain all work belonging to that phase, but this method deliberately reloads
// state on its next invocation instead of chaining across durable boundaries.
// That property makes crash recovery and operator-visible checkpoints the same
// path used during normal execution.
func (e *MigrationRecoveryExecutor) Execute(ctx context.Context, workspaceID string) (MigrationRecoveryExecutionResult, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if e == nil || ctx == nil || workspaceID == "" {
		return MigrationRecoveryExecutionResult{}, ErrInvalidMigrationRecoveryExecutor
	}
	state, err := e.stateLoader.Load(ctx, workspaceID)
	if err != nil {
		return MigrationRecoveryExecutionResult{}, fmt.Errorf("load task-domain migration recovery state: %w", err)
	}
	if state.WorkspaceID != workspaceID {
		return MigrationRecoveryExecutionResult{}, fmt.Errorf("%w: state workspace %q does not match %q", ErrInvalidMigrationRecoveryExecutor, state.WorkspaceID, workspaceID)
	}
	plan, err := PlanMigrationRecovery(state)
	if err != nil {
		return MigrationRecoveryExecutionResult{}, err
	}
	result := MigrationRecoveryExecutionResult{Plan: plan}

	var step MigrationRecoveryStep
	switch plan.Action {
	case MigrationRecoveryNone, MigrationRecoveryComplete:
		return result, nil
	case MigrationRecoveryManual:
		return result, fmt.Errorf("%w: workspace=%s migration=%s", ErrManualMigrationRecoveryRequired, plan.WorkspaceID, plan.MigrationID)
	case MigrationRecoveryResumeSnapshot:
		step = e.resumeSnapshot
	case MigrationRecoveryResumeReplay:
		step = e.resumeReplay
	case MigrationRecoveryResumeDrain:
		step = e.resumeDrain
	case MigrationRecoveryRetryCutover:
		step = e.retryCutover
	default:
		return result, fmt.Errorf("%w: unsupported action %q", ErrInvalidMigrationRecoveryExecutor, plan.Action)
	}

	result.Attempted = true
	if err := step(ctx, plan); err != nil {
		return result, &MigrationRecoveryExecutionError{Action: plan.Action, Cause: err}
	}
	return result, nil
}
