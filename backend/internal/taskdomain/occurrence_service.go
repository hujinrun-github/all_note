package taskdomain

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type OccurrenceCommand string

const (
	OccurrenceCommandStart    OccurrenceCommand = "start"
	OccurrenceCommandBlock    OccurrenceCommand = "block"
	OccurrenceCommandUnblock  OccurrenceCommand = "unblock"
	OccurrenceCommandComplete OccurrenceCommand = "complete"
	OccurrenceCommandSkip     OccurrenceCommand = "skip"
	OccurrenceCommandCancel   OccurrenceCommand = "cancel"
	OccurrenceCommandReopen   OccurrenceCommand = "reopen"
)

type OccurrenceCommandExpectedRevisions struct {
	Task       int64
	Schedule   int64
	Occurrence int64
}

type OccurrenceCommandRequest struct {
	WorkspaceID          string
	TaskID               string
	OccurrenceID         string
	Command              OccurrenceCommand
	ExpectedRuntimeEpoch int64
	Expected             OccurrenceCommandExpectedRevisions
	BlockedReason        string
	NextAction           string
	CommandID            string
	ActorID              string
	At                   time.Time
}

type OccurrenceCommandAudit struct {
	commandID    string
	command      OccurrenceCommand
	taskID       string
	occurrenceID string
	actorID      string
	createdAt    time.Time
}

func (audit OccurrenceCommandAudit) CommandID() string          { return audit.commandID }
func (audit OccurrenceCommandAudit) Command() OccurrenceCommand { return audit.command }
func (audit OccurrenceCommandAudit) TaskID() string             { return audit.taskID }
func (audit OccurrenceCommandAudit) OccurrenceID() string       { return audit.occurrenceID }
func (audit OccurrenceCommandAudit) ActorID() string            { return audit.actorID }
func (audit OccurrenceCommandAudit) CreatedAt() time.Time       { return audit.createdAt }

type OccurrenceCommandResult struct {
	taskRevision        int64
	scheduleRevision    int64
	occurrenceRevision  int64
	taskLifecycleStatus TaskLifecycleStatus
	executionStatus     ExecutionStatus
	executionLog        ExecutionLog
	audit               OccurrenceCommandAudit
}

func (result OccurrenceCommandResult) TaskRevision() int64 { return result.taskRevision }
func (result OccurrenceCommandResult) ScheduleRevision() int64 {
	return result.scheduleRevision
}
func (result OccurrenceCommandResult) OccurrenceRevision() int64 {
	return result.occurrenceRevision
}
func (result OccurrenceCommandResult) TaskLifecycleStatus() TaskLifecycleStatus {
	return result.taskLifecycleStatus
}
func (result OccurrenceCommandResult) ExecutionStatus() ExecutionStatus {
	return result.executionStatus
}
func (result OccurrenceCommandResult) ExecutionLog() ExecutionLog {
	return result.executionLog
}
func (result OccurrenceCommandResult) Audit() OccurrenceCommandAudit {
	return result.audit
}
func (result OccurrenceCommandResult) IsZero() bool {
	return result.taskRevision == 0 && result.scheduleRevision == 0 && result.occurrenceRevision == 0 &&
		result.taskLifecycleStatus == "" && result.executionStatus == "" && result.executionLog.IsZero() &&
		result.audit == (OccurrenceCommandAudit{})
}

type OccurrenceService struct {
	fencer TaskDomainCommandFencer
	reader TaskAggregateStateReader
}

func NewOccurrenceService(fencer TaskDomainCommandFencer, reader TaskAggregateStateReader) *OccurrenceService {
	return &OccurrenceService{fencer: fencer, reader: reader}
}

func (service *OccurrenceService) Execute(ctx context.Context, request OccurrenceCommandRequest) (OccurrenceCommandResult, error) {
	if err := validateOccurrenceCommandRequest(service, request); err != nil {
		return OccurrenceCommandResult{}, err
	}

	var result OccurrenceCommandResult
	err := service.fencer.BeginFencedWrite(ctx, request.WorkspaceID, request.ExpectedRuntimeEpoch, func(tx TaskDomainFencedTx) error {
		if tx == nil || tx.TaskDomainWriter() == nil {
			return ErrInvalidTaskCommand
		}
		state, err := service.reader.GetTaskAggregateState(ctx, request.TaskID)
		if err != nil {
			return err
		}
		if state.Aggregate.WorkspaceID != request.WorkspaceID || state.Aggregate.TaskID != request.TaskID {
			return ErrInvalidTaskCommand
		}
		if state.Aggregate.Revision != request.Expected.Task {
			return ErrTaskRevisionConflict
		}
		if state.ScheduleRevision != request.Expected.Schedule {
			return ErrScheduleRevisionConflict
		}
		occurrenceIndex := occurrenceIndex(state.Aggregate.Occurrences, request.OccurrenceID)
		if occurrenceIndex < 0 {
			return ErrOccurrenceNotFound
		}
		currentOccurrence := state.Aggregate.Occurrences[occurrenceIndex]
		if currentOccurrence.WorkspaceID != request.WorkspaceID || currentOccurrence.TaskID != request.TaskID {
			return ErrInvalidTaskCommand
		}
		if currentOccurrence.Revision != request.Expected.Occurrence {
			return ErrOccurrenceRevisionConflict
		}

		next, logs, err := executeOccurrenceCommand(state.Aggregate, occurrenceIndex, request)
		if err != nil {
			return err
		}
		write := TaskAggregateWrite{
			Aggregate: next,
			ExpectedRevisions: AggregateExpectedRevisions{
				Task:        request.Expected.Task,
				Occurrences: map[string]int64{request.OccurrenceID: request.Expected.Occurrence},
			},
			ExpectedScheduleRevision: request.Expected.Schedule,
			ExecutionLogs:            logs,
		}
		if err := tx.TaskDomainWriter().SaveTaskAggregate(ctx, write); err != nil {
			return err
		}
		updatedOccurrence := next.Occurrences[occurrenceIndex]
		result = newOccurrenceCommandResult(next, state.ScheduleRevision, updatedOccurrence, logs[0], request)
		return nil
	})
	if err != nil {
		return OccurrenceCommandResult{}, err
	}
	return result, nil
}

