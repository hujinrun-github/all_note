package router

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	authpkg "github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/taskapp"
	"github.com/hujinrun/flowspace/internal/taskdomain"
)

func TestTaskDomainV2RoutesResolveAuthenticatedWorkspacePerRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	env := setupRouterAuthEnv(t, false)
	const token = "task-domain-v2-route-session"
	createRouterSession(t, env, token)

	resolver := &routerTaskRuntimeResolver{snapshot: routerTaskRuntimeSnapshot(routerTestWorkspaceID)}
	env.config.TaskDomainV2Runtime = resolver
	env.config.TaskDomainModelSelector = &routerTaskDomainModelSelector{models: map[string]taskapp.ModelVersion{routerTestWorkspaceID: taskapp.ModelV2}}
	router := Setup(env.config)

	unauthenticated := httptest.NewRecorder()
	router.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/api/projects", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want %d; body = %s", unauthenticated.Code, http.StatusUnauthorized, unauthenticated.Body.String())
	}

	for range 2 {
		request := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
		request.AddCookie(&http.Cookie{Name: env.auth.Cookie.Name, Value: token})
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("authenticated status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
		}
	}

	if got := resolver.workspaces(); !reflect.DeepEqual(got, []string{routerTestWorkspaceID, routerTestWorkspaceID}) {
		t.Fatalf("resolved workspaces = %#v, want one authenticated workspace resolution per request", got)
	}
}

func TestTaskDomainV2RoutesFailClosedWhenRuntimeIsNotCutOver(t *testing.T) {
	gin.SetMode(gin.TestMode)
	env := setupRouterAuthEnv(t, false)
	const token = "task-domain-v2-not-cutover-session"
	createRouterSession(t, env, token)
	env.config.TaskDomainV2Runtime = &routerTaskRuntimeResolver{err: errors.New("v2 tenant schema is damaged")}
	env.config.TaskDomainModelSelector = &routerTaskDomainModelSelector{models: map[string]taskapp.ModelVersion{routerTestWorkspaceID: taskapp.ModelV2}}

	request := httptest.NewRequest(http.MethodGet, "/api/tasks", nil)
	request.AddCookie(&http.Cookie{Name: env.auth.Cookie.Name, Value: token})
	response := httptest.NewRecorder()
	Setup(env.config).ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusServiceUnavailable, response.Body.String())
	}
	assertRouterErrorCode(t, response.Body.String(), "task_domain_unavailable")
}

func TestTaskDomainV2RouteModeDoesNotRegisterConflictingLegacyTaskPaths(t *testing.T) {
	gin.SetMode(gin.TestMode)
	env := setupRouterAuthEnv(t, false)
	env.config.TaskDomainV2Runtime = &routerTaskRuntimeResolver{snapshot: routerTaskRuntimeSnapshot(routerTestWorkspaceID)}
	env.config.TaskDomainModelSelector = &routerTaskDomainModelSelector{models: map[string]taskapp.ModelVersion{routerTestWorkspaceID: taskapp.ModelV2}}

	router := Setup(env.config)
	routes := registeredRoutes(router)
	for _, route := range []string{
		"GET /api/projects",
		"GET /api/tasks",
		"GET /api/tasks/:taskID",
		"GET /api/task-occurrences",
		"GET /api/task-occurrences/:occurrenceID",
		"GET /api/calendar/entries",
		"GET /api/projects/:projectID/roadmap",
		"POST /api/projects/:projectID/roadmap",
		"POST /api/roadmaps/:id/nodes",
		"PATCH /api/roadmaps/:id/nodes/:nodeID",
		"DELETE /api/roadmaps/:id/nodes/:nodeID",
		"GET /api/auth/me",
		"GET /api/settings/profile",
		"GET /api/settings/runtime",
	} {
		if !routes[route] {
			t.Fatalf("v2 route mode is missing %s", route)
		}
	}
	counts := map[string]int{}
	for _, route := range router.Routes() {
		key := route.Method + " " + route.Path
		counts[key]++
		if counts[key] > 1 {
			t.Fatalf("model-aware router registered %s more than once", key)
		}
	}
}

