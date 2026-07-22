package taskapp

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/taskdomain"
)

var (
	ErrInvalidRequest = errors.New("invalid task application request")
	ErrInvalidRuntime = errors.New("invalid task application runtime")
)

type RuntimeResolver interface {
	Resolve(context.Context, string) (RuntimeSnapshot, error)
}

type Clock interface {
	Now() time.Time
}

type IDGenerator interface {
	NewID(context.Context) (string, error)
}

type CommandIDGenerator interface {
	NewCommandID(context.Context) (string, error)
}

type TaskFactory interface {
	Build(taskdomain.TaskCreationInput) (taskdomain.TaskAggregateSnapshot, taskdomain.TaskCreationDetails, error)
}

type TaskService interface {
	CreateTask(context.Context, taskdomain.CreateTaskRequest) (TaskCommandOutcome, error)
	PatchTask(context.Context, taskdomain.PatchTaskRequest) (TaskCommandOutcome, error)
	ExecuteLifecycleCommand(context.Context, taskdomain.LifecycleCommandRequest) (TaskCommandOutcome, error)
}

type OccurrenceService interface {
	Execute(context.Context, taskdomain.OccurrenceCommandRequest) (OccurrenceCommandOutcome, error)
}

type ProjectService interface {
	CreateProject(context.Context, taskdomain.CreateProjectRequest) (ProjectCommandOutcome, error)
	UpdateProject(context.Context, taskdomain.UpdateProjectRequest) (ProjectCommandOutcome, error)
	CompleteProject(context.Context, taskdomain.ExistingProjectRequest) (ProjectCommandOutcome, error)
	ArchiveProject(context.Context, taskdomain.ExistingProjectRequest) (ProjectCommandOutcome, error)
	DeleteProject(context.Context, taskdomain.ExistingProjectRequest) (ProjectCommandOutcome, error)
}

// CommandMetadata is created by the application boundary. HTTP callers never
// supply audit identifiers or timestamps for schedule mutations.
type CommandMetadata struct {
	ActorID   string
	CommandID string
	At        time.Time
}

type ScheduleService interface {
	RescheduleOccurrence(context.Context, taskdomain.RescheduleOccurrenceRequest, CommandMetadata) (ScheduleCommandOutcome, error)
	RescheduleThisAndFollowing(context.Context, taskdomain.RescheduleThisAndFutureRequest, CommandMetadata) (ScheduleCommandOutcome, error)
}

// TaskDomainReader is the narrow read-model capability carried by the same
// request-scoped runtime. Read-before-write commands and queries therefore
// never resolve a second runtime or cross an epoch boundary.
type TaskDomainReader interface {
	GetProject(context.Context, string) (taskdomain.ProjectSnapshot, error)
	ListProjects(context.Context, taskdomain.ProjectListFilter) ([]taskdomain.ProjectSnapshot, error)
	GetTaskAggregate(context.Context, string) (taskdomain.TaskAggregateQueryResult, error)
	ListTaskDefinitions(context.Context, taskdomain.TaskDefinitionListFilter) ([]taskdomain.TaskDefinitionSnapshot, error)
	GetOccurrence(context.Context, string) (taskdomain.QueryOccurrenceSnapshot, error)
	ListOccurrences(context.Context, taskdomain.OccurrenceListFilter) ([]taskdomain.QueryOccurrenceSnapshot, error)
}

type RuntimeSnapshot struct {
	WorkspaceID   string
	Epoch         int64
	Factory       TaskFactory
	Tasks         TaskService
	Occurrences   OccurrenceService
	Projects      ProjectService
	Roadmaps      *taskdomain.RoadmapService
	RoadmapReader taskdomain.RoadmapReader
	Schedules     ScheduleService
	Reader        TaskDomainReader
}

type TaskCommandOutcome struct {
	Task                taskdomain.TaskRecord
	TaskRevision        int64
	ScheduleRevision    int64
	LifecycleStatus     taskdomain.TaskLifecycleStatus
	OccurrenceRevisions map[string]int64
	CommandID           string
}

type OccurrenceCommandOutcome struct {
	TaskRevision        int64
	ScheduleRevision    int64
	OccurrenceRevision  int64
	TaskLifecycleStatus taskdomain.TaskLifecycleStatus
	ExecutionStatus     taskdomain.ExecutionStatus
	CommandID           string
}

type ProjectCommandOutcome struct {
	Project   taskdomain.Project
	Revision  int64
	Deleted   bool
	CommandID string
}

type ScheduleCommandOutcome struct {
	TaskRevision       int64
	ScheduleRevision   int64
	OccurrenceRevision int64
	ScheduleVersion    int64
	Candidates         []taskdomain.OffsetCandidate
	CommandID          string
}

type Facade struct {
	runtimes   RuntimeResolver
	clock      Clock
	ids        IDGenerator
	commandIDs CommandIDGenerator
}

type CreateRoadmapRequest struct{ WorkspaceID, ActorID, ProjectID, Title, Description string }
type CreateRoadmapNodeRequest struct {
	WorkspaceID, ActorID, RoadmapID, ParentID, Title, Description string
	Type                                                          taskdomain.RoadmapNodeType
	Position                                                      float64
}
type UpdateRoadmapNodeRequest struct {
	WorkspaceID, ActorID, RoadmapID, NodeID, ParentID, Title, Description string
	Type                                                                  taskdomain.RoadmapNodeType
	Position                                                              float64
	ExpectedRevision                                                      int64
}
type DeleteRoadmapNodeRequest struct {
	WorkspaceID, ActorID, RoadmapID, NodeID string
	ExpectedRevision                        int64
}

