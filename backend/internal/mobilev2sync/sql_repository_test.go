package mobilev2sync_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/mobilev2contract"
	"github.com/hujinrun/flowspace/internal/mobilev2sync"
	"github.com/hujinrun/flowspace/internal/storage"
	storagesqlite "github.com/hujinrun/flowspace/internal/storage/sqlite"
)

func TestSQLRepositoryPersistsFixedSnapshotPages(t *testing.T) {
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
	db.SetMaxOpenConns(1)
	defer db.Close()
	if _, err := db.ExecContext(ctx, `INSERT INTO tenant_workspaces(workspace_id) VALUES(?)`, "workspace-1"); err != nil {
		t.Fatal(err)
	}

	binding := mobilev2sync.TokenBinding{
		WorkspaceID: "workspace-1", Scope: mobilev2sync.ScopeIPhoneTaskCore,
		ContractEpoch: "3", RuntimeEpoch: "8", TaskModelVersion: 2,
		ScopeGeneration: "task-core-generation-1",
	}
	projected := []json.RawMessage{
		json.RawMessage(`{"entity_id":"task-a","entity_type":"task","revision":"1"}`),
		json.RawMessage(`{"entity_id":"task-b","entity_type":"task","revision":"1"}`),
		json.RawMessage(`{"entity_id":"task-c","entity_type":"task","revision":"1"}`),
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	repository := mobilev2sync.NewSQLRepository(
		db,
		mobilev2sync.SQLDialectSQLite,
		mobilev2sync.NewTokenCodec("snapshot-test-secret"),
	)
	first, err := repository.CreateSnapshot(ctx, mobilev2sync.CreateSnapshotInput{
		Binding: binding, ProjectionAsOf: now, ExpiresAt: now.Add(time.Hour), PageSize: 2,
	}, func(context.Context, *sql.Tx, uint64) ([]json.RawMessage, error) {
		return projected, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.AsOfSequence != "0" || first.PageCount != 2 || first.PageIndex != 0 {
		t.Fatalf("first page metadata = %#v", first)
	}
	var firstEntities []map[string]any
	if err := json.Unmarshal(first.EntitiesJSON, &firstEntities); err != nil {
		t.Fatal(err)
	}
	if len(firstEntities) != 2 || firstEntities[0]["entity_id"] != "task-a" {
		t.Fatalf("first page entities = %#v", firstEntities)
	}
	if want, err := mobilev2contract.PageChecksum(
		first.SnapshotID,
		0,
		first.AsOfSequence,
		first.EntitiesJSON,
	); err != nil || first.PageChecksum != want {
		t.Fatalf("page checksum = %q, want %q, err=%v", first.PageChecksum, want, err)
	}

	projected[2] = json.RawMessage(`{"entity_id":"task-mutated","entity_type":"task","revision":"9"}`)
	second, err := repository.ReadSnapshotPage(ctx, binding, first.SnapshotID, 1, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	var secondEntities []map[string]any
	if err := json.Unmarshal(second.EntitiesJSON, &secondEntities); err != nil {
		t.Fatal(err)
	}
	if len(secondEntities) != 1 || secondEntities[0]["entity_id"] != "task-c" {
		t.Fatalf("stored second page changed after projection: %#v", secondEntities)
	}
	if second.SnapshotManifestChecksum != first.SnapshotManifestChecksum ||
		second.SnapshotCursor != first.SnapshotCursor ||
		!second.ProjectionAsOf.Equal(first.ProjectionAsOf) {
		t.Fatalf("snapshot metadata changed: first=%#v second=%#v", first, second)
	}

	mismatch := binding
	mismatch.ContractEpoch = "4"
	if _, err := repository.ReadSnapshotPage(ctx, mismatch, first.SnapshotID, 1, now.Add(time.Minute)); !errors.Is(err, mobilev2sync.ErrSnapshotMismatch) {
		t.Fatalf("contract epoch mismatch error = %v", err)
	}
	if _, err := repository.ReadSnapshotPage(ctx, binding, first.SnapshotID, 1, first.ExpiresAt); !errors.Is(err, mobilev2sync.ErrSnapshotExpired) {
		t.Fatalf("expired snapshot error = %v", err)
	}
	deleted, err := repository.DeleteExpiredSnapshots(ctx, first.ExpiresAt)
	if err != nil || deleted != 1 {
		t.Fatalf("delete expired snapshots = (%d,%v)", deleted, err)
	}
}

func TestSQLRepositoryAppendsAndReadsIndependentScopeChanges(t *testing.T) {
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
	db.SetMaxOpenConns(1)
	defer db.Close()
	if _, err := db.ExecContext(ctx, `INSERT INTO tenant_workspaces(workspace_id) VALUES(?)`, "workspace-1"); err != nil {
		t.Fatal(err)
	}
	repository := mobilev2sync.NewSQLRepository(
		db,
		mobilev2sync.SQLDialectSQLite,
		mobilev2sync.NewTokenCodec("change-test-secret"),
	)
	now := time.Now().UTC()
	appendChange := func(scope mobilev2sync.ScopeName, entityID string, commit bool) uint64 {
		t.Helper()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		sequence, err := repository.AppendScopeChange(ctx, tx, mobilev2sync.ScopeChangeInput{
			WorkspaceID: "workspace-1",
			Scope:       scope,
			EntitiesJSON: json.RawMessage(
				`[{"entity_id":"` + entityID + `","entity_type":"task","revision":"1"}]`,
			),
			CommittedAt: now,
		})
		if err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if commit {
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
		} else if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		return sequence
	}
	if sequence := appendChange(mobilev2sync.ScopeIPhoneTaskCore, "task-rollback", false); sequence != 1 {
		t.Fatalf("rolled-back sequence = %d", sequence)
	}
	if sequence := appendChange(mobilev2sync.ScopeIPhoneTaskCore, "task-a", true); sequence != 1 {
		t.Fatalf("first committed sequence = %d", sequence)
	}
	if sequence := appendChange(mobilev2sync.ScopeIPhoneContent, "note-a", true); sequence != 2 {
		t.Fatalf("independent-scope sequence = %d", sequence)
	}
	if sequence := appendChange(mobilev2sync.ScopeIPhoneTaskCore, "task-b", true); sequence != 3 {
		t.Fatalf("second task sequence = %d", sequence)
	}
	if sequence := appendChange(mobilev2sync.ScopeIPhoneTaskCore, "task-c", true); sequence != 4 {
		t.Fatalf("third task sequence = %d", sequence)
	}

	binding := mobilev2sync.TokenBinding{
		WorkspaceID: "workspace-1", Scope: mobilev2sync.ScopeIPhoneTaskCore,
		ContractEpoch: "1", RuntimeEpoch: "1", TaskModelVersion: 2,
		ScopeGeneration: "task-core-generation-1",
	}
	first, err := repository.ReadChanges(ctx, binding, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if !first.HasMore || len(first.Changes) != 2 ||
		first.Changes[0].Sequence != "1" || first.Changes[1].Sequence != "3" {
		t.Fatalf("first change page = %#v", first)
	}
	second, err := repository.ReadChanges(ctx, binding, first.NextCursor, 2)
	if err != nil {
		t.Fatal(err)
	}
	if second.HasMore || len(second.Changes) != 1 || second.Changes[0].Sequence != "4" {
		t.Fatalf("second change page = %#v", second)
	}
	empty, err := repository.ReadChanges(ctx, binding, second.NextCursor, 2)
	if err != nil || empty.HasMore || len(empty.Changes) != 0 || empty.NextCursor != second.NextCursor {
		t.Fatalf("empty change page = %#v err=%v", empty, err)
	}
	mismatch := binding
	mismatch.ScopeGeneration = "task-core-generation-2"
	if _, err := repository.ReadChanges(ctx, mismatch, second.NextCursor, 2); !errors.Is(err, mobilev2sync.ErrCursorMismatch) {
		t.Fatalf("scope generation mismatch error = %v", err)
	}
	if _, err := db.Exec(`INSERT INTO mobile_v2_scope_retention
		(workspace_id,scope,compacted_through_sequence,updated_at) VALUES(?,?,?,?)`,
		"workspace-1", mobilev2sync.ScopeIPhoneTaskCore, 4, now.Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ReadChanges(ctx, binding, "", 2); !errors.Is(err, mobilev2sync.ErrCursorMismatch) {
		t.Fatalf("compacted cursor error = %v", err)
	}
	if page, err := repository.ReadChanges(ctx, binding, second.NextCursor, 2); err != nil || len(page.Changes) != 0 {
		t.Fatalf("cursor at compaction boundary page=%#v err=%v", page, err)
	}
}
