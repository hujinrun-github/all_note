package mobilev2service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hujinrun/flowspace/internal/handler"
	"github.com/hujinrun/flowspace/internal/mobilev2command"
	"github.com/hujinrun/flowspace/internal/mobilev2sync"
	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/taskapp"
	"github.com/hujinrun/flowspace/internal/taskdomain"
)

type projectCreatePayload struct {
	Name    string                    `json:"name"`
	Kind    taskdomain.ProjectKind    `json:"kind"`
	Horizon taskdomain.ProjectHorizon `json:"horizon"`
	Status  taskdomain.ProjectStatus  `json:"status"`
}

type projectUpdatePayload struct {
	Name    *string                    `json:"name"`
	Kind    *taskdomain.ProjectKind    `json:"kind"`
	Horizon *taskdomain.ProjectHorizon `json:"horizon"`
}

type projectRestorePayload struct {
	RestoreTo *taskdomain.ProjectStatus `json:"restore_to"`
}

type scheduleCommandPayload struct {
	RecurrenceType  taskdomain.RecurrenceType `json:"recurrence_type"`
	TimingType      taskdomain.TimingType     `json:"timing_type"`
	Timezone        string                    `json:"timezone"`
	StartsOn        *string                   `json:"starts_on"`
	EndsOn          *string                   `json:"ends_on"`
	Rule            json.RawMessage           `json:"rule"`
	LocalStartTime  *string                   `json:"local_start_time"`
	DurationMinutes *int                      `json:"duration_minutes"`
}

func (payload scheduleCommandPayload) domainInput() taskdomain.ScheduleInput {
	return taskdomain.ScheduleInput{
		RecurrenceType: payload.RecurrenceType, TimingType: payload.TimingType,
		Timezone: payload.Timezone, StartsOn: optionalValue(payload.StartsOn),
		EndsOn: optionalValue(payload.EndsOn), Rule: append(json.RawMessage(nil), payload.Rule...),
		LocalStartTime:  optionalValue(payload.LocalStartTime),
		DurationMinutes: optionalIntValue(payload.DurationMinutes),
	}
}

type taskCreatePayload struct {
	InitialOccurrenceClientID *string                        `json:"initial_occurrence_client_id"`
	Project                   mobilev2command.EnvelopeTarget `json:"project"`
	RoadmapNodeID             *string                        `json:"roadmap_node_id"`
	NoteID                    *string                        `json:"note_id"`
	Title                     string                         `json:"title"`
	Description               string                         `json:"description"`
	Priority                  int                            `json:"priority"`
	SortOrder                 float64                        `json:"sort_order"`
	Schedule                  scheduleCommandPayload         `json:"schedule"`
	AllDayEndDate             *string                        `json:"all_day_end_date"`
	DueAt                     *string                        `json:"due_at"`
	SelectedOffsets           map[string]int                 `json:"selected_offsets"`
}

type nullablePatchString struct {
	Set   bool
	Value *string
}

func (value *nullablePatchString) UnmarshalJSON(data []byte) error {
	value.Set = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		value.Value = nil
		return nil
	}
	var decoded string
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	value.Value = &decoded
	return nil
}

type taskUpdatePayload struct {
	Title         *string             `json:"title"`
	Description   *string             `json:"description"`
	Priority      *int                `json:"priority"`
	SortOrder     *float64            `json:"sort_order"`
	ProjectID     *string             `json:"project_id"`
	RoadmapNodeID nullablePatchString `json:"roadmap_node_id"`
	NoteID        nullablePatchString `json:"note_id"`
}

type occurrenceBlockPayload struct {
	BlockedReason string `json:"blocked_reason"`
	NextAction    string `json:"next_action"`
}

type occurrenceTimingPayload struct {
	TimingType      *taskdomain.TimingType `json:"timing_type"`
	Timezone        string                 `json:"timezone"`
	PlannedDate     *string                `json:"planned_date"`
	AllDayEndDate   *string                `json:"all_day_end_date"`
	LocalStartTime  *string                `json:"local_start_time"`
	DurationMinutes *int                   `json:"duration_minutes"`
	SelectedOffsets map[string]int         `json:"selected_offsets"`
	DueAt           nullablePatchString    `json:"due_at"`
}

