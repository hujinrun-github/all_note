package taskdomain

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestBuildCalendarProjectionSeparatesTimeBlocksAllDayAndUnscheduled(t *testing.T) {
	start := time.Date(2026, 7, 22, 1, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	due := time.Date(2026, 7, 22, 8, 0, 0, 0, time.UTC)
	snapshots := []QueryOccurrenceSnapshot{
		{
			WorkspaceID: "workspace-1", ProjectID: "project-1", TaskID: "task-time", OccurrenceID: "occ-time",
			Title: "语音服务评审", TimingType: TimingTimeBlock, Timezone: "Asia/Shanghai",
			PlannedDate: "2026-07-22", PlannedStartAt: &start, PlannedEndAt: &end, DueAt: &due,
			Status: ExecutionStatusActive, Recurring: true, Revision: 7,
			Location: "会议室 A", CalendarKind: "work", CalendarNotes: "评审接入方案",
			TaskNoteID: "task-note-1", OccurrenceNoteID: "occ-note-1",
		},
		{
			WorkspaceID: "workspace-1", ProjectID: "project-2", TaskID: "task-date", OccurrenceID: "occ-date",
			Title: "发布窗口", TimingType: TimingDate, Timezone: "Asia/Shanghai", PlannedDate: "2026-07-23",
			AllDayEndDate: "2026-07-25", Status: ExecutionStatusOpen, Revision: 3,
		},
		{
			WorkspaceID: "workspace-1", ProjectID: "project-3", TaskID: "task-unscheduled", OccurrenceID: "occ-unscheduled",
			Title: "整理想法", TimingType: TimingUnscheduled, Timezone: "Asia/Shanghai", Status: ExecutionStatusOpen, Revision: 1,
		},
	}

	projection, err := BuildCalendarProjection(snapshots)
	if err != nil {
		t.Fatalf("BuildCalendarProjection() unexpected error: %v", err)
	}
	if len(projection.TimeBlocks) != 1 || len(projection.AllDay) != 1 {
		t.Fatalf("calendar lanes = time_blocks:%d all_day:%d, want 1 and 1", len(projection.TimeBlocks), len(projection.AllDay))
	}

	entry := projection.TimeBlocks[0]
	if entry.DisplayType != TimingTimeBlock || entry.WorkspaceID != "workspace-1" || entry.ProjectID != "project-1" || entry.TaskID != "task-time" || entry.OccurrenceID != "occ-time" {
		t.Fatalf("time block identity/display = %#v", entry)
	}
	if entry.Revision != 7 || !entry.Recurring || entry.Status != ExecutionStatusActive {
		t.Fatalf("time block execution metadata = %#v", entry)
	}
	if entry.StartAt == nil || !entry.StartAt.Equal(start) || entry.EndAt == nil || !entry.EndAt.Equal(end) {
		t.Fatalf("time block instants = (%v, %v)", entry.StartAt, entry.EndAt)
	}
	if entry.DueAt == nil || !entry.DueAt.Equal(due) {
		t.Fatalf("planned time discarded independent due_at: %v", entry.DueAt)
	}
	if entry.Location != "会议室 A" || entry.CalendarKind != "work" || entry.CalendarNotes != "评审接入方案" || entry.TaskNoteID != "task-note-1" || entry.OccurrenceNoteID != "occ-note-1" {
		t.Fatalf("calendar metadata was not preserved: %#v", entry)
	}

	allDay := projection.AllDay[0]
	if allDay.DisplayType != TimingDate || allDay.PlannedDate != "2026-07-23" || allDay.AllDayEndDate != "2026-07-25" {
		t.Fatalf("all-day projection = %#v", allDay)
	}
	for _, lane := range [][]CalendarEntry{projection.TimeBlocks, projection.AllDay} {
		for _, calendarEntry := range lane {
			if calendarEntry.OccurrenceID == "occ-unscheduled" {
				t.Fatal("unscheduled occurrence entered calendar")
			}
		}
	}
}

func TestBuildCalendarProjectionHasStableLaneOrdering(t *testing.T) {
	late := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	early := time.Date(2026, 7, 22, 8, 0, 0, 0, time.UTC)
	lateEnd := late.Add(time.Hour)
	earlyEnd := early.Add(time.Hour)
	projection, err := BuildCalendarProjection([]QueryOccurrenceSnapshot{
		queryTimeBlock("late", late, lateEnd),
		queryDate("later-date", "2026-07-24"),
		queryTimeBlock("early", early, earlyEnd),
		queryDate("earlier-date", "2026-07-23"),
	})
	if err != nil {
		t.Fatalf("BuildCalendarProjection() unexpected error: %v", err)
	}
	if got, want := calendarOccurrenceIDs(projection.TimeBlocks), []string{"early", "late"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("time block order = %v, want %v", got, want)
	}
	if got, want := calendarOccurrenceIDs(projection.AllDay), []string{"earlier-date", "later-date"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("all-day order = %v, want %v", got, want)
	}
}

func TestBuildTodayProjectionKeepsDefaultAndOverdueTabsSeparate(t *testing.T) {
	now := time.Date(2026, 7, 22, 2, 0, 0, 0, time.UTC) // 10:00 Asia/Shanghai
	todayStart := time.Date(2026, 7, 22, 1, 0, 0, 0, time.UTC)
	todayEnd := todayStart.Add(time.Hour)
	pastDue := now.Add(-time.Minute)
	futureDue := now.Add(time.Hour)
	oldDue := now.Add(-24 * time.Hour)

	snapshots := []QueryOccurrenceSnapshot{
		queryTimeBlock("today-time", todayStart, todayEnd),
		queryDate("today-date", "2026-07-22"),
		{
			WorkspaceID: "workspace-1", ProjectID: "project-1", TaskID: "task-marked", OccurrenceID: "marked-unscheduled",
			Title: "今天处理", TimingType: TimingUnscheduled, Timezone: "Asia/Shanghai", MarkedForToday: true,
			DueAt: &futureDue, Status: ExecutionStatusOpen, Revision: 1,
		},
		{
			WorkspaceID: "workspace-1", ProjectID: "project-1", TaskID: "task-overdue", OccurrenceID: "overdue",
			Title: "已逾期", TimingType: TimingDate, Timezone: "Asia/Shanghai", PlannedDate: "2026-07-21",
			DueAt: &oldDue, Status: ExecutionStatusBlocked, Revision: 2,
		},
		{
			WorkspaceID: "workspace-1", ProjectID: "project-1", TaskID: "task-overdue-today", OccurrenceID: "overdue-today",
			Title: "今天但已逾期", TimingType: TimingDate, Timezone: "Asia/Shanghai", PlannedDate: "2026-07-22",
			DueAt: &pastDue, Status: ExecutionStatusActive, Revision: 3,
		},
		{
			WorkspaceID: "workspace-1", ProjectID: "project-1", TaskID: "task-terminal", OccurrenceID: "terminal-old-due",
			Title: "已完成", TimingType: TimingDate, Timezone: "Asia/Shanghai", PlannedDate: "2026-07-22",
			DueAt: &oldDue, Status: ExecutionStatusDone, Revision: 4,
		},
		queryDate("future", "2026-07-23"),
	}

	projection, err := BuildTodayProjection(snapshots, now, "Asia/Shanghai")
	if err != nil {
		t.Fatalf("BuildTodayProjection() unexpected error: %v", err)
	}
	if got, want := taskListOccurrenceIDs(projection.Default), []string{"today-time", "today-date", "marked-unscheduled", "terminal-old-due"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("today default IDs = %v, want %v", got, want)
	}
	if got, want := taskListOccurrenceIDs(projection.Overdue), []string{"overdue", "overdue-today"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("overdue IDs = %v, want %v", got, want)
	}
}

func TestBuildTodayProjectionIncludesOngoingMultiDayAllDayOccurrence(t *testing.T) {
	now := time.Date(2026, 7, 22, 2, 0, 0, 0, time.UTC)
	snapshot := queryDate("multi-day", "2026-07-21")
	snapshot.AllDayEndDate = "2026-07-24"

	projection, err := BuildTodayProjection([]QueryOccurrenceSnapshot{snapshot}, now, "Asia/Shanghai")
	if err != nil {
		t.Fatalf("BuildTodayProjection() unexpected error: %v", err)
	}
	if got := taskListOccurrenceIDs(projection.Default); !reflect.DeepEqual(got, []string{"multi-day"}) {
		t.Fatalf("today multi-day IDs = %v", got)
	}
}

func TestBuildTaskListPreservesEveryExecutionStatus(t *testing.T) {
	statuses := []ExecutionStatus{
		ExecutionStatusOpen,
		ExecutionStatusActive,
		ExecutionStatusBlocked,
		ExecutionStatusDone,
		ExecutionStatusSkipped,
		ExecutionStatusCancelled,
	}
	snapshots := make([]QueryOccurrenceSnapshot, 0, len(statuses))
	for _, status := range statuses {
		snapshots = append(snapshots, QueryOccurrenceSnapshot{
			WorkspaceID: "workspace-1", ProjectID: "project-1", TaskID: "task-" + string(status), OccurrenceID: "occ-" + string(status),
			Title: string(status), TimingType: TimingUnscheduled, Timezone: "UTC", Status: status, Revision: 1,
		})
	}

	items := BuildTaskList(snapshots)
	if len(items) != len(statuses) {
		t.Fatalf("task list length = %d, want %d", len(items), len(statuses))
	}
	for index, status := range statuses {
		if items[index].Status != status || items[index].OccurrenceID != "occ-"+string(status) {
			t.Fatalf("task list item %d = %#v, want status %q", index, items[index], status)
		}
	}
}

func TestQueryProjectionRejectsInvalidCalendarSnapshotsAndTimezone(t *testing.T) {
	start := time.Date(2026, 7, 22, 1, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	tests := []struct {
		name     string
		snapshot QueryOccurrenceSnapshot
	}{
		{name: "time block missing end", snapshot: QueryOccurrenceSnapshot{TimingType: TimingTimeBlock, Timezone: "UTC", PlannedDate: "2026-07-22", PlannedStartAt: &start}},
		{name: "time block end does not follow start", snapshot: QueryOccurrenceSnapshot{TimingType: TimingTimeBlock, Timezone: "UTC", PlannedDate: "2026-07-22", PlannedStartAt: &end, PlannedEndAt: &start}},
		{name: "date missing planned date", snapshot: QueryOccurrenceSnapshot{TimingType: TimingDate, Timezone: "UTC"}},
		{name: "date has time block", snapshot: QueryOccurrenceSnapshot{TimingType: TimingDate, Timezone: "UTC", PlannedDate: "2026-07-22", PlannedStartAt: &start, PlannedEndAt: &end}},
		{name: "all-day exclusive end is invalid", snapshot: QueryOccurrenceSnapshot{TimingType: TimingDate, Timezone: "UTC", PlannedDate: "2026-07-22", AllDayEndDate: "2026-07-22"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := BuildCalendarProjection([]QueryOccurrenceSnapshot{tt.snapshot})
			if !errors.Is(err, ErrInvalidSchedule) {
				t.Fatalf("BuildCalendarProjection() error = %v, want %v", err, ErrInvalidSchedule)
			}
		})
	}

	_, err := BuildTodayProjection(nil, time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC), "Local")
	if !errors.Is(err, ErrInvalidTimezone) {
		t.Fatalf("BuildTodayProjection() timezone error = %v, want %v", err, ErrInvalidTimezone)
	}
}

func queryTimeBlock(id string, start, end time.Time) QueryOccurrenceSnapshot {
	return QueryOccurrenceSnapshot{
		WorkspaceID: "workspace-1", ProjectID: "project-1", TaskID: "task-" + id, OccurrenceID: id,
		Title: id, TimingType: TimingTimeBlock, Timezone: "Asia/Shanghai",
		PlannedDate: start.In(time.FixedZone("UTC+8", 8*60*60)).Format("2006-01-02"), PlannedStartAt: &start, PlannedEndAt: &end,
		Status: ExecutionStatusOpen, Revision: 1,
	}
}

func queryDate(id, date string) QueryOccurrenceSnapshot {
	return QueryOccurrenceSnapshot{
		WorkspaceID: "workspace-1", ProjectID: "project-1", TaskID: "task-" + id, OccurrenceID: id,
		Title: id, TimingType: TimingDate, Timezone: "Asia/Shanghai", PlannedDate: date,
		Status: ExecutionStatusOpen, Revision: 1,
	}
}

func calendarOccurrenceIDs(entries []CalendarEntry) []string {
	ids := make([]string, len(entries))
	for index, entry := range entries {
		ids[index] = entry.OccurrenceID
	}
	return ids
}

func taskListOccurrenceIDs(entries []TaskListItem) []string {
	ids := make([]string, len(entries))
	for index, entry := range entries {
		ids[index] = entry.OccurrenceID
	}
	return ids
}
