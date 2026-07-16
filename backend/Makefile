PORT ?= 4201
TEST_PORT ?= 4101
FORMAT_PATHS := \
	cmd/checkfmt \
	cmd/server/main.go \
	internal/config/native.go \
	internal/config/native_test.go \
	internal/auth/context.go \
	internal/middleware/auth.go \
	internal/handler/mobile_conflicts.go \
	internal/handler/mobile_read.go \
	internal/handler/mobile_sync.go \
	internal/handler/native.go \
	internal/handler/transcription_job.go \
	internal/mobilecontract \
	internal/mobilesync \
	internal/mobilesyncpublisher \
	internal/model/mobile_sync.go \
	internal/model/task.go \
	internal/model/transcription_job.go \
	internal/model/native.go \
	internal/model/voice_audio_cleanup.go \
	internal/router/native_routes_test.go \
	internal/router/router.go \
	internal/service/native.go \
	internal/service/native_test.go \
	internal/storage/mobile_sync.go \
	internal/storage/mobile_sync_publisher.go \
	internal/storage/transcription_job.go \
	internal/storage/native.go \
	internal/storage/voice_audio_cleanup.go \
	internal/storage/contracttest/mobile_sync_contract_tests.go \
	internal/storage/contracttest/transcription_job_contract_tests.go \
	internal/storage/sqlite/mobile_sync.go \
	internal/storage/sqlite/mobile_entity_sync.go \
	internal/storage/sqlite/mobile_conflicts.go \
	internal/storage/sqlite/mobile_sync_read.go \
	internal/storage/sqlite/mobile_sync_publisher.go \
	internal/storage/sqlite/mobile_sync_migrations.go \
	internal/storage/sqlite/mobile_sync_migrations_test.go \
	internal/storage/sqlite/provider.go \
	internal/storage/sqlite/native.go \
	internal/storage/sqlite/native_migrations.go \
	internal/storage/sqlite/events.go \
	internal/storage/sqlite/inbox.go \
	internal/storage/sqlite/recurrence.go \
	internal/storage/sqlite/search.go \
	internal/storage/sqlite/tasks.go \
	internal/storage/sqlite/transcription_job.go \
	internal/storage/sqlite/transcription_job_migrations.go \
	internal/storage/sqlite/transcription_worker.go \
	internal/storage/sqlite/voice_audio_cleanup.go \
	internal/storage/sqlite/voice_audio_cleanup_migrations.go \
	internal/storage/postgres/mobile_sync.go \
	internal/storage/postgres/mobile_entity_sync.go \
	internal/storage/postgres/mobile_conflicts.go \
	internal/storage/postgres/mobile_sync_read.go \
	internal/storage/postgres/mobile_sync_publisher.go \
	internal/storage/postgres/mobile_sync_cursor_integration_test.go \
	internal/storage/postgres/mobile_sync_http_integration_test.go \
	internal/storage/postgres/mobile_sync_migrations_test.go \
	internal/storage/postgres/provider.go \
	internal/storage/postgres/events.go \
	internal/storage/postgres/inbox.go \
	internal/storage/postgres/native.go \
	internal/storage/postgres/recurrence.go \
	internal/storage/postgres/tasks.go \
	internal/storage/postgres/transcription_job.go \
	internal/storage/postgres/transcription_worker.go \
	internal/storage/postgres/voice_audio_cleanup.go \
	internal/testsupport \
	internal/transcriptionjob \
	internal/voiceaudiocleanup \
	internal/watchprojection \
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
	FLOWSPACE_REQUIRE_POSTGRES_TESTS=true go test -p 1 ./internal/storage/postgres ./internal/migration -count=1

test-object-minio:
	FLOWSPACE_REQUIRE_MINIO_TESTS=true go test ./internal/objectstore -run '^TestMinIOAdapterRoundTripAndDeleteIdempotently$$' -count=1

test-openapi:
	go test ./internal/mobilecontract -count=1

test-phase0: format-check lint test-openapi test-unit test-race test-contract-postgres test-object-minio

lint:
	go vet ./...

seed:
	go run ./cmd/seed/main.go
