# GitHub OAuth Login Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement GitHub OAuth login for FlowSpace, including first-login auto-provisioning, session fixation protection, provider discovery, and frontend error handling.

**Architecture:** Keep GitHub OAuth as a public auth flow that ends by issuing the existing `fs_session` HttpOnly cookie. Add a small OAuth layer beside the existing auth code: config, state store, GitHub client abstraction, handler, storage identity mapping, and frontend link/error UI. Preserve existing workspace ownership rules by creating users, workspaces, default workspace data, and identity bindings in one transaction.

**Tech Stack:** Go 1.26, Gin, database/sql, PostgreSQL via pgx, SQLite via modernc.org/sqlite, React 19, React Router, Vitest, Testing Library

---

## Source Spec

- `docs/github-oauth-login-design.md`

## Execution Rules

- Create a feature branch before implementation: `git switch -c codex/github-oauth-login`.
- Follow strict TDD for every production change: RED test first, verify RED, write minimal GREEN implementation, verify GREEN, refactor only after GREEN.
- If a test passes on the first RED run, rewrite the test until it fails because the behavior is missing.
- Do not write production code before its failing test. If production code appears first, delete it and restart that task.
- Keep each task in a small commit after its narrow tests pass.
- Use fake HTTP servers for GitHub API tests; do not call real GitHub in automated tests.
- Never log or persist GitHub OAuth `code`, access token, client secret, session token, or cookies.
- Run storage contract tests for SQLite after each storage task. Run PostgreSQL tests when the local PostgreSQL test database is available.

## TDD Protocol

Every behavior task follows this loop:

1. RED: add one focused failing test.
2. Verify RED: run only that test and confirm it fails for the expected missing behavior.
3. GREEN: write the smallest implementation that makes the test pass.
4. Verify GREEN: rerun the focused test and the narrow package suite.
5. Refactor: clean only after GREEN, then rerun the same suite.
6. Commit: commit only after the task-specific tests pass.

## File Map

| File | Responsibility | Change |
| --- | --- | --- |
| `backend/internal/config/auth.go` | Parse GitHub OAuth config and expose provider enablement | Modify |
| `backend/internal/config/auth_test.go` | Config RED/GREEN tests | Modify |
| `backend/internal/auth/oauth_state.go` | In-memory OAuth state store, TTL, cleanup | Create |
| `backend/internal/auth/oauth_state_test.go` | State store TDD tests | Create |
| `backend/internal/auth/oauth_next.go` | Safe OAuth `next` sanitizer | Create |
| `backend/internal/auth/oauth_next_test.go` | Redirect sanitization TDD tests | Create |
| `backend/internal/model/auth.go` | Add `PasswordSet` and `AuthIdentity` | Modify |
| `backend/internal/storage/store.go` | Add auth identity repository methods | Modify |
| `backend/internal/storage/contracttest/auth_contract_tests.go` | PasswordSet and identity storage contracts | Modify |
| `backend/internal/storage/sqlite/auth_migrations.go` | SQLite users/password_set and auth_identities schema | Modify |
| `backend/internal/storage/sqlite/auth.go` | SQLite AuthRepository identity methods | Modify |
| `backend/internal/storage/postgres/auth.go` | PostgreSQL AuthRepository identity methods | Modify |
| `backend/db/migrations/postgres/0005_github_oauth.sql` | PostgreSQL GitHub OAuth schema migration | Create |
| `backend/internal/storage/postgres/migrations_test.go` | Migration coverage for password_set and auth_identities | Modify |
| `backend/internal/handler/github_oauth.go` | Providers, GitHub start, GitHub callback handlers | Create |
| `backend/internal/handler/github_oauth_test.go` | OAuth handler behavior tests with fake GitHub server | Create |
| `backend/internal/handler/auth.go` | `ChangePassword` returns `PASSWORD_NOT_SET` for OAuth-only users | Modify |
| `backend/internal/handler/auth_test.go` | Password-set regression test | Modify |
| `backend/internal/router/router.go` | Register public providers/start/callback routes | Modify |
| `backend/internal/router/auth_routes_test.go` | Route registration and middleware placement tests | Modify |
| `backend/cmd/server/main.go` | Create OAuth state store and pass it to router | Modify |
| `frontend/src/api/auth.ts` | Add providers API and OAuth provider type | Modify |
| `frontend/src/routes/Login.tsx` | Render GitHub link, providers gating, oauth_error messages | Modify |
| `frontend/src/routes/Login.test.tsx` | Frontend TDD tests | Modify |
| `docs/github-oauth-login-design.md` | Mark implementation decisions that change during build | Modify only if implementation reveals a design correction |

---

## Task 0: Branch And Baseline

**Files:**
- No source changes

- [ ] **Step 1: Create branch**

```bash
git switch -c codex/github-oauth-login
```

Expected: current branch becomes `codex/github-oauth-login`.

- [ ] **Step 2: Run backend baseline**

```bash
cd backend && go test ./...
```

Expected: existing backend tests pass before GitHub OAuth work begins.

- [ ] **Step 3: Run frontend baseline**

```bash
cd frontend && npm run test
```

Expected: existing frontend tests pass before GitHub OAuth work begins.

- [ ] **Step 4: Confirm worktree**

```bash
git status --short
```

Expected: only pre-existing user changes appear. Do not revert unrelated changes.

---

## Task 1: GitHub OAuth Config

**Files:**
- Modify: `backend/internal/config/auth.go`
- Modify: `backend/internal/config/auth_test.go`

- [ ] **Step 1: RED - add config parsing tests**

Add these tests to `backend/internal/config/auth_test.go`:

```go
func TestLoadAuthConfigParsesGitHubOAuth(t *testing.T) {
	clearAuthEnv(t)
	t.Setenv("AUTH_GITHUB_ENABLED", "true")
	t.Setenv("AUTH_GITHUB_CLIENT_ID", "client-id")
	t.Setenv("AUTH_GITHUB_CLIENT_SECRET", "client-secret")
	t.Setenv("AUTH_GITHUB_REDIRECT_URL", "https://all-note.jinrunlab.site/api/auth/github/callback")
	t.Setenv("AUTH_GITHUB_AUTO_CREATE_USERS", "true")
	t.Setenv("AUTH_GITHUB_STATE_TTL", "7m")
	t.Setenv("AUTH_GITHUB_ALLOWED_REDIRECT_HOSTS", "all-note.jinrunlab.site,localhost:4100")

	cfg, err := LoadAuthConfig(EnvironmentTest)
	if err != nil {
		t.Fatalf("load auth config: %v", err)
	}

	if !cfg.GitHub.Enabled {
		t.Fatal("GitHub.Enabled = false, want true")
	}
	if cfg.GitHub.ClientID != "client-id" || cfg.GitHub.ClientSecret != "client-secret" {
		t.Fatalf("unexpected GitHub client config: %#v", cfg.GitHub)
	}
	if cfg.GitHub.RedirectURL != "https://all-note.jinrunlab.site/api/auth/github/callback" {
		t.Fatalf("redirect URL = %q", cfg.GitHub.RedirectURL)
	}
	if !cfg.GitHub.AutoCreateUsers {
		t.Fatal("AutoCreateUsers = false, want true")
	}
	if cfg.GitHub.StateTTL != 7*time.Minute {
		t.Fatalf("StateTTL = %s, want 7m", cfg.GitHub.StateTTL)
	}
	if got := strings.Join(cfg.GitHub.AllowedRedirectHosts, ","); got != "all-note.jinrunlab.site,localhost:4100" {
		t.Fatalf("AllowedRedirectHosts = %q", got)
	}
}

func TestLoadAuthConfigKeepsGitHubDisabledWhenIncomplete(t *testing.T) {
	clearAuthEnv(t)
	t.Setenv("AUTH_GITHUB_ENABLED", "true")
	t.Setenv("AUTH_GITHUB_CLIENT_ID", "client-id")
	t.Setenv("AUTH_GITHUB_CLIENT_SECRET", "")

	cfg, err := LoadAuthConfig(EnvironmentTest)
	if err != nil {
		t.Fatalf("load auth config: %v", err)
	}
	if cfg.GitHub.Available() {
		t.Fatal("GitHub provider should not be available without complete config")
	}
}
```

