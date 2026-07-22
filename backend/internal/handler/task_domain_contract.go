package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/taskdomain"
)

// ScheduleV2Input is the transport representation of a schedule submitted as
// part of task creation. It deliberately keeps local date/time values separate
// from timezone resolution performed by the domain layer.
type ScheduleV2Input struct {
	RecurrenceType  taskdomain.RecurrenceType `json:"recurrence_type"`
	TimingType      taskdomain.TimingType     `json:"timing_type"`
	Timezone        string                    `json:"timezone"`
	StartsOn        string                    `json:"starts_on,omitempty"`
	EndsOn          string                    `json:"ends_on,omitempty"`
	LocalStartTime  string                    `json:"local_start_time,omitempty"`
	DurationMinutes int                       `json:"duration_minutes,omitempty"`
	Rule            json.RawMessage           `json:"rule,omitempty"`
}

// CreateTaskV2Request submits the stable definition and schedule together so
// the application service can create the aggregate atomically.
type CreateTaskV2Request struct {
	ProjectID       string          `json:"project_id"`
	RoadmapNodeID   *string         `json:"roadmap_node_id,omitempty"`
	TaskNoteID      *string         `json:"task_note_id,omitempty"`
	Title           string          `json:"title"`
	Description     string          `json:"description,omitempty"`
	Priority        int             `json:"priority"`
	SortOrder       float64         `json:"sort_order,omitempty"`
	Schedule        ScheduleV2Input `json:"schedule"`
	AllDayEndDate   string          `json:"all_day_end_date,omitempty"`
	DueAt           *time.Time      `json:"due_at,omitempty"`
	SelectedOffsets map[string]int  `json:"selected_offsets,omitempty"`
}

// CreateProjectV2Request mirrors the frontend create contract. Terminal
// states are reached only through explicit project commands.
type CreateProjectV2Request struct {
	Name    string                    `json:"name"`
	Kind    taskdomain.ProjectKind    `json:"kind"`
	Horizon taskdomain.ProjectHorizon `json:"horizon"`
	Status  taskdomain.ProjectStatus  `json:"status"`
}

// UpdateProjectV2Request uses pointers to distinguish omitted fields from
// explicit zero values. Identity and system-role fields are intentionally not
// represented and are rejected by DecodeTaskDomainRequest.
type UpdateProjectV2Request struct {
	Name                    *string                    `json:"name,omitempty"`
	Kind                    *taskdomain.ProjectKind    `json:"kind,omitempty"`
	Horizon                 *taskdomain.ProjectHorizon `json:"horizon,omitempty"`
	Status                  *taskdomain.ProjectStatus  `json:"status,omitempty"`
	ExpectedProjectRevision int64                      `json:"expected_project_revision"`
}

type ProjectCommandV2Request struct {
	ExpectedProjectRevision int64 `json:"expected_project_revision"`
}

type DeleteProjectV2Request struct {
	ExpectedProjectRevision int64 `json:"expected_project_revision"`
}

type ProjectCommandV2Response struct {
	ProjectID       string                   `json:"project_id"`
	ProjectRevision int64                    `json:"project_revision"`
	Status          taskdomain.ProjectStatus `json:"status,omitempty"`
	Deleted         bool                     `json:"deleted,omitempty"`
}

// OccurrenceCommandV2Request carries all independently evolving revisions.
// Block metadata is present only for the explicit block command.
type OccurrenceCommandV2Request struct {
	ExpectedTaskRevision        int64            `json:"expected_task_revision"`
	ExpectedScheduleRevision    int64            `json:"expected_schedule_revision"`
	ExpectedOccurrenceRevisions map[string]int64 `json:"expected_occurrence_revisions"`
	BlockedReason               string           `json:"blocked_reason,omitempty"`
	NextAction                  string           `json:"next_action,omitempty"`
}

type OccurrenceTimingV2Input struct {
	TimingType      taskdomain.TimingType `json:"timing_type"`
	Timezone        string                `json:"timezone"`
	PlannedDate     string                `json:"planned_date,omitempty"`
	AllDayEndDate   string                `json:"all_day_end_date,omitempty"`
	LocalStartTime  string                `json:"local_start_time,omitempty"`
	DurationMinutes int                   `json:"duration_minutes,omitempty"`
}

