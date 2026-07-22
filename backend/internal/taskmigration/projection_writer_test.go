package taskmigration

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/taskdomain"
	_ "modernc.org/sqlite"
)

var projectionWriterSQLiteSequence atomic.Uint64

func TestV2ProjectionWriterPersistsCompleteProjectionAndRerunsIdempotently(t *testing.T) {
	db := openProjectionWriterSQLite(t, "alpha", "beta")
	writer, err := NewV2ProjectionWriter(DialectSQLite)
	if err != nil {
		t.Fatalf("NewV2ProjectionWriter: %v", err)
	}
	input := projectionWriterInput("alpha")

	writeProjectionTransaction(t, db, writer, input)
	// A retry may use a new coordinator clock value. Persistence identity is
	// the source logical version and exact projected data, not created_at.
	input.WrittenAt = input.WrittenAt.Add(time.Hour)
	writeProjectionTransaction(t, db, writer, input)

	assertProjectionWriterCount(t, db, "domain_projects_v2", "alpha", 1)
	assertProjectionWriterCount(t, db, "domain_tasks_v2", "alpha", 1)
	assertProjectionWriterCount(t, db, "domain_task_schedules_v2", "alpha", 1)
	assertProjectionWriterCount(t, db, "domain_task_schedule_versions_v2", "alpha", 1)
	assertProjectionWriterCount(t, db, "domain_task_occurrences_v2", "alpha", 1)
	assertProjectionWriterCount(t, db, "task_domain_legacy_id_map", "alpha", 4)

	var recurrenceRule, title, plannedDate string
	if err := db.QueryRow(`SELECT recurrence_rule FROM domain_task_schedule_versions_v2
		WHERE workspace_id=? AND task_id=? AND schedule_revision=1`, "alpha", "task-1").Scan(&recurrenceRule); err != nil {
		t.Fatalf("read schedule version: %v", err)
	}
	if recurrenceRule != `{}` {
		t.Fatalf("recurrence_rule=%s, want {}", recurrenceRule)
	}
	if err := db.QueryRow(`SELECT title FROM domain_tasks_v2 WHERE workspace_id=? AND id=?`, "alpha", "task-1").Scan(&title); err != nil {
		t.Fatalf("read task: %v", err)
	}
	if title != "Write projection" {
		t.Fatalf("title=%q", title)
	}
	if err := db.QueryRow(`SELECT planned_date FROM domain_task_occurrences_v2 WHERE workspace_id=? AND id=?`, "alpha", "occurrence-1").Scan(&plannedDate); err != nil {
		t.Fatalf("read occurrence: %v", err)
	}
	if plannedDate != "2026-07-22" {
		t.Fatalf("planned_date=%q", plannedDate)
	}

	var taskVersion, projectVersion int64
	if err := db.QueryRow(`SELECT source_logical_version FROM task_domain_legacy_id_map
		WHERE workspace_id=? AND entity_kind='task' AND legacy_id='legacy-task-1' AND target_kind='task'`, "alpha").Scan(&taskVersion); err != nil {
		t.Fatalf("read task ID map: %v", err)
	}
	if err := db.QueryRow(`SELECT source_logical_version FROM task_domain_legacy_id_map
		WHERE workspace_id=? AND entity_kind='project' AND legacy_id='legacy-project-1' AND target_kind='project'`, "alpha").Scan(&projectVersion); err != nil {
		t.Fatalf("read project ID map: %v", err)
	}
	if taskVersion != 5 || projectVersion != 2 {
		t.Fatalf("source versions=(task:%d project:%d), want (5,2)", taskVersion, projectVersion)
	}
}

func TestV2ProjectionWriterKeepsWorkspaceIdentityComposite(t *testing.T) {
	db := openProjectionWriterSQLite(t, "alpha", "beta")
	writer, err := NewV2ProjectionWriter(DialectSQLite)
	if err != nil {
		t.Fatal(err)
	}
	writeProjectionTransaction(t, db, writer, projectionWriterInput("alpha"))
	writeProjectionTransaction(t, db, writer, projectionWriterInput("beta"))

	for _, table := range []string{"domain_projects_v2", "domain_tasks_v2", "domain_task_occurrences_v2"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 2 {
			t.Fatalf("%s rows=%d, want 2", table, count)
		}
	}
}

