package mobilev2service

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/storage"
	storagesqlite "github.com/hujinrun/flowspace/internal/storage/sqlite"
	"github.com/hujinrun/flowspace/internal/taskruntime"
)

type retentionWorkspaceListerStub struct {
	workspaceIDs []string
}

func (stub retentionWorkspaceListerStub) ListActiveWorkspaceIDs(context.Context) ([]string, error) {
	return append([]string(nil), stub.workspaceIDs...), nil
}

func TestContentRetentionWorkerPurgesExpiredDeletedRows(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tenant-retention-worker.db")
	cfg := storage.Config{Env: "test", Driver: storage.DriverSQLite, SQLitePath: path}
	if err := (storagesqlite.Provider{}).MigrateTenant(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const workspaceID = "workspace-retention-worker"
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	deletedAt := now.Add(-48 * time.Hour).Unix()
	if _, err := db.Exec(`INSERT INTO tenant_workspaces(workspace_id,epoch,state) VALUES(?,1,'active')`, workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO notes
		(id,workspace_id,client_id,revision,title,body,folder_id,tags,created_at,updated_at,deleted_at)
		VALUES('expired-note',?,'expired-note-client',2,'','',NULL,'[]',?,?,?)`,
		workspaceID, deletedAt, deletedAt, deletedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO mobile_v2_content_tombstones
		(workspace_id,entity_type,entity_id,client_id,revision,deleted_at)
		VALUES(?,'note','expired-note','expired-note-client',2,?)`, workspaceID, deletedAt); err != nil {
		t.Fatal(err)
	}
	worker, err := NewContentRetentionWorker(ContentRetentionWorkerConfig{
		Workspaces: retentionWorkspaceListerStub{workspaceIDs: []string{workspaceID}},
		Runtime: mobileV2RuntimeResolverFake{snapshot: taskruntime.MobileRuntimeSnapshot{
			WorkspaceID: workspaceID,
			Epoch:       1,
			Driver:      storage.DriverSQLite,
			DB:          db,
			Writer:      storagesqlite.NewTenantWriter(cfg),
		}},
		DeletedContentRetention: 24 * time.Hour,
		Now:                     func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	var notes, tombstones int
	if err := db.QueryRow(`SELECT COUNT(*) FROM notes WHERE workspace_id=? AND id='expired-note'`, workspaceID).Scan(&notes); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM mobile_v2_content_tombstones
		WHERE workspace_id=? AND entity_id='expired-note'`, workspaceID).Scan(&tombstones); err != nil {
		t.Fatal(err)
	}
	if notes != 0 || tombstones != 1 {
		t.Fatalf("notes=%d tombstones=%d, want physical row purged and tombstone retained", notes, tombstones)
	}
}