type scheduleReschedulePayload struct {
	EffectiveFrom            string                 `json:"effective_from"`
	GenerateThroughExclusive string                 `json:"generate_through_exclusive"`
	Schedule                 scheduleCommandPayload `json:"schedule"`
	SelectedOffsets          map[string]int         `json:"selected_offsets"`
}

func dispatchTaskCommand(
	ctx context.Context,
	runtime taskapp.RuntimeSnapshot,
	identity handler.MobileV2Identity,
	envelope mobilev2command.Envelope,
	now time.Time,
) (taskCommandOutcome, error) {
	if runtime.WorkspaceID != identity.WorkspaceID || runtime.Epoch < 1 || strings.TrimSpace(identity.UserID) == "" {
		return newTaskCommandOutcome(), taskapp.ErrInvalidRuntime
	}
	switch {
	case strings.HasPrefix(envelope.CommandType, "project."):
		return dispatchProjectCommand(ctx, runtime, identity, envelope, now)
	case strings.HasPrefix(envelope.CommandType, "task."):
		return dispatchTaskAggregateCommand(ctx, runtime, identity, envelope, now)
	case strings.HasPrefix(envelope.CommandType, "occurrence."):
		return dispatchOccurrenceCommand(ctx, runtime, identity, envelope, now)
	case envelope.CommandType == "schedule.reschedule-this-and-following":
		return dispatchScheduleCommand(ctx, runtime, identity, envelope, now)
	default:
		return newTaskCommandOutcome(), mobilev2command.ErrInvalidCommandEnvelope
	}
}

