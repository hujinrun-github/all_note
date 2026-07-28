# Runtime settings deployment

FlowSpace keeps authentication and workspace settings in the control database.
Workspace content uses the platform-data database until a completed storage
migration activates another concrete endpoint.

## Required database configuration

Set the two roles independently:

```text
FLOWSPACE_CONTROL_DATABASE_DRIVER=postgres
FLOWSPACE_CONTROL_DATABASE_URL=postgres://.../flowspace_control
FLOWSPACE_PLATFORM_DATA_DATABASE_DRIVER=postgres
FLOWSPACE_PLATFORM_DATA_DATABASE_URL=postgres://.../flowspace_prod
```

`FLOWSPACE_CONTROL_DATABASE_URL` is never imported as a user-selectable data
profile. Changing the platform-data URL creates a new system profile version;
it does not silently move existing workspace bindings.

Docker Compose runs `flowspace-admin migrate-control`, the optional idempotent
tenant adoption step, and `flowspace-admin migrate-tenant` before starting the
HTTP server. Existing mixed-schema tenants must configure both
`FLOWSPACE_TENANT_ADOPT_MANIFEST_ID` and
`FLOWSPACE_TENANT_ADOPT_MANIFEST_CHECKSUM`; leaving both empty skips adoption.
Outside Compose, run the same maintenance commands explicitly after every
upgrade. The server verifies the migration history and checksums and refuses to
start on an incomplete or modified schema.

## Task-domain v2 routing gate

The unified Project → Task → Schedule → Occurrence model is opt-in at the
server boundary:

```text
FLOWSPACE_ENABLE_TASK_DOMAIN_V2_ROUTING=false
```

Leave this flag disabled until every target tenant endpoint has the verified
`0003_task_domain_legacy_migration.sql` tenant schema and the selected canary
workspace has completed the snapshot, replay, drain, reconcile, and CAS
cutover workflow. Enabling the flag does not migrate data or run DDL.

When enabled, the server resolves the durable model independently for every
authenticated workspace. Only an explicit stable `legacy + idle` state may
use legacy handlers; only `v2 + idle/cutover` may use the v2 runtime. Migration
states, epoch divergence, an unavailable endpoint, or a checksum mismatch fail
closed and never fall back to legacy writes. The frontend reads
`GET /api/task-domain/capabilities` to select the matching UI for that
workspace.

After `v2_first_write_at` is set, an application rollback must keep this flag
enabled and must continue using the v2 schema. It is not a data rollback
switch. Disable it only before the first v2 business write or after explicitly
returning the workspace to a stable legacy state through the migration
recovery procedure.

The schema deploy does not perform a workspace data cutover. Inspect one
workspace with:

```text
flowspace-admin task-migration-status --workspace-id <workspace-id>
```

Run the durable snapshot/replay/drain/reconcile coordinator to `legacy/ready`
with an explicit stable migration id and frozen IANA timezone:

```text
flowspace-admin task-migration-run-to-ready \
  --workspace-id <workspace-id> \
  --migration-id <migration-id> \
  --migration-timezone Asia/Shanghai \
  --confirm-fence-legacy-writes
```

`task-migration-run-to-ready` closes the legacy task write fence during its
drain phase. It deliberately does not perform the final model-version CAS.
GitHub's `Task Domain V2 Migration` workflow exposes the same operation as a
manual, per-workspace action and requires the
`RUN_TO_READY_AND_FENCE_WRITES` confirmation. Do not run the mutating action
until the final cutover command and mobile-v2 service are deployed; use its
`status` action freely for read-only inspection.

## Credential keyring

Create the ignored `secrets/credentials-keyring.json` described in
[`secrets/README.md`](../secrets/README.md). Configure the active id with
`FLOWSPACE_CREDENTIALS_ACTIVE_KEY_ID`. Never put key material in environment
examples, logs, Git, or the control database.

## Private service allowlist

User-configured PostgreSQL, MinIO, FunASR, and AI endpoints are subject to the
outbound SSRF policy. Permit only exact private destinations required by the
deployment:

```text
FLOWSPACE_ALLOWED_PRIVATE_CIDRS=192.168.1.13/32,192.168.1.20/32
```

The dialer resolves and validates every physical connection. Environment proxy
variables and unsafe redirects are not used. Loopback, link-local, multicast,
and unspecified addresses remain blocked even if a broad private range is
listed. In Compose, use the reachable service address and the narrowest bridge
network CIDR possible rather than `127.0.0.1`.

## Compromised runtime artifacts

If a keyring, SQLite/WAL file, or cookie file was committed, deleting it in a
later commit is not sufficient. Rotate the keyring, revoke affected application
sessions and OAuth grants, and decide whether repository visibility requires a
coordinated history rewrite. Treat old clones and build caches as still holding
the compromised material.
