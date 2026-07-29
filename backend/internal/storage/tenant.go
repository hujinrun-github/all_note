package storage

import (
	"context"
	"database/sql"
	"errors"

	"github.com/hujinrun/flowspace/internal/taskdomain"
)

var (
	ErrTenantWorkspaceMissing = errors.New("tenant workspace anchor is missing")
	ErrTenantWorkspaceFenced  = errors.New("tenant workspace is not writable")
	ErrTenantEpochMismatch    = errors.New("tenant runtime epoch mismatch")
	ErrTenantWriteTxClosed    = errors.New("tenant write transaction is closed")
)

type TenantOutboxEvent struct {
	ID                string
	Topic             string
	AggregateID       string
	AggregateRevision int64
	PayloadJSON       string
}

type TenantWriteTx interface {
	EnqueueOutbox(context.Context, TenantOutboxEvent) error
	TaskDomainWriter() taskdomain.TaskDomainWriter
	ScheduleCommandWriter() taskdomain.ScheduleCommandWriter
	GetProject(context.Context, string) (taskdomain.ProjectSnapshot, error)
	CountNonTerminalProjectOccurrences(context.Context, string) (int, error)
	ProjectWriter() taskdomain.ProjectWriter
	RoadmapWriter() taskdomain.RoadmapWriter
	GetRoadmapByProject(context.Context, string) (taskdomain.RoadmapSnapshot, error)
	GetRoadmapByID(context.Context, string) (taskdomain.RoadmapSnapshot, error)
	GetRoadmapNode(context.Context, string) (taskdomain.RoadmapNodeSnapshot, error)
	CountRoadmapNodeTasks(context.Context, string) (int, error)
	LoadRecurringCompletionState(context.Context, string) (taskdomain.RecurringCompletionCommandState, error)
	ListGenerationTargets(context.Context) ([]taskdomain.GenerationTargetState, error)
	InsertMissingOccurrences(context.Context, taskdomain.GenerationInsert) error
	CompleteGeneration(context.Context, taskdomain.GenerationCompletion) error
}

type TenantFencedWriter interface {
	BeginFencedWrite(context.Context, string, int64, func(TenantWriteTx) error) error
}

// MobileV2TenantFencedWriter exposes a fenced transaction for non-task
// mobile-v2 commands without mutating task-domain cutover metadata.
type MobileV2TenantFencedWriter interface {
	TenantFencedWriter
	BeginFencedMobileV2Write(context.Context, string, int64, func(MobileV2TenantWriteTx) error) error
}

type TenantSQLRunner interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// MobileV2TenantWriteTx is the narrow production bridge that lets the
// mobile-v2 command boundary reuse the already-fenced task transaction. It
// avoids a nested transaction while keeping raw SQL unavailable from ordinary
// task-domain handlers.
type MobileV2TenantWriteTx interface {
	TenantWriteTx
	TaskDomainReader() taskdomain.TaskDomainReader
	MobileV2SQLRunner() TenantSQLRunner
}

type TenantMigrationFencer interface {
	FenceWorkspace(context.Context, string, int64, string) (int64, error)
	ActivateWorkspace(context.Context, string, int64, string) error
}
