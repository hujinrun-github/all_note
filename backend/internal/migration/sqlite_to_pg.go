package migration

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/storage/postgres"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/lib/pq"
	_ "modernc.org/sqlite"
)

func MigrateSQLiteToPostgres(sqlitePath, postgresURL string) error {
	ctx := context.Background()
	copyPath, cleanup, err := prepareSQLiteMigrationCopy(sqlitePath)
	if err != nil {
		return err
	}
	defer cleanup()

	sqliteDB, err := sql.Open("sqlite", copyPath+"?_foreign_keys=ON")
	if err != nil {
		return err
	}
	defer sqliteDB.Close()

	if err := runLegacySQLiteUpgrade(sqliteDB); err != nil {
		return err
	}
	if err := validateSQLiteSource(sqliteDB); err != nil {
		return err
	}

	pgDB, err := sql.Open("pgx", postgresURL)
	if err != nil {
		return err
	}
	defer pgDB.Close()

	if err := postgres.RunPostgresMigrationsContext(ctx, pgDB); err != nil {
		return err
	}
	if err := ensurePostgresMigrationTargetEmpty(pgDB); err != nil {
		return err
	}

	tx, err := pgDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	steps := []func(context.Context, *sql.DB, *sql.Tx) error{
		migrateFolders,
		migrateNotes,
		migrateTaskProjects,
		migrateNoteProjectLinks,
		migrateLearningRoadmaps,
		migrateRoadmapNodes,
		migrateTasks,
		migrateRoadmapEdges,
		migrateRoadmapResources,
		migrateEvents,
		migrateInbox,
		migrateSyncTargets,
		migrateNoteSyncBindings,
		migrateSyncExternalClaims,
		migrateNoteSyncSuppressions,
		migrateSyncImportTombstones,
		migrateSyncStates,
	}
	for _, step := range steps {
		if err := step(ctx, sqliteDB, tx); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func prepareSQLiteMigrationCopy(sqlitePath string) (string, func(), error) {
	if strings.TrimSpace(sqlitePath) == "" {
		return "", nil, fmt.Errorf("sqlite path is required")
	}
	source, err := sql.Open("sqlite", sqlitePath)
	if err != nil {
		return "", nil, err
	}
	defer source.Close()
	dir, err := os.MkdirTemp("", "flowspace-sqlite-migration-*")
	if err != nil {
		return "", nil, err
	}
	copyPath := filepath.Join(dir, "flowspace.copy.db")
	if _, err := source.Exec(`VACUUM INTO ?`, copyPath); err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, fmt.Errorf("create WAL-safe SQLite migration copy: %w", err)
	}
	return copyPath, func() { _ = os.RemoveAll(dir) }, nil
}

func runLegacySQLiteUpgrade(db *sql.DB) error {
	statements := []string{
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
	}
	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
			return err
		}
	}
	return nil
}

func validateSQLiteSource(db *sql.DB) error {
	checks := []struct {
		name string
		sql  string
	}{
		{"tasks.priority", `SELECT COUNT(*) FROM tasks WHERE priority < 0`},
		{"tasks.done", `SELECT COUNT(*) FROM tasks WHERE done NOT IN (0, 1)`},
		{"tasks.status", `SELECT COUNT(*) FROM tasks WHERE status NOT IN ('open','active','blocked','done','archived','migrated','cancelled')`},
		{"tasks.horizon", `SELECT COUNT(*) FROM tasks WHERE horizon NOT IN ('day','week','long')`},
		{"tasks.scope", `SELECT COUNT(*) FROM tasks WHERE scope NOT IN ('daily','weekly','monthly','yearly')`},
		{"events.time_range", `SELECT COUNT(*) FROM events WHERE end_time <= start_time`},
		{"sync_targets.type", `SELECT COUNT(*) FROM sync_targets WHERE type NOT IN ('obsidian','notion')`},
		{"note_sync_state.status", `SELECT COUNT(*) FROM note_sync_state WHERE status NOT IN ('synced','pending','failed','external_deleted')`},
		{"note_sync_state.last_direction", `SELECT COUNT(*) FROM note_sync_state WHERE last_direction IS NOT NULL AND last_direction NOT IN ('push','pull','import','restore','delete','delete_detected')`},
	}
	for _, check := range checks {
		var count int
		if err := db.QueryRow(check.sql).Scan(&count); err != nil {
			return fmt.Errorf("validate %s: %w", check.name, err)
		}
		if count > 0 {
			return fmt.Errorf("validate %s: %d invalid rows", check.name, count)
		}
	}
	if err := validateSQLiteJSONArrays(db, "notes", "tags"); err != nil {
		return err
	}
	if err := validateSQLiteJSONArrays(db, "roadmap_nodes", "article_search_queries"); err != nil {
		return err
	}
	if err := validateSQLiteSyncTargetConfig(db); err != nil {
		return err
	}
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		return fmt.Errorf("sqlite foreign_key_check failed")
	}
	return rows.Err()
}