// RescheduleOccurrenceV2Request changes only one materialized occurrence.
type RescheduleOccurrenceV2Request struct {
	ExpectedTaskRevision       int64                   `json:"expected_task_revision"`
	ExpectedScheduleRevision   int64                   `json:"expected_schedule_revision"`
	ExpectedOccurrenceRevision int64                   `json:"expected_occurrence_revision"`
	Timing                     OccurrenceTimingV2Input `json:"timing"`
	SelectedOffsets            map[string]int          `json:"selected_offsets,omitempty"`
}

// RescheduleThisAndFutureV2Request installs a new immutable schedule version
// from EffectiveFrom onward.
type RescheduleThisAndFutureV2Request struct {
	ExpectedTaskRevision     int64           `json:"expected_task_revision"`
	ExpectedScheduleRevision int64           `json:"expected_schedule_revision"`
	EffectiveFrom            string          `json:"effective_from"`
	GenerateThroughExclusive string          `json:"generate_through_exclusive"`
	Schedule                 ScheduleV2Input `json:"schedule"`
	SelectedOffsets          map[string]int  `json:"selected_offsets,omitempty"`
}

var ErrInvalidTaskDomainRequest = errors.New("invalid task-domain request")

type taskDomainRequestValidator interface {
	validateTaskDomainRequest() error
}

// DecodeTaskDomainRequest is the single JSON entry point for v2 task-domain
// requests. It rejects unknown nested fields and trailing JSON before running
// request-specific semantic validation.
func DecodeTaskDomainRequest(reader io.Reader, destination any) error {
	if reader == nil || destination == nil {
		return invalidTaskDomainRequest("request body is required")
	}
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return invalidTaskDomainRequest(err.Error())
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return invalidTaskDomainRequest("request body must contain one JSON object")
		}
		return invalidTaskDomainRequest(err.Error())
	}
	if validator, ok := destination.(taskDomainRequestValidator); ok {
		if err := validator.validateTaskDomainRequest(); err != nil {
			return invalidTaskDomainRequest(err.Error())
		}
	}
	return nil
}

func (request *CreateProjectV2Request) validateTaskDomainRequest() error {
	if request == nil || strings.TrimSpace(request.Name) == "" || !validProjectKind(request.Kind) ||
		!validProjectHorizon(request.Horizon) || !validProjectCreateStatus(request.Status) {
		return errors.New("invalid project creation")
	}
	return nil
}

func (request *UpdateProjectV2Request) validateTaskDomainRequest() error {
	if request == nil || request.ExpectedProjectRevision < 1 {
		return errors.New("expected_project_revision must be positive")
	}
	if request.Name == nil && request.Kind == nil && request.Horizon == nil && request.Status == nil {
		return errors.New("project update has no mutable fields")
	}
	if request.Name != nil && strings.TrimSpace(*request.Name) == "" {
		return errors.New("name must not be blank")
	}
	if request.Kind != nil && !validProjectKind(*request.Kind) {
		return errors.New("invalid project kind")
	}
	if request.Horizon != nil && !validProjectHorizon(*request.Horizon) {
		return errors.New("invalid project horizon")
	}
	if request.Status != nil && !validProjectMutableStatus(*request.Status) {
		return errors.New("project status requires an explicit command")
	}
	return nil
}

func (request *ProjectCommandV2Request) validateTaskDomainRequest() error {
	if request == nil || request.ExpectedProjectRevision < 1 {
		return errors.New("expected_project_revision must be positive")
	}
	return nil
}

func (request *DeleteProjectV2Request) validateTaskDomainRequest() error {
	if request == nil || request.ExpectedProjectRevision < 1 {
		return errors.New("expected_project_revision must be positive")
	}
	return nil
}

