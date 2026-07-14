# Single Note Sync Target Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make each FlowSpace note bind to at most one sync target, keep unbound notes unsynced by default, and make Notion/Obsidian push, pull, import, deletion, and target management respect that binding.

**Architecture:** Add binding/claim/tombstone tables to the pluggable storage providers, expose binding-aware repository methods through `storage.Store`, route all cross-note/sync flows through `Store.Transact`, then update handlers and frontend UI to select one target name per note instead of independent Notion/Obsidian toggles.

**Tech Stack:** Go backend, `storage.Store` provider abstraction, PostgreSQL, SQLite, React/Vitest frontend.

---

## Reference Spec

Implement the approved design in [2026-06-18-single-note-sync-target-design.md](../specs/2026-06-18-single-note-sync-target-design.md). Treat that spec as the source of truth for API semantics and database invariants. This plan narrows execution order and verification.

## Non-Negotiable Rules

- Default note sync behavior is **not synced** unless a `note_sync_bindings` row exists.
- A note can have only one active binding: `note_sync_bindings.note_id` is the primary key.
- A claim must be constrained to the current binding: `sync_external_claims(note_id, target_id)` references `note_sync_bindings(note_id, target_id)` with `ON DELETE CASCADE`.
- Explicit unbind and note deletion must create tombstone records before releasing claims or deleting notes.
- Pull/import must not auto-bind a note that was explicitly unbound; return a conflict or `binding_required` response.
- Target identity fields cannot be edited after bindings, claims, or sync history exist.
- Binding switches, imports, note updates, state writes, claim writes, suppression writes, and tombstone writes must use `Store.Transact`.
- Query compatibility endpoints may return `200` with mismatch flags; execution endpoints return `409` for mismatches.
- The first schema/data migration must generate initial bindings and claims before services switch to "push only bound notes".

## Task 1: Add Domain Models and Storage Contract Tests

- [ ] Add model types in `backend/internal/model/sync.go`:
  - `SyncTarget.IsDefault bool`
  - `NoteSyncBinding`
  - `SyncExternalClaim`
  - `NoteSyncSuppression`
  - `SyncImportTombstone`
  - request/response structs for binding APIs, target-scoped deletion APIs, and compatibility flags.
- [ ] Extend `backend/internal/storage/store.go` `SyncRepository` with these methods:
  ```go
  GetTarget(ctx context.Context, targetID string) (*model.SyncTarget, error)
  DeleteTarget(ctx context.Context, targetID string) error
  CountBindingsByTarget(ctx context.Context, targetID string) (int, error)
  CountClaimsByTarget(ctx context.Context, targetID string) (int, error)
  CountStatesByTarget(ctx context.Context, targetID string) (int, error)
  GetBinding(ctx context.Context, noteID string) (*model.NoteSyncBinding, error)
  PutBinding(ctx context.Context, binding model.NoteSyncBinding) error
  DeleteBinding(ctx context.Context, noteID string) error
  ListBindingsByTarget(ctx context.Context, targetID string) ([]model.NoteSyncBinding, error)
  GetExternalClaim(ctx context.Context, externalKey string) (*model.SyncExternalClaim, error)
  GetExternalClaimByNote(ctx context.Context, noteID string) (*model.SyncExternalClaim, error)
  PutExternalClaim(ctx context.Context, claim model.SyncExternalClaim) error
  ReleaseExternalClaim(ctx context.Context, noteID string) error
  PutSuppression(ctx context.Context, suppression model.NoteSyncSuppression) error
  DeleteSuppression(ctx context.Context, noteID string, targetID string) error
  GetSuppression(ctx context.Context, noteID string, targetID string) (*model.NoteSyncSuppression, error)
  PutImportTombstone(ctx context.Context, tombstone model.SyncImportTombstone) error
  DeleteImportTombstone(ctx context.Context, externalKey string) error
  DeleteImportTombstonesForNoteTarget(ctx context.Context, noteID string, targetID string) error
  FindImportTombstone(ctx context.Context, targetID string, externalKey string, formerNoteID string, externalType string) (*model.SyncImportTombstone, error)
  ```
