package watchprojection

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/hujinrun/flowspace/internal/model"
)

func ProjectSnapshot(entities []model.MobileEntityEnvelope, nowUnix int64, timeZone string) ([]model.MobileEntityEnvelope, int64, error) {
	location, err := time.LoadLocation(timeZone)
	if err != nil {
		return nil, 0, err
	}
	now := time.Unix(nowUnix, 0).In(location)
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location)
	end := start.AddDate(0, 0, 7)
	validUntil := start.AddDate(0, 0, 1).Unix()

	parentTaskIDs := make(map[string]bool)
	includedVoiceIDs := make(map[string]bool)
	voiceCount := 0
	for _, entity := range entities {
		switch entity.EntityType {
		case "task_occurrence":
			var payload struct {
				TaskID         string `json:"task_id"`
				OccurrenceDate string `json:"occurrence_date"`
			}
			if err := json.Unmarshal(entity.Payload, &payload); err != nil {
				return nil, 0, err
			}
			if dateInWindow(payload.OccurrenceDate, start, end, location) {
				parentTaskIDs[payload.TaskID] = true
			}
		case "voice_note":
			if voiceCount < 20 {
				includedVoiceIDs[entity.ClientID] = true
				voiceCount++
			}
		}
	}

	projected := make([]model.MobileEntityEnvelope, 0, len(entities))
	for _, entity := range entities {
		include := false
		switch entity.EntityType {
		case "task_occurrence":
			var payload struct {
				OccurrenceDate string `json:"occurrence_date"`
			}
			if err := json.Unmarshal(entity.Payload, &payload); err != nil {
				return nil, 0, err
			}
			include = dateInWindow(payload.OccurrenceDate, start, end, location)
		case "task":
			var payload struct {
				ExecutionType string  `json:"execution_type"`
				PlannedDate   *string `json:"planned_date"`
				Due           *int64  `json:"due"`
			}
			if err := json.Unmarshal(entity.Payload, &payload); err != nil {
				return nil, 0, err
			}
			if payload.ExecutionType == "recurring" {
				include = parentTaskIDs[entity.ID]
			} else if payload.PlannedDate != nil && dateInWindow(*payload.PlannedDate, start, end, location) {
				include = true
			} else if payload.Due != nil {
				due := time.Unix(*payload.Due, 0).In(location)
				include = !due.Before(start) && due.Before(end)
			}
		case "event":
			var payload struct {
				StartTime int64 `json:"start_time"`
				EndTime   int64 `json:"end_time"`
			}
			if err := json.Unmarshal(entity.Payload, &payload); err != nil {
				return nil, 0, err
			}
			include = payload.EndTime > start.Unix() && payload.StartTime < end.Unix()
		case "voice_note":
			include = includedVoiceIDs[entity.ClientID]
		case "transcription_job":
			var payload struct {
				VoiceNoteID string `json:"voice_note_id"`
			}
			if err := json.Unmarshal(entity.Payload, &payload); err != nil {
				return nil, 0, err
			}
			include = includedVoiceIDs[payload.VoiceNoteID]
		default:
			return nil, 0, errors.New("watch projection received an unsupported entity type")
		}
		if include {
			projected = append(projected, entity)
		}
	}
	return projected, validUntil, nil
}

func dateInWindow(value string, start, end time.Time, location *time.Location) bool {
	date, err := time.ParseInLocation("2006-01-02", value, location)
	if err != nil {
		return false
	}
	return !date.Before(start) && date.Before(end)
}
