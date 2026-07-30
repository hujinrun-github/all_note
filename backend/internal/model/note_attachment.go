package model

const (
	NoteAttachmentKindAudio = "audio"
	NoteAttachmentKindVideo = "video"
	NoteAttachmentKindImage = "image"
	NoteAttachmentKindFile  = "file"

	NoteAttachmentSourceUpload    = "upload"
	NoteAttachmentSourceVoiceNote = "voice_note"
)

type NoteAttachment struct {
	ID           string `json:"id"`
	NoteID       string `json:"note_id"`
	Kind         string `json:"kind"`
	OriginalName string `json:"original_name"`
	MimeType     string `json:"mime_type"`
	SizeBytes    int64  `json:"size_bytes"`
	SHA256       string `json:"sha256"`
	Source       string `json:"source"`
	Deletable    bool   `json:"deletable"`
	CreatedAt    int64  `json:"created_at"`
	ContentURL   string `json:"content_url,omitempty"`

	WorkspaceID string `json:"-"`
	ObjectKey   string `json:"-"`
}
