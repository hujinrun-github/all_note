package taskmigration

import "testing"

func TestReconcileInventoriesSupportsMappedScheduleTargets(t *testing.T) {
	source := ReplayEntityKey{Kind: ReplayEntityRule, SourceID: "legacy-rule"}
	target := ReplayEntityKey{Kind: ReplayEntitySchedule, SourceID: "task-schedule"}
	plan, err := ReconcileInventories(ReconcileInput{
		Source: []CanonicalSourceRow{{Source: source, Target: target, Digest: "same"}},
		V2:     []V2MappedRow{{Target: target, Digest: "same"}},
		IDMaps: []ReconcileIDMap{{Source: source, Target: target}},
	})
	if err != nil {
		t.Fatalf("ReconcileInventories: %v", err)
	}
	if !plan.Ready || len(plan.UpsertMissing) != 0 || len(plan.DeleteExtra) != 0 || len(plan.Mismatches) != 0 {
		t.Fatalf("plan=%+v", plan)
	}
}

func TestReduceReplayStillRejectsScheduleAsLegacyOutboxEntity(t *testing.T) {
	state := ReplayState{Ledger: map[ReplayEntityKey]ReplayLedgerEntry{}, Projection: map[ReplayEntityKey]ReplayProjectionEntry{}}
	_, _, err := ReduceReplay(state, []ReplayEvent{{
		Sequence: 1, EntityKind: ReplayEntitySchedule, SourceID: "not-a-legacy-source",
		Operation: ReplayUpsert, LogicalVersion: 1, AfterImage: ReplayImage{"id": "x"},
	}})
	block, ok := err.(*ReplayBlock)
	if !ok || block.Code != ReplayBlockEntityKind {
		t.Fatalf("ReduceReplay error=%v, want invalid entity kind", err)
	}
}
