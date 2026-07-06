package handler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/config"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/provisioning"
	"github.com/hujinrun/flowspace/internal/storage"
)

var errGitHubAutoCreateDisabled = errors.New("github oauth auto-create disabled")

type GitHubClient interface {
	ExchangeCode(ctx context.Context, code string) (string, error)
	FetchProfile(ctx context.Context, token string) (*GitHubProfile, error)
	FetchEmails(ctx context.Context, token string) ([]GitHubEmail, error)
}

type GitHubProfile struct {
	ID        int64  `json:"id"`
	Login     string `json:"login"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url"`
}

type GitHubEmail struct {
	Email    string `json:"email"`
	Primary  bool   `json:"primary"`
	Verified bool   `json:"verified"`
}

type GitHubHTTPClient struct {
	cfg        config.GitHubOAuthConfig
	httpClient *http.Client
	tokenURL   string
	apiBaseURL string
}

func NewGitHubHTTPClient(cfg config.GitHubOAuthConfig) *GitHubHTTPClient {
	return &GitHubHTTPClient{
		cfg:        cfg,
		httpClient: http.DefaultClient,
		tokenURL:   "https://github.com/login/oauth/access_token",
		apiBaseURL: "https://api.github.com",
	}
}

func (c *GitHubHTTPClient) ExchangeCode(ctx context.Context, code string) (string, error) {
	body, err := json.Marshal(map[string]string{
		"client_id":     c.cfg.ClientID,
		"client_secret": c.cfg.ClientSecret,
		"code":          code,
		"redirect_uri":  c.cfg.RedirectURL,
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	var response struct {
		AccessToken string `json:"access_token"`
	}
	if err := c.doJSON(req, &response); err != nil {
		return "", err
	}
	if strings.TrimSpace(response.AccessToken) == "" {
		return "", errors.New("github token response missing access token")
	}
	return response.AccessToken, nil
}

func (c *GitHubHTTPClient) FetchProfile(ctx context.Context, token string) (*GitHubProfile, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.apiBaseURL, "/")+"/user", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	var profile GitHubProfile
	if err := c.doJSON(req, &profile); err != nil {
		return nil, err
	}
	return &profile, nil
}

func (c *GitHubHTTPClient) FetchEmails(ctx context.Context, token string) ([]GitHubEmail, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.apiBaseURL, "/")+"/user/emails", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	var emails []GitHubEmail
	if err := c.doJSON(req, &emails); err != nil {
		return nil, err
	}
	return emails, nil
}

func (c *GitHubHTTPClient) doJSON(req *http.Request, target interface{}) error {
	client := c.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return fmt.Errorf("github request failed with status %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(target)
}

func AuthProviders(authCfg config.AuthConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		providers := []string{}
		if authCfg.GitHub.Available() {
			providers = append(providers, "github")
		}
		success(c, gin.H{"providers": providers})
	}
}

func GitHubOAuthStart(store storage.Store, authCfg config.AuthConfig, stateStore auth.OAuthStateStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !authCfg.GitHub.Available() {
			c.Redirect(http.StatusFound, "/login?oauth_error=github_disabled")
			return
		}
		next := auth.SanitizeOAuthNext(c.Query("next"))
		if hasValidSession(c.Request.Context(), store, authCfg, c.Request) {
			c.Redirect(http.StatusFound, next)
			return
		}
		if stateStore == nil {
			c.Redirect(http.StatusFound, "/login?oauth_error=github_state_invalid")
			return
		}
		state, err := auth.GenerateSessionToken()
		if err != nil {
			c.Redirect(http.StatusFound, "/login?oauth_error=github_state_invalid")
			return
		}
		if err := stateStore.Save(c.Request.Context(), state, next, githubStateTTL(authCfg.GitHub)); err != nil {
			c.Redirect(http.StatusFound, "/login?oauth_error=github_state_invalid")
			return
		}
		c.Redirect(http.StatusFound, githubAuthorizeURL(authCfg.GitHub, state))
	}
}

