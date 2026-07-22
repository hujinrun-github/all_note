package taskdomain

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestOccurrenceServiceRecurringCommandsChangeOnlyTargetOccurrence(t *testing.T) {
	tests := []struct {
		name       string
		command    OccurrenceCommand
		from       ExecutionStatus
		want       ExecutionStatus
		reason     string
		nextAction string
	}{
		{name: "start", command: OccurrenceCommandStart, from: ExecutionStatusOpen, want: ExecutionStatusActive},
		{name: "block", command: OccurrenceCommandBlock, from: ExecutionStatusActive, want: ExecutionStatusBlocked, reason: "waiting for reviewer", nextAction: "ask reviewer"},
		{name: "unblock", command: OccurrenceCommandUnblock, from: ExecutionStatusBlocked, want: ExecutionStatusActive},
		{name: "complete", command: OccurrenceCommandComplete, from: ExecutionStatusActive, want: ExecutionStatusDone},
		{name: "skip", command: OccurrenceCommandSkip, from: ExecutionStatusOpen, want: ExecutionStatusSkipped},
		{name: "cancel", command: OccurrenceCommandCancel, from: ExecutionStatusBlocked, want: ExecutionStatusCancelled},
		{name: "reopen", command: OccurrenceCommandReopen, from: ExecutionStatusDone, want: ExecutionStatusOpen},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			current := occurrenceServiceAggregate(true, TaskLifecycleActive,
				occurrenceServiceOccurrence("target", tt.from, true, 11),
				occurrenceServiceOccurrence("other", ExecutionStatusOpen, true, 12),
			)
			writer := &occurrenceServiceWriter{}
			fencer := &occurrenceServiceFencer{writer: writer}
			reader := &occurrenceServiceReader{fencer: fencer, state: TaskAggregateState{Aggregate: current, ScheduleRevision: 7}}
			service := NewOccurrenceService(fencer, reader)
			request := occurrenceServiceRequest(tt.command, 11)
			request.BlockedReason = tt.reason
			request.NextAction = tt.nextAction

			result, err := service.Execute(context.Background(), request)
			if err != nil {
				t.Fatalf("Execute(%q) unexpected error: %v", tt.command, err)
			}
			if fencer.beginCalls != 1 || reader.calls != 1 || !reader.readInsideFence || writer.saveCalls != 1 {
				t.Fatalf("command boundary = fence:%d read:%d inside:%t save:%d", fencer.beginCalls, reader.calls, reader.readInsideFence, writer.saveCalls)
			}
			write := writer.writes[0]
			if write.Aggregate.LifecycleStatus != TaskLifecycleActive || write.Aggregate.Revision != 6 {
				t.Fatalf("recurring task changed lifecycle/revision incorrectly: %#v", write.Aggregate)
			}
			assertOccurrenceServiceState(t, write.Aggregate, "target", tt.want, 12)
			assertOccurrenceServiceState(t, write.Aggregate, "other", ExecutionStatusOpen, 12)
			if !reflect.DeepEqual(write.ExpectedRevisions.Occurrences, map[string]int64{"target": 11}) || write.ExpectedScheduleRevision != 7 || len(write.ExecutionLogs) != 1 {
				t.Fatalf("atomic write = %#v", write)
			}
			log := write.ExecutionLogs[0]
			if log.FromStatus() != tt.from || log.ToStatus() != tt.want || log.OccurrenceRevision() != 12 {
				t.Fatalf("execution log = from:%q to:%q revision:%d", log.FromStatus(), log.ToStatus(), log.OccurrenceRevision())
			}
			if result.TaskRevision() != 6 || result.ScheduleRevision() != 7 || result.OccurrenceRevision() != 12 || result.ExecutionStatus() != tt.want {
				t.Fatalf("result = task:%d schedule:%d occurrence:%d status:%q", result.TaskRevision(), result.ScheduleRevision(), result.OccurrenceRevision(), result.ExecutionStatus())
			}
			assertOccurrenceServiceAudit(t, result, tt.command)
		})
	}
}

