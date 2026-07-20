package tenantmigration

import (
	"context"
	"errors"
	"fmt"
)

var (
	ErrTargetWorkspaceInUse = errors.New("target workspace is active or owned by another migration")
	ErrRetiredDataExists    = errors.New("target contains retired workspace data; explicit replacement is required")
)

type ImportTarget interface {
	BeginImport(context.Context) (ImportTransaction, error)
}
type ImportTransaction interface {
	WorkspaceState(context.Context, string) (state, migrationID string, err error)
	HasActiveBinding(context.Context, string) (bool, error)
	DeleteWorkspace(context.Context, string) error
	PrepareFenced(context.Context, string, string) error
	InsertRows(context.Context, LogicalTable, []LogicalRow) error
	Commit() error
	Rollback() error
}
type ImportOptions struct {
	MigrationID    string
	ReplaceRetired bool
}

func Import(ctx context.Context, target ImportTarget, pack TransferPackage, options ImportOptions) error {
	if target == nil || options.MigrationID == "" || pack.Manifest.WorkspaceID == "" {
		return errors.New("import target, migration id, and workspace are required")
	}
	tx, err := target.BeginImport(ctx)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	state, migrationID, err := tx.WorkspaceState(ctx, pack.Manifest.WorkspaceID)
	if err != nil {
		return err
	}
	switch state {
	case "missing":
	case "fenced":
		if migrationID != options.MigrationID {
			return ErrTargetWorkspaceInUse
		}
		if err := tx.DeleteWorkspace(ctx, pack.Manifest.WorkspaceID); err != nil {
			return err
		}
	case "retired":
		if !options.ReplaceRetired {
			return ErrRetiredDataExists
		}
		active, err := tx.HasActiveBinding(ctx, pack.Manifest.WorkspaceID)
		if err != nil {
			return err
		}
		if active {
			return ErrTargetWorkspaceInUse
		}
		if err := tx.DeleteWorkspace(ctx, pack.Manifest.WorkspaceID); err != nil {
			return err
		}
	default:
		return ErrTargetWorkspaceInUse
	}
	if err := tx.PrepareFenced(ctx, pack.Manifest.WorkspaceID, options.MigrationID); err != nil {
		return err
	}
	for _, data := range pack.Tables {
		if err := tx.InsertRows(ctx, data.Table, data.Rows); err != nil {
			return fmt.Errorf("import logical table %s failed", data.Table.Name)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}