If `clearAuthEnv` does not include GitHub variables, extend its env list:

```go
"AUTH_GITHUB_ENABLED",
"AUTH_GITHUB_CLIENT_ID",
"AUTH_GITHUB_CLIENT_SECRET",
"AUTH_GITHUB_REDIRECT_URL",
"AUTH_GITHUB_AUTO_CREATE_USERS",
"AUTH_GITHUB_STATE_TTL",
"AUTH_GITHUB_ALLOWED_REDIRECT_HOSTS",
```

- [ ] **Step 2: Verify RED**

```bash
cd backend && go test ./internal/config -run 'TestLoadAuthConfigParsesGitHubOAuth|TestLoadAuthConfigKeepsGitHubDisabledWhenIncomplete' -count=1 -v
```

Expected: FAIL because `AuthConfig.GitHub` and `GitHubOAuthConfig.Available` do not exist.

- [ ] **Step 3: GREEN - add GitHub config**

Add to `backend/internal/config/auth.go`:

```go
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
		(strings.TrimSpace(cfg.RedirectURL) != "" || len(cfg.AllowedRedirectHosts) > 0)
}
```

Add field to `AuthConfig`:

```go
GitHub GitHubOAuthConfig
```

In `LoadAuthConfig`, parse:

```go
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
githubHosts, err := splitStrictCSVAllowEmpty("AUTH_GITHUB_ALLOWED_REDIRECT_HOSTS", os.Getenv("AUTH_GITHUB_ALLOWED_REDIRECT_HOSTS"))
if err != nil {
	return AuthConfig{}, err
}
```

Implement `splitStrictCSVAllowEmpty`:

```go
func splitStrictCSVAllowEmpty(name, value string) ([]string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	return splitStrictCSV(name, value)
}
```

Populate:

```go
GitHub: GitHubOAuthConfig{
	Enabled:              githubEnabled,
	ClientID:             strings.TrimSpace(os.Getenv("AUTH_GITHUB_CLIENT_ID")),
	ClientSecret:         strings.TrimSpace(os.Getenv("AUTH_GITHUB_CLIENT_SECRET")),
	RedirectURL:          strings.TrimSpace(os.Getenv("AUTH_GITHUB_REDIRECT_URL")),
	AutoCreateUsers:      githubAutoCreateUsers,
	StateTTL:             githubStateTTL,
	AllowedRedirectHosts: githubHosts,
},
```

- [ ] **Step 4: Verify GREEN**

```bash
cd backend && go test ./internal/config -run 'GitHubOAuth|ConfiguredValues' -count=1 -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/config/auth.go backend/internal/config/auth_test.go
git commit -m "feat: add github oauth auth config"
```

---

## Task 2: OAuth State Store And Safe Redirects

**Files:**
- Create: `backend/internal/auth/oauth_state.go`
- Create: `backend/internal/auth/oauth_state_test.go`
- Create: `backend/internal/auth/oauth_next.go`
- Create: `backend/internal/auth/oauth_next_test.go`

- [ ] **Step 1: RED - add state store tests**

Create `backend/internal/auth/oauth_state_test.go`:

```go
package auth

import (
	"errors"
	"testing"
	"time"
)

func TestMemoryOAuthStateStoreConsumesStateOnce(t *testing.T) {
	store := NewMemoryOAuthStateStore()
	state := "state-1"
	if err := store.Save(t.Context(), state, "/tasks", time.Minute); err != nil {
		t.Fatalf("save state: %v", err)
	}
	next, err := store.Consume(t.Context(), state)
	if err != nil {
		t.Fatalf("consume state: %v", err)
	}
	if next != "/tasks" {
		t.Fatalf("next = %q, want /tasks", next)
	}
	_, err = store.Consume(t.Context(), state)
	if !errors.Is(err, ErrOAuthStateInvalid) {
		t.Fatalf("second consume error = %v, want ErrOAuthStateInvalid", err)
	}
}

func TestMemoryOAuthStateStoreRejectsExpiredState(t *testing.T) {
	store := NewMemoryOAuthStateStore()
	if err := store.Save(t.Context(), "expired", "/notes", time.Nanosecond); err != nil {
		t.Fatalf("save state: %v", err)
	}
	time.Sleep(time.Millisecond)
	_, err := store.Consume(t.Context(), "expired")
	if !errors.Is(err, ErrOAuthStateInvalid) {
		t.Fatalf("expired state error = %v, want ErrOAuthStateInvalid", err)
	}
}

func TestMemoryOAuthStateStoreCleanupBatch(t *testing.T) {
	store := NewMemoryOAuthStateStore()
	for i := 0; i < 5; i++ {
		if err := store.Save(t.Context(), fmt.Sprintf("state-%d", i), "/", time.Nanosecond); err != nil {
			t.Fatalf("save state %d: %v", i, err)
		}
	}
	time.Sleep(time.Millisecond)
	deleted := store.CleanupExpired(time.Now().UTC(), 2)
	if deleted != 2 {
		t.Fatalf("deleted = %d, want 2", deleted)
	}
}
```

Add `fmt` import when the test file is created.

- [ ] **Step 2: RED - add safe next tests**

Create `backend/internal/auth/oauth_next_test.go`:

```go
package auth

import "testing"

func TestSanitizeOAuthNextAcceptsSafeRelativePaths(t *testing.T) {
	for _, input := range []string{"/", "/tasks", "/notes?id=1", "/editor/note_1#sync"} {
		if got := SanitizeOAuthNext(input); got != input {
			t.Fatalf("SanitizeOAuthNext(%q) = %q", input, got)
		}
	}
}

func TestSanitizeOAuthNextRejectsExternalAndBackslashPaths(t *testing.T) {
	for _, input := range []string{
		"",
		"https://evil.com/phishing",
		"//evil.com/phishing",
		`\evil.com`,
		`/\evil.com`,
		`/%5Cevil.com`,
		"tasks",
	} {
		if got := SanitizeOAuthNext(input); got != "/" {
			t.Fatalf("SanitizeOAuthNext(%q) = %q, want /", input, got)
		}
	}
}
```

