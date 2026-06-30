package auth

import "errors"

var (
	ErrMissingWorkspace       = errors.New("missing workspace scope")
	ErrMissingIdentity        = errors.New("missing request identity")
	ErrInvalidCredentials     = errors.New("invalid credentials")
	ErrAccountDisabled        = errors.New("account disabled")
	ErrPasswordChangeRequired = errors.New("password change required")
	ErrWorkspaceAccessRevoked = errors.New("workspace access revoked")
	ErrLastAdminRequired      = errors.New("last active admin required")
	ErrEmailAlreadyExists     = errors.New("email already exists")
)