func TestTaskDomainModelAwareRoutesServeLegacyAndV2WorkspacesInOneRouter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	env := setupRouterAuthEnv(t, false)
	const (
		legacyToken     = "task-domain-legacy-workspace-session"
		v2Token         = "task-domain-v2-workspace-session"
		legacyWorkspace = routerTestWorkspaceID
		v2Workspace     = "router_workspace_v2"
	)
	createRouterSession(t, env, legacyToken)
	createTaskDomainWorkspaceSession(t, env, v2Workspace, "router_v2_user", "router_v2_session", v2Token)

	resolver := &routerTaskRuntimeResolver{snapshots: map[string]taskapp.RuntimeSnapshot{
		v2Workspace: routerTaskRuntimeSnapshot(v2Workspace),
	}}
	selector := &routerTaskDomainModelSelector{models: map[string]taskapp.ModelVersion{
		legacyWorkspace: taskapp.ModelLegacy,
		v2Workspace:     taskapp.ModelV2,
	}}
	env.config.TaskDomainV2Runtime = resolver
	env.config.TaskDomainModelSelector = selector
	router := Setup(env.config)

	legacyResponse := taskDomainAuthenticatedRequest(t, router, env.auth.Cookie.Name, legacyToken, http.MethodGet, "/api/tasks")
	if legacyResponse.Code != http.StatusOK {
		t.Fatalf("legacy status = %d, want %d; body = %s", legacyResponse.Code, http.StatusOK, legacyResponse.Body.String())
	}
	v2Response := taskDomainAuthenticatedRequest(t, router, env.auth.Cookie.Name, v2Token, http.MethodGet, "/api/tasks")
	if v2Response.Code != http.StatusOK {
		t.Fatalf("v2 status = %d, want %d; body = %s", v2Response.Code, http.StatusOK, v2Response.Body.String())
	}

	if got := resolver.workspaces(); !reflect.DeepEqual(got, []string{v2Workspace}) {
		t.Fatalf("v2 runtime resolutions = %#v, want only the explicitly v2 workspace", got)
	}
	if got := selector.workspaces(); !reflect.DeepEqual(got, []string{legacyWorkspace, v2Workspace}) {
		t.Fatalf("model selections = %#v, want both authenticated workspaces", got)
	}
}

func TestTaskDomainModelAwareRoutesNeverInferLegacyFromSelectorOrV2Failures(t *testing.T) {
	gin.SetMode(gin.TestMode)
	env := setupRouterAuthEnv(t, false)
	const token = "task-domain-routing-failure-session"
	createRouterSession(t, env, token)

	t.Run("selector failure", func(t *testing.T) {
		resolver := &routerTaskRuntimeResolver{snapshot: routerTaskRuntimeSnapshot(routerTestWorkspaceID)}
		cfg := env.config
		cfg.TaskDomainV2Runtime = resolver
		cfg.TaskDomainModelSelector = &routerTaskDomainModelSelector{err: errors.New("durable model state unavailable")}
		response := taskDomainAuthenticatedRequest(t, Setup(cfg), env.auth.Cookie.Name, token, http.MethodGet, "/api/tasks")
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusServiceUnavailable, response.Body.String())
		}
		assertRouterErrorCode(t, response.Body.String(), "task_domain_routing_unavailable")
		if got := resolver.workspaces(); len(got) != 0 {
			t.Fatalf("runtime resolved after selector failure: %#v", got)
		}
	})

	t.Run("damaged v2", func(t *testing.T) {
		resolver := &routerTaskRuntimeResolver{err: errors.New("v2 endpoint unavailable")}
		cfg := env.config
		cfg.TaskDomainV2Runtime = resolver
		cfg.TaskDomainModelSelector = &routerTaskDomainModelSelector{models: map[string]taskapp.ModelVersion{routerTestWorkspaceID: taskapp.ModelV2}}
		response := taskDomainAuthenticatedRequest(t, Setup(cfg), env.auth.Cookie.Name, token, http.MethodGet, "/api/tasks")
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusServiceUnavailable, response.Body.String())
		}
		assertRouterErrorCode(t, response.Body.String(), "task_domain_unavailable")
		if got := resolver.workspaces(); !reflect.DeepEqual(got, []string{routerTestWorkspaceID}) {
			t.Fatalf("runtime resolutions = %#v, want v2 attempt without legacy fallback", got)
		}
	})
}

