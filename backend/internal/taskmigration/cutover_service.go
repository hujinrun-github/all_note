package taskmigration

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
)

var (
	ErrInvalidCutoverService = errors.New("invalid task-domain cutover service")
	ErrCutoverGateClosed     = errors.New("task-domain cutover gate is closed")
)

// CutoverStateStore is the narrow part of StateStore needed by the
// coordinator. *StateStore implements it; tests and future control-plane
// adapters do not need a database merely to exercise orchestration semantics.
type CutoverStateStore interface {
	Load(context.Context, string) (WorkspaceTaskDomainState, error)
	CompareAndSwap(context.Context, WorkspaceTaskDomainState, WorkspaceTaskDomainState) error
}

// FinalCutoverObservation is captured after the legacy write fence has been
// closed. Reconcile is the final bidirectional source/v2 comparison, not an
// earlier backfill checkpoint.
type FinalCutoverObservation struct {
	OutboxWatermark          uint64
	ActiveLegacyTransactions int
	PreviousFenceEpoch       uint64
	Reconcile                ReconcilePlan
	PendingMutations         int
}

type FinalCutoverObserver interface {
	ObserveFinalCutover(context.Context, string, string, uint64) (FinalCutoverObservation, error)
}

// MobileCutoverPreflight intentionally matches mobilecontract's shutdown
// model without importing it. The concrete shutdown object is workspace-bound
// and proves that mutation, changes, snapshot, and watch are all closed.
type MobileCutoverPreflight interface {
	Preflight() error
}

type OldWriterHeartbeatCounter interface {
	CountOldWriterHeartbeats(context.Context, string) (int, error)
}

// TaskDomainV2Capability is supplied by the running application artifact.
// Rollback deployments must keep returning true once a workspace serves v2.
type TaskDomainV2Capability interface {
	SupportsTaskDomainV2Schema() bool
}

type CutoverFaultPoint string

const (
	CutoverFaultAfterChecksBeforeCAS   CutoverFaultPoint = "after_checks_before_cas"
	CutoverFaultAfterCASBeforeResponse CutoverFaultPoint = "after_cas_before_response"
)

type CutoverFaultInjector interface {
	Inject(context.Context, CutoverFaultPoint) error
}

type CutoverServiceDependencies struct {
	StateStore  CutoverStateStore
	Observer    FinalCutoverObserver
	Mobile      MobileCutoverPreflight
	Heartbeats  OldWriterHeartbeatCounter
	Application TaskDomainV2Capability
	Faults      CutoverFaultInjector
}

type CutoverService struct {
	stateStore  CutoverStateStore
	observer    FinalCutoverObserver
	mobile      MobileCutoverPreflight
	heartbeats  OldWriterHeartbeatCounter
	application TaskDomainV2Capability
	faults      CutoverFaultInjector
}

func NewCutoverService(dependencies CutoverServiceDependencies) (*CutoverService, error) {
	if dependencies.StateStore == nil || dependencies.Observer == nil || dependencies.Mobile == nil ||
		dependencies.Heartbeats == nil || dependencies.Application == nil {
		return nil, ErrInvalidCutoverService
	}
	return &CutoverService{
		stateStore: dependencies.StateStore, observer: dependencies.Observer,
		mobile: dependencies.Mobile, heartbeats: dependencies.Heartbeats,
		application: dependencies.Application, faults: dependencies.Faults,
	}, nil
}

type CutoverGateFailureCode string

const (
	CutoverGateInvalidRequest           CutoverGateFailureCode = "invalid_request"
	CutoverGateStateUnavailable         CutoverGateFailureCode = "state_unavailable"
	CutoverGateStateNotReady            CutoverGateFailureCode = "state_not_ready"
	CutoverGateStateConflict            CutoverGateFailureCode = "state_conflict"
	CutoverGateObservationUnavailable   CutoverGateFailureCode = "observation_unavailable"
	CutoverGateHeartbeatUnavailable     CutoverGateFailureCode = "heartbeat_unavailable"
	CutoverGateReplayIncomplete         CutoverGateFailureCode = "replay_incomplete"
	CutoverGateActiveLegacyTransactions CutoverGateFailureCode = "active_legacy_transactions"
	CutoverGateOldWriterHeartbeat       CutoverGateFailureCode = "old_writer_heartbeat"
	CutoverGateLegacyWritesEnabled      CutoverGateFailureCode = "legacy_writes_enabled"
	CutoverGateFenceEpochNotAdvanced    CutoverGateFailureCode = "fence_epoch_not_advanced"
	CutoverGateReconcileMismatch        CutoverGateFailureCode = "reconcile_mismatch"
	CutoverGatePendingMutation          CutoverGateFailureCode = "pending_mutation"
	CutoverGateMobileShutdownIncomplete CutoverGateFailureCode = "mobile_shutdown_incomplete"
	CutoverGateApplicationV2Unsupported CutoverGateFailureCode = "application_v2_unsupported"
	CutoverGateInterrupted              CutoverGateFailureCode = "interrupted"
)

