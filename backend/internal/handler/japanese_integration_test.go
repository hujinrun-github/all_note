package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

type japaneseMembershipStore struct {
	storage.Store
	membershipRole string
}

func (s japaneseMembershipStore) Auth() storage.AuthRepository {
	return japaneseMembershipAuth{membershipRole: s.membershipRole}
}

type japaneseMembershipAuth struct {
	storage.AuthRepository
	membershipRole string
}

func (s japaneseMembershipAuth) GetWorkspaceMembership(_ context.Context, workspace, user string) (*model.WorkspaceMember, error) {
	return &model.WorkspaceMember{WorkspaceID: workspace, UserID: user, Role: s.membershipRole}, nil
}

func TestJapaneseIntegrationManagerRequiresWorkspaceOwnerOrAdmin(t *testing.T) {
	for _, tc := range []struct {
		role string
		want int
	}{{"owner", 200}, {"admin", 200}, {"member", 403}, {"", 403}} {
		t.Run(tc.role, func(t *testing.T) {
			engine := gin.New()
			engine.Use(func(c *gin.Context) {
				ctx := auth.ContextWithIdentity(c.Request.Context(), auth.RequestIdentity{UserID: "u1", WorkspaceID: "w1"})
				c.Request = c.Request.WithContext(auth.ContextWithWorkspaceScope(ctx, "w1"))
				c.Next()
			})
			engine.GET("/", JapaneseIntegrationManager(japaneseMembershipStore{membershipRole: tc.role}), func(c *gin.Context) { c.Status(200) })
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
			if response.Code != tc.want {
				t.Fatalf("role=%q status=%d want=%d", tc.role, response.Code, tc.want)
			}
		})
	}
}

func TestJapaneseIntegrationManagementRequiresWorkspaceIdentity(t *testing.T) {
	for _, tc := range []struct{ name, identity, scope string }{
		{"anonymous", "", ""}, {"scope-only", "", "w1"}, {"mismatch", "w1", "w2"}, {"identity-only", "w1", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			engine := gin.New()
			engine.Use(func(c *gin.Context) {
				ctx := c.Request.Context()
				if tc.identity != "" {
					ctx = auth.ContextWithIdentity(ctx, auth.RequestIdentity{UserID: "u1", WorkspaceID: tc.identity})
				}
				if tc.scope != "" {
					ctx = auth.ContextWithWorkspaceScope(ctx, tc.scope)
				}
				c.Request = c.Request.WithContext(ctx)
				c.Next()
			})
			RegisterJapaneseIntegrationRoutes(engine.Group("/api"), engine.Group("/api"), nil)
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/integrations/japanese", nil))
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestJapaneseIntegrationJSONRejectsExtraInput(t *testing.T) {
	for _, body := range []string{`{"code":"x","workspace_id":"other"}`, `{"code":"x"} {"code":"y"}`, strings.Repeat("x", 65537)} {
		engine := gin.New()
		engine.POST("/", func(c *gin.Context) {
			var input struct {
				Code string `json:"code"`
			}
			if decodeJapaneseJSON(c, &input) {
				c.Status(200)
			}
		})
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))
		if response.Code != 400 {
			t.Fatalf("expected rejected input, status=%d", response.Code)
		}
	}
}
