package taskmigration

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestReplayStoreCommitRejectsWorkspaceThatLeftReplayPhase(t *testing.T) {
	db := openSQLiteReplayStoreTestDB(t)
	store := mustNewReplayStore(t, db, DialectSQLite)
	insertReplayOutbox(t, db, "alpha", "project", "p-1", "upsert", 1, `{"title":"one"}`, nil)
	page, err := store.FetchPage(context.Background(), "alpha", 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE workspace_task_domain_state SET migration_state='failed' WHERE workspace_id='alpha'`); err != nil {
		t.Fatal(err)
	}

	called := false
	err = store.CommitPage(context.Background(), page, func(context.Context, *sql.Tx, []ReplayEvent) error {
		called = true
		return nil
	})
	if !errors.Is(err, ErrReplayPhaseConflict) {
		t.Fatalf("CommitPage error=%v, want ErrReplayPhaseConflict", err)
	}
	if called {
		t.Fatal("projector ran after workspace left a replay phase")
	}
	assertReplayWatermark(t, db, "alpha", 0, 1)
}

func TestReplayStoreCommitDoesNotCrossDrainCutoverRevision(t *testing.T) {
	db := openSQLiteReplayStoreTestDB(t)
	store := mustNewReplayStore(t, db, DialectSQLite)
	insertReplayOutbox(t, db, "alpha", "project", "p-1", "upsert", 1, `{"title":"one"}`, nil)
	insertReplayOutbox(t, db, "alpha", "task", "t-1", "upsert", 1, `{"title":"two"}`, nil)
	page, err := store.FetchPage(context.Background(), "alpha", 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE workspace_task_domain_state
		SET migration_state='draining',cutover_revision=1 WHERE workspace_id='alpha'`); err != nil {
		t.Fatal(err)
	}

	err = store.CommitPage(context.Background(), page, func(context.Context, *sql.Tx, []ReplayEvent) error {
		t.Fatal("projector ran beyond the drain cutover revision")
		return nil
	})
	if !errors.Is(err, ErrReplayPhaseConflict) {
		t.Fatalf("CommitPage error=%v, want ErrReplayPhaseConflict", err)
	}
	assertReplayWatermark(t, db, "alpha", 0, 1)
}
