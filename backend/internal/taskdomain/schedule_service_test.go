package taskdomain

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestScheduleServiceRescheduleOccurrenceChangesOnlyTargetAtomically(t *testing.T) {
	state := scheduleServiceState()
	writer, fencer, reader := scheduleServiceHarness(state)
	service := NewScheduleService(fencer, reader)
	selected := -4 * 60 * 60
	request := RescheduleOccurrenceRequest{
		WorkspaceID: "workspace-1", TaskID: "task-1", OccurrenceID: "target", ExpectedRuntimeEpoch: 9,
		ExpectedTaskRevision: 5, ExpectedScheduleRevision: 7, ExpectedOccurrenceRevision: 11,
		Timing: OccurrenceTimingInput{TimingType: TimingTimeBlock, Timezone: "America/New_York", PlannedDate: "2026-07-24", LocalStartTime: "10:30", DurationMinutes: 45, SelectedOffsetSeconds: &selected},
	}

	result, err := service.RescheduleOccurrence(context.Background(), request)
	if err != nil {
		t.Fatalf("RescheduleOccurrence() unexpected error: %v", err)
	}
	if fencer.calls != 1 || fencer.workspaceID != "workspace-1" || fencer.epoch != 9 || reader.calls != 1 || !reader.insideFence {
		t.Fatalf("fenced boundary = calls:%d workspace:%q epoch:%d reads:%d inside:%t", fencer.calls, fencer.workspaceID, fencer.epoch, reader.calls, reader.insideFence)
	}
	if writer.rescheduleCalls != 1 || writer.versionCalls != 0 {
		t.Fatalf("writer calls = reschedule:%d version:%d", writer.rescheduleCalls, writer.versionCalls)
	}
	write := writer.reschedule
	if write.ExpectedTaskRevision != 5 || write.ExpectedScheduleRevision != 7 || write.ExpectedOccurrenceRevision != 11 {
		t.Fatalf("reschedule CAS = %#v", write)
	}
	if write.After.Record.ID != "target" || write.After.Record.Revision != 12 || !write.After.ManuallyOverridden || write.After.Record.GeneratedScheduleRevision != 3 {
		t.Fatalf("target after-image = %#v", write.After)
	}
	wantStart := time.Date(2026, 7, 24, 14, 30, 0, 0, time.UTC)
	if write.After.Record.PlannedStartAt == nil || !write.After.Record.PlannedStartAt.Equal(wantStart) ||
		write.After.Record.PlannedEndAt == nil || !write.After.Record.PlannedEndAt.Equal(wantStart.Add(45*time.Minute)) {
		t.Fatalf("resolved time block = %#v", write.After.Record)
	}
	if result.TaskRevision() != 5 || result.ScheduleRevision() != 7 || result.OccurrenceRevision() != 12 || len(result.Candidates()) != 1 {
		t.Fatalf("result = task:%d schedule:%d occurrence:%d candidates:%#v", result.TaskRevision(), result.ScheduleRevision(), result.OccurrenceRevision(), result.Candidates())
	}
}

func TestScheduleServiceRescheduleTerminalOccurrenceRequiresExplicitReopen(t *testing.T) {
	for _, status := range []ExecutionStatus{ExecutionStatusDone, ExecutionStatusSkipped, ExecutionStatusCancelled} {
		t.Run(string(status), func(t *testing.T) {
			state := scheduleServiceState()
			state.Occurrences[0].Record.ExecutionStatus = status
			if status == ExecutionStatusDone {
				completed := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
				state.Occurrences[0].CompletedAt = &completed
			}
			writer, fencer, reader := scheduleServiceHarness(state)
			service := NewScheduleService(fencer, reader)
			request := scheduleServiceRescheduleRequest()

			result, err := service.RescheduleOccurrence(context.Background(), request)
			if !errors.Is(err, ErrOccurrenceReopenRequired) || ErrorCodeOf(err) != ErrorCodeOccurrenceReopenRequired || !result.IsZero() {
				t.Fatalf("terminal reschedule = result:%#v err:%v code:%q", result, err, ErrorCodeOf(err))
			}
			if writer.rescheduleCalls != 0 || writer.versionCalls != 0 || fencer.calls != 1 {
				t.Fatalf("terminal command wrote state: %#v", writer)
			}
		})
	}
}

