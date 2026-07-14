package transcriptionjob

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/objectstore"
	"github.com/hujinrun/flowspace/internal/provisioning"
	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/storage/sqlite"
	"github.com/hujinrun/flowspace/internal/testsupport"
)

func TestWorkerCompletesDurableJobAndAppliesTranscript(t *testing.T) {
	fixture := newWorkerFixture(t)
	transcriber := testsupport.NewScriptedTranscriber(testsupport.TranscriptionStep{Text: "Worker transcript"})
	worker := fixture.worker(transcriber)
	claimed, err := worker.RunOne(context.Background())
	if err != nil || !claimed {
		t.Fatalf("RunOne = %v, %v", claimed, err)
	}
	job, err := fixture.jobs.Get(fixture.ctx, fixture.jobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.State != model.TranscriptionJobCompleted || transcriber.CallCount() != 1 {
		t.Fatalf("job=%+v provider calls=%d", job, transcriber.CallCount())
	}
	note, err := fixture.store.Notes().GetByID(fixture.ctx, fixture.noteID)
	if err != nil {
		t.Fatal(err)
	}
	if note.Body != "Worker transcript" {
		t.Fatalf("note body = %q", note.Body)
	}
}

func TestWorkerPersistsProviderFailureWithoutReturningJobToRequestPath(t *testing.T) {
	fixture := newWorkerFixture(t)
	providerErr := errors.New("synthetic provider outage")
	transcriber := testsupport.NewScriptedTranscriber(testsupport.TranscriptionStep{Err: providerErr})
	worker := fixture.worker(transcriber)
	worker.RetryDelay = func(int64) time.Duration { return 45 * time.Second }
	claimed, err := worker.RunOne(context.Background())
	if err != nil || !claimed {
		t.Fatalf("RunOne = %v, %v", claimed, err)
	}
	job, err := fixture.jobs.Get(fixture.ctx, fixture.jobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.State != model.TranscriptionJobRetryWaiting || job.ErrorCode != "provider_failed" || job.NextAttemptAt == nil || *job.NextAttemptAt != fixture.now.Add(45*time.Second).Unix() {
		t.Fatalf("retry job = %+v", job)
	}
}

type workerFixture struct {
	store   storage.Store
	ctx     context.Context
	objects *objectstore.MemoryStore
	jobs    storage.TranscriptionJobRepository
	jobID   string
	noteID  string
	now     time.Time
}

func newWorkerFixture(t *testing.T) workerFixture {
	t.Helper()
	store, err := (sqlite.Provider{}).Open(context.Background(), storage.Config{
		Env: "test", Driver: storage.DriverSQLite, SQLitePath: filepath.Join(t.TempDir(), "worker.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	workspaceID := "worker_workspace"
	ctx := auth.ContextWithWorkspaceScope(context.Background(), workspaceID)
	now := time.Date(2026, time.July, 14, 16, 0, 0, 0, time.UTC)
	if err := store.Transact(ctx, func(tx storage.Store) error {
		if err := tx.Auth().CreateUser(context.Background(), &model.User{
			ID: "worker_owner", Email: "worker@example.com", DisplayName: "Worker", PasswordHash: "hash",
			DefaultWorkspaceID: workspaceID, Role: "admin", Status: "active", CreatedAt: now.Unix(), UpdatedAt: now.Unix(),
		}); err != nil {
			return err
		}
		if err := tx.Auth().CreateWorkspace(context.Background(), &model.Workspace{
			ID: workspaceID, Name: workspaceID, OwnerUserID: "worker_owner", CreatedAt: now.Unix(), UpdatedAt: now.Unix(),
		}); err != nil {
			return err
		}
		if err := tx.Auth().AddWorkspaceMember(context.Background(), workspaceID, "worker_owner", "owner"); err != nil {
			return err
		}
		return provisioning.EnsureDefaultWorkspaceData(ctx, tx)
	}); err != nil {
		t.Fatal(err)
	}
	note, err := store.Notes().Create(ctx, &model.CreateNoteRequest{Title: "Worker voice", FolderID: "__uncategorized", Tags: "[]"})
	if err != nil {
		t.Fatal(err)
	}
	voiceClientID := uuid.NewString()
	objectKey := "worker/fixture.m4a"
	nativeStore, err := storage.NativeStoreFrom(store)
	if err != nil {
		t.Fatal(err)
	}
	if err := nativeStore.VoiceNotes().Create(ctx, &model.VoiceNote{
		ID: uuid.NewString(), ClientID: voiceClientID, NoteID: note.ID, DurationMS: 1000, RecordedAt: now.Unix(),
		Language: "zh", ObjectKey: objectKey, MimeType: "audio/mp4", AudioSize: 5,
		AudioSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		UploadState: model.VoiceUploadUploaded, TranscriptionState: model.TranscriptionNotStarted,
		CreatedAt: now.Unix(), UpdatedAt: now.Unix(),
	}); err != nil {
		t.Fatal(err)
	}
	objects := objectstore.NewMemoryStore()
	if err := objects.Put(ctx, objectKey, bytes.NewReader([]byte("audio")), 5, "audio/mp4"); err != nil {
		t.Fatal(err)
	}
	jobs, err := storage.TranscriptionJobRepositoryFrom(store)
	if err != nil {
		t.Fatal(err)
	}
	jobID := uuid.NewString()
	if _, err := jobs.CreateOrGet(ctx, model.CreateTranscriptionJob{
		JobID: jobID, MutationID: uuid.NewString(), RequestSHA256: "worker-request",
		VoiceNoteID: voiceClientID, Language: "zh", Now: now.Unix(),
	}); err != nil {
		t.Fatal(err)
	}
	return workerFixture{store: store, ctx: ctx, objects: objects, jobs: jobs, jobID: jobID, noteID: note.ID, now: now}
}

func (f workerFixture) worker(transcriber *testsupport.ScriptedTranscriber) Worker {
	worker := NewWorker(f.store, f.objects, transcriber, "worker-1")
	worker.Now = func() time.Time { return f.now }
	worker.NewLeaseToken = func() string { return "worker-lease" }
	return worker
}
