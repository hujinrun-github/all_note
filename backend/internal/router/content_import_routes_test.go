package router

import (
	"testing"

	"github.com/hujinrun/flowspace/internal/contentimport"
)

func TestContentImportFailureAndRetryRoutesAreRegistered(t *testing.T) {
	config := testRouterConfig(nil, testRouterAuthConfig(false))
	config.ContentImports = &contentimport.Service{}
	routes := registeredRoutes(Setup(config))

	for _, route := range []string{
		"GET /api/content-imports/:id",
		"POST /api/content-imports/:id/retry",
		"DELETE /api/content-imports/:id",
	} {
		if !routes[route] {
			t.Fatalf("content import route %s is not registered", route)
		}
	}
}
