import { describe, expect, it, vi } from 'vitest'
import { api } from './client'
import { annotateJapanese } from './japanese'

vi.mock('./client', () => ({
  api: {
    post: vi.fn(),
  },
}))

describe('Japanese furigana api', () => {
  it('requests structured furigana segments', async () => {
    const segments = [
      { text: 'すぐ' },
      { text: '近', reading: 'ちか' },
      { text: 'く' },
    ]
    vi.mocked(api.post).mockResolvedValue({ data: { segments, source: 'ai' } })

    const result = await annotateJapanese('すぐ近く')

    expect(api.post).toHaveBeenCalledWith('/api/japanese/furigana', {
      text: 'すぐ近く',
    })
    expect(result).toEqual({ segments, source: 'ai' })
  })

  it('forwards an abort signal for superseded live requests', async () => {
    const controller = new AbortController()
    vi.mocked(api.post).mockResolvedValue({
      data: { segments: [{ text: '日本', reading: 'にほん' }], source: 'ai' },
    })

    await annotateJapanese('日本', {
      signal: controller.signal,
      mode: 'local',
    })

    expect(api.post).toHaveBeenLastCalledWith(
      '/api/japanese/furigana',
      { text: '日本', mode: 'local' },
      { signal: controller.signal }
    )
  })
})
