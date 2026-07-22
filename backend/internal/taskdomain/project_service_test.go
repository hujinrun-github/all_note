package taskdomain

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestProjectServiceCreateDefaultsSystemRoleAndWritesOnceInsideFence(t *testing.T) {
	writer := &projectServiceWriter{}
	fencer := &projectServiceFencer{writer: writer}
	reader := &projectServiceReader{fencer: fencer}
	service := NewProjectService(fencer.bind(reader))
	at := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)
	request := CreateProjectRequest{
		WorkspaceID: "workspace-1", ExpectedRuntimeEpoch: 7, ExpectedProjectRevision: 0,
		Project: Project{
			WorkspaceID: "workspace-1", ID: "project-1", Name: "Learn Japanese",
			Kind: ProjectKindLearning, Horizon: ProjectHorizonLong, Status: ProjectStatusPlanning,
			SystemRole: ProjectSystemRoleInbox,
		},
		CommandID: "command-create", ActorID: "user-1", At: at,
	}

	result, err := service.CreateProject(context.Background(), request)
	if err != nil {
		t.Fatalf("CreateProject() error = %v", err)
	}
	if fencer.calls != 1 || fencer.workspaceID != "workspace-1" || fencer.expectedEpoch != 7 {
		t.Fatalf("fence calls/workspace/epoch = %d/%q/%d", fencer.calls, fencer.workspaceID, fencer.expectedEpoch)
	}
	if writer.saveCalls != 1 || writer.deleteCalls != 0 {
		t.Fatalf("writer calls save/delete = %d/%d", writer.saveCalls, writer.deleteCalls)
	}
	if writer.saved.ExpectedRevision != 0 || writer.saved.Project.SystemRole != ProjectSystemRoleNone {
		t.Fatalf("create write = %#v", writer.saved)
	}
	if result.Revision() != 1 || result.Project().SystemRole != ProjectSystemRoleNone || result.Deleted() {
		t.Fatalf("create result = %#v", result)
	}
	assertProjectAudit(t, result, ProjectCommandCreate, "command-create", "project-1", at)

	copy := result.Project()
	copy.Name = "mutated by caller"
	if result.Project().Name != "Learn Japanese" {
		t.Fatal("result project was externally mutable")
	}
}

func TestProjectServiceUpdateProtectsIdentityAndSystemRole(t *testing.T) {
	current := Project{
		WorkspaceID: "workspace-1", ID: "project-1", Name: "Old",
		Kind: ProjectKindStandard, Horizon: ProjectHorizonShort, Status: ProjectStatusPlanning,
		SystemRole: ProjectSystemRoleNone,
	}
	updated := current
	updated.Name = "New"
	updated.Kind = ProjectKindLearning
	updated.Horizon = ProjectHorizonLong
	updated.Status = ProjectStatusActive

	writer := &projectServiceWriter{}
	fencer := &projectServiceFencer{writer: writer}
	reader := &projectServiceReader{fencer: fencer, snapshot: ProjectSnapshot{Project: current, Revision: 4}}
	service := NewProjectService(fencer.bind(reader))
	result, err := service.UpdateProject(context.Background(), UpdateProjectRequest{
		WorkspaceID: "workspace-1", ProjectID: "project-1", ExpectedRuntimeEpoch: 8, ExpectedProjectRevision: 4,
		Project: updated, CommandID: "command-update", ActorID: "user-1", At: testProjectCommandTime(),
	})
	if err != nil {
		t.Fatalf("UpdateProject() error = %v", err)
	}
	if reader.getCalls != 1 || !reader.readInsideFence || writer.saveCalls != 1 || writer.saved.ExpectedRevision != 4 {
		t.Fatalf("update read/write boundary = reader:%#v writer:%#v", reader, writer)
	}
	if writer.saved.Project != updated || result.Project() != updated || result.Revision() != 5 {
		t.Fatalf("update write/result = %#v / %#v", writer.saved, result)
	}

	tests := []struct {
		name   string
		mutate func(*Project)
		want   error
	}{
		{name: "workspace", mutate: func(project *Project) { project.WorkspaceID = "workspace-2" }, want: ErrInvalidProject},
		{name: "id", mutate: func(project *Project) { project.ID = "project-2" }, want: ErrInvalidProject},
		{name: "system role", mutate: func(project *Project) { project.SystemRole = ProjectSystemRoleInbox }, want: ErrSystemProjectImmutable},
		{name: "complete status bypass", mutate: func(project *Project) { project.Status = ProjectStatusCompleted }, want: ErrInvalidProjectCommand},
		{name: "archive status bypass", mutate: func(project *Project) { project.Status = ProjectStatusArchived }, want: ErrInvalidProjectCommand},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := current
			tt.mutate(&candidate)
			writer := &projectServiceWriter{}
			fencer := &projectServiceFencer{writer: writer}
			reader := &projectServiceReader{fencer: fencer, snapshot: ProjectSnapshot{Project: current, Revision: 4}}
			result, err := NewProjectService(fencer.bind(reader)).UpdateProject(context.Background(), UpdateProjectRequest{
				WorkspaceID: "workspace-1", ProjectID: "project-1", ExpectedRuntimeEpoch: 8, ExpectedProjectRevision: 4,
				Project: candidate, CommandID: "command-update", ActorID: "user-1", At: testProjectCommandTime(),
			})
			if !errors.Is(err, tt.want) || !result.IsZero() || writer.saveCalls != 0 || writer.deleteCalls != 0 {
				t.Fatalf("result/error/writes = %#v / %v / %d,%d", result, err, writer.saveCalls, writer.deleteCalls)
			}
		})
	}
}

