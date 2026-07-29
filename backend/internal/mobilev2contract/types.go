package mobilev2contract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

type ProjectKind string
type ProjectHorizon string
type ProjectStatus string
type ProjectSystemRole string
type TaskLifecycleStatus string
type GenerationStatus string
type RecurrenceType string
type TimingType string
type ExecutionStatus string
type RoadmapStatus string
type RoadmapNodeType string
type RoadmapNodeStatus string
type RoadmapEdgeType string

type NotePayload struct {
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	FolderID  string   `json:"folder_id"`
	Tags      []string `json:"tags"`
	CreatedAt string   `json:"created_at"`
	UpdatedAt string   `json:"updated_at"`
}

type VoiceNotePayload struct {
	NoteID             string `json:"note_id"`
	Title              string `json:"title"`
	Body               string `json:"body"`
	DurationMS         string `json:"duration_ms"`
	RecordedAt         string `json:"recorded_at"`
	Language           string `json:"language"`
	UploadState        string `json:"upload_state"`
	AudioState         string `json:"audio_state"`
	AudioRevision      string `json:"audio_revision"`
	TranscriptionState string `json:"transcription_state"`
	TranscriptionError string `json:"transcription_error"`
	MimeType           string `json:"mime_type"`
	AudioSize          string `json:"audio_size"`
	AudioSHA256        string `json:"audio_sha256"`
	CreatedAt          string `json:"created_at"`
	UpdatedAt          string `json:"updated_at"`
}

type InboxPayload struct {
	Kind      string  `json:"kind"`
	Title     string  `json:"title"`
	Body      *string `json:"body"`
	Archived  bool    `json:"archived"`
	CreatedAt string  `json:"created_at"`
	UpdatedAt string  `json:"updated_at"`
}

type TranscriptionJobPayload struct {
	VoiceNoteID   string  `json:"voice_note_id"`
	Generation    string  `json:"generation"`
	State         string  `json:"state"`
	ErrorCode     string  `json:"error_code"`
	NextAttemptAt *string `json:"next_attempt_at"`
	CreatedAt     string  `json:"created_at"`
	UpdatedAt     string  `json:"updated_at"`
}

type AggregateRevisions struct {
	ProjectRevision    *string `json:"project_revision"`
	TaskRevision       *string `json:"task_revision"`
	ScheduleRevision   *string `json:"schedule_revision"`
	OccurrenceRevision *string `json:"occurrence_revision"`
}

type EntityEnvelope struct {
	EntityType         string
	EntityID           string
	ClientID           *string
	EntityRevision     string
	AggregateRevisions AggregateRevisions
	DeletedAt          *string
	Payload            any
}

type entityEnvelopeWire struct {
	EntityType         string             `json:"entity_type"`
	EntityID           string             `json:"entity_id"`
	ClientID           *string            `json:"client_id"`
	EntityRevision     string             `json:"entity_revision"`
	AggregateRevisions AggregateRevisions `json:"aggregate_revisions"`
	DeletedAt          *string            `json:"deleted_at"`
	Payload            json.RawMessage    `json:"payload"`
}

type ProjectPayload struct {
	Name               string             `json:"name"`
	Description        string             `json:"description"`
	Kind               ProjectKind        `json:"kind"`
	Horizon            ProjectHorizon     `json:"horizon"`
	Status             ProjectStatus      `json:"status"`
	ArchivedFromStatus *ProjectStatus     `json:"archived_from_status"`
	SystemRole         *ProjectSystemRole `json:"system_role"`
	TargetAt           *string            `json:"target_at"`
	CreatedAt          string             `json:"created_at"`
	UpdatedAt          string             `json:"updated_at"`
	ArchivedAt         *string            `json:"archived_at"`
}

