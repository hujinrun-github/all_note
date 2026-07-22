package taskmigration

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/taskdomain"
)

func TestReplayProjectionApplierUpdatesSingleTaskAndAllMappedTargets(t *testing.T) {
	db := openProjectionWriterSQLite(t, "alpha")
	writer, err := NewV2ProjectionWriter(DialectSQLite)
	if err != nil {
		t.Fatal(err)
	}
	writeProjectionTransaction(t, db, writer, projectionWriterInput("alpha"))
	setReplaySourceVersion(t, db, "alpha", ReplayEntityTask, "legacy-task-1", 6, false)

	event := replayTaskEvent(101, 6, "Changed by replay", "active", "2026-07-24")
	applyReplayProjection(t, db, "alpha", event)

	var title, description, lifecycle, projectID string
	var priority int
	if err := db.QueryRow(`SELECT title,description,lifecycle_status,priority,project_id
		FROM domain_tasks_v2 WHERE workspace_id='alpha' AND id='task-1'`).
		Scan(&title, &description, &lifecycle, &priority, &projectID); err != nil {
		t.Fatal(err)
	}
	if title != "Changed by replay" || description != "new content" || lifecycle != "active" || priority != 3 || projectID != "project-1" {
		t.Fatalf("task=(%q,%q,%q,%d,%q)", title, description, lifecycle, priority, projectID)
	}
	var plannedDate, status string
	if err := db.QueryRow(`SELECT planned_date,execution_status FROM domain_task_occurrences_v2
		WHERE workspace_id='alpha' AND id='occurrence-1'`).Scan(&plannedDate, &status); err != nil {
		t.Fatal(err)
	}
	if plannedDate != "2026-07-24" || status != "active" {
		t.Fatalf("occurrence=(%q,%q), want (2026-07-24,active)", plannedDate, status)
	}
	var timing, startsOn string
	if err := db.QueryRow(`SELECT timing_type,starts_on FROM domain_task_schedule_versions_v2
		WHERE workspace_id='alpha' AND task_id='task-1'`).Scan(&timing, &startsOn); err != nil {
		t.Fatal(err)
	}
	if timing != "date" || startsOn != "2026-07-24" {
		t.Fatalf("schedule=(%q,%q)", timing, startsOn)
	}
	assertReplayMappingState(t, db, "alpha", ReplayEntityTask, "legacy-task-1", 6, false, 3)
}

func TestReplayProjectionApplierRejectsDifferentImageAtSameVersion(t *testing.T) {
	db := openProjectionWriterSQLite(t, "alpha")
	writer, _ := NewV2ProjectionWriter(DialectSQLite)
	writeProjectionTransaction(t, db, writer, projectionWriterInput("alpha"))

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	applier, err := NewReplayProjectionApplier("alpha", DialectSQLite)
	if err != nil {
		t.Fatal(err)
	}
	err = applier.Apply(context.Background(), tx, []ReplayEvent{
		replayTaskEvent(102, 5, "Different at version five", "open", "2026-07-22"),
	})
	_ = tx.Rollback()
	var conflict *ReplayProjectionConflictError
	if !errors.As(err, &conflict) || conflict.Code != ReplayProjectionConflictVersion {
		t.Fatalf("Apply error=%v, want %s", err, ReplayProjectionConflictVersion)
	}
	var title string
	if err := db.QueryRow(`SELECT title FROM domain_tasks_v2 WHERE workspace_id='alpha' AND id='task-1'`).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != "Write projection" {
		t.Fatalf("same-version conflict changed title to %q", title)
	}
}

