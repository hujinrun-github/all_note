package legacytaskadapter

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/taskdomain"
)

var (
	ErrInvalidLegacyTask             = errors.New("invalid legacy task")
	ErrLegacyTaskUnrepresentable     = errors.New("v2 task cannot be represented by the legacy task contract")
	ErrLegacyTaskWorkspaceMismatch   = errors.New("legacy task workspace mismatch")
	ErrLegacyTaskBindingMismatch     = errors.New("legacy task projection binding mismatch")
	ErrLegacyTaskCommandRequired     = errors.New("legacy task update requires an explicit domain command")
	ErrLegacyTaskIDMapMissing        = errors.New("legacy task ID map missing")
	ErrLegacyTaskIDMapConflict       = errors.New("legacy task ID map conflict")
	ErrLegacyTaskDeleteStateConflict = errors.New("legacy task delete state conflict")
)

const (
	LegacyHorizonWeek = "week"
	LegacyHorizonLong = "long"

	LegacyScopeDaily   = "daily"
	LegacyScopeWeekly  = "weekly"
	LegacyScopeMonthly = "monthly"
	LegacyScopeYearly  = "yearly"

	LegacyExecutionSingle    = "single"
	LegacyExecutionRecurring = "recurring"
)

// LegacyTask is the flattened DTO understood by the old Web client. It keeps
// the three optimistic revisions separate: collapsing them to one number
// would let a compatibility write bypass either the schedule or occurrence
// compare-and-swap check.
type LegacyTask struct {
	WorkspaceID        string  `json:"workspace_id"`
	ID                 string  `json:"id"`
	TaskID             string  `json:"task_id"`
	OccurrenceID       string  `json:"occurrence_id"`
	Title              string  `json:"title"`
	Content            string  `json:"content"`
	Project            string  `json:"project"`
	ProjectID          string  `json:"project_id"`
	Due                *int64  `json:"due"`
	PlannedDate        *string `json:"planned_date"`
	Priority           int     `json:"priority"`
	Done               int     `json:"done"`
	Status             string  `json:"status"`
	Horizon            string  `json:"horizon"`
	Scope              string  `json:"scope"`
	SortOrder          float64 `json:"sort_order"`
	NoteID             string  `json:"note_id,omitempty"`
	RoadmapNodeID      string  `json:"roadmap_node_id,omitempty"`
	ExecutionType      string  `json:"execution_type"`
	OccurrenceDate     *string `json:"occurrence_date,omitempty"`
	OccurrenceStatus   *string `json:"occurrence_status,omitempty"`
	RecurrenceLabel    string  `json:"recurrence_label,omitempty"`
	TaskRevision       int64   `json:"task_revision"`
	ScheduleRevision   int64   `json:"schedule_revision"`
	OccurrenceRevision int64   `json:"occurrence_revision"`
}

// LegacyTaskProjectionSnapshot binds one selected occurrence to the stable
// task, project and immutable schedule version from which it was generated.
// A recurring task is flattened once per requested occurrence.
type LegacyTaskProjectionSnapshot struct {
	Project                taskdomain.ProjectSnapshot
	Task                   taskdomain.TaskRecord
	Schedule               taskdomain.ScheduleVersion
	ScheduleHeaderRevision int64
	Occurrence             taskdomain.QueryOccurrenceSnapshot
}

type LegacyRecurrenceConfig struct {
	StartDate string
	EndDate   *string
	Frequency string
	Interval  int
	Weekdays  []int
	MonthDays []int
	Timezone  string
	Enabled   *bool
}

type LegacyTaskCreate struct {
	Title         string
	Content       string
	ProjectID     string
	Due           *int64
	PlannedDate   *string
	Priority      int
	Scope         string
	Horizon       string
	SortOrder     float64
	NoteID        string
	RoadmapNodeID string
	ExecutionType string
	Recurrence    *LegacyRecurrenceConfig
}

