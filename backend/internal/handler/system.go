package handler

import (
	"errors"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/service"
)

func ListLocalDirectories(c *gin.Context) {
	result, err := service.ListLocalDirectories(c.Query("path"))
	if err != nil {
		if errors.Is(err, service.ErrLocalDirectoryNotDirectory) {
			badRequest(c, "path is not a directory")
			return
		}
		badRequest(c, err.Error())
		return
	}
	success(c, gin.H{"directory": result})
}
