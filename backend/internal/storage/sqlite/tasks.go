package sqlite

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
	db sqliteRunner
}

func (r taskRepository) withTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	db, ok := r.db.(*sql.DB)
	if !ok {
		// Already in a transaction, run directly
		return fn(r.db.(*sql.Tx))
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (r taskRepository) List(ctx context.Context, filter storage.TaskFilter) ([]model.Task, int, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, 0, err
	}
	where, args := sqliteTaskWhere(filter, workspaceID)
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
	query := fmt.Sprintf(`
		SELECT
			t.id, t.title, COALESCE(t.content, ''), COALESCE(p.name, t.project),
			t.project_id, p.type, t.due, t.planned_date, t.priority, t.done,
			COALESCE(t.status, CASE WHEN t.done = 1 THEN 'done' ELSE 'open' END),
			COALESCE(t.horizon, CASE WHEN t.scope IN ('monthly', 'yearly') THEN 'long' ELSE 'week' END),
			t.scope, t.sort_order, t.note_id, t.roadmap_node_id, t.created_at, t.updated_at,
			t.completed_at, t.execution_type
		FROM tasks t
		LEFT JOIN task_projects p ON p.workspace_id = t.workspace_id AND p.id = t.project_id
		WHERE %s
		ORDER BY
			CASE WHEN t.planned_date IS NULL THEN 1 ELSE 0 END ASC,
			t.planned_date DESC,
			t.sort_order ASC,
			t.created_at DESC
		LIMIT ? OFFSET ?
	`, whereClause)
	args = append(args, pageSize, offset)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	tasks, err := scanSQLiteTaskRows(rows)
	if err != nil {
		return nil, 0, err
	}
	return tasks, total, nil
}

