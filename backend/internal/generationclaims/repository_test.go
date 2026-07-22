package generationclaims

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/storage"
	storagesqlite "github.com/hujinrun/flowspace/internal/storage/sqlite"
	"github.com/hujinrun/flowspace/internal/taskdomain"
	"github.com/hujinrun/flowspace/internal/testsupport"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

var _ taskdomain.WorkspaceClaimSource = (*Repository)(nil)

func TestRepositoryClaimAndCompleteContractSQLite(t *testing.T) {
	t.Run("claims due work in stable order and leaves future work queued", func(t *testing.T) {
		repository, db := newSQLiteRepository(t)
		now := time.Date(2026, 7, 22, 8, 0, 0, 0, time.UTC)
		seedWorkspace(t, db, "w1")
		seedWorkspace(t, db, "w2")
		seedWorkspace(t, db, "w3")
		mustEnqueue(t, repository, EnqueueRequest{JobID: "j2", WorkspaceID: "w2", CreatedEpoch: 12, AvailableAt: now.Add(-time.Minute)})
		mustEnqueue(t, repository, EnqueueRequest{JobID: "j1", WorkspaceID: "w1", CreatedEpoch: 11, AvailableAt: now.Add(-2 * time.Minute)})
		mustEnqueue(t, repository, EnqueueRequest{JobID: "j3", WorkspaceID: "w3", CreatedEpoch: 13, AvailableAt: now.Add(time.Minute)})

		claims, err := repository.ClaimGenerationWorkspaces(context.Background(), 2, now)
		if err != nil {
			t.Fatal(err)
		}
		if len(claims) != 2 || claims[0].WorkspaceID != "w1" || claims[1].WorkspaceID != "w2" {
			t.Fatalf("claims=%#v", claims)
		}
		if claims[0].ClaimID == "" || claims[0].ClaimID == claims[1].ClaimID || claims[0].CreatedEpoch != 11 {
			t.Fatalf("invalid claim identities: %#v", claims)
		}
		future, err := repository.Get(context.Background(), "w3")
		if err != nil || future.Status != StatusQueued {
			t.Fatalf("future job=%#v err=%v", future, err)
		}
	})

	t.Run("idle acknowledgement completes and is idempotent", func(t *testing.T) {
		repository, db := newSQLiteRepository(t)
		now := time.Date(2026, 7, 22, 8, 0, 0, 0, time.UTC)
		seedWorkspace(t, db, "w1")
		mustEnqueue(t, repository, EnqueueRequest{JobID: "j1", WorkspaceID: "w1", CreatedEpoch: 4, AvailableAt: now})
		claim := mustClaimOne(t, repository, now)
		outcome := taskdomain.GenerationClaimOutcome{
			ClaimID: claim.ClaimID, WorkspaceID: "w1", CreatedEpoch: 4, RuntimeEpoch: 9,
			Status: taskdomain.GenerationStatusIdle, Inserted: 7, GenerationWatermark: "2026-10-20",
		}
		if err := repository.CompleteGenerationClaim(context.Background(), outcome); err != nil {
			t.Fatal(err)
		}
		if err := repository.CompleteGenerationClaim(context.Background(), outcome); err != nil {
			t.Fatalf("repeat acknowledgement: %v", err)
		}
		job, err := repository.Get(context.Background(), "w1")
		if err != nil {
			t.Fatal(err)
		}
		if job.Status != StatusCompleted || job.RuntimeEpoch != 9 || job.Inserted != 7 || job.GenerationWatermark != "2026-10-20" || job.ErrorCode != "" || !job.LeaseUntil.IsZero() {
			t.Fatalf("completed job=%#v", job)
		}
		conflict := outcome
		conflict.Inserted++
		if err := repository.CompleteGenerationClaim(context.Background(), conflict); !errors.Is(err, ErrClaimConflict) {
			t.Fatalf("conflicting repeat error=%v", err)
		}
	})

	t.Run("retry acknowledgement requeues at retry time and expired leases are reclaimed", func(t *testing.T) {
		repository, db := newSQLiteRepository(t)
		now := time.Date(2026, 7, 22, 8, 0, 0, 0, time.UTC)
		seedWorkspace(t, db, "w1")
		mustEnqueue(t, repository, EnqueueRequest{JobID: "j1", WorkspaceID: "w1", CreatedEpoch: 5, AvailableAt: now})
		first := mustClaimOne(t, repository, now)
		retryAt := now.Add(2 * time.Minute)
		outcome := taskdomain.GenerationClaimOutcome{
			ClaimID: first.ClaimID, WorkspaceID: "w1", CreatedEpoch: 5,
			Status: taskdomain.GenerationStatusRetryPending, RetryAt: retryAt,
			ErrorCode: taskdomain.GenerationClaimErrorRuntimeResolve,
		}
		if err := repository.CompleteGenerationClaim(context.Background(), outcome); err != nil {
			t.Fatal(err)
		}
		if claims, err := repository.ClaimGenerationWorkspaces(context.Background(), 1, retryAt.Add(-time.Second)); err != nil || len(claims) != 0 {
			t.Fatalf("early claims=%#v err=%v", claims, err)
		}
		second := mustClaimOne(t, repository, retryAt)
		if second.ClaimID == first.ClaimID {
			t.Fatalf("claim id was reused: %#v", second)
		}
		job, _ := repository.Get(context.Background(), "w1")
		if job.Attempt != 2 || job.Status != StatusClaimed {
			t.Fatalf("reclaimed job=%#v", job)
		}
		third := mustClaimOne(t, repository, job.LeaseUntil)
		if third.ClaimID == second.ClaimID {
			t.Fatalf("expired lease retained claim id: %#v", third)
		}
	})

	t.Run("failed acknowledgement is terminal", func(t *testing.T) {
		repository, db := newSQLiteRepository(t)
		now := time.Date(2026, 7, 22, 8, 0, 0, 0, time.UTC)
		seedWorkspace(t, db, "w1")
		mustEnqueue(t, repository, EnqueueRequest{JobID: "j1", WorkspaceID: "w1", CreatedEpoch: 5, AvailableAt: now})
		claim := mustClaimOne(t, repository, now)
		outcome := taskdomain.GenerationClaimOutcome{
			ClaimID: claim.ClaimID, WorkspaceID: "w1", CreatedEpoch: 5,
			Status: taskdomain.GenerationStatusFailed, ErrorCode: taskdomain.GenerationClaimErrorInvalidRuntime,
		}
		if err := repository.CompleteGenerationClaim(context.Background(), outcome); err != nil {
			t.Fatal(err)
		}
		claims, err := repository.ClaimGenerationWorkspaces(context.Background(), 1, now.Add(24*time.Hour))
		if err != nil || len(claims) != 0 {
			t.Fatalf("terminal claim=%#v err=%v", claims, err)
		}
	})
}

