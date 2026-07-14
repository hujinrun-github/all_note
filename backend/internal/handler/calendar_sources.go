package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/service"
	"github.com/hujinrun/flowspace/internal/storage"
)

func GetCalendarProjectSources(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		sources, err := service.ListCalendarProjectSources(c.Request.Context(), store)
		if err != nil {
			internalError(c, "failed to list calendar project sources")
			return
		}
		success(c, sources)
	}
}

func SaveCalendarProjectSources(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req model.SaveCalendarProjectSourcesRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			badRequest(c, "invalid calendar project sources")
			return
		}
		sources, err := service.SaveCalendarProjectSources(c.Request.Context(), store, req.Sources)
		if err != nil {
			internalError(c, "failed to save calendar project sources")
			return
		}
		success(c, sources)
	}
}
