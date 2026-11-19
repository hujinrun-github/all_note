// Package webplanning owns temporary speech/text input and reviewable plans.
// Confirmed actions are committed through the existing v2 task domain.
package webplanning

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/hujinrun/flowspace/internal/taskdomain"
)

var (
	ErrNotFound    = errors.New("planning item not found")
	ErrConflict    = errors.New("planning revision or request conflict")
	ErrInvalid     = errors.New("invalid planning input")
	ErrUnavailable = errors.New("planning service unavailable")
	ErrNoJob       = errors.New("no eligible planning job")
)

const (
	MaxAudioBytes      int64 = 20 * 1024 * 1024
	MaxDurationSeconds       = 180
	MaxItems                 = 20
)

// An empty ActorID is reserved for the trusted worker's job-discovery scan.
// HTTP entry points always supply an authenticated, nonempty ActorID.
type Scope struct{ WorkspaceID, ActorID string }

type Schedule struct {
	RecurrenceType  taskdomain.RecurrenceType  `json:"recurrence_type"`
	TimingType      taskdomain.TimingType      `json:"timing_type"`
	Timezone        string                     `json:"timezone"`
	StartsOn        string                     `json:"starts_on,omitempty"`
	EndsOn          string                     `json:"ends_on,omitempty"`
	LocalStartTime  string                     `json:"local_start_time,omitempty"`
	DurationMinutes int                        `json:"duration_minutes,omitempty"`
	Rule            *taskdomain.RecurrenceRule `json:"rule,omitempty"`
}

func (s Schedule) Input() taskdomain.ScheduleInput {
	var rule json.RawMessage
	if s.Rule != nil {
		rule, _ = json.Marshal(s.Rule)
	}
	return taskdomain.ScheduleInput{RecurrenceType: s.RecurrenceType, TimingType: s.TimingType, Timezone: s.Timezone, StartsOn: s.StartsOn, EndsOn: s.EndsOn, LocalStartTime: s.LocalStartTime, DurationMinutes: s.DurationMinutes, Rule: rule}
}

type Item struct {
	ID          string     `json:"id"`
	Selected    bool       `json:"selected"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Priority    int        `json:"priority"`
	Schedule    Schedule   `json:"schedule"`
	DueAt       *time.Time `json:"due_at,omitempty"`
}
type Draft struct {
	ID                string     `json:"id"`
	WorkspaceID       string     `json:"-"`
	OwnerID           string     `json:"-"`
	RequestID         string     `json:"-"`
	RequestHash       string     `json:"-"`
	Revision          int64      `json:"revision"`
	Status            string     `json:"status"`
	InputText         string     `json:"input_text"`
	Transcript        string     `json:"transcript"`
	Timezone          string     `json:"timezone"`
	ReferenceTime     time.Time  `json:"reference_time"`
	ProjectID         string     `json:"project_id"`
	Title             string     `json:"title"`
	Items             []Item     `json:"items"`
	Clarifications    []string   `json:"clarifications"`
	Assumptions       []string   `json:"assumptions"`
	LatestJob         *Job       `json:"latest_job,omitempty"`
	Receipt           *Receipt   `json:"receipt,omitempty"`
	AudioCleanupAfter time.Time  `json:"-"`
	AudioPending      bool       `json:"-"`
	CleanupObjectKeys []string   `json:"-"`
	AudioAvailable    bool       `json:"audio_available"`
	AudioObjectKey    string     `json:"-"`
	AudioMIME         string     `json:"-"`
	AudioFilename     string     `json:"-"`
	AudioDigest       string     `json:"-"`
	AudioSize         int64      `json:"-"`
	AudioExpiresAt    *time.Time `json:"-"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}
