package taskdomain

import (
	"errors"
	"testing"
	"time"
)

func TestValidateTaskAggregateSnapshot(t *testing.T) {
	valid := TaskAggregateSnapshot{
		Task:     TaskRecord{WorkspaceID: "w1", ID: "t1", ProjectID: "personal", Title: "Task", LifecycleStatus: TaskLifecycleActive, Revision: 1},
		Schedule: ScheduleHeader{WorkspaceID: "w1", TaskID: "t1", Revision: 1, CurrentScheduleRevision: 1},
		Versions: []ScheduleVersion{{
			WorkspaceID: "w1", TaskID: "t1", ScheduleRevision: 1,
			RecurrenceType: RecurrenceNone, TimingType: TimingUnscheduled, Timezone: "UTC", RecurrenceRule: `{}`,
		}},
		Occurrences: []OccurrenceRecord{{
			WorkspaceID: "w1", ID: "o1", TaskID: "t1", OccurrenceKey: "once",
			ExecutionStatus: ExecutionStatusOpen, Revision: 1, GeneratedScheduleRevision: 1,
		}},
	}
	if err := ValidateTaskAggregateSnapshot(valid); err != nil {
		t.Fatalf("valid snapshot: %v", err)
	}

	invalid := valid
	invalid.Schedule.WorkspaceID = "w2"
	if err := ValidateTaskAggregateSnapshot(invalid); !errors.Is(err, ErrInvalidTaskAggregateSnapshot) {
		t.Fatalf("workspace mismatch error = %v", err)
	}

	invalid = valid
	invalid.Occurrences[0].OccurrenceKey = "not-once"
	if err := ValidateTaskAggregateSnapshot(invalid); !errors.Is(err, ErrInvalidTaskAggregateSnapshot) {
		t.Fatalf("single occurrence key error = %v", err)
	}
}

func TestValidateTaskAggregateSnapshotAllowsRecurringRuleBeforeInitialWindow(t *testing.T) {
	snapshot := TaskAggregateSnapshot{
		Task: TaskRecord{
			WorkspaceID: "w1", ID: "future-recurring", ProjectID: "personal", Title: "Future",
			LifecycleStatus: TaskLifecycleDraft, Revision: 1,
		},
		Schedule: ScheduleHeader{WorkspaceID: "w1", TaskID: "future-recurring", Revision: 1, CurrentScheduleRevision: 1},
		Versions: []ScheduleVersion{{
			WorkspaceID: "w1", TaskID: "future-recurring", ScheduleRevision: 1, EffectiveFrom: "2026-07-22",
			RecurrenceType: RecurrenceWeekly, TimingType: TimingDate, Timezone: "UTC", StartsOn: "2027-01-04",
			RecurrenceRule: `{"interval":1,"weekdays":[1]}`,
		}},
	}
	if err := ValidateTaskAggregateSnapshot(snapshot); err != nil {
		t.Fatalf("future recurring snapshot with no initial occurrences: %v", err)
	}

	snapshot.Versions[0].RecurrenceType = RecurrenceNone
	snapshot.Versions[0].RecurrenceRule = `{}`
	if err := ValidateTaskAggregateSnapshot(snapshot); !errors.Is(err, ErrInvalidTaskAggregateSnapshot) {
		t.Fatalf("non-recurring snapshot without once occurrence error = %v", err)
	}
}

func TestValidateTaskAggregateWriteAndScheduleInstall(t *testing.T) {
	current := TaskAggregate{
		WorkspaceID: "w1", TaskID: "t1", LifecycleStatus: TaskLifecycleActive, Revision: 1,
		Occurrences: []Occurrence{{WorkspaceID: "w1", ID: "o1", TaskID: "t1", OccurrenceKey: "once", ExecutionStatus: ExecutionStatusOpen, Revision: 1}},
	}
	nextOccurrence, log, err := StartOccurrence(current.Occurrences[0], ExecutionTransition{LogID: "l1", ActorID: "u1", At: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	write := TaskAggregateWrite{
		Aggregate:         TaskAggregate{WorkspaceID: "w1", TaskID: "t1", LifecycleStatus: TaskLifecycleActive, Revision: 2, Occurrences: []Occurrence{nextOccurrence}},
		ExpectedRevisions: AggregateExpectedRevisions{Task: 1, Occurrences: map[string]int64{"o1": 1}},
		ExecutionLogs:     []ExecutionLog{log},
	}
	if err := ValidateTaskAggregateWrite(write); err != nil {
		t.Fatalf("valid aggregate write: %v", err)
	}
	write.Aggregate.Revision = 3
	if err := ValidateTaskAggregateWrite(write); !errors.Is(err, ErrInvalidTaskAggregateSnapshot) {
		t.Fatalf("stale task shape error = %v", err)
	}

	install := ScheduleVersionInstall{
		WorkspaceID: "w1", TaskID: "t1", ExpectedScheduleRevision: 1,
		Version: ScheduleVersion{
			WorkspaceID: "w1", TaskID: "t1", ScheduleRevision: 2, EffectiveFrom: "2026-08-01",
			RecurrenceType: RecurrenceDaily, TimingType: TimingDate, Timezone: "UTC", StartsOn: "2026-08-01", RecurrenceRule: `{"interval":1}`,
		},
	}
	if err := ValidateScheduleVersionInstall(install); err != nil {
		t.Fatalf("valid schedule install: %v", err)
	}
	install.Version.EffectiveFrom = ""
	if err := ValidateScheduleVersionInstall(install); !errors.Is(err, ErrInvalidTaskAggregateSnapshot) {
		t.Fatalf("missing effective_from error = %v", err)
	}
}
