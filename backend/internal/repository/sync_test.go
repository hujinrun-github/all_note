package repository

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/hujinrun/flowspace/internal/model"
	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)

	schema, err := os.ReadFile("../../db/schema.sql")
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}

	if _, err := db.Exec(string(schema)); err != nil {
		t.Fatalf("exec schema: %v", err)
	}

	DB = db
	t.Cleanup(func() {
		DB = nil
		db.Close()
	})

	return db
}

func openSyncTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return openTestDB(t)
}

func TestSyncTargetRoundTrip(t *testing.T) {
	openTestDB(t)

	target := &model.SyncTarget{
		Type:       "obsidian",
		Name:       "Local Vault",
		VaultPath:  "C:\\Vault",
		BaseFolder: "FlowSpace Notes",
		Enabled:    true,
		AutoSync:   true,
	}

	if err := SaveSyncTarget(target); err != nil {
		t.Fatalf("save sync target: %v", err)
	}

	got, err := GetDefaultSyncTarget("obsidian")
	if err != nil {
		t.Fatalf("get default sync target: %v", err)
	}

	if got.ID == "" {
		t.Fatal("expected generated sync target ID")
	}
	if got.VaultPath != "C:\\Vault" {
		t.Fatalf("expected vault path %q, got %q", "C:\\Vault", got.VaultPath)
	}
	if !got.AutoSync {
		t.Fatal("expected auto sync to be enabled")
	}
}

func TestInitDBAddsBidirectionalSyncColumns(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "flowspace.db")
	createOldSyncStateDB(t, dbPath)
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
	assertSyncStateColumns(t)

	if err := DB.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
	DB = nil

	if err := InitDB(dbPath); err != nil {
		t.Fatalf("init db again: %v", err)
	}
	assertSyncStateColumns(t)
}

func TestInitDBBackfillsLegacySyncedExternalHash(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "flowspace.db")
	createOldSyncStateDB(t, dbPath)
	insertOldSyncStateRow(t, dbPath, "note-legacy", "target-legacy", "legacy-content-hash", "synced")
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

	var externalHash sql.NullString
	if err := DB.QueryRow(`
		SELECT external_hash
		FROM note_sync_state
		WHERE note_id = 'note-legacy' AND target_id = 'target-legacy'
	`).Scan(&externalHash); err != nil {
		t.Fatalf("select external hash: %v", err)
	}
	if !externalHash.Valid || externalHash.String != "legacy-content-hash" {
		t.Fatalf("external_hash = %#v, want legacy content hash", externalHash)
	}
}

func TestIsDuplicateColumnErrorMatchesSQLiteMessage(t *testing.T) {
	if !isDuplicateColumnError(errors.New("SQL logic error: DUPLICATE COLUMN NAME: external_hash (1)")) {
		t.Fatal("expected duplicate-column error to be detected case-insensitively")
	}
	if isDuplicateColumnError(errors.New("SQL logic error: duplicate column names are documented here (1)")) {
		t.Fatal("expected non-SQLite duplicate-column message to be ignored")
	}
}

