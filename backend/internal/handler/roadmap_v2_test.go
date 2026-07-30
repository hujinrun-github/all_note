package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/taskapp"
	"github.com/hujinrun/flowspace/internal/taskdomain"
)

func TestRoadmapV2RoutesReturnDerivedProgressAndDeleteConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	app := &roadmapV2AppFake{taskDomainV2ApplicationFake: taskDomainV2ApplicationFake{}}
	r := gin.New()
	r.Use(func(c *gin.Context) {
		ctx := auth.ContextWithIdentity(c.Request.Context(), auth.RequestIdentity{UserID: "u1", WorkspaceID: "w1"})
		ctx = auth.ContextWithWorkspaceScope(ctx, "w1")
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	RegisterTaskDomainV2Routes(r.Group("/api"), app)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/projects/p1/roadmap", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"blocked":1`) || !strings.Contains(w.Body.String(), `"nodes"`) {
		t.Fatalf("GET status=%d body=%s", w.Code, w.Body.String())
	}
	app.deleteErr = taskdomain.ErrRoadmapNodeHasTasks
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/api/roadmaps/r1/nodes/n1?expected_revision=3", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "roadmap_node_has_tasks") {
		t.Fatalf("DELETE status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestRoadmapV2GenerationCreatesACompleteLearningPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	app := &roadmapV2AppFake{
		taskDomainV2ApplicationFake: taskDomainV2ApplicationFake{},
		roadmapMissing:              true,
	}
	r := gin.New()
	r.Use(func(c *gin.Context) {
		ctx := auth.ContextWithIdentity(c.Request.Context(), auth.RequestIdentity{UserID: "u1", WorkspaceID: "w1"})
		ctx = auth.ContextWithWorkspaceScope(ctx, "w1")
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	RegisterTaskDomainV2Routes(r.Group("/api"), app)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/p1/roadmap/generate",
		strings.NewReader(`{}`),
	)
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("POST status=%d body=%s", w.Code, w.Body.String())
	}
	if app.createdNodes < 14 {
		t.Fatalf("generated nodes = %d, want at least 14", app.createdNodes)
	}
	if app.lastParentID == "" {
		t.Fatal("generated path did not link nodes in sequence")
	}
}