func TestTaskDomainV2OnlyRoutesReturnStableModelMismatchForLegacyWorkspace(t *testing.T) {
	gin.SetMode(gin.TestMode)
	env := setupRouterAuthEnv(t, false)
	const token = "task-domain-v2-only-legacy-session"
	createRouterSession(t, env, token)
	resolver := &routerTaskRuntimeResolver{snapshot: routerTaskRuntimeSnapshot(routerTestWorkspaceID)}
	env.config.TaskDomainV2Runtime = resolver
	env.config.TaskDomainModelSelector = &routerTaskDomainModelSelector{models: map[string]taskapp.ModelVersion{routerTestWorkspaceID: taskapp.ModelLegacy}}

	response := taskDomainAuthenticatedRequest(t, Setup(env.config), env.auth.Cookie.Name, token, http.MethodGet, "/api/projects")
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusConflict, response.Body.String())
	}
	assertRouterErrorCode(t, response.Body.String(), "task_domain_v2_not_active")
	if got := resolver.workspaces(); len(got) != 0 {
		t.Fatalf("v2-only legacy request resolved v2 runtime: %#v", got)
	}
}

func TestCutOverWorkspaceLegacyWebTaskAndEventReadsUseV2RuntimeNeverFixedStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	env := setupRouterAuthEnv(t, false)
	const token = "task-domain-v2-legacy-web-read-session"
	createRouterSession(t, env, token)
	originalStore := env.config.Store
	env.config.ControlStore = originalStore
	env.config.Store = panicLegacyTaskDomainStore{Store: originalStore}
	resolver := &routerTaskRuntimeResolver{snapshot: routerTaskRuntimeSnapshot(routerTestWorkspaceID)}
	env.config.TaskDomainV2Runtime = resolver
	env.config.TaskDomainModelSelector = &routerTaskDomainModelSelector{models: map[string]taskapp.ModelVersion{routerTestWorkspaceID: taskapp.ModelV2}}
	router := Setup(env.config)

	for _, path := range []string{
		"/api/tasks?page=1&page_size=20&status=all",
		"/api/events?month=2026-07&timezone=Asia/Shanghai",
	} {
		response := taskDomainAuthenticatedRequest(t, router, env.auth.Cookie.Name, token, http.MethodGet, path)
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want %d; body = %s", path, response.Code, http.StatusOK, response.Body.String())
		}
	}
	if got := resolver.workspaces(); !reflect.DeepEqual(got, []string{routerTestWorkspaceID, routerTestWorkspaceID}) {
		t.Fatalf("runtime resolutions = %#v, want one request-scoped v2 resolution per compatibility read", got)
	}
}

func TestCutOverWorkspaceLegacyWebMutationsAre410WhileRevisionAwareV2RoutesRemainAvailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	env := setupRouterAuthEnv(t, false)
	const token = "task-domain-v2-legacy-web-mutation-session"
	createRouterSession(t, env, token)
	resolver := &routerTaskRuntimeResolver{snapshot: routerTaskRuntimeSnapshot(routerTestWorkspaceID)}
	env.config.TaskDomainV2Runtime = resolver
	env.config.TaskDomainModelSelector = &routerTaskDomainModelSelector{models: map[string]taskapp.ModelVersion{routerTestWorkspaceID: taskapp.ModelV2}}
	router := Setup(env.config)

	legacy := taskDomainAuthenticatedJSONRequest(t, router, env.auth.Cookie.Name, token, http.MethodPatch, "/api/tasks/task-1", `{"title":"old request"}`)
	if legacy.Code != http.StatusGone {
		t.Fatalf("legacy mutation status = %d, want %d; body = %s", legacy.Code, http.StatusGone, legacy.Body.String())
	}
	assertRouterErrorCode(t, legacy.Body.String(), "legacy_contract_revision_required")

	// Native v2 commands remain independently addressable and are not routed
	// through the compatibility handler.
	v2 := taskDomainAuthenticatedJSONRequest(t, router, env.auth.Cookie.Name, token, http.MethodPatch, "/api/tasks/task-1", `{"expected_task_revision":2,"expected_schedule_revision":3,"title":"v2 request"}`)
	if v2.Code != http.StatusOK {
		t.Fatalf("v2 mutation status = %d, want %d; body = %s", v2.Code, http.StatusOK, v2.Body.String())
	}
}

