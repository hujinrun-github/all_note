package router

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/handler"
	"github.com/hujinrun/flowspace/internal/storage"
	storagesqlite "github.com/hujinrun/flowspace/internal/storage/sqlite"
	"github.com/hujinrun/flowspace/internal/taskruntime"
	"github.com/hujinrun/flowspace/internal/tenantruntime"
)

// TestTaskDomainV2HTTPSQLiteSmoke is an isolated deployment-shaped smoke test:
// authentication remains in the control store while every v2 command/query is
// resolved per request against a separately migrated tenant database. It never
// runs the cutover coordinator and never touches a real workspace.
func TestTaskDomainV2HTTPSQLiteSmoke(t *testing.T) {
	const (
		primaryToken       = "task-domain-v2-e2e-primary"
		secondaryToken     = "task-domain-v2-e2e-secondary"
		secondaryWorkspace = "router_e2e_workspace_b"
	)

	env := setupRouterAuthEnv(t, false)
	env.config.ControlStore = env.store
	createRouterSession(t, env, primaryToken)
	createTaskDomainWorkspaceSession(t, env, secondaryWorkspace, "router_e2e_user_b", "router_e2e_session_b", secondaryToken)

	tenantConfig := storage.Config{
		Env: "test", Driver: storage.DriverSQLite, Name: "task-domain-v2-e2e",
		SQLitePath: filepath.Join(t.TempDir(), "task-domain-v2-tenant.db"),
	}
	provider := storagesqlite.Provider{}
	if err := provider.MigrateTenant(t.Context(), tenantConfig); err != nil {
		t.Fatalf("migrate isolated tenant: %v", err)
	}
	seedTaskDomainV2SmokeTenant(t, tenantConfig.SQLitePath, routerTestWorkspaceID, secondaryWorkspace)

	snapshots := map[string]tenantruntime.Snapshot{
		routerTestWorkspaceID: taskDomainV2SmokeSnapshot(routerTestWorkspaceID),
		secondaryWorkspace:    taskDomainV2SmokeSnapshot(secondaryWorkspace),
	}
	source := &taskDomainV2SmokeRuntimeSource{snapshots: snapshots}
	endpoints := &taskDomainV2SmokeEndpointSource{config: tenantConfig}
	factory, err := taskruntime.NewFactory(endpoints, taskruntime.ExpectedTenantSchemaVersion, taskDomainV2SmokeRejectPostgres)
	if err != nil {
		t.Fatalf("assemble task runtime factory: %v", err)
	}
	tenantResolver, err := tenantruntime.NewResolver(source, factory)
	if err != nil {
		t.Fatalf("assemble tenant resolver: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := tenantResolver.Close(); closeErr != nil {
			t.Errorf("close tenant resolver: %v", closeErr)
		}
	})
	applicationResolver, err := taskruntime.NewResolver(tenantResolver)
	if err != nil {
		t.Fatalf("assemble application resolver: %v", err)
	}
	modelSelector, err := taskruntime.NewModelSelector(tenantResolver)
	if err != nil {
		t.Fatalf("assemble durable model selector: %v", err)
	}

	env.config.TaskDomainV2Runtime = applicationResolver
	env.config.TaskDomainModelSelector = modelSelector
	server := httptest.NewServer(Setup(env.config))
	t.Cleanup(server.Close)
	client := server.Client()
	primaryCookie := &http.Cookie{Name: env.auth.Cookie.Name, Value: primaryToken}
	secondaryCookie := &http.Cookie{Name: env.auth.Cookie.Name, Value: secondaryToken}

	capability := taskDomainV2SmokeRawRequest[struct {
		ModelVersion string `json:"model_version"`
		Available    bool   `json:"available"`
	}](t, client, server.URL, primaryCookie, http.MethodGet, "/api/task-domain/capabilities", nil, http.StatusOK)
	if capability.ModelVersion != "v2" || !capability.Available {
		t.Fatalf("durable capability = %#v, want available v2", capability)
	}

	standard := taskDomainV2SmokeCreateProject(t, client, server.URL, primaryCookie, "交付项目", "standard", "short")
	learning := taskDomainV2SmokeCreateProject(t, client, server.URL, primaryCookie, "学习项目", "learning", "long")
	if standard.Kind != "standard" || learning.Kind != "learning" {
		t.Fatalf("created project kinds = %q/%q", standard.Kind, learning.Kind)
	}

	today := time.Now().UTC().Format("2006-01-02")
	tomorrow := time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02")
	twoDaysLater := time.Now().UTC().AddDate(0, 0, 2).Format("2006-01-02")
	inboxTask := taskDomainV2SmokeCreateTask(t, client, server.URL, primaryCookie, map[string]any{
		"project_id": "system-inbox", "title": "整理收件箱", "priority": 1,
		"schedule": map[string]any{"recurrence_type": "none", "timing_type": "unscheduled", "timezone": "UTC"},
	})
	dateTask := taskDomainV2SmokeCreateTask(t, client, server.URL, primaryCookie, map[string]any{
		"project_id": standard.ID, "title": "全天交付", "priority": 2,
		"schedule": map[string]any{"recurrence_type": "none", "timing_type": "date", "timezone": "UTC", "starts_on": tomorrow},
	})
	timeBlockTask := taskDomainV2SmokeCreateTask(t, client, server.URL, primaryCookie, map[string]any{
		"project_id": learning.ID, "title": "专注学习", "priority": 2,
		"schedule": map[string]any{"recurrence_type": "none", "timing_type": "time_block", "timezone": "UTC", "starts_on": today, "local_start_time": "10:00", "duration_minutes": 60},
	})
	recurringTask := taskDomainV2SmokeCreateTask(t, client, server.URL, primaryCookie, map[string]any{
		"project_id": standard.ID, "title": "每日复盘", "priority": 1,
		"schedule": map[string]any{"recurrence_type": "daily", "timing_type": "date", "timezone": "UTC", "starts_on": today, "ends_on": twoDaysLater, "rule": map[string]any{"interval": 1}},
	})
	if len(inboxTask.Occurrences) != 1 || inboxTask.Task.ProjectID != "system-inbox" || inboxTask.Occurrences[0].PlannedDate != "" {
		t.Fatalf("inbox task = %#v", inboxTask)
	}
	if len(dateTask.Occurrences) != 1 || len(timeBlockTask.Occurrences) != 1 || len(recurringTask.Occurrences) != 3 {
		t.Fatalf("materialized occurrence counts date/time/recurring = %d/%d/%d", len(dateTask.Occurrences), len(timeBlockTask.Occurrences), len(recurringTask.Occurrences))
	}

	workflowTaskRevision := recurringTask.Task.Revision
	workflowScheduleRevision := recurringTask.Task.ScheduleRevision
	publish := taskDomainV2SmokeRequest[handler.TaskAggregateCommandResponse](t, client, server.URL, primaryCookie, http.MethodPost,
		"/api/tasks/"+recurringTask.Task.ID+"/publish", map[string]any{
			"expected_task_revision": workflowTaskRevision, "expected_schedule_revision": workflowScheduleRevision,
		}, http.StatusOK)
	workflowTaskRevision = publish.TaskRevision
	occurrence := recurringTask.Occurrences[0]
	commands := []struct {
		name       string
		wantStatus string
		extra      map[string]any
	}{
		{name: "start", wantStatus: "active"},
		{name: "block", wantStatus: "blocked", extra: map[string]any{"blocked_reason": "等待评审", "next_action": "提醒评审人"}},
		{name: "unblock", wantStatus: "active"},
		{name: "complete", wantStatus: "done"},
	}
	for _, command := range commands {
		body := map[string]any{
			"expected_task_revision":        workflowTaskRevision,
			"expected_schedule_revision":    workflowScheduleRevision,
			"expected_occurrence_revisions": map[string]int64{occurrence.ID: occurrence.Revision},
		}
		for key, value := range command.extra {
			body[key] = value
		}
		outcome := taskDomainV2SmokeRequest[handler.TaskAggregateCommandResponse](t, client, server.URL, primaryCookie, http.MethodPost,
			"/api/task-occurrences/"+occurrence.ID+"/"+command.name, body, http.StatusOK)
		workflowTaskRevision = outcome.TaskRevision
		occurrence.Revision = outcome.OccurrenceRevisions[occurrence.ID]
		readOccurrence := taskDomainV2SmokeRequest[struct {
			Occurrence handler.OccurrenceV2DTO `json:"occurrence"`
		}](t, client, server.URL, primaryCookie, http.MethodGet, "/api/task-occurrences/"+occurrence.ID, nil, http.StatusOK)
		if string(readOccurrence.Occurrence.ExecutionStatus) != command.wantStatus || readOccurrence.Occurrence.Revision != occurrence.Revision {
			t.Fatalf("occurrence after %s = %#v", command.name, readOccurrence.Occurrence)
		}
		if command.name == "block" && (readOccurrence.Occurrence.BlockedReason != "等待评审" || readOccurrence.Occurrence.NextAction != "提醒评审人") {
			t.Fatalf("blocked metadata = %#v", readOccurrence.Occurrence)
		}
	}
	if occurrence.Revision != recurringTask.Occurrences[0].Revision+4 {
		t.Fatalf("occurrence workflow revision = %d, want %d", occurrence.Revision, recurringTask.Occurrences[0].Revision+4)
	}
	readTask := taskDomainV2SmokeRequest[struct {
		Task handler.TaskV2DTO `json:"task"`
	}](t, client, server.URL, primaryCookie, http.MethodGet, "/api/tasks/"+recurringTask.Task.ID, nil, http.StatusOK)
	if readTask.Task.LifecycleStatus != "active" || readTask.Task.Revision != workflowTaskRevision {
		t.Fatalf("one completed recurring occurrence changed task incorrectly: %#v", readTask.Task)
	}

	calendar := taskDomainV2SmokeRequest[struct {
		Entries []handler.CalendarEntryV2DTO `json:"entries"`
	}](t, client, server.URL, primaryCookie, http.MethodGet,
		fmt.Sprintf("/api/calendar/entries?from=%s&to=%s&timezone=UTC", today, time.Now().UTC().AddDate(0, 0, 4).Format("2006-01-02")), nil, http.StatusOK)
	entryTypes := make(map[string]string, len(calendar.Entries))
	for _, entry := range calendar.Entries {
		entryTypes[entry.TaskID] = string(entry.TimingType)
	}
	if entryTypes[dateTask.Task.ID] != "date" || entryTypes[timeBlockTask.Task.ID] != "time_block" || entryTypes[recurringTask.Task.ID] != "date" {
		t.Fatalf("calendar timing projection = %#v", entryTypes)
	}
	if _, unscheduledOnCalendar := entryTypes[inboxTask.Task.ID]; unscheduledOnCalendar {
		t.Fatalf("unscheduled inbox task appeared on calendar: %#v", entryTypes)
	}

	statuses := taskDomainV2SmokeConcurrentProjectPatches(t, client, server.URL, primaryCookie, standard)
	if len(statuses) != 2 || statuses[0] != http.StatusOK || statuses[1] != http.StatusConflict {
		t.Fatalf("concurrent old-revision statuses = %#v, want [200 409]", statuses)
	}

	secondaryProjects := taskDomainV2SmokeRequest[struct {
		Projects []handler.ProjectV2DTO `json:"projects"`
	}](t, client, server.URL, secondaryCookie, http.MethodGet, "/api/projects", nil, http.StatusOK)
	for _, project := range secondaryProjects.Projects {
		if project.ID == standard.ID || project.ID == learning.ID {
			t.Fatalf("secondary workspace observed primary project: %#v", project)
		}
	}
	secondaryTasks := taskDomainV2SmokeRequest[struct {
		Tasks []handler.TaskV2DTO `json:"tasks"`
	}](t, client, server.URL, secondaryCookie, http.MethodGet, "/api/tasks", nil, http.StatusOK)
	if len(secondaryTasks.Tasks) != 0 {
		t.Fatalf("secondary workspace observed primary tasks: %#v", secondaryTasks.Tasks)
	}
	if endpoints.callsFor(routerTestWorkspaceID) == 0 || endpoints.callsFor(secondaryWorkspace) == 0 {
		t.Fatalf("request-scoped endpoint resolutions = %#v", endpoints.callCounts())
	}
}

