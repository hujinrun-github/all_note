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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := runAdminCommand(ctx, os.Args[1:], runtimeStorage, registry); err != nil {
		log.Fatal(err)
	}
}

func runAdminCommand(ctx context.Context, args []string, cfg config.RuntimeStorageConfig, registry maintenanceRegistry) error {
	if len(args) == 0 {
		return fmt.Errorf("admin command is required: migrate-control, migrate-tenant, or adopt-tenant")
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
