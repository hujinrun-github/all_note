# Multi-User Auth Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement login, session auth, account management, and workspace-based data isolation for FlowSpace, with safe legacy migration for PostgreSQL and SQLite.

**Architecture:** Add auth/session/account modules beside existing handlers and storage providers, then make all business repositories resolve data through a `WorkspaceScope` rather than global queries. Migrations run in two parts: SQL adds auth tables and nullable workspace columns, Go bootstrap/backfill creates the first admin workspace and assigns legacy data, and explicit post-backfill finalizers apply NOT NULL, composite keys, composite FKs, and SQLite FTS rebuilds.

**Tech Stack:** Go 1.26, Gin, database/sql, PostgreSQL via pgx, SQLite via modernc.org/sqlite, bcrypt, React 19, React Router, TanStack Query, Vitest

---

## Source Spec

- `docs/superpowers/specs/2026-06-24-multi-user-auth-design.md`

## Execution Rules

- Create a feature branch before implementation: `git switch -c codex/multi-user-auth`.
- Work in small commits. Every task below ends with a commit.
- Follow strict TDD for every production change: RED test first, verify RED, write minimal GREEN implementation, verify GREEN, refactor only after GREEN.
- If a test passes on the first run, it is not a RED test for that change. Rewrite the test so it fails for the intended missing behavior before touching production code.
- If production code is written before its RED test, delete that production change and restart the task from the RED step.
- Keep `FLOWSPACE_BOOTSTRAP_ADMIN_PASSWORD` out of SQL and out of audit metadata.
- Every audit metadata write must pass through `auth.SanitizeAuditMetadata`; never store temporary passwords, password hashes, cookies, bearer tokens, session tokens, or authorization headers.
- Hash session tokens with HMAC-SHA256 using `FLOWSPACE_SESSION_SECRET`; production startup must fail without this secret.
- Use `FLOWSPACE_ALLOWED_ORIGINS` for CORS and CSRF origin checks; do not keep hardcoded localhost in middleware.
- Never let a repository query fall back to global data when `WorkspaceScope` is absent.
- Do not expose `/api/system/directories` unless `FLOWSPACE_ENABLE_LOCAL_DIRECTORY_BROWSER=true`.
- Do not allow Phase 1 auth endpoints to ship without Phase 2 workspace isolation in a multi-user deployment.
- Run provider contract tests for both PostgreSQL and SQLite after every storage task.

---

## TDD Protocol

Every task that changes production code must follow this exact loop:

1. RED: add one focused failing test for the behavior in that step.
2. Verify RED: run only that test and confirm the failure is because the behavior is missing, not because of a typo or broken test setup.
3. GREEN: write the smallest production change that can make that test pass.
4. Verify GREEN: rerun the focused test, then the narrow package or frontend suite listed in the task.
5. Refactor: clean up only after GREEN, then rerun the same tests.
6. Commit: commit only when the task-specific tests pass.

Tasks that only run baseline checks or create rollout notes do not change production code; they still run verification before commit.

---

## File Map

| File | Responsibility | Change |
| --- | --- | --- |
| `backend/internal/model/auth.go` | User, workspace, session, audit, auth/admin request models | Create |
| `backend/internal/auth/context.go` | `RequestIdentity`, `WorkspaceScope`, context helpers | Create |
| `backend/internal/auth/password.go` | bcrypt hash/verify and password policy | Create |
| `backend/internal/auth/session.go` | session token generation and HMAC token hashing | Create |
| `backend/internal/auth/throttle.go` | login failure throttling | Create |
| `backend/internal/auth/audit.go` | audit metadata sanitizer | Create |
| `backend/internal/auth/errors.go` | shared auth errors and error codes | Create |
| `backend/internal/storage/store.go` | Store interface and repository interfaces | Add `Auth()`, `UserListFilter`, workspace-aware contracts |
| `backend/internal/storage/contracttest/auth_contract_tests.go` | Auth storage contract tests | Create |
| `backend/internal/storage/contracttest/workspace_isolation_contract_tests.go` | Cross-workspace isolation tests | Create |
| `backend/internal/storage/postgres/auth.go` | PostgreSQL AuthRepository | Create |
| `backend/internal/storage/sqlite/auth.go` | SQLite AuthRepository | Create |
| `backend/db/migrations/postgres/0004_multi_user_auth_schema.sql` | PostgreSQL auth schema and nullable workspace columns | Create |
| `backend/internal/storage/postgres/auth_migrations.go` | Postgres backfill/finalizer helpers | Create |
| `backend/internal/storage/sqlite/auth_migrations.go` | SQLite table rebuild, backfill, FTS rebuild | Create |
| `backend/internal/bootstrap/auth_bootstrap.go` | bootstrap admin and legacy data assignment | Create |
| `backend/internal/middleware/auth.go` | session restore, admin gate, password-settled gate | Create |
| `backend/internal/middleware/csrf.go` | Origin/Referer CSRF check for mutating requests | Create |
| `backend/internal/middleware/cors.go` | allowed-origin CORS configuration | Modify |
| `backend/internal/handler/auth.go` | login, logout, me, change password | Create |
| `backend/internal/handler/admin_users.go` | admin list/create/update/reset/enable/disable | Create |
| `backend/internal/router/router.go` | public/auth/protected/admin route groups | Modify |
| `backend/internal/config/auth.go` | cookie, bootstrap, local directory browser config | Create |
| `backend/internal/service/inbox.go` | workspace-scoped transactional inbox conversion | Modify |
| `backend/internal/service/sync_dispatch.go` | require workspace scope for sync entry points | Modify |
| `backend/internal/service/notes.go` | pass ctx/store through note service paths | Modify |
| `backend/internal/service/tasks.go` | pass ctx/store through task service paths | Modify |
| `backend/internal/service/events.go` | pass ctx/store through event service paths | Modify |
| `backend/internal/service/session_cleanup.go` | periodic expired session deletion worker | Create |
| `frontend/src/api/auth.ts` | auth and account API client | Create |
| `frontend/src/api/client.ts` | credentials-included fetch and central 401 handling | Modify |
| `frontend/src/hooks/useAuth.tsx` | auth provider, session restore, logout | Create |
| `frontend/src/components/auth/ProtectedRoute.tsx` | protected route gate | Create |
| `frontend/src/components/auth/AdminRoute.tsx` | admin route gate | Create |
| `frontend/src/routes/Login.tsx` | real login form integration | Modify |
| `frontend/src/routes/ChangePassword.tsx` | forced password change screen | Create |
| `frontend/src/routes/AccountAdmin.tsx` | user management UI | Create |
| `frontend/src/router.tsx` | protected shell and admin route | Modify |
| `frontend/src/components/layout/TopBar.tsx` | account menu and logout | Modify |
| `frontend/src/components/layout/Sidebar.tsx` | admin users nav only for admins | Modify |

---

## Phase 0: Baseline

### Task 0: Create Branch And Capture Baseline

**Files:**
- No file changes expected

- [ ] **Step 1: Create implementation branch**

```bash
git switch -c codex/multi-user-auth
```

Expected: current branch becomes `codex/multi-user-auth`.

- [ ] **Step 2: Run backend baseline tests**

```bash
cd backend && go test ./...
```

Expected: existing backend packages pass before auth work begins. Do not name packages that will be created later; their RED tests are introduced in the task that creates them.

- [ ] **Step 3: Run frontend baseline tests**

```bash
cd frontend && npm run test
```

Expected: existing frontend unit tests pass before auth work begins.

- [ ] **Step 4: Confirm baseline did not change files**

Run:

```bash
git status --short
```

Expected: no output. If there is output, stop and inspect before starting Task 1.

---

## Phase 1: Auth Core Types

### Task 1: Add Auth Models And Context Helpers

**Files:**
- Create: `backend/internal/model/auth.go`
- Create: `backend/internal/auth/context.go`
- Create: `backend/internal/auth/errors.go`
- Test: `backend/internal/auth/context_test.go`

- [ ] **Step 1: Write failing context tests**

Create `backend/internal/auth/context_test.go`:

```go
package auth

import (
	"context"
	"errors"
	"testing"
)

func TestIdentityAndWorkspaceScopeAreSeparate(t *testing.T) {
	base := context.Background()
	identity := RequestIdentity{
		UserID:             "user_admin",
		SessionID:          "session_admin",
		WorkspaceID:        "workspace_admin",
		Role:               "admin",
		MustChangePassword: false,
	}

	ctx := ContextWithIdentity(base, identity)
	ctx = ContextWithWorkspaceScope(ctx, "workspace_target")

	gotIdentity, ok := IdentityFromContext(ctx)
	if !ok {
		t.Fatal("expected identity in context")
	}
	if gotIdentity.WorkspaceID != "workspace_admin" {
		t.Fatalf("identity workspace = %q, want workspace_admin", gotIdentity.WorkspaceID)
	}
	if gotIdentity.SessionID != "session_admin" {
		t.Fatalf("identity session = %q, want session_admin", gotIdentity.SessionID)
	}

	scope, err := WorkspaceIDFromContext(ctx)
	if err != nil {
		t.Fatalf("workspace scope: %v", err)
	}
	if scope != "workspace_target" {
		t.Fatalf("scope = %q, want workspace_target", scope)
	}
}

func TestWorkspaceIDFromContextMissing(t *testing.T) {
	_, err := WorkspaceIDFromContext(context.Background())
	if !errors.Is(err, ErrMissingWorkspace) {
		t.Fatalf("expected ErrMissingWorkspace, got %v", err)
	}
}
```

- [ ] **Step 2: Run failing test**

```bash
cd backend && go test ./internal/auth -run TestIdentityAndWorkspaceScopeAreSeparate -count=1 -v
```

Expected: FAIL because `backend/internal/auth` does not exist.

- [ ] **Step 3: Implement auth model and context helpers**

Create `backend/internal/model/auth.go`:

```go
package model

import "time"

type User struct {
	ID                 string `json:"id"`
	Email              string `json:"email"`
	DisplayName        string `json:"display_name"`
	PasswordHash       string `json:"-"`
	MustChangePassword bool   `json:"must_change_password"`
	DefaultWorkspaceID string `json:"default_workspace_id"`
	Role               string `json:"role"`
	Status             string `json:"status"`
	CreatedAt          int64  `json:"created_at"`
	UpdatedAt          int64  `json:"updated_at"`
	LastLoginAt         *int64 `json:"last_login_at,omitempty"`
	PasswordChangedAt  *int64 `json:"password_changed_at,omitempty"`
}

type Workspace struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	OwnerUserID string `json:"owner_user_id"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
}

type WorkspaceMember struct {
	WorkspaceID string `json:"workspace_id"`
	UserID      string `json:"user_id"`
	Role        string `json:"role"`
	CreatedAt   int64  `json:"created_at"`
}

type Session struct {
	ID          string
	UserID      string
	WorkspaceID string
	TokenHash   string
	UserAgent   string
	IPAddress   string
	ExpiresAt   time.Time
	RevokedAt   *time.Time
	CreatedAt   time.Time
	LastSeenAt   time.Time
}

type AuditEvent struct {
	ID           string         `json:"id"`
	ActorUserID  *string        `json:"actor_user_id,omitempty"`
	TargetUserID *string        `json:"target_user_id,omitempty"`
	WorkspaceID  *string        `json:"workspace_id,omitempty"`
	Action       string         `json:"action"`
	Metadata     map[string]any `json:"metadata"`
	CreatedAt    int64          `json:"created_at"`
}

type CurrentUser struct {
	User                User      `json:"user"`
	Workspace           Workspace `json:"workspace"`
	MustChangePassword  bool      `json:"must_change_password"`
}

type LoginRequest struct {
	Email      string `json:"email" binding:"required"`
	Password   string `json:"password" binding:"required"`
	RememberMe bool   `json:"remember_me"`
}

type LoginResponse struct {
	User      User      `json:"user"`
	Workspace Workspace `json:"workspace"`
}

type CreateUserRequest struct {
	Email             string `json:"email" binding:"required"`
	DisplayName       string `json:"display_name"`
	TemporaryPassword string `json:"temporary_password" binding:"required"`
	Role              string `json:"role" binding:"required"`
}

type UpdateUserRequest struct {
	Email       *string `json:"email"`
	DisplayName *string `json:"display_name"`
	Role        *string `json:"role"`
}

type ResetPasswordRequest struct {
	TemporaryPassword string `json:"temporary_password" binding:"required"`
}

type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password" binding:"required"`
	NewPassword     string `json:"new_password" binding:"required"`
}
```

Create `backend/internal/auth/errors.go`:

```go
package auth

import "errors"

var (
	ErrMissingWorkspace      = errors.New("missing workspace scope")
	ErrMissingIdentity       = errors.New("missing request identity")
	ErrInvalidCredentials    = errors.New("invalid credentials")
	ErrAccountDisabled       = errors.New("account disabled")
	ErrPasswordChangeRequired = errors.New("password change required")
	ErrWorkspaceAccessRevoked = errors.New("workspace access revoked")
	ErrLastAdminRequired     = errors.New("last active admin required")
)
```

Create `backend/internal/auth/context.go`:

```go
package auth

import "context"

type contextKey string

const (
	identityKey       contextKey = "flowspace.identity"
	workspaceScopeKey contextKey = "flowspace.workspace_scope"
)

type RequestIdentity struct {
	UserID             string
	SessionID          string
	WorkspaceID        string
	Role               string
	MustChangePassword bool
}

type WorkspaceScope struct {
	WorkspaceID string
}

func ContextWithIdentity(ctx context.Context, identity RequestIdentity) context.Context {
	return context.WithValue(ctx, identityKey, identity)
}

func IdentityFromContext(ctx context.Context) (RequestIdentity, bool) {
	identity, ok := ctx.Value(identityKey).(RequestIdentity)
	return identity, ok
}

func SessionIDFromContext(ctx context.Context) (string, bool) {
	identity, ok := IdentityFromContext(ctx)
	if !ok || identity.SessionID == "" {
		return "", false
	}
	return identity.SessionID, true
}

func ContextWithWorkspaceScope(ctx context.Context, workspaceID string) context.Context {
	return context.WithValue(ctx, workspaceScopeKey, WorkspaceScope{WorkspaceID: workspaceID})
}

func WorkspaceIDFromContext(ctx context.Context) (string, error) {
	scope, ok := ctx.Value(workspaceScopeKey).(WorkspaceScope)
	if !ok || scope.WorkspaceID == "" {
		return "", ErrMissingWorkspace
	}
	return scope.WorkspaceID, nil
}
```

- [ ] **Step 4: Run tests**

```bash
cd backend && go test ./internal/auth -count=1 -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/model/auth.go backend/internal/auth/context.go backend/internal/auth/errors.go backend/internal/auth/context_test.go
git commit -m "feat: add auth identity and workspace scope"
```

---

### Task 2: Add Password And Session Utilities

**Files:**
- Create: `backend/internal/auth/password.go`
- Create: `backend/internal/auth/session.go`
- Create: `backend/internal/config/auth.go`
- Test: `backend/internal/auth/password_test.go`
- Test: `backend/internal/auth/session_test.go`
- Test: `backend/internal/config/auth_test.go`

- [ ] **Step 1: Write failing password tests**

Create `backend/internal/auth/password_test.go`:

```go
package auth

