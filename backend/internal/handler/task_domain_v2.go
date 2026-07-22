package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/taskapp"
	"github.com/hujinrun/flowspace/internal/taskdomain"
	"github.com/hujinrun/flowspace/internal/tenantruntime"
)

// TaskDomainV2Application is the request-scoped application boundary used by
// the isolated v2 transport. It deliberately contains actor/workspace scope,
// but never exposes runtime epochs, repositories, aggregates, or storage.
// Production wiring is intentionally absent until tenant runtime cutover is
// safe; tests may register this router with an isolated application fake.
type TaskDomainV2Application interface {
	CreateProject(context.Context, taskapp.CreateProjectRequest) (taskapp.ProjectCommandOutcome, error)
	PatchProject(context.Context, taskapp.PatchProjectRequest) (taskapp.ProjectCommandOutcome, error)
	CompleteProject(context.Context, taskapp.ExistingProjectRequest) (taskapp.ProjectCommandOutcome, error)
	ArchiveProject(context.Context, taskapp.ExistingProjectRequest) (taskapp.ProjectCommandOutcome, error)
	DeleteProject(context.Context, taskapp.ExistingProjectRequest) (taskapp.ProjectCommandOutcome, error)
	CreateTask(context.Context, taskapp.CreateTaskRequest) (taskapp.CreateTaskResult, error)
	ExecuteTaskLifecycle(context.Context, taskapp.TaskLifecycleRequest) (taskapp.TaskCommandOutcome, error)
	ExecuteOccurrenceByID(context.Context, taskapp.OccurrenceByIDRequest) (taskapp.OccurrenceCommandOutcome, error)
	RescheduleOccurrence(context.Context, taskapp.RescheduleOccurrenceRequest) (taskapp.ScheduleCommandOutcome, error)
	RescheduleThisAndFollowing(context.Context, taskapp.RescheduleThisAndFollowingRequest) (taskapp.ScheduleCommandOutcome, error)
	ListOccurrences(context.Context, taskapp.OccurrenceQueryRequest) ([]taskdomain.QueryOccurrenceSnapshot, error)
	GetProject(context.Context, taskapp.EntityQueryRequest) (taskdomain.ProjectSnapshot, error)
	GetTask(context.Context, taskapp.EntityQueryRequest) (taskdomain.TaskAggregateQueryResult, error)
	GetOccurrence(context.Context, taskapp.EntityQueryRequest) (taskdomain.QueryOccurrenceSnapshot, error)
	ListProjects(context.Context, taskapp.ListProjectsRequest) (taskapp.ProjectListResult, error)
	ListTaskDefinitions(context.Context, taskapp.ListTaskDefinitionsRequest) (taskapp.TaskDefinitionListResult, error)
	PatchTask(context.Context, taskapp.PatchTaskRequest) (taskapp.TaskCommandOutcome, error)
	CalendarEntries(context.Context, taskapp.CalendarEntriesRequest) (taskapp.CalendarEntriesResult, error)
}

var _ TaskDomainV2Application = (*taskapp.Facade)(nil)

type PatchTaskV2Request struct {
	ExpectedTaskRevision     int64    `json:"expected_task_revision"`
	ExpectedScheduleRevision int64    `json:"expected_schedule_revision"`
	Title                    *string  `json:"title,omitempty"`
	Description              *string  `json:"description,omitempty"`
	Priority                 *int     `json:"priority,omitempty"`
	SortOrder                *float64 `json:"sort_order,omitempty"`
	ProjectID                *string  `json:"project_id,omitempty"`
	RoadmapNodeID            *string  `json:"roadmap_node_id,omitempty"`
	TaskNoteID               *string  `json:"task_note_id,omitempty"`
}

func (request *PatchTaskV2Request) validateTaskDomainRequest() error {
	if request == nil || request.ExpectedTaskRevision < 1 || request.ExpectedScheduleRevision < 1 ||
		(request.Title == nil && request.Description == nil && request.Priority == nil && request.SortOrder == nil &&
			request.ProjectID == nil && request.RoadmapNodeID == nil && request.TaskNoteID == nil) {
		return errors.New("invalid task patch")
	}
	if request.Title != nil && strings.TrimSpace(*request.Title) == "" {
		return errors.New("task title must not be blank")
	}
	if request.Priority != nil && (*request.Priority < 0 || *request.Priority > 3) {
		return errors.New("invalid task priority")
	}
	if request.ProjectID != nil && strings.TrimSpace(*request.ProjectID) == "" {
		return errors.New("project_id must not be blank")
	}
	return nil
}

type taskDomainScheduleCommandResponse struct {
	TaskRevision       int64                          `json:"task_revision"`
	ScheduleRevision   int64                          `json:"schedule_revision"`
	OccurrenceRevision int64                          `json:"occurrence_revision,omitempty"`
	ScheduleVersion    int64                          `json:"schedule_version,omitempty"`
	OffsetCandidates   []TaskDomainOffsetCandidateDTO `json:"offset_candidates,omitempty"`
}

