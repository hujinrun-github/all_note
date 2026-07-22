package taskdomain

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"
)

var ErrInvalidTaskAggregateSnapshot = errors.New("invalid task aggregate persistence snapshot")

type TaskRecord struct {
	WorkspaceID     string
	ID              string
	ProjectID       string
	RoadmapNodeID   string
	NoteID          string
	Title           string
	Description     string
	LifecycleStatus TaskLifecycleStatus
	Priority        int
	SortOrder       float64
	Revision        int64
}

type ScheduleHeader struct {
	WorkspaceID             string
	TaskID                  string
	Revision                int64
	CurrentScheduleRevision int64
}

type ScheduleVersion struct {
	WorkspaceID      string
	TaskID           string
	ScheduleRevision int64
	EffectiveFrom    string
	EffectiveTo      string
	RecurrenceType   RecurrenceType
	TimingType       TimingType
	Timezone         string
	StartsOn         string
	EndsOn           string
	RecurrenceRule   string
	LocalStartTime   string
	DurationMinutes  int
}

type OccurrenceRecord struct {
	WorkspaceID               string
	ID                        string
	TaskID                    string
	OccurrenceKey             string
	PlannedDate               string
	PlannedStartAt            *time.Time
	PlannedEndAt              *time.Time
	DueAt                     *time.Time
	ExecutionStatus           ExecutionStatus
	NoteID                    string
	AllDayEndDate             string
	Revision                  int64
	GeneratedScheduleRevision int64
}

type TaskAggregateSnapshot struct {
	Task        TaskRecord
	Schedule    ScheduleHeader
	Versions    []ScheduleVersion
	Occurrences []OccurrenceRecord
}

type ScheduleVersionInstall struct {
	WorkspaceID              string
	TaskID                   string
	ExpectedScheduleRevision int64
	Version                  ScheduleVersion
}

func ValidateTaskAggregateSnapshot(snapshot TaskAggregateSnapshot) error {
	workspaceID := strings.TrimSpace(snapshot.Task.WorkspaceID)
	taskID := strings.TrimSpace(snapshot.Task.ID)
	if workspaceID == "" || taskID == "" || strings.TrimSpace(snapshot.Task.ProjectID) == "" || strings.TrimSpace(snapshot.Task.Title) == "" ||
		snapshot.Task.Revision != 1 || snapshot.Task.Priority < 0 || snapshot.Task.Priority > 3 || !knownTaskLifecycleStatus(snapshot.Task.LifecycleStatus) {
		return ErrInvalidTaskAggregateSnapshot
	}
	if snapshot.Schedule.WorkspaceID != workspaceID || snapshot.Schedule.TaskID != taskID || snapshot.Schedule.Revision != 1 || snapshot.Schedule.CurrentScheduleRevision < 1 || len(snapshot.Versions) == 0 {
		return ErrInvalidTaskAggregateSnapshot
	}

	versions := make(map[int64]ScheduleVersion, len(snapshot.Versions))
	openVersions := 0
	for _, version := range snapshot.Versions {
		if version.WorkspaceID != workspaceID || version.TaskID != taskID || version.ScheduleRevision < 1 {
			return ErrInvalidTaskAggregateSnapshot
		}
		if _, duplicate := versions[version.ScheduleRevision]; duplicate {
			return ErrInvalidTaskAggregateSnapshot
		}
		if err := validatePersistenceScheduleVersion(version); err != nil {
			return err
		}
		if version.EffectiveTo == "" {
			openVersions++
		}
		versions[version.ScheduleRevision] = version
	}
	current, currentExists := versions[snapshot.Schedule.CurrentScheduleRevision]
	if !currentExists || current.EffectiveTo != "" || openVersions != 1 {
		return ErrInvalidTaskAggregateSnapshot
	}

	for _, occurrence := range snapshot.Occurrences {
		if occurrence.WorkspaceID != workspaceID || occurrence.TaskID != taskID || strings.TrimSpace(occurrence.ID) == "" ||
			strings.TrimSpace(occurrence.OccurrenceKey) == "" || occurrence.Revision != 1 || occurrence.ExecutionStatus != ExecutionStatusOpen {
			return ErrInvalidTaskAggregateSnapshot
		}
		version, exists := versions[occurrence.GeneratedScheduleRevision]
		if !exists || validatePersistenceOccurrenceTiming(occurrence, version) != nil {
			return ErrInvalidTaskAggregateSnapshot
		}
	}
	if current.RecurrenceType == RecurrenceNone {
		if len(snapshot.Occurrences) != 1 || snapshot.Occurrences[0].OccurrenceKey != "once" {
			return ErrInvalidTaskAggregateSnapshot
		}
	}
	return nil
}

