package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

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
	where := "workspace_id = ? AND archived = 0 AND converted_to IS NULL"
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
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO inbox (id, kind, title, body, source, archived, converted_to, created_at, updated_at, workspace_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, item.ID, item.Kind, item.Title, item.Body, item.Source, item.Archived, item.ConvertedTo, item.CreatedAt, item.UpdatedAt, workspaceID)
	return err
}

func (r inboxRepository) GetByID(ctx context.Context, id string) (*model.InboxItem, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return scanSQLiteInboxItem(r.db.QueryRowContext(ctx, `
		SELECT id, kind, title, body, source, archived, converted_to, created_at, updated_at
		FROM inbox WHERE workspace_id = ? AND id = ?
	`, workspaceID, id))
}

func (r inboxRepository) MarkConverted(ctx context.Context, id, convertedTo string) error {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, "UPDATE inbox SET converted_to = ?, updated_at = ? WHERE workspace_id = ? AND id = ? AND converted_to IS NULL", convertedTo, nowUnix(), workspaceID, id)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err == nil && affected != 1 {
		return sql.ErrNoRows
	}
	return err
}

func (r inboxRepository) Delete(ctx context.Context, id string) error {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, "DELETE FROM inbox WHERE workspace_id = ? AND id = ?", workspaceID, id)
	return err
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
	result, err := r.db.ExecContext(ctx, fmt.Sprintf("UPDATE inbox SET archived = 1, updated_at = ? WHERE workspace_id = ? AND id IN (%s)", placeholders), args...)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
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
	result, err := r.db.ExecContext(ctx, fmt.Sprintf("DELETE FROM inbox WHERE workspace_id = ? AND id IN (%s)", placeholders), args...)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
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