- [ ] Write red contract tests in `backend/internal/storage/contracttest/sync_binding_contract_tests.go`:
  - `TestSyncBindingContractAllowsOneBindingPerNote`
  - `TestSyncBindingContractClaimRequiresCurrentBinding`
  - `TestSyncBindingContractBindingDeleteCascadesClaim`
  - `TestSyncBindingContractTombstoneSurvivesNoteDelete`
  - `TestSyncBindingContractFindsTombstoneAfterExternalRename`
  - `TestSyncBindingContractOneDefaultTargetPerType`
  - `TestSyncBindingContractDefaultTargetDoesNotUseUpdatedAtFallback`
  - `TestSyncBindingContractTargetIdentityLockCounts`
- [ ] Register the new contract suite for both PostgreSQL and SQLite provider tests.
- [ ] Run and confirm expected failures:
  ```powershell
  Set-Location D:\MyGitProject\all_note\backend
  go test ./internal/storage/contracttest ./internal/storage/postgres ./internal/storage/sqlite
  ```
- [ ] Commit after the red tests are present:
  ```powershell
  git add backend/internal/model/sync.go backend/internal/storage/store.go backend/internal/storage/contracttest
  git commit -m "test: define single sync target storage contract"
  ```

## Task 2: Add Provider Schema and Repository Implementations

- [ ] Update PostgreSQL migration `backend/db/migrations/postgres/0001_init_postgres.sql`:
  ```sql
  ALTER TABLE sync_targets
    ADD COLUMN IF NOT EXISTS is_default BOOLEAN NOT NULL DEFAULT false;

  CREATE UNIQUE INDEX IF NOT EXISTS sync_targets_one_default_per_type_idx
    ON sync_targets (type)
    WHERE is_default = true;

  CREATE TABLE IF NOT EXISTS note_sync_bindings (
    note_id TEXT PRIMARY KEY REFERENCES notes(id) ON DELETE CASCADE,
    target_id TEXT NOT NULL REFERENCES sync_targets(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (note_id, target_id)
  );

  CREATE TABLE IF NOT EXISTS sync_external_claims (
    external_key TEXT PRIMARY KEY,
    note_id TEXT NOT NULL,
    target_id TEXT NOT NULL,
    external_type TEXT NOT NULL CHECK (external_type IN ('obsidian','notion')),
    external_id TEXT NOT NULL DEFAULT '',
    external_path TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (note_id),
    FOREIGN KEY (note_id, target_id)
      REFERENCES note_sync_bindings(note_id, target_id)
      ON DELETE CASCADE
  );

  CREATE TABLE IF NOT EXISTS note_sync_suppressions (
    note_id TEXT NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
    target_id TEXT NOT NULL REFERENCES sync_targets(id) ON DELETE CASCADE,
    reason TEXT NOT NULL CHECK (reason IN ('user_unbound','target_changed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (note_id, target_id)
  );

  CREATE TABLE IF NOT EXISTS sync_import_tombstones (
    external_key TEXT PRIMARY KEY,
    target_id TEXT NOT NULL REFERENCES sync_targets(id) ON DELETE CASCADE,
    former_note_id TEXT NOT NULL,
    external_type TEXT NOT NULL CHECK (external_type IN ('obsidian','notion')),
    external_id TEXT NOT NULL DEFAULT '',
    external_path TEXT NOT NULL DEFAULT '',
    reason TEXT NOT NULL CHECK (reason IN ('user_unbound','target_changed','note_deleted')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (target_id, former_note_id, external_type)
  );

  ALTER TABLE note_sync_state
    DROP CONSTRAINT IF EXISTS note_sync_state_last_direction_check;
  ALTER TABLE note_sync_state
    ADD CONSTRAINT note_sync_state_last_direction_check
    CHECK (last_direction IN ('push','pull','bidirectional','delete','delete_detected',''));
  ```
