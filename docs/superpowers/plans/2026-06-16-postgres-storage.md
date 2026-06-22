# Pluggable Storage Provider and PostgreSQL Upgrade Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Introduce a pluggable storage provider layer for FlowSpace, keep SQLite as a compatible provider, and add PostgreSQL as the recommended production provider while preserving current HTTP API behavior.

**Architecture:** Add `backend/internal/storage` with `Provider`, `Store`, domain repository interfaces, provider registry, capabilities, and transaction support. Keep the existing `repository` package as a facade that forwards to an active `storage.Store` during Phase 1. Implement `storage/sqlite` as the compatibility provider and `storage/postgres` as the PostgreSQL provider using native structures such as `TEXT[]`, `JSONB`, `TSTZRANGE`, GIN/GiST indexes, and a unified search index.

**Tech Stack:** Go 1.26, Gin, `database/sql` for SQLite compatibility, `github.com/jackc/pgx/v5/stdlib` for PostgreSQL during the initial provider phase, `github.com/lib/pq` for PostgreSQL array values/scans while using `database/sql`, PostgreSQL 10.6-compatible SQL migrations for the current test server, Vitest/frontend unchanged.

---

## File Structure

- Create `docker-compose.postgres.yml`: local PostgreSQL service for test/prod development.
- Create `docker/postgres/init-flowspace.sql`: initialize `flowspace_test` and `flowspace_prod`.
- Create `backend/db/migrations/postgres/0001_init_postgres.sql`: PostgreSQL schema and indexes.
- Create `backend/db/migrations/sqlite/`: SQLite migration directory for future schema files; legacy migration helper remains the first implementation.
- Create `backend/internal/storage/store.go`: `Provider`, `Store`, `Capabilities`, and domain repository interfaces.
- Create `backend/internal/storage/config.go`: `FLOWSPACE_DATABASE_DRIVER`, `FLOWSPACE_DATABASE_URL`, and `FLOWSPACE_SQLITE_PATH` config parsing.
- Create `backend/internal/storage/registry.go`: provider registration and selection.
- Create `backend/internal/storage/contract_tests.go`: shared provider behavior test suite.
- Create `backend/internal/storage/postgres/provider.go`: PostgreSQL provider open/validate/migrate.
- Create `backend/internal/storage/postgres/migrations.go`: transaction-backed `schema_migrations` runner.
- Create `backend/internal/storage/postgres/types.go`: helpers for time, bool, tags, JSONB compatibility.
- Create `backend/internal/storage/postgres/builder.go`: PostgreSQL placeholder, `IN`, and dynamic `SET` builders.
- Create `backend/internal/storage/postgres/testdb_test.go`: PostgreSQL integration test helper.
- Create `backend/internal/storage/sqlite/provider.go`: SQLite compatibility provider.
- Create `backend/internal/storage/sqlite/legacy_migrations.go`: extracted legacy SQLite schema upgrade helper.
- Modify `backend/internal/repository/*.go`: turn package-level repository functions into facade methods that call active `storage.Store`.
- Modify storage provider domain files by provider: `notes.go`, `search.go`, `tasks.go`, `events.go`, `inbox.go`, `roadmaps.go`, `sync.go`.
- Create `backend/cmd/migrate_sqlite_to_pg/main.go`: one-time SQLite-to-PostgreSQL migration command.
- Create `backend/internal/migration/sqlite_to_pg.go`: migration implementation.
- Create `backend/internal/migration/sqlite_to_pg_test.go`: migration fixture tests.
- Modify `backend/cmd/server/main.go` and `backend/cmd/seed/main.go`: open configured storage provider and call `repository.SetStore`.
- Modify `scripts/start-flowspace.mjs`, `Makefile`, `backend/Makefile`: pass storage driver and provider-specific config by environment.
- Modify `README.md` and `docs/service-ports.md`: document storage provider selection and PostgreSQL database separation.

## Assumptions

- Phase 1 keeps existing JSON API shapes. For example, `model.Note.Tags` remains `string` even though PostgreSQL stores tags as `TEXT[]`.
- Phase 1 uses a repository facade to minimize handler/service rewrite. Existing package-level repository calls remain, but they forward to active `storage.Store`.
- Phase 1 facade may use `context.Background()` for old calls. Any new or touched handler/service code should pass request context into storage-aware service methods.
- Phase 1 uses `database/sql` with `pgx` stdlib inside the PostgreSQL provider to minimize rewrite blast radius. A later cleanup can move the provider internals to `pgxpool`.
- Tests that need PostgreSQL require `FLOWSPACE_TEST_DATABASE_URL`. If the variable is absent, PostgreSQL integration tests skip with a clear message.
- Current PostgreSQL test server is `192.168.1.20:19588` and reports PostgreSQL 10.6. Do not use PG12+ generated columns in migrations; store `events.time_range` and `search_index.search_vector` as normal columns maintained by the provider.
- SQLite remains a runtime-compatible provider for local/lightweight use and contract tests. PostgreSQL remains the recommended production provider.
- Existing SQLite files are retained as migration sources and backups even after production switches to PostgreSQL.
- All later tasks that mention PostgreSQL repository tests must open a configured `storage.Store` through provider test helpers. Do not add new runtime code paths that call `repository.InitPostgres`.

---

## Execution Order and Checkpoints

Follow this order exactly. Do not start a later phase until the checkpoint for the current phase is green.

1. **Storage abstraction foundation:** Complete Tasks 1, 1A, and 1B.
   Checkpoint: `go test ./internal/storage ./internal/repository -run "TestLoadStorageConfig|TestValidateStorageConfig|TestRegistry|TestSetStore|TestActiveStore" -count=1` passes.

2. **SQLite compatibility provider:** Complete Task 1C.
   Checkpoint: SQLite provider opens a temporary DB, runs legacy migrations, reports capabilities, and passes `TestProvider`.

3. **PostgreSQL infrastructure:** Complete Tasks 2, 3, 4, and 5.
   Checkpoint: Docker PostgreSQL is healthy, migrations run twice idempotently, PostgreSQL provider opens through the registry, and helper tests pass.

4. **Provider contract harness:** Complete Task 5A.
   Checkpoint: `go test ./internal/storage/... -run "TestSQLiteStoreContract|TestPostgresStoreContract" -count=1` passes.

5. **Domain migration by behavior:** Complete Tasks 6 through 10 in order: notes/search, tasks/today, events/inbox, roadmaps, sync.
   Checkpoint after each domain: extend provider contract tests first, then run the domain's PostgreSQL-specific tests and the relevant service tests.

6. **SQLite-to-PostgreSQL migration command:** Complete Task 11 only after all domain repositories exist in the PostgreSQL provider.
   Checkpoint: full migration fixture covers all tables, rebuilds search, validates WAL-safe copy, preflight, transaction rollback, and seed conflict behavior.

7. **Runtime scripts and docs:** Complete Task 12.
   Checkpoint: test and prod startup commands explicitly pass `FLOWSPACE_DATABASE_DRIVER` plus provider-specific config.

8. **End-to-end verification:** Complete Task 13.
   Checkpoint: backend tests, frontend tests, smoke checks, migration dry-run, and startup logs all confirm the selected provider and database.

Branching rule: if a domain task exposes missing interface methods in `storage.Store`, add the interface methods and contract tests in the same task before implementing provider SQL. Do not add provider-specific behavior directly to handler/service code.

---

### Task 1: Add Storage Provider Configuration

**Files:**
- Create: `backend/internal/storage/config.go`
- Test: `backend/internal/storage/config_test.go`

- [ ] **Step 1: Write the failing storage config tests**

Create `backend/internal/storage/config_test.go`:

```go
package storage

import "testing"

func TestLoadStorageConfigDefaultsToPostgresDriver(t *testing.T) {
	t.Setenv("FLOWSPACE_DATABASE_DRIVER", "")
	t.Setenv("FLOWSPACE_DATABASE_URL", "")
	t.Setenv("FLOWSPACE_SQLITE_PATH", "")

	cfg := LoadStorageConfig("test")

	if cfg.Driver != DriverPostgres {
		t.Fatalf("expected postgres driver, got %q", cfg.Driver)
	}
	if cfg.URL != "" {
		t.Fatalf("expected empty postgres URL, got %q", cfg.URL)
	}
}

func TestLoadStorageConfigReadsPostgresURL(t *testing.T) {
	t.Setenv("FLOWSPACE_DATABASE_DRIVER", "postgres")
	t.Setenv("FLOWSPACE_DATABASE_URL", "postgres://flowspace:flowspace@127.0.0.1:15432/flowspace_test?sslmode=disable")
	cfg := LoadStorageConfig("test")

	if cfg.Driver != DriverPostgres {
		t.Fatalf("expected postgres driver, got %q", cfg.Driver)
	}
	if cfg.URL != "postgres://flowspace:flowspace@127.0.0.1:15432/flowspace_test?sslmode=disable" {
		t.Fatalf("unexpected URL: %q", cfg.URL)
	}
	if cfg.Name != "flowspace_test" {
		t.Fatalf("expected database name flowspace_test, got %q", cfg.Name)
	}
}

func TestLoadStorageConfigReadsSQLitePath(t *testing.T) {
	t.Setenv("FLOWSPACE_DATABASE_DRIVER", "sqlite")
	t.Setenv("FLOWSPACE_SQLITE_PATH", "backend/flowspace.test.db")

	cfg := LoadStorageConfig("test")

	if cfg.Driver != DriverSQLite {
		t.Fatalf("expected sqlite driver, got %q", cfg.Driver)
	}
	if cfg.SQLitePath != "backend/flowspace.test.db" {
		t.Fatalf("unexpected sqlite path: %q", cfg.SQLitePath)
	}
}

func TestValidateStorageConfigRejectsUnknownDriver(t *testing.T) {
	cfg := Config{Env: "test", Driver: Driver("mysql")}

	if err := ValidateStorageConfig(cfg); err == nil {
		t.Fatal("expected unknown driver to fail")
	}
}

func TestValidateStorageConfigRejectsMissingProviderConfig(t *testing.T) {
	if err := ValidateStorageConfig(Config{Env: "test", Driver: DriverPostgres}); err == nil {
		t.Fatal("expected postgres without URL to fail")
	}
	if err := ValidateStorageConfig(Config{Env: "test", Driver: DriverSQLite}); err == nil {
		t.Fatal("expected sqlite without path to fail")
	}
}

func TestValidateStorageConfigRejectsTestEnvPointingAtProdDatabase(t *testing.T) {
	cfg := Config{Env: "test", Driver: DriverPostgres, URL: "postgres://flowspace:flowspace@127.0.0.1:15432/flowspace_prod?sslmode=disable"}

	if err := ValidateStorageConfig(cfg); err == nil {
		t.Fatal("expected test environment to reject flowspace_prod URL")
	}
}

func TestValidateStorageConfigRejectsProdEnvPointingAtTestDatabase(t *testing.T) {
	cfg := Config{Env: "prod", Driver: DriverPostgres, URL: "postgres://flowspace:flowspace@127.0.0.1:15432/flowspace_test?sslmode=disable"}

	if err := ValidateStorageConfig(cfg); err == nil {
		t.Fatal("expected prod environment to reject flowspace_test URL")
	}
}

func TestValidateStorageConfigRejectsSQLiteEnvMismatch(t *testing.T) {
	if err := ValidateStorageConfig(Config{Env: "test", Driver: DriverSQLite, SQLitePath: "backend/flowspace.db"}); err == nil {
		t.Fatal("expected test environment to reject prod sqlite file")
	}
	if err := ValidateStorageConfig(Config{Env: "prod", Driver: DriverSQLite, SQLitePath: "backend/flowspace.test.db"}); err == nil {
		t.Fatal("expected prod environment to reject test sqlite file")
	}
}
```

- [ ] **Step 2: Run config tests and verify red**

Run:

```powershell
cd backend
go test ./internal/storage -run "TestLoadStorageConfig|TestValidateStorageConfig" -count=1
```

Expected: fail with `undefined: LoadStorageConfig` or `undefined: ValidateStorageConfig`.

- [ ] **Step 3: Implement minimal storage config**

Create `backend/internal/storage/config.go`:

```go
package storage

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

type Driver string

const (
	DriverPostgres Driver = "postgres"
	DriverSQLite   Driver = "sqlite"
)

type Config struct {
	Env        string
	Driver     Driver
	URL        string
	SQLitePath string
	Name       string
}

func LoadStorageConfig(environment string) Config {
	driver := Driver(strings.ToLower(strings.TrimSpace(os.Getenv("FLOWSPACE_DATABASE_DRIVER"))))
	if driver == "" {
		driver = DriverPostgres
	}
	rawURL := strings.TrimSpace(os.Getenv("FLOWSPACE_DATABASE_URL"))
	sqlitePath := strings.TrimSpace(os.Getenv("FLOWSPACE_SQLITE_PATH"))
	return Config{
		Env:        environment,
		Driver:     driver,
		URL:        rawURL,
		SQLitePath: sqlitePath,
		Name:       databaseNameFromURL(rawURL),
	}
}

func databaseNameFromURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(parsed.Path, "/")
}

func ValidateStorageConfig(cfg Config) error {
	switch cfg.Driver {
	case DriverPostgres:
		if strings.TrimSpace(cfg.URL) == "" {
			return fmt.Errorf("FLOWSPACE_DATABASE_URL is required when FLOWSPACE_DATABASE_DRIVER=postgres")
		}
		parsed, err := url.Parse(cfg.URL)
		if err != nil {
			return fmt.Errorf("invalid FLOWSPACE_DATABASE_URL: %w", err)
		}
		dbName := strings.TrimPrefix(parsed.Path, "/")
		if cfg.Env == "test" && dbName == "flowspace_prod" {
			return fmt.Errorf("FLOWSPACE_ENV=test cannot use prod database %q", dbName)
		}
		if cfg.Env == "prod" && dbName == "flowspace_test" {
			return fmt.Errorf("FLOWSPACE_ENV=prod cannot use test database %q", dbName)
		}
	case DriverSQLite:
		if strings.TrimSpace(cfg.SQLitePath) == "" {
			return fmt.Errorf("FLOWSPACE_SQLITE_PATH is required when FLOWSPACE_DATABASE_DRIVER=sqlite")
		}
		normalized := strings.ReplaceAll(strings.ToLower(cfg.SQLitePath), "\\", "/")
		if cfg.Env == "test" && strings.HasSuffix(normalized, "flowspace.db") {
			return fmt.Errorf("FLOWSPACE_ENV=test cannot use prod sqlite path %q", cfg.SQLitePath)
		}
		if cfg.Env == "prod" && strings.HasSuffix(normalized, "flowspace.test.db") {
			return fmt.Errorf("FLOWSPACE_ENV=prod cannot use test sqlite path %q", cfg.SQLitePath)
		}
	default:
		return fmt.Errorf("unsupported FLOWSPACE_DATABASE_DRIVER %q", cfg.Driver)
	}
	return nil
}
```

- [ ] **Step 4: Run config tests and verify green**

Run:

```powershell
cd backend
go test ./internal/storage -run "TestLoadStorageConfig|TestValidateStorageConfig" -count=1
```

Expected: pass.

- [ ] **Step 5: Commit**

```powershell
git add backend/internal/storage/config.go backend/internal/storage/config_test.go
git commit -m "feat: add storage provider config"
```

---

### Task 1A: Add Storage Interfaces and Provider Registry

**Files:**
- Create: `backend/internal/storage/store.go`
- Create: `backend/internal/storage/registry.go`
- Test: `backend/internal/storage/registry_test.go`

- [ ] **Step 1: Write failing registry tests**

Create `backend/internal/storage/registry_test.go`:

```go
package storage

import (
	"context"
	"errors"
	"testing"
)

type fakeProvider struct {
	driver    Driver
	validate  func(Config) error
	open      func(context.Context, Config) (Store, error)
	migrated  bool
	validated bool
	opened    bool
}

func (p *fakeProvider) Driver() Driver { return p.driver }

func (p *fakeProvider) Validate(cfg Config) error {
	p.validated = true
	if p.validate != nil {
		return p.validate(cfg)
	}
	return nil
}

func (p *fakeProvider) Open(ctx context.Context, cfg Config) (Store, error) {
	p.opened = true
	if p.open != nil {
		return p.open(ctx, cfg)
	}
	return &fakeStore{}, nil
}

func (p *fakeProvider) Migrate(ctx context.Context, cfg Config) error {
	p.migrated = true
	return nil
}

type fakeStore struct{}

func (s *fakeStore) Close() error { return nil }
func (s *fakeStore) Health(context.Context) error { return nil }
func (s *fakeStore) Capabilities() Capabilities { return Capabilities{} }
func (s *fakeStore) Transact(context.Context, func(Store) error) error { return errors.New("not implemented in fake") }
func (s *fakeStore) Folders() FolderRepository { return nil }
func (s *fakeStore) Notes() NoteRepository { return nil }
func (s *fakeStore) Tasks() TaskRepository { return nil }
func (s *fakeStore) Events() EventRepository { return nil }
func (s *fakeStore) Inbox() InboxRepository { return nil }
func (s *fakeStore) Roadmaps() RoadmapRepository { return nil }
func (s *fakeStore) Sync() SyncRepository { return nil }
func (s *fakeStore) Search() SearchRepository { return nil }

func TestRegistryRejectsDuplicateProvider(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(&fakeProvider{driver: DriverPostgres}); err != nil {
		t.Fatalf("register first provider: %v", err)
	}
	if err := registry.Register(&fakeProvider{driver: DriverPostgres}); err == nil {
		t.Fatal("expected duplicate provider registration to fail")
	}
}

func TestRegistryRejectsUnknownDriver(t *testing.T) {
	registry := NewRegistry()
	if _, err := registry.Open(context.Background(), Config{Driver: DriverPostgres}); err == nil {
		t.Fatal("expected unknown provider to fail")
	}
}

func TestRegistryValidatesBeforeOpen(t *testing.T) {
	expectedErr := errors.New("invalid config")
	provider := &fakeProvider{
		driver: DriverPostgres,
		validate: func(Config) error { return expectedErr },
	}
	registry := NewRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatalf("register provider: %v", err)
	}

	if _, err := registry.Open(context.Background(), Config{Driver: DriverPostgres}); !errors.Is(err, expectedErr) {
		t.Fatalf("expected validation error, got %v", err)
	}
	if !provider.validated {
		t.Fatal("expected provider Validate to be called")
	}
	if provider.opened {
		t.Fatal("provider Open must not run after validation failure")
	}
}
```

- [ ] **Step 2: Run registry tests and verify red**

Run:

```powershell
cd backend
go test ./internal/storage -run "TestRegistry" -count=1
```

Expected: fail with missing `Provider`, `Store`, or `NewRegistry`.

- [ ] **Step 3: Add storage interfaces**

Create `backend/internal/storage/store.go`:

```go
package storage

import (
	"context"

	"github.com/hujinrun/flowspace/internal/model"
)

type Capabilities struct {
	FullTextSearch bool
	PrefixSearch   bool
	TrigramSearch  bool
	JSONObjects    bool
	ArrayColumns    bool
	TimeRanges      bool
	AdvisoryLocks   bool
}

type Provider interface {
	Driver() Driver
	Validate(Config) error
	Open(context.Context, Config) (Store, error)
	Migrate(context.Context, Config) error
}

type Store interface {
	Close() error
	Health(context.Context) error
	Capabilities() Capabilities
	Transact(context.Context, func(Store) error) error

	Folders() FolderRepository
	Notes() NoteRepository
	Tasks() TaskRepository
	Events() EventRepository
	Inbox() InboxRepository
	Roadmaps() RoadmapRepository
	Sync() SyncRepository
	Search() SearchRepository
}

type FolderRepository interface{}

type NoteFilter struct {
	FolderID string
	Query    string
	Page     int
	PageSize int
}

type NoteRepository interface {
	List(context.Context, NoteFilter) ([]model.Note, int, error)
	GetByID(context.Context, string) (*model.Note, error)
	Create(context.Context, *model.CreateNoteRequest) (*model.Note, error)
	CreateWithID(context.Context, *model.Note) error
	Update(context.Context, string, *model.UpdateNoteRequest) (*model.Note, error)
	Delete(context.Context, string) error
	ListAll(context.Context) ([]model.Note, error)
	Recent(context.Context, int) ([]model.Note, error)
}

type TaskRepository interface{}
type EventRepository interface{}
type InboxRepository interface{}
type RoadmapRepository interface{}
type SyncRepository interface{}
type SearchRepository interface{}
```

The first implementation may leave non-note domain interfaces empty, then fill them as each domain task migrates. Do not expose database driver types from these interfaces.

- [ ] **Step 4: Add provider registry**

Create `backend/internal/storage/registry.go`:

