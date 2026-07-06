package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/jackc/pgx/v5/pgconn"
)

type authRepository struct {
	db postgresRunner
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
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
	`, user.ID, user.Email, user.DisplayName, user.PasswordHash, passwordSet, user.MustChangePassword, nullableString(user.DefaultWorkspaceID),
		user.Role, user.Status, unixToTime(user.CreatedAt), unixToTime(user.UpdatedAt), unixPtrToTime(user.LastLoginAt), unixPtrToTime(user.PasswordChangedAt))
	if isPostgresEmailAlreadyExists(err) {
		return auth.ErrEmailAlreadyExists
	}
	return err
}

func (r authRepository) SetDefaultWorkspace(ctx context.Context, userID, workspaceID string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE users
		SET default_workspace_id = $2,
			updated_at = now()
		WHERE id = $1
	`, userID, workspaceID)
	return err
}

func (r authRepository) GetUserByEmail(ctx context.Context, email string) (*model.User, error) {
	return scanPostgresAuthUser(r.db.QueryRowContext(ctx, postgresAuthUserSelectSQL()+`
		WHERE lower(email) = lower($1)
	`, email))
}

func (r authRepository) GetUserByID(ctx context.Context, id string) (*model.User, error) {
	return scanPostgresAuthUser(r.db.QueryRowContext(ctx, postgresAuthUserSelectSQL()+`
		WHERE id = $1
	`, id))
}