// CreateLegacyTaskCommandPlan is a pure application command. The v2 service
// allocates IDs and persists the Task/Schedule/Occurrence aggregate in one
// fenced transaction; this adapter never writes either data model.
type CreateLegacyTaskCommandPlan struct {
	WorkspaceID             string
	ProjectID               string
	ExpectedProjectRevision int64
	Definition              taskdomain.TaskDefinition
	SortOrder               float64
	NoteID                  string
	RoadmapNodeID           string
	Schedule                taskdomain.Schedule
	DueAt                   *time.Time
}

type LegacyTaskPatch struct {
	Title         *string
	Content       *string
	ProjectID     *string
	Priority      *int
	SortOrder     *float64
	NoteID        *string
	RoadmapNodeID *string
	Done          *int
	Status        *string
	Horizon       *string
	Scope         *string
	Due           *int64
	PlannedDate   *string
	ExecutionType *string
	Recurrence    *LegacyRecurrenceConfig
	Enabled       *bool
	EndDate       *string
}

type PatchLegacyTaskInput struct {
	Current         LegacyTaskProjectionSnapshot
	Patch           LegacyTaskPatch
	TargetProject   taskdomain.ProjectSnapshot
	PersonalProject taskdomain.ProjectSnapshot
}

// PatchLegacyTaskCommandPlan only contains a Task definition after-image.
// Lifecycle, execution and schedule changes have dedicated commands and are
// rejected by PlanLegacyTaskPatch.
type PatchLegacyTaskCommandPlan struct {
	Task                       taskdomain.TaskRecord
	ExpectedTaskRevision       int64
	ExpectedScheduleRevision   int64
	ExpectedOccurrenceRevision int64
	ExpectedProjectRevision    int64
}

type LegacyTaskIDMap struct {
	WorkspaceID        string
	LegacyTaskID       string
	TaskID             string
	Revision           int64
	Tombstoned         bool
	TombstonedAt       time.Time
	TombstoneCommandID string
	TombstonedBy       string
}

type DeleteLegacyTaskInput struct {
	WorkspaceID  string
	LegacyTaskID string
	Task         taskdomain.TaskAggregate
	IDMap        LegacyTaskIDMap
	CommandID    string
	ActorID      string
	DeletedAt    time.Time
}

// DeleteLegacyTaskCommandPlan cancels the v2 aggregate and tombstones the
// durable legacy identity map. It never describes a physical delete or a
// legacy-table write.
type DeleteLegacyTaskCommandPlan struct {
	NoOp              bool
	ExpectedRevisions taskdomain.AggregateExpectedRevisions
	Task              taskdomain.TaskAggregate
	ExecutionLogs     []taskdomain.ExecutionLog
	IDMapBefore       LegacyTaskIDMap
	IDMapAfter        LegacyTaskIDMap
}

func ProjectLegacyTask(snapshot LegacyTaskProjectionSnapshot) (LegacyTask, error) {
	if err := validateLegacyTaskProjectionBinding(snapshot); err != nil {
		return LegacyTask{}, err
	}
	if !legacyLifecycleRepresentable(snapshot.Task.LifecycleStatus) || !legacyExecutionRepresentable(snapshot.Occurrence.Status) {
		return LegacyTask{}, ErrLegacyTaskUnrepresentable
	}

	horizon, err := projectLegacyHorizon(snapshot.Project.Project.Horizon)
	if err != nil {
		return LegacyTask{}, err
	}
	scope, err := projectLegacyScope(snapshot.Schedule.RecurrenceType, snapshot.Project.Project.Horizon)
	if err != nil {
		return LegacyTask{}, err
	}
	executionType := LegacyExecutionSingle
	if snapshot.Schedule.RecurrenceType != taskdomain.RecurrenceNone {
		executionType = LegacyExecutionRecurring
	}
	status := string(snapshot.Occurrence.Status)
	result := LegacyTask{
		WorkspaceID:        snapshot.Task.WorkspaceID,
		ID:                 snapshot.Task.ID,
		TaskID:             snapshot.Task.ID,
		OccurrenceID:       snapshot.Occurrence.OccurrenceID,
		Title:              snapshot.Task.Title,
		Content:            snapshot.Task.Description,
		Project:            snapshot.Project.Project.Name,
		ProjectID:          snapshot.Task.ProjectID,
		Priority:           snapshot.Task.Priority,
		Status:             status,
		Horizon:            horizon,
		Scope:              scope,
		SortOrder:          snapshot.Task.SortOrder,
		NoteID:             snapshot.Task.NoteID,
		RoadmapNodeID:      snapshot.Task.RoadmapNodeID,
		ExecutionType:      executionType,
		OccurrenceStatus:   &status,
		TaskRevision:       snapshot.Task.Revision,
		ScheduleRevision:   snapshot.ScheduleHeaderRevision,
		OccurrenceRevision: snapshot.Occurrence.Revision,
	}
	if snapshot.Occurrence.DueAt != nil {
		value := snapshot.Occurrence.DueAt.Unix()
		result.Due = &value
	}
	if snapshot.Occurrence.PlannedDate != "" {
		value := snapshot.Occurrence.PlannedDate
		result.PlannedDate = &value
	}
	if snapshot.Occurrence.Status == taskdomain.ExecutionStatusDone {
		result.Done = 1
	}
	if executionType == LegacyExecutionRecurring {
		value := snapshot.Occurrence.OccurrenceKey
		result.OccurrenceDate = &value
		result.RecurrenceLabel = string(snapshot.Schedule.RecurrenceType)
	}
	return result, nil
}

