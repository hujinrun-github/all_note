package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

const maxUserAvatarBytes = 2 << 20

type profileResponse struct {
	UserID      string `json:"user_id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Locale      string `json:"locale"`
	TimeZone    string `json:"time_zone"`
	AvatarURL   string `json:"avatar_url,omitempty"`
	UpdatedAt   int64  `json:"updated_at"`
}

type updateProfileRequest struct {
	DisplayName string `json:"display_name" binding:"required"`
	Locale      string `json:"locale" binding:"required"`
	TimeZone    string `json:"time_zone" binding:"required"`
}

func GetSettingsProfile(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := auth.IdentityFromContext(c.Request.Context())
		if !ok || store == nil {
			errorResponse(c, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
			return
		}
		response, err := loadProfileResponse(c.Request.Context(), store, identity.UserID)
		if err != nil {
			internalError(c, "unable to load user profile")
			return
		}
		success(c, response)
	}
}

func UpdateSettingsProfile(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := auth.IdentityFromContext(c.Request.Context())
		if !ok || store == nil {
			errorResponse(c, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
			return
		}
		var request updateProfileRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			badRequest(c, "display_name, locale and time_zone are required")
			return
		}
		request.DisplayName = strings.TrimSpace(request.DisplayName)
		if request.DisplayName == "" || len([]rune(request.DisplayName)) > 80 || !validProfileLocale(request.Locale) {
			badRequest(c, "invalid profile values")
			return
		}
		if _, err := time.LoadLocation(request.TimeZone); err != nil {
			badRequest(c, "invalid time zone")
			return
		}
		ctx := c.Request.Context()
		err := store.Transact(ctx, func(tx storage.Store) error {
			name := request.DisplayName
			if _, err := tx.Auth().UpdateUser(ctx, identity.UserID, &model.UpdateUserRequest{DisplayName: &name}); err != nil {
				return err
			}
			if err := tx.Auth().UpsertUserProfile(ctx, &model.UserProfile{UserID: identity.UserID, Locale: request.Locale, TimeZone: request.TimeZone}); err != nil {
				return err
			}
			return tx.Auth().RecordAuditEvent(ctx, &model.AuditEvent{ActorUserID: &identity.UserID, TargetUserID: &identity.UserID, WorkspaceID: &identity.WorkspaceID, Action: "settings.profile.update", Metadata: map[string]any{}})
		})
		if err != nil {
			internalError(c, "unable to update user profile")
			return
		}
		response, err := loadProfileResponse(ctx, store, identity.UserID)
		if err != nil {
			internalError(c, "unable to load updated user profile")
			return
		}
		success(c, response)
	}
}

func PutSettingsAvatar(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := auth.IdentityFromContext(c.Request.Context())
		if !ok || store == nil {
			errorResponse(c, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
			return
		}
		contents, err := io.ReadAll(io.LimitReader(c.Request.Body, maxUserAvatarBytes+1))
		if err != nil || len(contents) == 0 || len(contents) > maxUserAvatarBytes {
			errorResponse(c, http.StatusRequestEntityTooLarge, "AVATAR_TOO_LARGE", "avatar must not exceed 2 MiB")
			return
		}
		mimeType := http.DetectContentType(contents)
		width, height, err := avatarDimensions(contents, mimeType)
		if err != nil || width < 1 || height < 1 || width > 4096 || height > 4096 {
			badRequest(c, "avatar must be a valid JPEG, PNG or WebP image up to 4096x4096")
			return
		}
		sum := sha256.Sum256(contents)
		avatar := &model.UserAvatar{UserID: identity.UserID, MIMEType: mimeType, SizeBytes: int64(len(contents)), SHA256: hex.EncodeToString(sum[:]), Width: width, Height: height, Content: contents}
		if err := store.Auth().UpsertUserAvatar(c.Request.Context(), avatar); err != nil {
			internalError(c, "unable to save avatar")
			return
		}
		success(c, map[string]any{"avatar_url": avatarURL(avatar.UpdatedAt), "sha256": avatar.SHA256, "width": width, "height": height})
	}
}

func GetSettingsAvatar(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := auth.IdentityFromContext(c.Request.Context())
		if !ok || store == nil {
			errorResponse(c, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
			return
		}
		avatar, err := store.Auth().GetUserAvatar(c.Request.Context(), identity.UserID)
		if errors.Is(err, sql.ErrNoRows) {
			notFound(c, "avatar not found")
			return
		}
		if err != nil {
			internalError(c, "unable to load avatar")
			return
		}
		etag := `"` + avatar.SHA256 + `"`
		if c.GetHeader("If-None-Match") == etag {
			c.Status(http.StatusNotModified)
			return
		}
		c.Header("Content-Type", avatar.MIMEType)
		c.Header("Content-Length", strconv.FormatInt(avatar.SizeBytes, 10))
		c.Header("ETag", etag)
		c.Header("Cache-Control", "private, max-age=300")
		c.Data(http.StatusOK, avatar.MIMEType, avatar.Content)
	}
}

func DeleteSettingsAvatar(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := auth.IdentityFromContext(c.Request.Context())
		if !ok || store == nil {
			errorResponse(c, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
			return
		}
		if err := store.Auth().DeleteUserAvatar(c.Request.Context(), identity.UserID); err != nil {
			internalError(c, "unable to delete avatar")
			return
		}
		noContent(c)
	}
}

func loadProfileResponse(ctx context.Context, store storage.Store, userID string) (profileResponse, error) {
	user, err := store.Auth().GetUserByID(ctx, userID)
	if err != nil {
		return profileResponse{}, err
	}
	profile, err := store.Auth().GetUserProfile(ctx, userID)
	if errors.Is(err, sql.ErrNoRows) {
		profile = &model.UserProfile{UserID: userID, Locale: "zh-CN", TimeZone: "Asia/Shanghai", UpdatedAt: user.UpdatedAt}
	} else if err != nil {
		return profileResponse{}, err
	}
	response := profileResponse{UserID: user.ID, Email: user.Email, DisplayName: user.DisplayName, Locale: profile.Locale, TimeZone: profile.TimeZone, UpdatedAt: profile.UpdatedAt}
	if avatar, err := store.Auth().GetUserAvatar(ctx, userID); err == nil {
		response.AvatarURL = avatarURL(avatar.UpdatedAt)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return profileResponse{}, err
	}
	return response, nil
}

func avatarURL(updatedAt int64) string {
	return "/api/settings/profile/avatar?v=" + strconv.FormatInt(updatedAt, 10)
}

func validProfileLocale(locale string) bool {
	return locale == "zh-CN" || locale == "en-US" || locale == "ja-JP"
}

func avatarDimensions(contents []byte, mimeType string) (int, int, error) {
	if mimeType == "image/jpeg" || mimeType == "image/png" {
		config, _, err := image.DecodeConfig(bytes.NewReader(contents))
		return config.Width, config.Height, err
	}
	if mimeType != "image/webp" || len(contents) < 30 || string(contents[:4]) != "RIFF" || string(contents[8:12]) != "WEBP" {
		return 0, 0, errors.New("unsupported image")
	}
	chunk := string(contents[12:16])
	payload := contents[20:]
	switch chunk {
	case "VP8X":
		return 1 + int(payload[4]) + int(payload[5])<<8 + int(payload[6])<<16, 1 + int(payload[7]) + int(payload[8])<<8 + int(payload[9])<<16, nil
	case "VP8L":
		if len(payload) < 5 || payload[0] != 0x2f {
			break
		}
		return 1 + int(payload[1]) + (int(payload[2]&0x3f) << 8), 1 + int(payload[2]>>6) + (int(payload[3]) << 2) + (int(payload[4]&0x0f) << 10), nil
	case "VP8 ":
		if len(payload) < 10 || payload[3] != 0x9d || payload[4] != 0x01 || payload[5] != 0x2a {
			break
		}
		return int(binary.LittleEndian.Uint16(payload[6:8]) & 0x3fff), int(binary.LittleEndian.Uint16(payload[8:10]) & 0x3fff), nil
	}
	return 0, 0, errors.New("invalid WebP")
}
