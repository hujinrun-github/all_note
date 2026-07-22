package taskmigration

import "testing"

func TestReconcileInventoriesCountsExpectedGeneratedSystemTargetSymmetrically(t *testing.T) {
	source := replayKey(ReplayEntityProject, "legacy-personal")
	target := replayKey(ReplayEntityProject, "system-personal")
	plan, err := ReconcileInventories(ReconcileInput{
		Source:             []CanonicalSourceRow{{Source: source, Target: target, Digest: "project-digest"}},
		V2:                 []V2MappedRow{{Target: target, Digest: "project-digest"}},
		IDMaps:             []ReconcileIDMap{{Source: source, Target: target}},
		GeneratedSystemIDs: []ReplayEntityKey{target},
	})
	if err != nil {
		t.Fatalf("ReconcileInventories: %v", err)
	}
	if !plan.Ready || len(plan.Mismatches) != 0 || len(plan.UpsertMissing) != 0 || len(plan.DeleteExtra) != 0 {
		t.Fatalf("plan=%+v", plan)
	}
}

func TestReconcileInventoriesProtectsGeneratedTargetAfterItsSourceWasDeleted(t *testing.T) {
	source := replayKey(ReplayEntityProject, "legacy-personal")
	target := replayKey(ReplayEntityProject, "system-personal")
	plan, err := ReconcileInventories(ReconcileInput{
		V2:                 []V2MappedRow{{Target: target, Digest: "project-digest"}},
		IDMaps:             []ReconcileIDMap{{Source: source, Target: target}},
		GeneratedSystemIDs: []ReplayEntityKey{target},
	})
	if err != nil {
		t.Fatalf("ReconcileInventories: %v", err)
	}
	if !plan.Ready || len(plan.DeleteExtra) != 0 || len(plan.Mismatches) != 0 {
		t.Fatalf("plan=%+v", plan)
	}
}
