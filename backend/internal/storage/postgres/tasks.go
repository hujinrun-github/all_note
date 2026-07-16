package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

type taskRepository struct {
	db postgresRunner
}

func (r taskRepository) List(ctx context.Context, filter storage.TaskFilter) ([]model.Task, int, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, 0, err
	}
	where, args := postgresTaskWhere(filter, workspaceID)
	whereClause := strings.Join(where, " AND ")

	var total int
	if err := r.db.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT COUNT(*)
		FROM tasks t
		LEFT JOIN task_projects p ON p.workspace_id = t.workspace_id AND p.id = t.project_id
		WHERE %s
	`, whereClause), args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	page := filter.Page
	if page <= 0 {
		page = 1
	}
	pageSize := filter.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize
	limitPlaceholder := pgPlaceholder(len(args) + 1)
	offsetPlaceholder := pgPlaceholder(len(args) + 2)
	query := fmt.Sprintf(`
		%s
		WHERE %s
		ORDER BY
			CASE WHEN t.planned_date IS NULL THEN 1 ELSE 0 END ASC,
			t.planned_date DESC,
			t.sort_order ASC,
			t.created_at DESC
		LIMIT %s OFFSET %s
	`, postgresTaskSelectSQL(), whereClause, limitPlaceholder, offsetPlaceholder)
	args = append(args, pageSize, offset)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	tasks, err := scanPostgresTaskRows(rows)
	if err != nil {
		return nil, 0, err
	}
	return tasks, total, nil
}

func postgresTaskWhere(filter storage.TaskFilter, workspaceID string) ([]string, []interface{}) {
	where := []string{"t.workspace_id = $1", "t.deleted_at IS NULL"}
	args := []interface{}{workspaceID}
	next := 2
	if filter.Project != "" {
		where = append(where, fmt.Sprintf("(COALESCE(p.name, t.project, '') = %s OR t.project = %s)", pgPlaceholder(next), pgPlaceholder(next+1)))
		args = append(args, filter.Project, filter.Project)
		next += 2
	}
	if filter.ProjectID != "" {
		where = append(where, fmt.Sprintf("t.project_id = %s", pgPlaceholder(next)))
		args = append(args, filter.ProjectID)
		next++
	}
	if filter.Status == "active" || filter.Status == "open" {
		where = append(where, "COALESCE(t.status, CASE WHEN t.done THEN 'done' ELSE 'open' END) <> 'done'")
	} else if filter.Status == "done" {
		where = append(where, "(t.done = true OR t.status = 'done')")
	}
	if filter.Scope != "" {
		where = append(where, fmt.Sprintf("t.scope = %s", pgPlaceholder(next)))
		args = append(args, filter.Scope)
		next++
	}
	if filter.Horizon != "" {
		where = append(where, fmt.Sprintf("t.horizon = %s", pgPlaceholder(next)))
		args = append(args, filter.Horizon)
		next++
	}
	if filter.PlannedDate != "" {
		where = append(where, fmt.Sprintf("t.planned_date = %s::date", pgPlaceholder(next)))
		args = append(args, filter.PlannedDate)
		next++
	}
	if filter.PlannedFrom != "" {
		where = append(where, fmt.Sprintf("t.planned_date >= %s::date", pgPlaceholder(next)))
		args = append(args, filter.PlannedFrom)
		next++
	}
	if filter.PlannedTo != "" {
		where = append(where, fmt.Sprintf("t.planned_date <= %s::date", pgPlaceholder(next)))
		args = append(args, filter.PlannedTo)
		next++
	}
	if filter.RoadmapNodeID != "" {
		where = append(where, fmt.Sprintf("t.roadmap_node_id = %s", pgPlaceholder(next)))
		args = append(args, filter.RoadmapNodeID)
		next++
	}
	if filter.ExecutionType != "all" {
		if filter.ExecutionType == "recurring" {
			where = append(where, fmt.Sprintf("t.execution_type = %s", pgPlaceholder(next)))
			args = append(args, "recurring")
		} else {
			where = append(where, fmt.Sprintf("(t.execution_type IS NULL OR t.execution_type = '' OR t.execution_type = %s)", pgPlaceholder(next)))
			args = append(args, "single")
		}
	}
	return where, args
}

func (r taskRepository) ListProjects(ctx context.Context) ([]model.TaskProject, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, name, type, description, created_at, updated_at
		FROM task_projects
		WHERE workspace_id = $1
		ORDER BY CASE WHEN id = 'personal' THEN 0 ELSE 1 END, updated_at DESC, lower(name) ASC
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPostgresTaskProjects(rows)
}

func (r taskRepository) CreateProject(ctx context.Context, req *model.CreateTaskProjectRequest) (*model.TaskProject, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, fmt.Errorf("project name is required")
	}
	projectType := normalizeProjectType(req.Type)
	if strings.EqualFold(name, "personal") || name == "个人" {
		projectType = "personal"
	}
	id := "project-" + newID()
	if projectType == "personal" {
		id = "personal"
	}
	now := time.Now().UTC()
	if existing, err := r.GetProjectByName(ctx, name); err == nil {
		if _, err := r.db.ExecContext(ctx, `
			UPDATE task_projects
			SET type = $1, description = $2, updated_at = $3
			WHERE workspace_id = $4 AND id = $5
		`, projectType, strings.TrimSpace(req.Description), now, workspaceID, existing.ID); err != nil {
			return nil, err
		}
		return r.GetProjectByID(ctx, existing.ID)
	} else if err != sql.ErrNoRows {
		return nil, err
	}
	if _, err := r.db.ExecContext(ctx, `
		INSERT INTO task_projects (id, name, type, description, created_at, updated_at, workspace_id)
		VALUES ($1, $2, $3, $4, $5, $5, $6)
	`, id, name, projectType, strings.TrimSpace(req.Description), now, workspaceID); err != nil {
		return nil, err
	}
	return r.GetProjectByName(ctx, name)
}

func (r taskRepository) UpdateProject(ctx context.Context, id string, req *model.UpdateTaskProjectRequest) (*model.TaskProject, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	builder := newPgSetBuilder(1)
	builder.Add("updated_at", time.Now().UTC())
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			return nil, fmt.Errorf("project name is required")
		}
		builder.Add("name", name)
	}
	if req.Type != nil && id != "personal" {
		builder.Add("type", normalizeProjectType(*req.Type))
	}
	if req.Description != nil {
		builder.Add("description", strings.TrimSpace(*req.Description))
	}
	clause, args := builder.ClauseAndArgs()
	args = append(args, id, workspaceID)
	if _, err := r.db.ExecContext(ctx, fmt.Sprintf("UPDATE task_projects SET %s WHERE id = %s AND workspace_id = %s", clause, pgPlaceholder(len(args)-1), pgPlaceholder(len(args))), args...); err != nil {
		return nil, err
	}
	return r.GetProjectByID(ctx, id)
}

func (r taskRepository) DeleteProject(ctx context.Context, id string) error {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("project id is required")
	}
	if id == "personal" {
		return fmt.Errorf("personal project cannot be deleted")
	}
	return r.withTx(ctx, func(tx *sql.Tx) error {
		var existingID string
		if err := tx.QueryRowContext(ctx, `SELECT id FROM task_projects WHERE workspace_id = $1 AND id = $2`, workspaceID, id).Scan(&existingID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE tasks AS t
			SET project_id = 'personal',
				project = COALESCE(personal.name, 'Personal'),
				updated_at = now()
			FROM task_projects AS deleting
			LEFT JOIN task_projects AS personal
				ON personal.id = 'personal'
				AND personal.workspace_id = deleting.workspace_id
			WHERE deleting.workspace_id = $1
				AND deleting.id = $2
				AND t.project_id = deleting.id
				AND t.workspace_id = deleting.workspace_id
		`, workspaceID, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE events
			SET project_id = NULL
			WHERE workspace_id = $1 AND project_id = $2
		`, workspaceID, id); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM task_projects WHERE workspace_id = $1 AND id = $2`, workspaceID, id)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err == nil && affected == 0 {
			return sql.ErrNoRows
		}
		return nil
	})
}

