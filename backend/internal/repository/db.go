package repository

import (
	"database/sql"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var DB *sql.DB

func InitDB(dbPath string) error {
	var err error
	DB, err = sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=ON")
	if err != nil {
		return err
	}
	DB.SetMaxOpenConns(1)

	schema, err := os.ReadFile("db/schema.sql")
	if err != nil {
		return err
	}
	_, err = DB.Exec(string(schema))
	if err != nil {
		return err
	}
	return migrateDB()
}

func SeedDB() error {
	seed, err := os.ReadFile("db/seed.sql")
	if err != nil {
		return err
	}
	_, err = DB.Exec(string(seed))
	return err
}

func newUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func nowUnix() int64 {
	return time.Now().Unix()
}

func migrateDB() error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS task_projects (
			id TEXT PRIMARY KEY,
			name TEXT UNIQUE NOT NULL,
			type TEXT NOT NULL DEFAULT 'regular',
			description TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS learning_roadmaps (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL UNIQUE REFERENCES task_projects(id) ON DELETE CASCADE,
			title TEXT NOT NULL,
			goal TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'draft',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS roadmap_nodes (
			id TEXT PRIMARY KEY,
			roadmap_id TEXT NOT NULL REFERENCES learning_roadmaps(id) ON DELETE CASCADE,
			parent_id TEXT REFERENCES roadmap_nodes(id) ON DELETE CASCADE,
			type TEXT NOT NULL DEFAULT 'task',
			title TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			path_type TEXT NOT NULL DEFAULT 'required',
			status TEXT NOT NULL DEFAULT 'todo',
			deliverable TEXT NOT NULL DEFAULT '',
			acceptance_criteria TEXT NOT NULL DEFAULT '',
			x REAL NOT NULL DEFAULT 0,
			y REAL NOT NULL DEFAULT 0,
			order_index INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS roadmap_edges (
			id TEXT PRIMARY KEY,
			roadmap_id TEXT NOT NULL REFERENCES learning_roadmaps(id) ON DELETE CASCADE,
			source_node_id TEXT NOT NULL REFERENCES roadmap_nodes(id) ON DELETE CASCADE,
			target_node_id TEXT NOT NULL REFERENCES roadmap_nodes(id) ON DELETE CASCADE,
			style TEXT NOT NULL DEFAULT 'solid',
			created_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS roadmap_resources (
			id TEXT PRIMARY KEY,
			node_id TEXT NOT NULL REFERENCES roadmap_nodes(id) ON DELETE CASCADE,
			title TEXT NOT NULL,
			url TEXT NOT NULL,
			summary TEXT NOT NULL DEFAULT '',
			source_type TEXT NOT NULL DEFAULT 'article',
			added_by TEXT NOT NULL DEFAULT 'user',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`ALTER TABLE tasks ADD COLUMN project_id TEXT`,
		`ALTER TABLE tasks ADD COLUMN planned_date TEXT`,
		`ALTER TABLE tasks ADD COLUMN status TEXT NOT NULL DEFAULT 'open'`,
		`ALTER TABLE tasks ADD COLUMN horizon TEXT NOT NULL DEFAULT 'week'`,
		`ALTER TABLE tasks ADD COLUMN roadmap_node_id TEXT`,
		`ALTER TABLE note_sync_state ADD COLUMN external_hash TEXT`,
		`ALTER TABLE note_sync_state ADD COLUMN external_mtime INTEGER`,
		`ALTER TABLE note_sync_state ADD COLUMN last_direction TEXT`,
	}
	for _, stmt := range statements {
		if _, err := DB.Exec(stmt); err != nil && !isDuplicateColumnError(err) {
			return err
		}
	}
	if _, err := DB.Exec(`
		UPDATE note_sync_state
		SET external_hash = content_hash
		WHERE status = 'synced'
			AND (external_hash IS NULL OR TRIM(external_hash) = '')
	`); err != nil {
		return err
	}
	return migrateTaskProjects()
}

func isDuplicateColumnError(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "duplicate column name:")
}

func migrateTaskProjects() error {
	now := nowUnix()
	if _, err := DB.Exec(`
		INSERT OR IGNORE INTO task_projects (id, name, type, description, created_at, updated_at)
		VALUES ('personal', '个人', 'personal', '默认个人任务项目', ?, ?)
	`, now, now); err != nil {
		return err
	}

	rows, err := DB.Query(`
		SELECT DISTINCT TRIM(project)
		FROM tasks
		WHERE project IS NOT NULL AND TRIM(project) <> ''
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	names := make([]string, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}

	type legacyProject struct {
		id   string
		name string
	}
	projects := make([]legacyProject, 0, len(names))
	for _, name := range names {
		if name == "个人" || strings.EqualFold(name, "personal") {
			projects = append(projects, legacyProject{id: "personal", name: name})
			continue
		}
		id, err := ensureTaskProjectByName(name, "regular")
		if err != nil {
			return err
		}
		projects = append(projects, legacyProject{id: id, name: name})
	}

	for _, project := range projects {
		if _, err := DB.Exec(`
			UPDATE tasks
			SET project_id = ?
			WHERE project IS NOT NULL AND TRIM(project) = ?
				AND (project_id IS NULL OR TRIM(project_id) = '')
		`, project.id, project.name); err != nil {
			return err
		}
	}

	_, err = DB.Exec(`
		UPDATE tasks
		SET
			project_id = COALESCE(NULLIF(TRIM(project_id), ''), 'personal'),
			status = CASE WHEN done = 1 THEN 'done' ELSE COALESCE(NULLIF(TRIM(status), ''), 'open') END,
			horizon = CASE
				WHEN COALESCE(NULLIF(TRIM(horizon), ''), '') <> '' THEN horizon
				WHEN scope IN ('monthly', 'yearly') THEN 'long'
				ELSE 'week'
			END,
			planned_date = COALESCE(planned_date, CASE WHEN due IS NOT NULL THEN date(due, 'unixepoch', 'localtime') ELSE date('now', 'localtime') END)
		WHERE project_id IS NULL
			OR TRIM(project_id) = ''
			OR status IS NULL
			OR TRIM(status) = ''
			OR horizon IS NULL
			OR TRIM(horizon) = ''
			OR planned_date IS NULL
	`)
	return err
}
