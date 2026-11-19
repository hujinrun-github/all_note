package main

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/taskapp"
)

type planningWorkspaceIDs []string

func (s planningWorkspaceIDs) ListActiveWorkspaceIDs(context.Context) ([]string, error) {
	return []string(s), nil
}

type planningWorkspaceModels struct {
	failure error
	t       *testing.T
}

func (s planningWorkspaceModels) SelectTaskDomainModel(ctx context.Context, id string) (taskapp.ModelVersion, error) {
	if workspace, err := auth.WorkspaceIDFromContext(ctx); err != nil || workspace != id {
		s.t.Errorf("worker workspace scope=%q %v", workspace, err)
	}
	switch id {
	case "legacy":
		return taskapp.ModelLegacy, nil
	case "broken":
		return "", s.failure
	default:
		return taskapp.ModelV2, nil
	}
}
func TestPlanningWorkerScansStableV2AndPreservesPartialProgress(t *testing.T) {
	failure := errors.New("runtime unavailable")
	source := v2PlanningWorkspaces{source: planningWorkspaceIDs{"v2-one", "legacy", "broken", "v2-two"}, models: planningWorkspaceModels{failure, t}}
	got, err := source.ListActiveWorkspaceIDs(context.Background())
	if !reflect.DeepEqual(got, []string{"v2-one", "v2-two"}) || !errors.Is(err, failure) {
		t.Fatalf("workspaces=%v error=%v", got, err)
	}
}
