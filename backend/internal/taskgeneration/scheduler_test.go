package taskgeneration

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/generationclaims"
	"github.com/hujinrun/flowspace/internal/storage"
	storagesqlite "github.com/hujinrun/flowspace/internal/storage/sqlite"
	"github.com/hujinrun/flowspace/internal/taskdomain"
	_ "modernc.org/sqlite"
)

func TestSchedulerPeriodicallyCompensatesStableV2WorkspaceSQLite(t *testing.T) {
	repository, db := schedulerSQLiteRepository(t, time.Minute)
	seedSchedulerWorkspace(t, db, "stable-v2")
	now := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)
	source := &stableWorkspaceSourceStub{workspaces: []StableV2Workspace{{WorkspaceID: "stable-v2", Epoch: 7}}}
	scheduler, err := NewScheduler(source, repository, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	result, err := scheduler.Reconcile(t.Context(), now)
	if err != nil || result.Scheduled != 1 || result.Active != 0 {
		t.Fatalf("initial reconcile=%#v err=%v", result, err)
	}
	claim := claimScheduledWorkspace(t, repository, now)
	if claim.CreatedEpoch != 7 {
		t.Fatalf("created epoch=%d", claim.CreatedEpoch)
	}
	if err := repository.CompleteGenerationClaim(t.Context(), taskdomain.GenerationClaimOutcome{
		ClaimID: claim.ClaimID, WorkspaceID: claim.WorkspaceID, CreatedEpoch: claim.CreatedEpoch,
		RuntimeEpoch: 11, Status: taskdomain.GenerationStatusIdle,
	}); err != nil {
		t.Fatal(err)
	}

	// No task command has to enqueue the next cycle. The periodic reconciliation
	// is the correctness backstop for missed best-effort nudges.
	result, err = scheduler.Reconcile(t.Context(), now.Add(15*time.Minute))
	if err != nil || result.Scheduled != 1 {
		t.Fatalf("compensation reconcile=%#v err=%v", result, err)
	}
	job, err := repository.Get(t.Context(), "stable-v2")
	if err != nil || job.Status != generationclaims.StatusQueued || job.CreatedEpoch != 7 {
		t.Fatalf("compensated job=%#v err=%v", job, err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM task_domain_generation_jobs WHERE workspace_id='stable-v2'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("job count=%d err=%v", count, err)
	}

	// Once the durable state source no longer classifies the workspace as stable
	// v2, reconciliation must not create or requeue work for it.
	source.workspaces = nil
	claim = claimScheduledWorkspace(t, repository, now.Add(15*time.Minute))
	if err := repository.CompleteGenerationClaim(t.Context(), taskdomain.GenerationClaimOutcome{
		ClaimID: claim.ClaimID, WorkspaceID: claim.WorkspaceID, CreatedEpoch: claim.CreatedEpoch,
		RuntimeEpoch: 12, Status: taskdomain.GenerationStatusIdle,
	}); err != nil {
		t.Fatal(err)
	}
	result, err = scheduler.Reconcile(t.Context(), now.Add(30*time.Minute))
	if err != nil || result.Scheduled != 0 {
		t.Fatalf("non-v2 reconcile=%#v err=%v", result, err)
	}
	job, _ = repository.Get(t.Context(), "stable-v2")
	if job.Status != generationclaims.StatusCompleted {
		t.Fatalf("non-v2 workspace was rescheduled: %#v", job)
	}
}

func TestSchedulerNudgeDoesNotStealClaimAndUsesEpochOnlyForNewCycle(t *testing.T) {
	repository, db := schedulerSQLiteRepository(t, time.Minute)
	seedSchedulerWorkspace(t, db, "workspace-1")
	now := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)
	scheduler, _ := NewScheduler(&stableWorkspaceSourceStub{}, repository, time.Hour)
	if err := scheduler.Nudge(t.Context(), "workspace-1", 4, now); err != nil {
		t.Fatal(err)
	}
	claim := claimScheduledWorkspace(t, repository, now)
	if err := scheduler.Nudge(t.Context(), "workspace-1", 5, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	job, _ := repository.Get(t.Context(), "workspace-1")
	if job.Status != generationclaims.StatusClaimed || job.CreatedEpoch != 4 || job.ClaimID != claim.ClaimID {
		t.Fatalf("nudge stole live claim: %#v", job)
	}
	if err := repository.CompleteGenerationClaim(t.Context(), taskdomain.GenerationClaimOutcome{
		ClaimID: claim.ClaimID, WorkspaceID: "workspace-1", CreatedEpoch: 4,
		RuntimeEpoch: 9, Status: taskdomain.GenerationStatusIdle,
	}); err != nil {
		t.Fatal(err)
	}
	if err := scheduler.Nudge(t.Context(), "workspace-1", 5, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	job, _ = repository.Get(t.Context(), "workspace-1")
	if job.Status != generationclaims.StatusQueued || job.CreatedEpoch != 5 {
		t.Fatalf("new cycle did not capture audit epoch: %#v", job)
	}
}

func TestGenerationWorkerRetriesAfterAcknowledgementFailureSQLite(t *testing.T) {
	repository, db := schedulerSQLiteRepository(t, time.Minute)
	seedSchedulerWorkspace(t, db, "workspace-ack")
	now := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)
	if _, err := repository.EnsureScheduled(t.Context(), generationclaims.EnqueueRequest{
		JobID: "ack-cycle", WorkspaceID: "workspace-ack", CreatedEpoch: 3, AvailableAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	claims := &failFirstAcknowledgement{delegate: repository}
	fencer := &countingGenerationFencer{}
	worker := taskdomain.NewGenerationWorker(claims, generationRuntimeStub{snapshot: taskdomain.GenerationRuntimeSnapshot{
		WorkspaceID: "workspace-ack", Epoch: 8, Fencer: fencer,
	}})

	first, err := worker.RunBatch(t.Context(), taskdomain.GenerationBatchRequest{Limit: 1, Now: now})
	if err != nil || len(first) != 1 || first[0].Acknowledged || !errors.Is(first[0].Error, taskdomain.ErrGenerationClaimAck) {
		t.Fatalf("first batch=%#v err=%v", first, err)
	}
	job, _ := repository.Get(t.Context(), "workspace-ack")
	if job.Status != generationclaims.StatusClaimed || fencer.calls != 1 {
		t.Fatalf("unacknowledged job=%#v writes=%d", job, fencer.calls)
	}
	second, err := worker.RunBatch(t.Context(), taskdomain.GenerationBatchRequest{Limit: 1, Now: job.LeaseUntil})
	if err != nil || len(second) != 1 || !second[0].Acknowledged {
		t.Fatalf("retry batch=%#v err=%v", second, err)
	}
	job, _ = repository.Get(t.Context(), "workspace-ack")
	if job.Status != generationclaims.StatusCompleted || job.Attempt != 2 || fencer.calls != 2 {
		t.Fatalf("retried job=%#v writes=%d", job, fencer.calls)
	}
}

func TestSchedulerRejectsMalformedStableWorkspaceSnapshot(t *testing.T) {
	repository, _ := schedulerSQLiteRepository(t, time.Minute)
	scheduler, _ := NewScheduler(&stableWorkspaceSourceStub{workspaces: []StableV2Workspace{
		{WorkspaceID: "duplicate", Epoch: 1}, {WorkspaceID: "duplicate", Epoch: 2},
	}}, repository, time.Minute)
	if _, err := scheduler.Reconcile(t.Context(), time.Now()); !errors.Is(err, ErrInvalidStableWorkspace) {
		t.Fatalf("duplicate durable workspace accepted: %v", err)
	}
}

type stableWorkspaceSourceStub struct {
	workspaces []StableV2Workspace
	err        error
}

func (s *stableWorkspaceSourceStub) ListStableV2Workspaces(context.Context) ([]StableV2Workspace, error) {
	return append([]StableV2Workspace(nil), s.workspaces...), s.err
}

type failFirstAcknowledgement struct {
	delegate *generationclaims.Repository
	failed   bool
}

func (s *failFirstAcknowledgement) ClaimGenerationWorkspaces(ctx context.Context, limit int, at time.Time) ([]taskdomain.GenerationWorkspaceClaim, error) {
	return s.delegate.ClaimGenerationWorkspaces(ctx, limit, at)
}

func (s *failFirstAcknowledgement) CompleteGenerationClaim(ctx context.Context, outcome taskdomain.GenerationClaimOutcome) error {
	if !s.failed {
		s.failed = true
		return errors.New("simulated control acknowledgement outage")
	}
	return s.delegate.CompleteGenerationClaim(ctx, outcome)
}

type generationRuntimeStub struct {
	snapshot taskdomain.GenerationRuntimeSnapshot
}

func (s generationRuntimeStub) ResolveGenerationRuntime(context.Context, string) (taskdomain.GenerationRuntimeSnapshot, error) {
	return s.snapshot, nil
}

type countingGenerationFencer struct {
	mu    sync.Mutex
	calls int
}

func (f *countingGenerationFencer) BeginGenerationWrite(_ context.Context, _ string, _ int64, callback func(taskdomain.GenerationStateReader, taskdomain.GenerationWriter) error) error {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	return callback(emptyGenerationReader{}, noopGenerationWriter{})
}

type emptyGenerationReader struct{}

func (emptyGenerationReader) ListGenerationTargets(context.Context) ([]taskdomain.GenerationTargetState, error) {
	return nil, nil
}

type noopGenerationWriter struct{}

func (noopGenerationWriter) InsertMissingOccurrences(context.Context, taskdomain.GenerationInsert) error {
	return nil
}

func (noopGenerationWriter) CompleteGeneration(context.Context, taskdomain.GenerationCompletion) error {
	return nil
}

func schedulerSQLiteRepository(t *testing.T, lease time.Duration) (*generationclaims.Repository, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "control.db")
	cfg := storage.Config{Env: "test", Driver: storage.DriverSQLite, SQLitePath: path}
	if err := (storagesqlite.Provider{}).MigrateControl(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repository, err := generationclaims.New(db, generationclaims.DialectSQLite, generationclaims.WithLeaseDuration(lease))
	if err != nil {
		t.Fatal(err)
	}
	return repository, db
}

func seedSchedulerWorkspace(t *testing.T, db *sql.DB, workspaceID string) {
	t.Helper()
	userID := "user-" + workspaceID
	if _, err := db.Exec(`INSERT INTO users(id,email,password_hash) VALUES(?,?,?)`, userID, userID+"@example.test", "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO workspaces(id,name,owner_user_id) VALUES(?,?,?)`, workspaceID, workspaceID, userID); err != nil {
		t.Fatal(err)
	}
}

func claimScheduledWorkspace(t *testing.T, repository *generationclaims.Repository, at time.Time) taskdomain.GenerationWorkspaceClaim {
	t.Helper()
	claims, err := repository.ClaimGenerationWorkspaces(t.Context(), 1, at)
	if err != nil || len(claims) != 1 {
		t.Fatalf("claims=%#v err=%v", claims, err)
	}
	return claims[0]
}
