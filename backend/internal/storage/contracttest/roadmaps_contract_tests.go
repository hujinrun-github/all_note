package contracttest

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/hujinrun/flowspace/internal/model"
)

func RunRoadmapSuite(t *testing.T, factory StoreFactory) {
	t.Helper()

	t.Run("RoadmapGraphRoundTripPreservesQueriesResourcesAndEdges", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctx := context.Background()
		project, err := store.Tasks().CreateProject(ctx, &model.CreateTaskProjectRequest{
			Name:        "AI Infra Roadmap",
			Type:        "learning",
			Description: "系统学习",
		})
		if err != nil {
			t.Fatalf("create project: %v", err)
		}

		parentID := "node-parent-contract"
		roadmap, err := store.Roadmaps().ReplaceLearningRoadmap(ctx, &model.LearningRoadmap{
			ProjectID: project.ID,
			Title:     "AI Infra Roadmap",
			Goal:      "掌握系统设计",
			Status:    "ready",
			Nodes: []model.RoadmapNode{
				{
					ID:                   "node-child-contract",
					ParentID:             &parentID,
					Type:                 "task",
					Title:                "实现迁移",
					Description:          "完成 provider 迁移",
					PathType:             "required",
					Status:               "todo",
					Deliverable:          "迁移 PR",
					AcceptanceCriteria:   "测试通过",
					X:                    30.5,
					Y:                    40.5,
					OrderIndex:           2,
					ArticleSearchQueries: []string{"PostgreSQL migration official docs", "PostgreSQL pg_trgm tutorial"},
				},
				{
					ID:         parentID,
					Type:       "phase",
					Title:      "理解 schema",
					PathType:   "required",
					Status:     "active",
					X:          12.5,
					Y:          20.5,
					OrderIndex: 1,
				},
			},
			Edges: []model.RoadmapEdge{{
				ID:           "edge-contract",
				SourceNodeID: parentID,
				TargetNodeID: "node-child-contract",
				Style:        "dotted",
			}},
		})
		if err != nil {
			t.Fatalf("replace roadmap: %v", err)
		}
		if roadmap.Status != "ready" || len(roadmap.Nodes) != 2 || len(roadmap.Edges) != 1 {
			t.Fatalf("unexpected roadmap after replace: %+v", roadmap)
		}

		resource := &model.RoadmapResource{
			NodeID:  "node-child-contract",
			Title:   "pg_trgm docs",
			URL:     "https://www.postgresql.org/docs/current/pgtrgm.html",
			Summary: "trigram search",
		}
		if err := store.Roadmaps().AddRoadmapResource(ctx, resource); err != nil {
			t.Fatalf("add resource: %v", err)
		}

		loaded, err := store.Roadmaps().GetLearningRoadmap(ctx, project.ID)
		if err != nil {
			t.Fatalf("get roadmap: %v", err)
		}
		if loaded.ID != roadmap.ID || loaded.Title != "AI Infra Roadmap" || len(loaded.Nodes) != 2 || len(loaded.Edges) != 1 {
			t.Fatalf("unexpected loaded roadmap: %+v", loaded)
		}
		child := findNode(t, loaded.Nodes, "node-child-contract")
		if child.ParentID == nil || *child.ParentID != parentID {
			t.Fatalf("expected child parent %q, got %+v", parentID, child.ParentID)
		}
		if child.X != 30.5 || child.Y != 40.5 {
			t.Fatalf("expected position round-trip, got x=%v y=%v", child.X, child.Y)
		}
		if len(child.ArticleSearchQueries) != 2 || child.ArticleSearchQueries[0] != "PostgreSQL migration official docs" || child.ArticleSearchQueries[1] != "PostgreSQL pg_trgm tutorial" {
			t.Fatalf("expected article search queries to round-trip, got %+v", child.ArticleSearchQueries)
		}
		if len(child.Resources) != 1 || child.Resources[0].Title != "pg_trgm docs" {
			t.Fatalf("expected node resource to round-trip, got %+v", child.Resources)
		}
		if loaded.Edges[0].SourceNodeID != parentID || loaded.Edges[0].TargetNodeID != "node-child-contract" || loaded.Edges[0].Style != "dotted" {
			t.Fatalf("unexpected edges: %+v", loaded.Edges)
		}
	})

	t.Run("RoadmapNodeLifecycle", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctx := context.Background()
		project, err := store.Tasks().CreateProject(ctx, &model.CreateTaskProjectRequest{Name: "Node Lifecycle", Type: "learning"})
		if err != nil {
			t.Fatalf("create project: %v", err)
		}
		roadmap, err := store.Roadmaps().ReplaceLearningRoadmap(ctx, &model.LearningRoadmap{
			ProjectID: project.ID,
			Title:     "Node Lifecycle",
			Goal:      "验证节点更新",
			Nodes:     []model.RoadmapNode{{ID: "root-node-contract", Title: "Root", Type: "phase"}},
		})
		if err != nil {
			t.Fatalf("replace roadmap: %v", err)
		}

		parentID := "root-node-contract"
		node, err := store.Roadmaps().CreateRoadmapNode(ctx, &model.RoadmapNode{
			RoadmapID:  roadmap.ID,
			ParentID:   &parentID,
			Title:      "Write tests",
			Type:       "module",
			PathType:   "optional",
			OrderIndex: 3,
		}, &model.RoadmapEdge{SourceNodeID: parentID, Style: "dotted"})
		if err != nil {
			t.Fatalf("create node: %v", err)
		}
		if node.ParentID == nil || *node.ParentID != parentID || node.Type != "module" || node.PathType != "optional" {
			t.Fatalf("unexpected created node: %+v", node)
		}

		title := "Write provider tests"
		status := "done"
		x, y := 8.0, 9.0
		updated, err := store.Roadmaps().UpdateRoadmapNode(ctx, node.ID, &model.UpdateRoadmapNodeRequest{
			Title:  &title,
			Status: &status,
			X:      &x,
			Y:      &y,
		})
		if err != nil {
			t.Fatalf("update node: %v", err)
		}
		if updated.Title != title || updated.Status != "done" || updated.X != x || updated.Y != y {
			t.Fatalf("unexpected updated node: %+v", updated)
		}

		if err := store.Roadmaps().UpdateRoadmapNodeStatus(ctx, node.ID, "active"); err != nil {
			t.Fatalf("update node status: %v", err)
		}
		active, err := store.Roadmaps().GetRoadmapNode(ctx, node.ID)
		if err != nil {
			t.Fatalf("get active node: %v", err)
		}
		if active.Status != "active" {
			t.Fatalf("expected active status, got %+v", active)
		}

		if err := store.Roadmaps().UpdateRoadmapLayout(ctx, roadmap.ID, []model.RoadmapLayoutNode{{ID: node.ID, X: 18, Y: 19}}); err != nil {
			t.Fatalf("update layout: %v", err)
		}
		moved, err := store.Roadmaps().GetRoadmapNode(ctx, node.ID)
		if err != nil {
			t.Fatalf("get moved node: %v", err)
		}
		if moved.X != 18 || moved.Y != 19 {
			t.Fatalf("expected moved node, got %+v", moved)
		}

		if err := store.Roadmaps().DeleteRoadmapNode(ctx, node.ID); err != nil {
			t.Fatalf("delete node: %v", err)
		}
		if _, err := store.Roadmaps().GetRoadmapNode(ctx, node.ID); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("expected deleted node to be missing, got %v", err)
		}
	})

	t.Run("FailedRoadmapStatusIsPersisted", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctx := context.Background()
		project, err := store.Tasks().CreateProject(ctx, &model.CreateTaskProjectRequest{Name: "Failed Roadmap", Type: "learning"})
		if err != nil {
			t.Fatalf("create project: %v", err)
		}
		roadmap, err := store.Roadmaps().SaveFailedLearningRoadmap(ctx, project.ID, "失败路线图", "生成失败")
		if err != nil {
			t.Fatalf("save failed roadmap: %v", err)
		}
		if roadmap.Status != "failed" {
			t.Fatalf("expected failed roadmap, got %+v", roadmap)
		}
	})
}

func findNode(t *testing.T, nodes []model.RoadmapNode, id string) model.RoadmapNode {
	t.Helper()
	for _, node := range nodes {
		if node.ID == id {
			return node
		}
	}
	t.Fatalf("node %q not found in %+v", id, nodes)
	return model.RoadmapNode{}
}
