package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/objectstore"
	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/transcription"
)

var (
	ErrInvalidVoiceClientID     = errors.New("client_id must be a UUID")
	ErrInvalidVoiceMetadata     = errors.New("invalid voice note metadata")
	ErrVoiceAudioTooLarge       = errors.New("voice audio exceeds the configured size limit")
	ErrVoiceAudioChecksum       = errors.New("voice audio checksum does not match")
	ErrVoiceAudioType           = errors.New("unsupported voice audio content type")
	ErrVoiceAudioNotUploaded    = errors.New("voice audio has not been uploaded")
	ErrVoiceStorageUnavailable  = errors.New("voice audio storage is unavailable")
	ErrTranscriptionUnavailable = errors.New("transcription service is unavailable")
)

const (
	defaultWatchTokenDays = 90
	maxWatchTokenDays     = 365
	maxVoiceDurationMS    = int64(12 * time.Hour / time.Millisecond)
	defaultVoiceMaxBytes  = int64(50 * 1024 * 1024)
)

type WatchSnapshot struct {
	Today      *TodayData        `json:"today"`
	VoiceNotes []model.VoiceNote `json:"voice_notes"`
}

func AuthorizeWatchDevice(ctx context.Context, store storage.Store, sessionSecret string, req model.AuthorizeWatchDeviceRequest) (*model.AuthorizeWatchDeviceResponse, error) {
	identity, ok := auth.IdentityFromContext(ctx)
	if !ok {
		return nil, auth.ErrMissingIdentity
	}
	days := req.ExpiresInDays
	if days == 0 {
		days = defaultWatchTokenDays
	}
	if days < 1 || days > maxWatchTokenDays {
		return nil, fmt.Errorf("%w: expires_in_days must be between 1 and %d", ErrInvalidVoiceMetadata, maxWatchTokenDays)
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "Apple Watch"
	}
	if len([]rune(name)) > 80 {
		return nil, fmt.Errorf("%w: device name is too long", ErrInvalidVoiceMetadata)
	}
	rawToken, err := auth.GenerateSessionToken()
	if err != nil {
		return nil, err
	}
	tokenHash, err := auth.HashSessionToken(sessionSecret, rawToken)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	device := &model.WatchDevice{
		ID:          uuid.NewString(),
		Name:        name,
		Status:      "active",
		ExpiresAt:   now.Add(time.Duration(days) * 24 * time.Hour).Unix(),
		LastSeenAt:  now.Unix(),
		CreatedAt:   now.Unix(),
		UpdatedAt:   now.Unix(),
		UserID:      identity.UserID,
		WorkspaceID: identity.WorkspaceID,
		TokenHash:   tokenHash,
	}
	err = store.Transact(ctx, func(tx storage.Store) error {
		nativeStore, err := storage.NativeStoreFrom(tx)
		if err != nil {
			return err
		}
		if err := nativeStore.WatchDevices().Create(ctx, device); err != nil {
			return err
		}
		workspaceID := identity.WorkspaceID
		actorID := identity.UserID
		return tx.Auth().RecordAuditEvent(ctx, &model.AuditEvent{
			ID:          uuid.NewString(),
			ActorUserID: &actorID,
			WorkspaceID: &workspaceID,
			Action:      "watch_device.authorized",
			Metadata: map[string]any{
				"device_id":  device.ID,
				"name":       device.Name,
				"expires_at": device.ExpiresAt,
			},
			CreatedAt: now.Unix(),
		})
	})
	if err != nil {
		return nil, err
	}
	return &model.AuthorizeWatchDeviceResponse{Device: *device, Token: rawToken}, nil
}

func RevokeWatchDevice(ctx context.Context, store storage.Store, deviceID string) error {
	identity, ok := auth.IdentityFromContext(ctx)
	if !ok {
		return auth.ErrMissingIdentity
	}
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return sql.ErrNoRows
	}
	return store.Transact(ctx, func(tx storage.Store) error {
		nativeStore, err := storage.NativeStoreFrom(tx)
		if err != nil {
			return err
		}
		if err := nativeStore.WatchDevices().Revoke(ctx, deviceID, identity.UserID); err != nil {
			return err
		}
		workspaceID := identity.WorkspaceID
		actorID := identity.UserID
		return tx.Auth().RecordAuditEvent(ctx, &model.AuditEvent{
			ID:          uuid.NewString(),
			ActorUserID: &actorID,
			WorkspaceID: &workspaceID,
			Action:      "watch_device.revoked",
			Metadata:    map[string]any{"device_id": deviceID},
			CreatedAt:   time.Now().Unix(),
		})
	})
}