func (facade *Facade) GetRoadmap(ctx context.Context, request EntityQueryRequest) (taskdomain.RoadmapSnapshot, error) {
	if !validWorkspaceActorEntity(request.WorkspaceID, request.ActorID, request.EntityID) {
		return taskdomain.RoadmapSnapshot{}, ErrInvalidRequest
	}
	runtime, err := facade.resolve(ctx, request.WorkspaceID)
	if err != nil {
		return taskdomain.RoadmapSnapshot{}, err
	}
	if runtime.RoadmapReader == nil {
		return taskdomain.RoadmapSnapshot{}, ErrInvalidRuntime
	}
	return runtime.RoadmapReader.GetRoadmapByProject(ctx, request.EntityID)
}
func (facade *Facade) CreateRoadmap(ctx context.Context, r CreateRoadmapRequest) (taskdomain.RoadmapSnapshot, error) {
	if !validWorkspaceActorEntity(r.WorkspaceID, r.ActorID, r.ProjectID) || strings.TrimSpace(r.Title) == "" {
		return taskdomain.RoadmapSnapshot{}, ErrInvalidRequest
	}
	runtime, now, commandID, err := facade.resolveAudit(ctx, r.WorkspaceID)
	if err != nil {
		return taskdomain.RoadmapSnapshot{}, err
	}
	if runtime.Roadmaps == nil {
		return taskdomain.RoadmapSnapshot{}, ErrInvalidRuntime
	}
	id, err := facade.newEntityID(ctx)
	if err != nil {
		return taskdomain.RoadmapSnapshot{}, err
	}
	return runtime.Roadmaps.CreateRoadmap(ctx, taskdomain.CreateRoadmapRequest{WorkspaceID: r.WorkspaceID, ProjectID: r.ProjectID, RoadmapID: id, Title: r.Title, Description: r.Description, ExpectedRuntimeEpoch: runtime.Epoch, CommandID: commandID, ActorID: r.ActorID, At: now})
}
func (facade *Facade) CreateRoadmapNode(ctx context.Context, r CreateRoadmapNodeRequest) (taskdomain.RoadmapNodeSnapshot, error) {
	if !validWorkspaceActorEntity(r.WorkspaceID, r.ActorID, r.RoadmapID) {
		return taskdomain.RoadmapNodeSnapshot{}, ErrInvalidRequest
	}
	runtime, now, commandID, err := facade.resolveAudit(ctx, r.WorkspaceID)
	if err != nil {
		return taskdomain.RoadmapNodeSnapshot{}, err
	}
	if runtime.Roadmaps == nil {
		return taskdomain.RoadmapNodeSnapshot{}, ErrInvalidRuntime
	}
	id, err := facade.newEntityID(ctx)
	if err != nil {
		return taskdomain.RoadmapNodeSnapshot{}, err
	}
	return runtime.Roadmaps.CreateNode(ctx, taskdomain.CreateRoadmapNodeRequest{WorkspaceID: r.WorkspaceID, RoadmapID: r.RoadmapID, NodeID: id, ParentID: r.ParentID, Title: r.Title, Description: r.Description, Type: r.Type, Position: r.Position, ExpectedRuntimeEpoch: runtime.Epoch, CommandID: commandID, ActorID: r.ActorID, At: now})
}
func (facade *Facade) UpdateRoadmapNode(ctx context.Context, r UpdateRoadmapNodeRequest) (taskdomain.RoadmapNodeSnapshot, error) {
	if !validWorkspaceActorEntity(r.WorkspaceID, r.ActorID, r.NodeID) {
		return taskdomain.RoadmapNodeSnapshot{}, ErrInvalidRequest
	}
	runtime, now, commandID, err := facade.resolveAudit(ctx, r.WorkspaceID)
	if err != nil {
		return taskdomain.RoadmapNodeSnapshot{}, err
	}
	if runtime.Roadmaps == nil {
		return taskdomain.RoadmapNodeSnapshot{}, ErrInvalidRuntime
	}
	return runtime.Roadmaps.UpdateNode(ctx, taskdomain.UpdateRoadmapNodeRequest{WorkspaceID: r.WorkspaceID, RoadmapID: r.RoadmapID, NodeID: r.NodeID, ParentID: r.ParentID, Title: r.Title, Description: r.Description, Type: r.Type, Position: r.Position, ExpectedRevision: r.ExpectedRevision, ExpectedRuntimeEpoch: runtime.Epoch, CommandID: commandID, ActorID: r.ActorID, At: now})
}
func (facade *Facade) DeleteRoadmapNode(ctx context.Context, r DeleteRoadmapNodeRequest) error {
	if !validWorkspaceActorEntity(r.WorkspaceID, r.ActorID, r.NodeID) {
		return ErrInvalidRequest
	}
	runtime, now, commandID, err := facade.resolveAudit(ctx, r.WorkspaceID)
	if err != nil {
		return err
	}
	if runtime.Roadmaps == nil {
		return ErrInvalidRuntime
	}
	return runtime.Roadmaps.DeleteNode(ctx, taskdomain.DeleteRoadmapNodeRequest{WorkspaceID: r.WorkspaceID, RoadmapID: r.RoadmapID, NodeID: r.NodeID, ExpectedRevision: r.ExpectedRevision, ExpectedRuntimeEpoch: runtime.Epoch, CommandID: commandID, ActorID: r.ActorID, At: now})
}

func NewFacade(runtimes RuntimeResolver, clock Clock, ids IDGenerator, commandIDs CommandIDGenerator) *Facade {
	return &Facade{runtimes: runtimes, clock: clock, ids: ids, commandIDs: commandIDs}
}

type CreateTaskRequest struct {
	WorkspaceID     string
	ActorID         string
	Project         taskdomain.ProjectIdentity
	Roadmap         *taskdomain.Roadmap
	TaskNote        *taskdomain.TaskNoteIdentity
	Title           string
	Description     string
	Priority        int
	SortOrder       float64
	Schedule        taskdomain.ScheduleInput
	AllDayEndDate   string
	DueAt           *time.Time
	SelectedOffsets map[string]int
}

type CreateTaskResult struct {
	TaskID           string
	TaskRevision     int64
	ScheduleRevision int64
	LifecycleStatus  taskdomain.TaskLifecycleStatus
	CommandID        string
	CreationDetails  taskdomain.TaskCreationDetails
	Occurrences      []taskdomain.OccurrenceRecord
}

func (facade *Facade) CreateTask(ctx context.Context, request CreateTaskRequest) (CreateTaskResult, error) {
	if !validWorkspaceActor(request.WorkspaceID, request.ActorID) || request.Project.WorkspaceID != request.WorkspaceID ||
		strings.TrimSpace(request.Project.ProjectID) == "" {
		return CreateTaskResult{}, ErrInvalidRequest
	}
	runtime, err := facade.resolve(ctx, request.WorkspaceID)
	if err != nil {
		return CreateTaskResult{}, err
	}
	now, err := facade.now()
	if err != nil {
		return CreateTaskResult{}, err
	}
	taskID, err := facade.newEntityID(ctx)
	if err != nil {
		return CreateTaskResult{}, err
	}
	commandID, err := facade.newCommandID(ctx)
	if err != nil {
		return CreateTaskResult{}, err
	}
	snapshot, details, err := runtime.Factory.Build(taskdomain.TaskCreationInput{
		WorkspaceID: request.WorkspaceID, Project: request.Project, Roadmap: request.Roadmap, TaskNote: request.TaskNote,
		TaskID: taskID, ActorID: request.ActorID, ActorTime: now,
		Title: request.Title, Description: request.Description, Priority: request.Priority, SortOrder: request.SortOrder,
		Schedule: request.Schedule, AllDayEndDate: request.AllDayEndDate, DueAt: cloneTime(request.DueAt),
		SelectedOffsets: cloneOffsets(request.SelectedOffsets),
	})
	if err != nil {
		return CreateTaskResult{}, err
	}
	outcome, err := runtime.Tasks.CreateTask(ctx, taskdomain.CreateTaskRequest{
		WorkspaceID: request.WorkspaceID, ExpectedRuntimeEpoch: runtime.Epoch, Snapshot: snapshot,
		CommandID: commandID, ActorID: request.ActorID, At: now,
	})
	if err != nil {
		return CreateTaskResult{}, err
	}
	return CreateTaskResult{
		TaskID: taskID, TaskRevision: outcome.TaskRevision, ScheduleRevision: outcome.ScheduleRevision,
		LifecycleStatus: outcome.LifecycleStatus, CommandID: commandID, CreationDetails: details,
		Occurrences: cloneOccurrenceRecords(snapshot.Occurrences),
	}, nil
}

type CreateProjectRequest struct {
	WorkspaceID string
	ActorID     string
	Name        string
	Kind        taskdomain.ProjectKind
	Horizon     taskdomain.ProjectHorizon
	Status      taskdomain.ProjectStatus
}

