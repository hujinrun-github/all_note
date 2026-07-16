package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
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
		WHERE workspace_id = ? AND deleted_at IS NULL AND start_time < ? AND end_time > ?
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
		WHERE e.workspace_id = ? AND e.deleted_at IS NULL AND e.start_time < ? AND e.end_time > ?
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
	clientID := deterministicSQLiteMobileEntityClientID("event", workspaceID, event.ID)
	return (mobileSyncRepository{db: r.db}).withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO events (id, client_id, revision, title, start_time, end_time, location, kind, note_id, project_id, created_at, updated_at, workspace_id)
			VALUES (?, ?, 1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, event.ID, clientID, event.Title, event.StartTime, event.EndTime, event.Location, event.Kind, event.NoteID, event.ProjectID, event.CreatedAt, event.UpdatedAt, workspaceID); err != nil {
			return err
		}
		return persistSQLiteServerEntityChange(ctx, tx, workspaceID, uuid.NewString(), "event", "event.server_created", clientID, event.UpdatedAt)
	})
}

func (r eventRepository) Update(ctx context.Context, id string, req *model.UpdateEventRequest) (*model.Event, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	now := nowUnix()
	sets := []string{"updated_at = ?", "revision = revision + 1"}
	args := []interface{}{now}
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
	var updated *model.Event
	err = (mobileSyncRepository{db: r.db}).withTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, fmt.Sprintf("UPDATE events SET %s WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL", strings.Join(sets, ", ")), args...)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			if err != nil {
				return err
			}
			return sql.ErrNoRows
		}
		updated, err = scanSQLiteEvent(tx.QueryRowContext(ctx, `
			`+sqliteEventSelect+` WHERE e.workspace_id = ? AND e.id = ? AND e.deleted_at IS NULL
		`, workspaceID, id))
		if err != nil {
			return err
		}
		var clientID string
		if err := tx.QueryRowContext(ctx, `SELECT client_id FROM events WHERE workspace_id = ? AND id = ?`, workspaceID, id).Scan(&clientID); err != nil {
			return err
		}
		return persistSQLiteServerEntityChange(ctx, tx, workspaceID, uuid.NewString(), "event", "event.server_updated", clientID, now)
	})
	return updated, err
}

func (r eventRepository) GetByID(ctx context.Context, id string) (*model.Event, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return scanSQLiteEvent(r.db.QueryRowContext(ctx, `
		`+sqliteEventSelect+`
		WHERE e.workspace_id = ? AND e.id = ? AND e.deleted_at IS NULL
	`, workspaceID, id))
}

func (r eventRepository) Delete(ctx context.Context, id string) error {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	now := nowUnix()
	return (mobileSyncRepository{db: r.db}).withTx(ctx, func(tx *sql.Tx) error {
		var clientID string
		err := tx.QueryRowContext(ctx, `SELECT client_id FROM events WHERE workspace_id = ? AND id = ? AND deleted_at IS NULL`, workspaceID, id).Scan(&clientID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE events SET deleted_at = ?, updated_at = ?, revision = revision + 1 WHERE workspace_id = ? AND id = ? AND deleted_at IS NULL`, now, now, workspaceID, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO mobile_retired_ids (workspace_id, entity_type, client_id, retired_at) VALUES (?, 'event', ?, ?)`, workspaceID, clientID, now); err != nil {
			return err
		}
		return persistSQLiteServerEntityChange(ctx, tx, workspaceID, uuid.NewString(), "event", "event.server_deleted", clientID, now)
	})
}

func (r eventRepository) Today(ctx context.Context, start, end int64) ([]model.Event, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		`+sqliteEventSelect+`
		WHERE e.workspace_id = ? AND e.deleted_at IS NULL AND e.start_time < ? AND e.end_time > ? ORDER BY e.start_time ASC
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