- [ ] Update SQLite schema `backend/db/schema.sql` with equivalent tables and indexes:
  - `sync_targets.is_default INTEGER NOT NULL DEFAULT 0`
  - unique partial index `sync_targets_one_default_per_type_idx`
  - `note_sync_bindings`
  - `sync_external_claims`
  - `note_sync_suppressions`
  - `sync_import_tombstones`
  - `note_sync_state.last_direction` accepts `delete_detected`.
- [ ] Implement repository methods in:
  - `backend/internal/storage/postgres/sync.go`
  - `backend/internal/storage/sqlite/sync.go`
- [ ] Update all target scan/select SQL to include `is_default`.
- [ ] Change `GetDefaultTarget(ctx, targetType)` to:
  - return only `WHERE type = ? AND enabled = true AND is_default = true`
  - return `nil` when no default exists
  - never use `ORDER BY updated_at DESC`.
- [ ] Ensure `SaveTarget` clears other defaults for the same type inside the same transaction when `IsDefault` is true.
- [ ] Run green contract tests:
  ```powershell
  Set-Location D:\MyGitProject\all_note\backend
  go test ./internal/storage/contracttest ./internal/storage/postgres ./internal/storage/sqlite
  ```
- [ ] Commit:
  ```powershell
  git add backend/db/schema.sql backend/db/migrations/postgres/0001_init_postgres.sql backend/internal/storage/postgres/sync.go backend/internal/storage/sqlite/sync.go
  git commit -m "feat: add single sync target storage"
  ```

## Task 3: Add Target Identity Locking

- [ ] Add tests:
  - `backend/internal/handler/sync_target_test.go`
    - `TestPatchSyncTargetAllowsDisplayFieldsWhenUsed`
    - `TestPatchSyncTargetRejectsObsidianIdentityChangeWhenUsed`
    - `TestPatchSyncTargetRejectsNotionDataSourceChangeWhenUsed`
    - `TestDeleteSyncTargetRejectsBoundTarget`
    - `TestDeleteSyncTargetDeletesUnusedTarget`
  - `backend/internal/storage/contracttest/sync_binding_contract_tests.go`
    - `TestSyncBindingContractCountsStatesAsTargetUsage`
- [ ] Add repository helper logic:
  - Obsidian identity fields: canonical `vault_path` and `base_folder`.
  - Notion identity field: normalized `config.data_source_id`.
- [ ] Update handler `backend/internal/handler/sync.go`:
  - `PATCH /api/sync/targets/:id` may update `name`, `enabled`, `auto_sync`, token env var, and non-identity property mappings when used.
  - Used target identity changes return `409` with code `target_identity_locked`.
  - `DELETE /api/sync/targets/:id` returns `409` if binding, claim, or state count is non-zero.
- [ ] Register delete route in `backend/internal/router/router.go`.
- [ ] Run:
  ```powershell
  Set-Location D:\MyGitProject\all_note\backend
  go test ./internal/handler ./internal/storage/contracttest ./internal/storage/postgres ./internal/storage/sqlite
  ```
- [ ] Commit:
  ```powershell
  git add backend/internal/handler/sync.go backend/internal/router/router.go backend/internal/handler/sync_target_test.go backend/internal/storage
  git commit -m "feat: lock used sync target identity"
  ```

## Task 4: Add SQLite and SQLite-to-PostgreSQL Migration Coverage

- [ ] Update SQLite runtime migration code in `backend/internal/repository/db.go` or the active SQLite provider migration path so old local files get:
  - `sync_targets.is_default`
  - new binding/claim/suppression/tombstone tables
  - default target backfill per type when exactly one enabled target exists.
- [ ] Update `backend/internal/migration/sqlite_to_pg.go`:
  - include `note_sync_bindings`
  - include `sync_external_claims`
  - include `note_sync_suppressions`
  - include `sync_import_tombstones`
  - include `sync_targets.is_default`
  - map historic `note_sync_state.last_direction='delete_detected'` without loss.