func (facade *Facade) CreateProject(ctx context.Context, request CreateProjectRequest) (ProjectCommandOutcome, error) {
	if !validWorkspaceActor(request.WorkspaceID, request.ActorID) {
		return ProjectCommandOutcome{}, ErrInvalidRequest
	}
	runtime, err := facade.resolve(ctx, request.WorkspaceID)
	if err != nil {
		return ProjectCommandOutcome{}, err
	}
	now, err := facade.now()
	if err != nil {
		return ProjectCommandOutcome{}, err
	}
	projectID, err := facade.newEntityID(ctx)
	if err != nil {
		return ProjectCommandOutcome{}, err
	}
	commandID, err := facade.newCommandID(ctx)
	if err != nil {
		return ProjectCommandOutcome{}, err
	}
	outcome, err := runtime.Projects.CreateProject(ctx, taskdomain.CreateProjectRequest{
		WorkspaceID: request.WorkspaceID, ExpectedRuntimeEpoch: runtime.Epoch, ExpectedProjectRevision: 0,
		Project: taskdomain.Project{
			WorkspaceID: request.WorkspaceID, ID: projectID, Name: request.Name, Kind: request.Kind,
			Horizon: request.Horizon, Status: request.Status, SystemRole: taskdomain.ProjectSystemRoleNone,
		},
		CommandID: commandID, ActorID: request.ActorID, At: now,
	})
	if err != nil {
		return ProjectCommandOutcome{}, err
	}
	outcome.CommandID = commandID
	return outcome, nil
}

type UpdateProjectRequest struct {
	WorkspaceID             string
	ActorID                 string
	ProjectID               string
	ExpectedProjectRevision int64
	Project                 taskdomain.Project
}

func (facade *Facade) UpdateProject(ctx context.Context, request UpdateProjectRequest) (ProjectCommandOutcome, error) {
	if !validWorkspaceActorEntity(request.WorkspaceID, request.ActorID, request.ProjectID) {
		return ProjectCommandOutcome{}, ErrInvalidRequest
	}
	runtime, now, commandID, err := facade.resolveAudit(ctx, request.WorkspaceID)
	if err != nil {
		return ProjectCommandOutcome{}, err
	}
	outcome, err := runtime.Projects.UpdateProject(ctx, taskdomain.UpdateProjectRequest{
		WorkspaceID: request.WorkspaceID, ProjectID: request.ProjectID,
		ExpectedRuntimeEpoch: runtime.Epoch, ExpectedProjectRevision: request.ExpectedProjectRevision,
		Project: request.Project, CommandID: commandID, ActorID: request.ActorID, At: now,
	})
	if err != nil {
		return ProjectCommandOutcome{}, err
	}
	outcome.CommandID = commandID
	return outcome, nil
}

type PatchProjectRequest struct {
	WorkspaceID             string
	ActorID                 string
	ProjectID               string
	ExpectedProjectRevision int64
	Name                    *string
	Kind                    *taskdomain.ProjectKind
	Horizon                 *taskdomain.ProjectHorizon
	Status                  *taskdomain.ProjectStatus
}

// PatchProject keeps read-before-write inside one immutable runtime snapshot;
// transports only describe the fields that changed.
func (facade *Facade) PatchProject(ctx context.Context, request PatchProjectRequest) (ProjectCommandOutcome, error) {
	if !validWorkspaceActorEntity(request.WorkspaceID, request.ActorID, request.ProjectID) || request.ExpectedProjectRevision < 1 ||
		(request.Name == nil && request.Kind == nil && request.Horizon == nil && request.Status == nil) {
		return ProjectCommandOutcome{}, ErrInvalidRequest
	}
	runtime, err := facade.resolve(ctx, request.WorkspaceID)
	if err != nil {
		return ProjectCommandOutcome{}, err
	}
	current, err := runtime.Reader.GetProject(ctx, request.ProjectID)
	if err != nil {
		return ProjectCommandOutcome{}, err
	}
	if current.Project.WorkspaceID != request.WorkspaceID || current.Project.ID != request.ProjectID {
		return ProjectCommandOutcome{}, ErrInvalidRuntime
	}
	if current.Revision != request.ExpectedProjectRevision {
		return ProjectCommandOutcome{}, taskdomain.ErrProjectRevisionConflict
	}
	after := current.Project
	if request.Name != nil {
		after.Name = *request.Name
	}
	if request.Kind != nil {
		after.Kind = *request.Kind
	}
	if request.Horizon != nil {
		after.Horizon = *request.Horizon
	}
	if request.Status != nil {
		after.Status = *request.Status
	}
	now, err := facade.now()
	if err != nil {
		return ProjectCommandOutcome{}, err
	}
	commandID, err := facade.newCommandID(ctx)
	if err != nil {
		return ProjectCommandOutcome{}, err
	}
	outcome, err := runtime.Projects.UpdateProject(ctx, taskdomain.UpdateProjectRequest{
		WorkspaceID: request.WorkspaceID, ProjectID: request.ProjectID,
		ExpectedRuntimeEpoch: runtime.Epoch, ExpectedProjectRevision: request.ExpectedProjectRevision,
		Project: after, CommandID: commandID, ActorID: request.ActorID, At: now,
	})
	if err != nil {
		return ProjectCommandOutcome{}, err
	}
	outcome.CommandID = commandID
	return outcome, nil
}

type ExistingProjectRequest struct {
	WorkspaceID             string
	ActorID                 string
	ProjectID               string
	ExpectedProjectRevision int64
}

func (facade *Facade) CompleteProject(ctx context.Context, request ExistingProjectRequest) (ProjectCommandOutcome, error) {
	return facade.executeProjectCommand(ctx, request, taskdomain.ProjectCommandComplete)
}

func (facade *Facade) ArchiveProject(ctx context.Context, request ExistingProjectRequest) (ProjectCommandOutcome, error) {
	return facade.executeProjectCommand(ctx, request, taskdomain.ProjectCommandArchive)
}

func (facade *Facade) DeleteProject(ctx context.Context, request ExistingProjectRequest) (ProjectCommandOutcome, error) {
	return facade.executeProjectCommand(ctx, request, taskdomain.ProjectCommandDelete)
}

func (facade *Facade) executeProjectCommand(ctx context.Context, request ExistingProjectRequest, command taskdomain.ProjectCommand) (ProjectCommandOutcome, error) {
	if !validWorkspaceActorEntity(request.WorkspaceID, request.ActorID, request.ProjectID) {
		return ProjectCommandOutcome{}, ErrInvalidRequest
	}
	runtime, now, commandID, err := facade.resolveAudit(ctx, request.WorkspaceID)
	if err != nil {
		return ProjectCommandOutcome{}, err
	}
	domainRequest := taskdomain.ExistingProjectRequest{
		WorkspaceID: request.WorkspaceID, ProjectID: request.ProjectID, Command: command,
		ExpectedRuntimeEpoch: runtime.Epoch, ExpectedProjectRevision: request.ExpectedProjectRevision,
		CommandID: commandID, ActorID: request.ActorID, At: now,
	}
	switch command {
	case taskdomain.ProjectCommandComplete:
		outcome, err := runtime.Projects.CompleteProject(ctx, domainRequest)
		return projectFacadeOutcome(outcome, commandID, err)
	case taskdomain.ProjectCommandArchive:
		outcome, err := runtime.Projects.ArchiveProject(ctx, domainRequest)
		return projectFacadeOutcome(outcome, commandID, err)
	case taskdomain.ProjectCommandDelete:
		outcome, err := runtime.Projects.DeleteProject(ctx, domainRequest)
		return projectFacadeOutcome(outcome, commandID, err)
	default:
		return ProjectCommandOutcome{}, ErrInvalidRequest
	}
}

type TaskLifecycleRequest struct {
	WorkspaceID string
	ActorID     string
	TaskID      string
	Command     taskdomain.TaskLifecycleCommand
	Expected    taskdomain.LifecycleExpectedRevisions
}

