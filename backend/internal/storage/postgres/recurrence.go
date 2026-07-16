package postgres

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/lib/pq"
)

type recurrenceRepository struct {
	db postgresRunner
}

func (r recurrenceRepository) UpsertRule(ctx context.Context, rule *model.RecurrenceRule) error {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	if err := r.ensureTaskInWorkspace(ctx, workspaceID, rule.TaskID); err != nil {
		return err
	}
	weekdays := rule.Weekdays
	if weekdays == nil {
		weekdays = []int{}
	}
	monthDays := rule.MonthDays
	if monthDays == nil {
		monthDays = []int{}
	}

	_, err = r.db.ExecContext(ctx, `
		INSERT INTO task_recurrence_rules (task_id, start_date, end_date, frequency, interval, weekdays, month_days, timezone, enabled, workspace_id)
		VALUES ($1, $2::date, $3::date, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (task_id) DO UPDATE SET
			start_date = EXCLUDED.start_date,
			end_date = EXCLUDED.end_date,
			frequency = EXCLUDED.frequency,
			interval = EXCLUDED.interval,
			weekdays = EXCLUDED.weekdays,
			month_days = EXCLUDED.month_days,
			timezone = EXCLUDED.timezone,
			enabled = EXCLUDED.enabled,
			workspace_id = EXCLUDED.workspace_id,
			updated_at = now()
	`, rule.TaskID, rule.StartDate, rule.EndDate, rule.Frequency, rule.Interval,
		pq.Array(weekdays), pq.Array(monthDays), rule.Timezone, rule.Enabled, workspaceID)
	return err
}

func (r recurrenceRepository) GetRule(ctx context.Context, taskID string) (*model.RecurrenceRule, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	rule := &model.RecurrenceRule{}
	var endDate sql.NullString
	var weekdays, monthDays []int64
	err = r.db.QueryRowContext(ctx, `
		SELECT task_id, start_date::text, end_date::text, frequency, interval, weekdays, month_days, timezone, enabled,
			EXTRACT(EPOCH FROM created_at)::bigint, EXTRACT(EPOCH FROM updated_at)::bigint
		FROM task_recurrence_rules WHERE workspace_id = $1 AND task_id = $2
	`, workspaceID, taskID).Scan(&rule.TaskID, &rule.StartDate, &endDate, &rule.Frequency, &rule.Interval,
		pq.Array(&weekdays), pq.Array(&monthDays), &rule.Timezone, &rule.Enabled,
		&rule.CreatedAt, &rule.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if endDate.Valid {
		rule.EndDate = &endDate.String
	}
	rule.Weekdays = make([]int, len(weekdays))
	for i, v := range weekdays {
		rule.Weekdays[i] = int(v)
	}
	rule.MonthDays = make([]int, len(monthDays))
	for i, v := range monthDays {
		rule.MonthDays[i] = int(v)
	}
	return rule, nil
}

func (r recurrenceRepository) DeleteRule(ctx context.Context, taskID string) error {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `DELETE FROM task_recurrence_rules WHERE workspace_id = $1 AND task_id = $2`, workspaceID, taskID)
	return err
}

func (r recurrenceRepository) ListActiveRules(ctx context.Context, from, to string) ([]model.RecurrenceRule, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT task_id, start_date::text, end_date::text, frequency, interval, weekdays, month_days, timezone, enabled,
			EXTRACT(EPOCH FROM created_at)::bigint, EXTRACT(EPOCH FROM updated_at)::bigint
		FROM task_recurrence_rules
		WHERE workspace_id = $1
			AND enabled = true
			AND start_date <= $3::date
			AND (end_date IS NULL OR end_date >= $2::date)
	`, workspaceID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRecurrenceRules(rows)
}

func (r recurrenceRepository) ListOccurrences(ctx context.Context, from, to string) ([]model.TaskOccurrence, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT o.task_id, o.occurrence_date::text, o.status, EXTRACT(EPOCH FROM o.completed_at)::bigint AS completed_at, o.note,
			t.title, COALESCE(t.content, ''), t.project_id, COALESCE(p.name, t.project),
			t.roadmap_node_id, t.sort_order,
			EXTRACT(EPOCH FROM o.created_at)::bigint
		FROM task_occurrences o
		JOIN tasks t ON t.workspace_id = o.workspace_id AND t.id = o.task_id
		LEFT JOIN task_projects p ON p.workspace_id = t.workspace_id AND p.id = t.project_id
		WHERE o.workspace_id = $1 AND o.deleted_at IS NULL AND t.deleted_at IS NULL
			AND o.occurrence_date >= $2::date AND o.occurrence_date <= $3::date
		ORDER BY o.occurrence_date, t.sort_order
	`, workspaceID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanOccurrences(rows)
}