// CutoverGateFailure deliberately contains only a stable code. Counts,
// database errors, mobile scope names, and endpoint details remain internal.
type CutoverGateFailure struct {
	Code CutoverGateFailureCode
}

type CutoverGateError struct {
	Failures []CutoverGateFailure
}

func (e *CutoverGateError) Error() string {
	if e == nil || len(e.Failures) == 0 {
		return ErrCutoverGateClosed.Error()
	}
	codes := make([]string, len(e.Failures))
	for index := range e.Failures {
		codes[index] = string(e.Failures[index].Code)
	}
	return fmt.Sprintf("%s: %s", ErrCutoverGateClosed, strings.Join(codes, ","))
}

func (e *CutoverGateError) Unwrap() error { return ErrCutoverGateClosed }

type CutoverExecutionResult struct {
	State          WorkspaceTaskDomainState
	Applied        bool
	AlreadyApplied bool
}

// Execute performs observation gates before deriving the pure state-machine
// transition and atomically persisting it. It neither installs triggers nor
// registers production routes; callers must opt into the coordinator.
func (s *CutoverService) Execute(
	ctx context.Context,
	workspaceID string,
	expectedRevision uint64,
	expectedEpoch uint64,
	migrationID string,
) (CutoverExecutionResult, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	migrationID = strings.TrimSpace(migrationID)
	if s == nil || ctx == nil || workspaceID == "" || migrationID == "" || expectedRevision == 0 || expectedEpoch == 0 {
		return CutoverExecutionResult{}, cutoverGateError(CutoverGateInvalidRequest)
	}

	state, err := s.stateStore.Load(ctx, workspaceID)
	if err != nil {
		return CutoverExecutionResult{}, cutoverGateError(CutoverGateStateUnavailable)
	}
	if alreadyAppliedCutover(state, workspaceID, migrationID) {
		return CutoverExecutionResult{State: state, AlreadyApplied: true}, nil
	}
	if err := state.Validate(); err != nil || state.WorkspaceID != workspaceID ||
		state.ModelVersion != ModelVersionLegacy || state.MigrationState != MigrationStateReady ||
		state.CutoverRevision == nil || state.MigrationID != migrationID {
		return CutoverExecutionResult{}, cutoverGateError(CutoverGateStateNotReady)
	}
	if state.Revision != expectedRevision || state.WriteEpoch != expectedEpoch {
		return CutoverExecutionResult{}, cutoverGateError(CutoverGateStateConflict)
	}

	observation, err := s.observer.ObserveFinalCutover(ctx, workspaceID, migrationID, *state.CutoverRevision)
	if err != nil || !cutoverObservationFitsDrain(observation, state) {
		return CutoverExecutionResult{}, cutoverGateError(CutoverGateObservationUnavailable)
	}
	heartbeatCount, err := s.heartbeats.CountOldWriterHeartbeats(ctx, workspaceID)
	if err != nil {
		return CutoverExecutionResult{}, cutoverGateError(CutoverGateHeartbeatUnavailable)
	}

	failures := evaluateCutoverGates(state, observation, heartbeatCount)
	if s.mobile.Preflight() != nil {
		failures = append(failures, CutoverGateFailure{Code: CutoverGateMobileShutdownIncomplete})
	}
	if !s.application.SupportsTaskDomainV2Schema() {
		failures = append(failures, CutoverGateFailure{Code: CutoverGateApplicationV2Unsupported})
	}
	if len(failures) != 0 {
		return CutoverExecutionResult{}, &CutoverGateError{Failures: failures}
	}

	next, err := Cutover(state, CutoverCommand{
		ExpectedRevision: expectedRevision, ExpectedWriteEpoch: expectedEpoch,
		MigrationID: migrationID, CutoverRevision: *state.CutoverRevision,
	})
	if err != nil {
		return CutoverExecutionResult{}, cutoverGateError(CutoverGateStateConflict)
	}
	if s.inject(ctx, CutoverFaultAfterChecksBeforeCAS) != nil {
		return CutoverExecutionResult{}, cutoverGateError(CutoverGateInterrupted)
	}
	if err := s.stateStore.CompareAndSwap(ctx, state, next); err != nil {
		if !errors.Is(err, ErrStateCASConflict) {
			return CutoverExecutionResult{}, cutoverGateError(CutoverGateStateUnavailable)
		}
		current, loadErr := s.stateStore.Load(ctx, workspaceID)
		if loadErr != nil {
			return CutoverExecutionResult{}, cutoverGateError(CutoverGateStateUnavailable)
		}
		if alreadyAppliedCutover(current, workspaceID, migrationID) {
			return CutoverExecutionResult{State: current, AlreadyApplied: true}, nil
		}
		return CutoverExecutionResult{}, cutoverGateError(CutoverGateStateConflict)
	}
	if s.inject(ctx, CutoverFaultAfterCASBeforeResponse) != nil {
		return CutoverExecutionResult{}, cutoverGateError(CutoverGateInterrupted)
	}
	return CutoverExecutionResult{State: next, Applied: true}, nil
}

