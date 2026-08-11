PORT ?= 4201
FRONTEND_PORT ?= 4200
DB_ENV ?= prod
DB_PATH ?=
TEST_PORT ?= 4101
TEST_FRONTEND_PORT ?= 4100
TAILSCALE_FRONTEND_PORT ?= 4200
TAILSCALE_BACKEND_HOST ?= [::1]
TAILSCALE_BASE ?= /all-note/
TAILSCALE_SERVE_PATH ?= /all-note
TEST_TAILSCALE_FRONTEND_PORT ?= 4100
TEST_TAILSCALE_BACKEND_HOST ?= 127.0.0.1
TEST_TAILSCALE_BASE ?= /all-note-test/
TEST_TAILSCALE_SERVE_PATH ?= /all-note-test
TEST_DB_PATH ?=
BACKEND_CMD ?=
FRONTEND_CMD ?=
MACOS_DERIVED_DATA_PATH ?= $(CURDIR)/.codex-run/macos-derived-data
MACOS_RELEASE_OUTPUT_DIR ?= $(CURDIR)/dist/macos
MACOS_VERSION_SETTINGS =
ifneq ($(strip $(FLOWSPACE_MARKETING_VERSION)),)
MACOS_VERSION_SETTINGS += MARKETING_VERSION='$(FLOWSPACE_MARKETING_VERSION)'
endif
ifneq ($(strip $(FLOWSPACE_BUILD_NUMBER)),)
MACOS_VERSION_SETTINGS += CURRENT_PROJECT_VERSION='$(FLOWSPACE_BUILD_NUMBER)'
endif
ifneq ($(strip $(FLOWSPACE_UPDATE_FEED_URL)),)
MACOS_VERSION_SETTINGS += FLOWSPACE_UPDATE_FEED_URL='$(FLOWSPACE_UPDATE_FEED_URL)'
endif
ifneq ($(strip $(FLOWSPACE_UPDATE_PUBLIC_ED_KEY)),)
MACOS_VERSION_SETTINGS += FLOWSPACE_UPDATE_PUBLIC_ED_KEY='$(FLOWSPACE_UPDATE_PUBLIC_ED_KEY)'
endif

START_FLAGS = --env $(DB_ENV) --backend-port $(PORT) --frontend-port $(FRONTEND_PORT)
TAILSCALE_FLAGS = --env $(DB_ENV) --proxy-host $(TAILSCALE_BACKEND_HOST) --backend-port $(PORT) --frontend-port $(TAILSCALE_FRONTEND_PORT) --frontend-base $(TAILSCALE_BASE)
TEST_FLAGS = --env test --backend-port $(TEST_PORT) --frontend-port $(TEST_FRONTEND_PORT)
TEST_TAILSCALE_FLAGS = --env test --proxy-host $(TEST_TAILSCALE_BACKEND_HOST) --backend-port $(TEST_PORT) --frontend-port $(TEST_TAILSCALE_FRONTEND_PORT) --frontend-base $(TEST_TAILSCALE_BASE)
ifneq ($(strip $(DB_PATH)),)
START_FLAGS += --db "$(DB_PATH)"
TAILSCALE_FLAGS += --db "$(DB_PATH)"
endif
ifneq ($(strip $(TEST_DB_PATH)),)
TEST_FLAGS += --db "$(TEST_DB_PATH)"
TEST_TAILSCALE_FLAGS += --db "$(TEST_DB_PATH)"
endif
ifneq ($(strip $(BACKEND_CMD)),)
START_FLAGS += --backend-cmd "$(BACKEND_CMD)"
TAILSCALE_FLAGS += --backend-cmd "$(BACKEND_CMD)"
TEST_FLAGS += --backend-cmd "$(BACKEND_CMD)"
TEST_TAILSCALE_FLAGS += --backend-cmd "$(BACKEND_CMD)"
endif
ifneq ($(strip $(FRONTEND_CMD)),)
START_FLAGS += --frontend-cmd "$(FRONTEND_CMD)"
TAILSCALE_FLAGS += --frontend-cmd "$(FRONTEND_CMD)"
TEST_FLAGS += --frontend-cmd "$(FRONTEND_CMD)"
TEST_TAILSCALE_FLAGS += --frontend-cmd "$(FRONTEND_CMD)"
endif

.PHONY: dev dev-prod dev-test kill kill-prod kill-test start-backend start-frontend start-prod-backend start-prod-frontend start-test-backend start-test-frontend start-tailscale-frontend kill-tailscale-frontend serve-tailscale start-test-tailscale-frontend kill-test-tailscale-frontend serve-test-tailscale serve-all-tailscale macos-editor macos-generate macos-build macos-test macos-ui-test macos-test-all macos-release macos-release-verify macos-distribute