func validateSQLiteJSONArrays(db *sql.DB, table, column string) error {
	rows, err := db.Query(`SELECT id, ` + pq.QuoteIdentifier(column) + ` FROM ` + pq.QuoteIdentifier(table))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return err
		}
		var values []string
		if err := json.Unmarshal([]byte(raw), &values); err != nil {
			return fmt.Errorf("%s.%s id=%s must be JSON string array: %w", table, column, id, err)
		}
	}
	return rows.Err()
}

func validateSQLiteSyncTargetConfig(db *sql.DB) error {
	rows, err := db.Query(`SELECT id, config_json FROM sync_targets`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return err
		}
		if _, err := normalizeJSONObject(raw); err != nil {
			return fmt.Errorf("sync_targets.config_json id=%s: %w", id, err)
		}
	}
	return rows.Err()
}

func ensurePostgresMigrationTargetEmpty(db *sql.DB) error {
	seedChecks := []struct {
		table string
		sql   string
	}{
		{"folders", `SELECT COUNT(*) FROM folders WHERE id NOT IN ('__uncategorized', '__work', '__personal')`},
		{"task_projects", `SELECT COUNT(*) FROM task_projects WHERE id <> 'personal'`},
	}
	for _, check := range seedChecks {
		var count int
		if err := db.QueryRow(check.sql).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return fmt.Errorf("postgres migration target table %s has non-seed rows", check.table)
		}
	}

	tables := []string{
		"notes", "tasks", "events", "inbox", "learning_roadmaps", "roadmap_nodes",
		"roadmap_edges", "roadmap_resources", "sync_targets", "note_sync_state",
		"note_sync_bindings", "sync_external_claims", "note_sync_suppressions",
		"sync_import_tombstones", "note_project_links", "search_index",
	}
	for _, table := range tables {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + pq.QuoteIdentifier(table)).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return fmt.Errorf("postgres migration target table %s is not empty", table)
		}
	}
	return nil
}

func migrateFolders(ctx context.Context, sqliteDB *sql.DB, tx *sql.Tx) error {
	rows, err := sqliteDB.Query(`SELECT id, name, sort_order, created_at FROM folders ORDER BY id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		var sortOrder float64
		var createdAt int64
		if err := rows.Scan(&id, &name, &sortOrder, &createdAt); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO folders (id, name, sort_order, created_at)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (id) DO UPDATE SET name = excluded.name, sort_order = excluded.sort_order, created_at = excluded.created_at
		`, id, name, sortOrder, unixToTime(createdAt)); err != nil {
			return err
		}
	}
	return rows.Err()
}

