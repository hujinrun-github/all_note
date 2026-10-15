/**
 * mobile-v2 digest construction, ported from
 * backend/internal/mobilev2contract/digest.go
 *
 * A command is only accepted by the server when `request_digest` matches
 *   "sha256:" + hex(SHA256(JCS(envelope without request_digest and
 *                                      forwarded_by_device_client_id)))
 * and three arrays inside the envelope are re-ordered semantically before
 * canonicalization: `entities`, `identity_mappings`, `field_paths`.
 */

import { canonicalizeValue, lessCodePoint, JCSValue } from './jcs'
import { sha256Hex } from './sha256'

type AnyRecord = { [key: string]: JCSValue }

function asRecord(value: unknown): AnyRecord | null {
  if (value !== null && typeof value === 'object' && !Array.isArray(value)) {
    return value as AnyRecord
  }
  return null
}

function asString(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

/**
 * Canonical identity of an entity, matching digest.go:108-122.
 * schedule_version uses k:<task_id>#<schedule_revision>; everything else
 * prefers s:<entity_id> and falls back to c:<client_id>.
 */
function canonicalEntityIdentity(entity: AnyRecord): string {
  if (asString(entity['entity_type']) === 'schedule_version') {
    const payload = asRecord(entity['payload'])
    if (payload) {
      const taskID = asString(payload['task_id'])
      const revision = asString(payload['schedule_revision'])
      if (taskID !== '' && revision !== '') {
        return 'k:' + taskID + '#' + revision
      }
    }
  }
  const entityID = asString(entity['entity_id'])
  if (entityID !== '') {
    return 's:' + entityID
  }
  return 'c:' + asString(entity['client_id'])
}

function sortEntityValues(values: unknown[]): void {
  values.sort((left, right) => {
    const leftObject = asRecord(left) ?? {}
    const rightObject = asRecord(right) ?? {}
    const leftType = asString(leftObject['entity_type'])
    const rightType = asString(rightObject['entity_type'])
    if (leftType !== rightType) {
      return lessCodePoint(leftType, rightType) ? -1 : 1
    }
    const leftIdentity = canonicalEntityIdentity(leftObject)
    const rightIdentity = canonicalEntityIdentity(rightObject)
    return lessCodePoint(leftIdentity, rightIdentity) ? -1 : leftIdentity === rightIdentity ? 0 : 1
  })
}

function sortIdentityMappings(values: unknown[]): void {
  values.sort((left, right) => {
    const leftObject = asRecord(left) ?? {}
    const rightObject = asRecord(right) ?? {}
    const leftKey = [
      asString(leftObject['entity_type']),
      asString(leftObject['client_id']),
      asString(leftObject['entity_id']),
    ]
    const rightKey = [
      asString(rightObject['entity_type']),
      asString(rightObject['client_id']),
      asString(rightObject['entity_id']),
    ]
    for (let index = 0; index < leftKey.length; index += 1) {
      if (leftKey[index] !== rightKey[index]) {
        return lessCodePoint(leftKey[index], rightKey[index]) ? -1 : 1
      }
    }
    return 0
  })
}

/** digest.go:68-93. Mutates semantic sets in place before canonicalization. */
function normalizeMobileV2SemanticSets(value: unknown): void {
  if (Array.isArray(value)) {
    for (const child of value) {
      normalizeMobileV2SemanticSets(child)
    }
    return
  }
  const object = asRecord(value)
  if (!object) {
    return
  }
  for (const key of Object.keys(object)) {
    const child = object[key]
    normalizeMobileV2SemanticSets(child)
    if (!Array.isArray(child)) {
      continue
    }
    if (key === 'entities') {
      sortEntityValues(child)
    } else if (key === 'identity_mappings') {
      sortIdentityMappings(child)
    } else if (key === 'field_paths') {
      child.sort((left, right) => (lessCodePoint(String(left), String(right)) ? -1 : 1))
    }
  }
}

/**
 * Compute the request digest for a command envelope.
 *
 * The input object is NOT mutated: it is deep-copied first, then
 * `request_digest` and `forwarded_by_device_client_id` are dropped, matching
 * digest.go:22-23.
 */
export function requestDigest(envelope: unknown): string {
  const object = asRecord(envelope)
  if (!object) {
    throw new Error('command envelope must be an object')
  }
  const copy = JSON.parse(JSON.stringify(envelope)) as AnyRecord
  delete copy['request_digest']
  delete copy['forwarded_by_device_client_id']
  normalizeMobileV2SemanticSets(copy)
  return 'sha256:' + sha256Hex(canonicalizeValue(copy as JCSValue))
}

/** digest.go:28-44 */
export function pageChecksum(
  snapshotID: string,
  pageIndex: number,
  asOfSequence: string,
  entities: unknown[],
): string {
  const copy = JSON.parse(JSON.stringify(entities)) as unknown[]
  sortEntityValues(copy)
  return 'sha256:' +
    sha256Hex(
      canonicalizeValue({
        snapshot_id: snapshotID,
        page_index: pageIndex,
        as_of_sequence: asOfSequence,
        entities: copy as JCSValue,
      }),
    )
}

/** digest.go:46-57 */
export function manifestChecksum(
  snapshotID: string,
  asOfSequence: string,
  scopeGeneration: string,
  pageChecksums: string[],
): string {
  return 'sha256:' +
    sha256Hex(
      canonicalizeValue({
        snapshot_id: snapshotID,
        as_of_sequence: asOfSequence,
        scope_generation: scopeGeneration,
        page_checksums: pageChecksums.slice(),
      }),
    )
}
