package handler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/config"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

const defaultSessionCookieName = "fs_session"

type authWorkspaceQueryer interface {
	QueryRowContext(context.Context, string, ...interface{}) *sql.Row
}

func Login(store storage.Store, authCfg config.AuthConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req model.LoginRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			badRequest(c, "email and password are required")
			return
		}
		if store == nil {
			errorResponse(c, http.StatusInternalServerError, "LOGIN_FAILED", "login failed")
			return
		}

		ctx := c.Request.Context()
		user, err := store.Auth().GetUserByEmail(ctx, strings.TrimSpace(req.Email))
		if err != nil {
			errorResponse(c, http.StatusUnauthorized, "INVALID_CREDENTIALS", "invalid email or password")
			return
		}
		if err := auth.VerifyPassword(user.PasswordHash, req.Password); err != nil {
			errorResponse(c, http.StatusUnauthorized, "INVALID_CREDENTIALS", "invalid email or password")
			return
		}
		if user.Status != "active" {
			errorResponse(c, http.StatusForbidden, "ACCOUNT_DISABLED", "account disabled")
			return
		}

		workspaceID := user.DefaultWorkspaceID
		if strings.TrimSpace(workspaceID) == "" {
			errorResponse(c, http.StatusUnauthorized, "WORKSPACE_ACCESS_REVOKED", "workspace access revoked")
			return
		}
		if _, err := store.Auth().GetWorkspaceMembership(ctx, workspaceID, user.ID); err != nil {
			errorResponse(c, http.StatusUnauthorized, "WORKSPACE_ACCESS_REVOKED", "workspace access revoked")
			return
		}

		token, err := auth.GenerateSessionToken()
		if err != nil {
			errorResponse(c, http.StatusInternalServerError, "LOGIN_FAILED", "login failed")
			return
		}
		tokenHash, err := auth.HashSessionToken(authCfg.SessionSecret, token)
		if err != nil {
			errorResponse(c, http.StatusInternalServerError, "LOGIN_FAILED", "login failed")
			return
		}
		ttl := sessionTTL(authCfg, req.RememberMe)
		now := time.Now().UTC()
		session := &model.Session{
			UserID:      user.ID,
			WorkspaceID: workspaceID,
			TokenHash:   tokenHash,
			UserAgent:   c.Request.UserAgent(),
			IPAddress:   c.ClientIP(),
			ExpiresAt:   now.Add(ttl),
			CreatedAt:   now,
			LastSeenAt:  now,
		}
		loginAt := now

		err = store.Transact(ctx, func(tx storage.Store) error {
			if err := tx.Auth().CreateSession(ctx, session); err != nil {
				return err
			}
			if err := tx.Auth().UpdateUserLastLogin(ctx, user.ID, loginAt); err != nil {
				return err
			}
			return tx.Auth().RecordAuditEvent(ctx, &model.AuditEvent{
				ActorUserID:  &user.ID,
				TargetUserID: &user.ID,
				WorkspaceID:  &workspaceID,
				Action:       "auth.login",
				Metadata:     authAuditMetadata(c),
			})
		})
		if err != nil {
			errorResponse(c, http.StatusInternalServerError, "LOGIN_FAILED", "login failed")
			return
		}

		http.SetCookie(c.Writer, activeSessionCookie(authCfg.Cookie, token, ttl, session.ExpiresAt))
		workspace := loadAuthWorkspace(ctx, store, workspaceID)
		success(c, model.LoginResponse{User: *user, Workspace: workspace})
	}
}

func Logout(store storage.Store, cookieCfg config.CookieConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := auth.IdentityFromContext(c.Request.Context())
		if ok && identity.SessionID != "" && store != nil {
			if err := store.Auth().RevokeSession(c.Request.Context(), identity.SessionID); err != nil {
				errorResponse(c, http.StatusInternalServerError, "LOGOUT_FAILED", "logout failed")
				return
			}
		}
		http.SetCookie(c.Writer, expiredSessionCookie(cookieCfg))
		noContent(c)
	}
}

