package mobilev2projection

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
)

func projectOccurrenceWindow(
	ctx context.Context,
	runner Runner,
	dialect Dialect,
	projection Projection,
) ([]json.RawMessage, error) {
	if projection.WindowStart.IsZero() || !projection.WindowStart.Before(projection.WindowEnd) ||
		projection.WindowStartDate == "" || projection.WindowEndDate == "" {
		return nil, errors.New("invalid mobile-v2 occurrence window")
	}
	rows, err := runner.QueryContext(ctx, bind(dialect, `SELECT
		o.id,o.task_id,o.occurrence_key,o.planned_date,o.planned_start_at,o.planned_end_at,o.due_at,
		o.execution_status,o.actual_start_at,o.completed_at,o.override_title,o.override_description,
		o.blocked_reason,o.next_action,o.location,o.calendar_kind,o.calendar_notes,o.note_id,
		o.all_day_end_date,o.generated_schedule_revision,o.revision,o.created_at,o.updated_at,
		t.revision,s.revision,p.revision
		FROM domain_task_occurrences_v2 o
		JOIN domain_tasks_v2 t ON t.workspace_id=o.workspace_id AND t.id=o.task_id
		JOIN domain_task_schedules_v2 s ON s.workspace_id=o.workspace_id AND s.task_id=o.task_id
		JOIN domain_projects_v2 p ON p.workspace_id=t.workspace_id AND p.id=t.project_id
		WHERE o.workspace_id=? AND (
			(o.planned_start_at IS NOT NULL AND o.planned_start_at>=? AND o.planned_start_at<?)
			OR (o.due_at IS NOT NULL AND o.due_at>=? AND o.due_at<?)
			OR (o.planned_date IS NOT NULL AND o.planned_date>=? AND o.planned_date<?)
			OR (o.planned_start_at IS NULL AND o.due_at IS NULL AND o.planned_date IS NULL
				AND o.execution_status IN ('open','active','blocked'))
		)
		ORDER BY COALESCE(o.planned_start_at,o.due_at,o.created_at),o.id`),
		projection.WorkspaceID,
		projection.WindowStart.UTC().Format("2006-01-02T15:04:05.000Z"),
		projection.WindowEnd.UTC().Format("2006-01-02T15:04:05.000Z"),
		projection.WindowStart.UTC().Format("2006-01-02T15:04:05.000Z"),
		projection.WindowEnd.UTC().Format("2006-01-02T15:04:05.000Z"),
		projection.WindowStartDate,
		projection.WindowEndDate,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]json.RawMessage, 0)
	for rows.Next() {
		var (
			id, taskID, occurrenceKey, executionStatus       string
			plannedDate, overrideTitle, overrideDescription  sql.NullString
			blockedReason, nextAction, location              sql.NullString
			calendarKind, calendarNotes, noteID              sql.NullString
			allDayEndDate                                    sql.NullString
			plannedStartAt, plannedEndAt, dueAt              flexibleInstant
			actualStartAt, completedAt, createdAt, updatedAt flexibleInstant
			generatedScheduleRevision, occurrenceRevision    int64
			taskRevision, scheduleRevision, projectRevision  int64
		)
		if err := rows.Scan(
			&id, &taskID, &occurrenceKey, &plannedDate, &plannedStartAt, &plannedEndAt, &dueAt,
			&executionStatus, &actualStartAt, &completedAt, &overrideTitle, &overrideDescription,
			&blockedReason, &nextAction, &location, &calendarKind, &calendarNotes, &noteID,
			&allDayEndDate, &generatedScheduleRevision, &occurrenceRevision, &createdAt, &updatedAt,
			&taskRevision, &scheduleRevision, &projectRevision,
		); err != nil {
			return nil, err
		}
		result, err = appendEnvelope(result, entityEnvelope{
			EntityType: "task_occurrence", EntityID: id,
			EntityRevision: strconv.FormatInt(occurrenceRevision, 10),
			AggregateRevisions: aggregateRevisions{
				ProjectRevision: revision(projectRevision), TaskRevision: revision(taskRevision),
				ScheduleRevision: revision(scheduleRevision), OccurrenceRevision: revision(occurrenceRevision),
			},
			Payload: map[string]any{
				"task_id": taskID, "occurrence_key": occurrenceKey,
				"planned_date": optionalString(plannedDate), "planned_start_at": instantString(plannedStartAt),
				"planned_end_at": instantString(plannedEndAt), "due_at": instantString(dueAt),
				"execution_status": executionStatus, "actual_start_at": instantString(actualStartAt),
				"completed_at": instantString(completedAt), "override_title": optionalString(overrideTitle),
				"override_description": optionalString(overrideDescription), "blocked_reason": optionalString(blockedReason),
				"next_action": optionalString(nextAction), "location": optionalString(location),
				"calendar_kind": optionalString(calendarKind), "calendar_notes": optionalString(calendarNotes),
				"note_id": optionalString(noteID), "all_day_end_date": optionalString(allDayEndDate),
				"generated_schedule_revision": strconv.FormatInt(generatedScheduleRevision, 10),
				"created_at":                  requiredInstantString(createdAt), "updated_at": requiredInstantString(updatedAt),
			},
		})
		if err != nil {
			return nil, err
		}
	}
	return result, rows.Err()
}
