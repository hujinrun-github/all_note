package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/legacytaskadapter"
	"github.com/hujinrun/flowspace/internal/taskapp"
	"github.com/hujinrun/flowspace/internal/taskdomain"
)

// LegacyWebTaskDomainApplication is deliberately read-only. The historical
// Web mutation DTOs do not carry the independent Task, Schedule and
// Occurrence revisions required by v2. Guessing revisions after a read would
// recreate last-write-wins and would let a request cross a runtime epoch.
// Those mutations therefore return a stable 410 and the new revision-aware
// endpoints remain the only production write path.
type LegacyWebTaskDomainApplication interface {
	ListOccurrences(context.Context, taskapp.OccurrenceQueryRequest) ([]taskdomain.QueryOccurrenceSnapshot, error)
	GetTask(context.Context, taskapp.EntityQueryRequest) (taskdomain.TaskAggregateQueryResult, error)
	GetProject(context.Context, taskapp.EntityQueryRequest) (taskdomain.ProjectSnapshot, error)
}

var _ LegacyWebTaskDomainApplication = (*taskapp.Facade)(nil)

func RegisterLegacyWebTaskDomainV2Routes(routes *gin.RouterGroup, application LegacyWebTaskDomainApplication) {
	if routes == nil {
		return
	}
	handler := legacyWebTaskDomainV2Handler{application: application}
	routes.GET("/tasks", handler.listTasks)
	routes.POST("/tasks", legacyWebRevisionRequired)
	routes.PATCH("/tasks/:taskID", legacyWebRevisionRequired)
	routes.DELETE("/tasks/:taskID", legacyWebRevisionRequired)
	routes.POST("/tasks/:taskID/occurrences/:date/complete", legacyWebRevisionRequired)
	routes.POST("/tasks/:taskID/occurrences/:date/reopen", legacyWebRevisionRequired)
	routes.POST("/tasks/:taskID/occurrences/:date/skip", legacyWebRevisionRequired)
	routes.GET("/events", handler.listEvents)
	routes.POST("/events", legacyWebRevisionRequired)
	routes.PATCH("/events/:eventID", legacyWebRevisionRequired)
	routes.DELETE("/events/:eventID", legacyWebRevisionRequired)
}

type legacyWebTaskDomainV2Handler struct {
	application LegacyWebTaskDomainApplication
}

func (handler legacyWebTaskDomainV2Handler) listTasks(c *gin.Context) {
	identity, ok := taskDomainAuthenticatedIdentity(c)
	if !ok {
		return
	}
	if handler.application == nil {
		writeLegacyProjectionError(c, taskapp.ErrInvalidRuntime)
		return
	}
	filter, err := legacyTaskOccurrenceFilter(c)
	if err != nil {
		badRequest(c, err.Error())
		return
	}
	occurrences, err := handler.application.ListOccurrences(c.Request.Context(), taskapp.OccurrenceQueryRequest{
		WorkspaceID: identity.workspaceID, ActorID: identity.actorID, Scope: taskdomain.OccurrenceListAll,
		ProjectID: filter.projectID, Statuses: filter.statuses, Recurring: filter.recurring,
	})
	if err != nil {
		writeLegacyProjectionError(c, err)
		return
	}

	tasks := make([]legacytaskadapter.LegacyTask, 0, len(occurrences))
	taskCache := make(map[string]taskdomain.TaskAggregateQueryResult)
	projectCache := make(map[string]taskdomain.ProjectSnapshot)
	for _, occurrence := range occurrences {
		if occurrence.WorkspaceID != identity.workspaceID {
			writeLegacyProjectionError(c, taskapp.ErrInvalidRuntime)
			return
		}
		if !legacyTaskOccurrenceMatches(occurrence, filter) {
			continue
		}
		aggregate, exists := taskCache[occurrence.TaskID]
		if !exists {
			aggregate, err = handler.application.GetTask(c.Request.Context(), taskapp.EntityQueryRequest{WorkspaceID: identity.workspaceID, ActorID: identity.actorID, EntityID: occurrence.TaskID})
			if err != nil {
				writeLegacyProjectionError(c, err)
				return
			}
			taskCache[occurrence.TaskID] = aggregate
		}
		project, exists := projectCache[occurrence.ProjectID]
		if !exists {
			project, err = handler.application.GetProject(c.Request.Context(), taskapp.EntityQueryRequest{WorkspaceID: identity.workspaceID, ActorID: identity.actorID, EntityID: occurrence.ProjectID})
			if err != nil {
				writeLegacyProjectionError(c, err)
				return
			}
			projectCache[occurrence.ProjectID] = project
		}
		version, found := legacyGeneratedScheduleVersion(aggregate.Versions, occurrence.GeneratedScheduleRevision)
		if !found || aggregate.Task.WorkspaceID != identity.workspaceID || aggregate.Schedule.WorkspaceID != identity.workspaceID || project.Project.WorkspaceID != identity.workspaceID {
			writeLegacyProjectionError(c, taskapp.ErrInvalidRuntime)
			return
		}
		projected, projectErr := legacytaskadapter.ProjectLegacyTask(legacytaskadapter.LegacyTaskProjectionSnapshot{
			Project: project, Task: aggregate.Task, Schedule: version, ScheduleHeaderRevision: aggregate.Schedule.Revision, Occurrence: occurrence,
		})
		if projectErr != nil {
			writeLegacyProjectionError(c, projectErr)
			return
		}
		if !legacyProjectedTaskMatches(projected, filter) {
			continue
		}
		tasks = append(tasks, projected)
	}

	page, pageSize := getPagination(c)
	total := len(tasks)
	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	successWithPagination(c, gin.H{"tasks": tasks[start:end]}, page, pageSize, total)
}