func TestOccurrenceServiceSingleCompleteReopenAndCancelUpdateTaskAtomically(t *testing.T) {
	t.Run("complete and reopen", func(t *testing.T) {
		writer := &occurrenceServiceWriter{}
		fencer := &occurrenceServiceFencer{writer: writer}
		reader := &occurrenceServiceReader{fencer: fencer, state: TaskAggregateState{
			Aggregate:        occurrenceServiceAggregate(false, TaskLifecycleActive, occurrenceServiceOccurrence("target", ExecutionStatusOpen, false, 11)),
			ScheduleRevision: 7,
		}}
		service := NewOccurrenceService(fencer, reader)

		completed, err := service.Execute(context.Background(), occurrenceServiceRequest(OccurrenceCommandComplete, 11))
		if err != nil {
			t.Fatalf("complete unexpected error: %v", err)
		}
		if writer.writes[0].Aggregate.LifecycleStatus != TaskLifecycleCompleted || completed.TaskLifecycleStatus() != TaskLifecycleCompleted {
			t.Fatalf("single complete did not atomically complete task: %#v", writer.writes[0])
		}

		reader.state = TaskAggregateState{Aggregate: writer.writes[0].Aggregate, ScheduleRevision: 7}
		reopen := occurrenceServiceRequest(OccurrenceCommandReopen, 12)
		reopen.Expected.Task = 6
		reopened, err := service.Execute(context.Background(), reopen)
		if err != nil {
			t.Fatalf("reopen unexpected error: %v", err)
		}
		if writer.writes[1].Aggregate.LifecycleStatus != TaskLifecycleActive || reopened.TaskLifecycleStatus() != TaskLifecycleActive {
			t.Fatalf("single reopen did not atomically reactivate task: %#v", writer.writes[1])
		}
		assertOccurrenceServiceState(t, writer.writes[1].Aggregate, "target", ExecutionStatusOpen, 13)
	})

	t.Run("cancel", func(t *testing.T) {
		current := occurrenceServiceAggregate(false, TaskLifecycleActive, occurrenceServiceOccurrence("target", ExecutionStatusActive, false, 11))
		writer, fencer, reader := occurrenceServiceHarness(current)
		service := NewOccurrenceService(fencer, reader)

		result, err := service.Execute(context.Background(), occurrenceServiceRequest(OccurrenceCommandCancel, 11))
		if err != nil {
			t.Fatalf("cancel unexpected error: %v", err)
		}
		if writer.writes[0].Aggregate.LifecycleStatus != TaskLifecycleCancelled || result.TaskLifecycleStatus() != TaskLifecycleCancelled {
			t.Fatalf("single cancel did not atomically cancel task: %#v", writer.writes[0])
		}
		assertOccurrenceServiceState(t, writer.writes[0].Aggregate, "target", ExecutionStatusCancelled, 12)
	})
}

func TestOccurrenceServiceRecurringReopenReactivatesNaturallyCompletedTask(t *testing.T) {
	current := occurrenceServiceAggregate(true, TaskLifecycleCompleted,
		occurrenceServiceOccurrence("target", ExecutionStatusDone, true, 11),
		occurrenceServiceOccurrence("other", ExecutionStatusDone, true, 12),
	)
	writer, fencer, reader := occurrenceServiceHarness(current)
	service := NewOccurrenceService(fencer, reader)

	result, err := service.Execute(context.Background(), occurrenceServiceRequest(OccurrenceCommandReopen, 11))
	if err != nil {
		t.Fatalf("reopen recurring occurrence: %v", err)
	}
	if writer.saveCalls != 1 || writer.writes[0].Aggregate.LifecycleStatus != TaskLifecycleActive || result.TaskLifecycleStatus() != TaskLifecycleActive {
		t.Fatalf("reopen did not reactivate recurring task: write=%#v result=%#v", writer.writes, result)
	}
	assertOccurrenceServiceState(t, writer.writes[0].Aggregate, "target", ExecutionStatusOpen, 12)
	assertOccurrenceServiceState(t, writer.writes[0].Aggregate, "other", ExecutionStatusDone, 12)
}

