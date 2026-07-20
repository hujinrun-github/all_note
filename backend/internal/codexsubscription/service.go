package codexsubscription

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hujinrun/flowspace/internal/codexoauth"
	"github.com/hujinrun/flowspace/internal/controlprofile"
)

const defaultModel = "gpt-5.3-codex"

type Authorizer interface {
	CanManageWorkspace(context.Context, string, string) (bool, error)
}

type OAuthClient interface {
	Start(context.Context) (codexoauth.DeviceAuthorization, error)
	Poll(context.Context, string, string) (codexoauth.AuthorizationGrant, bool, error)
	Exchange(context.Context, codexoauth.AuthorizationGrant) (codexoauth.Tokens, error)
}

type StartResult struct {
	FlowID          string    `json:"flow_id"`
	UserCode        string    `json:"user_code"`
	VerificationURL string    `json:"verification_url"`
	IntervalSeconds int64     `json:"interval_seconds"`
	ExpiresAt       time.Time `json:"expires_at"`
}

type PollResult struct {
	Status           string `json:"status"`
	EndpointID       string `json:"endpoint_id,omitempty"`
	ProfileVersionID string `json:"profile_version_id,omitempty"`
}

type Service struct {
	oauth      OAuthClient
	flows      *codexoauth.Repository
	profiles   *controlprofile.Repository
	authorizer Authorizer
	now        func() time.Time
}

func New(oauth OAuthClient, flows *codexoauth.Repository, profiles *controlprofile.Repository, authorizer Authorizer) (*Service, error) {
	if oauth == nil || flows == nil || profiles == nil || authorizer == nil {
		return nil, errors.New("Codex subscription dependencies are required")
	}
	return &Service{oauth: oauth, flows: flows, profiles: profiles, authorizer: authorizer, now: time.Now}, nil
}

func (s *Service) Start(ctx context.Context, userID, workspaceID string) (StartResult, error) {
	if err := s.authorize(ctx, userID, workspaceID); err != nil {
		return StartResult{}, err
	}
	device, err := s.oauth.Start(ctx)
	if err != nil {
		return StartResult{}, err
	}
	flowID := uuid.NewString()
	flow := codexoauth.Flow{ID: flowID, WorkspaceID: workspaceID, UserID: userID, DeviceAuthID: device.DeviceAuthID, UserCode: device.UserCode, VerificationURL: device.VerificationURL, Interval: device.Interval, ExpiresAt: device.ExpiresAt}
	if err := s.flows.Create(ctx, flow); err != nil {
		return StartResult{}, err
	}
	return StartResult{FlowID: flowID, UserCode: device.UserCode, VerificationURL: device.VerificationURL, IntervalSeconds: int64(device.Interval / time.Second), ExpiresAt: device.ExpiresAt}, nil
}

func (s *Service) Poll(ctx context.Context, userID, workspaceID, flowID string, expectedRevision int64) (PollResult, error) {
	if err := s.authorize(ctx, userID, workspaceID); err != nil {
		return PollResult{}, err
	}
	flow, err := s.flows.Get(ctx, flowID, workspaceID, userID)
	if err != nil {
		return PollResult{}, err
	}
	versionID, endpointID := "codex-"+flow.ID, "custom-codex-"+flow.ID
	if flow.State == "authorized" {
		return PollResult{Status: "connected", EndpointID: endpointID, ProfileVersionID: versionID}, nil
	}
	if flow.State != "pending" {
		return PollResult{Status: flow.State}, nil
	}
	if !s.now().Before(flow.ExpiresAt) {
		_ = s.flows.Complete(ctx, flow.ID, workspaceID, userID, "expired")
		return PollResult{Status: "expired"}, nil
	}
	grant, pending, err := s.oauth.Poll(ctx, flow.DeviceAuthID, flow.UserCode)
	if err != nil {
		return PollResult{}, err
	}
	if pending {
		return PollResult{Status: "pending"}, nil
	}
	tokens, err := s.oauth.Exchange(ctx, grant)
	if err != nil {
		return PollResult{}, err
	}
	secret, err := json.Marshal(map[string]string{"access_token": tokens.AccessToken, "refresh_token": tokens.RefreshToken, "id_token": tokens.IDToken, "account_id": tokens.AccountID})
	if err != nil {
		return PollResult{}, err
	}
	defer clear(secret)
	config, _ := json.Marshal(map[string]string{"endpoint": codexoauth.DefaultBackendURL, "model": defaultModel, "auth_mode": "chatgpt", "api_mode": "codex_responses"})
	familyID := "codex-subscription-" + flow.ID
	if err := s.profiles.EnsureFamily(ctx, workspaceID, familyID, "llm_chat", "Codex 订阅", userID); err != nil {
		return PollResult{}, err
	}
	version, err := s.profiles.GetVersion(ctx, workspaceID, "llm_chat", versionID)
	if errors.Is(err, sql.ErrNoRows) {
		version, err = s.profiles.CreateVersion(ctx, controlprofile.CreateVersionInput{ID: versionID, FamilyID: familyID, WorkspaceID: workspaceID, Kind: "llm_chat", Provider: "openai_codex_subscription", ConfigJSON: string(config), Secret: secret, CreatedBy: userID})
	}
	if err != nil {
		return PollResult{}, err
	}
	if version.State == "draft" {
		if err := s.profiles.MarkVerified(ctx, workspaceID, "llm_chat", versionID); err != nil {
			return PollResult{}, err
		}
	}
	if err := s.ensureEndpoint(ctx, workspaceID, endpointID, versionID); err != nil {
		return PollResult{}, err
	}
	binding, err := s.profiles.GetBinding(ctx, workspaceID, "llm_chat")
	if err == nil && binding.Mode == "custom" && binding.EndpointID == endpointID {
		// A retry after activation is already complete.
	} else {
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return PollResult{}, err
		}
		if _, err := s.profiles.SetBinding(ctx, controlprofile.SetBindingInput{WorkspaceID: workspaceID, Kind: "llm_chat", Mode: "custom", EndpointSourceType: "custom", EndpointID: endpointID, ExpectedRevision: expectedRevision, ActorUserID: userID}); err != nil {
			return PollResult{}, err
		}
	}
	if err := s.flows.Complete(ctx, flow.ID, workspaceID, userID, "authorized"); err != nil {
		return PollResult{}, err
	}
	return PollResult{Status: "connected", EndpointID: endpointID, ProfileVersionID: versionID}, nil
}

func (s *Service) ensureEndpoint(ctx context.Context, workspaceID, endpointID, versionID string) error {
	source, err := s.profiles.EndpointSource(ctx, workspaceID, "llm_chat", endpointID)
	if err == nil {
		if source != "custom" {
			return fmt.Errorf("Codex endpoint identity conflict")
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return s.profiles.CreateWorkspaceEndpoint(ctx, workspaceID, endpointID, "llm_chat", versionID)
}

func (s *Service) authorize(ctx context.Context, userID, workspaceID string) error {
	ok, err := s.authorizer.CanManageWorkspace(ctx, userID, workspaceID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("workspace settings permission denied")
	}
	return nil
}
