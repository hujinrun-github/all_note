package legacytaskadapter

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/taskdomain"
)

var (
	ErrInvalidLegacyEvent             = errors.New("invalid legacy event")
	ErrLegacyEventIDMapMissing        = errors.New("legacy event ID map missing")
	ErrLegacyEventIDMapConflict       = errors.New("legacy event ID map conflict")
	ErrLegacyEventWorkspaceMismatch   = errors.New("legacy event workspace mismatch")
	ErrLegacyEventDeleteRecurring     = errors.New("legacy event delete requires a non-recurring task")
	ErrLegacyEventBindingMismatch     = errors.New("legacy event task/occurrence binding mismatch")
	ErrLegacyEventDeleteStateConflict = errors.New("legacy event delete state conflict")
)

// LegacyEvent is the compatibility DTO returned by the old Web API. Date
// occurrences intentionally omit StartTime/EndTime instead of fabricating UTC
// midnight instants.
type LegacyEvent struct {
	ID                 string `json:"id"`
	WorkspaceID        string `json:"workspace_id,omitempty"`
	ProjectID          string `json:"project_id"`
	TaskID             string `json:"task_id"`
	OccurrenceID       string `json:"occurrence_id"`
	Title              string `json:"title"`
	StartTime          *int64 `json:"start_time,omitempty"`
	EndTime            *int64 `json:"end_time,omitempty"`
	DueAt              *int64 `json:"due_at,omitempty"`
	IsAllDay           bool   `json:"is_all_day"`
	PlannedDate        string `json:"planned_date,omitempty"`
	AllDayEndDate      string `json:"all_day_end_date,omitempty"`
	Timezone           string `json:"timezone"`
	Location           string `json:"location,omitempty"`
	Kind               string `json:"kind,omitempty"`
	Notes              string `json:"notes,omitempty"`
	NoteID             string `json:"note_id,omitempty"`
	TaskNoteID         string `json:"task_note_id,omitempty"`
	Recurring          bool   `json:"recurring"`
	ExecutionStatus    string `json:"execution_status"`
	Revision           int64  `json:"revision"`
	TaskRevision       int64  `json:"task_revision"`
	ScheduleRevision   int64  `json:"schedule_revision"`
	OccurrenceRevision int64  `json:"occurrence_revision"`
}

// LegacyEventCreate accepts either an instant time block or an explicit local
// all-day date range. All-day requests never interpret legacy timestamps as
// local-midnight dates.
type LegacyEventCreate struct {
	Title         string
	StartTime     *int64
	EndTime       *int64
	IsAllDay      bool
	PlannedDate   string
	AllDayEndDate string
	ProjectID     string
	Location      string
	Kind          string
	Notes         string
	NoteID        string
}

type LegacyEventPatch struct {
	Title         *string
	StartTime     *int64
	EndTime       *int64
	DueAt         *int64
	IsAllDay      *bool
	PlannedDate   *string
	AllDayEndDate *string
	ProjectID     *string
	Location      *string
	Kind          *string
	Notes         *string
	NoteID        *string
	TaskNoteID    *string
}

type EventOccurrenceMetadata struct {
	PlannedDate    string
	PlannedStartAt *time.Time
	PlannedEndAt   *time.Time
	Location       string
	CalendarKind   string
	CalendarNotes  string
	NoteID         string
	AllDayEndDate  string
}

type EventTaskDraft struct {
	WorkspaceID string
	ProjectID   string
	Title       string
	Schedule    taskdomain.Schedule
	Occurrence  EventOccurrenceMetadata
}

// EventProjectionSnapshot binds an occurrence query entry to the immutable
// ScheduleVersion that generated it. Projection uses only this version's
// timezone and never reads a user or process-local timezone.
type EventProjectionSnapshot struct {
	Entry            taskdomain.CalendarEntry
	ScheduleVersion  taskdomain.Schedule
	TaskRevision     int64
	ScheduleRevision int64
}

// LegacyEventIDMap is retained after a legacy DELETE. Tombstoning preserves
// the durable legacy-to-v2 identity needed for compatibility audit and makes
// retries idempotent; adapters must never physically remove this record.
type LegacyEventIDMap struct {
	WorkspaceID        string
	LegacyEventID      string
	TaskID             string
	OccurrenceID       string
	Revision           int64
	Tombstoned         bool
	TombstonedAt       time.Time
	TombstoneCommandID string
	TombstonedBy       string
}

type DeleteEventInput struct {
	WorkspaceID   string
	LegacyEventID string
	Task          taskdomain.TaskAggregate
	Occurrence    taskdomain.Occurrence
	IDMap         LegacyEventIDMap
	CommandID     string
	ActorID       string
	DeletedAt     time.Time
}

