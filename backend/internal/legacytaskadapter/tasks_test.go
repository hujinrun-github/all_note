package legacytaskadapter

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/taskdomain"
)

func TestProjectLegacyTaskPreservesWorkspaceLinksAndIndependentRevisions(t *testing.T) {
	due := time.Date(2026, 7, 23, 3, 4, 5, 0, time.UTC)
	snapshot := legacyTaskProjectionFixture()
	snapshot.Project.Project.Horizon = taskdomain.ProjectHorizonLong
	snapshot.Occurrence.DueAt = &due
	snapshot.Occurrence.Status = taskdomain.ExecutionStatusBlocked
	snapshot.Occurrence.BlockedReason = "waiting for review"
	snapshot.Task.NoteID = "note-1"
	snapshot.Task.RoadmapNodeID = "node-1"

	got, err := ProjectLegacyTask(snapshot)
	if err != nil {
		t.Fatalf("ProjectLegacyTask() unexpected error: %v", err)
	}
	if got.WorkspaceID != "workspace-1" || got.ID != "task-1" || got.TaskID != "task-1" || got.OccurrenceID != "occurrence-1" {
		t.Fatalf("identity = %#v", got)
	}
	if got.ProjectID != "project-1" || got.NoteID != "note-1" || got.RoadmapNodeID != "node-1" {
		t.Fatalf("stable links = %#v", got)
	}
	if got.Horizon != LegacyHorizonLong || got.Scope != LegacyScopeYearly || got.ExecutionType != LegacyExecutionSingle {
		t.Fatalf("legacy dimensions = horizon %q scope %q type %q", got.Horizon, got.Scope, got.ExecutionType)
	}
	if got.TaskRevision != 7 || got.ScheduleRevision != 5 || got.OccurrenceRevision != 11 {
		t.Fatalf("revisions = task %d schedule %d occurrence %d", got.TaskRevision, got.ScheduleRevision, got.OccurrenceRevision)
	}
	if got.Status != string(taskdomain.ExecutionStatusBlocked) || got.Done != 0 || got.Due == nil || *got.Due != due.Unix() {
		t.Fatalf("execution projection = %#v", got)
	}
}

func TestProjectLegacyTaskMapsRecurrenceAndOccurrenceDeterministically(t *testing.T) {
	tests := []struct {
		name       string
		recurrence taskdomain.RecurrenceType
		scope      string
	}{
		{name: "daily", recurrence: taskdomain.RecurrenceDaily, scope: LegacyScopeDaily},
		{name: "weekly", recurrence: taskdomain.RecurrenceWeekly, scope: LegacyScopeWeekly},
		{name: "monthly", recurrence: taskdomain.RecurrenceMonthly, scope: LegacyScopeMonthly},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := legacyTaskProjectionFixture()
			snapshot.Schedule.RecurrenceType = test.recurrence
			snapshot.Schedule.StartsOn = "2026-07-22"
			snapshot.Schedule.RecurrenceRule = `{"interval":1}`
			snapshot.Occurrence.Recurring = true
			snapshot.Occurrence.OccurrenceKey = "2026-07-23"

			got, err := ProjectLegacyTask(snapshot)
			if err != nil {
				t.Fatalf("ProjectLegacyTask() unexpected error: %v", err)
			}
			if got.ExecutionType != LegacyExecutionRecurring || got.Scope != test.scope || got.OccurrenceDate == nil || *got.OccurrenceDate != "2026-07-23" || got.RecurrenceLabel != string(test.recurrence) {
				t.Fatalf("recurrence projection = %#v", got)
			}
		})
	}
}

