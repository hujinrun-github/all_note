package taskdomain

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestEvaluateRecurringTaskNaturalCompletionRequiresEveryCondition(t *testing.T) {
	base := naturalCompletionFixture(t)

	tests := []struct {
		name   string
		mutate func(*RecurringTaskCompletionSnapshot)
	}{
		{
			name: "ends on has not passed in schedule timezone",
			mutate: func(snapshot *RecurringTaskCompletionSnapshot) {
				snapshot.Now = time.Date(2026, 7, 3, 15, 59, 0, 0, time.UTC)
			},
		},
		{
			name: "generation watermark does not cover ends on",
			mutate: func(snapshot *RecurringTaskCompletionSnapshot) {
				snapshot.GenerationWatermark = "2026-07-02"
			},
		},
		{
			name: "generation watermark is absent",
			mutate: func(snapshot *RecurringTaskCompletionSnapshot) {
				snapshot.GenerationWatermark = ""
			},
		},
		{
			name: "an expected occurrence key is missing",
			mutate: func(snapshot *RecurringTaskCompletionSnapshot) {
				snapshot.Occurrences = append([]CompletionOccurrence(nil), snapshot.Occurrences[:2]...)
			},
		},
		{
			name: "an unexpected occurrence key is materialized",
			mutate: func(snapshot *RecurringTaskCompletionSnapshot) {
				snapshot.Occurrences = append(snapshot.Occurrences, CompletionOccurrence{Key: "2026-07-04", Status: ExecutionStatusDone})
			},
		},
		{
			name: "generation is running",
			mutate: func(snapshot *RecurringTaskCompletionSnapshot) {
				snapshot.GenerationStatus = GenerationStatusRunning
			},
		},
		{
			name: "generation is retry pending",
			mutate: func(snapshot *RecurringTaskCompletionSnapshot) {
				snapshot.GenerationStatus = GenerationStatusRetryPending
			},
		},
		{
			name: "generation has failed",
			mutate: func(snapshot *RecurringTaskCompletionSnapshot) {
				snapshot.GenerationStatus = GenerationStatusFailed
			},
		},
		{
			name: "a retry pending job exists despite idle header",
			mutate: func(snapshot *RecurringTaskCompletionSnapshot) {
				snapshot.RetryPendingJobs = 1
			},
		},
		{
			name: "a failed job exists despite idle header",
			mutate: func(snapshot *RecurringTaskCompletionSnapshot) {
				snapshot.FailedJobs = 1
			},
		},
		{
			name: "an occurrence is open",
			mutate: func(snapshot *RecurringTaskCompletionSnapshot) {
				snapshot.Occurrences[0].Status = ExecutionStatusOpen
			},
		},
		{
			name: "an occurrence is active",
			mutate: func(snapshot *RecurringTaskCompletionSnapshot) {
				snapshot.Occurrences[0].Status = ExecutionStatusActive
			},
		},
		{
			name: "an occurrence is blocked",
			mutate: func(snapshot *RecurringTaskCompletionSnapshot) {
				snapshot.Occurrences[0].Status = ExecutionStatusBlocked
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snapshot := cloneNaturalCompletionSnapshot(base)
			tt.mutate(&snapshot)
			got, err := EvaluateRecurringTaskNaturalCompletion(snapshot)
			if err != nil {
				t.Fatalf("EvaluateRecurringTaskNaturalCompletion() unexpected error: %v", err)
			}
			if got != TaskLifecycleActive {
				t.Fatalf("lifecycle = %q, want %q", got, TaskLifecycleActive)
			}
		})
	}
}

func TestEvaluateRecurringTaskNaturalCompletionCompletesOnlyValidSnapshot(t *testing.T) {
	snapshot := naturalCompletionFixture(t)

	got, err := EvaluateRecurringTaskNaturalCompletion(snapshot)
	if err != nil {
		t.Fatalf("EvaluateRecurringTaskNaturalCompletion() unexpected error: %v", err)
	}
	if got != TaskLifecycleCompleted {
		t.Fatalf("lifecycle = %q, want %q", got, TaskLifecycleCompleted)
	}
}