func migrateNotes(ctx context.Context, sqliteDB *sql.DB, tx *sql.Tx) error {
	rows, err := sqliteDB.Query(`SELECT id, title, body, folder_id, tags, created_at, updated_at FROM notes ORDER BY id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, title, body, folderID, rawTags string
		var createdAt, updatedAt int64
		if err := rows.Scan(&id, &title, &body, &folderID, &rawTags, &createdAt, &updatedAt); err != nil {
			return err
		}
		tags, err := jsonStringArray(rawTags)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO notes (id, title, body, folder_id, tags, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5::text[], $6, $7)
		`, id, title, body, folderID, pq.Array(tags), unixToTime(createdAt), unixToTime(updatedAt)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO search_index (entity_type, entity_id, title, content, tags, updated_at, search_vector)
			VALUES ('note', $1, $2, $3, $4::text[], $5, to_tsvector('simple', coalesce($2,'') || ' ' || coalesce($3,'') || ' ' || coalesce(array_to_string($4::text[], ' '), '')))
		`, id, title, body, pq.Array(tags), unixToTime(updatedAt)); err != nil {
			return err
		}
	}
	return rows.Err()
}

func migrateTaskProjects(ctx context.Context, sqliteDB *sql.DB, tx *sql.Tx) error {
	rows, err := sqliteDB.Query(`SELECT id, name, type, description, created_at, updated_at FROM task_projects ORDER BY id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, name, projectType, description string
		var createdAt, updatedAt int64
		if err := rows.Scan(&id, &name, &projectType, &description, &createdAt, &updatedAt); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO task_projects (id, name, type, description, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (id) DO UPDATE SET name = excluded.name, type = excluded.type, description = excluded.description, created_at = excluded.created_at, updated_at = excluded.updated_at
		`, id, name, projectType, description, unixToTime(createdAt), unixToTime(updatedAt)); err != nil {
			return err
		}
	}
	return rows.Err()
}

func migrateNoteProjectLinks(ctx context.Context, sqliteDB *sql.DB, pgTx *sql.Tx) error {
	rows, err := sqliteDB.QueryContext(ctx,
		`SELECT note_id, project_id, created_at FROM note_project_links`)
	if err != nil {
		// Table might not exist in legacy SQLite — that's OK
		return nil
	}
	defer rows.Close()
	for rows.Next() {
		var noteID, projectID string
		var createdAt int64
		if err := rows.Scan(&noteID, &projectID, &createdAt); err != nil {
			return fmt.Errorf("scan note_project_links: %w", err)
		}
		_, err := pgTx.ExecContext(ctx,
			`INSERT INTO note_project_links (note_id, project_id, created_at)
			 VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
			noteID, projectID, unixToTime(createdAt))
		if err != nil {
			return fmt.Errorf("insert note_project_links (%s, %s): %w", noteID, projectID, err)
		}
	}
	return rows.Err()
}

func migrateLearningRoadmaps(ctx context.Context, sqliteDB *sql.DB, tx *sql.Tx) error {
	rows, err := sqliteDB.Query(`SELECT id, project_id, title, goal, status, created_at, updated_at FROM learning_roadmaps ORDER BY id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, projectID, title, goal, status string
		var createdAt, updatedAt int64
		if err := rows.Scan(&id, &projectID, &title, &goal, &status, &createdAt, &updatedAt); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO learning_roadmaps (id, project_id, title, goal, status, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, id, projectID, title, goal, status, unixToTime(createdAt), unixToTime(updatedAt)); err != nil {
			return err
		}
	}
	return rows.Err()
}

func migrateRoadmapNodes(ctx context.Context, sqliteDB *sql.DB, tx *sql.Tx) error {
	rows, err := sqliteDB.Query(`
		SELECT id, roadmap_id, parent_id, type, title, description, path_type, status,
			deliverable, acceptance_criteria, x, y, order_index, article_search_queries, created_at, updated_at
		FROM roadmap_nodes ORDER BY roadmap_id, order_index
	`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, roadmapID, nodeType, title, description, pathType, status, deliverable, acceptanceCriteria, rawQueries string
		var parentID sql.NullString
		var x, y float64
		var orderIndex int
		var createdAt, updatedAt int64
		if err := rows.Scan(&id, &roadmapID, &parentID, &nodeType, &title, &description, &pathType, &status, &deliverable, &acceptanceCriteria, &x, &y, &orderIndex, &rawQueries, &createdAt, &updatedAt); err != nil {
			return err
		}
		queries, err := jsonStringArray(rawQueries)
		if err != nil {
			return err
		}
		position, err := json.Marshal(map[string]float64{"x": x, "y": y})
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO roadmap_nodes (
				id, roadmap_id, parent_id, type, title, description, path_type, status,
				deliverable, acceptance_criteria, position, order_index, article_search_queries, created_at, updated_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11::jsonb, $12, $13::text[], $14, $15)
		`, id, roadmapID, nullStringValue(parentID), nodeType, title, description, pathType, status, deliverable, acceptanceCriteria, string(position), orderIndex, pq.Array(queries), unixToTime(createdAt), unixToTime(updatedAt)); err != nil {
			return err
		}
	}
	return rows.Err()
}

