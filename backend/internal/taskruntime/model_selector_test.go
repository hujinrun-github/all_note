package taskruntime

import (
	"context"
	"errors"
	"testing"

	"github.com/hujinrun/flowspace/internal/taskapp"
	"github.com/hujinrun/flowspace/internal/tenantruntime"
)

func TestModelSelectorReturnsOnlyStableDurableModels(t *testing.T) {
	tests := []struct {
		name  string
		state string
		want  taskapp.ModelVersion
	}{
		{
			name:  "legacy idle",
			state: `UPDATE workspace_task_domain_state SET model_version='legacy',migration_state='idle',accept_legacy_writes=1 WHERE workspace_id=?`,
			want:  taskapp.ModelLegacy,
		},
		{
			name:  "v2 idle",
			state: `UPDATE workspace_task_domain_state SET model_version='v2',migration_state='idle',accept_legacy_writes=0 WHERE workspace_id=?`,
			want:  taskapp.ModelV2,
		},
		{
			name: "v2 cutover",
			state: `UPDATE workspace_task_domain_state SET model_version='v2',migration_state='cutover',accept_legacy_writes=0,
				migration_id='migration-one',cutover_revision=4,source_watermark=4 WHERE workspace_id=?`,
			want: taskapp.ModelV2,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newSQLiteRuntimeFixture(t, "model-"+test.name)
			fixture.exec(t, test.state, fixture.workspaceID)
			selector, tenants := fixture.modelSelector(t, fixture.registry)
			defer tenants.Close()

			got, err := selector.SelectTaskDomainModel(t.Context(), fixture.workspaceID)
			if err != nil || got != test.want {
				t.Fatalf("model = %q, err=%v, want=%q", got, err, test.want)
			}
		})
	}
}

func TestLegacyModelResourceDoesNotExposeV2Application(t *testing.T) {
	fixture := newSQLiteRuntimeFixture(t, "legacy-capability")
	fixture.exec(t, `UPDATE workspace_task_domain_state SET model_version='legacy',accept_legacy_writes=1 WHERE workspace_id=?`, fixture.workspaceID)
	factory, err := newFactory(fixture.registry, fixture.endpoints, ExpectedTenantSchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	resource, err := factory.Build(t.Context(), runtimeSnapshot(fixture.workspaceID))
	if err != nil {
		t.Fatal(err)
	}
	defer resource.Close()
	if _, exposesV2 := resource.(interface {
		ApplicationSnapshot(context.Context) (taskapp.RuntimeSnapshot, error)
	}); exposesV2 {
		t.Fatal("legacy model resource exposed the v2 application snapshot")
	}
}

func TestModelSelectorRejectsTransitionalAndUnhealthyStates(t *testing.T) {
	tests := []struct {
		name  string
		state string
	}{
		{
			name: "backfilling",
			state: `UPDATE workspace_task_domain_state SET model_version='legacy',migration_state='backfilling',accept_legacy_writes=1,
				migration_id='migration-one' WHERE workspace_id=?`,
		},
		{
			name: "catching up",
			state: `UPDATE workspace_task_domain_state SET model_version='legacy',migration_state='catching_up',accept_legacy_writes=1,
				migration_id='migration-one' WHERE workspace_id=?`,
		},
		{
			name: "draining",
			state: `UPDATE workspace_task_domain_state SET model_version='legacy',migration_state='draining',accept_legacy_writes=0,
				migration_id='migration-one',cutover_revision=4,source_watermark=3 WHERE workspace_id=?`,
		},
		{
			name: "ready",
			state: `UPDATE workspace_task_domain_state SET model_version='legacy',migration_state='ready',accept_legacy_writes=0,
				migration_id='migration-one',cutover_revision=4,source_watermark=4 WHERE workspace_id=?`,
		},
		{
			name: "failed",
			state: `UPDATE workspace_task_domain_state SET model_version='legacy',migration_state='failed',accept_legacy_writes=1,
				migration_id='migration-one',last_error='copy failed' WHERE workspace_id=?`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newSQLiteRuntimeFixture(t, "reject-"+test.name)
			fixture.exec(t, test.state, fixture.workspaceID)
			selector, tenants := fixture.modelSelector(t, fixture.registry)
			defer tenants.Close()

			model, err := selector.SelectTaskDomainModel(t.Context(), fixture.workspaceID)
			if !errors.Is(err, tenantruntime.ErrRuntimeUnavailable) || model != "" {
				t.Fatalf("unsafe state selected model %q: %v", model, err)
			}
		})
	}
}