func TestReplayProjectionApplierIgnoresOlderImage(t *testing.T) {
	db := openProjectionWriterSQLite(t, "alpha")
	writer, _ := NewV2ProjectionWriter(DialectSQLite)
	writeProjectionTransaction(t, db, writer, projectionWriterInput("alpha"))
	setReplaySourceVersion(t, db, "alpha", ReplayEntityTask, "legacy-task-1", 6, false)

	applyReplayProjection(t, db, "alpha", replayTaskEvent(103, 4, "stale", "open", "2026-07-22"))
	var title string
	if err := db.QueryRow(`SELECT title FROM domain_tasks_v2 WHERE workspace_id='alpha' AND id='task-1'`).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != "Write projection" {
		t.Fatalf("stale replay changed title to %q", title)
	}
	assertReplayMappingState(t, db, "alpha", ReplayEntityTask, "legacy-task-1", 5, false, 3)
}

func TestReplayProjectionApplierEventDeleteTombstonePreventsDelayedSnapshotRevival(t *testing.T) {
	db := openProjectionWriterSQLite(t, "alpha")
	writer, _ := NewV2ProjectionWriter(DialectSQLite)
	input := replayEventProjectionInput("alpha")
	writeProjectionTransaction(t, db, writer, input)
	setReplaySourceVersion(t, db, "alpha", ReplayEntityEvent, "legacy-event-1", 4, true)

	deleteEvent := ReplayEvent{
		Sequence: 201, EntityKind: ReplayEntityEvent, SourceID: "legacy-event-1",
		Operation: ReplayDelete, LogicalVersion: 4,
		TombstoneImage: replayEventImage("Legacy meeting"),
	}
	applyReplayProjection(t, db, "alpha", deleteEvent)
	for _, table := range []string{"domain_tasks_v2", "domain_task_schedules_v2", "domain_task_schedule_versions_v2", "domain_task_occurrences_v2"} {
		assertProjectionWriterCount(t, db, table, "alpha", 0)
	}
	assertReplayMappingState(t, db, "alpha", ReplayEntityEvent, "legacy-event-1", 4, true, 3)

	// A delayed snapshot/outbox image is older than the durable tombstone and
	// cannot recreate any of the event aggregate rows.
	upsert := ReplayEvent{
		Sequence: 202, EntityKind: ReplayEntityEvent, SourceID: "legacy-event-1",
		Operation: ReplayUpsert, LogicalVersion: 3, AfterImage: replayEventImage("stale"),
	}
	applyReplayProjection(t, db, "alpha", upsert)
	assertProjectionWriterCount(t, db, "domain_tasks_v2", "alpha", 0)
	assertReplayMappingState(t, db, "alpha", ReplayEntityEvent, "legacy-event-1", 4, true, 3)
}

func TestReplayProjectionApplierNormalizesDeleteDependencyOrder(t *testing.T) {
	db := openProjectionWriterSQLite(t, "alpha")
	writer, _ := NewV2ProjectionWriter(DialectSQLite)
	writeProjectionTransaction(t, db, writer, replayEventProjectionInput("alpha"))
	setReplaySourceVersion(t, db, "alpha", ReplayEntityProject, "legacy-project-1", 3, true)
	setReplaySourceVersion(t, db, "alpha", ReplayEntityEvent, "legacy-event-1", 4, true)

	// The globally ordered outbox page may present the parent first. The
	// applier must project deletes in reverse dependency order.
	applyReplayProjection(t, db, "alpha",
		ReplayEvent{Sequence: 301, EntityKind: ReplayEntityProject, SourceID: "legacy-project-1", Operation: ReplayDelete, LogicalVersion: 3,
			TombstoneImage: ReplayImage{"workspace_id": "alpha", "id": "legacy-project-1", "name": "Calendar", "type": "regular"}},
		ReplayEvent{Sequence: 302, EntityKind: ReplayEntityEvent, SourceID: "legacy-event-1", Operation: ReplayDelete, LogicalVersion: 4,
			TombstoneImage: replayEventImage("Legacy meeting")},
	)
	assertProjectionWriterCount(t, db, "domain_tasks_v2", "alpha", 0)
	assertProjectionWriterCount(t, db, "domain_projects_v2", "alpha", 0)
}

