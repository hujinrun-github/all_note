package main

import (
	"log"

	"github.com/hujinrun/flowspace/internal/repository"
)

func main() {
	if err := repository.InitDB("flowspace.db"); err != nil {
		log.Fatalf("failed to init database: %v", err)
	}
	if err := repository.SeedDB(); err != nil {
		log.Fatalf("failed to seed database: %v", err)
	}
	log.Println("database seeded successfully")
}
