package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	authpkg "github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/taskapp"
	"github.com/hujinrun/flowspace/internal/taskdomain"
	"github.com/hujinrun/flowspace/internal/tenantruntime"
)

func TestRegisterTaskDomainV2RoutesDispatchesCommandsWithAuthenticatedScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	application := &taskDomainV2ApplicationFake{}
	router := isolatedTaskDomainV2Router(application, true)

	cases := []struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus int
		wantCall   string
	}{
		{"create project", http.MethodPost, "/api/projects", `{"name":"Learn","kind":"learning","horizon":"long","status":"planning"}`, http.StatusCreated, "create-project"},
		{"update project", http.MethodPatch, "/api/projects/project-1", `{"name":"Learn Go","expected_project_revision":2}`, http.StatusOK, "patch-project"},
		{"complete project", http.MethodPost, "/api/projects/project-1/complete", `{"expected_project_revision":2}`, http.StatusOK, "complete-project"},
		{"archive project", http.MethodPost, "/api/projects/project-1/archive", `{"expected_project_revision":3}`, http.StatusOK, "archive-project"},
		{"delete project", http.MethodDelete, "/api/projects/project-1", `{"expected_project_revision":4}`, http.StatusOK, "delete-project"},
		{"create task", http.MethodPost, "/api/tasks", `{"project_id":"project-1","title":"Write tests","priority":2,"schedule":{"recurrence_type":"none","timing_type":"unscheduled","timezone":"Asia/Shanghai"}}`, http.StatusCreated, "create-task"},
		{"patch task", http.MethodPatch, "/api/tasks/task-1", `{"expected_task_revision":2,"expected_schedule_revision":3,"title":"Updated"}`, http.StatusOK, "patch-task"},
		{"publish task", http.MethodPost, "/api/tasks/task-1/publish", revisionsJSON("occurrence-1"), http.StatusOK, "task-publish"},
		{"pause task", http.MethodPost, "/api/tasks/task-1/pause", revisionsJSON("occurrence-1"), http.StatusOK, "task-pause"},
		{"resume task", http.MethodPost, "/api/tasks/task-1/resume", revisionsJSON("occurrence-1"), http.StatusOK, "task-resume"},
		{"cancel task", http.MethodPost, "/api/tasks/task-1/cancel", revisionsJSON("occurrence-1"), http.StatusOK, "task-cancel"},
		{"restore task", http.MethodPost, "/api/tasks/task-1/restore", revisionsJSON("occurrence-1"), http.StatusOK, "task-restore"},
		{"archive task", http.MethodPost, "/api/tasks/task-1/archive", revisionsJSON("occurrence-1"), http.StatusOK, "task-archive"},
		{"start occurrence", http.MethodPost, "/api/task-occurrences/occurrence-1/start", revisionsJSON("occurrence-1"), http.StatusOK, "occurrence-start"},
		{"block occurrence", http.MethodPost, "/api/task-occurrences/occurrence-1/block", strings.TrimSuffix(revisionsJSON("occurrence-1"), "}") + `,"blocked_reason":"waiting","next_action":"ask"}`, http.StatusOK, "occurrence-block"},
		{"unblock occurrence", http.MethodPost, "/api/task-occurrences/occurrence-1/unblock", revisionsJSON("occurrence-1"), http.StatusOK, "occurrence-unblock"},
		{"complete occurrence", http.MethodPost, "/api/task-occurrences/occurrence-1/complete", revisionsJSON("occurrence-1"), http.StatusOK, "occurrence-complete"},
		{"skip occurrence", http.MethodPost, "/api/task-occurrences/occurrence-1/skip", revisionsJSON("occurrence-1"), http.StatusOK, "occurrence-skip"},
		{"cancel occurrence", http.MethodPost, "/api/task-occurrences/occurrence-1/cancel", revisionsJSON("occurrence-1"), http.StatusOK, "occurrence-cancel"},
		{"reopen occurrence", http.MethodPost, "/api/task-occurrences/occurrence-1/reopen", revisionsJSON("occurrence-1"), http.StatusOK, "occurrence-reopen"},
		{"reschedule only this", http.MethodPatch, "/api/task-occurrences/occurrence-1/schedule/only-this", `{"expected_task_revision":2,"expected_schedule_revision":3,"expected_occurrence_revision":4,"timing":{"timing_type":"date","timezone":"Asia/Shanghai","planned_date":"2026-07-23"}}`, http.StatusOK, "schedule-only-this"},
		{"reschedule occurrence compatibility", http.MethodPatch, "/api/task-occurrences/occurrence-1", `{"expected_task_revision":2,"expected_schedule_revision":3,"expected_occurrence_revision":4,"timing":{"timing_type":"date","timezone":"Asia/Shanghai","planned_date":"2026-07-23"}}`, http.StatusOK, "schedule-only-this"},
		{"reschedule this and following", http.MethodPost, "/api/tasks/task-1/schedule/this-and-following", `{"expected_task_revision":2,"expected_schedule_revision":3,"effective_from":"2026-07-23","generate_through_exclusive":"2026-08-23","schedule":{"recurrence_type":"daily","timing_type":"date","timezone":"Asia/Shanghai","starts_on":"2026-07-23","rule":{"interval":1}}}`, http.StatusOK, "schedule-following"},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			application.reset()
			response := performTaskDomainV2Request(router, test.method, test.path, test.body)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, test.wantStatus, response.Body.String())
			}
			if application.lastCall != test.wantCall {
				t.Fatalf("call = %q, want %q", application.lastCall, test.wantCall)
			}
			if application.workspaceID != "workspace-1" || application.actorID != "user-1" {
				t.Fatalf("scope = %q/%q", application.workspaceID, application.actorID)
			}
			assertTaskDomainDataEnvelope(t, response)
		})
	}
}

