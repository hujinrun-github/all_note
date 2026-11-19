// Package workspacecompat exposes explicitly named adapters for content whose
// authoritative source has not yet been migrated to the planning tenant.
package workspacecompat

import (
	"context"
	"errors"

	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/workspacequery"
)

type NotesSource struct{ store storage.Store }

func NewNotesSource(store storage.Store) *NotesSource { return &NotesSource{store: store} }

func (*NotesSource) Authority() string { return "platform_compatibility" }

func (s *NotesSource) Recent(ctx context.Context, workspaceID string, limit int) ([]workspacequery.NotePreview, error) {
	scope, err := auth.ResolveWorkspaceContext(ctx)
	if err != nil {
		return nil, err
	}
	if scope.WorkspaceID != workspaceID || limit < 1 || limit > workspacequery.MaxNotesLimit {
		return nil, workspacequery.ErrInvalidRequest
	}
	if s == nil || s.store == nil || s.store.Notes() == nil {
		return nil, errors.New("compatibility notes source is unavailable")
	}
	// Preserve the existing authority, while making the authorized scope explicit.
	ctx = auth.ContextWithWorkspaceScope(ctx, workspaceID)
	notes, err := s.store.Notes().Recent(ctx, limit)
	if err != nil {
		return nil, err
	}
	previews := make([]workspacequery.NotePreview, 0, len(notes))
	for _, note := range notes {
		previews = append(previews, workspacequery.NotePreview{ID: note.ID, Title: note.Title, UpdatedAt: note.UpdatedAt})
	}
	return previews, nil
}
