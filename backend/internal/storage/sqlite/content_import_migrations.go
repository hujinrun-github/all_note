package sqlite

import (
	"context"
	"database/sql"
	"fmt"
)

func ensureSQLiteContentImportSchema(ctx context.Context, db *sql.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS content_imports (
			id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, idempotency_key TEXT NOT NULL, request_sha256 TEXT NOT NULL,
			source_url TEXT NOT NULL, source_type TEXT NOT NULL DEFAULT '', canonical_url TEXT NOT NULL DEFAULT '',
			external_id TEXT NOT NULL DEFAULT '', feed_url TEXT NOT NULL DEFAULT '', title TEXT NOT NULL DEFAULT '',
			podcast_title TEXT NOT NULL DEFAULT '', cover_url TEXT NOT NULL DEFAULT '', description TEXT NOT NULL DEFAULT '',
			duration_seconds INTEGER NOT NULL DEFAULT 0, transcript_url TEXT NOT NULL DEFAULT '', audio_url TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL, stage TEXT NOT NULL, progress INTEGER NOT NULL DEFAULT 0 CHECK(progress BETWEEN 0 AND 100),
			summarize_with_ai INTEGER NOT NULL DEFAULT 1, summary_prompt TEXT NOT NULL DEFAULT '', include_transcript INTEGER NOT NULL DEFAULT 0,
			language TEXT NOT NULL DEFAULT 'auto', folder_id TEXT NOT NULL DEFAULT '', project_ids TEXT NOT NULL DEFAULT '[]',
			tags TEXT NOT NULL DEFAULT '[]', result_note_id TEXT NOT NULL DEFAULT '', error_code TEXT NOT NULL DEFAULT '',
			error_message TEXT NOT NULL DEFAULT '', attempt INTEGER NOT NULL DEFAULT 0, max_attempts INTEGER NOT NULL DEFAULT 4,
			next_attempt_at INTEGER, lease_owner TEXT NOT NULL DEFAULT '', lease_token TEXT NOT NULL DEFAULT '', lease_expires_at INTEGER,
			revision INTEGER NOT NULL DEFAULT 1, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
			UNIQUE(workspace_id, idempotency_key)
		)`,
		`CREATE INDEX IF NOT EXISTS content_imports_workspace_created_idx ON content_imports(workspace_id, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS content_imports_worker_idx ON content_imports(status, stage, lease_expires_at, created_at)`,
		`CREATE TABLE IF NOT EXISTS content_import_artifacts (
			workspace_id TEXT NOT NULL, import_id TEXT NOT NULL, kind TEXT NOT NULL, inline_text TEXT NOT NULL DEFAULT '',
			sha256 TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
			PRIMARY KEY(workspace_id, import_id, kind), FOREIGN KEY(import_id) REFERENCES content_imports(id) ON DELETE CASCADE
		)`,
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("ensure content import schema: %w", err)
		}
	}
	if err := ensureSQLiteColumn(ctx, db, "content_imports", "summary_prompt", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return fmt.Errorf("ensure content import summary prompt: %w", err)
	}
	return nil
}

func ensureSQLiteColumn(ctx context.Context, db *sql.DB, table, column, definition string) error {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, "ALTER TABLE "+table+" ADD COLUMN "+column+" "+definition)
	return err
}
