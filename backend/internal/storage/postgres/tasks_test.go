package postgres

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

func TestTaskUpdateSyncsRoadmapNodeStatus(t *testing.T) {
	schema := fmt.Sprintf("fs_test_task_roadmap_sync_%d", time.Now().UnixNano())
	opened, err := (Provider{}).Open(context.Background(), storage.Config{
		Env:    "test",
		Driver: storage.DriverPostgres,
		URL:    createPostgresTestSchema(t, schema),
	})
	if err != nil {
		t.Fatalf("open postgres store: %v", err)
	}
	defer opened.Close()

	store := opened.(*store)
	if _, err := store.db.Exec(`
		INSERT INTO learning_roadmaps (id, project_id, title, goal, status, created_at, updated_at)
		VALUES ('roadmap-task-sync', 'personal', 'Roadmap', '', 'active', now(), now())
	`); err != nil {
		t.Fatalf("insert roadmap: %v", err)
	}
	if _, err := store.db.Exec(`
		INSERT INTO roadmap_nodes (id, roadmap_id, title, status, created_at, updated_at)
		VALUES ('node-task-sync', 'roadmap-task-sync', 'Node', 'todo', now(), now())
	`); err != nil {
		t.Fatalf("insert roadmap node: %v", err)
	}

	nodeID := "node-task-sync"
	task := &model.Task{Title: "linked task", Status: "open", Horizon: "week", Scope: "daily", RoadmapNodeID: &nodeID}
	if err := opened.Tasks().Create(context.Background(), task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	done := 1
	if _, err := opened.Tasks().Update(context.Background(), task.ID, &model.UpdateTaskRequest{Done: &done}); err != nil {
		t.Fatalf("update task done: %v", err)
	}
	assertPostgresRoadmapNodeStatus(t, store, nodeID, "done")

	openStatus := "open"
	if _, err := opened.Tasks().Update(context.Background(), task.ID, &model.UpdateTaskRequest{Status: &openStatus}); err != nil {
		t.Fatalf("update task open: %v", err)
	}
	assertPostgresRoadmapNodeStatus(t, store, nodeID, "active")
}

func TestTaskFiltersTreatLegacyBlankExecutionTypeAsSingle(t *testing.T) {
	schema := fmt.Sprintf("fs_test_task_blank_execution_type_%d", time.Now().UnixNano())
	opened, err := (Provider{}).Open(context.Background(), storage.Config{
		Env:    "test",
		Driver: storage.DriverPostgres,
		URL:    createPostgresTestSchema(t, schema),
	})
	if err != nil {
		t.Fatalf("open postgres store: %v", err)
	}
	defer opened.Close()

	store := opened.(*store)
	ctx := context.Background()
	if _, err := store.db.ExecContext(ctx, `ALTER TABLE tasks DROP CONSTRAINT IF EXISTS tasks_execution_type_check`); err != nil {
		t.Fatalf("drop execution type check for legacy fixture: %v", err)
	}
	localDay := time.Date(2026, 6, 16, 0, 0, 0, 0, time.Local)
	todayStart := localDay.Unix()
	todayEnd := localDay.Add(24 * time.Hour).Unix()
	dueAt := localDay.Add(9 * time.Hour).Unix()
	plannedDate := "2026-06-16"
	task := &model.Task{
		Title:       "legacy blank execution type",
		Due:         &dueAt,
		PlannedDate: &plannedDate,
		Status:      "open",
		Horizon:     "week",
		Scope:       "daily",
	}
	if err := opened.Tasks().Create(ctx, task); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE tasks SET execution_type = '' WHERE id = $1`, task.ID); err != nil {
		t.Fatalf("write legacy blank execution type: %v", err)
	}

	tasks, total, err := opened.Tasks().List(ctx, storage.TaskFilter{
		Status:      "active",
		PlannedDate: plannedDate,
		Page:        1,
		PageSize:    20,
	})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if total != 1 || len(tasks) != 1 || tasks[0].ID != task.ID {
		t.Fatalf("expected legacy blank execution type task in default list, total=%d tasks=%+v", total, tasks)
	}

	todayTasks, overdueTasks, err := opened.Tasks().Today(ctx, todayStart, todayEnd, todayStart)
	if err != nil {
		t.Fatalf("today tasks: %v", err)
	}
	if len(overdueTasks) != 0 {
		t.Fatalf("expected no overdue tasks, got %+v", overdueTasks)
	}
	if !postgresTaskListContains(todayTasks, task.ID) {
		t.Fatalf("expected legacy blank execution type task in today tasks, got %+v", todayTasks)
	}
}

func assertPostgresRoadmapNodeStatus(t *testing.T, store *store, nodeID, want string) {
	t.Helper()

	var got string
	if err := store.db.QueryRow(`SELECT status FROM roadmap_nodes WHERE id = $1`, nodeID).Scan(&got); err != nil {
		t.Fatalf("query roadmap node status: %v", err)
	}
	if got != want {
		t.Fatalf("roadmap node status = %q, want %q", got, want)
	}
}

func postgresTaskListContains(tasks []model.Task, id string) bool {
	for _, task := range tasks {
		if task.ID == id {
			return true
		}
	}
	return false
}