// RegisterTaskDomainV2Routes registers only on the supplied isolated group.
// The production router never calls this function until a later cutover task.
func RegisterTaskDomainV2Routes(routes *gin.RouterGroup, application TaskDomainV2Application) {
	if routes == nil {
		return
	}
	handler := taskDomainV2Handler{application: application}
	routes.GET("/projects", handler.listProjects)
	routes.POST("/projects", handler.createProject)
	routes.GET("/projects/:projectID", handler.getProject)
	routes.PATCH("/projects/:projectID", handler.patchProject)
	routes.POST("/projects/:projectID/complete", handler.projectCommand(taskdomain.ProjectCommandComplete))
	routes.POST("/projects/:projectID/archive", handler.projectCommand(taskdomain.ProjectCommandArchive))
	routes.DELETE("/projects/:projectID", handler.deleteProject)

	routes.GET("/tasks", handler.listTasks)
	routes.POST("/tasks", handler.createTask)
	routes.GET("/tasks/:taskID", handler.getTask)
	routes.PATCH("/tasks/:taskID", handler.patchTask)
	for _, command := range []taskdomain.TaskLifecycleCommand{
		taskdomain.TaskCommandPublish, taskdomain.TaskCommandPause, taskdomain.TaskCommandResume,
		taskdomain.TaskCommandCancel, taskdomain.TaskCommandRestore, taskdomain.TaskCommandArchive,
	} {
		routes.POST("/tasks/:taskID/"+string(command), handler.taskLifecycle(command))
	}
	routes.POST("/tasks/:taskID/schedule/this-and-following", handler.rescheduleThisAndFollowing)

	routes.GET("/task-occurrences", handler.listOccurrences)
	routes.GET("/task-occurrences/:occurrenceID", handler.getOccurrence)
	for _, command := range []taskdomain.OccurrenceCommand{
		taskdomain.OccurrenceCommandStart, taskdomain.OccurrenceCommandBlock, taskdomain.OccurrenceCommandUnblock,
		taskdomain.OccurrenceCommandComplete, taskdomain.OccurrenceCommandSkip, taskdomain.OccurrenceCommandCancel,
		taskdomain.OccurrenceCommandReopen,
	} {
		routes.POST("/task-occurrences/:occurrenceID/"+string(command), handler.occurrenceCommand(command))
	}
	routes.PATCH("/task-occurrences/:occurrenceID/schedule/only-this", handler.rescheduleOccurrence)
	// Design-compatible alias: an occurrence PATCH is the public "only this"
	// schedule mutation; execution fields remain rejected by the strict DTO.
	routes.PATCH("/task-occurrences/:occurrenceID", handler.rescheduleOccurrence)
	routes.GET("/calendar/entries", handler.calendarEntries)
	routes.GET("/projects/:projectID/roadmap", handler.getRoadmap)
	routes.POST("/projects/:projectID/roadmap", handler.createRoadmap)
	routes.POST("/roadmaps/:id/nodes", handler.createRoadmapNode)
	routes.PATCH("/roadmaps/:id/nodes/:nodeID", handler.updateRoadmapNode)
	routes.DELETE("/roadmaps/:id/nodes/:nodeID", handler.deleteRoadmapNode)
}

type taskDomainV2Handler struct{ application TaskDomainV2Application }

func (handler taskDomainV2Handler) listProjects(c *gin.Context) {
	identity, ok := taskDomainAuthenticatedIdentity(c)
	if !ok {
		return
	}
	request, err := taskDomainListProjectsRequest(c, identity)
	if err != nil {
		writeTaskDomainError(c, err)
		return
	}
	projects, err := handler.application.ListProjects(c.Request.Context(), request)
	if err != nil {
		writeTaskDomainError(c, err)
		return
	}
	result := make([]ProjectV2DTO, 0, len(projects))
	for _, project := range projects {
		result = append(result, projectSnapshotV2DTO(project))
	}
	success(c, gin.H{"projects": result})
}

func (handler taskDomainV2Handler) getProject(c *gin.Context) {
	identity, ok := taskDomainAuthenticatedIdentity(c)
	if !ok {
		return
	}
	project, err := handler.application.GetProject(c.Request.Context(), taskapp.EntityQueryRequest{WorkspaceID: identity.workspaceID, ActorID: identity.actorID, EntityID: c.Param("projectID")})
	if err != nil {
		writeTaskDomainError(c, err)
		return
	}
	success(c, gin.H{"project": projectSnapshotV2DTO(project)})
}

func (handler taskDomainV2Handler) createProject(c *gin.Context) {
	identity, ok := taskDomainAuthenticatedIdentity(c)
	if !ok {
		return
	}
	var request CreateProjectV2Request
	if !decodeTaskDomainRequest(c, &request) {
		return
	}
	outcome, err := handler.application.CreateProject(c.Request.Context(), taskapp.CreateProjectRequest{
		WorkspaceID: identity.workspaceID, ActorID: identity.actorID, Name: request.Name,
		Kind: request.Kind, Horizon: request.Horizon, Status: request.Status,
	})
	if err != nil {
		writeTaskDomainError(c, err)
		return
	}
	created(c, gin.H{"project": projectV2DTO(outcome)})
}

