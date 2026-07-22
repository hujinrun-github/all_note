package taskdomain

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestCompleteAndReopenSingleOccurrenceMutateTaskAndOccurrenceAtomically(t *testing.T) {
	t.Parallel()

	current := singleTaskAggregate()
	completed, logs, err := CompleteSingleOccurrence(
		current,
		"occurrence-1",
		AggregateExpectedRevisions{Task: 4, Occurrences: map[string]int64{"occurrence-1": 7}},
		aggregateTransition("complete"),
	)
	if err != nil {
		t.Fatalf("CompleteSingleOccurrence() error = %v", err)
	}
	assertAggregateState(t, completed, TaskLifecycleCompleted, 5, "occurrence-1", ExecutionStatusDone, 8)
	if len(logs) != 1 || logs[0].ToStatus() != ExecutionStatusDone {
		t.Fatalf("completion logs = %#v, want one done transition", logs)
	}

	reopened, logs, err := ReopenSingleOccurrence(
		completed,
		"occurrence-1",
		AggregateExpectedRevisions{Task: 5, Occurrences: map[string]int64{"occurrence-1": 8}},
		aggregateTransition("reopen"),
	)
	if err != nil {
		t.Fatalf("ReopenSingleOccurrence() error = %v", err)
	}
	assertAggregateState(t, reopened, TaskLifecycleActive, 6, "occurrence-1", ExecutionStatusOpen, 9)
	if len(logs) != 1 || logs[0].ToStatus() != ExecutionStatusOpen {
		t.Fatalf("reopen logs = %#v, want one open transition", logs)
	}
}

func TestCancelTaskStopsGenerationAndCancelsOnlyNonTerminalOccurrences(t *testing.T) {
	t.Parallel()

	now := aggregateTransition("fixture").At
	current := recurringTaskAggregate(
		occurrenceWithStatus("open", ExecutionStatusOpen, now, 10),
		occurrenceWithStatus("active", ExecutionStatusActive, now, 11),
		occurrenceWithStatus("blocked", ExecutionStatusBlocked, now, 12),
		occurrenceWithStatus("done", ExecutionStatusDone, now, 13),
		occurrenceWithStatus("skipped", ExecutionStatusSkipped, now, 14),
		occurrenceWithStatus("cancelled", ExecutionStatusCancelled, now, 15),
	)
	original := cloneAggregateForTest(current)

	updated, logs, err := CancelTaskAggregate(
		current,
		AggregateExpectedRevisions{
			Task: 20,
			Occurrences: map[string]int64{
				"open": 10, "active": 11, "blocked": 12,
			},
		},
		map[string]ExecutionTransition{
			"open": aggregateTransition("cancel-open"), "active": aggregateTransition("cancel-active"), "blocked": aggregateTransition("cancel-blocked"),
		},
	)
	if err != nil {
		t.Fatalf("CancelTaskAggregate() error = %v", err)
	}
	if updated.LifecycleStatus != TaskLifecycleCancelled || updated.Revision != 21 || updated.GenerationEnabled {
		t.Fatalf("cancelled task = %#v", updated)
	}
	for _, id := range []string{"open", "active", "blocked"} {
		assertOccurrenceState(t, updated, id, ExecutionStatusCancelled, originalOccurrenceRevision(original, id)+1)
	}
	for _, id := range []string{"done", "skipped", "cancelled"} {
		want := occurrenceByID(t, original, id)
		got := occurrenceByID(t, updated, id)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("terminal occurrence %q changed: got %#v, want %#v", id, got, want)
		}
	}
	if len(logs) != 3 {
		t.Fatalf("logs = %d, want 3", len(logs))
	}
	if !reflect.DeepEqual(current, original) {
		t.Fatalf("command mutated input aggregate: got %#v, want %#v", current, original)
	}
}