func TestV2ProjectionWriterPersistsTimeBlockAndRecurringScheduleSemantics(t *testing.T) {
	db := openProjectionWriterSQLite(t, "alpha")
	writer, err := NewV2ProjectionWriter(DialectSQLite)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 7, 22, 1, 30, 0, 0, time.UTC) // 09:30 Asia/Shanghai.
	end := start.Add(90 * time.Minute)
	input := V2ProjectionWrite{
		WorkspaceID: "alpha",
		WrittenAt:   time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC),
		SourceVersions: []ProjectionSourceVersion{
			{EntityKind: LegacyEntityProject, LegacyID: "project-source", LogicalVersion: 1},
			{EntityKind: LegacyEntityEvent, LegacyID: "event-source", LogicalVersion: 3},
			{EntityKind: LegacyEntityTask, LegacyID: "task-source", LogicalVersion: 4},
			{EntityKind: LegacyEntityRule, LegacyID: "rule-source", LogicalVersion: 2},
			{EntityKind: LegacyEntityOccurrence, LegacyID: "occurrence-source", LogicalVersion: 7},
		},
		Projection: V2Projection{
			Projects: []V2ProjectProjection{{ID: "project-1", Name: "Mixed", Kind: "standard", Horizon: "short"}},
			Tasks: []V2TaskProjection{
				{ID: "event-task", ProjectID: "project-1", Title: "Meeting", LifecycleStatus: taskdomain.TaskLifecycleActive},
				{ID: "repeat-task", ProjectID: "project-1", Title: "Practice", LifecycleStatus: taskdomain.TaskLifecycleActive},
			},
			Schedules: []V2ScheduleProjection{
				{TaskID: "event-task", RecurrenceType: taskdomain.RecurrenceNone, TimingType: taskdomain.TimingTimeBlock, Timezone: "Asia/Shanghai", StartsOn: "2026-07-22", Interval: 1},
				{TaskID: "repeat-task", RecurrenceType: taskdomain.RecurrenceWeekly, TimingType: taskdomain.TimingDate, Timezone: "Asia/Shanghai", StartsOn: "2026-07-22", EndsOn: "2026-08-31", Interval: 2, Weekdays: []int{1, 3}},
			},
			Occurrences: []V2OccurrenceProjection{
				{ID: "event-occurrence", TaskID: "event-task", OccurrenceKey: "once", PlannedDate: "2026-07-22", PlannedStartAt: &start, PlannedEndAt: &end, ExecutionStatus: taskdomain.ExecutionStatusOpen, GeneratedScheduleRevision: 1},
				{ID: "repeat-occurrence", TaskID: "repeat-task", OccurrenceKey: "2026-07-23", PlannedDate: "2026-07-23", ExecutionStatus: taskdomain.ExecutionStatusOpen, GeneratedScheduleRevision: 1},
			},
			IDMap: []V2IDMapEntry{
				{LegacyKind: LegacyEntityProject, LegacyID: "project-source", TargetProjectID: "project-1"},
				{LegacyKind: LegacyEntityEvent, LegacyID: "event-source", TargetTaskID: "event-task", TargetScheduleID: "event-task", TargetOccurrenceID: "event-occurrence"},
				{LegacyKind: LegacyEntityTask, LegacyID: "task-source", TargetTaskID: "repeat-task"},
				{LegacyKind: LegacyEntityRule, LegacyID: "rule-source", TargetScheduleID: "repeat-task"},
				{LegacyKind: LegacyEntityOccurrence, LegacyID: "occurrence-source", TargetOccurrenceID: "repeat-occurrence"},
			},
		},
	}
	writeProjectionTransaction(t, db, writer, input)

	var localStart string
	var duration int
	if err := db.QueryRow(`SELECT local_start_time,duration_minutes FROM domain_task_schedule_versions_v2
		WHERE workspace_id='alpha' AND task_id='event-task'`).Scan(&localStart, &duration); err != nil {
		t.Fatal(err)
	}
	if localStart != "09:30:00" || duration != 90 {
		t.Fatalf("derived time block=(%s,%d), want (09:30:00,90)", localStart, duration)
	}
	var rule string
	if err := db.QueryRow(`SELECT recurrence_rule FROM domain_task_schedule_versions_v2
		WHERE workspace_id='alpha' AND task_id='repeat-task'`).Scan(&rule); err != nil {
		t.Fatal(err)
	}
	if rule != `{"interval":2,"weekdays":[1,3]}` {
		t.Fatalf("weekly recurrence_rule=%s", rule)
	}
}