func dispatchProjectCommand(
	ctx context.Context,
	runtime taskapp.RuntimeSnapshot,
	identity handler.MobileV2Identity,
	envelope mobilev2command.Envelope,
	now time.Time,
) (taskCommandOutcome, error) {
	outcome := newTaskCommandOutcome(mobilev2sync.ScopeIPhoneTaskCore)
	projectID := targetEntityID(envelope.Target)
	if envelope.CommandType == "project.create" {
		if envelope.Target.ClientID == nil {
			return outcome, mobilev2command.ErrInvalidCommandEnvelope
		}
		var payload projectCreatePayload
		if err := decodeCommandPayload(envelope.Payload, &payload); err != nil {
			return outcome, err
		}
		projectID = uuid.NewString()
		outcome.ProjectIDs[projectID] = struct{}{}
		result, err := runtime.Projects.CreateProject(ctx, taskdomain.CreateProjectRequest{
			WorkspaceID: runtime.WorkspaceID, ExpectedRuntimeEpoch: runtime.Epoch,
			ExpectedProjectRevision: 0,
			Project: taskdomain.Project{
				WorkspaceID: runtime.WorkspaceID, ID: projectID, Name: payload.Name,
				Kind: payload.Kind, Horizon: payload.Horizon, Status: payload.Status,
			},
			CommandID: envelope.CommandID, ActorID: identity.UserID, At: now,
		})
		if err != nil {
			return outcome, err
		}
		clientID, entityID := *envelope.Target.ClientID, projectID
		outcome.Result.IdentityMappings = []mobilev2command.IdentityMapping{{
			EntityType: "project", ClientID: &clientID, EntityID: &entityID,
		}}
		outcome.Result.AffectedRevisions = []mobilev2command.AffectedRevision{{
			EntityType: "project", EntityID: projectID, Revision: strconv.FormatInt(result.Revision, 10),
		}}
		return outcome, nil
	}
	if projectID == "" {
		return outcome, mobilev2command.ErrInvalidCommandEnvelope
	}
	outcome.ProjectIDs[projectID] = struct{}{}
	expected, err := envelope.Expected.Exact("project")
	if err != nil {
		return outcome, err
	}
	var result taskapp.ProjectCommandOutcome
	switch envelope.CommandType {
	case "project.update":
		var payload projectUpdatePayload
		if err := decodeCommandPayload(envelope.Payload, &payload); err != nil {
			return outcome, err
		}
		current, err := runtime.Reader.GetProject(ctx, projectID)
		if err != nil {
			return outcome, err
		}
		project := current.Project
		if payload.Name != nil {
			project.Name = *payload.Name
		}
		if payload.Kind != nil {
			project.Kind = *payload.Kind
		}
		if payload.Horizon != nil {
			project.Horizon = *payload.Horizon
		}
		result, err = runtime.Projects.UpdateProject(ctx, taskdomain.UpdateProjectRequest{
			WorkspaceID: runtime.WorkspaceID, ProjectID: projectID,
			ExpectedRuntimeEpoch: runtime.Epoch, ExpectedProjectRevision: expected,
			Project: project, CommandID: envelope.CommandID, ActorID: identity.UserID, At: now,
		})
		if err != nil {
			return outcome, err
		}
	default:
		restoreTo, err := decodeProjectLifecyclePayload(envelope)
		if err != nil {
			return outcome, err
		}
		request := taskdomain.ExistingProjectRequest{
			WorkspaceID: runtime.WorkspaceID, ProjectID: projectID,
			ExpectedRuntimeEpoch: runtime.Epoch, ExpectedProjectRevision: expected,
			CommandID: envelope.CommandID, ActorID: identity.UserID, At: now, RestoreTo: restoreTo,
		}
		switch envelope.CommandType {
		case "project.activate":
			result, err = runtime.Projects.ActivateProject(ctx, request)
		case "project.pause":
			result, err = runtime.Projects.PauseProject(ctx, request)
		case "project.resume":
			result, err = runtime.Projects.ResumeProject(ctx, request)
		case "project.complete":
			result, err = runtime.Projects.CompleteProject(ctx, request)
		case "project.archive":
			result, err = runtime.Projects.ArchiveProject(ctx, request)
		case "project.restore":
			result, err = runtime.Projects.RestoreProject(ctx, request)
		default:
			return outcome, mobilev2command.ErrInvalidCommandEnvelope
		}
		if err != nil {
			return outcome, err
		}
	}
	outcome.Result.AffectedRevisions = []mobilev2command.AffectedRevision{{
		EntityType: "project", EntityID: projectID, Revision: strconv.FormatInt(result.Revision, 10),
	}}
	return outcome, nil
}

func decodeProjectLifecyclePayload(envelope mobilev2command.Envelope) (*taskdomain.ProjectStatus, error) {
	if envelope.CommandType == "project.restore" {
		var payload projectRestorePayload
		if err := decodeCommandPayload(envelope.Payload, &payload); err != nil {
			return nil, err
		}
		return payload.RestoreTo, nil
	}
	return nil, decodeEmptyPayload(envelope.Payload)
}

