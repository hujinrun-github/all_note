package handler

import (
	"database/sql"
	"encoding/json"
	"errors"
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
)

const (
	adminTestPassword      = "abc12345"
	adminTestSessionSecret = "admin-session-secret-with-at-least-32-bytes"
	adminTestUserID        = "user_admin"
	adminTestWorkspaceID   = "workspace_admin"
)

func TestAdminListUsersReturnsPagination(t *testing.T) {
	env := setupAdminTestEnv(t)
	req := httptest.NewRequest(http.MethodGet, "/api/admin/users?page=1&page_size=20&q=admin", nil)
	req.AddCookie(adminSessionCookie(t, env))
	w := httptest.NewRecorder()

	env.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"pagination"`) || !strings.Contains(body, `"users"`) {
		t.Fatalf("missing users or pagination: %s", body)
	}
}

func TestAdminCreateUserProvisionsWorkspaceDefaultsAndRequiresPasswordChange(t *testing.T) {
	env := setupAdminTestEnv(t)
	body := `{"email":"created@example.com","display_name":"Created User","temporary_password":"tempPass123","role":"user"}`
	req := httptest.NewRequest(http.MethodPost, "/api/admin/users", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(adminSessionCookie(t, env))
	w := httptest.NewRecorder()

	env.router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", w.Code, w.Body.String())
	}
	user := getAdminTestUserByEmail(t, env.store, "created@example.com")
	if !user.MustChangePassword {
		t.Fatal("created user must_change_password = false, want true")
	}
	if user.DefaultWorkspaceID == "" {
		t.Fatal("created user default workspace is empty")
	}
	if countAdminTestRows(t, env.dbPath, `
		SELECT COUNT(*) FROM folders
		WHERE workspace_id = ? AND id IN ('__uncategorized', '__work', '__personal')
	`, user.DefaultWorkspaceID) != 3 {
		t.Fatalf("default folders were not provisioned for workspace %q", user.DefaultWorkspaceID)
	}
	if countAdminTestRows(t, env.dbPath, `
		SELECT COUNT(*) FROM task_projects
		WHERE workspace_id = ? AND id = 'personal'
	`, user.DefaultWorkspaceID) != 1 {
		t.Fatalf("default personal project was not provisioned for workspace %q", user.DefaultWorkspaceID)
	}

	loginReq := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"email":"created@example.com","password":"tempPass123"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	loginW := httptest.NewRecorder()
	env.router.ServeHTTP(loginW, loginReq)
	if loginW.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", loginW.Code, loginW.Body.String())
	}
	loginCookie := requireCookie(t, loginW.Result(), env.cfg.Cookie.Name)
	notesReq := httptest.NewRequest(http.MethodGet, "/api/notes", nil)
	notesReq.AddCookie(loginCookie)
	notesW := httptest.NewRecorder()
	env.router.ServeHTTP(notesW, notesReq)
	if notesW.Code != http.StatusForbidden {
		t.Fatalf("protected status = %d, want 403; body = %s", notesW.Code, notesW.Body.String())
	}
	assertErrorCode(t, notesW.Body.String(), "PASSWORD_CHANGE_REQUIRED")
}

func TestAdminCreateUserAuditDoesNotIncludeTemporaryPasswordOrHash(t *testing.T) {
	env := setupAdminTestEnv(t)
	body := `{"email":"audited@example.com","display_name":"Audited User","temporary_password":"auditPass123","role":"admin"}`
	req := httptest.NewRequest(http.MethodPost, "/api/admin/users", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(adminSessionCookie(t, env))
	w := httptest.NewRecorder()

	env.router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", w.Code, w.Body.String())
	}
	user := getAdminTestUserByEmail(t, env.store, "audited@example.com")
	metadata := lastAdminAuditMetadata(t, env.dbPath, "admin.user.create", user.ID)
	assertAdminAuditMetadataExcludes(t, metadata, "auditPass123", "temporary_password", "password_hash")
}

func TestAdminPatchRejectsStatusAndPassword(t *testing.T) {
	env := setupAdminTestEnv(t)
	seedAdminTestUser(t, env.store, adminSeedUser{
		ID:          "user_1",
		Email:       "user1@example.com",
		DisplayName: "User One",
		Role:        "user",
		Status:      "active",
	})
	body := `{"status":"disabled","temporary_password":"abc12345","password":"abc12345"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/admin/users/user_1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(adminSessionCookie(t, env))
	w := httptest.NewRecorder()

	env.router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", w.Code, w.Body.String())
	}
}

