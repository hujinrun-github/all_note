package contracttest

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/taskdomain"
)

type TaskDomainV2GeneratorFixture struct {
	DB             *sql.DB
	Dialect        TaskDomainV2Dialect
	Writer         storage.TenantFencedWriter
	Fencer         taskdomain.GenerationFencer
	ScheduleFencer taskdomain.ScheduleCommandFencer
}

func RunTaskDomainV2GeneratorSuite(t *testing.T, fixture TaskDomainV2GeneratorFixture) {
	t.Helper()
	ctx := context.Background()
	const workspaceID = "generation-w1"
	mustExec(t, fixture.DB, `INSERT INTO tenant_workspaces(workspace_id) VALUES('generation-w1')`)
	if err := fixture.Writer.BeginFencedWrite(ctx, workspaceID, 1, func(tx storage.TenantWriteTx) error {
		if err := tx.TaskDomainWriter().EnsureSystemProjects(ctx); err != nil {
			return err
		}
		for _, snapshot := range []taskdomain.TaskAggregateSnapshot{
			generatorContractAggregate(workspaceID, "generation-date", taskdomain.TimingDate),
			generatorContractAggregate(workspaceID, "generation-time", taskdomain.TimingTimeBlock),
			generatorContractAggregate(workspaceID, "generation-incomplete", taskdomain.TimingDate),
			generatorContractAggregate(workspaceID, "generation-rollback", taskdomain.TimingDate),
			generatorContractAggregate(workspaceID, "generation-idempotent", taskdomain.TimingDate),
		} {
			if err := tx.TaskDomainWriter().CreateTaskAggregate(ctx, snapshot); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	var initialTargets []taskdomain.GenerationTargetState
	if err := fixture.Fencer.BeginGenerationWrite(ctx, workspaceID, 1, func(reader taskdomain.GenerationStateReader, writer taskdomain.GenerationWriter) error {
		var err error
		initialTargets, err = reader.ListGenerationTargets(ctx)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	dateTarget := generatorTarget(t, initialTargets, "generation-date")
	timeTarget := generatorTarget(t, initialTargets, "generation-time")
	if dateTarget.ScheduleRevision != 1 || dateTarget.GenerationWatermark != "" || !dateTarget.GenerationEnabled ||
		len(dateTarget.Versions) != 1 || dateTarget.Versions[0].Revision != 1 || dateTarget.Versions[0].Effective.From != "2026-03-07" ||
		dateTarget.Versions[0].Schedule.TimingType != taskdomain.TimingDate || !reflect.DeepEqual(dateTarget.ExistingOccurrenceKeys, []string{"2026-03-07"}) {
		t.Fatalf("date generation target=%#v", dateTarget)
	}
	if timeTarget.Versions[0].Schedule.TimingType != taskdomain.TimingTimeBlock || timeTarget.Versions[0].Schedule.LocalStartTime != "03:30:00" || timeTarget.Versions[0].Schedule.DurationMinutes != 45 {
		t.Fatalf("time generation target=%#v", timeTarget)
	}

	called := false
	err := fixture.Fencer.BeginGenerationWrite(ctx, workspaceID, 2, func(taskdomain.GenerationStateReader, taskdomain.GenerationWriter) error {
		called = true
		return nil
	})
	if !errors.Is(err, storage.ErrTenantEpochMismatch) || called {
		t.Fatalf("stale generation epoch error=%v callback=%t", err, called)
	}

	t.Run("watermark_requires_all_expected_keys_and_callback_rolls_back", func(t *testing.T) {
		err := fixture.Fencer.BeginGenerationWrite(ctx, workspaceID, 1, func(_ taskdomain.GenerationStateReader, writer taskdomain.GenerationWriter) error {
			return writer.CompleteGeneration(ctx, taskdomain.GenerationCompletion{WorkspaceID: workspaceID, TaskID: "generation-incomplete", ExpectedScheduleRevision: 1, GenerationWatermark: "2026-03-09", Status: taskdomain.GenerationStatusIdle})
		})
		if !errors.Is(err, taskdomain.ErrInvalidGenerationWorker) {
			t.Fatalf("incomplete watermark error=%v", err)
		}
		assertGenerationHeader(t, fixture, "generation-incomplete", "", "idle", "", 0, 0)

		rollbackErr := errors.New("generation rollback")
		err = fixture.Fencer.BeginGenerationWrite(ctx, workspaceID, 1, func(_ taskdomain.GenerationStateReader, writer taskdomain.GenerationWriter) error {
			if err := writer.InsertMissingOccurrences(ctx, taskdomain.GenerationInsert{WorkspaceID: workspaceID, TaskID: "generation-rollback", ExpectedScheduleRevision: 1, Occurrences: []taskdomain.GenerationOccurrence{generatorDateOccurrence(workspaceID, "generation-rollback", "2026-03-08")}}); err != nil {
				return err
			}
			return rollbackErr
		})
		if !errors.Is(err, rollbackErr) {
			t.Fatalf("rollback error=%v", err)
		}
		assertGenerationOccurrenceCount(t, fixture.DB, "generation-rollback", 1)
		assertGenerationHeader(t, fixture, "generation-rollback", "", "idle", "", 0, 0)
	})

	t.Run("insert_missing_occurrences_is_provider_idempotent", func(t *testing.T) {
		insert := taskdomain.GenerationInsert{WorkspaceID: workspaceID, TaskID: "generation-idempotent", ExpectedScheduleRevision: 1, Occurrences: []taskdomain.GenerationOccurrence{generatorDateOccurrence(workspaceID, "generation-idempotent", "2026-03-08")}}
		for attempt := 0; attempt < 2; attempt++ {
			if err := fixture.Fencer.BeginGenerationWrite(ctx, workspaceID, 1, func(_ taskdomain.GenerationStateReader, writer taskdomain.GenerationWriter) error {
				return writer.InsertMissingOccurrences(ctx, insert)
			}); err != nil {
				t.Fatalf("idempotent insert attempt %d: %v", attempt+1, err)
			}
		}
		assertGenerationOccurrenceCount(t, fixture.DB, "generation-idempotent", 2)
		assertGenerationHeader(t, fixture, "generation-idempotent", "", "running", "", 0, 0)
	})

	now := time.Date(2026, 3, 7, 5, 0, 0, 0, time.UTC)
	worker := taskdomain.NewGenerationWorker(
		generatorClaimSource{claim: taskdomain.GenerationWorkspaceClaim{ClaimID: "generation-claim", WorkspaceID: workspaceID, CreatedEpoch: 9}},
		generatorRuntimeResolver{snapshot: taskdomain.GenerationRuntimeSnapshot{WorkspaceID: workspaceID, Epoch: 1, Fencer: fixture.Fencer}},
	)
	results, err := worker.RunBatch(ctx, taskdomain.GenerationBatchRequest{Limit: 1, Now: now})
	if err != nil || len(results) != 1 || results[0].RuntimeEpoch != 1 || results[0].CreatedEpoch != 9 || results[0].Status != taskdomain.GenerationStatusIdle {
		t.Fatalf("generation worker result=%#v err=%v", results, err)
	}
	assertGenerationOccurrenceCount(t, fixture.DB, "generation-date", 3)
	assertGenerationOccurrenceCount(t, fixture.DB, "generation-time", 3)
	assertGenerationHeader(t, fixture, "generation-date", "2026-06-05", "idle", "", 0, 0)
	assertGenerationTimeBlocks(t, fixture)

	second, err := worker.RunBatch(ctx, taskdomain.GenerationBatchRequest{Limit: 1, Now: now})
	if err != nil || len(second) != 1 || second[0].Inserted != 0 {
		t.Fatalf("idempotent generation result=%#v err=%v", second, err)
	}
	assertGenerationOccurrenceCount(t, fixture.DB, "generation-date", 3)

	t.Run("schedule_CAS_and_generation_status_combinations", func(t *testing.T) {
		staleOccurrence := generatorDateOccurrence(workspaceID, "generation-date", "2026-03-10")
		err := fixture.Fencer.BeginGenerationWrite(ctx, workspaceID, 1, func(_ taskdomain.GenerationStateReader, writer taskdomain.GenerationWriter) error {
			return writer.InsertMissingOccurrences(ctx, taskdomain.GenerationInsert{WorkspaceID: workspaceID, TaskID: "generation-date", ExpectedScheduleRevision: 99, Occurrences: []taskdomain.GenerationOccurrence{staleOccurrence}})
		})
		if !errors.Is(err, taskdomain.ErrScheduleRevisionConflict) {
			t.Fatalf("stale insert error=%v", err)
		}
		err = fixture.Fencer.BeginGenerationWrite(ctx, workspaceID, 1, func(_ taskdomain.GenerationStateReader, writer taskdomain.GenerationWriter) error {
			return writer.CompleteGeneration(ctx, taskdomain.GenerationCompletion{WorkspaceID: workspaceID, TaskID: "generation-date", ExpectedScheduleRevision: 99, GenerationWatermark: "2026-06-05", Status: taskdomain.GenerationStatusIdle})
		})
		if !errors.Is(err, taskdomain.ErrScheduleRevisionConflict) {
			t.Fatalf("stale completion error=%v", err)
		}

		retryAt := time.Date(2026, 3, 7, 5, 5, 0, 0, time.UTC)
		for _, completion := range []taskdomain.GenerationCompletion{
			{WorkspaceID: workspaceID, TaskID: "generation-date", ExpectedScheduleRevision: 1, GenerationWatermark: "2026-06-05", Status: taskdomain.GenerationStatusRunning},
			{WorkspaceID: workspaceID, TaskID: "generation-date", ExpectedScheduleRevision: 1, GenerationWatermark: "2026-06-05", Status: taskdomain.GenerationStatusRetryPending, Error: "temporary", RetryAt: &retryAt, RetryPendingJobs: 1},
			{WorkspaceID: workspaceID, TaskID: "generation-date", ExpectedScheduleRevision: 1, GenerationWatermark: "2026-06-05", Status: taskdomain.GenerationStatusFailed, Error: "permanent", FailedJobs: 1},
			{WorkspaceID: workspaceID, TaskID: "generation-date", ExpectedScheduleRevision: 1, GenerationWatermark: "2026-06-05", Status: taskdomain.GenerationStatusIdle},
		} {
			if err := fixture.Fencer.BeginGenerationWrite(ctx, workspaceID, 1, func(_ taskdomain.GenerationStateReader, writer taskdomain.GenerationWriter) error {
				return writer.CompleteGeneration(ctx, completion)
			}); err != nil {
				t.Fatalf("complete status %s: %v", completion.Status, err)
			}
		}
		assertGenerationHeader(t, fixture, "generation-date", "2026-06-05", "idle", "", 0, 0)
		invalid := taskdomain.GenerationCompletion{WorkspaceID: workspaceID, TaskID: "generation-date", ExpectedScheduleRevision: 1, GenerationWatermark: "2026-06-05", Status: taskdomain.GenerationStatusRetryPending}
		err = fixture.Fencer.BeginGenerationWrite(ctx, workspaceID, 1, func(_ taskdomain.GenerationStateReader, writer taskdomain.GenerationWriter) error {
			return writer.CompleteGeneration(ctx, invalid)
		})
		if !errors.Is(err, taskdomain.ErrInvalidGenerationWorker) {
			t.Fatalf("invalid retry status error=%v", err)
		}
		assertGenerationHeader(t, fixture, "generation-date", "2026-06-05", "idle", "", 0, 0)
	})

	t.Run("closed_generation_capabilities_are_unusable", func(t *testing.T) {
		var reader taskdomain.GenerationStateReader
		var writer taskdomain.GenerationWriter
		if err := fixture.Fencer.BeginGenerationWrite(ctx, workspaceID, 1, func(r taskdomain.GenerationStateReader, w taskdomain.GenerationWriter) error {
			reader, writer = r, w
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := reader.ListGenerationTargets(ctx); !errors.Is(err, storage.ErrTenantWriteTxClosed) {
			t.Fatalf("closed generation reader error=%v", err)
		}
		if err := writer.InsertMissingOccurrences(ctx, taskdomain.GenerationInsert{}); !errors.Is(err, storage.ErrTenantWriteTxClosed) {
			t.Fatalf("closed generation writer error=%v", err)
		}
	})

	t.Run("generation_and_schedule_change_are_serialized", func(t *testing.T) {
		if err := fixture.Writer.BeginFencedWrite(ctx, workspaceID, 1, func(tx storage.TenantWriteTx) error {
			return tx.TaskDomainWriter().CreateTaskAggregate(ctx, generatorContractAggregate(workspaceID, "generation-race", taskdomain.TimingDate))
		}); err != nil {
			t.Fatal(err)
		}
		loaded := make(chan struct{})
		release := make(chan struct{})
		generationResult := make(chan error, 1)
		go func() {
			generationResult <- fixture.Fencer.BeginGenerationWrite(ctx, workspaceID, 1, func(reader taskdomain.GenerationStateReader, writer taskdomain.GenerationWriter) error {
				targets, err := reader.ListGenerationTargets(ctx)
				if err != nil {
					return err
				}
				_ = generatorTarget(t, targets, "generation-race")
				close(loaded)
				<-release
				for _, date := range []string{"2026-03-08", "2026-03-09"} {
					if err := writer.InsertMissingOccurrences(ctx, taskdomain.GenerationInsert{WorkspaceID: workspaceID, TaskID: "generation-race", ExpectedScheduleRevision: 1, Occurrences: []taskdomain.GenerationOccurrence{generatorDateOccurrence(workspaceID, "generation-race", date)}}); err != nil {
						return err
					}
				}
				return writer.CompleteGeneration(ctx, taskdomain.GenerationCompletion{WorkspaceID: workspaceID, TaskID: "generation-race", ExpectedScheduleRevision: 1, GenerationWatermark: "2026-03-09", Status: taskdomain.GenerationStatusIdle})
			})
		}()
		<-loaded
		scheduleResult := make(chan error, 1)
		go func() {
			scheduleResult <- fixture.Writer.BeginFencedWrite(ctx, workspaceID, 1, func(tx storage.TenantWriteTx) error {
				return tx.TaskDomainWriter().InstallScheduleVersion(ctx, generatorNextVersion(workspaceID, "generation-race"))
			})
		}()
		select {
		case err := <-scheduleResult:
			t.Fatalf("schedule change crossed generation transaction: %v", err)
		case <-time.After(150 * time.Millisecond):
		}
		close(release)
		if err := <-generationResult; err != nil {
			t.Fatalf("generation transaction=%v", err)
		}
		if err := <-scheduleResult; err != nil {
			t.Fatalf("serialized schedule transaction=%v", err)
		}
		var revision int
		if err := fixture.DB.QueryRow(`SELECT revision FROM domain_task_schedules_v2 WHERE workspace_id='generation-w1' AND task_id='generation-race'`).Scan(&revision); err != nil || revision != 2 {
			t.Fatalf("schedule revision=%d err=%v", revision, err)
		}
		assertGenerationOccurrenceCount(t, fixture.DB, "generation-race", 3)
	})
}

func generatorContractAggregate(workspaceID, taskID string, timing taskdomain.TimingType) taskdomain.TaskAggregateSnapshot {
	version := taskdomain.ScheduleVersion{WorkspaceID: workspaceID, TaskID: taskID, ScheduleRevision: 1, EffectiveFrom: "2026-03-07", RecurrenceType: taskdomain.RecurrenceDaily, TimingType: timing, StartsOn: "2026-03-07", EndsOn: "2026-03-09", RecurrenceRule: `{"interval":1}`}
	occurrence := taskdomain.OccurrenceRecord{WorkspaceID: workspaceID, ID: taskID + "-occ-1", TaskID: taskID, OccurrenceKey: "2026-03-07", PlannedDate: "2026-03-07", ExecutionStatus: taskdomain.ExecutionStatusOpen, Revision: 1, GeneratedScheduleRevision: 1}
	if timing == taskdomain.TimingTimeBlock {
		version.Timezone, version.LocalStartTime, version.DurationMinutes = "America/New_York", "03:30", 45
		start, end := time.Date(2026, 3, 7, 8, 30, 0, 0, time.UTC), time.Date(2026, 3, 7, 9, 15, 0, 0, time.UTC)
		occurrence.PlannedStartAt, occurrence.PlannedEndAt = &start, &end
	} else {
		version.Timezone = "UTC"
	}
	return taskdomain.TaskAggregateSnapshot{
		Task:     taskdomain.TaskRecord{WorkspaceID: workspaceID, ID: taskID, ProjectID: taskdomain.PersonalProjectID, Title: taskID, LifecycleStatus: taskdomain.TaskLifecycleActive, Revision: 1},
		Schedule: taskdomain.ScheduleHeader{WorkspaceID: workspaceID, TaskID: taskID, Revision: 1, CurrentScheduleRevision: 1}, Versions: []taskdomain.ScheduleVersion{version}, Occurrences: []taskdomain.OccurrenceRecord{occurrence},
	}
}

func generatorDateOccurrence(workspaceID, taskID, date string) taskdomain.GenerationOccurrence {
	return taskdomain.GenerationOccurrence{WorkspaceID: workspaceID, TaskID: taskID, ID: taskdomain.DeterministicOccurrenceID(workspaceID, taskID, date), OccurrenceKey: date, PlannedDate: date, GeneratedScheduleRevision: 1}
}

func generatorNextVersion(workspaceID, taskID string) taskdomain.ScheduleVersionInstall {
	return taskdomain.ScheduleVersionInstall{WorkspaceID: workspaceID, TaskID: taskID, ExpectedScheduleRevision: 1, Version: taskdomain.ScheduleVersion{WorkspaceID: workspaceID, TaskID: taskID, ScheduleRevision: 2, EffectiveFrom: "2026-03-10", RecurrenceType: taskdomain.RecurrenceDaily, TimingType: taskdomain.TimingDate, Timezone: "UTC", StartsOn: "2026-03-10", EndsOn: "2026-03-11", RecurrenceRule: `{"interval":1}`}}
}

func generatorTarget(t *testing.T, targets []taskdomain.GenerationTargetState, taskID string) taskdomain.GenerationTargetState {
	t.Helper()
	for _, target := range targets {
		if target.TaskID == taskID {
			return target
		}
	}
	t.Fatalf("generation target %s not found in %#v", taskID, targets)
	return taskdomain.GenerationTargetState{}
}

func assertGenerationOccurrenceCount(t *testing.T, db *sql.DB, taskID string, expected int) {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM domain_task_occurrences_v2 WHERE workspace_id='generation-w1' AND task_id=$1`, taskID).Scan(&count); err != nil {
		if err := db.QueryRow(`SELECT COUNT(*) FROM domain_task_occurrences_v2 WHERE workspace_id='generation-w1' AND task_id=?`, taskID).Scan(&count); err != nil {
			t.Fatal(err)
		}
	}
	if count != expected {
		t.Fatalf("task %s occurrence count=%d want=%d", taskID, count, expected)
	}
}

func assertGenerationHeader(t *testing.T, fixture TaskDomainV2GeneratorFixture, taskID, watermark, status, generationError string, retryPending, failed int) {
	t.Helper()
	var actualWatermark, actualError sql.NullString
	var actualStatus string
	var actualRetryPending, actualFailed int
	statement := `SELECT generation_watermark,generation_status,generation_error,generation_retry_pending_jobs,generation_failed_jobs FROM domain_task_schedules_v2 WHERE workspace_id='generation-w1' AND task_id=?`
	if fixture.Dialect == TaskDomainV2Postgres {
		statement = `SELECT generation_watermark::text,generation_status,generation_error,generation_retry_pending_jobs,generation_failed_jobs FROM domain_task_schedules_v2 WHERE workspace_id='generation-w1' AND task_id=$1`
	}
	if err := fixture.DB.QueryRow(statement, taskID).Scan(&actualWatermark, &actualStatus, &actualError, &actualRetryPending, &actualFailed); err != nil {
		t.Fatal(err)
	}
	if actualWatermark.String != watermark || actualStatus != status || actualError.String != generationError || actualRetryPending != retryPending || actualFailed != failed {
		t.Fatalf("generation header=%q/%s/%q/%d/%d want=%q/%s/%q/%d/%d", actualWatermark.String, actualStatus, actualError.String, actualRetryPending, actualFailed, watermark, status, generationError, retryPending, failed)
	}
}

func assertGenerationTimeBlocks(t *testing.T, fixture TaskDomainV2GeneratorFixture) {
	t.Helper()
	rows, err := fixture.DB.Query(`SELECT occurrence_key,planned_date,planned_start_at,planned_end_at FROM domain_task_occurrences_v2 WHERE workspace_id='generation-w1' AND task_id='generation-time' ORDER BY occurrence_key`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	starts := make([]time.Time, 0, 3)
	for rows.Next() {
		var key, date string
		var startValue, endValue any
		if err := rows.Scan(&key, &date, &startValue, &endValue); err != nil {
			t.Fatal(err)
		}
		start, end := scheduleContractDBTime(t, startValue), scheduleContractDBTime(t, endValue)
		if key != scheduleContractDBDate(date) || end.Sub(start) != 45*time.Minute {
			t.Fatalf("time block key/date/range=%s/%s/%s", key, date, end.Sub(start))
		}
		starts = append(starts, start)
	}
	want := []time.Time{time.Date(2026, 3, 7, 8, 30, 0, 0, time.UTC), time.Date(2026, 3, 8, 7, 30, 0, 0, time.UTC), time.Date(2026, 3, 9, 7, 30, 0, 0, time.UTC)}
	if len(starts) != len(want) {
		t.Fatalf("DST time block start count=%d want=%d", len(starts), len(want))
	}
	for index := range want {
		if !starts[index].Equal(want[index]) {
			t.Fatalf("DST time block start[%d]=%v want=%v", index, starts[index], want[index])
		}
	}
}

type generatorClaimSource struct {
	claim taskdomain.GenerationWorkspaceClaim
}

func (source generatorClaimSource) ClaimGenerationWorkspaces(context.Context, int, time.Time) ([]taskdomain.GenerationWorkspaceClaim, error) {
	return []taskdomain.GenerationWorkspaceClaim{source.claim}, nil
}

func (generatorClaimSource) CompleteGenerationClaim(context.Context, taskdomain.GenerationClaimOutcome) error {
	return nil
}

type generatorRuntimeResolver struct {
	snapshot taskdomain.GenerationRuntimeSnapshot
}

func (resolver generatorRuntimeResolver) ResolveGenerationRuntime(context.Context, string) (taskdomain.GenerationRuntimeSnapshot, error) {
	return resolver.snapshot, nil
}

func sortedGenerationKeys(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}
