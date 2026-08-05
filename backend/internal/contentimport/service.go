package contentimport

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hujinrun/flowspace/internal/contentsource"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

var ErrInvalidRequest = errors.New("invalid content import request")

type CreateRequest struct {
	SourceURL         string   `json:"source_url"`
	SummarizeWithAI   bool     `json:"summarize_with_ai"`
	IncludeTranscript bool     `json:"include_transcript"`
	Language          string   `json:"language"`
	FolderID          string   `json:"folder_id"`
	ProjectIDs        []string `json:"project_ids"`
	Tags              []string `json:"tags"`
}

type Service struct {
	Store    storage.Store
	Resolver contentsource.Resolver
	Now      func() time.Time
	NewID    func() string
}

func NewService(store storage.Store, resolver contentsource.Resolver) (*Service, error) {
	if store == nil || resolver == nil {
		return nil, errors.New("content import service dependencies are required")
	}
	return &Service{Store: store, Resolver: resolver, Now: time.Now, NewID: uuid.NewString}, nil
}

func (s *Service) Resolve(ctx context.Context, sourceURL string) (*contentsource.Episode, error) {
	if strings.TrimSpace(sourceURL) == "" {
		return nil, ErrInvalidRequest
	}
	return s.Resolver.Resolve(ctx, sourceURL)
}

func (s *Service) Create(ctx context.Context, idempotencyKey string, request CreateRequest) (*model.ContentImport, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if _, err := uuid.Parse(idempotencyKey); err != nil {
		return nil, ErrInvalidRequest
	}
	request.SourceURL = strings.TrimSpace(request.SourceURL)
	parsed, err := url.Parse(request.SourceURL)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, contentsource.ErrInvalidURL
	}
	if request.Language == "" {
		request.Language = "auto"
	}
	if !request.SummarizeWithAI {
		request.IncludeTranscript = true
	}
	if s.Now == nil || s.NewID == nil {
		return nil, errors.New("content import service is not configured")
	}
	hash, err := requestHash(request)
	if err != nil {
		return nil, err
	}
	repository, err := storage.ContentImportRepositoryFrom(s.Store)
	if err != nil {
		return nil, err
	}
	now := s.Now().UTC().Unix()
	return repository.CreateOrGet(ctx, model.CreateContentImport{
		ID: s.NewID(), IdempotencyKey: idempotencyKey, RequestSHA256: hash, SourceURL: request.SourceURL,
		SummarizeWithAI: request.SummarizeWithAI, IncludeTranscript: request.IncludeTranscript,
		Language: request.Language, FolderID: strings.TrimSpace(request.FolderID),
		ProjectIDs: compactStrings(request.ProjectIDs), Tags: compactStrings(request.Tags), Now: now,
	})
}

func (s *Service) List(ctx context.Context, filter model.ContentImportFilter) ([]model.ContentImport, int, error) {
	repository, err := storage.ContentImportRepositoryFrom(s.Store)
	if err != nil {
		return nil, 0, err
	}
	items, total, err := repository.List(ctx, filter)
	if err != nil {
		return nil, 0, err
	}
	for index := range items {
		if err := s.setResultNoteAvailability(ctx, &items[index]); err != nil {
			return nil, 0, err
		}
	}
	return items, total, nil
}

func (s *Service) Get(ctx context.Context, id string) (*model.ContentImport, error) {
	if _, err := uuid.Parse(strings.TrimSpace(id)); err != nil {
		return nil, ErrInvalidRequest
	}
	repository, err := storage.ContentImportRepositoryFrom(s.Store)
	if err != nil {
		return nil, err
	}
	item, err := repository.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.setResultNoteAvailability(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *Service) GetByResultNoteID(ctx context.Context, noteID string) (*model.ContentImport, error) {
	noteID = strings.TrimSpace(noteID)
	if noteID == "" {
		return nil, ErrInvalidRequest
	}
	repository, err := storage.ContentImportRepositoryFrom(s.Store)
	if err != nil {
		return nil, err
	}
	item, err := repository.GetByResultNoteID(ctx, noteID)
	if err != nil {
		return nil, err
	}
	if err := s.setResultNoteAvailability(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *Service) GetTranscript(ctx context.Context, id string) (string, error) {
	if _, err := s.Get(ctx, id); err != nil {
		return "", err
	}
	repository, err := storage.ContentImportRepositoryFrom(s.Store)
	if err != nil {
		return "", err
	}
	artifact, err := repository.GetArtifact(ctx, id, "transcript_normalized")
	if err != nil {
		return "", err
	}
	return artifact.Text, nil
}

func (s *Service) Cancel(ctx context.Context, id string) (*model.ContentImport, error) {
	if _, err := uuid.Parse(strings.TrimSpace(id)); err != nil {
		return nil, ErrInvalidRequest
	}
	repository, err := storage.ContentImportRepositoryFrom(s.Store)
	if err != nil {
		return nil, err
	}
	return repository.Cancel(ctx, id, s.Now().UTC().Unix())
}

func (s *Service) Retry(ctx context.Context, id string) (*model.ContentImport, error) {
	if _, err := uuid.Parse(strings.TrimSpace(id)); err != nil {
		return nil, ErrInvalidRequest
	}
	repository, err := storage.ContentImportRepositoryFrom(s.Store)
	if err != nil {
		return nil, err
	}
	return repository.Retry(ctx, id, s.Now().UTC().Unix())
}

func (s *Service) Delete(ctx context.Context, id string) error {
	if _, err := uuid.Parse(strings.TrimSpace(id)); err != nil {
		return ErrInvalidRequest
	}
	repository, err := storage.ContentImportRepositoryFrom(s.Store)
	if err != nil {
		return err
	}
	return repository.Delete(ctx, id)
}

func (s *Service) setResultNoteAvailability(ctx context.Context, item *model.ContentImport) error {
	if item.ResultNoteID == "" {
		item.ResultNoteAvailable = false
		return nil
	}
	_, err := s.Store.Notes().GetByID(ctx, item.ResultNoteID)
	if errors.Is(err, sql.ErrNoRows) {
		item.ResultNoteAvailable = false
		return nil
	}
	if err != nil {
		return err
	}
	item.ResultNoteAvailable = true
	return nil
}

func requestHash(request CreateRequest) (string, error) {
	encoded, err := json.Marshal(request)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func compactStrings(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