func TestReplayProjectionApplierFailsClosedOnMissingMapAndWorkspaceMismatch(t *testing.T) {
	tests := []struct {
		name      string
		workspace string
		event     ReplayEvent
		code      ReplayProjectionConflictCode
	}{
		{
			name: "missing parent dependency",
			event: ReplayEvent{Sequence: 401, EntityKind: ReplayEntityTask, SourceID: "new-task", Operation: ReplayUpsert, LogicalVersion: 1,
				AfterImage: ReplayImage{"workspace_id": "alpha", "id": "new-task", "title": "new", "content": "", "priority": "0", "sort_order": "0", "status": "todo", "done": "false", "execution_type": "single", "planned_date": "null", "due_at": "null", "completed_at": "null", "updated_at": "2026-07-22T00:00:00Z", "project_id": "null", "note_id": "null"}},
			code: ReplayProjectionConflictDependency,
		},
		{
			name:  "workspace mismatch",
			event: replayTaskEvent(402, 6, "wrong workspace", "open", "2026-07-24"),
			code:  ReplayProjectionConflictWorkspace,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openProjectionWriterSQLite(t, "alpha", "beta")
			writer, _ := NewV2ProjectionWriter(DialectSQLite)
			writeProjectionTransaction(t, db, writer, projectionWriterInput("alpha"))
			if test.name == "missing parent dependency" {
				if _, err := db.Exec(`INSERT INTO legacy_task_domain_entity_versions
					(workspace_id,entity_kind,entity_id,logical_version,deleted) VALUES('alpha','task','new-task',1,0)`); err != nil {
					t.Fatal(err)
				}
			} else {
				test.event.AfterImage["workspace_id"] = "beta"
				setReplaySourceVersion(t, db, "alpha", ReplayEntityTask, "legacy-task-1", 6, false)
			}
			tx, err := db.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			applier, _ := NewReplayProjectionApplier("alpha", DialectSQLite)
			err = applier.Apply(context.Background(), tx, []ReplayEvent{test.event})
			_ = tx.Rollback()
			var conflict *ReplayProjectionConflictError
			if !errors.As(err, &conflict) || conflict.Code != test.code {
				t.Fatalf("Apply error=%v, want %s", err, test.code)
			}
		})
	}
}

func TestReplayProjectionApplierUpdatesProjectRuleOccurrenceAndEventWithoutDroppingFields(t *testing.T) {
	db := openProjectionWriterSQLite(t, "alpha")
	writer, _ := NewV2ProjectionWriter(DialectSQLite)
	input := replayRecurringProjectionInput("alpha")
	writeProjectionTransaction(t, db, writer, input)

	setReplaySourceVersion(t, db, "alpha", ReplayEntityProject, "legacy-project-1", 3, false)
	setReplaySourceVersion(t, db, "alpha", ReplayEntityRule, "legacy-task-1", 3, false)
	setReplaySourceVersion(t, db, "alpha", ReplayEntityOccurrence, `["legacy-task-1","2026-07-23"]`, 8, false)
	applyReplayProjection(t, db, "alpha",
		ReplayEvent{Sequence: 501, EntityKind: ReplayEntityProject, SourceID: "legacy-project-1", Operation: ReplayUpsert, LogicalVersion: 3,
			AfterImage: ReplayImage{"workspace_id": "alpha", "id": "legacy-project-1", "name": "Renamed", "type": "learning"}},
		ReplayEvent{Sequence: 502, EntityKind: ReplayEntityRule, SourceID: "legacy-task-1", Operation: ReplayUpsert, LogicalVersion: 3,
			AfterImage: ReplayImage{"workspace_id": "alpha", "task_id": "legacy-task-1", "frequency": "weekly", "interval": "2", "weekdays": "[1,3]", "month_days": "[]", "start_date": "2026-07-23", "end_date": "2026-08-31", "timezone": "Asia/Shanghai", "enabled": "true", "updated_at": "2026-07-22T01:00:00Z"}},
		ReplayEvent{Sequence: 503, EntityKind: ReplayEntityOccurrence, SourceID: `["legacy-task-1","2026-07-23"]`, Operation: ReplayUpsert, LogicalVersion: 8,
			AfterImage: ReplayImage{"workspace_id": "alpha", "task_id": "legacy-task-1", "occurrence_date": "2026-07-23", "status": "done", "completed_at": "2026-07-23T02:00:00Z", "updated_at": "2026-07-23T02:00:00Z", "note": "kept note"}},
	)

	var name, kind, horizon string
	if err := db.QueryRow(`SELECT name,kind,horizon FROM domain_projects_v2 WHERE workspace_id='alpha' AND id='project-1'`).Scan(&name, &kind, &horizon); err != nil {
		t.Fatal(err)
	}
	if name != "Renamed" || kind != "learning" || horizon != "long" {
		t.Fatalf("project=(%q,%q,%q)", name, kind, horizon)
	}
	var recurrence, rule string
	if err := db.QueryRow(`SELECT recurrence_type,recurrence_rule FROM domain_task_schedule_versions_v2
		WHERE workspace_id='alpha' AND task_id='task-1'`).Scan(&recurrence, &rule); err != nil {
		t.Fatal(err)
	}
	if recurrence != "weekly" || rule != `{"interval":2,"weekdays":[1,3]}` {
		t.Fatalf("schedule=(%q,%s)", recurrence, rule)
	}
	var status, completedAt, notes string
	if err := db.QueryRow(`SELECT execution_status,completed_at,calendar_notes FROM domain_task_occurrences_v2
		WHERE workspace_id='alpha' AND id='occurrence-1'`).Scan(&status, &completedAt, &notes); err != nil {
		t.Fatal(err)
	}
	if status != "done" || completedAt == "" || notes != "kept note" {
		t.Fatalf("occurrence=(%q,%q,%q)", status, completedAt, notes)
	}
}

