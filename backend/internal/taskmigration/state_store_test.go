package taskmigration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/lib/pq"
	_ "modernc.org/sqlite"
)

func TestStateStoreSQLiteRoundTripsFreshV2AndMigratedCutover(t *testing.T) {
	db := openSQLiteStateStoreTestDB(t)
	store := mustNewStateStore(t, db, DialectSQLite)

	fresh := mustLoadState(t, store, "fresh-v2")
	if fresh.ModelVersion != ModelVersionV2 || fresh.MigrationState != MigrationStateIdle || fresh.AcceptLegacyWrites {
		t.Fatalf("fresh state = %#v", fresh)
	}
	if fresh.WriteEpoch != 3 || fresh.Revision != 1 {
		t.Fatalf("fresh fence = %#v", fresh)
	}

	writtenAt := time.Date(2026, 7, 22, 9, 10, 11, 123456000, time.FixedZone("CST", 8*60*60)).UTC()
	cutoverRevision := uint64(41)
	want := WorkspaceTaskDomainState{
		WorkspaceID:        "migrated-v2",
		ModelVersion:       ModelVersionV2,
		MigrationState:     MigrationStateCutover,
		SourceWatermark:    43,
		CutoverRevision:    &cutoverRevision,
		WriteEpoch:         8,
		AcceptLegacyWrites: false,
		MigrationTimezone:  "Asia/Shanghai",
		V2FirstWriteAt:     &writtenAt,
		MigrationID:        "migration-42",
		LastError:          "",
		Revision:           9,
	}
	insertSQLiteStateStoreRow(t, db, want)
	got := mustLoadState(t, store, want.WorkspaceID)
	assertStateStoreStateEqual(t, got, want)
}

func TestStateStoreSQLiteCompareAndSwapIsIdempotent(t *testing.T) {
	db := openSQLiteStateStoreTestDB(t)
	store := mustNewStateStore(t, db, DialectSQLite)
	expected := mustLoadState(t, store, "fresh-v2")
	writtenAt := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	next, err := MarkV2FirstWrite(expected, MarkV2FirstWriteCommand{
		ExpectedRevision: expected.Revision, ExpectedWriteEpoch: expected.WriteEpoch, WrittenAt: writtenAt,
	})
	if err != nil {
		t.Fatalf("MarkV2FirstWrite: %v", err)
	}

	ctx := context.Background()
	if err := store.CompareAndSwap(ctx, expected, next); err != nil {
		t.Fatalf("CompareAndSwap first: %v", err)
	}
	if err := store.CompareAndSwap(ctx, expected, next); err != nil {
		t.Fatalf("CompareAndSwap retry: %v", err)
	}
	if err := store.CompareAndSwap(ctx, next, next); err != nil {
		t.Fatalf("CompareAndSwap no-op: %v", err)
	}
	assertStateStoreStateEqual(t, mustLoadState(t, store, expected.WorkspaceID), next)
}

func TestStateStoreSQLiteConcurrentCASHasSingleWinner(t *testing.T) {
	db := openSQLiteStateStoreTestDB(t)
	store := mustNewStateStore(t, db, DialectSQLite)
	expected := mustLoadState(t, store, "fresh-v2")

	makeNext := func(minute int) WorkspaceTaskDomainState {
		writtenAt := time.Date(2026, 7, 22, 13, minute, 0, 0, time.UTC)
		next, err := MarkV2FirstWrite(expected, MarkV2FirstWriteCommand{
			ExpectedRevision: expected.Revision, ExpectedWriteEpoch: expected.WriteEpoch, WrittenAt: writtenAt,
		})
		if err != nil {
			t.Fatalf("MarkV2FirstWrite(%d): %v", minute, err)
		}
		return next
	}
	nextA, nextB := makeNext(1), makeNext(2)

	start := make(chan struct{})
	results := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for _, next := range []WorkspaceTaskDomainState{nextA, nextB} {
		next := next
		go func() {
			ready.Done()
			<-start
			results <- store.CompareAndSwap(context.Background(), expected, next)
		}()
	}
	ready.Wait()
	close(start)

	var successes, conflicts int
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrStateCASConflict):
			conflicts++
		default:
			t.Fatalf("concurrent CAS error = %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent results: success=%d conflict=%d", successes, conflicts)
	}
	got := mustLoadState(t, store, expected.WorkspaceID)
	if got.V2FirstWriteAt == nil || (!got.V2FirstWriteAt.Equal(*nextA.V2FirstWriteAt) && !got.V2FirstWriteAt.Equal(*nextB.V2FirstWriteAt)) {
		t.Fatalf("winning state = %#v", got)
	}
}

