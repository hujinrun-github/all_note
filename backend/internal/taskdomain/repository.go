package taskdomain

import (
	"context"
	"errors"
)

const (
	SystemInboxProjectID = "system-inbox"
	PersonalProjectID    = "personal"
)

var (
	ErrProjectNotFound    = errors.New("task-domain project not found")
	ErrTaskNotFound       = errors.New("task-domain task not found")
	ErrOccurrenceNotFound = errors.New("task-domain occurrence not found")
)

// ProjectSnapshot adds the independent optimistic revision to the pure
// project value returned by a request-scoped tenant reader.
type ProjectSnapshot struct {
	Project  Project
	Revision int64
}

// ProjectWrite is the complete project after-image plus the revision observed
// by the caller. Providers apply it with compare-and-swap semantics.
type ProjectWrite struct {
	Project          Project
	ExpectedRevision int64
}

// TaskAggregateWrite is the complete atomic after-image produced by a domain
// command. ExecutionLogs are persisted in the same fenced transaction.
type TaskAggregateWrite struct {
	Task                     *TaskRecord
	Aggregate                TaskAggregate
	ExpectedRevisions        AggregateExpectedRevisions
	ExpectedScheduleRevision int64
	ExecutionLogs            []ExecutionLog
}

type ProjectReader interface {
	GetProject(context.Context, string) (ProjectSnapshot, error)
}

type ProjectWriter interface {
	EnsureSystemProjects(context.Context) error
	SaveProject(context.Context, ProjectWrite) error
	DeleteProject(context.Context, string, int64) error
}

// TaskDomainReader is scoped to the workspace of the current tenant runtime.
// IDs therefore do not accept a workspace parameter and cannot select another
// tenant through this contract.
type TaskDomainReader interface {
	ProjectReader
	ListProjects(context.Context, ProjectListFilter) ([]ProjectSnapshot, error)
	ListTaskDefinitions(context.Context, TaskDefinitionListFilter) ([]TaskDefinitionSnapshot, error)
	GetTaskAggregate(context.Context, string) (TaskAggregateQueryResult, error)
	GetOccurrence(context.Context, string) (QueryOccurrenceSnapshot, error)
	ListTaskOccurrences(context.Context, string) ([]QueryOccurrenceSnapshot, error)
	ListOccurrences(context.Context, OccurrenceListFilter) ([]QueryOccurrenceSnapshot, error)
}

// TaskDomainWriter is a transaction-bound write repository. Production code
// obtains it only from TaskDomainFencedTx inside a successful fenced callback;
// request runtimes never expose it directly.
type TaskDomainWriter interface {
	ProjectWriter
	CreateTaskAggregate(context.Context, TaskAggregateSnapshot) error
	SaveTaskAggregate(context.Context, TaskAggregateWrite) error
	InstallScheduleVersion(context.Context, ScheduleVersionInstall) error
}

// TaskDomainReadRuntime is the complete task-domain capability exposed by a
// request-scoped tenant runtime.
type TaskDomainReadRuntime interface {
	TaskDomainReader() TaskDomainReader
}

// TaskDomainFencedTx is the callback-scoped capability supplied after the
// outer tenant writer has validated and locked the expected workspace epoch.
// It intentionally has no generic Transact, Store, or database accessor.
type TaskDomainFencedTx interface {
	TaskDomainWriter() TaskDomainWriter
}
