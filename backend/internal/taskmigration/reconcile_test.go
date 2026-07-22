package taskmigration

import (
	"reflect"
	"testing"
)

func TestReconcileInventoriesPlansBidirectionalDiffInDependencyOrder(t *testing.T) {
	newProject := reconcileSource(ReplayEntityProject, "legacy-project-new", ReplayEntityProject, "project-new", "digest-project-new")
	newTask := reconcileSource(ReplayEntityTask, "legacy-task-new", ReplayEntityTask, "task-new", "digest-task-new")
	oldProjectSource := replayKey(ReplayEntityProject, "legacy-project-old")
	oldTaskSource := replayKey(ReplayEntityTask, "legacy-task-old")
	oldOccurrenceSource := replayKey(ReplayEntityOccurrence, "legacy-occurrence-old")
	systemInbox := replayKey(ReplayEntityProject, "system-inbox")
	unmanaged := replayKey(ReplayEntityTask, "v2-native-task")

	input := ReconcileInput{
		Source: []CanonicalSourceRow{newTask, newProject},
		V2: []V2MappedRow{
			{Target: replayKey(ReplayEntityOccurrence, "occurrence-old"), Digest: "old-occurrence"},
			{Target: unmanaged, Digest: "native"},
			{Target: replayKey(ReplayEntityTask, "task-old"), Digest: "old-task"},
			{Target: systemInbox, Digest: "generated"},
			{Target: replayKey(ReplayEntityProject, "project-old"), Digest: "old-project"},
		},
		IDMaps: []ReconcileIDMap{
			{Source: oldProjectSource, Target: replayKey(ReplayEntityProject, "project-old")},
			{Source: newProject.Source, Target: newProject.Target},
			{Source: replayKey(ReplayEntityProject, "legacy-generated-placeholder"), Target: systemInbox},
			{Source: oldOccurrenceSource, Target: replayKey(ReplayEntityOccurrence, "occurrence-old")},
			{Source: newTask.Source, Target: newTask.Target},
			{Source: oldTaskSource, Target: replayKey(ReplayEntityTask, "task-old")},
		},
		GeneratedSystemIDs: []ReplayEntityKey{systemInbox},
	}

	plan, err := ReconcileInventories(input)
	if err != nil {
		t.Fatalf("ReconcileInventories() error = %v", err)
	}
	if plan.Ready {
		t.Fatal("plan with differences reported Ready")
	}
	if got := mutationTargets(plan.UpsertMissing); !reflect.DeepEqual(got, []ReplayEntityKey{
		newProject.Target, newTask.Target,
	}) {
		t.Fatalf("upsert order = %#v", got)
	}
	if got := mutationTargets(plan.DeleteExtra); !reflect.DeepEqual(got, []ReplayEntityKey{
		replayKey(ReplayEntityOccurrence, "occurrence-old"),
		replayKey(ReplayEntityTask, "task-old"),
		replayKey(ReplayEntityProject, "project-old"),
	}) {
		t.Fatalf("delete order = %#v", got)
	}
	if containsMutationTarget(plan.DeleteExtra, systemInbox) {
		t.Fatal("generated system project was scheduled for deletion")
	}
	if containsMutationTarget(plan.DeleteExtra, unmanaged) {
		t.Fatal("v2-native row without legacy ID map was scheduled for deletion")
	}
	assertMismatchCodes(t, plan.Mismatches, ReconcileMismatchRowCount, ReconcileMismatchChecksum)
}

func TestReconcileInventoriesOrdersEveryEntityKind(t *testing.T) {
	kinds := []ReplayEntityKind{
		ReplayEntityEvent,
		ReplayEntityOccurrence,
		ReplayEntityRule,
		ReplayEntityTask,
		ReplayEntityProject,
	}
	input := ReconcileInput{}
	for _, kind := range kinds {
		source := reconcileSource(kind, "new-source-"+string(kind), kind, "new-"+string(kind), "new-digest-"+string(kind))
		input.Source = append(input.Source, source)
		input.IDMaps = append(input.IDMaps, ReconcileIDMap{Source: source.Source, Target: source.Target})

		oldSource := replayKey(kind, "old-source-"+string(kind))
		oldTarget := replayKey(kind, "old-"+string(kind))
		input.V2 = append(input.V2, V2MappedRow{Target: oldTarget, Digest: "old-digest-" + string(kind)})
		input.IDMaps = append(input.IDMaps, ReconcileIDMap{Source: oldSource, Target: oldTarget})
	}

	plan, err := ReconcileInventories(input)
	if err != nil {
		t.Fatalf("ReconcileInventories() error = %v", err)
	}
	wantUpsertKinds := []ReplayEntityKind{
		ReplayEntityProject, ReplayEntityTask, ReplayEntityRule, ReplayEntityOccurrence, ReplayEntityEvent,
	}
	wantDeleteKinds := []ReplayEntityKind{
		ReplayEntityEvent, ReplayEntityOccurrence, ReplayEntityRule, ReplayEntityTask, ReplayEntityProject,
	}
	if got := mutationKinds(plan.UpsertMissing); !reflect.DeepEqual(got, wantUpsertKinds) {
		t.Fatalf("upsert kinds = %v, want %v", got, wantUpsertKinds)
	}
	if got := mutationKinds(plan.DeleteExtra); !reflect.DeepEqual(got, wantDeleteKinds) {
		t.Fatalf("delete kinds = %v, want %v", got, wantDeleteKinds)
	}
}

