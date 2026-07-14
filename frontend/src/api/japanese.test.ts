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
})
