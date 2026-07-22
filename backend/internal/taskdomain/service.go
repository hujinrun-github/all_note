package taskdomain

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

var (
	ErrInvalidTaskCommand         = errors.New("invalid task-domain command")
	ErrTaskRuntimeEpochConflict   = errors.New("task-domain runtime epoch conflict")
	ErrTaskRevisionConflict       = errors.New("task revision conflict")
	ErrScheduleRevisionConflict   = errors.New("schedule revision conflict")
	ErrOccurrenceRevisionConflict = errors.New("occurrence revision conflict")
)

type TaskLifecycleCommand string

const (
	TaskCommandCreate  TaskLifecycleCommand = "create"
	TaskCommandPatch   TaskLifecycleCommand = "patch"
	TaskCommandPublish TaskLifecycleCommand = "publish"
	TaskCommandPause   TaskLifecycleCommand = "pause"
	TaskCommandResume  TaskLifecycleCommand = "resume"
	TaskCommandCancel  TaskLifecycleCommand = "cancel"
	TaskCommandRestore TaskLifecycleCommand = "restore"
	TaskCommandArchive TaskLifecycleCommand = "archive"
)

// TaskDomainCommandFencer is the task-domain adapter surface over a tenant
// fenced writer. Implementations must validate and lock expectedRuntimeEpoch
// before invoking the callback.
type TaskDomainCommandFencer interface {
	BeginFencedWrite(context.Context, string, int64, func(TaskDomainFencedTx) error) error
}

// TaskAggregateState is the complete command input read while the workspace
// fence is held. ScheduleRevision remains independent from Task.Revision.
type TaskAggregateState struct {
	Task             TaskRecord
	Aggregate        TaskAggregate
	ScheduleRevision int64
}

// TaskAggregateStateReader is intentionally narrower than a general runtime
// reader. SaveTaskAggregate still performs CAS using all expected revisions.
type TaskAggregateStateReader interface {
	GetTaskAggregateState(context.Context, string) (TaskAggregateState, error)
}

type TaskService struct {
	fencer TaskDomainCommandFencer
	reader TaskAggregateStateReader
}

func NewTaskService(fencer TaskDomainCommandFencer, reader TaskAggregateStateReader) *TaskService {
	return &TaskService{fencer: fencer, reader: reader}
}

type CreateTaskRequest struct {
	WorkspaceID          string
	ExpectedRuntimeEpoch int64
	Snapshot             TaskAggregateSnapshot
	CommandID            string
	ActorID              string
	At                   time.Time
}

type LifecycleExpectedRevisions struct {
	Task        int64
	Schedule    int64
	Occurrences map[string]int64
}

type LifecycleCommandRequest struct {
	WorkspaceID          string
	TaskID               string
	Command              TaskLifecycleCommand
	ExpectedRuntimeEpoch int64
	Expected             LifecycleExpectedRevisions
	CommandID            string
	ActorID              string
	At                   time.Time
}

type TaskAttributePatch struct {
	Title       *string
	Description *string
	Priority    *int
	SortOrder   *float64
	Project     *ProjectIdentity
	RoadmapSet  bool
	Roadmap     *Roadmap
	TaskNoteSet bool
	TaskNote    *TaskNoteIdentity
}

type PatchTaskRequest struct {
	WorkspaceID              string
	TaskID                   string
	ExpectedRuntimeEpoch     int64
	ExpectedTaskRevision     int64
	ExpectedScheduleRevision int64
	Patch                    TaskAttributePatch
	CommandID                string
	ActorID                  string
	At                       time.Time
}

// TaskCommandAudit is the immutable audit fact returned by a successful
// domain command. A persistence adapter may later store this fact without
// changing service orchestration or allowing a handler to assemble writes.
type TaskCommandAudit struct {
	commandID string
	command   TaskLifecycleCommand
	taskID    string
	actorID   string
	createdAt time.Time
}

