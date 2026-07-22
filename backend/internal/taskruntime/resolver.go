package taskruntime

import (
	"context"
	"errors"
	"fmt"

	"github.com/hujinrun/flowspace/internal/taskapp"
	"github.com/hujinrun/flowspace/internal/taskdomain"
	"github.com/hujinrun/flowspace/internal/tenantruntime"
)

type tenantRuntimeResolver interface {
	Resolve(context.Context, string) (tenantruntime.Runtime, error)
	Invalidate(string)
}

// ResolveGenerationRuntime implements the same per-claim durable revalidation
// as request runtime resolution, but exposes only a fenced generation
// transaction. CreatedEpoch stays with the control-plane claim; this method
// always returns the epoch that is current at execution time.
func (r *Resolver) ResolveGenerationRuntime(ctx context.Context, workspaceID string) (taskdomain.GenerationRuntimeSnapshot, error) {
	if r == nil || r.tenants == nil || workspaceID == "" {
		return taskdomain.GenerationRuntimeSnapshot{}, taskapp.ErrInvalidRuntime
	}
	snapshot, err := r.resolveGenerationOnce(ctx, workspaceID)
	if err == nil {
		return validateGenerationSnapshot(workspaceID, snapshot)
	}
	r.tenants.Invalidate(workspaceID)
	if !errors.Is(err, ErrTaskRuntimeEpochChanged) {
		return taskdomain.GenerationRuntimeSnapshot{}, err
	}
	snapshot, err = r.resolveGenerationOnce(ctx, workspaceID)
	if err != nil {
		r.tenants.Invalidate(workspaceID)
		return taskdomain.GenerationRuntimeSnapshot{}, err
	}
	return validateGenerationSnapshot(workspaceID, snapshot)
}

func (r *Resolver) resolveGenerationOnce(ctx context.Context, workspaceID string) (taskdomain.GenerationRuntimeSnapshot, error) {
	runtime, err := r.tenants.Resolve(ctx, workspaceID)
	if err != nil {
		return taskdomain.GenerationRuntimeSnapshot{}, err
	}
	resource, ok := runtime.Resource.(interface {
		GenerationSnapshot(context.Context) (taskdomain.GenerationRuntimeSnapshot, error)
	})
	if !ok {
		return taskdomain.GenerationRuntimeSnapshot{}, fmt.Errorf("%w: task model does not expose v2 generation", tenantruntime.ErrRuntimeUnavailable)
	}
	return resource.GenerationSnapshot(ctx)
}

func validateGenerationSnapshot(workspaceID string, snapshot taskdomain.GenerationRuntimeSnapshot) (taskdomain.GenerationRuntimeSnapshot, error) {
	if snapshot.WorkspaceID != workspaceID || snapshot.Epoch < 1 || snapshot.Fencer == nil {
		return taskdomain.GenerationRuntimeSnapshot{}, fmt.Errorf("%w: incomplete generation snapshot", ErrTaskRuntimeType)
	}
	return snapshot, nil
}

// IsStableV2Workspace is the scheduler eligibility classifier. It compares
// the durable control epoch supplied by the control scan with the tenant
// anchor/task-domain epoch and returns false only for a stable legacy model.
// Transitional or unhealthy states return an error and are never scheduled.
func (r *Resolver) IsStableV2Workspace(ctx context.Context, workspaceID string, controlEpoch int64) (bool, error) {
	if r == nil || r.tenants == nil || workspaceID == "" || controlEpoch < 1 {
		return false, taskapp.ErrInvalidRuntime
	}
	for attempt := 0; attempt < 2; attempt++ {
		stable, err := r.classifyStableV2Once(ctx, workspaceID, controlEpoch)
		if err == nil {
			return stable, nil
		}
		r.tenants.Invalidate(workspaceID)
		if attempt == 1 || (!errors.Is(err, ErrTaskRuntimeEpochChanged) && !errors.Is(err, ErrTaskRuntimeModelChanged)) {
			return false, err
		}
	}
	return false, ErrTaskRuntimeType
}

