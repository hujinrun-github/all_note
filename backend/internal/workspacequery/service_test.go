package workspacequery

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/taskdomain"
)

func TestWindowUsesCalendarBoundariesAcrossDST(t *testing.T) {
	for _, tc := range []struct {
		date  string
		hours int
	}{
		{"2026-03-08", 23}, {"2026-11-01", 25}, {"2026-11-19", 24},
	} {
		window, err := NewWindow(tc.date, "America/New_York")
		if err != nil {
			t.Fatal(err)
		}
		if hours := int(window.End.Sub(window.Start) / time.Hour); hours != tc.hours {
			t.Fatalf("%s hours=%d want%d", tc.date, hours, tc.hours)
		}
		if window.WeekStart.Weekday() != time.Monday || window.WeekEnd.Weekday() != time.Monday || window.MonthStart.Day() != 1 || window.MonthEnd.Day() != 1 {
			t.Fatalf("invalid local boundaries: %#v", window)
		}
		if window.End.In(window.Start.Location()).Hour() != 0 {
			t.Fatalf("day end isn't local midnight: %v", window.End)
		}
	}
	for _, tc := range []struct{ date, timezone string }{
		{"2026-02-30", "UTC"}, {"2026-11-19", "Local"}, {"2026-11-19", ""}, {"2026-11-19", "+08:00"}, {"2026-11-19 ", "UTC"}, {"0000-01-01", "UTC"},
	} {
		if _, err := NewWindow(tc.date, tc.timezone); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("invalid date/zone %v accepted: %v", tc, err)
		}
	}
}

func TestWindowResolvesMidnightGapsWithoutShiftingOtherBoundaries(t *testing.T) {
	for _, tc := range []struct {
		date, zone, start, end string
	}{
		{"2026-09-06", "America/Santiago", "2026-09-06T04:00:00Z", "2026-09-07T03:00:00Z"},
		{"2026-03-29", "Asia/Beirut", "2026-03-28T22:00:00Z", "2026-03-29T21:00:00Z"},
		{"2021-10-29", "Asia/Amman", "2021-10-28T21:00:00Z", "2021-10-29T22:00:00Z"},
	} {
		t.Run(tc.zone, func(t *testing.T) {
			window, err := NewWindow(tc.date, tc.zone)
			if err != nil {
				t.Fatal(err)
			}
			if got := window.Start.UTC().Format(time.RFC3339); got != tc.start {
				t.Fatalf("start=%s want%s", got, tc.start)
			}
			if got := window.End.UTC().Format(time.RFC3339); got != tc.end {
				t.Fatalf("end=%s want%s", got, tc.end)
			}
			if window.End.Hour() != 0 || window.WeekStart.Hour() != 0 || window.WeekEnd.Hour() != 0 || window.MonthStart.Hour() != 0 || window.MonthEnd.Hour() != 0 {
				t.Fatalf("a normalized midnight gap shifted an independent calendar boundary: %#v", window)
			}
		})
	}
	if _, err := NewWindow("2011-12-30", "Pacific/Apia"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("entirely skipped local date should be rejected: %v", err)
	}
}

