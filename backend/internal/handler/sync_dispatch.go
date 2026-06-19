package handler

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/service"
)

func SyncNote(c *gin.Context) {
	noteID := strings.TrimSpace(c.Param("id"))
	if noteID == "" {
		badRequest(c, "note id is required")
		return
	}
	item, err := service.SyncNote(noteID)
	if err != nil {
		syncDispatchError(c, err)
		return
	}
	success(c, gin.H{"item": item})
}

func SyncTargetPush(c *gin.Context) {
	result, err := service.SyncTargetPush(strings.TrimSpace(c.Param("target_id")))
	if err != nil {
		syncDispatchError(c, err)
		return
	}
	success(c, gin.H{"result": result})
}

func SyncTargetPull(c *gin.Context) {
	result, err := service.SyncTargetPull(strings.TrimSpace(c.Param("target_id")))
	if err != nil {
		syncDispatchError(c, err)
		return
	}
	success(c, gin.H{"result": result})
}

func SyncTargetBidirectional(c *gin.Context) {
	result, err := service.SyncTargetBidirectional(strings.TrimSpace(c.Param("target_id")))
	if err != nil {
		syncDispatchError(c, err)
		return
	}
	success(c, gin.H{"result": result})
}

func syncDispatchError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrSyncBindingRequired):
		errorResponse(c, http.StatusConflict, "binding_required", "note is not bound to a sync target")
	case errors.Is(err, service.ErrSyncBindingConflict):
		errorResponse(c, http.StatusConflict, "binding_mismatch", "note is bound to another sync target")
	case errors.Is(err, service.ErrSyncTargetNotFound), errors.Is(err, sql.ErrNoRows):
		errorResponse(c, http.StatusNotFound, "sync_target_not_found", "sync target not found")
	default:
		internalError(c, err.Error())
	}
}
