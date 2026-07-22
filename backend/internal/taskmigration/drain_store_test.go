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

func TestDrainStoreSQLiteBeginsWorkspaceScopedDrainAtomically(t *testing.T) {
	db := openSQLiteDrainStoreTestDB(t)
	store := mustNewDrainStore(t, db, DialectSQLite)

	got, err := store.BeginDrain(context.Background(), "alpha", 4, 7, "migration-alpha")
	if err != nil {
		t.Fatalf("BeginDrain: %v", err)
	}
	if got.MigrationState != MigrationStateDraining || got.ModelVersion != ModelVersionLegacy || got.AcceptLegacyWrites {
		t.Fatalf("drained state = %#v", got)
	}
	if got.WriteEpoch != 8 || got.Revision != 5 || got.CutoverRevision == nil || *got.CutoverRevision != 4 {
		t.Fatalf("drain fence = %#v, want epoch=8 revision=5 cutover=4", got)
	}
	assertSQLiteDrainAnchor(t, db, "alpha", 8, "active")
	assertSQLiteDrainAnchor(t, db, "beta", 3, "active")

	var betaState, betaAccept string
	if err := db.QueryRow(`SELECT migration_state,CAST(accept_legacy_writes AS TEXT)
		FROM workspace_task_domain_state WHERE workspace_id='beta'`).Scan(&betaState, &betaAccept); err != nil {
		t.Fatalf("read beta state: %v", err)
	}
	if betaState != "catching_up" || betaAccept != "1" {
		t.Fatalf("beta changed to state=%q accept=%q", betaState, betaAccept)
	}

	result, err := db.Exec(`UPDATE tenant_workspaces SET epoch=epoch
		WHERE workspace_id='alpha' AND epoch=7 AND state='active'`)
	if err != nil {
		t.Fatalf("probe old epoch fence: %v", err)
	}
	if rows, _ := result.RowsAffected(); rows != 0 {
		t.Fatalf("old epoch still passed tenant fence: affected=%d", rows)
	}

	if _, err := db.Exec(`INSERT INTO legacy_tasks(workspace_id,id,title) VALUES('alpha','late','late write')`); err == nil || !containsDrainTestText(err.Error(), "legacy_task_domain_fenced") {
		t.Fatalf("legacy DML error = %v, want legacy_task_domain_fenced", err)
	}
	if _, err := db.Exec(`INSERT INTO legacy_tasks(workspace_id,id,title) VALUES('beta','still-open','write')`); err != nil {
		t.Fatalf("other workspace legacy write was fenced: %v", err)
	}
}

func TestDrainStoreSQLiteRetryIsIdempotentAndConcurrentCallsAdvanceOnce(t *testing.T) {
	db := openSQLiteDrainStoreTestDB(t)
	store := mustNewDrainStore(t, db, DialectSQLite)

	start := make(chan struct{})
	results := make(chan struct {
		state WorkspaceTaskDomainState
		err   error
	}, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for range 2 {
		go func() {
			ready.Done()
			<-start
			state, err := store.BeginDrain(context.Background(), "alpha", 4, 7, "migration-alpha")
			results <- struct {
				state WorkspaceTaskDomainState
				err   error
			}{state: state, err: err}
		}()
	}
	ready.Wait()
	close(start)

	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatalf("concurrent BeginDrain: %v", result.err)
		}
		if result.state.WriteEpoch != 8 || result.state.Revision != 5 {
			t.Fatalf("concurrent result = %#v", result.state)
		}
	}

	retry, err := store.BeginDrain(context.Background(), "alpha", 4, 7, "migration-alpha")
	if err != nil {
		t.Fatalf("idempotent retry: %v", err)
	}
	if retry.WriteEpoch != 8 || retry.Revision != 5 {
		t.Fatalf("retry advanced state twice: %#v", retry)
	}
	assertSQLiteDrainAnchor(t, db, "alpha", 8, "active")
}

