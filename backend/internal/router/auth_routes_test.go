package router

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	authpkg "github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/config"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/repository"
	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/storage/sqlite"
)

const (
	routerTestSecret      = "router-session-secret-with-at-least-32-bytes"
	routerTestUserID      = "router_user"
	routerTestWorkspaceID = "router_workspace"
)

func TestAuthRoutesAreRegistered(t *testing.T) {
	env := setupRouterAuthEnv(t, false)
	routes := []string{
		"POST /api/auth/login",
		"POST /api/auth/logout",
		"GET /api/auth/me",
		"POST /api/auth/change-password",
	}

	registered := registeredRoutes(Setup(env.config))

	for _, route := range routes {
		if !registered[route] {
			t.Fatalf("route %s is not registered", route)
		}
	}
}

func TestProtectedBusinessRouteRequiresAuth(t *testing.T) {
	env := setupRouterAuthEnv(t, false)
	req := httptest.NewRequest(http.MethodGet, "/api/notes", nil)
	w := httptest.NewRecorder()

	Setup(env.config).ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body = %s", w.Code, w.Body.String())
	}
}

func TestLocalDirectoryBrowserRouteRegistrationFollowsConfig(t *testing.T) {
	disabled := setupRouterAuthEnv(t, false)
	if registeredRoutes(Setup(disabled.config))["GET /api/system/directories"] {
		t.Fatal("local directory browser route should not be registered when disabled")
	}

	enabled := setupRouterAuthEnv(t, true)
	if !registeredRoutes(Setup(enabled.config))["GET /api/system/directories"] {
		t.Fatal("local directory browser route should be registered when enabled")
	}
}

func TestAdminUserRoutesAreRegistered(t *testing.T) {
	env := setupRouterAuthEnv(t, false)
	routes := []string{
		"GET /api/admin/users",
		"POST /api/admin/users",
		"PATCH /api/admin/users/:id",
		"POST /api/admin/users/:id/reset-password",
		"POST /api/admin/users/:id/disable",
		"POST /api/admin/users/:id/enable",
	}

	registered := registeredRoutes(Setup(env.config))

	for _, route := range routes {
		if !registered[route] {
			t.Fatalf("route %s is not registered", route)
		}
	}
}

func TestAdminUserRoutesRequireAdmin(t *testing.T) {
	env := setupRouterAuthEnv(t, false, withRouterRole("user"))
	token := "admin-users-non-admin-token"
	createRouterSession(t, env, token)
	req := httptest.NewRequest(http.MethodGet, "/api/admin/users", nil)
	req.AddCookie(&http.Cookie{Name: env.auth.Cookie.Name, Value: token})
	w := httptest.NewRecorder()

	Setup(env.config).ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body = %s", w.Code, w.Body.String())
	}
}

func TestLocalDirectoryBrowserRequiresAdmin(t *testing.T) {
	env := setupRouterAuthEnv(t, true, withRouterRole("user"))
	token := "local-directory-user-token"
	createRouterSession(t, env, token)
	req := httptest.NewRequest(http.MethodGet, "/api/system/directories", nil)
	req.AddCookie(&http.Cookie{Name: env.auth.Cookie.Name, Value: token})
	w := httptest.NewRecorder()

	Setup(env.config).ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body = %s", w.Code, w.Body.String())
	}
}

func TestAuthMiddlewareUsesConfiguredCookieNameForWorkspaceRevocation(t *testing.T) {
	env := setupRouterAuthEnv(t, false)
	env.auth.Cookie.Name = "custom_session"
	env.config.Auth = env.auth
	token := "custom-cookie-token"
	createRouterSession(t, env, token)
	execRouterAuthSQL(t, env.dbPath, `DELETE FROM workspace_members WHERE workspace_id = ? AND user_id = ?`, routerTestWorkspaceID, routerTestUserID)
	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: env.auth.Cookie.Name, Value: token})
	w := httptest.NewRecorder()

	Setup(env.config).ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body = %s", w.Code, w.Body.String())
	}
	assertRouterErrorCode(t, w.Body.String(), "WORKSPACE_ACCESS_REVOKED")
	clearedCookie := requireRouterResponseCookie(t, w.Result(), env.auth.Cookie.Name)
	if clearedCookie.MaxAge != -1 {
		t.Fatalf("cleared cookie MaxAge = %d, want -1: %#v", clearedCookie.MaxAge, clearedCookie)
	}
	if clearedCookie.Path != "/" {
		t.Fatalf("cleared cookie Path = %q, want /: %#v", clearedCookie.Path, clearedCookie)
	}
}

