package contracttest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

func RunAuthContractTests(t *testing.T, factory StoreFactory) {
	t.Helper()

	t.Run("CreateUserWorkspaceMembershipInOneTransaction", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctx := scopedContractContext(t, store)
		user := contractUser("auth_user_contract", "Contract.User@Example.com", "Contract User", "user")
		workspace := contractWorkspace("auth_workspace_contract", user.ID, "Contract Workspace")
		user.DefaultWorkspaceID = workspace.ID

		err := store.Transact(ctx, func(tx storage.Store) error {
			if err := tx.Auth().CreateUser(ctx, user); err != nil {
				return err
			}
			if err := tx.Auth().CreateWorkspace(ctx, workspace); err != nil {
				return err
			}
			return tx.Auth().AddWorkspaceMember(ctx, workspace.ID, user.ID, "owner")
		})
		if err != nil {
			t.Fatalf("provision user workspace: %v", err)
		}

		loaded, err := store.Auth().GetUserByEmail(ctx, "contract.user@example.com")
		if err != nil {
			t.Fatalf("get user by lowercased email: %v", err)
		}
		if loaded.ID != user.ID {
			t.Fatalf("loaded user id = %q, want %q", loaded.ID, user.ID)
		}
		if loaded.DefaultWorkspaceID != workspace.ID {
			t.Fatalf("default workspace = %q, want %q", loaded.DefaultWorkspaceID, workspace.ID)
		}

		member, err := store.Auth().GetWorkspaceMembership(ctx, workspace.ID, user.ID)
		if err != nil {
			t.Fatalf("get workspace membership: %v", err)
		}
		if member.Role != "owner" {
			t.Fatalf("membership role = %q, want owner", member.Role)
		}
	})

	t.Run("CreateUserPersistsPasswordSet", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctx := scopedContractContext(t, store)
		user := contractUser("auth_user_password_set", "password-set@example.com", "Password Set", "user")
		user.PasswordHash = ""
		user.PasswordSet = false
		workspace := contractWorkspace("auth_workspace_password_set", user.ID, "Password Set Workspace")

		err := store.Transact(ctx, func(tx storage.Store) error {
			if err := tx.Auth().CreateUser(ctx, user); err != nil {
				return err
			}
			if err := tx.Auth().CreateWorkspace(ctx, workspace); err != nil {
				return err
			}
			if err := tx.Auth().SetDefaultWorkspace(ctx, user.ID, workspace.ID); err != nil {
				return err
			}
			return tx.Auth().AddWorkspaceMember(ctx, workspace.ID, user.ID, "owner")
		})
		if err != nil {
			t.Fatalf("create user with password_set=false: %v", err)
		}

		loaded, err := store.Auth().GetUserByEmail(ctx, "password-set@example.com")
		if err != nil {
			t.Fatalf("get user by email: %v", err)
		}
		if loaded.PasswordSet {
			t.Fatalf("password_set = true, want false: %+v", loaded)
		}
	})

	t.Run("AuthIdentityCreateGetUpdateAndList", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctx := scopedContractContext(t, store)
		seedAuthUserWorkspace(t, ctx, store, "auth_user_identity", "auth_workspace_identity", "identity@example.com")

		createdAt := time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC).Unix()
		avatarURL := "https://avatars.githubusercontent.com/u/12345"
		identity := &model.AuthIdentity{
			ID:             "auth_identity_github",
			UserID:         "auth_user_identity",
			Provider:       "github",
			ProviderUserID: "12345",
			ProviderLogin:  "octocat",
			Email:          "Octo@Example.com",
			AvatarURL:      &avatarURL,
			CreatedAt:      createdAt,
			UpdatedAt:      createdAt,
		}
		if err := store.Auth().CreateAuthIdentity(ctx, identity); err != nil {
			t.Fatalf("create auth identity: %v", err)
		}

		loaded, err := store.Auth().GetAuthIdentity(ctx, "github", "12345")
		if err != nil {
			t.Fatalf("get auth identity: %v", err)
		}
		assertAuthIdentityMatches(t, loaded, identity)
		if loaded.LastLoginAt != nil {
			t.Fatalf("last_login_at = %v, want nil", *loaded.LastLoginAt)
		}

		updatedAvatarURL := "https://avatars.githubusercontent.com/u/12345?v=2"
		loginAt := time.Date(2026, 7, 6, 11, 30, 0, 0, time.UTC)
		identity.ProviderLogin = "octocat-renamed"
		identity.Email = "octo.renamed@example.com"
		identity.AvatarURL = &updatedAvatarURL
		if err := store.Auth().UpdateAuthIdentityFromProvider(ctx, identity, loginAt); err != nil {
			t.Fatalf("update auth identity from provider: %v", err)
		}

		updated, err := store.Auth().GetAuthIdentity(ctx, "github", "12345")
		if err != nil {
			t.Fatalf("get updated auth identity: %v", err)
		}
		assertAuthIdentityMatches(t, updated, identity)
		if updated.LastLoginAt == nil || *updated.LastLoginAt != loginAt.Unix() {
			t.Fatalf("last_login_at = %v, want %d", updated.LastLoginAt, loginAt.Unix())
		}
		if updated.UpdatedAt != loginAt.Unix() {
			t.Fatalf("updated_at = %d, want %d", updated.UpdatedAt, loginAt.Unix())
		}

		identities, err := store.Auth().ListAuthIdentitiesByUser(ctx, "auth_user_identity")
		if err != nil {
			t.Fatalf("list auth identities by user: %v", err)
		}
		if len(identities) != 1 {
			t.Fatalf("listed %d auth identities, want 1: %+v", len(identities), identities)
		}
		assertAuthIdentityMatches(t, &identities[0], identity)
	})

	t.Run("CreateSessionAndGetSessionByTokenHashOnlyReturnActiveSessions", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctx := scopedContractContext(t, store)
		seedAuthUserWorkspace(t, ctx, store, "auth_user_sessions", "auth_workspace_sessions", "sessions@example.com")

		now := time.Now().UTC()
		sessions := []model.Session{
			{ID: "auth_session_active", UserID: "auth_user_sessions", WorkspaceID: "auth_workspace_sessions", TokenHash: "auth_active_hash", ExpiresAt: now.Add(time.Hour)},
			{ID: "auth_session_expired", UserID: "auth_user_sessions", WorkspaceID: "auth_workspace_sessions", TokenHash: "auth_expired_hash", ExpiresAt: now.Add(-time.Minute)},
			{ID: "auth_session_revoked", UserID: "auth_user_sessions", WorkspaceID: "auth_workspace_sessions", TokenHash: "auth_revoked_hash", ExpiresAt: now.Add(time.Hour)},
		}
		for i := range sessions {
			if err := store.Auth().CreateSession(ctx, &sessions[i]); err != nil {
				t.Fatalf("create session %s: %v", sessions[i].ID, err)
			}
		}
		if err := store.Auth().RevokeSession(ctx, "auth_session_revoked"); err != nil {
			t.Fatalf("revoke session: %v", err)
		}

		active, err := store.Auth().GetSessionByTokenHash(ctx, "auth_active_hash")
		if err != nil {
			t.Fatalf("active session lookup: %v", err)
		}
		if active.ID != "auth_session_active" || active.UserID != "auth_user_sessions" {
			t.Fatalf("unexpected active session: %+v", active)
		}
		expectSessionMissing(t, store, ctx, "auth_expired_hash")
		expectSessionMissing(t, store, ctx, "auth_revoked_hash")
	})

	t.Run("CreateAndUpdateUserReturnEmailAlreadyExists", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctx := scopedContractContext(t, store)
		seedAuthUserWorkspace(t, ctx, store, "auth_user_email_existing", "auth_workspace_email_existing", "existing@example.com")

		duplicate := contractUser("auth_user_email_duplicate", "Existing@Example.com", "Duplicate User", "user")
		if err := store.Auth().CreateUser(ctx, duplicate); !errors.Is(err, auth.ErrEmailAlreadyExists) {
			t.Fatalf("duplicate create error = %v, want ErrEmailAlreadyExists", err)
		}

		seedAuthUserWorkspace(t, ctx, store, "auth_user_email_update", "auth_workspace_email_update", "update-email@example.com")
		email := "existing@example.com"
		if _, err := store.Auth().UpdateUser(ctx, "auth_user_email_update", &model.UpdateUserRequest{Email: &email}); !errors.Is(err, auth.ErrEmailAlreadyExists) {
			t.Fatalf("duplicate update error = %v, want ErrEmailAlreadyExists", err)
		}
	})

	t.Run("RevokeUserSessionsExceptRevokesAllButCurrentSession", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctx := scopedContractContext(t, store)
		seedAuthUserWorkspace(t, ctx, store, "auth_user_revoke_except", "auth_workspace_revoke_except", "revoke-except@example.com")

		now := time.Now().UTC()
		for _, session := range []model.Session{
			{ID: "auth_session_keep", UserID: "auth_user_revoke_except", WorkspaceID: "auth_workspace_revoke_except", TokenHash: "auth_keep_hash", ExpiresAt: now.Add(time.Hour)},
			{ID: "auth_session_revoke", UserID: "auth_user_revoke_except", WorkspaceID: "auth_workspace_revoke_except", TokenHash: "auth_revoke_hash", ExpiresAt: now.Add(time.Hour)},
		} {
			session := session
			if err := store.Auth().CreateSession(ctx, &session); err != nil {
				t.Fatalf("create session %s: %v", session.ID, err)
			}
		}
		if err := store.Auth().RevokeUserSessionsExcept(ctx, "auth_user_revoke_except", "auth_session_keep"); err != nil {
			t.Fatalf("revoke except: %v", err)
		}
		if _, err := store.Auth().GetSessionByTokenHash(ctx, "auth_keep_hash"); err != nil {
			t.Fatalf("kept session lookup: %v", err)
		}
		expectSessionMissing(t, store, ctx, "auth_revoke_hash")
	})

	t.Run("RevokeUserSessionsRevokesEveryActiveSession", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctx := scopedContractContext(t, store)
		seedAuthUserWorkspace(t, ctx, store, "auth_user_revoke_all", "auth_workspace_revoke_all", "revoke-all@example.com")

		now := time.Now().UTC()
		for _, session := range []model.Session{
			{ID: "auth_session_revoke_all_1", UserID: "auth_user_revoke_all", WorkspaceID: "auth_workspace_revoke_all", TokenHash: "auth_revoke_all_hash_1", ExpiresAt: now.Add(time.Hour)},
			{ID: "auth_session_revoke_all_2", UserID: "auth_user_revoke_all", WorkspaceID: "auth_workspace_revoke_all", TokenHash: "auth_revoke_all_hash_2", ExpiresAt: now.Add(time.Hour)},
		} {
			session := session
			if err := store.Auth().CreateSession(ctx, &session); err != nil {
				t.Fatalf("create session %s: %v", session.ID, err)
			}
		}
		if err := store.Auth().RevokeUserSessions(ctx, "auth_user_revoke_all"); err != nil {
			t.Fatalf("revoke all user sessions: %v", err)
		}
		expectSessionMissing(t, store, ctx, "auth_revoke_all_hash_1")
		expectSessionMissing(t, store, ctx, "auth_revoke_all_hash_2")
	})

	t.Run("RecordAuditEventStoresJSONMetadata", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctx := scopedContractContext(t, store)
		seedAuthUserWorkspace(t, ctx, store, "auth_user_audit", "auth_workspace_audit", "audit@example.com")
		userID := "auth_user_audit"
		workspaceID := "auth_workspace_audit"
		event := &model.AuditEvent{
			ID:          "auth_audit_event",
			ActorUserID: &userID,
			WorkspaceID: &workspaceID,
			Action:      "auth.contract",
			Metadata: map[string]any{
				"reason": "contract",
				"count":  float64(2),
				"nested": map[string]any{"ok": true},
			},
		}
		if err := store.Auth().RecordAuditEvent(ctx, event); err != nil {
			t.Fatalf("record audit event: %v", err)
		}
	})

	t.Run("RecordAuditEventSanitizesMetadataAtStorageBoundary", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctx := scopedContractContext(t, store)
		seedAuthUserWorkspace(t, ctx, store, "auth_user_audit_sanitized", "auth_workspace_audit_sanitized", "audit-sanitized@example.com")
		userID := "auth_user_audit_sanitized"
		workspaceID := "auth_workspace_audit_sanitized"
		event := &model.AuditEvent{
			ID:          "auth_audit_sanitized_event",
			ActorUserID: &userID,
			WorkspaceID: &workspaceID,
			Action:      "auth.contract.sanitized",
			Metadata: map[string]any{
				"safe":    "kept",
				"api_key": "api-secret",
				"nested": map[string]any{
					"minio_secret_key": "minio-secret",
					"safe_nested":      "kept-nested",
				},
				"items": []any{
					map[string]any{"access_key": "access-secret", "name": "kept-item"},
				},
			},
		}
		if err := store.Auth().RecordAuditEvent(ctx, event); err != nil {
			t.Fatalf("record audit event: %v", err)
		}

		metadata := loadContractAuditMetadata(t, ctx, store, event.ID)
		raw := marshalContractAuditMetadata(t, metadata)
		for _, forbidden := range []string{"api-secret", "minio-secret", "access-secret", "api_key", "minio_secret_key", "access_key"} {
			if strings.Contains(raw, forbidden) {
				t.Fatalf("stored audit metadata contains forbidden value %q: %s", forbidden, raw)
			}
		}
		for _, want := range []string{"kept", "kept-nested", "kept-item"} {
			if !strings.Contains(raw, want) {
				t.Fatalf("stored audit metadata missing safe value %q: %s", want, raw)
			}
		}
	})

	t.Run("ListUsersSupportsQueryFilter", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctx := scopedContractContext(t, store)
		seedAuthUserWorkspace(t, ctx, store, "auth_user_query_ada", "auth_workspace_query_ada", "ada@example.com", withDisplayName("Ada Lovelace"))
		seedAuthUserWorkspace(t, ctx, store, "auth_user_query_grace", "auth_workspace_query_grace", "grace@example.com", withDisplayName("Grace Hopper"))

		users, total, err := store.Auth().ListUsers(ctx, storage.UserListFilter{
			Query:    "lovelace",
			Page:     1,
			PageSize: 10,
		})
		if err != nil {
			t.Fatalf("list users with query: %v", err)
		}
		if total != 1 || len(users) != 1 {
			t.Fatalf("list users total=%d len=%d users=%+v, want one", total, len(users), users)
		}
		if users[0].ID != "auth_user_query_ada" {
			t.Fatalf("query returned user %q, want auth_user_query_ada", users[0].ID)
		}
	})

	t.Run("UpdateUserFieldsLoginAndPassword", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctx := scopedContractContext(t, store)
		seedAuthUserWorkspace(t, ctx, store, "auth_user_update", "auth_workspace_update", "update@example.com")

		email := "Updated.User@Example.com"
		displayName := "Updated User"
		role := "admin"
		updated, err := store.Auth().UpdateUser(ctx, "auth_user_update", &model.UpdateUserRequest{
			Email:       &email,
			DisplayName: &displayName,
			Role:        &role,
		})
		if err != nil {
			t.Fatalf("update user: %v", err)
		}
		if updated.Email != email || updated.DisplayName != displayName || updated.Role != role {
			t.Fatalf("unexpected updated user: %+v", updated)
		}
		statusUpdated, err := store.Auth().UpdateUserStatus(ctx, "auth_user_update", "disabled")
		if err != nil {
			t.Fatalf("update user status: %v", err)
		}
		if statusUpdated.Status != "disabled" {
			t.Fatalf("status = %q, want disabled", statusUpdated.Status)
		}

		loginAt := time.Now().UTC().Add(-time.Minute)
		if err := store.Auth().UpdateUserLastLogin(ctx, "auth_user_update", loginAt); err != nil {
			t.Fatalf("update last login: %v", err)
		}
		if err := store.Auth().UpdateUserPassword(ctx, "auth_user_update", "new-hash", true); err != nil {
			t.Fatalf("update password: %v", err)
		}
		if err := store.Auth().SetDefaultWorkspace(ctx, "auth_user_update", "auth_workspace_update"); err != nil {
			t.Fatalf("set default workspace: %v", err)
		}

		loaded, err := store.Auth().GetUserByID(ctx, "auth_user_update")
		if err != nil {
			t.Fatalf("get user by id: %v", err)
		}
		if loaded.PasswordHash != "new-hash" || !loaded.MustChangePassword {
			t.Fatalf("unexpected password fields: %+v", loaded)
		}
		if loaded.LastLoginAt == nil || *loaded.LastLoginAt == 0 {
			t.Fatalf("expected last login timestamp: %+v", loaded)
		}
		if loaded.PasswordChangedAt == nil || *loaded.PasswordChangedAt == 0 {
			t.Fatalf("expected password changed timestamp: %+v", loaded)
		}
		if loaded.DefaultWorkspaceID != "auth_workspace_update" {
			t.Fatalf("default workspace = %q, want auth_workspace_update", loaded.DefaultWorkspaceID)
		}

		byEmail, err := store.Auth().GetUserByEmail(ctx, "updated.user@example.com")
		if err != nil {
			t.Fatalf("get updated user by lowercased email: %v", err)
		}
		if byEmail.ID != "auth_user_update" {
			t.Fatalf("loaded user id = %q, want auth_user_update", byEmail.ID)
		}
	})

	t.Run("LockActiveAdminsReturnsOnlyActiveAdmins", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctx := scopedContractContext(t, store)
		seedAuthUserWorkspace(t, ctx, store, "auth_user_active_admin", "auth_workspace_active_admin", "active-admin@example.com", withRole("admin"))
		seedAuthUserWorkspace(t, ctx, store, "auth_user_regular", "auth_workspace_regular", "regular@example.com", withRole("user"))
		seedAuthUserWorkspace(t, ctx, store, "auth_user_disabled_admin", "auth_workspace_disabled_admin", "disabled-admin@example.com", withRole("admin"), withStatus("disabled"))

		admins, err := store.Auth().LockActiveAdmins(ctx)
		if err != nil {
			t.Fatalf("lock active admins: %v", err)
		}
		if !hasUserID(admins, "auth_user_active_admin") {
			t.Fatalf("active admin missing from locked users: %+v", admins)
		}
		if hasUserID(admins, "auth_user_regular") || hasUserID(admins, "auth_user_disabled_admin") {
			t.Fatalf("non-active-admin returned from lock: %+v", admins)
		}
	})
}

