package mobilev2service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/handler"
	"github.com/hujinrun/flowspace/internal/mobilev2command"
	"github.com/hujinrun/flowspace/internal/mobilev2contract"
	"github.com/hujinrun/flowspace/internal/mobilev2projection"
	"github.com/hujinrun/flowspace/internal/mobilev2sync"
	"github.com/hujinrun/flowspace/internal/storage"
	storagesqlite "github.com/hujinrun/flowspace/internal/storage/sqlite"
	"github.com/hujinrun/flowspace/internal/taskapp"
	"github.com/hujinrun/flowspace/internal/taskruntime"
)

func TestCommandExecutorCommitsReplaysAndReturnsTerminalConflict(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tenant.db")
	cfg := storage.Config{Env: "test", Driver: storage.DriverSQLite, SQLitePath: path}
	if err := (storagesqlite.Provider{}).MigrateTenant(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO tenant_workspaces(workspace_id,epoch,state) VALUES('workspace-commands',1,'active')`); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 29, 9, 30, 0, 0, time.UTC)
	resolver := mobileV2RuntimeResolverFake{snapshot: taskruntime.MobileRuntimeSnapshot{
		WorkspaceID: "workspace-commands", Epoch: 1, Driver: storage.DriverSQLite, DB: db,
		Writer:      storagesqlite.NewTenantWriter(cfg),
		Application: taskapp.RuntimeSnapshot{WorkspaceID: "workspace-commands", Epoch: 1},
	}}
	executor, err := NewCommandExecutor(CommandExecutorConfig{
		Runtime: resolver, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	identity := handler.MobileV2Identity{WorkspaceID: "workspace-commands", UserID: "user-1"}
	create := signedCommand(t, `{
		"command_id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		"request_digest":"DIGEST",
		"origin_device_client_id":"cccccccc-cccc-4ccc-8ccc-cccccccccccc",
		"workspace_id":"workspace-commands",
		"command_type":"project.create",
		"target":{"entity_id":null,"client_id":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"},
		"created_runtime_epoch":"1",
		"expected":{},
		"depends_on_command_id":null,
		"supersedes_command_id":null,
		"payload":{"name":"Mobile Project","kind":"standard","horizon":"short","status":"planning"}
	}`)
	first, err := executor.ApplyCommand(ctx, handler.MobileV2CommandRequest{Identity: identity, RawEnvelope: create})
	if err != nil {
		t.Fatal(err)
	}
	firstWire := commandResponseWire(t, first)
	if firstWire.Replayed || firstWire.Receipt.Status != mobilev2command.StatusApplied ||
		firstWire.Receipt.CommitSequence != 1 || len(firstWire.Receipt.IdentityMappings) != 1 {
		t.Fatalf("first response = %#v", firstWire)
	}
	projectID := *firstWire.Receipt.IdentityMappings[0].EntityID
	var projectCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM domain_projects_v2 WHERE workspace_id=? AND id=?`,
		identity.WorkspaceID, projectID).Scan(&projectCount); err != nil || projectCount != 1 {
		t.Fatalf("project count=%d err=%v", projectCount, err)
	}

	replayed, err := executor.ApplyCommand(ctx, handler.MobileV2CommandRequest{Identity: identity, RawEnvelope: create})
	if err != nil {
		t.Fatal(err)
	}
	replayedWire := commandResponseWire(t, replayed)
	if !replayedWire.Replayed || replayedWire.Receipt.CommitSequence != 1 {
		t.Fatalf("replayed response = %#v", replayedWire)
	}

	conflict := signedCommand(t, strings.ReplaceAll(`{
		"command_id":"dddddddd-dddd-4ddd-8ddd-dddddddddddd",
		"request_digest":"DIGEST",
		"origin_device_client_id":"cccccccc-cccc-4ccc-8ccc-cccccccccccc",
		"workspace_id":"workspace-commands",
		"command_type":"project.update",
		"target":{"entity_id":"PROJECT_ID","client_id":null},
		"created_runtime_epoch":"1",
		"expected":{"project_revision":{"source":"exact","value":"99","dependency_command_id":null}},
		"depends_on_command_id":null,
		"supersedes_command_id":null,
		"payload":{"name":"Must Conflict"}
	}`, "PROJECT_ID", projectID))
	conflictValue, err := executor.ApplyCommand(ctx, handler.MobileV2CommandRequest{Identity: identity, RawEnvelope: conflict})
	if err != nil {
		t.Fatal(err)
	}
	conflictWire := commandResponseWire(t, conflictValue)
	if conflictWire.Receipt.Status != mobilev2command.StatusConflict ||
		conflictWire.Receipt.CommitSequence != 2 {
		t.Fatalf("conflict response = %#v", conflictWire)
	}
	var receiptCount, scopeCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM mobile_v2_command_receipts WHERE workspace_id=?`,
		identity.WorkspaceID).Scan(&receiptCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM mobile_v2_scope_change_batches WHERE workspace_id=?`,
		identity.WorkspaceID).Scan(&scopeCount); err != nil {
		t.Fatal(err)
	}
	if receiptCount != 2 || scopeCount != 2 {
		t.Fatalf("receipt_count=%d scope_count=%d", receiptCount, scopeCount)
	}
	var rawReceipt []byte
	if err := db.QueryRow(`SELECT receipt_json FROM mobile_v2_command_receipts
		WHERE workspace_id=? AND command_id=?`, identity.WorkspaceID, createCommandID(create)).Scan(&rawReceipt); err != nil {
		t.Fatal(err)
	}
	var receiptObject map[string]any
	if err := json.Unmarshal(rawReceipt, &receiptObject); err != nil {
		t.Fatal(err)
	}
	if receiptObject["commit_sequence"] != "1" || receiptObject["workspace_id"] != identity.WorkspaceID {
		t.Fatalf("stored receipt wire = %s", rawReceipt)
	}
}

