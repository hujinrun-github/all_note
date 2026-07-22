package taskdomain

import (
	"strings"
	"time"
)

type ExecutionStatus string

const (
	ExecutionStatusOpen      ExecutionStatus = "open"
	ExecutionStatusActive    ExecutionStatus = "active"
	ExecutionStatusBlocked   ExecutionStatus = "blocked"
	ExecutionStatusDone      ExecutionStatus = "done"
	ExecutionStatusSkipped   ExecutionStatus = "skipped"
	ExecutionStatusCancelled ExecutionStatus = "cancelled"
)

const (
	ErrorCodeInvalidOccurrenceTransition ErrorCode = "invalid_occurrence_transition"
	ErrorCodeInvalidOccurrenceSnapshot   ErrorCode = "invalid_occurrence_snapshot"
	ErrorCodeBlockedDetailsRequired      ErrorCode = "blocked_details_required"
	ErrorCodeSingleOccurrenceCannotSkip  ErrorCode = "single_occurrence_cannot_skip"
	ErrorCodeInvalidExecutionLog         ErrorCode = "invalid_execution_log"
)

type occurrenceDomainError struct {
	code ErrorCode
}

func (e *occurrenceDomainError) Error() string {
	return string(e.code)
}

func (e *occurrenceDomainError) Code() ErrorCode {
	return e.code
}

var (
	ErrInvalidOccurrenceTransition = &occurrenceDomainError{code: ErrorCodeInvalidOccurrenceTransition}
	ErrInvalidOccurrenceSnapshot   = &occurrenceDomainError{code: ErrorCodeInvalidOccurrenceSnapshot}
	ErrBlockedDetailsRequired      = &occurrenceDomainError{code: ErrorCodeBlockedDetailsRequired}
	ErrSingleOccurrenceCannotSkip  = &occurrenceDomainError{code: ErrorCodeSingleOccurrenceCannotSkip}
	ErrInvalidExecutionLog         = &occurrenceDomainError{code: ErrorCodeInvalidExecutionLog}
)

// Occurrence is one executable instance of a task. Recurring describes
// whether the source schedule can produce more than one instance.
type Occurrence struct {
	WorkspaceID     string
	ID              string
	TaskID          string
	OccurrenceKey   string
	ExecutionStatus ExecutionStatus
	Recurring       bool
	ActualStartAt   *time.Time
	CompletedAt     *time.Time
	BlockedReason   string
	NextAction      string
	Revision        int64
}

// ExecutionTransition contains the immutable audit identity for one status
// change. Callers must allocate a new LogID for every command.
type ExecutionTransition struct {
	LogID   string
	ActorID string
	At      time.Time
}

// ExecutionLog stores a transition and the relevant occurrence after-image.
// Its fields are deliberately private so a produced fact cannot be mutated.
type ExecutionLog struct {
	id                 string
	workspaceID        string
	occurrenceID       string
	fromStatus         ExecutionStatus
	toStatus           ExecutionStatus
	blockedReason      string
	nextAction         string
	actorID            string
	createdAt          time.Time
	occurrenceRevision int64
	actualStartAt      time.Time
	hasActualStartAt   bool
	completedAt        time.Time
	hasCompletedAt     bool
}

func StartOccurrence(current Occurrence, transition ExecutionTransition) (Occurrence, ExecutionLog, error) {
	return transitionOccurrence(current, transition, ExecutionStatusActive, ExecutionStatusOpen)
}

func BlockOccurrence(current Occurrence, reason, nextAction string, transition ExecutionTransition) (Occurrence, ExecutionLog, error) {
	if err := validateOccurrenceSnapshot(current); err != nil {
		return current, ExecutionLog{}, err
	}
	reason = strings.TrimSpace(reason)
	nextAction = strings.TrimSpace(nextAction)
	if reason == "" || nextAction == "" {
		return current, ExecutionLog{}, ErrBlockedDetailsRequired
	}
	return transitionValidatedOccurrence(current, transition, ExecutionStatusBlocked, reason, nextAction, ExecutionStatusActive)
}

func UnblockOccurrence(current Occurrence, transition ExecutionTransition) (Occurrence, ExecutionLog, error) {
	return transitionOccurrence(current, transition, ExecutionStatusActive, ExecutionStatusBlocked)
}