func TestProjectLegacyTaskRejectsUnrepresentableAndMismatchedSnapshots(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*LegacyTaskProjectionSnapshot)
		want   error
	}{
		{name: "paused lifecycle", mutate: func(s *LegacyTaskProjectionSnapshot) {
			s.Task.LifecycleStatus = taskdomain.TaskLifecyclePaused
			s.Occurrence.LifecycleStatus = taskdomain.TaskLifecyclePaused
		}, want: ErrLegacyTaskUnrepresentable},
		{name: "cancelled occurrence", mutate: func(s *LegacyTaskProjectionSnapshot) { s.Occurrence.Status = taskdomain.ExecutionStatusCancelled }, want: ErrLegacyTaskUnrepresentable},
		{name: "workspace mismatch", mutate: func(s *LegacyTaskProjectionSnapshot) { s.Occurrence.WorkspaceID = "workspace-2" }, want: ErrLegacyTaskWorkspaceMismatch},
		{name: "task mismatch", mutate: func(s *LegacyTaskProjectionSnapshot) { s.Schedule.TaskID = "task-2" }, want: ErrLegacyTaskBindingMismatch},
		{name: "recurrence mismatch", mutate: func(s *LegacyTaskProjectionSnapshot) { s.Occurrence.Recurring = true }, want: ErrLegacyTaskBindingMismatch},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := legacyTaskProjectionFixture()
			test.mutate(&snapshot)
			_, err := ProjectLegacyTask(snapshot)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestPlanCreateLegacyTaskUsesPersonalAndMapsSingleSchedule(t *testing.T) {
	plannedDate := "2026-07-23"
	due := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC).Unix()
	request := LegacyTaskCreate{
		Title: "  Ship release  ", Content: "keep every byte", PlannedDate: &plannedDate, Due: &due,
		Priority: 2, Horizon: LegacyHorizonWeek, Scope: LegacyScopeDaily, NoteID: "note-1", RoadmapNodeID: "node-1",
	}
	personal := taskdomain.ProjectSnapshot{Project: taskdomain.Project{WorkspaceID: "workspace-1", ID: "personal", Horizon: taskdomain.ProjectHorizonShort, SystemRole: taskdomain.ProjectSystemRolePersonal}, Revision: 4}

	plan, err := PlanCreateLegacyTask(request, "workspace-1", taskdomain.ProjectSnapshot{}, personal, "Asia/Shanghai")
	if err != nil {
		t.Fatalf("PlanCreateLegacyTask() unexpected error: %v", err)
	}
	if plan.ProjectID != "personal" || plan.ExpectedProjectRevision != 4 || plan.Definition.Title != "Ship release" || plan.Definition.Description != "keep every byte" {
		t.Fatalf("definition plan = %#v", plan)
	}
	if plan.NoteID != "note-1" || plan.RoadmapNodeID != "node-1" || plan.Schedule.RecurrenceType != taskdomain.RecurrenceNone || plan.Schedule.TimingType != taskdomain.TimingDate || plan.Schedule.StartsOn != plannedDate {
		t.Fatalf("schedule plan = %#v", plan)
	}
	if plan.DueAt == nil || plan.DueAt.Unix() != due {
		t.Fatalf("due = %v", plan.DueAt)
	}
}

func TestPlanCreateLegacyTaskMapsRecurringRule(t *testing.T) {
	end := "2026-09-30"
	request := LegacyTaskCreate{
		Title: "Weekly review", ProjectID: "project-1", Priority: 1, Horizon: LegacyHorizonLong, Scope: LegacyScopeWeekly,
		ExecutionType: LegacyExecutionRecurring,
		Recurrence:    &LegacyRecurrenceConfig{StartDate: "2026-07-22", EndDate: &end, Frequency: "weekly", Interval: 2, Weekdays: []int{5, 1, 5}, Timezone: "Asia/Shanghai"},
	}
	selected := taskdomain.ProjectSnapshot{Project: taskdomain.Project{WorkspaceID: "workspace-1", ID: "project-1", Horizon: taskdomain.ProjectHorizonLong}, Revision: 8}

	plan, err := PlanCreateLegacyTask(request, "workspace-1", selected, taskdomain.ProjectSnapshot{}, "UTC")
	if err != nil {
		t.Fatalf("PlanCreateLegacyTask() unexpected error: %v", err)
	}
	if plan.ProjectID != "project-1" || plan.Schedule.RecurrenceType != taskdomain.RecurrenceWeekly || plan.Schedule.TimingType != taskdomain.TimingDate || plan.Schedule.Timezone != "Asia/Shanghai" || plan.Schedule.EndsOn != end {
		t.Fatalf("recurring plan = %#v", plan)
	}
	if plan.Schedule.Rule == nil || plan.Schedule.Rule.Interval != 2 || len(plan.Schedule.Rule.Weekdays) != 2 || plan.Schedule.Rule.Weekdays[0] != 1 || plan.Schedule.Rule.Weekdays[1] != 5 {
		t.Fatalf("canonical rule = %#v", plan.Schedule.Rule)
	}
}

