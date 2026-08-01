package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hujinrun/flowspace/internal/storage"
)

func TestSQLiteTenantBaselineKeepsInstallationIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.db")
	cfg := storage.Config{Env: "test", Driver: storage.DriverSQLite, SQLitePath: path}
	p := Provider{}
	if err := p.MigrateTenant(context.Background(), cfg); err != nil {
		t.Fatalf("first tenant migration: %v", err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var firstID string
	if err := db.QueryRow(`SELECT installation_id FROM tenant_installations WHERE singleton_key=1`).Scan(&firstID); err != nil {
		t.Fatal(err)
	}
	if firstID == "" {
		t.Fatal("installation id must not be empty")
	}
	if err := p.MigrateTenant(context.Background(), cfg); err != nil {
		t.Fatalf("second tenant migration: %v", err)
	}
	var secondID string
	if err := db.QueryRow(`SELECT installation_id FROM tenant_installations WHERE singleton_key=1`).Scan(&secondID); err != nil {
		t.Fatal(err)
	}
	if firstID != secondID {
		t.Fatalf("installation id changed: %q -> %q", firstID, secondID)
	}
	for _, forbidden := range []string{"users", "sessions", "workspace_service_bindings", "audit_events"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, forbidden).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Errorf("tenant baseline contains control table %s", forbidden)
		}
	}
	store, err := p.OpenTenant(context.Background(), cfg, "0001_tenant_baseline.sql")
	if err != nil {
		t.Fatalf("open migrated tenant: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close migrated tenant: %v", err)
	}
}

