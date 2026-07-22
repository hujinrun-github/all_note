package taskdomain

// TaskAggregate is the in-memory transaction boundary for a task definition
// and its executable occurrences. Persistence code applies the returned value
// in one fenced transaction; these functions never mutate their input.
type TaskAggregate struct {
	WorkspaceID       string
	TaskID            string
	LifecycleStatus   TaskLifecycleStatus
	Recurring         bool
	Revision          int64
	GenerationEnabled bool
	Occurrences       []Occurrence
}

// AggregateExpectedRevisions carries every optimistic lock participating in
// an aggregate command. Occurrences only need entries when that command will
// mutate them.
type AggregateExpectedRevisions struct {
	Task        int64
	Occurrences map[string]int64
}

const errorCodeAggregateOccurrenceNotFound ErrorCode = "aggregate_occurrence_not_found"

var (
	ErrAggregateRevisionConflict   = &domainError{code: ErrorCode("revision_conflict")}
	ErrAggregateOccurrenceNotFound = &domainError{code: errorCodeAggregateOccurrenceNotFound}
)

// CompleteSingleOccurrence atomically completes the one executable instance
// and its task definition.
func CompleteSingleOccurrence(
	current TaskAggregate,
	occurrenceID string,
	expected AggregateExpectedRevisions,
	transition ExecutionTransition,
) (TaskAggregate, []ExecutionLog, error) {
	index, err := validateSingleOccurrenceCommand(current, occurrenceID, expected)
	if err != nil {
		return current, nil, err
	}

	nextTaskStatus, err := CompleteTask(current.LifecycleStatus)
	if err != nil {
		return current, nil, err
	}
	nextOccurrence, log, err := CompleteOccurrence(current.Occurrences[index], transition)
	if err != nil {
		return current, nil, err
	}

	next := cloneTaskAggregate(current)
	next.LifecycleStatus = nextTaskStatus
	next.Revision++
	next.Occurrences[index] = nextOccurrence
	return next, []ExecutionLog{log}, nil
}

// ReopenSingleOccurrence atomically reactivates a completed task and reopens
// its unique occurrence. A cancelled task must first use the explicit restore
// command and therefore fails the task lifecycle transition here.
func ReopenSingleOccurrence(
	current TaskAggregate,
	occurrenceID string,
	expected AggregateExpectedRevisions,
	transition ExecutionTransition,
) (TaskAggregate, []ExecutionLog, error) {
	index, err := validateSingleOccurrenceCommand(current, occurrenceID, expected)
	if err != nil {
		return current, nil, err
	}

	nextTaskStatus, err := ReopenTaskFromOccurrence(current.LifecycleStatus)
	if err != nil {
		return current, nil, err
	}
	nextOccurrence, log, err := ReopenOccurrence(current.Occurrences[index], transition)
	if err != nil {
		return current, nil, err
	}

	next := cloneTaskAggregate(current)
	next.LifecycleStatus = nextTaskStatus
	next.Revision++
	next.Occurrences[index] = nextOccurrence
	return next, []ExecutionLog{log}, nil
}

// CancelTaskAggregate stops future generation and cancels every non-terminal
// occurrence while preserving terminal execution history.
func CancelTaskAggregate(
	current TaskAggregate,
	expected AggregateExpectedRevisions,
	transitions map[string]ExecutionTransition,
) (TaskAggregate, []ExecutionLog, error) {
	affectedIDs := make([]string, 0, len(current.Occurrences))
	for _, occurrence := range current.Occurrences {
		if !isTerminalExecutionStatus(occurrence.ExecutionStatus) {
			affectedIDs = append(affectedIDs, occurrence.ID)
		}
	}
	if err := validateAggregateRevisions(current, expected, affectedIDs); err != nil {
		return current, nil, err
	}

	nextTaskStatus, err := CancelTask(current.LifecycleStatus)
	if err != nil {
		return current, nil, err
	}
	next := cloneTaskAggregate(current)
	logs := make([]ExecutionLog, 0, len(affectedIDs))
	for index, occurrence := range current.Occurrences {
		if isTerminalExecutionStatus(occurrence.ExecutionStatus) {
			continue
		}
		updated, log, err := CancelOccurrence(occurrence, transitions[occurrence.ID])
		if err != nil {
			return current, nil, err
		}
		next.Occurrences[index] = updated
		logs = append(logs, log)
	}

	next.LifecycleStatus = nextTaskStatus
	next.GenerationEnabled = false
	next.Revision++
	return next, logs, nil
}

