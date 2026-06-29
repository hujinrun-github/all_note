package middleware

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

const sessionCookieName = "fs_session"

type AuthMiddleware struct {
	Store         storage.Store
	SessionSecret string
}

func (m AuthMiddleware) Required() gin.HandlerFunc {
	return func(c *gin.Context) {
		if ok := m.restore(c, true); !ok {
			return
		}
		c.Next()
	}
}

func (m AuthMiddleware) Optional() gin.HandlerFunc {
	return func(c *gin.Context) {
		_ = m.restore(c, false)
		c.Next()
	}
}

func (m AuthMiddleware) RequireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := auth.IdentityFromContext(c.Request.Context())
		if !ok || identity.Role != "admin" {
			abortAuth(c, http.StatusForbidden, "FORBIDDEN", "admin access required")
			return
		}
		c.Next()
	}
}

func (m AuthMiddleware) RequirePasswordSettled() gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := auth.IdentityFromContext(c.Request.Context())
		if ok && identity.MustChangePassword {
			abortAuth(c, http.StatusForbidden, "PASSWORD_CHANGE_REQUIRED", "password change required")
			return
		}
		c.Next()
	}
}

func (m AuthMiddleware) restore(c *gin.Context, required bool) bool {
	if m.Store == nil {
		if required {
			abortAuth(c, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
		}
		return false
	}
	cookie, err := c.Cookie(sessionCookieName)
	if err != nil || cookie == "" {
		if required {
			abortAuth(c, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
		}
		return false
	}
	tokenHash, err := auth.HashSessionToken(m.SessionSecret, cookie)
	if err != nil {
		if required {
			abortAuth(c, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
		}
		return false
	}
	session, err := m.Store.Auth().GetSessionByTokenHash(c.Request.Context(), tokenHash)
	if err != nil {
		if required {
			abortAuth(c, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
		}
		return false
	}
	user, err := m.Store.Auth().GetUserByID(c.Request.Context(), session.UserID)
	if err != nil || user.Status != "active" {
		if required {
			abortAuth(c, http.StatusForbidden, "ACCOUNT_DISABLED", "account disabled")
		}
		return false
	}
	if _, err := m.Store.Auth().GetWorkspaceMembership(c.Request.Context(), session.WorkspaceID, session.UserID); err != nil {
		_ = m.Store.Auth().RevokeSession(c.Request.Context(), session.ID)
		clearSessionCookie(c)
		if required {
			abortAuth(c, http.StatusUnauthorized, "WORKSPACE_ACCESS_REVOKED", "workspace access revoked")
		}
		return false
	}

	identity := auth.RequestIdentity{
		UserID:             user.ID,
		SessionID:          session.ID,
		WorkspaceID:        session.WorkspaceID,
		Role:               user.Role,
		MustChangePassword: user.MustChangePassword,
	}
	ctx := auth.ContextWithIdentity(c.Request.Context(), identity)
	ctx = auth.ContextWithWorkspaceScope(ctx, session.WorkspaceID)
	c.Request = c.Request.WithContext(ctx)
	return true
}

func abortAuth(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, model.APIResponse{
		Error: &model.APIError{
			Code:    code,
			Message: message,
		},
	})
}

func clearSessionCookie(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0).UTC(),
	})
}