func TestStateStoreSQLiteEpochUpdateIsAtomicAndRequiresActiveAnchor(t *testing.T) {
	t.Run("updates epoch with domain state", func(t *testing.T) {
		db := openSQLiteStateStoreTestDB(t)
		store := mustNewStateStore(t, db, DialectSQLite)
		expected := mustAdvanceSQLiteStateStoreToCatchingUp(t, store)
		next, err := BeginDrain(expected, BeginDrainCommand{
			ExpectedRevision: expected.Revision, ExpectedWriteEpoch: expected.WriteEpoch, CutoverRevision: expected.SourceWatermark + 1,
		})
		if err != nil {
			t.Fatalf("BeginDrain: %v", err)
		}
		if err := store.CompareAndSwap(context.Background(), expected, next); err != nil {
			t.Fatalf("CompareAndSwap: %v", err)
		}
		assertSQLiteAnchor(t, db, expected.WorkspaceID, next.WriteEpoch, "active")
		assertStateStoreStateEqual(t, mustLoadState(t, store, expected.WorkspaceID), next)
	})

	t.Run("rolls epoch back when state update fails", func(t *testing.T) {
		db := openSQLiteStateStoreTestDB(t)
		store := mustNewStateStore(t, db, DialectSQLite)
		expected := mustAdvanceSQLiteStateStoreToCatchingUp(t, store)
		next, err := BeginDrain(expected, BeginDrainCommand{
			ExpectedRevision: expected.Revision, ExpectedWriteEpoch: expected.WriteEpoch, CutoverRevision: expected.SourceWatermark + 1,
		})
		if err != nil {
			t.Fatalf("BeginDrain: %v", err)
		}
		if _, err := db.Exec(`CREATE TRIGGER reject_domain_state_update BEFORE UPDATE ON workspace_task_domain_state BEGIN SELECT RAISE(ABORT, 'forced state failure'); END`); err != nil {
			t.Fatalf("create failure trigger: %v", err)
		}
		if err := store.CompareAndSwap(context.Background(), expected, next); err == nil {
			t.Fatal("CompareAndSwap succeeded despite forced state failure")
		}
		assertSQLiteAnchor(t, db, expected.WorkspaceID, expected.WriteEpoch, "active")
		assertStateStoreStateEqual(t, mustLoadState(t, store, expected.WorkspaceID), expected)
	})

	t.Run("rejects fenced anchor", func(t *testing.T) {
		db := openSQLiteStateStoreTestDB(t)
		store := mustNewStateStore(t, db, DialectSQLite)
		expected := mustLoadState(t, store, "legacy")
		if _, err := db.Exec(`UPDATE tenant_workspaces SET state='fenced', migration_id='outer-migration' WHERE workspace_id=?`, expected.WorkspaceID); err != nil {
			t.Fatalf("fence anchor: %v", err)
		}
		next, err := StartBackfill(expected, StartBackfillCommand{
			ExpectedRevision: expected.Revision, ExpectedWriteEpoch: expected.WriteEpoch,
			MigrationID: "migration-fenced", MigrationTimezone: "UTC",
		})
		if err != nil {
			t.Fatalf("StartBackfill: %v", err)
		}
		err = store.CompareAndSwap(context.Background(), expected, next)
		if !errors.Is(err, ErrStateCASConflict) {
			t.Fatalf("CompareAndSwap error = %v, want ErrStateCASConflict", err)
		}
		assertSQLiteAnchor(t, db, expected.WorkspaceID, expected.WriteEpoch, "fenced")
	})
}

