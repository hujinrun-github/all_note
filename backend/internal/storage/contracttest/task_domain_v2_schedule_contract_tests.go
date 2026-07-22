package contracttest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/taskdomain"
)

type TaskDomainV2ScheduleFixture struct {
	DB             *sql.DB
	Dialect        TaskDomainV2Dialect
	Writer         storage.TenantFencedWriter
	Fencer         taskdomain.ScheduleCommandFencer
	NewStateReader func(workspaceID string) taskdomain.ScheduleCommandStateReader
}

func RunTaskDomainV2ScheduleSuite(t *testing.T, fixture TaskDomainV2ScheduleFixture) {
	t.Helper()
	ctx := context.Background()
	mustExec(t, fixture.DB, `INSERT INTO tenant_workspaces(workspace_id) VALUES('schedule-w1')`)
	if err := fixture.Writer.BeginFencedWrite(ctx, "schedule-w1", 1, func(tx storage.TenantWriteTx) error {
		return tx.TaskDomainWriter().EnsureSystemProjects(ctx)
	}); err != nil {
		t.Fatal(err)
	}
	create := func(snapshot taskdomain.TaskAggregateSnapshot) {
		t.Helper()
		if err := fixture.Writer.BeginFencedWrite(ctx, "schedule-w1", 1, func(tx storage.TenantWriteTx) error {
			return tx.TaskDomainWriter().CreateTaskAggregate(ctx, snapshot)
		}); err != nil {
			t.Fatalf("create %s: %v", snapshot.Task.ID, err)
		}
	}
	create(scheduleContractDateAggregate())
	create(scheduleContractDSTAggregate())
	create(scheduleContractRecurringAggregate("schedule-main"))
	create(scheduleContractRecurringAggregate("schedule-rollback"))

	startedAt := time.Date(2026, 7, 23, 9, 5, 0, 0, time.UTC)
	completedAt := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)
	mustExecArgs(t, fixture.DB, `UPDATE domain_task_occurrences_v2 SET execution_status='active',actual_start_at=?
		WHERE workspace_id='schedule-w1' AND id='schedule-main-started'`, fixture.Dialect, queryContractTime(fixture.Dialect, startedAt))
	mustExecArgs(t, fixture.DB, `UPDATE domain_task_occurrences_v2 SET execution_status='done',completed_at=?
		WHERE workspace_id='schedule-w1' AND id='schedule-main-done'`, fixture.Dialect, queryContractTime(fixture.Dialect, completedAt))
	mustExec(t, fixture.DB, `UPDATE domain_task_occurrences_v2 SET manually_overridden=TRUE
		WHERE workspace_id='schedule-w1' AND id='schedule-main-manual'`)

	t.Run("state_reader_returns_header_versions_and_execution_flags", func(t *testing.T) {
		state, err := fixture.NewStateReader("schedule-w1").GetScheduleCommandState(ctx, "schedule-main")
		if err != nil {
			t.Fatalf("read schedule command state: %v", err)
		}
		if state.WorkspaceID != "schedule-w1" || state.TaskID != "schedule-main" || state.TaskRevision != 1 ||
			state.Schedule.Revision != 1 || state.Schedule.CurrentScheduleRevision != 1 || len(state.Versions) != 1 || len(state.Occurrences) != 6 {
			t.Fatalf("incomplete state: %#v", state)
		}
		started := scheduleContractOccurrence(t, state.Occurrences, "schedule-main-started")
		done := scheduleContractOccurrence(t, state.Occurrences, "schedule-main-done")
		manual := scheduleContractOccurrence(t, state.Occurrences, "schedule-main-manual")
		if started.ActualStartAt == nil || !started.ActualStartAt.Equal(startedAt) || done.CompletedAt == nil ||
			!done.CompletedAt.Equal(completedAt) || !manual.ManuallyOverridden {
			t.Fatalf("execution flags = started:%#v done:%#v manual:%#v", started, done, manual)
		}
	})

	t.Run("occurrence_reschedule_persists_date_and_DST_timeblock_after_images", func(t *testing.T) {
		service := taskdomain.NewScheduleService(fixture.Fencer, fixture.NewStateReader("schedule-w1"))
		dateResult, err := service.RescheduleOccurrence(ctx, taskdomain.RescheduleOccurrenceRequest{
			WorkspaceID: "schedule-w1", TaskID: "schedule-date", OccurrenceID: "schedule-date-occ", ExpectedRuntimeEpoch: 1,
			ExpectedTaskRevision: 1, ExpectedScheduleRevision: 1, ExpectedOccurrenceRevision: 1,
			Timing: taskdomain.OccurrenceTimingInput{TimingType: taskdomain.TimingDate, Timezone: "UTC", PlannedDate: "2026-07-25", AllDayEndDate: "2026-07-27"},
		})
		if err != nil || dateResult.OccurrenceRevision() != 2 {
			t.Fatalf("date reschedule result=%#v err=%v", dateResult, err)
		}
		var date, allDayEnd string
		var dateRevision int64
		var dateManual bool
		if err := fixture.DB.QueryRow(`SELECT planned_date,all_day_end_date,revision,manually_overridden FROM domain_task_occurrences_v2
			WHERE workspace_id='schedule-w1' AND id='schedule-date-occ'`).Scan(&date, &allDayEnd, &dateRevision, &dateManual); err != nil {
			t.Fatal(err)
		}
		if scheduleContractDBDate(date) != "2026-07-25" || scheduleContractDBDate(allDayEnd) != "2026-07-27" || dateRevision != 2 || !dateManual {
			t.Fatalf("date after-image date=%s end=%s revision=%d manual=%t", date, allDayEnd, dateRevision, dateManual)
		}

		selected := -5 * 60 * 60
		timeResult, err := service.RescheduleOccurrence(ctx, taskdomain.RescheduleOccurrenceRequest{
			WorkspaceID: "schedule-w1", TaskID: "schedule-dst", OccurrenceID: "schedule-dst-occ", ExpectedRuntimeEpoch: 1,
			ExpectedTaskRevision: 1, ExpectedScheduleRevision: 1, ExpectedOccurrenceRevision: 1,
			Timing: taskdomain.OccurrenceTimingInput{TimingType: taskdomain.TimingTimeBlock, Timezone: "America/New_York", PlannedDate: "2026-11-01", LocalStartTime: "01:30", DurationMinutes: 30, SelectedOffsetSeconds: &selected},
		})
		if err != nil || timeResult.OccurrenceRevision() != 2 || len(timeResult.Candidates()) != 2 {
			t.Fatalf("DST reschedule result=%#v err=%v", timeResult, err)
		}
		var startValue, endValue any
		var timeRevision, generatedRevision int64
		var timeManual bool
		if err := fixture.DB.QueryRow(`SELECT planned_start_at,planned_end_at,revision,generated_schedule_revision,manually_overridden
			FROM domain_task_occurrences_v2 WHERE workspace_id='schedule-w1' AND id='schedule-dst-occ'`).Scan(
			&startValue, &endValue, &timeRevision, &generatedRevision, &timeManual); err != nil {
			t.Fatal(err)
		}
		start := scheduleContractDBTime(t, startValue)
		end := scheduleContractDBTime(t, endValue)
		if !start.Equal(time.Date(2026, 11, 1, 6, 30, 0, 0, time.UTC)) || !end.Equal(start.Add(30*time.Minute)) ||
			timeRevision != 2 || generatedRevision != 1 || !timeManual {
			t.Fatalf("DST after-image start=%s end=%s revision=%d generated=%d manual=%t", start, end, timeRevision, generatedRevision, timeManual)
		}
	})

	t.Run("version_change_is_one_atomic_reconcile_and_preserves_history", func(t *testing.T) {
		service := taskdomain.NewScheduleService(fixture.Fencer, fixture.NewStateReader("schedule-w1"))
		result, err := service.RescheduleThisAndFuture(ctx, scheduleContractFutureRequest("schedule-main"))
		if err != nil {
			t.Fatalf("reschedule this and future: %v", err)
		}
		if result.TaskRevision() != 1 || result.ScheduleRevision() != 2 || result.ScheduleVersion() != 2 {
			t.Fatalf("version result = task:%d schedule:%d version:%d", result.TaskRevision(), result.ScheduleRevision(), result.ScheduleVersion())
		}
		var scheduleRevision, currentVersion, openVersions, versionCount int64
		if err := fixture.DB.QueryRow(`SELECT revision,current_schedule_revision,
			(SELECT COUNT(*) FROM domain_task_schedule_versions_v2 WHERE workspace_id='schedule-w1' AND task_id='schedule-main' AND effective_to IS NULL),
			(SELECT COUNT(*) FROM domain_task_schedule_versions_v2 WHERE workspace_id='schedule-w1' AND task_id='schedule-main')
			FROM domain_task_schedules_v2 WHERE workspace_id='schedule-w1' AND task_id='schedule-main'`).Scan(
			&scheduleRevision, &currentVersion, &openVersions, &versionCount); err != nil {
			t.Fatal(err)
		}
		if scheduleRevision != 2 || currentVersion != 2 || openVersions != 1 || versionCount != 2 {
			t.Fatalf("schedule history revision=%d current=%d open=%d versions=%d", scheduleRevision, currentVersion, openVersions, versionCount)
		}
		var oldTo string
		if err := fixture.DB.QueryRow(`SELECT effective_to FROM domain_task_schedule_versions_v2
			WHERE workspace_id='schedule-w1' AND task_id='schedule-main' AND schedule_revision=1`).Scan(&oldTo); err != nil || scheduleContractDBDate(oldTo) != "2026-07-23" {
			t.Fatalf("old effective_to=%q err=%v", oldTo, err)
		}
		for id, expectedStatus := range map[string]string{
			"schedule-main-past": "open", "schedule-main-started": "active", "schedule-main-done": "done", "schedule-main-manual": "open",
		} {
			var status string
			var revision, generated int64
			statement := fmt.Sprintf(`SELECT execution_status,revision,generated_schedule_revision FROM domain_task_occurrences_v2
				WHERE workspace_id='schedule-w1' AND id=%s`, scheduleContractBind(fixture.Dialect, 1))
			if err := fixture.DB.QueryRow(statement, id).Scan(&status, &revision, &generated); err != nil {
				t.Fatal(err)
			}
			if status != expectedStatus || revision != 1 || generated != 1 {
				t.Fatalf("preserved %s status=%s revision=%d generated=%d", id, status, revision, generated)
			}
		}
		var rewriteRevision, rewriteGenerated int64
		if err := fixture.DB.QueryRow(`SELECT revision,generated_schedule_revision FROM domain_task_occurrences_v2
			WHERE workspace_id='schedule-w1' AND id='schedule-main-rewrite'`).Scan(&rewriteRevision, &rewriteGenerated); err != nil {
			t.Fatal(err)
		}
		if rewriteRevision != 2 || rewriteGenerated != 2 {
			t.Fatalf("rewritten revision=%d generated=%d", rewriteRevision, rewriteGenerated)
		}
		var newGenerated int64
		if err := fixture.DB.QueryRow(`SELECT generated_schedule_revision FROM domain_task_occurrences_v2
			WHERE workspace_id='schedule-w1' AND task_id='schedule-main' AND occurrence_key='2026-07-27'`).Scan(&newGenerated); err != nil || newGenerated != 2 {
			t.Fatalf("new generated revision=%d err=%v", newGenerated, err)
		}
		var staleCount int
		if err := fixture.DB.QueryRow(`SELECT COUNT(*) FROM domain_task_occurrences_v2 WHERE workspace_id='schedule-w1' AND id='schedule-main-stale'`).Scan(&staleCount); err != nil || staleCount != 0 {
			t.Fatalf("stale count=%d err=%v", staleCount, err)
		}
	})

	t.Run("stale_CAS_at_any_boundary_rolls_back_all_rows", func(t *testing.T) {
		reader := fixture.NewStateReader("schedule-w1")
		state, err := reader.GetScheduleCommandState(ctx, "schedule-rollback")
		if err != nil {
			t.Fatal(err)
		}
		current := state.Versions[0]
		closed := current
		closed.EffectiveTo = "2026-07-23"
		newVersion := current
		newVersion.ScheduleRevision = 2
		newVersion.EffectiveFrom = "2026-07-23"
		newVersion.EffectiveTo = ""
		write := taskdomain.ScheduleVersionChangeWrite{
			WorkspaceID: "schedule-w1", TaskID: "schedule-rollback", ExpectedTaskRevision: 1, ExpectedScheduleRevision: 1,
			Schedule:      taskdomain.ScheduleHeader{WorkspaceID: "schedule-w1", TaskID: "schedule-rollback", Revision: 2, CurrentScheduleRevision: 2},
			ClosedVersion: closed, NewVersion: newVersion,
			ExpectedOccurrenceRevisions: map[string]int64{"schedule-rollback-rewrite": 99},
			UpsertOccurrences: []taskdomain.ScheduleOccurrenceSnapshot{{Record: taskdomain.OccurrenceRecord{
				WorkspaceID: "schedule-w1", ID: "schedule-rollback-rewrite", TaskID: "schedule-rollback", OccurrenceKey: "2026-07-26",
				PlannedDate: "2026-07-26", ExecutionStatus: taskdomain.ExecutionStatusOpen, Revision: 100, GeneratedScheduleRevision: 2,
			}}}, DeleteOccurrenceRevisions: map[string]int64{},
		}
		staleGenerator := write
		staleGenerator.ExpectedScheduleRevision = 99
		staleGenerator.Schedule.Revision = 100
		err = fixture.Fencer.BeginFencedScheduleWrite(ctx, "schedule-w1", 1, func(tx taskdomain.ScheduleCommandFencedTx) error {
			return tx.ScheduleCommandWriter().ApplyScheduleVersionChange(ctx, staleGenerator)
		})
		if !errors.Is(err, taskdomain.ErrScheduleRevisionConflict) {
			t.Fatalf("stale generator schedule error=%v", err)
		}
		err = fixture.Fencer.BeginFencedScheduleWrite(ctx, "schedule-w1", 1, func(tx taskdomain.ScheduleCommandFencedTx) error {
			return tx.ScheduleCommandWriter().ApplyScheduleVersionChange(ctx, write)
		})
		if !errors.Is(err, taskdomain.ErrOccurrenceRevisionConflict) {
			t.Fatalf("stale future occurrence error=%v", err)
		}
		var revision, currentRevision, versions, openVersions, generated int64
		if err := fixture.DB.QueryRow(`SELECT revision,current_schedule_revision,
			(SELECT COUNT(*) FROM domain_task_schedule_versions_v2 WHERE workspace_id='schedule-w1' AND task_id='schedule-rollback'),
			(SELECT COUNT(*) FROM domain_task_schedule_versions_v2 WHERE workspace_id='schedule-w1' AND task_id='schedule-rollback' AND effective_to IS NULL),
			(SELECT generated_schedule_revision FROM domain_task_occurrences_v2 WHERE workspace_id='schedule-w1' AND id='schedule-rollback-rewrite')
			FROM domain_task_schedules_v2 WHERE workspace_id='schedule-w1' AND task_id='schedule-rollback'`).Scan(
			&revision, &currentRevision, &versions, &openVersions, &generated); err != nil {
			t.Fatal(err)
		}
		if revision != 1 || currentRevision != 1 || versions != 1 || openVersions != 1 || generated != 1 {
			t.Fatalf("partial version write revision=%d current=%d versions=%d open=%d generated=%d", revision, currentRevision, versions, openVersions, generated)
		}

		baseAfter := state.Occurrences[0]
		baseAfter.Record.Revision++
		baseAfter.ManuallyOverridden = true
		for _, test := range []struct {
			name       string
			task       int64
			schedule   int64
			occurrence int64
			want       error
		}{
			{name: "task", task: 99, schedule: 1, occurrence: state.Occurrences[0].Record.Revision, want: taskdomain.ErrTaskRevisionConflict},
			{name: "schedule", task: 1, schedule: 99, occurrence: state.Occurrences[0].Record.Revision, want: taskdomain.ErrScheduleRevisionConflict},
			{name: "occurrence", task: 1, schedule: 1, occurrence: 99, want: taskdomain.ErrOccurrenceRevisionConflict},
		} {
			t.Run(test.name, func(t *testing.T) {
				after := baseAfter
				after.Record.Revision = test.occurrence + 1
				err := fixture.Fencer.BeginFencedScheduleWrite(ctx, "schedule-w1", 1, func(tx taskdomain.ScheduleCommandFencedTx) error {
					return tx.ScheduleCommandWriter().ApplyOccurrenceReschedule(ctx, taskdomain.OccurrenceRescheduleWrite{
						WorkspaceID: "schedule-w1", TaskID: "schedule-rollback", ExpectedTaskRevision: test.task,
						ExpectedScheduleRevision: test.schedule, ExpectedOccurrenceRevision: test.occurrence, After: after,
					})
				})
				if !errors.Is(err, test.want) {
					t.Fatalf("%s stale error=%v want=%v", test.name, err, test.want)
				}
			})
		}
	})

	t.Run("writer_capability_is_invalid_after_fenced_callback", func(t *testing.T) {
		var captured taskdomain.ScheduleCommandWriter
		if err := fixture.Fencer.BeginFencedScheduleWrite(ctx, "schedule-w1", 1, func(tx taskdomain.ScheduleCommandFencedTx) error {
			captured = tx.ScheduleCommandWriter()
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := captured.ApplyOccurrenceReschedule(ctx, taskdomain.OccurrenceRescheduleWrite{}); !errors.Is(err, storage.ErrTenantWriteTxClosed) {
			t.Fatalf("closed writer error=%v", err)
		}
	})
}

func scheduleContractDateAggregate() taskdomain.TaskAggregateSnapshot {
	return queryDateAggregate("schedule-w1", "schedule-date", taskdomain.PersonalProjectID, "2026-07-22", "")
}

func scheduleContractDSTAggregate() taskdomain.TaskAggregateSnapshot {
	start := time.Date(2026, 11, 1, 4, 30, 0, 0, time.UTC)
	end := start.Add(30 * time.Minute)
	return queryAggregate("schedule-w1", "schedule-dst", taskdomain.PersonalProjectID, taskdomain.ScheduleVersion{
		RecurrenceType: taskdomain.RecurrenceNone, TimingType: taskdomain.TimingTimeBlock, Timezone: "America/New_York", StartsOn: "2026-11-01",
		LocalStartTime: "00:30", DurationMinutes: 30, RecurrenceRule: `{}`,
	}, []taskdomain.OccurrenceRecord{{ID: "schedule-dst-occ", OccurrenceKey: "once", PlannedDate: "2026-11-01", PlannedStartAt: &start, PlannedEndAt: &end}})
}

func scheduleContractRecurringAggregate(taskID string) taskdomain.TaskAggregateSnapshot {
	dates := []struct {
		suffix string
		date   string
	}{
		{suffix: "past", date: "2026-07-22"}, {suffix: "started", date: "2026-07-23"}, {suffix: "done", date: "2026-07-24"},
		{suffix: "manual", date: "2026-07-25"}, {suffix: "rewrite", date: "2026-07-26"}, {suffix: "stale", date: "2026-07-29"},
	}
	occurrences := make([]taskdomain.OccurrenceRecord, 0, len(dates))
	for _, item := range dates {
		start := scheduleContractLocalStart(item.date, 9, 0)
		end := start.Add(time.Hour)
		occurrences = append(occurrences, taskdomain.OccurrenceRecord{
			ID: taskID + "-" + item.suffix, OccurrenceKey: item.date, PlannedDate: item.date, PlannedStartAt: &start, PlannedEndAt: &end,
		})
	}
	return queryAggregate("schedule-w1", taskID, taskdomain.PersonalProjectID, taskdomain.ScheduleVersion{
		EffectiveFrom: "2026-07-01", RecurrenceType: taskdomain.RecurrenceDaily, TimingType: taskdomain.TimingTimeBlock,
		Timezone: "UTC", StartsOn: "2026-07-01", EndsOn: "2026-07-31", RecurrenceRule: `{"interval":1}`, LocalStartTime: "09:00", DurationMinutes: 60,
	}, occurrences)
}

func scheduleContractFutureRequest(taskID string) taskdomain.RescheduleThisAndFutureRequest {
	return taskdomain.RescheduleThisAndFutureRequest{
		WorkspaceID: "schedule-w1", TaskID: taskID, ExpectedRuntimeEpoch: 1, ExpectedTaskRevision: 1, ExpectedScheduleRevision: 1,
		EffectiveFrom: "2026-07-23", GenerateThroughExclusive: "2026-07-28",
		Schedule: taskdomain.ScheduleInput{RecurrenceType: taskdomain.RecurrenceDaily, TimingType: taskdomain.TimingTimeBlock, Timezone: "UTC",
			StartsOn: "2026-07-23", EndsOn: "2026-07-27", Rule: []byte(`{"interval":1}`), LocalStartTime: "10:00", DurationMinutes: 30},
	}
}

func scheduleContractOccurrence(t *testing.T, occurrences []taskdomain.ScheduleOccurrenceSnapshot, id string) taskdomain.ScheduleOccurrenceSnapshot {
	t.Helper()
	for _, occurrence := range occurrences {
		if occurrence.Record.ID == id {
			return occurrence
		}
	}
	t.Fatalf("occurrence %s not found", id)
	return taskdomain.ScheduleOccurrenceSnapshot{}
}

func scheduleContractLocalStart(date string, hour, minute int) time.Time {
	parsed, _ := time.Parse("2006-01-02", date)
	return time.Date(parsed.Year(), parsed.Month(), parsed.Day(), hour, minute, 0, 0, time.UTC)
}

func scheduleContractDBTime(t *testing.T, value any) time.Time {
	t.Helper()
	switch typed := value.(type) {
	case time.Time:
		return typed
	case string:
		parsed, err := time.Parse(time.RFC3339Nano, typed)
		if err != nil {
			t.Fatalf("parse database time %q: %v", typed, err)
		}
		return parsed
	case []byte:
		return scheduleContractDBTime(t, string(typed))
	default:
		t.Fatalf("unsupported database time %T", value)
		return time.Time{}
	}
}

func scheduleContractBind(dialect TaskDomainV2Dialect, position int) string {
	if dialect == TaskDomainV2Postgres {
		return fmt.Sprintf("$%d", position)
	}
	return "?"
}

func scheduleContractDBDate(value string) string {
	if len(value) >= len("2006-01-02") {
		return value[:len("2006-01-02")]
	}
	return value
}