func (handler taskDomainV2Handler) patchProject(c *gin.Context) {
	identity, ok := taskDomainAuthenticatedIdentity(c)
	if !ok {
		return
	}
	var request UpdateProjectV2Request
	if !decodeTaskDomainRequest(c, &request) {
		return
	}
	outcome, err := handler.application.PatchProject(c.Request.Context(), taskapp.PatchProjectRequest{
		WorkspaceID: identity.workspaceID, ActorID: identity.actorID, ProjectID: c.Param("projectID"),
		ExpectedProjectRevision: request.ExpectedProjectRevision, Name: request.Name, Kind: request.Kind,
		Horizon: request.Horizon, Status: request.Status,
	})
	if err != nil {
		writeTaskDomainError(c, err)
		return
	}
	success(c, gin.H{"project": projectV2DTO(outcome)})
}

func (handler taskDomainV2Handler) projectCommand(command taskdomain.ProjectCommand) gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := taskDomainAuthenticatedIdentity(c)
		if !ok {
			return
		}
		var request ProjectCommandV2Request
		if !decodeTaskDomainRequest(c, &request) {
			return
		}
		applicationRequest := taskapp.ExistingProjectRequest{WorkspaceID: identity.workspaceID, ActorID: identity.actorID, ProjectID: c.Param("projectID"), ExpectedProjectRevision: request.ExpectedProjectRevision}
		var outcome taskapp.ProjectCommandOutcome
		var err error
		switch command {
		case taskdomain.ProjectCommandComplete:
			outcome, err = handler.application.CompleteProject(c.Request.Context(), applicationRequest)
		case taskdomain.ProjectCommandArchive:
			outcome, err = handler.application.ArchiveProject(c.Request.Context(), applicationRequest)
		default:
			err = taskapp.ErrInvalidRequest
		}
		if err != nil {
			writeTaskDomainError(c, err)
			return
		}
		success(c, projectCommandV2Response(outcome))
	}
}

func (handler taskDomainV2Handler) deleteProject(c *gin.Context) {
	identity, ok := taskDomainAuthenticatedIdentity(c)
	if !ok {
		return
	}
	var request DeleteProjectV2Request
	if !decodeTaskDomainRequest(c, &request) {
		return
	}
	outcome, err := handler.application.DeleteProject(c.Request.Context(), taskapp.ExistingProjectRequest{
		WorkspaceID: identity.workspaceID, ActorID: identity.actorID, ProjectID: c.Param("projectID"), ExpectedProjectRevision: request.ExpectedProjectRevision,
	})
	if err != nil {
		writeTaskDomainError(c, err)
		return
	}
	success(c, projectCommandV2Response(outcome))
}

func (handler taskDomainV2Handler) listTasks(c *gin.Context) {
	identity, ok := taskDomainAuthenticatedIdentity(c)
	if !ok {
		return
	}
	request, err := taskDomainListTasksRequest(c, identity)
	if err != nil {
		writeTaskDomainError(c, err)
		return
	}
	tasks, err := handler.application.ListTaskDefinitions(c.Request.Context(), request)
	if err != nil {
		writeTaskDomainError(c, err)
		return
	}
	result := make([]TaskV2DTO, 0, len(tasks))
	for _, task := range tasks {
		result = append(result, taskReadModelV2DTO(task))
	}
	success(c, gin.H{"tasks": result})
}

func (handler taskDomainV2Handler) getTask(c *gin.Context) {
	identity, ok := taskDomainAuthenticatedIdentity(c)
	if !ok {
		return
	}
	task, err := handler.application.GetTask(c.Request.Context(), taskapp.EntityQueryRequest{WorkspaceID: identity.workspaceID, ActorID: identity.actorID, EntityID: c.Param("taskID")})
	if err != nil {
		writeTaskDomainError(c, err)
		return
	}
	success(c, gin.H{"task": taskAggregateV2DTO(task)})
}

func (handler taskDomainV2Handler) patchTask(c *gin.Context) {
	identity, ok := taskDomainAuthenticatedIdentity(c)
	if !ok {
		return
	}
	var request PatchTaskV2Request
	if !decodeTaskDomainRequest(c, &request) {
		return
	}
	task, err := handler.application.PatchTask(c.Request.Context(), taskapp.PatchTaskRequest{
		WorkspaceID: identity.workspaceID, ActorID: identity.actorID, TaskID: c.Param("taskID"),
		ExpectedTaskRevision: request.ExpectedTaskRevision, ExpectedScheduleRevision: request.ExpectedScheduleRevision,
		Title: request.Title, Description: request.Description, Priority: request.Priority, SortOrder: request.SortOrder,
		ProjectID: request.ProjectID, RoadmapNodeID: request.RoadmapNodeID, NoteID: request.TaskNoteID,
	})
	if err != nil {
		writeTaskDomainError(c, err)
		return
	}
	success(c, gin.H{"task": taskCommandV2DTO(task)})
}

