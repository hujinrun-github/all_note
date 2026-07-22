package taskmigration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestReplayStoreFetchPageUsesWorkspaceWatermarkAndAllowsGlobalGaps(t *testing.T) {
	db := openSQLiteReplayStoreTestDB(t)
	store := mustNewReplayStore(t, db, DialectSQLite)
	insertReplayOutbox(t, db, "alpha", "project", "p-1", "upsert", 1, `{"title":"Alpha","priority":2}`, nil)
	insertReplayOutbox(t, db, "beta", "task", "ignored", "upsert", 1, `{"title":"Other"}`, nil)
	insertReplayOutbox(t, db, "alpha", "project", "p-1", "delete", 2, nil, `{"id":"p-1","title":"Alpha"}`)

	page, err := store.FetchPage(context.Background(), "alpha", 10)
	if err != nil {
		t.Fatalf("FetchPage: %v", err)
	}
	if page.WorkspaceID != "alpha" || page.MigrationID != "migration-alpha" || page.FromWatermark != 0 || page.ToWatermark != 3 {
		t.Fatalf("page bounds = %#v", page)
	}
	if len(page.Events) != 2 || page.Events[0].Sequence != 1 || page.Events[1].Sequence != 3 {
		t.Fatalf("events = %#v", page.Events)
	}
	if page.Events[0].AfterImage["title"] != "Alpha" || page.Events[0].AfterImage["priority"] != "2" {
		t.Fatalf("after image = %#v", page.Events[0].AfterImage)
	}
	if page.Events[1].Operation != ReplayDelete || page.Events[1].TombstoneImage["id"] != "p-1" || page.Events[1].AfterImage != nil {
		t.Fatalf("tombstone event = %#v", page.Events[1])
	}
}

func TestReplayStoreCommitRejectsForgedPageThatSkipsDurableWorkspaceEvent(t *testing.T) {
	db := openSQLiteReplayStoreTestDB(t)
	store := mustNewReplayStore(t, db, DialectSQLite)
	insertReplayOutbox(t, db, "alpha", "project", "p-1", "upsert", 1, `{"title":"one"}`, nil)
	insertReplayOutbox(t, db, "beta", "task", "ignored", "upsert", 1, `{"title":"other"}`, nil)
	insertReplayOutbox(t, db, "alpha", "task", "t-1", "upsert", 1, `{"title":"two"}`, nil)
	insertReplayOutbox(t, db, "alpha", "occurrence", "o-1", "upsert", 1, `{"status":"open"}`, nil)

	page, err := store.FetchPage(context.Background(), "alpha", 10)
	if err != nil {
		t.Fatalf("FetchPage: %v", err)
	}
	forged := cloneReplayPageForTest(page)
	forged.Events = []ReplayEvent{forged.Events[0], forged.Events[2]}
	called := false
	err = store.CommitPage(context.Background(), forged, func(context.Context, *sql.Tx, []ReplayEvent) error {
		called = true
		return nil
	})
	if !errors.Is(err, ErrInvalidReplayPage) {
		t.Fatalf("CommitPage forged gap error = %v, want ErrInvalidReplayPage", err)
	}
	if called {
		t.Fatal("projector called for a page that skipped a durable event")
	}
	assertReplayWatermark(t, db, "alpha", 0, 1)
}

