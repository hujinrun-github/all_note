package contracttest

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/taskdomain"
)

type TaskDomainV2ProjectCommandFixture struct {
	DB      *sql.DB
	Dialect TaskDomainV2Dialect
	Writer  storage.TenantFencedWriter
	Fencer  taskdomain.ProjectCommandFencer
	Reader  func(workspaceID string) taskdomain.ProjectReader
}

func RunTaskDomainV2ProjectCommandSuite(t *testing.T, fixture TaskDomainV2ProjectCommandFixture) {
	t.Helper()
	ctx := context.Background()
	const workspaceID = "project-command-w1"
	mustExec(t, fixture.DB, `INSERT INTO tenant_workspaces(workspace_id) VALUES('project-command-w1')`)
	if err := fixture.Writer.BeginFencedWrite(ctx, workspaceID, 1, func(tx storage.TenantWriteTx) error {
		return tx.TaskDomainWriter().EnsureSystemProjects(ctx)
	}); err != nil {
		t.Fatal(err)
	}
	service := taskdomain.NewProjectService(fixture.Fencer)
	at := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)

	project := taskdomain.Project{
		WorkspaceID: workspaceID, ID: "command-project", Name: "Command project",
		Kind: taskdomain.ProjectKindStandard, Horizon: taskdomain.ProjectHorizonShort,
		Status: taskdomain.ProjectStatusPlanning, SystemRole: taskdomain.ProjectSystemRoleNone,
	}
	created, err := service.CreateProject(ctx, taskdomain.CreateProjectRequest{
		WorkspaceID: workspaceID, ExpectedRuntimeEpoch: 1, Project: project,
		CommandID: "create-project", ActorID: "user-1", At: at,
	})
	if err != nil || created.Revision() != 1 {
		t.Fatalf("create result=%#v err=%v", created, err)
	}
	project.Name = "Updated command project"
	project.Status = taskdomain.ProjectStatusActive
	updated, err := service.UpdateProject(ctx, taskdomain.UpdateProjectRequest{
		WorkspaceID: workspaceID, ProjectID: project.ID, ExpectedRuntimeEpoch: 1, ExpectedProjectRevision: 1,
		Project: project, CommandID: "update-project", ActorID: "user-1", At: at,
	})
	if err != nil || updated.Revision() != 2 || updated.Project().Name != project.Name {
		t.Fatalf("update result=%#v err=%v", updated, err)
	}

	for _, status := range []taskdomain.ExecutionStatus{
		taskdomain.ExecutionStatusOpen, taskdomain.ExecutionStatusActive, taskdomain.ExecutionStatusBlocked,
		taskdomain.ExecutionStatusDone, taskdomain.ExecutionStatusSkipped, taskdomain.ExecutionStatusCancelled,
	} {
		taskID := "count-" + string(status)
		snapshot := queryUnscheduledAggregate(workspaceID, taskID, project.ID)
		if err := fixture.Writer.BeginFencedWrite(ctx, workspaceID, 1, func(tx storage.TenantWriteTx) error {
			return tx.TaskDomainWriter().CreateTaskAggregate(ctx, snapshot)
		}); err != nil {
			t.Fatalf("create count fixture %s: %v", status, err)
		}
		setProjectCommandOccurrenceStatus(t, fixture, taskID+"-occ", status)
	}

	var count int
	if err := fixture.Fencer.BeginFencedProjectWrite(ctx, workspaceID, 1, func(tx taskdomain.ProjectCommandTx) error {
		var err error
		count, err = tx.CountNonTerminalProjectOccurrences(ctx, project.ID)
		return err
	}); err != nil || count != 3 {
		t.Fatalf("non-terminal count=%d err=%v", count, err)
	}
	blocked, err := service.CompleteProject(ctx, projectCommandExistingRequest(workspaceID, project.ID, taskdomain.ProjectCommandComplete, 2, at))
	if !errors.Is(err, taskdomain.ErrProjectHasOpenOccurrences) || !blocked.IsZero() {
		t.Fatalf("complete with open occurrences result=%#v err=%v", blocked, err)
	}
	afterBlocked, err := fixture.Reader(workspaceID).GetProject(ctx, project.ID)
	if err != nil || afterBlocked.Revision != 2 || afterBlocked.Project.Status != taskdomain.ProjectStatusActive {
		t.Fatalf("blocked completion changed project: %#v err=%v", afterBlocked, err)
	}

	for _, status := range []taskdomain.ExecutionStatus{taskdomain.ExecutionStatusOpen, taskdomain.ExecutionStatusActive, taskdomain.ExecutionStatusBlocked} {
		setProjectCommandOccurrenceStatus(t, fixture, "count-"+string(status)+"-occ", taskdomain.ExecutionStatusCancelled)
	}
	completed, err := service.CompleteProject(ctx, projectCommandExistingRequest(workspaceID, project.ID, taskdomain.ProjectCommandComplete, 2, at))
	if err != nil || completed.Revision() != 3 || completed.Project().Status != taskdomain.ProjectStatusCompleted {
		t.Fatalf("complete result=%#v err=%v", completed, err)
	}
	archived, err := service.ArchiveProject(ctx, projectCommandExistingRequest(workspaceID, project.ID, taskdomain.ProjectCommandArchive, 3, at))
	if err != nil || archived.Revision() != 4 || archived.Project().Status != taskdomain.ProjectStatusArchived {
		t.Fatalf("archive result=%#v err=%v", archived, err)
	}
	mustExec(t, fixture.DB, `DELETE FROM domain_tasks_v2 WHERE workspace_id='project-command-w1' AND project_id='command-project'`)
	deleted, err := service.DeleteProject(ctx, projectCommandExistingRequest(workspaceID, project.ID, taskdomain.ProjectCommandDelete, 4, at))
	if err != nil || !deleted.Deleted() {
		t.Fatalf("delete result=%#v err=%v", deleted, err)
	}
	if _, err := fixture.Reader(workspaceID).GetProject(ctx, project.ID); !errors.Is(err, taskdomain.ErrProjectNotFound) {
		t.Fatalf("deleted project read error=%v", err)
	}

	t.Run("CAS_system_epoch_rollback_and_closed_capability", func(t *testing.T) {
		cas := taskdomain.Project{
			WorkspaceID: workspaceID, ID: "cas-project", Name: "CAS", Kind: taskdomain.ProjectKindStandard,
			Horizon: taskdomain.ProjectHorizonShort, Status: taskdomain.ProjectStatusActive,
		}
		if _, err := service.CreateProject(ctx, taskdomain.CreateProjectRequest{WorkspaceID: workspaceID, ExpectedRuntimeEpoch: 1, Project: cas, CommandID: "cas-create", ActorID: "user-1", At: at}); err != nil {
			t.Fatal(err)
		}
		cas.Name = "CAS updated"
		if _, err := service.UpdateProject(ctx, taskdomain.UpdateProjectRequest{WorkspaceID: workspaceID, ProjectID: cas.ID, ExpectedRuntimeEpoch: 1, ExpectedProjectRevision: 1, Project: cas, CommandID: "cas-update", ActorID: "user-1", At: at}); err != nil {
			t.Fatal(err)
		}
		if _, err := service.UpdateProject(ctx, taskdomain.UpdateProjectRequest{WorkspaceID: workspaceID, ProjectID: cas.ID, ExpectedRuntimeEpoch: 1, ExpectedProjectRevision: 1, Project: cas, CommandID: "cas-stale", ActorID: "user-1", At: at}); !errors.Is(err, taskdomain.ErrProjectRevisionConflict) {
			t.Fatalf("stale project revision error=%v", err)
		}
		if _, err := service.DeleteProject(ctx, projectCommandExistingRequest(workspaceID, taskdomain.SystemInboxProjectID, taskdomain.ProjectCommandDelete, 1, at)); !errors.Is(err, taskdomain.ErrSystemProjectImmutable) {
			t.Fatalf("system delete error=%v", err)
		}
		if _, err := service.CreateProject(ctx, taskdomain.CreateProjectRequest{WorkspaceID: workspaceID, ExpectedRuntimeEpoch: 2, Project: taskdomain.Project{WorkspaceID: workspaceID, ID: "stale-epoch", Name: "stale", Kind: taskdomain.ProjectKindStandard, Horizon: taskdomain.ProjectHorizonShort, Status: taskdomain.ProjectStatusPlanning}, CommandID: "stale-epoch", ActorID: "user-1", At: at}); !errors.Is(err, storage.ErrTenantEpochMismatch) {
			t.Fatalf("stale epoch error=%v", err)
		}

		rollbackErr := errors.New("force rollback")
		rollbackProject := cas
		rollbackProject.ID = "rollback-project"
		rollbackProject.Name = "Rollback project"
		err := fixture.Fencer.BeginFencedProjectWrite(ctx, workspaceID, 1, func(tx taskdomain.ProjectCommandTx) error {
			if err := tx.ProjectWriter().SaveProject(ctx, taskdomain.ProjectWrite{Project: rollbackProject}); err != nil {
				return err
			}
			return rollbackErr
		})
		if !errors.Is(err, rollbackErr) {
			t.Fatalf("rollback callback error=%v", err)
		}
		if _, err := fixture.Reader(workspaceID).GetProject(ctx, rollbackProject.ID); !errors.Is(err, taskdomain.ErrProjectNotFound) {
			t.Fatalf("rollback leaked project, read error=%v", err)
		}

		var captured taskdomain.ProjectCommandTx
		if err := fixture.Fencer.BeginFencedProjectWrite(ctx, workspaceID, 1, func(tx taskdomain.ProjectCommandTx) error {
			captured = tx
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := captured.GetProject(ctx, cas.ID); !errors.Is(err, storage.ErrTenantWriteTxClosed) {
			t.Fatalf("closed project read error=%v", err)
		}
		if _, err := captured.CountNonTerminalProjectOccurrences(ctx, cas.ID); !errors.Is(err, storage.ErrTenantWriteTxClosed) {
			t.Fatalf("closed project count error=%v", err)
		}
		if err := captured.ProjectWriter().SaveProject(ctx, taskdomain.ProjectWrite{}); !errors.Is(err, storage.ErrTenantWriteTxClosed) {
			t.Fatalf("closed project writer error=%v", err)
		}
	})

	t.Run("completion_serializes_concurrent_occurrence_creation", func(t *testing.T) {
		project := taskdomain.Project{WorkspaceID: workspaceID, ID: "concurrent-project", Name: "Concurrent", Kind: taskdomain.ProjectKindStandard, Horizon: taskdomain.ProjectHorizonShort, Status: taskdomain.ProjectStatusActive}
		if _, err := service.CreateProject(ctx, taskdomain.CreateProjectRequest{WorkspaceID: workspaceID, ExpectedRuntimeEpoch: 1, Project: project, CommandID: "concurrent-create", ActorID: "user-1", At: at}); err != nil {
			t.Fatal(err)
		}
		counted := make(chan struct{})
		release := make(chan struct{})
		completeResult := make(chan error, 1)
		go func() {
			completeResult <- fixture.Fencer.BeginFencedProjectWrite(ctx, workspaceID, 1, func(tx taskdomain.ProjectCommandTx) error {
				count, err := tx.CountNonTerminalProjectOccurrences(ctx, project.ID)
				if err != nil || count != 0 {
					return errors.New("unexpected concurrent count")
				}
				close(counted)
				<-release
				current, err := tx.GetProject(ctx, project.ID)
				if err != nil {
					return err
				}
				project := current.Project
				project.Status = taskdomain.ProjectStatusCompleted
				return tx.ProjectWriter().SaveProject(ctx, taskdomain.ProjectWrite{Project: project, ExpectedRevision: current.Revision})
			})
		}()
		<-counted
		createResult := make(chan error, 1)
		go func() {
			createResult <- fixture.Writer.BeginFencedWrite(ctx, workspaceID, 1, func(tx storage.TenantWriteTx) error {
				return tx.TaskDomainWriter().CreateTaskAggregate(ctx, queryUnscheduledAggregate(workspaceID, "late-task", project.ID))
			})
		}()
		select {
		case err := <-createResult:
			t.Fatalf("occurrence write crossed project count/save boundary: %v", err)
		case <-time.After(150 * time.Millisecond):
		}
		close(release)
		if err := <-completeResult; err != nil {
			t.Fatalf("complete transaction: %v", err)
		}
		if err := <-createResult; err == nil {
			t.Fatal("occurrence was created after project completed")
		}
		var taskCount int
		if err := fixture.DB.QueryRow(`SELECT COUNT(*) FROM domain_tasks_v2 WHERE workspace_id='project-command-w1' AND id='late-task'`).Scan(&taskCount); err != nil || taskCount != 0 {
			t.Fatalf("late task count=%d err=%v", taskCount, err)
		}
	})

	t.Run("completed_project_rejects_occurrence_reopen_atomically", func(t *testing.T) {
		project := taskdomain.Project{WorkspaceID: workspaceID, ID: "status-project", Name: "Status project", Kind: taskdomain.ProjectKindStandard, Horizon: taskdomain.ProjectHorizonShort, Status: taskdomain.ProjectStatusActive}
		if _, err := service.CreateProject(ctx, taskdomain.CreateProjectRequest{WorkspaceID: workspaceID, ExpectedRuntimeEpoch: 1, Project: project, CommandID: "status-create", ActorID: "user-1", At: at}); err != nil {
			t.Fatal(err)
		}
		if err := fixture.Writer.BeginFencedWrite(ctx, workspaceID, 1, func(tx storage.TenantWriteTx) error {
			return tx.TaskDomainWriter().CreateTaskAggregate(ctx, queryUnscheduledAggregate(workspaceID, "status-task", project.ID))
		}); err != nil {
			t.Fatal(err)
		}
		setProjectCommandOccurrenceStatus(t, fixture, "status-task-occ", taskdomain.ExecutionStatusDone)
		mustExec(t, fixture.DB, `UPDATE domain_tasks_v2 SET lifecycle_status='completed' WHERE workspace_id='project-command-w1' AND id='status-task'`)
		if _, err := service.CompleteProject(ctx, projectCommandExistingRequest(workspaceID, project.ID, taskdomain.ProjectCommandComplete, 1, at)); err != nil {
			t.Fatal(err)
		}
		completedAt := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)
		current := taskdomain.TaskAggregate{WorkspaceID: workspaceID, TaskID: "status-task", LifecycleStatus: taskdomain.TaskLifecycleCompleted, Revision: 1, Occurrences: []taskdomain.Occurrence{{WorkspaceID: workspaceID, ID: "status-task-occ", TaskID: "status-task", OccurrenceKey: "once", ExecutionStatus: taskdomain.ExecutionStatusDone, CompletedAt: &completedAt, Revision: 1}}}
		expected := taskdomain.AggregateExpectedRevisions{Task: 1, Occurrences: map[string]int64{"status-task-occ": 1}}
		reopened, logs, err := taskdomain.ReopenSingleOccurrence(current, "status-task-occ", expected, taskdomain.ExecutionTransition{LogID: "status-reopen-log", ActorID: "user-1", At: at})
		if err != nil {
			t.Fatal(err)
		}
		err = fixture.Writer.BeginFencedWrite(ctx, workspaceID, 1, func(tx storage.TenantWriteTx) error {
			return tx.TaskDomainWriter().SaveTaskAggregate(ctx, taskdomain.TaskAggregateWrite{Aggregate: reopened, ExpectedRevisions: expected, ExpectedScheduleRevision: 1, ExecutionLogs: logs})
		})
		if !errors.Is(err, taskdomain.ErrInvalidTaskAggregateSnapshot) {
			t.Fatalf("reopen in completed project error=%v", err)
		}
		var taskStatus, occurrenceStatus string
		var taskRevision, occurrenceRevision int64
		if err := fixture.DB.QueryRow(`SELECT task.lifecycle_status,task.revision,occurrence.execution_status,occurrence.revision FROM domain_tasks_v2 task JOIN domain_task_occurrences_v2 occurrence ON occurrence.workspace_id=task.workspace_id AND occurrence.task_id=task.id WHERE task.workspace_id='project-command-w1' AND task.id='status-task'`).Scan(&taskStatus, &taskRevision, &occurrenceStatus, &occurrenceRevision); err != nil {
			t.Fatal(err)
		}
		if taskStatus != "completed" || taskRevision != 1 || occurrenceStatus != "done" || occurrenceRevision != 1 {
			t.Fatalf("reopen partially wrote task=%s/%d occurrence=%s/%d", taskStatus, taskRevision, occurrenceStatus, occurrenceRevision)
		}
	})
}