func sqliteTaskWhere(filter storage.TaskFilter, workspaceID string) ([]string, []interface{}) {
	where := []string{"t.workspace_id = ?", "t.deleted_at IS NULL"}
	args := []interface{}{workspaceID}
	if filter.Project != "" {
		where = append(where, "(COALESCE(p.name, t.project, '') = ? OR t.project = ?)")
		args = append(args, filter.Project, filter.Project)
	}
	if filter.ProjectID != "" {
		where = append(where, "t.project_id = ?")
		args = append(args, filter.ProjectID)
	}
	if filter.Status == "active" || filter.Status == "open" {
		where = append(where, "COALESCE(t.status, CASE WHEN t.done = 1 THEN 'done' ELSE 'open' END) <> 'done'")
	} else if filter.Status == "done" {
		where = append(where, "(t.done = 1 OR t.status = 'done')")
	}
	if filter.Scope != "" {
		where = append(where, "t.scope = ?")
		args = append(args, filter.Scope)
	}
	if filter.Horizon != "" {
		where = append(where, "t.horizon = ?")
		args = append(args, filter.Horizon)
	}
	if filter.PlannedDate != "" {
		where = append(where, "t.planned_date = ?")
		args = append(args, filter.PlannedDate)
	}
	if filter.RoadmapNodeID != "" {
		where = append(where, "t.roadmap_node_id = ?")
		args = append(args, filter.RoadmapNodeID)
	}
	if filter.ExecutionType != "all" {
		if filter.ExecutionType == "recurring" {
			where = append(where, "t.execution_type = ?")
			args = append(args, "recurring")
		} else {
			where = append(where, "(t.execution_type IS NULL OR t.execution_type = '' OR t.execution_type = ?)")
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
		WHERE workspace_id = ?
		ORDER BY CASE WHEN id = 'personal' THEN 0 ELSE 1 END, updated_at DESC, lower(name) ASC
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSQLiteTaskProjects(rows)
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
	if name == "个人" || strings.EqualFold(name, "personal") {
		projectType = "personal"
	}
	id := "project-" + newID()
	if projectType == "personal" {
		id = "personal"
	}
	now := nowUnix()
	if existing, err := r.GetProjectByName(ctx, name); err == nil {
		if _, err := r.db.ExecContext(ctx, `
			UPDATE task_projects
			SET type = ?, description = ?, updated_at = ?
			WHERE workspace_id = ? AND id = ?
		`, projectType, strings.TrimSpace(req.Description), now, workspaceID, existing.ID); err != nil {
			return nil, err
		}
		return r.GetProjectByID(ctx, existing.ID)
	} else if err != sql.ErrNoRows {
		return nil, err
	}
	if _, err := r.db.ExecContext(ctx, `
		INSERT INTO task_projects (id, name, type, description, created_at, updated_at, workspace_id)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, id, name, projectType, strings.TrimSpace(req.Description), now, now, workspaceID); err != nil {
		return nil, err
	}
	return r.GetProjectByName(ctx, name)
}

func (r taskRepository) UpdateProject(ctx context.Context, id string, req *model.UpdateTaskProjectRequest) (*model.TaskProject, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	sets := []string{"updated_at = ?"}
	args := []interface{}{nowUnix()}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			return nil, fmt.Errorf("project name is required")
		}
		sets = append(sets, "name = ?")
		args = append(args, name)
	}
	if req.Type != nil && id != "personal" {
		sets = append(sets, "type = ?")
		args = append(args, normalizeProjectType(*req.Type))
	}
	if req.Description != nil {
		sets = append(sets, "description = ?")
		args = append(args, strings.TrimSpace(*req.Description))
	}
	args = append(args, id, workspaceID)
	if _, err := r.db.ExecContext(ctx, fmt.Sprintf("UPDATE task_projects SET %s WHERE id = ? AND workspace_id = ?", strings.Join(sets, ", ")), args...); err != nil {
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
		if _, err := scanSQLiteTaskProject(tx.QueryRowContext(ctx, `
			SELECT id, name, type, description, created_at, updated_at
			FROM task_projects WHERE workspace_id = ? AND id = ?
		`, workspaceID, id)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE tasks
			SET project_id = 'personal',
				project = COALESCE((SELECT name FROM task_projects WHERE workspace_id = ? AND id = 'personal'), 'Personal'),
				updated_at = ?
			WHERE workspace_id = ? AND project_id = ?
		`, workspaceID, nowUnix(), workspaceID, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE events
			SET project_id = NULL
			WHERE workspace_id = ? AND project_id = ?
		`, workspaceID, id); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM task_projects WHERE workspace_id = ? AND id = ?`, workspaceID, id)
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
	return scanSQLiteTaskProject(r.db.QueryRowContext(ctx, `
		SELECT id, name, type, description, created_at, updated_at
		FROM task_projects WHERE workspace_id = ? AND id = ?
	`, workspaceID, id))
}

func (r taskRepository) GetProjectByName(ctx context.Context, name string) (*model.TaskProject, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return scanSQLiteTaskProject(r.db.QueryRowContext(ctx, `
		SELECT id, name, type, description, created_at, updated_at
		FROM task_projects WHERE workspace_id = ? AND name = ?
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
	clientID := deterministicSQLiteMobileEntityClientID("task", workspaceID, task.ID)
	return r.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO tasks (
				id, client_id, revision, title, content, project, project_id, due, planned_date, priority, done, status, horizon, scope, sort_order, note_id, roadmap_node_id, execution_type, created_at, updated_at, workspace_id
			)
			VALUES (?, ?, 1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, task.ID, clientID, task.Title, task.Content, task.Project, task.ProjectID, task.Due, task.PlannedDate, task.Priority, task.Done, task.Status, task.Horizon, task.Scope, task.SortOrder, task.NoteID, task.RoadmapNodeID, task.ExecutionType, task.CreatedAt, task.UpdatedAt, workspaceID); err != nil {
			return err
		}
		return persistSQLiteServerEntityChange(ctx, tx, workspaceID, uuid.NewString(), "task", "task.server_created", clientID, task.UpdatedAt)
	})
}

func (r taskRepository) Update(ctx context.Context, id string, req *model.UpdateTaskRequest) (*model.Task, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	var task *model.Task
	err = r.withTx(ctx, func(tx *sql.Tx) error {
		// TOCTOU: read current state inside transaction
		var currentDone int
		if err := tx.QueryRowContext(ctx, `SELECT done FROM tasks WHERE workspace_id = ? AND id = ? AND deleted_at IS NULL`, workspaceID, id).Scan(&currentDone); err != nil {
			if err == sql.ErrNoRows {
				return err
			}
			return err
		}

		sets := []string{"updated_at = ?", "revision = revision + 1"}
		args := []interface{}{nowUnix()}
		if req.Title != nil {
			sets = append(sets, "title = ?")
			args = append(args, strings.TrimSpace(*req.Title))
		}
		if req.Content != nil {
			sets = append(sets, "content = ?")
			args = append(args, strings.TrimSpace(*req.Content))
		}
		if req.ProjectID != nil {
			project, err := r.getProjectByIDInTx(ctx, tx, workspaceID, *req.ProjectID)
			if err != nil {
				return err
			}
			sets = append(sets, "project_id = ?", "project = ?")
			args = append(args, project.ID, project.Name)
		} else if req.Project != nil {
			projectID, err := r.ensureTaskProjectByNameInTx(ctx, tx, workspaceID, *req.Project, "regular")
			if err != nil {
				return err
			}
			name := strings.TrimSpace(*req.Project)
			sets = append(sets, "project_id = ?", "project = ?")
			args = append(args, projectID, name)
		}
		if req.Due != nil {
			sets = append(sets, "due = ?")
			args = append(args, *req.Due)
		}
		if req.PlannedDate != nil {
			val := strings.TrimSpace(*req.PlannedDate)
			if val == "" {
				sets = append(sets, "planned_date = NULL")
			} else {
				sets = append(sets, "planned_date = ?")
				args = append(args, val)
			}
		}
		if req.Priority != nil {
			sets = append(sets, "priority = ?")
			args = append(args, *req.Priority)
		}
		if req.ExecutionType != nil {
			sets = append(sets, "execution_type = ?")
			args = append(args, *req.ExecutionType)
		}
		// Branch A: req.Done directly set
		if req.Done != nil && req.Status == nil {
			sets = append(sets, "done = ?")
			args = append(args, *req.Done)
			status := "open"
			if *req.Done == 1 {
				status = "done"
			}
			sets = append(sets, "status = ?")
			args = append(args, status)
			// TOCTOU: completed_at set/clear
			if *req.Done == 1 && currentDone == 0 {
				sets = append(sets, "completed_at = ?")
				args = append(args, nowUnix())
			} else if *req.Done == 0 && currentDone == 1 {
				sets = append(sets, "completed_at = NULL")
			}
		}
		// Branch B: req.Status indirectly changes done
		if req.Status != nil {
			newStatus := strings.ToLower(normalizeTaskStatus(*req.Status))
			isCurrentlyDone := (currentDone == 1)
			isBecomingDone := (newStatus == "done" && !isCurrentlyDone)
			isBecomingUndone := (newStatus != "done" && isCurrentlyDone)
			if isBecomingDone {
				sets = append(sets, "completed_at = ?")
				args = append(args, nowUnix())
			} else if isBecomingUndone {
				sets = append(sets, "completed_at = NULL")
			}
			status := normalizeTaskStatus(*req.Status)
			done := 0
			if status == "done" {
				done = 1
			}
			sets = append(sets, "status = ?", "done = ?")
			args = append(args, status, done)
		}
		if req.Scope != nil {
			sets = append(sets, "scope = ?")
			args = append(args, normalizeScope(*req.Scope))
		}
		if req.Horizon != nil {
			sets = append(sets, "horizon = ?")
			args = append(args, normalizeHorizon(*req.Horizon))
		}
		if req.SortOrder != nil {
			sets = append(sets, "sort_order = ?")
			args = append(args, *req.SortOrder)
		}
		if req.RoadmapNodeID != nil {
			sets = append(sets, "roadmap_node_id = ?")
			args = append(args, *req.RoadmapNodeID)
		}
		args = append(args, id, workspaceID)
		result, err := tx.ExecContext(ctx, fmt.Sprintf("UPDATE tasks SET %s WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL", strings.Join(sets, ", ")), args...)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err == nil && affected == 0 {
			return sql.ErrNoRows
		}
		// Read back inside transaction
		t, err := scanSQLiteTaskRow(tx.QueryRowContext(ctx, sqliteTaskSelectSQL()+` WHERE t.workspace_id = ? AND t.id = ? AND t.deleted_at IS NULL`, workspaceID, id))
		if err != nil {
			return err
		}
		task = t
		var clientID string
		if err := tx.QueryRowContext(ctx, `SELECT client_id FROM tasks WHERE workspace_id = ? AND id = ?`, workspaceID, id).Scan(&clientID); err != nil {
			return err
		}
		return persistSQLiteServerEntityChange(ctx, tx, workspaceID, uuid.NewString(), "task", "task.server_updated", clientID, nowUnix())
	})
	if err != nil {
		return nil, err
	}
	if err := r.syncRoadmapNodeStatus(ctx, workspaceID, task); err != nil {
		return nil, err
	}
	return task, nil
}

func (r taskRepository) GetByID(ctx context.Context, id string) (*model.Task, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return scanSQLiteTaskRow(r.db.QueryRowContext(ctx, sqliteTaskSelectSQL()+` WHERE t.workspace_id = ? AND t.id = ? AND t.deleted_at IS NULL`, workspaceID, id))
}

func (r taskRepository) Delete(ctx context.Context, id string) error {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	now := nowUnix()
	return r.withTx(ctx, func(tx *sql.Tx) error {
		var clientID string
		err := tx.QueryRowContext(ctx, `SELECT client_id FROM tasks WHERE workspace_id = ? AND id = ? AND deleted_at IS NULL`, workspaceID, id).Scan(&clientID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := tombstoneSQLiteTaskOccurrences(ctx, tx, workspaceID, id, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM task_recurrence_rules WHERE workspace_id = ? AND task_id = ?`, workspaceID, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE tasks SET deleted_at = ?, updated_at = ?, revision = revision + 1 WHERE workspace_id = ? AND id = ? AND deleted_at IS NULL`, now, now, workspaceID, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO mobile_retired_ids (workspace_id, entity_type, client_id, retired_at) VALUES (?, 'task', ?, ?)`, workspaceID, clientID, now); err != nil {
			return err
		}
		return persistSQLiteServerEntityChange(ctx, tx, workspaceID, uuid.NewString(), "task", "task.server_deleted", clientID, now)
	})
}

func (r taskRepository) Today(ctx context.Context, todayStart, todayEnd, overdueCutoff int64) ([]model.Task, []model.Task, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	todayDate := time.Unix(todayStart, 0).In(time.Local).Format("2006-01-02")
	overdueCutoffDate := time.Unix(overdueCutoff, 0).In(time.Local).Format("2006-01-02")
	rows, err := r.db.QueryContext(ctx, sqliteTaskSelectSQL()+`
		WHERE t.workspace_id = ? AND t.deleted_at IS NULL AND t.done = 0 AND (t.execution_type IS NULL OR t.execution_type = '' OR t.execution_type = 'single') AND (
			(t.due >= ? AND t.due < ?)
			OR (COALESCE(t.horizon, CASE WHEN t.scope IN ('monthly', 'yearly') THEN 'long' ELSE 'week' END) <> 'long' AND t.planned_date = ?)
			OR (COALESCE(t.horizon, CASE WHEN t.scope IN ('monthly', 'yearly') THEN 'long' ELSE 'week' END) = 'long'
				AND COALESCE(t.status, CASE WHEN t.done = 1 THEN 'done' ELSE 'open' END) = 'active')
		)
		ORDER BY t.sort_order ASC, t.created_at DESC
	`, workspaceID, todayStart, todayEnd, todayDate)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	todayTasks, err := scanSQLiteTaskRows(rows)
	if err != nil {
		return nil, nil, err
	}

	rows, err = r.db.QueryContext(ctx, sqliteTaskSelectSQL()+`
		WHERE t.workspace_id = ?
			AND t.deleted_at IS NULL
			AND t.done = 0
			AND (t.execution_type IS NULL OR t.execution_type = '' OR t.execution_type = 'single')
			AND COALESCE(t.horizon, CASE WHEN t.scope IN ('monthly', 'yearly') THEN 'long' ELSE 'week' END) <> 'long'
			AND ((t.due < ? AND t.due >= ?) OR (t.due IS NULL AND t.planned_date < ? AND t.planned_date >= ?))
			AND (t.planned_date IS NULL OR t.planned_date <> ?)
		ORDER BY t.due ASC LIMIT 10
	`, workspaceID, todayStart, overdueCutoff, todayDate, overdueCutoffDate, todayDate)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	overdueTasks, err := scanSQLiteTaskRows(rows)
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
		`SELECT COUNT(*) FROM tasks WHERE workspace_id = ? AND deleted_at IS NULL AND completed_at >= ? AND completed_at < ?`,
		workspaceID, from, to,
	).Scan(&total); err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	rows, err := r.db.QueryContext(ctx,
		`SELECT t.id, t.title, t.done, t.planned_date, t.due, t.completed_at, t.note_id,
		        p.id, p.name, p.type
		 FROM tasks t LEFT JOIN task_projects p ON p.workspace_id = t.workspace_id AND t.project_id = p.id
		 WHERE t.workspace_id = ? AND t.deleted_at IS NULL AND t.completed_at >= ? AND t.completed_at < ?
		 ORDER BY t.completed_at DESC LIMIT ? OFFSET ?`,
		workspaceID, from, to, pageSize, offset,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var summaries []model.TaskSummary
	for rows.Next() {
		var s model.TaskSummary
		var projectID, projectName, projectType sql.NullString
		if err := rows.Scan(&s.ID, &s.Title, &s.Done, &s.PlannedDate, &s.Due, &s.CompletedAt,
			&s.NoteID, &projectID, &projectName, &projectType); err != nil {
			return nil, 0, err
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
		`SELECT COUNT(DISTINCT DATE(completed_at, 'unixepoch')),
		        COUNT(DISTINCT project_id)
		 FROM tasks WHERE workspace_id = ? AND deleted_at IS NULL AND completed_at >= ? AND completed_at < ?`,
		workspaceID, from, to,
	).Scan(&activeDays, &projectCount)
	return activeDays, projectCount, err
}

func sqliteTaskSelectSQL() string {
	return `
		SELECT
			t.id, t.title, COALESCE(t.content, ''), COALESCE(p.name, t.project),
			t.project_id, p.type, t.due, t.planned_date, t.priority, t.done,
			COALESCE(t.status, CASE WHEN t.done = 1 THEN 'done' ELSE 'open' END),
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
	return scanSQLiteTaskProject(r.db.QueryRowContext(ctx, `SELECT id, name, type, description, created_at, updated_at FROM task_projects WHERE workspace_id = ? AND id = ?`, workspaceID, id))
}

func (r taskRepository) getProjectByName(ctx context.Context, workspaceID string, name string) (*model.TaskProject, error) {
	return scanSQLiteTaskProject(r.db.QueryRowContext(ctx, `SELECT id, name, type, description, created_at, updated_at FROM task_projects WHERE workspace_id = ? AND name = ?`, workspaceID, strings.TrimSpace(name)))
}

func (r taskRepository) getProjectByIDInTx(ctx context.Context, tx *sql.Tx, workspaceID string, id string) (*model.TaskProject, error) {
	return scanSQLiteTaskProject(tx.QueryRowContext(ctx, `SELECT id, name, type, description, created_at, updated_at FROM task_projects WHERE workspace_id = ? AND id = ?`, workspaceID, id))
}

func (r taskRepository) ensureTaskProjectByNameInTx(ctx context.Context, tx *sql.Tx, workspaceID string, name string, typ string) (string, error) {
	name = strings.TrimSpace(name)
	var id string
	err := tx.QueryRowContext(ctx, `SELECT id FROM task_projects WHERE workspace_id = ? AND name = ? AND type = ?`, workspaceID, name, typ).Scan(&id)
	if err == sql.ErrNoRows {
		id = uuid.New().String()
		now := nowUnix()
		_, err = tx.ExecContext(ctx,
			`INSERT INTO task_projects (id, name, type, description, created_at, updated_at, workspace_id) VALUES (?, ?, ?, '', ?, ?, ?)`,
			id, name, typ, now, now, workspaceID)
		if err != nil {
			return "", err
		}
		return id, nil
	}
	return id, err
}

func (r taskRepository) syncRoadmapNodeStatus(ctx context.Context, workspaceID string, task *model.Task) error {
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
	_, err := r.db.ExecContext(ctx, `
		UPDATE roadmap_nodes
		SET status = ?, updated_at = ?
		WHERE workspace_id = ? AND id = ?
	`, status, nowUnix(), workspaceID, *task.RoadmapNodeID)
	return err
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

type sqliteRowScanner interface {
	Scan(dest ...interface{}) error
}

func scanSQLiteTaskProject(row sqliteRowScanner) (*model.TaskProject, error) {
	var project model.TaskProject
	if err := row.Scan(&project.ID, &project.Name, &project.Type, &project.Description, &project.CreatedAt, &project.UpdatedAt); err != nil {
		return nil, err
	}
	return &project, nil
}

func scanSQLiteTaskProjects(rows *sql.Rows) ([]model.TaskProject, error) {
	projects := make([]model.TaskProject, 0)
	for rows.Next() {
		project, err := scanSQLiteTaskProject(rows)
		if err != nil {
			return nil, err
		}
		projects = append(projects, *project)
	}
	return projects, rows.Err()
}

func scanSQLiteTaskRow(row sqliteRowScanner) (*model.Task, error) {
	var task model.Task
	if err := row.Scan(
		&task.ID, &task.Title, &task.Content, &task.Project, &task.ProjectID, &task.ProjectType,
		&task.Due, &task.PlannedDate, &task.Priority, &task.Done, &task.Status, &task.Horizon,
		&task.Scope, &task.SortOrder, &task.NoteID, &task.RoadmapNodeID, &task.CreatedAt, &task.UpdatedAt,
		&task.CompletedAt, &task.ExecutionType,
	); err != nil {
		return nil, err
	}
	return &task, nil
}

func scanSQLiteTaskRows(rows *sql.Rows) ([]model.Task, error) {
	tasks := make([]model.Task, 0)
	for rows.Next() {
		task, err := scanSQLiteTaskRow(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, *task)
	}
	return tasks, rows.Err()
}
