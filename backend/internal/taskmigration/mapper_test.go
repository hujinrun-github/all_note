package taskmigration

import (
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/taskdomain"
)

func TestMapLegacySingleTaskKeepsDefinitionTimingDueAndCompletionSeparate(t *testing.T) {
	t.Parallel()

	due := mapperInstant("2026-07-23T08:00:00Z")
	updatedAt := mapperInstant("2026-07-22T12:00:00Z")
	content := strings.Repeat("完整迁移内容", 200)
	rows := validLegacyRows()
	rows.Tasks = []LegacyTaskRow{{
		ID: "task-orphan", ProjectID: "missing", ExecutionType: LegacyExecutionSingle,
		Title: "完成迁移", Content: content, Priority: 3, PlannedDate: "2026-07-22",
		DueAt: &due, Done: true, UpdatedAt: updatedAt, NoteID: "task-note-1", SortOrder: 17,
	}}
	preflight := validMapperPreflight()
	preflight.Tasks = []TaskDecision{{LegacyID: "task-orphan", TargetProjectID: "system-inbox"}}

	projection, err := MapLegacyTaskDomain(preflight, rows)
	if err != nil {
		t.Fatalf("MapLegacyTaskDomain() error = %v", err)
	}
	task := projectedTask(t, projection, "task-orphan")
	if task.ProjectID != "system-inbox" || task.Description != content || task.Priority != 3 || task.TaskNoteID != "task-note-1" || task.SortOrder != 17 {
		t.Fatalf("task projection = %#v", task)
	}
	if task.LifecycleStatus != taskdomain.TaskLifecycleCompleted {
		t.Fatalf("task lifecycle = %q", task.LifecycleStatus)
	}
	schedule := projectedSchedule(t, projection, "task-orphan")
	if schedule.RecurrenceType != taskdomain.RecurrenceNone || schedule.TimingType != taskdomain.TimingDate {
		t.Fatalf("single schedule = %#v", schedule)
	}
	occurrence := projectedOccurrenceByKey(t, projection, "task-orphan", "once")
	if occurrence.ExecutionStatus != taskdomain.ExecutionStatusDone || occurrence.PlannedDate != "2026-07-22" {
		t.Fatalf("single occurrence = %#v", occurrence)
	}
	if occurrence.DueAt == nil || !occurrence.DueAt.Equal(due) {
		t.Fatalf("due_at = %v, want %v", occurrence.DueAt, due)
	}
	if occurrence.CompletedAt == nil || !occurrence.CompletedAt.Equal(updatedAt) {
		t.Fatalf("completed_at = %v, want fallback %v", occurrence.CompletedAt, updatedAt)
	}
	if occurrence.PlannedStartAt != nil || occurrence.PlannedEndAt != nil {
		t.Fatalf("planned date was incorrectly converted to time block: %#v", occurrence)
	}
	if mapped := idMapEntry(t, projection, LegacyEntityTask, "task-orphan"); mapped.TargetScheduleID != "task-orphan" {
		t.Fatalf("single task schedule map = %#v", mapped)
	}
	assertUniqueIDMap(t, projection.IDMap)
}

func TestMapLegacyRecurringRuleAndMaterializedOccurrenceStatusArePreserved(t *testing.T) {
	t.Parallel()

	doneAt := mapperInstant("2026-07-15T10:00:00Z")
	rows := validLegacyRows()
	rows.Tasks = []LegacyTaskRow{{
		ID: "recurring", ProjectID: "project-1", ExecutionType: LegacyExecutionRecurring,
		Title: "每周复盘", Content: "不截断", Priority: 2,
	}}
	rows.Rules = []LegacyRuleRow{{
		ID: "rule-1", TaskID: "recurring", RecurrenceType: taskdomain.RecurrenceWeekly,
		TimingType: taskdomain.TimingTimeBlock, Timezone: "Asia/Shanghai",
		StartsOn: "2026-07-01", EndsOn: "2026-08-31", Interval: 2,
		Weekdays: []int{5, 1, 5}, LocalStartTime: "09:30:00", DurationMinutes: 45,
	}}
	rows.Occurrences = []LegacyOccurrenceRow{
		{TaskID: "recurring", OccurrenceDate: "2026-07-22", Status: taskdomain.ExecutionStatusOpen},
		{TaskID: "recurring", OccurrenceDate: "2026-07-15", Status: taskdomain.ExecutionStatusDone, CompletedAt: &doneAt},
		{TaskID: "recurring", OccurrenceDate: "2026-07-29", Status: taskdomain.ExecutionStatusSkipped},
	}
	preflight := validMapperPreflight()
	preflight.Tasks = []TaskDecision{{LegacyID: "recurring", TargetProjectID: "project-1"}}

	projection, err := MapLegacyTaskDomain(preflight, rows)
	if err != nil {
		t.Fatalf("MapLegacyTaskDomain() error = %v", err)
	}
	schedule := projectedSchedule(t, projection, "recurring")
	if schedule.RecurrenceType != taskdomain.RecurrenceWeekly || schedule.Interval != 2 || !reflect.DeepEqual(schedule.Weekdays, []int{1, 5}) {
		t.Fatalf("recurring schedule = %#v", schedule)
	}
	for key, status := range map[string]taskdomain.ExecutionStatus{
		"2026-07-15": taskdomain.ExecutionStatusDone,
		"2026-07-22": taskdomain.ExecutionStatusOpen,
		"2026-07-29": taskdomain.ExecutionStatusSkipped,
	} {
		if got := projectedOccurrenceByKey(t, projection, "recurring", key).ExecutionStatus; got != status {
			t.Fatalf("occurrence %s status = %q, want %q", key, got, status)
		}
	}
	if completed := projectedOccurrenceByKey(t, projection, "recurring", "2026-07-15").CompletedAt; completed == nil || !completed.Equal(doneAt) {
		t.Fatalf("done occurrence completed_at = %v", completed)
	}
	if mapped := idMapEntry(t, projection, LegacyEntityRule, "rule-1"); mapped.TargetTaskID != "recurring" || mapped.TargetScheduleID != "recurring" {
		t.Fatalf("recurrence rule schedule map = %#v", mapped)
	}
}

