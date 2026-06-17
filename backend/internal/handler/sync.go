package handler

import (
	"database/sql"
	"errors"
	"net/http"
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
	if err := repository.SaveSyncTarget(target); err != nil {
		internalError(c, "failed to update sync target")
		return
	}
	success(c, gin.H{"target": target})
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
	return &model.SyncTarget{
		Type:       syncType,
		Name:       req.Name,
		VaultPath:  req.VaultPath,
		BaseFolder: req.BaseFolder,
		ConfigJSON: configJSON,
		Enabled:    req.Enabled,
		AutoSync:   req.AutoSync,
	}
}

func validateSyncTargetRequest(target *model.SyncTarget) error {
	if target.Type == "obsidian" && (strings.TrimSpace(target.VaultPath) == "" || strings.TrimSpace(target.BaseFolder) == "") {
		return errors.New("obsidian sync target requires vault_path and base_folder")
	}
	return nil
}
