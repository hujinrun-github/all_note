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
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/objectstore"
	"github.com/hujinrun/flowspace/internal/storage"
)

var (
	ErrAttachmentInvalidMetadata = errors.New("invalid attachment metadata")
	ErrAttachmentTooLarge        = errors.New("attachment exceeds the configured size limit")
	ErrAttachmentLimitReached    = errors.New("note attachment limit reached")
	ErrAttachmentReadOnly        = errors.New("this attachment is managed by the voice-note lifecycle")
	ErrAttachmentStorage         = errors.New("attachment storage is unavailable")
)

const (
	defaultAttachmentMaxBytes = int64(200 * 1024 * 1024)
	maxAttachmentsPerNote     = 20
	maxAttachmentNameRunes    = 255
)

func ListNoteAttachments(ctx context.Context, store storage.Store, noteID string) ([]model.NoteAttachment, error) {
	noteID = strings.TrimSpace(noteID)
	if noteID == "" {
		return nil, sql.ErrNoRows
	}
	if _, err := store.Notes().GetByID(ctx, noteID); err != nil {
		return nil, err
	}
	attachmentStore, err := storage.NoteAttachmentStoreFrom(store)
	if err != nil {
		return nil, err
	}
	attachments, err := attachmentStore.NoteAttachments().ListByNote(ctx, noteID)
	if err != nil {
		return nil, err
	}
	voiceAttachment, err := getVoiceNoteAttachment(ctx, store, noteID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) && !errors.Is(err, storage.ErrNativeStorage) {
		return nil, err
	}
	if voiceAttachment != nil {
		attachments = append(attachments, *voiceAttachment)
	}
	for index := range attachments {
		attachments[index].ContentURL = noteAttachmentContentURL(noteID, attachments[index].ID)
	}
	return attachments, nil
}

func UploadNoteAttachment(
	ctx context.Context,
	store storage.Store,
	objects objectstore.Store,
	noteID, originalName, contentType string,
	body io.Reader,
	declaredSize, maxBytes int64,
) (*model.NoteAttachment, error) {
	if objects == nil {
		return nil, ErrAttachmentStorage
	}
	noteID = strings.TrimSpace(noteID)
	if noteID == "" {
		return nil, sql.ErrNoRows
	}
	if _, err := store.Notes().GetByID(ctx, noteID); err != nil {
		return nil, err
	}
	attachments, err := ListNoteAttachments(ctx, store, noteID)
	if err != nil {
		return nil, err
	}
	if len(attachments) >= maxAttachmentsPerNote {
		return nil, ErrAttachmentLimitReached
	}
	if maxBytes <= 0 {
		maxBytes = defaultAttachmentMaxBytes
	}
	if declaredSize > maxBytes {
		return nil, ErrAttachmentTooLarge
	}
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(contentType))
	if err != nil || strings.TrimSpace(mediaType) == "" {
		return nil, ErrAttachmentInvalidMetadata
	}
	originalName, err = normalizeAttachmentName(originalName)
	if err != nil {
		return nil, err
	}

	temp, err := os.CreateTemp("", "flowspace-attachment-*")
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
		return nil, ErrAttachmentInvalidMetadata
	}
	if written > maxBytes {
		return nil, ErrAttachmentTooLarge
	}

	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	attachmentID := uuid.NewString()
	objectKey := filepath.ToSlash(filepath.Join(
		"note-attachments",
		objectWorkspaceSegment(workspaceID),
		noteID,
		attachmentID+safeAttachmentExtension(originalName),
	))
	reader, err := os.Open(tempPath)
	if err != nil {
		return nil, err
	}
	putErr := objects.Put(ctx, objectKey, reader, written, mediaType)
	readerCloseErr := reader.Close()
	if putErr != nil {
		if errors.Is(putErr, objectstore.ErrUnavailable) {
			return nil, ErrAttachmentStorage
		}
		return nil, putErr
	}
	if readerCloseErr != nil {
		_ = objects.Remove(ctx, objectKey)
		return nil, readerCloseErr
	}

	attachment := &model.NoteAttachment{
		ID:           attachmentID,
		NoteID:       noteID,
		Kind:         classifyAttachment(mediaType),
		OriginalName: originalName,
		MimeType:     mediaType,
		SizeBytes:    written,
		SHA256:       hex.EncodeToString(hasher.Sum(nil)),
		Source:       model.NoteAttachmentSourceUpload,
		Deletable:    true,
		CreatedAt:    time.Now().UTC().Unix(),
		WorkspaceID:  workspaceID,
		ObjectKey:    objectKey,
	}
	attachmentStore, err := storage.NoteAttachmentStoreFrom(store)
	if err != nil {
		_ = objects.Remove(ctx, objectKey)
		return nil, err
	}
	if err := attachmentStore.NoteAttachments().Create(ctx, attachment); err != nil {
		_ = objects.Remove(ctx, objectKey)
		return nil, err
	}
	attachment.ContentURL = noteAttachmentContentURL(noteID, attachment.ID)
	return attachment, nil
}