func (handler taskDomainV2Handler) createTask(c *gin.Context) {
	identity, ok := taskDomainAuthenticatedIdentity(c)
	if !ok {
		return
	}
	var request CreateTaskV2Request
	if !decodeTaskDomainRequest(c, &request) {
		return
	}
	schedule, err := request.Schedule.domainInput()
	if err != nil {
		writeTaskDomainError(c, ErrInvalidTaskDomainRequest)
		return
	}
	applicationRequest := taskapp.CreateTaskRequest{
		WorkspaceID: identity.workspaceID, ActorID: identity.actorID,
		Project: taskdomain.ProjectIdentity{WorkspaceID: identity.workspaceID, ProjectID: request.ProjectID},
		Title:   request.Title, Description: request.Description, Priority: request.Priority, SortOrder: request.SortOrder,
		Schedule: schedule, AllDayEndDate: request.AllDayEndDate, DueAt: request.DueAt, SelectedOffsets: request.SelectedOffsets,
	}
	if request.RoadmapNodeID != nil {
		applicationRequest.Roadmap = &taskdomain.Roadmap{WorkspaceID: identity.workspaceID, ID: *request.RoadmapNodeID, ProjectID: request.ProjectID, Current: true}
	}
	if request.TaskNoteID != nil {
		applicationRequest.TaskNote = &taskdomain.TaskNoteIdentity{WorkspaceID: identity.workspaceID, NoteID: *request.TaskNoteID}
	}
	outcome, err := handler.application.CreateTask(c.Request.Context(), applicationRequest)
	if err != nil {
		writeTaskDomainError(c, err)
		return
	}
	task := TaskV2DTO{ID: outcome.TaskID, ProjectID: request.ProjectID, RoadmapNodeID: request.RoadmapNodeID, TaskNoteID: request.TaskNoteID,
		Title: request.Title, Description: request.Description, Priority: request.Priority, SortOrder: request.SortOrder,
		LifecycleStatus: outcome.LifecycleStatus, Revision: outcome.TaskRevision, ScheduleRevision: outcome.ScheduleRevision}
	occurrences := make([]OccurrenceV2DTO, 0, len(outcome.Occurrences))
	for _, occurrence := range outcome.Occurrences {
		occurrences = append(occurrences, createdOccurrenceV2DTO(occurrence, request.TaskNoteID))
	}
	created(c, gin.H{"task": task, "occurrences": occurrences})
}

func (handler taskDomainV2Handler) taskLifecycle(command taskdomain.TaskLifecycleCommand) gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := taskDomainAuthenticatedIdentity(c)
		if !ok {
			return
		}
		var request TaskAggregateCommandRequest
		if !decodeTaskDomainRequest(c, &request) {
			return
		}
		scheduleRevision := int64(0)
		if request.ExpectedScheduleRevision != nil {
			scheduleRevision = *request.ExpectedScheduleRevision
		}
		outcome, err := handler.application.ExecuteTaskLifecycle(c.Request.Context(), taskapp.TaskLifecycleRequest{
			WorkspaceID: identity.workspaceID, ActorID: identity.actorID, TaskID: c.Param("taskID"), Command: command,
			Expected: taskdomain.LifecycleExpectedRevisions{Task: request.ExpectedTaskRevision, Schedule: scheduleRevision, Occurrences: request.ExpectedOccurrenceRevisions},
		})
		if err != nil {
			writeTaskDomainError(c, err)
			return
		}
		success(c, TaskAggregateCommandResponse{TaskRevision: outcome.TaskRevision, ScheduleRevision: optionalPositiveRevision(outcome.ScheduleRevision), OccurrenceRevisions: outcome.OccurrenceRevisions})
	}
}

func (handler taskDomainV2Handler) occurrenceCommand(command taskdomain.OccurrenceCommand) gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := taskDomainAuthenticatedIdentity(c)
		if !ok {
			return
		}
		var request OccurrenceCommandV2Request
		if !decodeTaskDomainRequest(c, &request) {
			return
		}
		occurrenceID := c.Param("occurrenceID")
		expectedOccurrenceRevision, exists := request.ExpectedOccurrenceRevisions[occurrenceID]
		if !exists || len(request.ExpectedOccurrenceRevisions) != 1 {
			writeTaskDomainError(c, ErrInvalidTaskDomainRequest)
			return
		}
		blockedDetailsPresent := strings.TrimSpace(request.BlockedReason) != "" || strings.TrimSpace(request.NextAction) != ""
		if (command == taskdomain.OccurrenceCommandBlock) != blockedDetailsPresent {
			writeTaskDomainError(c, ErrInvalidTaskDomainRequest)
			return
		}
		outcome, err := handler.application.ExecuteOccurrenceByID(c.Request.Context(), taskapp.OccurrenceByIDRequest{
			WorkspaceID: identity.workspaceID, ActorID: identity.actorID, OccurrenceID: occurrenceID, Command: command,
			Expected:      taskdomain.OccurrenceCommandExpectedRevisions{Task: request.ExpectedTaskRevision, Schedule: request.ExpectedScheduleRevision, Occurrence: expectedOccurrenceRevision},
			BlockedReason: request.BlockedReason, NextAction: request.NextAction,
		})
		if err != nil {
			writeTaskDomainError(c, err)
			return
		}
		success(c, TaskAggregateCommandResponse{TaskRevision: outcome.TaskRevision, ScheduleRevision: optionalPositiveRevision(outcome.ScheduleRevision), OccurrenceRevisions: map[string]int64{occurrenceID: outcome.OccurrenceRevision}})
	}
}

