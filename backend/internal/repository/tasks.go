package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

func GetTasks(project, status, scope, horizon, projectID, plannedDate string, page, pageSize int) ([]model.Task, int, error) {
	if store := CurrentStore(); store != nil {
		return store.Tasks().List(context.Background(), storage.TaskFilter{
			Project:     project,
			Status:      status,
			Scope:       scope,
			Horizon:     horizon,
			ProjectID:   projectID,
			PlannedDate: plannedDate,
			Page:        page,
			PageSize:    pageSize,
		})
	}

	where := []string{"1=1"}
	args := []interface{}{}

	if project != "" {
		where = append(where, "(COALESCE(p.name, t.project, '') = ? OR t.project = ?)")
		args = append(args, project, project)
	}
	if projectID != "" {
		where = append(where, "t.project_id = ?")
		args = append(args, projectID)
	}
	if status == "active" || status == "open" {
		where = append(where, "COALESCE(t.status, CASE WHEN t.done = 1 THEN 'done' ELSE 'open' END) <> 'done'")
	} else if status == "done" {
		where = append(where, "(t.done = 1 OR t.status = 'done')")
	}
	if scope != "" {
		where = append(where, "t.scope = ?")
		args = append(args, scope)
	}
	if horizon != "" {
		where = append(where, "t.horizon = ?")
		args = append(args, horizon)
	}
	if plannedDate != "" {
		where = append(where, "t.planned_date = ?")
		args = append(args, plannedDate)
	}

	whereClause := strings.Join(where, " AND ")

	var total int
	if err := DB.QueryRow(fmt.Sprintf(`
		SELECT COUNT(*)
		FROM tasks t
		LEFT JOIN task_projects p ON p.id = t.project_id
		WHERE %s
	`, whereClause), args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	query := fmt.Sprintf(`
		SELECT
			t.id,
			t.title,
			COALESCE(t.content, ''),
			COALESCE(p.name, t.project),
			t.project_id,
			p.type,
			t.due,
			t.planned_date,
			t.priority,
			t.done,
			COALESCE(t.status, CASE WHEN t.done = 1 THEN 'done' ELSE 'open' END),
			COALESCE(t.horizon, CASE WHEN t.scope IN ('monthly', 'yearly') THEN 'long' ELSE 'week' END),
			t.scope,
			t.sort_order,
			t.note_id,
			t.roadmap_node_id,
			t.created_at,
			t.updated_at,
			t.completed_at
		FROM tasks t
		LEFT JOIN task_projects p ON p.id = t.project_id
		WHERE %s
		ORDER BY
			CASE WHEN t.planned_date IS NULL THEN 1 ELSE 0 END ASC,
			t.planned_date DESC,
			t.sort_order ASC,
			t.created_at DESC
		LIMIT ? OFFSET ?
	`, whereClause)
	args = append(args, pageSize, offset)

	rows, err := DB.Query(query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	tasks, err := scanTaskRows(rows)
	if err != nil {
		return nil, 0, err
	}
	return tasks, total, nil
}

func GetTasksByRoadmapNodeID(nodeID string) ([]model.Task, error) {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return []model.Task{}, nil
	}
	if store := CurrentStore(); store != nil {
		tasks, _, err := store.Tasks().List(context.Background(), storage.TaskFilter{
			RoadmapNodeID: nodeID,
			Page:          1,
			PageSize:      100,
		})
		return tasks, err
	}

	rows, err := DB.Query(`
		SELECT
			t.id,
			t.title,
			COALESCE(t.content, ''),
			COALESCE(p.name, t.project),
			t.project_id,
			p.type,
			t.due,
			t.planned_date,
			t.priority,
			t.done,
			COALESCE(t.status, CASE WHEN t.done = 1 THEN 'done' ELSE 'open' END),
			COALESCE(t.horizon, CASE WHEN t.scope IN ('monthly', 'yearly') THEN 'long' ELSE 'week' END),
			t.scope,
			t.sort_order,
			t.note_id,
			t.roadmap_node_id,
			t.created_at,
			t.updated_at,
			t.completed_at
		FROM tasks t
		LEFT JOIN task_projects p ON p.id = t.project_id
		WHERE t.roadmap_node_id = ?
		ORDER BY
			CASE WHEN t.planned_date IS NULL THEN 1 ELSE 0 END ASC,
			t.planned_date DESC,
			t.sort_order ASC,
			t.created_at DESC
		LIMIT 100
	`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTaskRows(rows)
}

func ListTaskProjects() ([]model.TaskProject, error) {
	if store := CurrentStore(); store != nil {
		return store.Tasks().ListProjects(context.Background())
	}

	rows, err := DB.Query(`
		SELECT id, name, type, description, created_at, updated_at
		FROM task_projects
		ORDER BY CASE WHEN id = 'personal' THEN 0 ELSE 1 END, updated_at DESC, name COLLATE NOCASE ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	projects := make([]model.TaskProject, 0)
	for rows.Next() {
		var project model.TaskProject
		if err := rows.Scan(&project.ID, &project.Name, &project.Type, &project.Description, &project.CreatedAt, &project.UpdatedAt); err != nil {
			return nil, err
		}
		projects = append(projects, project)
	}
	return projects, rows.Err()
}

func GetTaskProjects() ([]string, error) {
	projects, err := ListTaskProjects()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(projects))
	for _, project := range projects {
		names = append(names, project.Name)
	}
	return names, nil
}

func CreateTaskProject(req *model.CreateTaskProjectRequest) (*model.TaskProject, error) {
	if store := CurrentStore(); store != nil {
		return store.Tasks().CreateProject(context.Background(), req)
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, fmt.Errorf("project name is required")
	}
	projectType := normalizeProjectType(req.Type)
	if name == "个人" {
		projectType = "personal"
	}
	id := "project-" + newUUID()
	if projectType == "personal" {
		id = "personal"
	}
	now := nowUnix()
	_, err := DB.Exec(`
		INSERT INTO task_projects (id, name, type, description, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			type = excluded.type,
			description = excluded.description,
			updated_at = excluded.updated_at
	`, id, name, projectType, strings.TrimSpace(req.Description), now, now)
	if err != nil {
		return nil, err
	}
	return GetTaskProjectByName(name)
}

func UpdateTaskProject(id string, req *model.UpdateTaskProjectRequest) (*model.TaskProject, error) {
	if store := CurrentStore(); store != nil {
		return store.Tasks().UpdateProject(context.Background(), id, req)
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

	args = append(args, id)
	if _, err := DB.Exec(fmt.Sprintf("UPDATE task_projects SET %s WHERE id = ?", strings.Join(sets, ", ")), args...); err != nil {
		return nil, err
	}
	return GetTaskProjectByID(id)
}

func DeleteTaskProject(id string) error {
	if store := CurrentStore(); store != nil {
		return store.Tasks().DeleteProject(context.Background(), id)
	}

	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("project id is required")
	}
	if id == "personal" {
		return fmt.Errorf("personal project cannot be deleted")
	}
	if _, err := GetTaskProjectByID(id); err != nil {
		return err
	}
	result, err := DB.Exec(`DELETE FROM task_projects WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func GetTaskProjectByID(id string) (*model.TaskProject, error) {
	if store := CurrentStore(); store != nil {
		return store.Tasks().GetProjectByID(context.Background(), id)
	}

	var project model.TaskProject
	err := DB.QueryRow(`
		SELECT id, name, type, description, created_at, updated_at
		FROM task_projects
		WHERE id = ?
	`, id).Scan(&project.ID, &project.Name, &project.Type, &project.Description, &project.CreatedAt, &project.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &project, nil
}

func GetTaskProjectByName(name string) (*model.TaskProject, error) {
	if store := CurrentStore(); store != nil {
		return store.Tasks().GetProjectByName(context.Background(), name)
	}

	var project model.TaskProject
	err := DB.QueryRow(`
		SELECT id, name, type, description, created_at, updated_at
		FROM task_projects
		WHERE name = ?
	`, strings.TrimSpace(name)).Scan(&project.ID, &project.Name, &project.Type, &project.Description, &project.CreatedAt, &project.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &project, nil
}

func CreateTask(t *model.Task) error {
	if store := CurrentStore(); store != nil {
		return store.Tasks().Create(context.Background(), t)
	}

	t.ID = newUUID()
	now := nowUnix()
	t.CreatedAt = now
	t.UpdatedAt = now
	normalizeTaskDefaults(t)

	_, err := DB.Exec(`
		INSERT INTO tasks (
			id, title, content, project, project_id, due, planned_date, priority, done, status, horizon, scope, sort_order, note_id, roadmap_node_id, created_at, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, t.ID, t.Title, t.Content, t.Project, t.ProjectID, t.Due, t.PlannedDate, t.Priority, t.Done, t.Status, t.Horizon, t.Scope, t.SortOrder, t.NoteID, t.RoadmapNodeID, t.CreatedAt, t.UpdatedAt)
	return err
}

func UpdateTask(id string, req *model.UpdateTaskRequest) (*model.Task, error) {
	if store := CurrentStore(); store != nil {
		return store.Tasks().Update(context.Background(), id, req)
	}

	tx, err := DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var currentDone int
	if err := tx.QueryRow(`SELECT done FROM tasks WHERE id = ?`, id).Scan(&currentDone); err != nil {
		if err == sql.ErrNoRows {
			return nil, err
		}
		return nil, err
	}

	sets := []string{"updated_at = ?"}
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
		project, err := GetTaskProjectByID(*req.ProjectID)
		if err != nil {
			return nil, err
		}
		sets = append(sets, "project_id = ?", "project = ?")
		args = append(args, project.ID, project.Name)
	} else if req.Project != nil {
		projectID, err := ensureTaskProjectByName(*req.Project, "regular")
		if err != nil {
			return nil, err
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
		sets = append(sets, "planned_date = ?")
		args = append(args, strings.TrimSpace(*req.PlannedDate))
	}
	if req.Priority != nil {
		sets = append(sets, "priority = ?")
		args = append(args, *req.Priority)
	}
	// Branch A: req.Done directly set
	if req.Done != nil && req.Status == nil {
		status := "open"
		if *req.Done == 1 {
			status = "done"
		}
		sets = append(sets, "done = ?", "status = ?")
		args = append(args, *req.Done, status)
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

	args = append(args, id)
	result, err := tx.Exec(fmt.Sprintf("UPDATE tasks SET %s WHERE id = ?", strings.Join(sets, ", ")), args...)
	if err != nil {
		return nil, err
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return nil, sql.ErrNoRows
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	task, err := GetTaskByID(id)
	if err != nil {
		return nil, err
	}
	if task.RoadmapNodeID != nil {
		if task.Done == 1 || task.Status == "done" {
			_ = UpdateRoadmapNodeStatus(*task.RoadmapNodeID, "done")
		} else if task.Status == "open" {
			_ = UpdateRoadmapNodeStatus(*task.RoadmapNodeID, "active")
		}
	}
	return task, nil
}

func GetTaskByID(id string) (*model.Task, error) {
	if store := CurrentStore(); store != nil {
		return store.Tasks().GetByID(context.Background(), id)
	}

	row := DB.QueryRow(`
		SELECT
			t.id,
			t.title,
			COALESCE(t.content, ''),
			COALESCE(p.name, t.project),
			t.project_id,
			p.type,
			t.due,
			t.planned_date,
			t.priority,
			t.done,
			COALESCE(t.status, CASE WHEN t.done = 1 THEN 'done' ELSE 'open' END),
			COALESCE(t.horizon, CASE WHEN t.scope IN ('monthly', 'yearly') THEN 'long' ELSE 'week' END),
			t.scope,
			t.sort_order,
			t.note_id,
			t.roadmap_node_id,
			t.created_at,
			t.updated_at,
			t.completed_at
		FROM tasks t
		LEFT JOIN task_projects p ON p.id = t.project_id
		WHERE t.id = ?
	`, id)
	task, err := scanTaskRow(row)
	if err != nil {
		return nil, err
	}
	return task, nil
}

func DeleteTask(id string) error {
	if store := CurrentStore(); store != nil {
		return store.Tasks().Delete(context.Background(), id)
	}

	_, err := DB.Exec("DELETE FROM tasks WHERE id = ?", id)
	return err
}

func GetCompletedTasksByRange(from, to int64, page, pageSize int) ([]model.TaskSummary, int, error) {
	if store := CurrentStore(); store != nil {
		return store.Tasks().GetCompletedTasksByRange(context.Background(), from, to, page, pageSize)
	}

	var total int
	if err := DB.QueryRow(
		`SELECT COUNT(*) FROM tasks WHERE completed_at >= ? AND completed_at < ?`,
		from, to,
	).Scan(&total); err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	rows, err := DB.Query(
		`SELECT t.id, t.title, t.done, t.planned_date, t.due, t.completed_at, t.note_id,
		        p.id, p.name, p.type
		 FROM tasks t LEFT JOIN task_projects p ON t.project_id = p.id
		 WHERE t.completed_at >= ? AND t.completed_at < ?
		 ORDER BY t.completed_at DESC LIMIT ? OFFSET ?`,
		from, to, pageSize, offset,
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

func GetSummaryStats(from, to int64) (int, int, error) {
	if store := CurrentStore(); store != nil {
		return store.Tasks().GetSummaryStats(context.Background(), from, to)
	}

	var activeDays, projectCount int
	err := DB.QueryRow(
		`SELECT COUNT(DISTINCT DATE(completed_at, 'unixepoch')),
		        COUNT(DISTINCT project_id)
		 FROM tasks WHERE completed_at >= ? AND completed_at < ?`,
		from, to,
	).Scan(&activeDays, &projectCount)
	return activeDays, projectCount, err
}

func GetTodayTasks(todayStart, todayEnd, overdueCutoff int64) ([]model.Task, []model.Task, error) {
	if store := CurrentStore(); store != nil {
		return store.Tasks().Today(context.Background(), todayStart, todayEnd, overdueCutoff)
	}

	todayDate := time.Unix(todayStart, 0).In(time.Local).Format("2006-01-02")
	overdueCutoffDate := time.Unix(overdueCutoff, 0).In(time.Local).Format("2006-01-02")

	rows, err := DB.Query(`
		SELECT
			t.id, t.title, COALESCE(t.content, ''), COALESCE(p.name, t.project), t.project_id, p.type, t.due, t.planned_date, t.priority, t.done,
			COALESCE(t.status, CASE WHEN t.done = 1 THEN 'done' ELSE 'open' END),
			COALESCE(t.horizon, CASE WHEN t.scope IN ('monthly', 'yearly') THEN 'long' ELSE 'week' END),
			t.scope, t.sort_order, t.note_id, t.roadmap_node_id, t.created_at, t.updated_at,
			t.completed_at
		FROM tasks t
		LEFT JOIN task_projects p ON p.id = t.project_id
		WHERE t.done = 0 AND (
			(t.due >= ? AND t.due < ?)
			OR (
				COALESCE(t.horizon, CASE WHEN t.scope IN ('monthly', 'yearly') THEN 'long' ELSE 'week' END) <> 'long'
				AND t.planned_date = ?
			)
			OR (
				COALESCE(t.horizon, CASE WHEN t.scope IN ('monthly', 'yearly') THEN 'long' ELSE 'week' END) = 'long'
				AND COALESCE(t.status, CASE WHEN t.done = 1 THEN 'done' ELSE 'open' END) = 'active'
			)
		)
		ORDER BY t.sort_order ASC, t.created_at DESC
	`, todayStart, todayEnd, todayDate)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	todayTasks, err := scanTaskRows(rows)
	if err != nil {
		return nil, nil, err
	}

	rows2, err := DB.Query(`
		SELECT
			t.id, t.title, COALESCE(t.content, ''), COALESCE(p.name, t.project), t.project_id, p.type, t.due, t.planned_date, t.priority, t.done,
			COALESCE(t.status, CASE WHEN t.done = 1 THEN 'done' ELSE 'open' END),
			COALESCE(t.horizon, CASE WHEN t.scope IN ('monthly', 'yearly') THEN 'long' ELSE 'week' END),
			t.scope, t.sort_order, t.note_id, t.roadmap_node_id, t.created_at, t.updated_at,
			t.completed_at
		FROM tasks t
		LEFT JOIN task_projects p ON p.id = t.project_id
		WHERE t.done = 0
			AND COALESCE(t.horizon, CASE WHEN t.scope IN ('monthly', 'yearly') THEN 'long' ELSE 'week' END) <> 'long'
			AND (
				(t.due < ? AND t.due >= ?)
				OR (t.due IS NULL AND t.planned_date < ? AND t.planned_date >= ?)
			)
			AND (t.planned_date IS NULL OR t.planned_date <> ?)
			AND NOT (
				COALESCE(t.horizon, CASE WHEN t.scope IN ('monthly', 'yearly') THEN 'long' ELSE 'week' END) = 'long'
				AND COALESCE(t.status, CASE WHEN t.done = 1 THEN 'done' ELSE 'open' END) = 'active'
			)
		ORDER BY t.due ASC LIMIT 10
	`, todayStart, overdueCutoff, todayDate, overdueCutoffDate, todayDate)
	if err != nil {
		return nil, nil, err
	}
	defer rows2.Close()
	overdueTasks, err := scanTaskRows(rows2)
	if err != nil {
		return nil, nil, err
	}

	return todayTasks, overdueTasks, nil
}

func ensureTaskProjectByName(name string, projectType string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "personal", nil
	}
	if trimmed == "个人" || strings.EqualFold(trimmed, "personal") {
		return "personal", nil
	}
	project, err := GetTaskProjectByName(trimmed)
	if err == nil {
		return project.ID, nil
	}
	if err != sql.ErrNoRows {
		return "", err
	}
	created, err := CreateTaskProject(&model.CreateTaskProjectRequest{Name: trimmed, Type: projectType})
	if err != nil {
		return "", err
	}
	return created.ID, nil
}

func normalizeTaskDefaults(t *model.Task) {
	if strings.TrimSpace(t.Title) != "" {
		t.Title = strings.TrimSpace(t.Title)
	}
	if t.Scope == "" {
		t.Scope = "daily"
	}
	t.Scope = normalizeScope(t.Scope)
	t.Horizon = normalizeHorizon(t.Horizon)
	if t.Horizon == "long" && t.Scope == "daily" {
		t.Scope = "yearly"
	}
	t.Status = normalizeTaskStatus(t.Status)
	if t.Status == "done" {
		t.Done = 1
	}
	if t.Done == 1 {
		t.Status = "done"
	}
	// Recurring templates never get planned_date
	if t.ExecutionType == "recurring" {
		return
	}
	if t.PlannedDate == nil {
		planned := time.Now().Format("2006-01-02")
		if t.Due != nil {
			planned = time.Unix(*t.Due, 0).Format("2006-01-02")
		}
		t.PlannedDate = &planned
	}

	if t.ProjectID == nil || strings.TrimSpace(*t.ProjectID) == "" {
		if t.Project != nil {
			if projectID, err := ensureTaskProjectByName(*t.Project, "regular"); err == nil {
				t.ProjectID = &projectID
			}
		}
	}
	if t.ProjectID == nil || strings.TrimSpace(*t.ProjectID) == "" {
		projectID := "personal"
		t.ProjectID = &projectID
	}
	if project, err := GetTaskProjectByID(*t.ProjectID); err == nil {
		name := project.Name
		t.Project = &name
		t.ProjectType = &project.Type
	}
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
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "long":
		return "long"
	default:
		return "week"
	}
}

func normalizeTaskStatus(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "active":
		return "active"
	case "blocked":
		return "blocked"
	case "done":
		return "done"
	case "migrated":
		return "migrated"
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

type rowScanner interface {
	Scan(dest ...interface{}) error
}

func scanTaskRow(row rowScanner) (*model.Task, error) {
	var task model.Task
	err := row.Scan(
		&task.ID,
		&task.Title,
		&task.Content,
		&task.Project,
		&task.ProjectID,
		&task.ProjectType,
		&task.Due,
		&task.PlannedDate,
		&task.Priority,
		&task.Done,
		&task.Status,
		&task.Horizon,
		&task.Scope,
		&task.SortOrder,
		&task.NoteID,
		&task.RoadmapNodeID,
		&task.CreatedAt,
		&task.UpdatedAt,
		&task.CompletedAt,
	)
	if err != nil {
		return nil, err
	}
	return &task, nil
}

func scanTaskRows(rows *sql.Rows) ([]model.Task, error) {
	tasks := make([]model.Task, 0)
	for rows.Next() {
		task, err := scanTaskRow(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, *task)
	}
	return tasks, rows.Err()
}