type Job struct {
	ID            string    `json:"id"`
	DraftID       string    `json:"draft_id"`
	WorkspaceID   string    `json:"-"`
	OwnerID       string    `json:"-"`
	RequestID     string    `json:"-"`
	RequestHash   string    `json:"-"`
	Revision      int64     `json:"-"`
	InputRevision int64     `json:"-"`
	Stage         string    `json:"stage"`
	State         string    `json:"state"`
	ErrorCode     string    `json:"error_code,omitempty"`
	Retryable     bool      `json:"retryable"`
	Attempt       int       `json:"-"`
	LeaseToken    string    `json:"-"`
	LeaseUntil    time.Time `json:"-"`
	CreatedAt     time.Time `json:"-"`
	UpdatedAt     time.Time `json:"-"`
	RetryOf       string    `json:"-"`
}
type Receipt struct {
	DraftID       string    `json:"draft_id"`
	ProjectID     string    `json:"project_id"`
	TaskIDs       []string  `json:"task_ids"`
	OccurrenceIDs []string  `json:"occurrence_ids"`
	RequestID     string    `json:"-"`
	RequestHash   string    `json:"-"`
	CreatedAt     time.Time `json:"-"`
}
type Validation struct {
	DraftRevision int64    `json:"draft_revision"`
	Issues        []string `json:"issues"`
	Conflicts     []string `json:"conflicts"`
}
type NewProjectInput struct {
	Name    string                    `json:"name"`
	Kind    taskdomain.ProjectKind    `json:"kind"`
	Horizon taskdomain.ProjectHorizon `json:"horizon"`
}
type CommitRequest struct {
	DraftID          string           `json:"-"`
	ExpectedRevision int64            `json:"expected_revision"`
	RequestID        string           `json:"request_id"`
	Publish          bool             `json:"publish"`
	AllowConflicts   bool             `json:"allow_conflicts"`
	NewProject       *NewProjectInput `json:"new_project,omitempty"`
}
type Reader interface {
	GetDraft(context.Context, string) (Draft, error)
	FindDraftByRequestID(context.Context, string) (Draft, error)
	ListDrafts(context.Context) ([]Draft, error)
	GetJob(context.Context, string) (Job, error)
	FindJobByRequestID(context.Context, string) (Job, error)
	ListEligibleJobs(context.Context, time.Time, int) ([]Job, error)
	GetCommitByDraft(context.Context, string) (Receipt, error)
	GetCommitByKey(context.Context, string) (Receipt, error)
	ValidatePlan(context.Context, Draft) (Validation, error)
}
type Tx interface {
	Reader
	// expectedRevision=0 inserts; otherwise compare-and-swap. Callers assign the
	// new record's Revision=expectedRevision+1. Implementations never upsert past CAS.
	PutDraft(context.Context, Draft, int64) error
	PutJob(context.Context, Job, int64) error
	// CommitPlan checks the receipt before mutable revisions and writes tasks,
	// publication, receipt, draft state and mobile-v2 changes in this transaction.
	CommitPlan(context.Context, CommitRequest) (Receipt, error)
}
type Repository interface {
	Read(context.Context, Scope, func(Reader) error) error
	Write(context.Context, Scope, func(Tx) error) error
	Commit(context.Context, Scope, CommitRequest) (Receipt, error)
}
type WorkspaceLister interface {
	ListActiveWorkspaceIDs(context.Context) ([]string, error)
}
type Chat interface {
	Generate(context.Context, string, string, string) (string, error)
}
type FeatureResolver interface {
	ResolveFeature(context.Context, string, string) (bool, string, error)
}
type Capabilities struct {
	Available              bool   `json:"available"`
	TranscriptionAvailable bool   `json:"transcription_available"`
	GenerationAvailable    bool   `json:"generation_available"`
	MaxAudioBytes          int64  `json:"max_audio_bytes"`
	MaxDurationSeconds     int    `json:"max_duration_seconds"`
	Reason                 string `json:"reason,omitempty"`
}
type CreateRequest struct {
	RequestID string `json:"request_id"`
	InputText string `json:"input_text"`
	Timezone  string `json:"timezone"`
	ProjectID string `json:"project_id"`
}
type PatchRequest struct {
	ExpectedRevision int64   `json:"expected_revision"`
	InputText        *string `json:"input_text,omitempty"`
	ProjectID        *string `json:"project_id,omitempty"`
	Title            *string `json:"title,omitempty"`
	Items            *[]Item `json:"items,omitempty"`
}
type StartRequest struct {
	ExpectedRevision int64  `json:"expected_revision"`
	RequestID        string `json:"request_id"`
	RetryOf          string `json:"-"`
}
type JobResult struct {
	Draft Draft `json:"draft"`
	Job   Job   `json:"job"`
}