type taskDomainV2SmokeRuntimeSource struct {
	mu        sync.Mutex
	snapshots map[string]tenantruntime.Snapshot
}

func (source *taskDomainV2SmokeRuntimeSource) LoadVersion(_ context.Context, workspaceID string) (tenantruntime.Version, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	snapshot, ok := source.snapshots[workspaceID]
	if !ok {
		return tenantruntime.Version{}, errors.New("unknown isolated workspace")
	}
	return snapshot.Version, nil
}

func (source *taskDomainV2SmokeRuntimeSource) LoadSnapshot(_ context.Context, expected tenantruntime.Version) (tenantruntime.Snapshot, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	snapshot, ok := source.snapshots[expected.WorkspaceID]
	if !ok || snapshot.Version != expected {
		return tenantruntime.Snapshot{}, errors.New("isolated runtime version changed")
	}
	return snapshot, nil
}

type taskDomainV2SmokeEndpointSource struct {
	mu     sync.Mutex
	config storage.Config
	calls  map[string]int
}

func (source *taskDomainV2SmokeEndpointSource) LoadDatabaseEndpointConfig(_ context.Context, workspaceID, endpointID string) (taskruntime.DatabaseEndpointConfig, error) {
	if endpointID != "database" {
		return taskruntime.DatabaseEndpointConfig{}, errors.New("unexpected endpoint")
	}
	source.mu.Lock()
	defer source.mu.Unlock()
	if source.calls == nil {
		source.calls = make(map[string]int)
	}
	source.calls[workspaceID]++
	return taskruntime.DatabaseEndpointConfig{
		WorkspaceID: workspaceID, EndpointID: endpointID, ProfileVersionID: "isolated-sqlite-v1", Storage: source.config,
	}, nil
}