func (handler legacyWebTaskDomainV2Handler) listEvents(c *gin.Context) {
	identity, ok := taskDomainAuthenticatedIdentity(c)
	if !ok {
		return
	}
	if handler.application == nil {
		writeLegacyProjectionError(c, taskapp.ErrInvalidRuntime)
		return
	}
	timezone := strings.TrimSpace(c.DefaultQuery("timezone", "UTC"))
	location, err := time.LoadLocation(timezone)
	if err != nil || timezone == "Local" {
		badRequest(c, "invalid timezone")
		return
	}
	from, to, err := legacyEventMonthBounds(c.Query("month"), location)
	if err != nil {
		badRequest(c, "invalid month format, expected YYYY-MM")
		return
	}
	occurrences, err := handler.application.ListOccurrences(c.Request.Context(), taskapp.OccurrenceQueryRequest{
		WorkspaceID: identity.workspaceID, ActorID: identity.actorID, Scope: taskdomain.OccurrenceListCalendar,
		From: from, To: to, Timezone: timezone,
	})
	if err != nil {
		writeLegacyProjectionError(c, err)
		return
	}

	events := make([]legacytaskadapter.LegacyEvent, 0, len(occurrences))
	taskCache := make(map[string]taskdomain.TaskAggregateQueryResult)
	for _, occurrence := range occurrences {
		if occurrence.WorkspaceID != identity.workspaceID {
			writeLegacyProjectionError(c, taskapp.ErrInvalidRuntime)
			return
		}
		aggregate, exists := taskCache[occurrence.TaskID]
		if !exists {
			aggregate, err = handler.application.GetTask(c.Request.Context(), taskapp.EntityQueryRequest{WorkspaceID: identity.workspaceID, ActorID: identity.actorID, EntityID: occurrence.TaskID})
			if err != nil {
				writeLegacyProjectionError(c, err)
				return
			}
			taskCache[occurrence.TaskID] = aggregate
		}
		version, found := legacyGeneratedScheduleVersion(aggregate.Versions, occurrence.GeneratedScheduleRevision)
		if !found || aggregate.Task.WorkspaceID != identity.workspaceID || aggregate.Schedule.WorkspaceID != identity.workspaceID {
			writeLegacyProjectionError(c, taskapp.ErrInvalidRuntime)
			return
		}
		schedule, scheduleErr := legacytaskadapter.ScheduleFromVersion(version)
		if scheduleErr != nil {
			writeLegacyProjectionError(c, scheduleErr)
			return
		}
		projection, projectionErr := taskdomain.BuildCalendarProjection([]taskdomain.QueryOccurrenceSnapshot{occurrence})
		if projectionErr != nil {
			writeLegacyProjectionError(c, projectionErr)
			return
		}
		entries := append(projection.TimeBlocks, projection.AllDay...)
		if len(entries) != 1 {
			writeLegacyProjectionError(c, taskapp.ErrInvalidRuntime)
			return
		}
		event, eventErr := legacytaskadapter.ProjectLegacyEvent(legacytaskadapter.EventProjectionSnapshot{
			Entry: entries[0], ScheduleVersion: schedule, TaskRevision: occurrence.TaskRevision, ScheduleRevision: occurrence.ScheduleRevision,
		})
		if eventErr != nil {
			writeLegacyProjectionError(c, eventErr)
			return
		}
		events = append(events, event)
	}
	page, pageSize := getPagination(c)
	total := len(events)
	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	successWithPagination(c, gin.H{"events": events[start:end]}, page, pageSize, total)
}

type legacyTaskListFilter struct {
	projectID   string
	projectName string
	status      string
	scope       string
	horizon     string
	plannedDate string
	plannedFrom string
	plannedTo   string
	execution   string
	statuses    []taskdomain.ExecutionStatus
	recurring   *bool
}

