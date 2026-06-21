package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

type taskRepository struct {
	db postgresRunner
}

func (r taskRepository) List(ctx context.Context, filter storage.TaskFilter) ([]model.Task, int, error) {
	where, args := postgresTaskWhere(filter)
	whereClause := strings.Join(where, " AND ")

	var total int
	if err := r.db.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT COUNT(*)
		FROM tasks t
		LEFT JOIN task_projects p ON p.id = t.project_id
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

func postgresTaskWhere(filter storage.TaskFilter) ([]string, []interface{}) {
	where := []string{"1=1"}
	args := []interface{}{}
	next := 1
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
			where = append(where, fmt.Sprintf("(t.execution_type IS NULL OR t.execution_type = %s)", pgPlaceholder(next)))
			args = append(args, "single")
		}
	}
	return where, args
}

func (r taskRepository) ListProjects(ctx context.Context) ([]model.TaskProject, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, name, type, description, created_at, updated_at
		FROM task_projects
		ORDER BY CASE WHEN id = 'personal' THEN 0 ELSE 1 END, updated_at DESC, lower(name) ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPostgresTaskProjects(rows)
}

func (r taskRepository) CreateProject(ctx context.Context, req *model.CreateTaskProjectRequest) (*model.TaskProject, error) {
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
	if _, err := r.db.ExecContext(ctx, `
		INSERT INTO task_projects (id, name, type, description, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $5)
		ON CONFLICT (name) DO UPDATE SET
			type = excluded.type,
			description = excluded.description,
			updated_at = excluded.updated_at
	`, id, name, projectType, strings.TrimSpace(req.Description), now); err != nil {
		return nil, err
	}
	return r.GetProjectByName(ctx, name)
}

func (r taskRepository) UpdateProject(ctx context.Context, id string, req *model.UpdateTaskProjectRequest) (*model.TaskProject, error) {
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
	args = append(args, id)
	if _, err := r.db.ExecContext(ctx, fmt.Sprintf("UPDATE task_projects SET %s WHERE id = %s", clause, pgPlaceholder(len(args))), args...); err != nil {
		return nil, err
	}
	return r.GetProjectByID(ctx, id)
}

func (r taskRepository) DeleteProject(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("project id is required")
	}
	if id == "personal" {
		return fmt.Errorf("personal project cannot be deleted")
	}
	if _, err := r.GetProjectByID(ctx, id); err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, `DELETE FROM task_projects WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (r taskRepository) GetProjectByID(ctx context.Context, id string) (*model.TaskProject, error) {
	return scanPostgresTaskProject(r.db.QueryRowContext(ctx, `
		SELECT id, name, type, description, created_at, updated_at
		FROM task_projects WHERE id = $1
	`, id))
}

func (r taskRepository) GetProjectByName(ctx context.Context, name string) (*model.TaskProject, error) {
	return scanPostgresTaskProject(r.db.QueryRowContext(ctx, `
		SELECT id, name, type, description, created_at, updated_at
		FROM task_projects WHERE name = $1
	`, strings.TrimSpace(name)))
}

func (r taskRepository) Create(ctx context.Context, task *model.Task) error {
	task.ID = newID()
	now := nowUnix()
	task.CreatedAt = now
	task.UpdatedAt = now
	if err := r.normalizeTaskDefaults(ctx, task); err != nil {
		return err
	}
	return r.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO tasks (
				id, title, content, project, project_id, due_at, planned_date, priority, done, status, horizon, scope, sort_order, note_id, roadmap_node_id, execution_type, created_at, updated_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7::date, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
		`, task.ID, task.Title, task.Content, task.Project, task.ProjectID, postgresTimePtr(task.Due), task.PlannedDate, task.Priority, task.Done == 1, task.Status, task.Horizon, task.Scope, task.SortOrder, task.NoteID, task.RoadmapNodeID, task.ExecutionType, unixToTime(task.CreatedAt), unixToTime(task.UpdatedAt)); err != nil {
			return err
		}
		return upsertTaskSearchIndex(ctx, tx, task)
	})
}