- [ ] **Step 3: Verify RED**

```bash
cd backend && go test ./internal/auth -run 'OAuthState|SanitizeOAuthNext' -count=1 -v
```

Expected: FAIL because `NewMemoryOAuthStateStore`, `ErrOAuthStateInvalid`, and `SanitizeOAuthNext` do not exist.

- [ ] **Step 4: GREEN - implement state store**

Create `backend/internal/auth/oauth_state.go`:

```go
package auth

import (
	"context"
	"errors"
	"sync"
	"time"
)

var ErrOAuthStateInvalid = errors.New("oauth state invalid")

type OAuthStateStore interface {
	Save(ctx context.Context, state, next string, ttl time.Duration) error
	Consume(ctx context.Context, state string) (string, error)
}

type oauthStateEntry struct {
	next      string
	expiresAt time.Time
}

type MemoryOAuthStateStore struct {
	mu      sync.Mutex
	entries map[string]oauthStateEntry
}

func NewMemoryOAuthStateStore() *MemoryOAuthStateStore {
	return &MemoryOAuthStateStore{entries: map[string]oauthStateEntry{}}
}

func (s *MemoryOAuthStateStore) Save(ctx context.Context, state, next string, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[state] = oauthStateEntry{next: next, expiresAt: time.Now().UTC().Add(ttl)}
	return nil
}

func (s *MemoryOAuthStateStore) Consume(ctx context.Context, state string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[state]
	if !ok || time.Now().UTC().After(entry.expiresAt) {
		delete(s.entries, state)
		return "", ErrOAuthStateInvalid
	}
	delete(s.entries, state)
	return entry.next, nil
}

func (s *MemoryOAuthStateStore) CleanupExpired(now time.Time, limit int) int {
	if limit <= 0 {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	deleted := 0
	for key, entry := range s.entries {
		if deleted >= limit {
			break
		}
		if now.After(entry.expiresAt) {
			delete(s.entries, key)
			deleted++
		}
	}
	return deleted
}

func (s *MemoryOAuthStateStore) RunCleanup(ctx context.Context, interval time.Duration, batchSize int) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			s.CleanupExpired(now.UTC(), batchSize)
		}
	}
}
```

Create `backend/internal/auth/oauth_next.go`:

```go
package auth

import (
	"net/url"
	"strings"
)

func SanitizeOAuthNext(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || !strings.HasPrefix(value, "/") {
		return "/"
	}
	lower := strings.ToLower(value)
	if strings.HasPrefix(value, "//") || strings.HasPrefix(value, `/\`) || strings.Contains(value, `\`) || strings.Contains(lower, "%5c") {
		return "/"
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.IsAbs() || parsed.Host != "" {
		return "/"
	}
	return value
}
```

- [ ] **Step 5: Verify GREEN**

```bash
cd backend && go test ./internal/auth -run 'OAuthState|SanitizeOAuthNext' -count=1 -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/auth/oauth_state.go backend/internal/auth/oauth_state_test.go backend/internal/auth/oauth_next.go backend/internal/auth/oauth_next_test.go
git commit -m "feat: add oauth state and redirect guards"
```

---

## Task 3: Auth Identity Storage And PasswordSet

**Files:**
- Modify: `backend/internal/model/auth.go`
- Modify: `backend/internal/storage/store.go`
- Modify: `backend/internal/storage/contracttest/auth_contract_tests.go`
- Modify: `backend/internal/storage/sqlite/auth_migrations.go`
- Modify: `backend/internal/storage/sqlite/auth.go`
- Modify: `backend/internal/storage/postgres/auth.go`
- Create: `backend/db/migrations/postgres/0005_github_oauth.sql`
- Modify: `backend/internal/storage/postgres/migrations_test.go`

- [ ] **Step 1: RED - add storage contract tests**

Add to `RunAuthContractTests` in `backend/internal/storage/contracttest/auth_contract_tests.go`:

```go
t.Run("CreateUserPersistsPasswordSet", func(t *testing.T) {
	store := factory(t)
	defer store.Close()

	ctx := scopedContractContext(t, store)
	user := contractUser("auth_user_oauth", "oauth@example.com", "OAuth User", "user")
	user.PasswordSet = false
	workspace := contractWorkspace("auth_workspace_oauth", user.ID, "OAuth Workspace")

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
		return tx.Auth().AddWorkspaceMember(ctx, workspace.ID, user.ID, "owner")
	})
	if err != nil {
		t.Fatalf("create oauth user: %v", err)
	}

	loaded, err := store.Auth().GetUserByEmail(ctx, "oauth@example.com")
	if err != nil {
		t.Fatalf("get oauth user: %v", err)
	}
	if loaded.PasswordSet {
		t.Fatal("PasswordSet = true, want false")
	}
})

t.Run("AuthIdentityCreateGetUpdateAndList", func(t *testing.T) {
	store := factory(t)
	defer store.Close()

	ctx := scopedContractContext(t, store)
	seedAuthUserWorkspace(t, ctx, store, "auth_user_identity", "auth_workspace_identity", "identity@example.com")
	avatar := "https://avatars.githubusercontent.com/u/123"
	identity := &model.AuthIdentity{
		ID:             "identity_github_123",
		UserID:         "auth_user_identity",
		Provider:       "github",
		ProviderUserID: "123",
		ProviderLogin:  "octocat",
		Email:          "identity@example.com",
		AvatarURL:      &avatar,
	}
	if err := store.Auth().CreateAuthIdentity(ctx, identity); err != nil {
		t.Fatalf("create identity: %v", err)
	}
	loaded, err := store.Auth().GetAuthIdentity(ctx, "github", "123")
	if err != nil {
		t.Fatalf("get identity: %v", err)
	}
	if loaded.UserID != "auth_user_identity" || loaded.ProviderLogin != "octocat" {
		t.Fatalf("unexpected identity: %+v", loaded)
	}

	newAvatar := "https://avatars.githubusercontent.com/u/123?v=2"
	loaded.ProviderLogin = "octocat-renamed"
	loaded.Email = "new-primary@example.com"
	loaded.AvatarURL = &newAvatar
	loginAt := time.Now().UTC()
	if err := store.Auth().UpdateAuthIdentityFromProvider(ctx, loaded, loginAt); err != nil {
		t.Fatalf("update identity: %v", err)
	}

	updated, err := store.Auth().GetAuthIdentity(ctx, "github", "123")
	if err != nil {
		t.Fatalf("get updated identity: %v", err)
	}
	if updated.ProviderLogin != "octocat-renamed" || updated.Email != "new-primary@example.com" {
		t.Fatalf("provider fields not updated: %+v", updated)
	}
	if updated.LastLoginAt == nil || *updated.LastLoginAt == 0 {
		t.Fatalf("last login not set: %+v", updated)
	}

	all, err := store.Auth().ListAuthIdentitiesByUser(ctx, "auth_user_identity")
	if err != nil {
		t.Fatalf("list identities: %v", err)
	}
	if len(all) != 1 || all[0].ProviderUserID != "123" {
		t.Fatalf("listed identities = %+v", all)
	}
})
```