func dispatchTaskAggregateCommand(
	ctx context.Context,
	runtime taskapp.RuntimeSnapshot,
	identity handler.MobileV2Identity,
	envelope mobilev2command.Envelope,
	now time.Time,
) (taskCommandOutcome, error) {
	outcome := newTaskCommandOutcome(mobilev2sync.ScopeIPhoneTaskCore)
	taskID := targetEntityID(envelope.Target)
	if envelope.CommandType == "task.create" {
		if envelope.Target.ClientID == nil {
			return outcome, mobilev2command.ErrInvalidCommandEnvelope
		}
		var payload taskCreatePayload
		if err := decodeCommandPayload(envelope.Payload, &payload); err != nil {
			return outcome, err
		}
		projectID := targetEntityID(payload.Project)
		if projectID == "" {
			return outcome, mobilev2command.ErrInvalidCommandEnvelope
		}
		taskID = uuid.NewString()
		outcome.TaskIDs[taskID] = struct{}{}
		outcome.ProjectIDs[projectID] = struct{}{}
		dueAt, err := parseOptionalInstant(payload.DueAt)
		if err != nil {
			return outcome, err
		}
		var roadmap *taskdomain.Roadmap
		if payload.RoadmapNodeID != nil && strings.TrimSpace(*payload.RoadmapNodeID) != "" {
			roadmap = &taskdomain.Roadmap{
				WorkspaceID: runtime.WorkspaceID, ID: strings.TrimSpace(*payload.RoadmapNodeID),
				ProjectID: projectID,
			}
		}
		var note *taskdomain.TaskNoteIdentity
		if payload.NoteID != nil && strings.TrimSpace(*payload.NoteID) != "" {
			note = &taskdomain.TaskNoteIdentity{WorkspaceID: runtime.WorkspaceID, NoteID: strings.TrimSpace(*payload.NoteID)}
		}
		snapshot, _, err := runtime.Factory.Build(taskdomain.TaskCreationInput{
			WorkspaceID: runtime.WorkspaceID,
			Project:     taskdomain.ProjectIdentity{WorkspaceID: runtime.WorkspaceID, ProjectID: projectID},
			Roadmap:     roadmap, TaskNote: note, TaskID: taskID, ActorID: identity.UserID, ActorTime: now,
			Title: payload.Title, Description: payload.Description, Priority: payload.Priority,
			SortOrder: payload.SortOrder, Schedule: payload.Schedule.domainInput(),
			AllDayEndDate: optionalValue(payload.AllDayEndDate), DueAt: dueAt,
			SelectedOffsets: cloneIntMap(payload.SelectedOffsets),
		})
		if err != nil {
			return outcome, err
		}
		for _, occurrence := range snapshot.Occurrences {
			outcome.OccurrenceIDs[occurrence.ID] = struct{}{}
		}
		result, err := runtime.Tasks.CreateTask(ctx, taskdomain.CreateTaskRequest{
			WorkspaceID: runtime.WorkspaceID, ExpectedRuntimeEpoch: runtime.Epoch,
			Snapshot: snapshot, CommandID: envelope.CommandID, ActorID: identity.UserID, At: now,
		})
		if err != nil {
			return outcome, err
		}
		clientID, entityID := *envelope.Target.ClientID, taskID
		outcome.Result.IdentityMappings = append(outcome.Result.IdentityMappings, mobilev2command.IdentityMapping{
			EntityType: "task", ClientID: &clientID, EntityID: &entityID,
		})
		if payload.InitialOccurrenceClientID != nil && len(snapshot.Occurrences) == 1 {
			clientOccurrenceID, occurrenceID := *payload.InitialOccurrenceClientID, snapshot.Occurrences[0].ID
			outcome.Result.IdentityMappings = append(outcome.Result.IdentityMappings, mobilev2command.IdentityMapping{
				EntityType: "task_occurrence", ClientID: &clientOccurrenceID, EntityID: &occurrenceID,
			})
		}
		outcome.Result.AffectedRevisions = taskAffectedRevisions(
			taskID, result.TaskRevision, result.ScheduleRevision, result.OccurrenceRevisions,
		)
		return outcome, nil
	}
	if taskID == "" {
		return outcome, mobilev2command.ErrInvalidCommandEnvelope
	}
	outcome.TaskIDs[taskID] = struct{}{}
	taskRevision, err := envelope.Expected.Exact("task")
	if err != nil {
		return outcome, err
	}
	scheduleRevision, err := envelope.Expected.Exact("schedule")
	if err != nil {
		return outcome, err
	}
	var result taskapp.TaskCommandOutcome
	if envelope.CommandType == "task.update" {
		var payload taskUpdatePayload
		if err := decodeCommandPayload(envelope.Payload, &payload); err != nil {
			return outcome, err
		}
		current, err := runtime.Reader.GetTaskAggregate(ctx, taskID)
		if err != nil {
			return outcome, err
		}
		patch := taskdomain.TaskAttributePatch{
			Title: payload.Title, Description: payload.Description,
			Priority: payload.Priority, SortOrder: payload.SortOrder,
		}
		projectID := current.Task.ProjectID
		if payload.ProjectID != nil {
			projectID = strings.TrimSpace(*payload.ProjectID)
			patch.Project = &taskdomain.ProjectIdentity{WorkspaceID: runtime.WorkspaceID, ProjectID: projectID}
		}
		if payload.RoadmapNodeID.Set {
			patch.RoadmapSet = true
			if payload.RoadmapNodeID.Value != nil && strings.TrimSpace(*payload.RoadmapNodeID.Value) != "" {
				patch.Roadmap = &taskdomain.Roadmap{
					WorkspaceID: runtime.WorkspaceID, ID: strings.TrimSpace(*payload.RoadmapNodeID.Value),
					ProjectID: projectID,
				}
			}
		}
		if payload.NoteID.Set {
			patch.TaskNoteSet = true
			if payload.NoteID.Value != nil && strings.TrimSpace(*payload.NoteID.Value) != "" {
				patch.TaskNote = &taskdomain.TaskNoteIdentity{
					WorkspaceID: runtime.WorkspaceID, NoteID: strings.TrimSpace(*payload.NoteID.Value),
				}
			}
		}
		result, err = runtime.Tasks.PatchTask(ctx, taskdomain.PatchTaskRequest{
			WorkspaceID: runtime.WorkspaceID, TaskID: taskID, ExpectedRuntimeEpoch: runtime.Epoch,
			ExpectedTaskRevision: taskRevision, ExpectedScheduleRevision: scheduleRevision,
			Patch: patch, CommandID: envelope.CommandID, ActorID: identity.UserID, At: now,
		})
		if err != nil {
			return outcome, err
		}
	} else {
		if err := decodeEmptyPayload(envelope.Payload); err != nil {
			return outcome, err
		}
		occurrences, err := envelope.Expected.ExactOccurrences()
		if err != nil {
			return outcome, err
		}
		command, ok := taskLifecycleCommand(envelope.CommandType)
		if !ok {
			return outcome, mobilev2command.ErrInvalidCommandEnvelope
		}
		result, err = runtime.Tasks.ExecuteLifecycleCommand(ctx, taskdomain.LifecycleCommandRequest{
			WorkspaceID: runtime.WorkspaceID, TaskID: taskID, Command: command,
			ExpectedRuntimeEpoch: runtime.Epoch,
			Expected: taskdomain.LifecycleExpectedRevisions{
				Task: taskRevision, Schedule: scheduleRevision, Occurrences: occurrences,
			},
			CommandID: envelope.CommandID, ActorID: identity.UserID, At: now,
		})
		if err != nil {
			return outcome, err
		}
		for occurrenceID := range result.OccurrenceRevisions {
			outcome.OccurrenceIDs[occurrenceID] = struct{}{}
		}
		if len(result.OccurrenceRevisions) > 0 {
			outcome.Scopes = append(outcome.Scopes,
				mobilev2sync.ScopeIPhoneOccurrenceWindow, mobilev2sync.ScopeWatchOccurrenceWindow)
		}
	}
	outcome.Result.AffectedRevisions = taskAffectedRevisions(
		taskID, result.TaskRevision, result.ScheduleRevision, result.OccurrenceRevisions,
	)
	return outcome, nil
}

