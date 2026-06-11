package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/service"
)

func GetFolders(c *gin.Context) {
	folders, err := service.GetFolders()
	if err != nil {
		internalError(c, "failed to get folders")
		return
	}
	success(c, gin.H{"folders": folders})
}
