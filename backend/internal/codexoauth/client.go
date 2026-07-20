package codexoauth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	ClientID          = "app_EMoamEEZ73f0CkXaXp7hrann"
	DefaultIssuer     = "https://auth.openai.com"
	DefaultTokenURL   = "https://auth.openai.com/oauth/token"
	DefaultBackendURL = "https://chatgpt.com/backend-api/codex"
)

type DeviceAuthorization struct {
	DeviceAuthID, UserCode, VerificationURL string
	Interval                                time.Duration
	ExpiresAt                               time.Time
}
type AuthorizationGrant struct{ Code, Verifier string }
type Tokens struct{ AccessToken, RefreshToken, IDToken, AccountID string }
type Client struct {
	http             *http.Client
	issuer, tokenURL string
}

func NewClient(httpClient *http.Client, issuer, tokenURL string) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	return &Client{http: httpClient, issuer: strings.TrimRight(issuer, "/"), tokenURL: tokenURL}
}

func (c *Client) Start(ctx context.Context) (DeviceAuthorization, error) {
	body, _ := json.Marshal(map[string]string{"client_id": ClientID})
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.issuer+"/api/accounts/deviceauth/usercode", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return DeviceAuthorization{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return DeviceAuthorization{}, fmt.Errorf("Codex device authorization returned HTTP %d", response.StatusCode)
	}
	var payload struct {
		UserCode     string `json:"user_code"`
		DeviceAuthID string `json:"device_auth_id"`
		Interval     any    `json:"interval"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if json.NewDecoder(response.Body).Decode(&payload) != nil || payload.UserCode == "" || payload.DeviceAuthID == "" {
		return DeviceAuthorization{}, errors.New("invalid Codex device authorization response")
	}
	interval := 5
	switch value := payload.Interval.(type) {
	case float64:
		interval = int(value)
	case string:
		interval, _ = strconv.Atoi(value)
	}
	if interval < 3 {
		interval = 3
	}
	expires := payload.ExpiresIn
	if expires <= 0 || expires > 900 {
		expires = 900
	}
	return DeviceAuthorization{DeviceAuthID: payload.DeviceAuthID, UserCode: payload.UserCode, VerificationURL: c.issuer + "/codex/device", Interval: time.Duration(interval) * time.Second, ExpiresAt: time.Now().Add(time.Duration(expires) * time.Second)}, nil
}

func (c *Client) Poll(ctx context.Context, deviceAuthID, userCode string) (AuthorizationGrant, bool, error) {
	body, _ := json.Marshal(map[string]string{"device_auth_id": deviceAuthID, "user_code": userCode})
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.issuer+"/api/accounts/deviceauth/token", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return AuthorizationGrant{}, false, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusNotFound {
		return AuthorizationGrant{}, true, nil
	}
	if response.StatusCode != http.StatusOK {
		return AuthorizationGrant{}, false, fmt.Errorf("Codex device polling returned HTTP %d", response.StatusCode)
	}
	var payload struct {
		Code     string `json:"authorization_code"`
		Verifier string `json:"code_verifier"`
	}
	if json.NewDecoder(response.Body).Decode(&payload) != nil || payload.Code == "" || payload.Verifier == "" {
		return AuthorizationGrant{}, false, errors.New("invalid Codex authorization grant")
	}
	return AuthorizationGrant{Code: payload.Code, Verifier: payload.Verifier}, false, nil
}

func (c *Client) Exchange(ctx context.Context, grant AuthorizationGrant) (Tokens, error) {
	form := url.Values{"grant_type": {"authorization_code"}, "code": {grant.Code}, "redirect_uri": {c.issuer + "/deviceauth/callback"}, "client_id": {ClientID}, "code_verifier": {grant.Verifier}}
	return c.tokenRequest(ctx, form)
}

func (c *Client) Refresh(ctx context.Context, refreshToken string) (Tokens, error) {
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}, "client_id": {ClientID}}
	tokens, err := c.tokenRequest(ctx, form)
	if err == nil && tokens.RefreshToken == "" {
		tokens.RefreshToken = refreshToken
	}
	return tokens, err
}

func (c *Client) tokenRequest(ctx context.Context, form url.Values) (Tokens, error) {
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := c.http.Do(request)
	if err != nil {
		return Tokens{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Tokens{}, fmt.Errorf("Codex token exchange returned HTTP %d", response.StatusCode)
	}
	var payload struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
		IDToken string `json:"id_token"`
	}
	if json.NewDecoder(response.Body).Decode(&payload) != nil || payload.Access == "" {
		return Tokens{}, errors.New("invalid Codex token response")
	}
	return Tokens{AccessToken: payload.Access, RefreshToken: payload.Refresh, IDToken: payload.IDToken, AccountID: accountIDFromJWT(payload.IDToken)}, nil
}

func accountIDFromJWT(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Auth struct {
			AccountID string `json:"chatgpt_account_id"`
		} `json:"https://api.openai.com/auth"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return ""
	}
	return strings.TrimSpace(claims.Auth.AccountID)
}