func TestV2ProjectionWriterFailsClosedOnMissingOrExtraSourceVersion(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*V2ProjectionWrite)
		code   ProjectionWriteConflictCode
	}{
		{
			name: "missing source version",
			mutate: func(input *V2ProjectionWrite) {
				input.SourceVersions = input.SourceVersions[:1]
			},
			code: ProjectionWriteConflictMissingSourceVersion,
		},
		{
			name: "extra source version",
			mutate: func(input *V2ProjectionWrite) {
				input.SourceVersions = append(input.SourceVersions, ProjectionSourceVersion{
					EntityKind: LegacyEntityEvent, LegacyID: "not-projected", LogicalVersion: 1,
				})
			},
			code: ProjectionWriteConflictUnmappedSourceVersion,
		},
		{
			name: "zero source version",
			mutate: func(input *V2ProjectionWrite) {
				input.SourceVersions[0].LogicalVersion = 0
			},
			code: ProjectionWriteConflictInvalidSourceVersion,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openProjectionWriterSQLite(t, "alpha")
			writer, err := NewV2ProjectionWriter(DialectSQLite)
			if err != nil {
				t.Fatal(err)
			}
			input := projectionWriterInput("alpha")
			test.mutate(&input)
			tx, err := db.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			err = writer.Write(context.Background(), tx, input)
			_ = tx.Rollback()
			var conflict *ProjectionWriteConflictError
			if !errors.As(err, &conflict) || conflict.Code != test.code {
				t.Fatalf("Write error=%v, want conflict %s", err, test.code)
			}
			assertProjectionWriterCount(t, db, "domain_projects_v2", "alpha", 0)
		})
	}
}

func TestV2ProjectionWriterRejectsDifferentProjectionOrVersion(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*V2ProjectionWrite)
		code   ProjectionWriteConflictCode
	}{
		{
			name: "same target with different content",
			mutate: func(input *V2ProjectionWrite) {
				input.Projection.Tasks[0].Title = "Silently replaced"
			},
			code: ProjectionWriteConflictTargetData,
		},
		{
			name: "same mapping with newer source version",
			mutate: func(input *V2ProjectionWrite) {
				input.SourceVersions[1].LogicalVersion++
			},
			code: ProjectionWriteConflictMappingVersion,
		},
		{
			name: "source remapped to another target",
			mutate: func(input *V2ProjectionWrite) {
				input.Projection.IDMap[1].TargetTaskID = "other-task"
			},
			code: ProjectionWriteConflictTargetData,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openProjectionWriterSQLite(t, "alpha")
			writer, err := NewV2ProjectionWriter(DialectSQLite)
			if err != nil {
				t.Fatal(err)
			}
			original := projectionWriterInput("alpha")
			writeProjectionTransaction(t, db, writer, original)
			changed := projectionWriterInput("alpha")
			test.mutate(&changed)

			tx, err := db.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			err = writer.Write(context.Background(), tx, changed)
			_ = tx.Rollback()
			var conflict *ProjectionWriteConflictError
			if !errors.As(err, &conflict) || conflict.Code != test.code {
				t.Fatalf("Write error=%v, want conflict %s", err, test.code)
			}
			var title string
			if err := db.QueryRow(`SELECT title FROM domain_tasks_v2 WHERE workspace_id='alpha' AND id='task-1'`).Scan(&title); err != nil {
				t.Fatal(err)
			}
			if title != "Write projection" {
				t.Fatalf("target mutated to %q", title)
			}
		})
	}
}

func TestV2ProjectionWriterRejectsTargetAlreadyOwnedByAnotherSource(t *testing.T) {
	db := openProjectionWriterSQLite(t, "alpha")
	writer, err := NewV2ProjectionWriter(DialectSQLite)
	if err != nil {
		t.Fatal(err)
	}
	writeProjectionTransaction(t, db, writer, projectionWriterInput("alpha"))

	changed := projectionWriterInput("alpha")
	changed.Projection.IDMap[1].LegacyID = "other-legacy-task"
	changed.SourceVersions[1].LegacyID = "other-legacy-task"
	seedProjectionSourceVersions(t, db, changed)
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	err = writer.Write(context.Background(), tx, changed)
	_ = tx.Rollback()
	var conflict *ProjectionWriteConflictError
	if !errors.As(err, &conflict) || conflict.Code != ProjectionWriteConflictMappingTarget {
		t.Fatalf("Write error=%v, want mapping target conflict", err)
	}
}

