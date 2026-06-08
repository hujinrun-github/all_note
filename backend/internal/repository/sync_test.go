package repository

import (
	"database/sql"
	"os"
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
	openSyncTestDB(t)

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

	for _, name := range []string{"external_hash", "external_mtime", "last_direction"} {
		if !columns[name] {
			t.Fatalf("expected note_sync_state.%s to exist", name)
		}
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
