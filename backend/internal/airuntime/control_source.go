package airuntime

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/hujinrun/flowspace/internal/credentials"
)

type ControlDialect string

const (
	ControlSQLite   ControlDialect = "sqlite"
	ControlPostgres ControlDialect = "postgres"
)

type ControlSource struct {
	db      *sql.DB
	dialect ControlDialect
	keyring *credentials.Keyring
}

func NewControlSource(db *sql.DB, dialect ControlDialect, keyring *credentials.Keyring) (*ControlSource, error) {
	if db == nil || keyring == nil {
		return nil, errors.New("AI control source database and keyring are required")
	}
	if dialect != ControlSQLite && dialect != ControlPostgres {
		return nil, errors.New("unsupported AI control source dialect")
	}
	return &ControlSource{db: db, dialect: dialect, keyring: keyring}, nil
}

func (s *ControlSource) LoadBinding(ctx context.Context, workspaceID, kind string) (Binding, error) {
	var binding Binding
	var endpointID sql.NullString
	err := s.db.QueryRowContext(ctx, s.bind(`SELECT kind,mode,endpoint_id,revision FROM workspace_service_bindings WHERE workspace_id=? AND kind=?`), workspaceID, kind).
		Scan(&binding.Kind, &binding.Mode, &endpointID, &binding.Revision)
	if err != nil {
		return Binding{}, err
	}
	binding.EndpointID = endpointID.String
	return binding, nil
}

func (s *ControlSource) LoadEndpointProfile(ctx context.Context, workspaceID, kind, endpointID string) (EndpointProfile, error) {
	var sourceType string
	var systemVersionID, workspaceVersionID sql.NullString
	err := s.db.QueryRowContext(ctx, s.bind(`SELECT source_type,system_profile_version_id,workspace_profile_version_id FROM workspace_service_endpoints WHERE workspace_id=? AND kind=? AND id=?`), workspaceID, kind, endpointID).
		Scan(&sourceType, &systemVersionID, &workspaceVersionID)
	if err != nil {
		return EndpointProfile{}, err
	}
	profile := EndpointProfile{EndpointID: endpointID, Kind: kind}
	var familyID, state, configJSON string
	var version int64
	var ciphertext, nonce []byte
	var keyID sql.NullString
	switch sourceType {
	case "system":
		profile.ProfileVersionID = systemVersionID.String
		err = s.db.QueryRowContext(ctx, s.bind(`SELECT family_id,version,provider,state,config_json,secret_ciphertext,secret_nonce,encryption_key_id FROM system_profile_versions WHERE kind=? AND id=?`), kind, profile.ProfileVersionID).
			Scan(&familyID, &version, &profile.Provider, &state, &configJSON, &ciphertext, &nonce, &keyID)
	case "custom":
		profile.ProfileVersionID = workspaceVersionID.String
		err = s.db.QueryRowContext(ctx, s.bind(`SELECT family_id,version,provider,state,config_json,secret_ciphertext,secret_nonce,encryption_key_id FROM workspace_profile_versions WHERE workspace_id=? AND kind=? AND id=?`), workspaceID, kind, profile.ProfileVersionID).
			Scan(&familyID, &version, &profile.Provider, &state, &configJSON, &ciphertext, &nonce, &keyID)
	default:
		return EndpointProfile{}, errors.New("unsupported endpoint source")
	}
	if err != nil {
		return EndpointProfile{}, err
	}
	if state != "verified" && state != "retired" {
		return EndpointProfile{}, errors.New("endpoint profile version is not usable")
	}
	profile.ConfigJSON = configJSON
	var config struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
		return EndpointProfile{}, errors.New("endpoint profile config is invalid")
	}
	profile.Model = config.Model
	if len(ciphertext) > 0 {
		if !keyID.Valid {
			return EndpointProfile{}, credentials.ErrKeyNotFound
		}
		aad := credentials.AAD{Scope: sourceType, FamilyID: familyID, VersionID: profile.ProfileVersionID, Kind: kind, Version: version}
		if sourceType == "custom" {
			aad.Scope = "workspace"
			aad.WorkspaceID = workspaceID
		}
		profile.Secret, err = s.keyring.Decrypt(credentials.EncryptedSecret{KeyID: keyID.String, Nonce: nonce, Ciphertext: ciphertext}, aad)
		if err != nil {
			return EndpointProfile{}, err
		}
	}
	return profile, nil
}

func (s *ControlSource) LoadFeatureSetting(ctx context.Context, workspaceID, feature string) (FeatureSetting, error) {
	var setting FeatureSetting
	if s.dialect == ControlSQLite {
		var enabled int
		err := s.db.QueryRowContext(ctx, `SELECT feature,enabled,fallback_mode FROM workspace_ai_feature_settings WHERE workspace_id=? AND feature=?`, workspaceID, feature).Scan(&setting.Feature, &enabled, &setting.FallbackMode)
		setting.Enabled = enabled == 1
		return setting, err
	}
	err := s.db.QueryRowContext(ctx, s.bind(`SELECT feature,enabled,fallback_mode FROM workspace_ai_feature_settings WHERE workspace_id=? AND feature=?`), workspaceID, feature).Scan(&setting.Feature, &setting.Enabled, &setting.FallbackMode)
	return setting, err
}

func (s *ControlSource) bind(query string) string {
	if s.dialect == ControlSQLite {
		return query
	}
	var builder strings.Builder
	index := 1
	for _, char := range query {
		if char == '?' {
			fmt.Fprintf(&builder, "$%d", index)
			index++
		} else {
			builder.WriteRune(char)
		}
	}
	return builder.String()
}

var _ Source = (*ControlSource)(nil)
