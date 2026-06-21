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
	if err != nil && isNoSuchColumnError(err, "is_default") {
		if _, alterErr := DB.Exec(`ALTER TABLE sync_targets ADD COLUMN is_default INTEGER NOT NULL DEFAULT 0`); alterErr != nil && !isDuplicateColumnError(alterErr) {
			return alterErr
		}
		_, err = DB.Exec(string(schema))
	}
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
	return RunLegacySQLiteMigrations(DB)
}

func RunLegacySQLiteMigrations(db *sql.DB) error {
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
			article_search_queries TEXT NOT NULL DEFAULT '[]',
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
		`ALTER TABLE tasks ADD COLUMN content TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE roadmap_nodes ADD COLUMN article_search_queries TEXT NOT NULL DEFAULT '[]'`,
		`ALTER TABLE sync_targets ADD COLUMN config_json TEXT NOT NULL DEFAULT '{}'`,
		`ALTER TABLE sync_targets ADD COLUMN is_default INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE note_sync_state ADD COLUMN external_id TEXT`,
		`ALTER TABLE note_sync_state ADD COLUMN external_url TEXT`,
		`ALTER TABLE note_sync_state ADD COLUMN external_hash TEXT`,
		`ALTER TABLE note_sync_state ADD COLUMN external_mtime INTEGER`,
		`ALTER TABLE note_sync_state ADD COLUMN last_direction TEXT`,
			`ALTER TABLE tasks ADD COLUMN execution_type TEXT NOT NULL DEFAULT 'single'`,
	}
	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil && !isDuplicateColumnError(err) {
			return err
		}
	}
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS note_project_links (
			note_id TEXT NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
			project_id TEXT NOT NULL REFERENCES task_projects(id) ON DELETE CASCADE,
			created_at INTEGER NOT NULL,
			PRIMARY KEY (note_id, project_id)
		);
		CREATE INDEX IF NOT EXISTS note_project_links_project_note_idx
			ON note_project_links (project_id, note_id);
	`)
	if err != nil {
		return fmt.Errorf("create note_project_links table: %w", err)
	}
	if _, err := db.Exec(`
		CREATE UNIQUE INDEX IF NOT EXISTS sync_targets_one_default_per_type_idx
			ON sync_targets (type) WHERE is_default = 1
	`); err != nil {
		return fmt.Errorf("create sync target default index: %w", err)
	}
	if err := backfillSingleDefaultSyncTargets(db); err != nil {
		return err
	}
	if _, err := db.Exec(`
		UPDATE note_sync_state
		SET external_hash = content_hash
		WHERE status = 'synced'
			AND (external_hash IS NULL OR TRIM(external_hash) = '')
	`); err != nil {
		return err
	}
	_, err = db.Exec(`ALTER TABLE tasks ADD COLUMN completed_at INTEGER`)
	if err != nil && !isDuplicateColumnError(err) {
		return fmt.Errorf("add completed_at column: %w", err)
	}
	if err == nil {
		_, err = db.Exec(`UPDATE tasks SET completed_at = updated_at WHERE done = 1 AND completed_at IS NULL`)
		if err != nil {
			return fmt.Errorf("backfill completed_at: %w", err)
		}
	}
	return migrateTaskProjectsWithDB(db)
}

func backfillSingleDefaultSyncTargets(db *sql.DB) error {
	_, err := db.Exec(`
		UPDATE sync_targets
		SET is_default = 1
		WHERE enabled = 1
			AND is_default = 0
			AND NOT EXISTS (
				SELECT 1
				FROM sync_targets existing_default
				WHERE existing_default.type = sync_targets.type
					AND existing_default.is_default = 1
			)
			AND (
				SELECT COUNT(*)
				FROM sync_targets enabled_target
				WHERE enabled_target.type = sync_targets.type
					AND enabled_target.enabled = 1
			) = 1
	`)
	if err != nil {
		return fmt.Errorf("backfill single default sync target: %w", err)
	}
	return nil
}

func isDuplicateColumnError(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "duplicate column name:")
}

func isNoSuchColumnError(err error, column string) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "no such column: "+strings.ToLower(column))
}

func migrateTaskProjects() error {
	return migrateTaskProjectsWithDB(DB)
}

func migrateTaskProjectsWithDB(db *sql.DB) error {
	now := nowUnix()
	if _, err := db.Exec(`
		INSERT OR IGNORE INTO task_projects (id, name, type, description, created_at, updated_at)
		VALUES ('personal', '个人', 'personal', '默认个人任务项目', ?, ?)
	`, now, now); err != nil {
		return err
	}

	rows, err := db.Query(`
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
		id, err := ensureTaskProjectByNameWithDB(db, name, "regular")
		if err != nil {
			return err
		}
		projects = append(projects, legacyProject{id: id, name: name})
	}

	for _, project := range projects {
		if _, err := db.Exec(`
			UPDATE tasks
			SET project_id = ?
			WHERE project IS NOT NULL AND TRIM(project) = ?
				AND (project_id IS NULL OR TRIM(project_id) = '')
		`, project.id, project.name); err != nil {
			return err
		}
	}

	_, err = db.Exec(`
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

func ensureTaskProjectByNameWithDB(db *sql.DB, name string, projectType string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "personal", nil
	}
	if trimmed == "个人" || strings.EqualFold(trimmed, "personal") {
		return "personal", nil
	}

	var id string
	err := db.QueryRow(`SELECT id FROM task_projects WHERE name = ?`, trimmed).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return "", err
	}

	projectType = normalizeProjectType(projectType)
	id = "project-" + newUUID()
	now := nowUnix()
	if _, err := db.Exec(`
		INSERT INTO task_projects (id, name, type, description, created_at, updated_at)
		VALUES (?, ?, ?, '', ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			type = excluded.type,
			updated_at = excluded.updated_at
	`, id, trimmed, projectType, now, now); err != nil {
		return "", err
	}

	err = db.QueryRow(`SELECT id FROM task_projects WHERE name = ?`, trimmed).Scan(&id)
	if err != nil {
		return "", err
	}
	return id, nil
}
