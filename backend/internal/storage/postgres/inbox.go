package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/model"
)

type inboxRepository struct {
	db postgresRunner
}

func (r inboxRepository) List(ctx context.Context, kind string, page, pageSize int) ([]model.InboxItem, int, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, 0, err
	}
	where := "workspace_id = $1 AND deleted_at IS NULL AND archived = false AND converted_to IS NULL"
	args := []interface{}{workspaceID}
	if kind != "" && kind != "all" {
		where += " AND kind = $2"
		args = append(args, kind)
	}
	var total int
	if err := r.db.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM inbox WHERE %s", where), args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize
	limitPlaceholder := pgPlaceholder(len(args) + 1)
	offsetPlaceholder := pgPlaceholder(len(args) + 2)
	rows, err := r.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT id, kind, title, body, source, archived, converted_to, created_at, updated_at
		FROM inbox WHERE %s ORDER BY created_at DESC LIMIT %s OFFSET %s
	`, where, limitPlaceholder, offsetPlaceholder), append(args, pageSize, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items, err := scanPostgresInboxItems(rows)
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func (r inboxRepository) Create(ctx context.Context, item *model.InboxItem) error {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	item.ID = newID()
	now := nowUnix()
	item.CreatedAt = now
	item.UpdatedAt = now
	if item.Source == "" {
		item.Source = "quick-capture"
	}
	clientID := deterministicPostgresMobileEntityClientID("inbox", workspaceID, item.ID)
	return (mobileSyncRepository{db: r.db}).withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO inbox (id, client_id, revision, kind, title, body, source, archived, converted_to, payload, created_at, updated_at, workspace_id)
			VALUES ($1, $2, 1, $3, $4, $5, $6, $7, $8, '{}'::jsonb, $9, $10, $11)
		`, item.ID, clientID, item.Kind, item.Title, item.Body, item.Source, item.Archived == 1, item.ConvertedTo, unixToTime(item.CreatedAt), unixToTime(item.UpdatedAt), workspaceID); err != nil {
			return err
		}
		return persistPostgresServerEntityChange(ctx, tx, workspaceID, uuid.NewString(), "inbox", "inbox.server_created", clientID, unixToTime(item.UpdatedAt))
	})
}

func (r inboxRepository) GetByID(ctx context.Context, id string) (*model.InboxItem, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return scanPostgresInboxItem(r.db.QueryRowContext(ctx, `
		SELECT id, kind, title, body, source, archived, converted_to, created_at, updated_at
		FROM inbox WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL
	`, workspaceID, id))
}

