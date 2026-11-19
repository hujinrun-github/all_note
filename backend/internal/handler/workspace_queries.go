package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/taskdomain"
	"github.com/hujinrun/flowspace/internal/workspacequery"
)

type WorkspaceQueryApplication interface {
	Overview(context.Context, workspacequery.Params) (workspacequery.OverviewResult, error)
	Facets(context.Context, workspacequery.Params, string) (workspacequery.FacetsResult, error)
	ProjectSummaries(context.Context, workspacequery.ProjectParams) (workspacequery.ProjectsResult, error)
	ProjectOverview(context.Context, workspacequery.ProjectParams) (workspacequery.ProjectResult, error)
}

type WorkspaceQueryMetadataDTO struct {
	WorkspaceID      string    `json:"workspace_id"`
	TaskModelVersion int       `json:"task_model_version"`
	RuntimeEpoch     int64     `json:"runtime_epoch"`
	Date             string    `json:"date"`
	Timezone         string    `json:"timezone"`
	GeneratedAt      time.Time `json:"generated_at"`
	ReadConsistency  string    `json:"read_consistency"`
}

type WorkspaceFacetsDTO struct {
	Today          int `json:"today"`
	Week           int `json:"week"`
	Month          int `json:"month"`
	Overdue        int `json:"overdue"`
	Unscheduled    int `json:"unscheduled"`
	Inbox          int `json:"inbox"`
	CompletedToday int `json:"completed_today"`
	CompletedWeek  int `json:"completed_week"`
	CompletedTotal int `json:"completed_total"`
	Draft          int `json:"draft"`
	Recurring      int `json:"recurring"`
}

type WorkspaceOccurrenceDTO struct {
	OccurrenceV2DTO
	ProjectName string `json:"project_name"`
}

type WorkspaceProjectSummaryDTO struct {
	CompletedTaskCount       int                     `json:"completed_task_count"`
	TotalActionableTaskCount int                     `json:"total_actionable_task_count"`
	OpenOccurrenceCount      int                     `json:"open_occurrence_count"`
	BlockedOccurrenceCount   int                     `json:"blocked_occurrence_count"`
	HasOpenEndedRecurrence   bool                    `json:"has_open_ended_recurrence"`
	NextOccurrence           *WorkspaceOccurrenceDTO `json:"next_occurrence"`
	CompletionPercent        *int                    `json:"completion_percent"`
}

type WorkspaceProjectDTO struct {
	ProjectV2DTO
	Summary WorkspaceProjectSummaryDTO `json:"summary"`
}

type WorkspaceNotesDTO struct {
	State     string                       `json:"state"`
	Authority string                       `json:"authority"`
	AsOf      time.Time                    `json:"as_of"`
	Items     []workspacequery.NotePreview `json:"items"`
	Error     *WorkspaceSectionErrorDTO    `json:"error,omitempty"`
}

type WorkspaceSectionErrorDTO struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type WorkspaceOverviewResponse struct {
	WorkspaceQueryMetadataDTO
	Facets   WorkspaceFacetsDTO       `json:"facets"`
	Today    []WorkspaceOccurrenceDTO `json:"today"`
	Projects []WorkspaceProjectDTO    `json:"projects"`
	Notes    WorkspaceNotesDTO        `json:"notes"`
	Partial  bool                     `json:"partial"`
}

func GetWorkspaceOverview(application WorkspaceQueryApplication) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !workspaceQueryIdentity(c) {
			return
		}
		params, err := workspaceQueryParams(c, "today_limit", "project_limit", "notes_limit")
		if err != nil {
			writeWorkspaceQueryError(c, err)
			return
		}
		if application == nil {
			writeWorkspaceQueryError(c, workspacequery.ErrRuntimeUnavailable)
			return
		}
		result, err := application.Overview(c.Request.Context(), params)
		if err != nil {
			writeWorkspaceQueryError(c, err)
			return
		}
		today := make([]WorkspaceOccurrenceDTO, 0, len(result.Planning.Today))
		for _, occurrence := range result.Planning.Today {
			today = append(today, WorkspaceOccurrenceDTO{OccurrenceV2DTO: occurrenceV2DTO(occurrence), ProjectName: result.Planning.ProjectNames[occurrence.ProjectID]})
		}
		notes := WorkspaceNotesDTO{State: result.Notes.State, Authority: result.Notes.Authority, AsOf: result.Notes.AsOf, Items: result.Notes.Items}
		if result.Notes.ErrorCode != "" {
			notes.Error = &WorkspaceSectionErrorDTO{Code: result.Notes.ErrorCode, Message: "recent notes are temporarily unavailable"}
		}
		success(c, WorkspaceOverviewResponse{
			WorkspaceQueryMetadataDTO: workspaceQueryMetadataDTO(result.Metadata),
			Facets:                    workspaceFacetsDTO(result.Planning.Facets), Today: today,
			Projects: workspaceProjectsDTO(result.Planning.Projects), Notes: notes, Partial: result.Partial,
		})
	}
}

