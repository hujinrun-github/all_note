package controlsettings

import (
	"bytes"
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/hujinrun/flowspace/internal/controlprofile"
	"github.com/hujinrun/flowspace/internal/credentials"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
	storagesqlite "github.com/hujinrun/flowspace/internal/storage/sqlite"
)

func TestProvisionWorkspaceIdentityIsIdempotentAndCreatesConcreteDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control-test.db")
	cfg := storage.Config{Env: "test", Driver: storage.DriverSQLite, SQLitePath: path}
	provider := storagesqlite.Provider{}
	if err := provider.MigrateControl(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	opened, err := provider.OpenControl(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	db := opened.(storage.SQLStore).SQLDB()
	keyring, err := credentials.NewKeyring("active", map[string][]byte{"active": bytes.Repeat([]byte{3}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := controlprofile.New(db, controlprofile.DialectSQLite, keyring)
	if err != nil {
		t.Fatal(err)
	}
	defaults, err := ReconcileSystemDefaults(context.Background(), profiles, []SystemProfileSpec{
		{CandidateID: "db-v1", FamilyID: "platform-db", Kind: "data_store", Name: "Database", Provider: "sqlite", ConfigJSON: `{"path":"tenant.db"}`, Mode: "default"},
		{CandidateID: "object-v1", FamilyID: "platform-object", Kind: "object_s3", Name: "Objects", Provider: "unavailable", ConfigJSON: `{"reason":"not_configured"}`, Mode: "default"},
		{CandidateID: "chat-v1", FamilyID: "platform-chat", Kind: "llm_chat", Name: "Chat", Provider: "unavailable", ConfigJSON: `{"reason":"not_configured"}`, Mode: "disabled"},
		{CandidateID: "speech-v1", FamilyID: "platform-speech", Kind: "llm_transcription", Name: "Speech", Provider: "unavailable", ConfigJSON: `{"reason":"not_configured"}`, Mode: "disabled"},
	})
	if err != nil {
		t.Fatal(err)
	}
	user := model.User{ID: "u1", Email: "owner@example.test", DisplayName: "Owner", PasswordHash: "hash", PasswordSet: true, Role: "admin", Status: "active", DefaultWorkspaceID: "w1"}
	for i := 0; i < 2; i++ {
		if err := ProvisionWorkspaceIdentity(context.Background(), db, false, user, defaults); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM workspace_service_bindings WHERE workspace_id='w1'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 4 {
		t.Fatalf("workspace bindings = %d, want 4", count)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM workspace_service_endpoints WHERE workspace_id='w1' AND source_type='system'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 4 {
		t.Fatalf("workspace endpoints = %d, want 4", count)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM workspace_service_bindings WHERE workspace_id='w1' AND kind IN ('data_store','object_s3') AND mode='default'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("storage default bindings = %d, want 2", count)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM workspace_runtime_state WHERE workspace_id='w1'`).Scan(&count); err != nil && err != sql.ErrNoRows {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("runtime states = %d, want 1", count)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM workspace_ai_feature_settings WHERE workspace_id='w1'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("AI feature settings = %d, want 2", count)
	}
}
