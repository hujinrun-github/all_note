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
)

type eventRepository struct {
	db postgresRunner
}

const postgresEventSelect = `
	SELECT e.id, e.title, e.start_at, e.end_at, e.location, e.kind, e.note_id,
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
		WHERE workspace_id = $1 AND deleted_at IS NULL AND time_range && tstzrange($2, $3, '[)')
	`, workspaceID, unixToTime(start), unixToTime(end)).Scan(&total); err != nil {
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
		`+postgresEventSelect+`
		WHERE e.workspace_id = $1 AND e.deleted_at IS NULL AND e.time_range && tstzrange($2, $3, '[)')
		ORDER BY e.start_at ASC LIMIT $4 OFFSET $5
	`, workspaceID, unixToTime(start), unixToTime(end), pageSize, offset)
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
	clientID := deterministicPostgresMobileEntityClientID("event", workspaceID, event.ID)
	return r.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO events (id, client_id, revision, title, start_at, end_at, time_range, location, kind, note_id, project_id, created_at, updated_at, workspace_id)
			VALUES ($1, $2, 1, $3, $4, $5, tstzrange($4, $5, '[)'), $6, $7, $8, $9, $10, $11, $12)
		`, event.ID, clientID, event.Title, unixToTime(event.StartTime), unixToTime(event.EndTime), event.Location, event.Kind, event.NoteID, event.ProjectID, unixToTime(event.CreatedAt), unixToTime(event.UpdatedAt), workspaceID); err != nil {
			return err
		}
		if err := upsertEventSearchIndex(ctx, tx, workspaceID, event); err != nil {
			return err
		}
		return persistPostgresServerEntityChange(ctx, tx, workspaceID, uuid.NewString(), "event", "event.server_created", clientID, unixToTime(event.UpdatedAt))
	})
}

func (r eventRepository) Update(ctx context.Context, id string, req *model.UpdateEventRequest) (*model.Event, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
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
	if req.ProjectID != nil {
		if strings.TrimSpace(*req.ProjectID) == "" {
			builder.Add("project_id", nil)
		} else {
			builder.Add("project_id", *req.ProjectID)
		}
	}
	clause, args := builder.ClauseAndArgs()
	args = append(args, id, workspaceID)

	var updated *model.Event
	err = r.withTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, fmt.Sprintf("UPDATE events SET %s, revision = revision + 1 WHERE id = %s AND workspace_id = %s AND deleted_at IS NULL", clause, pgPlaceholder(len(args)-1), pgPlaceholder(len(args))), args...)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			if err != nil {
				return err
			}
			return sql.ErrNoRows
		}
		if _, err := tx.ExecContext(ctx, `UPDATE events SET time_range = tstzrange(start_at, end_at, '[)') WHERE id = $1 AND workspace_id = $2`, id, workspaceID); err != nil {
			return err
		}
		event, err := scanPostgresEvent(tx.QueryRowContext(ctx, `
			`+postgresEventSelect+`
			WHERE e.id = $1 AND e.workspace_id = $2 AND e.deleted_at IS NULL
		`, id, workspaceID))
		if err != nil {
			return err
		}
		if err := upsertEventSearchIndex(ctx, tx, workspaceID, event); err != nil {
			return err
		}
		updated = event
		var clientID string
		if err := tx.QueryRowContext(ctx, `SELECT client_id FROM events WHERE workspace_id = $1 AND id = $2`, workspaceID, id).Scan(&clientID); err != nil {
			return err
		}
		return persistPostgresServerEntityChange(ctx, tx, workspaceID, uuid.NewString(), "event", "event.server_updated", clientID, time.Now().UTC())
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

func (r eventRepository) GetByID(ctx context.Context, id string) (*model.Event, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return scanPostgresEvent(r.db.QueryRowContext(ctx, `
		`+postgresEventSelect+`
		WHERE e.id = $1 AND e.workspace_id = $2 AND e.deleted_at IS NULL
	`, id, workspaceID))
}

func (r eventRepository) Delete(ctx context.Context, id string) error {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	return r.withTx(ctx, func(tx *sql.Tx) error {
		var clientID string
		err := tx.QueryRowContext(ctx, `SELECT client_id FROM events WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL FOR UPDATE`, workspaceID, id).Scan(&clientID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE events SET deleted_at = $1, updated_at = $1, revision = revision + 1 WHERE workspace_id = $2 AND id = $3 AND deleted_at IS NULL`, now, workspaceID, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO mobile_retired_ids (workspace_id, entity_type, client_id, retired_at) VALUES ($1, 'event', $2, $3) ON CONFLICT DO NOTHING`, workspaceID, clientID, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM search_index WHERE workspace_id = $1 AND entity_type = 'event' AND entity_id = $2`, workspaceID, id); err != nil {
			return err
		}
		return persistPostgresServerEntityChange(ctx, tx, workspaceID, uuid.NewString(), "event", "event.server_deleted", clientID, now)
	})
}

func (r eventRepository) Today(ctx context.Context, start, end int64) ([]model.Event, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		`+postgresEventSelect+`
		WHERE e.workspace_id = $1 AND e.deleted_at IS NULL AND e.time_range && tstzrange($2, $3, '[)')
		ORDER BY e.start_at ASC
	`, workspaceID, unixToTime(start), unixToTime(end))
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
	var projectID sql.NullString
	var project sql.NullString
	var projectType sql.NullString
	var createdAt time.Time
	var updatedAt time.Time
	if err := row.Scan(&event.ID, &event.Title, &startAt, &endAt, &location, &event.Kind, &noteID, &projectID, &project, &projectType, &createdAt, &updatedAt); err != nil {
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
	if projectID.Valid {
		event.ProjectID = &projectID.String
	}
	if project.Valid {
		event.Project = &project.String
	}
	if projectType.Valid {
		event.ProjectType = &projectType.String
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

func upsertEventSearchIndex(ctx context.Context, tx *sql.Tx, workspaceID string, event *model.Event) error {
	contentParts := []string{event.Kind}
	if event.Location != nil {
		contentParts = append([]string{*event.Location}, contentParts...)
	}
	content := strings.Join(contentParts, " ")
	_, err := tx.ExecContext(ctx, `
		INSERT INTO search_index (workspace_id, entity_type, entity_id, title, content, tags, updated_at, search_vector)
		VALUES (
			$1,
			'event',
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
	`, workspaceID, event.ID, event.Title, content, unixToTime(event.UpdatedAt))
	return err
}