func TestOccurrenceServiceBlockedMetadataAndExecutionHistoryAreAtomic(t *testing.T) {
	current := occurrenceServiceAggregate(true, TaskLifecycleActive, occurrenceServiceOccurrence("target", ExecutionStatusActive, true, 11))
	writer, fencer, reader := occurrenceServiceHarness(current)
	service := NewOccurrenceService(fencer, reader)
	block := occurrenceServiceRequest(OccurrenceCommandBlock, 11)
	block.BlockedReason = "  waiting for review  "
	block.NextAction = "  ping owner  "

	blockedResult, err := service.Execute(context.Background(), block)
	if err != nil {
		t.Fatalf("block unexpected error: %v", err)
	}
	blockedWrite := writer.writes[0]
	blocked := occurrenceServiceOccurrenceByID(t, blockedWrite.Aggregate, "target")
	if blocked.BlockedReason != "waiting for review" || blocked.NextAction != "ping owner" {
		t.Fatalf("blocked current metadata = reason:%q next:%q", blocked.BlockedReason, blocked.NextAction)
	}
	blockLog := blockedWrite.ExecutionLogs[0]
	if blockLog.BlockedReason() != "waiting for review" || blockLog.NextAction() != "ping owner" {
		t.Fatalf("blocked historical fact = reason:%q next:%q", blockLog.BlockedReason(), blockLog.NextAction())
	}
	if blockedResult.ExecutionLog().ID() != blockLog.ID() {
		t.Fatalf("result log = %q, want %q", blockedResult.ExecutionLog().ID(), blockLog.ID())
	}

	reader.state = TaskAggregateState{Aggregate: blockedWrite.Aggregate, ScheduleRevision: 7}
	unblock := occurrenceServiceRequest(OccurrenceCommandUnblock, 12)
	unblock.Expected.Task = 6
	if _, err := service.Execute(context.Background(), unblock); err != nil {
		t.Fatalf("unblock unexpected error: %v", err)
	}
	unblockedWrite := writer.writes[1]
	unblocked := occurrenceServiceOccurrenceByID(t, unblockedWrite.Aggregate, "target")
	if unblocked.BlockedReason != "" || unblocked.NextAction != "" {
		t.Fatalf("unblocked current metadata was not cleared: %#v", unblocked)
	}
	if blockLog.BlockedReason() != "waiting for review" || blockLog.NextAction() != "ping owner" {
		t.Fatalf("earlier immutable block fact changed after unblock: %#v", blockLog)
	}
	if unblockedWrite.ExecutionLogs[0].FromStatus() != ExecutionStatusBlocked || unblockedWrite.ExecutionLogs[0].ToStatus() != ExecutionStatusActive {
		t.Fatalf("unblock log = %#v", unblockedWrite.ExecutionLogs[0])
	}
}

func TestOccurrenceServiceBlockRequiresReasonAndNextActionAndSkipRejectsSingleTask(t *testing.T) {
	tests := []struct {
		name    string
		current TaskAggregate
		request OccurrenceCommandRequest
		want    error
	}{
		{
			name: "block missing reason", current: occurrenceServiceAggregate(true, TaskLifecycleActive, occurrenceServiceOccurrence("target", ExecutionStatusActive, true, 11)),
			request: func() OccurrenceCommandRequest {
				r := occurrenceServiceRequest(OccurrenceCommandBlock, 11)
				r.NextAction = "retry"
				return r
			}(), want: ErrBlockedDetailsRequired,
		},
		{
			name: "block missing next action", current: occurrenceServiceAggregate(true, TaskLifecycleActive, occurrenceServiceOccurrence("target", ExecutionStatusActive, true, 11)),
			request: func() OccurrenceCommandRequest {
				r := occurrenceServiceRequest(OccurrenceCommandBlock, 11)
				r.BlockedReason = "dependency"
				return r
			}(), want: ErrBlockedDetailsRequired,
		},
		{
			name: "single task cannot skip", current: occurrenceServiceAggregate(false, TaskLifecycleActive, occurrenceServiceOccurrence("target", ExecutionStatusOpen, false, 11)),
			request: occurrenceServiceRequest(OccurrenceCommandSkip, 11), want: ErrSingleOccurrenceCannotSkip,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writer, fencer, reader := occurrenceServiceHarness(tt.current)
			result, err := NewOccurrenceService(fencer, reader).Execute(context.Background(), tt.request)
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
			if writer.saveCalls != 0 || !result.IsZero() {
				t.Fatalf("rejected command saved or returned result: saves=%d result=%#v", writer.saveCalls, result)
			}
		})
	}
}

