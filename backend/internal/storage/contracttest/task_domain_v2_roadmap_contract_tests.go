package contracttest

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/taskdomain"
)

type TaskDomainV2RoadmapFixture struct {
	DB        *sql.DB
	Dialect   TaskDomainV2Dialect
	Writer    storage.TenantFencedWriter
	NewReader func(string) taskdomain.RoadmapReader
}

func RunTaskDomainV2RoadmapSuite(t *testing.T, f TaskDomainV2RoadmapFixture) {
	ctx := context.Background()
	for _, w := range []string{"roadmap-w1", "roadmap-w2"} {
		mustExec(t, f.DB, `INSERT INTO tenant_workspaces(workspace_id) VALUES('`+w+`')`)
	}
	mustExec(t, f.DB, `INSERT INTO domain_projects_v2(workspace_id,id,name,kind,horizon,status,revision,created_at,updated_at) VALUES
		('roadmap-w1','learn','Learn','learning','long','active',1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP),
		('roadmap-w1','other','Other','learning','long','active',1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP),
		('roadmap-w2','learn','Learn 2','learning','long','active',1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`)
	if err := f.Writer.BeginFencedWrite(ctx, "roadmap-w1", 1, func(tx storage.TenantWriteTx) error {
		w := tx.RoadmapWriter()
		if err := w.CreateRoadmap(ctx, taskdomain.LearningRoadmap{WorkspaceID: "roadmap-w1", ID: "r1", ProjectID: "learn", Status: taskdomain.RoadmapStatusActive, Title: "Path", Revision: 1}); err != nil {
			return err
		}
		if err := w.CreateRoadmapNode(ctx, taskdomain.RoadmapNode{WorkspaceID: "roadmap-w1", ID: "n1", ProjectID: "learn", RoadmapID: "r1", Title: "Foundation", Type: taskdomain.RoadmapNodeStage, Revision: 1}); err != nil {
			return err
		}
		for _, id := range []string{"open", "active", "blocked", "done"} {
			s := queryUnscheduledAggregate("roadmap-w1", id, "learn")
			s.Task.RoadmapNodeID = "n1"
			if err := tx.TaskDomainWriter().CreateTaskAggregate(ctx, s); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed roadmap: %v", err)
	}
	mustExecArgs(t, f.DB, `UPDATE domain_task_occurrences_v2 SET execution_status=? WHERE workspace_id=? AND id=?`, f.Dialect, "active", "roadmap-w1", "active-occ")
	mustExecArgs(t, f.DB, `UPDATE domain_task_occurrences_v2 SET execution_status=?,blocked_reason=?,next_action=? WHERE workspace_id=? AND id=?`, f.Dialect, "blocked", "reason", "next", "roadmap-w1", "blocked-occ")
	mustExecArgs(t, f.DB, `UPDATE domain_task_occurrences_v2 SET execution_status=?,completed_at=CURRENT_TIMESTAMP WHERE workspace_id=? AND id=?`, f.Dialect, "done", "roadmap-w1", "done-occ")

	rm, err := f.NewReader("roadmap-w1").GetRoadmapByProject(ctx, "learn")
	if err != nil {
		t.Fatal(err)
	}
	if len(rm.Nodes) != 1 || len(rm.Edges) != 0 || rm.Nodes[0].Progress.Tasks != 4 || rm.Nodes[0].Progress.Total != 4 || rm.Nodes[0].Progress.Open != 1 || rm.Nodes[0].Progress.Active != 1 || rm.Nodes[0].Progress.Blocked != 1 || rm.Nodes[0].Progress.Done != 1 {
		t.Fatalf("unexpected roadmap projection: %#v", rm)
	}
	if _, err = f.NewReader("roadmap-w2").GetRoadmapByID(ctx, "r1"); !errors.Is(err, taskdomain.ErrRoadmapNotFound) {
		t.Fatalf("cross workspace read = %v", err)
	}

	expectStatementRejected(t, f.DB, `INSERT INTO domain_learning_roadmaps_v2(workspace_id,id,project_id,status,title,revision,created_at,updated_at) VALUES('roadmap-w1','duplicate','learn','active','Duplicate',1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`)
	expectStatementRejected(t, f.DB, `UPDATE domain_tasks_v2 SET project_id='other' WHERE workspace_id='roadmap-w1' AND id='open'`)

	err = f.Writer.BeginFencedWrite(ctx, "roadmap-w1", 1, func(tx storage.TenantWriteTx) error {
		return tx.RoadmapWriter().SaveRoadmapNode(ctx, taskdomain.RoadmapNodeWrite{Node: taskdomain.RoadmapNode{ID: "n1", ProjectID: "learn", RoadmapID: "r1", Title: "stale", Type: taskdomain.RoadmapNodeTopic, Revision: 2}, ExpectedRevision: 9})
	})
	if !errors.Is(err, taskdomain.ErrRoadmapRevisionConflict) {
		t.Fatalf("stale update error = %v", err)
	}
}