func migrateTasks(ctx context.Context, sqliteDB *sql.DB, tx *sql.Tx) error {
	rows, err := sqliteDB.Query(`
		SELECT id, title, content, project, project_id, due, planned_date, priority, done, status, horizon, scope,
			sort_order, note_id, roadmap_node_id, created_at, updated_at
		FROM tasks ORDER BY id
	`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, title, content, status, horizon, scope string
		var project, projectID, plannedDate, noteID, roadmapNodeID sql.NullString
		var due sql.NullInt64
		var priority, done int
		var sortOrder float64
		var createdAt, updatedAt int64
		if err := rows.Scan(&id, &title, &content, &project, &projectID, &due, &plannedDate, &priority, &done, &status, &horizon, &scope, &sortOrder, &noteID, &roadmapNodeID, &createdAt, &updatedAt); err != nil {
			return err
		}
		if !projectID.Valid || strings.TrimSpace(projectID.String) == "" {
			projectID = sql.NullString{String: "personal", Valid: true}
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO tasks (
				id, title, content, project, project_id, due_at, planned_date, priority, done, status, horizon, scope,
				sort_order, note_id, roadmap_node_id, created_at, updated_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7::date, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
		`, id, title, content, nullStringValue(project), projectID.String, nullIntTime(due), nullStringValue(plannedDate), priority, done == 1, status, horizon, scope, sortOrder, nullStringValue(noteID), nullStringValue(roadmapNodeID), unixToTime(createdAt), unixToTime(updatedAt)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO search_index (entity_type, entity_id, title, content, tags, updated_at, search_vector)
			VALUES ('task', $1, $2, $3, '{}'::text[], $4, to_tsvector('simple', coalesce($2,'') || ' ' || coalesce($3,'')))
		`, id, title, content, unixToTime(updatedAt)); err != nil {
			return err
		}
	}
	return rows.Err()
}

func migrateRoadmapEdges(ctx context.Context, sqliteDB *sql.DB, tx *sql.Tx) error {
	rows, err := sqliteDB.Query(`SELECT id, roadmap_id, source_node_id, target_node_id, style, created_at FROM roadmap_edges ORDER BY id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, roadmapID, sourceNodeID, targetNodeID, style string
		var createdAt int64
		if err := rows.Scan(&id, &roadmapID, &sourceNodeID, &targetNodeID, &style, &createdAt); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO roadmap_edges (id, roadmap_id, source_node_id, target_node_id, style, created_at)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, id, roadmapID, sourceNodeID, targetNodeID, style, unixToTime(createdAt)); err != nil {
			return err
		}
	}
	return rows.Err()
}

func migrateRoadmapResources(ctx context.Context, sqliteDB *sql.DB, tx *sql.Tx) error {
	rows, err := sqliteDB.Query(`SELECT id, node_id, title, url, summary, source_type, added_by, created_at, updated_at FROM roadmap_resources ORDER BY id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, nodeID, title, urlValue, summary, sourceType, addedBy string
		var createdAt, updatedAt int64
		if err := rows.Scan(&id, &nodeID, &title, &urlValue, &summary, &sourceType, &addedBy, &createdAt, &updatedAt); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO roadmap_resources (id, node_id, title, url, summary, source_type, added_by, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		`, id, nodeID, title, urlValue, summary, sourceType, addedBy, unixToTime(createdAt), unixToTime(updatedAt)); err != nil {
			return err
		}
	}
	return rows.Err()
}

func migrateEvents(ctx context.Context, sqliteDB *sql.DB, tx *sql.Tx) error {
	rows, err := sqliteDB.Query(`SELECT id, title, start_time, end_time, location, kind, note_id, created_at, updated_at FROM events ORDER BY id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, title, kind string
		var location, noteID sql.NullString
		var startTime, endTime, createdAt, updatedAt int64
		if err := rows.Scan(&id, &title, &startTime, &endTime, &location, &kind, &noteID, &createdAt, &updatedAt); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO events (id, title, start_at, end_at, time_range, location, kind, note_id, created_at, updated_at)
			VALUES ($1, $2, $3, $4, tstzrange($3, $4, '[)'), $5, $6, $7, $8, $9)
		`, id, title, unixToTime(startTime), unixToTime(endTime), nullStringValue(location), kind, nullStringValue(noteID), unixToTime(createdAt), unixToTime(updatedAt)); err != nil {
			return err
		}
		content := kind
		if location.Valid {
			content = strings.TrimSpace(location.String + " " + kind)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO search_index (entity_type, entity_id, title, content, tags, updated_at, search_vector)
			VALUES ('event', $1, $2, $3, '{}'::text[], $4, to_tsvector('simple', coalesce($2,'') || ' ' || coalesce($3,'')))
		`, id, title, content, unixToTime(updatedAt)); err != nil {
			return err
		}
	}
	return rows.Err()
}

func migrateInbox(ctx context.Context, sqliteDB *sql.DB, tx *sql.Tx) error {
	rows, err := sqliteDB.Query(`SELECT id, kind, title, body, source, archived, converted_to, created_at, updated_at FROM inbox ORDER BY id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, kind, title, source string
		var body, convertedTo sql.NullString
		var archived int
		var createdAt, updatedAt int64
		if err := rows.Scan(&id, &kind, &title, &body, &source, &archived, &convertedTo, &createdAt, &updatedAt); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO inbox (id, kind, title, body, source, archived, converted_to, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		`, id, kind, title, nullStringValue(body), source, archived == 1, nullStringValue(convertedTo), unixToTime(createdAt), unixToTime(updatedAt)); err != nil {
			return err
		}
	}
	return rows.Err()
}

