package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/hujinrun/flowspace/internal/auth"
)

type mobileSyncPublisherRepository struct {
	db *sql.DB
}

func (r mobileSyncPublisherRepository) PublishNextWorkspace(ctx context.Context, limit int, now int64) (int, error) {
	var workspaceID string
	err := r.db.QueryRowContext(ctx, `
		SELECT workspace_id FROM mobile_sync_outbox
		WHERE published_at IS NULL
		GROUP BY workspace_id ORDER BY MIN(sequence) LIMIT 1
	`).Scan(&workspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return (mobileSyncRepository{db: r.db}).PublishPendingChanges(auth.ContextWithWorkspaceScope(ctx, workspaceID), limit, now)
}

func (r mobileSyncPublisherRepository) PruneExpired(ctx context.Context, cutoff int64) (int, error) {
	pruned := 0
	err := (mobileSyncRepository{db: r.db}).withTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT workspace_id, MAX(position) FROM mobile_sync_changes
			WHERE committed_at < ? GROUP BY workspace_id
		`, cutoff)
		if err != nil {
			return err
		}
		type boundary struct {
			workspaceID string
			position    int64
		}
		boundaries := make([]boundary, 0)
		for rows.Next() {
			var value boundary
			if err := rows.Scan(&value.workspaceID, &value.position); err != nil {
				_ = rows.Close()
				return err
			}
			boundaries = append(boundaries, value)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, value := range boundaries {
			result, err := tx.ExecContext(ctx, `DELETE FROM mobile_sync_changes WHERE workspace_id = ? AND position <= ?`, value.workspaceID, value.position)
			if err != nil {
				return err
			}
			count, err := result.RowsAffected()
			if err != nil {
				return err
			}
			pruned += int(count)
			if _, err := tx.ExecContext(ctx, `UPDATE mobile_sync_change_heads SET min_position = MAX(min_position, ?) WHERE workspace_id = ?`, value.position, value.workspaceID); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM mobile_sync_snapshot_sessions WHERE expires_at < ?`, cutoff); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM mobile_sync_outbox WHERE published_at IS NOT NULL AND published_at < ?`, cutoff)
		return err
	})
	return pruned, err
}