func (request *CreateTaskV2Request) validateTaskDomainRequest() error {
	if request == nil || strings.TrimSpace(request.ProjectID) == "" || strings.TrimSpace(request.Title) == "" ||
		request.Priority < 0 || request.Priority > 3 || invalidOptionalIdentity(request.RoadmapNodeID) ||
		invalidOptionalIdentity(request.TaskNoteID) || validateSelectedOffsets(request.SelectedOffsets) != nil {
		return errors.New("invalid task creation")
	}
	schedule, err := request.Schedule.domainInput()
	if err != nil {
		return err
	}
	normalized, err := taskdomain.NormalizeSchedule(schedule)
	if err != nil {
		return err
	}
	if request.AllDayEndDate != "" {
		if normalized.RecurrenceType != taskdomain.RecurrenceNone || normalized.TimingType != taskdomain.TimingDate {
			return errors.New("all_day_end_date is only valid for one date occurrence")
		}
		if _, err := taskdomain.ResolveAllDayRangeUTC(normalized.StartsOn, request.AllDayEndDate, normalized.Timezone); err != nil {
			return err
		}
	}
	return nil
}

func (request *TaskAggregateCommandRequest) validateTaskDomainRequest() error {
	if request == nil || request.ExpectedTaskRevision < 1 {
		return errors.New("expected_task_revision must be positive")
	}
	if request.ExpectedScheduleRevision != nil && *request.ExpectedScheduleRevision < 1 {
		return errors.New("expected_schedule_revision must be positive")
	}
	return validateRevisionMap(request.ExpectedOccurrenceRevisions)
}

func (request *OccurrenceCommandV2Request) validateTaskDomainRequest() error {
	if request == nil || request.ExpectedTaskRevision < 1 || request.ExpectedScheduleRevision < 1 || len(request.ExpectedOccurrenceRevisions) == 0 {
		return errors.New("occurrence command revisions are required")
	}
	if err := validateRevisionMap(request.ExpectedOccurrenceRevisions); err != nil {
		return err
	}
	reasonPresent := strings.TrimSpace(request.BlockedReason) != ""
	nextPresent := strings.TrimSpace(request.NextAction) != ""
	if reasonPresent != nextPresent {
		return errors.New("blocked_reason and next_action must be provided together")
	}
	return nil
}

func (request *RescheduleOccurrenceV2Request) validateTaskDomainRequest() error {
	if request == nil || request.ExpectedTaskRevision < 1 || request.ExpectedScheduleRevision < 1 || request.ExpectedOccurrenceRevision < 1 {
		return errors.New("reschedule revisions must be positive")
	}
	if err := validateSelectedOffsets(request.SelectedOffsets); err != nil {
		return err
	}
	return request.Timing.validate()
}

func (request *RescheduleThisAndFutureV2Request) validateTaskDomainRequest() error {
	if request == nil || request.ExpectedTaskRevision < 1 || request.ExpectedScheduleRevision < 1 {
		return errors.New("reschedule revisions must be positive")
	}
	if !validTaskDomainDate(request.EffectiveFrom) || !validTaskDomainDate(request.GenerateThroughExclusive) || request.GenerateThroughExclusive <= request.EffectiveFrom {
		return errors.New("invalid schedule generation range")
	}
	if err := validateSelectedOffsets(request.SelectedOffsets); err != nil {
		return err
	}
	schedule, err := request.Schedule.domainInput()
	if err != nil {
		return err
	}
	_, err = taskdomain.NormalizeSchedule(schedule)
	return err
}

func (input ScheduleV2Input) domainInput() (taskdomain.ScheduleInput, error) {
	if !validRecurrenceType(input.RecurrenceType) || !validTimingType(input.TimingType) {
		return taskdomain.ScheduleInput{}, errors.New("unsupported schedule type")
	}
	return taskdomain.ScheduleInput{
		RecurrenceType: input.RecurrenceType, TimingType: input.TimingType, Timezone: input.Timezone,
		StartsOn: input.StartsOn, EndsOn: input.EndsOn, LocalStartTime: input.LocalStartTime,
		DurationMinutes: input.DurationMinutes, Rule: append(json.RawMessage(nil), input.Rule...),
	}, nil
}