func TestReplayStoreCommitRejectsPageThatDoesNotMatchDurableEnvelope(t *testing.T) {
	db := openSQLiteReplayStoreTestDB(t)
	store := mustNewReplayStore(t, db, DialectSQLite)
	insertReplayOutbox(t, db, "alpha", "project", "p-1", "upsert", 3, `{"title":"one","priority":2}`, nil)
	page, err := store.FetchPage(context.Background(), "alpha", 10)
	if err != nil {
		t.Fatalf("FetchPage: %v", err)
	}

	tests := []struct {
		name    string
		edit    func(*ReplayPage)
		wantErr error
	}{
		{name: "workspace", wantErr: ErrInvalidReplayPage, edit: func(page *ReplayPage) { page.WorkspaceID = "beta"; page.MigrationID = "migration-beta" }},
		{name: "migration", wantErr: ErrReplayWatermarkConflict, edit: func(page *ReplayPage) { page.MigrationID = "migration-forged" }},
		{name: "entity kind", wantErr: ErrInvalidReplayPage, edit: func(page *ReplayPage) { page.Events[0].EntityKind = ReplayEntityTask }},
		{name: "source id", wantErr: ErrInvalidReplayPage, edit: func(page *ReplayPage) { page.Events[0].SourceID = "p-forged" }},
		{name: "logical version", wantErr: ErrInvalidReplayPage, edit: func(page *ReplayPage) { page.Events[0].LogicalVersion++ }},
		{name: "operation", wantErr: ErrInvalidReplayPage, edit: func(page *ReplayPage) {
			page.Events[0].Operation = ReplayDelete
			page.Events[0].AfterImage = nil
			page.Events[0].TombstoneImage = ReplayImage{"id": "p-1"}
		}},
		{name: "payload", wantErr: ErrInvalidReplayPage, edit: func(page *ReplayPage) { page.Events[0].AfterImage["title"] = "forged" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := cloneReplayPageForTest(page)
			candidate.Events = cloneReplayEvents(page.Events)
			test.edit(&candidate)
			called := false
			err := store.CommitPage(context.Background(), candidate, func(context.Context, *sql.Tx, []ReplayEvent) error {
				called = true
				return nil
			})
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("CommitPage tampered envelope error = %v, want %v", err, test.wantErr)
			}
			if called {
				t.Fatal("projector called for a page that differs from durable outbox")
			}
		})
	}
	assertReplayWatermark(t, db, "alpha", 0, 1)
}

func TestReplayStoreIdempotentRetryStillVerifiesDurablePage(t *testing.T) {
	db := openSQLiteReplayStoreTestDB(t)
	store := mustNewReplayStore(t, db, DialectSQLite)
	insertReplayOutbox(t, db, "alpha", "project", "p-1", "upsert", 1, `{"title":"one"}`, nil)
	page, err := store.FetchPage(context.Background(), "alpha", 10)
	if err != nil {
		t.Fatalf("FetchPage: %v", err)
	}
	if err := store.CommitPage(context.Background(), page, recordReplayProjection); err != nil {
		t.Fatalf("CommitPage first: %v", err)
	}
	forged := cloneReplayPageForTest(page)
	forged.Events = cloneReplayEvents(page.Events)
	forged.Events[0].AfterImage["title"] = "forged retry"
	called := false
	err = store.CommitPage(context.Background(), forged, func(context.Context, *sql.Tx, []ReplayEvent) error {
		called = true
		return nil
	})
	if !errors.Is(err, ErrInvalidReplayPage) {
		t.Fatalf("CommitPage forged idempotent retry error = %v, want ErrInvalidReplayPage", err)
	}
	if called {
		t.Fatal("projector called for forged idempotent retry")
	}
}

func TestReplayStoreFetchPageResumesAfterCommittedWatermark(t *testing.T) {
	db := openSQLiteReplayStoreTestDB(t)
	store := mustNewReplayStore(t, db, DialectSQLite)
	insertReplayOutbox(t, db, "alpha", "project", "p-1", "upsert", 1, `{"title":"one"}`, nil)
	insertReplayOutbox(t, db, "alpha", "task", "t-1", "upsert", 1, `{"title":"two"}`, nil)

	first, err := store.FetchPage(context.Background(), "alpha", 1)
	if err != nil {
		t.Fatalf("FetchPage first: %v", err)
	}
	if err := store.CommitPage(context.Background(), first, recordReplayProjection); err != nil {
		t.Fatalf("CommitPage first: %v", err)
	}
	second, err := store.FetchPage(context.Background(), "alpha", 10)
	if err != nil {
		t.Fatalf("FetchPage second: %v", err)
	}
	if second.FromWatermark != first.ToWatermark || second.ToWatermark != 2 || len(second.Events) != 1 || second.Events[0].Sequence != 2 {
		t.Fatalf("resumed page = %#v", second)
	}
}

