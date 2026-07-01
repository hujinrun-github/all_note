package middleware

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	authpkg "github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/storage/sqlite"
	_ "modernc.org/sqlite"
)

const (
	middlewareTestSecret      = "middleware-session-secret-with-at-least-32-bytes"
	middlewareTestCookieName  = "fs_session"
	middlewareTestUserID      = "middleware_user"
	middlewareTestWorkspaceID = "middleware_workspace"
)

func TestRequiredRestoresIdentityWithSessionIDAndWorkspaceScope(t *testing.T) {
	env := setupMiddlewareAuthEnv(t)
	token := "required-token"
	sessionID := "required-session"
	createMiddlewareSession(t, env, middlewareTestUserID, sessionID, token, time.Now().UTC().Add(time.Hour), false)
	router := gin.New()
	router.GET("/required", env.middleware.Required(), func(c *gin.Context) {
		identity, ok := authpkg.IdentityFromContext(c.Request.Context())
		if !ok {
			t.Fatal("identity was not restored")
		}
		workspaceID, err := authpkg.WorkspaceIDFromContext(c.Request.Context())
		if err != nil {
			t.Fatalf("workspace scope missing: %v", err)
		}
		c.JSON(http.StatusOK, gin.H{
			"user_id":      identity.UserID,
			"session_id":   identity.SessionID,
			"workspace_id": workspaceID,
		})
	})
	req := httptest.NewRequest(http.MethodGet, "/required", nil)
	req.AddCookie(middlewareCookie(token))
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["user_id"] != middlewareTestUserID || body["session_id"] != sessionID || body["workspace_id"] != middlewareTestWorkspaceID {
		t.Fatalf("unexpected restored identity: %+v", body)
	}
}

func TestRequiredRejectsMissingInvalidExpiredAndRevokedSessions(t *testing.T) {
	for _, tc := range []struct {
		name       string
		withCookie bool
		token      string
		expires    time.Time
		revoked    bool
	}{
		{name: "missing"},
		{name: "invalid", withCookie: true, token: "missing-token"},
		{name: "expired", withCookie: true, token: "expired-token", expires: time.Now().UTC().Add(-time.Minute)},
		{name: "revoked", withCookie: true, token: "revoked-token", expires: time.Now().UTC().Add(time.Hour), revoked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := setupMiddlewareAuthEnv(t)
			if !tc.expires.IsZero() {
				createMiddlewareSession(t, env, middlewareTestUserID, "session_"+tc.name, tc.token, tc.expires, tc.revoked)
			}
			router := gin.New()
			router.GET("/required", env.middleware.Required(), func(c *gin.Context) {
				c.Status(http.StatusOK)
			})
			req := httptest.NewRequest(http.MethodGet, "/required", nil)
			if tc.withCookie {
				req.AddCookie(middlewareCookie(tc.token))
			}
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401; body = %s", w.Code, w.Body.String())
			}
			assertMiddlewareErrorCode(t, w.Body.String(), "UNAUTHENTICATED")
		})
	}
}

