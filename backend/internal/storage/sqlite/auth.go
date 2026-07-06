package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

type authRepository struct {
	db sqliteRunner
}

func (r authRepository) CreateUser(ctx context.Context, user *model.User) error {
	if user == nil {
		return fmt.Errorf("user is nil")
	}
	now := nowUnix()
	if strings.TrimSpace(user.ID) == "" {
		user.ID = newID()
	}
	if strings.TrimSpace(user.Role) == "" {
		user.Role = "user"
	}
	if strings.TrimSpace(user.Status) == "" {
		user.Status = "active"
	}
	if user.CreatedAt == 0 {
		user.CreatedAt = now
	}
	if user.UpdatedAt == 0 {
		user.UpdatedAt = now
	}
	passwordSet := user.PasswordSet
	if !passwordSet && strings.TrimSpace(user.PasswordHash) != "" {
		passwordSet = true
		user.PasswordSet = true
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO users (
			id, email, display_name, password_hash, password_set, must_change_password, default_workspace_id,
			role, status, created_at, updated_at, last_login_at, password_changed_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, user.ID, user.Email, user.DisplayName, user.PasswordHash, boolToSQLiteInt(passwordSet), boolToSQLiteInt(user.MustChangePassword), nullableString(user.DefaultWorkspaceID),
		user.Role, user.Status, user.CreatedAt, user.UpdatedAt, nullableUnixPtr(user.LastLoginAt), nullableUnixPtr(user.PasswordChangedAt))
	if isSQLiteEmailAlreadyExists(err) {
		return auth.ErrEmailAlreadyExists
	}
	return err
}

func (r authRepository) SetDefaultWorkspace(ctx context.Context, userID, workspaceID string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE users
		SET default_workspace_id = ?,
			updated_at = ?
		WHERE id = ?
	`, workspaceID, nowUnix(), userID)
	return err
}

func (r authRepository) GetUserByEmail(ctx context.Context, email string) (*model.User, error) {
	return scanSQLiteAuthUser(r.db.QueryRowContext(ctx, sqliteAuthUserSelectSQL()+`
		WHERE lower(email) = lower(?)
	`, email))
}

func (r authRepository) GetUserByID(ctx context.Context, id string) (*model.User, error) {
	return scanSQLiteAuthUser(r.db.QueryRowContext(ctx, sqliteAuthUserSelectSQL()+`
		WHERE id = ?
	`, id))
}

func (r authRepository) ListUsers(ctx context.Context, filter storage.UserListFilter) ([]model.User, int, error) {
	where := "1=1"
	args := []interface{}{}
	query := strings.TrimSpace(filter.Query)
	if query != "" {
		where = "(lower(email) LIKE '%' || lower(?) || '%' OR lower(display_name) LIKE '%' || lower(?) || '%')"
		args = append(args, query, query)
	}

	var total int
	if err := r.db.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM users WHERE %s", where), args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	page := filter.Page
	if page <= 0 {
		page = 1
	}
	pageSize := filter.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize
	selectArgs := append([]interface{}{}, args...)
	selectArgs = append(selectArgs, pageSize, offset)
	rows, err := r.db.QueryContext(ctx, fmt.Sprintf(sqliteAuthUserSelectSQL()+`
		WHERE %s
		ORDER BY created_at DESC, id ASC
		LIMIT ? OFFSET ?
	`, where), selectArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	users := make([]model.User, 0)
	for rows.Next() {
		user, err := scanSQLiteAuthUser(rows)
		if err != nil {
			return nil, 0, err
		}
		users = append(users, *user)
	}
	return users, total, rows.Err()
}

func (r authRepository) UpdateUser(ctx context.Context, id string, req *model.UpdateUserRequest) (*model.User, error) {
	if req == nil {
		req = &model.UpdateUserRequest{}
	}
	sets := []string{"updated_at = ?"}
	args := []interface{}{nowUnix()}
	if req.Email != nil {
		sets = append(sets, "email = ?")
		args = append(args, *req.Email)
	}
	if req.DisplayName != nil {
		sets = append(sets, "display_name = ?")
		args = append(args, *req.DisplayName)
	}
	if req.Role != nil {
		sets = append(sets, "role = ?")
		args = append(args, *req.Role)
	}
	args = append(args, id)
	if _, err := r.db.ExecContext(ctx, fmt.Sprintf("UPDATE users SET %s WHERE id = ?", strings.Join(sets, ", ")), args...); err != nil {
		if isSQLiteEmailAlreadyExists(err) {
			return nil, auth.ErrEmailAlreadyExists
		}
		return nil, err
	}
	return r.GetUserByID(ctx, id)
}

func (r authRepository) UpdateUserStatus(ctx context.Context, id string, status string) (*model.User, error) {
	result, err := r.db.ExecContext(ctx, `
		UPDATE users
		SET status = ?,
			updated_at = ?
		WHERE id = ?
	`, status, nowUnix(), id)
	if err != nil {
		return nil, err
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return nil, sql.ErrNoRows
	}
	return r.GetUserByID(ctx, id)
}

func (r authRepository) UpdateUserLastLogin(ctx context.Context, userID string, at time.Time) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE users
		SET last_login_at = ?,
			updated_at = ?
		WHERE id = ?
	`, at.UTC().Unix(), nowUnix(), userID)
	return err
}

