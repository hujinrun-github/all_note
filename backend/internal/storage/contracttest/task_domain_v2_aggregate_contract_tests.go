package contracttest

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/taskdomain"
)

type TaskDomainV2AggregateFixture struct {
	DB      *sql.DB
	Dialect TaskDomainV2Dialect
	Writer  storage.TenantFencedWriter
}

func RunTaskDomainV2AggregateSuite(t *testing.T, fixture TaskDomainV2AggregateFixture) {
	t.Helper()
	ctx := context.Background()
	for _, workspaceID := range []string{"aggregate-w1", "aggregate-w2"} {
		mustExec(t, fixture.DB, fmt.Sprintf(`INSERT INTO tenant_workspaces(workspace_id) VALUES('%s')`, workspaceID))
		if err := fixture.Writer.BeginFencedWrite(ctx, workspaceID, 1, func(tx storage.TenantWriteTx) error {
			return tx.TaskDomainWriter().EnsureSystemProjects(ctx)
		}); err != nil {
			t.Fatalf("ensure system projects for %s: %v", workspaceID, err)
		}
	}

	create := func(workspaceID string, snapshot taskdomain.TaskAggregateSnapshot) error {
		return fixture.Writer.BeginFencedWrite(ctx, workspaceID, 1, func(tx storage.TenantWriteTx) error {
			return tx.TaskDomainWriter().CreateTaskAggregate(ctx, snapshot)
		})
	}

	t.Run("missing_project_rejects_without_partial_rows", func(t *testing.T) {
		snapshot := unscheduledAggregate("aggregate-w1", "task-missing-project")
		snapshot.Task.ProjectID = "missing"
		if err := create("aggregate-w1", snapshot); err == nil {
			t.Fatal("missing project must be rejected")
		}
		assertAggregateCounts(t, fixture.DB, snapshot.Task.ID, 0, 0, 0, 0)
	})

	t.Run("single_unscheduled_creates_one_once_occurrence_atomically", func(t *testing.T) {
		snapshot := unscheduledAggregate("aggregate-w1", "task-unscheduled")
		if err := create("aggregate-w1", snapshot); err != nil {
			t.Fatalf("create unscheduled aggregate: %v", err)
		}
		assertAggregateCounts(t, fixture.DB, snapshot.Task.ID, 1, 1, 1, 1)
		var key string
		var plannedDate, plannedStart, plannedEnd sql.NullString
		if err := fixture.DB.QueryRow(`SELECT occurrence_key,planned_date,planned_start_at,planned_end_at
			FROM domain_task_occurrences_v2 WHERE workspace_id='aggregate-w1' AND task_id='task-unscheduled'`).Scan(&key, &plannedDate, &plannedStart, &plannedEnd); err != nil {
			t.Fatal(err)
		}
		if key != "once" || plannedDate.Valid || plannedStart.Valid || plannedEnd.Valid {
			t.Fatalf("unexpected unscheduled occurrence key=%q date=%v start=%v end=%v", key, plannedDate, plannedStart, plannedEnd)
		}
	})

	t.Run("date_and_time_block_timing_are_persisted", func(t *testing.T) {
		dateSnapshot := dateAggregate("aggregate-w1", "task-date", "2026-07-22")
		if err := create("aggregate-w1", dateSnapshot); err != nil {
			t.Fatalf("create date aggregate: %v", err)
		}
		start := time.Date(2026, 7, 22, 1, 0, 0, 0, time.UTC)
		end := start.Add(time.Hour)
		timeSnapshot := timeBlockAggregate("aggregate-w1", "task-time", "2026-07-22", start, end)
		if err := create("aggregate-w1", timeSnapshot); err != nil {
			t.Fatalf("create time-block aggregate: %v", err)
		}
		assertAggregateCounts(t, fixture.DB, dateSnapshot.Task.ID, 1, 1, 1, 1)
		assertAggregateCounts(t, fixture.DB, timeSnapshot.Task.ID, 1, 1, 1, 1)
	})

	t.Run("recurring_initial_keys_are_idempotently_deduplicated", func(t *testing.T) {
		snapshot := recurringAggregate("aggregate-w1", "task-recurring")
		duplicate := snapshot.Occurrences[0]
		duplicate.ID = "occ-recurring-duplicate"
		snapshot.Occurrences = append(snapshot.Occurrences, duplicate)
		if err := create("aggregate-w1", snapshot); err != nil {
			t.Fatalf("create recurring aggregate: %v", err)
		}
		assertAggregateCounts(t, fixture.DB, snapshot.Task.ID, 1, 1, 1, 2)
	})

	t.Run("recurring_start_beyond_generation_window_allows_zero_occurrences", func(t *testing.T) {
		snapshot := recurringAggregate("aggregate-w1", "task-recurring-future")
		snapshot.Versions[0].StartsOn = "2027-01-01"
		snapshot.Versions[0].EffectiveFrom = "2026-07-22"
		snapshot.Occurrences = nil
		if err := create("aggregate-w1", snapshot); err != nil {
			t.Fatalf("create future recurring aggregate: %v", err)
		}
		assertAggregateCounts(t, fixture.DB, snapshot.Task.ID, 1, 1, 1, 0)
	})

	t.Run("late_occurrence_failure_rolls_back_all_four_record_types", func(t *testing.T) {
		snapshot := recurringAggregate("aggregate-w1", "task-rollback")
		snapshot.Occurrences[1].NoteID = "missing-note"
		if err := create("aggregate-w1", snapshot); err == nil {
			t.Fatal("missing occurrence note must fail")
		}
		assertAggregateCounts(t, fixture.DB, snapshot.Task.ID, 0, 0, 0, 0)
	})

	t.Run("task_schedule_and_occurrence_revisions_are_independent", func(t *testing.T) {
		mustExec(t, fixture.DB, `UPDATE domain_task_occurrences_v2 SET revision=revision+1
			WHERE workspace_id='aggregate-w1' AND task_id='task-unscheduled' AND occurrence_key='once'`)
		var taskRevision, scheduleRevision, occurrenceRevision, generatedRevision int64
		if err := fixture.DB.QueryRow(`SELECT task.revision,schedule.revision,occurrence.revision,occurrence.generated_schedule_revision
			FROM domain_tasks_v2 task
			JOIN domain_task_schedules_v2 schedule ON schedule.workspace_id=task.workspace_id AND schedule.task_id=task.id
			JOIN domain_task_occurrences_v2 occurrence ON occurrence.workspace_id=task.workspace_id AND occurrence.task_id=task.id
			WHERE task.workspace_id='aggregate-w1' AND task.id='task-unscheduled'`).Scan(
			&taskRevision, &scheduleRevision, &occurrenceRevision, &generatedRevision,
		); err != nil {
			t.Fatal(err)
		}
		if taskRevision != 1 || scheduleRevision != 1 || occurrenceRevision != 2 || generatedRevision != 1 {
			t.Fatalf("revisions task=%d schedule=%d occurrence=%d generated=%d", taskRevision, scheduleRevision, occurrenceRevision, generatedRevision)
		}
	})

	t.Run("cross_project_and_cross_workspace_roadmap_links_are_rejected", func(t *testing.T) {
		seedAggregateRoadmaps(t, fixture.DB)
		crossProject := unscheduledAggregate("aggregate-w1", "task-cross-project")
		crossProject.Task.RoadmapNodeID = "node-learning-w1"
		if err := create("aggregate-w1", crossProject); err == nil {
			t.Fatal("cross-project roadmap link must fail")
		}
		assertAggregateCounts(t, fixture.DB, crossProject.Task.ID, 0, 0, 0, 0)

		crossWorkspace := unscheduledAggregate("aggregate-w1", "task-cross-workspace")
		crossWorkspace.Task.RoadmapNodeID = "node-learning-w2"
		if err := create("aggregate-w1", crossWorkspace); err == nil {
			t.Fatal("cross-workspace roadmap link must fail")
		}
		assertAggregateCounts(t, fixture.DB, crossWorkspace.Task.ID, 0, 0, 0, 0)
	})
}