func TestEvaluateRecurringTaskNaturalCompletionUsesAllScheduleVersions(t *testing.T) {
	oldSchedule := mustNormalizeCompletionSchedule(t, ScheduleInput{
		RecurrenceType: RecurrenceDaily,
		TimingType:     TimingDate,
		Timezone:       "Asia/Shanghai",
		StartsOn:       "2026-07-01",
		EndsOn:         "2026-07-07",
		Rule:           json.RawMessage(`{"interval":1}`),
	})
	newSchedule := mustNormalizeCompletionSchedule(t, ScheduleInput{
		RecurrenceType: RecurrenceWeekly,
		TimingType:     TimingDate,
		Timezone:       "Asia/Shanghai",
		StartsOn:       "2026-07-01",
		EndsOn:         "2026-07-07",
		Rule:           json.RawMessage(`{"interval":1,"weekdays":[1,3,5]}`),
	})
	snapshot := RecurringTaskCompletionSnapshot{
		LifecycleStatus:     TaskLifecycleActive,
		Now:                 time.Date(2026, 7, 7, 16, 1, 0, 0, time.UTC),
		GenerationWatermark: "2026-07-07",
		GenerationStatus:    GenerationStatusIdle,
		ScheduleVersions: []CompletionScheduleVersion{
			{Schedule: oldSchedule, Effective: ScheduleEffectiveRange{From: "2026-07-01", To: "2026-07-04"}},
			{Schedule: newSchedule, Effective: ScheduleEffectiveRange{From: "2026-07-04"}},
		},
		Occurrences: []CompletionOccurrence{
			{Key: "2026-07-01", Status: ExecutionStatusDone},
			{Key: "2026-07-02", Status: ExecutionStatusSkipped},
			{Key: "2026-07-03", Status: ExecutionStatusDone},
			{Key: "2026-07-06", Status: ExecutionStatusCancelled},
		},
	}

	got, err := EvaluateRecurringTaskNaturalCompletion(snapshot)
	if err != nil {
		t.Fatalf("EvaluateRecurringTaskNaturalCompletion() unexpected error: %v", err)
	}
	if got != TaskLifecycleCompleted {
		t.Fatalf("lifecycle = %q, want %q", got, TaskLifecycleCompleted)
	}
}

func TestEvaluateRecurringTaskNaturalCompletionReopenAndRecomplete(t *testing.T) {
	snapshot := naturalCompletionFixture(t)

	completed, err := EvaluateRecurringTaskNaturalCompletion(snapshot)
	if err != nil || completed != TaskLifecycleCompleted {
		t.Fatalf("initial evaluation = (%q, %v), want completed", completed, err)
	}

	snapshot.LifecycleStatus = completed
	snapshot.Occurrences[0].Status = ExecutionStatusOpen
	reopened, err := EvaluateRecurringTaskNaturalCompletion(snapshot)
	if err != nil {
		t.Fatalf("evaluation after reopen error: %v", err)
	}
	if reopened != TaskLifecycleActive {
		t.Fatalf("lifecycle after occurrence reopen = %q, want %q", reopened, TaskLifecycleActive)
	}

	snapshot.LifecycleStatus = reopened
	snapshot.Occurrences[0].Status = ExecutionStatusDone
	recompleted, err := EvaluateRecurringTaskNaturalCompletion(snapshot)
	if err != nil {
		t.Fatalf("evaluation after occurrence re-completion error: %v", err)
	}
	if recompleted != TaskLifecycleCompleted {
		t.Fatalf("lifecycle after occurrence re-completion = %q, want %q", recompleted, TaskLifecycleCompleted)
	}
}

func TestEvaluateRecurringTaskNaturalCompletionUsesCurrentScheduleTimezone(t *testing.T) {
	schedule := mustNormalizeCompletionSchedule(t, ScheduleInput{
		RecurrenceType: RecurrenceDaily,
		TimingType:     TimingDate,
		Timezone:       "America/New_York",
		StartsOn:       "2026-11-01",
		EndsOn:         "2026-11-01",
		Rule:           json.RawMessage(`{"interval":1}`),
	})
	snapshot := RecurringTaskCompletionSnapshot{
		LifecycleStatus:     TaskLifecycleActive,
		Now:                 time.Date(2026, 11, 2, 4, 30, 0, 0, time.UTC),
		GenerationWatermark: "2026-11-01",
		GenerationStatus:    GenerationStatusIdle,
		ScheduleVersions: []CompletionScheduleVersion{
			{Schedule: schedule, Effective: ScheduleEffectiveRange{From: "2026-11-01"}},
		},
		Occurrences: []CompletionOccurrence{{Key: "2026-11-01", Status: ExecutionStatusDone}},
	}

	got, err := EvaluateRecurringTaskNaturalCompletion(snapshot)
	if err != nil {
		t.Fatalf("before local midnight error: %v", err)
	}
	if got != TaskLifecycleActive {
		t.Fatalf("before local midnight lifecycle = %q, want active", got)
	}

	snapshot.Now = time.Date(2026, 11, 2, 5, 1, 0, 0, time.UTC)
	got, err = EvaluateRecurringTaskNaturalCompletion(snapshot)
	if err != nil {
		t.Fatalf("after local midnight error: %v", err)
	}
	if got != TaskLifecycleCompleted {
		t.Fatalf("after local midnight lifecycle = %q, want completed", got)
	}
}

