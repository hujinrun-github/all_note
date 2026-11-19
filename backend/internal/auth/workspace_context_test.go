package auth

import (
	"context"
	"errors"
	"testing"
)

func TestResolveWorkspaceContextCombinesVerifiedIdentityAndScope(t *testing.T) {
	ctx := ContextWithIdentity(t.Context(), RequestIdentity{
		UserID: " user-a ", WorkspaceID: " workspace-a ", SessionID: " session-a ",
		DeviceID: " device-a ", Role: " member ",
	})
	ctx = ContextWithWorkspaceScope(ctx, "workspace-a")
	got, err := ResolveWorkspaceContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := WorkspaceContext{
		WorkspaceID: "workspace-a", ActorID: "user-a", SessionID: "session-a", DeviceID: "device-a", Role: "member",
	}
	if got != want {
		t.Fatalf("workspace context = %#v, want %#v", got, want)
	}
}

func TestResolveWorkspaceContextFailsClosed(t *testing.T) {
	identity := RequestIdentity{UserID: "user-a", WorkspaceID: "workspace-a"}
	tests := []struct {
		name string
		ctx  context.Context
		want error
	}{
		{name: "nil context", want: ErrMissingIdentity},
		{name: "missing identity", ctx: ContextWithWorkspaceScope(t.Context(), "workspace-a"), want: ErrMissingIdentity},
		{name: "missing actor", ctx: ContextWithIdentity(t.Context(), RequestIdentity{WorkspaceID: "workspace-a"}), want: ErrMissingIdentity},
		{name: "blank actor", ctx: ContextWithIdentity(t.Context(), RequestIdentity{UserID: " \t", WorkspaceID: "workspace-a"}), want: ErrMissingIdentity},
		{name: "missing identity workspace", ctx: ContextWithIdentity(t.Context(), RequestIdentity{UserID: "user-a"}), want: ErrMissingWorkspace},
		{name: "blank identity workspace", ctx: ContextWithIdentity(t.Context(), RequestIdentity{UserID: "user-a", WorkspaceID: " \t"}), want: ErrMissingWorkspace},
		{name: "missing scope", ctx: ContextWithIdentity(t.Context(), identity), want: ErrMissingWorkspace},
		{name: "blank scope", ctx: ContextWithWorkspaceScope(ContextWithIdentity(t.Context(), identity), " \t"), want: ErrMissingWorkspace},
		{name: "different scope", ctx: ContextWithWorkspaceScope(ContextWithIdentity(t.Context(), identity), "workspace-b"), want: ErrWorkspaceScopeMismatch},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ResolveWorkspaceContext(test.ctx)
			if !errors.Is(err, test.want) || got != (WorkspaceContext{}) {
				t.Fatalf("context = %#v, error = %v, want %v", got, err, test.want)
			}
		})
	}
}

func TestWorkspaceScopeFromContextDistinguishesAbsentFromBlank(t *testing.T) {
	if _, ok := WorkspaceScopeFromContext(t.Context()); ok {
		t.Fatal("absent scope reported present")
	}
	got, ok := WorkspaceScopeFromContext(ContextWithWorkspaceScope(t.Context(), ""))
	if !ok || got.WorkspaceID != "" {
		t.Fatalf("explicit blank scope = %#v, present=%v", got, ok)
	}
}
