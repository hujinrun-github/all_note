package taskmigration

import (
	"reflect"
	"testing"
)

func TestReduceReplayAdvancesWatermarkInStrictSequence(t *testing.T) {
	initial := ReplayState{Watermark: 40}
	events := []ReplayEvent{
		upsertEvent(41, ReplayEntityProject, "project-1", 1, "name", "Personal"),
		upsertEvent(42, ReplayEntityTask, "task-1", 1, "title", "Write tests"),
		upsertEvent(43, ReplayEntityOccurrence, "occurrence-1", 1, "status", "open"),
	}

	next, plan, err := ReduceReplay(initial, events)
	if err != nil {
		t.Fatalf("ReduceReplay() error = %v", err)
	}
	if initial.Watermark != 40 || initial.Ledger != nil || initial.Projection != nil {
		t.Fatalf("input state mutated: %#v", initial)
	}
	if next.Watermark != 43 {
		t.Fatalf("watermark = %d, want 43", next.Watermark)
	}
	if plan.FromWatermark != 40 || plan.ToWatermark != 43 {
		t.Fatalf("plan watermark = %d..%d, want 40..43", plan.FromWatermark, plan.ToWatermark)
	}
	if got := stepSequences(plan.Steps); !reflect.DeepEqual(got, []int64{41, 42, 43}) {
		t.Fatalf("step sequences = %v", got)
	}

	gapped, gapPlan, err := ReduceReplay(initial, []ReplayEvent{
		upsertEvent(41, ReplayEntityProject, "project-1", 1, "name", "Personal"),
		upsertEvent(43, ReplayEntityTask, "task-1", 1, "title", "global sequence interleaved by another workspace"),
	})
	if err != nil {
		t.Fatalf("workspace stream with global sequence gap error = %v", err)
	}
	if gapped.Watermark != 43 || gapPlan.ToWatermark != 43 || len(gapPlan.Steps) != 2 {
		t.Fatalf("gapped workspace replay = %#v / %#v", gapped, gapPlan)
	}
}

func TestReduceReplayOlderLogicalVersionCannotOverwriteNewerProjection(t *testing.T) {
	key := ReplayEntityKey{Kind: ReplayEntityTask, SourceID: "task-1"}
	initial := ReplayState{
		Watermark: 10,
		Ledger: map[ReplayEntityKey]ReplayLedgerEntry{
			key: {LogicalVersion: 5},
		},
		Projection: map[ReplayEntityKey]ReplayProjectionEntry{
			key: {LogicalVersion: 5, Image: ReplayImage{"title": "new"}},
		},
	}

	next, plan, err := ReduceReplay(initial, []ReplayEvent{
		upsertEvent(11, ReplayEntityTask, "task-1", 4, "title", "old"),
	})
	if err != nil {
		t.Fatalf("ReduceReplay() error = %v", err)
	}
	if next.Watermark != 11 {
		t.Fatalf("watermark = %d, want 11", next.Watermark)
	}
	if got := next.Projection[key]; got.LogicalVersion != 5 || got.Image["title"] != "new" {
		t.Fatalf("projection overwritten by stale event: %#v", got)
	}
	if len(plan.Steps) != 0 {
		t.Fatalf("stale event produced mutations: %#v", plan.Steps)
	}
}

func TestReduceReplayTombstoneDeletesWithoutSourceAndPreventsRevival(t *testing.T) {
	key := ReplayEntityKey{Kind: ReplayEntityTask, SourceID: "task-1"}
	initial := ReplayState{
		Watermark: 20,
		Ledger: map[ReplayEntityKey]ReplayLedgerEntry{
			key: {LogicalVersion: 3},
		},
		Projection: map[ReplayEntityKey]ReplayProjectionEntry{
			key: {LogicalVersion: 3, Image: ReplayImage{"title": "exists only in projection"}},
		},
	}
	deleted, deletePlan, err := ReduceReplay(initial, []ReplayEvent{{
		Sequence:       21,
		EntityKind:     ReplayEntityTask,
		SourceID:       "task-1",
		Operation:      ReplayDelete,
		LogicalVersion: 4,
		TombstoneImage: ReplayImage{"title": "exists only in tombstone"},
	}})
	if err != nil {
		t.Fatalf("delete replay error = %v", err)
	}
	if _, ok := deleted.Projection[key]; ok {
		t.Fatal("tombstone did not remove projection")
	}
	if got := deleted.Ledger[key]; got.LogicalVersion != 4 || !got.Deleted {
		t.Fatalf("delete ledger = %#v, want version 4 tombstone", got)
	}
	if len(deletePlan.Steps) != 1 || deletePlan.Steps[0].Operation != ReplayDelete {
		t.Fatalf("delete plan = %#v", deletePlan)
	}

	replayed, delayedPlan, err := ReduceReplay(deleted, []ReplayEvent{
		upsertEvent(22, ReplayEntityTask, "task-1", 3, "title", "late snapshot"),
	})
	if err != nil {
		t.Fatalf("delayed snapshot replay error = %v", err)
	}
	if _, ok := replayed.Projection[key]; ok {
		t.Fatal("delayed snapshot/upsert revived a tombstoned projection")
	}
	if got := replayed.Ledger[key]; got.LogicalVersion != 4 || !got.Deleted {
		t.Fatalf("tombstone ledger changed: %#v", got)
	}
	if len(delayedPlan.Steps) != 0 || replayed.Watermark != 22 {
		t.Fatalf("stale upsert plan/watermark = %#v / %d", delayedPlan, replayed.Watermark)
	}
}

