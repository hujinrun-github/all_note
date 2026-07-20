package model

import "time"

type User struct {
	ID                 string `json:"id"`
	Email              string `json:"email"`
	DisplayName        string `json:"display_name"`
	PasswordHash       string `json:"-"`
	PasswordSet        bool   `json:"password_set"`
	MustChangePassword bool   `json:"must_change_password"`
	DefaultWorkspaceID string `json:"default_workspace_id"`
	Role               string `json:"role"`
	Status             string `json:"status"`
	CreatedAt          int64  `json:"created_at"`
	UpdatedAt          int64  `json:"updated_at"`
	LastLoginAt        *int64 `json:"last_login_at,omitempty"`
	PasswordChangedAt  *int64 `json:"password_changed_at,omitempty"`
}

type AuthIdentity struct {
	ID             string  `json:"id"`
	UserID         string  `json:"user_id"`
	Provider       string  `json:"provider"`
	ProviderUserID string  `json:"provider_user_id"`
	ProviderLogin  string  `json:"provider_login"`
	Email          string  `json:"email"`
	AvatarURL      *string `json:"avatar_url,omitempty"`
	CreatedAt      int64   `json:"created_at"`
	UpdatedAt      int64   `json:"updated_at"`
	LastLoginAt    *int64  `json:"last_login_at,omitempty"`
}

type Workspace struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	OwnerUserID string `json:"owner_user_id"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
}

type WorkspaceMember struct {
	WorkspaceID string `json:"workspace_id"`
	UserID      string `json:"user_id"`
	Role        string `json:"role"`
	CreatedAt   int64  `json:"created_at"`
}

type Session struct {
	ID          string
	UserID      string
	WorkspaceID string
	TokenHash   string
	UserAgent   string
	IPAddress   string
	ExpiresAt   time.Time
	RevokedAt   *time.Time
	CreatedAt   time.Time
	LastSeenAt  time.Time
}

type AuditEvent struct {
	ID           string         `json:"id"`
	ActorUserID  *string        `json:"actor_user_id,omitempty"`
	TargetUserID *string        `json:"target_user_id,omitempty"`
	WorkspaceID  *string        `json:"workspace_id,omitempty"`
	Action       string         `json:"action"`
	Metadata     map[string]any `json:"metadata"`
	CreatedAt    int64          `json:"created_at"`
}

type CurrentUser struct {
	User               User      `json:"user"`
	Workspace          Workspace `json:"workspace"`
	MustChangePassword bool      `json:"must_change_password"`
	AvatarURL          string    `json:"avatar_url,omitempty"`
}

type UserProfile struct {
	UserID    string `json:"user_id"`
	Locale    string `json:"locale"`
	TimeZone  string `json:"time_zone"`
	UpdatedAt int64  `json:"updated_at"`
}

type UserAvatar struct {
	UserID    string `json:"-"`
	MIMEType  string `json:"mime_type"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Content   []byte `json:"-"`
	UpdatedAt int64  `json:"updated_at"`
}

type LoginRequest struct {
	Email      string `json:"email" binding:"required"`
	Password   string `json:"password" binding:"required"`
	RememberMe bool   `json:"remember_me"`
}

type LoginResponse struct {
	User      User      `json:"user"`
	Workspace Workspace `json:"workspace"`
}

type CreateUserRequest struct {
	Email             string `json:"email" binding:"required"`
	DisplayName       string `json:"display_name"`
	TemporaryPassword string `json:"temporary_password" binding:"required"`
	Role              string `json:"role" binding:"required"`
}

type UpdateUserRequest struct {
	Email       *string `json:"email"`
	DisplayName *string `json:"display_name"`
	Role        *string `json:"role"`
}

type ResetPasswordRequest struct {
	TemporaryPassword string `json:"temporary_password" binding:"required"`
}

type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password" binding:"required"`
	NewPassword     string `json:"new_password" binding:"required"`
}
