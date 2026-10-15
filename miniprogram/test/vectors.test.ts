/**
 * Golden vectors shared with the Go server.
 *
 * The vectors live in backend/testdata/mobile-v2/miniprogram-vectors.json and
 * are validated on the Go side by
 * internal/mobilev2contract/miniprogram_vectors_test.go. Both sides read the
 * SAME file, so a contract change cannot be applied to one client only.
 */

import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

import { canonicalizeJSON } from '../lib/jcs'
import { manifestChecksum, pageChecksum, requestDigest } from '../lib/digest'
import { sha256Hex } from '../lib/sha256'

interface Vector {
  name: string
  kind: 'jcs' | 'request' | 'page' | 'manifest'
  input: unknown
  expected: string
}

const here = dirname(fileURLToPath(import.meta.url))
const vectors = JSON.parse(
  readFileSync(join(here, '..', '..', 'backend', 'testdata', 'mobile-v2', 'miniprogram-vectors.json'), 'utf8'),
) as Vector[]

describe('shared golden vectors', () => {
  it('loads a non-empty vector set', () => {
    expect(vectors.length).toBeGreaterThan(0)
  })

  for (const vector of vectors) {
    it(vector.name, () => {
      switch (vector.kind) {
        case 'jcs':
          expect(canonicalizeJSON(vector.input as string)).toBe(vector.expected)
          break
        case 'request':
          expect(requestDigest(vector.input)).toBe(vector.expected)
          break
        case 'page': {
          const input = vector.input as {
            snapshot_id: string
            page_index: number
            as_of_sequence: string
            entities: unknown[]
          }
          expect(
            pageChecksum(input.snapshot_id, input.page_index, input.as_of_sequence, input.entities),
          ).toBe(vector.expected)
          break
        }
        case 'manifest': {
          const input = vector.input as {
            snapshot_id: string
            as_of_sequence: string
            scope_generation: string
            page_checksums: string[]
          }
          expect(
            manifestChecksum(
              input.snapshot_id,
              input.as_of_sequence,
              input.scope_generation,
              input.page_checksums,
            ),
          ).toBe(vector.expected)
          break
        }
        default:
          throw new Error('unknown vector kind: ' + (vector as Vector).kind)
      }
    })
  }
})

describe('sha256', () => {
  it('matches published digests', () => {
    expect(sha256Hex('')).toBe('e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855')
    expect(sha256Hex('abc')).toBe('ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad')
  })

  it('encodes non-ASCII input as UTF-8', () => {
    expect(sha256Hex('中文')).toBe(sha256Hex(new TextEncoder().encode('中文')))
  })
})

describe('ordering rules', () => {
  it('sorts object keys by UTF-16 code unit, not code point', () => {
    // U+1F600 encodes as D83D DE00, so it sorts before the single unit U+E000
    // under UTF-16 — the opposite of what code point ordering would produce.
    // U+E000 is not a control character, so JCS emits it raw, not as \ue000.
    const privateUse = String.fromCharCode(0xe000)
    expect(canonicalizeJSON(`{"${privateUse}":2,"😀":1}`)).toBe(`{"😀":1,"${privateUse}":2}`)
  })

  it('keeps semantic sets on code point ordering instead', () => {
    // Same discriminating pair as above, but inside `entities`: here U+E000
    // sorts first because digest.go uses lessCodePoint. The digest differs from
    // the UTF-16 ordering, which is exactly what the shared vector asserts.
    const privateUse = String.fromCharCode(0xe000)
    const envelope = {
      command_id: 'a1a1a1a1-a1a1-4a1a-8a1a-a1a1a1a1a1a1',
      origin_device_client_id: 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb',
      workspace_id: 'workspace-1',
      command_type: 'note.update',
      target: { entity_id: 'n-1', client_id: null },
      created_runtime_epoch: '8',
      expected: { entity_revision: { source: 'exact', value: '3' } },
      depends_on_command_id: null,
      supersedes_command_id: null,
      payload: {
        entities: [
          { entity_type: '😀', entity_id: 'e-1', client_id: null },
          { entity_type: privateUse, entity_id: 'e-2', client_id: null },
        ],
      },
    }
    const vector = vectors.find((item) => item.name === 'request-digest-entities-codepoint-order')
    expect(vector).toBeDefined()
    expect(requestDigest(envelope)).toBe((vector as Vector).expected)
  })
})