func (r authRepository) UpdateUserPassword(ctx context.Context, userID, passwordHash string, mustChangePassword bool) error {
	now := nowUnix()
	_, err := r.db.ExecContext(ctx, `
		UPDATE users
		SET password_hash = ?,
			password_set = 1,
			must_change_password = ?,
			password_changed_at = ?,
			updated_at = ?
		WHERE id = ?
	`, passwordHash, boolToSQLiteInt(mustChangePassword), now, now, userID)
	return err
}

func (r authRepository) GetAuthIdentity(ctx context.Context, provider, providerUserID string) (*model.AuthIdentity, error) {
	return scanSQLiteAuthIdentity(r.db.QueryRowContext(ctx, sqliteAuthIdentitySelectSQL()+`
		WHERE provider = ? AND provider_user_id = ?
	`, provider, providerUserID))
}

func (r authRepository) CreateAuthIdentity(ctx context.Context, identity *model.AuthIdentity) error {
	if identity == nil {
		return fmt.Errorf("auth identity is nil")
	}
	now := nowUnix()
	if strings.TrimSpace(identity.ID) == "" {
		identity.ID = newID()
	}
	if identity.CreatedAt == 0 {
		identity.CreatedAt = now
	}
	if identity.UpdatedAt == 0 {
		identity.UpdatedAt = identity.CreatedAt
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO auth_identities (
			id, user_id, provider, provider_user_id, provider_login, email,
			avatar_url, created_at, updated_at, last_login_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, identity.ID, identity.UserID, identity.Provider, identity.ProviderUserID, identity.ProviderLogin, identity.Email,
		nullableStringPtr(identity.AvatarURL), identity.CreatedAt, identity.UpdatedAt, nullableUnixPtr(identity.LastLoginAt))
	return err
}

func (r authRepository) UpdateAuthIdentityFromProvider(ctx context.Context, identity *model.AuthIdentity, at time.Time) error {
	if identity == nil {
		return fmt.Errorf("auth identity is nil")
	}
	loginAt := at.UTC().Unix()
	result, err := r.db.ExecContext(ctx, `
		UPDATE auth_identities
		SET provider_login = ?,
			email = ?,
			avatar_url = ?,
			updated_at = ?,
			last_login_at = ?
		WHERE provider = ? AND provider_user_id = ?
	`, identity.ProviderLogin, identity.Email, nullableStringPtr(identity.AvatarURL), loginAt, loginAt, identity.Provider, identity.ProviderUserID)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return sql.ErrNoRows
	}
	identity.UpdatedAt = loginAt
	identity.LastLoginAt = &loginAt
	return nil
}

func (r authRepository) ListAuthIdentitiesByUser(ctx context.Context, userID string) ([]model.AuthIdentity, error) {
	rows, err := r.db.QueryContext(ctx, sqliteAuthIdentitySelectSQL()+`
		WHERE user_id = ?
		ORDER BY provider ASC, provider_user_id ASC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	identities := make([]model.AuthIdentity, 0)
	for rows.Next() {
		identity, err := scanSQLiteAuthIdentity(rows)
		if err != nil {
			return nil, err
		}
		identities = append(identities, *identity)
	}
	return identities, rows.Err()
}

func (r authRepository) CreateWorkspace(ctx context.Context, workspace *model.Workspace) error {
	if workspace == nil {
		return fmt.Errorf("workspace is nil")
	}
	now := nowUnix()
	if strings.TrimSpace(workspace.ID) == "" {
		workspace.ID = newID()
	}
	if workspace.CreatedAt == 0 {
		workspace.CreatedAt = now
	}
	if workspace.UpdatedAt == 0 {
		workspace.UpdatedAt = now
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO workspaces (id, name, owner_user_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
	`, workspace.ID, workspace.Name, workspace.OwnerUserID, workspace.CreatedAt, workspace.UpdatedAt)
	return err
}