func TestAdminUpdateAuditsDesignActionName(t *testing.T) {
	env := setupAdminTestEnv(t)
	seedAdminTestUser(t, env.store, adminSeedUser{
		ID:          "user_update",
		Email:       "update@example.com",
		DisplayName: "Update Target",
		Role:        "user",
		Status:      "active",
	})
	body := `{"display_name":"Updated Target"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/admin/users/user_update", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(adminSessionCookie(t, env))
	w := httptest.NewRecorder()

	env.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	if !adminAuditEventRecorded(t, env.dbPath, "admin.user.update", "user_update") {
		t.Fatal("admin.user.update audit event was not recorded")
	}
}

func TestDowngradeLastAdminReturnsConflict(t *testing.T) {
	env := setupAdminTestEnv(t)
	body := `{"role":"user"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/admin/users/user_admin", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(adminSessionCookie(t, env))
	w := httptest.NewRecorder()

	env.router.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body = %s", w.Code, w.Body.String())
	}
}

func TestDisableLastAdminReturnsConflict(t *testing.T) {
	env := setupAdminTestEnv(t)
	req := httptest.NewRequest(http.MethodPost, "/api/admin/users/user_admin/disable", nil)
	req.AddCookie(adminSessionCookie(t, env))
	w := httptest.NewRecorder()

	env.router.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body = %s", w.Code, w.Body.String())
	}
}