func PlanCreateLegacyTask(request LegacyTaskCreate, workspaceID string, selectedProject, personalProject taskdomain.ProjectSnapshot, defaultTimezone string) (CreateLegacyTaskCommandPlan, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	title := strings.TrimSpace(request.Title)
	if workspaceID == "" || title == "" || request.Priority < 0 || request.Priority > 3 {
		return CreateLegacyTaskCommandPlan{}, invalidLegacyTask("workspace, title and priority are invalid")
	}
	project, err := selectLegacyTaskProject(workspaceID, request.ProjectID, selectedProject, personalProject)
	if err != nil {
		return CreateLegacyTaskCommandPlan{}, err
	}
	expectedHorizon, err := projectLegacyHorizon(project.Project.Horizon)
	if err != nil {
		return CreateLegacyTaskCommandPlan{}, err
	}
	if request.Horizon != "" && request.Horizon != expectedHorizon {
		return CreateLegacyTaskCommandPlan{}, invalidLegacyTask("horizon does not match the selected project")
	}

	executionType := request.ExecutionType
	if executionType == "" {
		executionType = LegacyExecutionSingle
	}
	schedule, err := scheduleFromLegacyTaskCreate(request, executionType, defaultTimezone)
	if err != nil {
		return CreateLegacyTaskCommandPlan{}, err
	}
	expectedScope, err := projectLegacyScope(schedule.RecurrenceType, project.Project.Horizon)
	if err != nil {
		return CreateLegacyTaskCommandPlan{}, err
	}
	if request.Scope != "" && request.Scope != expectedScope {
		return CreateLegacyTaskCommandPlan{}, invalidLegacyTask("scope cannot be represented by the selected project and schedule")
	}

	plan := CreateLegacyTaskCommandPlan{
		WorkspaceID:             workspaceID,
		ProjectID:               project.Project.ID,
		ExpectedProjectRevision: project.Revision,
		Definition:              taskdomain.TaskDefinition{Title: title, Description: request.Content, Priority: request.Priority, LifecycleStatus: taskdomain.TaskLifecycleActive},
		SortOrder:               request.SortOrder,
		NoteID:                  request.NoteID,
		RoadmapNodeID:           request.RoadmapNodeID,
		Schedule:                schedule,
	}
	if request.Due != nil {
		if *request.Due <= 0 {
			return CreateLegacyTaskCommandPlan{}, invalidLegacyTask("due must be a positive Unix timestamp")
		}
		value := time.Unix(*request.Due, 0).UTC()
		plan.DueAt = &value
	}
	return plan, nil
}