func (r taskRepository) Update(ctx context.Context, id string, req *model.UpdateTaskRequest) (*model.Task, error) {
	var updated *model.Task
	err := r.withTx(ctx, func(tx *sql.Tx) error {
		// TOCTOU: read current done state inside transaction
		var currentDone bool
		if err := tx.QueryRowContext(ctx, `SELECT done FROM tasks WHERE id = $1`, id).Scan(&currentDone); err != nil {
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
			project, err := r.GetProjectByID(ctx, *req.ProjectID)
			if err != nil {
				return err
			}
			builder.Add("project_id", project.ID)
			builder.Add("project", project.Name)
		} else if req.Project != nil {
			projectID, err := r.ensureTaskProjectByName(ctx, *req.Project, "regular")
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
		args = append(args, id)

		result, err := tx.ExecContext(ctx, fmt.Sprintf("UPDATE tasks SET %s WHERE id = %s", clause, pgPlaceholder(len(args))), args...)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err == nil && affected == 0 {
			return sql.ErrNoRows
		}
		task, err := scanPostgresTaskRow(tx.QueryRowContext(ctx, postgresTaskSelectSQL()+` WHERE t.id = $1`, id))
		if err != nil {
			return err
		}
		if err := upsertTaskSearchIndex(ctx, tx, task); err != nil {
			return err
		}
		if err := r.syncRoadmapNodeStatus(ctx, tx, task); err != nil {
			return err
		}
		updated = task
		return nil
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

func (r taskRepository) GetByID(ctx context.Context, id string) (*model.Task, error) {
	return scanPostgresTaskRow(r.db.QueryRowContext(ctx, postgresTaskSelectSQL()+` WHERE t.id = $1`, id))
}

func (r taskRepository) Delete(ctx context.Context, id string) error {
	return r.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM tasks WHERE id = $1`, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM search_index WHERE entity_type = 'task' AND entity_id = $1`, id)
		return err
	})
}

func (r taskRepository) Today(ctx context.Context, todayStart, todayEnd, overdueCutoff int64) ([]model.Task, []model.Task, error) {
	todayDate := time.Unix(todayStart, 0).In(time.Local).Format("2006-01-02")
	overdueCutoffDate := time.Unix(overdueCutoff, 0).In(time.Local).Format("2006-01-02")
	rows, err := r.db.QueryContext(ctx, postgresTaskSelectSQL()+`
		WHERE t.done = false AND (t.execution_type IS NULL OR t.execution_type = 'single') AND (
			(t.due_at >= $1 AND t.due_at < $2)
			OR (COALESCE(t.horizon, CASE WHEN t.scope IN ('monthly', 'yearly') THEN 'long' ELSE 'week' END) <> 'long' AND t.planned_date = $3::date)
			OR (COALESCE(t.horizon, CASE WHEN t.scope IN ('monthly', 'yearly') THEN 'long' ELSE 'week' END) = 'long'
				AND COALESCE(t.status, CASE WHEN t.done THEN 'done' ELSE 'open' END) = 'active')
		)
		ORDER BY t.sort_order ASC, t.created_at DESC
	`, unixToTime(todayStart), unixToTime(todayEnd), todayDate)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	todayTasks, err := scanPostgresTaskRows(rows)
	if err != nil {
		return nil, nil, err
	}

	rows, err = r.db.QueryContext(ctx, postgresTaskSelectSQL()+`
		WHERE t.done = false
			AND (t.execution_type IS NULL OR t.execution_type = 'single')
			AND COALESCE(t.horizon, CASE WHEN t.scope IN ('monthly', 'yearly') THEN 'long' ELSE 'week' END) <> 'long'
			AND ((t.due_at < $1 AND t.due_at >= $2) OR (t.due_at IS NULL AND t.planned_date < $3::date AND t.planned_date >= $4::date))
			AND (t.planned_date IS NULL OR t.planned_date <> $3::date)
		ORDER BY t.due_at ASC LIMIT 10
	`, unixToTime(todayStart), unixToTime(overdueCutoff), todayDate, overdueCutoffDate)
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
	var total int
	if err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM tasks WHERE completed_at >= $1 AND completed_at < $2`,
		unixToTime(from), unixToTime(to),
	).Scan(&total); err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	rows, err := r.db.QueryContext(ctx,
		`SELECT t.id, t.title, t.done, t.planned_date, t.due_at, t.completed_at, t.note_id,
		        p.id, p.name, p.type
		 FROM tasks t LEFT JOIN task_projects p ON t.project_id = p.id
		 WHERE t.completed_at >= $1 AND t.completed_at < $2
		 ORDER BY t.completed_at DESC LIMIT $3 OFFSET $4`,
		unixToTime(from), unixToTime(to), pageSize, offset,
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
	var activeDays, projectCount int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT DATE(completed_at)),
		        COUNT(DISTINCT project_id)
		 FROM tasks WHERE completed_at >= $1 AND completed_at < $2`,
		unixToTime(from), unixToTime(to),
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
		LEFT JOIN task_projects p ON p.id = t.project_id
	`
}

func (r taskRepository) normalizeTaskDefaults(ctx context.Context, task *model.Task) error {
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
			projectID, err := r.ensureTaskProjectByName(ctx, *task.Project, "regular")
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
	if project, err := r.GetProjectByID(ctx, *task.ProjectID); err == nil {
		name := project.Name
		task.Project = &name
		task.ProjectType = &project.Type
	}
	return nil
}

func (r taskRepository) ensureTaskProjectByName(ctx context.Context, name, projectType string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" || strings.EqualFold(trimmed, "personal") || trimmed == "个人" {
		return "personal", nil
	}
	project, err := r.GetProjectByName(ctx, trimmed)
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

func (r taskRepository) syncRoadmapNodeStatus(ctx context.Context, tx *sql.Tx, task *model.Task) error {
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
		WHERE id = $3
	`, status, time.Now().UTC(), *task.RoadmapNodeID)
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

func upsertTaskSearchIndex(ctx context.Context, tx *sql.Tx, task *model.Task) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO search_index (entity_type, entity_id, title, content, tags, updated_at, search_vector)
		VALUES (
			'task',
			$1,
			$2,
			$3,
			'{}'::text[],
			$4,
			to_tsvector('simple', coalesce($2, '') || ' ' || coalesce($3, ''))
		)
		ON CONFLICT (entity_type, entity_id) DO UPDATE SET
			title = excluded.title,
			content = excluded.content,
			updated_at = excluded.updated_at,
			search_vector = excluded.search_vector
	`, task.ID, task.Title, task.Content, unixToTime(task.UpdatedAt))
	return err
}
