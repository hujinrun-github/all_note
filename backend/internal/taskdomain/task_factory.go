package taskdomain

import (
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strings"
	"time"
)

var ErrInvalidTaskCreation = errors.New("invalid task creation input")

type TaskNoteIdentity struct {
	WorkspaceID string
	NoteID      string
}

type TaskCreationInput struct {
	WorkspaceID string
	Project     ProjectIdentity
	Roadmap     *Roadmap
	TaskNote    *TaskNoteIdentity

	TaskID      string
	ActorID     string
	ActorTime   time.Time
	Title       string
	Description string
	Priority    int
	SortOrder   float64

	Schedule        ScheduleInput
	AllDayEndDate   string
	DueAt           *time.Time
	SelectedOffsets map[string]int
}

type TaskCreationOffsetCandidates struct {
	OccurrenceKey string
	LocalDate     string
	Candidates    []OffsetCandidate
}

// TaskCreationDetails makes the version effective boundary and inclusive
// initial generation watermark explicit. OffsetCandidates remains populated
// when a DST ambiguity prevents creation, allowing a handler to ask the user
// to select one of the exact offsets without constructing persistence rows.
type TaskCreationDetails struct {
	EffectiveFrom    string
	GenerateThrough  string
	OffsetCandidates []TaskCreationOffsetCandidates
}

type TaskFactory struct{}

func (TaskFactory) Build(input TaskCreationInput) (TaskAggregateSnapshot, TaskCreationDetails, error) {
	return buildTaskAggregateSnapshot(input)
}

func BuildTaskAggregateSnapshot(input TaskCreationInput) (TaskAggregateSnapshot, TaskCreationDetails, error) {
	return TaskFactory{}.Build(input)
}

func buildTaskAggregateSnapshot(input TaskCreationInput) (TaskAggregateSnapshot, TaskCreationDetails, error) {
	if err := validateTaskCreationInput(input); err != nil {
		return TaskAggregateSnapshot{}, TaskCreationDetails{}, err
	}
	normalized, err := NormalizeSchedule(input.Schedule)
	if err != nil {
		return TaskAggregateSnapshot{}, TaskCreationDetails{}, err
	}
	if input.AllDayEndDate != "" && (normalized.TimingType != TimingDate || normalized.RecurrenceType != RecurrenceNone) {
		return TaskAggregateSnapshot{}, TaskCreationDetails{}, ErrInvalidTaskCreation
	}

	details, effective, keys, err := taskCreationWindow(input.ActorTime, normalized)
	if err != nil {
		return TaskAggregateSnapshot{}, TaskCreationDetails{}, err
	}
	version, err := taskCreationScheduleVersion(input.WorkspaceID, input.TaskID, effective, normalized)
	if err != nil {
		return TaskAggregateSnapshot{}, TaskCreationDetails{}, err
	}

	snapshot := TaskAggregateSnapshot{
		Task: TaskRecord{
			WorkspaceID: input.WorkspaceID, ID: input.TaskID, ProjectID: input.Project.ProjectID,
			Title: strings.TrimSpace(input.Title), Description: input.Description,
			LifecycleStatus: TaskLifecycleDraft, Priority: input.Priority, SortOrder: input.SortOrder, Revision: 1,
		},
		Schedule: ScheduleHeader{
			WorkspaceID: input.WorkspaceID, TaskID: input.TaskID, Revision: 1, CurrentScheduleRevision: 1,
		},
		Versions: []ScheduleVersion{version},
	}
	if input.Roadmap != nil {
		snapshot.Task.RoadmapNodeID = input.Roadmap.ID
	}
	if input.TaskNote != nil {
		snapshot.Task.NoteID = input.TaskNote.NoteID
	}

	for _, key := range keys {
		occurrence, candidates, materializeErr := materializeTaskCreationOccurrence(input, normalized, key)
		if len(candidates) > 0 {
			details.OffsetCandidates = append(details.OffsetCandidates, TaskCreationOffsetCandidates{
				OccurrenceKey: key, LocalDate: taskCreationLocalDate(normalized, key),
				Candidates: cloneOffsetCandidates(candidates),
			})
		}
		if materializeErr != nil {
			sortTaskCreationCandidates(details.OffsetCandidates)
			return TaskAggregateSnapshot{}, details, materializeErr
		}
		snapshot.Occurrences = append(snapshot.Occurrences, occurrence)
	}
	sort.Slice(snapshot.Occurrences, func(i, j int) bool {
		return snapshot.Occurrences[i].OccurrenceKey < snapshot.Occurrences[j].OccurrenceKey
	})
	sortTaskCreationCandidates(details.OffsetCandidates)
	if err := ValidateTaskAggregateSnapshot(snapshot); err != nil {
		return TaskAggregateSnapshot{}, TaskCreationDetails{}, err
	}
	return snapshot, details, nil
}

func validateTaskCreationInput(input TaskCreationInput) error {
	if strings.TrimSpace(input.WorkspaceID) == "" || strings.TrimSpace(input.TaskID) == "" ||
		strings.TrimSpace(input.ActorID) == "" || input.ActorTime.IsZero() || strings.TrimSpace(input.Title) == "" ||
		input.Priority < 0 || input.Priority > 3 || math.IsNaN(input.SortOrder) || math.IsInf(input.SortOrder, 0) ||
		input.Project.WorkspaceID != input.WorkspaceID || strings.TrimSpace(input.Project.ProjectID) == "" {
		return ErrInvalidTaskCreation
	}
	if input.Roadmap != nil && (input.Roadmap.WorkspaceID != input.WorkspaceID || input.Roadmap.ProjectID != input.Project.ProjectID || strings.TrimSpace(input.Roadmap.ID) == "") {
		return ErrInvalidTaskCreation
	}
	if input.TaskNote != nil && (input.TaskNote.WorkspaceID != input.WorkspaceID || strings.TrimSpace(input.TaskNote.NoteID) == "") {
		return ErrInvalidTaskCreation
	}
	return nil
}

