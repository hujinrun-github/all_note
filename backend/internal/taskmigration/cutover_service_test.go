package taskmigration

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCutoverServiceExecutesReadyWorkspaceOnce(t *testing.T) {
	t.Parallel()
	state := cutoverReadyState()
	store := newFakeCutoverStateStore(state)
	service := mustCutoverService(t, store, defaultFinalCutoverObserver(), &fakeMobileCutoverPreflight{}, &fakeOldWriterCounter{}, &fakeV2Capability{supported: true}, nil)

	result, err := service.Execute(t.Context(), state.WorkspaceID, state.Revision, state.WriteEpoch, state.MigrationID)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Applied || result.AlreadyApplied {
		t.Fatalf("result = %#v", result)
	}
	if result.State.ModelVersion != ModelVersionV2 || result.State.MigrationState != MigrationStateCutover || result.State.Revision != state.Revision+1 {
		t.Fatalf("cutover state = %#v", result.State)
	}
	if got := store.writeCount(); got != 1 {
		t.Fatalf("CAS writes = %d, want 1", got)
	}
}

func TestCutoverServiceReturnsAllGateFailuresInStableOrder(t *testing.T) {
	t.Parallel()
	state := cutoverReadyState()
	observer := defaultFinalCutoverObserver()
	observer.observation.OutboxWatermark = *state.CutoverRevision - 1
	observer.observation.ActiveLegacyTransactions = 2
	observer.observation.PreviousFenceEpoch = state.WriteEpoch
	observer.observation.Reconcile = ReconcilePlan{
		Mismatches:    []ReconcileMismatch{{Code: ReconcileMismatchChecksum}},
		UpsertMissing: []ReconcileMutation{{}},
	}
	observer.observation.PendingMutations = 1
	store := newFakeCutoverStateStore(state)
	service := mustCutoverService(t, store, observer, &fakeMobileCutoverPreflight{err: errors.New("scopes changes,snapshot are open")}, &fakeOldWriterCounter{count: 3}, &fakeV2Capability{}, nil)

	_, err := service.Execute(t.Context(), state.WorkspaceID, state.Revision, state.WriteEpoch, state.MigrationID)
	assertCutoverCodes(t, err, []CutoverGateFailureCode{
		CutoverGateReplayIncomplete,
		CutoverGateActiveLegacyTransactions,
		CutoverGateOldWriterHeartbeat,
		CutoverGateFenceEpochNotAdvanced,
		CutoverGateReconcileMismatch,
		CutoverGatePendingMutation,
		CutoverGateMobileShutdownIncomplete,
		CutoverGateApplicationV2Unsupported,
	})
	if got := store.compareAndSwapCalls(); got != 0 {
		t.Fatalf("CAS calls = %d, want 0", got)
	}
	if strings.Contains(err.Error(), "changes") || strings.Contains(err.Error(), "snapshot") {
		t.Fatalf("gate error leaked mobile details: %v", err)
	}
}

