package service

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/repository"
	"github.com/hujinrun/flowspace/internal/storage"
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

func roadmapTestContext(t *testing.T) context.Context {
	t.Helper()
	return t.Context()
}

func roadmapTestStore(t *testing.T) storage.Store {
	t.Helper()
	return roadmapLegacyStore{}
}

type roadmapLegacyStore struct {
	storage.Store
}

func (roadmapLegacyStore) Tasks() storage.TaskRepository {
	return roadmapLegacyTasks{}
}

func (roadmapLegacyStore) Roadmaps() storage.RoadmapRepository {
	return roadmapLegacyRoadmaps{}
}

type roadmapLegacyTasks struct {
	storage.TaskRepository
}

func (roadmapLegacyTasks) GetProjectByID(ctx context.Context, id string) (*model.TaskProject, error) {
	return repository.GetTaskProjectByID(id)
}

func (roadmapLegacyTasks) List(ctx context.Context, filter storage.TaskFilter) ([]model.Task, int, error) {
	if filter.RoadmapNodeID == "" {
		return nil, 0, nil
	}
	tasks, err := repository.GetTasksByRoadmapNodeID(filter.RoadmapNodeID)
	return tasks, len(tasks), err
}

type roadmapLegacyRoadmaps struct {
	storage.RoadmapRepository
}

func (roadmapLegacyRoadmaps) ReplaceLearningRoadmap(ctx context.Context, roadmap *model.LearningRoadmap) (*model.LearningRoadmap, error) {
	return repository.ReplaceLearningRoadmap(roadmap)
}

func (roadmapLegacyRoadmaps) SaveFailedLearningRoadmap(ctx context.Context, projectID, title, goal string) (*model.LearningRoadmap, error) {
	return repository.SaveFailedLearningRoadmap(projectID, title, goal)
}

func (roadmapLegacyRoadmaps) GetLearningRoadmap(ctx context.Context, projectID string) (*model.LearningRoadmap, error) {
	return repository.GetLearningRoadmap(projectID)
}

func (roadmapLegacyRoadmaps) GetLearningRoadmapByID(ctx context.Context, roadmapID string) (*model.LearningRoadmap, error) {
	return repository.GetLearningRoadmapByID(roadmapID)
}

func (roadmapLegacyRoadmaps) GetRoadmapNode(ctx context.Context, nodeID string) (*model.RoadmapNode, error) {
	return repository.GetRoadmapNode(nodeID)
}

func (roadmapLegacyRoadmaps) CreateRoadmapNode(ctx context.Context, node *model.RoadmapNode, edge *model.RoadmapEdge) (*model.RoadmapNode, error) {
	return repository.CreateRoadmapNode(node, edge)
}

func (roadmapLegacyRoadmaps) UpdateRoadmapNode(ctx context.Context, id string, req *model.UpdateRoadmapNodeRequest) (*model.RoadmapNode, error) {
	return repository.UpdateRoadmapNode(id, req)
}

func (roadmapLegacyRoadmaps) DeleteRoadmapNode(ctx context.Context, id string) error {
	return repository.DeleteRoadmapNode(id)
}

func (roadmapLegacyRoadmaps) UpdateRoadmapNodeStatus(ctx context.Context, id, status string) error {
	return repository.UpdateRoadmapNodeStatus(id, status)
}

func (roadmapLegacyRoadmaps) UpdateRoadmapLayout(ctx context.Context, roadmapID string, nodes []model.RoadmapLayoutNode) error {
	return repository.UpdateRoadmapLayout(roadmapID, nodes)
}

func (roadmapLegacyRoadmaps) AddRoadmapResource(ctx context.Context, resource *model.RoadmapResource) error {
	return repository.AddRoadmapResource(resource)
}

func (roadmapLegacyRoadmaps) DeleteRoadmapResource(ctx context.Context, id string) error {
	return repository.DeleteRoadmapResource(id)
}

