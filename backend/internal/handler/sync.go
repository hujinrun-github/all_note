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
		success(c, gin.H{"targets": sanitizeSyncTargets(targets)})
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
		if err := normalizeSyncTargetConfigForStorage(target); err != nil {
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
		success(c, gin.H{"target": sanitizeSyncTarget(*target)})
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
		if err := normalizeSyncTargetConfigForStorage(target); err != nil {
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
		success(c, gin.H{"target": sanitizeSyncTarget(*target)})
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
	testNotionTargetWithStore(nil)(c)
}

func TestNotionTargetWithStore(store storage.Store) gin.HandlerFunc {
	return testNotionTargetWithStore(store)
}

func testNotionTargetWithStore(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req model.SaveSyncTargetRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			badRequest(c, "invalid sync target")
			return
		}
		target := syncTargetFromRequest(&req)
		target.ID = strings.TrimSpace(req.ID)
		target.Type = "notion"
		target.Enabled = true
		if err := mergeTestNotionTargetConfig(c.Request.Context(), store, target); err != nil {
			if strings.Contains(err.Error(), "sync target config") {
				badRequest(c, err.Error())
				return
			}
			internalError(c, "failed to load sync target")
			return
		}
		if err := service.TestNotionTarget(target); err != nil {
			badRequest(c, err.Error())
			return
		}
		success(c, gin.H{"ok": true})
	}
}

func mergeTestNotionTargetConfig(ctx context.Context, store storage.Store, target *model.SyncTarget) error {
	if store == nil || target == nil || strings.TrimSpace(target.ID) == "" {
		return nil
	}
	existing, err := store.Sync().GetTarget(ctx, target.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return mergeSyncTargetSecretConfig(existing, target)
}

func SyncObsidianNote(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		noteID := strings.TrimSpace(c.Param("id"))
		if !ensureLegacyNoteSyncCompatible(c, store, noteID, "obsidian") {
			return
		}
		item, err := service.SyncNoteToObsidianScoped(c.Request.Context(), store, noteID)
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
		item, err := service.SyncNoteToNotionScoped(c.Request.Context(), store, noteID)
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
		result := service.SyncNotesToObsidianScoped(c.Request.Context(), store, notes)
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
		result := service.SyncNotesToObsidianScoped(c.Request.Context(), store, notes)
		success(c, gin.H{"result": result})
	}
}

func SyncNotionAll(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !ensureLegacyBatchSyncCompatible(c, store, "notion") {
			return
		}
		result := service.SyncNotionAllScoped(c.Request.Context(), store)
		success(c, gin.H{"result": result})
	}
}

func SyncObsidianPull(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !ensureLegacyBatchSyncCompatible(c, store, "obsidian") {
			return
		}
		result := service.SyncObsidianPullScoped(c.Request.Context(), store)
		success(c, gin.H{"result": result})
	}
}

func SyncNotionPull(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !ensureLegacyBatchSyncCompatible(c, store, "notion") {
			return
		}
		result := service.SyncNotionPullScoped(c.Request.Context(), store)
		success(c, gin.H{"result": result})
	}
}

func SyncObsidianBidirectional(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !ensureLegacyBatchSyncCompatible(c, store, "obsidian") {
			return
		}
		result := service.SyncObsidianBidirectionalScoped(c.Request.Context(), store)
		success(c, gin.H{"result": result})
	}
}

func SyncNotionBidirectional(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !ensureLegacyBatchSyncCompatible(c, store, "notion") {
			return
		}
		result := service.SyncNotionBidirectionalScoped(c.Request.Context(), store)
		success(c, gin.H{"result": result})
	}
}

func ListObsidianDeletions(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		items, err := service.ListObsidianDeletionCandidatesScoped(c.Request.Context(), store)
		if err != nil {
			internalError(c, err.Error())
			return
		}
		success(c, gin.H{"items": items})
	}
}

func ListNotionDeletions(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		items, err := service.ListNotionDeletionCandidatesScoped(c.Request.Context(), store)
		if err != nil {
			notionDeletionError(c, err)
			return
		}
		success(c, gin.H{"items": items})
	}
}

func ConfirmObsidianDeletion(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := service.ConfirmObsidianDeletionScoped(c.Request.Context(), store, c.Param("note_id")); err != nil {
			obsidianDeletionError(c, err)
			return
		}
		noContent(c)
	}
}

func ConfirmNotionDeletion(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := service.ConfirmNotionDeletionScoped(c.Request.Context(), store, c.Param("note_id")); err != nil {
			notionDeletionError(c, err)
			return
		}
		noContent(c)
	}
}

func RestoreObsidianDeletion(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		item, err := service.RestoreObsidianDeletionScoped(c.Request.Context(), store, c.Param("note_id"))
		if err != nil {
			obsidianDeletionError(c, err)
			return
		}
		success(c, gin.H{"item": item})
	}
}

func RestoreNotionDeletion(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		item, err := service.RestoreNotionDeletionScoped(c.Request.Context(), store, c.Param("note_id"))
		if err != nil {
			notionDeletionError(c, err)
			return
		}
		success(c, gin.H{"item": item})
	}
}

