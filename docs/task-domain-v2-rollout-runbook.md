# Task-domain v2 rollout runbook

This runbook covers the server-side Project -> Task -> Schedule -> Occurrence
cutover. It does not cover a mobile UI migration and it never authorizes a
production cutover by itself.

## Safety boundary

- Keep `FLOWSPACE_ENABLE_TASK_DOMAIN_V2_ROUTING=false` while schemas are being
  installed and while legacy data is copied.
- Run control and tenant migrations explicitly. Opening a request runtime must
  never run DDL.
- Operate on one workspace at a time. The workspace id, migration id, source
  watermark, write epoch, and expected state revision are mandatory command
  inputs; do not infer them from a process cache.
- A failed endpoint, epoch mismatch, transitional migration state, or stale
  revision is a stop condition. Never fall back to the legacy write source.
- Do not archive legacy tables during the cutover release.

## Deployment prerequisites

1. Deploy an application version that understands both the legacy and v2
   schemas, but leave task-domain v2 routing disabled.
2. Run `flowspace-admin migrate-control` against the control database.
3. Run `flowspace-admin migrate-tenant` for each concrete tenant endpoint. For
   an existing tenant, complete its versioned adopt procedure before migration.
4. Verify the tenant migration checksums, the local workspace anchor, and
   `workspace_task_domain_state` before accepting traffic.
5. Confirm every old server instance has the writer-protocol heartbeat required
   by the drain gate. An instance that does not understand `write_epoch` must be
   removed before cutover.
6. Enable `FLOWSPACE_ENABLE_TASK_DOMAIN_V2_ROUTING=true`. This only enables
   durable per-workspace routing; it does not change any workspace model.

## Canary workflow

Use an internal workspace with representative projects, one-time and recurring
tasks, all-day and time-block events, Roadmap links, completed occurrences, and
at least one active mobile-v1 session.

For every transition, persist the result before starting the next step:

1. **Preflight**: inventory project/task/rule/occurrence/event source tables,
   choose and record the migration timezone, install the complete outbox
   manifest, and reject unsupported or ambiguous source rows.
2. **Snapshot**: enter `backfilling`, record the snapshot sequence, and project
   the consistent source snapshot to v2 using deterministic id mappings.
3. **Replay**: enter `catching_up`, replay after-images and tombstones in source
   sequence order, and persist the replay watermark after each committed page.
4. **Drain**: increment `write_epoch`, close `accept_legacy_writes`, disable the
   mobile-v1 task read/write/watch scopes, and wait for old-epoch transactions
   and old-writer heartbeats to reach zero.
5. **Reconcile**: compare source and v2 in both directions. A row count alone is
   not sufficient; validate ids, revisions, status, schedule timing, and delete
   tombstones. Apply a repair plan, then observe again until the plan is empty.
6. **Cutover CAS**: with the expected migration id, state revision, and epoch,
   atomically change the workspace model to v2. Exactly one coordinator may
   succeed.
7. **Read verification**: require the capability endpoint to return
   `model_version=v2` and `available=true`; verify Projects, Tasks, Today,
   Calendar, and the compatibility reads before allowing a business write.
8. **First write boundary**: the first successful v2 business write records
   `v2_first_write_at`. After this timestamp, data-layer rollback to legacy is
   forbidden.

## Required go/no-go observations

Stop the batch if any workspace reports one of the following:

- outbox or replay lag;
- active legacy transactions or old-writer heartbeats;
- legacy writes still enabled after drain;
- tenant anchor epoch different from the durable task-domain epoch;
- source/v2 forward or reverse differences;
- pending reconcile mutations;
- generation jobs in `retry_pending` or `failed`, or a generation watermark
  behind the expected recurrence horizon;
- mobile-v1 task scopes still accepting snapshot, changes, watch, or mutation;
- an application instance that cannot serve the v2 schema.

Record snapshot duration, replay duration, drain lock wait, outbox lag,
generation lag, reconcile repair count, adapter request count, and all stable
failure codes for every canary.

## Recovery and rollback

- Before the cutover CAS, resume from the durable phase and watermark. Never
  discard the migration id or start a second migration for the same workspace.
- If the cutover CAS succeeded but no v2 business write has occurred, the
  maintenance recovery command may CAS the workspace back to a stable legacy
  state after the same fences and observations pass.
- If `v2_first_write_at` is set, roll back only the application binary to a
  version that continues to read and write v2. Do not reopen legacy writes.
- Failure after CAS but before the coordinator response is handled by an
  idempotent retry with the same migration id; it is not evidence that the CAS
  failed.

## Stabilization and archive

Keep the compatibility adapter and legacy tables for at least one complete
release cycle. During stabilization, legacy tables remain protected by the
database write fence. Archive/read-only conversion is a later change with its
own migration; physical deletion and mobile-v2 are separate design efforts.

