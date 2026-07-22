package taskmigration

import (
	"context"
	"errors"
	"testing"
)

func TestMigrationRecoveryExecutorDispatchesOnlyDurableRecoveryAction(t *testing.T) {
	states := map[MigrationState]WorkspaceTaskDomainState{
		MigrationStateBackfilling: recoveryExecutorState(MigrationStateBackfilling),
		MigrationStateCatchingUp:  recoveryExecutorState(MigrationStateCatchingUp),
		MigrationStateDraining:    recoveryExecutorState(MigrationStateDraining),
		MigrationStateReady:       recoveryExecutorState(MigrationStateReady),
	}
	wantAction := map[MigrationState]MigrationRecoveryAction{
		MigrationStateBackfilling: MigrationRecoveryResumeSnapshot,
		MigrationStateCatchingUp:  MigrationRecoveryResumeReplay,
		MigrationStateDraining:    MigrationRecoveryResumeDrain,
		MigrationStateReady:       MigrationRecoveryRetryCutover,
	}

	for phase, state := range states {
		phase, state := phase, state
		t.Run(string(phase), func(t *testing.T) {
			loader := &recoveryExecutorStateLoader{state: state}
			calls := make([]MigrationRecoveryAction, 0, 1)
			executor, err := NewMigrationRecoveryExecutor(MigrationRecoveryExecutorDependencies{
				StateLoader:    loader,
				ResumeSnapshot: recoveryExecutorRecorder(MigrationRecoveryResumeSnapshot, &calls),
				ResumeReplay:   recoveryExecutorRecorder(MigrationRecoveryResumeReplay, &calls),
				ResumeDrain:    recoveryExecutorRecorder(MigrationRecoveryResumeDrain, &calls),
				RetryCutover:   recoveryExecutorRecorder(MigrationRecoveryRetryCutover, &calls),
			})
			if err != nil {
				t.Fatal(err)
			}

			result, err := executor.Execute(context.Background(), "alpha")
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if !result.Attempted || result.Plan.Action != wantAction[phase] {
				t.Fatalf("result=%+v", result)
			}
			if len(calls) != 1 || calls[0] != wantAction[phase] {
				t.Fatalf("calls=%v", calls)
			}
			if loader.workspaceID != "alpha" {
				t.Fatalf("loaded workspace=%q", loader.workspaceID)
			}
		})
	}
}

func TestMigrationRecoveryExecutorDoesNotMutateIdleOrCompletedWorkspace(t *testing.T) {
	for _, state := range []WorkspaceTaskDomainState{
		mustRecoveryExecutorState(t, ModelVersionLegacy, MigrationStateIdle),
		mustRecoveryExecutorState(t, ModelVersionV2, MigrationStateCutover),
	} {
		calls := make([]MigrationRecoveryAction, 0, 1)
		executor := mustRecoveryExecutor(t, &recoveryExecutorStateLoader{state: state}, &calls)
		result, err := executor.Execute(context.Background(), state.WorkspaceID)
		if err != nil {
			t.Fatalf("Execute(%s/%s): %v", state.ModelVersion, state.MigrationState, err)
		}
		if result.Attempted || len(calls) != 0 {
			t.Fatalf("result=%+v calls=%v", result, calls)
		}
	}
}

func TestMigrationRecoveryExecutorReturnsManualPlanWithoutGuessing(t *testing.T) {
	state := recoveryExecutorState(MigrationStateFailed)
	state.LastError = "projection checksum conflict"
	calls := make([]MigrationRecoveryAction, 0, 1)
	executor := mustRecoveryExecutor(t, &recoveryExecutorStateLoader{state: state}, &calls)

	result, err := executor.Execute(context.Background(), "alpha")
	if !errors.Is(err, ErrManualMigrationRecoveryRequired) {
		t.Fatalf("Execute error=%v", err)
	}
	if result.Attempted || result.Plan.Action != MigrationRecoveryManual || result.Plan.LastError != state.LastError {
		t.Fatalf("result=%+v", result)
	}
	if len(calls) != 0 {
		t.Fatalf("manual recovery invoked calls=%v", calls)
	}
}