func GetWorkspaceFacets(application WorkspaceQueryApplication) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !workspaceQueryIdentity(c) {
			return
		}
		params, err := workspaceQueryParams(c, "project_id")
		if err != nil {
			writeWorkspaceQueryError(c, err)
			return
		}
		if application == nil {
			writeWorkspaceQueryError(c, workspacequery.ErrRuntimeUnavailable)
			return
		}
		result, err := application.Facets(c.Request.Context(), params, c.Query("project_id"))
		if err != nil {
			writeWorkspaceQueryError(c, err)
			return
		}
		success(c, struct {
			WorkspaceQueryMetadataDTO
			Facets WorkspaceFacetsDTO `json:"facets"`
		}{workspaceQueryMetadataDTO(result.Metadata), workspaceFacetsDTO(result.Facets)})
	}
}

func GetWorkspaceProjectSummaries(application WorkspaceQueryApplication) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !workspaceQueryIdentity(c) {
			return
		}
		params, err := workspaceProjectParams(c, false)
		if err != nil {
			writeWorkspaceQueryError(c, err)
			return
		}
		if application == nil {
			writeWorkspaceQueryError(c, workspacequery.ErrRuntimeUnavailable)
			return
		}
		result, err := application.ProjectSummaries(c.Request.Context(), params)
		if err != nil {
			writeWorkspaceQueryError(c, err)
			return
		}
		success(c, struct {
			WorkspaceQueryMetadataDTO
			Projects []WorkspaceProjectDTO `json:"projects"`
		}{workspaceQueryMetadataDTO(result.Metadata), workspaceProjectsDTO(result.Projects)})
	}
}

func GetWorkspaceProjectOverview(application WorkspaceQueryApplication) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !workspaceQueryIdentity(c) {
			return
		}
		params, err := workspaceProjectParams(c, true)
		if err != nil {
			writeWorkspaceQueryError(c, err)
			return
		}
		if application == nil {
			writeWorkspaceQueryError(c, workspacequery.ErrRuntimeUnavailable)
			return
		}
		result, err := application.ProjectOverview(c.Request.Context(), params)
		if err != nil {
			writeWorkspaceQueryError(c, err)
			return
		}
		success(c, struct {
			WorkspaceQueryMetadataDTO
			Project WorkspaceProjectDTO `json:"project"`
		}{workspaceQueryMetadataDTO(result.Metadata), workspaceProjectDTO(result.Project)})
	}
}

func workspaceQueryIdentity(c *gin.Context) bool {
	_, err := auth.ResolveWorkspaceContext(c.Request.Context())
	if err != nil {
		writeWorkspaceQueryError(c, err)
		return false
	}
	c.Header("Cache-Control", "no-store")
	return true
}

func workspaceQueryParams(c *gin.Context, extras ...string) (workspacequery.Params, error) {
	allowed := map[string]bool{"date": true, "timezone": true}
	for _, field := range extras {
		allowed[field] = true
	}
	for field, values := range c.Request.URL.Query() {
		if !allowed[field] || len(values) != 1 {
			return workspacequery.Params{}, workspacequery.ErrInvalidRequest
		}
	}
	params := workspacequery.Params{Date: c.Query("date"), Timezone: c.Query("timezone")}
	if _, err := workspacequery.NewWindow(params.Date, params.Timezone); err != nil {
		return workspacequery.Params{}, err
	}
	for _, limit := range []struct {
		name  string
		value *int
		max   int
	}{
		{"today_limit", &params.TodayLimit, workspacequery.MaxTodayLimit},
		{"project_limit", &params.ProjectLimit, workspacequery.MaxProjectLimit},
		{"notes_limit", &params.NotesLimit, workspacequery.MaxNotesLimit},
	} {
		if values, exists := c.Request.URL.Query()[limit.name]; exists {
			value, err := strconv.Atoi(values[0])
			if err != nil || value < 1 || value > limit.max {
				return workspacequery.Params{}, workspacequery.ErrInvalidRequest
			}
			*limit.value = value
		}
	}
	return params, nil
}