func TestPlanCreateLegacyTaskRejectsLossyDimensionsAndInvalidPriority(t *testing.T) {
	project := taskdomain.ProjectSnapshot{Project: taskdomain.Project{WorkspaceID: "workspace-1", ID: "project-1", Horizon: taskdomain.ProjectHorizonShort}, Revision: 1}
	tests := []LegacyTaskCreate{
		{Title: "wrong horizon", ProjectID: "project-1", Horizon: LegacyHorizonLong, Scope: LegacyScopeDaily},
		{Title: "wrong scope", ProjectID: "project-1", Horizon: LegacyHorizonWeek, Scope: LegacyScopeYearly},
		{Title: "bad priority", ProjectID: "project-1", Horizon: LegacyHorizonWeek, Scope: LegacyScopeDaily, Priority: 4},
		{Title: "recurring without rule", ProjectID: "project-1", Horizon: LegacyHorizonWeek, Scope: LegacyScopeDaily, ExecutionType: LegacyExecutionRecurring},
	}
	for _, request := range tests {
		if _, err := PlanCreateLegacyTask(request, "workspace-1", project, taskdomain.ProjectSnapshot{}, "UTC"); !errors.Is(err, ErrInvalidLegacyTask) {
			t.Fatalf("request %q error = %v, want %v", request.Title, err, ErrInvalidLegacyTask)
		}
	}
}

func TestPlanLegacyTaskPatchOnlyProducesStableDefinitionAfterImage(t *testing.T) {
	snapshot := legacyTaskProjectionFixture()
	title := " Updated "
	content := "new description"
	priority := 3
	sortOrder := 8.5
	noteID := "note-2"
	roadmapNodeID := "node-2"
	input := PatchLegacyTaskInput{Current: snapshot, Patch: LegacyTaskPatch{Title: &title, Content: &content, Priority: &priority, SortOrder: &sortOrder, NoteID: &noteID, RoadmapNodeID: &roadmapNodeID}}

	plan, err := PlanLegacyTaskPatch(input)
	if err != nil {
		t.Fatalf("PlanLegacyTaskPatch() unexpected error: %v", err)
	}
	if plan.ExpectedTaskRevision != 7 || plan.ExpectedScheduleRevision != 5 || plan.ExpectedOccurrenceRevision != 11 {
		t.Fatalf("expected revisions = %#v", plan)
	}
	if plan.Task.Title != "Updated" || plan.Task.Description != content || plan.Task.Priority != 3 || plan.Task.SortOrder != sortOrder || plan.Task.NoteID != noteID || plan.Task.RoadmapNodeID != roadmapNodeID {
		t.Fatalf("after-image = %#v", plan.Task)
	}
	if snapshot.Task.Title != "Original" || snapshot.Task.NoteID != "" {
		t.Fatalf("input mutated: %#v", snapshot.Task)
	}
}

func TestPlanLegacyTaskPatchRequiresExplicitCommandsForLifecycleAndOccurrenceFields(t *testing.T) {
	done := 1
	status := "done"
	plannedDate := "2026-07-24"
	due := time.Now().Unix()
	executionType := LegacyExecutionRecurring
	horizon := LegacyHorizonLong
	scope := LegacyScopeWeekly
	tests := []LegacyTaskPatch{
		{Done: &done}, {Status: &status}, {PlannedDate: &plannedDate}, {Due: &due},
		{ExecutionType: &executionType}, {Recurrence: &LegacyRecurrenceConfig{Frequency: "daily"}}, {Horizon: &horizon}, {Scope: &scope},
	}
	for _, patch := range tests {
		_, err := PlanLegacyTaskPatch(PatchLegacyTaskInput{Current: legacyTaskProjectionFixture(), Patch: patch})
		if !errors.Is(err, ErrLegacyTaskCommandRequired) {
			t.Fatalf("patch %#v error = %v, want %v", patch, err, ErrLegacyTaskCommandRequired)
		}
	}
}

