package taskmigration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/taskdomain"
	_ "modernc.org/sqlite"
)

var backfillSQLiteSequence atomic.Uint64

func TestBackfillStoreRunsConsistentSQLiteSnapshotAndIsIdempotent(t *testing.T) {
	db := openBackfillSQLite(t)
	seedBackfillFixture(t, db)
	store, err := NewBackfillStore(db, DialectSQLite)
	if err != nil {
		t.Fatalf("NewBackfillStore: %v", err)
	}

	loaderCalls := 0
	projectorCalls := 0
	loader := func(ctx context.Context, tx *sql.Tx) (LegacyTaskDomainInventory, LegacyTaskDomainRows, error) {
		loaderCalls++
		return loadBackfillFixture(ctx, tx, "alpha")
	}
	projector := func(ctx context.Context, tx *sql.Tx, projection V2Projection) error {
		projectorCalls++
		assertBackfillProjection(t, projection)
		_, err := tx.ExecContext(ctx, `INSERT INTO backfill_projection_writes(workspace_id,task_count,occurrence_count,id_map_count)
			VALUES(?,?,?,?)`, "alpha", len(projection.Tasks), len(projection.Occurrences), len(projection.IDMap))
		return err
	}

	result, err := store.RunBackfill(context.Background(), "alpha", 2, 7, loader, projector)
	if err != nil {
		t.Fatalf("RunBackfill: %v", err)
	}
	if result.SnapshotSequence != 3 {
		t.Fatalf("snapshot sequence=%d, want workspace alpha max 3 (not global max 4)", result.SnapshotSequence)
	}
	if result.Idempotent {
		t.Fatal("first backfill unexpectedly marked idempotent")
	}
	if result.State.MigrationState != MigrationStateCatchingUp || result.State.SourceWatermark != 3 || result.State.Revision != 3 {
		t.Fatalf("unexpected next state: %+v", result.State)
	}
	assertBackfillDurableState(t, db, "alpha", "catching_up", 3, 3)
	assertBackfillDurableState(t, db, "beta", "backfilling", 0, 2)
	assertBackfillProjectionWriteCount(t, db, "alpha", 1)

	retry, err := store.RunBackfill(context.Background(), "alpha", 2, 7, loader, projector)
	if err != nil {
		t.Fatalf("idempotent retry: %v", err)
	}
	if !retry.Idempotent || retry.SnapshotSequence != 3 {
		t.Fatalf("unexpected retry result: %+v", retry)
	}
	if loaderCalls != 1 || projectorCalls != 1 {
		t.Fatalf("retry reran callbacks: loader=%d projector=%d", loaderCalls, projectorCalls)
	}
	assertBackfillProjectionWriteCount(t, db, "alpha", 1)
}