func migrateSyncTargets(ctx context.Context, sqliteDB *sql.DB, tx *sql.Tx) error {
	rows, err := sqliteDB.Query(`SELECT id, type, name, vault_path, base_folder, config_json, enabled, auto_sync, is_default, created_at, updated_at FROM sync_targets ORDER BY id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, targetType, name, vaultPath, baseFolder, rawConfig string
		var enabled, autoSync, isDefault int
		var createdAt, updatedAt int64
		if err := rows.Scan(&id, &targetType, &name, &vaultPath, &baseFolder, &rawConfig, &enabled, &autoSync, &isDefault, &createdAt, &updatedAt); err != nil {
			return err
		}
		config, err := normalizeJSONObject(rawConfig)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO sync_targets (id, type, name, vault_path, base_folder, config, enabled, auto_sync, is_default, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8, $9, $10, $11)
		`, id, targetType, name, vaultPath, baseFolder, config, enabled == 1, autoSync == 1, isDefault == 1, unixToTime(createdAt), unixToTime(updatedAt)); err != nil {
			return err
		}
	}
	return rows.Err()
}

func migrateNoteSyncBindings(ctx context.Context, sqliteDB *sql.DB, tx *sql.Tx) error {
	exists, err := sqliteTableExists(sqliteDB, "note_sync_bindings")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	rows, err := sqliteDB.Query(`SELECT note_id, target_id, created_at, updated_at FROM note_sync_bindings ORDER BY note_id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var noteID, targetID string
		var createdAt, updatedAt int64
		if err := rows.Scan(&noteID, &targetID, &createdAt, &updatedAt); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO note_sync_bindings (note_id, target_id, created_at, updated_at)
			VALUES ($1, $2, $3, $4)
		`, noteID, targetID, unixToTime(createdAt), unixToTime(updatedAt)); err != nil {
			return err
		}
	}
	return rows.Err()
}