func (handler taskDomainV2Handler) rescheduleOccurrence(c *gin.Context) {
	identity, ok := taskDomainAuthenticatedIdentity(c)
	if !ok {
		return
	}
	var request RescheduleOccurrenceV2Request
	if !decodeTaskDomainRequest(c, &request) {
		return
	}
	selected := selectedOccurrenceOffset(request.SelectedOffsets, request.Timing.PlannedDate, c.Param("occurrenceID"))
	outcome, err := handler.application.RescheduleOccurrence(c.Request.Context(), taskapp.RescheduleOccurrenceRequest{
		WorkspaceID: identity.workspaceID, ActorID: identity.actorID, OccurrenceID: c.Param("occurrenceID"),
		ExpectedTaskRevision: request.ExpectedTaskRevision, ExpectedScheduleRevision: request.ExpectedScheduleRevision,
		ExpectedOccurrenceRevision: request.ExpectedOccurrenceRevision,
		Timing: taskdomain.OccurrenceTimingInput{TimingType: request.Timing.TimingType, Timezone: request.Timing.Timezone,
			PlannedDate: request.Timing.PlannedDate, AllDayEndDate: request.Timing.AllDayEndDate,
			LocalStartTime: request.Timing.LocalStartTime, DurationMinutes: request.Timing.DurationMinutes, SelectedOffsetSeconds: selected},
	})
	if err != nil {
		writeTaskDomainError(c, scheduleOutcomeError(err, outcome.Candidates))
		return
	}
	success(c, taskDomainScheduleResponse(outcome))
}

func (handler taskDomainV2Handler) rescheduleThisAndFollowing(c *gin.Context) {
	identity, ok := taskDomainAuthenticatedIdentity(c)
	if !ok {
		return
	}
	var request RescheduleThisAndFutureV2Request
	if !decodeTaskDomainRequest(c, &request) {
		return
	}
	schedule, err := request.Schedule.domainInput()
	if err != nil {
		writeTaskDomainError(c, ErrInvalidTaskDomainRequest)
		return
	}
	outcome, err := handler.application.RescheduleThisAndFollowing(c.Request.Context(), taskapp.RescheduleThisAndFollowingRequest{
		WorkspaceID: identity.workspaceID, ActorID: identity.actorID, TaskID: c.Param("taskID"),
		ExpectedTaskRevision: request.ExpectedTaskRevision, ExpectedScheduleRevision: request.ExpectedScheduleRevision,
		EffectiveFrom: request.EffectiveFrom, GenerateThroughExclusive: request.GenerateThroughExclusive,
		Schedule: schedule, SelectedOffsets: request.SelectedOffsets,
	})
	if err != nil {
		writeTaskDomainError(c, scheduleOutcomeError(err, outcome.Candidates))
		return
	}
	success(c, taskDomainScheduleResponse(outcome))
}

func (handler taskDomainV2Handler) listOccurrences(c *gin.Context) {
	identity, ok := taskDomainAuthenticatedIdentity(c)
	if !ok {
		return
	}
	filter, err := taskDomainOccurrenceFilter(c)
	if err != nil {
		writeTaskDomainError(c, ErrInvalidTaskDomainRequest)
		return
	}
	snapshots, err := handler.application.ListOccurrences(c.Request.Context(), taskapp.OccurrenceQueryRequest{
		WorkspaceID: identity.workspaceID, ActorID: identity.actorID, TaskID: filter.TaskID, Scope: filter.Scope, From: filter.From, To: filter.To,
		Timezone: filter.Timezone, ProjectID: filter.ProjectID, Statuses: filter.Statuses, Recurring: filter.Recurring,
	})
	if err != nil {
		writeTaskDomainError(c, err)
		return
	}
	occurrences := make([]OccurrenceV2DTO, 0, len(snapshots))
	for _, snapshot := range snapshots {
		occurrences = append(occurrences, occurrenceV2DTO(snapshot))
	}
	success(c, gin.H{"occurrences": occurrences})
}

func (handler taskDomainV2Handler) getOccurrence(c *gin.Context) {
	identity, ok := taskDomainAuthenticatedIdentity(c)
	if !ok {
		return
	}
	occurrence, err := handler.application.GetOccurrence(c.Request.Context(), taskapp.EntityQueryRequest{WorkspaceID: identity.workspaceID, ActorID: identity.actorID, EntityID: c.Param("occurrenceID")})
	if err != nil {
		writeTaskDomainError(c, err)
		return
	}
	success(c, gin.H{"occurrence": occurrenceV2DTO(occurrence)})
}

func (handler taskDomainV2Handler) calendarEntries(c *gin.Context) {
	identity, ok := taskDomainAuthenticatedIdentity(c)
	if !ok {
		return
	}
	request, err := taskDomainCalendarQueryRequest(c, identity)
	if err != nil {
		writeTaskDomainError(c, err)
		return
	}
	entries, err := handler.application.CalendarEntries(c.Request.Context(), request)
	if err != nil {
		writeTaskDomainError(c, err)
		return
	}
	result := make([]CalendarEntryV2DTO, 0, len(entries))
	for _, entry := range entries {
		result = append(result, calendarEntryV2DTO(entry))
	}
	success(c, gin.H{"entries": result})
}

