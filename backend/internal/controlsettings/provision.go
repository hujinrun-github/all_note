package controlsettings

import (
	"context"
	"database/sql"
	"errors"

	"github.com/hujinrun/flowspace/internal/model"
)

// ProvisionWorkspaceIdentity projects the authentication identity into the
// independent control store. It does not copy tenant content.
func ProvisionWorkspaceIdentity(ctx context.Context, db *sql.DB, postgres bool, user model.User) error {
	if db == nil || user.ID == "" || user.DefaultWorkspaceID == "" {
		return errors.New("control identity is incomplete")
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
		{`INSERT INTO users(id,email,display_name,password_hash,password_set,must_change_password,default_workspace_id,role,status) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET email=excluded.email,display_name=excluded.display_name,default_workspace_id=excluded.default_workspace_id,role=excluded.role,status=excluded.status,updated_at=CURRENT_TIMESTAMP`, []any{user.ID, user.Email, user.DisplayName, user.PasswordHash, user.PasswordSet, user.MustChangePassword, user.DefaultWorkspaceID, user.Role, user.Status}},
		{`INSERT INTO workspaces(id,name,owner_user_id) VALUES(?,?,?) ON CONFLICT(id) DO NOTHING`, []any{user.DefaultWorkspaceID, "默认工作空间", user.ID}},
		{`INSERT INTO workspace_members(workspace_id,user_id,role) VALUES(?,?,'owner') ON CONFLICT(workspace_id,user_id) DO UPDATE SET role='owner'`, []any{user.DefaultWorkspaceID, user.ID}},
		{`INSERT INTO workspace_runtime_state(workspace_id,mode,epoch,binding_revision,updated_by) VALUES(?,'active',1,1,?) ON CONFLICT(workspace_id) DO NOTHING`, []any{user.DefaultWorkspaceID, user.ID}},
		{`INSERT INTO workspace_service_bindings(workspace_id,kind,mode,settings_json,revision,updated_by) VALUES(?,'llm_chat','disabled','{}',1,?) ON CONFLICT(workspace_id,kind) DO NOTHING`, []any{user.DefaultWorkspaceID, user.ID}},
		{`INSERT INTO workspace_service_bindings(workspace_id,kind,mode,settings_json,revision,updated_by) VALUES(?,'llm_transcription','disabled','{}',1,?) ON CONFLICT(workspace_id,kind) DO NOTHING`, []any{user.DefaultWorkspaceID, user.ID}},
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
	return "10"
}
