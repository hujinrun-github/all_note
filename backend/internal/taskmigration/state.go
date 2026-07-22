package taskmigration

import (
	"fmt"
	"strings"
	"time"
)

// ModelVersion identifies the only task-domain model that may serve business
// writes for a workspace.
type ModelVersion string

const (
	ModelVersionLegacy ModelVersion = "legacy"
	ModelVersionV2     ModelVersion = "v2"
)

// MigrationState is the durable coordinator phase. It is deliberately
// independent from process lifetime so another coordinator can safely resume.
type MigrationState string

const (
	MigrationStateIdle        MigrationState = "idle"
	MigrationStateBackfilling MigrationState = "backfilling"
	MigrationStateCatchingUp  MigrationState = "catching_up"
	MigrationStateDraining    MigrationState = "draining"
	MigrationStateReady       MigrationState = "ready"
	MigrationStateCutover     MigrationState = "cutover"
	MigrationStateFailed      MigrationState = "failed"
)

// StateErrorCode lets the application distinguish retryable optimistic-lock
// conflicts from invalid operator commands and hard rollback boundaries.
type StateErrorCode string

const (
	StateErrorInvalidArgument     StateErrorCode = "invalid_argument"
	StateErrorInvalidInvariant    StateErrorCode = "invalid_invariant"
	StateErrorInvalidTransition   StateErrorCode = "invalid_transition"
	StateErrorStaleRevision       StateErrorCode = "stale_revision"
	StateErrorStaleEpoch          StateErrorCode = "stale_epoch"
	StateErrorWatermarkRegression StateErrorCode = "watermark_regression"
	StateErrorDrainIncomplete     StateErrorCode = "drain_incomplete"
	StateErrorNotReady            StateErrorCode = "not_ready"
	StateErrorCASMismatch         StateErrorCode = "cas_mismatch"
	StateErrorRollbackForbidden   StateErrorCode = "rollback_forbidden"
)

type StateTransitionError struct {
	Code      StateErrorCode
	Operation string
	Detail    string
}

func (e *StateTransitionError) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("task domain state %s: %s", e.Operation, e.Code)
	}
	return fmt.Sprintf("task domain state %s: %s: %s", e.Operation, e.Code, e.Detail)
}

// Is allows callers to use errors.Is with a code-only StateTransitionError in
// addition to errors.As and direct Code inspection.
func (e *StateTransitionError) Is(target error) bool {
	other, ok := target.(*StateTransitionError)
	return ok && e.Code == other.Code
}

// WorkspaceTaskDomainState is a provider-neutral representation of the
// workspace_task_domain_state row. Command functions below are pure: they
// receive a value and return a new value without mutating their input.
type WorkspaceTaskDomainState struct {
	WorkspaceID        string
	ModelVersion       ModelVersion
	MigrationState     MigrationState
	SourceWatermark    uint64
	CutoverRevision    *uint64
	WriteEpoch         uint64
	AcceptLegacyWrites bool
	MigrationTimezone  string
	V2FirstWriteAt     *time.Time
	MigrationID        string
	LastError          string
	Revision           uint64
}

func NewWorkspaceTaskDomainState(workspaceID string, writeEpoch uint64) (WorkspaceTaskDomainState, error) {
	return newWorkspaceTaskDomainState(workspaceID, writeEpoch, ModelVersionLegacy, true)
}

// NewFreshV2WorkspaceTaskDomainState initializes a workspace that has no
// legacy task-domain data. It serves v2 immediately without creating a fake
// migration or briefly opening the legacy write fence.
func NewFreshV2WorkspaceTaskDomainState(workspaceID string, writeEpoch uint64) (WorkspaceTaskDomainState, error) {
	return newWorkspaceTaskDomainState(workspaceID, writeEpoch, ModelVersionV2, false)
}

