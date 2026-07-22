package taskdomain

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestGenerationWorkerMaterializesDefaultWindowWithCurrentRuntimeEpoch(t *testing.T) {
	now := time.Date(2026, 7, 22, 16, 30, 0, 0, time.UTC)
	claim := GenerationWorkspaceClaim{ClaimID: "claim-1", WorkspaceID: "workspace-1", CreatedEpoch: 2}
	source := &generationClaimSourceFake{batches: [][]GenerationWorkspaceClaim{{claim}}}
	reader := &generationStateReaderFake{targets: []GenerationTargetState{generationTargetForTest("task-1", nil)}}
	fencer := &generationFencerFake{reader: reader}
	resolver := &generationResolverFake{snapshots: map[string][]GenerationRuntimeSnapshot{
		"workspace-1": {{WorkspaceID: "workspace-1", Epoch: 9, Fencer: fencer}},
	}}
	worker := NewGenerationWorker(source, resolver)

	results, err := worker.RunBatch(context.Background(), GenerationBatchRequest{Limit: 10, Now: now})
	if err != nil {
		t.Fatalf("RunBatch() unexpected error: %v", err)
	}
	if source.calls != 1 || source.limit != 10 || !source.claimedAt.Equal(now) {
		t.Fatalf("claim request = calls:%d limit:%d at:%v", source.calls, source.limit, source.claimedAt)
	}
	if !reflect.DeepEqual(resolver.calls, []string{"workspace-1"}) || !reflect.DeepEqual(fencer.expectedEpochs, []int64{9}) {
		t.Fatalf("runtime resolution = calls:%v epochs:%v", resolver.calls, fencer.expectedEpochs)
	}
	if len(fencer.committedInserts) != 1 || len(fencer.committedCompletions) != 1 {
		t.Fatalf("committed writes = inserts:%#v completions:%#v", fencer.committedInserts, fencer.committedCompletions)
	}
	insert := fencer.committedInserts[0]
	if len(insert.Occurrences) != 91 || insert.Occurrences[0].OccurrenceKey != "2026-07-22" || insert.Occurrences[90].OccurrenceKey != "2026-10-20" {
		t.Fatalf("default [today,today+91d) window = count:%d first:%q last:%q", len(insert.Occurrences), insert.Occurrences[0].OccurrenceKey, insert.Occurrences[len(insert.Occurrences)-1].OccurrenceKey)
	}
	for _, occurrence := range insert.Occurrences {
		if occurrence.ID != DeterministicOccurrenceID("workspace-1", "task-1", occurrence.OccurrenceKey) {
			t.Fatalf("non-deterministic occurrence ID for %q: %q", occurrence.OccurrenceKey, occurrence.ID)
		}
	}
	completion := fencer.committedCompletions[0]
	if completion.TaskID != "task-1" || completion.ExpectedScheduleRevision != 4 || completion.GenerationWatermark != "2026-10-20" || completion.Status != GenerationStatusIdle {
		t.Fatalf("generation completion = %#v", completion)
	}
	if len(results) != 1 || results[0].CreatedEpoch != 2 || results[0].RuntimeEpoch != 9 || results[0].Status != GenerationStatusIdle || results[0].Inserted != 91 || results[0].GenerationWatermark != "2026-10-20" || results[0].Error != nil {
		t.Fatalf("workspace result = %#v", results)
	}
}