- [ ] Add migration tests:
  - `TestSQLiteMigrationAddsSingleSyncTargetTables`
  - `TestSQLiteToPostgresMigratesSyncBindingsClaimsSuppressionsTombstones`
  - `TestSQLiteToPostgresBackfillsInitialBindingBeforeServiceSwitch`
  - `TestSQLiteToPostgresPreservesDeleteDetectedDirection`
  - `TestSQLiteToPostgresRejectsDuplicateDefaultTargets`
- [ ] Initial binding backfill rule:
  - for each `note_sync_state` row whose target still exists and status is not terminal external deletion, create one binding if that note has no binding.
  - if a note has multiple historical targets, choose the newest non-deleted state and create tombstones for losing claimed resources with reason `target_changed`.
  - create active claim only when external identifier is present and the external resource still maps to the selected target.
- [ ] Run:
  ```powershell
  Set-Location D:\MyGitProject\all_note\backend
  go test ./internal/migration ./internal/repository ./internal/storage/...
  ```
- [ ] Commit:
  ```powershell
  git add backend/internal/migration backend/internal/repository/db.go backend/db/schema.sql backend/db/migrations/postgres/0001_init_postgres.sql
  git commit -m "feat: migrate sync bindings and claims"
  ```

## Task 5: Add Binding Handler API

- [ ] Add handler tests in `backend/internal/handler/sync_binding_test.go`:
  - `TestGetNoteSyncBindingReturnsNullWhenUnbound`
  - `TestPutNoteSyncBindingCreatesBinding`
  - `TestPutNoteSyncBindingRequiresConfirmWhenChangingTarget`
  - `TestPutNoteSyncBindingRejectsExpectedTargetMismatch`
  - `TestPutNoteSyncBindingDeletesSuppressionAndTombstoneOnExplicitRebind`
  - `TestDeleteNoteSyncBindingRequiresExpectedTarget`
  - `TestDeleteNoteSyncBindingRejectsExpectedUpdatedAtMismatch`
  - `TestDeleteNoteSyncBindingWritesSuppressionAndTombstoneBeforeClaimRelease`
- [ ] Add routes in `backend/internal/router/router.go`:
  ```text
  GET    /api/notes/:id/sync-binding
  PUT    /api/notes/:id/sync-binding
  DELETE /api/notes/:id/sync-binding
  ```
- [ ] Implement handler methods in `backend/internal/handler/sync.go` or `backend/internal/handler/sync_binding.go`.
- [ ] On `PUT`:
  - validate target exists and is enabled
  - reject stale `expected_target_id`
  - require `confirm_changed_target=true` when changing target
  - inside `Store.Transact`, remove suppression and tombstones for the note/target, write binding, and release old claim after tombstone creation.
- [ ] On `DELETE`:
  - validate `expected_target_id` and `expected_updated_at`
  - inside `Store.Transact`, read active claim by note, write tombstone if claim exists, write suppression, release claim, delete binding.
- [ ] Run:
  ```powershell
  Set-Location D:\MyGitProject\all_note\backend
  go test ./internal/handler ./internal/storage/...
  ```
- [ ] Commit:
  ```powershell
  git add backend/internal/handler backend/internal/router backend/internal/storage
  git commit -m "feat: add note sync binding API"
  ```

## Task 6: Update Sync-State Compatibility Semantics

- [ ] Add tests in `backend/internal/handler/sync_state_compat_test.go`:
  - `TestGetNoteSyncStateReturnsBindingMismatchForOtherType`
  - `TestGetNoteSyncStateReturnsDefaultTargetMissingWhenNoBindingAndNoDefault`
  - `TestLegacyExecutionReturnsDefaultTargetMissingConflict`
  - `TestLegacyExecutionReturnsBindingMismatchConflict`
