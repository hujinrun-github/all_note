package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/repository"
	"github.com/hujinrun/flowspace/internal/service"
)

func GetToday(c *gin.Context) {
	store := repository.ActiveStore()
	recurrenceSvc := service.NewRecurrenceService()
	data, err := service.GetToday(c.Request.Context(), store, recurrenceSvc)
	if err != nil {
		internalError(c, "failed to get today data")
		return
	}
	success(c, data)
}
