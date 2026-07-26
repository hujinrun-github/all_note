package taskdomain

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRoadmapServiceRejectsNonLearningProjectAndProtectsLinkedNodes(t *testing.T) {
	ctx := context.Background()
	tx := &roadmapServiceTxFake{project: ProjectSnapshot{Project: Project{WorkspaceID: "w1", ID: "p1", Name: "P", Kind: ProjectKindStandard, Horizon: ProjectHorizonLong, Status: ProjectStatusActive}, Revision: 1}}
	service := NewRoadmapService(roadmapServiceFencerFake{tx: tx})
	_, err := service.CreateRoadmap(ctx, CreateRoadmapRequest{WorkspaceID: "w1", ProjectID: "p1", RoadmapID: "r1", ExpectedRuntimeEpoch: 1, Title: "Path", CommandID: "c1", ActorID: "u1", At: time.Now()})
	if !errors.Is(err, ErrRoadmapRequiresLearningProject) {
		t.Fatalf("create error = %v", err)
	}

	tx.project.Project.Kind = ProjectKindLearning
	tx.roadmap = RoadmapSnapshot{Roadmap: LearningRoadmap{WorkspaceID: "w1", ID: "r1", ProjectID: "p1", Status: RoadmapStatusActive, Title: "Path", Revision: 1}}
	tx.node = RoadmapNodeSnapshot{Node: RoadmapNode{WorkspaceID: "w1", ID: "n1", ProjectID: "p1", RoadmapID: "r1", Title: "Node", Type: RoadmapNodeTopic, Revision: 3}}
	tx.linkedTasks = 1
	err = service.DeleteNode(ctx, DeleteRoadmapNodeRequest{WorkspaceID: "w1", RoadmapID: "r1", NodeID: "n1", ExpectedRuntimeEpoch: 1, ExpectedRevision: 3, CommandID: "c2", ActorID: "u1", At: time.Now()})
	if !errors.Is(err, ErrRoadmapNodeHasTasks) || tx.writer.deleteCalls != 0 {
		t.Fatalf("delete error=%v calls=%d", err, tx.writer.deleteCalls)
	}
}

func TestRoadmapServiceUsesIndependentNodeRevision(t *testing.T) {
	tx := &roadmapServiceTxFake{
		project: ProjectSnapshot{Project: Project{WorkspaceID: "w1", ID: "p1", Name: "P", Kind: ProjectKindLearning, Horizon: ProjectHorizonLong, Status: ProjectStatusActive}, Revision: 9},
		roadmap: RoadmapSnapshot{Roadmap: LearningRoadmap{WorkspaceID: "w1", ID: "r1", ProjectID: "p1", Status: RoadmapStatusActive, Title: "Path", Revision: 4}},
		node:    RoadmapNodeSnapshot{Node: RoadmapNode{WorkspaceID: "w1", ID: "n1", ProjectID: "p1", RoadmapID: "r1", Title: "Before", Type: RoadmapNodeTopic, Revision: 3}},
	}
	service := NewRoadmapService(roadmapServiceFencerFake{tx: tx})
	result, err := service.UpdateNode(context.Background(), UpdateRoadmapNodeRequest{WorkspaceID: "w1", RoadmapID: "r1", NodeID: "n1", ExpectedRuntimeEpoch: 1, ExpectedRevision: 3, Title: "After", Type: RoadmapNodeMilestone, CommandID: "c", ActorID: "u", At: time.Now()})
	if err != nil || result.Node.Revision != 4 || result.Node.Title != "After" || tx.writer.saved.ExpectedRevision != 3 {
		t.Fatalf("result=%#v saved=%#v err=%v", result, tx.writer.saved, err)
	}
}

func TestRoadmapServiceReplacesUnlinkedNodesInOneFencedWrite(t *testing.T) {
	tx := &roadmapServiceTxFake{
		project: ProjectSnapshot{Project: Project{WorkspaceID: "w1", ID: "p1", Name: "P", Kind: ProjectKindLearning, Horizon: ProjectHorizonLong, Status: ProjectStatusActive}, Revision: 1},
		roadmap: RoadmapSnapshot{
			Roadmap: LearningRoadmap{WorkspaceID: "w1", ID: "r1", ProjectID: "p1", Status: RoadmapStatusActive, Title: "Path", Revision: 1},
			Nodes: []RoadmapNodeSnapshot{
				{Node: RoadmapNode{WorkspaceID: "w1", ID: "old-parent", ProjectID: "p1", RoadmapID: "r1", Title: "Parent", Type: RoadmapNodeStage, Revision: 1}},
				{Node: RoadmapNode{WorkspaceID: "w1", ID: "old-child", ProjectID: "p1", RoadmapID: "r1", ParentID: "old-parent", Title: "Child", Type: RoadmapNodeTopic, Revision: 1}},
			},
		},
	}
	service := NewRoadmapService(roadmapServiceFencerFake{tx: tx})
	result, err := service.ReplaceNodes(context.Background(), ReplaceRoadmapNodesRequest{
		WorkspaceID: "w1",
		RoadmapID:   "r1",
		Nodes: []RoadmapNode{
			{WorkspaceID: "w1", ID: "new-parent", RoadmapID: "r1", Title: "New parent", Type: RoadmapNodeStage, Revision: 1},
			{WorkspaceID: "w1", ID: "new-child", RoadmapID: "r1", ParentID: "new-parent", Title: "New child", Type: RoadmapNodeMilestone, Revision: 1},
		},
		ExpectedRuntimeEpoch: 1,
		CommandID:            "replace-1",
		ActorID:              "u1",
		At:                   time.Now(),
	})
	if err != nil {
		t.Fatalf("ReplaceNodes() error = %v", err)
	}
	if len(result.Nodes) != 2 || len(tx.writer.created) != 2 {
		t.Fatalf("result nodes=%d created=%d", len(result.Nodes), len(tx.writer.created))
	}
	if len(tx.writer.deleted) != 2 || tx.writer.deleted[0] != "old-child" || tx.writer.deleted[1] != "old-parent" {
		t.Fatalf("deleted order = %v", tx.writer.deleted)
	}
	if result.Nodes[1].Node.ParentID != "new-parent" {
		t.Fatalf("new child parent = %q", result.Nodes[1].Node.ParentID)
	}
}