func TestScheduleServiceThisAndFutureInstallsVersionAndReconcilesOnlyMutableFuture(t *testing.T) {
	state := scheduleServiceState()
	startedAt := time.Date(2026, 7, 23, 9, 5, 0, 0, time.UTC)
	completedAt := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)
	state.Occurrences = []ScheduleOccurrenceSnapshot{
		scheduleServiceOccurrence("past", "2026-07-22", ExecutionStatusOpen, 20, false),
		scheduleServiceOccurrence("started", "2026-07-23", ExecutionStatusActive, 21, false),
		scheduleServiceOccurrence("done", "2026-07-24", ExecutionStatusDone, 22, false),
		scheduleServiceOccurrence("override", "2026-07-25", ExecutionStatusOpen, 23, true),
		scheduleServiceOccurrence("rewrite", "2026-07-26", ExecutionStatusOpen, 24, false),
		scheduleServiceOccurrence("stale", "2026-07-29", ExecutionStatusOpen, 25, false),
	}
	state.Occurrences[1].ActualStartAt = &startedAt
	state.Occurrences[2].CompletedAt = &completedAt
	writer, fencer, reader := scheduleServiceHarness(state)
	service := NewScheduleService(fencer, reader)
	request := RescheduleThisAndFutureRequest{
		WorkspaceID: "workspace-1", TaskID: "task-1", ExpectedRuntimeEpoch: 9,
		ExpectedTaskRevision: 5, ExpectedScheduleRevision: 7, EffectiveFrom: "2026-07-23", GenerateThroughExclusive: "2026-07-28",
		Schedule: ScheduleInput{RecurrenceType: RecurrenceDaily, TimingType: TimingTimeBlock, Timezone: "UTC", StartsOn: "2026-07-23", EndsOn: "2026-07-27", Rule: []byte(`{"interval":1}`), LocalStartTime: "10:00", DurationMinutes: 30},
	}

	result, err := service.RescheduleThisAndFuture(context.Background(), request)
	if err != nil {
		t.Fatalf("RescheduleThisAndFuture() unexpected error: %v", err)
	}
	if fencer.calls != 1 || reader.calls != 1 || !reader.insideFence || writer.versionCalls != 1 || writer.rescheduleCalls != 0 || writer.appliedVersionWrites != 1 {
		t.Fatalf("atomic boundary = fence:%d read:%d inside:%t version:%d applied:%d", fencer.calls, reader.calls, reader.insideFence, writer.versionCalls, writer.appliedVersionWrites)
	}
	write := writer.version
	if write.ExpectedTaskRevision != 5 || write.ExpectedScheduleRevision != 7 || write.Schedule.Revision != 8 || write.Schedule.CurrentScheduleRevision != 4 {
		t.Fatalf("schedule CAS/after-image = %#v", write)
	}
	if write.ClosedVersion.ScheduleRevision != 3 || write.ClosedVersion.EffectiveTo != "2026-07-23" ||
		write.NewVersion.ScheduleRevision != 4 || write.NewVersion.EffectiveFrom != "2026-07-23" || write.NewVersion.EffectiveTo != "" {
		t.Fatalf("version interval = old:%#v new:%#v", write.ClosedVersion, write.NewVersion)
	}
	if !reflect.DeepEqual(write.PreservedOccurrenceIDs, []string{"done", "override", "past", "started"}) {
		t.Fatalf("preserved IDs = %v", write.PreservedOccurrenceIDs)
	}
	if !reflect.DeepEqual(write.DeleteOccurrenceRevisions, map[string]int64{"stale": 25}) {
		t.Fatalf("deleted future occurrences = %#v", write.DeleteOccurrenceRevisions)
	}
	if !reflect.DeepEqual(write.ExpectedOccurrenceRevisions, map[string]int64{"rewrite": 24, "stale": 25}) {
		t.Fatalf("future occurrence CAS = %#v", write.ExpectedOccurrenceRevisions)
	}
	if len(write.UpsertOccurrences) != 2 {
		t.Fatalf("upsert occurrences = %#v", write.UpsertOccurrences)
	}
	rewritten := scheduleServiceWriteOccurrence(t, write.UpsertOccurrences, "2026-07-26")
	if rewritten.Record.ID != "rewrite" || rewritten.Record.Revision != 25 || rewritten.Record.GeneratedScheduleRevision != 4 || rewritten.ManuallyOverridden {
		t.Fatalf("rewritten occurrence = %#v", rewritten)
	}
	created := scheduleServiceWriteOccurrence(t, write.UpsertOccurrences, "2026-07-27")
	if created.Record.ID == "" || created.Record.Revision != 1 || created.Record.ExecutionStatus != ExecutionStatusOpen || created.Record.GeneratedScheduleRevision != 4 {
		t.Fatalf("created occurrence = %#v", created)
	}
	if result.TaskRevision() != 5 || result.ScheduleRevision() != 8 || result.ScheduleVersion() != 4 {
		t.Fatalf("result revisions = task:%d schedule:%d version:%d", result.TaskRevision(), result.ScheduleRevision(), result.ScheduleVersion())
	}
}