```go
package storage

import (
	"context"
	"fmt"
)

type Registry struct {
	providers map[Driver]Provider
}

func NewRegistry() *Registry {
	return &Registry{providers: map[Driver]Provider{}}
}

func (r *Registry) Register(provider Provider) error {
	if provider == nil {
		return fmt.Errorf("storage provider is nil")
	}
	driver := provider.Driver()
	if driver == "" {
		return fmt.Errorf("storage provider driver is empty")
	}
	if _, exists := r.providers[driver]; exists {
		return fmt.Errorf("storage provider %q already registered", driver)
	}
	r.providers[driver] = provider
	return nil
}

func (r *Registry) Open(ctx context.Context, cfg Config) (Store, error) {
	provider, ok := r.providers[cfg.Driver]
	if !ok {
		return nil, fmt.Errorf("storage provider %q is not registered", cfg.Driver)
	}
	if err := provider.Validate(cfg); err != nil {
		return nil, err
	}
	return provider.Open(ctx, cfg)
}
```

- [ ] **Step 5: Run registry tests and verify green**

Run:

```powershell
cd backend
go test ./internal/storage -run "TestRegistry" -count=1
```

Expected: pass.

- [ ] **Step 6: Commit**

```powershell
git add backend/internal/storage/store.go backend/internal/storage/registry.go backend/internal/storage/registry_test.go
git commit -m "feat: add storage provider registry"
```

---

### Task 1B: Add Repository Facade Store Wiring

**Files:**
- Create: `backend/internal/repository/facade.go`
- Test: `backend/internal/repository/facade_test.go`

- [ ] **Step 1: Write failing facade tests**

Create `backend/internal/repository/facade_test.go`:

```go
package repository

import (
	"testing"

	"github.com/hujinrun/flowspace/internal/storage"
)

func TestSetStoreAndActiveStore(t *testing.T) {
	store := &fakeRepositoryStore{}
	SetStore(store)
	t.Cleanup(func() { SetStore(nil) })

	if ActiveStore() != store {
		t.Fatal("expected active store to be returned")
	}
}

func TestActiveStorePanicsWhenMissing(t *testing.T) {
	SetStore(nil)
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic when active store is missing")
		}
	}()
	_ = ActiveStore()
}

type fakeRepositoryStore struct{ storage.Store }
```

- [ ] **Step 2: Run facade tests and verify red**

Run:

```powershell
cd backend
go test ./internal/repository -run "TestSetStore|TestActiveStore" -count=1
```

Expected: fail with `undefined: SetStore`.

- [ ] **Step 3: Implement facade store holder**

Create `backend/internal/repository/facade.go`:

```go
package repository

import (
	"sync"

	"github.com/hujinrun/flowspace/internal/storage"
)

var (
	activeStoreMu sync.RWMutex
	activeStore   storage.Store
)

func SetStore(store storage.Store) {
	activeStoreMu.Lock()
	defer activeStoreMu.Unlock()
	activeStore = store
}

func ActiveStore() storage.Store {
	activeStoreMu.RLock()
	defer activeStoreMu.RUnlock()
	if activeStore == nil {
		panic("repository active storage store is not initialized")
	}
	return activeStore
}
```

Existing package-level repository functions must be migrated domain by domain. Until a function is migrated, it may continue using legacy `DB`, but no new runtime function may use `DB` directly.

- [ ] **Step 4: Run facade tests and verify green**

Run:

```powershell
cd backend
go test ./internal/repository -run "TestSetStore|TestActiveStore" -count=1
```

Expected: pass.

- [ ] **Step 5: Commit**

```powershell
git add backend/internal/repository/facade.go backend/internal/repository/facade_test.go
git commit -m "feat: add repository storage facade"
```

---

### Task 1C: Add SQLite Compatibility Provider

**Files:**
- Create: `backend/internal/storage/sqlite/provider.go`
- Create: `backend/internal/storage/sqlite/legacy_migrations.go`
- Test: `backend/internal/storage/sqlite/provider_test.go`
- Modify: `backend/internal/repository/db.go`

- [ ] **Step 1: Write failing SQLite provider tests**

Create `backend/internal/storage/sqlite/provider_test.go`:

```go
package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/hujinrun/flowspace/internal/storage"
)

func TestProviderValidateRequiresSQLitePath(t *testing.T) {
	provider := Provider{}
	err := provider.Validate(storage.Config{Env: "test", Driver: storage.DriverSQLite})
	if err == nil {
		t.Fatal("expected missing sqlite path to fail")
	}
}

func TestProviderOpenUsesLegacySchemaAndReportsCapabilities(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "flowspace.test.db")
	provider := Provider{}

	store, err := provider.Open(context.Background(), storage.Config{
		Env:        "test",
		Driver:     storage.DriverSQLite,
		SQLitePath: dbPath,
	})
	if err != nil {
		t.Fatalf("open sqlite provider: %v", err)
	}
	defer store.Close()

	if err := store.Health(context.Background()); err != nil {
		t.Fatalf("health check: %v", err)
	}
	capabilities := store.Capabilities()
	if !capabilities.FullTextSearch || !capabilities.PrefixSearch {
		t.Fatalf("expected sqlite search capabilities, got %#v", capabilities)
	}
	if capabilities.TrigramSearch || capabilities.ArrayColumns || capabilities.TimeRanges || capabilities.AdvisoryLocks {
		t.Fatalf("unexpected postgres-only capabilities: %#v", capabilities)
	}
}
```

- [ ] **Step 2: Run SQLite provider tests and verify red**

Run:

```powershell
cd backend
go test ./internal/storage/sqlite -run "TestProvider" -count=1
```

Expected: fail with `undefined: Provider`.

- [ ] **Step 3: Extract legacy SQLite migration helper**

Modify `backend/internal/repository/db.go` so current `migrateDB()` delegates to an exported helper that accepts a DB:

```go
func migrateDB() error {
	return RunLegacySQLiteMigrations(DB)
}

func RunLegacySQLiteMigrations(db *sql.DB) error {
	statements := []string{
		// move the existing migrateDB statements here unchanged
	}
	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil && !isDuplicateColumnError(err) {
			return err
		}
	}
	if _, err := db.Exec(`
		UPDATE note_sync_state
		SET external_hash = content_hash
		WHERE status = 'synced'
			AND (external_hash IS NULL OR TRIM(external_hash) = '')
	`); err != nil {
		return err
	}
	return migrateTaskProjectsWithDB(db)
}
```

If `migrateTaskProjects` currently uses package-level `DB`, add `migrateTaskProjectsWithDB(db *sql.DB)` and let `migrateTaskProjects()` call it with package-level `DB`. This keeps legacy runtime working while allowing SQLite provider to run migrations on its own connection.

- [ ] **Step 4: Implement SQLite provider**

Create `backend/internal/storage/sqlite/provider.go`:

```go
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/hujinrun/flowspace/internal/repository"
	"github.com/hujinrun/flowspace/internal/storage"
	_ "modernc.org/sqlite"
)

type Provider struct{}

func (Provider) Driver() storage.Driver {
	return storage.DriverSQLite
}

func (Provider) Validate(cfg storage.Config) error {
	if strings.TrimSpace(cfg.SQLitePath) == "" {
		return errors.New("FLOWSPACE_SQLITE_PATH is required")
	}
	return storage.ValidateStorageConfig(cfg)
}

func (p Provider) Open(ctx context.Context, cfg storage.Config) (storage.Store, error) {
	if err := p.Validate(cfg); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", cfg.SQLitePath+"?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=ON")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := repository.RunLegacySQLiteMigrations(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return newStore(db), nil
}

func (p Provider) Migrate(ctx context.Context, cfg storage.Config) error {
	store, err := p.Open(ctx, cfg)
	if err != nil {
		return err
	}
	return store.Close()
}
```

Create the initial `store` implementation in the same package. Domain repository methods can initially panic with a clear message until each domain task migrates them; `Close`, `Health`, `Capabilities`, and `Transact` must work immediately.

- [ ] **Step 5: Run SQLite provider tests and verify green**

Run:

```powershell
cd backend
go test ./internal/storage/sqlite -run "TestProvider" -count=1
```

Expected: pass.

- [ ] **Step 6: Commit**

```powershell
git add backend/internal/storage/sqlite/provider.go backend/internal/storage/sqlite/provider_test.go backend/internal/repository/db.go
git commit -m "feat: add sqlite storage provider"
```

---

### Task 2: Add PostgreSQL Local Service

**Files:**
- Create: `docker-compose.postgres.yml`
- Create: `docker/postgres/init-flowspace.sql`
- Modify: `.gitignore` if local PostgreSQL data paths are introduced

- [ ] **Step 1: Add Docker Compose file**

Create `docker-compose.postgres.yml`:

```yaml
services:
  postgres:
    image: postgres:16-alpine
    container_name: flowspace-postgres
    environment:
      POSTGRES_USER: flowspace
      POSTGRES_PASSWORD: flowspace
      POSTGRES_DB: postgres
    ports:
      - "15432:5432"
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U flowspace -d postgres"]
      interval: 5s
      timeout: 3s
      retries: 10
    volumes:
      - ./docker/postgres/init-flowspace.sql:/docker-entrypoint-initdb.d/010-flowspace.sql:ro
      - flowspace_pg_data:/var/lib/postgresql/data

volumes:
  flowspace_pg_data:
```

- [ ] **Step 2: Add database initialization script**

Create `docker/postgres/init-flowspace.sql`:

```sql
SELECT 'CREATE DATABASE flowspace_test OWNER flowspace'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'flowspace_test')\gexec

SELECT 'CREATE DATABASE flowspace_prod OWNER flowspace'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'flowspace_prod')\gexec
```

- [ ] **Step 3: Start PostgreSQL**

Run:

```powershell
docker compose -f docker-compose.postgres.yml up -d
```

Expected: `flowspace-postgres` starts and becomes healthy.

- [ ] **Step 4: Verify both databases**

Run:

```powershell
docker exec flowspace-postgres pg_isready -U flowspace -d flowspace_test
docker exec flowspace-postgres pg_isready -U flowspace -d flowspace_prod
```

Expected: both commands output `accepting connections`.

- [ ] **Step 5: Document existing-volume behavior**

Add this note to `README.md` in Task 12 and keep it in mind during local setup:

```text
PostgreSQL init scripts only run when the Docker volume is empty. If `flowspace_pg_data`
already exists and `flowspace_prod` is missing, either recreate the volume:

docker compose -f docker-compose.postgres.yml down -v
docker compose -f docker-compose.postgres.yml up -d

or run the init SQL manually:

docker exec -i flowspace-postgres psql -U flowspace -d postgres -f /docker-entrypoint-initdb.d/010-flowspace.sql
```

- [ ] **Step 6: Commit**

```powershell
git add docker-compose.postgres.yml docker/postgres/init-flowspace.sql
git commit -m "chore: add postgres local service"
```

---

### Task 3: Add PostgreSQL Schema Migration

**Files:**
- Create: `backend/db/migrations/postgres/0001_init_postgres.sql`
- Create: `backend/internal/storage/postgres/migrations.go`
- Test: `backend/internal/storage/postgres/migrations_test.go`

- [ ] **Step 1: Write migration tests**

Create `backend/internal/storage/postgres/migrations_test.go`:

```go
package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"sync"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestRunPostgresMigrationsCreatesCoreTablesSeedDataAndIsIdempotent(t *testing.T) {
	schema := fmt.Sprintf("fs_test_migrations_%d", time.Now().UnixNano())
	db := openPostgresTestDB(t, schema)
	defer db.Close()

	if err := runPostgresMigrations(db); err != nil {
		t.Fatalf("first run migrations: %v", err)
	}
	if err := runPostgresMigrations(db); err != nil {
		t.Fatalf("second run migrations must be idempotent: %v", err)
	}

	for _, table := range []string{
		"schema_migrations",
		"folders",
		"notes",
		"task_projects",
		"tasks",
		"learning_roadmaps",
		"roadmap_nodes",
		"roadmap_edges",
		"roadmap_resources",
		"events",
		"inbox",
		"sync_targets",
		"note_sync_state",
		"search_index",
	} {
		var exists bool
		if err := db.QueryRow(`SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = $1 AND table_name = $2
		)`, schema, table).Scan(&exists); err != nil {
			t.Fatalf("check table %s: %v", table, err)
		}
		if !exists {
			t.Fatalf("expected table %s to exist", table)
		}
	}

	assertRowCount(t, db, `SELECT COUNT(*) FROM folders WHERE id IN ('__uncategorized', '__work', '__personal')`, 3)
	assertRowCount(t, db, `SELECT COUNT(*) FROM task_projects WHERE id = 'personal'`, 1)
	assertRowCount(t, db, `SELECT COUNT(*) FROM schema_migrations WHERE version = '0001_init_postgres.sql'`, 1)
	assertRowCount(t, db, `
		SELECT COUNT(*)
		FROM pg_extension e
		JOIN pg_namespace n ON n.oid = e.extnamespace
		WHERE e.extname = 'pg_trgm' AND n.nspname = 'public'
	`, 1)
}

func TestApplyPostgresMigrationSerializesConcurrentStartup(t *testing.T) {
	schema := fmt.Sprintf("fs_test_migration_lock_%d", time.Now().UnixNano())
	db := openPostgresTestDB(t, schema)
	defer db.Close()

	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY,
			checksum TEXT NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}

	sqlBytes := []byte(`
		SELECT pg_sleep(0.2);
		CREATE TABLE concurrent_migration_guard (id INTEGER PRIMARY KEY);
	`)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- applyPostgresMigration(db, "9999_concurrent_guard.sql", sqlBytes)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent migration should serialize and succeed: %v", err)
		}
	}

	assertRowCount(t, db, `SELECT COUNT(*) FROM schema_migrations WHERE version = '9999_concurrent_guard.sql'`, 1)
	assertRowCount(t, db, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = 'concurrent_migration_guard'`, 1)
}

func openPostgresTestDB(t *testing.T, schema string) *sql.DB {
	t.Helper()

	schemaURL := createPostgresTestSchema(t, schema)
	db, err := sql.Open("pgx", schemaURL)
	if err != nil {
		t.Fatalf("open postgres schema connection: %v", err)
	}
	return db
}

func createPostgresTestSchema(t *testing.T, schema string) string {
	t.Helper()

	baseURL := os.Getenv("FLOWSPACE_TEST_DATABASE_URL")
	if baseURL == "" {
		t.Skip("FLOWSPACE_TEST_DATABASE_URL is required for postgres integration tests")
	}

	adminDB, err := sql.Open("pgx", baseURL)
	if err != nil {
		t.Fatalf("open postgres admin connection: %v", err)
	}

	ctx := context.Background()
	quotedSchema := quotePostgresIdentifier(schema)
	if _, err := adminDB.ExecContext(ctx, `DROP SCHEMA IF EXISTS `+quotedSchema+` CASCADE`); err != nil {
		t.Fatalf("drop test schema: %v", err)
	}
	if _, err := adminDB.ExecContext(ctx, `CREATE SCHEMA `+quotedSchema); err != nil {
		t.Fatalf("create test schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = adminDB.ExecContext(context.Background(), `DROP SCHEMA IF EXISTS `+quotedSchema+` CASCADE`)
		_ = adminDB.Close()
	})

	return postgresTestURLForSchema(t, baseURL, schema)
}

func postgresTestURLForSchema(t *testing.T, baseURL, schema string) string {
	t.Helper()

	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("parse postgres URL: %v", err)
	}
	query := parsed.Query()
	query.Set("options", "-c search_path="+schema+",public")
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func quotePostgresIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func assertRowCount(t *testing.T, db *sql.DB, query string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(query).Scan(&got); err != nil {
		t.Fatalf("query row count: %v", err)
	}
	if got != want {
		t.Fatalf("expected row count %d, got %d for %s", want, got, query)
	}
}
```

- [ ] **Step 2: Run migration test and verify red**

Run:

```powershell
cd backend
$env:FLOWSPACE_TEST_DATABASE_URL = "postgres://flowspace:flowspace@127.0.0.1:15432/flowspace_test?sslmode=disable"
go test ./internal/storage/postgres -run "TestRunPostgresMigrationsCreatesCoreTablesSeedDataAndIsIdempotent|TestApplyPostgresMigrationSerializesConcurrentStartup" -count=1
```

Expected: fail with `undefined: runPostgresMigrations` or missing pgx module.

- [ ] **Step 3: Add PostgreSQL dependencies**

Run:

```powershell
cd backend
go get github.com/jackc/pgx/v5@latest
go get github.com/lib/pq@latest
```

Expected: `go.mod` contains `github.com/jackc/pgx/v5` and `github.com/lib/pq`.

- [ ] **Step 4: Add migration SQL**

Create `backend/db/migrations/postgres/0001_init_postgres.sql` using the PostgreSQL provider schema in `docs/postgres-storage-design.md`. The file must include:

- Extension: `CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public`.
- Core tables: `folders`, `notes`.
- Task tables: `task_projects`, `tasks`.
- `tasks.status` check must allow current repository values: `open`, `active`, `blocked`, `done`, `archived`, `migrated`, `cancelled`.
- Calendar and inbox tables: `events`, `inbox`.
- Roadmap tables: `learning_roadmaps`, `roadmap_nodes`, `roadmap_edges`, `roadmap_resources`.
- `learning_roadmaps.status` check must allow current repository values: `draft`, `ready`, `active`, `done`, `archived`, `failed`.
- `roadmap_nodes.type` check must allow current normalized values: `phase`, `module`, `choice`, `task`.
- `roadmap_nodes.path_type` check must allow current normalized values: `required`, `recommended`, `optional`, `alternative`.
- `roadmap_nodes.status` check must allow current normalized values: `todo`, `active`, `done`, `skipped`.
- `roadmap_nodes` must define `UNIQUE (roadmap_id, id)` before `roadmap_edges` is created.
- `roadmap_nodes.parent_id` must enforce same-roadmap ownership with `FOREIGN KEY (roadmap_id, parent_id) REFERENCES roadmap_nodes(roadmap_id, id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED`; nullable `parent_id` remains valid for root nodes, and deferred checking avoids parent/child insert-order failures during migration.
- `roadmap_edges` must enforce same-roadmap node ownership with composite foreign keys through `roadmap_nodes(roadmap_id, id)`.
- Post-create FK constraint: `tasks.roadmap_node_id -> roadmap_nodes.id ON DELETE SET NULL`.
- Sync tables: `sync_targets`, `note_sync_state`.
- Search table: `search_index`.
- PostgreSQL 10.6 compatibility: do not use `GENERATED ALWAYS AS ... STORED`; define `events.time_range` and `search_index.search_vector` as normal `NOT NULL` columns.
- Indexes listed in the PostgreSQL schema section of `docs/postgres-storage-design.md`, including hot-path partial indexes `tasks_due_open_idx`, `tasks_planned_open_idx`, `tasks_long_active_idx`, and `inbox_open_created_idx`.
- Default seed rows for `folders.__uncategorized`, `folders.__work`, `folders.__personal`, and `task_projects.personal` using `INSERT ... ON CONFLICT DO NOTHING`.
- No search refresh triggers in phase 1; repositories update `search_index` explicitly.

The actual SQL must be complete and executable by `psql` without manual edits.

- [ ] **Step 5: Implement migration runner**

Create `backend/internal/storage/postgres/migrations.go`:

```go
package postgres

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func RunPostgresMigrations(db *sql.DB) error {
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY,
			checksum TEXT NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`); err != nil {
		return fmt.Errorf("ensure schema_migrations: %w", err)
	}

	dir, err := findPostgresMigrationsDir()
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)

	for _, name := range names {
		path := filepath.Join(dir, name)
		sqlBytes, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := applyPostgresMigration(db, name, sqlBytes); err != nil {
			return err
		}
	}
	return nil
}

func runPostgresMigrations(db *sql.DB) error {
	return RunPostgresMigrations(db)
}

func findPostgresMigrationsDir() (string, error) {
	candidates := []string{
		filepath.Join("db", "migrations", "postgres"),
		filepath.Join("backend", "db", "migrations", "postgres"),
		filepath.Join("..", "..", "db", "migrations", "postgres"),
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("postgres migrations directory not found")
}

func applyPostgresMigration(db *sql.DB, version string, sqlBytes []byte) error {
	sum := sha256.Sum256(sqlBytes)
	checksum := hex.EncodeToString(sum[:])

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", version, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtext('flowspace_schema_migrations'))`); err != nil {
		return fmt.Errorf("lock migration runner %s: %w", version, err)
	}

	var existingChecksum string
	err = tx.QueryRow(`SELECT checksum FROM schema_migrations WHERE version = $1`, version).Scan(&existingChecksum)
	if err == nil {
		if existingChecksum != checksum {
			return fmt.Errorf("migration %s checksum mismatch: database=%s file=%s", version, existingChecksum, checksum)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit skipped migration %s: %w", version, err)
		}
		committed = true
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("check migration %s: %w", version, err)
	}

	if _, err := tx.Exec(string(sqlBytes)); err != nil {
		return fmt.Errorf("apply migration %s: %w", version, err)
	}
	if _, err := tx.Exec(`
		INSERT INTO schema_migrations (version, checksum)
		VALUES ($1, $2)
	`, version, checksum); err != nil {
		return fmt.Errorf("record migration %s: %w", version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %s: %w", version, err)
	}
	committed = true
	return nil
}
```

- [ ] **Step 6: Run migration test and verify green**

Run:

```powershell
cd backend
$env:FLOWSPACE_TEST_DATABASE_URL = "postgres://flowspace:flowspace@127.0.0.1:15432/flowspace_test?sslmode=disable"
go test ./internal/storage/postgres -run "TestRunPostgresMigrationsCreatesCoreTablesSeedDataAndIsIdempotent|TestApplyPostgresMigrationSerializesConcurrentStartup" -count=1
```

Expected: pass.

- [ ] **Step 7: Commit**

```powershell
git add backend/go.mod backend/go.sum backend/db/migrations/postgres/0001_init_postgres.sql backend/internal/storage/postgres/migrations.go backend/internal/storage/postgres/migrations_test.go
git commit -m "feat: add postgres schema migrations"
```

---

### Task 4: Add PostgreSQL Provider and Runtime Store Initialization

**Files:**
- Create: `backend/internal/storage/postgres/provider.go`
- Test: `backend/internal/storage/postgres/provider_test.go`
- Modify: `backend/cmd/server/main.go`
- Modify: `backend/cmd/seed/main.go`
- Test: `backend/cmd/server/main_test.go` if command-level startup tests already exist

- [ ] **Step 1: Write PostgreSQL provider tests**

Create `backend/internal/storage/postgres/provider_test.go`:

```go
package postgres

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/storage"
)

