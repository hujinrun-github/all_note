import { afterEach, describe, expect, it, vi } from 'vitest'
import { api } from './client'

describe('api client', () => {
  afterEach(() => {
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
  })

  it('includes browser credentials on API requests', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ data: { ok: true } }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    vi.stubGlobal('fetch', fetchMock)

    await api.get<{ ok: boolean }>('/api/example')

    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining('/api/example'),
      expect.objectContaining({ credentials: 'include' }),
    )
  })
})
