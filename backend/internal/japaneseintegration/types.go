// Package japaneseintegration connects authoritative Japanese study task states
// to explicitly linked FlowSpace v2 occurrences.
package japaneseintegration

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/hujinrun/flowspace/internal/credentials"
	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/taskapp"
)

var (
	ErrInvalid      = errors.New("invalid japanese integration request")
	ErrUnauthorized = errors.New("japanese integration unauthorized")
	ErrConflict     = errors.New("japanese integration conflict")
	ErrNotFound     = errors.New("japanese integration not found")
	ErrUnavailable  = errors.New("japanese integration unavailable")
)

// RuntimeSnapshot is intentionally declared at the integration boundary so HTTP
// handlers do not depend on provider-aware taskruntime composition.
type RuntimeSnapshot struct {
	WorkspaceID string
	Epoch       int64
	Driver      storage.Driver
	DB          *sql.DB
	Writer      storage.TenantFencedWriter
	Application taskapp.RuntimeSnapshot
}
type MobileRuntimeResolverFunc func(context.Context, string) (RuntimeSnapshot, error)

func (f MobileRuntimeResolverFunc) ResolveMobileRuntime(ctx context.Context, workspace string) (RuntimeSnapshot, error) {
	return f(ctx, workspace)
}

type MobileRuntimeResolver interface {
	ResolveMobileRuntime(context.Context, string) (RuntimeSnapshot, error)
}
type Config struct {
	ControlDB     *sql.DB
	ControlDriver storage.Driver
	Keyring       *credentials.Keyring
	Runtimes      MobileRuntimeResolver
	Authorize     func(context.Context, string, string) error
	Now           func() time.Time
}
type Service struct{ cfg Config }

type PairingCode struct {
	PairingCode string `json:"pairing_code"`
	ExpiresAt   string `json:"expires_at"`
}
type PairRequest struct {
	PairingCode      string `json:"pairing_code"`
	SourceInstanceID string `json:"source_instance_id"`
	SourceUserID     string `json:"source_user_id"`
	SourceUserName   string `json:"source_user_name,omitempty"`
}
type PairResult struct {
	ConnectionID  string `json:"connection_id"`
	KeyID         string `json:"key_id"`
	Secret        string `json:"secret"`
	WorkspaceID   string `json:"workspace_id"`
	WorkspaceName string `json:"workspace_name,omitempty"`
}
type Connection struct {
	ID               string `json:"connection_id"`
	WorkspaceID      string `json:"workspace_id"`
	ActorID          string `json:"actor_id"`
	SourceInstanceID string `json:"source_instance_id"`
	SourceUserID     string `json:"source_user_id"`
	SourceUserName   string `json:"source_user_name"`
	KeyID            string `json:"key_id"`
	State            string `json:"state"`
	CreatedAt        string `json:"created_at"`
}
type Catalog struct {
	SchemaVersion    int    `json:"schema_version"`
	SourceInstanceID string `json:"source_instance_id"`
	SourceUserID     string `json:"source_user_id"`
	CatalogRevision  int64  `json:"catalog_revision"`
	Plan             *Plan  `json:"plan"`
}
type Plan struct {
	PlanID    string `json:"plan_id"`
	Revision  int64  `json:"revision"`
	Title     string `json:"title"`
	Timezone  string `json:"timezone"`
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
	Weekdays  []int  `json:"weekdays"`
	TimeSlots []Slot `json:"time_slots"`
}
type Slot struct {
	ID              string      `json:"id"`
	Title           string      `json:"title"`
	Time            string      `json:"time"`
	DurationMinutes int         `json:"duration_minutes"`
	Tasks           []StudyTask `json:"tasks"`
}
type StudyTask struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Path        string `json:"path"`
	Description string `json:"description,omitempty"`
	Minutes     int    `json:"minutes"`
	Weekdays    []int  `json:"weekdays,omitempty"`
}
type Event struct {
	SchemaVersion    int    `json:"schema_version"`
	EventID          string `json:"event_id"`
	Type             string `json:"type"`
	SourceInstanceID string `json:"source_instance_id"`
	SourceUserID     string `json:"source_user_id"`
	PlanID           string `json:"plan_id"`
	PlanRevision     int64  `json:"plan_revision"`
	CatalogRevision  int64  `json:"catalog_revision"`
	SlotID           string `json:"slot_id"`
	TaskID           string `json:"task_id"`
	StudyDate        string `json:"study_date"`
	Timezone         string `json:"timezone"`
	StateRevision    int64  `json:"state_revision"`
	Completed        bool   `json:"completed"`
	OccurredAt       string `json:"occurred_at"`
	Reason           string `json:"reason"`
}
type BindingInput struct {
	ConnectionID  string `json:"connection_id"`
	PlanID        string `json:"plan_id"`
	SlotID        string `json:"slot_id"`
	TaskID        string `json:"task_id"`
	TargetTaskID  string `json:"target_task_id"`
	EffectiveFrom string `json:"effective_from,omitempty"`
	Enabled       *bool  `json:"enabled,omitempty"`
}
type Binding struct {
	ID               string `json:"id"`
	ConnectionID     string `json:"connection_id"`
	PlanID           string `json:"plan_id"`
	SlotID           string `json:"slot_id"`
	TaskID           string `json:"task_id"`
	TargetTaskID     string `json:"target_task_id"`
	TargetTitle      string `json:"target_title"`
	Version          int64  `json:"binding_version"`
	EffectiveFrom    string `json:"effective_from"`
	EffectiveTo      string `json:"effective_to,omitempty"`
	ScheduleRevision int64  `json:"schedule_revision"`
	Fingerprint      string `json:"source_fingerprint"`
	Enabled          bool   `json:"enabled"`
}
type Receipt struct {
	EventID          string `json:"event_id"`
	ConnectionID     string `json:"connection_id"`
	Status           string `json:"status"`
	ErrorCode        string `json:"error_code,omitempty"`
	StudyDate        string `json:"study_date"`
	SourceTaskID     string `json:"source_task_id"`
	OccurrenceID     string `json:"occurrence_id,omitempty"`
	TargetTaskID     string `json:"target_task_id,omitempty"`
	BindingVersion   int64  `json:"binding_version,omitempty"`
	Completed        bool   `json:"completed"`
	SourceOccurredAt string `json:"source_occurred_at"`
	ProcessedAt      string `json:"processed_at,omitempty"`
}
type CatalogEntry struct {
	ConnectionID string  `json:"connection_id"`
	Catalog      Catalog `json:"catalog"`
}
type Target struct {
	TaskID         string `json:"task_id"`
	Title          string `json:"title"`
	Timezone       string `json:"timezone"`
	RecurrenceType string `json:"recurrence_type"`
}
type Status struct {
	Connections []Connection   `json:"connections"`
	Catalogs    []CatalogEntry `json:"catalogs"`
	Bindings    []Binding      `json:"bindings"`
	Events      []Receipt      `json:"events"`
	Targets     []Target       `json:"targets"`
}
