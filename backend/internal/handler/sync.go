package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/service"
	"github.com/hujinrun/flowspace/internal/storage"
)

var (
	errSyncTargetInUse          = errors.New("sync target is in use")
	errSyncTargetIdentityLocked = errors.New("sync target identity is locked")
)

func ListSyncTargets(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		targets, err := store.Sync().ListTargets(c.Request.Context())
		if err != nil {
			internalError(c, "failed to list sync targets")
			return
		}
		success(c, gin.H{"targets": targets})
	}
}

func SaveSyncTarget(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req model.SaveSyncTargetRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			badRequest(c, "invalid sync target")
			return
		}
		target := syncTargetFromRequest(&req)
		if err := validateSyncTargetRequest(target); err != nil {
			badRequest(c, err.Error())
			return
		}
		if req.IsDefault == nil {
			target.IsDefault = true
		}
		if err := store.Sync().SaveTarget(c.Request.Context(), target); err != nil {
			internalError(c, "failed to save sync target")
			return
		}
		success(c, gin.H{"target": target})
	}
}

func UpdateSyncTarget(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req model.SaveSyncTargetRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			badRequest(c, "invalid sync target")
			return
		}
		target := syncTargetFromRequest(&req)
		target.ID = c.Param("id")
		if strings.TrimSpace(target.ID) == "" {
			badRequest(c, "sync target id is required")
			return
		}
		if err := validateSyncTargetRequest(target); err != nil {
			badRequest(c, err.Error())
			return
		}
		err := updateSyncTargetGuarded(c.Request.Context(), store, target, req.IsDefault == nil, syncTargetIdentityChanged)
		if errors.Is(err, sql.ErrNoRows) {
			notFound(c, "sync target not found")
			return
		}
		if errors.Is(err, errSyncTargetIdentityLocked) {
			errorResponse(c, http.StatusConflict, "target_identity_locked", "sync target identity is locked")
			return
		}
		if err != nil && strings.Contains(err.Error(), "sync target config") {
			badRequest(c, err.Error())
			return
		}
		if err != nil {
			internalError(c, "failed to update sync target")
			return
		}
		success(c, gin.H{"target": target})
	}
}

func DeleteSyncTarget(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		targetID := strings.TrimSpace(c.Param("id"))
		if targetID == "" {
			badRequest(c, "sync target id is required")
			return
		}
		err := deleteSyncTargetGuarded(c.Request.Context(), store, targetID)
		if errors.Is(err, sql.ErrNoRows) {
			notFound(c, "sync target not found")
			return
		}
		if errors.Is(err, errSyncTargetInUse) {
			errorResponse(c, http.StatusConflict, "target_in_use", "sync target is in use")
			return
		}
		if err != nil {
			internalError(c, "failed to delete sync target")
			return
		}
		noContent(c)
	}
}

func TestObsidianTarget(c *gin.Context) {
	var req model.SaveSyncTargetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid sync target")
		return
	}
	target := syncTargetFromRequest(&req)
	target.Enabled = true
	if err := service.TestObsidianTarget(target); err != nil {
		badRequest(c, err.Error())
		return
	}
	success(c, gin.H{"ok": true})
}

func TestNotionTarget(c *gin.Context) {
	var req model.SaveSyncTargetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid sync target")
		return
	}
	target := syncTargetFromRequest(&req)
	target.Type = "notion"
	target.Enabled = true
	if err := service.TestNotionTarget(target); err != nil {
		badRequest(c, err.Error())
		return
	}
	success(c, gin.H{"ok": true})
}

func SyncObsidianNote(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		noteID := strings.TrimSpace(c.Param("id"))
		if !ensureLegacyNoteSyncCompatible(c, store, noteID, "obsidian") {
			return
		}
		item, err := service.SyncNoteToObsidian(noteID)
		if err != nil {
			internalError(c, err.Error())
			return
		}
		success(c, gin.H{"item": item})
	}
}

func SyncNotionNote(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		noteID := strings.TrimSpace(c.Param("id"))
		if noteID == "" {
			badRequest(c, "note id is required")
			return
		}
		if !ensureLegacyNoteSyncCompatible(c, store, noteID, "notion") {
			return
		}
		item, err := service.SyncNoteToNotion(noteID)
		if err != nil {
			notionNoteSyncError(c, err)
			return
		}
		success(c, gin.H{"item": item})
	}
}

func SyncObsidianFolder(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !ensureLegacyBatchSyncCompatible(c, store, "obsidian") {
			return
		}
		notes, _, err := service.GetNotes(c.Request.Context(), store, c.Param("folder_id"), "", "recent", false, 1, 10000)
		if err != nil {
			internalError(c, "failed to load notes")
			return
		}
		result := service.SyncNotesToObsidian(notes)
		success(c, gin.H{"result": result})
	}
}