func unscheduledAggregate(workspaceID, taskID string) taskdomain.TaskAggregateSnapshot {
	return taskdomain.TaskAggregateSnapshot{
		Task: taskdomain.TaskRecord{
			WorkspaceID: workspaceID, ID: taskID, ProjectID: taskdomain.PersonalProjectID,
			Title: taskID, LifecycleStatus: taskdomain.TaskLifecycleActive, Revision: 1,
		},
		Schedule: taskdomain.ScheduleHeader{WorkspaceID: workspaceID, TaskID: taskID, Revision: 1, CurrentScheduleRevision: 1},
		Versions: []taskdomain.ScheduleVersion{{
			WorkspaceID: workspaceID, TaskID: taskID, ScheduleRevision: 1,
			RecurrenceType: taskdomain.RecurrenceNone, TimingType: taskdomain.TimingUnscheduled,
			Timezone: "UTC", RecurrenceRule: `{}`,
		}},
		Occurrences: []taskdomain.OccurrenceRecord{{
			WorkspaceID: workspaceID, ID: "occ-" + taskID, TaskID: taskID, OccurrenceKey: "once",
			ExecutionStatus: taskdomain.ExecutionStatusOpen, Revision: 1, GeneratedScheduleRevision: 1,
		}},
	}
}

func dateAggregate(workspaceID, taskID, date string) taskdomain.TaskAggregateSnapshot {
	snapshot := unscheduledAggregate(workspaceID, taskID)
	snapshot.Versions[0].TimingType = taskdomain.TimingDate
	snapshot.Versions[0].StartsOn = date
	snapshot.Occurrences[0].PlannedDate = date
	return snapshot
}

