package handler

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/legacytaskadapter"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
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
	registerLegacyWebTaskDomainV2Routes(routes, application, nil)
}

func RegisterLegacyWebTaskDomainV2RoutesWithStore(
	routes *gin.RouterGroup,
	application LegacyWebTaskDomainApplication,
	store storage.Store,
) {
	registerLegacyWebTaskDomainV2Routes(routes, application, store)
}

func registerLegacyWebTaskDomainV2Routes(
	routes *gin.RouterGroup,
	application LegacyWebTaskDomainApplication,
	store storage.Store,
) {
	if routes == nil {
		return
	}
	handler := legacyWebTaskDomainV2Handler{application: application, store: store}
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
	routes.GET("/today", handler.today)
	routes.GET("/summary", handler.summary)
}

type legacyWebTaskDomainV2Handler struct {
	application LegacyWebTaskDomainApplication
	store       storage.Store
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

func (handler legacyWebTaskDomainV2Handler) today(c *gin.Context) {
	identity, ok := taskDomainAuthenticatedIdentity(c)
	if !ok {
		return
	}
	if handler.application == nil {
		writeLegacyProjectionError(c, taskapp.ErrInvalidRuntime)
		return
	}
	timezone := strings.TrimSpace(c.DefaultQuery("timezone", "Asia/Shanghai"))
	location, err := time.LoadLocation(timezone)
	if err != nil || timezone == "Local" {
		badRequest(c, "invalid timezone")
		return
	}
	now := time.Now().In(location)
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location)
	todayEnd := todayStart.AddDate(0, 0, 1)

	todayOccurrences, err := handler.application.ListOccurrences(c.Request.Context(), taskapp.OccurrenceQueryRequest{
		WorkspaceID: identity.workspaceID, ActorID: identity.actorID, Scope: taskdomain.OccurrenceListToday,
		From: todayStart, To: todayEnd, Timezone: timezone,
	})
	if err != nil {
		writeLegacyProjectionError(c, err)
		return
	}
	overdueOccurrences, err := handler.application.ListOccurrences(c.Request.Context(), taskapp.OccurrenceQueryRequest{
		WorkspaceID: identity.workspaceID, ActorID: identity.actorID, Scope: taskdomain.OccurrenceListOverdue,
		From: now, Timezone: timezone,
	})
	if err != nil {
		writeLegacyProjectionError(c, err)
		return
	}
	overdueCutoff := todayStart.AddDate(0, 0, -7)
	overdueOccurrences = filterLegacyWebOverdueWindow(overdueOccurrences, overdueCutoff)

	todayTasks, err := handler.projectLegacyOccurrences(c.Request.Context(), identity, todayOccurrences)
	if err != nil {
		writeLegacyProjectionError(c, err)
		return
	}
	overdueTasks, err := handler.projectLegacyOccurrences(c.Request.Context(), identity, overdueOccurrences)
	if err != nil {
		writeLegacyProjectionError(c, err)
		return
	}
	sortLegacyWebTodayTasks(todayTasks)
	sortLegacyWebTodayTasks(overdueTasks)

	recentNotes := make([]model.Note, 0)
	if handler.store != nil {
		recentNotes, err = handler.store.Notes().Recent(c.Request.Context(), 5)
		if err != nil {
			internalError(c, "failed to get recent notes")
			return
		}
	}
	success(c, gin.H{
		"todayTasks": todayTasks, "overdueTasks": overdueTasks,
		"events": []model.Event{}, "recentNotes": recentNotes,
	})
}

