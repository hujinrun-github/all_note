package taskdomain

import (
	"errors"
	"sort"
	"time"
)

var (
	ErrInvalidOccurrenceListFilter     = errors.New("invalid occurrence list filter")
	ErrInvalidProjectListFilter        = errors.New("invalid project list filter")
	ErrInvalidTaskDefinitionListFilter = errors.New("invalid task definition list filter")
)

// ProjectListFilter selects the persisted project catalog of the workspace
// bound reader. Nil dimensions mean "all"; providers reject unknown enum
// values instead of silently returning an empty catalog.
type ProjectListFilter struct {
	Kind    *ProjectKind
	Horizon *ProjectHorizon
	Status  *ProjectStatus
}

// TaskDefinitionListFilter selects stable task definitions independently of
// occurrence execution state.
type TaskDefinitionListFilter struct {
	ProjectID       string
	LifecycleStatus *TaskLifecycleStatus
}

// TaskDefinitionSnapshot is the list read model needed by the Web API. The
// schedule revision is the CAS revision of the schedule header, while the
// current revision identifies the immutable version selected by that header.
type TaskDefinitionSnapshot struct {
	Task                    TaskRecord
	ScheduleRevision        int64
	CurrentScheduleRevision int64
}

func ValidateProjectListFilter(filter ProjectListFilter) error {
	if (filter.Kind != nil && !validProjectKind(*filter.Kind)) ||
		(filter.Horizon != nil && !validProjectHorizon(*filter.Horizon)) ||
		(filter.Status != nil && !validProjectStatus(*filter.Status)) {
		return ErrInvalidProjectListFilter
	}
	return nil
}

func ValidateTaskDefinitionListFilter(filter TaskDefinitionListFilter) error {
	if filter.LifecycleStatus != nil && !knownTaskLifecycleStatus(*filter.LifecycleStatus) {
		return ErrInvalidTaskDefinitionListFilter
	}
	return nil
}

type OccurrenceListScope string

const (
	OccurrenceListAll         OccurrenceListScope = "all"
	OccurrenceListToday       OccurrenceListScope = "today"
	OccurrenceListUpcoming    OccurrenceListScope = "upcoming"
	OccurrenceListOverdue     OccurrenceListScope = "overdue"
	OccurrenceListUnscheduled OccurrenceListScope = "unscheduled"
	OccurrenceListCompleted   OccurrenceListScope = "completed"
	OccurrenceListCalendar    OccurrenceListScope = "calendar"
)

// OccurrenceListFilter is applied inside a workspace-bound reader. From and
// To always form a half-open [from,to) range when the selected scope uses a
// range. Timezone defines the local date boundaries used for date schedules.
type OccurrenceListFilter struct {
	Scope     OccurrenceListScope
	From      time.Time
	To        time.Time
	Timezone  string
	ProjectID string
	TaskID    string
	Statuses  []ExecutionStatus
	Recurring *bool
}

// TaskAggregateQueryResult contains both the command aggregate and the
// persistence snapshots needed for optimistic writes and schedule changes.
type TaskAggregateQueryResult struct {
	Aggregate   TaskAggregate
	Task        TaskRecord
	Schedule    ScheduleHeader
	Versions    []ScheduleVersion
	Occurrences []QueryOccurrenceSnapshot
}

// QueryOccurrenceSnapshot is the repository-independent after-image consumed
// by the Calendar, Today, and task-list read-model projectors.
type QueryOccurrenceSnapshot struct {
	WorkspaceID               string
	ProjectID                 string
	TaskID                    string
	OccurrenceID              string
	OccurrenceKey             string
	Title                     string
	Description               string
	TimingType                TimingType
	Timezone                  string
	PlannedDate               string
	PlannedStartAt            *time.Time
	PlannedEndAt              *time.Time
	DueAt                     *time.Time
	Status                    ExecutionStatus
	Recurring                 bool
	Revision                  int64
	ProjectRevision           int64
	TaskRevision              int64
	ScheduleRevision          int64
	GeneratedScheduleRevision int64
	LifecycleStatus           TaskLifecycleStatus
	Priority                  int
	SortOrder                 float64
	ActualStartAt             *time.Time
	CompletedAt               *time.Time
	BlockedReason             string
	NextAction                string
	MarkedForToday            bool
	Location                  string
	CalendarKind              string
	CalendarNotes             string
	TaskNoteID                string
	OccurrenceNoteID          string
	AllDayEndDate             string
}

// CalendarEntry is a query DTO. It is projected from an occurrence and does
// not represent another persisted domain entity.
type CalendarEntry struct {
	WorkspaceID      string
	ProjectID        string
	TaskID           string
	OccurrenceID     string
	Title            string
	DisplayType      TimingType
	Timezone         string
	PlannedDate      string
	StartAt          *time.Time
	EndAt            *time.Time
	DueAt            *time.Time
	Status           ExecutionStatus
	Recurring        bool
	Revision         int64
	Location         string
	CalendarKind     string
	CalendarNotes    string
	TaskNoteID       string
	OccurrenceNoteID string
	AllDayEndDate    string
}