func obsidianDeletionError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrObsidianDeletionNotFound):
		notFound(c, err.Error())
	case errors.Is(err, service.ErrObsidianDeletionConflict):
		errorResponse(c, http.StatusConflict, "CONFLICT", err.Error())
	case errors.Is(err, service.ErrSyncBindingConflict):
		errorResponse(c, http.StatusConflict, "binding_mismatch", err.Error())
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
	case errors.Is(err, service.ErrSyncBindingConflict):
		errorResponse(c, http.StatusConflict, "binding_mismatch", err.Error())
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

func normalizeSyncTargetConfigForStorage(target *model.SyncTarget) error {
	if target == nil || strings.TrimSpace(target.Type) != "notion" {
		return nil
	}
	config, err := syncTargetConfigObject(target.ConfigJSON)
	if err != nil {
		return err
	}
	normalizeNotionSecretConfig(config)
	target.ConfigJSON = mustMarshalSyncTargetConfig(config)
	return nil
}

func mergeSyncTargetSecretConfig(existing, next *model.SyncTarget) error {
	if existing == nil || next == nil || strings.TrimSpace(next.Type) != "notion" {
		return nil
	}
	existingConfig, err := syncTargetConfigObject(existing.ConfigJSON)
	if err != nil {
		return err
	}
	nextConfig, err := syncTargetConfigObject(next.ConfigJSON)
	if err != nil {
		return err
	}

	nextToken, hasNextToken := trimmedConfigString(nextConfig, "token")
	if hasNextToken && nextToken != "" {
		nextConfig["token"] = nextToken
	} else {
		if existingToken, hasExistingToken := trimmedConfigString(existingConfig, "token"); hasExistingToken && existingToken != "" {
			nextConfig["token"] = existingToken
		} else {
			delete(nextConfig, "token")
		}
	}

	nextTokenEnv, hasNextTokenEnv := trimmedConfigString(nextConfig, "token_env")
	if hasNextTokenEnv {
		if nextTokenEnv == "" || (hasNextToken && nextToken != "") {
			delete(nextConfig, "token_env")
		} else {
			nextConfig["token_env"] = nextTokenEnv
		}
	} else if existingTokenEnv, hasExistingTokenEnv := trimmedConfigString(existingConfig, "token_env"); hasExistingTokenEnv && existingTokenEnv != "" && nextToken == "" {
		nextConfig["token_env"] = existingTokenEnv
	}

	delete(nextConfig, "token_set")
	next.ConfigJSON = mustMarshalSyncTargetConfig(nextConfig)
	return nil
}

func sanitizeSyncTargets(targets []model.SyncTarget) []model.SyncTarget {
	sanitized := make([]model.SyncTarget, len(targets))
	for index, target := range targets {
		sanitized[index] = sanitizeSyncTarget(target)
	}
	return sanitized
}

func sanitizeSyncTarget(target model.SyncTarget) model.SyncTarget {
	if strings.TrimSpace(target.Type) != "notion" {
		return target
	}
	config, err := syncTargetConfigObject(target.ConfigJSON)
	if err != nil {
		target.ConfigJSON = "{}"
		return target
	}
	token, hasToken := trimmedConfigString(config, "token")
	delete(config, "token")
	if hasToken && token != "" {
		config["token_set"] = true
	} else if tokenSet, ok := config["token_set"].(bool); ok && tokenSet {
		config["token_set"] = true
	} else {
		delete(config, "token_set")
	}
	target.ConfigJSON = mustMarshalSyncTargetConfig(config)
	return target
}

func sanitizeSyncTargetPtr(target *model.SyncTarget) *model.SyncTarget {
	if target == nil {
		return nil
	}
	sanitized := sanitizeSyncTarget(*target)
	return &sanitized
}

func syncTargetConfigObject(raw string) (map[string]any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "{}"
	}
	var config map[string]any
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		return nil, errors.New("sync target config_json must be a JSON object")
	}
	if config == nil {
		return nil, errors.New("sync target config_json must be a JSON object")
	}
	return config, nil
}

func normalizeNotionSecretConfig(config map[string]any) {
	token, hasToken := trimmedConfigString(config, "token")
	if hasToken && token != "" {
		config["token"] = token
	} else {
		delete(config, "token")
	}
	tokenEnv, hasTokenEnv := trimmedConfigString(config, "token_env")
	if hasTokenEnv && tokenEnv != "" {
		config["token_env"] = tokenEnv
	} else {
		delete(config, "token_env")
	}
	delete(config, "token_set")
}

func trimmedConfigString(config map[string]any, key string) (string, bool) {
	value, ok := config[key]
	if !ok || value == nil {
		return "", false
	}
	text, ok := value.(string)
	if !ok {
		return "", false
	}
	return strings.TrimSpace(text), true
}

func mustMarshalSyncTargetConfig(config map[string]any) string {
	raw, err := json.Marshal(config)
	if err != nil {
		return "{}"
	}
	return string(raw)
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
		if err := mergeSyncTargetSecretConfig(existing, target); err != nil {
			return err
		}
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
