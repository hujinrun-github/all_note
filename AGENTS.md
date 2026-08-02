# Project Memory

## Service connection notes

### PostgreSQL

- Current host: `192.168.1.13`
- Port: `19588`
- User: `postgres`
- Password: `12345`
- Command: `psql.exe -h 192.168.1.13 -p 19588 -U postgres`
- Important: `192.168.1.70` and `192.168.1.20` are old PostgreSQL hosts and must not be used.

### MinIO

- Endpoint: `http://192.168.1.13:19000`
- Test bucket: `flowspace-test`
- User: `tylerhu`
- Password: `123456hjr`

## Default v2 runtime

- All future local and test service starts must use the v2 task-domain pages and mobile-v2 APIs. Do not intentionally fall back to v1 unless the user explicitly requests it.
- Start the backend with both `FLOWSPACE_ENABLE_TASK_DOMAIN_V2_ROUTING=true` and `FLOWSPACE_ENABLE_MOBILE_SYNC_V2=true`.
- The frontend does not have a separate v2 port or feature flag. It selects v2 pages from `GET /api/task-domain/capabilities`; the authenticated workspace must return `model_version=v2` and `available=true`.
- Test ports remain frontend `4100` and backend `4101`. The frontend proxies `/api` to `4101`.
- The iPhone Simulator base URL remains `http://127.0.0.1:4100/`; mobile requests use `/api/mobile/v2/*` through that frontend proxy.
- After startup, verify that unauthenticated `GET /api/mobile/v2/capabilities` returns `401` rather than `404`, then verify an authenticated request returns `200` before considering the v2 service ready.

## Standard test-machine startup

- Use this startup layout for all future test-machine runs unless the user explicitly requests a different environment.
- Run the backend from `backend/` with the current source or a freshly built binary. Do not reuse an older binary after storage, authentication, migration, or runtime changes.
- Use the SQLite control database `backend/flowspace-control-v2.test.db` and SQLite platform/tenant database `backend/flowspace.test.db`. The bootstrap test workspace does not use PostgreSQL for its runtime data.
- The canonical authenticated test workspace is `workspace_bootstrap_admin`. It has already completed the irreversible v2 cutover. On normal starts, verify it is `model_version=v2`, `migration_state=cutover`, and `accept_legacy_writes=0`; do not rerun the cutover command. If this state is missing, stop and diagnose instead of falling back to v1.
- Start the backend on `4101` with these settings:
  - `FLOWSPACE_ENV=test`
  - `PORT=4101`
  - `FLOWSPACE_INSTANCE_MODE=single`
  - `FLOWSPACE_CONTROL_DATABASE_DRIVER=sqlite`
  - `FLOWSPACE_CONTROL_SQLITE_PATH=<repo>/backend/flowspace-control-v2.test.db`
  - `FLOWSPACE_PLATFORM_DATA_DATABASE_DRIVER=sqlite`
  - `FLOWSPACE_PLATFORM_DATA_SQLITE_PATH=<repo>/backend/flowspace.test.db`
  - `FLOWSPACE_CREDENTIALS_ACTIVE_KEY_ID=runtime-test`
  - `FLOWSPACE_CREDENTIALS_KEYRING_FILE=<repo>/.codex-run/runtime-test-keyring.json`
  - `FLOWSPACE_ALLOWED_PRIVATE_CIDRS=192.168.1.13/32`
  - `FLOWSPACE_ALLOWED_ORIGINS=http://127.0.0.1:4100,http://localhost:4100`
  - `FLOWSPACE_COOKIE_SECURE=false`
  - `FLOWSPACE_ENABLE_MOBILE_SYNC_V1=false`
  - `FLOWSPACE_ENABLE_TASK_DOMAIN_V2_ROUTING=true`
  - `FLOWSPACE_ENABLE_MOBILE_SYNC_V2=true`
  - Set the MinIO endpoint, access key, secret key, and bucket from the MinIO connection notes above.
  - Reuse `FLOWSPACE_SESSION_SECRET` from `.codex-run/flowspace.local.env`, but do not source that file wholesale because it can contain stale v1 or database settings.
- Start the frontend from `frontend/` on `127.0.0.1:4100` with `VITE_BACKEND_HOST=127.0.0.1` and `VITE_BACKEND_PORT=4101`. The iPhone Simulator continues to use `http://127.0.0.1:4100/`.
- Authentication and active sessions belong to the control database during this v2 startup. Do not diagnose login state only from `backend/flowspace.test.db`.
- The `workspace_bootstrap_admin` `object_s3` binding must resolve to provider `minio`, endpoint `http://192.168.1.13:19000`, and bucket `flowspace-test`. A bucket existing in MinIO is not sufficient if the workspace binding still points to an `unavailable` profile.
- Readiness checklist:
  1. `GET http://127.0.0.1:4100/api/health` returns `200`.
  2. Unauthenticated `GET http://127.0.0.1:4101/api/mobile/v2/capabilities` returns `401`, not `404`.
  3. An authenticated capabilities request through `4100` returns `200`, `workspace_id=workspace_bootstrap_admin`, `task_model_version=2`, and `workspace_mode=v2-active`.
  4. Runtime settings show the MinIO `object_s3` binding.
  5. Launch the iPhone Simulator app and confirm it reaches the workspace with a recent successful sync instead of showing “登录响应中没有可用工作区”。
- Keychain/session injection was a one-time recovery technique and is not part of the standard startup. Future runs should preserve a valid control-database session or use the normal login flow.
