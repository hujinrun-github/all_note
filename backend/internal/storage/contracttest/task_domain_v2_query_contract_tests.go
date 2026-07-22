package contracttest

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/taskdomain"
)

type TaskDomainV2QueryFixture struct {
	DB        *sql.DB
	Dialect   TaskDomainV2Dialect
	Writer    storage.TenantFencedWriter
	NewReader func(workspaceID string) taskdomain.TaskDomainReader
}

func RunTaskDomainV2QuerySuite(t *testing.T, fixture TaskDomainV2QueryFixture) {
	t.Helper()
	ctx := context.Background()
	for _, workspaceID := range []string{"query-w1", "query-w2"} {
		mustExec(t, fixture.DB, fmt.Sprintf(`INSERT INTO tenant_workspaces(workspace_id) VALUES('%s')`, workspaceID))
		if err := fixture.Writer.BeginFencedWrite(ctx, workspaceID, 1, func(tx storage.TenantWriteTx) error {
			return tx.TaskDomainWriter().EnsureSystemProjects(ctx)
		}); err != nil {
			t.Fatalf("ensure system projects for %s: %v", workspaceID, err)
		}
	}
	if err := fixture.Writer.BeginFencedWrite(ctx, "query-w1", 1, func(tx storage.TenantWriteTx) error {
		for _, project := range []taskdomain.Project{
			{WorkspaceID: "query-w1", ID: "query-other", Name: "Other", Kind: taskdomain.ProjectKindStandard,
				Horizon: taskdomain.ProjectHorizonShort, Status: taskdomain.ProjectStatusActive},
			{WorkspaceID: "query-w1", ID: "query-learning", Name: "Alpha Learning", Kind: taskdomain.ProjectKindLearning,
				Horizon: taskdomain.ProjectHorizonLong, Status: taskdomain.ProjectStatusPaused},
			{WorkspaceID: "query-w1", ID: "query-archived", Name: "Zulu Archive", Kind: taskdomain.ProjectKindStandard,
				Horizon: taskdomain.ProjectHorizonLong, Status: taskdomain.ProjectStatusArchived},
		} {
			if err := tx.TaskDomainWriter().SaveProject(ctx, taskdomain.ProjectWrite{Project: project}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("create query projects: %v", err)
	}

	start := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	create := func(snapshot taskdomain.TaskAggregateSnapshot) {
		t.Helper()
		if err := fixture.Writer.BeginFencedWrite(ctx, snapshot.Task.WorkspaceID, 1, func(tx storage.TenantWriteTx) error {
			return tx.TaskDomainWriter().CreateTaskAggregate(ctx, snapshot)
		}); err != nil {
			t.Fatalf("create %s: %v", snapshot.Task.ID, err)
		}
	}
	create(queryDateAggregate("query-w1", "date-a", taskdomain.PersonalProjectID, "2026-07-22", "2026-07-23"))
	create(queryDateAggregate("query-w1", "date-multi", taskdomain.PersonalProjectID, "2026-07-21", "2026-07-23"))
	create(queryTimeAggregate("query-w1", "time-a", taskdomain.PersonalProjectID, "2026-07-22", start, end))
	create(queryRecurringAggregate("query-w1", "future", taskdomain.PersonalProjectID))
	create(queryUnscheduledAggregate("query-w1", "overdue", taskdomain.PersonalProjectID))
	create(queryUnscheduledAggregate("query-w1", "done", taskdomain.PersonalProjectID))
	create(queryUnscheduledAggregate("query-w1", "unscheduled", taskdomain.PersonalProjectID))
	create(queryDateAggregate("query-w1", "other", "query-other", "2026-07-22", ""))
	create(queryCatalogAggregate("query-w1", "catalog-paused", "query-learning", taskdomain.TaskLifecyclePaused, 1))
	create(queryCatalogAggregate("query-w1", "catalog-draft", "query-learning", taskdomain.TaskLifecycleDraft, 2))
	create(queryDateAggregate("query-w2", "foreign-only", taskdomain.PersonalProjectID, "2026-07-22", ""))

	actualStart := time.Date(2026, 7, 22, 9, 5, 0, 0, time.UTC)
	duePast := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	completed := time.Date(2026, 7, 22, 8, 0, 0, 0, time.UTC)
	mustExecArgs(t, fixture.DB, `UPDATE domain_task_occurrences_v2
		SET override_title=?,override_description=?,location=?,calendar_kind=?,calendar_notes=?,execution_status='active',actual_start_at=?
		WHERE workspace_id='query-w1' AND id='time-a-occ'`, fixture.Dialect,
		"Overridden time title", "Occurrence detail", "Room A", "work", "Review", queryContractTime(fixture.Dialect, actualStart))
	mustExecArgs(t, fixture.DB, `UPDATE domain_task_occurrences_v2 SET due_at=?
		WHERE workspace_id='query-w1' AND id='overdue-occ'`, fixture.Dialect, queryContractTime(fixture.Dialect, duePast))
	mustExecArgs(t, fixture.DB, `UPDATE domain_task_occurrences_v2
		SET due_at=?,execution_status='done',completed_at=? WHERE workspace_id='query-w1' AND id='done-occ'`, fixture.Dialect,
		queryContractTime(fixture.Dialect, duePast), queryContractTime(fixture.Dialect, completed))

	reader := fixture.NewReader("query-w1")
	t.Run("project_and_task_catalog_filters_are_stable_and_workspace_bound", func(t *testing.T) {
		projects, err := reader.ListProjects(ctx, taskdomain.ProjectListFilter{})
		if err != nil {
			t.Fatalf("list projects: %v", err)
		}
		assertProjectSnapshotIDs(t, projects, "query-learning", taskdomain.SystemInboxProjectID, "query-other", taskdomain.PersonalProjectID, "query-archived")
		projectsAgain, err := reader.ListProjects(ctx, taskdomain.ProjectListFilter{})
		if err != nil || !projectSnapshotIDsEqual(projects, projectsAgain) {
			t.Fatalf("project order is not stable: first=%#v second=%#v err=%v", projects, projectsAgain, err)
		}
		learning := taskdomain.ProjectKindLearning
		long := taskdomain.ProjectHorizonLong
		paused := taskdomain.ProjectStatusPaused
		filteredProjects, err := reader.ListProjects(ctx, taskdomain.ProjectListFilter{Kind: &learning, Horizon: &long, Status: &paused})
		if err != nil {
			t.Fatalf("filter projects: %v", err)
		}
		assertProjectSnapshotIDs(t, filteredProjects, "query-learning")
		invalidKind := taskdomain.ProjectKind("foreign")
		if _, err := reader.ListProjects(ctx, taskdomain.ProjectListFilter{Kind: &invalidKind}); err != taskdomain.ErrInvalidProjectListFilter {
			t.Fatalf("invalid project filter error=%v", err)
		}

		tasks, err := reader.ListTaskDefinitions(ctx, taskdomain.TaskDefinitionListFilter{ProjectID: "query-learning"})
		if err != nil {
			t.Fatalf("list project task definitions: %v", err)
		}
		assertTaskDefinitionIDs(t, tasks, "catalog-paused", "catalog-draft")
		if tasks[0].ScheduleRevision != 1 || tasks[0].CurrentScheduleRevision != 1 || tasks[0].Task.Revision != 1 {
			t.Fatalf("incomplete task definition snapshot: %#v", tasks[0])
		}
		pausedLifecycle := taskdomain.TaskLifecyclePaused
		pausedTasks, err := reader.ListTaskDefinitions(ctx, taskdomain.TaskDefinitionListFilter{LifecycleStatus: &pausedLifecycle})
		if err != nil {
			t.Fatalf("filter task definitions: %v", err)
		}
		assertTaskDefinitionIDs(t, pausedTasks, "catalog-paused")
		invalidLifecycle := taskdomain.TaskLifecycleStatus("foreign")
		if _, err := reader.ListTaskDefinitions(ctx, taskdomain.TaskDefinitionListFilter{LifecycleStatus: &invalidLifecycle}); err != taskdomain.ErrInvalidTaskDefinitionListFilter {
			t.Fatalf("invalid task filter error=%v", err)
		}
		allTasks, err := reader.ListTaskDefinitions(ctx, taskdomain.TaskDefinitionListFilter{})
		if err != nil {
			t.Fatalf("list all task definitions: %v", err)
		}
		for _, item := range allTasks {
			if item.Task.WorkspaceID != "query-w1" || item.Task.ID == "foreign-only" {
				t.Fatalf("task catalog workspace leak: %#v", item)
			}
		}
	})

	t.Run("get_aggregate_and_occurrence_return_command_and_query_revisions", func(t *testing.T) {
		aggregate, err := reader.GetTaskAggregate(ctx, "future")
		if err != nil {
			t.Fatalf("get aggregate: %v", err)
		}
		if aggregate.Task.ID != "future" || aggregate.Task.Revision != 1 || aggregate.Schedule.Revision != 1 ||
			aggregate.Schedule.CurrentScheduleRevision != 1 || len(aggregate.Versions) != 1 || len(aggregate.Occurrences) != 2 ||
			!aggregate.Aggregate.Recurring || len(aggregate.Aggregate.Occurrences) != 2 {
			t.Fatalf("incomplete aggregate: %#v", aggregate)
		}
		occurrence, err := reader.GetOccurrence(ctx, "time-a-occ")
		if err != nil {
			t.Fatalf("get occurrence: %v", err)
		}
		if occurrence.WorkspaceID != "query-w1" || occurrence.Title != "Overridden time title" || occurrence.Description != "Occurrence detail" ||
			occurrence.Location != "Room A" || occurrence.CalendarKind != "work" || occurrence.CalendarNotes != "Review" ||
			occurrence.ProjectRevision != 1 || occurrence.TaskRevision != 1 || occurrence.ScheduleRevision != 1 || occurrence.GeneratedScheduleRevision != 1 ||
			occurrence.Revision != 1 || occurrence.ActualStartAt == nil || !occurrence.ActualStartAt.Equal(actualStart) {
			t.Fatalf("incomplete occurrence query snapshot: %#v", occurrence)
		}
		items, err := reader.ListTaskOccurrences(ctx, "future")
		if err != nil || len(items) != 2 || items[0].OccurrenceID != "future-1" || items[1].OccurrenceID != "future-2" {
			t.Fatalf("stable task occurrences = %#v, err=%v", items, err)
		}
	})

	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	dayStart := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	dayEnd := dayStart.Add(24 * time.Hour)
	t.Run("named_scopes_and_composable_filters_are_half_open", func(t *testing.T) {
		allForTask := queryList(t, reader, taskdomain.OccurrenceListFilter{Scope: taskdomain.OccurrenceListAll, TaskID: "future"})
		assertQueryIDs(t, allForTask, "future-1", "future-2")
		allForProject := queryList(t, reader, taskdomain.OccurrenceListFilter{Scope: taskdomain.OccurrenceListAll, ProjectID: "query-other"})
		assertQueryIDs(t, allForProject, "other-occ")

		today := queryList(t, reader, taskdomain.OccurrenceListFilter{Scope: taskdomain.OccurrenceListToday, From: dayStart, To: dayEnd, Timezone: "UTC"})
		assertQueryIDs(t, today, "date-multi-occ", "date-a-occ", "other-occ", "time-a-occ")

		upcoming := queryList(t, reader, taskdomain.OccurrenceListFilter{
			Scope: taskdomain.OccurrenceListUpcoming, From: now, To: dayStart.Add(72 * time.Hour), Timezone: "UTC",
			ProjectID: taskdomain.PersonalProjectID, Statuses: []taskdomain.ExecutionStatus{taskdomain.ExecutionStatusOpen}, Recurring: queryBool(true),
		})
		assertQueryIDs(t, upcoming, "future-1", "future-2")

		overdue := queryList(t, reader, taskdomain.OccurrenceListFilter{Scope: taskdomain.OccurrenceListOverdue, From: now, Timezone: "UTC"})
		assertQueryIDs(t, overdue, "overdue-occ")
		unscheduled := queryList(t, reader, taskdomain.OccurrenceListFilter{
			Scope: taskdomain.OccurrenceListUnscheduled, Timezone: "UTC", Statuses: []taskdomain.ExecutionStatus{taskdomain.ExecutionStatusOpen},
		})
		assertQueryIDs(t, unscheduled, "overdue-occ", "catalog-draft-occ", "catalog-paused-occ", "unscheduled-occ")
		completedItems := queryList(t, reader, taskdomain.OccurrenceListFilter{
			Scope: taskdomain.OccurrenceListCompleted, From: dayStart, To: dayEnd, Timezone: "UTC",
		})
		assertQueryIDs(t, completedItems, "done-occ")

		calendar := queryList(t, reader, taskdomain.OccurrenceListFilter{Scope: taskdomain.OccurrenceListCalendar, From: dayStart, To: dayEnd, Timezone: "UTC"})
		assertQueryIDs(t, calendar, "date-multi-occ", "date-a-occ", "other-occ", "time-a-occ")
		for _, item := range calendar {
			if item.TimingType == taskdomain.TimingUnscheduled {
				t.Fatalf("calendar leaked unscheduled occurrence: %#v", item)
			}
		}
	})

	t.Run("reader_is_workspace_bound", func(t *testing.T) {
		if _, err := reader.GetTaskAggregate(ctx, "foreign-only"); err != taskdomain.ErrTaskNotFound {
			t.Fatalf("foreign task error = %v, want %v", err, taskdomain.ErrTaskNotFound)
		}
		if _, err := reader.GetOccurrence(ctx, "foreign-only-occ"); err != taskdomain.ErrOccurrenceNotFound {
			t.Fatalf("foreign occurrence error = %v, want %v", err, taskdomain.ErrOccurrenceNotFound)
		}
		all := queryList(t, reader, taskdomain.OccurrenceListFilter{Scope: taskdomain.OccurrenceListToday, From: dayStart, To: dayEnd, Timezone: "UTC"})
		for _, item := range all {
			if item.WorkspaceID != "query-w1" || item.TaskID == "foreign-only" {
				t.Fatalf("workspace leak: %#v", item)
			}
		}
	})

	t.Run("critical_range_predicates_have_usable_indexes", func(t *testing.T) {
		assertTaskDomainV2QueryIndex(t, fixture, "domain_task_occurrences_v2_due_open_idx",
			`SELECT id FROM domain_task_occurrences_v2 WHERE workspace_id='query-w1' AND due_at < '2026-07-22T12:00:00Z' AND execution_status NOT IN ('done','skipped','cancelled') ORDER BY due_at`)
		assertTaskDomainV2QueryIndex(t, fixture, "domain_task_occurrences_v2_start_idx",
			`SELECT id FROM domain_task_occurrences_v2 WHERE workspace_id='query-w1' AND planned_start_at >= '2026-07-22T00:00:00Z' AND planned_start_at < '2026-07-23T00:00:00Z' ORDER BY planned_start_at`)
		assertTaskDomainV2QueryIndex(t, fixture, "domain_task_occurrences_v2_date_idx",
			`SELECT id FROM domain_task_occurrences_v2 WHERE workspace_id='query-w1' AND planned_date >= '2026-07-22' AND planned_date < '2026-07-23' ORDER BY planned_date`)
		assertTaskDomainV2QueryIndex(t, fixture, "domain_task_occurrences_v2_completed_idx",
			`SELECT id FROM domain_task_occurrences_v2 WHERE workspace_id='query-w1' AND completed_at >= '2026-07-22T00:00:00Z' AND completed_at < '2026-07-23T00:00:00Z' AND execution_status='done' ORDER BY completed_at`)
	})

	t.Run("task_attribute_after_image_is_saved_by_the_same_task_cas", func(t *testing.T) {
		current, err := reader.GetTaskAggregate(ctx, "catalog-draft")
		if err != nil {
			t.Fatalf("load task before attribute patch: %v", err)
		}
		rejectedTask := current.Task
		rejectedTask.ProjectID = "query-archived"
		rejectedTask.Title = "must roll back"
		rejectedTask.Revision++
		rejectedAggregate := current.Aggregate
		rejectedAggregate.Revision++
		rejectedWrite := taskdomain.TaskAggregateWrite{
			Task: &rejectedTask, Aggregate: rejectedAggregate,
			ExpectedRevisions:        taskdomain.AggregateExpectedRevisions{Task: current.Task.Revision, Occurrences: map[string]int64{}},
			ExpectedScheduleRevision: current.Schedule.Revision,
		}
		err = fixture.Writer.BeginFencedWrite(ctx, "query-w1", 1, func(tx storage.TenantWriteTx) error {
			return tx.TaskDomainWriter().SaveTaskAggregate(ctx, rejectedWrite)
		})
		if err != taskdomain.ErrInvalidTaskAggregateSnapshot {
			t.Fatalf("move open task to archived project error=%v", err)
		}
		afterRejected, err := reader.GetTaskAggregate(ctx, "catalog-draft")
		if err != nil || afterRejected.Task.ProjectID != "query-learning" || afterRejected.Task.Title != current.Task.Title || afterRejected.Task.Revision != 1 {
			t.Fatalf("rejected target project changed task: %#v err=%v", afterRejected.Task, err)
		}

		for _, invalidReference := range []struct {
			name   string
			mutate func(*taskdomain.TaskRecord)
		}{
			{name: "missing task note", mutate: func(task *taskdomain.TaskRecord) { task.NoteID = "missing-note" }},
			{name: "missing roadmap node", mutate: func(task *taskdomain.TaskRecord) { task.RoadmapNodeID = "missing-node" }},
		} {
			t.Run(invalidReference.name, func(t *testing.T) {
				invalidTask := current.Task
				invalidReference.mutate(&invalidTask)
				invalidTask.Revision++
				invalidAggregate := current.Aggregate
				invalidAggregate.Revision++
				invalidWrite := taskdomain.TaskAggregateWrite{
					Task: &invalidTask, Aggregate: invalidAggregate,
					ExpectedRevisions:        taskdomain.AggregateExpectedRevisions{Task: current.Task.Revision, Occurrences: map[string]int64{}},
					ExpectedScheduleRevision: current.Schedule.Revision,
				}
				err := fixture.Writer.BeginFencedWrite(ctx, "query-w1", 1, func(tx storage.TenantWriteTx) error {
					return tx.TaskDomainWriter().SaveTaskAggregate(ctx, invalidWrite)
				})
				if err != taskdomain.ErrInvalidTaskAggregateSnapshot {
					t.Fatalf("invalid %s error=%v, want %v", invalidReference.name, err, taskdomain.ErrInvalidTaskAggregateSnapshot)
				}
			})
		}
		afterInvalidReferences, err := reader.GetTaskAggregate(ctx, "catalog-draft")
		if err != nil || afterInvalidReferences.Task != current.Task {
			t.Fatalf("invalid references changed task: %#v err=%v", afterInvalidReferences.Task, err)
		}

		task := current.Task
		task.ProjectID = "query-other"
		task.Title = "Patched catalog task"
		task.Description = "patched description"
		task.Priority = 3
		task.SortOrder = -5
		task.Revision++
		next := current.Aggregate
		next.Revision++
		write := taskdomain.TaskAggregateWrite{
			Task: &task, Aggregate: next,
			ExpectedRevisions:        taskdomain.AggregateExpectedRevisions{Task: current.Task.Revision, Occurrences: map[string]int64{}},
			ExpectedScheduleRevision: current.Schedule.Revision,
		}
		if err := fixture.Writer.BeginFencedWrite(ctx, "query-w1", 1, func(tx storage.TenantWriteTx) error {
			return tx.TaskDomainWriter().SaveTaskAggregate(ctx, write)
		}); err != nil {
			t.Fatalf("save task attribute after-image: %v", err)
		}
		updated, err := reader.GetTaskAggregate(ctx, "catalog-draft")
		if err != nil {
			t.Fatalf("reload patched task: %v", err)
		}
		if updated.Task.ProjectID != "query-other" || updated.Task.Title != task.Title || updated.Task.Description != task.Description ||
			updated.Task.Priority != 3 || updated.Task.SortOrder != -5 || updated.Task.Revision != 2 || updated.Aggregate.Revision != 2 {
			t.Fatalf("task attribute patch was incomplete: %#v", updated.Task)
		}
		moved, err := reader.ListTaskDefinitions(ctx, taskdomain.TaskDefinitionListFilter{ProjectID: "query-other"})
		if err != nil {
			t.Fatalf("list moved task: %v", err)
		}
		assertTaskDefinitionIDs(t, moved, "catalog-draft", "other")

		staleTask := updated.Task
		staleTask.Title = "must not persist"
		stale := updated.Aggregate
		staleWrite := taskdomain.TaskAggregateWrite{
			Task: &staleTask, Aggregate: stale,
			ExpectedRevisions:        taskdomain.AggregateExpectedRevisions{Task: 1, Occurrences: map[string]int64{}},
			ExpectedScheduleRevision: updated.Schedule.Revision,
		}
		err = fixture.Writer.BeginFencedWrite(ctx, "query-w1", 1, func(tx storage.TenantWriteTx) error {
			return tx.TaskDomainWriter().SaveTaskAggregate(ctx, staleWrite)
		})
		if err != taskdomain.ErrAggregateRevisionConflict {
			t.Fatalf("stale task attribute CAS error=%v", err)
		}
		afterStale, err := reader.GetTaskAggregate(ctx, "catalog-draft")
		if err != nil || afterStale.Task.Title != task.Title || afterStale.Task.Revision != 2 {
			t.Fatalf("stale task attribute write changed state: %#v err=%v", afterStale.Task, err)
		}
	})
}

func queryList(t *testing.T, reader taskdomain.TaskDomainReader, filter taskdomain.OccurrenceListFilter) []taskdomain.QueryOccurrenceSnapshot {
	t.Helper()
	items, err := reader.ListOccurrences(context.Background(), filter)
	if err != nil {
		t.Fatalf("list %s occurrences: %v", filter.Scope, err)
	}
	return items
}

func assertQueryIDs(t *testing.T, items []taskdomain.QueryOccurrenceSnapshot, expected ...string) {
	t.Helper()
	actual := make([]string, len(items))
	for index := range items {
		actual[index] = items[index].OccurrenceID
	}
	if strings.Join(actual, ",") != strings.Join(expected, ",") {
		t.Fatalf("occurrence ids = %v, want %v", actual, expected)
	}
}

func assertProjectSnapshotIDs(t *testing.T, items []taskdomain.ProjectSnapshot, expected ...string) {
	t.Helper()
	actual := make([]string, len(items))
	for index := range items {
		actual[index] = items[index].Project.ID
		if items[index].Revision < 1 {
			t.Fatalf("project %s has invalid revision %d", actual[index], items[index].Revision)
		}
	}
	if strings.Join(actual, ",") != strings.Join(expected, ",") {
		t.Fatalf("project ids = %v, want %v", actual, expected)
	}
}

func projectSnapshotIDsEqual(left, right []taskdomain.ProjectSnapshot) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].Project.ID != right[index].Project.ID {
			return false
		}
	}
	return true
}

