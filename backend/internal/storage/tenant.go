package storage

import (
	"context"
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

type TenantMigrationFencer interface {
	FenceWorkspace(context.Context, string, int64, string) (int64, error)
	ActivateWorkspace(context.Context, string, int64, string) error
}
