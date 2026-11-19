package contracttest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/taskdomain"
	"github.com/hujinrun/flowspace/internal/workspacequery"
)

type WorkspaceQueryFixture struct {
	DB        *sql.DB
	Dialect   TaskDomainV2Dialect
	NewReader func(string) workspacequery.Reader
}

// RunWorkspaceQuerySuite runs identical business/SQL assertions against both
// adapters, including independently scoped workspaces with overlapping IDs.
func RunWorkspaceQuerySuite(t *testing.T, fixture WorkspaceQueryFixture) {
	t.Helper()
	ctx := t.Context()
	for _, workspace := range []string{"overview-a", "overview-b", "overview-dst", "overview-states", "overview-next"} {
		workspaceQueryExec(t, fixture, fixture.DB, `INSERT INTO tenant_workspaces(workspace_id) VALUES (?)`, workspace)
	}
	for _, workspace := range []string{"overview-a", "overview-b"} {
		workspaceQueryProject(t, fixture, workspace, "main", "Main project", "active", "")
		workspaceQueryProject(t, fixture, workspace, "inbox", "Inbox", "active", "inbox")
	}
	seed := func(id, lifecycle, timing, recurrence string, items ...workspaceQueryOccurrence) {
		workspaceQueryTask(t, fixture, "overview-a", "main", id, lifecycle, timing, recurrence, items)
	}
	seed("normal", "active", "date", "none", workspaceQueryOccurrence{ID: "normal-occ", Date: "2026-11-19", Status: "open"})
	seed("blocked", "active", "time_block", "none", workspaceQueryOccurrence{ID: "blocked-occ", Date: "2026-11-19", Start: "2026-11-19T09:00:00Z", End: "2026-11-19T10:00:00Z", Status: "blocked"})
	seed("repeat", "active", "date", "daily",
		workspaceQueryOccurrence{ID: "recurring-today", Date: "2026-11-19", Status: "open"},
		workspaceQueryOccurrence{ID: "recurring-next", Date: "2026-11-20", Status: "open"},
		workspaceQueryOccurrence{ID: "recurring-done", Date: "2026-11-18", Status: "done", Completed: "2026-11-19T01:00:00.500Z"})
	seed("finished", "completed", "date", "none", workspaceQueryOccurrence{ID: "finished-occ", Date: "2026-11-18", Status: "done", Completed: "2026-11-19T02:00:00Z"})
	seed("overdue", "active", "date", "none", workspaceQueryOccurrence{ID: "overdue-occ", Date: "2026-11-17", Due: "2026-11-18T12:00:00Z", Status: "open"})
	seed("draft", "draft", "date", "none")
	seed("cancelled", "cancelled", "date", "none", workspaceQueryOccurrence{ID: "cancelled-occ", Date: "2026-11-19", Status: "open"})
	seed("archived", "archived", "date", "none", workspaceQueryOccurrence{ID: "archived-occ", Date: "2026-11-19", Status: "open"})
	workspaceQueryTask(t, fixture, "overview-a", "inbox", "captured", "active", "unscheduled", "none", []workspaceQueryOccurrence{{ID: "captured-occ", Status: "open"}})
	workspaceQueryTask(t, fixture, "overview-b", "main", "normal", "active", "date", "none", []workspaceQueryOccurrence{{ID: "normal-occ", Date: "2026-11-19", Status: "open"}})
	window := workspaceQueryWindow(t, "2026-11-19", "UTC")
	request := workspacequery.Request{Window: window, ExpectedEpoch: 1, TodayLimit: 8, ProjectLimit: 3}

	t.Run("overview_counts_canonical_instances_and_keeps_task_progress_separate", func(t *testing.T) {
		result, err := fixture.NewReader("overview-a").Overview(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		want := workspacequery.Facets{Today: 3, Week: 5, Month: 5, Overdue: 1, Unscheduled: 1, Inbox: 1, CompletedToday: 2, CompletedWeek: 2, CompletedTotal: 2, Draft: 1, Recurring: 2}
		if result.Facets != want {
			t.Fatalf("facets=%+v want=%+v", result.Facets, want)
		}
		if len(result.Today) != 3 || result.Today[0].OccurrenceID != "blocked-occ" {
			t.Fatalf("today must put timed execution first: %#v", result.Today)
		}
		if result.ProjectNames["main"] != "Main project" {
			t.Fatalf("project names=%#v", result.ProjectNames)
		}
		if len(result.Projects) != 1 {
			t.Fatalf("overview projects=%#v", result.Projects)
		}
		p := result.Projects[0]
		if p.CompletedTaskCount != 1 || p.TotalActionableTaskCount != 5 || p.OpenOccurrenceCount != 5 || p.BlockedOccurrenceCount != 1 || !p.HasOpenEndedRecurrence {
			t.Fatalf("project summary=%+v", p)
		}
		if p.NextOccurrence == nil || p.NextOccurrence.WorkspaceID != "overview-a" || p.NextOccurrence.ProjectID != "main" || p.NextOccurrence.PlannedDate != "2026-11-19" {
			t.Fatalf("next=%#v", p.NextOccurrence)
		}
	})

	t.Run("bounds_sections_and_uses_workspace_scope_not_shared_entity_ids", func(t *testing.T) {
		limited := request
		limited.TodayLimit = 1
		limited.ProjectLimit = 0
		result, err := fixture.NewReader("overview-a").Overview(ctx, limited)
		if err != nil || len(result.Today) != 1 || len(result.Projects) != 0 || result.Facets.Today != 3 || result.ProjectNames["main"] != "Main project" {
			t.Fatalf("bounded result=%+v err=%v", result, err)
		}
		limited.TodayLimit = 0
		result, err = fixture.NewReader("overview-a").Overview(ctx, limited)
		if err != nil || len(result.Today) != 0 || result.Facets.Today != 3 {
			t.Fatalf("facets-only result=%+v err=%v", result, err)
		}
		other, err := fixture.NewReader("overview-b").Overview(ctx, request)
		if err != nil || other.Facets.Today != 1 || len(other.Today) != 1 || other.Today[0].WorkspaceID != "overview-b" || other.Projects[0].TotalActionableTaskCount != 1 {
			t.Fatalf("other workspace=%+v err=%v", other, err)
		}
	})

	t.Run("project_scope_catalog_and_missing_identity", func(t *testing.T) {
		filtered := request
		filtered.ProjectID = "inbox"
		result, err := fixture.NewReader("overview-a").Overview(ctx, filtered)
		if err != nil || result.Facets.Inbox != 1 || result.Facets.Today != 0 || result.Facets.Draft != 0 {
			t.Fatalf("project facets=%+v err=%v", result, err)
		}
		filtered.ProjectID = "missing"
		if _, err := fixture.NewReader("overview-a").Overview(ctx, filtered); !errors.Is(err, taskdomain.ErrProjectNotFound) {
			t.Fatalf("missing project err=%v", err)
		}
		catalog, err := fixture.NewReader("overview-a").ProjectSummaries(ctx, workspacequery.ProjectRequest{Window: window, ExpectedEpoch: 1})
		if err != nil || len(catalog) != 2 {
			t.Fatalf("full catalog=%#v err=%v", catalog, err)
		}
		if _, err := fixture.NewReader("overview-a").ProjectOverview(ctx, workspacequery.ProjectRequest{Window: window, ExpectedEpoch: 1, ProjectID: "missing"}); !errors.Is(err, taskdomain.ErrProjectNotFound) {
			t.Fatalf("project overview err=%v", err)
		}
	})

	t.Run("timezone_dst_overlap_and_fractional_boundary_completion", func(t *testing.T) {
		workspaceQueryProject(t, fixture, "overview-dst", "main", "DST project", "active", "")
		items := []workspaceQueryOccurrence{
			{ID: "ends-at-start", Date: "2026-03-07", Start: "2026-03-08T04:00:00Z", End: "2026-03-08T05:00:00Z", Status: "open"},
			{ID: "cross-start", Date: "2026-03-07", Start: "2026-03-08T04:30:00Z", End: "2026-03-08T05:30:00Z", Status: "open"},
			{ID: "starts-at-end", Date: "2026-03-09", Start: "2026-03-09T04:00:00Z", End: "2026-03-09T05:00:00Z", Status: "open"},
			{ID: "cross-end", Date: "2026-03-08", Start: "2026-03-09T03:30:00Z", End: "2026-03-09T04:30:00Z", Status: "open"},
			{ID: "fractional-start", Date: "2026-03-08", Start: "2026-03-08T05:00:00.500Z", End: "2026-03-08T06:00:00Z", Status: "open"},
			{ID: "ends-before-start", Date: "2026-03-07", Start: "2026-03-08T04:00:00Z", End: "2026-03-08T04:59:59.999999Z", Status: "open"},
			{ID: "ends-after-start", Date: "2026-03-07", Start: "2026-03-08T04:00:00Z", End: "2026-03-08T05:00:00.000001Z", Status: "open"},
			{ID: "starts-before-end", Date: "2026-03-08", Start: "2026-03-09T03:59:59.999999Z", End: "2026-03-09T05:00:00Z", Status: "open"},
			{ID: "starts-after-end", Date: "2026-03-09", Start: "2026-03-09T04:00:00.000001Z", End: "2026-03-09T05:00:00Z", Status: "open"},
		}
		for _, item := range items {
			workspaceQueryTask(t, fixture, "overview-dst", "main", item.ID, "active", "time_block", "none", []workspaceQueryOccurrence{item})
		}
		workspaceQueryTask(t, fixture, "overview-dst", "main", "all-day-span", "active", "date", "none", []workspaceQueryOccurrence{{ID: "all-day-span", Date: "2026-03-07", AllDayEnd: "2026-03-09", Status: "open"}})
		workspaceQueryTask(t, fixture, "overview-dst", "main", "completed-boundaries", "active", "date", "daily", []workspaceQueryOccurrence{
			{ID: "done-start", Date: "2026-03-06", Status: "done", Completed: "2026-03-08T05:00:00Z"},
			{ID: "done-fraction", Date: "2026-03-07", Status: "done", Completed: "2026-03-08T00:00:00.500-05:00"},
			{ID: "done-end", Date: "2026-03-08", Status: "done", Completed: "2026-03-09T04:00:00Z"},
			{ID: "done-before-start", Date: "2026-03-02", Status: "done", Completed: "2026-03-08T04:59:59.999999Z"},
			{ID: "done-after-start", Date: "2026-03-03", Status: "done", Completed: "2026-03-08T05:00:00.000001Z"},
			{ID: "done-before-end", Date: "2026-03-04", Status: "done", Completed: "2026-03-09T03:59:59.999999Z"},
			{ID: "done-after-end", Date: "2026-03-05", Status: "done", Completed: "2026-03-09T04:00:00.000001Z"},
		})
		dst := workspaceQueryWindow(t, "2026-03-08", "America/New_York")
		if dst.End.Sub(dst.Start) != 23*time.Hour {
			t.Fatal("fixture must cover DST day")
		}
		result, err := fixture.NewReader("overview-dst").Overview(ctx, workspacequery.Request{Window: dst, ExpectedEpoch: 1, TodayLimit: 50, ProjectLimit: 1})
		if err != nil || result.Facets.Today != 6 || result.Facets.CompletedToday != 4 || len(result.Today) != 6 {
			t.Fatalf("DST result=%+v err=%v", result, err)
		}
		for _, item := range result.Today {
			if item.OccurrenceID == "ends-at-start" || item.OccurrenceID == "starts-at-end" || item.OccurrenceID == "ends-before-start" || item.OccurrenceID == "starts-after-end" {
				t.Fatalf("exclusive boundary leaked: %+v", item)
			}
		}
		invalid := request
		invalid.Window = dst
		invalid.Window.End = dst.Start.Add(24 * time.Hour)
		if _, err := fixture.NewReader("overview-dst").Overview(ctx, invalid); !errors.Is(err, workspacequery.ErrInvalidRequest) {
			t.Fatalf("invalid DST window err=%v", err)
		}
	})

	t.Run("actionable_states_and_finite_recurrence", func(t *testing.T) {
		for _, state := range []string{"planning", "paused", "completed", "archived"} {
			workspaceQueryProject(t, fixture, "overview-states", state, state, state, "")
			workspaceQueryTask(t, fixture, "overview-states", state, state, "paused", "date", "none", []workspaceQueryOccurrence{{ID: state, Date: "2026-11-19", Status: "open"}})
		}
		result, err := fixture.NewReader("overview-states").Overview(ctx, request)
		if err != nil || result.Facets.Today != 2 {
			t.Fatalf("project lifecycle=%+v err=%v", result, err)
		}
		workspaceQueryExec(t, fixture, fixture.DB, `UPDATE domain_task_schedule_versions_v2 SET ends_on='2026-11-30' WHERE workspace_id=? AND task_id=?`, "overview-a", "repeat")
		project, err := fixture.NewReader("overview-a").ProjectOverview(ctx, workspacequery.ProjectRequest{Window: window, ExpectedEpoch: 1, ProjectID: "main"})
		if err != nil || project.HasOpenEndedRecurrence {
			t.Fatalf("finite recurrence=%+v err=%v", project, err)
		}
	})

	t.Run("next_execution_ignores_unscheduled_and_past_work", func(t *testing.T) {
		workspaceQueryProject(t, fixture, "overview-next", "main", "Next project", "active", "")
		workspaceQueryTask(t, fixture, "overview-next", "main", "unscheduled", "active", "unscheduled", "none", []workspaceQueryOccurrence{{ID: "unscheduled", Status: "open"}})
		workspaceQueryTask(t, fixture, "overview-next", "main", "past-date", "active", "date", "none", []workspaceQueryOccurrence{{ID: "past-date", Date: "2026-11-18", Status: "open"}})
		workspaceQueryTask(t, fixture, "overview-next", "main", "future-date", "active", "date", "none", []workspaceQueryOccurrence{{ID: "future-date", Date: "2026-11-21", Status: "open"}})
		workspaceQueryTask(t, fixture, "overview-next", "main", "timed-today", "active", "time_block", "none", []workspaceQueryOccurrence{{ID: "timed-today", Date: "2026-11-19", Start: "2026-11-19T12:00:00Z", End: "2026-11-19T13:00:00Z", Status: "open"}})
		workspaceQueryTask(t, fixture, "overview-next", "main", "timed-tomorrow", "active", "time_block", "none", []workspaceQueryOccurrence{{ID: "timed-tomorrow", Date: "2026-11-20", Start: "2026-11-20T09:00:00Z", End: "2026-11-20T10:00:00Z", Status: "open"}})
		request := workspacequery.ProjectRequest{Window: window, ExpectedEpoch: 1, ProjectID: "main"}
		p, err := fixture.NewReader("overview-next").ProjectOverview(ctx, request)
		if err != nil || p.NextOccurrence == nil || p.NextOccurrence.OccurrenceID != "timed-today" {
			t.Fatalf("next timed execution=%+v err=%v", p, err)
		}
		workspaceQueryExec(t, fixture, fixture.DB, `UPDATE domain_task_occurrences_v2 SET execution_status='done',completed_at='2026-11-19T13:00:00Z' WHERE workspace_id=? AND id=?`, "overview-next", "timed-today")
		p, err = fixture.NewReader("overview-next").ProjectOverview(ctx, request)
		if err != nil || p.NextOccurrence == nil || p.NextOccurrence.OccurrenceID != "timed-tomorrow" {
			t.Fatalf("next after completion=%+v err=%v", p, err)
		}
	})

	t.Run("stale_epoch_and_inactive_runtime_fail_closed_without_read_repairs", func(t *testing.T) {
		reader := fixture.NewReader("overview-a")
		workspaceQueryExec(t, fixture, fixture.DB, `UPDATE tenant_workspaces SET epoch=2 WHERE workspace_id=?`, "overview-a")
		if _, err := reader.Overview(ctx, request); !errors.Is(err, workspacequery.ErrRuntimeUnavailable) {
			t.Fatalf("split epoch err=%v", err)
		}
		workspaceQueryExec(t, fixture, fixture.DB, `UPDATE workspace_task_domain_state SET write_epoch=2 WHERE workspace_id=?`, "overview-a")
		if _, err := reader.Overview(ctx, request); !errors.Is(err, workspacequery.ErrRuntimeUnavailable) {
			t.Fatalf("stale epoch err=%v", err)
		}
		fresh := request
		fresh.ExpectedEpoch = 2
		if _, err := reader.Overview(ctx, fresh); err != nil {
			t.Fatalf("fresh epoch=%v", err)
		}
		workspaceQueryExec(t, fixture, fixture.DB, `UPDATE tenant_workspaces SET state='retired' WHERE workspace_id=?`, "overview-a")
		if _, err := reader.Overview(ctx, fresh); !errors.Is(err, workspacequery.ErrRuntimeUnavailable) {
			t.Fatalf("retired epoch err=%v", err)
		}
		if _, err := fixture.NewReader("unknown").Overview(ctx, request); !errors.Is(err, workspacequery.ErrRuntimeUnavailable) {
			t.Fatalf("missing anchor err=%v", err)
		}
		var count int
		if err := fixture.DB.QueryRow(`SELECT COUNT(*) FROM tenant_workspaces WHERE workspace_id='unknown'`).Scan(&count); err != nil || count != 0 {
			t.Fatalf("read repaired missing anchor count=%d err=%v", count, err)
		}
		invalid := request
		invalid.TodayLimit = 51
		if _, err := reader.Overview(ctx, invalid); !errors.Is(err, workspacequery.ErrInvalidRequest) {
			t.Fatalf("unbounded request err=%v", err)
		}
	})
}

type workspaceQueryOccurrence struct{ ID, Date, Start, End, Due, AllDayEnd, Status, Completed string }
type workspaceQueryExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func workspaceQueryExec(t *testing.T, f WorkspaceQueryFixture, db workspaceQueryExecer, query string, args ...any) {
	t.Helper()
	if f.Dialect == TaskDomainV2Postgres {
		for i := 1; strings.Contains(query, "?"); i++ {
			query = strings.Replace(query, "?", fmt.Sprintf("$%d", i), 1)
		}
	}
	if _, err := db.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatalf("seed workspace query: %v\n%s", err, query)
	}
}
func workspaceQueryProject(t *testing.T, f WorkspaceQueryFixture, w, id, name, status, role string) {
	t.Helper()
	var archived any
	if status == "archived" {
		archived = "active"
	}
	var systemRole any
	if role != "" {
		systemRole = role
	}
	workspaceQueryExec(t, f, f.DB, `INSERT INTO domain_projects_v2(workspace_id,id,name,kind,horizon,status,archived_from_status,system_role,revision,created_at,updated_at)
	 VALUES (?,?,?,'standard','short',?,?,?,1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, w, id, name, status, archived, systemRole)
}
func workspaceQueryTask(t *testing.T, f WorkspaceQueryFixture, w, p, id, lifecycle, timing, recurrence string, items []workspaceQueryOccurrence) {
	t.Helper()
	tx, err := f.DB.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	workspaceQueryExec(t, f, tx, `INSERT INTO domain_tasks_v2(workspace_id,id,project_id,title,lifecycle_status,priority,sort_order,revision,created_at,updated_at)
	 VALUES (?,?,?,?,?,1,0,1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, w, id, p, id, lifecycle)
	workspaceQueryExec(t, f, tx, `INSERT INTO domain_task_schedules_v2(workspace_id,task_id,revision,current_schedule_revision,updated_at) VALUES (?,?,1,1,CURRENT_TIMESTAMP)`, w, id)
	var starts, effective, localTime, duration any
	if timing != "unscheduled" {
		starts = "2026-01-01"
	}
	rule := "{}"
	if recurrence != "none" {
		effective = "2026-01-01"
		rule = `{"interval":1}`
	}
	if timing == "time_block" {
		localTime = "09:00"
		duration = 60
	}
	workspaceQueryExec(t, f, tx, `INSERT INTO domain_task_schedule_versions_v2(workspace_id,task_id,schedule_revision,effective_from,recurrence_type,timing_type,timezone,starts_on,recurrence_rule,local_start_time,duration_minutes,created_at)
	 VALUES (?,?,1,?,?,?,'UTC',?,?,?,?,CURRENT_TIMESTAMP)`, w, id, effective, recurrence, timing, starts, rule, localTime, duration)
	optional := func(s string) any {
		if s == "" {
			return nil
		}
		return s
	}
	for i, item := range items {
		key := "once"
		if recurrence != "none" {
			key = fmt.Sprintf("2026-%03d", i)
		}
		var reason, next any
		if item.Status == "blocked" {
			reason = "waiting"
			next = "follow up"
		}
		workspaceQueryExec(t, f, tx, `INSERT INTO domain_task_occurrences_v2(workspace_id,id,task_id,occurrence_key,planned_date,planned_start_at,planned_end_at,due_at,execution_status,completed_at,blocked_reason,next_action,all_day_end_date,revision,generated_schedule_revision,created_at,updated_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,1,1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, w, item.ID, id, key, optional(item.Date), optional(item.Start), optional(item.End), optional(item.Due), item.Status, optional(item.Completed), reason, next, optional(item.AllDayEnd))
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
func workspaceQueryWindow(t *testing.T, date, zone string) workspacequery.Window {
	t.Helper()
	loc, err := time.LoadLocation(zone)
	if err != nil {
		t.Fatal(err)
	}
	start, err := time.ParseInLocation("2006-01-02", date, loc)
	if err != nil {
		t.Fatal(err)
	}
	week := start.AddDate(0, 0, -(int(start.Weekday())+6)%7)
	month := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, loc)
	return workspacequery.Window{Date: date, Timezone: zone, Start: start, End: start.AddDate(0, 0, 1), WeekStart: week, WeekEnd: week.AddDate(0, 0, 7), MonthStart: month, MonthEnd: month.AddDate(0, 1, 0)}
}