func TestStateStoreSQLitePersistsPureMigrationCommandSequence(t *testing.T) {
	db := openSQLiteStateStoreTestDB(t)
	store := mustNewStateStore(t, db, DialectSQLite)
	state := mustLoadState(t, store, "legacy")
	initialEpoch := state.WriteEpoch

	advance := func(next WorkspaceTaskDomainState, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("build transition: %v", err)
		}
		if err := store.CompareAndSwap(context.Background(), state, next); err != nil {
			t.Fatalf("persist %s -> %s: %v", state.MigrationState, next.MigrationState, err)
		}
		state = next
	}

	advance(StartBackfill(state, StartBackfillCommand{
		ExpectedRevision: state.Revision, ExpectedWriteEpoch: state.WriteEpoch,
		MigrationID: "migration-success", MigrationTimezone: "Asia/Shanghai",
	}))
	advance(BeginCatchingUp(state, BeginCatchingUpCommand{
		ExpectedRevision: state.Revision, ExpectedWriteEpoch: state.WriteEpoch, SourceWatermark: 20,
	}))
	advance(BeginCatchingUp(state, BeginCatchingUpCommand{
		ExpectedRevision: state.Revision, ExpectedWriteEpoch: state.WriteEpoch, SourceWatermark: 22,
	}))
	advance(BeginDrain(state, BeginDrainCommand{
		ExpectedRevision: state.Revision, ExpectedWriteEpoch: state.WriteEpoch, CutoverRevision: 24,
	}))
	advance(MarkReady(state, MarkReadyCommand{
		ExpectedRevision: state.Revision, ExpectedWriteEpoch: state.WriteEpoch, SourceWatermark: 24,
	}))
	advance(Cutover(state, CutoverCommand{
		ExpectedRevision: state.Revision, ExpectedWriteEpoch: state.WriteEpoch,
		MigrationID: state.MigrationID, CutoverRevision: *state.CutoverRevision,
	}))

	if state.ModelVersion != ModelVersionV2 || state.MigrationState != MigrationStateCutover {
		t.Fatalf("final state = %#v", state)
	}
	if state.WriteEpoch != initialEpoch+1 {
		t.Fatalf("final epoch = %d, want %d", state.WriteEpoch, initialEpoch+1)
	}
	assertSQLiteAnchor(t, db, state.WorkspaceID, state.WriteEpoch, "active")
	assertStateStoreStateEqual(t, mustLoadState(t, store, state.WorkspaceID), state)
}

func TestStateStoreSQLitePersistsFenceFailureAndRecoveryCommands(t *testing.T) {
	db := openSQLiteStateStoreTestDB(t)
	store := mustNewStateStore(t, db, DialectSQLite)
	catchingUp := mustAdvanceSQLiteStateStoreToCatchingUp(t, store)
	draining, err := BeginDrain(catchingUp, BeginDrainCommand{
		ExpectedRevision: catchingUp.Revision, ExpectedWriteEpoch: catchingUp.WriteEpoch, CutoverRevision: 12,
	})
	if err != nil {
		t.Fatalf("BeginDrain: %v", err)
	}
	if err := store.CompareAndSwap(context.Background(), catchingUp, draining); err != nil {
		t.Fatalf("persist draining: %v", err)
	}
	failed, err := Fail(draining, FailCommand{
		ExpectedRevision: draining.Revision, ExpectedWriteEpoch: draining.WriteEpoch, Cause: "reconcile mismatch",
	})
	if err != nil {
		t.Fatalf("Fail: %v", err)
	}
	if err := store.CompareAndSwap(context.Background(), draining, failed); err != nil {
		t.Fatalf("persist failed: %v", err)
	}
	recovered, err := Recover(failed, RecoverCommand{
		ExpectedRevision: failed.Revision, ExpectedWriteEpoch: failed.WriteEpoch,
	})
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if err := store.CompareAndSwap(context.Background(), failed, recovered); err != nil {
		t.Fatalf("persist recovered: %v", err)
	}
	assertSQLiteAnchor(t, db, recovered.WorkspaceID, recovered.WriteEpoch, "active")
	assertStateStoreStateEqual(t, mustLoadState(t, store, recovered.WorkspaceID), recovered)
}