func (r authRepository) AddWorkspaceMember(ctx context.Context, workspaceID, userID, role string) error {
	if strings.TrimSpace(role) == "" {
		role = "member"
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO workspace_members (workspace_id, user_id, role, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(workspace_id, user_id) DO UPDATE SET
			role = excluded.role
	`, workspaceID, userID, role, nowUnix())
	return err
}

func (r authRepository) CreateSession(ctx context.Context, session *model.Session) error {
	if session == nil {
		return fmt.Errorf("session is nil")
	}
	now := time.Now().UTC()
	if strings.TrimSpace(session.ID) == "" {
		session.ID = newID()
	}
	if session.CreatedAt.IsZero() {
		session.CreatedAt = now
	}
	if session.LastSeenAt.IsZero() {
		session.LastSeenAt = now
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO sessions (
			id, user_id, workspace_id, token_hash, user_agent, ip_address,
			expires_at, revoked_at, created_at, last_seen_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, session.ID, session.UserID, session.WorkspaceID, session.TokenHash, session.UserAgent, session.IPAddress,
		session.ExpiresAt.UTC().Unix(), nullableSessionUnix(session.RevokedAt), session.CreatedAt.UTC().Unix(), session.LastSeenAt.UTC().Unix())
	return err
}

func (r authRepository) GetSessionByTokenHash(ctx context.Context, tokenHash string) (*model.Session, error) {
	return scanSQLiteAuthSession(r.db.QueryRowContext(ctx, `
		SELECT id, user_id, workspace_id, token_hash, user_agent, ip_address, expires_at, revoked_at, created_at, last_seen_at
		FROM sessions
		WHERE token_hash = ?
			AND revoked_at IS NULL
			AND expires_at > unixepoch()
	`, tokenHash))
}

func (r authRepository) GetWorkspaceMembership(ctx context.Context, workspaceID, userID string) (*model.WorkspaceMember, error) {
	var member model.WorkspaceMember
	err := r.db.QueryRowContext(ctx, `
		SELECT workspace_id, user_id, role, created_at
		FROM workspace_members
		WHERE workspace_id = ? AND user_id = ?
	`, workspaceID, userID).Scan(&member.WorkspaceID, &member.UserID, &member.Role, &member.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &member, nil
}

func (r authRepository) RevokeSession(ctx context.Context, sessionID string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE sessions
		SET revoked_at = ?
		WHERE id = ? AND revoked_at IS NULL
	`, nowUnix(), sessionID)
	return err
}

func (r authRepository) RevokeUserSessions(ctx context.Context, userID string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE sessions
		SET revoked_at = ?
		WHERE user_id = ? AND revoked_at IS NULL
	`, nowUnix(), userID)
	return err
}

func (r authRepository) RevokeUserSessionsExcept(ctx context.Context, userID, keepSessionID string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE sessions
		SET revoked_at = ?
		WHERE user_id = ?
			AND id <> ?
			AND revoked_at IS NULL
	`, nowUnix(), userID, keepSessionID)
	return err
}

func (r authRepository) RecordAuditEvent(ctx context.Context, event *model.AuditEvent) error {
	if event == nil {
		return fmt.Errorf("audit event is nil")
	}
	if strings.TrimSpace(event.ID) == "" {
		event.ID = newID()
	}
	if event.CreatedAt == 0 {
		event.CreatedAt = nowUnix()
	}
	metadata := event.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadata = auth.SanitizeAuditMetadata(metadata)
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("encode audit metadata: %w", err)
	}
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO audit_events (id, actor_user_id, target_user_id, workspace_id, action, metadata, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, event.ID, nullableStringPtr(event.ActorUserID), nullableStringPtr(event.TargetUserID), nullableStringPtr(event.WorkspaceID),
		event.Action, string(metadataJSON), event.CreatedAt)
	return err
}

