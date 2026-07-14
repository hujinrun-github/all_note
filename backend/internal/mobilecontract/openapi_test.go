package mobilecontract

import (
	"encoding/json"
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
		Schemas map[string]schemaDocument `yaml:"schemas"`
	} `yaml:"components"`
	GoldenFixtures []string `yaml:"x-golden-fixtures"`
}

type schemaDocument struct {
	OneOf                []referenceDocument          `yaml:"oneOf"`
	Required             []string                     `yaml:"required"`
	Properties           map[string]referenceDocument `yaml:"properties"`
	AdditionalProperties *bool                        `yaml:"additionalProperties"`
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
		"/api/mobile/sync/mutations":                        {"post"},
		"/api/mobile/sync/changes":                          {"get"},
		"/api/mobile/sync/snapshot":                         {"get"},
		"/api/mobile/sync/conflicts":                        {"get"},
		"/api/mobile/sync/conflicts/{conflictID}/resolve":   {"post"},
		"/api/mobile/voice-notes":                           {"post"},
		"/api/mobile/voice-notes/{clientID}/audio":          {"put", "delete"},
		"/api/mobile/voice-notes/{clientID}":                {"delete"},
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

func TestAllDayAndTimedEventWireModelsAreMutuallyExclusive(t *testing.T) {
	document := loadMobileV1Contract(t)
	event := document.Components.Schemas["EventInput"]
	gotRefs := make([]string, 0, len(event.OneOf))
	for _, candidate := range event.OneOf {
		gotRefs = append(gotRefs, candidate.Ref)
	}
	sort.Strings(gotRefs)
	wantRefs := []string{"#/components/schemas/AllDayEventInput", "#/components/schemas/TimedEventInput"}
	if len(gotRefs) != len(wantRefs) || gotRefs[0] != wantRefs[0] || gotRefs[1] != wantRefs[1] {
		t.Fatalf("EventInput oneOf = %v, want %v", gotRefs, wantRefs)
	}

	allDay := document.Components.Schemas["AllDayEventInput"]
	timed := document.Components.Schemas["TimedEventInput"]
	assertExactRequired(t, "AllDayEventInput", allDay.Required, []string{"end_date_exclusive", "start_date", "title"})
	assertExactRequired(t, "TimedEventInput", timed.Required, []string{"end_at", "start_at", "time_zone", "title"})
	if _, ok := allDay.Properties["start_at"]; ok {
		t.Error("AllDayEventInput must not expose start_at")
	}
	if _, ok := timed.Properties["start_date"]; ok {
		t.Error("TimedEventInput must not expose start_date")
	}
	if allDay.AdditionalProperties == nil || *allDay.AdditionalProperties || timed.AdditionalProperties == nil || *timed.AdditionalProperties {
		t.Error("event variants must reject undeclared fields")
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
