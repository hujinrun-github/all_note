import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  confirmNotionDeletion,
  getNoteSyncState,
  getNotionDeletions,
  restoreNotionDeletion,
  saveSyncTarget,
  syncNotionBidirectional,
  testNotionTarget,
} from './sync'

describe('notion sync api', () => {
  beforeEach(() => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => {
        return new Response(
          JSON.stringify({ data: { target: { id: 'target-1' }, result: { imported: 1 }, items: [], state: null } }),
          {
            status: 200,
            headers: { 'Content-Type': 'application/json' },
          },
        )
      }),
    )
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('saves notion target without sending a token', async () => {
    await saveSyncTarget({
      type: 'notion',
      name: 'Personal Notion',
      vault_path: '',
      base_folder: '',
      config_json: JSON.stringify({ data_source_id: 'ds-123', token_env: 'FLOWSPACE_NOTION_TOKEN' }),
      enabled: true,
      auto_sync: false,
    })

    const fetchMock = vi.mocked(fetch)
    const [, init] = fetchMock.mock.calls[0]
    expect(String(fetchMock.mock.calls[0][0])).toContain('/api/sync/targets')
    expect(init?.method).toBe('POST')
    expect(JSON.parse(String(init?.body))).toEqual({
      type: 'notion',
      name: 'Personal Notion',
      vault_path: '',
      base_folder: '',
      config_json: JSON.stringify({ data_source_id: 'ds-123', token_env: 'FLOWSPACE_NOTION_TOKEN' }),
      enabled: true,
      auto_sync: false,
    })
    expect(String(init?.body)).not.toContain('secret')
  })

  it('calls notion endpoints with encoded ids', async () => {
    await testNotionTarget({
      type: 'notion',
      name: 'Personal Notion',
      vault_path: '',
      base_folder: '',
      config_json: JSON.stringify({ data_source_id: 'ds-123' }),
      enabled: true,
      auto_sync: false,
    })
    await syncNotionBidirectional()
    await getNotionDeletions()
    await confirmNotionDeletion('note/1')
    await restoreNotionDeletion('note/1')
    await getNoteSyncState('note/1', 'notion')

    const paths = vi.mocked(fetch).mock.calls.map(([input]) => String(input))
    expect(paths.some((path) => path.includes('/api/sync/notion/test'))).toBe(true)
    expect(paths.some((path) => path.includes('/api/sync/notion/bidirectional'))).toBe(true)
    expect(paths.some((path) => path.includes('/api/sync/notion/deletions'))).toBe(true)
    expect(paths.some((path) => path.includes('/api/sync/notion/deletions/note%2F1/confirm'))).toBe(true)
    expect(paths.some((path) => path.includes('/api/sync/notion/deletions/note%2F1/restore'))).toBe(true)
    expect(paths.some((path) => path.includes('/api/notes/note%2F1/sync-state?target=notion'))).toBe(true)
  })
})
