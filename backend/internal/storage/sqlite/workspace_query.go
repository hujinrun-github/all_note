package sqlite

import (
	"context"

	"github.com/hujinrun/flowspace/internal/storage/workspacequerydb"
	"github.com/hujinrun/flowspace/internal/taskdomain"
	"github.com/hujinrun/flowspace/internal/workspacequery"
)

func (r *sqliteTaskDomainV2ProjectReader) workspaceQueries() (*workspacequerydb.Reader, error) {
	if r == nil {
		return nil, workspacequery.ErrRuntimeUnavailable
	}
	db, ok := r.queryer.(workspacequerydb.Beginner)
	if !ok {
		return nil, workspacequery.ErrRuntimeUnavailable
	}
	return workspacequerydb.New(db, r.workspaceID, workspacequerydb.SQLite, sqliteTaskDomainV2OccurrenceSelect,
		func(scanner workspacequerydb.Scanner) (taskdomain.QueryOccurrenceSnapshot, error) {
			return scanSQLiteTaskDomainV2Occurrence(scanner)
		}), nil
}

func (r *sqliteTaskDomainV2ProjectReader) Overview(ctx context.Context, request workspacequery.Request) (workspacequery.PlanningOverview, error) {
	reader, err := r.workspaceQueries()
	if err != nil {
		return workspacequery.PlanningOverview{}, err
	}
	return reader.Overview(ctx, request)
}
func (r *sqliteTaskDomainV2ProjectReader) ProjectSummaries(ctx context.Context, request workspacequery.ProjectRequest) ([]workspacequery.ProjectSummary, error) {
	reader, err := r.workspaceQueries()
	if err != nil {
		return nil, err
	}
	return reader.ProjectSummaries(ctx, request)
}
func (r *sqliteTaskDomainV2ProjectReader) ProjectOverview(ctx context.Context, request workspacequery.ProjectRequest) (workspacequery.ProjectSummary, error) {
	reader, err := r.workspaceQueries()
	if err != nil {
		return workspacequery.ProjectSummary{}, err
	}
	return reader.ProjectOverview(ctx, request)
}

var _ workspacequery.Reader = (*sqliteTaskDomainV2ProjectReader)(nil)