func (input OccurrenceTimingV2Input) validate() error {
	if !validTimingType(input.TimingType) {
		return errors.New("unsupported timing_type")
	}
	schedule := taskdomain.ScheduleInput{
		RecurrenceType: taskdomain.RecurrenceNone, TimingType: input.TimingType, Timezone: input.Timezone,
		StartsOn: input.PlannedDate, LocalStartTime: input.LocalStartTime, DurationMinutes: input.DurationMinutes,
	}
	normalized, err := taskdomain.NormalizeSchedule(schedule)
	if err != nil {
		return err
	}
	if input.AllDayEndDate != "" {
		if normalized.TimingType != taskdomain.TimingDate {
			return errors.New("all_day_end_date requires date timing")
		}
		_, err = taskdomain.ResolveAllDayRangeUTC(normalized.StartsOn, input.AllDayEndDate, normalized.Timezone)
	}
	return err
}

func validProjectKind(kind taskdomain.ProjectKind) bool {
	return kind == taskdomain.ProjectKindStandard || kind == taskdomain.ProjectKindLearning
}

func validProjectHorizon(horizon taskdomain.ProjectHorizon) bool {
	return horizon == taskdomain.ProjectHorizonShort || horizon == taskdomain.ProjectHorizonLong
}

func validProjectCreateStatus(status taskdomain.ProjectStatus) bool {
	return status == taskdomain.ProjectStatusPlanning || status == taskdomain.ProjectStatusActive
}

func validProjectMutableStatus(status taskdomain.ProjectStatus) bool {
	return validProjectCreateStatus(status) || status == taskdomain.ProjectStatusPaused
}

func validRecurrenceType(recurrenceType taskdomain.RecurrenceType) bool {
	switch recurrenceType {
	case taskdomain.RecurrenceNone, taskdomain.RecurrenceDaily, taskdomain.RecurrenceWeekly, taskdomain.RecurrenceMonthly:
		return true
	default:
		return false
	}
}

func validTimingType(timingType taskdomain.TimingType) bool {
	switch timingType {
	case taskdomain.TimingUnscheduled, taskdomain.TimingDate, taskdomain.TimingTimeBlock:
		return true
	default:
		return false
	}
}

func validateRevisionMap(revisions map[string]int64) error {
	for identity, revision := range revisions {
		if strings.TrimSpace(identity) == "" || revision < 1 {
			return errors.New("occurrence revisions must use non-empty identities and positive revisions")
		}
	}
	return nil
}

func validateSelectedOffsets(selected map[string]int) error {
	for identity, offset := range selected {
		if strings.TrimSpace(identity) == "" || offset < -24*60*60 || offset > 24*60*60 {
			return errors.New("invalid selected offset")
		}
	}
	return nil
}

func invalidOptionalIdentity(identity *string) bool {
	return identity != nil && strings.TrimSpace(*identity) == ""
}

func validTaskDomainDate(value string) bool {
	_, err := time.Parse("2006-01-02", value)
	return err == nil
}

func invalidTaskDomainRequest(reason string) error {
	return fmt.Errorf("%w: %s", ErrInvalidTaskDomainRequest, reason)
}

// ProjectV2DTO always carries the project's independent optimistic revision.
type ProjectV2DTO struct {
	ID         string                       `json:"id"`
	Name       string                       `json:"name"`
	Kind       taskdomain.ProjectKind       `json:"kind"`
	Horizon    taskdomain.ProjectHorizon    `json:"horizon"`
	Status     taskdomain.ProjectStatus     `json:"status"`
	SystemRole taskdomain.ProjectSystemRole `json:"system_role,omitempty"`
	Revision   int64                        `json:"revision"`
}