func TestModelSelectorRejectsAnchorAndControlDataEpochDivergence(t *testing.T) {
	t.Run("anchor fenced", func(t *testing.T) {
		fixture := newSQLiteRuntimeFixture(t, "anchor-fenced")
		fixture.exec(t, `UPDATE tenant_workspaces SET state='fenced',migration_id='migration-one' WHERE workspace_id=?`, fixture.workspaceID)
		selector, tenants := fixture.modelSelector(t, fixture.registry)
		defer tenants.Close()
		if model, err := selector.SelectTaskDomainModel(t.Context(), fixture.workspaceID); !errors.Is(err, tenantruntime.ErrRuntimeUnavailable) || model != "" {
			t.Fatalf("fenced anchor selected model %q: %v", model, err)
		}
	})

	t.Run("control epoch ahead", func(t *testing.T) {
		fixture := newSQLiteRuntimeFixture(t, "control-epoch")
		fixture.runtimeSource.setEpoch(2)
		selector, tenants := fixture.modelSelector(t, fixture.registry)
		defer tenants.Close()
		if model, err := selector.SelectTaskDomainModel(t.Context(), fixture.workspaceID); !errors.Is(err, tenantruntime.ErrRuntimeUnavailable) || model != "" {
			t.Fatalf("split epoch selected model %q: %v", model, err)
		}
	})

	t.Run("tenant domain and anchor diverge", func(t *testing.T) {
		fixture := newSQLiteRuntimeFixture(t, "tenant-epoch")
		fixture.exec(t, `UPDATE tenant_workspaces SET epoch=2 WHERE workspace_id=?`, fixture.workspaceID)
		selector, tenants := fixture.modelSelector(t, fixture.registry)
		defer tenants.Close()
		if model, err := selector.SelectTaskDomainModel(t.Context(), fixture.workspaceID); !errors.Is(err, tenantruntime.ErrRuntimeUnavailable) || model != "" {
			t.Fatalf("tenant split epoch selected model %q: %v", model, err)
		}
	})
}

func TestModelSelectorReusesVersionCacheAndRebuildsOnPersistentVersionChange(t *testing.T) {
	fixture := newSQLiteRuntimeFixture(t, "model-cache")
	fixture.exec(t, `UPDATE workspace_task_domain_state SET model_version='legacy',accept_legacy_writes=1 WHERE workspace_id=?`, fixture.workspaceID)
	registry := &wrappingRegistry{delegate: fixture.registry}
	selector, tenants := fixture.modelSelector(t, registry)
	defer tenants.Close()

	for range 2 {
		if model, err := selector.SelectTaskDomainModel(t.Context(), fixture.workspaceID); err != nil || model != taskapp.ModelLegacy {
			t.Fatalf("cached selection model=%q err=%v", model, err)
		}
	}
	if fixture.endpoints.callCount() != 1 || registry.last == nil || registry.last.closeCount() != 0 {
		t.Fatalf("cache endpoint_calls=%d old_close=%d", fixture.endpoints.callCount(), registry.last.closeCount())
	}
	first := registry.last
	fixture.runtimeSource.setBindingRevision(21)
	if model, err := selector.SelectTaskDomainModel(t.Context(), fixture.workspaceID); err != nil || model != taskapp.ModelLegacy {
		t.Fatalf("rebuilt selection model=%q err=%v", model, err)
	}
	if fixture.endpoints.callCount() != 2 || first.closeCount() != 1 {
		t.Fatalf("rebuild endpoint_calls=%d old_close=%d", fixture.endpoints.callCount(), first.closeCount())
	}
}

