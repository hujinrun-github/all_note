package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const sessionSecretMinBytes = 32

type BootstrapAdminConfig struct {
	Email    string
	Password string
	Name     string
}

type CookieConfig struct {
	Name     string
	Secure   bool
	SameSite string
}

type SessionTTLConfig struct {
	ShortTTL    time.Duration
	RememberTTL time.Duration
}

type LoginThrottleConfig struct {
	MaxFailures int
	Window      time.Duration
}

type SessionCleanupConfig struct {
	Interval time.Duration
}

type GitHubOAuthConfig struct {
	Enabled              bool
	ClientID             string
	ClientSecret         string
	RedirectURL          string
	AutoCreateUsers      bool
	StateTTL             time.Duration
	AllowedRedirectHosts []string
}

func (cfg GitHubOAuthConfig) Available() bool {
	return cfg.Enabled &&
		strings.TrimSpace(cfg.ClientID) != "" &&
		strings.TrimSpace(cfg.ClientSecret) != "" &&
		strings.TrimSpace(cfg.RedirectURL) != ""
}

type AuthConfig struct {
	Bootstrap                   BootstrapAdminConfig
	Cookie                      CookieConfig
	Session                     SessionTTLConfig
	SessionSecret               string
	AllowedOrigins              []string
	LoginThrottle               LoginThrottleConfig
	SessionCleanup              SessionCleanupConfig
	EnableLocalDirectoryBrowser bool
	AllowedLocalRoots           []string
	GitHub                      GitHubOAuthConfig
}

func LoadAuthConfig(environment string) (AuthConfig, error) {
	environment = normalizeEnvironment(environment)
	cookieSecure, err := envBool("FLOWSPACE_COOKIE_SECURE", environment == EnvironmentProduction)
	if err != nil {
		return AuthConfig{}, err
	}
	sessionTTL, err := envDuration("FLOWSPACE_SESSION_TTL", 12*time.Hour)
	if err != nil {
		return AuthConfig{}, err
	}
	rememberSessionTTL, err := envDuration("FLOWSPACE_REMEMBER_SESSION_TTL", 30*24*time.Hour)
	if err != nil {
		return AuthConfig{}, err
	}
	loginMaxFailures, err := envInt("FLOWSPACE_LOGIN_MAX_FAILURES", 5)
	if err != nil {
		return AuthConfig{}, err
	}
	loginWindow, err := envDuration("FLOWSPACE_LOGIN_WINDOW", 15*time.Minute)
	if err != nil {
		return AuthConfig{}, err
	}
	sessionCleanupInterval, err := envDuration("FLOWSPACE_SESSION_CLEANUP_INTERVAL", time.Hour)
	if err != nil {
		return AuthConfig{}, err
	}
	enableLocalDirectoryBrowser, err := envBool("FLOWSPACE_ENABLE_LOCAL_DIRECTORY_BROWSER", false)
	if err != nil {
		return AuthConfig{}, err
	}
	githubEnabled, err := envBool("AUTH_GITHUB_ENABLED", false)
	if err != nil {
		return AuthConfig{}, err
	}
	githubAutoCreateUsers, err := envBool("AUTH_GITHUB_AUTO_CREATE_USERS", false)
	if err != nil {
		return AuthConfig{}, err
	}
	githubStateTTL, err := envDuration("AUTH_GITHUB_STATE_TTL", 10*time.Minute)
	if err != nil {
		return AuthConfig{}, err
	}
	githubAllowedRedirectHosts, err := splitStrictCSVAllowEmpty("AUTH_GITHUB_ALLOWED_REDIRECT_HOSTS", os.Getenv("AUTH_GITHUB_ALLOWED_REDIRECT_HOSTS"))
	if err != nil {
		return AuthConfig{}, err
	}
	allowedOrigins, err := envAllowedOrigins("FLOWSPACE_ALLOWED_ORIGINS")
	if err != nil {
		return AuthConfig{}, err
	}

	cfg := AuthConfig{
		Bootstrap: BootstrapAdminConfig{
			Email:    strings.TrimSpace(os.Getenv("FLOWSPACE_BOOTSTRAP_ADMIN_EMAIL")),
			Password: os.Getenv("FLOWSPACE_BOOTSTRAP_ADMIN_PASSWORD"),
			Name:     strings.TrimSpace(os.Getenv("FLOWSPACE_BOOTSTRAP_ADMIN_NAME")),
		},
		Cookie: CookieConfig{
			Name:     "fs_session",
			Secure:   cookieSecure,
			SameSite: "Lax",
		},
		Session: SessionTTLConfig{
			ShortTTL:    sessionTTL,
			RememberTTL: rememberSessionTTL,
		},
		SessionSecret:               strings.TrimSpace(os.Getenv("FLOWSPACE_SESSION_SECRET")),
		AllowedOrigins:              allowedOrigins,
		LoginThrottle:               LoginThrottleConfig{MaxFailures: loginMaxFailures, Window: loginWindow},
		SessionCleanup:              SessionCleanupConfig{Interval: sessionCleanupInterval},
		EnableLocalDirectoryBrowser: enableLocalDirectoryBrowser,
		AllowedLocalRoots:           splitCSV(os.Getenv("FLOWSPACE_ALLOWED_LOCAL_ROOTS")),
		GitHub: GitHubOAuthConfig{
			Enabled:              githubEnabled,
			ClientID:             strings.TrimSpace(os.Getenv("AUTH_GITHUB_CLIENT_ID")),
			ClientSecret:         strings.TrimSpace(os.Getenv("AUTH_GITHUB_CLIENT_SECRET")),
			RedirectURL:          strings.TrimSpace(os.Getenv("AUTH_GITHUB_REDIRECT_URL")),
			AutoCreateUsers:      githubAutoCreateUsers,
			StateTTL:             githubStateTTL,
			AllowedRedirectHosts: githubAllowedRedirectHosts,
		},
	}
	if environment == EnvironmentProduction && cfg.SessionSecret == "" {
		return AuthConfig{}, errors.New("FLOWSPACE_SESSION_SECRET is required in prod")
	}
	if environment == EnvironmentProduction && len([]byte(cfg.SessionSecret)) < sessionSecretMinBytes {
		return AuthConfig{}, fmt.Errorf("FLOWSPACE_SESSION_SECRET must be at least %d bytes in prod", sessionSecretMinBytes)
	}
	if environment == EnvironmentProduction && !cfg.Cookie.Secure {
		return AuthConfig{}, errors.New("FLOWSPACE_COOKIE_SECURE must be true in prod")
	}
	if environment == EnvironmentProduction && len(cfg.AllowedOrigins) == 0 {
		return AuthConfig{}, errors.New("FLOWSPACE_ALLOWED_ORIGINS is required in prod")
	}
	if cfg.SessionSecret == "" {
		cfg.SessionSecret = "dev-only-session-secret"
	}
	if len(cfg.AllowedOrigins) == 0 {
		cfg.AllowedOrigins = []string{"http://localhost:5173"}
	}
	return cfg, nil
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func envAllowedOrigins(name string) ([]string, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return nil, nil
	}
	origins, err := splitStrictCSV(name, value)
	if err != nil {
		return nil, err
	}
	if len(origins) == 0 {
		return nil, fmt.Errorf("%s must include at least one origin", name)
	}
	for i, origin := range origins {
		normalized, err := normalizeAllowedOrigin(origin)
		if err != nil {
			return nil, fmt.Errorf("%s contains invalid origin %q: %w", name, origin, err)
		}
		origins[i] = normalized
	}
	return origins, nil
}

