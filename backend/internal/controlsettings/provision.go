package controlsettings

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/hujinrun/flowspace/internal/model"
)

type WorkspaceDefaultBinding struct {
	Kind             string
	Mode             string
	EndpointID       string
	ProfileVersionID string
}

type IdentityProvisioner struct {
	db       *sql.DB
	postgres bool
	defaults []WorkspaceDefaultBinding
}

func NewIdentityProvisioner(db *sql.DB, postgres bool, defaults []WorkspaceDefaultBinding) (*IdentityProvisioner, error) {
	if db == nil || len(defaults) != 4 {
		return nil, errors.New("control identity provisioner is incomplete")
	}
	return &IdentityProvisioner{db: db, postgres: postgres, defaults: append([]WorkspaceDefaultBinding(nil), defaults...)}, nil
}

func (p *IdentityProvisioner) Provision(ctx context.Context, user model.User) error {
	if p == nil {
		return errors.New("control identity provisioner is unavailable")
	}
	return ProvisionWorkspaceIdentity(ctx, p.db, p.postgres, user, p.defaults)
}

// ProvisionWorkspaceIdentity projects the authentication identity into the
// independent control store. It does not copy tenant content.
func ProvisionWorkspaceIdentity(ctx context.Context, db *sql.DB, postgres bool, user model.User, defaults []WorkspaceDefaultBinding) error {
	if db == nil || user.ID == "" || user.DefaultWorkspaceID == "" || len(defaults) != 4 {
		return errors.New("control identity is incomplete")
	}
	seen := make(map[string]bool, len(defaults))
	for _, item := range defaults {
		if seen[item.Kind] || item.EndpointID == "" || item.ProfileVersionID == "" || !validDefaultMode(item.Kind, item.Mode) {
			return errors.New("workspace system defaults are incomplete")
		}
		seen[item.Kind] = true
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	queries := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO users(id,email,display_name,password_hash,password_set,must_change_password,default_workspace_id,role,status) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`, []any{user.ID, user.Email, user.DisplayName, user.PasswordHash, user.PasswordSet, user.MustChangePassword, user.DefaultWorkspaceID, user.Role, user.Status}},
		{`INSERT INTO workspaces(id,name,owner_user_id) VALUES(?,?,?) ON CONFLICT(id) DO NOTHING`, []any{user.DefaultWorkspaceID, "默认工作空间", user.ID}},
		{`INSERT INTO workspace_members(workspace_id,user_id,role) VALUES(?,?,'owner') ON CONFLICT(workspace_id,user_id) DO UPDATE SET role='owner'`, []any{user.DefaultWorkspaceID, user.ID}},
		{`INSERT INTO workspace_runtime_state(workspace_id,mode,epoch,binding_revision,updated_by) VALUES(?,'active',1,1,?) ON CONFLICT(workspace_id) DO NOTHING`, []any{user.DefaultWorkspaceID, user.ID}},
		{`INSERT INTO workspace_ai_feature_settings(workspace_id,feature,enabled,fallback_mode,updated_by) VALUES(?,'roadmap_generation',1,'template',?) ON CONFLICT(workspace_id,feature) DO NOTHING`, []any{user.DefaultWorkspaceID, user.ID}},
		{`INSERT INTO workspace_ai_feature_settings(workspace_id,feature,enabled,fallback_mode,updated_by) VALUES(?,'japanese_furigana',1,'local',?) ON CONFLICT(workspace_id,feature) DO NOTHING`, []any{user.DefaultWorkspaceID, user.ID}},
	}
	for _, item := range defaults {
		queries = append(queries, struct {
			query string
			args  []any
		}{`INSERT INTO workspace_service_endpoints(id,workspace_id,kind,source_type,system_profile_version_id) VALUES(?,?,?,'system',?) ON CONFLICT(workspace_id,kind,source_type,id) DO NOTHING`, []any{item.EndpointID, user.DefaultWorkspaceID, item.Kind, item.ProfileVersionID}})
		var source, endpoint any
		if item.Mode == "default" {
			source, endpoint = "system", item.EndpointID
		}
		queries = append(queries, struct {
			query string
			args  []any
		}{`INSERT INTO workspace_service_bindings(workspace_id,kind,mode,endpoint_source_type,endpoint_id,settings_json,revision,updated_by) VALUES(?,?,?,?,?,'{}',1,?) ON CONFLICT(workspace_id,kind) DO NOTHING`, []any{user.DefaultWorkspaceID, item.Kind, item.Mode, source, endpoint, user.ID}})
	}
	for _, item := range queries {
		query := item.query
		if postgres {
			query = bindPostgres(query)
		}
		if _, err := tx.ExecContext(ctx, query, item.args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func validDefaultMode(kind, mode string) bool {
	if kind == "data_store" || kind == "object_s3" {
		return mode == "default"
	}
	if kind == "llm_chat" || kind == "llm_transcription" {
		return mode == "default" || mode == "disabled"
	}
	return false
}

func bindPostgres(query string) string {
	result := make([]byte, 0, len(query)+8)
	index := 1
	for i := 0; i < len(query); i++ {
		if query[i] == '?' {
			result = append(result, '$')
			result = append(result, []byte(fmtInt(index))...)
			index++
		} else {
			result = append(result, query[i])
		}
	}
	return string(result)
}

func fmtInt(value int) string {
	if value < 10 {
		return string(rune('0' + value))
	}
	return fmt.Sprintf("%d", value)
}
