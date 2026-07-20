package tenantmigration

import (
	"context"
	"errors"
	"testing"
	"time"
)

type memorySnapshot struct {
	workspaceID, state, schema string
	capabilities               map[string]bool
	rows                       map[string][]LogicalRow
}

func (s *memorySnapshot) Workspace(context.Context) (string, string, error) {
	return s.workspaceID, s.state, nil
}
func (s *memorySnapshot) Schema(context.Context) (string, map[string]bool, error) {
	return s.schema, s.capabilities, nil
}
func (s *memorySnapshot) ReadTable(_ context.Context, table LogicalTable) ([]LogicalRow, error) {
	return s.rows[table.Name], nil
}
func (s *memorySnapshot) Close() error { return nil }

func TestExportProducesProviderNeutralManifest(t *testing.T) {
	left := baselineSnapshot(false)
	right := baselineSnapshot(true)
	a, err := Export(context.Background(), left)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Export(context.Background(), right)
	if err != nil {
		t.Fatal(err)
	}
	if a.Manifest.LogicalHash != b.Manifest.LogicalHash {
		t.Fatalf("provider-neutral hashes differ: %s != %s", a.Manifest.LogicalHash, b.Manifest.LogicalHash)
	}
	if len(a.Manifest.Tables) != len(BaselineLogicalTables()) {
		t.Fatalf("manifest tables=%d", len(a.Manifest.Tables))
	}
}

func TestExportRequiresFencedWorkspace(t *testing.T) {
	snapshot := baselineSnapshot(false)
	snapshot.state = "active"
	if _, err := Export(context.Background(), snapshot); !errors.Is(err, ErrSourceNotFenced) {
		t.Fatalf("Export() error=%v", err)
	}
}

func baselineSnapshot(alternateRepresentation bool) *memorySnapshot {
	revision := any(int64(7))
	pinned := any(false)
	content := any(`{"type":"doc","content":[]}`)
	createdAt := any("2026-01-01T00:00:00Z")
	if alternateRepresentation {
		revision = float64(7)
		pinned = int64(0)
		content = []byte("{\n \"content\": [], \"type\": \"doc\" }")
		createdAt = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	return &memorySnapshot{
		workspaceID: "w1", state: "fenced", schema: "0001_tenant_baseline.sql",
		capabilities: map[string]bool{"trigram_search": false},
		rows: map[string][]LogicalRow{
			"folders":       {{"id": "f1", "workspace_id": "w1", "name": "Inbox", "position": int64(0), "created_at": createdAt, "updated_at": "2026-01-01T00:00:00Z"}},
			"notes":         {{"id": "n1", "workspace_id": "w1", "folder_id": "f1", "title": "private body is never logged", "content": content, "content_text": "private", "content_format": "tiptap_json", "revision": revision, "pinned": pinned, "deleted_at": nil, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"}},
			"task_projects": {}, "tasks": {}, "tenant_job_outbox": {},
		},
	}
}
