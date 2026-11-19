package router

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/handler"
	"github.com/hujinrun/flowspace/internal/workspacecompat"
	"github.com/hujinrun/flowspace/internal/workspacequery"
)

func workspaceQueryApplication(cfg Config) handler.WorkspaceQueryApplication {
	if cfg.WorkspaceQueries != nil {
		return cfg.WorkspaceQueries
	}
	resolver, ok := cfg.TaskDomainV2Runtime.(workspacequery.RuntimeResolver)
	if !ok {
		return nil
	}
	return workspacequery.NewService(resolver, workspacecompat.NewNotesSource(cfg.Store), time.Now)
}

func registerWorkspaceQueryRoutes(routes *gin.RouterGroup, application handler.WorkspaceQueryApplication) {
	// These routes always exist behind authentication. An unavailable v2 runtime
	// returns 503 rather than silently selecting legacy data or disappearing.
	routes.GET("/workspace/overview", handler.GetWorkspaceOverview(application))
	routes.GET("/task-workbench/facets", handler.GetWorkspaceFacets(application))
	routes.GET("/projects/:projectID/overview", handler.GetWorkspaceProjectOverview(application))
}

func projectListWithSummary(application handler.WorkspaceQueryApplication, regular gin.HandlerFunc) gin.HandlerFunc {
	summaries := handler.GetWorkspaceProjectSummaries(application)
	return func(c *gin.Context) {
		if _, requested := c.Request.URL.Query()["include"]; requested {
			summaries(c)
			return
		}
		regular(c)
	}
}