func (r recurrenceRepository) GetCompletedOccurrencesByRange(ctx context.Context, from, to int64) ([]model.TaskSummary, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT o.task_id AS id, t.title, EXTRACT(EPOCH FROM o.completed_at)::bigint AS completed_at,
			t.project_id, p.name AS project_name, p.type AS project_type,
			EXTRACT(EPOCH FROM t.due_at)::bigint, o.occurrence_date::text
		FROM task_occurrences o
		JOIN tasks t ON t.workspace_id = o.workspace_id AND t.id = o.task_id
		LEFT JOIN task_projects p ON p.workspace_id = t.workspace_id AND p.id = t.project_id
		WHERE o.workspace_id = $1 AND o.deleted_at IS NULL AND t.deleted_at IS NULL
			AND o.completed_at IS NOT NULL
			AND o.completed_at >= to_timestamp($2)
			AND o.completed_at < to_timestamp($3)
		ORDER BY o.completed_at DESC
	`, workspaceID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var summaries []model.TaskSummary
	for rows.Next() {
		var s model.TaskSummary
		var projectID, projectName, projectType sql.NullString
		var due sql.NullInt64
		var completedAt int64
		var occurrenceDate string
		if err := rows.Scan(&s.ID, &s.Title, &completedAt, &projectID, &projectName, &projectType, &due, &occurrenceDate); err != nil {
			return nil, err
		}
		s.CompletedAt = &completedAt
		s.ExecutionType = "recurring"
		s.OccurrenceDate = occurrenceDate
		if projectID.Valid {
			s.Project = &model.TaskProject{ID: projectID.String, Name: projectName.String, Type: projectType.String}
		}
		if due.Valid {
			s.Due = &due.Int64
		}
		s.Done = 1
		summaries = append(summaries, s)
	}
	return summaries, nil
}

func (r recurrenceRepository) CompleteOccurrence(ctx context.Context, taskID, date string, completedAt int64) (*model.TaskOccurrence, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if err := r.ensureTaskInWorkspace(ctx, workspaceID, taskID); err != nil {
		return nil, err
	}
	if err := r.writeMobileOccurrence(ctx, workspaceID, taskID, date, "done", &completedAt); err != nil {
		return nil, err
	}
	return r.getOccurrence(ctx, taskID, date)
}

func (r recurrenceRepository) ReopenOccurrence(ctx context.Context, taskID, date string) (*model.TaskOccurrence, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if err := r.ensureTaskInWorkspace(ctx, workspaceID, taskID); err != nil {
		return nil, err
	}
	if err := r.writeMobileOccurrence(ctx, workspaceID, taskID, date, "open", nil); err != nil {
		return nil, err
	}
	return r.getOccurrence(ctx, taskID, date)
}

func (r recurrenceRepository) SkipOccurrence(ctx context.Context, taskID, date string) (*model.TaskOccurrence, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if err := r.ensureTaskInWorkspace(ctx, workspaceID, taskID); err != nil {
		return nil, err
	}
	if err := r.writeMobileOccurrence(ctx, workspaceID, taskID, date, "skipped", nil); err != nil {
		return nil, err
	}
	return r.getOccurrence(ctx, taskID, date)
}

func (r recurrenceRepository) writeMobileOccurrence(ctx context.Context, workspaceID, taskID, date, status string, completedAt *int64) error {
	occurrenceID := deterministicPostgresMobileEntityClientID("task_occurrence", workspaceID, taskID+":"+date)
	now := time.Now().UTC()
	return (mobileSyncRepository{db: r.db}).withTx(ctx, func(tx *sql.Tx) error {
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_occurrences WHERE workspace_id = $1 AND task_id = $2 AND occurrence_date = $3::date`, workspaceID, taskID, date).Scan(&exists); err != nil {
			return err
		}
		operation := "task_occurrence.server_created"
		if exists == 0 {
			var completed any
			if completedAt != nil {
				completed = time.Unix(*completedAt, 0).UTC()
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO task_occurrences (
					task_id, occurrence_date, occurrence_id, revision, status, completed_at, workspace_id, created_at, updated_at
				) VALUES ($1, $2::date, $3, 1, $4, $5, $6, $7, $7)
			`, taskID, date, occurrenceID, status, completed, workspaceID, now); err != nil {
				return err
			}
		} else {
			operation = "task_occurrence.server_updated"
			var completed any
			if completedAt != nil {
				completed = time.Unix(*completedAt, 0).UTC()
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE task_occurrences SET status = $1, completed_at = $2, revision = revision + 1, updated_at = $3
				WHERE workspace_id = $4 AND task_id = $5 AND occurrence_date = $6::date AND deleted_at IS NULL
			`, status, completed, now, workspaceID, taskID, date); err != nil {
				return err
			}
		}
		return persistPostgresServerEntityChange(ctx, tx, workspaceID, uuid.NewString(), "task_occurrence", operation, occurrenceID, now)
	})
}