func legacyTaskOccurrenceFilter(c *gin.Context) (legacyTaskListFilter, error) {
	filter := legacyTaskListFilter{
		projectID: strings.TrimSpace(c.Query("project_id")), projectName: strings.TrimSpace(c.Query("project")),
		status: strings.TrimSpace(c.DefaultQuery("status", "all")), scope: strings.TrimSpace(c.Query("scope")), horizon: strings.TrimSpace(c.Query("horizon")),
		plannedDate: strings.TrimSpace(c.Query("planned_date")), plannedFrom: strings.TrimSpace(c.Query("planned_from")), plannedTo: strings.TrimSpace(c.Query("planned_to")),
		execution: strings.TrimSpace(c.Query("execution_type")),
	}
	switch filter.status {
	case "", "all":
	case "done", "completed":
		filter.statuses = []taskdomain.ExecutionStatus{taskdomain.ExecutionStatusDone}
	case "open", "active", "blocked", "skipped", "cancelled":
		filter.statuses = []taskdomain.ExecutionStatus{taskdomain.ExecutionStatus(filter.status)}
	case "incomplete", "pending":
		filter.statuses = []taskdomain.ExecutionStatus{taskdomain.ExecutionStatusOpen, taskdomain.ExecutionStatusActive, taskdomain.ExecutionStatusBlocked}
	default:
		return legacyTaskListFilter{}, errors.New("invalid task status")
	}
	switch filter.execution {
	case "", "all":
	case legacytaskadapter.LegacyExecutionSingle:
		value := false
		filter.recurring = &value
	case legacytaskadapter.LegacyExecutionRecurring:
		value := true
		filter.recurring = &value
	default:
		return legacyTaskListFilter{}, errors.New("invalid execution_type")
	}
	if filter.horizon != "" && filter.horizon != legacytaskadapter.LegacyHorizonWeek && filter.horizon != legacytaskadapter.LegacyHorizonLong {
		return legacyTaskListFilter{}, errors.New("invalid horizon")
	}
	if filter.scope != "" && filter.scope != legacytaskadapter.LegacyScopeDaily && filter.scope != legacytaskadapter.LegacyScopeWeekly &&
		filter.scope != legacytaskadapter.LegacyScopeMonthly && filter.scope != legacytaskadapter.LegacyScopeYearly {
		return legacyTaskListFilter{}, errors.New("invalid scope")
	}
	for _, date := range []string{filter.plannedDate, filter.plannedFrom, filter.plannedTo} {
		if date != "" {
			if parsed, err := time.Parse("2006-01-02", date); err != nil || parsed.Format("2006-01-02") != date {
				return legacyTaskListFilter{}, errors.New("invalid planned date")
			}
		}
	}
	return filter, nil
}

func legacyTaskOccurrenceMatches(occurrence taskdomain.QueryOccurrenceSnapshot, filter legacyTaskListFilter) bool {
	if filter.plannedDate != "" && occurrence.PlannedDate != filter.plannedDate {
		return false
	}
	if filter.plannedFrom != "" && (occurrence.PlannedDate == "" || occurrence.PlannedDate < filter.plannedFrom) {
		return false
	}
	if filter.plannedTo != "" && (occurrence.PlannedDate == "" || occurrence.PlannedDate > filter.plannedTo) {
		return false
	}
	return true
}

func legacyProjectedTaskMatches(task legacytaskadapter.LegacyTask, filter legacyTaskListFilter) bool {
	if filter.projectName != "" && task.Project != filter.projectName {
		return false
	}
	if filter.horizon != "" && task.Horizon != filter.horizon {
		return false
	}
	if filter.scope != "" && task.Scope != filter.scope {
		return false
	}
	return true
}

func legacyGeneratedScheduleVersion(versions []taskdomain.ScheduleVersion, revision int64) (taskdomain.ScheduleVersion, bool) {
	for _, version := range versions {
		if version.ScheduleRevision == revision {
			return version, true
		}
	}
	return taskdomain.ScheduleVersion{}, false
}

func legacyEventMonthBounds(month string, location *time.Location) (time.Time, time.Time, error) {
	if month == "" {
		return time.Time{}, time.Time{}, errors.New("month is required")
	}
	parsed, err := time.ParseInLocation("2006-01", month, location)
	if err != nil || parsed.Format("2006-01") != month {
		return time.Time{}, time.Time{}, errors.New("invalid month")
	}
	return parsed, parsed.AddDate(0, 1, 0), nil
}

func legacyWebRevisionRequired(c *gin.Context) {
	errorResponse(c, http.StatusGone, "legacy_contract_revision_required", "the legacy mutation contract cannot carry v2 task, schedule, and occurrence revisions; use the v2 endpoint and refresh before retrying")
}

func writeLegacyProjectionError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, legacytaskadapter.ErrLegacyTaskUnrepresentable):
		errorResponse(c, http.StatusConflict, "legacy_state_unrepresentable", "the v2 state cannot be represented by the legacy Web contract")
	case errors.Is(err, taskdomain.ErrTaskNotFound), errors.Is(err, taskdomain.ErrProjectNotFound), errors.Is(err, taskdomain.ErrOccurrenceNotFound):
		notFound(c, "task-domain resource not found")
	case errors.Is(err, legacytaskadapter.ErrInvalidLegacyTask), errors.Is(err, legacytaskadapter.ErrInvalidLegacyEvent), errors.Is(err, taskdomain.ErrInvalidSchedule):
		errorResponse(c, http.StatusUnprocessableEntity, "legacy_projection_invalid", "the v2 resource cannot be projected without losing task or calendar semantics")
	default:
		errorResponse(c, http.StatusServiceUnavailable, "legacy_projection_unavailable", "the request-scoped v2 projection is unavailable")
	}
}
