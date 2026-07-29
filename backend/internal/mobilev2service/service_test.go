package mobilev2service

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/handler"
	"github.com/hujinrun/flowspace/internal/mobilev2sync"
	"github.com/hujinrun/flowspace/internal/storage"
	storagesqlite "github.com/hujinrun/flowspace/internal/storage/sqlite"
	"github.com/hujinrun/flowspace/internal/taskapp"
	"github.com/hujinrun/flowspace/internal/taskruntime"
)

func TestServiceCapabilitiesSnapshotPagingAndChangesUseOneRuntime(t *testing.T) {
	ctx := context.Background()
	db := mobileV2ServiceDB(t)
	now := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
	resolver := mobileV2RuntimeResolverFake{snapshot: taskruntime.MobileRuntimeSnapshot{
		WorkspaceID: "workspace-1", Epoch: 1, Driver: storage.DriverSQLite, DB: db,
		Application: taskapp.RuntimeSnapshot{WorkspaceID: "workspace-1", Epoch: 1},
	}}
	service, err := New(Config{
		Runtime: resolver, Commands: mobileV2CommandServiceFake{}, TokenSecret: "mobile-v2-service-test-secret",
		PageSize: 2, SnapshotTTL: time.Hour, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	identity := handler.MobileV2Identity{WorkspaceID: "workspace-1", UserID: "user-1"}
	capabilitiesValue, err := service.Capabilities(ctx, identity)
	if err != nil {
		t.Fatal(err)
	}
	capabilities := capabilitiesValue.(capabilitiesResponse)
	if capabilities.WorkspaceMode != "v2-active" || capabilities.RuntimeEpoch != "1" ||
		capabilities.ContractSHA256 == "" || len(capabilities.SyncScopes) != 4 {
		t.Fatalf("capabilities = %#v", capabilities)
	}

	firstValue, err := service.Snapshot(ctx, handler.MobileV2SnapshotRequest{
		Identity: identity, Scope: string(mobilev2sync.ScopeIPhoneTaskCore),
	})
	if err != nil {
		t.Fatal(err)
	}
	first := firstValue.(snapshotResponse)
	if !first.HasMore || first.NextPageToken == nil || first.PageIndex != 0 ||
		first.AsOfSequence != "0" || first.SnapshotCursor == "" {
		t.Fatalf("first snapshot page = %#v", first)
	}
	var firstEntities []json.RawMessage
	if err := json.Unmarshal(first.Entities, &firstEntities); err != nil || len(firstEntities) != 2 {
		t.Fatalf("first entities=%s err=%v", first.Entities, err)
	}
	secondValue, err := service.Snapshot(ctx, handler.MobileV2SnapshotRequest{
		Identity: identity, Scope: string(mobilev2sync.ScopeIPhoneTaskCore), PageToken: *first.NextPageToken,
	})
	if err != nil {
		t.Fatal(err)
	}
	second := secondValue.(snapshotResponse)
	if second.HasMore || second.NextPageToken != nil || second.PageIndex != 1 ||
		second.SnapshotID != first.SnapshotID || second.SnapshotCursor != first.SnapshotCursor {
		t.Fatalf("second snapshot page = %#v", second)
	}

	var secondEntities []json.RawMessage
	if err := json.Unmarshal(second.Entities, &secondEntities); err != nil || len(secondEntities) != 2 {
		t.Fatalf("second entities=%s err=%v", second.Entities, err)
	}
	repository := mobilev2sync.NewSQLRepository(
		db,
		mobilev2sync.SQLDialectSQLite,
		mobilev2sync.NewTokenCodec("mobile-v2-service-test-secret"),
	)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.AppendScopeChange(ctx, tx, mobilev2sync.ScopeChangeInput{
		WorkspaceID: "workspace-1", Scope: mobilev2sync.ScopeIPhoneTaskCore,
		EntitiesJSON: json.RawMessage("[" + string(firstEntities[0]) + "]"), CommittedAt: now.Add(time.Minute),
	}); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	changesValue, err := service.Changes(ctx, handler.MobileV2ChangesRequest{
		Identity: identity, Scope: string(mobilev2sync.ScopeIPhoneTaskCore), Cursor: first.SnapshotCursor,
	})
	if err != nil {
		t.Fatal(err)
	}
	changes := changesValue.(changesResponse)
	if changes.HasMore || len(changes.Changes) != 1 || changes.Changes[0].ChangeSequence != "1" ||
		changes.Changes[0].Receipt == nil || string(changes.Changes[0].Receipt) != "null" {
		t.Fatalf("changes = %#v", changes)
	}
}

type mobileV2RuntimeResolverFake struct {
	snapshot taskruntime.MobileRuntimeSnapshot
	err      error
}

func (resolver mobileV2RuntimeResolverFake) ResolveMobileRuntime(context.Context, string) (taskruntime.MobileRuntimeSnapshot, error) {
	return resolver.snapshot, resolver.err
}

type mobileV2CommandServiceFake struct{}

func (mobileV2CommandServiceFake) ApplyCommand(context.Context, handler.MobileV2CommandRequest) (any, error) {
	return map[string]any{"schema_version": "mobile-v2"}, nil
}

func (mobileV2CommandServiceFake) Receipt(context.Context, handler.MobileV2ReceiptRequest) (any, error) {
	return map[string]any{"schema_version": "mobile-v2"}, nil
}

func mobileV2ServiceDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tenant.db")
	cfg := storage.Config{Env: "test", Driver: storage.DriverSQLite, SQLitePath: path}
	if err := (storagesqlite.Provider{}).MigrateTenant(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := "2026-07-29T08:00:00.000Z"
	mustServiceExec(t, tx, `INSERT INTO tenant_workspaces(workspace_id) VALUES('workspace-1')`)
	mustServiceExec(t, tx, `INSERT INTO domain_projects_v2
		(workspace_id,id,name,description,kind,horizon,status,revision,created_at,updated_at)
		VALUES('workspace-1','project-1','Project','Description','standard','short','active',2,?,?)`, now, now)
	mustServiceExec(t, tx, `INSERT INTO domain_tasks_v2
		(workspace_id,id,project_id,title,description,lifecycle_status,priority,sort_order,revision,created_at,updated_at)
		VALUES('workspace-1','task-1','project-1','Task','Description','active',1,0,3,?,?)`, now, now)
	mustServiceExec(t, tx, `INSERT INTO domain_task_schedules_v2
		(workspace_id,task_id,revision,current_schedule_revision,generation_status,updated_at)
		VALUES('workspace-1','task-1',4,1,'idle',?)`, now)
	mustServiceExec(t, tx, `INSERT INTO domain_task_schedule_versions_v2
		(workspace_id,task_id,schedule_revision,recurrence_type,timing_type,timezone,starts_on,recurrence_rule,created_at)
		VALUES('workspace-1','task-1',1,'none','date','Asia/Shanghai','2026-07-29','{}',?)`, now)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return db
}

func mustServiceExec(t *testing.T, tx *sql.Tx, query string, args ...any) {
	t.Helper()
	if _, err := tx.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}