func newWorkspaceTaskDomainState(workspaceID string, writeEpoch uint64, modelVersion ModelVersion, acceptLegacyWrites bool) (WorkspaceTaskDomainState, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return WorkspaceTaskDomainState{}, stateError(StateErrorInvalidArgument, "initialize", "workspace id is required")
	}
	if writeEpoch == 0 {
		return WorkspaceTaskDomainState{}, stateError(StateErrorInvalidArgument, "initialize", "write epoch must be positive")
	}
	return WorkspaceTaskDomainState{
		WorkspaceID:        workspaceID,
		ModelVersion:       modelVersion,
		MigrationState:     MigrationStateIdle,
		WriteEpoch:         writeEpoch,
		AcceptLegacyWrites: acceptLegacyWrites,
		Revision:           1,
	}, nil
}

func (s WorkspaceTaskDomainState) Validate() error {
	if strings.TrimSpace(s.WorkspaceID) == "" {
		return stateError(StateErrorInvalidInvariant, "validate", "workspace id is required")
	}
	if s.Revision == 0 || s.WriteEpoch == 0 {
		return stateError(StateErrorInvalidInvariant, "validate", "revision and write epoch must be positive")
	}
	if s.ModelVersion != ModelVersionLegacy && s.ModelVersion != ModelVersionV2 {
		return stateError(StateErrorInvalidInvariant, "validate", "unknown model version")
	}
	if !validMigrationState(s.MigrationState) {
		return stateError(StateErrorInvalidInvariant, "validate", "unknown migration state")
	}
	if s.V2FirstWriteAt != nil && (s.ModelVersion != ModelVersionV2 || (s.MigrationState != MigrationStateIdle && s.MigrationState != MigrationStateCutover)) {
		return stateError(StateErrorInvalidInvariant, "validate", "v2 first write is valid only while serving v2")
	}

	switch s.MigrationState {
	case MigrationStateIdle:
		if s.CutoverRevision != nil {
			return stateError(StateErrorInvalidInvariant, "validate", "idle must not retain a cutover revision")
		}
		switch s.ModelVersion {
		case ModelVersionLegacy:
			if !s.AcceptLegacyWrites || s.V2FirstWriteAt != nil {
				return stateError(StateErrorInvalidInvariant, "validate", "legacy idle must accept legacy writes")
			}
		case ModelVersionV2:
			if s.AcceptLegacyWrites || strings.TrimSpace(s.MigrationID) != "" || s.SourceWatermark != 0 {
				return stateError(StateErrorInvalidInvariant, "validate", "fresh v2 idle must keep the legacy migration fence closed")
			}
		}
	case MigrationStateBackfilling, MigrationStateCatchingUp:
		if err := validateActiveLegacyMigration(s); err != nil {
			return err
		}
		if !s.AcceptLegacyWrites || s.CutoverRevision != nil {
			return stateError(StateErrorInvalidInvariant, "validate", "pre-drain migration must accept legacy writes")
		}
	case MigrationStateDraining, MigrationStateReady:
		if err := validateActiveLegacyMigration(s); err != nil {
			return err
		}
		if s.AcceptLegacyWrites || s.CutoverRevision == nil {
			return stateError(StateErrorInvalidInvariant, "validate", "drain requires a closed legacy fence and cutover revision")
		}
		if s.MigrationState == MigrationStateReady && s.SourceWatermark < *s.CutoverRevision {
			return stateError(StateErrorInvalidInvariant, "validate", "ready watermark is behind cutover revision")
		}
	case MigrationStateCutover:
		if s.ModelVersion != ModelVersionV2 || s.AcceptLegacyWrites || s.CutoverRevision == nil {
			return stateError(StateErrorInvalidInvariant, "validate", "cutover must serve only v2")
		}
		if strings.TrimSpace(s.MigrationID) == "" || s.SourceWatermark < *s.CutoverRevision {
			return stateError(StateErrorInvalidInvariant, "validate", "cutover audit or watermark is incomplete")
		}
	case MigrationStateFailed:
		if s.ModelVersion != ModelVersionLegacy || !s.AcceptLegacyWrites || strings.TrimSpace(s.MigrationID) == "" || strings.TrimSpace(s.LastError) == "" {
			return stateError(StateErrorInvalidInvariant, "validate", "failed migration must retain audit data and reopen legacy writes")
		}
	}
	return nil
}

