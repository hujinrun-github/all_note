package auth

import "context"

type contextKey string

const (
	identityKey       contextKey = "flowspace.identity"
	workspaceScopeKey contextKey = "flowspace.workspace_scope"
)

type RequestIdentity struct {
	UserID             string
	SessionID          string
	WorkspaceID        string
	Role               string
	MustChangePassword bool
}

type WorkspaceScope struct {
	WorkspaceID string
}

func ContextWithIdentity(ctx context.Context, identity RequestIdentity) context.Context {
	return context.WithValue(ctx, identityKey, identity)
}

func IdentityFromContext(ctx context.Context) (RequestIdentity, bool) {
	identity, ok := ctx.Value(identityKey).(RequestIdentity)
	return identity, ok
}

func SessionIDFromContext(ctx context.Context) (string, bool) {
	identity, ok := IdentityFromContext(ctx)
	if !ok || identity.SessionID == "" {
		return "", false
	}
	return identity.SessionID, true
}

func ContextWithWorkspaceScope(ctx context.Context, workspaceID string) context.Context {
	return context.WithValue(ctx, workspaceScopeKey, WorkspaceScope{WorkspaceID: workspaceID})
}

func WorkspaceIDFromContext(ctx context.Context) (string, error) {
	scope, ok := ctx.Value(workspaceScopeKey).(WorkspaceScope)
	if !ok || scope.WorkspaceID == "" {
		return "", ErrMissingWorkspace
	}
	return scope.WorkspaceID, nil
}