import (
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestHashAndVerifyPassword(t *testing.T) {
	hash, err := HashPassword("secret123")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if hash == "secret123" {
		t.Fatal("hash must not equal plaintext")
	}
	if err := VerifyPassword(hash, "secret123"); err != nil {
		t.Fatalf("verify valid password: %v", err)
	}
	if err := VerifyPassword(hash, "wrong123"); err == nil {
		t.Fatal("expected invalid password error")
	}
}

func TestHashPasswordUsesCost12(t *testing.T) {
	hash, err := HashPassword("secret123")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	cost, err := bcrypt.Cost([]byte(hash))
	if err != nil {
		t.Fatalf("read bcrypt cost: %v", err)
	}
	if cost != PasswordBcryptCost {
		t.Fatalf("bcrypt cost = %d, want %d", cost, PasswordBcryptCost)
	}
}

func TestValidatePasswordPolicy(t *testing.T) {
	if err := ValidatePasswordPolicy("abc12345"); err != nil {
		t.Fatalf("valid password rejected: %v", err)
	}
	if err := ValidatePasswordPolicy("abcdefghi"); err == nil {
		t.Fatal("expected password without digit to fail")
	}
	if err := ValidatePasswordPolicy("123456789"); err == nil {
		t.Fatal("expected password without letter to fail")
	}
	if err := ValidatePasswordPolicy("a1"); err == nil {
		t.Fatal("expected short password to fail")
	}
}
```

- [ ] **Step 2: Write failing session tests**

Create `backend/internal/auth/session_test.go`:

```go
package auth

import (
	"testing"
	"time"
)

func TestSessionTokenHashUsesSecretPepper(t *testing.T) {
	secret := "test-session-secret-with-at-least-32-bytes"
	token := "session-token-value"
	hash1, err := HashSessionToken(secret, token)
	if err != nil {
		t.Fatalf("hash token: %v", err)
	}
	hash2, err := HashSessionToken(secret, token)
	if err != nil {
		t.Fatalf("hash token again: %v", err)
	}
	if hash1 != hash2 {
		t.Fatal("hash must be deterministic")
	}
	if hash1 == token {
		t.Fatal("hash must not equal token")
	}
	otherHash, err := HashSessionToken("other-session-secret-with-at-least-32-bytes", token)
	if err != nil {
		t.Fatalf("hash with other secret: %v", err)
	}
	if otherHash == hash1 {
		t.Fatal("hash must change when secret changes")
	}
}

func TestSessionTokenHashRequiresSecret(t *testing.T) {
	if _, err := HashSessionToken("", "session-token-value"); err == nil {
		t.Fatal("expected missing session secret error")
	}
}

func TestGenerateSessionToken(t *testing.T) {
	token, err := GenerateSessionToken()
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	if len(token) < 40 {
		t.Fatalf("token length = %d, want at least 40", len(token))
	}
}
```

- [ ] **Step 3: Write failing auth config tests**

Create `backend/internal/config/auth_test.go`:

```go
package config

import "testing"

func TestLoadAuthConfigRequiresSessionSecretInProd(t *testing.T) {
	t.Setenv("FLOWSPACE_SESSION_SECRET", "")
	t.Setenv("FLOWSPACE_ALLOWED_ORIGINS", "https://flowspace.example.com")

	_, err := LoadAuthConfig(EnvironmentProduction)
	if err == nil {
		t.Fatal("expected FLOWSPACE_SESSION_SECRET to be required in prod")
	}
}

func TestLoadAuthConfigParsesRequiredSecuritySettings(t *testing.T) {
	t.Setenv("FLOWSPACE_BOOTSTRAP_ADMIN_EMAIL", "admin@example.com")
	t.Setenv("FLOWSPACE_BOOTSTRAP_ADMIN_PASSWORD", "abc12345")
	t.Setenv("FLOWSPACE_BOOTSTRAP_ADMIN_NAME", "Admin")
	t.Setenv("FLOWSPACE_SESSION_SECRET", "prod-session-secret-with-at-least-32-bytes")
	t.Setenv("FLOWSPACE_ALLOWED_ORIGINS", "https://flowspace.example.com,http://localhost:5173")
	t.Setenv("FLOWSPACE_COOKIE_SECURE", "true")
	t.Setenv("FLOWSPACE_ENABLE_LOCAL_DIRECTORY_BROWSER", "false")

	cfg, err := LoadAuthConfig(EnvironmentProduction)
	if err != nil {
		t.Fatalf("load auth config: %v", err)
	}
	if cfg.SessionSecret != "prod-session-secret-with-at-least-32-bytes" {
		t.Fatalf("session secret not loaded")
	}
	if len(cfg.AllowedOrigins) != 2 || cfg.AllowedOrigins[0] != "https://flowspace.example.com" {
		t.Fatalf("allowed origins = %#v", cfg.AllowedOrigins)
	}
	if !cfg.Cookie.Secure {
		t.Fatal("cookie secure should be true")
	}
	if cfg.Session.ShortTTL != 12*time.Hour {
		t.Fatalf("short session ttl = %s, want 12h", cfg.Session.ShortTTL)
	}
	if cfg.Session.RememberTTL != 30*24*time.Hour {
		t.Fatalf("remember session ttl = %s, want 720h", cfg.Session.RememberTTL)
	}
}
```

- [ ] **Step 4: Run password, session, and config tests to verify RED**

```bash
cd backend && go test ./internal/auth ./internal/config -run 'TestHashAndVerifyPassword|TestHashPasswordUsesCost12|TestValidatePasswordPolicy|TestSessionTokenHash|TestGenerateSessionToken|TestLoadAuthConfig' -count=1 -v
```

Expected: FAIL because `backend/internal/auth` does not exist yet and `LoadAuthConfig` is not implemented.

- [ ] **Step 5: Implement password, session, and auth config utilities**

Create `backend/internal/auth/password.go`:

```go
package auth

import (
	"errors"
	"unicode"

	"golang.org/x/crypto/bcrypt"
)

var ErrWeakPassword = errors.New("weak password")

const PasswordBcryptCost = 12

func HashPassword(password string) (string, error) {
	if err := ValidatePasswordPolicy(password); err != nil {
		return "", err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), PasswordBcryptCost)
	return string(hash), err
}

func VerifyPassword(hash string, password string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}

func ValidatePasswordPolicy(password string) error {
	if len([]rune(password)) < 8 {
		return ErrWeakPassword
	}
	hasLetter := false
	hasDigit := false
	for _, r := range password {
		if unicode.IsLetter(r) {
			hasLetter = true
		}
		if unicode.IsDigit(r) {
			hasDigit = true
		}
	}
	if !hasLetter || !hasDigit {
		return ErrWeakPassword
	}
	return nil
}
```

Create `backend/internal/auth/session.go`:

```go
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
)

var ErrMissingSessionSecret = errors.New("missing session secret")

func GenerateSessionToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func HashSessionToken(secret string, token string) (string, error) {
	if strings.TrimSpace(secret) == "" {
		return "", ErrMissingSessionSecret
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(token))
	return hex.EncodeToString(mac.Sum(nil)), nil
}
```

Create `backend/internal/config/auth.go`:

```go
package config

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"time"
)

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
	Window     time.Duration
}

type SessionCleanupConfig struct {
	Interval time.Duration
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
}