func ValidateTaskAggregateWrite(write TaskAggregateWrite) error {
	aggregate := write.Aggregate
	if strings.TrimSpace(aggregate.WorkspaceID) == "" || strings.TrimSpace(aggregate.TaskID) == "" ||
		write.ExpectedRevisions.Task < 1 || aggregate.Revision != write.ExpectedRevisions.Task+1 ||
		!knownTaskLifecycleStatus(aggregate.LifecycleStatus) ||
		len(write.ExecutionLogs) != len(write.ExpectedRevisions.Occurrences) {
		return ErrInvalidTaskAggregateSnapshot
	}
	if write.Task != nil {
		task := *write.Task
		if task.WorkspaceID != aggregate.WorkspaceID || task.ID != aggregate.TaskID || task.Revision != aggregate.Revision ||
			task.LifecycleStatus != aggregate.LifecycleStatus || strings.TrimSpace(task.ProjectID) == "" || strings.TrimSpace(task.Title) == "" ||
			task.Priority < 0 || task.Priority > 3 || math.IsNaN(task.SortOrder) || math.IsInf(task.SortOrder, 0) {
			return ErrInvalidTaskAggregateSnapshot
		}
	}
	targets := make(map[string]Occurrence, len(aggregate.Occurrences))
	for _, occurrence := range aggregate.Occurrences {
		if occurrence.WorkspaceID != aggregate.WorkspaceID || occurrence.TaskID != aggregate.TaskID || occurrence.ID == "" {
			return ErrInvalidTaskAggregateSnapshot
		}
		targets[occurrence.ID] = occurrence
	}
	logs := make(map[string]ExecutionLog, len(write.ExecutionLogs))
	for _, log := range write.ExecutionLogs {
		if log.IsZero() || log.WorkspaceID() != aggregate.WorkspaceID || log.ID() == "" {
			return ErrInvalidTaskAggregateSnapshot
		}
		if _, duplicate := logs[log.OccurrenceID()]; duplicate {
			return ErrInvalidTaskAggregateSnapshot
		}
		logs[log.OccurrenceID()] = log
	}
	for occurrenceID, expectedRevision := range write.ExpectedRevisions.Occurrences {
		target, exists := targets[occurrenceID]
		log, hasLog := logs[occurrenceID]
		if expectedRevision < 1 || !exists || !hasLog || target.Revision != expectedRevision+1 ||
			validateOccurrenceSnapshot(target) != nil || log.OccurrenceRevision() != target.Revision || log.ToStatus() != target.ExecutionStatus {
			return ErrInvalidTaskAggregateSnapshot
		}
	}
	return nil
}

func ValidateScheduleVersionInstall(install ScheduleVersionInstall) error {
	if strings.TrimSpace(install.WorkspaceID) == "" || strings.TrimSpace(install.TaskID) == "" || install.ExpectedScheduleRevision < 1 ||
		install.Version.WorkspaceID != install.WorkspaceID || install.Version.TaskID != install.TaskID ||
		install.Version.ScheduleRevision < 1 || install.Version.EffectiveFrom == "" || install.Version.EffectiveTo != "" {
		return ErrInvalidTaskAggregateSnapshot
	}
	return validatePersistenceScheduleVersion(install.Version)
}

func validatePersistenceScheduleVersion(version ScheduleVersion) error {
	if version.EffectiveFrom != "" {
		from, err := parseLocalDate(version.EffectiveFrom)
		if err != nil {
			return ErrInvalidTaskAggregateSnapshot
		}
		if version.EffectiveTo != "" {
			to, err := parseLocalDate(version.EffectiveTo)
			if err != nil || !to.After(from) {
				return ErrInvalidTaskAggregateSnapshot
			}
		}
	} else if version.EffectiveTo != "" {
		return ErrInvalidTaskAggregateSnapshot
	}
	_, err := NormalizeSchedule(ScheduleInput{
		RecurrenceType: version.RecurrenceType, TimingType: version.TimingType, Timezone: version.Timezone,
		StartsOn: version.StartsOn, EndsOn: version.EndsOn, Rule: json.RawMessage(version.RecurrenceRule),
		LocalStartTime: version.LocalStartTime, DurationMinutes: version.DurationMinutes,
	})
	if err != nil || (version.RecurrenceType != RecurrenceNone && version.EffectiveFrom == "") {
		return ErrInvalidTaskAggregateSnapshot
	}
	return nil
}

func validatePersistenceOccurrenceTiming(occurrence OccurrenceRecord, version ScheduleVersion) error {
	switch version.TimingType {
	case TimingUnscheduled:
		if occurrence.PlannedDate != "" || occurrence.PlannedStartAt != nil || occurrence.PlannedEndAt != nil || occurrence.AllDayEndDate != "" {
			return ErrInvalidTaskAggregateSnapshot
		}
	case TimingDate:
		planned, err := parseLocalDate(occurrence.PlannedDate)
		if err != nil || occurrence.PlannedStartAt != nil || occurrence.PlannedEndAt != nil {
			return ErrInvalidTaskAggregateSnapshot
		}
		if occurrence.AllDayEndDate != "" {
			end, err := parseLocalDate(occurrence.AllDayEndDate)
			if err != nil || !end.After(planned) {
				return ErrInvalidTaskAggregateSnapshot
			}
		}
	case TimingTimeBlock:
		if _, err := parseLocalDate(occurrence.PlannedDate); err != nil || occurrence.PlannedStartAt == nil || occurrence.PlannedEndAt == nil ||
			!occurrence.PlannedEndAt.After(*occurrence.PlannedStartAt) || occurrence.AllDayEndDate != "" {
			return ErrInvalidTaskAggregateSnapshot
		}
	default:
		return ErrInvalidTaskAggregateSnapshot
	}
	return nil
}

func knownTaskLifecycleStatus(status TaskLifecycleStatus) bool {
	switch status {
	case TaskLifecycleDraft, TaskLifecycleActive, TaskLifecyclePaused, TaskLifecycleCompleted, TaskLifecycleCancelled, TaskLifecycleArchived:
		return true
	default:
		return false
	}
}