func TestTaskDomainV2ReadRoutesReturnExactResourceEnvelopes(t *testing.T) {
	application := &taskDomainV2ApplicationFake{}
	router := isolatedTaskDomainV2Router(application, true)
	tests := []struct {
		name     string
		path     string
		wantCall string
		dataKey  string
	}{
		{"list projects", "/api/projects?kind=standard&horizon=short&status=active", "list-projects", "projects"},
		{"get project", "/api/projects/project-1", "get-project", "project"},
		{"list tasks", "/api/tasks?project_id=project-1&lifecycle_status=active", "list-tasks", "tasks"},
		{"get task", "/api/tasks/task-1", "get-task", "task"},
		{"get occurrence", "/api/task-occurrences/occurrence-1", "get-occurrence", "occurrence"},
		{"calendar", "/api/calendar/entries?from=2026-07-01&to=2026-08-01&timezone=Asia%2FShanghai&project_id=project-1", "calendar-entries", "entries"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			application.reset()
			response := performTaskDomainV2Request(router, http.MethodGet, test.path, "")
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
			}
			if application.lastCall != test.wantCall || application.workspaceID != "workspace-1" || application.actorID != "user-1" {
				t.Fatalf("call/scope = %q %q/%q", application.lastCall, application.workspaceID, application.actorID)
			}
			var envelope struct {
				Data map[string]json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if len(envelope.Data[test.dataKey]) == 0 {
				t.Fatalf("missing data.%s: %s", test.dataKey, response.Body.String())
			}
		})
	}
}

