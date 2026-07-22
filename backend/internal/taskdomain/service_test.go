package taskdomain

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestTaskServiceCreateTaskPersistsCompleteAggregateInOneFencedWrite(t *testing.T) {
	writer := &serviceWriter{}
	fencer := &serviceFencer{writer: writer}
	reader := &serviceStateReader{fencer: fencer}
	service := NewTaskService(fencer, reader)
	snapshot := serviceTaskSnapshot()
	request := CreateTaskRequest{
		WorkspaceID: "workspace-1", ExpectedRuntimeEpoch: 9, Snapshot: snapshot,
		CommandID: "command-create", ActorID: "user-1", At: serviceCommandTime(),
	}

	result, err := service.CreateTask(context.Background(), request)
	if err != nil {
		t.Fatalf("CreateTask() unexpected error: %v", err)
	}
	if fencer.beginCalls != 1 || fencer.workspaceID != "workspace-1" || fencer.expectedEpoch != 9 {
		t.Fatalf("fenced write = calls:%d workspace:%q epoch:%d", fencer.beginCalls, fencer.workspaceID, fencer.expectedEpoch)
	}
	if writer.createCalls != 1 || writer.saveCalls != 0 || !reflect.DeepEqual(writer.created, snapshot) {
		t.Fatalf("writer calls/create = create:%d save:%d snapshot:%#v", writer.createCalls, writer.saveCalls, writer.created)
	}
	if reader.calls != 0 {
		t.Fatalf("CreateTask() unexpectedly read an existing aggregate %d times", reader.calls)
	}
	assertServiceAudit(t, result, TaskCommandCreate, "command-create", "task-1")
	if result.TaskRevision() != 1 || result.ScheduleRevision() != 1 || result.LifecycleStatus() != TaskLifecycleDraft {
		t.Fatalf("create result revisions/status = (%d,%d,%q)", result.TaskRevision(), result.ScheduleRevision(), result.LifecycleStatus())
	}
}

func TestTaskServiceLifecycleCommandsUsePureTransitionsAndAtomicSave(t *testing.T) {
	tests := []struct {
		name              string
		command           TaskLifecycleCommand
		current           TaskLifecycleStatus
		generationEnabled bool
		want              TaskLifecycleStatus
		wantGeneration    bool
	}{
		{name: "publish", command: TaskCommandPublish, current: TaskLifecycleDraft, want: TaskLifecycleActive, wantGeneration: true},
		{name: "pause", command: TaskCommandPause, current: TaskLifecycleActive, generationEnabled: true, want: TaskLifecyclePaused},
		{name: "resume", command: TaskCommandResume, current: TaskLifecyclePaused, want: TaskLifecycleActive, wantGeneration: true},
		{name: "restore", command: TaskCommandRestore, current: TaskLifecycleCancelled, want: TaskLifecycleActive, wantGeneration: true},
		{name: "archive completed", command: TaskCommandArchive, current: TaskLifecycleCompleted, want: TaskLifecycleArchived},
		{name: "archive cancelled", command: TaskCommandArchive, current: TaskLifecycleCancelled, want: TaskLifecycleArchived},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			current := serviceTaskAggregate(tt.current)
			current.GenerationEnabled = tt.generationEnabled
			writer := &serviceWriter{}
			fencer := &serviceFencer{writer: writer}
			reader := &serviceStateReader{fencer: fencer, state: TaskAggregateState{Aggregate: current, ScheduleRevision: 7}}
			service := NewTaskService(fencer, reader)
			request := serviceLifecycleRequest(tt.command)

			result, err := service.ExecuteLifecycleCommand(context.Background(), request)
			if err != nil {
				t.Fatalf("ExecuteLifecycleCommand() unexpected error: %v", err)
			}
			if fencer.beginCalls != 1 || reader.calls != 1 || !reader.readInsideFence {
				t.Fatalf("command boundary = fence:%d reads:%d readInside:%t", fencer.beginCalls, reader.calls, reader.readInsideFence)
			}
			if writer.saveCalls != 1 || writer.createCalls != 0 {
				t.Fatalf("writer calls = save:%d create:%d", writer.saveCalls, writer.createCalls)
			}
			write := writer.saved
			if write.Aggregate.LifecycleStatus != tt.want || write.Aggregate.Revision != 6 || write.Aggregate.GenerationEnabled != tt.wantGeneration {
				t.Fatalf("aggregate after-image = status:%q revision:%d generation:%t", write.Aggregate.LifecycleStatus, write.Aggregate.Revision, write.Aggregate.GenerationEnabled)
			}
			if write.ExpectedRevisions.Task != 5 || len(write.ExpectedRevisions.Occurrences) != 0 || write.ExpectedScheduleRevision != 7 || len(write.ExecutionLogs) != 0 {
				t.Fatalf("write CAS/audit shape = %#v", write)
			}
			assertServiceAudit(t, result, tt.command, "command-1", "task-1")
			if result.TaskRevision() != 6 || result.ScheduleRevision() != 7 || result.LifecycleStatus() != tt.want {
				t.Fatalf("command result = task:%d schedule:%d status:%q", result.TaskRevision(), result.ScheduleRevision(), result.LifecycleStatus())
			}
			if revisions := result.OccurrenceRevisions(); len(revisions) != 0 {
				t.Fatalf("non-cancel occurrence revisions = %#v, want empty", revisions)
			}
		})
	}
}