func TestLegacyOnlyTaskDomainRoutesRunOnlyForExplicitDurableLegacyModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	env := setupRouterAuthEnv(t, false)
	const token = "task-domain-legacy-only-session"
	createRouterSession(t, env, token)

	t.Run("legacy", func(t *testing.T) {
		cfg := env.config
		cfg.TaskDomainV2Runtime = &routerTaskRuntimeResolver{snapshot: routerTaskRuntimeSnapshot(routerTestWorkspaceID)}
		cfg.TaskDomainModelSelector = &routerTaskDomainModelSelector{models: map[string]taskapp.ModelVersion{routerTestWorkspaceID: taskapp.ModelLegacy}}
		response := taskDomainAuthenticatedRequest(t, Setup(cfg), env.auth.Cookie.Name, token, http.MethodGet, "/api/task-projects")
		if response.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
		}
	})

	t.Run("v2", func(t *testing.T) {
		resolver := &routerTaskRuntimeResolver{snapshot: routerTaskRuntimeSnapshot(routerTestWorkspaceID)}
		cfg := env.config
		cfg.TaskDomainV2Runtime = resolver
		cfg.TaskDomainModelSelector = &routerTaskDomainModelSelector{models: map[string]taskapp.ModelVersion{routerTestWorkspaceID: taskapp.ModelV2}}
		response := taskDomainAuthenticatedRequest(t, Setup(cfg), env.auth.Cookie.Name, token, http.MethodGet, "/api/task-projects")
		if response.Code != http.StatusGone {
			t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusGone, response.Body.String())
		}
		assertRouterErrorCode(t, response.Body.String(), "legacy_task_domain_endpoint_retired")
		if got := resolver.workspaces(); len(got) != 0 {
			t.Fatalf("legacy-only endpoint resolved v2 runtime: %#v", got)
		}
	})
}

func TestTaskDomainCapabilitiesReportAuthenticatedWorkspaceModelWithoutEndpointDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	env := setupRouterAuthEnv(t, false)
	const token = "task-domain-capabilities-session"
	createRouterSession(t, env, token)

	tests := []struct {
		name       string
		selector   taskapp.ModelSelector
		resolver   taskapp.RuntimeResolver
		wantStatus int
		wantBody   string
	}{
		{
			name: "legacy", selector: &routerTaskDomainModelSelector{models: map[string]taskapp.ModelVersion{routerTestWorkspaceID: taskapp.ModelLegacy}},
			resolver: &routerTaskRuntimeResolver{}, wantStatus: http.StatusOK,
			wantBody: `"model_version":"legacy","available":true`,
		},
		{
			name: "v2", selector: &routerTaskDomainModelSelector{models: map[string]taskapp.ModelVersion{routerTestWorkspaceID: taskapp.ModelV2}},
			resolver: &routerTaskRuntimeResolver{snapshot: routerTaskRuntimeSnapshot(routerTestWorkspaceID)}, wantStatus: http.StatusOK,
			wantBody: `"model_version":"v2","available":true`,
		},
		{
			name: "damaged", selector: &routerTaskDomainModelSelector{models: map[string]taskapp.ModelVersion{routerTestWorkspaceID: taskapp.ModelV2}},
			resolver: &routerTaskRuntimeResolver{err: errors.New("tenant schema unavailable")}, wantStatus: http.StatusServiceUnavailable,
			wantBody: `"model_version":"v2","available":false`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := env.config
			cfg.TaskDomainV2Runtime = test.resolver
			cfg.TaskDomainModelSelector = test.selector
			response := taskDomainAuthenticatedRequest(t, Setup(cfg), env.auth.Cookie.Name, token, http.MethodGet, "/api/task-domain/capabilities")
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, test.wantStatus, response.Body.String())
			}
			compact := response.Body.String()
			if !strings.Contains(compact, test.wantBody) {
				t.Fatalf("body = %s, want fields %s", compact, test.wantBody)
			}
			for _, leaked := range []string{"endpoint", "database_url", "profile_version"} {
				if strings.Contains(strings.ToLower(compact), leaked) {
					t.Fatalf("capability response leaked %q: %s", leaked, compact)
				}
			}
		})
	}
}