func (r recurrenceRepository) CountOccurrencesByTask(ctx context.Context, taskID string) (int, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return 0, err
	}
	var count int
	err = r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_occurrences WHERE workspace_id = $1 AND task_id = $2 AND deleted_at IS NULL`, workspaceID, taskID).Scan(&count)
	return count, err
}

func (r recurrenceRepository) getOccurrence(ctx context.Context, taskID, date string) (*model.TaskOccurrence, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	o := &model.TaskOccurrence{}
	var completedAt sql.NullInt64
	err = r.db.QueryRowContext(ctx, `
		SELECT task_id, occurrence_date::text, status,
			EXTRACT(EPOCH FROM completed_at)::bigint, note,
			EXTRACT(EPOCH FROM created_at)::bigint
		FROM task_occurrences WHERE workspace_id = $1 AND task_id = $2 AND occurrence_date = $3::date
	`, workspaceID, taskID, date).Scan(&o.TaskID, &o.OccurrenceDate, &o.Status, &completedAt, &o.Note, &o.CreatedAt)
	if err != nil {
		return nil, err
	}
	if completedAt.Valid {
		o.CompletedAt = &completedAt.Int64
	}
	return o, nil
}

func (r recurrenceRepository) ensureTaskInWorkspace(ctx context.Context, workspaceID, taskID string) error {
	var exists bool
	if err := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM tasks WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL)`, workspaceID, taskID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return sql.ErrNoRows
	}
	return nil
}

func scanRecurrenceRules(rows *sql.Rows) ([]model.RecurrenceRule, error) {
	var rules []model.RecurrenceRule
	for rows.Next() {
		var r model.RecurrenceRule
		var endDate sql.NullString
		var weekdays, monthDays []int64
		if err := rows.Scan(&r.TaskID, &r.StartDate, &endDate, &r.Frequency, &r.Interval,
			pq.Array(&weekdays), pq.Array(&monthDays), &r.Timezone, &r.Enabled,
			&r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		if endDate.Valid {
			r.EndDate = &endDate.String
		}
		r.Weekdays = make([]int, len(weekdays))
		for i, v := range weekdays {
			r.Weekdays[i] = int(v)
		}
		r.MonthDays = make([]int, len(monthDays))
		for i, v := range monthDays {
			r.MonthDays[i] = int(v)
		}
		rules = append(rules, r)
	}
	return rules, nil
}

func scanOccurrences(rows *sql.Rows) ([]model.TaskOccurrence, error) {
	var occurrences []model.TaskOccurrence
	for rows.Next() {
		var o model.TaskOccurrence
		var completedAt sql.NullInt64
		var projectID, project sql.NullString
		var roadmapNodeID sql.NullString
		if err := rows.Scan(&o.TaskID, &o.OccurrenceDate, &o.Status, &completedAt, &o.Note,
			&o.Title, &o.Content, &projectID, &project,
			&roadmapNodeID, &o.SortOrder, &o.CreatedAt); err != nil {
			return nil, err
		}
		if completedAt.Valid {
			o.CompletedAt = &completedAt.Int64
		}
		if projectID.Valid {
			o.ProjectID = &projectID.String
		}
		if project.Valid {
			o.Project = project.String
		}
		if roadmapNodeID.Valid {
			o.RoadmapNodeID = &roadmapNodeID.String
		}
		occurrences = append(occurrences, o)
	}
	return occurrences, nil
}

// Ensure interface compliance
var _ storage.RecurrenceRepository = (*recurrenceRepository)(nil)