func TestOptionalAuthNeverFailsAndRestoresValidIdentity(t *testing.T) {
	for _, tc := range []struct {
		name         string
		token        string
		create       bool
		wantIdentity bool
	}{
		{name: "missing"},
		{name: "invalid", token: "invalid-token"},
		{name: "valid", token: "optional-token", create: true, wantIdentity: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := setupMiddlewareAuthEnv(t)
			if tc.create {
				createMiddlewareSession(t, env, middlewareTestUserID, "session_optional", tc.token, time.Now().UTC().Add(time.Hour), false)
			}
			router := gin.New()
			router.GET("/optional", env.middleware.Optional(), func(c *gin.Context) {
				identity, ok := authpkg.IdentityFromContext(c.Request.Context())
				c.JSON(http.StatusOK, gin.H{
					"has_identity": ok,
					"user_id":      identity.UserID,
					"session_id":   identity.SessionID,
				})
			})
			req := httptest.NewRequest(http.MethodGet, "/optional", nil)
			if tc.token != "" {
				req.AddCookie(middlewareCookie(tc.token))
			}
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
			}
			var body struct {
				HasIdentity bool   `json:"has_identity"`
				UserID      string `json:"user_id"`
				SessionID   string `json:"session_id"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if body.HasIdentity != tc.wantIdentity {
				t.Fatalf("has_identity = %v, want %v; body = %+v", body.HasIdentity, tc.wantIdentity, body)
			}
			if tc.wantIdentity && (body.UserID != middlewareTestUserID || body.SessionID == "") {
				t.Fatalf("identity not restored: %+v", body)
			}
		})
	}
}

func TestWorkspaceMembershipRemovalRevokesSessionAndReturnsUnauthorized(t *testing.T) {
	env := setupMiddlewareAuthEnv(t)
	token := "removed-membership-token"
	createMiddlewareSession(t, env, middlewareTestUserID, "session_removed_membership", token, time.Now().UTC().Add(time.Hour), false)
	execMiddlewareSQL(t, env.dbPath, `DELETE FROM workspace_members WHERE workspace_id = ? AND user_id = ?`, middlewareTestWorkspaceID, middlewareTestUserID)
	router := gin.New()
	router.GET("/required", env.middleware.Required(), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/required", nil)
	req.AddCookie(middlewareCookie(token))
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body = %s", w.Code, w.Body.String())
	}
	assertMiddlewareErrorCode(t, w.Body.String(), "WORKSPACE_ACCESS_REVOKED")
	if !middlewareSessionRevoked(t, env, token) {
		t.Fatal("session should be revoked when workspace membership is removed")
	}
	clearedCookie := requireMiddlewareResponseCookie(t, w.Result(), middlewareTestCookieName)
	if clearedCookie.MaxAge != -1 {
		t.Fatalf("cleared cookie MaxAge = %d, want -1: %#v", clearedCookie.MaxAge, clearedCookie)
	}
	if clearedCookie.Path != "/" {
		t.Fatalf("cleared cookie Path = %q, want /: %#v", clearedCookie.Path, clearedCookie)
	}
}

func TestAuthRequireAdminAllowsOnlyAdminUsers(t *testing.T) {
	for _, tc := range []struct {
		name string
		role string
		want int
	}{
		{name: "admin", role: "admin", want: http.StatusOK},
		{name: "user", role: "user", want: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := setupMiddlewareAuthEnv(t, withMiddlewareRole(tc.role))
			token := tc.name + "-admin-token"
			createMiddlewareSession(t, env, middlewareTestUserID, "session_"+tc.name, token, time.Now().UTC().Add(time.Hour), false)
			router := gin.New()
			router.GET("/admin", env.middleware.Required(), env.middleware.RequireAdmin(), func(c *gin.Context) {
				c.Status(http.StatusOK)
			})
			req := httptest.NewRequest(http.MethodGet, "/admin", nil)
			req.AddCookie(middlewareCookie(token))
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d; body = %s", w.Code, tc.want, w.Body.String())
			}
		})
	}
}

func TestRequirePasswordSettledRejectsMustChangePassword(t *testing.T) {
	env := setupMiddlewareAuthEnv(t, withMiddlewareMustChangePassword(true))
	token := "password-settled-token"
	createMiddlewareSession(t, env, middlewareTestUserID, "session_password_settled", token, time.Now().UTC().Add(time.Hour), false)
	router := gin.New()
	router.GET("/protected", env.middleware.Required(), env.middleware.RequirePasswordSettled(), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.AddCookie(middlewareCookie(token))
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body = %s", w.Code, w.Body.String())
	}
	assertMiddlewareErrorCode(t, w.Body.String(), "PASSWORD_CHANGE_REQUIRED")
}

type middlewareAuthEnv struct {
	store      storage.Store
	dbPath     string
	middleware AuthMiddleware
}

type middlewareUserOption func(*model.User)

func withMiddlewareRole(role string) middlewareUserOption {
	return func(user *model.User) {
		user.Role = role
	}
}

func withMiddlewareMustChangePassword(value bool) middlewareUserOption {
	return func(user *model.User) {
		user.MustChangePassword = value
	}
}

func setupMiddlewareAuthEnv(t *testing.T, opts ...middlewareUserOption) *middlewareAuthEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dbPath := filepath.Join(t.TempDir(), "auth-middleware.db")
	store, err := sqlite.Provider{}.Open(t.Context(), storage.Config{
		Env:        "test",
		Driver:     storage.DriverSQLite,
		Name:       "flowspace_test",
		SQLitePath: dbPath,
	})
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("close sqlite store: %v", err)
		}
	})
	seedMiddlewareUser(t, store, opts...)
	return &middlewareAuthEnv{
		store:  store,
		dbPath: dbPath,
		middleware: AuthMiddleware{
			Store:         store,
			SessionSecret: middlewareTestSecret,
		},
	}
}

func seedMiddlewareUser(t *testing.T, store storage.Store, opts ...middlewareUserOption) {
	t.Helper()
	passwordHash, err := authpkg.HashPassword("abc12345")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	user := &model.User{
		ID:                 middlewareTestUserID,
		Email:              "middleware@example.com",
		DisplayName:        "Middleware User",
		PasswordHash:       passwordHash,
		DefaultWorkspaceID: middlewareTestWorkspaceID,
		Role:               "admin",
		Status:             "active",
	}
	for _, opt := range opts {
		opt(user)
	}
	workspace := &model.Workspace{
		ID:          middlewareTestWorkspaceID,
		Name:        "Middleware Workspace",
		OwnerUserID: user.ID,
	}
	err = store.Transact(t.Context(), func(tx storage.Store) error {
		if err := tx.Auth().CreateUser(t.Context(), user); err != nil {
			return err
		}
		if err := tx.Auth().CreateWorkspace(t.Context(), workspace); err != nil {
			return err
		}
		return tx.Auth().AddWorkspaceMember(t.Context(), workspace.ID, user.ID, "owner")
	})
	if err != nil {
		t.Fatalf("seed middleware auth user: %v", err)
	}
}

func createMiddlewareSession(t *testing.T, env *middlewareAuthEnv, userID, sessionID, token string, expiresAt time.Time, revoked bool) {
	t.Helper()
	session := &model.Session{
		ID:          sessionID,
		UserID:      userID,
		WorkspaceID: middlewareTestWorkspaceID,
		TokenHash:   hashMiddlewareToken(t, token),
		UserAgent:   "middleware-test",
		IPAddress:   "127.0.0.1",
		ExpiresAt:   expiresAt,
	}
	if err := env.store.Auth().CreateSession(t.Context(), session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if revoked {
		if err := env.store.Auth().RevokeSession(t.Context(), session.ID); err != nil {
			t.Fatalf("revoke session: %v", err)
		}
	}
}

func middlewareCookie(token string) *http.Cookie {
	return &http.Cookie{Name: middlewareTestCookieName, Value: token}
}

func requireMiddlewareResponseCookie(t *testing.T, response *http.Response, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range response.Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("response cookie %q missing from cookies: %#v", name, response.Cookies())
	return nil
}

func hashMiddlewareToken(t *testing.T, token string) string {
	t.Helper()
	hash, err := authpkg.HashSessionToken(middlewareTestSecret, token)
	if err != nil {
		t.Fatalf("hash token: %v", err)
	}
	return hash
}

func middlewareSessionRevoked(t *testing.T, env *middlewareAuthEnv, token string) bool {
	t.Helper()
	_, err := env.store.Auth().GetSessionByTokenHash(t.Context(), hashMiddlewareToken(t, token))
	return errors.Is(err, sql.ErrNoRows)
}

func execMiddlewareSQL(t *testing.T, dbPath, statement string, args ...any) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath+"?_foreign_keys=ON")
	if err != nil {
		t.Fatalf("open sqlite side connection: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(statement, args...); err != nil {
		t.Fatalf("exec sql: %v", err)
	}
}

func assertMiddlewareErrorCode(t *testing.T, body string, want string) {
	t.Helper()
	var response model.APIResponse
	if err := json.Unmarshal([]byte(body), &response); err != nil {
		t.Fatalf("decode response: %v; body = %s", err, body)
	}
	if response.Error == nil {
		t.Fatalf("missing error in body: %s", body)
	}
	if response.Error.Code != want {
		t.Fatalf("error code = %q, want %q; body = %s", response.Error.Code, want, body)
	}
}
