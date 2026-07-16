package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

const recurrenceSchemaSQL = `
CREATE TABLE IF NOT EXISTS task_recurrence_rules (
	task_id TEXT PRIMARY KEY REFERENCES tasks(id) ON DELETE CASCADE,
	start_date TEXT NOT NULL,
	end_date TEXT,
	frequency TEXT NOT NULL CHECK (frequency IN ('daily','weekly','monthly')),
	interval INTEGER NOT NULL DEFAULT 1 CHECK (interval >= 1),
	weekdays TEXT NOT NULL DEFAULT '[]',
	month_days TEXT NOT NULL DEFAULT '[]',
	timezone TEXT NOT NULL,
	enabled INTEGER NOT NULL DEFAULT 1,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS task_occurrences (
	task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
	occurrence_date TEXT NOT NULL,
	status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open','done','skipped')),
	completed_at INTEGER,
	note TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	PRIMARY KEY (task_id, occurrence_date)
);

CREATE INDEX IF NOT EXISTS idx_occurrences_date ON task_occurrences (occurrence_date, status);
CREATE INDEX IF NOT EXISTS idx_occurrences_task_date ON task_occurrences (task_id, occurrence_date);
CREATE INDEX IF NOT EXISTS idx_occurrences_completed ON task_occurrences (completed_at) WHERE completed_at IS NOT NULL;
`

func ensureRecurrenceSchema(db sqliteRunner) error {
	_, err := db.ExecContext(context.Background(), recurrenceSchemaSQL)
	return err
}

type recurrenceRepository struct {
	db sqliteRunner
}

func (r recurrenceRepository) UpsertRule(ctx context.Context, rule *model.RecurrenceRule) error {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	if err := r.ensureTaskInWorkspace(ctx, workspaceID, rule.TaskID); err != nil {
		return err
	}
	weekdaysJSON, err := json.Marshal(rule.Weekdays)
	if err != nil {
		return err
	}
	monthDaysJSON, err := json.Marshal(rule.MonthDays)
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	enabled := 0
	if rule.Enabled {
		enabled = 1
	}

	_, err = r.db.ExecContext(ctx, `
		INSERT INTO task_recurrence_rules (task_id, start_date, end_date, frequency, interval, weekdays, month_days, timezone, enabled, created_at, updated_at, workspace_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(task_id) DO UPDATE SET
			start_date = EXCLUDED.start_date,
			end_date = EXCLUDED.end_date,
			frequency = EXCLUDED.frequency,
			interval = EXCLUDED.interval,
			weekdays = EXCLUDED.weekdays,
			month_days = EXCLUDED.month_days,
			timezone = EXCLUDED.timezone,
			enabled = EXCLUDED.enabled,
			workspace_id = EXCLUDED.workspace_id,
			updated_at = EXCLUDED.updated_at
	`, rule.TaskID, rule.StartDate, rule.EndDate, rule.Frequency, rule.Interval,
		string(weekdaysJSON), string(monthDaysJSON), rule.Timezone, enabled, now, now, workspaceID)
	return err
}

func (r recurrenceRepository) GetRule(ctx context.Context, taskID string) (*model.RecurrenceRule, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	rule := &model.RecurrenceRule{}
	var endDate sql.NullString
	var weekdaysJSON, monthDaysJSON string
	var enabled int
	err = r.db.QueryRowContext(ctx, `
		SELECT task_id, start_date, end_date, frequency, interval, weekdays, month_days, timezone, enabled, created_at, updated_at
		FROM task_recurrence_rules WHERE workspace_id = ? AND task_id = ?
	`, workspaceID, taskID).Scan(&rule.TaskID, &rule.StartDate, &endDate, &rule.Frequency, &rule.Interval,
		&weekdaysJSON, &monthDaysJSON, &rule.Timezone, &enabled,
		&rule.CreatedAt, &rule.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if endDate.Valid {
		rule.EndDate = &endDate.String
	}
	rule.Enabled = enabled == 1
	if err := json.Unmarshal([]byte(weekdaysJSON), &rule.Weekdays); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(monthDaysJSON), &rule.MonthDays); err != nil {
		return nil, err
	}
	return rule, nil
}

func (r recurrenceRepository) DeleteRule(ctx context.Context, taskID string) error {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `DELETE FROM task_recurrence_rules WHERE workspace_id = ? AND task_id = ?`, workspaceID, taskID)
	return err
}