// DeleteEventCommandPlan contains only after-images and optimistic revisions.
// There is deliberately no delete flag for Task or Occurrence: the only valid
// compatibility delete is an atomic domain cancellation plus an ID-map
// tombstone.
type DeleteEventCommandPlan struct {
	NoOp                       bool
	ExpectedTaskRevision       int64
	ExpectedOccurrenceRevision int64
	Task                       taskdomain.TaskAggregate
	Occurrence                 taskdomain.Occurrence
	ExecutionLog               taskdomain.ExecutionLog
	IDMapBefore                LegacyEventIDMap
	IDMapAfter                 LegacyEventIDMap
}

func DraftEventTask(request LegacyEventCreate, workspaceID, personalProjectID, scheduleVersionTimezone string) (EventTaskDraft, error) {
	title := strings.TrimSpace(request.Title)
	if title == "" {
		return EventTaskDraft{}, invalidLegacyEvent("title is required")
	}
	projectID := strings.TrimSpace(request.ProjectID)
	if projectID == "" {
		projectID = strings.TrimSpace(personalProjectID)
	}
	if workspaceID == "" || projectID == "" {
		return EventTaskDraft{}, invalidLegacyEvent("workspace and project are required")
	}

	draft := EventTaskDraft{
		WorkspaceID: workspaceID,
		ProjectID:   projectID,
		Title:       title,
		Occurrence: EventOccurrenceMetadata{
			Location: request.Location, CalendarKind: request.Kind, CalendarNotes: request.Notes, NoteID: request.NoteID,
		},
	}
	if request.IsAllDay {
		if request.StartTime != nil || request.EndTime != nil {
			return EventTaskDraft{}, invalidLegacyEvent("all-day event must not contain instant timestamps")
		}
		plannedDate, err := parseLegacyDate(request.PlannedDate, "planned_date")
		if err != nil {
			return EventTaskDraft{}, err
		}
		if request.AllDayEndDate != "" {
			exclusiveEnd, err := parseLegacyDate(request.AllDayEndDate, "all_day_end_date")
			if err != nil {
				return EventTaskDraft{}, err
			}
			if !exclusiveEnd.After(plannedDate) {
				return EventTaskDraft{}, invalidLegacyEvent("all_day_end_date must be after planned_date")
			}
		}
		schedule, err := taskdomain.NormalizeSchedule(taskdomain.ScheduleInput{
			RecurrenceType: taskdomain.RecurrenceNone,
			TimingType:     taskdomain.TimingDate,
			Timezone:       scheduleVersionTimezone,
			StartsOn:       request.PlannedDate,
		})
		if err != nil {
			return EventTaskDraft{}, wrapLegacyEventError("invalid all-day schedule", err)
		}
		draft.Schedule = schedule
		draft.Occurrence.PlannedDate = request.PlannedDate
		draft.Occurrence.AllDayEndDate = request.AllDayEndDate
		return draft, nil
	}

	if request.StartTime == nil || request.EndTime == nil {
		return EventTaskDraft{}, invalidLegacyEvent("time-block event requires start_time and end_time")
	}
	start := time.Unix(*request.StartTime, 0).UTC()
	end := time.Unix(*request.EndTime, 0).UTC()
	if !end.After(start) {
		return EventTaskDraft{}, invalidLegacyEvent("end_time must follow start_time")
	}
	durationSeconds := end.Unix() - start.Unix()
	if durationSeconds%60 != 0 || durationSeconds/60 > int64(math.MaxInt) {
		return EventTaskDraft{}, invalidLegacyEvent("event duration must be a whole number of minutes")
	}
	location, err := loadLegacyLocation(scheduleVersionTimezone)
	if err != nil {
		return EventTaskDraft{}, err
	}
	localStart := start.In(location)
	plannedDate := localStart.Format("2006-01-02")
	schedule, err := taskdomain.NormalizeSchedule(taskdomain.ScheduleInput{
		RecurrenceType:  taskdomain.RecurrenceNone,
		TimingType:      taskdomain.TimingTimeBlock,
		Timezone:        scheduleVersionTimezone,
		StartsOn:        plannedDate,
		LocalStartTime:  localStart.Format("15:04:05"),
		DurationMinutes: int(durationSeconds / 60),
	})
	if err != nil {
		return EventTaskDraft{}, wrapLegacyEventError("invalid time-block schedule", err)
	}
	draft.Schedule = schedule
	draft.Occurrence.PlannedDate = plannedDate
	draft.Occurrence.PlannedStartAt = cloneLegacyTime(&start)
	draft.Occurrence.PlannedEndAt = cloneLegacyTime(&end)
	return draft, nil
}