func TestPlanLegacyTaskPatchRejectsAStateTheOldContractCannotExpress(t *testing.T) {
	snapshot := legacyTaskProjectionFixture()
	snapshot.Task.LifecycleStatus = taskdomain.TaskLifecyclePaused
	snapshot.Occurrence.LifecycleStatus = taskdomain.TaskLifecyclePaused
	title := "still not safely editable through legacy"
	_, err := PlanLegacyTaskPatch(PatchLegacyTaskInput{Current: snapshot, Patch: LegacyTaskPatch{Title: &title}})
	if !errors.Is(err, ErrLegacyTaskUnrepresentable) {
		t.Fatalf("error = %v, want %v", err, ErrLegacyTaskUnrepresentable)
	}
}

func TestPlanDeleteLegacyTaskCancelsAggregateAndTombstonesIDMap(t *testing.T) {
	at := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	input := legacyDeleteTaskFixture(at)
	plan, err := PlanDeleteLegacyTask(input)
	if err != nil {
		t.Fatalf("PlanDeleteLegacyTask() unexpected error: %v", err)
	}
	if plan.NoOp || plan.Task.LifecycleStatus != taskdomain.TaskLifecycleCancelled || plan.Task.GenerationEnabled {
		t.Fatalf("task cancellation = %#v", plan)
	}
	if plan.Task.Occurrences[0].ExecutionStatus != taskdomain.ExecutionStatusCancelled || plan.Task.Occurrences[1].ExecutionStatus != taskdomain.ExecutionStatusDone {
		t.Fatalf("occurrences = %#v", plan.Task.Occurrences)
	}
	if len(plan.ExecutionLogs) != 1 || !plan.IDMapAfter.Tombstoned || plan.IDMapAfter.Revision != 4 || plan.IDMapAfter.TombstoneCommandID != "delete-command" {
		t.Fatalf("audit plan = %#v", plan)
	}
	if input.Task.LifecycleStatus != taskdomain.TaskLifecycleActive || input.IDMap.Tombstoned {
		t.Fatalf("input mutated: %#v", input)
	}
}

func TestPlanDeleteLegacyTaskIsIdempotentOnlyForCompleteTombstone(t *testing.T) {
	at := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	input := legacyDeleteTaskFixture(at)
	input.Task.LifecycleStatus = taskdomain.TaskLifecycleCancelled
	input.Task.GenerationEnabled = false
	input.Task.Occurrences[0].ExecutionStatus = taskdomain.ExecutionStatusCancelled
	input.IDMap.Tombstoned = true
	input.IDMap.TombstonedAt = at
	input.IDMap.TombstoneCommandID = "old-command"
	input.IDMap.TombstonedBy = "actor-1"

	plan, err := PlanDeleteLegacyTask(input)
	if err != nil || !plan.NoOp {
		t.Fatalf("idempotent delete = %#v, %v", plan, err)
	}
	input.IDMap.TombstonedBy = ""
	if _, err := PlanDeleteLegacyTask(input); !errors.Is(err, ErrLegacyTaskDeleteStateConflict) {
		t.Fatalf("partial tombstone error = %v, want %v", err, ErrLegacyTaskDeleteStateConflict)
	}
}

func TestPlanDeleteLegacyTaskRejectsCrossWorkspaceAndIDMapConflicts(t *testing.T) {
	at := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		mutate func(*DeleteLegacyTaskInput)
		want   error
	}{
		{name: "workspace", mutate: func(i *DeleteLegacyTaskInput) { i.Task.WorkspaceID = "workspace-2" }, want: ErrLegacyTaskWorkspaceMismatch},
		{name: "legacy id", mutate: func(i *DeleteLegacyTaskInput) { i.IDMap.LegacyTaskID = "legacy-2" }, want: ErrLegacyTaskIDMapConflict},
		{name: "task id", mutate: func(i *DeleteLegacyTaskInput) { i.IDMap.TaskID = "task-2" }, want: ErrLegacyTaskIDMapConflict},
		{name: "missing map", mutate: func(i *DeleteLegacyTaskInput) { i.IDMap = LegacyTaskIDMap{} }, want: ErrLegacyTaskIDMapMissing},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := legacyDeleteTaskFixture(at)
			test.mutate(&input)
			_, err := PlanDeleteLegacyTask(input)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestLegacyTaskAdapterIsPureAndCannotBecomeASecondWriteSource(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve tasks_test.go path")
	}
	tasksFile := filepath.Join(filepath.Dir(currentFile), "tasks.go")
	parsed, err := parser.ParseFile(token.NewFileSet(), tasksFile, nil, parser.AllErrors)
	if err != nil {
		t.Fatalf("parse tasks.go: %v", err)
	}

	forbiddenImports := []string{"context", "database/sql", "net/http", "/storage", "/service", "/handler", "gin-gonic"}
	for _, spec := range parsed.Imports {
		path := strings.Trim(spec.Path.Value, `"`)
		for _, forbidden := range forbiddenImports {
			if path == forbidden || strings.Contains(path, forbidden) {
				t.Fatalf("legacy task adapter imports stateful boundary %q", path)
			}
		}
	}

	forbiddenCalls := map[string]struct{}{
		"Begin": {}, "BeginTx": {}, "Exec": {}, "ExecContext": {}, "Query": {}, "QueryContext": {},
		"Transact": {}, "Save": {}, "Create": {}, "Update": {}, "Delete": {}, "Do": {},
	}
	ast.Inspect(parsed, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if _, forbidden := forbiddenCalls[selector.Sel.Name]; forbidden {
			t.Errorf("legacy task adapter performs forbidden side-effect call .%s", selector.Sel.Name)
		}
		return true
	})
}

