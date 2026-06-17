package storage

import (
	"context"

	"github.com/hujinrun/flowspace/internal/model"
)

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
	Events() EventRepository
	Inbox() InboxRepository
	Roadmaps() RoadmapRepository
	Sync() SyncRepository
	Search() SearchRepository
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
	Project     string
	Status      string
	Scope       string
	Horizon     string
	ProjectID   string
	PlannedDate string
	Page        int
	PageSize    int
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
	GetDefaultTarget(context.Context, string) (*model.SyncTarget, error)
	ListTargets(context.Context) ([]model.SyncTarget, error)
	UpsertState(context.Context, *model.SyncState) error
	GetState(context.Context, string, string) (*model.SyncState, error)
	ListStatesByTarget(context.Context, string) ([]model.SyncState, error)
	DeleteState(context.Context, string, string) error
	ListExternalDeletedStates(context.Context, string) ([]model.ExternalDeletedNote, error)
}
type SearchRepository interface {
	Search(context.Context, string, int, int) ([]model.SearchResult, int, error)
}
