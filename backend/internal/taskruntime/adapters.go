package taskruntime

import (
	"context"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/taskapp"
	"github.com/hujinrun/flowspace/internal/taskdomain"
)

// GenerationNudger is a control-plane post-commit hint. Implementations must
// never be called from inside the tenant fenced transaction; periodic durable
// reconciliation remains the correctness fallback when a hint fails.
type GenerationNudger interface {
	Nudge(context.Context, string, int64, time.Time) error
}

type taskServiceDelegate interface {
	CreateTask(context.Context, taskdomain.CreateTaskRequest) (taskdomain.TaskCommandResult, error)
	PatchTask(context.Context, taskdomain.PatchTaskRequest) (taskdomain.TaskCommandResult, error)
	ExecuteLifecycleCommand(context.Context, taskdomain.LifecycleCommandRequest) (taskdomain.TaskCommandResult, error)
}

type taskServiceAdapter struct {
	delegate   taskServiceDelegate
	generation GenerationNudger
}

func (a taskServiceAdapter) CreateTask(ctx context.Context, request taskdomain.CreateTaskRequest) (taskapp.TaskCommandOutcome, error) {
	result, err := a.delegate.CreateTask(ctx, request)
	if err == nil && a.generation != nil && recurringTaskSnapshot(request.Snapshot) {
		// The tenant transaction has committed before the domain service returns.
		// Do not turn a committed command into a client-visible failure if the
		// best-effort control-plane hint is unavailable.
		_ = a.generation.Nudge(ctx, request.WorkspaceID, request.ExpectedRuntimeEpoch, request.At)
	}
	return taskCommandOutcome(result), err
}

func recurringTaskSnapshot(snapshot taskdomain.TaskAggregateSnapshot) bool {
	for _, version := range snapshot.Versions {
		if version.RecurrenceType != taskdomain.RecurrenceNone {
			return true
		}
	}
	return false
}

func (a taskServiceAdapter) PatchTask(ctx context.Context, request taskdomain.PatchTaskRequest) (taskapp.TaskCommandOutcome, error) {
	result, err := a.delegate.PatchTask(ctx, request)
	return taskCommandOutcome(result), err
}

func (a taskServiceAdapter) ExecuteLifecycleCommand(ctx context.Context, request taskdomain.LifecycleCommandRequest) (taskapp.TaskCommandOutcome, error) {
	result, err := a.delegate.ExecuteLifecycleCommand(ctx, request)
	return taskCommandOutcome(result), err
}

func taskCommandOutcome(result taskdomain.TaskCommandResult) taskapp.TaskCommandOutcome {
	return taskapp.TaskCommandOutcome{
		Task: result.Task(), TaskRevision: result.TaskRevision(), ScheduleRevision: result.ScheduleRevision(),
		LifecycleStatus: result.LifecycleStatus(), OccurrenceRevisions: result.OccurrenceRevisions(),
		CommandID: result.Audit().CommandID(),
	}
}

type occurrenceServiceAdapter struct{ delegate *taskdomain.OccurrenceService }

func (a occurrenceServiceAdapter) Execute(ctx context.Context, request taskdomain.OccurrenceCommandRequest) (taskapp.OccurrenceCommandOutcome, error) {
	result, err := a.delegate.Execute(ctx, request)
	return taskapp.OccurrenceCommandOutcome{
		TaskRevision: result.TaskRevision(), ScheduleRevision: result.ScheduleRevision(),
		OccurrenceRevision: result.OccurrenceRevision(), TaskLifecycleStatus: result.TaskLifecycleStatus(),
		ExecutionStatus: result.ExecutionStatus(), CommandID: result.Audit().CommandID(),
	}, err
}

type projectServiceAdapter struct{ delegate *taskdomain.ProjectService }

func (a projectServiceAdapter) CreateProject(ctx context.Context, request taskdomain.CreateProjectRequest) (taskapp.ProjectCommandOutcome, error) {
	result, err := a.delegate.CreateProject(ctx, request)
	return projectCommandOutcome(result), err
}

