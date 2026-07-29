package router

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/hujinrun/flowspace/internal/handler"
	"github.com/hujinrun/flowspace/internal/taskapp"
)

func TestMobileV2RoutesRegisterAsOneIndependentProtocolFamily(t *testing.T) {
	disabled := setupRouterAuthEnv(t, false)
	routes := registeredRoutes(Setup(disabled.config))
	for _, route := range mobileV2RouteNames() {
		if routes[route] {
			t.Fatalf("%s must be absent without a mobile-v2 service", route)
		}
	}

	enabled := setupRouterAuthEnv(t, false)
	enabled.config.MobileSyncV2 = &routerMobileV2Service{}
	routes = registeredRoutes(Setup(enabled.config))
	for _, route := range mobileV2RouteNames() {
		if !routes[route] {
			t.Fatalf("%s must be registered with a mobile-v2 service", route)
		}
	}
}

func TestMobileV2RoutesForwardAuthenticatedWorkspaceAndProtocolInputs(t *testing.T) {
	env := setupRouterAuthEnv(t, false)
	service := &routerMobileV2Service{}
	env.config.MobileSyncV2 = service
	token := "mobile-v2-route-session"
	createRouterSession(t, env, token)
	router := Setup(env.config)

	request := httptest.NewRequest(http.MethodGet,
		"/api/mobile/v2/snapshot?scope=iphone-occurrence-window&projection_time_zone=Asia%2FShanghai&page_token=page-2", nil)
	request.AddCookie(&http.Cookie{Name: env.auth.Cookie.Name, Value: token})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("snapshot status=%d body=%s", response.Code, response.Body.String())
	}
	if service.snapshot.Identity.WorkspaceID != routerTestWorkspaceID ||
		service.snapshot.Scope != "iphone-occurrence-window" ||
		service.snapshot.ProjectionTimeZone != "Asia/Shanghai" ||
		service.snapshot.PageToken != "page-2" {
		t.Fatalf("snapshot request = %+v", service.snapshot)
	}

	commandBody := []byte(`{"command_id":"11111111-1111-4111-8111-111111111111","payload":{}}`)
	request = httptest.NewRequest(http.MethodPost, "/api/mobile/v2/commands", bytes.NewReader(commandBody))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: env.auth.Cookie.Name, Value: token})
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("command status=%d body=%s", response.Code, response.Body.String())
	}
	if service.command.Identity.WorkspaceID != routerTestWorkspaceID ||
		!bytes.Equal(service.command.RawEnvelope, commandBody) {
		t.Fatalf("command request = workspace:%q body:%s", service.command.Identity.WorkspaceID, service.command.RawEnvelope)
	}

	request = httptest.NewRequest(http.MethodGet,
		"/api/mobile/v2/commands/device-1/command-1/receipt", nil)
	request.AddCookie(&http.Cookie{Name: env.auth.Cookie.Name, Value: token})
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("receipt status=%d body=%s", response.Code, response.Body.String())
	}
	if service.receipt.OriginDeviceClientID != "device-1" || service.receipt.CommandID != "command-1" ||
		service.receipt.Identity.WorkspaceID != routerTestWorkspaceID {
		t.Fatalf("receipt request = %+v", service.receipt)
	}
}

