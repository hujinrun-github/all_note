package postgres

import (
	"context"
	"database/sql"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/lib/pq"
)

type recurrenceRepository struct {
	db postgresRunner
}

func (r recurrenceRepository) UpsertRule(ctx context.Context, rule *model.RecurrenceRule) error {
	weekdays := rule.Weekdays
	if weekdays == nil {
		weekdays = []int{}
	}
	monthDays := rule.MonthDays
	if monthDays == nil {
		monthDays = []int{}
	}

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO task_recurrence_rules (task_id, start_date, end_date, frequency, interval, weekdays, month_days, timezone, enabled)
		VALUES ($1, $2::date, $3::date, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (task_id) DO UPDATE SET
			start_date = EXCLUDED.start_date,
			end_date = EXCLUDED.end_date,
			frequency = EXCLUDED.frequency,
			interval = EXCLUDED.interval,
			weekdays = EXCLUDED.weekdays,
			month_days = EXCLUDED.month_days,
			timezone = EXCLUDED.timezone,
			enabled = EXCLUDED.enabled,
			updated_at = now()
	`, rule.TaskID, rule.StartDate, rule.EndDate, rule.Frequency, rule.Interval,
		pq.Array(weekdays), pq.Array(monthDays), rule.Timezone, rule.Enabled)
	return err
}

func (r recurrenceRepository) GetRule(ctx context.Context, taskID string) (*model.RecurrenceRule, error) {
	rule := &model.RecurrenceRule{}
	var endDate sql.NullString
	var weekdays, monthDays []int64
	err := r.db.QueryRowContext(ctx, `
		SELECT task_id, start_date::text, end_date::text, frequency, interval, weekdays, month_days, timezone, enabled,
			EXTRACT(EPOCH FROM created_at)::bigint, EXTRACT(EPOCH FROM updated_at)::bigint
		FROM task_recurrence_rules WHERE task_id = $1
	`, taskID).Scan(&rule.TaskID, &rule.StartDate, &endDate, &rule.Frequency, &rule.Interval,
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
	_, err := r.db.ExecContext(ctx, `DELETE FROM task_recurrence_rules WHERE task_id = $1`, taskID)
	return err
}

func (r recurrenceRepository) ListActiveRules(ctx context.Context, from, to string) ([]model.RecurrenceRule, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT task_id, start_date::text, end_date::text, frequency, interval, weekdays, month_days, timezone, enabled,
			EXTRACT(EPOCH FROM created_at)::bigint, EXTRACT(EPOCH FROM updated_at)::bigint
		FROM task_recurrence_rules
		WHERE enabled = true
			AND start_date <= $2::date
			AND (end_date IS NULL OR end_date >= $1::date)
	`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRecurrenceRules(rows)
}

func (r recurrenceRepository) ListOccurrences(ctx context.Context, from, to string) ([]model.TaskOccurrence, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT o.task_id, o.occurrence_date::text, o.status, EXTRACT(EPOCH FROM o.completed_at)::bigint AS completed_at, o.note,
			t.title, COALESCE(t.content, ''), t.project_id, COALESCE(p.name, t.project),
			t.roadmap_node_id, t.sort_order,
			EXTRACT(EPOCH FROM o.created_at)::bigint
		FROM task_occurrences o
		JOIN tasks t ON t.id = o.task_id
		LEFT JOIN task_projects p ON p.id = t.project_id
		WHERE o.occurrence_date >= $1::date AND o.occurrence_date <= $2::date
		ORDER BY o.occurrence_date, t.sort_order
	`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanOccurrences(rows)
}

func (r recurrenceRepository) GetCompletedOccurrencesByRange(ctx context.Context, from, to int64) ([]model.TaskSummary, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT o.task_id AS id, t.title, EXTRACT(EPOCH FROM o.completed_at)::bigint AS completed_at,
			t.project_id, p.name AS project_name, p.type AS project_type,
			EXTRACT(EPOCH FROM t.due_at)::bigint, o.occurrence_date::text
		FROM task_occurrences o
		JOIN tasks t ON t.id = o.task_id
		LEFT JOIN task_projects p ON p.id = t.project_id
		WHERE o.completed_at IS NOT NULL
			AND o.completed_at >= to_timestamp($1)
			AND o.completed_at < to_timestamp($2)
		ORDER BY o.completed_at DESC
	`, from, to)
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
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO task_occurrences (task_id, occurrence_date, status, completed_at)
		VALUES ($1, $2::date, 'done', to_timestamp($3))
		ON CONFLICT (task_id, occurrence_date) DO UPDATE SET
			status = 'done', completed_at = to_timestamp($3), updated_at = now()
	`, taskID, date, completedAt)
	if err != nil {
		return nil, err
	}
	return r.getOccurrence(ctx, taskID, date)
}

func (r recurrenceRepository) ReopenOccurrence(ctx context.Context, taskID, date string) (*model.TaskOccurrence, error) {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO task_occurrences (task_id, occurrence_date, status, completed_at)
		VALUES ($1, $2::date, 'open', NULL)
		ON CONFLICT (task_id, occurrence_date) DO UPDATE SET
			status = 'open', completed_at = NULL, updated_at = now()
	`, taskID, date)
	if err != nil {
		return nil, err
	}
	return r.getOccurrence(ctx, taskID, date)
}

func (r recurrenceRepository) SkipOccurrence(ctx context.Context, taskID, date string) (*model.TaskOccurrence, error) {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO task_occurrences (task_id, occurrence_date, status, completed_at)
		VALUES ($1, $2::date, 'skipped', NULL)
		ON CONFLICT (task_id, occurrence_date) DO UPDATE SET
			status = 'skipped', completed_at = NULL, updated_at = now()
	`, taskID, date)
	if err != nil {
		return nil, err
	}
	return r.getOccurrence(ctx, taskID, date)
}

func (r recurrenceRepository) CountOccurrencesByTask(ctx context.Context, taskID string) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_occurrences WHERE task_id = $1`, taskID).Scan(&count)
	return count, err
}

func (r recurrenceRepository) getOccurrence(ctx context.Context, taskID, date string) (*model.TaskOccurrence, error) {
	o := &model.TaskOccurrence{}
	var completedAt sql.NullInt64
	err := r.db.QueryRowContext(ctx, `
		SELECT task_id, occurrence_date::text, status,
			EXTRACT(EPOCH FROM completed_at)::bigint, note,
			EXTRACT(EPOCH FROM created_at)::bigint
		FROM task_occurrences WHERE task_id = $1 AND occurrence_date = $2::date
	`, taskID, date).Scan(&o.TaskID, &o.OccurrenceDate, &o.Status, &completedAt, &o.Note, &o.CreatedAt)
	if err != nil {
		return nil, err
	}
	if completedAt.Valid {
		o.CompletedAt = &completedAt.Int64
	}
	return o, nil
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