func TestStateStoreRejectsForgedValidStateTransitionsBeforeWriting(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(WorkspaceTaskDomainState) WorkspaceTaskDomainState
	}{
		{name: "write epoch regression", mutate: func(expected WorkspaceTaskDomainState) WorkspaceTaskDomainState {
			next := expected
			next.WriteEpoch--
			next.Revision++
			return next
		}},
		{name: "write epoch jump", mutate: func(expected WorkspaceTaskDomainState) WorkspaceTaskDomainState {
			next := expected
			next.WriteEpoch += 2
			next.Revision++
			return next
		}},
		{name: "write epoch advance without a fence transition", mutate: func(expected WorkspaceTaskDomainState) WorkspaceTaskDomainState {
			next := expected
			next.WriteEpoch++
			next.Revision++
			return next
		}},
		{name: "model version replacement", mutate: func(expected WorkspaceTaskDomainState) WorkspaceTaskDomainState {
			next := expected
			next.ModelVersion = ModelVersionV2
			next.AcceptLegacyWrites = false
			next.MigrationTimezone = ""
			next.Revision++
			return next
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openSQLiteStateStoreTestDB(t)
			store := mustNewStateStore(t, db, DialectSQLite)
			expected := mustLoadState(t, store, "legacy")
			next := test.mutate(expected)
			if err := next.Validate(); err != nil {
				t.Fatalf("test must forge an individually valid next state: %v", err)
			}
			err := store.CompareAndSwap(context.Background(), expected, next)
			if !errors.Is(err, ErrInvalidStateStoreInput) {
				t.Fatalf("CompareAndSwap error = %v, want ErrInvalidStateStoreInput", err)
			}
			assertSQLiteAnchor(t, db, expected.WorkspaceID, expected.WriteEpoch, "active")
			assertStateStoreStateEqual(t, mustLoadState(t, store, expected.WorkspaceID), expected)
		})
	}
}

func TestStateStoreRejectsForgedActiveMigrationIdentityAndProgress(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(WorkspaceTaskDomainState) WorkspaceTaskDomainState
	}{
		{name: "migration id", mutate: func(next WorkspaceTaskDomainState) WorkspaceTaskDomainState {
			next.MigrationID = "replacement"
			return next
		}},
		{name: "migration timezone", mutate: func(next WorkspaceTaskDomainState) WorkspaceTaskDomainState {
			next.MigrationTimezone = "Asia/Shanghai"
			return next
		}},
		{name: "source watermark regression", mutate: func(next WorkspaceTaskDomainState) WorkspaceTaskDomainState { next.SourceWatermark--; return next }},
		{name: "last error outside fail command", mutate: func(next WorkspaceTaskDomainState) WorkspaceTaskDomainState { next.LastError = "forged"; return next }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openSQLiteStateStoreTestDB(t)
			store := mustNewStateStore(t, db, DialectSQLite)
			expected := mustAdvanceSQLiteStateStoreToCatchingUp(t, store)
			next := test.mutate(expected)
			next.Revision++
			if err := next.Validate(); err != nil {
				t.Fatalf("test must forge an individually valid next state: %v", err)
			}
			err := store.CompareAndSwap(context.Background(), expected, next)
			if !errors.Is(err, ErrInvalidStateStoreInput) {
				t.Fatalf("CompareAndSwap error = %v, want ErrInvalidStateStoreInput", err)
			}
			assertStateStoreStateEqual(t, mustLoadState(t, store, expected.WorkspaceID), expected)
		})
	}
}

