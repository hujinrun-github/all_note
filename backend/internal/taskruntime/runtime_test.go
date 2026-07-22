package taskruntime

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/airuntime"
	"github.com/hujinrun/flowspace/internal/storage"
	storagepostgres "github.com/hujinrun/flowspace/internal/storage/postgres"
	storagesqlite "github.com/hujinrun/flowspace/internal/storage/sqlite"
	"github.com/hujinrun/flowspace/internal/taskapp"
	"github.com/hujinrun/flowspace/internal/taskdomain"
	"github.com/hujinrun/flowspace/internal/tenantruntime"
	_ "modernc.org/sqlite"
)

func TestPostgresRuntimeProviderContract(t *testing.T) {
	rawURL := strings.TrimSpace(os.Getenv("FLOWSPACE_TEST_DATABASE_URL"))
	if rawURL == "" {
		if strings.EqualFold(strings.TrimSpace(os.Getenv("FLOWSPACE_REQUIRE_POSTGRES_TESTS")), "true") {
			t.Fatal("FLOWSPACE_TEST_DATABASE_URL is required")
		}
		t.Skip("FLOWSPACE_TEST_DATABASE_URL is not configured")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() == "" || parsed.User == nil {
		t.Fatal("invalid PostgreSQL test URL")
	}
	password, _ := parsed.User.Password()
	username := parsed.User.Username()
	parsed.User = url.User(username)
	query := parsed.Query()
	query.Del("search_path")
	query.Del("options")
	parsed.RawQuery = query.Encode()
	endpointURL := parsed.String()
	schema := fmt.Sprintf("fs_taskruntime_%d", time.Now().UnixNano())

	admin, err := sql.Open("pgx", rawURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if _, err := admin.Exec(`CREATE SCHEMA "` + schema + `"`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(`DROP SCHEMA IF EXISTS "` + schema + `" CASCADE`) })

	configJSON := fmt.Sprintf(`{"endpoint":%q,"schema":%q}`, endpointURL, schema)
	cfg, err := postgresStorageConfig("test", configJSON, []byte(password))
	if err != nil {
		t.Fatal(err)
	}
	provider := storagepostgres.Provider{}
	if err := provider.MigrateTenant(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	tenantDB, err := sql.Open("pgx", cfg.URL)
	if err != nil {
		t.Fatal(err)
	}
	workspaceID := "postgres-runtime"
	if _, err := tenantDB.Exec(`INSERT INTO tenant_workspaces(workspace_id,epoch,state) VALUES($1,1,'active')`, workspaceID); err != nil {
		t.Fatal(err)
	}
	_ = tenantDB.Close()

	registry := storage.NewRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatal(err)
	}
	endpoints := &fakeDatabaseEndpoints{config: DatabaseEndpointConfig{
		WorkspaceID: workspaceID, EndpointID: "database", ProfileVersionID: "version-one", Storage: cfg,
	}}
	factory, err := NewFactory(endpoints, ExpectedTenantSchemaVersion, (&net.Dialer{}).DialContext)
	if err != nil {
		t.Fatal(err)
	}
	tenants, err := tenantruntime.NewResolver(&staticTenantRuntimeSource{snapshot: runtimeSnapshot(workspaceID)}, factory)
	if err != nil {
		t.Fatal(err)
	}
	defer tenants.Close()
	resolver, _ := NewResolver(tenants)
	runtime, err := resolver.Resolve(t.Context(), workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.Epoch != 1 || runtime.Reader == nil || runtime.Projects == nil {
		t.Fatalf("PostgreSQL runtime = %#v", runtime)
	}
	generation, err := resolver.ResolveGenerationRuntime(t.Context(), workspaceID)
	if err != nil || generation.Epoch != 1 || generation.Fencer == nil {
		t.Fatalf("PostgreSQL generation runtime = %#v err=%v", generation, err)
	}
	if err := generation.Fencer.BeginGenerationWrite(t.Context(), workspaceID, generation.Epoch,
		func(reader taskdomain.GenerationStateReader, writer taskdomain.GenerationWriter) error {
			if reader == nil || writer == nil {
				return errors.New("missing transaction-scoped generation capabilities")
			}
			return nil
		}); err != nil {
		t.Fatalf("PostgreSQL generation fence: %v", err)
	}
}

func TestNewFactoryRequiresProtectedPostgresDialer(t *testing.T) {
	endpoints := &fakeDatabaseEndpoints{}
	if _, err := NewFactory(endpoints, ExpectedTenantSchemaVersion, nil); err == nil {
		t.Fatal("expected task runtime factory to reject an unprotected PostgreSQL opener")
	}
}

func TestSQLiteRuntimeAssemblesReadOnlyAndFencedApplicationCapabilities(t *testing.T) {
	fixture := newSQLiteRuntimeFixture(t, "runtime-capabilities")
	resolver := fixture.taskResolver(t)

	runtime, err := resolver.Resolve(t.Context(), fixture.workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.WorkspaceID != fixture.workspaceID || runtime.Epoch != 1 || runtime.Reader == nil || runtime.Tasks == nil ||
		runtime.Occurrences == nil || runtime.Projects == nil || runtime.Schedules == nil || runtime.Factory == nil {
		t.Fatalf("incomplete runtime: %#v", runtime)
	}
	if _, exposesStore := runtime.Reader.(storage.Store); exposesStore {
		t.Fatal("request runtime exposed the complete tenant store")
	}

	now := time.Date(2026, 7, 22, 9, 30, 0, 0, time.UTC)
	outcome, err := runtime.Projects.CreateProject(t.Context(), taskdomain.CreateProjectRequest{
		WorkspaceID: fixture.workspaceID, ExpectedRuntimeEpoch: runtime.Epoch,
		Project: taskdomain.Project{
			WorkspaceID: fixture.workspaceID, ID: "project-one", Name: "Project One",
			Kind: taskdomain.ProjectKindStandard, Horizon: taskdomain.ProjectHorizonShort, Status: taskdomain.ProjectStatusActive,
		},
		CommandID: "command-project-one", ActorID: "user-one", At: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Project.ID != "project-one" || outcome.Revision != 1 || outcome.CommandID != "command-project-one" {
		t.Fatalf("project outcome = %#v", outcome)
	}
	project, err := runtime.Reader.GetProject(t.Context(), "project-one")
	if err != nil || project.Project.WorkspaceID != fixture.workspaceID || project.Revision != 1 {
		t.Fatalf("project read = %#v, %v", project, err)
	}
}

func TestResolverRebuildsResourceWhenDurableTaskEpochChanges(t *testing.T) {
	fixture := newSQLiteRuntimeFixture(t, "runtime-epoch")
	resolver := fixture.taskResolver(t)

	first, err := resolver.Resolve(t.Context(), fixture.workspaceID)
	if err != nil || first.Epoch != 1 || fixture.endpoints.callCount() != 1 {
		t.Fatalf("first runtime = %#v err=%v calls=%d", first, err, fixture.endpoints.callCount())
	}
	fixture.exec(t, `UPDATE tenant_workspaces SET epoch=2 WHERE workspace_id=?`, fixture.workspaceID)
	fixture.exec(t, `UPDATE workspace_task_domain_state SET write_epoch=2 WHERE workspace_id=?`, fixture.workspaceID)
	fixture.runtimeSource.setEpoch(2)

	second, err := resolver.Resolve(t.Context(), fixture.workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if second.Epoch != 2 || fixture.endpoints.callCount() != 2 {
		t.Fatalf("rebuilt runtime epoch=%d endpoint_calls=%d", second.Epoch, fixture.endpoints.callCount())
	}
}

func TestGenerationRuntimeResolverUsesFreshEpochAndFencedWriterSQLite(t *testing.T) {
	fixture := newSQLiteRuntimeFixture(t, "generation-runtime")
	resolver := fixture.taskResolver(t)

	first, err := resolver.ResolveGenerationRuntime(t.Context(), fixture.workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if first.WorkspaceID != fixture.workspaceID || first.Epoch != 1 || first.Fencer == nil {
		t.Fatalf("first generation runtime=%#v", first)
	}
	called := false
	if err := first.Fencer.BeginGenerationWrite(t.Context(), fixture.workspaceID, first.Epoch,
		func(reader taskdomain.GenerationStateReader, writer taskdomain.GenerationWriter) error {
			called = reader != nil && writer != nil
			return nil
		}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("generation callback did not receive transaction-scoped capabilities")
	}

	fixture.exec(t, `UPDATE tenant_workspaces SET epoch=2 WHERE workspace_id=?`, fixture.workspaceID)
	fixture.exec(t, `UPDATE workspace_task_domain_state SET write_epoch=2 WHERE workspace_id=?`, fixture.workspaceID)
	fixture.runtimeSource.setEpoch(2)
	second, err := resolver.ResolveGenerationRuntime(t.Context(), fixture.workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if second.Epoch != 2 || fixture.endpoints.callCount() != 2 {
		t.Fatalf("fresh generation runtime=%#v endpoint_calls=%d", second, fixture.endpoints.callCount())
	}
	if err := second.Fencer.BeginGenerationWrite(t.Context(), fixture.workspaceID, 1,
		func(taskdomain.GenerationStateReader, taskdomain.GenerationWriter) error { return nil }); !errors.Is(err, storage.ErrTenantEpochMismatch) {
		t.Fatalf("stale generation epoch was accepted: %v", err)
	}
}

func TestGenerationRuntimeResolverRejectsTransitionalWorkspace(t *testing.T) {
	fixture := newSQLiteRuntimeFixture(t, "generation-transition")
	fixture.exec(t, `UPDATE workspace_task_domain_state SET model_version='legacy',migration_state='backfilling',
		accept_legacy_writes=1,migration_id='migration-generation' WHERE workspace_id=?`, fixture.workspaceID)
	_, err := fixture.taskResolver(t).ResolveGenerationRuntime(t.Context(), fixture.workspaceID)
	if !errors.Is(err, tenantruntime.ErrRuntimeUnavailable) {
		t.Fatalf("transitional workspace exposed generation runtime: %v", err)
	}
}

func TestGenerationEligibilityClassifierUsesControlAndTenantEpoch(t *testing.T) {
	t.Run("stable v2", func(t *testing.T) {
		fixture := newSQLiteRuntimeFixture(t, "generation-classifier-v2")
		stable, err := fixture.taskResolver(t).IsStableV2Workspace(t.Context(), fixture.workspaceID, 1)
		if err != nil || !stable {
			t.Fatalf("stable=%v err=%v", stable, err)
		}
	})
	t.Run("stable legacy is ineligible", func(t *testing.T) {
		fixture := newSQLiteRuntimeFixture(t, "generation-classifier-legacy")
		fixture.exec(t, `UPDATE workspace_task_domain_state SET model_version='legacy',accept_legacy_writes=1 WHERE workspace_id=?`, fixture.workspaceID)
		stable, err := fixture.taskResolver(t).IsStableV2Workspace(t.Context(), fixture.workspaceID, 1)
		if err != nil || stable {
			t.Fatalf("stable=%v err=%v", stable, err)
		}
	})
	t.Run("control tenant epoch mismatch", func(t *testing.T) {
		fixture := newSQLiteRuntimeFixture(t, "generation-classifier-epoch")
		stable, err := fixture.taskResolver(t).IsStableV2Workspace(t.Context(), fixture.workspaceID, 2)
		if err == nil || stable {
			t.Fatalf("stable=%v err=%v", stable, err)
		}
	})
}

func TestResolverRejectsControlAndTenantEpochDivergence(t *testing.T) {
	fixture := newSQLiteRuntimeFixture(t, "runtime-epoch-divergence")
	resolver := fixture.taskResolver(t)
	if _, err := resolver.Resolve(t.Context(), fixture.workspaceID); err != nil {
		t.Fatal(err)
	}
	fixture.exec(t, `UPDATE tenant_workspaces SET epoch=2 WHERE workspace_id=?`, fixture.workspaceID)
	fixture.exec(t, `UPDATE workspace_task_domain_state SET write_epoch=2 WHERE workspace_id=?`, fixture.workspaceID)

	_, err := resolver.Resolve(t.Context(), fixture.workspaceID)
	if !errors.Is(err, tenantruntime.ErrRuntimeUnavailable) {
		t.Fatalf("split control/data epoch was accepted: %v", err)
	}
}

func TestRuntimeFailsClosedForLegacyAndReadyWorkspaceStates(t *testing.T) {
	tests := []struct {
		name string
		sql  string
	}{
		{name: "legacy idle", sql: `UPDATE workspace_task_domain_state SET model_version='legacy',accept_legacy_writes=1 WHERE workspace_id=?`},
		{name: "ready", sql: `UPDATE workspace_task_domain_state SET model_version='legacy',migration_state='ready',accept_legacy_writes=0,
			migration_id='migration-one',cutover_revision=0,source_watermark=0 WHERE workspace_id=?`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newSQLiteRuntimeFixture(t, "runtime-state-"+test.name)
			fixture.exec(t, test.sql, fixture.workspaceID)
			_, err := fixture.taskResolver(t).Resolve(t.Context(), fixture.workspaceID)
			if !errors.Is(err, tenantruntime.ErrRuntimeUnavailable) {
				t.Fatalf("state did not fail closed: %v", err)
			}
		})
	}
}

func TestFactoryRejectsEndpointFailureOldSchemaAndUntypedStore(t *testing.T) {
	t.Run("endpoint failure", func(t *testing.T) {
		fixture := newSQLiteRuntimeFixture(t, "endpoint-failure")
		fixture.endpoints.err = errors.New("credential unavailable")
		_, err := fixture.taskResolver(t).Resolve(t.Context(), fixture.workspaceID)
		if !errors.Is(err, tenantruntime.ErrRuntimeUnavailable) || fixture.endpoints.callCount() != 1 {
			t.Fatalf("endpoint failure = %v", err)
		}
	})

	t.Run("endpoint identity mismatch", func(t *testing.T) {
		fixture := newSQLiteRuntimeFixture(t, "endpoint-identity")
		fixture.endpoints.config.WorkspaceID = "another-workspace"
		_, err := fixture.taskResolver(t).Resolve(t.Context(), fixture.workspaceID)
		if !errors.Is(err, tenantruntime.ErrRuntimeUnavailable) {
			t.Fatalf("endpoint identity mismatch = %v", err)
		}
	})

	t.Run("old schema", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "old-schema.test.db")
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`CREATE TABLE tenant_schema_migrations(version TEXT PRIMARY KEY,checksum TEXT NOT NULL);
			INSERT INTO tenant_schema_migrations VALUES('0001_tenant_baseline.sql','not-used')`); err != nil {
			t.Fatal(err)
		}
		_ = db.Close()
		endpoints := &fakeDatabaseEndpoints{config: DatabaseEndpointConfig{
			WorkspaceID: "old", EndpointID: "database", ProfileVersionID: "version-one",
			Storage: storage.Config{Env: "test", Driver: storage.DriverSQLite, SQLitePath: path},
		}}
		factory, _ := NewFactory(endpoints, ExpectedTenantSchemaVersion, rejectPostgresDial)
		_, err = factory.Build(t.Context(), runtimeSnapshot("old"))
		if !errors.Is(err, storage.ErrTenantSchemaNotReady) {
			t.Fatalf("old schema error = %v", err)
		}
		db, err = sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		var created int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='workspace_task_domain_state'`).Scan(&created); err != nil {
			t.Fatal(err)
		}
		if created != 0 {
			t.Fatal("runtime OpenTenant executed task-domain DDL")
		}
	})

	t.Run("untyped store", func(t *testing.T) {
		fixture := newSQLiteRuntimeFixture(t, "untyped-store")
		registry := &wrappingRegistry{delegate: fixture.registry, hideType: true}
		factory, err := newFactory(registry, fixture.endpoints, ExpectedTenantSchemaVersion)
		if err != nil {
			t.Fatal(err)
		}
		_, err = factory.Build(t.Context(), runtimeSnapshot(fixture.workspaceID))
		if !errors.Is(err, ErrTaskRuntimeType) || registry.last == nil || registry.last.closeCount() != 1 {
			t.Fatalf("untyped store error=%v close_count=%d", err, registry.last.closeCount())
		}
	})
}

func TestResourceCloseReleasesTenantStore(t *testing.T) {
	fixture := newSQLiteRuntimeFixture(t, "resource-close")
	registry := &wrappingRegistry{delegate: fixture.registry}
	factory, err := newFactory(registry, fixture.endpoints, ExpectedTenantSchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	resource, err := factory.Build(t.Context(), runtimeSnapshot(fixture.workspaceID))
	if err != nil {
		t.Fatal(err)
	}
	if err := resource.Close(); err != nil {
		t.Fatal(err)
	}
	if err := resource.Close(); err != nil || registry.last.closeCount() != 1 {
		t.Fatalf("idempotent close error=%v count=%d", err, registry.last.closeCount())
	}
	if _, err := resource.(*Resource).ApplicationSnapshot(t.Context()); !errors.Is(err, ErrTaskRuntimeClosed) {
		t.Fatalf("closed resource remained usable: %v", err)
	}
}

func TestProfileDatabaseEndpointSourceBuildsPostgresAndSQLiteConfigs(t *testing.T) {
	t.Run("postgres", func(t *testing.T) {
		source, err := NewProfileDatabaseEndpointConfigSource(profileSourceStub{profile: airuntime.EndpointProfile{
			EndpointID: "database", Kind: "data_store", ProfileVersionID: "version-one", Provider: "postgres",
			ConfigJSON: `{"endpoint":"postgres://tenant@db.example.com/flowspace_test?sslmode=require","schema":"workspace_one"}`,
			Secret:     []byte("password-one"),
		}}, "test")
		if err != nil {
			t.Fatal(err)
		}
		got, err := source.LoadDatabaseEndpointConfig(t.Context(), "workspace", "database")
		if err != nil {
			t.Fatal(err)
		}
		parsed, _ := url.Parse(got.Storage.URL)
		password, hasPassword := parsed.User.Password()
		if got.Storage.Driver != storage.DriverPostgres || parsed.Query().Get("search_path") != "workspace_one" || !hasPassword || password != "password-one" {
			t.Fatalf("PostgreSQL config = %#v", got.Storage)
		}
	})

	t.Run("sqlite", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "profile.test.db")
		source, _ := NewProfileDatabaseEndpointConfigSource(profileSourceStub{profile: airuntime.EndpointProfile{
			EndpointID: "database", Kind: "data_store", ProfileVersionID: "version-one", Provider: "sqlite",
			ConfigJSON: `{"path":"` + filepath.ToSlash(path) + `"}`,
		}}, "test")
		got, err := source.LoadDatabaseEndpointConfig(t.Context(), "workspace", "database")
		if err != nil || got.Storage.Driver != storage.DriverSQLite || filepath.Clean(got.Storage.SQLitePath) != filepath.Clean(path) {
			t.Fatalf("SQLite config = %#v, %v", got.Storage, err)
		}
	})

	t.Run("postgres verified profile may use passwordless authentication", func(t *testing.T) {
		source, _ := NewProfileDatabaseEndpointConfigSource(profileSourceStub{profile: airuntime.EndpointProfile{
			EndpointID: "database", Kind: "data_store", ProfileVersionID: "version-one", Provider: "postgres",
			ConfigJSON: `{"endpoint":"postgres://tenant@db.example.com/flowspace_test","schema":"public"}`,
		}}, "test")
		got, err := source.LoadDatabaseEndpointConfig(t.Context(), "workspace", "database")
		if err != nil {
			t.Fatal(err)
		}
		parsed, _ := url.Parse(got.Storage.URL)
		if _, hasPassword := parsed.User.Password(); hasPassword {
			t.Fatal("passwordless verified profile gained a synthetic password")
		}
	})
}

type sqliteRuntimeFixture struct {
	t             *testing.T
	workspaceID   string
	path          string
	registry      *storage.Registry
	endpoints     *fakeDatabaseEndpoints
	runtimeSource *staticTenantRuntimeSource
}

func newSQLiteRuntimeFixture(t *testing.T, name string) *sqliteRuntimeFixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "flowspace.test.db")
	cfg := storage.Config{Env: "test", Driver: storage.DriverSQLite, SQLitePath: path, Name: "flowspace.test.db"}
	provider := storagesqlite.Provider{}
	if err := provider.MigrateTenant(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	workspaceID := "workspace-" + name
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO tenant_workspaces(workspace_id,epoch,state) VALUES(?,1,'active')`, workspaceID); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	registry := storage.NewRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatal(err)
	}
	endpoint := &fakeDatabaseEndpoints{config: DatabaseEndpointConfig{
		WorkspaceID: workspaceID, EndpointID: "database", ProfileVersionID: "version-one", Storage: cfg,
	}}
	return &sqliteRuntimeFixture{
		t: t, workspaceID: workspaceID, path: path, registry: registry, endpoints: endpoint,
		runtimeSource: &staticTenantRuntimeSource{snapshot: runtimeSnapshot(workspaceID)},
	}
}

func (f *sqliteRuntimeFixture) taskResolver(t *testing.T) *Resolver {
	t.Helper()
	factory, err := NewFactory(f.endpoints, ExpectedTenantSchemaVersion, rejectPostgresDial)
	if err != nil {
		t.Fatal(err)
	}
	tenants, err := tenantruntime.NewResolver(f.runtimeSource, factory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tenants.Close() })
	resolver, err := NewResolver(tenants)
	if err != nil {
		t.Fatal(err)
	}
	return resolver
}

func rejectPostgresDial(context.Context, string, string) (net.Conn, error) {
	return nil, errors.New("unexpected PostgreSQL connection")
}

func (f *sqliteRuntimeFixture) exec(t *testing.T, statement string, args ...any) {
	t.Helper()
	db, err := sql.Open("sqlite", f.path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(statement, args...); err != nil {
		t.Fatal(err)
	}
}

func runtimeSnapshot(workspaceID string) tenantruntime.Snapshot {
	return tenantruntime.Snapshot{
		Version:            tenantruntime.Version{WorkspaceID: workspaceID, Mode: "active", Epoch: 1, BindingRevision: 20},
		DatabaseEndpointID: "database", ObjectEndpointID: "objects", ChatMode: "disabled", TranscriptionMode: "disabled",
	}
}

type staticTenantRuntimeSource struct {
	mu       sync.Mutex
	snapshot tenantruntime.Snapshot
}

func (s *staticTenantRuntimeSource) LoadVersion(context.Context, string) (tenantruntime.Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshot.Version, nil
}
func (s *staticTenantRuntimeSource) LoadSnapshot(context.Context, tenantruntime.Version) (tenantruntime.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshot, nil
}
func (s *staticTenantRuntimeSource) setEpoch(epoch int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshot.Epoch = epoch
}

type fakeDatabaseEndpoints struct {
	mu     sync.Mutex
	config DatabaseEndpointConfig
	err    error
	calls  int
}

func (s *fakeDatabaseEndpoints) LoadDatabaseEndpointConfig(context.Context, string, string) (DatabaseEndpointConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.config, s.err
}
func (s *fakeDatabaseEndpoints) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

type profileSourceStub struct {
	profile airuntime.EndpointProfile
	err     error
}

func (s profileSourceStub) LoadEndpointProfile(context.Context, string, string, string) (airuntime.EndpointProfile, error) {
	return s.profile, s.err
}

type wrappingRegistry struct {
	delegate *storage.Registry
	hideType bool
	last     *trackingStore
}

func (r *wrappingRegistry) OpenTenant(ctx context.Context, cfg storage.Config, version string) (storage.Store, error) {
	store, err := r.delegate.OpenTenant(ctx, cfg, version)
	if err != nil {
		return nil, err
	}
	r.last = &trackingStore{Store: store}
	if r.hideType {
		return &untypedStore{Store: r.last}, nil
	}
	return &typedTrackingStore{trackingStore: r.last, runtime: store.(storage.TaskDomainRuntimeStore)}, nil
}

type trackingStore struct {
	storage.Store
	mu     sync.Mutex
	closed int
}

func (s *trackingStore) Close() error {
	s.mu.Lock()
	s.closed++
	s.mu.Unlock()
	return s.Store.Close()
}
func (s *trackingStore) closeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

type untypedStore struct{ storage.Store }

type typedTrackingStore struct {
	*trackingStore
	runtime storage.TaskDomainRuntimeStore
}

func (s *typedTrackingStore) TaskDomainReader(workspaceID string) taskdomain.TaskDomainReader {
	return s.runtime.TaskDomainReader(workspaceID)
}
func (s *typedTrackingStore) LoadTaskDomainRuntimeState(ctx context.Context, workspaceID string) (storage.TaskDomainRuntimeState, error) {
	return s.runtime.LoadTaskDomainRuntimeState(ctx, workspaceID)
}

var _ taskapp.RuntimeResolver = (*Resolver)(nil)
