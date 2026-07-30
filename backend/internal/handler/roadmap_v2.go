package handler

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/service"
	"github.com/hujinrun/flowspace/internal/taskapp"
	"github.com/hujinrun/flowspace/internal/taskdomain"
)

type RoadmapV2Application interface {
	GetRoadmap(context.Context, taskapp.EntityQueryRequest) (taskdomain.RoadmapSnapshot, error)
	CreateRoadmap(context.Context, taskapp.CreateRoadmapRequest) (taskdomain.RoadmapSnapshot, error)
	CreateRoadmapNode(context.Context, taskapp.CreateRoadmapNodeRequest) (taskdomain.RoadmapNodeSnapshot, error)
	UpdateRoadmapNode(context.Context, taskapp.UpdateRoadmapNodeRequest) (taskdomain.RoadmapNodeSnapshot, error)
	DeleteRoadmapNode(context.Context, taskapp.DeleteRoadmapNodeRequest) error
	ReplaceRoadmapNodes(context.Context, taskapp.ReplaceRoadmapNodesRequest) (taskdomain.RoadmapSnapshot, error)
}

var _ RoadmapV2Application = (*taskapp.Facade)(nil)

const roadmapGenerationTimeout = 90 * time.Second

type roadmapCreateDTO struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}
type roadmapNodeMutationDTO struct {
	ParentID         string                     `json:"parent_id"`
	Title            string                     `json:"title"`
	Description      string                     `json:"description"`
	Type             taskdomain.RoadmapNodeType `json:"node_type"`
	Position         float64                    `json:"position"`
	ExpectedRevision int64                      `json:"expected_revision,omitempty"`
}
type roadmapNodeDTO struct {
	ID          string                         `json:"id"`
	ProjectID   string                         `json:"project_id"`
	RoadmapID   string                         `json:"roadmap_id"`
	ParentID    string                         `json:"parent_id,omitempty"`
	Title       string                         `json:"title"`
	Description string                         `json:"description"`
	Type        taskdomain.RoadmapNodeType     `json:"node_type"`
	Position    float64                        `json:"position"`
	Revision    int64                          `json:"revision"`
	Progress    taskdomain.RoadmapNodeProgress `json:"progress"`
}
type roadmapEdgeDTO struct {
	ID         string                     `json:"id"`
	FromNodeID string                     `json:"from_node_id"`
	ToNodeID   string                     `json:"to_node_id"`
	Type       taskdomain.RoadmapEdgeType `json:"edge_type"`
	Revision   int64                      `json:"revision"`
}
type roadmapDTO struct {
	ID          string                   `json:"id"`
	ProjectID   string                   `json:"project_id"`
	Title       string                   `json:"title"`
	Description string                   `json:"description"`
	Status      taskdomain.RoadmapStatus `json:"status"`
	Revision    int64                    `json:"revision"`
	Nodes       []roadmapNodeDTO         `json:"nodes"`
	Edges       []roadmapEdgeDTO         `json:"edges"`
}

func (h taskDomainV2Handler) roadmapApplication(c *gin.Context) (RoadmapV2Application, bool) {
	app, ok := h.application.(RoadmapV2Application)
	if !ok || app == nil {
		writeTaskDomainError(c, taskapp.ErrInvalidRuntime)
		return nil, false
	}
	return app, true
}
func (h taskDomainV2Handler) getRoadmap(c *gin.Context) {
	id, ok := taskDomainAuthenticatedIdentity(c)
	if !ok {
		return
	}
	app, ok := h.roadmapApplication(c)
	if !ok {
		return
	}
	rm, err := app.GetRoadmap(c.Request.Context(), taskapp.EntityQueryRequest{WorkspaceID: id.workspaceID, ActorID: id.actorID, EntityID: c.Param("projectID")})
	if err != nil {
		writeTaskDomainError(c, err)
		return
	}
	success(c, gin.H{"roadmap": roadmapV2DTO(rm)})
}
func (h taskDomainV2Handler) createRoadmap(c *gin.Context) {
	id, ok := taskDomainAuthenticatedIdentity(c)
	if !ok {
		return
	}
	app, ok := h.roadmapApplication(c)
	if !ok {
		return
	}
	var body roadmapCreateDTO
	if !decodeTaskDomainRequest(c, &body) {
		return
	}
	if strings.TrimSpace(body.Title) == "" {
		writeTaskDomainError(c, ErrInvalidTaskDomainRequest)
		return
	}
	rm, err := app.CreateRoadmap(c.Request.Context(), taskapp.CreateRoadmapRequest{WorkspaceID: id.workspaceID, ActorID: id.actorID, ProjectID: c.Param("projectID"), Title: body.Title, Description: body.Description})
	if err != nil {
		writeTaskDomainError(c, err)
		return
	}
	created(c, gin.H{"roadmap": roadmapV2DTO(rm)})
}

