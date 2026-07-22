package taskdomain

import (
	"strings"
	"time"
)

func newExecutionLog(before, after Occurrence, transition ExecutionTransition) (ExecutionLog, error) {
	logID := strings.TrimSpace(transition.LogID)
	actorID := strings.TrimSpace(transition.ActorID)
	if logID == "" || actorID == "" || transition.At.IsZero() {
		return ExecutionLog{}, ErrInvalidExecutionLog
	}
	if before.WorkspaceID != after.WorkspaceID || before.ID != after.ID || after.Revision != before.Revision+1 {
		return ExecutionLog{}, ErrInvalidExecutionLog
	}
	if err := validateOccurrenceSnapshot(after); err != nil {
		return ExecutionLog{}, ErrInvalidExecutionLog
	}

	log := ExecutionLog{
		id:                 logID,
		workspaceID:        after.WorkspaceID,
		occurrenceID:       after.ID,
		fromStatus:         before.ExecutionStatus,
		toStatus:           after.ExecutionStatus,
		blockedReason:      after.BlockedReason,
		nextAction:         after.NextAction,
		actorID:            actorID,
		createdAt:          transition.At,
		occurrenceRevision: after.Revision,
	}
	if after.ActualStartAt != nil {
		log.actualStartAt = *after.ActualStartAt
		log.hasActualStartAt = true
	}
	if after.CompletedAt != nil {
		log.completedAt = *after.CompletedAt
		log.hasCompletedAt = true
	}
	return log, nil
}

func (log ExecutionLog) IsZero() bool {
	return log == ExecutionLog{}
}

func (log ExecutionLog) ID() string {
	return log.id
}

func (log ExecutionLog) WorkspaceID() string {
	return log.workspaceID
}

func (log ExecutionLog) OccurrenceID() string {
	return log.occurrenceID
}

func (log ExecutionLog) FromStatus() ExecutionStatus {
	return log.fromStatus
}

func (log ExecutionLog) ToStatus() ExecutionStatus {
	return log.toStatus
}

func (log ExecutionLog) BlockedReason() string {
	return log.blockedReason
}

func (log ExecutionLog) NextAction() string {
	return log.nextAction
}

func (log ExecutionLog) ActorID() string {
	return log.actorID
}

func (log ExecutionLog) CreatedAt() time.Time {
	return log.createdAt
}

func (log ExecutionLog) OccurrenceRevision() int64 {
	return log.occurrenceRevision
}

func (log ExecutionLog) ActualStartAt() (time.Time, bool) {
	return log.actualStartAt, log.hasActualStartAt
}

func (log ExecutionLog) CompletedAt() (time.Time, bool) {
	return log.completedAt, log.hasCompletedAt
}
