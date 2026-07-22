package contracttest

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/taskdomain"
)

type TaskDomainV2CompletionFixture struct {
	DB             *sql.DB
	Dialect        TaskDomainV2Dialect
	Writer         storage.TenantFencedWriter
	Fencer         taskdomain.CompletionCommandFencer
	ScheduleFencer taskdomain.ScheduleCommandFencer
}

func RunTaskDomainV2CompletionSuite(t *testing.T, fixture TaskDomainV2CompletionFixture) {
	t.Helper()
	ctx := context.Background()
	const workspaceID = "completion-w1"
	mustExec(t, fixture.DB, `INSERT INTO tenant_workspaces(workspace_id) VALUES('completion-w1')`)
	if err := fixture.Writer.BeginFencedWrite(ctx, workspaceID, 1, func(tx storage.TenantWriteTx) error {
		if err := tx.TaskDomainWriter().EnsureSystemProjects(ctx); err != nil {
			return err
		}
		return tx.TaskDomainWriter().CreateTaskAggregate(ctx, completionContractAggregate(workspaceID, "completion-main"))
	}); err != nil {
		t.Fatal(err)
	}
	completionContractReady(t, fixture, "completion-main")

	t.Run("loads_complete_recurring_state_in_transaction", func(t *testing.T) {
		var state taskdomain.RecurringCompletionCommandState
		err := fixture.Fencer.BeginFencedCompletionWrite(ctx, workspaceID, 1, func(tx taskdomain.CompletionCommandTx) error {
			var err error
			state, err = tx.LoadRecurringCompletionState(ctx, "completion-main")
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		if state.Aggregate.WorkspaceID != workspaceID || state.Aggregate.TaskID != "completion-main" || !state.Aggregate.Recurring ||
			state.Aggregate.Revision != 1 || state.ScheduleRevision != 1 || state.GenerationWatermark != "2026-07-03" ||
			state.GenerationStatus != taskdomain.GenerationStatusIdle || state.RetryPendingJobs != 0 || state.FailedJobs != 0 {
			t.Fatalf("completion header state=%#v", state)
		}
		if len(state.ScheduleVersions) != 2 || state.ScheduleVersions[0].Effective.From != "2026-07-01" ||
			state.ScheduleVersions[0].Effective.To != "2026-07-02" || state.ScheduleVersions[1].Effective.From != "2026-07-02" ||
			state.ScheduleVersions[1].Effective.To != "" || state.ScheduleVersions[1].Schedule.Rule == nil ||
			state.ScheduleVersions[1].Schedule.Rule.Interval != 1 || state.ScheduleVersions[1].Schedule.Timezone != "Asia/Shanghai" ||
			state.ScheduleVersions[1].Schedule.EndsOn != "2026-07-03" {
			t.Fatalf("completion versions=%#v", state.ScheduleVersions)
		}
		if len(state.Aggregate.Occurrences) != 3 || state.Aggregate.Occurrences[0].OccurrenceKey != "2026-07-01" ||
			state.Aggregate.Occurrences[0].ExecutionStatus != taskdomain.ExecutionStatusDone ||
			state.Aggregate.Occurrences[1].ExecutionStatus != taskdomain.ExecutionStatusSkipped ||
			state.Aggregate.Occurrences[2].ExecutionStatus != taskdomain.ExecutionStatusCancelled {
			t.Fatalf("completion occurrences=%#v", state.Aggregate.Occurrences)
		}

		mustExec(t, fixture.DB, `UPDATE domain_task_schedules_v2 SET generation_status='retry_pending',generation_error='temporary',generation_retry_at=CURRENT_TIMESTAMP,generation_retry_pending_jobs=2,generation_failed_jobs=3 WHERE workspace_id='completion-w1' AND task_id='completion-main'`)
		err = fixture.Fencer.BeginFencedCompletionWrite(ctx, workspaceID, 1, func(tx taskdomain.CompletionCommandTx) error {
			var err error
			state, err = tx.LoadRecurringCompletionState(ctx, "completion-main")
			return err
		})
		if err != nil || state.GenerationStatus != taskdomain.GenerationStatusRetryPending || state.RetryPendingJobs != 2 || state.FailedJobs != 3 {
			t.Fatalf("generation jobs state=%#v err=%v", state, err)
		}
		mustExec(t, fixture.DB, `UPDATE domain_task_schedules_v2 SET generation_status='idle',generation_error=NULL,generation_retry_at=NULL,generation_retry_pending_jobs=0,generation_failed_jobs=0 WHERE workspace_id='completion-w1' AND task_id='completion-main'`)
	})

	service := taskdomain.NewCompletionService(fixture.Fencer)
	now := time.Date(2026, 7, 3, 16, 1, 0, 0, time.UTC)
	request := taskdomain.CompletionCommandRequest{WorkspaceID: workspaceID, TaskID: "completion-main", ExpectedRuntimeEpoch: 1, ExpectedTaskRevision: 1, ExpectedScheduleRevision: 1, Now: now}
	completed, err := service.Evaluate(ctx, request)
	if err != nil || !completed.Changed() || completed.LifecycleStatus() != taskdomain.TaskLifecycleCompleted || completed.TaskRevision() != 2 || completed.ScheduleRevision() != 1 {
		t.Fatalf("completion result=%#v err=%v", completed, err)
	}
	assertCompletionTask(t, fixture.DB, "completion-main", "completed", 2)

	stableRequest := request
	stableRequest.ExpectedTaskRevision = 2
	stable, err := service.Evaluate(ctx, stableRequest)
	if err != nil || stable.Changed() || stable.LifecycleStatus() != taskdomain.TaskLifecycleCompleted || stable.TaskRevision() != 2 {
		t.Fatalf("stable completion result=%#v err=%v", stable, err)
	}
	assertCompletionTask(t, fixture.DB, "completion-main", "completed", 2)

	mustExec(t, fixture.DB, `UPDATE domain_task_occurrences_v2 SET execution_status='open',completed_at=NULL,revision=revision+1 WHERE workspace_id='completion-w1' AND id='completion-main-occ-1'`)
	reactivated, err := service.Evaluate(ctx, stableRequest)
	if err != nil || !reactivated.Changed() || reactivated.LifecycleStatus() != taskdomain.TaskLifecycleActive || reactivated.TaskRevision() != 3 {
		t.Fatalf("reactivation result=%#v err=%v", reactivated, err)
	}
	assertCompletionTask(t, fixture.DB, "completion-main", "active", 3)
	noChangeRequest := stableRequest
	noChangeRequest.ExpectedTaskRevision = 3
	noChange, err := service.Evaluate(ctx, noChangeRequest)
	if err != nil || noChange.Changed() || noChange.LifecycleStatus() != taskdomain.TaskLifecycleActive || noChange.TaskRevision() != 3 {
		t.Fatalf("active no-change result=%#v err=%v", noChange, err)
	}
	assertCompletionTask(t, fixture.DB, "completion-main", "active", 3)

	t.Run("stale_epoch_task_schedule_and_callback_failure_are_atomic", func(t *testing.T) {
		for _, test := range []struct {
			name string
			req  taskdomain.CompletionCommandRequest
			want error
		}{
			{name: "epoch", req: taskdomain.CompletionCommandRequest{WorkspaceID: workspaceID, TaskID: "completion-main", ExpectedRuntimeEpoch: 2, ExpectedTaskRevision: 3, ExpectedScheduleRevision: 1, Now: now}, want: storage.ErrTenantEpochMismatch},
			{name: "task", req: taskdomain.CompletionCommandRequest{WorkspaceID: workspaceID, TaskID: "completion-main", ExpectedRuntimeEpoch: 1, ExpectedTaskRevision: 2, ExpectedScheduleRevision: 1, Now: now}, want: taskdomain.ErrTaskRevisionConflict},
			{name: "schedule", req: taskdomain.CompletionCommandRequest{WorkspaceID: workspaceID, TaskID: "completion-main", ExpectedRuntimeEpoch: 1, ExpectedTaskRevision: 3, ExpectedScheduleRevision: 2, Now: now}, want: taskdomain.ErrScheduleRevisionConflict},
		} {
			t.Run(test.name, func(t *testing.T) {
				result, err := service.Evaluate(ctx, test.req)
				if !errors.Is(err, test.want) || !result.IsZero() {
					t.Fatalf("stale result=%#v err=%v want=%v", result, err, test.want)
				}
				assertCompletionTask(t, fixture.DB, "completion-main", "active", 3)
			})
		}

		rollbackErr := errors.New("completion rollback")
		err := fixture.Fencer.BeginFencedCompletionWrite(ctx, workspaceID, 1, func(tx taskdomain.CompletionCommandTx) error {
			state, err := tx.LoadRecurringCompletionState(ctx, "completion-main")
			if err != nil {
				return err
			}
			next := state.Aggregate
			next.LifecycleStatus = taskdomain.TaskLifecycleCompleted
			next.Revision++
			if err := tx.TaskDomainWriter().SaveTaskAggregate(ctx, taskdomain.TaskAggregateWrite{Aggregate: next, ExpectedRevisions: taskdomain.AggregateExpectedRevisions{Task: state.Aggregate.Revision, Occurrences: map[string]int64{}}, ExpectedScheduleRevision: state.ScheduleRevision}); err != nil {
				return err
			}
			return rollbackErr
		})
		if !errors.Is(err, rollbackErr) {
			t.Fatalf("callback error=%v", err)
		}
		assertCompletionTask(t, fixture.DB, "completion-main", "active", 3)
	})

	t.Run("closed_completion_transaction_is_unusable", func(t *testing.T) {
		var captured taskdomain.CompletionCommandTx
		if err := fixture.Fencer.BeginFencedCompletionWrite(ctx, workspaceID, 1, func(tx taskdomain.CompletionCommandTx) error {
			captured = tx
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := captured.LoadRecurringCompletionState(ctx, "completion-main"); !errors.Is(err, storage.ErrTenantWriteTxClosed) {
			t.Fatalf("closed load error=%v", err)
		}
		if err := captured.TaskDomainWriter().SaveTaskAggregate(ctx, taskdomain.TaskAggregateWrite{}); !errors.Is(err, storage.ErrTenantWriteTxClosed) {
			t.Fatalf("closed writer error=%v", err)
		}
	})

	t.Run("completion_serializes_occurrence_reopen_and_loser_rolls_back", func(t *testing.T) {
		if err := fixture.Writer.BeginFencedWrite(ctx, workspaceID, 1, func(tx storage.TenantWriteTx) error {
			return tx.TaskDomainWriter().CreateTaskAggregate(ctx, completionContractAggregate(workspaceID, "completion-race"))
		}); err != nil {
			t.Fatal(err)
		}
		completionContractReady(t, fixture, "completion-race")
		counted := make(chan struct{})
		release := make(chan struct{})
		completionResult := make(chan error, 1)
		go func() {
			completionResult <- fixture.Fencer.BeginFencedCompletionWrite(ctx, workspaceID, 1, func(tx taskdomain.CompletionCommandTx) error {
				state, err := tx.LoadRecurringCompletionState(ctx, "completion-race")
				if err != nil {
					return err
				}
				close(counted)
				<-release
				next := state.Aggregate
				next.LifecycleStatus = taskdomain.TaskLifecycleCompleted
				next.Revision++
				return tx.TaskDomainWriter().SaveTaskAggregate(ctx, taskdomain.TaskAggregateWrite{Aggregate: next, ExpectedRevisions: taskdomain.AggregateExpectedRevisions{Task: 1, Occurrences: map[string]int64{}}, ExpectedScheduleRevision: 1})
			})
		}()
		<-counted
		completedAt := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
		currentOccurrence := taskdomain.Occurrence{WorkspaceID: workspaceID, ID: "completion-race-occ-1", TaskID: "completion-race", OccurrenceKey: "2026-07-01", ExecutionStatus: taskdomain.ExecutionStatusDone, CompletedAt: &completedAt, Revision: 1}
		reopened, log, err := taskdomain.ReopenOccurrence(currentOccurrence, taskdomain.ExecutionTransition{LogID: "completion-race-reopen", ActorID: "user-1", At: now})
		if err != nil {
			t.Fatal(err)
		}
		reopenResult := make(chan error, 1)
		go func() {
			reopenResult <- fixture.Writer.BeginFencedWrite(ctx, workspaceID, 1, func(tx storage.TenantWriteTx) error {
				return tx.TaskDomainWriter().SaveTaskAggregate(ctx, taskdomain.TaskAggregateWrite{
					Aggregate:         taskdomain.TaskAggregate{WorkspaceID: workspaceID, TaskID: "completion-race", LifecycleStatus: taskdomain.TaskLifecycleActive, Recurring: true, Revision: 2, GenerationEnabled: true, Occurrences: []taskdomain.Occurrence{reopened}},
					ExpectedRevisions: taskdomain.AggregateExpectedRevisions{Task: 1, Occurrences: map[string]int64{"completion-race-occ-1": 1}}, ExpectedScheduleRevision: 1, ExecutionLogs: []taskdomain.ExecutionLog{log},
				})
			})
		}()
		select {
		case err := <-reopenResult:
			t.Fatalf("reopen crossed completion load/save boundary: %v", err)
		case <-time.After(150 * time.Millisecond):
		}
		close(release)
		if err := <-completionResult; err != nil {
			t.Fatalf("completion write=%v", err)
		}
		if err := <-reopenResult; !errors.Is(err, taskdomain.ErrAggregateRevisionConflict) {
			t.Fatalf("reopen loser error=%v", err)
		}
		assertCompletionTask(t, fixture.DB, "completion-race", "completed", 2)
		var status string
		var revision, logs int
		if err := fixture.DB.QueryRow(`SELECT execution_status,revision,(SELECT COUNT(*) FROM domain_task_execution_logs_v2 WHERE workspace_id='completion-w1' AND id='completion-race-reopen') FROM domain_task_occurrences_v2 WHERE workspace_id='completion-w1' AND id='completion-race-occ-1'`).Scan(&status, &revision, &logs); err != nil {
			t.Fatal(err)
		}
		if status != "done" || revision != 1 || logs != 0 {
			t.Fatalf("reopen partially committed status=%s revision=%d logs=%d", status, revision, logs)
		}
	})

	t.Run("completion_serializes_generation_and_stale_task_CAS_rolls_back", func(t *testing.T) {
		if err := fixture.Writer.BeginFencedWrite(ctx, workspaceID, 1, func(tx storage.TenantWriteTx) error {
			return tx.TaskDomainWriter().CreateTaskAggregate(ctx, completionContractAggregate(workspaceID, "completion-generation"))
		}); err != nil {
			t.Fatal(err)
		}
		completionContractReady(t, fixture, "completion-generation")
		loaded := make(chan struct{})
		release := make(chan struct{})
		completionResult := make(chan error, 1)
		go func() {
			completionResult <- fixture.Fencer.BeginFencedCompletionWrite(ctx, workspaceID, 1, func(tx taskdomain.CompletionCommandTx) error {
				state, err := tx.LoadRecurringCompletionState(ctx, "completion-generation")
				if err != nil {
					return err
				}
				close(loaded)
				<-release
				next := state.Aggregate
				next.LifecycleStatus = taskdomain.TaskLifecycleCompleted
				next.Revision++
				return tx.TaskDomainWriter().SaveTaskAggregate(ctx, taskdomain.TaskAggregateWrite{Aggregate: next, ExpectedRevisions: taskdomain.AggregateExpectedRevisions{Task: 1, Occurrences: map[string]int64{}}, ExpectedScheduleRevision: 1})
			})
		}()
		<-loaded
		generationResult := make(chan error, 1)
		go func() {
			generationResult <- fixture.ScheduleFencer.BeginFencedScheduleWrite(ctx, workspaceID, 1, func(tx taskdomain.ScheduleCommandFencedTx) error {
				return tx.ScheduleCommandWriter().ApplyScheduleVersionChange(ctx, completionGenerationChange(workspaceID, "completion-generation"))
			})
		}()
		select {
		case err := <-generationResult:
			t.Fatalf("generation crossed completion load/save boundary: %v", err)
		case <-time.After(150 * time.Millisecond):
		}
		close(release)
		if err := <-completionResult; err != nil {
			t.Fatalf("completion write=%v", err)
		}
		if err := <-generationResult; !errors.Is(err, taskdomain.ErrTaskRevisionConflict) {
			t.Fatalf("generation loser error=%v", err)
		}
		assertCompletionTask(t, fixture.DB, "completion-generation", "completed", 2)
		var scheduleRevision, versionCount, generatedCount int
		if err := fixture.DB.QueryRow(`SELECT revision,
			(SELECT COUNT(*) FROM domain_task_schedule_versions_v2 WHERE workspace_id='completion-w1' AND task_id='completion-generation'),
			(SELECT COUNT(*) FROM domain_task_occurrences_v2 WHERE workspace_id='completion-w1' AND task_id='completion-generation' AND occurrence_key='2026-07-04')
			FROM domain_task_schedules_v2 WHERE workspace_id='completion-w1' AND task_id='completion-generation'`).Scan(&scheduleRevision, &versionCount, &generatedCount); err != nil {
			t.Fatal(err)
		}
		if scheduleRevision != 1 || versionCount != 2 || generatedCount != 0 {
			t.Fatalf("generation partially committed schedule=%d versions=%d generated=%d", scheduleRevision, versionCount, generatedCount)
		}
	})
}

func completionContractAggregate(workspaceID, taskID string) taskdomain.TaskAggregateSnapshot {
	versions := []taskdomain.ScheduleVersion{
		{WorkspaceID: workspaceID, TaskID: taskID, ScheduleRevision: 1, EffectiveFrom: "2026-07-01", EffectiveTo: "2026-07-02", RecurrenceType: taskdomain.RecurrenceDaily, TimingType: taskdomain.TimingDate, Timezone: "Asia/Shanghai", StartsOn: "2026-07-01", EndsOn: "2026-07-03", RecurrenceRule: `{"interval":1}`},
		{WorkspaceID: workspaceID, TaskID: taskID, ScheduleRevision: 2, EffectiveFrom: "2026-07-02", RecurrenceType: taskdomain.RecurrenceDaily, TimingType: taskdomain.TimingDate, Timezone: "Asia/Shanghai", StartsOn: "2026-07-02", EndsOn: "2026-07-03", RecurrenceRule: `{"interval":1}`},
	}
	occurrences := []taskdomain.OccurrenceRecord{
		{WorkspaceID: workspaceID, ID: taskID + "-occ-1", TaskID: taskID, OccurrenceKey: "2026-07-01", PlannedDate: "2026-07-01", ExecutionStatus: taskdomain.ExecutionStatusOpen, Revision: 1, GeneratedScheduleRevision: 1},
		{WorkspaceID: workspaceID, ID: taskID + "-occ-2", TaskID: taskID, OccurrenceKey: "2026-07-02", PlannedDate: "2026-07-02", ExecutionStatus: taskdomain.ExecutionStatusOpen, Revision: 1, GeneratedScheduleRevision: 2},
		{WorkspaceID: workspaceID, ID: taskID + "-occ-3", TaskID: taskID, OccurrenceKey: "2026-07-03", PlannedDate: "2026-07-03", ExecutionStatus: taskdomain.ExecutionStatusOpen, Revision: 1, GeneratedScheduleRevision: 2},
	}
	return taskdomain.TaskAggregateSnapshot{
		Task:     taskdomain.TaskRecord{WorkspaceID: workspaceID, ID: taskID, ProjectID: taskdomain.PersonalProjectID, Title: taskID, LifecycleStatus: taskdomain.TaskLifecycleActive, Revision: 1},
		Schedule: taskdomain.ScheduleHeader{WorkspaceID: workspaceID, TaskID: taskID, Revision: 1, CurrentScheduleRevision: 2}, Versions: versions, Occurrences: occurrences,
	}
}

func completionContractReady(t *testing.T, fixture TaskDomainV2CompletionFixture, taskID string) {
	t.Helper()
	mustExecArgs(t, fixture.DB, `UPDATE domain_task_schedules_v2 SET generation_watermark=?,generation_status='idle',generation_retry_pending_jobs=0,generation_failed_jobs=0 WHERE workspace_id='completion-w1' AND task_id=?`, fixture.Dialect, completionContractDateValue(fixture.Dialect, "2026-07-03"), taskID)
	mustExecArgs(t, fixture.DB, `UPDATE domain_task_occurrences_v2 SET execution_status='done',completed_at=? WHERE workspace_id='completion-w1' AND id=?`, fixture.Dialect, queryContractTime(fixture.Dialect, time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)), taskID+"-occ-1")
	mustExecArgs(t, fixture.DB, `UPDATE domain_task_occurrences_v2 SET execution_status='skipped' WHERE workspace_id='completion-w1' AND id=?`, fixture.Dialect, taskID+"-occ-2")
	mustExecArgs(t, fixture.DB, `UPDATE domain_task_occurrences_v2 SET execution_status='cancelled' WHERE workspace_id='completion-w1' AND id=?`, fixture.Dialect, taskID+"-occ-3")
}

func completionGenerationChange(workspaceID, taskID string) taskdomain.ScheduleVersionChangeWrite {
	closed := taskdomain.ScheduleVersion{WorkspaceID: workspaceID, TaskID: taskID, ScheduleRevision: 2, EffectiveFrom: "2026-07-02", EffectiveTo: "2026-07-04", RecurrenceType: taskdomain.RecurrenceDaily, TimingType: taskdomain.TimingDate, Timezone: "Asia/Shanghai", StartsOn: "2026-07-02", EndsOn: "2026-07-03", RecurrenceRule: `{"interval":1}`}
	next := taskdomain.ScheduleVersion{WorkspaceID: workspaceID, TaskID: taskID, ScheduleRevision: 3, EffectiveFrom: "2026-07-04", RecurrenceType: taskdomain.RecurrenceDaily, TimingType: taskdomain.TimingDate, Timezone: "Asia/Shanghai", StartsOn: "2026-07-04", EndsOn: "2026-07-04", RecurrenceRule: `{"interval":1}`}
	return taskdomain.ScheduleVersionChangeWrite{
		WorkspaceID: workspaceID, TaskID: taskID, ExpectedTaskRevision: 1, ExpectedScheduleRevision: 1,
		Schedule:      taskdomain.ScheduleHeader{WorkspaceID: workspaceID, TaskID: taskID, Revision: 2, CurrentScheduleRevision: 3},
		ClosedVersion: closed, NewVersion: next,
		PreservedOccurrenceIDs:      []string{taskID + "-occ-1", taskID + "-occ-2", taskID + "-occ-3"},
		UpsertOccurrences:           []taskdomain.ScheduleOccurrenceSnapshot{{Record: taskdomain.OccurrenceRecord{WorkspaceID: workspaceID, ID: taskID + "-occ-4", TaskID: taskID, OccurrenceKey: "2026-07-04", PlannedDate: "2026-07-04", ExecutionStatus: taskdomain.ExecutionStatusOpen, Revision: 1, GeneratedScheduleRevision: 3}}},
		ExpectedOccurrenceRevisions: map[string]int64{}, DeleteOccurrenceRevisions: map[string]int64{},
	}
}

func completionContractDateValue(dialect TaskDomainV2Dialect, value string) any {
	if dialect == TaskDomainV2Postgres {
		parsed, _ := time.Parse("2006-01-02", value)
		return parsed
	}
	return value
}

func assertCompletionTask(t *testing.T, db *sql.DB, taskID, status string, revision int64) {
	t.Helper()
	var actualStatus string
	var actualRevision int64
	if err := db.QueryRow(`SELECT lifecycle_status,revision FROM domain_tasks_v2 WHERE workspace_id='completion-w1' AND id=?`, taskID).Scan(&actualStatus, &actualRevision); err != nil {
		// PostgreSQL drivers do not rewrite placeholders, so retry the fixed test query.
		if err := db.QueryRow(`SELECT lifecycle_status,revision FROM domain_tasks_v2 WHERE workspace_id='completion-w1' AND id=$1`, taskID).Scan(&actualStatus, &actualRevision); err != nil {
			t.Fatal(err)
		}
	}
	if actualStatus != status || actualRevision != revision {
		t.Fatalf("task %s state=%s/%d want=%s/%d", taskID, actualStatus, actualRevision, status, revision)
	}
}