func TestDefaultRouterKeepsLegacyTaskRoutesUntilV2RuntimeIsExplicitlyWired(t *testing.T) {
	gin.SetMode(gin.TestMode)
	env := setupRouterAuthEnv(t, false)
	routes := registeredRoutes(Setup(env.config))

	for _, route := range []string{
		"GET /api/tasks",
		"GET /api/tasks/projects",
		"POST /api/tasks/:id/occurrences/:date/complete",
		"GET /api/task-occurrences",
	} {
		if !routes[route] {
			t.Fatalf("default router lost legacy route %s", route)
		}
	}
	for _, v2OnlyRoute := range []string{
		"GET /api/projects",
		"GET /api/tasks/:taskID",
		"GET /api/task-occurrences/:occurrenceID",
		"GET /api/calendar/entries",
	} {
		if routes[v2OnlyRoute] {
			t.Fatalf("default router must not switch production to v2 route %s", v2OnlyRoute)
		}
	}
}

type routerTaskRuntimeResolver struct {
	mu        sync.Mutex
	workspace []string
	snapshot  taskapp.RuntimeSnapshot
	snapshots map[string]taskapp.RuntimeSnapshot
	err       error
}

type panicLegacyTaskDomainStore struct{ storage.Store }

func (panicLegacyTaskDomainStore) Tasks() storage.TaskRepository {
	panic("cut-over compatibility path touched fixed legacy task repository")
}

func (panicLegacyTaskDomainStore) Recurrence() storage.RecurrenceRepository {
	panic("cut-over compatibility path touched fixed legacy recurrence repository")
}

func (panicLegacyTaskDomainStore) Events() storage.EventRepository {
	panic("cut-over compatibility path touched fixed legacy event repository")
}

func (resolver *routerTaskRuntimeResolver) Resolve(_ context.Context, workspaceID string) (taskapp.RuntimeSnapshot, error) {
	resolver.mu.Lock()
	resolver.workspace = append(resolver.workspace, workspaceID)
	resolver.mu.Unlock()
	if resolver.err != nil {
		return taskapp.RuntimeSnapshot{}, resolver.err
	}
	if snapshot, ok := resolver.snapshots[workspaceID]; ok {
		return snapshot, nil
	}
	return resolver.snapshot, nil
}

func (resolver *routerTaskRuntimeResolver) workspaces() []string {
	resolver.mu.Lock()
	defer resolver.mu.Unlock()
	return append([]string(nil), resolver.workspace...)
}

type routerTaskDomainModelSelector struct {
	mu        sync.Mutex
	workspace []string
	models    map[string]taskapp.ModelVersion
	err       error
}

func (selector *routerTaskDomainModelSelector) SelectTaskDomainModel(_ context.Context, workspaceID string) (taskapp.ModelVersion, error) {
	selector.mu.Lock()
	selector.workspace = append(selector.workspace, workspaceID)
	selector.mu.Unlock()
	if selector.err != nil {
		return "", selector.err
	}
	return selector.models[workspaceID], nil
}

func (selector *routerTaskDomainModelSelector) workspaces() []string {
	selector.mu.Lock()
	defer selector.mu.Unlock()
	return append([]string(nil), selector.workspace...)
}