- [ ] **Step 2: Verify RED**

```bash
cd backend && go test ./internal/storage/sqlite -run 'TestSQLiteAuthContract/CreateUserPersistsPasswordSet|TestSQLiteAuthContract/AuthIdentityCreateGetUpdateAndList' -count=1 -v
```

Expected: FAIL because `model.AuthIdentity`, `PasswordSet`, and identity repository methods do not exist.

- [ ] **Step 3: GREEN - add model and repository interface**

Add to `backend/internal/model/auth.go`:

```go
PasswordSet bool `json:"password_set"`
```

Add `AuthIdentity`:

```go
type AuthIdentity struct {
	ID             string  `json:"id"`
	UserID         string  `json:"user_id"`
	Provider       string  `json:"provider"`
	ProviderUserID string  `json:"provider_user_id"`
	ProviderLogin  string  `json:"provider_login"`
	Email          string  `json:"email"`
	AvatarURL      *string `json:"avatar_url,omitempty"`
	CreatedAt      int64   `json:"created_at"`
	UpdatedAt      int64   `json:"updated_at"`
	LastLoginAt    *int64  `json:"last_login_at,omitempty"`
}
```

Add to `AuthRepository` in `backend/internal/storage/store.go`:

```go
GetAuthIdentity(context.Context, string, string) (*model.AuthIdentity, error)
CreateAuthIdentity(context.Context, *model.AuthIdentity) error
UpdateAuthIdentityFromProvider(context.Context, *model.AuthIdentity, time.Time) error
ListAuthIdentitiesByUser(context.Context, string) ([]model.AuthIdentity, error)
```

- [ ] **Step 4: GREEN - add SQLite schema and repository implementation**

In `createSQLiteAuthTables`, add `password_set INTEGER NOT NULL DEFAULT 1` to `users`.

Add `auth_identities` statements:

```sql
CREATE TABLE IF NOT EXISTS auth_identities (
	id TEXT PRIMARY KEY,
	user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	provider TEXT NOT NULL,
	provider_user_id TEXT NOT NULL,
	provider_login TEXT NOT NULL,
	email TEXT NOT NULL,
	avatar_url TEXT,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	last_login_at INTEGER,
	UNIQUE (provider, provider_user_id)
)
```

```sql
CREATE INDEX IF NOT EXISTS idx_auth_identities_user_id ON auth_identities(user_id)
```

```sql
CREATE INDEX IF NOT EXISTS idx_auth_identities_email_lower ON auth_identities(lower(email))
```

Add an SQLite schema upgrade helper:

```go
func ensureSQLitePasswordSetColumn(ctx context.Context, db *sql.DB) error {
	exists, err := sqliteColumnExists(ctx, db, "users", "password_set")
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	_, err = db.ExecContext(ctx, `ALTER TABLE users ADD COLUMN password_set INTEGER NOT NULL DEFAULT 1`)
	return err
}
```

Update `CreateUser`, auth select SQL, and scanner to include `password_set`.

Implement SQLite identity methods using these SQL shapes:

```sql
SELECT id, user_id, provider, provider_user_id, provider_login, email, avatar_url,
       created_at, updated_at, last_login_at
FROM auth_identities
WHERE provider = ? AND provider_user_id = ?
```

```sql
INSERT INTO auth_identities (
	id, user_id, provider, provider_user_id, provider_login, email,
	avatar_url, created_at, updated_at, last_login_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
```

```sql
UPDATE auth_identities
SET provider_login = ?, email = ?, avatar_url = ?, last_login_at = ?, updated_at = ?
WHERE provider = ? AND provider_user_id = ?
```

- [ ] **Step 5: Verify SQLite GREEN**

```bash
cd backend && go test ./internal/storage/sqlite -run 'TestSQLiteAuthContract/CreateUserPersistsPasswordSet|TestSQLiteAuthContract/AuthIdentityCreateGetUpdateAndList' -count=1 -v
```

Expected: PASS.

- [ ] **Step 6: RED - add PostgreSQL migration test**

Add to `backend/internal/storage/postgres/migrations_test.go`:

```go
func TestRunPostgresMigrationsAddsGitHubOAuthAuthSchema(t *testing.T) {
	schema := fmt.Sprintf("fs_test_github_oauth_%d", time.Now().UnixNano())
	db := openPostgresTestDB(t, schema)

	if err := runPostgresMigrations(db); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	assertColumnExists(t, db, schema, "users", "password_set")
	assertColumnType(t, db, schema, "auth_identities", "created_at", "timestamptz")
	assertColumnType(t, db, schema, "auth_identities", "updated_at", "timestamptz")
	assertColumnType(t, db, schema, "auth_identities", "last_login_at", "timestamptz")
	assertUniqueConstraintExists(t, db, schema, "auth_identities", "auth_identities_provider_provider_user_id_key")
}
```

- [ ] **Step 7: Verify PostgreSQL RED**

```bash
cd backend && go test ./internal/storage/postgres -run TestRunPostgresMigrationsAddsGitHubOAuthAuthSchema -count=1 -v
```

Expected: FAIL because migration `0005_github_oauth.sql` does not exist.

- [ ] **Step 8: GREEN - add PostgreSQL migration and repository**

Create `backend/db/migrations/postgres/0005_github_oauth.sql`:

```sql
ALTER TABLE users
  ADD COLUMN IF NOT EXISTS password_set BOOLEAN NOT NULL DEFAULT true;

CREATE TABLE IF NOT EXISTS auth_identities (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  provider TEXT NOT NULL,
  provider_user_id TEXT NOT NULL,
  provider_login TEXT NOT NULL,
  email TEXT NOT NULL,
  avatar_url TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_login_at TIMESTAMPTZ,
  UNIQUE (provider, provider_user_id)
);

CREATE INDEX IF NOT EXISTS idx_auth_identities_user_id
  ON auth_identities(user_id);

CREATE INDEX IF NOT EXISTS idx_auth_identities_email_lower
  ON auth_identities(lower(email));
```

Mirror the SQLite repository methods in `backend/internal/storage/postgres/auth.go`, using `$1`, `$2`, and `unixToTime`/`timeToUnix` helpers already present in that package.

- [ ] **Step 9: Verify storage GREEN**

```bash
cd backend && go test ./internal/storage/sqlite -run TestSQLiteAuthContract -count=1 -v
cd backend && go test ./internal/storage/postgres -run 'TestRunPostgresMigrationsAddsGitHubOAuthAuthSchema|TestPostgresAuthContract' -count=1 -v
```

Expected: SQLite PASS. PostgreSQL PASS when local PostgreSQL test setup is available; if PostgreSQL is unavailable, record the connection error and run it before merge.

- [ ] **Step 10: Commit**