func evaluateCutoverGates(state WorkspaceTaskDomainState, observation FinalCutoverObservation, heartbeatCount int) []CutoverGateFailure {
	drain := EvaluateDrainPreconditions(DrainPreconditions{
		OutboxWatermark:          int64(observation.OutboxWatermark),
		CutoverSequence:          int64(*state.CutoverRevision),
		ActiveLegacyTransactions: observation.ActiveLegacyTransactions,
		OldWriterHeartbeats:      heartbeatCount,
		AcceptLegacyWrites:       state.AcceptLegacyWrites,
		PreviousFenceEpoch:       int64(observation.PreviousFenceEpoch),
		CurrentFenceEpoch:        int64(state.WriteEpoch),
	})
	failures := make([]CutoverGateFailure, 0, len(drain.Failures)+4)
	for _, failure := range drain.Failures {
		code, ok := cutoverCodeForDrainFailure(failure.Code)
		if ok {
			failures = append(failures, CutoverGateFailure{Code: code})
		}
	}
	pending := observation.PendingMutations != 0 || len(observation.Reconcile.UpsertMissing) != 0 || len(observation.Reconcile.DeleteExtra) != 0
	if len(observation.Reconcile.Mismatches) != 0 || (!observation.Reconcile.Ready && !pending) {
		failures = append(failures, CutoverGateFailure{Code: CutoverGateReconcileMismatch})
	}
	if pending {
		failures = append(failures, CutoverGateFailure{Code: CutoverGatePendingMutation})
	}
	return failures
}

func cutoverCodeForDrainFailure(code DrainFailureCode) (CutoverGateFailureCode, bool) {
	switch code {
	case DrainFailureOutboxLag:
		return CutoverGateReplayIncomplete, true
	case DrainFailureActiveTransactions:
		return CutoverGateActiveLegacyTransactions, true
	case DrainFailureOldWriterHeartbeat:
		return CutoverGateOldWriterHeartbeat, true
	case DrainFailureLegacyWritesEnabled:
		return CutoverGateLegacyWritesEnabled, true
	case DrainFailureFenceEpochNotAdvanced:
		return CutoverGateFenceEpochNotAdvanced, true
	default:
		return "", false
	}
}

func cutoverObservationFitsDrain(observation FinalCutoverObservation, state WorkspaceTaskDomainState) bool {
	return observation.OutboxWatermark <= math.MaxInt64 && observation.PreviousFenceEpoch <= math.MaxInt64 &&
		state.WriteEpoch <= math.MaxInt64 && state.CutoverRevision != nil && *state.CutoverRevision <= math.MaxInt64
}

func alreadyAppliedCutover(state WorkspaceTaskDomainState, workspaceID, migrationID string) bool {
	return state.WorkspaceID == workspaceID && state.ModelVersion == ModelVersionV2 &&
		state.MigrationState == MigrationStateCutover && state.MigrationID == migrationID &&
		state.CutoverRevision != nil && state.Validate() == nil
}

func (s *CutoverService) inject(ctx context.Context, point CutoverFaultPoint) error {
	if s.faults == nil {
		return nil
	}
	return s.faults.Inject(ctx, point)
}

func cutoverGateError(codes ...CutoverGateFailureCode) error {
	failures := make([]CutoverGateFailure, len(codes))
	for index := range codes {
		failures[index] = CutoverGateFailure{Code: codes[index]}
	}
	return &CutoverGateError{Failures: failures}
}