func TestOverviewResolvesOnceAndSeparatesNoteAuthority(t *testing.T) {
	reader := &testReader{overview: PlanningOverview{Today: []taskdomain.QueryOccurrenceSnapshot{{WorkspaceID: "workspace", ProjectID: "project"}}, Projects: []ProjectSummary{{Project: taskdomain.ProjectSnapshot{Project: taskdomain.Project{WorkspaceID: "workspace", ID: "project"}}}}}}
	resolver := &testResolver{runtime: Runtime{WorkspaceID: "workspace", Epoch: 7, Reader: reader}}
	notes := &testNotes{items: []NotePreview{{ID: "note", Title: "from compatibility authority"}}}
	clock := time.Date(2026, 11, 19, 3, 0, 0, 0, time.UTC)
	service := NewService(resolver, notes, func() time.Time { return clock })
	result, err := service.Overview(testContext(), Params{Date: "2026-11-19", Timezone: "Asia/Shanghai"})
	if err != nil {
		t.Fatal(err)
	}
	if resolver.calls != 1 || reader.calls != 1 || reader.request.ExpectedEpoch != 7 || reader.request.TodayLimit != DefaultTodayLimit || reader.request.ProjectLimit != DefaultProjectLimit {
		t.Fatalf("wrong runtime or query boundary: resolver=%d reader=%d req=%#v", resolver.calls, reader.calls, reader.request)
	}
	if notes.calls != 1 || notes.workspaceID != "workspace" || notes.limit != DefaultNotesLimit || !notes.deadline || result.Partial || result.Notes.State != "available" || result.Notes.Authority != "test_compatibility" || len(result.Notes.Items) != 1 || result.GeneratedAt != clock || result.Notes.AsOf != clock {
		t.Fatalf("unexpected overview %#v / source %#v", result, notes)
	}
}

func TestOverviewDistinguishesUnavailableNotesFromEmptyNotes(t *testing.T) {
	resolver := &testResolver{runtime: Runtime{WorkspaceID: "workspace", Epoch: 1, Reader: &testReader{}}}
	for _, tc := range []struct {
		name    string
		notes   NotesSource
		partial bool
		state   string
	}{
		{"empty", &testNotes{}, false, "available"}, {"failed", &testNotes{err: errors.New("database offline")}, true, "unavailable"}, {"unconfigured", nil, true, "unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := NewService(resolver, tc.notes, nil).Overview(testContext(), Params{Date: "2026-11-19", Timezone: "UTC"})
			if err != nil {
				t.Fatal(err)
			}
			if result.Partial != tc.partial || result.Notes.State != tc.state {
				t.Fatalf("result=%#v", result)
			}
			if tc.partial && (result.Notes.Items != nil || result.Notes.ErrorCode != "notes_unavailable") {
				t.Fatalf("unavailable disguised as empty: %#v", result.Notes)
			}
			if !tc.partial && (result.Notes.Items == nil || result.Notes.ErrorCode != "") {
				t.Fatalf("empty notes aren't an available array: %#v", result.Notes)
			}
		})
	}
}

func TestQueriesFailBeforeReadingContentOnInvalidAuthorityOrPlanning(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		ctx                  context.Context
		runtime              Runtime
		sourceErr, errorWant error
	}{
		{"missing identity", context.Background(), Runtime{}, nil, auth.ErrMissingIdentity},
		{"mismatched scope", auth.ContextWithWorkspaceScope(testContext(), "another"), Runtime{}, nil, auth.ErrWorkspaceScopeMismatch},
		{"wrong runtime", testContext(), Runtime{WorkspaceID: "another", Epoch: 1, Reader: &testReader{}}, nil, ErrRuntimeUnavailable},
		{"reader failure", testContext(), Runtime{WorkspaceID: "workspace", Epoch: 1, Reader: &testReader{err: errors.New("failed SQL")}}, nil, ErrRuntimeUnavailable},
		{"wrong row", testContext(), Runtime{WorkspaceID: "workspace", Epoch: 1, Reader: &testReader{overview: PlanningOverview{Today: []taskdomain.QueryOccurrenceSnapshot{{WorkspaceID: "another"}}}}}, nil, ErrRuntimeUnavailable},
		{"unavailable runtime", testContext(), Runtime{}, errors.New("unavailable"), ErrRuntimeUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			notes := &testNotes{}
			resolver := &testResolver{runtime: tc.runtime, err: tc.sourceErr}
			_, err := NewService(resolver, notes, nil).Overview(tc.ctx, Params{Date: "2026-11-19", Timezone: "UTC"})
			if !errors.Is(err, tc.errorWant) {
				t.Fatalf("err=%v want%v", err, tc.errorWant)
			}
			if notes.calls != 0 {
				t.Fatal("content read after planning/auth failed")
			}
		})
	}
}

