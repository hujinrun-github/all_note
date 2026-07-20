package airuntime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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

func TestConsumeResponsesSSEReturnsProviderError(t *testing.T) {
	_, err := consumeResponsesSSE(strings.NewReader("data: {\"type\":\"error\",\"error\":{\"message\":\"quota exhausted\"}}\n\n"))
	if err == nil || !strings.Contains(err.Error(), "quota exhausted") {
		t.Fatalf("err=%v", err)
	}
}
