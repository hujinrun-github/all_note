package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/webplanning"
)

type planningRejectedRepository struct {
	writes    int
	lastScope webplanning.Scope
}

func (r *planningRejectedRepository) Read(context.Context, webplanning.Scope, func(webplanning.Reader) error) error {
	return webplanning.ErrUnavailable
}
func (r *planningRejectedRepository) Write(_ context.Context, scope webplanning.Scope, _ func(webplanning.Tx) error) error {
	r.writes++
	r.lastScope = scope
	return webplanning.ErrConflict
}
func (r *planningRejectedRepository) Commit(context.Context, webplanning.Scope, webplanning.CommitRequest) (webplanning.Receipt, error) {
	return webplanning.Receipt{}, webplanning.ErrUnavailable
}
func planningHTTP(service *webplanning.Service, identity, scope string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		ctx := c.Request.Context()
		if identity != "" {
			ctx = auth.ContextWithIdentity(ctx, auth.RequestIdentity{UserID: "owner", WorkspaceID: identity, SessionID: "session"})
		}
		if scope != "" {
			ctx = auth.ContextWithWorkspaceScope(ctx, scope)
		}
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	RegisterWebPlanningRoutes(engine.Group("/api"), service)
	return engine
}
func TestWebPlanningRequiresMatchingAuthenticatedWorkspace(t *testing.T) {
	for _, test := range []struct{ name, identity, scope string }{{"anonymous", "", ""}, {"scope-is-not-auth", "", "workspace"}, {"missing-scope", "workspace", ""}, {"mismatched-scope", "workspace", "other"}} {
		t.Run(test.name, func(t *testing.T) {
			engine := planningHTTP(nil, test.identity, test.scope)
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/planning/capabilities", nil))
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("got %d: %s", response.Code, response.Body.String())
			}
		})
	}
}
func TestWebPlanningRejectsUntrustedFieldsBeforeApplication(t *testing.T) {
	repository := &planningRejectedRepository{}
	service, _ := webplanning.New(webplanning.Config{Repository: repository})
	engine := planningHTTP(service, "workspace", "workspace")
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/plan-drafts", strings.NewReader(`{"request_id":"create","input_text":"安排复习","timezone":"Asia/Shanghai","workspace_id":"other"}`))
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || repository.writes != 0 {
		t.Fatalf("untrusted input reached application: %d %d %s", response.Code, repository.writes, response.Body.String())
	}
}
func TestWebPlanningUsesVerifiedScopeAndMapsConflict(t *testing.T) {
	repository := &planningRejectedRepository{}
	service, _ := webplanning.New(webplanning.Config{Repository: repository})
	engine := planningHTTP(service, "workspace", "workspace")
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/plan-drafts", strings.NewReader(`{"request_id":"create","input_text":"安排复习","timezone":"Asia/Shanghai"}`))
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusConflict || repository.writes != 1 || repository.lastScope.WorkspaceID != "workspace" || repository.lastScope.ActorID != "owner" {
		t.Fatalf("scope/conflict: %d %+v %s", response.Code, repository.lastScope, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "PLANNING_CONFLICT") {
		t.Fatal(response.Body.String())
	}
}
