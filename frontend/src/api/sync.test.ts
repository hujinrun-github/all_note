import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  confirmNotionDeletion,
  getNoteSyncState,
  getNotionDeletions,
  restoreNotionDeletion,
  saveSyncTarget,
  syncNotionAll,
  syncNotionBidirectional,
  syncNotionPull,
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

  it('allowlists save target payload fields', async () => {
    await saveSyncTarget({
      id: 'target-1',
      type: 'notion',
      name: 'Personal Notion',
      vault_path: '',
      base_folder: '',
      config_json: JSON.stringify({ data_source_id: 'ds-123', token_env: 'FLOWSPACE_NOTION_TOKEN' }),
      enabled: true,
      auto_sync: false,
      token: 'secret-token',
      secret: 'secret-value',
    } as Parameters<typeof saveSyncTarget>[0] & { token: string; secret: string })

    const fetchMock = vi.mocked(fetch)
    const [input, init] = fetchMock.mock.calls[0]
    const body = JSON.parse(String(init?.body))

    expect(String(input)).toContain('/api/sync/targets/target-1')
    expect(init?.method).toBe('PATCH')
    expect(body).toEqual({
      type: 'notion',
      name: 'Personal Notion',
      vault_path: '',
      base_folder: '',
      config_json: JSON.stringify({ data_source_id: 'ds-123', token_env: 'FLOWSPACE_NOTION_TOKEN' }),
      enabled: true,
      auto_sync: false,
    })
    expect(body).not.toHaveProperty('id')
    expect(body).not.toHaveProperty('token')
    expect(body).not.toHaveProperty('secret')
    expect(String(init?.body)).not.toContain('secret-token')
    expect(String(init?.body)).not.toContain('secret-value')
  })

  it('includes explicit default flag when saving a target', async () => {
    await saveSyncTarget({
      id: 'target-1',
      type: 'notion',
      name: 'Personal Notion',
      vault_path: '',
      base_folder: '',
      config_json: JSON.stringify({ data_source_id: 'ds-123' }),
      enabled: true,
      auto_sync: false,
      is_default: true,
    })

    const [, init] = vi.mocked(fetch).mock.calls[0]
    expect(JSON.parse(String(init?.body))).toMatchObject({
      is_default: true,
    })
  })

  it('allowlists test notion target payload fields', async () => {
    await testNotionTarget({
      type: 'notion',
      name: 'Personal Notion',
      vault_path: '',
      base_folder: '',
      config_json: JSON.stringify({ data_source_id: 'ds-123' }),
      enabled: true,
      auto_sync: false,
      token: 'secret-token',
      secret: 'secret-value',
    } as Parameters<typeof testNotionTarget>[0] & { token: string; secret: string })

    const fetchMock = vi.mocked(fetch)
    const [input, init] = fetchMock.mock.calls[0]
    const body = JSON.parse(String(init?.body))

    expect(String(input)).toContain('/api/sync/notion/test')
    expect(body).toEqual({
      type: 'notion',
      name: 'Personal Notion',
      vault_path: '',
      base_folder: '',
      config_json: JSON.stringify({ data_source_id: 'ds-123' }),
      enabled: true,
      auto_sync: false,
    })
    expect(body).not.toHaveProperty('token')
    expect(body).not.toHaveProperty('secret')
    expect(String(init?.body)).not.toContain('secret-token')
    expect(String(init?.body)).not.toContain('secret-value')
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
    await syncNotionAll()
    await syncNotionPull()
    await getNotionDeletions()
    await confirmNotionDeletion('note/1')
    await restoreNotionDeletion('note/1')
    await getNoteSyncState('note/1', 'notion')

    const paths = vi.mocked(fetch).mock.calls.map(([input]) => String(input))
    expect(paths.some((path) => path.includes('/api/sync/notion/test'))).toBe(true)
    expect(paths.some((path) => path.includes('/api/sync/notion/bidirectional'))).toBe(true)
    expect(paths.some((path) => path.includes('/api/sync/notion/all'))).toBe(true)
    expect(paths.some((path) => path.includes('/api/sync/notion/pull'))).toBe(true)
    expect(paths.some((path) => path.includes('/api/sync/notion/deletions'))).toBe(true)
    expect(paths.some((path) => path.includes('/api/sync/notion/deletions/note%2F1/confirm'))).toBe(true)
    expect(paths.some((path) => path.includes('/api/sync/notion/deletions/note%2F1/restore'))).toBe(true)
    expect(paths.some((path) => path.includes('/api/notes/note%2F1/sync-state?target=notion'))).toBe(true)
  })
})