func (audit TaskCommandAudit) CommandID() string             { return audit.commandID }
func (audit TaskCommandAudit) Command() TaskLifecycleCommand { return audit.command }
func (audit TaskCommandAudit) TaskID() string                { return audit.taskID }
func (audit TaskCommandAudit) ActorID() string               { return audit.actorID }
func (audit TaskCommandAudit) CreatedAt() time.Time          { return audit.createdAt }

type TaskCommandResult struct {
	task                TaskRecord
	taskRevision        int64
	scheduleRevision    int64
	lifecycleStatus     TaskLifecycleStatus
	occurrenceRevisions map[string]int64
	executionLogs       []ExecutionLog
	audit               TaskCommandAudit
}

func (result TaskCommandResult) TaskRevision() int64                  { return result.taskRevision }
func (result TaskCommandResult) Task() TaskRecord                     { return result.task }
func (result TaskCommandResult) ScheduleRevision() int64              { return result.scheduleRevision }
func (result TaskCommandResult) LifecycleStatus() TaskLifecycleStatus { return result.lifecycleStatus }
func (result TaskCommandResult) Audit() TaskCommandAudit              { return result.audit }
func (result TaskCommandResult) OccurrenceRevisions() map[string]int64 {
	revisions := make(map[string]int64, len(result.occurrenceRevisions))
	for occurrenceID, revision := range result.occurrenceRevisions {
		revisions[occurrenceID] = revision
	}
	return revisions
}
func (result TaskCommandResult) ExecutionLogs() []ExecutionLog {
	return append([]ExecutionLog(nil), result.executionLogs...)
}
func (result TaskCommandResult) IsZero() bool {
	return result.task == (TaskRecord{}) && result.taskRevision == 0 && result.scheduleRevision == 0 && result.lifecycleStatus == "" &&
		len(result.occurrenceRevisions) == 0 && len(result.executionLogs) == 0 && result.audit == (TaskCommandAudit{})
}

func (service *TaskService) CreateTask(ctx context.Context, request CreateTaskRequest) (TaskCommandResult, error) {
	if service == nil || service.fencer == nil || strings.TrimSpace(request.WorkspaceID) == "" || request.ExpectedRuntimeEpoch < 1 ||
		request.Snapshot.Task.WorkspaceID != request.WorkspaceID || !validCommandAudit(request.CommandID, request.ActorID, request.At) {
		return TaskCommandResult{}, ErrInvalidTaskCommand
	}
	if err := ValidateTaskAggregateSnapshot(request.Snapshot); err != nil {
		return TaskCommandResult{}, err
	}

	var result TaskCommandResult
	err := service.fencer.BeginFencedWrite(ctx, request.WorkspaceID, request.ExpectedRuntimeEpoch, func(tx TaskDomainFencedTx) error {
		if tx == nil || tx.TaskDomainWriter() == nil {
			return ErrInvalidTaskCommand
		}
		if err := tx.TaskDomainWriter().CreateTaskAggregate(ctx, request.Snapshot); err != nil {
			return err
		}
		result = newTaskCommandResult(
			request.Snapshot.Task.Revision,
			request.Snapshot.Schedule.Revision,
			request.Snapshot.Task.LifecycleStatus,
			map[string]int64{},
			nil,
			TaskCommandCreate,
			request.CommandID,
			request.Snapshot.Task.ID,
			request.ActorID,
			request.At,
		)
		return nil
	})
	if err != nil {
		return TaskCommandResult{}, err
	}
	return result, nil
}