func TestTaskServiceCancelPersistsTaskOccurrencesAndLogsAtomically(t *testing.T) {
	now := serviceCommandTime()
	current := serviceTaskAggregate(TaskLifecycleActive)
	current.Occurrences = []Occurrence{
		serviceOccurrence("open", ExecutionStatusOpen, 10, now),
		serviceOccurrence("active", ExecutionStatusActive, 11, now),
		serviceOccurrence("done", ExecutionStatusDone, 12, now),
	}
	writer := &serviceWriter{}
	fencer := &serviceFencer{writer: writer}
	reader := &serviceStateReader{fencer: fencer, state: TaskAggregateState{Aggregate: current, ScheduleRevision: 7}}
	service := NewTaskService(fencer, reader)
	request := serviceLifecycleRequest(TaskCommandCancel)
	request.Expected.Occurrences = map[string]int64{"open": 10, "active": 11}

	result, err := service.ExecuteLifecycleCommand(context.Background(), request)
	if err != nil {
		t.Fatalf("ExecuteLifecycleCommand(cancel) unexpected error: %v", err)
	}
	if writer.saveCalls != 1 {
		t.Fatalf("SaveTaskAggregate() calls = %d, want 1", writer.saveCalls)
	}
	write := writer.saved
	if write.Aggregate.LifecycleStatus != TaskLifecycleCancelled || write.Aggregate.GenerationEnabled || write.Aggregate.Revision != 6 {
		t.Fatalf("cancelled task after-image = %#v", write.Aggregate)
	}
	assertServiceOccurrenceStatus(t, write.Aggregate, "open", ExecutionStatusCancelled, 11)
	assertServiceOccurrenceStatus(t, write.Aggregate, "active", ExecutionStatusCancelled, 12)
	assertServiceOccurrenceStatus(t, write.Aggregate, "done", ExecutionStatusDone, 12)
	if !reflect.DeepEqual(write.ExpectedRevisions.Occurrences, request.Expected.Occurrences) || write.ExpectedScheduleRevision != 7 || len(write.ExecutionLogs) != 2 {
		t.Fatalf("cancel atomic write = %#v", write)
	}
	logs := result.ExecutionLogs()
	if len(logs) != 2 || logs[0].ToStatus() != ExecutionStatusCancelled || logs[1].ToStatus() != ExecutionStatusCancelled {
		t.Fatalf("cancel result logs = %#v", logs)
	}
	revisions := result.OccurrenceRevisions()
	if !reflect.DeepEqual(revisions, map[string]int64{"open": 11, "active": 12}) {
		t.Fatalf("cancel result occurrence revisions = %#v", revisions)
	}
	revisions["open"] = 999
	if got := result.OccurrenceRevisions()["open"]; got != 11 {
		t.Fatalf("OccurrenceRevisions() leaked mutable state: %d", got)
	}
	assertServiceAudit(t, result, TaskCommandCancel, "command-1", "task-1")
}

