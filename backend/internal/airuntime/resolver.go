package airuntime

import (
	"context"
	"errors"
	"fmt"
)

var (
	ErrCapabilityDisabled       = errors.New("AI capability is disabled")
	ErrConfigurationUnavailable = errors.New("AI configuration is unavailable")
)

type Binding struct {
	Kind       string
	Mode       string
	EndpointID string
	Revision   int64
}

type EndpointProfile struct {
	EndpointID       string
	Kind             string
	ProfileVersionID string
	Provider         string
	Model            string
	ConfigJSON       string
	Secret           []byte
}

type FeatureSetting struct {
	Feature      string
	Enabled      bool
	FallbackMode string
}

type Source interface {
	LoadBinding(context.Context, string, string) (Binding, error)
	LoadEndpointProfile(context.Context, string, string, string) (EndpointProfile, error)
	LoadFeatureSetting(context.Context, string, string) (FeatureSetting, error)
}

type ResolvedCapability struct {
	Enabled          bool
	Mode             string
	ReusedChat       bool
	EndpointID       string
	ProfileVersionID string
	Provider         string
	Model            string
	ConfigJSON       string
	Secret           []byte
}

type Resolver struct{ source Source }

func NewResolver(source Source) (*Resolver, error) {
	if source == nil {
		return nil, errors.New("AI runtime source is required")
	}
	return &Resolver{source: source}, nil
}

func (r *Resolver) ResolveChat(ctx context.Context, workspaceID string) (ResolvedCapability, error) {
	return r.resolveDirect(ctx, workspaceID, "llm_chat")
}

func (r *Resolver) ResolveTranscription(ctx context.Context, workspaceID string) (ResolvedCapability, error) {
	binding, err := r.source.LoadBinding(ctx, workspaceID, "llm_transcription")
	if err != nil {
		return ResolvedCapability{}, unavailable("load transcription binding", err)
	}
	switch binding.Mode {
	case "disabled":
		return ResolvedCapability{Mode: "disabled"}, ErrCapabilityDisabled
	case "reuse_chat":
		chat, err := r.ResolveChat(ctx, workspaceID)
		if err != nil {
			return ResolvedCapability{}, err
		}
		chat.Mode = "reuse_chat"
		chat.ReusedChat = true
		return chat, nil
	case "default", "custom":
		return r.resolveBinding(ctx, workspaceID, binding)
	default:
		return ResolvedCapability{}, unavailable("invalid transcription mode", nil)
	}
}

func (r *Resolver) ResolveFeature(ctx context.Context, workspaceID, feature string) (FeatureSetting, error) {
	setting, err := r.source.LoadFeatureSetting(ctx, workspaceID, feature)
	if err != nil {
		return FeatureSetting{}, unavailable("load AI feature setting", err)
	}
	if setting.Feature != feature || !validFeatureFallback(feature, setting.FallbackMode) {
		return FeatureSetting{}, unavailable("invalid AI feature setting", nil)
	}
	return setting, nil
}

func (r *Resolver) resolveDirect(ctx context.Context, workspaceID, kind string) (ResolvedCapability, error) {
	binding, err := r.source.LoadBinding(ctx, workspaceID, kind)
	if err != nil {
		return ResolvedCapability{}, unavailable("load AI binding", err)
	}
	if binding.Mode == "disabled" {
		return ResolvedCapability{Mode: "disabled"}, ErrCapabilityDisabled
	}
	if binding.Mode != "default" && binding.Mode != "custom" {
		return ResolvedCapability{}, unavailable("invalid AI binding mode", nil)
	}
	return r.resolveBinding(ctx, workspaceID, binding)
}

func (r *Resolver) resolveBinding(ctx context.Context, workspaceID string, binding Binding) (ResolvedCapability, error) {
	if binding.EndpointID == "" {
		return ResolvedCapability{}, unavailable("AI endpoint is missing", nil)
	}
	profile, err := r.source.LoadEndpointProfile(ctx, workspaceID, binding.Kind, binding.EndpointID)
	if err != nil {
		return ResolvedCapability{}, unavailable("load AI endpoint", err)
	}
	if profile.EndpointID != binding.EndpointID || profile.Kind != binding.Kind || profile.ProfileVersionID == "" {
		return ResolvedCapability{}, unavailable("AI endpoint identity mismatch", nil)
	}
	return ResolvedCapability{
		Enabled: true, Mode: binding.Mode, EndpointID: profile.EndpointID, ProfileVersionID: profile.ProfileVersionID,
		Provider: profile.Provider, Model: profile.Model, ConfigJSON: profile.ConfigJSON, Secret: append([]byte(nil), profile.Secret...),
	}, nil
}

func unavailable(action string, cause error) error {
	if cause == nil {
		return fmt.Errorf("%w: %s", ErrConfigurationUnavailable, action)
	}
	return fmt.Errorf("%w: %s", ErrConfigurationUnavailable, action)
}

func validFeatureFallback(feature, fallback string) bool {
	return (feature == "roadmap_generation" && (fallback == "error" || fallback == "template")) ||
		(feature == "japanese_furigana" && (fallback == "error" || fallback == "local"))
}