func CompleteOccurrence(current Occurrence, transition ExecutionTransition) (Occurrence, ExecutionLog, error) {
	return transitionOccurrence(current, transition, ExecutionStatusDone, ExecutionStatusOpen, ExecutionStatusActive)
}

func SkipOccurrence(current Occurrence, transition ExecutionTransition) (Occurrence, ExecutionLog, error) {
	if err := validateOccurrenceSnapshot(current); err != nil {
		return current, ExecutionLog{}, err
	}
	if !current.Recurring {
		return current, ExecutionLog{}, ErrSingleOccurrenceCannotSkip
	}
	return transitionValidatedOccurrence(current, transition, ExecutionStatusSkipped, "", "", ExecutionStatusOpen)
}

func CancelOccurrence(current Occurrence, transition ExecutionTransition) (Occurrence, ExecutionLog, error) {
	return transitionOccurrence(current, transition, ExecutionStatusCancelled, ExecutionStatusOpen, ExecutionStatusActive, ExecutionStatusBlocked)
}

func ReopenOccurrence(current Occurrence, transition ExecutionTransition) (Occurrence, ExecutionLog, error) {
	return transitionOccurrence(current, transition, ExecutionStatusOpen, ExecutionStatusDone, ExecutionStatusSkipped, ExecutionStatusCancelled)
}

func transitionOccurrence(
	current Occurrence,
	transition ExecutionTransition,
	target ExecutionStatus,
	allowedFrom ...ExecutionStatus,
) (Occurrence, ExecutionLog, error) {
	if err := validateOccurrenceSnapshot(current); err != nil {
		return current, ExecutionLog{}, err
	}
	return transitionValidatedOccurrence(current, transition, target, "", "", allowedFrom...)
}

func transitionValidatedOccurrence(
	current Occurrence,
	transition ExecutionTransition,
	target ExecutionStatus,
	blockedReason string,
	nextAction string,
	allowedFrom ...ExecutionStatus,
) (Occurrence, ExecutionLog, error) {
	if !containsExecutionStatus(allowedFrom, current.ExecutionStatus) {
		return current, ExecutionLog{}, ErrInvalidOccurrenceTransition
	}

	updated := current
	updated.ExecutionStatus = target
	updated.BlockedReason = blockedReason
	updated.NextAction = nextAction
	updated.Revision++

	if target == ExecutionStatusActive && current.ExecutionStatus == ExecutionStatusOpen && updated.ActualStartAt == nil {
		startedAt := transition.At
		updated.ActualStartAt = &startedAt
	}
	if target == ExecutionStatusDone {
		completedAt := transition.At
		updated.CompletedAt = &completedAt
	} else {
		updated.CompletedAt = nil
	}

	log, err := newExecutionLog(current, updated, transition)
	if err != nil {
		return current, ExecutionLog{}, err
	}
	return updated, log, nil
}

func validateOccurrenceSnapshot(occurrence Occurrence) error {
	if !isKnownExecutionStatus(occurrence.ExecutionStatus) {
		return ErrInvalidOccurrenceSnapshot
	}
	if (occurrence.ExecutionStatus == ExecutionStatusDone) != (occurrence.CompletedAt != nil) {
		return ErrInvalidOccurrenceSnapshot
	}

	reason := strings.TrimSpace(occurrence.BlockedReason)
	nextAction := strings.TrimSpace(occurrence.NextAction)
	if occurrence.ExecutionStatus == ExecutionStatusBlocked {
		if reason == "" || nextAction == "" {
			return ErrInvalidOccurrenceSnapshot
		}
	} else if reason != "" || nextAction != "" {
		return ErrInvalidOccurrenceSnapshot
	}
	return nil
}

func containsExecutionStatus(statuses []ExecutionStatus, status ExecutionStatus) bool {
	for _, candidate := range statuses {
		if candidate == status {
			return true
		}
	}
	return false
}

func isKnownExecutionStatus(status ExecutionStatus) bool {
	switch status {
	case ExecutionStatusOpen,
		ExecutionStatusActive,
		ExecutionStatusBlocked,
		ExecutionStatusDone,
		ExecutionStatusSkipped,
		ExecutionStatusCancelled:
		return true
	default:
		return false
	}
}