func TestBackfillStoreRollsBackEveryFailureStage(t *testing.T) {
	tests := []struct {
		name      string
		revision  uint64
		loader    BackfillLoader
		projector BackfillProjector
		wantCode  BackfillConflictCode
	}{
		{
			name:     "stale state fence",
			revision: 99,
			loader: func(context.Context, *sql.Tx) (LegacyTaskDomainInventory, LegacyTaskDomainRows, error) {
				t.Fatal("loader must not run after state conflict")
				return LegacyTaskDomainInventory{}, LegacyTaskDomainRows{}, nil
			},
			projector: func(context.Context, *sql.Tx, V2Projection) error {
				t.Fatal("projector must not run after state conflict")
				return nil
			},
			wantCode: BackfillConflictStaleRevision,
		},
		{
			name:     "loader",
			revision: 2,
			loader: func(context.Context, *sql.Tx) (LegacyTaskDomainInventory, LegacyTaskDomainRows, error) {
				return LegacyTaskDomainInventory{}, LegacyTaskDomainRows{}, errors.New("load exploded")
			},
			projector: func(context.Context, *sql.Tx, V2Projection) error {
				t.Fatal("projector must not run after loader failure")
				return nil
			},
		},
		{
			name:     "preflight",
			revision: 2,
			loader: func(context.Context, *sql.Tx) (LegacyTaskDomainInventory, LegacyTaskDomainRows, error) {
				return LegacyTaskDomainInventory{WorkspaceID: "alpha", WorkspaceTimezone: "UTC"}, LegacyTaskDomainRows{}, nil
			},
			projector: func(context.Context, *sql.Tx, V2Projection) error {
				t.Fatal("projector must not run after preflight failure")
				return nil
			},
		},
		{
			name:     "mapper",
			revision: 2,
			loader: func(ctx context.Context, tx *sql.Tx) (LegacyTaskDomainInventory, LegacyTaskDomainRows, error) {
				inventory, rows, err := loadBackfillFixture(ctx, tx, "alpha")
				rows.Projects = nil
				return inventory, rows, err
			},
			projector: func(context.Context, *sql.Tx, V2Projection) error {
				t.Fatal("projector must not run after mapping failure")
				return nil
			},
		},
		{
			name:     "projector",
			revision: 2,
			loader: func(ctx context.Context, tx *sql.Tx) (LegacyTaskDomainInventory, LegacyTaskDomainRows, error) {
				return loadBackfillFixture(ctx, tx, "alpha")
			},
			projector: func(ctx context.Context, tx *sql.Tx, _ V2Projection) error {
				if _, err := tx.ExecContext(ctx, `INSERT INTO backfill_projection_writes(workspace_id) VALUES(?)`, "alpha"); err != nil {
					return err
				}
				return errors.New("project exploded")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openBackfillSQLite(t)
			seedBackfillFixture(t, db)
			store, err := NewBackfillStore(db, DialectSQLite)
			if err != nil {
				t.Fatalf("NewBackfillStore: %v", err)
			}
			_, err = store.RunBackfill(context.Background(), "alpha", test.revision, 7, test.loader, test.projector)
			if err == nil {
				t.Fatal("RunBackfill unexpectedly succeeded")
			}
			if test.wantCode != "" {
				var conflict *BackfillConflictError
				if !errors.As(err, &conflict) || conflict.Code != test.wantCode {
					t.Fatalf("error=%v, want conflict code %s", err, test.wantCode)
				}
			}
			assertBackfillDurableState(t, db, "alpha", "backfilling", 0, 2)
			assertBackfillDurableState(t, db, "beta", "backfilling", 0, 2)
			assertBackfillProjectionWriteCount(t, db, "alpha", 0)
		})
	}
}

func TestBackfillStoreRejectsChangedWatermarkOnCompletedRetry(t *testing.T) {
	db := openBackfillSQLite(t)
	seedBackfillFixture(t, db)
	store, err := NewBackfillStore(db, DialectSQLite)
	if err != nil {
		t.Fatal(err)
	}
	loader := func(ctx context.Context, tx *sql.Tx) (LegacyTaskDomainInventory, LegacyTaskDomainRows, error) {
		return loadBackfillFixture(ctx, tx, "alpha")
	}
	projector := func(ctx context.Context, tx *sql.Tx, _ V2Projection) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO backfill_projection_writes(workspace_id) VALUES(?)`, "alpha")
		return err
	}
	if _, err := store.RunBackfill(context.Background(), "alpha", 2, 7, loader, projector); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO task_domain_legacy_outbox(workspace_id) VALUES('alpha')`); err != nil {
		t.Fatal(err)
	}
	_, err = store.RunBackfill(context.Background(), "alpha", 2, 7, loader, projector)
	var conflict *BackfillConflictError
	if !errors.As(err, &conflict) || conflict.Code != BackfillConflictSnapshotChanged {
		t.Fatalf("error=%v, want snapshot_changed conflict", err)
	}
	assertBackfillProjectionWriteCount(t, db, "alpha", 1)
}

