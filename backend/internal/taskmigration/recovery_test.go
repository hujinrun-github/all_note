package taskmigration

import "testing"

func TestPlanMigrationRecoveryCoversEveryDurableCrashBoundary(t *testing.T) {
	idle, err := NewWorkspaceTaskDomainState("alpha", 7)
	if err != nil {
		t.Fatal(err)
	}
	backfilling, err := StartBackfill(idle, StartBackfillCommand{
		ExpectedRevision: idle.Revision, ExpectedWriteEpoch: idle.WriteEpoch,
		MigrationID: "migration-alpha", MigrationTimezone: "Asia/Shanghai",
	})
	if err != nil {
		t.Fatal(err)
	}
	catchingUp, err := BeginCatchingUp(backfilling, BeginCatchingUpCommand{
		ExpectedRevision: backfilling.Revision, ExpectedWriteEpoch: backfilling.WriteEpoch, SourceWatermark: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	draining, err := BeginDrain(catchingUp, BeginDrainCommand{
		ExpectedRevision: catchingUp.Revision, ExpectedWriteEpoch: catchingUp.WriteEpoch, CutoverRevision: 12,
	})
	if err != nil {
		t.Fatal(err)
	}
	ready, err := MarkReady(draining, MarkReadyCommand{
		ExpectedRevision: draining.Revision, ExpectedWriteEpoch: draining.WriteEpoch, SourceWatermark: 12,
	})
	if err != nil {
		t.Fatal(err)
	}
	cutover, err := Cutover(ready, CutoverCommand{
		ExpectedRevision: ready.Revision, ExpectedWriteEpoch: ready.WriteEpoch,
		MigrationID: ready.MigrationID, CutoverRevision: 12,
	})
	if err != nil {
		t.Fatal(err)
	}
	failed, err := Fail(draining, FailCommand{
		ExpectedRevision: draining.Revision, ExpectedWriteEpoch: draining.WriteEpoch, Cause: "reconcile mismatch",
	})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		state  WorkspaceTaskDomainState
		action MigrationRecoveryAction
	}{
		{name: "idle", state: idle, action: MigrationRecoveryNone},
		{name: "during snapshot", state: backfilling, action: MigrationRecoveryResumeSnapshot},
		{name: "after snapshot or mid replay", state: catchingUp, action: MigrationRecoveryResumeReplay},
		{name: "after drain", state: draining, action: MigrationRecoveryResumeDrain},
		{name: "before CAS", state: ready, action: MigrationRecoveryRetryCutover},
		{name: "after CAS before response", state: cutover, action: MigrationRecoveryComplete},
		{name: "failed", state: failed, action: MigrationRecoveryManual},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan, err := PlanMigrationRecovery(test.state)
			if err != nil {
				t.Fatalf("PlanMigrationRecovery: %v", err)
			}
			if plan.Action != test.action || plan.WorkspaceID != "alpha" || plan.Revision != test.state.Revision || plan.WriteEpoch != test.state.WriteEpoch {
				t.Fatalf("plan=%+v, want action=%s", plan, test.action)
			}
			if test.action == MigrationRecoveryManual && plan.LastError != "reconcile mismatch" {
				t.Fatalf("manual recovery lost last error: %+v", plan)
			}
		})
	}
}

func TestPlanMigrationRecoveryRejectsInvalidPersistedState(t *testing.T) {
	_, err := PlanMigrationRecovery(WorkspaceTaskDomainState{WorkspaceID: "alpha"})
	if err == nil {
		t.Fatal("invalid state produced a recovery action")
	}
}