func TestGenerateLearningRoadmapWithMockAI(t *testing.T) {
	openRoadmapServiceTestDB(t)
	t.Setenv("AI_PROVIDER", "mock")

	project := createLearningProjectForTest(t, "Go 鍚庣瀹炴垬")
	roadmap, err := GenerateLearningRoadmap(roadmapTestContext(t), roadmapTestStore(t), project.ID)
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
	for index := range roadmap.Nodes {
		for next := index + 1; next < len(roadmap.Nodes); next++ {
			if roadmapNodesOverlap(roadmap.Nodes[index], roadmap.Nodes[next]) {
				t.Fatalf("generated roadmap nodes overlap: %+v and %+v", roadmap.Nodes[index], roadmap.Nodes[next])
			}
		}
	}
}

func TestGenerateLearningRoadmapAutomaticallyAttachesArticleResources(t *testing.T) {
	openRoadmapServiceTestDB(t)
	t.Setenv("AI_PROVIDER", "mock")
	t.Setenv("ARTICLE_SEARCH_PROVIDER", "mock")

	project := createLearningProjectForTest(t, "frontend engineering")
	roadmap, err := GenerateLearningRoadmap(roadmapTestContext(t), roadmapTestStore(t), project.ID)
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
	project := model.TaskProject{Name: "鍚庣瀹炴垬"}
	draft := &roadmapDraft{
		Title: "鍚庣瀹炴垬瀛︿範璺嚎",
		Goal:  "learn backend development through project practice",
		Nodes: []model.RoadmapNode{
			{ID: "start", Type: "phase", Title: "鍩虹鍑嗗", PathType: "required", X: 0, Y: 0},
			{ID: "build", Type: "task", Title: "瀹炵幇 MVP", PathType: "required", X: 0, Y: 160},
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

func TestNormalizeGeneratedRoadmapLayoutSeparatesOverlappingNodes(t *testing.T) {
	draft := &roadmapDraft{
		Title: "閲嶅彔璺嚎",
		Goal:  "楠岃瘉甯冨眬閬胯",
		Nodes: []model.RoadmapNode{
			{ID: "a", Type: "phase", Title: "A", PathType: "required", X: 0, Y: 0, OrderIndex: 0},
			{ID: "b", Type: "module", Title: "B", PathType: "required", X: 20, Y: 10, OrderIndex: 1},
			{ID: "c", Type: "choice", Title: "C", PathType: "recommended", X: 30, Y: 20, OrderIndex: 2},
			{ID: "d", Type: "choice", Title: "D", PathType: "alternative", X: 40, Y: 30, OrderIndex: 3},
		},
	}

	normalizeGeneratedRoadmapLayout(draft)

	for index := range draft.Nodes {
		for next := index + 1; next < len(draft.Nodes); next++ {
			if roadmapNodesOverlap(draft.Nodes[index], draft.Nodes[next]) {
				t.Fatalf("nodes still overlap after layout normalization: %+v and %+v", draft.Nodes[index], draft.Nodes[next])
			}
		}
	}
}

func TestNormalizeGeneratedRoadmapLayoutSpreadsFanOutBranches(t *testing.T) {
	const minBranchGap = 300.0
	const minJoinGapY = 120.0

	draft := &roadmapDraft{
		Title: "branching layout",
		Goal:  "avoid branch edges being hidden by nodes",
		Nodes: []model.RoadmapNode{
			{ID: "source", Type: "choice", Title: "Choose serving framework", PathType: "required", X: 0, Y: 500, OrderIndex: 0},
			{ID: "left", Type: "task", Title: "Deploy Triton", PathType: "alternative", X: -2, Y: 650, OrderIndex: 1},
			{ID: "right", Type: "task", Title: "Deploy TensorFlow Serving", PathType: "alternative", X: 2, Y: 780, OrderIndex: 2},
			{ID: "join", Type: "task", Title: "Deploy on Kubernetes", PathType: "required", X: 0, Y: 940, OrderIndex: 3},
		},
		Edges: []model.RoadmapEdge{
			{ID: "edge-source-left", SourceNodeID: "source", TargetNodeID: "left", Style: "dotted"},
			{ID: "edge-source-right", SourceNodeID: "source", TargetNodeID: "right", Style: "dotted"},
			{ID: "edge-left-join", SourceNodeID: "left", TargetNodeID: "join", Style: "solid"},
			{ID: "edge-right-join", SourceNodeID: "right", TargetNodeID: "join", Style: "solid"},
		},
	}

	normalizeGeneratedRoadmapLayout(draft)

	source := roadmapTestNodeByID(t, draft.Nodes, "source")
	left := roadmapTestNodeByID(t, draft.Nodes, "left")
	right := roadmapTestNodeByID(t, draft.Nodes, "right")
	join := roadmapTestNodeByID(t, draft.Nodes, "join")

	if left.X >= source.X-minBranchGap {
		t.Fatalf("left fan-out node x = %.1f, want at least %.1f left of source %.1f", left.X, minBranchGap, source.X)
	}
	if right.X <= source.X+minBranchGap {
		t.Fatalf("right fan-out node x = %.1f, want at least %.1f right of source %.1f", right.X, minBranchGap, source.X)
	}
	if absFloat(left.Y-right.Y) > 1 {
		t.Fatalf("fan-out sibling nodes should share a row, got y %.1f and %.1f", left.Y, right.Y)
	}
	if join.Y <= left.Y+minJoinGapY || join.Y <= right.Y+minJoinGapY {
		t.Fatalf("fan-in join node y = %.1f, want below branch row %.1f/%.1f", join.Y, left.Y, right.Y)
	}
}

func TestNormalizeGeneratedRoadmapLayoutKeepsBranchesBelowShiftedSource(t *testing.T) {
	draft := &roadmapDraft{
		Title: "shifted source branches",
		Goal:  "fan-out rows follow source nodes after overlap avoidance",
		Nodes: []model.RoadmapNode{
			{ID: "start", Type: "phase", Title: "Start", PathType: "required", X: 0, Y: 0, OrderIndex: 0},
			{ID: "step-1", Type: "task", Title: "Step 1", PathType: "required", X: 0, Y: 5, OrderIndex: 1},
			{ID: "step-2", Type: "task", Title: "Step 2", PathType: "required", X: 0, Y: 10, OrderIndex: 2},
			{ID: "step-3", Type: "task", Title: "Step 3", PathType: "required", X: 0, Y: 15, OrderIndex: 3},
			{ID: "source", Type: "choice", Title: "Choose framework", PathType: "required", X: 0, Y: 20, OrderIndex: 4},
			{ID: "left", Type: "task", Title: "Left branch", PathType: "alternative", X: -2, Y: 25, OrderIndex: 5},
			{ID: "right", Type: "task", Title: "Right branch", PathType: "alternative", X: 2, Y: 30, OrderIndex: 6},
		},
		Edges: []model.RoadmapEdge{
			{ID: "edge-start-step-1", SourceNodeID: "start", TargetNodeID: "step-1", Style: "solid"},
			{ID: "edge-step-1-step-2", SourceNodeID: "step-1", TargetNodeID: "step-2", Style: "solid"},
			{ID: "edge-step-2-step-3", SourceNodeID: "step-2", TargetNodeID: "step-3", Style: "solid"},
			{ID: "edge-step-3-source", SourceNodeID: "step-3", TargetNodeID: "source", Style: "solid"},
			{ID: "edge-source-left", SourceNodeID: "source", TargetNodeID: "left", Style: "dotted"},
			{ID: "edge-source-right", SourceNodeID: "source", TargetNodeID: "right", Style: "dotted"},
		},
	}

	normalizeGeneratedRoadmapLayout(draft)

	source := roadmapTestNodeByID(t, draft.Nodes, "source")
	left := roadmapTestNodeByID(t, draft.Nodes, "left")
	right := roadmapTestNodeByID(t, draft.Nodes, "right")

	if left.Y <= source.Y || right.Y <= source.Y {
		t.Fatalf("fan-out branch row should stay below shifted source: source %.1f, left %.1f, right %.1f", source.Y, left.Y, right.Y)
	}
}

func roadmapTestNodeByID(t *testing.T, nodes []model.RoadmapNode, id string) model.RoadmapNode {
	t.Helper()
	for _, node := range nodes {
		if node.ID == id {
			return node
		}
	}
	t.Fatalf("node %q not found in %+v", id, nodes)
	return model.RoadmapNode{}
}

func TestGetLearningRoadmapNormalizesPersistedOverlappingNodesForDisplay(t *testing.T) {
	openRoadmapServiceTestDB(t)
	project := createLearningProjectForTest(t, "閲嶅彔灞曠ず")
	_, err := repository.ReplaceLearningRoadmap(&model.LearningRoadmap{
		ProjectID: project.ID,
		Title:     "閲嶅彔灞曠ず璺嚎",
		Goal:      "楠岃瘉鍘嗗彶甯冨眬灞曠ず閬胯",
		Status:    "ready",
		Nodes: []model.RoadmapNode{
			{ID: "node-a", Type: "phase", Title: "A", PathType: "required", X: 0, Y: 0, OrderIndex: 0},
			{ID: "node-b", Type: "module", Title: "B", PathType: "required", X: 10, Y: 10, OrderIndex: 1},
		},
		Edges: []model.RoadmapEdge{
			{ID: "edge-a-b", SourceNodeID: "node-a", TargetNodeID: "node-b", Style: "solid"},
		},
	})
	if err != nil {
		t.Fatalf("save overlapping roadmap: %v", err)
	}

	roadmap, err := GetLearningRoadmap(roadmapTestContext(t), roadmapTestStore(t), project.ID)
	if err != nil {
		t.Fatalf("get roadmap: %v", err)
	}

	if len(roadmap.Nodes) != 2 {
		t.Fatalf("nodes = %d, want 2", len(roadmap.Nodes))
	}
	if roadmapNodesOverlap(roadmap.Nodes[0], roadmap.Nodes[1]) {
		t.Fatalf("persisted overlapping nodes should be separated for display: %+v", roadmap.Nodes)
	}
}

func TestOptimizeRoadmapLayoutPersistsImprovedCoordinates(t *testing.T) {
	openRoadmapServiceTestDB(t)
	project := createLearningProjectForTest(t, "layout optimize")
	saved, err := repository.ReplaceLearningRoadmap(&model.LearningRoadmap{
		ProjectID: project.ID,
		Title:     "layout optimize roadmap",
		Goal:      "persist optimized coordinates",
		Status:    "ready",
		Nodes: []model.RoadmapNode{
			{ID: "source", Type: "choice", Title: "Source", PathType: "required", X: 0, Y: 0, OrderIndex: 0},
			{ID: "left", Type: "task", Title: "Left", PathType: "recommended", X: 1, Y: 10, OrderIndex: 1},
			{ID: "right", Type: "task", Title: "Right", PathType: "alternative", X: 2, Y: 12, OrderIndex: 2},
			{ID: "join", Type: "task", Title: "Join", PathType: "required", X: 3, Y: 14, OrderIndex: 3},
		},
		Edges: []model.RoadmapEdge{
			{ID: "edge-source-left", SourceNodeID: "source", TargetNodeID: "left", Style: "dotted"},
			{ID: "edge-source-right", SourceNodeID: "source", TargetNodeID: "right", Style: "dotted"},
			{ID: "edge-left-join", SourceNodeID: "left", TargetNodeID: "join", Style: "solid"},
			{ID: "edge-right-join", SourceNodeID: "right", TargetNodeID: "join", Style: "solid"},
		},
	})
	if err != nil {
		t.Fatalf("save roadmap: %v", err)
	}

	optimized, err := OptimizeRoadmapLayout(roadmapTestContext(t), roadmapTestStore(t), saved.ID)
	if err != nil {
		t.Fatalf("optimize roadmap layout: %v", err)
	}

	source := roadmapTestNodeByID(t, optimized.Nodes, "source")
	left := roadmapTestNodeByID(t, optimized.Nodes, "left")
	right := roadmapTestNodeByID(t, optimized.Nodes, "right")
	join := roadmapTestNodeByID(t, optimized.Nodes, "join")
	if left.X >= source.X-300 || right.X <= source.X+300 {
		t.Fatalf("branch nodes were not spread horizontally: source %.1f left %.1f right %.1f", source.X, left.X, right.X)
	}
	if left.Y <= source.Y || right.Y <= source.Y || join.Y <= left.Y || join.Y <= right.Y {
		t.Fatalf("optimized branch flow should progress downward: source %.1f left %.1f right %.1f join %.1f", source.Y, left.Y, right.Y, join.Y)
	}

	persisted, err := repository.GetLearningRoadmap(project.ID)
	if err != nil {
		t.Fatalf("get persisted roadmap: %v", err)
	}
	persistedLeft := roadmapTestNodeByID(t, persisted.Nodes, "left")
	persistedRight := roadmapTestNodeByID(t, persisted.Nodes, "right")
	if persistedLeft.X == 1 || persistedRight.X == 2 {
		t.Fatalf("optimized coordinates were not persisted: left %.1f right %.1f", persistedLeft.X, persistedRight.X)
	}
}

func TestCreateAndDeleteRoadmapNodeMutatesRoadmapGraph(t *testing.T) {
	openRoadmapServiceTestDB(t)
	project := createLearningProjectForTest(t, "manual node editing")
	rootID := "root-node"
	saved, err := repository.ReplaceLearningRoadmap(&model.LearningRoadmap{
		ProjectID: project.ID,
		Title:     "manual node editing roadmap",
		Goal:      "allow editing graph nodes",
		Status:    "ready",
		Nodes: []model.RoadmapNode{
			{ID: rootID, Type: "phase", Title: "Root", PathType: "required", X: 0, Y: 0, OrderIndex: 0},
		},
	})
	if err != nil {
		t.Fatalf("save roadmap: %v", err)
	}

	created, err := CreateRoadmapNode(roadmapTestContext(t), roadmapTestStore(t), saved.ID, &model.CreateRoadmapNodeRequest{
		ParentID:  &rootID,
		Title:     "Manual practice branch",
		Type:      "task",
		PathType:  "optional",
		EdgeStyle: "dotted",
	})
	if err != nil {
		t.Fatalf("create roadmap node: %v", err)
	}
	if created.ID == "" || created.RoadmapID != saved.ID {
		t.Fatalf("created node was not bound to roadmap: %+v", created)
	}
	if created.ParentID == nil || *created.ParentID != rootID {
		t.Fatalf("created node parent = %+v, want %q", created.ParentID, rootID)
	}

	roadmap, err := repository.GetLearningRoadmapByID(saved.ID)
	if err != nil {
		t.Fatalf("reload roadmap: %v", err)
	}
	if roadmapTestNodeByID(t, roadmap.Nodes, created.ID).Title != "Manual practice branch" {
		t.Fatalf("created node not found in roadmap: %+v", roadmap.Nodes)
	}
	foundEdge := false
	for _, edge := range roadmap.Edges {
		if edge.SourceNodeID == rootID && edge.TargetNodeID == created.ID && edge.Style == "dotted" {
			foundEdge = true
		}
	}
	if !foundEdge {
		t.Fatalf("expected dotted edge from root to created node, got %+v", roadmap.Edges)
	}

	if err := DeleteRoadmapNode(roadmapTestContext(t), roadmapTestStore(t), created.ID); err != nil {
		t.Fatalf("delete roadmap node: %v", err)
	}
	updated, err := repository.GetLearningRoadmapByID(saved.ID)
	if err != nil {
		t.Fatalf("reload updated roadmap: %v", err)
	}
	for _, node := range updated.Nodes {
		if node.ID == created.ID {
			t.Fatalf("deleted node still present: %+v", node)
		}
	}
	for _, edge := range updated.Edges {
		if edge.SourceNodeID == created.ID || edge.TargetNodeID == created.ID {
			t.Fatalf("deleted node edge still present: %+v", edge)
		}
	}
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

	project := createLearningProjectForTest(t, "Rust 椤圭洰瀹炴垬")
	if _, err := GenerateLearningRoadmap(roadmapTestContext(t), roadmapTestStore(t), project.ID); err == nil {
		t.Fatal("expected invalid AI response to fail")
	}

	roadmap, err := GetLearningRoadmap(roadmapTestContext(t), roadmapTestStore(t), project.ID)
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

func TestGenerateLearningRoadmapFallsBackToLocalDraftWhenAIUnavailable(t *testing.T) {
	openRoadmapServiceTestDB(t)
	t.Setenv("AI_PROVIDER", "")
	t.Setenv("AI_API_KEY", "")
	t.Setenv("ARTICLE_SEARCH_PROVIDER", "none")

	project := createLearningProjectForTest(t, "榛樿 DeepSeek")
	roadmap, err := GenerateLearningRoadmap(roadmapTestContext(t), roadmapTestStore(t), project.ID)
	if err != nil {
		t.Fatalf("generate roadmap should fall back when AI is unavailable: %v", err)
	}
	if roadmap.Status != "ready" || len(roadmap.Nodes) == 0 {
		t.Fatalf("expected ready fallback roadmap with nodes, got %+v", roadmap)
	}
}

func TestGenerateLearningRoadmapPrunesInvalidAIEdges(t *testing.T) {
	openRoadmapServiceTestDB(t)
	t.Setenv("AI_PROVIDER", "deepseek")
	t.Setenv("AI_API_KEY", "test-key")
	t.Setenv("ARTICLE_SEARCH_PROVIDER", "none")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected AI path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"title\":\"AI Roadmap\",\"goal\":\"goal\",\"nodes\":[{\"id\":\"start\",\"title\":\"Start\"},{\"id\":\"next\",\"title\":\"Next\"}],\"edges\":[{\"source_node_id\":\"start\",\"target_node_id\":\"next\",\"style\":\"solid\"},{\"source_node_id\":\"start\",\"target_node_id\":\"missing\",\"style\":\"solid\"},{\"source_node_id\":\"start\",\"target_node_id\":\"next\",\"style\":\"solid\"}]}"}}]}`))
	}))
	t.Cleanup(server.Close)
	t.Setenv("AI_BASE_URL", server.URL)

	project := createLearningProjectForTest(t, "AI edge cleanup")
	roadmap, err := GenerateLearningRoadmap(roadmapTestContext(t), roadmapTestStore(t), project.ID)
	if err != nil {
		t.Fatalf("generate roadmap with dirty AI edges: %v", err)
	}

	nodeIDs := make(map[string]bool, len(roadmap.Nodes))
	for _, node := range roadmap.Nodes {
		nodeIDs[node.ID] = true
	}
	seenEdges := map[string]bool{}
	for _, edge := range roadmap.Edges {
		if !nodeIDs[edge.SourceNodeID] || !nodeIDs[edge.TargetNodeID] {
			t.Fatalf("edge references missing node after generation: %+v nodes=%+v", edge, roadmap.Nodes)
		}
		key := edge.SourceNodeID + "->" + edge.TargetNodeID
		if seenEdges[key] {
			t.Fatalf("duplicate edge after generation: %+v", edge)
		}
		seenEdges[key] = true
	}
}

func TestRoadmapNodeResourcesBindToSelectedNode(t *testing.T) {
	openRoadmapServiceTestDB(t)
	t.Setenv("AI_PROVIDER", "mock")
	t.Setenv("ARTICLE_SEARCH_PROVIDER", "none")

	project := createLearningProjectForTest(t, "TypeScript 鍏ㄦ爤")
	roadmap, err := GenerateLearningRoadmap(roadmapTestContext(t), roadmapTestStore(t), project.ID)
	if err != nil {
		t.Fatalf("generate roadmap: %v", err)
	}
	node := roadmap.Nodes[0]

	t.Setenv("ARTICLE_SEARCH_PROVIDER", "mock")
	resources, err := SearchRoadmapNodeResources(roadmapTestContext(t), roadmapTestStore(t), node.ID, &model.SearchRoadmapResourcesRequest{
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

	unchanged, err := GetLearningRoadmap(roadmapTestContext(t), roadmapTestStore(t), project.ID)
	if err != nil {
		t.Fatalf("get unchanged roadmap: %v", err)
	}
	for _, unchangedNode := range unchanged.Nodes {
		if unchangedNode.ID == node.ID && len(unchangedNode.Resources) != 0 {
			t.Fatalf("search should not save resources before user selection, got %d", len(unchangedNode.Resources))
		}
	}

	manual, err := AddRoadmapNodeResource(roadmapTestContext(t), roadmapTestStore(t), node.ID, &model.CreateRoadmapResourceRequest{
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

	withSelected, err := GetLearningRoadmap(roadmapTestContext(t), roadmapTestStore(t), project.ID)
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

	if err := DeleteRoadmapResource(roadmapTestContext(t), roadmapTestStore(t), manual.ID); err != nil {
		t.Fatalf("delete resource: %v", err)
	}
	updated, err := GetLearningRoadmap(roadmapTestContext(t), roadmapTestStore(t), project.ID)
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

func TestArticleSearchDoesNotInventSearchEntryCandidates(t *testing.T) {
	node := model.RoadmapNode{Title: "Go API 椤圭洰", Deliverable: "REST API"}
	sources := selectedArticleSearchSources([]string{"medium", "reddit"})

	resources := ensureArticleResourceChoices(node, sources, nil, 10)

	if len(resources) != 0 {
		t.Fatalf("expected no invented search-entry candidates, got %+v", resources)
	}
}

func TestArticleSearchKeepsArticleTitlesAndFiltersSearchEntryURLs(t *testing.T) {
	node := model.RoadmapNode{Title: "Go API 椤圭洰", Deliverable: "REST API"}
	sources := selectedArticleSearchSources([]string{"medium", "reddit"})

	resources := ensureArticleResourceChoices(node, sources, []model.RoadmapResource{
		{
			Title:   "Go API 椤圭洰 Medium 鎼滅储鍏ュ彛 1",
			URL:     "https://medium.com/search?q=go+api",
			Summary: "Medium 婧愬唴鎼滅储鍏ュ彛",
		},
		{
			Title:   "How to design high throughput Go APIs",
			URL:     "https://medium.com/example/how-to-design-high-throughput-go-apis",
			Summary: "A popular technical article.",
		},
		{
			Title:   "Go API 椤圭洰 Reddit 鎼滅储鍏ュ彛 2",
			URL:     "https://www.reddit.com/search/?q=go+api",
			Summary: "Reddit 婧愬唴鎼滅储鍏ュ彛",
		},
	}, 10)

	if len(resources) != 1 {
		t.Fatalf("filtered resources = %d, want 1: %+v", len(resources), resources)
	}
	if resources[0].Title != "How to design high throughput Go APIs" {
		t.Fatalf("expected real article title, got %+v", resources[0])
	}
}

func TestArticleSearchQueryTargetsPopularHighSignalArticles(t *testing.T) {
	node := model.RoadmapNode{
		Title:                "Go API 椤圭洰",
		Deliverable:          "REST API",
		ArticleSearchQueries: []string{"go api performance tutorial"},
	}
	query := buildArticleSearchQuery(node, nil, selectedArticleSearchSources([]string{"medium", "reddit"}))

	for _, term := range []string{"popular", "most read", "upvoted"} {
		if !strings.Contains(strings.ToLower(query), term) {
			t.Fatalf("search query should target high-signal articles with %q, got %q", term, query)
		}
	}
}

func TestArticleSearchQueryUsesSelectedSourcesAsAlternatives(t *testing.T) {
	node := model.RoadmapNode{
		Title:                "Go API 椤圭洰",
		Deliverable:          "REST API",
		ArticleSearchQueries: []string{"go api performance tutorial"},
	}
	query := buildArticleSearchQuery(node, nil, selectedArticleSearchSources([]string{"medium", "reddit"}))

	if !strings.Contains(query, "site:medium.com OR site:reddit.com") {
		t.Fatalf("selected sources should be alternatives, got %q", query)
	}
}

func TestArticleSearchQueryIncludesNodeDescription(t *testing.T) {
	node := model.RoadmapNode{
		Title:                "vector search basics",
		Description:          "compare HNSW cold start recall strategy and incremental index maintenance",
		Deliverable:          "research notes",
		ArticleSearchQueries: []string{"generic vector database overview"},
	}
	query := buildArticleSearchQuery(node, nil, selectedArticleSearchSources([]string{"technical"}))

	for _, term := range []string{"HNSW", "cold start", "incremental index"} {
		if !strings.Contains(query, term) {
			t.Fatalf("expected node description term %q in article query %q", term, query)
		}
	}
}

func TestRoadmapArticleSearchUsesLinkedTaskContent(t *testing.T) {
	openRoadmapServiceTestDB(t)
	t.Setenv("ARTICLE_SEARCH_PROVIDER", "")

	project := createLearningProjectForTest(t, "vector search system")
	roadmap, err := repository.ReplaceLearningRoadmap(&model.LearningRoadmap{
		ProjectID: project.ID,
		Title:     "vector search roadmap",
		Goal:      "complete vector search system design",
		Status:    "ready",
		Nodes: []model.RoadmapNode{
			{
				ID:                   "node-vector-index",
				Type:                 "task",
				Title:                "search architecture overview",
				Description:          "understand overall system architecture",
				PathType:             "required",
				Status:               "active",
				Deliverable:          "architecture notes",
				AcceptanceCriteria:   "explain the core path",
				ArticleSearchQueries: []string{"generic vector database overview"},
			},
		},
	})
	if err != nil {
		t.Fatalf("save roadmap: %v", err)
	}
	node := roadmap.Nodes[0]
	linkedTask := &model.Task{
		Title:         "research HNSW index tuning",
		Content:       "compare efConstruction, M parameters, and rerank impact on recall",
		Status:        "open",
		Horizon:       "week",
		Scope:         "daily",
		RoadmapNodeID: &node.ID,
	}
	if err := repository.CreateTask(linkedTask); err != nil {
		t.Fatalf("create linked task: %v", err)
	}

	var capturedQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search/advanced" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		capturedQuery = r.URL.Query().Get("q")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[
			{"title":"HNSW Parameter Tuning Guide","link":"https://stackoverflow.com/questions/1","score":42,"answer_count":3},
			{"title":"Vector Search Rerank Tradeoffs","link":"https://stackoverflow.com/questions/2","score":31,"answer_count":2},
			{"title":"Approximate Nearest Neighbor Recall","link":"https://stackoverflow.com/questions/3","score":29,"answer_count":4},
			{"title":"ANN Index Benchmarks","link":"https://stackoverflow.com/questions/4","score":25,"answer_count":1},
			{"title":"HNSW efConstruction Explained","link":"https://stackoverflow.com/questions/5","score":22,"answer_count":2},
			{"title":"Vector Database Ranking","link":"https://stackoverflow.com/questions/6","score":20,"answer_count":1},
			{"title":"Embedding Search Evaluation","link":"https://stackoverflow.com/questions/7","score":19,"answer_count":1},
			{"title":"Hybrid Search Design","link":"https://stackoverflow.com/questions/8","score":18,"answer_count":1},
			{"title":"Recall Precision Tradeoff","link":"https://stackoverflow.com/questions/9","score":17,"answer_count":1},
			{"title":"Rerank Pipeline Design","link":"https://stackoverflow.com/questions/10","score":16,"answer_count":1}
		]}`))
	}))
	defer server.Close()
	oldStackExchangeURL := stackExchangeSearchURL
	stackExchangeSearchURL = server.URL + "/search/advanced"
	t.Cleanup(func() { stackExchangeSearchURL = oldStackExchangeURL })

	resources, err := SearchRoadmapNodeResources(roadmapTestContext(t), roadmapTestStore(t), node.ID, &model.SearchRoadmapResourcesRequest{Sources: []string{"stackoverflow"}})
	if err != nil {
		t.Fatalf("search resources: %v", err)
	}
	if len(resources) < 10 {
		t.Fatalf("expected stackoverflow resources, got %+v", resources)
	}
	for _, term := range []string{"HNSW", "efConstruction", "rerank"} {
		if !strings.Contains(capturedQuery, term) {
			t.Fatalf("expected linked task term %q in article query %q", term, capturedQuery)
		}
	}
}

