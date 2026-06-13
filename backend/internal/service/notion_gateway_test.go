package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hujinrun/flowspace/internal/model"
)

func TestNotionRealGatewayQueryRemoteNotesLoadsBlocksAndProperties(t *testing.T) {
	var sawQuery bool
	var sawChildren bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/data_sources/ds-123/query":
			sawQuery = true
			_ = json.NewEncoder(w).Encode(map[string]any{
				"results": []map[string]any{{
					"id":               "page-1",
					"url":              "https://www.notion.so/page-1",
					"last_edited_time": "2026-06-13T01:00:00.000Z",
					"properties": map[string]any{
						"Name": map[string]any{
							"title": []map[string]any{{"plain_text": "Remote Title"}},
						},
						"FlowSpace ID": map[string]any{
							"rich_text": []map[string]any{{"plain_text": "note-123"}},
						},
					},
				}},
				"has_more": false,
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/blocks/page-1/children":
			sawChildren = true
			_ = json.NewEncoder(w).Encode(map[string]any{
				"results": []map[string]any{
					{
						"id":   "block-heading",
						"type": "heading_1",
						"heading_1": map[string]any{
							"rich_text": []map[string]any{{"plain_text": "Heading"}},
						},
					},
					{
						"id":   "block-paragraph",
						"type": "paragraph",
						"paragraph": map[string]any{
							"rich_text": []map[string]any{{"plain_text": "Remote paragraph"}},
						},
					},
				},
				"has_more": false,
			})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	gateway := newTestRealNotionGateway(server.URL)
	notes, err := gateway.QueryRemoteNotes(testNotionGatewayConfig())
	if err != nil {
		t.Fatalf("query remote notes: %v", err)
	}

	if !sawQuery || !sawChildren {
		t.Fatalf("expected query and children requests, saw query=%v children=%v", sawQuery, sawChildren)
	}
	if len(notes) != 1 {
		t.Fatalf("notes = %#v", notes)
	}
	got := notes[0]
	if got.PageID != "page-1" || got.URL != "https://www.notion.so/page-1" {
		t.Fatalf("remote metadata = %+v", got)
	}
	if got.Title != "Remote Title" || got.FlowSpaceID != "note-123" {
		t.Fatalf("remote properties = %+v", got)
	}
	wantMarkdown := "# Heading\n\nRemote paragraph\n"
	if got.Markdown != wantMarkdown || got.Hash != notionMarkdownHash(wantMarkdown) {
		t.Fatalf("markdown = %q hash = %q", got.Markdown, got.Hash)
	}
	if len(got.UnsupportedTypes) != 0 {
		t.Fatalf("unsupported types = %#v", got.UnsupportedTypes)
	}
}

func TestNotionRealGatewayCreateRemoteNoteSendsPagePayload(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/pages" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":               "page-created",
			"url":              "https://www.notion.so/page-created",
			"last_edited_time": "2026-06-13T02:00:00.000Z",
		})
	}))
	defer server.Close()

	note := &model.Note{ID: "note-create", Title: "Created Title", Body: "Created body\n"}
	remote, err := newTestRealNotionGateway(server.URL).CreateRemoteNote(testNotionGatewayConfig(), note)
	if err != nil {
		t.Fatalf("create remote note: %v", err)
	}

	if remote.PageID != "page-created" || remote.URL != "https://www.notion.so/page-created" {
		t.Fatalf("remote = %+v", remote)
	}
	assertStringAt(t, payload, "ds-123", "parent", "data_source_id")
	assertStringAt(t, payload, "Created Title", "properties", "Name", "title", "0", "text", "content")
	assertStringAt(t, payload, "note-create", "properties", "FlowSpace ID", "rich_text", "0", "text", "content")
	assertStringAt(t, payload, "paragraph", "children", "0", "type")
	assertStringAt(t, payload, "Created body", "children", "0", "paragraph", "rich_text", "0", "text", "content")
}