func TestProviderValidateRequiresDatabaseURL(t *testing.T) {
	provider := Provider{}
	err := provider.Validate(storage.Config{Env: "test", Driver: storage.DriverPostgres})
	if err == nil {
		t.Fatal("expected error when database URL is empty")
	}
}

func TestProviderOpenConnectsMigratesAndReportsCapabilities(t *testing.T) {
	schema := fmt.Sprintf("fs_test_provider_%d", time.Now().UnixNano())
	databaseURL := createPostgresTestSchema(t, schema)
	provider := Provider{}

	store, err := provider.Open(context.Background(), storage.Config{
		Env:    "test",
		Driver: storage.DriverPostgres,
		URL:    databaseURL,
	})
	if err != nil {
		t.Fatalf("open postgres provider: %v", err)
	}
	defer store.Close()

	if err := store.Health(context.Background()); err != nil {
		t.Fatalf("health check: %v", err)
	}

	capabilities := store.Capabilities()
	if !capabilities.FullTextSearch || !capabilities.JSONObjects || !capabilities.ArrayColumns || !capabilities.TimeRanges {
		t.Fatalf("unexpected capabilities: %#v", capabilities)
	}
}
```

- [ ] **Step 2: Run test and verify red**

Run:

```powershell
cd backend
$env:FLOWSPACE_TEST_DATABASE_URL = "postgres://flowspace:flowspace@127.0.0.1:15432/flowspace_test?sslmode=disable"
go test ./internal/storage/postgres -run "TestProvider" -count=1
```

Expected: fail with `undefined: Provider`.

- [ ] **Step 3: Implement PostgreSQL provider**

Create `backend/internal/storage/postgres/provider.go`:

```go
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/storage"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type Provider struct{}

func (Provider) Driver() storage.Driver {
	return storage.DriverPostgres
}

func (Provider) Validate(cfg storage.Config) error {
	if strings.TrimSpace(cfg.URL) == "" {
		return errors.New("FLOWSPACE_DATABASE_URL is required")
	}
	return storage.ValidateStorageConfig(cfg)
}

func (p Provider) Open(ctx context.Context, cfg storage.Config) (storage.Store, error) {
	if err := p.Validate(cfg); err != nil {
		return nil, err
	}
	db, err := sql.Open("pgx", cfg.URL)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)
	db.SetConnMaxIdleTime(5 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := RunPostgresMigrations(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return newStore(db), nil
}

func (p Provider) Migrate(ctx context.Context, cfg storage.Config) error {
	store, err := p.Open(ctx, cfg)
	if err != nil {
		return err
	}
	return store.Close()
}

type store struct {
	db *sql.DB
}

func newStore(db *sql.DB) storage.Store {
	return &store{db: db}
}

func (s *store) Close() error { return s.db.Close() }

func (s *store) Health(ctx context.Context) error { return s.db.PingContext(ctx) }

func (s *store) Capabilities() storage.Capabilities {
	return storage.Capabilities{
		FullTextSearch: true,
		PrefixSearch:   true,
		TrigramSearch:  true,
		JSONObjects:    true,
		ArrayColumns:    true,
		TimeRanges:      true,
		AdvisoryLocks:   true,
	}
}

func (s *store) Transact(ctx context.Context, fn func(storage.Store) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	txStore := &storeTx{store: s, tx: tx}
	if err := fn(txStore); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

type storeTx struct {
	*store
	tx *sql.Tx
}
```

Domain methods such as `Notes()` and `Tasks()` can initially return repositories backed by `*sql.DB` or `*sql.Tx` as each domain task is implemented. Do not introduce `repository.InitPostgres`.

- [ ] **Step 4: Update server and seed commands**

In `backend/cmd/server/main.go`, replace runtime init with provider selection:

```go
runtimeConfig := config.LoadStorageConfig()
cfg := storage.LoadStorageConfig(runtimeConfig.Environment)
registry := storage.NewRegistry()
if err := registry.Register(postgres.Provider{}); err != nil {
	log.Fatalf("register postgres provider: %v", err)
}
if err := registry.Register(sqlite.Provider{}); err != nil {
	log.Fatalf("register sqlite provider: %v", err)
}
store, err := registry.Open(context.Background(), cfg)
if err != nil {
	log.Fatalf("open storage provider: %v", err)
}
repository.SetStore(store)
log.Printf("storage initialized env=%s driver=%s database=%s sqlite_path=%s capabilities=%+v", cfg.Env, cfg.Driver, cfg.Name, cfg.SQLitePath, store.Capabilities())
```

In `backend/cmd/seed/main.go`, use the same provider registry and `repository.SetStore(store)` flow.

- [ ] **Step 5: Run tests and verify green**

Run:

```powershell
cd backend
$env:FLOWSPACE_TEST_DATABASE_URL = "postgres://flowspace:flowspace@127.0.0.1:15432/flowspace_test?sslmode=disable"
go test ./internal/storage/postgres -run "TestProvider" -count=1
```

Expected: pass.

- [ ] **Step 6: Commit**

```powershell
git add backend/internal/storage/postgres/provider.go backend/internal/storage/postgres/provider_test.go backend/cmd/server/main.go backend/cmd/seed/main.go
git commit -m "feat: initialize pluggable storage providers"
```

---

### Task 5: Add PostgreSQL Type and SQL Builder Helpers

**Files:**
- Create: `backend/internal/storage/postgres/types.go`
- Create: `backend/internal/storage/postgres/builder.go`
- Test: `backend/internal/storage/postgres/types_test.go`
- Test: `backend/internal/storage/postgres/builder_test.go`

- [ ] **Step 1: Write helper tests**

Create `backend/internal/storage/postgres/types_test.go`:

```go
package postgres

import (
	"testing"
	"time"
)

func TestTagsJSONStringRoundTrip(t *testing.T) {
	tags, err := tagsJSONStringToArray(`["sync","publish","#work","sync"]`)
	if err != nil {
		t.Fatalf("parse tags: %v", err)
	}
	if len(tags) != 3 || tags[0] != "sync" || tags[1] != "publish" || tags[2] != "work" {
		t.Fatalf("unexpected tags: %#v", tags)
	}

	jsonText := tagsArrayToJSONString(tags)
	if jsonText != `["sync","publish","work"]` {
		t.Fatalf("unexpected json: %s", jsonText)
	}
}

func TestUnixTimeRoundTrip(t *testing.T) {
	value := int64(1800000000)
	asTime := unixToTime(value)
	if asTime.Location() != time.UTC {
		t.Fatalf("expected UTC time")
	}
	if got := timeToUnix(asTime); got != value {
		t.Fatalf("expected %d, got %d", value, got)
	}
}
```

- [ ] **Step 2: Run helper tests and verify red**

Run:

```powershell
cd backend
go test ./internal/storage/postgres -run "TestTagsJSONStringRoundTrip|TestUnixTimeRoundTrip" -count=1
```

Expected: fail with undefined helper functions.

- [ ] **Step 3: Implement helpers**

Create `backend/internal/storage/postgres/types.go`:

```go
package postgres

import (
	"encoding/json"
	"strings"
	"time"
)

func unixToTime(value int64) time.Time {
	return time.Unix(value, 0).UTC()
}

func timeToUnix(value time.Time) int64 {
	return value.UTC().Unix()
}

func tagsJSONStringToArray(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return []string{}, nil
	}
	var input []string
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	tags := make([]string, 0, len(input))
	for _, item := range input {
		tag := strings.TrimSpace(strings.TrimLeft(item, "#"))
		if tag == "" {
			continue
		}
		key := strings.ToLower(tag)
		if seen[key] {
			continue
		}
		seen[key] = true
		tags = append(tags, tag)
	}
	return tags, nil
}

func tagsArrayToJSONString(tags []string) string {
	if tags == nil {
		tags = []string{}
	}
	data, err := json.Marshal(tags)
	if err != nil {
		return "[]"
	}
	return string(data)
}
```

- [ ] **Step 4: Write SQL builder tests**

Create `backend/internal/storage/postgres/builder_test.go`:

```go
package postgres

import "testing"

func TestPgPlaceholders(t *testing.T) {
	got := pgPlaceholders(2, 3)
	if got != "$2,$3,$4" {
		t.Fatalf("expected placeholders $2,$3,$4, got %q", got)
	}
}

func TestPgInClauseRejectsEmptyValues(t *testing.T) {
	if _, err := pgInClause("id", 1, 0); err == nil {
		t.Fatal("expected empty IN clause to fail")
	}
}

func TestPgInClause(t *testing.T) {
	got, err := pgInClause("id", 3, 2)
	if err != nil {
		t.Fatalf("in clause: %v", err)
	}
	if got != "id IN ($3,$4)" {
		t.Fatalf("unexpected IN clause: %q", got)
	}
}

func TestPgSetBuilder(t *testing.T) {
	b := newPgSetBuilder(2)
	b.Add("title", "新标题")
	b.Add("updated_at", int64(1800000000))

	clause, args := b.ClauseAndArgs()
	if clause != "title = $2, updated_at = $3" {
		t.Fatalf("unexpected set clause: %q", clause)
	}
	if len(args) != 2 || args[0] != "新标题" || args[1] != int64(1800000000) {
		t.Fatalf("unexpected args: %#v", args)
	}
}
```

- [ ] **Step 5: Run SQL builder tests and verify red**

Run:

```powershell
cd backend
go test ./internal/storage/postgres -run "TestPgPlaceholders|TestPgInClause|TestPgSetBuilder" -count=1
```

Expected: fail with undefined helper functions.

- [ ] **Step 6: Implement SQL builder helpers**

Create `backend/internal/storage/postgres/builder.go`:

```go
package postgres

import (
	"fmt"
	"strings"
)

func pgPlaceholder(index int) string {
	return fmt.Sprintf("$%d", index)
}

func pgPlaceholders(start, count int) string {
	parts := make([]string, count)
	for i := 0; i < count; i++ {
		parts[i] = pgPlaceholder(start + i)
	}
	return strings.Join(parts, ",")
}

func pgInClause(column string, start, count int) (string, error) {
	if count <= 0 {
		return "", fmt.Errorf("%s IN clause requires at least one value", column)
	}
	return fmt.Sprintf("%s IN (%s)", column, pgPlaceholders(start, count)), nil
}

type pgSetBuilder struct {
	next int
	sets []string
	args []interface{}
}

func newPgSetBuilder(start int) *pgSetBuilder {
	return &pgSetBuilder{next: start}
}

func (b *pgSetBuilder) Add(column string, value interface{}) {
	b.sets = append(b.sets, fmt.Sprintf("%s = %s", column, pgPlaceholder(b.next)))
	b.args = append(b.args, value)
	b.next++
}

func (b *pgSetBuilder) ClauseAndArgs() (string, []interface{}) {
	return strings.Join(b.sets, ", "), b.args
}
```

- [ ] **Step 7: Run helper tests and verify green**

Run:

```powershell
cd backend
go test ./internal/storage/postgres -run "TestTagsJSONStringRoundTrip|TestUnixTimeRoundTrip|TestPg" -count=1
```

Expected: pass.

- [ ] **Step 8: Commit**

```powershell
git add backend/internal/storage/postgres/types.go backend/internal/storage/postgres/types_test.go backend/internal/storage/postgres/builder.go backend/internal/storage/postgres/builder_test.go
git commit -m "feat: add postgres repository helpers"
```

---

### Task 5A: Add Provider Contract Test Suite

**Files:**
- Create: `backend/internal/storage/contract_tests.go`
- Test: `backend/internal/storage/sqlite/contract_test.go`
- Test: `backend/internal/storage/postgres/contract_test.go`

- [ ] **Step 1: Create shared contract test harness**

Create `backend/internal/storage/contract_tests.go`:

```go
package storage

import (
	"context"
	"errors"
	"testing"
)

type ContractStoreFactory func(t *testing.T) Store

func RunStoreContractSuite(t *testing.T, factory ContractStoreFactory) {
	t.Helper()

	t.Run("Health", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		if err := store.Health(context.Background()); err != nil {
			t.Fatalf("health: %v", err)
		}
	})

	t.Run("TransactRollback", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		errSentinel := errors.New("contract rollback")
		err := store.Transact(context.Background(), func(txStore Store) error {
			return errSentinel
		})
		if !errors.Is(err, errSentinel) {
			t.Fatalf("expected rollback sentinel, got %v", err)
		}
	})
}
```

This first harness only verifies universal provider behavior. Each domain migration task must extend this suite with domain-specific assertions before implementing that domain for PostgreSQL.

- [ ] **Step 2: Add SQLite contract test entry**

Create `backend/internal/storage/sqlite/contract_test.go`:

```go
package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/hujinrun/flowspace/internal/storage"
)

func TestSQLiteStoreContract(t *testing.T) {
	storage.RunStoreContractSuite(t, func(t *testing.T) storage.Store {
		t.Helper()
		store, err := (Provider{}).Open(context.Background(), storage.Config{
			Env:        "test",
			Driver:     storage.DriverSQLite,
			SQLitePath: filepath.Join(t.TempDir(), "flowspace.test.db"),
		})
		if err != nil {
			t.Fatalf("open sqlite store: %v", err)
		}
		return store
	})
}
```

- [ ] **Step 3: Add PostgreSQL contract test entry**

Create `backend/internal/storage/postgres/contract_test.go`:

```go
package postgres

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/storage"
)

func TestPostgresStoreContract(t *testing.T) {
	storage.RunStoreContractSuite(t, func(t *testing.T) storage.Store {
		t.Helper()
		schema := fmt.Sprintf("fs_test_contract_%d", time.Now().UnixNano())
		store, err := (Provider{}).Open(context.Background(), storage.Config{
			Env:    "test",
			Driver: storage.DriverPostgres,
			URL:    createPostgresTestSchema(t, schema),
		})
		if err != nil {
			t.Fatalf("open postgres store: %v", err)
		}
		return store
	})
}
```

- [ ] **Step 4: Run contract tests**

Run:

```powershell
cd backend
$env:FLOWSPACE_TEST_DATABASE_URL = "postgres://flowspace:flowspace@127.0.0.1:15432/flowspace_test?sslmode=disable"
go test ./internal/storage/... -run "TestSQLiteStoreContract|TestPostgresStoreContract" -count=1
```

Expected: pass after SQLite and PostgreSQL providers implement `Health` and `Transact`.

- [ ] **Step 5: Commit**

```powershell
git add backend/internal/storage/contract_tests.go backend/internal/storage/sqlite/contract_test.go backend/internal/storage/postgres/contract_test.go
git commit -m "test: add storage provider contract suite"
```

---

## PostgreSQL Provider SQL Migration Checklist

Apply this checklist in every PostgreSQL provider domain task before marking the task complete. The generic repository facade must not contain SQL.

- Replace positional placeholders with `$n`; use `pgPlaceholder`, `pgPlaceholders`, `pgInClause`, and `pgSetBuilder` for dynamic SQL.
- Replace dynamic `IN (?, ?, ?)` with `pgInClause("id", startIndex, len(ids))`.
- Replace dynamic `UPDATE ... SET` assembly in `UpdateNote`, `UpdateTask`, `UpdateTaskProject`, `UpdateEvent`, and `UpdateRoadmapNode` with `pgSetBuilder`.
- Replace `COUNT(n.rowid)` with `COUNT(n.id)`.
- Remove all joins to FTS virtual tables and `rowid`; search must go through `search_index.entity_id`.
- Replace `COLLATE NOCASE` with `LOWER(name) ASC` unless a PostgreSQL collation is explicitly introduced and tested.
- Replace integer booleans in SQL predicates with PostgreSQL booleans, for example `done = true`, `archived = false`, and `enabled = true`.
- Replace SQLite date helpers such as `date(value, 'unixepoch', 'localtime')` with repository-side UTC conversion or PostgreSQL `to_timestamp($n)::date`.
- Replace `LIKE` where case-insensitive behavior is required with `ILIKE` or `LOWER(column) LIKE LOWER($n)`.
- Replace FTS5 `MATCH` and `snippet()` with prefix `to_tsquery`, trigram fallback, and `ts_headline`.

Domain task setup rule:

- If an older test snippet in this plan shows `InitPostgres(databaseURL)`, replace it during implementation with `store := openPostgresStoreForTest(t, databaseURL)`, then call provider repositories through `store.Notes()`, `store.Tasks()`, etc.
- If the test is verifying the legacy repository facade, call `repository.SetStore(store)` before invoking package-level repository functions.
- Do not create new `InitPostgres` or `LoadDatabaseConfig` APIs.
- Every domain task must add or extend provider contract tests for the same behavior, then add PostgreSQL-specific tests only for PostgreSQL-only structures or query plans.

Shared helper for repository facade tests that still live in `backend/internal/repository`:

```go
func openPostgresStoreForTest(t *testing.T, databaseURL string) storage.Store {
	t.Helper()
	store, err := (postgres.Provider{}).Open(context.Background(), storage.Config{
		Env:    "test",
		Driver: storage.DriverPostgres,
		URL:    databaseURL,
	})
	if err != nil {
		t.Fatalf("open postgres store: %v", err)
	}
	return store
}
```

---

### Task 6: Migrate Notes, Folders, and Search

**Files:**
- Modify: `backend/internal/repository/folders.go`
- Modify: `backend/internal/repository/notes.go`
- Modify: `backend/internal/repository/search.go`
- Test: existing and new repository tests for notes/search

- [ ] **Step 1: Write PostgreSQL notes repository test**

Create `backend/internal/repository/notes_postgres_test.go`:

```go
package repository

import (
	"fmt"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/model"
)

func TestNotesPostgresStoresTagsAsArrayAndReturnsJSONString(t *testing.T) {
	schema := fmt.Sprintf("fs_test_notes_%d", time.Now().UnixNano())
	databaseURL := createPostgresTestSchema(t, schema)

	store := openPostgresStoreForTest(t, databaseURL)
	defer store.Close()
	SetStore(store)
	t.Cleanup(func() { SetStore(nil) })
	truncatePostgresTables(t)

	note := &model.Note{
		Title:    "PostgreSQL 标签笔记",
		Body:     "body",
		FolderID: "__uncategorized",
		Tags:     `["sync","publish"]`,
	}
	if err := CreateNote(note); err != nil {
		t.Fatalf("create note: %v", err)
	}

	got, err := GetNoteByID(note.ID)
	if err != nil {
		t.Fatalf("get note: %v", err)
	}
	if got.Tags != `["sync","publish"]` {
		t.Fatalf("expected JSON string tags, got %s", got.Tags)
	}
}

func TestUpdateNotePostgresUpdatesDynamicFields(t *testing.T) {
	schema := fmt.Sprintf("fs_test_update_note_%d", time.Now().UnixNano())
	databaseURL := createPostgresTestSchema(t, schema)

	store := openPostgresStoreForTest(t, databaseURL)
	defer store.Close()
	SetStore(store)
	t.Cleanup(func() { SetStore(nil) })
	truncatePostgresTables(t)

	note := &model.Note{
		Title:    "旧标题",
		Body:     "旧正文",
		FolderID: "__uncategorized",
		Tags:     `["old"]`,
	}
	if err := CreateNote(note); err != nil {
		t.Fatalf("create note: %v", err)
	}

	title := "新标题"
	tags := `["sync","publish"]`
	updated, err := UpdateNote(note.ID, &model.UpdateNoteRequest{Title: &title, Tags: &tags})
	if err != nil {
		t.Fatalf("update note: %v", err)
	}
	if updated.Title != "新标题" || updated.Body != "旧正文" || updated.Tags != `["sync","publish"]` {
		t.Fatalf("unexpected updated note: %+v", updated)
	}
}

