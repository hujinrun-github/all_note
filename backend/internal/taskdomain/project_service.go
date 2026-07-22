package taskdomain

import (
	"context"
	"errors"
	"strings"
	"time"
)

var (
	ErrInvalidProjectCommand   = errors.New("invalid project command")
	ErrProjectRevisionConflict = errors.New("project revision conflict")
)

type ProjectCommand string

const (
	ProjectCommandCreate   ProjectCommand = "create"
	ProjectCommandUpdate   ProjectCommand = "update"
	ProjectCommandComplete ProjectCommand = "complete"
	ProjectCommandArchive  ProjectCommand = "archive"
	ProjectCommandDelete   ProjectCommand = "delete"
)

// ProjectCommandTx is the complete project command-side transaction surface.
// Reads and writes must be provided by the same fenced database transaction.
type ProjectCommandTx interface {
	GetProject(context.Context, string) (ProjectSnapshot, error)
	CountNonTerminalProjectOccurrences(context.Context, string) (int, error)
	ProjectWriter() ProjectWriter
}

type ProjectCommandFencer interface {
	BeginFencedProjectWrite(context.Context, string, int64, func(ProjectCommandTx) error) error
}

type ProjectService struct {
	fencer ProjectCommandFencer
}

func NewProjectService(fencer ProjectCommandFencer) *ProjectService {
	return &ProjectService{fencer: fencer}
}

type CreateProjectRequest struct {
	WorkspaceID             string
	ExpectedRuntimeEpoch    int64
	ExpectedProjectRevision int64
	Project                 Project
	CommandID               string
	ActorID                 string
	At                      time.Time
}

type UpdateProjectRequest struct {
	WorkspaceID             string
	ProjectID               string
	ExpectedRuntimeEpoch    int64
	ExpectedProjectRevision int64
	Project                 Project
	CommandID               string
	ActorID                 string
	At                      time.Time
}

type ExistingProjectRequest struct {
	WorkspaceID             string
	ProjectID               string
	Command                 ProjectCommand
	ExpectedRuntimeEpoch    int64
	ExpectedProjectRevision int64
	CommandID               string
	ActorID                 string
	At                      time.Time
}

type ProjectCommandAudit struct {
	commandID string
	command   ProjectCommand
	projectID string
	actorID   string
	createdAt time.Time
}

func (audit ProjectCommandAudit) CommandID() string       { return audit.commandID }
func (audit ProjectCommandAudit) Command() ProjectCommand { return audit.command }
func (audit ProjectCommandAudit) ProjectID() string       { return audit.projectID }
func (audit ProjectCommandAudit) ActorID() string         { return audit.actorID }
func (audit ProjectCommandAudit) CreatedAt() time.Time    { return audit.createdAt }

type ProjectCommandResult struct {
	project  Project
	revision int64
	deleted  bool
	audit    ProjectCommandAudit
}

func (result ProjectCommandResult) Project() Project           { return result.project }
func (result ProjectCommandResult) Revision() int64            { return result.revision }
func (result ProjectCommandResult) Deleted() bool              { return result.deleted }
func (result ProjectCommandResult) Audit() ProjectCommandAudit { return result.audit }
func (result ProjectCommandResult) IsZero() bool {
	return result.project == (Project{}) && result.revision == 0 && !result.deleted && result.audit == (ProjectCommandAudit{})
}

func (service *ProjectService) CreateProject(ctx context.Context, request CreateProjectRequest) (ProjectCommandResult, error) {
	project, err := validateCreateProjectRequest(service, request)
	if err != nil {
		return ProjectCommandResult{}, err
	}

	var result ProjectCommandResult
	err = service.fencer.BeginFencedProjectWrite(ctx, request.WorkspaceID, request.ExpectedRuntimeEpoch, func(tx ProjectCommandTx) error {
		writer, err := projectCommandWriter(tx)
		if err != nil {
			return err
		}
		if err := writer.SaveProject(ctx, ProjectWrite{Project: project, ExpectedRevision: 0}); err != nil {
			return err
		}
		result = newProjectCommandResult(project, 1, false, ProjectCommandCreate, request.CommandID, request.ActorID, request.At)
		return nil
	})
	if err != nil {
		return ProjectCommandResult{}, err
	}
	return result, nil
}