func (handler legacyWebTaskDomainV2Handler) summary(c *gin.Context) {
	identity, ok := taskDomainAuthenticatedIdentity(c)
	if !ok {
		return
	}
	if handler.application == nil {
		writeLegacyProjectionError(c, taskapp.ErrInvalidRuntime)
		return
	}
	timezone := strings.TrimSpace(c.DefaultQuery("timezone", "Asia/Shanghai"))
	location, err := time.LoadLocation(timezone)
	if err != nil || timezone == "Local" {
		badRequest(c, "invalid timezone")
		return
	}
	from, err := time.ParseInLocation("2006-01-02", c.Query("from"), location)
	if err != nil {
		badRequest(c, "invalid date format, expected YYYY-MM-DD")
		return
	}
	to, err := time.ParseInLocation("2006-01-02", c.Query("to"), location)
	if err != nil {
		badRequest(c, "invalid date format, expected YYYY-MM-DD")
		return
	}
	if from.After(to) {
		badRequest(c, "from date must be before to date")
		return
	}
	toExclusive := to.AddDate(0, 0, 1)
	occurrences, err := handler.application.ListOccurrences(c.Request.Context(), taskapp.OccurrenceQueryRequest{
		WorkspaceID: identity.workspaceID, ActorID: identity.actorID, Scope: taskdomain.OccurrenceListCompleted,
		From: from, To: toExclusive, Timezone: timezone,
	})
	if err != nil {
		writeLegacyProjectionError(c, err)
		return
	}

	projectCache := make(map[string]taskdomain.ProjectSnapshot)
	summaries := make([]model.TaskSummary, 0, len(occurrences))
	activeDates := make(map[string]struct{})
	projectIDs := make(map[string]struct{})
	for _, occurrence := range occurrences {
		if occurrence.WorkspaceID != identity.workspaceID || occurrence.CompletedAt == nil {
			writeLegacyProjectionError(c, taskapp.ErrInvalidRuntime)
			return
		}
		project, exists := projectCache[occurrence.ProjectID]
		if !exists {
			project, err = handler.application.GetProject(c.Request.Context(), taskapp.EntityQueryRequest{
				WorkspaceID: identity.workspaceID, ActorID: identity.actorID, EntityID: occurrence.ProjectID,
			})
			if err != nil {
				writeLegacyProjectionError(c, err)
				return
			}
			projectCache[occurrence.ProjectID] = project
		}
		if project.Project.WorkspaceID != identity.workspaceID {
			writeLegacyProjectionError(c, taskapp.ErrInvalidRuntime)
			return
		}
		summaries = append(summaries, legacyWebSummaryTask(occurrence, project))
		activeDates[occurrence.CompletedAt.In(location).Format("2006-01-02")] = struct{}{}
		projectIDs[occurrence.ProjectID] = struct{}{}
	}
	sort.SliceStable(summaries, func(left, right int) bool {
		return *summaries[left].CompletedAt > *summaries[right].CompletedAt
	})

	page, pageSize := getPagination(c)
	total := len(summaries)
	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	groups := legacyWebSummaryGroups(summaries[start:end], location)
	data := model.NewSummaryData(groups, len(activeDates), len(projectIDs), total)
	successWithPagination(c, data, page, pageSize, total)
}

func (handler legacyWebTaskDomainV2Handler) projectLegacyOccurrences(
	ctx context.Context,
	identity taskDomainIdentity,
	occurrences []taskdomain.QueryOccurrenceSnapshot,
) ([]legacytaskadapter.LegacyTask, error) {
	tasks := make([]legacytaskadapter.LegacyTask, 0, len(occurrences))
	taskCache := make(map[string]taskdomain.TaskAggregateQueryResult)
	projectCache := make(map[string]taskdomain.ProjectSnapshot)
	for _, occurrence := range occurrences {
		if occurrence.WorkspaceID != identity.workspaceID {
			return nil, taskapp.ErrInvalidRuntime
		}
		aggregate, exists := taskCache[occurrence.TaskID]
		if !exists {
			var err error
			aggregate, err = handler.application.GetTask(ctx, taskapp.EntityQueryRequest{
				WorkspaceID: identity.workspaceID, ActorID: identity.actorID, EntityID: occurrence.TaskID,
			})
			if err != nil {
				return nil, err
			}
			taskCache[occurrence.TaskID] = aggregate
		}
		project, exists := projectCache[occurrence.ProjectID]
		if !exists {
			var err error
			project, err = handler.application.GetProject(ctx, taskapp.EntityQueryRequest{
				WorkspaceID: identity.workspaceID, ActorID: identity.actorID, EntityID: occurrence.ProjectID,
			})
			if err != nil {
				return nil, err
			}
			projectCache[occurrence.ProjectID] = project
		}
		version, found := legacyGeneratedScheduleVersion(aggregate.Versions, occurrence.GeneratedScheduleRevision)
		if !found || aggregate.Task.WorkspaceID != identity.workspaceID ||
			aggregate.Schedule.WorkspaceID != identity.workspaceID || project.Project.WorkspaceID != identity.workspaceID {
			return nil, taskapp.ErrInvalidRuntime
		}
		projected, err := legacytaskadapter.ProjectLegacyTask(legacytaskadapter.LegacyTaskProjectionSnapshot{
			Project: project, Task: aggregate.Task, Schedule: version,
			ScheduleHeaderRevision: aggregate.Schedule.Revision, Occurrence: occurrence,
		})
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, projected)
	}
	return tasks, nil
}

