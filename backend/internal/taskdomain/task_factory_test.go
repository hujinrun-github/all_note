package taskdomain

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"
)

func TestBuildTaskAggregateSnapshotCreatesUnscheduledOnceAggregate(t *testing.T) {
	input := baseTaskCreationInput()
	input.Schedule = ScheduleInput{RecurrenceType: RecurrenceNone, TimingType: TimingUnscheduled, Timezone: "UTC"}

	snapshot, details, err := BuildTaskAggregateSnapshot(input)
	if err != nil {
		t.Fatalf("BuildTaskAggregateSnapshot() error = %v", err)
	}
	if snapshot.Task.WorkspaceID != "workspace-1" || snapshot.Task.ID != "task-1" || snapshot.Task.ProjectID != "project-1" ||
		snapshot.Task.RoadmapNodeID != "roadmap-node-1" || snapshot.Task.NoteID != "note-1" ||
		snapshot.Task.Title != "Task title" || snapshot.Task.Description != "Full description" ||
		snapshot.Task.Priority != 2 || snapshot.Task.SortOrder != 17.5 || snapshot.Task.LifecycleStatus != TaskLifecycleDraft || snapshot.Task.Revision != 1 {
		t.Fatalf("task = %#v", snapshot.Task)
	}
	if snapshot.Schedule.Revision != 1 || snapshot.Schedule.CurrentScheduleRevision != 1 || len(snapshot.Versions) != 1 {
		t.Fatalf("schedule/version = %#v / %#v", snapshot.Schedule, snapshot.Versions)
	}
	version := snapshot.Versions[0]
	if version.ScheduleRevision != 1 || version.RecurrenceType != RecurrenceNone || version.TimingType != TimingUnscheduled || version.EffectiveFrom != "" {
		t.Fatalf("version = %#v", version)
	}
	if len(snapshot.Occurrences) != 1 {
		t.Fatalf("occurrences = %#v", snapshot.Occurrences)
	}
	once := snapshot.Occurrences[0]
	if once.OccurrenceKey != "once" || once.ID != DeterministicOccurrenceID("workspace-1", "task-1", "once") ||
		once.ExecutionStatus != ExecutionStatusOpen || once.Revision != 1 || once.GeneratedScheduleRevision != 1 ||
		once.PlannedDate != "" || once.PlannedStartAt != nil || once.PlannedEndAt != nil || once.AllDayEndDate != "" {
		t.Fatalf("once occurrence = %#v", once)
	}
	if details.EffectiveFrom != "" || details.GenerateThrough != "" || len(details.OffsetCandidates) != 0 {
		t.Fatalf("details = %#v", details)
	}
	if err := ValidateTaskAggregateSnapshot(snapshot); err != nil {
		t.Fatalf("factory produced invalid persistence snapshot: %v", err)
	}
}

func TestBuildTaskAggregateSnapshotMaterializesDateAllDayAndIndependentDue(t *testing.T) {
	input := baseTaskCreationInput()
	input.Schedule = ScheduleInput{
		RecurrenceType: RecurrenceNone, TimingType: TimingDate, Timezone: "Asia/Shanghai", StartsOn: "2026-07-25",
	}
	input.AllDayEndDate = "2026-07-28"
	due := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	input.DueAt = &due

	snapshot, _, err := BuildTaskAggregateSnapshot(input)
	if err != nil {
		t.Fatalf("BuildTaskAggregateSnapshot() error = %v", err)
	}
	once := snapshot.Occurrences[0]
	if once.PlannedDate != "2026-07-25" || once.AllDayEndDate != "2026-07-28" || once.PlannedStartAt != nil || once.PlannedEndAt != nil {
		t.Fatalf("all-day occurrence = %#v", once)
	}
	if once.DueAt == nil || !once.DueAt.Equal(due) {
		t.Fatalf("due = %v, want %v", once.DueAt, due)
	}
	due = due.Add(48 * time.Hour)
	if once.DueAt.Equal(due) {
		t.Fatal("factory retained caller's due pointer")
	}

	input.AllDayEndDate = ""
	defaultDay, _, err := BuildTaskAggregateSnapshot(input)
	if err != nil {
		t.Fatalf("default all-day range error = %v", err)
	}
	if got := defaultDay.Occurrences[0].AllDayEndDate; got != "2026-07-26" {
		t.Fatalf("default exclusive end = %q, want 2026-07-26", got)
	}
}

