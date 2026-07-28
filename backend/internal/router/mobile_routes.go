package router

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/taskapp"
)

// requireMobileV1Workspace keeps the frozen v1 URLs available while making
// task-domain cutover irreversible per workspace. A selector failure never
// falls back to legacy storage.
func requireMobileV1Workspace(selector taskapp.ModelSelector) gin.HandlerFunc {
	return func(c *gin.Context) {
		if selector == nil {
			c.Next()
			return
		}
		workspaceID, err := auth.WorkspaceIDFromContext(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"schema_version": "mobile-v1",
				"type":           "error",
				"code":           "unauthorized",
				"message":        "authentication is required",
				"retryable":      false,
			})
			c.Abort()
			return
		}
		model, err := selector.SelectTaskDomainModel(c.Request.Context(), workspaceID)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"schema_version": "mobile-v1",
				"type":           "error",
				"code":           "task_domain_routing_unavailable",
				"message":        "task-domain routing state is unavailable",
				"retryable":      true,
			})
			c.Abort()
			return
		}
		switch model {
		case taskapp.ModelLegacy:
			c.Next()
		case taskapp.ModelV2:
			c.JSON(http.StatusUpgradeRequired, gin.H{
				"schema_version": "mobile-v1",
				"type":           "error",
				"code":           "mobile_task_domain_upgrade_required",
				"message":        "this workspace requires the mobile-v2 task domain",
				"retryable":      false,
			})
			c.Abort()
		default:
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"schema_version": "mobile-v1",
				"type":           "error",
				"code":           "task_domain_routing_unavailable",
				"message":        "task-domain routing state is unavailable",
				"retryable":      true,
			})
			c.Abort()
		}
	}
}
