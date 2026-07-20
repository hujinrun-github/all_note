package airuntime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Generator struct {
	resolver *Resolver
	http     *http.Client
}

func NewGenerator(resolver *Resolver, httpClient *http.Client) (*Generator, error) {
	if resolver == nil {
		return nil, errors.New("AI runtime resolver is required")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 90 * time.Second}
	}
	return &Generator{resolver: resolver, http: httpClient}, nil
}

func (g *Generator) Generate(ctx context.Context, workspaceID, systemPrompt, userPrompt string) (string, error) {
	resolved, err := g.resolver.ResolveChat(ctx, workspaceID)
	if err != nil {
		return "", err
	}
	defer clear(resolved.Secret)
	var config struct {
		Endpoint string `json:"endpoint"`
		Model    string `json:"model"`
		APIMode  string `json:"api_mode"`
	}
	if json.Unmarshal([]byte(resolved.ConfigJSON), &config) != nil || strings.TrimSpace(config.Endpoint) == "" {
		return "", ErrConfigurationUnavailable
	}
	if strings.TrimSpace(config.Model) == "" && resolved.Provider != "openai_codex_subscription" {
		config.Model = "deepseek-v4-pro"
	}
	if strings.TrimSpace(config.Model) == "" {
		return "", ErrConfigurationUnavailable
	}
	if resolved.Provider == "openai_codex_subscription" || config.APIMode == "codex_responses" {
		return g.generateCodex(ctx, config.Endpoint, config.Model, resolved.Secret, systemPrompt, userPrompt)
	}
	return g.generateCompatible(ctx, config.Endpoint, config.Model, resolved.Secret, systemPrompt, userPrompt)
}

func (g *Generator) generateCodex(ctx context.Context, endpoint, model string, secret []byte, systemPrompt, userPrompt string) (string, error) {
	var credentials struct {
		AccessToken string `json:"access_token"`
		AccountID   string `json:"account_id"`
	}
	if json.Unmarshal(secret, &credentials) != nil || strings.TrimSpace(credentials.AccessToken) == "" {
		return "", ErrConfigurationUnavailable
	}
	body := map[string]any{
		"model": model, "instructions": systemPrompt, "store": false, "stream": true,
		"input": []map[string]any{{"role": "user", "content": []map[string]string{{"type": "input_text", "text": userPrompt}}}},
	}
	payload, _ := json.Marshal(body)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(endpoint, "/")+"/responses", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("Authorization", "Bearer "+credentials.AccessToken)
	request.Header.Set("User-Agent", "FlowSpace/0.2")
	request.Header.Set("originator", "flowspace")
	if credentials.AccountID != "" {
		request.Header.Set("ChatGPT-Account-Id", credentials.AccountID)
	}
	response, err := g.http.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode >= http.StatusBadRequest {
		return "", limitedHTTPError("Codex", response)
	}
	return consumeResponsesSSE(response.Body)
}

func (g *Generator) generateCompatible(ctx context.Context, endpoint, model string, secret []byte, systemPrompt, userPrompt string) (string, error) {
	body := map[string]any{"model": model, "messages": []map[string]string{{"role": "system", "content": systemPrompt}, {"role": "user", "content": userPrompt}}, "response_format": map[string]string{"type": "json_object"}}
	payload, _ := json.Marshal(body)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(endpoint, "/")+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+string(secret))
	response, err := g.http.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode >= http.StatusBadRequest {
		return "", limitedHTTPError("AI", response)
	}
	var decoded struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.NewDecoder(response.Body).Decode(&decoded) != nil || len(decoded.Choices) == 0 || strings.TrimSpace(decoded.Choices[0].Message.Content) == "" {
		return "", errors.New("AI response did not include content")
	}
	return decoded.Choices[0].Message.Content, nil
}

func consumeResponsesSSE(reader io.Reader) (string, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64<<10), 2<<20)
	var output strings.Builder
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var event struct {
			Type, Delta string
			Error       struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(data), &event) != nil {
			continue
		}
		if event.Type == "error" || event.Type == "response.failed" {
			if event.Error.Message == "" {
				event.Error.Message = "Codex response failed"
			}
			return "", errors.New(event.Error.Message)
		}
		if event.Type == "response.output_text.delta" {
			output.WriteString(event.Delta)
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	if strings.TrimSpace(output.String()) == "" {
		return "", errors.New("Codex response did not include output text")
	}
	return output.String(), nil
}

func limitedHTTPError(provider string, response *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
	return fmt.Errorf("%s request failed: HTTP %d %s", provider, response.StatusCode, strings.TrimSpace(string(body)))
}
