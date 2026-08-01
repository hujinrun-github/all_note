package voiceaudiocleanup

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/objectstore"
	"github.com/hujinrun/flowspace/internal/provisioning"
	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/storage/sqlite"
)

func TestWorkerRemovesObjectAndCompletesDurableCleanup(t *testing.T) {
	store, ctx, objects, clientID, objectKey, now := newCleanupWorkerFixture(t)
	worker := NewWorker(store, objects, "voice-cleanup-worker")
	worker.Now = func() time.Time { return now }
	worker.NewLeaseToken = func() string { return "voice-cleanup-lease" }

	claimed, err := worker.RunOne(context.Background())
	if err != nil || !claimed {
		t.Fatalf("first run claimed=%v err=%v", claimed, err)
	}
	if _, err := objects.Get(ctx, objectKey); !errors.Is(err, objectstore.ErrNotFound) {
		t.Fatalf("object after cleanup error=%v, want ErrNotFound", err)
	}
	nativeStore, err := storage.NativeStoreFrom(store)
	if err != nil {
		t.Fatal(err)
	}
	voice, err := nativeStore.VoiceNotes().GetByClientID(ctx, clientID)
	if err != nil || voice.AudioState != model.VoiceAudioDeleted || voice.ObjectKey != "" || voice.Revision != 5 {
		t.Fatalf("voice after worker=%+v err=%v", voice, err)
	}
	claimed, err = worker.RunOne(context.Background())
	if err != nil || claimed {
		t.Fatalf("second run claimed=%v err=%v", claimed, err)
	}
}

func TestWorkerTreatsAlreadyMissingObjectAsSuccessfulCleanup(t *testing.T) {
	store, ctx, objects, clientID, objectKey, now := newCleanupWorkerFixture(t)
	if err := objects.Remove(ctx, objectKey); err != nil {
		t.Fatal(err)
	}
	worker := NewWorker(store, objects, "voice-cleanup-worker")
	worker.Now = func() time.Time { return now }
	worker.NewLeaseToken = func() string { return "voice-cleanup-missing-lease" }
	claimed, err := worker.RunOne(context.Background())
	if err != nil || !claimed {
		t.Fatalf("missing object run claimed=%v err=%v", claimed, err)
	}
	nativeStore, _ := storage.NativeStoreFrom(store)
	voice, err := nativeStore.VoiceNotes().GetByClientID(ctx, clientID)
	if err != nil || voice.AudioState != model.VoiceAudioDeleted || voice.ObjectKey != "" {
		t.Fatalf("voice after missing-object cleanup=%+v err=%v", voice, err)
	}
}

func TestWorkerRemovesUploadedNoteAttachmentForDurableCleanupJob(t *testing.T) {
	store, ctx, objects, _, _, now := newCleanupWorkerFixture(t)
	initialWorker := NewWorker(store, objects, "initial-voice-cleanup-worker")
	initialWorker.Now = func() time.Time { return now }
	initialWorker.NewLeaseToken = func() string { return "initial-voice-cleanup-lease" }
	if claimed, err := initialWorker.RunOne(context.Background()); err != nil || !claimed {
		t.Fatalf("initial voice cleanup claimed=%v err=%v", claimed, err)
	}
	note, err := store.Notes().Create(ctx, &model.CreateNoteRequest{
		Title: "Attachment cleanup", Body: "private", Tags: "[]",
	})
	if err != nil {
		t.Fatal(err)
	}
	sqlStore, ok := store.(storage.SQLStore)
	if !ok {
		t.Fatal("sqlite store does not expose SQLStore")
	}
	const attachmentID = "cleanup-attachment-1"
	const objectKey = "note-attachments/private.mov"
	if _, err := sqlStore.SQLDB().Exec(`INSERT INTO note_attachments
		(id,workspace_id,note_id,kind,original_name,mime_type,size_bytes,sha256,object_key,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)`, attachmentID, "voice_cleanup_workspace", note.ID, "video", "private.mov",
		"video/quicktime", 5, "sha", objectKey, now.Unix()); err != nil {
		t.Fatal(err)
	}
	if err := objects.Put(ctx, objectKey, bytes.NewBufferString("video"), 5, "video/quicktime"); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlStore.SQLDB().Exec(`INSERT INTO voice_audio_cleanup_jobs
		(job_id,workspace_id,voice_note_id,object_key,state,revision,attempt,max_attempts,error_code,next_attempt_at,lease_owner,lease_token,created_at,updated_at)
		VALUES(?,?,?,?, 'queued',1,0,6,'',?,'','',?,?)`, "attachment-cleanup-job", "voice_cleanup_workspace",
		storage.NoteAttachmentCleanupSubject(attachmentID), objectKey, now.Unix(), now.Unix(), now.Unix()); err != nil {
		t.Fatal(err)
	}
	worker := NewWorker(store, objects, "attachment-cleanup-worker")
	worker.Now = func() time.Time { return now }
	worker.NewLeaseToken = func() string { return "attachment-cleanup-lease" }
	claimed, err := worker.RunOne(context.Background())
	if err != nil || !claimed {
		t.Fatalf("attachment cleanup claimed=%v err=%v", claimed, err)
	}
	if _, err := objects.Get(ctx, objectKey); !errors.Is(err, objectstore.ErrNotFound) {
		t.Fatalf("attachment object after cleanup error=%v", err)
	}
	var attachmentCount int
	if err := sqlStore.SQLDB().QueryRow(`SELECT COUNT(*) FROM note_attachments
		WHERE workspace_id=? AND id=?`, "voice_cleanup_workspace", attachmentID).Scan(&attachmentCount); err != nil {
		t.Fatal(err)
	}
	if attachmentCount != 0 {
		t.Fatalf("attachment rows after cleanup = %d", attachmentCount)
	}
}

