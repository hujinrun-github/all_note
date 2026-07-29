package taskruntime

import (
	"context"

	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/taskapp"
	"github.com/hujinrun/flowspace/internal/taskdomain"
)

// NewTransactionApplication builds the ordinary task application services on
// top of an already-fenced tenant transaction. Mobile-v2 uses this composition
// so the domain effect, terminal receipt, and change batches share one commit.
func NewTransactionApplication(
	tx storage.MobileV2TenantWriteTx,
	workspaceID string,
	epoch int64,
) (taskapp.RuntimeSnapshot, error) {
	if tx == nil || workspaceID == "" || epoch < 1 {
		return taskapp.RuntimeSnapshot{}, ErrTaskRuntimeType
	}
	reader := tx.TaskDomainReader()
	stateReader, ok := reader.(taskdomain.TaskAggregateStateReader)
	if !ok || stateReader == nil {
		return taskapp.RuntimeSnapshot{}, ErrTaskRuntimeType
	}
	scheduleReader, ok := reader.(taskdomain.ScheduleCommandStateReader)
	if !ok || scheduleReader == nil {
		return taskapp.RuntimeSnapshot{}, ErrTaskRuntimeType
	}
	roadmapReader, ok := reader.(taskdomain.RoadmapReader)
	if !ok || roadmapReader == nil {
		return taskapp.RuntimeSnapshot{}, ErrTaskRuntimeType
	}
	fencer := transactionFencer{tx: tx, workspaceID: workspaceID, epoch: epoch}
	tasks := taskdomain.NewTaskService(fencer, stateReader)
	occurrences := taskdomain.NewOccurrenceService(fencer, stateReader)
	projects := taskdomain.NewProjectService(fencer)
	roadmaps := taskdomain.NewRoadmapService(fencer)
	schedules := taskdomain.NewScheduleService(fencer, scheduleReader)
	return taskapp.RuntimeSnapshot{
		WorkspaceID: workspaceID, Epoch: epoch, Factory: taskdomain.TaskFactory{},
		Tasks:       taskServiceAdapter{delegate: tasks},
		Occurrences: occurrenceServiceAdapter{delegate: occurrences},
		Projects:    projectServiceAdapter{delegate: projects},
		Roadmaps:    roadmaps, RoadmapReader: roadmapReader,
		Schedules: scheduleServiceAdapter{delegate: schedules},
		Reader:    reader,
	}, nil
}

type transactionFencer struct {
	tx          storage.MobileV2TenantWriteTx
	workspaceID string
	epoch       int64
}

func (fencer transactionFencer) valid(workspaceID string, epoch int64) bool {
	return fencer.tx != nil && workspaceID == fencer.workspaceID && epoch == fencer.epoch
}

func (fencer transactionFencer) BeginFencedWrite(
	_ context.Context,
	workspaceID string,
	epoch int64,
	callback func(taskdomain.TaskDomainFencedTx) error,
) error {
	if !fencer.valid(workspaceID, epoch) || callback == nil {
		return taskdomain.ErrInvalidTaskCommand
	}
	return callback(fencer.tx)
}

func (fencer transactionFencer) BeginFencedProjectWrite(
	_ context.Context,
	workspaceID string,
	epoch int64,
	callback func(taskdomain.ProjectCommandTx) error,
) error {
	if !fencer.valid(workspaceID, epoch) || callback == nil {
		return taskdomain.ErrInvalidProjectCommand
	}
	return callback(fencer.tx)
}

func (fencer transactionFencer) BeginFencedRoadmapWrite(
	_ context.Context,
	workspaceID string,
	epoch int64,
	callback func(taskdomain.RoadmapCommandTx) error,
) error {
	if !fencer.valid(workspaceID, epoch) || callback == nil {
		return taskdomain.ErrInvalidRoadmapCommand
	}
	return callback(fencer.tx)
}

func (fencer transactionFencer) BeginFencedScheduleWrite(
	_ context.Context,
	workspaceID string,
	epoch int64,
	callback func(taskdomain.ScheduleCommandFencedTx) error,
) error {
	if !fencer.valid(workspaceID, epoch) || callback == nil {
		return taskdomain.ErrInvalidScheduleCommand
	}
	return callback(fencer.tx)
}

var (
	_ taskdomain.TaskDomainCommandFencer = transactionFencer{}
	_ taskdomain.ProjectCommandFencer    = transactionFencer{}
	_ taskdomain.RoadmapCommandFencer    = transactionFencer{}
	_ taskdomain.ScheduleCommandFencer   = transactionFencer{}
)
