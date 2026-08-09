package model

const (
	ContentImportStatusActive      = "active"
	ContentImportStatusCompleted   = "completed"
	ContentImportStatusFailed      = "failed"
	ContentImportStatusNeedsReview = "needs_review"
	ContentImportStatusCanceled    = "canceled"

	ContentImportStageQueued      = "queued"
	ContentImportStageResolving   = "resolving"
	ContentImportStageAcquiring   = "acquiring"
	ContentImportStageSummarizing = "summarizing"
	ContentImportStagePublishing  = "publishing"
	ContentImportStageCompleted   = "completed"
	ContentImportStageTerminal    = "terminal"
)

type ContentImport struct {
	ID                  string   `json:"id"`
	SourceURL           string   `json:"source_url"`
	SourceType          string   `json:"source_type,omitempty"`
	CanonicalURL        string   `json:"canonical_url,omitempty"`
	ExternalID          string   `json:"external_id,omitempty"`
	FeedURL             string   `json:"feed_url,omitempty"`
	Title               string   `json:"title,omitempty"`
	PodcastTitle        string   `json:"podcast_title,omitempty"`
	CoverURL            string   `json:"cover_url,omitempty"`
	Description         string   `json:"description,omitempty"`
	DurationSeconds     int64    `json:"duration_seconds,omitempty"`
	TranscriptURL       string   `json:"-"`
	AudioURL            string   `json:"-"`
	Status              string   `json:"status"`
	Stage               string   `json:"stage"`
	Progress            int      `json:"progress"`
	SummarizeWithAI     bool     `json:"summarize_with_ai"`
	SummaryPrompt       string   `json:"summary_prompt,omitempty"`
	IncludeTranscript   bool     `json:"include_transcript"`
	Language            string   `json:"language"`
	FolderID            string   `json:"folder_id,omitempty"`
	ProjectIDs          []string `json:"project_ids"`
	Tags                []string `json:"tags"`
	ResultNoteID        string   `json:"result_note_id,omitempty"`
	ResultNoteAvailable bool     `json:"result_note_available"`
	ErrorCode           string   `json:"error_code,omitempty"`
	ErrorMessage        string   `json:"error_message,omitempty"`
	Retryable           bool     `json:"retryable"`
	Revision            int64    `json:"revision"`
	CreatedAt           int64    `json:"created_at"`
	UpdatedAt           int64    `json:"updated_at"`
	Attempt             int64    `json:"-"`
	MaxAttempts         int64    `json:"-"`
}

type CreateContentImport struct {
	ID                string
	IdempotencyKey    string
	RequestSHA256     string
	SourceURL         string
	SummarizeWithAI   bool
	SummaryPrompt     string
	IncludeTranscript bool
	Language          string
	FolderID          string
	ProjectIDs        []string
	Tags              []string
	Now               int64
}

type ContentImportFilter struct {
	Status   string
	Page     int
	PageSize int
}

type ContentImportLease struct {
	Import         ContentImport
	WorkspaceID    string
	LeaseToken     string
	LeaseExpiresAt int64
}

type ClaimContentImport struct {
	WorkerID       string
	LeaseToken     string
	Now            int64
	LeaseExpiresAt int64
}

type UpdateContentImportResolved struct {
	ID              string
	LeaseToken      string
	SourceType      string
	CanonicalURL    string
	ExternalID      string
	FeedURL         string
	Title           string
	PodcastTitle    string
	CoverURL        string
	Description     string
	DurationSeconds int64
	TranscriptURL   string
	AudioURL        string
	Now             int64
}

type UpdateContentImportStage struct {
	ID         string
	LeaseToken string
	Stage      string
	Progress   int
	Now        int64
}

type HeartbeatContentImport struct {
	ID             string
	LeaseToken     string
	Now            int64
	LeaseExpiresAt int64
}

type FailContentImport struct {
	ID           string
	LeaseToken   string
	Status       string
	ErrorCode    string
	ErrorMessage string
	Now          int64
}

type CompleteContentImport struct {
	ID           string
	LeaseToken   string
	ResultNoteID string
	Now          int64
}

type ContentImportArtifact struct {
	ImportID  string
	Kind      string
	Text      string
	SHA256    string
	CreatedAt int64
	UpdatedAt int64
}
