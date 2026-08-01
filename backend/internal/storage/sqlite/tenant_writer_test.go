package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/taskdomain"
)

func TestSQLiteFencedWriteChecksEpochRollsBackAndClosesTx(t *testing.T) {
	cfg, db := createSQLiteTenantWriterFixture(t)
	writer := NewTenantWriter(cfg)
	event := storage.TenantOutboxEvent{ID: "e1", Topic: "note.saved", AggregateID: "n1", AggregateRevision: 1, PayloadJSON: `{}`}
	var captured storage.TenantWriteTx
	errSentinel := errors.New("rollback")
	err := writer.BeginFencedWrite(context.Background(), "w1", 1, func(tx storage.TenantWriteTx) error {
		captured = tx
		if err := tx.EnqueueOutbox(context.Background(), event); err != nil {
			return err
		}
		return errSentinel
	})
	if !errors.Is(err, errSentinel) {
		t.Fatalf("callback error = %v", err)
	}
	assertSQLiteOutboxCount(t, db, 0)
	if err := captured.EnqueueOutbox(context.Background(), event); !errors.Is(err, storage.ErrTenantWriteTxClosed) {
		t.Fatalf("closed tx error = %v", err)
	}
	if err := writer.BeginFencedWrite(context.Background(), "w1", 2, func(storage.TenantWriteTx) error { return nil }); !errors.Is(err, storage.ErrTenantEpochMismatch) {
		t.Fatalf("stale/future epoch error = %v", err)
	}
	if err := writer.BeginFencedWrite(context.Background(), "missing", 1, func(storage.TenantWriteTx) error { return nil }); !errors.Is(err, storage.ErrTenantWorkspaceMissing) {
		t.Fatalf("missing anchor error = %v", err)
	}
}

