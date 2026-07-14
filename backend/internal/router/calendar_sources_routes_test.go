package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestCalendarProjectSourcesRoutesAreRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	env := setupRouterAuthEnv(t, false)

	registered := registeredRoutes(Setup(env.config))

	for _, route := range []string{
		"GET /api/calendar/project-sources",
		"PUT /api/calendar/project-sources",
	} {
		if !registered[route] {
			t.Fatalf("route %s is not registered", route)
		}
	}
}

func TestCalendarProjectSourcesRoutesRequireAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	env := setupRouterAuthEnv(t, false)

	for _, tc := range []struct {
		method string
		body   string
	}{
		{method: http.MethodGet},
		{method: http.MethodPut, body: `{"sources":[]}`},
	} {
		t.Run(tc.method, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, "/api/calendar/project-sources", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			Setup(env.config).ServeHTTP(w, req)

			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusUnauthorized, w.Body.String())
			}
			assertRouterErrorCode(t, w.Body.String(), "UNAUTHENTICATED")
		})
	}
}
