package taskdomain

import "time"

type GenerationStatus string

const (
	GenerationStatusIdle         GenerationStatus = "idle"
	GenerationStatusRunning      GenerationStatus = "running"
	GenerationStatusRetryPending GenerationStatus = "retry_pending"
	GenerationStatusFailed       GenerationStatus = "failed"
)

// CompletionScheduleVersion is the immutable rule and effective interval used
// to prove the complete expected occurrence-key set.
type CompletionScheduleVersion struct {
	Schedule  Schedule
	Effective ScheduleEffectiveRange
}

// CompletionOccurrence is the minimal occurrence after-image needed by the
// natural-completion rule.
type CompletionOccurrence struct {
	Key    string
	Status ExecutionStatus
}

// RecurringTaskCompletionSnapshot contains all state that must be read inside
// one fenced transaction before evaluating natural completion. The evaluator
// itself is pure and never reads a repository or the system clock.
type RecurringTaskCompletionSnapshot struct {
	LifecycleStatus     TaskLifecycleStatus
	Now                 time.Time
	GenerationWatermark string
	GenerationStatus    GenerationStatus
	RetryPendingJobs    int
	FailedJobs          int
	ScheduleVersions    []CompletionScheduleVersion
	Occurrences         []CompletionOccurrence
}

// EvaluateRecurringTaskNaturalCompletion returns completed only when the
// snapshot proves every natural-completion invariant. For active/completed
// recurring tasks, any unmet condition returns active; this makes reopening a
// historical occurrence immediately reactivate the definition. Explicit task
// states such as paused, cancelled, and archived are preserved.
func EvaluateRecurringTaskNaturalCompletion(snapshot RecurringTaskCompletionSnapshot) (TaskLifecycleStatus, error) {
	if !completionManagedLifecycle(snapshot.LifecycleStatus) {
		return snapshot.LifecycleStatus, nil
	}
	if snapshot.Now.IsZero() {
		return snapshot.LifecycleStatus, invalidSchedule("completion snapshot now is required")
	}
	if !validGenerationStatus(snapshot.GenerationStatus) {
		return snapshot.LifecycleStatus, invalidSchedule("invalid generation status")
	}
	if snapshot.RetryPendingJobs < 0 || snapshot.FailedJobs < 0 {
		return snapshot.LifecycleStatus, invalidSchedule("generation job counts must not be negative")
	}

	currentVersion, err := currentCompletionScheduleVersion(snapshot.ScheduleVersions)
	if err != nil {
		return snapshot.LifecycleStatus, err
	}
	if currentVersion.Schedule.RecurrenceType == RecurrenceNone || currentVersion.Schedule.EndsOn == "" {
		return snapshot.LifecycleStatus, invalidSchedule("natural completion requires a recurring schedule with ends_on")
	}
	endsOn, err := requiredGeneratorDate(currentVersion.Schedule.EndsOn, "ends_on")
	if err != nil {
		return snapshot.LifecycleStatus, err
	}
	location, err := loadIANALocation(currentVersion.Schedule.Timezone)
	if err != nil {
		return snapshot.LifecycleStatus, err
	}

	expectedKeys, err := expectedCompletionKeys(snapshot.ScheduleVersions, endsOn)
	if err != nil {
		return snapshot.LifecycleStatus, err
	}
	materializedKeys, allTerminal, err := materializedCompletionKeys(snapshot.Occurrences)
	if err != nil {
		return snapshot.LifecycleStatus, err
	}

	watermarkCoversEnd := false
	if snapshot.GenerationWatermark != "" {
		watermark, parseErr := requiredGeneratorDate(snapshot.GenerationWatermark, "generation watermark")
		if parseErr != nil {
			return snapshot.LifecycleStatus, parseErr
		}
		watermarkCoversEnd = !watermark.Before(endsOn)
	}

	localNow := snapshot.Now.In(location)
	localNowDate := time.Date(
		localNow.Year(),
		localNow.Month(),
		localNow.Day(),
		0, 0, 0, 0, time.UTC,
	)
	allConditionsMet := localNowDate.After(endsOn) &&
		watermarkCoversEnd &&
		sameCompletionKeySet(expectedKeys, materializedKeys) &&
		snapshot.GenerationStatus == GenerationStatusIdle &&
		snapshot.RetryPendingJobs == 0 &&
		snapshot.FailedJobs == 0 &&
		allTerminal
	if allConditionsMet {
		return TaskLifecycleCompleted, nil
	}
	return TaskLifecycleActive, nil
}

func completionManagedLifecycle(status TaskLifecycleStatus) bool {
	return status == TaskLifecycleActive || status == TaskLifecycleCompleted
}

func validGenerationStatus(status GenerationStatus) bool {
	switch status {
	case GenerationStatusIdle, GenerationStatusRunning, GenerationStatusRetryPending, GenerationStatusFailed:
		return true
	default:
		return false
	}
}

func currentCompletionScheduleVersion(versions []CompletionScheduleVersion) (CompletionScheduleVersion, error) {
	var current CompletionScheduleVersion
	openCount := 0
	for _, version := range versions {
		if version.Effective.To == "" {
			current = version
			openCount++
		}
	}
	if openCount != 1 {
		return CompletionScheduleVersion{}, invalidSchedule("completion snapshot must contain exactly one open schedule version")
	}
	return current, nil
}

func expectedCompletionKeys(versions []CompletionScheduleVersion, endsOn time.Time) (map[string]struct{}, error) {
	if len(versions) == 0 {
		return nil, invalidSchedule("completion snapshot requires schedule versions")
	}

	earliestEffectiveFrom := time.Time{}
	for _, version := range versions {
		from, err := requiredGeneratorDate(version.Effective.From, "effective from")
		if err != nil {
			return nil, err
		}
		if earliestEffectiveFrom.IsZero() || from.Before(earliestEffectiveFrom) {
			earliestEffectiveFrom = from
		}
	}
	window := OccurrenceWindow{From: formatLocalDate(earliestEffectiveFrom), To: formatLocalDate(endsOn.AddDate(0, 0, 1))}
	expected := make(map[string]struct{})
	for _, version := range versions {
		keys, err := CalculateOccurrenceKeys(version.Schedule, version.Effective, window)
		if err != nil {
			return nil, err
		}
		for _, key := range keys {
			if _, duplicate := expected[key]; duplicate {
				return nil, invalidSchedule("schedule versions produce a duplicate occurrence key")
			}
			expected[key] = struct{}{}
		}
	}
	return expected, nil
}

func materializedCompletionKeys(occurrences []CompletionOccurrence) (map[string]struct{}, bool, error) {
	materialized := make(map[string]struct{}, len(occurrences))
	allTerminal := true
	for _, occurrence := range occurrences {
		if occurrence.Key == "" {
			return nil, false, invalidSchedule("materialized occurrence key is required")
		}
		if _, duplicate := materialized[occurrence.Key]; duplicate {
			return nil, false, invalidSchedule("duplicate materialized occurrence key")
		}
		materialized[occurrence.Key] = struct{}{}
		if !completionTerminalStatus(occurrence.Status) {
			allTerminal = false
		}
	}
	return materialized, allTerminal, nil
}

func completionTerminalStatus(status ExecutionStatus) bool {
	switch status {
	case ExecutionStatusDone, ExecutionStatusSkipped, ExecutionStatusCancelled:
		return true
	default:
		return false
	}
}

func sameCompletionKeySet(expected, materialized map[string]struct{}) bool {
	if len(expected) != len(materialized) {
		return false
	}
	for key := range expected {
		if _, exists := materialized[key]; !exists {
			return false
		}
	}
	return true
}
