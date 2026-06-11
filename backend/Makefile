.PHONY: dev build test lint seed

dev:
	go run ./cmd/server/main.go

build:
	CGO_ENABLED=0 go build -o bin/server ./cmd/server/main.go

test:
	go test ./internal/... -cover -count=1

lint:
	go vet ./...

seed:
	go run ./cmd/seed/main.go
