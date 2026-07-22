package router

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/handler"
	"github.com/hujinrun/flowspace/internal/taskapp"
	"github.com/hujinrun/flowspace/internal/taskdomain"
)

type taskDomainRouteDispatcher struct {
	selector taskapp.ModelSelector
	runtime  taskapp.RuntimeResolver
	legacy   http.Handler
	v2       http.Handler
	compat   http.Handler
}

func registerTaskDomainRoutes(routes *gin.RouterGroup, cfg Config) {
	if cfg.TaskDomainModelSelector == nil && cfg.TaskDomainV2Runtime == nil {
		registerLegacyTaskDomainRoutes(routes, cfg)
		routes.GET("/task-domain/capabilities", taskDomainLegacyCapabilities)
		return
	}

	dispatcher := taskDomainRouteDispatcher{
		selector: cfg.TaskDomainModelSelector,
		runtime:  cfg.TaskDomainV2Runtime,
		legacy:   legacyTaskDomainDelegate(cfg),
		v2:       taskDomainV2Delegate(newTaskDomainV2Application(cfg.TaskDomainV2Runtime)),
	}
	dispatcher.compat = legacyWebTaskDomainV2Delegate(newTaskDomainV2Application(cfg.TaskDomainV2Runtime))
	registerModelAwareTaskDomainRoutes(routes, dispatcher)
	routes.GET("/task-domain/capabilities", dispatcher.capabilities)
}

func registerLegacyTaskDomainRoutes(routes *gin.RouterGroup, cfg Config) {
	routes.GET("/tasks", handler.GetTasks(cfg.Store))
	routes.GET("/tasks/projects", handler.GetTaskProjects(cfg.Store))
	routes.POST("/tasks", handler.CreateTask(cfg.Store))
	routes.PATCH("/tasks/:id", handler.UpdateTask(cfg.Store))
	routes.DELETE("/tasks/:id", handler.DeleteTask(cfg.Store))
	routes.POST("/tasks/:id/occurrences/:date/complete", handler.CompleteOccurrence(cfg.Store))
	routes.POST("/tasks/:id/occurrences/:date/reopen", handler.ReopenOccurrence(cfg.Store))
	routes.POST("/tasks/:id/occurrences/:date/skip", handler.SkipOccurrence(cfg.Store))
	routes.GET("/task-occurrences", handler.GetTaskOccurrences(cfg.Store))
	routes.GET("/task-projects", handler.ListTaskProjects(cfg.Store))
	routes.POST("/task-projects", handler.CreateTaskProject(cfg.Store))
	routes.PATCH("/task-projects/:id", handler.UpdateTaskProject(cfg.Store))
	routes.DELETE("/task-projects/:id", handler.DeleteTaskProject(cfg.Store))
	routes.POST("/task-projects/:id/roadmap/generate", handler.GenerateLearningRoadmapWithAI(cfg.Store, cfg.AIChat))
	routes.GET("/task-projects/:id/roadmap", handler.GetLearningRoadmap(cfg.Store))
	routes.POST("/roadmaps/:id/nodes", handler.CreateRoadmapNode(cfg.Store))
	routes.PATCH("/roadmap-nodes/:id", handler.UpdateRoadmapNode(cfg.Store))
	routes.DELETE("/roadmap-nodes/:id", handler.DeleteRoadmapNode(cfg.Store))
	routes.POST("/roadmap-nodes/:id/resources/search", handler.SearchRoadmapNodeResources(cfg.Store))
	routes.POST("/roadmap-nodes/:id/resources", handler.AddRoadmapNodeResource(cfg.Store))
	routes.PATCH("/roadmaps/:id/layout", handler.UpdateRoadmapLayout(cfg.Store))
	routes.POST("/roadmaps/:id/layout/optimize", handler.OptimizeRoadmapLayout(cfg.Store))
	routes.DELETE("/roadmap-resources/:id", handler.DeleteRoadmapResource(cfg.Store))
	routes.GET("/events", handler.GetEvents(cfg.Store))
	routes.POST("/events", handler.CreateEvent(cfg.Store))
	routes.PATCH("/events/:id", handler.UpdateEvent(cfg.Store))
	routes.DELETE("/events/:id", handler.DeleteEvent(cfg.Store))
	routes.GET("/calendar/project-sources", handler.GetCalendarProjectSources(cfg.Store))
	routes.PUT("/calendar/project-sources", handler.SaveCalendarProjectSources(cfg.Store))
	routes.GET("/today", handler.GetToday(cfg.Store))
	routes.GET("/summary", handler.GetSummary(cfg.Store))
}

