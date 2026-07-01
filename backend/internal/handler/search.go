package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/service"
	"github.com/hujinrun/flowspace/internal/storage"
)

func Search(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		page, pageSize := getPagination(c)
		q := c.Query("q")

		results, total, err := service.Search(c.Request.Context(), store, q, page, pageSize)
		if err != nil {
			internalError(c, "search failed")
			return
		}
		successWithPagination(c, gin.H{"items": results}, page, pageSize, total)
	}
}
