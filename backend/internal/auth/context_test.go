package auth

import (
	"context"
	"errors"
	"testing"
)

func TestIdentityAndWorkspaceScopeAreSeparate(t *testing.T) {
	base := context.Background()
	identity := RequestIdentity{
		UserID:             "user_admin",
		SessionID:          "session_admin",
		WorkspaceID:        "workspace_admin",
		Role:               "admin",
		MustChangePassword: false,
	}

	ctx := ContextWithIdentity(base, identity)
	ctx = ContextWithWorkspaceScope(ctx, "workspace_target")

	gotIdentity, ok := IdentityFromContext(ctx)
	if !ok {
		t.Fatal("expected identity in context")
	}
	if gotIdentity.WorkspaceID != "workspace_admin" {
		t.Fatalf("identity workspace = %q, want workspace_admin", gotIdentity.WorkspaceID)
	}
	if gotIdentity.SessionID != "session_admin" {
		t.Fatalf("identity session = %q, want session_admin", gotIdentity.SessionID)
	}

	scope, err := WorkspaceIDFromContext(ctx)
	if err != nil {
		t.Fatalf("workspace scope: %v", err)
	}
	if scope != "workspace_target" {
		t.Fatalf("scope = %q, want workspace_target", scope)
	}
}

func TestWorkspaceIDFromContextMissing(t *testing.T) {
	_, err := WorkspaceIDFromContext(context.Background())
	if !errors.Is(err, ErrMissingWorkspace) {
		t.Fatalf("expected ErrMissingWorkspace, got %v", err)
	}
}
