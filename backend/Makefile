PORT ?= 4201
TEST_PORT ?= 4101

.PHONY: dev dev-test dev-prod build test lint seed

dev: dev-test

dev-test:
	FLOWSPACE_ENV=test PORT=$(TEST_PORT) go run ./cmd/server/main.go

dev-prod:
	FLOWSPACE_ENV=prod PORT=$(PORT) go run ./cmd/server/main.go

build:
	CGO_ENABLED=0 go build -o bin/server ./cmd/server/main.go

test:
	go test ./internal/... -cover -count=1

lint:
	go vet ./...

seed:
	go run ./cmd/seed/main.go