func (service *TaskService) PatchTask(ctx context.Context, request PatchTaskRequest) (TaskCommandResult, error) {
	if err := validatePatchTaskRequest(service, request); err != nil {
		return TaskCommandResult{}, err
	}

	var result TaskCommandResult
	err := service.fencer.BeginFencedWrite(ctx, request.WorkspaceID, request.ExpectedRuntimeEpoch, func(tx TaskDomainFencedTx) error {
		if tx == nil || tx.TaskDomainWriter() == nil {
			return ErrInvalidTaskCommand
		}
		state, err := service.reader.GetTaskAggregateState(ctx, request.TaskID)
		if err != nil {
			return err
		}
		if state.Task.WorkspaceID != request.WorkspaceID || state.Task.ID != request.TaskID ||
			state.Aggregate.WorkspaceID != request.WorkspaceID || state.Aggregate.TaskID != request.TaskID ||
			state.Task.Revision != state.Aggregate.Revision || state.Task.LifecycleStatus != state.Aggregate.LifecycleStatus {
			return ErrInvalidTaskCommand
		}
		if state.Task.Revision != request.ExpectedTaskRevision {
			return ErrTaskRevisionConflict
		}
		if state.ScheduleRevision != request.ExpectedScheduleRevision {
			return ErrScheduleRevisionConflict
		}

		after, err := applyTaskAttributePatch(state.Task, request.WorkspaceID, request.Patch)
		if err != nil {
			return err
		}
		after.Revision++
		nextAggregate := cloneTaskAggregate(state.Aggregate)
		nextAggregate.Revision = after.Revision
		write := TaskAggregateWrite{
			Task: &after, Aggregate: nextAggregate,
			ExpectedRevisions:        AggregateExpectedRevisions{Task: request.ExpectedTaskRevision, Occurrences: map[string]int64{}},
			ExpectedScheduleRevision: request.ExpectedScheduleRevision,
		}
		if err := tx.TaskDomainWriter().SaveTaskAggregate(ctx, write); err != nil {
			return err
		}
		result = newTaskCommandResult(
			after.Revision, state.ScheduleRevision, after.LifecycleStatus, map[string]int64{}, nil,
			TaskCommandPatch, request.CommandID, request.TaskID, request.ActorID, request.At,
		)
		result.task = after
		return nil
	})
	if err != nil {
		return TaskCommandResult{}, err
	}
	return result, nil
}

func validatePatchTaskRequest(service *TaskService, request PatchTaskRequest) error {
	if service == nil || service.fencer == nil || service.reader == nil || strings.TrimSpace(request.WorkspaceID) == "" ||
		strings.TrimSpace(request.TaskID) == "" || request.ExpectedRuntimeEpoch < 1 || request.ExpectedTaskRevision < 1 ||
		request.ExpectedScheduleRevision < 1 || !validCommandAudit(request.CommandID, request.ActorID, request.At) ||
		!taskAttributePatchHasChanges(request.Patch) {
		return ErrInvalidTaskCommand
	}
	patch := request.Patch
	if patch.Title != nil && strings.TrimSpace(*patch.Title) == "" {
		return ErrInvalidTaskCommand
	}
	if patch.Priority != nil && (*patch.Priority < 0 || *patch.Priority > 3) {
		return ErrInvalidTaskCommand
	}
	if patch.SortOrder != nil && (math.IsNaN(*patch.SortOrder) || math.IsInf(*patch.SortOrder, 0)) {
		return ErrInvalidTaskCommand
	}
	if patch.Project != nil && (patch.Project.WorkspaceID != request.WorkspaceID || strings.TrimSpace(patch.Project.ProjectID) == "") {
		return ErrInvalidTaskCommand
	}
	if !patch.RoadmapSet && patch.Roadmap != nil {
		return ErrInvalidTaskCommand
	}
	if patch.Roadmap != nil && (patch.Roadmap.WorkspaceID != request.WorkspaceID || strings.TrimSpace(patch.Roadmap.ID) == "" || strings.TrimSpace(patch.Roadmap.ProjectID) == "") {
		return ErrInvalidTaskCommand
	}
	if !patch.TaskNoteSet && patch.TaskNote != nil {
		return ErrInvalidTaskCommand
	}
	if patch.TaskNote != nil && (patch.TaskNote.WorkspaceID != request.WorkspaceID || strings.TrimSpace(patch.TaskNote.NoteID) == "") {
		return ErrInvalidTaskCommand
	}
	return nil
}

func taskAttributePatchHasChanges(patch TaskAttributePatch) bool {
	return patch.Title != nil || patch.Description != nil || patch.Priority != nil || patch.SortOrder != nil ||
		patch.Project != nil || patch.RoadmapSet || patch.TaskNoteSet
}

