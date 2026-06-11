package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/service"
)

func GetInbox(c *gin.Context) {
	page, pageSize := getPagination(c)
	kind := c.DefaultQuery("kind", "all")

	items, total, err := service.GetInboxItems(kind, page, pageSize)
	if err != nil {
		internalError(c, "failed to get inbox items")
		return
	}
	successWithPagination(c, gin.H{"items": items}, page, pageSize, total)
}

func CreateInboxItem(c *gin.Context) {
	var req model.CreateInboxRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "kind and title are required")
		return
	}
	item, err := service.CreateInboxItem(&req)
	if err != nil {
		internalError(c, "failed to create inbox item")
		return
	}
	created(c, gin.H{"item": item})
}

func ConvertInboxItem(c *gin.Context) {
	var req model.ConvertInboxRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "kind is required")
		return
	}
	result, err := service.ConvertInboxItem(c.Param("id"), &req)
	if err != nil {
		badRequest(c, err.Error())
		return
	}
	created(c, gin.H{"item": result})
}

func DeleteInboxItem(c *gin.Context) {
	if err := service.DeleteInboxItem(c.Param("id")); err != nil {
		internalError(c, "failed to delete inbox item")
		return
	}
	noContent(c)
}

func BatchInbox(c *gin.Context) {
	var req model.BatchInboxRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "ids and action are required")
		return
	}
	affected, err := service.BatchInbox(&req)
	if err != nil {
		badRequest(c, err.Error())
		return
	}
	success(c, gin.H{"affected": affected})
}
