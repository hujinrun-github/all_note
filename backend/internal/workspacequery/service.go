package workspacequery

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/taskdomain"
)

const (
	DefaultTodayLimit   = 8
	DefaultProjectLimit = 3
	DefaultNotesLimit   = 3
	MaxTodayLimit       = 50
	MaxProjectLimit     = 20
	MaxNotesLimit       = 10
)

// RuntimeResolver resolves a durably verified, read-only workspace capability.
// Its implementation shares the task/mobile runtime boundary and never chooses
// a default database when a workspace cannot be resolved.
type RuntimeResolver interface {
	ResolveWorkspaceQuery(context.Context, string) (Runtime, error)
}

// NotesSource is deliberately independent from the planning runtime: content
// authority is migrated separately, after backfill and reconciliation.
type NotesSource interface {
	Authority() string
	Recent(context.Context, string, int) ([]NotePreview, error)
}

type NotePreview struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	UpdatedAt int64  `json:"updated_at"`
}

type Params struct {
	Date, Timezone                       string
	TodayLimit, ProjectLimit, NotesLimit int
}

type ProjectParams struct {
	Params
	ProjectID string
	Filter    taskdomain.ProjectListFilter
}

type Metadata struct {
	WorkspaceID  string
	RuntimeEpoch int64
	Date         string
	Timezone     string
	GeneratedAt  time.Time
}

type NotesSection struct {
	State     string
	Authority string
	AsOf      time.Time
	Items     []NotePreview
	ErrorCode string
}

type OverviewResult struct {
	Metadata
	Planning PlanningOverview
	Notes    NotesSection
	Partial  bool
}

type FacetsResult struct {
	Metadata
	Facets Facets
}

type ProjectsResult struct {
	Metadata
	Projects []ProjectSummary
}

type ProjectResult struct {
	Metadata
	Project ProjectSummary
}

type Service struct {
	runtimes RuntimeResolver
	notes    NotesSource
	now      func() time.Time
}

func NewService(runtimes RuntimeResolver, notes NotesSource, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{runtimes: runtimes, notes: notes, now: now}
}

// NewWindow uses local calendar arithmetic rather than fixed 24-hour periods,
// including for DST days and Monday-based weeks.
func NewWindow(date, timezone string) (Window, error) {
	if date != strings.TrimSpace(date) || timezone != strings.TrimSpace(timezone) || len(date) != 10 || timezone == "" || timezone == "Local" {
		return Window{}, ErrInvalidRequest
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return Window{}, ErrInvalidRequest
	}
	calendar, err := time.Parse("2006-01-02", date)
	if err != nil || calendar.Year() < 1 {
		return Window{}, ErrInvalidRequest
	}
	weekCalendar := calendar.AddDate(0, 0, -(int(calendar.Weekday())+6)%7)
	monthCalendar := time.Date(calendar.Year(), calendar.Month(), 1, 0, 0, 0, 0, time.UTC)
	boundaries := make([]time.Time, 0, 6)
	for _, day := range []time.Time{calendar, calendar.AddDate(0, 0, 1), weekCalendar, weekCalendar.AddDate(0, 0, 7), monthCalendar, monthCalendar.AddDate(0, 1, 0)} {
		boundary, err := localCalendarBoundary(day, location)
		if err != nil {
			return Window{}, err
		}
		boundaries = append(boundaries, boundary)
	}
	// A clock advance at midnight may start a valid date at 01:00. An
	// entirely skipped date, unlike such a midnight gap, cannot be queried.
	if boundaries[0].Format("2006-01-02") != date {
		return Window{}, ErrInvalidRequest
	}
	return Window{
		Date: date, Timezone: timezone, Start: boundaries[0], End: boundaries[1],
		WeekStart: boundaries[2], WeekEnd: boundaries[3],
		MonthStart: boundaries[4], MonthEnd: boundaries[5],
	}, nil
}

