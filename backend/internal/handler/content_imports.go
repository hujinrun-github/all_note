package handler

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/contentimport"
	"github.com/hujinrun/flowspace/internal/contentsource"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

type resolveContentImportRequest struct {
	SourceURL string `json:"source_url" binding:"required"`
}

func ResolveContentImport(service *contentimport.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request resolveContentImportRequest
		if service == nil || c.ShouldBindJSON(&request) != nil {
			badRequest(c, "source_url is required")
			return
		}
		episode, err := service.Resolve(c.Request.Context(), request.SourceURL)
		if err != nil {
			handleContentImportError(c, err)
			return
		}
		success(c, gin.H{"episode": episode})
	}
}

func CreateContentImport(service *contentimport.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request contentimport.CreateRequest
		if service == nil || c.ShouldBindJSON(&request) != nil || request.SourceURL == "" {
			badRequest(c, "source_url is required")
			return
		}
		item, err := service.Create(c.Request.Context(), c.GetHeader("Idempotency-Key"), request)
		if err != nil {
			handleContentImportError(c, err)
			return
		}
		c.JSON(http.StatusAccepted, model.APIResponse{Data: gin.H{"import": item}})
	}
}

func ListContentImports(service *contentimport.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		page, pageSize := getPagination(c)
		items, total, err := service.List(c.Request.Context(), model.ContentImportFilter{Status: c.DefaultQuery("status", "all"), Page: page, PageSize: pageSize})
		if err != nil {
			handleContentImportError(c, err)
			return
		}
		successWithPagination(c, gin.H{"imports": items}, page, pageSize, total)
	}
}

func GetContentImport(service *contentimport.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		item, err := service.Get(c.Request.Context(), c.Param("id"))
		if err != nil {
			handleContentImportError(c, err)
			return
		}
		success(c, gin.H{"import": item})
	}
}

func GetContentImportForNote(service *contentimport.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		item, err := service.GetByResultNoteID(c.Request.Context(), c.Param("id"))
		if err != nil {
			handleContentImportError(c, err)
			return
		}
		success(c, gin.H{"import": item})
	}
}

func GetContentImportTranscript(service *contentimport.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		text, err := service.GetTranscript(c.Request.Context(), c.Param("id"))
		if err != nil {
			handleContentImportError(c, err)
			return
		}
		success(c, gin.H{"transcript": text})
	}
}

func CancelContentImport(service *contentimport.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		item, err := service.Cancel(c.Request.Context(), c.Param("id"))
		if err != nil {
			handleContentImportError(c, err)
			return
		}
		success(c, gin.H{"import": item})
	}
}

func RetryContentImport(service *contentimport.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		item, err := service.Retry(c.Request.Context(), c.Param("id"))
		if err != nil {
			handleContentImportError(c, err)
			return
		}
		c.JSON(http.StatusAccepted, model.APIResponse{Data: gin.H{"import": item}})
	}
}

func DeleteContentImport(service *contentimport.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := service.Delete(c.Request.Context(), c.Param("id")); err != nil {
			handleContentImportError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	}
}

func handleContentImportError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, contentimport.ErrInvalidRequest), errors.Is(err, contentsource.ErrInvalidURL):
		errorResponse(c, http.StatusBadRequest, "SOURCE_URL_INVALID", "链接格式不正确")
	case errors.Is(err, contentsource.ErrEpisodeRequired):
		errorResponse(c, http.StatusBadRequest, "EPISODE_LINK_REQUIRED", "请粘贴具体单集链接")
	case errors.Is(err, contentsource.ErrUnsupportedSource):
		errorResponse(c, http.StatusBadRequest, "SOURCE_NOT_SUPPORTED", "目前仅支持小宇宙和 Apple Podcasts 单集链接")
	case errors.Is(err, contentsource.ErrSourceUnavailable):
		errorResponse(c, http.StatusUnprocessableEntity, "SOURCE_MEDIA_UNAVAILABLE", "来源页面暂时无法读取")
	case errors.Is(err, storage.ErrMutationIDReused):
		conflict(c, "MUTATION_ID_REUSED", "Idempotency-Key 已被其他请求使用")
	case errors.Is(err, storage.ErrContentImportNotRetryable):
		conflict(c, "IMPORT_NOT_RETRYABLE", "当前任务状态不允许此操作")
	case errors.Is(err, storage.ErrContentImportNotDeletable):
		conflict(c, "IMPORT_NOT_DELETABLE", "进行中的导入任务不能删除，请先取消任务")
	case errors.Is(err, sql.ErrNoRows):
		notFound(c, "导入任务不存在")
	default:
		internalError(c, "内容导入服务暂时不可用")
	}
}
