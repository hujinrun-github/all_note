package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/hujinrun/flowspace/internal/config"
	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/storage/postgres"
	"github.com/hujinrun/flowspace/internal/storage/sqlite"
)

type maintenanceRegistry interface {
	MigrateControl(context.Context, storage.Config) error
	MigrateTenant(context.Context, storage.Config) error
	AdoptExistingTenant(context.Context, storage.Config, storage.AdoptManifest) error
}

func main() {
	legacyRuntime := config.LoadStorageConfig()
	runtimeStorage, err := config.LoadRuntimeStorageConfig(
		legacyRuntime.Environment,
		config.RuntimeStorageLoadOptions{AllowLegacyUpgrade: true},
	)
	if err != nil {
		log.Fatalf("runtime storage config: %v", err)
	}

	registry := storage.NewRegistry()
	if err := registry.Register(postgres.Provider{}); err != nil {
		log.Fatalf("register postgres provider: %v", err)
	}
	if err := registry.Register(sqlite.Provider{}); err != nil {
		log.Fatalf("register sqlite provider: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), adminCommandTimeout(os.Args[1:]))
	defer cancel()
	if err := runAdminCommand(ctx, os.Args[1:], runtimeStorage, registry); err != nil {
		log.Fatal(err)
	}
}

func adminCommandTimeout(args []string) time.Duration {
	if len(args) != 0 && (args[0] == "task-migration-run-to-ready" || args[0] == "task-migration-cutover") {
		return 60 * time.Minute
	}
	return 10 * time.Minute
}

func runAdminCommand(ctx context.Context, args []string, cfg config.RuntimeStorageConfig, registry maintenanceRegistry) error {
	if len(args) == 0 {
		return fmt.Errorf("admin command is required: migrate-control, migrate-tenant, adopt-tenant, task-migration-status, task-migration-run-to-ready, or task-migration-cutover")
	}

	switch args[0] {
	case "migrate-control":
		if len(args) != 1 {
			return fmt.Errorf("migrate-control does not accept arguments")
		}
		return registry.MigrateControl(ctx, toStorageConfig(cfg.Environment, cfg.Control))
	case "migrate-tenant":
		if len(args) != 1 {
			return fmt.Errorf("migrate-tenant does not accept arguments")
		}
		return registry.MigrateTenant(ctx, toStorageConfig(cfg.Environment, cfg.PlatformData))
	case "adopt-tenant":
		flags := flag.NewFlagSet("adopt-tenant", flag.ContinueOnError)
		manifestID := flags.String("manifest-id", "", "versioned legacy manifest id")
		manifestChecksum := flags.String("manifest-checksum", "", "expected manifest checksum")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 || *manifestID == "" || *manifestChecksum == "" {
			return fmt.Errorf("adopt-tenant requires --manifest-id and --manifest-checksum")
		}
		return registry.AdoptExistingTenant(
			ctx,
			toStorageConfig(cfg.Environment, cfg.PlatformData),
			storage.AdoptManifest{ID: *manifestID, Checksum: *manifestChecksum},
		)
	case "task-migration-status":
		flags := flag.NewFlagSet("task-migration-status", flag.ContinueOnError)
		workspaceID := flags.String("workspace-id", "", "workspace to inspect")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 || *workspaceID == "" {
			return fmt.Errorf("task-migration-status requires --workspace-id")
		}
		opener, ok := registry.(tenantStoreOpener)
		if !ok {
			return fmt.Errorf("task-migration-status requires a registry with tenant SQL access")
		}
		return printTaskMigrationStatus(ctx, cfg, opener, *workspaceID)
	case "task-migration-run-to-ready":
		flags := flag.NewFlagSet("task-migration-run-to-ready", flag.ContinueOnError)
		workspaceID := flags.String("workspace-id", "", "workspace to migrate")
		migrationID := flags.String("migration-id", "", "stable operator migration id")
		migrationTimezone := flags.String("migration-timezone", "", "frozen IANA timezone for legacy interpretation")
		ownerTimezone := flags.String("owner-timezone", "", "optional owner IANA timezone fallback")
		deploymentTimezone := flags.String("deployment-timezone", "UTC", "optional deployment IANA timezone fallback")
		replayPageSize := flags.Int("replay-page-size", 100, "maximum outbox events per replay page")
		maximumSteps := flags.Int("maximum-steps", 100000, "maximum durable coordinator steps")
		confirmFenceLegacyWrites := flags.Bool(
			"confirm-fence-legacy-writes",
			false,
			"confirm that this command closes legacy task writes before stopping at legacy/ready",
		)
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 || *workspaceID == "" || *migrationID == "" || *migrationTimezone == "" {
			return fmt.Errorf("task-migration-run-to-ready requires --workspace-id, --migration-id, and --migration-timezone")
		}
		if *replayPageSize < 1 || *maximumSteps < 1 {
			return fmt.Errorf("replay page size and maximum steps must be positive")
		}
		if !*confirmFenceLegacyWrites {
			return fmt.Errorf("task-migration-run-to-ready requires --confirm-fence-legacy-writes")
		}
		opener, ok := registry.(tenantStoreOpener)
		if !ok {
			return fmt.Errorf("task-migration-run-to-ready requires a registry with tenant SQL access")
		}
		return runTaskMigrationToReady(ctx, cfg, opener, taskMigrationRunOptions{
			WorkspaceID:        *workspaceID,
			MigrationID:        *migrationID,
			MigrationTimezone:  *migrationTimezone,
			OwnerTimezone:      *ownerTimezone,
			DeploymentTimezone: *deploymentTimezone,
			ReplayPageSize:     *replayPageSize,
			MaximumSteps:       *maximumSteps,
		})
	case "task-migration-cutover":
		flags := flag.NewFlagSet("task-migration-cutover", flag.ContinueOnError)
		workspaceID := flags.String("workspace-id", "", "workspace to cut over")
		migrationID := flags.String("migration-id", "", "stable operator migration id")
		ownerTimezone := flags.String("owner-timezone", "", "optional owner IANA timezone fallback")
		deploymentTimezone := flags.String("deployment-timezone", "UTC", "optional deployment IANA timezone fallback")
		confirmBackendOffline := flags.Bool(
			"confirm-backend-offline",
			false,
			"confirm that the single backend service has been stopped",
		)
		confirmRetireMobileV1 := flags.Bool(
			"confirm-retire-mobile-v1-task-api",
			false,
			"confirm that mobile-v1 sync and watch task endpoints will return upgrade-required",
		)
		confirmIrreversibleCutover := flags.Bool(
			"confirm-irreversible-cutover",
			false,
			"confirm the durable per-workspace switch to task-domain v2",
		)
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 || *workspaceID == "" || *migrationID == "" {
			return fmt.Errorf("task-migration-cutover requires --workspace-id and --migration-id")
		}
		if !*confirmBackendOffline || !*confirmRetireMobileV1 || !*confirmIrreversibleCutover {
			return fmt.Errorf("task-migration-cutover requires --confirm-backend-offline, --confirm-retire-mobile-v1-task-api, and --confirm-irreversible-cutover")
		}
		opener, ok := registry.(taskCutoverStoreOpener)
		if !ok {
			return fmt.Errorf("task-migration-cutover requires a registry with control and tenant SQL access")
		}
		nativeCfg, err := config.LoadNativeConfig()
		if err != nil {
			return fmt.Errorf("load task-domain v2 application capability: %w", err)
		}
		return cutoverTaskMigration(ctx, cfg, opener, taskMigrationCutoverOptions{
			WorkspaceID:        *workspaceID,
			MigrationID:        *migrationID,
			OwnerTimezone:      *ownerTimezone,
			DeploymentTimezone: *deploymentTimezone,
			RoutingEnabled:     nativeCfg.TaskDomainV2RoutingEnabled,
			OfflineGate:        newSingleInstanceOfflineGate(),
		})
	default:
		return fmt.Errorf("unsupported admin command %q", args[0])
	}
}

func toStorageConfig(environment string, cfg config.DatabaseConfig) storage.Config {
	return storage.Config{
		Env:        environment,
		Driver:     storage.Driver(cfg.Driver),
		URL:        cfg.URL,
		SQLitePath: cfg.SQLitePath,
	}
}