func LoadAuthConfig(environment string) (AuthConfig, error) {
	cfg := AuthConfig{
		Bootstrap: BootstrapAdminConfig{
			Email:    strings.TrimSpace(os.Getenv("FLOWSPACE_BOOTSTRAP_ADMIN_EMAIL")),
			Password: os.Getenv("FLOWSPACE_BOOTSTRAP_ADMIN_PASSWORD"),
			Name:     strings.TrimSpace(os.Getenv("FLOWSPACE_BOOTSTRAP_ADMIN_NAME")),
		},
		Cookie: CookieConfig{
			Name:     "fs_session",
			Secure:   envBool("FLOWSPACE_COOKIE_SECURE", environment == EnvironmentProduction),
			SameSite: "Lax",
		},
		Session: SessionTTLConfig{
			ShortTTL:    envDuration("FLOWSPACE_SESSION_TTL", 12*time.Hour),
			RememberTTL: envDuration("FLOWSPACE_REMEMBER_SESSION_TTL", 30*24*time.Hour),
		},
		SessionSecret:               strings.TrimSpace(os.Getenv("FLOWSPACE_SESSION_SECRET")),
		AllowedOrigins:              splitCSV(os.Getenv("FLOWSPACE_ALLOWED_ORIGINS")),
		LoginThrottle:               LoginThrottleConfig{MaxFailures: envInt("FLOWSPACE_LOGIN_MAX_FAILURES", 5), Window: envDuration("FLOWSPACE_LOGIN_WINDOW", 15*time.Minute)},
		SessionCleanup:              SessionCleanupConfig{Interval: envDuration("FLOWSPACE_SESSION_CLEANUP_INTERVAL", time.Hour)},
		EnableLocalDirectoryBrowser: envBool("FLOWSPACE_ENABLE_LOCAL_DIRECTORY_BROWSER", false),
		AllowedLocalRoots:           splitCSV(os.Getenv("FLOWSPACE_ALLOWED_LOCAL_ROOTS")),
	}
	if environment == EnvironmentProduction && cfg.SessionSecret == "" {
		return AuthConfig{}, errors.New("FLOWSPACE_SESSION_SECRET is required in prod")
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

func envBool(name string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envInt(name string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func envDuration(name string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}
```

- [ ] **Step 6: Run tests**

```bash
cd backend && go test ./internal/auth ./internal/config -count=1 -v
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/auth/password.go backend/internal/auth/session.go backend/internal/config/auth.go backend/internal/auth/password_test.go backend/internal/auth/session_test.go backend/internal/config/auth_test.go
git commit -m "feat: add auth utilities and config"
```

---

## Phase 2: Storage Schema, Bootstrap, And Auth Repository

### Task 3: Add Auth Schema And Provider Finalizers

**Files:**
- Create: `backend/db/migrations/postgres/0004_multi_user_auth_schema.sql`
- Create: `backend/internal/storage/postgres/auth_migrations.go`
- Create: `backend/internal/storage/sqlite/auth_migrations.go`
- Modify: `backend/internal/storage/postgres/provider.go`
- Modify: `backend/internal/storage/sqlite/provider.go`
- Test: `backend/internal/storage/postgres/migrations_test.go`
- Test: `backend/internal/storage/sqlite/provider_test.go`

- [ ] **Step 1: Write migration tests for default workspace constraints**

Add a test case to `backend/internal/storage/postgres/migrations_test.go`:

```go
func TestMultiUserAuthMigrationEnforcesDefaultOwnedWorkspace(t *testing.T) {
	schema := fmt.Sprintf("fs_test_auth_default_ws_%d", time.Now().UnixNano())
	db := openPostgresTestDB(t, schema)
	defer db.Close()

	ctx := context.Background()
	if err := RunPostgresMigrationsContext(ctx, db); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO users (id, email, display_name, password_hash, role, status)
		VALUES ('user_owner', 'owner@example.com', 'Owner', 'hash', 'admin', 'active');
		INSERT INTO workspaces (id, name, owner_user_id)
		VALUES ('workspace_owner', 'Owner Workspace', 'user_owner');
		UPDATE users SET default_workspace_id = 'workspace_owner' WHERE id = 'user_owner';
	`); err != nil {
		t.Fatalf("seed valid owner workspace: %v", err)
	}
	if err := runMultiUserAuthFinalizer(ctx, db); err != nil {
		t.Fatalf("finalizer: %v", err)
	}

	_, err := db.ExecContext(ctx, `
		INSERT INTO users (id, email, display_name, password_hash, must_change_password, default_workspace_id, role, status)
		VALUES ('user_a', 'a@example.com', 'A', 'hash', false, 'workspace_b', 'user', 'active')
	`)
	if err == nil {
		t.Fatal("expected default workspace ownership constraint to reject invalid row")
	}
}
```

Add a second PostgreSQL test in the same file:

```go
func TestMultiUserAuthFinalizerCreatesDeferrableDefaultWorkspaceFK(t *testing.T) {
	schema := fmt.Sprintf("fs_test_auth_deferrable_ws_%d", time.Now().UnixNano())
	db := openPostgresTestDB(t, schema)
	defer db.Close()

	ctx := context.Background()
	if err := RunPostgresMigrationsContext(ctx, db); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO users (id, email, display_name, password_hash, role, status)
		VALUES ('user_existing', 'existing@example.com', 'Existing', 'hash', 'admin', 'active');
		INSERT INTO workspaces (id, name, owner_user_id)
		VALUES ('workspace_existing', 'Existing Workspace', 'user_existing');
		UPDATE users SET default_workspace_id = 'workspace_existing' WHERE id = 'user_existing';
	`); err != nil {
		t.Fatalf("seed finalizer data: %v", err)
	}
	if err := runMultiUserAuthFinalizer(ctx, db); err != nil {
		t.Fatalf("finalizer: %v", err)
	}

	var deferrable, deferred bool
	if err := db.QueryRowContext(ctx, `
		SELECT condeferrable, condeferred
		FROM pg_constraint
		WHERE conname = 'users_default_owned_workspace_fk'
	`).Scan(&deferrable, &deferred); err != nil {
		t.Fatalf("read default workspace constraint: %v", err)
	}
	if !deferrable || !deferred {
		t.Fatalf("constraint deferrable=%v deferred=%v, want true/true", deferrable, deferred)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO users (id, email, display_name, password_hash, must_change_password, default_workspace_id, role, status)
		VALUES ('user_later_workspace', 'later@example.com', 'Later', 'hash', true, 'workspace_later', 'user', 'active')
	`); err != nil {
		t.Fatalf("insert user before workspace should be deferred: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO workspaces (id, name, owner_user_id)
		VALUES ('workspace_later', 'Later Workspace', 'user_later_workspace')
	`); err != nil {
		t.Fatalf("insert later workspace: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit deferred default workspace FK: %v", err)
	}
}
```

This test lives in `backend/internal/storage/postgres/migrations_test.go`, which already imports `fmt` and `time` and already defines `openPostgresTestDB(t, schema)`.

- [ ] **Step 2: Run migration test to verify RED**

```bash
cd backend && go test ./internal/storage/postgres -run 'TestMultiUserAuthMigrationEnforcesDefaultOwnedWorkspace|TestMultiUserAuthFinalizerCreatesDeferrableDefaultWorkspaceFK' -count=1 -v
```

Expected: FAIL because `users` and `workspaces` auth schema or deferrable `users_default_owned_workspace_fk` does not exist yet.

- [ ] **Step 3: Create PostgreSQL schema migration**

Create `backend/db/migrations/postgres/0004_multi_user_auth_schema.sql` with these sections in this order:

```sql
CREATE TABLE IF NOT EXISTS users (
  id TEXT PRIMARY KEY,
  email TEXT NOT NULL,
  display_name TEXT NOT NULL DEFAULT '',
  password_hash TEXT NOT NULL,
  must_change_password BOOLEAN NOT NULL DEFAULT false,
  default_workspace_id TEXT,
  role TEXT NOT NULL DEFAULT 'user' CHECK (role IN ('admin', 'user')),
  status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_login_at TIMESTAMPTZ,
  password_changed_at TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS users_email_lower_idx ON users (lower(email));

CREATE TABLE IF NOT EXISTS workspaces (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  owner_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS workspaces_single_owner_v1_idx
  ON workspaces (owner_user_id);

CREATE UNIQUE INDEX IF NOT EXISTS workspaces_owner_workspace_idx
  ON workspaces (owner_user_id, id);

CREATE TABLE IF NOT EXISTS workspace_members (
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role TEXT NOT NULL DEFAULT 'owner' CHECK (role IN ('owner', 'member')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, user_id)
);

CREATE TABLE IF NOT EXISTS sessions (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  token_hash TEXT NOT NULL UNIQUE,
  user_agent TEXT NOT NULL DEFAULT '',
  ip_address TEXT NOT NULL DEFAULT '',
  expires_at TIMESTAMPTZ NOT NULL,
  revoked_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS sessions_user_active_idx
  ON sessions (user_id, expires_at)
  WHERE revoked_at IS NULL;

CREATE TABLE IF NOT EXISTS audit_events (
  id TEXT PRIMARY KEY,
  actor_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
  target_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
  workspace_id TEXT REFERENCES workspaces(id) ON DELETE SET NULL,
  action TEXT NOT NULL,
  metadata JSONB NOT NULL DEFAULT '{}',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS audit_events_created_idx ON audit_events (created_at DESC);
CREATE INDEX IF NOT EXISTS audit_events_actor_idx ON audit_events (actor_user_id, created_at DESC);

ALTER TABLE folders ADD COLUMN IF NOT EXISTS workspace_id TEXT;
ALTER TABLE notes ADD COLUMN IF NOT EXISTS workspace_id TEXT;
ALTER TABLE task_projects ADD COLUMN IF NOT EXISTS workspace_id TEXT;
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS workspace_id TEXT;
ALTER TABLE learning_roadmaps ADD COLUMN IF NOT EXISTS workspace_id TEXT;
ALTER TABLE roadmap_nodes ADD COLUMN IF NOT EXISTS workspace_id TEXT;
ALTER TABLE roadmap_edges ADD COLUMN IF NOT EXISTS workspace_id TEXT;
ALTER TABLE roadmap_resources ADD COLUMN IF NOT EXISTS workspace_id TEXT;
ALTER TABLE events ADD COLUMN IF NOT EXISTS workspace_id TEXT;
ALTER TABLE inbox ADD COLUMN IF NOT EXISTS workspace_id TEXT;
ALTER TABLE sync_targets ADD COLUMN IF NOT EXISTS workspace_id TEXT;
ALTER TABLE note_sync_state ADD COLUMN IF NOT EXISTS workspace_id TEXT;
ALTER TABLE note_project_links ADD COLUMN IF NOT EXISTS workspace_id TEXT;
ALTER TABLE task_recurrence_rules ADD COLUMN IF NOT EXISTS workspace_id TEXT;
ALTER TABLE task_occurrences ADD COLUMN IF NOT EXISTS workspace_id TEXT;
ALTER TABLE note_sync_bindings ADD COLUMN IF NOT EXISTS workspace_id TEXT;
ALTER TABLE sync_external_claims ADD COLUMN IF NOT EXISTS workspace_id TEXT;
ALTER TABLE note_sync_suppressions ADD COLUMN IF NOT EXISTS workspace_id TEXT;
ALTER TABLE sync_import_tombstones ADD COLUMN IF NOT EXISTS workspace_id TEXT;
ALTER TABLE search_index ADD COLUMN IF NOT EXISTS workspace_id TEXT;
```

Do not add bootstrap admin data or bcrypt hashes in SQL.

- [ ] **Step 4: Implement finalizer helpers**

Create `backend/internal/storage/postgres/auth_migrations.go` with functions named:

```go
package postgres

import (
	"context"
	"database/sql"
)

func runMultiUserAuthFinalizer(ctx context.Context, db *sql.DB) error {
	if err := validateWorkspaceBackfill(ctx, db); err != nil {
		return err
	}
	if err := applyWorkspaceNotNullConstraints(ctx, db); err != nil {
		return err
	}
	if err := applyWorkspaceCompositeKeys(ctx, db); err != nil {
		return err
	}
	return applyWorkspaceCompositeForeignKeys(ctx, db)
}
```

Each helper must be idempotent and must return a descriptive error before applying final constraints if any `workspace_id` or `default_workspace_id` is missing.

`applyWorkspaceCompositeForeignKeys` must create `users_default_owned_workspace_fk` exactly as a deferrable composite FK after adding `UNIQUE(workspaces.owner_user_id, workspaces.id)`:

```sql
ALTER TABLE users
  ADD CONSTRAINT users_default_owned_workspace_fk
  FOREIGN KEY (id, default_workspace_id)
  REFERENCES workspaces(owner_user_id, id)
  DEFERRABLE INITIALLY DEFERRED;
```

This constraint is required because admin user provisioning inserts a user row with a precomputed `default_workspace_id` before inserting the workspace row in the same transaction.

- [ ] **Step 5: Add SQLite auth migration file**

Create `backend/internal/storage/sqlite/auth_migrations.go` with functions named:

```go
package sqlite

import (
	"context"
	"database/sql"
)

func ensureSQLiteAuthSchema(ctx context.Context, db *sql.DB) error {
	if err := createSQLiteAuthTables(ctx, db); err != nil {
		return err
	}
	if err := ensureSQLiteWorkspaceColumns(ctx, db); err != nil {
		return err
	}
	return rebuildSQLiteFTSAfterWorkspaceMigration(ctx, db)
}
```

The rebuilt SQLite `users` table must include the same deferred ownership FK:

```sql
FOREIGN KEY (id, default_workspace_id)
  REFERENCES workspaces(owner_user_id, id)
  DEFERRABLE INITIALLY DEFERRED
```

SQLite auth tables must store timestamp columns as Unix seconds in `INTEGER` fields. This includes `sessions.expires_at`, `sessions.revoked_at`, `sessions.created_at`, `sessions.last_seen_at`, `users.last_login_at`, `users.password_changed_at`, and `audit_events.created_at`. Repository code converts between these integer values and the Go `time.Time` fields exposed by model structs.

`rebuildSQLiteFTSAfterWorkspaceMigration` must execute these statements after table rebuilds:

```sql
INSERT INTO notes_fts(notes_fts) VALUES('rebuild');
INSERT INTO tasks_fts(tasks_fts) VALUES('rebuild');
INSERT INTO events_fts(events_fts) VALUES('rebuild');
```

- [ ] **Step 6: Wire provider startup**

Do not run the PostgreSQL strict finalizer during Task 3 provider startup. `runMultiUserAuthFinalizer` validates fully backfilled `workspace_id` and `default_workspace_id` values before adding final constraints, so it must run only after Task 4 bootstrap/backfill succeeds.

Modify `backend/internal/storage/sqlite/provider.go` after recurrence schema:

```go
if err := ensureSQLiteAuthSchema(ctx, db); err != nil {
	_ = db.Close()
	return nil, err
}
```

- [ ] **Step 7: Run migration tests**

```bash
cd backend && go test ./internal/storage/postgres ./internal/storage/sqlite -run 'Test.*Migration|Test.*Provider' -count=1 -v
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add backend/db/migrations/postgres/0004_multi_user_auth_schema.sql backend/internal/storage/postgres/auth_migrations.go backend/internal/storage/sqlite/auth_migrations.go backend/internal/storage/postgres/provider.go backend/internal/storage/sqlite/provider.go backend/internal/storage/postgres/migrations_test.go backend/internal/storage/sqlite/provider_test.go
git commit -m "feat: add multi-user auth schema migrations"
```

---

### Task 4: Add Bootstrap And Legacy Backfill

**Files:**
- Create: `backend/internal/bootstrap/auth_bootstrap.go`
- Create: `backend/internal/bootstrap/auth_bootstrap_test.go`
- Modify: `backend/cmd/server/main.go`
- Modify: `backend/cmd/seed/main.go`
- Modify: `backend/internal/storage/postgres/provider.go`

- [ ] **Step 1: Write bootstrap tests**

Create `backend/internal/bootstrap/auth_bootstrap_test.go`:

```go
package bootstrap

import (
	"context"
	"testing"

	"github.com/hujinrun/flowspace/internal/storage"
)

func TestBootstrapRequiresAdminConfigForLegacyData(t *testing.T) {
	store := openSQLiteStoreWithLegacyNote(t)
	cfg := Config{}

	err := EnsureAuthReady(context.Background(), store, cfg)
	if err == nil {
		t.Fatal("expected missing bootstrap admin config error")
	}
}

func TestBootstrapAssignsLegacyDataBeforeDefaultRows(t *testing.T) {
	store := openSQLiteStoreWithLegacyDefaults(t)
	cfg := Config{
		AdminEmail:    "admin@example.com",
		AdminPassword: "abc12345",
		AdminName:     "Admin",
	}

	if err := EnsureAuthReady(context.Background(), store, cfg); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	assertNoDuplicateDefaultFolderIDs(t, store)
	assertAllBusinessRowsHaveWorkspace(t, store)
	assertDefaultWorkspaceOwnedByAdmin(t, store)
}

func openSQLiteStoreWithLegacyNote(t *testing.T) storage.Store {
	t.Helper()
	return openTestStore(t)
}
```

Use existing SQLite provider test helpers for `openTestStore`. Add local assert helpers in the same test file so the test is self-contained.

- [ ] **Step 2: Run bootstrap tests to verify RED**

```bash
cd backend && go test ./internal/bootstrap -run 'TestBootstrapRequiresAdminConfigForLegacyData|TestBootstrapAssignsLegacyDataBeforeDefaultRows' -count=1 -v
```

Expected: FAIL because `EnsureAuthReady`, `Config`, and bootstrap helpers do not exist yet.

- [ ] **Step 3: Implement bootstrap config**

Create `backend/internal/bootstrap/auth_bootstrap.go`:

```go
package bootstrap

import (
	"context"
	"errors"
	"strings"

	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

type Config struct {
	AdminEmail    string
	AdminPassword string
	AdminName     string
}

var ErrBootstrapAdminRequired = errors.New("bootstrap admin configuration required")

func EnsureAuthReady(ctx context.Context, store storage.Store, cfg Config) error {
	state, err := InspectState(ctx, store)
	if err != nil {
		return err
	}
	if state.HasUsers {
		return nil
	}
	if state.HasBusinessData && !cfg.Valid() {
		return ErrBootstrapAdminRequired
	}
	if !state.HasBusinessData && !cfg.Valid() {
		return nil
	}
	return store.Transact(ctx, func(tx storage.Store) error {
		return createBootstrapAdminAndWorkspace(ctx, tx, cfg, state.HasBusinessData)
	})
}

func (c Config) Valid() bool {
	return strings.TrimSpace(c.AdminEmail) != "" &&
		strings.TrimSpace(c.AdminPassword) != "" &&
		strings.TrimSpace(c.AdminName) != ""
}

func createBootstrapAdminAndWorkspace(ctx context.Context, store storage.Store, cfg Config, hasLegacyData bool) error {
	passwordHash, err := auth.HashPassword(cfg.AdminPassword)
	if err != nil {
		return err
	}
	userID := "user_bootstrap_admin"
	workspaceID := "workspace_bootstrap_admin"
	user := &model.User{
		ID:                 userID,
		Email:              strings.TrimSpace(cfg.AdminEmail),
		DisplayName:        strings.TrimSpace(cfg.AdminName),
		PasswordHash:       passwordHash,
		MustChangePassword: false,
		DefaultWorkspaceID: workspaceID,
		Role:               "admin",
		Status:             "active",
	}
	workspace := &model.Workspace{
		ID:          workspaceID,
		Name:        cfg.AdminName + " Workspace",
		OwnerUserID: userID,
	}
	if err := store.Auth().CreateUser(ctx, user); err != nil {
		return err
	}
	if err := store.Auth().CreateWorkspace(ctx, workspace); err != nil {
		return err
	}
	if err := store.Auth().SetDefaultWorkspace(ctx, userID, workspaceID); err != nil {
		return err
	}
	if err := store.Auth().AddWorkspaceMember(ctx, workspaceID, userID, "owner"); err != nil {
		return err
	}
	scopeCtx := auth.ContextWithWorkspaceScope(ctx, workspaceID)
	if hasLegacyData {
		if err := AssignLegacyBusinessData(scopeCtx, store, workspaceID); err != nil {
			return err
		}
	}
	if err := EnsureDefaultWorkspaceData(scopeCtx, store); err != nil {
		return err
	}
	return nil
}
```

Add these functions in the same file and cover them with tests in `auth_bootstrap_test.go`:

```go
type State struct {
	HasUsers        bool
	HasBusinessData bool
}

func InspectState(ctx context.Context, store storage.Store) (State, error)
func AssignLegacyBusinessData(ctx context.Context, store storage.Store, workspaceID string) error
func EnsureDefaultWorkspaceData(ctx context.Context, store storage.Store) error
```

`AssignLegacyBusinessData` updates existing rows that have empty `workspace_id`. `EnsureDefaultWorkspaceData` inserts missing default folders and `personal` task project only after legacy rows are assigned.

- [ ] **Step 4: Wire server startup**

Modify `backend/cmd/server/main.go` after opening store and before `repository.SetStore(store)`:

```go
authCfg, err := config.LoadAuthConfig(runtimeConfig.Environment)
if err != nil {
	log.Fatalf("auth config: %v", err)
}
bootstrapCfg := bootstrap.Config{
	AdminEmail:    authCfg.Bootstrap.Email,
	AdminPassword: authCfg.Bootstrap.Password,
	AdminName:     authCfg.Bootstrap.Name,
}
if err := bootstrap.EnsureAuthReady(startupCtx, store, bootstrapCfg); err != nil {
	log.Fatalf("auth bootstrap: %v", err)
}
if finalizer, ok := store.(interface {
	FinalizeAuthSchema(context.Context) error
}); ok {
	if err := finalizer.FinalizeAuthSchema(startupCtx); err != nil {
		log.Fatalf("auth schema finalizer: %v", err)
	}
}
```

Add imports:

```go
"github.com/hujinrun/flowspace/internal/bootstrap"
```

- [ ] **Step 5: Run bootstrap tests**

```bash
cd backend && go test ./internal/bootstrap -count=1 -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/bootstrap/auth_bootstrap.go backend/internal/bootstrap/auth_bootstrap_test.go backend/cmd/server/main.go backend/cmd/seed/main.go
git commit -m "feat: add auth bootstrap and legacy backfill"
```

---

### Task 5: Add AuthRepository For PostgreSQL And SQLite

Task 5a moved the AuthRepository primitives before Task 4 because bootstrap depends on `Store.Auth()` for user, workspace, session, and audit provisioning.

**Files:**
- Modify: `backend/internal/storage/store.go`
- Create: `backend/internal/storage/contracttest/auth_contract_tests.go`
- Create: `backend/internal/storage/postgres/auth.go`
- Create: `backend/internal/storage/sqlite/auth.go`
- Modify: `backend/internal/storage/postgres/provider.go`
- Modify: `backend/internal/storage/sqlite/provider.go`
- Modify: `backend/internal/storage/postgres/contract_test.go`
- Modify: `backend/internal/storage/sqlite/contract_test.go`

- [ ] **Step 1: Write shared auth contract tests**

Create `backend/internal/storage/contracttest/auth_contract_tests.go`:

```go
package contracttest

import (
	"context"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

func RunAuthContractTests(t *testing.T, openStore func(t *testing.T) storage.Store) {
	t.Run("CreateUserWorkspaceMembershipAfterFinalizer", func(t *testing.T) {
		store := openStore(t)
		ctx := context.Background()

		user := &model.User{
			ID:                 "user_contract",
			Email:              "contract@example.com",
			DisplayName:        "Contract",
			PasswordHash:       "hash",
			MustChangePassword: true,
			Role:               "user",
			Status:             "active",
		}
		workspace := &model.Workspace{
			ID:          "workspace_contract",
			Name:        "Contract Workspace",
			OwnerUserID: user.ID,
		}
		user.DefaultWorkspaceID = workspace.ID

		err := store.Transact(ctx, func(tx storage.Store) error {
			if err := tx.Auth().CreateUser(ctx, user); err != nil {
				return err
			}
			if err := tx.Auth().CreateWorkspace(ctx, workspace); err != nil {
				return err
			}
			return tx.Auth().AddWorkspaceMember(ctx, workspace.ID, user.ID, "owner")
		})
		if err != nil {
			t.Fatalf("provision user: %v", err)
		}

		loaded, err := store.Auth().GetUserByEmail(ctx, "contract@example.com")
		if err != nil {
			t.Fatalf("get user by email: %v", err)
		}
		if loaded.DefaultWorkspaceID != workspace.ID {
			t.Fatalf("default workspace = %q, want %q", loaded.DefaultWorkspaceID, workspace.ID)
		}
	})

	t.Run("RevokeUserSessionsExcept", func(t *testing.T) {
		store := openStore(t)
		ctx := context.Background()
		seedUserWorkspace(t, ctx, store, "user_sessions", "workspace_sessions")

		now := time.Now()
		for _, id := range []string{"session_keep", "session_revoke"} {
			err := store.Auth().CreateSession(ctx, &model.Session{
				ID:          id,
				UserID:      "user_sessions",
				WorkspaceID: "workspace_sessions",
				TokenHash:   id + "_hash",
				ExpiresAt:   now.Add(time.Hour),
			})
			if err != nil {
				t.Fatalf("create session %s: %v", id, err)
			}
		}
		if err := store.Auth().RevokeUserSessionsExcept(ctx, "user_sessions", "session_keep"); err != nil {
			t.Fatalf("revoke except: %v", err)
		}
	})

	t.Run("GetSessionByTokenHashOnlyReturnsActiveSessions", func(t *testing.T) {
		store := openStore(t)
		ctx := context.Background()
		seedUserWorkspace(t, ctx, store, "user_session_active", "workspace_session_active")
		now := time.Now()
		sessions := []model.Session{
			{ID: "session_active", UserID: "user_session_active", WorkspaceID: "workspace_session_active", TokenHash: "active_hash", ExpiresAt: now.Add(time.Hour)},
			{ID: "session_expired", UserID: "user_session_active", WorkspaceID: "workspace_session_active", TokenHash: "expired_hash", ExpiresAt: now.Add(-time.Minute)},
			{ID: "session_revoked", UserID: "user_session_active", WorkspaceID: "workspace_session_active", TokenHash: "revoked_hash", ExpiresAt: now.Add(time.Hour)},
		}
		for i := range sessions {
			if err := store.Auth().CreateSession(ctx, &sessions[i]); err != nil {
				t.Fatalf("create session %s: %v", sessions[i].ID, err)
			}
		}
		if err := store.Auth().RevokeSession(ctx, "session_revoked"); err != nil {
			t.Fatalf("revoke session: %v", err)
		}

		if _, err := store.Auth().GetSessionByTokenHash(ctx, "active_hash"); err != nil {
			t.Fatalf("active session lookup: %v", err)
		}
		if _, err := store.Auth().GetSessionByTokenHash(ctx, "expired_hash"); err == nil {
			t.Fatal("expired session was restored")
		}
		if _, err := store.Auth().GetSessionByTokenHash(ctx, "revoked_hash"); err == nil {
			t.Fatal("revoked session was restored")
		}
	})
}
```

Add helper functions in the same file for `seedUserWorkspace`.

The `CreateUserWorkspaceMembershipAfterFinalizer` case must run after Task 4 bootstrap/backfill has invoked the provider-specific finalizer hook, not immediately after raw provider startup. It verifies that the final `default_workspace_id NOT NULL` plus deferrable ownership FK still allows user provisioning in one transaction.

- [ ] **Step 2: Run auth contract tests to verify RED**

```bash
cd backend && go test ./internal/storage ./internal/storage/contracttest ./internal/storage/postgres ./internal/storage/sqlite -run AuthContract -count=1 -v
```

Expected: FAIL because `Store.Auth()` and `AuthRepository` do not exist yet.

- [ ] **Step 3: Extend storage interfaces**

Modify `backend/internal/storage/store.go`:

```go
import "time"

type UserListFilter struct {
	Page     int
	PageSize int
	Query    string
}

type AuthRepository interface {
	CreateUser(ctx context.Context, user *model.User) error
	SetDefaultWorkspace(ctx context.Context, userID, workspaceID string) error
	GetUserByEmail(ctx context.Context, email string) (*model.User, error)
	GetUserByID(ctx context.Context, id string) (*model.User, error)
	ListUsers(ctx context.Context, filter UserListFilter) ([]model.User, int, error)
	UpdateUser(ctx context.Context, id string, req *model.UpdateUserRequest) (*model.User, error)
	UpdateUserLastLogin(ctx context.Context, userID string, at time.Time) error
	UpdateUserPassword(ctx context.Context, userID, passwordHash string, mustChangePassword bool) error
	CreateWorkspace(ctx context.Context, workspace *model.Workspace) error
	AddWorkspaceMember(ctx context.Context, workspaceID, userID, role string) error
	CreateSession(ctx context.Context, session *model.Session) error
	GetSessionByTokenHash(ctx context.Context, tokenHash string) (*model.Session, error)
	GetWorkspaceMembership(ctx context.Context, workspaceID, userID string) (*model.WorkspaceMember, error)
	RevokeSession(ctx context.Context, sessionID string) error
	RevokeUserSessions(ctx context.Context, userID string) error
	RevokeUserSessionsExcept(ctx context.Context, userID, keepSessionID string) error
	RecordAuditEvent(ctx context.Context, event *model.AuditEvent) error
	LockActiveAdmins(ctx context.Context) ([]model.User, error)
}
```

Add `Auth() AuthRepository` to `Store`.

- [ ] **Step 4: Implement PostgreSQL auth repository**

Create `backend/internal/storage/postgres/auth.go` with methods matching `AuthRepository`. Use `lower(email)` for lookups and `ILIKE` for `UserListFilter.Query`.

Important SQL fragments:

```sql
SELECT id, email, display_name, password_hash, must_change_password, default_workspace_id,
       role, status, created_at, updated_at, last_login_at, password_changed_at
FROM users
WHERE lower(email) = lower($1)
```

`GetSessionByTokenHash` must never return expired or revoked sessions:

```sql
SELECT id, user_id, workspace_id, token_hash, user_agent, ip_address, expires_at, revoked_at, created_at, last_seen_at
FROM sessions
WHERE token_hash = $1
  AND revoked_at IS NULL
  AND expires_at > now()
```

SQLite must use the same predicate against Unix seconds, for example `expires_at > unixepoch()`.

`UpdateUserLastLogin` must be a narrow update:

```sql
UPDATE users
SET last_login_at = $2,
    updated_at = now()
WHERE id = $1
```

```sql
SELECT id
FROM users
WHERE role = 'admin' AND status = 'active'
FOR UPDATE
```

- [ ] **Step 5: Implement SQLite auth repository**

Create `backend/internal/storage/sqlite/auth.go`. Use `lower(email) = lower(?)` and `LIKE '%' || lower(?) || '%'` against `lower(email)` and `lower(display_name)` for search.

All SQLite auth timestamp reads and writes must stay at the repository boundary: write with `value.Unix()` and read with `time.Unix(value, 0).UTC()`. Do not leak raw integer timestamps into service, handler, or contract test model structs.

- [ ] **Step 6: Wire provider stores**

Add to both `store` and `storeTx` in PostgreSQL and SQLite providers:

```go
func (s *store) Auth() storage.AuthRepository {
	return authRepository{db: s.db}
}

func (s *storeTx) Auth() storage.AuthRepository {
	return authRepository{db: s.tx}
}
```

- [ ] **Step 7: Run auth contract tests**

```bash
cd backend && go test ./internal/storage ./internal/storage/contracttest ./internal/storage/postgres ./internal/storage/sqlite -run AuthContract -count=1 -v
```

Expected: PASS for PostgreSQL and SQLite providers.

- [ ] **Step 8: Commit**

```bash
git add backend/internal/storage/store.go backend/internal/storage/contracttest/auth_contract_tests.go backend/internal/storage/postgres/auth.go backend/internal/storage/sqlite/auth.go backend/internal/storage/postgres/provider.go backend/internal/storage/sqlite/provider.go backend/internal/storage/postgres/contract_test.go backend/internal/storage/sqlite/contract_test.go
git commit -m "feat: add auth storage repository"
```

---

## Phase 3: Backend Auth API

### Task 6: Add Middleware And Auth Handlers

**Files:**
- Create: `backend/internal/middleware/auth.go`
- Create: `backend/internal/handler/auth.go`
- Create: `backend/internal/handler/auth_test.go`
- Modify: `backend/internal/router/router.go`
- Modify: `backend/cmd/server/main.go`

- [ ] **Step 1: Write auth handler tests**

Create `backend/internal/handler/auth_test.go`:

```go
package handler

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLoginSetsHttpOnlyCookie(t *testing.T) {
	router := setupAuthTestRouter(t)
	body := `{"email":"admin@example.com","password":"abc12345","remember_me":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	cookie := w.Result().Cookies()[0]
	if cookie.Name != "fs_session" || !cookie.HttpOnly {
		t.Fatalf("unexpected cookie: %#v", cookie)
	}
	if !lastLoginUpdated(t, "user_admin") {
		t.Fatal("last_login_at was not updated")
	}
	if !auditEventRecorded(t, "auth.login", "user_admin") {
		t.Fatal("auth.login audit event was not recorded")
	}
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
			router := setupAuthTestRouter(t)
			body := fmt.Sprintf(`{"email":"admin@example.com","password":"abc12345","remember_me":%v}`, tc.remember)
			req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			start := time.Now()
			router.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
			}
			cookie := w.Result().Cookies()[0]
			if cookie.MaxAge != tc.wantCookieMaxAge {
				t.Fatalf("cookie max age = %d, want %d", cookie.MaxAge, tc.wantCookieMaxAge)
			}
			session := createdSessionForCookie(t, cookie)
			minExpires := start.Add(tc.wantSessionWindow - time.Minute)
			maxExpires := start.Add(tc.wantSessionWindow + time.Minute)
			if session.ExpiresAt.Before(minExpires) || session.ExpiresAt.After(maxExpires) {
				t.Fatalf("session expires at %s, want between %s and %s", session.ExpiresAt, minExpires, maxExpires)
			}
		})
	}
}

func TestPasswordChangeRequiredBlocksBusinessRoute(t *testing.T) {
	router := setupAuthTestRouterWithMustChangePasswordUser(t)
	req := httptest.NewRequest(http.MethodGet, "/api/notes", nil)
	req.AddCookie(validSessionCookie(t))
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	if !strings.Contains(w.Body.String(), "PASSWORD_CHANGE_REQUIRED") {
		t.Fatalf("body missing code: %s", w.Body.String())
	}
}

func TestExpiredOrRevokedSessionReturns401(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cookie *http.Cookie
	}{
		{name: "expired", cookie: expiredSessionCookie(t)},
		{name: "revoked", cookie: revokedSessionCookie(t)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := setupAuthTestRouter(t)
			req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
			req.AddCookie(tc.cookie)
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", w.Code)
			}
		})
	}
}

func TestLogoutRevokesCurrentSessionAndClearsCookie(t *testing.T) {
	router, sessionID := setupAuthTestRouterWithSession(t)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	req.AddCookie(validSessionCookie(t))
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}
	if !sessionRevoked(t, sessionID) {
		t.Fatalf("session %s was not revoked", sessionID)
	}
	cookie := w.Result().Cookies()[0]
	if cookie.Name != "fs_session" || cookie.MaxAge != -1 {
		t.Fatalf("logout did not clear session cookie: %#v", cookie)
	}
}

func TestChangePasswordRevokesOtherSessionsUsingCurrentSessionID(t *testing.T) {
	router, keptSessionID, otherSessionID, userID := setupAuthTestRouterWithTwoSessions(t)
	body := `{"current_password":"abc12345","new_password":"newpass123"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/change-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(validSessionCookieForSession(t, keptSessionID))
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}
	if sessionRevoked(t, keptSessionID) {
		t.Fatal("current session should be kept until client logs in again")
	}
	if !sessionRevoked(t, otherSessionID) {
		t.Fatal("other sessions should be revoked")
	}
	if !auditEventRecorded(t, "auth.change_password", userID) {
		t.Fatal("auth.change_password audit event was not recorded")
	}
}
```

Add test helpers in the same file using an in-memory SQLite store and seeded user/session.

- [ ] **Step 2: Run auth handler tests to verify RED**

```bash
cd backend && go test ./internal/handler -run 'TestLoginSetsHttpOnlyCookie|TestLoginSessionTTLMatchesRememberMe|TestPasswordChangeRequiredBlocksBusinessRoute|TestExpiredOrRevokedSessionReturns401|TestLogoutRevokesCurrentSessionAndClearsCookie|TestChangePasswordRevokesOtherSessionsUsingCurrentSessionID' -count=1 -v
```

Expected: FAIL because auth handlers, middleware, and router wiring do not exist yet.

- [ ] **Step 3: Implement auth middleware**

Create `backend/internal/middleware/auth.go`:

```go
package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/storage"
)

type AuthMiddleware struct {
	Store         storage.Store
	SessionSecret string
}

func (m AuthMiddleware) Required() gin.HandlerFunc {
	return func(c *gin.Context) {
		cookie, err := c.Cookie("fs_session")
		if err != nil || cookie == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": gin.H{"code": "UNAUTHENTICATED"}})
			return
		}
		tokenHash, err := auth.HashSessionToken(m.SessionSecret, cookie)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": gin.H{"code": "UNAUTHENTICATED"}})
			return
		}
		session, err := m.Store.Auth().GetSessionByTokenHash(c.Request.Context(), tokenHash)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": gin.H{"code": "UNAUTHENTICATED"}})
			return
		}
		user, err := m.Store.Auth().GetUserByID(c.Request.Context(), session.UserID)
		if err != nil || user.Status != "active" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": gin.H{"code": "ACCOUNT_DISABLED"}})
			return
		}
		if _, err := m.Store.Auth().GetWorkspaceMembership(c.Request.Context(), session.WorkspaceID, session.UserID); err != nil {
			_ = m.Store.Auth().RevokeSession(c.Request.Context(), session.ID)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": gin.H{"code": "WORKSPACE_ACCESS_REVOKED"}})
			return
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
		c.Next()
	}
}

func (m AuthMiddleware) Optional() gin.HandlerFunc {
	return func(c *gin.Context) {
		cookie, err := c.Cookie("fs_session")
		if err != nil || cookie == "" {
			c.Next()
			return
		}
		tokenHash, err := auth.HashSessionToken(m.SessionSecret, cookie)
		if err != nil {
			c.Next()
			return
		}
		session, err := m.Store.Auth().GetSessionByTokenHash(c.Request.Context(), tokenHash)
		if err != nil {
			c.Next()
			return
		}
		user, err := m.Store.Auth().GetUserByID(c.Request.Context(), session.UserID)
		if err != nil || user.Status != "active" {
			c.Next()
			return
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
		c.Next()
	}
}

func (m AuthMiddleware) RequireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := auth.IdentityFromContext(c.Request.Context())
		if !ok || identity.Role != "admin" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": gin.H{"code": "FORBIDDEN"}})
			return
		}
		c.Next()
	}
}

func (m AuthMiddleware) RequirePasswordSettled() gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := auth.IdentityFromContext(c.Request.Context())
		if ok && identity.MustChangePassword {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": gin.H{"code": "PASSWORD_CHANGE_REQUIRED"}})
			return
		}
		c.Next()
	}
}
```

- [ ] **Step 4: Implement auth handlers**

Create `backend/internal/handler/auth.go` with:

```go
func Login(store storage.Store, authCfg config.AuthConfig) gin.HandlerFunc
func Logout(store storage.Store, cookieCfg config.CookieConfig) gin.HandlerFunc
func Me(store storage.Store) gin.HandlerFunc
func ChangePassword(store storage.Store) gin.HandlerFunc
```

Login flow must:

```go
user, err := store.Auth().GetUserByEmail(ctx, req.Email)
err = auth.VerifyPassword(user.PasswordHash, req.Password)
workspaceID := user.DefaultWorkspaceID
_, err = store.Auth().GetWorkspaceMembership(ctx, workspaceID, user.ID)
token, err := auth.GenerateSessionToken()
tokenHash, err := auth.HashSessionToken(authCfg.SessionSecret, token)
if err != nil {
	return err
}
ttl := authCfg.Session.ShortTTL
if req.RememberMe {
	ttl = authCfg.Session.RememberTTL
}
session.ExpiresAt = time.Now().Add(ttl)
session.TokenHash = tokenHash
```

Login must create the session, update the user, and audit within the same transaction:

```go
err = store.Transact(ctx, func(tx storage.Store) error {
	if err := tx.Auth().CreateSession(ctx, session); err != nil {
		return err
	}
	if err := tx.Auth().UpdateUserLastLogin(ctx, user.ID, time.Now()); err != nil {
		return err
	}
	return tx.Auth().RecordAuditEvent(ctx, &model.AuditEvent{
		ActorUserID:  &user.ID,
		TargetUserID: &user.ID,
		WorkspaceID:  &workspaceID,
		Action:       "auth.login",
		Metadata:     auth.SanitizeAuditMetadata(map[string]any{"ip": c.ClientIP(), "user_agent": c.Request.UserAgent()}),
	})
})
http.SetCookie(c.Writer, cookieFromConfig(authCfg.Cookie, token, ttl))
```

`GET /api/auth/me` and middleware session restore must not extend `session.expires_at`; there is no sliding expiration in v1.

Logout must clear the cookie even when no valid session exists, and revoke only when optional auth restored a current session:

```go
identity, ok := auth.IdentityFromContext(c.Request.Context())
if ok && identity.SessionID != "" {
	if err := store.Auth().RevokeSession(c.Request.Context(), identity.SessionID); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": gin.H{"code": "LOGOUT_FAILED"}})
		return
	}
}
http.SetCookie(c.Writer, expiredCookie(cookieCfg))
c.Status(http.StatusNoContent)
```

Change password must call:

```go
err := store.Transact(ctx, func(tx storage.Store) error {
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
		Metadata:     auth.SanitizeAuditMetadata(map[string]any{"ip": c.ClientIP(), "user_agent": c.Request.UserAgent()}),
	})
})
```

- [ ] **Step 5: Update router shape**

Change `backend/internal/router/router.go` from `func Setup() *gin.Engine` to:

```go
type Config struct {
	Store storage.Store
	Auth  config.AuthConfig
}