func TestMigrationRecoveryExecutorPreservesStepFailureAndRecoveryPlan(t *testing.T) {
	state := recoveryExecutorState(MigrationStateCatchingUp)
	want := errors.New("replay interrupted")
	executor, err := NewMigrationRecoveryExecutor(MigrationRecoveryExecutorDependencies{
		StateLoader:    &recoveryExecutorStateLoader{state: state},
		ResumeSnapshot: recoveryExecutorNoop,
		ResumeReplay: func(context.Context, MigrationRecoveryPlan) error {
			return want
		},
		ResumeDrain:  recoveryExecutorNoop,
		RetryCutover: recoveryExecutorNoop,
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := executor.Execute(context.Background(), "alpha")
	if !errors.Is(err, want) {
		t.Fatalf("Execute error=%v", err)
	}
	var executionError *MigrationRecoveryExecutionError
	if !errors.As(err, &executionError) || executionError.Action != MigrationRecoveryResumeReplay {
		t.Fatalf("typed error=%v", err)
	}
	if !result.Attempted || result.Plan.Action != MigrationRecoveryResumeReplay {
		t.Fatalf("result=%+v", result)
	}
}

func TestNewMigrationRecoveryExecutorRejectsIncompleteDependencies(t *testing.T) {
	valid := MigrationRecoveryExecutorDependencies{
		StateLoader:    &recoveryExecutorStateLoader{},
		ResumeSnapshot: recoveryExecutorNoop,
		ResumeReplay:   recoveryExecutorNoop,
		ResumeDrain:    recoveryExecutorNoop,
		RetryCutover:   recoveryExecutorNoop,
	}
	if _, err := NewMigrationRecoveryExecutor(MigrationRecoveryExecutorDependencies{}); !errors.Is(err, ErrInvalidMigrationRecoveryExecutor) {
		t.Fatalf("empty dependencies error=%v", err)
	}
	valid.ResumeDrain = nil
	if _, err := NewMigrationRecoveryExecutor(valid); !errors.Is(err, ErrInvalidMigrationRecoveryExecutor) {
		t.Fatalf("missing step error=%v", err)
	}
}

type recoveryExecutorStateLoader struct {
	state       WorkspaceTaskDomainState
	workspaceID string
	err         error
}

func (l *recoveryExecutorStateLoader) Load(_ context.Context, workspaceID string) (WorkspaceTaskDomainState, error) {
	l.workspaceID = workspaceID
	return l.state, l.err
}

func recoveryExecutorRecorder(action MigrationRecoveryAction, calls *[]MigrationRecoveryAction) MigrationRecoveryStep {
	return func(_ context.Context, plan MigrationRecoveryPlan) error {
		if plan.Action != action {
			return errors.New("unexpected recovery action")
		}
		*calls = append(*calls, action)
		return nil
	}
}

func recoveryExecutorNoop(context.Context, MigrationRecoveryPlan) error { return nil }

func mustRecoveryExecutor(t *testing.T, loader MigrationRecoveryStateLoader, calls *[]MigrationRecoveryAction) *MigrationRecoveryExecutor {
	t.Helper()
	executor, err := NewMigrationRecoveryExecutor(MigrationRecoveryExecutorDependencies{
		StateLoader:    loader,
		ResumeSnapshot: recoveryExecutorRecorder(MigrationRecoveryResumeSnapshot, calls),
		ResumeReplay:   recoveryExecutorRecorder(MigrationRecoveryResumeReplay, calls),
		ResumeDrain:    recoveryExecutorRecorder(MigrationRecoveryResumeDrain, calls),
		RetryCutover:   recoveryExecutorRecorder(MigrationRecoveryRetryCutover, calls),
	})
	if err != nil {
		t.Fatal(err)
	}
	return executor
}

func recoveryExecutorState(phase MigrationState) WorkspaceTaskDomainState {
	state, _ := NewWorkspaceTaskDomainState("alpha", 7)
	state.MigrationState = phase
	state.MigrationID = "migration-alpha"
	state.MigrationTimezone = "Asia/Shanghai"
	state.Revision = 11
	state.SourceWatermark = 23
	switch phase {
	case MigrationStateDraining, MigrationStateReady:
		cutover := uint64(29)
		state.CutoverRevision = &cutover
		if phase == MigrationStateReady {
			state.SourceWatermark = cutover
		}
		state.WriteEpoch = 8
		state.AcceptLegacyWrites = false
	case MigrationStateFailed:
		state.LastError = "migration failed"
	}
	return state
}

func mustRecoveryExecutorState(t *testing.T, model ModelVersion, phase MigrationState) WorkspaceTaskDomainState {
	t.Helper()
	var state WorkspaceTaskDomainState
	var err error
	if model == ModelVersionV2 {
		state, err = NewFreshV2WorkspaceTaskDomainState("alpha", 7)
		state.MigrationState = phase
		if phase == MigrationStateCutover {
			cutover := uint64(0)
			state.CutoverRevision = &cutover
			state.MigrationID = "migration-alpha"
		}
	} else {
		state, err = NewWorkspaceTaskDomainState("alpha", 7)
		state.MigrationState = phase
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Validate(); err != nil {
		t.Fatalf("fixture state: %v", err)
	}
	return state
}