// TaskV2DTO uses task_note_id for the stable definition-level note. It must
// never be conflated with an occurrence-specific note.
type TaskV2DTO struct {
	ID               string                         `json:"id"`
	ProjectID        string                         `json:"project_id"`
	RoadmapNodeID    *string                        `json:"roadmap_node_id,omitempty"`
	TaskNoteID       *string                        `json:"task_note_id,omitempty"`
	Title            string                         `json:"title"`
	Description      string                         `json:"description,omitempty"`
	Priority         int                            `json:"priority"`
	SortOrder        float64                        `json:"sort_order"`
	LifecycleStatus  taskdomain.TaskLifecycleStatus `json:"lifecycle_status"`
	Revision         int64                          `json:"revision"`
	ScheduleRevision int64                          `json:"schedule_revision"`
}

// OccurrenceV2DTO exposes both note relationships with explicit names and
// keeps entity revision independent from the immutable schedule revision that
// generated the occurrence.
type OccurrenceV2DTO struct {
	ID                        string                     `json:"id"`
	TaskID                    string                     `json:"task_id"`
	OccurrenceKey             string                     `json:"occurrence_key"`
	TaskNoteID                *string                    `json:"task_note_id,omitempty"`
	OccurrenceNoteID          *string                    `json:"occurrence_note_id,omitempty"`
	ExecutionStatus           taskdomain.ExecutionStatus `json:"execution_status"`
	Revision                  int64                      `json:"revision"`
	GeneratedScheduleRevision int64                      `json:"generated_schedule_revision"`
	PlannedDate               string                     `json:"planned_date,omitempty"`
	AllDayEndDate             string                     `json:"all_day_end_date,omitempty"`
	PlannedStartAt            *time.Time                 `json:"planned_start_at,omitempty"`
	PlannedEndAt              *time.Time                 `json:"planned_end_at,omitempty"`
	DueAt                     *time.Time                 `json:"due_at,omitempty"`
	BlockedReason             string                     `json:"blocked_reason,omitempty"`
	NextAction                string                     `json:"next_action,omitempty"`
	Location                  string                     `json:"location,omitempty"`
	CalendarKind              string                     `json:"calendar_kind,omitempty"`
	CalendarNotes             string                     `json:"calendar_notes,omitempty"`
}

// TaskAggregateCommandRequest carries every optimistic lock that can
// participate in one aggregate command.
type TaskAggregateCommandRequest struct {
	ExpectedTaskRevision        int64            `json:"expected_task_revision"`
	ExpectedScheduleRevision    *int64           `json:"expected_schedule_revision,omitempty"`
	ExpectedOccurrenceRevisions map[string]int64 `json:"expected_occurrence_revisions,omitempty"`
}

// TaskAggregateCommandResponse returns the new revision for every entity the
// successful command modified.
type TaskAggregateCommandResponse struct {
	TaskRevision        int64            `json:"task_revision"`
	ScheduleRevision    *int64           `json:"schedule_revision,omitempty"`
	OccurrenceRevisions map[string]int64 `json:"occurrence_revisions,omitempty"`
}

// CalendarEntryV2DTO is a read projection over an occurrence rather than a
// second event write model.
type CalendarEntryV2DTO struct {
	ProjectID                 string                     `json:"project_id"`
	ProjectRevision           int64                      `json:"project_revision"`
	TaskID                    string                     `json:"task_id"`
	TaskRevision              int64                      `json:"task_revision"`
	ScheduleRevision          int64                      `json:"schedule_revision"`
	TaskTitle                 string                     `json:"task_title"`
	TaskNoteID                *string                    `json:"task_note_id,omitempty"`
	OccurrenceID              string                     `json:"occurrence_id"`
	OccurrenceKey             string                     `json:"occurrence_key"`
	OccurrenceRevision        int64                      `json:"occurrence_revision"`
	GeneratedScheduleRevision int64                      `json:"generated_schedule_revision"`
	OccurrenceNoteID          *string                    `json:"occurrence_note_id,omitempty"`
	ExecutionStatus           taskdomain.ExecutionStatus `json:"execution_status"`
	TimingType                taskdomain.TimingType      `json:"timing_type"`
	Timezone                  string                     `json:"timezone"`
	Recurring                 bool                       `json:"recurring"`
	PlannedDate               string                     `json:"planned_date,omitempty"`
	AllDayEndDate             string                     `json:"all_day_end_date,omitempty"`
	PlannedStartAt            *time.Time                 `json:"planned_start_at,omitempty"`
	PlannedEndAt              *time.Time                 `json:"planned_end_at,omitempty"`
	DueAt                     *time.Time                 `json:"due_at,omitempty"`
	Location                  string                     `json:"location,omitempty"`
	CalendarKind              string                     `json:"calendar_kind,omitempty"`
	CalendarNotes             string                     `json:"calendar_notes,omitempty"`
}

