package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/auth"
)

type fakeWorkspaceSettingsService struct {
	lastUserID, lastWorkspaceID string
	testRequest                 TestServiceProfileRequest
	bindingErr                  error
}

func (f *fakeWorkspaceSettingsService) GetRuntimeSettings(_ context.Context, userID, workspaceID string) (RuntimeSettingsDTO, error) {
	f.lastUserID, f.lastWorkspaceID = userID, workspaceID
	return RuntimeSettingsDTO{WorkspaceID: workspaceID, Mode: "active", Epoch: 2, BindingRevision: 3, Bindings: []ServiceBindingDTO{{Kind: "data_store", Mode: "default", Revision: 1}}}, nil
}
func (f *fakeWorkspaceSettingsService) TestProfile(_ context.Context, userID, workspaceID string, request TestServiceProfileRequest) (TestServiceProfileResult, error) {
	f.lastUserID, f.lastWorkspaceID, f.testRequest = userID, workspaceID, request
	return TestServiceProfileResult{OK: true, Code: "OK", Message: "connection verified"}, nil
}
func (f *fakeWorkspaceSettingsService) SaveProfile(_ context.Context, userID, workspaceID string, request SaveServiceProfileRequest) (SavedServiceProfileDTO, error) {
	f.lastUserID, f.lastWorkspaceID = userID, workspaceID
	return SavedServiceProfileDTO{ID: request.ID, FamilyID: request.FamilyID, Kind: request.Kind, Version: 1, State: "draft", HasCredentials: request.Secret != ""}, nil
}
func (f *fakeWorkspaceSettingsService) SetBinding(_ context.Context, userID, workspaceID, kind string, request SetServiceBindingRequest) (ServiceBindingDTO, error) {
	f.lastUserID, f.lastWorkspaceID = userID, workspaceID
	if f.bindingErr != nil {
		return ServiceBindingDTO{}, f.bindingErr
	}
	return ServiceBindingDTO{Kind: kind, Mode: request.Mode, EndpointID: request.EndpointID, Revision: request.ExpectedRevision + 1}, nil
}

func (f *fakeWorkspaceSettingsService) VerifyProfile(_ context.Context, _, _, kind, versionID string) (VerifiedServiceProfileDTO, error) {
	return VerifiedServiceProfileDTO{Kind: kind, ProfileVersionID: versionID, EndpointID: "custom-" + versionID}, nil
}

func TestWorkspaceSettingsHandlersUseAuthenticatedScopeAndRedactSecrets(t *testing.T) {
	service := &fakeWorkspaceSettingsService{}
	router := settingsServiceTestRouter(service)
	request := httptest.NewRequest(http.MethodPost, "/api/settings/profiles", strings.NewReader(`{"id":"v1","family_id":"f1","kind":"object_s3","name":"Objects","provider":"minio","config":{"endpoint":"https://objects.example"},"secret":"top-secret-value"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if service.lastUserID != "u1" || service.lastWorkspaceID != "w1" {
		t.Fatalf("scope user=%s workspace=%s", service.lastUserID, service.lastWorkspaceID)
	}
	if strings.Contains(response.Body.String(), "top-secret-value") || strings.Contains(response.Body.String(), "objects.example") {
		t.Fatalf("settings response leaked secret/config: %s", response.Body.String())
	}
}

func TestProfileTestIsSeparateFromSave(t *testing.T) {
	service := &fakeWorkspaceSettingsService{}
	router := settingsServiceTestRouter(service)
	request := httptest.NewRequest(http.MethodPost, "/api/settings/profiles/test", strings.NewReader(`{"kind":"data_store","provider":"postgres","config":{"url":"postgres://db.example/app"},"secret":"password"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || service.testRequest.Secret != "password" {
		t.Fatalf("status=%d request=%+v", response.Code, service.testRequest)
	}
	if strings.Contains(response.Body.String(), "password") || strings.Contains(response.Body.String(), "postgres://") {
		t.Fatalf("test response leaked request: %s", response.Body.String())
	}
}

func TestBindingRevisionConflictReturns409(t *testing.T) {
	service := &fakeWorkspaceSettingsService{bindingErr: ErrSettingsRevisionConflict}
	router := settingsServiceTestRouter(service)
	request := httptest.NewRequest(http.MethodPut, "/api/settings/bindings/llm_chat", strings.NewReader(`{"mode":"disabled","expected_revision":2}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "SETTINGS_REVISION_CONFLICT") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestSettingsServiceUnavailableIsExplicit(t *testing.T) {
	router := settingsServiceTestRouter(nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/settings/runtime", nil))
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "SETTINGS_UNAVAILABLE") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func settingsServiceTestRouter(service WorkspaceSettingsService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		identity := auth.RequestIdentity{UserID: "u1", WorkspaceID: "w1", SessionID: "s1"}
		c.Request = c.Request.WithContext(auth.ContextWithIdentity(c.Request.Context(), identity))
		c.Next()
	})
	router.GET("/api/settings/runtime", GetRuntimeSettings(service))
	router.POST("/api/settings/profiles/test", TestServiceProfile(service))
	router.POST("/api/settings/profiles", SaveServiceProfile(service))
	router.PUT("/api/settings/bindings/:kind", SetServiceBinding(service))
	return router
}