func TestReplayStoreCommitRollsBackProjectionAndWatermarkTogether(t *testing.T) {
	db := openSQLiteReplayStoreTestDB(t)
	store := mustNewReplayStore(t, db, DialectSQLite)
	insertReplayOutbox(t, db, "alpha", "project", "p-1", "upsert", 1, `{"title":"one"}`, nil)
	page, err := store.FetchPage(context.Background(), "alpha", 10)
	if err != nil {
		t.Fatalf("FetchPage: %v", err)
	}

	wantErr := errors.New("projection interrupted")
	err = store.CommitPage(context.Background(), page, func(ctx context.Context, tx *sql.Tx, events []ReplayEvent) error {
		if err := recordReplayProjection(ctx, tx, events); err != nil {
			return err
		}
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("CommitPage error = %v, want %v", err, wantErr)
	}
	assertReplayWatermark(t, db, "alpha", 0, 1)
	assertReplayProjectionCount(t, db, 0)

	if err := store.CommitPage(context.Background(), page, recordReplayProjection); err != nil {
		t.Fatalf("CommitPage retry: %v", err)
	}
	assertReplayWatermark(t, db, "alpha", page.ToWatermark, 2)
	assertReplayProjectionCount(t, db, 1)
}

func TestReplayStoreCommitIsIdempotentWithoutReplayingProjector(t *testing.T) {
	db := openSQLiteReplayStoreTestDB(t)
	store := mustNewReplayStore(t, db, DialectSQLite)
	insertReplayOutbox(t, db, "alpha", "project", "p-1", "upsert", 1, `{"title":"one"}`, nil)
	page, err := store.FetchPage(context.Background(), "alpha", 10)
	if err != nil {
		t.Fatalf("FetchPage: %v", err)
	}
	var calls int
	project := func(ctx context.Context, tx *sql.Tx, events []ReplayEvent) error {
		calls++
		return recordReplayProjection(ctx, tx, events)
	}
	if err := store.CommitPage(context.Background(), page, project); err != nil {
		t.Fatalf("CommitPage first: %v", err)
	}
	if err := store.CommitPage(context.Background(), page, project); err != nil {
		t.Fatalf("CommitPage retry: %v", err)
	}
	if calls != 1 {
		t.Fatalf("projector calls = %d, want 1", calls)
	}
	assertReplayProjectionCount(t, db, 1)
}

func TestReplayStoreSQLiteConcurrentCommitHasOneProjectorWinner(t *testing.T) {
	db := openSQLiteReplayStoreTestDB(t)
	store := mustNewReplayStore(t, db, DialectSQLite)
	insertReplayOutbox(t, db, "alpha", "project", "p-1", "upsert", 1, `{"title":"one"}`, nil)
	page, err := store.FetchPage(context.Background(), "alpha", 10)
	if err != nil {
		t.Fatalf("FetchPage: %v", err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	var ready sync.WaitGroup
	var calls atomic.Int32
	ready.Add(2)
	for range 2 {
		go func() {
			ready.Done()
			<-start
			results <- store.CommitPage(context.Background(), page, func(ctx context.Context, tx *sql.Tx, events []ReplayEvent) error {
				calls.Add(1)
				time.Sleep(25 * time.Millisecond)
				return recordReplayProjection(ctx, tx, events)
			})
		}()
	}
	ready.Wait()
	close(start)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("concurrent CommitPage: %v", err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("projector calls = %d, want 1", calls.Load())
	}
	assertReplayProjectionCount(t, db, 1)
}

func TestReplayStoreCommitRejectsAStaleOrAdvancedWatermark(t *testing.T) {
	db := openSQLiteReplayStoreTestDB(t)
	store := mustNewReplayStore(t, db, DialectSQLite)
	insertReplayOutbox(t, db, "alpha", "project", "p-1", "upsert", 1, `{"title":"one"}`, nil)
	page, err := store.FetchPage(context.Background(), "alpha", 10)
	if err != nil {
		t.Fatalf("FetchPage: %v", err)
	}
	if _, err := db.Exec(`UPDATE workspace_task_domain_state SET source_watermark=9 WHERE workspace_id='alpha'`); err != nil {
		t.Fatalf("advance watermark: %v", err)
	}
	called := false
	err = store.CommitPage(context.Background(), page, func(context.Context, *sql.Tx, []ReplayEvent) error {
		called = true
		return nil
	})
	if !errors.Is(err, ErrReplayWatermarkConflict) {
		t.Fatalf("CommitPage error = %v, want ErrReplayWatermarkConflict", err)
	}
	if called {
		t.Fatal("projector called despite watermark conflict")
	}
}

func TestReplayStoreRejectsInvalidInputsAndTamperedPages(t *testing.T) {
	db := openSQLiteReplayStoreTestDB(t)
	if _, err := NewReplayStore(nil, DialectSQLite); err == nil {
		t.Fatal("NewReplayStore accepted nil database")
	}
	if _, err := NewReplayStore(db, Dialect("mysql")); err == nil {
		t.Fatal("NewReplayStore accepted unknown dialect")
	}
	store := mustNewReplayStore(t, db, DialectSQLite)
	if _, err := store.FetchPage(context.Background(), "", 10); !errors.Is(err, ErrInvalidReplayPage) {
		t.Fatalf("empty workspace error = %v", err)
	}
	if _, err := store.FetchPage(context.Background(), "alpha", 0); !errors.Is(err, ErrInvalidReplayPage) {
		t.Fatalf("zero limit error = %v", err)
	}
	insertReplayOutbox(t, db, "alpha", "project", "p-1", "upsert", 1, `{"title":"one"}`, nil)
	insertReplayOutbox(t, db, "alpha", "task", "t-1", "upsert", 1, `{"title":"two"}`, nil)
	page, err := store.FetchPage(context.Background(), "alpha", 10)
	if err != nil {
		t.Fatalf("FetchPage: %v", err)
	}

	tests := []struct {
		name string
		edit func(*ReplayPage)
	}{
		{name: "empty workspace", edit: func(page *ReplayPage) { page.WorkspaceID = "" }},
		{name: "empty events", edit: func(page *ReplayPage) { page.Events = nil }},
		{name: "to skips fetched last", edit: func(page *ReplayPage) { page.ToWatermark++ }},
		{name: "to before last", edit: func(page *ReplayPage) { page.ToWatermark-- }},
		{name: "out of order", edit: func(page *ReplayPage) { page.Events[0], page.Events[1] = page.Events[1], page.Events[0] }},
		{name: "event at from", edit: func(page *ReplayPage) { page.Events[0].Sequence = page.FromWatermark }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := cloneReplayPageForTest(page)
			test.edit(&candidate)
			called := false
			err := store.CommitPage(context.Background(), candidate, func(context.Context, *sql.Tx, []ReplayEvent) error {
				called = true
				return nil
			})
			if !errors.Is(err, ErrInvalidReplayPage) {
				t.Fatalf("CommitPage error = %v, want ErrInvalidReplayPage", err)
			}
			if called {
				t.Fatal("projector called for invalid page")
			}
		})
	}
	if err := store.CommitPage(context.Background(), page, nil); !errors.Is(err, ErrInvalidReplayPage) {
		t.Fatalf("nil projector error = %v", err)
	}
}

func TestReplayStoreFetchRejectsMalformedJSONImages(t *testing.T) {
	db := openSQLiteReplayStoreTestDB(t)
	store := mustNewReplayStore(t, db, DialectSQLite)
	if _, err := db.Exec(`INSERT INTO task_domain_legacy_outbox(
workspace_id,entity_kind,entity_id,operation,source_logical_version,row_image,tombstone_image
) VALUES('alpha','project','p-1','upsert',1,'[]',NULL)`); err != nil {
		t.Fatalf("insert malformed fixture: %v", err)
	}
	if _, err := store.FetchPage(context.Background(), "alpha", 10); err == nil {
		t.Fatal("FetchPage accepted a non-object image")
	}
}

func TestReplayStoreFetchEmptyPageKeepsCurrentWatermark(t *testing.T) {
	db := openSQLiteReplayStoreTestDB(t)
	store := mustNewReplayStore(t, db, DialectSQLite)
	if _, err := db.Exec(`UPDATE workspace_task_domain_state SET source_watermark=17 WHERE workspace_id='alpha'`); err != nil {
		t.Fatalf("seed watermark: %v", err)
	}
	page, err := store.FetchPage(context.Background(), "alpha", 10)
	if err != nil {
		t.Fatalf("FetchPage: %v", err)
	}
	if page.FromWatermark != 17 || page.ToWatermark != 17 || len(page.Events) != 0 {
		t.Fatalf("empty page = %#v", page)
	}
	if err := store.CommitPage(context.Background(), page, recordReplayProjection); !errors.Is(err, ErrInvalidReplayPage) {
		t.Fatalf("CommitPage empty error = %v", err)
	}
}

func TestReplayStorePostgresContract(t *testing.T) {
	db := openPostgresStateStoreTestDB(t)
	const ddl = `
CREATE TABLE workspace_task_domain_state (
  workspace_id TEXT PRIMARY KEY,
  source_watermark BIGINT NOT NULL DEFAULT 0,
  migration_id TEXT NOT NULL,
  model_version TEXT NOT NULL DEFAULT 'legacy',
  migration_state TEXT NOT NULL DEFAULT 'catching_up',
  cutover_revision BIGINT,
  revision BIGINT NOT NULL DEFAULT 1,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE task_domain_legacy_outbox (
  sequence BIGSERIAL PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  entity_kind TEXT NOT NULL,
  entity_id TEXT NOT NULL,
  operation TEXT NOT NULL,
  source_logical_version BIGINT NOT NULL,
  row_image JSONB,
  tombstone_image JSONB
);
CREATE TABLE replay_projection (
  sequence BIGINT PRIMARY KEY,
  entity_id TEXT NOT NULL
);
INSERT INTO workspace_task_domain_state(workspace_id,migration_id) VALUES('pg-alpha','migration-pg-alpha'),('pg-beta','migration-pg-beta');
INSERT INTO task_domain_legacy_outbox(workspace_id,entity_kind,entity_id,operation,source_logical_version,row_image)
VALUES('pg-alpha','project','p-1','upsert',1,'{"title":"one"}'::jsonb),
      ('pg-beta','task','ignored','upsert',1,'{"title":"other"}'::jsonb),
      ('pg-alpha','task','t-1','upsert',1,'{"title":"two"}'::jsonb);`
	if _, err := db.Exec(ddl); err != nil {
		t.Fatalf("create PostgreSQL replay fixture: %v", err)
	}
	store := mustNewReplayStore(t, db, DialectPostgres)
	page, err := store.FetchPage(context.Background(), "pg-alpha", 10)
	if err != nil {
		t.Fatalf("FetchPage: %v", err)
	}
	if len(page.Events) != 2 || page.Events[0].Sequence != 1 || page.Events[1].Sequence != 3 {
		t.Fatalf("PostgreSQL page = %#v", page)
	}
	if err := store.CommitPage(context.Background(), page, func(ctx context.Context, tx *sql.Tx, events []ReplayEvent) error {
		for _, event := range events {
			if _, err := tx.ExecContext(ctx, `INSERT INTO replay_projection(sequence,entity_id) VALUES($1,$2)`, event.Sequence, event.SourceID); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("CommitPage: %v", err)
	}
	if err := store.CommitPage(context.Background(), page, func(context.Context, *sql.Tx, []ReplayEvent) error {
		t.Fatal("idempotent PostgreSQL commit called projector")
		return nil
	}); err != nil {
		t.Fatalf("CommitPage idempotent: %v", err)
	}
}

func openSQLiteReplayStoreTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:taskmigration-replay-store-%d?mode=memory&cache=shared&_pragma=busy_timeout(5000)", time.Now().UnixNano())
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(8)
	t.Cleanup(func() { _ = db.Close() })
	const ddl = `
CREATE TABLE workspace_task_domain_state (
  workspace_id TEXT PRIMARY KEY,
  source_watermark INTEGER NOT NULL DEFAULT 0,
  migration_id TEXT NOT NULL,
  model_version TEXT NOT NULL DEFAULT 'legacy',
  migration_state TEXT NOT NULL DEFAULT 'catching_up',
  cutover_revision INTEGER,
  revision INTEGER NOT NULL DEFAULT 1,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE task_domain_legacy_outbox (
  sequence INTEGER PRIMARY KEY AUTOINCREMENT,
  workspace_id TEXT NOT NULL,
  entity_kind TEXT NOT NULL,
  entity_id TEXT NOT NULL,
  operation TEXT NOT NULL,
  source_logical_version INTEGER NOT NULL,
  row_image TEXT,
  tombstone_image TEXT
);
CREATE TABLE replay_projection (
  sequence INTEGER PRIMARY KEY,
  entity_id TEXT NOT NULL
);
INSERT INTO workspace_task_domain_state(workspace_id,migration_id) VALUES('alpha','migration-alpha'),('beta','migration-beta');`
	if _, err := db.Exec(ddl); err != nil {
		t.Fatalf("create fixture: %v", err)
	}
	return db
}

func mustNewReplayStore(t *testing.T, db *sql.DB, dialect Dialect) *ReplayStore {
	t.Helper()
	store, err := NewReplayStore(db, dialect)
	if err != nil {
		t.Fatalf("NewReplayStore: %v", err)
	}
	return store
}

func insertReplayOutbox(t *testing.T, db *sql.DB, workspaceID, kind, entityID, operation string, version int64, rowImage, tombstone any) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO task_domain_legacy_outbox(
workspace_id,entity_kind,entity_id,operation,source_logical_version,row_image,tombstone_image
) VALUES(?,?,?,?,?,?,?)`, workspaceID, kind, entityID, operation, version, rowImage, tombstone)
	if err != nil {
		t.Fatalf("insert outbox: %v", err)
	}
}

func recordReplayProjection(ctx context.Context, tx *sql.Tx, events []ReplayEvent) error {
	for _, event := range events {
		if _, err := tx.ExecContext(ctx, `INSERT INTO replay_projection(sequence,entity_id) VALUES(?,?)`, event.Sequence, event.SourceID); err != nil {
			return err
		}
	}
	return nil
}

func assertReplayWatermark(t *testing.T, db *sql.DB, workspaceID string, wantWatermark, wantRevision int64) {
	t.Helper()
	var watermark, revision int64
	if err := db.QueryRow(`SELECT source_watermark,revision FROM workspace_task_domain_state WHERE workspace_id=?`, workspaceID).Scan(&watermark, &revision); err != nil {
		t.Fatalf("read watermark: %v", err)
	}
	if watermark != wantWatermark || revision != wantRevision {
		t.Fatalf("state = watermark %d revision %d, want %d/%d", watermark, revision, wantWatermark, wantRevision)
	}
}

func assertReplayProjectionCount(t *testing.T, db *sql.DB, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(`SELECT COUNT(*) FROM replay_projection`).Scan(&got); err != nil {
		t.Fatalf("count projection: %v", err)
	}
	if got != want {
		t.Fatalf("projection count = %d, want %d", got, want)
	}
}

func cloneReplayPageForTest(page ReplayPage) ReplayPage {
	clone := page
	clone.Events = append([]ReplayEvent(nil), page.Events...)
	return clone
}
