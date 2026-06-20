package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNotionClientQueryDataSourceSendsHeadersAndPaginates(t *testing.T) {
	var authHeader string
	var notionVersion string
	var secondCursor string
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		authHeader = r.Header.Get("Authorization")
		notionVersion = r.Header.Get("Notion-Version")
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s", r.Method)
		}
		if r.URL.Path != "/v1/data_sources/ds-123/query" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		var body struct {
			StartCursor string `json:"start_cursor"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if requests == 2 {
			secondCursor = body.StartCursor
		}

		w.Header().Set("Content-Type", "application/json")
		if requests == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"results":     []map[string]any{{"id": "page-1", "url": "https://notion.so/page-1", "last_edited_time": "2026-06-13T01:00:00.000Z"}},
				"has_more":    true,
				"next_cursor": "cursor-2",
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results":  []map[string]any{{"id": "page-2", "url": "https://notion.so/page-2", "last_edited_time": "2026-06-13T02:00:00.000Z"}},
			"has_more": false,
		})
	}))
	defer server.Close()

	client := newNotionHTTPClient("secret-token", server.URL)
	pages, err := client.QueryDataSource("ds-123")
	if err != nil {
		t.Fatalf("query data source: %v", err)
	}
	if authHeader != "Bearer secret-token" {
		t.Fatalf("authorization header = %q", authHeader)
	}
	if notionVersion != "2025-09-03" {
		t.Fatalf("notion version = %q, want 2025-09-03", notionVersion)
	}
	if secondCursor != "cursor-2" {
		t.Fatalf("second request start_cursor = %q", secondCursor)
	}
	if len(pages) != 2 || pages[0].ID != "page-1" || pages[1].ID != "page-2" {
		t.Fatalf("pages = %#v", pages)
	}
}

func TestNotionClientRetriesRateLimitedRequests(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		if requests == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]any{"message": "rate limited"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{}, "has_more": false})
	}))
	defer server.Close()

	client := newNotionHTTPClient("secret-token", server.URL)
	client.retrySleep = func(time.Duration) {}

	if _, err := client.QueryDataSource("ds-123"); err != nil {
		t.Fatalf("query after retry: %v", err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}

func TestNotionClientReturnsUsefulHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{"message": "integration lacks access"})
	}))
	defer server.Close()

	client := newNotionHTTPClient("secret-token", server.URL)
	_, err := client.QueryDataSource("ds-123")
	if err == nil || !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "integration lacks access") {
		t.Fatalf("expected useful 403 error, got %v", err)
	}
}

func TestNotionClientDoesNotLeakAuthorizationTokenInErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{"message": "bad token secret-token"})
	}))
	defer server.Close()

	client := newNotionHTTPClient("secret-token", server.URL)
	_, err := client.QueryDataSource("ds-123")
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("error leaked token: %v", err)
	}
	if !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("error did not include redaction marker: %v", err)
	}
}