type routerAuthEnv struct {
	store  storage.Store
	auth   config.AuthConfig
	config Config
	dbPath string
}

type routerUserOption func(*model.User)

func withRouterRole(role string) routerUserOption {
	return func(user *model.User) {
		user.Role = role
	}
}

func setupRouterAuthEnv(t *testing.T, enableDirectoryBrowser bool, opts ...routerUserOption) *routerAuthEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dbPath := filepath.Join(t.TempDir(), "auth-router.db")
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
	seedRouterAuthUser(t, store, opts...)

	authCfg := testRouterAuthConfig(enableDirectoryBrowser)
	return &routerAuthEnv{
		store:  store,
		auth:   authCfg,
		config: testRouterConfig(store, authCfg),
		dbPath: dbPath,
	}
}

func testRouterAuthConfig(enableDirectoryBrowser bool) config.AuthConfig {
	return config.AuthConfig{
		Cookie: config.CookieConfig{
			Name:     "fs_session",
			SameSite: "Lax",
		},
		Session: config.SessionTTLConfig{
			ShortTTL:    12 * time.Hour,
			RememberTTL: 30 * 24 * time.Hour,
		},
		SessionSecret:               routerTestSecret,
		EnableLocalDirectoryBrowser: enableDirectoryBrowser,
	}
}

func testRouterConfig(store storage.Store, authCfg config.AuthConfig) Config {
	return Config{
		Store: store,
		Auth:  authCfg,
	}
}

func seedRouterAuthUser(t *testing.T, store storage.Store, opts ...routerUserOption) {
	t.Helper()
	passwordHash, err := authpkg.HashPassword("abc12345")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	user := &model.User{
		ID:                 routerTestUserID,
		Email:              "router@example.com",
		DisplayName:        "Router User",
		PasswordHash:       passwordHash,
		DefaultWorkspaceID: routerTestWorkspaceID,
		Role:               "admin",
		Status:             "active",
	}
	for _, opt := range opts {
		opt(user)
	}
	workspace := &model.Workspace{
		ID:          routerTestWorkspaceID,
		Name:        "Router Workspace",
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
		t.Fatalf("seed router auth user: %v", err)
	}
}

func createRouterSession(t *testing.T, env *routerAuthEnv, token string) {
	t.Helper()
	hash, err := authpkg.HashSessionToken(env.auth.SessionSecret, token)
	if err != nil {
		t.Fatalf("hash session token: %v", err)
	}
	session := &model.Session{
		ID:          "router_session",
		UserID:      routerTestUserID,
		WorkspaceID: routerTestWorkspaceID,
		TokenHash:   hash,
		UserAgent:   "router-test",
		IPAddress:   "127.0.0.1",
		ExpiresAt:   time.Now().UTC().Add(time.Hour),
	}
	if err := env.store.Auth().CreateSession(t.Context(), session); err != nil {
		t.Fatalf("create router session: %v", err)
	}
}

func registeredRoutes(router *gin.Engine) map[string]bool {
	registered := map[string]bool{}
	for _, route := range router.Routes() {
		registered[route.Method+" "+route.Path] = true
	}
	return registered
}

func execRouterAuthSQL(t *testing.T, dbPath, statement string, args ...any) {
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

func requireRouterResponseCookie(t *testing.T, response *http.Response, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range response.Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("response cookie %q missing from cookies: %#v", name, response.Cookies())
	return nil
}

func assertRouterErrorCode(t *testing.T, body string, want string) {
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
