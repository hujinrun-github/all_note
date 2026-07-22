package taskdomain

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestCompletionServiceCompletesRecurringTaskInOneFencedTransaction(t *testing.T) {
	state := recurringCompletionCommandFixture(t)
	writer := &completionServiceWriter{}
	tx := &completionServiceTx{state: state, writer: writer}
	fencer := &completionServiceFencer{tx: tx}
	service := NewCompletionService(fencer)
	request := completionCommandRequest()

	result, err := service.Evaluate(context.Background(), request)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if fencer.calls != 1 || fencer.workspaceID != request.WorkspaceID || fencer.expectedEpoch != request.ExpectedRuntimeEpoch {
		t.Fatalf("fence calls/workspace/epoch = %d/%q/%d", fencer.calls, fencer.workspaceID, fencer.expectedEpoch)
	}
	if tx.loadCalls != 1 || !tx.loadInsideFence || writer.saveCalls != 1 || !writer.saveInsideFence {
		t.Fatalf("load/save boundary = tx:%#v writer:%#v", tx, writer)
	}
	write := writer.write
	if write.Aggregate.LifecycleStatus != TaskLifecycleCompleted || write.Aggregate.Revision != 6 ||
		write.ExpectedRevisions.Task != 5 || len(write.ExpectedRevisions.Occurrences) != 0 ||
		write.ExpectedScheduleRevision != 7 || len(write.ExecutionLogs) != 0 {
		t.Fatalf("completion write = %#v", write)
	}
	if !reflect.DeepEqual(write.Aggregate.Occurrences, state.Aggregate.Occurrences) {
		t.Fatal("natural completion changed occurrence history")
	}
	if !result.Changed() || result.LifecycleStatus() != TaskLifecycleCompleted || result.TaskRevision() != 6 || result.ScheduleRevision() != 7 {
		t.Fatalf("completion result = %#v", result)
	}
	if state.Aggregate.LifecycleStatus != TaskLifecycleActive || state.Aggregate.Revision != 5 {
		t.Fatalf("input state mutated: %#v", state.Aggregate)
	}
}

func TestCompletionServiceUnmetProofReturnsNoChangeAndNoSave(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*RecurringCompletionCommandState)
	}{
		{name: "watermark", mutate: func(state *RecurringCompletionCommandState) { state.GenerationWatermark = "2026-07-02" }},
		{name: "missing expected key", mutate: func(state *RecurringCompletionCommandState) {
			state.Aggregate.Occurrences = state.Aggregate.Occurrences[:2]
		}},
		{name: "generation running", mutate: func(state *RecurringCompletionCommandState) { state.GenerationStatus = GenerationStatusRunning }},
		{name: "retry pending", mutate: func(state *RecurringCompletionCommandState) { state.RetryPendingJobs = 1 }},
		{name: "generation failure", mutate: func(state *RecurringCompletionCommandState) { state.FailedJobs = 1 }},
		{name: "nonterminal", mutate: func(state *RecurringCompletionCommandState) {
			state.Aggregate.Occurrences[0].ExecutionStatus = ExecutionStatusOpen
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := recurringCompletionCommandFixture(t)
			tt.mutate(&state)
			writer := &completionServiceWriter{}
			tx := &completionServiceTx{state: state, writer: writer}
			fencer := &completionServiceFencer{tx: tx}
			result, err := NewCompletionService(fencer).Evaluate(context.Background(), completionCommandRequest())
			if err != nil {
				t.Fatalf("Evaluate() error = %v", err)
			}
			if result.Changed() || result.LifecycleStatus() != TaskLifecycleActive || result.TaskRevision() != 5 || result.ScheduleRevision() != 7 {
				t.Fatalf("no-change result = %#v", result)
			}
			if writer.saveCalls != 0 {
				t.Fatalf("unmet proof wrote aggregate: %#v", writer)
			}
		})
	}
}

func TestCompletionServiceReactivatesCompletedTaskAfterHistoricalOccurrenceReopen(t *testing.T) {
	state := recurringCompletionCommandFixture(t)
	state.Aggregate.LifecycleStatus = TaskLifecycleCompleted
	state.Aggregate.Occurrences[0].ExecutionStatus = ExecutionStatusOpen
	writer := &completionServiceWriter{}
	tx := &completionServiceTx{state: state, writer: writer}
	fencer := &completionServiceFencer{tx: tx}

	result, err := NewCompletionService(fencer).Evaluate(context.Background(), completionCommandRequest())
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if writer.saveCalls != 1 || writer.write.Aggregate.LifecycleStatus != TaskLifecycleActive || writer.write.Aggregate.Revision != 6 {
		t.Fatalf("reactivation write = %#v", writer.write)
	}
	if !result.Changed() || result.LifecycleStatus() != TaskLifecycleActive || result.TaskRevision() != 6 {
		t.Fatalf("reactivation result = %#v", result)
	}

	state = recurringCompletionCommandFixture(t)
	state.Aggregate.LifecycleStatus = TaskLifecycleCompleted
	writer = &completionServiceWriter{}
	tx = &completionServiceTx{state: state, writer: writer}
	fencer = &completionServiceFencer{tx: tx}
	result, err = NewCompletionService(fencer).Evaluate(context.Background(), completionCommandRequest())
	if err != nil || result.Changed() || result.LifecycleStatus() != TaskLifecycleCompleted || writer.saveCalls != 0 {
		t.Fatalf("stable completed result/error/writes = %#v / %v / %d", result, err, writer.saveCalls)
	}
}

