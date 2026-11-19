package webplanning

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/objectstore"
	"github.com/hujinrun/flowspace/internal/taskdomain"
	"github.com/hujinrun/flowspace/internal/transcription"
)

type Config struct {
	Repository           Repository
	Objects              objectstore.Store
	Transcriber          transcription.Transcriber
	Chat                 Chat
	Features             FeatureResolver
	ResolveTranscription func(context.Context, string) error
	ResolveGeneration    func(context.Context, string) error
	Now                  func() time.Time
	NewID                func() string
}
type Service struct{ config Config }

func New(config Config) (*Service, error) {
	if config.Repository == nil {
		return nil, ErrUnavailable
	}
	if config.Now == nil {
		config.Now = func() time.Time { return time.Now().UTC() }
	}
	if config.NewID == nil {
		config.NewID = uuid.NewString
	}
	return &Service{config: config}, nil
}
func (s *Service) now() time.Time { return s.config.Now().UTC() }
func validScope(scope Scope) bool {
	return strings.TrimSpace(scope.WorkspaceID) != "" && strings.TrimSpace(scope.ActorID) != ""
}
func validRequestID(id string) bool {
	return len(id) > 0 && len(id) <= 128 && !strings.ContainsAny(id, "\r\n\x00")
}
func hash(value any) string {
	data, _ := json.Marshal(value)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func RequestHash(value any) string { return hash(value) }
func mutable(d Draft) bool         { return d.Status != "committed" && d.Status != "discarded" }
func (s *Service) Capabilities(ctx context.Context, scope Scope) (Capabilities, error) {
	c := Capabilities{MaxAudioBytes: MaxAudioBytes, MaxDurationSeconds: MaxDurationSeconds}
	if !validScope(scope) {
		return c, ErrInvalid
	}
	if err := s.config.Repository.Read(ctx, scope, func(r Reader) error { _, err := r.ListDrafts(ctx); return err }); err != nil {
		return c, err
	}
	c.GenerationAvailable = s.config.Chat != nil
	if c.GenerationAvailable && s.config.ResolveGeneration != nil {
		c.GenerationAvailable = s.config.ResolveGeneration(ctx, scope.WorkspaceID) == nil
	}
	if c.GenerationAvailable && s.config.Features != nil {
		enabled, _, err := s.config.Features.ResolveFeature(ctx, scope.WorkspaceID, "plan_generation")
		c.GenerationAvailable = err == nil && enabled
	}
	c.TranscriptionAvailable = s.config.Transcriber != nil && s.config.Objects != nil
	if c.TranscriptionAvailable && s.config.ResolveTranscription != nil {
		c.TranscriptionAvailable = s.config.ResolveTranscription(ctx, scope.WorkspaceID) == nil
	}
	c.Available = c.GenerationAvailable
	if !c.GenerationAvailable {
		c.Reason = "请先在用户设置中启用文本 AI 服务和计划生成功能。"
	} else if !c.TranscriptionAvailable {
		c.Reason = "语音转写暂不可用，可以输入文字生成计划。"
	}
	return c, nil
}
func (s *Service) Create(ctx context.Context, scope Scope, req CreateRequest) (Draft, error) {
	if !validScope(scope) || !validRequestID(req.RequestID) || utf8.RuneCountInString(req.InputText) > 20000 {
		return Draft{}, ErrInvalid
	}
	if _, err := time.LoadLocation(req.Timezone); err != nil {
		return Draft{}, fmt.Errorf("%w: timezone", ErrInvalid)
	}
	var result Draft
	err := s.config.Repository.Write(ctx, scope, func(tx Tx) error {
		found, err := tx.FindDraftByRequestID(ctx, req.RequestID)
		if err == nil {
			if found.RequestHash != hash(req) {
				return ErrConflict
			}
			result = found
			return nil
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		now := s.now()
		result = Draft{ID: s.config.NewID(), WorkspaceID: scope.WorkspaceID, OwnerID: scope.ActorID, RequestID: req.RequestID, RequestHash: hash(req), Revision: 1, Status: "input", InputText: strings.TrimSpace(req.InputText), Timezone: req.Timezone, ReferenceTime: now, ProjectID: req.ProjectID, Items: []Item{}, Clarifications: []string{}, Assumptions: []string{}, CreatedAt: now, UpdatedAt: now}
		return tx.PutDraft(ctx, result, 0)
	})
	return result, err
}
func hydrate(ctx context.Context, r Reader, d *Draft) error {
	if d.LatestJob != nil {
		j, err := r.GetJob(ctx, d.LatestJob.ID)
		if err != nil {
			return err
		}
		d.LatestJob = &j
	}
	if d.Status == "committed" {
		receipt, err := r.GetCommitByDraft(ctx, d.ID)
		if err != nil {
			return err
		}
		d.Receipt = &receipt
	}
	return nil
}
func (s *Service) Get(ctx context.Context, scope Scope, id string) (Draft, error) {
	if !validScope(scope) {
		return Draft{}, ErrInvalid
	}
	var d Draft
	err := s.config.Repository.Read(ctx, scope, func(r Reader) error {
		var err error
		d, err = r.GetDraft(ctx, id)
		if err != nil {
			return err
		}
		return hydrate(ctx, r, &d)
	})
	return d, err
}
func (s *Service) List(ctx context.Context, scope Scope) ([]Draft, error) {
	if !validScope(scope) {
		return nil, ErrInvalid
	}
	var ds []Draft
	err := s.config.Repository.Read(ctx, scope, func(r Reader) error {
		var err error
		ds, err = r.ListDrafts(ctx)
		if err != nil {
			return err
		}
		for i := range ds {
			if err := hydrate(ctx, r, &ds[i]); err != nil {
				return err
			}
		}
		return nil
	})
	if ds == nil {
		ds = []Draft{}
	}
	return ds, err
}
func invalidateJob(ctx context.Context, tx Tx, d *Draft, now time.Time) error {
	if d.LatestJob == nil {
		return nil
	}
	j, err := tx.GetJob(ctx, d.LatestJob.ID)
	if err != nil {
		return err
	}
	if j.State == "queued" || j.State == "processing" {
		old := j.Revision
		j.Revision++
		j.State = "stale"
		j.ErrorCode = "input_changed"
		j.Retryable = false
		j.LeaseToken = ""
		j.LeaseUntil = time.Time{}
		j.UpdatedAt = now
		if err := tx.PutJob(ctx, j, old); err != nil {
			return err
		}
		d.LatestJob = &j
	}
	return nil
}
func (s *Service) Patch(ctx context.Context, scope Scope, id string, req PatchRequest) (Draft, error) {
	if !validScope(scope) || req.ExpectedRevision < 1 {
		return Draft{}, ErrInvalid
	}
	if req.InputText != nil && utf8.RuneCountInString(*req.InputText) > 20000 {
		return Draft{}, ErrInvalid
	}
	if req.Title != nil && utf8.RuneCountInString(*req.Title) > 200 {
		return Draft{}, ErrInvalid
	}
	if req.Items != nil {
		if err := validateItemShape(*req.Items, false); err != nil {
			return Draft{}, err
		}
	}
	var d Draft
	err := s.config.Repository.Write(ctx, scope, func(tx Tx) error {
		var err error
		d, err = tx.GetDraft(ctx, id)
		if err != nil {
			return err
		}
		if d.Revision != req.ExpectedRevision || !mutable(d) {
			return ErrConflict
		}
		if err := invalidateJob(ctx, tx, &d, s.now()); err != nil {
			return err
		}
		if req.InputText != nil && d.InputText != strings.TrimSpace(*req.InputText) {
			d.InputText = strings.TrimSpace(*req.InputText)
			d.Items = []Item{}
			d.Title = ""
			d.Clarifications = []string{}
			d.Assumptions = []string{}
		}
		if req.ProjectID != nil {
			d.ProjectID = strings.TrimSpace(*req.ProjectID)
		}
		if req.Title != nil {
			d.Title = strings.TrimSpace(*req.Title)
		}
		if req.Items != nil {
			d.Items = *req.Items
		}
		old := d.Revision
		d.Revision++
		d.UpdatedAt = s.now()
		d.Status = "input"
		if len(d.Items) > 0 {
			d.Status = "ready"
		}
		return tx.PutDraft(ctx, d, old)
	})
	return d, err
}
func validateItemShape(items []Item, strict bool) error {
	if len(items) > MaxItems {
		return fmt.Errorf("%w: maximum %d tasks", ErrInvalid, MaxItems)
	}
	seen := map[string]bool{}
	for _, item := range items {
		if item.ID == "" || len(item.ID) > 100 || seen[item.ID] || utf8.RuneCountInString(item.Title) > 300 || utf8.RuneCountInString(item.Description) > 10000 || item.Priority < 0 || item.Priority > 3 {
			return ErrInvalid
		}
		seen[item.ID] = true
		if strict {
			if strings.TrimSpace(item.Title) == "" {
				return ErrInvalid
			}
			if _, err := taskdomain.NormalizeSchedule(item.Schedule.Input()); err != nil {
				return fmt.Errorf("%w: %v", ErrInvalid, err)
			}
			if item.DueAt != nil && item.Schedule.RecurrenceType != taskdomain.RecurrenceNone {
				return fmt.Errorf("%w: recurring deadline is unsupported", ErrInvalid)
			}
		}
	}
	return nil
}
func (s *Service) Upload(ctx context.Context, scope Scope, id string, expected int64, contentType string, body io.Reader) (Draft, error) {
	if !validScope(scope) || expected < 1 || s.config.Objects == nil {
		return Draft{}, ErrInvalid
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return Draft{}, ErrInvalid
	}
	extensions := map[string]string{"audio/webm": ".webm", "audio/mp4": ".m4a", "audio/m4a": ".m4a", "audio/x-m4a": ".m4a", "audio/aac": ".aac", "audio/mpeg": ".mp3", "audio/wav": ".wav", "audio/x-wav": ".wav", "audio/ogg": ".ogg"}
	ext, ok := extensions[mediaType]
	if !ok {
		return Draft{}, fmt.Errorf("%w: unsupported audio format", ErrInvalid)
	}
	audio, err := io.ReadAll(io.LimitReader(body, MaxAudioBytes+1))
	if err != nil {
		return Draft{}, err
	}
	if len(audio) == 0 || int64(len(audio)) > MaxAudioBytes {
		return Draft{}, fmt.Errorf("%w: audio size", ErrInvalid)
	}
	sum := sha256.Sum256(audio)
	digest := hex.EncodeToString(sum[:])
	var d Draft
	err = s.config.Repository.Write(ctx, scope, func(tx Tx) error {
		var e error
		d, e = tx.GetDraft(ctx, id)
		if e != nil {
			return e
		}
		if !mutable(d) {
			return ErrConflict
		}
		if d.AudioDigest == digest && d.AudioObjectKey != "" {
			return nil
		}
		if d.Revision != expected {
			return ErrConflict
		}
		if e := invalidateJob(ctx, tx, &d, s.now()); e != nil {
			return e
		}
		if d.AudioObjectKey != "" {
			d.CleanupObjectKeys = append(d.CleanupObjectKeys, d.AudioObjectKey)
		}
		expires := s.now().Add(7 * 24 * time.Hour)
		d.AudioObjectKey = "plans/" + scope.WorkspaceID + "/" + d.ID + "/" + s.config.NewID() + ext
		d.AudioMIME = mediaType
		d.AudioFilename = "recording" + ext
		d.AudioSize = int64(len(audio))
		d.AudioDigest = digest
		d.AudioExpiresAt = &expires
		d.AudioAvailable = false
		d.AudioPending = true
		d.AudioCleanupAfter = s.now().Add(5 * time.Minute)
		d.Transcript = ""
		d.InputText = ""
		d.Items = []Item{}
		d.Title = ""
		d.Clarifications = []string{}
		d.Assumptions = []string{}
		d.Status = "input"
		old := d.Revision
		d.Revision++
		d.UpdatedAt = s.now()
		return tx.PutDraft(ctx, d, old)
	})
	if err != nil {
		return Draft{}, err
	}
	if d.AudioAvailable {
		return d, nil
	}
	objectCtx := auth.ContextWithWorkspaceScope(ctx, scope.WorkspaceID)
	putCtx, stopPut := context.WithTimeout(objectCtx, 2*time.Minute)
	defer stopPut()
	if err := s.config.Objects.Put(putCtx, d.AudioObjectKey, bytes.NewReader(audio), d.AudioSize, d.AudioMIME); err != nil {
		return Draft{}, err
	}
	reserved := d
	err = s.config.Repository.Write(ctx, scope, func(tx Tx) error {
		current, e := tx.GetDraft(ctx, id)
		if e != nil {
			return e
		}
		if !mutable(current) || current.AudioObjectKey != reserved.AudioObjectKey || current.AudioDigest != digest {
			return ErrConflict
		}
		// Another identical upload may have already confirmed the object.
		if current.AudioAvailable {
			d = current
			return nil
		}
		if current.Revision != reserved.Revision {
			return ErrConflict
		}
		old := current.Revision
		current.Revision++
		current.AudioAvailable = true
		current.AudioPending = false
		current.UpdatedAt = s.now()
		if e := tx.PutDraft(ctx, current, old); e != nil {
			return e
		}
		d = current
		return nil
	})
	if err != nil {
		_ = s.config.Objects.Remove(objectCtx, reserved.AudioObjectKey)
	}
	return d, err
}
func (s *Service) Audio(ctx context.Context, scope Scope, id string) (*objectstore.Object, error) {
	d, err := s.Get(ctx, scope, id)
	if err != nil {
		return nil, err
	}
	if !d.AudioAvailable || d.AudioObjectKey == "" || d.AudioExpiresAt != nil && !s.now().Before(*d.AudioExpiresAt) {
		return nil, ErrNotFound
	}
	return s.config.Objects.Get(auth.ContextWithWorkspaceScope(ctx, scope.WorkspaceID), d.AudioObjectKey)
}
func (s *Service) Start(ctx context.Context, scope Scope, id, stage string, req StartRequest) (JobResult, error) {
	if !validScope(scope) || !validRequestID(req.RequestID) || req.ExpectedRevision < 1 || (stage != "transcribe" && stage != "generate") {
		return JobResult{}, ErrInvalid
	}
	requestHash := hash(struct {
		ID, Stage, RetryOf string
		Request            StartRequest
	}{id, stage, req.RetryOf, req})
	var replay JobResult
	replayFound := false
	err := s.config.Repository.Read(ctx, scope, func(r Reader) error {
		found, e := r.FindJobByRequestID(ctx, req.RequestID)
		if errors.Is(e, ErrNotFound) {
			return nil
		}
		if e != nil {
			return e
		}
		if found.RequestHash != requestHash {
			return ErrConflict
		}
		d, e := r.GetDraft(ctx, found.DraftID)
		if e != nil {
			return e
		}
		replay = JobResult{Draft: d, Job: found}
		replayFound = true
		return nil
	})
	if err != nil {
		return JobResult{}, err
	}
	if replayFound {
		return replay, nil
	}
	caps, err := s.Capabilities(ctx, scope)
	if err != nil {
		return JobResult{}, err
	}
	if stage == "transcribe" && !caps.TranscriptionAvailable || stage == "generate" && !caps.GenerationAvailable {
		return JobResult{}, ErrUnavailable
	}
	var result JobResult
	err = s.config.Repository.Write(ctx, scope, func(tx Tx) error {
		found, e := tx.FindJobByRequestID(ctx, req.RequestID)
		if e == nil {
			if found.RequestHash != requestHash {
				return ErrConflict
			}
			d, e := tx.GetDraft(ctx, found.DraftID)
			result = JobResult{Draft: d, Job: found}
			return e
		}
		if !errors.Is(e, ErrNotFound) {
			return e
		}
		d, e := tx.GetDraft(ctx, id)
		if e != nil {
			return e
		}
		if !mutable(d) || d.Revision != req.ExpectedRevision {
			return ErrConflict
		}
		if stage == "transcribe" && (!d.AudioAvailable || d.AudioObjectKey == "" || d.AudioExpiresAt != nil && !s.now().Before(*d.AudioExpiresAt)) {
			return fmt.Errorf("%w: audio not available", ErrInvalid)
		}
		if stage == "generate" && strings.TrimSpace(d.InputText) == "" {
			return fmt.Errorf("%w: input text is empty", ErrInvalid)
		}
		if e := invalidateJob(ctx, tx, &d, s.now()); e != nil {
			return e
		}
		old := d.Revision
		d.Revision++
		d.Status = map[string]string{"transcribe": "transcribing", "generate": "generating"}[stage]
		d.UpdatedAt = s.now()
		j := Job{ID: s.config.NewID(), WorkspaceID: scope.WorkspaceID, OwnerID: scope.ActorID, DraftID: id, RequestID: req.RequestID, RequestHash: requestHash, RetryOf: req.RetryOf, Revision: 1, InputRevision: d.Revision, Stage: stage, State: "queued", Retryable: false, CreatedAt: s.now(), UpdatedAt: s.now()}
		d.LatestJob = &j
		if e := tx.PutJob(ctx, j, 0); e != nil {
			return e
		}
		if e := tx.PutDraft(ctx, d, old); e != nil {
			return e
		}
		result = JobResult{Draft: d, Job: j}
		return nil
	})
	return result, err
}
func (s *Service) Job(ctx context.Context, scope Scope, id string) (Job, error) {
	if !validScope(scope) {
		return Job{}, ErrInvalid
	}
	var j Job
	err := s.config.Repository.Read(ctx, scope, func(r Reader) error { var e error; j, e = r.GetJob(ctx, id); return e })
	return j, err
}
func (s *Service) Retry(ctx context.Context, scope Scope, id, requestID string) (JobResult, error) {
	if !validScope(scope) || !validRequestID(requestID) {
		return JobResult{}, ErrInvalid
	}
	var replay JobResult
	replayFound := false
	err := s.config.Repository.Read(ctx, scope, func(r Reader) error {
		found, e := r.FindJobByRequestID(ctx, requestID)
		if errors.Is(e, ErrNotFound) {
			return nil
		}
		if e != nil {
			return e
		}
		if found.RetryOf != id {
			return ErrConflict
		}
		d, e := r.GetDraft(ctx, found.DraftID)
		if e != nil {
			return e
		}
		replay = JobResult{Draft: d, Job: found}
		replayFound = true
		return nil
	})
	if err != nil {
		return JobResult{}, err
	}
	if replayFound {
		return replay, nil
	}
	j, err := s.Job(ctx, scope, id)
	if err != nil {
		return JobResult{}, err
	}
	if j.State != "failed" || !j.Retryable {
		return JobResult{}, ErrConflict
	}
	d, err := s.Get(ctx, scope, j.DraftID)
	if err != nil {
		return JobResult{}, err
	}
	if d.LatestJob == nil || d.LatestJob.ID != j.ID || d.Revision != j.InputRevision+1 || !mutable(d) {
		return JobResult{}, ErrConflict
	}
	return s.Start(ctx, scope, d.ID, j.Stage, StartRequest{ExpectedRevision: d.Revision, RequestID: requestID, RetryOf: id})
}

func (s *Service) Cancel(ctx context.Context, scope Scope, id string) (JobResult, error) {
	if !validScope(scope) {
		return JobResult{}, ErrInvalid
	}
	var result JobResult
	err := s.config.Repository.Write(ctx, scope, func(tx Tx) error {
		j, e := tx.GetJob(ctx, id)
		if e != nil {
			return e
		}
		d, e := tx.GetDraft(ctx, j.DraftID)
		if e != nil {
			return e
		}
		if j.State == "queued" || j.State == "processing" {
			old := j.Revision
			j.Revision++
			j.State = "cancelled"
			j.Retryable = false
			j.LeaseToken = ""
			j.LeaseUntil = time.Time{}
			j.UpdatedAt = s.now()
			if e := tx.PutJob(ctx, j, old); e != nil {
				return e
			}
			if mutable(d) && d.LatestJob != nil && d.LatestJob.ID == j.ID {
				old := d.Revision
				d.Revision++
				d.Status = "input"
				if len(d.Items) > 0 {
					d.Status = "ready"
				}
				d.LatestJob = &j
				d.UpdatedAt = s.now()
				if e := tx.PutDraft(ctx, d, old); e != nil {
					return e
				}
			}
		}
		result = JobResult{Draft: d, Job: j}
		return nil
	})
	return result, err
}
func (s *Service) Validate(ctx context.Context, scope Scope, id string, expected int64) (Validation, error) {
	if !validScope(scope) {
		return Validation{}, ErrInvalid
	}
	var result Validation
	err := s.config.Repository.Read(ctx, scope, func(r Reader) error {
		d, e := r.GetDraft(ctx, id)
		if e != nil {
			return e
		}
		if d.Revision != expected || !mutable(d) {
			return ErrConflict
		}
		result, e = r.ValidatePlan(ctx, d)
		return e
	})
	return result, err
}
func (s *Service) Commit(ctx context.Context, scope Scope, id string, req CommitRequest) (Receipt, error) {
	if !validScope(scope) || !validRequestID(req.RequestID) || req.ExpectedRevision < 1 {
		return Receipt{}, ErrInvalid
	}
	req.DraftID = id
	return s.config.Repository.Commit(ctx, scope, req)
}
func (s *Service) Discard(ctx context.Context, scope Scope, id string) error {
	if !validScope(scope) {
		return ErrInvalid
	}
	return s.config.Repository.Write(ctx, scope, func(tx Tx) error {
		d, e := tx.GetDraft(ctx, id)
		if e != nil {
			return e
		}
		if d.Status == "committed" {
			return ErrConflict
		}
		if d.Status == "discarded" {
			return nil
		}
		if e := invalidateJob(ctx, tx, &d, s.now()); e != nil {
			return e
		}
		old := d.Revision
		d.Revision++
		d.Status = "discarded"
		d.UpdatedAt = s.now()
		return tx.PutDraft(ctx, d, old)
	})
}