func dispatchOccurrenceCommand(
	ctx context.Context,
	runtime taskapp.RuntimeSnapshot,
	identity handler.MobileV2Identity,
	envelope mobilev2command.Envelope,
	now time.Time,
) (taskCommandOutcome, error) {
	outcome := newTaskCommandOutcome(
		mobilev2sync.ScopeIPhoneTaskCore,
		mobilev2sync.ScopeIPhoneOccurrenceWindow,
		mobilev2sync.ScopeWatchOccurrenceWindow,
	)
	occurrenceID := targetEntityID(envelope.Target)
	if occurrenceID == "" {
		return outcome, mobilev2command.ErrInvalidCommandEnvelope
	}
	outcome.OccurrenceIDs[occurrenceID] = struct{}{}
	current, err := runtime.Reader.GetOccurrence(ctx, occurrenceID)
	if err != nil {
		return outcome, err
	}
	taskID := current.TaskID
	outcome.TaskIDs[taskID] = struct{}{}
	taskRevision, err := envelope.Expected.Exact("task")
	if err != nil {
		return outcome, err
	}
	scheduleRevision, err := envelope.Expected.Exact("schedule")
	if err != nil {
		return outcome, err
	}
	occurrenceRevision, err := envelope.Expected.Exact("occurrence")
	if err != nil {
		return outcome, err
	}
	if envelope.CommandType == "occurrence.reschedule-only-this" {
		var payload occurrenceTimingPayload
		if err := decodeCommandPayload(envelope.Payload, &payload); err != nil {
			return outcome, err
		}
		if payload.TimingType == nil && !payload.DueAt.Set {
			return outcome, mobilev2command.ErrInvalidCommandEnvelope
		}
		dueAt, err := parseOptionalInstant(payload.DueAt.Value)
		if err != nil {
			return outcome, err
		}
		timing := taskdomain.OccurrenceTimingInput{
			Timezone: payload.Timezone, PreserveTiming: payload.TimingType == nil,
			PlannedDate: optionalValue(payload.PlannedDate), AllDayEndDate: optionalValue(payload.AllDayEndDate),
			LocalStartTime: optionalValue(payload.LocalStartTime), DurationMinutes: optionalIntValue(payload.DurationMinutes),
			DueAtSet: payload.DueAt.Set, DueAt: dueAt,
		}
		if payload.TimingType != nil {
			timing.TimingType = *payload.TimingType
		}
		if selected, exists := payload.SelectedOffsets[timing.PlannedDate]; exists {
			timing.SelectedOffsetSeconds = &selected
		}
		result, err := runtime.Schedules.RescheduleOccurrence(ctx, taskdomain.RescheduleOccurrenceRequest{
			WorkspaceID: runtime.WorkspaceID, TaskID: taskID, OccurrenceID: occurrenceID,
			ExpectedRuntimeEpoch: runtime.Epoch, ExpectedTaskRevision: taskRevision,
			ExpectedScheduleRevision: scheduleRevision, ExpectedOccurrenceRevision: occurrenceRevision,
			Timing: timing,
		}, taskapp.CommandMetadata{ActorID: identity.UserID, CommandID: envelope.CommandID, At: now})
		if err != nil {
			return outcome, err
		}
		outcome.Result.AffectedRevisions = []mobilev2command.AffectedRevision{
			{EntityType: "task", EntityID: taskID, Revision: strconv.FormatInt(result.TaskRevision, 10)},
			{EntityType: "task_schedule", EntityID: taskID, Revision: strconv.FormatInt(result.ScheduleRevision, 10)},
			{EntityType: "task_occurrence", EntityID: occurrenceID, Revision: strconv.FormatInt(result.OccurrenceRevision, 10)},
		}
		return outcome, nil
	}
	command, ok := occurrenceCommand(envelope.CommandType)
	if !ok {
		return outcome, mobilev2command.ErrInvalidCommandEnvelope
	}
	blockedReason, nextAction := "", ""
	if command == taskdomain.OccurrenceCommandBlock {
		var payload occurrenceBlockPayload
		if err := decodeCommandPayload(envelope.Payload, &payload); err != nil {
			return outcome, err
		}
		blockedReason, nextAction = payload.BlockedReason, payload.NextAction
	} else if err := decodeEmptyPayload(envelope.Payload); err != nil {
		return outcome, err
	}
	result, err := runtime.Occurrences.Execute(ctx, taskdomain.OccurrenceCommandRequest{
		WorkspaceID: runtime.WorkspaceID, TaskID: taskID, OccurrenceID: occurrenceID,
		Command: command, ExpectedRuntimeEpoch: runtime.Epoch,
		Expected: taskdomain.OccurrenceCommandExpectedRevisions{
			Task: taskRevision, Schedule: scheduleRevision, Occurrence: occurrenceRevision,
		},
		BlockedReason: blockedReason, NextAction: nextAction,
		CommandID: envelope.CommandID, ActorID: identity.UserID, At: now,
	})
	if err != nil {
		return outcome, err
	}
	outcome.Result.AffectedRevisions = []mobilev2command.AffectedRevision{
		{EntityType: "task", EntityID: taskID, Revision: strconv.FormatInt(result.TaskRevision, 10)},
		{EntityType: "task_schedule", EntityID: taskID, Revision: strconv.FormatInt(result.ScheduleRevision, 10)},
		{EntityType: "task_occurrence", EntityID: occurrenceID, Revision: strconv.FormatInt(result.OccurrenceRevision, 10)},
	}
	return outcome, nil
}