type seedAuthUserWorkspaceOption func(*model.User)

func withDisplayName(displayName string) seedAuthUserWorkspaceOption {
	return func(user *model.User) {
		user.DisplayName = displayName
	}
}

func withRole(role string) seedAuthUserWorkspaceOption {
	return func(user *model.User) {
		user.Role = role
	}
}

func withStatus(status string) seedAuthUserWorkspaceOption {
	return func(user *model.User) {
		user.Status = status
	}
}

func seedAuthUserWorkspace(t *testing.T, ctx context.Context, store storage.Store, userID, workspaceID, email string, opts ...seedAuthUserWorkspaceOption) {
	t.Helper()

	user := contractUser(userID, email, "Contract User", "user")
	for _, opt := range opts {
		opt(user)
	}
	workspace := contractWorkspace(workspaceID, user.ID, "Contract Workspace")
	user.DefaultWorkspaceID = workspace.ID

	err := store.Transact(ctx, func(tx storage.Store) error {
		if err := tx.Auth().CreateUser(ctx, user); err != nil {
			return err
		}
		if err := tx.Auth().CreateWorkspace(ctx, workspace); err != nil {
			return err
		}
		return tx.Auth().AddWorkspaceMember(ctx, workspace.ID, user.ID, "owner")
	})
	if err != nil {
		t.Fatalf("seed auth user workspace: %v", err)
	}
}