type taskDomainIdentity struct{ workspaceID, actorID string }

func taskDomainAuthenticatedIdentity(c *gin.Context) (taskDomainIdentity, bool) {
	if c == nil {
		return taskDomainIdentity{}, false
	}
	identity, identityOK := auth.IdentityFromContext(c.Request.Context())
	workspaceID, workspaceErr := auth.WorkspaceIDFromContext(c.Request.Context())
	if !identityOK || strings.TrimSpace(identity.UserID) == "" || workspaceErr != nil || strings.TrimSpace(workspaceID) == "" {
		c.JSON(http.StatusUnauthorized, TaskDomainErrorResponse{Error: TaskDomainAPIError{Code: "unauthorized", Message: "authentication is required", Retryable: false}})
		return taskDomainIdentity{}, false
	}
	return taskDomainIdentity{workspaceID: workspaceID, actorID: identity.UserID}, true
}

func decodeTaskDomainRequest(c *gin.Context, destination any) bool {
	if err := DecodeTaskDomainRequest(c.Request.Body, destination); err != nil {
		writeTaskDomainError(c, err)
		return false
	}
	return true
}

func writeTaskDomainError(c *gin.Context, err error) {
	if errors.Is(err, taskapp.ErrInvalidRuntime) || errors.Is(err, tenantruntime.ErrRuntimeNotActive) || errors.Is(err, tenantruntime.ErrRuntimeUnavailable) {
		mapped := taskDomainHTTPError(http.StatusServiceUnavailable, "task_domain_unavailable", "task-domain runtime is unavailable", true, nil)
		c.JSON(mapped.Status, mapped.Response)
		return
	}
	if errors.Is(err, taskapp.ErrInvalidRequest) || errors.Is(err, taskdomain.ErrInvalidOccurrenceListFilter) ||
		errors.Is(err, taskdomain.ErrInvalidProjectListFilter) || errors.Is(err, taskdomain.ErrInvalidTaskDefinitionListFilter) {
		err = ErrInvalidTaskDomainRequest
	}
	mapped := MapTaskDomainError(err)
	c.JSON(mapped.Status, mapped.Response)
}

func projectV2DTO(outcome taskapp.ProjectCommandOutcome) ProjectV2DTO {
	return ProjectV2DTO{ID: outcome.Project.ID, Name: outcome.Project.Name, Kind: outcome.Project.Kind, Horizon: outcome.Project.Horizon,
		Status: outcome.Project.Status, SystemRole: outcome.Project.SystemRole, Revision: outcome.Revision}
}

func projectSnapshotV2DTO(snapshot taskdomain.ProjectSnapshot) ProjectV2DTO {
	return ProjectV2DTO{ID: snapshot.Project.ID, Name: snapshot.Project.Name, Kind: snapshot.Project.Kind,
		Horizon: snapshot.Project.Horizon, Status: snapshot.Project.Status, SystemRole: snapshot.Project.SystemRole, Revision: snapshot.Revision}
}

func taskReadModelV2DTO(model taskdomain.TaskDefinitionSnapshot) TaskV2DTO {
	return TaskV2DTO{ID: model.Task.ID, ProjectID: model.Task.ProjectID, RoadmapNodeID: optionalString(model.Task.RoadmapNodeID),
		TaskNoteID: optionalString(model.Task.NoteID), Title: model.Task.Title, Description: model.Task.Description,
		Priority: model.Task.Priority, SortOrder: model.Task.SortOrder, LifecycleStatus: model.Task.LifecycleStatus,
		Revision: model.Task.Revision, ScheduleRevision: model.ScheduleRevision}
}

func taskAggregateV2DTO(result taskdomain.TaskAggregateQueryResult) TaskV2DTO {
	return taskReadModelV2DTO(taskdomain.TaskDefinitionSnapshot{Task: result.Task, ScheduleRevision: result.Schedule.Revision})
}

func taskCommandV2DTO(result taskapp.TaskCommandOutcome) TaskV2DTO {
	return taskReadModelV2DTO(taskdomain.TaskDefinitionSnapshot{Task: result.Task, ScheduleRevision: result.ScheduleRevision})
}

func projectCommandV2Response(outcome taskapp.ProjectCommandOutcome) ProjectCommandV2Response {
	return ProjectCommandV2Response{ProjectID: outcome.Project.ID, ProjectRevision: outcome.Revision, Status: outcome.Project.Status, Deleted: outcome.Deleted}
}

func optionalPositiveRevision(revision int64) *int64 {
	if revision < 1 {
		return nil
	}
	value := revision
	return &value
}

func taskDomainScheduleResponse(outcome taskapp.ScheduleCommandOutcome) taskDomainScheduleCommandResponse {
	candidates := make([]TaskDomainOffsetCandidateDTO, len(outcome.Candidates))
	for index, candidate := range outcome.Candidates {
		candidates[index] = TaskDomainOffsetCandidateDTO{OffsetSeconds: candidate.OffsetSeconds, UTC: candidate.UTC}
	}
	return taskDomainScheduleCommandResponse{TaskRevision: outcome.TaskRevision, ScheduleRevision: outcome.ScheduleRevision,
		OccurrenceRevision: outcome.OccurrenceRevision, ScheduleVersion: outcome.ScheduleVersion, OffsetCandidates: candidates}
}

