package storage

import (
	"context"
	"errors"
	"time"

	"github.com/hujinrun/flowspace/internal/model"
)

var ErrNotImplemented = errors.New("not implemented")

type Capabilities struct {
	FullTextSearch bool
	PrefixSearch   bool
	TrigramSearch  bool
	JSONObjects    bool
	ArrayColumns   bool
	TimeRanges     bool
	AdvisoryLocks  bool
}

type Provider interface {
	Driver() Driver
	Validate(Config) error
	Open(context.Context, Config) (Store, error)
	Migrate(context.Context, Config) error
}

type Store interface {
	Close() error
	Health(context.Context) error
	Capabilities() Capabilities
	Transact(context.Context, func(Store) error) error

	Folders() FolderRepository
	Notes() NoteRepository
	Tasks() TaskRepository
	Recurrence() RecurrenceRepository
	Events() EventRepository
	Inbox() InboxRepository
	Roadmaps() RoadmapRepository
	Sync() SyncRepository
	Search() SearchRepository
	Auth() AuthRepository
}

type FolderRepository interface {
	List(context.Context) ([]model.Folder, error)
	Exists(context.Context, string) (bool, error)
}

type NoteFilter struct {
	FolderID   string
	ProjectID  string
	Unassigned bool
	Query      string
	Sort       string // "recent" | "az"
	Page       int
	PageSize   int
}

type UserListFilter struct {
	Page     int
	PageSize int
	Query    string
}

type AuthRepository interface {
	CreateUser(context.Context, *model.User) error
	SetDefaultWorkspace(context.Context, string, string) error
	GetUserByEmail(context.Context, string) (*model.User, error)
	GetUserByID(context.Context, string) (*model.User, error)
	ListUsers(context.Context, UserListFilter) ([]model.User, int, error)
	UpdateUser(context.Context, string, *model.UpdateUserRequest) (*model.User, error)
	UpdateUserLastLogin(context.Context, string, time.Time) error
	UpdateUserPassword(context.Context, string, string, bool) error
	CreateWorkspace(context.Context, *model.Workspace) error
	AddWorkspaceMember(context.Context, string, string, string) error
	CreateSession(context.Context, *model.Session) error
	GetSessionByTokenHash(context.Context, string) (*model.Session, error)
	GetWorkspaceMembership(context.Context, string, string) (*model.WorkspaceMember, error)
	RevokeSession(context.Context, string) error
	RevokeUserSessions(context.Context, string) error
	RevokeUserSessionsExcept(context.Context, string, string) error
	RecordAuditEvent(context.Context, *model.AuditEvent) error
	LockActiveAdmins(context.Context) ([]model.User, error)
}

type NoteRepository interface {
	List(context.Context, NoteFilter) ([]model.Note, int, error)
	GetByID(context.Context, string) (*model.Note, error)
	Create(context.Context, *model.CreateNoteRequest) (*model.Note, error)
	CreateWithID(context.Context, *model.Note) error
	Update(context.Context, string, *model.UpdateNoteRequest) (*model.Note, error)
	Delete(context.Context, string) error
	ListAll(context.Context) ([]model.Note, error)
	Recent(context.Context, int) ([]model.Note, error)
	GetNotesByProjectIDs(ctx context.Context, projectIDs []string) (map[string][]model.NoteRef, error)
}

type TaskFilter struct {
	Project       string
	Status        string
	Scope         string
	Horizon       string
	ProjectID     string
	PlannedDate   string
	RoadmapNodeID string
	ExecutionType string // "" (default=single), "single", "recurring", "all"
	Page          int
	PageSize      int
}

// ExecutionTypeFilter returns the WHERE clause and args for execution_type filtering.
// Empty and NULL execution_type values mean "single" for backward compatibility.
func ExecutionTypeFilter(execType string) (string, []any) {
	switch execType {
	case "recurring":
		return "t.execution_type = 'recurring'", nil
	case "all":
		return "", nil // no filter
	default: // "" or "single"
		return "(t.execution_type IS NULL OR t.execution_type = '' OR t.execution_type = 'single')", nil
	}
}

type TaskRepository interface {
	List(context.Context, TaskFilter) ([]model.Task, int, error)
	ListProjects(context.Context) ([]model.TaskProject, error)
	CreateProject(context.Context, *model.CreateTaskProjectRequest) (*model.TaskProject, error)
	UpdateProject(context.Context, string, *model.UpdateTaskProjectRequest) (*model.TaskProject, error)
	DeleteProject(context.Context, string) error
	GetProjectByID(context.Context, string) (*model.TaskProject, error)
	GetProjectByName(context.Context, string) (*model.TaskProject, error)
	Create(context.Context, *model.Task) error
	Update(context.Context, string, *model.UpdateTaskRequest) (*model.Task, error)
	GetByID(context.Context, string) (*model.Task, error)
	Delete(context.Context, string) error
	Today(context.Context, int64, int64, int64) ([]model.Task, []model.Task, error)
	GetCompletedTasksByRange(ctx context.Context, from, to int64, page, pageSize int) ([]model.TaskSummary, int, error)
	GetSummaryStats(ctx context.Context, from, to int64) (activeDays, projectCount int, err error)
}

