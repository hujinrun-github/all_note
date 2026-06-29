package main

import (
	"context"
	"log"
	"time"

	"github.com/hujinrun/flowspace/internal/bootstrap"
	"github.com/hujinrun/flowspace/internal/config"
	"github.com/hujinrun/flowspace/internal/repository"
	storagepkg "github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/storage/postgres"
	"github.com/hujinrun/flowspace/internal/storage/sqlite"
)

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

	if storageConfig.Driver == storagepkg.DriverSQLite {
		if err := repository.InitDB(storageConfig.SQLitePath); err != nil {
			log.Fatalf("failed to init legacy sqlite database for seed: %v", err)
		}
		if err := repository.SeedDB(); err != nil {
			log.Fatalf("failed to seed legacy sqlite database: %v", err)
		}
	}

	authCfg, err := config.LoadAuthConfig(runtimeConfig.Environment)
	if err != nil {
		log.Fatalf("auth config: %v", err)
	}
	bootstrapCfg := bootstrap.Config{
		AdminEmail:    authCfg.Bootstrap.Email,
		AdminPassword: authCfg.Bootstrap.Password,
		AdminName:     authCfg.Bootstrap.Name,
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