func dispatchScheduleCommand(
	ctx context.Context,
	runtime taskapp.RuntimeSnapshot,
	identity handler.MobileV2Identity,
	envelope mobilev2command.Envelope,
	now time.Time,
) (taskCommandOutcome, error) {
	outcome := newTaskCommandOutcome(
		mobilev2sync.ScopeIPhoneTaskCore,
		mobilev2sync.ScopeIPhoneOccurrenceWindow,
		mobilev2sync.ScopeWatchOccurrenceWindow,
	)
	taskID := targetEntityID(envelope.Target)
	if taskID == "" {
		return outcome, mobilev2command.ErrInvalidCommandEnvelope
	}
	outcome.TaskIDs[taskID] = struct{}{}
	taskRevision, err := envelope.Expected.Exact("task")
	if err != nil {
		return outcome, err
	}
	scheduleRevision, err := envelope.Expected.Exact("schedule")
	if err != nil {
		return outcome, err
	}
	var payload scheduleReschedulePayload
	if err := decodeCommandPayload(envelope.Payload, &payload); err != nil {
		return outcome, err
	}
	before, err := runtime.Reader.GetTaskAggregate(ctx, taskID)
	if err != nil {
		return outcome, err
	}
	beforeOccurrences := make(map[string]int64, len(before.Aggregate.Occurrences))
	for _, occurrence := range before.Aggregate.Occurrences {
		beforeOccurrences[occurrence.ID] = occurrence.Revision
	}
	result, err := runtime.Schedules.RescheduleThisAndFollowing(ctx, taskdomain.RescheduleThisAndFutureRequest{
		WorkspaceID: runtime.WorkspaceID, TaskID: taskID, ExpectedRuntimeEpoch: runtime.Epoch,
		ExpectedTaskRevision: taskRevision, ExpectedScheduleRevision: scheduleRevision,
		EffectiveFrom: payload.EffectiveFrom, GenerateThroughExclusive: payload.GenerateThroughExclusive,
		Schedule: payload.Schedule.domainInput(), SelectedOffsets: cloneIntMap(payload.SelectedOffsets),
	}, taskapp.CommandMetadata{ActorID: identity.UserID, CommandID: envelope.CommandID, At: now})
	if err != nil {
		return outcome, err
	}
	aggregate, err := runtime.Reader.GetTaskAggregate(ctx, taskID)
	if err != nil {
		return outcome, err
	}
	for _, occurrence := range aggregate.Aggregate.Occurrences {
		outcome.OccurrenceIDs[occurrence.ID] = struct{}{}
		delete(beforeOccurrences, occurrence.ID)
	}
	outcome.Result.AffectedRevisions = []mobilev2command.AffectedRevision{
		{EntityType: "task", EntityID: taskID, Revision: strconv.FormatInt(result.TaskRevision, 10)},
		{EntityType: "task_schedule", EntityID: taskID, Revision: strconv.FormatInt(result.ScheduleRevision, 10)},
	}
	if result.OccurrenceRevision > 0 {
		for _, occurrence := range aggregate.Aggregate.Occurrences {
			if occurrence.Revision != result.OccurrenceRevision {
				continue
			}
			outcome.Result.AffectedRevisions = append(outcome.Result.AffectedRevisions, mobilev2command.AffectedRevision{
				EntityType: "task_occurrence", EntityID: occurrence.ID,
				Revision: strconv.FormatInt(occurrence.Revision, 10),
			})
		}
	}
	for occurrenceID, revision := range beforeOccurrences {
		tombstoneRevision := revision + 1
		outcome.Deleted = append(outcome.Deleted, storage.MobileV2DeletedEntity{
			EntityType: "task_occurrence", EntityID: occurrenceID, Revision: tombstoneRevision,
		})
		outcome.Result.AffectedRevisions = append(outcome.Result.AffectedRevisions, mobilev2command.AffectedRevision{
			EntityType: "task_occurrence", EntityID: occurrenceID,
			Revision: strconv.FormatInt(tombstoneRevision, 10),
		})
	}
	return outcome, nil
}