func TestRoadmapV2GenerationAllowsFullAIResponseWindow(t *testing.T) {
	gin.SetMode(gin.TestMode)
	app := &roadmapV2AppFake{
		taskDomainV2ApplicationFake: taskDomainV2ApplicationFake{},
		roadmapMissing:              true,
	}
	chat := &deadlineRecordingRoadmapChat{}
	r := gin.New()
	r.Use(func(c *gin.Context) {
		ctx := auth.ContextWithIdentity(c.Request.Context(), auth.RequestIdentity{UserID: "u1", WorkspaceID: "w1"})
		ctx = auth.ContextWithWorkspaceScope(ctx, "w1")
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	RegisterTaskDomainV2RoutesWithAI(r.Group("/api"), app, chat)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/p1/roadmap/generate",
		strings.NewReader(`{"prompt":"include distributed training"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("POST status=%d body=%s", w.Code, w.Body.String())
	}
	if chat.remaining < time.Minute {
		t.Fatalf("AI generation window=%s, want at least one minute", chat.remaining)
	}
}

func TestRoadmapV2GenerationReplacesAnUnlinkedExistingPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	app := &roadmapV2AppFake{
		taskDomainV2ApplicationFake: taskDomainV2ApplicationFake{},
		existingNodeTasksSet:        true,
	}
	r := gin.New()
	r.Use(func(c *gin.Context) {
		ctx := auth.ContextWithIdentity(c.Request.Context(), auth.RequestIdentity{UserID: "u1", WorkspaceID: "w1"})
		ctx = auth.ContextWithWorkspaceScope(ctx, "w1")
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	RegisterTaskDomainV2Routes(r.Group("/api"), app)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/projects/p1/roadmap/generate", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("POST status=%d body=%s", w.Code, w.Body.String())
	}
	if app.deletedNodes != 1 {
		t.Fatalf("deleted nodes = %d, want 1", app.deletedNodes)
	}
	if app.createdNodes < 14 {
		t.Fatalf("generated nodes = %d, want at least 14", app.createdNodes)
	}
}

func TestRoadmapV2GenerationProtectsNodesWithLinkedTasks(t *testing.T) {
	gin.SetMode(gin.TestMode)
	app := &roadmapV2AppFake{
		taskDomainV2ApplicationFake: taskDomainV2ApplicationFake{},
		existingNodeTasks:           2,
		existingNodeTasksSet:        true,
	}
	r := gin.New()
	r.Use(func(c *gin.Context) {
		ctx := auth.ContextWithIdentity(c.Request.Context(), auth.RequestIdentity{UserID: "u1", WorkspaceID: "w1"})
		ctx = auth.ContextWithWorkspaceScope(ctx, "w1")
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	RegisterTaskDomainV2Routes(r.Group("/api"), app)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/projects/p1/roadmap/generate", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "roadmap_node_has_tasks") {
		t.Fatalf("POST status=%d body=%s", w.Code, w.Body.String())
	}
	if app.deletedNodes != 0 || app.createdNodes != 0 {
		t.Fatalf("regeneration mutated protected path: deleted=%d created=%d", app.deletedNodes, app.createdNodes)
	}
}

type deadlineRecordingRoadmapChat struct {
	remaining time.Duration
}

func (c *deadlineRecordingRoadmapChat) Generate(ctx context.Context, _, _, _ string) (string, error) {
	if deadline, ok := ctx.Deadline(); ok {
		c.remaining = time.Until(deadline)
	}
	return `{"title":"AI Roadmap","goal":"Learn","nodes":[{"id":"start","title":"Start","description":"Begin"}],"edges":[]}`, nil
}

func (*deadlineRecordingRoadmapChat) ResolveFeature(context.Context, string, string) (bool, string, error) {
	return true, "template", nil
}

type roadmapV2AppFake struct {
	taskDomainV2ApplicationFake
	deleteErr            error
	roadmapMissing       bool
	roadmapCreated       bool
	existingNodeTasks    int
	existingNodeTasksSet bool
	deletedNodes         int
	createdNodes         int
	lastParentID         string
}

func (f *roadmapV2AppFake) GetProject(_ context.Context, request taskapp.EntityQueryRequest) (taskdomain.ProjectSnapshot, error) {
	return taskdomain.ProjectSnapshot{
		Project: taskdomain.Project{
			WorkspaceID: request.WorkspaceID,
			ID:          request.EntityID,
			Name:        "日语学习",
			Kind:        taskdomain.ProjectKindLearning,
			Horizon:     taskdomain.ProjectHorizonLong,
			Status:      taskdomain.ProjectStatusActive,
		},
		Revision: 1,
	}, nil
}
func (f *roadmapV2AppFake) GetRoadmap(context.Context, taskapp.EntityQueryRequest) (taskdomain.RoadmapSnapshot, error) {
	if f.roadmapMissing && !f.roadmapCreated {
		return taskdomain.RoadmapSnapshot{}, taskdomain.ErrRoadmapNotFound
	}
	nodeTasks := 2
	if f.existingNodeTasksSet {
		nodeTasks = f.existingNodeTasks
	}
	nodes := []taskdomain.RoadmapNodeSnapshot{{
		Node: taskdomain.RoadmapNode{ID: "n1", ProjectID: "p1", RoadmapID: "r1", Title: "Node", Type: taskdomain.RoadmapNodeTopic, Revision: 3},
		Progress: taskdomain.RoadmapNodeProgress{
			Tasks:   nodeTasks,
			Total:   nodeTasks,
			Open:    nodeTasks,
			Blocked: 1,
		},
	}}
	if f.roadmapCreated {
		nodes = nil
	}
	return taskdomain.RoadmapSnapshot{Roadmap: taskdomain.LearningRoadmap{ID: "r1", ProjectID: "p1", Status: taskdomain.RoadmapStatusActive, Title: "Path", Revision: 1}, Nodes: nodes}, nil
}
func (f *roadmapV2AppFake) CreateRoadmap(context.Context, taskapp.CreateRoadmapRequest) (taskdomain.RoadmapSnapshot, error) {
	f.roadmapCreated = true
	return taskdomain.RoadmapSnapshot{Roadmap: taskdomain.LearningRoadmap{ID: "r1", ProjectID: "p1", Status: taskdomain.RoadmapStatusActive, Title: "Path", Revision: 1}}, nil
}
func (f *roadmapV2AppFake) CreateRoadmapNode(_ context.Context, request taskapp.CreateRoadmapNodeRequest) (taskdomain.RoadmapNodeSnapshot, error) {
	f.createdNodes++
	f.lastParentID = request.ParentID
	return taskdomain.RoadmapNodeSnapshot{Node: taskdomain.RoadmapNode{
		ID:        fmt.Sprintf("generated-%d", f.createdNodes),
		ProjectID: "p1",
		RoadmapID: request.RoadmapID,
		ParentID:  request.ParentID,
		Title:     request.Title,
		Type:      request.Type,
		Revision:  1,
	}}, nil
}
func (f *roadmapV2AppFake) ReplaceRoadmapNodes(_ context.Context, request taskapp.ReplaceRoadmapNodesRequest) (taskdomain.RoadmapSnapshot, error) {
	nodeTasks := 2
	if f.existingNodeTasksSet {
		nodeTasks = f.existingNodeTasks
	}
	if !f.roadmapMissing && nodeTasks > 0 {
		return taskdomain.RoadmapSnapshot{}, taskdomain.ErrRoadmapNodeHasTasks
	}
	if !f.roadmapMissing {
		f.deletedNodes++
	}
	f.createdNodes += len(request.Nodes)
	nodes := make([]taskdomain.RoadmapNodeSnapshot, 0, len(request.Nodes))
	for index, node := range request.Nodes {
		parentID := ""
		if node.ParentIndex >= 0 {
			parentID = fmt.Sprintf("generated-%d", node.ParentIndex+1)
			f.lastParentID = parentID
		}
		nodes = append(nodes, taskdomain.RoadmapNodeSnapshot{Node: taskdomain.RoadmapNode{
			ID:        fmt.Sprintf("generated-%d", index+1),
			ProjectID: "p1",
			RoadmapID: request.RoadmapID,
			ParentID:  parentID,
			Title:     node.Title,
			Type:      node.Type,
			Revision:  1,
		}})
	}
	return taskdomain.RoadmapSnapshot{
		Roadmap: taskdomain.LearningRoadmap{ID: request.RoadmapID, ProjectID: "p1", Status: taskdomain.RoadmapStatusActive, Title: "Path", Revision: 1},
		Nodes:   nodes,
	}, nil
}
func (f *roadmapV2AppFake) UpdateRoadmapNode(context.Context, taskapp.UpdateRoadmapNodeRequest) (taskdomain.RoadmapNodeSnapshot, error) {
	return taskdomain.RoadmapNodeSnapshot{}, nil
}
func (f *roadmapV2AppFake) DeleteRoadmapNode(context.Context, taskapp.DeleteRoadmapNodeRequest) error {
	if f.deleteErr == nil {
		f.deletedNodes++
	}
	return f.deleteErr
}