func legacyTaskProjectionFixture() LegacyTaskProjectionSnapshot {
	return LegacyTaskProjectionSnapshot{
		Project:                taskdomain.ProjectSnapshot{Project: taskdomain.Project{WorkspaceID: "workspace-1", ID: "project-1", Name: "Project", Kind: taskdomain.ProjectKindStandard, Horizon: taskdomain.ProjectHorizonShort, Status: taskdomain.ProjectStatusActive}, Revision: 3},
		Task:                   taskdomain.TaskRecord{WorkspaceID: "workspace-1", ID: "task-1", ProjectID: "project-1", Title: "Original", Description: "description", LifecycleStatus: taskdomain.TaskLifecycleActive, Priority: 1, SortOrder: 2.5, Revision: 7},
		Schedule:               taskdomain.ScheduleVersion{WorkspaceID: "workspace-1", TaskID: "task-1", ScheduleRevision: 5, RecurrenceType: taskdomain.RecurrenceNone, TimingType: taskdomain.TimingUnscheduled, Timezone: "Asia/Shanghai"},
		ScheduleHeaderRevision: 5,
		Occurrence:             taskdomain.QueryOccurrenceSnapshot{WorkspaceID: "workspace-1", ProjectID: "project-1", TaskID: "task-1", OccurrenceID: "occurrence-1", OccurrenceKey: "once", Title: "Original", Description: "description", TimingType: taskdomain.TimingUnscheduled, Timezone: "Asia/Shanghai", Status: taskdomain.ExecutionStatusOpen, Revision: 11, TaskRevision: 7, ScheduleRevision: 5, GeneratedScheduleRevision: 5, LifecycleStatus: taskdomain.TaskLifecycleActive, Priority: 1, SortOrder: 2.5},
	}
}

func legacyDeleteTaskFixture(at time.Time) DeleteLegacyTaskInput {
	open := taskdomain.Occurrence{WorkspaceID: "workspace-1", ID: "occurrence-open", TaskID: "task-1", OccurrenceKey: "2026-07-22", ExecutionStatus: taskdomain.ExecutionStatusOpen, Recurring: true, Revision: 5}
	doneAt := at.Add(-time.Hour)
	done := taskdomain.Occurrence{WorkspaceID: "workspace-1", ID: "occurrence-done", TaskID: "task-1", OccurrenceKey: "2026-07-21", ExecutionStatus: taskdomain.ExecutionStatusDone, Recurring: true, CompletedAt: &doneAt, Revision: 2}
	return DeleteLegacyTaskInput{
		WorkspaceID: "workspace-1", LegacyTaskID: "legacy-task-1", CommandID: "delete-command", ActorID: "actor-1", DeletedAt: at,
		Task:  taskdomain.TaskAggregate{WorkspaceID: "workspace-1", TaskID: "task-1", LifecycleStatus: taskdomain.TaskLifecycleActive, Recurring: true, Revision: 9, GenerationEnabled: true, Occurrences: []taskdomain.Occurrence{open, done}},
		IDMap: LegacyTaskIDMap{WorkspaceID: "workspace-1", LegacyTaskID: "legacy-task-1", TaskID: "task-1", Revision: 3},
	}
}