func applyTaskAttributePatch(current TaskRecord, workspaceID string, patch TaskAttributePatch) (TaskRecord, error) {
	after := current
	if patch.Title != nil {
		after.Title = strings.TrimSpace(*patch.Title)
	}
	if patch.Description != nil {
		after.Description = *patch.Description
	}
	if patch.Priority != nil {
		after.Priority = *patch.Priority
	}
	if patch.SortOrder != nil {
		after.SortOrder = *patch.SortOrder
	}
	if patch.Project != nil {
		after.ProjectID = patch.Project.ProjectID
		if current.ProjectID != after.ProjectID && current.RoadmapNodeID != "" && !patch.RoadmapSet {
			return current, ErrInvalidTaskCommand
		}
	}
	if patch.RoadmapSet {
		after.RoadmapNodeID = ""
		if patch.Roadmap != nil {
			if patch.Roadmap.WorkspaceID != workspaceID || patch.Roadmap.ProjectID != after.ProjectID {
				return current, ErrInvalidTaskCommand
			}
			after.RoadmapNodeID = patch.Roadmap.ID
		}
	}
	if patch.TaskNoteSet {
		after.NoteID = ""
		if patch.TaskNote != nil {
			if patch.TaskNote.WorkspaceID != workspaceID {
				return current, ErrInvalidTaskCommand
			}
			after.NoteID = patch.TaskNote.NoteID
		}
	}
	return after, nil
}

func (service *TaskService) ExecuteLifecycleCommand(ctx context.Context, request LifecycleCommandRequest) (TaskCommandResult, error) {
	if err := validateLifecycleCommandRequest(service, request); err != nil {
		return TaskCommandResult{}, err
	}

	var result TaskCommandResult
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

		next, logs, expectedOccurrences, err := executeLifecycleTransition(state.Aggregate, request)
		if err != nil {
			return err
		}
		write := TaskAggregateWrite{
			Aggregate: next,
			ExpectedRevisions: AggregateExpectedRevisions{
				Task:        request.Expected.Task,
				Occurrences: expectedOccurrences,
			},
			ExpectedScheduleRevision: request.Expected.Schedule,
			ExecutionLogs:            logs,
		}
		if err := tx.TaskDomainWriter().SaveTaskAggregate(ctx, write); err != nil {
			return err
		}
		result = newTaskCommandResult(
			next.Revision,
			state.ScheduleRevision,
			next.LifecycleStatus,
			occurrenceRevisionsAfter(next, expectedOccurrences),
			logs,
			request.Command,
			request.CommandID,
			request.TaskID,
			request.ActorID,
			request.At,
		)
		return nil
	})
	if err != nil {
		return TaskCommandResult{}, err
	}
	return result, nil
}

func executeLifecycleTransition(current TaskAggregate, request LifecycleCommandRequest) (TaskAggregate, []ExecutionLog, map[string]int64, error) {
	if request.Command == TaskCommandCancel {
		expectedOccurrences, err := validateCancelOccurrenceRevisions(current, request.Expected.Occurrences)
		if err != nil {
			return current, nil, nil, err
		}
		transitions := make(map[string]ExecutionTransition, len(expectedOccurrences))
		occurrenceIDs := make([]string, 0, len(expectedOccurrences))
		for occurrenceID := range expectedOccurrences {
			occurrenceIDs = append(occurrenceIDs, occurrenceID)
		}
		sort.Strings(occurrenceIDs)
		for _, occurrenceID := range occurrenceIDs {
			transitions[occurrenceID] = ExecutionTransition{
				LogID:   request.CommandID + ":" + occurrenceID,
				ActorID: request.ActorID,
				At:      request.At,
			}
		}
		next, logs, err := CancelTaskAggregate(
			current,
			AggregateExpectedRevisions{Task: request.Expected.Task, Occurrences: expectedOccurrences},
			transitions,
		)
		return next, logs, expectedOccurrences, err
	}
	if len(request.Expected.Occurrences) != 0 {
		return current, nil, nil, ErrOccurrenceRevisionConflict
	}

	if request.Command == TaskCommandPause {
		next, logs, err := PauseTaskAggregate(current, request.Expected.Task)
		return next, logs, map[string]int64{}, err
	}

	nextStatus, err := lifecycleStatusTransition(request.Command, current.LifecycleStatus)
	if err != nil {
		return current, nil, nil, err
	}
	next := cloneTaskAggregate(current)
	next.LifecycleStatus = nextStatus
	next.Revision++
	switch request.Command {
	case TaskCommandPublish, TaskCommandResume, TaskCommandRestore:
		next.GenerationEnabled = next.Recurring
	case TaskCommandArchive:
		next.GenerationEnabled = false
	}
	return next, nil, map[string]int64{}, nil
}