func TestReplayProjectionApplierCreatesPostSnapshotProjectAndSingleTask(t *testing.T) {
	db := openProjectionWriterSQLite(t, "alpha")
	insertReplaySourceVersion(t, db, "alpha", ReplayEntityProject, "late-project", 1, false)
	insertReplaySourceVersion(t, db, "alpha", ReplayEntityTask, "late-task", 1, false)

	project := ReplayEvent{Sequence: 601, EntityKind: ReplayEntityProject, SourceID: "late-project", Operation: ReplayUpsert, LogicalVersion: 1,
		AfterImage: ReplayImage{"workspace_id": "alpha", "id": "late-project", "name": "Late project", "type": "regular", "updated_at": "2026-07-22T02:00:00Z"}}
	task := replayTaskEvent(602, 1, "Late task", "open", "2026-07-26")
	task.SourceID = "late-task"
	task.AfterImage["id"] = "late-task"
	task.AfterImage["project_id"] = "late-project"
	applyReplayProjection(t, db, "alpha", project, task)

	assertProjectionWriterCount(t, db, "domain_projects_v2", "alpha", 1)
	assertProjectionWriterCount(t, db, "domain_tasks_v2", "alpha", 1)
	assertProjectionWriterCount(t, db, "domain_task_schedules_v2", "alpha", 1)
	assertProjectionWriterCount(t, db, "domain_task_occurrences_v2", "alpha", 1)
	var occurrenceID string
	if err := db.QueryRow(`SELECT id FROM domain_task_occurrences_v2 WHERE workspace_id='alpha' AND task_id='late-task'`).Scan(&occurrenceID); err != nil {
		t.Fatal(err)
	}
	if want := deterministicProjectionID("task-occurrence", "late-task", "once"); occurrenceID != want {
		t.Fatalf("occurrence id=%q, want %q", occurrenceID, want)
	}
	assertReplayMappingState(t, db, "alpha", ReplayEntityTask, "late-task", 1, false, 3)
}