func (a projectServiceAdapter) UpdateProject(ctx context.Context, request taskdomain.UpdateProjectRequest) (taskapp.ProjectCommandOutcome, error) {
	result, err := a.delegate.UpdateProject(ctx, request)
	return projectCommandOutcome(result), err
}

func (a projectServiceAdapter) CompleteProject(ctx context.Context, request taskdomain.ExistingProjectRequest) (taskapp.ProjectCommandOutcome, error) {
	result, err := a.delegate.CompleteProject(ctx, request)
	return projectCommandOutcome(result), err
}

func (a projectServiceAdapter) ArchiveProject(ctx context.Context, request taskdomain.ExistingProjectRequest) (taskapp.ProjectCommandOutcome, error) {
	result, err := a.delegate.ArchiveProject(ctx, request)
	return projectCommandOutcome(result), err
}

func (a projectServiceAdapter) DeleteProject(ctx context.Context, request taskdomain.ExistingProjectRequest) (taskapp.ProjectCommandOutcome, error) {
	result, err := a.delegate.DeleteProject(ctx, request)
	return projectCommandOutcome(result), err
}

func projectCommandOutcome(result taskdomain.ProjectCommandResult) taskapp.ProjectCommandOutcome {
	return taskapp.ProjectCommandOutcome{
		Project: result.Project(), Revision: result.Revision(), Deleted: result.Deleted(),
		CommandID: result.Audit().CommandID(),
	}
}

type scheduleServiceDelegate interface {
	RescheduleOccurrence(context.Context, taskdomain.RescheduleOccurrenceRequest) (taskdomain.ScheduleCommandResult, error)
	RescheduleThisAndFuture(context.Context, taskdomain.RescheduleThisAndFutureRequest) (taskdomain.ScheduleCommandResult, error)
}

type scheduleServiceAdapter struct {
	delegate   scheduleServiceDelegate
	generation GenerationNudger
}

func (a scheduleServiceAdapter) RescheduleOccurrence(ctx context.Context, request taskdomain.RescheduleOccurrenceRequest, metadata taskapp.CommandMetadata) (taskapp.ScheduleCommandOutcome, error) {
	if strings.TrimSpace(metadata.ActorID) == "" || strings.TrimSpace(metadata.CommandID) == "" || metadata.At.IsZero() {
		return taskapp.ScheduleCommandOutcome{}, taskdomain.ErrInvalidScheduleCommand
	}
	result, err := a.delegate.RescheduleOccurrence(ctx, request)
	return scheduleCommandOutcome(result, metadata.CommandID), err
}

func (a scheduleServiceAdapter) RescheduleThisAndFollowing(ctx context.Context, request taskdomain.RescheduleThisAndFutureRequest, metadata taskapp.CommandMetadata) (taskapp.ScheduleCommandOutcome, error) {
	if strings.TrimSpace(metadata.ActorID) == "" || strings.TrimSpace(metadata.CommandID) == "" || metadata.At.IsZero() {
		return taskapp.ScheduleCommandOutcome{}, taskdomain.ErrInvalidScheduleCommand
	}
	result, err := a.delegate.RescheduleThisAndFuture(ctx, request)
	if err == nil && a.generation != nil {
		_ = a.generation.Nudge(ctx, request.WorkspaceID, request.ExpectedRuntimeEpoch, metadata.At)
	}
	return scheduleCommandOutcome(result, metadata.CommandID), err
}

func scheduleCommandOutcome(result taskdomain.ScheduleCommandResult, commandID string) taskapp.ScheduleCommandOutcome {
	return taskapp.ScheduleCommandOutcome{
		TaskRevision: result.TaskRevision(), ScheduleRevision: result.ScheduleRevision(),
		OccurrenceRevision: result.OccurrenceRevision(), ScheduleVersion: result.ScheduleVersion(),
		Candidates: result.Candidates(), CommandID: commandID,
	}
}

var _ taskapp.TaskService = taskServiceAdapter{}
var _ taskapp.OccurrenceService = occurrenceServiceAdapter{}
var _ taskapp.ProjectService = projectServiceAdapter{}
var _ taskapp.ScheduleService = scheduleServiceAdapter{}