type CalendarProjection struct {
	TimeBlocks []CalendarEntry
	AllDay     []CalendarEntry
}

// TaskListItem deliberately retains execution status instead of flattening it
// into a completed boolean.
type TaskListItem struct {
	WorkspaceID      string
	ProjectID        string
	TaskID           string
	OccurrenceID     string
	Title            string
	TimingType       TimingType
	Timezone         string
	PlannedDate      string
	PlannedStartAt   *time.Time
	PlannedEndAt     *time.Time
	DueAt            *time.Time
	Status           ExecutionStatus
	Recurring        bool
	Revision         int64
	Location         string
	CalendarKind     string
	CalendarNotes    string
	TaskNoteID       string
	OccurrenceNoteID string
	AllDayEndDate    string
}

type TodayProjection struct {
	Default []TaskListItem
	Overdue []TaskListItem
}

// BuildCalendarProjection sends time blocks to the time grid, date schedules
// to the all-day lane, and excludes unscheduled occurrences.
func BuildCalendarProjection(snapshots []QueryOccurrenceSnapshot) (CalendarProjection, error) {
	projection := CalendarProjection{
		TimeBlocks: make([]CalendarEntry, 0),
		AllDay:     make([]CalendarEntry, 0),
	}
	for _, snapshot := range snapshots {
		if err := validateQueryTiming(snapshot); err != nil {
			return CalendarProjection{}, err
		}
		if snapshot.TimingType == TimingUnscheduled {
			continue
		}
		entry := projectCalendarEntry(snapshot)
		switch snapshot.TimingType {
		case TimingTimeBlock:
			projection.TimeBlocks = append(projection.TimeBlocks, entry)
		case TimingDate:
			projection.AllDay = append(projection.AllDay, entry)
		}
	}

	sort.SliceStable(projection.TimeBlocks, func(left, right int) bool {
		leftStart := projection.TimeBlocks[left].StartAt
		rightStart := projection.TimeBlocks[right].StartAt
		if leftStart.Equal(*rightStart) {
			return projection.TimeBlocks[left].OccurrenceID < projection.TimeBlocks[right].OccurrenceID
		}
		return leftStart.Before(*rightStart)
	})
	sort.SliceStable(projection.AllDay, func(left, right int) bool {
		if projection.AllDay[left].PlannedDate == projection.AllDay[right].PlannedDate {
			return projection.AllDay[left].OccurrenceID < projection.AllDay[right].OccurrenceID
		}
		return projection.AllDay[left].PlannedDate < projection.AllDay[right].PlannedDate
	})
	return projection, nil
}

// BuildTodayProjection returns separate default and overdue collections. An
// overdue item never leaks into Default, even when it was planned for today.
func BuildTodayProjection(snapshots []QueryOccurrenceSnapshot, now time.Time, timezone string) (TodayProjection, error) {
	if now.IsZero() {
		return TodayProjection{}, invalidSchedule("today projection now is required")
	}
	location, err := loadIANALocation(timezone)
	if err != nil {
		return TodayProjection{}, err
	}
	today := now.In(location).Format("2006-01-02")
	projection := TodayProjection{
		Default: make([]TaskListItem, 0),
		Overdue: make([]TaskListItem, 0),
	}
	for _, snapshot := range snapshots {
		if err := validateQueryTiming(snapshot); err != nil {
			return TodayProjection{}, err
		}
		item := projectTaskListItem(snapshot)
		if queryOccurrenceIsOverdue(snapshot, now) {
			projection.Overdue = append(projection.Overdue, item)
			continue
		}
		if queryOccurrenceIsToday(snapshot, today, location) {
			projection.Default = append(projection.Default, item)
		}
	}
	return projection, nil
}

// BuildTaskList is a lossless status projection; filtering by a particular
// status is a caller concern.
func BuildTaskList(snapshots []QueryOccurrenceSnapshot) []TaskListItem {
	items := make([]TaskListItem, 0, len(snapshots))
	for _, snapshot := range snapshots {
		items = append(items, projectTaskListItem(snapshot))
	}
	return items
}

