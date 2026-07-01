package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

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
	where := "workspace_id = $1 AND archived = false AND converted_to IS NULL"
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
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO inbox (id, kind, title, body, source, archived, converted_to, payload, created_at, updated_at, workspace_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, '{}'::jsonb, $8, $9, $10)
	`, item.ID, item.Kind, item.Title, item.Body, item.Source, item.Archived == 1, item.ConvertedTo, unixToTime(item.CreatedAt), unixToTime(item.UpdatedAt), workspaceID)
	return err
}

func (r inboxRepository) GetByID(ctx context.Context, id string) (*model.InboxItem, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return scanPostgresInboxItem(r.db.QueryRowContext(ctx, `
		SELECT id, kind, title, body, source, archived, converted_to, created_at, updated_at
		FROM inbox WHERE workspace_id = $1 AND id = $2
	`, workspaceID, id))
}

func (r inboxRepository) MarkConverted(ctx context.Context, id, convertedTo string) error {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, `UPDATE inbox SET converted_to = $1, updated_at = $2 WHERE workspace_id = $3 AND id = $4 AND converted_to IS NULL`, convertedTo, time.Now().UTC(), workspaceID, id)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err == nil && affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (r inboxRepository) Delete(ctx context.Context, id string) error {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `DELETE FROM inbox WHERE workspace_id = $1 AND id = $2`, workspaceID, id)
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
	clause, err := pgInClause("id", 3, len(ids))
	if err != nil {
		return 0, err
	}
	args := make([]interface{}, 0, len(ids)+2)
	args = append(args, time.Now().UTC(), workspaceID)
	for _, id := range ids {
		args = append(args, id)
	}
	result, err := r.db.ExecContext(ctx, fmt.Sprintf("UPDATE inbox SET archived = true, updated_at = $1 WHERE workspace_id = $2 AND %s", clause), args...)
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
	clause, err := pgInClause("id", 2, len(ids))
	if err != nil {
		return 0, err
	}
	args := make([]interface{}, 0, len(ids)+1)
	args = append(args, workspaceID)
	for _, id := range ids {
		args = append(args, id)
	}
	result, err := r.db.ExecContext(ctx, fmt.Sprintf("DELETE FROM inbox WHERE workspace_id = $1 AND %s", clause), args...)
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
