package watchprojection

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/model"
)

func TestProjectionUsesSevenLocalDaysTwentyVoicesAndNextLocalMidnight(t *testing.T) {
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.March, 8, 0, 30, 0, 0, location)
	entities := []model.MobileEntityEnvelope{
		watchEntity("task_occurrence", "occ-in", map[string]any{"task_id": "task-parent", "occurrence_date": "2026-03-14"}),
		watchEntity("task_occurrence", "occ-out", map[string]any{"task_id": "task-parent", "occurrence_date": "2026-03-15"}),
		watchEntityWithID("task", "task-parent", "task-parent-client", map[string]any{"execution_type": "recurring"}),
		watchEntityWithID("task", "task-single-in", "task-single-in-client", map[string]any{"execution_type": "single", "planned_date": "2026-03-10"}),
		watchEntityWithID("task", "task-single-out", "task-single-out-client", map[string]any{"execution_type": "single", "planned_date": "2026-03-20"}),
		watchEntity("event", "event-in", map[string]any{"start_time": now.Add(24 * time.Hour).Unix(), "end_time": now.Add(25 * time.Hour).Unix()}),
		watchEntity("event", "event-out", map[string]any{"start_time": now.AddDate(0, 0, 8).Unix(), "end_time": now.AddDate(0, 0, 8).Add(time.Hour).Unix()}),
	}
	for index := range 22 {
		entities = append(entities, watchEntity("voice_note", "voice-"+string(rune('a'+index)), map[string]any{}))
	}
	entities = append(entities,
		watchEntity("transcription_job", "job-in", map[string]any{"voice_note_id": "voice-a"}),
		watchEntity("transcription_job", "job-out", map[string]any{"voice_note_id": "voice-v"}),
	)

	projected, validUntil, err := ProjectSnapshot(entities, now.Unix(), "America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	ids := make(map[string]bool, len(projected))
	voiceCount := 0
	for _, entity := range projected {
		ids[entity.ClientID] = true
		if entity.EntityType == "voice_note" {
			voiceCount++
		}
	}
	for _, id := range []string{"occ-in", "task-parent-client", "task-single-in-client", "event-in", "job-in"} {
		if !ids[id] {
			t.Fatalf("projection missing %s: %+v", id, ids)
		}
	}
	for _, id := range []string{"occ-out", "task-single-out-client", "event-out", "voice-v", "job-out"} {
		if ids[id] {
			t.Fatalf("projection unexpectedly contains %s: %+v", id, ids)
		}
	}
	if voiceCount != 20 {
		t.Fatalf("voice count=%d, want 20", voiceCount)
	}
	wantMidnight := time.Date(2026, time.March, 9, 0, 0, 0, 0, location)
	if validUntil != wantMidnight.Unix() {
		t.Fatalf("validUntil=%s want %s", time.Unix(validUntil, 0), wantMidnight)
	}
	if validUntil-now.Unix() != int64((22*time.Hour+30*time.Minute)/time.Second) {
		t.Fatalf("DST interval=%s, want 22h30m", time.Duration(validUntil-now.Unix())*time.Second)
	}
}

func watchEntity(entityType, clientID string, payload map[string]any) model.MobileEntityEnvelope {
	return watchEntityWithID(entityType, clientID, clientID, payload)
}

func watchEntityWithID(entityType, id, clientID string, payload map[string]any) model.MobileEntityEnvelope {
	encoded, _ := json.Marshal(payload)
	return model.MobileEntityEnvelope{EntityType: entityType, ID: id, ClientID: clientID, Revision: 1, Payload: encoded}
}