func TestTaskDomainV2CalendarResponseCarriesIndependentRevisions(t *testing.T) {
	application := &taskDomainV2ApplicationFake{}
	response := performTaskDomainV2Request(isolatedTaskDomainV2Router(application, true), http.MethodGet,
		"/api/calendar/entries?from=2026-07-01&to=2026-08-01", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
	}
	var envelope struct {
		Data struct {
			Entries []CalendarEntryV2DTO `json:"entries"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data.Entries) != 1 || envelope.Data.Entries[0].ProjectRevision != 7 ||
		envelope.Data.Entries[0].TaskRevision != 2 || envelope.Data.Entries[0].OccurrenceRevision != 4 {
		t.Fatalf("calendar entries = %#v", envelope.Data.Entries)
	}
}

func TestTaskDomainV2OccurrenceQueriesUseIsolatedApplicationPort(t *testing.T) {
	application := &taskDomainV2ApplicationFake{}
	router := isolatedTaskDomainV2Router(application, true)

	response := performTaskDomainV2Request(router, http.MethodGet,
		"/api/task-occurrences?scope=calendar&from=2026-07-22T00:00:00Z&to=2026-07-23T00:00:00Z&timezone=Asia%2FShanghai&project_id=project-1&execution_status=open,blocked&recurring=true", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
	}
	if application.lastCall != "list-occurrences" || application.workspaceID != "workspace-1" || application.actorID != "user-1" {
		t.Fatalf("query call/scope = %q %q/%q", application.lastCall, application.workspaceID, application.actorID)
	}
	if application.listRequest.Scope != taskdomain.OccurrenceListCalendar || application.listRequest.ProjectID != "project-1" || application.listRequest.Recurring == nil || !*application.listRequest.Recurring {
		t.Fatalf("filter = %#v", application.listRequest)
	}
	if got := application.listRequest.Statuses; len(got) != 2 || got[0] != taskdomain.ExecutionStatusOpen || got[1] != taskdomain.ExecutionStatusBlocked {
		t.Fatalf("statuses = %#v", got)
	}
	assertTaskDomainDataEnvelope(t, response)
}

func TestTaskDomainV2OccurrenceQuerySupportsTaskIDOnly(t *testing.T) {
	application := &taskDomainV2ApplicationFake{}
	response := performTaskDomainV2Request(isolatedTaskDomainV2Router(application, true), http.MethodGet, "/api/task-occurrences?task_id=task-1", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
	}
	if application.listRequest.TaskID != "task-1" || application.listRequest.Scope != taskdomain.OccurrenceListAll {
		t.Fatalf("query = %#v", application.listRequest)
	}
}

func TestTaskDomainV2CommandResponsesExposeIndependentRevisions(t *testing.T) {
	application := &taskDomainV2ApplicationFake{}
	router := isolatedTaskDomainV2Router(application, true)

	response := performTaskDomainV2Request(router, http.MethodPost, "/api/task-occurrences/occurrence-1/start", revisionsJSON("occurrence-1"))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
	}
	var envelope struct {
		Data TaskAggregateCommandResponse `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if envelope.Data.TaskRevision != 2 || envelope.Data.ScheduleRevision == nil || *envelope.Data.ScheduleRevision != 3 || envelope.Data.OccurrenceRevisions["occurrence-1"] != 5 {
		t.Fatalf("revisions = %#v", envelope.Data)
	}
}

func TestTaskDomainV2CreateTaskReturnsMaterializedOccurrencesFromFacade(t *testing.T) {
	application := &taskDomainV2ApplicationFake{}
	response := performTaskDomainV2Request(isolatedTaskDomainV2Router(application, true), http.MethodPost, "/api/tasks",
		`{"project_id":"project-1","title":"Write tests","priority":2,"sort_order":1.5,"schedule":{"recurrence_type":"none","timing_type":"unscheduled","timezone":"UTC"}}`)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
	}
	var envelope struct {
		Data struct {
			Task        TaskV2DTO         `json:"task"`
			Occurrences []OccurrenceV2DTO `json:"occurrences"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.Task.ID != "task-created" || envelope.Data.Task.SortOrder != 1.5 || application.createRequest.SortOrder != 1.5 ||
		len(envelope.Data.Occurrences) != 1 || envelope.Data.Occurrences[0].ID != "occurrence-created" {
		t.Fatalf("create response = %#v", envelope.Data)
	}
	listResponse := performTaskDomainV2Request(isolatedTaskDomainV2Router(application, true), http.MethodGet, "/api/tasks", "")
	var listEnvelope struct {
		Data struct {
			Tasks []TaskV2DTO `json:"tasks"`
		} `json:"data"`
	}
	if err := json.Unmarshal(listResponse.Body.Bytes(), &listEnvelope); err != nil {
		t.Fatal(err)
	}
	if listResponse.Code != http.StatusOK || len(listEnvelope.Data.Tasks) != 1 || listEnvelope.Data.Tasks[0].SortOrder != 1.5 {
		t.Fatalf("list sort_order round trip = %d %#v", listResponse.Code, listEnvelope.Data.Tasks)
	}
}

func TestTaskDomainV2RoutesRejectUnknownFieldsAndUnauthenticatedRequests(t *testing.T) {
	application := &taskDomainV2ApplicationFake{}
	authenticated := isolatedTaskDomainV2Router(application, true)

	unknown := performTaskDomainV2Request(authenticated, http.MethodPost, "/api/projects", `{"name":"A","kind":"standard","horizon":"short","status":"planning","workspace_id":"other"}`)
	assertTaskDomainError(t, unknown, http.StatusBadRequest, "invalid_request")
	if application.lastCall != "" {
		t.Fatalf("application called for unknown field: %q", application.lastCall)
	}

	forbiddenLifecyclePatch := performTaskDomainV2Request(authenticated, http.MethodPost, "/api/tasks/task-1/pause",
		`{"expected_task_revision":2,"expected_schedule_revision":3,"expected_occurrence_revisions":{"occurrence-1":4},"lifecycle_status":"paused"}`)
	assertTaskDomainError(t, forbiddenLifecyclePatch, http.StatusBadRequest, "invalid_request")
	forbiddenOrdinaryPatch := performTaskDomainV2Request(authenticated, http.MethodPatch, "/api/tasks/task-1",
		`{"expected_task_revision":2,"expected_schedule_revision":3,"lifecycle_status":"paused"}`)
	assertTaskDomainError(t, forbiddenOrdinaryPatch, http.StatusBadRequest, "invalid_request")
	forbiddenSchedulePatch := performTaskDomainV2Request(authenticated, http.MethodPatch, "/api/tasks/task-1",
		`{"expected_task_revision":2,"expected_schedule_revision":3,"title":"Task","schedule":{"timing_type":"date"}}`)
	assertTaskDomainError(t, forbiddenSchedulePatch, http.StatusBadRequest, "invalid_request")
	forbiddenAmbiguousNotePatch := performTaskDomainV2Request(authenticated, http.MethodPatch, "/api/tasks/task-1",
		`{"expected_task_revision":2,"expected_schedule_revision":3,"note_id":"note-1"}`)
	assertTaskDomainError(t, forbiddenAmbiguousNotePatch, http.StatusBadRequest, "invalid_request")
	forbiddenExecutionPatch := performTaskDomainV2Request(authenticated, http.MethodPatch, "/api/task-occurrences/occurrence-1",
		`{"expected_task_revision":2,"expected_schedule_revision":3,"expected_occurrence_revision":4,"timing":{"timing_type":"unscheduled","timezone":"UTC"},"execution_status":"done"}`)
	assertTaskDomainError(t, forbiddenExecutionPatch, http.StatusBadRequest, "invalid_request")

	wrongOccurrenceRevision := performTaskDomainV2Request(authenticated, http.MethodPost, "/api/task-occurrences/occurrence-1/start", revisionsJSON("other-occurrence"))
	assertTaskDomainError(t, wrongOccurrenceRevision, http.StatusBadRequest, "invalid_request")

	missingBlockMetadata := performTaskDomainV2Request(authenticated, http.MethodPost, "/api/task-occurrences/occurrence-1/block", revisionsJSON("occurrence-1"))
	assertTaskDomainError(t, missingBlockMetadata, http.StatusBadRequest, "invalid_request")

	unauthenticated := isolatedTaskDomainV2Router(application, false)
	missingIdentity := performTaskDomainV2Request(unauthenticated, http.MethodPost, "/api/projects", `{"name":"A","kind":"standard","horizon":"short","status":"planning"}`)
	assertTaskDomainError(t, missingIdentity, http.StatusUnauthorized, "unauthorized")
}

func TestTaskDomainV2PatchTaskForwardsAllMutableFieldsAndReturnsAfterImage(t *testing.T) {
	application := &taskDomainV2ApplicationFake{}
	response := performTaskDomainV2Request(isolatedTaskDomainV2Router(application, true), http.MethodPatch, "/api/tasks/task-1",
		`{"expected_task_revision":5,"expected_schedule_revision":3,"title":"After","description":"Details","priority":2,"sort_order":1.5,"project_id":"project-2","roadmap_node_id":"","task_note_id":""}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
	}
	request := application.patchRequest
	if request.ExpectedTaskRevision != 5 || request.ExpectedScheduleRevision != 3 || request.Title == nil || *request.Title != "After" ||
		request.Description == nil || *request.Description != "Details" || request.Priority == nil || *request.Priority != 2 ||
		request.SortOrder == nil || *request.SortOrder != 1.5 || request.ProjectID == nil || *request.ProjectID != "project-2" ||
		request.RoadmapNodeID == nil || *request.RoadmapNodeID != "" || request.NoteID == nil || *request.NoteID != "" {
		t.Fatalf("patch request = %#v", request)
	}
	var envelope struct {
		Data struct {
			Task TaskV2DTO `json:"task"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.Task.ID != "task-1" || envelope.Data.Task.Title != "After" || envelope.Data.Task.SortOrder != 1.5 ||
		envelope.Data.Task.Revision != 6 || envelope.Data.Task.ScheduleRevision != 3 {
		t.Fatalf("patch after-image = %#v", envelope.Data.Task)
	}
	performTaskDomainV2Request(isolatedTaskDomainV2Router(application, true), http.MethodPatch, "/api/tasks/task-1",
		`{"expected_task_revision":6,"expected_schedule_revision":3,"title":"Retained links"}`)
	if application.patchRequest.NoteID != nil || application.patchRequest.RoadmapNodeID != nil || application.patchRequest.ProjectID != nil {
		t.Fatalf("omitted links must remain absent: %#v", application.patchRequest)
	}
}

func TestTaskDomainV2RoutesReturnRevisionConflictWithoutFallback(t *testing.T) {
	application := &taskDomainV2ApplicationFake{err: taskdomain.ErrProjectRevisionConflict}
	router := isolatedTaskDomainV2Router(application, true)

	response := performTaskDomainV2Request(router, http.MethodPost, "/api/projects/project-1/complete", `{"expected_project_revision":2}`)
	assertTaskDomainError(t, response, http.StatusConflict, "revision_conflict")
	if application.calls != 1 {
		t.Fatalf("application calls = %d, want exactly one", application.calls)
	}
}

func TestTaskDomainV2RoutesPreserveTenantFenceErrors(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"epoch", storage.ErrTenantEpochMismatch, http.StatusConflict, "tenant_epoch_mismatch"},
		{"fenced", storage.ErrTenantWorkspaceFenced, http.StatusServiceUnavailable, "tenant_workspace_fenced"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			application := &taskDomainV2ApplicationFake{err: test.err}
			router := isolatedTaskDomainV2Router(application, true)
			response := performTaskDomainV2Request(router, http.MethodPost, "/api/tasks/task-1/pause", revisionsJSON("occurrence-1"))
			assertTaskDomainError(t, response, test.status, test.code)
			if application.calls != 1 {
				t.Fatalf("application calls = %d, want exactly one", application.calls)
			}
		})
	}
}

func TestTaskDomainV2RoutesMapApplicationBoundaryErrors(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"invalid query", taskdomain.ErrInvalidOccurrenceListFilter, http.StatusBadRequest, "invalid_request"},
		{"invalid project filter", taskdomain.ErrInvalidProjectListFilter, http.StatusBadRequest, "invalid_request"},
		{"invalid task filter", taskdomain.ErrInvalidTaskDefinitionListFilter, http.StatusBadRequest, "invalid_request"},
		{"invalid aggregate", taskdomain.ErrInvalidTaskAggregateSnapshot, http.StatusBadRequest, "invalid_request"},
		{"runtime unavailable", taskapp.ErrInvalidRuntime, http.StatusServiceUnavailable, "task_domain_unavailable"},
		{"tenant runtime unavailable", tenantruntime.ErrRuntimeUnavailable, http.StatusServiceUnavailable, "task_domain_unavailable"},
		{"tenant runtime inactive", tenantruntime.ErrRuntimeNotActive, http.StatusServiceUnavailable, "task_domain_unavailable"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			application := &taskDomainV2ApplicationFake{err: test.err}
			response := performTaskDomainV2Request(isolatedTaskDomainV2Router(application, true), http.MethodGet, "/api/task-occurrences", "")
			assertTaskDomainError(t, response, test.status, test.code)
		})
	}
}

func isolatedTaskDomainV2Router(application TaskDomainV2Application, authenticated bool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	if authenticated {
		router.Use(func(c *gin.Context) {
			ctx := authpkg.ContextWithIdentity(c.Request.Context(), authpkg.RequestIdentity{UserID: "user-1", WorkspaceID: "workspace-1"})
			ctx = authpkg.ContextWithWorkspaceScope(ctx, "workspace-1")
			c.Request = c.Request.WithContext(ctx)
			c.Next()
		})
	}
	RegisterTaskDomainV2Routes(router.Group("/api"), application)
	return router
}

func performTaskDomainV2Request(router http.Handler, method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func revisionsJSON(occurrenceID string) string {
	return `{"expected_task_revision":2,"expected_schedule_revision":3,"expected_occurrence_revisions":{"` + occurrenceID + `":4}}`
}

func assertTaskDomainDataEnvelope(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	var body map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body["data"]) == 0 {
		t.Fatalf("response has no data: %s", response.Body.String())
	}
}

func assertTaskDomainError(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, status, response.Body.String())
	}
	var body TaskDomainErrorResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if body.Error.Code != code {
		t.Fatalf("error code = %q, want %q", body.Error.Code, code)
	}
}

type taskDomainV2ApplicationFake struct {
	err           error
	calls         int
	lastCall      string
	workspaceID   string
	actorID       string
	listRequest   taskapp.OccurrenceQueryRequest
	patchRequest  taskapp.PatchTaskRequest
	createRequest taskapp.CreateTaskRequest
}

func (fake *taskDomainV2ApplicationFake) reset() {
	fake.err, fake.calls, fake.lastCall, fake.workspaceID, fake.actorID = nil, 0, "", "", ""
}

func (fake *taskDomainV2ApplicationFake) ListProjects(_ context.Context, request taskapp.ListProjectsRequest) (taskapp.ProjectListResult, error) {
	if err := fake.record("list-projects", request.WorkspaceID, request.ActorID); err != nil {
		return nil, err
	}
	return []taskdomain.ProjectSnapshot{{Project: taskdomain.Project{WorkspaceID: request.WorkspaceID, ID: "project-1", Name: "Project", Kind: taskdomain.ProjectKindStandard, Horizon: taskdomain.ProjectHorizonShort, Status: taskdomain.ProjectStatusActive}, Revision: 2}}, nil
}

func (fake *taskDomainV2ApplicationFake) GetProject(_ context.Context, request taskapp.EntityQueryRequest) (taskdomain.ProjectSnapshot, error) {
	if err := fake.record("get-project", request.WorkspaceID, request.ActorID); err != nil {
		return taskdomain.ProjectSnapshot{}, err
	}
	return taskdomain.ProjectSnapshot{Project: taskdomain.Project{WorkspaceID: request.WorkspaceID, ID: request.EntityID, Name: "Project", Kind: taskdomain.ProjectKindStandard, Horizon: taskdomain.ProjectHorizonShort, Status: taskdomain.ProjectStatusActive}, Revision: 2}, nil
}

func (fake *taskDomainV2ApplicationFake) ListTaskDefinitions(_ context.Context, request taskapp.ListTaskDefinitionsRequest) (taskapp.TaskDefinitionListResult, error) {
	if err := fake.record("list-tasks", request.WorkspaceID, request.ActorID); err != nil {
		return nil, err
	}
	return taskapp.TaskDefinitionListResult{taskReadModelFake(request.WorkspaceID, "task-1")}, nil
}

func (fake *taskDomainV2ApplicationFake) GetTask(_ context.Context, request taskapp.EntityQueryRequest) (taskdomain.TaskAggregateQueryResult, error) {
	if err := fake.record("get-task", request.WorkspaceID, request.ActorID); err != nil {
		return taskdomain.TaskAggregateQueryResult{}, err
	}
	model := taskReadModelFake(request.WorkspaceID, request.EntityID)
	return taskdomain.TaskAggregateQueryResult{Task: model.Task, Schedule: taskdomain.ScheduleHeader{WorkspaceID: request.WorkspaceID, TaskID: request.EntityID, Revision: model.ScheduleRevision}}, nil
}

func (fake *taskDomainV2ApplicationFake) PatchTask(_ context.Context, request taskapp.PatchTaskRequest) (taskapp.TaskCommandOutcome, error) {
	if err := fake.record("patch-task", request.WorkspaceID, request.ActorID); err != nil {
		return taskapp.TaskCommandOutcome{}, err
	}
	fake.patchRequest = request
	return taskapp.TaskCommandOutcome{Task: taskdomain.TaskRecord{WorkspaceID: request.WorkspaceID, ID: request.TaskID, ProjectID: "project-2", Title: "After", Description: "Details", Priority: 2, SortOrder: 1.5, LifecycleStatus: taskdomain.TaskLifecycleActive, Revision: request.ExpectedTaskRevision + 1}, TaskRevision: request.ExpectedTaskRevision + 1, ScheduleRevision: request.ExpectedScheduleRevision}, nil
}

func (fake *taskDomainV2ApplicationFake) GetOccurrence(_ context.Context, request taskapp.EntityQueryRequest) (taskdomain.QueryOccurrenceSnapshot, error) {
	if err := fake.record("get-occurrence", request.WorkspaceID, request.ActorID); err != nil {
		return taskdomain.QueryOccurrenceSnapshot{}, err
	}
	return occurrenceSnapshotFake(request.WorkspaceID, request.EntityID), nil
}

func (fake *taskDomainV2ApplicationFake) CalendarEntries(_ context.Context, request taskapp.CalendarEntriesRequest) (taskapp.CalendarEntriesResult, error) {
	if err := fake.record("calendar-entries", request.WorkspaceID, request.ActorID); err != nil {
		return nil, err
	}
	return taskapp.CalendarEntriesResult{{Occurrence: occurrenceSnapshotFake(request.WorkspaceID, "occurrence-1"), ProjectRevision: 7}}, nil
}

func (fake *taskDomainV2ApplicationFake) record(call, workspaceID, actorID string) error {
	fake.calls++
	fake.lastCall, fake.workspaceID, fake.actorID = call, workspaceID, actorID
	return fake.err
}

func (fake *taskDomainV2ApplicationFake) CreateProject(_ context.Context, request taskapp.CreateProjectRequest) (taskapp.ProjectCommandOutcome, error) {
	if err := fake.record("create-project", request.WorkspaceID, request.ActorID); err != nil {
		return taskapp.ProjectCommandOutcome{}, err
	}
	return projectFakeOutcome("project-created", request.Name, request.Kind, request.Horizon, request.Status, 1), nil
}

func (fake *taskDomainV2ApplicationFake) PatchProject(_ context.Context, request taskapp.PatchProjectRequest) (taskapp.ProjectCommandOutcome, error) {
	if err := fake.record("patch-project", request.WorkspaceID, request.ActorID); err != nil {
		return taskapp.ProjectCommandOutcome{}, err
	}
	return projectFakeOutcome(request.ProjectID, "Learn Go", taskdomain.ProjectKindLearning, taskdomain.ProjectHorizonLong, taskdomain.ProjectStatusPlanning, request.ExpectedProjectRevision+1), nil
}

func (fake *taskDomainV2ApplicationFake) CompleteProject(_ context.Context, request taskapp.ExistingProjectRequest) (taskapp.ProjectCommandOutcome, error) {
	if err := fake.record("complete-project", request.WorkspaceID, request.ActorID); err != nil {
		return taskapp.ProjectCommandOutcome{}, err
	}
	return projectFakeOutcome(request.ProjectID, "P", taskdomain.ProjectKindStandard, taskdomain.ProjectHorizonShort, taskdomain.ProjectStatusCompleted, request.ExpectedProjectRevision+1), nil
}

func (fake *taskDomainV2ApplicationFake) ArchiveProject(_ context.Context, request taskapp.ExistingProjectRequest) (taskapp.ProjectCommandOutcome, error) {
	if err := fake.record("archive-project", request.WorkspaceID, request.ActorID); err != nil {
		return taskapp.ProjectCommandOutcome{}, err
	}
	return projectFakeOutcome(request.ProjectID, "P", taskdomain.ProjectKindStandard, taskdomain.ProjectHorizonShort, taskdomain.ProjectStatusArchived, request.ExpectedProjectRevision+1), nil
}

func (fake *taskDomainV2ApplicationFake) DeleteProject(_ context.Context, request taskapp.ExistingProjectRequest) (taskapp.ProjectCommandOutcome, error) {
	if err := fake.record("delete-project", request.WorkspaceID, request.ActorID); err != nil {
		return taskapp.ProjectCommandOutcome{}, err
	}
	return taskapp.ProjectCommandOutcome{Project: taskdomain.Project{ID: request.ProjectID}, Revision: request.ExpectedProjectRevision + 1, Deleted: true}, nil
}

func (fake *taskDomainV2ApplicationFake) CreateTask(_ context.Context, request taskapp.CreateTaskRequest) (taskapp.CreateTaskResult, error) {
	if err := fake.record("create-task", request.WorkspaceID, request.ActorID); err != nil {
		return taskapp.CreateTaskResult{}, err
	}
	fake.createRequest = request
	return taskapp.CreateTaskResult{TaskID: "task-created", TaskRevision: 1, ScheduleRevision: 1, LifecycleStatus: taskdomain.TaskLifecycleDraft,
		Occurrences: []taskdomain.OccurrenceRecord{{WorkspaceID: request.WorkspaceID, ID: "occurrence-created", TaskID: "task-created", OccurrenceKey: "once", ExecutionStatus: taskdomain.ExecutionStatusOpen, Revision: 1, GeneratedScheduleRevision: 1}}}, nil
}

func (fake *taskDomainV2ApplicationFake) ExecuteTaskLifecycle(_ context.Context, request taskapp.TaskLifecycleRequest) (taskapp.TaskCommandOutcome, error) {
	if err := fake.record("task-"+string(request.Command), request.WorkspaceID, request.ActorID); err != nil {
		return taskapp.TaskCommandOutcome{}, err
	}
	return taskapp.TaskCommandOutcome{TaskRevision: request.Expected.Task + 1, ScheduleRevision: request.Expected.Schedule, LifecycleStatus: taskdomain.TaskLifecycleActive}, nil
}

func (fake *taskDomainV2ApplicationFake) ExecuteOccurrenceByID(_ context.Context, request taskapp.OccurrenceByIDRequest) (taskapp.OccurrenceCommandOutcome, error) {
	if err := fake.record("occurrence-"+string(request.Command), request.WorkspaceID, request.ActorID); err != nil {
		return taskapp.OccurrenceCommandOutcome{}, err
	}
	return taskapp.OccurrenceCommandOutcome{TaskRevision: request.Expected.Task, ScheduleRevision: request.Expected.Schedule, OccurrenceRevision: request.Expected.Occurrence + 1, ExecutionStatus: taskdomain.ExecutionStatusActive}, nil
}

func (fake *taskDomainV2ApplicationFake) RescheduleOccurrence(_ context.Context, request taskapp.RescheduleOccurrenceRequest) (taskapp.ScheduleCommandOutcome, error) {
	if err := fake.record("schedule-only-this", request.WorkspaceID, request.ActorID); err != nil {
		return taskapp.ScheduleCommandOutcome{}, err
	}
	return taskapp.ScheduleCommandOutcome{TaskRevision: request.ExpectedTaskRevision, ScheduleRevision: request.ExpectedScheduleRevision, OccurrenceRevision: request.ExpectedOccurrenceRevision + 1}, nil
}

func (fake *taskDomainV2ApplicationFake) RescheduleThisAndFollowing(_ context.Context, request taskapp.RescheduleThisAndFollowingRequest) (taskapp.ScheduleCommandOutcome, error) {
	if err := fake.record("schedule-following", request.WorkspaceID, request.ActorID); err != nil {
		return taskapp.ScheduleCommandOutcome{}, err
	}
	return taskapp.ScheduleCommandOutcome{TaskRevision: request.ExpectedTaskRevision, ScheduleRevision: request.ExpectedScheduleRevision + 1, ScheduleVersion: 2}, nil
}

func (fake *taskDomainV2ApplicationFake) ListOccurrences(_ context.Context, request taskapp.OccurrenceQueryRequest) ([]taskdomain.QueryOccurrenceSnapshot, error) {
	if err := fake.record("list-occurrences", request.WorkspaceID, request.ActorID); err != nil {
		return nil, err
	}
	fake.listRequest = request
	return []taskdomain.QueryOccurrenceSnapshot{{WorkspaceID: request.WorkspaceID, ProjectID: "project-1", TaskID: "task-1", OccurrenceID: "occurrence-1", OccurrenceKey: "once", Title: "Task", TimingType: taskdomain.TimingUnscheduled, Timezone: "Asia/Shanghai", Status: taskdomain.ExecutionStatusOpen, Revision: 4, TaskRevision: 2, ScheduleRevision: 3, GeneratedScheduleRevision: 1, LifecycleStatus: taskdomain.TaskLifecycleActive}}, nil
}

func projectFakeOutcome(id, name string, kind taskdomain.ProjectKind, horizon taskdomain.ProjectHorizon, status taskdomain.ProjectStatus, revision int64) taskapp.ProjectCommandOutcome {
	return taskapp.ProjectCommandOutcome{Project: taskdomain.Project{ID: id, Name: name, Kind: kind, Horizon: horizon, Status: status}, Revision: revision}
}

func taskReadModelFake(workspaceID, taskID string) taskdomain.TaskDefinitionSnapshot {
	return taskdomain.TaskDefinitionSnapshot{Task: taskdomain.TaskRecord{WorkspaceID: workspaceID, ID: taskID, ProjectID: "project-1", Title: "Task", Priority: 1, SortOrder: 1.5, LifecycleStatus: taskdomain.TaskLifecycleActive, Revision: 2}, ScheduleRevision: 3}
}

func occurrenceSnapshotFake(workspaceID, occurrenceID string) taskdomain.QueryOccurrenceSnapshot {
	return taskdomain.QueryOccurrenceSnapshot{WorkspaceID: workspaceID, ProjectID: "project-1", TaskID: "task-1", OccurrenceID: occurrenceID,
		OccurrenceKey: "once", Title: "Task", TimingType: taskdomain.TimingUnscheduled, Timezone: "UTC", Status: taskdomain.ExecutionStatusOpen,
		Revision: 4, TaskRevision: 2, ScheduleRevision: 3, GeneratedScheduleRevision: 1, LifecycleStatus: taskdomain.TaskLifecycleActive}
}

var _ TaskDomainV2Application = (*taskDomainV2ApplicationFake)(nil)
var _ = errors.New
var _ = time.Time{}
