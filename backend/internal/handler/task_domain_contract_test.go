package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/taskdomain"
)

func TestProjectMutationContractsMatchFrontendAndStrictlyRejectIdentityOrTerminalPatch(t *testing.T) {
	t.Parallel()

	var create CreateProjectV2Request
	decodeTaskDomainJSON(t, `{"name":"Launch","kind":"standard","horizon":"short","status":"planning"}`, &create)
	if create.Name != "Launch" || create.Kind != taskdomain.ProjectKindStandard || create.Horizon != taskdomain.ProjectHorizonShort || create.Status != taskdomain.ProjectStatusPlanning {
		t.Fatalf("create project request = %#v", create)
	}

	var update UpdateProjectV2Request
	decodeTaskDomainJSON(t, `{"name":"Launch v2","status":"paused","expected_project_revision":7}`, &update)
	if update.Name == nil || *update.Name != "Launch v2" || update.Status == nil || *update.Status != taskdomain.ProjectStatusPaused || update.ExpectedProjectRevision != 7 {
		t.Fatalf("update project request = %#v", update)
	}

	for _, payload := range []string{
		`{"expected_project_revision":7,"status":"completed"}`,
		`{"expected_project_revision":7,"status":"archived"}`,
		`{"expected_project_revision":7,"system_role":"inbox"}`,
		`{"expected_project_revision":7,"id":"project-2"}`,
		`{"expected_project_revision":7,"workspace_id":"workspace-2"}`,
	} {
		var request UpdateProjectV2Request
		err := DecodeTaskDomainRequest(strings.NewReader(payload), &request)
		if !errors.Is(err, ErrInvalidTaskDomainRequest) {
			t.Fatalf("DecodeTaskDomainRequest(%s) error = %v, want %v", payload, err, ErrInvalidTaskDomainRequest)
		}
		mapped := MapTaskDomainError(err)
		if mapped.Status != http.StatusBadRequest || mapped.Response.Error.Code != "invalid_request" {
			t.Fatalf("mapped strict project error = %#v", mapped)
		}
	}

	commandWire := marshalObject(t, ProjectCommandV2Request{ExpectedProjectRevision: 7})
	deleteWire := marshalObject(t, DeleteProjectV2Request{ExpectedProjectRevision: 7})
	for _, wire := range []map[string]any{commandWire, deleteWire} {
		assertJSONNumber(t, wire, "expected_project_revision", 7)
	}
	responseWire := marshalObject(t, ProjectCommandV2Response{
		ProjectID: "project-1", ProjectRevision: 8, Status: taskdomain.ProjectStatusCompleted, Deleted: true,
	})
	assertJSONValue(t, responseWire, "project_id", "project-1")
	assertJSONNumber(t, responseWire, "project_revision", 8)
	assertJSONValue(t, responseWire, "status", "completed")
	assertJSONValue(t, responseWire, "deleted", true)
}

