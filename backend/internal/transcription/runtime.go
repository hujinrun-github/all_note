package transcription

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/airuntime"
	"github.com/hujinrun/flowspace/internal/auth"
)

type RuntimeTranscriber struct {
	resolver *airuntime.Resolver
	client   *http.Client
	dial     DialContext
}

func NewRuntimeTranscriber(resolver *airuntime.Resolver, client *http.Client, dialers ...DialContext) (*RuntimeTranscriber, error) {
	if resolver == nil {
		return nil, errors.New("AI runtime resolver is required")
	}
	if len(dialers) > 1 {
		return nil, errors.New("only one transcription TCP dialer can be configured")
	}
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	var dial DialContext
	if len(dialers) == 1 {
		dial = dialers[0]
	}
	return &RuntimeTranscriber{resolver: resolver, client: client, dial: dial}, nil
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
	if resolved.Provider == "wyoming" {
		timeout := input.Timeout
		if timeout <= 0 {
			timeout = 2 * time.Minute
		}
		provider, err := NewWyomingTranscriber(WyomingConfig{
			Endpoint: config.Endpoint,
			Model:    config.Model,
			Timeout:  timeout,
		}, t.dial)
		if err != nil {
			return "", fmt.Errorf("configure Wyoming transcription service: %w", err)
		}
		return provider.Transcribe(ctx, input)
	}
	endpoint := strings.TrimRight(strings.TrimSpace(config.Endpoint), "/")
	if resolved.Provider == "openai_compatible" && !strings.HasSuffix(endpoint, "/audio/transcriptions") {
		endpoint += "/audio/transcriptions"
	}
	timeout := input.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	provider := NewProviderHTTPTranscriber(ProviderHTTPConfig{
		Provider: resolved.Provider,
		URL:      endpoint,
		APIKey:   string(resolved.Secret),
		Model:    config.Model,
		Timeout:  timeout,
	}, t.client)
	return provider.Transcribe(ctx, input)
}

var _ Transcriber = (*RuntimeTranscriber)(nil)
