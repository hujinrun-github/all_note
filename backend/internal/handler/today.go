package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/service"
)

func GetToday(c *gin.Context) {
	data, err := service.GetToday()
	if err != nil {
		internalError(c, "failed to get today data")
		return
	}
	success(c, data)
}