func executeOccurrenceCommand(current TaskAggregate, index int, request OccurrenceCommandRequest) (TaskAggregate, []ExecutionLog, error) {
	transition := ExecutionTransition{LogID: request.CommandID, ActorID: request.ActorID, At: request.At}
	expected := AggregateExpectedRevisions{
		Task:        request.Expected.Task,
		Occurrences: map[string]int64{request.OccurrenceID: request.Expected.Occurrence},
	}
	occurrence := current.Occurrences[index]

	if !current.Recurring {
		switch request.Command {
		case OccurrenceCommandComplete:
			return CompleteSingleOccurrence(current, request.OccurrenceID, expected, transition)
		case OccurrenceCommandReopen:
			return ReopenSingleOccurrence(current, request.OccurrenceID, expected, transition)
		case OccurrenceCommandCancel:
			return cancelSingleOccurrence(current, index, transition)
		}
	}
	if request.Command == OccurrenceCommandReopen && current.LifecycleStatus == TaskLifecycleCancelled {
		return current, nil, ErrInvalidTaskTransition
	}

	updated, log, err := executePureOccurrenceCommand(occurrence, request, transition)
	if err != nil {
		return current, nil, err
	}
	next := cloneTaskAggregate(current)
	next.Revision++
	next.Occurrences[index] = updated
	if request.Command == OccurrenceCommandReopen && current.LifecycleStatus == TaskLifecycleCompleted {
		nextStatus, transitionErr := ReopenTaskFromOccurrence(current.LifecycleStatus)
		if transitionErr != nil {
			return current, nil, transitionErr
		}
		next.LifecycleStatus = nextStatus
	}
	return next, []ExecutionLog{log}, nil
}

func executePureOccurrenceCommand(current Occurrence, request OccurrenceCommandRequest, transition ExecutionTransition) (Occurrence, ExecutionLog, error) {
	switch request.Command {
	case OccurrenceCommandStart:
		return StartOccurrence(current, transition)
	case OccurrenceCommandBlock:
		return BlockOccurrence(current, request.BlockedReason, request.NextAction, transition)
	case OccurrenceCommandUnblock:
		return UnblockOccurrence(current, transition)
	case OccurrenceCommandComplete:
		return CompleteOccurrence(current, transition)
	case OccurrenceCommandSkip:
		return SkipOccurrence(current, transition)
	case OccurrenceCommandCancel:
		return CancelOccurrence(current, transition)
	case OccurrenceCommandReopen:
		return ReopenOccurrence(current, transition)
	default:
		return current, ExecutionLog{}, fmt.Errorf("%w: unsupported occurrence command %q", ErrInvalidTaskCommand, request.Command)
	}
}

func cancelSingleOccurrence(current TaskAggregate, index int, transition ExecutionTransition) (TaskAggregate, []ExecutionLog, error) {
	nextTaskStatus, err := CancelTask(current.LifecycleStatus)
	if err != nil {
		return current, nil, err
	}
	updated, log, err := CancelOccurrence(current.Occurrences[index], transition)
	if err != nil {
		return current, nil, err
	}
	next := cloneTaskAggregate(current)
	next.LifecycleStatus = nextTaskStatus
	next.GenerationEnabled = false
	next.Revision++
	next.Occurrences[index] = updated
	return next, []ExecutionLog{log}, nil
}

func validateOccurrenceCommandRequest(service *OccurrenceService, request OccurrenceCommandRequest) error {
	if service == nil || service.fencer == nil || service.reader == nil || strings.TrimSpace(request.WorkspaceID) == "" ||
		strings.TrimSpace(request.TaskID) == "" || strings.TrimSpace(request.OccurrenceID) == "" ||
		request.ExpectedRuntimeEpoch < 1 || request.Expected.Task < 1 || request.Expected.Schedule < 1 || request.Expected.Occurrence < 1 ||
		!validCommandAudit(request.CommandID, request.ActorID, request.At) {
		return ErrInvalidTaskCommand
	}
	switch request.Command {
	case OccurrenceCommandStart, OccurrenceCommandBlock, OccurrenceCommandUnblock, OccurrenceCommandComplete,
		OccurrenceCommandSkip, OccurrenceCommandCancel, OccurrenceCommandReopen:
		return nil
	default:
		return fmt.Errorf("%w: unsupported occurrence command %q", ErrInvalidTaskCommand, request.Command)
	}
}

func newOccurrenceCommandResult(
	aggregate TaskAggregate,
	scheduleRevision int64,
	occurrence Occurrence,
	log ExecutionLog,
	request OccurrenceCommandRequest,
) OccurrenceCommandResult {
	return OccurrenceCommandResult{
		taskRevision:        aggregate.Revision,
		scheduleRevision:    scheduleRevision,
		occurrenceRevision:  occurrence.Revision,
		taskLifecycleStatus: aggregate.LifecycleStatus,
		executionStatus:     occurrence.ExecutionStatus,
		executionLog:        log,
		audit: OccurrenceCommandAudit{
			commandID:    request.CommandID,
			command:      request.Command,
			taskID:       request.TaskID,
			occurrenceID: request.OccurrenceID,
			actorID:      request.ActorID,
			createdAt:    request.At,
		},
	}
}