func SyncObsidianAll(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !ensureLegacyBatchSyncCompatible(c, store, "obsidian") {
			return
		}
		notes, _, err := service.GetNotes(c.Request.Context(), store, "", "", "recent", false, 1, 10000)
		if err != nil {
			internalError(c, "failed to load notes")
			return
		}
		result := service.SyncNotesToObsidian(notes)
		success(c, gin.H{"result": result})
	}
}

func SyncNotionAll(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !ensureLegacyBatchSyncCompatible(c, store, "notion") {
			return
		}
		result := service.SyncNotionAll()
		success(c, gin.H{"result": result})
	}
}

func SyncObsidianPull(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !ensureLegacyBatchSyncCompatible(c, store, "obsidian") {
			return
		}
		result := service.SyncObsidianPull()
		success(c, gin.H{"result": result})
	}
}

func SyncNotionPull(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !ensureLegacyBatchSyncCompatible(c, store, "notion") {
			return
		}
		result := service.SyncNotionPull()
		success(c, gin.H{"result": result})
	}
}

func SyncObsidianBidirectional(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !ensureLegacyBatchSyncCompatible(c, store, "obsidian") {
			return
		}
		result := service.SyncObsidianBidirectional()
		success(c, gin.H{"result": result})
	}
}

func SyncNotionBidirectional(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !ensureLegacyBatchSyncCompatible(c, store, "notion") {
			return
		}
		result := service.SyncNotionBidirectional()
		success(c, gin.H{"result": result})
	}
}

func ListObsidianDeletions(c *gin.Context) {
	items, err := service.ListObsidianDeletionCandidates()
	if err != nil {
		internalError(c, err.Error())
		return
	}
	success(c, gin.H{"items": items})
}

func ListNotionDeletions(c *gin.Context) {
	items, err := service.ListNotionDeletionCandidates()
	if err != nil {
		notionDeletionError(c, err)
		return
	}
	success(c, gin.H{"items": items})
}

func ConfirmObsidianDeletion(c *gin.Context) {
	if err := service.ConfirmObsidianDeletion(c.Param("note_id")); err != nil {
		obsidianDeletionError(c, err)
		return
	}
	noContent(c)
}

func ConfirmNotionDeletion(c *gin.Context) {
	if err := service.ConfirmNotionDeletion(c.Param("note_id")); err != nil {
		notionDeletionError(c, err)
		return
	}
	noContent(c)
}

func RestoreObsidianDeletion(c *gin.Context) {
	item, err := service.RestoreObsidianDeletion(c.Param("note_id"))
	if err != nil {
		obsidianDeletionError(c, err)
		return
	}
	success(c, gin.H{"item": item})
}

func RestoreNotionDeletion(c *gin.Context) {
	item, err := service.RestoreNotionDeletion(c.Param("note_id"))
	if err != nil {
		notionDeletionError(c, err)
		return
	}
	success(c, gin.H{"item": item})
}

func obsidianDeletionError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrObsidianDeletionNotFound):
		notFound(c, err.Error())
	case errors.Is(err, service.ErrObsidianDeletionConflict):
		errorResponse(c, http.StatusConflict, "CONFLICT", err.Error())
	case errors.Is(err, service.ErrObsidianDeletionInvalidState):
		badRequest(c, err.Error())
	default:
		internalError(c, err.Error())
	}
}

func notionDeletionError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrNotionDeletionNotFound):
		notFound(c, err.Error())
	case errors.Is(err, service.ErrNotionDeletionConflict):
		errorResponse(c, http.StatusConflict, "CONFLICT", err.Error())
	case errors.Is(err, service.ErrNotionDeletionInvalidState):
		badRequest(c, err.Error())
	default:
		internalError(c, err.Error())
	}
}

func notionNoteSyncError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		notFound(c, err.Error())
	default:
		internalError(c, err.Error())
	}
}

func GetNoteSyncState(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		getNoteSyncStateWithStore(c, store)
	}
}

func syncTargetFromRequest(req *model.SaveSyncTargetRequest) *model.SyncTarget {
	syncType := strings.TrimSpace(req.Type)
	if syncType == "" {
		syncType = "obsidian"
	}
	configJSON := strings.TrimSpace(req.ConfigJSON)
	if configJSON == "" {
		configJSON = "{}"
	}
	isDefault := false
	if req.IsDefault != nil {
		isDefault = *req.IsDefault
	}
	return &model.SyncTarget{
		Type:       syncType,
		Name:       req.Name,
		VaultPath:  req.VaultPath,
		BaseFolder: req.BaseFolder,
		ConfigJSON: configJSON,
		Enabled:    req.Enabled,
		AutoSync:   req.AutoSync,
		IsDefault:  isDefault,
	}
}