func projectCommandExistingRequest(workspaceID, projectID string, command taskdomain.ProjectCommand, revision int64, at time.Time) taskdomain.ExistingProjectRequest {
	return taskdomain.ExistingProjectRequest{WorkspaceID: workspaceID, ProjectID: projectID, Command: command, ExpectedRuntimeEpoch: 1, ExpectedProjectRevision: revision, CommandID: "command-" + string(command), ActorID: "user-1", At: at}
}

func setProjectCommandOccurrenceStatus(t *testing.T, fixture TaskDomainV2ProjectCommandFixture, occurrenceID string, status taskdomain.ExecutionStatus) {
	t.Helper()
	completed := any(nil)
	actualStart := any(nil)
	blockedReason := any(nil)
	nextAction := any(nil)
	if status == taskdomain.ExecutionStatusActive || status == taskdomain.ExecutionStatusBlocked {
		actualStart = queryContractTime(fixture.Dialect, time.Date(2026, 7, 22, 8, 0, 0, 0, time.UTC))
	}
	if status == taskdomain.ExecutionStatusDone {
		completed = queryContractTime(fixture.Dialect, time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC))
	}
	if status == taskdomain.ExecutionStatusBlocked {
		blockedReason, nextAction = "waiting", "follow up"
	}
	mustExecArgs(t, fixture.DB, `UPDATE domain_task_occurrences_v2 SET execution_status=?,actual_start_at=?,completed_at=?,blocked_reason=?,next_action=? WHERE workspace_id='project-command-w1' AND id=?`, fixture.Dialect, status, actualStart, completed, blockedReason, nextAction, occurrenceID)
}
