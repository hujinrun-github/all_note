package model

const (
	TranscriptionJobWaitingForAudio = "waiting_for_audio"
	TranscriptionJobQueued          = "queued"
	TranscriptionJobProcessing      = "processing"
	TranscriptionJobRetryWaiting    = "retry_waiting"
	TranscriptionJobCompleted       = "completed"
	TranscriptionJobNeedsReview     = "needs_review"
	TranscriptionJobFailed          = "failed"
	TranscriptionJobCanceled        = "canceled"
)

type TranscriptionJob struct {
	JobID         string `json:"job_id"`
	VoiceNoteID   string `json:"voice_note_id"`
	Generation    int64  `json:"generation"`
	State         string `json:"state"`
	Revision      int64  `json:"revision"`
	ErrorCode     string `json:"error_code,omitempty"`
	NextAttemptAt *int64 `json:"next_attempt_at,omitempty"`
	Language      string `json:"-"`
	Attempt       int64  `json:"-"`
	MaxAttempts   int64  `json:"-"`
	CreatedAt     int64  `json:"-"`
	UpdatedAt     int64  `json:"-"`
}

type CreateTranscriptionJob struct {
	JobID         string
	MutationID    string
	RequestSHA256 string
	VoiceNoteID   string
	Language      string
	Now           int64
}

type RetryTranscriptionJob struct {
	JobID         string
	MutationID    string
	RequestSHA256 string
	FailedJobID   string
	Now           int64
}

type ClaimTranscriptionJob struct {
	WorkerID       string
	LeaseToken     string
	Now            int64
	LeaseExpiresAt int64
}

type TranscriptionJobLease struct {
	Job            TranscriptionJob
	WorkspaceID    string
	LeaseToken     string
	LeaseExpiresAt int64
}

type HeartbeatTranscriptionJob struct {
	JobID          string
	LeaseToken     string
	Now            int64
	LeaseExpiresAt int64
}

type FailTranscriptionJob struct {
	JobID         string
	LeaseToken    string
	ErrorCode     string
	NextAttemptAt int64
	Now           int64
}

type CompleteTranscriptionJob struct {
	JobID      string
	LeaseToken string
	Text       string
	Now        int64
}
