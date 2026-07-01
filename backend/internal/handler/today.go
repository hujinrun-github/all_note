package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/service"
	"github.com/hujinrun/flowspace/internal/storage"
)

func GetToday(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		recurrenceSvc := service.NewRecurrenceService()
		data, err := service.GetToday(c.Request.Context(), store, recurrenceSvc)
		if err != nil {
			internalError(c, "failed to get today data")
			return
		}
		success(c, data)
	}
}