func lifecycleStatusTransition(command TaskLifecycleCommand, current TaskLifecycleStatus) (TaskLifecycleStatus, error) {
	switch command {
	case TaskCommandPublish:
		return PublishTask(current)
	case TaskCommandResume:
		return ResumeTask(current)
	case TaskCommandRestore:
		return RestoreTask(current)
	case TaskCommandArchive:
		return ArchiveTask(current)
	default:
		return current, ErrInvalidTaskCommand
	}
}

func validateCancelOccurrenceRevisions(current TaskAggregate, expected map[string]int64) (map[string]int64, error) {
	affected := make(map[string]int64)
	for _, occurrence := range current.Occurrences {
		if isTerminalExecutionStatus(occurrence.ExecutionStatus) {
			continue
		}
		revision, exists := expected[occurrence.ID]
		if !exists || revision != occurrence.Revision {
			return nil, ErrOccurrenceRevisionConflict
		}
		affected[occurrence.ID] = revision
	}
	if len(affected) != len(expected) {
		return nil, ErrOccurrenceRevisionConflict
	}
	return affected, nil
}

func validateLifecycleCommandRequest(service *TaskService, request LifecycleCommandRequest) error {
	if service == nil || service.fencer == nil || service.reader == nil || strings.TrimSpace(request.WorkspaceID) == "" || strings.TrimSpace(request.TaskID) == "" ||
		request.ExpectedRuntimeEpoch < 1 || request.Expected.Task < 1 || request.Expected.Schedule < 1 || !validCommandAudit(request.CommandID, request.ActorID, request.At) {
		return ErrInvalidTaskCommand
	}
	switch request.Command {
	case TaskCommandPublish, TaskCommandPause, TaskCommandResume, TaskCommandCancel, TaskCommandRestore, TaskCommandArchive:
		return nil
	default:
		return fmt.Errorf("%w: unsupported lifecycle command %q", ErrInvalidTaskCommand, request.Command)
	}
}

func validCommandAudit(commandID, actorID string, at time.Time) bool {
	return strings.TrimSpace(commandID) != "" && strings.TrimSpace(actorID) != "" && !at.IsZero()
}

func occurrenceRevisionsAfter(aggregate TaskAggregate, affected map[string]int64) map[string]int64 {
	revisions := make(map[string]int64, len(affected))
	for _, occurrence := range aggregate.Occurrences {
		if _, exists := affected[occurrence.ID]; exists {
			revisions[occurrence.ID] = occurrence.Revision
		}
	}
	return revisions
}

func newTaskCommandResult(
	taskRevision int64,
	scheduleRevision int64,
	lifecycleStatus TaskLifecycleStatus,
	occurrenceRevisions map[string]int64,
	logs []ExecutionLog,
	command TaskLifecycleCommand,
	commandID string,
	taskID string,
	actorID string,
	createdAt time.Time,
) TaskCommandResult {
	revisions := make(map[string]int64, len(occurrenceRevisions))
	for occurrenceID, revision := range occurrenceRevisions {
		revisions[occurrenceID] = revision
	}
	return TaskCommandResult{
		taskRevision:        taskRevision,
		scheduleRevision:    scheduleRevision,
		lifecycleStatus:     lifecycleStatus,
		occurrenceRevisions: revisions,
		executionLogs:       append([]ExecutionLog(nil), logs...),
		audit: TaskCommandAudit{
			commandID: commandID,
			command:   command,
			taskID:    taskID,
			actorID:   actorID,
			createdAt: createdAt,
		},
	}
}