func TestPauseAndPauseWithFutureCancellationAreDifferentCommands(t *testing.T) {
	t.Parallel()

	now := aggregateTransition("fixture").At
	current := recurringTaskAggregate(
		occurrenceWithStatus("past-open", ExecutionStatusOpen, now, 5),
		occurrenceWithStatus("future-open", ExecutionStatusOpen, now, 6),
		occurrenceWithStatus("future-done", ExecutionStatusDone, now, 7),
	)

	paused, logs, err := PauseTaskAggregate(current, 20)
	if err != nil {
		t.Fatalf("PauseTaskAggregate() error = %v", err)
	}
	if paused.LifecycleStatus != TaskLifecyclePaused || paused.Revision != 21 || paused.GenerationEnabled {
		t.Fatalf("paused task = %#v", paused)
	}
	if len(logs) != 0 || !reflect.DeepEqual(paused.Occurrences, current.Occurrences) {
		t.Fatalf("ordinary pause modified occurrences: logs=%#v occurrences=%#v", logs, paused.Occurrences)
	}

	pausedAndCancelled, logs, err := PauseTaskAndCancelFutureOccurrences(
		current,
		[]string{"future-open", "future-done"},
		AggregateExpectedRevisions{Task: 20, Occurrences: map[string]int64{"future-open": 6}},
		map[string]ExecutionTransition{"future-open": aggregateTransition("cancel-future")},
	)
	if err != nil {
		t.Fatalf("PauseTaskAndCancelFutureOccurrences() error = %v", err)
	}
	assertOccurrenceState(t, pausedAndCancelled, "past-open", ExecutionStatusOpen, 5)
	assertOccurrenceState(t, pausedAndCancelled, "future-open", ExecutionStatusCancelled, 7)
	assertOccurrenceState(t, pausedAndCancelled, "future-done", ExecutionStatusDone, 7)
	if len(logs) != 1 {
		t.Fatalf("logs = %d, want 1", len(logs))
	}
}

func TestCancelledTaskOccurrenceCannotBeReopenedDirectly(t *testing.T) {
	t.Parallel()

	current := singleTaskAggregate()
	current.LifecycleStatus = TaskLifecycleCancelled
	current.Occurrences[0] = occurrenceWithStatus("occurrence-1", ExecutionStatusCancelled, aggregateTransition("fixture").At, 7)
	current.Occurrences[0].Recurring = false
	original := cloneAggregateForTest(current)

	got, logs, err := ReopenSingleOccurrence(
		current,
		"occurrence-1",
		AggregateExpectedRevisions{Task: 4, Occurrences: map[string]int64{"occurrence-1": 7}},
		aggregateTransition("reopen"),
	)
	if !errors.Is(err, ErrInvalidTaskTransition) {
		t.Fatalf("ReopenSingleOccurrence() error = %v, want %v", err, ErrInvalidTaskTransition)
	}
	if !reflect.DeepEqual(got, original) || len(logs) != 0 {
		t.Fatalf("rejected reopen mutated aggregate: got=%#v logs=%#v", got, logs)
	}
}

func TestAggregateCommandsValidateTaskAndOccurrenceRevisionsBeforeMutation(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		expected AggregateExpectedRevisions
	}{
		{name: "stale task", expected: AggregateExpectedRevisions{Task: 3, Occurrences: map[string]int64{"occurrence-1": 7}}},
		{name: "stale occurrence", expected: AggregateExpectedRevisions{Task: 4, Occurrences: map[string]int64{"occurrence-1": 6}}},
		{name: "missing occurrence revision", expected: AggregateExpectedRevisions{Task: 4, Occurrences: map[string]int64{}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current := singleTaskAggregate()
			original := cloneAggregateForTest(current)
			got, logs, err := CompleteSingleOccurrence(current, "occurrence-1", tc.expected, aggregateTransition("complete"))
			if !errors.Is(err, ErrAggregateRevisionConflict) {
				t.Fatalf("error = %v, want %v", err, ErrAggregateRevisionConflict)
			}
			if ErrorCodeOf(err) != ErrorCode("revision_conflict") {
				t.Fatalf("error code = %q, want revision_conflict", ErrorCodeOf(err))
			}
			if !reflect.DeepEqual(got, original) || len(logs) != 0 {
				t.Fatalf("revision failure mutated aggregate: got=%#v logs=%#v", got, logs)
			}
		})
	}
}

func TestCancelTaskValidatesEveryAffectedOccurrenceBeforeMutation(t *testing.T) {
	t.Parallel()

	now := aggregateTransition("fixture").At
	current := recurringTaskAggregate(
		occurrenceWithStatus("first", ExecutionStatusOpen, now, 2),
		occurrenceWithStatus("second", ExecutionStatusActive, now, 3),
	)
	original := cloneAggregateForTest(current)
	got, logs, err := CancelTaskAggregate(
		current,
		AggregateExpectedRevisions{Task: 20, Occurrences: map[string]int64{"first": 2, "second": 99}},
		map[string]ExecutionTransition{"first": aggregateTransition("first"), "second": aggregateTransition("second")},
	)
	if !errors.Is(err, ErrAggregateRevisionConflict) {
		t.Fatalf("CancelTaskAggregate() error = %v, want revision conflict", err)
	}
	if !reflect.DeepEqual(got, original) || len(logs) != 0 {
		t.Fatalf("failed bulk command was not atomic: got=%#v logs=%#v", got, logs)
	}
}