func TestCutoverServiceEachBlockingGateSkipsCAS(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*fakeFinalCutoverObserver, *fakeMobileCutoverPreflight, *fakeOldWriterCounter, *fakeV2Capability)
		code   CutoverGateFailureCode
	}{
		{name: "replay", code: CutoverGateReplayIncomplete, mutate: func(o *fakeFinalCutoverObserver, _ *fakeMobileCutoverPreflight, _ *fakeOldWriterCounter, _ *fakeV2Capability) {
			o.observation.OutboxWatermark--
		}},
		{name: "active transaction", code: CutoverGateActiveLegacyTransactions, mutate: func(o *fakeFinalCutoverObserver, _ *fakeMobileCutoverPreflight, _ *fakeOldWriterCounter, _ *fakeV2Capability) {
			o.observation.ActiveLegacyTransactions = 1
		}},
		{name: "heartbeat", code: CutoverGateOldWriterHeartbeat, mutate: func(_ *fakeFinalCutoverObserver, _ *fakeMobileCutoverPreflight, h *fakeOldWriterCounter, _ *fakeV2Capability) {
			h.count = 1
		}},
		{name: "fence", code: CutoverGateFenceEpochNotAdvanced, mutate: func(o *fakeFinalCutoverObserver, _ *fakeMobileCutoverPreflight, _ *fakeOldWriterCounter, _ *fakeV2Capability) {
			o.observation.PreviousFenceEpoch++
		}},
		{name: "mismatch", code: CutoverGateReconcileMismatch, mutate: func(o *fakeFinalCutoverObserver, _ *fakeMobileCutoverPreflight, _ *fakeOldWriterCounter, _ *fakeV2Capability) {
			o.observation.Reconcile.Ready = false
			o.observation.Reconcile.Mismatches = []ReconcileMismatch{{Code: ReconcileMismatchStatus}}
		}},
		{name: "reconcile mutation", code: CutoverGatePendingMutation, mutate: func(o *fakeFinalCutoverObserver, _ *fakeMobileCutoverPreflight, _ *fakeOldWriterCounter, _ *fakeV2Capability) {
			o.observation.Reconcile.Ready = false
			o.observation.Reconcile.DeleteExtra = []ReconcileMutation{{}}
		}},
		{name: "explicit pending mutation", code: CutoverGatePendingMutation, mutate: func(o *fakeFinalCutoverObserver, _ *fakeMobileCutoverPreflight, _ *fakeOldWriterCounter, _ *fakeV2Capability) {
			o.observation.PendingMutations = 1
		}},
		{name: "mobile", code: CutoverGateMobileShutdownIncomplete, mutate: func(_ *fakeFinalCutoverObserver, m *fakeMobileCutoverPreflight, _ *fakeOldWriterCounter, _ *fakeV2Capability) {
			m.err = errors.New("not closed")
		}},
		{name: "application", code: CutoverGateApplicationV2Unsupported, mutate: func(_ *fakeFinalCutoverObserver, _ *fakeMobileCutoverPreflight, _ *fakeOldWriterCounter, a *fakeV2Capability) {
			a.supported = false
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			state := cutoverReadyState()
			store := newFakeCutoverStateStore(state)
			observer := defaultFinalCutoverObserver()
			mobile := &fakeMobileCutoverPreflight{}
			heartbeat := &fakeOldWriterCounter{}
			app := &fakeV2Capability{supported: true}
			test.mutate(observer, mobile, heartbeat, app)
			service := mustCutoverService(t, store, observer, mobile, heartbeat, app, nil)
			_, err := service.Execute(t.Context(), state.WorkspaceID, state.Revision, state.WriteEpoch, state.MigrationID)
			assertCutoverCodes(t, err, []CutoverGateFailureCode{test.code})
			if store.compareAndSwapCalls() != 0 {
				t.Fatal("blocked cutover attempted CAS")
			}
		})
	}
}

func TestCutoverServiceAlreadyAppliedSameMigrationIsIdempotent(t *testing.T) {
	t.Parallel()
	cutover := cutoverAppliedState(t)
	store := newFakeCutoverStateStore(cutover)
	observer := defaultFinalCutoverObserver()
	service := mustCutoverService(t, store, observer, &fakeMobileCutoverPreflight{}, &fakeOldWriterCounter{}, &fakeV2Capability{supported: true}, nil)

	result, err := service.Execute(t.Context(), cutover.WorkspaceID, cutover.Revision-1, cutover.WriteEpoch, cutover.MigrationID)
	if err != nil || !result.AlreadyApplied || result.Applied {
		t.Fatalf("idempotent result = %#v, err = %v", result, err)
	}
	if observer.calls.Load() != 0 || store.compareAndSwapCalls() != 0 {
		t.Fatalf("idempotent retry invoked gates/CAS: observer=%d cas=%d", observer.calls.Load(), store.compareAndSwapCalls())
	}
}