func validateSyncTargetRequest(target *model.SyncTarget) error {
	if target.Type == "obsidian" && (strings.TrimSpace(target.VaultPath) == "" || strings.TrimSpace(target.BaseFolder) == "") {
		return errors.New("obsidian sync target requires vault_path and base_folder")
	}
	return nil
}

type syncTargetIdentityChangedFunc func(existing, proposed *model.SyncTarget) (bool, error)

func updateSyncTargetGuarded(ctx context.Context, store storage.Store, target *model.SyncTarget, preserveIsDefault bool, identityChanged syncTargetIdentityChangedFunc) error {
	if target == nil {
		return errors.New("sync target is nil")
	}
	return store.Transact(ctx, func(tx storage.Store) error {
		existing, err := tx.Sync().LockTarget(ctx, target.ID)
		if err != nil {
			return err
		}
		target.CreatedAt = existing.CreatedAt
		if preserveIsDefault {
			target.IsDefault = existing.IsDefault
		}
		inUse, err := syncTargetInUse(ctx, tx.Sync(), target.ID)
		if err != nil {
			return err
		}
		if inUse {
			changed, err := identityChanged(existing, target)
			if err != nil {
				return err
			}
			if changed {
				return errSyncTargetIdentityLocked
			}
		}
		return tx.Sync().SaveTarget(ctx, target)
	})
}

func deleteSyncTargetGuarded(ctx context.Context, store storage.Store, targetID string) error {
	return store.Transact(ctx, func(tx storage.Store) error {
		if _, err := tx.Sync().LockTarget(ctx, targetID); err != nil {
			return err
		}
		inUse, err := syncTargetInUse(ctx, tx.Sync(), targetID)
		if err != nil {
			return err
		}
		if inUse {
			return errSyncTargetInUse
		}
		return tx.Sync().DeleteTarget(ctx, targetID)
	})
}

func syncTargetInUse(ctx context.Context, repo storage.SyncRepository, targetID string) (bool, error) {
	bindings, err := repo.CountBindingsByTarget(ctx, targetID)
	if err != nil {
		return false, err
	}
	if bindings > 0 {
		return true, nil
	}
	claims, err := repo.CountClaimsByTarget(ctx, targetID)
	if err != nil {
		return false, err
	}
	if claims > 0 {
		return true, nil
	}
	states, err := repo.CountStatesByTarget(ctx, targetID)
	if err != nil {
		return false, err
	}
	return states > 0, nil
}

func syncTargetIdentityChanged(existing, next *model.SyncTarget) (bool, error) {
	if strings.TrimSpace(existing.Type) != strings.TrimSpace(next.Type) {
		return true, nil
	}
	switch strings.TrimSpace(existing.Type) {
	case "obsidian":
		return obsidianIdentity(existing) != obsidianIdentity(next), nil
	case "notion":
		existingDataSourceID, err := notionDataSourceID(existing.ConfigJSON)
		if err != nil {
			return false, err
		}
		nextDataSourceID, err := notionDataSourceID(next.ConfigJSON)
		if err != nil {
			return false, err
		}
		return existingDataSourceID != nextDataSourceID, nil
	default:
		return false, nil
	}
}

func obsidianIdentity(target *model.SyncTarget) string {
	windowsLike := isWindowsLikePath(target.VaultPath)
	return normalizeSyncPathIdentity(target.VaultPath, windowsLike) + "\n" + normalizeSyncPathIdentity(target.BaseFolder, windowsLike)
}

func normalizeSyncPathIdentity(value string, windowsLike bool) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	normalized := strings.ReplaceAll(trimmed, "\\", "/")
	normalized = path.Clean(normalized)
	if normalized == "." {
		return ""
	}
	if windowsLike || isWindowsLikePath(trimmed) {
		normalized = strings.ToLower(normalized)
	}
	return normalized
}

func isWindowsLikePath(value string) bool {
	if strings.Contains(value, "\\") {
		return true
	}
	trimmed := strings.TrimSpace(value)
	return len(trimmed) >= 2 && trimmed[1] == ':'
}

func notionDataSourceID(configJSON string) (string, error) {
	raw := strings.TrimSpace(configJSON)
	if raw == "" {
		return "", nil
	}
	var config map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		return "", errors.New("sync target config_json must be a JSON object")
	}
	value, ok := config["data_source_id"]
	if !ok || value == nil {
		return "", nil
	}
	dataSourceID, ok := value.(string)
	if !ok {
		return "", errors.New("sync target config.data_source_id must be a string")
	}
	return strings.TrimSpace(dataSourceID), nil
}
