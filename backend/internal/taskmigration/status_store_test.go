package taskmigration

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "modernc.org/sqlite"
)

func TestMigrationStatusStoreReportsDurableReplayLagAndAuditState(t *testing.T) {
	db := openMigrationStatusSQLite(t)
	seedMigrationStatus(t, db, "alpha", "legacy", "catching_up", 7, 4, 3, true, "migration-alpha", "")
	for index := 0; index < 3; index++ {
		if _, err := db.Exec(`INSERT INTO task_domain_legacy_outbox(workspace_id) VALUES('alpha')`); err != nil {
			t.Fatal(err)
		}
	}
	// A different workspace uses the same global sequence but cannot inflate
	// alpha's workspace-local migration lag.
	if _, err := db.Exec(`INSERT INTO task_domain_legacy_outbox(workspace_id) VALUES('beta')`); err != nil {
		t.Fatal(err)
	}

	store, err := NewMigrationStatusStore(db, DialectSQLite)
	if err != nil {
		t.Fatal(err)
	}
	status, err := store.Load(context.Background(), "alpha")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if status.WorkspaceID != "alpha" || status.MigrationID != "migration-alpha" || status.MigrationState != MigrationStateCatchingUp {
		t.Fatalf("status=%+v", status)
	}
	if status.SourceWatermark != 3 || status.OutboxHead != 3 || status.ReplayLag != 0 {
		t.Fatalf("watermarks=%+v", status)
	}
	if status.Revision != 7 || status.WriteEpoch != 4 || !status.AcceptLegacyWrites {
		t.Fatalf("fences=%+v", status)
	}
}

func TestMigrationStatusStoreUsesGlobalSequenceAsWorkspaceHeadWithoutCountingGaps(t *testing.T) {
	db := openMigrationStatusSQLite(t)
	seedMigrationStatus(t, db, "alpha", "legacy", "catching_up", 7, 4, 1, true, "migration-alpha", "")
	if _, err := db.Exec(`INSERT INTO task_domain_legacy_outbox(workspace_id) VALUES('alpha');
		INSERT INTO task_domain_legacy_outbox(workspace_id) VALUES('beta');
		INSERT INTO task_domain_legacy_outbox(workspace_id) VALUES('alpha')`); err != nil {
		t.Fatal(err)
	}
	store, _ := NewMigrationStatusStore(db, DialectSQLite)
	status, err := store.Load(context.Background(), "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if status.OutboxHead != 3 || status.ReplayLag != 2 {
		t.Fatalf("status=%+v", status)
	}
}

func TestMigrationStatusStoreRejectsImpossibleDurableWatermark(t *testing.T) {
	db := openMigrationStatusSQLite(t)
	seedMigrationStatus(t, db, "alpha", "legacy", "catching_up", 7, 4, 5, true, "migration-alpha", "")
	store, _ := NewMigrationStatusStore(db, DialectSQLite)
	_, err := store.Load(context.Background(), "alpha")
	if !errors.Is(err, ErrInvalidMigrationStatus) {
		t.Fatalf("Load error=%v", err)
	}
}

func TestNewMigrationStatusStoreRejectsInvalidInput(t *testing.T) {
	if _, err := NewMigrationStatusStore(nil, DialectSQLite); !errors.Is(err, ErrInvalidMigrationStatus) {
		t.Fatalf("nil database error=%v", err)
	}
	db := openMigrationStatusSQLite(t)
	if _, err := NewMigrationStatusStore(db, Dialect("mysql")); !errors.Is(err, ErrInvalidMigrationStatus) {
		t.Fatalf("dialect error=%v", err)
	}
	store, _ := NewMigrationStatusStore(db, DialectSQLite)
	if _, err := store.Load(context.Background(), ""); !errors.Is(err, ErrInvalidMigrationStatus) {
		t.Fatalf("workspace error=%v", err)
	}
}

func openMigrationStatusSQLite(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:taskmigration-status-"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE workspace_task_domain_state(
		workspace_id TEXT PRIMARY KEY,model_version TEXT NOT NULL,migration_state TEXT NOT NULL,
		source_watermark INTEGER NOT NULL,cutover_revision INTEGER,write_epoch INTEGER NOT NULL,
		accept_legacy_writes INTEGER NOT NULL,migration_timezone TEXT NOT NULL,v2_first_write_at TEXT,
		migration_id TEXT,last_error TEXT,revision INTEGER NOT NULL);
		CREATE TABLE task_domain_legacy_outbox(
		sequence INTEGER PRIMARY KEY AUTOINCREMENT,workspace_id TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedMigrationStatus(t *testing.T, db *sql.DB, workspaceID, model, phase string, revision, epoch, watermark int64, accept bool, migrationID, lastError string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO workspace_task_domain_state(
		workspace_id,model_version,migration_state,source_watermark,cutover_revision,write_epoch,
		accept_legacy_writes,migration_timezone,v2_first_write_at,migration_id,last_error,revision)
		VALUES(?,?,?,?,NULL,? ,?,'UTC',NULL,?,?,?)`,
		workspaceID, model, phase, watermark, epoch, accept, nullableStatusText(migrationID), nullableStatusText(lastError), revision); err != nil {
		t.Fatal(err)
	}
}

func nullableStatusText(value string) any {
	if value == "" {
		return nil
	}
	return value
}