func Setup(cfg Config) *gin.Engine
```

Register:

```go
api.GET("/health", handler.Health)
authMiddleware := middleware.AuthMiddleware{Store: cfg.Store, SessionSecret: cfg.Auth.SessionSecret}
authRoutes := api.Group("/auth")
authRoutes.POST("/login", handler.Login(cfg.Store, cfg.Auth))
authRoutes.POST("/logout", authMiddleware.Optional(), handler.Logout(cfg.Store, cfg.Auth.Cookie))
authRoutes.GET("/me", authMiddleware.Required(), handler.Me(cfg.Store))
authRoutes.POST("/change-password", authMiddleware.Required(), handler.ChangePassword(cfg.Store))
```

Move current business routes under:

```go
protected := api.Group("")
protected.Use(authMiddleware.Required(), authMiddleware.RequirePasswordSettled())
```

Register system directories only inside:

```go
if cfg.Auth.EnableLocalDirectoryBrowser {
	systemAdmin := protected.Group("/system")
	systemAdmin.Use(authMiddleware.RequireAdmin())
	systemAdmin.GET("/directories", handler.ListLocalDirectories(cfg.Store))
}
```

Modify `backend/cmd/server/main.go` to pass the loaded auth config into the router:

```go
r := router.Setup(router.Config{
	Store: store,
	Auth:  authCfg,
})
```

- [ ] **Step 6: Run auth handler tests**

```bash
cd backend && go test ./internal/handler ./internal/router -run 'Auth|Password|Session|Route' -count=1 -v
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/middleware/auth.go backend/internal/handler/auth.go backend/internal/handler/auth_test.go backend/internal/router/router.go backend/cmd/server/main.go
git commit -m "feat: add auth middleware and routes"
```

---

### Task 7: Add Admin User Management Backend

**Files:**
- Create: `backend/internal/handler/admin_users.go`
- Create: `backend/internal/handler/admin_users_test.go`
- Modify: `backend/internal/router/router.go`
- Modify: `backend/internal/storage/postgres/auth.go`
- Modify: `backend/internal/storage/sqlite/auth.go`

- [ ] **Step 1: Write admin API tests**

Create `backend/internal/handler/admin_users_test.go`:

```go
package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminListUsersReturnsPagination(t *testing.T) {
	router := setupAdminTestRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/api/admin/users?page=1&page_size=20&q=admin", nil)
	req.AddCookie(adminSessionCookie(t))
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"pagination"`) || !strings.Contains(body, `"users"`) {
		t.Fatalf("missing users or pagination: %s", body)
	}
}

func TestAdminPatchRejectsStatusAndPassword(t *testing.T) {
	router := setupAdminTestRouter(t)
	body := `{"status":"disabled","temporary_password":"abc12345"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/admin/users/user_1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(adminSessionCookie(t))
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestDowngradeLastAdminReturnsConflict(t *testing.T) {
	router := setupSingleAdminTestRouter(t)
	body := `{"role":"user"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/admin/users/user_admin", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(adminSessionCookie(t))
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", w.Code)
	}
}

func TestRoleChangeRollsBackWhenSessionRevokeFails(t *testing.T) {
	router := setupAdminTestRouterWithRevokeFailure(t)
	body := `{"role":"user"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/admin/users/user_target", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(adminSessionCookie(t))
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	if roleChanged(t, "user_target") {
		t.Fatal("role changed even though session revoke failed")
	}
}
```

- [ ] **Step 2: Run admin API tests to verify RED**

```bash
cd backend && go test ./internal/handler -run 'TestAdminListUsersReturnsPagination|TestAdminPatchRejectsStatusAndPassword|TestDowngradeLastAdminReturnsConflict|TestRoleChangeRollsBackWhenSessionRevokeFails' -count=1 -v
```

Expected: FAIL because admin user handlers and route wiring do not exist yet.

- [ ] **Step 3: Implement admin handlers**

Create `backend/internal/handler/admin_users.go` with handlers:

```go
func ListUsers(store storage.Store) gin.HandlerFunc
func CreateUser(store storage.Store) gin.HandlerFunc
func UpdateUser(store storage.Store) gin.HandlerFunc
func ResetUserPassword(store storage.Store) gin.HandlerFunc
func DisableUser(store storage.Store) gin.HandlerFunc
func EnableUser(store storage.Store) gin.HandlerFunc
```

`CreateUser` must provision user, workspace, membership, default folders/project, and audit in one transaction:

```go
workspace := &model.Workspace{
	ID:          newID("workspace"),
	Name:        user.DisplayName + " Workspace",
	OwnerUserID: user.ID,
}
user.DefaultWorkspaceID = workspace.ID

err := store.Transact(ctx, func(tx storage.Store) error {
	if err := tx.Auth().CreateUser(ctx, user); err != nil {
		return err
	}
	if err := tx.Auth().CreateWorkspace(ctx, workspace); err != nil {
		return err
	}
	if err := tx.Auth().SetDefaultWorkspace(ctx, user.ID, workspace.ID); err != nil {
		return err
	}
	if err := tx.Auth().AddWorkspaceMember(ctx, workspace.ID, user.ID, "owner"); err != nil {
		return err
	}
	targetCtx := auth.ContextWithWorkspaceScope(ctx, workspace.ID)
	if err := createDefaultWorkspaceData(targetCtx, tx); err != nil {
		return err
	}
	auditEvent.Metadata = auth.SanitizeAuditMetadata(auditEvent.Metadata)
	return tx.Auth().RecordAuditEvent(ctx, auditEvent)
})
```

`UpdateUser` must reject forbidden fields by decoding into `map[string]json.RawMessage` first and checking keys.

`ResetUserPassword` must hash the temporary password, set `must_change_password=true`, revoke the target user's sessions, and write an `auth.reset_password` audit event in the same transaction. Its audit metadata must be sanitized and must never include the temporary password, password hash, cookie, authorization header, raw session token, or hashed session token.

- [ ] **Step 4: Implement last-admin guard**

Inside role downgrade and disable flows:

```go
err := store.Transact(ctx, func(tx storage.Store) error {
	admins, err := tx.Auth().LockActiveAdmins(ctx)
	if err != nil {
		return err
	}
	if wouldRemoveLastActiveAdmin(admins, targetUserID) {
		return auth.ErrLastAdminRequired
	}
	return applyUserChange(ctx, tx)
})
```

For role changes and disable, call:

```go
if err := tx.Auth().RevokeUserSessions(ctx, targetUserID); err != nil {
	return err
}
```

Do not ignore revoke errors. If session revocation fails, the role/status transaction must roll back so old sessions cannot survive a permission change.

- [ ] **Step 5: Register admin routes**

In `backend/internal/router/router.go`:

```go
admin := protected.Group("/admin")
admin.Use(authMiddleware.RequireAdmin())
admin.GET("/users", handler.ListUsers(cfg.Store))
admin.POST("/users", handler.CreateUser(cfg.Store))
admin.PATCH("/users/:id", handler.UpdateUser(cfg.Store))
admin.POST("/users/:id/reset-password", handler.ResetUserPassword(cfg.Store))
admin.POST("/users/:id/disable", handler.DisableUser(cfg.Store))
admin.POST("/users/:id/enable", handler.EnableUser(cfg.Store))
```

- [ ] **Step 6: Run admin tests**

```bash
cd backend && go test ./internal/handler -run 'Admin|Downgrade|Reset|Disable' -count=1 -v
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/handler/admin_users.go backend/internal/handler/admin_users_test.go backend/internal/router/router.go backend/internal/storage/postgres/auth.go backend/internal/storage/sqlite/auth.go
git commit -m "feat: add admin user management API"
```

---

## Phase 4: Workspace Isolation

### Task 8: Add WorkspaceScope To Business Repositories

**Files:**
- Modify: `backend/internal/service/notes.go`
- Modify: `backend/internal/service/tasks.go`
- Modify: `backend/internal/service/events.go`
- Modify: `backend/internal/service/today.go`
- Modify: `backend/internal/service/summary.go`
- Modify: `backend/internal/service/inbox.go`
- Modify: `backend/internal/service/search.go`
- Modify: `backend/internal/service/sync_dispatch.go`
- Modify: `backend/internal/service/roadmaps.go`
- Modify: `backend/internal/handler/notes.go`
- Modify: `backend/internal/handler/tasks.go`
- Modify: `backend/internal/handler/events.go`
- Modify: `backend/internal/handler/today.go`
- Modify: `backend/internal/handler/summary.go`
- Modify: `backend/internal/handler/inbox.go`
- Modify: `backend/internal/handler/search.go`
- Modify: `backend/internal/handler/sync.go`
- Modify: `backend/internal/handler/sync_binding.go`
- Modify: `backend/internal/handler/sync_compat.go`
- Create: `backend/internal/service/no_global_repository_test.go`
- Modify: `backend/internal/storage/postgres/folders.go`
- Modify: `backend/internal/storage/postgres/notes.go`
- Modify: `backend/internal/storage/postgres/tasks.go`
- Modify: `backend/internal/storage/postgres/events.go`
- Modify: `backend/internal/storage/postgres/inbox.go`
- Modify: `backend/internal/storage/sqlite/folders.go`
- Modify: `backend/internal/storage/sqlite/notes.go`
- Modify: `backend/internal/storage/sqlite/tasks.go`
- Modify: `backend/internal/storage/sqlite/events.go`
- Modify: `backend/internal/storage/sqlite/inbox.go`
- Test: `backend/internal/storage/contracttest/workspace_isolation_contract_tests.go`

- [ ] **Step 1: Write failing architecture guard test**

Create `backend/internal/service/no_global_repository_test.go`:

```go
package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProtectedServicesDoNotUseGlobalRepositoryFacade(t *testing.T) {
	root := filepath.Join("..", "service")
	forbidden := []string{"repository.", "context.Background()"}
	allowedFiles := map[string]bool{
		"no_global_repository_test.go": true,
	}

	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if allowedFiles[filepath.Base(path)] {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		content := string(data)
		for _, token := range forbidden {
			if strings.Contains(content, token) {
				t.Fatalf("%s still contains %s", path, token)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk service files: %v", err)
	}
}
```

- [ ] **Step 2: Run architecture guard to verify RED**

```bash
cd backend && go test ./internal/service -run TestProtectedServicesDoNotUseGlobalRepositoryFacade -count=1 -v
```

Expected: FAIL because existing service files still use `repository.` or `context.Background()`.

- [ ] **Step 3: Refactor service signatures before enforcing repository scope**

Change service functions from package-level repository usage to explicit `ctx` and `store`:

```go
func GetNotes(ctx context.Context, store storage.Store, filter storage.NoteFilter) ([]model.Note, int, error) {
	return store.Notes().List(ctx, filter)
}
```

Change these service entry points to accept `ctx context.Context` and `store storage.Store`: `GetTasks`, `CreateTask`, `UpdateTask`, `DeleteTask`, `GetEvents`, `CreateEvent`, `UpdateEvent`, `DeleteEvent`, `GetToday`, `GetSummary`, `GetInboxItems`, `ConvertInboxItem`, `Search`, `GetLearningRoadmap`, `UpdateRoadmapNode`, `ListSyncTargets`, `SyncNote`, and target sync dispatch functions.

- [ ] **Step 4: Update handlers to pass request context before repository scope is mandatory**

Example for notes:

```go
notes, total, err := service.GetNotes(c.Request.Context(), store, filter)
```

No protected handler should call a service without passing `c.Request.Context()`.

- [ ] **Step 5: Write workspace isolation contract tests**

Create `backend/internal/storage/contracttest/workspace_isolation_contract_tests.go`:

```go
package contracttest

import (
	"context"
	"testing"

	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

func RunWorkspaceIsolationContractTests(t *testing.T, openStore func(t *testing.T) storage.Store) {
	t.Run("NotesAndTasksAreScoped", func(t *testing.T) {
		store := openStore(t)
		ctxA := auth.ContextWithWorkspaceScope(context.Background(), "workspace_a")
		ctxB := auth.ContextWithWorkspaceScope(context.Background(), "workspace_b")
		seedWorkspaceDefaults(t, context.Background(), store, "workspace_a", "user_a")
		seedWorkspaceDefaults(t, context.Background(), store, "workspace_b", "user_b")

		note, err := store.Notes().Create(ctxA, &model.CreateNoteRequest{Title: "A note"})
		if err != nil {
			t.Fatalf("create note: %v", err)
		}
		if _, err := store.Notes().GetByID(ctxB, note.ID); err == nil {
			t.Fatal("workspace B read workspace A note")
		}
	})

	t.Run("MissingWorkspaceScopeFails", func(t *testing.T) {
		store := openStore(t)
		_, err := store.Notes().List(context.Background(), storage.NoteFilter{Page: 1, PageSize: 20})
		if err == nil {
			t.Fatal("expected missing workspace error")
		}
	})

	t.Run("InboxConversionWritesTypedConvertedTo", func(t *testing.T) {
		store := openStore(t)
		ctx := auth.ContextWithWorkspaceScope(context.Background(), "workspace_inbox_typed")
		seedWorkspaceDefaults(t, context.Background(), store, "workspace_inbox_typed", "user_inbox_typed")

		item, err := store.Inbox().Create(ctx, &model.CreateInboxItemRequest{Content: "Capture me"})
		if err != nil {
			t.Fatalf("create inbox item: %v", err)
		}
		note, err := store.Notes().Create(ctx, &model.CreateNoteRequest{Title: "Converted note"})
		if err != nil {
			t.Fatalf("create converted note: %v", err)
		}
		if err := store.Inbox().MarkConverted(ctx, item.ID, "note:"+note.ID); err != nil {
			t.Fatalf("mark converted: %v", err)
		}
		loaded, err := store.Inbox().GetByID(ctx, item.ID)
		if err != nil {
			t.Fatalf("load converted inbox item: %v", err)
		}
		if loaded.ConvertedTo == nil {
			t.Fatal("converted_to is nil")
		}
		if *loaded.ConvertedTo != "note:"+note.ID {
			t.Fatalf("converted_to = %q, want note:%s", *loaded.ConvertedTo, note.ID)
		}
		if *loaded.ConvertedTo == note.ID {
			t.Fatal("new conversion wrote legacy id-only converted_to")
		}
	})
}
```

Add `seedWorkspaceDefaults` in the same file.

- [ ] **Step 6: Run workspace isolation tests to verify RED**

```bash
cd backend && go test ./internal/storage ./internal/storage/contracttest ./internal/storage/postgres ./internal/storage/sqlite -run WorkspaceIsolation -count=1 -v
```

Expected: FAIL because repositories still read and write without `WorkspaceScope`.

- [ ] **Step 7: Update repository SQL**

For every business repository method, start by resolving scope:

```go
workspaceID, err := auth.WorkspaceIDFromContext(ctx)
if err != nil {
	return nil, 0, err
}
```

For methods returning `(*model.Note, error)`, return `nil, err`. For methods returning `error`, return `err`. For methods returning `([]model.Note, int, error)`, return `nil, 0, err`. Every query must include `workspace_id`.

Example for note GetByID:

```sql
SELECT id, title, body, folder_id, tags, created_at, updated_at
FROM notes
WHERE workspace_id = $1 AND id = $2
```

Example for create:

```sql
INSERT INTO notes (id, workspace_id, title, body, folder_id, tags, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, now(), now())
```

- [ ] **Step 8: Make inbox conversion transactional**

Modify `backend/internal/service/inbox.go` so `ConvertInboxItem` receives `context.Context` and `storage.Store`:

```go
func ConvertInboxItem(ctx context.Context, store storage.Store, id string, req *model.ConvertInboxRequest) (interface{}, error) {
	var convertedID string
	var targetKind string
	err := store.Transact(ctx, func(tx storage.Store) error {
		item, err := tx.Inbox().GetByID(ctx, id)
		if err != nil {
			return err
		}
		if item.ConvertedTo != nil {
			return errors.New("already converted")
		}
		target, err := createInboxTarget(ctx, tx, item, req.Kind)
		if err != nil {
			return err
		}
		targetKind = req.Kind
		convertedID = target.ID
		return tx.Inbox().MarkConverted(ctx, id, targetKind+":"+convertedID)
	})
	if err != nil {
		return nil, err
	}
	return loadConvertedTarget(ctx, store, targetKind, convertedID)
}
```

- [ ] **Step 9: Run isolation contract tests and architecture guard**

```bash
(cd backend && go test ./internal/service -run TestProtectedServicesDoNotUseGlobalRepositoryFacade -count=1 -v)
(cd backend && go test ./internal/storage ./internal/storage/contracttest ./internal/storage/postgres ./internal/storage/sqlite -run WorkspaceIsolation -count=1 -v)
rg -n "context\\.Background\\(\\)|repository\\." backend/internal/service backend/internal/handler -g "*.go" -g "!*_test.go" -g "!health.go"
```

Expected: service guard passes, workspace isolation tests pass for PostgreSQL and SQLite, and `rg` has no output for protected production flows.

- [ ] **Step 10: Commit**

```bash
git add backend/internal/service backend/internal/handler backend/internal/storage/contracttest/workspace_isolation_contract_tests.go backend/internal/storage/postgres/folders.go backend/internal/storage/postgres/notes.go backend/internal/storage/postgres/tasks.go backend/internal/storage/postgres/events.go backend/internal/storage/postgres/inbox.go backend/internal/storage/sqlite/folders.go backend/internal/storage/sqlite/notes.go backend/internal/storage/sqlite/tasks.go backend/internal/storage/sqlite/events.go backend/internal/storage/sqlite/inbox.go
git commit -m "feat: scope core repositories by workspace"
```

---

### Task 9: Scope Search, Sync, Roadmap, And Recurrence

**Files:**
- Modify: `backend/internal/storage/postgres/search.go`
- Modify: `backend/internal/storage/postgres/sync.go`
- Modify: `backend/internal/storage/postgres/roadmaps.go`
- Modify: `backend/internal/storage/postgres/recurrence.go`
- Modify: `backend/internal/storage/sqlite/search.go`
- Modify: `backend/internal/storage/sqlite/sync.go`
- Modify: `backend/internal/storage/sqlite/roadmaps.go`
- Modify: `backend/internal/storage/sqlite/recurrence.go`
- Modify: `backend/internal/service/sync_dispatch.go`
- Test: `backend/internal/storage/contracttest/sync_contract_tests.go`
- Test: `backend/internal/storage/contracttest/roadmaps_contract_tests.go`
- Test: `backend/internal/storage/contracttest/recurrence_contract_tests.go`
- Test: `backend/internal/storage/contracttest/notes_contract_tests.go`

- [ ] **Step 1: Add cross-workspace search test**

Extend `backend/internal/storage/contracttest/notes_contract_tests.go`:

```go
t.Run("SearchDoesNotReturnOtherWorkspaceResults", func(t *testing.T) {
	store := openStore(t)
	ctxA := auth.ContextWithWorkspaceScope(context.Background(), "workspace_search_a")
	ctxB := auth.ContextWithWorkspaceScope(context.Background(), "workspace_search_b")
	seedWorkspaceDefaults(t, context.Background(), store, "workspace_search_a", "user_search_a")
	seedWorkspaceDefaults(t, context.Background(), store, "workspace_search_b", "user_search_b")

	if _, err := store.Notes().Create(ctxA, &model.CreateNoteRequest{Title: "private phrase alpha"}); err != nil {
		t.Fatalf("create note: %v", err)
	}
	results, total, err := store.Search().Search(ctxB, "private phrase alpha", 1, 20)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if total != 0 || len(results) != 0 {
		t.Fatalf("workspace B saw workspace A search results: total=%d results=%+v", total, results)
	}
})
```

- [ ] **Step 2: Run cross-workspace search test to verify RED**

```bash
cd backend && go test ./internal/storage ./internal/storage/contracttest ./internal/storage/postgres ./internal/storage/sqlite -run SearchDoesNotReturnOtherWorkspaceResults -count=1 -v
```

Expected: FAIL because search still lacks first-stage workspace filtering.

- [ ] **Step 3: Update PostgreSQL search and sync SQL**

Search must filter `search_index` first:

```sql
FROM search_index s
WHERE s.workspace_id = $1
  AND (s.title ILIKE $2 OR s.content ILIKE $2)
```

Sync must include workspace in all target, state, binding, claim, suppression, and tombstone lookups. Advisory lock key must include workspace:

```go
lockKey := "note_sync_binding:" + workspaceID + ":" + noteID
```

- [ ] **Step 4: Update SQLite FTS search**

SQLite FTS queries must join business tables and filter workspace:

```sql
SELECT n.id, n.title, n.body
FROM notes_fts f
JOIN notes n ON n.rowid = f.rowid
WHERE n.workspace_id = ?
  AND notes_fts MATCH ?
```

COUNT queries must use the same workspace filter.

- [ ] **Step 5: Update roadmap and recurrence repositories**

Roadmap update example:

```sql
UPDATE roadmap_nodes
SET status = $1, updated_at = $2
WHERE workspace_id = $3 AND id = $4
```

Recurrence rule queries must use `(workspace_id, task_id)` and occurrences must use `(workspace_id, task_id, occurrence_date)`.

- [ ] **Step 6: Run focused storage tests**

```bash
cd backend && go test ./internal/storage ./internal/storage/contracttest ./internal/storage/postgres ./internal/storage/sqlite -run 'Search|Sync|Roadmap|Recurrence|Workspace' -count=1 -v
```

Expected: PASS for PostgreSQL and SQLite.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/storage/postgres/search.go backend/internal/storage/postgres/sync.go backend/internal/storage/postgres/roadmaps.go backend/internal/storage/postgres/recurrence.go backend/internal/storage/sqlite/search.go backend/internal/storage/sqlite/sync.go backend/internal/storage/sqlite/roadmaps.go backend/internal/storage/sqlite/recurrence.go backend/internal/service/sync_dispatch.go backend/internal/storage/contracttest
git commit -m "feat: scope search sync roadmap and recurrence"
```

---

## Phase 5: Frontend Auth And Account Management

### Task 10: Add Frontend Auth Client And Route Guards

**Files:**
- Create: `frontend/src/api/auth.ts`
- Create: `frontend/src/hooks/useAuth.tsx`
- Create: `frontend/src/components/auth/ProtectedRoute.tsx`
- Create: `frontend/src/components/auth/AdminRoute.tsx`
- Create: `frontend/src/routes/ChangePassword.tsx`
- Modify: `frontend/src/router.tsx`
- Modify: `frontend/src/api/client.ts`
- Test: `frontend/src/api/client.test.ts`
- Test: `frontend/src/hooks/useAuth.test.tsx`

- [ ] **Step 1: Write failing auth hook and route guard tests**

Create `frontend/src/hooks/useAuth.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ReactNode } from 'react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { AuthProvider, useAuth } from './useAuth'
import { ProtectedRoute } from '../components/auth/ProtectedRoute'
import { AdminRoute } from '../components/auth/AdminRoute'
import * as authApi from '../api/auth'

vi.mock('../api/auth')

function renderWithAuth(ui: ReactNode, initialEntries = ['/']) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <AuthProvider>
        <MemoryRouter initialEntries={initialEntries}>{ui}</MemoryRouter>
      </AuthProvider>
    </QueryClientProvider>,
  )
}

function AuthProbe() {
  const auth = useAuth()
  if (auth.isLoading) return <span>loading</span>
  return (
    <div>
      <span>{auth.user?.email ?? 'anonymous'}</span>
      <span>{auth.isAdmin ? 'admin' : 'not-admin'}</span>
      <button onClick={() => auth.login({ email: 'admin@example.com', password: 'abc12345', remember_me: true })}>
        login
      </button>
    </div>
  )
}

describe('useAuth', () => {
  beforeEach(() => {
    vi.resetAllMocks()
  })

  it('restores the current user from /api/auth/me', async () => {
    vi.mocked(authApi.me).mockResolvedValue({
      user: {
        id: 'user_admin',
        email: 'admin@example.com',
        display_name: 'Admin',
        role: 'admin',
        status: 'active',
        must_change_password: false,
      },
      workspace: { id: 'workspace_admin', name: 'Admin Workspace' },
      must_change_password: false,
    })

    renderWithAuth(<AuthProbe />)

    expect(await screen.findByText('admin@example.com')).toBeInTheDocument()
    expect(screen.getByText('admin')).toBeInTheDocument()
  })

  it('calls login mutation and refreshes current user', async () => {
    vi.mocked(authApi.me).mockResolvedValue({
      user: {
        id: 'user_admin',
        email: 'admin@example.com',
        display_name: 'Admin',
        role: 'admin',
        status: 'active',
        must_change_password: false,
      },
      workspace: { id: 'workspace_admin', name: 'Admin Workspace' },
      must_change_password: false,
    })
    vi.mocked(authApi.login).mockResolvedValue({
      user: {
        id: 'user_admin',
        email: 'admin@example.com',
        display_name: 'Admin',
        role: 'admin',
        status: 'active',
        must_change_password: false,
      },
      workspace: { id: 'workspace_admin', name: 'Admin Workspace' },
    })

    renderWithAuth(<AuthProbe />)
    await userEvent.click(await screen.findByRole('button', { name: 'login' }))

    await waitFor(() => {
      expect(authApi.login).toHaveBeenCalledWith({
        email: 'admin@example.com',
        password: 'abc12345',
        remember_me: true,
      })
    })
  })
})

describe('route guards', () => {
  beforeEach(() => {
    vi.resetAllMocks()
  })

  it('redirects anonymous users to login with next path', async () => {
    vi.mocked(authApi.me).mockRejectedValue(new Error('unauthenticated'))

    renderWithAuth(
      <Routes>
        <Route path="/tasks" element={<ProtectedRoute><span>tasks</span></ProtectedRoute>} />
        <Route path="/login" element={<span>login page</span>} />
      </Routes>,
      ['/tasks'],
    )

    expect(await screen.findByText('login page')).toBeInTheDocument()
  })

  it('redirects anonymous users from change-password to login', async () => {
    vi.mocked(authApi.me).mockRejectedValue(new Error('unauthenticated'))

    renderWithAuth(
      <Routes>
        <Route path="/change-password" element={<ProtectedRoute><span>change password</span></ProtectedRoute>} />
        <Route path="/login" element={<span>login page</span>} />
      </Routes>,
      ['/change-password'],
    )

    expect(await screen.findByText('login page')).toBeInTheDocument()
  })

  it('allows must-change-password users to open change-password', async () => {
    vi.mocked(authApi.me).mockResolvedValue({
      user: {
        id: 'user_forced',
        email: 'forced@example.com',
        display_name: 'Forced',
        role: 'user',
        status: 'active',
        must_change_password: true,
      },
      workspace: { id: 'workspace_forced', name: 'Forced Workspace' },
      must_change_password: true,
    })

    renderWithAuth(
      <Routes>
        <Route path="/change-password" element={<ProtectedRoute><span>change password</span></ProtectedRoute>} />
        <Route path="/login" element={<span>login page</span>} />
      </Routes>,
      ['/change-password'],
    )

    expect(await screen.findByText('change password')).toBeInTheDocument()
  })

  it('blocks non-admin users from admin routes', async () => {
    vi.mocked(authApi.me).mockResolvedValue({
      user: {
        id: 'user_regular',
        email: 'user@example.com',
        display_name: 'User',
        role: 'user',
        status: 'active',
        must_change_password: false,
      },
      workspace: { id: 'workspace_user', name: 'User Workspace' },
      must_change_password: false,
    })

    renderWithAuth(
      <Routes>
        <Route path="/" element={<span>home</span>} />
        <Route path="/admin/users" element={<AdminRoute><span>admin users</span></AdminRoute>} />
      </Routes>,
      ['/admin/users'],
    )

    expect(await screen.findByText('home')).toBeInTheDocument()
  })
})
```

Create `frontend/src/api/client.test.ts`:

```ts
import { afterEach, describe, expect, it, vi } from 'vitest'
import { api, setUnauthorizedHandler } from './client'

describe('api client auth handling', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    setUnauthorizedHandler(null)
    window.history.pushState({}, '', '/')
  })

  it('sends credentials and calls unauthorized handler on 401', async () => {
    const onUnauthorized = vi.fn()
    setUnauthorizedHandler(onUnauthorized)
    const fetchMock = vi.fn().mockResolvedValue(new Response(
      JSON.stringify({ error: { code: 'UNAUTHENTICATED', message: 'login required' } }),
      { status: 401, headers: { 'Content-Type': 'application/json' } },
    ))
    vi.stubGlobal('fetch', fetchMock)

    await expect(api.get('/api/notes')).rejects.toMatchObject({ status: 401, code: 'UNAUTHENTICATED' })

    expect(fetchMock).toHaveBeenCalledWith(expect.any(String), expect.objectContaining({ credentials: 'include' }))
    expect(onUnauthorized).toHaveBeenCalledTimes(1)
  })

  it('does not call unauthorized handler for invalid login credentials', async () => {
    const onUnauthorized = vi.fn()
    setUnauthorizedHandler(onUnauthorized)
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(
      JSON.stringify({ error: { code: 'INVALID_CREDENTIALS', message: '邮箱或密码错误' } }),
      { status: 401, headers: { 'Content-Type': 'application/json' } },
    )))

    await expect(api.post('/api/auth/login', { email: 'admin@example.com', password: 'wrong' }))
      .rejects.toMatchObject({ status: 401, code: 'INVALID_CREDENTIALS' })

    expect(onUnauthorized).not.toHaveBeenCalled()
  })

  it('does not call unauthorized handler while already on login page', async () => {
    window.history.pushState({}, '', '/login')
    const onUnauthorized = vi.fn()
    setUnauthorizedHandler(onUnauthorized)
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(
      JSON.stringify({ error: { code: 'UNAUTHENTICATED', message: 'login required' } }),
      { status: 401, headers: { 'Content-Type': 'application/json' } },
    )))

    await expect(api.get('/api/auth/me')).rejects.toMatchObject({ status: 401, code: 'UNAUTHENTICATED' })

    expect(onUnauthorized).not.toHaveBeenCalled()
  })
})
```

- [ ] **Step 2: Run frontend auth tests to verify RED**

```bash
cd frontend && npm run test -- useAuth client
```

Expected: FAIL because `frontend/src/api/auth.ts`, `AuthProvider`, `ProtectedRoute`, `AdminRoute`, and `setUnauthorizedHandler` do not exist yet.

- [ ] **Step 3: Add auth API client**

Create `frontend/src/api/auth.ts`:

```ts
import { api } from './client'

export interface AuthUser {
  id: string
  email: string
  display_name: string
  role: 'admin' | 'user'
  status: 'active' | 'disabled'
  must_change_password: boolean
}

export interface AuthWorkspace {
  id: string
  name: string
}

export interface CurrentUserResponse {
  user: AuthUser
  workspace: AuthWorkspace
  must_change_password: boolean
}

export async function login(payload: { email: string; password: string; remember_me: boolean }) {
  const res = await api.post<{ user: AuthUser; workspace: AuthWorkspace }>('/api/auth/login', payload)
  return res.data
}

export async function me() {
  const res = await api.get<CurrentUserResponse>('/api/auth/me')
  return res.data
}

export async function logout() {
  await api.post('/api/auth/logout', {})
}

export async function changePassword(payload: { current_password: string; new_password: string }) {
  await api.post('/api/auth/change-password', payload)
}
```

Update `frontend/src/api/client.ts` so every request uses credentials and every 401 calls a central handler:

```ts
let unauthorizedHandler: (() => void) | null = null

export function setUnauthorizedHandler(handler: (() => void) | null) {
  unauthorizedHandler = handler
}

class APIClient {
  private basePath = import.meta.env.BASE_URL === '/' ? '' : import.meta.env.BASE_URL.replace(/\/$/, '')

  private urlFor(path: string, params?: Record<string, string>) {
    const url = new URL(`${this.basePath}${path}`, window.location.origin)
    if (params) {
      Object.entries(params).forEach(([key, value]) => {
        if (value) url.searchParams.set(key, value)
      })
    }
    return url.toString()
  }

  private async request<T>(path: string, init: RequestInit, params?: Record<string, string>): Promise<APIResponse<T>> {
    const res = await fetch(this.urlFor(path, params), { credentials: 'include', ...init })
    if (!res.ok) {
      const body = await res.json().catch(() => ({}))
      const err = new APIError(res.status, body?.error?.code ?? 'UNKNOWN', body?.error?.message ?? 'Request failed')
      const isLoginRequest = path === '/api/auth/login'
      const isAlreadyOnLogin = window.location.pathname === '/login'
      if (res.status === 401 && err.code !== 'INVALID_CREDENTIALS' && !isLoginRequest && !isAlreadyOnLogin) {
        unauthorizedHandler?.()
      }
      throw err
    }
    if (res.status === 204) return { data: undefined as T }
    return res.json()
  }

  get<T>(path: string, params?: Record<string, string>) {
    return this.request<T>(path, { method: 'GET' }, params)
  }

  post<T>(path: string, body?: unknown) {
    return this.request<T>(path, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: body !== undefined ? JSON.stringify(body) : undefined,
    })
  }

  put<T>(path: string, body?: unknown) {
    return this.request<T>(path, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: body !== undefined ? JSON.stringify(body) : undefined,
    })
  }

  patch<T>(path: string, body?: unknown) {
    return this.request<T>(path, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: body !== undefined ? JSON.stringify(body) : undefined,
    })
  }

  async del(path: string, body?: unknown) {
    await this.request<void>(path, {
      method: 'DELETE',
      headers: body !== undefined ? { 'Content-Type': 'application/json' } : undefined,
      body: body !== undefined ? JSON.stringify(body) : undefined,
    })
  }
}
```

- [ ] **Step 4: Add auth provider**

Create `frontend/src/hooks/useAuth.tsx` with a React context wrapping `useQuery({ queryKey: ['auth', 'me'], queryFn: me })`, `loginMutation`, and `logoutMutation`.

Register the API client 401 handler inside the provider:

```tsx
useEffect(() => {
  setUnauthorizedHandler(() => {
    queryClient.removeQueries({ queryKey: ['auth'] })
    window.location.assign(`/login?next=${encodeURIComponent(window.location.pathname)}`)
  })
  return () => setUnauthorizedHandler(null)
}, [queryClient])
```

The provider value must include:

```ts
{
  user,
  workspace,
  mustChangePassword,
  isLoading,
  isAdmin,
  login,
  logout
}
```

- [ ] **Step 5: Add route guards**

Create `frontend/src/components/auth/ProtectedRoute.tsx`:

```tsx
import type { ReactNode } from 'react'
import { Navigate, useLocation } from 'react-router-dom'
import { useAuth } from '../../hooks/useAuth'

export function ProtectedRoute({ children }: { children: ReactNode }) {
  const auth = useAuth()
  const location = useLocation()

  if (auth.isLoading) return <div className="route-loading">Loading</div>
  if (!auth.user) return <Navigate to={`/login?next=${encodeURIComponent(location.pathname)}`} replace />
  if (auth.mustChangePassword && location.pathname !== '/change-password') {
    return <Navigate to="/change-password" replace />
  }
  return <>{children}</>
}
```

Create `frontend/src/components/auth/AdminRoute.tsx`:

```tsx
import type { ReactNode } from 'react'
import { Navigate } from 'react-router-dom'
import { useAuth } from '../../hooks/useAuth'

export function AdminRoute({ children }: { children: ReactNode }) {
  const auth = useAuth()
  if (!auth.isAdmin) return <Navigate to="/" replace />
  return <>{children}</>
}
```

- [ ] **Step 6: Create guarded change-password page**

Create `frontend/src/routes/ChangePassword.tsx`:

```tsx
import { type FormEvent, useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { changePassword } from '../api/auth'
import { useAuth } from '../hooks/useAuth'

export default function ChangePassword() {
  const auth = useAuth()
  const navigate = useNavigate()
  const [currentPassword, setCurrentPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)

  useEffect(() => {
    if (!auth.isLoading && auth.user && !auth.mustChangePassword) {
      navigate('/', { replace: true })
    }
  }, [auth.isLoading, auth.mustChangePassword, auth.user, navigate])

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setError('')
    setSubmitting(true)
    try {
      await changePassword({ current_password: currentPassword, new_password: newPassword })
      await auth.logout()
      navigate('/login', { replace: true })
    } catch (err) {
      setError(err instanceof Error ? err.message : '修改密码失败')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <main className="auth-page">
      <form className="auth-form" onSubmit={onSubmit}>
        <h1>修改临时密码</h1>
        <label>
          当前密码
          <input value={currentPassword} onChange={(event) => setCurrentPassword(event.target.value)} type="password" autoComplete="current-password" />
        </label>
        <label>
          新密码
          <input value={newPassword} onChange={(event) => setNewPassword(event.target.value)} type="password" autoComplete="new-password" />
        </label>
        {error && <p role="alert">{error}</p>}
        <button type="submit" disabled={submitting}>保存新密码</button>
      </form>
    </main>
  )
}
```

- [ ] **Step 7: Wire router**

Modify `frontend/src/router.tsx` so the app shell is inside `ProtectedRoute`, add guarded `/change-password`, and add admin route:

```tsx
{ path: '/login', element: <Login /> },
{ path: '/change-password', element: <ProtectedRoute><ChangePassword /></ProtectedRoute> },
{
  path: '/',
  element: (
    <ProtectedRoute>
      <App />
    </ProtectedRoute>
  ),
  children: [
    { index: true, element: <Dashboard /> },
    { path: 'admin/users', element: <AdminRoute><AccountAdmin /></AdminRoute> },
  ],
}
```

- [ ] **Step 8: Run frontend tests**

```bash
cd frontend && npm run test -- useAuth client
```

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add frontend/src/api/auth.ts frontend/src/hooks/useAuth.tsx frontend/src/components/auth/ProtectedRoute.tsx frontend/src/components/auth/AdminRoute.tsx frontend/src/routes/ChangePassword.tsx frontend/src/router.tsx frontend/src/api/client.ts frontend/src/api/client.test.ts frontend/src/hooks/useAuth.test.tsx
git commit -m "feat: add frontend auth session guard"
```

---

### Task 11: Wire Login, Account Menu, And Admin UI

**Files:**
- Modify: `frontend/src/routes/Login.tsx`
- Create: `frontend/src/routes/Login.test.tsx`
- Modify: `frontend/src/components/layout/TopBar.tsx`
- Create: `frontend/src/components/layout/TopBar.test.tsx`
- Modify: `frontend/src/components/layout/Sidebar.tsx`
- Modify: `frontend/src/components/layout/Sidebar.test.tsx`
- Create: `frontend/src/routes/AccountAdmin.tsx`
- Create: `frontend/src/routes/AccountAdmin.test.tsx`

- [ ] **Step 1: Write failing UI integration tests**

Create `frontend/src/routes/Login.test.tsx`:

```tsx
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import Login from './Login'
import { useAuth } from '../hooks/useAuth'

vi.mock('../hooks/useAuth')

const navigate = vi.fn()
vi.mock('react-router-dom', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-router-dom')>()
  return Object.assign({}, actual, {
    useNavigate: () => navigate,
    useSearchParams: () => [new URLSearchParams('next=/tasks')],
  })
})

describe('Login', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    vi.mocked(useAuth).mockReturnValue({
      user: null,
      workspace: null,
      mustChangePassword: false,
      isLoading: false,
      isAdmin: false,
      login: vi.fn().mockResolvedValue(undefined),
      logout: vi.fn(),
    })
  })

  it('submits credentials through auth hook and navigates to next', async () => {
    const auth = useAuth()
    render(<MemoryRouter><Login /></MemoryRouter>)

    await userEvent.type(screen.getByLabelText('邮箱'), 'admin@example.com')
    await userEvent.type(screen.getByLabelText('密码'), 'abc12345')
    await userEvent.click(screen.getByRole('button', { name: '登录' }))

    await waitFor(() => {
      expect(auth.login).toHaveBeenCalledWith({
        email: 'admin@example.com',
        password: 'abc12345',
        remember_me: true,
      })
      expect(navigate).toHaveBeenCalledWith('/tasks', { replace: true })
    })
  })
})
```

Create `frontend/src/components/layout/TopBar.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { TopBar } from './TopBar'
import { useAuth } from '../../hooks/useAuth'

vi.mock('../../hooks/useAuth')

describe('TopBar account menu', () => {
  beforeEach(() => {
    vi.resetAllMocks()
  })

  it('shows current user and logs out', async () => {
    const logout = vi.fn()
    vi.mocked(useAuth).mockReturnValue({
      user: { id: 'user_admin', email: 'admin@example.com', display_name: 'Admin', role: 'admin', status: 'active', must_change_password: false },
      workspace: { id: 'workspace_admin', name: 'Admin Workspace' },
      mustChangePassword: false,
      isLoading: false,
      isAdmin: true,
      login: vi.fn(),
      logout,
    })

    render(<MemoryRouter><TopBar /></MemoryRouter>)
    await userEvent.click(screen.getByRole('button', { name: /Admin/ }))
    await userEvent.click(screen.getByRole('button', { name: '退出登录' }))

    expect(logout).toHaveBeenCalled()
  })
})
```

Modify `frontend/src/components/layout/Sidebar.test.tsx` with this test:

```tsx
it('shows admin account link only for admins', () => {
  vi.mocked(useAuth).mockReturnValue({
    user: { id: 'user_admin', email: 'admin@example.com', display_name: 'Admin', role: 'admin', status: 'active', must_change_password: false },
    workspace: { id: 'workspace_admin', name: 'Admin Workspace' },
    mustChangePassword: false,
    isLoading: false,
    isAdmin: true,
    login: vi.fn(),
    logout: vi.fn(),
  })

  renderSidebar()

  expect(screen.getByRole('link', { name: /账号/ })).toBeInTheDocument()
})
```

Create `frontend/src/routes/AccountAdmin.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import AccountAdmin from './AccountAdmin'
import * as authApi from '../api/auth'

vi.mock('../api/auth')

function renderAccountAdmin() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={queryClient}>
      <AccountAdmin />
    </QueryClientProvider>,
  )
}

