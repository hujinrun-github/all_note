import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  bidirectionalTarget,
  confirmTargetDeletion,
  deleteNoteSyncBinding,
  deleteSyncTarget,
  confirmNotionDeletion,
  getNoteSyncBinding,
  getNoteSyncState,
  getNotionDeletions,
  getTargetDeletions,
  pullTarget,
  pushTarget,
  putNoteSyncBinding,
  restoreNotionDeletion,
  restoreTargetDeletion,
  saveSyncTarget,
  syncNote,
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

  it('gets note sync binding from the binding endpoint', async () => {
    await getNoteSyncBinding('note/1')

    const [input] = vi.mocked(fetch).mock.calls[0]
    expect(String(input)).toContain('/api/notes/note%2F1/sync-binding')
  })

  it('puts note sync binding with change confirmation fields', async () => {
    await putNoteSyncBinding('note/1', {
      target_id: 'target-2',
      expected_target_id: 'target-1',
      confirm_changed_target: true,
    })

    const [input, init] = vi.mocked(fetch).mock.calls[0]
    expect(String(input)).toContain('/api/notes/note%2F1/sync-binding')
    expect(init?.method).toBe('PUT')
    expect(JSON.parse(String(init?.body))).toEqual({
      target_id: 'target-2',
      expected_target_id: 'target-1',
      confirm_changed_target: true,
    })
  })

  it('deletes note sync binding with optimistic concurrency fields', async () => {
    await deleteNoteSyncBinding('note/1', {
      expected_target_id: 'target-1',
      expected_updated_at: 123,
    })

    const [input, init] = vi.mocked(fetch).mock.calls[0]
    expect(String(input)).toContain('/api/notes/note%2F1/sync-binding')
    expect(init?.method).toBe('DELETE')
    expect(JSON.parse(String(init?.body))).toEqual({
      expected_target_id: 'target-1',
      expected_updated_at: 123,
    })
  })

  it('preserves delete error code and message', async () => {
    vi.mocked(fetch).mockResolvedValueOnce(
      new Response(JSON.stringify({ error: { code: 'sync_binding_conflict', message: 'stale binding' } }), {
        status: 409,
        headers: { 'Content-Type': 'application/json' },
      }),
    )

    await expect(
      deleteNoteSyncBinding('note/1', {
        expected_target_id: 'target-1',
        expected_updated_at: 123,
      }),
    ).rejects.toMatchObject({
      status: 409,
      code: 'sync_binding_conflict',
      message: 'stale binding',
    })
  })

  it('calls unified note sync endpoint', async () => {
    await syncNote('note/1')

    const [input, init] = vi.mocked(fetch).mock.calls[0]
    expect(String(input)).toContain('/api/sync/notes/note%2F1')
    expect(init?.method).toBe('POST')
  })

  it('calls target scoped sync and deletion endpoints with target id', async () => {
    await pushTarget('target/1')
    await pullTarget('target/1')
    await bidirectionalTarget('target/1')
    await getTargetDeletions('target/1')
    await confirmTargetDeletion('target/1', 'note/1')
    await restoreTargetDeletion('target/1', 'note/1')
    await deleteSyncTarget('target/1')

    const calls = vi.mocked(fetch).mock.calls
    const paths = calls.map(([input]) => String(input))
    expect(paths.some((path) => path.includes('/api/sync/targets/target%2F1/push'))).toBe(true)
    expect(paths.some((path) => path.includes('/api/sync/targets/target%2F1/pull'))).toBe(true)
    expect(paths.some((path) => path.includes('/api/sync/targets/target%2F1/bidirectional'))).toBe(true)
    expect(paths.some((path) => path.includes('/api/sync/targets/target%2F1/deletions'))).toBe(true)
    expect(paths.some((path) => path.includes('/api/sync/targets/target%2F1/deletions/note%2F1/confirm'))).toBe(true)
    expect(paths.some((path) => path.includes('/api/sync/targets/target%2F1/deletions/note%2F1/restore'))).toBe(true)
    expect(paths.some((path) => path.includes('/api/sync/targets/target%2F1'))).toBe(true)
    expect(calls.at(-1)?.[1]?.method).toBe('DELETE')
  })
})