func TestDrainStoreSQLiteReturnsStableConflictForEveryFenceMismatch(t *testing.T) {
	tests := []struct {
		name      string
		workspace string
		revision  uint64
		epoch     uint64
		migration string
		mutate    string
	}{
		{name: "missing workspace", workspace: "missing", revision: 4, epoch: 7, migration: "migration-alpha"},
		{name: "stale revision", workspace: "alpha", revision: 3, epoch: 7, migration: "migration-alpha"},
		{name: "stale epoch", workspace: "alpha", revision: 4, epoch: 6, migration: "migration-alpha"},
		{name: "migration id", workspace: "alpha", revision: 4, epoch: 7, migration: "migration-other"},
		{name: "wrong phase", workspace: "alpha", revision: 4, epoch: 7, migration: "migration-alpha", mutate: `UPDATE workspace_task_domain_state SET migration_state='backfilling' WHERE workspace_id='alpha'`},
		{name: "closed legacy fence", workspace: "alpha", revision: 4, epoch: 7, migration: "migration-alpha", mutate: `UPDATE workspace_task_domain_state SET accept_legacy_writes=0 WHERE workspace_id='alpha'`},
		{name: "inactive anchor", workspace: "alpha", revision: 4, epoch: 7, migration: "migration-alpha", mutate: `UPDATE tenant_workspaces SET state='fenced',migration_id='outer' WHERE workspace_id='alpha'`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openSQLiteDrainStoreTestDB(t)
			if test.mutate != "" {
				if _, err := db.Exec(test.mutate); err != nil {
					t.Fatalf("mutate fixture: %v", err)
				}
			}
			store := mustNewDrainStore(t, db, DialectSQLite)
			_, err := store.BeginDrain(context.Background(), test.workspace, test.revision, test.epoch, test.migration)
			if !errors.Is(err, ErrDrainConflict) {
				t.Fatalf("BeginDrain error = %v, want ErrDrainConflict", err)
			}
		})
	}
}

func TestDrainStoreSQLiteRollsBackAnchorWhenStateUpdateFails(t *testing.T) {
	db := openSQLiteDrainStoreTestDB(t)
	if _, err := db.Exec(`CREATE TRIGGER reject_drain_state_update
		BEFORE UPDATE ON workspace_task_domain_state
		WHEN OLD.workspace_id='alpha'
		BEGIN SELECT RAISE(ABORT, 'forced drain failure'); END`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}
	store := mustNewDrainStore(t, db, DialectSQLite)

	if _, err := store.BeginDrain(context.Background(), "alpha", 4, 7, "migration-alpha"); err == nil {
		t.Fatal("BeginDrain succeeded despite forced state failure")
	}
	assertSQLiteDrainAnchor(t, db, "alpha", 7, "active")
	var migrationState string
	var accept bool
	var epoch, revision uint64
	var cutover sql.NullInt64
	if err := db.QueryRow(`SELECT migration_state,accept_legacy_writes,write_epoch,revision,cutover_revision
		FROM workspace_task_domain_state WHERE workspace_id='alpha'`).Scan(&migrationState, &accept, &epoch, &revision, &cutover); err != nil {
		t.Fatalf("read rolled-back state: %v", err)
	}
	if migrationState != "catching_up" || !accept || epoch != 7 || revision != 4 || cutover.Valid {
		t.Fatalf("state was partially updated: state=%q accept=%v epoch=%d revision=%d cutover=%v", migrationState, accept, epoch, revision, cutover)
	}
}