func TestProjectServiceCompleteChecksOpenOccurrencesInsideSameFence(t *testing.T) {
	current := ordinaryProject(ProjectStatusActive)
	tests := []struct {
		name       string
		openCount  int
		wantStatus ProjectStatus
		wantErr    error
		wantWrites int
	}{
		{name: "complete", openCount: 0, wantStatus: ProjectStatusCompleted, wantWrites: 1},
		{name: "blocked", openCount: 2, wantErr: ErrProjectHasOpenOccurrences},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writer := &projectServiceWriter{}
			fencer := &projectServiceFencer{writer: writer}
			reader := &projectServiceReader{
				fencer: fencer, snapshot: ProjectSnapshot{Project: current, Revision: 6}, nonTerminalCount: tt.openCount,
			}
			result, err := NewProjectService(fencer.bind(reader)).CompleteProject(context.Background(), existingProjectRequest(ProjectCommandComplete, 6))
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("CompleteProject() error = %v, want %v", err, tt.wantErr)
			}
			if reader.getCalls != 1 || reader.countCalls != 1 || !reader.readInsideFence || !reader.countInsideFence {
				t.Fatalf("reads were not in one fence: %#v", reader)
			}
			if writer.saveCalls != tt.wantWrites || writer.deleteCalls != 0 {
				t.Fatalf("writer calls = save:%d delete:%d", writer.saveCalls, writer.deleteCalls)
			}
			if tt.wantErr != nil {
				if !result.IsZero() {
					t.Fatalf("failure result = %#v", result)
				}
				return
			}
			if writer.saved.Project.Status != tt.wantStatus || writer.saved.ExpectedRevision != 6 || result.Revision() != 7 {
				t.Fatalf("completed write/result = %#v / %#v", writer.saved, result)
			}
		})
	}
}

func TestProjectServiceArchiveAndDeleteAreExplicitCommands(t *testing.T) {
	current := ordinaryProject(ProjectStatusCompleted)

	archiveWriter := &projectServiceWriter{}
	archiveFencer := &projectServiceFencer{writer: archiveWriter}
	archiveReader := &projectServiceReader{fencer: archiveFencer, snapshot: ProjectSnapshot{Project: current, Revision: 3}}
	archived, err := NewProjectService(archiveFencer.bind(archiveReader)).ArchiveProject(context.Background(), existingProjectRequest(ProjectCommandArchive, 3))
	if err != nil {
		t.Fatalf("ArchiveProject() error = %v", err)
	}
	if archiveWriter.saveCalls != 1 || archiveWriter.saved.Project.Status != ProjectStatusArchived || archiveWriter.saved.ExpectedRevision != 3 || archived.Revision() != 4 {
		t.Fatalf("archive write/result = %#v / %#v", archiveWriter.saved, archived)
	}

	deleteWriter := &projectServiceWriter{}
	deleteFencer := &projectServiceFencer{writer: deleteWriter}
	deleteReader := &projectServiceReader{fencer: deleteFencer, snapshot: ProjectSnapshot{Project: current, Revision: 3}}
	deleted, err := NewProjectService(deleteFencer.bind(deleteReader)).DeleteProject(context.Background(), existingProjectRequest(ProjectCommandDelete, 3))
	if err != nil {
		t.Fatalf("DeleteProject() error = %v", err)
	}
	if deleteWriter.deleteCalls != 1 || deleteWriter.saveCalls != 0 || deleteWriter.deletedID != current.ID || deleteWriter.deleteExpectedRevision != 3 {
		t.Fatalf("delete calls = %#v", deleteWriter)
	}
	if !deleted.Deleted() || deleted.Revision() != 3 || deleted.Project() != current {
		t.Fatalf("delete result = %#v", deleted)
	}
	assertProjectAudit(t, deleted, ProjectCommandDelete, "command-delete", "project-1", testProjectCommandTime())
}

