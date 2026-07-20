package codexoauth

import (
	"bytes"
	"context"
	"github.com/hujinrun/flowspace/internal/credentials"
	"github.com/hujinrun/flowspace/internal/storage"
	storagesqlite "github.com/hujinrun/flowspace/internal/storage/sqlite"
	"path/filepath"
	"testing"
	"time"
)

func TestRepositoryEncryptsAndScopesDeviceAuthorization(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control-test.db")
	cfg := storage.Config{Env: "test", Driver: storage.DriverSQLite, SQLitePath: path}
	provider := storagesqlite.Provider{}
	if err := provider.MigrateControl(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	store, err := provider.OpenControl(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	db := store.(storage.SQLStore).SQLDB()
	_, _ = db.Exec(`INSERT INTO users(id,email,password_hash) VALUES('u1','u1@example.test','x'),('u2','u2@example.test','x'); INSERT INTO workspaces(id,name,owner_user_id) VALUES('w1','one','u1'); INSERT INTO workspace_members(workspace_id,user_id,role) VALUES('w1','u1','owner')`)
	keyring, _ := credentials.NewKeyring("active", map[string][]byte{"active": bytes.Repeat([]byte{8}, 32)})
	repo, _ := NewRepository(db, DialectSQLite, keyring)
	flow := Flow{ID: "flow1", WorkspaceID: "w1", UserID: "u1", DeviceAuthID: "device-secret", UserCode: "CODE", VerificationURL: "https://auth.example/device", Interval: 5 * time.Second, ExpiresAt: time.Now().Add(time.Minute)}
	if err := repo.Create(context.Background(), flow); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := db.QueryRow(`SELECT hex(device_ciphertext) FROM codex_oauth_device_flows WHERE id='flow1'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw == "" || bytes.Contains([]byte(raw), []byte("device-secret")) {
		t.Fatal("device credential was not encrypted")
	}
	loaded, err := repo.Get(context.Background(), "flow1", "w1", "u1")
	if err != nil || loaded.DeviceAuthID != "device-secret" {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}
	if _, err := repo.Get(context.Background(), "flow1", "w1", "u2"); err == nil {
		t.Fatal("cross-user flow read succeeded")
	}
}