func TestCutoverServiceDoesNotTreatAnotherMigrationAsIdempotent(t *testing.T) {
	t.Parallel()
	cutover := cutoverAppliedState(t)
	service := mustCutoverService(t, newFakeCutoverStateStore(cutover), defaultFinalCutoverObserver(), &fakeMobileCutoverPreflight{}, &fakeOldWriterCounter{}, &fakeV2Capability{supported: true}, nil)

	_, err := service.Execute(t.Context(), cutover.WorkspaceID, cutover.Revision-1, cutover.WriteEpoch, "another-migration")
	assertCutoverCodes(t, err, []CutoverGateFailureCode{CutoverGateStateNotReady})
}

func TestCutoverServiceRejectsStaleCommandWithoutCAS(t *testing.T) {
	t.Parallel()
	state := cutoverReadyState()
	store := newFakeCutoverStateStore(state)
	service := mustCutoverService(t, store, defaultFinalCutoverObserver(), &fakeMobileCutoverPreflight{}, &fakeOldWriterCounter{}, &fakeV2Capability{supported: true}, nil)

	_, err := service.Execute(t.Context(), state.WorkspaceID, state.Revision-1, state.WriteEpoch, state.MigrationID)
	assertCutoverCodes(t, err, []CutoverGateFailureCode{CutoverGateStateConflict})
	if store.compareAndSwapCalls() != 0 {
		t.Fatal("stale command attempted CAS")
	}
}

func TestCutoverServiceConcurrentCoordinatorsHaveOneCASWinner(t *testing.T) {
	state := cutoverReadyState()
	store := newFakeCutoverStateStore(state)
	service := mustCutoverService(t, store, defaultFinalCutoverObserver(), &fakeMobileCutoverPreflight{}, &fakeOldWriterCounter{}, &fakeV2Capability{supported: true}, nil)

	const coordinators = 24
	start := make(chan struct{})
	results := make(chan CutoverExecutionResult, coordinators)
	errs := make(chan error, coordinators)
	var group sync.WaitGroup
	for range coordinators {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			result, err := service.Execute(context.Background(), state.WorkspaceID, state.Revision, state.WriteEpoch, state.MigrationID)
			results <- result
			errs <- err
		}()
	}
	close(start)
	group.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Execute() error = %v", err)
		}
	}
	applied := 0
	for result := range results {
		if result.Applied {
			applied++
		}
	}
	if applied != 1 || store.writeCount() != 1 {
		t.Fatalf("applied results=%d CAS writes=%d, want one", applied, store.writeCount())
	}
}

func TestCutoverServiceFailureBeforeCASSkipsWriteAndCanRetry(t *testing.T) {
	t.Parallel()
	state := cutoverReadyState()
	store := newFakeCutoverStateStore(state)
	fault := &fakeCutoverFaultInjector{failAt: CutoverFaultAfterChecksBeforeCAS, remaining: 1, err: errors.New("process crashed with secret")}
	service := mustCutoverService(t, store, defaultFinalCutoverObserver(), &fakeMobileCutoverPreflight{}, &fakeOldWriterCounter{}, &fakeV2Capability{supported: true}, fault)

	_, err := service.Execute(t.Context(), state.WorkspaceID, state.Revision, state.WriteEpoch, state.MigrationID)
	assertCutoverCodes(t, err, []CutoverGateFailureCode{CutoverGateInterrupted})
	if store.writeCount() != 0 || strings.Contains(err.Error(), "secret") {
		t.Fatalf("before-CAS failure wrote or leaked details: writes=%d err=%v", store.writeCount(), err)
	}
	result, err := service.Execute(t.Context(), state.WorkspaceID, state.Revision, state.WriteEpoch, state.MigrationID)
	if err != nil || !result.Applied || store.writeCount() != 1 {
		t.Fatalf("retry result=%#v writes=%d err=%v", result, store.writeCount(), err)
	}
}