func (source *taskDomainV2SmokeEndpointSource) callsFor(workspaceID string) int {
	source.mu.Lock()
	defer source.mu.Unlock()
	return source.calls[workspaceID]
}

func (source *taskDomainV2SmokeEndpointSource) callCounts() map[string]int {
	source.mu.Lock()
	defer source.mu.Unlock()
	copy := make(map[string]int, len(source.calls))
	for workspaceID, count := range source.calls {
		copy[workspaceID] = count
	}
	return copy
}

func taskDomainV2SmokeSnapshot(workspaceID string) tenantruntime.Snapshot {
	return tenantruntime.Snapshot{
		Version:            tenantruntime.Version{WorkspaceID: workspaceID, Mode: "active", Epoch: 1, BindingRevision: 1},
		DatabaseEndpointID: "database", ObjectEndpointID: "objects", ChatMode: "disabled", TranscriptionMode: "disabled",
	}
}

func taskDomainV2SmokeRejectPostgres(context.Context, string, string) (net.Conn, error) {
	return nil, errors.New("isolated SQLite smoke attempted a PostgreSQL connection")
}

func seedTaskDomainV2SmokeTenant(t *testing.T, path string, workspaceIDs ...string) {
	t.Helper()
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, workspaceID := range workspaceIDs {
		if _, err := db.Exec(`INSERT INTO tenant_workspaces(workspace_id,epoch,state) VALUES(?,1,'active')`, workspaceID); err != nil {
			t.Fatalf("seed tenant anchor %s: %v", workspaceID, err)
		}
		for _, project := range []struct{ id, name, role string }{
			{id: "system-inbox", name: "收件箱", role: "inbox"},
			{id: "system-personal", name: "个人", role: "personal"},
		} {
			if _, err := db.Exec(`INSERT INTO domain_projects_v2
				(workspace_id,id,name,kind,horizon,status,system_role,revision,created_at,updated_at)
				VALUES(?,?,?,'standard','short','active',?,1,?,?)`, workspaceID, project.id, project.name, project.role, now, now); err != nil {
				t.Fatalf("seed system project %s/%s: %v", workspaceID, project.id, err)
			}
		}
	}
}