func TestStateStoreRejectsForgedCutoverRevisionReplacement(t *testing.T) {
	db := openSQLiteStateStoreTestDB(t)
	store := mustNewStateStore(t, db, DialectSQLite)
	catchingUp := mustAdvanceSQLiteStateStoreToCatchingUp(t, store)
	draining, err := BeginDrain(catchingUp, BeginDrainCommand{
		ExpectedRevision: catchingUp.Revision, ExpectedWriteEpoch: catchingUp.WriteEpoch, CutoverRevision: 15,
	})
	if err != nil {
		t.Fatalf("BeginDrain: %v", err)
	}
	if err := store.CompareAndSwap(context.Background(), catchingUp, draining); err != nil {
		t.Fatalf("persist draining: %v", err)
	}

	next := draining
	cutoverRevision := *draining.CutoverRevision + 1
	next.CutoverRevision = &cutoverRevision
	next.Revision++
	if err := next.Validate(); err != nil {
		t.Fatalf("test must forge an individually valid next state: %v", err)
	}
	err = store.CompareAndSwap(context.Background(), draining, next)
	if !errors.Is(err, ErrInvalidStateStoreInput) {
		t.Fatalf("CompareAndSwap error = %v, want ErrInvalidStateStoreInput", err)
	}
	assertStateStoreStateEqual(t, mustLoadState(t, store, draining.WorkspaceID), draining)
}

func TestStateStoreRejectsInvalidTransitionsBeforeWriting(t *testing.T) {
	db := openSQLiteStateStoreTestDB(t)
	store := mustNewStateStore(t, db, DialectSQLite)
	expected := mustLoadState(t, store, "fresh-v2")

	tests := []struct {
		name string
		next WorkspaceTaskDomainState
	}{
		{name: "workspace mismatch", next: func() WorkspaceTaskDomainState {
			next := expected
			next.WorkspaceID = "other"
			next.Revision++
			return next
		}()},
		{name: "revision jump", next: func() WorkspaceTaskDomainState { next := expected; next.Revision += 2; return next }()},
		{name: "invalid next", next: func() WorkspaceTaskDomainState {
			next := expected
			next.Revision++
			next.AcceptLegacyWrites = true
			return next
		}()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := store.CompareAndSwap(context.Background(), expected, test.next); err == nil {
				t.Fatal("CompareAndSwap succeeded")
			}
			assertStateStoreStateEqual(t, mustLoadState(t, store, expected.WorkspaceID), expected)
		})
	}
}

func TestStateStoreSQLiteRejectsEveryDurableCASIdentityMismatch(t *testing.T) {
	tests := []struct {
		name       string
		assignment string
	}{
		{name: "revision", assignment: `revision=revision+1`},
		{name: "write epoch", assignment: `write_epoch=write_epoch+1`},
		{name: "model version", assignment: `model_version='legacy'`},
		{name: "migration id", assignment: `migration_id='unexpected-migration'`},
		{name: "cutover revision", assignment: `cutover_revision=99`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openSQLiteStateStoreTestDB(t)
			store := mustNewStateStore(t, db, DialectSQLite)
			expected := mustLoadState(t, store, "fresh-v2")
			writtenAt := time.Date(2026, 7, 22, 15, 0, 0, 0, time.UTC)
			next, err := MarkV2FirstWrite(expected, MarkV2FirstWriteCommand{
				ExpectedRevision: expected.Revision, ExpectedWriteEpoch: expected.WriteEpoch, WrittenAt: writtenAt,
			})
			if err != nil {
				t.Fatalf("MarkV2FirstWrite: %v", err)
			}
			if _, err := db.Exec(`UPDATE workspace_task_domain_state SET `+test.assignment+` WHERE workspace_id=?`, expected.WorkspaceID); err != nil {
				t.Fatalf("mutate durable CAS identity: %v", err)
			}
			err = store.CompareAndSwap(context.Background(), expected, next)
			if !errors.Is(err, ErrStateCASConflict) {
				t.Fatalf("CompareAndSwap error = %v, want ErrStateCASConflict", err)
			}
		})
	}
}