func TestAggregateFailureNeverMutatesOriginal(t *testing.T) {
	t.Parallel()

	current := singleTaskAggregate()
	current.Occurrences[0].ExecutionStatus = ExecutionStatusBlocked
	current.Occurrences[0].BlockedReason = ""
	current.Occurrences[0].NextAction = ""
	original := cloneAggregateForTest(current)
	got, logs, err := CancelTaskAggregate(
		current,
		AggregateExpectedRevisions{Task: 4, Occurrences: map[string]int64{"occurrence-1": 7}},
		map[string]ExecutionTransition{"occurrence-1": aggregateTransition("cancel")},
	)
	if !errors.Is(err, ErrInvalidOccurrenceSnapshot) {
		t.Fatalf("CancelTaskAggregate() error = %v, want %v", err, ErrInvalidOccurrenceSnapshot)
	}
	if !reflect.DeepEqual(got, original) || len(logs) != 0 || !reflect.DeepEqual(current, original) {
		t.Fatalf("failed command mutated aggregate: got=%#v current=%#v logs=%#v", got, current, logs)
	}
}

func singleTaskAggregate() TaskAggregate {
	return TaskAggregate{
		WorkspaceID:       "workspace-1",
		TaskID:            "task-1",
		LifecycleStatus:   TaskLifecycleActive,
		Recurring:         false,
		Revision:          4,
		GenerationEnabled: false,
		Occurrences: []Occurrence{{
			WorkspaceID: "workspace-1", ID: "occurrence-1", TaskID: "task-1", OccurrenceKey: "once",
			ExecutionStatus: ExecutionStatusOpen, Recurring: false, Revision: 7,
		}},
	}
}

func recurringTaskAggregate(occurrences ...Occurrence) TaskAggregate {
	return TaskAggregate{
		WorkspaceID:       "workspace-1",
		TaskID:            "task-1",
		LifecycleStatus:   TaskLifecycleActive,
		Recurring:         true,
		Revision:          20,
		GenerationEnabled: true,
		Occurrences:       occurrences,
	}
}

func occurrenceWithStatus(id string, status ExecutionStatus, at time.Time, revision int64) Occurrence {
	o := Occurrence{
		WorkspaceID: "workspace-1", ID: id, TaskID: "task-1", OccurrenceKey: id,
		ExecutionStatus: status, Recurring: true, Revision: revision,
	}
	if status == ExecutionStatusActive || status == ExecutionStatusBlocked {
		startedAt := at
		o.ActualStartAt = &startedAt
	}
	if status == ExecutionStatusBlocked {
		o.BlockedReason = "waiting"
		o.NextAction = "follow up"
	}
	if status == ExecutionStatusDone {
		completedAt := at
		o.CompletedAt = &completedAt
	}
	return o
}

func aggregateTransition(suffix string) ExecutionTransition {
	return ExecutionTransition{
		LogID: "log-" + suffix, ActorID: "user-1",
		At: time.Date(2026, 7, 22, 11, 0, 0, 0, time.UTC),
	}
}

func cloneAggregateForTest(current TaskAggregate) TaskAggregate {
	clone := current
	clone.Occurrences = append([]Occurrence(nil), current.Occurrences...)
	return clone
}

func assertAggregateState(t *testing.T, aggregate TaskAggregate, taskStatus TaskLifecycleStatus, taskRevision int64, occurrenceID string, occurrenceStatus ExecutionStatus, occurrenceRevision int64) {
	t.Helper()
	if aggregate.LifecycleStatus != taskStatus || aggregate.Revision != taskRevision {
		t.Fatalf("task state = (%q, %d), want (%q, %d)", aggregate.LifecycleStatus, aggregate.Revision, taskStatus, taskRevision)
	}
	assertOccurrenceState(t, aggregate, occurrenceID, occurrenceStatus, occurrenceRevision)
}

func assertOccurrenceState(t *testing.T, aggregate TaskAggregate, id string, status ExecutionStatus, revision int64) {
	t.Helper()
	occurrence := occurrenceByID(t, aggregate, id)
	if occurrence.ExecutionStatus != status || occurrence.Revision != revision {
		t.Fatalf("occurrence %q state = (%q, %d), want (%q, %d)", id, occurrence.ExecutionStatus, occurrence.Revision, status, revision)
	}
}

func occurrenceByID(t *testing.T, aggregate TaskAggregate, id string) Occurrence {
	t.Helper()
	for _, occurrence := range aggregate.Occurrences {
		if occurrence.ID == id {
			return occurrence
		}
	}
	t.Fatalf("occurrence %q not found", id)
	return Occurrence{}
}

func originalOccurrenceRevision(aggregate TaskAggregate, id string) int64 {
	for _, occurrence := range aggregate.Occurrences {
		if occurrence.ID == id {
			return occurrence.Revision
		}
	}
	return 0
}