func (r *Resolver) classifyStableV2Once(ctx context.Context, workspaceID string, controlEpoch int64) (bool, error) {
	runtime, err := r.tenants.Resolve(ctx, workspaceID)
	if err != nil {
		return false, err
	}
	resource, ok := runtime.Resource.(taskModelResource)
	if !ok {
		return false, ErrTaskRuntimeType
	}
	model, err := resource.TaskDomainModel(ctx)
	if err != nil {
		return false, err
	}
	if model == taskapp.ModelLegacy {
		return false, nil
	}
	if model != taskapp.ModelV2 {
		return false, ErrTaskRuntimeType
	}
	generationResource, ok := runtime.Resource.(interface {
		GenerationSnapshot(context.Context) (taskdomain.GenerationRuntimeSnapshot, error)
	})
	if !ok {
		return false, ErrTaskRuntimeType
	}
	snapshot, err := generationResource.GenerationSnapshot(ctx)
	if err != nil {
		return false, err
	}
	if snapshot.WorkspaceID != workspaceID || snapshot.Epoch != controlEpoch || snapshot.Fencer == nil {
		return false, fmt.Errorf("%w: control=%d tenant=%d", ErrTaskRuntimeEpochChanged, controlEpoch, snapshot.Epoch)
	}
	return true, nil
}

// Resolver is the task application's request-scoped adapter over the generic
// tenant runtime resolver. It revalidates the durable task-domain epoch on
// every request. An epoch change closes and rebuilds the cached resource;
// legacy, ready, fenced, and malformed states fail closed.
type Resolver struct{ tenants tenantRuntimeResolver }

func NewResolver(tenants *tenantruntime.Resolver) (*Resolver, error) {
	return newResolver(tenants)
}

func newResolver(tenants tenantRuntimeResolver) (*Resolver, error) {
	if tenants == nil {
		return nil, errors.New("tenant runtime resolver is required")
	}
	return &Resolver{tenants: tenants}, nil
}

func (r *Resolver) Resolve(ctx context.Context, workspaceID string) (taskapp.RuntimeSnapshot, error) {
	if r == nil || r.tenants == nil || workspaceID == "" {
		return taskapp.RuntimeSnapshot{}, taskapp.ErrInvalidRuntime
	}
	runtime, err := r.tenants.Resolve(ctx, workspaceID)
	if err != nil {
		return taskapp.RuntimeSnapshot{}, err
	}
	resource, ok := runtime.Resource.(interface {
		ApplicationSnapshot(context.Context) (taskapp.RuntimeSnapshot, error)
	})
	if !ok {
		r.tenants.Invalidate(workspaceID)
		return taskapp.RuntimeSnapshot{}, fmt.Errorf("%w: task model does not expose v2 application", tenantruntime.ErrRuntimeUnavailable)
	}
	application, err := resource.ApplicationSnapshot(ctx)
	if err == nil {
		return validateApplicationSnapshot(workspaceID, application)
	}
	r.tenants.Invalidate(workspaceID)
	if !errors.Is(err, ErrTaskRuntimeEpochChanged) {
		return taskapp.RuntimeSnapshot{}, err
	}

	// Re-resolve once after the durable epoch changed. The generic resolver's
	// per-workspace gate ensures concurrent requests converge on one rebuild.
	runtime, err = r.tenants.Resolve(ctx, workspaceID)
	if err != nil {
		return taskapp.RuntimeSnapshot{}, err
	}
	resource, ok = runtime.Resource.(interface {
		ApplicationSnapshot(context.Context) (taskapp.RuntimeSnapshot, error)
	})
	if !ok {
		r.tenants.Invalidate(workspaceID)
		return taskapp.RuntimeSnapshot{}, fmt.Errorf("%w: task model does not expose v2 application", tenantruntime.ErrRuntimeUnavailable)
	}
	application, err = resource.ApplicationSnapshot(ctx)
	if err != nil {
		r.tenants.Invalidate(workspaceID)
		return taskapp.RuntimeSnapshot{}, err
	}
	return validateApplicationSnapshot(workspaceID, application)
}

func validateApplicationSnapshot(workspaceID string, snapshot taskapp.RuntimeSnapshot) (taskapp.RuntimeSnapshot, error) {
	if snapshot.WorkspaceID != workspaceID || snapshot.Epoch < 1 || snapshot.Factory == nil || snapshot.Tasks == nil ||
		snapshot.Occurrences == nil || snapshot.Projects == nil || snapshot.Schedules == nil || snapshot.Reader == nil {
		return taskapp.RuntimeSnapshot{}, fmt.Errorf("%w: incomplete application snapshot", ErrTaskRuntimeType)
	}
	return snapshot, nil
}

var _ taskapp.RuntimeResolver = (*Resolver)(nil)
var _ taskdomain.GenerationRuntimeResolver = (*Resolver)(nil)