func TestDrainStoreRejectsInvalidConstructionAndCommandInput(t *testing.T) {
	db := openSQLiteDrainStoreTestDB(t)
	if _, err := NewDrainStore(nil, DialectSQLite); !errors.Is(err, ErrInvalidDrainStoreInput) {
		t.Fatalf("nil DB error = %v", err)
	}
	if _, err := NewDrainStore(db, Dialect("oracle")); !errors.Is(err, ErrInvalidDrainStoreInput) {
		t.Fatalf("unknown dialect error = %v", err)
	}
	store := mustNewDrainStore(t, db, DialectSQLite)
	for _, command := range []struct {
		ctx       context.Context
		workspace string
		revision  uint64
		epoch     uint64
		migration string
	}{
		{ctx: nil, workspace: "alpha", revision: 4, epoch: 7, migration: "migration-alpha"},
		{ctx: context.Background(), workspace: " ", revision: 4, epoch: 7, migration: "migration-alpha"},
		{ctx: context.Background(), workspace: "alpha", revision: 0, epoch: 7, migration: "migration-alpha"},
		{ctx: context.Background(), workspace: "alpha", revision: 4, epoch: 0, migration: "migration-alpha"},
		{ctx: context.Background(), workspace: "alpha", revision: 4, epoch: 7, migration: " "},
	} {
		if _, err := store.BeginDrain(command.ctx, command.workspace, command.revision, command.epoch, command.migration); !errors.Is(err, ErrInvalidDrainStoreInput) {
			t.Fatalf("invalid command %#v error = %v", command, err)
		}
	}
}

func TestDrainStorePostgresWaitsForLegacySharedLock(t *testing.T) {
	db := openPostgresDrainStoreTestDB(t)
	store := mustNewDrainStore(t, db, DialectPostgres)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	legacyTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin legacy transaction: %v", err)
	}
	defer legacyTx.Rollback()
	// This is the production TenantWriter lock order: admit the write under an
	// anchor share lock, then let the callback/legacy trigger touch the domain
	// state. Drain must take the same order with stronger locks or the two
	// transactions can deadlock.
	var admittedEpoch int64
	if err := legacyTx.QueryRowContext(ctx, `SELECT epoch FROM tenant_workspaces
		WHERE workspace_id='alpha' AND state='active' FOR SHARE`).Scan(&admittedEpoch); err != nil {
		t.Fatalf("admit fenced tenant write: %v", err)
	}
	if admittedEpoch != 7 {
		t.Fatalf("admitted epoch = %d, want 7", admittedEpoch)
	}
	var acceptsLegacy bool
	if err := legacyTx.QueryRowContext(ctx, `SELECT accept_legacy_writes
		FROM workspace_task_domain_state WHERE workspace_id='alpha' FOR SHARE`).Scan(&acceptsLegacy); err != nil {
		t.Fatalf("lock legacy fence for share: %v", err)
	}
	if !acceptsLegacy {
		t.Fatal("legacy fixture unexpectedly fenced")
	}
	if _, err := legacyTx.ExecContext(ctx, `INSERT INTO task_domain_legacy_outbox(
		workspace_id,entity_kind,entity_id,operation,source_logical_version,row_image)
		VALUES('alpha','task','a-late','upsert',1,'{}'::jsonb)`); err != nil {
		t.Fatalf("insert legacy outbox event: %v", err)
	}

	type drainResult struct {
		state WorkspaceTaskDomainState
		err   error
	}
	result := make(chan drainResult, 1)
	go func() {
		state, err := store.BeginDrain(ctx, "alpha", 4, 7, "migration-alpha")
		result <- drainResult{state: state, err: err}
	}()
	select {
	case early := <-result:
		t.Fatalf("drain did not wait for legacy shared lock: %#v / %v", early.state, early.err)
	case <-time.After(150 * time.Millisecond):
	}
	if err := legacyTx.Commit(); err != nil {
		t.Fatalf("commit legacy transaction: %v", err)
	}
	completed := <-result
	if completed.err != nil {
		t.Fatalf("BeginDrain after legacy commit: %v", completed.err)
	}
	if completed.state.CutoverRevision == nil || *completed.state.CutoverRevision != 2 ||
		completed.state.WriteEpoch != 8 || completed.state.Revision != 5 {
		t.Fatalf("PostgreSQL drained state = %#v", completed.state)
	}
	assertPostgresDrainAnchor(t, db, "alpha", 8, "active")

	retry, err := store.BeginDrain(ctx, "alpha", 4, 7, "migration-alpha")
	if err != nil {
		t.Fatalf("PostgreSQL idempotent retry: %v", err)
	}
	if retry.Revision != 5 || retry.WriteEpoch != 8 {
		t.Fatalf("PostgreSQL retry advanced twice: %#v", retry)
	}
}

func openSQLiteDrainStoreTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:taskmigration-drain-%d?mode=memory&cache=shared&_pragma=busy_timeout(5000)", time.Now().UnixNano())
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(8)
	t.Cleanup(func() { _ = db.Close() })
	const ddl = `
CREATE TABLE tenant_workspaces(workspace_id TEXT PRIMARY KEY,epoch INTEGER NOT NULL,state TEXT NOT NULL,migration_id TEXT);
CREATE TABLE workspace_task_domain_state(
 workspace_id TEXT PRIMARY KEY,model_version TEXT NOT NULL,migration_state TEXT NOT NULL,
 source_watermark INTEGER NOT NULL,cutover_revision INTEGER,write_epoch INTEGER NOT NULL,
 accept_legacy_writes INTEGER NOT NULL,migration_timezone TEXT NOT NULL,v2_first_write_at TEXT,
 migration_id TEXT,last_error TEXT,revision INTEGER NOT NULL,updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE task_domain_legacy_outbox(
 sequence INTEGER PRIMARY KEY AUTOINCREMENT,workspace_id TEXT NOT NULL,entity_kind TEXT NOT NULL,
 entity_id TEXT NOT NULL,operation TEXT NOT NULL,source_logical_version INTEGER NOT NULL,
 row_image TEXT,tombstone_image TEXT
);
CREATE TABLE legacy_tasks(workspace_id TEXT NOT NULL,id TEXT NOT NULL,title TEXT NOT NULL,PRIMARY KEY(workspace_id,id));
CREATE TRIGGER legacy_tasks_outbox_fence AFTER INSERT ON legacy_tasks BEGIN
 SELECT RAISE(ABORT,'legacy_task_domain_fenced') WHERE NOT EXISTS(
   SELECT 1 FROM workspace_task_domain_state
   WHERE workspace_id=NEW.workspace_id AND accept_legacy_writes=1
 );
END;
INSERT INTO tenant_workspaces VALUES('alpha',7,'active',NULL),('beta',3,'active',NULL);
INSERT INTO workspace_task_domain_state VALUES
 ('alpha','legacy','catching_up',1,NULL,7,1,'Asia/Shanghai',NULL,'migration-alpha',NULL,4,CURRENT_TIMESTAMP),
 ('beta','legacy','catching_up',0,NULL,3,1,'UTC',NULL,'migration-beta',NULL,2,CURRENT_TIMESTAMP);
INSERT INTO task_domain_legacy_outbox(workspace_id,entity_kind,entity_id,operation,source_logical_version,row_image)
VALUES
 ('beta','task','b-1','upsert',1,'{}'),
 ('alpha','task','a-1','upsert',1,'{}'),
 ('alpha','task','a-2','upsert',1,'{}'),
 ('alpha','task','a-3','upsert',1,'{}'),
 ('beta','task','b-2','upsert',1,'{}');`
	if _, err := db.Exec(ddl); err != nil {
		t.Fatalf("create drain fixture: %v", err)
	}
	return db
}

func mustNewDrainStore(t *testing.T, db *sql.DB, dialect Dialect) *DrainStore {
	t.Helper()
	store, err := NewDrainStore(db, dialect)
	if err != nil {
		t.Fatalf("NewDrainStore: %v", err)
	}
	return store
}

func assertSQLiteDrainAnchor(t *testing.T, db *sql.DB, workspaceID string, wantEpoch uint64, wantState string) {
	t.Helper()
	var epoch uint64
	var state string
	if err := db.QueryRow(`SELECT epoch,state FROM tenant_workspaces WHERE workspace_id=?`, workspaceID).Scan(&epoch, &state); err != nil {
		t.Fatalf("read anchor %q: %v", workspaceID, err)
	}
	if epoch != wantEpoch || state != wantState {
		t.Fatalf("anchor %q = %d/%q, want %d/%q", workspaceID, epoch, state, wantEpoch, wantState)
	}
}