- [ ] Update `GetNoteSyncState`:
  - when note has binding and query target type mismatches, return `200` with `binding_mismatch=true`, `bound_target_id`, and `bound_target_name`.
  - when note has no binding and query target type has no default, return `200` with `default_target_missing=true`.
- [ ] Update legacy execution handlers:
  - no binding and no default target returns `409 default_target_missing`.
  - existing binding for a different target/type returns `409 binding_mismatch`.
- [ ] Run:
  ```powershell
  Set-Location D:\MyGitProject\all_note\backend
  go test ./internal/handler
  ```
- [ ] Commit:
  ```powershell
  git add backend/internal/handler backend/internal/model
  git commit -m "feat: define sync state compatibility responses"
  ```

## Task 7: Add Unified Push and Target-Scoped Sync APIs

- [ ] Add service tests:
  - `backend/internal/service/sync_dispatch_test.go`
    - `TestSyncNoteRequiresBinding`
    - `TestSyncNoteDispatchesToBoundObsidianTarget`
    - `TestSyncNoteDispatchesToBoundNotionTarget`
    - `TestSyncNoteIgnoresRequiredTagsForExplicitBoundNote`
    - `TestTargetPushOnlyProcessesBoundNotesForTarget`
    - `TestTargetPullRejectsBindingConflict`
    - `TestTargetBidirectionalUsesTargetScope`
- [ ] Add handler tests:
  - `TestPostUnifiedNoteSyncRequiresBinding`
  - `TestPostTargetPushReturnsTargetNotFound`
  - `TestPostTargetPullReturnsConflictForForeignBinding`
- [ ] Add routes:
  ```text
  POST /api/sync/notes/:id
  POST /api/sync/targets/:target_id/push
  POST /api/sync/targets/:target_id/pull
  POST /api/sync/targets/:target_id/bidirectional
  ```
- [ ] Implement `backend/internal/service/sync_dispatch.go`:
  - load binding
  - load target
  - dispatch by target type
  - pass explicit target to Notion/Obsidian services
  - never apply `required_tags` skip to explicit bound note sync.
- [ ] Batch push behavior:
  - list bindings for the target
  - sync those notes only
  - skip unbound notes even if tags match.
- [ ] Run:
  ```powershell
  Set-Location D:\MyGitProject\all_note\backend
  go test ./internal/service ./internal/handler
  ```
- [ ] Commit:
  ```powershell
  git add backend/internal/service backend/internal/handler backend/internal/router
  git commit -m "feat: route sync through note bindings"
  ```

## Task 8: Harden Obsidian Claim, Tombstone, and Deletion Flows

- [ ] Add Obsidian tests:
  - `TestObsidianPushReservesClaimBeforeWrite`
  - `TestObsidianPushClaimFailureDoesNotWriteFile`
  - `TestObsidianPullDoesNotAutoBindSuppressedNote`
  - `TestObsidianPullDetectsForeignBindingByFlowSpaceID`
  - `TestObsidianPullChecksTombstoneAfterPathRename`
  - `TestObsidianDeleteCandidatesAreTargetScoped`
  - `TestObsidianConfirmDeletionUsesTargetID`
  - `TestObsidianRestoreDeletionUsesTargetID`
- [ ] Update `backend/internal/service/obsidian_sync.go` and `backend/internal/service/obsidian_bidirectional.go`:
  - canonical external key remains canonical path.
  - reserve active claim in a transaction before `os.WriteFile`.
  - if claim reservation fails, do not write the file.
  - pull/import lookup order:
    1. active claim by external key
    2. FlowSpace ID note lookup
    3. tombstone by external key
    4. tombstone by `(target_id, former_note_id, external_type)`
    5. tombstone by `(former_note_id, external_type)` for FlowSpace ID resources
  - if existing note is bound to another target, return binding conflict and do not update note.
  - if note has suppression for target, return binding required and do not bind.
