package handler

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

type adminSQLRunner interface {
	ExecContext(context.Context, string, ...interface{}) (sql.Result, error)
}

type adminDefaultFolder struct {
	ID        string
	Name      string
	SortOrder float64
}

var adminDefaultFolders = []adminDefaultFolder{
	{ID: "__uncategorized", Name: "Uncategorized", SortOrder: 0},
	{ID: "__work", Name: "Work", SortOrder: 1},
	{ID: "__personal", Name: "Personal", SortOrder: 2},
}

var adminPatchForbiddenFields = map[string]bool{
	"id":                   true,
	"status":               true,
	"password":             true,
	"password_hash":        true,
	"temporary_password":   true,
	"must_change_password": true,
	"default_workspace_id": true,
	"last_login_at":        true,
	"password_changed_at":  true,
	"created_at":           true,
	"updated_at":           true,
}

var adminPatchAllowedFields = map[string]bool{
	"email":        true,
	"display_name": true,
	"role":         true,
}

func ListUsers(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if store == nil {
			internalError(c, "list users failed")
			return
		}
		page, pageSize := getPagination(c)
		users, total, err := store.Auth().ListUsers(c.Request.Context(), storage.UserListFilter{
			Page:     page,
			PageSize: pageSize,
			Query:    strings.TrimSpace(c.Query("q")),
		})
		if err != nil {
			internalError(c, "list users failed")
			return
		}
		successWithPagination(c, gin.H{"users": users}, page, pageSize, total)
	}
}

func CreateUser(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if store == nil {
			internalError(c, "create user failed")
			return
		}
		var req model.CreateUserRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			badRequest(c, "email, display_name, temporary_password, and role are required")
			return
		}
		email := strings.TrimSpace(req.Email)
		displayName := strings.TrimSpace(req.DisplayName)
		role, ok := normalizeAdminUserRole(req.Role)
		if email == "" || displayName == "" || strings.TrimSpace(req.TemporaryPassword) == "" || !ok {
			badRequest(c, "email, display_name, temporary_password, and a valid role are required")
			return
		}
		passwordHash, err := auth.HashPassword(req.TemporaryPassword)
		if err != nil {
			if errors.Is(err, auth.ErrWeakPassword) {
				errorResponse(c, http.StatusBadRequest, "WEAK_PASSWORD", "temporary password does not meet policy")
				return
			}
			internalError(c, "create user failed")
			return
		}

		user := &model.User{
			ID:                 newAdminID("user"),
			Email:              email,
			DisplayName:        displayName,
			PasswordHash:       passwordHash,
			MustChangePassword: true,
			Role:               role,
			Status:             "active",
		}
		workspace := &model.Workspace{
			ID:          newAdminID("workspace"),
			Name:        displayName + " Workspace",
			OwnerUserID: user.ID,
		}
		user.DefaultWorkspaceID = workspace.ID
		identity, _ := auth.IdentityFromContext(c.Request.Context())

		err = store.Transact(c.Request.Context(), func(tx storage.Store) error {
			if err := tx.Auth().CreateUser(c.Request.Context(), user); err != nil {
				return err
			}
			if err := tx.Auth().CreateWorkspace(c.Request.Context(), workspace); err != nil {
				return err
			}
			if err := tx.Auth().SetDefaultWorkspace(c.Request.Context(), user.ID, workspace.ID); err != nil {
				return err
			}
			if err := tx.Auth().AddWorkspaceMember(c.Request.Context(), workspace.ID, user.ID, "owner"); err != nil {
				return err
			}
			targetCtx := auth.ContextWithWorkspaceScope(c.Request.Context(), workspace.ID)
			if err := createDefaultWorkspaceData(targetCtx, tx); err != nil {
				return err
			}
			return tx.Auth().RecordAuditEvent(c.Request.Context(), &model.AuditEvent{
				ActorUserID:  stringPtr(identity.UserID),
				TargetUserID: stringPtr(user.ID),
				WorkspaceID:  stringPtr(workspace.ID),
				Action:       "admin.user.create",
				Metadata: adminAuditMetadata(c, map[string]any{
					"email": email,
					"role":  role,
				}),
			})
		})
		if err != nil {
			internalError(c, "create user failed")
			return
		}
		created(c, gin.H{"user": user, "workspace": workspace})
	}
}