func (r recurrenceRepository) ListActiveRules(ctx context.Context, from, to string) ([]model.RecurrenceRule, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT task_id, start_date, end_date, frequency, interval, weekdays, month_days, timezone, enabled, created_at, updated_at
		FROM task_recurrence_rules
		WHERE workspace_id = ?
			AND enabled = 1
			AND start_date <= ?
			AND (end_date IS NULL OR end_date >= ?)
	`, workspaceID, to, from)
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
		SELECT o.task_id, o.occurrence_date, o.status, o.completed_at, o.note,
			t.title, COALESCE(t.content, ''), t.project_id, COALESCE(p.name, t.project),
			t.roadmap_node_id, t.sort_order, o.created_at
		FROM task_occurrences o
		JOIN tasks t ON t.workspace_id = o.workspace_id AND t.id = o.task_id
		LEFT JOIN task_projects p ON p.workspace_id = t.workspace_id AND p.id = t.project_id
		WHERE o.workspace_id = ? AND o.deleted_at IS NULL AND t.deleted_at IS NULL
			AND o.occurrence_date >= ? AND o.occurrence_date <= ?
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
		SELECT o.task_id AS id, t.title, o.completed_at,
			t.project_id, p.name AS project_name, p.type AS project_type,
			t.due, o.occurrence_date
		FROM task_occurrences o
		JOIN tasks t ON t.workspace_id = o.workspace_id AND t.id = o.task_id
		LEFT JOIN task_projects p ON p.workspace_id = t.workspace_id AND p.id = t.project_id
		WHERE o.workspace_id = ? AND o.deleted_at IS NULL AND t.deleted_at IS NULL
			AND o.completed_at IS NOT NULL
			AND o.completed_at >= ?
			AND o.completed_at < ?
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
	now := time.Now().Unix()
	occurrenceID := deterministicSQLiteMobileEntityClientID("task_occurrence", workspaceID, taskID+":"+date)
	return (mobileSyncRepository{db: r.db}).withTx(ctx, func(tx *sql.Tx) error {
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_occurrences WHERE workspace_id = ? AND task_id = ? AND occurrence_date = ?`, workspaceID, taskID, date).Scan(&exists); err != nil {
			return err
		}
		operation := "task_occurrence.server_created"
		if exists == 0 {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO task_occurrences (
					task_id, occurrence_date, occurrence_id, revision, status, completed_at, created_at, updated_at, workspace_id
				) VALUES (?, ?, ?, 1, ?, ?, ?, ?, ?)
			`, taskID, date, occurrenceID, status, completedAt, now, now, workspaceID); err != nil {
				return err
			}
		} else {
			operation = "task_occurrence.server_updated"
			if _, err := tx.ExecContext(ctx, `
				UPDATE task_occurrences SET status = ?, completed_at = ?, revision = revision + 1, updated_at = ?
				WHERE workspace_id = ? AND task_id = ? AND occurrence_date = ? AND deleted_at IS NULL
			`, status, completedAt, now, workspaceID, taskID, date); err != nil {
				return err
			}
		}
		return persistSQLiteServerEntityChange(ctx, tx, workspaceID, uuid.NewString(), "task_occurrence", operation, occurrenceID, now)
	})
}

func (r recurrenceRepository) CountOccurrencesByTask(ctx context.Context, taskID string) (int, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return 0, err
	}
	var count int
	err = r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_occurrences WHERE workspace_id = ? AND task_id = ? AND deleted_at IS NULL`, workspaceID, taskID).Scan(&count)
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
		SELECT task_id, occurrence_date, status, completed_at, note, created_at
		FROM task_occurrences WHERE workspace_id = ? AND task_id = ? AND occurrence_date = ?
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
	if err := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM tasks WHERE workspace_id = ? AND id = ? AND deleted_at IS NULL)`, workspaceID, taskID).Scan(&exists); err != nil {
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
		var weekdaysJSON, monthDaysJSON string
		var enabled int
		if err := rows.Scan(&r.TaskID, &r.StartDate, &endDate, &r.Frequency, &r.Interval,
			&weekdaysJSON, &monthDaysJSON, &r.Timezone, &enabled,
			&r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		if endDate.Valid {
			r.EndDate = &endDate.String
		}
		r.Enabled = enabled == 1
		if err := json.Unmarshal([]byte(weekdaysJSON), &r.Weekdays); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(monthDaysJSON), &r.MonthDays); err != nil {
			return nil, err
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