// PauseTaskAggregate only pauses future materialization. Existing occurrences
// remain executable and retain their revisions.
func PauseTaskAggregate(current TaskAggregate, expectedTaskRevision int64) (TaskAggregate, []ExecutionLog, error) {
	if current.Revision != expectedTaskRevision {
		return current, nil, ErrAggregateRevisionConflict
	}
	nextStatus, err := PauseTask(current.LifecycleStatus)
	if err != nil {
		return current, nil, err
	}
	next := cloneTaskAggregate(current)
	next.LifecycleStatus = nextStatus
	next.GenerationEnabled = false
	next.Revision++
	return next, nil, nil
}

// PauseTaskAndCancelFutureOccurrences is deliberately separate from pause.
// The application layer determines which materialized IDs are in the future;
// this pure command atomically pauses the task and cancels those non-terminal
// occurrences only.
func PauseTaskAndCancelFutureOccurrences(
	current TaskAggregate,
	futureOccurrenceIDs []string,
	expected AggregateExpectedRevisions,
	transitions map[string]ExecutionTransition,
) (TaskAggregate, []ExecutionLog, error) {
	indices, affectedIDs, err := selectNonTerminalOccurrences(current, futureOccurrenceIDs)
	if err != nil {
		return current, nil, err
	}
	if err := validateAggregateRevisions(current, expected, affectedIDs); err != nil {
		return current, nil, err
	}
	nextTaskStatus, err := PauseTask(current.LifecycleStatus)
	if err != nil {
		return current, nil, err
	}

	next := cloneTaskAggregate(current)
	logs := make([]ExecutionLog, 0, len(indices))
	for _, index := range indices {
		occurrence := current.Occurrences[index]
		updated, log, err := CancelOccurrence(occurrence, transitions[occurrence.ID])
		if err != nil {
			return current, nil, err
		}
		next.Occurrences[index] = updated
		logs = append(logs, log)
	}
	next.LifecycleStatus = nextTaskStatus
	next.GenerationEnabled = false
	next.Revision++
	return next, logs, nil
}

func validateSingleOccurrenceCommand(current TaskAggregate, occurrenceID string, expected AggregateExpectedRevisions) (int, error) {
	if current.Recurring {
		return -1, ErrInvalidOccurrenceTransition
	}
	index := occurrenceIndex(current.Occurrences, occurrenceID)
	if index < 0 {
		return -1, ErrAggregateOccurrenceNotFound
	}
	if current.Occurrences[index].Recurring {
		return -1, ErrInvalidOccurrenceTransition
	}
	if err := validateAggregateRevisions(current, expected, []string{occurrenceID}); err != nil {
		return -1, err
	}
	return index, nil
}

func validateAggregateRevisions(current TaskAggregate, expected AggregateExpectedRevisions, occurrenceIDs []string) error {
	if current.Revision != expected.Task {
		return ErrAggregateRevisionConflict
	}
	for _, occurrenceID := range occurrenceIDs {
		index := occurrenceIndex(current.Occurrences, occurrenceID)
		if index < 0 {
			return ErrAggregateOccurrenceNotFound
		}
		expectedRevision, ok := expected.Occurrences[occurrenceID]
		if !ok || current.Occurrences[index].Revision != expectedRevision {
			return ErrAggregateRevisionConflict
		}
	}
	return nil
}

func selectNonTerminalOccurrences(current TaskAggregate, occurrenceIDs []string) ([]int, []string, error) {
	indices := make([]int, 0, len(occurrenceIDs))
	affectedIDs := make([]string, 0, len(occurrenceIDs))
	seen := make(map[string]struct{}, len(occurrenceIDs))
	for _, occurrenceID := range occurrenceIDs {
		if _, duplicate := seen[occurrenceID]; duplicate {
			continue
		}
		seen[occurrenceID] = struct{}{}
		index := occurrenceIndex(current.Occurrences, occurrenceID)
		if index < 0 {
			return nil, nil, ErrAggregateOccurrenceNotFound
		}
		if isTerminalExecutionStatus(current.Occurrences[index].ExecutionStatus) {
			continue
		}
		indices = append(indices, index)
		affectedIDs = append(affectedIDs, occurrenceID)
	}
	return indices, affectedIDs, nil
}

func occurrenceIndex(occurrences []Occurrence, occurrenceID string) int {
	for index, occurrence := range occurrences {
		if occurrence.ID == occurrenceID {
			return index
		}
	}
	return -1
}

func isTerminalExecutionStatus(status ExecutionStatus) bool {
	switch status {
	case ExecutionStatusDone, ExecutionStatusSkipped, ExecutionStatusCancelled:
		return true
	default:
		return false
	}
}

func cloneTaskAggregate(current TaskAggregate) TaskAggregate {
	clone := current
	clone.Occurrences = append([]Occurrence(nil), current.Occurrences...)
	return clone
}
