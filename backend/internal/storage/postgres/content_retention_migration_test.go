package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/storage"
)

func TestPostgresMobileV2ContentRetentionMigration(t *testing.T) {
	rawURL := createPostgresTestSchema(t, fmt.Sprintf("fs_test_content_retention_%d", time.Now().UnixNano()))
	cfg := storage.Config{Env: "test", Driver: storage.DriverPostgres, URL: rawURL}
	if err := (Provider{}).MigrateTenant(context.Background(), cfg); err != nil {
		t.Fatalf("migrate tenant: %v", err)
	}
	db, err := sql.Open("pgx", rawURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	assertRowCount(t, db, `SELECT COUNT(*) FROM tenant_schema_migrations
		WHERE version='0012_mobile_v2_content_retention.sql'`, 1)
	for _, table := range []string{
		"mobile_v2_content_tombstones",
		"mobile_v2_scope_retention",
		"note_attachments",
	} {
		var exists bool
		if err := db.QueryRow(`SELECT to_regclass(current_schema()||'.'||$1) IS NOT NULL`, table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Fatalf("retention migration table %s is missing", table)
		}
	}
	const workspaceID = "postgres-content-retention-workspace"
	if _, err := db.Exec(`INSERT INTO tenant_workspaces(workspace_id) VALUES($1)`, workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		CREATE TABLE search_index (
			workspace_id TEXT NOT NULL, entity_type TEXT NOT NULL, entity_id TEXT NOT NULL,
			title TEXT NOT NULL DEFAULT '', content TEXT NOT NULL DEFAULT '', tags TEXT[] NOT NULL DEFAULT '{}',
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now(), search_vector TSVECTOR NOT NULL DEFAULT ''::tsvector,
			PRIMARY KEY (entity_type,entity_id)
		);
		CREATE TABLE mobile_sync_outbox (
			sequence BIGSERIAL PRIMARY KEY, workspace_id TEXT NOT NULL, mutation_id TEXT NOT NULL,
			entity_type TEXT NOT NULL, entity_client_id TEXT NOT NULL, operation TEXT NOT NULL,
			revision BIGINT NOT NULL, entity_json JSONB NOT NULL, created_at TIMESTAMPTZ NOT NULL,
			published_at TIMESTAMPTZ,
			UNIQUE (workspace_id,mutation_id,entity_type,entity_client_id)
		);
		CREATE TABLE mobile_retired_ids (
			workspace_id TEXT NOT NULL, entity_type TEXT NOT NULL, client_id TEXT NOT NULL,
			retired_at TIMESTAMPTZ NOT NULL, PRIMARY KEY (workspace_id,entity_type,client_id)
		);
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO folders(id,workspace_id,name)
		VALUES('__uncategorized',$1,'Uncategorized')`, workspaceID); err != nil {
		t.Fatal(err)
	}
	ctx := auth.ContextWithWorkspaceScope(context.Background(), workspaceID)
	const noteID = "postgres-web-delete-note"
	if _, err := db.Exec(`INSERT INTO notes
		(id,workspace_id,client_id,revision,title,body,folder_id,tags,created_at,updated_at)
		VALUES($1,$2,'72000000-0000-4000-8000-000000000001',1,'Web note','body','__uncategorized','{}',now(),now())`,
		noteID, workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO mobile_v2_commit_heads(workspace_id,latest_sequence)
		VALUES($1,1)`, workspaceID); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	if _, err := db.Exec(`INSERT INTO domain_projects_v2
		(workspace_id,id,name,description,kind,horizon,status,revision,created_at,updated_at)
		VALUES($1,'project-1','Project','Description','standard','short','active',2,$2,$2)`, workspaceID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO domain_tasks_v2
		(workspace_id,id,project_id,note_id,title,description,lifecycle_status,priority,sort_order,revision,created_at,updated_at)
		VALUES($1,'task-1','project-1',$2,'Task','Description','active',1,0,3,$3,$3)`, workspaceID, noteID, now); err != nil {
		t.Fatal(err)
	}
	taskTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := taskTx.Exec(`INSERT INTO domain_task_schedules_v2
		(workspace_id,task_id,revision,current_schedule_revision,generation_status,updated_at)
		VALUES($1,'task-1',4,1,'idle',$2)`, workspaceID, now); err != nil {
		taskTx.Rollback()
		t.Fatal(err)
	}
	if _, err := taskTx.Exec(`INSERT INTO domain_task_schedule_versions_v2
		(workspace_id,task_id,schedule_revision,recurrence_type,timing_type,timezone,starts_on,recurrence_rule,created_at)
		VALUES($1,'task-1',1,'none','date','Asia/Shanghai','2026-08-01','{}',$2)`, workspaceID, now); err != nil {
		taskTx.Rollback()
		t.Fatal(err)
	}
	if _, err := taskTx.Exec(`INSERT INTO domain_task_occurrences_v2
		(workspace_id,id,task_id,occurrence_key,planned_date,execution_status,note_id,revision,generated_schedule_revision,created_at,updated_at)
		VALUES($1,'occurrence-1','task-1','2026-08-01','2026-08-01','open',$2,5,1,$3,$3)`,
		workspaceID, noteID, now); err != nil {
		taskTx.Rollback()
		t.Fatal(err)
	}
	if err := taskTx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO tasks(id,workspace_id,note_id,title,updated_at)
		VALUES('legacy-task-1',$1,$2,'Legacy task',$3)`, workspaceID, noteID, now); err != nil {
		t.Fatal(err)
	}
	if err := (noteRepository{db: db}).Delete(ctx, noteID); err != nil {
		t.Fatal(err)
	}
	var taskNoteID, occurrenceNoteID, legacyNoteID sql.NullString
	var taskRevision, occurrenceRevision int64
	if err := db.QueryRow(`SELECT note_id,revision FROM domain_tasks_v2
		WHERE workspace_id=$1 AND id='task-1'`, workspaceID).Scan(&taskNoteID, &taskRevision); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT note_id,revision FROM domain_task_occurrences_v2
		WHERE workspace_id=$1 AND id='occurrence-1'`, workspaceID).Scan(&occurrenceNoteID, &occurrenceRevision); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT note_id FROM tasks
		WHERE workspace_id=$1 AND id='legacy-task-1'`, workspaceID).Scan(&legacyNoteID); err != nil {
		t.Fatal(err)
	}
	if taskNoteID.Valid || occurrenceNoteID.Valid || legacyNoteID.Valid || taskRevision != 4 || occurrenceRevision != 6 {
		t.Fatalf("postgres web delete refs: task=%v/%d occurrence=%v/%d legacy=%v",
			taskNoteID, taskRevision, occurrenceNoteID, occurrenceRevision, legacyNoteID)
	}
	for _, scope := range []string{"iphone-content", "iphone-task-core", "iphone-occurrence-window", "watch-occurrence-window"} {
		var entities string
		if err := db.QueryRow(`SELECT entities_json::text FROM mobile_v2_scope_change_batches
			WHERE workspace_id=$1 AND scope=$2 AND sequence=2`, workspaceID, scope).Scan(&entities); err != nil {
			t.Fatal(err)
		}
		if scope != "iphone-content" && !strings.Contains(entities, `"note_id": null`) {
			t.Fatalf("postgres web delete scope %s = %s", scope, entities)
		}
	}
}