type PatchTaskRequest struct {
	WorkspaceID              string
	ActorID                  string
	TaskID                   string
	ExpectedTaskRevision     int64
	ExpectedScheduleRevision int64
	Title                    *string
	Description              *string
	Priority                 *int
	SortOrder                *float64
	ProjectID                *string
	RoadmapNodeID            *string
	NoteID                   *string
}

func (facade *Facade) PatchTask(ctx context.Context, request PatchTaskRequest) (TaskCommandOutcome, error) {
	if !validWorkspaceActorEntity(request.WorkspaceID, request.ActorID, request.TaskID) ||
		(request.Title == nil && request.Description == nil && request.Priority == nil && request.SortOrder == nil &&
			request.ProjectID == nil && request.RoadmapNodeID == nil && request.NoteID == nil) {
		return TaskCommandOutcome{}, ErrInvalidRequest
	}
	runtime, err := facade.resolve(ctx, request.WorkspaceID)
	if err != nil {
		return TaskCommandOutcome{}, err
	}
	current, err := runtime.Reader.GetTaskAggregate(ctx, request.TaskID)
	if err != nil {
		return TaskCommandOutcome{}, err
	}
	if current.Task.WorkspaceID != request.WorkspaceID || current.Task.ID != request.TaskID ||
		current.Aggregate.WorkspaceID != request.WorkspaceID || current.Aggregate.TaskID != request.TaskID {
		return TaskCommandOutcome{}, ErrInvalidRuntime
	}
	projectID := current.Task.ProjectID
	patch := taskdomain.TaskAttributePatch{
		Title: cloneString(request.Title), Description: cloneString(request.Description), Priority: cloneInt(request.Priority), SortOrder: cloneFloat64(request.SortOrder),
	}
	if request.ProjectID != nil {
		projectID = strings.TrimSpace(*request.ProjectID)
		if projectID == "" {
			return TaskCommandOutcome{}, ErrInvalidRequest
		}
		patch.Project = &taskdomain.ProjectIdentity{WorkspaceID: request.WorkspaceID, ProjectID: projectID}
	}
	if request.RoadmapNodeID != nil {
		patch.RoadmapSet = true
		if roadmapID := strings.TrimSpace(*request.RoadmapNodeID); roadmapID != "" {
			patch.Roadmap = &taskdomain.Roadmap{WorkspaceID: request.WorkspaceID, ID: roadmapID, ProjectID: projectID}
		}
	}
	if request.NoteID != nil {
		patch.TaskNoteSet = true
		if noteID := strings.TrimSpace(*request.NoteID); noteID != "" {
			patch.TaskNote = &taskdomain.TaskNoteIdentity{WorkspaceID: request.WorkspaceID, NoteID: noteID}
		}
	}
	metadata, err := facade.commandMetadata(ctx, request.ActorID)
	if err != nil {
		return TaskCommandOutcome{}, err
	}
	outcome, err := runtime.Tasks.PatchTask(ctx, taskdomain.PatchTaskRequest{
		WorkspaceID: request.WorkspaceID, TaskID: request.TaskID, ExpectedRuntimeEpoch: runtime.Epoch,
		ExpectedTaskRevision: request.ExpectedTaskRevision, ExpectedScheduleRevision: request.ExpectedScheduleRevision,
		Patch: patch, CommandID: metadata.CommandID, ActorID: metadata.ActorID, At: metadata.At,
	})
	if err != nil {
		return TaskCommandOutcome{}, err
	}
	outcome.OccurrenceRevisions = cloneRevisions(outcome.OccurrenceRevisions)
	outcome.CommandID = metadata.CommandID
	return outcome, nil
}

func (facade *Facade) ExecuteTaskLifecycle(ctx context.Context, request TaskLifecycleRequest) (TaskCommandOutcome, error) {
	if !validWorkspaceActorEntity(request.WorkspaceID, request.ActorID, request.TaskID) {
		return TaskCommandOutcome{}, ErrInvalidRequest
	}
	runtime, now, commandID, err := facade.resolveAudit(ctx, request.WorkspaceID)
	if err != nil {
		return TaskCommandOutcome{}, err
	}
	outcome, err := runtime.Tasks.ExecuteLifecycleCommand(ctx, taskdomain.LifecycleCommandRequest{
		WorkspaceID: request.WorkspaceID, TaskID: request.TaskID, Command: request.Command,
		ExpectedRuntimeEpoch: runtime.Epoch, Expected: cloneLifecycleExpected(request.Expected),
		CommandID: commandID, ActorID: request.ActorID, At: now,
	})
	if err != nil {
		return TaskCommandOutcome{}, err
	}
	outcome.OccurrenceRevisions = cloneRevisions(outcome.OccurrenceRevisions)
	outcome.CommandID = commandID
	return outcome, nil
}

type OccurrenceRequest struct {
	WorkspaceID   string
	ActorID       string
	TaskID        string
	OccurrenceID  string
	Command       taskdomain.OccurrenceCommand
	Expected      taskdomain.OccurrenceCommandExpectedRevisions
	BlockedReason string
	NextAction    string
}

func (facade *Facade) ExecuteOccurrence(ctx context.Context, request OccurrenceRequest) (OccurrenceCommandOutcome, error) {
	if !validWorkspaceActorEntity(request.WorkspaceID, request.ActorID, request.TaskID) || strings.TrimSpace(request.OccurrenceID) == "" {
		return OccurrenceCommandOutcome{}, ErrInvalidRequest
	}
	runtime, err := facade.resolve(ctx, request.WorkspaceID)
	if err != nil {
		return OccurrenceCommandOutcome{}, err
	}
	return facade.executeOccurrence(ctx, runtime, request)
}

type OccurrenceByIDRequest struct {
	WorkspaceID   string
	ActorID       string
	OccurrenceID  string
	Command       taskdomain.OccurrenceCommand
	Expected      taskdomain.OccurrenceCommandExpectedRevisions
	BlockedReason string
	NextAction    string
}

// ExecuteOccurrenceByID serves occurrence-centric routes without making the
// handler perform a repository lookup. The lookup and write share one runtime.
func (facade *Facade) ExecuteOccurrenceByID(ctx context.Context, request OccurrenceByIDRequest) (OccurrenceCommandOutcome, error) {
	if !validWorkspaceActor(request.WorkspaceID, request.ActorID) || strings.TrimSpace(request.OccurrenceID) == "" {
		return OccurrenceCommandOutcome{}, ErrInvalidRequest
	}
	runtime, err := facade.resolve(ctx, request.WorkspaceID)
	if err != nil {
		return OccurrenceCommandOutcome{}, err
	}
	snapshot, err := runtime.Reader.GetOccurrence(ctx, request.OccurrenceID)
	if err != nil {
		return OccurrenceCommandOutcome{}, err
	}
	if snapshot.WorkspaceID != request.WorkspaceID || snapshot.OccurrenceID != request.OccurrenceID || strings.TrimSpace(snapshot.TaskID) == "" {
		return OccurrenceCommandOutcome{}, ErrInvalidRuntime
	}
	return facade.executeOccurrence(ctx, runtime, OccurrenceRequest{
		WorkspaceID: request.WorkspaceID, ActorID: request.ActorID, TaskID: snapshot.TaskID, OccurrenceID: request.OccurrenceID,
		Command: request.Command, Expected: request.Expected, BlockedReason: request.BlockedReason, NextAction: request.NextAction,
	})
}