func TestBackfillStoreRollsBackProjectionWhenStateAdvanceFails(t *testing.T) {
	db := openBackfillSQLite(t)
	seedBackfillFixture(t, db)
	if _, err := db.Exec(`CREATE TRIGGER reject_backfill_state_advance
		BEFORE UPDATE OF migration_state ON workspace_task_domain_state
		WHEN NEW.migration_state='catching_up'
		BEGIN SELECT RAISE(ABORT, 'forced state advance failure'); END`); err != nil {
		t.Fatal(err)
	}
	store, err := NewBackfillStore(db, DialectSQLite)
	if err != nil {
		t.Fatal(err)
	}
	loader := func(ctx context.Context, tx *sql.Tx) (LegacyTaskDomainInventory, LegacyTaskDomainRows, error) {
		return loadBackfillFixture(ctx, tx, "alpha")
	}
	projector := func(ctx context.Context, tx *sql.Tx, projection V2Projection) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO backfill_projection_writes(workspace_id,task_count,id_map_count) VALUES(?,?,?)`,
			"alpha", len(projection.Tasks), len(projection.IDMap))
		return err
	}
	if _, err := store.RunBackfill(context.Background(), "alpha", 2, 7, loader, projector); err == nil || !strings.Contains(err.Error(), "forced state advance failure") {
		t.Fatalf("RunBackfill error=%v, want injected state failure", err)
	}
	assertBackfillDurableState(t, db, "alpha", "backfilling", 0, 2)
	assertBackfillProjectionWriteCount(t, db, "alpha", 0)
}

func TestNewBackfillStoreValidatesInputs(t *testing.T) {
	db := openBackfillSQLite(t)
	if _, err := NewBackfillStore(nil, DialectSQLite); !errors.Is(err, ErrInvalidBackfillInput) {
		t.Fatalf("nil db error=%v", err)
	}
	if _, err := NewBackfillStore(db, Dialect("oracle")); !errors.Is(err, ErrInvalidBackfillInput) {
		t.Fatalf("invalid dialect error=%v", err)
	}
	store, err := NewBackfillStore(db, DialectSQLite)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.RunBackfill(context.Background(), "", 1, 1, nil, nil)
	if !errors.Is(err, ErrInvalidBackfillInput) {
		t.Fatalf("invalid run input error=%v", err)
	}
}

func openBackfillSQLite(t *testing.T) *sql.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:taskmigration-backfill-%d?mode=memory&cache=shared", backfillSQLiteSequence.Add(1))
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(4)
	t.Cleanup(func() { _ = db.Close() })
	for _, statement := range strings.Split(backfillSQLiteSchema, ";") {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("create schema: %v\n%s", err, statement)
		}
	}
	return db
}

func seedBackfillFixture(t *testing.T, db *sql.DB) {
	t.Helper()
	statements := []string{
		`INSERT INTO workspace_task_domain_state(workspace_id,model_version,migration_state,write_epoch,accept_legacy_writes,migration_timezone,migration_id,revision)
		 VALUES('alpha','legacy','backfilling',7,1,'Asia/Shanghai','migration-alpha',2),
		       ('beta','legacy','backfilling',8,1,'UTC','migration-beta',2)`,
		`INSERT INTO task_domain_legacy_outbox(workspace_id) VALUES('alpha'),('beta'),('alpha'),('beta')`,
		`INSERT INTO legacy_projects VALUES('alpha','personal','个人', 'personal','2026-01-01T00:00:00Z')`,
		`INSERT INTO legacy_tasks VALUES
		 ('alpha','task-done','personal','single','已完成','task body',2,15,'2026-07-02','done',1,'2026-07-02T03:04:05Z','2026-07-02T03:04:05Z','note-task'),
		 ('alpha','task-repeat','personal','recurring','循环任务','repeat body',1,20,'','open',0,NULL,'2026-07-02T03:00:00Z','')`,
		`INSERT INTO legacy_rules VALUES('alpha','rule-1','task-repeat','weekly','date','Asia/Shanghai','2026-07-01','2026-08-01',1,'1,3','', '',0)`,
		`INSERT INTO legacy_occurrences VALUES('alpha','occ-1','task-repeat','2026-07-02','blocked',NULL,'2026-07-02T04:00:00Z',NULL,'等待输入','联系负责人')`,
		`INSERT INTO legacy_events VALUES('alpha','event-1','personal','全天活动','calendar body','2026-07-01T16:00:00Z','2026-07-03T16:00:00Z',1,'上海','focus','calendar notes','note-event')`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("seed fixture: %v\n%s", err, statement)
		}
	}
}

func loadBackfillFixture(ctx context.Context, tx *sql.Tx, workspaceID string) (LegacyTaskDomainInventory, LegacyTaskDomainRows, error) {
	inventory := LegacyTaskDomainInventory{
		WorkspaceID:       workspaceID,
		WorkspaceTimezone: "Asia/Shanghai",
		Sources: map[LegacySourceKind][]string{
			LegacySourceProject:    {"id", "name", "type", "created_at"},
			LegacySourceTask:       {"id", "project_id", "priority"},
			LegacySourceRule:       {"id", "task_id"},
			LegacySourceOccurrence: {"task_id", "occurrence_date", "status"},
			LegacySourceEvent:      {"id", "project_id", "start_time", "end_time", "is_all_day"},
			LegacySourceRoadmap:    {"id", "project_id"},
		},
	}
	var rows LegacyTaskDomainRows

	projectRows, err := tx.QueryContext(ctx, `SELECT id,name,type,created_at FROM legacy_projects WHERE workspace_id=?`, workspaceID)
	if err != nil {
		return inventory, rows, err
	}
	for projectRows.Next() {
		var id, name, kind, created string
		if err := projectRows.Scan(&id, &name, &kind, &created); err != nil {
			_ = projectRows.Close()
			return inventory, rows, err
		}
		createdAt, err := time.Parse(time.RFC3339, created)
		if err != nil {
			_ = projectRows.Close()
			return inventory, rows, err
		}
		inventory.Projects = append(inventory.Projects, LegacyProject{ID: id, Name: name, Type: LegacyProjectType(kind), CreatedAt: createdAt})
		rows.Projects = append(rows.Projects, LegacyProjectRow{ID: id, Name: name, Type: LegacyProjectType(kind)})
	}
	if err := projectRows.Close(); err != nil {
		return inventory, rows, err
	}

	taskRows, err := tx.QueryContext(ctx, `SELECT id,project_id,execution_type,title,content,priority,sort_order,planned_date,status,done,completed_at,updated_at,note_id
		FROM legacy_tasks WHERE workspace_id=? ORDER BY id`, workspaceID)
	if err != nil {
		return inventory, rows, err
	}
	for taskRows.Next() {
		var row LegacyTaskRow
		var execution, status, updated string
		var completed sql.NullString
		var done int
		if err := taskRows.Scan(&row.ID, &row.ProjectID, &execution, &row.Title, &row.Content, &row.Priority, &row.SortOrder,
			&row.PlannedDate, &status, &done, &completed, &updated, &row.NoteID); err != nil {
			_ = taskRows.Close()
			return inventory, rows, err
		}
		row.ExecutionType = LegacyExecutionType(execution)
		row.Status = taskdomain.ExecutionStatus(status)
		row.Done = done != 0
		if completed.Valid {
			value, err := time.Parse(time.RFC3339, completed.String)
			if err != nil {
				_ = taskRows.Close()
				return inventory, rows, err
			}
			row.CompletedAt = &value
		}
		row.UpdatedAt, err = time.Parse(time.RFC3339, updated)
		if err != nil {
			_ = taskRows.Close()
			return inventory, rows, err
		}
		inventory.Tasks = append(inventory.Tasks, LegacyTask{ID: row.ID, ProjectID: row.ProjectID, Priority: row.Priority})
		rows.Tasks = append(rows.Tasks, row)
	}
	if err := taskRows.Close(); err != nil {
		return inventory, rows, err
	}

	var weekdays, monthDays string
	var rule LegacyRuleRow
	var recurrence, timing string
	err = tx.QueryRowContext(ctx, `SELECT id,task_id,recurrence_type,timing_type,timezone,starts_on,ends_on,interval,weekdays,month_days,local_start_time,duration_minutes
		FROM legacy_rules WHERE workspace_id=?`, workspaceID).Scan(&rule.ID, &rule.TaskID, &recurrence, &timing, &rule.Timezone,
		&rule.StartsOn, &rule.EndsOn, &rule.Interval, &weekdays, &monthDays, &rule.LocalStartTime, &rule.DurationMinutes)
	if err != nil {
		return inventory, rows, err
	}
	rule.RecurrenceType = taskdomain.RecurrenceType(recurrence)
	rule.TimingType = taskdomain.TimingType(timing)
	rule.Weekdays = []int{1, 3}
	rows.Rules = append(rows.Rules, rule)

	var occurrence LegacyOccurrenceRow
	var occurrenceStatus, occurrenceUpdated string
	var occurrenceCompleted, occurrenceDue sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT id,task_id,occurrence_date,status,completed_at,updated_at,due_at,blocked_reason,next_action
		FROM legacy_occurrences WHERE workspace_id=?`, workspaceID).Scan(&occurrence.ID, &occurrence.TaskID, &occurrence.OccurrenceDate,
		&occurrenceStatus, &occurrenceCompleted, &occurrenceUpdated, &occurrenceDue, &occurrence.BlockedReason, &occurrence.NextAction)
	if err != nil {
		return inventory, rows, err
	}
	occurrence.Status = taskdomain.ExecutionStatus(occurrenceStatus)
	occurrence.UpdatedAt, _ = time.Parse(time.RFC3339, occurrenceUpdated)
	rows.Occurrences = append(rows.Occurrences, occurrence)

	var event LegacyEventRow
	var startAt, endAt string
	var allDay int
	err = tx.QueryRowContext(ctx, `SELECT id,project_id,title,description,start_at,end_at,is_all_day,location,kind,notes,note_id
		FROM legacy_events WHERE workspace_id=?`, workspaceID).Scan(&event.ID, &event.ProjectID, &event.Title, &event.Description, &startAt,
		&endAt, &allDay, &event.Location, &event.Kind, &event.Notes, &event.NoteID)
	if err != nil {
		return inventory, rows, err
	}
	event.StartAt, _ = time.Parse(time.RFC3339, startAt)
	event.EndAt, _ = time.Parse(time.RFC3339, endAt)
	event.AllDay = allDay != 0
	inventory.Events = append(inventory.Events, LegacyEvent{ID: event.ID, AllDay: event.AllDay, StartAt: event.StartAt, EndAt: event.EndAt})
	rows.Events = append(rows.Events, event)
	return inventory, rows, nil
}

