package taskruntime

import (
	"context"
	"errors"
	"testing"

	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/taskapp"
	"github.com/hujinrun/flowspace/internal/tenantruntime"
	"github.com/hujinrun/flowspace/internal/workspacequery"
)

func TestWorkspaceResolversRejectMismatchedContextBeforeOpeningTenant(t *testing.T) {
	const workspaceID = "workspace-a"
	identity := auth.RequestIdentity{UserID: "user-a", WorkspaceID: workspaceID}
	tests := []struct {
		name string
		ctx  context.Context
		want error
	}{
		{name: "different scope", ctx: auth.ContextWithWorkspaceScope(t.Context(), "workspace-b"), want: auth.ErrWorkspaceScopeMismatch},
		{name: "blank scope", ctx: auth.ContextWithWorkspaceScope(t.Context(), " \t"), want: auth.ErrMissingWorkspace},
		{name: "different identity workspace", ctx: auth.ContextWithIdentity(t.Context(), auth.RequestIdentity{UserID: "user-a", WorkspaceID: "workspace-b"}), want: auth.ErrWorkspaceScopeMismatch},
		{name: "identity and scope disagree", ctx: auth.ContextWithWorkspaceScope(auth.ContextWithIdentity(t.Context(), identity), "workspace-b"), want: auth.ErrWorkspaceScopeMismatch},
		{name: "identity missing actor", ctx: auth.ContextWithIdentity(t.Context(), auth.RequestIdentity{WorkspaceID: workspaceID}), want: auth.ErrMissingIdentity},
		{name: "identity missing workspace", ctx: auth.ContextWithIdentity(t.Context(), auth.RequestIdentity{UserID: "user-a"}), want: auth.ErrMissingWorkspace},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tenants := &workspaceResolutionStub{}
			resolver, err := newResolver(tenants)
			if err != nil {
				t.Fatal(err)
			}
			_, taskErr := resolver.Resolve(test.ctx, workspaceID)
			_, mobileErr := resolver.ResolveMobileRuntime(test.ctx, workspaceID)
			_, queryErr := resolver.ResolveWorkspaceQuery(test.ctx, workspaceID)
			for kind, got := range map[string]error{"task": taskErr, "mobile": mobileErr, "query": queryErr} {
				if !errors.Is(got, test.want) || !errors.Is(got, taskapp.ErrInvalidRuntime) {
					t.Errorf("%s resolver error = %v, want %v wrapped as invalid runtime", kind, got, test.want)
				}
			}
			if tenants.calls != 0 || tenants.invalidations != 0 {
				t.Fatalf("mismatched authorization accessed tenant: calls=%d invalidations=%d", tenants.calls, tenants.invalidations)
			}
		})
	}
}

func TestSharedWorkspaceResolutionRetriesOnlyEpochChangesOnce(t *testing.T) {
	transient := errors.New("projection unavailable")
	tests := []struct {
		name              string
		projectionErrors  []error
		resolveError      error
		wantError         error
		wantCalls         int
		wantInvalidations int
	}{
		{name: "epoch refresh", projectionErrors: []error{ErrTaskRuntimeEpochChanged, nil}, wantCalls: 2, wantInvalidations: 1},
		{name: "epoch still changing", projectionErrors: []error{ErrTaskRuntimeEpochChanged, ErrTaskRuntimeEpochChanged}, wantError: ErrTaskRuntimeEpochChanged, wantCalls: 2, wantInvalidations: 2},
		{name: "non epoch failure", projectionErrors: []error{transient}, wantError: transient, wantCalls: 1, wantInvalidations: 1},
		{name: "tenant unavailable", resolveError: transient, wantError: transient, wantCalls: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tenants := &workspaceResolutionStub{err: test.resolveError}
			resolver, err := newResolver(tenants)
			if err != nil {
				t.Fatal(err)
			}
			projections := 0
			result, err := resolveWorkspaceSnapshot(resolver, auth.ContextWithWorkspaceScope(t.Context(), "workspace-a"), "workspace-a",
				func(context.Context, tenantruntime.Runtime) (int, error) {
					projectionError := test.projectionErrors[projections]
					projections++
					return 42, projectionError
				})
			if !errors.Is(err, test.wantError) {
				t.Fatalf("resolution error = %v, want %v", err, test.wantError)
			}
			if test.wantError != nil && result != 0 || test.wantError == nil && result != 42 {
				t.Fatalf("resolution result = %d, error=%v", result, err)
			}
			if tenants.calls != test.wantCalls || tenants.invalidations != test.wantInvalidations {
				t.Fatalf("calls=%d invalidations=%d, want %d/%d", tenants.calls, tenants.invalidations, test.wantCalls, test.wantInvalidations)
			}
		})
	}
}