func TestReduceReplayResumeAndDuplicateReplayAreIdempotent(t *testing.T) {
	events := []ReplayEvent{
		upsertEvent(1, ReplayEntityProject, "project-1", 1, "name", "Project"),
		upsertEvent(2, ReplayEntityTask, "task-1", 1, "title", "Task"),
		upsertEvent(3, ReplayEntityRule, "rule-1", 1, "type", "weekly"),
		upsertEvent(4, ReplayEntityOccurrence, "occurrence-1", 1, "status", "open"),
		upsertEvent(5, ReplayEntityEvent, "event-1", 1, "title", "Meeting"),
	}

	full, _, err := ReduceReplay(ReplayState{}, events)
	if err != nil {
		t.Fatalf("full replay error = %v", err)
	}
	partial, _, err := ReduceReplay(ReplayState{}, events[:2])
	if err != nil {
		t.Fatalf("partial replay error = %v", err)
	}
	resumed, _, err := ReduceReplay(partial, events[2:])
	if err != nil {
		t.Fatalf("resumed replay error = %v", err)
	}
	if !reflect.DeepEqual(resumed, full) {
		t.Fatalf("resumed state differs from full replay\nresumed=%#v\nfull=%#v", resumed, full)
	}

	repeated, plan, err := ReduceReplay(full, events)
	if err != nil {
		t.Fatalf("duplicate replay error = %v", err)
	}
	if !reflect.DeepEqual(repeated, full) {
		t.Fatalf("duplicate replay changed state\nrepeated=%#v\nfull=%#v", repeated, full)
	}
	if plan.FromWatermark != 5 || plan.ToWatermark != 5 || len(plan.Steps) != 0 {
		t.Fatalf("duplicate replay plan = %#v", plan)
	}
}

func TestReduceReplayUsesFixedDependencyOrder(t *testing.T) {
	validUpserts := []ReplayEvent{
		upsertEvent(1, ReplayEntityProject, "project-1", 1, "name", "Project"),
		upsertEvent(2, ReplayEntityTask, "task-1", 1, "title", "Task"),
		upsertEvent(3, ReplayEntityRule, "rule-1", 1, "type", "weekly"),
		upsertEvent(4, ReplayEntityOccurrence, "occurrence-1", 1, "status", "open"),
		upsertEvent(5, ReplayEntityEvent, "event-1", 1, "title", "Meeting"),
	}
	state, _, err := ReduceReplay(ReplayState{}, validUpserts)
	if err != nil {
		t.Fatalf("ordered upserts error = %v", err)
	}

	validDeletes := []ReplayEvent{
		deleteEvent(6, ReplayEntityEvent, "event-1", 2),
		deleteEvent(7, ReplayEntityOccurrence, "occurrence-1", 2),
		deleteEvent(8, ReplayEntityRule, "rule-1", 2),
		deleteEvent(9, ReplayEntityTask, "task-1", 2),
		deleteEvent(10, ReplayEntityProject, "project-1", 2),
	}
	deleted, plan, err := ReduceReplay(state, validDeletes)
	if err != nil {
		t.Fatalf("ordered deletes error = %v", err)
	}
	if deleted.Watermark != 10 || len(deleted.Projection) != 0 || len(plan.Steps) != 5 {
		t.Fatalf("delete result = %#v, plan = %#v", deleted, plan)
	}

	mixedSourceOrder := []ReplayEvent{
		upsertEvent(1, ReplayEntityTask, "task-1", 1, "title", "Task before project"),
		upsertEvent(2, ReplayEntityProject, "project-1", 1, "name", "Project"),
	}
	next, mixedPlan, err := ReduceReplay(ReplayState{}, mixedSourceOrder)
	if err != nil {
		t.Fatalf("chronological source order should be normalized by projection plan: %v", err)
	}
	if next.Watermark != 2 || len(mixedPlan.Steps) != 2 ||
		mixedPlan.Steps[0].Entity.Kind != ReplayEntityProject || mixedPlan.Steps[1].Entity.Kind != ReplayEntityTask {
		t.Fatalf("normalized projection plan = %#v", mixedPlan.Steps)
	}
}

