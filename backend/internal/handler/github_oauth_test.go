package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
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

func TestAuthProvidersReturnsGitHubWhenAvailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := config.AuthConfig{GitHub: config.GitHubOAuthConfig{
		Enabled:         true,
		ClientID:        "client-id",
		ClientSecret:    "client-secret",
		RedirectURL:     "https://example.com/api/auth/github/callback",
		AutoCreateUsers: true,
		StateTTL:        10 * time.Minute,
	}}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	AuthProviders(cfg)(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	var body struct {
		Data struct {
			Providers []string `json:"providers"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Data.Providers) != 1 || body.Data.Providers[0] != "github" {
		t.Fatalf("providers = %#v, want [github]", body.Data.Providers)
	}
}

func TestGitHubOAuthStartRedirectsDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/auth/github/start?next=/tasks", nil)

	GitHubOAuthStart(nil, config.AuthConfig{}, nil)(c)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", w.Code)
	}
	if got := w.Header().Get("Location"); got != "/login?oauth_error=github_disabled" {
		t.Fatalf("Location = %q", got)
	}
}

func TestGitHubOAuthCallbackRedirectsDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/auth/github/callback?code=x&state=y", nil)

	GitHubOAuthCallback(nil, config.AuthConfig{}, nil, nil)(c)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", w.Code)
	}
	if got := w.Header().Get("Location"); got != "/login?oauth_error=github_disabled" {
		t.Fatalf("Location = %q", got)
	}
}

func TestGitHubOAuthStartSavesStateAndRedirectsToGitHub(t *testing.T) {
	env := setupGitHubOAuthHandlerEnv(t)
	req := httptest.NewRequest(http.MethodGet, "/api/auth/github/start?next=/tasks", nil)
	w := httptest.NewRecorder()

	env.router.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302; body = %s", w.Code, w.Body.String())
	}
	location := w.Header().Get("Location")
	if !strings.HasPrefix(location, "https://github.com/login/oauth/authorize?") {
		t.Fatalf("Location = %q", location)
	}
	if !strings.Contains(location, "client_id=client-id") {
		t.Fatalf("Location missing client_id: %q", location)
	}
	if !strings.Contains(location, "scope=read%3Auser+user%3Aemail") {
		t.Fatalf("Location missing scope: %q", location)
	}
	state := mustQueryParam(t, location, "state")
	next, err := env.stateStore.Consume(t.Context(), state)
	if err != nil {
		t.Fatalf("state not saved: %v", err)
	}
	if next != "/tasks" {
		t.Fatalf("next = %q, want /tasks", next)
	}
}

func TestGitHubOAuthStartIgnoresAlreadyLoggedInUsers(t *testing.T) {
	env := setupGitHubOAuthHandlerEnv(t)
	token := seedGitHubOAuthSession(t, env)
	req := httptest.NewRequest(http.MethodGet, "/api/auth/github/start?next=/notes", nil)
	req.AddCookie(&http.Cookie{Name: env.auth.Cookie.Name, Value: token})
	w := httptest.NewRecorder()

	env.router.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", w.Code)
	}
	if got := w.Header().Get("Location"); got != "/notes" {
		t.Fatalf("Location = %q, want /notes", got)
	}
}

func TestGitHubOAuthCallbackAutoCreatesUserWorkspaceIdentityAndSession(t *testing.T) {
	env := setupGitHubOAuthHandlerEnv(t)
	state := "state-success"
	if err := env.stateStore.Save(t.Context(), state, "/tasks", time.Minute); err != nil {
		t.Fatalf("save state: %v", err)
	}
	env.github.profile = GitHubProfile{ID: 123, Login: "octocat", Name: "Octo Cat", AvatarURL: "https://avatars.example/octo.png"}
	env.github.emails = []GitHubEmail{{Email: "octo@example.com", Primary: true, Verified: true}}

	req := httptest.NewRequest(http.MethodGet, "/api/auth/github/callback?code=code-ok&state="+state, nil)
	w := httptest.NewRecorder()
	env.router.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302; body = %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Location"); got != "/tasks" {
		t.Fatalf("Location = %q, want /tasks", got)
	}
	sessionCookie := requireCookie(t, w.Result(), env.auth.Cookie.Name)
	if sessionCookie.Value == "" {
		t.Fatal("session cookie value is empty")
	}
	user, err := env.store.Auth().GetUserByEmail(t.Context(), "octo@example.com")
	if err != nil {
		t.Fatalf("get created user: %v", err)
	}
	if user.DefaultWorkspaceID == "" {
		t.Fatal("default workspace not set")
	}
	if user.PasswordSet {
		t.Fatal("GitHub user PasswordSet = true, want false")
	}
	identity, err := env.store.Auth().GetAuthIdentity(t.Context(), "github", "123")
	if err != nil {
		t.Fatalf("get identity: %v", err)
	}
	if identity.UserID != user.ID || identity.ProviderLogin != "octocat" {
		t.Fatalf("unexpected identity: %+v", identity)
	}
}

func TestGitHubOAuthCallbackRevokesExistingSessionBeforeNewSession(t *testing.T) {
	env := setupGitHubOAuthHandlerEnv(t)
	oldToken := seedGitHubOAuthSession(t, env)
	state := "state-fixation"
	if err := env.stateStore.Save(t.Context(), state, "/", time.Minute); err != nil {
		t.Fatalf("save state: %v", err)
	}
	env.github.profile = GitHubProfile{ID: 456, Login: "secure", Name: "Secure User"}
	env.github.emails = []GitHubEmail{{Email: "secure@example.com", Primary: true, Verified: true}}

	req := httptest.NewRequest(http.MethodGet, "/api/auth/github/callback?code=code-ok&state="+state, nil)
	req.AddCookie(&http.Cookie{Name: env.auth.Cookie.Name, Value: oldToken})
	w := httptest.NewRecorder()
	env.router.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", w.Code)
	}
	oldHash, err := authpkg.HashSessionToken(env.auth.SessionSecret, oldToken)
	if err != nil {
		t.Fatalf("hash old token: %v", err)
	}
	if _, err := env.store.Auth().GetSessionByTokenHash(t.Context(), oldHash); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("old session lookup error = %v, want sql.ErrNoRows", err)
	}
	newCookie := requireCookie(t, w.Result(), env.auth.Cookie.Name)
	if newCookie.Value == oldToken {
		t.Fatal("new session reused old token")
	}
}

func TestGitHubOAuthCallbackRejectsBadState(t *testing.T) {
	env := setupGitHubOAuthHandlerEnv(t)
	req := httptest.NewRequest(http.MethodGet, "/api/auth/github/callback?code=code-ok&state=missing", nil)
	w := httptest.NewRecorder()

	env.router.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", w.Code)
	}
	if got := w.Header().Get("Location"); got != "/login?oauth_error=github_state_invalid" {
		t.Fatalf("Location = %q", got)
	}
	if env.github.exchangeCode != "" {
		t.Fatalf("exchange called with %q for invalid state", env.github.exchangeCode)
	}
}

func TestGitHubOAuthCallbackRedirectsExchangeFailure(t *testing.T) {
	env := setupGitHubOAuthHandlerEnv(t)
	state := "state-exchange"
	_ = env.stateStore.Save(t.Context(), state, "/", time.Minute)
	env.github.exchangeErr = errors.New("exchange failed")
	req := httptest.NewRequest(http.MethodGet, "/api/auth/github/callback?code=bad&state="+state, nil)
	w := httptest.NewRecorder()

	env.router.ServeHTTP(w, req)

	if got := w.Header().Get("Location"); got != "/login?oauth_error=github_exchange_failed" {
		t.Fatalf("Location = %q", got)
	}
}

func TestGitHubOAuthCallbackRedirectsProfileFailureForUserAPI5xx(t *testing.T) {
	env := setupGitHubOAuthHandlerEnv(t)
	state := "state-profile"
	_ = env.stateStore.Save(t.Context(), state, "/", time.Minute)
	env.github.profileErr = errors.New("github user api failed")
	req := httptest.NewRequest(http.MethodGet, "/api/auth/github/callback?code=ok&state="+state, nil)
	w := httptest.NewRecorder()

	env.router.ServeHTTP(w, req)

	if got := w.Header().Get("Location"); got != "/login?oauth_error=github_profile_failed" {
		t.Fatalf("Location = %q", got)
	}
}

func TestGitHubOAuthCallbackRedirectsProfileFailureForEmailsAPI5xx(t *testing.T) {
	env := setupGitHubOAuthHandlerEnv(t)
	state := "state-emails"
	_ = env.stateStore.Save(t.Context(), state, "/", time.Minute)
	env.github.profile = GitHubProfile{ID: 321, Login: "octocat"}
	env.github.emailsErr = errors.New("github emails api failed")
	req := httptest.NewRequest(http.MethodGet, "/api/auth/github/callback?code=ok&state="+state, nil)
	w := httptest.NewRecorder()

	env.router.ServeHTTP(w, req)

	if got := w.Header().Get("Location"); got != "/login?oauth_error=github_profile_failed" {
		t.Fatalf("Location = %q", got)
	}
}

func TestGitHubOAuthCallbackRejectsNoVerifiedEmail(t *testing.T) {
	env := setupGitHubOAuthHandlerEnv(t)
	state := "state-no-email"
	_ = env.stateStore.Save(t.Context(), state, "/", time.Minute)
	env.github.profile = GitHubProfile{ID: 654, Login: "octocat"}
	env.github.emails = []GitHubEmail{{Email: "octo@example.com", Primary: true, Verified: false}}
	req := httptest.NewRequest(http.MethodGet, "/api/auth/github/callback?code=ok&state="+state, nil)
	w := httptest.NewRecorder()

	env.router.ServeHTTP(w, req)

	if got := w.Header().Get("Location"); got != "/login?oauth_error=github_no_verified_email" {
		t.Fatalf("Location = %q", got)
	}
}

func TestGitHubOAuthCallbackRejectsNewUserWhenAutoCreateDisabled(t *testing.T) {
	env := setupGitHubOAuthHandlerEnv(t)
	env.auth.GitHub.AutoCreateUsers = false
	env.router = routerForGitHubOAuthEnv(env)
	state := "state-auto-create-disabled"
	_ = env.stateStore.Save(t.Context(), state, "/", time.Minute)
	env.github.profile = GitHubProfile{ID: 987, Login: "new-user"}
	env.github.emails = []GitHubEmail{{Email: "new-user@example.com", Primary: true, Verified: true}}
	req := httptest.NewRequest(http.MethodGet, "/api/auth/github/callback?code=ok&state="+state, nil)
	w := httptest.NewRecorder()

	env.router.ServeHTTP(w, req)

	if got := w.Header().Get("Location"); got != "/login?oauth_error=github_auto_create_disabled" {
		t.Fatalf("Location = %q", got)
	}
}

func TestGitHubOAuthCallbackLinksExistingUserByEmailCaseInsensitive(t *testing.T) {
	env := setupGitHubOAuthHandlerEnv(t)
	seedGitHubOAuthSession(t, env)
	state := "state-link-email"
	_ = env.stateStore.Save(t.Context(), state, "/", time.Minute)
	env.github.profile = GitHubProfile{ID: 741, Login: "linked"}
	env.github.emails = []GitHubEmail{{Email: "ADMIN@example.com", Primary: true, Verified: true}}
	req := httptest.NewRequest(http.MethodGet, "/api/auth/github/callback?code=ok&state="+state, nil)
	w := httptest.NewRecorder()

	env.router.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302; body = %s", w.Code, w.Body.String())
	}
	identity, err := env.store.Auth().GetAuthIdentity(t.Context(), "github", "741")
	if err != nil {
		t.Fatalf("get linked identity: %v", err)
	}
	if identity.UserID != authTestUserID {
		t.Fatalf("identity user = %q, want %q", identity.UserID, authTestUserID)
	}
}

func TestGitHubOAuthCallbackRejectsDisabledMatchedUser(t *testing.T) {
	env := setupGitHubOAuthHandlerEnv(t)
	seedGitHubOAuthSession(t, env)
	if _, err := env.store.Auth().UpdateUserStatus(t.Context(), authTestUserID, "disabled"); err != nil {
		t.Fatalf("disable user: %v", err)
	}
	state := "state-disabled-user"
	_ = env.stateStore.Save(t.Context(), state, "/", time.Minute)
	env.github.profile = GitHubProfile{ID: 753, Login: "disabled"}
	env.github.emails = []GitHubEmail{{Email: "admin@example.com", Primary: true, Verified: true}}
	req := httptest.NewRequest(http.MethodGet, "/api/auth/github/callback?code=ok&state="+state, nil)
	w := httptest.NewRecorder()

	env.router.ServeHTTP(w, req)

	if got := w.Header().Get("Location"); got != "/login?oauth_error=github_create_user_failed" {
		t.Fatalf("Location = %q", got)
	}
	if cookies := w.Result().Cookies(); len(cookies) != 0 {
		t.Fatalf("unexpected cookies for disabled user: %#v", cookies)
	}
}

func TestGitHubOAuthCallbackRejectsExternalNextFromState(t *testing.T) {
	env := setupGitHubOAuthHandlerEnv(t)
	if err := env.stateStore.Save(t.Context(), "state-external", "//evil.com", time.Minute); err != nil {
		t.Fatalf("save state: %v", err)
	}
	env.github.profile = GitHubProfile{ID: 999, Login: "safe"}
	env.github.emails = []GitHubEmail{{Email: "safe@example.com", Primary: true, Verified: true}}

	req := httptest.NewRequest(http.MethodGet, "/api/auth/github/callback?code=ok&state=state-external", nil)
	w := httptest.NewRecorder()
	env.router.ServeHTTP(w, req)

	if got := w.Header().Get("Location"); got != "/" {
		t.Fatalf("Location = %q, want /", got)
	}
}

func TestGitHubOAuthCallbackDoesNotPersistAccessTokenInAuditMetadata(t *testing.T) {
	env := setupGitHubOAuthHandlerEnv(t)
	if err := env.stateStore.Save(t.Context(), "state-token", "/", time.Minute); err != nil {
		t.Fatalf("save state: %v", err)
	}
	env.github.accessToken = "github-access-token-secret"
	env.github.profile = GitHubProfile{ID: 1000, Login: "audit-safe"}
	env.github.emails = []GitHubEmail{{Email: "audit-safe@example.com", Primary: true, Verified: true}}

	req := httptest.NewRequest(http.MethodGet, "/api/auth/github/callback?code=ok&state=state-token", nil)
	w := httptest.NewRecorder()
	env.router.ServeHTTP(w, req)

	metadataJSON := allGitHubOAuthAuditMetadataJSON(t, env.dbPath)
	if strings.Contains(metadataJSON, "github-access-token-secret") {
		t.Fatalf("audit metadata contains access token: %s", metadataJSON)
	}
}

func TestGitHubOAuthCallbackUpdatesIdentityProviderFieldsOnLogin(t *testing.T) {
	env := setupGitHubOAuthHandlerEnv(t)
	seedGitHubOAuthSession(t, env)
	oldAvatar := "https://avatars.example/old.png"
	if err := env.store.Auth().CreateAuthIdentity(t.Context(), &model.AuthIdentity{
		UserID:         authTestUserID,
		Provider:       "github",
		ProviderUserID: "852",
		ProviderLogin:  "old-login",
		Email:          "old@example.com",
		AvatarURL:      &oldAvatar,
	}); err != nil {
		t.Fatalf("create identity: %v", err)
	}
	state := "state-update-identity"
	_ = env.stateStore.Save(t.Context(), state, "/", time.Minute)
	env.github.profile = GitHubProfile{ID: 852, Login: "new-login", AvatarURL: "https://avatars.example/new.png"}
	env.github.emails = []GitHubEmail{{Email: "new@example.com", Primary: true, Verified: true}}
	req := httptest.NewRequest(http.MethodGet, "/api/auth/github/callback?code=ok&state="+state, nil)
	w := httptest.NewRecorder()

	env.router.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302; body = %s", w.Code, w.Body.String())
	}
	identity, err := env.store.Auth().GetAuthIdentity(t.Context(), "github", "852")
	if err != nil {
		t.Fatalf("get updated identity: %v", err)
	}
	if identity.ProviderLogin != "new-login" || identity.Email != "new@example.com" {
		t.Fatalf("identity provider fields not updated: %+v", identity)
	}
	if identity.AvatarURL == nil || *identity.AvatarURL != "https://avatars.example/new.png" {
		t.Fatalf("avatar url = %v", identity.AvatarURL)
	}
	if identity.LastLoginAt == nil || *identity.LastLoginAt == 0 {
		t.Fatalf("last login not set: %+v", identity)
	}
}

func TestGitHubHTTPClientExchangeProfileAndEmails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/oauth/access_token":
			if r.Method != http.MethodPost {
				t.Fatalf("token method = %s, want POST", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"token-123","token_type":"bearer"}`))
		case "/user":
			if got := r.Header.Get("Authorization"); got != "Bearer token-123" {
				t.Fatalf("Authorization = %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":123,"login":"octocat","name":"Octo Cat","avatar_url":"https://avatars.example/octo.png"}`))
		case "/user/emails":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"email":"octo@example.com","primary":true,"verified":true}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewGitHubHTTPClient(config.GitHubOAuthConfig{
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		RedirectURL:  server.URL + "/callback",
	})
	client.httpClient = server.Client()
	client.tokenURL = server.URL + "/login/oauth/access_token"
	client.apiBaseURL = server.URL

	token, err := client.ExchangeCode(t.Context(), "code-123")
	if err != nil {
		t.Fatalf("exchange code: %v", err)
	}
	if token != "token-123" {
		t.Fatalf("token = %q, want token-123", token)
	}
	profile, err := client.FetchProfile(t.Context(), token)
	if err != nil {
		t.Fatalf("fetch profile: %v", err)
	}
	if profile.ID != 123 || profile.Login != "octocat" {
		t.Fatalf("profile = %+v", profile)
	}
	emails, err := client.FetchEmails(t.Context(), token)
	if err != nil {
		t.Fatalf("fetch emails: %v", err)
	}
	if len(emails) != 1 || emails[0].Email != "octo@example.com" || !emails[0].Verified {
		t.Fatalf("emails = %+v", emails)
	}
}

func TestGitHubHTTPClientReturnsProfileErrorFor5xx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "github unavailable", http.StatusBadGateway)
	}))
	defer server.Close()

	client := NewGitHubHTTPClient(config.GitHubOAuthConfig{})
	client.httpClient = server.Client()
	client.apiBaseURL = server.URL

	_, err := client.FetchProfile(t.Context(), "token-123")
	if err == nil {
		t.Fatal("expected profile fetch error")
	}
}

type githubOAuthHandlerEnv struct {
	router     *gin.Engine
	store      storage.Store
	auth       config.AuthConfig
	stateStore *authpkg.MemoryOAuthStateStore
	github     *fakeGitHubClient
	dbPath     string
}

type fakeGitHubClient struct {
	accessToken  string
	exchangeErr  error
	profile      GitHubProfile
	profileErr   error
	emails       []GitHubEmail
	emailsErr    error
	exchangeCode string
}

func (c *fakeGitHubClient) ExchangeCode(ctx context.Context, code string) (string, error) {
	c.exchangeCode = code
	if c.exchangeErr != nil {
		return "", c.exchangeErr
	}
	if c.accessToken == "" {
		return "github-access-token", nil
	}
	return c.accessToken, nil
}

func (c *fakeGitHubClient) FetchProfile(ctx context.Context, token string) (*GitHubProfile, error) {
	if c.profileErr != nil {
		return nil, c.profileErr
	}
	return &c.profile, nil
}

func (c *fakeGitHubClient) FetchEmails(ctx context.Context, token string) ([]GitHubEmail, error) {
	if c.emailsErr != nil {
		return nil, c.emailsErr
	}
	return c.emails, nil
}

func setupGitHubOAuthHandlerEnv(t *testing.T) *githubOAuthHandlerEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dbPath := filepath.Join(t.TempDir(), "github-oauth-handler.db")
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

	authCfg := config.AuthConfig{
		Cookie: config.CookieConfig{
			Name:     "fs_session",
			SameSite: "Lax",
		},
		Session: config.SessionTTLConfig{
			ShortTTL:    12 * time.Hour,
			RememberTTL: 30 * 24 * time.Hour,
		},
		SessionSecret: authTestSessionSecret,
		GitHub: config.GitHubOAuthConfig{
			Enabled:         true,
			ClientID:        "client-id",
			ClientSecret:    "client-secret",
			RedirectURL:     "https://example.com/api/auth/github/callback",
			AutoCreateUsers: true,
			StateTTL:        time.Minute,
		},
	}
	stateStore := authpkg.NewMemoryOAuthStateStore()
	github := &fakeGitHubClient{}
	env := &githubOAuthHandlerEnv{
		store:      store,
		auth:       authCfg,
		stateStore: stateStore,
		github:     github,
		dbPath:     dbPath,
	}
	env.router = routerForGitHubOAuthEnv(env)
	return env
}

func routerForGitHubOAuthEnv(env *githubOAuthHandlerEnv) *gin.Engine {
	router := gin.New()
	authRoutes := router.Group("/api/auth")
	authRoutes.GET("/providers", AuthProviders(env.auth))
	authRoutes.GET("/github/start", GitHubOAuthStart(env.store, env.auth, env.stateStore))
	authRoutes.GET("/github/callback", GitHubOAuthCallback(env.store, env.auth, env.stateStore, env.github))
	return router
}

func seedGitHubOAuthSession(t *testing.T, env *githubOAuthHandlerEnv) string {
	t.Helper()
	user := &model.User{
		ID:                 authTestUserID,
		Email:              "admin@example.com",
		DisplayName:        "Admin",
		PasswordHash:       mustGitHubOAuthPasswordHash(t),
		PasswordSet:        true,
		MustChangePassword: false,
		DefaultWorkspaceID: authTestWorkspaceID,
		Role:               "admin",
		Status:             "active",
	}
	workspace := &model.Workspace{
		ID:          authTestWorkspaceID,
		Name:        "Admin Workspace",
		OwnerUserID: user.ID,
	}
	if err := env.store.Transact(t.Context(), func(tx storage.Store) error {
		if err := tx.Auth().CreateUser(t.Context(), user); err != nil {
			return err
		}
		if err := tx.Auth().CreateWorkspace(t.Context(), workspace); err != nil {
			return err
		}
		if err := tx.Auth().SetDefaultWorkspace(t.Context(), user.ID, workspace.ID); err != nil {
			return err
		}
		return tx.Auth().AddWorkspaceMember(t.Context(), workspace.ID, user.ID, "owner")
	}); err != nil {
		t.Fatalf("seed github oauth user: %v", err)
	}

	token := "github-oauth-existing-session"
	tokenHash, err := authpkg.HashSessionToken(env.auth.SessionSecret, token)
	if err != nil {
		t.Fatalf("hash session token: %v", err)
	}
	now := time.Now().UTC()
	session := &model.Session{
		ID:          "session_github_oauth_existing",
		UserID:      user.ID,
		WorkspaceID: workspace.ID,
		TokenHash:   tokenHash,
		UserAgent:   "github-oauth-test",
		IPAddress:   "127.0.0.1",
		ExpiresAt:   now.Add(time.Hour),
		CreatedAt:   now,
		LastSeenAt:  now,
	}
	if err := env.store.Auth().CreateSession(t.Context(), session); err != nil {
		t.Fatalf("create github oauth session: %v", err)
	}
	return token
}

func mustGitHubOAuthPasswordHash(t *testing.T) string {
	t.Helper()
	hash, err := authpkg.HashPassword(authTestPassword)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	return hash
}

func mustQueryParam(t *testing.T, rawURL string, name string) string {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse URL %q: %v", rawURL, err)
	}
	value := parsed.Query().Get(name)
	if value == "" {
		t.Fatalf("query param %q missing from %q", name, rawURL)
	}
	return value
}

func allGitHubOAuthAuditMetadataJSON(t *testing.T, dbPath string) string {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath+"?_foreign_keys=ON")
	if err != nil {
		t.Fatalf("open sqlite side connection: %v", err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT metadata FROM audit_events ORDER BY created_at ASC`)
	if err != nil {
		t.Fatalf("query audit metadata: %v", err)
	}
	defer rows.Close()
	var builder strings.Builder
	for rows.Next() {
		var metadata string
		if err := rows.Scan(&metadata); err != nil {
			t.Fatalf("scan audit metadata: %v", err)
		}
		builder.WriteString(metadata)
		builder.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate audit metadata: %v", err)
	}
	return builder.String()
}