func (r taskRepository) GetProjectByID(ctx context.Context, id string) (*model.TaskProject, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return scanPostgresTaskProject(r.db.QueryRowContext(ctx, `
		SELECT id, name, type, description, created_at, updated_at
		FROM task_projects WHERE workspace_id = $1 AND id = $2
	`, workspaceID, id))
}

func (r taskRepository) GetProjectByName(ctx context.Context, name string) (*model.TaskProject, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return scanPostgresTaskProject(r.db.QueryRowContext(ctx, `
		SELECT id, name, type, description, created_at, updated_at
		FROM task_projects WHERE workspace_id = $1 AND name = $2
	`, workspaceID, strings.TrimSpace(name)))
}

func (r taskRepository) Create(ctx context.Context, task *model.Task) error {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	task.ID = newID()
	now := nowUnix()
	task.CreatedAt = now
	task.UpdatedAt = now
	if err := r.normalizeTaskDefaults(ctx, workspaceID, task); err != nil {
		return err
	}
	clientID := deterministicPostgresMobileEntityClientID("task", workspaceID, task.ID)
	return r.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO tasks (
				id, client_id, revision, title, content, project, project_id, due_at, planned_date, priority, done, status, horizon, scope, sort_order, note_id, roadmap_node_id, execution_type, created_at, updated_at, workspace_id
			)
			VALUES ($1, $2, 1, $3, $4, $5, $6, $7, $8::date, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)
		`, task.ID, clientID, task.Title, task.Content, task.Project, task.ProjectID, postgresTimePtr(task.Due), task.PlannedDate, task.Priority, task.Done == 1, task.Status, task.Horizon, task.Scope, task.SortOrder, task.NoteID, task.RoadmapNodeID, task.ExecutionType, unixToTime(task.CreatedAt), unixToTime(task.UpdatedAt), workspaceID); err != nil {
			return err
		}
		if err := upsertTaskSearchIndex(ctx, tx, workspaceID, task); err != nil {
			return err
		}
		return persistPostgresServerEntityChange(ctx, tx, workspaceID, uuid.NewString(), "task", "task.server_created", clientID, unixToTime(task.UpdatedAt))
	})
}

func (r taskRepository) Update(ctx context.Context, id string, req *model.UpdateTaskRequest) (*model.Task, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	var updated *model.Task
	err = r.withTx(ctx, func(tx *sql.Tx) error {
		// TOCTOU: read current done state inside transaction
		var currentDone bool
		if err := tx.QueryRowContext(ctx, `SELECT done FROM tasks WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL`, workspaceID, id).Scan(&currentDone); err != nil {
			return err
		}

		builder := newPgSetBuilder(1)
		builder.Add("updated_at", time.Now().UTC())
		if req.Title != nil {
			builder.Add("title", strings.TrimSpace(*req.Title))
		}
		if req.Content != nil {
			builder.Add("content", strings.TrimSpace(*req.Content))
		}
		if req.ProjectID != nil {
			project, err := r.getProjectByID(ctx, workspaceID, *req.ProjectID)
			if err != nil {
				return err
			}
			builder.Add("project_id", project.ID)
			builder.Add("project", project.Name)
		} else if req.Project != nil {
			projectID, err := r.ensureTaskProjectByName(ctx, workspaceID, *req.Project, "regular")
			if err != nil {
				return err
			}
			builder.Add("project_id", projectID)
			builder.Add("project", strings.TrimSpace(*req.Project))
		}
		if req.Due != nil {
			builder.Add("due_at", unixToTime(*req.Due))
		}
		if req.PlannedDate != nil {
			val := strings.TrimSpace(*req.PlannedDate)
			if val == "" {
				builder.Add("planned_date", nil)
			} else {
				builder.Add("planned_date", val)
			}
		}
		if req.Priority != nil {
			builder.Add("priority", *req.Priority)
		}
		if req.Done != nil && req.Status == nil {
			builder.Add("done", *req.Done == 1)
			status := "open"
			if *req.Done == 1 {
				status = "done"
			}
			builder.Add("status", status)
			// Branch A: completed_at set/clear
			if *req.Done == 1 && !currentDone {
				builder.Add("completed_at", time.Now())
			} else if *req.Done != 1 && currentDone {
				builder.Add("completed_at", nil)
			}
		}
		if req.Status != nil {
			status := normalizeTaskStatus(*req.Status)
			builder.Add("status", status)
			newDone := status == "done"
			builder.Add("done", newDone)
			// Branch B: completed_at set/clear via status change
			if newDone && !currentDone {
				builder.Add("completed_at", time.Now())
			} else if !newDone && currentDone {
				builder.Add("completed_at", nil)
			}
		}
		if req.Scope != nil {
			builder.Add("scope", normalizeScope(*req.Scope))
		}
		if req.Horizon != nil {
			builder.Add("horizon", normalizeHorizon(*req.Horizon))
		}
		if req.SortOrder != nil {
			builder.Add("sort_order", *req.SortOrder)
		}
		if req.RoadmapNodeID != nil {
			builder.Add("roadmap_node_id", *req.RoadmapNodeID)
		}
		if req.ExecutionType != nil {
			builder.Add("execution_type", *req.ExecutionType)
		}
		clause, args := builder.ClauseAndArgs()
		args = append(args, id, workspaceID)

		result, err := tx.ExecContext(ctx, fmt.Sprintf("UPDATE tasks SET %s, revision = revision + 1 WHERE id = %s AND workspace_id = %s AND deleted_at IS NULL", clause, pgPlaceholder(len(args)-1), pgPlaceholder(len(args))), args...)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err == nil && affected == 0 {
			return sql.ErrNoRows
		}
		task, err := scanPostgresTaskRow(tx.QueryRowContext(ctx, postgresTaskSelectSQL()+` WHERE t.workspace_id = $1 AND t.id = $2 AND t.deleted_at IS NULL`, workspaceID, id))
		if err != nil {
			return err
		}
		if err := upsertTaskSearchIndex(ctx, tx, workspaceID, task); err != nil {
			return err
		}
		if err := r.syncRoadmapNodeStatus(ctx, tx, workspaceID, task); err != nil {
			return err
		}
		updated = task
		var clientID string
		if err := tx.QueryRowContext(ctx, `SELECT client_id FROM tasks WHERE workspace_id = $1 AND id = $2`, workspaceID, id).Scan(&clientID); err != nil {
			return err
		}
		return persistPostgresServerEntityChange(ctx, tx, workspaceID, uuid.NewString(), "task", "task.server_updated", clientID, time.Now().UTC())
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

func (r taskRepository) GetByID(ctx context.Context, id string) (*model.Task, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return scanPostgresTaskRow(r.db.QueryRowContext(ctx, postgresTaskSelectSQL()+` WHERE t.workspace_id = $1 AND t.id = $2 AND t.deleted_at IS NULL`, workspaceID, id))
}

func (r taskRepository) Delete(ctx context.Context, id string) error {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	return r.withTx(ctx, func(tx *sql.Tx) error {
		var clientID string
		err := tx.QueryRowContext(ctx, `SELECT client_id FROM tasks WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL FOR UPDATE`, workspaceID, id).Scan(&clientID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := tombstonePostgresTaskOccurrences(ctx, tx, workspaceID, id, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM task_recurrence_rules WHERE workspace_id = $1 AND task_id = $2`, workspaceID, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE tasks SET deleted_at = $1, updated_at = $1, revision = revision + 1 WHERE workspace_id = $2 AND id = $3 AND deleted_at IS NULL`, now, workspaceID, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO mobile_retired_ids (workspace_id, entity_type, client_id, retired_at) VALUES ($1, 'task', $2, $3) ON CONFLICT DO NOTHING`, workspaceID, clientID, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM search_index WHERE workspace_id = $1 AND entity_type = 'task' AND entity_id = $2`, workspaceID, id); err != nil {
			return err
		}
		return persistPostgresServerEntityChange(ctx, tx, workspaceID, uuid.NewString(), "task", "task.server_deleted", clientID, now)
	})
}

func (r taskRepository) Today(ctx context.Context, todayStart, todayEnd, overdueCutoff int64) ([]model.Task, []model.Task, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	todayDate := time.Unix(todayStart, 0).In(time.Local).Format("2006-01-02")
	overdueCutoffDate := time.Unix(overdueCutoff, 0).In(time.Local).Format("2006-01-02")
	rows, err := r.db.QueryContext(ctx, postgresTaskSelectSQL()+`
		WHERE t.workspace_id = $1 AND t.deleted_at IS NULL AND t.done = false AND (t.execution_type IS NULL OR t.execution_type = '' OR t.execution_type = 'single') AND (
			(t.due_at >= $2 AND t.due_at < $3)
			OR (COALESCE(t.horizon, CASE WHEN t.scope IN ('monthly', 'yearly') THEN 'long' ELSE 'week' END) <> 'long' AND t.planned_date = $4::date)
			OR (COALESCE(t.horizon, CASE WHEN t.scope IN ('monthly', 'yearly') THEN 'long' ELSE 'week' END) = 'long'
				AND COALESCE(t.status, CASE WHEN t.done THEN 'done' ELSE 'open' END) = 'active')
		)
		ORDER BY t.sort_order ASC, t.created_at DESC
	`, workspaceID, unixToTime(todayStart), unixToTime(todayEnd), todayDate)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	todayTasks, err := scanPostgresTaskRows(rows)
	if err != nil {
		return nil, nil, err
	}

	rows, err = r.db.QueryContext(ctx, postgresTaskSelectSQL()+`
		WHERE t.workspace_id = $1
			AND t.deleted_at IS NULL
			AND t.done = false
			AND (t.execution_type IS NULL OR t.execution_type = '' OR t.execution_type = 'single')
			AND COALESCE(t.horizon, CASE WHEN t.scope IN ('monthly', 'yearly') THEN 'long' ELSE 'week' END) <> 'long'
			AND ((t.due_at < $2 AND t.due_at >= $3) OR (t.due_at IS NULL AND t.planned_date < $4::date AND t.planned_date >= $5::date))
			AND (t.planned_date IS NULL OR t.planned_date <> $4::date)
		ORDER BY t.due_at ASC LIMIT 10
	`, workspaceID, unixToTime(todayStart), unixToTime(overdueCutoff), todayDate, overdueCutoffDate)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	overdueTasks, err := scanPostgresTaskRows(rows)
	if err != nil {
		return nil, nil, err
	}
	return todayTasks, overdueTasks, nil
}

func (r taskRepository) GetCompletedTasksByRange(ctx context.Context, from, to int64, page, pageSize int) ([]model.TaskSummary, int, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, 0, err
	}
	var total int
	if err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM tasks WHERE workspace_id = $1 AND deleted_at IS NULL AND completed_at >= $2 AND completed_at < $3`,
		workspaceID, unixToTime(from), unixToTime(to),
	).Scan(&total); err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	rows, err := r.db.QueryContext(ctx,
		`SELECT t.id, t.title, t.done, t.planned_date, t.due_at, t.completed_at, t.note_id,
		        p.id, p.name, p.type
		 FROM tasks t LEFT JOIN task_projects p ON p.workspace_id = t.workspace_id AND t.project_id = p.id
		 WHERE t.workspace_id = $1 AND t.deleted_at IS NULL AND t.completed_at >= $2 AND t.completed_at < $3
		 ORDER BY t.completed_at DESC LIMIT $4 OFFSET $5`,
		workspaceID, unixToTime(from), unixToTime(to), pageSize, offset,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var summaries []model.TaskSummary
	for rows.Next() {
		var s model.TaskSummary
		var done bool
		var dueAt sql.NullTime
		var completedAt sql.NullTime
		var projectID, projectName, projectType sql.NullString
		if err := rows.Scan(&s.ID, &s.Title, &done, &s.PlannedDate, &dueAt, &completedAt,
			&s.NoteID, &projectID, &projectName, &projectType); err != nil {
			return nil, 0, err
		}
		if done {
			s.Done = 1
		}
		if dueAt.Valid {
			v := timeToUnix(dueAt.Time)
			s.Due = &v
		}
		if completedAt.Valid {
			v := timeToUnix(completedAt.Time)
			s.CompletedAt = &v
		}
		if projectID.Valid {
			s.Project = &model.TaskProject{ID: projectID.String, Name: projectName.String, Type: projectType.String}
		}
		summaries = append(summaries, s)
	}
	return summaries, total, rows.Err()
}

