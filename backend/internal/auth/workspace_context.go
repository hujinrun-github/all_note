package auth

import (
	"context"
	"errors"
	"strings"
)

// ErrWorkspaceScopeMismatch means an authenticated identity and the selected
// workspace do not describe the same authorization boundary.
var ErrWorkspaceScopeMismatch = errors.New("workspace identity and scope do not match")

// WorkspaceContext is the authenticated application identity. It does not
// choose a database or infer authorization from an HTTP parameter.
type WorkspaceContext struct {
	WorkspaceID string
	ActorID     string
	SessionID   string
	DeviceID    string
	Role        string
}

// ResolveWorkspaceContext requires both a verified request identity and its
// explicit workspace scope. Callers must authenticate before using it.
func ResolveWorkspaceContext(ctx context.Context) (WorkspaceContext, error) {
	if ctx == nil {
		return WorkspaceContext{}, ErrMissingIdentity
	}
	identity, ok := IdentityFromContext(ctx)
	if !ok || strings.TrimSpace(identity.UserID) == "" {
		return WorkspaceContext{}, ErrMissingIdentity
	}
	workspaceID := strings.TrimSpace(identity.WorkspaceID)
	if workspaceID == "" {
		return WorkspaceContext{}, ErrMissingWorkspace
	}
	scope, ok := WorkspaceScopeFromContext(ctx)
	if !ok || strings.TrimSpace(scope.WorkspaceID) == "" {
		return WorkspaceContext{}, ErrMissingWorkspace
	}
	if workspaceID != strings.TrimSpace(scope.WorkspaceID) {
		return WorkspaceContext{}, ErrWorkspaceScopeMismatch
	}
	return WorkspaceContext{
		WorkspaceID: workspaceID,
		ActorID:     strings.TrimSpace(identity.UserID),
		SessionID:   strings.TrimSpace(identity.SessionID),
		DeviceID:    strings.TrimSpace(identity.DeviceID),
		Role:        strings.TrimSpace(identity.Role),
	}, nil
}

// WorkspaceScopeFromContext preserves the distinction between an absent
// scope and an explicitly supplied, malformed scope. Runtime resolvers use
// that distinction for internal jobs that have no HTTP identity.
func WorkspaceScopeFromContext(ctx context.Context) (WorkspaceScope, bool) {
	if ctx == nil {
		return WorkspaceScope{}, false
	}
	scope, ok := ctx.Value(workspaceScopeKey).(WorkspaceScope)
	return scope, ok
}