func splitStrictCSV(name, value string) ([]string, error) {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("%s must not contain empty CSV segments", name)
		}
		out = append(out, part)
	}
	return out, nil
}

func splitStrictCSVAllowEmpty(name, value string) ([]string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	return splitStrictCSV(name, value)
}

func validateAllowedOrigin(origin string) error {
	_, err := normalizeAllowedOrigin(origin)
	return err
}

func normalizeAllowedOrigin(origin string) (string, error) {
	if strings.Contains(origin, "*") {
		return "", errors.New("wildcard origins are not allowed")
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return "", err
	}
	if !parsed.IsAbs() || parsed.Host == "" || parsed.Hostname() == "" {
		return "", errors.New("origin must be an absolute URL with scheme and host")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("origin scheme must be http or https")
	}
	if !isValidOriginHost(parsed.Hostname()) {
		return "", errors.New("origin host is malformed")
	}
	if parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawPath != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("origin must not include userinfo, path, query, or fragment")
	}
	if err := validateOriginPort(parsed.Host, parsed.Port()); err != nil {
		return "", err
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}

func isValidOriginHost(hostname string) bool {
	if hostname == "localhost" || net.ParseIP(hostname) != nil {
		return true
	}
	if isDottedNumericHost(hostname) {
		return false
	}
	labels := strings.Split(hostname, ".")
	for _, label := range labels {
		if label == "" || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, ch := range label {
			if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' {
				continue
			}
			return false
		}
	}
	return true
}

func isDottedNumericHost(hostname string) bool {
	if !strings.Contains(hostname, ".") {
		return false
	}
	for _, ch := range hostname {
		if (ch < '0' || ch > '9') && ch != '.' {
			return false
		}
	}
	return true
}

func validateOriginPort(host, port string) error {
	if hasPortSeparator(host) && port == "" {
		return errors.New("origin port must be numeric")
	}
	if port == "" {
		return nil
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 0 || portNumber > 65535 {
		return errors.New("origin port must be between 0 and 65535")
	}
	return nil
}

func hasPortSeparator(host string) bool {
	if strings.HasPrefix(host, "[") {
		closingBracket := strings.LastIndex(host, "]")
		return closingBracket >= 0 && strings.HasPrefix(host[closingBracket+1:], ":")
	}
	return strings.Contains(host, ":")
}

func envBool(name string, fallback bool) (bool, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean: %w", name, err)
	}
	return parsed, nil
}

func envInt(name string, fallback int) (int, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("%s must be positive", name)
	}
	return parsed, nil
}

func envDuration(name string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration: %w", name, err)
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("%s must be positive", name)
	}
	return parsed, nil
}
