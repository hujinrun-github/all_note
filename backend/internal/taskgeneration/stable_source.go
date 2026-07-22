package taskgeneration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type ControlDialect string

const (
	ControlDialectSQLite   ControlDialect = "sqlite"
	ControlDialectPostgres ControlDialect = "postgres"
)

type ActiveWorkspaceSource interface {
	ListActiveWorkspaces(context.Context) ([]StableV2Workspace, error)
}

// StableV2Classifier must compare the active control epoch with the tenant
// anchor/task-domain epoch and accept only model=v2 in idle or cutover state.
// Returning false,nil is the normal result for a stable legacy workspace.
type StableV2Classifier interface {
	IsStableV2Workspace(context.Context, string, int64) (bool, error)
}

type StableV2ClassifierFunc func(context.Context, string, int64) (bool, error)

func (fn StableV2ClassifierFunc) IsStableV2Workspace(ctx context.Context, workspaceID string, epoch int64) (bool, error) {
	if fn == nil {
		return false, ErrInvalidScheduler
	}
	return fn(ctx, workspaceID, epoch)
}

type SQLActiveWorkspaceSource struct {
	db      *sql.DB
	dialect ControlDialect
}

func NewSQLActiveWorkspaceSource(db *sql.DB, dialect ControlDialect) (*SQLActiveWorkspaceSource, error) {
	if db == nil || (dialect != ControlDialectSQLite && dialect != ControlDialectPostgres) {
		return nil, ErrInvalidScheduler
	}
	return &SQLActiveWorkspaceSource{db: db, dialect: dialect}, nil
}

func (s *SQLActiveWorkspaceSource) ListActiveWorkspaces(ctx context.Context) ([]StableV2Workspace, error) {
	if s == nil || s.db == nil {
		return nil, ErrInvalidScheduler
	}
	rows, err := s.db.QueryContext(ctx, `SELECT workspace_id,epoch
		FROM workspace_runtime_state WHERE mode='active' ORDER BY workspace_id`)
	if err != nil {
		return nil, fmt.Errorf("list active generation workspaces: %w", err)
	}
	defer rows.Close()
	workspaces := make([]StableV2Workspace, 0)
	for rows.Next() {
		var workspace StableV2Workspace
		if err := rows.Scan(&workspace.WorkspaceID, &workspace.Epoch); err != nil {
			return nil, fmt.Errorf("scan active generation workspace: %w", err)
		}
		workspaces = append(workspaces, workspace)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read active generation workspaces: %w", err)
	}
	if err := validateStableWorkspaces(workspaces); err != nil {
		return nil, err
	}
	return workspaces, nil
}

type DurableStableV2Source struct {
	active     ActiveWorkspaceSource
	classifier StableV2Classifier
}

func NewDurableStableV2Source(active ActiveWorkspaceSource, classifier StableV2Classifier) (*DurableStableV2Source, error) {
	if active == nil || classifier == nil {
		return nil, ErrInvalidScheduler
	}
	return &DurableStableV2Source{active: active, classifier: classifier}, nil
}

// ListStableV2Workspaces preserves partial progress: a broken or transitional
// tenant is excluded and reported, while other stable v2 workspaces remain
// schedulable. Scheduler.Reconcile joins this error after processing the list.
func (s *DurableStableV2Source) ListStableV2Workspaces(ctx context.Context) ([]StableV2Workspace, error) {
	if s == nil || s.active == nil || s.classifier == nil {
		return nil, ErrInvalidScheduler
	}
	candidates, err := s.active.ListActiveWorkspaces(ctx)
	if err != nil {
		return nil, err
	}
	stable := make([]StableV2Workspace, 0, len(candidates))
	var classificationErr error
	for _, candidate := range candidates {
		eligible, err := s.classifier.IsStableV2Workspace(ctx, candidate.WorkspaceID, candidate.Epoch)
		if err != nil {
			classificationErr = errors.Join(classificationErr,
				fmt.Errorf("classify generation workspace %s: %w", candidate.WorkspaceID, err))
			continue
		}
		if eligible {
			stable = append(stable, candidate)
		}
	}
	return stable, classificationErr
}

var _ ActiveWorkspaceSource = (*SQLActiveWorkspaceSource)(nil)
var _ StableV2WorkspaceSource = (*DurableStableV2Source)(nil)
