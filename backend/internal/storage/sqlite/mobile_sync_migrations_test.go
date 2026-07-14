package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSQLiteMobileSyncMigrationBackfillsStableLegacyNoteClientID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-mobile.db")
	db, err := sql.Open("sqlite", path+"?_foreign_keys=ON")
	if err != nil {
		t.Fatalf("open legacy sqlite: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`
		CREATE TABLE notes (
			id TEXT PRIMARY KEY,
			workspace_id TEXT NOT NULL,
			title TEXT NOT NULL,
			body TEXT NOT NULL,
			folder_id TEXT NOT NULL,
			tags TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		);
		INSERT INTO notes (id, workspace_id, title, body, folder_id, tags, created_at, updated_at)
		VALUES ('legacy-note-not-a-uuid', 'legacy-workspace', 'Legacy title', 'Legacy body', '__uncategorized', '["legacy"]', 100, 200);
	`); err != nil {
		t.Fatalf("seed legacy sqlite note: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := ensureSQLiteMobileSyncSchema(ctx, db); err != nil {
		t.Fatalf("first mobile migration: %v", err)
	}
	first := assertSQLiteLegacyMobileNote(t, db)
	if _, err := uuid.Parse(first); err != nil {
		t.Fatalf("backfilled client_id %q is not a UUID: %v", first, err)
	}
	if err := ensureSQLiteMobileSyncSchema(ctx, db); err != nil {
		t.Fatalf("second mobile migration: %v", err)
	}
	second := assertSQLiteLegacyMobileNote(t, db)
	if second != first {
		t.Fatalf("client_id changed across rerun: first=%q second=%q", first, second)
	}
}

func assertSQLiteLegacyMobileNote(t *testing.T, db *sql.DB) string {
	t.Helper()
	var (
		workspaceID string
		title       string
		body        string
		clientID    string
		revision    int64
		deletedAt   sql.NullInt64
		createdAt   int64
		updatedAt   int64
	)
	if err := db.QueryRow(`
		SELECT workspace_id, title, body, client_id, revision, deleted_at, created_at, updated_at
		FROM notes WHERE id = 'legacy-note-not-a-uuid'
	`).Scan(&workspaceID, &title, &body, &clientID, &revision, &deletedAt, &createdAt, &updatedAt); err != nil {
		t.Fatalf("read upgraded sqlite note: %v", err)
	}
	if workspaceID != "legacy-workspace" || title != "Legacy title" || body != "Legacy body" || revision != 1 || deletedAt.Valid || createdAt != 100 || updatedAt != 200 {
		t.Fatalf("legacy note changed: workspace=%q title=%q body=%q revision=%d deleted=%v created=%d updated=%d", workspaceID, title, body, revision, deletedAt, createdAt, updatedAt)
	}
	return clientID
}
