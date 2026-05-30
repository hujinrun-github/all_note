package repository

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/hujinrun/flowspace/internal/model"
)

func GetTasks(project, status, scope string, page, pageSize int) ([]model.Task, int, error) {
	where := []string{"1=1"}
	args := []interface{}{}

	if project != "" {
		where = append(where, "t.project = ?")
		args = append(args, project)
	}
	if status == "active" {
		where = append(where, "t.done = 0")
	} else if status == "done" {
		where = append(where, "t.done = 1")
	}
	if scope != "" {
		where = append(where, "t.scope = ?")
		args = append(args, scope)
	}

	whereClause := strings.Join(where, " AND ")

	var total int
	DB.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM tasks t WHERE %s", whereClause), args...).Scan(&total)

	offset := (page - 1) * pageSize
	query := fmt.Sprintf(`
		SELECT t.id, t.title, t.project, t.due, t.priority, t.done, t.scope, t.sort_order, t.note_id, t.created_at, t.updated_at
		FROM tasks t WHERE %s ORDER BY t.sort_order ASC, t.created_at DESC LIMIT ? OFFSET ?
	`, whereClause)
	args = append(args, pageSize, offset)

	rows, err := DB.Query(query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var tasks []model.Task
	for rows.Next() {
		var t model.Task
		if err := rows.Scan(&t.ID, &t.Title, &t.Project, &t.Due, &t.Priority, &t.Done, &t.Scope, &t.SortOrder, &t.NoteID, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, 0, err
		}
		tasks = append(tasks, t)
	}
	return tasks, total, nil
}

func CreateTask(t *model.Task) error {
	t.ID = newUUID()
	now := nowUnix()
	t.CreatedAt = now
	t.UpdatedAt = now
	if t.Scope == "" {
		t.Scope = "daily"
	}
	_, err := DB.Exec(`
		INSERT INTO tasks (id, title, project, due, priority, done, scope, sort_order, note_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, t.ID, t.Title, t.Project, t.Due, t.Priority, t.Done, t.Scope, t.SortOrder, t.NoteID, t.CreatedAt, t.UpdatedAt)
	return err
}

func UpdateTask(id string, req *model.UpdateTaskRequest) (*model.Task, error) {
	sets := []string{"updated_at = ?"}
	args := []interface{}{nowUnix()}

	if req.Title != nil {
		sets = append(sets, "title = ?")
		args = append(args, *req.Title)
	}
	if req.Project != nil {
		sets = append(sets, "project = ?")
		args = append(args, *req.Project)
	}
	if req.Due != nil {
		sets = append(sets, "due = ?")
		args = append(args, *req.Due)
	}
	if req.Priority != nil {
		sets = append(sets, "priority = ?")
		args = append(args, *req.Priority)
	}
	if req.Done != nil {
		sets = append(sets, "done = ?")
		args = append(args, *req.Done)
	}
	if req.Scope != nil {
		sets = append(sets, "scope = ?")
		args = append(args, *req.Scope)
	}
	if req.SortOrder != nil {
		sets = append(sets, "sort_order = ?")
		args = append(args, *req.SortOrder)
	}

	args = append(args, id)
	_, err := DB.Exec(fmt.Sprintf("UPDATE tasks SET %s WHERE id = ?", strings.Join(sets, ", ")), args...)
	if err != nil {
		return nil, err
	}
	return GetTaskByID(id)
}

func GetTaskByID(id string) (*model.Task, error) {
	var t model.Task
	err := DB.QueryRow(`
		SELECT id, title, project, due, priority, done, scope, sort_order, note_id, created_at, updated_at
		FROM tasks WHERE id = ?
	`, id).Scan(&t.ID, &t.Title, &t.Project, &t.Due, &t.Priority, &t.Done, &t.Scope, &t.SortOrder, &t.NoteID, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func DeleteTask(id string) error {
	_, err := DB.Exec("DELETE FROM tasks WHERE id = ?", id)
	return err
}

func GetTodayTasks(todayStart, todayEnd, overdueCutoff int64) ([]model.Task, []model.Task, error) {
	rows, err := DB.Query(`
		SELECT id, title, project, due, priority, done, scope, sort_order, note_id, created_at, updated_at
		FROM tasks WHERE done = 0 AND due >= ? AND due < ? ORDER BY sort_order ASC, created_at DESC
	`, todayStart, todayEnd)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	todayTasks := scanTasks(rows)

	rows2, err := DB.Query(`
		SELECT id, title, project, due, priority, done, scope, sort_order, note_id, created_at, updated_at
		FROM tasks WHERE done = 0 AND due < ? AND due >= ? ORDER BY due ASC LIMIT 10
	`, todayStart, overdueCutoff)
	if err != nil {
		return nil, nil, err
	}
	defer rows2.Close()
	overdueTasks := scanTasks(rows2)

	return todayTasks, overdueTasks, nil
}

func scanTasks(rows *sql.Rows) []model.Task {
	var tasks []model.Task
	for rows.Next() {
		var t model.Task
		rows.Scan(&t.ID, &t.Title, &t.Project, &t.Due, &t.Priority, &t.Done, &t.Scope, &t.SortOrder, &t.NoteID, &t.CreatedAt, &t.UpdatedAt)
		tasks = append(tasks, t)
	}
	return tasks
}
