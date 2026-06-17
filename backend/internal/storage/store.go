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
	FolderID string
	Query    string
	Page     int
	PageSize int
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
}

type TaskRepository interface{}
type EventRepository interface{}
type InboxRepository interface{}
type RoadmapRepository interface{}
type SyncRepository interface{}
type SearchRepository interface {
	Search(context.Context, string, int, int) ([]model.SearchResult, int, error)
}