func GetNoteAttachment(
	ctx context.Context,
	store storage.Store,
	objects objectstore.Store,
	noteID, attachmentID string,
) (*model.NoteAttachment, *objectstore.Object, error) {
	if objects == nil {
		return nil, nil, ErrAttachmentStorage
	}
	attachmentStore, err := storage.NoteAttachmentStoreFrom(store)
	if err != nil {
		return nil, nil, err
	}
	noteID = strings.TrimSpace(noteID)
	attachmentID = strings.TrimSpace(attachmentID)
	attachment, err := attachmentStore.NoteAttachments().GetByNoteAndID(
		ctx, noteID, attachmentID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		attachment, err = getVoiceNoteAttachment(ctx, store, noteID)
		if err == nil && attachment.ID != attachmentID {
			err = sql.ErrNoRows
		}
	}
	if err != nil {
		return nil, nil, err
	}
	object, err := objects.Get(ctx, attachment.ObjectKey)
	if errors.Is(err, objectstore.ErrUnavailable) {
		return nil, nil, ErrAttachmentStorage
	}
	if err != nil {
		return nil, nil, err
	}
	attachment.ContentURL = noteAttachmentContentURL(attachment.NoteID, attachment.ID)
	return attachment, object, nil
}

func DeleteNoteAttachment(
	ctx context.Context,
	store storage.Store,
	objects objectstore.Store,
	noteID, attachmentID string,
) error {
	attachmentStore, err := storage.NoteAttachmentStoreFrom(store)
	if err != nil {
		return err
	}
	noteID = strings.TrimSpace(noteID)
	attachmentID = strings.TrimSpace(attachmentID)
	attachment, err := attachmentStore.NoteAttachments().GetByNoteAndID(
		ctx, noteID, attachmentID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		attachment, err = getVoiceNoteAttachment(ctx, store, noteID)
		if err == nil && attachment.ID != attachmentID {
			err = sql.ErrNoRows
		}
	}
	if err != nil {
		return err
	}
	if !attachment.Deletable || attachment.Source != model.NoteAttachmentSourceUpload {
		return ErrAttachmentReadOnly
	}
	if objects == nil {
		return ErrAttachmentStorage
	}
	if err := objects.Remove(ctx, attachment.ObjectKey); err != nil && !errors.Is(err, objectstore.ErrNotFound) {
		if errors.Is(err, objectstore.ErrUnavailable) {
			return ErrAttachmentStorage
		}
		return err
	}
	return attachmentStore.NoteAttachments().Delete(ctx, noteID, attachmentID)
}

func normalizeAttachmentName(value string) (string, error) {
	value = filepath.Base(strings.TrimSpace(value))
	if value == "" || value == "." || value == string(filepath.Separator) ||
		!utf8.ValidString(value) || len([]rune(value)) > maxAttachmentNameRunes {
		return "", ErrAttachmentInvalidMetadata
	}
	return value, nil
}

func classifyAttachment(mediaType string) string {
	normalized := strings.ToLower(strings.TrimSpace(mediaType))
	switch {
	case strings.HasPrefix(normalized, "audio/"):
		return model.NoteAttachmentKindAudio
	case strings.HasPrefix(normalized, "video/"):
		return model.NoteAttachmentKindVideo
	case normalized == "image/jpeg", normalized == "image/png",
		normalized == "image/webp", normalized == "image/gif":
		return model.NoteAttachmentKindImage
	default:
		return model.NoteAttachmentKindFile
	}
}

func safeAttachmentExtension(name string) string {
	extension := strings.ToLower(filepath.Ext(name))
	if len(extension) < 2 || len(extension) > 12 {
		return ""
	}
	for _, character := range extension[1:] {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') {
			return ""
		}
	}
	return extension
}

func noteAttachmentContentURL(noteID, attachmentID string) string {
	return fmt.Sprintf(
		"/api/notes/%s/attachments/%s/content",
		url.PathEscape(noteID),
		url.PathEscape(attachmentID),
	)
}

func getVoiceNoteAttachment(
	ctx context.Context,
	store storage.Store,
	noteID string,
) (*model.NoteAttachment, error) {
	nativeStore, err := storage.NativeStoreFrom(store)
	if err != nil {
		return nil, err
	}
	voice, err := nativeStore.VoiceNotes().GetByNoteID(ctx, noteID)
	if err != nil {
		return nil, err
	}
	return &model.NoteAttachment{
		ID:                 voice.ClientID,
		NoteID:             voice.NoteID,
		Kind:               model.NoteAttachmentKindAudio,
		OriginalName:       voiceAttachmentName(voice.Title, voice.MimeType),
		MimeType:           voice.MimeType,
		SizeBytes:          voice.AudioSize,
		SHA256:             voice.AudioSHA256,
		Source:             model.NoteAttachmentSourceVoiceNote,
		Deletable:          false,
		CreatedAt:          voice.CreatedAt,
		TranscriptionState: voice.TranscriptionState,
		TranscriptionError: voice.TranscriptionError,
		WorkspaceID:        voice.WorkspaceID,
		ObjectKey:          voice.ObjectKey,
	}, nil
}

func voiceAttachmentName(title, mimeType string) string {
	extension := ".m4a"
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "audio/aac":
		extension = ".aac"
	case "audio/mpeg":
		extension = ".mp3"
	case "audio/wav", "audio/x-wav":
		extension = ".wav"
	}
	name := strings.TrimSpace(title)
	if name == "" {
		name = "语音笔记"
	}
	if filepath.Ext(name) == "" {
		name += extension
	}
	return name
}
