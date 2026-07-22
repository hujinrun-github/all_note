package taskmigration

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDBFinalCutoverObserverSQLiteObservesFrozenReadyWorkspace(t *testing.T) {
	db, observer := prepareFinalCutoverObserverSQLite(t)

	observation, err := observer.ObserveFinalCutover(context.Background(), "alpha", "migration-alpha", 1)
	if err != nil {
		t.Fatalf("ObserveFinalCutover: %v", err)
	}
	if observation.OutboxWatermark != 1 || observation.ActiveLegacyTransactions != 0 || observation.PreviousFenceEpoch != 1 {
		t.Fatalf("observation fence=%+v", observation)
	}
	if !observation.Reconcile.Ready || observation.PendingMutations != 0 {
		t.Fatalf("observation reconcile=%+v pending=%d", observation.Reconcile, observation.PendingMutations)
	}

	// The observer is read-only: it must not advance state or create outbox
	// rows while proving cutover readiness.
	var state, version string
	var revision, outboxCount int
	if err := db.QueryRow(`SELECT migration_state,model_version,revision FROM workspace_task_domain_state WHERE workspace_id='alpha'`).Scan(&state, &version, &revision); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM task_domain_legacy_outbox WHERE workspace_id='alpha'`).Scan(&outboxCount); err != nil {
		t.Fatal(err)
	}
	if state != "ready" || version != "legacy" || revision != 2 || outboxCount != 1 {
		t.Fatalf("state=%s model=%s revision=%d outbox=%d", state, version, revision, outboxCount)
	}
}

func TestDBFinalCutoverObserverSQLiteReportsReplayLagWithoutMutating(t *testing.T) {
	db, observer := prepareFinalCutoverObserverSQLite(t)
	if _, err := db.Exec(`UPDATE workspace_task_domain_state SET migration_state='draining',source_watermark=0 WHERE workspace_id='alpha'`); err != nil {
		t.Fatal(err)
	}

	observation, err := observer.ObserveFinalCutover(context.Background(), "alpha", "migration-alpha", 1)
	if err != nil {
		t.Fatalf("ObserveFinalCutover: %v", err)
	}
	if observation.OutboxWatermark != 0 || observation.PendingMutations != 1 || !observation.Reconcile.Ready {
		t.Fatalf("observation=%+v", observation)
	}
	var watermark int
	if err := db.QueryRow(`SELECT source_watermark FROM workspace_task_domain_state WHERE workspace_id='alpha'`).Scan(&watermark); err != nil || watermark != 0 {
		t.Fatalf("watermark=%d err=%v", watermark, err)
	}
}

func TestDBFinalCutoverObserverSQLiteReturnsFinalMismatchPlan(t *testing.T) {
	db, observer := prepareFinalCutoverObserverSQLite(t)
	if _, err := db.Exec(`UPDATE domain_tasks_v2 SET title='tampered after replay' WHERE workspace_id='alpha' AND id='a-task'`); err != nil {
		t.Fatal(err)
	}

	observation, err := observer.ObserveFinalCutover(context.Background(), "alpha", "migration-alpha", 1)
	if err != nil {
		t.Fatalf("ObserveFinalCutover: %v", err)
	}
	if observation.Reconcile.Ready || !hasMismatchCode(observation.Reconcile.Mismatches, ReconcileMismatchChecksum) {
		t.Fatalf("plan=%+v", observation.Reconcile)
	}
}

func TestDBFinalCutoverObserverSQLiteCountsReconcileRepairsAsPendingMutations(t *testing.T) {
	db, observer := prepareFinalCutoverObserverSQLite(t)
	if _, err := db.Exec(`DELETE FROM domain_task_occurrences_v2
		WHERE workspace_id='alpha' AND task_id='a-task'`); err != nil {
		t.Fatal(err)
	}

	observation, err := observer.ObserveFinalCutover(context.Background(), "alpha", "migration-alpha", 1)
	if err != nil {
		t.Fatalf("ObserveFinalCutover: %v", err)
	}
	if observation.Reconcile.Ready || len(observation.Reconcile.UpsertMissing) != 1 || observation.PendingMutations != 1 {
		t.Fatalf("observation=%+v", observation)
	}
}

func TestDBFinalCutoverObserverSQLiteIsWorkspaceIsolated(t *testing.T) {
	db, observer := prepareFinalCutoverObserverSQLite(t)
	if _, err := db.Exec(`UPDATE tasks SET title='beta changed after alpha drain' WHERE workspace_id='beta' AND id='beta-task'`); err != nil {
		t.Fatal(err)
	}

	observation, err := observer.ObserveFinalCutover(context.Background(), "alpha", "migration-alpha", 1)
	if err != nil {
		t.Fatalf("ObserveFinalCutover(alpha): %v", err)
	}
	if observation.OutboxWatermark != 1 || !observation.Reconcile.Ready || observation.PendingMutations != 0 {
		t.Fatalf("alpha observation=%+v", observation)
	}
	var betaOutbox int
	if err := db.QueryRow(`SELECT COUNT(*) FROM task_domain_legacy_outbox WHERE workspace_id='beta'`).Scan(&betaOutbox); err != nil || betaOutbox != 1 {
		t.Fatalf("beta outbox=%d err=%v", betaOutbox, err)
	}
}

func TestDBFinalCutoverObserverSQLiteFailsClosedOnStateLedgerAndTriggerAnomalies(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *sql.DB)
	}{
		{
			name: "wrong durable phase",
			mutate: func(t *testing.T, db *sql.DB) {
				_, err := db.Exec(`UPDATE workspace_task_domain_state SET
					migration_state='catching_up',accept_legacy_writes=1,cutover_revision=NULL
					WHERE workspace_id='alpha'`)
				if err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "watermark beyond frozen outbox",
			mutate: func(t *testing.T, db *sql.DB) {
				if _, err := db.Exec(`UPDATE workspace_task_domain_state SET source_watermark=2 WHERE workspace_id='alpha'`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "missing current source ledger",
			mutate: func(t *testing.T, db *sql.DB) {
				if _, err := db.Exec(`DELETE FROM legacy_task_domain_entity_versions
					WHERE workspace_id='alpha' AND entity_kind='task' AND entity_id='a-task'`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "live ledger without source",
			mutate: func(t *testing.T, db *sql.DB) {
				if _, err := db.Exec(`INSERT INTO legacy_task_domain_entity_versions
					(workspace_id,entity_kind,entity_id,logical_version,deleted)
					VALUES('alpha','task','ghost-task',1,0)`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "missing canonical fence trigger",
			mutate: func(t *testing.T, db *sql.DB) {
				if _, err := db.Exec(`DROP TRIGGER task_domain_legacy_outbox_tasks_update`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "missing roadmap freeze trigger",
			mutate: func(t *testing.T, db *sql.DB) {
				if _, err := db.Exec(`DROP TRIGGER task_domain_legacy_roadmap_freeze_roadmap_nodes_update`); err != nil {
					t.Fatal(err)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db, observer := prepareFinalCutoverObserverSQLite(t)
			test.mutate(t, db)
			_, err := observer.ObserveFinalCutover(context.Background(), "alpha", "migration-alpha", 1)
			if !errors.Is(err, ErrFinalCutoverObservation) {
				t.Fatalf("error=%v, want ErrFinalCutoverObservation", err)
			}
		})
	}
}

func TestDBFinalCutoverObserverRejectsWrongCutoverIdentityAndInvalidConstruction(t *testing.T) {
	db, observer := prepareFinalCutoverObserverSQLite(t)
	if _, err := NewDBFinalCutoverObserver(DBFinalCutoverObserverConfig{}); !errors.Is(err, ErrInvalidFinalCutoverObserver) {
		t.Fatalf("nil DB constructor error=%v", err)
	}
	if _, err := NewDBFinalCutoverObserver(DBFinalCutoverObserverConfig{DB: db, Dialect: Dialect("oracle")}); !errors.Is(err, ErrInvalidFinalCutoverObserver) {
		t.Fatalf("invalid dialect constructor error=%v", err)
	}
	for _, test := range []struct {
		workspace, migration string
		cutover              uint64
	}{
		{workspace: "beta", migration: "migration-alpha", cutover: 1},
		{workspace: "alpha", migration: "migration-other", cutover: 1},
		{workspace: "alpha", migration: "migration-alpha", cutover: 2},
	} {
		if _, err := observer.ObserveFinalCutover(context.Background(), test.workspace, test.migration, test.cutover); !errors.Is(err, ErrFinalCutoverObservation) {
			t.Fatalf("ObserveFinalCutover(%+v) error=%v", test, err)
		}
	}
	if _, err := observer.ObserveFinalCutover(context.Background(), "", "migration-alpha", 1); !errors.Is(err, ErrInvalidFinalCutoverObserver) {
		t.Fatalf("empty workspace error=%v", err)
	}
}

func prepareFinalCutoverObserverSQLite(t *testing.T) (*sql.DB, *DBFinalCutoverObserver) {
	t.Helper()
	db := openLegacySnapshotSQLite(t)
	if _, err := db.Exec(`CREATE TABLE tenant_workspaces(
		workspace_id TEXT PRIMARY KEY,
		epoch INTEGER NOT NULL DEFAULT 1 CHECK(epoch>0),
		state TEXT NOT NULL DEFAULT 'active' CHECK(state IN ('active','fenced','retired')),
		migration_id TEXT,
		created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
		CHECK ((state='fenced' AND migration_id IS NOT NULL) OR (state<>'fenced' AND migration_id IS NULL))
	);
	INSERT INTO tenant_workspaces(workspace_id) VALUES('alpha'),('beta')`); err != nil {
		t.Fatalf("create tenant anchors: %v", err)
	}
	for _, name := range []string{"0002_task_domain_v2.sql", "0003_task_domain_legacy_migration.sql"} {
		path := filepath.Join("..", "..", "db", "migrations", "tenant", "sqlite", name)
		script, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if _, err := db.Exec(string(script)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
	seedLegacySnapshotSQLite(t, db)
	// The adopted SQLite tenant contract canonicalizes the historical `due`
	// spelling to the same due_at column used by the outbox image contract.
	// Keep the fixture on the real historical schema and perform that explicit
	// adopt step instead of constructing a reduced test-only source schema.
	if _, err := db.Exec(`ALTER TABLE tasks ADD COLUMN due_at TEXT;
		UPDATE tasks SET due_at=strftime('%Y-%m-%dT%H:%M:%SZ',due,'unixepoch') WHERE due IS NOT NULL;
		DELETE FROM task_occurrences WHERE workspace_id NOT IN ('alpha','beta');
		DELETE FROM task_recurrence_rules WHERE workspace_id NOT IN ('alpha','beta');
		DELETE FROM tasks WHERE workspace_id NOT IN ('alpha','beta');
		DELETE FROM events WHERE workspace_id NOT IN ('alpha','beta');
		DELETE FROM roadmap_edges WHERE workspace_id NOT IN ('alpha','beta');
		DELETE FROM roadmap_nodes WHERE workspace_id NOT IN ('alpha','beta');
		DELETE FROM learning_roadmaps WHERE workspace_id NOT IN ('alpha','beta');
		DELETE FROM task_projects WHERE workspace_id NOT IN ('alpha','beta')`); err != nil {
		t.Fatalf("adopt historical SQLite due column: %v", err)
	}
	if err := InstallLegacyOutboxTriggers(context.Background(), db, DialectSQLite, TaskDomainSourceLegacyWorkspace); err != nil {
		t.Fatalf("install legacy outbox: %v", err)
	}
	// Produce one real, trigger-backed event after baseline. Its sequence is
	// the drain cutover revision and its ledger version is the final version.
	if _, err := db.Exec(`UPDATE tasks SET title='Done once final' WHERE workspace_id='alpha' AND id='a-task'`); err != nil {
		t.Fatalf("create final legacy outbox event: %v", err)
	}

	loader, err := NewLegacySnapshotLoader(LegacySnapshotLoaderConfig{
		Dialect: DialectSQLite, WorkspaceID: "alpha", WorkspaceTimezone: "Asia/Shanghai",
	})
	if err != nil {
		t.Fatal(err)
	}
	writer, err := NewV2ProjectionWriter(DialectSQLite)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	inventory, legacyRows, err := loader.Load(context.Background(), tx)
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("load final source: %v", err)
	}
	preflight, err := PreflightLegacyTaskDomain(inventory)
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("preflight final source: %v", err)
	}
	projection, err := MapLegacyTaskDomain(preflight, legacyRows)
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("map final source: %v", err)
	}
	if err := writer.WriteSnapshot(context.Background(), tx, "alpha", projection, finalCutoverFixtureTime()); err != nil {
		_ = tx.Rollback()
		t.Fatalf("write final projection: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit final projection: %v", err)
	}

	if _, err := db.Exec(`UPDATE tenant_workspaces SET epoch=2 WHERE workspace_id='alpha';
		UPDATE workspace_task_domain_state SET
			migration_state='ready',source_watermark=1,cutover_revision=1,
			write_epoch=2,accept_legacy_writes=0,migration_timezone='Asia/Shanghai',
			migration_id='migration-alpha',revision=2
		WHERE workspace_id='alpha'`); err != nil {
		t.Fatalf("close final drain fence: %v", err)
	}

	observer, err := NewDBFinalCutoverObserver(DBFinalCutoverObserverConfig{DB: db, Dialect: DialectSQLite})
	if err != nil {
		t.Fatal(err)
	}
	return db, observer
}

func finalCutoverFixtureTime() time.Time {
	return time.Date(2026, 7, 22, 16, 0, 0, 0, time.UTC)
}