func UpdateUser(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if store == nil {
			internalError(c, "update user failed")
			return
		}
		targetUserID := strings.TrimSpace(c.Param("id"))
		var raw map[string]json.RawMessage
		if err := c.ShouldBindJSON(&raw); err != nil {
			badRequest(c, "invalid user update")
			return
		}
		for key := range raw {
			normalized := strings.ToLower(strings.TrimSpace(key))
			if adminPatchForbiddenFields[normalized] {
				badRequest(c, "field "+key+" cannot be updated with PATCH")
				return
			}
			if !adminPatchAllowedFields[normalized] {
				badRequest(c, "unsupported user field "+key)
				return
			}
		}
		var req model.UpdateUserRequest
		body, err := json.Marshal(raw)
		if err != nil {
			internalError(c, "update user failed")
			return
		}
		if err := json.Unmarshal(body, &req); err != nil {
			badRequest(c, "invalid user update")
			return
		}
		if req.Email != nil {
			email := strings.TrimSpace(*req.Email)
			if email == "" {
				badRequest(c, "email is required")
				return
			}
			req.Email = &email
		}
		if req.DisplayName != nil {
			displayName := strings.TrimSpace(*req.DisplayName)
			if displayName == "" {
				badRequest(c, "display_name is required")
				return
			}
			req.DisplayName = &displayName
		}
		if req.Role != nil {
			role, ok := normalizeAdminUserRole(*req.Role)
			if !ok {
				badRequest(c, "role must be admin or user")
				return
			}
			req.Role = &role
		}

		var updated *model.User
		err = store.Transact(c.Request.Context(), func(tx storage.Store) error {
			current, err := tx.Auth().GetUserByID(c.Request.Context(), targetUserID)
			if err != nil {
				return err
			}
			roleChanged := req.Role != nil && *req.Role != current.Role
			if roleChanged && current.Role == "admin" && *req.Role != "admin" {
				if err := ensureCanRemoveActiveAdmin(c.Request.Context(), tx, targetUserID); err != nil {
					return err
				}
			}
			updated, err = tx.Auth().UpdateUser(c.Request.Context(), targetUserID, &req)
			if err != nil {
				return err
			}
			if roleChanged {
				if err := tx.Auth().RevokeUserSessions(c.Request.Context(), targetUserID); err != nil {
					return err
				}
			}
			identity, _ := auth.IdentityFromContext(c.Request.Context())
			workspaceID := current.DefaultWorkspaceID
			return tx.Auth().RecordAuditEvent(c.Request.Context(), &model.AuditEvent{
				ActorUserID:  stringPtr(identity.UserID),
				TargetUserID: stringPtr(targetUserID),
				WorkspaceID:  stringPtr(workspaceID),
				Action:       "admin.user.update",
				Metadata: adminAuditMetadata(c, map[string]any{
					"role_changed": roleChanged,
				}),
			})
		})
		if err != nil {
			handleAdminUserError(c, err, "update user failed")
			return
		}
		success(c, gin.H{"user": updated})
	}
}

func ResetUserPassword(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if store == nil {
			internalError(c, "reset password failed")
			return
		}
		targetUserID := strings.TrimSpace(c.Param("id"))
		var req model.ResetPasswordRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			badRequest(c, "temporary_password is required")
			return
		}
		passwordHash, err := auth.HashPassword(req.TemporaryPassword)
		if err != nil {
			if errors.Is(err, auth.ErrWeakPassword) {
				errorResponse(c, http.StatusBadRequest, "WEAK_PASSWORD", "temporary password does not meet policy")
				return
			}
			internalError(c, "reset password failed")
			return
		}

		err = store.Transact(c.Request.Context(), func(tx storage.Store) error {
			target, err := tx.Auth().GetUserByID(c.Request.Context(), targetUserID)
			if err != nil {
				return err
			}
			if err := tx.Auth().UpdateUserPassword(c.Request.Context(), targetUserID, passwordHash, true); err != nil {
				return err
			}
			if err := tx.Auth().RevokeUserSessions(c.Request.Context(), targetUserID); err != nil {
				return err
			}
			identity, _ := auth.IdentityFromContext(c.Request.Context())
			return tx.Auth().RecordAuditEvent(c.Request.Context(), &model.AuditEvent{
				ActorUserID:  stringPtr(identity.UserID),
				TargetUserID: stringPtr(targetUserID),
				WorkspaceID:  stringPtr(target.DefaultWorkspaceID),
				Action:       "admin.user.reset_password",
				Metadata:     adminAuditMetadata(c, nil),
			})
		})
		if err != nil {
			handleAdminUserError(c, err, "reset password failed")
			return
		}
		noContent(c)
	}
}

func DisableUser(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if store == nil {
			internalError(c, "disable user failed")
			return
		}
		targetUserID := strings.TrimSpace(c.Param("id"))
		err := store.Transact(c.Request.Context(), func(tx storage.Store) error {
			target, err := tx.Auth().GetUserByID(c.Request.Context(), targetUserID)
			if err != nil {
				return err
			}
			if target.Role == "admin" && target.Status == "active" {
				if err := ensureCanRemoveActiveAdmin(c.Request.Context(), tx, targetUserID); err != nil {
					return err
				}
			}
			if _, err := tx.Auth().UpdateUserStatus(c.Request.Context(), targetUserID, "disabled"); err != nil {
				return err
			}
			if err := tx.Auth().RevokeUserSessions(c.Request.Context(), targetUserID); err != nil {
				return err
			}
			identity, _ := auth.IdentityFromContext(c.Request.Context())
			return tx.Auth().RecordAuditEvent(c.Request.Context(), &model.AuditEvent{
				ActorUserID:  stringPtr(identity.UserID),
				TargetUserID: stringPtr(targetUserID),
				WorkspaceID:  stringPtr(target.DefaultWorkspaceID),
				Action:       "admin.user.disable",
				Metadata:     adminAuditMetadata(c, nil),
			})
		})
		if err != nil {
			handleAdminUserError(c, err, "disable user failed")
			return
		}
		noContent(c)
	}
}