func (r authRepository) LockActiveAdmins(ctx context.Context) ([]model.User, error) {
	if _, err := r.db.ExecContext(ctx, `
		UPDATE users
		SET updated_at = updated_at
		WHERE role = 'admin' AND status = 'active'
	`); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, sqliteAuthUserSelectSQL()+`
		WHERE role = 'admin' AND status = 'active'
		ORDER BY id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	users := make([]model.User, 0)
	for rows.Next() {
		user, err := scanSQLiteAuthUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, *user)
	}
	return users, rows.Err()
}

func sqliteAuthUserSelectSQL() string {
	return `
		SELECT id, email, display_name, password_hash, password_set, must_change_password, default_workspace_id,
			role, status, created_at, updated_at, last_login_at, password_changed_at
		FROM users
	`
}

func scanSQLiteAuthUser(row sqliteRowScanner) (*model.User, error) {
	var user model.User
	var passwordSet int
	var mustChangePassword int
	var defaultWorkspaceID sql.NullString
	var lastLoginAt sql.NullInt64
	var passwordChangedAt sql.NullInt64
	if err := row.Scan(
		&user.ID,
		&user.Email,
		&user.DisplayName,
		&user.PasswordHash,
		&passwordSet,
		&mustChangePassword,
		&defaultWorkspaceID,
		&user.Role,
		&user.Status,
		&user.CreatedAt,
		&user.UpdatedAt,
		&lastLoginAt,
		&passwordChangedAt,
	); err != nil {
		return nil, err
	}
	user.PasswordSet = passwordSet == 1
	user.MustChangePassword = mustChangePassword == 1
	user.DefaultWorkspaceID = defaultWorkspaceID.String
	user.LastLoginAt = nullInt64Ptr(lastLoginAt)
	user.PasswordChangedAt = nullInt64Ptr(passwordChangedAt)
	return &user, nil
}

func sqliteAuthIdentitySelectSQL() string {
	return `
		SELECT id, user_id, provider, provider_user_id, provider_login, email,
			avatar_url, created_at, updated_at, last_login_at
		FROM auth_identities
	`
}

func scanSQLiteAuthIdentity(row sqliteRowScanner) (*model.AuthIdentity, error) {
	var identity model.AuthIdentity
	var avatarURL sql.NullString
	var lastLoginAt sql.NullInt64
	if err := row.Scan(
		&identity.ID,
		&identity.UserID,
		&identity.Provider,
		&identity.ProviderUserID,
		&identity.ProviderLogin,
		&identity.Email,
		&avatarURL,
		&identity.CreatedAt,
		&identity.UpdatedAt,
		&lastLoginAt,
	); err != nil {
		return nil, err
	}
	identity.AvatarURL = nullStringPtr(avatarURL)
	identity.LastLoginAt = nullInt64Ptr(lastLoginAt)
	return &identity, nil
}

func scanSQLiteAuthSession(row sqliteRowScanner) (*model.Session, error) {
	var session model.Session
	var expiresAt int64
	var revokedAt sql.NullInt64
	var createdAt int64
	var lastSeenAt int64
	if err := row.Scan(
		&session.ID,
		&session.UserID,
		&session.WorkspaceID,
		&session.TokenHash,
		&session.UserAgent,
		&session.IPAddress,
		&expiresAt,
		&revokedAt,
		&createdAt,
		&lastSeenAt,
	); err != nil {
		return nil, err
	}
	session.ExpiresAt = time.Unix(expiresAt, 0).UTC()
	session.RevokedAt = nullUnixToTimePtr(revokedAt)
	session.CreatedAt = time.Unix(createdAt, 0).UTC()
	session.LastSeenAt = time.Unix(lastSeenAt, 0).UTC()
	return &session, nil
}

func nullInt64Ptr(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	return &value.Int64
}

func nullableUnixPtr(value *int64) interface{} {
	if value == nil {
		return nil
	}
	return *value
}

func nullableSessionUnix(value *time.Time) interface{} {
	if value == nil || value.IsZero() {
		return nil
	}
	return value.UTC().Unix()
}

func nullUnixToTimePtr(value sql.NullInt64) *time.Time {
	if !value.Valid {
		return nil
	}
	at := time.Unix(value.Int64, 0).UTC()
	return &at
}

func nullableString(value string) interface{} {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func nullableStringPtr(value *string) interface{} {
	if value == nil {
		return nil
	}
	return *value
}

func nullStringPtr(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func isSQLiteEmailAlreadyExists(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	if !strings.Contains(message, "unique constraint failed") && !strings.Contains(message, "constraint failed") {
		return false
	}
	return strings.Contains(message, "users_email_lower_idx") ||
		strings.Contains(message, "users.email") ||
		strings.Contains(message, "index 'users_email_lower_idx'")
}