func TestScheduleServiceDSTErrorsReturnCandidatesAndNeverWrite(t *testing.T) {
	tests := []struct {
		name       string
		date       string
		clock      string
		wantCode   ErrorCode
		candidates int
	}{
		{name: "nonexistent", date: "2026-03-08", clock: "02:30", wantCode: ErrorCodeNonexistentLocalTime},
		{name: "ambiguous", date: "2026-11-01", clock: "01:30", wantCode: ErrorCodeAmbiguousLocalTime, candidates: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := scheduleServiceState()
			state.Occurrences = []ScheduleOccurrenceSnapshot{scheduleServiceOccurrence("past", "2026-01-01", ExecutionStatusOpen, 20, false)}
			writer, _, reader := scheduleServiceHarness(state)
			service := NewScheduleService(reader.fencer, reader)
			request := RescheduleThisAndFutureRequest{
				WorkspaceID: "workspace-1", TaskID: "task-1", ExpectedRuntimeEpoch: 9, ExpectedTaskRevision: 5, ExpectedScheduleRevision: 7,
				EffectiveFrom: tt.date, GenerateThroughExclusive: scheduleServiceNextDay(tt.date),
				Schedule: ScheduleInput{RecurrenceType: RecurrenceNone, TimingType: TimingTimeBlock, Timezone: "America/New_York", StartsOn: tt.date, LocalStartTime: tt.clock, DurationMinutes: 30},
			}

			result, err := service.RescheduleThisAndFuture(context.Background(), request)
			if ErrorCodeOf(err) != tt.wantCode || len(result.Candidates()) != tt.candidates {
				t.Fatalf("DST result = err:%v code:%q candidates:%#v", err, ErrorCodeOf(err), result.Candidates())
			}
			if writer.versionCalls != 0 || writer.rescheduleCalls != 0 {
				t.Fatalf("DST failure wrote state: %#v", writer)
			}
			if tt.wantCode == ErrorCodeAmbiguousLocalTime {
				request.SelectedOffsets = map[string]int{tt.date: result.Candidates()[0].OffsetSeconds}
				resolved, resolveErr := service.RescheduleThisAndFuture(context.Background(), request)
				if resolveErr != nil || writer.versionCalls != 1 || len(resolved.Candidates()) != 2 {
					t.Fatalf("selected DST candidate = result:%#v err:%v calls:%d", resolved, resolveErr, writer.versionCalls)
				}
			}
		})
	}
}