// localCalendarBoundary resolves each boundary independently. Adding a local
// calendar day to a gap-normalized 01:00 start would incorrectly retain 01:00
// at the end, instead of using the next day's midnight.
func localCalendarBoundary(calendar time.Time, location *time.Location) (time.Time, error) {
	instant := calendar
	seen := make(map[time.Time]struct{}, 4)
	for attempt := 0; attempt < 4; attempt++ {
		local := instant.In(location)
		wall := time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), local.Minute(), local.Second(), 0, time.UTC)
		next := instant.Add(calendar.Sub(wall))
		if next.Equal(instant) {
			// A backward transition can repeat midnight. The prior offset
			// identifies an earlier matching midnight when one exists.
			_, priorOffset := instant.AddDate(0, 0, -2).In(location).Zone()
			earlier := calendar.Add(-time.Duration(priorOffset) * time.Second)
			localEarlier := earlier.In(location)
			wallEarlier := time.Date(localEarlier.Year(), localEarlier.Month(), localEarlier.Day(), localEarlier.Hour(), localEarlier.Minute(), localEarlier.Second(), 0, time.UTC)
			if earlier.Before(instant) && wallEarlier.Equal(calendar) {
				instant = earlier
			}
			return instant.In(location), nil
		}
		if _, exists := seen[next]; exists {
			// A missing midnight oscillates across the offset transition;
			// take the later instant, the first valid time after that gap.
			if next.After(instant) {
				instant = next
			}
			return instant.In(location), nil
		}
		seen[instant] = struct{}{}
		instant = next
	}
	return time.Time{}, ErrInvalidRequest
}