func TestStateStorePostgresContract(t *testing.T) {
	db := openPostgresStateStoreTestDB(t)
	createPostgresStateStoreSchema(t, db)
	store := mustNewStateStore(t, db, DialectPostgres)
	expected := mustLoadState(t, store, "pg-v2")
	// PostgreSQL TIMESTAMPTZ is microsecond-precise. A retry must still be
	// idempotent when the caller's timestamp contains sub-microsecond data.
	writtenAt := time.Date(2026, 7, 22, 14, 0, 0, 123456789, time.UTC)
	next, err := MarkV2FirstWrite(expected, MarkV2FirstWriteCommand{
		ExpectedRevision: expected.Revision, ExpectedWriteEpoch: expected.WriteEpoch, WrittenAt: writtenAt,
	})
	if err != nil {
		t.Fatalf("MarkV2FirstWrite: %v", err)
	}
	if err := store.CompareAndSwap(context.Background(), expected, next); err != nil {
		t.Fatalf("CompareAndSwap: %v", err)
	}
	if err := store.CompareAndSwap(context.Background(), expected, next); err != nil {
		t.Fatalf("idempotent CompareAndSwap: %v", err)
	}
	assertStateStoreStateEqual(t, mustLoadState(t, store, expected.WorkspaceID), next)
}

func TestStateStorePostgresUsesTenantAnchorBeforeDomainStateLock(t *testing.T) {
	db := openPostgresStateStoreTestDB(t)
	createPostgresStateStoreSchema(t, db)
	store := mustNewStateStore(t, db, DialectPostgres)
	expected := mustLoadState(t, store, "pg-v2")
	next, err := MarkV2FirstWrite(expected, MarkV2FirstWriteCommand{
		ExpectedRevision:   expected.Revision,
		ExpectedWriteEpoch: expected.WriteEpoch,
		WrittenAt:          time.Date(2026, 7, 22, 15, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("MarkV2FirstWrite: %v", err)
	}

	writerTx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer writerTx.Rollback()
	if _, err := writerTx.Exec(`SET LOCAL lock_timeout='3s'`); err != nil {
		t.Fatal(err)
	}
	var epoch int64
	if err := writerTx.QueryRow(`SELECT epoch FROM tenant_workspaces WHERE workspace_id='pg-v2' FOR SHARE`).Scan(&epoch); err != nil {
		t.Fatalf("lock writer anchor: %v", err)
	}

	casDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		casDone <- store.CompareAndSwap(ctx, expected, next)
	}()
	// Give the competing coordinator time to reach its first lock. With the
	// wrong domain-state -> anchor order this creates a deterministic cycle
	// when the task writer records its first successful v2 write below.
	time.Sleep(200 * time.Millisecond)
	if _, err := writerTx.Exec(`UPDATE workspace_task_domain_state
		SET v2_first_write_at=now(),revision=revision+1
		WHERE workspace_id='pg-v2' AND v2_first_write_at IS NULL`); err != nil {
		t.Fatalf("task writer hit lock-order failure: %v", err)
	}
	if err := writerTx.Commit(); err != nil {
		t.Fatalf("commit task writer: %v", err)
	}

	select {
	case err := <-casDone:
		if !errors.Is(err, ErrStateCASConflict) {
			t.Fatalf("state CAS error = %v, want ordinary stale-state conflict after writer commit", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("state CAS did not finish; possible lock-order deadlock")
	}
}

func openSQLiteStateStoreTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:taskmigration-state-store-%d?mode=memory&cache=shared&_pragma=busy_timeout(5000)", time.Now().UnixNano())
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(8)
	t.Cleanup(func() { _ = db.Close() })
	const ddl = `
CREATE TABLE tenant_workspaces (
  workspace_id TEXT PRIMARY KEY,
  epoch INTEGER NOT NULL,
  state TEXT NOT NULL,
  migration_id TEXT
);
CREATE TABLE workspace_task_domain_state (
  workspace_id TEXT PRIMARY KEY,
  model_version TEXT NOT NULL,
  migration_state TEXT NOT NULL,
  source_watermark INTEGER NOT NULL,
  cutover_revision INTEGER,
  write_epoch INTEGER NOT NULL,
  accept_legacy_writes INTEGER NOT NULL,
  migration_timezone TEXT NOT NULL,
  v2_first_write_at TEXT,
  migration_id TEXT,
  last_error TEXT,
  revision INTEGER NOT NULL,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO tenant_workspaces(workspace_id,epoch,state,migration_id) VALUES
  ('fresh-v2',3,'active',NULL), ('legacy',5,'active',NULL), ('migrated-v2',8,'active',NULL);
INSERT INTO workspace_task_domain_state(
  workspace_id,model_version,migration_state,source_watermark,cutover_revision,write_epoch,
  accept_legacy_writes,migration_timezone,v2_first_write_at,migration_id,last_error,revision
) VALUES
  ('fresh-v2','v2','idle',0,NULL,3,0,'UTC',NULL,NULL,NULL,1),
  ('legacy','legacy','idle',0,NULL,5,1,'UTC',NULL,NULL,NULL,1);`
	if _, err := db.Exec(ddl); err != nil {
		t.Fatalf("create sqlite fixture: %v", err)
	}
	return db
}

func insertSQLiteStateStoreRow(t *testing.T, db *sql.DB, state WorkspaceTaskDomainState) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO workspace_task_domain_state(
workspace_id,model_version,migration_state,source_watermark,cutover_revision,write_epoch,
accept_legacy_writes,migration_timezone,v2_first_write_at,migration_id,last_error,revision
) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, state.WorkspaceID, state.ModelVersion, state.MigrationState,
		state.SourceWatermark, nullableStateStoreUint(state.CutoverRevision), state.WriteEpoch,
		state.AcceptLegacyWrites, state.MigrationTimezone, nullableStateStoreTime(state.V2FirstWriteAt),
		nullableStateStoreString(state.MigrationID), nullableStateStoreString(state.LastError), state.Revision)
	if err != nil {
		t.Fatalf("insert state: %v", err)
	}
}