func (service *ProjectService) UpdateProject(ctx context.Context, request UpdateProjectRequest) (ProjectCommandResult, error) {
	if err := validateUpdateProjectRequest(service, request); err != nil {
		return ProjectCommandResult{}, err
	}

	var result ProjectCommandResult
	err := service.fencer.BeginFencedProjectWrite(ctx, request.WorkspaceID, request.ExpectedRuntimeEpoch, func(tx ProjectCommandTx) error {
		writer, err := projectCommandWriter(tx)
		if err != nil {
			return err
		}
		current, err := readExpectedProject(ctx, tx, request.WorkspaceID, request.ProjectID, request.ExpectedProjectRevision)
		if err != nil {
			return err
		}
		if current.Project.SystemRole != request.Project.SystemRole {
			return ErrSystemProjectImmutable
		}
		if current.Project.Status != request.Project.Status &&
			(request.Project.Status == ProjectStatusCompleted || request.Project.Status == ProjectStatusArchived) {
			return ErrInvalidProjectCommand
		}
		if err := writer.SaveProject(ctx, ProjectWrite{Project: request.Project, ExpectedRevision: request.ExpectedProjectRevision}); err != nil {
			return err
		}
		result = newProjectCommandResult(
			request.Project, request.ExpectedProjectRevision+1, false, ProjectCommandUpdate,
			request.CommandID, request.ActorID, request.At,
		)
		return nil
	})
	if err != nil {
		return ProjectCommandResult{}, err
	}
	return result, nil
}

func (service *ProjectService) CompleteProject(ctx context.Context, request ExistingProjectRequest) (ProjectCommandResult, error) {
	if err := validateExistingProjectRequest(service, request, ProjectCommandComplete); err != nil {
		return ProjectCommandResult{}, err
	}
	return service.executeExistingProjectCommand(ctx, request, func(ctx context.Context, tx ProjectCommandTx, current Project) (Project, bool, error) {
		count, err := tx.CountNonTerminalProjectOccurrences(ctx, request.ProjectID)
		if err != nil {
			return current, false, err
		}
		next, err := CompleteProject(current, count)
		return next, false, err
	})
}

func (service *ProjectService) ArchiveProject(ctx context.Context, request ExistingProjectRequest) (ProjectCommandResult, error) {
	if err := validateExistingProjectRequest(service, request, ProjectCommandArchive); err != nil {
		return ProjectCommandResult{}, err
	}
	return service.executeExistingProjectCommand(ctx, request, func(_ context.Context, _ ProjectCommandTx, current Project) (Project, bool, error) {
		current.Status = ProjectStatusArchived
		if err := ValidateProject(current); err != nil {
			return Project{}, false, err
		}
		return current, false, nil
	})
}

func (service *ProjectService) DeleteProject(ctx context.Context, request ExistingProjectRequest) (ProjectCommandResult, error) {
	if err := validateExistingProjectRequest(service, request, ProjectCommandDelete); err != nil {
		return ProjectCommandResult{}, err
	}
	return service.executeExistingProjectCommand(ctx, request, func(_ context.Context, _ ProjectCommandTx, current Project) (Project, bool, error) {
		if err := ValidateProjectDeletion(current); err != nil {
			return Project{}, false, err
		}
		return current, true, nil
	})
}

type existingProjectTransition func(context.Context, ProjectCommandTx, Project) (Project, bool, error)

func (service *ProjectService) executeExistingProjectCommand(
	ctx context.Context,
	request ExistingProjectRequest,
	transition existingProjectTransition,
) (ProjectCommandResult, error) {
	var result ProjectCommandResult
	err := service.fencer.BeginFencedProjectWrite(ctx, request.WorkspaceID, request.ExpectedRuntimeEpoch, func(tx ProjectCommandTx) error {
		writer, err := projectCommandWriter(tx)
		if err != nil {
			return err
		}
		current, err := readExpectedProject(ctx, tx, request.WorkspaceID, request.ProjectID, request.ExpectedProjectRevision)
		if err != nil {
			return err
		}
		next, deleting, err := transition(ctx, tx, current.Project)
		if err != nil {
			return err
		}
		revision := request.ExpectedProjectRevision + 1
		if deleting {
			if err := writer.DeleteProject(ctx, request.ProjectID, request.ExpectedProjectRevision); err != nil {
				return err
			}
			revision = request.ExpectedProjectRevision
		} else {
			if err := writer.SaveProject(ctx, ProjectWrite{Project: next, ExpectedRevision: request.ExpectedProjectRevision}); err != nil {
				return err
			}
		}
		result = newProjectCommandResult(next, revision, deleting, request.Command, request.CommandID, request.ActorID, request.At)
		return nil
	})
	if err != nil {
		return ProjectCommandResult{}, err
	}
	return result, nil
}

