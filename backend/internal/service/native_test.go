package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/objectstore"
	"github.com/hujinrun/flowspace/internal/provisioning"
	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/storage/sqlite"
	"github.com/hujinrun/flowspace/internal/transcription"
)

const nativeTestSessionSecret = "native-test-session-secret-with-at-least-32-bytes"

func TestCreateVoiceNoteIsIdempotentByClientID(t *testing.T) {
	store, ctx := openNativeServiceStore(t)
	clientID := uuid.NewString()
	req := model.CreateVoiceNoteRequest{ClientID: clientID, Title: "散步灵感", DurationMS: 4200, Language: "zh"}

	first, created, err := CreateVoiceNote(ctx, store, req)
	if err != nil {
		t.Fatalf("create first voice note: %v", err)
	}
	if !created {
		t.Fatal("first create should report created=true")
	}
	second, created, err := CreateVoiceNote(ctx, store, req)
	if err != nil {
		t.Fatalf("repeat voice note create: %v", err)
	}
	if created {
		t.Fatal("repeat create should report created=false")
	}
	if first.ID != second.ID || first.NoteID != second.NoteID {
		t.Fatalf("repeat create returned a different record: first=%+v second=%+v", first, second)
	}

	_, total, err := store.Notes().List(ctx, storage.NoteFilter{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("list notes: %v", err)
	}
	if total != 1 {
		t.Fatalf("note count = %d, want 1", total)
	}
}

func TestUploadVoiceAudioIsIdempotentAndRejectsDifferentContent(t *testing.T) {
	store, ctx := openNativeServiceStore(t)
	clientID := uuid.NewString()
	if _, _, err := CreateVoiceNote(ctx, store, model.CreateVoiceNoteRequest{ClientID: clientID}); err != nil {
		t.Fatalf("create voice note: %v", err)
	}
	objects := objectstore.NewMemoryStore()
	audio := []byte("m4a-audio-one")
	digest := sha256.Sum256(audio)
	checksum := hex.EncodeToString(digest[:])

	first, err := UploadVoiceAudio(ctx, store, objects, clientID, "audio/mp4", checksum, bytes.NewReader(audio), int64(len(audio)), 1024)
	if err != nil {
		t.Fatalf("upload audio: %v", err)
	}
	if first.UploadState != model.VoiceUploadUploaded || first.AudioSHA256 != checksum {
		t.Fatalf("uploaded voice state = %+v", first)
	}
	second, err := UploadVoiceAudio(ctx, store, objects, clientID, "audio/mp4", checksum, bytes.NewReader(audio), int64(len(audio)), 1024)
	if err != nil {
		t.Fatalf("repeat same upload: %v", err)
	}
	if second.AudioSHA256 != checksum {
		t.Fatalf("repeat upload checksum = %q, want %q", second.AudioSHA256, checksum)
	}

	different := []byte("m4a-audio-two")
	if _, err := UploadVoiceAudio(ctx, store, objects, clientID, "audio/mp4", "", bytes.NewReader(different), int64(len(different)), 1024); !errors.Is(err, storage.ErrUploadConflict) {
		t.Fatalf("different upload error = %v, want ErrUploadConflict", err)
	}
	_, object, err := GetVoiceAudio(ctx, store, objects, clientID)
	if err != nil {
		t.Fatalf("get voice audio: %v", err)
	}
	defer object.Body.Close()
	stored, err := io.ReadAll(object.Body)
	if err != nil {
		t.Fatalf("read stored voice audio: %v", err)
	}
	if !bytes.Equal(stored, audio) {
		t.Fatalf("stored audio = %q, want %q", stored, audio)
	}
}

func TestTranscribeVoiceNoteUpdatesOrdinaryNote(t *testing.T) {
	store, ctx := openNativeServiceStore(t)
	clientID := uuid.NewString()
	voice, _, err := CreateVoiceNote(ctx, store, model.CreateVoiceNoteRequest{ClientID: clientID, Title: "录音"})
	if err != nil {
		t.Fatalf("create voice note: %v", err)
	}
	objects := objectstore.NewMemoryStore()
	audio := []byte("recorded audio")
	if _, err := UploadVoiceAudio(ctx, store, objects, clientID, "audio/mp4", "", bytes.NewReader(audio), int64(len(audio)), 1024); err != nil {
		t.Fatalf("upload voice audio: %v", err)
	}
	transcriber := staticTranscriber{text: "这是转写后的正文。"}

	completed, err := TranscribeVoiceNote(ctx, store, objects, transcriber, clientID, "zh")
	if err != nil {
		t.Fatalf("transcribe voice note: %v", err)
	}
	if completed.TranscriptionState != model.TranscriptionCompleted || completed.Body != transcriber.text {
		t.Fatalf("completed voice note = %+v", completed)
	}
	note, err := store.Notes().GetByID(ctx, voice.NoteID)
	if err != nil {
		t.Fatalf("get ordinary note: %v", err)
	}
	if note.Body != transcriber.text {
		t.Fatalf("ordinary note body = %q, want %q", note.Body, transcriber.text)
	}
}

func TestWatchDeviceCanBeRevoked(t *testing.T) {
	store, ctx := openNativeServiceStore(t)
	response, err := AuthorizeWatchDevice(ctx, store, nativeTestSessionSecret, model.AuthorizeWatchDeviceRequest{Name: "My Watch"})
	if err != nil {
		t.Fatalf("authorize watch: %v", err)
	}
	hash, err := auth.HashSessionToken(nativeTestSessionSecret, response.Token)
	if err != nil {
		t.Fatalf("hash watch token: %v", err)
	}
	nativeStore, err := storage.NativeStoreFrom(store)
	if err != nil {
		t.Fatalf("native store: %v", err)
	}
	if _, err := nativeStore.WatchDevices().GetActiveByTokenHash(ctx, hash); err != nil {
		t.Fatalf("get active watch: %v", err)
	}
	if err := RevokeWatchDevice(ctx, store, response.Device.ID); err != nil {
		t.Fatalf("revoke watch: %v", err)
	}
	if _, err := nativeStore.WatchDevices().GetActiveByTokenHash(ctx, hash); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("revoked token lookup error = %v, want sql.ErrNoRows", err)
	}
}