func ProjectLegacyEvent(snapshot EventProjectionSnapshot) (LegacyEvent, error) {
	entry := snapshot.Entry
	version := snapshot.ScheduleVersion
	if entry.Revision < 1 || !legacyEventExecutionRepresentable(entry.Status) {
		return LegacyEvent{}, invalidLegacyEvent("event revision or execution status cannot be represented")
	}
	location, err := loadLegacyLocation(version.Timezone)
	if err != nil {
		return LegacyEvent{}, err
	}
	if version.TimingType != entry.DisplayType {
		return LegacyEvent{}, invalidLegacyEvent("calendar entry timing does not match schedule version")
	}

	event := LegacyEvent{
		ID:          entry.OccurrenceID,
		WorkspaceID: entry.WorkspaceID, ProjectID: entry.ProjectID, TaskID: entry.TaskID, OccurrenceID: entry.OccurrenceID,
		Title: entry.Title, PlannedDate: entry.PlannedDate, AllDayEndDate: entry.AllDayEndDate, Timezone: version.Timezone,
		Location: entry.Location, Kind: entry.CalendarKind, Notes: entry.CalendarNotes,
		NoteID: entry.OccurrenceNoteID, TaskNoteID: entry.TaskNoteID,
		Recurring: entry.Recurring, ExecutionStatus: string(entry.Status), Revision: entry.Revision,
		TaskRevision: snapshot.TaskRevision, ScheduleRevision: snapshot.ScheduleRevision, OccurrenceRevision: entry.Revision,
	}
	if entry.DueAt != nil {
		due := entry.DueAt.Unix()
		event.DueAt = &due
	}

	switch entry.DisplayType {
	case taskdomain.TimingTimeBlock:
		if entry.StartAt == nil || entry.EndAt == nil || !entry.EndAt.After(*entry.StartAt) {
			return LegacyEvent{}, invalidLegacyEvent("time-block projection requires valid start and end")
		}
		if entry.PlannedDate == "" || entry.StartAt.In(location).Format("2006-01-02") != entry.PlannedDate {
			return LegacyEvent{}, invalidLegacyEvent("planned_date does not match schedule version timezone")
		}
		start := entry.StartAt.Unix()
		end := entry.EndAt.Unix()
		event.StartTime = &start
		event.EndTime = &end
	case taskdomain.TimingDate:
		plannedDate, err := parseLegacyDate(entry.PlannedDate, "planned_date")
		if err != nil {
			return LegacyEvent{}, err
		}
		if entry.StartAt != nil || entry.EndAt != nil {
			return LegacyEvent{}, invalidLegacyEvent("date projection must not contain instant timestamps")
		}
		if entry.AllDayEndDate != "" {
			exclusiveEnd, err := parseLegacyDate(entry.AllDayEndDate, "all_day_end_date")
			if err != nil {
				return LegacyEvent{}, err
			}
			if !exclusiveEnd.After(plannedDate) {
				return LegacyEvent{}, invalidLegacyEvent("all_day_end_date must be after planned_date")
			}
		}
		event.IsAllDay = true
	default:
		return LegacyEvent{}, invalidLegacyEvent("unscheduled occurrence cannot be projected as an event")
	}
	return event, nil
}

func legacyEventExecutionRepresentable(status taskdomain.ExecutionStatus) bool {
	switch status {
	case taskdomain.ExecutionStatusOpen, taskdomain.ExecutionStatusActive, taskdomain.ExecutionStatusBlocked,
		taskdomain.ExecutionStatusDone, taskdomain.ExecutionStatusSkipped, taskdomain.ExecutionStatusCancelled:
		return true
	default:
		return false
	}
}

// ScheduleFromVersion reconstructs the validated immutable schedule used to
// interpret a legacy Event projection. In particular, callers must use the
// version that generated the occurrence rather than the workspace's current
// timezone or the schedule header's current version.
func ScheduleFromVersion(version taskdomain.ScheduleVersion) (taskdomain.Schedule, error) {
	return taskdomain.NormalizeSchedule(taskdomain.ScheduleInput{
		RecurrenceType:  version.RecurrenceType,
		TimingType:      version.TimingType,
		Timezone:        version.Timezone,
		StartsOn:        version.StartsOn,
		EndsOn:          version.EndsOn,
		Rule:            json.RawMessage(version.RecurrenceRule),
		LocalStartTime:  version.LocalStartTime,
		DurationMinutes: version.DurationMinutes,
	})
}

