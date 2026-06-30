package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/service"
	"github.com/hujinrun/flowspace/internal/storage"
)

func GetEvents(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		page, pageSize := getPagination(c)
		month := c.DefaultQuery("month", "")

		events, total, err := service.GetEvents(c.Request.Context(), store, month, page, pageSize)
		if err != nil {
			badRequest(c, "invalid month format, expected YYYY-MM")
			return
		}
		successWithPagination(c, gin.H{"events": events}, page, pageSize, total)
	}
}

func CreateEvent(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req model.CreateEventRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			badRequest(c, "title, start_time, and end_time are required")
			return
		}
		event, err := service.CreateEvent(c.Request.Context(), store, &req)
		if err != nil {
			internalError(c, "failed to create event")
			return
		}
		created(c, gin.H{"event": event})
	}
}

func UpdateEvent(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req model.UpdateEventRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			badRequest(c, "invalid request body")
			return
		}
		event, err := service.UpdateEvent(c.Request.Context(), store, c.Param("id"), &req)
		if err != nil {
			notFound(c, "event not found")
			return
		}
		success(c, gin.H{"event": event})
	}
}

func DeleteEvent(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := service.DeleteEvent(c.Request.Context(), store, c.Param("id")); err != nil {
			internalError(c, "failed to delete event")
			return
		}
		noContent(c)
	}
}