type taskDomainV2SmokeCreateTaskResponse struct {
	Task        handler.TaskV2DTO         `json:"task"`
	Occurrences []handler.OccurrenceV2DTO `json:"occurrences"`
}

func taskDomainV2SmokeCreateProject(t *testing.T, client *http.Client, baseURL string, cookie *http.Cookie, name, kind, horizon string) handler.ProjectV2DTO {
	t.Helper()
	result := taskDomainV2SmokeRequest[struct {
		Project handler.ProjectV2DTO `json:"project"`
	}](t, client, baseURL, cookie, http.MethodPost, "/api/projects", map[string]any{
		"name": name, "kind": kind, "horizon": horizon, "status": "active",
	}, http.StatusCreated)
	return result.Project
}

func taskDomainV2SmokeCreateTask(t *testing.T, client *http.Client, baseURL string, cookie *http.Cookie, body map[string]any) taskDomainV2SmokeCreateTaskResponse {
	t.Helper()
	return taskDomainV2SmokeRequest[taskDomainV2SmokeCreateTaskResponse](t, client, baseURL, cookie, http.MethodPost, "/api/tasks", body, http.StatusCreated)
}

func taskDomainV2SmokeConcurrentProjectPatches(t *testing.T, client *http.Client, baseURL string, cookie *http.Cookie, project handler.ProjectV2DTO) []int {
	t.Helper()
	statuses := make(chan int, 2)
	var wait sync.WaitGroup
	for _, name := range []string{"并发修改 A", "并发修改 B"} {
		wait.Add(1)
		go func(name string) {
			defer wait.Done()
			body, _ := json.Marshal(map[string]any{"name": name, "expected_project_revision": project.Revision})
			request, err := http.NewRequest(http.MethodPatch, baseURL+"/api/projects/"+project.ID, bytes.NewReader(body))
			if err != nil {
				statuses <- 0
				return
			}
			request.Header.Set("Content-Type", "application/json")
			request.AddCookie(cookie)
			response, err := client.Do(request)
			if err != nil {
				statuses <- 0
				return
			}
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			statuses <- response.StatusCode
		}(name)
	}
	wait.Wait()
	close(statuses)
	result := make([]int, 0, 2)
	for status := range statuses {
		result = append(result, status)
	}
	sort.Ints(result)
	return result
}