func TestCutoverServiceFailureAfterCASIsRecoveredByIdempotentRetry(t *testing.T) {
	t.Parallel()
	state := cutoverReadyState()
	store := newFakeCutoverStateStore(state)
	fault := &fakeCutoverFaultInjector{failAt: CutoverFaultAfterCASBeforeResponse, remaining: 1, err: errors.New("connection died")}
	service := mustCutoverService(t, store, defaultFinalCutoverObserver(), &fakeMobileCutoverPreflight{}, &fakeOldWriterCounter{}, &fakeV2Capability{supported: true}, fault)

	_, err := service.Execute(t.Context(), state.WorkspaceID, state.Revision, state.WriteEpoch, state.MigrationID)
	assertCutoverCodes(t, err, []CutoverGateFailureCode{CutoverGateInterrupted})
	if store.writeCount() != 1 {
		t.Fatalf("after-CAS write count = %d", store.writeCount())
	}
	result, err := service.Execute(t.Context(), state.WorkspaceID, state.Revision, state.WriteEpoch, state.MigrationID)
	if err != nil || !result.AlreadyApplied || result.Applied || store.writeCount() != 1 {
		t.Fatalf("retry result=%#v writes=%d err=%v", result, store.writeCount(), err)
	}
}

func TestCutoverServiceCutoverHonorsV2FirstWriteRollbackBoundary(t *testing.T) {
	t.Parallel()
	state := cutoverReadyState()
	service := mustCutoverService(t, newFakeCutoverStateStore(state), defaultFinalCutoverObserver(), &fakeMobileCutoverPreflight{}, &fakeOldWriterCounter{}, &fakeV2Capability{supported: true}, nil)
	result, err := service.Execute(t.Context(), state.WorkspaceID, state.Revision, state.WriteEpoch, state.MigrationID)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	rolledBack, err := Recover(result.State, RecoverCommand{ExpectedRevision: result.State.Revision, ExpectedWriteEpoch: result.State.WriteEpoch})
	if err != nil || rolledBack.ModelVersion != ModelVersionLegacy {
		t.Fatalf("pre-write Recover() state=%#v err=%v", rolledBack, err)
	}
	written, err := MarkV2FirstWrite(result.State, MarkV2FirstWriteCommand{
		ExpectedRevision: result.State.Revision, ExpectedWriteEpoch: result.State.WriteEpoch, WrittenAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("MarkV2FirstWrite() error = %v", err)
	}
	_, err = Recover(written, RecoverCommand{ExpectedRevision: written.Revision, ExpectedWriteEpoch: written.WriteEpoch})
	var transition *StateTransitionError
	if !errors.As(err, &transition) || transition.Code != StateErrorRollbackForbidden {
		t.Fatalf("post-write Recover() error = %v", err)
	}
}

func TestCutoverServiceSanitizesDependencyFailures(t *testing.T) {
	t.Parallel()
	state := cutoverReadyState()
	tests := []struct {
		name string
		deps CutoverServiceDependencies
		code CutoverGateFailureCode
	}{
		{name: "state", code: CutoverGateStateUnavailable, deps: validCutoverDependencies(newFakeCutoverStateStoreWithError(state, errors.New("postgres password")))},
		{name: "observation", code: CutoverGateObservationUnavailable, deps: validCutoverDependenciesWithObserver(newFakeCutoverStateStore(state), &fakeFinalCutoverObserver{err: errors.New("snapshot token")})},
		{name: "heartbeat", code: CutoverGateHeartbeatUnavailable, deps: validCutoverDependenciesWithHeartbeat(newFakeCutoverStateStore(state), &fakeOldWriterCounter{err: errors.New("internal host")})},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service, err := NewCutoverService(test.deps)
			if err != nil {
				t.Fatalf("NewCutoverService() error = %v", err)
			}
			_, err = service.Execute(t.Context(), state.WorkspaceID, state.Revision, state.WriteEpoch, state.MigrationID)
			assertCutoverCodes(t, err, []CutoverGateFailureCode{test.code})
			for _, secret := range []string{"password", "token", "host"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("error leaked %q: %v", secret, err)
				}
			}
		})
	}
}

func TestCutoverServiceCASConflictReloadsAppliedState(t *testing.T) {
	t.Parallel()
	state := cutoverReadyState()
	store := newFakeCutoverStateStore(state)
	store.forceConflictAfterApplying = true
	service := mustCutoverService(t, store, defaultFinalCutoverObserver(), &fakeMobileCutoverPreflight{}, &fakeOldWriterCounter{}, &fakeV2Capability{supported: true}, nil)

	result, err := service.Execute(t.Context(), state.WorkspaceID, state.Revision, state.WriteEpoch, state.MigrationID)
	if err != nil || !result.AlreadyApplied || result.Applied || store.writeCount() != 1 {
		t.Fatalf("conflict recovery result=%#v writes=%d err=%v", result, store.writeCount(), err)
	}
}

