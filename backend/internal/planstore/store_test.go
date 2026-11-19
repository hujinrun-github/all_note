package planstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hujinrun/flowspace/internal/objectstore"
	"github.com/hujinrun/flowspace/internal/storage"
	storagepostgres "github.com/hujinrun/flowspace/internal/storage/postgres"
	storagesqlite "github.com/hujinrun/flowspace/internal/storage/sqlite"
	"github.com/hujinrun/flowspace/internal/taskapp"
	"github.com/hujinrun/flowspace/internal/taskdomain"
	"github.com/hujinrun/flowspace/internal/taskruntime"
	"github.com/hujinrun/flowspace/internal/testsupport"
	"github.com/hujinrun/flowspace/internal/transcription"
	"github.com/hujinrun/flowspace/internal/webplanning"
)

var testNow = time.Date(2026, 11, 19, 4, 0, 0, 0, time.UTC)
var owner = webplanning.Scope{WorkspaceID: "w1", ActorID: "owner"}

type testResolver struct {
	db     *sql.DB
	store  storage.Store
	writer storage.TenantFencedWriter
	driver storage.Driver
	epoch  int64
}

func (r *testResolver) ResolveMobileRuntime(_ context.Context, workspace string) (taskruntime.MobileRuntimeSnapshot, error) {
	return taskruntime.MobileRuntimeSnapshot{WorkspaceID: workspace, Epoch: r.epoch, DB: r.db, Store: r.store, Writer: r.writer, Driver: r.driver, Application: taskapp.RuntimeSnapshot{WorkspaceID: workspace, Epoch: r.epoch, Reader: r.store.(storage.TaskDomainRuntimeStore).TaskDomainReader(workspace)}}, nil
}

