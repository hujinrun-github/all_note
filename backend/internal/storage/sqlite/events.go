package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/model"
)

type eventRepository struct {
	db sqliteRunner
}

const sqliteEventSelect = `
	SELECT e.id, e.title, e.start_time, e.end_time, e.location, e.kind, e.note_id,
	       e.project_id, p.name, p.type, e.created_at, e.updated_at
	FROM events e
	LEFT JOIN task_projects p ON p.workspace_id = e.workspace_id AND p.id = e.project_id
`

func (r eventRepository) List(ctx context.Context, start, end int64, page, pageSize int) ([]model.Event, int, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, 0, err
	}
	var total int
	if err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM events
		WHERE workspace_id = ? AND start_time < ? AND end_time > ?
	`, workspaceID, end, start).Scan(&total); err != nil {
		return nil, 0, err
	}
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize
	rows, err := r.db.QueryContext(ctx, `
		`+sqliteEventSelect+`
		WHERE e.workspace_id = ? AND e.start_time < ? AND e.end_time > ?
		ORDER BY e.start_time ASC LIMIT ? OFFSET ?
	`, workspaceID, end, start, pageSize, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	events, err := scanSQLiteEvents(rows)
	if err != nil {
		return nil, 0, err
	}
	return events, total, nil
}

func (r eventRepository) Create(ctx context.Context, event *model.Event) error {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	event.ID = newID()
	now := nowUnix()
	event.CreatedAt = now
	event.UpdatedAt = now
	if event.Kind == "" {
		event.Kind = "work"
	}
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO events (id, title, start_time, end_time, location, kind, note_id, project_id, created_at, updated_at, workspace_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, event.ID, event.Title, event.StartTime, event.EndTime, event.Location, event.Kind, event.NoteID, event.ProjectID, event.CreatedAt, event.UpdatedAt, workspaceID)
	return err
}

func (r eventRepository) Update(ctx context.Context, id string, req *model.UpdateEventRequest) (*model.Event, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	sets := []string{"updated_at = ?"}
	args := []interface{}{nowUnix()}
	if req.Title != nil {
		sets = append(sets, "title = ?")
		args = append(args, *req.Title)
	}
	if req.StartTime != nil {
		sets = append(sets, "start_time = ?")
		args = append(args, *req.StartTime)
	}
	if req.EndTime != nil {
		sets = append(sets, "end_time = ?")
		args = append(args, *req.EndTime)
	}
	if req.Location != nil {
		sets = append(sets, "location = ?")
		args = append(args, *req.Location)
	}
	if req.Kind != nil {
		sets = append(sets, "kind = ?")
		args = append(args, *req.Kind)
	}
	if req.ProjectID != nil {
		if strings.TrimSpace(*req.ProjectID) == "" {
			sets = append(sets, "project_id = NULL")
		} else {
			sets = append(sets, "project_id = ?")
			args = append(args, *req.ProjectID)
		}
	}
	args = append(args, id)
	args = append(args, workspaceID)
	if _, err := r.db.ExecContext(ctx, fmt.Sprintf("UPDATE events SET %s WHERE id = ? AND workspace_id = ?", strings.Join(sets, ", ")), args...); err != nil {
		return nil, err
	}
	return r.GetByID(ctx, id)
}

func (r eventRepository) GetByID(ctx context.Context, id string) (*model.Event, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return scanSQLiteEvent(r.db.QueryRowContext(ctx, `
		`+sqliteEventSelect+`
		WHERE e.workspace_id = ? AND e.id = ?
	`, workspaceID, id))
}

func (r eventRepository) Delete(ctx context.Context, id string) error {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, "DELETE FROM events WHERE workspace_id = ? AND id = ?", workspaceID, id)
	return err
}

func (r eventRepository) Today(ctx context.Context, start, end int64) ([]model.Event, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		`+sqliteEventSelect+`
		WHERE e.workspace_id = ? AND e.start_time < ? AND e.end_time > ? ORDER BY e.start_time ASC
	`, workspaceID, end, start)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSQLiteEvents(rows)
}

func scanSQLiteEvent(row sqliteRowScanner) (*model.Event, error) {
	var event model.Event
	if err := row.Scan(&event.ID, &event.Title, &event.StartTime, &event.EndTime, &event.Location, &event.Kind, &event.NoteID, &event.ProjectID, &event.Project, &event.ProjectType, &event.CreatedAt, &event.UpdatedAt); err != nil {
		return nil, err
	}
	return &event, nil
}

func scanSQLiteEvents(rows *sql.Rows) ([]model.Event, error) {
	events := make([]model.Event, 0)
	for rows.Next() {
		event, err := scanSQLiteEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, *event)
	}
	return events, rows.Err()
}
