package repository

import (
	"database/sql"
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
			updated_at INTEGER NOT NULL
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
