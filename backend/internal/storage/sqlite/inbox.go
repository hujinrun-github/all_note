package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/hujinrun/flowspace/internal/model"
)

type inboxRepository struct {
	db sqliteRunner
}

func (r inboxRepository) List(ctx context.Context, kind string, page, pageSize int) ([]model.InboxItem, int, error) {
	where := "archived = 0 AND converted_to IS NULL"
	args := []interface{}{}
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
	item.ID = newID()
	now := nowUnix()
	item.CreatedAt = now
	item.UpdatedAt = now
	if item.Source == "" {
		item.Source = "quick-capture"
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO inbox (id, kind, title, body, source, archived, converted_to, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, item.ID, item.Kind, item.Title, item.Body, item.Source, item.Archived, item.ConvertedTo, item.CreatedAt, item.UpdatedAt)
	return err
}

func (r inboxRepository) GetByID(ctx context.Context, id string) (*model.InboxItem, error) {
	return scanSQLiteInboxItem(r.db.QueryRowContext(ctx, `
		SELECT id, kind, title, body, source, archived, converted_to, created_at, updated_at
		FROM inbox WHERE id = ?
	`, id))
}

func (r inboxRepository) MarkConverted(ctx context.Context, id, convertedTo string) error {
	_, err := r.db.ExecContext(ctx, "UPDATE inbox SET converted_to = ?, updated_at = ? WHERE id = ?", convertedTo, nowUnix(), id)
	return err
}

func (r inboxRepository) Delete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, "DELETE FROM inbox WHERE id = ?", id)
	return err
}

func (r inboxRepository) BatchArchive(ctx context.Context, ids []string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]interface{}, len(ids)+1)
	args[0] = nowUnix()
	for i, id := range ids {
		args[i+1] = id
	}
	result, err := r.db.ExecContext(ctx, fmt.Sprintf("UPDATE inbox SET archived = 1, updated_at = ? WHERE id IN (%s)", placeholders), args...)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (r inboxRepository) BatchDelete(ctx context.Context, ids []string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	result, err := r.db.ExecContext(ctx, fmt.Sprintf("DELETE FROM inbox WHERE id IN (%s)", placeholders), args...)
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