```bash
git add backend/internal/model/auth.go backend/internal/storage/store.go backend/internal/storage/contracttest/auth_contract_tests.go backend/internal/storage/sqlite/auth_migrations.go backend/internal/storage/sqlite/auth.go backend/internal/storage/postgres/auth.go backend/db/migrations/postgres/0005_github_oauth.sql backend/internal/storage/postgres/migrations_test.go
git commit -m "feat: add auth identities storage"
```

---

## Task 4: PasswordSet Change Password Behavior

**Files:**
- Modify: `backend/internal/handler/auth.go`
- Modify: `backend/internal/handler/auth_test.go`

- [ ] **Step 1: RED - add password-not-set test**

Add to `backend/internal/handler/auth_test.go`:

```go
func TestChangePasswordRejectsUsersWithoutPasswordSet(t *testing.T) {
	env := setupAuthHandlerEnv(t)
	user := seedAuthHandlerUser(t, env.store, func(user *model.User) {
		user.PasswordSet = false
		user.PasswordHash = mustHashPassword(t, "random-oauth-only-password")
	})
	token := createAuthHandlerSession(t, env, user.ID, user.DefaultWorkspaceID)
	body := strings.NewReader(`{"current_password":"anything123","new_password":"newpass123"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/change-password", body)
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(auth.ContextWithIdentity(req.Context(), auth.RequestIdentity{
		UserID:      user.ID,
		SessionID:   "handler_session",
		WorkspaceID: user.DefaultWorkspaceID,
	}))
	req.AddCookie(&http.Cookie{Name: env.auth.Cookie.Name, Value: token})
	w := httptest.NewRecorder()

	ChangePassword(env.store).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", w.Code, w.Body.String())
	}
	assertHandlerErrorCode(t, w.Body.String(), "PASSWORD_NOT_SET")
}
```

- [ ] **Step 2: Verify RED**

```bash
cd backend && go test ./internal/handler -run TestChangePasswordRejectsUsersWithoutPasswordSet -count=1 -v
```

Expected: FAIL because `ChangePassword` verifies the current password and returns `INVALID_CREDENTIALS`.

- [ ] **Step 3: GREEN - check PasswordSet before VerifyPassword**

In `ChangePassword`, after loading user and before `auth.VerifyPassword`:

```go
if !user.PasswordSet {
	errorResponse(c, http.StatusBadRequest, "PASSWORD_NOT_SET", "password has not been set for this account")
	return
}
```

When password changes successfully, existing `UpdateUserPassword(ctx, identity.UserID, newHash, false)` remains correct and repository must set `password_set=true` internally for password updates.

- [ ] **Step 4: Verify GREEN**

```bash
cd backend && go test ./internal/handler -run 'TestChangePasswordRejectsUsersWithoutPasswordSet|TestChangePassword' -count=1 -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/handler/auth.go backend/internal/handler/auth_test.go
git commit -m "feat: reject password change for oauth-only users"
```

---

## Task 5: GitHub OAuth Handler

**Files:**
- Create: `backend/internal/handler/github_oauth.go`
- Create: `backend/internal/handler/github_oauth_test.go`

- [ ] **Step 1: RED - add providers test**

Create `backend/internal/handler/github_oauth_test.go` with:

```go
package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/config"
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
		Providers []string `json:"providers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Providers) != 1 || body.Providers[0] != "github" {
		t.Fatalf("providers = %#v, want [github]", body.Providers)
	}
}
```

- [ ] **Step 2: RED - add disabled redirect tests**

Add:

```go
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
```

- [ ] **Step 3: Verify RED**

```bash
cd backend && go test ./internal/handler -run 'TestAuthProvidersReturnsGitHubWhenAvailable|TestGitHubOAuthStartRedirectsDisabled|TestGitHubOAuthCallbackRedirectsDisabled' -count=1 -v
```

Expected: FAIL because GitHub OAuth handlers do not exist.

- [ ] **Step 4: GREEN - implement providers and disabled behavior**

Create `backend/internal/handler/github_oauth.go`:

```go
package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/config"
	"github.com/hujinrun/flowspace/internal/storage"
)

type GitHubClient interface {
	ExchangeCode(ctx context.Context, code string) (string, error)
	FetchProfile(ctx context.Context, token string) (*GitHubProfile, error)
	FetchEmails(ctx context.Context, token string) ([]GitHubEmail, error)
}

type GitHubProfile struct {
	ID        int64
	Login     string
	Name      string
	AvatarURL string
}

type GitHubEmail struct {
	Email    string
	Primary  bool
	Verified bool
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
		c.Redirect(http.StatusFound, "/")
	}
}

func GitHubOAuthCallback(store storage.Store, authCfg config.AuthConfig, stateStore auth.OAuthStateStore, client GitHubClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !authCfg.GitHub.Available() {
			c.Redirect(http.StatusFound, "/login?oauth_error=github_disabled")
			return
		}
		c.Redirect(http.StatusFound, "/login?oauth_error=github_state_invalid")
	}
}
```

Add `context` import after compiling.

- [ ] **Step 5: Verify GREEN**

```bash
cd backend && go test ./internal/handler -run 'TestAuthProvidersReturnsGitHubWhenAvailable|TestGitHubOAuthStartRedirectsDisabled|TestGitHubOAuthCallbackRedirectsDisabled' -count=1 -v
```

Expected: PASS.

- [ ] **Step 6: RED - add start redirect and logged-in behavior tests**

Add tests:

```go
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
```

- [ ] **Step 7: Verify RED**

```bash
cd backend && go test ./internal/handler -run 'TestGitHubOAuthStartSavesStateAndRedirectsToGitHub|TestGitHubOAuthStartIgnoresAlreadyLoggedInUsers' -count=1 -v
```

Expected: FAIL because start handler does not save state or inspect existing session.

- [ ] **Step 8: GREEN - implement start handler**

Implement:

```go
next := auth.SanitizeOAuthNext(c.Query("next"))
if hasValidSession(c.Request.Context(), store, authCfg, c.Request) {
	c.Redirect(http.StatusFound, next)
	return
}
state, err := auth.GenerateSessionToken()
if err != nil {
	c.Redirect(http.StatusFound, "/login?oauth_error=github_state_invalid")
	return
}
if err := stateStore.Save(c.Request.Context(), state, next, authCfg.GitHub.StateTTL); err != nil {
	c.Redirect(http.StatusFound, "/login?oauth_error=github_state_invalid")
	return
}
authorizeURL := githubAuthorizeURL(authCfg.GitHub, state)
c.Redirect(http.StatusFound, authorizeURL)
```

`githubAuthorizeURL` must set:

```go
client_id
redirect_uri
state
scope = "read:user user:email"
```

- [ ] **Step 9: Verify start GREEN**

```bash
cd backend && go test ./internal/handler -run 'TestGitHubOAuthStart' -count=1 -v
```

Expected: PASS.

- [ ] **Step 10: RED - add callback success tests**

Add:

