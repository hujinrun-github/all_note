package router

import (
	"testing"

	"github.com/gin-gonic/gin"
)

func TestNotionRoutesAreRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	routes := []string{
		"POST /api/sync/notion/test",
		"POST /api/sync/notion/bidirectional",
		"POST /api/sync/notion/notes/:id",
		"GET /api/sync/notion/deletions",
		"POST /api/sync/notion/deletions/:note_id/confirm",
		"POST /api/sync/notion/deletions/:note_id/restore",
	}

	router := Setup()
	registered := map[string]bool{}
	for _, route := range router.Routes() {
		registered[route.Method+" "+route.Path] = true
	}
	for _, route := range routes {
		if !registered[route] {
			t.Fatalf("route %s is not registered", route)
		}
	}
}

func TestSyncTargetRoutesAreRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	routes := []string{
		"GET /api/sync/targets",
		"POST /api/sync/targets",
		"PATCH /api/sync/targets/:id",
		"DELETE /api/sync/targets/:id",
	}

	router := Setup()
	registered := map[string]bool{}
	for _, route := range router.Routes() {
		registered[route.Method+" "+route.Path] = true
	}
	for _, route := range routes {
		if !registered[route] {
			t.Fatalf("route %s is not registered", route)
		}
	}
}