// MergeLegacyEventPatch is field-presence based: omitted compatibility fields
// retain their current values, while a present empty string explicitly clears
// a nullable string at the later persistence boundary.
func MergeLegacyEventPatch(current LegacyEvent, patch LegacyEventPatch) LegacyEvent {
	merged := current
	merged.StartTime = cloneLegacyInt64(current.StartTime)
	merged.EndTime = cloneLegacyInt64(current.EndTime)
	merged.DueAt = cloneLegacyInt64(current.DueAt)
	if patch.Title != nil {
		merged.Title = *patch.Title
	}
	if patch.StartTime != nil {
		merged.StartTime = cloneLegacyInt64(patch.StartTime)
	}
	if patch.EndTime != nil {
		merged.EndTime = cloneLegacyInt64(patch.EndTime)
	}
	if patch.DueAt != nil {
		merged.DueAt = cloneLegacyInt64(patch.DueAt)
	}
	if patch.IsAllDay != nil {
		merged.IsAllDay = *patch.IsAllDay
	}
	if patch.PlannedDate != nil {
		merged.PlannedDate = *patch.PlannedDate
	}
	if patch.AllDayEndDate != nil {
		merged.AllDayEndDate = *patch.AllDayEndDate
	}
	if patch.ProjectID != nil {
		merged.ProjectID = *patch.ProjectID
	}
	if patch.Location != nil {
		merged.Location = *patch.Location
	}
	if patch.Kind != nil {
		merged.Kind = *patch.Kind
	}
	if patch.Notes != nil {
		merged.Notes = *patch.Notes
	}
	if patch.NoteID != nil {
		merged.NoteID = *patch.NoteID
	}
	if patch.TaskNoteID != nil {
		merged.TaskNoteID = *patch.TaskNoteID
	}
	return merged
}

// PlanDeleteEvent translates the legacy DELETE contract into one atomic v2
// command plan. It performs no persistence and never mutates its input.
func PlanDeleteEvent(input DeleteEventInput) (DeleteEventCommandPlan, error) {
	if strings.TrimSpace(input.WorkspaceID) == "" || strings.TrimSpace(input.LegacyEventID) == "" ||
		strings.TrimSpace(input.CommandID) == "" || strings.TrimSpace(input.ActorID) == "" || input.DeletedAt.IsZero() {
		return DeleteEventCommandPlan{}, invalidLegacyEvent("delete identity and audit fields are required")
	}
	if missingLegacyEventIDMap(input.IDMap) {
		return DeleteEventCommandPlan{}, ErrLegacyEventIDMapMissing
	}
	if input.Task.WorkspaceID != input.WorkspaceID || input.Occurrence.WorkspaceID != input.WorkspaceID || input.IDMap.WorkspaceID != input.WorkspaceID {
		return DeleteEventCommandPlan{}, ErrLegacyEventWorkspaceMismatch
	}
	if input.IDMap.LegacyEventID != input.LegacyEventID || input.IDMap.TaskID != input.Task.TaskID || input.IDMap.OccurrenceID != input.Occurrence.ID {
		return DeleteEventCommandPlan{}, ErrLegacyEventIDMapConflict
	}
	if input.Task.TaskID == "" || input.Occurrence.ID == "" || input.Occurrence.TaskID != input.Task.TaskID ||
		len(input.Task.Occurrences) != 1 || !sameLegacyOccurrence(input.Task.Occurrences[0], input.Occurrence) {
		return DeleteEventCommandPlan{}, ErrLegacyEventBindingMismatch
	}
	if input.Task.Recurring || input.Occurrence.Recurring {
		return DeleteEventCommandPlan{}, ErrLegacyEventDeleteRecurring
	}
	if input.Occurrence.OccurrenceKey != "once" {
		return DeleteEventCommandPlan{}, ErrLegacyEventBindingMismatch
	}
	if input.Task.Revision < 1 || input.Occurrence.Revision < 1 || input.IDMap.Revision < 1 {
		return DeleteEventCommandPlan{}, ErrLegacyEventBindingMismatch
	}

	if input.Task.LifecycleStatus == taskdomain.TaskLifecycleCancelled && input.Occurrence.ExecutionStatus == taskdomain.ExecutionStatusCancelled {
		if !validLegacyEventTombstone(input.IDMap) {
			return DeleteEventCommandPlan{}, ErrLegacyEventDeleteStateConflict
		}
		return DeleteEventCommandPlan{NoOp: true, IDMapBefore: input.IDMap, IDMapAfter: input.IDMap}, nil
	}
	if input.IDMap.Tombstoned || input.Task.LifecycleStatus == taskdomain.TaskLifecycleCancelled || input.Occurrence.ExecutionStatus == taskdomain.ExecutionStatusCancelled {
		return DeleteEventCommandPlan{}, ErrLegacyEventDeleteStateConflict
	}

	nextTaskStatus, err := taskdomain.CancelTask(input.Task.LifecycleStatus)
	if err != nil {
		return DeleteEventCommandPlan{}, err
	}
	nextOccurrence, log, err := taskdomain.CancelOccurrence(input.Occurrence, taskdomain.ExecutionTransition{
		LogID: input.CommandID, ActorID: input.ActorID, At: input.DeletedAt,
	})
	if err != nil {
		return DeleteEventCommandPlan{}, err
	}

	nextTask := input.Task
	nextTask.Occurrences = append([]taskdomain.Occurrence(nil), input.Task.Occurrences...)
	nextTask.LifecycleStatus = nextTaskStatus
	nextTask.GenerationEnabled = false
	nextTask.Revision++
	nextTask.Occurrences[0] = nextOccurrence

	nextIDMap := input.IDMap
	nextIDMap.Revision++
	nextIDMap.Tombstoned = true
	nextIDMap.TombstonedAt = input.DeletedAt
	nextIDMap.TombstoneCommandID = input.CommandID
	nextIDMap.TombstonedBy = input.ActorID

	return DeleteEventCommandPlan{
		ExpectedTaskRevision:       input.Task.Revision,
		ExpectedOccurrenceRevision: input.Occurrence.Revision,
		Task:                       nextTask,
		Occurrence:                 nextOccurrence,
		ExecutionLog:               log,
		IDMapBefore:                input.IDMap,
		IDMapAfter:                 nextIDMap,
	}, nil
}