func createTaskDomainWorkspaceSession(t *testing.T, env *routerAuthEnv, workspaceID, userID, sessionID, token string) {
	t.Helper()
	passwordHash, err := authpkg.HashPassword("abc12345")
	if err != nil {
		t.Fatalf("hash task-domain user password: %v", err)
	}
	user := &model.User{
		ID: userID, Email: userID + "@example.com", DisplayName: "Task Domain V2 User", PasswordHash: passwordHash,
		DefaultWorkspaceID: workspaceID, Role: "user", Status: "active",
	}
	workspace := &model.Workspace{ID: workspaceID, Name: "Task Domain V2 Workspace", OwnerUserID: userID}
	if err := env.store.Transact(t.Context(), func(tx storage.Store) error {
		if err := tx.Auth().CreateUser(t.Context(), user); err != nil {
			return err
		}
		if err := tx.Auth().CreateWorkspace(t.Context(), workspace); err != nil {
			return err
		}
		return tx.Auth().AddWorkspaceMember(t.Context(), workspaceID, userID, "owner")
	}); err != nil {
		t.Fatalf("create task-domain workspace: %v", err)
	}
	tokenHash, err := authpkg.HashSessionToken(env.auth.SessionSecret, token)
	if err != nil {
		t.Fatalf("hash task-domain session token: %v", err)
	}
	if err := env.store.Auth().CreateSession(t.Context(), &model.Session{
		ID: sessionID, UserID: userID, WorkspaceID: workspaceID, TokenHash: tokenHash,
		UserAgent: "router-task-domain-test", IPAddress: "127.0.0.1", ExpiresAt: time.Now().UTC().Add(time.Hour),
	}); err != nil {
		t.Fatalf("create task-domain workspace session: %v", err)
	}
}

func taskDomainAuthenticatedRequest(t *testing.T, router http.Handler, cookieName, token, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, nil)
	request.AddCookie(&http.Cookie{Name: cookieName, Value: token})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func taskDomainAuthenticatedJSONRequest(t *testing.T, router http.Handler, cookieName, token, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: cookieName, Value: token})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func routerTaskRuntimeSnapshot(workspaceID string) taskapp.RuntimeSnapshot {
	services := routerTaskRuntimeServices{workspaceID: workspaceID}
	return taskapp.RuntimeSnapshot{
		WorkspaceID: workspaceID,
		Epoch:       9,
		Factory:     services,
		Tasks:       services,
		Occurrences: services,
		Projects:    services,
		Schedules:   services,
		Reader:      services,
	}
}

type routerTaskRuntimeServices struct{ workspaceID string }

func (routerTaskRuntimeServices) Build(taskdomain.TaskCreationInput) (taskdomain.TaskAggregateSnapshot, taskdomain.TaskCreationDetails, error) {
	return taskdomain.TaskAggregateSnapshot{}, taskdomain.TaskCreationDetails{}, nil
}

func (routerTaskRuntimeServices) CreateTask(context.Context, taskdomain.CreateTaskRequest) (taskapp.TaskCommandOutcome, error) {
	return taskapp.TaskCommandOutcome{}, nil
}

func (services routerTaskRuntimeServices) PatchTask(_ context.Context, request taskdomain.PatchTaskRequest) (taskapp.TaskCommandOutcome, error) {
	return taskapp.TaskCommandOutcome{Task: taskdomain.TaskRecord{WorkspaceID: services.workspaceID, ID: request.TaskID, ProjectID: "project-1", Title: "v2 request", LifecycleStatus: taskdomain.TaskLifecycleActive, Revision: request.ExpectedTaskRevision + 1}, TaskRevision: request.ExpectedTaskRevision + 1, ScheduleRevision: request.ExpectedScheduleRevision, LifecycleStatus: taskdomain.TaskLifecycleActive}, nil
}

func (routerTaskRuntimeServices) ExecuteLifecycleCommand(context.Context, taskdomain.LifecycleCommandRequest) (taskapp.TaskCommandOutcome, error) {
	return taskapp.TaskCommandOutcome{}, nil
}

func (routerTaskRuntimeServices) Execute(context.Context, taskdomain.OccurrenceCommandRequest) (taskapp.OccurrenceCommandOutcome, error) {
	return taskapp.OccurrenceCommandOutcome{}, nil
}

func (routerTaskRuntimeServices) CreateProject(context.Context, taskdomain.CreateProjectRequest) (taskapp.ProjectCommandOutcome, error) {
	return taskapp.ProjectCommandOutcome{}, nil
}

