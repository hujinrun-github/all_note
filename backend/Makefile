PORT ?= 4201
TEST_PORT ?= 4101
FORMAT_PATHS := \
	cmd/checkfmt \
	cmd/server/main.go \
	internal/config/native.go \
	internal/config/native_test.go \
	internal/handler/mobile_sync.go \
	internal/handler/transcription_job.go \
	internal/mobilecontract \
	internal/mobilesync \
	internal/model/mobile_sync.go \
	internal/model/transcription_job.go \
	internal/router/native_routes_test.go \
	internal/router/router.go \
	internal/storage/mobile_sync.go \
	internal/storage/transcription_job.go \
	internal/storage/contracttest/mobile_sync_contract_tests.go \
	internal/storage/contracttest/transcription_job_contract_tests.go \
	internal/storage/sqlite/mobile_sync.go \
	internal/storage/sqlite/mobile_sync_migrations.go \
	internal/storage/sqlite/mobile_sync_migrations_test.go \
	internal/storage/sqlite/transcription_job.go \
	internal/storage/sqlite/transcription_job_migrations.go \
	internal/storage/sqlite/transcription_worker.go \
	internal/storage/postgres/mobile_sync.go \
	internal/storage/postgres/mobile_sync_http_integration_test.go \
	internal/storage/postgres/mobile_sync_migrations_test.go \
	internal/storage/postgres/transcription_job.go \
	internal/storage/postgres/transcription_worker.go \
	internal/testsupport \
	internal/transcriptionjob \
	internal/objectstore/minio_integration_test.go \
	internal/storage/postgres/testdb_test.go \
	internal/migration/sqlite_to_pg_test.go

.PHONY: dev dev-test dev-prod build test lint seed format-check test-unit test-race test-contract-postgres test-object-minio test-openapi test-phase0

dev: dev-test

dev-test:
	FLOWSPACE_ENV=test PORT=$(TEST_PORT) go run ./cmd/server/main.go

dev-prod:
	FLOWSPACE_ENV=prod PORT=$(PORT) go run ./cmd/server/main.go

build:
	CGO_ENABLED=0 go build -o bin/server ./cmd/server/main.go

test: test-unit

format-check:
	go run ./cmd/checkfmt $(FORMAT_PATHS)

test-unit:
	go test ./internal/... -cover -count=1

test-race:
	go test -race ./internal/... -count=1

test-contract-postgres:
	FLOWSPACE_REQUIRE_POSTGRES_TESTS=true go test ./internal/storage/postgres ./internal/migration -count=1

test-object-minio:
	FLOWSPACE_REQUIRE_MINIO_TESTS=true go test ./internal/objectstore -run '^TestMinIOAdapterRoundTripAndDeleteIdempotently$$' -count=1

test-openapi:
	go test ./internal/mobilecontract -count=1

test-phase0: format-check lint test-openapi test-unit test-race test-contract-postgres test-object-minio

lint:
	go vet ./...

seed:
	go run ./cmd/seed/main.go
