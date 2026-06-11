package service

import (
	"database/sql"
	"os"
	"strings"
	"testing"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/repository"
	_ "modernc.org/sqlite"
)

func openRoadmapServiceTestDB(t *testing.T) {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)

	schema, err := os.ReadFile("../../db/schema.sql")
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	if _, err := db.Exec(string(schema)); err != nil {
		t.Fatalf("exec schema: %v", err)
	}

	repository.DB = db
	t.Cleanup(func() {
		repository.DB = nil
		db.Close()
	})
}

func TestGenerateLearningRoadmapWithMockAI(t *testing.T) {
	openRoadmapServiceTestDB(t)
	t.Setenv("AI_PROVIDER", "mock")

	project := createLearningProjectForTest(t, "Go 后端实战")
	roadmap, err := GenerateLearningRoadmap(project.ID)
	if err != nil {
		t.Fatalf("generate learning roadmap: %v", err)
	}

	if roadmap.ProjectID != project.ID || roadmap.Status != "ready" {
		t.Fatalf("unexpected roadmap header: %+v", roadmap)
	}
	if len(roadmap.Nodes) < 4 {
		t.Fatalf("expected generated nodes, got %d", len(roadmap.Nodes))
	}
	if len(roadmap.Edges) == 0 {
		t.Fatal("expected generated edges")
	}

	var hasDeliverable bool
	for _, node := range roadmap.Nodes {
		if node.Deliverable != "" {
			hasDeliverable = true
			break
		}
	}
	if !hasDeliverable {
		t.Fatal("expected project-practice roadmap nodes to include deliverables")
	}
}

func TestGenerateLearningRoadmapAutomaticallyAttachesArticleResources(t *testing.T) {
	openRoadmapServiceTestDB(t)
	t.Setenv("AI_PROVIDER", "mock")
	t.Setenv("ARTICLE_SEARCH_PROVIDER", "mock")

	project := createLearningProjectForTest(t, "前端工程化")
	roadmap, err := GenerateLearningRoadmap(project.ID)
	if err != nil {
		t.Fatalf("generate learning roadmap: %v", err)
	}

	var resourceCount int
	for _, node := range roadmap.Nodes {
		resourceCount += len(node.Resources)
	}
	if resourceCount == 0 {
		t.Fatal("expected roadmap generation to attach article resources automatically")
	}
}

func TestEnsureRoadmapBranchingAddsChoiceBranchesToLinearDraft(t *testing.T) {
	project := model.TaskProject{Name: "后端实战"}
	draft := &roadmapDraft{
		Title: "后端实战学习路线",
		Goal:  "通过项目实战掌握后端开发",
		Nodes: []model.RoadmapNode{
			{ID: "start", Type: "phase", Title: "基础准备", PathType: "required", X: 0, Y: 0},
			{ID: "build", Type: "task", Title: "实现 MVP", PathType: "required", X: 0, Y: 160},
		},
		Edges: []model.RoadmapEdge{
			{ID: "edge-1", SourceNodeID: "start", TargetNodeID: "build", Style: "solid"},
		},
	}

	ensureRoadmapBranching(draft, project)

	var choiceCount int
	branchFanOut := map[string]int{}
	for _, node := range draft.Nodes {
		if node.Type == "choice" && (node.PathType == "recommended" || node.PathType == "alternative") {
			choiceCount++
		}
	}
	for _, edge := range draft.Edges {
		if edge.Style == "dotted" {
			branchFanOut[edge.SourceNodeID]++
		}
	}
	if choiceCount < 2 {
		t.Fatalf("expected at least two choice branch nodes, got %d", choiceCount)
	}
	for sourceID, count := range branchFanOut {
		if count >= 2 {
			return
		}
		_ = sourceID
	}
	t.Fatalf("expected one source node to fan out into at least two dotted branch edges, got %+v", branchFanOut)
}

func TestRoadmapPromptRequestsBranchingAndOnlineArticles(t *testing.T) {
	prompt := buildRoadmapSystemPrompt()

	for _, required := range []string{"branch", "choice", "recommended", "alternative", "article_search_queries", "online articles"} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("expected prompt to contain %q, got %s", required, prompt)
		}
	}
}

func TestGenerateLearningRoadmapInvalidAIJSONStoresFailedRoadmap(t *testing.T) {
	openRoadmapServiceTestDB(t)
	t.Setenv("AI_PROVIDER", "invalid-json")

	project := createLearningProjectForTest(t, "Rust 项目实战")
	if _, err := GenerateLearningRoadmap(project.ID); err == nil {
		t.Fatal("expected invalid AI response to fail")
	}

	roadmap, err := GetLearningRoadmap(project.ID)
	if err != nil {
		t.Fatalf("get failed roadmap: %v", err)
	}
	if roadmap.Status != "failed" {
		t.Fatalf("status = %q, want failed", roadmap.Status)
	}
	if len(roadmap.Nodes) != 0 {
		t.Fatalf("expected no partial nodes after invalid AI response, got %d", len(roadmap.Nodes))
	}
}

