package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/hujinrun/flowspace/internal/model"
)

type inboxRepository struct {
	db postgresRunner
}

func (r inboxRepository) List(ctx context.Context, kind string, page, pageSize int) ([]model.InboxItem, int, error) {
	where := "archived = false AND converted_to IS NULL"
	args := []interface{}{}
	if kind != "" && kind != "all" {
		where += " AND kind = $1"
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
	item.ID = newID()
	now := nowUnix()
	item.CreatedAt = now
	item.UpdatedAt = now
	if item.Source == "" {
		item.Source = "quick-capture"
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO inbox (id, kind, title, body, source, archived, converted_to, payload, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, '{}'::jsonb, $8, $9)
	`, item.ID, item.Kind, item.Title, item.Body, item.Source, item.Archived == 1, item.ConvertedTo, unixToTime(item.CreatedAt), unixToTime(item.UpdatedAt))
	return err
}

func (r inboxRepository) GetByID(ctx context.Context, id string) (*model.InboxItem, error) {
	return scanPostgresInboxItem(r.db.QueryRowContext(ctx, `
		SELECT id, kind, title, body, source, archived, converted_to, created_at, updated_at
		FROM inbox WHERE id = $1
	`, id))
}

func (r inboxRepository) MarkConverted(ctx context.Context, id, convertedTo string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE inbox SET converted_to = $1, updated_at = $2 WHERE id = $3`, convertedTo, time.Now().UTC(), id)
	return err
}

func (r inboxRepository) Delete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM inbox WHERE id = $1`, id)
	return err
}

func (r inboxRepository) BatchArchive(ctx context.Context, ids []string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	clause, err := pgInClause("id", 2, len(ids))
	if err != nil {
		return 0, err
	}
	args := make([]interface{}, 0, len(ids)+1)
	args = append(args, time.Now().UTC())
	for _, id := range ids {
		args = append(args, id)
	}
	result, err := r.db.ExecContext(ctx, fmt.Sprintf("UPDATE inbox SET archived = true, updated_at = $1 WHERE %s", clause), args...)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (r inboxRepository) BatchDelete(ctx context.Context, ids []string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	clause, err := pgInClause("id", 1, len(ids))
	if err != nil {
		return 0, err
	}
	args := make([]interface{}, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	result, err := r.db.ExecContext(ctx, fmt.Sprintf("DELETE FROM inbox WHERE %s", clause), args...)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
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