func mustAdvanceSQLiteStateStoreToCatchingUp(t *testing.T, store *StateStore) WorkspaceTaskDomainState {
	t.Helper()
	state := mustLoadState(t, store, "legacy")
	backfilling, err := StartBackfill(state, StartBackfillCommand{
		ExpectedRevision: state.Revision, ExpectedWriteEpoch: state.WriteEpoch,
		MigrationID: "migration-state-store", MigrationTimezone: "UTC",
	})
	if err != nil {
		t.Fatalf("StartBackfill: %v", err)
	}
	if err := store.CompareAndSwap(context.Background(), state, backfilling); err != nil {
		t.Fatalf("persist backfilling: %v", err)
	}
	catchingUp, err := BeginCatchingUp(backfilling, BeginCatchingUpCommand{
		ExpectedRevision: backfilling.Revision, ExpectedWriteEpoch: backfilling.WriteEpoch, SourceWatermark: 11,
	})
	if err != nil {
		t.Fatalf("BeginCatchingUp: %v", err)
	}
	if err := store.CompareAndSwap(context.Background(), backfilling, catchingUp); err != nil {
		t.Fatalf("persist catching up: %v", err)
	}
	return catchingUp
}

func assertSQLiteAnchor(t *testing.T, db *sql.DB, workspaceID string, wantEpoch uint64, wantState string) {
	t.Helper()
	var epoch uint64
	var state string
	if err := db.QueryRow(`SELECT epoch,state FROM tenant_workspaces WHERE workspace_id=?`, workspaceID).Scan(&epoch, &state); err != nil {
		t.Fatalf("read anchor: %v", err)
	}
	if epoch != wantEpoch || state != wantState {
		t.Fatalf("anchor = epoch %d state %q, want %d/%q", epoch, state, wantEpoch, wantState)
	}
}

func mustNewStateStore(t *testing.T, db *sql.DB, dialect Dialect) *StateStore {
	t.Helper()
	store, err := NewStateStore(db, dialect)
	if err != nil {
		t.Fatalf("NewStateStore: %v", err)
	}
	return store
}

func mustLoadState(t *testing.T, store *StateStore, workspaceID string) WorkspaceTaskDomainState {
	t.Helper()
	state, err := store.Load(context.Background(), workspaceID)
	if err != nil {
		t.Fatalf("Load(%q): %v", workspaceID, err)
	}
	return state
}

