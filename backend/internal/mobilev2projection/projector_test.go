package mobilev2projection_test

import (
	"bytes"
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/mobilev2contract"
	"github.com/hujinrun/flowspace/internal/mobilev2projection"
	"github.com/hujinrun/flowspace/internal/mobilev2sync"
	"github.com/hujinrun/flowspace/internal/storage"
	storagesqlite "github.com/hujinrun/flowspace/internal/storage/sqlite"
)

func TestSQLiteTaskAndOccurrenceProjectionsDecodeAsFrozenContract(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tenant.db")
	cfg := storage.Config{Env: "test", Driver: storage.DriverSQLite, SQLitePath: path}
	if err := (storagesqlite.Provider{}).MigrateTenant(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := "2026-07-29T08:00:00.000Z"
	mustExec(t, tx, `INSERT INTO tenant_workspaces(workspace_id) VALUES('workspace-1')`)
	mustExec(t, tx, `INSERT INTO domain_projects_v2
		(workspace_id,id,name,description,kind,horizon,status,revision,created_at,updated_at)
		VALUES('workspace-1','project-1','Project','Description','standard','short','active',2,?,?)`, now, now)
	mustExec(t, tx, `INSERT INTO domain_tasks_v2
		(workspace_id,id,project_id,title,description,lifecycle_status,priority,sort_order,revision,created_at,updated_at)
		VALUES('workspace-1','task-1','project-1','Task','Description','active',1,0,3,?,?)`, now, now)
	mustExec(t, tx, `INSERT INTO domain_task_schedules_v2
		(workspace_id,task_id,revision,current_schedule_revision,generation_status,updated_at)
		VALUES('workspace-1','task-1',4,1,'idle',?)`, now)
	mustExec(t, tx, `INSERT INTO domain_task_schedule_versions_v2
		(workspace_id,task_id,schedule_revision,recurrence_type,timing_type,timezone,starts_on,recurrence_rule,created_at)
		VALUES('workspace-1','task-1',1,'none','date','Asia/Shanghai','2026-07-29','{}',?)`, now)
	mustExec(t, tx, `INSERT INTO domain_task_occurrences_v2
		(workspace_id,id,task_id,occurrence_key,planned_date,execution_status,revision,generated_schedule_revision,created_at,updated_at)
		VALUES('workspace-1','occurrence-1','task-1','2026-07-29','2026-07-29','open',5,1,?,?)`, now, now)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	taskEntities := projectWithTx(t, db, mobilev2projection.Projection{
		WorkspaceID: "workspace-1",
		Scope:       mobilev2sync.ScopeIPhoneTaskCore,
		AsOf:        time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC),
		Sequence:    9,
	})
	decoded := decodeMatrix(t, taskEntities)
	if len(decoded) != 4 {
		t.Fatalf("task-core entity count = %d, want project/task/schedule/version", len(decoded))
	}
	if decoded[0].EntityType != "project" || decoded[1].EntityType != "task" ||
		decoded[2].EntityType != "task_schedule" || decoded[3].EntityType != "schedule_version" {
		t.Fatalf("task-core entity order/types = %#v", decoded)
	}

	occurrenceEntities := projectWithTx(t, db, mobilev2projection.Projection{
		WorkspaceID:     "workspace-1",
		Scope:           mobilev2sync.ScopeIPhoneOccurrenceWindow,
		AsOf:            time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC),
		WindowStart:     time.Date(2026, 7, 28, 16, 0, 0, 0, time.UTC),
		WindowEnd:       time.Date(2026, 8, 12, 16, 0, 0, 0, time.UTC),
		WindowStartDate: "2026-07-29",
		WindowEndDate:   "2026-08-13",
		Sequence:        9,
	})
	decoded = decodeMatrix(t, occurrenceEntities)
	if len(decoded) != 1 || decoded[0].EntityType != "task_occurrence" ||
		decoded[0].AggregateRevisions.OccurrenceRevision == nil ||
		*decoded[0].AggregateRevisions.OccurrenceRevision != "5" {
		t.Fatalf("occurrence entities = %#v", decoded)
	}
}

func projectWithTx(t *testing.T, db *sql.DB, projection mobilev2projection.Projection) [][]byte {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	entities, err := mobilev2projection.Project(context.Background(), tx, mobilev2projection.DialectSQLite, projection)
	if err != nil {
		t.Fatal(err)
	}
	result := make([][]byte, len(entities))
	for index := range entities {
		result[index] = entities[index]
	}
	return result
}

func decodeMatrix(t *testing.T, entities [][]byte) []mobilev2contract.EntityEnvelope {
	t.Helper()
	encoded := append([]byte{'['}, bytes.Join(entities, []byte(","))...)
	encoded = append(encoded, ']')
	decoded, err := mobilev2contract.DecodeEntityMatrix(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

func mustExec(t *testing.T, tx *sql.Tx, query string, args ...any) {
	t.Helper()
	if _, err := tx.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}
