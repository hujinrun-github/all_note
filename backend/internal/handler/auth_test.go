package handler

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	authpkg "github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/config"
	"github.com/hujinrun/flowspace/internal/middleware"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/repository"
	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/storage/sqlite"
	_ "modernc.org/sqlite"
)

const (
	authTestPassword      = "abc12345"
	authTestSessionSecret = "test-session-secret-with-at-least-32-bytes"
	authTestUserID        = "user_admin"
	authTestWorkspaceID   = "workspace_admin"
)

func TestLoginSetsHttpOnlyCookieAndUsesHMACSessionSecret(t *testing.T) {
	env := setupAuthTestEnv(t)
	body := `{"email":"admin@example.com","password":"abc12345","remember_me":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	env.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	cookie := requireCookie(t, w.Result(), env.cfg.Cookie.Name)
	if !cookie.HttpOnly {
		t.Fatalf("cookie should be HttpOnly: %#v", cookie)
	}
	session := createdSessionForCookie(t, env, cookie)
	if session.UserID != authTestUserID {
		t.Fatalf("session user = %q, want %q", session.UserID, authTestUserID)
	}
	otherSecretHash, err := authpkg.HashSessionToken("other-session-secret-with-at-least-32-bytes", cookie.Value)
	if err != nil {
		t.Fatalf("hash with other secret: %v", err)
	}
	if _, err := env.store.Auth().GetSessionByTokenHash(t.Context(), otherSecretHash); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("session restored with wrong secret hash: %v", err)
	}
	if !lastLoginUpdated(t, env.store, authTestUserID) {
		t.Fatal("last_login_at was not updated")
	}
	if !auditEventRecorded(t, env.dbPath, "auth.login", authTestUserID) {
		t.Fatal("auth.login audit event was not recorded")
	}
}

func TestLoginDisabledUserWithWrongPasswordReturnsInvalidCredentials(t *testing.T) {
	env := setupAuthTestEnv(t, withUserStatus("disabled"))
	body := `{"email":"admin@example.com","password":"wrongpass123","remember_me":false}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	env.router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusUnauthorized, w.Body.String())
	}
	assertErrorCode(t, w.Body.String(), "INVALID_CREDENTIALS")
}

func TestLoginSessionTTLMatchesRememberMe(t *testing.T) {
	for _, tc := range []struct {
		name              string
		remember          bool
		wantCookieMaxAge  int
		wantSessionWindow time.Duration
	}{
		{name: "short", remember: false, wantCookieMaxAge: int((12 * time.Hour).Seconds()), wantSessionWindow: 12 * time.Hour},
		{name: "remember", remember: true, wantCookieMaxAge: int((30 * 24 * time.Hour).Seconds()), wantSessionWindow: 30 * 24 * time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := setupAuthTestEnv(t)
			body := fmt.Sprintf(`{"email":"admin@example.com","password":"abc12345","remember_me":%v}`, tc.remember)
			req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			start := time.Now().UTC()
			env.router.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
			}
			cookie := requireCookie(t, w.Result(), env.cfg.Cookie.Name)
			if cookie.MaxAge != tc.wantCookieMaxAge {
				t.Fatalf("cookie max age = %d, want %d", cookie.MaxAge, tc.wantCookieMaxAge)
			}
			session := createdSessionForCookie(t, env, cookie)
			minExpires := start.Add(tc.wantSessionWindow - time.Minute)
			maxExpires := start.Add(tc.wantSessionWindow + time.Minute)
			if session.ExpiresAt.Before(minExpires) || session.ExpiresAt.After(maxExpires) {
				t.Fatalf("session expires at %s, want between %s and %s", session.ExpiresAt, minExpires, maxExpires)
			}
		})
	}
}

