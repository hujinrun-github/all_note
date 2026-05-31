package repository

import (
	"fmt"
	"strings"

	"github.com/hujinrun/flowspace/internal/model"
)

func GetEvents(monthStart, monthEnd int64, page, pageSize int) ([]model.Event, int, error) {
	var total int
	DB.QueryRow(`
		SELECT COUNT(*) FROM events
		WHERE start_time < ? AND end_time > ?
	`, monthEnd, monthStart).Scan(&total)

	offset := (page - 1) * pageSize
	rows, err := DB.Query(`
		SELECT id, title, start_time, end_time, location, kind, note_id, created_at, updated_at
		FROM events
		WHERE start_time < ? AND end_time > ?
		ORDER BY start_time ASC LIMIT ? OFFSET ?
	`, monthEnd, monthStart, pageSize, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

		events := make([]model.Event, 0)
	for rows.Next() {
		var e model.Event
		if err := rows.Scan(&e.ID, &e.Title, &e.StartTime, &e.EndTime, &e.Location, &e.Kind, &e.NoteID, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, 0, err
		}
		events = append(events, e)
	}
	return events, total, nil
}

func CreateEvent(e *model.Event) error {
	e.ID = newUUID()
	now := nowUnix()
	e.CreatedAt = now
	e.UpdatedAt = now
	if e.Kind == "" {
		e.Kind = "work"
	}
	_, err := DB.Exec(`
		INSERT INTO events (id, title, start_time, end_time, location, kind, note_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, e.ID, e.Title, e.StartTime, e.EndTime, e.Location, e.Kind, e.NoteID, e.CreatedAt, e.UpdatedAt)
	return err
}

func UpdateEvent(id string, req *model.UpdateEventRequest) (*model.Event, error) {
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
	_, err := DB.Exec(fmt.Sprintf("UPDATE events SET %s WHERE id = ?", strings.Join(sets, ", ")), args...)
	if err != nil {
		return nil, err
	}
	return GetEventByID(id)
}

func GetEventByID(id string) (*model.Event, error) {
	var e model.Event
	err := DB.QueryRow(`
		SELECT id, title, start_time, end_time, location, kind, note_id, created_at, updated_at
		FROM events WHERE id = ?
	`, id).Scan(&e.ID, &e.Title, &e.StartTime, &e.EndTime, &e.Location, &e.Kind, &e.NoteID, &e.CreatedAt, &e.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func DeleteEvent(id string) error {
	_, err := DB.Exec("DELETE FROM events WHERE id = ?", id)
	return err
}

func GetTodayEvents(todayStart, todayEnd int64) ([]model.Event, error) {
	rows, err := DB.Query(`
		SELECT id, title, start_time, end_time, location, kind, note_id, created_at, updated_at
		FROM events WHERE start_time < ? AND end_time > ? ORDER BY start_time ASC
	`, todayEnd, todayStart)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

		events := make([]model.Event, 0)
	for rows.Next() {
		var e model.Event
		rows.Scan(&e.ID, &e.Title, &e.StartTime, &e.EndTime, &e.Location, &e.Kind, &e.NoteID, &e.CreatedAt, &e.UpdatedAt)
		events = append(events, e)
	}
	return events, nil
}
