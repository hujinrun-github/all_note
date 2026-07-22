package taskmigration

import "fmt"

// MigrationRecoveryAction is derived only from durable state. A restarted
// coordinator does not guess which in-memory step completed before a crash.
type MigrationRecoveryAction string

const (
	MigrationRecoveryNone           MigrationRecoveryAction = "none"
	MigrationRecoveryResumeSnapshot MigrationRecoveryAction = "resume_snapshot"
	MigrationRecoveryResumeReplay   MigrationRecoveryAction = "resume_replay"
	MigrationRecoveryResumeDrain    MigrationRecoveryAction = "resume_drain_replay_and_reconcile"
	MigrationRecoveryRetryCutover   MigrationRecoveryAction = "retry_cutover"
	MigrationRecoveryComplete       MigrationRecoveryAction = "complete"
	MigrationRecoveryManual         MigrationRecoveryAction = "manual_recovery"
)

type MigrationRecoveryPlan struct {
	WorkspaceID        string
	MigrationID        string
	Action             MigrationRecoveryAction
	Revision           uint64
	WriteEpoch         uint64
	SourceWatermark    uint64
	CutoverRevision    *uint64
	AcceptLegacyWrites bool
	LastError          string
}

// PlanMigrationRecovery converts one validated persisted checkpoint into the
// only safe next operation. All referenced stores are idempotent at these
// boundaries, so recovery resumes a phase instead of rewinding data.
func PlanMigrationRecovery(state WorkspaceTaskDomainState) (MigrationRecoveryPlan, error) {
	if err := state.Validate(); err != nil {
		return MigrationRecoveryPlan{}, fmt.Errorf("plan task-domain migration recovery: %w", err)
	}
	plan := MigrationRecoveryPlan{
		WorkspaceID: state.WorkspaceID, MigrationID: state.MigrationID,
		Revision: state.Revision, WriteEpoch: state.WriteEpoch,
		SourceWatermark: state.SourceWatermark, CutoverRevision: cloneRecoveryUint64(state.CutoverRevision),
		AcceptLegacyWrites: state.AcceptLegacyWrites, LastError: state.LastError,
	}

	switch {
	case state.ModelVersion == ModelVersionLegacy && state.MigrationState == MigrationStateIdle:
		plan.Action = MigrationRecoveryNone
	case state.ModelVersion == ModelVersionLegacy && state.MigrationState == MigrationStateBackfilling:
		plan.Action = MigrationRecoveryResumeSnapshot
	case state.ModelVersion == ModelVersionLegacy && state.MigrationState == MigrationStateCatchingUp:
		plan.Action = MigrationRecoveryResumeReplay
	case state.ModelVersion == ModelVersionLegacy && state.MigrationState == MigrationStateDraining:
		plan.Action = MigrationRecoveryResumeDrain
	case state.ModelVersion == ModelVersionLegacy && state.MigrationState == MigrationStateReady:
		plan.Action = MigrationRecoveryRetryCutover
	case state.ModelVersion == ModelVersionLegacy && state.MigrationState == MigrationStateFailed:
		plan.Action = MigrationRecoveryManual
	case state.ModelVersion == ModelVersionV2 &&
		(state.MigrationState == MigrationStateIdle || state.MigrationState == MigrationStateCutover):
		plan.Action = MigrationRecoveryComplete
	default:
		return MigrationRecoveryPlan{}, fmt.Errorf("plan task-domain migration recovery: unsupported model/state %s/%s", state.ModelVersion, state.MigrationState)
	}
	return plan, nil
}

func cloneRecoveryUint64(value *uint64) *uint64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