func GitHubOAuthCallback(store storage.Store, authCfg config.AuthConfig, stateStore auth.OAuthStateStore, client GitHubClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !authCfg.GitHub.Available() {
			c.Redirect(http.StatusFound, "/login?oauth_error=github_disabled")
			return
		}
		if store == nil || stateStore == nil || client == nil {
			c.Redirect(http.StatusFound, "/login?oauth_error=github_state_invalid")
			return
		}
		next, err := stateStore.Consume(c.Request.Context(), c.Query("state"))
		if err != nil {
			c.Redirect(http.StatusFound, "/login?oauth_error=github_state_invalid")
			return
		}
		token, err := client.ExchangeCode(c.Request.Context(), c.Query("code"))
		if err != nil {
			c.Redirect(http.StatusFound, "/login?oauth_error=github_exchange_failed")
			return
		}
		profile, err := client.FetchProfile(c.Request.Context(), token)
		if err != nil {
			c.Redirect(http.StatusFound, "/login?oauth_error=github_profile_failed")
			return
		}
		emails, err := client.FetchEmails(c.Request.Context(), token)
		if err != nil {
			c.Redirect(http.StatusFound, "/login?oauth_error=github_profile_failed")
			return
		}
		email, ok := chooseVerifiedGitHubEmail(emails)
		if !ok {
			c.Redirect(http.StatusFound, "/login?oauth_error=github_no_verified_email")
			return
		}
		user, workspaceID, err := resolveGitHubUser(c.Request.Context(), store, authCfg, profile, email)
		if err != nil {
			c.Redirect(http.StatusFound, oauthCreateError(err))
			return
		}
		if err := revokeExistingSessionFromCookie(c.Request.Context(), store, authCfg, c.Request); err != nil {
			c.Redirect(http.StatusFound, "/login?oauth_error=github_create_user_failed")
			return
		}
		tokenValue, session, err := createOAuthSession(c, store, authCfg, user.ID, workspaceID)
		if err != nil {
			c.Redirect(http.StatusFound, "/login?oauth_error=github_create_user_failed")
			return
		}
		http.SetCookie(c.Writer, activeSessionCookie(authCfg.Cookie, tokenValue, sessionTTL(authCfg, true), session.ExpiresAt))
		c.Redirect(http.StatusFound, auth.SanitizeOAuthNext(next))
	}
}

func githubAuthorizeURL(cfg config.GitHubOAuthConfig, state string) string {
	values := url.Values{}
	values.Set("client_id", cfg.ClientID)
	values.Set("redirect_uri", cfg.RedirectURL)
	values.Set("scope", "read:user user:email")
	values.Set("state", state)
	return "https://github.com/login/oauth/authorize?" + values.Encode()
}

func githubStateTTL(cfg config.GitHubOAuthConfig) time.Duration {
	if cfg.StateTTL > 0 {
		return cfg.StateTTL
	}
	return 10 * time.Minute
}

func hasValidSession(ctx context.Context, store storage.Store, authCfg config.AuthConfig, req *http.Request) bool {
	if store == nil || req == nil {
		return false
	}
	cookie, err := req.Cookie(authCookieName(authCfg.Cookie))
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return false
	}
	tokenHash, err := auth.HashSessionToken(authCfg.SessionSecret, cookie.Value)
	if err != nil {
		return false
	}
	session, err := store.Auth().GetSessionByTokenHash(ctx, tokenHash)
	if err != nil {
		return false
	}
	user, err := store.Auth().GetUserByID(ctx, session.UserID)
	if err != nil || user.Status != "active" {
		return false
	}
	if _, err := store.Auth().GetWorkspaceMembership(ctx, session.WorkspaceID, session.UserID); err != nil {
		return false
	}
	return true
}

func chooseVerifiedGitHubEmail(emails []GitHubEmail) (string, bool) {
	for _, email := range emails {
		if email.Primary && email.Verified && strings.TrimSpace(email.Email) != "" {
			return strings.TrimSpace(email.Email), true
		}
	}
	for _, email := range emails {
		if email.Verified && strings.TrimSpace(email.Email) != "" {
			return strings.TrimSpace(email.Email), true
		}
	}
	return "", false
}