func TestCutoverServiceCASConflictWithDifferentStateIsStableConflict(t *testing.T) {
	t.Parallel()
	state := cutoverReadyState()
	store := newFakeCutoverStateStore(state)
	store.forceConflict = true
	service := mustCutoverService(t, store, defaultFinalCutoverObserver(), &fakeMobileCutoverPreflight{}, &fakeOldWriterCounter{}, &fakeV2Capability{supported: true}, nil)

	_, err := service.Execute(t.Context(), state.WorkspaceID, state.Revision, state.WriteEpoch, state.MigrationID)
	assertCutoverCodes(t, err, []CutoverGateFailureCode{CutoverGateStateConflict})
}

func TestNewCutoverServiceRejectsMissingDependencies(t *testing.T) {
	t.Parallel()
	_, err := NewCutoverService(CutoverServiceDependencies{})
	if !errors.Is(err, ErrInvalidCutoverService) {
		t.Fatalf("NewCutoverService() error = %v", err)
	}
}

func TestCutoverServiceRejectsInvalidRequestWithoutDependencies(t *testing.T) {
	t.Parallel()
	state := cutoverReadyState()
	store := newFakeCutoverStateStore(state)
	service := mustCutoverService(t, store, defaultFinalCutoverObserver(), &fakeMobileCutoverPreflight{}, &fakeOldWriterCounter{}, &fakeV2Capability{supported: true}, nil)

	_, err := service.Execute(nil, "", 0, 0, "")
	assertCutoverCodes(t, err, []CutoverGateFailureCode{CutoverGateInvalidRequest})
	if store.loadCalls.Load() != 0 {
		t.Fatal("invalid request loaded state")
	}
}

type fakeCutoverStateStore struct {
	mu                         sync.Mutex
	state                      WorkspaceTaskDomainState
	loadErr                    error
	loadCalls                  atomic.Int32
	casCalls                   int
	writes                     int
	forceConflict              bool
	forceConflictAfterApplying bool
}

func newFakeCutoverStateStore(state WorkspaceTaskDomainState) *fakeCutoverStateStore {
	return &fakeCutoverStateStore{state: state}
}

func newFakeCutoverStateStoreWithError(state WorkspaceTaskDomainState, err error) *fakeCutoverStateStore {
	return &fakeCutoverStateStore{state: state, loadErr: err}
}

func (s *fakeCutoverStateStore) Load(_ context.Context, _ string) (WorkspaceTaskDomainState, error) {
	s.loadCalls.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadErr != nil {
		return WorkspaceTaskDomainState{}, s.loadErr
	}
	return s.state, nil
}

func (s *fakeCutoverStateStore) CompareAndSwap(_ context.Context, expected, next WorkspaceTaskDomainState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.casCalls++
	if s.forceConflictAfterApplying {
		s.forceConflictAfterApplying = false
		s.state = next
		s.writes++
		return ErrStateCASConflict
	}
	if s.forceConflict || !stateStoreStatesEqual(s.state, expected) {
		return ErrStateCASConflict
	}
	s.state = next
	s.writes++
	return nil
}

func (s *fakeCutoverStateStore) writeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writes
}

func (s *fakeCutoverStateStore) compareAndSwapCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.casCalls
}

type fakeFinalCutoverObserver struct {
	observation FinalCutoverObservation
	err         error
	calls       atomic.Int32
}

func defaultFinalCutoverObserver() *fakeFinalCutoverObserver {
	return &fakeFinalCutoverObserver{observation: FinalCutoverObservation{
		OutboxWatermark:          42,
		ActiveLegacyTransactions: 0,
		PreviousFenceEpoch:       8,
		Reconcile:                ReconcilePlan{Ready: true},
	}}
}