func containsDrainTestText(value, fragment string) bool {
	for index := 0; index+len(fragment) <= len(value); index++ {
		if value[index:index+len(fragment)] == fragment {
			return true
		}
	}
	return false
}

func openPostgresDrainStoreTestDB(t *testing.T) *sql.DB {
	t.Helper()
	baseURL := strings.TrimSpace(os.Getenv("FLOWSPACE_TEST_DATABASE_URL"))
	if baseURL == "" {
		t.Skip("FLOWSPACE_TEST_DATABASE_URL is required for PostgreSQL drain-store contract")
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
	schema := fmt.Sprintf("fs_test_taskmigration_drain_%d", time.Now().UnixNano())
	quoted := pq.QuoteIdentifier(schema)
	if _, err := admin.ExecContext(ctx, `CREATE SCHEMA `+quoted); err != nil {
		_ = admin.Close()
		t.Fatalf("create postgres drain schema: %v", err)
	}
	schemaURL := *parsed
	query := schemaURL.Query()
	query.Set("options", "-c search_path="+schema+",public")
	schemaURL.RawQuery = query.Encode()
	db, err := sql.Open("pgx", schemaURL.String())
	if err != nil {
		t.Fatalf("open postgres drain schema: %v", err)
	}
	db.SetMaxOpenConns(8)
	t.Cleanup(func() {
		_ = db.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = admin.ExecContext(cleanupCtx, `DROP SCHEMA IF EXISTS `+quoted+` CASCADE`)
		_ = admin.Close()
	})
	const ddl = `
CREATE TABLE tenant_workspaces(workspace_id TEXT PRIMARY KEY,epoch BIGINT NOT NULL,state TEXT NOT NULL,migration_id TEXT);
CREATE TABLE workspace_task_domain_state(
 workspace_id TEXT PRIMARY KEY,model_version TEXT NOT NULL,migration_state TEXT NOT NULL,
 source_watermark BIGINT NOT NULL,cutover_revision BIGINT,write_epoch BIGINT NOT NULL,
 accept_legacy_writes BOOLEAN NOT NULL,migration_timezone TEXT NOT NULL,v2_first_write_at TIMESTAMPTZ,
 migration_id TEXT,last_error TEXT,revision BIGINT NOT NULL,updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE task_domain_legacy_outbox(
 sequence BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,workspace_id TEXT NOT NULL,entity_kind TEXT NOT NULL,
 entity_id TEXT NOT NULL,operation TEXT NOT NULL,source_logical_version BIGINT NOT NULL,
 row_image JSONB,tombstone_image JSONB
);
INSERT INTO tenant_workspaces VALUES('alpha',7,'active',NULL),('beta',3,'active',NULL);
INSERT INTO workspace_task_domain_state VALUES
 ('alpha','legacy','catching_up',0,NULL,7,TRUE,'Asia/Shanghai',NULL,'migration-alpha',NULL,4,now()),
 ('beta','legacy','catching_up',0,NULL,3,TRUE,'UTC',NULL,'migration-beta',NULL,2,now());
INSERT INTO task_domain_legacy_outbox(workspace_id,entity_kind,entity_id,operation,source_logical_version,row_image)
VALUES('alpha','task','a-1','upsert',1,'{}'::jsonb);`
	if _, err := db.ExecContext(ctx, ddl); err != nil {
		t.Fatalf("create postgres drain fixture: %v", err)
	}
	return db
}

func assertPostgresDrainAnchor(t *testing.T, db *sql.DB, workspaceID string, wantEpoch uint64, wantState string) {
	t.Helper()
	var epoch uint64
	var state string
	if err := db.QueryRow(`SELECT epoch,state FROM tenant_workspaces WHERE workspace_id=$1`, workspaceID).Scan(&epoch, &state); err != nil {
		t.Fatalf("read PostgreSQL anchor %q: %v", workspaceID, err)
	}
	if epoch != wantEpoch || state != wantState {
		t.Fatalf("PostgreSQL anchor %q = %d/%q, want %d/%q", workspaceID, epoch, state, wantEpoch, wantState)
	}
}
