package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/taskdomain"
)

type sqliteTaskDomainV2Queryer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type sqliteTaskDomainV2ProjectReader struct {
	queryer     sqliteTaskDomainV2Queryer
	workspaceID string
}

func newTaskDomainV2ProjectReader(db *sql.DB, workspaceID string) taskdomain.TaskDomainReader {
	return &sqliteTaskDomainV2ProjectReader{queryer: db, workspaceID: workspaceID}
}

func (r *sqliteTaskDomainV2ProjectReader) GetProject(ctx context.Context, projectID string) (taskdomain.ProjectSnapshot, error) {
	return getSQLiteTaskDomainV2Project(ctx, r.queryer, r.workspaceID, projectID)
}

func (r *sqliteTaskDomainV2ProjectReader) ListProjects(ctx context.Context, filter taskdomain.ProjectListFilter) ([]taskdomain.ProjectSnapshot, error) {
	if err := taskdomain.ValidateProjectListFilter(filter); err != nil {
		return nil, err
	}
	conditions := []string{"workspace_id=?"}
	args := []any{r.workspaceID}
	if filter.Kind != nil {
		conditions = append(conditions, "kind=?")
		args = append(args, *filter.Kind)
	}
	if filter.Horizon != nil {
		conditions = append(conditions, "horizon=?")
		args = append(args, *filter.Horizon)
	}
	if filter.Status != nil {
		conditions = append(conditions, "status=?")
		args = append(args, *filter.Status)
	}
	rows, err := r.queryer.QueryContext(ctx, `SELECT workspace_id,id,name,kind,horizon,status,system_role,revision
		FROM domain_projects_v2 WHERE `+strings.Join(conditions, " AND ")+` ORDER BY name,id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]taskdomain.ProjectSnapshot, 0)
	for rows.Next() {
		var item taskdomain.ProjectSnapshot
		var kind, horizon, status string
		var systemRole sql.NullString
		if err := rows.Scan(&item.Project.WorkspaceID, &item.Project.ID, &item.Project.Name, &kind, &horizon, &status, &systemRole, &item.Revision); err != nil {
			return nil, err
		}
		item.Project.Kind = taskdomain.ProjectKind(kind)
		item.Project.Horizon = taskdomain.ProjectHorizon(horizon)
		item.Project.Status = taskdomain.ProjectStatus(status)
		item.Project.SystemRole = taskdomain.ProjectSystemRole(systemRole.String)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (r *sqliteTaskDomainV2ProjectReader) ListTaskDefinitions(ctx context.Context, filter taskdomain.TaskDefinitionListFilter) ([]taskdomain.TaskDefinitionSnapshot, error) {
	if err := taskdomain.ValidateTaskDefinitionListFilter(filter); err != nil {
		return nil, err
	}
	conditions := []string{"t.workspace_id=?"}
	args := []any{r.workspaceID}
	if filter.ProjectID != "" {
		conditions = append(conditions, "t.project_id=?")
		args = append(args, filter.ProjectID)
	}
	if filter.LifecycleStatus != nil {
		conditions = append(conditions, "t.lifecycle_status=?")
		args = append(args, *filter.LifecycleStatus)
	}
	rows, err := r.queryer.QueryContext(ctx, `SELECT
		t.workspace_id,t.id,t.project_id,t.roadmap_node_id,t.note_id,t.title,t.description,t.lifecycle_status,
		t.priority,t.sort_order,t.revision,s.revision,s.current_schedule_revision
		FROM domain_tasks_v2 t JOIN domain_task_schedules_v2 s
		ON s.workspace_id=t.workspace_id AND s.task_id=t.id
		WHERE `+strings.Join(conditions, " AND ")+` ORDER BY t.sort_order,t.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]taskdomain.TaskDefinitionSnapshot, 0)
	for rows.Next() {
		var item taskdomain.TaskDefinitionSnapshot
		var roadmapNodeID, noteID sql.NullString
		var lifecycle string
		if err := rows.Scan(
			&item.Task.WorkspaceID, &item.Task.ID, &item.Task.ProjectID, &roadmapNodeID, &noteID,
			&item.Task.Title, &item.Task.Description, &lifecycle, &item.Task.Priority, &item.Task.SortOrder, &item.Task.Revision,
			&item.ScheduleRevision, &item.CurrentScheduleRevision,
		); err != nil {
			return nil, err
		}
		item.Task.RoadmapNodeID = roadmapNodeID.String
		item.Task.NoteID = noteID.String
		item.Task.LifecycleStatus = taskdomain.TaskLifecycleStatus(lifecycle)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (r *sqliteTaskDomainV2ProjectReader) GetScheduleCommandState(ctx context.Context, taskID string) (taskdomain.ScheduleCommandState, error) {
	state := taskdomain.ScheduleCommandState{WorkspaceID: r.workspaceID, TaskID: taskID}
	err := r.queryer.QueryRowContext(ctx, `SELECT t.revision,s.revision,s.current_schedule_revision
		FROM domain_tasks_v2 t JOIN domain_task_schedules_v2 s ON s.workspace_id=t.workspace_id AND s.task_id=t.id
		WHERE t.workspace_id=? AND t.id=?`, r.workspaceID, taskID).Scan(
		&state.TaskRevision, &state.Schedule.Revision, &state.Schedule.CurrentScheduleRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return taskdomain.ScheduleCommandState{}, taskdomain.ErrTaskNotFound
	}
	if err != nil {
		return taskdomain.ScheduleCommandState{}, err
	}
	state.Schedule.WorkspaceID = r.workspaceID
	state.Schedule.TaskID = taskID
	rows, err := r.queryer.QueryContext(ctx, `SELECT schedule_revision,effective_from,effective_to,recurrence_type,timing_type,timezone,
		starts_on,ends_on,recurrence_rule,local_start_time,duration_minutes
		FROM domain_task_schedule_versions_v2 WHERE workspace_id=? AND task_id=? ORDER BY schedule_revision`, r.workspaceID, taskID)
	if err != nil {
		return taskdomain.ScheduleCommandState{}, err
	}
	for rows.Next() {
		version, err := scanSQLiteTaskDomainV2ScheduleVersion(rows, r.workspaceID, taskID)
		if err != nil {
			rows.Close()
			return taskdomain.ScheduleCommandState{}, err
		}
		state.Versions = append(state.Versions, version)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return taskdomain.ScheduleCommandState{}, err
	}
	rows.Close()
	rows, err = r.queryer.QueryContext(ctx, `SELECT id,occurrence_key,planned_date,planned_start_at,planned_end_at,due_at,
		execution_status,note_id,all_day_end_date,revision,generated_schedule_revision,actual_start_at,completed_at,manually_overridden
		FROM domain_task_occurrences_v2 WHERE workspace_id=? AND task_id=? ORDER BY occurrence_key,id`, r.workspaceID, taskID)
	if err != nil {
		return taskdomain.ScheduleCommandState{}, err
	}
	defer rows.Close()
	for rows.Next() {
		occurrence, err := scanSQLiteTaskDomainV2ScheduleOccurrence(rows, r.workspaceID, taskID)
		if err != nil {
			return taskdomain.ScheduleCommandState{}, err
		}
		state.Occurrences = append(state.Occurrences, occurrence)
	}
	if err := rows.Err(); err != nil {
		return taskdomain.ScheduleCommandState{}, err
	}
	return state, nil
}

func (r *sqliteTaskDomainV2ProjectReader) GetTaskAggregate(ctx context.Context, taskID string) (taskdomain.TaskAggregateQueryResult, error) {
	var result taskdomain.TaskAggregateQueryResult
	var roadmapNodeID, noteID sql.NullString
	var lifecycle string
	err := r.queryer.QueryRowContext(ctx, `SELECT
		t.workspace_id,t.id,t.project_id,t.roadmap_node_id,t.note_id,t.title,t.description,t.lifecycle_status,t.priority,t.sort_order,t.revision,
		s.revision,s.current_schedule_revision
		FROM domain_tasks_v2 t
		JOIN domain_task_schedules_v2 s ON s.workspace_id=t.workspace_id AND s.task_id=t.id
		WHERE t.workspace_id=? AND t.id=?`, r.workspaceID, taskID).Scan(
		&result.Task.WorkspaceID, &result.Task.ID, &result.Task.ProjectID, &roadmapNodeID, &noteID,
		&result.Task.Title, &result.Task.Description, &lifecycle, &result.Task.Priority, &result.Task.SortOrder, &result.Task.Revision,
		&result.Schedule.Revision, &result.Schedule.CurrentScheduleRevision,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return taskdomain.TaskAggregateQueryResult{}, taskdomain.ErrTaskNotFound
	}
	if err != nil {
		return taskdomain.TaskAggregateQueryResult{}, err
	}
	result.Task.RoadmapNodeID = roadmapNodeID.String
	result.Task.NoteID = noteID.String
	result.Task.LifecycleStatus = taskdomain.TaskLifecycleStatus(lifecycle)
	result.Schedule.WorkspaceID = r.workspaceID
	result.Schedule.TaskID = taskID

	rows, err := r.queryer.QueryContext(ctx, `SELECT schedule_revision,effective_from,effective_to,recurrence_type,timing_type,timezone,
		starts_on,ends_on,recurrence_rule,local_start_time,duration_minutes
		FROM domain_task_schedule_versions_v2 WHERE workspace_id=? AND task_id=? ORDER BY schedule_revision`, r.workspaceID, taskID)
	if err != nil {
		return taskdomain.TaskAggregateQueryResult{}, err
	}
	defer rows.Close()
	for rows.Next() {
		version, err := scanSQLiteTaskDomainV2ScheduleVersion(rows, r.workspaceID, taskID)
		if err != nil {
			return taskdomain.TaskAggregateQueryResult{}, err
		}
		result.Versions = append(result.Versions, version)
	}
	if err := rows.Err(); err != nil {
		return taskdomain.TaskAggregateQueryResult{}, err
	}
	result.Occurrences, err = r.ListTaskOccurrences(ctx, taskID)
	if err != nil {
		return taskdomain.TaskAggregateQueryResult{}, err
	}
	result.Aggregate = taskdomain.TaskAggregate{
		WorkspaceID: r.workspaceID, TaskID: taskID, LifecycleStatus: result.Task.LifecycleStatus,
		Revision: result.Task.Revision, Occurrences: make([]taskdomain.Occurrence, 0, len(result.Occurrences)),
	}
	for _, occurrence := range result.Occurrences {
		result.Aggregate.Recurring = result.Aggregate.Recurring || occurrence.Recurring
		result.Aggregate.Occurrences = append(result.Aggregate.Occurrences, sqliteTaskDomainV2DomainOccurrence(occurrence))
	}
	result.Aggregate.GenerationEnabled = result.Aggregate.Recurring && result.Aggregate.LifecycleStatus == taskdomain.TaskLifecycleActive
	return result, nil
}

func (r *sqliteTaskDomainV2ProjectReader) GetOccurrence(ctx context.Context, occurrenceID string) (taskdomain.QueryOccurrenceSnapshot, error) {
	rows, err := r.queryer.QueryContext(ctx, sqliteTaskDomainV2OccurrenceSelect+` WHERE o.workspace_id=? AND o.id=?`, r.workspaceID, occurrenceID)
	if err != nil {
		return taskdomain.QueryOccurrenceSnapshot{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return taskdomain.QueryOccurrenceSnapshot{}, err
		}
		return taskdomain.QueryOccurrenceSnapshot{}, taskdomain.ErrOccurrenceNotFound
	}
	snapshot, err := scanSQLiteTaskDomainV2Occurrence(rows)
	if err != nil {
		return taskdomain.QueryOccurrenceSnapshot{}, err
	}
	return snapshot, nil
}

func (r *sqliteTaskDomainV2ProjectReader) ListTaskOccurrences(ctx context.Context, taskID string) ([]taskdomain.QueryOccurrenceSnapshot, error) {
	return r.listOccurrences(ctx, `o.workspace_id=? AND o.task_id=?`, []any{r.workspaceID, taskID})
}

func (r *sqliteTaskDomainV2ProjectReader) ListOccurrences(ctx context.Context, filter taskdomain.OccurrenceListFilter) ([]taskdomain.QueryOccurrenceSnapshot, error) {
	predicate, args, err := sqliteTaskDomainV2OccurrenceFilter(filter)
	if err != nil {
		return nil, err
	}
	conditions := []string{"o.workspace_id=?", predicate}
	queryArgs := []any{r.workspaceID}
	queryArgs = append(queryArgs, args...)
	if filter.ProjectID != "" {
		conditions = append(conditions, "t.project_id=?")
		queryArgs = append(queryArgs, filter.ProjectID)
	}
	if filter.TaskID != "" {
		conditions = append(conditions, "o.task_id=?")
		queryArgs = append(queryArgs, filter.TaskID)
	}
	if len(filter.Statuses) > 0 {
		placeholders := make([]string, 0, len(filter.Statuses))
		for _, status := range filter.Statuses {
			if !sqliteTaskDomainV2KnownExecutionStatus(status) {
				return nil, taskdomain.ErrInvalidOccurrenceListFilter
			}
			placeholders = append(placeholders, "?")
			queryArgs = append(queryArgs, status)
		}
		conditions = append(conditions, "o.execution_status IN ("+strings.Join(placeholders, ",")+")")
	}
	if filter.Recurring != nil {
		if *filter.Recurring {
			conditions = append(conditions, "v.recurrence_type <> 'none'")
		} else {
			conditions = append(conditions, "v.recurrence_type = 'none'")
		}
	}
	return r.listOccurrences(ctx, strings.Join(conditions, " AND "), queryArgs)
}

func (r *sqliteTaskDomainV2ProjectReader) listOccurrences(ctx context.Context, predicate string, args []any) ([]taskdomain.QueryOccurrenceSnapshot, error) {
	rows, err := r.queryer.QueryContext(ctx, sqliteTaskDomainV2OccurrenceSelect+" WHERE "+predicate, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]taskdomain.QueryOccurrenceSnapshot, 0)
	for rows.Next() {
		item, err := scanSQLiteTaskDomainV2Occurrence(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(items, func(left, right int) bool {
		return sqliteTaskDomainV2OccurrenceLess(items[left], items[right])
	})
	return items, nil
}

const sqliteTaskDomainV2OccurrenceSelect = `SELECT
	o.workspace_id,t.project_id,t.id,o.id,o.occurrence_key,
	COALESCE(o.override_title,t.title),COALESCE(o.override_description,t.description),
	v.timing_type,v.timezone,o.planned_date,o.planned_start_at,o.planned_end_at,o.due_at,o.execution_status,
	CASE WHEN v.recurrence_type <> 'none' THEN 1 ELSE 0 END,
	o.revision,p.revision,t.revision,s.revision,o.generated_schedule_revision,t.lifecycle_status,t.priority,t.sort_order,
	o.actual_start_at,o.completed_at,o.blocked_reason,o.next_action,o.location,o.calendar_kind,o.calendar_notes,
	t.note_id,o.note_id,o.all_day_end_date
	FROM domain_task_occurrences_v2 o
	JOIN domain_tasks_v2 t ON t.workspace_id=o.workspace_id AND t.id=o.task_id
	JOIN domain_projects_v2 p ON p.workspace_id=t.workspace_id AND p.id=t.project_id
	JOIN domain_task_schedules_v2 s ON s.workspace_id=o.workspace_id AND s.task_id=o.task_id
	JOIN domain_task_schedule_versions_v2 v ON v.workspace_id=o.workspace_id AND v.task_id=o.task_id
		AND v.schedule_revision=o.generated_schedule_revision`

type sqliteTaskDomainV2Scanner interface {
	Scan(...any) error
}

func scanSQLiteTaskDomainV2Occurrence(scanner sqliteTaskDomainV2Scanner) (taskdomain.QueryOccurrenceSnapshot, error) {
	var item taskdomain.QueryOccurrenceSnapshot
	var timingType, executionStatus, lifecycleStatus string
	var recurring int
	var plannedDate, plannedStart, plannedEnd, dueAt, actualStart, completedAt sql.NullString
	var blockedReason, nextAction, location, calendarKind, calendarNotes, taskNoteID, occurrenceNoteID, allDayEndDate sql.NullString
	err := scanner.Scan(
		&item.WorkspaceID, &item.ProjectID, &item.TaskID, &item.OccurrenceID, &item.OccurrenceKey,
		&item.Title, &item.Description, &timingType, &item.Timezone, &plannedDate, &plannedStart, &plannedEnd, &dueAt, &executionStatus,
		&recurring, &item.Revision, &item.ProjectRevision, &item.TaskRevision, &item.ScheduleRevision, &item.GeneratedScheduleRevision,
		&lifecycleStatus, &item.Priority, &item.SortOrder, &actualStart, &completedAt, &blockedReason, &nextAction,
		&location, &calendarKind, &calendarNotes, &taskNoteID, &occurrenceNoteID, &allDayEndDate,
	)
	if err != nil {
		return taskdomain.QueryOccurrenceSnapshot{}, err
	}
	item.TimingType = taskdomain.TimingType(timingType)
	item.Status = taskdomain.ExecutionStatus(executionStatus)
	item.LifecycleStatus = taskdomain.TaskLifecycleStatus(lifecycleStatus)
	item.Recurring = recurring != 0
	item.PlannedDate = plannedDate.String
	item.AllDayEndDate = allDayEndDate.String
	item.BlockedReason = blockedReason.String
	item.NextAction = nextAction.String
	item.Location = location.String
	item.CalendarKind = calendarKind.String
	item.CalendarNotes = calendarNotes.String
	item.TaskNoteID = taskNoteID.String
	item.OccurrenceNoteID = occurrenceNoteID.String
	for _, pair := range []struct {
		source sql.NullString
		target **time.Time
	}{
		{plannedStart, &item.PlannedStartAt}, {plannedEnd, &item.PlannedEndAt}, {dueAt, &item.DueAt},
		{actualStart, &item.ActualStartAt}, {completedAt, &item.CompletedAt},
	} {
		source, target := pair.source, pair.target
		if source.Valid {
			parsed, err := time.Parse(time.RFC3339Nano, source.String)
			if err != nil {
				return taskdomain.QueryOccurrenceSnapshot{}, err
			}
			*target = &parsed
		}
	}
	return item, nil
}

func scanSQLiteTaskDomainV2ScheduleVersion(scanner sqliteTaskDomainV2Scanner, workspaceID, taskID string) (taskdomain.ScheduleVersion, error) {
	version := taskdomain.ScheduleVersion{WorkspaceID: workspaceID, TaskID: taskID}
	var effectiveFrom, effectiveTo, startsOn, endsOn, localStartTime sql.NullString
	var recurrenceType, timingType string
	var duration sql.NullInt64
	err := scanner.Scan(&version.ScheduleRevision, &effectiveFrom, &effectiveTo, &recurrenceType, &timingType, &version.Timezone,
		&startsOn, &endsOn, &version.RecurrenceRule, &localStartTime, &duration)
	if err != nil {
		return taskdomain.ScheduleVersion{}, err
	}
	version.EffectiveFrom = effectiveFrom.String
	version.EffectiveTo = effectiveTo.String
	version.RecurrenceType = taskdomain.RecurrenceType(recurrenceType)
	version.TimingType = taskdomain.TimingType(timingType)
	version.StartsOn = startsOn.String
	version.EndsOn = endsOn.String
	version.LocalStartTime = localStartTime.String
	if duration.Valid {
		version.DurationMinutes = int(duration.Int64)
	}
	return version, nil
}

func scanSQLiteTaskDomainV2ScheduleOccurrence(scanner sqliteTaskDomainV2Scanner, workspaceID, taskID string) (taskdomain.ScheduleOccurrenceSnapshot, error) {
	var snapshot taskdomain.ScheduleOccurrenceSnapshot
	snapshot.Record.WorkspaceID = workspaceID
	snapshot.Record.TaskID = taskID
	var plannedDate, plannedStart, plannedEnd, dueAt, noteID, allDayEnd, actualStart, completedAt sql.NullString
	var status string
	var manuallyOverridden int
	err := scanner.Scan(&snapshot.Record.ID, &snapshot.Record.OccurrenceKey, &plannedDate, &plannedStart, &plannedEnd, &dueAt,
		&status, &noteID, &allDayEnd, &snapshot.Record.Revision, &snapshot.Record.GeneratedScheduleRevision,
		&actualStart, &completedAt, &manuallyOverridden)
	if err != nil {
		return taskdomain.ScheduleOccurrenceSnapshot{}, err
	}
	snapshot.Record.PlannedDate = plannedDate.String
	snapshot.Record.ExecutionStatus = taskdomain.ExecutionStatus(status)
	snapshot.Record.NoteID = noteID.String
	snapshot.Record.AllDayEndDate = allDayEnd.String
	snapshot.ManuallyOverridden = manuallyOverridden != 0
	for _, pair := range []struct {
		source sql.NullString
		target **time.Time
	}{
		{plannedStart, &snapshot.Record.PlannedStartAt}, {plannedEnd, &snapshot.Record.PlannedEndAt}, {dueAt, &snapshot.Record.DueAt},
		{actualStart, &snapshot.ActualStartAt}, {completedAt, &snapshot.CompletedAt},
	} {
		if pair.source.Valid {
			parsed, err := time.Parse(time.RFC3339Nano, pair.source.String)
			if err != nil {
				return taskdomain.ScheduleOccurrenceSnapshot{}, err
			}
			*pair.target = &parsed
		}
	}
	return snapshot, nil
}

func sqliteTaskDomainV2DomainOccurrence(item taskdomain.QueryOccurrenceSnapshot) taskdomain.Occurrence {
	return taskdomain.Occurrence{
		WorkspaceID: item.WorkspaceID, ID: item.OccurrenceID, TaskID: item.TaskID, OccurrenceKey: item.OccurrenceKey,
		ExecutionStatus: item.Status, Recurring: item.Recurring, ActualStartAt: cloneSQLiteTaskDomainV2Time(item.ActualStartAt),
		CompletedAt: cloneSQLiteTaskDomainV2Time(item.CompletedAt), BlockedReason: item.BlockedReason, NextAction: item.NextAction,
		Revision: item.Revision,
	}
}

func sqliteTaskDomainV2OccurrenceFilter(filter taskdomain.OccurrenceListFilter) (string, []any, error) {
	from := filter.From.UTC().Format(time.RFC3339Nano)
	to := filter.To.UTC().Format(time.RFC3339Nano)
	dateBounds := func() (string, string, error) {
		if filter.Timezone == "" {
			return "", "", taskdomain.ErrInvalidOccurrenceListFilter
		}
		location, err := time.LoadLocation(filter.Timezone)
		if err != nil {
			return "", "", taskdomain.ErrInvalidOccurrenceListFilter
		}
		fromLocal := filter.From.In(location)
		toLocal := filter.To.In(location)
		toDate := toLocal.Format("2006-01-02")
		if toLocal.Hour() != 0 || toLocal.Minute() != 0 || toLocal.Second() != 0 || toLocal.Nanosecond() != 0 {
			toDate = toLocal.AddDate(0, 0, 1).Format("2006-01-02")
		}
		return fromLocal.Format("2006-01-02"), toDate, nil
	}
	requireRange := func() error {
		if filter.From.IsZero() || filter.To.IsZero() || !filter.To.After(filter.From) {
			return taskdomain.ErrInvalidOccurrenceListFilter
		}
		return nil
	}
	switch filter.Scope {
	case taskdomain.OccurrenceListAll:
		return `1=1`, nil, nil
	case taskdomain.OccurrenceListToday:
		if err := requireRange(); err != nil {
			return "", nil, err
		}
		fromDate, toDate, err := dateBounds()
		if err != nil {
			return "", nil, err
		}
		return `((v.timing_type='date' AND o.planned_date < ? AND COALESCE(o.all_day_end_date,date(o.planned_date,'+1 day')) > ?)
			OR (v.timing_type='time_block' AND o.planned_start_at < ? AND o.planned_end_at > ?))`, []any{toDate, fromDate, to, from}, nil
	case taskdomain.OccurrenceListUpcoming:
		if err := requireRange(); err != nil {
			return "", nil, err
		}
		fromDate, toDate, err := dateBounds()
		if err != nil {
			return "", nil, err
		}
		return `(((v.timing_type='date' AND o.planned_date >= ? AND o.planned_date < ?)
			OR (v.timing_type='time_block' AND o.planned_start_at >= ? AND o.planned_start_at < ?))
			AND o.execution_status NOT IN ('done','skipped','cancelled'))`, []any{fromDate, toDate, from, to}, nil
	case taskdomain.OccurrenceListOverdue:
		if filter.From.IsZero() {
			return "", nil, taskdomain.ErrInvalidOccurrenceListFilter
		}
		return `o.due_at < ? AND o.execution_status NOT IN ('done','skipped','cancelled')`, []any{from}, nil
	case taskdomain.OccurrenceListUnscheduled:
		return `v.timing_type='unscheduled'`, nil, nil
	case taskdomain.OccurrenceListCompleted:
		if err := requireRange(); err != nil {
			return "", nil, err
		}
		return `o.execution_status='done' AND o.completed_at >= ? AND o.completed_at < ?`, []any{from, to}, nil
	case taskdomain.OccurrenceListCalendar:
		if err := requireRange(); err != nil {
			return "", nil, err
		}
		fromDate, toDate, err := dateBounds()
		if err != nil {
			return "", nil, err
		}
		return `((v.timing_type='date' AND o.planned_date < ? AND COALESCE(o.all_day_end_date,date(o.planned_date,'+1 day')) > ?)
			OR (v.timing_type='time_block' AND o.planned_start_at < ? AND o.planned_end_at > ?))`, []any{toDate, fromDate, to, from}, nil
	default:
		return "", nil, taskdomain.ErrInvalidOccurrenceListFilter
	}
}

func sqliteTaskDomainV2KnownExecutionStatus(status taskdomain.ExecutionStatus) bool {
	switch status {
	case taskdomain.ExecutionStatusOpen, taskdomain.ExecutionStatusActive, taskdomain.ExecutionStatusBlocked,
		taskdomain.ExecutionStatusDone, taskdomain.ExecutionStatusSkipped, taskdomain.ExecutionStatusCancelled:
		return true
	default:
		return false
	}
}

func sqliteTaskDomainV2OccurrenceLess(left, right taskdomain.QueryOccurrenceSnapshot) bool {
	if left.PlannedDate != right.PlannedDate {
		if left.PlannedDate == "" {
			return false
		}
		if right.PlannedDate == "" {
			return true
		}
		return left.PlannedDate < right.PlannedDate
	}
	if left.PlannedStartAt != nil || right.PlannedStartAt != nil {
		if left.PlannedStartAt == nil {
			return true
		}
		if right.PlannedStartAt == nil {
			return false
		}
		if !left.PlannedStartAt.Equal(*right.PlannedStartAt) {
			return left.PlannedStartAt.Before(*right.PlannedStartAt)
		}
	}
	if left.DueAt != nil || right.DueAt != nil {
		if left.DueAt == nil {
			return false
		}
		if right.DueAt == nil {
			return true
		}
		if !left.DueAt.Equal(*right.DueAt) {
			return left.DueAt.Before(*right.DueAt)
		}
	}
	return left.OccurrenceID < right.OccurrenceID
}

func cloneSQLiteTaskDomainV2Time(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

type sqliteTaskDomainV2ProjectWriter struct {
	queryer     sqliteTaskDomainV2Queryer
	workspaceID string
	isClosed    func() bool
}

type sqliteProjectCommandWriter struct {
	delegate *sqliteTaskDomainV2ProjectWriter
}

func (w *sqliteProjectCommandWriter) EnsureSystemProjects(ctx context.Context) error {
	return w.delegate.EnsureSystemProjects(ctx)
}

func (w *sqliteProjectCommandWriter) SaveProject(ctx context.Context, write taskdomain.ProjectWrite) error {
	err := w.delegate.SaveProject(ctx, write)
	if errors.Is(err, taskdomain.ErrAggregateRevisionConflict) {
		return taskdomain.ErrProjectRevisionConflict
	}
	return err
}

func (w *sqliteProjectCommandWriter) DeleteProject(ctx context.Context, projectID string, expectedRevision int64) error {
	err := w.delegate.DeleteProject(ctx, projectID, expectedRevision)
	if errors.Is(err, taskdomain.ErrAggregateRevisionConflict) {
		return taskdomain.ErrProjectRevisionConflict
	}
	return err
}

func (w *sqliteTaskDomainV2ProjectWriter) GetProject(ctx context.Context, projectID string) (taskdomain.ProjectSnapshot, error) {
	if w.closed() {
		return taskdomain.ProjectSnapshot{}, storage.ErrTenantWriteTxClosed
	}
	return getSQLiteTaskDomainV2Project(ctx, w.queryer, w.workspaceID, projectID)
}

func (w *sqliteTaskDomainV2ProjectWriter) CountNonTerminalProjectOccurrences(ctx context.Context, projectID string) (int, error) {
	if w.closed() {
		return 0, storage.ErrTenantWriteTxClosed
	}
	var count int
	err := w.queryer.QueryRowContext(ctx, `SELECT COUNT(*)
		FROM domain_task_occurrences_v2 occurrence
		JOIN domain_tasks_v2 task ON task.workspace_id=occurrence.workspace_id AND task.id=occurrence.task_id
		WHERE task.workspace_id=? AND task.project_id=? AND occurrence.execution_status IN ('open','active','blocked')`,
		w.workspaceID, projectID).Scan(&count)
	return count, err
}

func (w *sqliteTaskDomainV2ProjectWriter) LoadRecurringCompletionState(ctx context.Context, taskID string) (taskdomain.RecurringCompletionCommandState, error) {
	if w.closed() {
		return taskdomain.RecurringCompletionCommandState{}, storage.ErrTenantWriteTxClosed
	}
	reader := &sqliteTaskDomainV2ProjectReader{queryer: w.queryer, workspaceID: w.workspaceID}
	query, err := reader.GetTaskAggregate(ctx, taskID)
	if err != nil {
		return taskdomain.RecurringCompletionCommandState{}, err
	}
	state := taskdomain.RecurringCompletionCommandState{Aggregate: query.Aggregate}
	var watermark sql.NullString
	var generationStatus string
	err = w.queryer.QueryRowContext(ctx, `SELECT revision,generation_watermark,generation_status,
		generation_retry_pending_jobs,generation_failed_jobs FROM domain_task_schedules_v2
		WHERE workspace_id=? AND task_id=?`, w.workspaceID, taskID).Scan(
		&state.ScheduleRevision, &watermark, &generationStatus, &state.RetryPendingJobs, &state.FailedJobs)
	if errors.Is(err, sql.ErrNoRows) {
		return taskdomain.RecurringCompletionCommandState{}, taskdomain.ErrTaskNotFound
	}
	if err != nil {
		return taskdomain.RecurringCompletionCommandState{}, err
	}
	state.GenerationWatermark = watermark.String
	state.GenerationStatus = taskdomain.GenerationStatus(generationStatus)
	state.ScheduleVersions = make([]taskdomain.CompletionScheduleVersion, 0, len(query.Versions))
	for _, version := range query.Versions {
		schedule, err := taskdomain.NormalizeSchedule(taskdomain.ScheduleInput{
			RecurrenceType: version.RecurrenceType, TimingType: version.TimingType, Timezone: version.Timezone,
			StartsOn: version.StartsOn, EndsOn: version.EndsOn, Rule: json.RawMessage(version.RecurrenceRule),
			LocalStartTime: version.LocalStartTime, DurationMinutes: version.DurationMinutes,
		})
		if err != nil {
			return taskdomain.RecurringCompletionCommandState{}, err
		}
		state.ScheduleVersions = append(state.ScheduleVersions, taskdomain.CompletionScheduleVersion{
			Schedule: schedule, Effective: taskdomain.ScheduleEffectiveRange{From: version.EffectiveFrom, To: version.EffectiveTo},
		})
	}
	return state, nil
}

func (w *sqliteTaskDomainV2ProjectWriter) ListGenerationTargets(ctx context.Context) ([]taskdomain.GenerationTargetState, error) {
	if w.closed() {
		return nil, storage.ErrTenantWriteTxClosed
	}
	rows, err := w.queryer.QueryContext(ctx, `SELECT task.id FROM domain_tasks_v2 task
		JOIN domain_task_schedules_v2 schedule ON schedule.workspace_id=task.workspace_id AND schedule.task_id=task.id
		JOIN domain_task_schedule_versions_v2 version ON version.workspace_id=schedule.workspace_id AND version.task_id=schedule.task_id AND version.schedule_revision=schedule.current_schedule_revision
		WHERE task.workspace_id=? AND version.recurrence_type<>'none' ORDER BY task.id`, w.workspaceID)
	if err != nil {
		return nil, err
	}
	var taskIDs []string
	for rows.Next() {
		var taskID string
		if err := rows.Scan(&taskID); err != nil {
			rows.Close()
			return nil, err
		}
		taskIDs = append(taskIDs, taskID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	targets := make([]taskdomain.GenerationTargetState, 0, len(taskIDs))
	for _, taskID := range taskIDs {
		target, err := w.loadGenerationTarget(ctx, taskID)
		if err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	return targets, nil
}

func (w *sqliteTaskDomainV2ProjectWriter) loadGenerationTarget(ctx context.Context, taskID string) (taskdomain.GenerationTargetState, error) {
	target := taskdomain.GenerationTargetState{TaskID: taskID}
	var lifecycle string
	var watermark sql.NullString
	var currentVersion int64
	err := w.queryer.QueryRowContext(ctx, `SELECT task.lifecycle_status,schedule.revision,schedule.current_schedule_revision,schedule.generation_watermark
		FROM domain_tasks_v2 task JOIN domain_task_schedules_v2 schedule ON schedule.workspace_id=task.workspace_id AND schedule.task_id=task.id
		WHERE task.workspace_id=? AND task.id=?`, w.workspaceID, taskID).Scan(&lifecycle, &target.ScheduleRevision, &currentVersion, &watermark)
	if errors.Is(err, sql.ErrNoRows) {
		return taskdomain.GenerationTargetState{}, taskdomain.ErrTaskNotFound
	}
	if err != nil {
		return taskdomain.GenerationTargetState{}, err
	}
	target.GenerationWatermark = watermark.String
	rows, err := w.queryer.QueryContext(ctx, `SELECT schedule_revision,effective_from,effective_to,recurrence_type,timing_type,timezone,
		starts_on,ends_on,recurrence_rule,local_start_time,duration_minutes FROM domain_task_schedule_versions_v2
		WHERE workspace_id=? AND task_id=? ORDER BY schedule_revision`, w.workspaceID, taskID)
	if err != nil {
		return taskdomain.GenerationTargetState{}, err
	}
	for rows.Next() {
		version, err := scanSQLiteTaskDomainV2ScheduleVersion(rows, w.workspaceID, taskID)
		if err != nil {
			rows.Close()
			return taskdomain.GenerationTargetState{}, err
		}
		schedule, err := normalizeGenerationScheduleVersion(version)
		if err != nil {
			rows.Close()
			return taskdomain.GenerationTargetState{}, err
		}
		target.Versions = append(target.Versions, taskdomain.GenerationScheduleVersion{Revision: version.ScheduleRevision, Schedule: schedule, Effective: taskdomain.ScheduleEffectiveRange{From: version.EffectiveFrom, To: version.EffectiveTo}})
		if version.ScheduleRevision == currentVersion {
			target.GenerationEnabled = taskdomain.TaskLifecycleStatus(lifecycle) == taskdomain.TaskLifecycleActive && version.RecurrenceType != taskdomain.RecurrenceNone
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return taskdomain.GenerationTargetState{}, err
	}
	rows.Close()
	rows, err = w.queryer.QueryContext(ctx, `SELECT occurrence_key FROM domain_task_occurrences_v2 WHERE workspace_id=? AND task_id=? ORDER BY occurrence_key`, w.workspaceID, taskID)
	if err != nil {
		return taskdomain.GenerationTargetState{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return taskdomain.GenerationTargetState{}, err
		}
		target.ExistingOccurrenceKeys = append(target.ExistingOccurrenceKeys, key)
	}
	if err := rows.Err(); err != nil {
		return taskdomain.GenerationTargetState{}, err
	}
	return target, nil
}

func (w *sqliteTaskDomainV2ProjectWriter) InsertMissingOccurrences(ctx context.Context, insert taskdomain.GenerationInsert) error {
	if w.closed() {
		return storage.ErrTenantWriteTxClosed
	}
	if insert.WorkspaceID != w.workspaceID || insert.TaskID == "" || insert.ExpectedScheduleRevision < 1 || len(insert.Occurrences) == 0 {
		return taskdomain.ErrInvalidGenerationWorker
	}
	target, err := w.loadGenerationTarget(ctx, insert.TaskID)
	if err != nil {
		return err
	}
	if target.ScheduleRevision != insert.ExpectedScheduleRevision {
		return taskdomain.ErrScheduleRevisionConflict
	}
	versions := make(map[int64]taskdomain.GenerationScheduleVersion, len(target.Versions))
	for _, version := range target.Versions {
		versions[version.Revision] = version
	}
	for _, occurrence := range insert.Occurrences {
		version, exists := versions[occurrence.GeneratedScheduleRevision]
		if !exists || validateGenerationOccurrence(insert, occurrence, version) != nil {
			return taskdomain.ErrInvalidGenerationWorker
		}
	}
	result, err := w.queryer.ExecContext(ctx, `UPDATE domain_task_schedules_v2 SET generation_status='running',generation_error=NULL,generation_retry_at=NULL,
		generation_retry_pending_jobs=0,generation_failed_jobs=0,updated_at=CURRENT_TIMESTAMP WHERE workspace_id=? AND task_id=? AND revision=?`,
		w.workspaceID, insert.TaskID, insert.ExpectedScheduleRevision)
	if err != nil {
		return err
	}
	if err := requireSQLiteTaskDomainV2ScheduleChanged(result); err != nil {
		return err
	}
	for _, occurrence := range insert.Occurrences {
		if _, err := w.queryer.ExecContext(ctx, `INSERT INTO domain_task_occurrences_v2
			(workspace_id,id,task_id,occurrence_key,planned_date,planned_start_at,planned_end_at,execution_status,revision,generated_schedule_revision,manually_overridden,created_at,updated_at)
			VALUES (?,?,?,?,?,?,?,'open',1,?,FALSE,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)
			ON CONFLICT(workspace_id,task_id,occurrence_key) DO NOTHING`,
			w.workspaceID, occurrence.ID, insert.TaskID, occurrence.OccurrenceKey, occurrence.PlannedDate,
			sqliteTaskDomainV2Time(occurrence.PlannedStartAt), sqliteTaskDomainV2Time(occurrence.PlannedEndAt), occurrence.GeneratedScheduleRevision); err != nil {
			return err
		}
	}
	return nil
}

func (w *sqliteTaskDomainV2ProjectWriter) CompleteGeneration(ctx context.Context, completion taskdomain.GenerationCompletion) error {
	if w.closed() {
		return storage.ErrTenantWriteTxClosed
	}
	if completion.WorkspaceID != w.workspaceID || completion.TaskID == "" || completion.ExpectedScheduleRevision < 1 || validateGenerationCompletion(completion) != nil {
		return taskdomain.ErrInvalidGenerationWorker
	}
	target, err := w.loadGenerationTarget(ctx, completion.TaskID)
	if err != nil {
		return err
	}
	if target.ScheduleRevision != completion.ExpectedScheduleRevision {
		return taskdomain.ErrScheduleRevisionConflict
	}
	if target.GenerationWatermark != "" && completion.GenerationWatermark < target.GenerationWatermark {
		return taskdomain.ErrInvalidGenerationWorker
	}
	if completion.Status == taskdomain.GenerationStatusIdle {
		complete, err := generationTargetHasExpectedKeysThrough(target, completion.GenerationWatermark)
		if err != nil || !complete {
			return taskdomain.ErrInvalidGenerationWorker
		}
	}
	result, err := w.queryer.ExecContext(ctx, `UPDATE domain_task_schedules_v2 SET generation_watermark=?,generation_status=?,generation_error=?,generation_retry_at=?,
		generation_retry_pending_jobs=?,generation_failed_jobs=?,updated_at=CURRENT_TIMESTAMP WHERE workspace_id=? AND task_id=? AND revision=?`,
		completion.GenerationWatermark, completion.Status, nullableSQLiteTaskDomainV2String(completion.Error), sqliteTaskDomainV2Time(completion.RetryAt),
		completion.RetryPendingJobs, completion.FailedJobs, w.workspaceID, completion.TaskID, completion.ExpectedScheduleRevision)
	if err != nil {
		return err
	}
	return requireSQLiteTaskDomainV2ScheduleChanged(result)
}

func (w *sqliteTaskDomainV2ProjectWriter) EnsureSystemProjects(ctx context.Context) error {
	if w.closed() {
		return storage.ErrTenantWriteTxClosed
	}
	for _, project := range []taskdomain.Project{
		{
			WorkspaceID: w.workspaceID, ID: taskdomain.SystemInboxProjectID, Name: "Inbox",
			Kind: taskdomain.ProjectKindStandard, Horizon: taskdomain.ProjectHorizonShort,
			Status: taskdomain.ProjectStatusActive, SystemRole: taskdomain.ProjectSystemRoleInbox,
		},
		{
			WorkspaceID: w.workspaceID, ID: taskdomain.PersonalProjectID, Name: "Personal",
			Kind: taskdomain.ProjectKindStandard, Horizon: taskdomain.ProjectHorizonShort,
			Status: taskdomain.ProjectStatusActive, SystemRole: taskdomain.ProjectSystemRolePersonal,
		},
	} {
		if _, err := w.queryer.ExecContext(ctx, `INSERT INTO domain_projects_v2
			(workspace_id,id,name,kind,horizon,status,system_role,revision,created_at,updated_at)
			VALUES (?,?,?,?,?,?,?,1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)
			ON CONFLICT DO NOTHING`, project.WorkspaceID, project.ID, project.Name, project.Kind, project.Horizon, project.Status, project.SystemRole); err != nil {
			return err
		}
	}
	projects := make([]taskdomain.Project, 0, 2)
	for _, id := range []string{taskdomain.SystemInboxProjectID, taskdomain.PersonalProjectID} {
		snapshot, err := getSQLiteTaskDomainV2Project(ctx, w.queryer, w.workspaceID, id)
		if err != nil {
			return taskdomain.ErrInvalidSystemProjectSet
		}
		projects = append(projects, snapshot.Project)
	}
	return taskdomain.ValidateWorkspaceSystemProjects(w.workspaceID, projects)
}

func (w *sqliteTaskDomainV2ProjectWriter) SaveProject(ctx context.Context, write taskdomain.ProjectWrite) error {
	if w.closed() {
		return storage.ErrTenantWriteTxClosed
	}
	if write.ExpectedRevision < 0 || write.Project.WorkspaceID != w.workspaceID {
		return taskdomain.ErrInvalidProject
	}
	if err := taskdomain.ValidateProject(write.Project); err != nil {
		return err
	}
	if write.ExpectedRevision == 0 {
		if write.Project.SystemRole != taskdomain.ProjectSystemRoleNone {
			return taskdomain.ErrSystemProjectImmutable
		}
		result, err := w.queryer.ExecContext(ctx, `INSERT INTO domain_projects_v2
			(workspace_id,id,name,kind,horizon,status,system_role,revision,created_at,updated_at)
			VALUES (?,?,?,?,?,?,NULL,1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)
			ON CONFLICT DO NOTHING`, write.Project.WorkspaceID, write.Project.ID, write.Project.Name, write.Project.Kind, write.Project.Horizon, write.Project.Status)
		if err != nil {
			return err
		}
		return requireSQLiteTaskDomainV2Changed(result)
	}

	current, err := getSQLiteTaskDomainV2Project(ctx, w.queryer, w.workspaceID, write.Project.ID)
	if err != nil {
		return err
	}
	if current.Project.SystemRole != write.Project.SystemRole {
		return taskdomain.ErrSystemProjectImmutable
	}
	result, err := w.queryer.ExecContext(ctx, `UPDATE domain_projects_v2
		SET name=?,kind=?,horizon=?,status=?,updated_at=CURRENT_TIMESTAMP,revision=revision+1
		WHERE workspace_id=? AND id=? AND revision=?`,
		write.Project.Name, write.Project.Kind, write.Project.Horizon, write.Project.Status,
		w.workspaceID, write.Project.ID, write.ExpectedRevision)
	if err != nil {
		return err
	}
	return requireSQLiteTaskDomainV2Changed(result)
}

func (w *sqliteTaskDomainV2ProjectWriter) DeleteProject(ctx context.Context, projectID string, expectedRevision int64) error {
	if w.closed() {
		return storage.ErrTenantWriteTxClosed
	}
	if expectedRevision < 1 {
		return taskdomain.ErrAggregateRevisionConflict
	}
	current, err := getSQLiteTaskDomainV2Project(ctx, w.queryer, w.workspaceID, projectID)
	if err != nil {
		return err
	}
	if err := taskdomain.ValidateProjectDeletion(current.Project); err != nil {
		return err
	}
	result, err := w.queryer.ExecContext(ctx, `DELETE FROM domain_projects_v2 WHERE workspace_id=? AND id=? AND revision=?`, w.workspaceID, projectID, expectedRevision)
	if err != nil {
		return err
	}
	return requireSQLiteTaskDomainV2Changed(result)
}

func (w *sqliteTaskDomainV2ProjectWriter) CreateTaskAggregate(ctx context.Context, snapshot taskdomain.TaskAggregateSnapshot) error {
	if w.closed() {
		return storage.ErrTenantWriteTxClosed
	}
	if snapshot.Task.WorkspaceID != w.workspaceID {
		return taskdomain.ErrInvalidTaskAggregateSnapshot
	}
	if err := taskdomain.ValidateTaskAggregateSnapshot(snapshot); err != nil {
		return err
	}
	if err := w.ensureProjectAcceptsNonTerminalOccurrences(ctx, snapshot.Task.ProjectID); err != nil {
		return err
	}
	if _, err := w.queryer.ExecContext(ctx, `INSERT INTO domain_tasks_v2
		(workspace_id,id,project_id,roadmap_node_id,note_id,title,description,lifecycle_status,priority,sort_order,revision,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`,
		snapshot.Task.WorkspaceID, snapshot.Task.ID, snapshot.Task.ProjectID,
		nullableSQLiteTaskDomainV2String(snapshot.Task.RoadmapNodeID), nullableSQLiteTaskDomainV2String(snapshot.Task.NoteID),
		snapshot.Task.Title, snapshot.Task.Description, snapshot.Task.LifecycleStatus,
		snapshot.Task.Priority, snapshot.Task.SortOrder, snapshot.Task.Revision,
	); err != nil {
		return err
	}
	if _, err := w.queryer.ExecContext(ctx, `INSERT INTO domain_task_schedules_v2
		(workspace_id,task_id,revision,current_schedule_revision,generation_status,updated_at)
		VALUES (?,?,?,?, 'idle',CURRENT_TIMESTAMP)`,
		snapshot.Schedule.WorkspaceID, snapshot.Schedule.TaskID, snapshot.Schedule.Revision, snapshot.Schedule.CurrentScheduleRevision,
	); err != nil {
		return err
	}
	for _, version := range snapshot.Versions {
		if _, err := w.queryer.ExecContext(ctx, `INSERT INTO domain_task_schedule_versions_v2
			(workspace_id,task_id,schedule_revision,effective_from,effective_to,recurrence_type,timing_type,timezone,starts_on,ends_on,recurrence_rule,local_start_time,duration_minutes,created_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,CURRENT_TIMESTAMP)`,
			version.WorkspaceID, version.TaskID, version.ScheduleRevision,
			nullableSQLiteTaskDomainV2String(version.EffectiveFrom), nullableSQLiteTaskDomainV2String(version.EffectiveTo),
			version.RecurrenceType, version.TimingType, version.Timezone,
			nullableSQLiteTaskDomainV2String(version.StartsOn), nullableSQLiteTaskDomainV2String(version.EndsOn), version.RecurrenceRule,
			nullableSQLiteTaskDomainV2String(version.LocalStartTime), nullableSQLiteTaskDomainV2PositiveInt(version.DurationMinutes),
		); err != nil {
			return err
		}
	}
	for _, occurrence := range snapshot.Occurrences {
		if _, err := w.queryer.ExecContext(ctx, `INSERT INTO domain_task_occurrences_v2
			(workspace_id,id,task_id,occurrence_key,planned_date,planned_start_at,planned_end_at,due_at,execution_status,note_id,all_day_end_date,revision,generated_schedule_revision,manually_overridden,created_at,updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,FALSE,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)
			ON CONFLICT(workspace_id,task_id,occurrence_key) DO NOTHING`,
			occurrence.WorkspaceID, occurrence.ID, occurrence.TaskID, occurrence.OccurrenceKey,
			nullableSQLiteTaskDomainV2String(occurrence.PlannedDate), sqliteTaskDomainV2Time(occurrence.PlannedStartAt),
			sqliteTaskDomainV2Time(occurrence.PlannedEndAt), sqliteTaskDomainV2Time(occurrence.DueAt), occurrence.ExecutionStatus,
			nullableSQLiteTaskDomainV2String(occurrence.NoteID), nullableSQLiteTaskDomainV2String(occurrence.AllDayEndDate),
			occurrence.Revision, occurrence.GeneratedScheduleRevision,
		); err != nil {
			return err
		}
	}
	return nil
}

func (w *sqliteTaskDomainV2ProjectWriter) SaveTaskAggregate(ctx context.Context, write taskdomain.TaskAggregateWrite) error {
	if w.closed() {
		return storage.ErrTenantWriteTxClosed
	}
	if write.Aggregate.WorkspaceID != w.workspaceID {
		return taskdomain.ErrInvalidTaskAggregateSnapshot
	}
	if err := taskdomain.ValidateTaskAggregateWrite(write); err != nil {
		return err
	}
	if write.Task != nil {
		if err := w.validateTaskAttributeReferences(ctx, *write.Task); err != nil {
			return err
		}
	}
	if write.ExpectedScheduleRevision > 0 {
		if err := requireSQLiteTaskDomainV2ScheduleRevision(ctx, w.queryer, w.workspaceID, write.Aggregate.TaskID, write.ExpectedScheduleRevision); err != nil {
			return err
		}
	}
	if write.Task != nil && taskAggregateHasNonTerminalOccurrence(write.Aggregate) {
		if err := w.ensureProjectAcceptsNonTerminalOccurrences(ctx, write.Task.ProjectID); err != nil {
			return err
		}
	} else if taskAggregateWriteHasNonTerminalOccurrence(write) {
		if err := w.ensureTaskProjectAcceptsNonTerminalOccurrences(ctx, write.Aggregate.TaskID); err != nil {
			return err
		}
	}
	var result sql.Result
	var err error
	if write.Task == nil {
		result, err = w.queryer.ExecContext(ctx, `UPDATE domain_tasks_v2
			SET lifecycle_status=?,revision=revision+1,updated_at=CURRENT_TIMESTAMP
			WHERE workspace_id=? AND id=? AND revision=?`,
			write.Aggregate.LifecycleStatus, w.workspaceID, write.Aggregate.TaskID, write.ExpectedRevisions.Task)
	} else {
		task := *write.Task
		result, err = w.queryer.ExecContext(ctx, `UPDATE domain_tasks_v2 SET
			project_id=?,roadmap_node_id=?,note_id=?,title=?,description=?,priority=?,sort_order=?,lifecycle_status=?,
			revision=revision+1,updated_at=CURRENT_TIMESTAMP
			WHERE workspace_id=? AND id=? AND revision=?`,
			task.ProjectID, nullableSQLiteTaskDomainV2String(task.RoadmapNodeID), nullableSQLiteTaskDomainV2String(task.NoteID),
			task.Title, task.Description, task.Priority, task.SortOrder, task.LifecycleStatus,
			w.workspaceID, write.Aggregate.TaskID, write.ExpectedRevisions.Task)
	}
	if err != nil {
		return err
	}
	if err := requireSQLiteTaskDomainV2Changed(result); err != nil {
		return err
	}
	targets := make(map[string]taskdomain.Occurrence, len(write.Aggregate.Occurrences))
	for _, occurrence := range write.Aggregate.Occurrences {
		targets[occurrence.ID] = occurrence
	}
	for occurrenceID, expectedRevision := range write.ExpectedRevisions.Occurrences {
		occurrence := targets[occurrenceID]
		result, err := w.queryer.ExecContext(ctx, `UPDATE domain_task_occurrences_v2
			SET execution_status=?,actual_start_at=?,completed_at=?,blocked_reason=?,next_action=?,revision=revision+1,updated_at=CURRENT_TIMESTAMP
			WHERE workspace_id=? AND id=? AND task_id=? AND revision=?`,
			occurrence.ExecutionStatus, sqliteTaskDomainV2Time(occurrence.ActualStartAt), sqliteTaskDomainV2Time(occurrence.CompletedAt),
			nullableSQLiteTaskDomainV2String(occurrence.BlockedReason), nullableSQLiteTaskDomainV2String(occurrence.NextAction),
			w.workspaceID, occurrenceID, write.Aggregate.TaskID, expectedRevision)
		if err != nil {
			return err
		}
		if err := requireSQLiteTaskDomainV2Changed(result); err != nil {
			return err
		}
	}
	for _, log := range write.ExecutionLogs {
		if _, err := w.queryer.ExecContext(ctx, `INSERT INTO domain_task_execution_logs_v2
			(workspace_id,id,occurrence_id,from_status,to_status,blocked_reason,next_action,actor_id,metadata,created_at)
			VALUES (?,?,?,?,?,?,?,?,?,?)`,
			w.workspaceID, log.ID(), log.OccurrenceID(), log.FromStatus(), log.ToStatus(),
			nullableSQLiteTaskDomainV2String(log.BlockedReason()), nullableSQLiteTaskDomainV2String(log.NextAction()),
			log.ActorID(), `{}`, log.CreatedAt().UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	return nil
}

func (w *sqliteTaskDomainV2ProjectWriter) validateTaskAttributeReferences(ctx context.Context, task taskdomain.TaskRecord) error {
	var projectExists bool
	if err := w.queryer.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM domain_projects_v2 WHERE workspace_id=? AND id=?
	)`, w.workspaceID, task.ProjectID).Scan(&projectExists); err != nil {
		return err
	}
	if !projectExists {
		return taskdomain.ErrInvalidTaskAggregateSnapshot
	}
	if task.NoteID != "" {
		var noteExists bool
		if err := w.queryer.QueryRowContext(ctx, `SELECT EXISTS(
			SELECT 1 FROM notes WHERE workspace_id=? AND id=?
		)`, w.workspaceID, task.NoteID).Scan(&noteExists); err != nil {
			return err
		}
		if !noteExists {
			return taskdomain.ErrInvalidTaskAggregateSnapshot
		}
	}
	if task.RoadmapNodeID != "" {
		var nodeExists bool
		if err := w.queryer.QueryRowContext(ctx, `SELECT EXISTS(
			SELECT 1 FROM domain_roadmap_nodes_v2 WHERE workspace_id=? AND id=? AND project_id=?
		)`, w.workspaceID, task.RoadmapNodeID, task.ProjectID).Scan(&nodeExists); err != nil {
			return err
		}
		if !nodeExists {
			return taskdomain.ErrInvalidTaskAggregateSnapshot
		}
	}
	return nil
}

func (w *sqliteTaskDomainV2ProjectWriter) ensureProjectAcceptsNonTerminalOccurrences(ctx context.Context, projectID string) error {
	var status taskdomain.ProjectStatus
	err := w.queryer.QueryRowContext(ctx, `SELECT status FROM domain_projects_v2 WHERE workspace_id=? AND id=?`, w.workspaceID, projectID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return taskdomain.ErrProjectNotFound
	}
	if err != nil {
		return err
	}
	if status == taskdomain.ProjectStatusCompleted || status == taskdomain.ProjectStatusArchived {
		return taskdomain.ErrInvalidTaskAggregateSnapshot
	}
	return nil
}

func (w *sqliteTaskDomainV2ProjectWriter) ensureTaskProjectAcceptsNonTerminalOccurrences(ctx context.Context, taskID string) error {
	var projectID string
	err := w.queryer.QueryRowContext(ctx, `SELECT project_id FROM domain_tasks_v2 WHERE workspace_id=? AND id=?`, w.workspaceID, taskID).Scan(&projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return taskdomain.ErrTaskNotFound
	}
	if err != nil {
		return err
	}
	return w.ensureProjectAcceptsNonTerminalOccurrences(ctx, projectID)
}

func taskAggregateWriteHasNonTerminalOccurrence(write taskdomain.TaskAggregateWrite) bool {
	for occurrenceID := range write.ExpectedRevisions.Occurrences {
		for _, occurrence := range write.Aggregate.Occurrences {
			if occurrence.ID == occurrenceID && (occurrence.ExecutionStatus == taskdomain.ExecutionStatusOpen ||
				occurrence.ExecutionStatus == taskdomain.ExecutionStatusActive || occurrence.ExecutionStatus == taskdomain.ExecutionStatusBlocked) {
				return true
			}
		}
	}
	return false
}

func taskAggregateHasNonTerminalOccurrence(aggregate taskdomain.TaskAggregate) bool {
	for _, occurrence := range aggregate.Occurrences {
		if occurrence.ExecutionStatus == taskdomain.ExecutionStatusOpen || occurrence.ExecutionStatus == taskdomain.ExecutionStatusActive ||
			occurrence.ExecutionStatus == taskdomain.ExecutionStatusBlocked {
			return true
		}
	}
	return false
}

func normalizeGenerationScheduleVersion(version taskdomain.ScheduleVersion) (taskdomain.Schedule, error) {
	return taskdomain.NormalizeSchedule(taskdomain.ScheduleInput{
		RecurrenceType: version.RecurrenceType, TimingType: version.TimingType, Timezone: version.Timezone,
		StartsOn: version.StartsOn, EndsOn: version.EndsOn, Rule: json.RawMessage(version.RecurrenceRule),
		LocalStartTime: version.LocalStartTime, DurationMinutes: version.DurationMinutes,
	})
}

func validateGenerationOccurrence(insert taskdomain.GenerationInsert, occurrence taskdomain.GenerationOccurrence, version taskdomain.GenerationScheduleVersion) error {
	if occurrence.WorkspaceID != insert.WorkspaceID || occurrence.TaskID != insert.TaskID || occurrence.ID == "" ||
		occurrence.OccurrenceKey == "" || occurrence.PlannedDate == "" ||
		occurrence.ID != taskdomain.DeterministicOccurrenceID(insert.WorkspaceID, insert.TaskID, occurrence.OccurrenceKey) {
		return taskdomain.ErrInvalidGenerationWorker
	}
	localDate := occurrence.OccurrenceKey
	if occurrence.OccurrenceKey == "once" {
		localDate = version.Schedule.StartsOn
	}
	if occurrence.PlannedDate != localDate {
		return taskdomain.ErrInvalidGenerationWorker
	}
	switch version.Schedule.TimingType {
	case taskdomain.TimingDate:
		if occurrence.PlannedStartAt != nil || occurrence.PlannedEndAt != nil {
			return taskdomain.ErrInvalidGenerationWorker
		}
	case taskdomain.TimingTimeBlock:
		if occurrence.PlannedStartAt == nil || occurrence.PlannedEndAt == nil {
			return taskdomain.ErrInvalidGenerationWorker
		}
		rangeValue, _, err := taskdomain.ResolveTimeBlockUTC(localDate, version.Schedule.LocalStartTime, version.Schedule.Timezone, version.Schedule.DurationMinutes, nil)
		if err != nil || !occurrence.PlannedStartAt.Equal(rangeValue.StartUTC) || !occurrence.PlannedEndAt.Equal(rangeValue.EndUTC) {
			return taskdomain.ErrInvalidGenerationWorker
		}
	default:
		return taskdomain.ErrInvalidGenerationWorker
	}
	return nil
}

func validateGenerationCompletion(completion taskdomain.GenerationCompletion) error {
	if _, err := time.Parse("2006-01-02", completion.GenerationWatermark); err != nil || completion.RetryPendingJobs < 0 || completion.FailedJobs < 0 {
		return taskdomain.ErrInvalidGenerationWorker
	}
	switch completion.Status {
	case taskdomain.GenerationStatusIdle, taskdomain.GenerationStatusRunning:
		if completion.Error != "" || completion.RetryAt != nil || completion.RetryPendingJobs != 0 || completion.FailedJobs != 0 {
			return taskdomain.ErrInvalidGenerationWorker
		}
	case taskdomain.GenerationStatusRetryPending:
		if strings.TrimSpace(completion.Error) == "" || completion.RetryAt == nil || completion.RetryPendingJobs < 1 {
			return taskdomain.ErrInvalidGenerationWorker
		}
	case taskdomain.GenerationStatusFailed:
		if strings.TrimSpace(completion.Error) == "" || completion.RetryAt != nil || completion.RetryPendingJobs != 0 || completion.FailedJobs < 1 {
			return taskdomain.ErrInvalidGenerationWorker
		}
	default:
		return taskdomain.ErrInvalidGenerationWorker
	}
	return nil
}

func generationTargetHasExpectedKeysThrough(target taskdomain.GenerationTargetState, watermark string) (bool, error) {
	watermarkDate, err := time.Parse("2006-01-02", watermark)
	if err != nil || len(target.Versions) == 0 {
		return false, taskdomain.ErrInvalidGenerationWorker
	}
	earliest := ""
	for _, version := range target.Versions {
		if earliest == "" || version.Effective.From < earliest {
			earliest = version.Effective.From
		}
	}
	window := taskdomain.OccurrenceWindow{From: earliest, To: watermarkDate.AddDate(0, 0, 1).Format("2006-01-02")}
	expected := make(map[string]struct{})
	for _, version := range target.Versions {
		keys, err := taskdomain.CalculateOccurrenceKeys(version.Schedule, version.Effective, window)
		if err != nil {
			return false, err
		}
		for _, key := range keys {
			expected[key] = struct{}{}
		}
	}
	existing := make(map[string]struct{}, len(target.ExistingOccurrenceKeys))
	for _, key := range target.ExistingOccurrenceKeys {
		existing[key] = struct{}{}
	}
	for key := range expected {
		if _, ok := existing[key]; !ok {
			return false, nil
		}
	}
	return true, nil
}

func (w *sqliteTaskDomainV2ProjectWriter) InstallScheduleVersion(ctx context.Context, install taskdomain.ScheduleVersionInstall) error {
	if w.closed() {
		return storage.ErrTenantWriteTxClosed
	}
	if install.WorkspaceID != w.workspaceID {
		return taskdomain.ErrInvalidTaskAggregateSnapshot
	}
	if err := taskdomain.ValidateScheduleVersionInstall(install); err != nil {
		return err
	}
	result, err := w.queryer.ExecContext(ctx, `UPDATE domain_task_schedules_v2
		SET revision=revision+1,current_schedule_revision=?,updated_at=CURRENT_TIMESTAMP
		WHERE workspace_id=? AND task_id=? AND revision=?`,
		install.Version.ScheduleRevision, w.workspaceID, install.TaskID, install.ExpectedScheduleRevision)
	if err != nil {
		return err
	}
	if err := requireSQLiteTaskDomainV2Changed(result); err != nil {
		return err
	}
	result, err = w.queryer.ExecContext(ctx, `UPDATE domain_task_schedule_versions_v2
		SET effective_to=? WHERE workspace_id=? AND task_id=? AND effective_to IS NULL`,
		install.Version.EffectiveFrom, w.workspaceID, install.TaskID)
	if err != nil {
		return err
	}
	if err := requireSQLiteTaskDomainV2Changed(result); err != nil {
		return err
	}
	version := install.Version
	_, err = w.queryer.ExecContext(ctx, `INSERT INTO domain_task_schedule_versions_v2
		(workspace_id,task_id,schedule_revision,effective_from,effective_to,recurrence_type,timing_type,timezone,starts_on,ends_on,recurrence_rule,local_start_time,duration_minutes,created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,CURRENT_TIMESTAMP)`,
		version.WorkspaceID, version.TaskID, version.ScheduleRevision,
		nullableSQLiteTaskDomainV2String(version.EffectiveFrom), nullableSQLiteTaskDomainV2String(version.EffectiveTo),
		version.RecurrenceType, version.TimingType, version.Timezone,
		nullableSQLiteTaskDomainV2String(version.StartsOn), nullableSQLiteTaskDomainV2String(version.EndsOn), version.RecurrenceRule,
		nullableSQLiteTaskDomainV2String(version.LocalStartTime), nullableSQLiteTaskDomainV2PositiveInt(version.DurationMinutes))
	return err
}

func (w *sqliteTaskDomainV2ProjectWriter) ApplyOccurrenceReschedule(ctx context.Context, write taskdomain.OccurrenceRescheduleWrite) error {
	if w.closed() {
		return storage.ErrTenantWriteTxClosed
	}
	if write.WorkspaceID != w.workspaceID || write.TaskID == "" || write.ExpectedTaskRevision < 1 ||
		write.ExpectedScheduleRevision < 1 || write.ExpectedOccurrenceRevision < 1 ||
		write.After.Record.WorkspaceID != w.workspaceID || write.After.Record.TaskID != write.TaskID || write.After.Record.ID == "" ||
		write.After.Record.Revision != write.ExpectedOccurrenceRevision+1 || !write.After.ManuallyOverridden {
		return taskdomain.ErrInvalidScheduleCommand
	}
	if err := requireSQLiteTaskDomainV2TaskRevision(ctx, w.queryer, w.workspaceID, write.TaskID, write.ExpectedTaskRevision); err != nil {
		return err
	}
	if err := requireSQLiteTaskDomainV2ScheduleRevision(ctx, w.queryer, w.workspaceID, write.TaskID, write.ExpectedScheduleRevision); err != nil {
		return err
	}
	record := write.After.Record
	result, err := w.queryer.ExecContext(ctx, `UPDATE domain_task_occurrences_v2 SET
		planned_date=?,planned_start_at=?,planned_end_at=?,due_at=?,note_id=?,all_day_end_date=?,revision=revision+1,
		manually_overridden=TRUE,updated_at=CURRENT_TIMESTAMP
		WHERE workspace_id=? AND task_id=? AND id=? AND revision=?`,
		nullableSQLiteTaskDomainV2String(record.PlannedDate), sqliteTaskDomainV2Time(record.PlannedStartAt), sqliteTaskDomainV2Time(record.PlannedEndAt),
		sqliteTaskDomainV2Time(record.DueAt), nullableSQLiteTaskDomainV2String(record.NoteID), nullableSQLiteTaskDomainV2String(record.AllDayEndDate),
		w.workspaceID, write.TaskID, record.ID, write.ExpectedOccurrenceRevision)
	if err != nil {
		return err
	}
	return requireSQLiteTaskDomainV2OccurrenceChanged(result)
}

func (w *sqliteTaskDomainV2ProjectWriter) ApplyScheduleVersionChange(ctx context.Context, write taskdomain.ScheduleVersionChangeWrite) error {
	if w.closed() {
		return storage.ErrTenantWriteTxClosed
	}
	if err := validateSQLiteTaskDomainV2ScheduleVersionChange(w.workspaceID, write); err != nil {
		return err
	}
	if len(write.UpsertOccurrences) > 0 {
		if err := w.ensureTaskProjectAcceptsNonTerminalOccurrences(ctx, write.TaskID); err != nil {
			return err
		}
	}
	if err := requireSQLiteTaskDomainV2TaskRevision(ctx, w.queryer, w.workspaceID, write.TaskID, write.ExpectedTaskRevision); err != nil {
		return err
	}
	result, err := w.queryer.ExecContext(ctx, `UPDATE domain_task_schedules_v2 SET revision=revision+1,current_schedule_revision=?,updated_at=CURRENT_TIMESTAMP
		WHERE workspace_id=? AND task_id=? AND revision=? AND current_schedule_revision=?`,
		write.NewVersion.ScheduleRevision, w.workspaceID, write.TaskID, write.ExpectedScheduleRevision, write.ClosedVersion.ScheduleRevision)
	if err != nil {
		return err
	}
	if err := requireSQLiteTaskDomainV2ScheduleChanged(result); err != nil {
		return err
	}
	result, err = w.queryer.ExecContext(ctx, `UPDATE domain_task_schedule_versions_v2 SET effective_to=?
		WHERE workspace_id=? AND task_id=? AND schedule_revision=? AND effective_to IS NULL`,
		write.ClosedVersion.EffectiveTo, w.workspaceID, write.TaskID, write.ClosedVersion.ScheduleRevision)
	if err != nil {
		return err
	}
	if err := requireSQLiteTaskDomainV2ScheduleChanged(result); err != nil {
		return err
	}
	version := write.NewVersion
	if _, err := w.queryer.ExecContext(ctx, `INSERT INTO domain_task_schedule_versions_v2
		(workspace_id,task_id,schedule_revision,effective_from,effective_to,recurrence_type,timing_type,timezone,starts_on,ends_on,recurrence_rule,local_start_time,duration_minutes,created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,CURRENT_TIMESTAMP)`,
		version.WorkspaceID, version.TaskID, version.ScheduleRevision, nullableSQLiteTaskDomainV2String(version.EffectiveFrom),
		nullableSQLiteTaskDomainV2String(version.EffectiveTo), version.RecurrenceType, version.TimingType, version.Timezone,
		nullableSQLiteTaskDomainV2String(version.StartsOn), nullableSQLiteTaskDomainV2String(version.EndsOn), version.RecurrenceRule,
		nullableSQLiteTaskDomainV2String(version.LocalStartTime), nullableSQLiteTaskDomainV2PositiveInt(version.DurationMinutes)); err != nil {
		return err
	}
	for _, occurrence := range write.UpsertOccurrences {
		record := occurrence.Record
		expectedRevision, exists := write.ExpectedOccurrenceRevisions[record.ID]
		if exists {
			result, err := w.queryer.ExecContext(ctx, `UPDATE domain_task_occurrences_v2 SET
				occurrence_key=?,planned_date=?,planned_start_at=?,planned_end_at=?,due_at=?,execution_status=?,actual_start_at=?,completed_at=?,
				note_id=?,all_day_end_date=?,blocked_reason=NULL,next_action=NULL,revision=revision+1,generated_schedule_revision=?,
				manually_overridden=?,updated_at=CURRENT_TIMESTAMP
				WHERE workspace_id=? AND task_id=? AND id=? AND revision=?`,
				record.OccurrenceKey, nullableSQLiteTaskDomainV2String(record.PlannedDate), sqliteTaskDomainV2Time(record.PlannedStartAt),
				sqliteTaskDomainV2Time(record.PlannedEndAt), sqliteTaskDomainV2Time(record.DueAt), record.ExecutionStatus,
				sqliteTaskDomainV2Time(occurrence.ActualStartAt), sqliteTaskDomainV2Time(occurrence.CompletedAt), nullableSQLiteTaskDomainV2String(record.NoteID),
				nullableSQLiteTaskDomainV2String(record.AllDayEndDate), record.GeneratedScheduleRevision, occurrence.ManuallyOverridden,
				w.workspaceID, write.TaskID, record.ID, expectedRevision)
			if err != nil {
				return err
			}
			if err := requireSQLiteTaskDomainV2OccurrenceChanged(result); err != nil {
				return err
			}
			continue
		}
		if _, err := w.queryer.ExecContext(ctx, `INSERT INTO domain_task_occurrences_v2
			(workspace_id,id,task_id,occurrence_key,planned_date,planned_start_at,planned_end_at,due_at,execution_status,
			actual_start_at,completed_at,note_id,all_day_end_date,revision,generated_schedule_revision,manually_overridden,created_at,updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`,
			w.workspaceID, record.ID, write.TaskID, record.OccurrenceKey, nullableSQLiteTaskDomainV2String(record.PlannedDate),
			sqliteTaskDomainV2Time(record.PlannedStartAt), sqliteTaskDomainV2Time(record.PlannedEndAt), sqliteTaskDomainV2Time(record.DueAt),
			record.ExecutionStatus, sqliteTaskDomainV2Time(occurrence.ActualStartAt), sqliteTaskDomainV2Time(occurrence.CompletedAt),
			nullableSQLiteTaskDomainV2String(record.NoteID), nullableSQLiteTaskDomainV2String(record.AllDayEndDate), record.Revision,
			record.GeneratedScheduleRevision, occurrence.ManuallyOverridden); err != nil {
			return err
		}
	}
	for occurrenceID, expectedRevision := range write.DeleteOccurrenceRevisions {
		result, err := w.queryer.ExecContext(ctx, `DELETE FROM domain_task_occurrences_v2
			WHERE workspace_id=? AND task_id=? AND id=? AND revision=?`, w.workspaceID, write.TaskID, occurrenceID, expectedRevision)
		if err != nil {
			return err
		}
		if err := requireSQLiteTaskDomainV2OccurrenceChanged(result); err != nil {
			return err
		}
	}
	return nil
}

func validateSQLiteTaskDomainV2ScheduleVersionChange(workspaceID string, write taskdomain.ScheduleVersionChangeWrite) error {
	if write.WorkspaceID != workspaceID || write.TaskID == "" || write.ExpectedTaskRevision < 1 || write.ExpectedScheduleRevision < 1 ||
		write.Schedule.WorkspaceID != workspaceID || write.Schedule.TaskID != write.TaskID || write.Schedule.Revision != write.ExpectedScheduleRevision+1 ||
		write.ClosedVersion.WorkspaceID != workspaceID || write.ClosedVersion.TaskID != write.TaskID || write.ClosedVersion.EffectiveTo == "" ||
		write.NewVersion.WorkspaceID != workspaceID || write.NewVersion.TaskID != write.TaskID ||
		write.Schedule.CurrentScheduleRevision != write.NewVersion.ScheduleRevision || write.ClosedVersion.EffectiveTo != write.NewVersion.EffectiveFrom {
		return taskdomain.ErrInvalidScheduleCommand
	}
	if err := taskdomain.ValidateScheduleVersionInstall(taskdomain.ScheduleVersionInstall{
		WorkspaceID: workspaceID, TaskID: write.TaskID, ExpectedScheduleRevision: write.ExpectedScheduleRevision, Version: write.NewVersion,
	}); err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(write.ExpectedOccurrenceRevisions))
	for _, occurrence := range write.UpsertOccurrences {
		record := occurrence.Record
		if record.WorkspaceID != workspaceID || record.TaskID != write.TaskID || record.ID == "" || record.OccurrenceKey == "" ||
			record.GeneratedScheduleRevision != write.NewVersion.ScheduleRevision {
			return taskdomain.ErrInvalidScheduleCommand
		}
		if expected, exists := write.ExpectedOccurrenceRevisions[record.ID]; exists {
			if expected < 1 || record.Revision != expected+1 {
				return taskdomain.ErrInvalidScheduleCommand
			}
			seen[record.ID] = struct{}{}
		} else if record.Revision != 1 {
			return taskdomain.ErrInvalidScheduleCommand
		}
	}
	for occurrenceID, expected := range write.DeleteOccurrenceRevisions {
		if expected < 1 || write.ExpectedOccurrenceRevisions[occurrenceID] != expected {
			return taskdomain.ErrInvalidScheduleCommand
		}
		seen[occurrenceID] = struct{}{}
	}
	if len(seen) != len(write.ExpectedOccurrenceRevisions) {
		return taskdomain.ErrInvalidScheduleCommand
	}
	for _, occurrenceID := range write.PreservedOccurrenceIDs {
		if _, mutated := write.ExpectedOccurrenceRevisions[occurrenceID]; mutated {
			return taskdomain.ErrInvalidScheduleCommand
		}
	}
	return nil
}

func requireSQLiteTaskDomainV2TaskRevision(ctx context.Context, queryer sqliteTaskDomainV2Queryer, workspaceID, taskID string, expected int64) error {
	var revision int64
	err := queryer.QueryRowContext(ctx, `SELECT revision FROM domain_tasks_v2 WHERE workspace_id=? AND id=?`, workspaceID, taskID).Scan(&revision)
	if err != nil || revision != expected {
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		return taskdomain.ErrTaskRevisionConflict
	}
	return nil
}

func requireSQLiteTaskDomainV2ScheduleRevision(ctx context.Context, queryer sqliteTaskDomainV2Queryer, workspaceID, taskID string, expected int64) error {
	var revision int64
	err := queryer.QueryRowContext(ctx, `SELECT revision FROM domain_task_schedules_v2 WHERE workspace_id=? AND task_id=?`, workspaceID, taskID).Scan(&revision)
	if err != nil || revision != expected {
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		return taskdomain.ErrScheduleRevisionConflict
	}
	return nil
}

func requireSQLiteTaskDomainV2ScheduleChanged(result sql.Result) error {
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return taskdomain.ErrScheduleRevisionConflict
	}
	return nil
}

func requireSQLiteTaskDomainV2OccurrenceChanged(result sql.Result) error {
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return taskdomain.ErrOccurrenceRevisionConflict
	}
	return nil
}

func (w *sqliteTaskDomainV2ProjectWriter) closed() bool {
	return w.isClosed == nil || w.isClosed()
}

func getSQLiteTaskDomainV2Project(ctx context.Context, queryer sqliteTaskDomainV2Queryer, workspaceID, projectID string) (taskdomain.ProjectSnapshot, error) {
	var snapshot taskdomain.ProjectSnapshot
	var kind, horizon, status string
	var systemRole sql.NullString
	err := queryer.QueryRowContext(ctx, `SELECT id,name,kind,horizon,status,system_role,revision
		FROM domain_projects_v2 WHERE workspace_id=? AND id=?`, workspaceID, projectID).Scan(
		&snapshot.Project.ID, &snapshot.Project.Name, &kind, &horizon, &status, &systemRole, &snapshot.Revision,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return taskdomain.ProjectSnapshot{}, taskdomain.ErrProjectNotFound
	}
	if err != nil {
		return taskdomain.ProjectSnapshot{}, err
	}
	snapshot.Project.WorkspaceID = workspaceID
	snapshot.Project.Kind = taskdomain.ProjectKind(kind)
	snapshot.Project.Horizon = taskdomain.ProjectHorizon(horizon)
	snapshot.Project.Status = taskdomain.ProjectStatus(status)
	if systemRole.Valid {
		snapshot.Project.SystemRole = taskdomain.ProjectSystemRole(systemRole.String)
	}
	return snapshot, nil
}

func requireSQLiteTaskDomainV2Changed(result sql.Result) error {
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return taskdomain.ErrAggregateRevisionConflict
	}
	return nil
}

func nullableSQLiteTaskDomainV2String(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableSQLiteTaskDomainV2PositiveInt(value int) any {
	if value == 0 {
		return nil
	}
	return value
}

func sqliteTaskDomainV2Time(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC().Format(time.RFC3339Nano)
}

var _ taskdomain.ProjectReader = (*sqliteTaskDomainV2ProjectReader)(nil)
var _ taskdomain.TaskDomainReader = (*sqliteTaskDomainV2ProjectReader)(nil)
var _ taskdomain.ScheduleCommandStateReader = (*sqliteTaskDomainV2ProjectReader)(nil)
var _ taskdomain.TaskDomainWriter = (*sqliteTaskDomainV2ProjectWriter)(nil)
var _ taskdomain.ScheduleCommandWriter = (*sqliteTaskDomainV2ProjectWriter)(nil)