func TestCreateTaskTransportCarriesTaskFactoryTimingInputsAndRejectsUnknownDomainFields(t *testing.T) {
	t.Parallel()

	const payload = `{
		"project_id":"project-1",
		"title":"Review",
		"priority":1,
		"all_day_end_date":"2026-07-25",
		"due_at":"2026-07-24T09:00:00Z",
		"selected_offsets":{"2026-11-01":-14400},
		"schedule":{"recurrence_type":"none","timing_type":"date","timezone":"UTC","starts_on":"2026-07-24"}
	}`
	var request CreateTaskV2Request
	decodeTaskDomainJSON(t, payload, &request)
	if request.AllDayEndDate != "2026-07-25" || request.DueAt == nil || request.DueAt.Format(time.RFC3339) != "2026-07-24T09:00:00Z" || request.SelectedOffsets["2026-11-01"] != -14400 {
		t.Fatalf("create task timing fields = %#v", request)
	}

	for name, invalid := range map[string]string{
		"lifecycle status": `{"project_id":"project-1","title":"Review","priority":1,"lifecycle_status":"active","schedule":{"recurrence_type":"none","timing_type":"unscheduled","timezone":"UTC"}}`,
		"schedule field":   `{"project_id":"project-1","title":"Review","priority":1,"schedule":{"recurrence_type":"none","timing_type":"unscheduled","timezone":"UTC","execution_status":"open"}}`,
		"schedule enum":    `{"project_id":"project-1","title":"Review","priority":1,"schedule":{"recurrence_type":"hourly","timing_type":"unscheduled","timezone":"UTC"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			var got CreateTaskV2Request
			if err := DecodeTaskDomainRequest(strings.NewReader(invalid), &got); !errors.Is(err, ErrInvalidTaskDomainRequest) {
				t.Fatalf("DecodeTaskDomainRequest() error = %v, want %v", err, ErrInvalidTaskDomainRequest)
			}
		})
	}
}

func TestOccurrenceAndScheduleCommandContractsUseIndependentRevisionsAndStrictDecode(t *testing.T) {
	t.Parallel()

	var occurrence OccurrenceCommandV2Request
	decodeTaskDomainJSON(t, `{
		"expected_task_revision":4,
		"expected_schedule_revision":8,
		"expected_occurrence_revisions":{"occurrence-1":9},
		"blocked_reason":"dependency unavailable",
		"next_action":"contact owner"
	}`, &occurrence)
	if occurrence.ExpectedTaskRevision != 4 || occurrence.ExpectedScheduleRevision != 8 || occurrence.ExpectedOccurrenceRevisions["occurrence-1"] != 9 || occurrence.BlockedReason == "" || occurrence.NextAction == "" {
		t.Fatalf("occurrence command = %#v", occurrence)
	}

	for _, payload := range []string{
		`{"expected_task_revision":4,"expected_schedule_revision":8,"expected_occurrence_revisions":{"occurrence-1":9},"execution_status":"done"}`,
		`{"expected_task_revision":4,"expected_schedule_revision":8,"expected_occurrence_revisions":{"occurrence-1":9},"workspace_id":"workspace-2"}`,
	} {
		var request OccurrenceCommandV2Request
		if err := DecodeTaskDomainRequest(strings.NewReader(payload), &request); !errors.Is(err, ErrInvalidTaskDomainRequest) {
			t.Fatalf("DecodeTaskDomainRequest(%s) error = %v, want %v", payload, err, ErrInvalidTaskDomainRequest)
		}
	}

	var once RescheduleOccurrenceV2Request
	decodeTaskDomainJSON(t, `{
		"expected_task_revision":4,
		"expected_schedule_revision":8,
		"expected_occurrence_revision":9,
		"timing":{"timing_type":"time_block","timezone":"America/New_York","planned_date":"2026-11-01","local_start_time":"01:30:00","duration_minutes":30},
		"selected_offsets":{"2026-11-01":-14400}
	}`, &once)
	if once.SelectedOffsets["2026-11-01"] != -14400 || once.ExpectedOccurrenceRevision != 9 {
		t.Fatalf("reschedule occurrence = %#v", once)
	}

	var future RescheduleThisAndFutureV2Request
	decodeTaskDomainJSON(t, `{
		"expected_task_revision":4,
		"expected_schedule_revision":8,
		"effective_from":"2026-11-01",
		"generate_through_exclusive":"2027-02-01",
		"schedule":{"recurrence_type":"daily","timing_type":"time_block","timezone":"America/New_York","starts_on":"2026-11-01","local_start_time":"01:30:00","duration_minutes":30,"rule":{"interval":1}},
		"selected_offsets":{"2026-11-01":-14400}
	}`, &future)
	if future.EffectiveFrom != "2026-11-01" || future.SelectedOffsets["2026-11-01"] != -14400 {
		t.Fatalf("reschedule this-and-future = %#v", future)
	}
}

func TestDecodeTaskDomainRequestRejectsTrailingJSON(t *testing.T) {
	t.Parallel()

	var request ProjectCommandV2Request
	err := DecodeTaskDomainRequest(strings.NewReader(`{"expected_project_revision":1}{"expected_project_revision":2}`), &request)
	if !errors.Is(err, ErrInvalidTaskDomainRequest) {
		t.Fatalf("DecodeTaskDomainRequest() error = %v, want %v", err, ErrInvalidTaskDomainRequest)
	}
}

func TestCreateTaskV2RequestJSONContainsDefinitionAndSchedule(t *testing.T) {
	t.Parallel()

	request := CreateTaskV2Request{
		ProjectID:   "project-1",
		TaskNoteID:  stringPointer("task-note-1"),
		Title:       "每日复盘",
		Description: "记录进展与阻塞",
		Priority:    1,
		Schedule: ScheduleV2Input{
			RecurrenceType:  taskdomain.RecurrenceDaily,
			TimingType:      taskdomain.TimingTimeBlock,
			Timezone:        "Asia/Shanghai",
			StartsOn:        "2026-07-21",
			LocalStartTime:  "21:00:00",
			DurationMinutes: 30,
			Rule:            json.RawMessage(`{"interval":1}`),
		},
	}

	wire := marshalObject(t, request)
	assertJSONValue(t, wire, "project_id", "project-1")
	assertJSONValue(t, wire, "task_note_id", "task-note-1")
	if _, ambiguous := wire["note_id"]; ambiguous {
		t.Fatal("create task contract contains ambiguous note_id")
	}
	schedule, ok := wire["schedule"].(map[string]any)
	if !ok {
		t.Fatalf("schedule = %#v, want object", wire["schedule"])
	}
	assertJSONValue(t, schedule, "recurrence_type", "daily")
	assertJSONValue(t, schedule, "timing_type", "time_block")
	assertJSONValue(t, schedule, "timezone", "Asia/Shanghai")
	assertJSONValue(t, schedule, "local_start_time", "21:00:00")
	if _, ok := schedule["rule"].(map[string]any); !ok {
		t.Fatalf("schedule rule = %#v, want JSON object", schedule["rule"])
	}
}

func TestTaskDomainEntityDTOsExposeIndependentRevisionsAndNotes(t *testing.T) {
	t.Parallel()

	projectWire := marshalObject(t, ProjectV2DTO{ID: "project-1", Revision: 3})
	assertJSONNumber(t, projectWire, "revision", 3)

	taskWire := marshalObject(t, TaskV2DTO{
		ID: "task-1", ProjectID: "project-1", TaskNoteID: stringPointer("task-note-1"),
		Revision: 4, ScheduleRevision: 8,
	})
	assertJSONNumber(t, taskWire, "revision", 4)
	assertJSONNumber(t, taskWire, "schedule_revision", 8)
	assertJSONValue(t, taskWire, "task_note_id", "task-note-1")
	if _, ambiguous := taskWire["note_id"]; ambiguous {
		t.Fatal("task DTO contains ambiguous note_id")
	}

	occurrenceWire := marshalObject(t, OccurrenceV2DTO{
		ID: "occurrence-1", TaskID: "task-1",
		TaskNoteID: stringPointer("task-note-1"), OccurrenceNoteID: stringPointer("occurrence-note-1"),
		Revision: 9, GeneratedScheduleRevision: 8,
	})
	assertJSONNumber(t, occurrenceWire, "revision", 9)
	assertJSONNumber(t, occurrenceWire, "generated_schedule_revision", 8)
	assertJSONValue(t, occurrenceWire, "task_note_id", "task-note-1")
	assertJSONValue(t, occurrenceWire, "occurrence_note_id", "occurrence-note-1")
	if _, ambiguous := occurrenceWire["note_id"]; ambiguous {
		t.Fatal("occurrence DTO contains ambiguous note_id")
	}
}

func TestAggregateCommandContractsCarryExpectedAndNewRevisions(t *testing.T) {
	t.Parallel()

	requestWire := marshalObject(t, TaskAggregateCommandRequest{
		ExpectedTaskRevision:        4,
		ExpectedScheduleRevision:    int64Pointer(8),
		ExpectedOccurrenceRevisions: map[string]int64{"occurrence-1": 9},
	})
	assertJSONNumber(t, requestWire, "expected_task_revision", 4)
	assertJSONNumber(t, requestWire, "expected_schedule_revision", 8)
	assertRevisionMap(t, requestWire, "expected_occurrence_revisions", map[string]int64{"occurrence-1": 9})

	responseWire := marshalObject(t, TaskAggregateCommandResponse{
		TaskRevision:        5,
		ScheduleRevision:    int64Pointer(8),
		OccurrenceRevisions: map[string]int64{"occurrence-1": 10},
	})
	assertJSONNumber(t, responseWire, "task_revision", 5)
	assertJSONNumber(t, responseWire, "schedule_revision", 8)
	assertRevisionMap(t, responseWire, "occurrence_revisions", map[string]int64{"occurrence-1": 10})
}

func TestCalendarEntryV2JSONPreservesProjectionIdentityRevisionsAndMetadata(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 7, 22, 13, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	due := end.Add(24 * time.Hour)
	wire := marshalObject(t, CalendarEntryV2DTO{
		ProjectID: "project-1", ProjectRevision: 3,
		TaskID: "task-1", TaskRevision: 4, ScheduleRevision: 11, TaskTitle: "复盘", TaskNoteID: stringPointer("task-note-1"),
		OccurrenceID: "occurrence-1", OccurrenceKey: "2026-07-22", OccurrenceRevision: 9, GeneratedScheduleRevision: 8,
		OccurrenceNoteID: stringPointer("occurrence-note-1"), ExecutionStatus: taskdomain.ExecutionStatusActive,
		TimingType: taskdomain.TimingTimeBlock, Timezone: "Asia/Shanghai", Recurring: true,
		PlannedStartAt: &start, PlannedEndAt: &end, DueAt: &due,
		Location: "会议室", CalendarKind: "work", CalendarNotes: "带材料",
	})
	for field, want := range map[string]float64{
		"project_revision": 3, "task_revision": 4, "schedule_revision": 11,
		"occurrence_revision": 9, "generated_schedule_revision": 8,
	} {
		assertJSONNumber(t, wire, field, int64(want))
	}
	assertJSONValue(t, wire, "occurrence_key", "2026-07-22")
	assertJSONValue(t, wire, "timezone", "Asia/Shanghai")
	assertJSONValue(t, wire, "recurring", true)
	assertJSONValue(t, wire, "task_note_id", "task-note-1")
	assertJSONValue(t, wire, "occurrence_note_id", "occurrence-note-1")
	assertJSONValue(t, wire, "execution_status", "active")
	assertJSONValue(t, wire, "timing_type", "time_block")
	assertJSONValue(t, wire, "planned_start_at", start.Format(time.RFC3339))
	assertJSONValue(t, wire, "location", "会议室")
	if _, ambiguous := wire["note_id"]; ambiguous {
		t.Fatal("calendar entry contains ambiguous note_id")
	}
}

func TestMapTaskDomainErrorStatusAndStableCode(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		err       error
		status    int
		code      string
		retryable bool
	}{
		{name: "revision conflict", err: fmt.Errorf("save: %w", taskdomain.ErrAggregateRevisionConflict), status: http.StatusConflict, code: "revision_conflict"},
		{name: "task revision conflict", err: taskdomain.ErrTaskRevisionConflict, status: http.StatusConflict, code: "revision_conflict"},
		{name: "schedule revision conflict", err: taskdomain.ErrScheduleRevisionConflict, status: http.StatusConflict, code: "revision_conflict"},
		{name: "occurrence revision conflict", err: taskdomain.ErrOccurrenceRevisionConflict, status: http.StatusConflict, code: "revision_conflict"},
		{name: "project revision conflict", err: taskdomain.ErrProjectRevisionConflict, status: http.StatusConflict, code: "revision_conflict"},
		{name: "task transition", err: taskdomain.ErrInvalidTaskTransition, status: http.StatusBadRequest, code: "invalid_transition"},
		{name: "occurrence transition", err: taskdomain.ErrInvalidOccurrenceTransition, status: http.StatusBadRequest, code: "invalid_transition"},
		{name: "occurrence requires reopen", err: taskdomain.ErrOccurrenceReopenRequired, status: http.StatusConflict, code: "occurrence_reopen_required"},
		{name: "invalid schedule", err: taskdomain.ErrInvalidSchedule, status: http.StatusBadRequest, code: "invalid_schedule"},
		{name: "nonexistent local time", err: taskdomain.ErrNonexistentLocalTime, status: http.StatusUnprocessableEntity, code: "nonexistent_local_time"},
		{name: "service epoch mismatch", err: taskdomain.ErrTaskRuntimeEpochConflict, status: http.StatusConflict, code: "tenant_epoch_mismatch", retryable: true},
		{name: "epoch mismatch", err: fmt.Errorf("write: %w", storage.ErrTenantEpochMismatch), status: http.StatusConflict, code: "tenant_epoch_mismatch", retryable: true},
		{name: "tenant fenced", err: fmt.Errorf("write: %w", storage.ErrTenantWorkspaceFenced), status: http.StatusServiceUnavailable, code: "tenant_workspace_fenced", retryable: true},
		{name: "project not found", err: taskdomain.ErrProjectNotFound, status: http.StatusNotFound, code: "project_not_found"},
		{name: "task not found", err: taskdomain.ErrTaskNotFound, status: http.StatusNotFound, code: "task_not_found"},
		{name: "occurrence not found", err: taskdomain.ErrOccurrenceNotFound, status: http.StatusNotFound, code: "occurrence_not_found"},
		{name: "project has open occurrences", err: taskdomain.ErrProjectHasOpenOccurrences, status: http.StatusConflict, code: "project_has_open_occurrences"},
		{name: "system project immutable", err: taskdomain.ErrSystemProjectImmutable, status: http.StatusBadRequest, code: "system_project_immutable"},
		{name: "invalid system project set", err: taskdomain.ErrInvalidSystemProjectSet, status: http.StatusBadRequest, code: "invalid_system_project_set"},
		{name: "invalid project command", err: taskdomain.ErrInvalidProjectCommand, status: http.StatusBadRequest, code: "invalid_project_command"},
		{name: "invalid task command", err: taskdomain.ErrInvalidTaskCommand, status: http.StatusBadRequest, code: "invalid_task_command"},
		{name: "invalid schedule command", err: taskdomain.ErrInvalidScheduleCommand, status: http.StatusBadRequest, code: "invalid_schedule_command"},
		{name: "invalid task creation", err: taskdomain.ErrInvalidTaskCreation, status: http.StatusBadRequest, code: "invalid_task_creation"},
		{name: "blocked details required", err: taskdomain.ErrBlockedDetailsRequired, status: http.StatusBadRequest, code: "blocked_details_required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mapped := MapTaskDomainError(tc.err)
			if mapped.Status != tc.status || mapped.Response.Error.Code != tc.code || mapped.Response.Error.Retryable != tc.retryable {
				t.Fatalf("MapTaskDomainError() = %#v, want status=%d code=%q retryable=%v", mapped, tc.status, tc.code, tc.retryable)
			}
			if mapped.Response.Error.Message == "" {
				t.Fatal("mapped error message is empty")
			}
		})
	}
}

func TestRevisionConflictMappingPreservesCurrentRevisionSnapshot(t *testing.T) {
	t.Parallel()

	current := TaskDomainCurrentRevisions{
		ProjectRevision:     int64Pointer(3),
		TaskRevision:        int64Pointer(5),
		ScheduleRevision:    int64Pointer(8),
		OccurrenceRevisions: map[string]int64{"occurrence-1": 10},
	}
	mapped := MapTaskDomainError(fmt.Errorf("save aggregate: %w", &RevisionConflictContractError{
		CurrentRevisions: current,
	}))

	if mapped.Status != http.StatusConflict || mapped.Response.Error.Code != "revision_conflict" {
		t.Fatalf("mapped revision conflict = %#v", mapped)
	}
	if mapped.Response.Error.Details == nil || !reflect.DeepEqual(mapped.Response.Error.Details.CurrentRevisions, &current) {
		t.Fatalf("current revisions = %#v, want %#v", mapped.Response.Error.Details, current)
	}

	wire := marshalObject(t, mapped.Response)
	errorObject, ok := wire["error"].(map[string]any)
	if !ok {
		t.Fatalf("error response = %#v, want error object", wire)
	}
	details, ok := errorObject["details"].(map[string]any)
	if !ok {
		t.Fatalf("details = %#v, want object", errorObject["details"])
	}
	revisions, ok := details["current_revisions"].(map[string]any)
	if !ok {
		t.Fatalf("current_revisions = %#v, want object", details["current_revisions"])
	}
	assertJSONNumber(t, revisions, "project_revision", 3)
	assertJSONNumber(t, revisions, "task_revision", 5)
	assertJSONNumber(t, revisions, "schedule_revision", 8)
	assertRevisionMap(t, revisions, "occurrence_revisions", map[string]int64{"occurrence-1": 10})
}

func TestRevisionConflictWithoutSnapshotKeepsDetailsEmpty(t *testing.T) {
	t.Parallel()

	mapped := MapTaskDomainError(taskdomain.ErrAggregateRevisionConflict)
	if mapped.Status != http.StatusConflict || mapped.Response.Error.Code != "revision_conflict" {
		t.Fatalf("mapped revision conflict = %#v", mapped)
	}
	if mapped.Response.Error.Details != nil {
		t.Fatalf("plain revision conflict details = %#v, want nil", mapped.Response.Error.Details)
	}
}

func TestAmbiguousLocalTimeMappingPreservesOffsetCandidates(t *testing.T) {
	t.Parallel()

	candidates := []taskdomain.OffsetCandidate{
		{OffsetSeconds: -14400, UTC: time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC)},
		{OffsetSeconds: -18000, UTC: time.Date(2026, 11, 1, 6, 30, 0, 0, time.UTC)},
	}
	mapped := MapTaskDomainError(NewAmbiguousLocalTimeContractError(candidates))
	if mapped.Status != http.StatusUnprocessableEntity || mapped.Response.Error.Code != "ambiguous_local_time" {
		t.Fatalf("mapped ambiguous error = %#v", mapped)
	}
	if mapped.Response.Error.Details == nil || len(mapped.Response.Error.Details.OffsetCandidates) != 2 {
		t.Fatalf("offset candidates were not preserved: %#v", mapped.Response.Error.Details)
	}
	for index, candidate := range mapped.Response.Error.Details.OffsetCandidates {
		if candidate.OffsetSeconds != candidates[index].OffsetSeconds || !candidate.UTC.Equal(candidates[index].UTC) {
			t.Fatalf("candidate %d = %#v, want %#v", index, candidate, candidates[index])
		}
	}
	wire := marshalObject(t, mapped.Response)
	errorObject, ok := wire["error"].(map[string]any)
	if !ok {
		t.Fatalf("error response = %#v, want error object", wire)
	}
	details, ok := errorObject["details"].(map[string]any)
	if !ok {
		t.Fatalf("error details = %#v, want object", errorObject["details"])
	}
	if values, ok := details["offset_candidates"].([]any); !ok || len(values) != 2 {
		t.Fatalf("serialized candidates = %#v, want two", details["offset_candidates"])
	}
}

func TestTenantFenceErrorsAlwaysProduceExplicitHTTPFailure(t *testing.T) {
	t.Parallel()

	for _, err := range []error{storage.ErrTenantEpochMismatch, storage.ErrTenantWorkspaceFenced} {
		mapped := MapTaskDomainError(err)
		if mapped.Status < 400 || mapped.Response.Error.Code == "" {
			t.Fatalf("tenant fence error %v was not explicitly rejected: %#v", err, mapped)
		}
	}
}

func marshalObject(t *testing.T, value any) map[string]any {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal(%T): %v", value, err)
	}
	var object map[string]any
	if err := json.Unmarshal(payload, &object); err != nil {
		t.Fatalf("json.Unmarshal(%s): %v", payload, err)
	}
	return object
}

func decodeTaskDomainJSON(t *testing.T, payload string, destination any) {
	t.Helper()
	if err := DecodeTaskDomainRequest(strings.NewReader(payload), destination); err != nil {
		t.Fatalf("DecodeTaskDomainRequest(%s): %v", payload, err)
	}
}

func assertJSONValue(t *testing.T, object map[string]any, field string, want any) {
	t.Helper()
	if !reflect.DeepEqual(object[field], want) {
		t.Fatalf("JSON field %q = %#v, want %#v", field, object[field], want)
	}
}

func assertJSONNumber(t *testing.T, object map[string]any, field string, want int64) {
	t.Helper()
	if got, ok := object[field].(float64); !ok || got != float64(want) {
		t.Fatalf("JSON field %q = %#v, want %d", field, object[field], want)
	}
}

func assertRevisionMap(t *testing.T, object map[string]any, field string, want map[string]int64) {
	t.Helper()
	got, ok := object[field].(map[string]any)
	if !ok {
		t.Fatalf("JSON field %q = %#v, want object", field, object[field])
	}
	for id, revision := range want {
		assertJSONNumber(t, got, id, revision)
	}
}

func stringPointer(value string) *string { return &value }
func int64Pointer(value int64) *int64    { return &value }