type TaskDomainOffsetCandidateDTO struct {
	OffsetSeconds int       `json:"offset_seconds"`
	UTC           time.Time `json:"utc"`
}

// TaskDomainCurrentRevisions is the server snapshot returned with a revision
// conflict. Its JSON shape is shared verbatim with the frontend
// TaskDomainCurrentRevisions contract.
type TaskDomainCurrentRevisions struct {
	ProjectRevision     *int64           `json:"project_revision,omitempty"`
	TaskRevision        *int64           `json:"task_revision,omitempty"`
	ScheduleRevision    *int64           `json:"schedule_revision,omitempty"`
	OccurrenceRevisions map[string]int64 `json:"occurrence_revisions,omitempty"`
}

type TaskDomainErrorDetails struct {
	OffsetCandidates []TaskDomainOffsetCandidateDTO `json:"offset_candidates,omitempty"`
	CurrentRevisions *TaskDomainCurrentRevisions    `json:"current_revisions,omitempty"`
}

type TaskDomainAPIError struct {
	Code      string                  `json:"code"`
	Message   string                  `json:"message"`
	Retryable bool                    `json:"retryable"`
	Details   *TaskDomainErrorDetails `json:"details,omitempty"`
}

type TaskDomainErrorResponse struct {
	Error TaskDomainAPIError `json:"error"`
}

// TaskDomainHTTPError is a pure mapping result. A later route implementation
// may serialize Response with the returned Status; this contract itself does
// not register or call any route.
type TaskDomainHTTPError struct {
	Status   int
	Response TaskDomainErrorResponse
}

// AmbiguousLocalTimeContractError attaches the domain resolver's candidates
// without weakening the stable ambiguous_local_time error identity.
type AmbiguousLocalTimeContractError struct {
	candidates []TaskDomainOffsetCandidateDTO
}

// RevisionConflictContractError enriches the stable domain conflict with the
// current server revisions needed by clients to refresh or compare edits.
type RevisionConflictContractError struct {
	CurrentRevisions TaskDomainCurrentRevisions
}

func (e *RevisionConflictContractError) Error() string {
	return "revision_conflict"
}

func (e *RevisionConflictContractError) Unwrap() error {
	return taskdomain.ErrAggregateRevisionConflict
}

func NewAmbiguousLocalTimeContractError(candidates []taskdomain.OffsetCandidate) error {
	wireCandidates := make([]TaskDomainOffsetCandidateDTO, len(candidates))
	for index, candidate := range candidates {
		wireCandidates[index] = TaskDomainOffsetCandidateDTO{
			OffsetSeconds: candidate.OffsetSeconds,
			UTC:           candidate.UTC,
		}
	}
	return &AmbiguousLocalTimeContractError{candidates: wireCandidates}
}

func (e *AmbiguousLocalTimeContractError) Error() string {
	return string(taskdomain.ErrorCodeAmbiguousLocalTime)
}

func (e *AmbiguousLocalTimeContractError) Unwrap() error {
	return taskdomain.ErrAmbiguousLocalTime
}