func PlanLegacyTaskPatch(input PatchLegacyTaskInput) (PatchLegacyTaskCommandPlan, error) {
	if err := validateLegacyTaskProjectionBinding(input.Current); err != nil {
		return PatchLegacyTaskCommandPlan{}, err
	}
	if !legacyLifecycleRepresentable(input.Current.Task.LifecycleStatus) || !legacyExecutionRepresentable(input.Current.Occurrence.Status) {
		return PatchLegacyTaskCommandPlan{}, ErrLegacyTaskUnrepresentable
	}
	patch := input.Patch
	if patch.Done != nil || patch.Status != nil || patch.Horizon != nil || patch.Scope != nil || patch.Due != nil || patch.PlannedDate != nil ||
		patch.ExecutionType != nil || patch.Recurrence != nil || patch.Enabled != nil || patch.EndDate != nil {
		return PatchLegacyTaskCommandPlan{}, ErrLegacyTaskCommandRequired
	}

	next := input.Current.Task
	expectedProjectRevision := input.Current.Project.Revision
	if patch.Title != nil {
		next.Title = strings.TrimSpace(*patch.Title)
		if next.Title == "" {
			return PatchLegacyTaskCommandPlan{}, invalidLegacyTask("title is required")
		}
	}
	if patch.Content != nil {
		next.Description = *patch.Content
	}
	if patch.Priority != nil {
		if *patch.Priority < 0 || *patch.Priority > 3 {
			return PatchLegacyTaskCommandPlan{}, invalidLegacyTask("priority must be between 0 and 3")
		}
		next.Priority = *patch.Priority
	}
	if patch.SortOrder != nil {
		next.SortOrder = *patch.SortOrder
	}
	if patch.NoteID != nil {
		next.NoteID = *patch.NoteID
	}
	if patch.RoadmapNodeID != nil {
		next.RoadmapNodeID = *patch.RoadmapNodeID
	}
	if patch.ProjectID != nil {
		target, err := selectLegacyTaskProject(next.WorkspaceID, *patch.ProjectID, input.TargetProject, input.PersonalProject)
		if err != nil {
			return PatchLegacyTaskCommandPlan{}, err
		}
		next.ProjectID = target.Project.ID
		expectedProjectRevision = target.Revision
	}
	next.Revision++
	return PatchLegacyTaskCommandPlan{
		Task:                       next,
		ExpectedTaskRevision:       input.Current.Task.Revision,
		ExpectedScheduleRevision:   input.Current.ScheduleHeaderRevision,
		ExpectedOccurrenceRevision: input.Current.Occurrence.Revision,
		ExpectedProjectRevision:    expectedProjectRevision,
	}, nil
}

