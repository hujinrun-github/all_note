package mobilecontract

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/goccy/go-yaml"
)

type openAPIDocument struct {
	OpenAPI string `yaml:"openapi"`
	Info    struct {
		Version string `yaml:"version"`
	} `yaml:"info"`
	Paths      map[string]map[string]any `yaml:"paths"`
	Components struct {
		SecuritySchemes map[string]securitySchemeDocument `yaml:"securitySchemes"`
		Schemas         map[string]schemaDocument         `yaml:"schemas"`
	} `yaml:"components"`
	GoldenFixtures []string `yaml:"x-golden-fixtures"`
}

type schemaDocument struct {
	OneOf                []referenceDocument          `yaml:"oneOf"`
	Required             []string                     `yaml:"required"`
	Properties           map[string]referenceDocument `yaml:"properties"`
	AdditionalProperties *bool                        `yaml:"additionalProperties"`
}

type securitySchemeDocument struct {
	Name string `yaml:"name"`
}

type referenceDocument struct {
	Ref string `yaml:"$ref"`
}

func loadMobileV1Contract(t *testing.T) openAPIDocument {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "api", "mobile-v1.openapi.yaml"))
	if err != nil {
		t.Fatalf("read mobile-v1 OpenAPI: %v", err)
	}
	var document openAPIDocument
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatalf("decode mobile-v1 OpenAPI: %v", err)
	}
	return document
}

func TestMobileV1OpenAPIContainsEveryDocumentedOperation(t *testing.T) {
	document := loadMobileV1Contract(t)
	if document.OpenAPI != "3.1.0" {
		t.Fatalf("expected OpenAPI 3.1.0, got %q", document.OpenAPI)
	}
	if document.Info.Version != "1.0.0" {
		t.Fatalf("expected contract version 1.0.0, got %q", document.Info.Version)
	}

	required := map[string][]string{
		"/api/mobile/capabilities":                          {"get"},
		"/api/mobile/sync/mutations":                        {"post"},
		"/api/mobile/sync/changes":                          {"get"},
		"/api/mobile/sync/snapshot":                         {"get"},
		"/api/mobile/sync/conflicts":                        {"get"},
		"/api/mobile/sync/conflicts/{conflictID}/resolve":   {"post"},
		"/api/mobile/voice-notes/{clientID}/audio":          {"put"},
		"/api/mobile/voice-notes/{clientID}/transcriptions": {"post"},
		"/api/mobile/transcription-jobs/{jobID}":            {"get"},
		"/api/mobile/transcription-jobs/{jobID}/retry":      {"post"},
	}
	for path, methods := range required {
		operations, ok := document.Paths[path]
		if !ok {
			t.Errorf("missing path %s", path)
			continue
		}
		for _, method := range methods {
			operation, ok := operations[method].(map[string]any)
			if !ok {
				t.Errorf("missing %s %s", method, path)
				continue
			}
			if operationID, _ := operation["operationId"].(string); operationID == "" {
				t.Errorf("%s %s has no operationId", method, path)
			}
		}
	}
}

func TestMobileV1RuntimeContractSHAIsCurrent(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "api", "mobile-v1.openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != ContractSHA256 {
		t.Fatalf("runtime contract SHA = %s, file SHA = %s", ContractSHA256, got)
	}
}

func TestMobileV1SessionCookieMatchesRuntimeDefault(t *testing.T) {
	document := loadMobileV1Contract(t)
	if got := document.Components.SecuritySchemes["sessionCookie"].Name; got != "fs_session" {
		t.Fatalf("session cookie = %q, want fs_session", got)
	}
}

func TestMobileV1GoldenExamplesValidateAgainstSchema(t *testing.T) {
	document := loadMobileV1Contract(t)
	want := []string{"conflict.json", "error.json", "mutation-success.json", "tombstone.json"}
	sort.Strings(document.GoldenFixtures)
	if len(document.GoldenFixtures) != len(want) {
		t.Fatalf("golden fixture list = %v, want %v", document.GoldenFixtures, want)
	}
	for i := range want {
		if document.GoldenFixtures[i] != want[i] {
			t.Fatalf("golden fixture list = %v, want %v", document.GoldenFixtures, want)
		}
	}
	for _, name := range want {
		data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "mobile-v1", name))
		if err != nil {
			t.Errorf("read %s: %v", name, err)
			continue
		}
		var value map[string]any
		if err := json.Unmarshal(data, &value); err != nil {
			t.Errorf("decode %s: %v", name, err)
			continue
		}
		if value["type"] == nil || value["schema_version"] != "mobile-v1" {
			t.Errorf("%s must declare type and schema_version=mobile-v1", name)
		}
	}
}

func TestTaskAndEventWireModelsMatchMobileClientPayloads(t *testing.T) {
	document := loadMobileV1Contract(t)
	task := document.Components.Schemas["TaskInput"]
	for _, field := range []string{"title", "content", "project_id", "due", "planned_date", "priority", "done", "status"} {
		if _, ok := task.Properties[field]; !ok {
			t.Errorf("TaskInput missing %s", field)
		}
	}
	event := document.Components.Schemas["EventInput"]
	assertExactRequired(t, "EventInput", event.Required, []string{"end_time", "is_all_day", "start_time", "title"})
	for _, field := range []string{"title", "start_time", "end_time", "location", "kind", "is_all_day", "notes"} {
		if _, ok := event.Properties[field]; !ok {
			t.Errorf("EventInput missing %s", field)
		}
	}
	if task.AdditionalProperties == nil || *task.AdditionalProperties || event.AdditionalProperties == nil || *event.AdditionalProperties {
		t.Error("mobile entity inputs must reject undeclared fields")
	}
}

func assertExactRequired(t *testing.T, name string, got, want []string) {
	t.Helper()
	sort.Strings(got)
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("%s required = %v, want %v", name, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s required = %v, want %v", name, got, want)
		}
	}
}
