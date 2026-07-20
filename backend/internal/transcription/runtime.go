package transcription

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/airuntime"
	"github.com/hujinrun/flowspace/internal/auth"
)

type RuntimeTranscriber struct {
	resolver *airuntime.Resolver
	client   *http.Client
}

func NewRuntimeTranscriber(resolver *airuntime.Resolver, client *http.Client) (*RuntimeTranscriber, error) {
	if resolver == nil {
		return nil, errors.New("AI runtime resolver is required")
	}
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	return &RuntimeTranscriber{resolver: resolver, client: client}, nil
}

func (t *RuntimeTranscriber) Transcribe(ctx context.Context, input Input) (string, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return "", ErrUnavailable
	}
	resolved, err := t.resolver.ResolveTranscription(ctx, workspaceID)
	if err != nil {
		return "", err
	}
	defer clear(resolved.Secret)
	var config struct {
		Endpoint string `json:"endpoint"`
		Model    string `json:"model"`
	}
	if json.Unmarshal([]byte(resolved.ConfigJSON), &config) != nil || strings.TrimSpace(config.Endpoint) == "" {
		return "", ErrUnavailable
	}
	endpoint := strings.TrimRight(strings.TrimSpace(config.Endpoint), "/")
	if resolved.Provider == "openai_compatible" && !strings.HasSuffix(endpoint, "/audio/transcriptions") {
		endpoint += "/audio/transcriptions"
	}
	provider := NewProviderHTTPTranscriber(ProviderHTTPConfig{
		Provider: resolved.Provider,
		URL:      endpoint,
		APIKey:   string(resolved.Secret),
		Model:    config.Model,
		Timeout:  2 * time.Minute,
	}, t.client)
	return provider.Transcribe(ctx, input)
}

var _ Transcriber = (*RuntimeTranscriber)(nil)