func PlanDeleteLegacyTask(input DeleteLegacyTaskInput) (DeleteLegacyTaskCommandPlan, error) {
	if strings.TrimSpace(input.WorkspaceID) == "" || strings.TrimSpace(input.LegacyTaskID) == "" || strings.TrimSpace(input.CommandID) == "" || strings.TrimSpace(input.ActorID) == "" || input.DeletedAt.IsZero() {
		return DeleteLegacyTaskCommandPlan{}, invalidLegacyTask("delete identity and audit fields are required")
	}
	if missingLegacyTaskIDMap(input.IDMap) {
		return DeleteLegacyTaskCommandPlan{}, ErrLegacyTaskIDMapMissing
	}
	if input.Task.WorkspaceID != input.WorkspaceID || input.IDMap.WorkspaceID != input.WorkspaceID {
		return DeleteLegacyTaskCommandPlan{}, ErrLegacyTaskWorkspaceMismatch
	}
	if input.IDMap.LegacyTaskID != input.LegacyTaskID || input.IDMap.TaskID != input.Task.TaskID {
		return DeleteLegacyTaskCommandPlan{}, ErrLegacyTaskIDMapConflict
	}
	if err := validateLegacyDeleteAggregate(input.Task); err != nil {
		return DeleteLegacyTaskCommandPlan{}, err
	}

	if input.Task.LifecycleStatus == taskdomain.TaskLifecycleCancelled {
		if !validLegacyTaskTombstone(input.IDMap) || !validCancelledLegacyAggregate(input.Task) {
			return DeleteLegacyTaskCommandPlan{}, ErrLegacyTaskDeleteStateConflict
		}
		return DeleteLegacyTaskCommandPlan{NoOp: true, IDMapBefore: input.IDMap, IDMapAfter: input.IDMap}, nil
	}
	if input.IDMap.Tombstoned {
		return DeleteLegacyTaskCommandPlan{}, ErrLegacyTaskDeleteStateConflict
	}

	expected := taskdomain.AggregateExpectedRevisions{Task: input.Task.Revision, Occurrences: make(map[string]int64)}
	transitions := make(map[string]taskdomain.ExecutionTransition)
	for _, occurrence := range input.Task.Occurrences {
		if legacyExecutionTerminal(occurrence.ExecutionStatus) {
			continue
		}
		expected.Occurrences[occurrence.ID] = occurrence.Revision
		transitions[occurrence.ID] = taskdomain.ExecutionTransition{LogID: input.CommandID + ":" + occurrence.ID, ActorID: input.ActorID, At: input.DeletedAt}
	}
	next, logs, err := taskdomain.CancelTaskAggregate(input.Task, expected, transitions)
	if err != nil {
		return DeleteLegacyTaskCommandPlan{}, err
	}
	nextMap := input.IDMap
	nextMap.Revision++
	nextMap.Tombstoned = true
	nextMap.TombstonedAt = input.DeletedAt
	nextMap.TombstoneCommandID = input.CommandID
	nextMap.TombstonedBy = input.ActorID
	return DeleteLegacyTaskCommandPlan{ExpectedRevisions: cloneLegacyExpectedRevisions(expected), Task: next, ExecutionLogs: logs, IDMapBefore: input.IDMap, IDMapAfter: nextMap}, nil
}

func validateLegacyTaskProjectionBinding(snapshot LegacyTaskProjectionSnapshot) error {
	workspaceID := snapshot.Task.WorkspaceID
	if workspaceID == "" || snapshot.Project.Project.WorkspaceID != workspaceID || snapshot.Schedule.WorkspaceID != workspaceID || snapshot.Occurrence.WorkspaceID != workspaceID {
		return ErrLegacyTaskWorkspaceMismatch
	}
	if snapshot.Project.Revision < 1 || snapshot.Task.Revision < 1 || snapshot.ScheduleHeaderRevision < 1 || snapshot.Schedule.ScheduleRevision < 1 || snapshot.Occurrence.Revision < 1 ||
		snapshot.Project.Project.ID != snapshot.Task.ProjectID || snapshot.Schedule.TaskID != snapshot.Task.ID || snapshot.Occurrence.TaskID != snapshot.Task.ID || snapshot.Occurrence.ProjectID != snapshot.Task.ProjectID ||
		snapshot.Occurrence.TaskRevision != snapshot.Task.Revision || snapshot.Occurrence.ScheduleRevision != snapshot.ScheduleHeaderRevision || snapshot.Occurrence.GeneratedScheduleRevision != snapshot.Schedule.ScheduleRevision ||
		snapshot.Occurrence.LifecycleStatus != snapshot.Task.LifecycleStatus || snapshot.Occurrence.Title != snapshot.Task.Title || snapshot.Occurrence.Description != snapshot.Task.Description || snapshot.Occurrence.Priority != snapshot.Task.Priority || snapshot.Occurrence.SortOrder != snapshot.Task.SortOrder {
		return ErrLegacyTaskBindingMismatch
	}
	recurring := snapshot.Schedule.RecurrenceType != taskdomain.RecurrenceNone
	if snapshot.Occurrence.Recurring != recurring || (!recurring && snapshot.Occurrence.OccurrenceKey != "once") {
		return ErrLegacyTaskBindingMismatch
	}
	return nil
}

