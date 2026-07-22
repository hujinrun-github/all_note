package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/auth"
)

var ErrSettingsRevisionConflict = errors.New("settings revision conflict")
var ErrDatabaseMigrationRequired = errors.New("database binding change requires migration")

type ServiceBindingDTO struct {
	Kind             string `json:"kind"`
	Mode             string `json:"mode"`
	EndpointID       string `json:"endpoint_id,omitempty"`
	EndpointName     string `json:"endpoint_name,omitempty"`
	Provider         string `json:"provider,omitempty"`
	ProfileVersionID string `json:"profile_version_id,omitempty"`
	HasCredentials   bool   `json:"has_credentials"`
	Revision         int64  `json:"revision"`
}

type RuntimeSettingsDTO struct {
	WorkspaceID     string              `json:"workspace_id"`
	Mode            string              `json:"mode"`
	Epoch           int64               `json:"epoch"`
	BindingRevision int64               `json:"binding_revision"`
	Bindings        []ServiceBindingDTO `json:"bindings"`
}

type TestServiceProfileRequest struct {
	Kind      string         `json:"kind" binding:"required"`
	Provider  string         `json:"provider" binding:"required"`
	Config    map[string]any `json:"config" binding:"required"`
	Secret    string         `json:"secret"`
	VersionID string         `json:"version_id"`
}

type TestServiceProfileResult struct {
	OK           bool   `json:"ok"`
	Code         string `json:"code"`
	Message      string `json:"message"`
	Installation string `json:"installation_id,omitempty"`
	Schema       string `json:"schema_identity,omitempty"`
}

type SaveServiceProfileRequest struct {
	ID                    string         `json:"id" binding:"required"`
	FamilyID              string         `json:"family_id" binding:"required"`
	Kind                  string         `json:"kind" binding:"required"`
	Name                  string         `json:"name" binding:"required"`
	Provider              string         `json:"provider" binding:"required"`
	Config                map[string]any `json:"config" binding:"required"`
	Secret                string         `json:"secret"`
	PreserveFromVersionID string         `json:"preserve_from_version_id"`
}

type SavedServiceProfileDTO struct {
	ID             string `json:"id"`
	FamilyID       string `json:"family_id"`
	Kind           string `json:"kind"`
	Version        int64  `json:"version"`
	State          string `json:"state"`
	HasCredentials bool   `json:"has_credentials"`
}

type VerifiedServiceProfileDTO struct {
	EndpointID       string `json:"endpoint_id"`
	ProfileVersionID string `json:"profile_version_id"`
	Kind             string `json:"kind"`
}

type SetServiceBindingRequest struct {
	Mode                    string `json:"mode" binding:"required"`
	EndpointID              string `json:"endpoint_id"`
	ExpectedRevision        int64  `json:"expected_revision" binding:"required"`
	ExpectedRuntimeRevision int64  `json:"expected_runtime_revision" binding:"required"`
}

type WorkspaceSettingsService interface {
	GetRuntimeSettings(context.Context, string, string) (RuntimeSettingsDTO, error)
	TestProfile(context.Context, string, string, TestServiceProfileRequest) (TestServiceProfileResult, error)
	SaveProfile(context.Context, string, string, SaveServiceProfileRequest) (SavedServiceProfileDTO, error)
	VerifyProfile(context.Context, string, string, string, string) (VerifiedServiceProfileDTO, error)
	SetBinding(context.Context, string, string, string, SetServiceBindingRequest) (ServiceBindingDTO, error)
}

func VerifyServiceProfile(service WorkspaceSettingsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := settingsIdentity(c, service)
		if !ok {
			return
		}
		kind, versionID := c.Param("kind"), c.Param("versionID")
		if !validServiceKind(kind) || strings.TrimSpace(versionID) == "" {
			badRequest(c, "invalid service profile version")
			return
		}
		verified, err := service.VerifyProfile(c.Request.Context(), identity.UserID, identity.WorkspaceID, kind, versionID)
		if err != nil {
			errorResponse(c, http.StatusBadGateway, "PROFILE_VERIFY_FAILED", "service profile verification failed")
			return
		}
		success(c, verified)
	}
}

func GetRuntimeSettings(service WorkspaceSettingsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := settingsIdentity(c, service)
		if !ok {
			return
		}
		settings, err := service.GetRuntimeSettings(c.Request.Context(), identity.UserID, identity.WorkspaceID)
		if err != nil {
			internalError(c, "unable to load workspace settings")
			return
		}
		success(c, settings)
	}
}

func TestServiceProfile(service WorkspaceSettingsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := settingsIdentity(c, service)
		if !ok {
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
		var request TestServiceProfileRequest
		if err := c.ShouldBindJSON(&request); err != nil || !validServiceKind(request.Kind) {
			badRequest(c, "invalid service profile test request")
			return
		}
		result, err := service.TestProfile(c.Request.Context(), identity.UserID, identity.WorkspaceID, request)
		if err != nil {
			errorResponse(c, http.StatusBadGateway, "PROFILE_TEST_FAILED", "service connection test failed")
			return
		}
		success(c, result)
	}
}

func SaveServiceProfile(service WorkspaceSettingsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := settingsIdentity(c, service)
		if !ok {
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
		var request SaveServiceProfileRequest
		if err := c.ShouldBindJSON(&request); err != nil || !validServiceKind(request.Kind) || strings.TrimSpace(request.Name) == "" {
			badRequest(c, "invalid service profile")
			return
		}
		saved, err := service.SaveProfile(c.Request.Context(), identity.UserID, identity.WorkspaceID, request)
		if err != nil {
			internalError(c, "unable to save service profile")
			return
		}
		created(c, saved)
	}
}

func SetServiceBinding(service WorkspaceSettingsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := settingsIdentity(c, service)
		if !ok {
			return
		}
		kind := c.Param("kind")
		var request SetServiceBindingRequest
		if !validServiceKind(kind) || c.ShouldBindJSON(&request) != nil {
			badRequest(c, "invalid service binding")
			return
		}
		binding, err := service.SetBinding(c.Request.Context(), identity.UserID, identity.WorkspaceID, kind, request)
		if errors.Is(err, ErrDatabaseMigrationRequired) {
			conflict(c, "DATABASE_MIGRATION_REQUIRED", "database changes must use a storage migration")
			return
		}
		if errors.Is(err, ErrSettingsRevisionConflict) {
			conflict(c, "SETTINGS_REVISION_CONFLICT", "settings changed; reload and try again")
			return
		}
		if err != nil {
			internalError(c, "unable to update service binding")
			return
		}
		success(c, binding)
	}
}

func settingsIdentity(c *gin.Context, service WorkspaceSettingsService) (auth.RequestIdentity, bool) {
	identity, ok := auth.IdentityFromContext(c.Request.Context())
	if !ok {
		errorResponse(c, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
		return auth.RequestIdentity{}, false
	}
	if service == nil {
		errorResponse(c, http.StatusServiceUnavailable, "SETTINGS_UNAVAILABLE", "workspace settings are unavailable")
		return auth.RequestIdentity{}, false
	}
	return identity, true
}

func validServiceKind(kind string) bool {
	return kind == "data_store" || kind == "object_s3" || kind == "llm_chat" || kind == "llm_transcription"
}