func assertBackfillProjection(t *testing.T, projection V2Projection) {
	t.Helper()
	if len(projection.Projects) != 2 || len(projection.Tasks) != 3 || len(projection.Schedules) != 3 || len(projection.Occurrences) != 3 {
		t.Fatalf("unexpected projection counts: projects=%d tasks=%d schedules=%d occurrences=%d", len(projection.Projects), len(projection.Tasks), len(projection.Schedules), len(projection.Occurrences))
	}
	var completedTask V2TaskProjection
	var completedOccurrence, blockedOccurrence, eventOccurrence V2OccurrenceProjection
	for _, task := range projection.Tasks {
		if task.ID == "task-done" {
			completedTask = task
		}
	}
	for _, occurrence := range projection.Occurrences {
		switch occurrence.TaskID {
		case "task-done":
			completedOccurrence = occurrence
		case "task-repeat":
			blockedOccurrence = occurrence
		default:
			if occurrence.CalendarKind == "focus" {
				eventOccurrence = occurrence
			}
		}
	}
	if completedTask.TaskNoteID != "note-task" || completedTask.LifecycleStatus != taskdomain.TaskLifecycleCompleted || completedOccurrence.CompletedAt == nil {
		t.Fatalf("completed task metadata lost: task=%+v occurrence=%+v", completedTask, completedOccurrence)
	}
	if blockedOccurrence.ExecutionStatus != taskdomain.ExecutionStatusBlocked || blockedOccurrence.BlockedReason != "等待输入" || blockedOccurrence.NextAction != "联系负责人" {
		t.Fatalf("blocked metadata lost: %+v", blockedOccurrence)
	}
	if eventOccurrence.PlannedDate != "2026-07-02" || eventOccurrence.AllDayEndDate != "2026-07-04" || eventOccurrence.PlannedStartAt != nil ||
		eventOccurrence.CalendarNotes != "calendar notes" || eventOccurrence.OccurrenceNoteID != "note-event" {
		t.Fatalf("all-day/calendar metadata lost: %+v", eventOccurrence)
	}
}