func (r inboxRepository) MarkConverted(ctx context.Context, id, convertedTo string) error {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	return (mobileSyncRepository{db: r.db}).withTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE inbox SET converted_to = $1, updated_at = $2, revision = revision + 1 WHERE workspace_id = $3 AND id = $4 AND converted_to IS NULL AND deleted_at IS NULL`, convertedTo, now, workspaceID, id)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			if err != nil {
				return err
			}
			return sql.ErrNoRows
		}
		var clientID string
		if err := tx.QueryRowContext(ctx, `SELECT client_id FROM inbox WHERE workspace_id = $1 AND id = $2`, workspaceID, id).Scan(&clientID); err != nil {
			return err
		}
		return persistPostgresServerEntityChange(ctx, tx, workspaceID, uuid.NewString(), "inbox", "inbox.server_updated", clientID, now)
	})
}

func (r inboxRepository) Delete(ctx context.Context, id string) error {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	return (mobileSyncRepository{db: r.db}).withTx(ctx, func(tx *sql.Tx) error {
		var clientID string
		err := tx.QueryRowContext(ctx, `SELECT client_id FROM inbox WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL FOR UPDATE`, workspaceID, id).Scan(&clientID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE inbox SET deleted_at = $1, updated_at = $1, revision = revision + 1 WHERE workspace_id = $2 AND id = $3 AND deleted_at IS NULL`, now, workspaceID, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO mobile_retired_ids (workspace_id, entity_type, client_id, retired_at) VALUES ($1, 'inbox', $2, $3) ON CONFLICT DO NOTHING`, workspaceID, clientID, now); err != nil {
			return err
		}
		return persistPostgresServerEntityChange(ctx, tx, workspaceID, uuid.NewString(), "inbox", "inbox.server_deleted", clientID, now)
	})
}

func (r inboxRepository) BatchArchive(ctx context.Context, ids []string) (int64, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	clause, err := pgInClause("id", 3, len(ids))
	if err != nil {
		return 0, err
	}
	args := make([]interface{}, 0, len(ids)+2)
	args = append(args, time.Now().UTC(), workspaceID)
	for _, id := range ids {
		args = append(args, id)
	}
	var affected int64
	err = (mobileSyncRepository{db: r.db}).withTx(ctx, func(tx *sql.Tx) error {
		clientIDs, err := postgresInboxClientIDs(ctx, tx, workspaceID, ids)
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, fmt.Sprintf("UPDATE inbox SET archived = true, updated_at = $1, revision = revision + 1 WHERE workspace_id = $2 AND deleted_at IS NULL AND %s", clause), args...)
		if err != nil {
			return err
		}
		affected, err = result.RowsAffected()
		if err != nil {
			return err
		}
		for _, clientID := range clientIDs {
			if err := persistPostgresServerEntityChange(ctx, tx, workspaceID, uuid.NewString(), "inbox", "inbox.server_updated", clientID, args[0].(time.Time)); err != nil {
				return err
			}
		}
		return nil
	})
	return affected, err
}

func (r inboxRepository) BatchDelete(ctx context.Context, ids []string) (int64, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	clause, err := pgInClause("id", 3, len(ids))
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	var affected int64
	err = (mobileSyncRepository{db: r.db}).withTx(ctx, func(tx *sql.Tx) error {
		clientIDs, err := postgresInboxClientIDs(ctx, tx, workspaceID, ids)
		if err != nil {
			return err
		}
		updateArgs := make([]interface{}, 0, len(ids)+2)
		updateArgs = append(updateArgs, now, workspaceID)
		for _, id := range ids {
			updateArgs = append(updateArgs, id)
		}
		result, err := tx.ExecContext(ctx, fmt.Sprintf("UPDATE inbox SET deleted_at = $1, updated_at = $1, revision = revision + 1 WHERE workspace_id = $2 AND deleted_at IS NULL AND %s", clause), updateArgs...)
		if err != nil {
			return err
		}
		affected, err = result.RowsAffected()
		if err != nil {
			return err
		}
		for _, clientID := range clientIDs {
			if _, err := tx.ExecContext(ctx, `INSERT INTO mobile_retired_ids (workspace_id, entity_type, client_id, retired_at) VALUES ($1, 'inbox', $2, $3) ON CONFLICT DO NOTHING`, workspaceID, clientID, now); err != nil {
				return err
			}
			if err := persistPostgresServerEntityChange(ctx, tx, workspaceID, uuid.NewString(), "inbox", "inbox.server_deleted", clientID, now); err != nil {
				return err
			}
		}
		return nil
	})
	return affected, err
}

func postgresInboxClientIDs(ctx context.Context, tx *sql.Tx, workspaceID string, ids []string) ([]string, error) {
	clause, err := pgInClause("id", 2, len(ids))
	if err != nil {
		return nil, err
	}
	args := make([]interface{}, 0, len(ids)+1)
	args = append(args, workspaceID)
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := tx.QueryContext(ctx, fmt.Sprintf(`SELECT client_id FROM inbox WHERE workspace_id = $1 AND deleted_at IS NULL AND %s ORDER BY id FOR UPDATE`, clause), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	clientIDs := make([]string, 0, len(ids))
	for rows.Next() {
		var clientID string
		if err := rows.Scan(&clientID); err != nil {
			return nil, err
		}
		clientIDs = append(clientIDs, clientID)
	}
	return clientIDs, rows.Err()
}

func scanPostgresInboxItem(row rowScanner) (*model.InboxItem, error) {
	var item model.InboxItem
	var body sql.NullString
	var convertedTo sql.NullString
	var archived bool
	var createdAt time.Time
	var updatedAt time.Time
	if err := row.Scan(&item.ID, &item.Kind, &item.Title, &body, &item.Source, &archived, &convertedTo, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	if body.Valid {
		item.Body = &body.String
	}
	if convertedTo.Valid {
		item.ConvertedTo = &convertedTo.String
	}
	if archived {
		item.Archived = 1
	}
	item.CreatedAt = timeToUnix(createdAt)
	item.UpdatedAt = timeToUnix(updatedAt)
	return &item, nil
}

func scanPostgresInboxItems(rows *sql.Rows) ([]model.InboxItem, error) {
	items := make([]model.InboxItem, 0)
	for rows.Next() {
		item, err := scanPostgresInboxItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}