func TestEvaluateRecurringTaskNaturalCompletionRejectsInvalidSnapshot(t *testing.T) {
	base := naturalCompletionFixture(t)
	tests := []struct {
		name   string
		mutate func(*RecurringTaskCompletionSnapshot)
	}{
		{name: "no schedule versions", mutate: func(snapshot *RecurringTaskCompletionSnapshot) { snapshot.ScheduleVersions = nil }},
		{name: "no open current version", mutate: func(snapshot *RecurringTaskCompletionSnapshot) {
			snapshot.ScheduleVersions[0].Effective.To = "2026-07-04"
		}},
		{name: "multiple open versions", mutate: func(snapshot *RecurringTaskCompletionSnapshot) {
			snapshot.ScheduleVersions = append(snapshot.ScheduleVersions, snapshot.ScheduleVersions[0])
		}},
		{name: "current schedule has no ends on", mutate: func(snapshot *RecurringTaskCompletionSnapshot) { snapshot.ScheduleVersions[0].Schedule.EndsOn = "" }},
		{name: "invalid generation status", mutate: func(snapshot *RecurringTaskCompletionSnapshot) {
			snapshot.GenerationStatus = GenerationStatus("unknown")
		}},
		{name: "negative retry jobs", mutate: func(snapshot *RecurringTaskCompletionSnapshot) { snapshot.RetryPendingJobs = -1 }},
		{name: "duplicate occurrence key", mutate: func(snapshot *RecurringTaskCompletionSnapshot) {
			snapshot.Occurrences = append(snapshot.Occurrences, snapshot.Occurrences[0])
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snapshot := cloneNaturalCompletionSnapshot(base)
			tt.mutate(&snapshot)
			_, err := EvaluateRecurringTaskNaturalCompletion(snapshot)
			if !errors.Is(err, ErrInvalidSchedule) {
				t.Fatalf("error = %v, want %v", err, ErrInvalidSchedule)
			}
		})
	}
}

func naturalCompletionFixture(t *testing.T) RecurringTaskCompletionSnapshot {
	t.Helper()
	schedule := mustNormalizeCompletionSchedule(t, ScheduleInput{
		RecurrenceType: RecurrenceDaily,
		TimingType:     TimingDate,
		Timezone:       "Asia/Shanghai",
		StartsOn:       "2026-07-01",
		EndsOn:         "2026-07-03",
		Rule:           json.RawMessage(`{"interval":1}`),
	})
	return RecurringTaskCompletionSnapshot{
		LifecycleStatus:     TaskLifecycleActive,
		Now:                 time.Date(2026, 7, 3, 16, 1, 0, 0, time.UTC),
		GenerationWatermark: "2026-07-03",
		GenerationStatus:    GenerationStatusIdle,
		ScheduleVersions: []CompletionScheduleVersion{
			{Schedule: schedule, Effective: ScheduleEffectiveRange{From: "2026-07-01"}},
		},
		Occurrences: []CompletionOccurrence{
			{Key: "2026-07-01", Status: ExecutionStatusDone},
			{Key: "2026-07-02", Status: ExecutionStatusSkipped},
			{Key: "2026-07-03", Status: ExecutionStatusCancelled},
		},
	}
}

func cloneNaturalCompletionSnapshot(current RecurringTaskCompletionSnapshot) RecurringTaskCompletionSnapshot {
	clone := current
	clone.ScheduleVersions = append([]CompletionScheduleVersion(nil), current.ScheduleVersions...)
	clone.Occurrences = append([]CompletionOccurrence(nil), current.Occurrences...)
	return clone
}

func mustNormalizeCompletionSchedule(t *testing.T, input ScheduleInput) Schedule {
	t.Helper()
	schedule, err := NormalizeSchedule(input)
	if err != nil {
		t.Fatalf("NormalizeSchedule() unexpected error: %v", err)
	}
	return schedule
}