func TestNotionRealGatewayUpdateRemoteNoteReplacesChildren(t *testing.T) {
	requests := make([]string, 0)
	archived := make([]string, 0)
	var pagePatch map[string]any
	var appendPayload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPatch && r.URL.Path == "/v1/pages/page-1":
			if err := json.NewDecoder(r.Body).Decode(&pagePatch); err != nil {
				t.Fatalf("decode page patch: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":               "page-1",
				"url":              "https://www.notion.so/page-1",
				"last_edited_time": "2026-06-13T03:00:00.000Z",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/blocks/page-1/children":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"results":  []map[string]any{{"id": "block-old", "type": "paragraph"}},
				"has_more": false,
			})
		case r.Method == http.MethodPatch && r.URL.Path == "/v1/blocks/block-old":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode archive body: %v", err)
			}
			if body["archived"] != true {
				t.Fatalf("archive body = %#v", body)
			}
			archived = append(archived, "block-old")
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "block-old", "archived": true})
		case r.Method == http.MethodPatch && r.URL.Path == "/v1/blocks/page-1/children":
			if err := json.NewDecoder(r.Body).Decode(&appendPayload); err != nil {
				t.Fatalf("decode append body: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{}})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	note := &model.Note{ID: "note-1", Title: "Updated Title", Body: "# Updated\n"}
	remote, err := newTestRealNotionGateway(server.URL).UpdateRemoteNote(testNotionGatewayConfig(), "page-1", note)
	if err != nil {
		t.Fatalf("update remote note: %v", err)
	}

	if remote.PageID != "page-1" || remote.Markdown != "# Updated\n" {
		t.Fatalf("remote = %+v", remote)
	}
	if strings.Join(requests, ",") != "PATCH /v1/pages/page-1,GET /v1/blocks/page-1/children,PATCH /v1/blocks/block-old,PATCH /v1/blocks/page-1/children" {
		t.Fatalf("requests = %#v", requests)
	}
	if len(archived) != 1 || archived[0] != "block-old" {
		t.Fatalf("archived = %#v", archived)
	}
	assertStringAt(t, pagePatch, "Updated Title", "properties", "Name", "title", "0", "text", "content")
	assertStringAt(t, appendPayload, "heading_1", "children", "0", "type")
	assertStringAt(t, appendPayload, "Updated", "children", "0", "heading_1", "rich_text", "0", "text", "content")
}

func TestNotionRealGatewayRestoreRemoteNoteClearsTrashFlags(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/v1/pages/page-restore" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode restore body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":               "page-restore",
			"url":              "https://www.notion.so/page-restore",
			"last_edited_time": "2026-06-13T04:00:00.000Z",
		})
	}))
	defer server.Close()

	note := &model.Note{ID: "note-restore", Title: "Restore Me", Body: "Restored body\n"}
	remote, err := newTestRealNotionGateway(server.URL).RestoreRemoteNote(testNotionGatewayConfig(), note, notionSyncStateSnapshot{ExternalID: "page-restore"})
	if err != nil {
		t.Fatalf("restore remote note: %v", err)
	}

	if payload["archived"] != false || payload["in_trash"] != false {
		t.Fatalf("restore payload = %#v", payload)
	}
	if remote.PageID != "page-restore" || remote.Markdown != "Restored body\n" || remote.FlowSpaceID != "note-restore" {
		t.Fatalf("remote = %+v", remote)
	}
}

func newTestRealNotionGateway(baseURL string) *realNotionSyncGateway {
	return &realNotionSyncGateway{client: newNotionHTTPClient("secret-token", baseURL)}
}

func testNotionGatewayConfig() notionTargetConfig {
	return notionTargetConfig{
		DataSourceID:        "ds-123",
		TitleProperty:       "Name",
		FlowSpaceIDProperty: "FlowSpace ID",
	}
}

func assertStringAt(t *testing.T, value any, want string, path ...string) {
	t.Helper()
	current := value
	for _, key := range path {
		switch node := current.(type) {
		case map[string]any:
			current = node[key]
		case []any:
			if key != "0" {
				t.Fatalf("unsupported test index %q in path %#v", key, path)
			}
			if len(node) == 0 {
				t.Fatalf("empty array at path %#v", path)
			}
			current = node[0]
		default:
			t.Fatalf("path %#v reached %T (%#v)", path, current, current)
		}
	}
	if got, ok := current.(string); !ok || got != want {
		t.Fatalf("path %#v = %#v, want %q", path, current, want)
	}
}
