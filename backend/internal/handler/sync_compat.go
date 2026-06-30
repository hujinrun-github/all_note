package handler

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

func getNoteSyncStateWithStore(c *gin.Context, store storage.Store) {
	noteID := strings.TrimSpace(c.Param("id"))
	if noteID == "" {
		badRequest(c, "note id is required")
		return
	}
	targetType := requestedSyncTargetType(c)
	if targetType == "" {
		badRequest(c, "unsupported sync target")
		return
	}

	response, err := buildNoteSyncStateCompatibilityResponse(c.Request.Context(), store.Sync(), noteID, targetType)
	if err != nil {
		internalError(c, "failed to get sync state")
		return
	}
	success(c, response)
}

func buildNoteSyncStateCompatibilityResponse(ctx context.Context, syncRepo storage.SyncRepository, noteID string, targetType string) (model.NoteSyncStateCompatibilityResponse, error) {
	binding, err := syncRepo.GetBinding(ctx, noteID)
	if err == nil {
		return noteSyncStateForBinding(ctx, syncRepo, noteID, binding, targetType)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return model.NoteSyncStateCompatibilityResponse{}, err
	}

	target, err := syncRepo.GetDefaultTarget(ctx, targetType)
	if errors.Is(err, sql.ErrNoRows) {
		return model.NoteSyncStateCompatibilityResponse{
			Flags: model.SyncCompatibilityFlags{DefaultTargetMissing: true},
		}, nil
	}
	if err != nil {
		return model.NoteSyncStateCompatibilityResponse{}, err
	}
	state, err := syncRepo.GetState(ctx, noteID, target.ID)
	if errors.Is(err, sql.ErrNoRows) {
		state = nil
	} else if err != nil {
		return model.NoteSyncStateCompatibilityResponse{}, err
	}
	return model.NoteSyncStateCompatibilityResponse{
		State:  state,
		Target: target,
	}, nil
}

func noteSyncStateForBinding(ctx context.Context, syncRepo storage.SyncRepository, noteID string, binding *model.NoteSyncBinding, requestedType string) (model.NoteSyncStateCompatibilityResponse, error) {
	target, err := syncRepo.GetTarget(ctx, binding.TargetID)
	if err != nil {
		return model.NoteSyncStateCompatibilityResponse{}, err
	}
	if target.Type != requestedType {
		response := model.NoteSyncStateCompatibilityResponse{
			Flags: model.SyncCompatibilityFlags{
				BindingMismatch: true,
				BoundTargetID:   target.ID,
				BoundTargetName: target.Name,
			},
		}
		if requestedTarget, err := syncRepo.GetDefaultTarget(ctx, requestedType); err == nil {
			response.Target = requestedTarget
		} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return model.NoteSyncStateCompatibilityResponse{}, err
		}
		return response, nil
	}
	state, err := syncRepo.GetState(ctx, noteID, target.ID)
	if errors.Is(err, sql.ErrNoRows) {
		state = nil
	} else if err != nil {
		return model.NoteSyncStateCompatibilityResponse{}, err
	}
	return model.NoteSyncStateCompatibilityResponse{
		State:  state,
		Target: target,
	}, nil
}

func ensureLegacyNoteSyncCompatible(c *gin.Context, store storage.Store, noteID string, targetType string) bool {
	syncRepo := store.Sync()
	binding, err := syncRepo.GetBinding(c.Request.Context(), noteID)
	if err == nil {
		target, err := syncRepo.GetTarget(c.Request.Context(), binding.TargetID)
		if err != nil {
			internalError(c, "failed to get sync binding target")
			return false
		}
		if target.Type != targetType {
			syncCompatibilityConflict(c, "binding_mismatch")
			return false
		}
		defaultTarget, err := syncRepo.GetDefaultTarget(c.Request.Context(), targetType)
		if errors.Is(err, sql.ErrNoRows) {
			syncCompatibilityConflict(c, "default_target_missing")
			return false
		}
		if err != nil {
			internalError(c, "failed to get default sync target")
			return false
		}
		if defaultTarget.ID != binding.TargetID {
			syncCompatibilityConflict(c, "binding_mismatch")
			return false
		}
		return true
	}
	if !errors.Is(err, sql.ErrNoRows) {
		internalError(c, "failed to get sync binding")
		return false
	}
	if _, err := syncRepo.GetDefaultTarget(c.Request.Context(), targetType); errors.Is(err, sql.ErrNoRows) {
		syncCompatibilityConflict(c, "default_target_missing")
		return false
	} else if err != nil {
		internalError(c, "failed to get default sync target")
		return false
	}
	return true
}

func ensureLegacyBatchSyncCompatible(c *gin.Context, store storage.Store, targetType string) bool {
	syncRepo := store.Sync()
	defaultTarget, err := syncRepo.GetDefaultTarget(c.Request.Context(), targetType)
	if errors.Is(err, sql.ErrNoRows) {
		syncCompatibilityConflict(c, "default_target_missing")
		return false
	}
	if err != nil {
		internalError(c, "failed to get default sync target")
		return false
	}
	targets, err := syncRepo.ListTargets(c.Request.Context())
	if err != nil {
		internalError(c, "failed to list sync targets")
		return false
	}
	for _, target := range targets {
		if target.ID == defaultTarget.ID {
			continue
		}
		count, err := syncRepo.CountBindingsByTarget(c.Request.Context(), target.ID)
		if err != nil {
			internalError(c, "failed to count sync bindings")
			return false
		}
		if count > 0 {
			syncCompatibilityConflict(c, "binding_mismatch")
			return false
		}
	}
	return true
}

func syncCompatibilityConflict(c *gin.Context, code string) {
	message := "sync binding compatibility conflict"
	if code == "default_target_missing" {
		message = "default sync target is missing"
	} else if code == "binding_mismatch" {
		message = "note is bound to another sync target"
	}
	errorResponse(c, http.StatusConflict, code, message)
}

func requestedSyncTargetType(c *gin.Context) string {
	targetType := strings.TrimSpace(c.Query("target"))
	if targetType == "" {
		targetType = "obsidian"
	}
	if targetType != "obsidian" && targetType != "notion" {
		return ""
	}
	return targetType
}
