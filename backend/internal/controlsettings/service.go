package controlsettings

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/hujinrun/flowspace/internal/controlprofile"
	"github.com/hujinrun/flowspace/internal/handler"
	"github.com/hujinrun/flowspace/internal/runtimecontrol"
)

var ErrForbidden = errors.New("workspace settings permission denied")

type Authorizer interface {
	CanManageWorkspace(context.Context, string, string) (bool, error)
}

type ProbeResult struct {
	Code, Message, InstallationID, SchemaIdentity string
}

type Prober interface {
	Probe(context.Context, string, string, []byte, []byte) (ProbeResult, error)
}

type Service struct {
	profiles   *controlprofile.Repository
	runtime    *runtimecontrol.Repository
	authorizer Authorizer
	prober     Prober
}

func New(profiles *controlprofile.Repository, runtimeRepository *runtimecontrol.Repository, authorizer Authorizer, prober Prober) (*Service, error) {
	if profiles == nil || runtimeRepository == nil || authorizer == nil || prober == nil {
		return nil, errors.New("control settings dependencies are required")
	}
	return &Service{profiles: profiles, runtime: runtimeRepository, authorizer: authorizer, prober: prober}, nil
}

func (s *Service) GetRuntimeSettings(ctx context.Context, userID, workspaceID string) (handler.RuntimeSettingsDTO, error) {
	if err := s.authorize(ctx, userID, workspaceID); err != nil {
		return handler.RuntimeSettingsDTO{}, err
	}
	state, err := s.runtime.Get(ctx, workspaceID)
	if err != nil {
		return handler.RuntimeSettingsDTO{}, err
	}
	result := handler.RuntimeSettingsDTO{WorkspaceID: workspaceID, Mode: state.Mode, Epoch: state.Epoch, BindingRevision: state.BindingRevision}
	for _, kind := range []string{"data_store", "object_s3", "llm_chat", "llm_transcription"} {
		binding, err := s.profiles.GetBinding(ctx, workspaceID, kind)
		if errors.Is(err, sql.ErrNoRows) {
			result.Bindings = append(result.Bindings, handler.ServiceBindingDTO{Kind: kind, Mode: "default"})
			continue
		}
		if err != nil {
			return handler.RuntimeSettingsDTO{}, err
		}
		dto := handler.ServiceBindingDTO{Kind: kind, Mode: binding.Mode, EndpointID: binding.EndpointID, Revision: binding.Revision}
		if binding.EndpointID != "" {
			endpoint, err := s.profiles.GetEndpointSummary(ctx, workspaceID, kind, binding.EndpointID)
			if err != nil {
				return handler.RuntimeSettingsDTO{}, err
			}
			dto.Provider, dto.ProfileVersionID, dto.HasCredentials = endpoint.Provider, endpoint.ProfileVersionID, endpoint.HasCredentials
		}
		result.Bindings = append(result.Bindings, dto)
	}
	return result, nil
}

func (s *Service) TestProfile(ctx context.Context, userID, workspaceID string, request handler.TestServiceProfileRequest) (handler.TestServiceProfileResult, error) {
	if err := s.authorize(ctx, userID, workspaceID); err != nil {
		return handler.TestServiceProfileResult{}, err
	}
	if err := validateProfileConfig(request.Kind, request.Config); err != nil {
		return handler.TestServiceProfileResult{}, err
	}
	if err := validateProfileSecret(request.Kind, request.Secret); err != nil {
		return handler.TestServiceProfileResult{}, err
	}
	configJSON, err := json.Marshal(request.Config)
	if err != nil {
		return handler.TestServiceProfileResult{}, err
	}
	secret := []byte(request.Secret)
	defer clear(secret)
	result, err := s.prober.Probe(ctx, request.Kind, request.Provider, configJSON, secret)
	if err != nil {
		return handler.TestServiceProfileResult{}, err
	}
	return handler.TestServiceProfileResult{OK: true, Code: result.Code, Message: result.Message, Installation: result.InstallationID, Schema: result.SchemaIdentity}, nil
}

func (s *Service) SaveProfile(ctx context.Context, userID, workspaceID string, request handler.SaveServiceProfileRequest) (handler.SavedServiceProfileDTO, error) {
	if err := s.authorize(ctx, userID, workspaceID); err != nil {
		return handler.SavedServiceProfileDTO{}, err
	}
	if err := validateProfileConfig(request.Kind, request.Config); err != nil {
		return handler.SavedServiceProfileDTO{}, err
	}
	if err := validateProfileSecret(request.Kind, request.Secret); err != nil {
		return handler.SavedServiceProfileDTO{}, err
	}
	configJSON, err := json.Marshal(request.Config)
	if err != nil {
		return handler.SavedServiceProfileDTO{}, err
	}
	if err := s.profiles.EnsureFamily(ctx, workspaceID, request.FamilyID, request.Kind, request.Name, userID); err != nil {
		return handler.SavedServiceProfileDTO{}, err
	}
	secret := []byte(request.Secret)
	defer clear(secret)
	version, err := s.profiles.CreateVersion(ctx, controlprofile.CreateVersionInput{ID: request.ID, FamilyID: request.FamilyID, WorkspaceID: workspaceID, Kind: request.Kind, Provider: request.Provider, ConfigJSON: string(configJSON), Secret: secret, PreserveFromVersionID: request.PreserveFromVersionID, CreatedBy: userID})
	if err != nil {
		return handler.SavedServiceProfileDTO{}, err
	}
	return handler.SavedServiceProfileDTO{ID: version.ID, FamilyID: version.FamilyID, Kind: version.Kind, Version: version.Version, State: version.State, HasCredentials: version.HasSecret}, nil
}

var postgresIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,62}$`)
var bucketName = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)

func validateProfileConfig(kind string, profile map[string]any) error {
	stringField := func(name string) string { value, _ := profile[name].(string); return strings.TrimSpace(value) }
	switch kind {
	case "data_store":
		schema := stringField("schema")
		if schema == "" {
			schema = "public"
			profile["schema"] = schema
		}
		if !postgresIdentifier.MatchString(schema) {
			return fmt.Errorf("invalid PostgreSQL schema name")
		}
	case "object_s3":
		bucket := stringField("bucket")
		if !bucketName.MatchString(bucket) || strings.Contains(bucket, "..") {
			return fmt.Errorf("invalid object bucket name")
		}
	case "llm_transcription":
		if stringField("endpoint") == "" {
			return errors.New("transcription service endpoint is required")
		}
	}
	return nil
}

type objectCredentials struct {
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"`
}

func validateProfileSecret(kind, secret string) error {
	if kind != "object_s3" {
		return nil
	}
	var credentials objectCredentials
	if json.Unmarshal([]byte(secret), &credentials) != nil || strings.TrimSpace(credentials.AccessKey) == "" || strings.TrimSpace(credentials.SecretKey) == "" {
		return errors.New("object storage access key and secret key are required")
	}
	return nil
}

func (s *Service) VerifyProfile(ctx context.Context, userID, workspaceID, kind, versionID string) (handler.VerifiedServiceProfileDTO, error) {
	if err := s.authorize(ctx, userID, workspaceID); err != nil {
		return handler.VerifiedServiceProfileDTO{}, err
	}
	version, err := s.profiles.GetVersion(ctx, workspaceID, kind, versionID)
	if err != nil {
		return handler.VerifiedServiceProfileDTO{}, err
	}
	secret := []byte(nil)
	if version.HasSecret {
		secret, err = s.profiles.DecryptSecret(ctx, workspaceID, kind, versionID)
		if err != nil {
			return handler.VerifiedServiceProfileDTO{}, err
		}
		defer clear(secret)
	}
	if _, err := s.prober.Probe(ctx, kind, version.Provider, []byte(version.ConfigJSON), secret); err != nil {
		return handler.VerifiedServiceProfileDTO{}, err
	}
	if err := s.profiles.MarkVerified(ctx, workspaceID, kind, versionID); err != nil {
		return handler.VerifiedServiceProfileDTO{}, err
	}
	endpointID := "custom-" + versionID
	if err := s.profiles.CreateWorkspaceEndpoint(ctx, workspaceID, endpointID, kind, versionID); err != nil {
		return handler.VerifiedServiceProfileDTO{}, err
	}
	return handler.VerifiedServiceProfileDTO{EndpointID: endpointID, ProfileVersionID: versionID, Kind: kind}, nil
}

func (s *Service) SetBinding(ctx context.Context, userID, workspaceID, kind string, request handler.SetServiceBindingRequest) (handler.ServiceBindingDTO, error) {
	if err := s.authorize(ctx, userID, workspaceID); err != nil {
		return handler.ServiceBindingDTO{}, err
	}
	if kind == "data_store" {
		return handler.ServiceBindingDTO{}, handler.ErrDatabaseMigrationRequired
	}
	source := ""
	if request.EndpointID != "" {
		var err error
		source, err = s.profiles.EndpointSource(ctx, workspaceID, kind, request.EndpointID)
		if err != nil {
			return handler.ServiceBindingDTO{}, err
		}
	}
	binding, err := s.profiles.SetBinding(ctx, controlprofile.SetBindingInput{WorkspaceID: workspaceID, Kind: kind, Mode: request.Mode, EndpointSourceType: source, EndpointID: request.EndpointID, ExpectedRevision: request.ExpectedRevision, ExpectedRuntimeRevision: request.ExpectedRuntimeRevision, ActorUserID: userID})
	if errors.Is(err, controlprofile.ErrBindingCASConflict) {
		return handler.ServiceBindingDTO{}, handler.ErrSettingsRevisionConflict
	}
	if err != nil {
		return handler.ServiceBindingDTO{}, err
	}
	dto := handler.ServiceBindingDTO{Kind: kind, Mode: binding.Mode, EndpointID: binding.EndpointID, Revision: binding.Revision}
	if binding.EndpointID != "" {
		endpoint, err := s.profiles.GetEndpointSummary(ctx, workspaceID, kind, binding.EndpointID)
		if err != nil {
			return handler.ServiceBindingDTO{}, err
		}
		dto.Provider, dto.ProfileVersionID, dto.HasCredentials = endpoint.Provider, endpoint.ProfileVersionID, endpoint.HasCredentials
	}
	return dto, nil
}

func (s *Service) authorize(ctx context.Context, userID, workspaceID string) error {
	ok, err := s.authorizer.CanManageWorkspace(ctx, userID, workspaceID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrForbidden
	}
	return nil
}

var _ handler.WorkspaceSettingsService = (*Service)(nil)