func workspaceProjectParams(c *gin.Context, single bool) (workspacequery.ProjectParams, error) {
	extras := []string{"include", "kind", "horizon", "status"}
	if single {
		extras = nil
	}
	params, err := workspaceQueryParams(c, extras...)
	if err != nil {
		return workspacequery.ProjectParams{}, err
	}
	request := workspacequery.ProjectParams{Params: params, ProjectID: c.Param("projectID")}
	if single {
		if request.ProjectID == "" || request.ProjectID != strings.TrimSpace(request.ProjectID) {
			return workspacequery.ProjectParams{}, workspacequery.ErrInvalidRequest
		}
		return request, nil
	}
	if c.Query("include") != "summary" {
		return workspacequery.ProjectParams{}, workspacequery.ErrInvalidRequest
	}
	if value := c.Query("kind"); value != "" {
		kind := taskdomain.ProjectKind(value)
		request.Filter.Kind = &kind
	}
	if value := c.Query("horizon"); value != "" {
		horizon := taskdomain.ProjectHorizon(value)
		request.Filter.Horizon = &horizon
	}
	if value := c.Query("status"); value != "" {
		status := taskdomain.ProjectStatus(value)
		request.Filter.Status = &status
	}
	if taskdomain.ValidateProjectListFilter(request.Filter) != nil {
		return workspacequery.ProjectParams{}, workspacequery.ErrInvalidRequest
	}
	return request, nil
}

func workspaceQueryMetadataDTO(metadata workspacequery.Metadata) WorkspaceQueryMetadataDTO {
	return WorkspaceQueryMetadataDTO{WorkspaceID: metadata.WorkspaceID, TaskModelVersion: 2, RuntimeEpoch: metadata.RuntimeEpoch,
		Date: metadata.Date, Timezone: metadata.Timezone, GeneratedAt: metadata.GeneratedAt, ReadConsistency: "planning_snapshot"}
}

func workspaceFacetsDTO(f workspacequery.Facets) WorkspaceFacetsDTO {
	return WorkspaceFacetsDTO{Today: f.Today, Week: f.Week, Month: f.Month, Overdue: f.Overdue, Unscheduled: f.Unscheduled, Inbox: f.Inbox,
		CompletedToday: f.CompletedToday, CompletedWeek: f.CompletedWeek, CompletedTotal: f.CompletedTotal, Draft: f.Draft, Recurring: f.Recurring}
}

func workspaceProjectsDTO(projects []workspacequery.ProjectSummary) []WorkspaceProjectDTO {
	items := make([]WorkspaceProjectDTO, 0, len(projects))
	for _, project := range projects {
		items = append(items, workspaceProjectDTO(project))
	}
	return items
}

func workspaceProjectDTO(project workspacequery.ProjectSummary) WorkspaceProjectDTO {
	summary := WorkspaceProjectSummaryDTO{
		CompletedTaskCount: project.CompletedTaskCount, TotalActionableTaskCount: project.TotalActionableTaskCount,
		OpenOccurrenceCount: project.OpenOccurrenceCount, BlockedOccurrenceCount: project.BlockedOccurrenceCount,
		HasOpenEndedRecurrence: project.HasOpenEndedRecurrence,
	}
	if project.TotalActionableTaskCount > 0 && !project.HasOpenEndedRecurrence {
		percent := int(float64(project.CompletedTaskCount)/float64(project.TotalActionableTaskCount)*100 + 0.5)
		summary.CompletionPercent = &percent
	}
	if project.NextOccurrence != nil {
		summary.NextOccurrence = &WorkspaceOccurrenceDTO{OccurrenceV2DTO: occurrenceV2DTO(*project.NextOccurrence), ProjectName: project.Project.Project.Name}
	}
	return WorkspaceProjectDTO{ProjectV2DTO: projectSnapshotV2DTO(project.Project), Summary: summary}
}

func writeWorkspaceQueryError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, auth.ErrMissingIdentity), errors.Is(err, auth.ErrMissingWorkspace), errors.Is(err, auth.ErrWorkspaceScopeMismatch):
		errorResponse(c, http.StatusUnauthorized, "unauthorized", "authentication is required")
	case errors.Is(err, workspacequery.ErrInvalidRequest):
		errorResponse(c, http.StatusBadRequest, "invalid_request", "valid date, timezone and query parameters are required")
	case errors.Is(err, taskdomain.ErrProjectNotFound):
		errorResponse(c, http.StatusNotFound, "project_not_found", "project not found")
	default:
		errorResponse(c, http.StatusServiceUnavailable, "workspace_unavailable", "workspace queries are temporarily unavailable")
	}
}