func (f *fakeFinalCutoverObserver) ObserveFinalCutover(context.Context, string, string, uint64) (FinalCutoverObservation, error) {
	f.calls.Add(1)
	return f.observation, f.err
}

type fakeMobileCutoverPreflight struct{ err error }

func (f *fakeMobileCutoverPreflight) Preflight() error { return f.err }

type fakeOldWriterCounter struct {
	count int
	err   error
}

func (f *fakeOldWriterCounter) CountOldWriterHeartbeats(context.Context, string) (int, error) {
	return f.count, f.err
}

type fakeV2Capability struct{ supported bool }

func (f *fakeV2Capability) SupportsTaskDomainV2Schema() bool { return f.supported }

type fakeCutoverFaultInjector struct {
	mu        sync.Mutex
	failAt    CutoverFaultPoint
	remaining int
	err       error
}

func (f *fakeCutoverFaultInjector) Inject(_ context.Context, point CutoverFaultPoint) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if point == f.failAt && f.remaining > 0 {
		f.remaining--
		return f.err
	}
	return nil
}

func cutoverReadyState() WorkspaceTaskDomainState {
	revision := uint64(42)
	return WorkspaceTaskDomainState{
		WorkspaceID: "workspace-cutover", ModelVersion: ModelVersionLegacy, MigrationState: MigrationStateReady,
		SourceWatermark: revision, CutoverRevision: &revision, WriteEpoch: 9, AcceptLegacyWrites: false,
		MigrationTimezone: "Asia/Shanghai", MigrationID: "migration-cutover", Revision: 5,
	}
}

func cutoverAppliedState(t *testing.T) WorkspaceTaskDomainState {
	t.Helper()
	state := cutoverReadyState()
	next, err := Cutover(state, CutoverCommand{
		ExpectedRevision: state.Revision, ExpectedWriteEpoch: state.WriteEpoch,
		MigrationID: state.MigrationID, CutoverRevision: *state.CutoverRevision,
	})
	if err != nil {
		t.Fatalf("Cutover() error = %v", err)
	}
	return next
}

func validCutoverDependencies(store CutoverStateStore) CutoverServiceDependencies {
	return CutoverServiceDependencies{
		StateStore: store, Observer: defaultFinalCutoverObserver(), Mobile: &fakeMobileCutoverPreflight{},
		Heartbeats: &fakeOldWriterCounter{}, Application: &fakeV2Capability{supported: true},
	}
}

func validCutoverDependenciesWithObserver(store CutoverStateStore, observer FinalCutoverObserver) CutoverServiceDependencies {
	deps := validCutoverDependencies(store)
	deps.Observer = observer
	return deps
}

func validCutoverDependenciesWithHeartbeat(store CutoverStateStore, heartbeat OldWriterHeartbeatCounter) CutoverServiceDependencies {
	deps := validCutoverDependencies(store)
	deps.Heartbeats = heartbeat
	return deps
}

func mustCutoverService(t *testing.T, store CutoverStateStore, observer FinalCutoverObserver, mobile MobileCutoverPreflight, heartbeat OldWriterHeartbeatCounter, app TaskDomainV2Capability, faults CutoverFaultInjector) *CutoverService {
	t.Helper()
	service, err := NewCutoverService(CutoverServiceDependencies{
		StateStore: store, Observer: observer, Mobile: mobile, Heartbeats: heartbeat, Application: app, Faults: faults,
	})
	if err != nil {
		t.Fatalf("NewCutoverService() error = %v", err)
	}
	return service
}

func assertCutoverCodes(t *testing.T, err error, want []CutoverGateFailureCode) {
	t.Helper()
	var gateErr *CutoverGateError
	if !errors.As(err, &gateErr) {
		t.Fatalf("error = %T(%v), want *CutoverGateError", err, err)
	}
	got := make([]CutoverGateFailureCode, len(gateErr.Failures))
	for i := range gateErr.Failures {
		got[i] = gateErr.Failures[i].Code
	}
	if len(got) != len(want) {
		t.Fatalf("gate codes = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("gate codes = %v, want %v", got, want)
		}
	}
}