func TestReduceReplayCoalescesMultipleChangesToFinalEntityMutation(t *testing.T) {
	next, plan, err := ReduceReplay(ReplayState{}, []ReplayEvent{
		upsertEvent(1, ReplayEntityProject, "project-1", 1, "name", "before"),
		deleteEvent(2, ReplayEntityProject, "project-1", 2),
		upsertEvent(3, ReplayEntityProject, "project-1", 3, "name", "after recreate"),
	})
	if err != nil {
		t.Fatalf("ReduceReplay() error = %v", err)
	}
	key := ReplayEntityKey{Kind: ReplayEntityProject, SourceID: "project-1"}
	if next.Watermark != 3 || next.Ledger[key].LogicalVersion != 3 || next.Ledger[key].Deleted || next.Projection[key].Image["name"] != "after recreate" {
		t.Fatalf("final replay state = %#v", next)
	}
	if len(plan.Steps) != 1 || plan.Steps[0].Operation != ReplayUpsert || plan.Steps[0].LogicalVersion != 3 || plan.Steps[0].Sequence != 3 {
		t.Fatalf("coalesced plan = %#v", plan.Steps)
	}
}

func TestReduceReplayRejectsInvalidEnvelopeAtomically(t *testing.T) {
	initial := ReplayState{Watermark: 7}
	tests := []struct {
		name   string
		events []ReplayEvent
		code   ReplayBlockCode
	}{
		{
			name: "duplicate sequence",
			events: []ReplayEvent{
				upsertEvent(8, ReplayEntityProject, "p1", 1, "name", "one"),
				upsertEvent(8, ReplayEntityTask, "t1", 1, "title", "two"),
			},
			code: ReplayBlockSequenceOrder,
		},
		{
			name: "zero logical version",
			events: []ReplayEvent{
				upsertEvent(8, ReplayEntityProject, "p1", 0, "name", "one"),
			},
			code: ReplayBlockLogicalVersion,
		},
		{
			name:   "delete without tombstone",
			events: []ReplayEvent{{Sequence: 8, EntityKind: ReplayEntityProject, SourceID: "p1", Operation: ReplayDelete, LogicalVersion: 1}},
			code:   ReplayBlockMissingImage,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			next, plan, err := ReduceReplay(initial, tt.events)
			assertReplayBlock(t, err, tt.code, "")
			if !reflect.DeepEqual(next, initial) || !reflect.DeepEqual(plan, ReplayPlan{}) {
				t.Fatalf("invalid input returned partial result: next=%#v plan=%#v", next, plan)
			}
		})
	}
}

func upsertEvent(sequence int64, kind ReplayEntityKind, sourceID string, version int64, field, value string) ReplayEvent {
	return ReplayEvent{
		Sequence: sequence, EntityKind: kind, SourceID: sourceID,
		Operation: ReplayUpsert, LogicalVersion: version,
		AfterImage: ReplayImage{field: value},
	}
}

func deleteEvent(sequence int64, kind ReplayEntityKind, sourceID string, version int64) ReplayEvent {
	return ReplayEvent{
		Sequence: sequence, EntityKind: kind, SourceID: sourceID,
		Operation: ReplayDelete, LogicalVersion: version,
		TombstoneImage: ReplayImage{"id": sourceID},
	}
}

func stepSequences(steps []ReplayStep) []int64 {
	result := make([]int64, len(steps))
	for i, step := range steps {
		result[i] = step.Sequence
	}
	return result
}

func assertReplayBlock(t *testing.T, err error, code ReplayBlockCode, reference string) {
	t.Helper()
	block, ok := err.(*ReplayBlock)
	if !ok {
		t.Fatalf("error = %T %v, want *ReplayBlock", err, err)
	}
	if block.Code != code {
		t.Fatalf("block code = %q, want %q", block.Code, code)
	}
	if reference != "" && block.Reference != reference {
		t.Fatalf("block reference = %q, want %q", block.Reference, reference)
	}
}
