package repository

import (
	"fmt"
	"strings"

	"github.com/hujinrun/flowspace/internal/model"
)

func GetInboxItems(kind string, page, pageSize int) ([]model.InboxItem, int, error) {
	where := "archived = 0 AND converted_to IS NULL"
	args := []interface{}{}
	if kind != "" && kind != "all" {
		where += " AND kind = ?"
		args = append(args, kind)
	}

	var total int
	DB.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM inbox WHERE %s", where), args...).Scan(&total)

	offset := (page - 1) * pageSize
	rows, err := DB.Query(fmt.Sprintf(`
		SELECT id, kind, title, body, source, archived, converted_to, created_at, updated_at
		FROM inbox WHERE %s ORDER BY created_at DESC LIMIT ? OFFSET ?
	`, where), append(args, pageSize, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var items []model.InboxItem
	for rows.Next() {
		var it model.InboxItem
		if err := rows.Scan(&it.ID, &it.Kind, &it.Title, &it.Body, &it.Source, &it.Archived, &it.ConvertedTo, &it.CreatedAt, &it.UpdatedAt); err != nil {
			return nil, 0, err
		}
		items = append(items, it)
	}
	return items, total, nil
}

func CreateInboxItem(it *model.InboxItem) error {
	it.ID = newUUID()
	now := nowUnix()
	it.CreatedAt = now
	it.UpdatedAt = now
	if it.Source == "" {
		it.Source = "quick-capture"
	}
	_, err := DB.Exec(`
		INSERT INTO inbox (id, kind, title, body, source, archived, converted_to, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, it.ID, it.Kind, it.Title, it.Body, it.Source, it.Archived, it.ConvertedTo, it.CreatedAt, it.UpdatedAt)
	return err
}

func GetInboxItemByID(id string) (*model.InboxItem, error) {
	var it model.InboxItem
	err := DB.QueryRow(`
		SELECT id, kind, title, body, source, archived, converted_to, created_at, updated_at
		FROM inbox WHERE id = ?
	`, id).Scan(&it.ID, &it.Kind, &it.Title, &it.Body, &it.Source, &it.Archived, &it.ConvertedTo, &it.CreatedAt, &it.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &it, nil
}

func MarkInboxConverted(id, convertedTo string) error {
	_, err := DB.Exec("UPDATE inbox SET converted_to = ?, updated_at = ? WHERE id = ?", convertedTo, nowUnix(), id)
	return err
}

func DeleteInboxItem(id string) error {
	_, err := DB.Exec("DELETE FROM inbox WHERE id = ?", id)
	return err
}

func BatchArchiveInbox(ids []string) (int64, error) {
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]interface{}, len(ids)+1)
	args[0] = nowUnix()
	for i, id := range ids {
		args[i+1] = id
	}
	result, err := DB.Exec(fmt.Sprintf("UPDATE inbox SET archived = 1, updated_at = ? WHERE id IN (%s)", placeholders), args...)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func BatchDeleteInbox(ids []string) (int64, error) {
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	result, err := DB.Exec(fmt.Sprintf("DELETE FROM inbox WHERE id IN (%s)", placeholders), args...)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
