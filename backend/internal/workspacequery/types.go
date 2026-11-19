package workspacequery

import (
	"context"
	"errors"
	"time"

	"github.com/hujinrun/flowspace/internal/taskdomain"
)

var (
	ErrInvalidRequest     = errors.New("invalid workspace query request")
	ErrRuntimeUnavailable = errors.New("workspace query runtime unavailable")
)

// Runtime carries only the read capability of a durably verified v2 workspace.
type Runtime struct {
	WorkspaceID string
	Epoch       int64
	Reader      Reader
}

// Window contains local calendar boundaries and their corresponding instants.
// All intervals are half-open. Week starts on Monday.
type Window struct {
	Date                 string
	Timezone             string
	Start, End           time.Time
	WeekStart, WeekEnd   time.Time
	MonthStart, MonthEnd time.Time
}

type Request struct {
	Window                   Window
	ExpectedEpoch            int64
	TodayLimit, ProjectLimit int
	ProjectID                string
}

type ProjectRequest struct {
	Window        Window
	ExpectedEpoch int64
	Filter        taskdomain.ProjectListFilter
	ProjectID     string
	// Limit zero returns the full filtered catalog for the existing list API.
	Limit int
}

// Facets counts executable instances independently from task-definition progress.
type Facets struct {
	Today, Week, Month                            int
	Overdue, Unscheduled, Inbox                   int
	CompletedToday, CompletedWeek, CompletedTotal int
	Draft, Recurring                              int
}

type ProjectSummary struct {
	Project                                      taskdomain.ProjectSnapshot
	CompletedTaskCount, TotalActionableTaskCount int
	OpenOccurrenceCount, BlockedOccurrenceCount  int
	HasOpenEndedRecurrence                       bool
	NextOccurrence                               *taskdomain.QueryOccurrenceSnapshot
}

type PlanningOverview struct {
	Facets       Facets
	Today        []taskdomain.QueryOccurrenceSnapshot
	Projects     []ProjectSummary
	ProjectNames map[string]string
}

type Reader interface {
	Overview(context.Context, Request) (PlanningOverview, error)
	ProjectSummaries(context.Context, ProjectRequest) ([]ProjectSummary, error)
	ProjectOverview(context.Context, ProjectRequest) (ProjectSummary, error)
}
