package legacytaskadapter

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/taskdomain"
)

func TestDraftEventTaskCreatesNonRecurringTimeBlockInVersionTimezone(t *testing.T) {
	start := time.Date(2026, 7, 21, 23, 30, 0, 0, time.UTC).Unix()
	end := time.Date(2026, 7, 22, 0, 30, 0, 0, time.UTC).Unix()
	request := LegacyEventCreate{
		Title: "语音服务评审", StartTime: &start, EndTime: &end,
		Location: "会议室 A", Kind: "work", Notes: "确认转写方案", NoteID: "occ-note-1",
	}

	draft, err := DraftEventTask(request, "workspace-1", "personal-project", "Asia/Shanghai")
	if err != nil {
		t.Fatalf("DraftEventTask() unexpected error: %v", err)
	}
	if draft.WorkspaceID != "workspace-1" || draft.ProjectID != "personal-project" || draft.Title != request.Title {
		t.Fatalf("task draft identity = %#v", draft)
	}
	if draft.Schedule.RecurrenceType != taskdomain.RecurrenceNone || draft.Schedule.TimingType != taskdomain.TimingTimeBlock {
		t.Fatalf("schedule types = (%q, %q)", draft.Schedule.RecurrenceType, draft.Schedule.TimingType)
	}
	if draft.Schedule.Timezone != "Asia/Shanghai" || draft.Schedule.StartsOn != "2026-07-22" || draft.Schedule.LocalStartTime != "07:30:00" || draft.Schedule.DurationMinutes != 60 {
		t.Fatalf("schedule local fields = %#v", draft.Schedule)
	}
	if draft.Occurrence.PlannedStartAt == nil || draft.Occurrence.PlannedStartAt.Unix() != start || draft.Occurrence.PlannedEndAt == nil || draft.Occurrence.PlannedEndAt.Unix() != end {
		t.Fatalf("occurrence instants = (%v, %v)", draft.Occurrence.PlannedStartAt, draft.Occurrence.PlannedEndAt)
	}
	if draft.Occurrence.PlannedDate != "2026-07-22" || draft.Occurrence.Location != request.Location || draft.Occurrence.CalendarKind != request.Kind || draft.Occurrence.CalendarNotes != request.Notes || draft.Occurrence.NoteID != request.NoteID {
		t.Fatalf("occurrence metadata = %#v", draft.Occurrence)
	}
}

func TestDraftEventTaskCreatesAllDayDateWithoutFakeUTCInstants(t *testing.T) {
	request := LegacyEventCreate{
		Title: "发布窗口", IsAllDay: true, PlannedDate: "2026-07-22", AllDayEndDate: "2026-07-25",
		ProjectID: "project-1", Location: "线上", Kind: "release", Notes: "三天窗口", NoteID: "occ-note-2",
	}

	draft, err := DraftEventTask(request, "workspace-1", "personal-project", "America/New_York")
	if err != nil {
		t.Fatalf("DraftEventTask() unexpected error: %v", err)
	}
	if draft.ProjectID != "project-1" {
		t.Fatalf("project ID = %q, want project-1", draft.ProjectID)
	}
	if draft.Schedule.RecurrenceType != taskdomain.RecurrenceNone || draft.Schedule.TimingType != taskdomain.TimingDate || draft.Schedule.StartsOn != "2026-07-22" || draft.Schedule.Timezone != "America/New_York" {
		t.Fatalf("all-day schedule = %#v", draft.Schedule)
	}
	if draft.Occurrence.PlannedStartAt != nil || draft.Occurrence.PlannedEndAt != nil {
		t.Fatalf("all-day draft forged UTC midnight instants: %#v", draft.Occurrence)
	}
	if draft.Occurrence.PlannedDate != "2026-07-22" || draft.Occurrence.AllDayEndDate != "2026-07-25" {
		t.Fatalf("all-day range = [%q,%q)", draft.Occurrence.PlannedDate, draft.Occurrence.AllDayEndDate)
	}
	if draft.Occurrence.Location != request.Location || draft.Occurrence.CalendarKind != request.Kind || draft.Occurrence.CalendarNotes != request.Notes || draft.Occurrence.NoteID != request.NoteID {
		t.Fatalf("all-day metadata = %#v", draft.Occurrence)
	}
}