func TestReplayProjectionApplierCreatesPostSnapshotRecurringAggregate(t *testing.T) {
	db := openProjectionWriterSQLite(t, "alpha")
	writer, _ := NewV2ProjectionWriter(DialectSQLite)
	writeProjectionTransaction(t, db, writer, replayEventProjectionInput("alpha"))
	insertReplaySourceVersion(t, db, "alpha", ReplayEntityTask, "late-repeat", 1, false)
	insertReplaySourceVersion(t, db, "alpha", ReplayEntityRule, "late-repeat", 1, false)
	occurrenceSource := `["late-repeat","2026-07-24"]`
	insertReplaySourceVersion(t, db, "alpha", ReplayEntityOccurrence, occurrenceSource, 1, false)

	task := replayTaskEvent(701, 1, "Late repeat", "open", "null")
	task.SourceID = "late-repeat"
	task.AfterImage["id"] = "late-repeat"
	task.AfterImage["execution_type"] = "recurring"
	task.AfterImage["project_id"] = "legacy-project-1"
	rule := ReplayEvent{Sequence: 702, EntityKind: ReplayEntityRule, SourceID: "late-repeat", Operation: ReplayUpsert, LogicalVersion: 1,
		AfterImage: ReplayImage{"workspace_id": "alpha", "task_id": "late-repeat", "frequency": "weekly", "interval": "1", "weekdays": "[1,3]", "month_days": "[]", "start_date": "2026-07-24", "end_date": "2026-08-31", "timezone": "Asia/Shanghai", "enabled": "true", "updated_at": "2026-07-22T02:00:00Z"}}
	occurrence := ReplayEvent{Sequence: 703, EntityKind: ReplayEntityOccurrence, SourceID: occurrenceSource, Operation: ReplayUpsert, LogicalVersion: 1,
		AfterImage: ReplayImage{"workspace_id": "alpha", "task_id": "late-repeat", "occurrence_date": "2026-07-24", "status": "open", "completed_at": "null", "updated_at": "2026-07-22T02:00:00Z", "note": "first"}}
	applyReplayProjection(t, db, "alpha", task, rule, occurrence)

	var recurrence string
	if err := db.QueryRow(`SELECT recurrence_type FROM domain_task_schedule_versions_v2 WHERE workspace_id='alpha' AND task_id='late-repeat'`).Scan(&recurrence); err != nil {
		t.Fatal(err)
	}
	if recurrence != "weekly" {
		t.Fatalf("recurrence=%q", recurrence)
	}
	var occurrenceID string
	if err := db.QueryRow(`SELECT id FROM domain_task_occurrences_v2 WHERE workspace_id='alpha' AND task_id='late-repeat'`).Scan(&occurrenceID); err != nil {
		t.Fatal(err)
	}
	if want := deterministicProjectionID("task-occurrence", "late-repeat", "2026-07-24"); occurrenceID != want {
		t.Fatalf("occurrence id=%q, want %q", occurrenceID, want)
	}
}

