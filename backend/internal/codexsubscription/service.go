package codexsubscription

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hujinrun/flowspace/internal/airuntime"
	"github.com/hujinrun/flowspace/internal/codexoauth"
	"github.com/hujinrun/flowspace/internal/controlprofile"
	"github.com/hujinrun/flowspace/internal/runtimecontrol"
)

const defaultModel = "gpt-5.3-codex"

type Authorizer interface {
	CanManageWorkspace(context.Context, string, string) (bool, error)
}

type OAuthClient interface {
	Start(context.Context) (codexoauth.DeviceAuthorization, error)
	Poll(context.Context, string, string) (codexoauth.AuthorizationGrant, bool, error)
	Exchange(context.Context, codexoauth.AuthorizationGrant) (codexoauth.Tokens, error)
	Refresh(context.Context, string) (codexoauth.Tokens, error)
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
	runtime    *runtimecontrol.Repository
	authorizer Authorizer
	now        func() time.Time
}

func New(oauth OAuthClient, flows *codexoauth.Repository, profiles *controlprofile.Repository, runtimeRepository *runtimecontrol.Repository, authorizer Authorizer) (*Service, error) {
	if oauth == nil || flows == nil || profiles == nil || runtimeRepository == nil || authorizer == nil {
		return nil, errors.New("Codex subscription dependencies are required")
	}
	return &Service{oauth: oauth, flows: flows, profiles: profiles, runtime: runtimeRepository, authorizer: authorizer, now: time.Now}, nil
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

func (s *Service) Poll(ctx context.Context, userID, workspaceID, flowID string, expectedRevision, expectedRuntimeRevision int64) (PollResult, error) {
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
	if tokens.ExpiresAt.IsZero() {
		tokens.ExpiresAt = s.now().Add(time.Hour)
	}
	secret, err := json.Marshal(map[string]string{"access_token": tokens.AccessToken, "refresh_token": tokens.RefreshToken, "id_token": tokens.IDToken, "account_id": tokens.AccountID, "expires_at": tokens.ExpiresAt.UTC().Format(time.RFC3339Nano)})
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
		if _, err := s.profiles.SetBinding(ctx, controlprofile.SetBindingInput{WorkspaceID: workspaceID, Kind: "llm_chat", Mode: "custom", EndpointSourceType: "custom", EndpointID: endpointID, ExpectedRevision: expectedRevision, ExpectedRuntimeRevision: expectedRuntimeRevision, ActorUserID: userID}); err != nil {
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

func (s *Service) RefreshCodexCredentials(ctx context.Context, workspaceID string, resolved airuntime.ResolvedCapability) (airuntime.ResolvedCapability, error) {
	if resolved.Provider != "openai_codex_subscription" || resolved.ProfileVersionID == "" || resolved.EndpointID == "" {
		return airuntime.ResolvedCapability{}, errors.New("Codex credential refresh identity is incomplete")
	}
	var current struct {
		RefreshToken string `json:"refresh_token"`
		AccountID    string `json:"account_id"`
	}
	if json.Unmarshal(resolved.Secret, &current) != nil || current.RefreshToken == "" {
		return airuntime.ResolvedCapability{}, errors.New("Codex refresh token is unavailable")
	}
	tokens, err := s.oauth.Refresh(ctx, current.RefreshToken)
	if err != nil {
		return airuntime.ResolvedCapability{}, err
	}
	if tokens.RefreshToken == "" {
		tokens.RefreshToken = current.RefreshToken
	}
	if tokens.AccountID == "" {
		tokens.AccountID = current.AccountID
	}
	if tokens.ExpiresAt.IsZero() {
		tokens.ExpiresAt = s.now().Add(time.Hour)
	}
	secret, err := json.Marshal(map[string]string{
		"access_token": tokens.AccessToken, "refresh_token": tokens.RefreshToken, "id_token": tokens.IDToken,
		"account_id": tokens.AccountID, "expires_at": tokens.ExpiresAt.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return airuntime.ResolvedCapability{}, err
	}
	defer clear(secret)
	currentVersion, err := s.profiles.GetVersion(ctx, workspaceID, "llm_chat", resolved.ProfileVersionID)
	if err != nil {
		return airuntime.ResolvedCapability{}, err
	}
	binding, err := s.profiles.GetBinding(ctx, workspaceID, "llm_chat")
	if err != nil {
		return airuntime.ResolvedCapability{}, err
	}
	if binding.EndpointID != resolved.EndpointID || binding.Mode != "custom" {
		return airuntime.ResolvedCapability{}, controlprofile.ErrBindingCASConflict
	}
	runtimeState, err := s.runtime.Get(ctx, workspaceID)
	if err != nil {
		return airuntime.ResolvedCapability{}, err
	}
	actorUserID, err := s.profiles.WorkspaceOwnerID(ctx, workspaceID)
	if err != nil {
		return airuntime.ResolvedCapability{}, err
	}
	versionID := "codex-refresh-" + uuid.NewString()
	version, err := s.profiles.CreateVersion(ctx, controlprofile.CreateVersionInput{
		ID: versionID, FamilyID: currentVersion.FamilyID, WorkspaceID: workspaceID, Kind: "llm_chat",
		Provider: currentVersion.Provider, ConfigJSON: currentVersion.ConfigJSON, Secret: secret, CreatedBy: actorUserID,
	})
	if err != nil {
		return airuntime.ResolvedCapability{}, err
	}
	if err := s.profiles.MarkVerified(ctx, workspaceID, "llm_chat", version.ID); err != nil {
		return airuntime.ResolvedCapability{}, err
	}
	endpointID := "custom-" + version.ID
	if err := s.profiles.CreateWorkspaceEndpoint(ctx, workspaceID, endpointID, "llm_chat", version.ID); err != nil {
		return airuntime.ResolvedCapability{}, err
	}
	if _, err := s.profiles.SetBinding(ctx, controlprofile.SetBindingInput{
		WorkspaceID: workspaceID, Kind: "llm_chat", Mode: "custom", EndpointSourceType: "custom", EndpointID: endpointID,
		ExpectedRevision: binding.Revision, ExpectedRuntimeRevision: runtimeState.BindingRevision, ActorUserID: actorUserID,
	}); err != nil {
		return airuntime.ResolvedCapability{}, err
	}
	refreshed := resolved
	refreshed.EndpointID = endpointID
	refreshed.ProfileVersionID = version.ID
	refreshed.Secret = append([]byte(nil), secret...)
	return refreshed, nil
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
