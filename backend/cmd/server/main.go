package main

import (
	"log"
	"os"

	"github.com/hujinrun/flowspace/internal/repository"
	"github.com/hujinrun/flowspace/internal/router"
)

func main() {
	if err := repository.InitDB("flowspace.db"); err != nil {
		log.Fatalf("failed to init database: %v", err)
	}
	log.Println("database initialized")

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	r := router.Setup()
	addr := ":" + port
	log.Printf("server starting on %s", addr)
	if err := r.Run(addr); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
