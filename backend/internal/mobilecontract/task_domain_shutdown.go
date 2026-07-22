package mobilecontract

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

const TaskDomainUpgradeRequiredCode = "mobile_task_domain_upgrade_required"

var (
	ErrInvalidTaskDomainShutdownInput = errors.New("invalid mobile task-domain shutdown input")
	ErrTaskDomainShutdownIncomplete   = errors.New("mobile task-domain shutdown is incomplete")
	ErrTaskDomainUpgradeRequired      = errors.New("mobile task-domain upgrade required")
)

// MobileEntity is the stable entity name used by the mobile-v1 contract gate.
type MobileEntity string

const (
	MobileEntityEvent               MobileEntity = "event"
	MobileEntityProject             MobileEntity = "project"
	MobileEntityTask                MobileEntity = "task"
	MobileEntityTaskOccurrence      MobileEntity = "task_occurrence"
	MobileEntityTaskRecurrenceRule  MobileEntity = "task_recurrence_rule"
	MobileEntityWatchTaskProjection MobileEntity = "watch_task_projection"

	// These entities are intentionally outside the task-domain shutdown. They
	// remain available while mobile-v1 task-domain reads and writes are closed.
	MobileEntityNote       MobileEntity = "note"
	MobileEntityVoiceNote  MobileEntity = "voice_note"
	MobileEntityVoiceAudio MobileEntity = "voice_audio"
)

// MobileOperationScope names the four mobile-v1 surfaces which must be closed
// before a workspace can cut over to the v2 task domain.
type MobileOperationScope string

const (
	MobileScopeChanges  MobileOperationScope = "changes"
	MobileScopeMutation MobileOperationScope = "mutation"
	MobileScopeSnapshot MobileOperationScope = "snapshot"
	MobileScopeWatch    MobileOperationScope = "watch"
)

// MobileBindingKind identifies the client artifact whose contract generation
// is being checked. A generation bump invalidates all three artifact classes.
type MobileBindingKind string

const (
	MobileBindingCursor  MobileBindingKind = "cursor"
	MobileBindingSession MobileBindingKind = "session"
	MobileBindingToken   MobileBindingKind = "token"
)

var affectedTaskDomainEntities = []MobileEntity{
	MobileEntityEvent,
	MobileEntityProject,
	MobileEntityTask,
	MobileEntityTaskOccurrence,
	MobileEntityTaskRecurrenceRule,
	MobileEntityWatchTaskProjection,
}

var requiredTaskDomainShutdownScopes = []MobileOperationScope{
	MobileScopeChanges,
	MobileScopeMutation,
	MobileScopeSnapshot,
	MobileScopeWatch,
}

// AffectedTaskDomainEntities returns a deterministic copy of the task-domain
// entity inventory. The returned slice is safe for callers to mutate.
func AffectedTaskDomainEntities() []MobileEntity {
	return append([]MobileEntity(nil), affectedTaskDomainEntities...)
}

// RequiredTaskDomainShutdownScopes returns all scopes which a cutover
// coordinator must prove closed. The order is deterministic.
func RequiredTaskDomainShutdownScopes() []MobileOperationScope {
	return append([]MobileOperationScope(nil), requiredTaskDomainShutdownScopes...)
}

// MobileContractBinding is the workspace contract generation embedded in an
// existing cursor, snapshot session, or watch/access token.
type MobileContractBinding struct {
	Kind        MobileBindingKind
	WorkspaceID string
	Generation  uint64
}

// TaskDomainMobileAccess describes an attempted mobile-v1 operation.
type TaskDomainMobileAccess struct {
	WorkspaceID string
	Entity      MobileEntity
	Scope       MobileOperationScope
	Binding     MobileContractBinding
}

// TaskDomainShutdown is an in-memory contract model. Persistence and router
// wiring are deliberately outside this package; this type only defines the
// fail-closed semantics used by those layers.
type TaskDomainShutdown struct {
	mu           sync.RWMutex
	workspaceID  string
	generation   uint64
	revision     uint64
	closedScopes map[MobileOperationScope]struct{}
}

func NewTaskDomainShutdown(workspaceID string, generation uint64) (*TaskDomainShutdown, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" || generation == 0 {
		return nil, invalidTaskDomainShutdownInput("workspace and generation are required")
	}
	return &TaskDomainShutdown{
		workspaceID:  workspaceID,
		generation:   generation,
		closedScopes: make(map[MobileOperationScope]struct{}, len(requiredTaskDomainShutdownScopes)),
	}, nil
}

func (s *TaskDomainShutdown) Revision() uint64 {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.revision
}