func TestRepositoryMaintenanceAndValidationSQLite(t *testing.T) {
	repository, db := newSQLiteRepository(t)
	now := time.Date(2026, 7, 22, 8, 0, 0, 123456789, time.UTC)
	seedWorkspace(t, db, "w1")
	if err := repository.Enqueue(context.Background(), EnqueueRequest{JobID: "j1", WorkspaceID: "w1", CreatedEpoch: 3, AvailableAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := repository.Enqueue(context.Background(), EnqueueRequest{JobID: "j1", WorkspaceID: "w1", CreatedEpoch: 3, AvailableAt: now}); err != nil {
		t.Fatalf("idempotent enqueue: %v", err)
	}
	if err := repository.Enqueue(context.Background(), EnqueueRequest{JobID: "different", WorkspaceID: "w1", CreatedEpoch: 3, AvailableAt: now}); !errors.Is(err, ErrJobConflict) {
		t.Fatalf("conflicting enqueue error=%v", err)
	}
	claim := mustClaimOne(t, repository, now.Add(time.Second))
	if err := repository.Reschedule(context.Background(), RescheduleRequest{WorkspaceID: "w1", CreatedEpoch: 4, AvailableAt: now.Add(time.Hour)}); !errors.Is(err, ErrClaimConflict) {
		t.Fatalf("reschedule claimed error=%v", err)
	}
	if err := repository.CompleteGenerationClaim(context.Background(), taskdomain.GenerationClaimOutcome{
		ClaimID: claim.ClaimID, WorkspaceID: "w1", CreatedEpoch: 3, RuntimeEpoch: 7, Status: taskdomain.GenerationStatusIdle,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repository.Reschedule(context.Background(), RescheduleRequest{WorkspaceID: "w1", CreatedEpoch: 4, AvailableAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	job, _ := repository.Get(context.Background(), "w1")
	if job.Status != StatusQueued || job.CreatedEpoch != 4 || job.Attempt != 0 || job.ClaimID != "" || job.RuntimeEpoch != 0 || job.Revision < 4 {
		t.Fatalf("rescheduled job=%#v", job)
	}

	invalidOutcomes := []taskdomain.GenerationClaimOutcome{
		{ClaimID: "c", WorkspaceID: "w1", CreatedEpoch: 1, Status: taskdomain.GenerationStatusRunning},
		{ClaimID: "c", WorkspaceID: "w1", CreatedEpoch: 1, Status: taskdomain.GenerationStatusIdle, ErrorCode: taskdomain.GenerationClaimErrorInvalidRuntime},
		{ClaimID: "c", WorkspaceID: "w1", CreatedEpoch: 1, Status: taskdomain.GenerationStatusRetryPending},
		{ClaimID: "c", WorkspaceID: "w1", CreatedEpoch: 1, Status: taskdomain.GenerationStatusFailed, ErrorCode: taskdomain.GenerationClaimErrorCode("raw secret error")},
	}
	for _, outcome := range invalidOutcomes {
		if err := repository.CompleteGenerationClaim(context.Background(), outcome); !errors.Is(err, ErrInvalidClaim) {
			t.Fatalf("outcome=%#v error=%v", outcome, err)
		}
	}
}

func TestRepositoryEnsureScheduledSQLite(t *testing.T) {
	repository, db := newSQLiteRepository(t)
	now := time.Date(2026, 7, 22, 8, 0, 0, 0, time.UTC)
	seedWorkspace(t, db, "w1")

	scheduled, err := repository.EnsureScheduled(context.Background(), EnqueueRequest{
		JobID: "cycle-1", WorkspaceID: "w1", CreatedEpoch: 3, AvailableAt: now,
	})
	if err != nil || !scheduled {
		t.Fatalf("initial schedule: scheduled=%v err=%v", scheduled, err)
	}
	claim := mustClaimOne(t, repository, now)
	scheduled, err = repository.EnsureScheduled(context.Background(), EnqueueRequest{
		JobID: "cycle-2", WorkspaceID: "w1", CreatedEpoch: 4, AvailableAt: now.Add(time.Minute),
	})
	if err != nil || scheduled {
		t.Fatalf("live lease was rescheduled: scheduled=%v err=%v", scheduled, err)
	}
	if err := repository.CompleteGenerationClaim(context.Background(), taskdomain.GenerationClaimOutcome{
		ClaimID: claim.ClaimID, WorkspaceID: "w1", CreatedEpoch: 3, RuntimeEpoch: 9,
		Status: taskdomain.GenerationStatusIdle,
	}); err != nil {
		t.Fatal(err)
	}
	scheduled, err = repository.EnsureScheduled(context.Background(), EnqueueRequest{
		JobID: "cycle-2", WorkspaceID: "w1", CreatedEpoch: 4, AvailableAt: now.Add(time.Minute),
	})
	if err != nil || !scheduled {
		t.Fatalf("completed cycle was not rescheduled: scheduled=%v err=%v", scheduled, err)
	}
	job, err := repository.Get(context.Background(), "w1")
	if err != nil || job.JobID != "cycle-2" || job.Status != StatusQueued || job.CreatedEpoch != 4 ||
		job.Attempt != 0 || job.ClaimID != "" || job.RuntimeEpoch != 0 {
		t.Fatalf("rescheduled job=%#v err=%v", job, err)
	}
}

func TestRepositoryConcurrentClaimDoesNotDoubleLeaseSQLite(t *testing.T) {
	repository, db := newSQLiteRepository(t)
	now := time.Date(2026, 7, 22, 8, 0, 0, 0, time.UTC)
	seedWorkspace(t, db, "w1")
	mustEnqueue(t, repository, EnqueueRequest{JobID: "j1", WorkspaceID: "w1", CreatedEpoch: 1, AvailableAt: now})

	start := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(2)
	claims := make(chan taskdomain.GenerationWorkspaceClaim, 2)
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			defer wait.Done()
			<-start
			batch, err := repository.ClaimGenerationWorkspaces(context.Background(), 1, now)
			if err != nil {
				errs <- err
				return
			}
			for _, claim := range batch {
				claims <- claim
			}
		}()
	}
	close(start)
	wait.Wait()
	close(claims)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var got []taskdomain.GenerationWorkspaceClaim
	for claim := range claims {
		got = append(got, claim)
	}
	if len(got) != 1 {
		t.Fatalf("claims=%#v", got)
	}
}

func TestControlMigrationGenerationJobConstraintsSQLite(t *testing.T) {
	_, db := newSQLiteRepository(t)
	seedWorkspace(t, db, "w1")
	_, err := db.Exec(`INSERT INTO task_domain_generation_jobs(
		job_id,workspace_id,created_epoch,status,attempt,available_at,claim_id,lease_until,error_code,revision
	) VALUES('bad','w1',1,'queued',0,CURRENT_TIMESTAMP,'claim-on-queued',NULL,'raw database error',1)`)
	if err == nil {
		t.Fatal("invalid state/error combination was accepted")
	}
}

func TestPostgresClaimBranchUsesSkipLocked(t *testing.T) {
	if !strings.Contains(postgresClaimOneSQL, "FOR UPDATE SKIP LOCKED") {
		t.Fatalf("postgres claim SQL must use SKIP LOCKED:\n%s", postgresClaimOneSQL)
	}
}

func TestRepositoryClaimAndCompletePostgres(t *testing.T) {
	db := newPostgresControlDB(t)
	repository, err := New(db, DialectPostgres, WithLeaseDuration(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	workspaceID := "generation-pg-w1"
	userID := "generation-pg-u1"
	if _, err := db.Exec(`INSERT INTO users(id,email,password_hash) VALUES($1,$2,'x')`, userID, userID+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO workspaces(id,name,owner_user_id) VALUES($1,$1,$2)`, workspaceID, userID); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 22, 8, 0, 0, 123456789, time.UTC)
	mustEnqueue(t, repository, EnqueueRequest{JobID: "generation-pg-j1", WorkspaceID: workspaceID, CreatedEpoch: 4, AvailableAt: now})
	claim := mustClaimOne(t, repository, now.Add(time.Second))
	outcome := taskdomain.GenerationClaimOutcome{
		ClaimID: claim.ClaimID, WorkspaceID: workspaceID, CreatedEpoch: 4, RuntimeEpoch: 9,
		Status: taskdomain.GenerationStatusIdle, Inserted: 2, GenerationWatermark: "2026-10-20",
	}
	if err := repository.CompleteGenerationClaim(context.Background(), outcome); err != nil {
		t.Fatal(err)
	}
	if err := repository.CompleteGenerationClaim(context.Background(), outcome); err != nil {
		t.Fatalf("idempotent postgres acknowledgement: %v", err)
	}
	job, err := repository.Get(context.Background(), workspaceID)
	if err != nil || job.Status != StatusCompleted || job.Inserted != 2 || job.RuntimeEpoch != 9 {
		t.Fatalf("job=%#v err=%v", job, err)
	}
}

func TestRepositoryEnsureScheduledPostgres(t *testing.T) {
	db := newPostgresControlDB(t)
	repository, err := New(db, DialectPostgres)
	if err != nil {
		t.Fatal(err)
	}
	workspaceID := "generation-schedule-pg-w1"
	userID := "generation-schedule-pg-u1"
	if _, err := db.Exec(`INSERT INTO users(id,email,password_hash) VALUES($1,$2,'x')`, userID, userID+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO workspaces(id,name,owner_user_id) VALUES($1,$1,$2)`, workspaceID, userID); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 22, 8, 0, 0, 0, time.UTC)
	if scheduled, err := repository.EnsureScheduled(context.Background(), EnqueueRequest{
		JobID: "generation-schedule-pg-cycle-1", WorkspaceID: workspaceID, CreatedEpoch: 2, AvailableAt: now,
	}); err != nil || !scheduled {
		t.Fatalf("postgres schedule: scheduled=%v err=%v", scheduled, err)
	}
	claim := mustClaimOne(t, repository, now)
	if scheduled, err := repository.EnsureScheduled(context.Background(), EnqueueRequest{
		JobID: "generation-schedule-pg-cycle-2", WorkspaceID: workspaceID, CreatedEpoch: 3, AvailableAt: now,
	}); err != nil || scheduled {
		t.Fatalf("postgres live claim changed: scheduled=%v err=%v", scheduled, err)
	}
	if err := repository.CompleteGenerationClaim(context.Background(), taskdomain.GenerationClaimOutcome{
		ClaimID: claim.ClaimID, WorkspaceID: workspaceID, CreatedEpoch: 2, RuntimeEpoch: 5,
		Status: taskdomain.GenerationStatusIdle,
	}); err != nil {
		t.Fatal(err)
	}
	if scheduled, err := repository.EnsureScheduled(context.Background(), EnqueueRequest{
		JobID: "generation-schedule-pg-cycle-2", WorkspaceID: workspaceID, CreatedEpoch: 3, AvailableAt: now.Add(time.Minute),
	}); err != nil || !scheduled {
		t.Fatalf("postgres completed cycle: scheduled=%v err=%v", scheduled, err)
	}
}

func newSQLiteRepository(t *testing.T) (*Repository, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "control.db")
	cfg := storage.Config{Env: "test", Driver: storage.DriverSQLite, SQLitePath: path}
	if err := (storagesqlite.Provider{}).MigrateControl(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(8)
	t.Cleanup(func() { _ = db.Close() })
	repository, err := New(db, DialectSQLite, WithLeaseDuration(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	return repository, db
}

func seedWorkspace(t *testing.T, db *sql.DB, workspaceID string) {
	t.Helper()
	userID := "user-" + workspaceID
	if _, err := db.Exec(`INSERT INTO users(id,email,password_hash) VALUES(?,?,?)`, userID, userID+"@example.test", "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO workspaces(id,name,owner_user_id) VALUES(?,?,?)`, workspaceID, workspaceID, userID); err != nil {
		t.Fatal(err)
	}
}

func mustEnqueue(t *testing.T, repository *Repository, request EnqueueRequest) {
	t.Helper()
	if err := repository.Enqueue(context.Background(), request); err != nil {
		t.Fatal(err)
	}
}

func mustClaimOne(t *testing.T, repository *Repository, now time.Time) taskdomain.GenerationWorkspaceClaim {
	t.Helper()
	claims, err := repository.ClaimGenerationWorkspaces(context.Background(), 1, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 1 {
		t.Fatalf("claims=%#v", claims)
	}
	return claims[0]
}

func newPostgresControlDB(t *testing.T) *sql.DB {
	t.Helper()
	baseURL, ready, err := testsupport.IntegrationTarget("PostgreSQL", "FLOWSPACE_TEST_DATABASE_URL", "FLOWSPACE_REQUIRE_POSTGRES_TESTS")
	if err != nil {
		t.Fatal(err)
	}
	if !ready {
		t.Skip("FLOWSPACE_TEST_DATABASE_URL is required for postgres integration tests")
	}
	schema := fmt.Sprintf("fs_test_generation_claims_%d", time.Now().UnixNano())
	admin, err := sql.Open("pgx", baseURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(`CREATE SCHEMA "` + schema + `"`); err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(`DROP SCHEMA IF EXISTS "` + schema + `" CASCADE`)
		_ = admin.Close()
	})
	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("options", "-c search_path="+schema+",public")
	parsed.RawQuery = query.Encode()
	db, err := sql.Open("pgx", parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`
		CREATE TABLE users (id TEXT PRIMARY KEY, email TEXT NOT NULL, password_hash TEXT NOT NULL);
		CREATE TABLE workspaces (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			owner_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT
		);`); err != nil {
		t.Fatal(err)
	}
	migrationPath := filepath.Join("..", "..", "db", "migrations", "control", "postgres", "0004_task_domain_generation_jobs.sql")
	migration, err := os.ReadFile(migrationPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(migration)); err != nil {
		t.Fatalf("apply generation claim migration: %v", err)
	}
	return db
}
