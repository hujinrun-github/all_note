package repository

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/model"
	_ "modernc.org/sqlite"
)

func TestTaskProjectsIncludeDefaultPersonalProject(t *testing.T) {
	openTestDB(t)

	projects, err := ListTaskProjects()
	if err != nil {
		t.Fatalf("list task projects: %v", err)
	}

	if len(projects) == 0 {
		t.Fatal("expected at least the default personal project")
	}
	if projects[0].ID != "personal" || projects[0].Name != "个人" || projects[0].Type != "personal" {
		t.Fatalf("unexpected default project: %+v", projects[0])
	}
}

func TestCreateTaskDefaultsToPersonalWeekTask(t *testing.T) {
	openTestDB(t)

	task := &model.Task{Title: "写周复盘"}
	if err := CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	got, err := GetTaskByID(task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}

	if got.ProjectID == nil || *got.ProjectID != "personal" {
		t.Fatalf("project_id = %v, want personal", got.ProjectID)
	}
	if got.Project == nil || *got.Project != "个人" {
		t.Fatalf("project = %v, want 个人", got.Project)
	}
	if got.Horizon != "week" {
		t.Fatalf("horizon = %q, want week", got.Horizon)
	}
	if got.PlannedDate == nil || *got.PlannedDate != time.Now().Format("2006-01-02") {
		t.Fatalf("planned_date = %v, want today", got.PlannedDate)
	}
	if got.Status != "open" || got.Done != 0 {
		t.Fatalf("status/done = %q/%d, want open/0", got.Status, got.Done)
	}
}

func TestUpdateTaskAcceptsLongTaskTrackingStatuses(t *testing.T) {
	openTestDB(t)

	task := &model.Task{Title: "推进长期任务", Horizon: "long"}
	if err := CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	for _, status := range []string{"active", "blocked"} {
		got, err := UpdateTask(task.ID, &model.UpdateTaskRequest{Status: &status})
		if err != nil {
			t.Fatalf("update task status %q: %v", status, err)
		}
		if got.Status != status {
			t.Fatalf("status = %q, want %q", got.Status, status)
		}
		if got.Done != 0 {
			t.Fatalf("done = %d, want 0 for status %q", got.Done, status)
		}
	}
}