func (routerTaskRuntimeServices) UpdateProject(context.Context, taskdomain.UpdateProjectRequest) (taskapp.ProjectCommandOutcome, error) {
	return taskapp.ProjectCommandOutcome{}, nil
}

func (routerTaskRuntimeServices) CompleteProject(context.Context, taskdomain.ExistingProjectRequest) (taskapp.ProjectCommandOutcome, error) {
	return taskapp.ProjectCommandOutcome{}, nil
}

func (routerTaskRuntimeServices) ArchiveProject(context.Context, taskdomain.ExistingProjectRequest) (taskapp.ProjectCommandOutcome, error) {
	return taskapp.ProjectCommandOutcome{}, nil
}

func (routerTaskRuntimeServices) DeleteProject(context.Context, taskdomain.ExistingProjectRequest) (taskapp.ProjectCommandOutcome, error) {
	return taskapp.ProjectCommandOutcome{}, nil
}

func (routerTaskRuntimeServices) RescheduleOccurrence(context.Context, taskdomain.RescheduleOccurrenceRequest, taskapp.CommandMetadata) (taskapp.ScheduleCommandOutcome, error) {
	return taskapp.ScheduleCommandOutcome{}, nil
}

func (routerTaskRuntimeServices) RescheduleThisAndFollowing(context.Context, taskdomain.RescheduleThisAndFutureRequest, taskapp.CommandMetadata) (taskapp.ScheduleCommandOutcome, error) {
	return taskapp.ScheduleCommandOutcome{}, nil
}

func (routerTaskRuntimeServices) GetProject(context.Context, string) (taskdomain.ProjectSnapshot, error) {
	return taskdomain.ProjectSnapshot{}, nil
}

func (routerTaskRuntimeServices) ListProjects(context.Context, taskdomain.ProjectListFilter) ([]taskdomain.ProjectSnapshot, error) {
	return []taskdomain.ProjectSnapshot{}, nil
}

func (services routerTaskRuntimeServices) GetTaskAggregate(_ context.Context, taskID string) (taskdomain.TaskAggregateQueryResult, error) {
	return taskdomain.TaskAggregateQueryResult{
		Aggregate: taskdomain.TaskAggregate{WorkspaceID: services.workspaceID, TaskID: taskID, LifecycleStatus: taskdomain.TaskLifecycleActive, Revision: 2},
		Task:      taskdomain.TaskRecord{WorkspaceID: services.workspaceID, ID: taskID, ProjectID: "project-1", Title: "Task", LifecycleStatus: taskdomain.TaskLifecycleActive, Revision: 2},
		Schedule:  taskdomain.ScheduleHeader{WorkspaceID: services.workspaceID, TaskID: taskID, Revision: 3, CurrentScheduleRevision: 1},
	}, nil
}

func (routerTaskRuntimeServices) ListTaskDefinitions(context.Context, taskdomain.TaskDefinitionListFilter) ([]taskdomain.TaskDefinitionSnapshot, error) {
	return []taskdomain.TaskDefinitionSnapshot{}, nil
}

func (routerTaskRuntimeServices) GetOccurrence(context.Context, string) (taskdomain.QueryOccurrenceSnapshot, error) {
	return taskdomain.QueryOccurrenceSnapshot{}, nil
}

func (routerTaskRuntimeServices) ListOccurrences(context.Context, taskdomain.OccurrenceListFilter) ([]taskdomain.QueryOccurrenceSnapshot, error) {
	return []taskdomain.QueryOccurrenceSnapshot{}, nil
}

var _ taskapp.RuntimeResolver = (*routerTaskRuntimeResolver)(nil)
var _ taskapp.TaskFactory = routerTaskRuntimeServices{}
var _ taskapp.TaskService = routerTaskRuntimeServices{}
var _ taskapp.OccurrenceService = routerTaskRuntimeServices{}
var _ taskapp.ProjectService = routerTaskRuntimeServices{}
var _ taskapp.ScheduleService = routerTaskRuntimeServices{}
var _ taskapp.TaskDomainReader = routerTaskRuntimeServices{}