func filterLegacyWebOverdueWindow(
	occurrences []taskdomain.QueryOccurrenceSnapshot,
	cutoff time.Time,
) []taskdomain.QueryOccurrenceSnapshot {
	result := make([]taskdomain.QueryOccurrenceSnapshot, 0, len(occurrences))
	for _, occurrence := range occurrences {
		if occurrence.DueAt == nil || !occurrence.DueAt.Before(cutoff) {
			result = append(result, occurrence)
		}
	}
	return result
}

func sortLegacyWebTodayTasks(tasks []legacytaskadapter.LegacyTask) {
	sort.SliceStable(tasks, func(left, right int) bool {
		if tasks[left].SortOrder != tasks[right].SortOrder {
			return tasks[left].SortOrder < tasks[right].SortOrder
		}
		return tasks[left].OccurrenceID < tasks[right].OccurrenceID
	})
}

func legacyWebSummaryTask(
	occurrence taskdomain.QueryOccurrenceSnapshot,
	project taskdomain.ProjectSnapshot,
) model.TaskSummary {
	completedAt := occurrence.CompletedAt.Unix()
	id := occurrence.OccurrenceID
	if id == "" {
		id = occurrence.TaskID
	}
	projectType := "regular"
	if project.Project.Kind == taskdomain.ProjectKindLearning {
		projectType = "learning"
	}
	result := model.TaskSummary{
		ID: id, Title: occurrence.Title, Done: 1, CompletedAt: &completedAt,
		Project: &model.TaskProject{ID: project.Project.ID, Name: project.Project.Name, Type: projectType},
	}
	if occurrence.PlannedDate != "" {
		plannedDate := occurrence.PlannedDate
		result.PlannedDate = &plannedDate
	}
	if occurrence.DueAt != nil {
		due := occurrence.DueAt.Unix()
		result.Due = &due
	}
	noteID := occurrence.OccurrenceNoteID
	if noteID == "" {
		noteID = occurrence.TaskNoteID
	}
	if noteID != "" {
		result.NoteID = &noteID
	}
	if occurrence.Recurring {
		result.ExecutionType = legacytaskadapter.LegacyExecutionRecurring
		result.OccurrenceDate = occurrence.OccurrenceKey
	} else {
		result.ExecutionType = legacytaskadapter.LegacyExecutionSingle
	}
	return result
}

func legacyWebSummaryGroups(tasks []model.TaskSummary, location *time.Location) []model.DateGroup {
	groups := make([]model.DateGroup, 0)
	indexByDate := make(map[string]int)
	for _, task := range tasks {
		date := task.CompletedAt
		if date == nil {
			continue
		}
		dateKey := time.Unix(*date, 0).In(location).Format("2006-01-02")
		index, exists := indexByDate[dateKey]
		if !exists {
			index = len(groups)
			indexByDate[dateKey] = index
			groups = append(groups, model.DateGroup{Date: dateKey, Tasks: make([]model.TaskSummary, 0)})
		}
		groups[index].Tasks = append(groups[index].Tasks, task)
		groups[index].Count++
	}
	return groups
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
