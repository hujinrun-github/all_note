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