func (h taskDomainV2Handler) generateRoadmap(c *gin.Context) {
	id, ok := taskDomainAuthenticatedIdentity(c)
	if !ok {
		return
	}
	app, ok := h.roadmapApplication(c)
	if !ok {
		return
	}
	var body model.GenerateLearningRoadmapRequest
	if err := c.ShouldBindJSON(&body); err != nil && !errors.Is(err, io.EOF) {
		badRequest(c, "invalid roadmap generation prompt")
		return
	}

	project, err := h.application.GetProject(
		c.Request.Context(),
		taskapp.EntityQueryRequest{
			WorkspaceID: id.workspaceID,
			ActorID:     id.actorID,
			EntityID:    c.Param("projectID"),
		},
	)
	if err != nil {
		writeTaskDomainError(c, err)
		return
	}
	if project.Project.Kind != taskdomain.ProjectKindLearning {
		writeTaskDomainError(c, taskdomain.ErrRoadmapRequiresLearningProject)
		return
	}

	var generator service.TextGenerator
	allowTemplateFallback := true
	if h.chat != nil {
		if features, featureAware := h.chat.(WorkspaceAIFeatureService); featureAware {
			enabled, fallback, resolveErr := features.ResolveFeature(
				c.Request.Context(),
				id.workspaceID,
				"roadmap_generation",
			)
			if resolveErr != nil {
				internalError(c, "unable to resolve roadmap AI settings")
				return
			}
			// A configured AI route may still be temporarily unavailable. An
			// unedited request can safely degrade to the deterministic learning
			// template; an edited prompt is rejected by the service instead of
			// pretending its custom requirements were applied.
			allowTemplateFallback = fallback == "template" || enabled
			if enabled {
				generator = workspaceTextGenerator{
					service:     h.chat,
					workspaceID: id.workspaceID,
				}
			}
		} else {
			generator = workspaceTextGenerator{
				service:     h.chat,
				workspaceID: id.workspaceID,
			}
		}
	}

	generationContext, cancelGeneration := context.WithTimeout(
		c.Request.Context(),
		roadmapGenerationTimeout,
	)
	defer cancelGeneration()
	plan, err := service.GenerateLearningRoadmapPlan(
		generationContext,
		project.Project.Name,
		body.Prompt,
		generator,
		allowTemplateFallback,
	)
	if err != nil {
		log.Printf("roadmap v2 generation failed workspace=%s project=%s: %v", id.workspaceID, project.Project.ID, err)
		internalError(c, err.Error())
		return
	}

	roadmap, err := app.GetRoadmap(
		c.Request.Context(),
		taskapp.EntityQueryRequest{
			WorkspaceID: id.workspaceID,
			ActorID:     id.actorID,
			EntityID:    project.Project.ID,
		},
	)
	switch {
	case err == nil:
	case errors.Is(err, taskdomain.ErrRoadmapNotFound):
		roadmap, err = app.CreateRoadmap(
			c.Request.Context(),
			taskapp.CreateRoadmapRequest{
				WorkspaceID: id.workspaceID,
				ActorID:     id.actorID,
				ProjectID:   project.Project.ID,
				Title:       plan.Title,
				Description: plan.Description,
			},
		)
		if err != nil {
			writeTaskDomainError(c, err)
			return
		}
	default:
		writeTaskDomainError(c, err)
		return
	}

	replacements := make([]taskapp.RoadmapNodeReplacement, 0, len(plan.Nodes))
	for index, node := range plan.Nodes {
		parentIndex := index - 1
		replacements = append(replacements, taskapp.RoadmapNodeReplacement{
			Title:       node.Title,
			Description: node.Description,
			Type:        generatedRoadmapNodeType(node.Type),
			Position:    node.Position,
			ParentIndex: parentIndex,
		})
	}
	generated, err := app.ReplaceRoadmapNodes(
		c.Request.Context(),
		taskapp.ReplaceRoadmapNodesRequest{
			WorkspaceID: id.workspaceID,
			ActorID:     id.actorID,
			RoadmapID:   roadmap.Roadmap.ID,
			Nodes:       replacements,
		},
	)
	if err != nil {
		log.Printf("roadmap v2 replacement failed workspace=%s project=%s roadmap=%s: %v", id.workspaceID, project.Project.ID, roadmap.Roadmap.ID, err)
		writeTaskDomainError(c, err)
		return
	}
	created(c, gin.H{"roadmap": roadmapV2DTO(generated)})
}

