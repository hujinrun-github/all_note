package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/service"
)

func Search(c *gin.Context) {
	page, pageSize := getPagination(c)
	q := c.Query("q")

	results, total, err := service.Search(q, page, pageSize)
	if err != nil {
		internalError(c, "search failed")
		return
	}
	successWithPagination(c, gin.H{"items": results}, page, pageSize, total)
}
