package model

const (
	VoiceUploadPending   = "pending"
	VoiceUploadUploading = "uploading"
	VoiceUploadUploaded  = "uploaded"
	VoiceUploadFailed    = "failed"

	TranscriptionNotStarted = "not_started"
	TranscriptionProcessing = "processing"
	TranscriptionCompleted  = "completed"
	TranscriptionFailed     = "failed"
)

type WatchDevice struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	ExpiresAt  int64  `json:"expires_at"`
	LastSeenAt int64  `json:"last_seen_at"`
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
	RevokedAt  *int64 `json:"revoked_at,omitempty"`

	UserID      string `json:"-"`
	WorkspaceID string `json:"-"`
	TokenHash   string `json:"-"`
}

type AuthorizeWatchDeviceRequest struct {
	Name          string `json:"name"`
	ExpiresInDays int    `json:"expires_in_days"`
}

type AuthorizeWatchDeviceResponse struct {
	Device WatchDevice `json:"device"`
	Token  string      `json:"token"`
}

type RevokeWatchDeviceRequest struct {
	DeviceID string `json:"device_id" binding:"required"`
}

type VoiceNote struct {
	ID                 string `json:"id"`
	ClientID           string `json:"client_id"`
	NoteID             string `json:"note_id"`
	Title              string `json:"title"`
	Body               string `json:"body"`
	DurationMS         int64  `json:"duration_ms"`
	RecordedAt         int64  `json:"recorded_at"`
	Language           string `json:"language"`
	UploadState        string `json:"upload_state"`
	TranscriptionState string `json:"transcription_state"`
	TranscriptionError string `json:"transcription_error,omitempty"`
	MimeType           string `json:"mime_type,omitempty"`
	AudioSize          int64  `json:"audio_size,omitempty"`
	AudioSHA256        string `json:"audio_sha256,omitempty"`
	CreatedAt          int64  `json:"created_at"`
	UpdatedAt          int64  `json:"updated_at"`
	WorkspaceID        string `json:"-"`
	ObjectKey          string `json:"-"`
}

type CreateVoiceNoteRequest struct {
	ClientID   string `json:"client_id" binding:"required"`
	Title      string `json:"title"`
	DurationMS int64  `json:"duration_ms"`
	RecordedAt int64  `json:"recorded_at"`
	Language   string `json:"language"`
}

type VoiceUploadClaim struct {
	ObjectKey string
	MimeType  string
	Size      int64
	SHA256    string
}

type VoiceTranscriptionRequest struct {
	Language string `json:"language"`
}

type VoiceNoteStatusResponse struct {
	VoiceNote VoiceNote `json:"voice_note"`
}