func contractUser(id, email, displayName, role string) *model.User {
	return &model.User{
		ID:                 id,
		Email:              email,
		DisplayName:        displayName,
		PasswordHash:       "hash",
		MustChangePassword: true,
		PasswordSet:        true,
		Role:               role,
		Status:             "active",
	}
}

func contractWorkspace(id, ownerUserID, name string) *model.Workspace {
	return &model.Workspace{
		ID:          id,
		Name:        name,
		OwnerUserID: ownerUserID,
	}
}

func expectSessionMissing(t *testing.T, store storage.Store, ctx context.Context, tokenHash string) {
	t.Helper()

	if _, err := store.Auth().GetSessionByTokenHash(ctx, tokenHash); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("session %q error = %v, want sql.ErrNoRows", tokenHash, err)
	}
}

func hasUserID(users []model.User, id string) bool {
	for _, user := range users {
		if user.ID == id {
			return true
		}
	}
	return false
}

type contractAuditMetadataReader interface {
	QueryRowContext(context.Context, string, ...interface{}) *sql.Row
}

func loadContractAuditMetadata(t *testing.T, ctx context.Context, store storage.Store, eventID string) map[string]any {
	t.Helper()
	reader, ok := store.(contractAuditMetadataReader)
	if !ok {
		t.Fatalf("store %T does not expose QueryRowContext", store)
	}
	placeholder := "?"
	if store.Capabilities().TimeRanges {
		placeholder = "$1"
	}
	var raw string
	if err := reader.QueryRowContext(ctx, "SELECT metadata FROM audit_events WHERE id = "+placeholder, eventID).Scan(&raw); err != nil {
		t.Fatalf("load audit metadata: %v", err)
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
		t.Fatalf("decode audit metadata: %v; raw = %s", err, raw)
	}
	return metadata
}

func marshalContractAuditMetadata(t *testing.T, metadata map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("marshal audit metadata: %v", err)
	}
	return strings.ToLower(string(raw))
}

func assertAuthIdentityMatches(t *testing.T, got *model.AuthIdentity, want *model.AuthIdentity) {
	t.Helper()

	if got.ID != want.ID ||
		got.UserID != want.UserID ||
		got.Provider != want.Provider ||
		got.ProviderUserID != want.ProviderUserID ||
		got.ProviderLogin != want.ProviderLogin ||
		got.Email != want.Email ||
		got.CreatedAt != want.CreatedAt {
		t.Fatalf("auth identity = %+v, want %+v", got, want)
	}
	if (got.AvatarURL == nil) != (want.AvatarURL == nil) {
		t.Fatalf("avatar_url = %v, want %v", got.AvatarURL, want.AvatarURL)
	}
	if got.AvatarURL != nil && *got.AvatarURL != *want.AvatarURL {
		t.Fatalf("avatar_url = %q, want %q", *got.AvatarURL, *want.AvatarURL)
	}
}