type RecurrenceRepository interface {
	UpsertRule(ctx context.Context, rule *model.RecurrenceRule) error
	GetRule(ctx context.Context, taskID string) (*model.RecurrenceRule, error)
	DeleteRule(ctx context.Context, taskID string) error
	ListActiveRules(ctx context.Context, from, to string) ([]model.RecurrenceRule, error)
	ListOccurrences(ctx context.Context, from, to string) ([]model.TaskOccurrence, error)
	GetCompletedOccurrencesByRange(ctx context.Context, from, to int64) ([]model.TaskSummary, error)
	CompleteOccurrence(ctx context.Context, taskID, date string, completedAt int64) (*model.TaskOccurrence, error)
	ReopenOccurrence(ctx context.Context, taskID, date string) (*model.TaskOccurrence, error)
	SkipOccurrence(ctx context.Context, taskID, date string) (*model.TaskOccurrence, error)
	CountOccurrencesByTask(ctx context.Context, taskID string) (int, error)
}

type EventRepository interface {
	List(context.Context, int64, int64, int, int) ([]model.Event, int, error)
	Create(context.Context, *model.Event) error
	Update(context.Context, string, *model.UpdateEventRequest) (*model.Event, error)
	GetByID(context.Context, string) (*model.Event, error)
	Delete(context.Context, string) error
	Today(context.Context, int64, int64) ([]model.Event, error)
}

type InboxRepository interface {
	List(context.Context, string, int, int) ([]model.InboxItem, int, error)
	Create(context.Context, *model.InboxItem) error
	GetByID(context.Context, string) (*model.InboxItem, error)
	MarkConverted(context.Context, string, string) error
	Delete(context.Context, string) error
	BatchArchive(context.Context, []string) (int64, error)
	BatchDelete(context.Context, []string) (int64, error)
}
type RoadmapRepository interface {
	ReplaceLearningRoadmap(context.Context, *model.LearningRoadmap) (*model.LearningRoadmap, error)
	SaveFailedLearningRoadmap(context.Context, string, string, string) (*model.LearningRoadmap, error)
	GetLearningRoadmap(context.Context, string) (*model.LearningRoadmap, error)
	GetLearningRoadmapByID(context.Context, string) (*model.LearningRoadmap, error)
	ListRoadmapNodes(context.Context, string) ([]model.RoadmapNode, error)
	ListRoadmapEdges(context.Context, string) ([]model.RoadmapEdge, error)
	GetRoadmapNode(context.Context, string) (*model.RoadmapNode, error)
	CreateRoadmapNode(context.Context, *model.RoadmapNode, *model.RoadmapEdge) (*model.RoadmapNode, error)
	UpdateRoadmapNode(context.Context, string, *model.UpdateRoadmapNodeRequest) (*model.RoadmapNode, error)
	DeleteRoadmapNode(context.Context, string) error
	UpdateRoadmapNodeStatus(context.Context, string, string) error
	UpdateRoadmapLayout(context.Context, string, []model.RoadmapLayoutNode) error
	ListRoadmapResources(context.Context, string) ([]model.RoadmapResource, error)
	AddRoadmapResource(context.Context, *model.RoadmapResource) error
	DeleteRoadmapResource(context.Context, string) error
}
type SyncRepository interface {
	SaveTarget(context.Context, *model.SyncTarget) error
	GetTarget(ctx context.Context, targetID string) (*model.SyncTarget, error)
	LockTarget(ctx context.Context, targetID string) (*model.SyncTarget, error)
	GetDefaultTarget(context.Context, string) (*model.SyncTarget, error)
	ListTargets(context.Context) ([]model.SyncTarget, error)
	DeleteTarget(ctx context.Context, targetID string) error
	CountBindingsByTarget(ctx context.Context, targetID string) (int, error)
	CountClaimsByTarget(ctx context.Context, targetID string) (int, error)
	CountStatesByTarget(ctx context.Context, targetID string) (int, error)
	UpsertState(context.Context, *model.SyncState) error
	GetState(context.Context, string, string) (*model.SyncState, error)
	ListStatesByTarget(context.Context, string) ([]model.SyncState, error)
	DeleteState(context.Context, string, string) error
	ListExternalDeletedStates(context.Context, string) ([]model.ExternalDeletedNote, error)
	LockBindingSlot(ctx context.Context, noteID string) error
	GetBinding(ctx context.Context, noteID string) (*model.NoteSyncBinding, error)
	PutBinding(ctx context.Context, binding model.NoteSyncBinding) error
	DeleteBinding(ctx context.Context, noteID string) error
	ListBindingsByTarget(ctx context.Context, targetID string) ([]model.NoteSyncBinding, error)
	GetExternalClaim(ctx context.Context, externalKey string) (*model.SyncExternalClaim, error)
	GetExternalClaimByNote(ctx context.Context, noteID string) (*model.SyncExternalClaim, error)
	PutExternalClaim(ctx context.Context, claim model.SyncExternalClaim) error
	ReleaseExternalClaim(ctx context.Context, noteID string) error
	PutSuppression(ctx context.Context, suppression model.NoteSyncSuppression) error
	DeleteSuppression(ctx context.Context, noteID string, targetID string) error
	GetSuppression(ctx context.Context, noteID string, targetID string) (*model.NoteSyncSuppression, error)
	PutImportTombstone(ctx context.Context, tombstone model.SyncImportTombstone) error
	DeleteImportTombstone(ctx context.Context, externalKey string) error
	DeleteImportTombstonesForNoteTarget(ctx context.Context, noteID string, targetID string) error
	FindImportTombstone(ctx context.Context, targetID string, externalKey string, formerNoteID string, externalType string) (*model.SyncImportTombstone, error)
}
type SearchRepository interface {
	Search(context.Context, string, int, int) ([]model.SearchResult, int, error)
}
