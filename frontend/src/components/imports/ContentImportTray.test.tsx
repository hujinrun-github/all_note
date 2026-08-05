import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import * as contentImports from '../../api/contentImports'
import { ContentImportTray } from './ContentImportTray'

vi.mock('../../api/contentImports')

function renderTray() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <MemoryRouter>
      <QueryClientProvider client={queryClient}>
        <ContentImportTray open onOpen={vi.fn()} onClose={vi.fn()} />
      </QueryClientProvider>
    </MemoryRouter>
  )
}

describe('ContentImportTray', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(contentImports.listContentImports).mockResolvedValue([
      {
        id: 'import-canceled',
        source_url: 'https://www.xiaoyuzhoufm.com/episode/canceled',
        source_type: 'xiaoyuzhou',
        title: '已取消的播客任务',
        podcast_title: '小宇宙',
        status: 'canceled',
        stage: 'terminal',
        progress: 35,
        summarize_with_ai: true,
        include_transcript: false,
        language: 'auto',
        project_ids: [],
        tags: ['播客'],
        error_code: 'canceled_by_user',
        revision: 2,
        created_at: 1,
        updated_at: 2,
      },
    ])
  })

  it('renders a static icon for a canceled import', async () => {
    renderTray()

    const canceledLabel = await screen.findByText('已取消')
    const canceledTask = canceledLabel.closest('article')

    expect(canceledTask).toHaveClass('is-canceled')
    expect(canceledTask?.querySelector('svg')).toBeInTheDocument()
    expect(canceledTask?.querySelector('.is-spinning')).not.toBeInTheDocument()
  })

  it('exposes an AI failure and retries the AI stage', async () => {
    vi.mocked(contentImports.listContentImports).mockResolvedValue([
      {
        id: 'import-ai-failed',
        source_url: 'https://www.xiaoyuzhoufm.com/episode/ai-failed',
        source_type: 'xiaoyuzhou',
        title: 'AI 调用失败的播客任务',
        podcast_title: '小宇宙',
        status: 'failed',
        stage: 'terminal',
        progress: 100,
        summarize_with_ai: true,
        include_transcript: false,
        language: 'auto',
        project_ids: [],
        tags: ['播客'],
        error_code: 'TEXT_AI_CALL_FAILED',
        error_message: '文本 AI 调用失败，逐字稿已保留，可直接重试 AI 整理',
        retryable: true,
        revision: 3,
        created_at: 1,
        updated_at: 3,
      },
    ])
    vi.mocked(contentImports.retryContentImport).mockResolvedValue({
      id: 'import-ai-failed',
      source_url: 'https://www.xiaoyuzhoufm.com/episode/ai-failed',
      status: 'active',
      stage: 'queued',
      progress: 0,
      summarize_with_ai: true,
      include_transcript: false,
      language: 'auto',
      project_ids: [],
      tags: ['播客'],
      retryable: false,
      revision: 4,
      created_at: 1,
      updated_at: 4,
    })
    const user = userEvent.setup()
    renderTray()

    expect(await screen.findByText('AI 整理失败')).toBeVisible()
    expect(screen.getByText(/逐字稿已保留/)).toBeVisible()
    await user.click(screen.getByRole('button', { name: '重试 AI 整理' }))

    await waitFor(() => {
      expect(
        vi.mocked(contentImports.retryContentImport).mock.calls[0]?.[0]
      ).toBe('import-ai-failed')
    })
  })

  it('deletes a terminal import after confirming without deleting its note', async () => {
    vi.mocked(contentImports.listContentImports).mockResolvedValue([
      {
        id: 'import-completed',
        source_url: 'https://www.xiaoyuzhoufm.com/episode/completed',
        source_type: 'xiaoyuzhou',
        title: '已完成的播客任务',
        podcast_title: '小宇宙',
        status: 'completed',
        stage: 'completed',
        progress: 100,
        summarize_with_ai: true,
        include_transcript: false,
        language: 'auto',
        project_ids: [],
        tags: ['播客'],
        result_note_id: 'note-1',
        result_note_available: true,
        revision: 3,
        created_at: 1,
        updated_at: 3,
      },
    ])
    vi.mocked(contentImports.deleteContentImport).mockResolvedValue(undefined)
    const user = userEvent.setup()
    renderTray()

    await user.click(
      await screen.findByRole('button', {
        name: '删除导入记录：已完成的播客任务',
      })
    )
    expect(
      screen.getByText('仅删除导入记录和逐字稿，已生成的笔记会保留。')
    ).toBeVisible()
    await user.click(screen.getByRole('button', { name: '确认删除' }))

    await waitFor(() => {
      expect(
        vi.mocked(contentImports.deleteContentImport).mock.calls[0]?.[0]
      ).toBe('import-completed')
    })
    expect(screen.queryByText('已完成的播客任务')).not.toBeInTheDocument()
  })

  it('marks a missing result note and still allows deleting the import record', async () => {
    vi.mocked(contentImports.listContentImports).mockResolvedValue([
      {
        id: 'import-note-deleted',
        source_url: 'https://www.xiaoyuzhoufm.com/episode/note-deleted',
        source_type: 'xiaoyuzhou',
        title: '笔记已删除的播客任务',
        podcast_title: '小宇宙',
        status: 'completed',
        stage: 'completed',
        progress: 100,
        summarize_with_ai: true,
        include_transcript: false,
        language: 'auto',
        project_ids: [],
        tags: ['播客'],
        result_note_id: 'note-deleted',
        result_note_available: false,
        revision: 3,
        created_at: 1,
        updated_at: 3,
      },
    ])
    const user = userEvent.setup()
    renderTray()

    expect(await screen.findByText('笔记已删除')).toBeVisible()
    expect(
      screen.queryByRole('button', { name: '打开笔记' })
    ).not.toBeInTheDocument()
    await user.click(
      screen.getByRole('button', {
        name: '删除导入记录：笔记已删除的播客任务',
      })
    )
    expect(
      screen.getByText('关联笔记已删除；将清理这条导入记录和逐字稿。')
    ).toBeVisible()
  })
})
