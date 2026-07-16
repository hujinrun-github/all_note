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

type inboxRepository struct {
	db sqliteRunner
}

func (r inboxRepository) List(ctx context.Context, kind string, page, pageSize int) ([]model.InboxItem, int, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, 0, err
	}
	where := "workspace_id = ? AND deleted_at IS NULL AND archived = 0 AND converted_to IS NULL"
	args := []interface{}{workspaceID}
	if kind != "" && kind != "all" {
		where += " AND kind = ?"
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
	rows, err := r.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT id, kind, title, body, source, archived, converted_to, created_at, updated_at
		FROM inbox WHERE %s ORDER BY created_at DESC LIMIT ? OFFSET ?
	`, where), append(args, pageSize, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items, err := scanSQLiteInboxItems(rows)
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
	clientID := deterministicSQLiteMobileEntityClientID("inbox", workspaceID, item.ID)
	return (mobileSyncRepository{db: r.db}).withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO inbox (id, client_id, revision, kind, title, body, source, archived, converted_to, created_at, updated_at, workspace_id)
			VALUES (?, ?, 1, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, item.ID, clientID, item.Kind, item.Title, item.Body, item.Source, item.Archived, item.ConvertedTo, item.CreatedAt, item.UpdatedAt, workspaceID); err != nil {
			return err
		}
		return persistSQLiteServerEntityChange(ctx, tx, workspaceID, uuid.NewString(), "inbox", "inbox.server_created", clientID, item.UpdatedAt)
	})
}

func (r inboxRepository) GetByID(ctx context.Context, id string) (*model.InboxItem, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return scanSQLiteInboxItem(r.db.QueryRowContext(ctx, `
		SELECT id, kind, title, body, source, archived, converted_to, created_at, updated_at
		FROM inbox WHERE workspace_id = ? AND id = ? AND deleted_at IS NULL
	`, workspaceID, id))
}

func (r inboxRepository) MarkConverted(ctx context.Context, id, convertedTo string) error {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	now := nowUnix()
	return (mobileSyncRepository{db: r.db}).withTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, "UPDATE inbox SET converted_to = ?, updated_at = ?, revision = revision + 1 WHERE workspace_id = ? AND id = ? AND converted_to IS NULL AND deleted_at IS NULL", convertedTo, now, workspaceID, id)
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
		if err := tx.QueryRowContext(ctx, `SELECT client_id FROM inbox WHERE workspace_id = ? AND id = ?`, workspaceID, id).Scan(&clientID); err != nil {
			return err
		}
		return persistSQLiteServerEntityChange(ctx, tx, workspaceID, uuid.NewString(), "inbox", "inbox.server_updated", clientID, now)
	})
}

func (r inboxRepository) Delete(ctx context.Context, id string) error {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	now := nowUnix()
	return (mobileSyncRepository{db: r.db}).withTx(ctx, func(tx *sql.Tx) error {
		var clientID string
		err := tx.QueryRowContext(ctx, `SELECT client_id FROM inbox WHERE workspace_id = ? AND id = ? AND deleted_at IS NULL`, workspaceID, id).Scan(&clientID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE inbox SET deleted_at = ?, updated_at = ?, revision = revision + 1 WHERE workspace_id = ? AND id = ? AND deleted_at IS NULL`, now, now, workspaceID, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO mobile_retired_ids (workspace_id, entity_type, client_id, retired_at) VALUES (?, 'inbox', ?, ?)`, workspaceID, clientID, now); err != nil {
			return err
		}
		return persistSQLiteServerEntityChange(ctx, tx, workspaceID, uuid.NewString(), "inbox", "inbox.server_deleted", clientID, now)
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
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]interface{}, len(ids)+2)
	args[0] = nowUnix()
	args[1] = workspaceID
	for i, id := range ids {
		args[i+2] = id
	}
	var affected int64
	err = (mobileSyncRepository{db: r.db}).withTx(ctx, func(tx *sql.Tx) error {
		clientIDs, err := sqliteInboxClientIDs(ctx, tx, workspaceID, placeholders, ids)
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, fmt.Sprintf("UPDATE inbox SET archived = 1, updated_at = ?, revision = revision + 1 WHERE workspace_id = ? AND deleted_at IS NULL AND id IN (%s)", placeholders), args...)
		if err != nil {
			return err
		}
		affected, err = result.RowsAffected()
		if err != nil {
			return err
		}
		for _, clientID := range clientIDs {
			if err := persistSQLiteServerEntityChange(ctx, tx, workspaceID, uuid.NewString(), "inbox", "inbox.server_updated", clientID, args[0].(int64)); err != nil {
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
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]interface{}, 0, len(ids)+1)
	args = append(args, workspaceID)
	for i, id := range ids {
		_ = i
		args = append(args, id)
	}
	now := nowUnix()
	var affected int64
	err = (mobileSyncRepository{db: r.db}).withTx(ctx, func(tx *sql.Tx) error {
		clientIDs, err := sqliteInboxClientIDs(ctx, tx, workspaceID, placeholders, ids)
		if err != nil {
			return err
		}
		updateArgs := make([]interface{}, 0, len(ids)+3)
		updateArgs = append(updateArgs, now, now, workspaceID)
		for _, id := range ids {
			updateArgs = append(updateArgs, id)
		}
		result, err := tx.ExecContext(ctx, fmt.Sprintf("UPDATE inbox SET deleted_at = ?, updated_at = ?, revision = revision + 1 WHERE workspace_id = ? AND deleted_at IS NULL AND id IN (%s)", placeholders), updateArgs...)
		if err != nil {
			return err
		}
		affected, err = result.RowsAffected()
		if err != nil {
			return err
		}
		for _, clientID := range clientIDs {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO mobile_retired_ids (workspace_id, entity_type, client_id, retired_at) VALUES (?, 'inbox', ?, ?)`, workspaceID, clientID, now); err != nil {
				return err
			}
			if err := persistSQLiteServerEntityChange(ctx, tx, workspaceID, uuid.NewString(), "inbox", "inbox.server_deleted", clientID, now); err != nil {
				return err
			}
		}
		return nil
	})
	return affected, err
}

func sqliteInboxClientIDs(ctx context.Context, tx *sql.Tx, workspaceID, placeholders string, ids []string) ([]string, error) {
	args := make([]interface{}, 0, len(ids)+1)
	args = append(args, workspaceID)
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := tx.QueryContext(ctx, fmt.Sprintf(`SELECT client_id FROM inbox WHERE workspace_id = ? AND deleted_at IS NULL AND id IN (%s) ORDER BY id`, placeholders), args...)
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

func scanSQLiteInboxItem(row sqliteRowScanner) (*model.InboxItem, error) {
	var item model.InboxItem
	if err := row.Scan(&item.ID, &item.Kind, &item.Title, &item.Body, &item.Source, &item.Archived, &item.ConvertedTo, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return nil, err
	}
	return &item, nil
}

func scanSQLiteInboxItems(rows *sql.Rows) ([]model.InboxItem, error) {
	items := make([]model.InboxItem, 0)
	for rows.Next() {
		item, err := scanSQLiteInboxItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}
