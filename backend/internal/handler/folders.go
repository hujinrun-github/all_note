package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/service"
	"github.com/hujinrun/flowspace/internal/storage"
)

func GetFolders(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		folders, err := service.GetFolders(c.Request.Context(), store)
		if err != nil {
			internalError(c, "failed to get folders")
			return
		}
		success(c, gin.H{"folders": folders})
	}
}
