package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/model"
)

type eventRepository struct {
	db postgresRunner
}

func (r eventRepository) List(ctx context.Context, start, end int64, page, pageSize int) ([]model.Event, int, error) {
	var total int
	if err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM events
		WHERE time_range && tstzrange($1, $2, '[)')
	`, unixToTime(start), unixToTime(end)).Scan(&total); err != nil {
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
		SELECT id, title, start_at, end_at, location, kind, note_id, created_at, updated_at
		FROM events
		WHERE time_range && tstzrange($1, $2, '[)')
		ORDER BY start_at ASC LIMIT $3 OFFSET $4
	`, unixToTime(start), unixToTime(end), pageSize, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	events, err := scanPostgresEvents(rows)
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
	return r.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO events (id, title, start_at, end_at, time_range, location, kind, note_id, created_at, updated_at)
			VALUES ($1, $2, $3, $4, tstzrange($3, $4, '[)'), $5, $6, $7, $8, $9)
		`, event.ID, event.Title, unixToTime(event.StartTime), unixToTime(event.EndTime), event.Location, event.Kind, event.NoteID, unixToTime(event.CreatedAt), unixToTime(event.UpdatedAt)); err != nil {
			return err
		}
		return upsertEventSearchIndex(ctx, tx, event)
	})
}

func (r eventRepository) Update(ctx context.Context, id string, req *model.UpdateEventRequest) (*model.Event, error) {
	builder := newPgSetBuilder(1)
	builder.Add("updated_at", time.Now().UTC())
	if req.Title != nil {
		builder.Add("title", *req.Title)
	}
	if req.StartTime != nil {
		builder.Add("start_at", unixToTime(*req.StartTime))
	}
	if req.EndTime != nil {
		builder.Add("end_at", unixToTime(*req.EndTime))
	}
	if req.Location != nil {
		builder.Add("location", *req.Location)
	}
	if req.Kind != nil {
		builder.Add("kind", *req.Kind)
	}
	clause, args := builder.ClauseAndArgs()
	args = append(args, id)

	var updated *model.Event
	err := r.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("UPDATE events SET %s WHERE id = %s", clause, pgPlaceholder(len(args))), args...); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE events SET time_range = tstzrange(start_at, end_at, '[)') WHERE id = $1`, id); err != nil {
			return err
		}
		event, err := scanPostgresEvent(tx.QueryRowContext(ctx, `
			SELECT id, title, start_at, end_at, location, kind, note_id, created_at, updated_at
			FROM events WHERE id = $1
		`, id))
		if err != nil {
			return err
		}
		if err := upsertEventSearchIndex(ctx, tx, event); err != nil {
			return err
		}
		updated = event
		return nil
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

func (r eventRepository) GetByID(ctx context.Context, id string) (*model.Event, error) {
	return scanPostgresEvent(r.db.QueryRowContext(ctx, `
		SELECT id, title, start_at, end_at, location, kind, note_id, created_at, updated_at
		FROM events WHERE id = $1
	`, id))
}

func (r eventRepository) Delete(ctx context.Context, id string) error {
	return r.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM events WHERE id = $1`, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM search_index WHERE entity_type = 'event' AND entity_id = $1`, id)
		return err
	})
}

func (r eventRepository) Today(ctx context.Context, start, end int64) ([]model.Event, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, title, start_at, end_at, location, kind, note_id, created_at, updated_at
		FROM events
		WHERE time_range && tstzrange($1, $2, '[)')
		ORDER BY start_at ASC
	`, unixToTime(start), unixToTime(end))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPostgresEvents(rows)
}

func (r eventRepository) withTx(ctx context.Context, fn func(*sql.Tx) error) error {
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

func scanPostgresEvent(row rowScanner) (*model.Event, error) {
	var event model.Event
	var startAt time.Time
	var endAt time.Time
	var location sql.NullString
	var noteID sql.NullString
	var createdAt time.Time
	var updatedAt time.Time
	if err := row.Scan(&event.ID, &event.Title, &startAt, &endAt, &location, &event.Kind, &noteID, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	event.StartTime = timeToUnix(startAt)
	event.EndTime = timeToUnix(endAt)
	if location.Valid {
		event.Location = &location.String
	}
	if noteID.Valid {
		event.NoteID = &noteID.String
	}
	event.CreatedAt = timeToUnix(createdAt)
	event.UpdatedAt = timeToUnix(updatedAt)
	return &event, nil
}

func scanPostgresEvents(rows *sql.Rows) ([]model.Event, error) {
	events := make([]model.Event, 0)
	for rows.Next() {
		event, err := scanPostgresEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, *event)
	}
	return events, rows.Err()
}

func upsertEventSearchIndex(ctx context.Context, tx *sql.Tx, event *model.Event) error {
	contentParts := []string{event.Kind}
	if event.Location != nil {
		contentParts = append([]string{*event.Location}, contentParts...)
	}
	content := strings.Join(contentParts, " ")
	_, err := tx.ExecContext(ctx, `
		INSERT INTO search_index (entity_type, entity_id, title, content, tags, updated_at, search_vector)
		VALUES (
			'event',
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
	`, event.ID, event.Title, content, unixToTime(event.UpdatedAt))
	return err
}