func TestAdminRoleChangeRollsBackWhenSessionRevokeFails(t *testing.T) {
	env := setupAdminTestEnv(t)
	seedAdminTestUser(t, env.store, adminSeedUser{
		ID:          "user_target",
		Email:       "target@example.com",
		DisplayName: "Target Admin",
		Role:        "admin",
		Status:      "active",
	})
	createAdminTestSession(t, env, "user_target", "session_target", "target-token")
	installAdminRevokeFailureTrigger(t, env.dbPath)
	body := `{"role":"user"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/admin/users/user_target", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(adminSessionCookie(t, env))
	w := httptest.NewRecorder()

	env.router.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body = %s", w.Code, w.Body.String())
	}
	if got := adminTestUser(t, env.store, "user_target").Role; got != "admin" {
		t.Fatalf("role = %q, want admin after rollback", got)
	}
}

func TestDisableRollsBackWhenSessionRevokeFails(t *testing.T) {
	env := setupAdminTestEnv(t)
	seedAdminTestUser(t, env.store, adminSeedUser{
		ID:          "user_disable_target",
		Email:       "disable@example.com",
		DisplayName: "Disable Target",
		Role:        "user",
		Status:      "active",
	})
	createAdminTestSession(t, env, "user_disable_target", "session_disable_target", "disable-token")
	installAdminRevokeFailureTrigger(t, env.dbPath)
	req := httptest.NewRequest(http.MethodPost, "/api/admin/users/user_disable_target/disable", nil)
	req.AddCookie(adminSessionCookie(t, env))
	w := httptest.NewRecorder()

	env.router.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body = %s", w.Code, w.Body.String())
	}
	if got := adminTestUser(t, env.store, "user_disable_target").Status; got != "active" {
		t.Fatalf("status = %q, want active after rollback", got)
	}
}

func TestAdminResetPasswordAuditMetadataExcludesSecretsAndRevokesSessions(t *testing.T) {
	env := setupAdminTestEnv(t)
	seedAdminTestUser(t, env.store, adminSeedUser{
		ID:          "user_reset",
		Email:       "reset@example.com",
		DisplayName: "Reset Target",
		Role:        "user",
		Status:      "active",
	})
	createAdminTestSession(t, env, "user_reset", "session_reset", "reset-user-token")
	body := `{"temporary_password":"resetPass123"}`
	req := httptest.NewRequest(http.MethodPost, "/api/admin/users/user_reset/reset-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer should-not-be-audited")
	req.AddCookie(adminSessionCookie(t, env))
	w := httptest.NewRecorder()

	env.router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body = %s", w.Code, w.Body.String())
	}
	loaded := adminTestUser(t, env.store, "user_reset")
	if !loaded.MustChangePassword {
		t.Fatal("must_change_password = false, want true")
	}
	if err := authpkg.VerifyPassword(loaded.PasswordHash, "resetPass123"); err != nil {
		t.Fatalf("reset password hash did not verify: %v", err)
	}
	if !adminTestSessionRevoked(t, env, "reset-user-token") {
		t.Fatal("target sessions were not revoked")
	}
	metadata := lastAdminAuditMetadata(t, env.dbPath, "admin.user.reset_password", "user_reset")
	assertAdminAuditMetadataExcludes(t, metadata,
		"resetPass123",
		"temporary_password",
		"password_hash",
		"cookie",
		"authorization",
		"raw_session_token",
		"hashed_session_token",
		"token_hash",
		"should-not-be-audited",
	)
}

func TestAdminDisableRevokesSessionsAndAudits(t *testing.T) {
	env := setupAdminTestEnv(t)
	seedAdminTestUser(t, env.store, adminSeedUser{
		ID:          "user_disable",
		Email:       "disable-ok@example.com",
		DisplayName: "Disable OK",
		Role:        "user",
		Status:      "active",
	})
	createAdminTestSession(t, env, "user_disable", "session_disable", "disable-ok-token")
	req := httptest.NewRequest(http.MethodPost, "/api/admin/users/user_disable/disable", nil)
	req.AddCookie(adminSessionCookie(t, env))
	w := httptest.NewRecorder()

	env.router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body = %s", w.Code, w.Body.String())
	}
	if got := adminTestUser(t, env.store, "user_disable").Status; got != "disabled" {
		t.Fatalf("status = %q, want disabled", got)
	}
	if !adminTestSessionRevoked(t, env, "disable-ok-token") {
		t.Fatal("target sessions were not revoked")
	}
	if !adminAuditEventRecorded(t, env.dbPath, "admin.user.disable", "user_disable") {
		t.Fatal("admin.user.disable audit event was not recorded")
	}
}

func TestAdminEnableAudits(t *testing.T) {
	env := setupAdminTestEnv(t)
	seedAdminTestUser(t, env.store, adminSeedUser{
		ID:          "user_enable",
		Email:       "enable@example.com",
		DisplayName: "Enable Target",
		Role:        "user",
		Status:      "disabled",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/admin/users/user_enable/enable", nil)
	req.AddCookie(adminSessionCookie(t, env))
	w := httptest.NewRecorder()

	env.router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body = %s", w.Code, w.Body.String())
	}
	if got := adminTestUser(t, env.store, "user_enable").Status; got != "active" {
		t.Fatalf("status = %q, want active", got)
	}
	if !adminAuditEventRecorded(t, env.dbPath, "admin.user.enable", "user_enable") {
		t.Fatal("admin.user.enable audit event was not recorded")
	}
}

func TestAdminRoutesRequireAdminRole(t *testing.T) {
	env := setupAdminTestEnv(t, withAdminActorRole("user"))
	req := httptest.NewRequest(http.MethodGet, "/api/admin/users", nil)
	req.AddCookie(adminSessionCookie(t, env))
	w := httptest.NewRecorder()

	env.router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body = %s", w.Code, w.Body.String())
	}
	assertErrorCode(t, w.Body.String(), "FORBIDDEN")
}

type adminTestEnv struct {
	router *gin.Engine
	store  storage.Store
	cfg    config.AuthConfig
	dbPath string
}

type adminActorOption func(*model.User)

func withAdminActorRole(role string) adminActorOption {
	return func(user *model.User) {
		user.Role = role
	}
}

func setupAdminTestEnv(t *testing.T, opts ...adminActorOption) *adminTestEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dbPath := filepath.Join(t.TempDir(), "admin-users.db")
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
		SessionSecret: adminTestSessionSecret,
	}
	seedAdminActor(t, store, opts...)

	authMiddleware := middleware.AuthMiddleware{Store: store, SessionSecret: cfg.SessionSecret, Cookie: cfg.Cookie}
	r := gin.New()
	authRoutes := r.Group("/api/auth")
	authRoutes.POST("/login", Login(store, cfg))
	authRoutes.GET("/me", authMiddleware.Required(), Me(store))

	protected := r.Group("/api")
	protected.Use(authMiddleware.Required(), authMiddleware.RequirePasswordSettled())
	protected.GET("/notes", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	admin := protected.Group("/admin")
	admin.Use(authMiddleware.RequireAdmin())
	admin.GET("/users", ListUsers(store))
	admin.POST("/users", CreateUser(store))
	admin.PATCH("/users/:id", UpdateUser(store))
	admin.POST("/users/:id/reset-password", ResetUserPassword(store))
	admin.POST("/users/:id/disable", DisableUser(store))
	admin.POST("/users/:id/enable", EnableUser(store))

	return &adminTestEnv{router: r, store: store, cfg: cfg, dbPath: dbPath}
}

type adminSeedUser struct {
	ID                 string
	Email              string
	DisplayName        string
	Role               string
	Status             string
	MustChangePassword bool
}

func seedAdminActor(t *testing.T, store storage.Store, opts ...adminActorOption) {
	t.Helper()
	user := &model.User{
		ID:                 adminTestUserID,
		Email:              "admin@example.com",
		DisplayName:        "Admin",
		DefaultWorkspaceID: adminTestWorkspaceID,
		Role:               "admin",
		Status:             "active",
	}
	for _, opt := range opts {
		opt(user)
	}
	hash, err := authpkg.HashPassword(adminTestPassword)
	if err != nil {
		t.Fatalf("hash admin password: %v", err)
	}
	user.PasswordHash = hash
	workspace := &model.Workspace{
		ID:          adminTestWorkspaceID,
		Name:        "Admin Workspace",
		OwnerUserID: user.ID,
	}
	if err := store.Transact(t.Context(), func(tx storage.Store) error {
		if err := tx.Auth().CreateUser(t.Context(), user); err != nil {
			return err
		}
		if err := tx.Auth().CreateWorkspace(t.Context(), workspace); err != nil {
			return err
		}
		return tx.Auth().AddWorkspaceMember(t.Context(), workspace.ID, user.ID, "owner")
	}); err != nil {
		t.Fatalf("seed admin actor: %v", err)
	}
}

func seedAdminTestUser(t *testing.T, store storage.Store, seed adminSeedUser) {
	t.Helper()
	hash, err := authpkg.HashPassword(adminTestPassword)
	if err != nil {
		t.Fatalf("hash seed password: %v", err)
	}
	workspaceID := seed.ID + "_workspace"
	user := &model.User{
		ID:                 seed.ID,
		Email:              seed.Email,
		DisplayName:        seed.DisplayName,
		PasswordHash:       hash,
		MustChangePassword: seed.MustChangePassword,
		DefaultWorkspaceID: workspaceID,
		Role:               seed.Role,
		Status:             seed.Status,
	}
	if user.Status == "" {
		user.Status = "active"
	}
	if user.Role == "" {
		user.Role = "user"
	}
	workspace := &model.Workspace{
		ID:          workspaceID,
		Name:        seed.DisplayName + " Workspace",
		OwnerUserID: seed.ID,
	}
	if err := store.Transact(t.Context(), func(tx storage.Store) error {
		if err := tx.Auth().CreateUser(t.Context(), user); err != nil {
			return err
		}
		if err := tx.Auth().CreateWorkspace(t.Context(), workspace); err != nil {
			return err
		}
		return tx.Auth().AddWorkspaceMember(t.Context(), workspace.ID, user.ID, "owner")
	}); err != nil {
		t.Fatalf("seed admin test user %s: %v", seed.ID, err)
	}
}

func adminSessionCookie(t *testing.T, env *adminTestEnv) *http.Cookie {
	t.Helper()
	token := "admin-session-token"
	createAdminTestSession(t, env, adminTestUserID, "session_admin", token)
	return &http.Cookie{Name: env.cfg.Cookie.Name, Value: token}
}

func createAdminTestSession(t *testing.T, env *adminTestEnv, userID, sessionID, token string) {
	t.Helper()
	user := adminTestUser(t, env.store, userID)
	session := &model.Session{
		ID:          sessionID,
		UserID:      userID,
		WorkspaceID: user.DefaultWorkspaceID,
		TokenHash:   hashAdminTestToken(t, env, token),
		UserAgent:   "admin-test",
		IPAddress:   "127.0.0.1",
		ExpiresAt:   time.Now().UTC().Add(time.Hour),
	}
	if err := env.store.Auth().CreateSession(t.Context(), session); err != nil {
		t.Fatalf("create session %s: %v", sessionID, err)
	}
}

func hashAdminTestToken(t *testing.T, env *adminTestEnv, token string) string {
	t.Helper()
	hash, err := authpkg.HashSessionToken(env.cfg.SessionSecret, token)
	if err != nil {
		t.Fatalf("hash admin test token: %v", err)
	}
	return hash
}

func adminTestUser(t *testing.T, store storage.Store, userID string) *model.User {
	t.Helper()
	user, err := store.Auth().GetUserByID(t.Context(), userID)
	if err != nil {
		t.Fatalf("get user %s: %v", userID, err)
	}
	return user
}

func getAdminTestUserByEmail(t *testing.T, store storage.Store, email string) *model.User {
	t.Helper()
	user, err := store.Auth().GetUserByEmail(t.Context(), email)
	if err != nil {
		t.Fatalf("get user by email %s: %v", email, err)
	}
	return user
}

func adminTestSessionRevoked(t *testing.T, env *adminTestEnv, token string) bool {
	t.Helper()
	_, err := env.store.Auth().GetSessionByTokenHash(t.Context(), hashAdminTestToken(t, env, token))
	return errors.Is(err, sql.ErrNoRows)
}

func installAdminRevokeFailureTrigger(t *testing.T, dbPath string) {
	t.Helper()
	execAdminTestSQL(t, dbPath, `
		CREATE TRIGGER fail_admin_revoke
		BEFORE UPDATE OF revoked_at ON sessions
		WHEN NEW.revoked_at IS NOT NULL
		BEGIN
			SELECT RAISE(FAIL, 'session revoke failed');
		END;
	`)
}

func lastAdminAuditMetadata(t *testing.T, dbPath, action, targetUserID string) map[string]any {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath+"?_foreign_keys=ON")
	if err != nil {
		t.Fatalf("open sqlite side connection: %v", err)
	}
	defer db.Close()
	var raw string
	if err := db.QueryRow(`
		SELECT metadata
		FROM audit_events
		WHERE action = ? AND target_user_id = ?
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, action, targetUserID).Scan(&raw); err != nil {
		t.Fatalf("load audit metadata for %s/%s: %v", action, targetUserID, err)
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
		t.Fatalf("decode audit metadata: %v; raw = %s", err, raw)
	}
	return metadata
}

func assertAdminAuditMetadataExcludes(t *testing.T, metadata map[string]any, forbidden ...string) {
	t.Helper()
	raw, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	normalized := strings.ToLower(string(raw))
	for _, item := range forbidden {
		if strings.Contains(normalized, strings.ToLower(item)) {
			t.Fatalf("audit metadata contains forbidden value %q: %s", item, string(raw))
		}
	}
}

func adminAuditEventRecorded(t *testing.T, dbPath, action, targetUserID string) bool {
	t.Helper()
	return countAdminTestRows(t, dbPath, `
		SELECT COUNT(*)
		FROM audit_events
		WHERE action = ? AND target_user_id = ?
	`, action, targetUserID) > 0
}

func execAdminTestSQL(t *testing.T, dbPath, statement string, args ...any) {
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

func countAdminTestRows(t *testing.T, dbPath, query string, args ...any) int {
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