func TestReplayProjectionApplierCreatesPostSnapshotEvent(t *testing.T) {
	db := openProjectionWriterSQLite(t, "alpha")
	writer, _ := NewV2ProjectionWriter(DialectSQLite)
	input := projectionWriterInput("alpha")
	writeProjectionTransaction(t, db, writer, input)
	insertReplaySourceVersion(t, db, "alpha", ReplayEntityEvent, "late-event", 1, false)
	image := replayEventImage("Late event")
	image["id"] = "late-event"
	event := ReplayEvent{Sequence: 801, EntityKind: ReplayEntityEvent, SourceID: "late-event", Operation: ReplayUpsert, LogicalVersion: 1, AfterImage: image}
	applyReplayProjection(t, db, "alpha", event)

	var taskID, occurrenceID string
	if err := db.QueryRow(`SELECT v2_id FROM task_domain_legacy_id_map WHERE workspace_id='alpha' AND entity_kind='event' AND legacy_id='late-event' AND target_kind='task'`).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT v2_id FROM task_domain_legacy_id_map WHERE workspace_id='alpha' AND entity_kind='event' AND legacy_id='late-event' AND target_kind='occurrence'`).Scan(&occurrenceID); err != nil {
		t.Fatal(err)
	}
	if taskID != deterministicProjectionID("event-task", "late-event") || occurrenceID != deterministicProjectionID("event-occurrence", "late-event") {
		t.Fatalf("event targets=(%q,%q)", taskID, occurrenceID)
	}
}

func TestReplayProjectionApplierRecordsCreateDeleteAfterSnapshotAsTombstoneOnly(t *testing.T) {
	db := openProjectionWriterSQLite(t, "alpha")
	insertReplaySourceVersion(t, db, "alpha", ReplayEntityTask, "transient-task", 2, true)
	upsert := replayTaskEvent(901, 1, "Transient", "open", "2026-07-26")
	upsert.SourceID = "transient-task"
	upsert.AfterImage["id"] = "transient-task"
	deleted := ReplayEvent{
		Sequence: 902, EntityKind: ReplayEntityTask, SourceID: "transient-task", Operation: ReplayDelete, LogicalVersion: 2,
		TombstoneImage: cloneReplayImage(upsert.AfterImage),
	}
	applyReplayProjection(t, db, "alpha", upsert, deleted)

	assertProjectionWriterCount(t, db, "domain_tasks_v2", "alpha", 0)
	assertReplayMappingState(t, db, "alpha", ReplayEntityTask, "transient-task", 2, true, 3)
}

func TestReplayProjectionApplierRequiresDurableSourceLedgerAtOrBeyondEvent(t *testing.T) {
	db := openProjectionWriterSQLite(t, "alpha")
	writer, _ := NewV2ProjectionWriter(DialectSQLite)
	writeProjectionTransaction(t, db, writer, projectionWriterInput("alpha"))
	// The ID map and source ledger both remain at five. A forged/future event
	// cannot move projection state ahead of the trigger-owned durable ledger.
	event := replayTaskEvent(1001, 6, "future", "open", "2026-07-27")
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	applier, _ := NewReplayProjectionApplier("alpha", DialectSQLite)
	err = applier.Apply(context.Background(), tx, []ReplayEvent{event})
	_ = tx.Rollback()
	var conflict *ReplayProjectionConflictError
	if !errors.As(err, &conflict) || conflict.Code != ReplayProjectionConflictSourceLedger {
		t.Fatalf("Apply error=%v, want %s", err, ReplayProjectionConflictSourceLedger)
	}
	assertReplayMappingState(t, db, "alpha", ReplayEntityTask, "legacy-task-1", 5, false, 3)
}

func TestReplayProjectionApplierRejectsSplitMappingVersionBeforeCAS(t *testing.T) {
	db := openProjectionWriterSQLite(t, "alpha")
	writer, _ := NewV2ProjectionWriter(DialectSQLite)
	writeProjectionTransaction(t, db, writer, projectionWriterInput("alpha"))
	setReplaySourceVersion(t, db, "alpha", ReplayEntityTask, "legacy-task-1", 6, false)
	if _, err := db.Exec(`UPDATE task_domain_legacy_id_map SET source_logical_version=4
		WHERE workspace_id='alpha' AND entity_kind='task' AND legacy_id='legacy-task-1' AND target_kind='schedule'`); err != nil {
		t.Fatal(err)
	}

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	applier, _ := NewReplayProjectionApplier("alpha", DialectSQLite)
	err = applier.Apply(context.Background(), tx, []ReplayEvent{replayTaskEvent(1002, 6, "must not split", "open", "2026-07-27")})
	_ = tx.Rollback()
	var conflict *ReplayProjectionConflictError
	if !errors.As(err, &conflict) || conflict.Code != ReplayProjectionConflictMapping {
		t.Fatalf("Apply error=%v, want %s", err, ReplayProjectionConflictMapping)
	}
	var title string
	if err := db.QueryRow(`SELECT title FROM domain_tasks_v2 WHERE workspace_id='alpha' AND id='task-1'`).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != "Write projection" {
		t.Fatalf("split mapping changed target to %q", title)
	}
}

func applyReplayProjection(t *testing.T, db *sql.DB, workspaceID string, events ...ReplayEvent) {
	t.Helper()
	applier, err := NewReplayProjectionApplier(workspaceID, DialectSQLite)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := applier.Apply(context.Background(), tx, events); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func replayTaskEvent(sequence, version int64, title, status, plannedDate string) ReplayEvent {
	return ReplayEvent{
		Sequence: sequence, EntityKind: ReplayEntityTask, SourceID: "legacy-task-1", Operation: ReplayUpsert, LogicalVersion: version,
		AfterImage: ReplayImage{
			"workspace_id": "alpha", "id": "legacy-task-1", "project_id": "legacy-project-1", "note_id": "null",
			"title": title, "content": "new content", "priority": "3", "sort_order": "4", "status": status,
			"done": "false", "execution_type": "single", "planned_date": plannedDate, "due_at": "2026-07-25T00:00:00Z",
			"completed_at": "null", "updated_at": "2026-07-22T01:00:00Z", "horizon": "week", "scope": "daily", "roadmap_node_id": "null",
		},
	}
}

func replayEventImage(title string) ReplayImage {
	return ReplayImage{
		"workspace_id": "alpha", "id": "legacy-event-1", "project_id": "legacy-project-1", "title": title,
		"start_time": "2026-07-22T01:30:00Z", "end_time": "2026-07-22T03:00:00Z", "is_all_day": "false",
		"location": "Room 1", "kind": "meeting", "notes": "agenda", "note_id": "null", "updated_at": "2026-07-22T00:00:00Z",
	}
}

func replayEventProjectionInput(workspaceID string) V2ProjectionWrite {
	start := time.Date(2026, 7, 22, 1, 30, 0, 0, time.UTC)
	end := start.Add(90 * time.Minute)
	return V2ProjectionWrite{
		WorkspaceID: workspaceID, WrittenAt: time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC),
		SourceVersions: []ProjectionSourceVersion{
			{EntityKind: LegacyEntityProject, LegacyID: "legacy-project-1", LogicalVersion: 2},
			{EntityKind: LegacyEntityEvent, LegacyID: "legacy-event-1", LogicalVersion: 3},
		},
		Projection: V2Projection{
			Projects:  []V2ProjectProjection{{ID: "project-1", Name: "Calendar", Kind: "standard", Horizon: "long"}},
			Tasks:     []V2TaskProjection{{ID: "event-task-1", ProjectID: "project-1", Title: "Legacy meeting", Description: "must survive", LifecycleStatus: taskdomain.TaskLifecycleActive}},
			Schedules: []V2ScheduleProjection{{TaskID: "event-task-1", RecurrenceType: taskdomain.RecurrenceNone, TimingType: taskdomain.TimingTimeBlock, Timezone: "Asia/Shanghai", StartsOn: "2026-07-22", Interval: 1}},
			Occurrences: []V2OccurrenceProjection{{ID: "event-occurrence-1", TaskID: "event-task-1", OccurrenceKey: "once", ExecutionStatus: taskdomain.ExecutionStatusOpen,
				PlannedDate: "2026-07-22", PlannedStartAt: &start, PlannedEndAt: &end, Location: "Room 1", CalendarKind: "meeting", CalendarNotes: "agenda", GeneratedScheduleRevision: 1}},
			IDMap: []V2IDMapEntry{
				{LegacyKind: LegacyEntityProject, LegacyID: "legacy-project-1", TargetProjectID: "project-1"},
				{LegacyKind: LegacyEntityEvent, LegacyID: "legacy-event-1", TargetTaskID: "event-task-1", TargetScheduleID: "event-task-1", TargetOccurrenceID: "event-occurrence-1"},
			},
		},
	}
}

func replayRecurringProjectionInput(workspaceID string) V2ProjectionWrite {
	return V2ProjectionWrite{
		WorkspaceID: workspaceID, WrittenAt: time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC),
		SourceVersions: []ProjectionSourceVersion{
			{EntityKind: LegacyEntityProject, LegacyID: "legacy-project-1", LogicalVersion: 2},
			{EntityKind: LegacyEntityTask, LegacyID: "legacy-task-1", LogicalVersion: 5},
			{EntityKind: LegacyEntityRule, LegacyID: "legacy-task-1", LogicalVersion: 2},
			{EntityKind: LegacyEntityOccurrence, LegacyID: `["legacy-task-1","2026-07-23"]`, LogicalVersion: 7},
		},
		Projection: V2Projection{
			Projects:    []V2ProjectProjection{{ID: "project-1", Name: "Repeat", Kind: "standard", Horizon: "long"}},
			Tasks:       []V2TaskProjection{{ID: "task-1", ProjectID: "project-1", Title: "Practice", Description: "description", LifecycleStatus: taskdomain.TaskLifecycleActive}},
			Schedules:   []V2ScheduleProjection{{TaskID: "task-1", RecurrenceType: taskdomain.RecurrenceDaily, TimingType: taskdomain.TimingDate, Timezone: "Asia/Shanghai", StartsOn: "2026-07-22", EndsOn: "2026-08-31", Interval: 1}},
			Occurrences: []V2OccurrenceProjection{{ID: "occurrence-1", TaskID: "task-1", OccurrenceKey: "2026-07-23", PlannedDate: "2026-07-23", ExecutionStatus: taskdomain.ExecutionStatusOpen, GeneratedScheduleRevision: 1}},
			IDMap: []V2IDMapEntry{
				{LegacyKind: LegacyEntityProject, LegacyID: "legacy-project-1", TargetProjectID: "project-1"},
				{LegacyKind: LegacyEntityTask, LegacyID: "legacy-task-1", TargetTaskID: "task-1"},
				{LegacyKind: LegacyEntityRule, LegacyID: "legacy-task-1", TargetScheduleID: "task-1"},
				{LegacyKind: LegacyEntityOccurrence, LegacyID: `["legacy-task-1","2026-07-23"]`, TargetOccurrenceID: "occurrence-1"},
			},
		},
	}
}

func setReplaySourceVersion(t *testing.T, db *sql.DB, workspaceID string, kind ReplayEntityKind, sourceID string, version int64, deleted bool) {
	t.Helper()
	if _, err := db.Exec(`UPDATE legacy_task_domain_entity_versions SET logical_version=?,deleted=?
		WHERE workspace_id=? AND entity_kind=? AND entity_id=?`, version, deleted, workspaceID, kind, sourceID); err != nil {
		t.Fatal(err)
	}
}

func insertReplaySourceVersion(t *testing.T, db *sql.DB, workspaceID string, kind ReplayEntityKind, sourceID string, version int64, deleted bool) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO legacy_task_domain_entity_versions
		(workspace_id,entity_kind,entity_id,logical_version,deleted) VALUES(?,?,?,?,?)`, workspaceID, kind, sourceID, version, deleted); err != nil {
		t.Fatal(err)
	}
}

func assertReplayMappingState(t *testing.T, db *sql.DB, workspaceID string, kind ReplayEntityKind, sourceID string, version int64, deleted bool, wantRows int) {
	t.Helper()
	rows, err := db.Query(`SELECT source_logical_version,deleted FROM task_domain_legacy_id_map
		WHERE workspace_id=? AND entity_kind=? AND legacy_id=?`, workspaceID, kind, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var gotVersion int64
		var gotDeleted bool
		if err := rows.Scan(&gotVersion, &gotDeleted); err != nil {
			t.Fatal(err)
		}
		if gotVersion != version || gotDeleted != deleted {
			t.Fatalf("mapping=(%d,%t), want (%d,%t)", gotVersion, gotDeleted, version, deleted)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if count != wantRows {
		t.Fatalf("mapping rows=%d, want %d", count, wantRows)
	}
}
