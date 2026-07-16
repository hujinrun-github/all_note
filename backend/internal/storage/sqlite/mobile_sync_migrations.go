package sqlite

import (
	"context"
	"crypto/md5"
	"database/sql"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

func ensureSQLiteMobileSyncSchema(ctx context.Context, db *sql.DB) error {
	columns := []struct {
		name       string
		definition string
	}{
		{name: "client_id", definition: "TEXT"},
		{name: "revision", definition: "INTEGER NOT NULL DEFAULT 1"},
		{name: "deleted_at", definition: "INTEGER"},
	}
	for _, column := range columns {
		exists, err := sqliteColumnExists(db, "notes", column.name)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		if _, err := db.ExecContext(ctx, "ALTER TABLE notes ADD COLUMN "+column.name+" "+column.definition); err != nil && !sqliteDuplicateColumnError(err) {
			return fmt.Errorf("add notes.%s: %w", column.name, err)
		}
	}
	if err := backfillSQLiteMobileNoteClientIDs(ctx, db); err != nil {
		return err
	}
	entityTables := make(map[string]bool, 3)
	for _, table := range []string{"tasks", "events", "inbox"} {
		tableExists, err := sqliteTableExists(db, table)
		if err != nil {
			return err
		}
		if !tableExists {
			continue
		}
		entityTables[table] = true
		for _, column := range columns {
			exists, err := sqliteColumnExists(db, table, column.name)
			if err != nil {
				return err
			}
			if exists {
				continue
			}
			if _, err := db.ExecContext(ctx, "ALTER TABLE "+table+" ADD COLUMN "+column.name+" "+column.definition); err != nil && !sqliteDuplicateColumnError(err) {
				return fmt.Errorf("add %s.%s: %w", table, column.name, err)
			}
		}
		if err := backfillSQLiteMobileEntityClientIDs(ctx, db, table); err != nil {
			return err
		}
	}
	occurrenceTableExists, err := sqliteTableExists(db, "task_occurrences")
	if err != nil {
		return err
	}
	if occurrenceTableExists {
		for _, column := range []struct {
			name       string
			definition string
		}{
			{name: "occurrence_id", definition: "TEXT"},
			{name: "revision", definition: "INTEGER NOT NULL DEFAULT 1"},
			{name: "deleted_at", definition: "INTEGER"},
		} {
			exists, err := sqliteColumnExists(db, "task_occurrences", column.name)
			if err != nil {
				return err
			}
			if exists {
				continue
			}
			if _, err := db.ExecContext(ctx, "ALTER TABLE task_occurrences ADD COLUMN "+column.name+" "+column.definition); err != nil && !sqliteDuplicateColumnError(err) {
				return fmt.Errorf("add task_occurrences.%s: %w", column.name, err)
			}
		}
		if err := backfillSQLiteMobileOccurrenceIDs(ctx, db); err != nil {
			return err
		}
	}

	statements := []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS notes_workspace_client_id_idx
			ON notes (workspace_id, client_id) WHERE client_id IS NOT NULL`,
		`CREATE TABLE IF NOT EXISTS mobile_mutation_receipts (
			workspace_id TEXT NOT NULL,
			device_client_id TEXT NOT NULL,
			mutation_id TEXT NOT NULL,
			request_sha256 TEXT NOT NULL,
			response_json TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			PRIMARY KEY (workspace_id, device_client_id, mutation_id)
		)`,
		`CREATE TABLE IF NOT EXISTS mobile_sync_outbox (
			sequence INTEGER PRIMARY KEY AUTOINCREMENT,
			workspace_id TEXT NOT NULL,
			mutation_id TEXT NOT NULL,
			entity_type TEXT NOT NULL,
			entity_client_id TEXT NOT NULL,
			operation TEXT NOT NULL,
			revision INTEGER NOT NULL,
			entity_json TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			published_at INTEGER,
			UNIQUE (workspace_id, mutation_id, entity_type, entity_client_id)
		)`,
		`CREATE INDEX IF NOT EXISTS mobile_sync_outbox_pending_idx
			ON mobile_sync_outbox (workspace_id, sequence) WHERE published_at IS NULL`,
		`CREATE TABLE IF NOT EXISTS mobile_sync_change_heads (
			workspace_id TEXT PRIMARY KEY,
			latest_position INTEGER NOT NULL DEFAULT 0,
			min_position INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS mobile_sync_changes (
			workspace_id TEXT NOT NULL,
			position INTEGER NOT NULL,
			operation TEXT NOT NULL,
			entity_json TEXT NOT NULL,
			committed_at INTEGER NOT NULL,
			PRIMARY KEY (workspace_id, position)
		)`,
		`CREATE TABLE IF NOT EXISTS mobile_sync_snapshot_sessions (
			session_id TEXT PRIMARY KEY,
			workspace_id TEXT NOT NULL,
			scope TEXT NOT NULL,
			boundary_position INTEGER NOT NULL,
			projection_time_zone TEXT NOT NULL DEFAULT 'UTC',
			scope_valid_until INTEGER NOT NULL DEFAULT 0,
			expires_at INTEGER NOT NULL,
			created_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS mobile_sync_snapshot_items (
			session_id TEXT NOT NULL,
			ordinal INTEGER NOT NULL,
			entity_json TEXT NOT NULL,
			PRIMARY KEY (session_id, ordinal),
			FOREIGN KEY (session_id) REFERENCES mobile_sync_snapshot_sessions(session_id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS mobile_retired_ids (
			workspace_id TEXT NOT NULL,
			entity_type TEXT NOT NULL,
			client_id TEXT NOT NULL,
			retired_at INTEGER NOT NULL,
			PRIMARY KEY (workspace_id, entity_type, client_id)
		)`,
		`CREATE TABLE IF NOT EXISTS mobile_sync_conflicts (
			workspace_id TEXT NOT NULL,
			conflict_id TEXT NOT NULL,
			mutation_id TEXT NOT NULL,
			device_client_id TEXT NOT NULL,
			request_sha256 TEXT NOT NULL,
			entity_type TEXT NOT NULL,
			entity_client_id TEXT NOT NULL,
			operation TEXT NOT NULL,
			base_revision INTEGER NOT NULL,
			remote_revision INTEGER NOT NULL,
			local_payload TEXT NOT NULL,
			remote_payload TEXT NOT NULL,
			revision INTEGER NOT NULL DEFAULT 1,
			resolution TEXT,
			resolved_at INTEGER,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			PRIMARY KEY (workspace_id, conflict_id),
			UNIQUE (workspace_id, device_client_id, mutation_id)
		)`,
		`CREATE INDEX IF NOT EXISTS mobile_sync_conflicts_unresolved_idx
			ON mobile_sync_conflicts (workspace_id, created_at, conflict_id) WHERE resolved_at IS NULL`,
	}
	for table := range entityTables {
		statements = append(statements, fmt.Sprintf(
			"CREATE UNIQUE INDEX IF NOT EXISTS %s_workspace_client_id_idx ON %s (workspace_id, client_id) WHERE client_id IS NOT NULL",
			table, table,
		))
	}
	if occurrenceTableExists {
		statements = append(statements, `CREATE UNIQUE INDEX IF NOT EXISTS task_occurrences_workspace_occurrence_id_idx
			ON task_occurrences (workspace_id, occurrence_id) WHERE occurrence_id IS NOT NULL`)
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("ensure mobile sync schema: %w", err)
		}
	}
	for _, column := range []struct {
		name       string
		definition string
	}{
		{name: "projection_time_zone", definition: "TEXT NOT NULL DEFAULT 'UTC'"},
		{name: "scope_valid_until", definition: "INTEGER NOT NULL DEFAULT 0"},
	} {
		exists, err := sqliteColumnExists(db, "mobile_sync_snapshot_sessions", column.name)
		if err != nil {
			return err
		}
		if !exists {
			if _, err := db.ExecContext(ctx, "ALTER TABLE mobile_sync_snapshot_sessions ADD COLUMN "+column.name+" "+column.definition); err != nil && !sqliteDuplicateColumnError(err) {
				return fmt.Errorf("add mobile_sync_snapshot_sessions.%s: %w", column.name, err)
			}
		}
	}
	return nil
}

func backfillSQLiteMobileOccurrenceIDs(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `SELECT task_id, occurrence_date, workspace_id FROM task_occurrences WHERE occurrence_id IS NULL ORDER BY workspace_id, task_id, occurrence_date`)
	if err != nil {
		return err
	}
	type occurrence struct {
		taskID, date, workspaceID string
	}
	occurrences := make([]occurrence, 0)
	for rows.Next() {
		var item occurrence
		if err := rows.Scan(&item.taskID, &item.date, &item.workspaceID); err != nil {
			_ = rows.Close()
			return err
		}
		occurrences = append(occurrences, item)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, item := range occurrences {
		occurrenceID := deterministicSQLiteMobileEntityClientID("task_occurrence", item.workspaceID, item.taskID+":"+item.date)
		if _, err := db.ExecContext(ctx, `UPDATE task_occurrences SET occurrence_id = ? WHERE workspace_id = ? AND task_id = ? AND occurrence_date = ? AND occurrence_id IS NULL`,
			occurrenceID, item.workspaceID, item.taskID, item.date); err != nil {
			return err
		}
	}
	return nil
}

func backfillSQLiteMobileNoteClientIDs(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `
		SELECT id, workspace_id
		FROM notes
		WHERE client_id IS NULL
		ORDER BY workspace_id, id
	`)
	if err != nil {
		return fmt.Errorf("list legacy mobile notes: %w", err)
	}
	type legacyNote struct {
		id          string
		workspaceID sql.NullString
	}
	legacyNotes := make([]legacyNote, 0)
	for rows.Next() {
		var note legacyNote
		if err := rows.Scan(&note.id, &note.workspaceID); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan legacy mobile note: %w", err)
		}
		legacyNotes = append(legacyNotes, note)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close legacy mobile note rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate legacy mobile notes: %w", err)
	}
	if len(legacyNotes) == 0 {
		return nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin legacy mobile note backfill: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	for _, note := range legacyNotes {
		clientID := deterministicSQLiteMobileNoteClientID(note.workspaceID.String, note.id)
		if _, err := tx.ExecContext(ctx, `
			UPDATE notes
			SET client_id = ?
			WHERE id = ? AND client_id IS NULL
		`, clientID, note.id); err != nil {
			return fmt.Errorf("backfill legacy mobile note %s: %w", note.id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit legacy mobile note backfill: %w", err)
	}
	committed = true
	return nil
}

func deterministicSQLiteMobileNoteClientID(workspaceID, noteID string) string {
	return deterministicSQLiteMobileEntityClientID("note", workspaceID, noteID)
}

func deterministicSQLiteMobileEntityClientID(entityType, workspaceID, entityID string) string {
	digest := md5.Sum([]byte("flowspace:" + entityType + ":" + workspaceID + ":" + entityID))
	digest[6] = (digest[6] & 0x0f) | 0x30
	digest[8] = (digest[8] & 0x3f) | 0x80
	return uuid.UUID(digest).String()
}

func backfillSQLiteMobileEntityClientIDs(ctx context.Context, db *sql.DB, table string) error {
	rows, err := db.QueryContext(ctx, `SELECT id, workspace_id FROM `+table+` WHERE client_id IS NULL ORDER BY workspace_id, id`)
	if err != nil {
		return fmt.Errorf("list legacy mobile %s: %w", table, err)
	}
	type legacyEntity struct {
		id          string
		workspaceID sql.NullString
	}
	entities := make([]legacyEntity, 0)
	for rows.Next() {
		var entity legacyEntity
		if err := rows.Scan(&entity.id, &entity.workspaceID); err != nil {
			_ = rows.Close()
			return err
		}
		entities = append(entities, entity)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(entities) == 0 {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	entityType := strings.TrimSuffix(table, "s")
	for _, entity := range entities {
		clientID := deterministicSQLiteMobileEntityClientID(entityType, entity.workspaceID.String, entity.id)
		if _, err := tx.ExecContext(ctx, `UPDATE `+table+` SET client_id = ? WHERE id = ? AND client_id IS NULL`, clientID, entity.id); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}
