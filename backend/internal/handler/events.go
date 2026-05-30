package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/service"
)

func GetEvents(c *gin.Context) {
	page, pageSize := getPagination(c)
	month := c.DefaultQuery("month", "")

	events, total, err := service.GetEvents(month, page, pageSize)
	if err != nil {
		badRequest(c, "invalid month format, expected YYYY-MM")
		return
	}
	successWithPagination(c, gin.H{"events": events}, page, pageSize, total)
}

func CreateEvent(c *gin.Context) {
	var req model.CreateEventRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "title, start_time, and end_time are required")
		return
	}
	event, err := service.CreateEvent(&req)
	if err != nil {
		internalError(c, "failed to create event")
		return
	}
	created(c, gin.H{"event": event})
}

func UpdateEvent(c *gin.Context) {
	var req model.UpdateEventRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid request body")
		return
	}
	event, err := service.UpdateEvent(c.Param("id"), &req)
	if err != nil {
		notFound(c, "event not found")
		return
	}
	success(c, gin.H{"event": event})
}

func DeleteEvent(c *gin.Context) {
	if err := service.DeleteEvent(c.Param("id")); err != nil {
		internalError(c, "failed to delete event")
		return
	}
	noContent(c)
}
