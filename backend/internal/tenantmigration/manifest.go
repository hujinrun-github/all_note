package tenantmigration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

type LogicalRow map[string]any

type LogicalTable struct {
	Name             string
	Columns          []string
	PrimaryKey       []string
	JSONColumns      map[string]bool
	BooleanColumns   map[string]bool
	TimestampColumns map[string]bool
	RevisionColumn   string
}

type TableManifest struct {
	Name           string `json:"name"`
	Rows           int64  `json:"rows"`
	PrimaryKeyHash string `json:"primary_key_hash"`
	CriticalHash   string `json:"critical_hash"`
	MaxRevision    int64  `json:"max_revision"`
}

type TransferManifest struct {
	WorkspaceID  string          `json:"workspace_id"`
	Schema       string          `json:"schema"`
	Capabilities map[string]bool `json:"capabilities"`
	Tables       []TableManifest `json:"tables"`
	LogicalHash  string          `json:"logical_hash"`
}

type TableData struct {
	Table LogicalTable
	Rows  []LogicalRow
}

type TransferPackage struct {
	Manifest TransferManifest
	Tables   []TableData
}

func BaselineLogicalTables() []LogicalTable {
	return []LogicalTable{
		{Name: "folders", Columns: []string{"id", "workspace_id", "name", "position", "created_at", "updated_at"}, PrimaryKey: []string{"id"}, TimestampColumns: timestamps("created_at", "updated_at")},
		{Name: "notes", Columns: []string{"id", "workspace_id", "folder_id", "title", "content", "content_text", "content_format", "revision", "pinned", "deleted_at", "created_at", "updated_at"}, PrimaryKey: []string{"id"}, JSONColumns: map[string]bool{"content": true}, BooleanColumns: map[string]bool{"pinned": true}, TimestampColumns: timestamps("deleted_at", "created_at", "updated_at"), RevisionColumn: "revision"},
		{Name: "task_projects", Columns: []string{"id", "workspace_id", "name", "color", "created_at", "updated_at"}, PrimaryKey: []string{"id"}, TimestampColumns: timestamps("created_at", "updated_at")},
		{Name: "tasks", Columns: []string{"id", "workspace_id", "project_id", "note_id", "title", "description", "status", "priority", "due_at", "completed_at", "deleted_at", "created_at", "updated_at"}, PrimaryKey: []string{"id"}, TimestampColumns: timestamps("due_at", "completed_at", "deleted_at", "created_at", "updated_at")},
		{Name: "tenant_job_outbox", Columns: []string{"id", "workspace_id", "topic", "aggregate_id", "aggregate_revision", "payload_json", "created_at", "published_at"}, PrimaryKey: []string{"id"}, JSONColumns: map[string]bool{"payload_json": true}, TimestampColumns: timestamps("created_at", "published_at"), RevisionColumn: "aggregate_revision"},
	}
}

func timestamps(columns ...string) map[string]bool {
	result := make(map[string]bool, len(columns))
	for _, column := range columns {
		result[column] = true
	}
	return result
}

func manifestHash(manifest TransferManifest) string {
	copy := manifest
	copy.LogicalHash = ""
	copy.Capabilities = nil
	encoded, _ := json.Marshal(copy)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
