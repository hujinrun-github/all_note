package main

import (
	"context"
	"log"
	"time"

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
	store, err := registry.Open(startupCtx, storageConfig)
	cancel()
	if err != nil {
		log.Fatalf("open storage provider: %v", err)
	}
	defer store.Close()

	repository.SetStore(store)
	log.Printf("storage initialized env=%s driver=%s database=%s sqlite_path=%s capabilities=%+v", storageConfig.Env, storageConfig.Driver, storageConfig.Name, storageConfig.SQLitePath, store.Capabilities())
	if storageConfig.Driver == storagepkg.DriverSQLite {
		if err := repository.InitDB(storageConfig.SQLitePath); err != nil {
			log.Fatalf("failed to init legacy sqlite database for seed: %v", err)
		}
		if err := repository.SeedDB(); err != nil {
			log.Fatalf("failed to seed legacy sqlite database: %v", err)
		}
	}
	log.Printf("database seed completed env=%s driver=%s database=%s sqlite_path=%s", storageConfig.Env, storageConfig.Driver, storageConfig.Name, storageConfig.SQLitePath)
}
