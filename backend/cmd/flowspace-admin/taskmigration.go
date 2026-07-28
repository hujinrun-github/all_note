package main

import (
	"context"
	"fmt"

	"github.com/hujinrun/flowspace/internal/config"
	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/taskmigration"
	"github.com/hujinrun/flowspace/internal/taskruntime"
)

// tenantStoreOpener is intentionally narrower than storage.Registry. It keeps
// the command dispatcher testable while ensuring the production path obtains a
// schema-verified SQL tenant store through the normal provider registry.
type tenantStoreOpener interface {
	OpenTenant(context.Context, storage.Config, string) (storage.Store, error)
}

type taskMigrationRunOptions struct {
	WorkspaceID        string
	MigrationID        string
	MigrationTimezone  string
	OwnerTimezone      string
	DeploymentTimezone string
	ReplayPageSize     int
	MaximumSteps       int
}

func openTaskMigrationStore(
	ctx context.Context,
	cfg config.RuntimeStorageConfig,
	opener tenantStoreOpener,
) (storage.SQLStore, func() error, taskmigration.Dialect, error) {
	if opener == nil {
		return nil, nil, "", fmt.Errorf("tenant store opener is required")
	}
	store, err := opener.OpenTenant(
		ctx,
		toStorageConfig(cfg.Environment, cfg.PlatformData),
		taskruntime.ExpectedTenantSchemaVersion,
	)
	if err != nil {
		return nil, nil, "", fmt.Errorf("open migrated tenant store: %w", err)
	}
	sqlStore, ok := store.(storage.SQLStore)
	if !ok || sqlStore.SQLDB() == nil {
		_ = store.Close()
		return nil, nil, "", fmt.Errorf("tenant store does not expose SQL access")
	}
	dialect, err := taskMigrationDialect(cfg.PlatformData.Driver)
	if err != nil {
		_ = store.Close()
		return nil, nil, "", err
	}
	return sqlStore, store.Close, dialect, nil
}

func taskMigrationDialect(driver config.DatabaseDriver) (taskmigration.Dialect, error) {
	switch driver {
	case config.DatabaseDriverPostgres:
		return taskmigration.DialectPostgres, nil
	case config.DatabaseDriverSQLite:
		return taskmigration.DialectSQLite, nil
	default:
		return "", fmt.Errorf("unsupported task migration database driver %q", driver)
	}
}

func printTaskMigrationStatus(
	ctx context.Context,
	cfg config.RuntimeStorageConfig,
	opener tenantStoreOpener,
	workspaceID string,
) error {
	store, closeStore, dialect, err := openTaskMigrationStore(ctx, cfg, opener)
	if err != nil {
		return err
	}
	defer closeStore()
	statusStore, err := taskmigration.NewMigrationStatusStore(store.SQLDB(), dialect)
	if err != nil {
		return err
	}
	status, err := statusStore.Load(ctx, workspaceID)
	if err != nil {
		return err
	}
	fmt.Printf("workspace=%s model=%s state=%s migration=%s revision=%d epoch=%d watermark=%d cutover_revision=%s outbox_head=%d replay_lag=%d accept_legacy_writes=%t last_error=%q\n",
		status.WorkspaceID, status.ModelVersion, status.MigrationState, status.MigrationID,
		status.Revision, status.WriteEpoch, status.SourceWatermark, formatOptionalUint64(status.CutoverRevision),
		status.OutboxHead, status.ReplayLag, status.AcceptLegacyWrites, status.LastError,
	)
	return nil
}

func runTaskMigrationToReady(
	ctx context.Context,
	cfg config.RuntimeStorageConfig,
	opener tenantStoreOpener,
	options taskMigrationRunOptions,
) error {
	store, closeStore, dialect, err := openTaskMigrationStore(ctx, cfg, opener)
	if err != nil {
		return err
	}
	defer closeStore()
	coordinator, err := taskmigration.NewMigrationCoordinator(taskmigration.MigrationCoordinatorConfig{
		DB:                 store.SQLDB(),
		Dialect:            dialect,
		WorkspaceID:        options.WorkspaceID,
		MigrationID:        options.MigrationID,
		MigrationTimezone:  options.MigrationTimezone,
		OwnerTimezone:      options.OwnerTimezone,
		DeploymentTimezone: options.DeploymentTimezone,
		ReplayPageSize:     options.ReplayPageSize,
		MaximumSteps:       options.MaximumSteps,
	})
	if err != nil {
		return err
	}
	state, err := coordinator.RunToReady(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("workspace=%s model=%s state=%s migration=%s revision=%d epoch=%d watermark=%d cutover_revision=%s accept_legacy_writes=%t\n",
		state.WorkspaceID, state.ModelVersion, state.MigrationState, state.MigrationID,
		state.Revision, state.WriteEpoch, state.SourceWatermark, formatOptionalUint64(state.CutoverRevision),
		state.AcceptLegacyWrites,
	)
	return nil
}

func formatOptionalUint64(value *uint64) string {
	if value == nil {
		return "none"
	}
	return fmt.Sprintf("%d", *value)
}