func TestScheduleServiceRevisionAndFenceConflictsAreAtomic(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*ScheduleCommandState, *scheduleServiceFencer, *scheduleServiceWriter)
		want       error
		writerCall bool
	}{
		{name: "task", mutate: func(state *ScheduleCommandState, _ *scheduleServiceFencer, _ *scheduleServiceWriter) {
			state.TaskRevision = 6
		}, want: ErrTaskRevisionConflict},
		{name: "schedule", mutate: func(state *ScheduleCommandState, _ *scheduleServiceFencer, _ *scheduleServiceWriter) {
			state.Schedule.Revision = 8
		}, want: ErrScheduleRevisionConflict},
		{name: "generator won CAS", mutate: func(_ *ScheduleCommandState, _ *scheduleServiceFencer, writer *scheduleServiceWriter) {
			writer.versionError = ErrScheduleRevisionConflict
		}, want: ErrScheduleRevisionConflict, writerCall: true},
		{name: "epoch", mutate: func(_ *ScheduleCommandState, fencer *scheduleServiceFencer, _ *scheduleServiceWriter) {
			fencer.err = ErrTaskRuntimeEpochConflict
		}, want: ErrTaskRuntimeEpochConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := scheduleServiceState()
			writer, fencer, reader := scheduleServiceHarness(state)
			tt.mutate(&reader.state, fencer, writer)
			service := NewScheduleService(fencer, reader)
			result, err := service.RescheduleThisAndFuture(context.Background(), scheduleServiceFutureRequest())
			if !errors.Is(err, tt.want) || !result.IsZero() || writer.appliedVersionWrites != 0 {
				t.Fatalf("conflict result = result:%#v err:%v applied:%d", result, err, writer.appliedVersionWrites)
			}
			if (writer.versionCalls == 1) != tt.writerCall {
				t.Fatalf("writer calls = %d, want call:%t", writer.versionCalls, tt.writerCall)
			}
		})
	}
}

type scheduleServiceWriter struct {
	rescheduleCalls      int
	versionCalls         int
	appliedVersionWrites int
	reschedule           OccurrenceRescheduleWrite
	version              ScheduleVersionChangeWrite
	rescheduleError      error
	versionError         error
}

func (writer *scheduleServiceWriter) ApplyOccurrenceReschedule(_ context.Context, write OccurrenceRescheduleWrite) error {
	writer.rescheduleCalls++
	writer.reschedule = write
	return writer.rescheduleError
}

func (writer *scheduleServiceWriter) ApplyScheduleVersionChange(_ context.Context, write ScheduleVersionChangeWrite) error {
	writer.versionCalls++
	writer.version = write
	if writer.versionError != nil {
		return writer.versionError
	}
	writer.appliedVersionWrites++
	return nil
}

type scheduleServiceTx struct{ writer ScheduleCommandWriter }

func (tx scheduleServiceTx) ScheduleCommandWriter() ScheduleCommandWriter { return tx.writer }

type scheduleServiceFencer struct {
	writer      ScheduleCommandWriter
	calls       int
	workspaceID string
	epoch       int64
	inside      bool
	err         error
}

func (fencer *scheduleServiceFencer) BeginFencedScheduleWrite(_ context.Context, workspaceID string, epoch int64, callback func(ScheduleCommandFencedTx) error) error {
	fencer.calls++
	fencer.workspaceID = workspaceID
	fencer.epoch = epoch
	if fencer.err != nil {
		return fencer.err
	}
	fencer.inside = true
	err := callback(scheduleServiceTx{writer: fencer.writer})
	fencer.inside = false
	return err
}

type scheduleServiceReader struct {
	fencer      *scheduleServiceFencer
	state       ScheduleCommandState
	calls       int
	insideFence bool
}

func (reader *scheduleServiceReader) GetScheduleCommandState(context.Context, string) (ScheduleCommandState, error) {
	reader.calls++
	reader.insideFence = reader.fencer.inside
	return reader.state, nil
}

func scheduleServiceHarness(state ScheduleCommandState) (*scheduleServiceWriter, *scheduleServiceFencer, *scheduleServiceReader) {
	writer := &scheduleServiceWriter{}
	fencer := &scheduleServiceFencer{writer: writer}
	reader := &scheduleServiceReader{fencer: fencer, state: state}
	return writer, fencer, reader
}

