package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/mobilesync"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

const (
	mobileReadPageSize        = 100
	mobilePublisherBatchSize  = 1000
	mobilePublisherMaxBatches = 10
	mobileSnapshotLifetime    = 15 * time.Minute
)

type mobileEntityWire struct {
	EntityType string          `json:"entity_type"`
	EntityID   string          `json:"entity_id"`
	Revision   int64           `json:"revision"`
	DeletedAt  *string         `json:"deleted_at,omitempty"`
	Payload    json.RawMessage `json:"payload"`
}

type mobileChangePageWire struct {
	SchemaVersion             string             `json:"schema_version"`
	Changes                   []mobileEntityWire `json:"changes"`
	NextCursor                string             `json:"next_cursor"`
	HasMore                   bool               `json:"has_more"`
	TimeZone                  string             `json:"time_zone,omitempty"`
	ScopeValidUntil           string             `json:"scope_valid_until,omitempty"`
	ProjectionRefreshRequired bool               `json:"projection_refresh_required,omitempty"`
}

type mobileSnapshotPageWire struct {
	SchemaVersion             string             `json:"schema_version"`
	Entities                  []mobileEntityWire `json:"entities"`
	NextPageToken             string             `json:"next_page_token,omitempty"`
	SnapshotCursor            string             `json:"snapshot_cursor"`
	HasMore                   bool               `json:"has_more"`
	ScopeValidUntil           string             `json:"scope_valid_until"`
	TimeZone                  string             `json:"time_zone,omitempty"`
	ProjectionRefreshRequired bool               `json:"projection_refresh_required,omitempty"`
}

func ListMobileChanges(store storage.Store, tokenSecret string) gin.HandlerFunc {
	codec := mobilesync.NewTokenCodec(tokenSecret)
	return func(c *gin.Context) {
		scope, workspaceID, ok := mobileReadScope(c)
		if !ok {
			return
		}
		syncRepo, err := storage.MobileSyncRepositoryFrom(store)
		if err != nil {
			mobileError(c, http.StatusInternalServerError, "mobile_sync_unavailable", "mobile sync is unavailable", true)
			return
		}
		now := time.Now().UTC()
		projectionTimeZone, err := mobileProjectionTimeZone(c, scope)
		if err != nil {
			mobileError(c, http.StatusBadRequest, "invalid_time_zone", "time_zone must be an IANA time zone", false)
			return
		}
		if err := publishMobileChanges(c, syncRepo, now.Unix()); err != nil {
			mobileError(c, http.StatusInternalServerError, "mobile_sync_unavailable", "mobile sync is unavailable", true)
			return
		}
		position := int64(0)
		if token := strings.TrimSpace(c.Query("cursor")); token != "" {
			cursor, err := codec.DecodeCursorBound(token, workspaceID, scope, projectionTimeZone)
			if err != nil {
				mobileResyncError(c, http.StatusConflict, "The cursor is invalid or belongs to another sync scope.")
				return
			}
			position = cursor.Position
		}
		page, err := syncRepo.ReadCommittedChanges(c.Request.Context(), position, mobileReadPageSize)
		if errors.Is(err, storage.ErrMobileCursorExpired) {
			mobileResyncError(c, http.StatusConflict, "The cursor is no longer retained.")
			return
		}
		if err != nil {
			mobileError(c, http.StatusInternalServerError, "mobile_sync_unavailable", "mobile sync is unavailable", true)
			return
		}
		nextCursor, err := codec.EncodeCursor(mobilesync.SyncCursor{
			WorkspaceID: workspaceID, Scope: scope, Position: page.NextPosition, TimeZone: projectionTimeZone,
		})
		if err != nil {
			mobileError(c, http.StatusInternalServerError, "mobile_sync_unavailable", "mobile sync is unavailable", true)
			return
		}
		changes := make([]mobileEntityWire, 0, len(page.Changes))
		projectionRefreshRequired := scope == "watch" && len(page.Changes) > 0
		for _, change := range page.Changes {
			if scope != "watch" && mobileEntityAllowedForScope(scope, change.Entity.EntityType) {
				changes = append(changes, mobileEntityToWire(change.Entity))
			}
		}
		scopeValidUntil := ""
		if scope == "watch" {
			value, err := nextLocalMidnight(now, projectionTimeZone)
			if err != nil {
				mobileError(c, http.StatusBadRequest, "invalid_time_zone", "time_zone must be an IANA time zone", false)
				return
			}
			scopeValidUntil = value.Format(time.RFC3339)
		}
		c.JSON(http.StatusOK, mobileChangePageWire{
			SchemaVersion: "mobile-v1", Changes: changes, NextCursor: nextCursor, HasMore: page.HasMore,
			TimeZone: projectionTimeZone, ScopeValidUntil: scopeValidUntil, ProjectionRefreshRequired: projectionRefreshRequired,
		})
	}
}