func TestFacetsOnlyRequestCountersAndProjectLookupIsBounded(t *testing.T) {
	reader := &testReader{project: ProjectSummary{Project: taskdomain.ProjectSnapshot{Project: taskdomain.Project{WorkspaceID: "workspace", ID: "project"}}}}
	resolver := &testResolver{runtime: Runtime{WorkspaceID: "workspace", Epoch: 2, Reader: reader}}
	service := NewService(resolver, nil, nil)
	params := Params{Date: "2026-11-19", Timezone: "UTC"}
	if _, err := service.Facets(testContext(), params, "project"); err != nil {
		t.Fatal(err)
	}
	if reader.request.TodayLimit != 0 || reader.request.ProjectLimit != 0 || reader.request.ProjectID != "project" {
		t.Fatalf("facets loads lists: %#v", reader.request)
	}
	if _, err := service.ProjectOverview(testContext(), ProjectParams{Params: params, ProjectID: "project"}); err != nil {
		t.Fatal(err)
	}
	if reader.projectRequest.ProjectID != "project" || reader.projectRequest.ExpectedEpoch != 2 {
		t.Fatalf("project lookup=%#v", reader.projectRequest)
	}
	reader.project.NextOccurrence = &taskdomain.QueryOccurrenceSnapshot{WorkspaceID: "another", ProjectID: "project"}
	if _, err := service.ProjectOverview(testContext(), ProjectParams{Params: params, ProjectID: "project"}); !errors.Is(err, ErrRuntimeUnavailable) {
		t.Fatalf("crossworkspace next occurrence: %v", err)
	}
}

func TestInvalidParamsDoNotResolveOrQuery(t *testing.T) {
	resolver := &testResolver{}
	service := NewService(resolver, nil, nil)
	for _, params := range []Params{{Date: "invalid", Timezone: "UTC"}, {Date: "2026-11-19", Timezone: "UTC", TodayLimit: 51}, {Date: "2026-11-19", Timezone: "UTC", NotesLimit: -1}, {Date: "2026-11-19", Timezone: "UTC", ProjectLimit: 21}} {
		if _, err := service.Overview(testContext(), params); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("invalid params=%#v err=%v", params, err)
		}
	}
	if resolver.calls != 0 {
		t.Fatal("resolved runtime for invalid params")
	}
}

func testContext() context.Context {
	ctx := auth.ContextWithIdentity(context.Background(), auth.RequestIdentity{UserID: "actor", WorkspaceID: "workspace"})
	return auth.ContextWithWorkspaceScope(ctx, "workspace")
}

type testResolver struct {
	runtime Runtime
	err     error
	calls   int
}

func (r *testResolver) ResolveWorkspaceQuery(context.Context, string) (Runtime, error) {
	r.calls++
	return r.runtime, r.err
}

type testReader struct {
	overview       PlanningOverview
	project        ProjectSummary
	err            error
	calls          int
	request        Request
	projectRequest ProjectRequest
}

func (r *testReader) Overview(_ context.Context, request Request) (PlanningOverview, error) {
	r.calls++
	r.request = request
	return r.overview, r.err
}
func (r *testReader) ProjectSummaries(_ context.Context, request ProjectRequest) ([]ProjectSummary, error) {
	r.projectRequest = request
	return []ProjectSummary{r.project}, r.err
}
func (r *testReader) ProjectOverview(_ context.Context, request ProjectRequest) (ProjectSummary, error) {
	r.projectRequest = request
	return r.project, r.err
}

type testNotes struct {
	items        []NotePreview
	err          error
	calls, limit int
	workspaceID  string
	deadline     bool
}

func (*testNotes) Authority() string { return "test_compatibility" }
func (n *testNotes) Recent(ctx context.Context, workspaceID string, limit int) ([]NotePreview, error) {
	n.calls++
	n.workspaceID = workspaceID
	n.limit = limit
	_, n.deadline = ctx.Deadline()
	return n.items, n.err
}