type staticTranscriber struct {
	text string
}

func (s staticTranscriber) Transcribe(_ context.Context, input transcription.Input) (string, error) {
	if _, err := io.ReadAll(input.Audio); err != nil {
		return "", err
	}
	return s.text, nil
}

func openNativeServiceStore(t *testing.T) (storage.Store, context.Context) {
	t.Helper()
	store, err := (sqlite.Provider{}).Open(t.Context(), storage.Config{
		Env:        "test",
		Driver:     storage.DriverSQLite,
		SQLitePath: filepath.Join(t.TempDir(), "native-service.test.db"),
	})
	if err != nil {
		t.Fatalf("open SQLite native service store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("close SQLite native service store: %v", err)
		}
	})

	user := &model.User{
		ID:                 "native_user",
		Email:              "native@example.com",
		DisplayName:        "Native User",
		PasswordHash:       "test-password-hash",
		PasswordSet:        true,
		MustChangePassword: false,
		Role:               "admin",
		Status:             "active",
	}
	workspace := &model.Workspace{ID: "native_workspace", Name: "Native Workspace", OwnerUserID: user.ID}
	ctx := auth.ContextWithIdentity(t.Context(), auth.RequestIdentity{
		UserID:      user.ID,
		WorkspaceID: workspace.ID,
		Role:        user.Role,
	})
	ctx = auth.ContextWithWorkspaceScope(ctx, workspace.ID)
	if err := store.Transact(ctx, func(tx storage.Store) error {
		if err := tx.Auth().CreateUser(ctx, user); err != nil {
			return err
		}
		if err := tx.Auth().CreateWorkspace(ctx, workspace); err != nil {
			return err
		}
		if err := tx.Auth().AddWorkspaceMember(ctx, workspace.ID, user.ID, "owner"); err != nil {
			return err
		}
		if err := tx.Auth().SetDefaultWorkspace(ctx, user.ID, workspace.ID); err != nil {
			return err
		}
		return provisioning.EnsureDefaultWorkspaceData(ctx, tx)
	}); err != nil {
		t.Fatalf("seed native service store: %v", err)
	}
	return store, ctx
}