func missingLegacyEventIDMap(idMap LegacyEventIDMap) bool {
	return strings.TrimSpace(idMap.WorkspaceID) == "" || strings.TrimSpace(idMap.LegacyEventID) == "" ||
		strings.TrimSpace(idMap.TaskID) == "" || strings.TrimSpace(idMap.OccurrenceID) == ""
}

func validLegacyEventTombstone(idMap LegacyEventIDMap) bool {
	return idMap.Tombstoned && !idMap.TombstonedAt.IsZero() && strings.TrimSpace(idMap.TombstoneCommandID) != "" && strings.TrimSpace(idMap.TombstonedBy) != ""
}

func sameLegacyOccurrence(aggregateOccurrence, occurrence taskdomain.Occurrence) bool {
	return aggregateOccurrence.WorkspaceID == occurrence.WorkspaceID && aggregateOccurrence.ID == occurrence.ID &&
		aggregateOccurrence.TaskID == occurrence.TaskID && aggregateOccurrence.OccurrenceKey == occurrence.OccurrenceKey &&
		aggregateOccurrence.ExecutionStatus == occurrence.ExecutionStatus && aggregateOccurrence.Recurring == occurrence.Recurring &&
		aggregateOccurrence.Revision == occurrence.Revision && equalLegacyTime(aggregateOccurrence.ActualStartAt, occurrence.ActualStartAt) &&
		equalLegacyTime(aggregateOccurrence.CompletedAt, occurrence.CompletedAt) && aggregateOccurrence.BlockedReason == occurrence.BlockedReason &&
		aggregateOccurrence.NextAction == occurrence.NextAction
}

func equalLegacyTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func loadLegacyLocation(value string) (*time.Location, error) {
	if value == "" || value == "Local" || strings.TrimSpace(value) != value || (value != "UTC" && !strings.Contains(value, "/")) {
		return nil, invalidLegacyEvent("invalid schedule version timezone")
	}
	location, err := time.LoadLocation(value)
	if err != nil {
		return nil, wrapLegacyEventError("invalid schedule version timezone", err)
	}
	return location, nil
}

func parseLegacyDate(value, field string) (time.Time, error) {
	if value == "" {
		return time.Time{}, invalidLegacyEvent(field + " is required")
	}
	date, err := time.Parse("2006-01-02", value)
	if err != nil || date.Format("2006-01-02") != value {
		return time.Time{}, invalidLegacyEvent("invalid " + field)
	}
	return date, nil
}

func invalidLegacyEvent(detail string) error {
	return fmt.Errorf("%w: %s", ErrInvalidLegacyEvent, detail)
}

func wrapLegacyEventError(detail string, cause error) error {
	return fmt.Errorf("%w: %s: %v", ErrInvalidLegacyEvent, detail, cause)
}

func cloneLegacyTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneLegacyInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
