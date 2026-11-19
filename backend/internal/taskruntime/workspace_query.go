package taskruntime

import (
	"context"
	"fmt"

	"github.com/hujinrun/flowspace/internal/workspacequery"
)

// ResolveWorkspaceQuery shares the same durable workspace resolution as task
// commands. It exposes only a read capability and never chooses a default
// content store or changes an existing module's authoritative data source.
func (r *Resolver) ResolveWorkspaceQuery(ctx context.Context, workspaceID string) (workspacequery.Runtime, error) {
	application, err := r.Resolve(ctx, workspaceID)
	if err != nil {
		return workspacequery.Runtime{}, fmt.Errorf("%w: %w", workspacequery.ErrRuntimeUnavailable, err)
	}
	reader, ok := application.Reader.(workspacequery.Reader)
	if !ok || reader == nil {
		return workspacequery.Runtime{}, fmt.Errorf("%w: workspace query capability is unavailable", workspacequery.ErrRuntimeUnavailable)
	}
	return workspacequery.Runtime{
		WorkspaceID: application.WorkspaceID,
		Epoch:       application.Epoch,
		Reader:      reader,
	}, nil
}