func TestListAllNotesReturnsEveryNote(t *testing.T) {
	openSyncTestDB(t)

	const noteCount = 100001
	tx, err := DB.Begin()
	if err != nil {
		t.Fatalf("begin insert notes: %v", err)
	}
	stmt, err := tx.Prepare(`
		INSERT INTO notes (id, title, body, folder_id, tags, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		t.Fatalf("prepare insert note: %v", err)
	}
	for i := 0; i < noteCount; i++ {
		if _, err := stmt.Exec(fmt.Sprintf("note-%06d", i), fmt.Sprintf("Note %06d", i), "Body", "__uncategorized", "[]", int64(i), int64(i)); err != nil {
			stmt.Close()
			t.Fatalf("insert note %d: %v", i, err)
		}
	}
	if err := stmt.Close(); err != nil {
		t.Fatalf("close insert note statement: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit notes: %v", err)
	}

	notes, err := ListAllNotes()
	if err != nil {
		t.Fatalf("list all notes: %v", err)
	}
	if len(notes) != noteCount {
		t.Fatalf("expected %d notes, got %d", noteCount, len(notes))
	}
}

func TestSyncStateRoundTripIncludesExternalMetadata(t *testing.T) {
	openSyncTestDB(t)
	target := insertSyncTargetForTest(t)
	note := insertNoteForTest(t, "Round Trip", "Body")
	now := nowUnix()
	state := &model.SyncState{
		NoteID:        note.ID,
		TargetID:      target.ID,
		ExternalPath:  "D:\\Vault\\FlowSpace Notes\\Round Trip.md",
		ContentHash:   "flow-hash",
		ExternalHash:  "obsidian-hash",
		ExternalMTime: &now,
		LastSyncedAt:  &now,
		LastDirection: "pull",
		Status:        "synced",
	}

	if err := UpsertSyncState(state); err != nil {
		t.Fatalf("upsert state: %v", err)
	}
	got, err := GetSyncState(note.ID, target.ID)
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if got.ExternalHash != "obsidian-hash" || got.ExternalMTime == nil || got.LastDirection != "pull" {
		t.Fatalf("metadata was not persisted: %+v", got)
	}

	updatedMTime := now + 60
	state.ExternalHash = "obsidian-hash-updated"
	state.ExternalMTime = &updatedMTime
	state.LastDirection = "push"
	if err := UpsertSyncState(state); err != nil {
		t.Fatalf("upsert updated state: %v", err)
	}
	got, err = GetSyncState(note.ID, target.ID)
	if err != nil {
		t.Fatalf("get updated state: %v", err)
	}
	if got.ExternalHash != "obsidian-hash-updated" || got.ExternalMTime == nil || *got.ExternalMTime != updatedMTime || got.LastDirection != "push" {
		t.Fatalf("metadata was not updated on conflict: %+v", got)
	}
}

func TestListExternalDeletedSyncStates(t *testing.T) {
	openSyncTestDB(t)
	target := insertSyncTargetForTest(t)
	note := insertNoteForTest(t, "Deleted In Obsidian", "Body")
	now := nowUnix()
	if err := UpsertSyncState(&model.SyncState{
		NoteID:        note.ID,
		TargetID:      target.ID,
		ExternalPath:  "D:\\Vault\\FlowSpace Notes\\Deleted.md",
		ContentHash:   "flow-hash",
		ExternalHash:  "obsidian-hash",
		ExternalMTime: &now,
		LastSyncedAt:  &now,
		LastDirection: "delete_detected",
		Status:        "external_deleted",
	}); err != nil {
		t.Fatalf("upsert state: %v", err)
	}

	items, err := ListExternalDeletedSyncStates(target.ID)
	if err != nil {
		t.Fatalf("list external deleted: %v", err)
	}
	if len(items) != 1 || items[0].NoteID != note.ID || items[0].Title != "Deleted In Obsidian" {
		t.Fatalf("unexpected items: %+v", items)
	}
}

func insertSyncTargetForTest(t *testing.T) model.SyncTarget {
	t.Helper()
	target := &model.SyncTarget{
		Type:       "obsidian",
		Name:       "Test Vault",
		VaultPath:  "D:\\Vault",
		BaseFolder: "FlowSpace Notes",
		Enabled:    true,
		AutoSync:   false,
	}
	if err := SaveSyncTarget(target); err != nil {
		t.Fatalf("save target: %v", err)
	}
	return *target
}

func insertNoteForTest(t *testing.T, title string, body string) model.Note {
	t.Helper()
	note := &model.Note{
		Title:    title,
		Body:     body,
		FolderID: "__uncategorized",
		Tags:     "[]",
	}
	if err := CreateNote(note); err != nil {
		t.Fatalf("create note: %v", err)
	}
	return *note
}

func chdirBackendRoot(t *testing.T) {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	if err := os.Chdir("../.."); err != nil {
		t.Fatalf("chdir backend root: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(wd); err != nil {
			t.Fatalf("restore working directory: %v", err)
		}
	})
}

func createOldSyncStateDB(t *testing.T, dbPath string) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open old db: %v", err)
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
		CREATE TABLE sync_targets (
			id TEXT PRIMARY KEY,
			type TEXT NOT NULL,
			name TEXT NOT NULL,
			vault_path TEXT NOT NULL,
			base_folder TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			auto_sync INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		);
		CREATE TABLE note_sync_state (
			note_id TEXT NOT NULL,
			target_id TEXT NOT NULL,
			external_path TEXT NOT NULL,
			content_hash TEXT NOT NULL,
			last_synced_at INTEGER,
			status TEXT NOT NULL,
			error_message TEXT,
			PRIMARY KEY (note_id, target_id),
			FOREIGN KEY (note_id) REFERENCES notes(id) ON DELETE CASCADE,
			FOREIGN KEY (target_id) REFERENCES sync_targets(id) ON DELETE CASCADE
		);
	`); err != nil {
		t.Fatalf("create old db: %v", err)
	}
}

func insertOldSyncStateRow(t *testing.T, dbPath, noteID, targetID, contentHash, status string) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open old db for insert: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`
		INSERT INTO folders (id, name, sort_order, created_at)
		VALUES ('__uncategorized', 'Uncategorized', 0, 1717200000)
	`); err != nil {
		t.Fatalf("insert old folder: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO notes (id, title, body, folder_id, tags, created_at, updated_at)
		VALUES (?, 'Legacy', 'Legacy body', '__uncategorized', '[]', 1717200000, 1717200000)
	`, noteID); err != nil {
		t.Fatalf("insert old note: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO sync_targets (id, type, name, vault_path, base_folder, enabled, auto_sync, created_at, updated_at)
		VALUES (?, 'obsidian', 'Legacy Vault', 'D:\Vault', 'FlowSpace Notes', 1, 0, 1717200000, 1717200000)
	`, targetID); err != nil {
		t.Fatalf("insert old sync target: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO note_sync_state (note_id, target_id, external_path, content_hash, last_synced_at, status, error_message)
		VALUES (?, ?, 'D:\Vault\FlowSpace Notes\Legacy.md', ?, 1717200000, ?, NULL)
	`, noteID, targetID, contentHash, status); err != nil {
		t.Fatalf("insert old sync state row: %v", err)
	}
}

func assertSyncStateColumns(t *testing.T) {
	t.Helper()
	rows, err := DB.Query(`PRAGMA table_info(note_sync_state)`)
	if err != nil {
		t.Fatalf("table info: %v", err)
	}
	defer rows.Close()

	columns := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			t.Fatalf("scan column: %v", err)
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate columns: %v", err)
	}

	for _, name := range []string{"external_hash", "external_mtime", "last_direction"} {
		if !columns[name] {
			t.Fatalf("expected note_sync_state.%s to exist", name)
		}
	}
}