func resolveGitHubUser(ctx context.Context, store storage.Store, authCfg config.AuthConfig, profile *GitHubProfile, email string) (*model.User, string, error) {
	if profile == nil {
		return nil, "", errors.New("missing github profile")
	}
	providerUserID := strconv.FormatInt(profile.ID, 10)
	identity := githubIdentityFromProfile(profile, email)
	loginAt := time.Now().UTC()

	existingIdentity, err := store.Auth().GetAuthIdentity(ctx, "github", providerUserID)
	if err == nil {
		identity.ID = existingIdentity.ID
		identity.UserID = existingIdentity.UserID
		if err := store.Auth().UpdateAuthIdentityFromProvider(ctx, identity, loginAt); err != nil {
			return nil, "", err
		}
		user, err := store.Auth().GetUserByID(ctx, existingIdentity.UserID)
		if err != nil {
			return nil, "", err
		}
		workspaceID, err := validatedGitHubWorkspace(ctx, store, user)
		return user, workspaceID, err
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, "", err
	}

	user, err := store.Auth().GetUserByEmail(ctx, email)
	if err == nil {
		workspaceID, err := validatedGitHubWorkspace(ctx, store, user)
		if err != nil {
			return nil, "", err
		}
		identity.UserID = user.ID
		if err := store.Auth().CreateAuthIdentity(ctx, identity); err != nil {
			return nil, "", err
		}
		return user, workspaceID, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, "", err
	}
	if !authCfg.GitHub.AutoCreateUsers {
		return nil, "", errGitHubAutoCreateDisabled
	}
	return createGitHubUser(ctx, store, profile, email, identity)
}

func validatedGitHubWorkspace(ctx context.Context, store storage.Store, user *model.User) (string, error) {
	if user == nil {
		return "", errors.New("missing user")
	}
	if user.Status != "active" {
		return "", errors.New("account disabled")
	}
	workspaceID := strings.TrimSpace(user.DefaultWorkspaceID)
	if workspaceID == "" {
		return "", errors.New("default workspace missing")
	}
	if _, err := store.Auth().GetWorkspaceMembership(ctx, workspaceID, user.ID); err != nil {
		return "", err
	}
	return workspaceID, nil
}

func githubIdentityFromProfile(profile *GitHubProfile, email string) *model.AuthIdentity {
	identity := &model.AuthIdentity{
		Provider:       "github",
		ProviderUserID: strconv.FormatInt(profile.ID, 10),
		ProviderLogin:  strings.TrimSpace(profile.Login),
		Email:          strings.TrimSpace(email),
	}
	if strings.TrimSpace(profile.AvatarURL) != "" {
		avatarURL := strings.TrimSpace(profile.AvatarURL)
		identity.AvatarURL = &avatarURL
	}
	return identity
}

