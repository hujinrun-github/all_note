package handler

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/service"
	"github.com/hujinrun/flowspace/internal/storage"
)

func SyncNote(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		noteID := strings.TrimSpace(c.Param("id"))
		if noteID == "" {
			badRequest(c, "note id is required")
			return
		}
		item, err := service.SyncNote(c.Request.Context(), store, noteID)
		if err != nil {
			syncDispatchError(c, err)
			return
		}
		success(c, gin.H{"item": item})
	}
}

func SyncTargetPush(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		result, err := service.SyncTargetPush(c.Request.Context(), store, strings.TrimSpace(c.Param("target_id")))
		if err != nil {
			syncDispatchError(c, err)
			return
		}
		success(c, gin.H{"result": result})
	}
}

func SyncTargetPull(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		result, err := service.SyncTargetPull(c.Request.Context(), store, strings.TrimSpace(c.Param("target_id")))
		if err != nil {
			syncDispatchError(c, err)
			return
		}
		success(c, gin.H{"result": result})
	}
}

func SyncTargetBidirectional(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		result, err := service.SyncTargetBidirectional(c.Request.Context(), store, strings.TrimSpace(c.Param("target_id")))
		if err != nil {
			syncDispatchError(c, err)
			return
		}
		success(c, gin.H{"result": result})
	}
}

func ListTargetDeletions(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		targetID := strings.TrimSpace(c.Param("target_id"))
		targetType, err := targetDeletionType(c, store, targetID)
		if err != nil {
			targetDeletionError(c, err)
			return
		}
		switch targetType {
		case "obsidian":
			items, err := service.ListObsidianDeletionCandidatesForTarget(targetID)
			if err != nil {
				targetDeletionError(c, err)
				return
			}
			success(c, gin.H{"items": items})
		case "notion":
			items, err := service.ListNotionDeletionCandidatesForTarget(targetID)
			if err != nil {
				targetDeletionError(c, err)
				return
			}
			success(c, gin.H{"items": items})
		default:
			badRequest(c, "unsupported sync target")
		}
	}
}

func ConfirmTargetDeletion(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		targetID := strings.TrimSpace(c.Param("target_id"))
		targetType, err := targetDeletionType(c, store, targetID)
		if err != nil {
			targetDeletionError(c, err)
			return
		}
		noteID := strings.TrimSpace(c.Param("note_id"))
		switch targetType {
		case "obsidian":
			err = service.ConfirmObsidianDeletionForTargetScoped(c.Request.Context(), store, noteID, targetID)
		case "notion":
			err = service.ConfirmNotionDeletionForTargetScoped(c.Request.Context(), store, noteID, targetID)
		default:
			badRequest(c, "unsupported sync target")
			return
		}
		if err != nil {
			targetDeletionError(c, err)
			return
		}
		noContent(c)
	}
}

func RestoreTargetDeletion(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		targetID := strings.TrimSpace(c.Param("target_id"))
		targetType, err := targetDeletionType(c, store, targetID)
		if err != nil {
			targetDeletionError(c, err)
			return
		}
		noteID := strings.TrimSpace(c.Param("note_id"))
		var item any
		switch targetType {
		case "obsidian":
			item, err = service.RestoreObsidianDeletionForTargetScoped(c.Request.Context(), store, noteID, targetID)
		case "notion":
			item, err = service.RestoreNotionDeletionForTargetScoped(c.Request.Context(), store, noteID, targetID)
		default:
			badRequest(c, "unsupported sync target")
			return
		}
		if err != nil {
			targetDeletionError(c, err)
			return
		}
		success(c, gin.H{"item": item})
	}
}

func targetDeletionType(c *gin.Context, store storage.Store, targetID string) (string, error) {
	if strings.TrimSpace(targetID) == "" {
		return "", service.ErrSyncTargetNotFound
	}
	target, err := store.Sync().GetTarget(c.Request.Context(), targetID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", service.ErrSyncTargetNotFound
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(target.Type), nil
}

func targetDeletionError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrSyncTargetNotFound), errors.Is(err, sql.ErrNoRows),
		errors.Is(err, service.ErrObsidianDeletionNotFound), errors.Is(err, service.ErrNotionDeletionNotFound):
		notFound(c, err.Error())
	case errors.Is(err, service.ErrObsidianDeletionConflict), errors.Is(err, service.ErrNotionDeletionConflict):
		errorResponse(c, http.StatusConflict, "CONFLICT", err.Error())
	case errors.Is(err, service.ErrObsidianDeletionInvalidState), errors.Is(err, service.ErrNotionDeletionInvalidState):
		badRequest(c, err.Error())
	default:
		internalError(c, err.Error())
	}
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
