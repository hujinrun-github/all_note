package postgres

import (
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/storage/contracttest"
	"github.com/hujinrun/flowspace/internal/workspacequery"
)

func TestPostgresWorkspaceQueryContract(t *testing.T) {
	url := createPostgresTestSchema(t, fmt.Sprintf("fs_test_workspace_query_%d", time.Now().UnixNano()))
	config := storage.Config{Env: "test", Driver: storage.DriverPostgres, URL: url}
	if err := (Provider{}).MigrateTenant(t.Context(), config); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	contracttest.RunWorkspaceQuerySuite(t, contracttest.WorkspaceQueryFixture{DB: db, Dialect: contracttest.TaskDomainV2Postgres, NewReader: func(workspaceID string) workspacequery.Reader {
		return newTaskDomainV2ProjectReader(db, workspaceID).(workspacequery.Reader)
	}})
}
