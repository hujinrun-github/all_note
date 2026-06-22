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

	if _, _, err := GetTasks(context.Background(), store, "", "all", "", "", "", "", "recurring", 1, 20); err != nil {
		t.Fatalf("get tasks: %v", err)
	}

	if repo.filter.ExecutionType != "recurring" {
		t.Fatalf("ExecutionType = %q, want recurring", repo.filter.ExecutionType)
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