func legacyTaskDomainDelegate(cfg Config) http.Handler {
	router := gin.New()
	api := router.Group("/api")
	registerLegacyTaskDomainRoutes(api, cfg)
	return router
}

func taskDomainV2Delegate(application handler.TaskDomainV2Application) http.Handler {
	router := gin.New()
	handler.RegisterTaskDomainV2Routes(router.Group("/api"), application)
	return router
}

func legacyWebTaskDomainV2Delegate(application handler.LegacyWebTaskDomainApplication) http.Handler {
	router := gin.New()
	handler.RegisterLegacyWebTaskDomainV2Routes(router.Group("/api"), application)
	return router
}

func registerModelAwareTaskDomainRoutes(routes *gin.RouterGroup, dispatcher taskDomainRouteDispatcher) {
	shared := dispatcher.handler(true, true)
	legacyOnly := dispatcher.handler(true, false)
	v2Only := dispatcher.handler(false, true)

	routes.GET("/tasks", dispatcher.taskContractHandler())
	routes.POST("/tasks", dispatcher.taskContractHandler())
	routes.PATCH("/tasks/:taskID", dispatcher.taskContractHandler())
	routes.GET("/task-occurrences", shared)

	routes.GET("/tasks/projects", legacyOnly)
	routes.DELETE("/tasks/:taskID", dispatcher.handlerWithV2(true, dispatcher.compat))
	routes.POST("/tasks/:taskID/occurrences/:date/complete", dispatcher.handlerWithV2(true, dispatcher.compat))
	routes.POST("/tasks/:taskID/occurrences/:date/reopen", dispatcher.handlerWithV2(true, dispatcher.compat))
	routes.POST("/tasks/:taskID/occurrences/:date/skip", dispatcher.handlerWithV2(true, dispatcher.compat))
	routes.GET("/task-projects", legacyOnly)
	routes.POST("/task-projects", legacyOnly)
	routes.PATCH("/task-projects/:id", legacyOnly)
	routes.DELETE("/task-projects/:id", legacyOnly)
	routes.POST("/task-projects/:id/roadmap/generate", legacyOnly)
	routes.GET("/task-projects/:id/roadmap", legacyOnly)
	routes.POST("/roadmaps/:id/nodes", shared)
	routes.PATCH("/roadmap-nodes/:id", legacyOnly)
	routes.DELETE("/roadmap-nodes/:id", legacyOnly)
	routes.POST("/roadmap-nodes/:id/resources/search", legacyOnly)
	routes.POST("/roadmap-nodes/:id/resources", legacyOnly)
	routes.PATCH("/roadmaps/:id/layout", legacyOnly)
	routes.POST("/roadmaps/:id/layout/optimize", legacyOnly)
	routes.DELETE("/roadmap-resources/:id", legacyOnly)
	routes.GET("/events", dispatcher.handlerWithV2(true, dispatcher.compat))
	routes.POST("/events", dispatcher.handlerWithV2(true, dispatcher.compat))
	routes.PATCH("/events/:id", dispatcher.handlerWithV2(true, dispatcher.compat))
	routes.DELETE("/events/:id", dispatcher.handlerWithV2(true, dispatcher.compat))
	routes.GET("/calendar/project-sources", legacyOnly)
	routes.PUT("/calendar/project-sources", legacyOnly)
	routes.GET("/today", legacyOnly)
	routes.GET("/summary", legacyOnly)

	routes.GET("/projects", v2Only)
	routes.POST("/projects", v2Only)
	routes.GET("/projects/:projectID", v2Only)
	routes.PATCH("/projects/:projectID", v2Only)
	routes.POST("/projects/:projectID/complete", v2Only)
	routes.POST("/projects/:projectID/archive", v2Only)
	routes.DELETE("/projects/:projectID", v2Only)
	routes.GET("/tasks/:taskID", v2Only)
	for _, command := range []taskdomain.TaskLifecycleCommand{
		taskdomain.TaskCommandPublish, taskdomain.TaskCommandPause, taskdomain.TaskCommandResume,
		taskdomain.TaskCommandCancel, taskdomain.TaskCommandRestore, taskdomain.TaskCommandArchive,
	} {
		routes.POST("/tasks/:taskID/"+string(command), v2Only)
	}
	routes.POST("/tasks/:taskID/schedule/this-and-following", v2Only)
	routes.GET("/task-occurrences/:occurrenceID", v2Only)
	for _, command := range []taskdomain.OccurrenceCommand{
		taskdomain.OccurrenceCommandStart, taskdomain.OccurrenceCommandBlock, taskdomain.OccurrenceCommandUnblock,
		taskdomain.OccurrenceCommandComplete, taskdomain.OccurrenceCommandSkip, taskdomain.OccurrenceCommandCancel,
		taskdomain.OccurrenceCommandReopen,
	} {
		routes.POST("/task-occurrences/:occurrenceID/"+string(command), v2Only)
	}
	routes.PATCH("/task-occurrences/:occurrenceID/schedule/only-this", v2Only)
	routes.PATCH("/task-occurrences/:occurrenceID", v2Only)
	routes.GET("/calendar/entries", v2Only)
	routes.GET("/projects/:projectID/roadmap", v2Only)
	routes.POST("/projects/:projectID/roadmap", v2Only)
	routes.PATCH("/roadmaps/:id/nodes/:nodeID", v2Only)
	routes.DELETE("/roadmaps/:id/nodes/:nodeID", v2Only)
}