type TaskPayload struct {
	ProjectID       string              `json:"project_id"`
	RoadmapNodeID   *string             `json:"roadmap_node_id"`
	NoteID          *string             `json:"note_id"`
	Title           string              `json:"title"`
	Description     string              `json:"description"`
	LifecycleStatus TaskLifecycleStatus `json:"lifecycle_status"`
	Priority        int                 `json:"priority"`
	SortOrder       float64             `json:"sort_order"`
	CreatedAt       string              `json:"created_at"`
	UpdatedAt       string              `json:"updated_at"`
	ArchivedAt      *string             `json:"archived_at"`
}

type TaskSchedulePayload struct {
	TaskID                  string           `json:"task_id"`
	CurrentScheduleRevision string           `json:"current_schedule_revision"`
	GenerationWatermark     *string          `json:"generation_watermark"`
	GenerationStatus        GenerationStatus `json:"generation_status"`
	GenerationError         *string          `json:"generation_error"`
	GenerationRetryAt       *string          `json:"generation_retry_at"`
	UpdatedAt               string           `json:"updated_at"`
}

type ScheduleVersionPayload struct {
	TaskID           string         `json:"task_id"`
	ScheduleRevision string         `json:"schedule_revision"`
	EffectiveFrom    *string        `json:"effective_from"`
	EffectiveTo      *string        `json:"effective_to"`
	RecurrenceType   RecurrenceType `json:"recurrence_type"`
	TimingType       TimingType     `json:"timing_type"`
	Timezone         string         `json:"timezone"`
	StartsOn         *string        `json:"starts_on"`
	EndsOn           *string        `json:"ends_on"`
	Rule             map[string]any `json:"rule"`
	LocalStartTime   *string        `json:"local_start_time"`
	DurationMinutes  *int           `json:"duration_minutes"`
	CreatedAt        string         `json:"created_at"`
}

type TaskOccurrencePayload struct {
	TaskID                    string          `json:"task_id"`
	OccurrenceKey             string          `json:"occurrence_key"`
	PlannedDate               *string         `json:"planned_date"`
	PlannedStartAt            *string         `json:"planned_start_at"`
	PlannedEndAt              *string         `json:"planned_end_at"`
	DueAt                     *string         `json:"due_at"`
	ExecutionStatus           ExecutionStatus `json:"execution_status"`
	ActualStartAt             *string         `json:"actual_start_at"`
	CompletedAt               *string         `json:"completed_at"`
	OverrideTitle             *string         `json:"override_title"`
	OverrideDescription       *string         `json:"override_description"`
	BlockedReason             *string         `json:"blocked_reason"`
	NextAction                *string         `json:"next_action"`
	Location                  *string         `json:"location"`
	CalendarKind              *string         `json:"calendar_kind"`
	CalendarNotes             *string         `json:"calendar_notes"`
	NoteID                    *string         `json:"note_id"`
	AllDayEndDate             *string         `json:"all_day_end_date"`
	GeneratedScheduleRevision string          `json:"generated_schedule_revision"`
	CreatedAt                 string          `json:"created_at"`
	UpdatedAt                 string          `json:"updated_at"`
}

type LearningRoadmapPayload struct {
	ProjectID   string        `json:"project_id"`
	Title       string        `json:"title"`
	Description string        `json:"description"`
	Status      RoadmapStatus `json:"status"`
	CreatedAt   string        `json:"created_at"`
	UpdatedAt   string        `json:"updated_at"`
}

type RoadmapNodePayload struct {
	ProjectID      string            `json:"project_id"`
	RoadmapID      string            `json:"roadmap_id"`
	ParentID       *string           `json:"parent_id"`
	Title          string            `json:"title"`
	Description    string            `json:"description"`
	NodeType       RoadmapNodeType   `json:"node_type"`
	Status         RoadmapNodeStatus `json:"status"`
	Position       float64           `json:"position"`
	LegacyMetadata map[string]any    `json:"legacy_metadata"`
	CreatedAt      string            `json:"created_at"`
	UpdatedAt      string            `json:"updated_at"`
}

