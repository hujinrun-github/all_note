package handler

import (
	"database/sql"
	"errors"
	"io"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/service"
	"github.com/hujinrun/flowspace/internal/storage"
)

func GenerateLearningRoadmap(store storage.Store) gin.HandlerFunc {
	return GenerateLearningRoadmapWithAI(store, nil)
}

func GenerateLearningRoadmapWithAI(store storage.Store, chat WorkspaceChatService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req model.GenerateLearningRoadmapRequest
		if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
			badRequest(c, "invalid roadmap generation prompt")
			return
		}
		var generator service.TextGenerator
		allowTemplateFallback := true
		if identity, ok := auth.IdentityFromContext(c.Request.Context()); ok && chat != nil {
			if features, ok := chat.(WorkspaceAIFeatureService); ok {
				enabled, fallback, err := features.ResolveFeature(c.Request.Context(), identity.WorkspaceID, "roadmap_generation")
				if err != nil {
					internalError(c, "unable to resolve roadmap AI settings")
					return
				}
				allowTemplateFallback = fallback == "template"
				if enabled {
					generator = workspaceTextGenerator{service: chat, workspaceID: identity.WorkspaceID}
				}
			} else {
				generator = workspaceTextGenerator{service: chat, workspaceID: identity.WorkspaceID}
			}
		}
		roadmap, err := service.GenerateLearningRoadmapWithPromptAndAIPolicy(c.Request.Context(), store, c.Param("id"), req.Prompt, generator, allowTemplateFallback)
		if err != nil {
			internalError(c, err.Error())
			return
		}
		created(c, gin.H{"roadmap": roadmap})
	}
}

func GetLearningRoadmap(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		roadmap, err := service.GetLearningRoadmap(c.Request.Context(), store, c.Param("id"))
		if err != nil {
			if err == sql.ErrNoRows {
				notFound(c, "roadmap not found")
				return
			}
			internalError(c, "failed to get roadmap")
			return
		}
		success(c, gin.H{"roadmap": roadmap})
	}
}

func UpdateRoadmapNode(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req model.UpdateRoadmapNodeRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			badRequest(c, "invalid request body")
			return
		}
		node, err := service.UpdateRoadmapNode(c.Request.Context(), store, c.Param("id"), &req)
		if err != nil {
			notFound(c, "roadmap node not found")
			return
		}
		success(c, gin.H{"node": node})
	}
}

func CreateRoadmapNode(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req model.CreateRoadmapNodeRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			badRequest(c, "node title is required")
			return
		}
		node, err := service.CreateRoadmapNode(c.Request.Context(), store, c.Param("id"), &req)
		if err != nil {
			if err == sql.ErrNoRows {
				notFound(c, "roadmap or parent node not found")
				return
			}
			badRequest(c, err.Error())
			return
		}
		created(c, gin.H{"node": node})
	}
}

func UpdateRoadmapLayout(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req model.UpdateRoadmapLayoutRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			badRequest(c, "invalid request body")
			return
		}
		if err := service.UpdateRoadmapLayout(c.Request.Context(), store, c.Param("id"), req.Nodes); err != nil {
			internalError(c, "failed to save roadmap layout")
			return
		}
		noContent(c)
	}
}

func OptimizeRoadmapLayout(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		roadmap, err := service.OptimizeRoadmapLayout(c.Request.Context(), store, c.Param("id"))
		if err != nil {
			if err == sql.ErrNoRows {
				notFound(c, "roadmap not found")
				return
			}
			internalError(c, "failed to optimize roadmap layout")
			return
		}
		success(c, gin.H{"roadmap": roadmap})
	}
}

func DeleteRoadmapNode(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := service.DeleteRoadmapNode(c.Request.Context(), store, c.Param("id")); err != nil {
			notFound(c, "roadmap node not found")
			return
		}
		noContent(c)
	}
}

func SearchRoadmapNodeResources(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req model.SearchRoadmapResourcesRequest
		if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
			badRequest(c, "invalid search request")
			return
		}
		result, err := service.SearchRoadmapNodeResources(c.Request.Context(), store, c.Param("id"), &req)
		if err != nil {
			internalError(c, "failed to search roadmap resources")
			return
		}
		success(c, gin.H{"node_id": result.NodeID, "query": result.Query, "resources": result.Resources})
	}
}

func AddRoadmapNodeResource(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req model.CreateRoadmapResourceRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			badRequest(c, "resource title and url are required")
			return
		}
		resource, err := service.AddRoadmapNodeResource(c.Request.Context(), store, c.Param("id"), &req)
		if err != nil {
			badRequest(c, err.Error())
			return
		}
		created(c, gin.H{"resource": resource})
	}
}

func DeleteRoadmapResource(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := service.DeleteRoadmapResource(c.Request.Context(), store, c.Param("id")); err != nil {
			notFound(c, "roadmap resource not found")
			return
		}
		noContent(c)
	}
}