```go
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
	sessionCookie := requireResponseCookie(t, w.Result(), env.auth.Cookie.Name)
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
	_ = env.stateStore.Save(t.Context(), state, "/", time.Minute)
	env.github.profile = GitHubProfile{ID: 456, Login: "secure", Name: "Secure User"}
	env.github.emails = []GitHubEmail{{Email: "secure@example.com", Primary: true, Verified: true}}

	req := httptest.NewRequest(http.MethodGet, "/api/auth/github/callback?code=code-ok&state="+state, nil)
	req.AddCookie(&http.Cookie{Name: env.auth.Cookie.Name, Value: oldToken})
	w := httptest.NewRecorder()
	env.router.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", w.Code)
	}
	oldHash, _ := auth.HashSessionToken(env.auth.SessionSecret, oldToken)
	if _, err := env.store.Auth().GetSessionByTokenHash(t.Context(), oldHash); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("old session lookup error = %v, want sql.ErrNoRows", err)
	}
	newCookie := requireResponseCookie(t, w.Result(), env.auth.Cookie.Name)
	if newCookie.Value == oldToken {
		t.Fatal("new session reused old token")
	}
}
```

- [ ] **Step 11: Verify callback RED**

```bash
cd backend && go test ./internal/handler -run 'TestGitHubOAuthCallbackAutoCreatesUserWorkspaceIdentityAndSession|TestGitHubOAuthCallbackRevokesExistingSessionBeforeNewSession' -count=1 -v
```

Expected: FAIL because callback does not exchange code, create users, or issue sessions.

- [ ] **Step 12: GREEN - implement callback**

Implement callback with this order:

```go
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
```

`resolveGitHubUser` must:

- first lookup identity by `provider=github` and `provider_user_id`;
- update `last_login_at`, `provider_login`, `email`, and `avatar_url` on existing identity;
- fallback to `GetUserByEmail` with case-insensitive email matching;
- bind identity to existing user if email matches;
- auto-create user only when `authCfg.GitHub.AutoCreateUsers` is true;
- auto-create user transaction with `CreateUser`, `CreateWorkspace`, `SetDefaultWorkspace`, `AddWorkspaceMember`, `auth.ContextWithWorkspaceScope`, `provisioning.EnsureDefaultWorkspaceData`, `CreateAuthIdentity`, and audit events.

- [ ] **Step 13: Verify callback GREEN**

```bash
cd backend && go test ./internal/handler -run 'TestGitHubOAuthCallback' -count=1 -v
```

Expected: PASS.

- [ ] **Step 14: RED/GREEN error cases**

Add and implement one test at a time:

```go
func TestGitHubOAuthCallbackRejectsBadState(t *testing.T)
func TestGitHubOAuthCallbackRedirectsExchangeFailure(t *testing.T)
func TestGitHubOAuthCallbackRedirectsProfileFailureForUserAPI5xx(t *testing.T)
func TestGitHubOAuthCallbackRedirectsProfileFailureForEmailsAPI5xx(t *testing.T)
func TestGitHubOAuthCallbackRejectsNoVerifiedEmail(t *testing.T)
func TestGitHubOAuthCallbackRejectsNewUserWhenAutoCreateDisabled(t *testing.T)
func TestGitHubOAuthCallbackLinksExistingUserByEmailCaseInsensitive(t *testing.T)
func TestGitHubOAuthCallbackUpdatesIdentityProviderFieldsOnLogin(t *testing.T)
```

Run after each new RED test:

```bash
cd backend && go test ./internal/handler -run '<exact test name>' -count=1 -v
```

Expected RED for each: FAIL with the specific missing behavior. Expected GREEN for each: PASS after the minimal callback change.

- [ ] **Step 15: RED - add GitHub HTTP client tests**

Add:

```go
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
		t.Fatalf("token = %q", token)
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
```

- [ ] **Step 16: Verify HTTP client RED**

```bash
cd backend && go test ./internal/handler -run 'TestGitHubHTTPClientExchangeProfileAndEmails|TestGitHubHTTPClientReturnsProfileErrorFor5xx' -count=1 -v
```

Expected: FAIL because `NewGitHubHTTPClient` and concrete client methods do not exist.

- [ ] **Step 17: GREEN - implement GitHub HTTP client**

Add to `backend/internal/handler/github_oauth.go`:

```go
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
```

Implement methods with these request rules:

```go
func (c *GitHubHTTPClient) ExchangeCode(ctx context.Context, code string) (string, error)
func (c *GitHubHTTPClient) FetchProfile(ctx context.Context, token string) (*GitHubProfile, error)
func (c *GitHubHTTPClient) FetchEmails(ctx context.Context, token string) ([]GitHubEmail, error)
```

- `ExchangeCode` sends JSON to `tokenURL` with `client_id`, `client_secret`, `code`, and `redirect_uri`.
- `ExchangeCode` sets `Accept: application/json`.
- `FetchProfile` sends `GET {apiBaseURL}/user` with `Authorization: Bearer <token>`.
- `FetchEmails` sends `GET {apiBaseURL}/user/emails` with `Authorization: Bearer <token>`.
- Any non-2xx status returns an error.
- Invalid JSON returns an error.
- The token string is never logged or included in error messages.

- [ ] **Step 18: Verify HTTP client GREEN**

```bash
cd backend && go test ./internal/handler -run 'TestGitHubHTTPClient' -count=1 -v
```

Expected: PASS.

- [ ] **Step 19: Commit**

```bash
git add backend/internal/handler/github_oauth.go backend/internal/handler/github_oauth_test.go
git commit -m "feat: add github oauth handlers"
```

---

## Task 6: Router Wiring And Server State Store

**Files:**
- Modify: `backend/internal/router/router.go`
- Modify: `backend/internal/router/auth_routes_test.go`
- Modify: `backend/cmd/server/main.go`

- [ ] **Step 1: RED - add route registration tests**

Add to `backend/internal/router/auth_routes_test.go`:

```go
func TestGitHubOAuthRoutesAreRegisteredAsPublicAuthRoutes(t *testing.T) {
	env := setupRouterAuthEnv(t, false)
	env.auth.GitHub = config.GitHubOAuthConfig{
		Enabled:         true,
		ClientID:        "client-id",
		ClientSecret:    "client-secret",
		RedirectURL:     "https://example.com/api/auth/github/callback",
		AutoCreateUsers: true,
		StateTTL:        time.Minute,
	}
	env.config.Auth = env.auth
	env.config.OAuthStateStore = authpkg.NewMemoryOAuthStateStore()

	registered := registeredRoutes(Setup(env.config))

	for _, route := range []string{
		"GET /api/auth/providers",
		"GET /api/auth/github/start",
		"GET /api/auth/github/callback",
	} {
		if !registered[route] {
			t.Fatalf("route %s is not registered", route)
		}
	}
}

func TestGitHubOAuthStartDoesNotRequirePasswordSettled(t *testing.T) {
	env := setupRouterAuthEnv(t, false, withRouterMustChangePassword(true))
	env.auth.GitHub = config.GitHubOAuthConfig{
		Enabled:         true,
		ClientID:        "client-id",
		ClientSecret:    "client-secret",
		RedirectURL:     "https://example.com/api/auth/github/callback",
		AutoCreateUsers: true,
		StateTTL:        time.Minute,
	}
	env.config.Auth = env.auth
	env.config.OAuthStateStore = authpkg.NewMemoryOAuthStateStore()

	req := httptest.NewRequest(http.MethodGet, "/api/auth/github/start?next=/tasks", nil)
	w := httptest.NewRecorder()
	Setup(env.config).ServeHTTP(w, req)

	if w.Code == http.StatusForbidden {
		t.Fatalf("github start went through protected middleware: body = %s", w.Body.String())
	}
}
```