func TestProjectLegacyEventSupportsTimeBlockAndDate(t *testing.T) {
	start := time.Date(2026, 7, 22, 1, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	due := end.Add(24 * time.Hour)
	timeSchedule := mustAdapterSchedule(t, taskdomain.ScheduleInput{
		RecurrenceType: taskdomain.RecurrenceDaily, TimingType: taskdomain.TimingTimeBlock,
		Timezone: "Asia/Shanghai", StartsOn: "2026-07-22", LocalStartTime: "09:00:00", DurationMinutes: 60,
		Rule: json.RawMessage(`{"interval":1}`),
	})
	timeEntry := taskdomain.CalendarEntry{
		WorkspaceID: "workspace-1", ProjectID: "project-1", TaskID: "task-1", OccurrenceID: "occ-1",
		Title: "评审", DisplayType: taskdomain.TimingTimeBlock, Timezone: "Asia/Shanghai", PlannedDate: "2026-07-22",
		StartAt: &start, EndAt: &end, DueAt: &due, Status: taskdomain.ExecutionStatusActive, Recurring: true, Revision: 9,
		Location: "A101", CalendarKind: "work", CalendarNotes: "带录音", TaskNoteID: "task-note", OccurrenceNoteID: "occ-note",
	}

	timeEvent, err := ProjectLegacyEvent(EventProjectionSnapshot{Entry: timeEntry, ScheduleVersion: timeSchedule})
	if err != nil {
		t.Fatalf("ProjectLegacyEvent(time block) unexpected error: %v", err)
	}
	if timeEvent.IsAllDay || timeEvent.StartTime == nil || *timeEvent.StartTime != start.Unix() || timeEvent.EndTime == nil || *timeEvent.EndTime != end.Unix() {
		t.Fatalf("time-block legacy event = %#v", timeEvent)
	}
	if timeEvent.WorkspaceID != "workspace-1" || timeEvent.ProjectID != "project-1" || timeEvent.TaskID != "task-1" || timeEvent.OccurrenceID != "occ-1" || timeEvent.Revision != 9 || !timeEvent.Recurring {
		t.Fatalf("legacy identity/revision = %#v", timeEvent)
	}
	if timeEvent.Location != "A101" || timeEvent.Kind != "work" || timeEvent.Notes != "带录音" || timeEvent.NoteID != "occ-note" || timeEvent.TaskNoteID != "task-note" {
		t.Fatalf("legacy time-block metadata = %#v", timeEvent)
	}

	dateSchedule := mustAdapterSchedule(t, taskdomain.ScheduleInput{
		RecurrenceType: taskdomain.RecurrenceNone, TimingType: taskdomain.TimingDate,
		Timezone: "America/New_York", StartsOn: "2026-07-23",
	})
	dateEntry := taskdomain.CalendarEntry{
		WorkspaceID: "workspace-1", ProjectID: "project-2", TaskID: "task-2", OccurrenceID: "occ-2",
		Title: "全天发布", DisplayType: taskdomain.TimingDate, Timezone: "America/New_York", PlannedDate: "2026-07-23",
		AllDayEndDate: "2026-07-25", Status: taskdomain.ExecutionStatusOpen, Revision: 3,
		Location: "线上", CalendarKind: "release", CalendarNotes: "发布窗口", OccurrenceNoteID: "occ-note-2",
	}
	dateEvent, err := ProjectLegacyEvent(EventProjectionSnapshot{Entry: dateEntry, ScheduleVersion: dateSchedule})
	if err != nil {
		t.Fatalf("ProjectLegacyEvent(date) unexpected error: %v", err)
	}
	if !dateEvent.IsAllDay || dateEvent.StartTime != nil || dateEvent.EndTime != nil {
		t.Fatalf("date projection forged legacy instants: %#v", dateEvent)
	}
	if dateEvent.PlannedDate != "2026-07-23" || dateEvent.AllDayEndDate != "2026-07-25" {
		t.Fatalf("date legacy range = %#v", dateEvent)
	}
}

func TestProjectLegacyEventUsesScheduleVersionTimezone(t *testing.T) {
	start := time.Date(2026, 7, 21, 23, 30, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	entry := taskdomain.CalendarEntry{
		WorkspaceID: "workspace-1", ProjectID: "project-1", TaskID: "task-1", OccurrenceID: "occ-1",
		Title: "跨日事件", DisplayType: taskdomain.TimingTimeBlock, PlannedDate: "2026-07-22", StartAt: &start, EndAt: &end,
		Timezone: "America/Los_Angeles", Status: taskdomain.ExecutionStatusOpen, Revision: 1,
	}
	version := mustAdapterSchedule(t, taskdomain.ScheduleInput{
		RecurrenceType: taskdomain.RecurrenceNone, TimingType: taskdomain.TimingTimeBlock,
		Timezone: "Asia/Shanghai", StartsOn: "2026-07-22", LocalStartTime: "07:30:00", DurationMinutes: 60,
	})

	event, err := ProjectLegacyEvent(EventProjectionSnapshot{Entry: entry, ScheduleVersion: version})
	if err != nil {
		t.Fatalf("ProjectLegacyEvent() unexpected error: %v", err)
	}
	if event.Timezone != "Asia/Shanghai" || event.PlannedDate != "2026-07-22" {
		t.Fatalf("projection timezone/date = (%q,%q), want immutable version timezone/date", event.Timezone, event.PlannedDate)
	}

	wrongVersion := version
	wrongVersion.Timezone = "UTC"
	_, err = ProjectLegacyEvent(EventProjectionSnapshot{Entry: entry, ScheduleVersion: wrongVersion})
	if !errors.Is(err, ErrInvalidLegacyEvent) {
		t.Fatalf("mismatched immutable timezone error = %v, want %v", err, ErrInvalidLegacyEvent)
	}
}

func TestMergeLegacyEventPatchPreservesOmittedExtendedFields(t *testing.T) {
	start := int64(1784682000)
	end := start + 3600
	current := LegacyEvent{
		WorkspaceID: "workspace-1", ProjectID: "project-1", TaskID: "task-1", OccurrenceID: "occ-1",
		Title: "旧标题", StartTime: &start, EndTime: &end, IsAllDay: false, PlannedDate: "2026-07-22",
		Location: "A101", Kind: "work", Notes: "保留备注", NoteID: "occ-note", TaskNoteID: "task-note", Revision: 4,
	}
	newTitle := "新标题"

	merged := MergeLegacyEventPatch(current, LegacyEventPatch{Title: &newTitle})
	if merged.Title != newTitle || merged.Location != current.Location || merged.Kind != current.Kind || merged.Notes != current.Notes || merged.NoteID != current.NoteID || merged.TaskNoteID != current.TaskNoteID || merged.PlannedDate != current.PlannedDate || merged.IsAllDay != current.IsAllDay {
		t.Fatalf("omitted fields were not preserved: %#v", merged)
	}
	if merged.StartTime == current.StartTime || merged.EndTime == current.EndTime || *merged.StartTime != *current.StartTime || *merged.EndTime != *current.EndTime {
		t.Fatalf("merged timestamp pointers were not independently copied")
	}

	clearLocation := ""
	newNotes := "更新备注"
	merged = MergeLegacyEventPatch(current, LegacyEventPatch{Location: &clearLocation, Notes: &newNotes})
	if merged.Location != "" || merged.Notes != newNotes || merged.Kind != current.Kind || merged.NoteID != current.NoteID {
		t.Fatalf("explicit extended patch = %#v", merged)
	}
}

func TestDraftEventTaskValidatesRangesAndUsesPersonalFallback(t *testing.T) {
	start := time.Date(2026, 7, 22, 1, 0, 0, 0, time.UTC).Unix()
	end := start + 3600
	tests := []struct {
		name    string
		request LegacyEventCreate
	}{
		{name: "blank title", request: LegacyEventCreate{StartTime: &start, EndTime: &end}},
		{name: "time block missing end", request: LegacyEventCreate{Title: "event", StartTime: &start}},
		{name: "time block invalid interval", request: LegacyEventCreate{Title: "event", StartTime: &end, EndTime: &start}},
		{name: "time block sub-minute duration", request: LegacyEventCreate{Title: "event", StartTime: &start, EndTime: legacyInt64(start + 30)}},
		{name: "all day missing date", request: LegacyEventCreate{Title: "event", IsAllDay: true}},
		{name: "all day contains timestamps", request: LegacyEventCreate{Title: "event", IsAllDay: true, PlannedDate: "2026-07-22", StartTime: &start, EndTime: &end}},
		{name: "all day invalid exclusive end", request: LegacyEventCreate{Title: "event", IsAllDay: true, PlannedDate: "2026-07-22", AllDayEndDate: "2026-07-22"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DraftEventTask(tt.request, "workspace-1", "personal-project", "UTC")
			if !errors.Is(err, ErrInvalidLegacyEvent) {
				t.Fatalf("DraftEventTask() error = %v, want %v", err, ErrInvalidLegacyEvent)
			}
		})
	}

	draft, err := DraftEventTask(LegacyEventCreate{Title: "event", StartTime: &start, EndTime: &end, ProjectID: "  "}, "workspace-1", "personal-project", "UTC")
	if err != nil {
		t.Fatalf("DraftEventTask(personal fallback) error: %v", err)
	}
	if draft.ProjectID != "personal-project" {
		t.Fatalf("blank project fallback = %q", draft.ProjectID)
	}
}

func mustAdapterSchedule(t *testing.T, input taskdomain.ScheduleInput) taskdomain.Schedule {
	t.Helper()
	schedule, err := taskdomain.NormalizeSchedule(input)
	if err != nil {
		t.Fatalf("NormalizeSchedule() unexpected error: %v", err)
	}
	return schedule
}

func legacyInt64(value int64) *int64 {
	return &value
}

func TestMergeLegacyEventPatchDoesNotMutateInput(t *testing.T) {
	start := int64(100)
	end := int64(200)
	current := LegacyEvent{Title: "old", StartTime: &start, EndTime: &end, Notes: "keep"}
	original := current
	newTitle := "new"
	_ = MergeLegacyEventPatch(current, LegacyEventPatch{Title: &newTitle})
	if !reflect.DeepEqual(current, original) {
		t.Fatalf("MergeLegacyEventPatch() mutated input: got %#v, want %#v", current, original)
	}
}

func TestPlanDeleteEventCancelsSingleTaskAndOccurrenceAndTombstonesIDMap(t *testing.T) {
	input := legacyDeleteInput(taskdomain.TaskLifecycleActive, taskdomain.ExecutionStatusBlocked)
	original := cloneLegacyDeleteInputForTest(input)

	plan, err := PlanDeleteEvent(input)
	if err != nil {
		t.Fatalf("PlanDeleteEvent() unexpected error: %v", err)
	}
	if plan.NoOp {
		t.Fatal("first delete unexpectedly produced a no-op plan")
	}
	if plan.ExpectedTaskRevision != 5 || plan.ExpectedOccurrenceRevision != 11 {
		t.Fatalf("expected revisions = task:%d occurrence:%d", plan.ExpectedTaskRevision, plan.ExpectedOccurrenceRevision)
	}
	if plan.Task.LifecycleStatus != taskdomain.TaskLifecycleCancelled || plan.Task.Revision != 6 || plan.Task.GenerationEnabled {
		t.Fatalf("task cancellation after-image = %#v", plan.Task)
	}
	if plan.Occurrence.ExecutionStatus != taskdomain.ExecutionStatusCancelled || plan.Occurrence.Revision != 12 || plan.Occurrence.BlockedReason != "" || plan.Occurrence.NextAction != "" {
		t.Fatalf("occurrence cancellation after-image = %#v", plan.Occurrence)
	}
	if len(plan.Task.Occurrences) != 1 || !reflect.DeepEqual(plan.Task.Occurrences[0], plan.Occurrence) {
		t.Fatalf("task aggregate does not contain occurrence after-image: %#v", plan.Task)
	}
	if plan.ExecutionLog.FromStatus() != taskdomain.ExecutionStatusBlocked || plan.ExecutionLog.ToStatus() != taskdomain.ExecutionStatusCancelled || plan.ExecutionLog.OccurrenceRevision() != 12 {
		t.Fatalf("execution log = from:%q to:%q revision:%d", plan.ExecutionLog.FromStatus(), plan.ExecutionLog.ToStatus(), plan.ExecutionLog.OccurrenceRevision())
	}
	if !reflect.DeepEqual(plan.IDMapBefore, input.IDMap) {
		t.Fatalf("ID map audit did not retain before-image: %#v", plan.IDMapBefore)
	}
	if plan.IDMapAfter.WorkspaceID != "workspace-1" || plan.IDMapAfter.LegacyEventID != "legacy-event-1" || plan.IDMapAfter.TaskID != "task-1" || plan.IDMapAfter.OccurrenceID != "occurrence-1" ||
		!plan.IDMapAfter.Tombstoned || !plan.IDMapAfter.TombstonedAt.Equal(input.DeletedAt) || plan.IDMapAfter.TombstoneCommandID != "delete-command-1" || plan.IDMapAfter.TombstonedBy != "user-1" || plan.IDMapAfter.Revision != 4 {
		t.Fatalf("ID map tombstone after-image = %#v", plan.IDMapAfter)
	}
	if !reflect.DeepEqual(input, original) {
		t.Fatalf("PlanDeleteEvent() mutated input: got %#v, want %#v", input, original)
	}
}

func TestPlanDeleteEventIsIdempotentAfterCancellationAndTombstone(t *testing.T) {
	input := legacyDeleteInput(taskdomain.TaskLifecycleCancelled, taskdomain.ExecutionStatusCancelled)
	input.IDMap.Tombstoned = true
	input.IDMap.TombstonedAt = time.Date(2026, 7, 21, 8, 0, 0, 0, time.UTC)
	input.IDMap.TombstoneCommandID = "original-delete"
	input.IDMap.TombstonedBy = "original-user"
	input.IDMap.Revision = 4
	original := cloneLegacyDeleteInputForTest(input)

	plan, err := PlanDeleteEvent(input)
	if err != nil {
		t.Fatalf("PlanDeleteEvent(repeated) unexpected error: %v", err)
	}
	if !plan.NoOp || !plan.ExecutionLog.IsZero() || !reflect.DeepEqual(plan.IDMapBefore, input.IDMap) || !reflect.DeepEqual(plan.IDMapAfter, input.IDMap) {
		t.Fatalf("idempotent delete plan = %#v", plan)
	}
	if plan.Task.TaskID != "" || plan.Occurrence.ID != "" {
		t.Fatalf("no-op plan unexpectedly requested state writes: %#v", plan)
	}
	if !reflect.DeepEqual(input, original) {
		t.Fatalf("repeated delete mutated input: got %#v, want %#v", input, original)
	}
}

func TestPlanDeleteEventRejectsUnsafeOrConflictingBindingsWithZeroPlan(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*DeleteEventInput)
		want   error
	}{
		{name: "missing ID map", mutate: func(input *DeleteEventInput) { input.IDMap = LegacyEventIDMap{} }, want: ErrLegacyEventIDMapMissing},
		{name: "conflicting legacy ID map", mutate: func(input *DeleteEventInput) { input.IDMap.LegacyEventID = "other-event" }, want: ErrLegacyEventIDMapConflict},
		{name: "conflicting task ID map", mutate: func(input *DeleteEventInput) { input.IDMap.TaskID = "other-task" }, want: ErrLegacyEventIDMapConflict},
		{name: "conflicting occurrence ID map", mutate: func(input *DeleteEventInput) { input.IDMap.OccurrenceID = "other-occurrence" }, want: ErrLegacyEventIDMapConflict},
		{name: "task from another workspace", mutate: func(input *DeleteEventInput) { input.Task.WorkspaceID = "workspace-2" }, want: ErrLegacyEventWorkspaceMismatch},
		{name: "occurrence from another workspace", mutate: func(input *DeleteEventInput) { input.Occurrence.WorkspaceID = "workspace-2" }, want: ErrLegacyEventWorkspaceMismatch},
		{name: "ID map from another workspace", mutate: func(input *DeleteEventInput) { input.IDMap.WorkspaceID = "workspace-2" }, want: ErrLegacyEventWorkspaceMismatch},
		{name: "recurring task", mutate: func(input *DeleteEventInput) { input.Task.Recurring = true }, want: ErrLegacyEventDeleteRecurring},
		{name: "recurring occurrence", mutate: func(input *DeleteEventInput) {
			input.Occurrence.Recurring = true
			input.Task.Occurrences[0].Recurring = true
		}, want: ErrLegacyEventDeleteRecurring},
		{name: "not once occurrence", mutate: func(input *DeleteEventInput) {
			input.Occurrence.OccurrenceKey = "2026-07-22"
			input.Task.Occurrences[0].OccurrenceKey = "2026-07-22"
		}, want: ErrLegacyEventBindingMismatch},
		{name: "occurrence belongs to another task", mutate: func(input *DeleteEventInput) {
			input.Occurrence.TaskID = "other-task"
			input.Task.Occurrences[0].TaskID = "other-task"
		}, want: ErrLegacyEventBindingMismatch},
		{name: "aggregate occurrence differs", mutate: func(input *DeleteEventInput) { input.Task.Occurrences[0].Revision++ }, want: ErrLegacyEventBindingMismatch},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := legacyDeleteInput(taskdomain.TaskLifecycleActive, taskdomain.ExecutionStatusOpen)
			tt.mutate(&input)
			original := cloneLegacyDeleteInputForTest(input)

			plan, err := PlanDeleteEvent(input)
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
			if !reflect.DeepEqual(plan, DeleteEventCommandPlan{}) {
				t.Fatalf("rejected delete returned non-zero plan: %#v", plan)
			}
			if !reflect.DeepEqual(input, original) {
				t.Fatalf("rejected delete mutated input: got %#v, want %#v", input, original)
			}
		})
	}
}

