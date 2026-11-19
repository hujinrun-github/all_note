package sqlite

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/storage/contracttest"
	"github.com/hujinrun/flowspace/internal/workspacequery"
)

func TestSQLiteWorkspaceQueryContract(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace-query.db")
	config := storage.Config{Env: "test", Driver: storage.DriverSQLite, SQLitePath: path}
	if err := (Provider{}).MigrateTenant(t.Context(), config); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	contracttest.RunWorkspaceQuerySuite(t, contracttest.WorkspaceQueryFixture{DB: db, Dialect: contracttest.TaskDomainV2SQLite, NewReader: func(workspaceID string) workspacequery.Reader {
		return newTaskDomainV2ProjectReader(db, workspaceID).(workspacequery.Reader)
	}})
}
