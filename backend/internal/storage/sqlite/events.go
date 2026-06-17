package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/hujinrun/flowspace/internal/model"
)

type eventRepository struct {
	db sqliteRunner
}

func (r eventRepository) List(ctx context.Context, start, end int64, page, pageSize int) ([]model.Event, int, error) {
	var total int
	if err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM events
		WHERE start_time < ? AND end_time > ?
	`, end, start).Scan(&total); err != nil {
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
		SELECT id, title, start_time, end_time, location, kind, note_id, created_at, updated_at
		FROM events
		WHERE start_time < ? AND end_time > ?
		ORDER BY start_time ASC LIMIT ? OFFSET ?
	`, end, start, pageSize, offset)
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
	event.ID = newID()
	now := nowUnix()
	event.CreatedAt = now
	event.UpdatedAt = now
	if event.Kind == "" {
		event.Kind = "work"
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO events (id, title, start_time, end_time, location, kind, note_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, event.ID, event.Title, event.StartTime, event.EndTime, event.Location, event.Kind, event.NoteID, event.CreatedAt, event.UpdatedAt)
	return err
}

func (r eventRepository) Update(ctx context.Context, id string, req *model.UpdateEventRequest) (*model.Event, error) {
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
	args = append(args, id)
	if _, err := r.db.ExecContext(ctx, fmt.Sprintf("UPDATE events SET %s WHERE id = ?", strings.Join(sets, ", ")), args...); err != nil {
		return nil, err
	}
	return r.GetByID(ctx, id)
}

func (r eventRepository) GetByID(ctx context.Context, id string) (*model.Event, error) {
	return scanSQLiteEvent(r.db.QueryRowContext(ctx, `
		SELECT id, title, start_time, end_time, location, kind, note_id, created_at, updated_at
		FROM events WHERE id = ?
	`, id))
}

func (r eventRepository) Delete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, "DELETE FROM events WHERE id = ?", id)
	return err
}

func (r eventRepository) Today(ctx context.Context, start, end int64) ([]model.Event, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, title, start_time, end_time, location, kind, note_id, created_at, updated_at
		FROM events WHERE start_time < ? AND end_time > ? ORDER BY start_time ASC
	`, end, start)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSQLiteEvents(rows)
}

func scanSQLiteEvent(row sqliteRowScanner) (*model.Event, error) {
	var event model.Event
	if err := row.Scan(&event.ID, &event.Title, &event.StartTime, &event.EndTime, &event.Location, &event.Kind, &event.NoteID, &event.CreatedAt, &event.UpdatedAt); err != nil {
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
