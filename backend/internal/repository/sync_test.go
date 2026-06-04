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