func TestTaskServiceRevisionAndEpochConflictsAreDistinctAndNeverSave(t *testing.T) {
	base := serviceTaskAggregate(TaskLifecycleActive)
	base.Occurrences = []Occurrence{serviceOccurrence("open", ExecutionStatusOpen, 10, serviceCommandTime())}

	tests := []struct {
		name       string
		fenceError error
		state      TaskAggregateState
		mutate     func(*LifecycleCommandRequest)
		want       error
	}{
		{name: "stale runtime epoch", fenceError: ErrTaskRuntimeEpochConflict, state: TaskAggregateState{Aggregate: base, ScheduleRevision: 7}, want: ErrTaskRuntimeEpochConflict},
		{name: "stale task revision", state: TaskAggregateState{Aggregate: base, ScheduleRevision: 7}, mutate: func(request *LifecycleCommandRequest) { request.Expected.Task = 4 }, want: ErrTaskRevisionConflict},
		{name: "stale schedule revision", state: TaskAggregateState{Aggregate: base, ScheduleRevision: 7}, mutate: func(request *LifecycleCommandRequest) { request.Expected.Schedule = 6 }, want: ErrScheduleRevisionConflict},
		{name: "stale occurrence revision", state: TaskAggregateState{Aggregate: base, ScheduleRevision: 7}, mutate: func(request *LifecycleCommandRequest) { request.Expected.Occurrences = map[string]int64{"open": 9} }, want: ErrOccurrenceRevisionConflict},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writer := &serviceWriter{}
			fencer := &serviceFencer{writer: writer, beginError: tt.fenceError}
			reader := &serviceStateReader{fencer: fencer, state: tt.state}
			service := NewTaskService(fencer, reader)
			request := serviceLifecycleRequest(TaskCommandCancel)
			request.Expected.Occurrences = map[string]int64{"open": 10}
			if tt.mutate != nil {
				tt.mutate(&request)
			}

			_, err := service.ExecuteLifecycleCommand(context.Background(), request)
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
			if writer.saveCalls != 0 || writer.createCalls != 0 {
				t.Fatalf("conflict wrote state: save=%d create=%d", writer.saveCalls, writer.createCalls)
			}
		})
	}
}

func TestTaskServiceInvalidTransitionAndWriterFailureDoNotReturnAuditResult(t *testing.T) {
	tests := []struct {
		name        string
		current     TaskLifecycleStatus
		command     TaskLifecycleCommand
		writerError error
		want        error
	}{
		{name: "invalid pure transition", current: TaskLifecycleDraft, command: TaskCommandPause, want: ErrInvalidTaskTransition},
		{name: "writer failure", current: TaskLifecycleActive, command: TaskCommandPause, writerError: errors.New("save failed"), want: errors.New("save failed")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writer := &serviceWriter{saveError: tt.writerError}
			fencer := &serviceFencer{writer: writer}
			reader := &serviceStateReader{fencer: fencer, state: TaskAggregateState{Aggregate: serviceTaskAggregate(tt.current), ScheduleRevision: 7}}
			service := NewTaskService(fencer, reader)
			result, err := service.ExecuteLifecycleCommand(context.Background(), serviceLifecycleRequest(tt.command))
			if tt.writerError != nil {
				if err == nil || err.Error() != tt.writerError.Error() {
					t.Fatalf("writer error = %v, want %v", err, tt.writerError)
				}
			} else if !errors.Is(err, tt.want) {
				t.Fatalf("transition error = %v, want %v", err, tt.want)
			}
			if !result.IsZero() {
				t.Fatalf("failed command returned an audit result: %#v", result)
			}
		})
	}
}