func TestLegacyEventCreateGetPatchDateAndTimeBlockRegression(t *testing.T) {
	t.Run("create and get date", func(t *testing.T) {
		create := LegacyEventCreate{Title: "release", IsAllDay: true, PlannedDate: "2026-07-22", AllDayEndDate: "2026-07-24"}
		draft, err := DraftEventTask(create, "workspace-1", "personal", "Asia/Shanghai")
		if err != nil {
			t.Fatal(err)
		}
		entry := taskdomain.CalendarEntry{
			WorkspaceID: "workspace-1", ProjectID: "personal", TaskID: "task-date", OccurrenceID: "occ-date",
			Title: draft.Title, DisplayType: taskdomain.TimingDate, PlannedDate: draft.Occurrence.PlannedDate,
			AllDayEndDate: draft.Occurrence.AllDayEndDate, Status: taskdomain.ExecutionStatusOpen, Revision: 1,
		}
		event, err := ProjectLegacyEvent(EventProjectionSnapshot{Entry: entry, ScheduleVersion: draft.Schedule})
		if err != nil || !event.IsAllDay || event.StartTime != nil || event.EndTime != nil || event.AllDayEndDate != "2026-07-24" {
			t.Fatalf("date round trip = event:%#v error:%v", event, err)
		}
	})

	t.Run("create get and patch time block", func(t *testing.T) {
		start := time.Date(2026, 7, 22, 1, 0, 0, 0, time.UTC).Unix()
		end := start + 3600
		draft, err := DraftEventTask(LegacyEventCreate{Title: "review", StartTime: &start, EndTime: &end}, "workspace-1", "personal", "Asia/Shanghai")
		if err != nil {
			t.Fatal(err)
		}
		entry := taskdomain.CalendarEntry{
			WorkspaceID: "workspace-1", ProjectID: "personal", TaskID: "task-time", OccurrenceID: "occ-time",
			Title: draft.Title, DisplayType: taskdomain.TimingTimeBlock, PlannedDate: draft.Occurrence.PlannedDate,
			StartAt: draft.Occurrence.PlannedStartAt, EndAt: draft.Occurrence.PlannedEndAt, Status: taskdomain.ExecutionStatusOpen, Revision: 2,
		}
		event, err := ProjectLegacyEvent(EventProjectionSnapshot{Entry: entry, ScheduleVersion: draft.Schedule})
		if err != nil || event.StartTime == nil || *event.StartTime != start || event.EndTime == nil || *event.EndTime != end {
			t.Fatalf("time-block round trip = event:%#v error:%v", event, err)
		}
		newTitle := "review updated"
		patched := MergeLegacyEventPatch(event, LegacyEventPatch{Title: &newTitle})
		if patched.Title != newTitle || patched.StartTime == event.StartTime || *patched.StartTime != *event.StartTime {
			t.Fatalf("time-block patch regression = %#v", patched)
		}
	})
}

