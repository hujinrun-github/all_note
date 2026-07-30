package airuntime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeCodexRefresher struct{ calls int }

func (f *fakeCodexRefresher) RefreshCodexCredentials(_ context.Context, _ string, resolved ResolvedCapability) (ResolvedCapability, error) {
	f.calls++
	secret, _ := json.Marshal(map[string]string{"access_token": "refreshed", "account_id": "acct", "expires_at": time.Now().Add(time.Hour).Format(time.RFC3339Nano)})
	resolved.Secret = secret
	resolved.ProfileVersionID = "v2"
	resolved.EndpointID = "codex-v2"
	return resolved, nil
}

func TestGeneratorUsesCodexResponsesStreamAndAccountHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" || r.Header.Get("Authorization") != "Bearer access" || r.Header.Get("ChatGPT-Account-Id") != "acct" {
			t.Fatalf("request path=%s headers=%v", r.URL.Path, r.Header)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["stream"] != true || body["store"] != false || body["instructions"] != "system" {
			t.Fatalf("body=%v", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello \"}\n\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"world\"}\n\n"))
	}))
	defer server.Close()
	secret, _ := json.Marshal(map[string]string{"access_token": "access", "account_id": "acct"})
	source := keyedSource{&fakeSource{bindings: map[string]Binding{"llm_chat": {Kind: "llm_chat", Mode: "custom", EndpointID: "codex"}}, profiles: map[string]EndpointProfile{"codex": {EndpointID: "codex", Kind: "llm_chat", ProfileVersionID: "v1", Provider: "openai_codex_subscription", Model: "gpt-test", ConfigJSON: `{"endpoint":"` + server.URL + `","model":"gpt-test","api_mode":"codex_responses"}`, Secret: secret}}, features: map[string]FeatureSetting{}}}
	resolver, _ := NewResolver(source)
	generator, _ := NewGenerator(resolver, server.Client())
	got, err := generator.Generate(context.Background(), "w1", "system", "user")
	if err != nil || got != "hello world" {
		t.Fatalf("got=%q err=%v", got, err)
	}
}

func TestGeneratorUsesCompatibleStructuredContentAndRoadmapSizedBudget(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer api-key" {
			t.Fatalf("request path=%s headers=%v", r.URL.Path, r.Header)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body["max_tokens"] != float64(12000) {
			t.Fatalf("max_tokens=%v, want 12000", body["max_tokens"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"content":[{"type":"text","text":"{\"title\":\"AI Roadmap\"}"}]}}]}`))
	}))
	defer server.Close()
	source := keyedSource{&fakeSource{
		bindings: map[string]Binding{"llm_chat": {Kind: "llm_chat", Mode: "custom", EndpointID: "chat"}},
		profiles: map[string]EndpointProfile{"chat": {
			EndpointID:       "chat",
			Kind:             "llm_chat",
			ProfileVersionID: "v1",
			Provider:         "openai_compatible",
			Model:            "gpt-test",
			ConfigJSON:       `{"endpoint":"` + server.URL + `","model":"gpt-test"}`,
			Secret:           []byte("api-key"),
		}},
		features: map[string]FeatureSetting{},
	}}
	resolver, _ := NewResolver(source)
	generator, _ := NewGenerator(resolver, server.Client())

	got, err := generator.Generate(context.Background(), "w1", "system", "user")
	if err != nil || got != `{"title":"AI Roadmap"}` {
		t.Fatalf("got=%q err=%v", got, err)
	}
}

func TestGeneratorRetriesCompatibleEmptyJSONContent(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"content":""}}]}`))
			return
		}
		if len(body.Messages) != 2 || !strings.Contains(body.Messages[1].Content, "Return the JSON object immediately") {
			t.Fatalf("retry messages=%+v", body.Messages)
		}
		_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"content":"{\"title\":\"AI Roadmap\"}"}}]}`))
	}))
	defer server.Close()
	source := keyedSource{&fakeSource{
		bindings: map[string]Binding{"llm_chat": {Kind: "llm_chat", Mode: "custom", EndpointID: "chat"}},
		profiles: map[string]EndpointProfile{"chat": {
			EndpointID:       "chat",
			Kind:             "llm_chat",
			ProfileVersionID: "v1",
			Provider:         "openai_compatible",
			Model:            "gpt-test",
			ConfigJSON:       `{"endpoint":"` + server.URL + `","model":"gpt-test"}`,
			Secret:           []byte("api-key"),
		}},
		features: map[string]FeatureSetting{},
	}}
	resolver, _ := NewResolver(source)
	generator, _ := NewGenerator(resolver, server.Client())

	got, err := generator.Generate(context.Background(), "w1", "system", "user")
	if err != nil || got != `{"title":"AI Roadmap"}` || calls != 2 {
		t.Fatalf("got=%q calls=%d err=%v", got, calls, err)
	}
}

func TestGeneratorUsesCompatibleJSONObjectContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"content":{"title":"AI Roadmap"}}}]}`))
	}))
	defer server.Close()
	source := keyedSource{&fakeSource{
		bindings: map[string]Binding{"llm_chat": {Kind: "llm_chat", Mode: "custom", EndpointID: "chat"}},
		profiles: map[string]EndpointProfile{"chat": {
			EndpointID:       "chat",
			Kind:             "llm_chat",
			ProfileVersionID: "v1",
			Provider:         "openai_compatible",
			Model:            "gpt-test",
			ConfigJSON:       `{"endpoint":"` + server.URL + `","model":"gpt-test"}`,
			Secret:           []byte("api-key"),
		}},
		features: map[string]FeatureSetting{},
	}}
	resolver, _ := NewResolver(source)
	generator, _ := NewGenerator(resolver, server.Client())

	got, err := generator.Generate(context.Background(), "w1", "system", "user")
	if err != nil || got != `{"title":"AI Roadmap"}` {
		t.Fatalf("got=%q err=%v", got, err)
	}
}