func TestCreateNoteWithIDPostgresUpdatesSearchIndex(t *testing.T) {
	schema := fmt.Sprintf("fs_test_imported_note_%d", time.Now().UnixNano())
	databaseURL := createPostgresTestSchema(t, schema)

	store := openPostgresStoreForTest(t, databaseURL)
	defer store.Close()
	SetStore(store)
	t.Cleanup(func() { SetStore(nil) })
	truncatePostgresTables(t)

	created, err := CreateNoteWithID(&model.CreateNoteWithIDRequest{
		ID:       "imported-note-1",
		Title:    "Imported Notion Note",
		Body:     "This note was imported from Notion sync.",
		FolderID: "__uncategorized",
		Tags:     `["sync"]`,
	})
	if err != nil {
		t.Fatalf("create note with id: %v", err)
	}
	if created.ID != "imported-note-1" {
		t.Fatalf("expected caller-provided id, got %+v", created)
	}

	results, total, err := Search("Notion", 1, 10)
	if err != nil {
		t.Fatalf("search imported note: %v", err)
	}
	if total == 0 {
		t.Fatalf("expected imported note to be searchable, got total=0 results=%+v", results)
	}
	for _, result := range results {
		if result.Type == "note" && result.ID == "imported-note-1" {
			return
		}
	}
	t.Fatalf("missing imported note search result in %+v", results)
}

func TestNoteSearchIndexPostgresTracksUpdateAndDelete(t *testing.T) {
	schema := fmt.Sprintf("fs_test_note_search_lifecycle_%d", time.Now().UnixNano())
	databaseURL := createPostgresTestSchema(t, schema)

	store := openPostgresStoreForTest(t, databaseURL)
	defer store.Close()
	SetStore(store)
	t.Cleanup(func() { SetStore(nil) })
	truncatePostgresTables(t)

	note := &model.Note{Title: "old searchable note", Body: "old body", FolderID: "__uncategorized", Tags: `[]`}
	if err := CreateNote(note); err != nil {
		t.Fatalf("create note: %v", err)
	}
	title := "new searchable note"
	if _, err := UpdateNote(note.ID, &model.UpdateNoteRequest{Title: &title}); err != nil {
		t.Fatalf("update note: %v", err)
	}
	oldResults, oldTotal, err := Search("old searchable", 1, 10)
	if err != nil {
		t.Fatalf("search old note title: %v", err)
	}
	if oldTotal != 0 || len(oldResults) != 0 {
		t.Fatalf("expected old note title to disappear, total=%d results=%+v", oldTotal, oldResults)
	}
	newResults, newTotal, err := Search("new searchable", 1, 10)
	if err != nil {
		t.Fatalf("search new note title: %v", err)
	}
	if newTotal == 0 {
		t.Fatalf("expected new note title to appear, results=%+v", newResults)
	}
	if err := DeleteNote(note.ID); err != nil {
		t.Fatalf("delete note: %v", err)
	}
	deletedResults, deletedTotal, err := Search("new searchable", 1, 10)
	if err != nil {
		t.Fatalf("search deleted note: %v", err)
	}
	if deletedTotal != 0 || len(deletedResults) != 0 {
		t.Fatalf("expected deleted note to disappear, total=%d results=%+v", deletedTotal, deletedResults)
	}
}
```

Add this helper in the same file:

```go
func truncatePostgresTables(t *testing.T) {
	t.Helper()
	_, err := DB.Exec(`
		TRUNCATE note_sync_state, sync_targets, inbox, events, roadmap_resources,
			roadmap_edges, tasks, roadmap_nodes, learning_roadmaps,
			task_projects, notes, folders, search_index
		RESTART IDENTITY CASCADE
	`)
	if err != nil {
		t.Fatalf("truncate tables: %v", err)
	}
	if _, err := DB.Exec(`
		INSERT INTO folders (id, name, sort_order, created_at)
		VALUES
			('__uncategorized', '未分类', 0, now()),
			('__work', '工作', 1, now()),
			('__personal', '个人', 2, now())
	`); err != nil {
		t.Fatalf("seed default folder: %v", err)
	}
	if _, err := DB.Exec(`
		INSERT INTO task_projects (id, name, type, description, created_at, updated_at)
		VALUES ('personal', '个人', 'personal', '默认个人任务项目', now(), now())
	`); err != nil {
		t.Fatalf("seed personal project: %v", err)
	}
}
```

- [ ] **Step 2: Run notes test and verify red**

Run:

```powershell
cd backend
$env:FLOWSPACE_TEST_DATABASE_URL = "postgres://flowspace:flowspace@127.0.0.1:15432/flowspace_test?sslmode=disable"
go test ./internal/repository -run "TestNotesPostgresStoresTagsAsArrayAndReturnsJSONString|TestCreateNoteWithIDPostgresUpdatesSearchIndex" -count=1
```

Expected: fail because existing SQL uses SQLite placeholders, inserts tags as string into `TEXT[]`, or does not make `CreateNoteWithID` searchable yet.

- [ ] **Step 3: Update notes SQL**

In `backend/internal/repository/notes.go`:

- Replace `?` placeholders with `$1`, `$2`, etc.
- Use `pgSetBuilder` for `UpdateNote`; do not build dynamic `SET` clauses with hard-coded `?` placeholders.
- Convert input `Tags` with `tagsJSONStringToArray`.
- Import `github.com/lib/pq`.
- Pass `tags TEXT[]` with `pq.Array(tags)` and scan with `pq.Array(&tags)`.
- Return JSON string with `tagsArrayToJSONString`.

Concrete insert pattern:

```go
tags, err := tagsJSONStringToArray(n.Tags)
if err != nil {
	return err
}
_, err = DB.Exec(`
	INSERT INTO notes (id, title, body, folder_id, tags, created_at, updated_at)
	VALUES ($1, $2, $3, $4, $5::text[], $6, $7)
`, n.ID, n.Title, n.Body, n.FolderID, pq.Array(tags), unixToTime(n.CreatedAt), unixToTime(n.UpdatedAt))
```

When scanning:

```go
var tags []string
if err := row.Scan(&n.ID, &n.Title, &n.Body, &n.FolderID, pq.Array(&tags), &createdAt, &updatedAt); err != nil {
	return nil, err
}
n.Tags = tagsArrayToJSONString(tags)
```

- [ ] **Step 4: Update search index writes**

After note create/update/delete, update `search_index`. `CreateNote`, `CreateNoteWithID`, `UpdateNote`, and `DeleteNote` must wrap the source table write and the `search_index` upsert/delete in the same `sql.Tx`; do not commit the note row first and update search later.

Use helpers with transaction parameters, for example `upsertNoteSearchIndex(tx *sql.Tx, note model.Note)` and `deleteSearchIndex(tx *sql.Tx, entityType, entityID string)`.

```sql
INSERT INTO search_index (entity_type, entity_id, title, content, tags, updated_at)
VALUES ('note', $1, $2, $3, $4::text[], $5)
ON CONFLICT (entity_type, entity_id) DO UPDATE SET
  title = excluded.title,
  content = excluded.content,
  tags = excluded.tags,
  updated_at = excluded.updated_at
```

On delete:

```sql
DELETE FROM search_index WHERE entity_type = 'note' AND entity_id = $1
```

- [ ] **Step 5: Write PostgreSQL search test**

Create `backend/internal/repository/search_postgres_test.go`:

```go
package repository

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/lib/pq"
)

