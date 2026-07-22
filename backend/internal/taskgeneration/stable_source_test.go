package taskgeneration

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDurableStableV2SourceListsOnlyActiveAndClassifiedWorkspacesSQLite(t *testing.T) {
	_, db := schedulerSQLiteRepository(t, time.Minute)
	for _, workspaceID := range []string{"v2", "legacy", "transitional", "inactive"} {
		seedSchedulerWorkspace(t, db, workspaceID)
	}
	if _, err := db.Exec(`INSERT INTO workspace_runtime_state(workspace_id,mode,epoch,binding_revision,updated_by) VALUES
		('v2','active',7,1,'user-v2'),('legacy','active',3,1,'user-legacy'),
		('transitional','active',5,1,'user-transitional'),('inactive','blocked',9,1,'user-inactive')`); err != nil {
		t.Fatal(err)
	}
	candidates, err := NewSQLActiveWorkspaceSource(db, ControlDialectSQLite)
	if err != nil {
		t.Fatal(err)
	}
	classificationErr := errors.New("tenant migration in progress")
	classifier := stableClassifierStub{results: map[string]classificationResult{
		"v2":           {stable: true},
		"legacy":       {stable: false},
		"transitional": {stable: false, err: classificationErr},
	}}
	source, err := NewDurableStableV2Source(candidates, classifier)
	if err != nil {
		t.Fatal(err)
	}
	workspaces, err := source.ListStableV2Workspaces(t.Context())
	if !errors.Is(err, classificationErr) {
		t.Fatalf("classification error=%v", err)
	}
	if len(workspaces) != 1 || workspaces[0] != (StableV2Workspace{WorkspaceID: "v2", Epoch: 7}) {
		t.Fatalf("stable workspaces=%#v", workspaces)
	}
}

func TestSchedulerProcessesPartialStableSnapshotAndReportsClassificationError(t *testing.T) {
	repository, db := schedulerSQLiteRepository(t, time.Minute)
	seedSchedulerWorkspace(t, db, "healthy")
	sourceErr := errors.New("other workspace unavailable")
	source := partialStableSource{workspaces: []StableV2Workspace{{WorkspaceID: "healthy", Epoch: 2}}, err: sourceErr}
	scheduler, _ := NewScheduler(source, repository, time.Minute)
	result, err := scheduler.Reconcile(t.Context(), time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC))
	if !errors.Is(err, sourceErr) || result.Scheduled != 1 {
		t.Fatalf("partial reconcile=%#v err=%v", result, err)
	}
}

type classificationResult struct {
	stable bool
	err    error
}

type stableClassifierStub struct {
	results map[string]classificationResult
}

func (s stableClassifierStub) IsStableV2Workspace(_ context.Context, workspaceID string, _ int64) (bool, error) {
	result := s.results[workspaceID]
	return result.stable, result.err
}

type partialStableSource struct {
	workspaces []StableV2Workspace
	err        error
}

func (s partialStableSource) ListStableV2Workspaces(context.Context) ([]StableV2Workspace, error) {
	return s.workspaces, s.err
}