func timeBlockAggregate(workspaceID, taskID, date string, start, end time.Time) taskdomain.TaskAggregateSnapshot {
	snapshot := dateAggregate(workspaceID, taskID, date)
	snapshot.Versions[0].TimingType = taskdomain.TimingTimeBlock
	snapshot.Versions[0].LocalStartTime = "01:00:00"
	snapshot.Versions[0].DurationMinutes = 60
	snapshot.Occurrences[0].PlannedStartAt = &start
	snapshot.Occurrences[0].PlannedEndAt = &end
	return snapshot
}

func recurringAggregate(workspaceID, taskID string) taskdomain.TaskAggregateSnapshot {
	snapshot := dateAggregate(workspaceID, taskID, "2026-07-22")
	snapshot.Versions[0].RecurrenceType = taskdomain.RecurrenceDaily
	snapshot.Versions[0].EffectiveFrom = "2026-07-22"
	snapshot.Versions[0].RecurrenceRule = `{"interval":1}`
	snapshot.Occurrences = []taskdomain.OccurrenceRecord{
		{WorkspaceID: workspaceID, ID: "occ-" + taskID + "-1", TaskID: taskID, OccurrenceKey: "2026-07-22", PlannedDate: "2026-07-22", ExecutionStatus: taskdomain.ExecutionStatusOpen, Revision: 1, GeneratedScheduleRevision: 1},
		{WorkspaceID: workspaceID, ID: "occ-" + taskID + "-2", TaskID: taskID, OccurrenceKey: "2026-07-23", PlannedDate: "2026-07-23", ExecutionStatus: taskdomain.ExecutionStatusOpen, Revision: 1, GeneratedScheduleRevision: 1},
	}
	return snapshot
}

func assertAggregateCounts(t *testing.T, db *sql.DB, taskID string, tasks, schedules, versions, occurrences int) {
	t.Helper()
	queries := []struct {
		query string
		want  int
	}{
		{`SELECT COUNT(*) FROM domain_tasks_v2 WHERE id='` + taskID + `'`, tasks},
		{`SELECT COUNT(*) FROM domain_task_schedules_v2 WHERE task_id='` + taskID + `'`, schedules},
		{`SELECT COUNT(*) FROM domain_task_schedule_versions_v2 WHERE task_id='` + taskID + `'`, versions},
		{`SELECT COUNT(*) FROM domain_task_occurrences_v2 WHERE task_id='` + taskID + `'`, occurrences},
	}
	for _, check := range queries {
		var got int
		if err := db.QueryRow(check.query).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != check.want {
			t.Fatalf("query %q count=%d want=%d", check.query, got, check.want)
		}
	}
}

func seedAggregateRoadmaps(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, workspaceID := range []string{"aggregate-w1", "aggregate-w2"} {
		suffix := "w1"
		if workspaceID == "aggregate-w2" {
			suffix = "w2"
		}
		mustExec(t, db, fmt.Sprintf(`INSERT INTO domain_projects_v2
			(workspace_id,id,name,kind,horizon,status,revision,created_at,updated_at)
			VALUES ('%s','learning-%s','Learning %s','learning','long','active',1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, workspaceID, suffix, suffix))
		mustExec(t, db, fmt.Sprintf(`INSERT INTO domain_learning_roadmaps_v2
			(workspace_id,id,project_id,status,revision,created_at,updated_at)
			VALUES ('%s','roadmap-%s','learning-%s','active',1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, workspaceID, suffix, suffix))
		mustExec(t, db, fmt.Sprintf(`INSERT INTO domain_roadmap_nodes_v2
			(workspace_id,id,project_id,roadmap_id,title,node_type,status,position,revision,created_at,updated_at)
			VALUES ('%s','node-learning-%s','learning-%s','roadmap-%s','Node','topic','available',0,1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, workspaceID, suffix, suffix, suffix))
	}
}
