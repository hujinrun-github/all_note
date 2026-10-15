/**
 * Frozen mobile-v2 contract constants.
 *
 * Source: backend/api/mobile-v2.sha256 and
 * backend/internal/mobilev2contract/version.go
 *
 * The server does NOT enforce a minimum client build by default
 * (internal/mobilev2service/service.go:60-62 leaves minimum_build at 1), so the
 * client must self-check `contract_sha256` from GET /api/mobile/v2/capabilities
 * and refuse to sync on mismatch. See design doc §6.3 item 3.
 */

export const CONTRACT_SHA256 = 'c602dd87b040c706edb08da6aa8a8d99281e5ab5fae95b780b231774e595474c'
export const CONTRACT_EPOCH = '1'
export const SCHEMA_VERSION = 'mobile-v2'
export const TASK_MODEL_VERSION = 2

/** Sync scopes. Hyphenated, per internal/mobilev2sync/contract.go:16-19. */
export const SCOPE_CONTENT = 'iphone-content'
export const SCOPE_TASK_CORE = 'iphone-task-core'
export const SCOPE_OCCURRENCE_WINDOW = 'iphone-occurrence-window'

/** Stable scopes reject projection_time_zone with 410; the window scope needs it. */
export const SLIDING_SCOPES: readonly string[] = [SCOPE_OCCURRENCE_WINDOW]

export const ALL_SCOPES: readonly string[] = [SCOPE_TASK_CORE, SCOPE_CONTENT, SCOPE_OCCURRENCE_WINDOW]

/** Server cap on a single command envelope (internal/handler/mobile_v2.go:15). */
export const MAX_COMMAND_BYTES = 1024 * 1024

export class ContractMismatchError extends Error {
  constructor(readonly serverSHA256: string) {
    super(
      'mobile-v2 contract mismatch: client=' + CONTRACT_SHA256 + ' server=' + serverSHA256 + '; upgrade required',
    )
    this.name = 'ContractMismatchError'
  }
}

export function assertContract(serverSHA256: string): void {
  if (serverSHA256 !== CONTRACT_SHA256) {
    throw new ContractMismatchError(serverSHA256)
  }
}
