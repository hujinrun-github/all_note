package taskmigration

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestDomainStateStartsLegacyIdleAndValid(t *testing.T) {
	t.Parallel()

	state, err := NewWorkspaceTaskDomainState("workspace-1", 7)
	if err != nil {
		t.Fatalf("NewWorkspaceTaskDomainState() error = %v", err)
	}
	if state.ModelVersion != ModelVersionLegacy || state.MigrationState != MigrationStateIdle {
		t.Fatalf("initial version/state = (%q, %q)", state.ModelVersion, state.MigrationState)
	}
	if !state.AcceptLegacyWrites || state.Revision != 1 || state.WriteEpoch != 7 {
		t.Fatalf("initial fence = %#v", state)
	}
	if err := state.Validate(); err != nil {
		t.Fatalf("initial state validation = %v", err)
	}
}

func TestDomainStateStartsFreshV2IdleAndValid(t *testing.T) {
	t.Parallel()

	state, err := NewFreshV2WorkspaceTaskDomainState("workspace-2", 11)
	if err != nil {
		t.Fatalf("NewFreshV2WorkspaceTaskDomainState() error = %v", err)
	}
	if state.ModelVersion != ModelVersionV2 || state.MigrationState != MigrationStateIdle {
		t.Fatalf("initial version/state = (%q, %q)", state.ModelVersion, state.MigrationState)
	}
	if state.AcceptLegacyWrites || state.Revision != 1 || state.WriteEpoch != 11 {
		t.Fatalf("initial fence = %#v", state)
	}
	if err := state.Validate(); err != nil {
		t.Fatalf("initial state validation = %v", err)
	}
}

func TestDomainStateMarksFirstV2WriteOnceForFreshAndMigratedWorkspaces(t *testing.T) {
	t.Parallel()

	firstWrite := time.Date(2026, 7, 22, 10, 15, 0, 0, time.UTC)
	for _, initial := range []WorkspaceTaskDomainState{
		newFreshV2DomainState(t),
		mustCutover(t),
	} {
		initial := initial
		t.Run(string(initial.MigrationState), func(t *testing.T) {
			next, err := MarkV2FirstWrite(initial, MarkV2FirstWriteCommand{
				ExpectedRevision:   initial.Revision,
				ExpectedWriteEpoch: initial.WriteEpoch,
				WrittenAt:          firstWrite,
			})
			if err != nil {
				t.Fatalf("MarkV2FirstWrite() error = %v", err)
			}
			if next.V2FirstWriteAt == nil || !next.V2FirstWriteAt.Equal(firstWrite) {
				t.Fatalf("V2FirstWriteAt = %v, want %v", next.V2FirstWriteAt, firstWrite)
			}
			if next.Revision != initial.Revision+1 {
				t.Fatalf("revision = %d, want %d", next.Revision, initial.Revision+1)
			}
			if err := next.Validate(); err != nil {
				t.Fatalf("marked state validation = %v", err)
			}

			idempotent, err := MarkV2FirstWrite(next, MarkV2FirstWriteCommand{
				ExpectedRevision:   next.Revision,
				ExpectedWriteEpoch: next.WriteEpoch,
				WrittenAt:          firstWrite.Add(time.Hour),
			})
			if err != nil {
				t.Fatalf("idempotent MarkV2FirstWrite() error = %v", err)
			}
			if !reflect.DeepEqual(idempotent, next) {
				t.Fatalf("idempotent mark changed state: before=%#v after=%#v", next, idempotent)
			}
		})
	}
}

func TestDomainStateRejectsFirstV2WriteForLegacyOrInvalidTimestamp(t *testing.T) {
	t.Parallel()

	legacy := newDomainState(t)
	_, err := MarkV2FirstWrite(legacy, MarkV2FirstWriteCommand{
		ExpectedRevision:   legacy.Revision,
		ExpectedWriteEpoch: legacy.WriteEpoch,
		WrittenAt:          time.Now().UTC(),
	})
	assertStateError(t, err, StateErrorInvalidTransition)

	fresh := newFreshV2DomainState(t)
	_, err = MarkV2FirstWrite(fresh, MarkV2FirstWriteCommand{
		ExpectedRevision:   fresh.Revision,
		ExpectedWriteEpoch: fresh.WriteEpoch,
	})
	assertStateError(t, err, StateErrorInvalidArgument)
}