func TestWorkspaceQueryRuntimeRequiresExplicitReadCapability(t *testing.T) {
	fixture := newSQLiteRuntimeFixture(t, "query-capability-required")
	application, err := fixture.taskResolver(t).Resolve(t.Context(), fixture.workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	// An embedded narrow interface intentionally hides optional query methods.
	application.Reader = taskReaderOnly{TaskDomainReader: application.Reader}
	tenants := &workspaceResolutionStub{resource: &applicationResourceStub{application: application}}
	resolver, err := newResolver(tenants)
	if err != nil {
		t.Fatal(err)
	}
	got, err := resolver.ResolveWorkspaceQuery(t.Context(), fixture.workspaceID)
	if !errors.Is(err, workspacequery.ErrRuntimeUnavailable) || got != (workspacequery.Runtime{}) {
		t.Fatalf("query without read capability = %#v, error=%v", got, err)
	}
}

func TestWorkspaceQueryRuntimeUsesSameVerifiedApplicationResolution(t *testing.T) {
	fixture := newSQLiteRuntimeFixture(t, "query-application-boundary")
	application, err := fixture.taskResolver(t).Resolve(t.Context(), fixture.workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	reader := &queryCapableTaskReader{
		TaskDomainReader: application.Reader,
		Reader:           &workspaceQueryReaderStub{},
	}
	application.Reader = reader
	tenants := &workspaceResolutionStub{resource: &applicationResourceStub{application: application}}
	resolver, err := newResolver(tenants)
	if err != nil {
		t.Fatal(err)
	}
	ctx := auth.ContextWithIdentity(t.Context(), auth.RequestIdentity{UserID: "user-a", WorkspaceID: fixture.workspaceID})
	ctx = auth.ContextWithWorkspaceScope(ctx, fixture.workspaceID)
	got, err := resolver.ResolveWorkspaceQuery(ctx, fixture.workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if got.WorkspaceID != fixture.workspaceID || got.Epoch != application.Epoch || got.Reader != reader || tenants.calls != 1 {
		t.Fatalf("query runtime = %#v, tenant resolutions=%d", got, tenants.calls)
	}
	if _, exposesStore := got.Reader.(storage.Store); exposesStore {
		t.Fatal("query runtime exposed a writable tenant store")
	}
}

func TestWorkspaceQueryRuntimeRevalidatesEpochAndAcceptsExplicitJobScope(t *testing.T) {
	fixture := newSQLiteRuntimeFixture(t, "query-runtime-epoch")
	resolver := fixture.taskResolver(t)
	ctx := auth.ContextWithWorkspaceScope(t.Context(), fixture.workspaceID)
	window, err := workspacequery.NewWindow("2026-11-19", "Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	first, err := resolver.ResolveWorkspaceQuery(ctx, fixture.workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if first.Epoch != 1 {
		t.Fatalf("first epoch = %d", first.Epoch)
	}
	if _, err := first.Reader.Overview(ctx, workspacequery.Request{Window: window, ExpectedEpoch: first.Epoch, TodayLimit: 5, ProjectLimit: 3}); err != nil {
		t.Fatalf("first read capability: %v", err)
	}

	fixture.exec(t, `UPDATE tenant_workspaces SET epoch=2 WHERE workspace_id=?`, fixture.workspaceID)
	fixture.exec(t, `UPDATE workspace_task_domain_state SET write_epoch=2 WHERE workspace_id=?`, fixture.workspaceID)
	fixture.runtimeSource.setEpoch(2)
	second, err := resolver.ResolveWorkspaceQuery(ctx, fixture.workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if second.Epoch != 2 || fixture.endpoints.callCount() != 2 {
		t.Fatalf("fresh query epoch=%d endpoint_calls=%d", second.Epoch, fixture.endpoints.callCount())
	}
	if _, err := second.Reader.Overview(ctx, workspacequery.Request{Window: window, ExpectedEpoch: second.Epoch, TodayLimit: 5, ProjectLimit: 3}); err != nil {
		t.Fatalf("refreshed read capability: %v", err)
	}
	if _, err := second.Reader.Overview(ctx, workspacequery.Request{Window: window, ExpectedEpoch: first.Epoch, TodayLimit: 5, ProjectLimit: 3}); !errors.Is(err, workspacequery.ErrRuntimeUnavailable) {
		t.Fatalf("stale query epoch was accepted: %v", err)
	}
}

func TestWorkspaceQueryRuntimeRejectsTransitionalWorkspace(t *testing.T) {
	fixture := newSQLiteRuntimeFixture(t, "query-runtime-transition")
	fixture.exec(t, `UPDATE workspace_task_domain_state SET model_version='legacy',migration_state='backfilling',
		accept_legacy_writes=1,migration_id='migration-query' WHERE workspace_id=?`, fixture.workspaceID)
	got, err := fixture.taskResolver(t).ResolveWorkspaceQuery(t.Context(), fixture.workspaceID)
	if !errors.Is(err, workspacequery.ErrRuntimeUnavailable) || got.Reader != nil {
		t.Fatalf("transitional query runtime = %#v, error=%v", got, err)
	}
}

type workspaceResolutionStub struct {
	resource      tenantruntime.Resource
	err           error
	calls         int
	invalidations int
}

func (s *workspaceResolutionStub) Resolve(context.Context, string) (tenantruntime.Runtime, error) {
	s.calls++
	return tenantruntime.Runtime{Resource: s.resource}, s.err
}

func (s *workspaceResolutionStub) Invalidate(string) { s.invalidations++ }

type taskReaderOnly struct{ taskapp.TaskDomainReader }

type queryCapableTaskReader struct {
	taskapp.TaskDomainReader
	workspacequery.Reader
}

type workspaceQueryReaderStub struct{}

func (*workspaceQueryReaderStub) Overview(context.Context, workspacequery.Request) (workspacequery.PlanningOverview, error) {
	return workspacequery.PlanningOverview{}, nil
}

func (*workspaceQueryReaderStub) ProjectSummaries(context.Context, workspacequery.ProjectRequest) ([]workspacequery.ProjectSummary, error) {
	return nil, nil
}

func (*workspaceQueryReaderStub) ProjectOverview(context.Context, workspacequery.ProjectRequest) (workspacequery.ProjectSummary, error) {
	return workspacequery.ProjectSummary{}, nil
}

type applicationResourceStub struct{ application taskapp.RuntimeSnapshot }

func (r *applicationResourceStub) Close() error { return nil }

func (r *applicationResourceStub) ApplicationSnapshot(context.Context) (taskapp.RuntimeSnapshot, error) {
	return r.application, nil
}
