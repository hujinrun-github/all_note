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

type TaskDomainV2StateFixture struct {
	DB      *sql.DB
	Dialect TaskDomainV2Dialect
	Writer  storage.TenantFencedWriter
}

func RunTaskDomainV2StateSuite(t *testing.T, fixture TaskDomainV2StateFixture) {
	t.Helper()
	ctx := context.Background()

	t.Run("first_v2_business_write_is_recorded_atomically", func(t *testing.T) {
		mustExec(t, fixture.DB, `INSERT INTO tenant_workspaces(workspace_id) VALUES('state-first-write')`)
		assertTaskDomainFirstWriteState(t, fixture.DB, fixture.Dialect, "state-first-write", false, 1, "")

		if err := fixture.Writer.BeginFencedWrite(ctx, "state-first-write", 1, func(tx storage.TenantWriteTx) error {
			return tx.TaskDomainWriter().EnsureSystemProjects(ctx)
		}); err != nil {
			t.Fatalf("first v2 write: %v", err)
		}
		firstWriteAt := assertTaskDomainFirstWriteState(t, fixture.DB, fixture.Dialect, "state-first-write", true, 2, "")

		if err := fixture.Writer.BeginFencedWrite(ctx, "state-first-write", 1, func(tx storage.TenantWriteTx) error {
			return tx.TaskDomainWriter().EnsureSystemProjects(ctx)
		}); err != nil {
			t.Fatalf("second v2 write: %v", err)
		}
		assertTaskDomainFirstWriteState(t, fixture.DB, fixture.Dialect, "state-first-write", true, 2, firstWriteAt)
	})

	t.Run("failed_v2_business_write_rolls_back_first_write_marker", func(t *testing.T) {
		mustExec(t, fixture.DB, `INSERT INTO tenant_workspaces(workspace_id) VALUES('state-first-write-rollback')`)
		sentinel := errors.New("roll back task-domain command")
		err := fixture.Writer.BeginFencedWrite(ctx, "state-first-write-rollback", 1, func(tx storage.TenantWriteTx) error {
			if err := tx.TaskDomainWriter().EnsureSystemProjects(ctx); err != nil {
				return err
			}
			return sentinel
		})
		if !errors.Is(err, sentinel) {
			t.Fatalf("rollback error = %v", err)
		}
		assertTaskDomainFirstWriteState(t, fixture.DB, fixture.Dialect, "state-first-write-rollback", false, 1, "")
		var projects int
		if err := fixture.DB.QueryRow(`SELECT COUNT(*) FROM domain_projects_v2 WHERE workspace_id='state-first-write-rollback'`).Scan(&projects); err != nil {
			t.Fatal(err)
		}
		if projects != 0 {
			t.Fatalf("rolled-back project count = %d", projects)
		}
	})

	t.Run("legacy_shadow_write_does_not_set_v2_first_write", func(t *testing.T) {
		mustExec(t, fixture.DB, `INSERT INTO tenant_workspaces(workspace_id) VALUES('state-legacy-shadow')`)
		mustExec(t, fixture.DB, `UPDATE workspace_task_domain_state
			SET model_version='legacy', accept_legacy_writes=TRUE
			WHERE workspace_id='state-legacy-shadow'`)
		if err := fixture.Writer.BeginFencedWrite(ctx, "state-legacy-shadow", 1, func(tx storage.TenantWriteTx) error {
			return tx.TaskDomainWriter().EnsureSystemProjects(ctx)
		}); err != nil {
			t.Fatalf("legacy shadow write: %v", err)
		}
		assertTaskDomainFirstWriteState(t, fixture.DB, fixture.Dialect, "state-legacy-shadow", false, 1, "")
	})

	t.Run("every_task_domain_command_entry_point_marks_first_write", func(t *testing.T) {
		scheduleFencer, ok := fixture.Writer.(taskdomain.ScheduleCommandFencer)
		if !ok {
			t.Fatal("writer does not implement ScheduleCommandFencer")
		}
		projectFencer, ok := fixture.Writer.(taskdomain.ProjectCommandFencer)
		if !ok {
			t.Fatal("writer does not implement ProjectCommandFencer")
		}
		completionFencer, ok := fixture.Writer.(taskdomain.CompletionCommandFencer)
		if !ok {
			t.Fatal("writer does not implement CompletionCommandFencer")
		}
		generationFencer, ok := fixture.Writer.(taskdomain.GenerationFencer)
		if !ok {
			t.Fatal("writer does not implement GenerationFencer")
		}

		commands := []struct {
			workspaceID string
			run         func() error
		}{
			{workspaceID: "state-entry-schedule", run: func() error {
				return scheduleFencer.BeginFencedScheduleWrite(ctx, "state-entry-schedule", 1, func(taskdomain.ScheduleCommandFencedTx) error { return nil })
			}},
			{workspaceID: "state-entry-project", run: func() error {
				return projectFencer.BeginFencedProjectWrite(ctx, "state-entry-project", 1, func(taskdomain.ProjectCommandTx) error { return nil })
			}},
			{workspaceID: "state-entry-completion", run: func() error {
				return completionFencer.BeginFencedCompletionWrite(ctx, "state-entry-completion", 1, func(taskdomain.CompletionCommandTx) error { return nil })
			}},
			{workspaceID: "state-entry-generation", run: func() error {
				return generationFencer.BeginGenerationWrite(ctx, "state-entry-generation", 1, func(taskdomain.GenerationStateReader, taskdomain.GenerationWriter) error { return nil })
			}},
		}
		for _, command := range commands {
			mustExec(t, fixture.DB, `INSERT INTO tenant_workspaces(workspace_id) VALUES('`+command.workspaceID+`')`)
			if err := command.run(); err != nil {
				t.Fatalf("%s: %v", command.workspaceID, err)
			}
			assertTaskDomainFirstWriteState(t, fixture.DB, fixture.Dialect, command.workspaceID, true, 2, "")
		}
	})

	mustExec(t, fixture.DB, `INSERT INTO tenant_workspaces(workspace_id) VALUES('state-w1')`)
	if err := fixture.Writer.BeginFencedWrite(ctx, "state-w1", 1, func(tx storage.TenantWriteTx) error {
		return tx.TaskDomainWriter().EnsureSystemProjects(ctx)
	}); err != nil {
		t.Fatal(err)
	}
	create := func(snapshot taskdomain.TaskAggregateSnapshot) error {
		return fixture.Writer.BeginFencedWrite(ctx, "state-w1", 1, func(tx storage.TenantWriteTx) error {
			return tx.TaskDomainWriter().CreateTaskAggregate(ctx, snapshot)
		})
	}
	for _, snapshot := range []taskdomain.TaskAggregateSnapshot{
		unscheduledAggregate("state-w1", "task-block"),
		unscheduledAggregate("state-w1", "task-done"),
		recurringAggregate("state-w1", "task-schedule"),
	} {
		if err := create(snapshot); err != nil {
			t.Fatalf("create %s: %v", snapshot.Task.ID, err)
		}
	}

	save := func(write taskdomain.TaskAggregateWrite) error {
		return fixture.Writer.BeginFencedWrite(ctx, "state-w1", 1, func(tx storage.TenantWriteTx) error {
			return tx.TaskDomainWriter().SaveTaskAggregate(ctx, write)
		})
	}
	blockCurrent := initialStateAggregate("state-w1", "task-block", "occ-task-block", false)
	started, startLog, err := taskdomain.StartOccurrence(blockCurrent.Occurrences[0], taskdomain.ExecutionTransition{
		LogID: "log-start", ActorID: "user-1", At: time.Date(2026, 7, 22, 7, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := save(aggregateStateWrite(blockCurrent, started, startLog)); err != nil {
		t.Fatalf("start occurrence before blocked transition: %v", err)
	}
	blockCurrent.Revision = 2
	blockCurrent.Occurrences = []taskdomain.Occurrence{started}

	t.Run("blocked_snapshot_and_execution_log_are_atomic", func(t *testing.T) {
		blocked, log, err := taskdomain.BlockOccurrence(blockCurrent.Occurrences[0], "Waiting for review", "Ask the owner", taskdomain.ExecutionTransition{
			LogID: "log-block", ActorID: "user-1", At: time.Date(2026, 7, 22, 8, 0, 0, 0, time.UTC),
		})
		if err != nil {
			t.Fatal(err)
		}
		write := aggregateStateWrite(blockCurrent, blocked, log)
		if err := save(write); err != nil {
			t.Fatalf("save blocked state: %v", err)
		}
		var status, reason, nextAction string
		var occurrenceRevision, taskRevision, logs int64
		if err := fixture.DB.QueryRow(`SELECT occurrence.execution_status,occurrence.blocked_reason,occurrence.next_action,
			occurrence.revision,task.revision,(SELECT COUNT(*) FROM domain_task_execution_logs_v2 WHERE id='log-block')
			FROM domain_task_occurrences_v2 occurrence
			JOIN domain_tasks_v2 task ON task.workspace_id=occurrence.workspace_id AND task.id=occurrence.task_id
			WHERE occurrence.workspace_id='state-w1' AND occurrence.id='occ-task-block'`).Scan(
			&status, &reason, &nextAction, &occurrenceRevision, &taskRevision, &logs,
		); err != nil {
			t.Fatal(err)
		}
		if status != "blocked" || reason != "Waiting for review" || nextAction != "Ask the owner" || occurrenceRevision != 3 || taskRevision != 3 || logs != 1 {
			t.Fatalf("blocked state status=%s reason=%q next=%q occurrence_rev=%d task_rev=%d logs=%d", status, reason, nextAction, occurrenceRevision, taskRevision, logs)
		}
		expectStatementRejected(t, fixture.DB, `UPDATE domain_task_execution_logs_v2 SET actor_id='other' WHERE workspace_id='state-w1' AND id='log-block'`)
		expectStatementRejected(t, fixture.DB, `DELETE FROM domain_task_execution_logs_v2 WHERE workspace_id='state-w1' AND id='log-block'`)
	})

	t.Run("done_snapshot_sets_completed_at_and_task_lifecycle", func(t *testing.T) {
		current := initialStateAggregate("state-w1", "task-done", "occ-task-done", false)
		next, logs, err := taskdomain.CompleteSingleOccurrence(current, "occ-task-done", taskdomain.AggregateExpectedRevisions{
			Task: 1, Occurrences: map[string]int64{"occ-task-done": 1},
		}, taskdomain.ExecutionTransition{LogID: "log-done", ActorID: "user-1", At: time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)})
		if err != nil {
			t.Fatal(err)
		}
		write := taskdomain.TaskAggregateWrite{Aggregate: next, ExpectedRevisions: taskdomain.AggregateExpectedRevisions{Task: 1, Occurrences: map[string]int64{"occ-task-done": 1}}, ExecutionLogs: logs}
		if err := save(write); err != nil {
			t.Fatalf("save done state: %v", err)
		}
		var lifecycle, status string
		var completedAt sql.NullString
		if err := fixture.DB.QueryRow(`SELECT task.lifecycle_status,occurrence.execution_status,occurrence.completed_at
			FROM domain_tasks_v2 task JOIN domain_task_occurrences_v2 occurrence
			ON occurrence.workspace_id=task.workspace_id AND occurrence.task_id=task.id
			WHERE task.workspace_id='state-w1' AND task.id='task-done'`).Scan(&lifecycle, &status, &completedAt); err != nil {
			t.Fatal(err)
		}
		if lifecycle != "completed" || status != "done" || !completedAt.Valid {
			t.Fatalf("done state lifecycle=%s status=%s completed=%v", lifecycle, status, completedAt)
		}
	})

	t.Run("stale_task_and_occurrence_revisions_roll_back_everything", func(t *testing.T) {
		current := initialStateAggregate("state-w1", "task-block", "occ-task-block", false)
		current.Revision = 3
		current.Occurrences[0].ExecutionStatus = taskdomain.ExecutionStatusBlocked
		current.Occurrences[0].BlockedReason = "Waiting for review"
		current.Occurrences[0].NextAction = "Ask the owner"
		current.Occurrences[0].Revision = 3
		nextOccurrence, log, err := taskdomain.UnblockOccurrence(current.Occurrences[0], taskdomain.ExecutionTransition{LogID: "log-unblock", ActorID: "user-1", At: time.Now().UTC()})
		if err != nil {
			t.Fatal(err)
		}
		next := current
		next.Revision = 4
		next.Occurrences = []taskdomain.Occurrence{nextOccurrence}
		write := taskdomain.TaskAggregateWrite{Aggregate: next, ExpectedRevisions: taskdomain.AggregateExpectedRevisions{Task: 3, Occurrences: map[string]int64{"occ-task-block": 3}}, ExecutionLogs: []taskdomain.ExecutionLog{log}}
		mustExec(t, fixture.DB, `UPDATE domain_tasks_v2 SET revision=4 WHERE workspace_id='state-w1' AND id='task-block'`)
		if err := save(write); !errors.Is(err, taskdomain.ErrAggregateRevisionConflict) {
			t.Fatalf("stale task error = %v", err)
		}

		mustExec(t, fixture.DB, `UPDATE domain_tasks_v2 SET revision=3 WHERE workspace_id='state-w1' AND id='task-block'`)
		mustExec(t, fixture.DB, `UPDATE domain_task_occurrences_v2 SET revision=4 WHERE workspace_id='state-w1' AND id='occ-task-block'`)
		if err := save(write); !errors.Is(err, taskdomain.ErrAggregateRevisionConflict) {
			t.Fatalf("stale occurrence error = %v", err)
		}
		var taskRevision, logs int
		if err := fixture.DB.QueryRow(`SELECT revision,(SELECT COUNT(*) FROM domain_task_execution_logs_v2 WHERE id='log-unblock')
			FROM domain_tasks_v2 WHERE workspace_id='state-w1' AND id='task-block'`).Scan(&taskRevision, &logs); err != nil {
			t.Fatal(err)
		}
		if taskRevision != 3 || logs != 0 {
			t.Fatalf("rollback task_revision=%d logs=%d", taskRevision, logs)
		}
	})

	t.Run("install_schedule_version_closes_old_range_and_preserves_occurrence_history", func(t *testing.T) {
		install := scheduleInstall("state-w1", "task-schedule", 1, 2, "2026-08-01")
		err := fixture.Writer.BeginFencedWrite(ctx, "state-w1", 1, func(tx storage.TenantWriteTx) error {
			return tx.TaskDomainWriter().InstallScheduleVersion(ctx, install)
		})
		if err != nil {
			t.Fatalf("install schedule version: %v", err)
		}
		var scheduleRevision, currentRevision, oldGenerated int64
		var oldEffectiveTo sql.NullString
		if err := fixture.DB.QueryRow(`SELECT schedule.revision,schedule.current_schedule_revision,old_version.effective_to,occurrence.generated_schedule_revision
			FROM domain_task_schedules_v2 schedule
			JOIN domain_task_schedule_versions_v2 old_version ON old_version.workspace_id=schedule.workspace_id AND old_version.task_id=schedule.task_id AND old_version.schedule_revision=1
			JOIN domain_task_occurrences_v2 occurrence ON occurrence.workspace_id=schedule.workspace_id AND occurrence.task_id=schedule.task_id
			WHERE schedule.workspace_id='state-w1' AND schedule.task_id='task-schedule' LIMIT 1`).Scan(
			&scheduleRevision, &currentRevision, &oldEffectiveTo, &oldGenerated,
		); err != nil {
			t.Fatal(err)
		}
		if scheduleRevision != 2 || currentRevision != 2 || !oldEffectiveTo.Valid || oldEffectiveTo.String[:10] != "2026-08-01" || oldGenerated != 1 {
			t.Fatalf("schedule revision=%d current=%d old_to=%v generated=%d", scheduleRevision, currentRevision, oldEffectiveTo, oldGenerated)
		}

		stale := scheduleInstall("state-w1", "task-schedule", 1, 3, "2026-09-01")
		err = fixture.Writer.BeginFencedWrite(ctx, "state-w1", 1, func(tx storage.TenantWriteTx) error {
			return tx.TaskDomainWriter().InstallScheduleVersion(ctx, stale)
		})
		if !errors.Is(err, taskdomain.ErrAggregateRevisionConflict) {
			t.Fatalf("stale schedule error = %v", err)
		}
		expectStatementRejected(t, fixture.DB, scheduleVersionDirectInsertSQL("state-w1", "task-schedule", 3, "2026-09-01", "", "daily", "date", "2026-09-01", `{"interval":1}`))
		expectStatementRejected(t, fixture.DB, `UPDATE domain_task_schedule_versions_v2 SET effective_to='2026-09-01'
			WHERE workspace_id='state-w1' AND task_id='task-schedule' AND schedule_revision=2`)
	})
}

func assertTaskDomainFirstWriteState(t *testing.T, db *sql.DB, dialect TaskDomainV2Dialect, workspaceID string, wantSet bool, wantRevision int64, wantTimestamp string) string {
	t.Helper()
	var firstWriteAt sql.NullString
	var revision int64
	query := `SELECT CAST(v2_first_write_at AS TEXT), revision
		FROM workspace_task_domain_state WHERE workspace_id=?`
	if dialect == TaskDomainV2Postgres {
		query = `SELECT CAST(v2_first_write_at AS TEXT), revision
			FROM workspace_task_domain_state WHERE workspace_id=$1`
	}
	if err := db.QueryRow(query, workspaceID).Scan(&firstWriteAt, &revision); err != nil {
		t.Fatal(err)
	}
	if firstWriteAt.Valid != wantSet || revision != wantRevision {
		t.Fatalf("workspace %s first_write=%v revision=%d, want set=%v revision=%d", workspaceID, firstWriteAt, revision, wantSet, wantRevision)
	}
	if wantTimestamp != "" && firstWriteAt.String != wantTimestamp {
		t.Fatalf("workspace %s first_write=%q, want original %q", workspaceID, firstWriteAt.String, wantTimestamp)
	}
	return firstWriteAt.String
}

func initialStateAggregate(workspaceID, taskID, occurrenceID string, recurring bool) taskdomain.TaskAggregate {
	key := "once"
	if recurring {
		key = "2026-07-22"
	}
	return taskdomain.TaskAggregate{
		WorkspaceID: workspaceID, TaskID: taskID, LifecycleStatus: taskdomain.TaskLifecycleActive, Recurring: recurring, Revision: 1,
		Occurrences: []taskdomain.Occurrence{{
			WorkspaceID: workspaceID, ID: occurrenceID, TaskID: taskID, OccurrenceKey: key,
			ExecutionStatus: taskdomain.ExecutionStatusOpen, Recurring: recurring, Revision: 1,
		}},
	}
}

func aggregateStateWrite(current taskdomain.TaskAggregate, nextOccurrence taskdomain.Occurrence, log taskdomain.ExecutionLog) taskdomain.TaskAggregateWrite {
	next := current
	next.Revision++
	next.Occurrences = []taskdomain.Occurrence{nextOccurrence}
	return taskdomain.TaskAggregateWrite{
		Aggregate:         next,
		ExpectedRevisions: taskdomain.AggregateExpectedRevisions{Task: current.Revision, Occurrences: map[string]int64{current.Occurrences[0].ID: current.Occurrences[0].Revision}},
		ExecutionLogs:     []taskdomain.ExecutionLog{log},
	}
}

func scheduleInstall(workspaceID, taskID string, expectedHeader, versionRevision int64, effectiveFrom string) taskdomain.ScheduleVersionInstall {
	return taskdomain.ScheduleVersionInstall{
		WorkspaceID: workspaceID, TaskID: taskID, ExpectedScheduleRevision: expectedHeader,
		Version: taskdomain.ScheduleVersion{
			WorkspaceID: workspaceID, TaskID: taskID, ScheduleRevision: versionRevision, EffectiveFrom: effectiveFrom,
			RecurrenceType: taskdomain.RecurrenceDaily, TimingType: taskdomain.TimingDate, Timezone: "UTC",
			StartsOn: effectiveFrom, RecurrenceRule: `{"interval":1}`,
		},
	}
}

func scheduleVersionDirectInsertSQL(workspaceID, taskID string, revision int64, effectiveFrom, effectiveTo, recurrenceType, timingType, startsOn, rule string) string {
	to := "NULL"
	if effectiveTo != "" {
		to = "'" + effectiveTo + "'"
	}
	return fmt.Sprintf(`INSERT INTO domain_task_schedule_versions_v2
		(workspace_id,task_id,schedule_revision,effective_from,effective_to,recurrence_type,timing_type,timezone,starts_on,recurrence_rule,created_at)
		VALUES ('%s','%s',%d,'%s',%s,'%s','%s','UTC','%s','%s',CURRENT_TIMESTAMP)`,
		workspaceID, taskID, revision, effectiveFrom, to, recurrenceType, timingType, startsOn, rule)
}
