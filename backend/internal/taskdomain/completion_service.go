package taskdomain

import (
	"context"
	"errors"
	"strings"
	"time"
)

var ErrInvalidCompletionCommand = errors.New("invalid recurring completion command")

// CompletionCommandFencer is deliberately independent from read runtimes.
// Implementations validate and lock the workspace epoch before exposing a tx
// that owns both the completion state read and aggregate writer.
type CompletionCommandFencer interface {
	BeginFencedCompletionWrite(context.Context, string, int64, func(CompletionCommandTx) error) error
}

type CompletionCommandTx interface {
	LoadRecurringCompletionState(context.Context, string) (RecurringCompletionCommandState, error)
	TaskDomainWriter() TaskDomainWriter
}

type RecurringCompletionCommandState struct {
	Aggregate           TaskAggregate
	ScheduleRevision    int64
	GenerationWatermark string
	GenerationStatus    GenerationStatus
	RetryPendingJobs    int
	FailedJobs          int
	ScheduleVersions    []CompletionScheduleVersion
}

type CompletionCommandRequest struct {
	WorkspaceID              string
	TaskID                   string
	ExpectedRuntimeEpoch     int64
	ExpectedTaskRevision     int64
	ExpectedScheduleRevision int64
	Now                      time.Time
}

type CompletionCommandResult struct {
	changed          bool
	lifecycleStatus  TaskLifecycleStatus
	taskRevision     int64
	scheduleRevision int64
}

func (result CompletionCommandResult) Changed() bool { return result.changed }
func (result CompletionCommandResult) LifecycleStatus() TaskLifecycleStatus {
	return result.lifecycleStatus
}
func (result CompletionCommandResult) TaskRevision() int64     { return result.taskRevision }
func (result CompletionCommandResult) ScheduleRevision() int64 { return result.scheduleRevision }
func (result CompletionCommandResult) IsZero() bool {
	return !result.changed && result.lifecycleStatus == "" && result.taskRevision == 0 && result.scheduleRevision == 0
}

type CompletionService struct {
	fencer CompletionCommandFencer
}

func NewCompletionService(fencer CompletionCommandFencer) *CompletionService {
	return &CompletionService{fencer: fencer}
}

func (service *CompletionService) Evaluate(ctx context.Context, request CompletionCommandRequest) (CompletionCommandResult, error) {
	if err := validateCompletionCommandRequest(service, request); err != nil {
		return CompletionCommandResult{}, err
	}

	var result CompletionCommandResult
	err := service.fencer.BeginFencedCompletionWrite(ctx, request.WorkspaceID, request.ExpectedRuntimeEpoch, func(tx CompletionCommandTx) error {
		if tx == nil {
			return ErrInvalidCompletionCommand
		}
		writer := tx.TaskDomainWriter()
		if writer == nil {
			return ErrInvalidCompletionCommand
		}
		state, err := tx.LoadRecurringCompletionState(ctx, request.TaskID)
		if err != nil {
			return err
		}
		if err := validateRecurringCompletionState(state, request); err != nil {
			return err
		}

		nextStatus, err := EvaluateRecurringTaskNaturalCompletion(completionEvaluationSnapshot(state, request.Now))
		if err != nil {
			return err
		}
		if nextStatus == state.Aggregate.LifecycleStatus {
			result = CompletionCommandResult{
				lifecycleStatus: state.Aggregate.LifecycleStatus,
				taskRevision:    state.Aggregate.Revision, scheduleRevision: state.ScheduleRevision,
			}
			return nil
		}

		next := cloneTaskAggregate(state.Aggregate)
		next.LifecycleStatus = nextStatus
		next.Revision++
		write := TaskAggregateWrite{
			Aggregate: next,
			ExpectedRevisions: AggregateExpectedRevisions{
				Task:        request.ExpectedTaskRevision,
				Occurrences: map[string]int64{},
			},
			ExpectedScheduleRevision: request.ExpectedScheduleRevision,
			ExecutionLogs:            nil,
		}
		if err := writer.SaveTaskAggregate(ctx, write); err != nil {
			return err
		}
		result = CompletionCommandResult{
			changed: true, lifecycleStatus: nextStatus,
			taskRevision: next.Revision, scheduleRevision: state.ScheduleRevision,
		}
		return nil
	})
	if err != nil {
		return CompletionCommandResult{}, err
	}
	return result, nil
}

func validateCompletionCommandRequest(service *CompletionService, request CompletionCommandRequest) error {
	if service == nil || service.fencer == nil || strings.TrimSpace(request.WorkspaceID) == "" ||
		strings.TrimSpace(request.TaskID) == "" || request.ExpectedRuntimeEpoch < 1 ||
		request.ExpectedTaskRevision < 1 || request.ExpectedScheduleRevision < 1 || request.Now.IsZero() {
		return ErrInvalidCompletionCommand
	}
	return nil
}

func validateRecurringCompletionState(state RecurringCompletionCommandState, request CompletionCommandRequest) error {
	if state.Aggregate.WorkspaceID != request.WorkspaceID || state.Aggregate.TaskID != request.TaskID || !state.Aggregate.Recurring {
		return ErrInvalidCompletionCommand
	}
	if state.Aggregate.Revision != request.ExpectedTaskRevision {
		return ErrTaskRevisionConflict
	}
	if state.ScheduleRevision != request.ExpectedScheduleRevision {
		return ErrScheduleRevisionConflict
	}
	return nil
}

func completionEvaluationSnapshot(state RecurringCompletionCommandState, now time.Time) RecurringTaskCompletionSnapshot {
	versions := make([]CompletionScheduleVersion, len(state.ScheduleVersions))
	copy(versions, state.ScheduleVersions)
	occurrences := make([]CompletionOccurrence, len(state.Aggregate.Occurrences))
	for index, occurrence := range state.Aggregate.Occurrences {
		occurrences[index] = CompletionOccurrence{Key: occurrence.OccurrenceKey, Status: occurrence.ExecutionStatus}
	}
	return RecurringTaskCompletionSnapshot{
		LifecycleStatus:     state.Aggregate.LifecycleStatus,
		Now:                 now,
		GenerationWatermark: state.GenerationWatermark,
		GenerationStatus:    state.GenerationStatus,
		RetryPendingJobs:    state.RetryPendingJobs,
		FailedJobs:          state.FailedJobs,
		ScheduleVersions:    versions,
		Occurrences:         occurrences,
	}
}
