package controlsettings

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
	storagesqlite "github.com/hujinrun/flowspace/internal/storage/sqlite"
)

func TestProvisionWorkspaceIdentityIsIdempotentAndCreatesDisabledAIBindings(t *testing.T) {
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
	user := model.User{ID: "u1", Email: "owner@example.test", DisplayName: "Owner", PasswordHash: "hash", PasswordSet: true, Role: "admin", Status: "active", DefaultWorkspaceID: "w1"}
	for i := 0; i < 2; i++ {
		if err := ProvisionWorkspaceIdentity(context.Background(), db, false, user); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM workspace_service_bindings WHERE workspace_id='w1' AND mode='disabled'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("disabled AI bindings = %d, want 2", count)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM workspace_runtime_state WHERE workspace_id='w1'`).Scan(&count); err != nil && err != sql.ErrNoRows {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("runtime states = %d, want 1", count)
	}
}