func Me(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := auth.IdentityFromContext(c.Request.Context())
		if !ok {
			errorResponse(c, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
			return
		}
		if store == nil {
			errorResponse(c, http.StatusInternalServerError, "AUTH_FAILED", "authentication failed")
			return
		}
		user, err := store.Auth().GetUserByID(c.Request.Context(), identity.UserID)
		if err != nil {
			errorResponse(c, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
			return
		}
		workspace := loadAuthWorkspace(c.Request.Context(), store, identity.WorkspaceID)
		success(c, model.CurrentUser{
			User:               *user,
			Workspace:          workspace,
			MustChangePassword: user.MustChangePassword,
		})
	}
}

func ChangePassword(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := auth.IdentityFromContext(c.Request.Context())
		if !ok || identity.SessionID == "" {
			errorResponse(c, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
			return
		}
		if store == nil {
			errorResponse(c, http.StatusInternalServerError, "PASSWORD_CHANGE_FAILED", "password change failed")
			return
		}

		var req model.ChangePasswordRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			badRequest(c, "current_password and new_password are required")
			return
		}
		user, err := store.Auth().GetUserByID(c.Request.Context(), identity.UserID)
		if err != nil {
			errorResponse(c, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
			return
		}
		if !user.PasswordSet {
			errorResponse(c, http.StatusBadRequest, "PASSWORD_NOT_SET", "password has not been set for this account")
			return
		}
		if err := auth.VerifyPassword(user.PasswordHash, req.CurrentPassword); err != nil {
			errorResponse(c, http.StatusUnauthorized, "INVALID_CREDENTIALS", "invalid current password")
			return
		}
		newHash, err := auth.HashPassword(req.NewPassword)
		if err != nil {
			if errors.Is(err, auth.ErrWeakPassword) {
				errorResponse(c, http.StatusBadRequest, "WEAK_PASSWORD", "new password does not meet policy")
				return
			}
			errorResponse(c, http.StatusInternalServerError, "PASSWORD_CHANGE_FAILED", "password change failed")
			return
		}

		ctx := c.Request.Context()
		err = store.Transact(ctx, func(tx storage.Store) error {
			if err := tx.Auth().UpdateUserPassword(ctx, identity.UserID, newHash, false); err != nil {
				return err
			}
			if err := tx.Auth().RevokeUserSessionsExcept(ctx, identity.UserID, identity.SessionID); err != nil {
				return err
			}
			return tx.Auth().RecordAuditEvent(ctx, &model.AuditEvent{
				ActorUserID:  &identity.UserID,
				TargetUserID: &identity.UserID,
				WorkspaceID:  &identity.WorkspaceID,
				Action:       "auth.change_password",
				Metadata:     authAuditMetadata(c),
			})
		})
		if err != nil {
			errorResponse(c, http.StatusInternalServerError, "PASSWORD_CHANGE_FAILED", "password change failed")
			return
		}
		noContent(c)
	}
}

func sessionTTL(authCfg config.AuthConfig, remember bool) time.Duration {
	if remember {
		if authCfg.Session.RememberTTL > 0 {
			return authCfg.Session.RememberTTL
		}
		return 30 * 24 * time.Hour
	}
	if authCfg.Session.ShortTTL > 0 {
		return authCfg.Session.ShortTTL
	}
	return 12 * time.Hour
}

func activeSessionCookie(cookieCfg config.CookieConfig, token string, ttl time.Duration, expiresAt time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     authCookieName(cookieCfg),
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   cookieCfg.Secure,
		SameSite: sameSiteMode(cookieCfg.SameSite),
		MaxAge:   int(ttl.Seconds()),
		Expires:  expiresAt,
	}
}

func expiredSessionCookie(cookieCfg config.CookieConfig) *http.Cookie {
	return &http.Cookie{
		Name:     authCookieName(cookieCfg),
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   cookieCfg.Secure,
		SameSite: sameSiteMode(cookieCfg.SameSite),
		MaxAge:   -1,
		Expires:  time.Unix(0, 0).UTC(),
	}
}

func authCookieName(cookieCfg config.CookieConfig) string {
	if strings.TrimSpace(cookieCfg.Name) == "" {
		return defaultSessionCookieName
	}
	return cookieCfg.Name
}

func sameSiteMode(value string) http.SameSite {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "strict":
		return http.SameSiteStrictMode
	case "none":
		return http.SameSiteNoneMode
	default:
		return http.SameSiteLaxMode
	}
}

func authAuditMetadata(c *gin.Context) map[string]any {
	return map[string]any{
		"ip":         c.ClientIP(),
		"user_agent": c.Request.UserAgent(),
	}
}

func loadAuthWorkspace(ctx context.Context, store storage.Store, workspaceID string) model.Workspace {
	workspace := model.Workspace{ID: workspaceID}
	runner, ok := store.(authWorkspaceQueryer)
	if !ok || strings.TrimSpace(workspaceID) == "" {
		return workspace
	}
	placeholder := "?"
	if store.Capabilities().TimeRanges {
		placeholder = "$1"
	}
	query := fmt.Sprintf(`SELECT id, name, owner_user_id FROM workspaces WHERE id = %s`, placeholder)
	if err := runner.QueryRowContext(ctx, query, workspaceID).Scan(&workspace.ID, &workspace.Name, &workspace.OwnerUserID); err != nil {
		return model.Workspace{ID: workspaceID}
	}
	return workspace
}
