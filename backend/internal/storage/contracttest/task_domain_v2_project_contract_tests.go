package contracttest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/taskdomain"
)

type TaskDomainV2ProjectFixture struct {
	DB        *sql.DB
	Dialect   TaskDomainV2Dialect
	Writer    storage.TenantFencedWriter
	NewReader func(workspaceID string) taskdomain.ProjectReader
}

func RunTaskDomainV2ProjectSuite(t *testing.T, fixture TaskDomainV2ProjectFixture) {
	t.Helper()
	ctx := context.Background()
	for _, workspaceID := range []string{"project-w1", "project-w2"} {
		mustExec(t, fixture.DB, fmt.Sprintf(`INSERT INTO tenant_workspaces(workspace_id) VALUES('%s')`, workspaceID))
	}

	ensureSystemProjects := func(workspaceID string, epoch int64) error {
		return fixture.Writer.BeginFencedWrite(ctx, workspaceID, epoch, func(tx storage.TenantWriteTx) error {
			return tx.TaskDomainWriter().EnsureSystemProjects(ctx)
		})
	}

	t.Run("system_projects_use_fixed_ids_per_workspace_and_provision_idempotently", func(t *testing.T) {
		for _, workspaceID := range []string{"project-w1", "project-w2"} {
			if err := ensureSystemProjects(workspaceID, 1); err != nil {
				t.Fatalf("ensure system projects for %s: %v", workspaceID, err)
			}
			reader := fixture.NewReader(workspaceID)
			inbox, err := reader.GetProject(ctx, taskdomain.SystemInboxProjectID)
			if err != nil {
				t.Fatalf("read inbox for %s: %v", workspaceID, err)
			}
			personal, err := reader.GetProject(ctx, taskdomain.PersonalProjectID)
			if err != nil {
				t.Fatalf("read personal for %s: %v", workspaceID, err)
			}
			if inbox.Project.WorkspaceID != workspaceID || inbox.Project.SystemRole != taskdomain.ProjectSystemRoleInbox {
				t.Fatalf("unexpected inbox: %#v", inbox)
			}
			if personal.Project.WorkspaceID != workspaceID || personal.Project.SystemRole != taskdomain.ProjectSystemRolePersonal {
				t.Fatalf("unexpected personal: %#v", personal)
			}
			if inbox.Revision != 1 || personal.Revision != 1 {
				t.Fatalf("initial system revisions = inbox:%d personal:%d", inbox.Revision, personal.Revision)
			}
			if err := ensureSystemProjects(workspaceID, 1); err != nil {
				t.Fatalf("idempotent ensure for %s: %v", workspaceID, err)
			}
			after, err := reader.GetProject(ctx, taskdomain.SystemInboxProjectID)
			if err != nil || after.Revision != 1 {
				t.Fatalf("idempotent ensure changed inbox: snapshot=%#v err=%v", after, err)
			}
		}
	})

	t.Run("system_role_is_unique_within_workspace", func(t *testing.T) {
		expectStatementRejected(t, fixture.DB, `INSERT INTO domain_projects_v2
			(workspace_id,id,name,kind,horizon,status,system_role,revision,created_at,updated_at)
			VALUES ('project-w1','duplicate-personal','Duplicate personal','standard','short','active','personal',1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`)
	})

	normal := taskdomain.Project{
		WorkspaceID: "project-w1",
		ID:          "project-normal",
		Name:        "Normal project",
		Kind:        taskdomain.ProjectKindStandard,
		Horizon:     taskdomain.ProjectHorizonShort,
		Status:      taskdomain.ProjectStatusActive,
		SystemRole:  taskdomain.ProjectSystemRoleNone,
	}

	t.Run("project_create_read_and_update_use_cas", func(t *testing.T) {
		if err := fixture.Writer.BeginFencedWrite(ctx, "project-w1", 1, func(tx storage.TenantWriteTx) error {
			return tx.TaskDomainWriter().SaveProject(ctx, taskdomain.ProjectWrite{Project: normal, ExpectedRevision: 0})
		}); err != nil {
			t.Fatalf("create project: %v", err)
		}
		created, err := fixture.NewReader("project-w1").GetProject(ctx, normal.ID)
		if err != nil || created.Revision != 1 || created.Project.Name != normal.Name {
			t.Fatalf("created project = %#v err=%v", created, err)
		}

		updated := normal
		updated.Name = "Renamed project"
		if err := fixture.Writer.BeginFencedWrite(ctx, "project-w1", 1, func(tx storage.TenantWriteTx) error {
			return tx.TaskDomainWriter().SaveProject(ctx, taskdomain.ProjectWrite{Project: updated, ExpectedRevision: 1})
		}); err != nil {
			t.Fatalf("update project: %v", err)
		}
		after, err := fixture.NewReader("project-w1").GetProject(ctx, normal.ID)
		if err != nil || after.Revision != 2 || after.Project.Name != updated.Name {
			t.Fatalf("updated project = %#v err=%v", after, err)
		}
	})

	t.Run("same_revision_allows_exactly_one_concurrent_update", func(t *testing.T) {
		start := make(chan struct{})
		results := make(chan error, 2)
		var ready sync.WaitGroup
		ready.Add(2)
		for _, name := range []string{"Concurrent A", "Concurrent B"} {
			name := name
			go func() {
				ready.Done()
				<-start
				project := normal
				project.Name = name
				results <- fixture.Writer.BeginFencedWrite(ctx, "project-w1", 1, func(tx storage.TenantWriteTx) error {
					return tx.TaskDomainWriter().SaveProject(ctx, taskdomain.ProjectWrite{Project: project, ExpectedRevision: 2})
				})
			}()
		}
		ready.Wait()
		close(start)
		successes := 0
		conflicts := 0
		for i := 0; i < 2; i++ {
			err := <-results
			switch {
			case err == nil:
				successes++
			case errors.Is(err, taskdomain.ErrAggregateRevisionConflict):
				conflicts++
			default:
				t.Fatalf("unexpected concurrent update error: %v", err)
			}
		}
		if successes != 1 || conflicts != 1 {
			t.Fatalf("concurrent results successes=%d conflicts=%d", successes, conflicts)
		}
	})

	t.Run("system_project_role_change_and_delete_are_rejected_by_repository_and_database", func(t *testing.T) {
		reader := fixture.NewReader("project-w1")
		inbox, err := reader.GetProject(ctx, taskdomain.SystemInboxProjectID)
		if err != nil {
			t.Fatal(err)
		}
		changed := inbox.Project
		changed.SystemRole = taskdomain.ProjectSystemRolePersonal
		err = fixture.Writer.BeginFencedWrite(ctx, "project-w1", 1, func(tx storage.TenantWriteTx) error {
			return tx.TaskDomainWriter().SaveProject(ctx, taskdomain.ProjectWrite{Project: changed, ExpectedRevision: inbox.Revision})
		})
		if !errors.Is(err, taskdomain.ErrSystemProjectImmutable) {
			t.Fatalf("repository role change error = %v", err)
		}
		err = fixture.Writer.BeginFencedWrite(ctx, "project-w1", 1, func(tx storage.TenantWriteTx) error {
			return tx.TaskDomainWriter().DeleteProject(ctx, taskdomain.SystemInboxProjectID, inbox.Revision)
		})
		if !errors.Is(err, taskdomain.ErrSystemProjectImmutable) {
			t.Fatalf("repository delete error = %v", err)
		}
		expectStatementRejected(t, fixture.DB, `UPDATE domain_projects_v2 SET system_role='personal' WHERE workspace_id='project-w1' AND id='system-inbox'`)
		expectStatementRejected(t, fixture.DB, `DELETE FROM domain_projects_v2 WHERE workspace_id='project-w1' AND id='system-inbox'`)
	})

	t.Run("normal_project_delete_uses_cas", func(t *testing.T) {
		current, err := fixture.NewReader("project-w1").GetProject(ctx, normal.ID)
		if err != nil {
			t.Fatal(err)
		}
		err = fixture.Writer.BeginFencedWrite(ctx, "project-w1", 1, func(tx storage.TenantWriteTx) error {
			return tx.TaskDomainWriter().DeleteProject(ctx, normal.ID, current.Revision)
		})
		if err != nil {
			t.Fatalf("delete normal project: %v", err)
		}
		if _, err := fixture.NewReader("project-w1").GetProject(ctx, normal.ID); !errors.Is(err, taskdomain.ErrProjectNotFound) {
			t.Fatalf("read deleted project error = %v", err)
		}
	})

	t.Run("closed_transaction_writer_is_unusable", func(t *testing.T) {
		var captured taskdomain.TaskDomainWriter
		if err := fixture.Writer.BeginFencedWrite(ctx, "project-w1", 1, func(tx storage.TenantWriteTx) error {
			captured = tx.TaskDomainWriter()
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := captured.EnsureSystemProjects(ctx); !errors.Is(err, storage.ErrTenantWriteTxClosed) {
			t.Fatalf("closed project writer error = %v", err)
		}
	})

	t.Run("stale_epoch_rejects_before_callback", func(t *testing.T) {
		called := false
		err := fixture.Writer.BeginFencedWrite(ctx, "project-w1", 2, func(tx storage.TenantWriteTx) error {
			called = true
			return tx.TaskDomainWriter().EnsureSystemProjects(ctx)
		})
		if !errors.Is(err, storage.ErrTenantEpochMismatch) || called {
			t.Fatalf("stale epoch error=%v callback_called=%v", err, called)
		}
	})
}
