package airuntime

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeSource struct {
	bindings map[string]Binding
	profiles map[string]EndpointProfile
	features map[string]FeatureSetting
	err      error
}

func (f *fakeSource) LoadBinding(context.Context, string, string) (Binding, error) {
	panic("use keyed fake")
}

type keyedSource struct{ *fakeSource }

func (f keyedSource) LoadBinding(_ context.Context, _ string, kind string) (Binding, error) {
	if f.err != nil {
		return Binding{}, f.err
	}
	return f.bindings[kind], nil
}
func (f keyedSource) LoadEndpointProfile(_ context.Context, _, _, endpointID string) (EndpointProfile, error) {
	if f.err != nil {
		return EndpointProfile{}, f.err
	}
	return f.profiles[endpointID], nil
}
func (f keyedSource) LoadFeatureSetting(_ context.Context, _, feature string) (FeatureSetting, error) {
	if f.err != nil {
		return FeatureSetting{}, f.err
	}
	return f.features[feature], nil
}

// Satisfy the embedded fake's unused methods explicitly through keyedSource.
func (f *fakeSource) LoadEndpointProfile(context.Context, string, string, string) (EndpointProfile, error) {
	return EndpointProfile{}, f.err
}
func (f *fakeSource) LoadFeatureSetting(context.Context, string, string) (FeatureSetting, error) {
	return FeatureSetting{}, f.err
}

func TestResolveExplicitAIModes(t *testing.T) {
	source := keyedSource{&fakeSource{
		bindings: map[string]Binding{
			"llm_chat":          {Kind: "llm_chat", Mode: "custom", EndpointID: "chat"},
			"llm_transcription": {Kind: "llm_transcription", Mode: "reuse_chat"},
		},
		profiles: map[string]EndpointProfile{"chat": {EndpointID: "chat", Kind: "llm_chat", ProfileVersionID: "chat-v1", Provider: "openai", Model: "gpt-test", Secret: []byte("api-secret")}},
		features: map[string]FeatureSetting{"roadmap_generation": {Feature: "roadmap_generation", Enabled: true, FallbackMode: "template"}},
	}}
	resolver, _ := NewResolver(source)
	chat, err := resolver.ResolveChat(context.Background(), "w1")
	if err != nil || !chat.Enabled || chat.Mode != "custom" || chat.ProfileVersionID != "chat-v1" {
		t.Fatalf("chat=%+v err=%v", chat, err)
	}
	transcription, err := resolver.ResolveTranscription(context.Background(), "w1")
	if err != nil || !transcription.ReusedChat || transcription.Mode != "reuse_chat" || transcription.EndpointID != "chat" {
		t.Fatalf("transcription=%+v err=%v", transcription, err)
	}
	feature, err := resolver.ResolveFeature(context.Background(), "w1", "roadmap_generation")
	if err != nil || !feature.Enabled || feature.FallbackMode != "template" {
		t.Fatalf("feature=%+v err=%v", feature, err)
	}
}

func TestDisabledAndUnavailableAIFailClosedWithoutSecretLeak(t *testing.T) {
	source := keyedSource{&fakeSource{bindings: map[string]Binding{"llm_chat": {Kind: "llm_chat", Mode: "disabled"}}, profiles: map[string]EndpointProfile{}, features: map[string]FeatureSetting{}}}
	resolver, _ := NewResolver(source)
	if _, err := resolver.ResolveChat(context.Background(), "w1"); !errors.Is(err, ErrCapabilityDisabled) {
		t.Fatalf("disabled chat error=%v", err)
	}
	source.err = errors.New("postgres://user:super-secret@db/private")
	_, err := resolver.ResolveChat(context.Background(), "w1")
	if !errors.Is(err, ErrConfigurationUnavailable) || strings.Contains(err.Error(), "super-secret") || strings.Contains(err.Error(), "postgres://") {
		t.Fatalf("unsafe unavailable error=%v", err)
	}
}
