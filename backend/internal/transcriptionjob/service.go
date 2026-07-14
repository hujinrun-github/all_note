package transcriptionjob

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

var ErrInvalidRequest = errors.New("invalid transcription job request")

type Service struct {
	Now   func() time.Time
	NewID func() string
}

func NewService() Service {
	return Service{Now: time.Now, NewID: uuid.NewString}
}

func (s Service) Create(ctx context.Context, store storage.Store, mutationID, voiceNoteID, language string) (*model.TranscriptionJob, error) {
	mutationID = strings.TrimSpace(mutationID)
	voiceNoteID = strings.TrimSpace(voiceNoteID)
	language = strings.TrimSpace(language)
	if _, err := uuid.Parse(mutationID); err != nil {
		return nil, ErrInvalidRequest
	}
	if _, err := uuid.Parse(voiceNoteID); err != nil || language == "" {
		return nil, ErrInvalidRequest
	}
	if s.Now == nil || s.NewID == nil {
		return nil, errors.New("transcription job service dependencies are not configured")
	}
	repository, err := storage.TranscriptionJobRepositoryFrom(store)
	if err != nil {
		return nil, err
	}
	requestHash, err := RequestHash(voiceNoteID, language)
	if err != nil {
		return nil, err
	}
	return repository.CreateOrGet(ctx, model.CreateTranscriptionJob{
		JobID:         s.NewID(),
		MutationID:    mutationID,
		RequestSHA256: requestHash,
		VoiceNoteID:   voiceNoteID,
		Language:      language,
		Now:           s.Now().UTC().Unix(),
	})
}

func (s Service) Get(ctx context.Context, store storage.Store, jobID string) (*model.TranscriptionJob, error) {
	if _, err := uuid.Parse(strings.TrimSpace(jobID)); err != nil {
		return nil, ErrInvalidRequest
	}
	repository, err := storage.TranscriptionJobRepositoryFrom(store)
	if err != nil {
		return nil, err
	}
	return repository.Get(ctx, jobID)
}

func (s Service) Retry(ctx context.Context, store storage.Store, mutationID, failedJobID string) (*model.TranscriptionJob, error) {
	mutationID = strings.TrimSpace(mutationID)
	failedJobID = strings.TrimSpace(failedJobID)
	if _, err := uuid.Parse(mutationID); err != nil {
		return nil, ErrInvalidRequest
	}
	if _, err := uuid.Parse(failedJobID); err != nil {
		return nil, ErrInvalidRequest
	}
	if s.Now == nil || s.NewID == nil {
		return nil, errors.New("transcription job service dependencies are not configured")
	}
	repository, err := storage.TranscriptionJobRepositoryFrom(store)
	if err != nil {
		return nil, err
	}
	requestHash, err := RetryRequestHash(failedJobID)
	if err != nil {
		return nil, err
	}
	return repository.Retry(ctx, model.RetryTranscriptionJob{
		JobID: s.NewID(), MutationID: mutationID, RequestSHA256: requestHash,
		FailedJobID: failedJobID, Now: s.Now().UTC().Unix(),
	})
}

func RequestHash(voiceNoteID, language string) (string, error) {
	encoded, err := json.Marshal(struct {
		VoiceNoteID string `json:"voice_note_id"`
		Language    string `json:"language"`
	}{VoiceNoteID: strings.TrimSpace(voiceNoteID), Language: strings.TrimSpace(language)})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func RetryRequestHash(failedJobID string) (string, error) {
	encoded, err := json.Marshal(struct {
		Operation   string `json:"operation"`
		FailedJobID string `json:"failed_job_id"`
	}{Operation: "retry", FailedJobID: strings.TrimSpace(failedJobID)})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}