func taskAffectedRevisions(taskID string, taskRevision, scheduleRevision int64, occurrences map[string]int64) []mobilev2command.AffectedRevision {
	result := []mobilev2command.AffectedRevision{
		{EntityType: "task", EntityID: taskID, Revision: strconv.FormatInt(taskRevision, 10)},
		{EntityType: "task_schedule", EntityID: taskID, Revision: strconv.FormatInt(scheduleRevision, 10)},
	}
	for occurrenceID, revision := range occurrences {
		result = append(result, mobilev2command.AffectedRevision{
			EntityType: "task_occurrence", EntityID: occurrenceID, Revision: strconv.FormatInt(revision, 10),
		})
	}
	return result
}

func taskLifecycleCommand(commandType string) (taskdomain.TaskLifecycleCommand, bool) {
	switch commandType {
	case "task.publish":
		return taskdomain.TaskCommandPublish, true
	case "task.pause":
		return taskdomain.TaskCommandPause, true
	case "task.resume":
		return taskdomain.TaskCommandResume, true
	case "task.cancel":
		return taskdomain.TaskCommandCancel, true
	case "task.restore":
		return taskdomain.TaskCommandRestore, true
	case "task.archive":
		return taskdomain.TaskCommandArchive, true
	default:
		return "", false
	}
}