func (r taskRepository) GetSummaryStats(ctx context.Context, from, to int64) (int, int, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return 0, 0, err
	}
	var activeDays, projectCount int
	err = r.db.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT DATE(completed_at)),
		        COUNT(DISTINCT project_id)
		 FROM tasks WHERE workspace_id = $1 AND deleted_at IS NULL AND completed_at >= $2 AND completed_at < $3`,
		workspaceID, unixToTime(from), unixToTime(to),
	).Scan(&activeDays, &projectCount)
	return activeDays, projectCount, err
}

func postgresTaskSelectSQL() string {
	return `
		SELECT
			t.id, t.title, COALESCE(t.content, ''), COALESCE(p.name, t.project),
			t.project_id, p.type, t.due_at, t.planned_date, t.priority, t.done,
			COALESCE(t.status, CASE WHEN t.done THEN 'done' ELSE 'open' END),
			COALESCE(t.horizon, CASE WHEN t.scope IN ('monthly', 'yearly') THEN 'long' ELSE 'week' END),
			t.scope, t.sort_order, t.note_id, t.roadmap_node_id, t.created_at, t.updated_at,
			t.completed_at, t.execution_type
		FROM tasks t
		LEFT JOIN task_projects p ON p.workspace_id = t.workspace_id AND p.id = t.project_id
	`
}

func (r taskRepository) normalizeTaskDefaults(ctx context.Context, workspaceID string, task *model.Task) error {
	task.Title = strings.TrimSpace(task.Title)
	if task.Scope == "" {
		task.Scope = "daily"
	}
	task.Scope = normalizeScope(task.Scope)
	task.Horizon = normalizeHorizon(task.Horizon)
	if task.Horizon == "long" && task.Scope == "daily" {
		task.Scope = "yearly"
	}
	task.Status = normalizeTaskStatus(task.Status)
	if task.Status == "done" || task.Done == 1 {
		task.Done = 1
		task.Status = "done"
	}
	if strings.TrimSpace(task.ExecutionType) == "" {
		task.ExecutionType = "single"
	}
	// Recurring templates never get planned_date
	if task.ExecutionType == "recurring" {
		return nil
	}
	if task.PlannedDate == nil {
		planned := time.Now().Format("2006-01-02")
		if task.Due != nil {
			planned = time.Unix(*task.Due, 0).Format("2006-01-02")
		}
		task.PlannedDate = &planned
	}
	if task.ProjectID == nil || strings.TrimSpace(*task.ProjectID) == "" {
		if task.Project != nil {
			projectID, err := r.ensureTaskProjectByName(ctx, workspaceID, *task.Project, "regular")
			if err != nil {
				return err
			}
			task.ProjectID = &projectID
		}
	}
	if task.ProjectID == nil || strings.TrimSpace(*task.ProjectID) == "" {
		projectID := "personal"
		task.ProjectID = &projectID
	}
	if project, err := r.getProjectByID(ctx, workspaceID, *task.ProjectID); err == nil {
		name := project.Name
		task.Project = &name
		task.ProjectType = &project.Type
	}
	return nil
}

func (r taskRepository) ensureTaskProjectByName(ctx context.Context, workspaceID string, name, projectType string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" || strings.EqualFold(trimmed, "personal") || trimmed == "个人" {
		return "personal", nil
	}
	project, err := r.getProjectByName(ctx, workspaceID, trimmed)
	if err == nil {
		return project.ID, nil
	}
	if err != sql.ErrNoRows {
		return "", err
	}
	created, err := r.CreateProject(ctx, &model.CreateTaskProjectRequest{Name: trimmed, Type: projectType})
	if err != nil {
		return "", err
	}
	return created.ID, nil
}

func (r taskRepository) getProjectByID(ctx context.Context, workspaceID string, id string) (*model.TaskProject, error) {
	return scanPostgresTaskProject(r.db.QueryRowContext(ctx, `
		SELECT id, name, type, description, created_at, updated_at
		FROM task_projects WHERE workspace_id = $1 AND id = $2
	`, workspaceID, id))
}

func (r taskRepository) getProjectByName(ctx context.Context, workspaceID string, name string) (*model.TaskProject, error) {
	return scanPostgresTaskProject(r.db.QueryRowContext(ctx, `
		SELECT id, name, type, description, created_at, updated_at
		FROM task_projects WHERE workspace_id = $1 AND name = $2
	`, workspaceID, strings.TrimSpace(name)))
}

func (r taskRepository) syncRoadmapNodeStatus(ctx context.Context, tx *sql.Tx, workspaceID string, task *model.Task) error {
	if task.RoadmapNodeID == nil {
		return nil
	}
	status := ""
	if task.Done == 1 || task.Status == "done" {
		status = "done"
	} else if task.Status == "open" {
		status = "active"
	}
	if status == "" {
		return nil
	}
	_, err := tx.ExecContext(ctx, `
		UPDATE roadmap_nodes
		SET status = $1, updated_at = $2
		WHERE workspace_id = $3 AND id = $4
	`, status, time.Now().UTC(), workspaceID, *task.RoadmapNodeID)
	return err
}

func (r taskRepository) withTx(ctx context.Context, fn func(*sql.Tx) error) error {
	if tx, ok := r.db.(*sql.Tx); ok {
		return fn(tx)
	}
	db, ok := r.db.(*sql.DB)
	if !ok {
		return fmt.Errorf("unsupported postgres runner %T", r.db)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func normalizeProjectType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "personal":
		return "personal"
	case "learning":
		return "learning"
	default:
		return "regular"
	}
}

func normalizeHorizon(value string) string {
	if strings.ToLower(strings.TrimSpace(value)) == "long" {
		return "long"
	}
	return "week"
}

func normalizeTaskStatus(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "active", "blocked", "done", "migrated":
		return strings.ToLower(strings.TrimSpace(value))
	case "cancelled", "canceled":
		return "cancelled"
	default:
		return "open"
	}
}

func normalizeScope(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "monthly":
		return "monthly"
	case "yearly":
		return "yearly"
	default:
		return "daily"
	}
}

func postgresTimePtr(value *int64) interface{} {
	if value == nil {
		return nil
	}
	return unixToTime(*value)
}

func scanPostgresTaskProject(row rowScanner) (*model.TaskProject, error) {
	var project model.TaskProject
	var createdAt time.Time
	var updatedAt time.Time
	if err := row.Scan(&project.ID, &project.Name, &project.Type, &project.Description, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	project.CreatedAt = timeToUnix(createdAt)
	project.UpdatedAt = timeToUnix(updatedAt)
	return &project, nil
}

func scanPostgresTaskProjects(rows *sql.Rows) ([]model.TaskProject, error) {
	projects := make([]model.TaskProject, 0)
	for rows.Next() {
		project, err := scanPostgresTaskProject(rows)
		if err != nil {
			return nil, err
		}
		projects = append(projects, *project)
	}
	return projects, rows.Err()
}

func scanPostgresTaskRow(row rowScanner) (*model.Task, error) {
	var task model.Task
	var project sql.NullString
	var projectID sql.NullString
	var projectType sql.NullString
	var dueAt sql.NullTime
	var plannedDate sql.NullTime
	var done bool
	var noteID sql.NullString
	var roadmapNodeID sql.NullString
	var createdAt time.Time
	var updatedAt time.Time
	var completedAt sql.NullTime
	if err := row.Scan(
		&task.ID, &task.Title, &task.Content, &project, &projectID, &projectType,
		&dueAt, &plannedDate, &task.Priority, &done, &task.Status, &task.Horizon,
		&task.Scope, &task.SortOrder, &noteID, &roadmapNodeID, &createdAt, &updatedAt,
		&completedAt, &task.ExecutionType,
	); err != nil {
		return nil, err
	}
	if project.Valid {
		task.Project = &project.String
	}
	if projectID.Valid {
		task.ProjectID = &projectID.String
	}
	if projectType.Valid {
		task.ProjectType = &projectType.String
	}
	if dueAt.Valid {
		value := timeToUnix(dueAt.Time)
		task.Due = &value
	}
	if plannedDate.Valid {
		value := plannedDate.Time.Format("2006-01-02")
		task.PlannedDate = &value
	}
	if done {
		task.Done = 1
	}
	if noteID.Valid {
		task.NoteID = &noteID.String
	}
	if roadmapNodeID.Valid {
		task.RoadmapNodeID = &roadmapNodeID.String
	}
	task.CreatedAt = timeToUnix(createdAt)
	task.UpdatedAt = timeToUnix(updatedAt)
	if completedAt.Valid {
		u := timeToUnix(completedAt.Time)
		task.CompletedAt = &u
	}
	return &task, nil
}

func scanPostgresTaskRows(rows *sql.Rows) ([]model.Task, error) {
	tasks := make([]model.Task, 0)
	for rows.Next() {
		task, err := scanPostgresTaskRow(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, *task)
	}
	return tasks, rows.Err()
}

func upsertTaskSearchIndex(ctx context.Context, tx *sql.Tx, workspaceID string, task *model.Task) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO search_index (workspace_id, entity_type, entity_id, title, content, tags, updated_at, search_vector)
		VALUES (
			$1,
			'task',
			$2,
			$3,
			$4,
			'{}'::text[],
			$5,
			to_tsvector('simple', coalesce($3, '') || ' ' || coalesce($4, ''))
		)
		ON CONFLICT (entity_type, entity_id) DO UPDATE SET
			workspace_id = excluded.workspace_id,
			title = excluded.title,
			content = excluded.content,
			updated_at = excluded.updated_at,
			search_vector = excluded.search_vector
	`, workspaceID, task.ID, task.Title, task.Content, unixToTime(task.UpdatedAt))
	return err
}