func selectLegacyTaskProject(workspaceID, requestedProjectID string, selectedProject, personalProject taskdomain.ProjectSnapshot) (taskdomain.ProjectSnapshot, error) {
	requestedProjectID = strings.TrimSpace(requestedProjectID)
	project := selectedProject
	if requestedProjectID == "" {
		project = personalProject
		if project.Project.SystemRole != taskdomain.ProjectSystemRolePersonal {
			return taskdomain.ProjectSnapshot{}, invalidLegacyTask("missing project requires the workspace personal project")
		}
	} else if project.Project.ID != requestedProjectID {
		return taskdomain.ProjectSnapshot{}, invalidLegacyTask("selected project does not match project_id")
	}
	if project.Project.WorkspaceID != workspaceID {
		return taskdomain.ProjectSnapshot{}, ErrLegacyTaskWorkspaceMismatch
	}
	if strings.TrimSpace(project.Project.ID) == "" || project.Revision < 1 {
		return taskdomain.ProjectSnapshot{}, invalidLegacyTask("project snapshot is invalid")
	}
	return project, nil
}

func scheduleFromLegacyTaskCreate(request LegacyTaskCreate, executionType, defaultTimezone string) (taskdomain.Schedule, error) {
	defaultTimezone = strings.TrimSpace(defaultTimezone)
	if executionType == LegacyExecutionSingle {
		if request.Recurrence != nil {
			return taskdomain.Schedule{}, invalidLegacyTask("single task must not include recurrence")
		}
		input := taskdomain.ScheduleInput{RecurrenceType: taskdomain.RecurrenceNone, TimingType: taskdomain.TimingUnscheduled, Timezone: defaultTimezone}
		if request.PlannedDate != nil {
			input.TimingType = taskdomain.TimingDate
			input.StartsOn = strings.TrimSpace(*request.PlannedDate)
		}
		schedule, err := taskdomain.NormalizeSchedule(input)
		if err != nil {
			return taskdomain.Schedule{}, fmt.Errorf("%w: %v", ErrInvalidLegacyTask, err)
		}
		return schedule, nil
	}
	if executionType != LegacyExecutionRecurring || request.Recurrence == nil {
		return taskdomain.Schedule{}, invalidLegacyTask("execution_type and recurrence are inconsistent")
	}
	if request.Recurrence.Enabled != nil && !*request.Recurrence.Enabled {
		return taskdomain.Schedule{}, invalidLegacyTask("disabled recurrence needs the explicit pause command")
	}
	recurrenceType := taskdomain.RecurrenceType(strings.TrimSpace(request.Recurrence.Frequency))
	if recurrenceType != taskdomain.RecurrenceDaily && recurrenceType != taskdomain.RecurrenceWeekly && recurrenceType != taskdomain.RecurrenceMonthly {
		return taskdomain.Schedule{}, invalidLegacyTask("unsupported recurrence frequency")
	}
	timezone := strings.TrimSpace(request.Recurrence.Timezone)
	if timezone == "" {
		timezone = defaultTimezone
	}
	rule, err := json.Marshal(taskdomain.RecurrenceRule{Interval: request.Recurrence.Interval, Weekdays: append([]int(nil), request.Recurrence.Weekdays...), MonthDays: append([]int(nil), request.Recurrence.MonthDays...)})
	if err != nil {
		return taskdomain.Schedule{}, invalidLegacyTask("recurrence rule cannot be encoded")
	}
	endsOn := ""
	if request.Recurrence.EndDate != nil {
		endsOn = strings.TrimSpace(*request.Recurrence.EndDate)
	}
	if request.PlannedDate != nil && strings.TrimSpace(*request.PlannedDate) != strings.TrimSpace(request.Recurrence.StartDate) {
		return taskdomain.Schedule{}, invalidLegacyTask("planned_date must equal recurring start_date")
	}
	schedule, err := taskdomain.NormalizeSchedule(taskdomain.ScheduleInput{
		RecurrenceType: recurrenceType, TimingType: taskdomain.TimingDate, Timezone: timezone,
		StartsOn: strings.TrimSpace(request.Recurrence.StartDate), EndsOn: endsOn, Rule: rule,
	})
	if err != nil {
		return taskdomain.Schedule{}, fmt.Errorf("%w: %v", ErrInvalidLegacyTask, err)
	}
	return schedule, nil
}

