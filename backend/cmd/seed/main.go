package main

import (
	"log"

	"github.com/hujinrun/flowspace/internal/config"
	"github.com/hujinrun/flowspace/internal/repository"
)

func main() {
	storage := config.LoadStorageConfig()
	if err := repository.InitDB(storage.DBPath); err != nil {
		log.Fatalf("failed to init database: %v", err)
	}
	if err := repository.SeedDB(); err != nil {
		log.Fatalf("failed to seed database: %v", err)
	}
	log.Printf("database seeded successfully env=%s path=%s", storage.Environment, storage.DBPath)
}