type MarkV2FirstWriteCommand struct {
	ExpectedRevision   uint64
	ExpectedWriteEpoch uint64
	WrittenAt          time.Time
}

// MarkV2FirstWrite records the irreversible data-layer rollback boundary. The
// operation is idempotent after the first successful write so retrying the
// surrounding business command cannot move the original audit timestamp.
func MarkV2FirstWrite(state WorkspaceTaskDomainState, command MarkV2FirstWriteCommand) (WorkspaceTaskDomainState, error) {
	const operation = "mark_v2_first_write"
	if err := validateAndGuard(state, command.ExpectedRevision, command.ExpectedWriteEpoch, operation); err != nil {
		return WorkspaceTaskDomainState{}, err
	}
	if state.ModelVersion != ModelVersionV2 || (state.MigrationState != MigrationStateIdle && state.MigrationState != MigrationStateCutover) {
		return WorkspaceTaskDomainState{}, stateError(StateErrorInvalidTransition, operation, "workspace is not serving v2")
	}
	if command.WrittenAt.IsZero() {
		return WorkspaceTaskDomainState{}, stateError(StateErrorInvalidArgument, operation, "written timestamp is required")
	}
	if state.V2FirstWriteAt != nil {
		return state, nil
	}

	next := state
	writtenAt := command.WrittenAt.UTC()
	next.V2FirstWriteAt = &writtenAt
	next.Revision++
	return next, nil
}

type StartBackfillCommand struct {
	ExpectedRevision   uint64
	ExpectedWriteEpoch uint64
	MigrationID        string
	MigrationTimezone  string
}

func StartBackfill(state WorkspaceTaskDomainState, command StartBackfillCommand) (WorkspaceTaskDomainState, error) {
	const operation = "start_backfill"
	if err := validateAndGuard(state, command.ExpectedRevision, command.ExpectedWriteEpoch, operation); err != nil {
		return WorkspaceTaskDomainState{}, err
	}
	if state.ModelVersion != ModelVersionLegacy || state.MigrationState != MigrationStateIdle {
		return WorkspaceTaskDomainState{}, stateError(StateErrorInvalidTransition, operation, "backfill starts only from legacy idle")
	}
	migrationID := strings.TrimSpace(command.MigrationID)
	timezone := strings.TrimSpace(command.MigrationTimezone)
	if migrationID == "" || timezone == "" {
		return WorkspaceTaskDomainState{}, stateError(StateErrorInvalidArgument, operation, "migration id and timezone are required")
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		return WorkspaceTaskDomainState{}, stateError(StateErrorInvalidArgument, operation, "migration timezone is invalid")
	}

	next := state
	next.MigrationState = MigrationStateBackfilling
	next.SourceWatermark = 0
	next.CutoverRevision = nil
	next.MigrationID = migrationID
	next.MigrationTimezone = timezone
	next.LastError = ""
	next.Revision++
	return next, nil
}

type BeginCatchingUpCommand struct {
	ExpectedRevision   uint64
	ExpectedWriteEpoch uint64
	SourceWatermark    uint64
}

func BeginCatchingUp(state WorkspaceTaskDomainState, command BeginCatchingUpCommand) (WorkspaceTaskDomainState, error) {
	const operation = "begin_catching_up"
	if err := validateAndGuard(state, command.ExpectedRevision, command.ExpectedWriteEpoch, operation); err != nil {
		return WorkspaceTaskDomainState{}, err
	}
	if state.MigrationState != MigrationStateBackfilling && state.MigrationState != MigrationStateCatchingUp {
		return WorkspaceTaskDomainState{}, stateError(StateErrorInvalidTransition, operation, "catch-up requires backfilling or catching_up")
	}
	if command.SourceWatermark < state.SourceWatermark {
		return WorkspaceTaskDomainState{}, watermarkError(operation, state.SourceWatermark, command.SourceWatermark)
	}

	next := state
	next.MigrationState = MigrationStateCatchingUp
	next.SourceWatermark = command.SourceWatermark
	next.Revision++
	return next, nil
}

