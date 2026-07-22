package taskruntime

import (
	"context"
	"errors"
	"fmt"

	"github.com/hujinrun/flowspace/internal/taskapp"
	"github.com/hujinrun/flowspace/internal/tenantruntime"
)

type taskModelResource interface {
	TaskDomainModel(context.Context) (taskapp.ModelVersion, error)
}

// DurableModelSelector is the narrow request-time routing adapter over the
// generic tenant runtime cache. It always revalidates the data-plane state;
// an error never implies the legacy model.
type DurableModelSelector struct{ tenants tenantRuntimeResolver }

func NewModelSelector(tenants *tenantruntime.Resolver) (*DurableModelSelector, error) {
	return newModelSelector(tenants)
}

func newModelSelector(tenants tenantRuntimeResolver) (*DurableModelSelector, error) {
	if tenants == nil {
		return nil, errors.New("tenant runtime resolver is required")
	}
	return &DurableModelSelector{tenants: tenants}, nil
}

func (s *DurableModelSelector) SelectTaskDomainModel(ctx context.Context, workspaceID string) (taskapp.ModelVersion, error) {
	if s == nil || s.tenants == nil || workspaceID == "" {
		return "", fmt.Errorf("%w: invalid task model selector request", tenantruntime.ErrRuntimeUnavailable)
	}
	model, err := s.selectOnce(ctx, workspaceID)
	if err == nil {
		return model, nil
	}
	s.tenants.Invalidate(workspaceID)
	if !errors.Is(err, ErrTaskRuntimeEpochChanged) && !errors.Is(err, ErrTaskRuntimeModelChanged) {
		return "", fmt.Errorf("%w: validate durable task model: %v", tenantruntime.ErrRuntimeUnavailable, err)
	}

	// A durable epoch/model transition invalidates the generic resource. Retry
	// once so stable cutover requests converge on the new cached resource.
	model, err = s.selectOnce(ctx, workspaceID)
	if err != nil {
		s.tenants.Invalidate(workspaceID)
		return "", fmt.Errorf("%w: rebuild durable task model: %v", tenantruntime.ErrRuntimeUnavailable, err)
	}
	return model, nil
}

func (s *DurableModelSelector) selectOnce(ctx context.Context, workspaceID string) (taskapp.ModelVersion, error) {
	runtime, err := s.tenants.Resolve(ctx, workspaceID)
	if err != nil {
		return "", err
	}
	resource, ok := runtime.Resource.(taskModelResource)
	if !ok {
		return "", ErrTaskRuntimeType
	}
	model, err := resource.TaskDomainModel(ctx)
	if err != nil {
		return "", err
	}
	if model != taskapp.ModelLegacy && model != taskapp.ModelV2 {
		return "", ErrTaskRuntimeType
	}
	return model, nil
}

var _ taskapp.ModelSelector = (*DurableModelSelector)(nil)