// MapTaskDomainError maps all concurrency, schedule, and fence failures to an
// explicit HTTP response. In particular, fenced writes never fall back to a
// legacy or platform store.
func MapTaskDomainError(err error) TaskDomainHTTPError {
	if errors.Is(err, ErrInvalidTaskDomainRequest) || errors.Is(err, taskdomain.ErrInvalidTaskAggregateSnapshot) {
		return taskDomainHTTPError(http.StatusBadRequest, "invalid_request", "the request body is invalid", false, nil)
	}
	if errors.Is(err, storage.ErrTenantEpochMismatch) || errors.Is(err, taskdomain.ErrTaskRuntimeEpochConflict) {
		return taskDomainHTTPError(http.StatusConflict, "tenant_epoch_mismatch", "tenant runtime changed; refresh and retry", true, nil)
	}
	if errors.Is(err, storage.ErrTenantWorkspaceFenced) || errors.Is(err, storage.ErrTenantWriteTxClosed) {
		return taskDomainHTTPError(http.StatusServiceUnavailable, "tenant_workspace_fenced", "tenant writes are temporarily fenced", true, nil)
	}
	if errors.Is(err, storage.ErrTenantWorkspaceMissing) {
		return taskDomainHTTPError(http.StatusServiceUnavailable, "tenant_workspace_unavailable", "tenant workspace is unavailable", true, nil)
	}
	if errors.Is(err, taskdomain.ErrTaskRevisionConflict) ||
		errors.Is(err, taskdomain.ErrScheduleRevisionConflict) ||
		errors.Is(err, taskdomain.ErrOccurrenceRevisionConflict) ||
		errors.Is(err, taskdomain.ErrProjectRevisionConflict) || errors.Is(err, taskdomain.ErrRoadmapRevisionConflict) {
		return taskDomainHTTPError(http.StatusConflict, "revision_conflict", "the resource changed; refresh and retry", false, nil)
	}
	if errors.Is(err, taskdomain.ErrOccurrenceReopenRequired) {
		return taskDomainHTTPError(http.StatusConflict, "occurrence_reopen_required", "reopen the occurrence before changing its schedule", false, nil)
	}
	if errors.Is(err, taskdomain.ErrInvalidTaskCommand) {
		return taskDomainHTTPError(http.StatusBadRequest, "invalid_task_command", "the task command is invalid", false, nil)
	}
	if errors.Is(err, taskdomain.ErrInvalidScheduleCommand) {
		return taskDomainHTTPError(http.StatusBadRequest, "invalid_schedule_command", "the schedule command is invalid", false, nil)
	}
	if errors.Is(err, taskdomain.ErrInvalidTaskCreation) {
		return taskDomainHTTPError(http.StatusBadRequest, "invalid_task_creation", "the task creation request is invalid", false, nil)
	}
	if errors.Is(err, taskdomain.ErrProjectNotFound) {
		return taskDomainHTTPError(http.StatusNotFound, "project_not_found", "project not found", false, nil)
	}
	if errors.Is(err, taskdomain.ErrTaskNotFound) {
		return taskDomainHTTPError(http.StatusNotFound, "task_not_found", "task not found", false, nil)
	}
	if errors.Is(err, taskdomain.ErrOccurrenceNotFound) {
		return taskDomainHTTPError(http.StatusNotFound, "occurrence_not_found", "occurrence not found", false, nil)
	}
	if errors.Is(err, taskdomain.ErrRoadmapNotFound) || errors.Is(err, taskdomain.ErrRoadmapNodeNotFound) {
		return taskDomainHTTPError(http.StatusNotFound, "roadmap_not_found", "roadmap or node not found", false, nil)
	}
	if errors.Is(err, taskdomain.ErrRoadmapNodeHasTasks) {
		return taskDomainHTTPError(http.StatusConflict, "roadmap_node_has_tasks", "unlink or move linked tasks before deleting this node", false, nil)
	}
	if errors.Is(err, taskdomain.ErrRoadmapAlreadyExists) {
		return taskDomainHTTPError(http.StatusConflict, "roadmap_already_exists", "this project already has a roadmap", false, nil)
	}
	if errors.Is(err, taskdomain.ErrRoadmapRequiresLearningProject) {
		return taskDomainHTTPError(http.StatusBadRequest, "roadmap_requires_learning_project", "only learning projects can own a roadmap", false, nil)
	}
	if errors.Is(err, taskdomain.ErrInvalidRoadmapCommand) {
		return taskDomainHTTPError(http.StatusBadRequest, "invalid_roadmap_command", "the roadmap command is invalid", false, nil)
	}
	if errors.Is(err, taskdomain.ErrProjectHasOpenOccurrences) {
		return taskDomainHTTPError(http.StatusConflict, "project_has_open_occurrences", "project still has non-terminal occurrences", false, nil)
	}
	if errors.Is(err, taskdomain.ErrSystemProjectImmutable) {
		return taskDomainHTTPError(http.StatusBadRequest, "system_project_immutable", "system project identity cannot be changed", false, nil)
	}
	if errors.Is(err, taskdomain.ErrInvalidSystemProjectSet) {
		return taskDomainHTTPError(http.StatusBadRequest, "invalid_system_project_set", "the workspace system project set is invalid", false, nil)
	}
	if errors.Is(err, taskdomain.ErrInvalidProjectCommand) {
		return taskDomainHTTPError(http.StatusBadRequest, "invalid_project_command", "the project command is invalid", false, nil)
	}

	code := taskdomain.ErrorCodeOf(err)
	switch code {
	case taskdomain.ErrorCode("revision_conflict"):
		var conflict *RevisionConflictContractError
		var details *TaskDomainErrorDetails
		if errors.As(err, &conflict) {
			current := cloneTaskDomainCurrentRevisions(conflict.CurrentRevisions)
			details = &TaskDomainErrorDetails{CurrentRevisions: &current}
		}
		return taskDomainHTTPError(http.StatusConflict, "revision_conflict", "the resource changed; refresh and retry", false, details)
	case taskdomain.ErrorCodeInvalidTaskTransition, taskdomain.ErrorCodeInvalidOccurrenceTransition:
		return taskDomainHTTPError(http.StatusBadRequest, "invalid_transition", "the requested state transition is not allowed", false, nil)
	case taskdomain.ErrorCodeInvalidSchedule, taskdomain.ErrorCodeInvalidTimezone:
		return taskDomainHTTPError(http.StatusBadRequest, "invalid_schedule", "the schedule is invalid", false, nil)
	case taskdomain.ErrorCodeNonexistentLocalTime:
		return taskDomainHTTPError(http.StatusUnprocessableEntity, "nonexistent_local_time", "the local time does not exist in the selected timezone", false, nil)
	case taskdomain.ErrorCodeAmbiguousLocalTime:
		var ambiguous *AmbiguousLocalTimeContractError
		var details *TaskDomainErrorDetails
		if errors.As(err, &ambiguous) {
			details = &TaskDomainErrorDetails{
				OffsetCandidates: append([]TaskDomainOffsetCandidateDTO(nil), ambiguous.candidates...),
			}
		}
		return taskDomainHTTPError(http.StatusUnprocessableEntity, "ambiguous_local_time", "select one timezone offset candidate", false, details)
	case taskdomain.ErrorCodeLifecyclePatchForbidden,
		taskdomain.ErrorCodeBlockedDetailsRequired,
		taskdomain.ErrorCodeSingleOccurrenceCannotSkip,
		taskdomain.ErrorCodeInvalidProject:
		return taskDomainHTTPError(http.StatusBadRequest, string(code), "the request violates a task-domain rule", false, nil)
	default:
		return taskDomainHTTPError(http.StatusInternalServerError, "internal_error", "task-domain request failed", false, nil)
	}
}

func cloneTaskDomainCurrentRevisions(current TaskDomainCurrentRevisions) TaskDomainCurrentRevisions {
	clone := current
	if current.OccurrenceRevisions != nil {
		clone.OccurrenceRevisions = make(map[string]int64, len(current.OccurrenceRevisions))
		for occurrenceID, revision := range current.OccurrenceRevisions {
			clone.OccurrenceRevisions[occurrenceID] = revision
		}
	}
	return clone
}

func taskDomainHTTPError(status int, code, message string, retryable bool, details *TaskDomainErrorDetails) TaskDomainHTTPError {
	return TaskDomainHTTPError{
		Status: status,
		Response: TaskDomainErrorResponse{Error: TaskDomainAPIError{
			Code: code, Message: message, Retryable: retryable, Details: details,
		}},
	}
}