func readExpectedProject(ctx context.Context, tx ProjectCommandTx, workspaceID, projectID string, expectedRevision int64) (ProjectSnapshot, error) {
	if tx == nil {
		return ProjectSnapshot{}, ErrInvalidProjectCommand
	}
	snapshot, err := tx.GetProject(ctx, projectID)
	if err != nil {
		return ProjectSnapshot{}, err
	}
	if snapshot.Project.WorkspaceID != workspaceID || snapshot.Project.ID != projectID {
		return ProjectSnapshot{}, ErrInvalidProjectCommand
	}
	if snapshot.Revision != expectedRevision {
		return ProjectSnapshot{}, ErrProjectRevisionConflict
	}
	if err := ValidateProject(snapshot.Project); err != nil {
		return ProjectSnapshot{}, err
	}
	return snapshot, nil
}

func validateCreateProjectRequest(service *ProjectService, request CreateProjectRequest) (Project, error) {
	if service == nil || service.fencer == nil || strings.TrimSpace(request.WorkspaceID) == "" ||
		request.ExpectedRuntimeEpoch < 1 || request.Project.WorkspaceID != request.WorkspaceID ||
		!validCommandAudit(request.CommandID, request.ActorID, request.At) {
		return Project{}, ErrInvalidProjectCommand
	}
	if request.ExpectedProjectRevision != 0 {
		return Project{}, ErrProjectRevisionConflict
	}
	project := request.Project
	project.SystemRole = ProjectSystemRoleNone
	if err := ValidateProject(project); err != nil {
		return Project{}, err
	}
	return project, nil
}

func validateUpdateProjectRequest(service *ProjectService, request UpdateProjectRequest) error {
	if service == nil || service.fencer == nil || strings.TrimSpace(request.WorkspaceID) == "" ||
		strings.TrimSpace(request.ProjectID) == "" || request.ExpectedRuntimeEpoch < 1 || request.ExpectedProjectRevision < 1 ||
		!validCommandAudit(request.CommandID, request.ActorID, request.At) {
		return ErrInvalidProjectCommand
	}
	if request.Project.WorkspaceID != request.WorkspaceID || request.Project.ID != request.ProjectID {
		return ErrInvalidProject
	}
	return ValidateProject(request.Project)
}

func validateExistingProjectRequest(service *ProjectService, request ExistingProjectRequest, command ProjectCommand) error {
	if service == nil || service.fencer == nil || request.Command != command ||
		strings.TrimSpace(request.WorkspaceID) == "" || strings.TrimSpace(request.ProjectID) == "" ||
		request.ExpectedRuntimeEpoch < 1 || request.ExpectedProjectRevision < 1 ||
		!validCommandAudit(request.CommandID, request.ActorID, request.At) {
		return ErrInvalidProjectCommand
	}
	return nil
}

func projectCommandWriter(tx ProjectCommandTx) (ProjectWriter, error) {
	if tx == nil {
		return nil, ErrInvalidProjectCommand
	}
	writer := tx.ProjectWriter()
	if writer == nil {
		return nil, ErrInvalidProjectCommand
	}
	return writer, nil
}

func newProjectCommandResult(
	project Project,
	revision int64,
	deleted bool,
	command ProjectCommand,
	commandID string,
	actorID string,
	at time.Time,
) ProjectCommandResult {
	return ProjectCommandResult{
		project: project, revision: revision, deleted: deleted,
		audit: ProjectCommandAudit{
			commandID: commandID, command: command, projectID: project.ID,
			actorID: actorID, createdAt: at,
		},
	}
}
