package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/taskdomain"
)

func (s *store) TaskDomainReader(workspaceID string) taskdomain.TaskDomainReader {
	return newTaskDomainV2ProjectReader(s.db, workspaceID)
}

func (s *store) LoadTaskDomainRuntimeState(ctx context.Context, workspaceID string) (storage.TaskDomainRuntimeState, error) {
	var state storage.TaskDomainRuntimeState
	var domainEpoch, anchorEpoch int64
	err := s.db.QueryRowContext(ctx, `SELECT domain.workspace_id,domain.model_version,domain.migration_state,
		domain.write_epoch,anchor.epoch,anchor.state
		FROM workspace_task_domain_state domain
		JOIN tenant_workspaces anchor ON anchor.workspace_id=domain.workspace_id
		WHERE domain.workspace_id=?`, workspaceID).Scan(
		&state.WorkspaceID, &state.ModelVersion, &state.MigrationState,
		&domainEpoch, &anchorEpoch, &state.AnchorState,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return storage.TaskDomainRuntimeState{}, storage.ErrTenantWorkspaceMissing
	}
	if err != nil {
		return storage.TaskDomainRuntimeState{}, err
	}
	if domainEpoch != anchorEpoch {
		return storage.TaskDomainRuntimeState{}, storage.ErrTenantEpochMismatch
	}
	state.Epoch = domainEpoch
	return state, nil
}

func (r *sqliteTaskDomainV2ProjectReader) GetTaskAggregateState(ctx context.Context, taskID string) (taskdomain.TaskAggregateState, error) {
	result, err := r.GetTaskAggregate(ctx, taskID)
	if err != nil {
		return taskdomain.TaskAggregateState{}, err
	}
	return taskdomain.TaskAggregateState{
		Task: result.Task, Aggregate: result.Aggregate, ScheduleRevision: result.Schedule.Revision,
	}, nil
}

var _ storage.TaskDomainRuntimeStore = (*store)(nil)
var _ taskdomain.TaskAggregateStateReader = (*sqliteTaskDomainV2ProjectReader)(nil)
