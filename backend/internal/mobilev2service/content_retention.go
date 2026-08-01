package mobilev2service

import (
	"context"
	"time"

	"github.com/hujinrun/flowspace/internal/mobilev2projection"
	"github.com/hujinrun/flowspace/internal/storage"
)

const defaultDeletedContentRetention = 30 * 24 * time.Hour

type scopeCompaction struct {
	scope    string
	sequence int64
}

func maintainContentRetention(
	ctx context.Context,
	runner storage.TenantSQLRunner,
	dialect mobilev2projection.Dialect,
	workspaceID string,
	now time.Time,
	retention time.Duration,
) error {
	if retention <= 0 {
		retention = defaultDeletedContentRetention
	}
	now = now.UTC()
	cutoff := now.Add(-retention)
	if _, err := runner.ExecContext(ctx, bindContentSQL(dialect, `DELETE FROM mobile_v2_snapshot_sessions
		WHERE workspace_id=? AND expires_at<=?`), workspaceID, now); err != nil {
		return err
	}

	rows, err := runner.QueryContext(ctx, bindContentSQL(dialect, `SELECT scope,MAX(sequence)
		FROM mobile_v2_scope_change_batches
		WHERE workspace_id=? AND committed_at<?
		GROUP BY scope`), workspaceID, cutoff)
	if err != nil {
		return err
	}
	compactions := make([]scopeCompaction, 0, 4)
	for rows.Next() {
		var item scopeCompaction
		if err := rows.Scan(&item.scope, &item.sequence); err != nil {
			rows.Close()
			return err
		}
		compactions = append(compactions, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, item := range compactions {
		upsert := `INSERT INTO mobile_v2_scope_retention
			(workspace_id,scope,compacted_through_sequence,updated_at)
			VALUES (?,?,?,?)
			ON CONFLICT(workspace_id,scope) DO UPDATE SET
				compacted_through_sequence=MAX(mobile_v2_scope_retention.compacted_through_sequence,excluded.compacted_through_sequence),
				updated_at=excluded.updated_at`
		if dialect == mobilev2projection.DialectPostgres {
			upsert = `INSERT INTO mobile_v2_scope_retention
				(workspace_id,scope,compacted_through_sequence,updated_at)
				VALUES (?,?,?,?)
				ON CONFLICT(workspace_id,scope) DO UPDATE SET
					compacted_through_sequence=GREATEST(mobile_v2_scope_retention.compacted_through_sequence,excluded.compacted_through_sequence),
					updated_at=excluded.updated_at`
		}
		if _, err := runner.ExecContext(ctx, bindContentSQL(dialect, upsert), workspaceID, item.scope,
			item.sequence, contentTimestamp(dialect, now)); err != nil {
			return err
		}
		if _, err := runner.ExecContext(ctx, bindContentSQL(dialect, `DELETE FROM mobile_v2_scope_change_batches
			WHERE workspace_id=? AND scope=? AND sequence<=?`), workspaceID, item.scope, item.sequence); err != nil {
			return err
		}
	}
	if _, err := runner.ExecContext(ctx, bindContentSQL(dialect, `DELETE FROM mobile_v2_change_batches
		WHERE workspace_id=? AND committed_at<?`), workspaceID, cutoff); err != nil {
		return err
	}

	contentCutoff := contentTimestamp(dialect, cutoff)
	if _, err := runner.ExecContext(ctx, bindContentSQL(dialect, `DELETE FROM note_attachments
		WHERE workspace_id=? AND object_key='' AND EXISTS (
			SELECT 1 FROM notes n WHERE n.workspace_id=note_attachments.workspace_id
				AND n.id=note_attachments.note_id AND n.deleted_at IS NOT NULL AND n.deleted_at<?
		)`), workspaceID, contentCutoff); err != nil {
		return err
	}
	if _, err := runner.ExecContext(ctx, bindContentSQL(dialect, `DELETE FROM voice_notes
		WHERE workspace_id=? AND deleted_at IS NOT NULL AND deleted_at<?
			AND (object_key='' OR audio_state='deleted')
			AND EXISTS (
				SELECT 1 FROM mobile_v2_content_tombstones t
				WHERE t.workspace_id=voice_notes.workspace_id AND t.entity_type='voice_note' AND t.entity_id=voice_notes.id
			)`), workspaceID, contentCutoff); err != nil {
		return err
	}
	if _, err := runner.ExecContext(ctx, bindContentSQL(dialect, `DELETE FROM inbox
		WHERE workspace_id=? AND deleted_at IS NOT NULL AND deleted_at<?
			AND EXISTS (
				SELECT 1 FROM mobile_v2_content_tombstones t
				WHERE t.workspace_id=inbox.workspace_id AND t.entity_type='inbox' AND t.entity_id=inbox.id
			)`), workspaceID, contentCutoff); err != nil {
		return err
	}
	if _, err := runner.ExecContext(ctx, bindContentSQL(dialect, `DELETE FROM notes
		WHERE workspace_id=? AND deleted_at IS NOT NULL AND deleted_at<?
			AND EXISTS (
				SELECT 1 FROM mobile_v2_content_tombstones t
				WHERE t.workspace_id=notes.workspace_id AND t.entity_type='note' AND t.entity_id=notes.id
			)
			AND NOT EXISTS (SELECT 1 FROM voice_notes v WHERE v.workspace_id=notes.workspace_id AND v.note_id=notes.id)
			AND NOT EXISTS (SELECT 1 FROM note_attachments a WHERE a.workspace_id=notes.workspace_id AND a.note_id=notes.id)
			AND NOT EXISTS (SELECT 1 FROM tasks lt WHERE lt.workspace_id=notes.workspace_id AND lt.note_id=notes.id)
			AND NOT EXISTS (SELECT 1 FROM domain_tasks_v2 dt WHERE dt.workspace_id=notes.workspace_id AND dt.note_id=notes.id)
			AND NOT EXISTS (SELECT 1 FROM domain_task_occurrences_v2 o WHERE o.workspace_id=notes.workspace_id AND o.note_id=notes.id)
		`), workspaceID, contentCutoff); err != nil {
		return err
	}
	if _, err := runner.ExecContext(ctx, bindContentSQL(dialect, `DELETE FROM voice_audio_cleanup_jobs
		WHERE workspace_id=? AND state='completed' AND updated_at<?`), workspaceID, cutoff.Unix()); err != nil {
		return err
	}
	return nil
}