- [ ] Add target-scoped deletion APIs:
  ```text
  GET  /api/sync/targets/:target_id/deletions
  POST /api/sync/targets/:target_id/deletions/:note_id/confirm
  POST /api/sync/targets/:target_id/deletions/:note_id/restore
  ```
- [ ] Run:
  ```powershell
  Set-Location D:\MyGitProject\all_note\backend
  go test ./internal/service ./internal/handler
  ```
- [ ] Commit:
  ```powershell
  git add backend/internal/service backend/internal/handler backend/internal/router
  git commit -m "feat: bind obsidian sync to one target"
  ```

## Task 9: Harden Notion Claim, Tombstone, and Recovery Flows

- [ ] Add Notion tests:
  - `TestNotionUpdateUsesExistingClaim`
  - `TestNotionCreatePageRecordsClaimAndState`
  - `TestNotionCreatePageDatabaseFailureLeavesRecoverableFlowSpaceID`
  - `TestNotionPullDoesNotAutoBindSuppressedNote`
  - `TestNotionPullDetectsForeignBindingByFlowSpaceID`
  - `TestNotionPullChecksTombstoneByFormerNoteID`
  - `TestNotionDeleteCandidatesAreTargetScoped`
  - `TestNotionConfirmDeletionUsesTargetID`
  - `TestNotionRestoreDeletionUsesTargetID`
- [ ] Update `backend/internal/service/notion_bidirectional.go`:
  - use external key `notion:<page_id>`.
  - update existing pages only through active claim or current binding.
  - when creating a new page, include FlowSpace ID in Notion properties before local claim write.
  - if local state/claim write fails after remote page creation, retry path must detect the remote page by FlowSpace ID and complete local claim rather than creating another page.
  - pull/import conflict rules match Obsidian.
- [ ] Ensure all new state/claim writes and note updates use `Store.Transact`.
- [ ] Run:
  ```powershell
  Set-Location D:\MyGitProject\all_note\backend
  go test ./internal/service ./internal/handler
  ```
- [ ] Commit:
  ```powershell
  git add backend/internal/service backend/internal/handler
  git commit -m "feat: bind notion sync to one target"
  ```

## Task 10: Add Note Deletion Tombstone Transaction

- [ ] Add tests:
  - `backend/internal/service/notes_delete_test.go`
    - `TestDeleteNoteWritesTombstoneBeforeDeletingBoundNote`
    - `TestDeleteNoteWithoutClaimDeletesNormally`
    - `TestDeleteNoteRollsBackWhenTombstoneWriteFails`
- [ ] Move HTTP note deletion to a service method if it currently calls repository directly.
- [ ] Implement delete flow:
  - inside `Store.Transact`
  - load active claim by note
  - write `sync_import_tombstones` with reason `note_deleted`
  - delete note
  - rely on cascade to remove binding, claim, suppression, and state.
- [ ] Run:
  ```powershell
  Set-Location D:\MyGitProject\all_note\backend
  go test ./internal/service ./internal/handler
  ```
- [ ] Commit:
  ```powershell
  git add backend/internal/service backend/internal/handler backend/internal/router
  git commit -m "feat: preserve sync tombstones on note delete"
  ```

## Task 11: Update Frontend API and Hooks

- [ ] Update `frontend/src/api/sync.ts`:
  - add `is_default` to `SyncTarget`.
  - add binding types and functions:
    ```ts
    getNoteSyncBinding(noteId: string)
    putNoteSyncBinding(noteId: string, payload: SaveNoteSyncBindingRequest)
    deleteNoteSyncBinding(noteId: string, payload: DeleteNoteSyncBindingRequest)
    syncNote(noteId: string)
    pushTarget(targetId: string)
    pullTarget(targetId: string)
    bidirectionalTarget(targetId: string)
    getTargetDeletions(targetId: string)
    confirmTargetDeletion(targetId: string, noteId: string)
    restoreTargetDeletion(targetId: string, noteId: string)
    deleteSyncTarget(targetId: string)
    ```