func TestReconcileInventoriesRequiresAllIntegrityChecksForReady(t *testing.T) {
	source := reconcileSource(ReplayEntityTask, "legacy-task", ReplayEntityTask, "task", "source-digest")
	input := ReconcileInput{
		Source: []CanonicalSourceRow{source},
		V2:     []V2MappedRow{{Target: source.Target, Digest: "different-digest"}},
		IDMaps: []ReconcileIDMap{{Source: source.Source, Target: source.Target}},
		ForeignKeyViolations: []ReconcileViolation{
			{Entity: source.Target, Detail: "project is missing"},
		},
		StatusViolations: []ReconcileViolation{
			{Entity: source.Target, Detail: "done lacks completed_at"},
		},
	}

	plan, err := ReconcileInventories(input)
	if err != nil {
		t.Fatalf("ReconcileInventories() error = %v", err)
	}
	if plan.Ready {
		t.Fatal("digest/FK/status violations reported Ready")
	}
	assertMismatchCodes(t, plan.Mismatches,
		ReconcileMismatchRowDigest,
		ReconcileMismatchChecksum,
		ReconcileMismatchForeignKey,
		ReconcileMismatchStatus,
	)

	matching := input
	matching.V2 = []V2MappedRow{{Target: source.Target, Digest: source.Digest}}
	matching.ForeignKeyViolations = nil
	matching.StatusViolations = nil
	ready, err := ReconcileInventories(matching)
	if err != nil {
		t.Fatalf("matching ReconcileInventories() error = %v", err)
	}
	if !ready.Ready || len(ready.UpsertMissing) != 0 || len(ready.DeleteExtra) != 0 || len(ready.Mismatches) != 0 {
		t.Fatalf("matching inventory plan = %#v", ready)
	}
}

func TestReconcileInventoriesIsDeterministicAndIdempotent(t *testing.T) {
	project := reconcileSource(ReplayEntityProject, "legacy-project", ReplayEntityProject, "project", "p")
	task := reconcileSource(ReplayEntityTask, "legacy-task", ReplayEntityTask, "task", "t")
	extraSource := replayKey(ReplayEntityOccurrence, "legacy-extra")
	extraTarget := replayKey(ReplayEntityOccurrence, "extra")
	ordered := ReconcileInput{
		Source: []CanonicalSourceRow{project, task},
		V2: []V2MappedRow{
			{Target: project.Target, Digest: project.Digest},
			{Target: extraTarget, Digest: "extra"},
		},
		IDMaps: []ReconcileIDMap{
			{Source: project.Source, Target: project.Target},
			{Source: task.Source, Target: task.Target},
			{Source: extraSource, Target: extraTarget},
		},
		ForeignKeyViolations: []ReconcileViolation{
			{Entity: task.Target, Detail: "missing project"},
		},
	}
	shuffled := ReconcileInput{
		Source: []CanonicalSourceRow{task, project},
		V2: []V2MappedRow{
			{Target: extraTarget, Digest: "extra"},
			{Target: project.Target, Digest: project.Digest},
		},
		IDMaps: []ReconcileIDMap{
			{Source: extraSource, Target: extraTarget},
			{Source: task.Source, Target: task.Target},
			{Source: project.Source, Target: project.Target},
		},
		ForeignKeyViolations: []ReconcileViolation{
			{Entity: task.Target, Detail: "missing project"},
		},
	}

	first, err := ReconcileInventories(ordered)
	if err != nil {
		t.Fatalf("ordered reconcile error = %v", err)
	}
	second, err := ReconcileInventories(shuffled)
	if err != nil {
		t.Fatalf("shuffled reconcile error = %v", err)
	}
	third, err := ReconcileInventories(ordered)
	if err != nil {
		t.Fatalf("repeated reconcile error = %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("input order changed result\nfirst=%#v\nsecond=%#v", first, second)
	}
	if !reflect.DeepEqual(first, third) {
		t.Fatalf("repeated reconcile was not idempotent\nfirst=%#v\nthird=%#v", first, third)
	}
}

func TestReconcileInventoriesRejectsIdentityAndMapConflictsAtomically(t *testing.T) {
	source := reconcileSource(ReplayEntityProject, "legacy-project", ReplayEntityProject, "project", "digest")
	target := source.Target
	tests := []struct {
		name  string
		input ReconcileInput
		code  ReconcileBlockCode
	}{
		{
			name:  "duplicate canonical target",
			input: ReconcileInput{Source: []CanonicalSourceRow{source, source}},
			code:  ReconcileBlockDuplicateIdentity,
		},
		{
			name: "duplicate v2 target",
			input: ReconcileInput{V2: []V2MappedRow{
				{Target: target, Digest: "one"}, {Target: target, Digest: "two"},
			}},
			code: ReconcileBlockDuplicateIdentity,
		},
		{
			name: "target mapped from two legacy identities",
			input: ReconcileInput{IDMaps: []ReconcileIDMap{
				{Source: source.Source, Target: target},
				{Source: replayKey(ReplayEntityProject, "other-source"), Target: target},
			}},
			code: ReconcileBlockMapConflict,
		},
		{
			name: "dangling map has neither canonical source nor v2 target",
			input: ReconcileInput{IDMaps: []ReconcileIDMap{
				{Source: source.Source, Target: target},
			}},
			code: ReconcileBlockDanglingMap,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := ReconcileInventories(tt.input)
			assertReconcileBlock(t, err, tt.code)
			if !reflect.DeepEqual(plan, ReconcilePlan{}) {
				t.Fatalf("error returned partial plan: %#v", plan)
			}
		})
	}
}

