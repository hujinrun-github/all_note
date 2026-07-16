package contracttest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

func RunTranscriptionJobSuite(t *testing.T, factory StoreFactory) {
	t.Helper()

	t.Run("TRANS-IDEM-001_CreateReplayAndRejectChangedRequest", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		ctx := scopedContractContext(t, store)
		voiceClientID := seedUploadedVoiceNote(t, ctx, store)
		repository := transcriptionJobRepository(t, store)
		request := model.CreateTranscriptionJob{
			JobID:         "11111111-1111-4111-8111-111111111111",
			MutationID:    "22222222-2222-4222-8222-222222222222",
			RequestSHA256: "request-a",
			VoiceNoteID:   voiceClientID,
			Language:      "zh",
			Now:           time.Now().UTC().Unix(),
		}
		first, err := repository.CreateOrGet(ctx, request)
		if err != nil {
			t.Fatalf("create transcription job: %v", err)
		}
		if first.JobID != request.JobID || first.VoiceNoteID != voiceClientID || first.Generation != 1 || first.State != model.TranscriptionJobQueued || first.Revision != 1 {
			t.Fatalf("unexpected job: %+v", first)
		}
		replayed, err := repository.CreateOrGet(ctx, request)
		if err != nil {
			t.Fatalf("replay transcription job: %v", err)
		}
		firstJSON, err := json.Marshal(first)
		if err != nil {
			t.Fatal(err)
		}
		replayedJSON, err := json.Marshal(replayed)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(replayedJSON, firstJSON) {
			t.Fatalf("replayed wire job = %s, want %s", replayedJSON, firstJSON)
		}
		changed := request
		changed.RequestSHA256 = "request-b"
		if _, err := repository.CreateOrGet(ctx, changed); !errors.Is(err, storage.ErrMutationIDReused) {
			t.Fatalf("changed replay error = %v, want ErrMutationIDReused", err)
		}
	})

	t.Run("TRANS-IDEM-002_DifferentMutationsConvergeOnOneActiveJob", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		ctx := scopedContractContext(t, store)
		voiceClientID := seedUploadedVoiceNote(t, ctx, store)
		repository := transcriptionJobRepository(t, store)
		now := time.Now().UTC().Unix()
		first, err := repository.CreateOrGet(ctx, model.CreateTranscriptionJob{
			JobID: "33333333-3333-4333-8333-333333333333", MutationID: "44444444-4444-4444-8444-444444444444",
			RequestSHA256: "iphone-request", VoiceNoteID: voiceClientID, Language: "zh", Now: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		second, err := repository.CreateOrGet(ctx, model.CreateTranscriptionJob{
			JobID: "55555555-5555-4555-8555-555555555555", MutationID: "66666666-6666-4666-8666-666666666666",
			RequestSHA256: "watch-request", VoiceNoteID: voiceClientID, Language: "zh", Now: now + 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		if second.JobID != first.JobID || second.Generation != first.Generation {
			t.Fatalf("active jobs diverged: first=%+v second=%+v", first, second)
		}
	})

	t.Run("TRANS-TX-001_JobAndReceiptRollbackTogether", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		ctx := scopedContractContext(t, store)
		voiceClientID := seedUploadedVoiceNote(t, ctx, store)
		jobID := "77777777-7777-4777-8777-777777777777"
		sentinel := errors.New("rollback transcription job")
		err := store.Transact(ctx, func(tx storage.Store) error {
			repository := transcriptionJobRepository(t, tx)
			if _, err := repository.CreateOrGet(ctx, model.CreateTranscriptionJob{
				JobID: jobID, MutationID: "88888888-8888-4888-8888-888888888888", RequestSHA256: "rollback-request",
				VoiceNoteID: voiceClientID, Language: "zh", Now: time.Now().UTC().Unix(),
			}); err != nil {
				return err
			}
			return sentinel
		})
		if !errors.Is(err, sentinel) {
			t.Fatalf("transaction error = %v, want sentinel", err)
		}
		repository := transcriptionJobRepository(t, store)
		if _, err := repository.Get(ctx, jobID); !errors.Is(err, storage.ErrMobileEntityNotFound) {
			t.Fatalf("job after rollback error = %v, want not found", err)
		}
	})

	t.Run("TRANS-IDEM-003_ConcurrentPathsCreateOneActiveJob", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		ctx := scopedContractContext(t, store)
		voiceClientID := seedUploadedVoiceNote(t, ctx, store)
		repository := transcriptionJobRepository(t, store)
		now := time.Now().UTC().Unix()
		requests := []model.CreateTranscriptionJob{
			{JobID: "99999999-9999-4999-8999-999999999999", MutationID: "aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa", RequestSHA256: "concurrent-iphone", VoiceNoteID: voiceClientID, Language: "zh", Now: now},
			{JobID: "aaaaaaaa-2222-4222-8222-aaaaaaaaaaaa", MutationID: "aaaaaaaa-3333-4333-8333-aaaaaaaaaaaa", RequestSHA256: "concurrent-watch", VoiceNoteID: voiceClientID, Language: "zh", Now: now},
		}
		type outcome struct {
			job *model.TranscriptionJob
			err error
		}
		start := make(chan struct{})
		outcomes := make(chan outcome, len(requests))
		var ready sync.WaitGroup
		ready.Add(len(requests))
		for _, request := range requests {
			request := request
			go func() {
				ready.Done()
				<-start
				job, err := repository.CreateOrGet(ctx, request)
				outcomes <- outcome{job: job, err: err}
			}()
		}
		ready.Wait()
		close(start)
		first := <-outcomes
		second := <-outcomes
		if first.err != nil || second.err != nil {
			t.Fatalf("concurrent errors: first=%v second=%v", first.err, second.err)
		}
		if first.job.JobID != second.job.JobID || first.job.Generation != 1 || second.job.Generation != 1 {
			t.Fatalf("concurrent jobs diverged: first=%+v second=%+v", first.job, second.job)
		}
	})

	t.Run("TRANS-QUEUE-001_UploadPromotesWaitingJobAtomically", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		ctx := scopedContractContext(t, store)
		voiceClientID := seedPendingVoiceNote(t, ctx, store)
		repository := transcriptionJobRepository(t, store)
		now := time.Now().UTC().Unix()
		created, err := repository.CreateOrGet(ctx, model.CreateTranscriptionJob{
			JobID: "abababab-1111-4111-8111-abababababab", MutationID: "abababab-2222-4222-8222-abababababab",
			RequestSHA256: "waiting-for-upload", VoiceNoteID: voiceClientID, Language: "zh", Now: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		if created.State != model.TranscriptionJobWaitingForAudio {
			t.Fatalf("initial job state = %q, want waiting_for_audio", created.State)
		}
		nativeStore, err := storage.NativeStoreFrom(store)
		if err != nil {
			t.Fatal(err)
		}
		const audioHash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		if _, err := nativeStore.VoiceNotes().ClaimUpload(ctx, voiceClientID, model.VoiceUploadClaim{
			ObjectKey: "test/pending.m4a", MimeType: "audio/mp4", Size: 5, SHA256: audioHash,
		}); err != nil {
			t.Fatalf("claim upload: %v", err)
		}
		if _, err := nativeStore.VoiceNotes().MarkUploaded(ctx, voiceClientID, audioHash); err != nil {
			t.Fatalf("mark uploaded: %v", err)
		}
		promoted, err := repository.Get(ctx, created.JobID)
		if err != nil {
			t.Fatal(err)
		}
		if promoted.State != model.TranscriptionJobQueued || promoted.Revision != created.Revision+1 {
			t.Fatalf("promoted job = %+v", promoted)
		}
		lease, err := transcriptionJobWorkerRepository(t, store).ClaimNext(context.Background(), model.ClaimTranscriptionJob{
			WorkerID: "upload-worker", LeaseToken: "upload-lease", Now: now + 1, LeaseExpiresAt: now + 121,
		})
		if err != nil || lease.Job.JobID != created.JobID {
			t.Fatalf("claim promoted job: lease=%+v err=%v", lease, err)
		}
	})

	t.Run("TRANS-LEASE-001_ClaimExpiresAndFencesOldWorker", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		ctx := scopedContractContext(t, store)
		voiceClientID := seedUploadedVoiceNote(t, ctx, store)
		repository := transcriptionJobRepository(t, store)
		now := time.Now().UTC().Unix()
		created, err := repository.CreateOrGet(ctx, model.CreateTranscriptionJob{
			JobID: "bbbbbbbb-1111-4111-8111-bbbbbbbbbbbb", MutationID: "bbbbbbbb-2222-4222-8222-bbbbbbbbbbbb",
			RequestSHA256: "lease-request", VoiceNoteID: voiceClientID, Language: "zh", Now: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		worker := transcriptionJobWorkerRepository(t, store)
		first, err := worker.ClaimNext(context.Background(), model.ClaimTranscriptionJob{
			WorkerID: "worker-a", LeaseToken: "lease-a", Now: now, LeaseExpiresAt: now + 120,
		})
		if err != nil {
			t.Fatalf("first claim: %v", err)
		}
		if first.Job.JobID != created.JobID || first.Job.State != model.TranscriptionJobProcessing || first.Job.Attempt != 1 || first.Job.Revision != 2 {
			t.Fatalf("first lease = %+v", first)
		}
		if _, err := worker.ClaimNext(context.Background(), model.ClaimTranscriptionJob{
			WorkerID: "worker-b", LeaseToken: "lease-b-early", Now: now + 30, LeaseExpiresAt: now + 150,
		}); !errors.Is(err, storage.ErrNoTranscriptionJob) {
			t.Fatalf("early second claim error = %v, want ErrNoTranscriptionJob", err)
		}
		if _, err := worker.Heartbeat(context.Background(), model.HeartbeatTranscriptionJob{
			JobID: created.JobID, LeaseToken: "wrong-token", Now: now + 40, LeaseExpiresAt: now + 160,
		}); !errors.Is(err, storage.ErrTranscriptionLeaseLost) {
			t.Fatalf("wrong-token heartbeat error = %v, want lease lost", err)
		}
		heartbeat, err := worker.Heartbeat(context.Background(), model.HeartbeatTranscriptionJob{
			JobID: created.JobID, LeaseToken: "lease-a", Now: now + 40, LeaseExpiresAt: now + 160,
		})
		if err != nil {
			t.Fatalf("heartbeat: %v", err)
		}
		if heartbeat.Job.Revision != 3 || heartbeat.LeaseExpiresAt != now+160 {
			t.Fatalf("heartbeat lease = %+v", heartbeat)
		}
		second, err := worker.ClaimNext(context.Background(), model.ClaimTranscriptionJob{
			WorkerID: "worker-b", LeaseToken: "lease-b", Now: now + 161, LeaseExpiresAt: now + 281,
		})
		if err != nil {
			t.Fatalf("expired lease reclaim: %v", err)
		}
		if second.Job.JobID != created.JobID || second.Job.Attempt != 2 || second.Job.Revision != 4 || second.LeaseToken != "lease-b" {
			t.Fatalf("reclaimed lease = %+v", second)
		}
		if _, err := worker.Heartbeat(context.Background(), model.HeartbeatTranscriptionJob{
			JobID: created.JobID, LeaseToken: "lease-a", Now: now + 162, LeaseExpiresAt: now + 282,
		}); !errors.Is(err, storage.ErrTranscriptionLeaseLost) {
			t.Fatalf("stale heartbeat error = %v, want lease lost", err)
		}
	})

	t.Run("TRANS-LEASE-002_ConcurrentWorkersHaveOneWinner", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		ctx := scopedContractContext(t, store)
		voiceClientID := seedUploadedVoiceNote(t, ctx, store)
		now := time.Now().UTC().Unix()
		if _, err := transcriptionJobRepository(t, store).CreateOrGet(ctx, model.CreateTranscriptionJob{
			JobID: "cccccccc-1111-4111-8111-cccccccccccc", MutationID: "cccccccc-2222-4222-8222-cccccccccccc",
			RequestSHA256: "concurrent-claim", VoiceNoteID: voiceClientID, Language: "zh", Now: now,
		}); err != nil {
			t.Fatal(err)
		}
		worker := transcriptionJobWorkerRepository(t, store)
		claims := []model.ClaimTranscriptionJob{
			{WorkerID: "worker-a", LeaseToken: "claim-a", Now: now, LeaseExpiresAt: now + 120},
			{WorkerID: "worker-b", LeaseToken: "claim-b", Now: now, LeaseExpiresAt: now + 120},
		}
		type outcome struct {
			lease *model.TranscriptionJobLease
			err   error
		}
		start := make(chan struct{})
		outcomes := make(chan outcome, len(claims))
		var ready sync.WaitGroup
		ready.Add(len(claims))
		for _, claim := range claims {
			claim := claim
			go func() {
				ready.Done()
				<-start
				lease, err := worker.ClaimNext(context.Background(), claim)
				outcomes <- outcome{lease: lease, err: err}
			}()
		}
		ready.Wait()
		close(start)
		results := []outcome{<-outcomes, <-outcomes}
		successes := 0
		noJobs := 0
		for _, result := range results {
			switch {
			case result.err == nil:
				successes++
				if result.lease == nil || result.lease.Job.Attempt != 1 {
					t.Fatalf("invalid winning lease: %+v", result.lease)
				}
			case errors.Is(result.err, storage.ErrNoTranscriptionJob):
				noJobs++
			default:
				t.Fatalf("unexpected claim error: %v", result.err)
			}
		}
		if successes != 1 || noJobs != 1 {
			t.Fatalf("claim results: successes=%d noJobs=%d outcomes=%+v", successes, noJobs, results)
		}
	})

	t.Run("TRANS-RETRY-001_RetryEligibilityAndTerminalAttempt", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		ctx := scopedContractContext(t, store)
		voiceClientID := seedUploadedVoiceNote(t, ctx, store)
		now := time.Now().UTC().Unix()
		created, err := transcriptionJobRepository(t, store).CreateOrGet(ctx, model.CreateTranscriptionJob{
			JobID: "dddddddd-1111-4111-8111-dddddddddddd", MutationID: "dddddddd-2222-4222-8222-dddddddddddd",
			RequestSHA256: "retry-request", VoiceNoteID: voiceClientID, Language: "zh", Now: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		mobileRepository, err := storage.MobileSyncRepositoryFrom(store)
		if err != nil {
			t.Fatal(err)
		}
		voiceBefore, err := mobileRepository.GetEntityByClientID(ctx, "voice_note", voiceClientID)
		if err != nil {
			t.Fatal(err)
		}
		changesBefore, err := mobileRepository.ListPendingChanges(ctx)
		if err != nil {
			t.Fatal(err)
		}
		worker := transcriptionJobWorkerRepository(t, store)
		claim := func(attempt int64, at int64) *model.TranscriptionJobLease {
			t.Helper()
			lease, err := worker.ClaimNext(context.Background(), model.ClaimTranscriptionJob{
				WorkerID: "retry-worker", LeaseToken: fmt.Sprintf("retry-lease-%d", attempt), Now: at, LeaseExpiresAt: at + 120,
			})
			if err != nil {
				t.Fatalf("claim attempt %d: %v", attempt, err)
			}
			return lease
		}
		first := claim(1, now)
		retryAt := now + 30
		retrying, err := worker.Fail(context.Background(), model.FailTranscriptionJob{
			JobID: created.JobID, LeaseToken: first.LeaseToken, ErrorCode: "provider_timeout", NextAttemptAt: retryAt, Now: now + 1,
		})
		if err != nil {
			t.Fatalf("schedule retry: %v", err)
		}
		if retrying.State != model.TranscriptionJobRetryWaiting || retrying.NextAttemptAt == nil || *retrying.NextAttemptAt != retryAt {
			t.Fatalf("retrying job = %+v", retrying)
		}
		if _, err := worker.ClaimNext(context.Background(), model.ClaimTranscriptionJob{
			WorkerID: "early-worker", LeaseToken: "early", Now: retryAt - 1, LeaseExpiresAt: retryAt + 119,
		}); !errors.Is(err, storage.ErrNoTranscriptionJob) {
			t.Fatalf("early retry claim error = %v", err)
		}
		second := claim(2, retryAt)
		if second.Job.Attempt != 2 {
			t.Fatalf("second attempt = %d", second.Job.Attempt)
		}
		secondRetryAt := retryAt + 60
		if _, err := worker.Fail(context.Background(), model.FailTranscriptionJob{
			JobID: created.JobID, LeaseToken: second.LeaseToken, ErrorCode: "provider_5xx", NextAttemptAt: secondRetryAt, Now: retryAt + 1,
		}); err != nil {
			t.Fatalf("schedule second retry: %v", err)
		}
		at := secondRetryAt
		var failed *model.TranscriptionJob
		for attempt := int64(3); attempt <= 6; attempt++ {
			lease := claim(attempt, at)
			next := at + 120
			failed, err = worker.Fail(context.Background(), model.FailTranscriptionJob{
				JobID: created.JobID, LeaseToken: lease.LeaseToken, ErrorCode: "provider_5xx", NextAttemptAt: next, Now: at + 1,
			})
			if err != nil {
				t.Fatalf("failure attempt %d: %v", attempt, err)
			}
			at = next
		}
		if failed.State != model.TranscriptionJobFailed || failed.NextAttemptAt != nil || failed.ErrorCode != "provider_5xx" {
			t.Fatalf("failed job = %+v", failed)
		}
		voiceAfter, err := mobileRepository.GetEntityByClientID(ctx, "voice_note", voiceClientID)
		if err != nil {
			t.Fatal(err)
		}
		if voiceAfter.Revision != voiceBefore.Revision+1 {
			t.Fatalf("voice revision after terminal failure = %d, want %d", voiceAfter.Revision, voiceBefore.Revision+1)
		}
		changesAfter, err := mobileRepository.ListPendingChanges(ctx)
		if err != nil {
			t.Fatal(err)
		}
		terminalChanges := changesAfter[len(changesBefore):]
		if len(terminalChanges) != 2 {
			t.Fatalf("terminal failure changes = %d, want 2: %+v", len(terminalChanges), terminalChanges)
		}
		jobChangeFound := false
		voiceChangeFound := false
		for _, change := range terminalChanges {
			switch change.Entity.EntityType {
			case "transcription_job":
				jobChangeFound = change.Operation == "transcription_job.failed" && change.Entity.ClientID == created.JobID
			case "voice_note":
				voiceChangeFound = change.Operation == "voice.server_updated" && change.Entity.ClientID == voiceClientID
			}
		}
		if !jobChangeFound || !voiceChangeFound {
			t.Fatalf("terminal failure changes = %+v", terminalChanges)
		}
		if _, err := worker.ClaimNext(context.Background(), model.ClaimTranscriptionJob{
			WorkerID: "after-terminal", LeaseToken: "after-terminal", Now: at + 1000, LeaseExpiresAt: at + 1120,
		}); !errors.Is(err, storage.ErrNoTranscriptionJob) {
			t.Fatalf("terminal job was claimable: %v", err)
		}
	})

	t.Run("TRANS-RETRY-002_AutomaticExecutionIsInitialPlusExactlyFiveRetries", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		ctx := scopedContractContext(t, store)
		voiceClientID := seedUploadedVoiceNote(t, ctx, store)
		now := time.Now().UTC().Unix()
		created, err := transcriptionJobRepository(t, store).CreateOrGet(ctx, model.CreateTranscriptionJob{
			JobID: uuid.NewString(), MutationID: uuid.NewString(), RequestSHA256: "six-attempt-request",
			VoiceNoteID: voiceClientID, Language: "zh", Now: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		worker := transcriptionJobWorkerRepository(t, store)
		at := now
		for attempt := int64(1); attempt <= 6; attempt++ {
			lease, err := worker.ClaimNext(context.Background(), model.ClaimTranscriptionJob{
				WorkerID: "six-attempt-worker", LeaseToken: fmt.Sprintf("six-attempt-lease-%d", attempt),
				Now: at, LeaseExpiresAt: at + 120,
			})
			if err != nil {
				t.Fatalf("claim attempt %d: %v", attempt, err)
			}
			if lease.Job.Attempt != attempt || lease.Job.JobID != created.JobID {
				t.Fatalf("claim attempt %d returned %+v", attempt, lease.Job)
			}
			next := at + 1
			failed, err := worker.Fail(context.Background(), model.FailTranscriptionJob{
				JobID: created.JobID, LeaseToken: lease.LeaseToken, ErrorCode: "provider_failed",
				NextAttemptAt: next, Now: at,
			})
			if err != nil {
				t.Fatalf("fail attempt %d: %v", attempt, err)
			}
			if attempt < 6 && failed.State != model.TranscriptionJobRetryWaiting {
				t.Fatalf("attempt %d state = %q, want retry_waiting", attempt, failed.State)
			}
			if attempt == 6 && failed.State != model.TranscriptionJobFailed {
				t.Fatalf("attempt 6 state = %q, want failed", failed.State)
			}
			at = next
		}
		if _, err := worker.ClaimNext(context.Background(), model.ClaimTranscriptionJob{
			WorkerID: "seventh-attempt-worker", LeaseToken: "seventh-attempt-lease", Now: at + 100,
			LeaseExpiresAt: at + 220,
		}); !errors.Is(err, storage.ErrNoTranscriptionJob) {
			t.Fatalf("seventh attempt error = %v, want no job", err)
		}
	})

	t.Run("TRANS-MANUAL-RETRY-001_TerminalFailureCreatesOneIdempotentGeneration", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		ctx := scopedContractContext(t, store)
		voiceClientID := seedUploadedVoiceNote(t, ctx, store)
		repository := transcriptionJobRepository(t, store)
		now := time.Now().UTC().Unix()
		created, err := repository.CreateOrGet(ctx, model.CreateTranscriptionJob{
			JobID: "cdcdcdcd-1111-4111-8111-cdcdcdcdcdcd", MutationID: "cdcdcdcd-2222-4222-8222-cdcdcdcdcdcd",
			RequestSHA256: "manual-retry-source", VoiceNoteID: voiceClientID, Language: "zh", Now: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		retryRequest := model.RetryTranscriptionJob{
			JobID: "cdcdcdcd-3333-4333-8333-cdcdcdcdcdcd", MutationID: "cdcdcdcd-4444-4444-8444-cdcdcdcdcdcd",
			RequestSHA256: "manual-retry-a", FailedJobID: created.JobID, Now: now + 10,
		}
		if _, err := repository.Retry(ctx, retryRequest); !errors.Is(err, storage.ErrTranscriptionJobNotRetryable) {
			t.Fatalf("retry active job error = %v, want not retryable", err)
		}
		worker := transcriptionJobWorkerRepository(t, store)
		at := now
		for attempt := int64(1); attempt <= 6; attempt++ {
			lease, err := worker.ClaimNext(context.Background(), model.ClaimTranscriptionJob{
				WorkerID: "manual-retry-worker", LeaseToken: fmt.Sprintf("manual-retry-lease-%d", attempt),
				Now: at, LeaseExpiresAt: at + 120,
			})
			if err != nil {
				t.Fatalf("claim attempt %d: %v", attempt, err)
			}
			next := at + 1
			if _, err := worker.Fail(context.Background(), model.FailTranscriptionJob{
				JobID: created.JobID, LeaseToken: lease.LeaseToken, ErrorCode: "provider_failed",
				NextAttemptAt: next, Now: at,
			}); err != nil {
				t.Fatalf("fail attempt %d: %v", attempt, err)
			}
			at = next
		}
		first, err := repository.Retry(ctx, retryRequest)
		if err != nil {
			t.Fatalf("manual retry: %v", err)
		}
		if first.JobID != retryRequest.JobID || first.Generation != 2 || first.State != model.TranscriptionJobQueued || first.Attempt != 0 {
			t.Fatalf("manual retry job = %+v", first)
		}
		replayed, err := repository.Retry(ctx, retryRequest)
		if err != nil {
			t.Fatalf("manual retry replay: %v", err)
		}
		firstJSON, _ := json.Marshal(first)
		replayedJSON, _ := json.Marshal(replayed)
		if !bytes.Equal(firstJSON, replayedJSON) {
			t.Fatalf("retry replay = %s, want %s", replayedJSON, firstJSON)
		}
		changed := retryRequest
		changed.RequestSHA256 = "manual-retry-b"
		if _, err := repository.Retry(ctx, changed); !errors.Is(err, storage.ErrMutationIDReused) {
			t.Fatalf("changed retry replay error = %v, want mutation reused", err)
		}
		for attempt := int64(1); attempt <= 6; attempt++ {
			lease, err := worker.ClaimNext(context.Background(), model.ClaimTranscriptionJob{
				WorkerID: "second-generation-worker", LeaseToken: fmt.Sprintf("second-generation-lease-%d", attempt),
				Now: at, LeaseExpiresAt: at + 120,
			})
			if err != nil {
				t.Fatalf("claim second generation attempt %d: %v", attempt, err)
			}
			next := at + 1
			if _, err := worker.Fail(context.Background(), model.FailTranscriptionJob{
				JobID: first.JobID, LeaseToken: lease.LeaseToken, ErrorCode: "provider_failed",
				NextAttemptAt: next, Now: at,
			}); err != nil {
				t.Fatalf("fail second generation attempt %d: %v", attempt, err)
			}
			at = next
		}
		stale := model.RetryTranscriptionJob{
			JobID: uuid.NewString(), MutationID: uuid.NewString(), RequestSHA256: "stale-manual-retry",
			FailedJobID: created.JobID, Now: at,
		}
		if _, err := repository.Retry(ctx, stale); !errors.Is(err, storage.ErrStaleTranscriptionJob) {
			t.Fatalf("retry stale generation error = %v, want stale job", err)
		}
		latest := model.RetryTranscriptionJob{
			JobID: uuid.NewString(), MutationID: uuid.NewString(), RequestSHA256: "latest-manual-retry",
			FailedJobID: first.JobID, Now: at,
		}
		thirdGeneration, err := repository.Retry(ctx, latest)
		if err != nil {
			t.Fatalf("retry latest failed generation: %v", err)
		}
		if thirdGeneration.Generation != 3 || thirdGeneration.State != model.TranscriptionJobQueued {
			t.Fatalf("third generation = %+v", thirdGeneration)
		}
	})

	t.Run("TRANS-CAS-001_CompletionDoesNotOverwriteUserBody", func(t *testing.T) {
		for _, testCase := range []struct {
			name      string
			userBody  string
			wantState string
			wantBody  string
		}{
			{name: "empty body applies transcript", wantState: model.TranscriptionJobCompleted, wantBody: "Synthetic transcript"},
			{name: "edited body needs review", userBody: "User edited body", wantState: model.TranscriptionJobNeedsReview, wantBody: "User edited body"},
		} {
			t.Run(testCase.name, func(t *testing.T) {
				store := factory(t)
				defer store.Close()
				ctx := scopedContractContext(t, store)
				voiceClientID, noteID := seedUploadedVoiceNoteWithBackingID(t, ctx, store)
				if testCase.userBody != "" {
					if _, err := store.Notes().Update(ctx, noteID, &model.UpdateNoteRequest{Body: &testCase.userBody}); err != nil {
						t.Fatalf("edit note body: %v", err)
					}
				}
				now := time.Now().UTC().Unix()
				created, err := transcriptionJobRepository(t, store).CreateOrGet(ctx, model.CreateTranscriptionJob{
					JobID: uuid.NewString(), MutationID: uuid.NewString(), RequestSHA256: "completion-request-" + testCase.name,
					VoiceNoteID: voiceClientID, Language: "zh", Now: now,
				})
				if err != nil {
					t.Fatal(err)
				}
				mobileRepository, err := storage.MobileSyncRepositoryFrom(store)
				if err != nil {
					t.Fatal(err)
				}
				beforeChanges, err := mobileRepository.ListPendingChanges(ctx)
				if err != nil {
					t.Fatal(err)
				}
				voiceBefore, err := mobileRepository.GetEntityByClientID(ctx, "voice_note", voiceClientID)
				if err != nil {
					t.Fatal(err)
				}
				worker := transcriptionJobWorkerRepository(t, store)
				lease, err := worker.ClaimNext(context.Background(), model.ClaimTranscriptionJob{
					WorkerID: "complete-worker", LeaseToken: "complete-lease", Now: now, LeaseExpiresAt: now + 120,
				})
				if err != nil {
					t.Fatal(err)
				}
				completed, err := worker.Complete(context.Background(), model.CompleteTranscriptionJob{
					JobID: created.JobID, LeaseToken: lease.LeaseToken, Text: "Synthetic transcript", Now: now + 1,
				})
				if err != nil {
					t.Fatalf("complete job: %v", err)
				}
				if completed.State != testCase.wantState {
					t.Fatalf("state = %q, want %q", completed.State, testCase.wantState)
				}
				note, err := store.Notes().GetByID(ctx, noteID)
				if err != nil {
					t.Fatal(err)
				}
				if note.Body != testCase.wantBody {
					t.Fatalf("note body = %q, want %q", note.Body, testCase.wantBody)
				}
				afterChanges, err := mobileRepository.ListPendingChanges(ctx)
				if err != nil {
					t.Fatal(err)
				}
				wantChangeCount := len(beforeChanges) + 2
				if testCase.wantState == model.TranscriptionJobCompleted {
					wantChangeCount++
				}
				if len(afterChanges) != wantChangeCount {
					t.Fatalf("pending changes after completion = %d, want %d: %+v", len(afterChanges), wantChangeCount, afterChanges)
				}
				newChanges := afterChanges[len(beforeChanges):]
				jobChangeFound := false
				noteChangeFound := false
				voiceChangeFound := false
				for _, change := range newChanges {
					switch change.Entity.EntityType {
					case "transcription_job":
						jobChangeFound = change.Operation == "transcription_job.completed" && change.Entity.ClientID == created.JobID
						var payload map[string]any
						if err := json.Unmarshal(change.Entity.Payload, &payload); err != nil {
							t.Fatalf("decode job change: %v", err)
						}
						if payload["state"] != testCase.wantState {
							t.Fatalf("job change state = %v, want %q", payload["state"], testCase.wantState)
						}
					case "note":
						noteChangeFound = change.Operation == "note.transcription_applied"
					case "voice_note":
						voiceChangeFound = change.Operation == "voice.server_updated" && change.Entity.ClientID == voiceClientID
					}
				}
				if !jobChangeFound || !voiceChangeFound || (testCase.wantState == model.TranscriptionJobCompleted) != noteChangeFound {
					t.Fatalf("completion changes = %+v", newChanges)
				}
				voiceAfter, err := mobileRepository.GetEntityByClientID(ctx, "voice_note", voiceClientID)
				if err != nil {
					t.Fatal(err)
				}
				if voiceAfter.Revision != voiceBefore.Revision+1 {
					t.Fatalf("voice revision after completion = %d, want %d", voiceAfter.Revision, voiceBefore.Revision+1)
				}
				if _, err := worker.Complete(context.Background(), model.CompleteTranscriptionJob{
					JobID: created.JobID, LeaseToken: lease.LeaseToken, Text: "Stale overwrite", Now: now + 2,
				}); !errors.Is(err, storage.ErrTranscriptionLeaseLost) {
					t.Fatalf("stale completion error = %v, want lease lost", err)
				}
			})
		}
	})
}

func seedUploadedVoiceNote(t *testing.T, ctx context.Context, store storage.Store) string {
	clientID, _ := seedUploadedVoiceNoteWithBackingID(t, ctx, store)
	return clientID
}

func seedPendingVoiceNote(t *testing.T, ctx context.Context, store storage.Store) string {
	t.Helper()
	note, err := store.Notes().Create(ctx, &model.CreateNoteRequest{Title: "Pending synthetic voice note", FolderID: "__uncategorized", Tags: "[]"})
	if err != nil {
		t.Fatalf("create voice backing note: %v", err)
	}
	nativeStore, err := storage.NativeStoreFrom(store)
	if err != nil {
		t.Fatalf("native store: %v", err)
	}
	now := time.Now().UTC().Unix()
	clientID := uuid.NewString()
	voice := &model.VoiceNote{
		ID: uuid.NewString(), ClientID: clientID, NoteID: note.ID, DurationMS: 1000, RecordedAt: now,
		Language: "zh", UploadState: model.VoiceUploadPending, TranscriptionState: model.TranscriptionNotStarted,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := nativeStore.VoiceNotes().Create(ctx, voice); err != nil {
		t.Fatalf("create pending voice note: %v", err)
	}
	return clientID
}

func seedUploadedVoiceNoteWithBackingID(t *testing.T, ctx context.Context, store storage.Store) (string, string) {
	t.Helper()
	note, err := store.Notes().Create(ctx, &model.CreateNoteRequest{Title: "Synthetic voice note", FolderID: "__uncategorized", Tags: "[]"})
	if err != nil {
		t.Fatalf("create voice backing note: %v", err)
	}
	nativeStore, err := storage.NativeStoreFrom(store)
	if err != nil {
		t.Fatalf("native store: %v", err)
	}
	now := time.Now().UTC().Unix()
	clientID := uuid.NewString()
	voice := &model.VoiceNote{
		ID: uuid.NewString(), ClientID: clientID, NoteID: note.ID, DurationMS: 1000, RecordedAt: now,
		Language: "zh", ObjectKey: "test/voice.m4a", MimeType: "audio/mp4", AudioSize: 5,
		AudioSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		UploadState: model.VoiceUploadUploaded, TranscriptionState: model.TranscriptionNotStarted,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := nativeStore.VoiceNotes().Create(ctx, voice); err != nil {
		t.Fatalf("create uploaded voice note: %v", err)
	}
	return clientID, note.ID
}

func transcriptionJobRepository(t *testing.T, store storage.Store) storage.TranscriptionJobRepository {
	t.Helper()
	repository, err := storage.TranscriptionJobRepositoryFrom(store)
	if err != nil {
		t.Fatalf("transcription job repository: %v", err)
	}
	return repository
}

func transcriptionJobWorkerRepository(t *testing.T, store storage.Store) storage.TranscriptionJobWorkerRepository {
	t.Helper()
	repository, err := storage.TranscriptionJobWorkerRepositoryFrom(store)
	if err != nil {
		t.Fatalf("transcription job worker repository: %v", err)
	}
	return repository
}