func CreateVoiceNote(ctx context.Context, store storage.Store, req model.CreateVoiceNoteRequest) (*model.VoiceNote, bool, error) {
	clientID := strings.TrimSpace(req.ClientID)
	if _, err := uuid.Parse(clientID); err != nil {
		return nil, false, ErrInvalidVoiceClientID
	}
	if req.DurationMS < 0 || req.DurationMS > maxVoiceDurationMS {
		return nil, false, fmt.Errorf("%w: duration_ms is out of range", ErrInvalidVoiceMetadata)
	}
	if len([]rune(strings.TrimSpace(req.Language))) > 32 {
		return nil, false, fmt.Errorf("%w: language is too long", ErrInvalidVoiceMetadata)
	}
	recordedAt := req.RecordedAt
	if recordedAt <= 0 {
		recordedAt = time.Now().Unix()
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = "语音笔记 " + time.Unix(recordedAt, 0).Format("01-02 15:04")
	}
	if len([]rune(title)) > 200 {
		return nil, false, fmt.Errorf("%w: title is too long", ErrInvalidVoiceMetadata)
	}

	now := time.Now().Unix()
	note := &model.Note{
		ID:        uuid.NewString(),
		Title:     title,
		Body:      "",
		FolderID:  "__uncategorized",
		Tags:      `["voice"]`,
		CreatedAt: recordedAt,
		UpdatedAt: now,
	}
	voice := &model.VoiceNote{
		ID:                 uuid.NewString(),
		ClientID:           clientID,
		NoteID:             note.ID,
		Title:              title,
		DurationMS:         req.DurationMS,
		RecordedAt:         recordedAt,
		Language:           strings.TrimSpace(req.Language),
		UploadState:        model.VoiceUploadPending,
		TranscriptionState: model.TranscriptionNotStarted,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	err := store.Transact(ctx, func(tx storage.Store) error {
		if err := tx.Notes().CreateWithID(ctx, note); err != nil {
			return err
		}
		nativeStore, err := storage.NativeStoreFrom(tx)
		if err != nil {
			return err
		}
		return nativeStore.VoiceNotes().Create(ctx, voice)
	})
	if errors.Is(err, storage.ErrAlreadyExists) {
		nativeStore, nativeErr := storage.NativeStoreFrom(store)
		if nativeErr != nil {
			return nil, false, nativeErr
		}
		existing, getErr := nativeStore.VoiceNotes().GetByClientID(ctx, clientID)
		return existing, false, getErr
	}
	if err != nil {
		return nil, false, err
	}
	nativeStore, err := storage.NativeStoreFrom(store)
	if err != nil {
		return nil, false, err
	}
	created, err := nativeStore.VoiceNotes().GetByClientID(ctx, clientID)
	return created, true, err
}

func GetVoiceNote(ctx context.Context, store storage.Store, clientID string) (*model.VoiceNote, error) {
	nativeStore, err := storage.NativeStoreFrom(store)
	if err != nil {
		return nil, err
	}
	voice, err := nativeStore.VoiceNotes().GetByClientID(ctx, strings.TrimSpace(clientID))
	if err == nil && voice.DeletedAt != nil {
		return nil, storage.ErrMobileEntityGone
	}
	return voice, err
}

func UploadVoiceAudio(ctx context.Context, store storage.Store, objects objectstore.Store, clientID, contentType, expectedSHA256 string, body io.Reader, declaredSize, maxBytes int64) (*model.VoiceNote, error) {
	if objects == nil {
		return nil, ErrVoiceStorageUnavailable
	}
	if maxBytes <= 0 {
		maxBytes = defaultVoiceMaxBytes
	}
	if declaredSize > maxBytes {
		return nil, ErrVoiceAudioTooLarge
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || !supportedVoiceMediaType(mediaType) {
		return nil, ErrVoiceAudioType
	}
	expectedSHA256 = strings.ToLower(strings.TrimSpace(expectedSHA256))
	if expectedSHA256 != "" {
		decoded, err := hex.DecodeString(expectedSHA256)
		if err != nil || len(decoded) != sha256.Size {
			return nil, ErrVoiceAudioChecksum
		}
	}

	temp, err := os.CreateTemp("", "flowspace-voice-*")
	if err != nil {
		return nil, err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(temp, hasher), io.LimitReader(body, maxBytes+1))
	closeErr := temp.Close()
	if copyErr != nil {
		return nil, copyErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if written == 0 {
		return nil, ErrInvalidVoiceMetadata
	}
	if written > maxBytes {
		return nil, ErrVoiceAudioTooLarge
	}
	actualSHA256 := hex.EncodeToString(hasher.Sum(nil))
	if expectedSHA256 != "" && expectedSHA256 != actualSHA256 {
		return nil, ErrVoiceAudioChecksum
	}

	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	clientID = strings.TrimSpace(clientID)
	if _, err := uuid.Parse(clientID); err != nil {
		return nil, ErrInvalidVoiceClientID
	}
	extension := voiceFileExtension(mediaType)
	objectKey := filepath.ToSlash(filepath.Join("voice-notes", objectWorkspaceSegment(workspaceID), clientID+extension))
	nativeStore, err := storage.NativeStoreFrom(store)
	if err != nil {
		return nil, err
	}
	claimed, err := nativeStore.VoiceNotes().ClaimUpload(ctx, clientID, model.VoiceUploadClaim{
		ObjectKey: objectKey,
		MimeType:  mediaType,
		Size:      written,
		SHA256:    actualSHA256,
	})
	if err != nil {
		return nil, err
	}
	if claimed.UploadState == model.VoiceUploadUploaded {
		return claimed, nil
	}
	reader, err := os.Open(tempPath)
	if err != nil {
		_ = nativeStore.VoiceNotes().MarkUploadFailed(ctx, clientID, actualSHA256)
		return nil, err
	}
	putErr := objects.Put(ctx, objectKey, reader, written, mediaType)
	closeErr = reader.Close()
	if putErr != nil {
		_ = nativeStore.VoiceNotes().MarkUploadFailed(ctx, clientID, actualSHA256)
		if errors.Is(putErr, objectstore.ErrUnavailable) {
			return nil, ErrVoiceStorageUnavailable
		}
		return nil, putErr
	}
	if closeErr != nil {
		_ = nativeStore.VoiceNotes().MarkUploadFailed(ctx, clientID, actualSHA256)
		return nil, closeErr
	}
	uploaded, err := nativeStore.VoiceNotes().MarkUploaded(ctx, clientID, actualSHA256)
	if errors.Is(err, storage.ErrVoiceAudioGone) {
		_ = objects.Remove(ctx, objectKey)
	}
	return uploaded, err
}

func GetVoiceAudio(ctx context.Context, store storage.Store, objects objectstore.Store, clientID string) (*model.VoiceNote, *objectstore.Object, error) {
	voice, err := GetVoiceNote(ctx, store, clientID)
	if err != nil {
		return nil, nil, err
	}
	if voice.UploadState != model.VoiceUploadUploaded || voice.ObjectKey == "" {
		if voice.AudioState == model.VoiceAudioDeleteRequested || voice.AudioState == model.VoiceAudioDeleted {
			return voice, nil, storage.ErrVoiceAudioGone
		}
		return voice, nil, ErrVoiceAudioNotUploaded
	}
	if objects == nil {
		return voice, nil, ErrVoiceStorageUnavailable
	}
	object, err := objects.Get(ctx, voice.ObjectKey)
	if errors.Is(err, objectstore.ErrUnavailable) {
		return voice, nil, ErrVoiceStorageUnavailable
	}
	if err != nil {
		return voice, nil, err
	}
	return voice, object, nil
}

func TranscribeVoiceNote(ctx context.Context, store storage.Store, objects objectstore.Store, transcriber transcription.Transcriber, clientID, language string) (*model.VoiceNote, error) {
	if transcriber == nil {
		return nil, ErrTranscriptionUnavailable
	}
	voice, err := GetVoiceNote(ctx, store, clientID)
	if err != nil {
		return nil, err
	}
	if voice.TranscriptionState == model.TranscriptionCompleted && strings.TrimSpace(voice.Body) != "" {
		return voice, nil
	}
	if voice.UploadState != model.VoiceUploadUploaded || voice.ObjectKey == "" {
		if voice.AudioState == model.VoiceAudioDeleteRequested || voice.AudioState == model.VoiceAudioDeleted {
			return nil, storage.ErrVoiceAudioGone
		}
		return nil, ErrVoiceAudioNotUploaded
	}
	language = strings.TrimSpace(language)
	if language == "" {
		language = voice.Language
	}
	if len([]rune(language)) > 32 {
		return nil, ErrInvalidVoiceMetadata
	}
	nativeStore, err := storage.NativeStoreFrom(store)
	if err != nil {
		return nil, err
	}
	if _, err := nativeStore.VoiceNotes().SetTranscriptionState(ctx, clientID, model.TranscriptionProcessing, ""); err != nil {
		return nil, err
	}
	_, audio, err := GetVoiceAudio(ctx, store, objects, clientID)
	if err != nil {
		_, _ = nativeStore.VoiceNotes().SetTranscriptionState(ctx, clientID, model.TranscriptionFailed, compactError(err))
		return nil, err
	}
	defer audio.Body.Close()
	text, err := transcriber.Transcribe(ctx, transcription.Input{
		Audio:       audio.Body,
		Filename:    clientID + voiceFileExtension(voice.MimeType),
		ContentType: voice.MimeType,
		Language:    language,
	})
	if err != nil {
		_, _ = nativeStore.VoiceNotes().SetTranscriptionState(ctx, clientID, model.TranscriptionFailed, compactError(err))
		if errors.Is(err, transcription.ErrUnavailable) {
			return nil, ErrTranscriptionUnavailable
		}
		return nil, err
	}

	var completed *model.VoiceNote
	err = store.Transact(ctx, func(tx storage.Store) error {
		if _, err := tx.Notes().Update(ctx, voice.NoteID, &model.UpdateNoteRequest{Body: &text}); err != nil {
			return err
		}
		txNative, err := storage.NativeStoreFrom(tx)
		if err != nil {
			return err
		}
		completed, err = txNative.VoiceNotes().SetTranscriptionState(ctx, clientID, model.TranscriptionCompleted, "")
		return err
	})
	if err != nil {
		_, _ = nativeStore.VoiceNotes().SetTranscriptionState(ctx, clientID, model.TranscriptionFailed, compactError(err))
		return nil, err
	}
	return completed, nil
}

func GetWatchSnapshot(ctx context.Context, store storage.Store) (*WatchSnapshot, error) {
	today, err := GetToday(ctx, store, NewRecurrenceService())
	if err != nil {
		return nil, err
	}
	nativeStore, err := storage.NativeStoreFrom(store)
	if err != nil {
		return nil, err
	}
	voiceNotes, err := nativeStore.VoiceNotes().ListRecent(ctx, 5)
	if err != nil {
		return nil, err
	}
	return &WatchSnapshot{Today: today, VoiceNotes: voiceNotes}, nil
}

func supportedVoiceMediaType(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "audio/mp4", "audio/m4a", "audio/x-m4a", "audio/aac", "audio/mpeg", "audio/wav", "audio/x-wav":
		return true
	default:
		return false
	}
}

func voiceFileExtension(mediaType string) string {
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "audio/aac":
		return ".aac"
	case "audio/mpeg":
		return ".mp3"
	case "audio/wav", "audio/x-wav":
		return ".wav"
	default:
		return ".m4a"
	}
}

func compactError(err error) string {
	if err == nil {
		return ""
	}
	message := strings.TrimSpace(err.Error())
	runes := []rune(message)
	if len(runes) > 500 {
		message = string(runes[:500])
	}
	return message
}

func objectWorkspaceSegment(workspaceID string) string {
	digest := sha256.Sum256([]byte(workspaceID))
	return hex.EncodeToString(digest[:16])
}