type RoadmapEdgePayload struct {
	ProjectID  string          `json:"project_id"`
	RoadmapID  string          `json:"roadmap_id"`
	FromNodeID string          `json:"from_node_id"`
	ToNodeID   string          `json:"to_node_id"`
	EdgeType   RoadmapEdgeType `json:"edge_type"`
	CreatedAt  string          `json:"created_at"`
}

type RoadmapNodeProgressPayload struct {
	RoadmapNodeID string `json:"roadmap_node_id"`
	AsOfSequence  string `json:"as_of_sequence"`
	Total         int    `json:"total"`
	Open          int    `json:"open"`
	Active        int    `json:"active"`
	Blocked       int    `json:"blocked"`
	Done          int    `json:"done"`
	Skipped       int    `json:"skipped"`
	Cancelled     int    `json:"cancelled"`
}

func DecodeEntityMatrix(data []byte) ([]EntityEnvelope, error) {
	var wire []entityEnvelopeWire
	if err := decodeStrict(data, &wire); err != nil {
		return nil, err
	}
	result := make([]EntityEnvelope, 0, len(wire))
	for index, item := range wire {
		if item.EntityID == "" || item.EntityRevision == "" {
			return nil, fmt.Errorf("entity %d is missing identity or revision", index)
		}
		if !supportedEntityType(item.EntityType) {
			return nil, fmt.Errorf("entity %d has unsupported entity_type %q", index, item.EntityType)
		}
		if bytes.Equal(bytes.TrimSpace(item.Payload), []byte("null")) {
			if item.DeletedAt == nil {
				return nil, fmt.Errorf("entity %d has null payload without deleted_at", index)
			}
			result = append(result, EntityEnvelope{
				EntityType: item.EntityType, EntityID: item.EntityID, ClientID: item.ClientID,
				EntityRevision: item.EntityRevision, AggregateRevisions: item.AggregateRevisions,
				DeletedAt: item.DeletedAt,
			})
			continue
		}
		var payload any
		switch item.EntityType {
		case "note":
			payload = &NotePayload{}
		case "voice_note":
			payload = &VoiceNotePayload{}
		case "inbox":
			payload = &InboxPayload{}
		case "transcription_job":
			payload = &TranscriptionJobPayload{}
		case "project":
			payload = &ProjectPayload{}
		case "task":
			payload = &TaskPayload{}
		case "task_schedule":
			payload = &TaskSchedulePayload{}
		case "schedule_version":
			payload = &ScheduleVersionPayload{}
		case "task_occurrence":
			payload = &TaskOccurrencePayload{}
		case "learning_roadmap":
			payload = &LearningRoadmapPayload{}
		case "roadmap_node":
			payload = &RoadmapNodePayload{}
		case "roadmap_edge":
			payload = &RoadmapEdgePayload{}
		case "roadmap_node_progress":
			payload = &RoadmapNodeProgressPayload{}
		default:
			return nil, fmt.Errorf("entity %d has unsupported entity_type %q", index, item.EntityType)
		}
		if err := decodeStrict(item.Payload, payload); err != nil {
			return nil, fmt.Errorf("decode %s payload: %w", item.EntityType, err)
		}
		payload = dereferencePayload(payload)
		if err := validatePayloadEnums(payload); err != nil {
			return nil, fmt.Errorf("decode %s payload: %w", item.EntityType, err)
		}
		result = append(result, EntityEnvelope{
			EntityType: item.EntityType, EntityID: item.EntityID, ClientID: item.ClientID,
			EntityRevision: item.EntityRevision, AggregateRevisions: item.AggregateRevisions,
			DeletedAt: item.DeletedAt, Payload: payload,
		})
	}
	return result, nil
}