func createGitHubUser(ctx context.Context, store storage.Store, profile *GitHubProfile, email string, identity *model.AuthIdentity) (*model.User, string, error) {
	passwordToken, err := auth.GenerateSessionToken()
	if err != nil {
		return nil, "", err
	}
	passwordHash, err := auth.HashPassword(passwordToken + "A1")
	if err != nil {
		return nil, "", err
	}
	displayName := githubDisplayName(profile, email)
	user := &model.User{
		Email:              strings.TrimSpace(email),
		DisplayName:        displayName,
		PasswordHash:       passwordHash,
		PasswordSet:        false,
		MustChangePassword: false,
		Role:               "user",
		Status:             "active",
	}
	workspace := &model.Workspace{
		Name:        displayName + " Workspace",
		OwnerUserID: user.ID,
	}

	err = store.Transact(ctx, func(tx storage.Store) error {
		if err := tx.Auth().CreateUser(ctx, user); err != nil {
			return err
		}
		workspace.OwnerUserID = user.ID
		if err := tx.Auth().CreateWorkspace(ctx, workspace); err != nil {
			return err
		}
		if err := tx.Auth().SetDefaultWorkspace(ctx, user.ID, workspace.ID); err != nil {
			return err
		}
		user.DefaultWorkspaceID = workspace.ID
		if err := tx.Auth().AddWorkspaceMember(ctx, workspace.ID, user.ID, "owner"); err != nil {
			return err
		}
		targetCtx := auth.ContextWithWorkspaceScope(ctx, workspace.ID)
		if err := provisioning.EnsureDefaultWorkspaceData(targetCtx, tx); err != nil {
			return err
		}
		identity.UserID = user.ID
		if err := tx.Auth().CreateAuthIdentity(ctx, identity); err != nil {
			return err
		}
		if err := tx.Auth().RecordAuditEvent(ctx, &model.AuditEvent{
			TargetUserID: stringPtr(user.ID),
			WorkspaceID:  stringPtr(workspace.ID),
			Action:       "auth.user_auto_created",
			Metadata:     map[string]any{"provider": "github", "email": user.Email},
		}); err != nil {
			return err
		}
		if err := tx.Auth().RecordAuditEvent(ctx, &model.AuditEvent{
			TargetUserID: stringPtr(user.ID),
			WorkspaceID:  stringPtr(workspace.ID),
			Action:       "auth.workspace_auto_created",
			Metadata:     map[string]any{"provider": "github"},
		}); err != nil {
			return err
		}
		return tx.Auth().RecordAuditEvent(ctx, &model.AuditEvent{
			TargetUserID: stringPtr(user.ID),
			WorkspaceID:  stringPtr(workspace.ID),
			Action:       "auth.identity_linked",
			Metadata: map[string]any{
				"provider":         "github",
				"provider_user_id": identity.ProviderUserID,
			},
		})
	})
	if err != nil {
		return nil, "", err
	}
	return user, workspace.ID, nil
}

func githubDisplayName(profile *GitHubProfile, email string) string {
	for _, value := range []string{profile.Name, profile.Login, email} {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return "GitHub User"
}

func revokeExistingSessionFromCookie(ctx context.Context, store storage.Store, authCfg config.AuthConfig, req *http.Request) error {
	if store == nil || req == nil {
		return nil
	}
	cookie, err := req.Cookie(authCookieName(authCfg.Cookie))
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return nil
	}
	tokenHash, err := auth.HashSessionToken(authCfg.SessionSecret, cookie.Value)
	if err != nil {
		return nil
	}
	session, err := store.Auth().GetSessionByTokenHash(ctx, tokenHash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return store.Auth().RevokeSession(ctx, session.ID)
}

func createOAuthSession(c *gin.Context, store storage.Store, authCfg config.AuthConfig, userID, workspaceID string) (string, *model.Session, error) {
	token, err := auth.GenerateSessionToken()
	if err != nil {
		return "", nil, err
	}
	tokenHash, err := auth.HashSessionToken(authCfg.SessionSecret, token)
	if err != nil {
		return "", nil, err
	}
	ttl := sessionTTL(authCfg, true)
	now := time.Now().UTC()
	session := &model.Session{
		UserID:      userID,
		WorkspaceID: workspaceID,
		TokenHash:   tokenHash,
		UserAgent:   c.Request.UserAgent(),
		IPAddress:   c.ClientIP(),
		ExpiresAt:   now.Add(ttl),
		CreatedAt:   now,
		LastSeenAt:  now,
	}
	ctx := c.Request.Context()
	err = store.Transact(ctx, func(tx storage.Store) error {
		if err := tx.Auth().CreateSession(ctx, session); err != nil {
			return err
		}
		if err := tx.Auth().UpdateUserLastLogin(ctx, userID, now); err != nil {
			return err
		}
		return tx.Auth().RecordAuditEvent(ctx, &model.AuditEvent{
			ActorUserID:  stringPtr(userID),
			TargetUserID: stringPtr(userID),
			WorkspaceID:  stringPtr(workspaceID),
			Action:       "auth.login.github",
			Metadata:     authAuditMetadata(c),
		})
	})
	if err != nil {
		return "", nil, err
	}
	return token, session, nil
}

func oauthCreateError(err error) string {
	if errors.Is(err, errGitHubAutoCreateDisabled) {
		return "/login?oauth_error=github_auto_create_disabled"
	}
	return "/login?oauth_error=github_create_user_failed"
}