func projectionWriterInput(workspaceID string) V2ProjectionWrite {
	return V2ProjectionWrite{
		WorkspaceID: workspaceID,
		WrittenAt:   time.Date(2026, 7, 22, 10, 30, 0, 0, time.UTC),
		SourceVersions: []ProjectionSourceVersion{
			{EntityKind: LegacyEntityProject, LegacyID: "legacy-project-1", LogicalVersion: 2},
			{EntityKind: LegacyEntityTask, LegacyID: "legacy-task-1", LogicalVersion: 5},
		},
		Projection: V2Projection{
			Projects: []V2ProjectProjection{{
				ID: "project-1", Name: "Projection", Kind: "standard", Horizon: "short",
			}},
			Tasks: []V2TaskProjection{{
				ID: "task-1", ProjectID: "project-1", Title: "Write projection", Description: "Persist the snapshot",
				Priority: 2, SortOrder: 3, LifecycleStatus: taskdomain.TaskLifecycleActive,
			}},
			Schedules: []V2ScheduleProjection{{
				TaskID: "task-1", RecurrenceType: taskdomain.RecurrenceNone, TimingType: taskdomain.TimingDate,
				Timezone: "Asia/Shanghai", StartsOn: "2026-07-22", Interval: 1,
			}},
			Occurrences: []V2OccurrenceProjection{{
				ID: "occurrence-1", TaskID: "task-1", OccurrenceKey: "once",
				ExecutionStatus: taskdomain.ExecutionStatusOpen, PlannedDate: "2026-07-22", GeneratedScheduleRevision: 1,
			}},
			IDMap: []V2IDMapEntry{
				{LegacyKind: LegacyEntityProject, LegacyID: "legacy-project-1", TargetProjectID: "project-1"},
				{LegacyKind: LegacyEntityTask, LegacyID: "legacy-task-1", TargetProjectID: "project-1", TargetTaskID: "task-1", TargetScheduleID: "task-1", TargetOccurrenceID: "occurrence-1"},
			},
		},
	}
}

func writeProjectionTransaction(t *testing.T, db *sql.DB, writer *V2ProjectionWriter, input V2ProjectionWrite) {
	t.Helper()
	seedProjectionSourceVersions(t, db, input)
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin projection transaction: %v", err)
	}
	defer tx.Rollback()
	if err := writer.Write(context.Background(), tx, input); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit projection transaction: %v", err)
	}
}

func seedProjectionSourceVersions(t *testing.T, db *sql.DB, input V2ProjectionWrite) {
	t.Helper()
	for _, source := range input.SourceVersions {
		if _, err := db.Exec(`INSERT INTO legacy_task_domain_entity_versions
			(workspace_id,entity_kind,entity_id,logical_version,deleted)
			VALUES(?,?,?,?,0) ON CONFLICT DO NOTHING`,
			input.WorkspaceID, source.EntityKind, source.LegacyID, source.LogicalVersion); err != nil {
			t.Fatalf("seed durable source version %s/%s: %v", source.EntityKind, source.LegacyID, err)
		}
	}
}

func openProjectionWriterSQLite(t *testing.T, workspaces ...string) *sql.DB {
	t.Helper()
	dsn := "file:taskmigration-projection-writer-" + time.Now().Format("150405.000000000") + "-" +
		time.Duration(projectionWriterSQLiteSequence.Add(1)).String() + "?mode=memory&cache=shared"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`PRAGMA foreign_keys=ON;
		CREATE TABLE tenant_workspaces(workspace_id TEXT PRIMARY KEY);
		CREATE TABLE notes(workspace_id TEXT NOT NULL,id TEXT NOT NULL,PRIMARY KEY(workspace_id,id));`); err != nil {
		t.Fatalf("create projection migration prerequisites: %v", err)
	}
	for _, workspaceID := range workspaces {
		if _, err := db.Exec(`INSERT INTO tenant_workspaces(workspace_id) VALUES(?)`, workspaceID); err != nil {
			t.Fatalf("seed workspace: %v", err)
		}
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
	return db
}

func assertProjectionWriterCount(t *testing.T, db *sql.DB, table, workspaceID string, want int) {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE workspace_id=?`, workspaceID).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if count != want {
		t.Fatalf("%s workspace=%s rows=%d, want %d", table, workspaceID, count, want)
	}
}