func TestLoginRollsBackSessionAndLastLoginWhenAuditFails(t *testing.T) {
	env := setupAuthTestEnv(t)
	execAuthTestSQL(t, env.dbPath, `
		CREATE TRIGGER fail_auth_login_audit
		BEFORE INSERT ON audit_events
		WHEN NEW.action = 'auth.login'
		BEGIN
			SELECT RAISE(FAIL, 'audit disabled');
		END;
	`)
	body := `{"email":"admin@example.com","password":"abc12345","remember_me":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	env.router.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusInternalServerError, w.Body.String())
	}
	if countAuthTestRows(t, env.dbPath, `SELECT COUNT(*) FROM sessions WHERE user_id = ?`, authTestUserID) != 0 {
		t.Fatal("session was created even though audit failed")
	}
	if lastLoginUpdated(t, env.store, authTestUserID) {
		t.Fatal("last_login_at was updated even though audit failed")
	}
}

func TestMeDoesNotExtendSessionExpiration(t *testing.T) {
	env := setupAuthTestEnv(t)
	expiresAt := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Second)
	token := "me-session-token"
	createAuthTestSession(t, env, authTestUserID, "session_me", token, expiresAt, false)
	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req.AddCookie(authTestCookie(env, token))
	w := httptest.NewRecorder()

	env.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	session, err := env.store.Auth().GetSessionByTokenHash(t.Context(), hashAuthTestToken(t, env, token))
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if !session.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("session expiration changed to %s, want %s", session.ExpiresAt, expiresAt)
	}
}

func TestPasswordChangeRequiredAllowsAuthRoutes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   string
		want   int
	}{
		{name: "me", method: http.MethodGet, path: "/api/auth/me", want: http.StatusOK},
		{name: "change password", method: http.MethodPost, path: "/api/auth/change-password", body: `{"current_password":"abc12345","new_password":"newpass123"}`, want: http.StatusNoContent},
		{name: "logout", method: http.MethodPost, path: "/api/auth/logout", want: http.StatusNoContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := setupAuthTestEnv(t, withMustChangePassword(true))
			token := "must-change-" + strings.ReplaceAll(tc.name, " ", "-")
			createAuthTestSession(t, env, authTestUserID, "session_"+strings.ReplaceAll(tc.name, " ", "_"), token, time.Now().UTC().Add(time.Hour), false)
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.AddCookie(authTestCookie(env, token))
			w := httptest.NewRecorder()

			env.router.ServeHTTP(w, req)

			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d; body = %s", w.Code, tc.want, w.Body.String())
			}
		})
	}
}

func TestPasswordChangeRequiredBlocksBusinessRoute(t *testing.T) {
	env := setupAuthTestEnv(t, withMustChangePassword(true))
	token := "must-change-business-token"
	createAuthTestSession(t, env, authTestUserID, "session_must_change_business", token, time.Now().UTC().Add(time.Hour), false)
	req := httptest.NewRequest(http.MethodGet, "/api/notes", nil)
	req.AddCookie(authTestCookie(env, token))
	w := httptest.NewRecorder()

	env.router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body = %s", w.Code, w.Body.String())
	}
	assertErrorCode(t, w.Body.String(), "PASSWORD_CHANGE_REQUIRED")
}

func TestExpiredOrRevokedSessionReturns401(t *testing.T) {
	for _, tc := range []struct {
		name    string
		revoked bool
		expires time.Time
	}{
		{name: "expired", expires: time.Now().UTC().Add(-time.Minute)},
		{name: "revoked", revoked: true, expires: time.Now().UTC().Add(time.Hour)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := setupAuthTestEnv(t)
			token := tc.name + "-token"
			createAuthTestSession(t, env, authTestUserID, "session_"+tc.name, token, tc.expires, tc.revoked)
			req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
			req.AddCookie(authTestCookie(env, token))
			w := httptest.NewRecorder()

			env.router.ServeHTTP(w, req)

			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401; body = %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestLogoutRevokesCurrentSessionAndClearsCookie(t *testing.T) {
	env := setupAuthTestEnv(t)
	token := "logout-token"
	sessionID := "session_logout"
	createAuthTestSession(t, env, authTestUserID, sessionID, token, time.Now().UTC().Add(time.Hour), false)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	req.AddCookie(authTestCookie(env, token))
	w := httptest.NewRecorder()

	env.router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body = %s", w.Code, w.Body.String())
	}
	if !sessionRevoked(t, env, token) {
		t.Fatalf("session %s was not revoked", sessionID)
	}
	cookie := requireCookie(t, w.Result(), env.cfg.Cookie.Name)
	if cookie.MaxAge != -1 {
		t.Fatalf("logout did not clear session cookie: %#v", cookie)
	}
}

func TestLogoutClearsCookieWithoutValidSession(t *testing.T) {
	env := setupAuthTestEnv(t)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	req.AddCookie(authTestCookie(env, "not-a-real-session"))
	w := httptest.NewRecorder()

	env.router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body = %s", w.Code, w.Body.String())
	}
	cookie := requireCookie(t, w.Result(), env.cfg.Cookie.Name)
	if cookie.MaxAge != -1 {
		t.Fatalf("logout did not clear session cookie: %#v", cookie)
	}
}

func TestChangePasswordVerifiesCurrentPassword(t *testing.T) {
	env := setupAuthTestEnv(t)
	token := "wrong-password-token"
	createAuthTestSession(t, env, authTestUserID, "session_wrong_password", token, time.Now().UTC().Add(time.Hour), false)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/change-password", strings.NewReader(`{"current_password":"wrong1234","new_password":"newpass123"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(authTestCookie(env, token))
	w := httptest.NewRecorder()

	env.router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body = %s", w.Code, w.Body.String())
	}
	loaded, err := env.store.Auth().GetUserByID(t.Context(), authTestUserID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if err := authpkg.VerifyPassword(loaded.PasswordHash, authTestPassword); err != nil {
		t.Fatalf("original password should remain valid: %v", err)
	}
}

func TestChangePasswordRevokesOtherSessionsUsingCurrentSessionID(t *testing.T) {
	env := setupAuthTestEnv(t, withMustChangePassword(true))
	keptToken := "kept-session-token"
	otherToken := "other-session-token"
	keptSessionID := "session_keep"
	otherSessionID := "session_other"
	createAuthTestSession(t, env, authTestUserID, keptSessionID, keptToken, time.Now().UTC().Add(time.Hour), false)
	createAuthTestSession(t, env, authTestUserID, otherSessionID, otherToken, time.Now().UTC().Add(time.Hour), false)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/change-password", strings.NewReader(`{"current_password":"abc12345","new_password":"newpass123"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(authTestCookie(env, keptToken))
	w := httptest.NewRecorder()

	env.router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body = %s", w.Code, w.Body.String())
	}
	if sessionRevoked(t, env, keptToken) {
		t.Fatal("current session should be kept")
	}
	if !sessionRevoked(t, env, otherToken) {
		t.Fatalf("session %s should be revoked", otherSessionID)
	}
	loaded, err := env.store.Auth().GetUserByID(t.Context(), authTestUserID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if loaded.MustChangePassword {
		t.Fatal("must_change_password should be cleared")
	}
	if err := authpkg.VerifyPassword(loaded.PasswordHash, "newpass123"); err != nil {
		t.Fatalf("new password hash did not verify: %v", err)
	}
	if !auditEventRecorded(t, env.dbPath, "auth.change_password", authTestUserID) {
		t.Fatal("auth.change_password audit event was not recorded")
	}
}

type authTestEnv struct {
	router *gin.Engine
	store  storage.Store
	cfg    config.AuthConfig
	dbPath string
}

type authTestOption func(*model.User)

func withMustChangePassword(value bool) authTestOption {
	return func(user *model.User) {
		user.MustChangePassword = value
	}
}

func withUserStatus(status string) authTestOption {
	return func(user *model.User) {
		user.Status = status
	}
}

func setupAuthTestEnv(t *testing.T, opts ...authTestOption) *authTestEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dbPath := filepath.Join(t.TempDir(), "auth-handler.db")
	store, err := sqlite.Provider{}.Open(t.Context(), storage.Config{
		Env:        "test",
		Driver:     storage.DriverSQLite,
		Name:       "flowspace_test",
		SQLitePath: dbPath,
	})
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	repository.SetStore(store)
	t.Cleanup(func() {
		repository.SetStore(nil)
		if err := store.Close(); err != nil {
			t.Fatalf("close sqlite store: %v", err)
		}
	})

	cfg := config.AuthConfig{
		Cookie: config.CookieConfig{
			Name:     "fs_session",
			SameSite: "Lax",
		},
		Session: config.SessionTTLConfig{
			ShortTTL:    12 * time.Hour,
			RememberTTL: 30 * 24 * time.Hour,
		},
		SessionSecret: authTestSessionSecret,
	}
	seedAuthHandlerUser(t, store, opts...)

	authMiddleware := middleware.AuthMiddleware{Store: store, SessionSecret: cfg.SessionSecret, Cookie: cfg.Cookie}
	r := gin.New()
	authRoutes := r.Group("/api/auth")
	authRoutes.POST("/login", Login(store, cfg))
	authRoutes.POST("/logout", authMiddleware.Optional(), Logout(store, cfg.Cookie))
	authRoutes.GET("/me", authMiddleware.Required(), Me(store))
	authRoutes.POST("/change-password", authMiddleware.Required(), ChangePassword(store))
	protected := r.Group("/api")
	protected.Use(authMiddleware.Required(), authMiddleware.RequirePasswordSettled())
	protected.GET("/notes", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	return &authTestEnv{router: r, store: store, cfg: cfg, dbPath: dbPath}
}

func seedAuthHandlerUser(t *testing.T, store storage.Store, opts ...authTestOption) {
	t.Helper()
	passwordHash, err := authpkg.HashPassword(authTestPassword)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	user := &model.User{
		ID:                 authTestUserID,
		Email:              "admin@example.com",
		DisplayName:        "Admin",
		PasswordHash:       passwordHash,
		MustChangePassword: false,
		DefaultWorkspaceID: authTestWorkspaceID,
		Role:               "admin",
		Status:             "active",
	}
	for _, opt := range opts {
		opt(user)
	}
	workspace := &model.Workspace{
		ID:          authTestWorkspaceID,
		Name:        "Admin Workspace",
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
		t.Fatalf("seed auth user: %v", err)
	}
}

func createAuthTestSession(t *testing.T, env *authTestEnv, userID, sessionID, token string, expiresAt time.Time, revoked bool) {
	t.Helper()
	session := &model.Session{
		ID:          sessionID,
		UserID:      userID,
		WorkspaceID: authTestWorkspaceID,
		TokenHash:   hashAuthTestToken(t, env, token),
		UserAgent:   "auth-test",
		IPAddress:   "127.0.0.1",
		ExpiresAt:   expiresAt,
	}
	if err := env.store.Auth().CreateSession(t.Context(), session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if revoked {
		if err := env.store.Auth().RevokeSession(t.Context(), sessionID); err != nil {
			t.Fatalf("revoke session: %v", err)
		}
	}
}

func authTestCookie(env *authTestEnv, token string) *http.Cookie {
	return &http.Cookie{Name: env.cfg.Cookie.Name, Value: token}
}

func requireCookie(t *testing.T, response *http.Response, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range response.Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("cookie %q missing from response cookies: %#v", name, response.Cookies())
	return nil
}

func createdSessionForCookie(t *testing.T, env *authTestEnv, cookie *http.Cookie) *model.Session {
	t.Helper()
	hash := hashAuthTestToken(t, env, cookie.Value)
	session, err := env.store.Auth().GetSessionByTokenHash(t.Context(), hash)
	if err != nil {
		t.Fatalf("get created session: %v", err)
	}
	return session
}

func hashAuthTestToken(t *testing.T, env *authTestEnv, token string) string {
	t.Helper()
	hash, err := authpkg.HashSessionToken(env.cfg.SessionSecret, token)
	if err != nil {
		t.Fatalf("hash session token: %v", err)
	}
	return hash
}

func lastLoginUpdated(t *testing.T, store storage.Store, userID string) bool {
	t.Helper()
	user, err := store.Auth().GetUserByID(t.Context(), userID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	return user.LastLoginAt != nil && *user.LastLoginAt > 0
}

func auditEventRecorded(t *testing.T, dbPath, action, actorUserID string) bool {
	t.Helper()
	return countAuthTestRows(t, dbPath, `
		SELECT COUNT(*)
		FROM audit_events
		WHERE action = ? AND actor_user_id = ?
	`, action, actorUserID) > 0
}

func sessionRevoked(t *testing.T, env *authTestEnv, token string) bool {
	t.Helper()
	_, err := env.store.Auth().GetSessionByTokenHash(t.Context(), hashAuthTestToken(t, env, token))
	return errors.Is(err, sql.ErrNoRows)
}

func execAuthTestSQL(t *testing.T, dbPath, statement string, args ...any) {
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

func countAuthTestRows(t *testing.T, dbPath, query string, args ...any) int {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath+"?_foreign_keys=ON")
	if err != nil {
		t.Fatalf("open sqlite side connection: %v", err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return count
}

func assertErrorCode(t *testing.T, body string, want string) {
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