func TestMapLegacyEventsAtomicallyProjectsTimeBlockAndAllDayMetadata(t *testing.T) {
	t.Parallel()

	rows := validLegacyRows()
	rows.Events = []LegacyEventRow{
		{
			ID: "timed", ProjectID: "project-1", Title: "会议", Description: "讨论上线",
			StartAt: mapperInstant("2026-07-22T15:00:00Z"), EndAt: mapperInstant("2026-07-22T16:00:00Z"),
			Location: "会议室", Kind: "work", Notes: "带材料", NoteID: "event-note-1",
		},
		{
			ID: "all-day", Title: "出差", Description: "三天",
			StartAt: mapperInstant("2026-07-20T16:00:00Z"), EndAt: mapperInstant("2026-07-23T16:00:00Z"),
			AllDay: true, Location: "上海", Kind: "personal", Notes: "行程", NoteID: "event-note-2",
		},
	}

	projection, err := MapLegacyTaskDomain(validMapperPreflight(), rows)
	if err != nil {
		t.Fatalf("MapLegacyTaskDomain() error = %v", err)
	}
	timedMap := idMapEntry(t, projection, LegacyEntityEvent, "timed")
	timedTask := projectedTask(t, projection, timedMap.TargetTaskID)
	if timedTask.ProjectID != "project-1" || timedTask.Description != "讨论上线" {
		t.Fatalf("timed event task = %#v", timedTask)
	}
	timedSchedule := projectedSchedule(t, projection, timedMap.TargetTaskID)
	if timedMap.TargetScheduleID != timedMap.TargetTaskID {
		t.Fatalf("timed event schedule map = %#v", timedMap)
	}
	if timedSchedule.RecurrenceType != taskdomain.RecurrenceNone || timedSchedule.TimingType != taskdomain.TimingTimeBlock || timedSchedule.Timezone != "Asia/Shanghai" {
		t.Fatalf("timed event schedule = %#v", timedSchedule)
	}
	timedOccurrence := projectedOccurrenceByID(t, projection, timedMap.TargetOccurrenceID)
	if timedOccurrence.PlannedStartAt == nil || !timedOccurrence.PlannedStartAt.Equal(rows.Events[0].StartAt) || timedOccurrence.PlannedEndAt == nil || !timedOccurrence.PlannedEndAt.Equal(rows.Events[0].EndAt) {
		t.Fatalf("timed UTC instants changed: %#v", timedOccurrence)
	}
	if timedOccurrence.PlannedDate != "2026-07-22" || timedOccurrence.Location != "会议室" || timedOccurrence.CalendarKind != "work" || timedOccurrence.CalendarNotes != "带材料" || timedOccurrence.OccurrenceNoteID != "event-note-1" {
		t.Fatalf("timed metadata = %#v", timedOccurrence)
	}

	allDayMap := idMapEntry(t, projection, LegacyEntityEvent, "all-day")
	allDayTask := projectedTask(t, projection, allDayMap.TargetTaskID)
	if allDayTask.ProjectID != "personal" {
		t.Fatalf("projectless event target = %q, want personal", allDayTask.ProjectID)
	}
	allDaySchedule := projectedSchedule(t, projection, allDayMap.TargetTaskID)
	if allDaySchedule.TimingType != taskdomain.TimingDate {
		t.Fatalf("all-day schedule = %#v", allDaySchedule)
	}
	allDayOccurrence := projectedOccurrenceByID(t, projection, allDayMap.TargetOccurrenceID)
	if allDayOccurrence.PlannedDate != "2026-07-21" || allDayOccurrence.AllDayEndDate != "2026-07-24" {
		t.Fatalf("all-day local range = [%q,%q)", allDayOccurrence.PlannedDate, allDayOccurrence.AllDayEndDate)
	}
	if allDayOccurrence.PlannedStartAt != nil || allDayOccurrence.PlannedEndAt != nil {
		t.Fatalf("all-day event was projected as UTC time block: %#v", allDayOccurrence)
	}
	assertUniqueIDMap(t, projection.IDMap)
}