- [ ] Update `frontend/src/hooks/useSync.ts` with hook wrappers for the new API functions.
- [ ] Add frontend tests:
  - `frontend/src/api/sync.test.ts`
    - `getNoteSyncBinding calls note binding endpoint`
    - `putNoteSyncBinding sends confirm_changed_target`
    - `deleteNoteSyncBinding sends optimistic concurrency fields`
    - `syncNote calls unified endpoint`
    - `target scoped deletion calls include target id`
  - `frontend/src/hooks/useSync.test.tsx`
    - hook methods expose binding and target-scoped calls.
- [ ] Run:
  ```powershell
  Set-Location D:\MyGitProject\all_note\frontend
  npm run test:unit -- src/api/sync.test.ts src/hooks/useSync.test.tsx
  ```
- [ ] Commit:
  ```powershell
  git add frontend/src/api/sync.ts frontend/src/api/sync.test.ts frontend/src/hooks/useSync.ts frontend/src/hooks/useSync.test.tsx
  git commit -m "feat: add frontend sync binding API"
  ```

## Task 12: Replace Note Sync UI with One Target Selector

- [ ] Update `frontend/src/components/sync/NoteSyncCard.tsx`:
  - show one selector of enabled target names.
  - include "不同步" option.
  - display current binding target name and type.
  - warn before changing target; only send `confirm_changed_target=true` after user confirmation.
  - deleting binding sends `expected_target_id` and `expected_updated_at`.
  - show `binding_mismatch`, `default_target_missing`, `binding_required`, and `target_identity_locked` messages in Chinese.
  - one click "同步此笔记" calls `POST /api/sync/notes/:id`.
- [ ] Add/replace tests in `frontend/src/components/sync/NoteSyncCard.test.tsx`:
  - `renders single sync target selector`
  - `unbound note defaults to do not sync`
  - `selecting target creates binding`
  - `changing target requires confirmation`
  - `choosing do not sync deletes binding with expected fields`
  - `sync button uses unified note endpoint`
  - `mismatch response is shown in Chinese`
- [ ] Remove old two-card Notion/Obsidian selection behavior from this component only; keep target settings panels for managing targets.
- [ ] Run:
  ```powershell
  Set-Location D:\MyGitProject\all_note\frontend
  npm run test:unit -- src/components/sync/NoteSyncCard.test.tsx
  ```
- [ ] Commit:
  ```powershell
  git add frontend/src/components/sync/NoteSyncCard.tsx frontend/src/components/sync/NoteSyncCard.test.tsx
  git commit -m "feat: use one sync target per note"
  ```

## Task 13: Update Sync Settings UI for Defaults and Identity Lock

- [ ] Update:
  - `frontend/src/components/sync/SyncSettingsPanel.tsx`
  - `frontend/src/components/sync/NotionSyncPanel.tsx`
  - `frontend/src/components/sync/ObsidianSyncPanel.tsx`
- [ ] UI requirements:
  - target list shows default badge per type.
  - user can mark one enabled target as default.
  - target delete is disabled or confirmed when unused; backend `409` is shown in Chinese.
  - used targets show locked identity fields.
  - editing locked identity field shows explanation before submit and handles `target_identity_locked`.
  - target-scoped push/pull/bidirectional buttons use target IDs.
  - target-scoped deletion candidates use target ID routes.
- [ ] Add tests:
  - `SyncSettingsPanel.test.tsx`
    - `marks one default target per type`
    - `shows target identity locked error`
    - `deletes unused target`
  - `NotionSyncPanel.test.tsx`
    - `uses target scoped push and pull`
    - `shows data source id as locked when backend reports lock`
  - `ObsidianSyncPanel.test.tsx`
    - `uses target scoped deletion candidates`
    - `shows vault path as locked when backend reports lock`
- [ ] Run:
  ```powershell
  Set-Location D:\MyGitProject\all_note\frontend
  npm run test:unit -- src/components/sync/SyncSettingsPanel.test.tsx src/components/sync/NotionSyncPanel.test.tsx src/components/sync/ObsidianSyncPanel.test.tsx
  ```
