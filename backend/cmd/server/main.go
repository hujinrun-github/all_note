package main

import (
	"log"

	"github.com/hujinrun/flowspace/internal/repository"
	"github.com/hujinrun/flowspace/internal/router"
)

func main() {
	if err := repository.InitDB("flowspace.db"); err != nil {
		log.Fatalf("failed to init database: %v", err)
	}
	log.Println("database initialized")

	r := router.Setup()
	log.Println("server starting on :8080")
	if err := r.Run(":8080"); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