func migrateSyncExternalClaims(ctx context.Context, sqliteDB *sql.DB, tx *sql.Tx) error {
	exists, err := sqliteTableExists(sqliteDB, "sync_external_claims")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	rows, err := sqliteDB.Query(`
		SELECT external_key, note_id, target_id, external_type, external_id, external_path, created_at, updated_at
		FROM sync_external_claims ORDER BY external_key
	`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var externalKey, noteID, targetID, externalType, externalID, externalPath string
		var createdAt, updatedAt int64
		if err := rows.Scan(&externalKey, &noteID, &targetID, &externalType, &externalID, &externalPath, &createdAt, &updatedAt); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO sync_external_claims (
				external_key, note_id, target_id, external_type, external_id, external_path, created_at, updated_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		`, externalKey, noteID, targetID, externalType, externalID, externalPath, unixToTime(createdAt), unixToTime(updatedAt)); err != nil {
			return err
		}
	}
	return rows.Err()
}

func migrateNoteSyncSuppressions(ctx context.Context, sqliteDB *sql.DB, tx *sql.Tx) error {
	exists, err := sqliteTableExists(sqliteDB, "note_sync_suppressions")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	rows, err := sqliteDB.Query(`SELECT note_id, target_id, reason, created_at, updated_at FROM note_sync_suppressions ORDER BY note_id, target_id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var noteID, targetID, reason string
		var createdAt, updatedAt int64
		if err := rows.Scan(&noteID, &targetID, &reason, &createdAt, &updatedAt); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO note_sync_suppressions (note_id, target_id, reason, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5)
		`, noteID, targetID, normalizeSyncSuppressionReason(reason), unixToTime(createdAt), unixToTime(updatedAt)); err != nil {
			return err
		}
	}
	return rows.Err()
}

func migrateSyncImportTombstones(ctx context.Context, sqliteDB *sql.DB, tx *sql.Tx) error {
	exists, err := sqliteTableExists(sqliteDB, "sync_import_tombstones")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	rows, err := sqliteDB.Query(`
		SELECT external_key, target_id, former_note_id, external_type, external_id, external_path, reason, created_at, updated_at
		FROM sync_import_tombstones ORDER BY external_key
	`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var externalKey, targetID, formerNoteID, externalType, externalID, externalPath, reason string
		var createdAt, updatedAt int64
		if err := rows.Scan(&externalKey, &targetID, &formerNoteID, &externalType, &externalID, &externalPath, &reason, &createdAt, &updatedAt); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO sync_import_tombstones (
				external_key, target_id, former_note_id, external_type, external_id, external_path, reason, created_at, updated_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		`, externalKey, targetID, formerNoteID, externalType, externalID, externalPath, normalizeSyncImportTombstoneReason(reason), unixToTime(createdAt), unixToTime(updatedAt)); err != nil {
			return err
		}
	}
	return rows.Err()
}

func migrateSyncStates(ctx context.Context, sqliteDB *sql.DB, tx *sql.Tx) error {
	rows, err := sqliteDB.Query(`
		SELECT note_id, target_id, external_path, external_id, external_url, content_hash, external_hash, external_mtime,
			last_direction, last_synced_at, status, error_message
		FROM note_sync_state ORDER BY note_id, target_id
	`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var noteID, targetID, externalPath, contentHash, status string
		var externalID, externalURL, externalHash, lastDirection, errorMessage sql.NullString
		var externalMTime, lastSyncedAt sql.NullInt64
		if err := rows.Scan(&noteID, &targetID, &externalPath, &externalID, &externalURL, &contentHash, &externalHash, &externalMTime, &lastDirection, &lastSyncedAt, &status, &errorMessage); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO note_sync_state (
				note_id, target_id, external_path, external_id, external_url, content_hash, external_hash, external_mtime,
				last_direction, last_synced_at, status, error_message
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		`, noteID, targetID, externalPath, nullStringValue(externalID), nullStringValue(externalURL), contentHash, nullStringValue(externalHash), nullIntTime(externalMTime), nullStringValue(lastDirection), nullIntTime(lastSyncedAt), status, nullStringValue(errorMessage)); err != nil {
			return err
		}
	}
	return rows.Err()
}

func unixToTime(value int64) interface{} {
	if value == 0 {
		return nil
	}
	return time.Unix(value, 0).UTC()
}

func nullIntTime(value sql.NullInt64) interface{} {
	if !value.Valid {
		return nil
	}
	return unixToTime(value.Int64)
}

func nullStringValue(value sql.NullString) interface{} {
	if !value.Valid {
		return nil
	}
	return value.String
}

func jsonStringArray(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return []string{}, nil
	}
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil, err
	}
	if values == nil {
		return []string{}, nil
	}
	return values, nil
}

func normalizeJSONObject(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "{}", nil
	}
	var value map[string]any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return "", err
	}
	if value == nil {
		return "", fmt.Errorf("JSON value must be an object")
	}
	var compacted bytes.Buffer
	if err := json.Compact(&compacted, []byte(raw)); err != nil {
		return "", err
	}
	return compacted.String(), nil
}

func sqliteTableExists(db *sql.DB, table string) (bool, error) {
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func normalizeSyncSuppressionReason(reason string) string {
	switch strings.TrimSpace(reason) {
	case "target_changed":
		return "target_changed"
	default:
		return "user_unbound"
	}
}

func normalizeSyncImportTombstoneReason(reason string) string {
	switch strings.TrimSpace(reason) {
	case "target_changed":
		return "target_changed"
	case "note_deleted":
		return "note_deleted"
	default:
		return "user_unbound"
	}
}