func legacyDeleteInput(lifecycle taskdomain.TaskLifecycleStatus, execution taskdomain.ExecutionStatus) DeleteEventInput {
	deletedAt := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	occurrence := taskdomain.Occurrence{
		WorkspaceID: "workspace-1", ID: "occurrence-1", TaskID: "task-1", OccurrenceKey: "once",
		ExecutionStatus: execution, Recurring: false, Revision: 11,
	}
	if execution == taskdomain.ExecutionStatusActive || execution == taskdomain.ExecutionStatusBlocked {
		startedAt := deletedAt.Add(-time.Hour)
		occurrence.ActualStartAt = &startedAt
	}
	if execution == taskdomain.ExecutionStatusBlocked {
		occurrence.BlockedReason = "dependency"
		occurrence.NextAction = "ask owner"
	}
	if execution == taskdomain.ExecutionStatusDone {
		completedAt := deletedAt.Add(-time.Minute)
		occurrence.CompletedAt = &completedAt
	}
	return DeleteEventInput{
		WorkspaceID: "workspace-1", LegacyEventID: "legacy-event-1",
		Task: taskdomain.TaskAggregate{
			WorkspaceID: "workspace-1", TaskID: "task-1", LifecycleStatus: lifecycle,
			Recurring: false, Revision: 5, Occurrences: []taskdomain.Occurrence{occurrence},
		},
		Occurrence: occurrence,
		IDMap: LegacyEventIDMap{
			WorkspaceID: "workspace-1", LegacyEventID: "legacy-event-1", TaskID: "task-1", OccurrenceID: "occurrence-1", Revision: 3,
		},
		CommandID: "delete-command-1", ActorID: "user-1", DeletedAt: deletedAt,
	}
}

func cloneLegacyDeleteInputForTest(input DeleteEventInput) DeleteEventInput {
	clone := input
	clone.Task.Occurrences = append([]taskdomain.Occurrence(nil), input.Task.Occurrences...)
	return clone
}
