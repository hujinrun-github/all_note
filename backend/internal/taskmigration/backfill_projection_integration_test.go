package taskmigration

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestBackfillStoreRunSnapshotBackfillPersistsProjectionAndStateAtomically(t *testing.T) {
	db := openProjectionWriterSQLite(t, "alpha")
	if _, err := db.Exec(`UPDATE workspace_task_domain_state SET
		model_version='legacy',migration_state='backfilling',source_watermark=0,
		cutover_revision=NULL,write_epoch=7,accept_legacy_writes=1,
		migration_timezone='Asia/Shanghai',migration_id='migration-alpha',revision=2
		WHERE workspace_id='alpha'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO legacy_task_domain_entity_versions
		(workspace_id,entity_kind,entity_id,logical_version,deleted)
		VALUES('alpha','project','personal',1,0)`); err != nil {
		t.Fatal(err)
	}

	store, err := NewBackfillStore(db, DialectSQLite)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := NewV2ProjectionWriter(DialectSQLite)
	if err != nil {
		t.Fatal(err)
	}
	loader := func(context.Context, *sql.Tx) (LegacyTaskDomainInventory, LegacyTaskDomainRows, error) {
		inventory := validLegacyInventory()
		inventory.WorkspaceID = "alpha"
		rows := validLegacyRows()
		rows.Projects = rows.Projects[:1]
		return inventory, rows, nil
	}

	result, err := store.RunSnapshotBackfill(
		context.Background(), "alpha", 2, 7,
		time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC), loader, writer,
	)
	if err != nil {
		t.Fatalf("RunSnapshotBackfill: %v", err)
	}
	if result.State.MigrationState != MigrationStateCatchingUp || result.State.Revision != 3 {
		t.Fatalf("state=%+v", result.State)
	}
	assertProjectionWriterCount(t, db, "domain_projects_v2", "alpha", 2)
	assertProjectionWriterCount(t, db, "task_domain_legacy_id_map", "alpha", 1)
}