func occurrenceCommand(commandType string) (taskdomain.OccurrenceCommand, bool) {
	switch commandType {
	case "occurrence.start":
		return taskdomain.OccurrenceCommandStart, true
	case "occurrence.block":
		return taskdomain.OccurrenceCommandBlock, true
	case "occurrence.unblock":
		return taskdomain.OccurrenceCommandUnblock, true
	case "occurrence.complete":
		return taskdomain.OccurrenceCommandComplete, true
	case "occurrence.skip":
		return taskdomain.OccurrenceCommandSkip, true
	case "occurrence.cancel":
		return taskdomain.OccurrenceCommandCancel, true
	case "occurrence.reopen":
		return taskdomain.OccurrenceCommandReopen, true
	default:
		return "", false
	}
}

func targetEntityID(target mobilev2command.EnvelopeTarget) string {
	if target.EntityID == nil {
		return ""
	}
	return strings.TrimSpace(*target.EntityID)
}

func decodeCommandPayload(raw json.RawMessage, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(destination); err != nil {
		return mobilev2command.ErrInvalidCommandEnvelope
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return mobilev2command.ErrInvalidCommandEnvelope
	}
	return nil
}

func decodeEmptyPayload(raw json.RawMessage) error {
	var object map[string]json.RawMessage
	if err := decodeCommandPayload(raw, &object); err != nil || len(object) != 0 {
		return mobilev2command.ErrInvalidCommandEnvelope
	}
	return nil
}

func parseOptionalInstant(raw *string) (*time.Time, error) {
	if raw == nil {
		return nil, nil
	}
	value, err := time.Parse("2006-01-02T15:04:05.000Z", *raw)
	if err != nil {
		return nil, mobilev2command.ErrInvalidCommandEnvelope
	}
	value = value.UTC()
	return &value, nil
}

func optionalValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func optionalIntValue(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func cloneIntMap(values map[string]int) map[string]int {
	result := make(map[string]int, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}
