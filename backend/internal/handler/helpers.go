package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/model"
)

func success(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, model.APIResponse{Data: data})
}

func successWithPagination(c *gin.Context, data interface{}, page, pageSize, total int) {
	c.JSON(http.StatusOK, model.APIResponse{
		Data: data,
		Pagination: &model.Pagination{
			Page:     page,
			PageSize: pageSize,
			Total:    total,
		},
	})
}

func created(c *gin.Context, data interface{}) {
	c.JSON(http.StatusCreated, model.APIResponse{Data: data})
}

func noContent(c *gin.Context) {
	c.Status(http.StatusNoContent)
}

func errorResponse(c *gin.Context, status int, code, message string) {
	c.JSON(status, model.APIResponse{
		Error: &model.APIError{Code: code, Message: message},
	})
}

func badRequest(c *gin.Context, msg string) {
	errorResponse(c, http.StatusBadRequest, "BAD_REQUEST", msg)
}

func notFound(c *gin.Context, msg string) {
	errorResponse(c, http.StatusNotFound, "NOT_FOUND", msg)
}

func internalError(c *gin.Context, msg string) {
	errorResponse(c, http.StatusInternalServerError, "INTERNAL_ERROR", msg)
}

func conflict(c *gin.Context, code, msg string) {
	errorResponse(c, http.StatusConflict, code, msg)
}

func getPagination(c *gin.Context) (int, int) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	} else if pageSize > 100 {
		pageSize = 100
	}
	return page, pageSize
}