func TestOccurrenceServiceCancelAndReopenTerminalStateMatrix(t *testing.T) {
	now := occurrenceServiceTime()
	for _, tt := range []struct {
		command OccurrenceCommand
		from    ExecutionStatus
		want    ExecutionStatus
		valid   bool
	}{
		{OccurrenceCommandCancel, ExecutionStatusOpen, ExecutionStatusCancelled, true},
		{OccurrenceCommandCancel, ExecutionStatusActive, ExecutionStatusCancelled, true},
		{OccurrenceCommandCancel, ExecutionStatusBlocked, ExecutionStatusCancelled, true},
		{OccurrenceCommandCancel, ExecutionStatusDone, "", false},
		{OccurrenceCommandCancel, ExecutionStatusSkipped, "", false},
		{OccurrenceCommandCancel, ExecutionStatusCancelled, "", false},
		{OccurrenceCommandReopen, ExecutionStatusDone, ExecutionStatusOpen, true},
		{OccurrenceCommandReopen, ExecutionStatusSkipped, ExecutionStatusOpen, true},
		{OccurrenceCommandReopen, ExecutionStatusCancelled, ExecutionStatusOpen, true},
		{OccurrenceCommandReopen, ExecutionStatusOpen, "", false},
		{OccurrenceCommandReopen, ExecutionStatusActive, "", false},
		{OccurrenceCommandReopen, ExecutionStatusBlocked, "", false},
	} {
		name := string(tt.command) + "_from_" + string(tt.from)
		t.Run(name, func(t *testing.T) {
			current := occurrenceServiceAggregate(true, TaskLifecycleActive, occurrenceServiceOccurrenceAt("target", tt.from, true, 11, now))
			writer, fencer, reader := occurrenceServiceHarness(current)
			result, err := NewOccurrenceService(fencer, reader).Execute(context.Background(), occurrenceServiceRequest(tt.command, 11))
			if tt.valid {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				assertOccurrenceServiceState(t, writer.writes[0].Aggregate, "target", tt.want, 12)
				return
			}
			if !errors.Is(err, ErrInvalidOccurrenceTransition) || writer.saveCalls != 0 || !result.IsZero() {
				t.Fatalf("invalid transition = err:%v saves:%d result:%#v", err, writer.saveCalls, result)
			}
		})
	}
}

func TestOccurrenceServiceConflictsAreDistinctAndNeverSave(t *testing.T) {
	current := occurrenceServiceAggregate(true, TaskLifecycleActive, occurrenceServiceOccurrence("target", ExecutionStatusOpen, true, 11))
	tests := []struct {
		name       string
		fenceError error
		mutate     func(*OccurrenceCommandRequest)
		want       error
	}{
		{name: "runtime epoch", fenceError: ErrTaskRuntimeEpochConflict, want: ErrTaskRuntimeEpochConflict},
		{name: "task revision", mutate: func(r *OccurrenceCommandRequest) { r.Expected.Task = 4 }, want: ErrTaskRevisionConflict},
		{name: "schedule revision", mutate: func(r *OccurrenceCommandRequest) { r.Expected.Schedule = 6 }, want: ErrScheduleRevisionConflict},
		{name: "occurrence revision", mutate: func(r *OccurrenceCommandRequest) { r.Expected.Occurrence = 10 }, want: ErrOccurrenceRevisionConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writer, fencer, reader := occurrenceServiceHarness(current)
			fencer.beginError = tt.fenceError
			request := occurrenceServiceRequest(OccurrenceCommandStart, 11)
			if tt.mutate != nil {
				tt.mutate(&request)
			}
			result, err := NewOccurrenceService(fencer, reader).Execute(context.Background(), request)
			if !errors.Is(err, tt.want) || writer.saveCalls != 0 || !result.IsZero() {
				t.Fatalf("conflict = err:%v saves:%d result:%#v", err, writer.saveCalls, result)
			}
		})
	}
}

