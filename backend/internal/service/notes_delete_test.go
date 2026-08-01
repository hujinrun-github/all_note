package service

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/repository"
	"github.com/hujinrun/flowspace/internal/storage"
)

func TestDeleteNoteWritesTombstoneBeforeDeletingBoundNote(t *testing.T) {
	store := openServiceSyncStoreTestDB(t)
	target := saveServiceStoreNotionTarget(t, `{"data_source_id":"ds-delete"}`)
	note := createServiceStoreNote(t, "Delete Tombstone", "Body\n", "[]")
	putServiceStoreBinding(t, store, note.ID, target.ID)
	if err := store.Sync().PutExternalClaim(t.Context(), model.SyncExternalClaim{
		ExternalKey:  "notion:page-delete",
		NoteID:       note.ID,
		TargetID:     target.ID,
		ExternalType: "notion_page",
		ExternalID:   "page-delete",
		ExternalPath: "notion:page-delete",
	}); err != nil {
		t.Fatalf("put claim: %v", err)
	}

	if err := DeleteNote(serviceSyncTestContext(t), store, note.ID); err != nil {
		t.Fatalf("delete note: %v", err)
	}

	if _, err := store.Notes().GetByID(serviceSyncTestContext(t), note.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("note lookup error = %v, want sql.ErrNoRows", err)
	}
	tombstone, err := store.Sync().FindImportTombstone(t.Context(), target.ID, "notion:page-delete", note.ID, "notion_page")
	if err != nil {
		t.Fatalf("find tombstone: %v", err)
	}
	if tombstone.Reason != "note_deleted" || tombstone.ExternalID != "page-delete" {
		t.Fatalf("tombstone = %+v", tombstone)
	}
}

func TestDeleteNoteWithoutClaimDeletesNormally(t *testing.T) {
	store := openServiceSyncStoreTestDB(t)
	note := createServiceStoreNote(t, "Plain Delete", "Body\n", "[]")

	if err := DeleteNote(serviceSyncTestContext(t), store, note.ID); err != nil {
		t.Fatalf("delete note: %v", err)
	}

	if _, err := store.Notes().GetByID(serviceSyncTestContext(t), note.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("note lookup error = %v, want sql.ErrNoRows", err)
	}
}

