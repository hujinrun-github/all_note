package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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

type roadmapV2AppFake struct {
	taskDomainV2ApplicationFake
	deleteErr error
}

func (f *roadmapV2AppFake) GetRoadmap(context.Context, taskapp.EntityQueryRequest) (taskdomain.RoadmapSnapshot, error) {
	return taskdomain.RoadmapSnapshot{Roadmap: taskdomain.LearningRoadmap{ID: "r1", ProjectID: "p1", Status: taskdomain.RoadmapStatusActive, Title: "Path", Revision: 1}, Nodes: []taskdomain.RoadmapNodeSnapshot{{Node: taskdomain.RoadmapNode{ID: "n1", ProjectID: "p1", RoadmapID: "r1", Title: "Node", Type: taskdomain.RoadmapNodeTopic, Revision: 3}, Progress: taskdomain.RoadmapNodeProgress{Tasks: 2, Total: 2, Open: 1, Blocked: 1}}}}, nil
}
func (f *roadmapV2AppFake) CreateRoadmap(context.Context, taskapp.CreateRoadmapRequest) (taskdomain.RoadmapSnapshot, error) {
	return taskdomain.RoadmapSnapshot{}, nil
}
func (f *roadmapV2AppFake) CreateRoadmapNode(context.Context, taskapp.CreateRoadmapNodeRequest) (taskdomain.RoadmapNodeSnapshot, error) {
	return taskdomain.RoadmapNodeSnapshot{}, nil
}
func (f *roadmapV2AppFake) UpdateRoadmapNode(context.Context, taskapp.UpdateRoadmapNodeRequest) (taskdomain.RoadmapNodeSnapshot, error) {
	return taskdomain.RoadmapNodeSnapshot{}, nil
}
func (f *roadmapV2AppFake) DeleteRoadmapNode(context.Context, taskapp.DeleteRoadmapNodeRequest) error {
	return f.deleteErr
}