- [ ] **Step 2: Verify RED**

```bash
cd backend && go test ./internal/router -run 'TestGitHubOAuthRoutesAreRegisteredAsPublicAuthRoutes|TestGitHubOAuthStartDoesNotRequirePasswordSettled' -count=1 -v
```

Expected: FAIL because router config does not include state store and routes are not registered.

- [ ] **Step 3: GREEN - register public routes**

Add to `router.Config`:

```go
OAuthStateStore auth.OAuthStateStore
GitHubClient    handler.GitHubClient
```

Register in public `authRoutes`:

```go
authRoutes.GET("/providers", handler.AuthProviders(cfg.Auth))
authRoutes.GET("/github/start", handler.GitHubOAuthStart(cfg.Store, cfg.Auth, cfg.OAuthStateStore))
authRoutes.GET("/github/callback", handler.GitHubOAuthCallback(cfg.Store, cfg.Auth, cfg.OAuthStateStore, cfg.GitHubClient))
```

Do not place these routes under `protected`.

- [ ] **Step 4: GREEN - wire server state store**

In `backend/cmd/server/main.go`, before `router.Setup`:

```go
oauthStateStore := auth.NewMemoryOAuthStateStore()
oauthStateCtx, stopOAuthStateCleanup := context.WithCancel(context.Background())
defer stopOAuthStateCleanup()
go oauthStateStore.RunCleanup(oauthStateCtx, 2*time.Minute, 1000)
```

Pass:

```go
r := router.Setup(router.Config{
	Store:           store,
	Auth:            authCfg,
	OAuthStateStore: oauthStateStore,
	GitHubClient:    handler.NewGitHubHTTPClient(authCfg.GitHub),
})
```

- [ ] **Step 5: Verify GREEN**

```bash
cd backend && go test ./internal/router -run 'GitHubOAuth|AuthRoutesAreRegistered' -count=1 -v
cd backend && go test ./cmd/server -count=1
```

Expected: PASS. If `./cmd/server` has no tests, expected output is `? github.com/hujinrun/flowspace/cmd/server [no test files]`.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/router/router.go backend/internal/router/auth_routes_test.go backend/cmd/server/main.go
git commit -m "feat: register github oauth routes"
```

---

## Task 7: Frontend Providers, GitHub Link, And oauth_error

**Files:**
- Modify: `frontend/src/api/auth.ts`
- Modify: `frontend/src/routes/Login.tsx`
- Modify: `frontend/src/routes/Login.test.tsx`

- [ ] **Step 1: RED - add Login tests**

Update `frontend/src/routes/Login.test.tsx` mock:

```ts
vi.mock('../api/auth', () => ({
  login: vi.fn(),
  listAuthProviders: vi.fn(),
}))
```

Import:

```ts
import { listAuthProviders, login } from '../api/auth'
```

Add beforeEach:

```ts
vi.mocked(listAuthProviders).mockReset()
vi.mocked(listAuthProviders).mockResolvedValue(['github'])
```

Add tests:

```tsx
it('renders GitHub login as a full-page link when provider is enabled', async () => {
  render(
    <MemoryRouter initialEntries={['/login?next=/tasks']}>
      <Login />
    </MemoryRouter>,
  )

  const link = await screen.findByRole('link', { name: '使用 GitHub 登录' })
  expect(link).toHaveAttribute('href', '/api/auth/github/start?next=%2Ftasks')
})

it('hides GitHub login when provider list does not include GitHub', async () => {
  vi.mocked(listAuthProviders).mockResolvedValue([])

  render(
    <MemoryRouter initialEntries={['/login']}>
      <Login />
    </MemoryRouter>,
  )

  await waitFor(() => {
    expect(screen.queryByRole('link', { name: '使用 GitHub 登录' })).not.toBeInTheDocument()
  })
})

it('shows oauth_error message from the query string', async () => {
  render(
    <MemoryRouter initialEntries={['/login?oauth_error=github_no_verified_email']}>
      <Login />
    </MemoryRouter>,
  )

  expect(await screen.findByRole('alert')).toHaveTextContent('GitHub 账号没有已验证邮箱')
})

it('shows generic oauth_error message for unknown error codes', async () => {
  render(
    <MemoryRouter initialEntries={['/login?oauth_error=unknown_code']}>
      <Login />
    </MemoryRouter>,
  )

  expect(await screen.findByRole('alert')).toHaveTextContent('GitHub 登录失败，请重新尝试')
})
```

- [ ] **Step 2: Verify RED**

```bash
cd frontend && npm run test -- src/routes/Login.test.tsx
```

Expected: FAIL because `listAuthProviders` does not exist and GitHub login is a button.

- [ ] **Step 3: GREEN - add providers API**

In `frontend/src/api/auth.ts`:

```ts
export type AuthProvider = 'github'

export async function listAuthProviders() {
  const res = await api.get<{ providers: AuthProvider[] }>('/api/auth/providers')
  return res.data.providers
}
```

- [ ] **Step 4: GREEN - update Login**

In `Login.tsx`, import `useEffect` and `listAuthProviders`:

```ts
import { type FormEvent, useEffect, useMemo, useState } from 'react'
import { listAuthProviders, login } from '../api/auth'
```

Add state:

```ts
const [providers, setProviders] = useState<string[]>([])
```

Add effects:

```ts
useEffect(() => {
  let cancelled = false
  listAuthProviders()
    .then((items) => {
      if (!cancelled) setProviders(items)
    })
    .catch(() => {
      if (!cancelled) setProviders([])
    })
  return () => {
    cancelled = true
  }
}, [])

const oauthError = searchParams.get('oauth_error')

useEffect(() => {
  if (oauthError) setError(oauthErrorMessage(oauthError))
}, [oauthError])