func TestDeleteNoteCleansAssociatedVoiceNote(t *testing.T) {
	store := openServiceSyncStoreTestDB(t)
	ctx := serviceSyncTestContext(t)
	clientID := "77777777-7777-4777-8777-777777777777"
	voice, _, err := CreateVoiceNote(ctx, store, model.CreateVoiceNoteRequest{
		ClientID:   clientID,
		Title:      "Voice-backed note",
		DurationMS: 4200,
		Language:   "zh",
	})
	if err != nil {
		t.Fatalf("create voice note: %v", err)
	}
	db := serviceSyncSQLDB(t, store)
	now := time.Now().Unix()
	if _, err := db.ExecContext(ctx, `
		UPDATE voice_notes
		SET object_key='voice/test-delete.m4a',
			mime_type='audio/mp4',
			audio_size=128,
			audio_sha256='test-sha',
			upload_state='uploaded',
			audio_state='uploaded',
			transcription_state='processing',
			transcription_error='stale error'
		WHERE workspace_id=? AND client_id=?
	`, "service-sync-test-workspace", clientID); err != nil {
		t.Fatalf("mark voice uploaded: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO transcription_jobs (
			job_id,workspace_id,voice_note_id,generation,state,revision,language,created_at,updated_at
		) VALUES ('voice-delete-job',?,?,1,'processing',1,'zh',?,?)
	`, "service-sync-test-workspace", clientID, now, now); err != nil {
		t.Fatalf("insert transcription job: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO transcription_results (
			workspace_id,job_id,voice_note_id,text,applied,created_at
		) VALUES (?,'voice-delete-job',?,'private transcript',1,?)
	`, "service-sync-test-workspace", clientID, now); err != nil {
		t.Fatalf("insert transcription result: %v", err)
	}

	if err := DeleteNote(ctx, store, voice.NoteID); err != nil {
		t.Fatalf("delete voice-backed note: %v", err)
	}

	var deletedAt sql.NullInt64
	var audioState string
	var durationMS int64
	var language string
	var transcriptionError string
	if err := db.QueryRowContext(ctx, `
		SELECT deleted_at,audio_state,duration_ms,language,transcription_error
		FROM voice_notes WHERE workspace_id=? AND client_id=?
	`, "service-sync-test-workspace", clientID).Scan(&deletedAt, &audioState, &durationMS, &language, &transcriptionError); err != nil {
		t.Fatalf("load deleted voice note: %v", err)
	}
	if !deletedAt.Valid || audioState != model.VoiceAudioDeleteRequested || durationMS != 0 || language != "" || transcriptionError != "" {
		t.Fatalf("voice note not fully redacted: deleted_at=%v audio_state=%q duration=%d language=%q error=%q",
			deletedAt, audioState, durationMS, language, transcriptionError)
	}
	var cleanupJobs int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM voice_audio_cleanup_jobs
		WHERE workspace_id=? AND voice_note_id=? AND object_key='voice/test-delete.m4a'
	`, "service-sync-test-workspace", clientID).Scan(&cleanupJobs); err != nil {
		t.Fatalf("count cleanup jobs: %v", err)
	}
	if cleanupJobs != 1 {
		t.Fatalf("cleanup jobs = %d, want 1", cleanupJobs)
	}
	var jobState, jobLanguage, errorCode string
	if err := db.QueryRowContext(ctx, `
		SELECT state,language,error_code FROM transcription_jobs
		WHERE workspace_id=? AND job_id='voice-delete-job'
	`, "service-sync-test-workspace").Scan(&jobState, &jobLanguage, &errorCode); err != nil {
		t.Fatalf("load transcription job: %v", err)
	}
	if jobState != model.TranscriptionJobCanceled || jobLanguage != "" || errorCode != "voice_audio_deleted" {
		t.Fatalf("transcription job = state:%q language:%q error:%q", jobState, jobLanguage, errorCode)
	}
	var results int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM transcription_results
		WHERE workspace_id=? AND voice_note_id=?
	`, "service-sync-test-workspace", clientID).Scan(&results); err != nil {
		t.Fatalf("count transcription results: %v", err)
	}
	if results != 0 {
		t.Fatalf("transcription results = %d, want 0", results)
	}
	var tombstones int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM mobile_v2_content_tombstones
		WHERE workspace_id=? AND entity_type='voice_note' AND client_id=?
	`, "service-sync-test-workspace", clientID).Scan(&tombstones); err != nil {
		t.Fatalf("count voice tombstones: %v", err)
	}
	if tombstones != 1 {
		t.Fatalf("voice tombstones = %d, want 1", tombstones)
	}
	var outboxOperation string
	if err := db.QueryRowContext(ctx, `
		SELECT operation FROM mobile_sync_outbox
		WHERE workspace_id=? AND entity_type='voice_note' AND entity_client_id=?
		ORDER BY sequence DESC LIMIT 1
	`, "service-sync-test-workspace", clientID).Scan(&outboxOperation); err != nil {
		t.Fatalf("load voice outbox operation: %v", err)
	}
	if outboxOperation != "voice_note.server_deleted" {
		t.Fatalf("voice outbox operation = %q, want voice_note.server_deleted", outboxOperation)
	}
}

func TestDeleteNoteRollsBackWhenTombstoneWriteFails(t *testing.T) {
	store := openServiceSyncStoreTestDB(t)
	target := saveServiceStoreNotionTarget(t, `{"data_source_id":"ds-delete"}`)
	note := createServiceStoreNote(t, "Rollback Tombstone", "Body\n", "[]")
	putServiceStoreBinding(t, store, note.ID, target.ID)
	if err := store.Sync().PutExternalClaim(t.Context(), model.SyncExternalClaim{
		ExternalKey:  "notion:page-rollback",
		NoteID:       note.ID,
		TargetID:     target.ID,
		ExternalType: "notion_page",
		ExternalID:   "page-rollback",
		ExternalPath: "notion:page-rollback",
	}); err != nil {
		t.Fatalf("put claim: %v", err)
	}
	remainingFailures := 1
	repository.SetStore(&putTombstoneFailOnceStore{
		Store:     store,
		err:       errors.New("tombstone database unavailable"),
		remaining: &remainingFailures,
	})

	err := DeleteNote(serviceSyncTestContext(t), repository.CurrentStore(), note.ID)

	if err == nil {
		t.Fatal("expected tombstone write failure")
	}
	if _, err := store.Notes().GetByID(serviceSyncTestContext(t), note.ID); err != nil {
		t.Fatalf("note should remain after rollback: %v", err)
	}
	claim, err := store.Sync().GetExternalClaimByNote(t.Context(), note.ID)
	if err != nil {
		t.Fatalf("claim should remain after rollback: %v", err)
	}
	if claim.ExternalID != "page-rollback" {
		t.Fatalf("claim = %+v", claim)
	}
}

func serviceSyncSQLDB(t *testing.T, store storage.Store) *sql.DB {
	t.Helper()
	scoped, ok := store.(scopedRepositoryStore)
	if !ok {
		t.Fatalf("store %T is not scopedRepositoryStore", store)
	}
	sqlStore, ok := scoped.Store.(interface {
		SQLDB() *sql.DB
	})
	if !ok {
		t.Fatalf("store %T does not expose SQLDB", scoped.Store)
	}
	return sqlStore.SQLDB()
}

type putTombstoneFailOnceStore struct {
	storage.Store
	err       error
	remaining *int
}

func (store *putTombstoneFailOnceStore) Transact(ctx context.Context, fn func(storage.Store) error) error {
	return store.Store.Transact(ctx, func(txStore storage.Store) error {
		return fn(&putTombstoneFailOnceStore{
			Store:     txStore,
			err:       store.err,
			remaining: store.remaining,
		})
	})
}

func (store *putTombstoneFailOnceStore) Sync() storage.SyncRepository {
	return &putTombstoneFailOnceSyncRepository{
		SyncRepository: store.Store.Sync(),
		err:            store.err,
		remaining:      store.remaining,
	}
}

type putTombstoneFailOnceSyncRepository struct {
	storage.SyncRepository
	err       error
	remaining *int
}

func (repo *putTombstoneFailOnceSyncRepository) PutImportTombstone(ctx context.Context, tombstone model.SyncImportTombstone) error {
	if repo.remaining != nil && *repo.remaining > 0 {
		*repo.remaining--
		return repo.err
	}
	return repo.SyncRepository.PutImportTombstone(ctx, tombstone)
}