func supportedEntityType(entityType string) bool {
	switch entityType {
	case "note", "voice_note", "inbox", "transcription_job", "project", "task",
		"task_schedule", "schedule_version", "task_occurrence", "learning_roadmap",
		"roadmap_node", "roadmap_edge", "roadmap_node_progress":
		return true
	default:
		return false
	}
}

func validatePayloadEnums(payload any) error {
	switch value := payload.(type) {
	case ProjectPayload:
		if !enumAllowed(string(value.Kind), "standard", "learning") {
			return invalidEnum("kind", string(value.Kind))
		}
		if !enumAllowed(string(value.Horizon), "short", "long") {
			return invalidEnum("horizon", string(value.Horizon))
		}
		if !enumAllowed(string(value.Status), "planning", "active", "paused", "completed", "archived") {
			return invalidEnum("status", string(value.Status))
		}
		if value.ArchivedFromStatus != nil && !enumAllowed(string(*value.ArchivedFromStatus), "planning", "active", "paused", "completed") {
			return invalidEnum("archived_from_status", string(*value.ArchivedFromStatus))
		}
		if value.SystemRole != nil && !enumAllowed(string(*value.SystemRole), "inbox", "personal") {
			return invalidEnum("system_role", string(*value.SystemRole))
		}
	case TaskPayload:
		if !enumAllowed(string(value.LifecycleStatus), "draft", "active", "paused", "completed", "cancelled", "archived") {
			return invalidEnum("lifecycle_status", string(value.LifecycleStatus))
		}
	case TaskSchedulePayload:
		if !enumAllowed(string(value.GenerationStatus), "idle", "running", "retry_pending", "failed") {
			return invalidEnum("generation_status", string(value.GenerationStatus))
		}
	case ScheduleVersionPayload:
		if !enumAllowed(string(value.RecurrenceType), "none", "daily", "weekly", "monthly") {
			return invalidEnum("recurrence_type", string(value.RecurrenceType))
		}
		if !enumAllowed(string(value.TimingType), "unscheduled", "date", "time_block") {
			return invalidEnum("timing_type", string(value.TimingType))
		}
	case TaskOccurrencePayload:
		if !enumAllowed(string(value.ExecutionStatus), "open", "active", "blocked", "done", "skipped", "cancelled") {
			return invalidEnum("execution_status", string(value.ExecutionStatus))
		}
	case LearningRoadmapPayload:
		if !enumAllowed(string(value.Status), "draft", "active", "completed", "failed", "archived") {
			return invalidEnum("status", string(value.Status))
		}
	case RoadmapNodePayload:
		if !enumAllowed(string(value.NodeType), "stage", "topic", "milestone") {
			return invalidEnum("node_type", string(value.NodeType))
		}
		if !enumAllowed(string(value.Status), "locked", "available", "in_progress", "mastered", "skipped") {
			return invalidEnum("status", string(value.Status))
		}
	case RoadmapEdgePayload:
		if !enumAllowed(string(value.EdgeType), "prerequisite", "related", "suggested_order") {
			return invalidEnum("edge_type", string(value.EdgeType))
		}
	}
	return nil
}

func enumAllowed(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func invalidEnum(field, value string) error {
	return fmt.Errorf("%s has unknown enum value %q", field, value)
}

func dereferencePayload(payload any) any {
	switch value := payload.(type) {
	case *NotePayload:
		return *value
	case *VoiceNotePayload:
		return *value
	case *InboxPayload:
		return *value
	case *TranscriptionJobPayload:
		return *value
	case *ProjectPayload:
		return *value
	case *TaskPayload:
		return *value
	case *TaskSchedulePayload:
		return *value
	case *ScheduleVersionPayload:
		return *value
	case *TaskOccurrencePayload:
		return *value
	case *LearningRoadmapPayload:
		return *value
	case *RoadmapNodePayload:
		return *value
	case *RoadmapEdgePayload:
		return *value
	case *RoadmapNodeProgressPayload:
		return *value
	default:
		return payload
	}
}

func decodeStrict(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}
