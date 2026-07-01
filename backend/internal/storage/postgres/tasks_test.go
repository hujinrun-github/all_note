package postgres

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/provisioning"
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
	workspaceID := "workspace_task_roadmap_sync"
	ctx := scopedPostgresTestContext(t, opened, workspaceID)
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO learning_roadmaps (id, project_id, workspace_id, title, goal, status, created_at, updated_at)
		VALUES ('roadmap-task-sync', 'personal', $1, 'Roadmap', '', 'active', now(), now())
	`, workspaceID); err != nil {
		t.Fatalf("insert roadmap: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO roadmap_nodes (id, roadmap_id, workspace_id, title, status, created_at, updated_at)
		VALUES ('node-task-sync', 'roadmap-task-sync', $1, 'Node', 'todo', now(), now())
	`, workspaceID); err != nil {
		t.Fatalf("insert roadmap node: %v", err)
	}

	nodeID := "node-task-sync"
	task := &model.Task{Title: "linked task", Status: "open", Horizon: "week", Scope: "daily", RoadmapNodeID: &nodeID}
	if err := opened.Tasks().Create(ctx, task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	done := 1
	if _, err := opened.Tasks().Update(ctx, task.ID, &model.UpdateTaskRequest{Done: &done}); err != nil {
		t.Fatalf("update task done: %v", err)
	}
	assertPostgresRoadmapNodeStatus(t, store, nodeID, "done")

	openStatus := "open"
	if _, err := opened.Tasks().Update(ctx, task.ID, &model.UpdateTaskRequest{Status: &openStatus}); err != nil {
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
	ctx := scopedPostgresTestContext(t, opened, "workspace_task_blank_execution_type")
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
	workspaceID, _ := auth.WorkspaceIDFromContext(ctx)
	if _, err := store.db.ExecContext(ctx, `UPDATE tasks SET execution_type = '' WHERE workspace_id = $1 AND id = $2`, workspaceID, task.ID); err != nil {
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

func TestDeleteProjectAfterAuthFinalizerReassignsTasksToPersonal(t *testing.T) {
	schema := fmt.Sprintf("fs_test_task_delete_project_finalized_%d", time.Now().UnixNano())
	db := openPostgresTestDB(t, schema)
	defer db.Close()

	ctx := auth.ContextWithWorkspaceScope(context.Background(), "workspace_task_delete_project")
	seedFinalizedWorkspace(t, ctx, db, "user_task_delete_project", "workspace_task_delete_project")
	if _, err := db.ExecContext(ctx, `
		INSERT INTO task_projects (id, name, type, description, workspace_id)
		VALUES ('project_to_delete', 'Project To Delete', 'regular', '', 'workspace_task_delete_project');
		INSERT INTO tasks (id, title, content, project_id, workspace_id)
		VALUES ('task_on_deleted_project', 'Task On Deleted Project', '', 'project_to_delete', 'workspace_task_delete_project');
	`); err != nil {
		t.Fatalf("seed project task: %v", err)
	}
	if err := runMultiUserAuthFinalizer(ctx, db); err != nil {
		t.Fatalf("finalizer: %v", err)
	}

	store := newStore(db)
	if err := store.Tasks().DeleteProject(ctx, "project_to_delete"); err != nil {
		t.Fatalf("delete project: %v", err)
	}

	var projectID string
	if err := db.QueryRowContext(ctx, `
		SELECT project_id
		FROM tasks
		WHERE id = 'task_on_deleted_project'
	`).Scan(&projectID); err != nil {
		t.Fatalf("read reassigned task: %v", err)
	}
	if projectID != "personal" {
		t.Fatalf("task project_id = %q, want personal", projectID)
	}
	assertRowCount(t, db, `SELECT COUNT(*) FROM task_projects WHERE id = 'project_to_delete'`, 0)
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

func scopedPostgresTestContext(t *testing.T, store storage.Store, workspaceID string) context.Context {
	t.Helper()

	ctx := auth.ContextWithWorkspaceScope(context.Background(), workspaceID)
	userID := workspaceID + "_owner"
	if err := store.Transact(ctx, func(tx storage.Store) error {
		if err := tx.Auth().CreateUser(context.Background(), &model.User{
			ID:                 userID,
			Email:              fmt.Sprintf("%s@example.com", workspaceID),
			DisplayName:        workspaceID,
			PasswordHash:       "hash",
			MustChangePassword: false,
			DefaultWorkspaceID: workspaceID,
			Role:               "admin",
			Status:             "active",
		}); err != nil {
			return fmt.Errorf("create workspace user %s: %w", userID, err)
		}
		if err := tx.Auth().CreateWorkspace(context.Background(), &model.Workspace{
			ID:          workspaceID,
			Name:        workspaceID,
			OwnerUserID: userID,
		}); err != nil {
			return fmt.Errorf("create workspace %s: %w", workspaceID, err)
		}
		if err := tx.Auth().AddWorkspaceMember(context.Background(), workspaceID, userID, "owner"); err != nil {
			return fmt.Errorf("add workspace member %s: %w", workspaceID, err)
		}
		return provisioning.EnsureDefaultWorkspaceData(ctx, tx)
	}); err != nil {
		t.Fatalf("seed workspace %s: %v", workspaceID, err)
	}
	return ctx
}

func postgresTaskListContains(tasks []model.Task, id string) bool {
	for _, task := range tasks {
		if task.ID == id {
			return true
		}
	}
	return false
}
