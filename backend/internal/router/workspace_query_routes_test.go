package router

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/provisioning"
	"github.com/hujinrun/flowspace/internal/storage"
	storagesqlite "github.com/hujinrun/flowspace/internal/storage/sqlite"
	"github.com/hujinrun/flowspace/internal/taskruntime"
	"github.com/hujinrun/flowspace/internal/tenantruntime"
)

const (
	workspaceQueryDate        = "2026-11-19"
	workspaceQueryWindow      = "date=2026-11-19&timezone=Asia%2FShanghai"
	workspaceQuerySecondaryID = "workspace_query_secondary"
)

type workspaceQueryHTTPFixture struct {
	env                *routerAuthEnv
	server             *httptest.Server
	primary, secondary *http.Cookie
	tenantPath         string
}

type workspaceQueryHTTPProject struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Summary struct {
		CompletedTaskCount       int  `json:"completed_task_count"`
		TotalActionableTaskCount int  `json:"total_actionable_task_count"`
		OpenOccurrenceCount      int  `json:"open_occurrence_count"`
		BlockedOccurrenceCount   int  `json:"blocked_occurrence_count"`
		HasOpenEndedRecurrence   bool `json:"has_open_ended_recurrence"`
		CompletionPercent        *int `json:"completion_percent"`
		NextOccurrence           *struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"next_occurrence"`
	} `json:"summary"`
}

type workspaceQueryHTTPOverview struct {
	WorkspaceID      string         `json:"workspace_id"`
	TaskModelVersion int            `json:"task_model_version"`
	RuntimeEpoch     int64          `json:"runtime_epoch"`
	Date             string         `json:"date"`
	Timezone         string         `json:"timezone"`
	ReadConsistency  string         `json:"read_consistency"`
	Facets           map[string]int `json:"facets"`
	Today            []struct {
		ID          string `json:"id"`
		Title       string `json:"title"`
		ProjectName string `json:"project_name"`
	} `json:"today"`
	Projects []workspaceQueryHTTPProject `json:"projects"`
	Notes    struct {
		State     string `json:"state"`
		Authority string `json:"authority"`
		Items     []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"items"`
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	} `json:"notes"`
	Partial bool `json:"partial"`
}