func TestDomainStateHappyPathRequiresDrainBeforeCASCutover(t *testing.T) {
	t.Parallel()

	state := newDomainState(t)
	state = mustStartBackfill(t, state)
	state = mustBeginCatchingUp(t, state, 40)

	draining, err := BeginDrain(state, BeginDrainCommand{
		ExpectedRevision:   state.Revision,
		ExpectedWriteEpoch: state.WriteEpoch,
		CutoverRevision:    45,
	})
	if err != nil {
		t.Fatalf("BeginDrain() error = %v", err)
	}
	if draining.MigrationState != MigrationStateDraining || draining.AcceptLegacyWrites {
		t.Fatalf("draining state = %#v", draining)
	}
	if draining.WriteEpoch != state.WriteEpoch+1 || valueOfRevision(draining.CutoverRevision) != 45 {
		t.Fatalf("drain fence/cutover = %#v", draining)
	}

	ready, err := MarkReady(draining, MarkReadyCommand{
		ExpectedRevision:   draining.Revision,
		ExpectedWriteEpoch: draining.WriteEpoch,
		SourceWatermark:    45,
	})
	if err != nil {
		t.Fatalf("MarkReady() error = %v", err)
	}
	cutover, err := Cutover(ready, CutoverCommand{
		ExpectedRevision:   ready.Revision,
		ExpectedWriteEpoch: ready.WriteEpoch,
		MigrationID:        ready.MigrationID,
		CutoverRevision:    45,
	})
	if err != nil {
		t.Fatalf("Cutover() error = %v", err)
	}
	if cutover.ModelVersion != ModelVersionV2 || cutover.MigrationState != MigrationStateCutover {
		t.Fatalf("cutover state = %#v", cutover)
	}
	if cutover.AcceptLegacyWrites || cutover.Revision != ready.Revision+1 {
		t.Fatalf("cutover fence/revision = %#v", cutover)
	}
}

func TestDomainStateRejectsStaleRevisionAndEpoch(t *testing.T) {
	t.Parallel()

	state := newDomainState(t)
	before := state
	_, err := StartBackfill(state, StartBackfillCommand{
		ExpectedRevision:   state.Revision - 1,
		ExpectedWriteEpoch: state.WriteEpoch,
		MigrationID:        "migration-1",
		MigrationTimezone:  "Asia/Shanghai",
	})
	assertStateError(t, err, StateErrorStaleRevision)
	if !reflect.DeepEqual(state, before) {
		t.Fatalf("stale revision mutated input: before=%#v after=%#v", before, state)
	}

	_, err = StartBackfill(state, StartBackfillCommand{
		ExpectedRevision:   state.Revision,
		ExpectedWriteEpoch: state.WriteEpoch - 1,
		MigrationID:        "migration-1",
		MigrationTimezone:  "Asia/Shanghai",
	})
	assertStateError(t, err, StateErrorStaleEpoch)
}

func TestDomainStateRejectsWatermarkRegression(t *testing.T) {
	t.Parallel()

	state := mustBeginCatchingUp(t, mustStartBackfill(t, newDomainState(t)), 20)
	before := state
	_, err := BeginCatchingUp(state, BeginCatchingUpCommand{
		ExpectedRevision:   state.Revision,
		ExpectedWriteEpoch: state.WriteEpoch,
		SourceWatermark:    19,
	})
	assertStateError(t, err, StateErrorWatermarkRegression)
	if !reflect.DeepEqual(state, before) {
		t.Fatalf("watermark regression mutated input: before=%#v after=%#v", before, state)
	}
}

func TestDomainStateRejectsDrainBeforeCatchUpAndCutoverBehindWatermark(t *testing.T) {
	t.Parallel()

	backfilling := mustStartBackfill(t, newDomainState(t))
	_, err := BeginDrain(backfilling, BeginDrainCommand{
		ExpectedRevision:   backfilling.Revision,
		ExpectedWriteEpoch: backfilling.WriteEpoch,
		CutoverRevision:    10,
	})
	assertStateError(t, err, StateErrorInvalidTransition)

	catchingUp := mustBeginCatchingUp(t, backfilling, 20)
	_, err = BeginDrain(catchingUp, BeginDrainCommand{
		ExpectedRevision:   catchingUp.Revision,
		ExpectedWriteEpoch: catchingUp.WriteEpoch,
		CutoverRevision:    19,
	})
	assertStateError(t, err, StateErrorWatermarkRegression)
}

