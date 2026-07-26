package mobilev2contract

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestMTDV2Contract013GoDTOsDecodeCompleteEntityMatrix(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "mobile-v2", "entity-matrix.json"))
	if err != nil {
		t.Fatal(err)
	}
	envelopes, err := DecodeEntityMatrix(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(envelopes) != 9 {
		t.Fatalf("entity count = %d, want 9", len(envelopes))
	}
	want := map[string]bool{
		"project": false, "task": false, "task_schedule": false,
		"schedule_version": false, "task_occurrence": false,
		"learning_roadmap": false, "roadmap_node": false,
		"roadmap_edge": false, "roadmap_node_progress": false,
	}
	for _, envelope := range envelopes {
		if _, ok := want[envelope.EntityType]; !ok {
			t.Errorf("unexpected entity type %q", envelope.EntityType)
			continue
		}
		want[envelope.EntityType] = true
		if envelope.EntityID == "" || envelope.Payload == nil {
			t.Errorf("incomplete envelope %#v", envelope)
		}
	}
	for entityType, found := range want {
		if !found {
			t.Errorf("missing typed payload %s", entityType)
		}
	}
	project, ok := envelopes[0].Payload.(ProjectPayload)
	if !ok {
		t.Fatalf("project payload has type %T", envelopes[0].Payload)
	}
	if project.Name != "Project" || project.Description != "Description" || project.ArchivedFromStatus != nil {
		t.Fatalf("project payload lost fields: %#v", project)
	}

	invalid := bytes.Replace(data, []byte(`"lifecycle_status":"active"`), []byte(`"lifecycle_status":"unknown"`), 1)
	if _, err := DecodeEntityMatrix(invalid); err == nil {
		t.Fatal("unknown task lifecycle enum must be rejected")
	}
}