type BeginDrainCommand struct {
	ExpectedRevision   uint64
	ExpectedWriteEpoch uint64
	CutoverRevision    uint64
}

func BeginDrain(state WorkspaceTaskDomainState, command BeginDrainCommand) (WorkspaceTaskDomainState, error) {
	const operation = "begin_drain"
	if err := validateAndGuard(state, command.ExpectedRevision, command.ExpectedWriteEpoch, operation); err != nil {
		return WorkspaceTaskDomainState{}, err
	}
	if state.MigrationState != MigrationStateCatchingUp {
		return WorkspaceTaskDomainState{}, stateError(StateErrorInvalidTransition, operation, "drain requires catching_up")
	}
	if command.CutoverRevision < state.SourceWatermark {
		return WorkspaceTaskDomainState{}, watermarkError(operation, state.SourceWatermark, command.CutoverRevision)
	}

	next := state
	cutoverRevision := command.CutoverRevision
	next.CutoverRevision = &cutoverRevision
	next.MigrationState = MigrationStateDraining
	next.AcceptLegacyWrites = false
	next.WriteEpoch++
	next.Revision++
	return next, nil
}

type MarkReadyCommand struct {
	ExpectedRevision   uint64
	ExpectedWriteEpoch uint64
	SourceWatermark    uint64
}

func MarkReady(state WorkspaceTaskDomainState, command MarkReadyCommand) (WorkspaceTaskDomainState, error) {
	const operation = "mark_ready"
	if err := validateAndGuard(state, command.ExpectedRevision, command.ExpectedWriteEpoch, operation); err != nil {
		return WorkspaceTaskDomainState{}, err
	}
	if state.MigrationState != MigrationStateDraining {
		return WorkspaceTaskDomainState{}, stateError(StateErrorInvalidTransition, operation, "ready requires draining")
	}
	if command.SourceWatermark < state.SourceWatermark {
		return WorkspaceTaskDomainState{}, watermarkError(operation, state.SourceWatermark, command.SourceWatermark)
	}
	if state.CutoverRevision == nil || command.SourceWatermark < *state.CutoverRevision {
		return WorkspaceTaskDomainState{}, stateError(StateErrorDrainIncomplete, operation, "source watermark has not reached cutover revision")
	}

	next := state
	next.SourceWatermark = command.SourceWatermark
	next.MigrationState = MigrationStateReady
	next.Revision++
	return next, nil
}

type CutoverCommand struct {
	ExpectedRevision   uint64
	ExpectedWriteEpoch uint64
	MigrationID        string
	CutoverRevision    uint64
}

func Cutover(state WorkspaceTaskDomainState, command CutoverCommand) (WorkspaceTaskDomainState, error) {
	const operation = "cutover"
	if err := validateAndGuard(state, command.ExpectedRevision, command.ExpectedWriteEpoch, operation); err != nil {
		return WorkspaceTaskDomainState{}, err
	}
	if state.MigrationState != MigrationStateReady {
		return WorkspaceTaskDomainState{}, stateError(StateErrorNotReady, operation, "migration has not reached ready")
	}
	if state.ModelVersion != ModelVersionLegacy || state.CutoverRevision == nil ||
		command.MigrationID != state.MigrationID || command.CutoverRevision != *state.CutoverRevision {
		return WorkspaceTaskDomainState{}, stateError(StateErrorCASMismatch, operation, "model version, migration id, or cutover revision changed")
	}
	if state.SourceWatermark < *state.CutoverRevision || state.AcceptLegacyWrites {
		return WorkspaceTaskDomainState{}, stateError(StateErrorNotReady, operation, "drain has not completed")
	}

	next := state
	next.ModelVersion = ModelVersionV2
	next.MigrationState = MigrationStateCutover
	next.Revision++
	return next, nil
}

type FailCommand struct {
	ExpectedRevision   uint64
	ExpectedWriteEpoch uint64
	Cause              string
}

