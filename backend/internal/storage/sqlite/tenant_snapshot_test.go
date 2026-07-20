package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/hujinrun/flowspace/internal/storage"
)

func TestSQLiteTenantSnapshotRequiresFenceAndIsStable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.db")
	cfg := storage.Config{Env: "test", Driver: storage.DriverSQLite, SQLitePath: path}
	p := Provider{}
	if err := p.MigrateTenant(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO tenant_workspaces(workspace_id) VALUES('w1')`); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ExportTenantSnapshot(context.Background(), cfg, "w1"); err == nil {
		t.Fatal("active workspace snapshot must be rejected")
	}
	if _, err := db.Exec(`UPDATE tenant_workspaces SET state='fenced',migration_id='m1' WHERE workspace_id='w1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO notes(id,workspace_id,title,content,revision) VALUES('n1','w1','one','{}',1)`); err != nil {
		t.Fatal(err)
	}
	first, err := p.ExportTenantSnapshot(context.Background(), cfg, "w1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := p.ExportTenantSnapshot(context.Background(), cfg, "w1")
	if err != nil {
		t.Fatal(err)
	}
	if first.InstallationID == "" || first.SchemaVersion == "" || len(first.Tables) != len(second.Tables) {
		t.Fatalf("incomplete snapshot: %+v", first)
	}
	for i := range first.Tables {
		if first.Tables[i] != second.Tables[i] {
			t.Fatalf("snapshot changed without writes: %+v != %+v", first.Tables[i], second.Tables[i])
		}
	}
}