func TestTaskServicePatchTaskWritesOnlyOrdinaryAttributesAtomically(t *testing.T) {
	current := serviceTaskAggregate(TaskLifecycleActive)
	current.Occurrences = []Occurrence{serviceOccurrence("open", ExecutionStatusOpen, 3, serviceCommandTime())}
	state := TaskAggregateState{
		Aggregate: current, ScheduleRevision: 7,
		Task: TaskRecord{
			WorkspaceID: "workspace-1", ID: "task-1", ProjectID: "project-old", RoadmapNodeID: "roadmap-old", NoteID: "note-old",
			Title: "Before", Description: "Old", LifecycleStatus: TaskLifecycleActive, Priority: 1, SortOrder: 2.5, Revision: 5,
		},
	}
	writer := &serviceWriter{}
	fencer := &serviceFencer{writer: writer}
	reader := &serviceStateReader{fencer: fencer, state: state}
	service := NewTaskService(fencer, reader)
	title, description, priority, sortOrder := " After ", "New", 3, 9.5

	result, err := service.PatchTask(context.Background(), PatchTaskRequest{
		WorkspaceID: "workspace-1", TaskID: "task-1", ExpectedRuntimeEpoch: 12,
		ExpectedTaskRevision: 5, ExpectedScheduleRevision: 7,
		Patch: TaskAttributePatch{
			Title: &title, Description: &description, Priority: &priority, SortOrder: &sortOrder,
			Project:    &ProjectIdentity{WorkspaceID: "workspace-1", ProjectID: "project-new"},
			RoadmapSet: true, Roadmap: &Roadmap{WorkspaceID: "workspace-1", ID: "roadmap-new", ProjectID: "project-new"},
			TaskNoteSet: true, TaskNote: &TaskNoteIdentity{WorkspaceID: "workspace-1", NoteID: "note-new"},
		},
		CommandID: "patch-1", ActorID: "user-1", At: serviceCommandTime(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if fencer.beginCalls != 1 || fencer.expectedEpoch != 12 || reader.calls != 1 || !reader.readInsideFence || writer.saveCalls != 1 {
		t.Fatalf("patch boundary = fence:%d epoch:%d read:%d inside:%t save:%d", fencer.beginCalls, fencer.expectedEpoch, reader.calls, reader.readInsideFence, writer.saveCalls)
	}
	write := writer.saved
	if write.Task == nil || write.Task.Title != "After" || write.Task.Description != "New" || write.Task.Priority != 3 || write.Task.SortOrder != 9.5 ||
		write.Task.ProjectID != "project-new" || write.Task.RoadmapNodeID != "roadmap-new" || write.Task.NoteID != "note-new" || write.Task.Revision != 6 {
		t.Fatalf("task patch after-image = %#v", write.Task)
	}
	if write.Task.WorkspaceID != "workspace-1" || write.Task.ID != "task-1" || write.Task.LifecycleStatus != TaskLifecycleActive ||
		write.Aggregate.LifecycleStatus != current.LifecycleStatus || write.Aggregate.GenerationEnabled != current.GenerationEnabled ||
		write.Aggregate.Revision != 6 || !reflect.DeepEqual(write.Aggregate.Occurrences, current.Occurrences) ||
		write.ExpectedRevisions.Task != 5 || len(write.ExpectedRevisions.Occurrences) != 0 || write.ExpectedScheduleRevision != 7 || len(write.ExecutionLogs) != 0 {
		t.Fatalf("patch changed protected aggregate state = %#v", write)
	}
	if result.Task().Title != "After" || result.TaskRevision() != 6 || result.ScheduleRevision() != 7 || result.LifecycleStatus() != TaskLifecycleActive {
		t.Fatalf("patch result = %#v / %#v", result, result.Task())
	}
	assertServiceAudit(t, result, TaskCommandPatch, "patch-1", "task-1")
}

func TestTaskServicePatchTaskPreservesOmittedFieldsAndCanClearOptionalLinks(t *testing.T) {
	state := TaskAggregateState{
		Aggregate: serviceTaskAggregate(TaskLifecycleDraft), ScheduleRevision: 4,
		Task: TaskRecord{
			WorkspaceID: "workspace-1", ID: "task-1", ProjectID: "project-1", RoadmapNodeID: "roadmap-1", NoteID: "note-1",
			Title: "Before", Description: "Keep", LifecycleStatus: TaskLifecycleDraft, Priority: 2, SortOrder: 4, Revision: 5,
		},
	}
	writer := &serviceWriter{}
	fencer := &serviceFencer{writer: writer}
	service := NewTaskService(fencer, &serviceStateReader{fencer: fencer, state: state})
	request := PatchTaskRequest{
		WorkspaceID: "workspace-1", TaskID: "task-1", ExpectedRuntimeEpoch: 2, ExpectedTaskRevision: 5, ExpectedScheduleRevision: 4,
		Patch: TaskAttributePatch{RoadmapSet: true, TaskNoteSet: true}, CommandID: "patch-1", ActorID: "user-1", At: serviceCommandTime(),
	}
	if _, err := service.PatchTask(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	after := writer.saved.Task
	if after == nil || after.Title != "Before" || after.Description != "Keep" || after.Priority != 2 || after.SortOrder != 4 ||
		after.ProjectID != "project-1" || after.RoadmapNodeID != "" || after.NoteID != "" {
		t.Fatalf("omitted fields were not preserved: %#v", after)
	}
}

func TestTaskServicePatchTaskRejectsStaleRevisionsAndInvalidReferences(t *testing.T) {
	base := TaskAggregateState{
		Aggregate: serviceTaskAggregate(TaskLifecycleActive), ScheduleRevision: 7,
		Task: TaskRecord{WorkspaceID: "workspace-1", ID: "task-1", ProjectID: "project-1", Title: "Task", LifecycleStatus: TaskLifecycleActive, Priority: 1, Revision: 5},
	}
	title := "Changed"
	tests := []struct {
		name       string
		fenceError error
		mutate     func(*PatchTaskRequest)
		want       error
	}{
		{name: "runtime epoch", fenceError: ErrTaskRuntimeEpochConflict, want: ErrTaskRuntimeEpochConflict},
		{name: "task revision", mutate: func(request *PatchTaskRequest) { request.ExpectedTaskRevision = 4 }, want: ErrTaskRevisionConflict},
		{name: "schedule revision", mutate: func(request *PatchTaskRequest) { request.ExpectedScheduleRevision = 6 }, want: ErrScheduleRevisionConflict},
		{name: "project workspace", mutate: func(request *PatchTaskRequest) {
			request.Patch.Project = &ProjectIdentity{WorkspaceID: "workspace-2", ProjectID: "project-2"}
		}, want: ErrInvalidTaskCommand},
		{name: "roadmap workspace", mutate: func(request *PatchTaskRequest) {
			request.Patch.RoadmapSet = true
			request.Patch.Roadmap = &Roadmap{WorkspaceID: "workspace-2", ID: "roadmap-1", ProjectID: "project-1"}
		}, want: ErrInvalidTaskCommand},
		{name: "roadmap project", mutate: func(request *PatchTaskRequest) {
			request.Patch.RoadmapSet = true
			request.Patch.Roadmap = &Roadmap{WorkspaceID: "workspace-1", ID: "roadmap-1", ProjectID: "project-other"}
		}, want: ErrInvalidTaskCommand},
		{name: "note workspace", mutate: func(request *PatchTaskRequest) {
			request.Patch.TaskNoteSet = true
			request.Patch.TaskNote = &TaskNoteIdentity{WorkspaceID: "workspace-2", NoteID: "note-1"}
		}, want: ErrInvalidTaskCommand},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writer := &serviceWriter{}
			fencer := &serviceFencer{writer: writer, beginError: tt.fenceError}
			reader := &serviceStateReader{fencer: fencer, state: base}
			service := NewTaskService(fencer, reader)
			request := PatchTaskRequest{
				WorkspaceID: "workspace-1", TaskID: "task-1", ExpectedRuntimeEpoch: 9, ExpectedTaskRevision: 5, ExpectedScheduleRevision: 7,
				Patch: TaskAttributePatch{Title: &title}, CommandID: "patch-1", ActorID: "user-1", At: serviceCommandTime(),
			}
			if tt.mutate != nil {
				tt.mutate(&request)
			}
			result, err := service.PatchTask(context.Background(), request)
			if !errors.Is(err, tt.want) || !result.IsZero() || writer.saveCalls != 0 {
				t.Fatalf("result/error/save = %#v / %v / %d, want %v", result, err, writer.saveCalls, tt.want)
			}
		})
	}
}

func TestTaskServicePatchTaskWriterFailureReturnsNoAfterImage(t *testing.T) {
	writeErr := errors.New("composite foreign key rejected task patch")
	state := TaskAggregateState{
		Aggregate: serviceTaskAggregate(TaskLifecycleActive), ScheduleRevision: 7,
		Task: TaskRecord{WorkspaceID: "workspace-1", ID: "task-1", ProjectID: "project-1", Title: "Before", LifecycleStatus: TaskLifecycleActive, Priority: 1, Revision: 5},
	}
	writer := &serviceWriter{saveError: writeErr}
	fencer := &serviceFencer{writer: writer}
	service := NewTaskService(fencer, &serviceStateReader{fencer: fencer, state: state})
	project := ProjectIdentity{WorkspaceID: "workspace-1", ProjectID: "missing-project"}

	result, err := service.PatchTask(context.Background(), PatchTaskRequest{
		WorkspaceID: "workspace-1", TaskID: "task-1", ExpectedRuntimeEpoch: 9, ExpectedTaskRevision: 5, ExpectedScheduleRevision: 7,
		Patch: TaskAttributePatch{Project: &project}, CommandID: "patch-1", ActorID: "user-1", At: serviceCommandTime(),
	})
	if !errors.Is(err, writeErr) || !result.IsZero() || writer.saveCalls != 1 {
		t.Fatalf("writer failure result/error/save = %#v / %v / %d", result, err, writer.saveCalls)
	}
}

func serviceLifecycleRequest(command TaskLifecycleCommand) LifecycleCommandRequest {
	return LifecycleCommandRequest{
		WorkspaceID: "workspace-1", TaskID: "task-1", Command: command,
		ExpectedRuntimeEpoch: 9,
		Expected:             LifecycleExpectedRevisions{Task: 5, Schedule: 7, Occurrences: map[string]int64{}},
		CommandID:            "command-1", ActorID: "user-1", At: serviceCommandTime(),
	}
}

func serviceTaskAggregate(status TaskLifecycleStatus) TaskAggregate {
	return TaskAggregate{
		WorkspaceID: "workspace-1", TaskID: "task-1", LifecycleStatus: status,
		Recurring: true, Revision: 5, Occurrences: []Occurrence{},
	}
}

func serviceTaskSnapshot() TaskAggregateSnapshot {
	return TaskAggregateSnapshot{
		Task: TaskRecord{
			WorkspaceID: "workspace-1", ID: "task-1", ProjectID: PersonalProjectID,
			Title: "任务", LifecycleStatus: TaskLifecycleDraft, Priority: 1, Revision: 1,
		},
		Schedule: ScheduleHeader{WorkspaceID: "workspace-1", TaskID: "task-1", Revision: 1, CurrentScheduleRevision: 1},
		Versions: []ScheduleVersion{{
			WorkspaceID: "workspace-1", TaskID: "task-1", ScheduleRevision: 1,
			RecurrenceType: RecurrenceNone, TimingType: TimingUnscheduled, Timezone: "UTC", RecurrenceRule: `{}`,
		}},
		Occurrences: []OccurrenceRecord{{
			WorkspaceID: "workspace-1", ID: "occurrence-1", TaskID: "task-1", OccurrenceKey: "once",
			ExecutionStatus: ExecutionStatusOpen, Revision: 1, GeneratedScheduleRevision: 1,
		}},
	}
}

func serviceOccurrence(id string, status ExecutionStatus, revision int64, at time.Time) Occurrence {
	occurrence := Occurrence{
		WorkspaceID: "workspace-1", ID: id, TaskID: "task-1", OccurrenceKey: id,
		ExecutionStatus: status, Recurring: true, Revision: revision,
	}
	if status == ExecutionStatusActive || status == ExecutionStatusBlocked {
		startedAt := at
		occurrence.ActualStartAt = &startedAt
	}
	if status == ExecutionStatusDone {
		completedAt := at
		occurrence.CompletedAt = &completedAt
	}
	return occurrence
}

func serviceCommandTime() time.Time {
	return time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
}

func assertServiceAudit(t *testing.T, result TaskCommandResult, command TaskLifecycleCommand, commandID, taskID string) {
	t.Helper()
	audit := result.Audit()
	if audit.Command() != command || audit.CommandID() != commandID || audit.TaskID() != taskID || audit.ActorID() != "user-1" || !audit.CreatedAt().Equal(serviceCommandTime()) {
		t.Fatalf("audit = command:%q commandID:%q task:%q actor:%q at:%v", audit.Command(), audit.CommandID(), audit.TaskID(), audit.ActorID(), audit.CreatedAt())
	}
}

func assertServiceOccurrenceStatus(t *testing.T, aggregate TaskAggregate, id string, status ExecutionStatus, revision int64) {
	t.Helper()
	for _, occurrence := range aggregate.Occurrences {
		if occurrence.ID == id {
			if occurrence.ExecutionStatus != status || occurrence.Revision != revision {
				t.Fatalf("occurrence %q = (%q,%d), want (%q,%d)", id, occurrence.ExecutionStatus, occurrence.Revision, status, revision)
			}
			return
		}
	}
	t.Fatalf("occurrence %q not found", id)
}

type serviceFencer struct {
	writer        *serviceWriter
	beginError    error
	beginCalls    int
	workspaceID   string
	expectedEpoch int64
	inCallback    bool
}

func (f *serviceFencer) BeginFencedWrite(_ context.Context, workspaceID string, expectedEpoch int64, callback func(TaskDomainFencedTx) error) error {
	f.beginCalls++
	f.workspaceID = workspaceID
	f.expectedEpoch = expectedEpoch
	if f.beginError != nil {
		return f.beginError
	}
	f.inCallback = true
	err := callback(serviceTx{writer: f.writer})
	f.inCallback = false
	return err
}

type serviceTx struct{ writer TaskDomainWriter }

func (tx serviceTx) TaskDomainWriter() TaskDomainWriter { return tx.writer }

type serviceStateReader struct {
	fencer          *serviceFencer
	state           TaskAggregateState
	err             error
	calls           int
	readInsideFence bool
}

func (r *serviceStateReader) GetTaskAggregateState(_ context.Context, _ string) (TaskAggregateState, error) {
	r.calls++
	r.readInsideFence = r.fencer.inCallback
	return r.state, r.err
}

type serviceWriter struct {
	created     TaskAggregateSnapshot
	saved       TaskAggregateWrite
	createCalls int
	saveCalls   int
	createError error
	saveError   error
}

func (w *serviceWriter) EnsureSystemProjects(context.Context) error         { return nil }
func (w *serviceWriter) SaveProject(context.Context, ProjectWrite) error    { return nil }
func (w *serviceWriter) DeleteProject(context.Context, string, int64) error { return nil }
func (w *serviceWriter) InstallScheduleVersion(context.Context, ScheduleVersionInstall) error {
	return nil
}

func (w *serviceWriter) CreateTaskAggregate(_ context.Context, snapshot TaskAggregateSnapshot) error {
	w.createCalls++
	w.created = snapshot
	return w.createError
}

func (w *serviceWriter) SaveTaskAggregate(_ context.Context, write TaskAggregateWrite) error {
	w.saveCalls++
	w.saved = write
	return w.saveError
}