func generatedRoadmapNodeType(value string) taskdomain.RoadmapNodeType {
	switch value {
	case string(taskdomain.RoadmapNodeStage):
		return taskdomain.RoadmapNodeStage
	case string(taskdomain.RoadmapNodeMilestone):
		return taskdomain.RoadmapNodeMilestone
	default:
		return taskdomain.RoadmapNodeTopic
	}
}
func (h taskDomainV2Handler) createRoadmapNode(c *gin.Context) {
	id, ok := taskDomainAuthenticatedIdentity(c)
	if !ok {
		return
	}
	app, ok := h.roadmapApplication(c)
	if !ok {
		return
	}
	var b roadmapNodeMutationDTO
	if !decodeTaskDomainRequest(c, &b) {
		return
	}
	n, err := app.CreateRoadmapNode(c.Request.Context(), taskapp.CreateRoadmapNodeRequest{WorkspaceID: id.workspaceID, ActorID: id.actorID, RoadmapID: c.Param("id"), ParentID: b.ParentID, Title: b.Title, Description: b.Description, Type: b.Type, Position: b.Position})
	if err != nil {
		writeTaskDomainError(c, err)
		return
	}
	created(c, gin.H{"node": roadmapNodeV2DTO(n)})
}
func (h taskDomainV2Handler) updateRoadmapNode(c *gin.Context) {
	id, ok := taskDomainAuthenticatedIdentity(c)
	if !ok {
		return
	}
	app, ok := h.roadmapApplication(c)
	if !ok {
		return
	}
	var b roadmapNodeMutationDTO
	if !decodeTaskDomainRequest(c, &b) {
		return
	}
	n, err := app.UpdateRoadmapNode(c.Request.Context(), taskapp.UpdateRoadmapNodeRequest{WorkspaceID: id.workspaceID, ActorID: id.actorID, RoadmapID: c.Param("id"), NodeID: c.Param("nodeID"), ParentID: b.ParentID, Title: b.Title, Description: b.Description, Type: b.Type, Position: b.Position, ExpectedRevision: b.ExpectedRevision})
	if err != nil {
		writeTaskDomainError(c, err)
		return
	}
	success(c, gin.H{"node": roadmapNodeV2DTO(n)})
}
func (h taskDomainV2Handler) deleteRoadmapNode(c *gin.Context) {
	id, ok := taskDomainAuthenticatedIdentity(c)
	if !ok {
		return
	}
	app, ok := h.roadmapApplication(c)
	if !ok {
		return
	}
	rev, err := strconv.ParseInt(c.Query("expected_revision"), 10, 64)
	if err != nil || rev < 1 {
		writeTaskDomainError(c, ErrInvalidTaskDomainRequest)
		return
	}
	err = app.DeleteRoadmapNode(c.Request.Context(), taskapp.DeleteRoadmapNodeRequest{WorkspaceID: id.workspaceID, ActorID: id.actorID, RoadmapID: c.Param("id"), NodeID: c.Param("nodeID"), ExpectedRevision: rev})
	if err != nil {
		writeTaskDomainError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
func roadmapV2DTO(r taskdomain.RoadmapSnapshot) roadmapDTO {
	d := roadmapDTO{ID: r.Roadmap.ID, ProjectID: r.Roadmap.ProjectID, Title: r.Roadmap.Title, Description: r.Roadmap.Description, Status: r.Roadmap.Status, Revision: r.Roadmap.Revision, Nodes: make([]roadmapNodeDTO, 0, len(r.Nodes)), Edges: make([]roadmapEdgeDTO, 0, len(r.Edges))}
	for _, n := range r.Nodes {
		d.Nodes = append(d.Nodes, roadmapNodeV2DTO(n))
	}
	for _, e := range r.Edges {
		d.Edges = append(d.Edges, roadmapEdgeDTO{ID: e.ID, FromNodeID: e.FromNodeID, ToNodeID: e.ToNodeID, Type: e.Type, Revision: e.Revision})
	}
	return d
}
func roadmapNodeV2DTO(n taskdomain.RoadmapNodeSnapshot) roadmapNodeDTO {
	return roadmapNodeDTO{ID: n.Node.ID, ProjectID: n.Node.ProjectID, RoadmapID: n.Node.RoadmapID, ParentID: n.Node.ParentID, Title: n.Node.Title, Description: n.Node.Description, Type: n.Node.Type, Position: n.Node.Position, Revision: n.Node.Revision, Progress: n.Progress}
}