func (facade *Facade) executeOccurrence(ctx context.Context, runtime RuntimeSnapshot, request OccurrenceRequest) (OccurrenceCommandOutcome, error) {
	now, err := facade.now()
	if err != nil {
		return OccurrenceCommandOutcome{}, err
	}
	commandID, err := facade.newCommandID(ctx)
	if err != nil {
		return OccurrenceCommandOutcome{}, err
	}
	outcome, err := runtime.Occurrences.Execute(ctx, taskdomain.OccurrenceCommandRequest{
		WorkspaceID: request.WorkspaceID, TaskID: request.TaskID, OccurrenceID: request.OccurrenceID,
		Command: request.Command, ExpectedRuntimeEpoch: runtime.Epoch, Expected: request.Expected,
		BlockedReason: request.BlockedReason, NextAction: request.NextAction,
		CommandID: commandID, ActorID: request.ActorID, At: now,
	})
	if err != nil {
		return OccurrenceCommandOutcome{}, err
	}
	outcome.CommandID = commandID
	return outcome, nil
}

type RescheduleOccurrenceRequest struct {
	WorkspaceID                string
	ActorID                    string
	TaskID                     string
	OccurrenceID               string
	ExpectedTaskRevision       int64
	ExpectedScheduleRevision   int64
	ExpectedOccurrenceRevision int64
	Timing                     taskdomain.OccurrenceTimingInput
}

func (facade *Facade) RescheduleOccurrence(ctx context.Context, request RescheduleOccurrenceRequest) (ScheduleCommandOutcome, error) {
	if !validWorkspaceActor(request.WorkspaceID, request.ActorID) || strings.TrimSpace(request.OccurrenceID) == "" {
		return ScheduleCommandOutcome{}, ErrInvalidRequest
	}
	runtime, err := facade.resolve(ctx, request.WorkspaceID)
	if err != nil {
		return ScheduleCommandOutcome{}, err
	}
	taskID := request.TaskID
	if strings.TrimSpace(taskID) == "" {
		snapshot, readErr := runtime.Reader.GetOccurrence(ctx, request.OccurrenceID)
		if readErr != nil {
			return ScheduleCommandOutcome{}, readErr
		}
		if snapshot.WorkspaceID != request.WorkspaceID || snapshot.OccurrenceID != request.OccurrenceID || strings.TrimSpace(snapshot.TaskID) == "" {
			return ScheduleCommandOutcome{}, ErrInvalidRuntime
		}
		taskID = snapshot.TaskID
	}
	metadata, err := facade.commandMetadata(ctx, request.ActorID)
	if err != nil {
		return ScheduleCommandOutcome{}, err
	}
	outcome, err := runtime.Schedules.RescheduleOccurrence(ctx, taskdomain.RescheduleOccurrenceRequest{
		WorkspaceID: request.WorkspaceID, TaskID: taskID, OccurrenceID: request.OccurrenceID,
		ExpectedRuntimeEpoch: runtime.Epoch, ExpectedTaskRevision: request.ExpectedTaskRevision,
		ExpectedScheduleRevision: request.ExpectedScheduleRevision, ExpectedOccurrenceRevision: request.ExpectedOccurrenceRevision,
		Timing: cloneOccurrenceTiming(request.Timing),
	}, metadata)
	return scheduleFacadeOutcome(outcome, metadata.CommandID, err)
}

type RescheduleThisAndFollowingRequest struct {
	WorkspaceID              string
	ActorID                  string
	TaskID                   string
	ExpectedTaskRevision     int64
	ExpectedScheduleRevision int64
	EffectiveFrom            string
	GenerateThroughExclusive string
	Schedule                 taskdomain.ScheduleInput
	SelectedOffsets          map[string]int
}

func (facade *Facade) RescheduleThisAndFollowing(ctx context.Context, request RescheduleThisAndFollowingRequest) (ScheduleCommandOutcome, error) {
	if !validWorkspaceActorEntity(request.WorkspaceID, request.ActorID, request.TaskID) {
		return ScheduleCommandOutcome{}, ErrInvalidRequest
	}
	runtime, err := facade.resolve(ctx, request.WorkspaceID)
	if err != nil {
		return ScheduleCommandOutcome{}, err
	}
	metadata, err := facade.commandMetadata(ctx, request.ActorID)
	if err != nil {
		return ScheduleCommandOutcome{}, err
	}
	outcome, err := runtime.Schedules.RescheduleThisAndFollowing(ctx, taskdomain.RescheduleThisAndFutureRequest{
		WorkspaceID: request.WorkspaceID, TaskID: request.TaskID, ExpectedRuntimeEpoch: runtime.Epoch,
		ExpectedTaskRevision: request.ExpectedTaskRevision, ExpectedScheduleRevision: request.ExpectedScheduleRevision,
		EffectiveFrom: request.EffectiveFrom, GenerateThroughExclusive: request.GenerateThroughExclusive,
		Schedule: request.Schedule, SelectedOffsets: cloneOffsets(request.SelectedOffsets),
	}, metadata)
	return scheduleFacadeOutcome(outcome, metadata.CommandID, err)
}

func scheduleFacadeOutcome(outcome ScheduleCommandOutcome, commandID string, err error) (ScheduleCommandOutcome, error) {
	outcome.Candidates = append([]taskdomain.OffsetCandidate(nil), outcome.Candidates...)
	if err != nil {
		code := taskdomain.ErrorCodeOf(err)
		if code != taskdomain.ErrorCodeAmbiguousLocalTime && code != taskdomain.ErrorCodeNonexistentLocalTime {
			return ScheduleCommandOutcome{}, err
		}
		// Ambiguous/nonexistent local times deliberately return candidates with
		// the domain error so a caller can choose an offset and retry.
		outcome.CommandID = commandID
		return outcome, err
	}
	outcome.CommandID = commandID
	return outcome, nil
}

type OccurrenceQueryRequest struct {
	WorkspaceID string
	ActorID     string
	TaskID      string
	Scope       taskdomain.OccurrenceListScope
	From        time.Time
	To          time.Time
	Timezone    string
	ProjectID   string
	Statuses    []taskdomain.ExecutionStatus
	Recurring   *bool
}

type EntityQueryRequest struct {
	WorkspaceID string
	ActorID     string
	EntityID    string
}

type ListProjectsRequest struct {
	WorkspaceID string
	ActorID     string
	Kind        *taskdomain.ProjectKind
	Horizon     *taskdomain.ProjectHorizon
	Status      *taskdomain.ProjectStatus
}

type ProjectListResult []taskdomain.ProjectSnapshot

func (facade *Facade) ListProjects(ctx context.Context, request ListProjectsRequest) (ProjectListResult, error) {
	if !validWorkspaceActor(request.WorkspaceID, request.ActorID) {
		return nil, ErrInvalidRequest
	}
	runtime, err := facade.resolve(ctx, request.WorkspaceID)
	if err != nil {
		return nil, err
	}
	projects, err := runtime.Reader.ListProjects(ctx, taskdomain.ProjectListFilter{
		Kind: cloneProjectKind(request.Kind), Horizon: cloneProjectHorizon(request.Horizon), Status: cloneProjectStatus(request.Status),
	})
	if err != nil {
		return nil, err
	}
	result := make(ProjectListResult, len(projects))
	copy(result, projects)
	for _, project := range result {
		if project.Project.WorkspaceID != request.WorkspaceID {
			return nil, ErrInvalidRuntime
		}
	}
	return result, nil
}