func TestSQLiteFenceWaitsForWritesAndInvalidatesOldEpoch(t *testing.T) {
	cfg, db := createSQLiteTenantWriterFixture(t)
	writer := NewTenantWriter(cfg)
	writeStarted := make(chan struct{})
	releaseWrite := make(chan struct{})
	writeDone := make(chan error, 1)
	go func() {
		writeDone <- writer.BeginFencedWrite(context.Background(), "w1", 1, func(tx storage.TenantWriteTx) error {
			close(writeStarted)
			<-releaseWrite
			return tx.EnqueueOutbox(context.Background(), storage.TenantOutboxEvent{ID: "e1", Topic: "note.saved", AggregateID: "n1", AggregateRevision: 1, PayloadJSON: `{}`})
		})
	}()
	<-writeStarted
	fenceDone := make(chan error, 1)
	go func() {
		_, err := writer.FenceWorkspace(context.Background(), "w1", 1, "m1")
		fenceDone <- err
	}()
	select {
	case err := <-fenceDone:
		t.Fatalf("fence did not drain active write: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseWrite)
	if err := <-writeDone; err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := <-fenceDone; err != nil {
		t.Fatalf("fence: %v", err)
	}
	assertSQLiteOutboxCount(t, db, 1)
	if err := writer.BeginFencedWrite(context.Background(), "w1", 1, func(storage.TenantWriteTx) error { return nil }); !errors.Is(err, storage.ErrTenantEpochMismatch) {
		t.Fatalf("old epoch after fence = %v", err)
	}
	if err := writer.BeginFencedWrite(context.Background(), "w1", 2, func(storage.TenantWriteTx) error { return nil }); !errors.Is(err, storage.ErrTenantWorkspaceFenced) {
		t.Fatalf("fenced state write = %v", err)
	}
	if err := writer.ActivateWorkspace(context.Background(), "w1", 2, "m1"); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if err := writer.BeginFencedWrite(context.Background(), "w1", 2, func(storage.TenantWriteTx) error { return nil }); err != nil {
		t.Fatalf("new epoch write after activation: %v", err)
	}
}

func TestSQLiteTaskWritePublishesAtomicMobileV2ServerChange(t *testing.T) {
	cfg, db := createSQLiteTenantWriterFixture(t)
	writer := NewTenantWriter(cfg)
	if err := writer.BeginFencedWrite(context.Background(), "w1", 1, func(tx storage.TenantWriteTx) error {
		return tx.TaskDomainWriter().EnsureSystemProjects(context.Background())
	}); err != nil {
		t.Fatal(err)
	}
	var sequence int64
	var entities string
	if err := db.QueryRow(`SELECT sequence,entities_json
		FROM mobile_v2_scope_change_batches
		WHERE workspace_id='w1' AND scope='iphone-task-core'`).Scan(&sequence, &entities); err != nil {
		t.Fatal(err)
	}
	if sequence != 1 || entities == "[]" {
		t.Fatalf("sequence=%d entities=%s", sequence, entities)
	}
	var latest int64
	if err := db.QueryRow(`SELECT latest_sequence FROM mobile_v2_commit_heads WHERE workspace_id='w1'`).
		Scan(&latest); err != nil {
		t.Fatal(err)
	}
	if latest != sequence {
		t.Fatalf("latest=%d sequence=%d", latest, sequence)
	}
}

func TestSQLiteProjectDeletePublishesMobileV2Tombstone(t *testing.T) {
	cfg, db := createSQLiteTenantWriterFixture(t)
	writer := NewTenantWriter(cfg)
	project := taskdomain.Project{
		WorkspaceID: "w1", ID: "project-delete", Name: "Delete me",
		Kind: taskdomain.ProjectKindStandard, Horizon: taskdomain.ProjectHorizonShort,
		Status: taskdomain.ProjectStatusPlanning,
	}
	if err := writer.BeginFencedWrite(context.Background(), "w1", 1, func(tx storage.TenantWriteTx) error {
		return tx.ProjectWriter().SaveProject(context.Background(), taskdomain.ProjectWrite{Project: project})
	}); err != nil {
		t.Fatal(err)
	}
	if err := writer.BeginFencedWrite(context.Background(), "w1", 1, func(tx storage.TenantWriteTx) error {
		return tx.ProjectWriter().DeleteProject(context.Background(), project.ID, 1)
	}); err != nil {
		t.Fatal(err)
	}
	var entities string
	if err := db.QueryRow(`SELECT entities_json FROM mobile_v2_scope_change_batches
		WHERE workspace_id='w1' AND scope='iphone-task-core' AND sequence=2`).Scan(&entities); err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		`"entity_type":"project"`, `"entity_id":"project-delete"`,
		`"entity_revision":"2"`, `"deleted_at":`, `"payload":null`,
	} {
		if !strings.Contains(entities, fragment) {
			t.Fatalf("project tombstone missing %s: %s", fragment, entities)
		}
	}
}

func TestSQLiteWebNoteWritePublishesAtomicMobileV2ContentChange(t *testing.T) {
	_, db := createSQLiteTenantWriterFixture(t)
	if _, err := db.Exec(`
		CREATE TABLE mobile_sync_outbox (
			sequence INTEGER PRIMARY KEY AUTOINCREMENT,
			workspace_id TEXT NOT NULL,
			mutation_id TEXT NOT NULL,
			entity_type TEXT NOT NULL,
			entity_client_id TEXT NOT NULL,
			operation TEXT NOT NULL,
			revision INTEGER NOT NULL,
			entity_json TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			published_at INTEGER,
			UNIQUE (workspace_id, mutation_id, entity_type, entity_client_id)
		);
		CREATE TABLE mobile_retired_ids (
			workspace_id TEXT NOT NULL,
			entity_type TEXT NOT NULL,
			client_id TEXT NOT NULL,
			retired_at INTEGER NOT NULL,
			PRIMARY KEY (workspace_id, entity_type, client_id)
		);
		INSERT INTO folders(id,workspace_id,name) VALUES('__uncategorized','w1','Uncategorized');
	`); err != nil {
		t.Fatal(err)
	}
	ctx := auth.ContextWithWorkspaceScope(context.Background(), "w1")
	note := &model.Note{ID: "note-web-v2", Title: "Web note", Body: "body", FolderID: "__uncategorized"}
	if err := (noteRepository{db: db}).CreateWithID(ctx, note); err != nil {
		t.Fatal(err)
	}
	var sequence int64
	var entities string
	if err := db.QueryRow(`SELECT sequence,entities_json
		FROM mobile_v2_scope_change_batches
		WHERE workspace_id='w1' AND scope='iphone-content'`).Scan(&sequence, &entities); err != nil {
		t.Fatal(err)
	}
	if sequence != 1 || !strings.Contains(entities, `"entity_id":"note-web-v2"`) {
		t.Fatalf("sequence=%d entities=%s", sequence, entities)
	}
	taskTimestamp := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO domain_projects_v2
		(workspace_id,id,name,description,kind,horizon,status,revision,created_at,updated_at)
		VALUES('w1','project-web-v2','Project','Description','standard','short','active',2,?,?)`,
		taskTimestamp, taskTimestamp); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO domain_tasks_v2
		(workspace_id,id,project_id,note_id,title,description,lifecycle_status,priority,sort_order,revision,created_at,updated_at)
		VALUES('w1','task-web-v2','project-web-v2','note-web-v2','Task','Description','active',1,0,3,?,?)`,
		taskTimestamp, taskTimestamp); err != nil {
		t.Fatal(err)
	}
	taskTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := taskTx.Exec(`INSERT INTO domain_task_schedules_v2
		(workspace_id,task_id,revision,current_schedule_revision,generation_status,updated_at)
		VALUES('w1','task-web-v2',4,1,'idle',?)`, taskTimestamp); err != nil {
		taskTx.Rollback()
		t.Fatal(err)
	}
	if _, err := taskTx.Exec(`INSERT INTO domain_task_schedule_versions_v2
		(workspace_id,task_id,schedule_revision,recurrence_type,timing_type,timezone,starts_on,recurrence_rule,created_at)
		VALUES('w1','task-web-v2',1,'none','date','Asia/Shanghai','2026-08-01','{}',?)`, taskTimestamp); err != nil {
		taskTx.Rollback()
		t.Fatal(err)
	}
	if _, err := taskTx.Exec(`INSERT INTO domain_task_occurrences_v2
		(workspace_id,id,task_id,occurrence_key,planned_date,execution_status,note_id,revision,generated_schedule_revision,created_at,updated_at)
		VALUES('w1','occurrence-web-v2','task-web-v2','2026-08-01','2026-08-01','open','note-web-v2',5,1,?,?)`,
		taskTimestamp, taskTimestamp); err != nil {
		taskTx.Rollback()
		t.Fatal(err)
	}
	if err := taskTx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO tasks(id,workspace_id,note_id,title,updated_at)
		VALUES('legacy-task-web-v2','w1','note-web-v2','Legacy task',?)`, taskTimestamp); err != nil {
		t.Fatal(err)
	}
	if err := (noteRepository{db: db}).Delete(ctx, note.ID); err != nil {
		t.Fatal(err)
	}
	var taskNoteID, occurrenceNoteID, legacyNoteID sql.NullString
	var taskRevision, occurrenceRevision int64
	if err := db.QueryRow(`SELECT note_id,revision FROM domain_tasks_v2
		WHERE workspace_id='w1' AND id='task-web-v2'`).Scan(&taskNoteID, &taskRevision); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT note_id,revision FROM domain_task_occurrences_v2
		WHERE workspace_id='w1' AND id='occurrence-web-v2'`).Scan(&occurrenceNoteID, &occurrenceRevision); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT note_id FROM tasks
		WHERE workspace_id='w1' AND id='legacy-task-web-v2'`).Scan(&legacyNoteID); err != nil {
		t.Fatal(err)
	}
	if taskNoteID.Valid || occurrenceNoteID.Valid || legacyNoteID.Valid || taskRevision != 4 || occurrenceRevision != 6 {
		t.Fatalf("web delete refs: task=%v/%d occurrence=%v/%d legacy=%v",
			taskNoteID, taskRevision, occurrenceNoteID, occurrenceRevision, legacyNoteID)
	}
	for _, scope := range []string{"iphone-content", "iphone-task-core", "iphone-occurrence-window", "watch-occurrence-window"} {
		var changed string
		if err := db.QueryRow(`SELECT entities_json FROM mobile_v2_scope_change_batches
			WHERE workspace_id='w1' AND scope=? AND sequence=2`, scope).Scan(&changed); err != nil {
			t.Fatal(err)
		}
		if scope != "iphone-content" && !strings.Contains(changed, `"note_id":null`) {
			t.Fatalf("web delete scope %s = %s", scope, changed)
		}
	}
}

func createSQLiteTenantWriterFixture(t *testing.T) (storage.Config, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tenant-writer.db")
	cfg := storage.Config{Env: "test", Driver: storage.DriverSQLite, SQLitePath: path}
	if err := (Provider{}).MigrateTenant(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`INSERT INTO tenant_workspaces(workspace_id) VALUES('w1')`); err != nil {
		t.Fatal(err)
	}
	return cfg, db
}

func assertSQLiteOutboxCount(t *testing.T, db *sql.DB, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(`SELECT COUNT(*) FROM tenant_job_outbox`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("outbox count=%d want=%d", got, want)
	}
}
