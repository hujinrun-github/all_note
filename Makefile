PORT ?= 8080
FRONTEND_PORT ?= 5199
DB_ENV ?= prod
DB_PATH ?=
TEST_PORT ?= 18080
TEST_FRONTEND_PORT ?= 15199
TEST_TAILSCALE_FRONTEND_PORT ?= 15198
TEST_TAILSCALE_BASE ?= /all-note-test/
TEST_TAILSCALE_SERVE_PATH ?= /all-note-test
TEST_DB_PATH ?=
BACKEND_CMD ?=
FRONTEND_CMD ?=

START_FLAGS = --env $(DB_ENV) --backend-port $(PORT) --frontend-port $(FRONTEND_PORT)
TEST_FLAGS = --env test --backend-port $(TEST_PORT) --frontend-port $(TEST_FRONTEND_PORT)
TEST_TAILSCALE_FLAGS = --env test --backend-port $(TEST_PORT) --frontend-port $(TEST_TAILSCALE_FRONTEND_PORT) --frontend-base $(TEST_TAILSCALE_BASE)
ifneq ($(strip $(DB_PATH)),)
START_FLAGS += --db "$(DB_PATH)"
endif
ifneq ($(strip $(TEST_DB_PATH)),)
TEST_FLAGS += --db "$(TEST_DB_PATH)"
TEST_TAILSCALE_FLAGS += --db "$(TEST_DB_PATH)"
endif
ifneq ($(strip $(BACKEND_CMD)),)
START_FLAGS += --backend-cmd "$(BACKEND_CMD)"
TEST_FLAGS += --backend-cmd "$(BACKEND_CMD)"
TEST_TAILSCALE_FLAGS += --backend-cmd "$(BACKEND_CMD)"
endif
ifneq ($(strip $(FRONTEND_CMD)),)
START_FLAGS += --frontend-cmd "$(FRONTEND_CMD)"
TEST_FLAGS += --frontend-cmd "$(FRONTEND_CMD)"
TEST_TAILSCALE_FLAGS += --frontend-cmd "$(FRONTEND_CMD)"
endif

.PHONY: dev dev-prod dev-test kill kill-test start-backend start-frontend start-test-backend start-test-frontend start-test-tailscale-frontend kill-test-tailscale-frontend serve-test-tailscale

dev:
	node scripts/start-flowspace.mjs $(START_FLAGS)

dev-prod: dev

dev-test:
	node scripts/start-flowspace.mjs $(TEST_FLAGS)

kill:
	node scripts/start-flowspace.mjs $(START_FLAGS) --kill-only

kill-test:
	node scripts/start-flowspace.mjs $(TEST_FLAGS) --kill-only

start-backend:
	node scripts/start-flowspace.mjs $(START_FLAGS) --backend-only

start-frontend:
	node scripts/start-flowspace.mjs $(START_FLAGS) --frontend-only

start-test-backend:
	node scripts/start-flowspace.mjs $(TEST_FLAGS) --backend-only

start-test-frontend:
	node scripts/start-flowspace.mjs $(TEST_FLAGS) --frontend-only

start-test-tailscale-frontend:
	node scripts/start-flowspace.mjs $(TEST_TAILSCALE_FLAGS) --frontend-only

kill-test-tailscale-frontend:
	node scripts/start-flowspace.mjs $(TEST_TAILSCALE_FLAGS) --frontend-only --kill-only

serve-test-tailscale:
	tailscale funnel --bg --yes --set-path $(TEST_TAILSCALE_SERVE_PATH) http://127.0.0.1:$(TEST_TAILSCALE_FRONTEND_PORT)$(TEST_TAILSCALE_SERVE_PATH)