func TestDomainStateRejectsReadyUntilDrainWatermarkIsComplete(t *testing.T) {
	t.Parallel()

	state := mustBeginCatchingUp(t, mustStartBackfill(t, newDomainState(t)), 40)
	draining, err := BeginDrain(state, BeginDrainCommand{
		ExpectedRevision:   state.Revision,
		ExpectedWriteEpoch: state.WriteEpoch,
		CutoverRevision:    45,
	})
	if err != nil {
		t.Fatalf("BeginDrain() error = %v", err)
	}
	before := draining
	_, err = MarkReady(draining, MarkReadyCommand{
		ExpectedRevision:   draining.Revision,
		ExpectedWriteEpoch: draining.WriteEpoch,
		SourceWatermark:    44,
	})
	assertStateError(t, err, StateErrorDrainIncomplete)
	if !reflect.DeepEqual(draining, before) {
		t.Fatalf("incomplete drain mutated input: before=%#v after=%#v", before, draining)
	}
}

func TestDomainStateRejectsCutoverUntilReadyAndOnCASMismatch(t *testing.T) {
	t.Parallel()

	draining := mustDraining(t)
	_, err := Cutover(draining, CutoverCommand{
		ExpectedRevision:   draining.Revision,
		ExpectedWriteEpoch: draining.WriteEpoch,
		MigrationID:        draining.MigrationID,
		CutoverRevision:    valueOfRevision(draining.CutoverRevision),
	})
	assertStateError(t, err, StateErrorNotReady)

	ready, err := MarkReady(draining, MarkReadyCommand{
		ExpectedRevision:   draining.Revision,
		ExpectedWriteEpoch: draining.WriteEpoch,
		SourceWatermark:    valueOfRevision(draining.CutoverRevision),
	})
	if err != nil {
		t.Fatalf("MarkReady() error = %v", err)
	}
	for _, command := range []CutoverCommand{
		{ExpectedRevision: ready.Revision, ExpectedWriteEpoch: ready.WriteEpoch, MigrationID: "other", CutoverRevision: valueOfRevision(ready.CutoverRevision)},
		{ExpectedRevision: ready.Revision, ExpectedWriteEpoch: ready.WriteEpoch, MigrationID: ready.MigrationID, CutoverRevision: valueOfRevision(ready.CutoverRevision) + 1},
	} {
		_, err = Cutover(ready, command)
		assertStateError(t, err, StateErrorCASMismatch)
	}
}

func TestDomainStateFailReopensLegacyFenceAndRecoverRestartsIdle(t *testing.T) {
	t.Parallel()

	draining := mustDraining(t)
	failed, err := Fail(draining, FailCommand{
		ExpectedRevision:   draining.Revision,
		ExpectedWriteEpoch: draining.WriteEpoch,
		Cause:              "reconcile mismatch",
	})
	if err != nil {
		t.Fatalf("Fail() error = %v", err)
	}
	if failed.MigrationState != MigrationStateFailed || !failed.AcceptLegacyWrites {
		t.Fatalf("failed state = %#v", failed)
	}
	if failed.WriteEpoch != draining.WriteEpoch+1 || failed.LastError != "reconcile mismatch" {
		t.Fatalf("failed fence/error = %#v", failed)
	}
	if failed.MigrationID != draining.MigrationID {
		t.Fatalf("Fail() lost migration audit identity: %#v", failed)
	}

	recovered, err := Recover(failed, RecoverCommand{
		ExpectedRevision:   failed.Revision,
		ExpectedWriteEpoch: failed.WriteEpoch,
	})
	if err != nil {
		t.Fatalf("Recover() error = %v", err)
	}
	if recovered.ModelVersion != ModelVersionLegacy || recovered.MigrationState != MigrationStateIdle || !recovered.AcceptLegacyWrites {
		t.Fatalf("recovered state = %#v", recovered)
	}
	if recovered.LastError != "" || recovered.SourceWatermark != 0 || recovered.CutoverRevision != nil {
		t.Fatalf("recovered progress = %#v", recovered)
	}
}

func TestDomainStateAllowsRollbackBeforeFirstV2WriteButFencesOldEpoch(t *testing.T) {
	t.Parallel()

	cutover := mustCutover(t)
	recovered, err := Recover(cutover, RecoverCommand{
		ExpectedRevision:   cutover.Revision,
		ExpectedWriteEpoch: cutover.WriteEpoch,
	})
	if err != nil {
		t.Fatalf("Recover(cutover) error = %v", err)
	}
	if recovered.ModelVersion != ModelVersionLegacy || recovered.MigrationState != MigrationStateIdle || !recovered.AcceptLegacyWrites {
		t.Fatalf("rollback state = %#v", recovered)
	}
	if recovered.WriteEpoch != cutover.WriteEpoch+1 {
		t.Fatalf("rollback epoch = %d, want %d", recovered.WriteEpoch, cutover.WriteEpoch+1)
	}
}

