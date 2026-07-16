package model

const (
	VoiceAudioCleanupQueued       = "queued"
	VoiceAudioCleanupProcessing   = "processing"
	VoiceAudioCleanupRetryWaiting = "retry_waiting"
	VoiceAudioCleanupCompleted    = "completed"
	VoiceAudioCleanupFailed       = "failed"
)

type VoiceAudioCleanupJob struct {
	JobID         string
	VoiceNoteID   string
	ObjectKey     string
	State         string
	Revision      int64
	Attempt       int64
	MaxAttempts   int64
	ErrorCode     string
	NextAttemptAt *int64
	CreatedAt     int64
	UpdatedAt     int64
	WorkspaceID   string
}

type ClaimVoiceAudioCleanupJob struct {
	WorkerID       string
	LeaseToken     string
	Now            int64
	LeaseExpiresAt int64
}

type VoiceAudioCleanupLease struct {
	Job            VoiceAudioCleanupJob
	WorkspaceID    string
	LeaseToken     string
	LeaseExpiresAt int64
}

type CompleteVoiceAudioCleanupJob struct {
	JobID      string
	LeaseToken string
	Now        int64
}

type FailVoiceAudioCleanupJob struct {
	JobID         string
	LeaseToken    string
	ErrorCode     string
	NextAttemptAt int64
	Now           int64
}