type ListTaskDefinitionsRequest struct {
	WorkspaceID     string
	ActorID         string
	ProjectID       string
	LifecycleStatus *taskdomain.TaskLifecycleStatus
}

type TaskDefinitionListResult []taskdomain.TaskDefinitionSnapshot

func (facade *Facade) ListTaskDefinitions(ctx context.Context, request ListTaskDefinitionsRequest) (TaskDefinitionListResult, error) {
	if !validWorkspaceActor(request.WorkspaceID, request.ActorID) {
		return nil, ErrInvalidRequest
	}
	runtime, err := facade.resolve(ctx, request.WorkspaceID)
	if err != nil {
		return nil, err
	}
	tasks, err := runtime.Reader.ListTaskDefinitions(ctx, taskdomain.TaskDefinitionListFilter{
		ProjectID: strings.TrimSpace(request.ProjectID), LifecycleStatus: cloneTaskLifecycleStatus(request.LifecycleStatus),
	})
	if err != nil {
		return nil, err
	}
	result := make(TaskDefinitionListResult, len(tasks))
	copy(result, tasks)
	for _, task := range result {
		if task.Task.WorkspaceID != request.WorkspaceID {
			return nil, ErrInvalidRuntime
		}
	}
	return result, nil
}

type CalendarEntriesRequest struct {
	WorkspaceID string
	ActorID     string
	From        time.Time
	To          time.Time
	Timezone    string
	ProjectID   string
}

type CalendarEntryReadModel struct {
	Occurrence      taskdomain.QueryOccurrenceSnapshot
	ProjectRevision int64
}

type CalendarEntriesResult []CalendarEntryReadModel

func (facade *Facade) CalendarEntries(ctx context.Context, request CalendarEntriesRequest) (CalendarEntriesResult, error) {
	snapshots, err := facade.listOccurrences(ctx, OccurrenceQueryRequest{
		WorkspaceID: request.WorkspaceID, ActorID: request.ActorID, From: request.From, To: request.To,
		Timezone: request.Timezone, ProjectID: request.ProjectID,
	}, taskdomain.OccurrenceListCalendar)
	if err != nil {
		return nil, err
	}
	result := make(CalendarEntriesResult, len(snapshots))
	for index, snapshot := range snapshots {
		result[index] = CalendarEntryReadModel{Occurrence: snapshot, ProjectRevision: snapshot.ProjectRevision}
	}
	return result, nil
}

func (facade *Facade) GetProject(ctx context.Context, request EntityQueryRequest) (taskdomain.ProjectSnapshot, error) {
	if !validWorkspaceActorEntity(request.WorkspaceID, request.ActorID, request.EntityID) {
		return taskdomain.ProjectSnapshot{}, ErrInvalidRequest
	}
	runtime, err := facade.resolve(ctx, request.WorkspaceID)
	if err != nil {
		return taskdomain.ProjectSnapshot{}, err
	}
	result, err := runtime.Reader.GetProject(ctx, request.EntityID)
	if err != nil {
		return taskdomain.ProjectSnapshot{}, err
	}
	if result.Project.WorkspaceID != request.WorkspaceID || result.Project.ID != request.EntityID {
		return taskdomain.ProjectSnapshot{}, ErrInvalidRuntime
	}
	return result, nil
}

func (facade *Facade) GetOccurrence(ctx context.Context, request EntityQueryRequest) (taskdomain.QueryOccurrenceSnapshot, error) {
	if !validWorkspaceActorEntity(request.WorkspaceID, request.ActorID, request.EntityID) {
		return taskdomain.QueryOccurrenceSnapshot{}, ErrInvalidRequest
	}
	runtime, err := facade.resolve(ctx, request.WorkspaceID)
	if err != nil {
		return taskdomain.QueryOccurrenceSnapshot{}, err
	}
	result, err := runtime.Reader.GetOccurrence(ctx, request.EntityID)
	if err != nil {
		return taskdomain.QueryOccurrenceSnapshot{}, err
	}
	if result.WorkspaceID != request.WorkspaceID || result.OccurrenceID != request.EntityID {
		return taskdomain.QueryOccurrenceSnapshot{}, ErrInvalidRuntime
	}
	return cloneQueryOccurrences([]taskdomain.QueryOccurrenceSnapshot{result})[0], nil
}

func (facade *Facade) GetTask(ctx context.Context, request EntityQueryRequest) (taskdomain.TaskAggregateQueryResult, error) {
	if !validWorkspaceActorEntity(request.WorkspaceID, request.ActorID, request.EntityID) {
		return taskdomain.TaskAggregateQueryResult{}, ErrInvalidRequest
	}
	runtime, err := facade.resolve(ctx, request.WorkspaceID)
	if err != nil {
		return taskdomain.TaskAggregateQueryResult{}, err
	}
	result, err := runtime.Reader.GetTaskAggregate(ctx, request.EntityID)
	if err != nil {
		return taskdomain.TaskAggregateQueryResult{}, err
	}
	if result.Aggregate.WorkspaceID != request.WorkspaceID || result.Aggregate.TaskID != request.EntityID ||
		result.Task.WorkspaceID != request.WorkspaceID || result.Task.ID != request.EntityID {
		return taskdomain.TaskAggregateQueryResult{}, ErrInvalidRuntime
	}
	return cloneTaskAggregateQueryResult(result), nil
}

func (facade *Facade) ListOccurrences(ctx context.Context, request OccurrenceQueryRequest) ([]taskdomain.QueryOccurrenceSnapshot, error) {
	scope := request.Scope
	if scope == "" {
		scope = taskdomain.OccurrenceListAll
	}
	return facade.listOccurrences(ctx, request, scope)
}

func (facade *Facade) Today(ctx context.Context, request OccurrenceQueryRequest) (taskdomain.TodayProjection, error) {
	snapshots, err := facade.listOccurrences(ctx, request, taskdomain.OccurrenceListToday)
	if err != nil {
		return taskdomain.TodayProjection{}, err
	}
	now, err := facade.now()
	if err != nil {
		return taskdomain.TodayProjection{}, err
	}
	return taskdomain.BuildTodayProjection(snapshots, now, request.Timezone)
}

func (facade *Facade) Upcoming(ctx context.Context, request OccurrenceQueryRequest) ([]taskdomain.TaskListItem, error) {
	return facade.taskList(ctx, request, taskdomain.OccurrenceListUpcoming)
}

func (facade *Facade) Overdue(ctx context.Context, request OccurrenceQueryRequest) ([]taskdomain.TaskListItem, error) {
	return facade.taskList(ctx, request, taskdomain.OccurrenceListOverdue)
}

func (facade *Facade) Unscheduled(ctx context.Context, request OccurrenceQueryRequest) ([]taskdomain.TaskListItem, error) {
	return facade.taskList(ctx, request, taskdomain.OccurrenceListUnscheduled)
}

func (facade *Facade) Completed(ctx context.Context, request OccurrenceQueryRequest) ([]taskdomain.TaskListItem, error) {
	return facade.taskList(ctx, request, taskdomain.OccurrenceListCompleted)
}

func (facade *Facade) Calendar(ctx context.Context, request OccurrenceQueryRequest) (taskdomain.CalendarProjection, error) {
	snapshots, err := facade.listOccurrences(ctx, request, taskdomain.OccurrenceListCalendar)
	if err != nil {
		return taskdomain.CalendarProjection{}, err
	}
	return taskdomain.BuildCalendarProjection(snapshots)
}