// CloseScope is idempotent. Only the first transition of a scope increments
// the local revision, which lets coordinators retry safely.
func (s *TaskDomainShutdown) CloseScope(scope MobileOperationScope) (bool, error) {
	if s == nil || !validTaskDomainShutdownScope(scope) {
		return false, invalidTaskDomainShutdownInput("unknown operation scope")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.closedScopes[scope]; exists {
		return false, nil
	}
	s.closedScopes[scope] = struct{}{}
	s.revision++
	return true, nil
}

// Preflight proves that every mobile-v1 task-domain surface is closed. Missing
// scopes are returned in stable lexical order through the declared inventory.
func (s *TaskDomainShutdown) Preflight() error {
	if s == nil {
		return invalidTaskDomainShutdownInput("shutdown state is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	missing := make([]MobileOperationScope, 0, len(requiredTaskDomainShutdownScopes))
	for _, scope := range requiredTaskDomainShutdownScopes {
		if _, closed := s.closedScopes[scope]; !closed {
			missing = append(missing, scope)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return &MissingTaskDomainShutdownScopesError{MissingScopes: missing}
}

// Check enforces the mobile-v1 task-domain boundary. Known non-task entities
// bypass this generation because they are independent capabilities. Unknown
// inputs are rejected rather than accidentally falling through.
func (s *TaskDomainShutdown) Check(access TaskDomainMobileAccess) error {
	if s == nil {
		return invalidTaskDomainShutdownInput("shutdown state is required")
	}
	workspaceID := strings.TrimSpace(access.WorkspaceID)
	if workspaceID == "" || !validTaskDomainShutdownScope(access.Scope) || !knownMobileEntity(access.Entity) {
		return invalidTaskDomainShutdownInput("workspace, entity, and scope must be valid")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if workspaceID != s.workspaceID {
		return invalidTaskDomainShutdownInput("workspace does not match shutdown state")
	}
	if !isTaskDomainEntity(access.Entity) {
		return nil
	}
	if !validMobileBindingKind(access.Binding.Kind) || strings.TrimSpace(access.Binding.WorkspaceID) == "" || access.Binding.Generation == 0 {
		return invalidTaskDomainShutdownInput("task-domain access requires a valid contract binding")
	}
	if access.Binding.WorkspaceID != s.workspaceID {
		return invalidTaskDomainShutdownInput("binding workspace does not match shutdown state")
	}
	if access.Binding.Generation != s.generation {
		return newTaskDomainUpgradeRequired(
			"contract_generation_mismatch",
			s.workspaceID,
			s.generation,
			access.Binding.Generation,
		)
	}
	if _, closed := s.closedScopes[access.Scope]; closed {
		return newTaskDomainUpgradeRequired(
			"task_domain_scope_closed",
			s.workspaceID,
			s.generation,
			access.Binding.Generation,
		)
	}
	return nil
}

type MissingTaskDomainShutdownScopesError struct {
	MissingScopes []MobileOperationScope
}

func (e *MissingTaskDomainShutdownScopesError) Error() string {
	if e == nil {
		return ErrTaskDomainShutdownIncomplete.Error()
	}
	return fmt.Sprintf("%s: missing scopes %v", ErrTaskDomainShutdownIncomplete, e.MissingScopes)
}

func (e *MissingTaskDomainShutdownScopesError) Unwrap() error {
	return ErrTaskDomainShutdownIncomplete
}

// TaskDomainUpgradeRequiredError is transport-neutral but carries the stable
// HTTP response contract expected by old mobile clients.
type TaskDomainUpgradeRequiredError struct {
	Status              int
	Code                string
	SchemaVersion       string
	Retryable           bool
	Reason              string
	WorkspaceID         string
	RequiredGeneration  uint64
	PresentedGeneration uint64
}

func (e *TaskDomainUpgradeRequiredError) Error() string {
	if e == nil {
		return ErrTaskDomainUpgradeRequired.Error()
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Reason)
}

func (e *TaskDomainUpgradeRequiredError) Unwrap() error {
	return ErrTaskDomainUpgradeRequired
}

func newTaskDomainUpgradeRequired(reason, workspaceID string, required, presented uint64) error {
	return &TaskDomainUpgradeRequiredError{
		Status:              http.StatusUpgradeRequired,
		Code:                TaskDomainUpgradeRequiredCode,
		SchemaVersion:       SchemaVersion,
		Retryable:           false,
		Reason:              reason,
		WorkspaceID:         workspaceID,
		RequiredGeneration:  required,
		PresentedGeneration: presented,
	}
}

func invalidTaskDomainShutdownInput(detail string) error {
	return fmt.Errorf("%w: %s", ErrInvalidTaskDomainShutdownInput, detail)
}

func validTaskDomainShutdownScope(scope MobileOperationScope) bool {
	switch scope {
	case MobileScopeChanges, MobileScopeMutation, MobileScopeSnapshot, MobileScopeWatch:
		return true
	default:
		return false
	}
}

func validMobileBindingKind(kind MobileBindingKind) bool {
	switch kind {
	case MobileBindingCursor, MobileBindingSession, MobileBindingToken:
		return true
	default:
		return false
	}
}

func isTaskDomainEntity(entity MobileEntity) bool {
	switch entity {
	case MobileEntityEvent,
		MobileEntityProject,
		MobileEntityTask,
		MobileEntityTaskOccurrence,
		MobileEntityTaskRecurrenceRule,
		MobileEntityWatchTaskProjection:
		return true
	default:
		return false
	}
}

func knownMobileEntity(entity MobileEntity) bool {
	if isTaskDomainEntity(entity) {
		return true
	}
	switch entity {
	case MobileEntityNote, MobileEntityVoiceNote, MobileEntityVoiceAudio:
		return true
	default:
		return false
	}
}
