package handler

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/repository"
	"github.com/hujinrun/flowspace/internal/service"
)

func ListSyncTargets(c *gin.Context) {
	targets, err := repository.ListSyncTargets()
	if err != nil {
		internalError(c, "failed to list sync targets")
		return
	}
	success(c, gin.H{"targets": targets})
}

func SaveSyncTarget(c *gin.Context) {
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
	if err := repository.SaveSyncTarget(target); err != nil {
		internalError(c, "failed to save sync target")
		return
	}
	success(c, gin.H{"target": target})
}

func UpdateSyncTarget(c *gin.Context) {
	var req model.SaveSyncTargetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid sync target")
		return
	}

	target := syncTargetFromRequest(&req)
	target.ID = c.Param("id")
	if target.ID == "" {
		badRequest(c, "sync target id is required")
		return
	}
	if err := validateSyncTargetRequest(target); err != nil {
		badRequest(c, err.Error())
		return
	}
	err := repository.UpdateSyncTargetGuarded(target, req.IsDefault == nil, syncTargetIdentityChanged)
	if errors.Is(err, sql.ErrNoRows) {
		notFound(c, "sync target not found")
		return
	}
	if errors.Is(err, repository.ErrSyncTargetIdentityLocked) {
		errorResponse(c, http.StatusConflict, "target_identity_locked", "同步目标已被使用，不能修改身份字段")
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

func DeleteSyncTarget(c *gin.Context) {
	targetID := strings.TrimSpace(c.Param("id"))
	if targetID == "" {
		badRequest(c, "sync target id is required")
		return
	}
	err := repository.DeleteSyncTargetGuarded(targetID)
	if errors.Is(err, sql.ErrNoRows) {
		notFound(c, "sync target not found")
		return
	}
	if errors.Is(err, repository.ErrSyncTargetInUse) {
		errorResponse(c, http.StatusConflict, "target_in_use", "同步目标已被使用，不能删除")
		return
	}
	if err != nil {
		internalError(c, "failed to delete sync target")
		return
	}
	noContent(c)
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

func SyncObsidianNote(c *gin.Context) {
	item, err := service.SyncNoteToObsidian(c.Param("id"))
	if err != nil {
		internalError(c, err.Error())
		return
	}
	success(c, gin.H{"item": item})
}

func SyncNotionNote(c *gin.Context) {
	noteID := strings.TrimSpace(c.Param("id"))
	if noteID == "" {
		badRequest(c, "note id is required")
		return
	}
	item, err := service.SyncNoteToNotion(noteID)
	if err != nil {
		notionNoteSyncError(c, err)
		return
	}
	success(c, gin.H{"item": item})
}

func SyncObsidianFolder(c *gin.Context) {
	notes, _, err := service.GetNotes(c.Param("folder_id"), "", "recent", false, 1, 10000)
	if err != nil {
		internalError(c, "failed to load notes")
		return
	}
	result := service.SyncNotesToObsidian(notes)
	success(c, gin.H{"result": result})
}

func SyncObsidianAll(c *gin.Context) {
	notes, _, err := service.GetNotes("", "", "recent", false, 1, 10000)
	if err != nil {
		internalError(c, "failed to load notes")
		return
	}
	result := service.SyncNotesToObsidian(notes)
	success(c, gin.H{"result": result})
}

func SyncNotionAll(c *gin.Context) {
	result := service.SyncNotionAll()
	success(c, gin.H{"result": result})
}

func SyncObsidianPull(c *gin.Context) {
	result := service.SyncObsidianPull()
	success(c, gin.H{"result": result})
}

func SyncNotionPull(c *gin.Context) {
	result := service.SyncNotionPull()
	success(c, gin.H{"result": result})
}

func SyncObsidianBidirectional(c *gin.Context) {
	result := service.SyncObsidianBidirectional()
	success(c, gin.H{"result": result})
}

func SyncNotionBidirectional(c *gin.Context) {
	result := service.SyncNotionBidirectional()
	success(c, gin.H{"result": result})
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

func GetNoteSyncState(c *gin.Context) {
	targetType := strings.TrimSpace(c.Query("target"))
	if targetType == "" {
		targetType = "obsidian"
	}
	if targetType != "obsidian" && targetType != "notion" {
		badRequest(c, "unsupported sync target")
		return
	}

	target, err := repository.GetDefaultSyncTarget(targetType)
	if err == sql.ErrNoRows {
		success(c, gin.H{"state": nil})
		return
	}
	if err != nil {
		internalError(c, "failed to get sync target")
		return
	}

	state, err := repository.GetSyncState(c.Param("id"), target.ID)
	if err == sql.ErrNoRows {
		success(c, gin.H{"state": nil})
		return
	}
	if err != nil {
		internalError(c, "failed to get sync state")
		return
	}
	success(c, gin.H{"state": state})
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