func (dispatcher taskDomainRouteDispatcher) handler(legacyAllowed, v2Allowed bool) gin.HandlerFunc {
	var v2 http.Handler
	if v2Allowed {
		v2 = dispatcher.v2
	}
	return dispatcher.handlerWithV2(legacyAllowed, v2)
}

func (dispatcher taskDomainRouteDispatcher) handlerWithV2(legacyAllowed bool, v2 http.Handler) gin.HandlerFunc {
	return func(c *gin.Context) {
		workspaceID, err := auth.WorkspaceIDFromContext(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusUnauthorized, taskDomainRoutingError("unauthorized", "authentication is required", false))
			return
		}
		model, err := dispatcher.selectModel(c.Request.Context(), workspaceID)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, taskDomainRoutingError("task_domain_routing_unavailable", "task-domain routing state is unavailable", true))
			return
		}
		switch model {
		case taskapp.ModelLegacy:
			if !legacyAllowed {
				c.JSON(http.StatusConflict, taskDomainRoutingError("task_domain_v2_not_active", "this workspace has not activated the v2 task domain", false))
				return
			}
			dispatcher.legacy.ServeHTTP(c.Writer, c.Request)
		case taskapp.ModelV2:
			if v2 == nil {
				c.JSON(http.StatusGone, taskDomainRoutingError("legacy_task_domain_endpoint_retired", "this legacy task-domain endpoint is not available for a v2 workspace", false))
				return
			}
			v2.ServeHTTP(c.Writer, c.Request)
		default:
			c.JSON(http.StatusServiceUnavailable, taskDomainRoutingError("task_domain_routing_unavailable", "task-domain routing state is unavailable", true))
		}
		c.Abort()
	}
}