func taskCreationWindow(actorTime time.Time, schedule Schedule) (TaskCreationDetails, ScheduleEffectiveRange, []string, error) {
	if schedule.RecurrenceType == RecurrenceNone {
		return TaskCreationDetails{}, ScheduleEffectiveRange{}, []string{"once"}, nil
	}
	location, err := loadIANALocation(schedule.Timezone)
	if err != nil {
		return TaskCreationDetails{}, ScheduleEffectiveRange{}, nil, err
	}
	local := actorTime.In(location)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
	effectiveFrom := formatLocalDate(today)
	through := formatLocalDate(today.AddDate(0, 0, 90))
	details := TaskCreationDetails{EffectiveFrom: effectiveFrom, GenerateThrough: through}
	effective := ScheduleEffectiveRange{From: effectiveFrom}
	keys, err := CalculateOccurrenceKeys(schedule, effective, OccurrenceWindow{
		From: effectiveFrom, To: formatLocalDate(today.AddDate(0, 0, 91)),
	})
	if err != nil {
		return TaskCreationDetails{}, ScheduleEffectiveRange{}, nil, err
	}
	return details, effective, keys, nil
}

func taskCreationScheduleVersion(workspaceID, taskID string, effective ScheduleEffectiveRange, schedule Schedule) (ScheduleVersion, error) {
	rule := `{}`
	if schedule.Rule != nil {
		encoded, err := json.Marshal(schedule.Rule)
		if err != nil {
			return ScheduleVersion{}, err
		}
		rule = string(encoded)
	}
	return ScheduleVersion{
		WorkspaceID: workspaceID, TaskID: taskID, ScheduleRevision: 1,
		EffectiveFrom: effective.From, EffectiveTo: effective.To,
		RecurrenceType: schedule.RecurrenceType, TimingType: schedule.TimingType, Timezone: schedule.Timezone,
		StartsOn: schedule.StartsOn, EndsOn: schedule.EndsOn, RecurrenceRule: rule,
		LocalStartTime: schedule.LocalStartTime, DurationMinutes: schedule.DurationMinutes,
	}, nil
}

func materializeTaskCreationOccurrence(input TaskCreationInput, schedule Schedule, key string) (OccurrenceRecord, []OffsetCandidate, error) {
	localDate := taskCreationLocalDate(schedule, key)
	record := OccurrenceRecord{
		WorkspaceID: input.WorkspaceID,
		ID:          DeterministicOccurrenceID(input.WorkspaceID, input.TaskID, key),
		TaskID:      input.TaskID, OccurrenceKey: key,
		ExecutionStatus: ExecutionStatusOpen, Revision: 1, GeneratedScheduleRevision: 1,
		DueAt: cloneTaskCreationTime(input.DueAt),
	}
	switch schedule.TimingType {
	case TimingUnscheduled:
		return record, nil, nil
	case TimingDate:
		exclusiveEnd := ""
		if schedule.RecurrenceType == RecurrenceNone {
			exclusiveEnd = input.AllDayEndDate
		}
		rangeValue, err := ResolveAllDayRangeUTC(localDate, exclusiveEnd, schedule.Timezone)
		if err != nil {
			return OccurrenceRecord{}, nil, err
		}
		record.PlannedDate = rangeValue.StartDate
		record.AllDayEndDate = rangeValue.ExclusiveEndDate
		return record, nil, nil
	case TimingTimeBlock:
		selected := selectedTaskCreationOffset(input.SelectedOffsets, localDate, key)
		instantRange, candidates, err := ResolveTimeBlockUTC(localDate, schedule.LocalStartTime, schedule.Timezone, schedule.DurationMinutes, selected)
		if err != nil {
			return OccurrenceRecord{}, candidates, err
		}
		record.PlannedDate = localDate
		record.PlannedStartAt = cloneTaskCreationTime(&instantRange.StartUTC)
		record.PlannedEndAt = cloneTaskCreationTime(&instantRange.EndUTC)
		return record, candidates, nil
	default:
		return OccurrenceRecord{}, nil, ErrInvalidTaskCreation
	}
}

func taskCreationLocalDate(schedule Schedule, key string) string {
	if schedule.RecurrenceType == RecurrenceNone {
		return schedule.StartsOn
	}
	return key
}

func selectedTaskCreationOffset(selected map[string]int, localDate, key string) *int {
	if offset, exists := selected[localDate]; exists {
		value := offset
		return &value
	}
	if offset, exists := selected[key]; exists {
		value := offset
		return &value
	}
	return nil
}

func cloneTaskCreationTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneOffsetCandidates(candidates []OffsetCandidate) []OffsetCandidate {
	return append([]OffsetCandidate(nil), candidates...)
}

func sortTaskCreationCandidates(candidates []TaskCreationOffsetCandidates) {
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].OccurrenceKey != candidates[j].OccurrenceKey {
			return candidates[i].OccurrenceKey < candidates[j].OccurrenceKey
		}
		return candidates[i].LocalDate < candidates[j].LocalDate
	})
}
