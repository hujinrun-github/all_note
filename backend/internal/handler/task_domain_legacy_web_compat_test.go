package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	authpkg "github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/taskapp"
	"github.com/hujinrun/flowspace/internal/taskdomain"
)

func TestLegacyWebV2EventsProjectsDateAndTimeBlockWithoutLegacyStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	start := time.Date(2026, 7, 22, 1, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	application := &legacyWebApplicationFake{
		workspaceID: "workspace-1",
		occurrences: []taskdomain.QueryOccurrenceSnapshot{
			legacyWebOccurrence("workspace-1", "task-time", "occ-time", taskdomain.TimingTimeBlock, "2026-07-22", &start, &end, ""),
			legacyWebOccurrence("workspace-1", "task-date", "occ-date", taskdomain.TimingDate, "2026-07-23", nil, nil, "2026-07-26"),
		},
		tasks: map[string]taskdomain.TaskAggregateQueryResult{
			"task-time": legacyWebTask("workspace-1", "task-time", taskdomain.TimingTimeBlock, "2026-07-22", "09:00:00", 60),
			"task-date": legacyWebTask("workspace-1", "task-date", taskdomain.TimingDate, "2026-07-23", "", 0),
		},
	}
	router := legacyWebCompatRouter(application)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/events?month=2026-07&timezone=Asia/Shanghai", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	var envelope struct {
		Data struct {
			Events []struct {
				ID            string `json:"id"`
				IsAllDay      bool   `json:"is_all_day"`
				PlannedDate   string `json:"planned_date"`
				AllDayEndDate string `json:"all_day_end_date"`
				StartTime     *int64 `json:"start_time"`
			} `json:"events"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data.Events) != 2 || envelope.Data.Events[0].ID != "occ-time" || envelope.Data.Events[0].StartTime == nil {
		t.Fatalf("time-block projection = %#v", envelope.Data.Events)
	}
	if event := envelope.Data.Events[1]; event.ID != "occ-date" || !event.IsAllDay || event.PlannedDate != "2026-07-23" || event.AllDayEndDate != "2026-07-26" || event.StartTime != nil {
		t.Fatalf("all-day projection = %#v", event)
	}
	if application.listCalls != 1 || application.getTaskCalls != 2 {
		t.Fatalf("application calls list=%d get-task=%d", application.listCalls, application.getTaskCalls)
	}
}

func TestLegacyWebV2TasksKeepIndependentRevisionsAndWorkspaceScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	application := &legacyWebApplicationFake{
		workspaceID: "workspace-1",
		occurrences: []taskdomain.QueryOccurrenceSnapshot{
			legacyWebOccurrence("workspace-1", "task-1", "occ-1", taskdomain.TimingDate, "2026-07-22", nil, nil, ""),
		},
		tasks: map[string]taskdomain.TaskAggregateQueryResult{
			"task-1": legacyWebTask("workspace-1", "task-1", taskdomain.TimingDate, "2026-07-22", "", 0),
		},
		projects: map[string]taskdomain.ProjectSnapshot{
			"project-1": {Project: taskdomain.Project{WorkspaceID: "workspace-1", ID: "project-1", Name: "Personal", Kind: taskdomain.ProjectKindStandard, Horizon: taskdomain.ProjectHorizonShort, Status: taskdomain.ProjectStatusActive}, Revision: 7},
		},
	}
	router := legacyWebCompatRouter(application)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/tasks?page=1&page_size=20&status=all", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	for _, fragment := range []string{`"task_revision":2`, `"schedule_revision":3`, `"occurrence_revision":4`, `"workspace_id":"workspace-1"`} {
		if !strings.Contains(response.Body.String(), fragment) {
			t.Fatalf("body = %s, want %s", response.Body.String(), fragment)
		}
	}

	application.occurrences[0].WorkspaceID = "workspace-2"
	mismatch := httptest.NewRecorder()
	router.ServeHTTP(mismatch, httptest.NewRequest(http.MethodGet, "/api/tasks?page=1", nil))
	if mismatch.Code != http.StatusServiceUnavailable || !strings.Contains(mismatch.Body.String(), `"code":"legacy_projection_unavailable"`) {
		t.Fatalf("workspace mismatch status/body = %d %s", mismatch.Code, mismatch.Body.String())
	}
}

func TestLegacyWebV2MutationsFailWithStableRevisionRequiredInsteadOfLastWriteWins(t *testing.T) {
	gin.SetMode(gin.TestMode)
	application := &legacyWebApplicationFake{workspaceID: "workspace-1"}
	router := legacyWebCompatRouter(application)
	requests := []struct{ method, path, body string }{
		{http.MethodPost, "/api/tasks", `{"title":"old client"}`},
		{http.MethodPatch, "/api/tasks/task-1", `{"title":"old client"}`},
		{http.MethodDelete, "/api/tasks/task-1", ``},
		{http.MethodPost, "/api/tasks/task-1/occurrences/2026-07-22/complete", ``},
		{http.MethodPost, "/api/tasks/task-1/occurrences/2026-07-22/reopen", ``},
		{http.MethodPost, "/api/tasks/task-1/occurrences/2026-07-22/skip", ``},
		{http.MethodPost, "/api/events", `{"title":"old event","start_time":1,"end_time":2}`},
		{http.MethodPatch, "/api/events/event-1", `{"title":"old event"}`},
		{http.MethodDelete, "/api/events/event-1", ``},
	}
	for _, request := range requests {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(request.method, request.path, strings.NewReader(request.body)))
		if response.Code != http.StatusGone || !strings.Contains(response.Body.String(), `"code":"legacy_contract_revision_required"`) {
			t.Fatalf("%s %s status/body = %d %s", request.method, request.path, response.Code, response.Body.String())
		}
	}
	if application.listCalls != 0 || application.getTaskCalls != 0 || application.getProjectCalls != 0 {
		t.Fatalf("unsafe mutation touched v2 application: %#v", application)
	}
}

func TestLegacyWebV2UnknownOrUnrepresentableStateHasStableError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	occurrence := legacyWebOccurrence("workspace-1", "task-1", "occ-1", taskdomain.TimingDate, "2026-07-22", nil, nil, "")
	occurrence.Status = taskdomain.ExecutionStatusSkipped
	application := &legacyWebApplicationFake{
		workspaceID: "workspace-1", occurrences: []taskdomain.QueryOccurrenceSnapshot{occurrence},
		tasks:    map[string]taskdomain.TaskAggregateQueryResult{"task-1": legacyWebTask("workspace-1", "task-1", taskdomain.TimingDate, "2026-07-22", "", 0)},
		projects: map[string]taskdomain.ProjectSnapshot{"project-1": {Project: taskdomain.Project{WorkspaceID: "workspace-1", ID: "project-1", Name: "Personal", Kind: taskdomain.ProjectKindStandard, Horizon: taskdomain.ProjectHorizonShort, Status: taskdomain.ProjectStatusActive}, Revision: 7}},
	}
	response := httptest.NewRecorder()
	legacyWebCompatRouter(application).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/tasks?page=1", nil))
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"code":"legacy_state_unrepresentable"`) {
		t.Fatalf("status/body = %d %s", response.Code, response.Body.String())
	}
}