func assertStateStoreStateEqual(t *testing.T, got, want WorkspaceTaskDomainState) {
	t.Helper()
	if got.WorkspaceID != want.WorkspaceID || got.ModelVersion != want.ModelVersion || got.MigrationState != want.MigrationState ||
		got.SourceWatermark != want.SourceWatermark || got.WriteEpoch != want.WriteEpoch ||
		got.AcceptLegacyWrites != want.AcceptLegacyWrites || got.MigrationTimezone != want.MigrationTimezone ||
		got.MigrationID != want.MigrationID || got.LastError != want.LastError || got.Revision != want.Revision ||
		!equalStateStoreUint(got.CutoverRevision, want.CutoverRevision) || !equalStateStoreTime(got.V2FirstWriteAt, want.V2FirstWriteAt) {
		t.Fatalf("state mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

func equalStateStoreUint(left, right *uint64) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func equalStateStoreTime(left, right *time.Time) bool {
	return left == nil && right == nil || left != nil && right != nil && left.UnixMicro() == right.UnixMicro()
}

func nullableStateStoreUint(value *uint64) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableStateStoreTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func nullableStateStoreString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func openPostgresStateStoreTestDB(t *testing.T) *sql.DB {
	t.Helper()
	baseURL := strings.TrimSpace(os.Getenv("FLOWSPACE_TEST_DATABASE_URL"))
	if baseURL == "" {
		t.Skip("FLOWSPACE_TEST_DATABASE_URL is required for PostgreSQL state-store contract")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Hostname() == "" {
		t.Fatalf("invalid FLOWSPACE_TEST_DATABASE_URL")
	}
	if parsed.Hostname() == "192.168.1.20" {
		t.Fatal("FLOWSPACE_TEST_DATABASE_URL points at the retired PostgreSQL host")
	}
	admin, err := sql.Open("pgx", baseURL)
	if err != nil {
		t.Fatalf("open postgres admin: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := admin.PingContext(ctx); err != nil {
		_ = admin.Close()
		t.Fatalf("ping postgres: %v", err)
	}
	schema := fmt.Sprintf("fs_test_taskmigration_state_%d", time.Now().UnixNano())
	quoted := pq.QuoteIdentifier(schema)
	if _, err := admin.ExecContext(ctx, `CREATE SCHEMA `+quoted); err != nil {
		_ = admin.Close()
		t.Fatalf("create schema: %v", err)
	}
	schemaURL := *parsed
	query := schemaURL.Query()
	query.Set("options", "-c search_path="+schema+",public")
	schemaURL.RawQuery = query.Encode()
	db, err := sql.Open("pgx", schemaURL.String())
	if err != nil {
		t.Fatalf("open schema db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = admin.ExecContext(cleanupCtx, `DROP SCHEMA IF EXISTS `+quoted+` CASCADE`)
		_ = admin.Close()
	})
	return db
}

func createPostgresStateStoreSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	const ddl = `
CREATE TABLE tenant_workspaces(workspace_id TEXT PRIMARY KEY, epoch BIGINT NOT NULL, state TEXT NOT NULL, migration_id TEXT);
CREATE TABLE workspace_task_domain_state(
 workspace_id TEXT PRIMARY KEY, model_version TEXT NOT NULL, migration_state TEXT NOT NULL,
 source_watermark BIGINT NOT NULL, cutover_revision BIGINT, write_epoch BIGINT NOT NULL,
 accept_legacy_writes BOOLEAN NOT NULL, migration_timezone TEXT NOT NULL, v2_first_write_at TIMESTAMPTZ,
 migration_id TEXT, last_error TEXT, revision BIGINT NOT NULL, updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO tenant_workspaces VALUES('pg-v2',7,'active',NULL);
INSERT INTO workspace_task_domain_state VALUES('pg-v2','v2','idle',0,NULL,7,FALSE,'UTC',NULL,NULL,NULL,1,now());`
	if _, err := db.Exec(ddl); err != nil {
		t.Fatalf("create postgres state schema: %v", err)
	}
}