func TestSearchPostgresReturnsHighlightsAndEntityFields(t *testing.T) {
	schema := fmt.Sprintf("fs_test_search_%d", time.Now().UnixNano())
	databaseURL := createPostgresTestSchema(t, schema)

	store := openPostgresStoreForTest(t, databaseURL)
	defer store.Close()
	SetStore(store)
	t.Cleanup(func() { SetStore(nil) })
	truncatePostgresTables(t)

	now := time.Now().UTC()
	if _, err := DB.Exec(`
		INSERT INTO notes (id, title, body, folder_id, tags, created_at, updated_at)
		VALUES ($1, $2, $3, '__uncategorized', $4::text[], $5, $5)
	`, "note-search", "PostgreSQL migration 中文笔记", "数据库同步正文", pq.Array([]string{"sync"}), now); err != nil {
		t.Fatalf("insert note: %v", err)
	}
	if _, err := DB.Exec(`
		INSERT INTO tasks (id, title, content, project_id, done, status, horizon, scope, created_at, updated_at)
		VALUES ($1, $2, $3, 'personal', true, 'open', 'week', 'daily', $4, $4)
	`, "task-search", "中文搜索任务", "检查 task done 字段", now); err != nil {
		t.Fatalf("insert task: %v", err)
	}
	if _, err := DB.Exec(`
		INSERT INTO events (id, title, start_at, end_at, location, kind, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, 'work', $3, $3)
	`, "event-search", "中文搜索会议", now, now.Add(time.Hour), "会议室A"); err != nil {
		t.Fatalf("insert event: %v", err)
	}
	for _, row := range []struct {
		entityType string
		entityID   string
		title      string
		content    string
		tags       []string
	}{
		{"note", "note-search", "PostgreSQL migration 中文笔记", "数据库同步正文", []string{"sync"}},
		{"task", "task-search", "中文搜索任务", "检查 task done 字段", []string{}},
		{"event", "event-search", "中文搜索会议", "会议室A work", []string{}},
		{"note", "missing-note", "stale ghost note", "stale content", []string{}},
	} {
		if _, err := DB.Exec(`
			INSERT INTO search_index (entity_type, entity_id, title, content, tags, updated_at)
			VALUES ($1, $2, $3, $4, $5::text[], $6)
		`, row.entityType, row.entityID, row.title, row.content, pq.Array(row.tags), now); err != nil {
			t.Fatalf("insert search index %s/%s: %v", row.entityType, row.entityID, err)
		}
	}

	results, total, err := Search("中文", 1, 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if total < 3 {
		t.Fatalf("expected at least 3 search results, got total=%d results=%+v", total, results)
	}
	seen := map[string]bool{}
	for _, result := range results {
		key := result.Type + "/" + result.ID
		if seen[key] {
			t.Fatalf("duplicate search result %s in %+v", key, results)
		}
		seen[key] = true
	}
	if total != len(seen) {
		t.Fatalf("expected total to equal deduplicated result count, total=%d unique=%d results=%+v", total, len(seen), results)
	}

	note := findSearchResult(t, results, "note", "note-search")
	if note.FolderID == nil || *note.FolderID != "__uncategorized" {
		t.Fatalf("expected note folder id, got %+v", note)
	}
	if !strings.Contains(note.Highlight, "<mark>") {
		t.Fatalf("expected note highlight to contain mark tags, got %q", note.Highlight)
	}

	prefixResults, prefixTotal, err := Search("Post migr", 1, 10)
	if err != nil {
		t.Fatalf("search prefix query: %v", err)
	}
	if prefixTotal == 0 {
		t.Fatalf("expected prefix search to match PostgreSQL migration, got total=0 results=%+v", prefixResults)
	}
	findSearchResult(t, prefixResults, "note", "note-search")

	tagResults, tagTotal, err := Search("#sync", 1, 10)
	if err != nil {
		t.Fatalf("search normalized tag: %v", err)
	}
	if tagTotal == 0 {
		t.Fatalf("expected #sync to match sync tag, got total=0 results=%+v", tagResults)
	}
	findSearchResult(t, tagResults, "note", "note-search")

	task := findSearchResult(t, results, "task", "task-search")
	if task.Done == nil || *task.Done != 1 {
		t.Fatalf("expected task done=1, got %+v", task)
	}

	event := findSearchResult(t, results, "event", "event-search")
	if event.Kind == nil || *event.Kind != "work" {
		t.Fatalf("expected event kind=work, got %+v", event)
	}

	locationResults, locationTotal, err := Search("会议室A", 1, 10)
	if err != nil {
		t.Fatalf("search event location: %v", err)
	}
	if locationTotal != 1 {
		t.Fatalf("expected one event location result, got total=%d results=%+v", locationTotal, locationResults)
	}
	findSearchResult(t, locationResults, "event", "event-search")

	staleResults, staleTotal, err := Search("stale ghost", 1, 10)
	if err != nil {
		t.Fatalf("search stale index row: %v", err)
	}
	if staleTotal != 0 || len(staleResults) != 0 {
		t.Fatalf("expected stale index row to be filtered, total=%d results=%+v", staleTotal, staleResults)
	}
}

func findSearchResult(t *testing.T, results []model.SearchResult, resultType, id string) model.SearchResult {
	t.Helper()
	for _, result := range results {
		if result.Type == resultType && result.ID == id {
			return result
		}
	}
	t.Fatalf("missing %s result %s in %+v", resultType, id, results)
	return model.SearchResult{}
}
```

- [ ] **Step 6: Update PostgreSQL search implementation**

In `backend/internal/repository/search.go`, replace the SQLite FTS5 functions with PostgreSQL queries against `search_index`:

- Build a PostgreSQL prefix tsquery from the raw query before executing SQL. For example, `Post migr` becomes `Post:* & migr:*`; pass the raw query as `$1` for fallback and the prefix tsquery string as `$2` for FTS.
- Preserve the existing empty-query guard in Go: `strings.TrimSpace(q) == ""` must return no results before any SQL is executed.
- If `buildPostgresPrefixTSQuery` returns `false`, do not call `to_tsquery('simple', '')`; execute a fallback-only query path using trigram/substring/tag matching.
- Use `to_tsquery('simple', $2)` and `ts_headline('simple', content, to_tsquery('simple', $2), 'StartSel=<mark>, StopSel=</mark>, MaxWords=40')` for tokenized matches.
- Use trigram/substring fallback for CJK and short query matches. Match title/content substrings plus normalized tags where `lower(tag) = lower(trim(leading '#' from $1))`.
- Rank fallback matches with title, content, and tag signals; do not use only `similarity(title, $1)`, because content-only and tag-only matches need stable ordering.
- Deduplicate by `(entity_type, entity_id)` before applying `LIMIT/OFFSET`.
- Compute `total` from the same deduplicated CTE with `COUNT(*)`.
- Join back to source tables so API fields stay compatible:
  - `notes.folder_id` -> `SearchResult.FolderID`
  - `tasks.done` -> `SearchResult.Done` as `0/1`
  - `events.kind` -> `SearchResult.Kind`
- Filter stale index rows after the source table joins: return only `note` rows with `n.id IS NOT NULL`, `task` rows with `t.id IS NOT NULL`, and `event` rows with `e.id IS NOT NULL`.
- Build fallback highlights in Go when `ts_headline` returns empty or no `<mark>` tag.

Add this helper in `backend/internal/repository/search.go`:

```go
func buildPostgresPrefixTSQuery(raw string) (string, bool) {
	parts := strings.Fields(strings.TrimSpace(raw))
	tokens := make([]string, 0, len(parts))
	for _, part := range parts {
		token := sanitizeTSQueryToken(part)
		if len([]rune(token)) < 2 {
			continue
		}
		tokens = append(tokens, token+":*")
	}
	if len(tokens) == 0 {
		return "", false
	}
	return strings.Join(tokens, " & "), true
}

func sanitizeTSQueryToken(value string) string {
	var builder strings.Builder
	for _, r := range value {
		if r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}
```

Concrete source join shape:

```sql
WITH fts AS (
  SELECT
    entity_type,
    entity_id,
    title,
    content,
    updated_at,
    ts_rank_cd(search_vector, to_tsquery('simple', $2)) AS rank,
    ts_headline(
      'simple',
      content,
      to_tsquery('simple', $2),
      'StartSel=<mark>, StopSel=</mark>, MaxWords=40'
    ) AS highlight
  FROM search_index
  WHERE search_vector @@ to_tsquery('simple', $2)
),
fallback AS (
  SELECT
    entity_type,
    entity_id,
    title,
    content,
    updated_at,
    GREATEST(
      similarity(title, $1),
      similarity(content, $1) * 0.75,
      CASE WHEN EXISTS (
        SELECT 1 FROM unnest(tags) AS tag
        WHERE lower(tag) = lower(trim(leading '#' from $1))
      ) THEN 0.9 ELSE 0 END
    ) AS rank,
    content AS highlight
  FROM search_index
  WHERE title ILIKE '%' || $1 || '%'
     OR content ILIKE '%' || $1 || '%'
     OR EXISTS (
       SELECT 1 FROM unnest(tags) AS tag
       WHERE lower(tag) = lower(trim(leading '#' from $1))
     )
),
matched AS (
  SELECT * FROM fts
  UNION ALL
  SELECT * FROM fallback
),
deduped AS (
  SELECT DISTINCT ON (entity_type, entity_id)
    entity_type,
    entity_id,
    title,
    highlight,
    rank,
    updated_at
  FROM matched
  ORDER BY entity_type, entity_id, rank DESC, updated_at DESC
)
SELECT
  d.entity_type,
  d.entity_id,
  d.title,
  d.highlight,
  n.folder_id,
  CASE WHEN t.done THEN 1 ELSE 0 END AS done,
  e.kind,
  extract(epoch FROM d.updated_at)::bigint AS updated_at
FROM deduped d
LEFT JOIN notes n ON d.entity_type = 'note' AND n.id = d.entity_id
LEFT JOIN tasks t ON d.entity_type = 'task' AND t.id = d.entity_id
LEFT JOIN events e ON d.entity_type = 'event' AND e.id = d.entity_id
WHERE (d.entity_type = 'note' AND n.id IS NOT NULL)
   OR (d.entity_type = 'task' AND t.id IS NOT NULL)
   OR (d.entity_type = 'event' AND e.id IS NOT NULL)
ORDER BY d.rank DESC, d.updated_at DESC
LIMIT $3 OFFSET $4
```

Concrete total query shape:

```sql
WITH fts AS (
  SELECT entity_type, entity_id, ts_rank_cd(search_vector, to_tsquery('simple', $2)) AS rank, updated_at
  FROM search_index
  WHERE search_vector @@ to_tsquery('simple', $2)
),
fallback AS (
  SELECT
    entity_type,
    entity_id,
    GREATEST(
      similarity(title, $1),
      similarity(content, $1) * 0.75,
      CASE WHEN EXISTS (
        SELECT 1 FROM unnest(tags) AS tag
        WHERE lower(tag) = lower(trim(leading '#' from $1))
      ) THEN 0.9 ELSE 0 END
    ) AS rank,
    updated_at
  FROM search_index
  WHERE title ILIKE '%' || $1 || '%'
     OR content ILIKE '%' || $1 || '%'
     OR EXISTS (
       SELECT 1 FROM unnest(tags) AS tag
       WHERE lower(tag) = lower(trim(leading '#' from $1))
     )
),
matched AS (
  SELECT * FROM fts
  UNION ALL
  SELECT * FROM fallback
),
deduped AS (
  SELECT DISTINCT ON (entity_type, entity_id) entity_type, entity_id, rank, updated_at
  FROM matched
  ORDER BY entity_type, entity_id, rank DESC, updated_at DESC
)
SELECT COUNT(*)
FROM deduped d
LEFT JOIN notes n ON d.entity_type = 'note' AND n.id = d.entity_id
LEFT JOIN tasks t ON d.entity_type = 'task' AND t.id = d.entity_id
LEFT JOIN events e ON d.entity_type = 'event' AND e.id = d.entity_id
WHERE (d.entity_type = 'note' AND n.id IS NOT NULL)
   OR (d.entity_type = 'task' AND t.id IS NOT NULL)
   OR (d.entity_type = 'event' AND e.id IS NOT NULL)
```

Concrete fallback-only source query shape used when `buildPostgresPrefixTSQuery` returns `false`:

```sql
WITH fallback AS (
  SELECT
    entity_type,
    entity_id,
    title,
    content,
    updated_at,
    GREATEST(
      similarity(title, $1),
      similarity(content, $1) * 0.75,
      CASE WHEN EXISTS (
        SELECT 1 FROM unnest(tags) AS tag
        WHERE lower(tag) = lower(trim(leading '#' from $1))
      ) THEN 0.9 ELSE 0 END
    ) AS rank,
    content AS highlight
  FROM search_index
  WHERE length(trim($1)) > 0
    AND (
      title ILIKE '%' || $1 || '%'
      OR content ILIKE '%' || $1 || '%'
      OR EXISTS (
        SELECT 1 FROM unnest(tags) AS tag
        WHERE lower(tag) = lower(trim(leading '#' from $1))
      )
    )
),
deduped AS (
  SELECT DISTINCT ON (entity_type, entity_id)
    entity_type,
    entity_id,
    title,
    highlight,
    rank,
    updated_at
  FROM fallback
  ORDER BY entity_type, entity_id, rank DESC, updated_at DESC
)
SELECT
  d.entity_type,
  d.entity_id,
  d.title,
  d.highlight,
  n.folder_id,
  CASE WHEN t.done THEN 1 ELSE 0 END AS done,
  e.kind,
  extract(epoch FROM d.updated_at)::bigint AS updated_at
FROM deduped d
LEFT JOIN notes n ON d.entity_type = 'note' AND n.id = d.entity_id
LEFT JOIN tasks t ON d.entity_type = 'task' AND t.id = d.entity_id
LEFT JOIN events e ON d.entity_type = 'event' AND e.id = d.entity_id
WHERE (d.entity_type = 'note' AND n.id IS NOT NULL)
   OR (d.entity_type = 'task' AND t.id IS NOT NULL)
   OR (d.entity_type = 'event' AND e.id IS NOT NULL)
ORDER BY d.rank DESC, d.updated_at DESC
LIMIT $2 OFFSET $3
```

Concrete fallback-only total query shape:

```sql
WITH fallback AS (
  SELECT
    entity_type,
    entity_id,
    GREATEST(
      similarity(title, $1),
      similarity(content, $1) * 0.75,
      CASE WHEN EXISTS (
        SELECT 1 FROM unnest(tags) AS tag
        WHERE lower(tag) = lower(trim(leading '#' from $1))
      ) THEN 0.9 ELSE 0 END
    ) AS rank,
    updated_at
  FROM search_index
  WHERE length(trim($1)) > 0
    AND (
      title ILIKE '%' || $1 || '%'
      OR content ILIKE '%' || $1 || '%'
      OR EXISTS (
        SELECT 1 FROM unnest(tags) AS tag
        WHERE lower(tag) = lower(trim(leading '#' from $1))
      )
    )
),
deduped AS (
  SELECT DISTINCT ON (entity_type, entity_id) entity_type, entity_id, rank, updated_at
  FROM fallback
  ORDER BY entity_type, entity_id, rank DESC, updated_at DESC
)
SELECT COUNT(*)
FROM deduped d
LEFT JOIN notes n ON d.entity_type = 'note' AND n.id = d.entity_id
LEFT JOIN tasks t ON d.entity_type = 'task' AND t.id = d.entity_id
LEFT JOIN events e ON d.entity_type = 'event' AND e.id = d.entity_id
WHERE (d.entity_type = 'note' AND n.id IS NOT NULL)
   OR (d.entity_type = 'task' AND t.id IS NOT NULL)
   OR (d.entity_type = 'event' AND e.id IS NOT NULL)
```

- [ ] **Step 7: Run notes and search tests and verify green**

Run:

```powershell
cd backend
$env:FLOWSPACE_TEST_DATABASE_URL = "postgres://flowspace:flowspace@127.0.0.1:15432/flowspace_test?sslmode=disable"
go test ./internal/repository -run "TestNotesPostgres|TestUpdateNote|TestNoteSearchIndex|TestSearchPostgres|TestTagsJSONStringRoundTrip" -count=1
```

Expected: pass.

- [ ] **Step 8: Commit**

```powershell
git add backend/internal/repository/notes.go backend/internal/repository/folders.go backend/internal/repository/search.go backend/internal/repository/notes_postgres_test.go backend/internal/repository/search_postgres_test.go
git commit -m "feat: migrate notes storage to postgres"
```

---

### Task 7: Migrate Tasks and Today Queries

**Files:**
- Modify: `backend/internal/repository/tasks.go`
- Modify: `backend/internal/service/today.go` if time/date assumptions break
- Test: `backend/internal/repository/task_projects_test.go`, task repository tests

- [ ] **Step 1: Write PostgreSQL task filtering test**

Create `backend/internal/repository/tasks_postgres_test.go`:

```go
package repository

import (
	"fmt"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/model"
)

func TestTasksPostgresFiltersByPlannedDateAndProject(t *testing.T) {
	schema := fmt.Sprintf("fs_test_tasks_%d", time.Now().UnixNano())
	databaseURL := createPostgresTestSchema(t, schema)

	store := openPostgresStoreForTest(t, databaseURL)
	defer store.Close()
	SetStore(store)
	t.Cleanup(func() { SetStore(nil) })
	truncatePostgresTables(t)

	project, err := CreateTaskProject(&model.CreateTaskProjectRequest{Name: "学习写小说", Type: "regular"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	plannedDate := "2026-06-16"
	task := &model.Task{
		Title:       "读完故事这本书",
		Content:     "阅读并记录笔记",
		ProjectID:   &project.ID,
		PlannedDate: &plannedDate,
		Status:      "open",
		Horizon:     "week",
		Scope:       "daily",
	}
	if err := CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	tasks, total, err := GetTasks("", "active", "", "", project.ID, plannedDate, 1, 20)
	if err != nil {
		t.Fatalf("get tasks: %v", err)
	}
	if total != 1 || len(tasks) != 1 || tasks[0].Title != "读完故事这本书" {
		t.Fatalf("unexpected tasks total=%d items=%+v", total, tasks)
	}
}

func TestUpdateTaskPostgresUpdatesDynamicFields(t *testing.T) {
	schema := fmt.Sprintf("fs_test_update_task_%d", time.Now().UnixNano())
	databaseURL := createPostgresTestSchema(t, schema)

	store := openPostgresStoreForTest(t, databaseURL)
	defer store.Close()
	SetStore(store)
	t.Cleanup(func() { SetStore(nil) })
	truncatePostgresTables(t)

	task := &model.Task{Title: "旧任务", Content: "旧内容", Status: "open", Horizon: "week", Scope: "daily"}
	if err := CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	title := "新任务"
	done := 1
	status := "done"
	updated, err := UpdateTask(task.ID, &model.UpdateTaskRequest{Title: &title, Done: &done, Status: &status})
	if err != nil {
		t.Fatalf("update task: %v", err)
	}
	if updated.Title != "新任务" || updated.Done != 1 || updated.Status != "done" {
		t.Fatalf("unexpected updated task: %+v", updated)
	}
}

func TestTaskSearchIndexPostgresTracksUpdateAndDelete(t *testing.T) {
	schema := fmt.Sprintf("fs_test_task_search_lifecycle_%d", time.Now().UnixNano())
	databaseURL := createPostgresTestSchema(t, schema)

	store := openPostgresStoreForTest(t, databaseURL)
	defer store.Close()
	SetStore(store)
	t.Cleanup(func() { SetStore(nil) })
	truncatePostgresTables(t)

	task := &model.Task{Title: "old searchable task", Content: "old task body", Status: "open", Horizon: "week", Scope: "daily"}
	if err := CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}
	title := "new searchable task"
	if _, err := UpdateTask(task.ID, &model.UpdateTaskRequest{Title: &title}); err != nil {
		t.Fatalf("update task: %v", err)
	}
	oldResults, oldTotal, err := Search("old searchable task", 1, 10)
	if err != nil {
		t.Fatalf("search old task title: %v", err)
	}
	if oldTotal != 0 || len(oldResults) != 0 {
		t.Fatalf("expected old task title to disappear, total=%d results=%+v", oldTotal, oldResults)
	}
	newResults, newTotal, err := Search("new searchable task", 1, 10)
	if err != nil {
		t.Fatalf("search new task title: %v", err)
	}
	if newTotal == 0 {
		t.Fatalf("expected new task title to appear, results=%+v", newResults)
	}
	if err := DeleteTask(task.ID); err != nil {
		t.Fatalf("delete task: %v", err)
	}
	deletedResults, deletedTotal, err := Search("new searchable task", 1, 10)
	if err != nil {
		t.Fatalf("search deleted task: %v", err)
	}
	if deletedTotal != 0 || len(deletedResults) != 0 {
		t.Fatalf("expected deleted task to disappear, total=%d results=%+v", deletedTotal, deletedResults)
	}
}

func TestTaskPostgresAcceptsMigratedAndCancelledStatuses(t *testing.T) {
	schema := fmt.Sprintf("fs_test_task_status_%d", time.Now().UnixNano())
	databaseURL := createPostgresTestSchema(t, schema)

	store := openPostgresStoreForTest(t, databaseURL)
	defer store.Close()
	SetStore(store)
	t.Cleanup(func() { SetStore(nil) })
	truncatePostgresTables(t)

	task := &model.Task{Title: "状态兼容任务", Content: "历史状态", Status: "open", Horizon: "week", Scope: "daily"}
	if err := CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	for _, status := range []string{"migrated", "cancelled"} {
		updated, err := UpdateTask(task.ID, &model.UpdateTaskRequest{Status: &status})
		if err != nil {
			t.Fatalf("update status %s: %v", status, err)
		}
		if updated.Status != status {
			t.Fatalf("expected status %s, got %+v", status, updated)
		}
	}
}

func TestTodayTasksPostgresUsesLocalDateBoundaries(t *testing.T) {
	schema := fmt.Sprintf("fs_test_today_tasks_%d", time.Now().UnixNano())
	databaseURL := createPostgresTestSchema(t, schema)

	store := openPostgresStoreForTest(t, databaseURL)
	defer store.Close()
	SetStore(store)
	t.Cleanup(func() { SetStore(nil) })
	truncatePostgresTables(t)

	localDay := time.Date(2026, 6, 16, 0, 0, 0, 0, time.Local)
	todayStart := localDay.Unix()
	todayEnd := localDay.Add(24 * time.Hour).Unix()
	overdueCutoff := todayStart
	plannedDate := "2026-06-16"
	due0030 := time.Date(2026, 6, 16, 0, 30, 0, 0, time.Local).Unix()
	due2330 := time.Date(2026, 6, 16, 23, 30, 0, 0, time.Local).Unix()

	cases := []model.Task{
		{Title: "due at local 00:30", Due: &due0030, Status: "open", Horizon: "week", Scope: "daily"},
		{Title: "due at local 23:30", Due: &due2330, Status: "open", Horizon: "week", Scope: "daily"},
		{Title: "planned date without due", PlannedDate: &plannedDate, Status: "open", Horizon: "week", Scope: "daily"},
		{Title: "active long task", Status: "active", Horizon: "long", Scope: "monthly"},
	}
	for i := range cases {
		if err := CreateTask(&cases[i]); err != nil {
			t.Fatalf("create task %q: %v", cases[i].Title, err)
		}
	}

	todayTasks, overdueTasks, err := GetTodayTasks(todayStart, todayEnd, overdueCutoff)
	if err != nil {
		t.Fatalf("get today tasks: %v", err)
	}
	if len(overdueTasks) != 0 {
		t.Fatalf("expected no overdue tasks, got %+v", overdueTasks)
	}

	found := map[string]bool{}
	for _, task := range todayTasks {
		found[task.Title] = true
	}
	for _, title := range []string{"due at local 00:30", "due at local 23:30", "planned date without due", "active long task"} {
		if !found[title] {
			t.Fatalf("expected %q in today tasks, got %+v", title, todayTasks)
		}
	}
}

func TestTaskProjectsPostgresSortsCaseInsensitively(t *testing.T) {
	schema := fmt.Sprintf("fs_test_task_project_sort_%d", time.Now().UnixNano())
	databaseURL := createPostgresTestSchema(t, schema)

	store := openPostgresStoreForTest(t, databaseURL)
	defer store.Close()
	SetStore(store)
	t.Cleanup(func() { SetStore(nil) })
	truncatePostgresTables(t)

	beta, err := CreateTaskProject(&model.CreateTaskProjectRequest{Name: "beta", Type: "regular"})
	if err != nil {
		t.Fatalf("create beta: %v", err)
	}
	alpha, err := CreateTaskProject(&model.CreateTaskProjectRequest{Name: "Alpha", Type: "regular"})
	if err != nil {
		t.Fatalf("create Alpha: %v", err)
	}
	if _, err := DB.Exec(`UPDATE task_projects SET updated_at = to_timestamp(1800000000) WHERE id IN ($1, $2)`, beta.ID, alpha.ID); err != nil {
		t.Fatalf("normalize updated_at: %v", err)
	}

	projects, err := ListTaskProjects()
	if err != nil {
		t.Fatalf("list projects: %v", err)
	}
	if len(projects) < 3 || projects[0].ID != "personal" {
		t.Fatalf("expected personal project first, got %+v", projects)
	}
	if projects[1].Name != "Alpha" || projects[2].Name != "beta" {
		t.Fatalf("expected case-insensitive order Alpha then beta, got %+v", projects)
	}
}
```

- [ ] **Step 2: Run task test and verify red**

Run:

```powershell
cd backend
$env:FLOWSPACE_TEST_DATABASE_URL = "postgres://flowspace:flowspace@127.0.0.1:15432/flowspace_test?sslmode=disable"
go test ./internal/repository -run TestTasksPostgresFiltersByPlannedDateAndProject -count=1
```

Expected: fail due to SQLite placeholders or date/time mismatch.

- [ ] **Step 3: Update task SQL**

In `backend/internal/repository/tasks.go`:

- Convert placeholders to `$n`.
- Use `pgSetBuilder` for `UpdateTask` and `UpdateTaskProject`.
- Map `due_at TIMESTAMPTZ` to API `Due *int64`.
- Map `planned_date DATE` to API `PlannedDate *string`.
- Convert `done BOOLEAN` to API `Done int` for compatibility.
- Upsert task project with PostgreSQL `ON CONFLICT (name) DO UPDATE`.
- Replace SQLite `name COLLATE NOCASE ASC` with `LOWER(name) ASC`.

Concrete date scan pattern:

```go
var dueAt sql.NullTime
var plannedDate sql.NullTime
var done bool
```

Then:

```go
if dueAt.Valid {
	value := timeToUnix(dueAt.Time)
	task.Due = &value
}
if plannedDate.Valid {
	value := plannedDate.Time.Format("2006-01-02")
	task.PlannedDate = &value
}
if done {
	task.Done = 1
}
```

- [ ] **Step 4: Sync task search index**

On create/update/delete, write `search_index` with `entity_type='task'` and `content=task.Content`. The task row mutation and `search_index` upsert/delete must run in the same `sql.Tx`, including `DeleteTask`, so search cannot observe committed tasks without indexes or stale indexes for deleted tasks.

- [ ] **Step 5: Run task tests and verify green**

Run:

```powershell
cd backend
$env:FLOWSPACE_TEST_DATABASE_URL = "postgres://flowspace:flowspace@127.0.0.1:15432/flowspace_test?sslmode=disable"
go test ./internal/repository -run "TestTasksPostgres|TestUpdateTask|TestTaskProjectsPostgres|TestTask|TestTodayTasksPostgres" -count=1
```

Expected: pass.

- [ ] **Step 6: Commit**

```powershell
git add backend/internal/repository/tasks.go backend/internal/repository/tasks_postgres_test.go backend/internal/service/today.go
git commit -m "feat: migrate tasks storage to postgres"
```

---

### Task 8: Migrate Events and Inbox

**Files:**
- Modify: `backend/internal/repository/events.go`
- Modify: `backend/internal/repository/inbox.go`
- Test: new PostgreSQL event/inbox repository tests

- [ ] **Step 1: Write event range test**

Create `backend/internal/repository/events_postgres_test.go`:

```go
package repository

import (
	"fmt"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/model"
)

func TestEventsPostgresStoresTimeRange(t *testing.T) {
	schema := fmt.Sprintf("fs_test_events_%d", time.Now().UnixNano())
	databaseURL := createPostgresTestSchema(t, schema)

	store := openPostgresStoreForTest(t, databaseURL)
	defer store.Close()
	SetStore(store)
	t.Cleanup(func() { SetStore(nil) })
	truncatePostgresTables(t)

	event := &model.Event{
		Title:     "设计 PostgreSQL 迁移",
		StartTime: 1800000000,
		EndTime:   1800003600,
		Kind:      "work",
	}
	if err := CreateEvent(event); err != nil {
		t.Fatalf("create event: %v", err)
	}

	var overlaps bool
	if err := DB.QueryRow(`
		SELECT EXISTS (
			SELECT 1 FROM events
			WHERE time_range && tstzrange(to_timestamp($1), to_timestamp($2), '[)')
		)
	`, int64(1800001800), int64(1800005400)).Scan(&overlaps); err != nil {
		t.Fatalf("query overlap: %v", err)
	}
	if !overlaps {
		t.Fatal("expected event range to overlap")
	}
}

func TestEventsPostgresRepositoryQueriesUseTimeRangeBoundaries(t *testing.T) {
	schema := fmt.Sprintf("fs_test_events_range_%d", time.Now().UnixNano())
	databaseURL := createPostgresTestSchema(t, schema)

	store := openPostgresStoreForTest(t, databaseURL)
	defer store.Close()
	SetStore(store)
	t.Cleanup(func() { SetStore(nil) })
	truncatePostgresTables(t)

	localDay := time.Date(2026, 6, 16, 0, 0, 0, 0, time.Local)
	dayStart := localDay.Unix()
	dayEnd := localDay.Add(24 * time.Hour).Unix()
	location := "会议室A"

	events := []model.Event{
		{Title: "event at local 00:30", StartTime: localDay.Add(30 * time.Minute).Unix(), EndTime: localDay.Add(time.Hour).Unix(), Location: &location, Kind: "work"},
		{Title: "event at local 23:30", StartTime: localDay.Add(23*time.Hour + 30*time.Minute).Unix(), EndTime: localDay.Add(24*time.Hour - time.Minute).Unix(), Location: &location, Kind: "work"},
		{Title: "cross day event", StartTime: localDay.Add(-30 * time.Minute).Unix(), EndTime: localDay.Add(30 * time.Minute).Unix(), Location: &location, Kind: "work"},
	}
	for i := range events {
		if err := CreateEvent(&events[i]); err != nil {
			t.Fatalf("create event %q: %v", events[i].Title, err)
		}
	}

	monthEvents, total, err := GetEvents(dayStart, dayEnd, 1, 20)
	if err != nil {
		t.Fatalf("get events: %v", err)
	}
	if total != 3 {
		t.Fatalf("expected 3 events in range, total=%d events=%+v", total, monthEvents)
	}

	todayEvents, err := GetTodayEvents(dayStart, dayEnd)
	if err != nil {
		t.Fatalf("get today events: %v", err)
	}
	found := map[string]bool{}
	for _, event := range todayEvents {
		found[event.Title] = true
	}
	for _, title := range []string{"event at local 00:30", "event at local 23:30", "cross day event"} {
		if !found[title] {
			t.Fatalf("expected %q in today events, got %+v", title, todayEvents)
		}
	}
}

func TestEventSearchIndexPostgresTracksUpdateAndDelete(t *testing.T) {
	schema := fmt.Sprintf("fs_test_event_search_lifecycle_%d", time.Now().UnixNano())
	databaseURL := createPostgresTestSchema(t, schema)

	store := openPostgresStoreForTest(t, databaseURL)
	defer store.Close()
	SetStore(store)
	t.Cleanup(func() { SetStore(nil) })
	truncatePostgresTables(t)

	oldLocation := "Old Room"
	event := &model.Event{
		Title:     "old searchable event",
		StartTime: time.Now().UTC().Unix(),
		EndTime:   time.Now().UTC().Add(time.Hour).Unix(),
		Location:  &oldLocation,
		Kind:      "work",
	}
	if err := CreateEvent(event); err != nil {
		t.Fatalf("create event: %v", err)
	}
	newTitle := "new searchable event"
	newLocation := "New Room"
	if _, err := UpdateEvent(event.ID, &model.UpdateEventRequest{Title: &newTitle, Location: &newLocation}); err != nil {
		t.Fatalf("update event: %v", err)
	}
	oldResults, oldTotal, err := Search("Old Room", 1, 10)
	if err != nil {
		t.Fatalf("search old event location: %v", err)
	}
	if oldTotal != 0 || len(oldResults) != 0 {
		t.Fatalf("expected old event location to disappear, total=%d results=%+v", oldTotal, oldResults)
	}
	newResults, newTotal, err := Search("New Room", 1, 10)
	if err != nil {
		t.Fatalf("search new event location: %v", err)
	}
	if newTotal == 0 {
		t.Fatalf("expected new event location to appear, results=%+v", newResults)
	}
	if err := DeleteEvent(event.ID); err != nil {
		t.Fatalf("delete event: %v", err)
	}
	deletedResults, deletedTotal, err := Search("New Room", 1, 10)
	if err != nil {
		t.Fatalf("search deleted event: %v", err)
	}
	if deletedTotal != 0 || len(deletedResults) != 0 {
		t.Fatalf("expected deleted event to disappear, total=%d results=%+v", deletedTotal, deletedResults)
	}
}

func TestInboxPostgresBatchArchiveAndDeleteUseDynamicInClause(t *testing.T) {
	schema := fmt.Sprintf("fs_test_inbox_batch_%d", time.Now().UnixNano())
	databaseURL := createPostgresTestSchema(t, schema)

	store := openPostgresStoreForTest(t, databaseURL)
	defer store.Close()
	SetStore(store)
	t.Cleanup(func() { SetStore(nil) })
	truncatePostgresTables(t)

	first := &model.InboxItem{Kind: "note", Title: "第一条"}
	second := &model.InboxItem{Kind: "task", Title: "第二条"}
	third := &model.InboxItem{Kind: "note", Title: "第三条"}
	for _, item := range []*model.InboxItem{first, second, third} {
		if err := CreateInboxItem(item); err != nil {
			t.Fatalf("create inbox item: %v", err)
		}
	}

	archived, err := BatchArchiveInbox([]string{first.ID, second.ID})
	if err != nil {
		t.Fatalf("batch archive: %v", err)
	}
	if archived != 2 {
		t.Fatalf("expected 2 archived rows, got %d", archived)
	}

	deleted, err := BatchDeleteInbox([]string{first.ID, third.ID})
	if err != nil {
		t.Fatalf("batch delete: %v", err)
	}
	if deleted != 2 {
		t.Fatalf("expected 2 deleted rows, got %d", deleted)
	}
}
```

- [ ] **Step 2: Run event test and verify red**

Run:

```powershell
cd backend
$env:FLOWSPACE_TEST_DATABASE_URL = "postgres://flowspace:flowspace@127.0.0.1:15432/flowspace_test?sslmode=disable"
go test ./internal/repository -run TestEventsPostgresStoresTimeRange -count=1
```

Expected: fail until event repository uses PostgreSQL columns.

- [ ] **Step 3: Update event SQL**

In `backend/internal/repository/events.go`:

- Insert `start_at` and `end_at` using `unixToTime`.
- Scan `start_at` and `end_at` into `time.Time`, return Unix seconds.
- Use `pgSetBuilder` for `UpdateEvent`.
- Write `search_index` with `entity_type='event'`; event create/update/delete and index upsert/delete must use the same `sql.Tx`.
- Build event `search_index.content` from `COALESCE(location, '') || ' ' || kind`, so searches for event location keep working after replacing SQLite FTS.

- [ ] **Step 4: Update inbox SQL**

In `backend/internal/repository/inbox.go`:

- Convert placeholders to `$n`.
- Use `pgInClause` for `BatchArchiveInbox` and `BatchDeleteInbox`.
- Map `archived BOOLEAN` to current API int/bool expectations.
- Store extra provider/source fields in `payload JSONB` only when future code uses them; default `{}`.

- [ ] **Step 5: Run tests and verify green**

Run:

```powershell
cd backend
$env:FLOWSPACE_TEST_DATABASE_URL = "postgres://flowspace:flowspace@127.0.0.1:15432/flowspace_test?sslmode=disable"
go test ./internal/repository -run "TestEventsPostgres|TestEventSearchIndex|TestInbox" -count=1
```

Expected: pass.

- [ ] **Step 6: Commit**

```powershell
git add backend/internal/repository/events.go backend/internal/repository/inbox.go backend/internal/repository/events_postgres_test.go
git commit -m "feat: migrate events and inbox storage to postgres"
```

---

### Task 9: Migrate Roadmaps

**Files:**
- Modify: `backend/internal/repository/roadmaps.go`
- Test: existing roadmap tests plus PostgreSQL-specific relation test

- [ ] **Step 1: Write roadmap graph relation test**

Create `backend/internal/repository/roadmaps_postgres_test.go`:

```go
package repository

import (
	"fmt"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/model"
)

func TestRoadmapPostgresPreservesNodesEdgesResources(t *testing.T) {
	schema := fmt.Sprintf("fs_test_roadmaps_%d", time.Now().UnixNano())
	databaseURL := createPostgresTestSchema(t, schema)

	store := openPostgresStoreForTest(t, databaseURL)
	defer store.Close()
	SetStore(store)
	t.Cleanup(func() { SetStore(nil) })
	truncatePostgresTables(t)

	project, err := CreateTaskProject(&model.CreateTaskProjectRequest{Name: "AI Infra 工程师", Type: "learning"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	roadmap, err := EnsureRoadmapForProject(project.ID, "AI Infra Roadmap", "系统学习")
	if err != nil {
		t.Fatalf("ensure roadmap: %v", err)
	}
	parent, err := CreateRoadmapNode(roadmap.ID, model.CreateRoadmapNodeRequest{Title: "基础", Type: "milestone"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	child, err := CreateRoadmapNode(roadmap.ID, model.CreateRoadmapNodeRequest{ParentID: &parent.ID, Title: "网络基础", Type: "task"})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	if err := CreateRoadmapEdge(roadmap.ID, parent.ID, child.ID, "solid"); err != nil {
		t.Fatalf("create edge: %v", err)
	}

	loaded, err := GetRoadmapByProjectID(project.ID)
	if err != nil {
		t.Fatalf("load roadmap: %v", err)
	}
	if len(loaded.Nodes) != 2 || len(loaded.Edges) != 1 {
		t.Fatalf("unexpected roadmap graph: %+v", loaded)
	}
}

func TestRoadmapPostgresStoresArticleSearchQueriesAndFailedStatus(t *testing.T) {
	schema := fmt.Sprintf("fs_test_roadmap_queries_%d", time.Now().UnixNano())
	databaseURL := createPostgresTestSchema(t, schema)

	store := openPostgresStoreForTest(t, databaseURL)
	defer store.Close()
	SetStore(store)
	t.Cleanup(func() { SetStore(nil) })
	truncatePostgresTables(t)

	project, err := CreateTaskProject(&model.CreateTaskProjectRequest{Name: "PostgreSQL 学习", Type: "learning"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	roadmap, err := ReplaceLearningRoadmap(&model.LearningRoadmap{
		ProjectID: project.ID,
		Title:     "PostgreSQL 学习路线",
		Goal:      "掌握数据库迁移",
		Status:    "ready",
		Nodes: []model.RoadmapNode{
			{
				Title:                "官方文档",
				Type:                 "task",
				ArticleSearchQueries: []string{"PostgreSQL migration official docs", "PostgreSQL pg_trgm tutorial"},
			},
		},
	})
	if err != nil {
		t.Fatalf("replace roadmap: %v", err)
	}
	if roadmap.Status != "ready" || len(roadmap.Nodes) != 1 {
		t.Fatalf("unexpected roadmap: %+v", roadmap)
	}
	if got := roadmap.Nodes[0].ArticleSearchQueries; len(got) != 2 || got[0] != "PostgreSQL migration official docs" || got[1] != "PostgreSQL pg_trgm tutorial" {
		t.Fatalf("article search queries did not round trip: %#v", got)
	}

	failed, err := SaveFailedLearningRoadmap(project.ID, "失败路线", "AI 生成失败")
	if err != nil {
		t.Fatalf("save failed roadmap: %v", err)
	}
	if failed.Status != "failed" {
		t.Fatalf("expected failed status, got %+v", failed)
	}
}

func TestRoadmapPostgresRejectsCrossRoadmapEdges(t *testing.T) {
	schema := fmt.Sprintf("fs_test_roadmap_edge_fk_%d", time.Now().UnixNano())
	databaseURL := createPostgresTestSchema(t, schema)

	store := openPostgresStoreForTest(t, databaseURL)
	defer store.Close()
	SetStore(store)
	t.Cleanup(func() { SetStore(nil) })
	truncatePostgresTables(t)

	firstProject, err := CreateTaskProject(&model.CreateTaskProjectRequest{Name: "第一条路线", Type: "learning"})
	if err != nil {
		t.Fatalf("create first project: %v", err)
	}
	secondProject, err := CreateTaskProject(&model.CreateTaskProjectRequest{Name: "第二条路线", Type: "learning"})
	if err != nil {
		t.Fatalf("create second project: %v", err)
	}
	firstRoadmap, err := EnsureRoadmapForProject(firstProject.ID, "第一条", "目标一")
	if err != nil {
		t.Fatalf("ensure first roadmap: %v", err)
	}
	secondRoadmap, err := EnsureRoadmapForProject(secondProject.ID, "第二条", "目标二")
	if err != nil {
		t.Fatalf("ensure second roadmap: %v", err)
	}
	firstNode, err := CreateRoadmapNode(firstRoadmap.ID, model.CreateRoadmapNodeRequest{Title: "节点一"})
	if err != nil {
		t.Fatalf("create first node: %v", err)
	}
	secondNode, err := CreateRoadmapNode(secondRoadmap.ID, model.CreateRoadmapNodeRequest{Title: "节点二"})
	if err != nil {
		t.Fatalf("create second node: %v", err)
	}

	if _, err := DB.Exec(`
		INSERT INTO roadmap_edges (id, roadmap_id, source_node_id, target_node_id, style, created_at)
		VALUES ($1, $2, $3, $4, 'solid', now())
	`, "bad-cross-roadmap-edge", firstRoadmap.ID, firstNode.ID, secondNode.ID); err == nil {
		t.Fatal("expected cross-roadmap edge to be rejected by composite foreign key")
	}

	if _, err := DB.Exec(`
		INSERT INTO roadmap_nodes (id, roadmap_id, parent_id, title, created_at, updated_at)
		VALUES ($1, $2, $3, $4, now(), now())
	`, "bad-cross-roadmap-parent", secondRoadmap.ID, firstNode.ID, "跨路线 parent"); err == nil {
		t.Fatal("expected cross-roadmap parent to be rejected by composite foreign key")
	}
}
```

- [ ] **Step 2: Run roadmap test and verify red**

Run:

```powershell
cd backend
$env:FLOWSPACE_TEST_DATABASE_URL = "postgres://flowspace:flowspace@127.0.0.1:15432/flowspace_test?sslmode=disable"
go test ./internal/repository -run TestRoadmapPostgresPreservesNodesEdgesResources -count=1
```

Expected: fail until SQL is PostgreSQL-compatible.

- [ ] **Step 3: Update roadmap SQL**

In `backend/internal/repository/roadmaps.go`:

- Convert placeholders.
- Store `position JSONB` using `jsonb_build_object('x', $n, 'y', $m)` with distinct placeholder indexes.
- Read `position->>'x'` and `position->>'y'` as floats.
- Store `article_search_queries TEXT[]` with `pq.Array` and scan it back into `RoadmapNode.ArticleSearchQueries`.
- Use `pgSetBuilder` for `UpdateRoadmapNode`.
- Rely on schema-level composite foreign keys for both `roadmap_edges` and `roadmap_nodes.parent_id`, so a node cannot reference a parent from another roadmap.

- [ ] **Step 4: Run roadmap tests and verify green**

Run:

```powershell
cd backend
$env:FLOWSPACE_TEST_DATABASE_URL = "postgres://flowspace:flowspace@127.0.0.1:15432/flowspace_test?sslmode=disable"
go test ./internal/repository ./internal/service -run "Roadmap|TestRoadmap" -count=1
```

Expected: pass.

- [ ] **Step 5: Commit**

```powershell
git add backend/internal/repository/roadmaps.go backend/internal/repository/roadmaps_postgres_test.go
git commit -m "feat: migrate roadmaps storage to postgres"
```

---

### Task 10: Migrate Sync Targets and Sync State

**Files:**
- Modify: `backend/internal/handler/sync.go`
- Modify: `backend/internal/repository/sync.go`
- Modify: sync tests

- [ ] **Step 1: Write sync JSONB test**

Create `backend/internal/repository/sync_postgres_test.go`:

```go
package repository

import (
	"fmt"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/model"
)

func TestSyncTargetPostgresStoresConfigAsJSONB(t *testing.T) {
	schema := fmt.Sprintf("fs_test_sync_%d", time.Now().UnixNano())
	databaseURL := createPostgresTestSchema(t, schema)

	store := openPostgresStoreForTest(t, databaseURL)
	defer store.Close()
	SetStore(store)
	t.Cleanup(func() { SetStore(nil) })
	truncatePostgresTables(t)

	target := &model.SyncTarget{
		Type:       "notion",
		Name:       "Personal Notion",
		ConfigJSON: `{"data_source_id":"ds-1","token_env":"FLOWSPACE_NOTION_TOKEN","required_tags":["sync"]}`,
		Enabled:    true,
	}
	if err := SaveSyncTarget(target); err != nil {
		t.Fatalf("save target: %v", err)
	}

	loaded, err := GetDefaultSyncTarget("notion")
	if err != nil {
		t.Fatalf("load target: %v", err)
	}
	if loaded.ConfigJSON == "" || loaded.ConfigJSON == "{}" {
		t.Fatalf("expected config json to round trip, got %q", loaded.ConfigJSON)
	}

	var tokenSaved bool
	if err := DB.QueryRow(`SELECT config::text LIKE '%secret_%' FROM sync_targets WHERE id = $1`, target.ID).Scan(&tokenSaved); err != nil {
		t.Fatalf("check token: %v", err)
	}
	if tokenSaved {
		t.Fatal("notion token must not be saved")
	}
}
```

Create `backend/internal/handler/sync_validation_test.go`:

```go
package handler

import (
	"errors"
	"net/http"
	"testing"

	"github.com/hujinrun/flowspace/internal/model"
)

func TestValidateSyncTargetRequestRejectsInvalidType(t *testing.T) {
	err := validateSyncTargetRequest(&model.SyncTarget{
		Type:       "dropbox",
		Name:       "Invalid Target",
		ConfigJSON: `{}`,
	})
	if err == nil {
		t.Fatal("expected invalid sync target type to be rejected")
	}
}

func TestValidateSyncTargetRequestRejectsNonObjectConfig(t *testing.T) {
	err := validateSyncTargetRequest(&model.SyncTarget{
		Type:       "notion",
		Name:       "Invalid Config",
		ConfigJSON: `[]`,
	})
	if err == nil {
		t.Fatal("expected non-object config JSON to be rejected")
	}
}

func TestSyncTargetSaveErrorStatusMapsConstraintErrors(t *testing.T) {
	status, _ := syncTargetSaveErrorStatus(errors.New(`ERROR: duplicate key value violates unique constraint "sync_targets_type_name_key"`))
	if status != http.StatusConflict {
		t.Fatalf("expected duplicate sync target to map to 409, got %d", status)
	}

	status, _ = syncTargetSaveErrorStatus(errors.New(`ERROR: new row for relation "sync_targets" violates check constraint "sync_targets_type_check"`))
	if status != http.StatusBadRequest {
		t.Fatalf("expected invalid sync target type constraint to map to 400, got %d", status)
	}
}
```

- [ ] **Step 2: Run sync test and verify red**

Run:

```powershell
cd backend
$env:FLOWSPACE_TEST_DATABASE_URL = "postgres://flowspace:flowspace@127.0.0.1:15432/flowspace_test?sslmode=disable"
go test ./internal/repository ./internal/handler -run "TestSyncTargetPostgresStoresConfigAsJSONB|TestValidateSyncTargetRequestRejectsInvalidType|TestValidateSyncTargetRequestRejectsNonObjectConfig|TestSyncTargetSaveErrorStatusMapsConstraintErrors" -count=1
```

Expected: fail until sync repository uses `config JSONB` and sync handler validates/maps target errors.

- [ ] **Step 3: Update sync SQL**

In `backend/internal/repository/sync.go`:

- Replace `config_json` column references with `config`.
- Pass config as `[]byte(target.ConfigJSON)` or `json.RawMessage`.
- Return `config::text` as `ConfigJSON`.
- Convert `enabled` and `auto_sync` to booleans.
- Convert `external_mtime` and `last_synced_at` to/from `TIMESTAMPTZ`.
- Keep model names unchanged for API compatibility.

In `backend/internal/handler/sync.go`:

- Extend `validateSyncTargetRequest` so `target.Type` must be `obsidian` or `notion`; invalid values return a 400 response before repository save.
- Validate `target.ConfigJSON` as a JSON object before repository save; empty config strings are normalized to `{}`, while `null`, arrays, strings, and numbers return 400.
- Add `syncTargetSaveErrorStatus(err error) (int, string)` and use it in the save handler. Unique violations for `(type,name)` must map to 409; CHECK violations for `sync_targets.type` must map to 400.

- [ ] **Step 4: Run sync, Obsidian, and Notion tests**

Run:

```powershell
cd backend
$env:FLOWSPACE_TEST_DATABASE_URL = "postgres://flowspace:flowspace@127.0.0.1:15432/flowspace_test?sslmode=disable"
go test ./internal/repository ./internal/service ./internal/handler -run "Sync|Notion|Obsidian" -count=1
```

Expected: pass.

- [ ] **Step 5: Commit**

```powershell
git add backend/internal/handler/sync.go backend/internal/handler/sync_validation_test.go backend/internal/repository/sync.go backend/internal/repository/sync_postgres_test.go backend/internal/repository/sync_test.go
git commit -m "feat: migrate sync storage to postgres"
```

---

### Task 11: Implement SQLite to PostgreSQL Migration Command

**Files:**
- Create: `backend/internal/migration/sqlite_to_pg.go`
- Create: `backend/internal/migration/sqlite_to_pg_test.go`
- Create: `backend/cmd/migrate_sqlite_to_pg/main.go`
- Modify: `backend/internal/repository/db.go`

- [ ] **Step 1: Write migration test**

Create `backend/internal/migration/sqlite_to_pg_test.go`:

```go
package migration

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/repository"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/lib/pq"
	_ "modernc.org/sqlite"
)

func TestSQLiteToPostgresMigratesAllTablesAndRebuildsSearchIndex(t *testing.T) {
	basePGURL := os.Getenv("FLOWSPACE_TEST_DATABASE_URL")
	if basePGURL == "" {
		t.Skip("FLOWSPACE_TEST_DATABASE_URL is required")
	}
	schema := fmt.Sprintf("fs_test_migration_%d", time.Now().UnixNano())
	pgURL := createMigrationPostgresSchema(t, basePGURL, schema)

	sqlitePath := filepath.Join(t.TempDir(), "flowspace.db")
	sqliteDB, err := sql.Open("sqlite", sqlitePath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqliteDB.Close()

	schemaSQL, err := os.ReadFile(filepath.Join("..", "..", "db", "schema.sql"))
	if err != nil {
		t.Fatalf("read sqlite schema: %v", err)
	}
	if _, err := sqliteDB.Exec(string(schemaSQL)); err != nil {
		t.Fatalf("create sqlite schema: %v", err)
	}
	if _, err := sqliteDB.Exec(`
		UPDATE folders SET name = 'Custom Personal Folder', sort_order = 42 WHERE id = '__personal';
		UPDATE task_projects SET name = 'Custom Personal Project', description = 'user renamed default project' WHERE id = 'personal';

		INSERT INTO folders (id, name, sort_order, created_at)
		VALUES ('folder-1', '迁移文件夹', 3, 1800000000);

		INSERT INTO notes (id, title, body, folder_id, tags, created_at, updated_at)
		VALUES ('note-1', '迁移笔记', '正文 中文', 'folder-1', '["sync","publish"]', 1800000000, 1800000100);

		INSERT INTO task_projects (id, name, type, description, created_at, updated_at)
		VALUES ('project-1', '迁移项目', 'learning', '迁移学习项目', 1800000000, 1800000100);

		INSERT INTO learning_roadmaps (id, project_id, title, goal, status, created_at, updated_at)
		VALUES ('roadmap-1', 'project-1', '迁移路线', '完成 PostgreSQL 迁移', 'active', 1800000000, 1800000100);

		INSERT INTO roadmap_nodes (
			id, roadmap_id, parent_id, type, title, description, path_type, status,
			deliverable, acceptance_criteria, x, y, order_index, created_at, updated_at
		)
		VALUES
			('node-2', 'roadmap-1', 'node-1', 'task', '实现迁移', '写脚本', 'required', 'todo', '迁移命令', '测试通过', 30.0, 40.0, 2, 1800000000, 1800000100),
			('node-1', 'roadmap-1', NULL, 'phase', '理解 schema', '读设计', 'required', 'active', 'DDL', '能解释表结构', 12.5, 20.5, 1, 1800000000, 1800000100);

		INSERT INTO tasks (
			id, title, content, project, project_id, due, planned_date, priority,
			done, status, horizon, scope, sort_order, note_id, roadmap_node_id,
			created_at, updated_at
		)
		VALUES (
			'task-1', '迁移任务', '任务内容 中文', '迁移项目', 'project-1', 1800003600,
			'2026-06-16', 2, 1, 'active', 'long', 'monthly', 1.5,
			'note-1', 'node-1', 1800000000, 1800000100
		);

		INSERT INTO roadmap_edges (id, roadmap_id, source_node_id, target_node_id, style, created_at)
		VALUES ('edge-1', 'roadmap-1', 'node-1', 'node-2', 'solid', 1800000000);

		INSERT INTO roadmap_resources (id, node_id, title, url, summary, source_type, added_by, created_at, updated_at)
		VALUES ('resource-1', 'node-1', 'PostgreSQL 文档', 'https://www.postgresql.org/docs/', '官方文档', 'article', 'user', 1800000000, 1800000100);

		INSERT INTO events (id, title, start_time, end_time, location, kind, note_id, created_at, updated_at)
		VALUES ('event-1', '迁移会议', 1800000000, 1800007200, '线上', 'work', 'note-1', 1800000000, 1800000100);

		INSERT INTO inbox (id, kind, title, body, source, archived, converted_to, created_at, updated_at)
		VALUES ('inbox-1', 'note', '收件箱条目', '待整理', 'quick-capture', 0, NULL, 1800000000, 1800000100);

		INSERT INTO sync_targets (id, type, name, vault_path, base_folder, config_json, enabled, auto_sync, created_at, updated_at)
		VALUES ('target-1', 'notion', 'Notion', '', '', '{"token_env":"FLOWSPACE_NOTION_TOKEN","required_tags":["sync"]}', 1, 0, 1800000000, 1800000100);

		INSERT INTO note_sync_state (
			note_id, target_id, external_path, external_id, external_url, content_hash,
			external_hash, external_mtime, last_direction, last_synced_at, status, error_message
		)
		VALUES (
			'note-1', 'target-1', 'notion/note-1', 'external-1', 'https://notion.so/external-1',
			'hash-local', 'hash-external', 1800000200, 'push', 1800000300, 'synced', NULL
		);
	`); err != nil {
		t.Fatalf("seed sqlite: %v", err)
	}

	pgDB, err := sql.Open("pgx", pgURL)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	defer pgDB.Close()

	if err := MigrateSQLiteToPostgres(sqlitePath, pgURL); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	assertMigrationTableCount(t, pgDB, "folders", 4)
	assertMigrationTableCount(t, pgDB, "notes", 1)
	assertMigrationTableCount(t, pgDB, "task_projects", 2)
	assertMigrationTableCount(t, pgDB, "learning_roadmaps", 1)
	assertMigrationTableCount(t, pgDB, "roadmap_nodes", 2)
	assertMigrationTableCount(t, pgDB, "tasks", 1)
	assertMigrationTableCount(t, pgDB, "roadmap_edges", 1)
	assertMigrationTableCount(t, pgDB, "roadmap_resources", 1)
	assertMigrationTableCount(t, pgDB, "events", 1)
	assertMigrationTableCount(t, pgDB, "inbox", 1)
	assertMigrationTableCount(t, pgDB, "sync_targets", 1)
	assertMigrationTableCount(t, pgDB, "note_sync_state", 1)
	assertMigrationTableCount(t, pgDB, "search_index", 3)

	var personalFolderName string
	var personalFolderSort float64
	if err := pgDB.QueryRow(`SELECT name, sort_order FROM folders WHERE id = '__personal'`).Scan(&personalFolderName, &personalFolderSort); err != nil {
		t.Fatalf("query overridden personal folder: %v", err)
	}
	if personalFolderName != "Custom Personal Folder" || personalFolderSort != 42 {
		t.Fatalf("expected SQLite default folder to override seed, got name=%q sort=%v", personalFolderName, personalFolderSort)
	}

	var personalProjectName string
	var personalProjectDescription string
	if err := pgDB.QueryRow(`SELECT name, description FROM task_projects WHERE id = 'personal'`).Scan(&personalProjectName, &personalProjectDescription); err != nil {
		t.Fatalf("query overridden personal project: %v", err)
	}
	if personalProjectName != "Custom Personal Project" || personalProjectDescription != "user renamed default project" {
		t.Fatalf("expected SQLite personal project to override seed, got name=%q description=%q", personalProjectName, personalProjectDescription)
	}

	var tags []string
	if err := pgDB.QueryRow(`SELECT tags FROM notes WHERE id = 'note-1'`).Scan(pq.Array(&tags)); err != nil {
		t.Fatalf("query tags: %v", err)
	}
	if len(tags) != 2 || tags[0] != "sync" || tags[1] != "publish" {
		t.Fatalf("unexpected tags: %#v", tags)
	}

	var tokenEnv string
	if err := pgDB.QueryRow(`SELECT config->>'token_env' FROM sync_targets WHERE id = 'target-1'`).Scan(&tokenEnv); err != nil {
		t.Fatalf("query config: %v", err)
	}
	if tokenEnv != "FLOWSPACE_NOTION_TOKEN" {
		t.Fatalf("unexpected token env: %q", tokenEnv)
	}

	var eventOverlaps bool
	if err := pgDB.QueryRow(`
		SELECT time_range && tstzrange(to_timestamp(1800000100), to_timestamp(1800000200), '[)')
		FROM events
		WHERE id = 'event-1'
	`).Scan(&eventOverlaps); err != nil {
		t.Fatalf("query event range: %v", err)
	}
	if !eventOverlaps {
		t.Fatal("expected migrated event time_range to overlap")
	}

	var childCount int
	if err := pgDB.QueryRow(`
		SELECT COUNT(*)
		FROM roadmap_nodes child
		JOIN roadmap_nodes parent ON parent.id = child.parent_id
		WHERE child.id = 'node-2' AND parent.id = 'node-1'
	`).Scan(&childCount); err != nil {
		t.Fatalf("query roadmap parent: %v", err)
	}
	if childCount != 1 {
		t.Fatalf("expected roadmap parent relation, got %d", childCount)
	}
}

func TestSQLiteToPostgresRollsBackWhenLateTableMigrationFails(t *testing.T) {
	basePGURL := os.Getenv("FLOWSPACE_TEST_DATABASE_URL")
	if basePGURL == "" {
		t.Skip("FLOWSPACE_TEST_DATABASE_URL is required")
	}
	schema := fmt.Sprintf("fs_test_migration_rollback_%d", time.Now().UnixNano())
	pgURL := createMigrationPostgresSchema(t, basePGURL, schema)

	sqlitePath := filepath.Join(t.TempDir(), "flowspace.db")
	sqliteDB, err := sql.Open("sqlite", sqlitePath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqliteDB.Close()

	schemaSQL, err := os.ReadFile(filepath.Join("..", "..", "db", "schema.sql"))
	if err != nil {
		t.Fatalf("read sqlite schema: %v", err)
	}
	if _, err := sqliteDB.Exec(string(schemaSQL)); err != nil {
		t.Fatalf("create sqlite schema: %v", err)
	}
	if _, err := sqliteDB.Exec(`
		INSERT INTO folders (id, name, sort_order, created_at)
		VALUES ('folder-rollback', '回滚文件夹', 3, 1800000000);

		INSERT INTO notes (id, title, body, folder_id, tags, created_at, updated_at)
		VALUES ('note-rollback', '应被回滚的笔记', '正文', 'folder-rollback', '["sync"]', 1800000000, 1800000100);

		INSERT INTO events (id, title, start_time, end_time, location, kind, note_id, created_at, updated_at)
		VALUES ('bad-event', '结束早于开始', 1800003600, 1800000000, '线上', 'work', 'note-rollback', 1800000000, 1800000100);
	`); err != nil {
		t.Fatalf("seed invalid sqlite data: %v", err)
	}

	pgDB, err := sql.Open("pgx", pgURL)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	defer pgDB.Close()

	if err := MigrateSQLiteToPostgres(sqlitePath, pgURL); err == nil {
		t.Fatal("expected migration to fail because event end_time is before start_time")
	}

	assertMigrationTableCount(t, pgDB, "notes", 0)
	assertMigrationTableCount(t, pgDB, "tasks", 0)
	assertMigrationTableCount(t, pgDB, "events", 0)
	assertMigrationTableCount(t, pgDB, "search_index", 0)
}

func TestSQLiteToPostgresValidatesSourceBeforeTouchingPostgres(t *testing.T) {
	basePGURL := os.Getenv("FLOWSPACE_TEST_DATABASE_URL")
	if basePGURL == "" {
		t.Skip("FLOWSPACE_TEST_DATABASE_URL is required")
	}
	schema := fmt.Sprintf("fs_test_migration_preflight_%d", time.Now().UnixNano())
	pgURL := createMigrationPostgresSchema(t, basePGURL, schema)

	sqlitePath := filepath.Join(t.TempDir(), "flowspace.db")
	sqliteDB, err := sql.Open("sqlite", sqlitePath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqliteDB.Close()

	schemaSQL, err := os.ReadFile(filepath.Join("..", "..", "db", "schema.sql"))
	if err != nil {
		t.Fatalf("read sqlite schema: %v", err)
	}
	if _, err := sqliteDB.Exec(string(schemaSQL)); err != nil {
		t.Fatalf("create sqlite schema: %v", err)
	}
	if _, err := sqliteDB.Exec(`
		INSERT INTO tasks (id, title, content, priority, done, status, horizon, scope, created_at, updated_at)
		VALUES ('bad-priority', 'bad priority task', 'invalid', -1, 0, 'open', 'week', 'daily', 1800000000, 1800000000)
	`); err != nil {
		t.Fatalf("seed invalid priority: %v", err)
	}

	err = MigrateSQLiteToPostgres(sqlitePath, pgURL)
	if err == nil {
		t.Fatal("expected migration to fail during SQLite source validation")
	}
	if !strings.Contains(err.Error(), "tasks.priority") {
		t.Fatalf("expected tasks.priority validation error, got %v", err)
	}

	pgDB, err := sql.Open("pgx", pgURL)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	defer pgDB.Close()
	var notesTableExists bool
	if err := pgDB.QueryRow(`
		SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = current_schema() AND table_name = 'notes'
		)
	`).Scan(&notesTableExists); err != nil {
		t.Fatalf("check postgres touched: %v", err)
	}
	if notesTableExists {
		t.Fatal("expected source validation to fail before PostgreSQL migrations create business tables")
	}
}

func TestSQLiteMigrationCopyRunsLegacyUpgradeWithoutMutatingSource(t *testing.T) {
	sqlitePath := filepath.Join(t.TempDir(), "legacy-flowspace.db")
	sqliteDB, err := sql.Open("sqlite", sqlitePath)
	if err != nil {
		t.Fatalf("open legacy sqlite: %v", err)
	}
	if _, err := sqliteDB.Exec(`
		CREATE TABLE tasks (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			project TEXT,
			done INTEGER NOT NULL DEFAULT 0,
			scope TEXT NOT NULL DEFAULT 'daily',
			sort_order REAL NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		);
		CREATE TABLE sync_targets (
			id TEXT PRIMARY KEY,
			type TEXT NOT NULL,
			name TEXT NOT NULL,
			vault_path TEXT NOT NULL DEFAULT '',
			base_folder TEXT NOT NULL DEFAULT '',
			enabled INTEGER NOT NULL DEFAULT 1,
			auto_sync INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		);
		CREATE TABLE note_sync_state (
			note_id TEXT NOT NULL,
			target_id TEXT NOT NULL,
			external_path TEXT NOT NULL,
			content_hash TEXT NOT NULL,
			last_synced_at INTEGER,
			status TEXT NOT NULL,
			error_message TEXT,
			PRIMARY KEY (note_id, target_id)
		);
	`); err != nil {
		t.Fatalf("create legacy sqlite schema: %v", err)
	}
	if err := sqliteDB.Close(); err != nil {
		t.Fatalf("close legacy sqlite: %v", err)
	}

	copyPath, cleanup, err := prepareSQLiteMigrationCopy(sqlitePath)
	if err != nil {
		t.Fatalf("prepare migration copy: %v", err)
	}
	defer cleanup()

	copyDB, err := sql.Open("sqlite", copyPath)
	if err != nil {
		t.Fatalf("open sqlite copy: %v", err)
	}
	defer copyDB.Close()
	if err := repository.RunLegacySQLiteMigrations(copyDB); err != nil {
		t.Fatalf("upgrade sqlite copy: %v", err)
	}

	if !sqliteColumnExists(t, copyDB, "tasks", "content") {
		t.Fatal("expected upgraded copy to have tasks.content")
	}

	sourceDB, err := sql.Open("sqlite", sqlitePath)
	if err != nil {
		t.Fatalf("reopen source sqlite: %v", err)
	}
	defer sourceDB.Close()
	if sqliteColumnExists(t, sourceDB, "tasks", "content") {
		t.Fatal("expected original SQLite source to remain unmodified")
	}
}

func sqliteColumnExists(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		t.Fatalf("pragma table_info %s: %v", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name string
		var columnType string
		var notNull int
		var defaultValue sql.NullString
		var primaryKey int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatalf("scan table_info %s: %v", table, err)
		}
		if name == column {
			return true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate table_info %s: %v", table, err)
	}
	return false
}

func TestValidateSQLiteForeignKeysCountsPragmaRows(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`
		PRAGMA foreign_keys = OFF;
		CREATE TABLE parent (id TEXT PRIMARY KEY);
		CREATE TABLE child (id TEXT PRIMARY KEY, parent_id TEXT REFERENCES parent(id));
		INSERT INTO child (id, parent_id) VALUES ('child-1', 'missing-parent');
	`); err != nil {
		t.Fatalf("seed foreign key violation: %v", err)
	}

	err = validateSQLiteForeignKeys(db)
	if err == nil || !strings.Contains(err.Error(), "foreign_key_check") {
		t.Fatalf("expected foreign key check violation, got %v", err)
	}
}

func TestValidateSQLiteSourceRejectsNonObjectConfigAndSuspiciousUnixSeconds(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`
		CREATE TABLE sync_targets (id TEXT PRIMARY KEY, config_json TEXT);
		INSERT INTO sync_targets (id, config_json) VALUES ('target-array', '[]');
	`); err != nil {
		t.Fatalf("seed sync config: %v", err)
	}
	if err := validateSQLiteJSONObjectColumn(db, "sync_targets", "id", "config_json"); err == nil {
		t.Fatal("expected JSON array config to be rejected")
	}

	if _, err := db.Exec(`
		CREATE TABLE events (id TEXT PRIMARY KEY, start_time INTEGER);
		INSERT INTO events (id, start_time) VALUES ('event-ms', 1800000000000);
	`); err != nil {
		t.Fatalf("seed millisecond timestamp: %v", err)
	}
	if err := validateSQLiteUnixSeconds(db, "events", "id", "start_time"); err == nil {
		t.Fatal("expected millisecond timestamp to be rejected")
	}
}

func createMigrationPostgresSchema(t *testing.T, baseURL, schema string) string {
	t.Helper()

	adminDB, err := sql.Open("pgx", baseURL)
	if err != nil {
		t.Fatalf("open postgres admin connection: %v", err)
	}

	ctx := context.Background()
	quotedSchema := quoteMigrationIdentifier(schema)
	if _, err := adminDB.ExecContext(ctx, `DROP SCHEMA IF EXISTS `+quotedSchema+` CASCADE`); err != nil {
		t.Fatalf("drop test schema: %v", err)
	}
	if _, err := adminDB.ExecContext(ctx, `CREATE SCHEMA `+quotedSchema); err != nil {
		t.Fatalf("create test schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = adminDB.ExecContext(context.Background(), `DROP SCHEMA IF EXISTS `+quotedSchema+` CASCADE`)
		_ = adminDB.Close()
	})

	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("parse postgres URL: %v", err)
	}
	query := parsed.Query()
	query.Set("options", "-c search_path="+schema+",public")
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func quoteMigrationIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func assertMigrationTableCount(t *testing.T, db *sql.DB, table string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + quoteMigrationIdentifier(table)).Scan(&got); err != nil {
		t.Fatalf("count table %s: %v", table, err)
	}
	if got != want {
		t.Fatalf("expected %s count %d, got %d", table, want, got)
	}
}
```

- [ ] **Step 2: Run migration test and verify red**

Run:

```powershell
cd backend
$env:FLOWSPACE_TEST_DATABASE_URL = "postgres://flowspace:flowspace@127.0.0.1:15432/flowspace_test?sslmode=disable"
go test ./internal/migration -run "TestSQLiteToPostgresMigratesAllTablesAndRebuildsSearchIndex|TestSQLiteToPostgresRollsBackWhenLateTableMigrationFails|TestSQLiteToPostgresValidatesSourceBeforeTouchingPostgres|TestSQLiteMigrationCopyRunsLegacyUpgradeWithoutMutatingSource|TestValidateSQLiteForeignKeysCountsPragmaRows|TestValidateSQLiteSourceRejectsNonObjectConfigAndSuspiciousUnixSeconds" -count=1
```

Expected: fail with `undefined: MigrateSQLiteToPostgres`.

- [ ] **Step 3: Implement migration function**

Create `backend/internal/migration/sqlite_to_pg.go` with:

```go
package migration

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/repository"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

type migrationExecutor interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
	Query(query string, args ...interface{}) (*sql.Rows, error)
	QueryRow(query string, args ...interface{}) *sql.Row
}

func MigrateSQLiteToPostgres(sqlitePath, postgresURL string) error {
	workingSQLitePath, cleanup, err := prepareSQLiteMigrationCopy(sqlitePath)
	if err != nil {
		return err
	}
	defer cleanup()

	sqliteDB, err := sql.Open("sqlite", workingSQLitePath)
	if err != nil {
		return err
	}
	defer sqliteDB.Close()

	if err := repository.RunLegacySQLiteMigrations(sqliteDB); err != nil {
		return fmt.Errorf("upgrade SQLite copy before migration: %w", err)
	}

	if err := validateSQLiteSource(sqliteDB); err != nil {
		return err
	}

	pgDB, err := sql.Open("pgx", postgresURL)
	if err != nil {
		return err
	}
	defer pgDB.Close()

	if err := repository.RunPostgresMigrations(pgDB); err != nil {
		return err
	}
	if err := ensurePostgresMigrationTargetEmpty(pgDB); err != nil {
		return err
	}

	tx, err := pgDB.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := migrateFolders(sqliteDB, tx); err != nil {
		return err
	}
	if err := migrateNotes(sqliteDB, tx); err != nil {
		return err
	}
	if err := migrateSyncTargets(sqliteDB, tx); err != nil {
		return err
	}
	if err := migrateTaskProjects(sqliteDB, tx); err != nil {
		return err
	}
	if err := migrateLearningRoadmaps(sqliteDB, tx); err != nil {
		return err
	}
	if err := migrateRoadmapNodes(sqliteDB, tx); err != nil {
		return err
	}
	if err := migrateTasks(sqliteDB, tx); err != nil {
		return err
	}
	if err := migrateRoadmapEdges(sqliteDB, tx); err != nil {
		return err
	}
	if err := migrateRoadmapResources(sqliteDB, tx); err != nil {
		return err
	}
	if err := migrateEvents(sqliteDB, tx); err != nil {
		return err
	}
	if err := migrateInbox(sqliteDB, tx); err != nil {
		return err
	}
	if err := migrateNoteSyncState(sqliteDB, tx); err != nil {
		return err
	}
	if err := rebuildSearchIndex(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}
```

Add this helper in the same file:

```go
func prepareSQLiteMigrationCopy(sourcePath string) (string, func(), error) {
	tempDir, err := os.MkdirTemp("", "flowspace-sqlite-migration-*")
	if err != nil {
		return "", func() {}, fmt.Errorf("create migration temp dir: %w", err)
	}
	cleanup := func() {
		_ = os.RemoveAll(tempDir)
	}

	targetPath := filepath.Join(tempDir, filepath.Base(sourcePath))
	sourceDB, err := sql.Open("sqlite", sourcePath)
	if err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("open SQLite source for backup: %w", err)
	}
	defer sourceDB.Close()

	if _, err := sourceDB.Exec(`VACUUM INTO ` + quoteSQLiteString(targetPath)); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("create WAL-safe SQLite migration copy with VACUUM INTO: %w", err)
	}
	return targetPath, cleanup, nil
}

func quoteSQLiteString(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func validateSQLiteSource(db *sql.DB) error {
	if err := validateSQLiteForeignKeys(db); err != nil {
		return err
	}

	checks := []struct {
		name  string
		query string
	}{
		{"tasks.priority", "SELECT COUNT(*) FROM tasks WHERE priority < 0"},
		{"tasks.status", "SELECT COUNT(*) FROM tasks WHERE status IS NOT NULL AND status NOT IN ('open','active','blocked','done','archived','migrated','cancelled')"},
		{"tasks.horizon", "SELECT COUNT(*) FROM tasks WHERE horizon IS NOT NULL AND horizon NOT IN ('day','week','long')"},
		{"tasks.scope", "SELECT COUNT(*) FROM tasks WHERE scope IS NOT NULL AND scope NOT IN ('daily','weekly','monthly','yearly')"},
		{"tasks.done", "SELECT COUNT(*) FROM tasks WHERE done NOT IN (0,1)"},
		{"task_projects.type", "SELECT COUNT(*) FROM task_projects WHERE type NOT IN ('personal','regular','learning')"},
		{"learning_roadmaps.status", "SELECT COUNT(*) FROM learning_roadmaps WHERE status NOT IN ('draft','ready','active','done','archived','failed')"},
		{"roadmap_nodes.type", "SELECT COUNT(*) FROM roadmap_nodes WHERE type NOT IN ('phase','module','choice','task')"},
		{"roadmap_nodes.path_type", "SELECT COUNT(*) FROM roadmap_nodes WHERE path_type NOT IN ('required','recommended','optional','alternative')"},
		{"roadmap_nodes.status", "SELECT COUNT(*) FROM roadmap_nodes WHERE status NOT IN ('todo','active','done','skipped')"},
		{"events.time_range", "SELECT COUNT(*) FROM events WHERE end_time <= start_time"},
		{"inbox.archived", "SELECT COUNT(*) FROM inbox WHERE archived NOT IN (0,1)"},
		{"sync_targets.type", "SELECT COUNT(*) FROM sync_targets WHERE type NOT IN ('obsidian','notion')"},
		{"sync_targets.enabled", "SELECT COUNT(*) FROM sync_targets WHERE enabled NOT IN (0,1)"},
		{"sync_targets.auto_sync", "SELECT COUNT(*) FROM sync_targets WHERE auto_sync NOT IN (0,1)"},
		{"sync_targets(type,name)", "SELECT COUNT(*) FROM (SELECT type, name FROM sync_targets GROUP BY type, name HAVING COUNT(*) > 1)"},
		{"note_sync_state.status", "SELECT COUNT(*) FROM note_sync_state WHERE status NOT IN ('synced','pending','failed','external_deleted')"},
		{"note_sync_state.last_direction", "SELECT COUNT(*) FROM note_sync_state WHERE last_direction IS NOT NULL AND last_direction NOT IN ('push','pull','import','restore','delete')"},
	}
	for _, check := range checks {
		var count int
		if err := db.QueryRow(check.query).Scan(&count); err != nil {
			return fmt.Errorf("validate SQLite source %s: %w", check.name, err)
		}
		if count != 0 {
			return fmt.Errorf("invalid SQLite source: %s has %d invalid row(s)", check.name, count)
		}
	}

	if err := validateSQLiteDateColumn(db, "tasks", "id", "planned_date"); err != nil {
		return err
	}
	if err := validateSQLiteStringArrayJSONColumn(db, "notes", "id", "tags"); err != nil {
		return err
	}
	if err := validateSQLiteJSONObjectColumn(db, "sync_targets", "id", "config_json"); err != nil {
		return err
	}
	for _, check := range []struct {
		table   string
		idCol   string
		columns []string
	}{
		{"folders", "id", []string{"created_at"}},
		{"notes", "id", []string{"created_at", "updated_at"}},
		{"task_projects", "id", []string{"created_at", "updated_at"}},
		{"tasks", "id", []string{"due", "created_at", "updated_at"}},
		{"learning_roadmaps", "id", []string{"created_at", "updated_at"}},
		{"roadmap_nodes", "id", []string{"created_at", "updated_at"}},
		{"roadmap_edges", "id", []string{"created_at"}},
		{"roadmap_resources", "id", []string{"created_at", "updated_at"}},
		{"events", "id", []string{"start_time", "end_time", "created_at", "updated_at"}},
		{"inbox", "id", []string{"created_at", "updated_at"}},
		{"sync_targets", "id", []string{"created_at", "updated_at"}},
		{"note_sync_state", "note_id", []string{"external_mtime", "last_synced_at"}},
	} {
		if err := validateSQLiteUnixSeconds(db, check.table, check.idCol, check.columns...); err != nil {
			return err
		}
	}
	return nil
}

func validateSQLiteForeignKeys(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		return fmt.Errorf("run PRAGMA foreign_key_check: %w", err)
	}
	defer rows.Close()

	violations := 0
	for rows.Next() {
		violations++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate PRAGMA foreign_key_check: %w", err)
	}
	if violations != 0 {
		return fmt.Errorf("invalid SQLite source: PRAGMA foreign_key_check returned %d violation(s)", violations)
	}
	return nil
}

func validateSQLiteDateColumn(db *sql.DB, table, idColumn, dateColumn string) error {
	rows, err := db.Query(fmt.Sprintf("SELECT %s, %s FROM %s WHERE %s IS NOT NULL AND TRIM(%s) <> ''", idColumn, dateColumn, table, dateColumn, dateColumn))
	if err != nil {
		return fmt.Errorf("query %s.%s: %w", table, dateColumn, err)
	}
	defer rows.Close()

	for rows.Next() {
		var id string
		var raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return fmt.Errorf("scan %s.%s: %w", table, dateColumn, err)
		}
		if _, err := time.Parse("2006-01-02", raw); err != nil {
			return fmt.Errorf("invalid SQLite source: %s.%s contains invalid date %q for id %s", table, dateColumn, raw, id)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate %s.%s: %w", table, dateColumn, err)
	}
	return nil
}

func validateSQLiteStringArrayJSONColumn(db *sql.DB, table, idColumn, jsonColumn string) error {
	rows, err := db.Query(fmt.Sprintf("SELECT %s, %s FROM %s WHERE %s IS NOT NULL AND TRIM(%s) <> ''", idColumn, jsonColumn, table, jsonColumn, jsonColumn))
	if err != nil {
		return fmt.Errorf("query %s.%s: %w", table, jsonColumn, err)
	}
	defer rows.Close()

	for rows.Next() {
		var id string
		var raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return fmt.Errorf("scan %s.%s: %w", table, jsonColumn, err)
		}
		var values []string
		if err := json.Unmarshal([]byte(raw), &values); err != nil {
			return fmt.Errorf("invalid SQLite source: %s.%s must be a JSON string array for id %s: %w", table, jsonColumn, id, err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate %s.%s: %w", table, jsonColumn, err)
	}
	return nil
}

func validateSQLiteUnixSeconds(db *sql.DB, table, idColumn string, columns ...string) error {
	const minUnix = int64(946684800)  // 2000-01-01
	const maxUnix = int64(4102444800) // 2100-01-01
	for _, column := range columns {
		rows, err := db.Query(fmt.Sprintf("SELECT %s, %s FROM %s WHERE %s IS NOT NULL", idColumn, column, table, column))
		if err != nil {
			return fmt.Errorf("query %s.%s: %w", table, column, err)
		}
		for rows.Next() {
			var id string
			var value int64
			if err := rows.Scan(&id, &value); err != nil {
				_ = rows.Close()
				return fmt.Errorf("scan %s.%s: %w", table, column, err)
			}
			if value < 0 || value > maxUnix || value > 100000000000 {
				_ = rows.Close()
				return fmt.Errorf("invalid SQLite source: %s.%s has suspicious Unix seconds %d for id %s", table, column, value, id)
			}
			if value != 0 && value < minUnix {
				_ = rows.Close()
				return fmt.Errorf("invalid SQLite source: %s.%s is before 2000-01-01 for id %s: %d", table, column, id, value)
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return fmt.Errorf("iterate %s.%s: %w", table, column, err)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("close %s.%s rows: %w", table, column, err)
		}
	}
	return nil
}

func validateSQLiteJSONObjectColumn(db *sql.DB, table, idColumn, jsonColumn string) error {
	rows, err := db.Query(fmt.Sprintf("SELECT %s, %s FROM %s WHERE %s IS NOT NULL AND TRIM(%s) <> ''", idColumn, jsonColumn, table, jsonColumn, jsonColumn))
	if err != nil {
		return fmt.Errorf("query %s.%s: %w", table, jsonColumn, err)
	}
	defer rows.Close()

	for rows.Next() {
		var id string
		var raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return fmt.Errorf("scan %s.%s: %w", table, jsonColumn, err)
		}
		var value map[string]any
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			return fmt.Errorf("invalid SQLite source: %s.%s must be a JSON object for id %s: %w", table, jsonColumn, id, err)
		}
		if value == nil {
			return fmt.Errorf("invalid SQLite source: %s.%s must be a JSON object for id %s, got null", table, jsonColumn, id)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate %s.%s: %w", table, jsonColumn, err)
	}
	return nil
}

func normalizeSQLiteJSONObject(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return "{}"
	}
	return raw
}

func ensurePostgresMigrationTargetEmpty(db *sql.DB) error {
	checks := []struct {
		name  string
		query string
	}{
		{"folders", "SELECT COUNT(*) FROM folders WHERE id NOT IN ('__uncategorized', '__work', '__personal')"},
		{"notes", "SELECT COUNT(*) FROM notes"},
		{"task_projects", "SELECT COUNT(*) FROM task_projects WHERE id <> 'personal'"},
		{"learning_roadmaps", "SELECT COUNT(*) FROM learning_roadmaps"},
		{"roadmap_nodes", "SELECT COUNT(*) FROM roadmap_nodes"},
		{"roadmap_edges", "SELECT COUNT(*) FROM roadmap_edges"},
		{"roadmap_resources", "SELECT COUNT(*) FROM roadmap_resources"},
		{"tasks", "SELECT COUNT(*) FROM tasks"},
		{"events", "SELECT COUNT(*) FROM events"},
		{"inbox", "SELECT COUNT(*) FROM inbox"},
		{"sync_targets", "SELECT COUNT(*) FROM sync_targets"},
		{"note_sync_state", "SELECT COUNT(*) FROM note_sync_state"},
		{"search_index", "SELECT COUNT(*) FROM search_index"},
	}
	for _, check := range checks {
		var count int
		if err := db.QueryRow(check.query).Scan(&count); err != nil {
			return fmt.Errorf("check target table %s: %w", check.name, err)
		}
		if count != 0 {
			return fmt.Errorf("target table %s is not empty", check.name)
		}
	}
	return nil
}
```

Then implement every migration helper named above completely. The helpers must:

- Extract the current SQLite upgrade statements from `backend/internal/repository/db.go:migrateDB` into `RunLegacySQLiteMigrations(db *sql.DB) error`. Existing SQLite startup should call the new helper with package-level `DB`; the PostgreSQL migration command should call it on the temporary SQLite copy before `validateSQLiteSource`.
- Accept `migrationExecutor` for PostgreSQL writes so they can run inside the transaction.
- Create the temporary SQLite working copy with `VACUUM INTO`, then call `RunLegacySQLiteMigrations` on that copy; never mutate the user-provided SQLite file and never use raw `io.Copy` for WAL-mode databases. Production migration still requires write freeze/service stop before this step.
- Call `validateSQLiteSource` after the SQLite copy has been upgraded and before opening or migrating PostgreSQL, so malformed SQLite data fails before PostgreSQL business tables are created.
- Call `ensurePostgresMigrationTargetEmpty` before starting the data transaction. It must allow only migration seed rows (`folders.__uncategorized`, `folders.__work`, `folders.__personal`, `task_projects.personal`) and reject any existing business rows in `notes`, `tasks`, `events`, `inbox`, `learning_roadmaps`, `roadmap_nodes`, `roadmap_edges`, `roadmap_resources`, `sync_targets`, `note_sync_state`, and `search_index`.
- Use source-over-seed conflict handling for default rows: `migrateFolders` and `migrateTaskProjects` must use `ON CONFLICT (id) DO UPDATE` so SQLite values override migration seed values for name, sort order, type, and description.
- Convert Unix seconds to `time.Unix(value, 0).UTC()`.
- Convert SQLite `done`, `enabled`, `auto_sync`, and `archived` integers to PostgreSQL booleans.
- Parse note tag JSON strings into `[]string`.
- Insert `TEXT[]` values with `pq.Array(tags)` and explicit `::text[]` casts.
- Validate `notes.tags` as a JSON string array and `sync_targets.config_json` as a JSON object before inserting as `TEXT[]` or `JSONB`; normalize empty config strings to `{}`.
- Map roadmap node `x`/`y` into `position JSONB`, for example `{"x":12.5,"y":20.5}`.
- Let new PostgreSQL-only fields use schema defaults: `inbox.payload`, `roadmap_nodes.article_search_queries`, `roadmap_resources.metadata`, and `note_sync_state.external_metadata`.
- Rebuild `search_index` from migrated `notes`, `tasks`, and `events` after all source rows are migrated.

- [ ] **Step 4: Add command entrypoint**

Create `backend/cmd/migrate_sqlite_to_pg/main.go`:

```go
package main

import (
	"log"
	"os"

	"github.com/hujinrun/flowspace/internal/migration"
)

func main() {
	sqlitePath := os.Getenv("SQLITE_DB_PATH")
	postgresURL := os.Getenv("FLOWSPACE_DATABASE_URL")
	if sqlitePath == "" {
		log.Fatal("SQLITE_DB_PATH is required")
	}
	if postgresURL == "" {
		log.Fatal("FLOWSPACE_DATABASE_URL is required")
	}
	if err := migration.MigrateSQLiteToPostgres(sqlitePath, postgresURL); err != nil {
		log.Fatalf("migration failed: %v", err)
	}
	log.Printf("migration completed sqlite=%s", sqlitePath)
}
```

- [ ] **Step 5: Run migration test and verify green**

Run:

```powershell
cd backend
$env:FLOWSPACE_TEST_DATABASE_URL = "postgres://flowspace:flowspace@127.0.0.1:15432/flowspace_test?sslmode=disable"
go test ./internal/migration -run "TestSQLiteToPostgresMigratesAllTablesAndRebuildsSearchIndex|TestSQLiteToPostgresRollsBackWhenLateTableMigrationFails|TestSQLiteToPostgresValidatesSourceBeforeTouchingPostgres|TestSQLiteMigrationCopyRunsLegacyUpgradeWithoutMutatingSource|TestValidateSQLiteForeignKeysCountsPragmaRows|TestValidateSQLiteSourceRejectsNonObjectConfigAndSuspiciousUnixSeconds" -count=1
```

Expected: pass.

- [ ] **Step 6: Commit**

```powershell
git add backend/internal/migration backend/cmd/migrate_sqlite_to_pg
git commit -m "feat: add sqlite to postgres migration command"
```

---

### Task 12: Update Startup Scripts and Documentation

**Files:**
- Modify: `scripts/start-flowspace.mjs`
- Modify: `Makefile`
- Modify: `backend/Makefile`
- Modify: `README.md`
- Modify: `docs/service-ports.md`

- [ ] **Step 1: Write script config test if existing script tests are present**

If there are existing Node tests for `scripts/start-flowspace.mjs`, add a test that verifies:

```text
--env test sets FLOWSPACE_ENV=test and does not set FLOWSPACE_DB_PATH
--env prod sets FLOWSPACE_ENV=prod and does not set FLOWSPACE_DB_PATH
FLOWSPACE_DATABASE_DRIVER is passed through unchanged
FLOWSPACE_DATABASE_URL is passed through unchanged
FLOWSPACE_SQLITE_PATH is passed through unchanged
```

If no script test harness exists, skip this step and validate by command output in Step 4.

- [ ] **Step 2: Update startup script**

Modify `scripts/start-flowspace.mjs`:

- Accept `--database-driver` with `postgres` and `sqlite`.
- Accept `--database-url`.
- Accept `--sqlite-path`.
- Pass `FLOWSPACE_DATABASE_DRIVER`, `FLOWSPACE_DATABASE_URL`, and `FLOWSPACE_SQLITE_PATH` to backend process.
- Keep old `--db` as an alias for `--sqlite-path` plus `--database-driver sqlite`, and print a compatibility warning.

Concrete environment merge:

```js
const backendEnv = {
  ...process.env,
  FLOWSPACE_ENV: selectedEnv,
  FLOWSPACE_DATABASE_DRIVER: options.databaseDriver || process.env.FLOWSPACE_DATABASE_DRIVER || 'postgres',
  FLOWSPACE_DATABASE_URL: options.databaseUrl || process.env.FLOWSPACE_DATABASE_URL || '',
  FLOWSPACE_SQLITE_PATH: options.sqlitePath || process.env.FLOWSPACE_SQLITE_PATH || '',
  PORT: backendPort,
}
```

- [ ] **Step 3: Update Makefiles**

Update root `Makefile` and `backend/Makefile`:

```makefile
POSTGRES_TEST_URL ?= postgres://flowspace:flowspace@127.0.0.1:15432/flowspace_test?sslmode=disable&application_name=flowspace-test&options=-c%20statement_timeout=15000
POSTGRES_PROD_URL ?= postgres://flowspace:flowspace@127.0.0.1:15432/flowspace_prod?sslmode=disable&application_name=flowspace-prod&options=-c%20statement_timeout=15000

dev-test:
	FLOWSPACE_ENV=test FLOWSPACE_DATABASE_DRIVER=postgres FLOWSPACE_DATABASE_URL="$(POSTGRES_TEST_URL)" node scripts/start-flowspace.mjs --env test

dev-prod:
	FLOWSPACE_ENV=prod FLOWSPACE_DATABASE_DRIVER=postgres FLOWSPACE_DATABASE_URL="$(POSTGRES_PROD_URL)" node scripts/start-flowspace.mjs --env prod

dev-test-sqlite:
	FLOWSPACE_ENV=test FLOWSPACE_DATABASE_DRIVER=sqlite FLOWSPACE_SQLITE_PATH="backend/flowspace.test.db" node scripts/start-flowspace.mjs --env test
```

- [ ] **Step 4: Validate startup command**

Run:

```powershell
$env:FLOWSPACE_DATABASE_URL = "postgres://flowspace:flowspace@127.0.0.1:15432/flowspace_test?sslmode=disable&application_name=flowspace-test&options=-c%20statement_timeout=15000"
$env:FLOWSPACE_DATABASE_DRIVER = "postgres"
node .\scripts\start-flowspace.mjs --env test
```

Expected: backend logs `storage initialized env=test driver=postgres database=flowspace_test` and listens on `4101`.

- [ ] **Step 5: Update docs**

Update `README.md` and `docs/service-ports.md`:

- Replace SQLite rows with PostgreSQL database names.
- Document `FLOWSPACE_DATABASE_DRIVER=postgres|sqlite`.
- Document `FLOWSPACE_SQLITE_PATH` for the SQLite compatibility provider.
- Add Docker Compose startup.
- Document that Docker init scripts only run for empty volumes, and include both `docker compose down -v` and manual `psql -f /docker-entrypoint-initdb.d/010-flowspace.sql` recovery commands.
- Add migration command examples.
- State that test service uses `flowspace_test`; prod service uses `flowspace_prod`.
- Keep a backup note for old SQLite files.

- [ ] **Step 6: Commit**

```powershell
git add scripts/start-flowspace.mjs Makefile backend/Makefile README.md docs/service-ports.md
git commit -m "docs: document postgres storage runtime"
```

---

### Task 13: End-to-End Verification

**Files:**
- No source files unless failures are found.

- [ ] **Step 1: Start PostgreSQL**

Run:

```powershell
docker compose -f docker-compose.postgres.yml up -d
```

Expected: PostgreSQL healthy.

- [ ] **Step 2: Run backend tests**

Run:

```powershell
cd backend
$env:FLOWSPACE_TEST_DATABASE_URL = "postgres://flowspace:flowspace@127.0.0.1:15432/flowspace_test?sslmode=disable"
go test ./...
```

Expected: all backend tests pass or SQLite-only legacy tests have been removed/updated.

- [ ] **Step 3: Run frontend tests**

Run:

```powershell
cd frontend
npm test -- --run
npm run build
```

Expected: frontend tests and build pass unchanged.

- [ ] **Step 4: Start test service**

Run:

```powershell
$env:FLOWSPACE_DATABASE_URL = "postgres://flowspace:flowspace@127.0.0.1:15432/flowspace_test?sslmode=disable&application_name=flowspace-test&options=-c%20statement_timeout=15000"
make dev-test
```

Expected:

- Frontend: `http://127.0.0.1:4100`
- Backend: `http://127.0.0.1:4101/api`
- Storage: PostgreSQL `flowspace_test`

- [ ] **Step 5: Browser smoke test**

Use the in-app browser:

1. Open `http://127.0.0.1:4100/notes`.
2. Create a note with tag `sync`.
3. Search for the note title.
4. Create a task for today.
5. Open tasks page and verify the task appears.
6. Open sync panel and verify Notion/Obsidian settings still render.

Expected: no console errors, API requests succeed, data persists after backend restart.

- [ ] **Step 6: Commit final fixes**

If the smoke test required fixes:

```powershell
git add <changed-files>
git commit -m "fix: stabilize postgres storage smoke flow"
```

If no fixes were required, do not create an empty commit.

---

## Self-Review

Spec coverage:

- Storage provider config is covered by Tasks 1, 4, and 12.
- Provider registry, `storage.Store`, repository facade, SQLite provider, PostgreSQL provider, and provider contract tests are covered by Tasks 1A, 1B, 1C, 4, and 5A.
- PostgreSQL schema and different data structures are covered by Task 3.
- Tags as `TEXT[]`, configs as `JSONB`, event ranges as `TSTZRANGE`, and search as `TSVECTOR` are covered by Tasks 3, 6, 8, and 10.
- Idempotent schema migrations are covered by Task 3 with `schema_migrations`, checksum validation, and two-run tests.
- SQLite-to-PostgreSQL migration is covered by Task 11 across folders, notes, task projects, tasks, events, inbox, roadmaps, sync targets, sync state, and `search_index`.
- SQLite-to-PostgreSQL migration atomicity is covered by Task 11 with `TestSQLiteToPostgresRollsBackWhenLateTableMigrationFails`, `ensurePostgresMigrationTargetEmpty`, and one data transaction.
- Default seed rows for `folders.__uncategorized`, `folders.__work`, `folders.__personal`, and `task_projects.personal` are required in Task 3 migration SQL and verified by Task 3 tests.
- Current task status values `migrated` and `cancelled` are covered by Task 7.
- Current roadmap status values `ready` and `failed` are covered by Task 9.
- `roadmap_nodes.article_search_queries` round-trip and same-roadmap edge constraints are covered by Task 9.
- PostgreSQL search behavior is covered by Task 6 with `SearchPostgres` tests for highlight, CJK fallback, and note/task/event source joins.
- Search duplicate prevention and pagination totals are covered by Task 6 with deduplicated `DISTINCT ON` CTEs and `COUNT(*) FROM deduped`.
- Dynamic SQL placeholder safety is covered by Task 5 helpers plus Task 6/7/8 tests for `UpdateNote`, `UpdateTask`, and inbox batch operations.
- SQLite-specific SQL replacements are captured in the PostgreSQL Provider SQL Migration Checklist.
- PostgreSQL extension visibility with per-test schemas is covered by Task 3 using `CREATE EXTENSION ... WITH SCHEMA public` and `search_path=<schema>,public`.
- Environment safety is covered by Task 1 validation tests that reject test/prod PostgreSQL URL mismatches and SQLite path mismatches.
- Test/prod environment separation is covered by Tasks 1, 2, 12, and 13; local Docker initialization creates both `flowspace_test` and `flowspace_prod`.
- Existing Docker volume behavior is documented in Tasks 2 and 12.
- PostgreSQL integration tests use per-test schemas and do not drop the shared `public` schema.
- TDD red-green sequence is included in every implementation task.

Placeholder scan:

- Implementation SQL in Task 3 references the design document and explicitly requires complete SQL in the migration file.
- No task asks the engineer to add vague error handling without test coverage.

Type consistency:

- `FLOWSPACE_DATABASE_DRIVER`, `FLOWSPACE_DATABASE_URL`, `FLOWSPACE_SQLITE_PATH`, `storage.Config`, `storage.Store`, provider registry, `postgres.Provider`, `sqlite.Provider`, `RunPostgresMigrations`, `tagsJSONStringToArray`, and `tagsArrayToJSONString` are introduced before later tasks use them.
- API compatibility keeps current model names in phase 1.