func scheduleServiceState() ScheduleCommandState {
	return ScheduleCommandState{
		WorkspaceID: "workspace-1", TaskID: "task-1", TaskRevision: 5,
		Schedule: ScheduleHeader{WorkspaceID: "workspace-1", TaskID: "task-1", Revision: 7, CurrentScheduleRevision: 3},
		Versions: []ScheduleVersion{{
			WorkspaceID: "workspace-1", TaskID: "task-1", ScheduleRevision: 3, EffectiveFrom: "2026-07-01",
			RecurrenceType: RecurrenceDaily, TimingType: TimingTimeBlock, Timezone: "UTC", StartsOn: "2026-07-01", EndsOn: "2026-07-31",
			RecurrenceRule: `{"interval":1}`, LocalStartTime: "09:00:00", DurationMinutes: 60,
		}},
		Occurrences: []ScheduleOccurrenceSnapshot{
			{Record: OccurrenceRecord{WorkspaceID: "workspace-1", ID: "target", TaskID: "task-1", OccurrenceKey: "2026-07-23", PlannedDate: "2026-07-23", ExecutionStatus: ExecutionStatusOpen, Revision: 11, GeneratedScheduleRevision: 3}},
			{Record: OccurrenceRecord{WorkspaceID: "workspace-1", ID: "other", TaskID: "task-1", OccurrenceKey: "2026-07-24", PlannedDate: "2026-07-24", ExecutionStatus: ExecutionStatusOpen, Revision: 12, GeneratedScheduleRevision: 3}},
		},
	}
}

func scheduleServiceOccurrence(id, date string, status ExecutionStatus, revision int64, overridden bool) ScheduleOccurrenceSnapshot {
	return ScheduleOccurrenceSnapshot{Record: OccurrenceRecord{
		WorkspaceID: "workspace-1", ID: id, TaskID: "task-1", OccurrenceKey: date, PlannedDate: date,
		ExecutionStatus: status, Revision: revision, GeneratedScheduleRevision: 3,
	}, ManuallyOverridden: overridden}
}

func scheduleServiceRescheduleRequest() RescheduleOccurrenceRequest {
	return RescheduleOccurrenceRequest{
		WorkspaceID: "workspace-1", TaskID: "task-1", OccurrenceID: "target", ExpectedRuntimeEpoch: 9,
		ExpectedTaskRevision: 5, ExpectedScheduleRevision: 7, ExpectedOccurrenceRevision: 11,
		Timing: OccurrenceTimingInput{TimingType: TimingDate, Timezone: "UTC", PlannedDate: "2026-07-25"},
	}
}

func scheduleServiceFutureRequest() RescheduleThisAndFutureRequest {
	return RescheduleThisAndFutureRequest{
		WorkspaceID: "workspace-1", TaskID: "task-1", ExpectedRuntimeEpoch: 9, ExpectedTaskRevision: 5, ExpectedScheduleRevision: 7,
		EffectiveFrom: "2026-07-23", GenerateThroughExclusive: "2026-07-28",
		Schedule: ScheduleInput{RecurrenceType: RecurrenceDaily, TimingType: TimingTimeBlock, Timezone: "UTC", StartsOn: "2026-07-23", EndsOn: "2026-07-27", Rule: []byte(`{"interval":1}`), LocalStartTime: "10:00", DurationMinutes: 30},
	}
}

func scheduleServiceWriteOccurrence(t *testing.T, occurrences []ScheduleOccurrenceSnapshot, key string) ScheduleOccurrenceSnapshot {
	t.Helper()
	for _, occurrence := range occurrences {
		if occurrence.Record.OccurrenceKey == key {
			return occurrence
		}
	}
	t.Fatalf("occurrence key %s not found in %#v", key, occurrences)
	return ScheduleOccurrenceSnapshot{}
}

func scheduleServiceNextDay(date string) string {
	parsed, _ := time.Parse("2006-01-02", date)
	return parsed.AddDate(0, 0, 1).Format("2006-01-02")
}