// taskContractHandler keeps the ambiguous historical /api/tasks path usable
// during Web cutover without weakening the native v2 contract. Legacy list
// queries are structurally distinct (pagination/status fields); v2 mutations
// carry either a schedule or optimistic revisions. An explicit header is
// available for clients that want a deterministic compatibility read.
func (dispatcher taskDomainRouteDispatcher) taskContractHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		workspaceID, err := auth.WorkspaceIDFromContext(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusUnauthorized, taskDomainRoutingError("unauthorized", "authentication is required", false))
			return
		}
		model, err := dispatcher.selectModel(c.Request.Context(), workspaceID)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, taskDomainRoutingError("task_domain_routing_unavailable", "task-domain routing state is unavailable", true))
			return
		}
		if model == taskapp.ModelLegacy {
			dispatcher.legacy.ServeHTTP(c.Writer, c.Request)
			c.Abort()
			return
		}
		if model != taskapp.ModelV2 {
			c.JSON(http.StatusServiceUnavailable, taskDomainRoutingError("task_domain_routing_unavailable", "task-domain routing state is unavailable", true))
			return
		}
		target := dispatcher.v2
		if legacyTaskContractRequest(c.Request) {
			target = dispatcher.compat
		}
		if target == nil {
			c.JSON(http.StatusServiceUnavailable, taskDomainRoutingError("task_domain_unavailable", "the v2 task-domain runtime is unavailable", true))
			return
		}
		target.ServeHTTP(c.Writer, c.Request)
		c.Abort()
	}
}

func legacyTaskContractRequest(request *http.Request) bool {
	if request.Header.Get("X-FlowSpace-Legacy-Task-Contract") == "1" {
		return true
	}
	switch request.Method {
	case http.MethodGet:
		query := request.URL.Query()
		for _, name := range []string{"page", "page_size", "project", "status", "scope", "horizon", "planned_date", "planned_from", "planned_to", "execution_type"} {
			if _, exists := query[name]; exists {
				return true
			}
		}
		return false
	case http.MethodPost:
		return !taskRequestBodyHasField(request, "schedule")
	case http.MethodPatch:
		return !taskRequestBodyHasField(request, "expected_task_revision")
	default:
		return true
	}
}

func taskRequestBodyHasField(request *http.Request, field string) bool {
	if request.Body == nil {
		return false
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return false
	}
	request.Body = io.NopCloser(bytes.NewReader(body))
	var object map[string]json.RawMessage
	if json.Unmarshal(body, &object) != nil {
		return false
	}
	_, exists := object[field]
	return exists
}

func (dispatcher taskDomainRouteDispatcher) selectModel(ctx context.Context, workspaceID string) (taskapp.ModelVersion, error) {
	if dispatcher.selector == nil {
		return "", errors.New("task-domain model selector is required")
	}
	model, err := dispatcher.selector.SelectTaskDomainModel(ctx, workspaceID)
	if err != nil {
		return "", err
	}
	if model != taskapp.ModelLegacy && model != taskapp.ModelV2 {
		return "", errors.New("task-domain model selector returned an unknown model")
	}
	return model, nil
}

type taskDomainCapabilitiesResponse struct {
	ModelVersion string                      `json:"model_version"`
	Available    bool                        `json:"available"`
	Error        *handler.TaskDomainAPIError `json:"error,omitempty"`
}

func taskDomainLegacyCapabilities(c *gin.Context) {
	c.JSON(http.StatusOK, taskDomainCapabilitiesResponse{ModelVersion: string(taskapp.ModelLegacy), Available: true})
}