dev: dev-test

dev-prod:
	node scripts/start-flowspace.mjs $(START_FLAGS)

dev-test:
	node scripts/start-flowspace.mjs $(TEST_FLAGS)

kill: kill-test

kill-prod:
	node scripts/start-flowspace.mjs $(START_FLAGS) --kill-only

kill-test:
	node scripts/start-flowspace.mjs $(TEST_FLAGS) --kill-only

start-backend: start-test-backend

start-frontend: start-test-frontend

start-prod-backend:
	node scripts/start-flowspace.mjs $(START_FLAGS) --backend-only

start-prod-frontend:
	node scripts/start-flowspace.mjs $(START_FLAGS) --frontend-only

start-test-backend:
	node scripts/start-flowspace.mjs $(TEST_FLAGS) --backend-only

start-test-frontend:
	node scripts/start-flowspace.mjs $(TEST_FLAGS) --frontend-only

start-tailscale-frontend:
	node scripts/start-flowspace.mjs $(TAILSCALE_FLAGS) --frontend-only

kill-tailscale-frontend:
	node scripts/start-flowspace.mjs $(TAILSCALE_FLAGS) --frontend-only --kill-only

serve-tailscale:
	tailscale funnel --bg --yes --set-path $(TAILSCALE_SERVE_PATH) http://127.0.0.1:$(TAILSCALE_FRONTEND_PORT)$(TAILSCALE_SERVE_PATH)

start-test-tailscale-frontend:
	node scripts/start-flowspace.mjs $(TEST_TAILSCALE_FLAGS) --frontend-only

kill-test-tailscale-frontend:
	node scripts/start-flowspace.mjs $(TEST_TAILSCALE_FLAGS) --frontend-only --kill-only

serve-test-tailscale:
	tailscale funnel --bg --yes --set-path $(TEST_TAILSCALE_SERVE_PATH) http://127.0.0.1:$(TEST_TAILSCALE_FRONTEND_PORT)$(TEST_TAILSCALE_SERVE_PATH)

serve-all-tailscale: serve-tailscale serve-test-tailscale

macos-editor:
	frontend/node_modules/.bin/vite build --config macos/EditorWeb/vite.config.mjs

macos-generate: macos-editor
	cd macos && xcodegen generate

macos-build: macos-generate
	cd macos && xcodebuild -project FlowSpaceMac.xcodeproj -scheme FlowSpaceMac -configuration Debug -destination 'platform=macOS' CODE_SIGNING_ALLOWED=NO build

macos-test: macos-generate
	cd macos && xcodebuild -project FlowSpaceMac.xcodeproj -scheme FlowSpaceMacUnitTests -configuration Debug -destination 'platform=macOS' test

macos-ui-test: macos-generate
	cd macos && xcodebuild -project FlowSpaceMac.xcodeproj -scheme FlowSpaceMacUITests -configuration Debug -destination 'platform=macOS' test

macos-test-all: macos-test macos-ui-test

macos-release: macos-generate
	cd macos && xcodebuild -project FlowSpaceMac.xcodeproj -scheme FlowSpaceMac -configuration Release -destination 'generic/platform=macOS' -derivedDataPath '$(MACOS_DERIVED_DATA_PATH)' ARCHS='arm64 x86_64' ONLY_ACTIVE_ARCH=NO CODE_SIGNING_ALLOWED=NO $(MACOS_VERSION_SETTINGS) build

macos-release-verify: macos-release
	scripts/verify-macos-app.sh '$(MACOS_DERIVED_DATA_PATH)/Build/Products/Release/FlowSpaceMac.app'

macos-distribute:
	@test -n "$(FLOWSPACE_DEVELOPER_ID_APPLICATION)" || (echo "FLOWSPACE_DEVELOPER_ID_APPLICATION is required" >&2; exit 2)
	@test -n "$(FLOWSPACE_NOTARY_PROFILE)" || (echo "FLOWSPACE_NOTARY_PROFILE is required" >&2; exit 2)
	@test -n "$(FLOWSPACE_UPDATE_FEED_URL)" || (echo "FLOWSPACE_UPDATE_FEED_URL is required" >&2; exit 2)
	@test -n "$(FLOWSPACE_UPDATE_PUBLIC_ED_KEY)" || (echo "FLOWSPACE_UPDATE_PUBLIC_ED_KEY is required" >&2; exit 2)
	$(MAKE) macos-release
	FLOWSPACE_RELEASE_OUTPUT_DIR='$(MACOS_RELEASE_OUTPUT_DIR)' scripts/package-macos-release.sh '$(MACOS_DERIVED_DATA_PATH)/Build/Products/Release/FlowSpaceMac.app'