func TestGeneratorExplainsCompatibleTokenExhaustion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"length","message":{"content":null,"reasoning_content":"unfinished reasoning"}}]}`))
	}))
	defer server.Close()
	source := keyedSource{&fakeSource{
		bindings: map[string]Binding{"llm_chat": {Kind: "llm_chat", Mode: "custom", EndpointID: "chat"}},
		profiles: map[string]EndpointProfile{"chat": {
			EndpointID:       "chat",
			Kind:             "llm_chat",
			ProfileVersionID: "v1",
			Provider:         "openai_compatible",
			Model:            "gpt-test",
			ConfigJSON:       `{"endpoint":"` + server.URL + `","model":"gpt-test"}`,
			Secret:           []byte("api-key"),
		}},
		features: map[string]FeatureSetting{},
	}}
	resolver, _ := NewResolver(source)
	generator, _ := NewGenerator(resolver, server.Client())

	_, err := generator.Generate(context.Background(), "w1", "system", "user")
	if err == nil || !strings.Contains(err.Error(), "token budget") {
		t.Fatalf("err=%v", err)
	}
}

func TestConsumeResponsesSSEReturnsProviderError(t *testing.T) {
	_, err := consumeResponsesSSE(strings.NewReader("data: {\"type\":\"error\",\"error\":{\"message\":\"quota exhausted\"}}\n\n"))
	if err == nil || !strings.Contains(err.Error(), "quota exhausted") {
		t.Fatalf("err=%v", err)
	}
}

func TestGeneratorRefreshesExpiredCodexCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer refreshed" {
			t.Fatalf("Authorization=%q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n"))
	}))
	defer server.Close()
	secret, _ := json.Marshal(map[string]string{"access_token": "expired", "refresh_token": "refresh", "expires_at": time.Now().Add(-time.Minute).Format(time.RFC3339Nano)})
	source := keyedSource{&fakeSource{bindings: map[string]Binding{"llm_chat": {Kind: "llm_chat", Mode: "custom", EndpointID: "codex"}}, profiles: map[string]EndpointProfile{"codex": {EndpointID: "codex", Kind: "llm_chat", ProfileVersionID: "v1", Provider: "openai_codex_subscription", Model: "gpt-test", ConfigJSON: `{"endpoint":"` + server.URL + `","model":"gpt-test","api_mode":"codex_responses"}`, Secret: secret}}, features: map[string]FeatureSetting{}}}
	resolver, _ := NewResolver(source)
	generator, _ := NewGenerator(resolver, server.Client())
	refresher := &fakeCodexRefresher{}
	generator.SetCodexCredentialRefresher(refresher)
	got, err := generator.Generate(context.Background(), "w1", "system", "user")
	if err != nil || got != "ok" || refresher.calls != 1 {
		t.Fatalf("got=%q calls=%d err=%v", got, refresher.calls, err)
	}
}

func TestNewGeneratorExtendsAIRequestTimeoutWithoutMutatingSharedClient(t *testing.T) {
	sharedTransport := &http.Transport{ResponseHeaderTimeout: 20 * time.Second}
	sharedClient := &http.Client{
		Transport: sharedTransport,
		Timeout:   30 * time.Second,
	}
	resolver, _ := NewResolver(keyedSource{&fakeSource{}})

	generator, err := NewGenerator(resolver, sharedClient)
	if err != nil {
		t.Fatalf("new generator: %v", err)
	}
	if generator.http.Timeout != 2*time.Minute {
		t.Fatalf("generator timeout=%s, want 2m", generator.http.Timeout)
	}
	generatorTransport, ok := generator.http.Transport.(*http.Transport)
	if !ok || generatorTransport.ResponseHeaderTimeout != 2*time.Minute {
		t.Fatalf("generator response header timeout=%v", generator.http.Transport)
	}
	if sharedClient.Timeout != 30*time.Second || sharedTransport.ResponseHeaderTimeout != 20*time.Second {
		t.Fatal("new generator mutated the shared outbound client")
	}
}