func (dispatcher taskDomainRouteDispatcher) capabilities(c *gin.Context) {
	workspaceID, err := auth.WorkspaceIDFromContext(c.Request.Context())
	if err != nil {
		apiError := taskDomainAPIError("unauthorized", "authentication is required", false)
		c.JSON(http.StatusUnauthorized, taskDomainCapabilitiesResponse{ModelVersion: "unknown", Available: false, Error: &apiError})
		return
	}
	model, err := dispatcher.selectModel(c.Request.Context(), workspaceID)
	if err != nil {
		apiError := taskDomainAPIError("task_domain_routing_unavailable", "task-domain routing state is unavailable", true)
		c.JSON(http.StatusServiceUnavailable, taskDomainCapabilitiesResponse{ModelVersion: "unknown", Available: false, Error: &apiError})
		return
	}
	if model == taskapp.ModelV2 {
		if dispatcher.runtime == nil {
			apiError := taskDomainAPIError("task_domain_unavailable", "task-domain runtime is unavailable", true)
			c.JSON(http.StatusServiceUnavailable, taskDomainCapabilitiesResponse{ModelVersion: string(model), Available: false, Error: &apiError})
			return
		}
		if _, err := dispatcher.runtime.Resolve(c.Request.Context(), workspaceID); err != nil {
			apiError := taskDomainAPIError("task_domain_unavailable", "task-domain runtime is unavailable", true)
			c.JSON(http.StatusServiceUnavailable, taskDomainCapabilitiesResponse{ModelVersion: string(model), Available: false, Error: &apiError})
			return
		}
	}
	c.JSON(http.StatusOK, taskDomainCapabilitiesResponse{ModelVersion: string(model), Available: true})
}

func taskDomainRoutingError(code, message string, retryable bool) handler.TaskDomainErrorResponse {
	return handler.TaskDomainErrorResponse{Error: taskDomainAPIError(code, message, retryable)}
}

func taskDomainAPIError(code, message string, retryable bool) handler.TaskDomainAPIError {
	return handler.TaskDomainAPIError{Code: code, Message: message, Retryable: retryable}
}

// newTaskDomainV2Application is the only production-shaped composition point
// between HTTP and the request-scoped tenant runtime. It deliberately accepts
// no storage.Store: every application operation must resolve the authenticated
// workspace's current runtime and durable epoch.
func newTaskDomainV2Application(runtime taskapp.RuntimeResolver) handler.TaskDomainV2Application {
	return taskapp.NewFacade(
		taskDomainUnavailableResolver{delegate: runtime},
		taskDomainClock{},
		taskDomainIDGenerator{},
		taskDomainIDGenerator{},
	)
}

// taskDomainUnavailableResolver gives all runtime-resolution failures a
// stable fail-closed HTTP classification. It keeps the original error in the
// chain for diagnostics and never substitutes the legacy Store.
type taskDomainUnavailableResolver struct{ delegate taskapp.RuntimeResolver }

func (resolver taskDomainUnavailableResolver) Resolve(ctx context.Context, workspaceID string) (taskapp.RuntimeSnapshot, error) {
	if resolver.delegate == nil {
		return taskapp.RuntimeSnapshot{}, taskapp.ErrInvalidRuntime
	}
	snapshot, err := resolver.delegate.Resolve(ctx, workspaceID)
	if err != nil {
		return taskapp.RuntimeSnapshot{}, errors.Join(taskapp.ErrInvalidRuntime, err)
	}
	return snapshot, nil
}

type taskDomainClock struct{}

func (taskDomainClock) Now() time.Time { return time.Now().UTC() }

type taskDomainIDGenerator struct{}

func (taskDomainIDGenerator) NewID(context.Context) (string, error) {
	return uuid.NewString(), nil
}

func (taskDomainIDGenerator) NewCommandID(context.Context) (string, error) {
	return uuid.NewString(), nil
}

var _ taskapp.RuntimeResolver = taskDomainUnavailableResolver{}
var _ taskapp.Clock = taskDomainClock{}
var _ taskapp.IDGenerator = taskDomainIDGenerator{}
var _ taskapp.CommandIDGenerator = taskDomainIDGenerator{}
