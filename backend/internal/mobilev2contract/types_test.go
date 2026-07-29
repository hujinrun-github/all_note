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
	if len(envelopes) != 13 {
		t.Fatalf("entity count = %d, want 13", len(envelopes))
	}
	want := map[string]bool{
		"note": false, "voice_note": false, "inbox": false, "transcription_job": false,
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
	note, ok := envelopes[9].Payload.(NotePayload)
	if !ok {
		t.Fatalf("note payload has type %T", envelopes[9].Payload)
	}
	if note.Title != "Note" || len(note.Tags) != 2 {
		t.Fatalf("note payload lost fields: %#v", note)
	}
	voice, ok := envelopes[10].Payload.(VoiceNotePayload)
	if !ok {
		t.Fatalf("voice payload has type %T", envelopes[10].Payload)
	}
	if voice.AudioRevision != "2" || voice.AudioSize != "4096" {
		t.Fatalf("voice payload lost bigint fields: %#v", voice)
	}

	invalid := bytes.Replace(data, []byte(`"lifecycle_status":"active"`), []byte(`"lifecycle_status":"unknown"`), 1)
	if _, err := DecodeEntityMatrix(invalid); err == nil {
		t.Fatal("unknown task lifecycle enum must be rejected")
	}
}

func TestDecodeEntityMatrixAcceptsOnlyExplicitDeletionTombstonesWithNullPayload(t *testing.T) {
	tombstone := []byte(`[{
		"entity_type":"project","entity_id":"project-deleted","client_id":null,
		"entity_revision":"3",
		"aggregate_revisions":{"project_revision":"3","task_revision":null,"schedule_revision":null,"occurrence_revision":null},
		"deleted_at":"2026-07-29T12:00:00.000Z","payload":null
	}]`)
	entities, err := DecodeEntityMatrix(tombstone)
	if err != nil {
		t.Fatal(err)
	}
	if len(entities) != 1 || entities[0].Payload != nil || entities[0].DeletedAt == nil {
		t.Fatalf("decoded tombstone = %#v", entities)
	}
	invalid := bytes.Replace(tombstone,
		[]byte(`"deleted_at":"2026-07-29T12:00:00.000Z"`), []byte(`"deleted_at":null`), 1)
	if _, err := DecodeEntityMatrix(invalid); err == nil {
		t.Fatal("null payload without deleted_at must be rejected")
	}
}