func taskDomainV2SmokeRequest[T any](t *testing.T, client *http.Client, baseURL string, cookie *http.Cookie, method, path string, body any, wantStatus int) T {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode %s %s: %v", method, path, err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(t.Context(), method, baseURL+path, reader)
	if err != nil {
		t.Fatalf("build %s %s: %v", method, path, err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.AddCookie(cookie)
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("perform %s %s: %v", method, path, err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read %s %s: %v", method, path, err)
	}
	if response.StatusCode != wantStatus {
		t.Fatalf("%s %s status = %d, want %d; body=%s", method, path, response.StatusCode, wantStatus, responseBody)
	}
	var envelope struct {
		Data T `json:"data"`
	}
	if err := json.Unmarshal(responseBody, &envelope); err != nil {
		t.Fatalf("decode %s %s: %v; body=%s", method, path, err, responseBody)
	}
	return envelope.Data
}

func taskDomainV2SmokeRawRequest[T any](t *testing.T, client *http.Client, baseURL string, cookie *http.Cookie, method, path string, body any, wantStatus int) T {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode %s %s: %v", method, path, err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(t.Context(), method, baseURL+path, reader)
	if err != nil {
		t.Fatalf("build %s %s: %v", method, path, err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.AddCookie(cookie)
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("perform %s %s: %v", method, path, err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read %s %s: %v", method, path, err)
	}
	if response.StatusCode != wantStatus {
		t.Fatalf("%s %s status = %d, want %d; body=%s", method, path, response.StatusCode, wantStatus, responseBody)
	}
	var decoded T
	if err := json.Unmarshal(responseBody, &decoded); err != nil {
		t.Fatalf("decode %s %s: %v; body=%s", method, path, err, responseBody)
	}
	return decoded
}