func TestMapLegacyTaskDomainIsRepeatableAndSorted(t *testing.T) {
	t.Parallel()

	rows := validLegacyRows()
	rows.Tasks = []LegacyTaskRow{
		{ID: "z-task", ProjectID: "project-1", ExecutionType: LegacyExecutionSingle, Title: "Z", Priority: 1},
		{ID: "a-task", ProjectID: "project-1", ExecutionType: LegacyExecutionSingle, Title: "A", Priority: 0},
	}
	preflight := validMapperPreflight()
	preflight.Tasks = []TaskDecision{
		{LegacyID: "z-task", TargetProjectID: "project-1"},
		{LegacyID: "a-task", TargetProjectID: "project-1"},
	}

	first, err := MapLegacyTaskDomain(preflight, rows)
	if err != nil {
		t.Fatalf("first mapping error = %v", err)
	}
	second, err := MapLegacyTaskDomain(preflight, rows)
	if err != nil {
		t.Fatalf("second mapping error = %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("rerun changed projection:\nfirst=%#v\nsecond=%#v", first, second)
	}
	assertProjectionSorted(t, first)
}

func TestMapLegacyTaskDomainBlocksConflictsWithoutPartialProjection(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		rows func() LegacyTaskDomainRows
		code MapperBlockCode
	}{
		{
			name: "duplicate task identity",
			rows: func() LegacyTaskDomainRows {
				rows := validLegacyRows()
				rows.Tasks = []LegacyTaskRow{{ID: "dup", ExecutionType: LegacyExecutionSingle}, {ID: "dup", ExecutionType: LegacyExecutionSingle}}
				return rows
			},
			code: MapperBlockDuplicateIdentity,
		},
		{
			name: "recurring task missing rule",
			rows: func() LegacyTaskDomainRows {
				rows := validLegacyRows()
				rows.Tasks = []LegacyTaskRow{{ID: "recurring", ProjectID: "project-1", ExecutionType: LegacyExecutionRecurring}}
				return rows
			},
			code: MapperBlockMissingRule,
		},
		{
			name: "recurrence rule missing stable identity",
			rows: func() LegacyTaskDomainRows {
				rows := validLegacyRows()
				rows.Tasks = []LegacyTaskRow{{ID: "recurring", ProjectID: "project-1", ExecutionType: LegacyExecutionRecurring}}
				rows.Rules = []LegacyRuleRow{{TaskID: "recurring", RecurrenceType: taskdomain.RecurrenceDaily}}
				return rows
			},
			code: MapperBlockInvalidIdentity,
		},
		{
			name: "done task lacks deterministic completion time",
			rows: func() LegacyTaskDomainRows {
				rows := validLegacyRows()
				rows.Tasks = []LegacyTaskRow{{ID: "done", ProjectID: "project-1", ExecutionType: LegacyExecutionSingle, Done: true}}
				return rows
			},
			code: MapperBlockMissingCompletedAt,
		},
		{
			name: "invalid event range",
			rows: func() LegacyTaskDomainRows {
				rows := validLegacyRows()
				at := mapperInstant("2026-07-22T00:00:00Z")
				rows.Events = []LegacyEventRow{{ID: "event", StartAt: at, EndAt: at}}
				return rows
			},
			code: MapperBlockInvalidEventRange,
		},
		{
			name: "project row missing preflight decision",
			rows: func() LegacyTaskDomainRows {
				rows := validLegacyRows()
				rows.Projects = append(rows.Projects, LegacyProjectRow{ID: "unmapped-project", Name: "遗漏项目", Type: LegacyProjectRegular})
				return rows
			},
			code: MapperBlockMissingPreflight,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := tc.rows()
			preflight := validMapperPreflight()
			for _, task := range rows.Tasks {
				preflight.Tasks = append(preflight.Tasks, TaskDecision{LegacyID: task.ID, TargetProjectID: "project-1"})
			}
			projection, err := MapLegacyTaskDomain(preflight, rows)
			if !reflect.DeepEqual(projection, V2Projection{}) {
				t.Fatalf("blocked mapping returned partial projection: %#v", projection)
			}
			block, ok := err.(*MapperBlock)
			if !ok || block.Code != tc.code {
				t.Fatalf("error = %T(%v), want mapper block %q", err, err, tc.code)
			}
		})
	}
}