func (facade *Facade) taskList(ctx context.Context, request OccurrenceQueryRequest, scope taskdomain.OccurrenceListScope) ([]taskdomain.TaskListItem, error) {
	snapshots, err := facade.listOccurrences(ctx, request, scope)
	if err != nil {
		return nil, err
	}
	return taskdomain.BuildTaskList(snapshots), nil
}

func (facade *Facade) listOccurrences(ctx context.Context, request OccurrenceQueryRequest, scope taskdomain.OccurrenceListScope) ([]taskdomain.QueryOccurrenceSnapshot, error) {
	if !validWorkspaceActor(request.WorkspaceID, request.ActorID) || !validOccurrenceScope(scope) {
		return nil, ErrInvalidRequest
	}
	runtime, err := facade.resolve(ctx, request.WorkspaceID)
	if err != nil {
		return nil, err
	}
	filter := taskdomain.OccurrenceListFilter{
		Scope: scope, From: request.From, To: request.To, Timezone: request.Timezone, ProjectID: request.ProjectID, TaskID: request.TaskID,
		Statuses: append([]taskdomain.ExecutionStatus(nil), request.Statuses...), Recurring: cloneBool(request.Recurring),
	}
	snapshots, err := runtime.Reader.ListOccurrences(ctx, filter)
	if err != nil {
		return nil, err
	}
	return cloneQueryOccurrences(snapshots), nil
}

func validOccurrenceScope(scope taskdomain.OccurrenceListScope) bool {
	switch scope {
	case taskdomain.OccurrenceListAll, taskdomain.OccurrenceListToday, taskdomain.OccurrenceListUpcoming, taskdomain.OccurrenceListOverdue,
		taskdomain.OccurrenceListUnscheduled, taskdomain.OccurrenceListCompleted, taskdomain.OccurrenceListCalendar:
		return true
	default:
		return false
	}
}

func projectFacadeOutcome(outcome ProjectCommandOutcome, commandID string, err error) (ProjectCommandOutcome, error) {
	if err != nil {
		return ProjectCommandOutcome{}, err
	}
	outcome.CommandID = commandID
	return outcome, nil
}

func (facade *Facade) resolve(ctx context.Context, workspaceID string) (RuntimeSnapshot, error) {
	if facade == nil || facade.runtimes == nil || facade.clock == nil || facade.ids == nil || facade.commandIDs == nil {
		return RuntimeSnapshot{}, ErrInvalidRuntime
	}
	runtime, err := facade.runtimes.Resolve(ctx, workspaceID)
	if err != nil {
		return RuntimeSnapshot{}, err
	}
	if runtime.WorkspaceID != workspaceID || runtime.Epoch < 1 || runtime.Factory == nil || runtime.Tasks == nil ||
		runtime.Occurrences == nil || runtime.Projects == nil || runtime.Schedules == nil || runtime.Reader == nil {
		return RuntimeSnapshot{}, ErrInvalidRuntime
	}
	return runtime, nil
}

func (facade *Facade) commandMetadata(ctx context.Context, actorID string) (CommandMetadata, error) {
	now, err := facade.now()
	if err != nil {
		return CommandMetadata{}, err
	}
	commandID, err := facade.newCommandID(ctx)
	if err != nil {
		return CommandMetadata{}, err
	}
	return CommandMetadata{ActorID: actorID, CommandID: commandID, At: now}, nil
}

func (facade *Facade) resolveAudit(ctx context.Context, workspaceID string) (RuntimeSnapshot, time.Time, string, error) {
	runtime, err := facade.resolve(ctx, workspaceID)
	if err != nil {
		return RuntimeSnapshot{}, time.Time{}, "", err
	}
	now, err := facade.now()
	if err != nil {
		return RuntimeSnapshot{}, time.Time{}, "", err
	}
	commandID, err := facade.newCommandID(ctx)
	if err != nil {
		return RuntimeSnapshot{}, time.Time{}, "", err
	}
	return runtime, now, commandID, nil
}

func (facade *Facade) now() (time.Time, error) {
	value := facade.clock.Now()
	if value.IsZero() {
		return time.Time{}, ErrInvalidRequest
	}
	return value, nil
}

func (facade *Facade) newEntityID(ctx context.Context) (string, error) {
	value, err := facade.ids.NewID(ctx)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(value) == "" {
		return "", ErrInvalidRequest
	}
	return value, nil
}

func (facade *Facade) newCommandID(ctx context.Context) (string, error) {
	value, err := facade.commandIDs.NewCommandID(ctx)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(value) == "" {
		return "", ErrInvalidRequest
	}
	return value, nil
}

func validWorkspaceActor(workspaceID, actorID string) bool {
	return strings.TrimSpace(workspaceID) != "" && strings.TrimSpace(actorID) != ""
}