func scheduleOutcomeError(err error, candidates []taskdomain.OffsetCandidate) error {
	if len(candidates) > 0 && taskdomain.ErrorCodeOf(err) == taskdomain.ErrorCodeAmbiguousLocalTime {
		return NewAmbiguousLocalTimeContractError(candidates)
	}
	return err
}

func selectedOccurrenceOffset(offsets map[string]int, keys ...string) *int {
	for _, key := range keys {
		if offset, exists := offsets[key]; exists {
			value := offset
			return &value
		}
	}
	return nil
}

func taskDomainOccurrenceFilter(c *gin.Context) (taskdomain.OccurrenceListFilter, error) {
	filter := taskdomain.OccurrenceListFilter{Scope: taskdomain.OccurrenceListAll, Timezone: c.DefaultQuery("timezone", "UTC"),
		TaskID: strings.TrimSpace(c.Query("task_id")), ProjectID: strings.TrimSpace(c.Query("project_id"))}
	if scope := strings.TrimSpace(c.Query("scope")); scope != "" {
		filter.Scope = taskdomain.OccurrenceListScope(scope)
	}
	switch filter.Scope {
	case taskdomain.OccurrenceListAll, taskdomain.OccurrenceListToday, taskdomain.OccurrenceListUpcoming, taskdomain.OccurrenceListOverdue,
		taskdomain.OccurrenceListUnscheduled, taskdomain.OccurrenceListCompleted, taskdomain.OccurrenceListCalendar:
	default:
		return taskdomain.OccurrenceListFilter{}, ErrInvalidTaskDomainRequest
	}
	var err error
	if raw := strings.TrimSpace(c.Query("from")); raw != "" {
		filter.From, err = time.Parse(time.RFC3339, raw)
		if err != nil {
			return taskdomain.OccurrenceListFilter{}, err
		}
	}
	if raw := strings.TrimSpace(c.Query("to")); raw != "" {
		filter.To, err = time.Parse(time.RFC3339, raw)
		if err != nil {
			return taskdomain.OccurrenceListFilter{}, err
		}
	}
	if raw := strings.TrimSpace(c.Query("execution_status")); raw != "" {
		for _, value := range strings.Split(raw, ",") {
			status := taskdomain.ExecutionStatus(strings.TrimSpace(value))
			switch status {
			case taskdomain.ExecutionStatusOpen, taskdomain.ExecutionStatusActive, taskdomain.ExecutionStatusBlocked,
				taskdomain.ExecutionStatusDone, taskdomain.ExecutionStatusSkipped, taskdomain.ExecutionStatusCancelled:
				filter.Statuses = append(filter.Statuses, status)
			default:
				return taskdomain.OccurrenceListFilter{}, ErrInvalidTaskDomainRequest
			}
		}
	}
	if raw := strings.TrimSpace(c.Query("recurring")); raw != "" {
		value, parseErr := strconv.ParseBool(raw)
		if parseErr != nil {
			return taskdomain.OccurrenceListFilter{}, parseErr
		}
		filter.Recurring = &value
	}
	return filter, nil
}

func taskDomainListProjectsRequest(c *gin.Context, identity taskDomainIdentity) (taskapp.ListProjectsRequest, error) {
	request := taskapp.ListProjectsRequest{WorkspaceID: identity.workspaceID, ActorID: identity.actorID}
	if value := strings.TrimSpace(c.Query("kind")); value != "" {
		kind := taskdomain.ProjectKind(value)
		if kind != taskdomain.ProjectKindStandard && kind != taskdomain.ProjectKindLearning {
			return taskapp.ListProjectsRequest{}, ErrInvalidTaskDomainRequest
		}
		request.Kind = &kind
	}
	if value := strings.TrimSpace(c.Query("horizon")); value != "" {
		horizon := taskdomain.ProjectHorizon(value)
		if horizon != taskdomain.ProjectHorizonShort && horizon != taskdomain.ProjectHorizonLong {
			return taskapp.ListProjectsRequest{}, ErrInvalidTaskDomainRequest
		}
		request.Horizon = &horizon
	}
	if value := strings.TrimSpace(c.Query("status")); value != "" {
		status := taskdomain.ProjectStatus(value)
		switch status {
		case taskdomain.ProjectStatusPlanning, taskdomain.ProjectStatusActive, taskdomain.ProjectStatusPaused,
			taskdomain.ProjectStatusCompleted, taskdomain.ProjectStatusArchived:
			request.Status = &status
		default:
			return taskapp.ListProjectsRequest{}, ErrInvalidTaskDomainRequest
		}
	}
	return request, nil
}