func TestDomainStateRejectsRollbackAfterFirstV2WriteWithoutMutatingInput(t *testing.T) {
	t.Parallel()

	state := mustCutover(t)
	firstWrite := time.Date(2026, 7, 22, 9, 30, 0, 0, time.UTC)
	state.V2FirstWriteAt = &firstWrite
	before := state
	_, err := Recover(state, RecoverCommand{
		ExpectedRevision:   state.Revision,
		ExpectedWriteEpoch: state.WriteEpoch,
	})
	assertStateError(t, err, StateErrorRollbackForbidden)
	if !reflect.DeepEqual(state, before) {
		t.Fatalf("forbidden rollback mutated input: before=%#v after=%#v", before, state)
	}
}

func newDomainState(t *testing.T) WorkspaceTaskDomainState {
	t.Helper()
	state, err := NewWorkspaceTaskDomainState("workspace-1", 5)
	if err != nil {
		t.Fatalf("NewWorkspaceTaskDomainState() error = %v", err)
	}
	return state
}

func newFreshV2DomainState(t *testing.T) WorkspaceTaskDomainState {
	t.Helper()
	state, err := NewFreshV2WorkspaceTaskDomainState("workspace-fresh-v2", 9)
	if err != nil {
		t.Fatalf("NewFreshV2WorkspaceTaskDomainState() error = %v", err)
	}
	return state
}

func mustStartBackfill(t *testing.T, state WorkspaceTaskDomainState) WorkspaceTaskDomainState {
	t.Helper()
	next, err := StartBackfill(state, StartBackfillCommand{
		ExpectedRevision:   state.Revision,
		ExpectedWriteEpoch: state.WriteEpoch,
		MigrationID:        "migration-1",
		MigrationTimezone:  "Asia/Shanghai",
	})
	if err != nil {
		t.Fatalf("StartBackfill() error = %v", err)
	}
	return next
}

func mustBeginCatchingUp(t *testing.T, state WorkspaceTaskDomainState, watermark uint64) WorkspaceTaskDomainState {
	t.Helper()
	next, err := BeginCatchingUp(state, BeginCatchingUpCommand{
		ExpectedRevision:   state.Revision,
		ExpectedWriteEpoch: state.WriteEpoch,
		SourceWatermark:    watermark,
	})
	if err != nil {
		t.Fatalf("BeginCatchingUp() error = %v", err)
	}
	return next
}

func mustDraining(t *testing.T) WorkspaceTaskDomainState {
	t.Helper()
	state := mustBeginCatchingUp(t, mustStartBackfill(t, newDomainState(t)), 40)
	next, err := BeginDrain(state, BeginDrainCommand{
		ExpectedRevision:   state.Revision,
		ExpectedWriteEpoch: state.WriteEpoch,
		CutoverRevision:    45,
	})
	if err != nil {
		t.Fatalf("BeginDrain() error = %v", err)
	}
	return next
}

func mustCutover(t *testing.T) WorkspaceTaskDomainState {
	t.Helper()
	draining := mustDraining(t)
	ready, err := MarkReady(draining, MarkReadyCommand{
		ExpectedRevision:   draining.Revision,
		ExpectedWriteEpoch: draining.WriteEpoch,
		SourceWatermark:    valueOfRevision(draining.CutoverRevision),
	})
	if err != nil {
		t.Fatalf("MarkReady() error = %v", err)
	}
	next, err := Cutover(ready, CutoverCommand{
		ExpectedRevision:   ready.Revision,
		ExpectedWriteEpoch: ready.WriteEpoch,
		MigrationID:        ready.MigrationID,
		CutoverRevision:    valueOfRevision(ready.CutoverRevision),
	})
	if err != nil {
		t.Fatalf("Cutover() error = %v", err)
	}
	return next
}

func assertStateError(t *testing.T, err error, code StateErrorCode) {
	t.Helper()
	var stateErr *StateTransitionError
	if !errors.As(err, &stateErr) {
		t.Fatalf("error = %T(%v), want *StateTransitionError", err, err)
	}
	if stateErr.Code != code {
		t.Fatalf("error code = %q, want %q (error=%v)", stateErr.Code, code, err)
	}
}

func valueOfRevision(value *uint64) uint64 {
	if value == nil {
		return 0
	}
	return *value
}