func TestContentCommandExecutorCommitsNoteVoiceAndTranscriptionChanges(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tenant-content.db")
	cfg := storage.Config{Env: "test", Driver: storage.DriverSQLite, SQLitePath: path}
	if err := (storagesqlite.Provider{}).MigrateTenant(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const workspaceID = "workspace-content"
	if _, err := db.Exec(`INSERT INTO tenant_workspaces(workspace_id,epoch,state) VALUES(?,1,'active')`, workspaceID); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC)
	executor, err := NewCommandExecutor(CommandExecutorConfig{
		Runtime: mobileV2RuntimeResolverFake{snapshot: taskruntime.MobileRuntimeSnapshot{
			WorkspaceID: workspaceID, Epoch: 1, Driver: storage.DriverSQLite, DB: db,
			Writer: storagesqlite.NewTenantWriter(cfg),
		}},
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	identity := handler.MobileV2Identity{WorkspaceID: workspaceID, UserID: "user-content"}
	noteCreate := signedCommand(t, `{
		"command_id":"10000000-0000-4000-8000-000000000001",
		"request_digest":"DIGEST",
		"origin_device_client_id":"20000000-0000-4000-8000-000000000001",
		"workspace_id":"workspace-content",
		"command_type":"note.create",
		"target":{"entity_id":null,"client_id":"30000000-0000-4000-8000-000000000001"},
		"created_runtime_epoch":"1",
		"expected":{},
		"depends_on_command_id":null,
		"supersedes_command_id":null,
		"payload":{"title":"Mobile note","body":"body","tags":["mobile","v2"]}
	}`)
	noteValue, err := executor.ApplyCommand(ctx, handler.MobileV2CommandRequest{Identity: identity, RawEnvelope: noteCreate})
	if err != nil {
		t.Fatal(err)
	}
	noteWire := commandResponseWire(t, noteValue)
	if noteWire.Receipt.Status != mobilev2command.StatusApplied || noteWire.Receipt.CommitSequence != 1 ||
		len(noteWire.Receipt.IdentityMappings) != 1 {
		t.Fatalf("note receipt = %#v", noteWire)
	}
	noteID := *noteWire.Receipt.IdentityMappings[0].EntityID
	var noteBody, noteTags string
	if err := db.QueryRow(`SELECT body,tags FROM notes WHERE workspace_id=? AND id=?`, workspaceID, noteID).
		Scan(&noteBody, &noteTags); err != nil {
		t.Fatal(err)
	}
	if noteBody != "body" || noteTags != `["mobile","v2"]` {
		t.Fatalf("note body=%q tags=%q", noteBody, noteTags)
	}

	noteUpdate := signedCommand(t, strings.ReplaceAll(`{
		"command_id":"10000000-0000-4000-8000-000000000002",
		"request_digest":"DIGEST",
		"origin_device_client_id":"20000000-0000-4000-8000-000000000001",
		"workspace_id":"workspace-content",
		"command_type":"note.update",
		"target":{"entity_id":"NOTE_ID","client_id":null},
		"created_runtime_epoch":"1",
		"expected":{"entity_revision":{"source":"exact","value":"1","dependency_command_id":null}},
		"depends_on_command_id":null,
		"supersedes_command_id":null,
		"payload":{"body":"updated"}
	}`, "NOTE_ID", noteID))
	noteUpdateValue, err := executor.ApplyCommand(ctx, handler.MobileV2CommandRequest{Identity: identity, RawEnvelope: noteUpdate})
	if err != nil {
		t.Fatal(err)
	}
	if receipt := commandResponseWire(t, noteUpdateValue).Receipt; receipt.Status != mobilev2command.StatusApplied ||
		receipt.AffectedRevisions[0].Revision != "2" {
		t.Fatalf("note update receipt = %#v", receipt)
	}

	voiceCreate := signedCommand(t, `{
		"command_id":"10000000-0000-4000-8000-000000000003",
		"request_digest":"DIGEST",
		"origin_device_client_id":"20000000-0000-4000-8000-000000000001",
		"workspace_id":"workspace-content",
		"command_type":"voice.create",
		"target":{"entity_id":null,"client_id":"30000000-0000-4000-8000-000000000002"},
		"created_runtime_epoch":"1",
		"expected":{},
		"depends_on_command_id":null,
		"supersedes_command_id":null,
		"payload":{"title":"Voice","duration_ms":"1200","recorded_at":"2026-07-29T10:00:00.000Z","language":"zh-CN"}
	}`)
	voiceValue, err := executor.ApplyCommand(ctx, handler.MobileV2CommandRequest{Identity: identity, RawEnvelope: voiceCreate})
	if err != nil {
		t.Fatal(err)
	}
	voiceReceipt := commandResponseWire(t, voiceValue).Receipt
	if voiceReceipt.Status != mobilev2command.StatusApplied || len(voiceReceipt.IdentityMappings) != 2 {
		t.Fatalf("voice receipt = %#v", voiceReceipt)
	}
	voiceID := *voiceReceipt.IdentityMappings[0].EntityID

	voiceUpdate := signedCommand(t, strings.ReplaceAll(`{
		"command_id":"10000000-0000-4000-8000-000000000004",
		"request_digest":"DIGEST",
		"origin_device_client_id":"20000000-0000-4000-8000-000000000001",
		"workspace_id":"workspace-content",
		"command_type":"voice.update",
		"target":{"entity_id":"VOICE_ID","client_id":null},
		"created_runtime_epoch":"1",
		"expected":{"entity_revision":{"source":"exact","value":"1","dependency_command_id":null}},
		"depends_on_command_id":null,
		"supersedes_command_id":null,
		"payload":{"title":"Renamed voice"}
	}`, "VOICE_ID", voiceID))
	voiceUpdateValue, err := executor.ApplyCommand(ctx, handler.MobileV2CommandRequest{Identity: identity, RawEnvelope: voiceUpdate})
	if err != nil {
		t.Fatal(err)
	}
	voiceUpdateReceipt := commandResponseWire(t, voiceUpdateValue).Receipt
	if voiceUpdateReceipt.Status != mobilev2command.StatusApplied ||
		voiceUpdateReceipt.CommitSequence != 4 || len(voiceUpdateReceipt.AffectedRevisions) != 2 {
		t.Fatalf("voice update receipt = %#v", voiceUpdateReceipt)
	}
	var voiceTitle string
	if err := db.QueryRow(`SELECT n.title FROM voice_notes v JOIN notes n ON n.id=v.note_id
		WHERE v.workspace_id=? AND v.id=?`, workspaceID, voiceID).Scan(&voiceTitle); err != nil {
		t.Fatal(err)
	}
	if voiceTitle != "Renamed voice" {
		t.Fatalf("voice title = %q", voiceTitle)
	}

	transcription := signedCommand(t, strings.ReplaceAll(`{
		"command_id":"10000000-0000-4000-8000-000000000005",
		"request_digest":"DIGEST",
		"origin_device_client_id":"20000000-0000-4000-8000-000000000001",
		"workspace_id":"workspace-content",
		"command_type":"transcription.request",
		"target":{"entity_id":"VOICE_ID","client_id":null},
		"created_runtime_epoch":"1",
		"expected":{"entity_revision":{"source":"exact","value":"2","dependency_command_id":null}},
		"depends_on_command_id":null,
		"supersedes_command_id":null,
		"payload":{"language":"zh-CN","failed_job_id":null}
	}`, "VOICE_ID", voiceID))
	transcriptionValue, err := executor.ApplyCommand(ctx, handler.MobileV2CommandRequest{Identity: identity, RawEnvelope: transcription})
	if err != nil {
		t.Fatal(err)
	}
	transcriptionReceipt := commandResponseWire(t, transcriptionValue).Receipt
	if transcriptionReceipt.Status != mobilev2command.StatusApplied ||
		transcriptionReceipt.CommitSequence != 5 || len(transcriptionReceipt.AffectedRevisions) != 2 {
		t.Fatalf("transcription receipt = %#v", transcriptionReceipt)
	}
	var receiptCount, contentScopeCount, jobCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM mobile_v2_command_receipts WHERE workspace_id=?`, workspaceID).
		Scan(&receiptCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM mobile_v2_scope_change_batches
		WHERE workspace_id=? AND scope='iphone-content'`, workspaceID).Scan(&contentScopeCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM transcription_jobs WHERE workspace_id=?`, workspaceID).
		Scan(&jobCount); err != nil {
		t.Fatal(err)
	}
	if receiptCount != 5 || contentScopeCount != 5 || jobCount != 1 {
		t.Fatalf("receipts=%d content_scopes=%d jobs=%d", receiptCount, contentScopeCount, jobCount)
	}
}

func TestContentDeleteRedactsPayloadCleansAttachmentsAndRetainsMinimalTombstone(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tenant-content-retention.db")
	cfg := storage.Config{Env: "test", Driver: storage.DriverSQLite, SQLitePath: path}
	if err := (storagesqlite.Provider{}).MigrateTenant(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const workspaceID = "workspace-content-retention"
	if _, err := db.Exec(`INSERT INTO tenant_workspaces(workspace_id,epoch,state) VALUES(?,1,'active')`, workspaceID); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	resolver := mobileV2RuntimeResolverFake{snapshot: taskruntime.MobileRuntimeSnapshot{
		WorkspaceID: workspaceID, Epoch: 1, Driver: storage.DriverSQLite, DB: db,
		Writer: storagesqlite.NewTenantWriter(cfg),
	}}
	executor, err := NewCommandExecutor(CommandExecutorConfig{
		Runtime: resolver,
		Now:     func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	identity := handler.MobileV2Identity{WorkspaceID: workspaceID, UserID: "user-content-retention"}
	const noteClientID = "71000000-0000-4000-8000-000000000001"
	create := signedCommand(t, `{
		"command_id":"71000000-0000-4000-8000-000000000002",
		"request_digest":"DIGEST",
		"origin_device_client_id":"71000000-0000-4000-8000-000000000003",
		"workspace_id":"workspace-content-retention",
		"command_type":"note.create",
		"target":{"entity_id":null,"client_id":"71000000-0000-4000-8000-000000000001"},
		"created_runtime_epoch":"1",
		"expected":{},
		"depends_on_command_id":null,
		"supersedes_command_id":null,
		"payload":{"title":"private title","body":"private body","tags":["private-tag"]}
	}`)
	createdValue, err := executor.ApplyCommand(ctx, handler.MobileV2CommandRequest{Identity: identity, RawEnvelope: create})
	if err != nil {
		t.Fatal(err)
	}
	noteID := *commandResponseWire(t, createdValue).Receipt.IdentityMappings[0].EntityID
	if _, err := db.Exec(`INSERT INTO note_attachments
		(id,workspace_id,note_id,kind,original_name,mime_type,size_bytes,sha256,object_key,created_at)
		VALUES('attachment-1',?,?, 'video','private.mov','video/quicktime',12,'sha','attachments/private.mov',?)`,
		workspaceID, noteID, now.Unix()); err != nil {
		t.Fatal(err)
	}
	taskTimestamp := now.Format(time.RFC3339Nano)
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
	if err := taskTx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO domain_task_occurrences_v2
		(workspace_id,id,task_id,occurrence_key,planned_date,execution_status,note_id,revision,generated_schedule_revision,created_at,updated_at)
		VALUES(?,'occurrence-1','task-1','2026-08-01','2026-08-01','open',?,5,1,?,?)`,
		workspaceID, noteID, taskTimestamp, taskTimestamp); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO tasks
		(id,workspace_id,note_id,title,updated_at) VALUES('legacy-task-1',?,?,'Legacy task',?)`,
		workspaceID, noteID, taskTimestamp); err != nil {
		t.Fatal(err)
	}
	deleteCommand := signedCommand(t, strings.ReplaceAll(`{
		"command_id":"71000000-0000-4000-8000-000000000004",
		"request_digest":"DIGEST",
		"origin_device_client_id":"71000000-0000-4000-8000-000000000003",
		"workspace_id":"workspace-content-retention",
		"command_type":"note.delete",
		"target":{"entity_id":"NOTE_ID","client_id":null},
		"created_runtime_epoch":"1",
		"expected":{"entity_revision":{"source":"exact","value":"1","dependency_command_id":null}},
		"depends_on_command_id":null,
		"supersedes_command_id":null,
		"payload":{}
	}`, "NOTE_ID", noteID))
	deletedValue, err := executor.ApplyCommand(ctx, handler.MobileV2CommandRequest{Identity: identity, RawEnvelope: deleteCommand})
	if err != nil {
		t.Fatal(err)
	}
	deleteReceipt := commandResponseWire(t, deletedValue).Receipt
	if deleteReceipt.Status != mobilev2command.StatusApplied {
		t.Fatalf("delete receipt = %#v", deleteReceipt)
	}
	affected := make(map[string]string, len(deleteReceipt.AffectedRevisions))
	for _, revision := range deleteReceipt.AffectedRevisions {
		affected[revision.EntityType+":"+revision.EntityID] = revision.Revision
	}
	if affected["note:"+noteID] != "2" || affected["task:task-1"] != "4" ||
		affected["task_occurrence:occurrence-1"] != "6" {
		t.Fatalf("delete affected revisions = %#v", affected)
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
		t.Fatalf("detached refs: task=%v/%d occurrence=%v/%d legacy=%v",
			taskNoteID, taskRevision, occurrenceNoteID, occurrenceRevision, legacyTaskNoteID)
	}
	var title, body, tags, content, contentText string
	var deletedAt sql.NullInt64
	if err := db.QueryRow(`SELECT title,body,tags,content,content_text,deleted_at FROM notes
		WHERE workspace_id=? AND id=?`, workspaceID, noteID).
		Scan(&title, &body, &tags, &content, &contentText, &deletedAt); err != nil {
		t.Fatal(err)
	}
	if title != "" || body != "" || tags != "[]" || content != "" || contentText != "" || !deletedAt.Valid {
		t.Fatalf("redacted note = title:%q body:%q tags:%q content:%q text:%q deleted:%v",
			title, body, tags, content, contentText, deletedAt.Valid)
	}
	var tombstoneCount, cleanupCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM mobile_v2_content_tombstones
		WHERE workspace_id=? AND entity_type='note' AND entity_id=? AND client_id=? AND revision=2`,
		workspaceID, noteID, noteClientID).Scan(&tombstoneCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM voice_audio_cleanup_jobs
		WHERE workspace_id=? AND voice_note_id=? AND object_key='attachments/private.mov' AND state='queued'`,
		workspaceID, storage.NoteAttachmentCleanupSubject("attachment-1")).Scan(&cleanupCount); err != nil {
		t.Fatal(err)
	}
	if tombstoneCount != 1 || cleanupCount != 1 {
		t.Fatalf("tombstones=%d cleanup_jobs=%d", tombstoneCount, cleanupCount)
	}
	var deleteImages string
	if err := db.QueryRow(`SELECT entities_json FROM mobile_v2_scope_change_batches
		WHERE workspace_id=? AND scope='iphone-content' AND sequence=2`, workspaceID).Scan(&deleteImages); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(deleteImages, "private title") || strings.Contains(deleteImages, "private body") ||
		!strings.Contains(deleteImages, `"payload":null`) {
		t.Fatalf("delete after-image leaked content: %s", deleteImages)
	}
	for _, scope := range []string{"iphone-task-core", "iphone-occurrence-window", "watch-occurrence-window"} {
		var scopeImages string
		if err := db.QueryRow(`SELECT entities_json FROM mobile_v2_scope_change_batches
			WHERE workspace_id=? AND scope=? AND sequence=2`, workspaceID, scope).Scan(&scopeImages); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(scopeImages, `"note_id":null`) {
			t.Fatalf("scope %s did not publish detached note reference: %s", scope, scopeImages)
		}
	}

	if _, err := db.Exec(`DELETE FROM note_attachments WHERE workspace_id=? AND id='attachment-1'`, workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE voice_audio_cleanup_jobs SET state='completed',updated_at=?
		WHERE workspace_id=?`, now.Unix(), workspaceID); err != nil {
		t.Fatal(err)
	}
	now = now.Add(25 * time.Hour)
	worker, err := NewContentRetentionWorker(ContentRetentionWorkerConfig{
		Workspaces:              retentionWorkspaceListerStub{workspaceIDs: []string{workspaceID}},
		Runtime:                 resolver,
		DeletedContentRetention: 24 * time.Hour,
		Now:                     func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	var noteCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM notes WHERE workspace_id=? AND id=?`, workspaceID, noteID).Scan(&noteCount); err != nil {
		t.Fatal(err)
	}
	if noteCount != 0 {
		t.Fatalf("expired redacted note count = %d", noteCount)
	}
	entities, err := mobilev2projection.Project(ctx, db, mobilev2projection.DialectSQLite, mobilev2projection.Projection{
		WorkspaceID: workspaceID, Scope: mobilev2sync.ScopeIPhoneContent, AsOf: now, Sequence: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	foundTombstone := false
	for _, entity := range entities {
		if strings.Contains(string(entity), `"entity_id":"`+noteID+`"`) {
			foundTombstone = strings.Contains(string(entity), `"payload":null`) && !strings.Contains(string(entity), "private")
		}
	}
	if !foundTombstone {
		t.Fatalf("purged note tombstone missing from projection: %s", entities)
	}

	lateCreate := signedCommand(t, `{
		"command_id":"71000000-0000-4000-8000-000000000007",
		"request_digest":"DIGEST",
		"origin_device_client_id":"71000000-0000-4000-8000-000000000003",
		"workspace_id":"workspace-content-retention",
		"command_type":"note.create",
		"target":{"entity_id":null,"client_id":"71000000-0000-4000-8000-000000000001"},
		"created_runtime_epoch":"1",
		"expected":{},
		"depends_on_command_id":null,
		"supersedes_command_id":null,
		"payload":{"title":"must not resurrect"}
	}`)
	lateValue, err := executor.ApplyCommand(ctx, handler.MobileV2CommandRequest{Identity: identity, RawEnvelope: lateCreate})
	if err != nil {
		t.Fatal(err)
	}
	if receipt := commandResponseWire(t, lateValue).Receipt; receipt.Status != mobilev2command.StatusConflict {
		t.Fatalf("late create receipt = %#v", receipt)
	}
}

func TestContentCommandExecutorResolvesDependencyReceiptIdentityAndRevision(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tenant-dependencies.db")
	cfg := storage.Config{Env: "test", Driver: storage.DriverSQLite, SQLitePath: path}
	if err := (storagesqlite.Provider{}).MigrateTenant(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const workspaceID = "workspace-dependencies"
	if _, err := db.Exec(`INSERT INTO tenant_workspaces(workspace_id,epoch,state) VALUES(?,1,'active')`, workspaceID); err != nil {
		t.Fatal(err)
	}
	executor, err := NewCommandExecutor(CommandExecutorConfig{
		Runtime: mobileV2RuntimeResolverFake{snapshot: taskruntime.MobileRuntimeSnapshot{
			WorkspaceID: workspaceID, Epoch: 1, Driver: storage.DriverSQLite, DB: db,
			Writer: storagesqlite.NewTenantWriter(cfg),
		}},
		Now: func() time.Time { return time.Date(2026, 7, 29, 11, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	identity := handler.MobileV2Identity{WorkspaceID: workspaceID, UserID: "user-dependencies"}
	const (
		noteClientID   = "50000000-0000-4000-8000-000000000001"
		deviceClientID = "60000000-0000-4000-8000-000000000001"
	)
	create := signedCommand(t, `{
		"command_id":"40000000-0000-4000-8000-000000000001",
		"request_digest":"DIGEST",
		"origin_device_client_id":"60000000-0000-4000-8000-000000000001",
		"workspace_id":"workspace-dependencies",
		"command_type":"note.create",
		"target":{"entity_id":null,"client_id":"50000000-0000-4000-8000-000000000001"},
		"created_runtime_epoch":"1",
		"expected":{},
		"depends_on_command_id":null,
		"supersedes_command_id":null,
		"payload":{"title":"Offline note","body":"before"}
	}`)
	if _, err := executor.ApplyCommand(ctx, handler.MobileV2CommandRequest{Identity: identity, RawEnvelope: create}); err != nil {
		t.Fatal(err)
	}
	update := signedCommand(t, `{
		"command_id":"40000000-0000-4000-8000-000000000002",
		"request_digest":"DIGEST",
		"origin_device_client_id":"60000000-0000-4000-8000-000000000001",
		"workspace_id":"workspace-dependencies",
		"command_type":"note.update",
		"target":{"entity_id":null,"client_id":"50000000-0000-4000-8000-000000000001"},
		"created_runtime_epoch":"1",
		"expected":{"entity_revision":{"source":"from_dependency_receipt","value":null,"dependency_command_id":"40000000-0000-4000-8000-000000000001"}},
		"depends_on_command_id":"40000000-0000-4000-8000-000000000001",
		"supersedes_command_id":null,
		"payload":{"body":"after"}
	}`)
	value, err := executor.ApplyCommand(ctx, handler.MobileV2CommandRequest{Identity: identity, RawEnvelope: update})
	if err != nil {
		t.Fatal(err)
	}
	receipt := commandResponseWire(t, value).Receipt
	if receipt.Status != mobilev2command.StatusApplied || receipt.CommitSequence != 2 ||
		len(receipt.AffectedRevisions) != 1 || receipt.AffectedRevisions[0].Revision != "2" {
		t.Fatalf("dependency update receipt = %#v", receipt)
	}
	var noteID, body string
	if err := db.QueryRow(`SELECT id,body FROM notes WHERE workspace_id=? AND client_id=?`,
		workspaceID, noteClientID).Scan(&noteID, &body); err != nil {
		t.Fatal(err)
	}
	if noteID == "" || body != "after" {
		t.Fatalf("resolved note id=%q body=%q", noteID, body)
	}

	missing := signedCommand(t, `{
		"command_id":"40000000-0000-4000-8000-000000000003",
		"request_digest":"DIGEST",
		"origin_device_client_id":"60000000-0000-4000-8000-000000000001",
		"workspace_id":"workspace-dependencies",
		"command_type":"note.update",
		"target":{"entity_id":null,"client_id":"50000000-0000-4000-8000-000000000001"},
		"created_runtime_epoch":"1",
		"expected":{"entity_revision":{"source":"from_dependency_receipt","value":null,"dependency_command_id":"70000000-0000-4000-8000-000000000001"}},
		"depends_on_command_id":"70000000-0000-4000-8000-000000000001",
		"supersedes_command_id":null,
		"payload":{"body":"must not apply"}
	}`)
	_, err = executor.ApplyCommand(ctx, handler.MobileV2CommandRequest{Identity: identity, RawEnvelope: missing})
	var protocolError *handler.MobileV2ProtocolError
	if !errors.As(err, &protocolError) || protocolError.Code != "receipt_history_ambiguous" {
		t.Fatalf("missing dependency error = %#v", err)
	}
	var receiptCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM mobile_v2_command_receipts
		WHERE workspace_id=? AND origin_device_client_id=?`, workspaceID, deviceClientID).
		Scan(&receiptCount); err != nil {
		t.Fatal(err)
	}
	if receiptCount != 2 {
		t.Fatalf("terminal receipt count after pending dependency = %d", receiptCount)
	}
}

type commandResponseTestWire struct {
	SchemaVersion string                  `json:"schema_version"`
	Replayed      bool                    `json:"replayed"`
	Receipt       mobilev2command.Receipt `json:"receipt"`
}

func commandResponseWire(t *testing.T, value any) commandResponseTestWire {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var result commandResponseTestWire
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func signedCommand(t *testing.T, template string) json.RawMessage {
	t.Helper()
	raw := json.RawMessage(strings.Replace(template, `"DIGEST"`,
		`"sha256:0000000000000000000000000000000000000000000000000000000000000000"`, 1))
	digest, err := mobilev2contract.RequestDigest(raw)
	if err != nil {
		t.Fatal(err)
	}
	return json.RawMessage(strings.Replace(string(raw),
		"sha256:0000000000000000000000000000000000000000000000000000000000000000", digest, 1))
}

func createCommandID(raw json.RawMessage) string {
	var envelope struct {
		CommandID string `json:"command_id"`
	}
	_ = json.Unmarshal(raw, &envelope)
	return envelope.CommandID
}