func TestGenerateLearningRoadmapDefaultsToRealProviderWhenUnset(t *testing.T) {
	openRoadmapServiceTestDB(t)
	t.Setenv("AI_PROVIDER", "")
	t.Setenv("AI_API_KEY", "")

	project := createLearningProjectForTest(t, "默认 DeepSeek")
	if _, err := GenerateLearningRoadmap(project.ID); err == nil || !strings.Contains(err.Error(), "AI_API_KEY is required") {
		t.Fatalf("expected missing API key error from real provider default, got %v", err)
	}
}

func TestRoadmapNodeResourcesBindToSelectedNode(t *testing.T) {
	openRoadmapServiceTestDB(t)
	t.Setenv("AI_PROVIDER", "mock")
	t.Setenv("ARTICLE_SEARCH_PROVIDER", "none")

	project := createLearningProjectForTest(t, "TypeScript 全栈")
	roadmap, err := GenerateLearningRoadmap(project.ID)
	if err != nil {
		t.Fatalf("generate roadmap: %v", err)
	}
	node := roadmap.Nodes[0]

	t.Setenv("ARTICLE_SEARCH_PROVIDER", "mock")
	resources, err := SearchRoadmapNodeResources(node.ID, &model.SearchRoadmapResourcesRequest{
		Sources: []string{"medium", "reddit"},
	})
	if err != nil {
		t.Fatalf("search resources: %v", err)
	}
	if len(resources) < 10 {
		t.Fatalf("expected at least 10 article candidates, got %d", len(resources))
	}
	if resources[0].ID == "" || resources[0].NodeID != node.ID || resources[0].URL == "" {
		t.Fatalf("candidate not bound to selected node: %+v", resources[0])
	}
	for _, resource := range resources {
		if !strings.Contains(resource.URL, "medium.com") && !strings.Contains(resource.URL, "reddit.com") {
			t.Fatalf("candidate did not honor selected sources: %+v", resource)
		}
	}

	unchanged, err := GetLearningRoadmap(project.ID)
	if err != nil {
		t.Fatalf("get unchanged roadmap: %v", err)
	}
	for _, unchangedNode := range unchanged.Nodes {
		if unchangedNode.ID == node.ID && len(unchangedNode.Resources) != 0 {
			t.Fatalf("search should not save resources before user selection, got %d", len(unchangedNode.Resources))
		}
	}

	manual, err := AddRoadmapNodeResource(node.ID, &model.CreateRoadmapResourceRequest{
		Title:   resources[0].Title,
		URL:     resources[0].URL,
		Summary: resources[0].Summary,
	})
	if err != nil {
		t.Fatalf("add selected resource: %v", err)
	}
	if manual.AddedBy != "user" || manual.SourceType != "manual" {
		t.Fatalf("unexpected selected resource metadata: %+v", manual)
	}

	withSelected, err := GetLearningRoadmap(project.ID)
	if err != nil {
		t.Fatalf("get roadmap with selected resource: %v", err)
	}
	foundSelected := false
	for _, updatedNode := range withSelected.Nodes {
		for _, resource := range updatedNode.Resources {
			if resource.ID == manual.ID {
				foundSelected = true
			}
		}
	}
	if !foundSelected {
		t.Fatalf("selected resource was not saved to target node: %+v", manual)
	}

	if err := DeleteRoadmapResource(manual.ID); err != nil {
		t.Fatalf("delete resource: %v", err)
	}
	updated, err := GetLearningRoadmap(project.ID)
	if err != nil {
		t.Fatalf("get roadmap: %v", err)
	}
	for _, updatedNode := range updated.Nodes {
		for _, resource := range updatedNode.Resources {
			if resource.ID == manual.ID {
				t.Fatalf("deleted resource still present: %+v", resource)
			}
		}
	}
}

func TestArticleSearchFallbackPadsSelectedSourcesToTenChoices(t *testing.T) {
	node := model.RoadmapNode{Title: "Go API 项目", Deliverable: "REST API"}
	sources := selectedArticleSearchSources([]string{"medium", "reddit"})

	resources := ensureArticleResourceChoices(node, sources, nil, 10)

	if len(resources) != 10 {
		t.Fatalf("fallback resources = %d, want 10", len(resources))
	}
	for _, resource := range resources {
		if !strings.Contains(resource.URL, "medium.com") && !strings.Contains(resource.URL, "reddit.com") {
			t.Fatalf("fallback resource did not honor selected sources: %+v", resource)
		}
	}
}

func createLearningProjectForTest(t *testing.T, name string) model.TaskProject {
	t.Helper()

	project, err := repository.CreateTaskProject(&model.CreateTaskProjectRequest{
		Name: name,
		Type: "learning",
	})
	if err != nil {
		t.Fatalf("create learning project: %v", err)
	}
	return *project
}
