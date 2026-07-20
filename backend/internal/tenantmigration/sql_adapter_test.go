package tenantmigration

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/hujinrun/flowspace/internal/storage"
	storagesqlite "github.com/hujinrun/flowspace/internal/storage/sqlite"
	_ "modernc.org/sqlite"
)

func TestExportImportSQLiteAdapters(t *testing.T) {
	ctx := context.Background()
	sourceDB := newSQLiteTenantDB(t, "source.db")
	targetDB := newSQLiteTenantDB(t, "target.db")
	_, err := sourceDB.Exec(`INSERT INTO tenant_workspaces(workspace_id,epoch,state,migration_id) VALUES('w1',2,'fenced','source-fence'); INSERT INTO folders(id,workspace_id,name,position) VALUES('f1','w1','Inbox',0); INSERT INTO notes(id,workspace_id,folder_id,title,content,content_text,revision,pinned) VALUES('n1','w1','f1','One','{"type":"doc"}','One',4,1)`)
	if err != nil {
		t.Fatal(err)
	}
	sourceSnapshot, err := NewSQLSnapshot(ctx, sourceDB, SQLDialectSQLite, "w1")
	if err != nil {
		t.Fatal(err)
	}
	pack, err := Export(ctx, sourceSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	target := SQLImportTarget{DB: targetDB, Dialect: SQLDialectSQLite, ActiveBinding: func(context.Context, string) (bool, error) { return false, nil }}
	if err := Import(ctx, target, pack, ImportOptions{MigrationID: "m1"}); err != nil {
		t.Fatal(err)
	}
	targetSnapshot, err := NewSQLSnapshot(ctx, targetDB, SQLDialectSQLite, "w1")
	if err != nil {
		t.Fatal(err)
	}
	actual, err := Export(ctx, targetSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(pack.Manifest, actual.Manifest); err != nil {
		t.Fatalf("verify imported workspace: %v", err)
	}
}

func newSQLiteTenantDB(t *testing.T, name string) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	cfg := storage.Config{Env: "test", Driver: storage.DriverSQLite, SQLitePath: path}
	if err := (storagesqlite.Provider{}).MigrateTenant(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