func TestPublicSourceArticleSearchReturnsRealArticlesOnRepeatedCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/articles" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{
				"title": "Rate Limiting Strategies in Go",
				"url": "https://dev.to/example/rate-limiting-strategies-in-go",
				"description": "A practical Go API article.",
				"public_reactions_count": 342,
				"reading_time_minutes": 8
			}
		]`))
	}))
	t.Cleanup(server.Close)

	originalDevToURL := devToArticlesURL
	devToArticlesURL = server.URL + "/articles"
	t.Cleanup(func() { devToArticlesURL = originalDevToURL })

	node := model.RoadmapNode{
		Title:                "Go API 椤圭洰",
		Deliverable:          "REST API",
		ArticleSearchQueries: []string{"go api performance tutorial"},
	}
	sources := selectedArticleSearchSources([]string{"devto"})

	for attempt := 1; attempt <= 2; attempt++ {
		resources := searchPublicSourceResources(node, nil, sources, 10)
		if len(resources) != 1 {
			t.Fatalf("attempt %d resources = %d, want 1: %+v", attempt, len(resources), resources)
		}
		if resources[0].Title != "Rate Limiting Strategies in Go" {
			t.Fatalf("attempt %d should show the real article title, got %+v", attempt, resources[0])
		}
		if strings.Contains(resources[0].URL, "/search") {
			t.Fatalf("attempt %d returned a search entry instead of an article: %+v", attempt, resources[0])
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