func TestGenerationWorkerRerunAndOverlappingVersionsNeverDuplicateOccurrences(t *testing.T) {
	now := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)
	target := generationTargetForTest("task-1", []string{"2026-07-23", "2026-07-25"})
	target.Versions = []GenerationScheduleVersion{
		{Revision: 1, Schedule: generationDailySchedule(t, "2026-07-22"), Effective: ScheduleEffectiveRange{From: "2026-07-22"}},
		{Revision: 2, Schedule: generationDailySchedule(t, "2026-07-22"), Effective: ScheduleEffectiveRange{From: "2026-07-24"}},
	}
	reader := &generationStateReaderFake{targets: []GenerationTargetState{target}}
	fencer := &generationFencerFake{reader: reader}
	source := &generationClaimSourceFake{batches: [][]GenerationWorkspaceClaim{
		{{ClaimID: "claim-first", WorkspaceID: "workspace-1", CreatedEpoch: 1}},
		{{ClaimID: "claim-rerun", WorkspaceID: "workspace-1", CreatedEpoch: 1}},
	}}
	resolver := &generationResolverFake{snapshots: map[string][]GenerationRuntimeSnapshot{
		"workspace-1": {
			{WorkspaceID: "workspace-1", Epoch: 5, Fencer: fencer},
			{WorkspaceID: "workspace-1", Epoch: 5, Fencer: fencer},
		},
	}}
	worker := NewGenerationWorker(source, resolver)

	first, err := worker.RunBatch(context.Background(), GenerationBatchRequest{Limit: 1, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || first[0].Inserted != 89 {
		t.Fatalf("first result = %#v, want 89 missing of 91 keys", first)
	}
	firstInsert := fencer.committedInserts[0]
	seen := make(map[string]GenerationOccurrence, len(firstInsert.Occurrences))
	for _, occurrence := range firstInsert.Occurrences {
		if _, duplicate := seen[occurrence.OccurrenceKey]; duplicate {
			t.Fatalf("overlapping versions emitted duplicate key %q", occurrence.OccurrenceKey)
		}
		seen[occurrence.OccurrenceKey] = occurrence
	}
	if seen["2026-07-24"].GeneratedScheduleRevision != 2 || seen["2026-07-22"].GeneratedScheduleRevision != 1 {
		t.Fatalf("overlap ownership is not deterministic: day22=%#v day24=%#v", seen["2026-07-22"], seen["2026-07-24"])
	}

	allKeys := append([]string{"2026-07-23", "2026-07-25"}, generationOccurrenceKeys(firstInsert.Occurrences)...)
	reader.targets[0].ExistingOccurrenceKeys = allKeys
	second, err := worker.RunBatch(context.Background(), GenerationBatchRequest{Limit: 1, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 || second[0].Inserted != 0 {
		t.Fatalf("rerun result = %#v, want no new occurrences", second)
	}
	if len(fencer.committedInserts) != 1 {
		t.Fatalf("rerun unexpectedly issued another insert: %#v", fencer.committedInserts)
	}
}

func TestGenerationWorkerMaterializesTimeBlocksAcrossDSTWithUTCInstants(t *testing.T) {
	now := time.Date(2026, 3, 7, 5, 0, 0, 0, time.UTC)
	target := GenerationTargetState{
		TaskID: "time-task", ScheduleRevision: 2, GenerationEnabled: true,
		Versions: []GenerationScheduleVersion{{Revision: 1, Schedule: mustNormalizeGeneratorSchedule(t, ScheduleInput{
			RecurrenceType: RecurrenceDaily, TimingType: TimingTimeBlock, Timezone: "America/New_York",
			StartsOn: "2026-03-07", EndsOn: "2026-03-09", Rule: json.RawMessage(`{"interval":1}`),
			LocalStartTime: "03:30", DurationMinutes: 45,
		}), Effective: ScheduleEffectiveRange{From: "2026-03-07"}}},
	}
	fencer := &generationFencerFake{reader: &generationStateReaderFake{targets: []GenerationTargetState{target}}}
	worker := NewGenerationWorker(
		&generationClaimSourceFake{batches: [][]GenerationWorkspaceClaim{{{ClaimID: "dst", WorkspaceID: "workspace-1", CreatedEpoch: 1}}}},
		&generationResolverFake{snapshots: map[string][]GenerationRuntimeSnapshot{"workspace-1": {{WorkspaceID: "workspace-1", Epoch: 3, Fencer: fencer}}}},
	)
	if _, err := worker.RunBatch(context.Background(), GenerationBatchRequest{Limit: 1, Now: now}); err != nil {
		t.Fatal(err)
	}
	occurrences := fencer.committedInserts[0].Occurrences
	if len(occurrences) != 3 {
		t.Fatalf("DST occurrences=%#v", occurrences)
	}
	for _, occurrence := range occurrences {
		if occurrence.PlannedStartAt == nil || occurrence.PlannedEndAt == nil ||
			occurrence.PlannedDate != occurrence.OccurrenceKey || occurrence.PlannedEndAt.Sub(*occurrence.PlannedStartAt) != 45*time.Minute {
			t.Fatalf("incomplete time block occurrence=%#v", occurrence)
		}
	}
	if !occurrences[0].PlannedStartAt.Equal(time.Date(2026, 3, 7, 8, 30, 0, 0, time.UTC)) ||
		!occurrences[1].PlannedStartAt.Equal(time.Date(2026, 3, 8, 7, 30, 0, 0, time.UTC)) ||
		!occurrences[2].PlannedStartAt.Equal(time.Date(2026, 3, 9, 7, 30, 0, 0, time.UTC)) {
		t.Fatalf("DST UTC starts=%s,%s,%s", occurrences[0].PlannedStartAt, occurrences[1].PlannedStartAt, occurrences[2].PlannedStartAt)
	}
}

func TestGenerationWorkerIsolatesWorkspaceFailuresAndClassifiesRetry(t *testing.T) {
	now := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)
	source := &generationClaimSourceFake{batches: [][]GenerationWorkspaceClaim{{
		{ClaimID: "retry", WorkspaceID: "workspace-retry", CreatedEpoch: 1},
		{ClaimID: "ok", WorkspaceID: "workspace-ok", CreatedEpoch: 1},
		{ClaimID: "failed", WorkspaceID: "workspace-failed", CreatedEpoch: 1},
	}}}
	retryFencer := &generationFencerFake{
		reader:      &generationStateReaderFake{targets: []GenerationTargetState{generationTargetForTest("task-retry", nil)}},
		insertError: retryableGenerationError{cause: errors.New("temporary write failure")},
	}
	okFencer := &generationFencerFake{reader: &generationStateReaderFake{targets: []GenerationTargetState{generationTargetForTest("task-ok", nil)}}}
	failedFencer := &generationFencerFake{reader: &generationStateReaderFake{err: errors.New("corrupt generation state")}}
	resolver := &generationResolverFake{snapshots: map[string][]GenerationRuntimeSnapshot{
		"workspace-retry":  {{WorkspaceID: "workspace-retry", Epoch: 4, Fencer: retryFencer}},
		"workspace-ok":     {{WorkspaceID: "workspace-ok", Epoch: 7, Fencer: okFencer}},
		"workspace-failed": {{WorkspaceID: "workspace-failed", Epoch: 8, Fencer: failedFencer}},
	}}

	results, err := NewGenerationWorker(source, resolver).RunBatch(context.Background(), GenerationBatchRequest{Limit: 3, Now: now})
	if err != nil {
		t.Fatalf("RunBatch() returned batch error: %v", err)
	}
	if len(results) != 3 || results[0].Status != GenerationStatusRetryPending || results[1].Status != GenerationStatusIdle || results[2].Status != GenerationStatusFailed {
		t.Fatalf("per-workspace results = %#v", results)
	}
	if results[0].Error == nil || results[2].Error == nil || results[1].Error != nil {
		t.Fatalf("per-workspace errors = %#v", results)
	}
	if len(okFencer.committedCompletions) != 1 || len(retryFencer.committedInserts) != 0 || len(retryFencer.committedCompletions) != 0 || len(failedFencer.committedCompletions) != 0 {
		t.Fatalf("workspace transaction isolation failed: retry=%#v ok=%#v failed=%#v", retryFencer, okFencer, failedFencer)
	}
}

func TestGenerationWorkerDoesNotAdvanceWatermarkWhenInsertOrCompletionFails(t *testing.T) {
	now := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name            string
		insertError     error
		completionError error
		wantStatus      GenerationStatus
	}{
		{name: "retryable insert", insertError: retryableGenerationError{cause: errors.New("insert interrupted")}, wantStatus: GenerationStatusRetryPending},
		{name: "permanent completion", completionError: errors.New("schedule CAS rejected"), wantStatus: GenerationStatusFailed},
	} {
		t.Run(tt.name, func(t *testing.T) {
			reader := &generationStateReaderFake{targets: []GenerationTargetState{generationTargetForTest("task-1", nil)}}
			fencer := &generationFencerFake{reader: reader, insertError: tt.insertError, completionError: tt.completionError}
			source := &generationClaimSourceFake{batches: [][]GenerationWorkspaceClaim{{{ClaimID: "claim", WorkspaceID: "workspace-1", CreatedEpoch: 1}}}}
			resolver := &generationResolverFake{snapshots: map[string][]GenerationRuntimeSnapshot{"workspace-1": {{WorkspaceID: "workspace-1", Epoch: 3, Fencer: fencer}}}}

			results, err := NewGenerationWorker(source, resolver).RunBatch(context.Background(), GenerationBatchRequest{Limit: 1, Now: now})
			if err != nil {
				t.Fatal(err)
			}
			if len(results) != 1 || results[0].Status != tt.wantStatus || results[0].GenerationWatermark != "" {
				t.Fatalf("failure result = %#v", results)
			}
			if len(fencer.committedInserts) != 0 || len(fencer.committedCompletions) != 0 {
				t.Fatalf("failed transaction committed partial writes: inserts=%#v completions=%#v", fencer.committedInserts, fencer.committedCompletions)
			}
			if tt.insertError != nil && fencer.lastStagedCompletionCalls != 0 {
				t.Fatalf("watermark completion was attempted after insert failed")
			}
		})
	}
}

func TestGenerationWorkerStaleEpochRetriesWithFreshlyResolvedRuntime(t *testing.T) {
	now := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)
	claim := GenerationWorkspaceClaim{ClaimID: "claim", WorkspaceID: "workspace-1", CreatedEpoch: 1}
	source := &generationClaimSourceFake{batches: [][]GenerationWorkspaceClaim{{claim}, {claim}}}
	staleFencer := &generationFencerFake{beginErrors: []error{ErrTaskRuntimeEpochConflict}}
	freshFencer := &generationFencerFake{reader: &generationStateReaderFake{targets: []GenerationTargetState{generationTargetForTest("task-1", nil)}}}
	resolver := &generationResolverFake{snapshots: map[string][]GenerationRuntimeSnapshot{
		"workspace-1": {
			{WorkspaceID: "workspace-1", Epoch: 4, Fencer: staleFencer},
			{WorkspaceID: "workspace-1", Epoch: 5, Fencer: freshFencer},
		},
	}}
	worker := NewGenerationWorker(source, resolver)

	first, err := worker.RunBatch(context.Background(), GenerationBatchRequest{Limit: 1, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	second, err := worker.RunBatch(context.Background(), GenerationBatchRequest{Limit: 1, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if first[0].Status != GenerationStatusRetryPending || first[0].RuntimeEpoch != 4 || second[0].Status != GenerationStatusIdle || second[0].RuntimeEpoch != 5 {
		t.Fatalf("stale/fresh results = first:%#v second:%#v", first, second)
	}
	if !reflect.DeepEqual(staleFencer.expectedEpochs, []int64{4}) || !reflect.DeepEqual(freshFencer.expectedEpochs, []int64{5}) {
		t.Fatalf("write epochs = stale:%v fresh:%v; created_epoch=%d must not be used", staleFencer.expectedEpochs, freshFencer.expectedEpochs, claim.CreatedEpoch)
	}
}

func TestGenerationWorkerAcknowledgesResolveFailureWithSanitizedRetryOutcome(t *testing.T) {
	now := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)
	secretFailure := retryableGenerationError{cause: errors.New("dial postgres://admin:super-secret@db.internal/tenant")}
	source := &generationClaimSourceFake{batches: [][]GenerationWorkspaceClaim{{{
		ClaimID: "claim-resolve", WorkspaceID: "workspace-1", CreatedEpoch: 4,
	}}}}
	resolver := &generationResolverFake{errors: map[string][]error{"workspace-1": {secretFailure}}}

	results, err := NewGenerationWorker(source, resolver).RunBatch(context.Background(), GenerationBatchRequest{Limit: 1, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Status != GenerationStatusRetryPending || !results[0].Acknowledged || results[0].AckError != nil {
		t.Fatalf("resolve failure result = %#v", results)
	}
	if len(source.outcomes) != 1 {
		t.Fatalf("ack outcomes = %#v", source.outcomes)
	}
	outcome := source.outcomes[0]
	if outcome.ClaimID != "claim-resolve" || outcome.WorkspaceID != "workspace-1" || outcome.CreatedEpoch != 4 || outcome.RuntimeEpoch != 0 ||
		outcome.Status != GenerationStatusRetryPending || !outcome.RetryAt.Equal(now.Add(GenerationClaimRetryDelay)) || outcome.ErrorCode != GenerationClaimErrorRuntimeResolve {
		t.Fatalf("resolve outcome = %#v", outcome)
	}
	encoded, marshalErr := json.Marshal(outcome)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if strings.Contains(string(encoded), "super-secret") || strings.Contains(string(encoded), "postgres://") {
		t.Fatalf("ack outcome leaked secret: %s", encoded)
	}
}

func TestGenerationWorkerAcknowledgesWriteRollbackOutsideTenantTransaction(t *testing.T) {
	now := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)
	fencer := &generationFencerFake{
		reader:      &generationStateReaderFake{targets: []GenerationTargetState{generationTargetForTest("task-1", nil)}},
		insertError: errors.New("constraint rejected"),
	}
	source := &generationClaimSourceFake{
		batches: [][]GenerationWorkspaceClaim{{{ClaimID: "claim-write", WorkspaceID: "workspace-1", CreatedEpoch: 2}}},
		ackHook: func(GenerationClaimOutcome) error {
			if fencer.inTransaction {
				t.Fatal("claim was acknowledged inside tenant transaction")
			}
			return nil
		},
	}
	resolver := &generationResolverFake{snapshots: map[string][]GenerationRuntimeSnapshot{
		"workspace-1": {{WorkspaceID: "workspace-1", Epoch: 8, Fencer: fencer}},
	}}

	results, err := NewGenerationWorker(source, resolver).RunBatch(context.Background(), GenerationBatchRequest{Limit: 1, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Status != GenerationStatusFailed || !results[0].Acknowledged {
		t.Fatalf("write failure result = %#v", results)
	}
	if len(fencer.committedInserts) != 0 || len(fencer.committedCompletions) != 0 {
		t.Fatalf("failed tenant write committed data: %#v / %#v", fencer.committedInserts, fencer.committedCompletions)
	}
	if len(source.outcomes) != 1 || source.outcomes[0].RuntimeEpoch != 8 || source.outcomes[0].ErrorCode != GenerationClaimErrorFencedWrite || !source.outcomes[0].RetryAt.IsZero() {
		t.Fatalf("write failure outcome = %#v", source.outcomes)
	}
}

func TestGenerationWorkerAckFailureIsVisibleAndLeaseRerunIsIdempotent(t *testing.T) {
	now := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)
	claim := GenerationWorkspaceClaim{ClaimID: "claim-1", WorkspaceID: "workspace-1", CreatedEpoch: 3}
	source := &generationClaimSourceFake{
		batches:   [][]GenerationWorkspaceClaim{{claim}, {claim}},
		ackErrors: []error{errors.New("claim store unavailable"), nil},
	}
	reader := &generationStateReaderFake{targets: []GenerationTargetState{generationTargetForTest("task-1", nil)}}
	fencer := &generationFencerFake{reader: reader}
	resolver := &generationResolverFake{snapshots: map[string][]GenerationRuntimeSnapshot{
		"workspace-1": {
			{WorkspaceID: "workspace-1", Epoch: 8, Fencer: fencer},
			{WorkspaceID: "workspace-1", Epoch: 9, Fencer: fencer},
		},
	}}
	worker := NewGenerationWorker(source, resolver)

	first, err := worker.RunBatch(context.Background(), GenerationBatchRequest{Limit: 1, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || first[0].Status != GenerationStatusIdle || first[0].Acknowledged || first[0].AckError == nil || first[0].Error == nil {
		t.Fatalf("first result = %#v", first)
	}
	if len(fencer.committedInserts) != 1 {
		t.Fatalf("first committed inserts = %#v", fencer.committedInserts)
	}
	reader.targets[0].ExistingOccurrenceKeys = generationOccurrenceKeys(fencer.committedInserts[0].Occurrences)

	second, err := worker.RunBatch(context.Background(), GenerationBatchRequest{Limit: 1, Now: now.Add(2 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 || second[0].Status != GenerationStatusIdle || !second[0].Acknowledged || second[0].AckError != nil || second[0].Inserted != 0 {
		t.Fatalf("second result = %#v", second)
	}
	if len(fencer.committedInserts) != 1 {
		t.Fatalf("lease rerun duplicated occurrence insert: %#v", fencer.committedInserts)
	}
	if len(source.outcomes) != 2 || source.outcomes[0].RuntimeEpoch != 8 || source.outcomes[1].RuntimeEpoch != 9 ||
		source.outcomes[0].CreatedEpoch != 3 || source.outcomes[1].CreatedEpoch != 3 {
		t.Fatalf("audit epochs = %#v", source.outcomes)
	}
}

func TestGenerationWorkerMixedBatchAcknowledgesEveryValidClaimAndContinuesAfterAckFailure(t *testing.T) {
	now := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)
	source := &generationClaimSourceFake{
		batches: [][]GenerationWorkspaceClaim{{
			{ClaimID: "resolve", WorkspaceID: "workspace-resolve", CreatedEpoch: 1},
			{ClaimID: "success", WorkspaceID: "workspace-success", CreatedEpoch: 1},
			{ClaimID: "write", WorkspaceID: "workspace-write", CreatedEpoch: 1},
		}},
		ackErrors: []error{errors.New("first ack failed"), nil, nil},
	}
	writeFencer := &generationFencerFake{beginErrors: []error{ErrTaskRuntimeEpochConflict}}
	successFencer := &generationFencerFake{reader: &generationStateReaderFake{targets: []GenerationTargetState{generationTargetForTest("task-ok", nil)}}}
	resolver := &generationResolverFake{
		errors: map[string][]error{"workspace-resolve": {errors.New("profile unavailable")}},
		snapshots: map[string][]GenerationRuntimeSnapshot{
			"workspace-success": {{WorkspaceID: "workspace-success", Epoch: 6, Fencer: successFencer}},
			"workspace-write":   {{WorkspaceID: "workspace-write", Epoch: 7, Fencer: writeFencer}},
		},
	}

	results, err := NewGenerationWorker(source, resolver).RunBatch(context.Background(), GenerationBatchRequest{Limit: 3, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 || results[0].AckError == nil || results[1].Status != GenerationStatusIdle || !results[1].Acknowledged ||
		results[2].Status != GenerationStatusRetryPending || !results[2].Acknowledged {
		t.Fatalf("mixed results = %#v", results)
	}
	if len(source.outcomes) != 3 || !reflect.DeepEqual(resolver.calls, []string{"workspace-resolve", "workspace-success", "workspace-write"}) {
		t.Fatalf("batch did not isolate claims: outcomes=%#v calls=%#v", source.outcomes, resolver.calls)
	}
}

func TestGenerationWorkerRejectsInvalidClaimsWithoutResolvingOrAcknowledging(t *testing.T) {
	now := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)
	source := &generationClaimSourceFake{batches: [][]GenerationWorkspaceClaim{{
		{ClaimID: "", WorkspaceID: "workspace-1", CreatedEpoch: 1},
		{ClaimID: "claim", WorkspaceID: "", CreatedEpoch: 1},
		{ClaimID: "claim-zero", WorkspaceID: "workspace-2", CreatedEpoch: 0},
	}}}
	resolver := &generationResolverFake{}
	results, err := NewGenerationWorker(source, resolver).RunBatch(context.Background(), GenerationBatchRequest{Limit: 3, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Fatalf("invalid results = %#v", results)
	}
	for _, result := range results {
		if result.Status != GenerationStatusFailed || !errors.Is(result.Error, ErrInvalidGenerationWorker) || result.Acknowledged {
			t.Fatalf("invalid claim result = %#v", result)
		}
	}
	if len(resolver.calls) != 0 || len(source.outcomes) != 0 {
		t.Fatalf("invalid claims reached dependencies: resolver=%v outcomes=%#v", resolver.calls, source.outcomes)
	}
}

func generationTargetForTest(taskID string, existing []string) GenerationTargetState {
	return GenerationTargetState{
		TaskID: taskID, ScheduleRevision: 4, GenerationEnabled: true,
		Versions: []GenerationScheduleVersion{{
			Revision: 1,
			Schedule: Schedule{
				RecurrenceType: RecurrenceDaily, TimingType: TimingDate, Timezone: "UTC", StartsOn: "2026-07-22",
				Rule: &RecurrenceRule{Interval: 1},
			},
			Effective: ScheduleEffectiveRange{From: "2026-07-22"},
		}},
		ExistingOccurrenceKeys: append([]string(nil), existing...),
	}
}

func generationDailySchedule(t *testing.T, startsOn string) Schedule {
	t.Helper()
	return mustNormalizeGeneratorSchedule(t, ScheduleInput{
		RecurrenceType: RecurrenceDaily, TimingType: TimingDate, Timezone: "UTC", StartsOn: startsOn,
		Rule: json.RawMessage(`{"interval":1}`),
	})
}

func generationOccurrenceKeys(occurrences []GenerationOccurrence) []string {
	keys := make([]string, 0, len(occurrences))
	for _, occurrence := range occurrences {
		keys = append(keys, occurrence.OccurrenceKey)
	}
	return keys
}

type generationClaimSourceFake struct {
	batches   [][]GenerationWorkspaceClaim
	calls     int
	limit     int
	claimedAt time.Time
	outcomes  []GenerationClaimOutcome
	ackErrors []error
	ackHook   func(GenerationClaimOutcome) error
}

func (source *generationClaimSourceFake) CompleteGenerationClaim(_ context.Context, outcome GenerationClaimOutcome) error {
	source.outcomes = append(source.outcomes, outcome)
	if source.ackHook != nil {
		if err := source.ackHook(outcome); err != nil {
			return err
		}
	}
	if len(source.ackErrors) == 0 {
		return nil
	}
	err := source.ackErrors[0]
	source.ackErrors = source.ackErrors[1:]
	return err
}

func (source *generationClaimSourceFake) ClaimGenerationWorkspaces(_ context.Context, limit int, claimedAt time.Time) ([]GenerationWorkspaceClaim, error) {
	source.calls++
	source.limit = limit
	source.claimedAt = claimedAt
	if len(source.batches) == 0 {
		return nil, nil
	}
	batch := append([]GenerationWorkspaceClaim(nil), source.batches[0]...)
	source.batches = source.batches[1:]
	return batch, nil
}

type generationResolverFake struct {
	snapshots map[string][]GenerationRuntimeSnapshot
	errors    map[string][]error
	calls     []string
}

func (resolver *generationResolverFake) ResolveGenerationRuntime(_ context.Context, workspaceID string) (GenerationRuntimeSnapshot, error) {
	resolver.calls = append(resolver.calls, workspaceID)
	if failures := resolver.errors[workspaceID]; len(failures) > 0 {
		err := failures[0]
		resolver.errors[workspaceID] = failures[1:]
		return GenerationRuntimeSnapshot{}, err
	}
	snapshots := resolver.snapshots[workspaceID]
	if len(snapshots) == 0 {
		return GenerationRuntimeSnapshot{}, errors.New("runtime not found")
	}
	snapshot := snapshots[0]
	resolver.snapshots[workspaceID] = snapshots[1:]
	return snapshot, nil
}

type generationStateReaderFake struct {
	targets []GenerationTargetState
	err     error
	calls   int
}

func (reader *generationStateReaderFake) ListGenerationTargets(context.Context) ([]GenerationTargetState, error) {
	reader.calls++
	if reader.err != nil {
		return nil, reader.err
	}
	return append([]GenerationTargetState(nil), reader.targets...), nil
}

type generationFencerFake struct {
	reader                    GenerationStateReader
	beginErrors               []error
	insertError               error
	completionError           error
	expectedEpochs            []int64
	committedInserts          []GenerationInsert
	committedCompletions      []GenerationCompletion
	lastStagedCompletionCalls int
	inTransaction             bool
}

func (fencer *generationFencerFake) BeginGenerationWrite(_ context.Context, _ string, expectedEpoch int64, callback func(GenerationStateReader, GenerationWriter) error) error {
	fencer.expectedEpochs = append(fencer.expectedEpochs, expectedEpoch)
	if len(fencer.beginErrors) > 0 {
		err := fencer.beginErrors[0]
		fencer.beginErrors = fencer.beginErrors[1:]
		if err != nil {
			return err
		}
	}
	writer := &generationWriterFake{insertError: fencer.insertError, completionError: fencer.completionError}
	fencer.inTransaction = true
	err := callback(fencer.reader, writer)
	fencer.inTransaction = false
	fencer.lastStagedCompletionCalls = len(writer.completions)
	if err != nil {
		return err
	}
	fencer.committedInserts = append(fencer.committedInserts, writer.inserts...)
	fencer.committedCompletions = append(fencer.committedCompletions, writer.completions...)
	return nil
}

type generationWriterFake struct {
	insertError     error
	completionError error
	inserts         []GenerationInsert
	completions     []GenerationCompletion
}

func (writer *generationWriterFake) InsertMissingOccurrences(_ context.Context, insert GenerationInsert) error {
	writer.inserts = append(writer.inserts, insert)
	return writer.insertError
}

func (writer *generationWriterFake) CompleteGeneration(_ context.Context, completion GenerationCompletion) error {
	writer.completions = append(writer.completions, completion)
	return writer.completionError
}

type retryableGenerationError struct{ cause error }

func (err retryableGenerationError) Error() string   { return err.cause.Error() }
func (err retryableGenerationError) Unwrap() error   { return err.cause }
func (err retryableGenerationError) Retryable() bool { return true }