func (r authRepository) ListUsers(ctx context.Context, filter storage.UserListFilter) ([]model.User, int, error) {
	where := "1=1"
	args := []interface{}{}
	query := strings.TrimSpace(filter.Query)
	if query != "" {
		args = append(args, "%"+query+"%")
		where = fmt.Sprintf("(email ILIKE %s OR display_name ILIKE %s)", pgPlaceholder(len(args)), pgPlaceholder(len(args)))
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
	limitPlaceholder := pgPlaceholder(len(selectArgs) + 1)
	offsetPlaceholder := pgPlaceholder(len(selectArgs) + 2)
	selectArgs = append(selectArgs, pageSize, offset)

	rows, err := r.db.QueryContext(ctx, fmt.Sprintf(postgresAuthUserSelectSQL()+`
		WHERE %s
		ORDER BY created_at DESC, id ASC
		LIMIT %s OFFSET %s
	`, where, limitPlaceholder, offsetPlaceholder), selectArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	users := make([]model.User, 0)
	for rows.Next() {
		user, err := scanPostgresAuthUser(rows)
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
	builder := newPgSetBuilder(1)
	builder.Add("updated_at", time.Now().UTC())
	if req.Email != nil {
		builder.Add("email", *req.Email)
	}
	if req.DisplayName != nil {
		builder.Add("display_name", *req.DisplayName)
	}
	if req.Role != nil {
		builder.Add("role", *req.Role)
	}

	clause, args := builder.ClauseAndArgs()
	args = append(args, id)
	if _, err := r.db.ExecContext(ctx, fmt.Sprintf("UPDATE users SET %s WHERE id = %s", clause, pgPlaceholder(len(args))), args...); err != nil {
		if isPostgresEmailAlreadyExists(err) {
			return nil, auth.ErrEmailAlreadyExists
		}
		return nil, err
	}
	return r.GetUserByID(ctx, id)
}

func (r authRepository) UpdateUserStatus(ctx context.Context, id string, status string) (*model.User, error) {
	result, err := r.db.ExecContext(ctx, `
		UPDATE users
		SET status = $2,
			updated_at = now()
		WHERE id = $1
	`, id, status)
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
		SET last_login_at = $2,
			updated_at = now()
		WHERE id = $1
	`, userID, at.UTC())
	return err
}

func (r authRepository) UpdateUserPassword(ctx context.Context, userID, passwordHash string, mustChangePassword bool) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE users
		SET password_hash = $2,
			password_set = true,
			must_change_password = $3,
			password_changed_at = now(),
			updated_at = now()
		WHERE id = $1
	`, userID, passwordHash, mustChangePassword)
	return err
}

func (r authRepository) GetAuthIdentity(ctx context.Context, provider, providerUserID string) (*model.AuthIdentity, error) {
	return scanPostgresAuthIdentity(r.db.QueryRowContext(ctx, postgresAuthIdentitySelectSQL()+`
		WHERE provider = $1 AND provider_user_id = $2
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
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, identity.ID, identity.UserID, identity.Provider, identity.ProviderUserID, identity.ProviderLogin, identity.Email,
		nullableStringPtr(identity.AvatarURL), unixToTime(identity.CreatedAt), unixToTime(identity.UpdatedAt), unixPtrToTime(identity.LastLoginAt))
	return err
}

func (r authRepository) UpdateAuthIdentityFromProvider(ctx context.Context, identity *model.AuthIdentity, at time.Time) error {
	if identity == nil {
		return fmt.Errorf("auth identity is nil")
	}
	loginAt := at.UTC()
	result, err := r.db.ExecContext(ctx, `
		UPDATE auth_identities
		SET provider_login = $1,
			email = $2,
			avatar_url = $3,
			updated_at = $4,
			last_login_at = $4
		WHERE provider = $5 AND provider_user_id = $6
	`, identity.ProviderLogin, identity.Email, nullableStringPtr(identity.AvatarURL), loginAt, identity.Provider, identity.ProviderUserID)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return sql.ErrNoRows
	}
	loginAtUnix := timeToUnix(loginAt)
	identity.UpdatedAt = loginAtUnix
	identity.LastLoginAt = &loginAtUnix
	return nil
}

func (r authRepository) ListAuthIdentitiesByUser(ctx context.Context, userID string) ([]model.AuthIdentity, error) {
	rows, err := r.db.QueryContext(ctx, postgresAuthIdentitySelectSQL()+`
		WHERE user_id = $1
		ORDER BY provider ASC, provider_user_id ASC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	identities := make([]model.AuthIdentity, 0)
	for rows.Next() {
		identity, err := scanPostgresAuthIdentity(rows)
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
		VALUES ($1, $2, $3, $4, $5)
	`, workspace.ID, workspace.Name, workspace.OwnerUserID, unixToTime(workspace.CreatedAt), unixToTime(workspace.UpdatedAt))
	return err
}

func (r authRepository) AddWorkspaceMember(ctx context.Context, workspaceID, userID, role string) error {
	if strings.TrimSpace(role) == "" {
		role = "member"
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO workspace_members (workspace_id, user_id, role, created_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (workspace_id, user_id) DO UPDATE SET
			role = EXCLUDED.role
	`, workspaceID, userID, role)
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
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, session.ID, session.UserID, session.WorkspaceID, session.TokenHash, session.UserAgent, session.IPAddress,
		session.ExpiresAt.UTC(), nullableSessionTime(session.RevokedAt), session.CreatedAt.UTC(), session.LastSeenAt.UTC())
	return err
}

func (r authRepository) GetSessionByTokenHash(ctx context.Context, tokenHash string) (*model.Session, error) {
	return scanPostgresAuthSession(r.db.QueryRowContext(ctx, `
		SELECT id, user_id, workspace_id, token_hash, user_agent, ip_address, expires_at, revoked_at, created_at, last_seen_at
		FROM sessions
		WHERE token_hash = $1
			AND revoked_at IS NULL
			AND expires_at > now()
	`, tokenHash))
}

func (r authRepository) GetWorkspaceMembership(ctx context.Context, workspaceID, userID string) (*model.WorkspaceMember, error) {
	var member model.WorkspaceMember
	var createdAt time.Time
	err := r.db.QueryRowContext(ctx, `
		SELECT workspace_id, user_id, role, created_at
		FROM workspace_members
		WHERE workspace_id = $1 AND user_id = $2
	`, workspaceID, userID).Scan(&member.WorkspaceID, &member.UserID, &member.Role, &createdAt)
	if err != nil {
		return nil, err
	}
	member.CreatedAt = timeToUnix(createdAt)
	return &member, nil
}

func (r authRepository) RevokeSession(ctx context.Context, sessionID string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE sessions
		SET revoked_at = now()
		WHERE id = $1 AND revoked_at IS NULL
	`, sessionID)
	return err
}

func (r authRepository) RevokeUserSessions(ctx context.Context, userID string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE sessions
		SET revoked_at = now()
		WHERE user_id = $1 AND revoked_at IS NULL
	`, userID)
	return err
}

func (r authRepository) RevokeUserSessionsExcept(ctx context.Context, userID, keepSessionID string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE sessions
		SET revoked_at = now()
		WHERE user_id = $1
			AND id <> $2
			AND revoked_at IS NULL
	`, userID, keepSessionID)
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
		VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7)
	`, event.ID, nullableStringPtr(event.ActorUserID), nullableStringPtr(event.TargetUserID), nullableStringPtr(event.WorkspaceID),
		event.Action, string(metadataJSON), unixToTime(event.CreatedAt))
	return err
}

func (r authRepository) LockActiveAdmins(ctx context.Context) ([]model.User, error) {
	rows, err := r.db.QueryContext(ctx, postgresAuthUserSelectSQL()+`
		WHERE role = 'admin' AND status = 'active'
		ORDER BY id ASC
		FOR UPDATE
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	users := make([]model.User, 0)
	for rows.Next() {
		user, err := scanPostgresAuthUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, *user)
	}
	return users, rows.Err()
}

func postgresAuthUserSelectSQL() string {
	return `
		SELECT id, email, display_name, password_hash, password_set, must_change_password, default_workspace_id,
			role, status, created_at, updated_at, last_login_at, password_changed_at
		FROM users
	`
}

func scanPostgresAuthUser(row rowScanner) (*model.User, error) {
	var user model.User
	var defaultWorkspaceID sql.NullString
	var createdAt time.Time
	var updatedAt time.Time
	var lastLoginAt sql.NullTime
	var passwordChangedAt sql.NullTime
	if err := row.Scan(
		&user.ID,
		&user.Email,
		&user.DisplayName,
		&user.PasswordHash,
		&user.PasswordSet,
		&user.MustChangePassword,
		&defaultWorkspaceID,
		&user.Role,
		&user.Status,
		&createdAt,
		&updatedAt,
		&lastLoginAt,
		&passwordChangedAt,
	); err != nil {
		return nil, err
	}
	user.DefaultWorkspaceID = defaultWorkspaceID.String
	user.CreatedAt = timeToUnix(createdAt)
	user.UpdatedAt = timeToUnix(updatedAt)
	user.LastLoginAt = nullTimeToUnixPtr(lastLoginAt)
	user.PasswordChangedAt = nullTimeToUnixPtr(passwordChangedAt)
	return &user, nil
}

func postgresAuthIdentitySelectSQL() string {
	return `
		SELECT id, user_id, provider, provider_user_id, provider_login, email,
			avatar_url, created_at, updated_at, last_login_at
		FROM auth_identities
	`
}

func scanPostgresAuthIdentity(row rowScanner) (*model.AuthIdentity, error) {
	var identity model.AuthIdentity
	var avatarURL sql.NullString
	var createdAt time.Time
	var updatedAt time.Time
	var lastLoginAt sql.NullTime
	if err := row.Scan(
		&identity.ID,
		&identity.UserID,
		&identity.Provider,
		&identity.ProviderUserID,
		&identity.ProviderLogin,
		&identity.Email,
		&avatarURL,
		&createdAt,
		&updatedAt,
		&lastLoginAt,
	); err != nil {
		return nil, err
	}
	identity.AvatarURL = nullStringPtr(avatarURL)
	identity.CreatedAt = timeToUnix(createdAt)
	identity.UpdatedAt = timeToUnix(updatedAt)
	identity.LastLoginAt = nullTimeToUnixPtr(lastLoginAt)
	return &identity, nil
}

func scanPostgresAuthSession(row rowScanner) (*model.Session, error) {
	var session model.Session
	var revokedAt sql.NullTime
	var expiresAt time.Time
	var createdAt time.Time
	var lastSeenAt time.Time
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
	session.ExpiresAt = expiresAt.UTC()
	session.RevokedAt = nullTimeToTimePtr(revokedAt)
	session.CreatedAt = createdAt.UTC()
	session.LastSeenAt = lastSeenAt.UTC()
	return &session, nil
}

func nullableSessionTime(value *time.Time) interface{} {
	if value == nil || value.IsZero() {
		return nil
	}
	return value.UTC()
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

func nullTimeToTimePtr(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	at := value.Time.UTC()
	return &at
}

func isPostgresEmailAlreadyExists(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "23505" && pgErr.ConstraintName == "users_email_lower_idx"
}