func TestProjectServiceNeverDeletesSystemProjects(t *testing.T) {
	for _, role := range []ProjectSystemRole{ProjectSystemRoleInbox, ProjectSystemRolePersonal} {
		t.Run(string(role), func(t *testing.T) {
			current := ordinaryProject(ProjectStatusActive)
			current.SystemRole = role
			writer := &projectServiceWriter{}
			fencer := &projectServiceFencer{writer: writer}
			reader := &projectServiceReader{fencer: fencer, snapshot: ProjectSnapshot{Project: current, Revision: 2}}
			result, err := NewProjectService(fencer.bind(reader)).DeleteProject(context.Background(), existingProjectRequest(ProjectCommandDelete, 2))
			if !errors.Is(err, ErrSystemProjectImmutable) || !result.IsZero() {
				t.Fatalf("result/error = %#v / %v", result, err)
			}
			if writer.saveCalls != 0 || writer.deleteCalls != 0 {
				t.Fatalf("system deletion wrote: %#v", writer)
			}
		})
	}
}

func TestProjectServiceEveryCommandValidatesEpochAndRevision(t *testing.T) {
	current := ordinaryProject(ProjectStatusActive)
	tests := []struct {
		name    string
		execute func(*ProjectService) (ProjectCommandResult, error)
	}{
		{name: "update", execute: func(service *ProjectService) (ProjectCommandResult, error) {
			return service.UpdateProject(context.Background(), UpdateProjectRequest{
				WorkspaceID: "workspace-1", ProjectID: "project-1", ExpectedRuntimeEpoch: 9, ExpectedProjectRevision: 8,
				Project: current, CommandID: "command-update", ActorID: "user-1", At: testProjectCommandTime(),
			})
		}},
		{name: "complete", execute: func(service *ProjectService) (ProjectCommandResult, error) {
			request := existingProjectRequest(ProjectCommandComplete, 8)
			return service.CompleteProject(context.Background(), request)
		}},
		{name: "archive", execute: func(service *ProjectService) (ProjectCommandResult, error) {
			request := existingProjectRequest(ProjectCommandArchive, 8)
			return service.ArchiveProject(context.Background(), request)
		}},
		{name: "delete", execute: func(service *ProjectService) (ProjectCommandResult, error) {
			request := existingProjectRequest(ProjectCommandDelete, 8)
			return service.DeleteProject(context.Background(), request)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name+" revision", func(t *testing.T) {
			writer := &projectServiceWriter{}
			fencer := &projectServiceFencer{writer: writer}
			reader := &projectServiceReader{fencer: fencer, snapshot: ProjectSnapshot{Project: current, Revision: 7}}
			result, err := tt.execute(NewProjectService(fencer.bind(reader)))
			if !errors.Is(err, ErrProjectRevisionConflict) || !result.IsZero() || writer.saveCalls != 0 || writer.deleteCalls != 0 {
				t.Fatalf("result/error/writes = %#v / %v / %#v", result, err, writer)
			}
		})
		t.Run(tt.name+" epoch", func(t *testing.T) {
			writer := &projectServiceWriter{}
			fencer := &projectServiceFencer{writer: writer, err: ErrTaskRuntimeEpochConflict}
			reader := &projectServiceReader{fencer: fencer, snapshot: ProjectSnapshot{Project: current, Revision: 8}}
			result, err := tt.execute(NewProjectService(fencer.bind(reader)))
			if !errors.Is(err, ErrTaskRuntimeEpochConflict) || !result.IsZero() || reader.getCalls != 0 || writer.saveCalls != 0 || writer.deleteCalls != 0 {
				t.Fatalf("result/error/read/write = %#v / %v / %d / %#v", result, err, reader.getCalls, writer)
			}
		})
	}

	writer := &projectServiceWriter{}
	fencer := &projectServiceFencer{writer: writer}
	result, err := NewProjectService(fencer).CreateProject(context.Background(), CreateProjectRequest{
		WorkspaceID: "workspace-1", ExpectedRuntimeEpoch: 9, ExpectedProjectRevision: 1,
		Project: ordinaryProject(ProjectStatusPlanning), CommandID: "command-create", ActorID: "user-1", At: testProjectCommandTime(),
	})
	if !errors.Is(err, ErrProjectRevisionConflict) || !result.IsZero() || fencer.calls != 0 || writer.saveCalls != 0 {
		t.Fatalf("create revision result/error/fence/write = %#v / %v / %d / %d", result, err, fencer.calls, writer.saveCalls)
	}

	writer = &projectServiceWriter{}
	fencer = &projectServiceFencer{writer: writer, err: ErrTaskRuntimeEpochConflict}
	result, err = NewProjectService(fencer).CreateProject(context.Background(), CreateProjectRequest{
		WorkspaceID: "workspace-1", ExpectedRuntimeEpoch: 9, ExpectedProjectRevision: 0,
		Project: ordinaryProject(ProjectStatusPlanning), CommandID: "command-create", ActorID: "user-1", At: testProjectCommandTime(),
	})
	if !errors.Is(err, ErrTaskRuntimeEpochConflict) || !result.IsZero() || fencer.calls != 1 || writer.saveCalls != 0 {
		t.Fatalf("create epoch result/error/fence/write = %#v / %v / %d / %d", result, err, fencer.calls, writer.saveCalls)
	}
}

func TestProjectServiceInvalidInputAndWriterFailureReturnZeroResult(t *testing.T) {
	invalid := ordinaryProject(ProjectStatusPlanning)
	invalid.Kind = "unsupported"
	writer := &projectServiceWriter{}
	fencer := &projectServiceFencer{writer: writer}
	result, err := NewProjectService(fencer).CreateProject(context.Background(), CreateProjectRequest{
		WorkspaceID: "workspace-1", ExpectedRuntimeEpoch: 1, Project: invalid,
		CommandID: "command-create", ActorID: "user-1", At: testProjectCommandTime(),
	})
	if !errors.Is(err, ErrInvalidProject) || !result.IsZero() || fencer.calls != 0 || writer.saveCalls != 0 {
		t.Fatalf("invalid create result/error/fence/write = %#v / %v / %d / %d", result, err, fencer.calls, writer.saveCalls)
	}

	current := ordinaryProject(ProjectStatusActive)
	writer = &projectServiceWriter{saveErr: errors.New("save failed")}
	fencer = &projectServiceFencer{writer: writer}
	reader := &projectServiceReader{fencer: fencer, snapshot: ProjectSnapshot{Project: current, Revision: 2}}
	result, err = NewProjectService(fencer.bind(reader)).ArchiveProject(context.Background(), existingProjectRequest(ProjectCommandArchive, 2))
	if err == nil || !result.IsZero() || writer.saveCalls != 1 {
		t.Fatalf("writer failure result/error/writes = %#v / %v / %d", result, err, writer.saveCalls)
	}
}

func ordinaryProject(status ProjectStatus) Project {
	return Project{
		WorkspaceID: "workspace-1", ID: "project-1", Name: "Project",
		Kind: ProjectKindStandard, Horizon: ProjectHorizonShort, Status: status,
		SystemRole: ProjectSystemRoleNone,
	}
}

func existingProjectRequest(command ProjectCommand, revision int64) ExistingProjectRequest {
	return ExistingProjectRequest{
		WorkspaceID: "workspace-1", ProjectID: "project-1", Command: command,
		ExpectedRuntimeEpoch: 9, ExpectedProjectRevision: revision,
		CommandID: "command-" + string(command), ActorID: "user-1", At: testProjectCommandTime(),
	}
}

func testProjectCommandTime() time.Time {
	return time.Date(2026, 7, 22, 10, 30, 0, 0, time.UTC)
}

func assertProjectAudit(t *testing.T, result ProjectCommandResult, command ProjectCommand, commandID, projectID string, at time.Time) {
	t.Helper()
	audit := result.Audit()
	if audit.Command() != command || audit.CommandID() != commandID || audit.ProjectID() != projectID ||
		audit.ActorID() != "user-1" || !audit.CreatedAt().Equal(at) {
		t.Fatalf("audit = %#v", audit)
	}
}

type projectServiceFencer struct {
	writer        *projectServiceWriter
	reader        *projectServiceReader
	err           error
	calls         int
	workspaceID   string
	expectedEpoch int64
	inside        bool
}

func (fencer *projectServiceFencer) bind(reader *projectServiceReader) *projectServiceFencer {
	fencer.reader = reader
	return fencer
}

func (fencer *projectServiceFencer) BeginFencedProjectWrite(_ context.Context, workspaceID string, expectedEpoch int64, callback func(ProjectCommandTx) error) error {
	fencer.calls++
	fencer.workspaceID = workspaceID
	fencer.expectedEpoch = expectedEpoch
	if fencer.err != nil {
		return fencer.err
	}
	fencer.inside = true
	err := callback(projectServiceTx{writer: fencer.writer, reader: fencer.reader})
	fencer.inside = false
	return err
}

type projectServiceTx struct {
	writer *projectServiceWriter
	reader *projectServiceReader
}

func (tx projectServiceTx) ProjectWriter() ProjectWriter { return tx.writer }

func (tx projectServiceTx) GetProject(ctx context.Context, id string) (ProjectSnapshot, error) {
	if tx.reader == nil {
		return ProjectSnapshot{}, errors.New("unexpected GetProject")
	}
	return tx.reader.GetProject(ctx, id)
}

func (tx projectServiceTx) CountNonTerminalProjectOccurrences(ctx context.Context, id string) (int, error) {
	if tx.reader == nil {
		return 0, errors.New("unexpected CountNonTerminalProjectOccurrences")
	}
	return tx.reader.CountNonTerminalProjectOccurrences(ctx, id)
}

type projectServiceReader struct {
	fencer           *projectServiceFencer
	snapshot         ProjectSnapshot
	getErr           error
	countErr         error
	nonTerminalCount int
	getCalls         int
	countCalls       int
	readInsideFence  bool
	countInsideFence bool
}

func (reader *projectServiceReader) GetProject(_ context.Context, _ string) (ProjectSnapshot, error) {
	reader.getCalls++
	reader.readInsideFence = reader.fencer != nil && reader.fencer.inside
	return reader.snapshot, reader.getErr
}

func (reader *projectServiceReader) CountNonTerminalProjectOccurrences(_ context.Context, _ string) (int, error) {
	reader.countCalls++
	reader.countInsideFence = reader.fencer != nil && reader.fencer.inside
	return reader.nonTerminalCount, reader.countErr
}

type projectServiceWriter struct {
	saveCalls              int
	deleteCalls            int
	saved                  ProjectWrite
	deletedID              string
	deleteExpectedRevision int64
	saveErr                error
	deleteErr              error
}

func (writer *projectServiceWriter) EnsureSystemProjects(context.Context) error { return nil }

func (writer *projectServiceWriter) SaveProject(_ context.Context, write ProjectWrite) error {
	writer.saveCalls++
	writer.saved = write
	return writer.saveErr
}

func (writer *projectServiceWriter) DeleteProject(_ context.Context, id string, expectedRevision int64) error {
	writer.deleteCalls++
	writer.deletedID = id
	writer.deleteExpectedRevision = expectedRevision
	return writer.deleteErr
}

func (writer *projectServiceWriter) CreateTaskAggregate(context.Context, TaskAggregateSnapshot) error {
	return errors.New("unexpected CreateTaskAggregate")
}

func (writer *projectServiceWriter) SaveTaskAggregate(context.Context, TaskAggregateWrite) error {
	return errors.New("unexpected SaveTaskAggregate")
}

func (writer *projectServiceWriter) InstallScheduleVersion(context.Context, ScheduleVersionInstall) error {
	return errors.New("unexpected InstallScheduleVersion")
}

func TestProjectCommandResultZeroValueIsStable(t *testing.T) {
	if !(ProjectCommandResult{}).IsZero() || !reflect.DeepEqual((ProjectCommandResult{}).Project(), Project{}) {
		t.Fatal("zero project command result is not stable")
	}
}
