package service

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/google/uuid"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/objectstore"
)

func TestNoteAttachmentRoundTripAndDelete(t *testing.T) {
	store, ctx := openNativeServiceStore(t)
	note, err := CreateNote(ctx, store, &model.CreateNoteRequest{
		Title: "Attachment note", Body: "", FolderID: "__uncategorized", Tags: "[]",
	})
	if err != nil {
		t.Fatalf("create note: %v", err)
	}
	objects := objectstore.NewMemoryStore()
	video := []byte("synthetic-video")
	attachment, err := UploadNoteAttachment(
		ctx, store, objects, note.ID, "demo.mp4", "video/mp4",
		bytes.NewReader(video), int64(len(video)), 1024,
	)
	if err != nil {
		t.Fatalf("upload attachment: %v", err)
	}
	if attachment.Kind != model.NoteAttachmentKindVideo ||
		attachment.Source != model.NoteAttachmentSourceUpload ||
		!attachment.Deletable ||
		attachment.ContentURL == "" {
		t.Fatalf("attachment = %+v", attachment)
	}

	attachments, err := ListNoteAttachments(ctx, store, note.ID)
	if err != nil {
		t.Fatalf("list attachments: %v", err)
	}
	if len(attachments) != 1 || attachments[0].ID != attachment.ID {
		t.Fatalf("attachments = %+v", attachments)
	}
	_, object, err := GetNoteAttachment(ctx, store, objects, note.ID, attachment.ID)
	if err != nil {
		t.Fatalf("get attachment: %v", err)
	}
	stored, readErr := io.ReadAll(object.Body)
	closeErr := object.Body.Close()
	if readErr != nil || closeErr != nil || !bytes.Equal(stored, video) {
		t.Fatalf("read attachment: data=%q readErr=%v closeErr=%v", stored, readErr, closeErr)
	}

	if err := DeleteNoteAttachment(ctx, store, objects, note.ID, attachment.ID); err != nil {
		t.Fatalf("delete attachment: %v", err)
	}
	attachments, err = ListNoteAttachments(ctx, store, note.ID)
	if err != nil || len(attachments) != 0 {
		t.Fatalf("attachments after delete = %+v, err=%v", attachments, err)
	}
}

func TestListNoteAttachmentsIncludesExistingVoiceAudio(t *testing.T) {
	store, ctx := openNativeServiceStore(t)
	clientID := uuid.NewString()
	voice, _, err := CreateVoiceNote(ctx, store, model.CreateVoiceNoteRequest{
		ClientID: clientID,
		Title:    "散步录音",
	})
	if err != nil {
		t.Fatalf("create voice note: %v", err)
	}
	objects := objectstore.NewMemoryStore()
	audio := []byte("synthetic-m4a")
	if _, err := UploadVoiceAudio(
		ctx, store, objects, clientID, "audio/mp4", "",
		bytes.NewReader(audio), int64(len(audio)), 1024,
	); err != nil {
		t.Fatalf("upload voice audio: %v", err)
	}

	attachments, err := ListNoteAttachments(ctx, store, voice.NoteID)
	if err != nil {
		t.Fatalf("list attachments: %v", err)
	}
	if len(attachments) != 1 ||
		attachments[0].ID != clientID ||
		attachments[0].Source != model.NoteAttachmentSourceVoiceNote ||
		attachments[0].Deletable ||
		attachments[0].OriginalName != "散步录音.m4a" {
		t.Fatalf("voice attachment = %+v", attachments)
	}
	if err := DeleteNoteAttachment(ctx, store, objects, voice.NoteID, clientID); !errors.Is(err, ErrAttachmentReadOnly) {
		t.Fatalf("delete voice attachment error = %v", err)
	}
}

func TestUploadNoteAttachmentValidatesSizeAndMetadata(t *testing.T) {
	store, ctx := openNativeServiceStore(t)
	note, err := CreateNote(ctx, store, &model.CreateNoteRequest{
		Title: "Limits", FolderID: "__uncategorized", Tags: "[]",
	})
	if err != nil {
		t.Fatalf("create note: %v", err)
	}
	objects := objectstore.NewMemoryStore()
	if _, err := UploadNoteAttachment(
		ctx, store, objects, note.ID, "large.mp4", "video/mp4",
		bytes.NewReader([]byte("12345")), 5, 4,
	); !errors.Is(err, ErrAttachmentTooLarge) {
		t.Fatalf("large attachment error = %v", err)
	}
	if _, err := UploadNoteAttachment(
		ctx, store, objects, note.ID, "", "video/mp4",
		bytes.NewReader([]byte("1")), 1, 4,
	); !errors.Is(err, ErrAttachmentInvalidMetadata) {
		t.Fatalf("invalid name error = %v", err)
	}
}