func TestOccurrenceServiceFailureNeverReturnsAuditOrOrphanLog(t *testing.T) {
	current := occurrenceServiceAggregate(true, TaskLifecycleActive, occurrenceServiceOccurrence("target", ExecutionStatusOpen, true, 11))
	writer, fencer, reader := occurrenceServiceHarness(current)
	writer.saveError = errors.New("save failed")

	result, err := NewOccurrenceService(fencer, reader).Execute(context.Background(), occurrenceServiceRequest(OccurrenceCommandStart, 11))
	if err == nil || err.Error() != "save failed" || writer.saveCalls != 1 || !result.IsZero() {
		t.Fatalf("writer failure = err:%v saves:%d result:%#v", err, writer.saveCalls, result)
	}
}

func occurrenceServiceRequest(command OccurrenceCommand, occurrenceRevision int64) OccurrenceCommandRequest {
	return OccurrenceCommandRequest{
		WorkspaceID: "workspace-1", TaskID: "task-1", OccurrenceID: "target", Command: command,
		ExpectedRuntimeEpoch: 9,
		Expected:             OccurrenceCommandExpectedRevisions{Task: 5, Schedule: 7, Occurrence: occurrenceRevision},
		CommandID:            "command-1", ActorID: "user-1", At: occurrenceServiceTime(),
	}
}

func occurrenceServiceHarness(current TaskAggregate) (*occurrenceServiceWriter, *occurrenceServiceFencer, *occurrenceServiceReader) {
	writer := &occurrenceServiceWriter{}
	fencer := &occurrenceServiceFencer{writer: writer}
	reader := &occurrenceServiceReader{fencer: fencer, state: TaskAggregateState{Aggregate: current, ScheduleRevision: 7}}
	return writer, fencer, reader
}

func occurrenceServiceAggregate(recurring bool, lifecycle TaskLifecycleStatus, occurrences ...Occurrence) TaskAggregate {
	return TaskAggregate{
		WorkspaceID: "workspace-1", TaskID: "task-1", LifecycleStatus: lifecycle,
		Recurring: recurring, Revision: 5, GenerationEnabled: recurring, Occurrences: occurrences,
	}
}

func occurrenceServiceOccurrence(id string, status ExecutionStatus, recurring bool, revision int64) Occurrence {
	return occurrenceServiceOccurrenceAt(id, status, recurring, revision, occurrenceServiceTime())
}

func occurrenceServiceOccurrenceAt(id string, status ExecutionStatus, recurring bool, revision int64, at time.Time) Occurrence {
	occurrence := Occurrence{
		WorkspaceID: "workspace-1", ID: id, TaskID: "task-1", OccurrenceKey: id,
		ExecutionStatus: status, Recurring: recurring, Revision: revision,
	}
	if status == ExecutionStatusActive || status == ExecutionStatusBlocked {
		startedAt := at.Add(-time.Hour)
		occurrence.ActualStartAt = &startedAt
	}
	if status == ExecutionStatusBlocked {
		occurrence.BlockedReason = "dependency"
		occurrence.NextAction = "retry"
	}
	if status == ExecutionStatusDone {
		completedAt := at.Add(-time.Minute)
		occurrence.CompletedAt = &completedAt
	}
	return occurrence
}