func TestWorkspaceQueryRoutesRequireAuthenticationAndV2Runtime(t *testing.T) {
	env := setupRouterAuthEnv(t, false)
	createRouterSession(t, env, "workspace-query-no-runtime")
	routes := Setup(env.config)
	for _, path := range []string{
		"/api/workspace/overview?" + workspaceQueryWindow,
		"/api/task-workbench/facets?" + workspaceQueryWindow,
		"/api/projects?include=summary&" + workspaceQueryWindow,
		"/api/projects/system-personal/overview?" + workspaceQueryWindow,
	} {
		t.Run(path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, path, nil)
			response := httptest.NewRecorder()
			routes.ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("unauthenticated status=%d body=%s", response.Code, response.Body.String())
			}
			request = httptest.NewRequest(http.MethodGet, path, nil)
			request.AddCookie(&http.Cookie{Name: env.auth.Cookie.Name, Value: "workspace-query-no-runtime"})
			response = httptest.NewRecorder()
			routes.ServeHTTP(response, request)
			if response.Code != http.StatusServiceUnavailable {
				t.Fatalf("missing v2 runtime status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestWorkspaceQueryHTTPUsesCanonicalTenantSummariesAndCompatibilityNotes(t *testing.T) {
	fixture := newWorkspaceQueryHTTPFixture(t)
	client := fixture.server.Client()
	baseURL := fixture.server.URL
	overview := taskDomainV2SmokeRequest[workspaceQueryHTTPOverview](t, client, baseURL, fixture.primary,
		http.MethodGet, "/api/workspace/overview?"+workspaceQueryWindow+"&today_limit=2&project_limit=1&notes_limit=1", nil, http.StatusOK)
	if overview.WorkspaceID != routerTestWorkspaceID || overview.TaskModelVersion != 2 || overview.RuntimeEpoch != 1 ||
		overview.Date != workspaceQueryDate || overview.Timezone != "Asia/Shanghai" || overview.ReadConsistency != "planning_snapshot" {
		t.Fatalf("overview scope=%#v", overview)
	}
	for name, expected := range map[string]int{"today": 3, "week": 4, "inbox": 1, "completed_today": 1, "draft": 1} {
		if overview.Facets[name] != expected {
			t.Fatalf("facet %s=%d want %d; all=%#v", name, overview.Facets[name], expected, overview.Facets)
		}
	}
	if len(overview.Today) != 2 || len(overview.Projects) != 1 {
		t.Fatalf("bounded overview today=%d projects=%d", len(overview.Today), len(overview.Projects))
	}
	for _, occurrence := range overview.Today {
		if occurrence.ProjectName == "" || strings.Contains(occurrence.Title, "另一工作区") {
			t.Fatalf("missing joined project or leaked occurrence: %#v", occurrence)
		}
	}
	if overview.Partial || overview.Notes.State != "available" || overview.Notes.Authority != "platform_compatibility" ||
		len(overview.Notes.Items) != 1 || overview.Notes.Items[0].Title != "兼容库笔记" {
		t.Fatalf("notes authority or availability changed: %#v", overview)
	}

	project := taskDomainV2SmokeRequest[struct {
		Project workspaceQueryHTTPProject `json:"project"`
	}](t, client, baseURL,
		fixture.primary, http.MethodGet, "/api/projects/shared-project/overview?"+workspaceQueryWindow, nil, http.StatusOK).Project
	if project.Name != "主工作区项目" || project.Summary.CompletedTaskCount != 1 || project.Summary.TotalActionableTaskCount != 4 ||
		project.Summary.OpenOccurrenceCount != 4 || project.Summary.BlockedOccurrenceCount != 1 || !project.Summary.HasOpenEndedRecurrence ||
		project.Summary.CompletionPercent != nil || project.Summary.NextOccurrence == nil {
		t.Fatalf("canonical project summary=%#v", project)
	}

	list := taskDomainV2SmokeRequest[struct {
		Projects []workspaceQueryHTTPProject `json:"projects"`
	}](t, client, baseURL,
		fixture.primary, http.MethodGet, "/api/projects?include=summary&kind=standard&status=active&"+workspaceQueryWindow, nil, http.StatusOK)
	foundProject := false
	for _, item := range list.Projects {
		if item.ID == project.ID {
			foundProject = true
		}
		if item.ID == project.ID && item.Summary.TotalActionableTaskCount != project.Summary.TotalActionableTaskCount {
			t.Fatalf("list/detail summary disagreement: %#v / %#v", item, project)
		}
	}
	if !foundProject {
		t.Fatalf("project summary list omitted canonical project: %#v", list)
	}
	legacyShape := taskDomainV2SmokeRequest[struct {
		Projects []map[string]json.RawMessage `json:"projects"`
	}](t, client, baseURL,
		fixture.primary, http.MethodGet, "/api/projects", nil, http.StatusOK)
	for _, item := range legacyShape.Projects {
		if _, added := item["summary"]; added {
			t.Fatalf("existing project response gained mandatory summary: %#v", item)
		}
	}
	// A real existing v2 command must immediately change aggregate reads. This
	// also verifies that the new query path is not reading a legacy task mirror.
	taskDomainV2SmokeRequest[map[string]any](t, client, baseURL, fixture.primary, http.MethodPost,
		"/api/task-occurrences/shared-occurrence/complete", map[string]any{
			"expected_task_revision":        1,
			"expected_schedule_revision":    1,
			"expected_occurrence_revisions": map[string]int64{"shared-occurrence": 1},
		}, http.StatusOK)
	after := taskDomainV2SmokeRequest[workspaceQueryHTTPOverview](t, client, baseURL, fixture.primary, http.MethodGet,
		"/api/workspace/overview?"+workspaceQueryWindow, nil, http.StatusOK)
	if after.Facets["today"] != 2 {
		t.Fatalf("completed occurrence remained in pending overview: %#v", after.Facets)
	}
	updatedProject := taskDomainV2SmokeRequest[struct {
		Project workspaceQueryHTTPProject `json:"project"`
	}](t, client, baseURL, fixture.primary, http.MethodGet,
		"/api/projects/shared-project/overview?"+workspaceQueryWindow, nil, http.StatusOK).Project
	if updatedProject.Summary.CompletedTaskCount != 2 || updatedProject.Summary.OpenOccurrenceCount != 3 {
		t.Fatalf("summary did not reflect canonical command: %#v", updatedProject)
	}
}

func TestWorkspaceQueryHTTPRejectsForgedScopesAndIsolatesRepeatedEntityIDs(t *testing.T) {
	fixture := newWorkspaceQueryHTTPFixture(t)
	client := fixture.server.Client()
	secondary := taskDomainV2SmokeRequest[workspaceQueryHTTPOverview](t, client, fixture.server.URL, fixture.secondary,
		http.MethodGet, "/api/workspace/overview?"+workspaceQueryWindow, nil, http.StatusOK)
	if secondary.WorkspaceID != workspaceQuerySecondaryID || secondary.Facets["today"] != 1 || len(secondary.Today) != 1 ||
		secondary.Today[0].ID != "shared-occurrence" || secondary.Today[0].Title != "另一工作区事项" {
		t.Fatalf("secondary workspace leaked/failed repeated IDs: %#v", secondary)
	}
	project := taskDomainV2SmokeRequest[struct {
		Project workspaceQueryHTTPProject `json:"project"`
	}](t, client, fixture.server.URL,
		fixture.secondary, http.MethodGet, "/api/projects/shared-project/overview?"+workspaceQueryWindow, nil, http.StatusOK).Project
	if project.Name != "另一工作区项目" || project.Summary.TotalActionableTaskCount != 1 || project.Summary.CompletedTaskCount != 0 ||
		project.Summary.HasOpenEndedRecurrence || project.Summary.CompletionPercent == nil || *project.Summary.CompletionPercent != 0 ||
		project.Summary.NextOccurrence == nil || project.Summary.NextOccurrence.ID != "shared-occurrence" {
		t.Fatalf("same project ID crossed workspace boundary: %#v", project)
	}
	for _, path := range []string{
		"/api/workspace/overview?" + workspaceQueryWindow + "&workspace_id=" + workspaceQuerySecondaryID,
		"/api/task-workbench/facets?" + workspaceQueryWindow + "&workspace_id=" + workspaceQuerySecondaryID,
		"/api/projects?include=summary&" + workspaceQueryWindow + "&workspace_id=" + workspaceQuerySecondaryID,
	} {
		taskDomainV2SmokeRawRequest[map[string]any](t, client, fixture.server.URL, fixture.primary, http.MethodGet, path, nil, http.StatusBadRequest)
	}
	taskDomainV2SmokeRawRequest[map[string]any](t, client, fixture.server.URL, fixture.primary, http.MethodGet,
		"/api/projects/missing/overview?"+workspaceQueryWindow, nil, http.StatusNotFound)
	taskDomainV2SmokeRawRequest[map[string]any](t, client, fixture.server.URL, fixture.primary, http.MethodGet,
		"/api/projects/secondary-only/overview?"+workspaceQueryWindow, nil, http.StatusNotFound)
	taskDomainV2SmokeRawRequest[map[string]any](t, client, fixture.server.URL, fixture.primary, http.MethodGet,
		"/api/task-workbench/facets?"+workspaceQueryWindow+"&project_id=missing", nil, http.StatusNotFound)
}

func TestWorkspaceQueryHTTPValidatesDateTimezoneAndBounds(t *testing.T) {
	fixture := newWorkspaceQueryHTTPFixture(t)
	for _, query := range []string{
		"", "date=2026-11-19", "timezone=UTC", "date=2026-02-30&timezone=UTC",
		"date=2026-11-19&timezone=Local", "date=2026-11-19&timezone=Not%2FAZone",
		workspaceQueryWindow + "&today_limit=0", workspaceQueryWindow + "&today_limit=51",
		workspaceQueryWindow + "&project_limit=-1", workspaceQueryWindow + "&notes_limit=11",
		workspaceQueryWindow + "&today_limit=many",
		workspaceQueryWindow + "&today_limit=",
		workspaceQueryWindow + "&project_limit=",
		workspaceQueryWindow + "&notes_limit=",
		workspaceQueryWindow + "&today_limit=1&today_limit=2",
		workspaceQueryWindow + "&timezone=UTC",
	} {
		t.Run(query, func(t *testing.T) {
			taskDomainV2SmokeRawRequest[map[string]any](t, fixture.server.Client(), fixture.server.URL, fixture.primary, http.MethodGet,
				"/api/workspace/overview?"+query, nil, http.StatusBadRequest)
		})
	}
	for _, query := range []string{
		"include=unsupported&" + workspaceQueryWindow,
		"include=summary&include=unsupported&" + workspaceQueryWindow,
		"include=summary&kind=unknown&" + workspaceQueryWindow,
		"include=summary&status=unknown&" + workspaceQueryWindow,
	} {
		t.Run(query, func(t *testing.T) {
			taskDomainV2SmokeRawRequest[map[string]any](t, fixture.server.Client(), fixture.server.URL, fixture.primary, http.MethodGet,
				"/api/projects?"+query, nil, http.StatusBadRequest)
		})
	}
}

func TestWorkspaceQueryHTTPNotesFailureIsPartialAndStaleEpochFailsClosed(t *testing.T) {
	fixture := newWorkspaceQueryHTTPFixture(t)
	// Remove only compatibility note storage: the control identity and canonical
	// tenant stay usable. A missing content source must not be reported as empty.
	platformDB, err := sql.Open("sqlite", fixture.env.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = platformDB.Exec(`ALTER TABLE notes RENAME TO notes_temporarily_unavailable`); err != nil {
		t.Fatal(err)
	}
	if err = platformDB.Close(); err != nil {
		t.Fatal(err)
	}
	response := taskDomainV2SmokeRequest[workspaceQueryHTTPOverview](t, fixture.server.Client(), fixture.server.URL, fixture.primary,
		http.MethodGet, "/api/workspace/overview?"+workspaceQueryWindow, nil, http.StatusOK)
	if !response.Partial || response.Notes.State != "unavailable" || response.Notes.Items != nil || response.Notes.Error == nil || response.Facets["today"] != 3 {
		t.Fatalf("partial note failure=%#v", response)
	}
	db, err := sql.Open("sqlite", fixture.tenantPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE tenant_workspaces SET epoch=2 WHERE workspace_id=?`, routerTestWorkspaceID); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/workspace/overview?" + workspaceQueryWindow, "/api/projects/shared-project/overview?" + workspaceQueryWindow} {
		taskDomainV2SmokeRawRequest[map[string]any](t, fixture.server.Client(), fixture.server.URL, fixture.primary, http.MethodGet, path, nil, http.StatusServiceUnavailable)
	}
}

func newWorkspaceQueryHTTPFixture(t *testing.T) *workspaceQueryHTTPFixture {
	t.Helper()
	env := setupRouterAuthEnv(t, false)
	env.config.ControlStore = env.store
	createRouterSession(t, env, "workspace-query-primary")
	createTaskDomainWorkspaceSession(t, env, workspaceQuerySecondaryID, "workspace_query_user_b", "workspace_query_session_b", "workspace-query-secondary")
	config := storage.Config{Env: "test", Driver: storage.DriverSQLite, Name: "workspace-query-http", SQLitePath: filepath.Join(t.TempDir(), "tenant.db")}
	if err := (storagesqlite.Provider{}).MigrateTenant(t.Context(), config); err != nil {
		t.Fatal(err)
	}
	seedTaskDomainV2SmokeTenant(t, config.SQLitePath, routerTestWorkspaceID, workspaceQuerySecondaryID)
	seedWorkspaceQueryHTTPData(t, config.SQLitePath)
	context := auth.ContextWithWorkspaceScope(t.Context(), routerTestWorkspaceID)
	if err := provisioning.EnsureDefaultWorkspaceData(context, env.store); err != nil {
		t.Fatalf("seed compatibility content workspace: %v", err)
	}
	if err := env.store.Notes().CreateWithID(context, &model.Note{ID: "shared-note", Title: "兼容库笔记", Body: "兼容内容权威源", Tags: "[]", CreatedAt: 1795046400, UpdatedAt: 1795046400}); err != nil {
		t.Fatalf("seed compatibility note: %v", err)
	}
	source := &taskDomainV2SmokeRuntimeSource{snapshots: map[string]tenantruntime.Snapshot{
		routerTestWorkspaceID: taskDomainV2SmokeSnapshot(routerTestWorkspaceID), workspaceQuerySecondaryID: taskDomainV2SmokeSnapshot(workspaceQuerySecondaryID),
	}}
	endpoints := &taskDomainV2SmokeEndpointSource{config: config}
	factory, err := taskruntime.NewFactory(endpoints, taskruntime.ExpectedTenantSchemaVersion, taskDomainV2SmokeRejectPostgres)
	if err != nil {
		t.Fatal(err)
	}
	tenants, err := tenantruntime.NewResolver(source, factory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := tenants.Close(); err != nil {
			t.Error(err)
		}
	})
	runtime, err := taskruntime.NewResolver(tenants)
	if err != nil {
		t.Fatal(err)
	}
	selector, err := taskruntime.NewModelSelector(tenants)
	if err != nil {
		t.Fatal(err)
	}
	env.config.TaskDomainV2Runtime = runtime
	env.config.TaskDomainModelSelector = selector
	server := httptest.NewServer(Setup(env.config))
	t.Cleanup(server.Close)
	return &workspaceQueryHTTPFixture{env: env, server: server, tenantPath: config.SQLitePath,
		primary: &http.Cookie{Name: env.auth.Cookie.Name, Value: "workspace-query-primary"}, secondary: &http.Cookie{Name: env.auth.Cookie.Name, Value: "workspace-query-secondary"}}
}

func seedWorkspaceQueryHTTPData(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	const now = "2026-11-19T04:00:00Z"
	for _, workspace := range []struct{ id, name string }{{routerTestWorkspaceID, "主工作区项目"}, {workspaceQuerySecondaryID, "另一工作区项目"}} {
		if _, err = tx.Exec(`INSERT INTO domain_projects_v2 (workspace_id,id,name,kind,horizon,status,revision,created_at,updated_at) VALUES(?,'shared-project',?,'standard','short','active',1,?,?)`, workspace.id, workspace.name, now, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = tx.Exec(`INSERT INTO domain_projects_v2 (workspace_id,id,name,kind,horizon,status,revision,created_at,updated_at) VALUES(?,'secondary-only','私有工作区项目','standard','short','active',1,?,?)`, workspaceQuerySecondaryID, now, now); err != nil {
		t.Fatal(err)
	}
	seed := func(workspace, id, title, lifecycle, project, date, status, recurrence string, dates []string) {
		t.Helper()
		if _, err = tx.Exec(`INSERT INTO domain_tasks_v2 (workspace_id,id,project_id,title,lifecycle_status,revision,created_at,updated_at) VALUES(?,?,?,?,?,1,?,?)`, workspace, id, project, title, lifecycle, now, now); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(`INSERT INTO domain_task_schedules_v2 (workspace_id,task_id,revision,current_schedule_revision,updated_at) VALUES(?,?,1,1,?)`, workspace, id, now); err != nil {
			t.Fatal(err)
		}
		timing := "date"
		var starts, effective any = date, nil
		if date == "" {
			timing = "unscheduled"
			starts = nil
		}
		rule := "{}"
		if recurrence != "none" {
			effective = date
			rule = `{"interval":1}`
		}
		if _, err = tx.Exec(`INSERT INTO domain_task_schedule_versions_v2 (workspace_id,task_id,schedule_revision,effective_from,recurrence_type,timing_type,timezone,starts_on,recurrence_rule,created_at) VALUES(?,?,1,?,?,?,'Asia/Shanghai',?,?,?)`, workspace, id, effective, recurrence, timing, starts, rule, now); err != nil {
			t.Fatal(err)
		}
		for i, planned := range dates {
			occurrenceID := id + "-occurrence"
			if id == "shared-task" {
				occurrenceID = "shared-occurrence"
			}
			key := "once"
			if recurrence != "none" {
				key = planned
				occurrenceID = fmt.Sprintf("%s-%d", id, i)
			}
			var plannedDate, completed, reason, next any = planned, nil, nil, nil
			if planned == "" {
				plannedDate = nil
			}
			if status == "done" {
				completed = now
			}
			if status == "blocked" {
				reason = "等待评审"
				next = "联系评审人"
			}
			if _, err = tx.Exec(`INSERT INTO domain_task_occurrences_v2 (workspace_id,id,task_id,occurrence_key,planned_date,execution_status,completed_at,blocked_reason,next_action,revision,generated_schedule_revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,1,1,?,?)`, workspace, occurrenceID, id, key, plannedDate, status, completed, reason, next, now, now); err != nil {
				t.Fatal(err)
			}
		}
	}
	seed(routerTestWorkspaceID, "shared-task", "主工作区事项", "active", "shared-project", workspaceQueryDate, "open", "none", []string{workspaceQueryDate})
	seed(routerTestWorkspaceID, "blocked-task", "受阻事项", "active", "shared-project", workspaceQueryDate, "blocked", "none", []string{workspaceQueryDate})
	seed(routerTestWorkspaceID, "completed-task", "完成事项", "completed", "shared-project", "2026-11-18", "done", "none", []string{"2026-11-18"})
	seed(routerTestWorkspaceID, "recurring-task", "每日检查", "active", "shared-project", workspaceQueryDate, "open", "daily", []string{workspaceQueryDate, "2026-11-20"})
	seed(routerTestWorkspaceID, "draft-task", "草稿事项", "draft", "shared-project", workspaceQueryDate, "open", "none", []string{workspaceQueryDate})
	seed(routerTestWorkspaceID, "cancelled-task", "取消事项", "cancelled", "shared-project", workspaceQueryDate, "cancelled", "none", []string{workspaceQueryDate})
	seed(routerTestWorkspaceID, "inbox-task", "待归类事项", "active", "system-inbox", "", "open", "none", []string{""})
	seed(workspaceQuerySecondaryID, "shared-task", "另一工作区事项", "active", "shared-project", workspaceQueryDate, "open", "none", []string{workspaceQueryDate})
	if _, err = tx.Exec(`INSERT INTO notes (workspace_id,id,title,content,content_text,created_at,updated_at) VALUES(?,'shared-note','租户库笔记','tenant','tenant',?,?)`, routerTestWorkspaceID, now, now); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
