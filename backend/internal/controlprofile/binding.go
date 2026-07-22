package controlprofile

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

var ErrBindingCASConflict = errors.New("service binding revision conflict")

type Binding struct {
	WorkspaceID        string
	Kind               string
	Mode               string
	EndpointSourceType string
	EndpointID         string
	SettingsJSON       string
	Revision           int64
}

type SetBindingInput struct {
	WorkspaceID             string
	Kind                    string
	Mode                    string
	EndpointSourceType      string
	EndpointID              string
	SettingsJSON            string
	ExpectedRevision        int64
	ExpectedRuntimeRevision int64
	ActorUserID             string
}

func (r *Repository) CreateWorkspaceEndpoint(ctx context.Context, workspaceID, endpointID, kind, versionID string) error {
	var state string
	err := r.db.QueryRowContext(ctx, r.bind(`SELECT state FROM workspace_profile_versions WHERE workspace_id=? AND kind=? AND id=?`), workspaceID, kind, versionID).Scan(&state)
	if err != nil {
		return err
	}
	if state != "verified" {
		return fmt.Errorf("workspace profile version is not verified")
	}
	_, err = r.db.ExecContext(ctx, r.bind(`INSERT INTO workspace_service_endpoints(id,workspace_id,kind,source_type,workspace_profile_version_id) VALUES(?,?,?,'custom',?)`), endpointID, workspaceID, kind, versionID)
	return err
}

func (r *Repository) CreateSystemEndpoint(ctx context.Context, workspaceID, endpointID, kind, versionID string) error {
	var state string
	err := r.db.QueryRowContext(ctx, r.bind(`SELECT state FROM system_profile_versions WHERE kind=? AND id=?`), kind, versionID).Scan(&state)
	if err != nil {
		return err
	}
	if state != "verified" {
		return fmt.Errorf("system profile version is not verified")
	}
	_, err = r.db.ExecContext(ctx, r.bind(`INSERT INTO workspace_service_endpoints(id,workspace_id,kind,source_type,system_profile_version_id) VALUES(?,?,?,'system',?)`), endpointID, workspaceID, kind, versionID)
	return err
}

func (r *Repository) SetBinding(ctx context.Context, input SetBindingInput) (Binding, error) {
	if input.WorkspaceID == "" || input.ActorUserID == "" || input.ExpectedRuntimeRevision <= 0 || !validKind(input.Kind) || !validBindingMode(input.Kind, input.Mode) {
		return Binding{}, errors.New("invalid service binding")
	}
	if input.SettingsJSON == "" {
		input.SettingsJSON = `{}`
	}
	var source any
	var endpoint any
	if input.Mode == "disabled" || input.Mode == "reuse_chat" {
		if input.EndpointSourceType != "" || input.EndpointID != "" {
			return Binding{}, errors.New("binding mode cannot contain an endpoint")
		}
	} else {
		if input.EndpointID == "" || (input.EndpointSourceType != "system" && input.EndpointSourceType != "custom") {
			return Binding{}, errors.New("binding endpoint is required")
		}
		if (input.Mode == "default") != (input.EndpointSourceType == "system") {
			return Binding{}, errors.New("binding mode and endpoint source disagree")
		}
		source, endpoint = input.EndpointSourceType, input.EndpointID
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Binding{}, err
	}
	defer tx.Rollback()
	if input.ExpectedRevision == 0 {
		_, err := tx.ExecContext(ctx, r.bind(`INSERT INTO workspace_service_bindings(workspace_id,kind,mode,endpoint_source_type,endpoint_id,settings_json,revision,updated_by) VALUES(?,?,?,?,?,?,1,?)`), input.WorkspaceID, input.Kind, input.Mode, source, endpoint, input.SettingsJSON, input.ActorUserID)
		if err != nil {
			return Binding{}, err
		}
	} else {
		result, err := tx.ExecContext(ctx, r.bind(`UPDATE workspace_service_bindings SET mode=?,endpoint_source_type=?,endpoint_id=?,settings_json=?,revision=revision+1,updated_by=?,updated_at=CURRENT_TIMESTAMP WHERE workspace_id=? AND kind=? AND revision=?`), input.Mode, source, endpoint, input.SettingsJSON, input.ActorUserID, input.WorkspaceID, input.Kind, input.ExpectedRevision)
		if err != nil {
			return Binding{}, err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return Binding{}, err
		}
		if count != 1 {
			return Binding{}, ErrBindingCASConflict
		}
	}
	result, err := tx.ExecContext(ctx, r.bind(`UPDATE workspace_runtime_state SET binding_revision=binding_revision+1,updated_by=?,updated_at=CURRENT_TIMESTAMP WHERE workspace_id=? AND mode='active' AND binding_revision=? AND storage_operation_id IS NULL`), input.ActorUserID, input.WorkspaceID, input.ExpectedRuntimeRevision)
	if err != nil {
		return Binding{}, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return Binding{}, err
	}
	if count != 1 {
		return Binding{}, ErrBindingCASConflict
	}
	binding, err := r.getBinding(ctx, tx, input.WorkspaceID, input.Kind)
	if err != nil {
		return Binding{}, err
	}
	if err := tx.Commit(); err != nil {
		return Binding{}, err
	}
	return binding, nil
}

func (r *Repository) GetBinding(ctx context.Context, workspaceID, kind string) (Binding, error) {
	return r.getBinding(ctx, r.db, workspaceID, kind)
}

type bindingQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (r *Repository) getBinding(ctx context.Context, queryer bindingQueryer, workspaceID, kind string) (Binding, error) {
	var binding Binding
	var source, endpoint sql.NullString
	err := queryer.QueryRowContext(ctx, r.bind(`SELECT workspace_id,kind,mode,endpoint_source_type,endpoint_id,settings_json,revision FROM workspace_service_bindings WHERE workspace_id=? AND kind=?`), workspaceID, kind).
		Scan(&binding.WorkspaceID, &binding.Kind, &binding.Mode, &source, &endpoint, &binding.SettingsJSON, &binding.Revision)
	if err != nil {
		return Binding{}, err
	}
	binding.EndpointSourceType = source.String
	binding.EndpointID = endpoint.String
	return binding, nil
}

func (r *Repository) RetireWorkspaceVersion(ctx context.Context, workspaceID, kind, versionID string) error {
	result, err := r.db.ExecContext(ctx, r.bind(`UPDATE workspace_profile_versions SET state='retired' WHERE workspace_id=? AND kind=? AND id=? AND state IN ('draft','verified')`), workspaceID, kind, versionID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return errors.New("profile version retire state conflict")
	}
	return nil
}

func validBindingMode(kind, mode string) bool {
	switch kind {
	case "data_store", "object_s3":
		return mode == "default" || mode == "custom"
	case "llm_chat":
		return mode == "default" || mode == "custom" || mode == "disabled"
	case "llm_transcription":
		return mode == "default" || mode == "custom" || mode == "reuse_chat" || mode == "disabled"
	default:
		return false
	}
}