func Fail(state WorkspaceTaskDomainState, command FailCommand) (WorkspaceTaskDomainState, error) {
	const operation = "fail"
	if err := validateAndGuard(state, command.ExpectedRevision, command.ExpectedWriteEpoch, operation); err != nil {
		return WorkspaceTaskDomainState{}, err
	}
	if state.ModelVersion != ModelVersionLegacy || state.MigrationState == MigrationStateIdle || state.MigrationState == MigrationStateFailed {
		return WorkspaceTaskDomainState{}, stateError(StateErrorInvalidTransition, operation, "only an active legacy migration can fail")
	}
	cause := strings.TrimSpace(command.Cause)
	if cause == "" {
		return WorkspaceTaskDomainState{}, stateError(StateErrorInvalidArgument, operation, "failure cause is required")
	}

	next := state
	next.MigrationState = MigrationStateFailed
	next.LastError = cause
	if !next.AcceptLegacyWrites {
		next.AcceptLegacyWrites = true
		next.WriteEpoch++
	}
	next.Revision++
	return next, nil
}

type RecoverCommand struct {
	ExpectedRevision   uint64
	ExpectedWriteEpoch uint64
}

func Recover(state WorkspaceTaskDomainState, command RecoverCommand) (WorkspaceTaskDomainState, error) {
	const operation = "recover"
	if err := validateAndGuard(state, command.ExpectedRevision, command.ExpectedWriteEpoch, operation); err != nil {
		return WorkspaceTaskDomainState{}, err
	}
	if state.ModelVersion == ModelVersionV2 && state.MigrationState == MigrationStateCutover {
		if state.V2FirstWriteAt != nil {
			return WorkspaceTaskDomainState{}, stateError(StateErrorRollbackForbidden, operation, "v2 business data has already been written")
		}
		next := resetToLegacyIdle(state)
		next.WriteEpoch++
		next.Revision++
		return next, nil
	}
	if state.ModelVersion != ModelVersionLegacy || state.MigrationState != MigrationStateFailed {
		return WorkspaceTaskDomainState{}, stateError(StateErrorInvalidTransition, operation, "recover requires failed legacy migration or unused v2 cutover")
	}

	next := resetToLegacyIdle(state)
	next.Revision++
	return next, nil
}

func resetToLegacyIdle(state WorkspaceTaskDomainState) WorkspaceTaskDomainState {
	next := state
	next.ModelVersion = ModelVersionLegacy
	next.MigrationState = MigrationStateIdle
	next.SourceWatermark = 0
	next.CutoverRevision = nil
	next.AcceptLegacyWrites = true
	next.V2FirstWriteAt = nil
	next.LastError = ""
	return next
}

func validateAndGuard(state WorkspaceTaskDomainState, expectedRevision, expectedWriteEpoch uint64, operation string) error {
	if err := state.Validate(); err != nil {
		return err
	}
	if state.Revision != expectedRevision {
		return stateError(StateErrorStaleRevision, operation, fmt.Sprintf("expected=%d actual=%d", expectedRevision, state.Revision))
	}
	if state.WriteEpoch != expectedWriteEpoch {
		return stateError(StateErrorStaleEpoch, operation, fmt.Sprintf("expected=%d actual=%d", expectedWriteEpoch, state.WriteEpoch))
	}
	return nil
}

func validateActiveLegacyMigration(state WorkspaceTaskDomainState) error {
	if state.ModelVersion != ModelVersionLegacy || strings.TrimSpace(state.MigrationID) == "" || strings.TrimSpace(state.MigrationTimezone) == "" {
		return stateError(StateErrorInvalidInvariant, "validate", "active migration requires legacy model and audit identity")
	}
	return nil
}

func validMigrationState(state MigrationState) bool {
	switch state {
	case MigrationStateIdle, MigrationStateBackfilling, MigrationStateCatchingUp,
		MigrationStateDraining, MigrationStateReady, MigrationStateCutover, MigrationStateFailed:
		return true
	default:
		return false
	}
}

func watermarkError(operation string, current, attempted uint64) error {
	return stateError(StateErrorWatermarkRegression, operation, fmt.Sprintf("current=%d attempted=%d", current, attempted))
}

func stateError(code StateErrorCode, operation, detail string) error {
	return &StateTransitionError{Code: code, Operation: operation, Detail: detail}
}