func assertTaskDefinitionIDs(t *testing.T, items []taskdomain.TaskDefinitionSnapshot, expected ...string) {
	t.Helper()
	actual := make([]string, len(items))
	for index := range items {
		actual[index] = items[index].Task.ID
	}
	if strings.Join(actual, ",") != strings.Join(expected, ",") {
		t.Fatalf("task definition ids = %v, want %v", actual, expected)
	}
}

func queryBool(value bool) *bool { return &value }

func queryContractTime(dialect TaskDomainV2Dialect, value time.Time) any {
	if dialect == TaskDomainV2Postgres {
		return value
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func mustExecArgs(t *testing.T, db *sql.DB, sqliteStatement string, dialect TaskDomainV2Dialect, args ...any) {
	t.Helper()
	statement := sqliteStatement
	if dialect == TaskDomainV2Postgres {
		position := 0
		for strings.Contains(statement, "?") {
			position++
			statement = strings.Replace(statement, "?", fmt.Sprintf("$%d", position), 1)
		}
	}
	if _, err := db.Exec(statement, args...); err != nil {
		t.Fatalf("exec query fixture statement: %v\n%s", err, statement)
	}
}

func assertTaskDomainV2QueryIndex(t *testing.T, fixture TaskDomainV2QueryFixture, indexName, statement string) {
	t.Helper()
	var rows *sql.Rows
	var err error
	if fixture.Dialect == TaskDomainV2Postgres {
		if _, err = fixture.DB.Exec(`SET enable_seqscan = off`); err != nil {
			t.Fatalf("disable postgres seqscan for plan assertion: %v", err)
		}
		rows, err = fixture.DB.Query("EXPLAIN " + statement)
	} else {
		rows, err = fixture.DB.Query("EXPLAIN QUERY PLAN " + statement)
	}
	if err != nil {
		t.Fatalf("explain query: %v", err)
	}
	defer rows.Close()
	var plan strings.Builder
	columns, _ := rows.Columns()
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for index := range values {
			pointers[index] = &values[index]
		}
		if err := rows.Scan(pointers...); err != nil {
			t.Fatalf("scan explain: %v", err)
		}
		for _, value := range values {
			fmt.Fprint(&plan, value, " ")
		}
	}
	if !strings.Contains(plan.String(), indexName) {
		t.Fatalf("query plan does not use %s: %s", indexName, plan.String())
	}
}

func queryUnscheduledAggregate(workspaceID, taskID, projectID string) taskdomain.TaskAggregateSnapshot {
	return queryAggregate(workspaceID, taskID, projectID, taskdomain.ScheduleVersion{
		RecurrenceType: taskdomain.RecurrenceNone, TimingType: taskdomain.TimingUnscheduled, Timezone: "UTC", RecurrenceRule: `{}`,
	}, []taskdomain.OccurrenceRecord{{ID: taskID + "-occ", OccurrenceKey: "once"}})
}

func queryCatalogAggregate(workspaceID, taskID, projectID string, lifecycle taskdomain.TaskLifecycleStatus, sortOrder float64) taskdomain.TaskAggregateSnapshot {
	snapshot := queryUnscheduledAggregate(workspaceID, taskID, projectID)
	snapshot.Task.LifecycleStatus = lifecycle
	snapshot.Task.SortOrder = sortOrder
	return snapshot
}

func queryDateAggregate(workspaceID, taskID, projectID, date, allDayEnd string) taskdomain.TaskAggregateSnapshot {
	return queryAggregate(workspaceID, taskID, projectID, taskdomain.ScheduleVersion{
		RecurrenceType: taskdomain.RecurrenceNone, TimingType: taskdomain.TimingDate, Timezone: "UTC", StartsOn: date, RecurrenceRule: `{}`,
	}, []taskdomain.OccurrenceRecord{{ID: taskID + "-occ", OccurrenceKey: "once", PlannedDate: date, AllDayEndDate: allDayEnd}})
}

func queryTimeAggregate(workspaceID, taskID, projectID, date string, start, end time.Time) taskdomain.TaskAggregateSnapshot {
	return queryAggregate(workspaceID, taskID, projectID, taskdomain.ScheduleVersion{
		RecurrenceType: taskdomain.RecurrenceNone, TimingType: taskdomain.TimingTimeBlock, Timezone: "UTC", StartsOn: date,
		LocalStartTime: "09:00", DurationMinutes: 60, RecurrenceRule: `{}`,
	}, []taskdomain.OccurrenceRecord{{ID: taskID + "-occ", OccurrenceKey: "once", PlannedDate: date, PlannedStartAt: &start, PlannedEndAt: &end}})
}

func queryRecurringAggregate(workspaceID, taskID, projectID string) taskdomain.TaskAggregateSnapshot {
	firstStart := time.Date(2026, 7, 23, 9, 0, 0, 0, time.UTC)
	firstEnd := firstStart.Add(time.Hour)
	secondStart := firstStart.Add(24 * time.Hour)
	secondEnd := secondStart.Add(time.Hour)
	return queryAggregate(workspaceID, taskID, projectID, taskdomain.ScheduleVersion{
		EffectiveFrom: "2026-07-22", RecurrenceType: taskdomain.RecurrenceDaily, TimingType: taskdomain.TimingTimeBlock,
		Timezone: "UTC", StartsOn: "2026-07-23", EndsOn: "2026-07-24", LocalStartTime: "09:00", DurationMinutes: 60,
		RecurrenceRule: `{"interval":1}`,
	}, []taskdomain.OccurrenceRecord{
		{ID: "future-1", OccurrenceKey: "2026-07-23", PlannedDate: "2026-07-23", PlannedStartAt: &firstStart, PlannedEndAt: &firstEnd},
		{ID: "future-2", OccurrenceKey: "2026-07-24", PlannedDate: "2026-07-24", PlannedStartAt: &secondStart, PlannedEndAt: &secondEnd},
	})
}

func queryAggregate(workspaceID, taskID, projectID string, version taskdomain.ScheduleVersion, occurrences []taskdomain.OccurrenceRecord) taskdomain.TaskAggregateSnapshot {
	version.WorkspaceID = workspaceID
	version.TaskID = taskID
	version.ScheduleRevision = 1
	for index := range occurrences {
		occurrences[index].WorkspaceID = workspaceID
		occurrences[index].TaskID = taskID
		occurrences[index].ExecutionStatus = taskdomain.ExecutionStatusOpen
		occurrences[index].Revision = 1
		occurrences[index].GeneratedScheduleRevision = 1
	}
	return taskdomain.TaskAggregateSnapshot{
		Task:     taskdomain.TaskRecord{WorkspaceID: workspaceID, ID: taskID, ProjectID: projectID, Title: taskID + " title", LifecycleStatus: taskdomain.TaskLifecycleActive, Revision: 1},
		Schedule: taskdomain.ScheduleHeader{WorkspaceID: workspaceID, TaskID: taskID, Revision: 1, CurrentScheduleRevision: 1},
		Versions: []taskdomain.ScheduleVersion{version}, Occurrences: occurrences,
	}
}