func TestCompletionServiceStaleEpochTaskAndScheduleNeverWrite(t *testing.T) {
	tests := []struct {
		name       string
		fenceError error
		mutate     func(*RecurringCompletionCommandState)
		want       error
	}{
		{name: "epoch", fenceError: ErrTaskRuntimeEpochConflict, want: ErrTaskRuntimeEpochConflict},
		{name: "task", mutate: func(state *RecurringCompletionCommandState) { state.Aggregate.Revision = 4 }, want: ErrTaskRevisionConflict},
		{name: "schedule", mutate: func(state *RecurringCompletionCommandState) { state.ScheduleRevision = 6 }, want: ErrScheduleRevisionConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := recurringCompletionCommandFixture(t)
			if tt.mutate != nil {
				tt.mutate(&state)
			}
			writer := &completionServiceWriter{}
			tx := &completionServiceTx{state: state, writer: writer}
			fencer := &completionServiceFencer{tx: tx, err: tt.fenceError}
			result, err := NewCompletionService(fencer).Evaluate(context.Background(), completionCommandRequest())
			if !errors.Is(err, tt.want) || !result.IsZero() || writer.saveCalls != 0 {
				t.Fatalf("result/error/write = %#v / %v / %d", result, err, writer.saveCalls)
			}
			if tt.fenceError != nil && tx.loadCalls != 0 {
				t.Fatalf("epoch conflict loaded state %d times", tx.loadCalls)
			}
		})
	}
}

func TestCompletionServiceErrorsReturnZeroResult(t *testing.T) {
	baseRequest := completionCommandRequest()
	tests := []struct {
		name   string
		mutate func(*CompletionCommandRequest)
	}{
		{name: "workspace", mutate: func(request *CompletionCommandRequest) { request.WorkspaceID = "" }},
		{name: "task", mutate: func(request *CompletionCommandRequest) { request.TaskID = "" }},
		{name: "epoch", mutate: func(request *CompletionCommandRequest) { request.ExpectedRuntimeEpoch = 0 }},
		{name: "task revision", mutate: func(request *CompletionCommandRequest) { request.ExpectedTaskRevision = 0 }},
		{name: "schedule revision", mutate: func(request *CompletionCommandRequest) { request.ExpectedScheduleRevision = 0 }},
		{name: "now", mutate: func(request *CompletionCommandRequest) { request.Now = time.Time{} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := baseRequest
			tt.mutate(&request)
			writer := &completionServiceWriter{}
			tx := &completionServiceTx{state: recurringCompletionCommandFixture(t), writer: writer}
			fencer := &completionServiceFencer{tx: tx}
			result, err := NewCompletionService(fencer).Evaluate(context.Background(), request)
			if !errors.Is(err, ErrInvalidCompletionCommand) || !result.IsZero() || fencer.calls != 0 || writer.saveCalls != 0 {
				t.Fatalf("result/error/fence/write = %#v / %v / %d / %d", result, err, fencer.calls, writer.saveCalls)
			}
		})
	}

	state := recurringCompletionCommandFixture(t)
	writer := &completionServiceWriter{saveErr: errors.New("save failed")}
	tx := &completionServiceTx{state: state, writer: writer}
	fencer := &completionServiceFencer{tx: tx}
	result, err := NewCompletionService(fencer).Evaluate(context.Background(), baseRequest)
	if err == nil || !result.IsZero() || writer.saveCalls != 1 {
		t.Fatalf("save failure result/error/write = %#v / %v / %d", result, err, writer.saveCalls)
	}

	tx = &completionServiceTx{state: state, writer: &completionServiceWriter{}, loadErr: errors.New("load failed")}
	fencer = &completionServiceFencer{tx: tx}
	result, err = NewCompletionService(fencer).Evaluate(context.Background(), baseRequest)
	if err == nil || !result.IsZero() || tx.writer.saveCalls != 0 {
		t.Fatalf("load failure result/error/write = %#v / %v / %d", result, err, tx.writer.saveCalls)
	}
}