func legacyWebCompatRouter(application LegacyWebTaskDomainApplication) *gin.Engine {
	router := gin.New()
	router.Use(func(c *gin.Context) {
		ctx := authpkg.ContextWithIdentity(c.Request.Context(), authpkg.RequestIdentity{UserID: "user-1", WorkspaceID: "workspace-1"})
		ctx = authpkg.ContextWithWorkspaceScope(ctx, "workspace-1")
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	RegisterLegacyWebTaskDomainV2Routes(router.Group("/api"), application)
	return router
}

type legacyWebApplicationFake struct {
	workspaceID     string
	occurrences     []taskdomain.QueryOccurrenceSnapshot
	tasks           map[string]taskdomain.TaskAggregateQueryResult
	projects        map[string]taskdomain.ProjectSnapshot
	listCalls       int
	getTaskCalls    int
	getProjectCalls int
}

func (fake *legacyWebApplicationFake) ListOccurrences(_ context.Context, request taskapp.OccurrenceQueryRequest) ([]taskdomain.QueryOccurrenceSnapshot, error) {
	fake.listCalls++
	if request.WorkspaceID != fake.workspaceID {
		return nil, taskapp.ErrInvalidRuntime
	}
	return append([]taskdomain.QueryOccurrenceSnapshot(nil), fake.occurrences...), nil
}

func (fake *legacyWebApplicationFake) GetTask(_ context.Context, request taskapp.EntityQueryRequest) (taskdomain.TaskAggregateQueryResult, error) {
	fake.getTaskCalls++
	return fake.tasks[request.EntityID], nil
}

func (fake *legacyWebApplicationFake) GetProject(_ context.Context, request taskapp.EntityQueryRequest) (taskdomain.ProjectSnapshot, error) {
	fake.getProjectCalls++
	return fake.projects[request.EntityID], nil
}

func legacyWebOccurrence(workspaceID, taskID, occurrenceID string, timing taskdomain.TimingType, date string, start, end *time.Time, allDayEnd string) taskdomain.QueryOccurrenceSnapshot {
	return taskdomain.QueryOccurrenceSnapshot{
		WorkspaceID: workspaceID, ProjectID: "project-1", TaskID: taskID, OccurrenceID: occurrenceID, OccurrenceKey: "once",
		Title: "Task", Description: "Details", TimingType: timing, Timezone: "Asia/Shanghai", PlannedDate: date,
		PlannedStartAt: start, PlannedEndAt: end, Status: taskdomain.ExecutionStatusOpen, Revision: 4,
		ProjectRevision: 7, TaskRevision: 2, ScheduleRevision: 3, GeneratedScheduleRevision: 1,
		LifecycleStatus: taskdomain.TaskLifecycleActive, Priority: 1, AllDayEndDate: allDayEnd,
	}
}

func legacyWebTask(workspaceID, taskID string, timing taskdomain.TimingType, startsOn, localStart string, duration int) taskdomain.TaskAggregateQueryResult {
	return taskdomain.TaskAggregateQueryResult{
		Task:     taskdomain.TaskRecord{WorkspaceID: workspaceID, ID: taskID, ProjectID: "project-1", Title: "Task", Description: "Details", LifecycleStatus: taskdomain.TaskLifecycleActive, Priority: 1, Revision: 2},
		Schedule: taskdomain.ScheduleHeader{WorkspaceID: workspaceID, TaskID: taskID, Revision: 3, CurrentScheduleRevision: 1},
		Versions: []taskdomain.ScheduleVersion{{WorkspaceID: workspaceID, TaskID: taskID, ScheduleRevision: 1, RecurrenceType: taskdomain.RecurrenceNone, TimingType: timing, Timezone: "Asia/Shanghai", StartsOn: startsOn, LocalStartTime: localStart, DurationMinutes: duration}},
	}
}
