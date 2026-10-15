/**
 * Runnable usage examples for the S1 lib layer.
 *
 * These are real assertions with printed output, not documentation snippets:
 * `npm test` prints them, so the values below are always the actual behaviour
 * of the current code.
 */

import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

import { requestDigest } from '../lib/digest'
import { uuidV4, isUUID } from '../lib/uuid'
import { formatInstant, formatLocalDate, formatLocalTime, zoneOffsetMinutes } from '../lib/time'
import { compareDecimalString, atLeast } from '../lib/bignum'
import { ALL_SCOPES, CONTRACT_SHA256, SLIDING_SCOPES, assertContract } from '../lib/contract'

const here = dirname(fileURLToPath(import.meta.url))
const vectors = JSON.parse(
  readFileSync(join(here, '..', '..', 'backend', 'testdata', 'mobile-v2', 'miniprogram-vectors.json'), 'utf8'),
) as Array<{ name: string; expected: string }>

function show(label: string, value: unknown): void {
  console.log('  ' + label + ' = ' + (typeof value === 'string' ? value : JSON.stringify(value)))
}

describe('examples', () => {
  it('builds a command envelope and its request digest', () => {
    console.log('\n[example] command envelope -> request_digest')
    const envelope = {
      command_id: 'e5e5e5e5-e5e5-4e5e-8e5e-e5e5e5e5e5e5',
      origin_device_client_id: 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb',
      workspace_id: 'workspace-1',
      command_type: 'note.update',
      target: { entity_id: 'n-1', client_id: null },
      created_runtime_epoch: '8',
      expected: { entity_revision: { source: 'exact', value: '3' } },
      depends_on_command_id: null,
      supersedes_command_id: null,
      payload: {
        title: '晨间笔记',
        content: '今天要完成 #任务 与 😀 复盘',
        revision: '9007199254740993',
        flag: true,
        count: 42,
        ratio: 0.5,
      },
    }
    const digest = requestDigest(envelope)
    show('digest', digest)
    const expected = vectors.find((item) => item.name === 'request-digest-realistic-note-update')?.expected
    show('matches Go vector', String(digest === expected))
    expect(digest).toBe(expected)
  })

  it('generates command ids', () => {
    console.log('\n[example] uuid v4')
    const deterministic = uuidV4(() => 0.5)
    show('uuidV4(random=0.5)', deterministic)
    show('isUUID', String(isUUID(deterministic)))
    expect(isUUID(deterministic)).toBe(true)
    expect(isUUID('E5E5E5E5-E5E5-4E5E-8E5E-E5E5E5E5E5E5')).toBe(false)
  })

  it('formats protocol timestamps', () => {
    console.log('\n[example] time formatting (Asia/Shanghai = +08:00)')
    const offset = zoneOffsetMinutes('Asia/Shanghai')
    const at = Date.UTC(2026, 9, 12, 1, 34, 56, 789)
    show('instant', formatInstant(at))
    show('local date', formatLocalDate(at, offset))
    show('local time', formatLocalTime(at, offset))
    expect(formatInstant(at)).toBe('2026-10-12T01:34:56.789Z')
    expect(formatLocalDate(at, offset)).toBe('2026-10-12')
    expect(formatLocalTime(at, offset)).toBe('09:34:56')
  })

  it('compares revisions without losing precision', () => {
    console.log('\n[example] big revision strings')
    const revision = '9007199254740993'
    show('revision', revision)
    show('as JS number (lossy)', String(Number(revision)))
    show('compare(revision, "9007199254740992")', String(compareDecimalString(revision, '9007199254740992')))
    show('atLeast(revision, "9007199254740993")', String(atLeast(revision, '9007199254740993')))
    expect(Number(revision)).toBe(9007199254740992)
    expect(compareDecimalString(revision, '9007199254740992')).toBe(1)
  })

  it('exposes the frozen contract constants', () => {
    console.log('\n[example] contract constants and scopes')
    show('contract_sha256', CONTRACT_SHA256)
    show('scopes', ALL_SCOPES)
    show('sliding (need projection_time_zone)', SLIDING_SCOPES)
    expect(() => assertContract(CONTRACT_SHA256)).not.toThrow()
    expect(() => assertContract('deadbeef')).toThrow(/contract mismatch/)
  })
})
