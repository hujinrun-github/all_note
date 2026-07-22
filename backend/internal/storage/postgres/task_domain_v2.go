package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/taskdomain"
)

type postgresTaskDomainV2Queryer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type postgresTaskDomainV2ProjectReader struct {
	queryer     postgresTaskDomainV2Queryer
	workspaceID string
}

func newTaskDomainV2ProjectReader(db *sql.DB, workspaceID string) taskdomain.TaskDomainReader {
	return &postgresTaskDomainV2ProjectReader{queryer: db, workspaceID: workspaceID}
}

func (r *postgresTaskDomainV2ProjectReader) GetProject(ctx context.Context, projectID string) (taskdomain.ProjectSnapshot, error) {
	return getPostgresTaskDomainV2Project(ctx, r.queryer, r.workspaceID, projectID)
}

func (r *postgresTaskDomainV2ProjectReader) ListProjects(ctx context.Context, filter taskdomain.ProjectListFilter) ([]taskdomain.ProjectSnapshot, error) {
	if err := taskdomain.ValidateProjectListFilter(filter); err != nil {
		return nil, err
	}
	args := []any{r.workspaceID}
	conditions := []string{"workspace_id=$1"}
	add := func(column string, value any) {
		args = append(args, value)
		conditions = append(conditions, fmt.Sprintf("%s=$%d", column, len(args)))
	}
	if filter.Kind != nil {
		add("kind", *filter.Kind)
	}
	if filter.Horizon != nil {
		add("horizon", *filter.Horizon)
	}
	if filter.Status != nil {
		add("status", *filter.Status)
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

func (r *postgresTaskDomainV2ProjectReader) ListTaskDefinitions(ctx context.Context, filter taskdomain.TaskDefinitionListFilter) ([]taskdomain.TaskDefinitionSnapshot, error) {
	if err := taskdomain.ValidateTaskDefinitionListFilter(filter); err != nil {
		return nil, err
	}
	args := []any{r.workspaceID}
	conditions := []string{"t.workspace_id=$1"}
	add := func(column string, value any) {
		args = append(args, value)
		conditions = append(conditions, fmt.Sprintf("%s=$%d", column, len(args)))
	}
	if filter.ProjectID != "" {
		add("t.project_id", filter.ProjectID)
	}
	if filter.LifecycleStatus != nil {
		add("t.lifecycle_status", *filter.LifecycleStatus)
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

func (r *postgresTaskDomainV2ProjectReader) GetScheduleCommandState(ctx context.Context, taskID string) (taskdomain.ScheduleCommandState, error) {
	state := taskdomain.ScheduleCommandState{WorkspaceID: r.workspaceID, TaskID: taskID}
	err := r.queryer.QueryRowContext(ctx, `SELECT t.revision,s.revision,s.current_schedule_revision
		FROM domain_tasks_v2 t JOIN domain_task_schedules_v2 s ON s.workspace_id=t.workspace_id AND s.task_id=t.id
		WHERE t.workspace_id=$1 AND t.id=$2`, r.workspaceID, taskID).Scan(
		&state.TaskRevision, &state.Schedule.Revision, &state.Schedule.CurrentScheduleRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return taskdomain.ScheduleCommandState{}, taskdomain.ErrTaskNotFound
	}
	if err != nil {
		return taskdomain.ScheduleCommandState{}, err
	}
	state.Schedule.WorkspaceID = r.workspaceID
	state.Schedule.TaskID = taskID
	rows, err := r.queryer.QueryContext(ctx, `SELECT schedule_revision,effective_from::text,effective_to::text,recurrence_type,timing_type,timezone,
		starts_on::text,ends_on::text,recurrence_rule::text,local_start_time::text,duration_minutes
		FROM domain_task_schedule_versions_v2 WHERE workspace_id=$1 AND task_id=$2 ORDER BY schedule_revision`, r.workspaceID, taskID)
	if err != nil {
		return taskdomain.ScheduleCommandState{}, err
	}
	for rows.Next() {
		version, err := scanPostgresTaskDomainV2ScheduleVersion(rows, r.workspaceID, taskID)
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
	rows, err = r.queryer.QueryContext(ctx, `SELECT id,occurrence_key,planned_date::text,planned_start_at,planned_end_at,due_at,
		execution_status,note_id,all_day_end_date::text,revision,generated_schedule_revision,actual_start_at,completed_at,manually_overridden
		FROM domain_task_occurrences_v2 WHERE workspace_id=$1 AND task_id=$2 ORDER BY occurrence_key,id`, r.workspaceID, taskID)
	if err != nil {
		return taskdomain.ScheduleCommandState{}, err
	}
	defer rows.Close()
	for rows.Next() {
		occurrence, err := scanPostgresTaskDomainV2ScheduleOccurrence(rows, r.workspaceID, taskID)
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

func (r *postgresTaskDomainV2ProjectReader) GetTaskAggregate(ctx context.Context, taskID string) (taskdomain.TaskAggregateQueryResult, error) {
	var result taskdomain.TaskAggregateQueryResult
	var roadmapNodeID, noteID sql.NullString
	var lifecycle string
	err := r.queryer.QueryRowContext(ctx, `SELECT
		t.workspace_id,t.id,t.project_id,t.roadmap_node_id,t.note_id,t.title,t.description,t.lifecycle_status,t.priority,t.sort_order,t.revision,
		s.revision,s.current_schedule_revision
		FROM domain_tasks_v2 t
		JOIN domain_task_schedules_v2 s ON s.workspace_id=t.workspace_id AND s.task_id=t.id
		WHERE t.workspace_id=$1 AND t.id=$2`, r.workspaceID, taskID).Scan(
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

	rows, err := r.queryer.QueryContext(ctx, `SELECT schedule_revision,effective_from::text,effective_to::text,recurrence_type,timing_type,timezone,
		starts_on::text,ends_on::text,recurrence_rule::text,local_start_time::text,duration_minutes
		FROM domain_task_schedule_versions_v2 WHERE workspace_id=$1 AND task_id=$2 ORDER BY schedule_revision`, r.workspaceID, taskID)
	if err != nil {
		return taskdomain.TaskAggregateQueryResult{}, err
	}
	defer rows.Close()
	for rows.Next() {
		version, err := scanPostgresTaskDomainV2ScheduleVersion(rows, r.workspaceID, taskID)
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
		result.Aggregate.Occurrences = append(result.Aggregate.Occurrences, postgresTaskDomainV2DomainOccurrence(occurrence))
	}
	result.Aggregate.GenerationEnabled = result.Aggregate.Recurring && result.Aggregate.LifecycleStatus == taskdomain.TaskLifecycleActive
	return result, nil
}

func (r *postgresTaskDomainV2ProjectReader) GetOccurrence(ctx context.Context, occurrenceID string) (taskdomain.QueryOccurrenceSnapshot, error) {
	rows, err := r.queryer.QueryContext(ctx, postgresTaskDomainV2OccurrenceSelect+` WHERE o.workspace_id=$1 AND o.id=$2`, r.workspaceID, occurrenceID)
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
	return scanPostgresTaskDomainV2Occurrence(rows)
}

func (r *postgresTaskDomainV2ProjectReader) ListTaskOccurrences(ctx context.Context, taskID string) ([]taskdomain.QueryOccurrenceSnapshot, error) {
	return r.listOccurrences(ctx, `o.workspace_id=$1 AND o.task_id=$2`, []any{r.workspaceID, taskID})
}

func (r *postgresTaskDomainV2ProjectReader) ListOccurrences(ctx context.Context, filter taskdomain.OccurrenceListFilter) ([]taskdomain.QueryOccurrenceSnapshot, error) {
	predicate, args, err := postgresTaskDomainV2OccurrenceFilter(filter, 2)
	if err != nil {
		return nil, err
	}
	return r.listOccurrences(ctx, "o.workspace_id=$1 AND "+predicate, append([]any{r.workspaceID}, args...))
}

func (r *postgresTaskDomainV2ProjectReader) listOccurrences(ctx context.Context, predicate string, args []any) ([]taskdomain.QueryOccurrenceSnapshot, error) {
	rows, err := r.queryer.QueryContext(ctx, postgresTaskDomainV2OccurrenceSelect+" WHERE "+predicate, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]taskdomain.QueryOccurrenceSnapshot, 0)
	for rows.Next() {
		item, err := scanPostgresTaskDomainV2Occurrence(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(items, func(left, right int) bool {
		return postgresTaskDomainV2OccurrenceLess(items[left], items[right])
	})
	return items, nil
}

const postgresTaskDomainV2OccurrenceSelect = `SELECT
	o.workspace_id,t.project_id,t.id,o.id,o.occurrence_key,
	COALESCE(o.override_title,t.title),COALESCE(o.override_description,t.description),
	v.timing_type,v.timezone,o.planned_date::text,o.planned_start_at,o.planned_end_at,o.due_at,o.execution_status,
	(v.recurrence_type <> 'none'),
	o.revision,p.revision,t.revision,s.revision,o.generated_schedule_revision,t.lifecycle_status,t.priority,t.sort_order,
	o.actual_start_at,o.completed_at,o.blocked_reason,o.next_action,o.location,o.calendar_kind,o.calendar_notes,
	t.note_id,o.note_id,o.all_day_end_date::text
	FROM domain_task_occurrences_v2 o
	JOIN domain_tasks_v2 t ON t.workspace_id=o.workspace_id AND t.id=o.task_id
	JOIN domain_projects_v2 p ON p.workspace_id=t.workspace_id AND p.id=t.project_id
	JOIN domain_task_schedules_v2 s ON s.workspace_id=o.workspace_id AND s.task_id=o.task_id
	JOIN domain_task_schedule_versions_v2 v ON v.workspace_id=o.workspace_id AND v.task_id=o.task_id
		AND v.schedule_revision=o.generated_schedule_revision`

type postgresTaskDomainV2Scanner interface {
	Scan(...any) error
}

func scanPostgresTaskDomainV2Occurrence(scanner postgresTaskDomainV2Scanner) (taskdomain.QueryOccurrenceSnapshot, error) {
	var item taskdomain.QueryOccurrenceSnapshot
	var timingType, executionStatus, lifecycleStatus string
	var plannedDate, blockedReason, nextAction, location, calendarKind, calendarNotes, taskNoteID, occurrenceNoteID, allDayEndDate sql.NullString
	var plannedStart, plannedEnd, dueAt, actualStart, completedAt sql.NullTime
	err := scanner.Scan(
		&item.WorkspaceID, &item.ProjectID, &item.TaskID, &item.OccurrenceID, &item.OccurrenceKey,
		&item.Title, &item.Description, &timingType, &item.Timezone, &plannedDate, &plannedStart, &plannedEnd, &dueAt, &executionStatus,
		&item.Recurring, &item.Revision, &item.ProjectRevision, &item.TaskRevision, &item.ScheduleRevision, &item.GeneratedScheduleRevision,
		&lifecycleStatus, &item.Priority, &item.SortOrder, &actualStart, &completedAt, &blockedReason, &nextAction,
		&location, &calendarKind, &calendarNotes, &taskNoteID, &occurrenceNoteID, &allDayEndDate,
	)
	if err != nil {
		return taskdomain.QueryOccurrenceSnapshot{}, err
	}
	item.TimingType = taskdomain.TimingType(timingType)
	item.Status = taskdomain.ExecutionStatus(executionStatus)
	item.LifecycleStatus = taskdomain.TaskLifecycleStatus(lifecycleStatus)
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
		source sql.NullTime
		target **time.Time
	}{
		{plannedStart, &item.PlannedStartAt}, {plannedEnd, &item.PlannedEndAt}, {dueAt, &item.DueAt},
		{actualStart, &item.ActualStartAt}, {completedAt, &item.CompletedAt},
	} {
		if pair.source.Valid {
			value := pair.source.Time
			*pair.target = &value
		}
	}
	return item, nil
}

func scanPostgresTaskDomainV2ScheduleVersion(scanner postgresTaskDomainV2Scanner, workspaceID, taskID string) (taskdomain.ScheduleVersion, error) {
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

func scanPostgresTaskDomainV2ScheduleOccurrence(scanner postgresTaskDomainV2Scanner, workspaceID, taskID string) (taskdomain.ScheduleOccurrenceSnapshot, error) {
	var snapshot taskdomain.ScheduleOccurrenceSnapshot
	snapshot.Record.WorkspaceID = workspaceID
	snapshot.Record.TaskID = taskID
	var plannedDate, noteID, allDayEnd sql.NullString
	var plannedStart, plannedEnd, dueAt, actualStart, completedAt sql.NullTime
	var status string
	err := scanner.Scan(&snapshot.Record.ID, &snapshot.Record.OccurrenceKey, &plannedDate, &plannedStart, &plannedEnd, &dueAt,
		&status, &noteID, &allDayEnd, &snapshot.Record.Revision, &snapshot.Record.GeneratedScheduleRevision,
		&actualStart, &completedAt, &snapshot.ManuallyOverridden)
	if err != nil {
		return taskdomain.ScheduleOccurrenceSnapshot{}, err
	}
	snapshot.Record.PlannedDate = plannedDate.String
	snapshot.Record.ExecutionStatus = taskdomain.ExecutionStatus(status)
	snapshot.Record.NoteID = noteID.String
	snapshot.Record.AllDayEndDate = allDayEnd.String
	for _, pair := range []struct {
		source sql.NullTime
		target **time.Time
	}{
		{plannedStart, &snapshot.Record.PlannedStartAt}, {plannedEnd, &snapshot.Record.PlannedEndAt}, {dueAt, &snapshot.Record.DueAt},
		{actualStart, &snapshot.ActualStartAt}, {completedAt, &snapshot.CompletedAt},
	} {
		if pair.source.Valid {
			value := pair.source.Time
			*pair.target = &value
		}
	}
	return snapshot, nil
}

func postgresTaskDomainV2DomainOccurrence(item taskdomain.QueryOccurrenceSnapshot) taskdomain.Occurrence {
	return taskdomain.Occurrence{
		WorkspaceID: item.WorkspaceID, ID: item.OccurrenceID, TaskID: item.TaskID, OccurrenceKey: item.OccurrenceKey,
		ExecutionStatus: item.Status, Recurring: item.Recurring, ActualStartAt: clonePostgresTaskDomainV2Time(item.ActualStartAt),
		CompletedAt: clonePostgresTaskDomainV2Time(item.CompletedAt), BlockedReason: item.BlockedReason, NextAction: item.NextAction,
		Revision: item.Revision,
	}
}

func postgresTaskDomainV2OccurrenceFilter(filter taskdomain.OccurrenceListFilter, firstPlaceholder int) (string, []any, error) {
	args := make([]any, 0)
	placeholder := func(value any) string {
		position := firstPlaceholder + len(args)
		args = append(args, value)
		return fmt.Sprintf("$%d", position)
	}
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
	var predicate string
	switch filter.Scope {
	case taskdomain.OccurrenceListAll:
		predicate = `TRUE`
	case taskdomain.OccurrenceListToday:
		if err := requireRange(); err != nil {
			return "", nil, err
		}
		fromDate, toDate, err := dateBounds()
		if err != nil {
			return "", nil, err
		}
		predicate = fmt.Sprintf(`((v.timing_type='date' AND o.planned_date < %s AND COALESCE(o.all_day_end_date,o.planned_date + 1) > %s)
			OR (v.timing_type='time_block' AND o.planned_start_at < %s AND o.planned_end_at > %s))`,
			placeholder(toDate), placeholder(fromDate), placeholder(filter.To), placeholder(filter.From))
	case taskdomain.OccurrenceListUpcoming:
		if err := requireRange(); err != nil {
			return "", nil, err
		}
		fromDate, toDate, err := dateBounds()
		if err != nil {
			return "", nil, err
		}
		predicate = fmt.Sprintf(`(((v.timing_type='date' AND o.planned_date >= %s AND o.planned_date < %s)
			OR (v.timing_type='time_block' AND o.planned_start_at >= %s AND o.planned_start_at < %s))
			AND o.execution_status NOT IN ('done','skipped','cancelled'))`,
			placeholder(fromDate), placeholder(toDate), placeholder(filter.From), placeholder(filter.To))
	case taskdomain.OccurrenceListOverdue:
		if filter.From.IsZero() {
			return "", nil, taskdomain.ErrInvalidOccurrenceListFilter
		}
		predicate = fmt.Sprintf(`o.due_at < %s AND o.execution_status NOT IN ('done','skipped','cancelled')`, placeholder(filter.From))
	case taskdomain.OccurrenceListUnscheduled:
		predicate = `v.timing_type='unscheduled'`
	case taskdomain.OccurrenceListCompleted:
		if err := requireRange(); err != nil {
			return "", nil, err
		}
		predicate = fmt.Sprintf(`o.execution_status='done' AND o.completed_at >= %s AND o.completed_at < %s`,
			placeholder(filter.From), placeholder(filter.To))
	case taskdomain.OccurrenceListCalendar:
		if err := requireRange(); err != nil {
			return "", nil, err
		}
		fromDate, toDate, err := dateBounds()
		if err != nil {
			return "", nil, err
		}
		predicate = fmt.Sprintf(`((v.timing_type='date' AND o.planned_date < %s AND COALESCE(o.all_day_end_date,o.planned_date + 1) > %s)
			OR (v.timing_type='time_block' AND o.planned_start_at < %s AND o.planned_end_at > %s))`,
			placeholder(toDate), placeholder(fromDate), placeholder(filter.To), placeholder(filter.From))
	default:
		return "", nil, taskdomain.ErrInvalidOccurrenceListFilter
	}

	conditions := []string{predicate}
	if filter.ProjectID != "" {
		conditions = append(conditions, "t.project_id="+placeholder(filter.ProjectID))
	}
	if filter.TaskID != "" {
		conditions = append(conditions, "o.task_id="+placeholder(filter.TaskID))
	}
	if len(filter.Statuses) > 0 {
		placeholders := make([]string, 0, len(filter.Statuses))
		for _, status := range filter.Statuses {
			if !postgresTaskDomainV2KnownExecutionStatus(status) {
				return "", nil, taskdomain.ErrInvalidOccurrenceListFilter
			}
			placeholders = append(placeholders, placeholder(status))
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
	return strings.Join(conditions, " AND "), args, nil
}

func postgresTaskDomainV2KnownExecutionStatus(status taskdomain.ExecutionStatus) bool {
	switch status {
	case taskdomain.ExecutionStatusOpen, taskdomain.ExecutionStatusActive, taskdomain.ExecutionStatusBlocked,
		taskdomain.ExecutionStatusDone, taskdomain.ExecutionStatusSkipped, taskdomain.ExecutionStatusCancelled:
		return true
	default:
		return false
	}
}

func postgresTaskDomainV2OccurrenceLess(left, right taskdomain.QueryOccurrenceSnapshot) bool {
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

func clonePostgresTaskDomainV2Time(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

type postgresTaskDomainV2ProjectWriter struct {
	queryer     postgresTaskDomainV2Queryer
	workspaceID string
	isClosed    func() bool
}

type postgresProjectCommandWriter struct {
	delegate *postgresTaskDomainV2ProjectWriter
}

func (w *postgresProjectCommandWriter) EnsureSystemProjects(ctx context.Context) error {
	return w.delegate.EnsureSystemProjects(ctx)
}

func (w *postgresProjectCommandWriter) SaveProject(ctx context.Context, write taskdomain.ProjectWrite) error {
	err := w.delegate.SaveProject(ctx, write)
	if errors.Is(err, taskdomain.ErrAggregateRevisionConflict) {
		return taskdomain.ErrProjectRevisionConflict
	}
	return err
}

func (w *postgresProjectCommandWriter) DeleteProject(ctx context.Context, projectID string, expectedRevision int64) error {
	err := w.delegate.DeleteProject(ctx, projectID, expectedRevision)
	if errors.Is(err, taskdomain.ErrAggregateRevisionConflict) {
		return taskdomain.ErrProjectRevisionConflict
	}
	return err
}

func (w *postgresTaskDomainV2ProjectWriter) GetProject(ctx context.Context, projectID string) (taskdomain.ProjectSnapshot, error) {
	if w.closed() {
		return taskdomain.ProjectSnapshot{}, storage.ErrTenantWriteTxClosed
	}
	return getPostgresTaskDomainV2Project(ctx, w.queryer, w.workspaceID, projectID)
}

func (w *postgresTaskDomainV2ProjectWriter) CountNonTerminalProjectOccurrences(ctx context.Context, projectID string) (int, error) {
	if w.closed() {
		return 0, storage.ErrTenantWriteTxClosed
	}
	var count int
	err := w.queryer.QueryRowContext(ctx, `SELECT COUNT(*)
		FROM domain_task_occurrences_v2 occurrence
		JOIN domain_tasks_v2 task ON task.workspace_id=occurrence.workspace_id AND task.id=occurrence.task_id
		WHERE task.workspace_id=$1 AND task.project_id=$2 AND occurrence.execution_status IN ('open','active','blocked')`,
		w.workspaceID, projectID).Scan(&count)
	return count, err
}

func (w *postgresTaskDomainV2ProjectWriter) LoadRecurringCompletionState(ctx context.Context, taskID string) (taskdomain.RecurringCompletionCommandState, error) {
	if w.closed() {
		return taskdomain.RecurringCompletionCommandState{}, storage.ErrTenantWriteTxClosed
	}
	reader := &postgresTaskDomainV2ProjectReader{queryer: w.queryer, workspaceID: w.workspaceID}
	query, err := reader.GetTaskAggregate(ctx, taskID)
	if err != nil {
		return taskdomain.RecurringCompletionCommandState{}, err
	}
	state := taskdomain.RecurringCompletionCommandState{Aggregate: query.Aggregate}
	var watermark sql.NullString
	var generationStatus string
	err = w.queryer.QueryRowContext(ctx, `SELECT revision,generation_watermark::text,generation_status,
		generation_retry_pending_jobs,generation_failed_jobs FROM domain_task_schedules_v2
		WHERE workspace_id=$1 AND task_id=$2`, w.workspaceID, taskID).Scan(
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

func (w *postgresTaskDomainV2ProjectWriter) ListGenerationTargets(ctx context.Context) ([]taskdomain.GenerationTargetState, error) {
	if w.closed() {
		return nil, storage.ErrTenantWriteTxClosed
	}
	rows, err := w.queryer.QueryContext(ctx, `SELECT task.id FROM domain_tasks_v2 task
		JOIN domain_task_schedules_v2 schedule ON schedule.workspace_id=task.workspace_id AND schedule.task_id=task.id
		JOIN domain_task_schedule_versions_v2 version ON version.workspace_id=schedule.workspace_id AND version.task_id=schedule.task_id AND version.schedule_revision=schedule.current_schedule_revision
		WHERE task.workspace_id=$1 AND version.recurrence_type<>'none' ORDER BY task.id`, w.workspaceID)
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

func (w *postgresTaskDomainV2ProjectWriter) loadGenerationTarget(ctx context.Context, taskID string) (taskdomain.GenerationTargetState, error) {
	target := taskdomain.GenerationTargetState{TaskID: taskID}
	var lifecycle string
	var watermark sql.NullString
	var currentVersion int64
	err := w.queryer.QueryRowContext(ctx, `SELECT task.lifecycle_status,schedule.revision,schedule.current_schedule_revision,schedule.generation_watermark::text
		FROM domain_tasks_v2 task JOIN domain_task_schedules_v2 schedule ON schedule.workspace_id=task.workspace_id AND schedule.task_id=task.id
		WHERE task.workspace_id=$1 AND task.id=$2`, w.workspaceID, taskID).Scan(&lifecycle, &target.ScheduleRevision, &currentVersion, &watermark)
	if errors.Is(err, sql.ErrNoRows) {
		return taskdomain.GenerationTargetState{}, taskdomain.ErrTaskNotFound
	}
	if err != nil {
		return taskdomain.GenerationTargetState{}, err
	}
	target.GenerationWatermark = watermark.String
	rows, err := w.queryer.QueryContext(ctx, `SELECT schedule_revision,effective_from::text,effective_to::text,recurrence_type,timing_type,timezone,
		starts_on::text,ends_on::text,recurrence_rule::text,local_start_time::text,duration_minutes FROM domain_task_schedule_versions_v2
		WHERE workspace_id=$1 AND task_id=$2 ORDER BY schedule_revision`, w.workspaceID, taskID)
	if err != nil {
		return taskdomain.GenerationTargetState{}, err
	}
	for rows.Next() {
		version, err := scanPostgresTaskDomainV2ScheduleVersion(rows, w.workspaceID, taskID)
		if err != nil {
			rows.Close()
			return taskdomain.GenerationTargetState{}, err
		}
		schedule, err := normalizePostgresGenerationScheduleVersion(version)
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
	rows, err = w.queryer.QueryContext(ctx, `SELECT occurrence_key FROM domain_task_occurrences_v2 WHERE workspace_id=$1 AND task_id=$2 ORDER BY occurrence_key`, w.workspaceID, taskID)
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

func (w *postgresTaskDomainV2ProjectWriter) InsertMissingOccurrences(ctx context.Context, insert taskdomain.GenerationInsert) error {
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
		if !exists || validatePostgresGenerationOccurrence(insert, occurrence, version) != nil {
			return taskdomain.ErrInvalidGenerationWorker
		}
	}
	result, err := w.queryer.ExecContext(ctx, `UPDATE domain_task_schedules_v2 SET generation_status='running',generation_error=NULL,generation_retry_at=NULL,
		generation_retry_pending_jobs=0,generation_failed_jobs=0,updated_at=now() WHERE workspace_id=$1 AND task_id=$2 AND revision=$3`,
		w.workspaceID, insert.TaskID, insert.ExpectedScheduleRevision)
	if err != nil {
		return err
	}
	if err := requirePostgresTaskDomainV2ScheduleChanged(result); err != nil {
		return err
	}
	for _, occurrence := range insert.Occurrences {
		if _, err := w.queryer.ExecContext(ctx, `INSERT INTO domain_task_occurrences_v2
			(workspace_id,id,task_id,occurrence_key,planned_date,planned_start_at,planned_end_at,execution_status,revision,generated_schedule_revision,manually_overridden,created_at,updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,'open',1,$8,FALSE,now(),now())
			ON CONFLICT(workspace_id,task_id,occurrence_key) DO NOTHING`,
			w.workspaceID, occurrence.ID, insert.TaskID, occurrence.OccurrenceKey, occurrence.PlannedDate,
			occurrence.PlannedStartAt, occurrence.PlannedEndAt, occurrence.GeneratedScheduleRevision); err != nil {
			return err
		}
	}
	return nil
}

func (w *postgresTaskDomainV2ProjectWriter) CompleteGeneration(ctx context.Context, completion taskdomain.GenerationCompletion) error {
	if w.closed() {
		return storage.ErrTenantWriteTxClosed
	}
	if completion.WorkspaceID != w.workspaceID || completion.TaskID == "" || completion.ExpectedScheduleRevision < 1 || validatePostgresGenerationCompletion(completion) != nil {
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
		complete, err := postgresGenerationTargetHasExpectedKeysThrough(target, completion.GenerationWatermark)
		if err != nil || !complete {
			return taskdomain.ErrInvalidGenerationWorker
		}
	}
	result, err := w.queryer.ExecContext(ctx, `UPDATE domain_task_schedules_v2 SET generation_watermark=$1,generation_status=$2,generation_error=$3,generation_retry_at=$4,
		generation_retry_pending_jobs=$5,generation_failed_jobs=$6,updated_at=now() WHERE workspace_id=$7 AND task_id=$8 AND revision=$9`,
		completion.GenerationWatermark, completion.Status, nullablePostgresTaskDomainV2String(completion.Error), completion.RetryAt,
		completion.RetryPendingJobs, completion.FailedJobs, w.workspaceID, completion.TaskID, completion.ExpectedScheduleRevision)
	if err != nil {
		return err
	}
	return requirePostgresTaskDomainV2ScheduleChanged(result)
}

func (w *postgresTaskDomainV2ProjectWriter) EnsureSystemProjects(ctx context.Context) error {
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
			VALUES ($1,$2,$3,$4,$5,$6,$7,1,now(),now())
			ON CONFLICT DO NOTHING`, project.WorkspaceID, project.ID, project.Name, project.Kind, project.Horizon, project.Status, project.SystemRole); err != nil {
			return err
		}
	}
	projects := make([]taskdomain.Project, 0, 2)
	for _, id := range []string{taskdomain.SystemInboxProjectID, taskdomain.PersonalProjectID} {
		snapshot, err := getPostgresTaskDomainV2Project(ctx, w.queryer, w.workspaceID, id)
		if err != nil {
			return taskdomain.ErrInvalidSystemProjectSet
		}
		projects = append(projects, snapshot.Project)
	}
	return taskdomain.ValidateWorkspaceSystemProjects(w.workspaceID, projects)
}

func (w *postgresTaskDomainV2ProjectWriter) SaveProject(ctx context.Context, write taskdomain.ProjectWrite) error {
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
			VALUES ($1,$2,$3,$4,$5,$6,NULL,1,now(),now())
			ON CONFLICT DO NOTHING`, write.Project.WorkspaceID, write.Project.ID, write.Project.Name, write.Project.Kind, write.Project.Horizon, write.Project.Status)
		if err != nil {
			return err
		}
		return requirePostgresTaskDomainV2Changed(result)
	}

	current, err := getPostgresTaskDomainV2Project(ctx, w.queryer, w.workspaceID, write.Project.ID)
	if err != nil {
		return err
	}
	if current.Project.SystemRole != write.Project.SystemRole {
		return taskdomain.ErrSystemProjectImmutable
	}
	result, err := w.queryer.ExecContext(ctx, `UPDATE domain_projects_v2
		SET name=$1,kind=$2,horizon=$3,status=$4,updated_at=now(),revision=revision+1
		WHERE workspace_id=$5 AND id=$6 AND revision=$7`,
		write.Project.Name, write.Project.Kind, write.Project.Horizon, write.Project.Status,
		w.workspaceID, write.Project.ID, write.ExpectedRevision)
	if err != nil {
		return err
	}
	return requirePostgresTaskDomainV2Changed(result)
}

func (w *postgresTaskDomainV2ProjectWriter) DeleteProject(ctx context.Context, projectID string, expectedRevision int64) error {
	if w.closed() {
		return storage.ErrTenantWriteTxClosed
	}
	if expectedRevision < 1 {
		return taskdomain.ErrAggregateRevisionConflict
	}
	current, err := getPostgresTaskDomainV2Project(ctx, w.queryer, w.workspaceID, projectID)
	if err != nil {
		return err
	}
	if err := taskdomain.ValidateProjectDeletion(current.Project); err != nil {
		return err
	}
	result, err := w.queryer.ExecContext(ctx, `DELETE FROM domain_projects_v2 WHERE workspace_id=$1 AND id=$2 AND revision=$3`, w.workspaceID, projectID, expectedRevision)
	if err != nil {
		return err
	}
	return requirePostgresTaskDomainV2Changed(result)
}

func (w *postgresTaskDomainV2ProjectWriter) CreateTaskAggregate(ctx context.Context, snapshot taskdomain.TaskAggregateSnapshot) error {
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
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,now(),now())`,
		snapshot.Task.WorkspaceID, snapshot.Task.ID, snapshot.Task.ProjectID,
		nullablePostgresTaskDomainV2String(snapshot.Task.RoadmapNodeID), nullablePostgresTaskDomainV2String(snapshot.Task.NoteID),
		snapshot.Task.Title, snapshot.Task.Description, snapshot.Task.LifecycleStatus,
		snapshot.Task.Priority, snapshot.Task.SortOrder, snapshot.Task.Revision,
	); err != nil {
		return err
	}
	if _, err := w.queryer.ExecContext(ctx, `INSERT INTO domain_task_schedules_v2
		(workspace_id,task_id,revision,current_schedule_revision,generation_status,updated_at)
		VALUES ($1,$2,$3,$4,'idle',now())`,
		snapshot.Schedule.WorkspaceID, snapshot.Schedule.TaskID, snapshot.Schedule.Revision, snapshot.Schedule.CurrentScheduleRevision,
	); err != nil {
		return err
	}
	for _, version := range snapshot.Versions {
		if _, err := w.queryer.ExecContext(ctx, `INSERT INTO domain_task_schedule_versions_v2
			(workspace_id,task_id,schedule_revision,effective_from,effective_to,recurrence_type,timing_type,timezone,starts_on,ends_on,recurrence_rule,local_start_time,duration_minutes,created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11::jsonb,$12,$13,now())`,
			version.WorkspaceID, version.TaskID, version.ScheduleRevision,
			nullablePostgresTaskDomainV2String(version.EffectiveFrom), nullablePostgresTaskDomainV2String(version.EffectiveTo),
			version.RecurrenceType, version.TimingType, version.Timezone,
			nullablePostgresTaskDomainV2String(version.StartsOn), nullablePostgresTaskDomainV2String(version.EndsOn), version.RecurrenceRule,
			nullablePostgresTaskDomainV2String(version.LocalStartTime), nullablePostgresTaskDomainV2PositiveInt(version.DurationMinutes),
		); err != nil {
			return err
		}
	}
	for _, occurrence := range snapshot.Occurrences {
		if _, err := w.queryer.ExecContext(ctx, `INSERT INTO domain_task_occurrences_v2
			(workspace_id,id,task_id,occurrence_key,planned_date,planned_start_at,planned_end_at,due_at,execution_status,note_id,all_day_end_date,revision,generated_schedule_revision,manually_overridden,created_at,updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,FALSE,now(),now())
			ON CONFLICT(workspace_id,task_id,occurrence_key) DO NOTHING`,
			occurrence.WorkspaceID, occurrence.ID, occurrence.TaskID, occurrence.OccurrenceKey,
			nullablePostgresTaskDomainV2String(occurrence.PlannedDate), postgresTaskDomainV2Time(occurrence.PlannedStartAt),
			postgresTaskDomainV2Time(occurrence.PlannedEndAt), postgresTaskDomainV2Time(occurrence.DueAt), occurrence.ExecutionStatus,
			nullablePostgresTaskDomainV2String(occurrence.NoteID), nullablePostgresTaskDomainV2String(occurrence.AllDayEndDate),
			occurrence.Revision, occurrence.GeneratedScheduleRevision,
		); err != nil {
			return err
		}
	}
	return nil
}

func (w *postgresTaskDomainV2ProjectWriter) SaveTaskAggregate(ctx context.Context, write taskdomain.TaskAggregateWrite) error {
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
		if err := requirePostgresTaskDomainV2ScheduleRevision(ctx, w.queryer, w.workspaceID, write.Aggregate.TaskID, write.ExpectedScheduleRevision); err != nil {
			return err
		}
	}
	if write.Task != nil && postgresTaskAggregateHasNonTerminalOccurrence(write.Aggregate) {
		if err := w.ensureProjectAcceptsNonTerminalOccurrences(ctx, write.Task.ProjectID); err != nil {
			return err
		}
	} else if postgresTaskAggregateWriteHasNonTerminalOccurrence(write) {
		if err := w.ensureTaskProjectAcceptsNonTerminalOccurrences(ctx, write.Aggregate.TaskID); err != nil {
			return err
		}
	}
	var result sql.Result
	var err error
	if write.Task == nil {
		result, err = w.queryer.ExecContext(ctx, `UPDATE domain_tasks_v2
			SET lifecycle_status=$1,revision=revision+1,updated_at=now()
			WHERE workspace_id=$2 AND id=$3 AND revision=$4`,
			write.Aggregate.LifecycleStatus, w.workspaceID, write.Aggregate.TaskID, write.ExpectedRevisions.Task)
	} else {
		task := *write.Task
		result, err = w.queryer.ExecContext(ctx, `UPDATE domain_tasks_v2 SET
			project_id=$1,roadmap_node_id=$2,note_id=$3,title=$4,description=$5,priority=$6,sort_order=$7,lifecycle_status=$8,
			revision=revision+1,updated_at=now()
			WHERE workspace_id=$9 AND id=$10 AND revision=$11`,
			task.ProjectID, nullablePostgresTaskDomainV2String(task.RoadmapNodeID), nullablePostgresTaskDomainV2String(task.NoteID),
			task.Title, task.Description, task.Priority, task.SortOrder, task.LifecycleStatus,
			w.workspaceID, write.Aggregate.TaskID, write.ExpectedRevisions.Task)
	}
	if err != nil {
		return err
	}
	if err := requirePostgresTaskDomainV2Changed(result); err != nil {
		return err
	}
	targets := make(map[string]taskdomain.Occurrence, len(write.Aggregate.Occurrences))
	for _, occurrence := range write.Aggregate.Occurrences {
		targets[occurrence.ID] = occurrence
	}
	for occurrenceID, expectedRevision := range write.ExpectedRevisions.Occurrences {
		occurrence := targets[occurrenceID]
		result, err := w.queryer.ExecContext(ctx, `UPDATE domain_task_occurrences_v2
			SET execution_status=$1,actual_start_at=$2,completed_at=$3,blocked_reason=$4,next_action=$5,revision=revision+1,updated_at=now()
			WHERE workspace_id=$6 AND id=$7 AND task_id=$8 AND revision=$9`,
			occurrence.ExecutionStatus, postgresTaskDomainV2Time(occurrence.ActualStartAt), postgresTaskDomainV2Time(occurrence.CompletedAt),
			nullablePostgresTaskDomainV2String(occurrence.BlockedReason), nullablePostgresTaskDomainV2String(occurrence.NextAction),
			w.workspaceID, occurrenceID, write.Aggregate.TaskID, expectedRevision)
		if err != nil {
			return err
		}
		if err := requirePostgresTaskDomainV2Changed(result); err != nil {
			return err
		}
	}
	for _, log := range write.ExecutionLogs {
		if _, err := w.queryer.ExecContext(ctx, `INSERT INTO domain_task_execution_logs_v2
			(workspace_id,id,occurrence_id,from_status,to_status,blocked_reason,next_action,actor_id,metadata,created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9::jsonb,$10)`,
			w.workspaceID, log.ID(), log.OccurrenceID(), log.FromStatus(), log.ToStatus(),
			nullablePostgresTaskDomainV2String(log.BlockedReason()), nullablePostgresTaskDomainV2String(log.NextAction()),
			log.ActorID(), `{}`, log.CreatedAt()); err != nil {
			return err
		}
	}
	return nil
}

func (w *postgresTaskDomainV2ProjectWriter) validateTaskAttributeReferences(ctx context.Context, task taskdomain.TaskRecord) error {
	var projectExists bool
	if err := w.queryer.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM domain_projects_v2 WHERE workspace_id=$1 AND id=$2
	)`, w.workspaceID, task.ProjectID).Scan(&projectExists); err != nil {
		return err
	}
	if !projectExists {
		return taskdomain.ErrInvalidTaskAggregateSnapshot
	}
	if task.NoteID != "" {
		var noteExists bool
		if err := w.queryer.QueryRowContext(ctx, `SELECT EXISTS(
			SELECT 1 FROM notes WHERE workspace_id=$1 AND id=$2
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
			SELECT 1 FROM domain_roadmap_nodes_v2 WHERE workspace_id=$1 AND id=$2 AND project_id=$3
		)`, w.workspaceID, task.RoadmapNodeID, task.ProjectID).Scan(&nodeExists); err != nil {
			return err
		}
		if !nodeExists {
			return taskdomain.ErrInvalidTaskAggregateSnapshot
		}
	}
	return nil
}

func (w *postgresTaskDomainV2ProjectWriter) ensureProjectAcceptsNonTerminalOccurrences(ctx context.Context, projectID string) error {
	var status taskdomain.ProjectStatus
	err := w.queryer.QueryRowContext(ctx, `SELECT status FROM domain_projects_v2 WHERE workspace_id=$1 AND id=$2`, w.workspaceID, projectID).Scan(&status)
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

func (w *postgresTaskDomainV2ProjectWriter) ensureTaskProjectAcceptsNonTerminalOccurrences(ctx context.Context, taskID string) error {
	var projectID string
	err := w.queryer.QueryRowContext(ctx, `SELECT project_id FROM domain_tasks_v2 WHERE workspace_id=$1 AND id=$2`, w.workspaceID, taskID).Scan(&projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return taskdomain.ErrTaskNotFound
	}
	if err != nil {
		return err
	}
	return w.ensureProjectAcceptsNonTerminalOccurrences(ctx, projectID)
}

func postgresTaskAggregateWriteHasNonTerminalOccurrence(write taskdomain.TaskAggregateWrite) bool {
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

func postgresTaskAggregateHasNonTerminalOccurrence(aggregate taskdomain.TaskAggregate) bool {
	for _, occurrence := range aggregate.Occurrences {
		if occurrence.ExecutionStatus == taskdomain.ExecutionStatusOpen || occurrence.ExecutionStatus == taskdomain.ExecutionStatusActive ||
			occurrence.ExecutionStatus == taskdomain.ExecutionStatusBlocked {
			return true
		}
	}
	return false
}

func normalizePostgresGenerationScheduleVersion(version taskdomain.ScheduleVersion) (taskdomain.Schedule, error) {
	return taskdomain.NormalizeSchedule(taskdomain.ScheduleInput{
		RecurrenceType: version.RecurrenceType, TimingType: version.TimingType, Timezone: version.Timezone,
		StartsOn: version.StartsOn, EndsOn: version.EndsOn, Rule: json.RawMessage(version.RecurrenceRule),
		LocalStartTime: version.LocalStartTime, DurationMinutes: version.DurationMinutes,
	})
}

func validatePostgresGenerationOccurrence(insert taskdomain.GenerationInsert, occurrence taskdomain.GenerationOccurrence, version taskdomain.GenerationScheduleVersion) error {
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

func validatePostgresGenerationCompletion(completion taskdomain.GenerationCompletion) error {
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

func postgresGenerationTargetHasExpectedKeysThrough(target taskdomain.GenerationTargetState, watermark string) (bool, error) {
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

func (w *postgresTaskDomainV2ProjectWriter) InstallScheduleVersion(ctx context.Context, install taskdomain.ScheduleVersionInstall) error {
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
		SET revision=revision+1,current_schedule_revision=$1,updated_at=now()
		WHERE workspace_id=$2 AND task_id=$3 AND revision=$4`,
		install.Version.ScheduleRevision, w.workspaceID, install.TaskID, install.ExpectedScheduleRevision)
	if err != nil {
		return err
	}
	if err := requirePostgresTaskDomainV2Changed(result); err != nil {
		return err
	}
	result, err = w.queryer.ExecContext(ctx, `UPDATE domain_task_schedule_versions_v2
		SET effective_to=$1 WHERE workspace_id=$2 AND task_id=$3 AND effective_to IS NULL`,
		install.Version.EffectiveFrom, w.workspaceID, install.TaskID)
	if err != nil {
		return err
	}
	if err := requirePostgresTaskDomainV2Changed(result); err != nil {
		return err
	}
	version := install.Version
	_, err = w.queryer.ExecContext(ctx, `INSERT INTO domain_task_schedule_versions_v2
		(workspace_id,task_id,schedule_revision,effective_from,effective_to,recurrence_type,timing_type,timezone,starts_on,ends_on,recurrence_rule,local_start_time,duration_minutes,created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11::jsonb,$12,$13,now())`,
		version.WorkspaceID, version.TaskID, version.ScheduleRevision,
		nullablePostgresTaskDomainV2String(version.EffectiveFrom), nullablePostgresTaskDomainV2String(version.EffectiveTo),
		version.RecurrenceType, version.TimingType, version.Timezone,
		nullablePostgresTaskDomainV2String(version.StartsOn), nullablePostgresTaskDomainV2String(version.EndsOn), version.RecurrenceRule,
		nullablePostgresTaskDomainV2String(version.LocalStartTime), nullablePostgresTaskDomainV2PositiveInt(version.DurationMinutes))
	return err
}

func (w *postgresTaskDomainV2ProjectWriter) ApplyOccurrenceReschedule(ctx context.Context, write taskdomain.OccurrenceRescheduleWrite) error {
	if w.closed() {
		return storage.ErrTenantWriteTxClosed
	}
	if write.WorkspaceID != w.workspaceID || write.TaskID == "" || write.ExpectedTaskRevision < 1 ||
		write.ExpectedScheduleRevision < 1 || write.ExpectedOccurrenceRevision < 1 ||
		write.After.Record.WorkspaceID != w.workspaceID || write.After.Record.TaskID != write.TaskID || write.After.Record.ID == "" ||
		write.After.Record.Revision != write.ExpectedOccurrenceRevision+1 || !write.After.ManuallyOverridden {
		return taskdomain.ErrInvalidScheduleCommand
	}
	if err := requirePostgresTaskDomainV2TaskRevision(ctx, w.queryer, w.workspaceID, write.TaskID, write.ExpectedTaskRevision); err != nil {
		return err
	}
	if err := requirePostgresTaskDomainV2ScheduleRevision(ctx, w.queryer, w.workspaceID, write.TaskID, write.ExpectedScheduleRevision); err != nil {
		return err
	}
	record := write.After.Record
	result, err := w.queryer.ExecContext(ctx, `UPDATE domain_task_occurrences_v2 SET
		planned_date=$1,planned_start_at=$2,planned_end_at=$3,due_at=$4,note_id=$5,all_day_end_date=$6,revision=revision+1,
		manually_overridden=TRUE,updated_at=now()
		WHERE workspace_id=$7 AND task_id=$8 AND id=$9 AND revision=$10`,
		nullablePostgresTaskDomainV2String(record.PlannedDate), record.PlannedStartAt, record.PlannedEndAt,
		record.DueAt, nullablePostgresTaskDomainV2String(record.NoteID), nullablePostgresTaskDomainV2String(record.AllDayEndDate),
		w.workspaceID, write.TaskID, record.ID, write.ExpectedOccurrenceRevision)
	if err != nil {
		return err
	}
	return requirePostgresTaskDomainV2OccurrenceChanged(result)
}

func (w *postgresTaskDomainV2ProjectWriter) ApplyScheduleVersionChange(ctx context.Context, write taskdomain.ScheduleVersionChangeWrite) error {
	if w.closed() {
		return storage.ErrTenantWriteTxClosed
	}
	if err := validatePostgresTaskDomainV2ScheduleVersionChange(w.workspaceID, write); err != nil {
		return err
	}
	if len(write.UpsertOccurrences) > 0 {
		if err := w.ensureTaskProjectAcceptsNonTerminalOccurrences(ctx, write.TaskID); err != nil {
			return err
		}
	}
	if err := requirePostgresTaskDomainV2TaskRevision(ctx, w.queryer, w.workspaceID, write.TaskID, write.ExpectedTaskRevision); err != nil {
		return err
	}
	result, err := w.queryer.ExecContext(ctx, `UPDATE domain_task_schedules_v2 SET revision=revision+1,current_schedule_revision=$1,updated_at=now()
		WHERE workspace_id=$2 AND task_id=$3 AND revision=$4 AND current_schedule_revision=$5`,
		write.NewVersion.ScheduleRevision, w.workspaceID, write.TaskID, write.ExpectedScheduleRevision, write.ClosedVersion.ScheduleRevision)
	if err != nil {
		return err
	}
	if err := requirePostgresTaskDomainV2ScheduleChanged(result); err != nil {
		return err
	}
	result, err = w.queryer.ExecContext(ctx, `UPDATE domain_task_schedule_versions_v2 SET effective_to=$1
		WHERE workspace_id=$2 AND task_id=$3 AND schedule_revision=$4 AND effective_to IS NULL`,
		write.ClosedVersion.EffectiveTo, w.workspaceID, write.TaskID, write.ClosedVersion.ScheduleRevision)
	if err != nil {
		return err
	}
	if err := requirePostgresTaskDomainV2ScheduleChanged(result); err != nil {
		return err
	}
	version := write.NewVersion
	if _, err := w.queryer.ExecContext(ctx, `INSERT INTO domain_task_schedule_versions_v2
		(workspace_id,task_id,schedule_revision,effective_from,effective_to,recurrence_type,timing_type,timezone,starts_on,ends_on,recurrence_rule,local_start_time,duration_minutes,created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11::jsonb,$12,$13,now())`,
		version.WorkspaceID, version.TaskID, version.ScheduleRevision, nullablePostgresTaskDomainV2String(version.EffectiveFrom),
		nullablePostgresTaskDomainV2String(version.EffectiveTo), version.RecurrenceType, version.TimingType, version.Timezone,
		nullablePostgresTaskDomainV2String(version.StartsOn), nullablePostgresTaskDomainV2String(version.EndsOn), version.RecurrenceRule,
		nullablePostgresTaskDomainV2String(version.LocalStartTime), nullablePostgresTaskDomainV2PositiveInt(version.DurationMinutes)); err != nil {
		return err
	}
	for _, occurrence := range write.UpsertOccurrences {
		record := occurrence.Record
		expectedRevision, exists := write.ExpectedOccurrenceRevisions[record.ID]
		if exists {
			result, err := w.queryer.ExecContext(ctx, `UPDATE domain_task_occurrences_v2 SET
				occurrence_key=$1,planned_date=$2,planned_start_at=$3,planned_end_at=$4,due_at=$5,execution_status=$6,actual_start_at=$7,completed_at=$8,
				note_id=$9,all_day_end_date=$10,blocked_reason=NULL,next_action=NULL,revision=revision+1,generated_schedule_revision=$11,
				manually_overridden=$12,updated_at=now()
				WHERE workspace_id=$13 AND task_id=$14 AND id=$15 AND revision=$16`,
				record.OccurrenceKey, nullablePostgresTaskDomainV2String(record.PlannedDate), record.PlannedStartAt,
				record.PlannedEndAt, record.DueAt, record.ExecutionStatus, occurrence.ActualStartAt, occurrence.CompletedAt,
				nullablePostgresTaskDomainV2String(record.NoteID), nullablePostgresTaskDomainV2String(record.AllDayEndDate),
				record.GeneratedScheduleRevision, occurrence.ManuallyOverridden, w.workspaceID, write.TaskID, record.ID, expectedRevision)
			if err != nil {
				return err
			}
			if err := requirePostgresTaskDomainV2OccurrenceChanged(result); err != nil {
				return err
			}
			continue
		}
		if _, err := w.queryer.ExecContext(ctx, `INSERT INTO domain_task_occurrences_v2
			(workspace_id,id,task_id,occurrence_key,planned_date,planned_start_at,planned_end_at,due_at,execution_status,
			actual_start_at,completed_at,note_id,all_day_end_date,revision,generated_schedule_revision,manually_overridden,created_at,updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,now(),now())`,
			w.workspaceID, record.ID, write.TaskID, record.OccurrenceKey, nullablePostgresTaskDomainV2String(record.PlannedDate),
			record.PlannedStartAt, record.PlannedEndAt, record.DueAt, record.ExecutionStatus, occurrence.ActualStartAt, occurrence.CompletedAt,
			nullablePostgresTaskDomainV2String(record.NoteID), nullablePostgresTaskDomainV2String(record.AllDayEndDate), record.Revision,
			record.GeneratedScheduleRevision, occurrence.ManuallyOverridden); err != nil {
			return err
		}
	}
	for occurrenceID, expectedRevision := range write.DeleteOccurrenceRevisions {
		result, err := w.queryer.ExecContext(ctx, `DELETE FROM domain_task_occurrences_v2
			WHERE workspace_id=$1 AND task_id=$2 AND id=$3 AND revision=$4`, w.workspaceID, write.TaskID, occurrenceID, expectedRevision)
		if err != nil {
			return err
		}
		if err := requirePostgresTaskDomainV2OccurrenceChanged(result); err != nil {
			return err
		}
	}
	return nil
}

func validatePostgresTaskDomainV2ScheduleVersionChange(workspaceID string, write taskdomain.ScheduleVersionChangeWrite) error {
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

func requirePostgresTaskDomainV2TaskRevision(ctx context.Context, queryer postgresTaskDomainV2Queryer, workspaceID, taskID string, expected int64) error {
	var revision int64
	err := queryer.QueryRowContext(ctx, `SELECT revision FROM domain_tasks_v2 WHERE workspace_id=$1 AND id=$2 FOR UPDATE`, workspaceID, taskID).Scan(&revision)
	if err != nil || revision != expected {
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		return taskdomain.ErrTaskRevisionConflict
	}
	return nil
}

func requirePostgresTaskDomainV2ScheduleRevision(ctx context.Context, queryer postgresTaskDomainV2Queryer, workspaceID, taskID string, expected int64) error {
	var revision int64
	err := queryer.QueryRowContext(ctx, `SELECT revision FROM domain_task_schedules_v2 WHERE workspace_id=$1 AND task_id=$2 FOR UPDATE`, workspaceID, taskID).Scan(&revision)
	if err != nil || revision != expected {
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		return taskdomain.ErrScheduleRevisionConflict
	}
	return nil
}

func requirePostgresTaskDomainV2ScheduleChanged(result sql.Result) error {
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return taskdomain.ErrScheduleRevisionConflict
	}
	return nil
}

func requirePostgresTaskDomainV2OccurrenceChanged(result sql.Result) error {
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return taskdomain.ErrOccurrenceRevisionConflict
	}
	return nil
}

func (w *postgresTaskDomainV2ProjectWriter) closed() bool {
	return w.isClosed == nil || w.isClosed()
}

func getPostgresTaskDomainV2Project(ctx context.Context, queryer postgresTaskDomainV2Queryer, workspaceID, projectID string) (taskdomain.ProjectSnapshot, error) {
	var snapshot taskdomain.ProjectSnapshot
	var kind, horizon, status string
	var systemRole sql.NullString
	err := queryer.QueryRowContext(ctx, `SELECT id,name,kind,horizon,status,system_role,revision
		FROM domain_projects_v2 WHERE workspace_id=$1 AND id=$2`, workspaceID, projectID).Scan(
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

func requirePostgresTaskDomainV2Changed(result sql.Result) error {
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return taskdomain.ErrAggregateRevisionConflict
	}
	return nil
}

func nullablePostgresTaskDomainV2String(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullablePostgresTaskDomainV2PositiveInt(value int) any {
	if value == 0 {
		return nil
	}
	return value
}

func postgresTaskDomainV2Time(value *time.Time) any {
	if value == nil {
		return nil
	}
	return *value
}

var _ taskdomain.ProjectReader = (*postgresTaskDomainV2ProjectReader)(nil)
var _ taskdomain.TaskDomainReader = (*postgresTaskDomainV2ProjectReader)(nil)
var _ taskdomain.ScheduleCommandStateReader = (*postgresTaskDomainV2ProjectReader)(nil)
var _ taskdomain.TaskDomainWriter = (*postgresTaskDomainV2ProjectWriter)(nil)
var _ taskdomain.ScheduleCommandWriter = (*postgresTaskDomainV2ProjectWriter)(nil)
