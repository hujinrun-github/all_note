package postgres

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMobileSyncMigrationUpgradesLegacyNotesWithStableClientIDs(t *testing.T) {
	schema := fmt.Sprintf("fs_test_mobile_upgrade_%d", time.Now().UnixNano())
	db := openPostgresTestDB(t, schema)
	previousDir := copyPostgresMigrationsBefore(t, "0010_mobile_sync_expand.sql")
	if err := RunPostgresMigrationsFromDirContext(context.Background(), db, previousDir); err != nil {
		t.Fatalf("apply previous release migrations: %v", err)
	}

	const (
		workspaceID = "legacy_mobile_workspace"
		noteID      = "legacy-note-not-a-uuid"
		title       = "Legacy title"
		body        = "Legacy body must survive migration"
	)
	updatedAt := time.Date(2025, time.December, 31, 23, 59, 58, 0, time.UTC)
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin previous release seed: %v", err)
	}
	defer tx.Rollback()
	seedStatements := []string{
		`INSERT INTO users (id, email, display_name, password_hash, role, status)
		 VALUES ('legacy_mobile_owner', 'legacy-mobile@example.com', 'Legacy Owner', 'hash', 'admin', 'active')`,
		`INSERT INTO workspaces (id, name, owner_user_id)
		 VALUES ('legacy_mobile_workspace', 'Legacy Mobile Workspace', 'legacy_mobile_owner')`,
		`UPDATE users SET default_workspace_id = 'legacy_mobile_workspace' WHERE id = 'legacy_mobile_owner'`,
		`INSERT INTO workspace_members (workspace_id, user_id, role)
		 VALUES ('legacy_mobile_workspace', 'legacy_mobile_owner', 'owner')`,
		`UPDATE folders SET workspace_id = 'legacy_mobile_workspace' WHERE id = '__uncategorized'`,
	}
	for _, statement := range seedStatements {
		if _, err := tx.Exec(statement); err != nil {
			t.Fatalf("seed previous release workspace: %v", err)
		}
	}
	if _, err := tx.Exec(`
		INSERT INTO notes (id, workspace_id, title, body, folder_id, tags, created_at, updated_at)
		VALUES ($1, $2, $3, $4, '__uncategorized', ARRAY['legacy'], $5, $5)
	`, noteID, workspaceID, title, body, updatedAt); err != nil {
		t.Fatalf("seed previous release note: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit previous release seed: %v", err)
	}

	currentDir, err := findPostgresMigrationsDir()
	if err != nil {
		t.Fatalf("find current migrations: %v", err)
	}
	if err := RunPostgresMigrationsFromDirContext(context.Background(), db, currentDir); err != nil {
		t.Fatalf("upgrade current migrations: %v", err)
	}
	firstClientID := assertLegacyMobileNotePreserved(t, db, noteID, workspaceID, title, body, updatedAt)
	if _, err := uuid.Parse(firstClientID); err != nil {
		t.Fatalf("backfilled client_id %q is not a UUID: %v", firstClientID, err)
	}

	if err := RunPostgresMigrationsFromDirContext(context.Background(), db, currentDir); err != nil {
		t.Fatalf("rerun current migrations: %v", err)
	}
	secondClientID := assertLegacyMobileNotePreserved(t, db, noteID, workspaceID, title, body, updatedAt)
	if secondClientID != firstClientID {
		t.Fatalf("client_id changed across rerun: first=%q second=%q", firstClientID, secondClientID)
	}
}

func copyPostgresMigrationsBefore(t *testing.T, cutoff string) string {
	t.Helper()
	sourceDir, err := findPostgresMigrationsDir()
	if err != nil {
		t.Fatalf("find migrations: %v", err)
	}
	targetDir := t.TempDir()
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".sql" || name >= cutoff {
			continue
		}
		contents, err := os.ReadFile(filepath.Join(sourceDir, name))
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(targetDir, name), contents, 0o600); err != nil {
			t.Fatalf("copy migration %s: %v", name, err)
		}
	}
	return targetDir
}

func assertLegacyMobileNotePreserved(t *testing.T, db postgresRunner, noteID, workspaceID, title, body string, updatedAt time.Time) string {
	t.Helper()
	var (
		gotWorkspace string
		gotTitle     string
		gotBody      string
		clientID     string
		revision     int64
		deleted      *time.Time
		gotUpdated   time.Time
	)
	if err := db.QueryRowContext(context.Background(), `
		SELECT workspace_id, title, body, client_id, revision, deleted_at, updated_at
		FROM notes
		WHERE id = $1
	`, noteID).Scan(&gotWorkspace, &gotTitle, &gotBody, &clientID, &revision, &deleted, &gotUpdated); err != nil {
		t.Fatalf("read upgraded legacy note: %v", err)
	}
	if gotWorkspace != workspaceID || gotTitle != title || gotBody != body || revision != 1 || deleted != nil || !gotUpdated.Equal(updatedAt) {
		t.Fatalf("legacy note changed: workspace=%q title=%q body=%q revision=%d deleted=%v updated=%v", gotWorkspace, gotTitle, gotBody, revision, deleted, gotUpdated)
	}
	return clientID
}