const githubLoginHref = useMemo(() => {
  return `/api/auth/github/start?next=${encodeURIComponent(safeNext(searchParams.get('next')))}`
}, [searchParams])
```

Replace GitHub button:

```tsx
{providers.includes('github') && (
  <a className="auth-oauth-btn" href={githubLoginHref}>
    <GithubIcon />
    使用 GitHub 登录
  </a>
)}
```

Add message helper:

```ts
function oauthErrorMessage(code: string) {
  const messages: Record<string, string> = {
    github_disabled: 'GitHub 登录暂未启用',
    github_state_invalid: '登录状态已过期，请重新尝试',
    github_exchange_failed: 'GitHub 授权失败，请稍后重试',
    github_profile_failed: '无法读取 GitHub 用户信息',
    github_no_verified_email: 'GitHub 账号没有已验证邮箱',
    github_auto_create_disabled: '当前暂不允许 GitHub 新账号自动注册',
    github_create_user_failed: '创建账号失败，请稍后重试',
  }
  return messages[code] ?? 'GitHub 登录失败，请重新尝试'
}
```

- [ ] **Step 5: Verify GREEN**

```bash
cd frontend && npm run test -- src/routes/Login.test.tsx
```

Expected: PASS.

- [ ] **Step 6: Run frontend narrow build check**

```bash
cd frontend && npm run build
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add frontend/src/api/auth.ts frontend/src/routes/Login.tsx frontend/src/routes/Login.test.tsx
git commit -m "feat: add github login entrypoint"
```

---

## Task 8: Security And End-To-End Regression Coverage

**Files:**
- Modify: `backend/internal/handler/github_oauth_test.go`
- Modify: `backend/internal/router/auth_routes_test.go`
- Modify: `frontend/src/routes/Login.test.tsx`

- [ ] **Step 1: RED - add focused security tests**

Add these backend tests if they are not already covered:

```go
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
	_ = env.stateStore.Save(t.Context(), "state-token", "/", time.Minute)
	env.github.accessToken = "github-access-token-secret"
	env.github.profile = GitHubProfile{ID: 1000, Login: "audit-safe"}
	env.github.emails = []GitHubEmail{{Email: "audit-safe@example.com", Primary: true, Verified: true}}

	req := httptest.NewRequest(http.MethodGet, "/api/auth/github/callback?code=ok&state=state-token", nil)
	w := httptest.NewRecorder()
	env.router.ServeHTTP(w, req)

	metadataJSON := allAuditMetadataJSON(t, env.store)
	if strings.Contains(metadataJSON, "github-access-token-secret") {
		t.Fatalf("audit metadata contains access token: %s", metadataJSON)
	}
}
```

Add frontend test:

```tsx
it('does not render GitHub login when providers request fails', async () => {
  vi.mocked(listAuthProviders).mockRejectedValue(new Error('offline'))

  render(
    <MemoryRouter initialEntries={['/login']}>
      <Login />
    </MemoryRouter>,
  )

  await waitFor(() => {
    expect(screen.queryByRole('link', { name: '使用 GitHub 登录' })).not.toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Verify RED**

```bash
cd backend && go test ./internal/handler -run 'TestGitHubOAuthCallbackRejectsExternalNextFromState|TestGitHubOAuthCallbackDoesNotPersistAccessTokenInAuditMetadata' -count=1 -v
cd frontend && npm run test -- src/routes/Login.test.tsx
```

Expected: backend and frontend tests fail if the security behavior is not implemented.

- [ ] **Step 3: GREEN - fix only failing security behavior**

Apply the smallest changes needed:

- Always call `auth.SanitizeOAuthNext(next)` immediately before redirecting from callback.
- Exclude `code`, `access_token`, `session_token`, `cookie`, `authorization`, and `client_secret` from audit metadata.
- Keep frontend providers failure handling as `setProviders([])`.

- [ ] **Step 4: Verify GREEN**

```bash
cd backend && go test ./internal/handler ./internal/router -run 'GitHubOAuth|AuthRoutes' -count=1 -v
cd frontend && npm run test -- src/routes/Login.test.tsx
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/handler/github_oauth.go backend/internal/handler/github_oauth_test.go backend/internal/router/auth_routes_test.go frontend/src/routes/Login.tsx frontend/src/routes/Login.test.tsx
git commit -m "test: cover github oauth security regressions"
```

---

## Task 9: Full Verification And Documentation Sync

**Files:**
- Modify: `docs/github-oauth-login-design.md` only if implementation behavior differs from the design

- [ ] **Step 1: Run backend tests**

```bash
cd backend && go test ./...
```

Expected: PASS.

- [ ] **Step 2: Run frontend tests and build**

```bash
cd frontend && npm run test && npm run build
```

Expected: PASS.

- [ ] **Step 3: Scan for secret leakage risks**

```bash
rg -n "access_token|client_secret|authorization|session_token|Set-Cookie|fs_session" backend/internal/handler backend/internal/auth backend/internal/storage
```

Expected: every hit is reviewed. No GitHub access token, OAuth code, client secret, session token, or cookie value is written into audit metadata, logs, database identity rows, or frontend responses.

- [ ] **Step 4: Scan route placement**

```bash
rg -n "github/start|github/callback|providers|RequirePasswordSettled|protected := api.Group" backend/internal/router/router.go
```

Expected: `providers`, `github/start`, and `github/callback` appear in public `authRoutes`; they do not appear under `protected`.

- [ ] **Step 5: Confirm final worktree**

```bash
git status --short
```

Expected: only intentional changes remain.

- [ ] **Step 6: Commit documentation sync if needed**

```bash
git add docs/github-oauth-login-design.md
git commit -m "docs: sync github oauth implementation design"
```

Use this commit only if Step 3 or implementation details required a design update. If design already matches the implementation, skip this commit.

---

## Final Acceptance Checklist

- [ ] `GET /api/auth/providers` returns `["github"]` only when GitHub OAuth config is complete.
- [ ] `GET /api/auth/github/start` is public and never uses `RequirePasswordSettled`.
- [ ] `GET /api/auth/github/start` redirects disabled config to `/login?oauth_error=github_disabled`.
- [ ] `GET /api/auth/github/start` redirects already-authenticated users to safe `next` or `/`.
- [ ] OAuth state is random, single-use, TTL-bound, and cleanup is batch-limited.
- [ ] OAuth callback rejects missing, expired, or reused state.
- [ ] OAuth callback handles GitHub token exchange failure as `github_exchange_failed`.
- [ ] OAuth callback handles GitHub `/user` and `/user/emails` failures as `github_profile_failed`.
- [ ] OAuth callback rejects accounts without a verified email.
- [ ] Existing identity login updates `last_login_at` and refreshes provider login, email, and avatar when changed.
- [ ] Existing local user with matching verified email gets a GitHub identity binding.
- [ ] New GitHub user auto-creation creates user, workspace, default workspace ID, owner membership, default workspace data, identity, audit events, and session.
- [ ] New GitHub user auto-creation writes `password_set=false`.
- [ ] `ChangePassword` returns `PASSWORD_NOT_SET` for `password_set=false` users.
- [ ] Callback revokes valid old session from incoming `fs_session` before issuing a new session.
- [ ] Callback always generates a new session token and overwrites the cookie.
- [ ] Callback redirects only to sanitized same-site relative `next`.
- [ ] GitHub access token is not stored, logged, or returned.
- [ ] Frontend hides GitHub login when providers endpoint returns no GitHub provider or fails.
- [ ] Frontend GitHub login is an `<a>` link with `/api/auth/github/start?next=...`.
- [ ] Frontend displays all documented `oauth_error` messages and a generic fallback.
- [ ] Full backend tests pass.
- [ ] Full frontend tests and build pass.