func TestModelSelectorInvalidatesCachedModelWhenDurableStateTransitions(t *testing.T) {
	fixture := newSQLiteRuntimeFixture(t, "model-transition")
	fixture.exec(t, `UPDATE workspace_task_domain_state SET model_version='legacy',accept_legacy_writes=1 WHERE workspace_id=?`, fixture.workspaceID)
	registry := &wrappingRegistry{delegate: fixture.registry}
	selector, tenants := fixture.modelSelector(t, registry)
	defer tenants.Close()
	if _, err := selector.SelectTaskDomainModel(t.Context(), fixture.workspaceID); err != nil {
		t.Fatal(err)
	}
	first := registry.last
	fixture.exec(t, `UPDATE workspace_task_domain_state SET migration_state='backfilling',migration_id='migration-one' WHERE workspace_id=?`, fixture.workspaceID)
	if model, err := selector.SelectTaskDomainModel(t.Context(), fixture.workspaceID); err == nil || model != "" {
		t.Fatalf("transition selected model %q: %v", model, err)
	}
	if first.closeCount() != 1 {
		t.Fatalf("transition did not close invalid cached resource: %d", first.closeCount())
	}
}

func TestModelSelectorRebuildsWhenDurableStableModelChanges(t *testing.T) {
	fixture := newSQLiteRuntimeFixture(t, "stable-model-change")
	fixture.exec(t, `UPDATE workspace_task_domain_state SET model_version='legacy',accept_legacy_writes=1 WHERE workspace_id=?`, fixture.workspaceID)
	registry := &wrappingRegistry{delegate: fixture.registry}
	selector, tenants := fixture.modelSelector(t, registry)
	defer tenants.Close()
	if model, err := selector.SelectTaskDomainModel(t.Context(), fixture.workspaceID); err != nil || model != taskapp.ModelLegacy {
		t.Fatalf("legacy model=%q err=%v", model, err)
	}
	first := registry.last
	fixture.exec(t, `UPDATE workspace_task_domain_state SET model_version='v2',accept_legacy_writes=0 WHERE workspace_id=?`, fixture.workspaceID)
	if model, err := selector.SelectTaskDomainModel(t.Context(), fixture.workspaceID); err != nil || model != taskapp.ModelV2 {
		t.Fatalf("v2 model=%q err=%v", model, err)
	}
	if first.closeCount() != 1 || fixture.endpoints.callCount() != 2 {
		t.Fatalf("stable model rebuild close=%d endpoint_calls=%d", first.closeCount(), fixture.endpoints.callCount())
	}
}

func TestModelSelectorCloseReleasesCachedTenantStore(t *testing.T) {
	fixture := newSQLiteRuntimeFixture(t, "model-close")
	registry := &wrappingRegistry{delegate: fixture.registry}
	selector, tenants := fixture.modelSelector(t, registry)
	if _, err := selector.SelectTaskDomainModel(t.Context(), fixture.workspaceID); err != nil {
		t.Fatal(err)
	}
	if err := tenants.Close(); err != nil {
		t.Fatal(err)
	}
	if registry.last == nil || registry.last.closeCount() != 1 {
		t.Fatalf("resolver close count = %d", registry.last.closeCount())
	}
}

func (f *sqliteRuntimeFixture) modelSelector(t *testing.T, registry tenantStoreRegistry) (*DurableModelSelector, *tenantruntime.Resolver) {
	t.Helper()
	factory, err := newFactory(registry, f.endpoints, ExpectedTenantSchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	tenants, err := tenantruntime.NewResolver(f.runtimeSource, factory)
	if err != nil {
		t.Fatal(err)
	}
	selector, err := NewModelSelector(tenants)
	if err != nil {
		t.Fatal(err)
	}
	return selector, tenants
}

func (s *staticTenantRuntimeSource) setBindingRevision(revision int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshot.BindingRevision = revision
}