func newCleanupWorkerFixture(t *testing.T) (storage.Store, context.Context, *objectstore.MemoryStore, string, string, time.Time) {
	t.Helper()
	store, err := (sqlite.Provider{}).Open(context.Background(), storage.Config{
		Env: "test", Driver: storage.DriverSQLite, SQLitePath: filepath.Join(t.TempDir(), "voice-cleanup-worker.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	workspaceID := "voice_cleanup_workspace"
	ctx := auth.ContextWithWorkspaceScope(context.Background(), workspaceID)
	now := time.Date(2030, time.July, 14, 18, 0, 0, 0, time.UTC)
	if err := store.Transact(ctx, func(tx storage.Store) error {
		if err := tx.Auth().CreateUser(context.Background(), &model.User{
			ID: "voice_cleanup_owner", Email: "voice-cleanup@example.com", DisplayName: "Voice cleanup",
			PasswordHash: "hash", DefaultWorkspaceID: workspaceID, Role: "admin", Status: "active",
			CreatedAt: now.Unix(), UpdatedAt: now.Unix(),
		}); err != nil {
			return err
		}
		if err := tx.Auth().CreateWorkspace(context.Background(), &model.Workspace{
			ID: workspaceID, Name: workspaceID, OwnerUserID: "voice_cleanup_owner", CreatedAt: now.Unix(), UpdatedAt: now.Unix(),
		}); err != nil {
			return err
		}
		if err := tx.Auth().AddWorkspaceMember(context.Background(), workspaceID, "voice_cleanup_owner", "owner"); err != nil {
			return err
		}
		return provisioning.EnsureDefaultWorkspaceData(ctx, tx)
	}); err != nil {
		t.Fatal(err)
	}
	repository, err := storage.MobileSyncRepositoryFrom(store)
	if err != nil {
		t.Fatal(err)
	}
	clientID := uuid.NewString()
	if _, err := repository.ApplyEntityMutation(ctx, model.MobileEntityMutation{
		MutationID: uuid.NewString(), DeviceClientID: uuid.NewString(), EntityType: "voice_note", EntityClientID: clientID,
		Operation: "voice.create", RequestSHA256: "cleanup-worker-create", Payload: []byte(`{"title":"Cleanup worker"}`),
	}); err != nil {
		t.Fatal(err)
	}
	objectKey := "voice/cleanup-worker.m4a"
	checksum := strings.Repeat("c", 64)
	nativeStore, err := storage.NativeStoreFrom(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := nativeStore.VoiceNotes().ClaimUpload(ctx, clientID, model.VoiceUploadClaim{
		ObjectKey: objectKey, MimeType: "audio/mp4", Size: 5, SHA256: checksum,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := nativeStore.VoiceNotes().MarkUploaded(ctx, clientID, checksum); err != nil {
		t.Fatal(err)
	}
	baseThree := int64(3)
	if _, err := repository.ApplyEntityMutation(ctx, model.MobileEntityMutation{
		MutationID: uuid.NewString(), DeviceClientID: uuid.NewString(), EntityType: "voice_note", EntityClientID: clientID,
		Operation: "voice_audio.delete", BaseRevision: &baseThree, RequestSHA256: "cleanup-worker-delete", Payload: []byte(`{}`),
	}); err != nil {
		t.Fatal(err)
	}
	objects := objectstore.NewMemoryStore()
	if err := objects.Put(ctx, objectKey, bytes.NewReader([]byte("audio")), 5, "audio/mp4"); err != nil {
		t.Fatal(err)
	}
	return store, ctx, objects, clientID, objectKey, now
}