func projectCalendarEntry(snapshot QueryOccurrenceSnapshot) CalendarEntry {
	return CalendarEntry{
		WorkspaceID: snapshot.WorkspaceID, ProjectID: snapshot.ProjectID, TaskID: snapshot.TaskID, OccurrenceID: snapshot.OccurrenceID,
		Title: snapshot.Title, DisplayType: snapshot.TimingType, Timezone: snapshot.Timezone, PlannedDate: snapshot.PlannedDate,
		StartAt: cloneQueryTime(snapshot.PlannedStartAt), EndAt: cloneQueryTime(snapshot.PlannedEndAt), DueAt: cloneQueryTime(snapshot.DueAt),
		Status: snapshot.Status, Recurring: snapshot.Recurring, Revision: snapshot.Revision,
		Location: snapshot.Location, CalendarKind: snapshot.CalendarKind, CalendarNotes: snapshot.CalendarNotes,
		TaskNoteID: snapshot.TaskNoteID, OccurrenceNoteID: snapshot.OccurrenceNoteID, AllDayEndDate: snapshot.AllDayEndDate,
	}
}

func projectTaskListItem(snapshot QueryOccurrenceSnapshot) TaskListItem {
	return TaskListItem{
		WorkspaceID: snapshot.WorkspaceID, ProjectID: snapshot.ProjectID, TaskID: snapshot.TaskID, OccurrenceID: snapshot.OccurrenceID,
		Title: snapshot.Title, TimingType: snapshot.TimingType, Timezone: snapshot.Timezone, PlannedDate: snapshot.PlannedDate,
		PlannedStartAt: cloneQueryTime(snapshot.PlannedStartAt), PlannedEndAt: cloneQueryTime(snapshot.PlannedEndAt), DueAt: cloneQueryTime(snapshot.DueAt),
		Status: snapshot.Status, Recurring: snapshot.Recurring, Revision: snapshot.Revision,
		Location: snapshot.Location, CalendarKind: snapshot.CalendarKind, CalendarNotes: snapshot.CalendarNotes,
		TaskNoteID: snapshot.TaskNoteID, OccurrenceNoteID: snapshot.OccurrenceNoteID, AllDayEndDate: snapshot.AllDayEndDate,
	}
}

func validateQueryTiming(snapshot QueryOccurrenceSnapshot) error {
	location, err := loadIANALocation(snapshot.Timezone)
	if err != nil {
		return err
	}
	switch snapshot.TimingType {
	case TimingUnscheduled:
		if snapshot.PlannedDate != "" || snapshot.PlannedStartAt != nil || snapshot.PlannedEndAt != nil || snapshot.AllDayEndDate != "" {
			return invalidSchedule("unscheduled query snapshot contains planning fields")
		}
	case TimingDate:
		plannedDate, err := requiredGeneratorDate(snapshot.PlannedDate, "planned_date")
		if err != nil {
			return err
		}
		if snapshot.PlannedStartAt != nil || snapshot.PlannedEndAt != nil {
			return invalidSchedule("date query snapshot contains time-block instants")
		}
		if snapshot.AllDayEndDate != "" {
			exclusiveEnd, err := requiredGeneratorDate(snapshot.AllDayEndDate, "all_day_end_date")
			if err != nil {
				return err
			}
			if !exclusiveEnd.After(plannedDate) {
				return invalidSchedule("all_day_end_date must be after planned_date")
			}
		}
	case TimingTimeBlock:
		if snapshot.PlannedStartAt == nil || snapshot.PlannedEndAt == nil {
			return invalidSchedule("time_block query snapshot requires start and end")
		}
		if !snapshot.PlannedEndAt.After(*snapshot.PlannedStartAt) {
			return invalidSchedule("planned end must follow planned start")
		}
		if _, err := requiredGeneratorDate(snapshot.PlannedDate, "planned_date"); err != nil {
			return err
		}
		if snapshot.PlannedStartAt.In(location).Format("2006-01-02") != snapshot.PlannedDate {
			return invalidSchedule("planned_date does not match time_block timezone")
		}
		if snapshot.AllDayEndDate != "" {
			return invalidSchedule("time_block query snapshot has all-day end date")
		}
	default:
		return invalidSchedule("unsupported timing_type")
	}
	return nil
}

func queryOccurrenceIsOverdue(snapshot QueryOccurrenceSnapshot, now time.Time) bool {
	return snapshot.DueAt != nil && snapshot.DueAt.Before(now) && !queryTerminalStatus(snapshot.Status)
}

func queryOccurrenceIsToday(snapshot QueryOccurrenceSnapshot, today string, location *time.Location) bool {
	if snapshot.MarkedForToday {
		return true
	}
	if snapshot.PlannedStartAt != nil {
		return snapshot.PlannedStartAt.In(location).Format("2006-01-02") == today
	}
	if snapshot.PlannedDate == "" {
		return false
	}
	if snapshot.AllDayEndDate == "" {
		return snapshot.PlannedDate == today
	}
	return snapshot.PlannedDate <= today && today < snapshot.AllDayEndDate
}

func queryTerminalStatus(status ExecutionStatus) bool {
	switch status {
	case ExecutionStatusDone, ExecutionStatusSkipped, ExecutionStatusCancelled:
		return true
	default:
		return false
	}
}

func cloneQueryTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