func projectLegacyHorizon(horizon taskdomain.ProjectHorizon) (string, error) {
	switch horizon {
	case taskdomain.ProjectHorizonShort:
		return LegacyHorizonWeek, nil
	case taskdomain.ProjectHorizonLong:
		return LegacyHorizonLong, nil
	default:
		return "", ErrLegacyTaskUnrepresentable
	}
}

func projectLegacyScope(recurrence taskdomain.RecurrenceType, horizon taskdomain.ProjectHorizon) (string, error) {
	switch recurrence {
	case taskdomain.RecurrenceNone:
		if horizon == taskdomain.ProjectHorizonShort {
			return LegacyScopeDaily, nil
		}
		if horizon == taskdomain.ProjectHorizonLong {
			return LegacyScopeYearly, nil
		}
	case taskdomain.RecurrenceDaily:
		return LegacyScopeDaily, nil
	case taskdomain.RecurrenceWeekly:
		return LegacyScopeWeekly, nil
	case taskdomain.RecurrenceMonthly:
		return LegacyScopeMonthly, nil
	}
	return "", ErrLegacyTaskUnrepresentable
}

func legacyLifecycleRepresentable(status taskdomain.TaskLifecycleStatus) bool {
	return status == taskdomain.TaskLifecycleActive || status == taskdomain.TaskLifecycleCompleted
}

func legacyExecutionRepresentable(status taskdomain.ExecutionStatus) bool {
	switch status {
	case taskdomain.ExecutionStatusOpen, taskdomain.ExecutionStatusActive, taskdomain.ExecutionStatusBlocked, taskdomain.ExecutionStatusDone:
		return true
	default:
		return false
	}
}

func legacyExecutionTerminal(status taskdomain.ExecutionStatus) bool {
	return status == taskdomain.ExecutionStatusDone || status == taskdomain.ExecutionStatusSkipped || status == taskdomain.ExecutionStatusCancelled
}

func validateLegacyDeleteAggregate(aggregate taskdomain.TaskAggregate) error {
	if strings.TrimSpace(aggregate.TaskID) == "" || aggregate.Revision < 1 || len(aggregate.Occurrences) == 0 {
		return ErrLegacyTaskBindingMismatch
	}
	for _, occurrence := range aggregate.Occurrences {
		if occurrence.WorkspaceID != aggregate.WorkspaceID || occurrence.TaskID != aggregate.TaskID || strings.TrimSpace(occurrence.ID) == "" || occurrence.Revision < 1 || occurrence.Recurring != aggregate.Recurring {
			return ErrLegacyTaskBindingMismatch
		}
	}
	return nil
}

func validCancelledLegacyAggregate(aggregate taskdomain.TaskAggregate) bool {
	if aggregate.GenerationEnabled {
		return false
	}
	for _, occurrence := range aggregate.Occurrences {
		if !legacyExecutionTerminal(occurrence.ExecutionStatus) {
			return false
		}
	}
	return true
}

func missingLegacyTaskIDMap(idMap LegacyTaskIDMap) bool {
	return strings.TrimSpace(idMap.WorkspaceID) == "" || strings.TrimSpace(idMap.LegacyTaskID) == "" || strings.TrimSpace(idMap.TaskID) == "" || idMap.Revision < 1
}

func validLegacyTaskTombstone(idMap LegacyTaskIDMap) bool {
	return idMap.Tombstoned && !idMap.TombstonedAt.IsZero() && strings.TrimSpace(idMap.TombstoneCommandID) != "" && strings.TrimSpace(idMap.TombstonedBy) != ""
}

func cloneLegacyExpectedRevisions(value taskdomain.AggregateExpectedRevisions) taskdomain.AggregateExpectedRevisions {
	result := taskdomain.AggregateExpectedRevisions{Task: value.Task, Occurrences: make(map[string]int64, len(value.Occurrences))}
	for id, revision := range value.Occurrences {
		result.Occurrences[id] = revision
	}
	return result
}

func invalidLegacyTask(detail string) error {
	return fmt.Errorf("%w: %s", ErrInvalidLegacyTask, detail)
}