func occurrenceServiceTime() time.Time {
	return time.Date(2026, 7, 22, 11, 0, 0, 0, time.UTC)
}

func occurrenceServiceOccurrenceByID(t *testing.T, aggregate TaskAggregate, occurrenceID string) Occurrence {
	t.Helper()
	for _, occurrence := range aggregate.Occurrences {
		if occurrence.ID == occurrenceID {
			return occurrence
		}
	}
	t.Fatalf("occurrence %q not found", occurrenceID)
	return Occurrence{}
}

func assertOccurrenceServiceState(t *testing.T, aggregate TaskAggregate, occurrenceID string, status ExecutionStatus, revision int64) {
	t.Helper()
	occurrence := occurrenceServiceOccurrenceByID(t, aggregate, occurrenceID)
	if occurrence.ExecutionStatus != status || occurrence.Revision != revision {
		t.Fatalf("occurrence %q = (%q,%d), want (%q,%d)", occurrenceID, occurrence.ExecutionStatus, occurrence.Revision, status, revision)
	}
}

func assertOccurrenceServiceAudit(t *testing.T, result OccurrenceCommandResult, command OccurrenceCommand) {
	t.Helper()
	audit := result.Audit()
	if audit.Command() != command || audit.CommandID() != "command-1" || audit.TaskID() != "task-1" || audit.OccurrenceID() != "target" || audit.ActorID() != "user-1" || !audit.CreatedAt().Equal(occurrenceServiceTime()) {
		t.Fatalf("audit = command:%q id:%q task:%q occurrence:%q actor:%q at:%v", audit.Command(), audit.CommandID(), audit.TaskID(), audit.OccurrenceID(), audit.ActorID(), audit.CreatedAt())
	}
}

type occurrenceServiceFencer struct {
	writer        *occurrenceServiceWriter
	beginError    error
	beginCalls    int
	workspaceID   string
	expectedEpoch int64
	inCallback    bool
}

func (f *occurrenceServiceFencer) BeginFencedWrite(_ context.Context, workspaceID string, expectedEpoch int64, callback func(TaskDomainFencedTx) error) error {
	f.beginCalls++
	f.workspaceID = workspaceID
	f.expectedEpoch = expectedEpoch
	if f.beginError != nil {
		return f.beginError
	}
	f.inCallback = true
	err := callback(occurrenceServiceTx{writer: f.writer})
	f.inCallback = false
	return err
}

type occurrenceServiceTx struct{ writer TaskDomainWriter }

func (tx occurrenceServiceTx) TaskDomainWriter() TaskDomainWriter { return tx.writer }

type occurrenceServiceReader struct {
	fencer          *occurrenceServiceFencer
	state           TaskAggregateState
	err             error
	calls           int
	readInsideFence bool
}

func (r *occurrenceServiceReader) GetTaskAggregateState(_ context.Context, _ string) (TaskAggregateState, error) {
	r.calls++
	r.readInsideFence = r.fencer.inCallback
	return r.state, r.err
}

type occurrenceServiceWriter struct {
	writes     []TaskAggregateWrite
	saveCalls  int
	saveError  error
	createCall int
}

func (w *occurrenceServiceWriter) EnsureSystemProjects(context.Context) error         { return nil }
func (w *occurrenceServiceWriter) SaveProject(context.Context, ProjectWrite) error    { return nil }
func (w *occurrenceServiceWriter) DeleteProject(context.Context, string, int64) error { return nil }
func (w *occurrenceServiceWriter) InstallScheduleVersion(context.Context, ScheduleVersionInstall) error {
	return nil
}
func (w *occurrenceServiceWriter) CreateTaskAggregate(context.Context, TaskAggregateSnapshot) error {
	w.createCall++
	return nil
}
func (w *occurrenceServiceWriter) SaveTaskAggregate(_ context.Context, write TaskAggregateWrite) error {
	w.saveCalls++
	w.writes = append(w.writes, write)
	return w.saveError
}