func taskDomainListTasksRequest(c *gin.Context, identity taskDomainIdentity) (taskapp.ListTaskDefinitionsRequest, error) {
	request := taskapp.ListTaskDefinitionsRequest{WorkspaceID: identity.workspaceID, ActorID: identity.actorID, ProjectID: strings.TrimSpace(c.Query("project_id"))}
	if value := strings.TrimSpace(c.Query("lifecycle_status")); value != "" {
		status := taskdomain.TaskLifecycleStatus(value)
		switch status {
		case taskdomain.TaskLifecycleDraft, taskdomain.TaskLifecycleActive, taskdomain.TaskLifecyclePaused,
			taskdomain.TaskLifecycleCompleted, taskdomain.TaskLifecycleCancelled, taskdomain.TaskLifecycleArchived:
			request.LifecycleStatus = &status
		default:
			return taskapp.ListTaskDefinitionsRequest{}, ErrInvalidTaskDomainRequest
		}
	}
	return request, nil
}

func taskDomainCalendarQueryRequest(c *gin.Context, identity taskDomainIdentity) (taskapp.CalendarEntriesRequest, error) {
	timezone := c.DefaultQuery("timezone", "UTC")
	from, err := parseTaskDomainCalendarDate(c.Query("from"), timezone)
	if err != nil {
		return taskapp.CalendarEntriesRequest{}, err
	}
	to, err := parseTaskDomainCalendarDate(c.Query("to"), timezone)
	if err != nil || !to.After(from) {
		return taskapp.CalendarEntriesRequest{}, ErrInvalidTaskDomainRequest
	}
	return taskapp.CalendarEntriesRequest{WorkspaceID: identity.workspaceID, ActorID: identity.actorID,
		From: from, To: to, Timezone: timezone, ProjectID: strings.TrimSpace(c.Query("project_id"))}, nil
}

func parseTaskDomainCalendarDate(value, timezone string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, ErrInvalidTaskDomainRequest
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Time{}, ErrInvalidTaskDomainRequest
	}
	return time.ParseInLocation("2006-01-02", value, location)
}

func occurrenceV2DTO(snapshot taskdomain.QueryOccurrenceSnapshot) OccurrenceV2DTO {
	return OccurrenceV2DTO{ID: snapshot.OccurrenceID, TaskID: snapshot.TaskID, OccurrenceKey: snapshot.OccurrenceKey,
		TaskNoteID: optionalString(snapshot.TaskNoteID), OccurrenceNoteID: optionalString(snapshot.OccurrenceNoteID),
		ExecutionStatus: snapshot.Status, Revision: snapshot.Revision, GeneratedScheduleRevision: snapshot.GeneratedScheduleRevision,
		PlannedDate: snapshot.PlannedDate, AllDayEndDate: snapshot.AllDayEndDate, PlannedStartAt: snapshot.PlannedStartAt,
		PlannedEndAt: snapshot.PlannedEndAt, DueAt: snapshot.DueAt, BlockedReason: snapshot.BlockedReason,
		NextAction: snapshot.NextAction, Location: snapshot.Location, CalendarKind: snapshot.CalendarKind, CalendarNotes: snapshot.CalendarNotes}
}

func createdOccurrenceV2DTO(record taskdomain.OccurrenceRecord, taskNoteID *string) OccurrenceV2DTO {
	return OccurrenceV2DTO{ID: record.ID, TaskID: record.TaskID, OccurrenceKey: record.OccurrenceKey,
		TaskNoteID: taskNoteID, OccurrenceNoteID: optionalString(record.NoteID), ExecutionStatus: record.ExecutionStatus,
		Revision: record.Revision, GeneratedScheduleRevision: record.GeneratedScheduleRevision, PlannedDate: record.PlannedDate,
		AllDayEndDate: record.AllDayEndDate, PlannedStartAt: record.PlannedStartAt, PlannedEndAt: record.PlannedEndAt, DueAt: record.DueAt}
}

func calendarEntryV2DTO(model taskapp.CalendarEntryReadModel) CalendarEntryV2DTO {
	snapshot := model.Occurrence
	return CalendarEntryV2DTO{ProjectID: snapshot.ProjectID, ProjectRevision: model.ProjectRevision,
		TaskID: snapshot.TaskID, TaskRevision: snapshot.TaskRevision, ScheduleRevision: snapshot.ScheduleRevision,
		TaskTitle: snapshot.Title, TaskNoteID: optionalString(snapshot.TaskNoteID),
		OccurrenceID: snapshot.OccurrenceID, OccurrenceKey: snapshot.OccurrenceKey,
		OccurrenceRevision: snapshot.Revision, GeneratedScheduleRevision: snapshot.GeneratedScheduleRevision,
		OccurrenceNoteID: optionalString(snapshot.OccurrenceNoteID), ExecutionStatus: snapshot.Status, TimingType: snapshot.TimingType,
		Timezone: snapshot.Timezone, Recurring: snapshot.Recurring,
		PlannedDate: snapshot.PlannedDate, AllDayEndDate: snapshot.AllDayEndDate, PlannedStartAt: snapshot.PlannedStartAt,
		PlannedEndAt: snapshot.PlannedEndAt, DueAt: snapshot.DueAt, Location: snapshot.Location,
		CalendarKind: snapshot.CalendarKind, CalendarNotes: snapshot.CalendarNotes}
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	copy := value
	return &copy
}