func TestRoadmapServiceRefusesToReplaceLinkedNodes(t *testing.T) {
	tx := &roadmapServiceTxFake{
		project:     ProjectSnapshot{Project: Project{WorkspaceID: "w1", ID: "p1", Name: "P", Kind: ProjectKindLearning, Horizon: ProjectHorizonLong, Status: ProjectStatusActive}, Revision: 1},
		roadmap:     RoadmapSnapshot{Roadmap: LearningRoadmap{WorkspaceID: "w1", ID: "r1", ProjectID: "p1", Status: RoadmapStatusActive, Title: "Path", Revision: 1}, Nodes: []RoadmapNodeSnapshot{{Node: RoadmapNode{WorkspaceID: "w1", ID: "old", ProjectID: "p1", RoadmapID: "r1", Title: "Old", Type: RoadmapNodeTopic, Revision: 1}}}},
		linkedTasks: 1,
	}
	service := NewRoadmapService(roadmapServiceFencerFake{tx: tx})
	_, err := service.ReplaceNodes(context.Background(), ReplaceRoadmapNodesRequest{
		WorkspaceID:          "w1",
		RoadmapID:            "r1",
		Nodes:                []RoadmapNode{{WorkspaceID: "w1", ID: "new", RoadmapID: "r1", Title: "New", Type: RoadmapNodeTopic, Revision: 1}},
		ExpectedRuntimeEpoch: 1,
		CommandID:            "replace-1",
		ActorID:              "u1",
		At:                   time.Now(),
	})
	if !errors.Is(err, ErrRoadmapNodeHasTasks) || len(tx.writer.deleted) != 0 || len(tx.writer.created) != 0 {
		t.Fatalf("ReplaceNodes() error=%v deleted=%v created=%v", err, tx.writer.deleted, tx.writer.created)
	}
}

type roadmapServiceFencerFake struct{ tx *roadmapServiceTxFake }

func (f roadmapServiceFencerFake) BeginFencedRoadmapWrite(_ context.Context, _ string, _ int64, fn func(RoadmapCommandTx) error) error {
	return fn(f.tx)
}

type roadmapServiceTxFake struct {
	project     ProjectSnapshot
	roadmap     RoadmapSnapshot
	node        RoadmapNodeSnapshot
	linkedTasks int
	writer      roadmapWriterFake
}

func (f *roadmapServiceTxFake) GetProject(context.Context, string) (ProjectSnapshot, error) {
	return f.project, nil
}
func (f *roadmapServiceTxFake) GetRoadmapByProject(context.Context, string) (RoadmapSnapshot, error) {
	if f.roadmap.Roadmap.ID == "" {
		return RoadmapSnapshot{}, ErrRoadmapNotFound
	}
	return f.roadmap, nil
}
func (f *roadmapServiceTxFake) GetRoadmapByID(context.Context, string) (RoadmapSnapshot, error) {
	return f.roadmap, nil
}
func (f *roadmapServiceTxFake) GetRoadmapNode(context.Context, string) (RoadmapNodeSnapshot, error) {
	return f.node, nil
}
func (f *roadmapServiceTxFake) CountRoadmapNodeTasks(context.Context, string) (int, error) {
	return f.linkedTasks, nil
}
func (f *roadmapServiceTxFake) RoadmapWriter() RoadmapWriter { return &f.writer }

type roadmapWriterFake struct {
	saved       RoadmapNodeWrite
	deleteCalls int
	created     []RoadmapNode
	deleted     []string
}

func (*roadmapWriterFake) CreateRoadmap(context.Context, LearningRoadmap) error { return nil }
func (f *roadmapWriterFake) CreateRoadmapNode(_ context.Context, node RoadmapNode) error {
	f.created = append(f.created, node)
	return nil
}
func (f *roadmapWriterFake) SaveRoadmapNode(_ context.Context, write RoadmapNodeWrite) error {
	f.saved = write
	return nil
}
func (f *roadmapWriterFake) DeleteRoadmapNode(_ context.Context, nodeID string, _ int64) error {
	f.deleteCalls++
	f.deleted = append(f.deleted, nodeID)
	return nil
}
