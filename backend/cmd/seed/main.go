package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/bootstrap"
	"github.com/hujinrun/flowspace/internal/config"
	"github.com/hujinrun/flowspace/internal/repository"
	storagepkg "github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/storage/postgres"
	"github.com/hujinrun/flowspace/internal/storage/sqlite"
)

var ErrLegacySQLiteSeedAuthEnabled = errors.New("legacy sqlite seed cannot run after auth users exist")

func main() {
	runtimeConfig := config.LoadStorageConfig()
	storageConfig := storagepkg.LoadStorageConfig(runtimeConfig.Environment)

	registry := storagepkg.NewRegistry()
	if err := registry.Register(postgres.Provider{}); err != nil {
		log.Fatalf("register postgres provider: %v", err)
	}
	if err := registry.Register(sqlite.Provider{}); err != nil {
		log.Fatalf("register sqlite provider: %v", err)
	}

	startupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := registry.Open(startupCtx, storageConfig)
	if err != nil {
		log.Fatalf("open storage provider: %v", err)
	}
	defer store.Close()

	authCfg, err := config.LoadAuthConfig(runtimeConfig.Environment)
	if err != nil {
		log.Fatalf("auth config: %v", err)
	}
	bootstrapCfg := bootstrap.Config{
		AdminEmail:    authCfg.Bootstrap.Email,
		AdminPassword: authCfg.Bootstrap.Password,
		AdminName:     authCfg.Bootstrap.Name,
	}

	if storageConfig.Driver == storagepkg.DriverSQLite {
		if err := runLegacySQLiteSeed(startupCtx, store, storageConfig.SQLitePath, bootstrapCfg, repository.InitDB, repository.SeedDB); err != nil {
			log.Fatalf("legacy sqlite seed: %v", err)
		}
	}

	if err := bootstrap.EnsureAuthReady(startupCtx, store, bootstrapCfg); err != nil {
		log.Fatalf("auth bootstrap: %v", err)
	}
	if finalizer, ok := store.(interface {
		FinalizeAuthSchema(context.Context) error
	}); ok {
		state, err := bootstrap.InspectState(startupCtx, store)
		if err != nil {
			log.Fatalf("auth bootstrap state: %v", err)
		}
		if state.HasUsers {
			if err := finalizer.FinalizeAuthSchema(startupCtx); err != nil {
				log.Fatalf("auth schema finalizer: %v", err)
			}
		}
	}

	repository.SetStore(store)
	log.Printf("storage initialized env=%s driver=%s database=%s sqlite_path=%s capabilities=%+v", storageConfig.Env, storageConfig.Driver, storageConfig.Name, storageConfig.SQLitePath, store.Capabilities())
	log.Printf("database seed completed env=%s driver=%s database=%s sqlite_path=%s", storageConfig.Env, storageConfig.Driver, storageConfig.Name, storageConfig.SQLitePath)
}

func runLegacySQLiteSeed(ctx context.Context, store storagepkg.Store, sqlitePath string, bootstrapCfg bootstrap.Config, initDB func(string) error, seedDB func() error) error {
	state, err := bootstrap.InspectState(ctx, store)
	if err != nil {
		return fmt.Errorf("inspect auth state before legacy sqlite seed: %w", err)
	}
	if state.HasUsers {
		return ErrLegacySQLiteSeedAuthEnabled
	}
	if bootstrapCfg.Incomplete() {
		return bootstrap.ErrBootstrapAdminIncomplete
	}
	if !bootstrapCfg.Configured() {
		return bootstrap.ErrBootstrapAdminRequired
	}
	if _, err := auth.HashPassword(bootstrapCfg.AdminPassword); err != nil {
		return err
	}
	if err := initDB(sqlitePath); err != nil {
		return fmt.Errorf("init legacy sqlite database: %w", err)
	}
	if err := seedDB(); err != nil {
		return fmt.Errorf("seed legacy sqlite database: %w", err)
	}
	return nil
}