func recurringCompletionCommandFixture(t *testing.T) RecurringCompletionCommandState {
	t.Helper()
	schedule, err := NormalizeSchedule(ScheduleInput{
		RecurrenceType: RecurrenceDaily, TimingType: TimingDate, Timezone: "Asia/Shanghai",
		StartsOn: "2026-07-01", EndsOn: "2026-07-03", Rule: json.RawMessage(`{"interval":1}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	return RecurringCompletionCommandState{
		Aggregate: TaskAggregate{
			WorkspaceID: "workspace-1", TaskID: "task-1", LifecycleStatus: TaskLifecycleActive,
			Recurring: true, Revision: 5, GenerationEnabled: true,
			Occurrences: []Occurrence{
				{WorkspaceID: "workspace-1", ID: "occ-1", TaskID: "task-1", OccurrenceKey: "2026-07-01", ExecutionStatus: ExecutionStatusDone, Revision: 2},
				{WorkspaceID: "workspace-1", ID: "occ-2", TaskID: "task-1", OccurrenceKey: "2026-07-02", ExecutionStatus: ExecutionStatusSkipped, Revision: 2},
				{WorkspaceID: "workspace-1", ID: "occ-3", TaskID: "task-1", OccurrenceKey: "2026-07-03", ExecutionStatus: ExecutionStatusCancelled, Revision: 2},
			},
		},
		ScheduleRevision:    7,
		GenerationWatermark: "2026-07-03",
		GenerationStatus:    GenerationStatusIdle,
		ScheduleVersions: []CompletionScheduleVersion{{
			Schedule: schedule, Effective: ScheduleEffectiveRange{From: "2026-07-01"},
		}},
	}
}

func completionCommandRequest() CompletionCommandRequest {
	return CompletionCommandRequest{
		WorkspaceID: "workspace-1", TaskID: "task-1", ExpectedRuntimeEpoch: 9,
		ExpectedTaskRevision: 5, ExpectedScheduleRevision: 7,
		Now: time.Date(2026, 7, 3, 16, 1, 0, 0, time.UTC),
	}
}

type completionServiceFencer struct {
	tx            *completionServiceTx
	err           error
	calls         int
	workspaceID   string
	expectedEpoch int64
	inside        bool
}

func (fencer *completionServiceFencer) BeginFencedCompletionWrite(_ context.Context, workspaceID string, expectedEpoch int64, callback func(CompletionCommandTx) error) error {
	fencer.calls++
	fencer.workspaceID = workspaceID
	fencer.expectedEpoch = expectedEpoch
	if fencer.err != nil {
		return fencer.err
	}
	fencer.inside = true
	fencer.tx.fencer = fencer
	err := callback(fencer.tx)
	fencer.inside = false
	return err
}

type completionServiceTx struct {
	fencer          *completionServiceFencer
	state           RecurringCompletionCommandState
	loadErr         error
	writer          *completionServiceWriter
	loadCalls       int
	loadInsideFence bool
}

func (tx *completionServiceTx) LoadRecurringCompletionState(_ context.Context, _ string) (RecurringCompletionCommandState, error) {
	tx.loadCalls++
	tx.loadInsideFence = tx.fencer != nil && tx.fencer.inside
	return tx.state, tx.loadErr
}

func (tx *completionServiceTx) TaskDomainWriter() TaskDomainWriter {
	tx.writer.fencer = tx.fencer
	return tx.writer
}

type completionServiceWriter struct {
	fencer          *completionServiceFencer
	saveCalls       int
	saveInsideFence bool
	write           TaskAggregateWrite
	saveErr         error
}

func (*completionServiceWriter) EnsureSystemProjects(context.Context) error { return nil }
func (*completionServiceWriter) SaveProject(context.Context, ProjectWrite) error {
	return errors.New("unexpected SaveProject")
}
func (*completionServiceWriter) DeleteProject(context.Context, string, int64) error {
	return errors.New("unexpected DeleteProject")
}
func (*completionServiceWriter) CreateTaskAggregate(context.Context, TaskAggregateSnapshot) error {
	return errors.New("unexpected CreateTaskAggregate")
}
func (writer *completionServiceWriter) SaveTaskAggregate(_ context.Context, write TaskAggregateWrite) error {
	writer.saveCalls++
	writer.saveInsideFence = writer.fencer != nil && writer.fencer.inside
	writer.write = write
	return writer.saveErr
}
func (*completionServiceWriter) InstallScheduleVersion(context.Context, ScheduleVersionInstall) error {
	return errors.New("unexpected InstallScheduleVersion")
}

func TestCompletionCommandResultZeroValueIsStable(t *testing.T) {
	if !(CompletionCommandResult{}).IsZero() || !reflect.DeepEqual(CompletionCommandResult{}, CompletionCommandResult{}) {
		t.Fatal("zero completion result is not stable")
	}
}