func TestBuildTaskAggregateSnapshotReturnsStructuredDSTCandidates(t *testing.T) {
	input := baseTaskCreationInput()
	input.Schedule = ScheduleInput{
		RecurrenceType: RecurrenceNone, TimingType: TimingTimeBlock, Timezone: "America/New_York",
		StartsOn: "2026-11-01", LocalStartTime: "01:30", DurationMinutes: 60,
	}

	snapshot, details, err := BuildTaskAggregateSnapshot(input)
	if !errors.Is(err, ErrAmbiguousLocalTime) || !reflect.DeepEqual(snapshot, TaskAggregateSnapshot{}) {
		t.Fatalf("ambiguous snapshot/error = %#v / %v", snapshot, err)
	}
	if len(details.OffsetCandidates) != 1 || details.OffsetCandidates[0].OccurrenceKey != "once" || len(details.OffsetCandidates[0].Candidates) != 2 {
		t.Fatalf("DST details = %#v", details)
	}
	selected := details.OffsetCandidates[0].Candidates[1]
	input.SelectedOffsets = map[string]int{"2026-11-01": selected.OffsetSeconds}
	snapshot, details, err = TaskFactory{}.Build(input)
	if err != nil {
		t.Fatalf("selected offset build error = %v", err)
	}
	once := snapshot.Occurrences[0]
	if once.PlannedStartAt == nil || !once.PlannedStartAt.Equal(selected.UTC) || once.PlannedEndAt == nil || !once.PlannedEndAt.Equal(selected.UTC.Add(time.Hour)) {
		t.Fatalf("selected time block = %#v, candidate=%#v", once, selected)
	}
	if len(details.OffsetCandidates) != 1 || len(details.OffsetCandidates[0].Candidates) != 2 {
		t.Fatalf("successful DST candidate details = %#v", details)
	}
}

func TestBuildTaskAggregateSnapshotGeneratesRecurringLocalTodayThroughPlus90(t *testing.T) {
	input := baseTaskCreationInput()
	input.ActorTime = time.Date(2026, 7, 22, 17, 30, 0, 0, time.UTC) // 2026-07-23 in Shanghai.
	input.Schedule = ScheduleInput{
		RecurrenceType: RecurrenceDaily, TimingType: TimingDate, Timezone: "Asia/Shanghai",
		StartsOn: "2026-07-23", Rule: json.RawMessage(`{"interval":1}`),
	}

	first, firstDetails, err := BuildTaskAggregateSnapshot(input)
	if err != nil {
		t.Fatalf("BuildTaskAggregateSnapshot() error = %v", err)
	}
	second, secondDetails, err := BuildTaskAggregateSnapshot(input)
	if err != nil {
		t.Fatalf("second BuildTaskAggregateSnapshot() error = %v", err)
	}
	if !reflect.DeepEqual(first, second) || !reflect.DeepEqual(firstDetails, secondDetails) {
		t.Fatal("same input did not produce a deep-equal result")
	}
	if firstDetails.EffectiveFrom != "2026-07-23" || firstDetails.GenerateThrough != "2026-10-21" {
		t.Fatalf("initial window details = %#v", firstDetails)
	}
	if len(first.Versions) != 1 || first.Versions[0].EffectiveFrom != "2026-07-23" {
		t.Fatalf("recurring version = %#v", first.Versions)
	}
	if len(first.Occurrences) != 91 || first.Occurrences[0].OccurrenceKey != "2026-07-23" || first.Occurrences[90].OccurrenceKey != "2026-10-21" {
		t.Fatalf("recurring occurrences count/edges = %d / %q / %q", len(first.Occurrences), first.Occurrences[0].OccurrenceKey, first.Occurrences[len(first.Occurrences)-1].OccurrenceKey)
	}
	for _, occurrence := range first.Occurrences {
		if occurrence.ID != DeterministicOccurrenceID(input.WorkspaceID, input.TaskID, occurrence.OccurrenceKey) ||
			occurrence.PlannedDate != occurrence.OccurrenceKey || occurrence.GeneratedScheduleRevision != 1 || occurrence.Revision != 1 {
			t.Fatalf("recurring occurrence = %#v", occurrence)
		}
	}
	if err := ValidateTaskAggregateSnapshot(first); err != nil {
		t.Fatalf("recurring factory snapshot invalid: %v", err)
	}
}

func TestBuildTaskAggregateSnapshotDoesNotInventFarFutureRecurringOccurrence(t *testing.T) {
	input := baseTaskCreationInput()
	input.ActorTime = time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	input.Schedule = ScheduleInput{
		RecurrenceType: RecurrenceWeekly, TimingType: TimingDate, Timezone: "UTC",
		StartsOn: "2027-01-04", Rule: json.RawMessage(`{"interval":1,"weekdays":[1]}`),
	}

	snapshot, details, err := BuildTaskAggregateSnapshot(input)
	if err != nil {
		t.Fatalf("BuildTaskAggregateSnapshot() error = %v", err)
	}
	if len(snapshot.Occurrences) != 0 {
		t.Fatalf("factory invented far-future occurrences: %#v", snapshot.Occurrences)
	}
	if details.EffectiveFrom != "2026-07-22" || details.GenerateThrough != "2026-10-20" {
		t.Fatalf("window details = %#v", details)
	}
	if err := ValidateTaskAggregateSnapshot(snapshot); err != nil {
		t.Fatalf("recurring zero-occurrence snapshot should be valid: %v", err)
	}
}