- [ ] Commit:
  ```powershell
  git add frontend/src/components/sync
  git commit -m "feat: manage sync target defaults and locks"
  ```

## Task 14: Backend Full Verification

- [ ] Run full backend tests:
  ```powershell
  Set-Location D:\MyGitProject\all_note\backend
  go test ./...
  ```
- [ ] If PostgreSQL integration tests require the local test database, use the configured test connection:
  ```powershell
  $env:FLOWSPACE_DATABASE_DRIVER="postgres"
  $env:FLOWSPACE_DATABASE_URL="postgres://postgres:12345@192.168.1.70:19588/flowspace_test?sslmode=disable"
  go test ./...
  ```
- [ ] Verify SQLite provider still passes:
  ```powershell
  $env:FLOWSPACE_DATABASE_DRIVER="sqlite"
  Remove-Item Env:\FLOWSPACE_DATABASE_URL -ErrorAction SilentlyContinue
  go test ./...
  ```
- [ ] Commit fixes only after both provider modes pass.

## Task 15: Frontend Full Verification

- [ ] Run frontend tests:
  ```powershell
  Set-Location D:\MyGitProject\all_note\frontend
  npm run test:unit
  npm run build
  ```
- [ ] Check rendered UI in the test app:
  - open `http://localhost:4100/all-note-test/notes`
  - open a note editor
  - verify one sync target selector appears
  - switch from "不同步" to a Notion or Obsidian target
  - switch target and confirm warning
  - choose "不同步" and verify it remains unbound after pull/import.
- [ ] Commit frontend fixes:
  ```powershell
  git add frontend
  git commit -m "test: verify sync binding frontend"
  ```

## Task 16: Documentation and Final Cleanup

- [ ] Update docs:
  - `docs/postgres-storage-design.md` if schema/provider docs list sync tables.
  - the user-facing sync documentation that explains Notion/Obsidian setup.
  - test/prod service port docs only if any endpoint examples changed.
- [ ] Include user-facing behavior:
  - "默认不同步"
  - "每篇笔记只能选择一个同步目标"
  - "解除绑定后外部资源不会自动重新绑定，需要手动确认"
  - "目标已被使用后不能修改外部空间身份字段"
- [ ] Run final checks:
  ```powershell
  Set-Location D:\MyGitProject\all_note
  git diff --check
  git status --short
  ```
- [ ] Leave unrelated local files untouched:
  - `.serena/`
  - `backend/internal/storage/sqlite/tasks.go.tmp`
- [ ] Final commit:
  ```powershell
  git add docs backend frontend
  git commit -m "docs: document single sync target behavior"
  ```

## Implementation Checkpoints

Use these checkpoints to avoid merging a half-compatible version:

1. Storage checkpoint: Task 1 and Task 2 complete; both PostgreSQL and SQLite contract suites pass.
2. Migration checkpoint: Task 4 complete; existing synchronized notes have initial bindings before services enforce binding-only push.
3. Backend checkpoint: Task 5 through Task 10 complete; all handler/service tests pass.
4. Frontend checkpoint: Task 11 through Task 13 complete; UI exposes exactly one target selector per note.
5. Release checkpoint: Task 14 through Task 16 complete; backend full test, frontend unit test, frontend build, and manual smoke are recorded.

## Rollback Notes

- Schema changes are additive except the strengthened `note_sync_state.last_direction` check replacement. If deployment fails before traffic uses new APIs, roll back the application binary and keep the additive tables in place.
- If binding migration produces incorrect bindings, stop sync services, restore from the pre-migration database backup, fix the migration fixture, and rerun migration. Do not hand-edit `sync_external_claims` without also checking `note_sync_bindings` and tombstones.
- If a target identity was accidentally changed before the lock code ships, create a new target for the new external space and restore the old target identity from backup before allowing pull/import.
