package taskmigration

import (
	"context"
	"testing"
)

func TestV2ProjectionWriterWriteSnapshotLoadsDurableSourceVersions(t *testing.T) {
	db := openProjectionWriterSQLite(t, "alpha")
	writer, err := NewV2ProjectionWriter(DialectSQLite)
	if err != nil {
		t.Fatal(err)
	}
	input := projectionWriterInput("alpha")
	seedProjectionSourceVersions(t, db, input)

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := writer.WriteSnapshot(
		context.Background(), tx, input.WorkspaceID, input.Projection, input.WrittenAt,
	); err != nil {
		t.Fatalf("WriteSnapshot: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	assertProjectionWriterCount(t, db, "domain_projects_v2", "alpha", 1)
	assertProjectionWriterCount(t, db, "domain_tasks_v2", "alpha", 1)
	assertProjectionWriterCount(t, db, "task_domain_legacy_id_map", "alpha", 4)
}

func TestV2ProjectionWriterWriteSnapshotRejectsDeletedDurableSource(t *testing.T) {
	db := openProjectionWriterSQLite(t, "alpha")
	writer, err := NewV2ProjectionWriter(DialectSQLite)
	if err != nil {
		t.Fatal(err)
	}
	input := projectionWriterInput("alpha")
	seedProjectionSourceVersions(t, db, input)
	if _, err := db.Exec(`UPDATE legacy_task_domain_entity_versions SET deleted=1
		WHERE workspace_id='alpha' AND entity_kind='task' AND entity_id='legacy-task-1'`); err != nil {
		t.Fatal(err)
	}

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteSnapshot(
		context.Background(), tx, input.WorkspaceID, input.Projection, input.WrittenAt,
	); err == nil {
		_ = tx.Rollback()
		t.Fatal("WriteSnapshot accepted a source deleted in the snapshot ledger")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	assertProjectionWriterCount(t, db, "domain_projects_v2", "alpha", 0)
}