func EnableUser(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if store == nil {
			internalError(c, "enable user failed")
			return
		}
		targetUserID := strings.TrimSpace(c.Param("id"))
		err := store.Transact(c.Request.Context(), func(tx storage.Store) error {
			target, err := tx.Auth().GetUserByID(c.Request.Context(), targetUserID)
			if err != nil {
				return err
			}
			if _, err := tx.Auth().UpdateUserStatus(c.Request.Context(), targetUserID, "active"); err != nil {
				return err
			}
			identity, _ := auth.IdentityFromContext(c.Request.Context())
			return tx.Auth().RecordAuditEvent(c.Request.Context(), &model.AuditEvent{
				ActorUserID:  stringPtr(identity.UserID),
				TargetUserID: stringPtr(targetUserID),
				WorkspaceID:  stringPtr(target.DefaultWorkspaceID),
				Action:       "admin.user.enable",
				Metadata:     adminAuditMetadata(c, nil),
			})
		})
		if err != nil {
			handleAdminUserError(c, err, "enable user failed")
			return
		}
		noContent(c)
	}
}

func createDefaultWorkspaceData(ctx context.Context, store storage.Store) error {
	runner, ok := store.(adminSQLRunner)
	if !ok {
		return fmt.Errorf("storage store %T does not expose SQL runner", store)
	}
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	if store.Capabilities().TimeRanges {
		for _, folder := range adminDefaultFolders {
			if _, err := runner.ExecContext(ctx, `
				INSERT INTO folders (id, name, sort_order, created_at, workspace_id)
				VALUES ($1, $2, $3, now(), $4)
				ON CONFLICT (workspace_id, id) DO NOTHING
			`, folder.ID, folder.Name, folder.SortOrder, workspaceID); err != nil {
				return fmt.Errorf("provision default folder %s: %w", folder.ID, err)
			}
		}
		if _, err := runner.ExecContext(ctx, `
			INSERT INTO task_projects (id, name, type, description, created_at, updated_at, workspace_id)
			VALUES ($1, $2, $3, $4, now(), now(), $5)
			ON CONFLICT (workspace_id, id) DO NOTHING
		`, "personal", "Personal", "personal", "Default personal task project", workspaceID); err != nil {
			return fmt.Errorf("provision default task project: %w", err)
		}
		return nil
	}

	for _, folder := range adminDefaultFolders {
		if _, err := runner.ExecContext(ctx, `
			INSERT INTO folders (id, name, sort_order, created_at, workspace_id)
			VALUES (?, ?, ?, unixepoch(), ?)
			ON CONFLICT (workspace_id, id) DO NOTHING
		`, folder.ID, folder.Name, folder.SortOrder, workspaceID); err != nil {
			return fmt.Errorf("provision default folder %s: %w", folder.ID, err)
		}
	}
	if _, err := runner.ExecContext(ctx, `
		INSERT INTO task_projects (id, name, type, description, created_at, updated_at, workspace_id)
		VALUES (?, ?, ?, ?, unixepoch(), unixepoch(), ?)
		ON CONFLICT (workspace_id, id) DO NOTHING
	`, "personal", "Personal", "personal", "Default personal task project", workspaceID); err != nil {
		return fmt.Errorf("provision default task project: %w", err)
	}
	return nil
}

func ensureCanRemoveActiveAdmin(ctx context.Context, store storage.Store, targetUserID string) error {
	admins, err := store.Auth().LockActiveAdmins(ctx)
	if err != nil {
		return err
	}
	if wouldRemoveLastActiveAdmin(admins, targetUserID) {
		return auth.ErrLastAdminRequired
	}
	return nil
}

func wouldRemoveLastActiveAdmin(admins []model.User, targetUserID string) bool {
	targetActiveAdmin := false
	for _, admin := range admins {
		if admin.ID == targetUserID {
			targetActiveAdmin = true
			break
		}
	}
	return targetActiveAdmin && len(admins) <= 1
}

func normalizeAdminUserRole(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "admin":
		return "admin", true
	case "user":
		return "user", true
	default:
		return "", false
	}
}

func adminAuditMetadata(c *gin.Context, extra map[string]any) map[string]any {
	metadata := authAuditMetadata(c)
	for key, value := range extra {
		metadata[key] = value
	}
	return auth.SanitizeAuditMetadata(metadata)
}

func handleAdminUserError(c *gin.Context, err error, message string) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		notFound(c, "user not found")
	case errors.Is(err, auth.ErrLastAdminRequired):
		conflict(c, "LAST_ADMIN_REQUIRED", "at least one active admin is required")
	default:
		internalError(c, message)
	}
}

func stringPtr(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return &value
}

func newAdminID(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}
	return fmt.Sprintf("%s_%x%x%x%x%x", prefix, b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
