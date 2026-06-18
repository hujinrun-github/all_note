package sqlite

import (
	"database/sql"
	"fmt"
	"strings"
)

func ensureSQLiteSyncColumnsBeforeSchema(db *sql.DB) error {
	exists, err := sqliteTableExists(db, "sync_targets")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	return sqliteAddColumnIfMissing(db, "sync_targets", "is_default", `ALTER TABLE sync_targets ADD COLUMN is_default INTEGER NOT NULL DEFAULT 0`)
}

func ensureSQLiteSyncSchema(db *sql.DB) error {
	if err := sqliteAddColumnIfMissing(db, "sync_targets", "is_default", `ALTER TABLE sync_targets ADD COLUMN is_default INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	statements := []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS sync_targets_type_name_idx
			ON sync_targets (type, name)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS sync_targets_one_default_per_type_idx
			ON sync_targets (type) WHERE is_default = 1`,
		`CREATE TABLE IF NOT EXISTS note_sync_bindings (
			note_id TEXT PRIMARY KEY,
			target_id TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			UNIQUE (note_id, target_id),
			FOREIGN KEY (note_id) REFERENCES notes(id) ON DELETE CASCADE,
			FOREIGN KEY (target_id) REFERENCES sync_targets(id) ON DELETE RESTRICT
		)`,
		`CREATE INDEX IF NOT EXISTS note_sync_bindings_target_idx
			ON note_sync_bindings (target_id, updated_at DESC)`,
		`CREATE TABLE IF NOT EXISTS sync_external_claims (
			external_key TEXT PRIMARY KEY,
			note_id TEXT NOT NULL UNIQUE,
			target_id TEXT NOT NULL,
			external_type TEXT NOT NULL CHECK (external_type IN ('obsidian_file', 'notion_page')),
			external_id TEXT NOT NULL DEFAULT '',
			external_path TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			FOREIGN KEY (note_id, target_id) REFERENCES note_sync_bindings(note_id, target_id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS sync_external_claims_target_idx
			ON sync_external_claims (target_id, updated_at DESC)`,
		`CREATE TABLE IF NOT EXISTS note_sync_suppressions (
			note_id TEXT NOT NULL,
			target_id TEXT NOT NULL,
			reason TEXT NOT NULL DEFAULT 'user_unbound' CHECK (reason IN ('user_unbound', 'target_changed')),
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			PRIMARY KEY (note_id, target_id),
			FOREIGN KEY (note_id) REFERENCES notes(id) ON DELETE CASCADE,
			FOREIGN KEY (target_id) REFERENCES sync_targets(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS note_sync_suppressions_target_updated_idx
			ON note_sync_suppressions (target_id, updated_at DESC)`,
		`CREATE TABLE IF NOT EXISTS sync_import_tombstones (
			external_key TEXT PRIMARY KEY,
			target_id TEXT NOT NULL REFERENCES sync_targets(id) ON DELETE CASCADE,
			former_note_id TEXT NOT NULL,
			external_type TEXT NOT NULL CHECK (external_type IN ('obsidian_file', 'notion_page')),
			external_id TEXT NOT NULL DEFAULT '',
			external_path TEXT NOT NULL DEFAULT '',
			reason TEXT NOT NULL DEFAULT 'user_unbound' CHECK (reason IN ('user_unbound', 'target_changed', 'note_deleted')),
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			UNIQUE (target_id, former_note_id, external_type)
		)`,
		`CREATE INDEX IF NOT EXISTS sync_import_tombstones_target_updated_idx
			ON sync_import_tombstones (target_id, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS sync_import_tombstones_note_type_idx
			ON sync_import_tombstones (former_note_id, external_type, updated_at DESC, created_at DESC)`,
	}
	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func sqliteAddColumnIfMissing(db *sql.DB, table, column, statement string) error {
	exists, err := sqliteColumnExists(db, table, column)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	if _, err := db.Exec(statement); err != nil && !sqliteDuplicateColumnError(err) {
		return err
	}
	return nil
}

func sqliteTableExists(db *sql.DB, table string) (bool, error) {
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func sqliteColumnExists(db *sql.DB, table, column string) (bool, error) {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false, fmt.Errorf("inspect SQLite table %s: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name string
		var columnType string
		var notNull int
		var defaultValue any
		var primaryKey int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func sqliteDuplicateColumnError(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "duplicate column name")
}
