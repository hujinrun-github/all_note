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
	for _, table := range []string{"tasks", "events", "inbox"} {
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

	statements := []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS notes_workspace_client_id_idx
			ON notes (workspace_id, client_id) WHERE client_id IS NOT NULL`,
		`CREATE UNIQUE INDEX IF NOT EXISTS tasks_workspace_client_id_idx
			ON tasks (workspace_id, client_id) WHERE client_id IS NOT NULL`,
		`CREATE UNIQUE INDEX IF NOT EXISTS events_workspace_client_id_idx
			ON events (workspace_id, client_id) WHERE client_id IS NOT NULL`,
		`CREATE UNIQUE INDEX IF NOT EXISTS inbox_workspace_client_id_idx
			ON inbox (workspace_id, client_id) WHERE client_id IS NOT NULL`,
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
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("ensure mobile sync schema: %w", err)
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