func TestTaskContentCanBeCreatedAndUpdated(t *testing.T) {
	openTestDB(t)

	task := &model.Task{
		Title:   "拆解 Roadmap 节点",
		Content: "1. 阅读系统设计资料\n2. 输出关键问题清单",
	}
	if err := CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	got, err := GetTaskByID(task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if got.Content != "1. 阅读系统设计资料\n2. 输出关键问题清单" {
		t.Fatalf("content = %q", got.Content)
	}

	updatedContent := "1. 完成容器基础复盘\n2. 记录 Kubernetes 调度问题"
	updated, err := UpdateTask(task.ID, &model.UpdateTaskRequest{Content: &updatedContent})
	if err != nil {
		t.Fatalf("update task content: %v", err)
	}
	if updated.Content != updatedContent {
		t.Fatalf("updated content = %q, want %q", updated.Content, updatedContent)
	}
}

func TestDeleteTaskProjectReassignsTasksToPersonal(t *testing.T) {
	openTestDB(t)

	project, err := CreateTaskProject(&model.CreateTaskProjectRequest{Name: "短期项目", Type: "regular"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	task := &model.Task{Title: "项目任务", ProjectID: &project.ID}
	if err := CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	if err := DeleteTaskProject(project.ID); err != nil {
		t.Fatalf("delete project: %v", err)
	}

	if _, err := GetTaskProjectByID(project.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted project lookup error = %v, want sql.ErrNoRows", err)
	}
	got, err := GetTaskByID(task.ID)
	if err != nil {
		t.Fatalf("get task after project delete: %v", err)
	}
	if got.ProjectID == nil || *got.ProjectID != "personal" {
		t.Fatalf("task project_id = %v, want personal", got.ProjectID)
	}
	if got.Project == nil || *got.Project != "个人" {
		t.Fatalf("task project = %v, want 个人", got.Project)
	}
}

func TestDeleteTaskProjectRefusesPersonalProject(t *testing.T) {
	openTestDB(t)

	if err := DeleteTaskProject("personal"); err == nil {
		t.Fatal("expected deleting personal project to fail")
	}

	if _, err := GetTaskProjectByID("personal"); err != nil {
		t.Fatalf("personal project should remain: %v", err)
	}
}

func TestGetTasksOrdersNewestPlannedTasksFirst(t *testing.T) {
	openTestDB(t)

	olderDate := "2026-06-11"
	newerDate := "2026-06-13"
	older := &model.Task{Title: "older planned task", PlannedDate: &olderDate, Horizon: "week"}
	if err := CreateTask(older); err != nil {
		t.Fatalf("create older task: %v", err)
	}
	newer := &model.Task{Title: "newest planned task", PlannedDate: &newerDate, Horizon: "week"}
	if err := CreateTask(newer); err != nil {
		t.Fatalf("create newer task: %v", err)
	}

	tasks, _, err := GetTasks("", "all", "", "week", "", "", 1, 20)
	if err != nil {
		t.Fatalf("get tasks: %v", err)
	}
	if len(tasks) < 2 {
		t.Fatalf("expected at least 2 tasks, got %d", len(tasks))
	}
	if tasks[0].ID != newer.ID {
		t.Fatalf("first task = %q, want newest planned task %q", tasks[0].Title, newer.Title)
	}
}

func TestGetTodayTasksIncludesActiveLongTasks(t *testing.T) {
	openTestDB(t)

	active := &model.Task{Title: "持续推进长期任务", Horizon: "long", Status: "active"}
	if err := CreateTask(active); err != nil {
		t.Fatalf("create active long task: %v", err)
	}
	oldDue := time.Date(2026, 6, 12, 0, 0, 0, 0, time.Local).Unix()
	activeWithOldDue := &model.Task{Title: "带旧日期的进行中长期任务", Horizon: "long", Status: "active", Due: &oldDue}
	if err := CreateTask(activeWithOldDue); err != nil {
		t.Fatalf("create active long task with old due: %v", err)
	}
	blocked := &model.Task{Title: "阻塞的长期任务", Horizon: "long", Status: "blocked"}
	if err := CreateTask(blocked); err != nil {
		t.Fatalf("create blocked long task: %v", err)
	}
	open := &model.Task{Title: "未开始长期任务", Horizon: "long", Status: "open"}
	if err := CreateTask(open); err != nil {
		t.Fatalf("create open long task: %v", err)
	}
	done := &model.Task{Title: "完成的长期任务", Horizon: "long", Status: "done"}
	if err := CreateTask(done); err != nil {
		t.Fatalf("create done long task: %v", err)
	}

	todayStart := time.Date(2026, 6, 15, 0, 0, 0, 0, time.Local).Unix()
	todayEnd := time.Date(2026, 6, 16, 0, 0, 0, 0, time.Local).Unix()
	overdueCutoff := time.Date(2026, 6, 8, 0, 0, 0, 0, time.Local).Unix()
	todayTasks, overdueTasks, err := GetTodayTasks(todayStart, todayEnd, overdueCutoff)
	if err != nil {
		t.Fatalf("get today tasks: %v", err)
	}

	if !taskListContains(todayTasks, active.ID) {
		t.Fatalf("expected active long task in today tasks, got %+v", todayTasks)
	}
	if !taskListContains(todayTasks, activeWithOldDue.ID) {
		t.Fatalf("expected active long task with old due in today tasks, got %+v", todayTasks)
	}
	if taskListContains(overdueTasks, activeWithOldDue.ID) {
		t.Fatalf("did not expect active long task with old due in overdue tasks")
	}
	for _, task := range []*model.Task{blocked, open, done} {
		if taskListContains(todayTasks, task.ID) {
			t.Fatalf("did not expect %q in today tasks", task.Title)
		}
		if taskListContains(overdueTasks, task.ID) {
			t.Fatalf("did not expect %q in overdue tasks", task.Title)
		}
	}
}

func TestGetTodayTasksIncludesTasksPlannedForTodayWithoutDue(t *testing.T) {
	openTestDB(t)

	today := "2026-06-15"
	plannedToday := &model.Task{Title: "只设置计划日期的今日任务", PlannedDate: &today, Horizon: "week"}
	if err := CreateTask(plannedToday); err != nil {
		t.Fatalf("create planned today task: %v", err)
	}
	otherDay := "2026-06-16"
	plannedOtherDay := &model.Task{Title: "明天的计划任务", PlannedDate: &otherDay, Horizon: "week"}
	if err := CreateTask(plannedOtherDay); err != nil {
		t.Fatalf("create planned other day task: %v", err)
	}

	todayStart := time.Date(2026, 6, 15, 0, 0, 0, 0, time.Local).Unix()
	todayEnd := time.Date(2026, 6, 16, 0, 0, 0, 0, time.Local).Unix()
	overdueCutoff := time.Date(2026, 6, 8, 0, 0, 0, 0, time.Local).Unix()
	todayTasks, overdueTasks, err := GetTodayTasks(todayStart, todayEnd, overdueCutoff)
	if err != nil {
		t.Fatalf("get today tasks: %v", err)
	}

	if !taskListContains(todayTasks, plannedToday.ID) {
		t.Fatalf("expected task planned for today in today tasks, got %+v", todayTasks)
	}
	if taskListContains(todayTasks, plannedOtherDay.ID) {
		t.Fatalf("did not expect task planned for another day in today tasks")
	}
	if taskListContains(overdueTasks, plannedToday.ID) {
		t.Fatalf("did not expect task planned for today in overdue tasks")
	}
}

func TestGetTodayTasksIncludesOverdueTasksPlannedWithoutDue(t *testing.T) {
	openTestDB(t)

	yesterday := "2026-06-14"
	plannedYesterday := &model.Task{Title: "昨天计划但未完成", PlannedDate: &yesterday, Horizon: "week"}
	if err := CreateTask(plannedYesterday); err != nil {
		t.Fatalf("create planned yesterday task: %v", err)
	}
	tooOldDate := "2026-06-07"
	plannedTooOld := &model.Task{Title: "太久以前的计划任务", PlannedDate: &tooOldDate, Horizon: "week"}
	if err := CreateTask(plannedTooOld); err != nil {
		t.Fatalf("create planned too old task: %v", err)
	}

	todayStart := time.Date(2026, 6, 15, 0, 0, 0, 0, time.Local).Unix()
	todayEnd := time.Date(2026, 6, 16, 0, 0, 0, 0, time.Local).Unix()
	overdueCutoff := time.Date(2026, 6, 8, 0, 0, 0, 0, time.Local).Unix()
	todayTasks, overdueTasks, err := GetTodayTasks(todayStart, todayEnd, overdueCutoff)
	if err != nil {
		t.Fatalf("get today tasks: %v", err)
	}

	if taskListContains(todayTasks, plannedYesterday.ID) {
		t.Fatalf("did not expect yesterday task in today tasks")
	}
	if !taskListContains(overdueTasks, plannedYesterday.ID) {
		t.Fatalf("expected yesterday planned task in overdue tasks, got %+v", overdueTasks)
	}
	if taskListContains(overdueTasks, plannedTooOld.ID) {
		t.Fatalf("did not expect task older than overdue window in overdue tasks")
	}
}

func TestInitDBMigratesLegacyTaskProjects(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "flowspace.db")
	createOldTaskDB(t, dbPath)
	chdirBackendRoot(t)
	t.Cleanup(func() {
		if DB != nil {
			DB.Close()
			DB = nil
		}
	})

	if err := InitDB(dbPath); err != nil {
		t.Fatalf("init db: %v", err)
	}

	projects, err := ListTaskProjects()
	if err != nil {
		t.Fatalf("list task projects: %v", err)
	}
	projectByName := map[string]model.TaskProject{}
	for _, project := range projects {
		projectByName[project.Name] = project
	}
	if projectByName["个人"].ID != "personal" {
		t.Fatalf("expected migrated default personal project, got %+v", projectByName["个人"])
	}
	if projectByName["客户项目"].Type != "regular" {
		t.Fatalf("legacy project type = %q, want regular", projectByName["客户项目"].Type)
	}

	tasks, _, err := GetTasks("", "all", "", "", "", "", 1, 20)
	if err != nil {
		t.Fatalf("get tasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected 2 migrated tasks, got %d", len(tasks))
	}

	for _, task := range tasks {
		if task.ProjectID == nil || *task.ProjectID == "" {
			t.Fatalf("task %q missing project_id after migration: %+v", task.Title, task)
		}
		if task.Status == "" || task.Horizon == "" {
			t.Fatalf("task %q missing status/horizon after migration: %+v", task.Title, task)
		}
	}
}

func createOldTaskDB(t *testing.T, dbPath string) {
	t.Helper()

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open old task db: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`
		CREATE TABLE folders (
			id TEXT PRIMARY KEY,
			name TEXT UNIQUE NOT NULL,
			sort_order REAL NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL
		);
		CREATE TABLE notes (
			rowid INTEGER PRIMARY KEY AUTOINCREMENT,
			id TEXT UNIQUE NOT NULL,
			title TEXT NOT NULL,
			body TEXT NOT NULL DEFAULT '',
			folder_id TEXT NOT NULL DEFAULT '__uncategorized' REFERENCES folders(id),
			tags TEXT NOT NULL DEFAULT '[]',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		);
		CREATE TABLE tasks (
			rowid INTEGER PRIMARY KEY AUTOINCREMENT,
			id TEXT UNIQUE NOT NULL,
			title TEXT NOT NULL,
			project TEXT,
			due INTEGER,
			priority INTEGER NOT NULL DEFAULT 0,
			done INTEGER NOT NULL DEFAULT 0,
			scope TEXT NOT NULL DEFAULT 'daily',
			sort_order REAL NOT NULL DEFAULT 0,
			note_id TEXT REFERENCES notes(id) ON DELETE SET NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			completed_at INTEGER
		);
		CREATE VIRTUAL TABLE tasks_fts USING fts5(
			title, content='tasks', content_rowid='rowid'
		);
		CREATE TRIGGER tasks_ai AFTER INSERT ON tasks BEGIN
			INSERT INTO tasks_fts(rowid, title) VALUES (new.rowid, new.title);
		END;
		CREATE TRIGGER tasks_ad AFTER DELETE ON tasks BEGIN
			INSERT INTO tasks_fts(tasks_fts, rowid, title) VALUES ('delete', old.rowid, old.title);
		END;
		CREATE TRIGGER tasks_au AFTER UPDATE ON tasks BEGIN
			INSERT INTO tasks_fts(tasks_fts, rowid, title) VALUES ('delete', old.rowid, old.title);
			INSERT INTO tasks_fts(rowid, title) VALUES (new.rowid, new.title);
		END;
	`); err != nil {
		t.Fatalf("create old task schema: %v", err)
	}

	now := time.Now().Unix()
	if _, err := db.Exec(`
		INSERT INTO tasks (id, title, project, due, priority, done, scope, sort_order, created_at, updated_at)
		VALUES
			('legacy-1', '旧客户任务', '客户项目', ?, 0, 0, 'daily', 0, ?, ?),
			('legacy-2', '无项目旧任务', NULL, NULL, 0, 1, 'yearly', 0, ?, ?)
	`, now, now, now, now, now); err != nil {
		t.Fatalf("insert legacy tasks: %v", err)
	}

	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("expected db file to exist: %v", err)
	}
}

func taskListContains(tasks []model.Task, id string) bool {
	for _, task := range tasks {
		if task.ID == id {
			return true
		}
	}
	return false
}