func mobileEntityAllowedForScope(scope, entityType string) bool {
	if scope == "iphone" {
		return true
	}
	switch entityType {
	case "task", "task_occurrence", "event", "voice_note", "transcription_job":
		return true
	default:
		return false
	}
}

func GetMobileSnapshot(store storage.Store, tokenSecret string) gin.HandlerFunc {
	codec := mobilesync.NewTokenCodec(tokenSecret)
	return func(c *gin.Context) {
		scope, workspaceID, ok := mobileReadScope(c)
		if !ok {
			return
		}
		syncRepo, err := storage.MobileSyncRepositoryFrom(store)
		if err != nil {
			mobileError(c, http.StatusInternalServerError, "mobile_sync_unavailable", "mobile sync is unavailable", true)
			return
		}
		now := time.Now().UTC()
		projectionTimeZone, err := mobileProjectionTimeZone(c, scope)
		if err != nil {
			mobileError(c, http.StatusBadRequest, "invalid_time_zone", "time_zone must be an IANA time zone", false)
			return
		}
		var sessionID string
		var offset int64
		var expiresAt int64
		if token := strings.TrimSpace(c.Query("page_token")); token != "" {
			pageToken, err := codec.DecodeSnapshotPageBound(token, workspaceID, scope, projectionTimeZone)
			if err != nil || now.Unix() > pageToken.ExpiresAt {
				mobileResyncError(c, http.StatusGone, "The snapshot session expired or is invalid.")
				return
			}
			sessionID, offset, expiresAt = pageToken.SessionID, pageToken.Offset, pageToken.ExpiresAt
		} else {
			if err := publishMobileChanges(c, syncRepo, now.Unix()); err != nil {
				mobileError(c, http.StatusInternalServerError, "mobile_sync_unavailable", "mobile sync is unavailable", true)
				return
			}
			snapshot, err := syncRepo.BeginSnapshot(c.Request.Context(), model.BeginMobileSnapshot{
				SessionID: uuid.NewString(), Scope: scope, TimeZone: projectionTimeZone,
				Now: now.Unix(), ExpiresAt: now.Add(mobileSnapshotLifetime).Unix(),
			})
			if err != nil {
				mobileError(c, http.StatusInternalServerError, "mobile_sync_unavailable", "mobile sync is unavailable", true)
				return
			}
			sessionID, expiresAt = snapshot.SessionID, snapshot.ExpiresAt
		}
		page, err := syncRepo.ReadSnapshot(c.Request.Context(), model.ReadMobileSnapshot{
			SessionID: sessionID, Offset: offset, Limit: mobileReadPageSize, Now: now.Unix(),
		})
		if errors.Is(err, storage.ErrMobileSnapshotExpired) {
			mobileResyncError(c, http.StatusGone, "The snapshot session expired or is invalid.")
			return
		}
		if err != nil {
			mobileError(c, http.StatusInternalServerError, "mobile_sync_unavailable", "mobile sync is unavailable", true)
			return
		}
		nextPageToken := ""
		if page.HasMore {
			nextPageToken, err = codec.EncodeSnapshotPage(mobilesync.SnapshotPageToken{
				WorkspaceID: workspaceID, Scope: scope, SessionID: sessionID, Offset: page.NextOffset,
				ExpiresAt: expiresAt, TimeZone: projectionTimeZone,
			})
			if err != nil {
				mobileError(c, http.StatusInternalServerError, "mobile_sync_unavailable", "mobile sync is unavailable", true)
				return
			}
		}
		snapshotCursor, err := codec.EncodeCursor(mobilesync.SyncCursor{
			WorkspaceID: workspaceID, Scope: scope, Position: page.BoundaryPosition, TimeZone: projectionTimeZone,
		})
		if err != nil {
			mobileError(c, http.StatusInternalServerError, "mobile_sync_unavailable", "mobile sync is unavailable", true)
			return
		}
		entities := make([]mobileEntityWire, 0, len(page.Entities))
		for _, entity := range page.Entities {
			entities = append(entities, mobileEntityToWire(entity))
		}
		c.JSON(http.StatusOK, mobileSnapshotPageWire{
			SchemaVersion: "mobile-v1", Entities: entities, NextPageToken: nextPageToken,
			SnapshotCursor: snapshotCursor, HasMore: page.HasMore,
			ScopeValidUntil: time.Unix(page.ScopeValidUntil, 0).In(mustLoadLocation(projectionTimeZone)).Format(time.RFC3339),
			TimeZone:        projectionTimeZone,
		})
	}
}

