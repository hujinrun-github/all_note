package transcription

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hujinrun/flowspace/internal/airuntime"
	"github.com/hujinrun/flowspace/internal/auth"
)

type runtimeTranscriptionSource struct {
	endpoint string
}

func (s runtimeTranscriptionSource) LoadBinding(context.Context, string, string) (airuntime.Binding, error) {
	return airuntime.Binding{Kind: "llm_transcription", Mode: "custom", EndpointID: "speech"}, nil
}

func (s runtimeTranscriptionSource) LoadEndpointProfile(context.Context, string, string, string) (airuntime.EndpointProfile, error) {
	config, _ := json.Marshal(map[string]string{"endpoint": s.endpoint, "model": "whisper-1"})
	return airuntime.EndpointProfile{
		EndpointID: "speech", Kind: "llm_transcription", ProfileVersionID: "v1",
		Provider: "openai_compatible", ConfigJSON: string(config), Secret: []byte("workspace-key"),
	}, nil
}

func (runtimeTranscriptionSource) LoadFeatureSetting(context.Context, string, string) (airuntime.FeatureSetting, error) {
	return airuntime.FeatureSetting{}, nil
}

func TestRuntimeTranscriberResolvesWorkspaceProfile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/transcriptions" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer workspace-key" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"workspace transcript"}`))
	}))
	defer server.Close()

	resolver, err := airuntime.NewResolver(runtimeTranscriptionSource{endpoint: server.URL + "/v1"})
	if err != nil {
		t.Fatalf("resolver: %v", err)
	}
	transcriber, err := NewRuntimeTranscriber(resolver, server.Client())
	if err != nil {
		t.Fatalf("runtime transcriber: %v", err)
	}
	ctx := auth.ContextWithWorkspaceScope(context.Background(), "workspace-1")
	text, err := transcriber.Transcribe(ctx, Input{Audio: stringsReader("audio"), Filename: "note.m4a"})
	if err != nil {
		t.Fatalf("transcribe: %v", err)
	}
	if text != "workspace transcript" {
		t.Fatalf("text = %q", text)
	}
}
