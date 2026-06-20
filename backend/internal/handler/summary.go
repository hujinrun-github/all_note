package handler

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/service"
)

func GetSummary(c *gin.Context) {
	fromTime, err := time.ParseInLocation("2006-01-02", c.DefaultQuery("from", ""), time.Local)
	if err != nil {
		badRequest(c, "日期格式无效，需要 YYYY-MM-DD")
		return
	}
	toTime, err := time.ParseInLocation("2006-01-02", c.DefaultQuery("to", ""), time.Local)
	if err != nil {
		badRequest(c, "日期格式无效，需要 YYYY-MM-DD")
		return
	}
	if !fromTime.Before(toTime) {
		badRequest(c, "起始日期必须早于结束日期")
		return
	}
	toTime = toTime.Add(24 * time.Hour)

	page, pageSize := getPagination(c)
	params := model.SummaryParams{
		From: fromTime.Unix(), To: toTime.Unix(),
		Page: page, PageSize: pageSize,
	}
	data, err := service.GetSummary(params)
	if err != nil {
		internalError(c, "获取总结失败")
		return
	}
	successWithPagination(c, data, page, pageSize, data.PaginationTotal())
}
