package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestCORSUsesConfiguredAllowedOrigin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	authCfg := testRouterAuthConfig(false)
	authCfg.AllowedOrigins = []string{"http://all-note.jirunlab.site"}
	router := Setup(testRouterConfig(nil, authCfg))
	request := httptest.NewRequest(http.MethodOptions, "/api/notes", nil)
	request.Header.Set("Origin", "http://all-note.jirunlab.site")
	request.Header.Set("Access-Control-Request-Method", http.MethodPost)
	recorder := httptest.NewRecorder()

	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "http://all-note.jirunlab.site" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want configured origin", got)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("Access-Control-Allow-Credentials = %q, want true", got)
	}
}

func TestCORSDoesNotReflectUnconfiguredOrigin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	authCfg := testRouterAuthConfig(false)
	authCfg.AllowedOrigins = []string{"http://all-note.jirunlab.site"}
	router := Setup(testRouterConfig(nil, authCfg))
	request := httptest.NewRequest(http.MethodOptions, "/api/notes", nil)
	request.Header.Set("Origin", "http://evil.example.com")
	request.Header.Set("Access-Control-Request-Method", http.MethodPost)
	recorder := httptest.NewRecorder()

	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want empty for unconfigured origin", got)
	}
}