func assertBackfillDurableState(t *testing.T, db *sql.DB, workspaceID, migrationState string, watermark, revision int64) {
	t.Helper()
	var gotState string
	var gotWatermark, gotRevision int64
	if err := db.QueryRow(`SELECT migration_state,source_watermark,revision FROM workspace_task_domain_state WHERE workspace_id=?`, workspaceID).
		Scan(&gotState, &gotWatermark, &gotRevision); err != nil {
		t.Fatal(err)
	}
	if gotState != migrationState || gotWatermark != watermark || gotRevision != revision {
		t.Fatalf("workspace %s state=(%s,%d,%d), want=(%s,%d,%d)", workspaceID, gotState, gotWatermark, gotRevision, migrationState, watermark, revision)
	}
}

func assertBackfillProjectionWriteCount(t *testing.T, db *sql.DB, workspaceID string, want int) {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM backfill_projection_writes WHERE workspace_id=?`, workspaceID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("projection write count=%d, want=%d", count, want)
	}
}

const backfillSQLiteSchema = `
CREATE TABLE workspace_task_domain_state(
 workspace_id TEXT PRIMARY KEY, model_version TEXT NOT NULL, migration_state TEXT NOT NULL,
 source_watermark INTEGER NOT NULL DEFAULT 0, cutover_revision INTEGER, write_epoch INTEGER NOT NULL,
 accept_legacy_writes INTEGER NOT NULL, migration_timezone TEXT NOT NULL, v2_first_write_at TEXT,
 migration_id TEXT, last_error TEXT, revision INTEGER NOT NULL, updated_at TEXT DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE task_domain_legacy_outbox(sequence INTEGER PRIMARY KEY AUTOINCREMENT, workspace_id TEXT NOT NULL);
CREATE TABLE backfill_projection_writes(
 workspace_id TEXT PRIMARY KEY, task_count INTEGER NOT NULL DEFAULT 0,
 occurrence_count INTEGER NOT NULL DEFAULT 0, id_map_count INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE legacy_projects(workspace_id TEXT,id TEXT,name TEXT,type TEXT,created_at TEXT);
CREATE TABLE legacy_tasks(
 workspace_id TEXT,id TEXT,project_id TEXT,execution_type TEXT,title TEXT,content TEXT,priority INTEGER,
 sort_order INTEGER,planned_date TEXT,status TEXT,done INTEGER,completed_at TEXT,updated_at TEXT,note_id TEXT
);
CREATE TABLE legacy_rules(
 workspace_id TEXT,id TEXT,task_id TEXT,recurrence_type TEXT,timing_type TEXT,timezone TEXT,starts_on TEXT,ends_on TEXT,
 interval INTEGER,weekdays TEXT,month_days TEXT,local_start_time TEXT,duration_minutes INTEGER
);
CREATE TABLE legacy_occurrences(
 workspace_id TEXT,id TEXT,task_id TEXT,occurrence_date TEXT,status TEXT,completed_at TEXT,updated_at TEXT,due_at TEXT,
 blocked_reason TEXT,next_action TEXT
);
CREATE TABLE legacy_events(
 workspace_id TEXT,id TEXT,project_id TEXT,title TEXT,description TEXT,start_at TEXT,end_at TEXT,is_all_day INTEGER,
 location TEXT,kind TEXT,notes TEXT,note_id TEXT
);`