func TestSQLiteContentRetentionMigrationRedactsExistingDeletedPayloads(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tenant-retention-upgrade.db")
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `CREATE TABLE tenant_schema_migrations(
		version TEXT PRIMARY KEY,checksum TEXT NOT NULL,applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	dir, err := findSQLiteTenantMigrationsDir()
	if err != nil {
		t.Fatal(err)
	}
	migrations, err := loadSQLiteTenantMigrations(dir)
	if err != nil {
		t.Fatal(err)
	}
	var retentionMigration tenantMigration
	for _, migration := range migrations {
		if migration.version == "0012_mobile_v2_content_retention.sql" {
			retentionMigration = migration
			break
		}
		if err := applySQLiteTenantMigration(ctx, db, migration); err != nil {
			t.Fatalf("apply prerequisite %s: %v", migration.version, err)
		}
	}
	if retentionMigration.version == "" {
		t.Fatal("content retention migration is missing")
	}
	const workspaceID = "retention-upgrade-workspace"
	const noteID = "retention-upgrade-note"
	const clientID = "retention-upgrade-client"
	if _, err := db.Exec(`INSERT INTO tenant_workspaces(workspace_id) VALUES(?)`, workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO notes
		(id,workspace_id,client_id,revision,title,body,tags,content,content_text,deleted_at,created_at,updated_at)
		VALUES(?,?,?,2,'private title','private body','["private"]','private-json','private-text',100,100,100)`,
		noteID, workspaceID, clientID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO mobile_v2_commit_heads(workspace_id,latest_sequence) VALUES(?,1)`, workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO mobile_v2_command_receipts
		(workspace_id,origin_device_client_id,command_id,request_digest,command_type,status,commit_sequence,receipt_json,completed_at)
		VALUES(?,'device','command','digest','note.delete','applied',1,'{}','2026-08-01T00:00:00Z')`, workspaceID); err != nil {
		t.Fatal(err)
	}
	deletedEntity := `[{"entity_type":"note","entity_id":"retention-upgrade-note","client_id":"retention-upgrade-client","entity_revision":"2","aggregate_revisions":{},"deleted_at":"2026-08-01T00:00:00.000Z","payload":{"title":"private title","body":"private body"}}]`
	if _, err := db.Exec(`INSERT INTO mobile_v2_change_batches
		(workspace_id,sequence,caused_by_command_id,origin_device_client_id,receipt_json,after_images_json,committed_at)
		VALUES(?,1,'command','device','{}',?,'2026-08-01T00:00:00Z')`, workspaceID, deletedEntity); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO mobile_v2_scope_change_batches
		(workspace_id,scope,sequence,caused_by_command_id,origin_device_client_id,receipt_json,entities_json,committed_at)
		VALUES(?,'iphone-content',1,'command','device','{}',?,'2026-08-01T00:00:00Z')`, workspaceID, deletedEntity); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO mobile_v2_scope_change_batches
		(workspace_id,scope,sequence,caused_by_command_id,origin_device_client_id,receipt_json,entities_json,committed_at)
		VALUES(?,'iphone-task-core',1,'command','device','{}','[]','2026-08-01T00:00:00Z')`, workspaceID); err != nil {
		t.Fatal(err)
	}
	taskTimestamp := "2026-08-01T00:00:00.000Z"
	if _, err := db.Exec(`INSERT INTO domain_projects_v2
		(workspace_id,id,name,description,kind,horizon,status,revision,created_at,updated_at)
		VALUES(?,'project-1','Project','Description','standard','short','active',2,?,?)`,
		workspaceID, taskTimestamp, taskTimestamp); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO domain_tasks_v2
		(workspace_id,id,project_id,note_id,title,description,lifecycle_status,priority,sort_order,revision,created_at,updated_at)
		VALUES(?,'task-1','project-1',?,'Task','Description','active',1,0,3,?,?)`,
		workspaceID, noteID, taskTimestamp, taskTimestamp); err != nil {
		t.Fatal(err)
	}
	taskTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := taskTx.Exec(`INSERT INTO domain_task_schedules_v2
		(workspace_id,task_id,revision,current_schedule_revision,generation_status,updated_at)
		VALUES(?,'task-1',4,1,'idle',?)`, workspaceID, taskTimestamp); err != nil {
		taskTx.Rollback()
		t.Fatal(err)
	}
	if _, err := taskTx.Exec(`INSERT INTO domain_task_schedule_versions_v2
		(workspace_id,task_id,schedule_revision,recurrence_type,timing_type,timezone,starts_on,recurrence_rule,created_at)
		VALUES(?,'task-1',1,'none','date','Asia/Shanghai','2026-08-01','{}',?)`,
		workspaceID, taskTimestamp); err != nil {
		taskTx.Rollback()
		t.Fatal(err)
	}
	if _, err := taskTx.Exec(`INSERT INTO domain_task_occurrences_v2
		(workspace_id,id,task_id,occurrence_key,planned_date,execution_status,note_id,revision,generated_schedule_revision,created_at,updated_at)
		VALUES(?,'occurrence-1','task-1','2026-08-01','2026-08-01','open',?,5,1,?,?)`,
		workspaceID, noteID, taskTimestamp, taskTimestamp); err != nil {
		taskTx.Rollback()
		t.Fatal(err)
	}
	if err := taskTx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO tasks
		(id,workspace_id,note_id,title,updated_at) VALUES('legacy-task-1',?,?,'Legacy task',?)`,
		workspaceID, noteID, taskTimestamp); err != nil {
		t.Fatal(err)
	}
	checksum := "sha256:" + strings.Repeat("a", 64)
	if _, err := db.Exec(`INSERT INTO mobile_v2_snapshot_sessions
		(snapshot_id,workspace_id,scope,as_of_sequence,contract_epoch,runtime_epoch,task_model_version,
		 projection_as_of,scope_generation,snapshot_cursor,manifest_checksum,page_count,expires_at,created_at)
		VALUES('snapshot',?,'iphone-content',1,1,1,2,'2026-08-01T00:00:00Z','generation','cursor',?,1,
		       '2026-08-01T01:00:00Z','2026-08-01T00:00:00Z')`, workspaceID, checksum); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO mobile_v2_snapshot_pages
		(snapshot_id,page_index,page_checksum,entities_json) VALUES('snapshot',0,?,?)`, checksum, deletedEntity); err != nil {
		t.Fatal(err)
	}
	if err := applySQLiteTenantMigration(ctx, db, retentionMigration); err != nil {
		t.Fatalf("apply content retention migration: %v", err)
	}
	var title, body, tags, content, contentText string
	if err := db.QueryRow(`SELECT title,body,tags,content,content_text FROM notes
		WHERE workspace_id=? AND id=?`, workspaceID, noteID).
		Scan(&title, &body, &tags, &content, &contentText); err != nil {
		t.Fatal(err)
	}
	if title != "" || body != "" || tags != "[]" || content != "" || contentText != "" {
		t.Fatalf("migration did not redact note: %q %q %q %q %q", title, body, tags, content, contentText)
	}
	for _, query := range []string{
		`SELECT after_images_json FROM mobile_v2_change_batches WHERE workspace_id=? AND sequence=1`,
		`SELECT entities_json FROM mobile_v2_scope_change_batches WHERE workspace_id=? AND sequence=1`,
	} {
		var payload string
		if err := db.QueryRow(query, workspaceID).Scan(&payload); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(payload, "private") || !strings.Contains(payload, `"payload":null`) {
			t.Fatalf("migration left sensitive payload in %s: %s", query, payload)
		}
	}
	var snapshotSessions, snapshotPages int
	if err := db.QueryRow(`SELECT COUNT(*) FROM mobile_v2_snapshot_sessions
		WHERE workspace_id=?`, workspaceID).Scan(&snapshotSessions); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM mobile_v2_snapshot_pages
		WHERE snapshot_id='snapshot'`).Scan(&snapshotPages); err != nil {
		t.Fatal(err)
	}
	if snapshotSessions != 0 || snapshotPages != 0 {
		t.Fatalf("migration retained stale snapshot: sessions=%d pages=%d", snapshotSessions, snapshotPages)
	}
	var taskNoteID, occurrenceNoteID, legacyTaskNoteID sql.NullString
	var taskRevision, occurrenceRevision int64
	if err := db.QueryRow(`SELECT note_id,revision FROM domain_tasks_v2
		WHERE workspace_id=? AND id='task-1'`, workspaceID).Scan(&taskNoteID, &taskRevision); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT note_id,revision FROM domain_task_occurrences_v2
		WHERE workspace_id=? AND id='occurrence-1'`, workspaceID).Scan(&occurrenceNoteID, &occurrenceRevision); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT note_id FROM tasks
		WHERE workspace_id=? AND id='legacy-task-1'`, workspaceID).Scan(&legacyTaskNoteID); err != nil {
		t.Fatal(err)
	}
	if taskNoteID.Valid || occurrenceNoteID.Valid || legacyTaskNoteID.Valid || taskRevision != 4 || occurrenceRevision != 6 {
		t.Fatalf("migration did not detach refs: task=%v/%d occurrence=%v/%d legacy=%v",
			taskNoteID, taskRevision, occurrenceNoteID, occurrenceRevision, legacyTaskNoteID)
	}
	var latestSequence int64
	if err := db.QueryRow(`SELECT latest_sequence FROM mobile_v2_commit_heads
		WHERE workspace_id=?`, workspaceID).Scan(&latestSequence); err != nil {
		t.Fatal(err)
	}
	var taskRetentionRows, oldTaskScopeRows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM mobile_v2_scope_retention
		WHERE workspace_id=? AND scope IN ('iphone-task-core','iphone-occurrence-window','watch-occurrence-window')
		  AND compacted_through_sequence=2`, workspaceID).Scan(&taskRetentionRows); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM mobile_v2_scope_change_batches
		WHERE workspace_id=? AND scope='iphone-task-core' AND sequence=1`, workspaceID).Scan(&oldTaskScopeRows); err != nil {
		t.Fatal(err)
	}
	if latestSequence != 2 || taskRetentionRows != 3 || oldTaskScopeRows != 0 {
		t.Fatalf("migration resnapshot boundary: latest=%d retention=%d old_batches=%d",
			latestSequence, taskRetentionRows, oldTaskScopeRows)
	}
	var tombstoneCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM mobile_v2_content_tombstones
		WHERE workspace_id=? AND entity_type='note' AND entity_id=? AND revision=2`, workspaceID, noteID).
		Scan(&tombstoneCount); err != nil {
		t.Fatal(err)
	}
	if tombstoneCount != 1 {
		t.Fatalf("backfilled tombstones = %d", tombstoneCount)
	}
}

func TestSQLiteOpenTenantRejectsChecksumMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant-checksum.db")
	cfg := storage.Config{Env: "test", Driver: storage.DriverSQLite, SQLitePath: path}
	p := Provider{}
	if err := p.MigrateTenant(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE tenant_schema_migrations SET checksum='tampered' WHERE version='0001_tenant_baseline.sql'`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	store, err := p.OpenTenant(context.Background(), cfg, "0001_tenant_baseline.sql")
	if store != nil {
		_ = store.Close()
		t.Fatal("checksum mismatch must not return a store")
	}
	if !errors.Is(err, storage.ErrTenantSchemaNotReady) {
		t.Fatalf("expected tenant schema not ready, got %v", err)
	}
}
