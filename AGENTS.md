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

- Endpoint: `http://192.168.1.20:19000`
- User: `tylerhu`
- Password: `123456hjr`

## Default v2 runtime

- All future local and test service starts must use the v2 task-domain pages and mobile-v2 APIs. Do not intentionally fall back to v1 unless the user explicitly requests it.
- Start the backend with both `FLOWSPACE_ENABLE_TASK_DOMAIN_V2_ROUTING=true` and `FLOWSPACE_ENABLE_MOBILE_SYNC_V2=true`.
- The frontend does not have a separate v2 port or feature flag. It selects v2 pages from `GET /api/task-domain/capabilities`; the authenticated workspace must return `model_version=v2` and `available=true`.
- Test ports remain frontend `4100` and backend `4101`. The frontend proxies `/api` to `4101`.
- The iPhone Simulator base URL remains `http://127.0.0.1:4100/`; mobile requests use `/api/mobile/v2/*` through that frontend proxy.
- After startup, verify that unauthenticated `GET /api/mobile/v2/capabilities` returns `401` rather than `404`, then verify an authenticated request returns `200` before considering the v2 service ready.
