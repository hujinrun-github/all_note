package controlprofile

import (
	"context"
	"database/sql"
	"errors"
)

type EndpointSummary struct {
	ID               string
	SourceType       string
	Provider         string
	ProfileVersionID string
	HasCredentials   bool
}

func (r *Repository) EnsureFamily(ctx context.Context, workspaceID, familyID, kind, name, actorUserID string) error {
	var existingKind string
	err := r.db.QueryRowContext(ctx, r.bind(`SELECT kind FROM workspace_profile_families WHERE workspace_id=? AND id=?`), workspaceID, familyID).Scan(&existingKind)
	if err == nil {
		if existingKind != kind {
			return errors.New("profile family kind mismatch")
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return r.CreateFamily(ctx, workspaceID, familyID, kind, name, actorUserID)
}

func (r *Repository) GetEndpointSummary(ctx context.Context, workspaceID, kind, endpointID string) (EndpointSummary, error) {
	var summary EndpointSummary
	var systemID, workspaceIDValue sql.NullString
	err := r.db.QueryRowContext(ctx, r.bind(`SELECT id,source_type,system_profile_version_id,workspace_profile_version_id FROM workspace_service_endpoints WHERE workspace_id=? AND kind=? AND id=?`), workspaceID, kind, endpointID).
		Scan(&summary.ID, &summary.SourceType, &systemID, &workspaceIDValue)
	if err != nil {
		return EndpointSummary{}, err
	}
	var secret []byte
	if summary.SourceType == "system" {
		summary.ProfileVersionID = systemID.String
		err = r.db.QueryRowContext(ctx, r.bind(`SELECT provider,secret_ciphertext FROM system_profile_versions WHERE kind=? AND id=?`), kind, summary.ProfileVersionID).Scan(&summary.Provider, &secret)
	} else {
		summary.ProfileVersionID = workspaceIDValue.String
		err = r.db.QueryRowContext(ctx, r.bind(`SELECT provider,secret_ciphertext FROM workspace_profile_versions WHERE workspace_id=? AND kind=? AND id=?`), workspaceID, kind, summary.ProfileVersionID).Scan(&summary.Provider, &secret)
	}
	if err != nil {
		return EndpointSummary{}, err
	}
	summary.HasCredentials = len(secret) > 0
	return summary, nil
}

func (r *Repository) EndpointSource(ctx context.Context, workspaceID, kind, endpointID string) (string, error) {
	var source string
	err := r.db.QueryRowContext(ctx, r.bind(`SELECT source_type FROM workspace_service_endpoints WHERE workspace_id=? AND kind=? AND id=?`), workspaceID, kind, endpointID).Scan(&source)
	return source, err
}

func (r *Repository) WorkspaceOwnerID(ctx context.Context, workspaceID string) (string, error) {
	var userID string
	err := r.db.QueryRowContext(ctx, r.bind(`SELECT owner_user_id FROM workspaces WHERE id=?`), workspaceID).Scan(&userID)
	return userID, err
}
