import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import * as contentImports from '../../api/contentImports'
import { PodcastSourceCard } from './PodcastSourceCard'

vi.mock('../../api/contentImports')

function renderCard() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <PodcastSourceCard noteID="note-1" />
    </QueryClientProvider>
  )
}

describe('PodcastSourceCard', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(contentImports.getContentImportForNote).mockResolvedValue({
      id: 'import-1',
      source_url: 'https://www.xiaoyuzhoufm.com/episode/episode-1',
      source_type: 'xiaoyuzhou',
      canonical_url: 'https://www.xiaoyuzhoufm.com/episode/episode-1',
      title: 'AI 产品经理的下一站',
      podcast_title: '产品沉思录',
      duration_seconds: 3723,
      status: 'completed',
      stage: 'completed',
      progress: 100,
      summarize_with_ai: true,
      include_transcript: false,
      language: 'zh',
      project_ids: [],
      tags: ['播客'],
      result_note_id: 'note-1',
      revision: 4,
      created_at: 1,
      updated_at: 2,
    })
    vi.mocked(contentImports.getContentImportTranscript).mockResolvedValue(
      '[00:00:00]\n产品经理需要先定义问题。'
    )
  })

  it('loads the transcript only after the user opens it', async () => {
    const user = userEvent.setup()
    renderCard()

    expect(await screen.findByText('产品沉思录 · AI 产品经理的下一站')).toBeVisible()
    expect(contentImports.getContentImportTranscript).not.toHaveBeenCalled()
    expect(screen.getByRole('link', { name: /打开原始链接/ })).toHaveAttribute(
      'href',
      'https://www.xiaoyuzhoufm.com/episode/episode-1'
    )

    await user.click(screen.getByRole('button', { name: /查看逐字稿/ }))

    expect(await screen.findByRole('dialog', { name: 'AI 产品经理的下一站' })).toBeVisible()
    expect(await screen.findByText(/产品经理需要先定义问题/)).toBeVisible()
    await waitFor(() =>
      expect(contentImports.getContentImportTranscript).toHaveBeenCalledWith(
        'import-1'
      )
    )
  })
})
