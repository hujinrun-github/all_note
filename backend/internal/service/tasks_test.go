package service

import (
	"context"
	"testing"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

func TestGetTasksPassesExecutionTypeFilter(t *testing.T) {
	repo := &captureTaskRepository{}
	store := &captureTaskStore{tasks: repo}

	if _, _, err := GetTasks(context.Background(), store, "", "all", "", "", "", "", "2026-07-01", "2026-07-31", "recurring", 1, 20); err != nil {
		t.Fatalf("get tasks: %v", err)
	}

	if repo.filter.ExecutionType != "recurring" {
		t.Fatalf("ExecutionType = %q, want recurring", repo.filter.ExecutionType)
	}
	if repo.filter.PlannedFrom != "2026-07-01" || repo.filter.PlannedTo != "2026-07-31" {
		t.Fatalf("planned range = %q..%q, want July 2026", repo.filter.PlannedFrom, repo.filter.PlannedTo)
	}
}

type captureTaskStore struct {
	storage.Store
	tasks storage.TaskRepository
}

func (s *captureTaskStore) Tasks() storage.TaskRepository {
	return s.tasks
}

type captureTaskRepository struct {
	storage.TaskRepository
	filter storage.TaskFilter
}

func (r *captureTaskRepository) List(_ context.Context, filter storage.TaskFilter) ([]model.Task, int, error) {
	r.filter = filter
	return nil, 0, nil
}