func mobileProjectionTimeZone(c *gin.Context, scope string) (string, error) {
	if scope != "watch" {
		return "UTC", nil
	}
	value := strings.TrimSpace(c.Query("time_zone"))
	if value == "" {
		value = "UTC"
	}
	_, err := time.LoadLocation(value)
	return value, err
}

func nextLocalMidnight(now time.Time, timeZone string) (time.Time, error) {
	location, err := time.LoadLocation(timeZone)
	if err != nil {
		return time.Time{}, err
	}
	local := now.In(location)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location).AddDate(0, 0, 1), nil
}

func mustLoadLocation(timeZone string) *time.Location {
	location, err := time.LoadLocation(timeZone)
	if err != nil {
		return time.UTC
	}
	return location
}

func mobileReadScope(c *gin.Context) (string, string, bool) {
	scope := strings.TrimSpace(c.Query("scope"))
	if scope != "iphone" && scope != "watch" {
		mobileError(c, http.StatusBadRequest, "invalid_scope", "scope must be iphone or watch", false)
		return "", "", false
	}
	identity, ok := auth.IdentityFromContext(c.Request.Context())
	if !ok || identity.WorkspaceID == "" {
		mobileError(c, http.StatusUnauthorized, "unauthenticated", "authentication is required", false)
		return "", "", false
	}
	if identity.SessionID == "" && scope != "watch" {
		mobileError(c, http.StatusForbidden, "scope_forbidden", "watch credentials can only read the watch scope", false)
		return "", "", false
	}
	return scope, identity.WorkspaceID, true
}

func publishMobileChanges(c *gin.Context, syncRepo storage.MobileSyncRepository, now int64) error {
	for range mobilePublisherMaxBatches {
		count, err := syncRepo.PublishPendingChanges(c.Request.Context(), mobilePublisherBatchSize, now)
		if err != nil {
			return err
		}
		if count < mobilePublisherBatchSize {
			return nil
		}
	}
	return nil
}

func mobileEntityToWire(entity model.MobileEntityEnvelope) mobileEntityWire {
	wire := mobileEntityWire{
		EntityType: entity.EntityType, EntityID: entity.ClientID, Revision: entity.Revision, Payload: entity.Payload,
	}
	if entity.DeletedAt != nil {
		deletedAt := time.Unix(*entity.DeletedAt, 0).UTC().Format(time.RFC3339)
		wire.DeletedAt = &deletedAt
	}
	return wire
}

func mobileResyncError(c *gin.Context, status int, message string) {
	c.JSON(status, gin.H{
		"schema_version":  "mobile-v1",
		"type":            "error",
		"code":            "resync_required",
		"message":         message,
		"retryable":       false,
		"resync_required": true,
	})
}
