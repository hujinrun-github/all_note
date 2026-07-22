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
}

func (*roadmapWriterFake) CreateRoadmap(context.Context, LearningRoadmap) error { return nil }
func (*roadmapWriterFake) CreateRoadmapNode(context.Context, RoadmapNode) error { return nil }
func (f *roadmapWriterFake) SaveRoadmapNode(_ context.Context, write RoadmapNodeWrite) error {
	f.saved = write
	return nil
}
func (f *roadmapWriterFake) DeleteRoadmapNode(context.Context, string, int64) error {
	f.deleteCalls++
	return nil
}
