package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestJapaneseFuriganaRouteIsRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	env := setupRouterAuthEnv(t, false)

	registered := registeredRoutes(Setup(env.config))
	if !registered["POST /api/japanese/furigana"] {
		t.Fatal("POST /api/japanese/furigana is not registered")
	}
}

func TestJapaneseFuriganaRouteRequiresAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	env := setupRouterAuthEnv(t, false)
	req := httptest.NewRequest(http.MethodPost, "/api/japanese/furigana", strings.NewReader(`{"text":"近く"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	Setup(env.config).ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusUnauthorized, w.Body.String())
	}
	assertRouterErrorCode(t, w.Body.String(), "UNAUTHENTICATED")
}
