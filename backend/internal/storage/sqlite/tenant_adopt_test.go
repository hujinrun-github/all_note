package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/storage/adopt"
)

func TestSQLiteAdoptExistingTenantBacksUpAndIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	cfg := storage.Config{Env: "test", Driver: storage.DriverSQLite, SQLitePath: path}
	p := Provider{}
	legacy, err := p.Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("create legacy fixture: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO users(id,email,display_name,password_hash,created_at,updated_at) VALUES('u1','u1@example.test','one','x',1,1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO workspaces(id,name,owner_user_id,created_at,updated_at) VALUES('w1','one','u1',1,1)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	manifestPath, err := findSQLiteTenantAdoptManifest()
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := adopt.LoadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	request := storage.AdoptManifest{ID: manifest.ID, Checksum: manifest.Checksum}
	if err := p.AdoptExistingTenant(context.Background(), cfg, request); err != nil {
		t.Fatalf("first adopt: %v", err)
	}
	if err := p.AdoptExistingTenant(context.Background(), cfg, request); err != nil {
		t.Fatalf("second adopt: %v", err)
	}
	backups, err := filepath.Glob(path + ".pre-adopt-*.bak")
	if err != nil || len(backups) == 0 {
		t.Fatalf("expected pre-adopt backup, got %v: %v", backups, err)
	}
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var anchors, migrations, installations int
	if err := db.QueryRow(`SELECT COUNT(*) FROM tenant_workspaces WHERE workspace_id='w1'`).Scan(&anchors); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM tenant_schema_migrations`).Scan(&migrations); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM tenant_installations`).Scan(&installations); err != nil {
		t.Fatal(err)
	}
	if anchors != 1 || migrations != 1 || installations != 1 {
		t.Fatalf("unexpected adopt state anchors=%d migrations=%d installations=%d", anchors, migrations, installations)
	}
}