func validWorkspaceActorEntity(workspaceID, actorID, entityID string) bool {
	return validWorkspaceActor(workspaceID, actorID) && strings.TrimSpace(entityID) != ""
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneOffsets(values map[string]int) map[string]int {
	if values == nil {
		return nil
	}
	result := make(map[string]int, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func cloneRevisions(values map[string]int64) map[string]int64 {
	result := make(map[string]int64, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func cloneBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneFloat64(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneProjectKind(value *taskdomain.ProjectKind) *taskdomain.ProjectKind {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneProjectHorizon(value *taskdomain.ProjectHorizon) *taskdomain.ProjectHorizon {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneProjectStatus(value *taskdomain.ProjectStatus) *taskdomain.ProjectStatus {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneTaskLifecycleStatus(value *taskdomain.TaskLifecycleStatus) *taskdomain.TaskLifecycleStatus {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneOccurrenceTiming(value taskdomain.OccurrenceTimingInput) taskdomain.OccurrenceTimingInput {
	result := value
	if value.SelectedOffsetSeconds != nil {
		offset := *value.SelectedOffsetSeconds
		result.SelectedOffsetSeconds = &offset
	}
	return result
}

func cloneQueryOccurrences(values []taskdomain.QueryOccurrenceSnapshot) []taskdomain.QueryOccurrenceSnapshot {
	result := make([]taskdomain.QueryOccurrenceSnapshot, len(values))
	for index, value := range values {
		result[index] = value
		result[index].PlannedStartAt = cloneTime(value.PlannedStartAt)
		result[index].PlannedEndAt = cloneTime(value.PlannedEndAt)
		result[index].DueAt = cloneTime(value.DueAt)
		result[index].ActualStartAt = cloneTime(value.ActualStartAt)
		result[index].CompletedAt = cloneTime(value.CompletedAt)
	}
	return result
}

func cloneOccurrenceRecords(values []taskdomain.OccurrenceRecord) []taskdomain.OccurrenceRecord {
	result := make([]taskdomain.OccurrenceRecord, len(values))
	for index, value := range values {
		result[index] = value
		result[index].PlannedStartAt = cloneTime(value.PlannedStartAt)
		result[index].PlannedEndAt = cloneTime(value.PlannedEndAt)
		result[index].DueAt = cloneTime(value.DueAt)
	}
	return result
}

func cloneTaskAggregateQueryResult(value taskdomain.TaskAggregateQueryResult) taskdomain.TaskAggregateQueryResult {
	result := value
	result.Aggregate.Occurrences = make([]taskdomain.Occurrence, len(value.Aggregate.Occurrences))
	for index, occurrence := range value.Aggregate.Occurrences {
		result.Aggregate.Occurrences[index] = occurrence
		result.Aggregate.Occurrences[index].ActualStartAt = cloneTime(occurrence.ActualStartAt)
		result.Aggregate.Occurrences[index].CompletedAt = cloneTime(occurrence.CompletedAt)
	}
	result.Versions = append([]taskdomain.ScheduleVersion(nil), value.Versions...)
	result.Occurrences = cloneQueryOccurrences(value.Occurrences)
	return result
}

func cloneLifecycleExpected(value taskdomain.LifecycleExpectedRevisions) taskdomain.LifecycleExpectedRevisions {
	result := value
	if value.Occurrences == nil {
		return result
	}
	result.Occurrences = make(map[string]int64, len(value.Occurrences))
	for occurrenceID, revision := range value.Occurrences {
		result.Occurrences[occurrenceID] = revision
	}
	return result
}

// Domain service adapters keep the runtime ports narrow while allowing the
// existing domain services to be wired without exposing them to handlers.
type DomainTaskServiceAdapter struct{ Service *taskdomain.TaskService }

func (adapter DomainTaskServiceAdapter) CreateTask(ctx context.Context, request taskdomain.CreateTaskRequest) (TaskCommandOutcome, error) {
	if adapter.Service == nil {
		return TaskCommandOutcome{}, ErrInvalidRuntime
	}
	result, err := adapter.Service.CreateTask(ctx, request)
	if err != nil {
		return TaskCommandOutcome{}, err
	}
	return taskOutcome(result), nil
}

func (adapter DomainTaskServiceAdapter) PatchTask(ctx context.Context, request taskdomain.PatchTaskRequest) (TaskCommandOutcome, error) {
	if adapter.Service == nil {
		return TaskCommandOutcome{}, ErrInvalidRuntime
	}
	result, err := adapter.Service.PatchTask(ctx, request)
	if err != nil {
		return TaskCommandOutcome{}, err
	}
	return taskOutcome(result), nil
}

func (adapter DomainTaskServiceAdapter) ExecuteLifecycleCommand(ctx context.Context, request taskdomain.LifecycleCommandRequest) (TaskCommandOutcome, error) {
	if adapter.Service == nil {
		return TaskCommandOutcome{}, ErrInvalidRuntime
	}
	result, err := adapter.Service.ExecuteLifecycleCommand(ctx, request)
	if err != nil {
		return TaskCommandOutcome{}, err
	}
	return taskOutcome(result), nil
}

func taskOutcome(result taskdomain.TaskCommandResult) TaskCommandOutcome {
	return TaskCommandOutcome{
		Task:         result.Task(),
		TaskRevision: result.TaskRevision(), ScheduleRevision: result.ScheduleRevision(),
		LifecycleStatus: result.LifecycleStatus(), OccurrenceRevisions: result.OccurrenceRevisions(), CommandID: result.Audit().CommandID(),
	}
}

type DomainOccurrenceServiceAdapter struct{ Service *taskdomain.OccurrenceService }

func (adapter DomainOccurrenceServiceAdapter) Execute(ctx context.Context, request taskdomain.OccurrenceCommandRequest) (OccurrenceCommandOutcome, error) {
	if adapter.Service == nil {
		return OccurrenceCommandOutcome{}, ErrInvalidRuntime
	}
	result, err := adapter.Service.Execute(ctx, request)
	if err != nil {
		return OccurrenceCommandOutcome{}, err
	}
	return OccurrenceCommandOutcome{
		TaskRevision: result.TaskRevision(), ScheduleRevision: result.ScheduleRevision(), OccurrenceRevision: result.OccurrenceRevision(),
		TaskLifecycleStatus: result.TaskLifecycleStatus(), ExecutionStatus: result.ExecutionStatus(), CommandID: result.Audit().CommandID(),
	}, nil
}

type DomainProjectServiceAdapter struct{ Service *taskdomain.ProjectService }

func (adapter DomainProjectServiceAdapter) CreateProject(ctx context.Context, request taskdomain.CreateProjectRequest) (ProjectCommandOutcome, error) {
	if adapter.Service == nil {
		return ProjectCommandOutcome{}, ErrInvalidRuntime
	}
	result, err := adapter.Service.CreateProject(ctx, request)
	return projectOutcome(result, err)
}

func (adapter DomainProjectServiceAdapter) UpdateProject(ctx context.Context, request taskdomain.UpdateProjectRequest) (ProjectCommandOutcome, error) {
	if adapter.Service == nil {
		return ProjectCommandOutcome{}, ErrInvalidRuntime
	}
	result, err := adapter.Service.UpdateProject(ctx, request)
	return projectOutcome(result, err)
}

func (adapter DomainProjectServiceAdapter) CompleteProject(ctx context.Context, request taskdomain.ExistingProjectRequest) (ProjectCommandOutcome, error) {
	if adapter.Service == nil {
		return ProjectCommandOutcome{}, ErrInvalidRuntime
	}
	result, err := adapter.Service.CompleteProject(ctx, request)
	return projectOutcome(result, err)
}

func (adapter DomainProjectServiceAdapter) ArchiveProject(ctx context.Context, request taskdomain.ExistingProjectRequest) (ProjectCommandOutcome, error) {
	if adapter.Service == nil {
		return ProjectCommandOutcome{}, ErrInvalidRuntime
	}
	result, err := adapter.Service.ArchiveProject(ctx, request)
	return projectOutcome(result, err)
}

func (adapter DomainProjectServiceAdapter) DeleteProject(ctx context.Context, request taskdomain.ExistingProjectRequest) (ProjectCommandOutcome, error) {
	if adapter.Service == nil {
		return ProjectCommandOutcome{}, ErrInvalidRuntime
	}
	result, err := adapter.Service.DeleteProject(ctx, request)
	return projectOutcome(result, err)
}

func projectOutcome(result taskdomain.ProjectCommandResult, err error) (ProjectCommandOutcome, error) {
	if err != nil {
		return ProjectCommandOutcome{}, err
	}
	return ProjectCommandOutcome{
		Project: result.Project(), Revision: result.Revision(), Deleted: result.Deleted(), CommandID: result.Audit().CommandID(),
	}, nil
}

type DomainScheduleServiceAdapter struct{ Service *taskdomain.ScheduleService }

func (adapter DomainScheduleServiceAdapter) RescheduleOccurrence(
	ctx context.Context,
	request taskdomain.RescheduleOccurrenceRequest,
	_ CommandMetadata,
) (ScheduleCommandOutcome, error) {
	if adapter.Service == nil {
		return ScheduleCommandOutcome{}, ErrInvalidRuntime
	}
	result, err := adapter.Service.RescheduleOccurrence(ctx, request)
	return scheduleOutcome(result, err)
}

func (adapter DomainScheduleServiceAdapter) RescheduleThisAndFollowing(
	ctx context.Context,
	request taskdomain.RescheduleThisAndFutureRequest,
	_ CommandMetadata,
) (ScheduleCommandOutcome, error) {
	if adapter.Service == nil {
		return ScheduleCommandOutcome{}, ErrInvalidRuntime
	}
	result, err := adapter.Service.RescheduleThisAndFuture(ctx, request)
	return scheduleOutcome(result, err)
}

func scheduleOutcome(result taskdomain.ScheduleCommandResult, err error) (ScheduleCommandOutcome, error) {
	outcome := ScheduleCommandOutcome{
		TaskRevision: result.TaskRevision(), ScheduleRevision: result.ScheduleRevision(), OccurrenceRevision: result.OccurrenceRevision(),
		ScheduleVersion: result.ScheduleVersion(), Candidates: result.Candidates(),
	}
	if err != nil {
		return outcome, err
	}
	return outcome, nil
}
