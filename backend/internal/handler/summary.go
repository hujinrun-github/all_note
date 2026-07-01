package handler

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/service"
	"github.com/hujinrun/flowspace/internal/storage"
)

func GetSummary(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		fromTime, err := time.ParseInLocation("2006-01-02", c.DefaultQuery("from", ""), time.Local)
		if err != nil {
			badRequest(c, "invalid date format, expected YYYY-MM-DD")
			return
		}
		toTime, err := time.ParseInLocation("2006-01-02", c.DefaultQuery("to", ""), time.Local)
		if err != nil {
			badRequest(c, "invalid date format, expected YYYY-MM-DD")
			return
		}
		if fromTime.After(toTime) {
			badRequest(c, "from date must be before to date")
			return
		}
		toTime = toTime.Add(24 * time.Hour)

		page, pageSize := getPagination(c)
		params := model.SummaryParams{
			From: fromTime.Unix(), To: toTime.Unix(),
			Page: page, PageSize: pageSize,
		}
		data, err := service.GetSummary(c.Request.Context(), store, params)
		if err != nil {
			internalError(c, "failed to get summary")
			return
		}
		successWithPagination(c, data, page, pageSize, data.PaginationTotal())
	}
}
