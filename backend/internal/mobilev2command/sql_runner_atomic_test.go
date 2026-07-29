package mobilev2command_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/mobilev2command"
	"github.com/hujinrun/flowspace/internal/mobilev2contract"
	"github.com/hujinrun/flowspace/internal/mobilev2sync"
	"github.com/hujinrun/flowspace/internal/storage"
	storagesqlite "github.com/hujinrun/flowspace/internal/storage/sqlite"
)

func TestPrepareAndFinalizeShareTenantFencedTransaction(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tenant.db")
	cfg := storage.Config{Env: "test", Driver: storage.DriverSQLite, SQLitePath: path}
	if err := (storagesqlite.Provider{}).MigrateTenant(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	workspaceID := "workspace-atomic"
	if _, err := db.ExecContext(ctx, `INSERT INTO tenant_workspaces(workspace_id,epoch,state) VALUES(?,1,'active')`, workspaceID); err != nil {
		t.Fatal(err)
	}
	writer := storagesqlite.NewTenantWriter(cfg)
	ledger := mobilev2command.NewSQLLedger(db, mobilev2command.SQLDialectSQLite)
	command := runnerCommand(t, workspaceID)
	afterImage := []byte(`{"entity_type":"project","entity_id":"project-1","client_id":null,"entity_revision":"1","aggregate_revisions":{"project_revision":"1","task_revision":null,"schedule_revision":null,"occurrence_revision":null},"deleted_at":null,"payload":{}}`)
	rollback := errors.New("rollback after terminal writes")

	err = writer.BeginFencedWrite(ctx, workspaceID, 1, func(tx storage.TenantWriteTx) error {
		mobileTx, ok := tx.(storage.MobileV2TenantWriteTx)
		if !ok {
			t.Fatal("tenant transaction does not expose mobile-v2 runner")
		}
		if _, proceed, err := ledger.PrepareOnRunner(ctx, mobileTx.MobileV2SQLRunner(), command); err != nil || !proceed {
			t.Fatalf("prepare proceed=%v err=%v", proceed, err)
		}
		if err := tx.EnqueueOutbox(ctx, storage.TenantOutboxEvent{
			ID: "event-rollback", Topic: "test.mobile-v2", AggregateID: "project-1",
			AggregateRevision: 1, PayloadJSON: `{"rolled_back":true}`,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := ledger.FinalizeOnRunner(ctx, mobileTx.MobileV2SQLRunner(), command, time.Now().UTC(),
			mobilev2command.DynamicCommitResult{
				DomainResult: mobilev2command.DomainResult{Status: mobilev2command.StatusApplied, AfterImages: [][]byte{afterImage}},
				ScopeChanges: []mobilev2command.ScopeChange{{
					Scope: mobilev2sync.ScopeIPhoneTaskCore, AfterImages: [][]byte{afterImage},
				}},
			}); err != nil {
			t.Fatal(err)
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("rollback error = %v", err)
	}
	assertCount(t, db, "tenant_job_outbox", 0)
	assertCount(t, db, "mobile_v2_command_receipts", 0)
	assertCount(t, db, "mobile_v2_change_batches", 0)
	assertCount(t, db, "mobile_v2_scope_change_batches", 0)

	err = writer.BeginFencedWrite(ctx, workspaceID, 1, func(tx storage.TenantWriteTx) error {
		mobileTx := tx.(storage.MobileV2TenantWriteTx)
		if _, proceed, err := ledger.PrepareOnRunner(ctx, mobileTx.MobileV2SQLRunner(), command); err != nil || !proceed {
			t.Fatalf("prepare proceed=%v err=%v", proceed, err)
		}
		if err := tx.EnqueueOutbox(ctx, storage.TenantOutboxEvent{
			ID: "event-commit", Topic: "test.mobile-v2", AggregateID: "project-1",
			AggregateRevision: 1, PayloadJSON: `{"committed":true}`,
		}); err != nil {
			return err
		}
		response, err := ledger.FinalizeOnRunner(ctx, mobileTx.MobileV2SQLRunner(), command, time.Now().UTC(),
			mobilev2command.DynamicCommitResult{
				DomainResult: mobilev2command.DomainResult{Status: mobilev2command.StatusApplied, AfterImages: [][]byte{afterImage}},
				ScopeChanges: []mobilev2command.ScopeChange{{
					Scope: mobilev2sync.ScopeIPhoneTaskCore, AfterImages: [][]byte{afterImage},
				}},
			})
		if err != nil || response.Receipt == nil || response.Receipt.CommitSequence != 1 {
			t.Fatalf("finalize response=%#v err=%v", response, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	assertCount(t, db, "tenant_job_outbox", 1)
	assertCount(t, db, "mobile_v2_command_receipts", 1)
	assertCount(t, db, "mobile_v2_change_batches", 1)
	assertCount(t, db, "mobile_v2_scope_change_batches", 1)

	err = writer.BeginFencedWrite(ctx, workspaceID, 1, func(tx storage.TenantWriteTx) error {
		mobileTx := tx.(storage.MobileV2TenantWriteTx)
		response, proceed, err := ledger.PrepareOnRunner(ctx, mobileTx.MobileV2SQLRunner(), command)
		if err != nil || proceed || !response.Replayed || response.Receipt == nil {
			t.Fatalf("replay response=%#v proceed=%v err=%v", response, proceed, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	assertCount(t, db, "mobile_v2_command_receipts", 1)
}

func runnerCommand(t *testing.T, workspaceID string) mobilev2command.Command {
	t.Helper()
	raw := json.RawMessage(`{
		"command_id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		"request_digest":"sha256:0000000000000000000000000000000000000000000000000000000000000000",
		"origin_device_client_id":"cccccccc-cccc-4ccc-8ccc-cccccccccccc",
		"workspace_id":"` + workspaceID + `","command_type":"project.create",
		"target":{"entity_id":null,"client_id":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"},
		"created_runtime_epoch":"1","expected":{},"depends_on_command_id":null,
		"supersedes_command_id":null,"payload":{}
	}`)
	digest, err := mobilev2contract.RequestDigest(raw)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	object["request_digest"] = digest
	raw, err = json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	return mobilev2command.Command{
		WorkspaceID: workspaceID, OriginDeviceClientID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc",
		CommandID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", RequestDigest: digest,
		CommandType: "project.create", CreatedRuntimeEpoch: "1", RawEnvelope: raw,
		ExpectedRevisionNames: []string{},
	}
}

func assertCount(t *testing.T, db *sql.DB, table string, want int) {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("%s count = %d, want %d", table, count, want)
	}
}