describe('AccountAdmin', () => {
  beforeEach(() => {
    vi.resetAllMocks()
  })

  it('lists users with pagination-backed data and opens create dialog', async () => {
    vi.mocked(authApi.listUsers).mockResolvedValue({
      users: [{
        id: 'user_alice',
        email: 'alice@example.com',
        display_name: 'Alice',
        role: 'user',
        status: 'active',
        must_change_password: true,
      }],
      pagination: { page: 1, page_size: 20, total: 1 },
    })

    renderAccountAdmin()

    expect(await screen.findByText('alice@example.com')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '创建用户' }))
    expect(screen.getByLabelText('临时密码')).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Run UI tests to verify RED**

```bash
cd frontend && npm run test -- Login AccountAdmin TopBar Sidebar
```

Expected: FAIL because Login still uses mock timeout, TopBar has no account menu, Sidebar has no admin account link, and AccountAdmin does not exist yet.

- [ ] **Step 3: Make login submit real request**

Modify `frontend/src/routes/Login.tsx` submit path:

```tsx
const auth = useAuth()
const navigate = useNavigate()
const [searchParams] = useSearchParams()

async function handleSubmit(event: FormEvent<HTMLFormElement>) {
  event.preventDefault()
  setError('')
  try {
    await auth.login({ email, password, remember_me: remember })
    const next = searchParams.get('next') || '/'
    navigate(next, { replace: true })
  } catch {
    setError('邮箱或密码不正确')
  }
}
```

Remove or disable GitHub login in v1:

```tsx
<button className="auth-oauth-btn" type="button" disabled aria-disabled="true">
  <GithubIcon />
  GitHub 登录暂未启用
</button>
```

- [ ] **Step 4: Add account menu**

Modify `frontend/src/components/layout/TopBar.tsx` to show current user email and logout button:

```tsx
const auth = useAuth()

<button type="button" className="account-menu-button" onClick={() => setOpen((v) => !v)}>
  {auth.user?.display_name || auth.user?.email}
</button>
{open && (
  <div className="account-menu">
    <span>{auth.user?.email}</span>
    <button type="button" onClick={() => auth.logout()}>退出登录</button>
  </div>
)}
```

- [ ] **Step 5: Add admin sidebar entry**

Modify `frontend/src/components/layout/Sidebar.tsx`:

```tsx
const auth = useAuth()
const visibleNavItems = auth.isAdmin
  ? navItems.concat([{ to: '/admin/users', label: '账号', icon: UsersIcon }])
  : navItems
```

- [ ] **Step 6: Create account admin route**

Create `frontend/src/routes/AccountAdmin.tsx` with:

```tsx
export default function AccountAdmin() {
  const [query, setQuery] = useState('')
  const usersQuery = useQuery({
    queryKey: ['admin-users', query],
    queryFn: () => listUsers({ page: 1, page_size: 20, q: query }),
  })

  return (
    <section className="account-admin-page">
      <header className="account-admin-header">
        <h2>账号管理</h2>
        <button type="button" onClick={() => setCreateOpen(true)}>创建用户</button>
      </header>
      <input value={query} onChange={(event) => setQuery(event.target.value)} aria-label="搜索邮箱或姓名" />
      <table>
        <thead>
          <tr><th>邮箱</th><th>名称</th><th>角色</th><th>状态</th><th>最后登录</th><th>操作</th></tr>
        </thead>
        <tbody>
          {(usersQuery.data?.users ?? []).map((user) => (
            <tr key={user.id}>
              <td>{user.email}</td>
              <td>{user.display_name}</td>
              <td>{user.role}</td>
              <td>{user.status}</td>
              <td>{user.last_login_at ?? '-'}</td>
              <td><UserActions user={user} /></td>
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  )
}
```

Add modals for create user, reset temporary password, enable, and disable. Create/reset modal labels must say the password is temporary and the user must change it on first login.

- [ ] **Step 7: Run frontend tests and build**

```bash
cd frontend && npm run test && npm run build
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add frontend/src/routes/Login.tsx frontend/src/routes/Login.test.tsx frontend/src/components/layout/TopBar.tsx frontend/src/components/layout/TopBar.test.tsx frontend/src/components/layout/Sidebar.tsx frontend/src/components/layout/Sidebar.test.tsx frontend/src/routes/AccountAdmin.tsx frontend/src/routes/AccountAdmin.test.tsx
git commit -m "feat: add account management UI"
```

---

## Phase 6: End-To-End Hardening

### Task 12: Add CSRF, Login Throttle, And Session Cleanup

**Files:**
- Create: `backend/internal/middleware/csrf.go`
- Create: `backend/internal/middleware/csrf_test.go`
- Modify: `backend/internal/middleware/cors.go`
- Create: `backend/internal/auth/audit.go`
- Create: `backend/internal/auth/audit_test.go`
- Create: `backend/internal/auth/throttle.go`
- Create: `backend/internal/auth/throttle_test.go`
- Modify: `backend/internal/handler/auth.go`
- Modify: `backend/internal/handler/auth_test.go`
- Modify: `backend/internal/router/router.go`
- Modify: `backend/internal/storage/store.go`
- Modify: `backend/internal/storage/postgres/auth.go`
- Modify: `backend/internal/storage/sqlite/auth.go`
- Modify: `backend/internal/storage/contracttest/auth_contract_tests.go`
- Create: `backend/internal/service/session_cleanup.go`
- Create: `backend/internal/service/session_cleanup_test.go`
- Modify: `backend/cmd/server/main.go`

- [ ] **Step 1: Write failing CSRF origin tests**

Create `backend/internal/middleware/csrf_test.go`:

```go
package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestCSRFMiddlewareRejectsUntrustedOriginOnMutation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(CSRFOriginCheck([]string{"https://flowspace.example.com"}))
	r.POST("/api/notes", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	req := httptest.NewRequest(http.MethodPost, "/api/notes", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

func TestCSRFMiddlewareAllowsSafeMethodsAndAllowedOrigins(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(CSRFOriginCheck([]string{"https://flowspace.example.com"}))
	r.GET("/api/notes", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	r.POST("/api/notes", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	getReq := httptest.NewRequest(http.MethodGet, "/api/notes", nil)
	getW := httptest.NewRecorder()
	r.ServeHTTP(getW, getReq)
	if getW.Code != http.StatusNoContent {
		t.Fatalf("GET status = %d, want 204", getW.Code)
	}

	postReq := httptest.NewRequest(http.MethodPost, "/api/notes", nil)
	postReq.Header.Set("Origin", "https://flowspace.example.com")
	postW := httptest.NewRecorder()
	r.ServeHTTP(postW, postReq)
	if postW.Code != http.StatusNoContent {
		t.Fatalf("POST status = %d, want 204", postW.Code)
	}
}

func TestCORSEchoesOnlyAllowedOrigin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(CORS([]string{"https://flowspace.example.com"}))
	r.GET("/api/notes", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	allowedReq := httptest.NewRequest(http.MethodGet, "/api/notes", nil)
	allowedReq.Header.Set("Origin", "https://flowspace.example.com")
	allowedW := httptest.NewRecorder()
	r.ServeHTTP(allowedW, allowedReq)
	if allowedW.Header().Get("Access-Control-Allow-Origin") != "https://flowspace.example.com" {
		t.Fatalf("allowed origin header = %q", allowedW.Header().Get("Access-Control-Allow-Origin"))
	}

	blockedReq := httptest.NewRequest(http.MethodGet, "/api/notes", nil)
	blockedReq.Header.Set("Origin", "https://evil.example.com")
	blockedW := httptest.NewRecorder()
	r.ServeHTTP(blockedW, blockedReq)
	if blockedW.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("blocked origin header = %q", blockedW.Header().Get("Access-Control-Allow-Origin"))
	}
}
```

- [ ] **Step 2: Write failing audit metadata sanitizer tests**

Create `backend/internal/auth/audit_test.go`:

```go
package auth

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSanitizeAuditMetadataRemovesSecrets(t *testing.T) {
	input := map[string]any{
		"ip":                 "127.0.0.1",
		"user_agent":         "test-agent",
		"temporary_password": "abc12345",
		"password_hash":      "$2a$12$secret",
		"session_token":      "raw-session-token",
		"cookie":             "fs_session=secret",
		"authorization":      "Bearer secret",
		"nested": map[string]any{
			"new_password": "newpass123",
			"safe":         "kept",
		},
	}

	got := SanitizeAuditMetadata(input)
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal sanitized metadata: %v", err)
	}
	text := strings.ToLower(string(raw))
	for _, forbidden := range []string{"abc12345", "$2a$12", "raw-session-token", "fs_session", "bearer secret", "newpass123", "temporary_password", "password_hash", "session_token", "authorization"} {
		if strings.Contains(text, strings.ToLower(forbidden)) {
			t.Fatalf("sanitized metadata still contains %q: %s", forbidden, text)
		}
	}
	if got["ip"] != "127.0.0.1" || got["user_agent"] != "test-agent" {
		t.Fatalf("safe metadata was not preserved: %#v", got)
	}
}
```

- [ ] **Step 3: Write failing login throttle tests**

Create `backend/internal/auth/throttle_test.go`:

```go
package auth

import (
	"testing"
	"time"
)

func TestLoginThrottleBlocksAfterMaxFailuresAndExpiresWindow(t *testing.T) {
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	throttle := NewLoginThrottle(3, 10*time.Minute)
	key := "ip=127.0.0.1 email=admin@example.com"

	for i := 0; i < 3; i++ {
		if !throttle.Allow(key, now) {
			t.Fatalf("attempt %d should be allowed before recording failure", i+1)
		}
		throttle.RecordFailure(key, now)
	}
	if throttle.Allow(key, now.Add(time.Minute)) {
		t.Fatal("expected fourth attempt inside window to be blocked")
	}
	if !throttle.Allow(key, now.Add(11*time.Minute)) {
		t.Fatal("expected attempt after window to be allowed")
	}
}

func TestLoginThrottleResetClearsFailures(t *testing.T) {
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	throttle := NewLoginThrottle(1, 10*time.Minute)
	key := "ip=127.0.0.1 email=admin@example.com"

	throttle.RecordFailure(key, now)
	if throttle.Allow(key, now) {
		t.Fatal("expected key to be blocked")
	}
	throttle.Reset(key)
	if !throttle.Allow(key, now) {
		t.Fatal("expected reset key to be allowed")
	}
}
```

- [ ] **Step 4: Extend auth contract tests for expired session cleanup**

Add to `backend/internal/storage/contracttest/auth_contract_tests.go`:

```go
t.Run("DeleteExpiredSessions", func(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	seedUserWorkspace(t, ctx, store, "user_cleanup", "workspace_cleanup")
	expired := model.Session{ID: "session_expired", UserID: "user_cleanup", WorkspaceID: "workspace_cleanup", TokenHash: "expired_hash", ExpiresAt: now.Add(-time.Minute)}
	active := model.Session{ID: "session_active", UserID: "user_cleanup", WorkspaceID: "workspace_cleanup", TokenHash: "active_hash", ExpiresAt: now.Add(time.Hour)}
	if err := store.Auth().CreateSession(ctx, expired); err != nil {
		t.Fatalf("create expired session: %v", err)
	}
	if err := store.Auth().CreateSession(ctx, active); err != nil {
		t.Fatalf("create active session: %v", err)
	}

	deleted, err := store.Auth().DeleteExpiredSessions(ctx, now)
	if err != nil {
		t.Fatalf("delete expired sessions: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
	if _, err := store.Auth().GetSessionByTokenHash(ctx, "active_hash"); err != nil {
		t.Fatalf("active session missing: %v", err)
	}
})
```

Also extend `backend/internal/storage/store.go`:

```go
DeleteExpiredSessions(ctx context.Context, before time.Time) (int64, error)
```

- [ ] **Step 5: Write failing session cleanup worker test**

Create `backend/internal/service/session_cleanup_test.go`:

```go
package service

import (
	"context"
	"testing"
	"time"
)

type fakeExpiredSessionRepository struct {
	called bool
	before time.Time
}

func (f *fakeExpiredSessionRepository) DeleteExpiredSessions(ctx context.Context, before time.Time) (int64, error) {
	f.called = true
	f.before = before
	return 3, nil
}

func TestDeleteExpiredSessionsOnceUsesRepository(t *testing.T) {
	repo := &fakeExpiredSessionRepository{}
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)

	deleted, err := DeleteExpiredSessionsOnce(context.Background(), repo, now)
	if err != nil {
		t.Fatalf("delete expired sessions once: %v", err)
	}
	if deleted != 3 {
		t.Fatalf("deleted = %d, want 3", deleted)
	}
	if !repo.called || !repo.before.Equal(now) {
		t.Fatalf("repo call = called:%v before:%v", repo.called, repo.before)
	}
}
```

- [ ] **Step 6: Run hardening tests to verify RED**

```bash
(cd backend && go test ./internal/middleware -run CSRF -count=1 -v)
(cd backend && go test ./internal/auth -run SanitizeAuditMetadata -count=1 -v)
(cd backend && go test ./internal/auth -run LoginThrottle -count=1 -v)
(cd backend && go test ./internal/storage ./internal/storage/contracttest ./internal/storage/postgres ./internal/storage/sqlite -run DeleteExpiredSessions -count=1 -v)
(cd backend && go test ./internal/service -run DeleteExpiredSessionsOnce -count=1 -v)
```

Expected: FAIL because CSRF middleware, audit metadata sanitizer, login throttle, expired-session repository cleanup, and cleanup worker helpers are not implemented yet.

- [ ] **Step 7: Implement CSRF origin check and configurable CORS**

Create `backend/internal/middleware/csrf.go`:

```go
package middleware

import (
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"
)

func CSRFOriginCheck(allowedOrigins []string) gin.HandlerFunc {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		allowed[origin] = struct{}{}
	}
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			c.Next()
			return
		}
		origin := c.GetHeader("Origin")
		if origin == "" {
			origin = refererOrigin(c.GetHeader("Referer"))
		}
		if _, ok := allowed[origin]; ok {
			c.Next()
			return
		}
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": gin.H{"code": "CSRF_ORIGIN_REJECTED"}})
	}
}

func refererOrigin(raw string) string {
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}
```

Change `backend/internal/middleware/cors.go` from hardcoded localhost to:

```go
import "net/http"

func CORS(allowedOrigins []string) gin.HandlerFunc {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		allowed[origin] = struct{}{}
	}
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if _, ok := allowed[origin]; ok {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
			c.Header("Access-Control-Allow-Credentials", "true")
		}
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization")
		c.Header("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
```

Create `backend/internal/auth/audit.go`:

```go
package auth

import "strings"

var auditSecretKeys = map[string]struct{}{
	"password":           {},
	"current_password":   {},
	"new_password":       {},
	"temporary_password": {},
	"password_hash":      {},
	"token":              {},
	"session_token":      {},
	"cookie":             {},
	"authorization":      {},
}

func SanitizeAuditMetadata(metadata map[string]any) map[string]any {
	out := make(map[string]any, len(metadata))
	for key, value := range metadata {
		normalized := strings.ToLower(strings.TrimSpace(key))
		if _, secret := auditSecretKeys[normalized]; secret {
			continue
		}
		switch typed := value.(type) {
		case map[string]any:
			out[key] = SanitizeAuditMetadata(typed)
		default:
			out[key] = value
		}
	}
	return out
}
```

Every `RecordAuditEvent` call in auth, admin, and system handlers must wrap metadata:

```go
event.Metadata = auth.SanitizeAuditMetadata(event.Metadata)
```

`handler.ListLocalDirectories` must record a sanitized admin audit event when the route is enabled and called:

```go
_ = store.Auth().RecordAuditEvent(c.Request.Context(), &model.AuditEvent{
	ActorUserID: &identity.UserID,
	Action:      "system.directories.list",
	Metadata: auth.SanitizeAuditMetadata(map[string]any{
		"path":       c.Query("path"),
		"ip":         c.ClientIP(),
		"user_agent": c.Request.UserAgent(),
	}),
})
```

- [ ] **Step 8: Implement login throttle and wire auth handler**

Create `backend/internal/auth/throttle.go`:

```go
package auth

import (
	"sync"
	"time"
)

type LoginThrottle struct {
	mu          sync.Mutex
	maxFailures int
	window      time.Duration
	failures    map[string][]time.Time
}

func NewLoginThrottle(maxFailures int, window time.Duration) *LoginThrottle {
	return &LoginThrottle{maxFailures: maxFailures, window: window, failures: map[string][]time.Time{}}
}

func (t *LoginThrottle) Allow(key string, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pruneLocked(key, now)
	return len(t.failures[key]) < t.maxFailures
}

func (t *LoginThrottle) RecordFailure(key string, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pruneLocked(key, now)
	t.failures[key] = append(t.failures[key], now)
}

func (t *LoginThrottle) Reset(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.failures, key)
}

func (t *LoginThrottle) pruneLocked(key string, now time.Time) {
	cutoff := now.Add(-t.window)
	kept := t.failures[key][:0]
	for _, at := range t.failures[key] {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	t.failures[key] = kept
}
```

Change `handler.Login` to accept and use throttle:

```go
func Login(store storage.Store, authCfg config.AuthConfig, throttle *auth.LoginThrottle) gin.HandlerFunc
```

Before password verification:

```go
key := c.ClientIP() + "|" + strings.ToLower(strings.TrimSpace(req.Email))
now := time.Now()
if !throttle.Allow(key, now) {
	c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": gin.H{"code": "LOGIN_THROTTLED"}})
	return
}
```

On invalid password or missing active user:

```go
throttle.RecordFailure(key, now)
```

On successful login:

```go
throttle.Reset(key)
```

- [ ] **Step 9: Implement expired session cleanup**

Implement `DeleteExpiredSessions` in PostgreSQL:

```sql
DELETE FROM sessions
WHERE expires_at < $1
```

Implement `DeleteExpiredSessions` in SQLite:

```sql
DELETE FROM sessions
WHERE expires_at < ?
```

Create `backend/internal/service/session_cleanup.go`:

```go
package service

import (
	"context"
	"time"

	"github.com/hujinrun/flowspace/internal/storage"
)

type ExpiredSessionRepository interface {
	DeleteExpiredSessions(ctx context.Context, before time.Time) (int64, error)
}

func DeleteExpiredSessionsOnce(ctx context.Context, repo ExpiredSessionRepository, now time.Time) (int64, error) {
	return repo.DeleteExpiredSessions(ctx, now)
}

func StartExpiredSessionCleanup(ctx context.Context, store storage.Store, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			_, _ = DeleteExpiredSessionsOnce(ctx, store.Auth(), now)
		}
	}
}
```

In `backend/cmd/server/main.go`, start the worker after successful bootstrap:

```go
cleanupCtx, stopCleanup := context.WithCancel(context.Background())
defer stopCleanup()
go service.StartExpiredSessionCleanup(cleanupCtx, store, authCfg.SessionCleanup.Interval)
```

- [ ] **Step 10: Wire router middleware**

Update `backend/internal/router/router.go`:

```go
r.Use(gin.Logger(), gin.Recovery(), middleware.CORS(cfg.Auth.AllowedOrigins), middleware.CSRFOriginCheck(cfg.Auth.AllowedOrigins))
loginThrottle := auth.NewLoginThrottle(cfg.Auth.LoginThrottle.MaxFailures, cfg.Auth.LoginThrottle.Window)
authRoutes.POST("/login", handler.Login(cfg.Store, cfg.Auth, loginThrottle))
```

Update existing mutating auth/router tests to send an allowed origin:

```go
req.Header.Set("Origin", "https://flowspace.example.com")
```

- [ ] **Step 11: Run hardening tests**

```bash
(cd backend && go test ./internal/middleware -run 'CSRF|CORS' -count=1 -v)
(cd backend && go test ./internal/auth -run 'SanitizeAuditMetadata|LoginThrottle' -count=1 -v)
(cd backend && go test ./internal/handler ./internal/router -run 'Login|CSRF|CORS' -count=1 -v)
(cd backend && go test ./internal/storage ./internal/storage/contracttest ./internal/storage/postgres ./internal/storage/sqlite -run DeleteExpiredSessions -count=1 -v)
(cd backend && go test ./internal/service -run DeleteExpiredSessionsOnce -count=1 -v)
```

Expected: PASS.

- [ ] **Step 12: Commit**

```bash
git add backend/internal/middleware/csrf.go backend/internal/middleware/csrf_test.go backend/internal/middleware/cors.go backend/internal/auth/audit.go backend/internal/auth/audit_test.go backend/internal/auth/throttle.go backend/internal/auth/throttle_test.go backend/internal/handler/auth.go backend/internal/handler/auth_test.go backend/internal/router/router.go backend/internal/storage/store.go backend/internal/storage/postgres/auth.go backend/internal/storage/sqlite/auth.go backend/internal/storage/contracttest/auth_contract_tests.go backend/internal/service/session_cleanup.go backend/internal/service/session_cleanup_test.go backend/cmd/server/main.go
git commit -m "feat: harden auth session security"
```

---

### Task 13: Add API Integration And Security Tests

**Files:**
- Create: `backend/internal/router/auth_integration_test.go`
- Create: `backend/internal/router/workspace_isolation_test.go`
- Create: `backend/internal/router/system_directories_auth_test.go`
- Create: `backend/internal/router/inbox_conversion_test.go`
- Create: `backend/internal/router/audit_metadata_test.go`

- [ ] **Step 1: Add unauthenticated and cross-user tests**

Create `backend/internal/router/workspace_isolation_test.go`:

```go
package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUnauthenticatedNotesReturns401(t *testing.T) {
	r := setupIntegrationRouter(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/notes", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestUserCannotReadOtherWorkspaceNote(t *testing.T) {
	r, userACookie, userBCookie, noteID := setupTwoUserNoteScenario(t)

	req := httptest.NewRequest(http.MethodGet, "/api/notes/"+noteID, nil)
	req.AddCookie(userBCookie)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/notes/"+noteID, nil)
	req.AddCookie(userACookie)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("owner status = %d, want 200", w.Code)
	}
}

func TestNewUserFirstLoginSeesDefaultWorkspaceData(t *testing.T) {
	r, adminCookie := setupAdminIntegrationRouter(t)
	createBody := `{"email":"new-user@example.com","display_name":"New User","temporary_password":"abc12345","role":"user"}`
	req := httptest.NewRequest(http.MethodPost, "/api/admin/users", strings.NewReader(createBody))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(adminCookie)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create user status = %d, body = %s", w.Code, w.Body.String())
	}

	loginBody := `{"email":"new-user@example.com","password":"abc12345","remember_me":false}`
	req = httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(loginBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", w.Code, w.Body.String())
	}
	userCookie := w.Result().Cookies()[0]

	req = httptest.NewRequest(http.MethodGet, "/api/folders", nil)
	req.AddCookie(userCookie)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("folders status = %d, body = %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "__uncategorized") {
		t.Fatalf("default folders missing: %s", w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/task-projects", nil)
	req.AddCookie(userCookie)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("task projects status = %d, body = %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "personal") {
		t.Fatalf("default personal project missing: %s", w.Body.String())
	}
}
```

- [ ] **Step 2: Add system directories route registration test**

```go
func TestSystemDirectoriesRouteAbsentWhenDisabled(t *testing.T) {
	r := setupIntegrationRouterWithLocalDirectories(t, false)
	req := httptest.NewRequest(http.MethodGet, "/api/system/directories", nil)
	req.AddCookie(adminCookie(t))
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}
```

- [ ] **Step 3: Add audit metadata secret-redaction integration tests**

Create `backend/internal/router/audit_metadata_test.go`:

```go
package router

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResetPasswordAuditMetadataDoesNotStoreSecrets(t *testing.T) {
	r, adminCookie, targetUserID := setupAdminUserScenario(t)
	body := `{"temporary_password":"TempPass123"}`
	req := httptest.NewRequest(http.MethodPost, "/api/admin/users/"+targetUserID+"/reset-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(adminCookie)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	metadata := latestAuditMetadata(t, "auth.reset_password", targetUserID)
	assertAuditMetadataDoesNotContain(t, metadata, []string{"TempPass123", "temporary_password", "password_hash", "session_token", "fs_session", "authorization"})
}

func TestSystemDirectoriesAuditMetadataDoesNotStoreRequestSecrets(t *testing.T) {
	r, adminCookie := setupIntegrationRouterWithLocalDirectories(t, true)
	req := httptest.NewRequest(http.MethodGet, "/api/system/directories?path=C:/Users", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	req.AddCookie(adminCookie)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	metadata := latestAuditMetadata(t, "system.directories.list", "")
	assertAuditMetadataDoesNotContain(t, metadata, []string{"secret-token", "authorization", "fs_session", "session_token", "cookie"})
}

func assertAuditMetadataDoesNotContain(t *testing.T, metadata map[string]any, forbidden []string) {
	t.Helper()
	raw, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("marshal audit metadata: %v", err)
	}
	text := strings.ToLower(string(raw))
	for _, value := range forbidden {
		if strings.Contains(text, strings.ToLower(value)) {
			t.Fatalf("audit metadata contains %q: %s", value, text)
		}
	}
}
```

- [ ] **Step 4: Add inbox conversion rollback test**

```go
func TestInboxConvertRollsBackCreatedEntityWhenMarkFails(t *testing.T) {
	store := openFailingInboxMarkStore(t)
	ctx := authenticatedContextForWorkspace(t, "workspace_rollback")

	_, err := service.ConvertInboxItem(ctx, store, "inbox_1", &model.ConvertInboxRequest{Kind: "note"})
	if err == nil {
		t.Fatal("expected conversion error")
	}
	assertNoNoteWithTitle(t, store, ctx, "Inbox Title")
}
```

- [ ] **Step 5: Run integration tests**

```bash
cd backend && go test ./internal/router -run 'Auth|Workspace|System|Inbox|AuditMetadata' -count=1 -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/router/auth_integration_test.go backend/internal/router/workspace_isolation_test.go backend/internal/router/system_directories_auth_test.go backend/internal/router/inbox_conversion_test.go backend/internal/router/audit_metadata_test.go
git commit -m "test: add auth isolation integration coverage"
```

---

### Task 14: Full Verification And Release Notes

**Files:**
- Modify: `docker-compose.yml`
- Modify: `docker-compose.postgres.yml`
- Create: `docs/superpowers/notes/2026-06-25-multi-user-auth-rollout.md`

- [ ] **Step 1: Run full backend tests**

```bash
cd backend && go test ./...
```

Expected: PASS.

- [ ] **Step 2: Run full frontend checks**

```bash
cd frontend && npm run test && npm run build
```

Expected: PASS.

- [ ] **Step 3: Run targeted scans**

```bash
rg -n "context\\.Background\\(\\)|repository\\." backend/internal/service backend/internal/handler
rg -n "password|temporary_password|session|cookie|authorization|token" backend/internal/storage backend/internal/handler backend/internal/service
```

Expected: first command has no protected business-flow hits. Second command is reviewed to confirm audit metadata never writes secrets.

- [ ] **Step 4: Add docker compose auth environment passthrough**

Update the backend service environment in `docker-compose.yml` and the local PostgreSQL stack in `docker-compose.postgres.yml` when it includes a backend service. Keep existing database, MinIO, AI, Notion, and timezone variables unchanged, and add auth variables as pass-through values:

```yaml
environment:
  FLOWSPACE_BOOTSTRAP_ADMIN_EMAIL: ${FLOWSPACE_BOOTSTRAP_ADMIN_EMAIL:-}
  FLOWSPACE_BOOTSTRAP_ADMIN_PASSWORD: ${FLOWSPACE_BOOTSTRAP_ADMIN_PASSWORD:-}
  FLOWSPACE_BOOTSTRAP_ADMIN_NAME: ${FLOWSPACE_BOOTSTRAP_ADMIN_NAME:-}
  FLOWSPACE_SESSION_SECRET: ${FLOWSPACE_SESSION_SECRET:?FLOWSPACE_SESSION_SECRET is required}
  FLOWSPACE_ALLOWED_ORIGINS: ${FLOWSPACE_ALLOWED_ORIGINS:-http://localhost:4200}
  FLOWSPACE_COOKIE_SECURE: ${FLOWSPACE_COOKIE_SECURE:-false}
  FLOWSPACE_SESSION_TTL: ${FLOWSPACE_SESSION_TTL:-12h}
  FLOWSPACE_REMEMBER_SESSION_TTL: ${FLOWSPACE_REMEMBER_SESSION_TTL:-720h}
  FLOWSPACE_LOGIN_MAX_FAILURES: ${FLOWSPACE_LOGIN_MAX_FAILURES:-5}
  FLOWSPACE_LOGIN_WINDOW: ${FLOWSPACE_LOGIN_WINDOW:-15m}
  FLOWSPACE_SESSION_CLEANUP_INTERVAL: ${FLOWSPACE_SESSION_CLEANUP_INTERVAL:-1h}
  FLOWSPACE_ENABLE_LOCAL_DIRECTORY_BROWSER: ${FLOWSPACE_ENABLE_LOCAL_DIRECTORY_BROWSER:-false}
  FLOWSPACE_ALLOWED_LOCAL_ROOTS: ${FLOWSPACE_ALLOWED_LOCAL_ROOTS:-}
```

Run compose config validation after setting a local dummy `FLOWSPACE_SESSION_SECRET`:

```bash
FLOWSPACE_SESSION_SECRET=local-compose-check docker compose -f docker-compose.yml config >/dev/null
FLOWSPACE_SESSION_SECRET=local-compose-check docker compose -f docker-compose.postgres.yml config >/dev/null
rg -n "FLOWSPACE_SESSION_SECRET|FLOWSPACE_ALLOWED_ORIGINS|FLOWSPACE_SESSION_TTL|FLOWSPACE_REMEMBER_SESSION_TTL|FLOWSPACE_ENABLE_LOCAL_DIRECTORY_BROWSER" docker-compose.yml docker-compose.postgres.yml
```

Expected: compose config succeeds and both compose files either pass auth environment to the backend service or clearly have no backend service to configure.

- [ ] **Step 5: Write rollout notes**

Create `docs/superpowers/notes/2026-06-25-multi-user-auth-rollout.md`:

```markdown
# Multi-User Auth Rollout Notes

## Required Environment

- FLOWSPACE_BOOTSTRAP_ADMIN_EMAIL
- FLOWSPACE_BOOTSTRAP_ADMIN_PASSWORD
- FLOWSPACE_BOOTSTRAP_ADMIN_NAME
- FLOWSPACE_SESSION_SECRET
- FLOWSPACE_ALLOWED_ORIGINS
- FLOWSPACE_COOKIE_SECURE
- FLOWSPACE_SESSION_TTL
- FLOWSPACE_REMEMBER_SESSION_TTL
- FLOWSPACE_LOGIN_MAX_FAILURES
- FLOWSPACE_LOGIN_WINDOW
- FLOWSPACE_SESSION_CLEANUP_INTERVAL
- FLOWSPACE_ENABLE_LOCAL_DIRECTORY_BROWSER
- FLOWSPACE_ALLOWED_LOCAL_ROOTS

## First Startup

1. Back up the database.
2. Start the backend with bootstrap admin variables.
3. Confirm bootstrap logs show one admin user and one default workspace.
4. Confirm `/api/auth/login` works for the bootstrap admin.
5. Confirm old notes, tasks, events, inbox items, sync targets, and roadmaps are visible only to the bootstrap admin.
6. Confirm a mutating request from an unlisted `Origin` returns `CSRF_ORIGIN_REJECTED`.
7. Confirm repeated bad login attempts return `LOGIN_THROTTLED`.

## Rollback

1. Stop the backend.
2. Restore the database backup.
3. Deploy the previous backend and frontend build.
```

- [ ] **Step 6: Commit**

```bash
git add docker-compose.yml docker-compose.postgres.yml docs/superpowers/notes/2026-06-25-multi-user-auth-rollout.md
git commit -m "docs: add multi-user auth rollout notes"
```

- [ ] **Step 7: Final status**

```bash
git status --short
git log --oneline -n 10
```

Expected: working tree is clean and recent commits match the task list.

---

## Final Acceptance Checklist

- [ ] Login uses HttpOnly `fs_session` cookie.
- [ ] Session token hashes use HMAC-SHA256 with `FLOWSPACE_SESSION_SECRET`.
- [ ] Session restore rejects expired and revoked sessions on every request.
- [ ] Request identity includes the current `SessionID`; logout revokes the current session when present.
- [ ] Login updates `last_login_at` and writes `auth.login` audit; password change writes `auth.change_password` audit.
- [ ] `/api/auth/me` restores user, workspace, role, and `must_change_password`.
- [ ] Forced password users can only access `me`, `change-password`, and `logout`.
- [ ] Admin can list, create, update role/display/email, disable, enable, and reset temporary password.
- [ ] Admin create user writes default folders/project to the new user workspace.
- [ ] Admin role/status changes roll back if target session revocation fails.
- [ ] `users.default_workspace_id` is NOT NULL after finalizer and cannot point to another user's owned workspace.
- [ ] `users_default_owned_workspace_fk` is `DEFERRABLE INITIALLY DEFERRED` in PostgreSQL and SQLite rebuilt schema.
- [ ] Every protected business route requires auth and `RequirePasswordSettled`.
- [ ] Every business repository fails without `WorkspaceScope`.
- [ ] Notes, tasks, events, inbox, search, sync, roadmap, and recurrence data are isolated by workspace.
- [ ] Inbox conversion is transactional and writes typed `converted_to`.
- [ ] SQLite FTS is rebuilt after table rebuilds and search uses business-table workspace filters.
- [ ] `/api/system/directories` is absent when disabled and admin-only when enabled.
- [ ] CORS and CSRF checks use `FLOWSPACE_ALLOWED_ORIGINS`.
- [ ] Repeated failed login attempts are throttled and successful login resets the throttle key.
- [ ] Expired sessions are deleted by repository cleanup and the startup cleanup worker.
- [ ] Frontend API client sends credentials and clears auth state on central 401 handling.
- [ ] Frontend central 401 handling does not redirect for `/api/auth/login` `INVALID_CREDENTIALS` or while already on `/login`.
- [ ] `/change-password` is protected from anonymous access and remains reachable for forced-password users.
- [ ] Full backend tests pass.
- [ ] Full frontend tests and build pass.