func (s *Service) Overview(ctx context.Context, params Params) (OverviewResult, error) {
	params, window, err := normalizeParams(params)
	if err != nil {
		return OverviewResult{}, err
	}
	runtime, err := s.resolve(ctx)
	if err != nil {
		return OverviewResult{}, err
	}
	planning, err := runtime.Reader.Overview(ctx, Request{
		Window: window, ExpectedEpoch: runtime.Epoch,
		TodayLimit: params.TodayLimit, ProjectLimit: params.ProjectLimit,
	})
	if err != nil {
		return OverviewResult{}, queryError(err)
	}
	if err := validateOverview(runtime.WorkspaceID, planning); err != nil {
		return OverviewResult{}, err
	}
	if len(planning.Today) > params.TodayLimit || len(planning.Projects) > params.ProjectLimit {
		return OverviewResult{}, ErrRuntimeUnavailable
	}
	notes := NotesSection{State: "unavailable", Authority: "unconfigured", ErrorCode: "notes_unavailable"}
	if s.notes != nil {
		notes.Authority = s.notes.Authority()
		notesCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		items, notesErr := s.notes.Recent(notesCtx, runtime.WorkspaceID, params.NotesLimit)
		cancel()
		if notesErr == nil && len(items) <= params.NotesLimit {
			notes.State, notes.ErrorCode = "available", ""
			notes.Items = items
			if notes.Items == nil {
				notes.Items = []NotePreview{}
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return OverviewResult{}, err
	}
	notes.AsOf = s.now().UTC()
	return OverviewResult{
		Metadata: s.metadata(runtime, window), Planning: planning,
		Notes: notes, Partial: notes.State != "available",
	}, nil
}

func (s *Service) Facets(ctx context.Context, params Params, projectID string) (FacetsResult, error) {
	_, window, err := normalizeParams(params)
	if err != nil || projectID != strings.TrimSpace(projectID) {
		return FacetsResult{}, ErrInvalidRequest
	}
	runtime, err := s.resolve(ctx)
	if err != nil {
		return FacetsResult{}, err
	}
	planning, err := runtime.Reader.Overview(ctx, Request{Window: window, ExpectedEpoch: runtime.Epoch, ProjectID: projectID})
	if err != nil {
		return FacetsResult{}, queryError(err)
	}
	return FacetsResult{Metadata: s.metadata(runtime, window), Facets: planning.Facets}, nil
}

func (s *Service) ProjectSummaries(ctx context.Context, params ProjectParams) (ProjectsResult, error) {
	_, window, err := normalizeParams(params.Params)
	if err != nil || taskdomain.ValidateProjectListFilter(params.Filter) != nil {
		return ProjectsResult{}, ErrInvalidRequest
	}
	runtime, err := s.resolve(ctx)
	if err != nil {
		return ProjectsResult{}, err
	}
	projects, err := runtime.Reader.ProjectSummaries(ctx, ProjectRequest{Window: window, ExpectedEpoch: runtime.Epoch, Filter: params.Filter})
	if err != nil {
		return ProjectsResult{}, queryError(err)
	}
	for _, project := range projects {
		if err := validateSummary(runtime.WorkspaceID, project); err != nil {
			return ProjectsResult{}, err
		}
	}
	return ProjectsResult{Metadata: s.metadata(runtime, window), Projects: projects}, nil
}

func (s *Service) ProjectOverview(ctx context.Context, params ProjectParams) (ProjectResult, error) {
	_, window, err := normalizeParams(params.Params)
	if err != nil || strings.TrimSpace(params.ProjectID) == "" || params.ProjectID != strings.TrimSpace(params.ProjectID) {
		return ProjectResult{}, ErrInvalidRequest
	}
	runtime, err := s.resolve(ctx)
	if err != nil {
		return ProjectResult{}, err
	}
	project, err := runtime.Reader.ProjectOverview(ctx, ProjectRequest{Window: window, ExpectedEpoch: runtime.Epoch, ProjectID: params.ProjectID})
	if err != nil {
		return ProjectResult{}, queryError(err)
	}
	if err := validateSummary(runtime.WorkspaceID, project); err != nil {
		return ProjectResult{}, err
	}
	if project.Project.Project.ID != params.ProjectID {
		return ProjectResult{}, ErrRuntimeUnavailable
	}
	return ProjectResult{Metadata: s.metadata(runtime, window), Project: project}, nil
}

func normalizeParams(params Params) (Params, Window, error) {
	window, err := NewWindow(params.Date, params.Timezone)
	if err != nil {
		return Params{}, Window{}, err
	}
	for _, limit := range []struct {
		value         *int
		fallback, max int
	}{
		{&params.TodayLimit, DefaultTodayLimit, MaxTodayLimit},
		{&params.ProjectLimit, DefaultProjectLimit, MaxProjectLimit},
		{&params.NotesLimit, DefaultNotesLimit, MaxNotesLimit},
	} {
		if *limit.value == 0 {
			*limit.value = limit.fallback
		}
		if *limit.value < 1 || *limit.value > limit.max {
			return Params{}, Window{}, ErrInvalidRequest
		}
	}
	return params, window, nil
}

func (s *Service) resolve(ctx context.Context) (Runtime, error) {
	scope, err := auth.ResolveWorkspaceContext(ctx)
	if err != nil {
		return Runtime{}, err
	}
	if s == nil || s.runtimes == nil {
		return Runtime{}, ErrRuntimeUnavailable
	}
	runtime, err := s.runtimes.ResolveWorkspaceQuery(ctx, scope.WorkspaceID)
	if err != nil {
		return Runtime{}, fmt.Errorf("%w: %w", ErrRuntimeUnavailable, err)
	}
	if runtime.WorkspaceID != scope.WorkspaceID || runtime.Epoch < 1 || runtime.Reader == nil {
		return Runtime{}, ErrRuntimeUnavailable
	}
	return runtime, nil
}

func (s *Service) metadata(runtime Runtime, window Window) Metadata {
	return Metadata{WorkspaceID: runtime.WorkspaceID, RuntimeEpoch: runtime.Epoch, Date: window.Date, Timezone: window.Timezone, GeneratedAt: s.now().UTC()}
}

func queryError(err error) error {
	if errors.Is(err, ErrInvalidRequest) || errors.Is(err, taskdomain.ErrProjectNotFound) {
		return err
	}
	return fmt.Errorf("%w: %w", ErrRuntimeUnavailable, err)
}

func validateOverview(workspaceID string, overview PlanningOverview) error {
	for _, occurrence := range overview.Today {
		if occurrence.WorkspaceID != workspaceID {
			return ErrRuntimeUnavailable
		}
	}
	for _, project := range overview.Projects {
		if err := validateSummary(workspaceID, project); err != nil {
			return err
		}
	}
	return nil
}

func validateSummary(workspaceID string, summary ProjectSummary) error {
	if summary.Project.Project.WorkspaceID != workspaceID || summary.Project.Project.ID == "" {
		return ErrRuntimeUnavailable
	}
	if next := summary.NextOccurrence; next != nil && (next.WorkspaceID != workspaceID || next.ProjectID != summary.Project.Project.ID) {
		return ErrRuntimeUnavailable
	}
	return nil
}