func TestMobileV2RoutesRequireAuthenticationAndPreserveProtocolErrors(t *testing.T) {
	env := setupRouterAuthEnv(t, false)
	service := &routerMobileV2Service{err: &handler.MobileV2ProtocolError{
		Status: http.StatusConflict, Code: "receipt_history_ambiguous", Message: "receipt history is incomplete",
	}}
	env.config.MobileSyncV2 = service
	router := Setup(env.config)

	request := httptest.NewRequest(http.MethodGet, "/api/mobile/v2/capabilities", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status=%d body=%s", response.Code, response.Body.String())
	}

	token := "mobile-v2-error-session"
	createRouterSession(t, env, token)
	request = httptest.NewRequest(http.MethodGet, "/api/mobile/v2/changes?scope=iphone-task-core", nil)
	request.AddCookie(&http.Cookie{Name: env.auth.Cookie.Name, Value: token})
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("protocol error status=%d body=%s", response.Code, response.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["schema_version"] != "mobile-v2" || body["code"] != "receipt_history_ambiguous" {
		t.Fatalf("protocol error body=%v", body)
	}
}

func TestMobileV1RoutesRemainForLegacyWorkspaceAndRetireAfterV2Cutover(t *testing.T) {
	test := func(t *testing.T, selector *routerTaskDomainModelSelector, wantStatus int) {
		t.Helper()
		env := setupRouterAuthEnv(t, false)
		env.config.MobileSyncV1Enabled = true
		env.config.TaskDomainModelSelector = selector
		token := "mobile-v1-model-session-" + http.StatusText(wantStatus)
		createRouterSession(t, env, token)

		request := httptest.NewRequest(http.MethodGet, "/api/mobile/capabilities", nil)
		request.AddCookie(&http.Cookie{Name: env.auth.Cookie.Name, Value: token})
		response := httptest.NewRecorder()
		Setup(env.config).ServeHTTP(response, request)
		if response.Code != wantStatus {
			t.Fatalf("status=%d body=%s, want=%d", response.Code, response.Body.String(), wantStatus)
		}
	}

	t.Run("legacy", func(t *testing.T) {
		test(t, &routerTaskDomainModelSelector{models: map[string]taskapp.ModelVersion{
			routerTestWorkspaceID: taskapp.ModelLegacy,
		}}, http.StatusOK)
	})
	t.Run("v2", func(t *testing.T) {
		test(t, &routerTaskDomainModelSelector{models: map[string]taskapp.ModelVersion{
			routerTestWorkspaceID: taskapp.ModelV2,
		}}, http.StatusUpgradeRequired)
	})
	t.Run("routing unavailable", func(t *testing.T) {
		test(t, &routerTaskDomainModelSelector{err: errors.New("tenant state unavailable")}, http.StatusServiceUnavailable)
	})
}

func TestMobileV1NonTaskNativeRoutesRemainRegisteredAfterTaskCutover(t *testing.T) {
	env := setupRouterAuthEnv(t, false)
	env.config.MobileSyncV1Enabled = true
	env.config.TaskDomainModelSelector = &routerTaskDomainModelSelector{models: map[string]taskapp.ModelVersion{
		routerTestWorkspaceID: taskapp.ModelV2,
	}}
	routes := registeredRoutes(Setup(env.config))
	for _, route := range []string{
		"PUT /api/mobile/voice-notes/:clientID/audio",
		"POST /api/mobile/voice-notes/:clientID/transcriptions",
		"GET /api/mobile/transcription-jobs/:jobID",
		"POST /api/mobile/transcription-jobs/:jobID/retry",
	} {
		if !routes[route] {
			t.Fatalf("%s must remain registered after task-domain cutover", route)
		}
	}
}

func TestWatchTaskRoutesRetireWithMobileV1AfterV2Cutover(t *testing.T) {
	test := func(t *testing.T, selector *routerTaskDomainModelSelector, wantStatus int) {
		t.Helper()
		env := setupRouterAuthEnv(t, false)
		env.config.TaskDomainModelSelector = selector
		token := "watch-model-session-" + http.StatusText(wantStatus)
		createRouterSession(t, env, token)
		router := Setup(env.config)

		for _, request := range []*http.Request{
			httptest.NewRequest(http.MethodGet, "/api/watch/snapshot", nil),
			httptest.NewRequest(http.MethodPatch, "/api/watch/tasks/task-1", bytes.NewReader([]byte(`{}`))),
		} {
			request.AddCookie(&http.Cookie{Name: env.auth.Cookie.Name, Value: token})
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != wantStatus {
				t.Fatalf("%s %s status=%d body=%s, want=%d",
					request.Method, request.URL.Path, response.Code, response.Body.String(), wantStatus)
			}
		}
	}

	t.Run("v2", func(t *testing.T) {
		test(t, &routerTaskDomainModelSelector{models: map[string]taskapp.ModelVersion{
			routerTestWorkspaceID: taskapp.ModelV2,
		}}, http.StatusUpgradeRequired)
	})
	t.Run("routing unavailable", func(t *testing.T) {
		test(t, &routerTaskDomainModelSelector{err: errors.New("tenant state unavailable")}, http.StatusServiceUnavailable)
	})
}

func mobileV2RouteNames() []string {
	return []string{
		"GET /api/mobile/v2/capabilities",
		"GET /api/mobile/v2/snapshot",
		"GET /api/mobile/v2/changes",
		"POST /api/mobile/v2/commands",
		"GET /api/mobile/v2/commands/:originDeviceClientID/:commandID/receipt",
		"PUT /api/mobile/v2/voice-notes/:clientID/audio",
	}
}

type routerMobileV2Service struct {
	mu       sync.Mutex
	snapshot handler.MobileV2SnapshotRequest
	changes  handler.MobileV2ChangesRequest
	command  handler.MobileV2CommandRequest
	receipt  handler.MobileV2ReceiptRequest
	err      error
}

func (service *routerMobileV2Service) Capabilities(context.Context, handler.MobileV2Identity) (any, error) {
	return map[string]any{"schema_version": "mobile-v2"}, service.err
}

func (service *routerMobileV2Service) Snapshot(_ context.Context, request handler.MobileV2SnapshotRequest) (any, error) {
	service.mu.Lock()
	service.snapshot = request
	service.mu.Unlock()
	return map[string]any{"snapshot_id": "snapshot-1"}, service.err
}

func (service *routerMobileV2Service) Changes(_ context.Context, request handler.MobileV2ChangesRequest) (any, error) {
	service.mu.Lock()
	service.changes = request
	service.mu.Unlock()
	return map[string]any{"changes": []any{}}, service.err
}

func (service *routerMobileV2Service) ApplyCommand(_ context.Context, request handler.MobileV2CommandRequest) (any, error) {
	service.mu.Lock()
	service.command = request
	service.mu.Unlock()
	return map[string]any{"schema_version": "mobile-v2"}, service.err
}

func (service *routerMobileV2Service) Receipt(_ context.Context, request handler.MobileV2ReceiptRequest) (any, error) {
	service.mu.Lock()
	service.receipt = request
	service.mu.Unlock()
	return map[string]any{"schema_version": "mobile-v2"}, service.err
}