func validMapperPreflight() PreflightResult {
	return PreflightResult{
		MigrationTimezone: "Asia/Shanghai",
		TimezoneSource:    TimezoneSourceWorkspace,
		Projects: []ProjectDecision{
			{LegacyID: "personal", TargetID: "personal", Name: "个人", Kind: "standard", Horizon: "short", SystemRole: "personal"},
			{LegacyID: "project-1", TargetID: "project-1", Name: "项目", Kind: "standard", Horizon: "long"},
			{TargetID: "system-inbox", Name: "收件箱", Kind: "standard", Horizon: "short", SystemRole: "inbox", Generated: true},
		},
	}
}

func validLegacyRows() LegacyTaskDomainRows {
	return LegacyTaskDomainRows{
		Projects: []LegacyProjectRow{
			{ID: "personal", Name: "个人", Type: LegacyProjectPersonal},
			{ID: "project-1", Name: "项目", Type: LegacyProjectRegular},
		},
	}
}

func projectedTask(t *testing.T, projection V2Projection, id string) V2TaskProjection {
	t.Helper()
	for _, task := range projection.Tasks {
		if task.ID == id {
			return task
		}
	}
	t.Fatalf("projected task %q not found", id)
	return V2TaskProjection{}
}

func projectedSchedule(t *testing.T, projection V2Projection, taskID string) V2ScheduleProjection {
	t.Helper()
	for _, schedule := range projection.Schedules {
		if schedule.TaskID == taskID {
			return schedule
		}
	}
	t.Fatalf("projected schedule for task %q not found", taskID)
	return V2ScheduleProjection{}
}

func projectedOccurrenceByKey(t *testing.T, projection V2Projection, taskID, key string) V2OccurrenceProjection {
	t.Helper()
	for _, occurrence := range projection.Occurrences {
		if occurrence.TaskID == taskID && occurrence.OccurrenceKey == key {
			return occurrence
		}
	}
	t.Fatalf("projected occurrence (%q,%q) not found", taskID, key)
	return V2OccurrenceProjection{}
}

func projectedOccurrenceByID(t *testing.T, projection V2Projection, id string) V2OccurrenceProjection {
	t.Helper()
	for _, occurrence := range projection.Occurrences {
		if occurrence.ID == id {
			return occurrence
		}
	}
	t.Fatalf("projected occurrence %q not found", id)
	return V2OccurrenceProjection{}
}

func idMapEntry(t *testing.T, projection V2Projection, kind LegacyEntityKind, legacyID string) V2IDMapEntry {
	t.Helper()
	for _, entry := range projection.IDMap {
		if entry.LegacyKind == kind && entry.LegacyID == legacyID {
			return entry
		}
	}
	t.Fatalf("ID map (%q,%q) not found", kind, legacyID)
	return V2IDMapEntry{}
}

func assertUniqueIDMap(t *testing.T, entries []V2IDMapEntry) {
	t.Helper()
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		key := string(entry.LegacyKind) + "\x00" + entry.LegacyID
		if _, duplicate := seen[key]; duplicate {
			t.Fatalf("duplicate ID map key %q", key)
		}
		seen[key] = struct{}{}
	}
}

func assertProjectionSorted(t *testing.T, projection V2Projection) {
	t.Helper()
	assertSorted := func(name string, values []string) {
		t.Helper()
		if !sort.StringsAreSorted(values) {
			t.Fatalf("%s not sorted: %#v", name, values)
		}
	}
	projects := make([]string, len(projection.Projects))
	for index, project := range projection.Projects {
		projects[index] = project.ID
	}
	tasks := make([]string, len(projection.Tasks))
	for index, task := range projection.Tasks {
		tasks[index] = task.ID
	}
	schedules := make([]string, len(projection.Schedules))
	for index, schedule := range projection.Schedules {
		schedules[index] = schedule.TaskID
	}
	occurrences := make([]string, len(projection.Occurrences))
	for index, occurrence := range projection.Occurrences {
		occurrences[index] = occurrence.ID
	}
	idMap := make([]string, len(projection.IDMap))
	for index, entry := range projection.IDMap {
		idMap[index] = string(entry.LegacyKind) + "\x00" + entry.LegacyID
	}
	assertSorted("projects", projects)
	assertSorted("tasks", tasks)
	assertSorted("schedules", schedules)
	assertSorted("occurrences", occurrences)
	assertSorted("ID map", idMap)
}

func mapperInstant(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return parsed
}