func TestEvaluateDrainPreconditions(t *testing.T) {
	ready := EvaluateDrainPreconditions(DrainPreconditions{
		OutboxWatermark:          120,
		CutoverSequence:          120,
		ActiveLegacyTransactions: 0,
		OldWriterHeartbeats:      0,
		AcceptLegacyWrites:       false,
		PreviousFenceEpoch:       7,
		CurrentFenceEpoch:        8,
	})
	if !ready.Ready || len(ready.Failures) != 0 {
		t.Fatalf("ready drain check = %#v", ready)
	}

	blocked := EvaluateDrainPreconditions(DrainPreconditions{
		OutboxWatermark:          119,
		CutoverSequence:          120,
		ActiveLegacyTransactions: 2,
		OldWriterHeartbeats:      1,
		AcceptLegacyWrites:       true,
		PreviousFenceEpoch:       7,
		CurrentFenceEpoch:        7,
	})
	if blocked.Ready {
		t.Fatal("unsafe drain preconditions reported Ready")
	}
	want := []DrainFailureCode{
		DrainFailureOutboxLag,
		DrainFailureActiveTransactions,
		DrainFailureOldWriterHeartbeat,
		DrainFailureLegacyWritesEnabled,
		DrainFailureFenceEpochNotAdvanced,
	}
	if got := drainFailureCodes(blocked.Failures); !reflect.DeepEqual(got, want) {
		t.Fatalf("drain failures = %v, want %v", got, want)
	}
}

func reconcileSource(sourceKind ReplayEntityKind, sourceID string, targetKind ReplayEntityKind, targetID, digest string) CanonicalSourceRow {
	return CanonicalSourceRow{
		Source: replayKey(sourceKind, sourceID),
		Target: replayKey(targetKind, targetID),
		Digest: digest,
	}
}

func replayKey(kind ReplayEntityKind, id string) ReplayEntityKey {
	return ReplayEntityKey{Kind: kind, SourceID: id}
}

func mutationTargets(mutations []ReconcileMutation) []ReplayEntityKey {
	result := make([]ReplayEntityKey, len(mutations))
	for i, mutation := range mutations {
		result[i] = mutation.Target
	}
	return result
}

func mutationKinds(mutations []ReconcileMutation) []ReplayEntityKind {
	result := make([]ReplayEntityKind, len(mutations))
	for i, mutation := range mutations {
		result[i] = mutation.Target.Kind
	}
	return result
}

func containsMutationTarget(mutations []ReconcileMutation, target ReplayEntityKey) bool {
	for _, mutation := range mutations {
		if mutation.Target == target {
			return true
		}
	}
	return false
}

func assertMismatchCodes(t *testing.T, mismatches []ReconcileMismatch, want ...ReconcileMismatchCode) {
	t.Helper()
	got := make([]ReconcileMismatchCode, len(mismatches))
	for i, mismatch := range mismatches {
		got[i] = mismatch.Code
	}
	for _, code := range want {
		found := false
		for _, candidate := range got {
			if candidate == code {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("mismatch codes = %v, missing %q", got, code)
		}
	}
}

func assertReconcileBlock(t *testing.T, err error, code ReconcileBlockCode) {
	t.Helper()
	block, ok := err.(*ReconcileBlock)
	if !ok {
		t.Fatalf("error = %T %v, want *ReconcileBlock", err, err)
	}
	if block.Code != code {
		t.Fatalf("block code = %q, want %q", block.Code, code)
	}
}

func drainFailureCodes(failures []DrainFailure) []DrainFailureCode {
	result := make([]DrainFailureCode, len(failures))
	for i, failure := range failures {
		result[i] = failure.Code
	}
	return result
}