func fixture(t *testing.T, driver storage.Driver) (*Repository, *sql.DB, *testResolver) {
	t.Helper()
	ctx := context.Background()
	cfg := storage.Config{Env: "test", Driver: driver}
	var provider interface {
		MigrateTenant(context.Context, storage.Config) error
		OpenTenant(context.Context, storage.Config, string) (storage.Store, error)
	}
	if driver == storage.DriverSQLite {
		cfg.SQLitePath = filepath.Join(t.TempDir(), "planning.test.db")
		provider = storagesqlite.Provider{}
	} else {
		base, ready, err := testsupport.IntegrationTarget("PostgreSQL", "FLOWSPACE_TEST_DATABASE_URL", "FLOWSPACE_REQUIRE_POSTGRES_TESTS")
		if err != nil {
			t.Fatal(err)
		}
		if !ready {
			t.Skip("FLOWSPACE_TEST_DATABASE_URL is required for postgres planning integration tests")
		}
		admin, err := sql.Open("pgx", base)
		if err != nil {
			t.Fatal(err)
		}
		schema := "fs_test_plan_" + fmt.Sprint(time.Now().UnixNano())
		if _, err = admin.Exec(`CREATE SCHEMA "` + schema + `"`); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = admin.Exec(`DROP SCHEMA "` + schema + `" CASCADE`); _ = admin.Close() })
		u, err := url.Parse(base)
		if err != nil {
			t.Fatal(err)
		}
		q := u.Query()
		q.Set("options", "-c search_path="+schema+",public")
		u.RawQuery = q.Encode()
		cfg.URL = u.String()
		provider = storagepostgres.Provider{}
	}
	if err := provider.MigrateTenant(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	store, err := provider.OpenTenant(ctx, cfg, taskruntime.ExpectedTenantSchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	db := store.(storage.SQLStore).SQLDB()
	if _, err := db.Exec(`INSERT INTO tenant_workspaces(workspace_id) VALUES('w1'),('w2')`); err != nil {
		t.Fatal(err)
	}
	var writer storage.TenantFencedWriter
	if driver == storage.DriverSQLite {
		writer = storagesqlite.NewTenantWriter(cfg)
	} else {
		writer = storagepostgres.NewTenantWriter(cfg)
	}
	for _, workspace := range []string{"w1", "w2"} {
		if err := writer.BeginFencedWrite(ctx, workspace, 1, func(tx storage.TenantWriteTx) error { return tx.ProjectWriter().EnsureSystemProjects(ctx) }); err != nil {
			t.Fatal(err)
		}
	}
	resolver := &testResolver{db: db, store: store, writer: writer, driver: driver, epoch: 1}
	repo, err := New(resolver)
	if err != nil {
		t.Fatal(err)
	}
	repo.now = func() time.Time { return testNow }
	return repo, db, resolver
}

func draftRecordFor(id string) webplanning.Draft {
	return webplanning.Draft{ID: id, WorkspaceID: owner.WorkspaceID, OwnerID: owner.ActorID, RequestID: uuid.NewString(), RequestHash: "input-hash", Revision: 1, Status: "ready", InputText: "明天安排两件事", Timezone: "Asia/Shanghai", ReferenceTime: testNow, Title: "明日计划", Items: []webplanning.Item{
		{ID: "first", Title: "第一件事", Selected: true, Schedule: webplanning.Schedule{RecurrenceType: taskdomain.RecurrenceNone, TimingType: taskdomain.TimingUnscheduled, Timezone: "Asia/Shanghai"}},
		{ID: "second", Title: "第二件事", Selected: true, Schedule: webplanning.Schedule{RecurrenceType: taskdomain.RecurrenceNone, TimingType: taskdomain.TimingUnscheduled, Timezone: "Asia/Shanghai"}},
	}, CreatedAt: testNow, UpdatedAt: testNow}
}
func saveDraft(t *testing.T, repo *Repository, d webplanning.Draft) {
	t.Helper()
	if err := repo.Write(context.Background(), webplanning.Scope{WorkspaceID: d.WorkspaceID, ActorID: d.OwnerID}, func(tx webplanning.Tx) error { return tx.PutDraft(context.Background(), d, 0) }); err != nil {
		t.Fatal(err)
	}
}
func loadDraft(t *testing.T, repo *Repository, id string) webplanning.Draft {
	t.Helper()
	var d webplanning.Draft
	err := repo.Read(context.Background(), owner, func(r webplanning.Reader) error {
		var err error
		d, err = r.GetDraft(context.Background(), id)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func count(t *testing.T, db *sql.DB, query string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSQLitePlanningStore(t *testing.T)   { runContract(t, storage.DriverSQLite) }
func TestPostgresPlanningStore(t *testing.T) { runContract(t, storage.DriverPostgres) }

func runContract(t *testing.T, driver storage.Driver) {
	t.Run("speech_text_edit_generation_commit_and_cleanup_with_SQL_restart", func(t *testing.T) {
		repo, db, resolver := fixture(t, driver)
		clock := testNow
		repo.now = func() time.Time { return clock }
		objects := objectstore.NewMemoryStore()
		asr := &testASR{}
		chat := &testChat{}
		newService := func(repository *Repository) *webplanning.Service {
			s, err := webplanning.New(webplanning.Config{Repository: repository, Objects: objects, Transcriber: asr, Chat: chat, Now: func() time.Time { return clock }})
			if err != nil {
				t.Fatal(err)
			}
			return s
		}
		service := newService(repo)
		d, err := service.Create(context.Background(), owner, webplanning.CreateRequest{RequestID: uuid.NewString(), Timezone: "Asia/Shanghai"})
		if err != nil {
			t.Fatal(err)
		}
		d, err = service.Upload(context.Background(), owner, d.ID, d.Revision, "audio/webm;codecs=opus", strings.NewReader("synthetic-webm-audio"))
		if err != nil {
			t.Fatal(err)
		}
		if !d.AudioAvailable || d.AudioPending || d.AudioMIME != "audio/webm" {
			t.Fatalf("upload metadata: %#v", d)
		}
		audioKey := d.AudioObjectKey
		start := webplanning.StartRequest{ExpectedRevision: d.Revision, RequestID: uuid.NewString()}
		job, err := service.Start(context.Background(), owner, d.ID, "transcribe", start)
		if err != nil {
			t.Fatal(err)
		}
		// New service/repository instances discover the queued job from SQL.
		restarted, _ := New(resolver)
		restarted.now = repo.now
		service = newService(restarted)
		if claimed, err := service.RunWorkspace(context.Background(), "w1"); err != nil || !claimed {
			t.Fatalf("transcribe: claimed=%v err=%v", claimed, err)
		}
		d, err = service.Get(context.Background(), owner, d.ID)
		if err != nil {
			t.Fatal(err)
		}
		if d.InputText != "明天复习单词半小时" || d.Transcript != d.InputText || asr.calls != 1 {
			t.Fatalf("transcript lost: %#v calls=%d", d, asr.calls)
		}
		if d.LatestJob == nil || d.LatestJob.ID != job.Job.ID || d.LatestJob.State != "completed" {
			t.Fatalf("job state: %#v", d.LatestJob)
		}
		edited := "明天晚上八点复习单词半小时"
		d, err = service.Patch(context.Background(), owner, d.ID, webplanning.PatchRequest{ExpectedRevision: d.Revision, InputText: &edited})
		if err != nil {
			t.Fatal(err)
		}
		_, err = service.Start(context.Background(), owner, d.ID, "generate", webplanning.StartRequest{ExpectedRevision: d.Revision, RequestID: uuid.NewString()})
		if err != nil {
			t.Fatal(err)
		}
		if claimed, err := service.RunWorkspace(context.Background(), "w1"); err != nil || !claimed {
			t.Fatalf("generate: %v %v", claimed, err)
		}
		d, err = service.Get(context.Background(), owner, d.ID)
		if err != nil {
			t.Fatal(err)
		}
		if d.Status != "ready" || len(d.Items) != 1 || !strings.Contains(chat.input, edited) {
			t.Fatalf("generated draft=%#v input=%s", d, chat.input)
		}
		validation, err := service.Validate(context.Background(), owner, d.ID, d.Revision)
		if err != nil || len(validation.Issues) > 0 {
			t.Fatalf("validation=%#v err=%v", validation, err)
		}
		req := webplanning.CommitRequest{ExpectedRevision: d.Revision, RequestID: uuid.NewString(), Publish: true}
		receipt, err := service.Commit(context.Background(), owner, d.ID, req)
		if err != nil {
			t.Fatal(err)
		}
		if len(receipt.TaskIDs) != 1 || count(t, db, `SELECT COUNT(*) FROM notes`) != 0 {
			t.Fatalf("receipt=%#v", receipt)
		}
		clock = clock.Add(6 * time.Minute)
		if _, err := service.RunWorkspace(context.Background(), "w1"); err != nil {
			t.Fatal(err)
		}
		if _, err := objects.Get(context.Background(), audioKey); !errors.Is(err, objectstore.ErrNotFound) {
			t.Fatalf("audio not cleaned: %v", err)
		}
		if loadDraft(t, restarted, d.ID).AudioAvailable {
			t.Fatal("cleaned audio still advertised")
		}
		if _, err := service.Commit(context.Background(), owner, d.ID, req); err != nil {
			t.Fatalf("commit replay after cleanup: %v", err)
		}
	})
	t.Run("owner_workspace_CAS_and_private_metadata", func(t *testing.T) {
		repo, db, resolver := fixture(t, driver)
		d := draftRecordFor("private-draft")
		expiry := testNow.Add(24 * time.Hour)
		d.AudioObjectKey = "private/audio.webm"
		d.AudioMIME = "audio/webm"
		d.AudioDigest = "audio-hash"
		d.AudioSize = 512
		d.AudioPending = true
		d.AudioExpiresAt = &expiry
		d.CleanupObjectKeys = []string{"old/audio"}
		saveDraft(t, repo, d)
		for _, scope := range []webplanning.Scope{{WorkspaceID: "w1", ActorID: "other"}, {WorkspaceID: "w2", ActorID: "owner"}} {
			err := repo.Read(context.Background(), scope, func(r webplanning.Reader) error { _, e := r.GetDraft(context.Background(), d.ID); return e })
			if !errors.Is(err, webplanning.ErrNotFound) {
				t.Fatalf("scope leak: %v", err)
			}
		}
		// Reconstruct the repository as after a process restart.
		restarted, _ := New(resolver)
		restarted.now = repo.now
		got := loadDraft(t, restarted, d.ID)
		if !reflect.DeepEqual(got, d) {
			t.Fatalf("private metadata lost: %#v", got)
		}
		next := got
		next.Title = "修改"
		next.Revision++
		if err := repo.Write(context.Background(), owner, func(tx webplanning.Tx) error { return tx.PutDraft(context.Background(), next, 1) }); err != nil {
			t.Fatal(err)
		}
		if err := repo.Write(context.Background(), owner, func(tx webplanning.Tx) error { return tx.PutDraft(context.Background(), next, 1) }); !errors.Is(err, webplanning.ErrConflict) {
			t.Fatalf("stale write: %v", err)
		}
		if n := count(t, db, `SELECT COUNT(*) FROM notes`); n != 0 {
			t.Fatalf("draft created %d notes", n)
		}
		resolver.epoch = 2
		next.Revision++
		if err := repo.Write(context.Background(), owner, func(tx webplanning.Tx) error { return tx.PutDraft(context.Background(), next, 2) }); !errors.Is(err, storage.ErrTenantEpochMismatch) {
			t.Fatalf("epoch write: %v", err)
		}
	})
	t.Run("expired_job_is_discoverable_after_restart_and_claim_CAS", func(t *testing.T) {
		repo, _, resolver := fixture(t, driver)
		d := draftRecordFor("job-draft")
		saveDraft(t, repo, d)
		job := webplanning.Job{ID: "job", DraftID: d.ID, WorkspaceID: "w1", OwnerID: "owner", RequestID: uuid.NewString(), RequestHash: "job-hash", Revision: 1, InputRevision: 1, Stage: "generate", State: "processing", LeaseToken: "old-worker", LeaseUntil: testNow.Add(-time.Second), CreatedAt: testNow, UpdatedAt: testNow}
		if err := repo.Write(context.Background(), owner, func(tx webplanning.Tx) error { return tx.PutJob(context.Background(), job, 0) }); err != nil {
			t.Fatal(err)
		}
		restarted, _ := New(resolver)
		var jobs []webplanning.Job
		if err := restarted.Read(context.Background(), webplanning.Scope{WorkspaceID: "w1"}, func(r webplanning.Reader) error {
			var e error
			jobs, e = r.ListEligibleJobs(context.Background(), testNow, 10)
			return e
		}); err != nil {
			t.Fatal(err)
		}
		if len(jobs) != 1 || jobs[0].OwnerID != "owner" || jobs[0].LeaseToken != "old-worker" {
			t.Fatalf("lost durable lease: %#v", jobs)
		}
		claimed := jobs[0]
		claimed.Revision++
		claimed.LeaseToken = "new-worker"
		claimed.LeaseUntil = testNow.Add(time.Minute)
		if err := repo.Write(context.Background(), owner, func(tx webplanning.Tx) error { return tx.PutJob(context.Background(), claimed, 1) }); err != nil {
			t.Fatal(err)
		}
		job.Revision++
		job.State = "completed"
		if err := repo.Write(context.Background(), owner, func(tx webplanning.Tx) error { return tx.PutJob(context.Background(), job, 1) }); !errors.Is(err, webplanning.ErrConflict) {
			t.Fatalf("late worker accepted: %v", err)
		}
	})
	t.Run("atomic_published_commit_replay_and_changes", func(t *testing.T) {
		repo, db, _ := fixture(t, driver)
		d := draftRecordFor("commit-draft")
		saveDraft(t, repo, d)
		before := count(t, db, `SELECT latest_sequence FROM mobile_v2_commit_heads WHERE workspace_id='w1'`)
		req := webplanning.CommitRequest{DraftID: d.ID, ExpectedRevision: 1, RequestID: uuid.NewString(), Publish: true}
		receipt, err := repo.Commit(context.Background(), owner, req)
		if err != nil {
			t.Fatal(err)
		}
		if len(receipt.TaskIDs) != 2 || len(receipt.OccurrenceIDs) != 2 {
			t.Fatalf("receipt: %#v", receipt)
		}
		if n := count(t, db, `SELECT COUNT(*) FROM domain_tasks_v2 WHERE lifecycle_status='active'`); n != 2 {
			t.Fatalf("published=%d", n)
		}
		if n := count(t, db, `SELECT latest_sequence FROM mobile_v2_commit_heads WHERE workspace_id='w1'`); n != before+1 {
			t.Fatalf("batch sequence=%d before=%d", n, before)
		}
		if loadDraft(t, repo, d.ID).Status != "committed" {
			t.Fatal("draft not committed")
		}
		for i := 0; i < 2; i++ {
			if i == 1 {
				req.RequestID = uuid.NewString()
			}
			again, err := repo.Commit(context.Background(), owner, req)
			if err != nil || !reflect.DeepEqual(again, receipt) {
				t.Fatalf("replay: %#v %v", again, err)
			}
		}
		req.Publish = false
		if _, err := repo.Commit(context.Background(), owner, req); !errors.Is(err, webplanning.ErrConflict) {
			t.Fatalf("changed replay accepted: %v", err)
		}
		if n := count(t, db, `SELECT COUNT(*) FROM domain_tasks_v2`); n != 2 {
			t.Fatalf("duplicate tasks=%d", n)
		}
	})
	t.Run("failure_after_first_task_rolls_back_all_entities_receipt_and_changes", func(t *testing.T) {
		repo, db, _ := fixture(t, driver)
		d := draftRecordFor("rollback-draft")
		saveDraft(t, repo, d)
		if driver == storage.DriverSQLite {
			if _, err := db.Exec(`CREATE TRIGGER reject_second BEFORE INSERT ON domain_tasks_v2 WHEN NEW.title='第二件事' BEGIN SELECT RAISE(ABORT,'injected failure'); END`); err != nil {
				t.Fatal(err)
			}
		} else {
			if _, err := db.Exec(`ALTER TABLE domain_tasks_v2 ADD CONSTRAINT reject_second CHECK(title<>'第二件事')`); err != nil {
				t.Fatal(err)
			}
		}
		before := count(t, db, `SELECT latest_sequence FROM mobile_v2_commit_heads WHERE workspace_id='w1'`)
		_, err := repo.Commit(context.Background(), owner, webplanning.CommitRequest{DraftID: d.ID, ExpectedRevision: 1, RequestID: uuid.NewString(), Publish: true, NewProject: &webplanning.NewProjectInput{Name: "原子新项目", Kind: taskdomain.ProjectKindStandard, Horizon: taskdomain.ProjectHorizonShort}})
		if err == nil {
			t.Fatal("injected failure not returned")
		}
		if !strings.Contains(err.Error(), "injected failure") && !strings.Contains(err.Error(), "reject_second") {
			t.Fatalf("failed before injected second task: %v", err)
		}
		for _, query := range []string{`SELECT COUNT(*) FROM domain_tasks_v2`, `SELECT COUNT(*) FROM plan_commits`, `SELECT COUNT(*) FROM domain_projects_v2 WHERE name='原子新项目'`} {
			if n := count(t, db, query); n != 0 {
				t.Fatalf("partial effect: %s =%d", query, n)
			}
		}
		if n := count(t, db, `SELECT latest_sequence FROM mobile_v2_commit_heads WHERE workspace_id='w1'`); n != before {
			t.Fatalf("rolled back change escaped: %d", n)
		}
		if got := loadDraft(t, repo, d.ID); got.Revision != 1 || got.Status != "ready" {
			t.Fatalf("draft changed: %#v", got)
		}
	})
	t.Run("concurrent_different_keys_commit_once", func(t *testing.T) {
		repo, db, _ := fixture(t, driver)
		d := draftRecordFor("concurrent-draft")
		saveDraft(t, repo, d)
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		for range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := repo.Commit(context.Background(), owner, webplanning.CommitRequest{DraftID: d.ID, ExpectedRevision: 1, RequestID: uuid.NewString(), Publish: true})
				errs <- err
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		if n := count(t, db, `SELECT COUNT(*) FROM domain_tasks_v2`); n != 2 {
			t.Fatalf("duplicate effects: %d", n)
		}
		if n := count(t, db, `SELECT COUNT(*) FROM plan_commits`); n != 1 {
			t.Fatalf("duplicate commits: %d", n)
		}
	})
	t.Run("new_project_draft_and_conflict_recheck", func(t *testing.T) {
		repo, db, _ := fixture(t, driver)
		d := draftRecordFor("new-project")
		d.Items = d.Items[:1]
		d.Items[0].Schedule = webplanning.Schedule{RecurrenceType: taskdomain.RecurrenceNone, TimingType: taskdomain.TimingTimeBlock, Timezone: "Asia/Shanghai", StartsOn: "2026-11-20", LocalStartTime: "20:00", DurationMinutes: 30}
		saveDraft(t, repo, d)
		request := webplanning.CommitRequest{DraftID: d.ID, ExpectedRevision: 1, RequestID: uuid.NewString(), Publish: true, NewProject: &webplanning.NewProjectInput{Name: "显式学习项目", Kind: taskdomain.ProjectKindLearning, Horizon: taskdomain.ProjectHorizonLong}}
		receipt, err := repo.Commit(context.Background(), owner, request)
		if err != nil {
			t.Fatal(err)
		}
		if receipt.ProjectID == taskdomain.SystemInboxProjectID {
			t.Fatal("new project ignored")
		}
		if n := count(t, db, `SELECT COUNT(*) FROM domain_projects_v2 WHERE name='显式学习项目' AND status='active'`); n != 1 {
			t.Fatal("project not activated")
		}
		second := draftRecordFor("overlap")
		second.Items = d.Items
		saveDraft(t, repo, second)
		request = webplanning.CommitRequest{DraftID: second.ID, ExpectedRevision: 1, RequestID: uuid.NewString(), Publish: true}
		if _, err := repo.Commit(context.Background(), owner, request); !errors.Is(err, webplanning.ErrConflict) {
			t.Fatalf("conflict missed: %v", err)
		}
		request.AllowConflicts = true
		if _, err := repo.Commit(context.Background(), owner, request); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("draft_only_due_at_and_unselected_items", func(t *testing.T) {
		repo, db, _ := fixture(t, driver)
		d := draftRecordFor("only-draft")
		d.Items[1].Selected = false
		due := testNow.Add(48 * time.Hour)
		d.Items[0].DueAt = &due
		saveDraft(t, repo, d)
		receipt, err := repo.Commit(context.Background(), owner, webplanning.CommitRequest{DraftID: d.ID, ExpectedRevision: 1, RequestID: uuid.NewString(), NewProject: &webplanning.NewProjectInput{Name: "待启用项目"}})
		if err != nil {
			t.Fatal(err)
		}
		if len(receipt.TaskIDs) != 1 || len(receipt.OccurrenceIDs) != 1 {
			t.Fatalf("selection ignored: %#v", receipt)
		}
		if n := count(t, db, `SELECT COUNT(*) FROM domain_tasks_v2 WHERE lifecycle_status='draft'`); n != 1 {
			t.Fatalf("not draft: %d", n)
		}
		if n := count(t, db, `SELECT COUNT(*) FROM domain_projects_v2 WHERE name='待启用项目' AND status='planning'`); n != 1 {
			t.Fatal("project activated without publish")
		}
		var aggregate taskdomain.TaskAggregateQueryResult
		if err := repo.Read(context.Background(), owner, func(r webplanning.Reader) error {
			reader := r.(*reader)
			var e error
			aggregate, e = reader.runtime.Application.Reader.GetTaskAggregate(context.Background(), receipt.TaskIDs[0])
			return e
		}); err != nil {
			t.Fatal(err)
		}
		if len(aggregate.Occurrences) != 1 || aggregate.Occurrences[0].DueAt == nil || !aggregate.Occurrences[0].DueAt.Equal(due) {
			t.Fatalf("deadline lost: %#v", aggregate.Occurrences)
		}
		if aggregate.Occurrences[0].PlannedStartAt != nil {
			t.Fatal("deadline invented execution time")
		}
	})
	t.Run("recurrence_checks_second_week_in_preview_window", func(t *testing.T) {
		repo, db, _ := fixture(t, driver)
		first := draftRecordFor("second-sunday-meeting")
		first.Items = first.Items[:1]
		first.Items[0].Schedule = webplanning.Schedule{RecurrenceType: taskdomain.RecurrenceNone, TimingType: taskdomain.TimingTimeBlock, Timezone: "Asia/Shanghai", StartsOn: "2026-11-29", LocalStartTime: "20:00", DurationMinutes: 30}
		saveDraft(t, repo, first)
		if _, err := repo.Commit(context.Background(), owner, webplanning.CommitRequest{DraftID: first.ID, ExpectedRevision: 1, RequestID: uuid.NewString(), Publish: true}); err != nil {
			t.Fatal(err)
		}
		second := draftRecordFor("recurring-sundays")
		second.Items = second.Items[:1]
		second.Items[0].Schedule = webplanning.Schedule{RecurrenceType: taskdomain.RecurrenceWeekly, TimingType: taskdomain.TimingTimeBlock, Timezone: "Asia/Shanghai", StartsOn: "2026-11-22", EndsOn: "2026-11-29", LocalStartTime: "20:15", DurationMinutes: 30, Rule: &taskdomain.RecurrenceRule{Interval: 1, Weekdays: []int{0}}}
		saveDraft(t, repo, second)
		var validation webplanning.Validation
		if err := repo.Read(context.Background(), owner, func(r webplanning.Reader) error {
			var e error
			validation, e = r.ValidatePlan(context.Background(), second)
			return e
		}); err != nil {
			t.Fatal(err)
		}
		if len(validation.Conflicts) != 1 || !strings.Contains(validation.Conflicts[0], "11-29") {
			t.Fatalf("second week missed: %#v", validation)
		}
		if _, err := repo.Commit(context.Background(), owner, webplanning.CommitRequest{DraftID: second.ID, ExpectedRevision: 1, RequestID: uuid.NewString(), Publish: true, AllowConflicts: true}); err != nil {
			t.Fatal(err)
		}
		if n := count(t, db, `SELECT COUNT(*) FROM domain_tasks_v2`); n != 2 {
			t.Fatalf("expanded recurrence into tasks: %d", n)
		}
		if n := count(t, db, `SELECT COUNT(*) FROM domain_task_occurrences_v2`); n != 3 {
			t.Fatalf("recurrence instances=%d", n)
		}
	})
}

type testASR struct{ calls int }

func (a *testASR) Transcribe(_ context.Context, input transcription.Input) (string, error) {
	a.calls++
	data, err := io.ReadAll(input.Audio)
	if err != nil {
		return "", err
	}
	if string(data) != "synthetic-webm-audio" || input.Filename != "recording.webm" || input.ContentType != "audio/webm" {
		return "", fmt.Errorf("unexpected audio metadata")
	}
	return "明天复习单词半小时", nil
}

type testChat struct{ input string }

func (c *testChat) Generate(_ context.Context, _ string, _ string, input string) (string, error) {
	c.input = input
	output := struct {
		SchemaVersion  int                `json:"schema_version"`
		Title          string             `json:"title"`
		Items          []webplanning.Item `json:"items"`
		Clarifications []string           `json:"clarifications"`
		Assumptions    []string           `json:"assumptions"`
	}{1, "明日学习", []webplanning.Item{{ID: "asr-task", Selected: true, Title: "复习单词", Schedule: webplanning.Schedule{RecurrenceType: taskdomain.RecurrenceNone, TimingType: taskdomain.TimingTimeBlock, Timezone: "Asia/Shanghai", StartsOn: "2026-11-20", LocalStartTime: "20:00", DurationMinutes: 30}}}, []string{}, []string{}}
	data, err := json.Marshal(output)
	return string(data), err
}