func TestBuildTaskAggregateSnapshotRejectsInvalidCreationIdentityAndFields(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*TaskCreationInput)
	}{
		{name: "workspace", mutate: func(input *TaskCreationInput) { input.WorkspaceID = "" }},
		{name: "project id", mutate: func(input *TaskCreationInput) { input.Project.ProjectID = "" }},
		{name: "project workspace", mutate: func(input *TaskCreationInput) { input.Project.WorkspaceID = "other" }},
		{name: "roadmap id", mutate: func(input *TaskCreationInput) { input.Roadmap.ID = "" }},
		{name: "roadmap workspace", mutate: func(input *TaskCreationInput) { input.Roadmap.WorkspaceID = "other" }},
		{name: "roadmap project", mutate: func(input *TaskCreationInput) { input.Roadmap.ProjectID = "other" }},
		{name: "note id", mutate: func(input *TaskCreationInput) { input.TaskNote.NoteID = "" }},
		{name: "note workspace", mutate: func(input *TaskCreationInput) { input.TaskNote.WorkspaceID = "other" }},
		{name: "task id", mutate: func(input *TaskCreationInput) { input.TaskID = "" }},
		{name: "actor", mutate: func(input *TaskCreationInput) { input.ActorID = "" }},
		{name: "actor time", mutate: func(input *TaskCreationInput) { input.ActorTime = time.Time{} }},
		{name: "title", mutate: func(input *TaskCreationInput) { input.Title = "  " }},
		{name: "priority low", mutate: func(input *TaskCreationInput) { input.Priority = -1 }},
		{name: "priority high", mutate: func(input *TaskCreationInput) { input.Priority = 4 }},
		{name: "sort NaN", mutate: func(input *TaskCreationInput) { input.SortOrder = math.NaN() }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := baseTaskCreationInput()
			input.Schedule = ScheduleInput{RecurrenceType: RecurrenceNone, TimingType: TimingUnscheduled, Timezone: "UTC"}
			tt.mutate(&input)
			snapshot, _, err := BuildTaskAggregateSnapshot(input)
			if !errors.Is(err, ErrInvalidTaskCreation) || !reflect.DeepEqual(snapshot, TaskAggregateSnapshot{}) {
				t.Fatalf("snapshot/error = %#v / %v", snapshot, err)
			}
		})
	}
}

func TestBuildTaskAggregateSnapshotRejectsAllDayAndDueShapeWithoutPartialSnapshot(t *testing.T) {
	input := baseTaskCreationInput()
	input.Schedule = ScheduleInput{RecurrenceType: RecurrenceNone, TimingType: TimingTimeBlock, Timezone: "UTC", StartsOn: "2026-07-23", LocalStartTime: "09:00", DurationMinutes: 30}
	input.AllDayEndDate = "2026-07-24"
	snapshot, _, err := BuildTaskAggregateSnapshot(input)
	if !errors.Is(err, ErrInvalidTaskCreation) || !reflect.DeepEqual(snapshot, TaskAggregateSnapshot{}) {
		t.Fatalf("time-block all-day end snapshot/error = %#v / %v", snapshot, err)
	}

	input = baseTaskCreationInput()
	input.Schedule = ScheduleInput{RecurrenceType: RecurrenceDaily, TimingType: TimingDate, Timezone: "UTC", StartsOn: "2026-07-22", Rule: json.RawMessage(`{"interval":1}`)}
	input.AllDayEndDate = "2026-07-24"
	snapshot, _, err = BuildTaskAggregateSnapshot(input)
	if !errors.Is(err, ErrInvalidTaskCreation) || !reflect.DeepEqual(snapshot, TaskAggregateSnapshot{}) {
		t.Fatalf("recurring all-day end snapshot/error = %#v / %v", snapshot, err)
	}
}

func baseTaskCreationInput() TaskCreationInput {
	return TaskCreationInput{
		WorkspaceID: "workspace-1",
		Project:     ProjectIdentity{WorkspaceID: "workspace-1", ProjectID: "project-1"},
		Roadmap:     &Roadmap{WorkspaceID: "workspace-1", ID: "roadmap-node-1", ProjectID: "project-1"},
		TaskNote:    &TaskNoteIdentity{WorkspaceID: "workspace-1", NoteID: "note-1"},
		TaskID:      "task-1",
		ActorID:     "user-1",
		ActorTime:   time.Date(2026, 7, 22, 8, 0, 0, 0, time.UTC),
		Title:       "Task title",
		Description: "Full description",
		Priority:    2,
		SortOrder:   17.5,
	}
}
