package sqlite

import (
	"context"
	"database/sql"
	"strings"

	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/model"
)

type noteAttachmentRepository struct {
	db sqliteRunner
}

func (r noteAttachmentRepository) ListByNote(ctx context.Context, noteID string) ([]model.NoteAttachment, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT a.id, a.workspace_id, a.note_id, a.kind, a.original_name,
		       a.mime_type, a.size_bytes, a.sha256, a.object_key, a.created_at,
		       'upload' AS source
		FROM note_attachments a
		JOIN notes n ON n.workspace_id = a.workspace_id AND n.id = a.note_id
		WHERE a.workspace_id = ? AND a.note_id = ? AND n.deleted_at IS NULL
		ORDER BY a.created_at ASC
	`, workspaceID, strings.TrimSpace(noteID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]model.NoteAttachment, 0)
	for rows.Next() {
		attachment, err := scanSQLiteNoteAttachment(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *attachment)
	}
	return result, rows.Err()
}

func (r noteAttachmentRepository) GetByNoteAndID(ctx context.Context, noteID, attachmentID string) (*model.NoteAttachment, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return scanSQLiteNoteAttachment(r.db.QueryRowContext(ctx, `
		SELECT a.id, a.workspace_id, a.note_id, a.kind, a.original_name,
		       a.mime_type, a.size_bytes, a.sha256, a.object_key, a.created_at,
		       'upload' AS source
		FROM note_attachments a
		JOIN notes n ON n.workspace_id = a.workspace_id AND n.id = a.note_id
		WHERE a.workspace_id = ? AND a.note_id = ? AND a.id = ?
		  AND n.deleted_at IS NULL
	`, workspaceID, strings.TrimSpace(noteID), strings.TrimSpace(attachmentID)))
}

func (r noteAttachmentRepository) Create(ctx context.Context, attachment *model.NoteAttachment) error {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO note_attachments (
			id, workspace_id, note_id, kind, original_name, mime_type,
			size_bytes, sha256, object_key, created_at
		)
		SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
		WHERE EXISTS (
			SELECT 1 FROM notes
			WHERE workspace_id = ? AND id = ? AND deleted_at IS NULL
		)
	`, attachment.ID, workspaceID, attachment.NoteID, attachment.Kind,
		attachment.OriginalName, attachment.MimeType, attachment.SizeBytes,
		attachment.SHA256, attachment.ObjectKey, attachment.CreatedAt,
		workspaceID, attachment.NoteID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return sql.ErrNoRows
	}
	attachment.WorkspaceID = workspaceID
	return nil
}

func (r noteAttachmentRepository) Delete(ctx context.Context, noteID, attachmentID string) error {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, `
		DELETE FROM note_attachments
		WHERE workspace_id = ? AND note_id = ? AND id = ?
	`, workspaceID, strings.TrimSpace(noteID), strings.TrimSpace(attachmentID))
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func scanSQLiteNoteAttachment(scanner sqliteScanner) (*model.NoteAttachment, error) {
	var attachment model.NoteAttachment
	if err := scanner.Scan(
		&attachment.ID, &attachment.WorkspaceID, &attachment.NoteID,
		&attachment.Kind, &attachment.OriginalName, &attachment.MimeType,
		&attachment.SizeBytes, &attachment.SHA256, &attachment.ObjectKey,
		&attachment.CreatedAt, &attachment.Source,
	); err != nil {
		return nil, err
	}
	attachment.Deletable = true
	return &attachment, nil
}
